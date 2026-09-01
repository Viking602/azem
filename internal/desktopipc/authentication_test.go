package desktopipc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthenticationProofBindsClientWorkspaceAndVersion(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatal(err)
	}
	proof := AuthenticationProof(token, nonce, "client", "workspace", ProtocolVersion)
	if !VerifyAuthenticationProof(token, nonce, "client", "workspace", ProtocolVersion, proof) {
		t.Fatal("valid proof was rejected")
	}
	for _, changed := range []struct {
		client, workspace string
		version           int
	}{
		{client: "other", workspace: "workspace", version: ProtocolVersion},
		{client: "client", workspace: "other", version: ProtocolVersion},
		{client: "client", workspace: "workspace", version: ProtocolVersion + 1},
	} {
		if VerifyAuthenticationProof(token, nonce, changed.client, changed.workspace, changed.version, proof) {
			t.Fatalf("proof accepted changed identity %#v", changed)
		}
	}
}

func TestTokenAndEndpointFilesAreOwnerOnly(t *testing.T) {
	directory := t.TempDir()
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(directory, "token")
	if err := WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	endpointPath := filepath.Join(directory, "endpoint.json")
	endpoint := Endpoint{Protocol: ProtocolVersion, WorkspaceID: "workspace", Workspace: directory, Address: filepath.Join(directory, "socket"), TokenFile: tokenPath, PID: os.Getpid()}
	if err := WriteEndpointFile(endpointPath, endpoint); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{tokenPath, endpointPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", path, permissions)
		}
	}
	loaded, err := ReadEndpointFile(endpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkspaceID != endpoint.WorkspaceID || loaded.TokenFile != tokenPath {
		t.Fatalf("loaded endpoint = %#v", loaded)
	}
}
