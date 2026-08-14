package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestUsageReportEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newUsageReportService(t)
	report, err := svc.UsageReport(ctx, UsageReportQuery{Scope: UsageScopeAll, Now: time.Date(2026, 8, 14, 15, 0, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Empty || report.Requests != 0 || len(report.Days) != 0 || len(report.Models) != 0 || len(report.Skills) != 0 {
		t.Fatalf("empty report = %+v", report)
	}
	if report.From != "2025-08-14" || report.To != "2026-08-14" {
		t.Fatalf("window = %s %s", report.From, report.To)
	}
}

func TestUsageReportAggregatesCompletedFactsAndCacheSemantics(t *testing.T) {
	ctx := context.Background()
	svc := newUsageReportService(t)
	now := time.Date(2026, 8, 14, 16, 0, 0, 0, time.Local)
	if _, err := svc.Ensure(ctx, Session{ID: "sess-a", Title: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ensure(ctx, Session{ID: "sess-b", Title: "b"}); err != nil {
		t.Fatal(err)
	}
	facts := []ProviderRequestFact{
		{
			RequestID: "deepseek-1", SessionID: "sess-a", RunID: "run-a", RequestKind: "main",
			Provider: "deepseek", Model: "deepseek-v4-flash", Status: "completed",
			StartedAt: now.Add(-2 * time.Hour), CompletedAt: now.Add(-2 * time.Hour),
			InputTokens: 100, CachedTokens: 40, CacheWriteTokens: 8, OutputTokens: 12, TotalTokens: 112,
			CacheReported: true, CacheWriteReported: true,
		},
		{
			RequestID: "unknown-1", SessionID: "sess-a", RunID: "run-a", RequestKind: "subagent",
			Provider: "mystery", Model: "mystery-1", Status: "completed",
			StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Hour),
			InputTokens: 50, CachedTokens: 9, OutputTokens: 5, TotalTokens: 55,
		},
		{
			RequestID: "started-only", SessionID: "sess-a", RunID: "run-a", RequestKind: "main",
			Provider: "grok", Model: "grok-4", Status: "started", StartedAt: now,
			InputTokens: 999, OutputTokens: 999, TotalTokens: 1998,
		},
	}
	for _, fact := range facts {
		if err := svc.UpsertProviderRequest(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.StartToolRecord(ctx, "sess-a", ToolRecord{
		RunID: "run-a", ToolCallID: "skill-1", Name: "hydaelyn_activate_skill",
		Arguments: json.RawMessage(`{"name":"think"}`), StartedAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishToolRecord(ctx, "sess-a", ToolRecord{
		RunID: "run-a", ToolCallID: "skill-1", State: ToolCompleted, CompletedAt: now.Add(-29 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartToolRecord(ctx, "sess-a", ToolRecord{
		RunID: "run-a", ToolCallID: "skill-fail", Name: "hydaelyn_activate_skill",
		Arguments: json.RawMessage(`{"name":"broken"}`), StartedAt: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishToolRecord(ctx, "sess-a", ToolRecord{
		RunID: "run-a", ToolCallID: "skill-fail", State: ToolFailed, CompletedAt: now.Add(-19 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	report, err := svc.UsageReport(ctx, UsageReportQuery{Scope: UsageScopeAll, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if report.Empty || report.Requests != 2 || report.Sessions != 1 || report.Runs != 1 {
		t.Fatalf("counts = %+v", report)
	}
	if report.InputTokens != 150 || report.OutputTokens != 17 || report.TotalTokens != 167 {
		t.Fatalf("token totals = %+v", report)
	}
	if !report.CacheReported || report.ReportedInput != 100 || report.CacheReadTokens != 40 || report.CacheWriteTokens != 8 {
		t.Fatalf("cache inclusive/unreported mix = %+v", report)
	}
	if report.PeakDayTokens != 167 || report.CurrentStreak != 1 || report.LongestStreak != 1 {
		t.Fatalf("activity = %+v", report)
	}
	if len(report.Kinds) != 2 || report.Kinds[0].Kind != "main" || report.Kinds[1].Kind != "subagent" {
		t.Fatalf("kinds = %+v", report.Kinds)
	}
	if len(report.Models) != 2 {
		t.Fatalf("models = %+v", report.Models)
	}
	var deepseek, mystery UsageModelRow
	for _, model := range report.Models {
		switch model.Provider {
		case "deepseek":
			deepseek = model
		case "mystery":
			mystery = model
		}
	}
	if !deepseek.CacheReported || deepseek.CacheReadTokens != 40 || mystery.CacheReported || mystery.CacheReadTokens != 0 {
		t.Fatalf("model cache = deepseek %+v mystery %+v", deepseek, mystery)
	}
	if len(report.Skills) != 1 || report.Skills[0].Name != "think" || report.Skills[0].Activations != 1 {
		t.Fatalf("skills = %+v", report.Skills)
	}
	if report.LongestRunMS < 60*60*1000 {
		t.Fatalf("longest run ms = %d", report.LongestRunMS)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ToLower(string(encoded))
	for _, leaked := range []string{"secret", "apikey", "api_key", "credential", "sk-"} {
		if strings.Contains(payload, leaked) {
			t.Fatalf("usage report leaked %q: %s", leaked, payload)
		}
	}
}

func TestUsageReportDoesNotCrossProjects(t *testing.T) {
	ctx := context.Background()
	svc := newUsageReportService(t)
	now := time.Now()
	projectA := t.TempDir()
	projectB := t.TempDir()
	if _, err := svc.Ensure(ctx, Session{ID: "alpha", Title: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ensure(ctx, Session{ID: "beta", Title: "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWorkspaceSession(ctx, projectA, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWorkspaceSession(ctx, projectB, "beta"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertProviderRequest(ctx, ProviderRequestFact{
		RequestID: "a1", SessionID: "alpha", RunID: "run-a", RequestKind: "main",
		Provider: "chatgpt", Model: "gpt-5.6", Status: "completed",
		StartedAt: now, CompletedAt: now, InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertProviderRequest(ctx, ProviderRequestFact{
		RequestID: "b1", SessionID: "beta", RunID: "run-b", RequestKind: "main",
		Provider: "grok", Model: "grok-4", Status: "completed",
		StartedAt: now, CompletedAt: now, InputTokens: 80, OutputTokens: 8, TotalTokens: 88,
	}); err != nil {
		t.Fatal(err)
	}

	keyA, err := projectKey(projectA)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := svc.UsageReport(ctx, UsageReportQuery{Scope: UsageScopeProject, Workspace: keyA, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Requests != 1 || scoped.TotalTokens != 12 || scoped.Sessions != 1 {
		t.Fatalf("project A report mixed other workspaces: %+v", scoped)
	}
	if len(scoped.Models) != 1 || scoped.Models[0].Provider != "chatgpt" {
		t.Fatalf("project A models = %+v", scoped.Models)
	}
	all, err := svc.UsageReport(ctx, UsageReportQuery{Scope: UsageScopeAll, Workspace: keyA, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if all.Requests != 2 || all.TotalTokens != 100 {
		t.Fatalf("all-projects report = %+v", all)
	}
}

func TestUsageReportStreaksUseLocalCalendarDays(t *testing.T) {
	ctx := context.Background()
	svc := newUsageReportService(t)
	now := time.Now()
	if _, err := svc.Ensure(ctx, Session{ID: "streak", Title: "streak"}); err != nil {
		t.Fatal(err)
	}
	for _, daysAgo := range []int{0, 1, 4} {
		at := localDayAfternoon(now, daysAgo)
		if err := svc.UpsertProviderRequest(ctx, ProviderRequestFact{
			RequestID: "d-" + at.Format("20060102"), SessionID: "streak", RunID: "run", RequestKind: "main",
			Provider: "chatgpt", Model: "gpt-5.6", Status: "completed",
			StartedAt: at, CompletedAt: at, InputTokens: 3, OutputTokens: 1, TotalTokens: 4,
		}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := svc.UsageReport(ctx, UsageReportQuery{Scope: UsageScopeAll, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentStreak != 2 || report.LongestStreak != 2 {
		t.Fatalf("streaks = current %d longest %d days=%+v", report.CurrentStreak, report.LongestStreak, report.Days)
	}
}

func newUsageReportService(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	return NewService(store.DB())
}

func localDayAfternoon(now time.Time, daysAgo int) time.Time {
	loc := now.Location()
	year, month, day := now.In(loc).Date()
	return time.Date(year, month, day, 15, 0, 0, 0, loc).AddDate(0, 0, -daysAgo)
}
