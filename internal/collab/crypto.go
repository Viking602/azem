package collab

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func SealFrame(key []byte, frame Frame) ([]byte, error) {
	if len(key) != RoomKeyBytes {
		return nil, fmt.Errorf("collaboration room key must contain %d bytes", RoomKeyBytes)
	}
	if frame.Type == "" {
		return nil, errors.New("collaboration frame type is required")
	}
	plain, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if len(plain) > maxFrameBytes-EnvelopeHeaderBytes-64 {
		return nil, errors.New("collaboration frame is oversized")
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
	sealed := make([]byte, len(nonce), len(nonce)+len(plain)+gcm.Overhead())
	copy(sealed, nonce)
	return gcm.Seal(sealed, nonce, plain, nil), nil
}

func OpenFrame(key, sealed []byte) (Frame, error) {
	if len(key) != RoomKeyBytes {
		return Frame{}, fmt.Errorf("collaboration room key must contain %d bytes", RoomKeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Frame{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Frame{}, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() || len(sealed) > maxFrameBytes {
		return Frame{}, errors.New("collaboration sealed frame is truncated or oversized")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return Frame{}, errors.New("collaboration frame authentication failed")
	}
	var frame Frame
	if err := json.Unmarshal(plain, &frame); err != nil {
		return Frame{}, errors.New("collaboration frame is malformed")
	}
	if frame.Type == "" {
		return Frame{}, errors.New("collaboration frame type is missing")
	}
	return frame, nil
}

func GenerateWriteToken() ([]byte, error) {
	value := make([]byte, WriteTokenBytes)
	_, err := rand.Read(value)
	return value, err
}
