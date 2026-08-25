use std::io;

use bytes::Bytes;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

use crate::generated::{BinaryMetadata, Envelope, MAX_BINARY_CHUNK_BYTES, MAX_CONTROL_FRAME_BYTES};

const WIRE_CONTROL: u8 = 1;
const WIRE_BINARY: u8 = 2;
const MAX_BINARY_HEADER_BYTES: usize = 64 << 10;

#[derive(Debug)]
pub enum Frame {
    Control(Box<Envelope>),
    Binary(BinaryMetadata, Bytes),
}

pub async fn read_frame<R>(reader: &mut R) -> io::Result<Frame>
where
    R: AsyncRead + Unpin + ?Sized,
{
    let size = reader.read_u32().await? as usize;
    if size == 0 || size > MAX_CONTROL_FRAME_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            format!("invalid IPC frame size {size}"),
        ));
    }
    let mut payload = vec![0_u8; size];
    reader.read_exact(&mut payload).await?;
    match payload[0] {
        WIRE_CONTROL => {
            let envelope = serde_json::from_slice(&payload[1..])
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
            Ok(Frame::Control(Box::new(envelope)))
        }
        WIRE_BINARY => {
            if payload.len() < 3 {
                return Err(io::Error::new(
                    io::ErrorKind::UnexpectedEof,
                    "truncated IPC binary frame",
                ));
            }
            let header_len = u16::from_be_bytes([payload[1], payload[2]]) as usize;
            if header_len == 0
                || header_len > MAX_BINARY_HEADER_BYTES
                || 3 + header_len > payload.len()
            {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "invalid IPC binary header",
                ));
            }
            let metadata = serde_json::from_slice(&payload[3..3 + header_len])
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
            let data = Bytes::copy_from_slice(&payload[3 + header_len..]);
            if data.len() > MAX_BINARY_CHUNK_BYTES {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "IPC binary chunk is too large",
                ));
            }
            Ok(Frame::Binary(metadata, data))
        }
        kind => Err(io::Error::new(
            io::ErrorKind::InvalidData,
            format!("unsupported IPC wire kind {kind}"),
        )),
    }
}

pub async fn write_envelope<W>(writer: &mut W, envelope: &Envelope) -> io::Result<()>
where
    W: AsyncWrite + Unpin + ?Sized,
{
    let encoded = serde_json::to_vec(envelope)
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    write_physical(writer, WIRE_CONTROL, &[], &encoded).await
}

pub async fn write_binary<W>(
    writer: &mut W,
    metadata: &BinaryMetadata,
    data: &[u8],
) -> io::Result<()>
where
    W: AsyncWrite + Unpin + ?Sized,
{
    if data.len() > MAX_BINARY_CHUNK_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "IPC binary chunk is too large",
        ));
    }
    let header = serde_json::to_vec(metadata)
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    if header.len() > MAX_BINARY_HEADER_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "IPC binary metadata is too large",
        ));
    }
    let prefix = (header.len() as u16).to_be_bytes();
    let mut complete_header = Vec::with_capacity(prefix.len() + header.len());
    complete_header.extend_from_slice(&prefix);
    complete_header.extend_from_slice(&header);
    write_physical(writer, WIRE_BINARY, &complete_header, data).await
}

async fn write_physical<W>(
    writer: &mut W,
    kind: u8,
    header: &[u8],
    payload: &[u8],
) -> io::Result<()>
where
    W: AsyncWrite + Unpin + ?Sized,
{
    let size = 1 + header.len() + payload.len();
    if size > MAX_CONTROL_FRAME_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "IPC physical frame is too large",
        ));
    }
    writer.write_u32(size as u32).await?;
    writer.write_u8(kind).await?;
    writer.write_all(header).await?;
    writer.write_all(payload).await?;
    writer.flush().await
}

#[cfg(test)]
mod tests {
    use super::{Frame, read_frame, write_binary, write_envelope};
    use crate::{BinaryMetadata, Method, generated::MAX_CONTROL_FRAME_BYTES, protocol::envelope};

    #[tokio::test]
    async fn control_and_binary_frames_round_trip() {
        let (mut writer, mut reader) = tokio::io::duplex(4096);
        let task = tokio::spawn(async move {
            let mut request = envelope("request");
            request.id = "request-1".into();
            request.method = Some(Method::Initialise);
            write_envelope(&mut writer, &request).await.unwrap();
            let metadata = BinaryMetadata {
                transfer_id: "transfer-1".into(),
                purpose: "attachment".into(),
                count: 1,
                byte_length: 3,
                ..Default::default()
            };
            write_binary(&mut writer, &metadata, b"abc").await.unwrap();
        });

        match read_frame(&mut reader).await.unwrap() {
            Frame::Control(envelope) => {
                assert_eq!(envelope.id, "request-1");
                assert_eq!(envelope.method, Some(Method::Initialise));
            }
            frame => panic!("unexpected frame: {frame:?}"),
        }
        match read_frame(&mut reader).await.unwrap() {
            Frame::Binary(metadata, data) => {
                assert_eq!(metadata.transfer_id, "transfer-1");
                assert_eq!(&data[..], b"abc");
            }
            frame => panic!("unexpected frame: {frame:?}"),
        }
        task.await.unwrap();
    }

    #[tokio::test]
    async fn oversized_frame_is_rejected_before_payload_read() {
        let (mut writer, mut reader) = tokio::io::duplex(16);
        tokio::spawn(async move {
            use tokio::io::AsyncWriteExt;
            writer
                .write_all(&((MAX_CONTROL_FRAME_BYTES as u32) + 1).to_be_bytes())
                .await
                .unwrap();
        });
        let error = read_frame(&mut reader).await.unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);
    }
}
