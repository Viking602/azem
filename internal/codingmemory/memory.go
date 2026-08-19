// Package codingmemory stores small, typed, provenance-bearing coding memories.
package codingmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/evidence"
	"github.com/Viking602/azem/internal/session"
)

const (
	CatalogVersionV1 = 1
	KindStrategy     = "strategy"
	KindToolLesson   = "tool_lesson"
	KindAssetRef     = "asset_reference"
	ScopeRepository  = "repository"
	ScopeProject     = "project"
	ScopeUser        = "user"
	OriginDerived    = "derived"
	OriginGuidance   = "user_guidance"
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	maxEntries       = 1000
	maxContentBytes  = 16 << 10
)

type ScopeV1 struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type AttributionV1 struct {
	Ref       session.SourceRefV1 `json:"ref"`
	Authority string              `json:"authority"`
}

type MemoryV1 struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Scope      ScopeV1         `json:"scope"`
	Content    string          `json:"content"`
	Confidence float64         `json:"confidence"`
	Status     string          `json:"status"`
	Origin     string          `json:"origin"`
	Sources    []AttributionV1 `json:"sources"`
	Supersedes []string        `json:"supersedes,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type DeletionV1 struct {
	ID        string    `json:"id"`
	Scope     ScopeV1   `json:"scope"`
	Reason    string    `json:"reason"`
	DeletedAt time.Time `json:"deleted_at"`
}

type CatalogV1 struct {
	Version   int          `json:"version"`
	Revision  int64        `json:"revision"`
	Memories  []MemoryV1   `json:"memories"`
	Deletions []DeletionV1 `json:"deletions,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func (memory MemoryV1) Validate() error {
	if memory.Version != 1 || memory.ID == "" || !oneOf(memory.Kind, KindStrategy, KindToolLesson, KindAssetRef) || !oneOf(memory.Scope.Kind, ScopeRepository, ScopeProject, ScopeUser) || memory.Scope.ID == "" || strings.TrimSpace(memory.Content) == "" || len(memory.Content) > maxContentBytes || math.IsNaN(memory.Confidence) || math.IsInf(memory.Confidence, 0) || memory.Confidence < 0 || memory.Confidence > 1 || !oneOf(memory.Status, StatusActive, StatusSuperseded) || !oneOf(memory.Origin, OriginDerived, OriginGuidance) || len(memory.Sources) == 0 || len(memory.Sources) > 32 || memory.CreatedAt.IsZero() || memory.UpdatedAt.IsZero() {
		return fmt.Errorf("coding memory: invalid memory %q", memory.ID)
	}
	seenSources := make(map[string]struct{}, len(memory.Sources))
	for _, source := range memory.Sources {
		if source.Ref.Kind == "" || source.Ref.ID == "" || !oneOf(source.Authority, "workspace", "user", "history", "secondary", "validator") {
			return fmt.Errorf("coding memory: invalid source on %q", memory.ID)
		}
		key := source.Ref.Kind + "\x00" + source.Ref.ID
		if _, duplicate := seenSources[key]; duplicate {
			return fmt.Errorf("coding memory: duplicate source on %q", memory.ID)
		}
		seenSources[key] = struct{}{}
	}
	if memory.Origin == OriginGuidance {
		foundUser := false
		for _, source := range memory.Sources {
			foundUser = foundUser || source.Authority == "user"
		}
		if !foundUser {
			return fmt.Errorf("coding memory: user guidance %q lacks user source", memory.ID)
		}
	}
	return nil
}

func (catalog CatalogV1) Validate() error {
	if catalog.Version != CatalogVersionV1 || catalog.Revision < 0 || len(catalog.Memories) > maxEntries || catalog.UpdatedAt.IsZero() {
		return fmt.Errorf("coding memory: invalid catalog")
	}
	seen := make(map[string]struct{}, len(catalog.Memories)+len(catalog.Deletions))
	for _, memory := range catalog.Memories {
		if err := memory.Validate(); err != nil {
			return err
		}
		if _, exists := seen[memory.ID]; exists {
			return fmt.Errorf("coding memory: duplicate id %q", memory.ID)
		}
		seen[memory.ID] = struct{}{}
	}
	for _, deletion := range catalog.Deletions {
		if deletion.ID == "" || deletion.Scope.ID == "" || deletion.DeletedAt.IsZero() {
			return fmt.Errorf("coding memory: invalid deletion")
		}
		if _, exists := seen[deletion.ID]; exists {
			return fmt.Errorf("coding memory: deletion still has content %q", deletion.ID)
		}
		seen[deletion.ID] = struct{}{}
	}
	return nil
}

type Store struct {
	sessions  *session.Service
	sessionID string
	runID     string
}

var catalogLocks sync.Map

