package sessionshare

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	KeyBytes             = 32
	NonceBytes           = 12
	ServerMaxSealedBytes = 1_000_000
	GistMaxSealedBytes   = 5_000_000
	maxUncompressedBytes = 256 << 20
	shareDocumentVersion = 1
)

type Document struct {
	Version  int          `json:"version"`
	SharedAt time.Time    `json:"sharedAt"`
	Session  ShareSession `json:"session"`
}

func sealDocument(document Document, key []byte) ([]byte, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("share key must contain %d bytes", KeyBytes)
	}
	plain, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if len(plain) > maxUncompressedBytes {
		return nil, fmt.Errorf("share snapshot exceeds %d bytes", maxUncompressedBytes)
	}
	var compressed bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gzipWriter.Write(plain); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := make([]byte, len(nonce), len(nonce)+compressed.Len()+gcm.Overhead())
	copy(sealed, nonce)
	sealed = gcm.Seal(sealed, nonce, compressed.Bytes(), nil)
	return sealed, nil
}

func Open(sealed, key []byte) (Document, error) {
	if len(key) != KeyBytes {
		return Document{}, fmt.Errorf("share key must contain %d bytes", KeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Document{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Document{}, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return Document{}, errors.New("encrypted share is truncated")
	}
	nonce := sealed[:gcm.NonceSize()]
	compressed, err := gcm.Open(nil, nonce, sealed[gcm.NonceSize():], nil)
	if err != nil {
		return Document{}, errors.New("encrypted share authentication failed")
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return Document{}, fmt.Errorf("open encrypted share compression: %w", err)
	}
	plain, err := io.ReadAll(io.LimitReader(gzipReader, maxUncompressedBytes+1))
	closeErr := gzipReader.Close()
	if err != nil {
		return Document{}, err
	}
	if closeErr != nil {
		return Document{}, closeErr
	}
	if len(plain) > maxUncompressedBytes {
		return Document{}, fmt.Errorf("encrypted share expands beyond %d bytes", maxUncompressedBytes)
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(plain))
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("decode encrypted share: %w", err)
	}
	if document.Version != shareDocumentVersion {
		return Document{}, fmt.Errorf("unsupported encrypted share version %d", document.Version)
	}
	return document, nil
}

func EncodeKey(key []byte) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

func DecodeKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("share key must contain %d bytes", KeyBytes)
	}
	return key, nil
}
