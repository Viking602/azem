package authbroker

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func LoadToken(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("broker token file must be a private regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(payload))
	if len(token) < 32 || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("broker token file is malformed")
	}
	return token, nil
}

func EnsureToken(path string, regenerate bool) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return "", errors.New("broker token path is required")
	}
	if !regenerate {
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
				return "", errors.New("broker token file must be a private regular file")
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			token := strings.TrimSpace(string(payload))
			if len(token) < 32 || strings.ContainsAny(token, "\r\n\x00") {
				return "", errors.New("broker token file is malformed")
			}
			return token, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	temporary, err := os.CreateTemp(directory, ".auth-token-*")
	if err != nil {
		return "", err
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
		return "", err
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("refusing to replace symlinked broker token file")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(name, path); err != nil {
		return "", err
	}
	committed = true
	return token, nil
}
