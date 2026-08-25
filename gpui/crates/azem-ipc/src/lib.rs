pub mod client;
pub mod codec;
pub mod generated;
pub mod protocol;

pub use client::{Client, ClientEvent};
pub use generated::{ActionKind, BinaryMetadata, Envelope, EventKind, Method, ProtocolError};
pub use protocol::{Authenticate, Challenge, Endpoint, EventBatch, HelloAck};
