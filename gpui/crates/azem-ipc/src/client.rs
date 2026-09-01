use std::{collections::HashMap, io, sync::Arc, time::Duration};

use anyhow::{Context as _, Result, anyhow};
use bytes::Bytes;
use serde::Serialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, ReadHalf, WriteHalf},
    sync::{Mutex, broadcast, oneshot, watch},
    task::JoinHandle,
    time,
};
use uuid::Uuid;

use crate::{
    BinaryMetadata, Envelope, Method,
    codec::{Frame, read_frame, write_binary, write_envelope},
    generated::PROTOCOL_VERSION,
    protocol::{
        Authenticate, Challenge, Endpoint, EventBatch, HelloAck, authentication_proof, envelope,
    },
};

trait IoStream: AsyncRead + AsyncWrite + Send + Unpin {}
impl<T> IoStream for T where T: AsyncRead + AsyncWrite + Send + Unpin {}
type Stream = Box<dyn IoStream>;
type StreamReader = ReadHalf<Stream>;
type StreamWriter = WriteHalf<Stream>;

#[derive(Clone, Debug)]
pub enum ClientEvent {
    Envelope(Box<Envelope>),
    Binary(BinaryMetadata, Bytes),
    ResyncRequired { sequence: u64, reason: String },
    Disconnected(String),
}

#[derive(Clone)]
pub struct Client {
    inner: Arc<ClientInner>,
}

struct ClientInner {
    client_id: String,
    workspace_id: String,
    writer: Mutex<StreamWriter>,
    pending: Mutex<HashMap<String, oneshot::Sender<Result<Value, String>>>>,
    events: broadcast::Sender<ClientEvent>,
    shutdown: watch::Sender<bool>,
    reader_task: Mutex<Option<JoinHandle<()>>>,
    keepalive_task: Mutex<Option<JoinHandle<()>>>,
}

impl Client {
    pub async fn connect(
        endpoint: &Endpoint,
        client_id: Option<String>,
        last_sequence: u64,
    ) -> Result<(Self, HelloAck, broadcast::Receiver<ClientEvent>)> {
        let token = endpoint.token().context("read Azem IPC token")?;
        let mut stream = connect_stream(&endpoint.address)
            .await
            .context("connect to Azem daemon")?;
        let challenge =
            match time::timeout(Duration::from_secs(10), read_frame(&mut *stream)).await?? {
                Frame::Control(envelope) if envelope.kind == "hello" => {
                    serde_json::from_value::<Challenge>(envelope.payload)
                        .context("decode Azem IPC challenge")?
                }
                _ => return Err(anyhow!("daemon did not send an authentication challenge")),
            };
        if challenge.protocol != PROTOCOL_VERSION || challenge.workspace_id != endpoint.workspace_id
        {
            return Err(anyhow!("daemon challenge does not match the endpoint"));
        }
        let client_id = client_id
            .filter(|value| !value.trim().is_empty())
            .unwrap_or_else(|| Uuid::new_v4().to_string());
        let authentication = Authenticate {
            client_id: client_id.clone(),
            proof: authentication_proof(
                &token,
                &challenge.nonce,
                &client_id,
                &endpoint.workspace_id,
                PROTOCOL_VERSION,
            ),
            protocol: PROTOCOL_VERSION,
            last_sequence,
        };
        let mut hello = envelope("hello_ack");
        hello.client_id = client_id.clone();
        hello.workspace_id = endpoint.workspace_id.clone();
        hello.payload = serde_json::to_value(authentication)?;
        write_envelope(&mut *stream, &hello).await?;
        let acknowledgement =
            match time::timeout(Duration::from_secs(10), read_frame(&mut *stream)).await?? {
                Frame::Control(envelope) if envelope.kind == "hello_ack" => {
                    serde_json::from_value::<HelloAck>(envelope.payload)
                        .context("decode Azem IPC acknowledgement")?
                }
                _ => return Err(anyhow!("daemon rejected IPC authentication")),
            };
        let (reader, writer) = tokio::io::split(stream);
        let (events, _) = broadcast::channel(2048);
        let (shutdown, shutdown_rx) = watch::channel(false);
        let inner = Arc::new(ClientInner {
            client_id,
            workspace_id: endpoint.workspace_id.clone(),
            writer: Mutex::new(writer),
            pending: Mutex::new(HashMap::new()),
            events,
            shutdown,
            reader_task: Mutex::new(None),
            keepalive_task: Mutex::new(None),
        });
        let client = Self { inner };
        let receiver = client.subscribe();
        client.start_reader(reader).await;
        client.start_keepalive(shutdown_rx).await;
        Ok((client, acknowledgement, receiver))
    }

    pub fn subscribe(&self) -> broadcast::Receiver<ClientEvent> {
        self.inner.events.subscribe()
    }

