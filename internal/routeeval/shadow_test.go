package routeeval

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestShadowDecisionLogsScoresWithoutChangingControlPlane(t *testing.T) {
	t.Parallel()
	control := ControlPlaneV1{Route: "provider/model-a", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "venat", Budget: "standard", CancellationOwner: "coordinator"}
	decision, err := LogShadowDecision("session", "run", control, control, []ShadowAlternativeV1{
		{Route: "provider/model-b", Score: 0.7, Uncertainty: 0.2},
		{Route: "provider/model-a", Score: 0.6, Uncertainty: 0.1},
	}, "uncertain task class", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Mode != "shadow" || decision.ExecutedRoute != control.Route || decision.ShadowChoice != "provider/model-b" || decision.ControlDigest != controlDigest(control) || decision.ExplorationReason != "uncertain task class" {
		t.Fatalf("decision = %+v", decision)
	}
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "shadow.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "shadow"}); err != nil {
		t.Fatal(err)
	}
	store, _ := NewShadowStore(sessions, "session", "run")
	if err := store.Save(ctx, decision); err != nil {
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
	store, _ = NewShadowStore(session.NewService(provider.DB(), provider.Blobs()), "session", "run")
	loaded, err := store.Latest(ctx, decision.ID)
	if err != nil || loaded.ControlDigest != decision.ControlDigest || loaded.Alternatives[0].Route != "provider/model-b" {
		t.Fatalf("loaded = %+v err=%v", loaded, err)
	}
}

func TestShadowDecisionRejectsAnyControlPlaneMutation(t *testing.T) {
	t.Parallel()
	before := ControlPlaneV1{Route: "route-a", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "venat", Budget: "standard", CancellationOwner: "coordinator"}
	mutations := []ControlPlaneV1{
		{Route: "route-b", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "venat", Budget: "standard", CancellationOwner: "coordinator"},
		{Route: "route-a", PermissionsHash: "broader", ApprovalMode: "ask", RetryOwner: "venat", Budget: "standard", CancellationOwner: "coordinator"},
		{Route: "route-a", PermissionsHash: "permissions", ApprovalMode: "auto", RetryOwner: "venat", Budget: "standard", CancellationOwner: "coordinator"},
		{Route: "route-a", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "provider", Budget: "standard", CancellationOwner: "coordinator"},
		{Route: "route-a", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "venat", Budget: "large", CancellationOwner: "coordinator"},
		{Route: "route-a", PermissionsHash: "permissions", ApprovalMode: "ask", RetryOwner: "venat", Budget: "standard", CancellationOwner: "model"},
	}
	for _, after := range mutations {
		if _, err := LogShadowDecision("session", "run", before, after, []ShadowAlternativeV1{{Route: "route-b", Score: 0.8, Uncertainty: 0.1}}, "", time.Unix(1, 0).UTC()); err == nil {
			t.Fatalf("accepted control mutation: %+v", after)
		}
	}
}
