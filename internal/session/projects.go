package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Project struct {
	Workspace string    `json:"workspace"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Service) TouchProject(ctx context.Context, workspace string) error {
	workspace, err := canonicalProject(workspace)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO desktop_projects(workspace,updated_at) VALUES(?,?)
		ON CONFLICT(workspace) DO UPDATE SET updated_at=excluded.updated_at`, workspace, time.Now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("touch project: %w", err)
	}
	return nil
}

func (s *Service) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,updated_at FROM desktop_projects ORDER BY updated_at DESC,workspace`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var workspace string
		var updatedAt int64
		if err := rows.Scan(&workspace, &updatedAt); err != nil {
			return nil, err
		}
		canonical, err := canonicalProject(workspace)
		if err != nil {
			continue
		}
		projects = append(projects, Project{Workspace: canonical, UpdatedAt: time.Unix(0, updatedAt).UTC()})
	}
	return projects, rows.Err()
}

func (s *Service) LastProject(ctx context.Context) (string, error) {
	projects, err := s.Projects(ctx)
	if err != nil {
		return "", err
	}
	if len(projects) == 0 {
		return "", sql.ErrNoRows
	}
	return projects[0].Workspace, nil
}

// AdoptUnassignedSessions preserves pre-catalog history when exactly one real
// project is known. With multiple projects there is no honest way to infer the
// old ownership, so those sessions remain visible as legacy history.
func (s *Service) AdoptUnassignedSessions(ctx context.Context) error {
	projects, err := s.Projects(ctx)
	if err != nil || len(projects) != 1 {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO session_workspaces(session_id,workspace,assigned_at)
		SELECT s.id,?,s.updated_at FROM sessions s
		WHERE NOT EXISTS(SELECT 1 FROM session_workspaces sw WHERE sw.session_id=s.id)`, projects[0].Workspace)
	if err != nil {
		return fmt.Errorf("adopt legacy sessions: %w", err)
	}
	return nil
}

func (s *Service) SetWorkspaceSession(ctx context.Context, workspace, sessionID string) error {
	workspace, err := projectKey(workspace)
	if err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set workspace session: %w", err)
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT workspace FROM session_workspaces WHERE session_id=?`, sessionID).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read session project: %w", err)
	}
	if existing != "" && existing != workspace {
		return fmt.Errorf("session %q belongs to project %q", sessionID, existing)
	}
	now := time.Now().UTC().UnixNano()
	if err := persistWorkspaceSession(ctx, tx, workspace, sessionID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace session: %w", err)
	}
	return nil
}

func persistWorkspaceSession(ctx context.Context, tx *sql.Tx, workspace, sessionID string, now int64) error {
	writes := []struct {
		name  string
		query string
		args  []any
	}{
		{"remember project", `INSERT INTO desktop_projects(workspace,updated_at) VALUES(?,?)
			ON CONFLICT(workspace) DO UPDATE SET updated_at=excluded.updated_at`, []any{workspace, now}},
		{"assign session project", `INSERT INTO session_workspaces(session_id,workspace,assigned_at) VALUES(?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET assigned_at=excluded.assigned_at`, []any{sessionID, workspace, now}},
		{"remember workspace session", `INSERT INTO workspace_session_state(anchor,session_id,updated_at) VALUES(?,?,?)
			ON CONFLICT(anchor) DO UPDATE SET session_id=excluded.session_id,updated_at=excluded.updated_at`, []any{workspace, sessionID, now}},
	}
	for _, write := range writes {
		if _, err := tx.ExecContext(ctx, write.query, write.args...); err != nil {
			return fmt.Errorf("%s: %w", write.name, err)
		}
	}
	return nil
}

func (s *Service) sessionWorkspaces(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id,workspace FROM session_workspaces`)
	if err != nil {
		return nil, fmt.Errorf("list session projects: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var sessionID, workspace string
		if err := rows.Scan(&sessionID, &workspace); err != nil {
			return nil, err
		}
		values[sessionID] = workspace
	}
	return values, rows.Err()
}

func canonicalProject(workspace string) (string, error) {
	resolved, err := projectKey(workspace)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("open project path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory")
	}
	return resolved, nil
}

func projectKey(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", fmt.Errorf("project path is empty")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	resolved := absolute
	if existing, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		resolved = existing
	}
	resolved = filepath.Clean(resolved)
	if filepath.Dir(resolved) == resolved {
		return "", fmt.Errorf("project path must be a non-root directory")
	}
	return resolved, nil
}