    pub async fn request<T: Serialize>(&self, method: Method, payload: &T) -> Result<Value> {
        let id = Uuid::new_v4().to_string();
        let (sender, receiver) = oneshot::channel();
        self.inner.pending.lock().await.insert(id.clone(), sender);
        let mut request = Envelope::request(id.clone(), method, serde_json::to_value(payload)?);
        request.client_id = self.inner.client_id.clone();
        request.workspace_id = self.inner.workspace_id.clone();
        if let Err(error) = write_envelope(&mut *self.inner.writer.lock().await, &request).await {
            self.inner.pending.lock().await.remove(&id);
            return Err(error.into());
        }
        match time::timeout(Duration::from_secs(60), receiver).await {
            Ok(Ok(Ok(value))) => Ok(value),
            Ok(Ok(Err(error))) => Err(anyhow!(error)),
            Ok(Err(_)) => Err(anyhow!("IPC response channel closed")),
            Err(_) => {
                self.inner.pending.lock().await.remove(&id);
                Err(anyhow!("IPC request timed out"))
            }
        }
    }

    pub async fn stop_daemon(&self, include_active: bool) -> Result<()> {
        let id = Uuid::new_v4().to_string();
        let (sender, receiver) = oneshot::channel();
        self.inner.pending.lock().await.insert(id.clone(), sender);
        let mut request = envelope("daemon_stop");
        request.id = id.clone();
        request.client_id = self.inner.client_id.clone();
        request.workspace_id = self.inner.workspace_id.clone();
        request.payload = serde_json::json!({"includeActive": include_active});
        if let Err(error) = write_envelope(&mut *self.inner.writer.lock().await, &request).await {
            self.inner.pending.lock().await.remove(&id);
            return Err(error.into());
        }
        match time::timeout(Duration::from_secs(5), receiver).await {
            Ok(Ok(Ok(_))) => Ok(()),
            Ok(Ok(Err(error))) => Err(anyhow!(error)),
            Ok(Err(_)) => Err(anyhow!("IPC daemon stop response channel closed")),
            Err(_) => {
                self.inner.pending.lock().await.remove(&id);
                Err(anyhow!("IPC daemon stop timed out"))
            }
        }
    }

    pub async fn upload_attachment(
        &self,
        session_id: &str,
        path: impl AsRef<std::path::Path>,
        name: &str,
        mime_type: &str,
    ) -> Result<Value> {
        let path = path.as_ref();
        let byte_length = tokio::fs::metadata(path).await?.len();
        if byte_length > crate::generated::MAX_REASSEMBLED_BINARY as u64 {
            return Err(anyhow!("attachment exceeds the IPC reassembly limit"));
        }
        let mut hash_file = tokio::fs::File::open(path).await?;
        let mut hasher = Sha256::new();
        let mut buffer = vec![0_u8; crate::generated::MAX_BINARY_CHUNK_BYTES];
        loop {
            let read = hash_file.read(&mut buffer).await?;
            if read == 0 {
                break;
            }
            hasher.update(&buffer[..read]);
        }
        let digest = hasher
            .finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        let transfer_id = Uuid::new_v4().to_string();
        let chunk_count =
            byte_length.div_ceil(crate::generated::MAX_BINARY_CHUNK_BYTES as u64) as usize;
        self.request(
            Method::BeginAttachmentTransfer,
            &serde_json::json!({
                "transferId": transfer_id.clone(),
                "sessionId": session_id,
                "name": name,
                "mimeType": mime_type,
                "byteLength": byte_length,
                "sha256": digest.clone(),
                "chunkCount": chunk_count,
            }),
        )
        .await?;
        let upload = async {
            let mut file = tokio::fs::File::open(path).await?;
            for index in 0..chunk_count {
                let read = file.read(&mut buffer).await?;
                if read == 0 {
                    return Err(anyhow!("attachment ended before its declared size"));
                }
                let metadata = BinaryMetadata {
                    transfer_id: transfer_id.clone(),
                    purpose: "attachment".into(),
                    channel: String::new(),
                    sequence: 0,
                    name: name.into(),
                    media_type: mime_type.into(),
                    byte_length: byte_length as i64,
                    sha256: digest.clone(),
                    index,
                    count: chunk_count,
                };
                self.write_binary(&metadata, &buffer[..read]).await?;
            }
            self.request(
                Method::CommitAttachment,
                &serde_json::json!({"id": transfer_id.clone()}),
            )
            .await
        }
        .await;
        if upload.is_err() {
            let _ = self
                .request(
                    Method::AbortAttachment,
                    &serde_json::json!({"id": transfer_id.clone()}),
                )
                .await;
        }
        upload
    }
    pub async fn write_binary(&self, metadata: &BinaryMetadata, data: &[u8]) -> Result<()> {
        write_binary(&mut *self.inner.writer.lock().await, metadata, data).await?;
        Ok(())
    }

    pub async fn close(&self) -> Result<()> {
        let _ = self.inner.shutdown.send(true);
        let mut detach = envelope("client_detach");
        detach.client_id = self.inner.client_id.clone();
        detach.workspace_id = self.inner.workspace_id.clone();
        let _ = write_envelope(&mut *self.inner.writer.lock().await, &detach).await;
        if let Some(task) = self.inner.keepalive_task.lock().await.take() {
            task.abort();
        }
        if let Some(task) = self.inner.reader_task.lock().await.take() {
            task.abort();
        }
        Ok(())
    }