func catalogLock(sessionID string) *sync.Mutex {
	lock, _ := catalogLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func NewStore(sessions *session.Service, sessionID, runID string) (*Store, error) {
	if sessions == nil || sessionID == "" || runID == "" {
		return nil, fmt.Errorf("coding memory: store requires session service and ids")
	}
	return &Store{sessions: sessions, sessionID: sessionID, runID: runID}, nil
}

func (s *Store) Add(ctx context.Context, memory MemoryV1, policy PolicyV1, authorizedScope ScopeV1, explicitUserAction bool, now time.Time) (CatalogV1, error) {
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := policy.Validate(); err != nil {
		return CatalogV1{}, err
	}
	if !policy.LocalLearningEnabled {
		return CatalogV1{}, fmt.Errorf("coding memory: local learning is disabled")
	}
	if memory.Scope != authorizedScope {
		return CatalogV1{}, fmt.Errorf("coding memory: scope authorization mismatch")
	}
	if memory.Origin == OriginGuidance && !explicitUserAction {
		return CatalogV1{}, fmt.Errorf("coding memory: explicit user action required to store guidance")
	}
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return CatalogV1{}, err
	}
	if err := memory.Validate(); err != nil {
		return CatalogV1{}, err
	}
	if err := s.validateSources(ctx, memory); err != nil {
		return CatalogV1{}, err
	}
	for _, current := range catalog.Memories {
		if current.ID == memory.ID {
			return CatalogV1{}, fmt.Errorf("coding memory: id %q already exists", memory.ID)
		}
	}
	catalog.Memories = append(catalog.Memories, memory)
	return s.save(ctx, catalog, now)
}

func (s *Store) validateSources(ctx context.Context, memory MemoryV1) error {
	ledgerStore, err := evidenceLedgerStore(s)
	if err != nil {
		return err
	}
	for _, source := range memory.Sources {
		if source.Ref.Kind != "evidence_ledger" {
			continue
		}
		if _, err := ledgerStore.Latest(ctx, source.Ref.ID); err != nil {
			return fmt.Errorf("coding memory: invalid ledger provenance %q: %w", source.Ref.ID, err)
		}
	}
	return nil
}

func evidenceLedgerStore(s *Store) (*evidence.LedgerStore, error) {
	return evidence.NewLedgerStore(s.sessions, s.sessionID, s.runID)
}

func (s *Store) Supersede(ctx context.Context, targetID string, replacement MemoryV1, explicitUserAction bool, now time.Time) (CatalogV1, error) {
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return CatalogV1{}, err
	}
	index := memoryIndex(catalog.Memories, targetID)
	if index < 0 || catalog.Memories[index].Status != StatusActive {
		return CatalogV1{}, fmt.Errorf("coding memory: active target %q not found", targetID)
	}
	if (catalog.Memories[index].Origin == OriginGuidance || replacement.Origin == OriginGuidance) && !explicitUserAction {
		return CatalogV1{}, fmt.Errorf("coding memory: explicit user action required for guidance")
	}
	if replacement.ID == targetID || replacement.Status != StatusActive || !contains(replacement.Supersedes, targetID) {
		return CatalogV1{}, fmt.Errorf("coding memory: replacement must explicitly supersede target")
	}
	if err := replacement.Validate(); err != nil {
		return CatalogV1{}, err
	}
	if err := s.validateSources(ctx, replacement); err != nil {
		return CatalogV1{}, err
	}
	catalog.Memories[index].Status = StatusSuperseded
	catalog.Memories[index].UpdatedAt = now.UTC()
	catalog.Memories = append(catalog.Memories, replacement)
	return s.save(ctx, catalog, now)
}

// Forget removes content from the active catalog and appends a bounded tombstone.
// Guidance requires an explicit user action; derived memory may be forgotten by policy.
func (s *Store) Forget(ctx context.Context, id, reason string, explicitUserAction bool, now time.Time) (CatalogV1, error) {
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return CatalogV1{}, err
	}
	index := memoryIndex(catalog.Memories, id)
	if index < 0 {
		return CatalogV1{}, fmt.Errorf("coding memory: memory %q not found", id)
	}
	memory := catalog.Memories[index]
	if memory.Origin == OriginGuidance && !explicitUserAction {
		return CatalogV1{}, fmt.Errorf("coding memory: explicit user action required to forget guidance")
	}
	catalog.Memories = append(catalog.Memories[:index:index], catalog.Memories[index+1:]...)
	catalog.Deletions = append(catalog.Deletions, DeletionV1{ID: id, Scope: memory.Scope, Reason: strings.TrimSpace(reason), DeletedAt: now.UTC()})
	if len(catalog.Deletions) > maxEntries {
		catalog.Deletions = append([]DeletionV1(nil), catalog.Deletions[len(catalog.Deletions)-maxEntries:]...)
	}
	return s.save(ctx, catalog, now)
}

