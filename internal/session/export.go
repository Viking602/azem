package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ExportSnapshot struct {
	Session      Session      `json:"session"`
	Tree         SessionTree  `json:"tree"`
	ActiveBlocks []Block      `json:"activeBlocks"`
	AllBlocks    []Block      `json:"allBlocks"`
	ToolRecords  []ToolRecord `json:"toolRecords,omitempty"`
}

// LoadExportSnapshot returns both the active root-to-leaf projection and the
// complete append-only transcript. Callers choose the active view for readable
// exports and retain AllBlocks for lossless JSON or archival exports.
func (s *Service) LoadExportSnapshot(ctx context.Context, sessionID string) (ExportSnapshot, error) {
	projection, err := s.LoadProjection(ctx, sessionID)
	if err != nil {
		return ExportSnapshot{}, err
	}
	tree, err := s.LoadSessionTree(ctx, sessionID)
	if err != nil {
		return ExportSnapshot{}, err
	}
	allBlocks, err := s.loadSessionBlocks(ctx, s.db, sessionID)
	if err != nil {
		return ExportSnapshot{}, err
	}
	workspace, err := s.SessionWorkspace(ctx, sessionID)
	if err != nil {
		return ExportSnapshot{}, err
	}
	projection.Session.Workspace = workspace
	return ExportSnapshot{
		Session: projection.Session, Tree: tree, ActiveBlocks: projection.Blocks,
		AllBlocks: allBlocks, ToolRecords: projection.ToolRecords,
	}, nil
}

func (s *Service) SessionWorkspace(ctx context.Context, sessionID string) (string, error) {
	var workspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace FROM session_workspaces WHERE session_id=?`, sessionID).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		var found string
		if sessionErr := s.db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=?`, sessionID).Scan(&found); sessionErr != nil {
			if errors.Is(sessionErr, sql.ErrNoRows) {
				return "", fmt.Errorf("session %q not found", sessionID)
			}
			return "", sessionErr
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load session workspace: %w", err)
	}
	return workspace, nil
}
