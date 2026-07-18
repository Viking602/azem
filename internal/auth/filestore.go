package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("credential file path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("protect credential directory: %w", err)
	}
	return &FileStore{path: path}, nil
}

func (s *FileStore) Put(_ context.Context, value Credential) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return "", err
	}
	key := credentialKey(value.Provider, value.AccountID)
	values[key] = value
	if err := s.save(values); err != nil {
		return "", err
	}
	return "file:" + key, nil
}

func (s *FileStore) Get(_ context.Context, provider string, accountID string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return Credential{}, err
	}
	value, ok := values[credentialKey(provider, accountID)]
	if !ok {
		return Credential{}, ErrCredentialNotFound
	}
	return value, nil
}

func (s *FileStore) Delete(_ context.Context, provider string, accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return err
	}
	delete(values, credentialKey(provider, accountID))
	return s.save(values)
}

func (s *FileStore) load() (map[string]Credential, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]Credential), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("credential file exceeds 1 MiB")
	}
	values := make(map[string]Credential)
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decode credential file: %w", err)
	}
	return values, nil
}

func (s *FileStore) save(values map[string]Credential) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".azem-credentials-*")
	if err != nil {
		return fmt.Errorf("create credential temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync credential file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close credential file: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}

var _ CredentialStore = (*FileStore)(nil)
