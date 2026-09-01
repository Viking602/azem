use std::{
    fs::{self, OpenOptions},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{Arc, Mutex, RwLock},
    thread,
    time::Duration,
};

use anyhow::{Context as _, Result, anyhow};
use async_channel::{Receiver, Sender};
use azem_ipc::{Client, ClientEvent, Endpoint, Method};
use serde_json::Value;
use sha2::{Digest, Sha256};
use uuid::Uuid;

#[derive(Clone, Debug)]
pub enum RuntimeCommand {
    Request {
        id: String,
        method: Method,
        payload: Value,
    },
    UploadAttachment {
        id: String,
        session_id: String,
        path: PathBuf,
        name: String,
        mime_type: String,
    },
    Disconnect {
        stop_daemon: bool,
        completed: std::sync::mpsc::SyncSender<()>,
    },
}

#[derive(Clone, Debug)]
pub enum RuntimeMessage {
    Connecting(String),
    Connected {
        pid: u32,
        sequence: u64,
    },
    Snapshot(Value),
    Event(ClientEvent),
    Response {
        id: String,
        result: Result<Value, String>,
    },
}

#[derive(Clone)]
pub struct RuntimeConnection {
    commands: Sender<RuntimeCommand>,
    message_tx: Sender<RuntimeMessage>,
    pub messages: Receiver<RuntimeMessage>,
    snapshot_session: Arc<RwLock<String>>,
    startup: Arc<Mutex<std::sync::mpsc::Receiver<()>>>,
}

impl RuntimeConnection {
    pub fn start(options: RuntimeOptions) -> Self {
        let (commands_tx, commands_rx) = async_channel::bounded(256);
        let (messages_tx, messages_rx) = async_channel::bounded(4096);
        let supervisor_messages = messages_tx.clone();
        let (startup_tx, startup_rx) = std::sync::mpsc::sync_channel(1);
        let snapshot_session =
            Arc::new(RwLock::new(options.session_id.clone().unwrap_or_default()));
        let supervisor_snapshot_session = snapshot_session.clone();
        thread::Builder::new()
            .name("azem-ipc".into())
            .spawn(move || {
                let runtime = tokio::runtime::Builder::new_multi_thread()
                    .worker_threads(2)
                    .enable_all()
                    .build()
                    .expect("create Azem IPC runtime");
                runtime.block_on(supervise(
                    options,
                    commands_rx,
                    supervisor_messages,
                    supervisor_snapshot_session,
                    startup_tx,
                ));
            })
            .expect("start Azem IPC thread");
        Self {
            commands: commands_tx,
            message_tx: messages_tx,
            messages: messages_rx,
            snapshot_session,
            startup: Arc::new(Mutex::new(startup_rx)),
        }
    }

    pub fn wait_for_startup(&self, timeout: Duration) -> bool {
        self.startup
            .lock()
            .is_ok_and(|startup| startup.recv_timeout(timeout).is_ok())
    }

    pub fn request(&self, method: Method, payload: Value) -> String {
        let id = Uuid::new_v4().to_string();
        if let Err(error) = self.commands.try_send(RuntimeCommand::Request {
            id: id.clone(),
            method,
            payload,
        }) {
            let _ = self.message_tx.try_send(RuntimeMessage::Response {
                id: id.clone(),
                result: Err(format!("IPC command queue unavailable: {error}")),
            });
        }
        id
    }

    pub fn upload_attachment(
        &self,
        session_id: String,
        path: PathBuf,
        name: String,
        mime_type: String,
    ) -> String {
        let id = Uuid::new_v4().to_string();
        if let Err(error) = self.commands.try_send(RuntimeCommand::UploadAttachment {
            id: id.clone(),
            session_id,
            path,
            name,
            mime_type,
        }) {
            let _ = self.message_tx.try_send(RuntimeMessage::Response {
                id: id.clone(),
                result: Err(format!("IPC command queue unavailable: {error}")),
            });
        }
        id
    }

