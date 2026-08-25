package desktopipc

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const tokenBytes = 32

type Endpoint struct {
	Protocol    int       `json:"protocol"`
	WorkspaceID string    `json:"workspaceId"`
	Workspace   string    `json:"workspace"`
	Address     string    `json:"address"`
	TokenFile   string    `json:"tokenFile"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"startedAt"`
}

func WorkspaceID(workspace string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return hex.EncodeToString(digest[:16]), nil
}

func GenerateToken() ([]byte, error) {
	token := make([]byte, tokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	return token, nil
}

func GenerateNonce() (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

func EncodeToken(token []byte) string {
	return base64.RawURLEncoding.EncodeToString(token)
}

func DecodeToken(encoded string) ([]byte, error) {
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(token) != tokenBytes {
		return nil, errors.New("IPC token is invalid")
	}
	return token, nil
}

func AuthenticationProof(token []byte, nonce, clientID, workspaceID string, protocol int) string {
	mac := hmac.New(sha256.New, token)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s\x00%d", nonce, clientID, workspaceID, protocol)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func VerifyAuthenticationProof(token []byte, nonce, clientID, workspaceID string, protocol int, proof string) bool {
	expected := AuthenticationProof(token, nonce, clientID, workspaceID, protocol)
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(proof)))
}

func WriteTokenFile(path string, token []byte) error {
	if len(token) != tokenBytes {
		return errors.New("IPC token must contain 32 bytes")
	}
	return atomicPrivateFile(path, []byte(EncodeToken(token)+"\n"))
}

func ReadTokenFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("IPC token file %s is not private", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeToken(string(encoded))
}

func WriteEndpointFile(path string, endpoint Endpoint) error {
	encoded, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return err
	}
	return atomicPrivateFile(path, append(encoded, '\n'))
}

func ReadEndpointFile(path string) (Endpoint, error) {
	var endpoint Endpoint
	info, err := os.Stat(path)
	if err != nil {
		return endpoint, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return endpoint, fmt.Errorf("IPC endpoint file %s is not private", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return endpoint, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&endpoint); err != nil {
		return endpoint, err
	}
	if endpoint.Protocol != ProtocolVersion || endpoint.WorkspaceID == "" || endpoint.Address == "" || endpoint.TokenFile == "" || endpoint.PID <= 0 {
		return endpoint, errors.New("IPC endpoint metadata is incomplete")
	}
	return endpoint, nil
}

func atomicPrivateFile(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".azem-ipc-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(content)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
