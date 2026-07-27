package tui

import (
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/app"
	"github.com/charmbracelet/x/ansi"
)

func TestEstimatedContextProfileRendersSegmentedFooterImmediately(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 272_000
	model.applyEvent(app.Event{Kind: app.EventContextProfile, State: "estimated", ContextProfile: &app.ContextProfile{
		Source: "bootstrap", Estimated: true,
		Contributions: []app.ContextContribution{
			{Category: app.ContextCategoryCore, Name: "azem.core_instructions", Tokens: 100},
			{Category: app.ContextCategorySkills, Name: "demo", Tokens: 200},
			{Category: app.ContextCategoryBuiltinTools, Name: "read", Tokens: 300},
			{Category: app.ContextCategoryMCP, Name: "mcp__demo__search", Tokens: 400},
		},
	}})

	footer := ansi.Strip(model.renderContextUsage(120))
	if !strings.Contains(footer, "1K / 272K") || !strings.Contains(footer, "~0.4%") || !strings.Contains(footer, "■") {
		t.Fatalf("estimated context footer = %q", footer)
	}
}

func TestContextCommandOpensDetailedBreakdown(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.width, model.height = 120, 40
	model.usage.ContextLimit = 10_000
	model.contextProfile = app.ContextProfile{Source: "bootstrap", Estimated: true, Contributions: []app.ContextContribution{
		{Category: app.ContextCategoryCore, Name: "azem.core_instructions", Tokens: 100},
		{Category: app.ContextCategorySkills, Name: "demo", Tokens: 200},
		{Category: app.ContextCategoryMCP, Name: "mcp__demo__search", Tokens: 300},
		{Category: app.ContextCategoryConversation, Name: "message:user:1", Tokens: 400},
	}}

	command, ok, err := ParseCommand("/context")
	if err != nil || !ok {
		t.Fatalf("ParseCommand(/context) = %#v ok=%v err=%v", command, ok, err)
	}
	updated, cmd := model.executeCommand(command)
	if cmd != nil {
		t.Fatal("/context unexpectedly returned a command")
	}
	model = updated.(AppModel)
	if model.overlay != OverlayContext {
		t.Fatalf("overlay = %q, want context", model.overlay)
	}
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"CONTEXT USAGE", "Core instructions", "Skills", "MCP tools", "Conversation", "demo / search"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("context overlay missing %q:\n%s", wanted, content)
		}
	}

	updated, _ = model.executeCommand(Command{Name: "context", Args: []string{"extra"}})
	model = updated.(AppModel)
	if model.errorBanner != "usage: /context" {
		t.Fatalf("context usage error = %q", model.errorBanner)
	}
}

func TestReportedContextTotalNormalizesEstimatedCategories(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 10_000
	model.usage.InputTokens = 500
	model.usage.ContextReported = true
	model.contextProfile = app.ContextProfile{Source: "request", Estimated: true, Contributions: []app.ContextContribution{
		{Category: app.ContextCategoryCore, Name: "azem.core_instructions", Tokens: 100},
		{Category: app.ContextCategorySkills, Name: "demo", Tokens: 900},
	}}

	report := ansi.Strip(strings.Join(model.contextReportLines(), "\n"))
	for _, wanted := range []string{"500 / 10K", "Core instructions  ~50", "Skills  ~450", "Provider-reported total"} {
		if !strings.Contains(report, wanted) {
			t.Fatalf("normalized context report missing %q:\n%s", wanted, report)
		}
	}
}

func TestContextMetricsSaturateMalformedProviderTotals(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 10_000
	model.usage.InputTokens = int(^uint(0) >> 1)
	model.usage.OutputTokens = 1
	if got, want := model.contextMetrics().used, int(^uint(0)>>1); got != want {
		t.Fatalf("used context = %d, want saturated %d", got, want)
	}
}

