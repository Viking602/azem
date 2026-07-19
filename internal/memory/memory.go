// Package memory provides the workspace-scoped, evidence-first long-term memory store.
package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const MaxContentRunes = 8000

type Memory struct {
	ID, Content, Anchor, SessionID, Provenance, Status string
	Importance                                         int
	CreatedAt, UpdatedAt                               time.Time
}

type Service struct {
	db     *sql.DB
	anchor string
}

func NewService(db *sql.DB, workspace string) *Service {
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return &Service{db: db, anchor: filepath.Clean(workspace)}
}

func (s *Service) Remember(ctx context.Context, content, sessionID, provenance string, importance int) (Memory, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Memory{}, fmt.Errorf("memory content is empty")
	}
	if len([]rune(content)) > MaxContentRunes {
		return Memory{}, fmt.Errorf("memory content exceeds %d characters", MaxContentRunes)
	}
	if provenance == "" {
		provenance = "manual"
	}
	if provenance != "manual" && provenance != "runtime" {
		return Memory{}, fmt.Errorf("invalid memory provenance %q", provenance)
	}
	if importance < 0 || importance > 100 {
		return Memory{}, fmt.Errorf("importance must be between 0 and 100")
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return Memory{}, err
	}
	now := time.Now().UTC()
	m := Memory{ID: "mem_" + hex.EncodeToString(b), Content: content, Anchor: s.anchor, SessionID: sessionID, Provenance: provenance, Status: "active", Importance: importance, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO memories(id,content,anchor,session_id,provenance,status,importance,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, m.Content, m.Anchor, m.SessionID, m.Provenance, m.Status, m.Importance, now.UnixMilli(), now.UnixMilli())
	return m, err
}

func (s *Service) Forget(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE memories SET status='forgotten',updated_at=? WHERE id=? AND anchor=? AND status='active'`, time.Now().UTC().UnixMilli(), strings.TrimSpace(id), s.anchor)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) List(ctx context.Context, query string, limit int) ([]Memory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	if query != "" {
		match := ftsQuery(query)
		rows, err := s.db.QueryContext(ctx, `SELECT m.id,m.content,m.anchor,m.session_id,m.provenance,m.status,m.importance,m.created_at,m.updated_at FROM memories_fts f JOIN memories m ON m.memory_rowid=f.rowid WHERE memories_fts MATCH ? AND m.anchor=? AND m.status='active' ORDER BY bm25(memories_fts),m.importance DESC LIMIT ?`, match, s.anchor, limit)
		if err == nil {
			defer rows.Close()
			return scan(rows)
		}
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		like := "%" + escaped + "%"
		return s.selectRecent(ctx, ` AND content LIKE ? ESCAPE '\'`, limit, like)
	}
	return s.selectRecent(ctx, "", limit)
}

func ftsQuery(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 12 {
		terms = terms[:12]
	}
	quoted := terms[:0]
	for _, term := range terms {
		term = strings.Trim(term, `"'()[]{}:,*`)
		if term == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " OR ")
}

func (s *Service) selectRecent(ctx context.Context, extra string, limit int, args ...any) ([]Memory, error) {
	params := []any{s.anchor}
	params = append(params, args...)
	params = append(params, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,content,anchor,session_id,provenance,status,importance,created_at,updated_at FROM memories WHERE anchor=? AND status='active'`+extra+` ORDER BY importance DESC,updated_at DESC LIMIT ?`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}
func scan(rows *sql.Rows) ([]Memory, error) {
	var out []Memory
	for rows.Next() {
		var m Memory
		var c, u int64
		if err := rows.Scan(&m.ID, &m.Content, &m.Anchor, &m.SessionID, &m.Provenance, &m.Status, &m.Importance, &c, &u); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(c).UTC()
		m.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (m Memory) Citation() string {
	return fmt.Sprintf("[%s | %s | session:%s | %s]", m.ID, m.Provenance, m.SessionID, m.UpdatedAt.Format(time.RFC3339))
}
