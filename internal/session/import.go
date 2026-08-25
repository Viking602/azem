package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Viking602/venat/message"
)

const (
	ImportSourceClaude = "claude"
	ImportSourceCodex  = "codex"
)

type ImportEntry struct {
	SourceID       string    `json:"sourceId"`
	ParentSourceID string    `json:"parentSourceId,omitempty"`
	Block          Block     `json:"block"`
	CreatedAt      time.Time `json:"createdAt"`
}

type SessionImport struct {
	Session        Session       `json:"session"`
	SourceKind     string        `json:"sourceKind"`
	SourceRef      string        `json:"sourceRef"`
	Workspace      string        `json:"workspace,omitempty"`
	Entries        []ImportEntry `json:"entries"`
	ActiveSourceID string        `json:"activeSourceId,omitempty"`
}

// ImportSession installs a parsed foreign session atomically. Source records are
// immutable input: the import receives a new Azem identity and stores provenance
// without retaining authority over the source file.
func (s *Service) ImportSession(ctx context.Context, snapshot SessionImport) (err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	if err := validateSessionImport(snapshot); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return err
	}
	createdAt := snapshot.Session.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := snapshot.Session.UpdatedAt.UTC()
	if updatedAt.IsZero() || updatedAt.Before(createdAt) {
		updatedAt = createdAt
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, snapshot.Session.ID, snapshot.Session.Title, snapshot.Session.ProviderID, snapshot.Session.ModelID,
		snapshot.Session.Reasoning, firstSessionValue(snapshot.Session.AgentMode, "single"), createdAt.UnixNano(), updatedAt.UnixNano()); err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("session %q already exists", snapshot.Session.ID)
		}
		return fmt.Errorf("create imported session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_projections(session_id,last_run_id,updated_at,model_history,usage,checkpoint_generation,cache_epoch,cache_identity_hash,model_history_sha256)
		VALUES(?,'',?,'{}','{}',0,0,'','')
	`, snapshot.Session.ID, updatedAt.UnixNano()); err != nil {
		return fmt.Errorf("create imported session projection: %w", err)
	}
	if snapshot.Workspace != "" {
		workspace := filepath.Clean(snapshot.Workspace)
		if !filepath.IsAbs(workspace) {
			return errors.New("session: imported workspace must be absolute")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO desktop_projects(workspace,updated_at) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET updated_at=MAX(updated_at,excluded.updated_at)`, workspace, updatedAt.UnixNano()); err != nil {
			return fmt.Errorf("catalog imported workspace: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_workspaces(session_id,workspace,assigned_at) VALUES(?,?,?)`, snapshot.Session.ID, workspace, updatedAt.UnixNano()); err != nil {
			return fmt.Errorf("assign imported workspace: %w", err)
		}
	}
	entryIDs := make(map[string]string, len(snapshot.Entries))
	for index, imported := range snapshot.Entries {
		if len(imported.Block.ImportedMessage) > 0 {
			var modelMessage message.Message
			if err := json.Unmarshal(imported.Block.ImportedMessage, &modelMessage); err != nil || !modelMessage.HasContent() {
				return fmt.Errorf("session: imported entry %q has invalid model message", imported.SourceID)
			}
		}
		encoded, err := json.Marshal(imported.Block)
		if err != nil {
			return fmt.Errorf("encode imported entry %q: %w", imported.SourceID, err)
		}
		inline, digest, err := s.encodeBlockData(ctx, imported.Block, encoded)
		if err != nil {
			return fmt.Errorf("store imported entry %q: %w", imported.SourceID, err)
		}
		sequence := int64(index)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data,data_sha256)
			VALUES(?,?,?,?,?,?,?)
		`, snapshot.Session.ID, sequence, imported.Block.Kind, imported.Block.RunID, imported.Block.AgentID, inline, digest); err != nil {
			return fmt.Errorf("insert imported entry %q: %w", imported.SourceID, err)
		}
		entryID := fmt.Sprintf("%s:%020d", snapshot.Session.ID, sequence)
		entryIDs[imported.SourceID] = entryID
		entryTime := imported.CreatedAt.UTC()
		if entryTime.IsZero() {
			entryTime = createdAt.Add(time.Duration(index) * time.Nanosecond)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_graph_entries SET source_entry_id=?,created_at=?
			WHERE session_id=? AND entry_id=?
		`, imported.SourceID, entryTime.UnixNano(), snapshot.Session.ID, entryID); err != nil {
			return fmt.Errorf("index imported entry %q: %w", imported.SourceID, err)
		}
	}
	for _, imported := range snapshot.Entries {
		entryID := entryIDs[imported.SourceID]
		parentID := entryIDs[imported.ParentSourceID]
		if _, err := tx.ExecContext(ctx, `UPDATE session_graph_entries SET parent_entry_id=NULLIF(?,'') WHERE session_id=? AND entry_id=?`, parentID, snapshot.Session.ID, entryID); err != nil {
			return fmt.Errorf("link imported entry %q: %w", imported.SourceID, err)
		}
	}
	activeSourceID := snapshot.ActiveSourceID
	if activeSourceID == "" {
		activeSourceID = snapshot.Entries[len(snapshot.Entries)-1].SourceID
	}
	activeEntryID := entryIDs[activeSourceID]
	if activeEntryID == "" {
		return fmt.Errorf("session: imported active entry %q is missing", activeSourceID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_graphs
		SET root_session_id=?,parent_session_id=NULL,forked_from_entry_id='',prompt_cache_key=?,
			active_branch='main',active_leaf_entry_id=?,source_kind=?,source_ref=?,updated_at=?
		WHERE session_id=?
	`, snapshot.Session.ID, snapshot.Session.ID, activeEntryID, snapshot.SourceKind, snapshot.SourceRef, updatedAt.UnixNano(), snapshot.Session.ID); err != nil {
		return fmt.Errorf("activate imported session graph: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_branches SET head_entry_id=?,updated_at=? WHERE session_id=? AND name='main'`, activeEntryID, updatedAt.UnixNano(), snapshot.Session.ID); err != nil {
		return fmt.Errorf("activate imported session branch: %w", err)
	}
	return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
}

