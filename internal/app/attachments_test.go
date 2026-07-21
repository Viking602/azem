package app

import (
	"os"
	"path/filepath"
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
	if err := ValidateTurnAttachments([]session.Attachment{att}); err != nil {
		t.Fatal(err)
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
