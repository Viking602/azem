package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

var (
	ErrSessionGraphEntryNotFound = errors.New("session: graph entry not found")
	ErrSessionBranchNotFound     = errors.New("session: branch not found")
	ErrSessionBranchExists       = errors.New("session: branch already exists")
)

type GraphEntry struct {
	ID        string    `json:"id"`
	SourceID  string    `json:"sourceId,omitempty"`
	ParentID  string    `json:"parentId,omitempty"`
	Sequence  int64     `json:"sequence"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type TreeNode struct {
	Entry    GraphEntry `json:"entry"`
	Children []TreeNode `json:"children,omitempty"`
}

type SessionBranch struct {
	Name        string    `json:"name"`
	HeadEntryID string    `json:"headEntryId,omitempty"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type SessionLabel struct {
	EntryID   string    `json:"entryId"`
	Label     string    `json:"label"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SessionTree struct {
	SessionID         string          `json:"sessionId"`
	RootSessionID     string          `json:"rootSessionId"`
	ParentSessionID   string          `json:"parentSessionId,omitempty"`
	ForkedFromEntryID string          `json:"forkedFromEntryId,omitempty"`
	PromptCacheKey    string          `json:"promptCacheKey,omitempty"`
	SourceKind        string          `json:"sourceKind"`
	SourceRef         string          `json:"sourceRef,omitempty"`
	ActiveBranch      string          `json:"activeBranch"`
	ActiveLeafEntryID string          `json:"activeLeafEntryId,omitempty"`
	Roots             []TreeNode      `json:"roots"`
	Branches          []SessionBranch `json:"branches"`
}

type SessionNavigation struct {
	SessionID     string `json:"sessionId"`
	Branch        string `json:"branch"`
	OldLeaf       string `json:"oldLeaf,omitempty"`
	NewLeaf       string `json:"newLeaf,omitempty"`
	ContextChange bool   `json:"contextChange"`
}

func (s *Service) LoadSessionTree(ctx context.Context, sessionID string) (SessionTree, error) {
	var tree SessionTree
	var parent sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT session_id,root_session_id,parent_session_id,forked_from_entry_id,prompt_cache_key,
			source_kind,source_ref,active_branch,active_leaf_entry_id
		FROM session_graphs WHERE session_id=?
	`, sessionID).Scan(&tree.SessionID, &tree.RootSessionID, &parent, &tree.ForkedFromEntryID, &tree.PromptCacheKey, &tree.SourceKind, &tree.SourceRef, &tree.ActiveBranch, &tree.ActiveLeafEntryID); err != nil {
		return SessionTree{}, fmt.Errorf("load session graph: %w", err)
	}
	if parent.Valid {
		tree.ParentSessionID = parent.String
	}
	entries, err := loadGraphEntries(ctx, s.db, sessionID)
	if err != nil {
		return SessionTree{}, err
	}
	tree.Roots, err = buildSessionTree(entries)
	if err != nil {
		return SessionTree{}, err
	}
	tree.Branches, err = loadSessionBranches(ctx, s.db, sessionID, tree.ActiveBranch)
	if err != nil {
		return SessionTree{}, err
	}
	return tree, nil
}

