package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func minimalPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
		0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
		0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func TestImportBytesStoresImage(t *testing.T) {
	store := NewAttachmentStore(filepath.Join(t.TempDir(), "atts"))
	att, err := store.ImportBytes("session-1", "shot.png", "image/png", minimalPNG())
	if err != nil {
		t.Fatal(err)
	}
	if att.MIME != "image/png" || att.Size == 0 {
		t.Fatalf("attachment = %+v", att)
	}
	if _, err := os.Stat(att.Path); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateSessionAttachments("session-1", []session.Attachment{att}); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateSessionAttachments("session-2", []session.Attachment{att}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-session attachment validation = %v", err)
	}
}

func TestAttachmentStoreReadKeepsSessionBoundary(t *testing.T) {
	store := NewAttachmentStore(filepath.Join(t.TempDir(), "attachments"))
	want := minimalPNG()
	att, err := store.ImportBytes("session-1", "preview.png", "image/png", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Read("session-1", att)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("attachment bytes = %x, want %x", got, want)
	}
	if _, err := store.Read("session-2", att); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-session read error = %v", err)
	}
}

func TestAttachmentStoreAllowsImagesBeyondLegacyLimits(t *testing.T) {
	store := NewAttachmentStore(filepath.Join(t.TempDir(), "attachments"))
	large := make([]byte, (8<<20)+1)
	copy(large, minimalPNG())
	largeAttachment, err := store.ImportBytes("session-1", "large.png", "image/png", large)
	if err != nil {
		t.Fatalf("import image larger than legacy limit: %v", err)
	}
	read, err := store.Read("session-1", largeAttachment)
	if err != nil {
		t.Fatalf("read image larger than legacy limit: %v", err)
	}
	if len(read) != len(large) {
		t.Fatalf("large attachment bytes = %d, want %d", len(read), len(large))
	}
	sourcePath := filepath.Join(t.TempDir(), "large-source.png")
	if err := os.WriteFile(sourcePath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	importedFromPath, err := store.Import("session-1", sourcePath)
	if err != nil {
		t.Fatalf("import image path larger than legacy limit: %v", err)
	}
	if importedFromPath.Size != int64(len(large)) {
		t.Fatalf("path attachment bytes = %d, want %d", importedFromPath.Size, len(large))
	}

	attachments := make([]session.Attachment, 0, 7)
	for index := 0; index < 7; index++ {
		att, importErr := store.ImportBytes("session-1", fmt.Sprintf("shot-%d.png", index), "image/png", minimalPNG())
		if importErr != nil {
			t.Fatal(importErr)
		}
		attachments = append(attachments, att)
	}
	if err := store.ValidateSessionAttachments("session-1", attachments); err != nil {
		t.Fatalf("validate more than six images: %v", err)
	}
}

func TestUserMessageWithAttachmentsMetadata(t *testing.T) {
	msg := UserMessageWithAttachments("look", []session.Attachment{{
		ID: "img_1", Name: "a.png", MIME: "image/png", Path: "/tmp/a.png",
	}})
	if msg.Text != "look" {
		t.Fatalf("text = %q", msg.Text)
	}
	atts := AttachmentsFromMessage(msg)
	if len(atts) != 1 || atts[0].Name != "a.png" {
		t.Fatalf("meta attachments = %#v", atts)
	}
}
