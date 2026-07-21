package app

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/message"
)

const (
	attachmentMetaKey   = "azem.attachments"
	maxImagesPerTurn    = 6
	maxImageBytes       = 8 << 20 // 8 MiB
	maxImageReadProbe   = 512
	defaultImageQuality = "auto"
)

var allowedImageMIME = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/jpg":  ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// AttachmentStore copies user-selected images into a durable per-session directory.
type AttachmentStore struct {
	Root string
}

func NewAttachmentStore(root string) AttachmentStore {
	return AttachmentStore{Root: strings.TrimSpace(root)}
}

func (s AttachmentStore) Import(sessionID, sourcePath string) (session.Attachment, error) {
	if strings.TrimSpace(s.Root) == "" {
		return session.Attachment{}, fmt.Errorf("attachment store is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.Attachment{}, fmt.Errorf("session id is required")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return session.Attachment{}, fmt.Errorf("image path is required")
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		abs, err := filepath.Abs(sourcePath)
		if err != nil {
			return session.Attachment{}, fmt.Errorf("resolve image path: %w", err)
		}
		sourcePath = abs
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return session.Attachment{}, fmt.Errorf("stat image: %w", err)
	}
	if info.IsDir() {
		return session.Attachment{}, fmt.Errorf("image path is a directory")
	}
	if info.Size() <= 0 {
		return session.Attachment{}, fmt.Errorf("image is empty")
	}
	if info.Size() > maxImageBytes {
		return session.Attachment{}, fmt.Errorf("image exceeds %d MiB limit", maxImageBytes>>20)
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return session.Attachment{}, fmt.Errorf("open image: %w", err)
	}
	defer file.Close()
	probe := make([]byte, maxImageReadProbe)
	n, err := io.ReadFull(file, probe)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return session.Attachment{}, fmt.Errorf("read image: %w", err)
	}
	probe = probe[:n]
	mimeType := http.DetectContentType(probe)
	if mimeType == "application/octet-stream" {
		if ext := strings.ToLower(filepath.Ext(sourcePath)); ext != "" {
			if detected := mime.TypeByExtension(ext); detected != "" {
				mimeType = detected
			}
		}
	}
	mimeType = normalizeImageMIME(mimeType)
	if _, ok := allowedImageMIME[mimeType]; !ok {
		return session.Attachment{}, fmt.Errorf("unsupported image type %q (png, jpeg, gif, webp)", mimeType)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return session.Attachment{}, fmt.Errorf("rewind image: %w", err)
	}
	id, err := randomID("img")
	if err != nil {
		return session.Attachment{}, err
	}
	dir := filepath.Join(s.Root, sanitizePathComponent(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return session.Attachment{}, fmt.Errorf("create attachment dir: %w", err)
	}
	name := filepath.Base(sourcePath)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = id + allowedImageMIME[mimeType]
	}
	dest := filepath.Join(dir, id+filepath.Ext(name))
	if ext := filepath.Ext(dest); ext == "" {
		dest += allowedImageMIME[mimeType]
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return session.Attachment{}, fmt.Errorf("create attachment file: %w", err)
	}
	written, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return session.Attachment{}, fmt.Errorf("copy image: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return session.Attachment{}, closeErr
	}
	return session.Attachment{
		ID:   id,
		Name: name,
		MIME: mimeType,
		Path: dest,
		Size: written,
	}, nil
}