func validateSessionImport(snapshot SessionImport) error {
	sessionID := strings.TrimSpace(snapshot.Session.ID)
	if sessionID == "" || sessionID != snapshot.Session.ID || utf8.RuneCountInString(sessionID) > 200 {
		return errors.New("session: imported session id is required, must be trimmed, and cannot exceed 200 characters")
	}
	for _, r := range sessionID {
		if unicode.IsControl(r) {
			return errors.New("session: imported session id contains a control character")
		}
	}
	if snapshot.SourceKind != ImportSourceClaude && snapshot.SourceKind != ImportSourceCodex {
		return fmt.Errorf("session: unsupported import source %q", snapshot.SourceKind)
	}
	if strings.TrimSpace(snapshot.SourceRef) == "" {
		return errors.New("session: imported source reference is required")
	}
	if len(snapshot.Entries) == 0 {
		return errors.New("session: imported transcript contains no supported entries")
	}
	if _, err := normalizeSessionTitle(snapshot.Session.Title); err != nil {
		return fmt.Errorf("session: imported title: %w", err)
	}
	activeSourceID := strings.TrimSpace(snapshot.ActiveSourceID)
	if activeSourceID != snapshot.ActiveSourceID {
		return errors.New("session: imported active entry id must be trimmed")
	}
	seen := make(map[string]struct{}, len(snapshot.Entries))
	parentByID := make(map[string]string, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		sourceID := strings.TrimSpace(entry.SourceID)
		if sourceID == "" || sourceID != entry.SourceID || utf8.RuneCountInString(sourceID) > 300 {
			return errors.New("session: imported entry id is required, must be trimmed, and cannot exceed 300 characters")
		}
		if _, duplicate := seen[sourceID]; duplicate {
			return fmt.Errorf("session: duplicate imported entry id %q", sourceID)
		}
		parentSourceID := strings.TrimSpace(entry.ParentSourceID)
		if parentSourceID != entry.ParentSourceID {
			return fmt.Errorf("session: imported parent entry id for %q must be trimmed", sourceID)
		}
		parentByID[sourceID] = parentSourceID
		if strings.TrimSpace(entry.Block.Kind) == "" {
			return fmt.Errorf("session: imported entry %q has no kind", sourceID)
		}
	}
	state := make(map[string]uint8, len(parentByID))
	var visit func(string) error
	visit = func(id string) error {
		if id == "" {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("session: imported transcript contains a cycle at %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		parent, ok := parentByID[id]
		if !ok {
			return nil
		}
		state[id] = 1
		if err := visit(parent); err != nil {
			return err
		}
		state[id] = 2
		return nil
	}
	for id := range parentByID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
