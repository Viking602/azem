// Package blobstore is the content-addressed file store for large Azem
// payloads. SQLite keeps catalog rows and hashes; the bytes live on disk.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Store persists opaque payloads by SHA-256 digest.
type Store interface {
	Put(_ context.Context, payload []byte) (string, error)
	PutAt(_ context.Context, digest string, payload []byte) error
	InstallAt(_ context.Context, digest string, payload []byte) (bool, error)
	Get(_ context.Context, digest string) ([]byte, error)
	Exists(_ context.Context, digest string) (bool, error)
	Delete(_ context.Context, digest string) error
}

// Directory is a durable store under root/<aa>/<digest>.
type Directory struct {
	root string
}

func NewDirectory(root string) (*Directory, error) {
	if root == "" {
		return nil, fmt.Errorf("blob store root is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create blob store: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("protect blob store: %w", err)
	}
	return &Directory{root: root}, nil
}

func (s *Directory) Put(ctx context.Context, payload []byte) (string, error) {
	digest := Sum(payload)
	return digest, s.PutAt(ctx, digest, payload)
}

func (s *Directory) PutAt(ctx context.Context, digest string, payload []byte) error {
	_, err := s.InstallAt(ctx, digest, payload)
	return err
}

// InstallAt atomically reports whether this call created the digest path.
// A pre-existing corrupt path is repaired but is never reported as owned by
// the caller, so rollback cleanup cannot delete another writer's blob.
func (s *Directory) InstallAt(_ context.Context, digest string, payload []byte) (bool, error) {
	if err := validatePayloadDigest(digest, payload); err != nil {
		return false, err
	}
	path := s.path(digest)
	valid, existed, err := inspectBlob(path, digest, int64(len(payload)))
	if err != nil {
		return false, err
	}
	if valid {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create blob directory: %w", err)
	}
	tmpName, err := writeBlobTemp(filepath.Dir(path), payload)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmpName)
	if !existed {
		if err := os.Link(tmpName, path); err == nil {
			return true, nil
		}
		valid, existed, err = inspectBlob(path, digest, int64(len(payload)))
		if err != nil {
			return false, err
		}
		if valid {
			return false, nil
		}
		if !existed {
			return false, fmt.Errorf("install blob %s: destination was not created", digest)
		}
	}
	if err := replaceBlob(tmpName, path); err != nil {
		return false, fmt.Errorf("repair blob %s: %w", digest, err)
	}
	return false, nil
}

func inspectBlob(path, digest string, size int64) (valid bool, exists bool, err error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("stat blob %s: %w", digest, err)
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return false, true, nil
	}
	existing, err := readFileLimited(path, size)
	return err == nil && validatePayloadDigest(digest, existing) == nil, true, nil
}

func writeBlobTemp(directory string, payload []byte) (string, error) {
	tmp, err := os.CreateTemp(directory, ".blob-*")
	if err != nil {
		return "", fmt.Errorf("create blob tempfile: %w", err)
	}
	name := tmp.Name()
	if err := writePreparedBlob(tmp, payload); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func writePreparedBlob(tmp *os.File, payload []byte) error {
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return fmt.Errorf("write blob: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close blob: %w", err)
	}
	return nil
}

func replaceBlob(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}

func (s *Directory) Exists(_ context.Context, digest string) (bool, error) {
	if err := validateDigest(digest); err != nil {
		return false, err
	}
	info, err := os.Stat(s.path(digest))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat blob %s: %w", digest, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("blob %s is not a regular file", digest)
	}
	return true, nil
}

func (s *Directory) Delete(_ context.Context, digest string) error {
	if err := validateDigest(digest); err != nil {
		return err
	}
	if err := os.Remove(s.path(digest)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete blob %s: %w", digest, err)
	}
	return nil
}

func (s *Directory) Get(ctx context.Context, digest string) ([]byte, error) {
	return s.GetLimited(ctx, digest, 0)
}

func (s *Directory) GetLimited(_ context.Context, digest string, maxBytes int64) ([]byte, error) {
	if err := validateDigest(digest); err != nil {
		return nil, err
	}
	path := s.path(digest)
	if maxBytes > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat blob %s: %w", digest, err)
		}
		if info.Size() > maxBytes {
			return nil, fmt.Errorf("blob %s exceeds %d-byte limit", digest, maxBytes)
		}
	}
	payload, err := readFileLimited(path, maxBytes)
	if err != nil {
		if maxBytes > 0 && errors.Is(err, errBlobTooLarge) {
			return nil, fmt.Errorf("blob %s exceeds %d-byte limit", digest, maxBytes)
		}
		return nil, fmt.Errorf("read blob %s: %w", digest, err)
	}
	if err := validatePayloadDigest(digest, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *Directory) path(digest string) string {
	return filepath.Join(s.root, digest[:2], digest)
}

// Memory is an in-process store used by :memory: databases and unit tests.
type Memory struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{data: map[string][]byte{}}
}

func (s *Memory) Put(ctx context.Context, payload []byte) (string, error) {
	digest := Sum(payload)
	return digest, s.PutAt(ctx, digest, payload)
}

func (s *Memory) PutAt(ctx context.Context, digest string, payload []byte) error {
	_, err := s.InstallAt(ctx, digest, payload)
	return err
}

func (s *Memory) InstallAt(_ context.Context, digest string, payload []byte) (bool, error) {
	if err := validatePayloadDigest(digest, payload); err != nil {
		return false, err
	}
	s.mu.Lock()
	_, existed := s.data[digest]
	s.data[digest] = append([]byte(nil), payload...)
	s.mu.Unlock()
	return !existed, nil
}

func (s *Memory) Exists(_ context.Context, digest string) (bool, error) {
	if err := validateDigest(digest); err != nil {
		return false, err
	}
	s.mu.RLock()
	_, ok := s.data[digest]
	s.mu.RUnlock()
	return ok, nil
}

func (s *Memory) Delete(_ context.Context, digest string) error {
	if err := validateDigest(digest); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.data, digest)
	s.mu.Unlock()
	return nil
}

func (s *Memory) Get(ctx context.Context, digest string) ([]byte, error) {
	return s.GetLimited(ctx, digest, 0)
}

func (s *Memory) GetLimited(_ context.Context, digest string, maxBytes int64) ([]byte, error) {
	if err := validateDigest(digest); err != nil {
		return nil, err
	}
	s.mu.RLock()
	payload, ok := s.data[digest]
	if ok && maxBytes > 0 && int64(len(payload)) > maxBytes {
		s.mu.RUnlock()
		return nil, fmt.Errorf("blob %s exceeds %d-byte limit", digest, maxBytes)
	}
	if ok {
		payload = append([]byte(nil), payload...)
	}
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("read blob %s: not found", digest)
	}
	if err := validatePayloadDigest(digest, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

var errBlobTooLarge = errors.New("blob exceeds byte limit")

func readFileLimited(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return os.ReadFile(path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, errBlobTooLarge
	}
	return payload, nil
}

func Sum(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func validatePayloadDigest(digest string, payload []byte) error {
	if err := validateDigest(digest); err != nil {
		return err
	}
	actual := Sum(payload)
	if actual != digest {
		return fmt.Errorf("blob %s failed integrity check: got sha256 %s", digest, actual)
	}
	return nil
}

func ValidDigest(digest string) bool {
	return validateDigest(digest) == nil
}

func validateDigest(digest string) error {
	if len(digest) != 64 {
		return fmt.Errorf("blob digest %q is invalid", digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("blob digest %q is invalid", digest)
	}
	return nil
}