func (s AttachmentStore) ImportBytes(sessionID, name, mimeType string, data []byte) (session.Attachment, error) {
	if strings.TrimSpace(s.Root) == "" {
		return session.Attachment{}, fmt.Errorf("attachment store is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.Attachment{}, fmt.Errorf("session id is required")
	}
	if len(data) == 0 {
		return session.Attachment{}, fmt.Errorf("image is empty")
	}
	if len(data) > maxImageBytes {
		return session.Attachment{}, fmt.Errorf("image exceeds %d MiB limit", maxImageBytes>>20)
	}
	mimeType = normalizeImageMIME(mimeType)
	if mimeType == "" || mimeType == "application/octet-stream" {
		probe := data
		if len(probe) > maxImageReadProbe {
			probe = probe[:maxImageReadProbe]
		}
		mimeType = normalizeImageMIME(http.DetectContentType(probe))
	}
	if _, ok := allowedImageMIME[mimeType]; !ok {
		return session.Attachment{}, fmt.Errorf("unsupported image type %q (png, jpeg, gif, webp)", mimeType)
	}
	id, err := randomID("img")
	if err != nil {
		return session.Attachment{}, err
	}
	dir := filepath.Join(s.Root, sanitizePathComponent(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return session.Attachment{}, fmt.Errorf("create attachment dir: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = id + allowedImageMIME[mimeType]
	}
	ext := filepath.Ext(name)
	if ext == "" {
		ext = allowedImageMIME[mimeType]
		name += ext
	}
	dest := filepath.Join(dir, id+ext)
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return session.Attachment{}, fmt.Errorf("write attachment: %w", err)
	}
	return session.Attachment{
		ID: id, Name: filepath.Base(name), MIME: mimeType, Path: dest, Size: int64(len(data)),
	}, nil
}

func normalizeImageMIME(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "image/jpg" {
		return "image/jpeg"
	}
	if idx := strings.Index(value, ";"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

func sanitizePathComponent(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_")
	return replacer.Replace(value)
}

func ValidateTurnAttachments(atts []session.Attachment) error {
	if len(atts) == 0 {
		return nil
	}
	if len(atts) > maxImagesPerTurn {
		return fmt.Errorf("at most %d images can be attached per turn", maxImagesPerTurn)
	}
	seen := make(map[string]struct{}, len(atts))
	for _, att := range atts {
		if strings.TrimSpace(att.Path) == "" {
			return fmt.Errorf("attachment path is required")
		}
		if _, ok := allowedImageMIME[normalizeImageMIME(att.MIME)]; !ok && att.MIME != "" {
			return fmt.Errorf("unsupported image type %q", att.MIME)
		}
		if _, dup := seen[att.Path]; dup {
			return fmt.Errorf("duplicate attachment %q", att.Name)
		}
		seen[att.Path] = struct{}{}
		info, err := os.Stat(att.Path)
		if err != nil {
			return fmt.Errorf("attachment %q: %w", att.Name, err)
		}
		if info.IsDir() || info.Size() <= 0 {
			return fmt.Errorf("attachment %q is not a readable image file", att.Name)
		}
		if info.Size() > maxImageBytes {
			return fmt.Errorf("attachment %q exceeds %d MiB limit", att.Name, maxImageBytes>>20)
		}
	}
	return nil
}

func EncodeAttachmentsMeta(atts []session.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	encoded, err := json.Marshal(atts)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func DecodeAttachmentsMeta(raw string) []session.Attachment {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var atts []session.Attachment
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil
	}
	return atts
}

func AttachmentsFromMessage(msg message.Message) []session.Attachment {
	if msg.Metadata == nil {
		return nil
	}
	return DecodeAttachmentsMeta(msg.Metadata[attachmentMetaKey])
}

func UserMessageWithAttachments(text string, atts []session.Attachment) message.Message {
	msg := message.NewText(message.RoleUser, text)
	if len(atts) == 0 {
		return msg
	}
	cloned := append([]session.Attachment(nil), atts...)
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[attachmentMetaKey] = EncodeAttachmentsMeta(cloned)
	return msg
}

func CloneAttachments(atts []session.Attachment) []session.Attachment {
	if len(atts) == 0 {
		return nil
	}
	return append([]session.Attachment(nil), atts...)
}

func FormatAttachmentLabel(atts []session.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	names := make([]string, 0, len(atts))
	for _, att := range atts {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = filepath.Base(att.Path)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
