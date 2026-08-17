package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestListUsageEmitsSecretFreeProjectSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	sessions := session.NewService(store.DB())
	project := t.TempDir()
	other := t.TempDir()
	if _, err := sessions.Ensure(ctx, session.Session{ID: "owned", Title: "owned"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Ensure(ctx, session.Session{ID: "foreign", Title: "foreign"}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetWorkspaceSession(ctx, project, "owned"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetWorkspaceSession(ctx, other, "foreign"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := sessions.UpsertProviderRequest(ctx, session.ProviderRequestFact{
		RequestID: "owned-1", SessionID: "owned", RunID: "run-owned", RequestKind: "main",
		Provider: "chatgpt", Model: "gpt-5.6", Status: "completed",
		StartedAt: now, CompletedAt: now, InputTokens: 20, OutputTokens: 4, TotalTokens: 24,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.UpsertProviderRequest(ctx, session.ProviderRequestFact{
		RequestID: "foreign-1", SessionID: "foreign", RunID: "run-foreign", RequestKind: "main",
		Provider: "grok", Model: "grok-4", Status: "completed",
		StartedAt: now, CompletedAt: now, InputTokens: 200, OutputTokens: 40, TotalTokens: 240,
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	service.SetWorkspaceAnchor(project)
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(shutdown)
	})

	if err := service.ExecuteAction(ctx, Action{Kind: ActionListUsage, Target: session.UsageScopeProject}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventUsageReport || event.UsageReport == nil || event.UsageReport.Requests != 1 || event.UsageReport.TotalTokens != 24 {
		t.Fatalf("project usage event = %+v", event)
	}
	if event.UsageReport.Workspace != canonicalWorkspaceAnchor(project) {
		t.Fatalf("workspace = %q want %q", event.UsageReport.Workspace, canonicalWorkspaceAnchor(project))
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ToLower(string(encoded))
	for _, leaked := range []string{`"secret"`, "apikey", "api_key", "credential_ref", "sk-"} {
		if strings.Contains(payload, leaked) {
			t.Fatalf("usage event leaked %q: %s", leaked, payload)
		}
	}
	cloned := event.Clone()
	event.UsageReport.Models = append(event.UsageReport.Models, session.UsageModelRow{Provider: "mutated"})
	if len(cloned.UsageReport.Models) != 1 || cloned.UsageReport.Models[0].Provider == "mutated" {
		t.Fatal("Event.Clone shared usage report slices")
	}

	if err := service.ExecuteAction(ctx, Action{Kind: ActionListUsage, Target: session.UsageScopeAll}); err != nil {
		t.Fatal(err)
	}
	all, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if all.UsageReport == nil || all.UsageReport.Requests != 2 || all.UsageReport.TotalTokens != 264 {
		t.Fatalf("all usage event = %+v", all.UsageReport)
	}
}

func TestUsageReportEmptyWithoutSessions(t *testing.T) {
	ctx := context.Background()
	service := NewService(ctx, config.Default())
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(shutdown)
	})
	report, err := service.UsageReport(ctx, session.UsageScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Empty || report.Scope != session.UsageScopeAll {
		t.Fatalf("empty report = %+v", report)
	}
}