func (s *Service) LoadSessionBranch(ctx context.Context, sessionID, leafEntryID string) ([]GraphEntry, error) {
	if leafEntryID == "" {
		if err := s.db.QueryRowContext(ctx, `SELECT active_leaf_entry_id FROM session_graphs WHERE session_id=?`, sessionID).Scan(&leafEntryID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("session %q not found", sessionID)
			}
			return nil, fmt.Errorf("load active session leaf: %w", err)
		}
	}
	if leafEntryID == "" {
		return []GraphEntry{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE branch(entry_id,source_entry_id,parent_entry_id,block_sequence,kind,created_at,depth) AS (
			SELECT entry_id,source_entry_id,parent_entry_id,block_sequence,kind,created_at,0
			FROM session_graph_entries WHERE session_id=? AND entry_id=?
			UNION ALL
			SELECT entry.entry_id,entry.source_entry_id,entry.parent_entry_id,entry.block_sequence,entry.kind,entry.created_at,branch.depth+1
			FROM session_graph_entries entry JOIN branch ON entry.entry_id=branch.parent_entry_id
			WHERE entry.session_id=?
		)
		SELECT branch.entry_id,branch.source_entry_id,COALESCE(branch.parent_entry_id,''),branch.block_sequence,branch.kind,COALESCE(label.label,''),branch.created_at
		FROM branch LEFT JOIN session_labels label ON label.session_id=? AND label.entry_id=branch.entry_id ORDER BY branch.depth DESC
	`, sessionID, leafEntryID, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session branch: %w", err)
	}
	defer rows.Close()
	entries := make([]GraphEntry, 0)
	for rows.Next() {
		var entry GraphEntry
		var createdAt int64
		if err := rows.Scan(&entry.ID, &entry.SourceID, &entry.ParentID, &entry.Sequence, &entry.Kind, &entry.Label, &createdAt); err != nil {
			return nil, fmt.Errorf("scan session branch: %w", err)
		}
		entry.CreatedAt = time.Unix(0, createdAt).UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load session branch: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrSessionGraphEntryNotFound, leafEntryID)
	}
	return entries, nil
}

func (s *Service) ListSessionBranches(ctx context.Context, sessionID string) ([]SessionBranch, error) {
	var active string
	if err := s.db.QueryRowContext(ctx, `SELECT active_branch FROM session_graphs WHERE session_id=?`, sessionID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session %q not found", sessionID)
		}
		return nil, fmt.Errorf("load active session branch: %w", err)
	}
	return loadSessionBranches(ctx, s.db, sessionID, active)
}

func (s *Service) SetSessionEntryLabel(ctx context.Context, sessionID, entryID, label string) error {
	if err := requireGraphEntry(ctx, s.db, sessionID, entryID); err != nil {
		return err
	}
	label, err := normalizeEntryLabel(label)
	if err != nil {
		return err
	}
	if label == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM session_labels WHERE session_id=? AND entry_id=?`, sessionID, entryID); err != nil {
			return fmt.Errorf("delete session entry label: %w", err)
		}
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO session_labels(session_id,entry_id,label,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(session_id,entry_id) DO UPDATE SET label=excluded.label,updated_at=excluded.updated_at
	`, sessionID, entryID, label, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("set session entry label: %w", err)
	}
	return nil
}

func (s *Service) ListSessionLabels(ctx context.Context, sessionID string) ([]SessionLabel, error) {
	if _, _, err := loadActiveSessionPosition(ctx, s.db, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entry_id,label,updated_at FROM session_labels WHERE session_id=? ORDER BY updated_at,entry_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session labels: %w", err)
	}
	defer rows.Close()
	labels := make([]SessionLabel, 0)
	for rows.Next() {
		var label SessionLabel
		var updatedAt int64
		if err := rows.Scan(&label.EntryID, &label.Label, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan session label: %w", err)
		}
		label.UpdatedAt = time.Unix(0, updatedAt).UTC()
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load session labels: %w", err)
	}
	return labels, nil
}

func (s *Service) NavigateSessionTree(ctx context.Context, sessionID, targetEntryID string) (SessionNavigation, error) {
	return s.setActiveSessionPosition(ctx, sessionID, targetEntryID)
}

func (s *Service) CreateSessionBranch(ctx context.Context, sessionID, name, fromEntryID string) (SessionNavigation, error) {
	name, err := normalizeBranchName(name)
	if err != nil {
		return SessionNavigation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionNavigation{}, err
	}
	defer tx.Rollback()
	_, oldLeaf, err := loadActiveSessionPosition(ctx, tx, sessionID)
	if err != nil {
		return SessionNavigation{}, err
	}
	if fromEntryID == "" {
		fromEntryID = oldLeaf
	}
	if err := requireGraphEntry(ctx, tx, sessionID, fromEntryID); err != nil {
		return SessionNavigation{}, err
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at) VALUES(?,?,?,?,?)`, sessionID, name, fromEntryID, now, now); err != nil {
		if isUniqueConstraint(err) {
			return SessionNavigation{}, fmt.Errorf("%w: %s", ErrSessionBranchExists, name)
		}
		return SessionNavigation{}, fmt.Errorf("create session branch: %w", err)
	}
	if err := updateActiveSessionPosition(ctx, tx, sessionID, name, oldLeaf, fromEntryID, now); err != nil {
		return SessionNavigation{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionNavigation{}, err
	}
	return SessionNavigation{SessionID: sessionID, Branch: name, OldLeaf: oldLeaf, NewLeaf: fromEntryID, ContextChange: oldLeaf != fromEntryID}, nil
}

func (s *Service) SwitchSessionBranch(ctx context.Context, sessionID, name string) (SessionNavigation, error) {
	name, err := normalizeBranchName(name)
	if err != nil {
		return SessionNavigation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionNavigation{}, err
	}
	defer tx.Rollback()
	_, oldLeaf, err := loadActiveSessionPosition(ctx, tx, sessionID)
	if err != nil {
		return SessionNavigation{}, err
	}
	var storedName, head string
	if err := tx.QueryRowContext(ctx, `SELECT name,head_entry_id FROM session_branches WHERE session_id=? AND name=?`, sessionID, name).Scan(&storedName, &head); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionNavigation{}, fmt.Errorf("%w: %s", ErrSessionBranchNotFound, name)
		}
		return SessionNavigation{}, fmt.Errorf("load session branch: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	if err := updateActiveSessionPosition(ctx, tx, sessionID, storedName, oldLeaf, head, now); err != nil {
		return SessionNavigation{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionNavigation{}, err
	}
	return SessionNavigation{SessionID: sessionID, Branch: storedName, OldLeaf: oldLeaf, NewLeaf: head, ContextChange: oldLeaf != head}, nil
}

func (s *Service) RenameSessionBranch(ctx context.Context, sessionID, currentName, nextName string) error {
	currentName, err := normalizeBranchName(currentName)
	if err != nil {
		return err
	}
	nextName, err = normalizeBranchName(nextName)
	if err != nil {
		return err
	}
	if strings.EqualFold(currentName, "main") {
		return errors.New("session: main branch cannot be renamed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE session_branches SET name=?,updated_at=? WHERE session_id=? AND name=?`, nextName, time.Now().UTC().UnixNano(), sessionID, currentName)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: %s", ErrSessionBranchExists, nextName)
		}
		return fmt.Errorf("rename session branch: %w", err)
	}
	if err := requireOneSessionBranch(result, currentName); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_graphs SET active_branch=? WHERE session_id=? AND active_branch=?`, nextName, sessionID, currentName); err != nil {
		return fmt.Errorf("rename active session branch: %w", err)
	}
	return tx.Commit()
}

func (s *Service) DeleteSessionBranch(ctx context.Context, sessionID, name string) error {
	name, err := normalizeBranchName(name)
	if err != nil {
		return err
	}
	if strings.EqualFold(name, "main") {
		return errors.New("session: main branch cannot be deleted")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active string
	if err := tx.QueryRowContext(ctx, `SELECT active_branch FROM session_graphs WHERE session_id=?`, sessionID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %q not found", sessionID)
		}
		return err
	}
	if strings.EqualFold(active, name) {
		return errors.New("session: active branch cannot be deleted")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM session_branches WHERE session_id=? AND name=?`, sessionID, name)
	if err != nil {
		return fmt.Errorf("delete session branch: %w", err)
	}
	if err := requireOneSessionBranch(result, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) setActiveSessionPosition(ctx context.Context, sessionID, targetEntryID string) (SessionNavigation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionNavigation{}, err
	}
	defer tx.Rollback()
	branch, oldLeaf, err := loadActiveSessionPosition(ctx, tx, sessionID)
	if err != nil {
		return SessionNavigation{}, err
	}
	if err := requireGraphEntry(ctx, tx, sessionID, targetEntryID); err != nil {
		return SessionNavigation{}, err
	}
	now := time.Now().UTC().UnixNano()
	if err := updateActiveSessionPosition(ctx, tx, sessionID, branch, oldLeaf, targetEntryID, now); err != nil {
		return SessionNavigation{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionNavigation{}, err
	}
	return SessionNavigation{SessionID: sessionID, Branch: branch, OldLeaf: oldLeaf, NewLeaf: targetEntryID, ContextChange: oldLeaf != targetEntryID}, nil
}

