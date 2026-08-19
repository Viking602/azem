package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const EvidenceLedgerVersionV1 = 1

type CandidateRecordV1 struct {
	Ref       session.SourceRefV1 `json:"ref"`
	Authority string              `json:"authority"`
	Rank      int                 `json:"rank"`
	Score     int                 `json:"score"`
	Features  map[string]int      `json:"features"`
	Selected  bool                `json:"selected"`
}

type ConsumerOutcomeV1 struct {
	Action      string                `json:"action"`
	Status      string                `json:"status"`
	Evidence    []session.SourceRefV1 `json:"evidence,omitempty"`
	CompletedAt time.Time             `json:"completed_at"`
}

type LedgerEntryV1 struct {
	Version    int                   `json:"version"`
	ID         string                `json:"id"`
	SessionID  string                `json:"session_id"`
	RunID      string                `json:"run_id"`
	Query      string                `json:"query"`
	Subgoal    string                `json:"subgoal"`
	Candidates []CandidateRecordV1   `json:"candidates"`
	Selected   []session.SourceRefV1 `json:"selected"`
	Outcome    *ConsumerOutcomeV1    `json:"outcome,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
}

func (entry LedgerEntryV1) Validate() error {
	if entry.Version != EvidenceLedgerVersionV1 || entry.ID == "" || entry.SessionID == "" || entry.RunID == "" || strings.TrimSpace(entry.Query) == "" || strings.TrimSpace(entry.Subgoal) == "" || len(entry.Candidates) == 0 || entry.CreatedAt.IsZero() {
		return fmt.Errorf("evidence: invalid ledger entry")
	}
	selected := make(map[string]struct{}, len(entry.Selected))
	if err := validateSourceRefs(entry.Selected, "selected"); err != nil {
		return err
	}
	for _, ref := range entry.Selected {
		selected[refKey(ref)] = struct{}{}
	}
	matchedSelected := 0
	candidateRefs := make(map[string]struct{}, len(entry.Candidates))
	for index, candidate := range entry.Candidates {
		if candidate.Ref.Kind == "" || candidate.Ref.ID == "" || candidate.Rank != index+1 || candidate.Score <= 0 || candidate.Authority == "" || len(candidate.Features) == 0 {
			return fmt.Errorf("evidence: invalid candidate at rank %d", index+1)
		}
		if _, duplicate := candidateRefs[refKey(candidate.Ref)]; duplicate {
			return fmt.Errorf("evidence: duplicate candidate source")
		}
		candidateRefs[refKey(candidate.Ref)] = struct{}{}
		_, expectedSelected := selected[refKey(candidate.Ref)]
		if expectedSelected {
			matchedSelected++
		}
		if candidate.Selected != expectedSelected {
			return fmt.Errorf("evidence: selected candidate mismatch")
		}
	}
	if matchedSelected != len(selected) {
		return fmt.Errorf("evidence: selected source absent from candidates")
	}
	if entry.Outcome != nil {
		if strings.TrimSpace(entry.Outcome.Action) == "" || (entry.Outcome.Status != "pass" && entry.Outcome.Status != "fail" && entry.Outcome.Status != "uncertain") || entry.Outcome.CompletedAt.IsZero() {
			return fmt.Errorf("evidence: invalid consumer outcome")
		}
		if err := validateSourceRefs(entry.Outcome.Evidence, "outcome"); err != nil {
			return err
		}
		if entry.Outcome.Status == "pass" && len(entry.Outcome.Evidence) == 0 {
			return fmt.Errorf("evidence: passing outcome requires evidence")
		}
	}
	return nil
}

func validateSourceRefs(refs []session.SourceRefV1, label string) error {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.Kind == "" || ref.ID == "" {
			return fmt.Errorf("evidence: invalid %s source", label)
		}
		key := refKey(ref)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("evidence: duplicate %s source", label)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func refKey(ref session.SourceRefV1) string {
	return ref.Kind + "\x00" + ref.ID
}

func NewLedgerEntry(sessionID, runID, query, subgoal string, ranked []RankedEvidenceV1, selected []session.SourceRefV1, createdAt time.Time) (LedgerEntryV1, error) {
	selectedSet := make(map[string]struct{}, len(selected))
	for _, ref := range selected {
		selectedSet[ref.Kind+"\x00"+ref.ID] = struct{}{}
	}
	candidates := make([]CandidateRecordV1, 0, len(ranked))
	for index, item := range ranked {
		_, isSelected := selectedSet[item.Candidate.Ref.Kind+"\x00"+item.Candidate.Ref.ID]
		features := make(map[string]int, len(item.Features))
		for name, value := range item.Features {
			features[name] = value
		}
		authority := item.Candidate.Authority
		if authority == "external" {
			authority = "secondary"
		}
		candidates = append(candidates, CandidateRecordV1{Ref: item.Candidate.Ref, Authority: authority, Rank: index + 1, Score: item.Score, Features: features, Selected: isSelected})
	}
	identity := struct {
		SessionID  string                `json:"session_id"`
		RunID      string                `json:"run_id"`
		Query      string                `json:"query"`
		Subgoal    string                `json:"subgoal"`
		Candidates []CandidateRecordV1   `json:"candidates"`
		Selected   []session.SourceRefV1 `json:"selected"`
	}{sessionID, runID, query, subgoal, candidates, selected}
	entry := LedgerEntryV1{
		Version: EvidenceLedgerVersionV1, ID: "evidence-ledger:" + hashJSON(identity)[:24], SessionID: sessionID, RunID: runID,
		Query: query, Subgoal: subgoal, Candidates: candidates, Selected: append([]session.SourceRefV1(nil), selected...), CreatedAt: createdAt.UTC(),
	}
	return entry, entry.Validate()
}

func CompleteLedgerEntry(entry LedgerEntryV1, action, status string, evidence []session.SourceRefV1, completedAt time.Time) (LedgerEntryV1, error) {
	if err := entry.Validate(); err != nil {
		return LedgerEntryV1{}, err
	}
	entry.Candidates = append([]CandidateRecordV1(nil), entry.Candidates...)
	entry.Selected = append([]session.SourceRefV1(nil), entry.Selected...)
	entry.Outcome = &ConsumerOutcomeV1{Action: action, Status: status, Evidence: append([]session.SourceRefV1(nil), evidence...), CompletedAt: completedAt.UTC()}
	return entry, entry.Validate()
}

type LedgerStore struct {
	sessions  *session.Service
	sessionID string
	runID     string
}

func NewLedgerStore(sessions *session.Service, sessionID, runID string) (*LedgerStore, error) {
	if sessions == nil || sessionID == "" || runID == "" {
		return nil, fmt.Errorf("evidence: ledger store requires session service and ids")
	}
	return &LedgerStore{sessions: sessions, sessionID: sessionID, runID: runID}, nil
}

func (s *LedgerStore) Save(ctx context.Context, entry LedgerEntryV1) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.SessionID != s.sessionID || entry.RunID != s.runID {
		return fmt.Errorf("evidence: ledger store identity mismatch")
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	kind := session.InternalArtifactKindPrefix + "evidence_ledger_v1:" + hashJSON(entry.ID)[:24]
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, kind, payload, entry.Subgoal); err != nil {
		return fmt.Errorf("evidence: persist ledger: %w", err)
	}
	return nil
}

func (s *LedgerStore) Latest(ctx context.Context, ledgerID string) (LedgerEntryV1, error) {
	kind := session.InternalArtifactKindPrefix + "evidence_ledger_v1:" + hashJSON(ledgerID)[:24]
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, kind)
	if err != nil {
		return LedgerEntryV1{}, err
	}
	if artifact.SessionID != s.sessionID || artifact.RunID != s.runID {
		return LedgerEntryV1{}, fmt.Errorf("evidence: ledger artifact session/run mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact.Payload))
	decoder.DisallowUnknownFields()
	var entry LedgerEntryV1
	if err := decoder.Decode(&entry); err != nil {
		return LedgerEntryV1{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return LedgerEntryV1{}, fmt.Errorf("evidence: ledger contains trailing JSON")
	}
	if err := entry.Validate(); err != nil {
		return LedgerEntryV1{}, err
	}
	if entry.ID != ledgerID || entry.SessionID != s.sessionID || entry.RunID != s.runID {
		return LedgerEntryV1{}, fmt.Errorf("evidence: ledger artifact identity mismatch")
	}
	return entry, nil
}

func hashJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
