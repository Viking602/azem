package evidence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestEvidenceLedgerMarksSelectionFeaturesAndOutcome(t *testing.T) {
	t.Parallel()
	workspaceRef := session.SourceRefV1{Kind: "workspace_file", ID: "internal/a.go", SHA256: "abc"}
	externalRef := session.SourceRefV1{Kind: "external_doc", ID: "docs.example/api"}
	ranked := []RankedEvidenceV1{
		{Candidate: CandidateV1{Ref: workspaceRef, Authority: "workspace"}, Score: 20, Features: map[string]int{"symbols": 10, "diagnostics": 9, "workspace_authority": 1}},
		{Candidate: CandidateV1{Ref: externalRef, Authority: "external"}, Score: 3, Features: map[string]int{"content": 3}},
	}
	entry, err := NewLedgerEntry("session", "run", "timeout Run", "find failure source", ranked, []session.SourceRefV1{workspaceRef}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Candidates[0].Selected || entry.Candidates[1].Authority != "secondary" {
		t.Fatalf("entry candidates = %+v", entry.Candidates)
	}
	completed, err := CompleteLedgerEntry(entry, "patched timeout handling", "pass", []session.SourceRefV1{{Kind: "validator", ID: "go-test"}}, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if completed.Outcome == nil || completed.Outcome.Status != "pass" || len(completed.Outcome.Evidence) != 1 {
		t.Fatalf("completed ledger = %+v", completed)
	}
	if _, err := CompleteLedgerEntry(entry, "act", "pass", []session.SourceRefV1{{Kind: "validator", ID: "go-test"}, {Kind: "validator", ID: "go-test"}}, time.Unix(2, 0).UTC()); err == nil {
		t.Fatal("accepted duplicate outcome evidence")
	}
}

func TestEvidenceLedgerRoundTripsLatestOutcomeAfterReopen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "ledger.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "test"}); err != nil {
		t.Fatal(err)
	}
	ref := session.SourceRefV1{Kind: "workspace_file", ID: "a.go"}
	entry, err := NewLedgerEntry("session", "run", "needle", "find source", []RankedEvidenceV1{{Candidate: CandidateV1{Ref: ref, Authority: "workspace"}, Score: 2, Features: map[string]int{"path": 2}}}, []session.SourceRefV1{ref}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewLedgerStore(sessions, "session", "run")
	if err := store.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry, err = CompleteLedgerEntry(entry, "read source", "uncertain", nil, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, _ = NewLedgerStore(session.NewService(provider.DB(), provider.Blobs()), "session", "run")
	loaded, err := store.Latest(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	wrongRun, _ := NewLedgerStore(session.NewService(provider.DB(), provider.Blobs()), "session", "other-run")
	if _, err := wrongRun.Latest(ctx, entry.ID); err == nil {
		t.Fatal("loaded ledger from an unrelated run")
	}
	if loaded.Outcome == nil || loaded.Outcome.Action != "read source" || loaded.Outcome.Status != "uncertain" || loaded.Candidates[0].Features["path"] != 2 {
		t.Fatalf("loaded ledger = %+v", loaded)
	}
}

func TestEvidenceLedgerRejectsUnselectedAndUnsupportedPass(t *testing.T) {
	t.Parallel()
	ref := session.SourceRefV1{Kind: "workspace_file", ID: "a.go"}
	if _, err := NewLedgerEntry("session", "run", "query", "subgoal", []RankedEvidenceV1{{Candidate: CandidateV1{Ref: ref, Authority: "workspace"}, Score: 1, Features: map[string]int{"path": 1}}}, []session.SourceRefV1{{Kind: "workspace_file", ID: "missing.go"}}, time.Unix(1, 0).UTC()); err == nil {
		t.Fatal("accepted selected source absent from candidates")
	}
	entry, err := NewLedgerEntry("session", "run", "query", "subgoal", []RankedEvidenceV1{{Candidate: CandidateV1{Ref: ref, Authority: "workspace"}, Score: 1, Features: map[string]int{"path": 1}}}, []session.SourceRefV1{ref}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteLedgerEntry(entry, "act", "pass", nil, time.Unix(2, 0).UTC()); err == nil {
		t.Fatal("accepted passing outcome without evidence")
	}
}