func loadActiveSessionPosition(ctx context.Context, queryer dbgen.DBTX, sessionID string) (branch, leaf string, err error) {
	err = queryer.QueryRowContext(ctx, `SELECT active_branch,active_leaf_entry_id FROM session_graphs WHERE session_id=?`, sessionID).Scan(&branch, &leaf)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("session %q not found", sessionID)
	}
	if err != nil {
		return "", "", fmt.Errorf("load active session position: %w", err)
	}
	return branch, leaf, nil
}

func updateActiveSessionPosition(ctx context.Context, tx *sql.Tx, sessionID, branch, oldLeaf, nextLeaf string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE session_graphs SET active_branch=?,active_leaf_entry_id=?,updated_at=? WHERE session_id=?`, branch, nextLeaf, now, sessionID); err != nil {
		return fmt.Errorf("update active session position: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_branches SET head_entry_id=?,updated_at=? WHERE session_id=? AND name=?`, nextLeaf, now, sessionID, branch); err != nil {
		return fmt.Errorf("update active session branch: %w", err)
	}
	if oldLeaf != nextLeaf {
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_projections
			SET last_run_id='',model_history='{}',model_history_sha256='',checkpoint_generation=checkpoint_generation+1,
				cache_epoch=cache_epoch+1,cache_identity_hash='',updated_at=?
			WHERE session_id=?
		`, now, sessionID); err != nil {
			return fmt.Errorf("invalidate session branch checkpoint: %w", err)
		}
	}
	return nil
}

func configureForkGraph(ctx context.Context, tx *sql.Tx, sourceID, targetID, selectedEntryID string, pathOnly bool, now int64) error {
	var rootSessionID, sourceLeaf, sourceBranch, promptCacheKey string
	if err := tx.QueryRowContext(ctx, `
		SELECT root_session_id,active_leaf_entry_id,active_branch,prompt_cache_key
		FROM session_graphs WHERE session_id=?
	`, sourceID).Scan(&rootSessionID, &sourceLeaf, &sourceBranch, &promptCacheKey); err != nil {
		return fmt.Errorf("load fork source graph: %w", err)
	}
	if rootSessionID == "" {
		rootSessionID = sourceID
	}
	if strings.TrimSpace(promptCacheKey) == "" {
		promptCacheKey = sourceID
	}
	forkedFromEntryID := sourceLeaf
	if pathOnly {
		forkedFromEntryID = selectedEntryID
		sourceBranch = "main"
	}
	targetLeaf, err := mappedForkEntryID(ctx, tx, sourceID, targetID, forkedFromEntryID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_graph_entries
		SET source_entry_id=COALESCE((
				SELECT source.source_entry_id FROM session_graph_entries source
				WHERE source.session_id=? AND source.block_sequence=session_graph_entries.block_sequence
			),''),
			parent_entry_id=(
				SELECT CASE WHEN source.parent_entry_id IS NULL THEN NULL
					ELSE ? || ':' || printf('%020d',parent.block_sequence) END
				FROM session_graph_entries source
				LEFT JOIN session_graph_entries parent
					ON parent.session_id=source.session_id AND parent.entry_id=source.parent_entry_id
				WHERE source.session_id=? AND source.block_sequence=session_graph_entries.block_sequence
			)
		WHERE session_id=?
	`, sourceID, targetID, sourceID, targetID); err != nil {
		return fmt.Errorf("rewire fork session graph: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_graphs
		SET root_session_id=?,parent_session_id=?,forked_from_entry_id=?,prompt_cache_key=?,
			active_branch=?,active_leaf_entry_id=?,updated_at=?
		WHERE session_id=?
	`, rootSessionID, sourceID, forkedFromEntryID, promptCacheKey, sourceBranch, targetLeaf, now, targetID); err != nil {
		return fmt.Errorf("update fork session graph: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_branches WHERE session_id=?`, targetID); err != nil {
		return fmt.Errorf("replace fork session branches: %w", err)
	}
	if pathOnly {
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at) VALUES(?,'main',?,?,?)`, targetID, targetLeaf, now, now); err != nil {
			return fmt.Errorf("create fork main branch: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at)
			SELECT ?,branch.name,
				CASE WHEN branch.head_entry_id='' THEN '' ELSE ? || ':' || printf('%020d',entry.block_sequence) END,
				branch.created_at,branch.updated_at
			FROM session_branches branch
			LEFT JOIN session_graph_entries entry
				ON entry.session_id=? AND entry.entry_id=branch.head_entry_id
			WHERE branch.session_id=? AND (branch.head_entry_id='' OR entry.entry_id IS NOT NULL)
		`, targetID, targetID, sourceID, sourceID); err != nil {
			return fmt.Errorf("copy fork session branches: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_labels(session_id,entry_id,label,updated_at)
		SELECT ?,? || ':' || printf('%020d',source_entry.block_sequence),label.label,label.updated_at
		FROM session_labels label
		JOIN session_graph_entries source_entry
			ON source_entry.session_id=label.session_id AND source_entry.entry_id=label.entry_id
		JOIN session_graph_entries target_entry
			ON target_entry.session_id=? AND target_entry.block_sequence=source_entry.block_sequence
		WHERE label.session_id=?
	`, targetID, targetID, targetID, sourceID); err != nil {
		return fmt.Errorf("copy fork session labels: %w", err)
	}
	return nil
}

func mappedForkEntryID(ctx context.Context, queryer dbgen.DBTX, sourceID, targetID, sourceEntryID string) (string, error) {
	if sourceEntryID == "" {
		return "", nil
	}
	var sequence int64
	if err := queryer.QueryRowContext(ctx, `SELECT block_sequence FROM session_graph_entries WHERE session_id=? AND entry_id=?`, sourceID, sourceEntryID).Scan(&sequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrSessionGraphEntryNotFound, sourceEntryID)
		}
		return "", fmt.Errorf("map fork session entry: %w", err)
	}
	return fmt.Sprintf("%s:%020d", targetID, sequence), nil
}

func (s *Service) PromptCacheKey(ctx context.Context, sessionID string) (string, error) {
	var key string
	if err := s.db.QueryRowContext(ctx, `SELECT prompt_cache_key FROM session_graphs WHERE session_id=?`, sessionID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("session %q not found", sessionID)
		}
		return "", fmt.Errorf("load session prompt cache key: %w", err)
	}
	if strings.TrimSpace(key) == "" {
		return sessionID, nil
	}
	return key, nil
}

func requireGraphEntry(ctx context.Context, queryer dbgen.DBTX, sessionID, entryID string) error {
	if entryID == "" {
		return nil
	}
	var found int
	if err := queryer.QueryRowContext(ctx, `SELECT 1 FROM session_graph_entries WHERE session_id=? AND entry_id=?`, sessionID, entryID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrSessionGraphEntryNotFound, entryID)
		}
		return fmt.Errorf("load session graph entry: %w", err)
	}
	return nil
}

func loadGraphEntries(ctx context.Context, queryer dbgen.DBTX, sessionID string) ([]GraphEntry, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT entry.entry_id,entry.source_entry_id,COALESCE(entry.parent_entry_id,''),entry.block_sequence,entry.kind,COALESCE(label.label,''),entry.created_at FROM session_graph_entries entry LEFT JOIN session_labels label ON label.session_id=entry.session_id AND label.entry_id=entry.entry_id WHERE entry.session_id=? ORDER BY entry.block_sequence,entry.entry_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session graph entries: %w", err)
	}
	defer rows.Close()
	entries := make([]GraphEntry, 0)
	for rows.Next() {
		var entry GraphEntry
		var createdAt int64
		if err := rows.Scan(&entry.ID, &entry.SourceID, &entry.ParentID, &entry.Sequence, &entry.Kind, &entry.Label, &createdAt); err != nil {
			return nil, fmt.Errorf("scan session graph entry: %w", err)
		}
		entry.CreatedAt = time.Unix(0, createdAt).UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load session graph entries: %w", err)
	}
	return entries, nil
}

func buildSessionTree(entries []GraphEntry) ([]TreeNode, error) {
	byID := make(map[string]GraphEntry, len(entries))
	children := make(map[string][]GraphEntry, len(entries))
	roots := make([]GraphEntry, 0)
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, entry := range entries {
		if entry.ParentID == "" {
			roots = append(roots, entry)
			continue
		}
		if _, ok := byID[entry.ParentID]; !ok {
			roots = append(roots, entry)
			continue
		}
		children[entry.ParentID] = append(children[entry.ParentID], entry)
	}
	state := make(map[string]uint8, len(entries))
	var build func(GraphEntry) (TreeNode, error)
	build = func(entry GraphEntry) (TreeNode, error) {
		if state[entry.ID] == 1 {
			return TreeNode{}, fmt.Errorf("session: graph cycle at %s", entry.ID)
		}
		state[entry.ID] = 1
		node := TreeNode{Entry: entry}
		for _, child := range children[entry.ID] {
			childNode, err := build(child)
			if err != nil {
				return TreeNode{}, err
			}
			node.Children = append(node.Children, childNode)
		}
		state[entry.ID] = 2
		return node, nil
	}
	nodes := make([]TreeNode, 0, len(roots))
	for _, root := range roots {
		node, err := build(root)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func loadSessionBranches(ctx context.Context, queryer dbgen.DBTX, sessionID, active string) ([]SessionBranch, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT name,head_entry_id,created_at,updated_at FROM session_branches WHERE session_id=? ORDER BY created_at,name`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session branches: %w", err)
	}
	defer rows.Close()
	branches := make([]SessionBranch, 0)
	for rows.Next() {
		var branch SessionBranch
		var createdAt, updatedAt int64
		if err := rows.Scan(&branch.Name, &branch.HeadEntryID, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan session branch: %w", err)
		}
		branch.Active = strings.EqualFold(branch.Name, active)
		branch.CreatedAt = time.Unix(0, createdAt).UTC()
		branch.UpdatedAt = time.Unix(0, updatedAt).UTC()
		branches = append(branches, branch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load session branches: %w", err)
	}
	return branches, nil
}

func normalizeBranchName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", errors.New("session: branch name must contain 1 to 64 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return "", errors.New("session: branch name contains an unsupported character")
		}
	}
	return name, nil
}

func normalizeEntryLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 128 {
		return "", errors.New("session: entry label cannot exceed 128 characters")
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", errors.New("session: entry label contains a control character")
		}
	}
	return label, nil
}

func requireOneSessionBranch(result sql.Result, name string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: %s", ErrSessionBranchNotFound, name)
	}
	return nil
}

func filterBlocksToActiveSessionBranch(ctx context.Context, queryer dbgen.DBTX, sessionID string, blocks []Block) ([]Block, error) {
	rows, err := queryer.QueryContext(ctx, `
		WITH RECURSIVE branch(entry_id,parent_entry_id,block_sequence) AS (
			SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
			FROM session_graph_entries entry
			JOIN session_graphs graph ON graph.session_id=entry.session_id AND graph.active_leaf_entry_id=entry.entry_id

			WHERE entry.session_id=?
			UNION ALL
			SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
			FROM session_graph_entries entry JOIN branch ON entry.entry_id=branch.parent_entry_id
			WHERE entry.session_id=?
		)
		SELECT block_sequence FROM branch
	`, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load active session branch: %w", err)
	}
	defer rows.Close()
	sequences := make(map[int64]struct{}, len(blocks))
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			return nil, fmt.Errorf("scan active session branch: %w", err)
		}
		sequences[sequence] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load active session branch: %w", err)
	}
	filtered := make([]Block, 0, len(sequences))
	for _, block := range blocks {
		if _, ok := sequences[block.Sequence]; ok {
			filtered = append(filtered, block)
		}
	}
	return filtered, nil
}

func isUniqueConstraint(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == 19
}
