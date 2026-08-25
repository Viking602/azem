package authbroker

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var cacheMagic = []byte("AZBC1")

type SnapshotCache struct {
	Path string
	TTL  time.Duration
	Now  func() time.Time
}

func (cache SnapshotCache) Read(brokerURL, token string) (Snapshot, error) {
	if cache.Path == "" || cache.TTL <= 0 {
		return Snapshot{}, errors.New("broker snapshot cache is disabled")
	}
	info, err := os.Lstat(cache.Path)
	if err != nil {
		return Snapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return Snapshot{}, errors.New("broker snapshot cache must be a bounded regular file")
	}
	payload, err := os.ReadFile(cache.Path)
	if err != nil {
		return Snapshot{}, err
	}
	if len(payload) <= len(cacheMagic)+12+16 || !bytes.Equal(payload[:len(cacheMagic)], cacheMagic) {
		return Snapshot{}, errors.New("broker snapshot cache is malformed")
	}
	key := sha256.Sum256([]byte(token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return Snapshot{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Snapshot{}, err
	}
	nonce := payload[len(cacheMagic) : len(cacheMagic)+gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, payload[len(cacheMagic)+gcm.NonceSize():], []byte(strings.TrimRight(brokerURL, "/")))
	if err != nil {
		return Snapshot{}, errors.New("broker snapshot cache authentication failed")
	}
	var snapshot Snapshot
	if json.Unmarshal(plain, &snapshot) != nil || snapshot.Version != 1 || snapshot.GeneratedAt.IsZero() {
		return Snapshot{}, errors.New("broker snapshot cache payload is invalid")
	}
	now := time.Now().UTC()
	if cache.Now != nil {
		now = cache.Now().UTC()
	}
	if now.Sub(snapshot.GeneratedAt) < 0 || now.Sub(snapshot.GeneratedAt) > cache.TTL {
		return Snapshot{}, errors.New("broker snapshot cache is expired")
	}
	return snapshot, nil
}

func (cache SnapshotCache) Write(brokerURL, token string, snapshot Snapshot) error {
	if cache.Path == "" || cache.TTL <= 0 {
		return nil
	}
	if snapshot.Version != 1 || snapshot.GeneratedAt.IsZero() {
		return errors.New("cannot cache invalid broker snapshot")
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	payload := append(append([]byte(nil), cacheMagic...), nonce...)
	payload = gcm.Seal(payload, nonce, plain, []byte(strings.TrimRight(brokerURL, "/")))
	directory := filepath.Dir(cache.Path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(cache.Path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to replace symlinked broker snapshot cache")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".auth-broker-cache-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(name)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, cache.Path); err != nil {
		return fmt.Errorf("install broker snapshot cache: %w", err)
	}
	committed = true
	return nil
}
