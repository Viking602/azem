package sessionimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type Source string

const (
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
)

type Info struct {
	Source       Source    `json:"source"`
	ID           string    `json:"id"`
	Path         string    `json:"path"`
	Workspace    string    `json:"workspace"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount,omitempty"`
	FirstMessage string    `json:"firstMessage,omitempty"`
}

type AttachmentImporter interface {
	ImportBytes(sessionID, name, mimeType string, data []byte) (session.Attachment, error)
}

type Importer struct {
	Sessions    *session.Service
	Attachments AttachmentImporter
}

func New(sessions *session.Service, attachments AttachmentImporter) *Importer {
	return &Importer{Sessions: sessions, Attachments: attachments}
}

func DefaultRoot(source Source) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch source {
	case SourceClaude:
		return filepath.Join(home, ".claude"), nil
	case SourceCodex:
		return filepath.Join(home, ".codex"), nil
	default:
		return "", fmt.Errorf("unsupported foreign session source %q", source)
	}
}

func (importer *Importer) Discover(ctx context.Context, source Source, root string) ([]Info, error) {
	if strings.TrimSpace(root) == "" {
		resolved, err := DefaultRoot(source)
		if err != nil {
			return nil, err
		}
		root = resolved
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	switch source {
	case SourceClaude:
		return discoverClaude(ctx, root)
	case SourceCodex:
		return discoverCodex(ctx, root)
	default:
		return nil, fmt.Errorf("unsupported foreign session source %q", source)
	}
}

func (importer *Importer) Import(ctx context.Context, info Info, targetSessionID, fallbackWorkspace string) (session.Session, error) {
	if importer == nil || importer.Sessions == nil {
		return session.Session{}, errors.New("foreign session importer is unavailable")
	}
	targetSessionID = strings.TrimSpace(targetSessionID)
	if targetSessionID == "" {
		return session.Session{}, errors.New("target session id is required")
	}
	var (
		snapshot session.SessionImport
		err      error
	)
	switch info.Source {
	case SourceClaude:
		snapshot, err = importer.parseClaude(ctx, info, targetSessionID, fallbackWorkspace)
	case SourceCodex:
		snapshot, err = importer.parseCodex(ctx, info, targetSessionID, fallbackWorkspace)
	default:
		err = fmt.Errorf("unsupported foreign session source %q", info.Source)
	}
	if err != nil {
		return session.Session{}, err
	}
	if err := importer.Sessions.ImportSession(ctx, snapshot); err != nil {
		return session.Session{}, fmt.Errorf("import %s session %s: %w", info.Source, info.ID, err)
	}
	return importer.Sessions.LoadSession(ctx, targetSessionID)
}

func importedWorkspace(source, fallback string) string {
	for _, candidate := range []string{source, fallback} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return ""
}