    pub fn set_snapshot_session(&self, session_id: impl Into<String>) {
        if let Ok(mut current) = self.snapshot_session.write() {
            *current = session_id.into();
        }
    }
    pub fn disconnect(&self) {
        self.close(true);
    }

    pub fn detach(&self) {
        self.close(false);
    }

    fn close(&self, stop_daemon: bool) {
        let (completed, completion) = std::sync::mpsc::sync_channel(1);
        if self
            .commands
            .send_blocking(RuntimeCommand::Disconnect {
                stop_daemon,
                completed,
            })
            .is_ok()
        {
            let _ = completion.recv_timeout(Duration::from_secs(2));
        }
    }
}

#[derive(Clone, Debug)]
pub struct RuntimeOptions {
    pub workspace: PathBuf,
    pub session_id: Option<String>,
    pub config_file: Option<PathBuf>,
    pub daemon_binary: Option<PathBuf>,
    pub state_dir: Option<PathBuf>,
}

async fn supervise(
    options: RuntimeOptions,
    commands: Receiver<RuntimeCommand>,
    messages: Sender<RuntimeMessage>,
    snapshot_session: Arc<RwLock<String>>,
    startup: std::sync::mpsc::SyncSender<()>,
) {
    let mut startup = Some(startup);
    let client_id = Uuid::new_v4().to_string();
    let mut last_sequence = 0_u64;
    let mut backoff = Duration::from_millis(100);
    loop {
        let _ = messages
            .send(RuntimeMessage::Connecting(
                "Connecting to workspace runtime…".into(),
            ))
            .await;
        let endpoint = match ensure_daemon(&options).await {
            Ok(endpoint) => endpoint,
            Err(error) => {
                let _ = messages
                    .send(RuntimeMessage::Connecting(format!(
                        "Runtime unavailable: {error}"
                    )))
                    .await;
                tokio::time::sleep(backoff).await;
                backoff = (backoff * 2).min(Duration::from_secs(5));
                continue;
            }
        };
        let (client, acknowledgement, mut events) =
            match Client::connect(&endpoint, Some(client_id.clone()), last_sequence).await {
                Ok(connection) => connection,
                Err(error) => {
                    let _ = messages
                        .send(RuntimeMessage::Connecting(format!(
                            "Reconnect failed: {error}"
                        )))
                        .await;
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(Duration::from_secs(5));
                    continue;
                }
            };
        backoff = Duration::from_millis(100);
        let mut forward_after = acknowledgement.current_sequence;
        let _ = messages
            .send(RuntimeMessage::Connected {
                pid: endpoint.pid,
                sequence: acknowledgement.current_sequence,
            })
            .await;
        let snapshot_request = serde_json::json!({
            "sessionId": read_snapshot_session(&snapshot_session),
            "refresh": true
        });
        match request_snapshot_while_forwarding(
            &client,
            &mut events,
            &messages,
            &snapshot_request,
            &mut forward_after,
            &mut last_sequence,
        )
        .await
        {
            Ok(snapshot) => {
                let _ = messages.send(RuntimeMessage::Snapshot(snapshot)).await;
                if let Some(startup) = startup.take() {
                    let _ = startup.send(());
                }
            }
            Err(error) => {
                let _ = messages
                    .send(RuntimeMessage::Connecting(format!(
                        "Snapshot failed: {error}"
                    )))
                    .await;
                let _ = client.close().await;
                continue;
            }
        }
        loop {
            tokio::select! {
                event = events.recv() => match event {
                    Ok(ClientEvent::Envelope(envelope)) => {
                        last_sequence = last_sequence.max(envelope.sequence);
                        if should_forward_envelope(&envelope, forward_after) {
                            let _ = messages.send(RuntimeMessage::Event(ClientEvent::Envelope(envelope))).await;
                        }
                    }
                    Ok(ClientEvent::Binary(metadata, data)) => {
                        last_sequence = last_sequence.max(metadata.sequence);
                        if metadata.sequence > forward_after {
                            let _ = messages.send(RuntimeMessage::Event(ClientEvent::Binary(metadata, data))).await;
                        }
                    }
                    Ok(ClientEvent::ResyncRequired { sequence, reason }) => {
                        let snapshot_request = serde_json::json!({
                            "sessionId": read_snapshot_session(&snapshot_session),
                            "refresh": true
                        });
                        last_sequence = last_sequence.max(sequence);
                        forward_after = forward_after.max(sequence);
                        match request_snapshot_while_forwarding(
                            &client,
                            &mut events,
                            &messages,
                            &snapshot_request,
                            &mut forward_after,
                            &mut last_sequence,
                        )
                        .await
                        {
                            Ok(snapshot) => {
                                let _ = messages.send(RuntimeMessage::Snapshot(snapshot)).await;
                            }
                            Err(error) => {
                                let _ = messages.send(RuntimeMessage::Connecting(format!("Resync failed ({reason}): {error}"))).await;
                                break;
                            }
                        }
                    }
                    Ok(ClientEvent::Disconnected(reason)) => {
                        let _ = messages.send(RuntimeMessage::Connecting(format!("Disconnected: {reason}"))).await;
                        break;
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                        let _ = messages.send(RuntimeMessage::Connecting("Disconnected".into())).await;
                        break;
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => {
                        last_sequence = 0;
                        let _ = client.close().await;
                        break;
                    }
                },
                command = commands.recv() => match command {
                    Ok(RuntimeCommand::Request { id, method, payload }) => {
                        let request_client = client.clone();
                        let response_messages = messages.clone();
                        tokio::spawn(async move {
                            let result = request_client.request(method, &payload).await.map_err(|error| error.to_string());
                            let _ = response_messages.send(RuntimeMessage::Response { id, result }).await;
                        });
                    }
                    Ok(RuntimeCommand::UploadAttachment { id, session_id, path, name, mime_type }) => {
                        let request_client = client.clone();
                        let response_messages = messages.clone();
                        tokio::spawn(async move {
                            let result = request_client.upload_attachment(&session_id, path, &name, &mime_type).await.map_err(|error| error.to_string());
                            let _ = response_messages.send(RuntimeMessage::Response { id, result }).await;
                        });
                    }
                    Ok(RuntimeCommand::Disconnect { stop_daemon, completed }) => {
                        if stop_daemon {
                            let _ = client.stop_daemon(false).await;
                        }
                        let _ = client.close().await;
                        let _ = completed.send(());
                        return;
                    }
                    Err(_) => {
                        let _ = client.close().await;
                        return;
                    }
                }
            }
        }
        let _ = client.close().await;
    }
}

fn read_snapshot_session(snapshot_session: &RwLock<String>) -> String {
    snapshot_session
        .read()
        .map(|session_id| session_id.clone())
        .unwrap_or_default()
}

async fn request_snapshot_while_forwarding(
    client: &Client,
    events: &mut tokio::sync::broadcast::Receiver<ClientEvent>,
    messages: &Sender<RuntimeMessage>,
    snapshot_request: &Value,
    forward_after: &mut u64,
    last_sequence: &mut u64,
) -> Result<Value> {
    let request = client.request(Method::ReconnectSnapshot, snapshot_request);
    tokio::pin!(request);
    loop {
        tokio::select! {
            result = &mut request => return result,
            event = events.recv() => match event {
                Ok(ClientEvent::Envelope(envelope)) => {
                    *last_sequence = (*last_sequence).max(envelope.sequence);
                    if should_forward_envelope(&envelope, *forward_after) {
                        let _ = messages.send(RuntimeMessage::Event(ClientEvent::Envelope(envelope))).await;
                    }
                }
                Ok(ClientEvent::Binary(metadata, data)) => {
                    *last_sequence = (*last_sequence).max(metadata.sequence);
                    if metadata.sequence > *forward_after {
                        let _ = messages.send(RuntimeMessage::Event(ClientEvent::Binary(metadata, data))).await;
                    }
                }
                Ok(ClientEvent::ResyncRequired { sequence, .. }) => {
                    *last_sequence = (*last_sequence).max(sequence);
                    *forward_after = (*forward_after).max(sequence);
                }
                Ok(ClientEvent::Disconnected(reason)) => {
                    return Err(anyhow!("disconnected while loading snapshot: {reason}"));
                }
                Err(error) => return Err(anyhow!("event stream failed while loading snapshot: {error}")),
            }
        }
    }
}

fn should_forward_envelope(envelope: &azem_ipc::Envelope, baseline: u64) -> bool {
    if envelope.sequence > baseline {
        return true;
    }
    if envelope.channel != "runtime" {
        return false;
    }
    if envelope
        .payload
        .get("agentId")
        .and_then(Value::as_str)
        .is_some_and(|agent_id| !agent_id.is_empty())
    {
        return true;
    }
    matches!(
        envelope.payload.get("kind").and_then(Value::as_str),
        Some(
            "approval_requested"
                | "approval_resolved"
                | "user_input_requested"
                | "user_input_resolved"
                | "plan_proposed"
                | "plan_resolved"
        )
    )
}

async fn ensure_daemon(options: &RuntimeOptions) -> Result<Endpoint> {
    let endpoint_path = endpoint_path(options)?;
    let deadline = tokio::time::Instant::now() + Duration::from_secs(20);
    loop {
        if let Some(endpoint) = live_endpoint(&endpoint_path).await {
            return Ok(endpoint);
        }
        if let Some(_start_guard) = try_claim_daemon_start(&endpoint_path)? {
            if let Some(endpoint) = live_endpoint(&endpoint_path).await {
                return Ok(endpoint);
            }
            spawn_daemon(options, &endpoint_path)?;
            return wait_for_endpoint(&endpoint_path, deadline).await;
        }
        if tokio::time::Instant::now() >= deadline {
            return Err(anyhow!(
                "daemon did not publish an endpoint within 20 seconds"
            ));
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
}

async fn wait_for_endpoint(path: &Path, deadline: tokio::time::Instant) -> Result<Endpoint> {
    loop {
        if let Some(endpoint) = live_endpoint(path).await {
            return Ok(endpoint);
        }
        if tokio::time::Instant::now() >= deadline {
            return Err(anyhow!(
                "daemon did not publish an endpoint within 20 seconds"
            ));
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
}

async fn live_endpoint(path: &Path) -> Option<Endpoint> {
    let endpoint = Endpoint::load(path).ok()?;
    endpoint_alive(&endpoint).await.then_some(endpoint)
}

fn try_claim_daemon_start(endpoint_path: &Path) -> Result<Option<std::fs::File>> {
    let path = endpoint_path.with_file_name("start.lock");
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let file = OpenOptions::new()
        .create(true)
        .read(true)
        .write(true)
        .truncate(false)
        .open(path)?;
    match file.try_lock() {
        Ok(()) => Ok(Some(file)),
        Err(std::fs::TryLockError::WouldBlock) => Ok(None),
        Err(std::fs::TryLockError::Error(error)) => Err(error.into()),
    }
}

async fn endpoint_alive(endpoint: &Endpoint) -> bool {
    match Client::connect(endpoint, Some("azem-probe".into()), 0).await {
        Ok((client, _, _)) => {
            let _ = client.close().await;
            true
        }
        Err(_) => false,
    }
}

fn endpoint_path(options: &RuntimeOptions) -> Result<PathBuf> {
    let workspace = options
        .workspace
        .canonicalize()
        .unwrap_or_else(|_| options.workspace.clone());
    let digest = Sha256::digest(workspace.to_string_lossy().as_bytes());
    let workspace_id = digest[..16]
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let state_dir = options
        .state_dir
        .clone()
        .or_else(default_state_dir)
        .ok_or_else(|| anyhow!("cannot resolve Azem state directory"))?;
    Ok(state_dir
        .join("gpui-daemons")
        .join(workspace_id)
        .join("endpoint.json"))
}

pub(crate) fn restore_desktop_workspace(fallback: PathBuf, state_dir: Option<&Path>) -> PathBuf {
    let recent = state_dir
        .map(Path::to_path_buf)
        .or_else(default_state_dir)
        .and_then(|directory| fs::read_dir(directory.join("gpui-daemons")).ok())
        .into_iter()
        .flatten()
        .filter_map(|entry| {
            let endpoint_path = entry.ok()?.path().join("endpoint.json");
            let modified = endpoint_path.metadata().ok()?.modified().ok()?;
            let workspace = Endpoint::load(endpoint_path).ok()?.workspace;
            valid_project_workspace(workspace).map(|workspace| (modified, workspace))
        })
        .max_by_key(|(modified, _)| *modified)
        .map(|(_, workspace)| workspace);
    recent
        .or_else(|| valid_project_workspace(fallback.clone()))
        .or_else(|| dirs::home_dir().and_then(valid_project_workspace))
        .unwrap_or(fallback)
}

fn valid_project_workspace(workspace: PathBuf) -> Option<PathBuf> {
    let workspace = workspace.canonicalize().ok()?;
    (workspace.is_dir() && workspace.parent().is_some()).then_some(workspace)
}

fn default_state_dir() -> Option<PathBuf> {
    std::env::var_os("AZEM_HOME")
        .map(PathBuf::from)
        .or_else(|| dirs::home_dir().map(|home| home.join(".azem")))
}

fn bundled_daemon_binary() -> Option<PathBuf> {
    let candidate = std::env::current_exe()
        .ok()?
        .parent()?
        .join(if cfg!(windows) {
            "azem-daemon.exe"
        } else {
            "azem-daemon"
        });
    candidate.is_file().then_some(candidate)
}

fn spawn_daemon(options: &RuntimeOptions, endpoint_path: &Path) -> Result<()> {
    let binary = options
        .daemon_binary
        .clone()
        .or_else(|| std::env::var_os("AZEM_DAEMON_BINARY").map(PathBuf::from))
        .or_else(bundled_daemon_binary)
        .unwrap_or_else(|| PathBuf::from("azem"));
    if let Some(parent) = endpoint_path.parent() {
        fs::create_dir_all(parent)?;
    }
    let log_path = endpoint_path.with_file_name("daemon.log");
    let log = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)?;
    let mut command = Command::new(&binary);
    if binary.file_stem().and_then(|value| value.to_str()) != Some("azem-daemon") {
        command.args(["daemon", "serve"]);
    }
    command.arg("--workspace").arg(&options.workspace);
    if let Some(config_file) = &options.config_file {
        command.arg("--config").arg(config_file);
    }
    if let Some(state_dir) = &options.state_dir {
        command.env("AZEM_HOME", state_dir);
    }
    command
        .stdin(Stdio::null())
        .stdout(Stdio::from(log.try_clone()?))
        .stderr(Stdio::from(log));
    detach_command(&mut command);
    command
        .spawn()
        .with_context(|| format!("start Azem daemon with {}", binary.display()))?;
    Ok(())
}

#[cfg(unix)]
fn detach_command(command: &mut Command) {
    use std::os::unix::process::CommandExt;
    command.process_group(0);
}

#[cfg(windows)]
fn detach_command(command: &mut Command) {
    use std::os::windows::process::CommandExt;
    const DETACHED_PROCESS: u32 = 0x0000_0008;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    command.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP);
}

#[cfg(test)]
mod tests {
    use std::{
        path::PathBuf,
        sync::{
            Arc, Mutex, RwLock,
            atomic::{AtomicBool, Ordering},
        },
    };

    use azem_ipc::{Envelope, Method};
    use serde_json::json;
    use uuid::Uuid;

    use super::{
        RuntimeCommand, RuntimeConnection, RuntimeMessage, RuntimeOptions, endpoint_path,
        restore_desktop_workspace, should_forward_envelope,
    };

    #[test]
    fn desktop_disconnect_waits_for_the_daemon_stop_attempt() {
        let (commands, command_rx) = async_channel::bounded(1);
        let (message_tx, messages) = async_channel::bounded(1);
        let (_startup_tx, startup_rx) = std::sync::mpsc::sync_channel(1);
        let stopped = Arc::new(AtomicBool::new(false));
        let worker_stopped = stopped.clone();
        let worker = std::thread::spawn(move || {
            let RuntimeCommand::Disconnect {
                stop_daemon,
                completed,
            } = command_rx.recv_blocking().unwrap()
            else {
                panic!("expected disconnect command");
            };
            assert!(stop_daemon);
            worker_stopped.store(true, Ordering::SeqCst);
            completed.send(()).unwrap();
        });
        let connection = RuntimeConnection {
            commands,
            message_tx,
            messages,
            snapshot_session: Arc::new(RwLock::new(String::new())),
            startup: Arc::new(Mutex::new(startup_rx)),
        };

        connection.disconnect();

        assert!(stopped.load(Ordering::SeqCst));
        worker.join().unwrap();
    }

    #[test]
    fn workspace_switch_detaches_without_stopping_the_daemon() {
        let (commands, command_rx) = async_channel::bounded(1);
        let (message_tx, messages) = async_channel::bounded(1);
        let (_startup_tx, startup_rx) = std::sync::mpsc::sync_channel(1);
        let worker = std::thread::spawn(move || {
            let RuntimeCommand::Disconnect {
                stop_daemon,
                completed,
            } = command_rx.recv_blocking().unwrap()
            else {
                panic!("expected disconnect command");
            };
            assert!(!stop_daemon);
            completed.send(()).unwrap();
        });
        let connection = RuntimeConnection {
            commands,
            message_tx,
            messages,
            snapshot_session: Arc::new(RwLock::new(String::new())),
            startup: Arc::new(Mutex::new(startup_rx)),
        };

        connection.detach();

        worker.join().unwrap();
    }

    #[test]
    fn desktop_launch_restores_the_most_recent_endpoint_workspace() {
        let state_dir = std::env::temp_dir().join(format!("azem-state-{}", Uuid::new_v4()));
        let workspace = state_dir.join("workspace");
        let endpoint_dir = state_dir.join("gpui-daemons").join("recent");
        std::fs::create_dir_all(&workspace).unwrap();
        std::fs::create_dir_all(&endpoint_dir).unwrap();
        let endpoint_path = endpoint_dir.join("endpoint.json");
        std::fs::write(
            &endpoint_path,
            serde_json::to_vec(&json!({
                "protocol": 1,
                "workspaceId": "recent",
                "workspace": workspace,
                "address": endpoint_dir.join("azem.sock"),
                "tokenFile": endpoint_dir.join("token"),
                "pid": 42,
                "startedAt": "2026-08-26T00:00:00Z"
            }))
            .unwrap(),
        )
        .unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&endpoint_path, std::fs::Permissions::from_mode(0o600))
                .unwrap();
        }

        assert_eq!(
            restore_desktop_workspace(PathBuf::from("/"), Some(&state_dir)),
            workspace.canonicalize().unwrap()
        );
    }

    #[test]
    fn endpoint_path_is_stable_and_workspace_scoped() {
        let state_dir = std::env::temp_dir().join(format!("azem-state-{}", Uuid::new_v4()));
        let first = RuntimeOptions {
            workspace: PathBuf::from("/workspace/first"),
            session_id: Some("session-1".into()),
            config_file: None,
            daemon_binary: None,
            state_dir: Some(state_dir.clone()),
        };
        let mut second = first.clone();
        second.workspace = PathBuf::from("/workspace/second");
        let first_path = endpoint_path(&first).unwrap();
        assert_eq!(first_path, endpoint_path(&first).unwrap());
        assert_ne!(first_path, endpoint_path(&second).unwrap());
        assert!(first_path.starts_with(state_dir.join("gpui-daemons")));
        assert_eq!(
            first_path.file_name().and_then(|name| name.to_str()),
            Some("endpoint.json")
        );
    }

    #[test]
    fn daemon_start_has_one_owner_per_workspace() {
        let endpoint = std::env::temp_dir()
            .join(format!("azem-daemon-lock-{}", uuid::Uuid::new_v4()))
            .join("endpoint.json");
        let first = super::try_claim_daemon_start(&endpoint)
            .unwrap()
            .expect("first UI owns daemon startup");
        assert!(super::try_claim_daemon_start(&endpoint).unwrap().is_none());
        drop(first);
        assert!(super::try_claim_daemon_start(&endpoint).unwrap().is_some());
        let _ = std::fs::remove_dir_all(endpoint.parent().unwrap());
    }

    #[cfg(unix)]
    #[test]
    fn daemon_spawn_inherits_the_requested_state_directory() {
        use std::os::unix::fs::PermissionsExt;

        let root = std::env::temp_dir().join(format!("azem-daemon-home-{}", Uuid::new_v4()));
        let workspace = root.join("workspace");
        let state_dir = root.join("state");
        let binary = root.join("azem-daemon");
        let endpoint = state_dir.join("gpui-daemons/test/endpoint.json");
        std::fs::create_dir_all(&workspace).unwrap();
        std::fs::write(&binary, "#!/bin/sh\nprintf '%s' \"$AZEM_HOME\"\n").unwrap();
        std::fs::set_permissions(&binary, std::fs::Permissions::from_mode(0o700)).unwrap();
        let options = RuntimeOptions {
            workspace,
            session_id: None,
            config_file: None,
            daemon_binary: Some(binary),
            state_dir: Some(state_dir.clone()),
        };

        super::spawn_daemon(&options, &endpoint).unwrap();

        let log = endpoint.with_file_name("daemon.log");
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(1);
        loop {
            if std::fs::read_to_string(&log).unwrap_or_default() == state_dir.to_string_lossy() {
                break;
            }
            assert!(
                std::time::Instant::now() < deadline,
                "daemon did not inherit AZEM_HOME"
            );
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        let _ = std::fs::remove_dir_all(root);
    }

    #[test]
    fn full_command_queue_returns_an_explicit_response_error() {
        let (commands, _commands_rx) = async_channel::bounded(1);
        let (message_tx, messages) = async_channel::bounded(4);
        let (_startup_tx, startup_rx) = std::sync::mpsc::sync_channel(1);
        let connection = RuntimeConnection {
            commands,
            message_tx,
            messages: messages.clone(),
            snapshot_session: Arc::new(RwLock::new(String::new())),
            startup: Arc::new(Mutex::new(startup_rx)),
        };
        connection.request(Method::Initialise, json!({}));
        let rejected_id = connection.request(Method::Initialise, json!({}));
        match messages.try_recv().unwrap() {
            RuntimeMessage::Response { id, result } => {
                assert_eq!(id, rejected_id);
                assert!(result.unwrap_err().contains("queue unavailable"));
            }
            message => panic!("unexpected message: {message:?}"),
        }
    }

    #[test]
    fn replay_sequence_boundary_distinguishes_live_events() {
        let event = Envelope {
            version: 1,
            kind: "event".into(),
            id: String::new(),
            client_id: String::new(),
            workspace_id: String::new(),
            method: None,
            channel: "daemon".into(),
            sequence: 42,
            payload: json!({"workspace": "/workspace/other", "sessionId": "session-1"}),
            error: None,
            binary: None,
        };
        assert!(!should_forward_envelope(&event, 42));
        assert!(should_forward_envelope(&event, 41));
        let actionable = Envelope {
            channel: "runtime".into(),
            payload: json!({"kind": "approval_requested"}),
            ..event
        };
        assert!(should_forward_envelope(&actionable, 42));
        let child = Envelope {
            payload: json!({"kind": "text_delta", "agentId": "agent-1"}),
            ..actionable
        };
        assert!(should_forward_envelope(&child, 42));
    }
}

#[cfg(not(any(unix, windows)))]
fn detach_command(_command: &mut Command) {}