func TestEstimatedUsageCalibrationKeepsEstimateState(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 10_000
	model.contextProfile = app.ContextProfile{Estimated: true, Contributions: []app.ContextContribution{{
		Category: app.ContextCategoryConversation, Name: "before-compaction", Tokens: 500,
	}}}
	model.updateUsage(map[string]string{"inputTokens": "200", "outputTokens": "0", "cacheStatus": "pending"})
	metrics := model.contextMetrics()
	if metrics.used != 200 || metrics.reported || !metrics.estimated || !metrics.calibrated {
		t.Fatalf("estimated context metrics = %+v", metrics)
	}
	report := ansi.Strip(strings.Join(model.contextReportLines(), "\n"))
	if !strings.Contains(report, "~200 / 10K") || strings.Contains(report, "Provider-reported total") {
		t.Fatalf("estimated context report = %q", report)
	}
}

func TestContextContributionLabelsStripControlsAndBoundWidth(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	label := model.contextContributionLabel(app.ContextContribution{
		Name: "tool_result:\x1b[31mserver\n" + strings.Repeat("x", 200),
	})
	if strings.ContainsAny(label, "\x1b\n\r") {
		t.Fatalf("unsafe context label = %q", label)
	}
	if width := ansi.StringWidth(label); width > 96 {
		t.Fatalf("context label width = %d, want <= 96", width)
	}
}

func TestFailedContextProfileClearsStaleContributions(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.contextProfile = app.ContextProfile{Contributions: []app.ContextContribution{{
		Category: app.ContextCategoryConversation, Name: "stale", Tokens: 100,
	}}}
	model.applyEvent(app.Event{Kind: app.EventContextProfile, State: "failed", Text: "refresh failed"})
	if len(model.contextProfile.Contributions) != 0 || model.contextProfileError != "refresh failed" {
		t.Fatalf("failed profile state = %+v, error %q", model.contextProfile, model.contextProfileError)
	}
}

func TestRunStartClearsPreviousRequestProfile(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.contextProfile = app.ContextProfile{
		Source: "request", ReportedInputTokens: 100,
		Contributions: []app.ContextContribution{{Category: app.ContextCategoryConversation, Name: "stale", Tokens: 100}},
	}
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: model.sessionID, RunID: "next-run"})
	if len(model.contextProfile.Contributions) != 0 || model.contextProfile.ReportedInputTokens != 0 {
		t.Fatalf("run start retained stale context profile: %+v", model.contextProfile)
	}
}

func TestTeamContextProfileUsesItsReportedTotal(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "team")
	model.usage.ContextLimit = 10_000
	model.contextProfile = app.ContextProfile{
		Source: "team_request", Estimated: true, ReportedInputTokens: 800, ReportedOutputTokens: 50,
		Contributions: []app.ContextContribution{{Category: app.ContextCategoryCore, Name: "team", Tokens: 400}},
	}
	metrics := model.contextMetrics()
	if metrics.used != 850 || !metrics.reported || metrics.estimated {
		t.Fatalf("team context metrics = %+v", metrics)
	}
}

func TestRestoredReportedUsageMarksContextAsReported(t *testing.T) {
	model := NewModel(inertRuntime{}, ".", "chatgpt", "gpt-test", "high", "single")
	model.restoreUsage(`{"inputTokens":100,"currentTurnMainReported":true}`)
	if !model.usage.ContextReported {
		t.Fatalf("restored usage did not retain reported context: %+v", model.usage)
	}
}

func TestProviderAndModelChangesClearContextProfile(t *testing.T) {
	stale := app.ContextProfile{
		Source: "request", ReportedInputTokens: 100,
		Contributions: []app.ContextContribution{{Category: app.ContextCategoryConversation, Name: "stale", Tokens: 100}},
	}
	model := NewModel(inertRuntime{}, ".", "chatgpt", "first", "high", "single")
	model.models = []ModelChoice{{ID: "first", ContextWindow: 10_000}, {ID: "second", ContextWindow: 20_000}}
	model.contextProfile = stale
	model.selectModel("second")
	if len(model.contextProfile.Contributions) != 0 {
		t.Fatalf("model change retained context profile: %+v", model.contextProfile)
	}

	model.contextProfile = stale
	model.modelsByProvider = map[string][]ModelChoice{"grok": {{ID: "grok-model", ContextWindow: 30_000}}}
	model.switchProvider("grok")
	if len(model.contextProfile.Contributions) != 0 {
		t.Fatalf("provider change retained context profile: %+v", model.contextProfile)
	}
}
