package desktopipc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestTransferManagerRejectsOutOfOrderAndOversizedChunks(t *testing.T) {
	manager, err := NewTransferManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	data := []byte("attachment")
	digest := sha256.Sum256(data)
	payload, _ := json.Marshal(beginAttachmentParams{
		TransferID: "transfer", SessionID: "session", Name: "file.txt", MIMEType: "text/plain",
		ByteLength: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), ChunkCount: 1,
	})
	if err := manager.Begin(payload); err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteChunk(BinaryMetadata{TransferID: "transfer", Index: 1, Count: 1}, data); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("out-of-order error = %v", err)
	}
	if err := manager.WriteChunk(BinaryMetadata{TransferID: "transfer", Index: 0, Count: 1}, append(data, 'x')); err == nil || !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestTransferManagerRejectsDigestMismatchBeforeImport(t *testing.T) {
	manager, err := NewTransferManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	data := []byte("attachment")
	payload, _ := json.Marshal(beginAttachmentParams{
		TransferID: "transfer", SessionID: "session", Name: "file.txt", MIMEType: "text/plain",
		ByteLength: int64(len(data)), SHA256: strings.Repeat("0", sha256.Size*2), ChunkCount: 1,
	})
	if err := manager.Begin(payload); err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteChunk(BinaryMetadata{TransferID: "transfer", Index: 0, Count: 1}, data); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Commit("transfer", nil); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("commit error = %v", err)
	}
}
