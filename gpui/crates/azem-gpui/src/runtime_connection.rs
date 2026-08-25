use std::{
    fs::{self, OpenOptions},
    path::{Path, PathBuf},
    process::{Command, Stdio},
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
    Disconnect,
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
}

impl RuntimeConnection {
    pub fn start(options: RuntimeOptions) -> Self {
        let (commands_tx, commands_rx) = async_channel::bounded(256);
        let (messages_tx, messages_rx) = async_channel::bounded(4096);
        let supervisor_messages = messages_tx.clone();
        thread::Builder::new()
            .name("azem-ipc".into())
            .spawn(move || {
                let runtime = tokio::runtime::Builder::new_multi_thread()
                    .worker_threads(2)
                    .enable_all()
                    .build()
                    .expect("create Azem IPC runtime");
                runtime.block_on(supervise(options, commands_rx, supervisor_messages));
            })
            .expect("start Azem IPC thread");
        Self {
            commands: commands_tx,
            message_tx: messages_tx,
            messages: messages_rx,
        }
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

    pub fn disconnect(&self) {
        let _ = self.commands.try_send(RuntimeCommand::Disconnect);
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
) {
    let client_id = Uuid::new_v4().to_string();
    let snapshot_request = serde_json::json!({
        "sessionId": options.session_id.clone().unwrap_or_default(),
        "refresh": true
    });
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
        let _ = messages
            .send(RuntimeMessage::Connected {
                pid: endpoint.pid,
                sequence: acknowledgement.current_sequence,
            })
            .await;
        match client
            .request(Method::ReconnectSnapshot, &snapshot_request)
            .await
        {
            Ok(snapshot) => {
                let _ = messages.send(RuntimeMessage::Snapshot(snapshot)).await;
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
                        let _ = messages.send(RuntimeMessage::Event(ClientEvent::Envelope(envelope))).await;
                    }
                    Ok(ClientEvent::Binary(metadata, data)) => {
                        last_sequence = last_sequence.max(metadata.sequence);
                        let _ = messages.send(RuntimeMessage::Event(ClientEvent::Binary(metadata, data))).await;
                    }
                    Ok(ClientEvent::ResyncRequired { sequence, reason }) => {
                        last_sequence = sequence;
                        match client.request(Method::ReconnectSnapshot, &snapshot_request).await {
                            Ok(snapshot) => { let _ = messages.send(RuntimeMessage::Snapshot(snapshot)).await; }
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
                        match client.request(Method::ReconnectSnapshot, &snapshot_request).await {
                            Ok(snapshot) => { let _ = messages.send(RuntimeMessage::Snapshot(snapshot)).await; }
                            Err(_) => break,
                        }
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
                    Ok(RuntimeCommand::Disconnect) | Err(_) => {
                        let _ = client.close().await;
                        return;
                    }
                }
            }
        }
        let _ = client.close().await;
    }
}

async fn ensure_daemon(options: &RuntimeOptions) -> Result<Endpoint> {
    let endpoint_path = endpoint_path(options)?;
    if let Ok(endpoint) = Endpoint::load(&endpoint_path)
        && endpoint_alive(&endpoint).await
    {
        return Ok(endpoint);
    }
    spawn_daemon(options, &endpoint_path)?;
    let deadline = tokio::time::Instant::now() + Duration::from_secs(20);
    loop {
        if let Ok(endpoint) = Endpoint::load(&endpoint_path)
            && endpoint_alive(&endpoint).await
        {
            return Ok(endpoint);
        }
        if tokio::time::Instant::now() >= deadline {
            return Err(anyhow!(
                "daemon did not publish an endpoint within 20 seconds"
            ));
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
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
    use std::path::PathBuf;

    use azem_ipc::Method;
    use serde_json::json;
    use uuid::Uuid;

    use super::{RuntimeConnection, RuntimeMessage, RuntimeOptions, endpoint_path};

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
    fn full_command_queue_returns_an_explicit_response_error() {
        let (commands, _commands_rx) = async_channel::bounded(1);
        let (message_tx, messages) = async_channel::bounded(4);
        let connection = RuntimeConnection {
            commands,
            message_tx,
            messages: messages.clone(),
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
}

#[cfg(not(any(unix, windows)))]
fn detach_command(_command: &mut Command) {}
