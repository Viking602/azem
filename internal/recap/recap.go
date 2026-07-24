// Package recap stores one bounded, independently revisioned recap per session.
package recap

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

const MaxFieldRunes = 8000

var recapSecrets = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|password|secret)\s*[:=]\s*[^\s,;]+`), `${1}=[REDACTED]`},
	{regexp.MustCompile(`\b(?:sk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{12,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), `[REDACTED PRIVATE KEY]`},
}

type Recap struct {
	SessionID, Anchor, CoveredBoundary, Goal, Summary, OpenItems string
	Revision                                                     int
	UpdatedAt                                                    time.Time
}
type Service struct {
	db     *sql.DB
	anchor string
}

func NewService(db *sql.DB, workspace string) *Service {
	a, e := filepath.Abs(workspace)
	if e == nil {
		workspace = a
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return &Service{db: db, anchor: filepath.Clean(workspace)}
}
func bounded(v string, limit int) string {
	v = strings.TrimSpace(v)
	r := []rune(v)
	if limit <= 0 || limit > MaxFieldRunes {
		limit = MaxFieldRunes
	}
	if len(r) > limit {
		return string(r[:limit])
	}
	return v
}

func safeField(v string, limit int) string {
	for _, secret := range recapSecrets {
		v = secret.pattern.ReplaceAllString(v, secret.replacement)
	}
	return bounded(v, limit)
}

func (s *Service) Upsert(ctx context.Context, r Recap) (Recap, error) {
	r.SessionID = bounded(r.SessionID, 512)
	if r.SessionID == "" {
		return Recap{}, fmt.Errorf("recap session id is empty")
	}
	r.Anchor = s.anchor
	r.Goal = safeField(r.Goal, 1200)
	r.Summary = safeField(r.Summary, 2400)
	r.OpenItems = safeField(r.OpenItems, 1600)
	r.CoveredBoundary = bounded(r.CoveredBoundary, 256)
	r.UpdatedAt = time.Now().UTC()
	revision, err := dbgen.New(s.db).UpsertRecap(ctx, dbgen.UpsertRecapParams{SessionID: r.SessionID, Anchor: r.Anchor, CoveredBoundary: r.CoveredBoundary, Goal: r.Goal, Summary: r.Summary, OpenItems: r.OpenItems, UpdatedAt: r.UpdatedAt.UnixMilli()})
	r.Revision = int(revision)
	if err == sql.ErrNoRows {
		return Recap{}, fmt.Errorf("recap session %q belongs to another workspace", r.SessionID)
	}
	return r, err
}
func (s *Service) Load(ctx context.Context, id string) (Recap, error) {
	row, err := dbgen.New(s.db).GetRecap(ctx, dbgen.GetRecapParams{SessionID: id, Anchor: s.anchor})
	r := Recap{SessionID: row.SessionID, Anchor: row.Anchor, CoveredBoundary: row.CoveredBoundary, Revision: int(row.Revision), Goal: row.Goal, Summary: row.Summary, OpenItems: row.OpenItems, UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC()}
	return r, err
}
