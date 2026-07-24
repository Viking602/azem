package app

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadClipboardImageDarwinIgnoresSuccessfulCommandStderr(t *testing.T) {
	want := minimalPNG()
	data, mimeType, err := readClipboardImageDarwinWithRunner(func(_ string, path string) ([]byte, []byte, error) {
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
		return []byte("image/png\n"), []byte("*** error creating a jp2 color space: falling back to sRGB\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if mimeType != "image/png" {
		t.Fatalf("mime type = %q, want image/png", mimeType)
	}
	if !bytes.Equal(data, want) {
		t.Fatal("clipboard image data changed")
	}
}

func TestReadClipboardImageDarwinReportsCommandStderrOnFailure(t *testing.T) {
	_, _, err := readClipboardImageDarwinWithRunner(func(_ string, _ string) ([]byte, []byte, error) {
		return nil, []byte("permission denied\n"), errors.New("exit status 1")
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want stderr detail", err)
	}
}
