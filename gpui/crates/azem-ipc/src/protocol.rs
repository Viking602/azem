use std::{
    fs, io,
    path::{Path, PathBuf},
};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::Sha256;

use crate::generated::{Envelope, PROTOCOL_VERSION};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Endpoint {
    pub protocol: u32,
    pub workspace_id: String,
    pub workspace: PathBuf,
    pub address: String,
    pub token_file: PathBuf,
    pub pid: u32,
    pub started_at: String,
}

impl Endpoint {
    pub fn load(path: impl AsRef<Path>) -> io::Result<Self> {
        let path = path.as_ref();
        ensure_private(path)?;
        let encoded = fs::read(path)?;
        let endpoint: Self = serde_json::from_slice(&encoded)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if endpoint.protocol != PROTOCOL_VERSION
            || endpoint.workspace_id.is_empty()
            || endpoint.address.is_empty()
            || endpoint.pid == 0
        {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "incomplete Azem IPC endpoint",
            ));
        }
        Ok(endpoint)
    }

    pub fn token(&self) -> io::Result<Vec<u8>> {
        ensure_private(&self.token_file)?;
        let token = URL_SAFE_NO_PAD
            .decode(fs::read_to_string(&self.token_file)?.trim())
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if token.len() != 32 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid Azem IPC token",
            ));
        }
        Ok(token)
    }
}

#[cfg(unix)]
fn ensure_private(path: &Path) -> io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    if fs::metadata(path)?.permissions().mode() & 0o077 != 0 {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            format!("{} is not private", path.display()),
        ));
    }
    Ok(())
}

#[cfg(not(unix))]
fn ensure_private(path: &Path) -> io::Result<()> {
    let _ = fs::metadata(path)?;
    Ok(())
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Challenge {
    pub nonce: String,
    pub workspace_id: String,
    pub protocol: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Authenticate {
    pub client_id: String,
    pub proof: String,
    pub protocol: u32,
    #[serde(default)]
    pub last_sequence: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HelloAck {
    pub protocol: u32,
    pub workspace_id: String,
    pub current_sequence: u64,
    pub replay_available: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EventBatch {
    pub first_sequence: u64,
    pub last_sequence: u64,
    pub events: Vec<Value>,
}

pub fn authentication_proof(
    token: &[u8],
    nonce: &str,
    client_id: &str,
    workspace_id: &str,
    protocol: u32,
) -> String {
    let mut mac = Hmac::<Sha256>::new_from_slice(token).expect("HMAC accepts a 32-byte token");
    mac.update(nonce.as_bytes());
    mac.update(&[0]);
    mac.update(client_id.as_bytes());
    mac.update(&[0]);
    mac.update(workspace_id.as_bytes());
    mac.update(&[0]);
    mac.update(protocol.to_string().as_bytes());
    URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes())
}

pub fn envelope(kind: &str) -> Envelope {
    Envelope {
        version: PROTOCOL_VERSION,
        kind: kind.into(),
        id: String::new(),
        client_id: String::new(),
        workspace_id: String::new(),
        method: None,
        channel: String::new(),
        sequence: 0,
        payload: Value::Null,
        error: None,
        binary: None,
    }
}

#[cfg(test)]
mod tests {
    use std::{fs, path::PathBuf};

    use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
    use uuid::Uuid;

    use super::{Endpoint, authentication_proof};
    use crate::generated::PROTOCOL_VERSION;

    #[test]
    fn authentication_proof_binds_every_identity_field() {
        let token = [7_u8; 32];
        let proof = authentication_proof(&token, "nonce", "client", "workspace", PROTOCOL_VERSION);
        assert_ne!(
            proof,
            authentication_proof(&token, "nonce", "other", "workspace", PROTOCOL_VERSION)
        );
        assert_ne!(
            proof,
            authentication_proof(&token, "nonce", "client", "other", PROTOCOL_VERSION)
        );
        assert_ne!(
            proof,
            authentication_proof(&token, "nonce", "client", "workspace", PROTOCOL_VERSION + 1)
        );
    }

    #[test]
    fn endpoint_loads_only_private_state_files() {
        let directory = std::env::temp_dir().join(format!("azem-ipc-{}", Uuid::new_v4()));
        fs::create_dir_all(&directory).unwrap();
        let token_path = directory.join("token");
        fs::write(&token_path, URL_SAFE_NO_PAD.encode([3_u8; 32])).unwrap();
        let endpoint_path = directory.join("endpoint.json");
        let endpoint = Endpoint {
            protocol: PROTOCOL_VERSION,
            workspace_id: "workspace".into(),
            workspace: PathBuf::from("/workspace"),
            address: "/tmp/azem.sock".into(),
            token_file: token_path.clone(),
            pid: 1,
            started_at: "now".into(),
        };
        fs::write(&endpoint_path, serde_json::to_vec(&endpoint).unwrap()).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&token_path, fs::Permissions::from_mode(0o600)).unwrap();
            fs::set_permissions(&endpoint_path, fs::Permissions::from_mode(0o600)).unwrap();
        }
        let loaded = Endpoint::load(&endpoint_path).unwrap();
        assert_eq!(loaded.workspace_id, endpoint.workspace_id);
        assert_eq!(loaded.token().unwrap(), vec![3_u8; 32]);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&endpoint_path, fs::Permissions::from_mode(0o644)).unwrap();
            assert_eq!(
                Endpoint::load(&endpoint_path).unwrap_err().kind(),
                std::io::ErrorKind::PermissionDenied
            );
        }
        fs::remove_dir_all(directory).unwrap();
    }
}
