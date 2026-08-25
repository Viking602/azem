package azem

import (
	"context"
	"errors"
	"time"

	"github.com/Viking602/azem/internal/sessionexport"
	"github.com/Viking602/azem/internal/sessionimport"
	"github.com/Viking602/azem/internal/sessionshare"
)

type ExportFormat string

const (
	ExportHTML ExportFormat = "html"
	ExportText ExportFormat = "text"
	ExportJSON ExportFormat = "json"
)

type ExportOptions struct {
	AllBranches  bool
	ExcludeTools bool
}

type ShareOptions struct {
	ServerURL        string
	Store            string
	AllBranches      bool
	DisableRedaction bool
	Secrets          []string
}

type ShareResult struct {
	URL         string
	Method      string
	GistURL     string
	Truncated   bool
	SealedBytes int
}

type ForeignSessionSource string

const (
	ForeignClaude ForeignSessionSource = "claude"
	ForeignCodex  ForeignSessionSource = "codex"
)

type ForeignSessionInfo struct {
	Source       ForeignSessionSource
	ID           string
	Path         string
	Workspace    string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	FirstMessage string
}

func (runtime *Runtime) ExportSession(ctx context.Context, sessionID, outputPath string, format ExportFormat, options ExportOptions) (string, error) {
	if err := runtime.ensureOpen(); err != nil {
		return "", err
	}
	value := sessionexport.Format(format)
	if value != sessionexport.FormatHTML && value != sessionexport.FormatText && value != sessionexport.FormatJSON {
		return "", errors.New("azem: unsupported export format")
	}
	return sessionexport.New(runtime.sessions).ExportFile(ctx, outputPath, sessionID, value, sessionexport.Options{AllBranches: options.AllBranches, ExcludeTools: options.ExcludeTools})
}

func (runtime *Runtime) ShareSession(ctx context.Context, sessionID string, options ShareOptions) (ShareResult, error) {
	if err := runtime.ensureOpen(); err != nil {
		return ShareResult{}, err
	}
	result, err := sessionshare.New(runtime.sessions).Share(ctx, sessionID, sessionshare.Options{
		ServerURL: options.ServerURL, Store: sessionshare.Store(options.Store), AllBranches: options.AllBranches,
		DisableRedaction: options.DisableRedaction, Secrets: append([]string(nil), options.Secrets...),
	})
	if err != nil {
		return ShareResult{}, err
	}
	return ShareResult{URL: result.URL, Method: result.Method, GistURL: result.GistURL, Truncated: result.Truncated, SealedBytes: result.SealedBytes}, nil
}

func (runtime *Runtime) DiscoverForeignSessions(ctx context.Context, source ForeignSessionSource, root string) ([]ForeignSessionInfo, error) {
	if err := runtime.ensureOpen(); err != nil {
		return nil, err
	}
	values, err := sessionimport.New(runtime.sessions, runtimeAttachmentImporter{runtime: runtime}).Discover(ctx, sessionimport.Source(source), root)
	if err != nil {
		return nil, err
	}
	result := make([]ForeignSessionInfo, len(values))
	for index, value := range values {
		result[index] = foreignSessionInfo(value)
	}
	return result, nil
}

func (runtime *Runtime) ImportForeignSession(ctx context.Context, info ForeignSessionInfo, targetSessionID, fallbackWorkspace string) (Session, error) {
	if err := runtime.ensureOpen(); err != nil {
		return Session{}, err
	}
	return sessionimport.New(runtime.sessions, runtimeAttachmentImporter{runtime: runtime}).Import(ctx, sessionimport.Info{
		Source: sessionimport.Source(info.Source), ID: info.ID, Path: info.Path, Workspace: info.Workspace, Title: info.Title,
		CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt, MessageCount: info.MessageCount, FirstMessage: info.FirstMessage,
	}, targetSessionID, fallbackWorkspace)
}

func (runtime *Runtime) ForkSession(ctx context.Context, sourceID, targetID string) error {
	if err := runtime.ensureOpen(); err != nil {
		return err
	}
	return runtime.sessions.Fork(ctx, sourceID, targetID)
}

func (runtime *Runtime) ForkSessionAt(ctx context.Context, sourceID, targetID, entryID string) error {
	if err := runtime.ensureOpen(); err != nil {
		return err
	}
	return runtime.sessions.ForkAt(ctx, sourceID, targetID, entryID)
}

func (runtime *Runtime) NavigateSession(ctx context.Context, sessionID, entryID string) error {
	if err := runtime.ensureOpen(); err != nil {
		return err
	}
	_, err := runtime.sessions.NavigateSessionTree(ctx, sessionID, entryID)
	return err
}

func (runtime *Runtime) LabelSessionEntry(ctx context.Context, sessionID, entryID, label string) error {
	if err := runtime.ensureOpen(); err != nil {
		return err
	}
	return runtime.sessions.SetSessionEntryLabel(ctx, sessionID, entryID, label)
}

func foreignSessionInfo(value sessionimport.Info) ForeignSessionInfo {
	return ForeignSessionInfo{
		Source: ForeignSessionSource(value.Source), ID: value.ID, Path: value.Path, Workspace: value.Workspace, Title: value.Title,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, MessageCount: value.MessageCount, FirstMessage: value.FirstMessage,
	}
}

type runtimeAttachmentImporter struct{ runtime *Runtime }

func (adapter runtimeAttachmentImporter) ImportBytes(sessionID, name, mimeType string, data []byte) (Attachment, error) {
	return adapter.runtime.service.ImportImageBytes(sessionID, name, mimeType, data)
}
