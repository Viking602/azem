package workrevision

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestHandleGuidanceCancelsOnlySafeObsoleteWork(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "old requirement", "a.go", "a", 1)
	current := mustRevision(t, "new requirement", "a.go", "a", 2)
	safe := boundGuidanceIntent(t, source, "safe")
	unsafe := boundGuidanceIntent(t, source, "unsafe")
	var cancelled []string
	decisions, err := HandleGuidance(t.Context(), current, []ActiveIntentV1{
		{Intent: safe, SourceRevision: source, CancelSafe: true},
		{Intent: unsafe, SourceRevision: source, CancelSafe: false},
	}, func(_ context.Context, id string) error {
		cancelled = append(cancelled, id)
		return nil
	}, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cancelled, []string{"safe"}) {
		t.Fatalf("cancelled = %v", cancelled)
	}
	if decisions[0].Action != GuidanceCancel || decisions[1].Action != GuidanceContinueQuarantine {
		t.Fatalf("decisions = %+v", decisions)
	}
	for _, decision := range decisions {
		if !decision.PreserveTerminalEvidence || len(decision.Sources) != 2 {
			t.Fatalf("decision dropped evidence = %+v", decision)
		}
	}
}

func TestHandleGuidanceQuarantinesAfterFailedCancel(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "old", "a.go", "a", 1)
	current := mustRevision(t, "new", "a.go", "a", 2)
	intent := boundGuidanceIntent(t, source, "intent")
	decisions, err := HandleGuidance(t.Context(), current, []ActiveIntentV1{{Intent: intent, SourceRevision: source, CancelSafe: true}}, func(context.Context, string) error {
		return errors.New("still running")
	}, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Action != GuidanceContinueQuarantine || decisions[0].CancelError != "still running" {
		t.Fatalf("failed-cancel decision = %+v", decisions[0])
	}
}

func TestHandleGuidanceLeavesCompatibleWorkRunning(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "same", "a.go", "a", 1)
	current := mustRevision(t, "same", "a.go", "a", 2)
	intent := boundGuidanceIntent(t, source, "intent")
	calls := 0
	decisions, err := HandleGuidance(t.Context(), current, []ActiveIntentV1{{Intent: intent, SourceRevision: source, CancelSafe: true}}, func(context.Context, string) error {
		calls++
		return nil
	}, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || decisions[0].Action != GuidanceContinue {
		t.Fatalf("compatible decisions = %+v calls=%d", decisions, calls)
	}
}

func boundGuidanceIntent(t *testing.T, revision session.WorkRevisionV1, id string) session.ActionIntentV1 {
	t.Helper()
	intent, err := BindIntent(revision, session.ActionIntentV1{
		Version: 1, ID: id, WorkSpecID: "work", SessionID: revision.SessionID, RunID: "run", Kind: "subagent", Name: "review", CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}
