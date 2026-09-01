package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestBuildUserMessageWithImageAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
		0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
		0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	user := message.NewText(message.RoleUser, "describe this")
	user.Metadata = map[string]string{
		"azem.attachments": `[{"id":"img1","name":"shot.png","mime":"image/png","path":` + jsonString(path) + `}]`,
	}
	data, err := BuildContext(WithAttachmentRoot(context.Background(), dir), hyprovider.Request{
		Model:    "gpt-test",
		Messages: []message.Message{user},
	}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Input []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != 1 || payload.Input[0].Role != "user" || len(payload.Input[0].Content) != 2 {
		t.Fatalf("payload = %s", data)
	}
	if payload.Input[0].Content[0].Type != "input_text" || payload.Input[0].Content[0].Text != "describe this" {
		t.Fatalf("text part = %+v", payload.Input[0].Content[0])
	}
	if payload.Input[0].Content[1].Type != "input_image" || !strings.HasPrefix(payload.Input[0].Content[1].ImageURL, "data:image/png;base64,") {
		t.Fatalf("image part = %+v", payload.Input[0].Content[1])
	}
}

func TestBuildAllowsImagesBeyondLegacyLimits(t *testing.T) {
	dir := t.TempDir()
	attachments := make([]map[string]any, 0, 7)
	for index := 0; index < 7; index++ {
		path := filepath.Join(dir, fmt.Sprintf("shot-%d.png", index))
		if err := os.WriteFile(path, testPNG(), 0o600); err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, map[string]any{
			"id": fmt.Sprintf("img%d", index), "name": filepath.Base(path), "mime": "image/png", "path": path,
		})
	}
	encodedAttachments, err := json.Marshal(attachments)
	if err != nil {
		t.Fatal(err)
	}
	user := message.NewText(message.RoleUser, "compare these")
	user.Metadata = map[string]string{"azem.attachments": string(encodedAttachments)}
	data, err := BuildContext(WithAttachmentRoot(context.Background(), dir), hyprovider.Request{
		Model: "gpt-test", Messages: []message.Message{user},
	}, BuildOptions{})
	if err != nil {
		t.Fatalf("build request with more than six images: %v", err)
	}
	var payload struct {
		Input []struct {
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != 1 || len(payload.Input[0].Content) != 8 {
		t.Fatalf("content parts = %d, want text plus seven images", len(payload.Input[0].Content))
	}

	largePath := filepath.Join(dir, "large.png")
	large := make([]byte, (8<<20)+1)
	copy(large, testPNG())
	if err := os.WriteFile(largePath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	largeUser := imageMessage(largePath)
	images, err := LoadImageAttachments(largeUser.Metadata, dir)
	if err != nil {
		t.Fatalf("load image larger than legacy limit: %v", err)
	}
	if len(images) != 1 || len(images[0].Data) != len(large) {
		t.Fatalf("loaded images = %d with %d bytes", len(images), len(images[0].Data))
	}
}

func TestBuildRejectsImageOutsideTrustedAttachmentRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, testPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	user := imageMessage(outside)
	_, err := BuildContext(WithAttachmentRoot(context.Background(), root), hyprovider.Request{
		Model:    "gpt-test",
		Messages: []message.Message{user},
	}, BuildOptions{})
	if err == nil || !strings.Contains(err.Error(), "outside the trusted attachment root") {
		t.Fatalf("Build error = %v, want trusted-root rejection", err)
	}
}

func TestBuildRejectsImageSymlinkOutsideTrustedAttachmentRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, testPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	user := imageMessage(link)
	_, err := BuildContext(WithAttachmentRoot(context.Background(), root), hyprovider.Request{
		Model:    "gpt-test",
		Messages: []message.Message{user},
	}, BuildOptions{})
	if err == nil || !strings.Contains(err.Error(), "outside the trusted attachment root") {
		t.Fatalf("Build error = %v, want symlink escape rejection", err)
	}
}

func imageMessage(path string) message.Message {
	user := message.NewText(message.RoleUser, "describe this")
	user.Metadata = map[string]string{
		"azem.attachments": `[{"id":"img1","name":"shot.png","mime":"image/png","path":` + jsonString(path) + `}]`,
	}
	return user
}

func testPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
		0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
		0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
