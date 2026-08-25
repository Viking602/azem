package desktopipc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type beginAttachmentParams struct {
	TransferID string `json:"transferId"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	ByteLength int64  `json:"byteLength"`
	SHA256     string `json:"sha256"`
	ChunkCount int    `json:"chunkCount"`
}

type transferState struct {
	params    beginAttachmentParams
	path      string
	file      *os.File
	nextIndex int
	bytes     int64
}
type TransferManager struct {
	mu        sync.Mutex
	directory string
	states    map[string]*transferState
}

func NewTransferManager(directory string) (*TransferManager, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("attachment transfer directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &TransferManager{directory: directory, states: make(map[string]*transferState)}, nil
}

func (manager *TransferManager) Begin(payload json.RawMessage) error {
	params, err := decodeParams[beginAttachmentParams](payload)
	if err != nil {
		return err
	}
	params.TransferID = strings.TrimSpace(params.TransferID)
	if params.TransferID == "" || params.SessionID == "" || params.Name == "" || params.MIMEType == "" {
		return errors.New("attachment transfer identity is incomplete")
	}
	if params.ByteLength < 0 || params.ByteLength > MaxReassembledBinary {
		return fmt.Errorf("attachment transfer size %d is invalid", params.ByteLength)
	}
	if len(params.SHA256) != sha256.Size*2 {
		return errors.New("attachment transfer SHA-256 is invalid")
	}
	expectedChunks := int((params.ByteLength + MaxBinaryChunkBytes - 1) / MaxBinaryChunkBytes)
	if params.ChunkCount != expectedChunks {
		return fmt.Errorf("attachment transfer chunk count %d, want %d", params.ChunkCount, expectedChunks)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.states[params.TransferID]; exists {
		return errors.New("attachment transfer already exists")
	}
	file, err := os.CreateTemp(manager.directory, ".attachment-*")
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(file.Name())
		return err
	}
	manager.states[params.TransferID] = &transferState{params: params, path: file.Name(), file: file}
	return nil
}

func (manager *TransferManager) WriteChunk(metadata BinaryMetadata, data []byte) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.states[metadata.TransferID]
	if state == nil {
		return errors.New("attachment transfer is unknown")
	}
	if metadata.Index != state.nextIndex || metadata.Count != state.params.ChunkCount {
		return fmt.Errorf("attachment chunk index %d/%d is out of order", metadata.Index, metadata.Count)
	}
	if len(data) > MaxBinaryChunkBytes || state.bytes+int64(len(data)) > state.params.ByteLength {
		return errors.New("attachment chunk exceeds declared size")
	}
	if _, err := state.file.Write(data); err != nil {
		manager.abortLocked(metadata.TransferID)
		return err
	}
	state.bytes += int64(len(data))
	state.nextIndex++
	return nil
}

func (manager *TransferManager) Commit(transferID string, dispatcher RequestDispatcher) (any, error) {
	manager.mu.Lock()
	state := manager.states[strings.TrimSpace(transferID)]
	if state == nil {
		manager.mu.Unlock()
		return nil, errors.New("attachment transfer is unknown")
	}
	delete(manager.states, transferID)
	manager.mu.Unlock()
	defer os.Remove(state.path)
	if err := state.file.Sync(); err == nil {
		err = state.file.Close()
		if err != nil {
			return nil, err
		}
	} else {
		state.file.Close()
		return nil, err
	}
	if state.bytes != state.params.ByteLength || state.nextIndex != state.params.ChunkCount {
		return nil, errors.New("attachment transfer is incomplete")
	}
	file, err := os.Open(filepath.Clean(state.path))
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	data, err := io.ReadAll(io.TeeReader(io.LimitReader(file, MaxReassembledBinary+1), hash))
	file.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > MaxReassembledBinary {
		return nil, errors.New("attachment transfer exceeds reassembly limit")
	}
	if digest := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(digest, state.params.SHA256) {
		return nil, errors.New("attachment transfer digest mismatch")
	}
	if dispatcher == nil {
		return nil, errors.New("attachment dispatcher is unavailable")
	}
	return dispatcher.ImportAttachmentBytes(state.params.SessionID, state.params.Name, state.params.MIMEType, data)
}

func (manager *TransferManager) Abort(transferID string) {
	manager.mu.Lock()
	manager.abortLocked(strings.TrimSpace(transferID))
	manager.mu.Unlock()
}

func (manager *TransferManager) Close() {
	manager.mu.Lock()
	for transferID := range manager.states {
		manager.abortLocked(transferID)
	}
	manager.mu.Unlock()
}

func (manager *TransferManager) abortLocked(transferID string) {
	state := manager.states[transferID]
	delete(manager.states, transferID)
	if state != nil {
		state.file.Close()
		os.Remove(state.path)
	}
}
