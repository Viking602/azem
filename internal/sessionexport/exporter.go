package sessionexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type Format string

const (
	FormatHTML Format = "html"
	FormatText Format = "text"
	FormatJSON Format = "json"
)

type Options struct {
	AllBranches  bool `json:"allBranches,omitempty"`
	ExcludeTools bool `json:"excludeTools,omitempty"`
}

type Document struct {
	Version    int                    `json:"version"`
	ExportedAt time.Time              `json:"exportedAt"`
	Snapshot   session.ExportSnapshot `json:"snapshot"`
}

type Exporter struct {
	Sessions *session.Service
	Now      func() time.Time
}

func New(sessions *session.Service) *Exporter {
	return &Exporter{Sessions: sessions, Now: func() time.Time { return time.Now().UTC() }}
}

func (exporter *Exporter) Write(ctx context.Context, output io.Writer, sessionID string, format Format, options Options) error {
	if exporter == nil || exporter.Sessions == nil {
		return errors.New("session exporter is unavailable")
	}
	if output == nil {
		return errors.New("session export output is required")
	}
	snapshot, err := exporter.Sessions.LoadExportSnapshot(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return err
	}
	if options.ExcludeTools {
		snapshot.ToolRecords = nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch format {
	case FormatJSON:
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		now := time.Now().UTC()
		if exporter.Now != nil {
			now = exporter.Now().UTC()
		}
		return encoder.Encode(Document{Version: 1, ExportedAt: now, Snapshot: snapshot})
	case FormatText:
		return writeText(ctx, output, snapshot, options)
	case FormatHTML:
		return writeHTML(ctx, output, snapshot, options)
	default:
		return fmt.Errorf("unsupported session export format %q", format)
	}
}

func (exporter *Exporter) ExportFile(ctx context.Context, outputPath, sessionID string, format Format, options Options) (string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return "", errors.New("session export path is required")
	}
	absolute, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("session export target must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	directory := filepath.Dir(absolute)
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		if err != nil {
			return "", err
		}
		return "", errors.New("session export parent is not a directory")
	}
	temporary, err := os.CreateTemp(directory, ".azem-session-export-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", err
	}
	if err := exporter.Write(ctx, temporary, sessionID, format, options); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, absolute); err != nil {
		return "", err
	}
	committed = true
	return absolute, nil
}

func exportedBlocks(snapshot session.ExportSnapshot, options Options) []session.Block {
	if options.AllBranches {
		return snapshot.AllBlocks
	}
	return snapshot.ActiveBlocks
}

func exportedTools(snapshot session.ExportSnapshot, options Options) []session.ToolRecord {
	if options.ExcludeTools {
		return nil
	}
	if options.AllBranches {
		return snapshot.ToolRecords
	}
	active := make(map[int64]struct{}, len(snapshot.ActiveBlocks))
	for _, block := range snapshot.ActiveBlocks {
		active[block.Sequence] = struct{}{}
	}
	tools := make([]session.ToolRecord, 0, len(snapshot.ToolRecords))
	for _, record := range snapshot.ToolRecords {
		if _, ok := active[record.AnchorSequence]; ok {
			tools = append(tools, record)
		}
	}
	return tools
}