func (s *Store) Active(ctx context.Context, scopes []ScopeV1, now time.Time) ([]MemoryV1, error) {
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		allowed[scope.Kind+"\x00"+scope.ID] = struct{}{}
	}
	result := make([]MemoryV1, 0)
	for _, memory := range catalog.Memories {
		if memory.Status != StatusActive {
			continue
		}
		if _, ok := allowed[memory.Scope.Kind+"\x00"+memory.Scope.ID]; ok {
			result = append(result, memory)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Confidence != result[j].Confidence {
			return result[i].Confidence > result[j].Confidence
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// Recall resolves semantic-state references against the current catalog.
// Opt-out, forgetting, scope changes, and supersession all fail closed.
func (s *Store) Recall(ctx context.Context, refs []SalienceRefV1, policy PolicyV1, now time.Time) ([]MemoryV1, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if !policy.ExperiencePromotionEnabled {
		return nil, nil
	}
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return nil, err
	}
	active := make(map[string]MemoryV1, len(catalog.Memories))
	for _, memory := range catalog.Memories {
		if memory.Status == StatusActive {
			active[memory.ID] = memory
		}
	}
	result := make([]MemoryV1, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind != "coding_memory" {
			continue
		}
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		memory, exists := active[ref.ID]
		if !exists || memory.Scope != ref.Scope {
			continue
		}
		result = append(result, memory)
	}
	return result, nil
}

// PruneExpired removes derived memory content after the user-configured
// retention window. Direct user guidance is never removed by retention.
func (s *Store) PruneExpired(ctx context.Context, policy PolicyV1, now time.Time) (CatalogV1, error) {
	if err := policy.Validate(); err != nil {
		return CatalogV1{}, err
	}
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	catalog, err := s.loadOrEmpty(ctx, now)
	if err != nil {
		return CatalogV1{}, err
	}
	cutoff := now.UTC().Add(-time.Duration(policy.RetentionDays) * 24 * time.Hour)
	kept := make([]MemoryV1, 0, len(catalog.Memories))
	removed := 0
	for _, memory := range catalog.Memories {
		if memory.Origin == OriginDerived && !memory.UpdatedAt.After(cutoff) {
			catalog.Deletions = append(catalog.Deletions, DeletionV1{ID: memory.ID, Scope: memory.Scope, Reason: "retention_expired", DeletedAt: now.UTC()})
			removed++
			continue
		}
		kept = append(kept, memory)
	}
	if removed == 0 {
		return catalog, nil
	}
	catalog.Memories = kept
	if len(catalog.Deletions) > maxEntries {
		catalog.Deletions = append([]DeletionV1(nil), catalog.Deletions[len(catalog.Deletions)-maxEntries:]...)
	}
	return s.save(ctx, catalog, now)
}

func (s *Store) Load(ctx context.Context) (CatalogV1, error) {
	lock := catalogLock(s.sessionID)
	lock.Lock()
	defer lock.Unlock()
	return s.load(ctx)
}

func (s *Store) loadOrEmpty(ctx context.Context, now time.Time) (CatalogV1, error) {
	catalog, err := s.load(ctx)
	if err == nil {
		return catalog, nil
	}
	if !errors.Is(err, session.ErrContextArtifactNotFound) {
		return CatalogV1{}, err
	}
	return CatalogV1{Version: CatalogVersionV1, Memories: []MemoryV1{}, UpdatedAt: now.UTC()}, nil
}

func (s *Store) load(ctx context.Context) (CatalogV1, error) {
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, session.InternalArtifactKindPrefix+"coding_memory_catalog_v1")
	if err != nil {
		return CatalogV1{}, err
	}
	var catalog CatalogV1
	if err := decodeStrict(artifact.Payload, &catalog); err != nil {
		return CatalogV1{}, err
	}
	return catalog, catalog.Validate()
}

func (s *Store) save(ctx context.Context, catalog CatalogV1, now time.Time) (CatalogV1, error) {
	catalog.Version = CatalogVersionV1
	catalog.Revision++
	catalog.UpdatedAt = now.UTC()
	if err := catalog.Validate(); err != nil {
		return CatalogV1{}, err
	}
	payload, err := json.Marshal(catalog)
	if err != nil {
		return CatalogV1{}, err
	}
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, session.InternalArtifactKindPrefix+"coding_memory_catalog_v1", payload, fmt.Sprintf("coding memory revision %d", catalog.Revision)); err != nil {
		return CatalogV1{}, err
	}
	return catalog, nil
}

func memoryIndex(memories []MemoryV1, id string) int {
	for index := range memories {
		if memories[index].ID == id {
			return index
		}
	}
	return -1
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func decodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("coding memory: multiple JSON values")
		}
		return err
	}
	return nil
}