    async fn start_reader(&self, mut reader: StreamReader) {
        let inner = self.inner.clone();
        let task = tokio::spawn(async move {
            let disconnect_reason = loop {
                match read_frame(&mut reader).await {
                    Ok(Frame::Control(envelope)) => dispatch_envelope(&inner, *envelope).await,
                    Ok(Frame::Binary(metadata, data)) => {
                        let _ = inner.events.send(ClientEvent::Binary(metadata, data));
                    }
                    Err(error) => break error.to_string(),
                }
            };
            for (_, sender) in inner.pending.lock().await.drain() {
                let _ = sender.send(Err(disconnect_reason.clone()));
            }
            let _ = inner
                .events
                .send(ClientEvent::Disconnected(disconnect_reason));
        });
        *self.inner.reader_task.lock().await = Some(task);
    }

    async fn start_keepalive(&self, mut shutdown: watch::Receiver<bool>) {
        let inner = self.inner.clone();
        let task = tokio::spawn(async move {
            let mut interval = time::interval(Duration::from_secs(30));
            interval.set_missed_tick_behavior(time::MissedTickBehavior::Skip);
            loop {
                tokio::select! {
                    _ = interval.tick() => {
                        let mut ping = envelope("ping");
                        ping.id = Uuid::new_v4().to_string();
                        ping.client_id = inner.client_id.clone();
                        ping.workspace_id = inner.workspace_id.clone();
                        if write_envelope(&mut *inner.writer.lock().await, &ping).await.is_err() { return; }
                    }
                    changed = shutdown.changed() => {
                        if changed.is_err() || *shutdown.borrow() { return; }
                    }
                }
            }
        });
        *self.inner.keepalive_task.lock().await = Some(task);
    }
}

async fn dispatch_envelope(inner: &ClientInner, envelope: Envelope) {
    match envelope.kind.as_str() {
        "response" => {
            if let Some(sender) = inner.pending.lock().await.remove(&envelope.id) {
                let result = match envelope.error {
                    Some(error) => Err(format!("{}: {}", error.code, error.message)),
                    None => Ok(envelope.payload),
                };
                let _ = sender.send(result);
            }
        }
        "event_batch" => {
            if let Ok(batch) = serde_json::from_value::<EventBatch>(envelope.payload) {
                for value in batch.events {
                    if let Ok(event) = serde_json::from_value::<Envelope>(value) {
                        let _ = inner.events.send(ClientEvent::Envelope(Box::new(event)));
                    }
                }
            }
        }
        "resync_required" => {
            let reason = envelope
                .payload
                .get("reason")
                .and_then(Value::as_str)
                .unwrap_or("resync_required")
                .to_string();
            let _ = inner.events.send(ClientEvent::ResyncRequired {
                sequence: envelope.sequence,
                reason,
            });
        }
        "pong" | "replay_complete" => {}
        _ => {
            let _ = inner.events.send(ClientEvent::Envelope(Box::new(envelope)));
        }
    }
}

async fn connect_stream(address: &str) -> io::Result<Stream> {
    #[cfg(unix)]
    {
        Ok(Box::new(tokio::net::UnixStream::connect(address).await?))
    }
    #[cfg(windows)]
    {
        use tokio::net::windows::named_pipe::ClientOptions;
        let deadline = time::Instant::now() + Duration::from_secs(5);
        loop {
            match ClientOptions::new().open(address) {
                Ok(pipe) => return Ok(Box::new(pipe)),
                Err(error) if time::Instant::now() < deadline => {
                    time::sleep(Duration::from_millis(50)).await
                }
                Err(error) => return Err(error),
            }
        }
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = address;
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Azem IPC is unsupported on this platform",
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn idle_daemon_stop_waits_for_the_server_acknowledgement() {
        let (client_stream, mut server_stream) = tokio::io::duplex(4096);
        let (reader, writer) = tokio::io::split(Box::new(client_stream) as Stream);
        let (events, _) = broadcast::channel(8);
        let (shutdown, _) = watch::channel(false);
        let client = Client {
            inner: Arc::new(ClientInner {
                client_id: "desktop".into(),
                workspace_id: "workspace".into(),
                writer: Mutex::new(writer),
                pending: Mutex::new(HashMap::new()),
                events,
                shutdown,
                reader_task: Mutex::new(None),
                keepalive_task: Mutex::new(None),
            }),
        };
        client.start_reader(reader).await;
        let server = tokio::spawn(async move {
            let Frame::Control(request) = read_frame(&mut server_stream).await.unwrap() else {
                panic!("expected daemon stop control frame");
            };
            assert_eq!(request.kind, "daemon_stop");
            assert_eq!(request.payload["includeActive"], false);
            let mut response = envelope("response");
            response.id = request.id.clone();
            response.payload = serde_json::json!({"stopping": true});
            write_envelope(&mut server_stream, &response).await.unwrap();
        });

        client.stop_daemon(false).await.unwrap();
        server.await.unwrap();
        client.close().await.unwrap();
    }
}
