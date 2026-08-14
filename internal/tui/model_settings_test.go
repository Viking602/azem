package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
)

func TestReasoningPickerUsesSelectedModelLevelsAndConfiguresTurn(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "grok", "grok-4.5", "xhigh", "single")
	model.applyEvent(app.Event{
		Kind: app.EventModelCatalog,
		Data: map[string]string{
			"provider": "grok",
			"models":   `[{"id":"grok-4.5","name":"Grok 4.5","supportsReasoning":true,"reasoningLevels":["low","medium","high"],"defaultReasoning":"medium"}]`,
		},
	})
	if model.reasoning != "medium" {
		t.Fatalf("catalog default reasoning = %q, want medium", model.reasoning)
	}

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning || model.overlayOptionCount() != 3 || model.overlayCursor != 1 {
		t.Fatalf("reasoning overlay = overlay:%q count:%d cursor:%d", model.overlay, model.overlayOptionCount(), model.overlayCursor)
	}
	updated, _ = model.updateOverlayKey("up")
	model = updated.(AppModel)
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if model.reasoning != "low" || model.overlay != OverlayNone {
		t.Fatalf("selected reasoning = %q overlay:%q", model.reasoning, model.overlay)
	}

	model.composer.SetValue("use the selected thinking level")
	updated, startCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("reasoning turn command is nil")
	}
	_ = startCmd()
	if runtime.request.Reasoning != "low" {
		t.Fatalf("turn reasoning = %q, want low", runtime.request.Reasoning)
	}

	model.status = "Ready"
	model.runID = ""
	model.errorBanner = ""
	updated, _ = model.executeCommand(Command{Name: "reasoning", Args: []string{"xhigh"}})
	model = updated.(AppModel)
	if !strings.Contains(model.errorBanner, "low|medium|high") {
		t.Fatalf("unsupported reasoning error = %q", model.errorBanner)
	}
	if footer := ansi.Strip(model.renderStatus(120)); !strings.Contains(footer, "Ctrl+R reasoning") {
		t.Fatalf("reasoning shortcut missing from footer: %q", footer)
	}
}

func TestContextTokenCountUsesCompactUnits(t *testing.T) {
	tests := []struct {
		tokens int
		want   string
	}{
		{tokens: 0, want: "0"},
		{tokens: 999, want: "999"},
		{tokens: 1_000, want: "1K"},
		{tokens: 1_500, want: "1.5K"},
		{tokens: 500_000, want: "500K"},
		{tokens: 999_999, want: "999K"},
		{tokens: 1_000_000, want: "1M"},
		{tokens: 1_250_000, want: "1.2M"},
		{tokens: 10_000_000, want: "10M"},
	}
	for _, test := range tests {
		if got := formatTokens(test.tokens); got != test.want {
			t.Errorf("formatTokens(%d) = %q, want %q", test.tokens, got, test.want)
		}
	}
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 500_000
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0 / 500K") {
		t.Fatalf("500K context footer = %q", footer)
	}
	model.usage.ContextLimit = 1_000_000
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0 / 1M") {
		t.Fatalf("1M context footer = %q", footer)
	}
}

func TestModelFooterShowsCatalogContextAndLiveOccupancy(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.applyEvent(app.Event{
		Kind: app.EventModelCatalog,
		Data: map[string]string{
			"provider": "grok",
			"models":   `[{"id":"grok-4.5","name":"Grok 4.5","contextWindow":131072,"supportsTools":true,"supportsReasoning":true}]`,
		},
	})

	header := ansi.Strip(model.renderHeader(120))
	if strings.Contains(header, "grok-4.5") {
		t.Fatalf("header still contains model metadata: %q", header)
	}
	footer := ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"MODEL grok/grok-4.5 · THINK high", "CTX [", "0 / 131K", "0.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("initial model footer missing %q: %q", wanted, footer)
		}
	}

	model.updateUsage(map[string]string{
		"inputTokens":  "30000",
		"outputTokens": "2768",
	})
	footer = ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"32K / 131K", "25.0%", "■"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("occupied model footer missing %q: %q", wanted, footer)
		}
	}
}

func TestModelFooterUpdatesFromProviderContextUsageEvent(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.selectModels([]ModelChoice{{ID: "gpt-5.6-sol", ContextWindow: 272_000}})
	model.status = "Running"
	model.runID = "run-1"

	model.applyEvent(app.Event{
		Kind: app.EventContextUsage, SessionID: model.sessionID, RunID: "run-1",
		Data: map[string]string{
			"inputTokens": "68000", "cachedInputTokens": "34000", "outputTokens": "4000", "totalTokens": "72000", "cacheStatus": "reported",
		},
	})

	footer := ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"72K / 272K", "26.5%", "CACHE 34K/68K", "50.0%", "■"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("context usage footer missing %q: %q", wanted, footer)
		}
	}

	model.applyEvent(app.Event{Kind: app.EventContextUsage, SessionID: model.sessionID, RunID: "run-1", State: "reported"})
	if footer = ansi.Strip(model.renderModelStatus(120)); !strings.Contains(footer, "72K / 272K") {
		t.Fatalf("missing provider usage reset estimated occupancy: %q", footer)
	}
}

func TestContextCacheHitRateStatesAndBounds(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000

	model.updateUsage(map[string]string{"inputTokens": "100", "outputTokens": "20", "cacheStatus": "pending"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "CACHE --") {
		t.Fatalf("pending cache footer = %q", footer)
	}
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "0", "outputTokens": "20", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "CACHE 0/100 · 0.0%") {
		t.Fatalf("zero-hit cache footer = %q", footer)
	}

	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "150", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "100.0%") {
		t.Fatalf("over-reported cache footer = %q", footer)
	}
	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "-5", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0.0%") {
		t.Fatalf("negative cache footer = %q", footer)
	}

	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "50", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(40)); !strings.Contains(footer, "CACHE 50.0%") {
		t.Fatalf("compact cache footer = %q", footer)
	}
	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "80", "outputTokens": "0", "cacheStatus": "pending"})
	model.updateUsage(map[string]string{})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "80 / 1K") || !strings.Contains(footer, "CACHE --") {
		t.Fatalf("omitted cache usage footer = %q", footer)
	}
}

func TestContextCacheHitRateAccumulatesModelCallsWithinTurn(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000

	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "80", "outputTokens": "20", "cacheStatus": "reported"})
	model.updateUsage(map[string]string{"inputTokens": "150", "outputTokens": "0", "cacheStatus": "pending"})
	model.updateUsage(map[string]string{"inputTokens": "150", "cachedInputTokens": "0", "outputTokens": "30", "cacheStatus": "reported"})

	footer := ansi.Strip(model.renderContextUsage(120))
	for _, wanted := range []string{"180 / 1K", "CACHE 80/250", "32.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("turn cache footer missing %q: %q", wanted, footer)
		}
	}
}

func TestSubagentCacheUsageDoesNotEnterMainContextKernel(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000
	model.updateUsage(map[string]string{
		"inputTokens": "100", "cachedInputTokens": "20", "outputTokens": "10", "cacheStatus": "reported",
	})
	model.updateUsage(map[string]string{
		"inputTokens": "50", "cachedInputTokens": "40", "outputTokens": "5",
		"cacheStatus": "reported", "aggregateOnly": "true", "requestKind": "subagent",
	})
	if model.usage.InputTokens != 100 || model.usage.OutputTokens != 10 {
		t.Fatalf("subagent usage replaced main context occupancy: %+v", model.usage)
	}
	if model.usage.CacheInputTokens != 100 || model.usage.CachedInputTokens != 20 {
		t.Fatalf("subagent cache leaked into main kernel: %+v", model.usage)
	}
	if model.usage.SubagentInput != 50 || model.usage.SubagentRequests != 1 {
		t.Fatalf("subagent usage was not tracked separately: %+v", model.usage)
	}
	footer := ansi.Strip(model.renderContextUsage(200))
	if !strings.Contains(footer, "20.0%") {
		t.Fatalf("main cache footer missing 20.0%%: %q", footer)
	}
	if strings.Contains(footer, "ALL") {
		t.Fatalf("subagent usage appeared in main footer: %q", footer)
	}
}

func TestDetailedUsageShowsReasoningCompactionAndTransport(t *testing.T) {
	// ChatGPT uses write-token cache semantics; W counters should surface in /status.
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 500_000
	model.updateUsage(map[string]string{
		"inputTokens": "100000", "cachedInputTokens": "60000", "uncachedInputTokens": "40000", "outputTokens": "5000",
		"cacheWriteTokens": "10000", "cacheStatus": "reported", "requestKind": "main", "transport": "codex-responses",
		"cacheModel": "write-tokens", "provider": "chatgpt",
	})
	model.updateUsage(map[string]string{"reasoningTokens": "3000", "requestKind": "main", "aggregateOnly": "true"})
	model.updateUsage(map[string]string{
		"inputTokens": "20000", "cachedInputTokens": "5000", "uncachedInputTokens": "15000", "cacheWriteTokens": "2000", "outputTokens": "2000", "reasoningTokens": "500",
		"cacheStatus": "reported", "requestKind": "compaction", "aggregateOnly": "true", "transport": "codex-responses",
	})
	model.updateUsage(map[string]string{
		"inputTokens": "30000", "cachedInputTokens": "18000", "uncachedInputTokens": "12000", "cacheWriteTokens": "1000", "outputTokens": "4000", "reasoningTokens": "700",
		"cacheStatus": "reported", "requestKind": "team", "aggregateOnly": "true", "transport": "codex-responses",
	})
	// Dense diagnostics belong in /status, not the footer strip.
	footer := ansi.Strip(model.renderContextUsage(320))
	for _, unwanted := range []string{"U 40K", "CMP ", "TEAM ", "codex-responses"} {
		if strings.Contains(footer, unwanted) {
			t.Fatalf("footer still leaks diagnostic %q: %q", unwanted, footer)
		}
	}
	report := strings.Join(model.statusReportLines(), "\n")
	for _, wanted := range []string{
		"Cache model: write-tokens (cached + cache write)",
		"Uncached input (U): 40K",
		"Cache write (W): 10K main / 13K all",
		"Reasoning tokens (R): 3K",
		"Compaction (CMP): 20K in / 2K out",
		"U 15K",
		"W 2K",
		"R 500",
		"Team usage (TEAM): 30K in / 4K out",
		"cache 60%",
		"U 12K",
		"W 1K",
		"R 700",
		"Last request kind: team",
		"Transport: codex-responses",
	} {
		if !strings.Contains(report, wanted) {
			t.Fatalf("status report missing %q:\n%s", wanted, report)
		}
	}
}

func TestGrokAutomaticCacheHidesWriteCounters(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.usage.ContextLimit = 500_000
	model.updateUsage(map[string]string{
		"inputTokens": "100000", "cachedInputTokens": "60000", "uncachedInputTokens": "40000", "outputTokens": "5000",
		"cacheWriteTokens": "10000", "cacheStatus": "reported", "requestKind": "main", "transport": "xai-responses",
		"cacheModel": "automatic", "provider": "grok",
	})
	if model.usage.CacheWriteTokens != 0 || model.showsCacheWrite() {
		t.Fatalf("automatic cache still kept write counters: %+v", model.usage)
	}
	report := strings.Join(model.statusReportLines(), "\n")
	if strings.Contains(report, "Cache write") || strings.Contains(report, "(W):") {
		t.Fatalf("automatic cache still shows write diagnostics:\n%s", report)
	}
	if !strings.Contains(report, "automatic (hits via cached tokens; write N/A)") {
		t.Fatalf("status report missing automatic cache model:\n%s", report)
	}
}

func TestFactSnapshotPreservesExplicitZeroCacheWriteTelemetry(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.updateUsage(map[string]string{
		"factSnapshot": "true", "usageSnapshot": `{"cacheWriteReported":true,"lastProvider":"chatgpt"}`,
		"requestKind": "review", "provider": "chatgpt", "model": "gpt-5.6-luna",
		"transport": "chatgpt-codex-responses", "cacheModel": "write-tokens",
	})
	if !model.usage.CacheWriteReported || model.usage.CacheModel != "write-tokens" || model.usage.LastRequestKind != "review" || model.usage.LastModel != "gpt-5.6-luna" {
		t.Fatalf("fact snapshot metadata=%+v", model.usage)
	}
	report := strings.Join(model.statusReportLines(), "\n")
	if !strings.Contains(report, "Cache write (W): 0") || strings.Contains(report, "Cache write (W): N/A") {
		t.Fatalf("explicit zero cache write was not distinguished from missing telemetry:\n%s", report)
	}
}

func TestStatusReportShowsActiveShellOwnerAndProcess(t *testing.T) {
	runtime := &recordedRuntime{shells: []agentservice.ShellExecutionSnapshot{{
		AgentID: "background-reviewer", ToolCallID: "shell-17", PID: 123, PGID: 123,
		OutputBytes: 4096, CommandHash: "1234567890abcdef",
	}}}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	report := strings.Join(model.statusReportLines(), "\n")
	for _, want := range []string{"Shell processes", "background-reviewer", "shell-17", "pid 123 / pgid 123", "4096 bytes", "1234567890ab"} {
		if !strings.Contains(report, want) {
			t.Fatalf("status report missing %q:\n%s", want, report)
		}
	}
}

func TestStatusCommandOpensDiagnosticsOverlay(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single", "session-status")
	model.status = "Ready"
	model.usage.ContextLimit = 500_000
	model.usage.InputTokens = 153_000
	model.usage.UncachedInputTokens = 339
	model.usage.ReasoningTokens = 8
	model.usage.LastRequestKind = "main"
	model.usage.LastTransport = "xai-responses"

	command, ok, err := ParseCommand("/status")
	if err != nil || !ok || command.Name != "status" {
		t.Fatalf("ParseCommand(/status) = %#v ok=%v err=%v", command, ok, err)
	}
	updated, _ := model.executeCommand(command)
	model = updated.(AppModel)
	if model.overlay != OverlayStatus {
		t.Fatalf("overlay = %q, want status", model.overlay)
	}
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"RUNTIME STATUS", "Uncached input (U): 339", "Reasoning tokens (R): 8", "Last request kind: main", "Transport: xai-responses", "grok-4.5"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("status overlay missing %q:\n%s", wanted, content)
		}
	}

	updated, _ = model.executeCommand(Command{Name: "status", Args: []string{"extra"}})
	model = updated.(AppModel)
	if model.errorBanner != "usage: /status" {
		t.Fatalf("status usage error = %q", model.errorBanner)
	}
}

func TestModelSelectionUpdatesCatalogContextAndResetsOccupancy(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "first", "high", "single")
	model.selectModels([]ModelChoice{
		{ID: "first", ContextWindow: 131_072},
		{ID: "second", ContextWindow: 200_000},
		{ID: "million", ContextWindow: 1_000_000},
	})
	model.updateUsage(map[string]string{"inputTokens": "1000", "outputTokens": "200"})
	model.selectModel("second")

	if model.model != "second" || model.usage.ContextLimit != 200_000 {
		t.Fatalf("selected model context = model:%q limit:%d", model.model, model.usage.ContextLimit)
	}
	if model.usage.InputTokens != 0 || model.usage.OutputTokens != 0 {
		t.Fatalf("selected model retained occupancy: %+v", model.usage)
	}
	model.overlay = OverlayModel
	options := model.overlayOptions()
	if len(options) != 3 || !strings.Contains(options[1].Detail, "200K context") || !strings.Contains(options[2].Detail, "1M context") {
		t.Fatalf("model picker context detail = %+v", options)
	}
}

func TestModelOverlaySearchFiltersClearsAndSelects(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {
			{ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol", ContextWindow: 272_000},
		},
		"grok": {
			{ID: "grok-4.3", Name: "Grok 4.3", ContextWindow: 1_000_000},
			{ID: "grok-4.5", Name: "Grok 4.5", ContextWindow: 500_000},
		},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.openOverlay(OverlayModel)

	for _, key := range "grok 4.5" {
		updated, _ := model.updateKey(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	options := model.overlayOptions()
	if len(options) != 1 || options[0].Label != "Grok 4.5" {
		t.Fatalf("filtered model options = %+v", options)
	}
	rendered := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{"SEARCH", "grok 4.5", "Grok 4.5"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("search overlay missing %q:\n%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "GPT 5.6 Sol") {
		t.Fatalf("search overlay retained a non-match:\n%s", rendered)
	}

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(AppModel)
	if model.overlay != OverlayModel || len(model.overlayOptions()) != 3 {
		t.Fatalf("first Esc did not clear search: overlay:%q options:%+v", model.overlay, model.overlayOptions())
	}
	for _, key := range "grok 4.5" {
		updated, _ = model.updateKey(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning || model.pendingSessionModel == nil {
		t.Fatalf("searched model should chain to reasoning: overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-5.6-sol" {
		t.Fatalf("search selection applied before reasoning: provider:%q model:%q", model.provider, model.model)
	}
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.provider != "grok" || model.model != "grok-4.5" {
		t.Fatalf("searched model selection = overlay:%q provider:%q model:%q", model.overlay, model.provider, model.model)
	}
}

func TestModelRoutingCommandRendersConfiguredAndInheritedRoutes(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	updated, cmd := model.executeCommand(Command{Name: "model-routing"})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("/model-routing did not start a list action")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionListModelRoutes {
		t.Fatalf("model routing list action = %#v", runtime.actions)
	}

	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"subagent_max_concurrency": "2"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "title", Label: "Title"},
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("grok", "grok-4.5", "low")},
	}})
	if model.overlay != OverlayModelRoutes || len(model.overlayOptions()) != 4 {
		t.Fatalf("model routes overlay = %q options=%#v", model.overlay, model.overlayOptions())
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	for _, wanted := range []string{"MODEL ROUTING", "Session title", "Plan model", "Compaction", "Inherit from active agent", "explore", "grok/grok-4.5/low"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("model routes missing %q:\n%s", wanted, rendered)
		}
	}
}

func settingsMenuModel(t *testing.T) (AppModel, *recordedRuntime) {
	t.Helper()
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"grok": {{ID: "grok-fast", SupportsReasoning: false}},
	}
	model.openOverlay(OverlayCommand)
	options := model.overlayOptions()
	if len(options) == 0 {
		t.Fatalf("settings menu entry = %#v", options)
	}
	if options[0].Label != "Settings" {
		t.Fatalf("settings menu label = %q", options[0].Label)
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings menu did not load routes")
	}
	if model.overlayPurpose != "settings" {
		t.Fatalf("settings purpose = %q", model.overlayPurpose)
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 {
		t.Fatalf("settings list action = %#v", runtime.actions)
	}
	if runtime.actions[0].Kind != ActionListModelRoutes {
		t.Fatalf("settings list action kind = %q", runtime.actions[0].Kind)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
	}})
	return model, runtime
}

func settingsEntryCursor(t *testing.T, model AppModel, kind settingsEntryKind, section string, routeIndex int) int {
	t.Helper()
	for index, entry := range model.settingsEntries() {
		if entry.Kind == kind && entry.Section == section && (kind != settingsEntryRoute || entry.RouteIndex == routeIndex) {
			return index
		}
	}
	t.Fatalf("settings entry kind=%d section=%q route=%d not found", kind, section, routeIndex)
	return 0
}

func expandSettingsEntry(t *testing.T, model AppModel, kind settingsEntryKind, section string, routeIndex int) AppModel {
	t.Helper()
	model.overlayCursor = settingsEntryCursor(t, model, kind, section, routeIndex)
	entry := model.settingsEntries()[model.overlayCursor]
	updated, cmd := model.updateOverlayKey("right")
	model = updated.(AppModel)
	if cmd != nil || !model.settingsExpanded[entry.Key] {
		t.Fatalf("settings entry %q did not expand: expanded=%v cmd=%v", entry.Key, model.settingsExpanded, cmd != nil)
	}
	return model
}

func TestSettingsMenuRendersFunctionalCategoriesAndRoleModels(t *testing.T) {
	model, _ := settingsMenuModel(t)
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
		{Scope: "subagent", Role: "plan", Label: "Produce a decision-complete implementation plan without changing the workspace."},
		{Scope: "subagent", Role: "review", Label: "Review a delegated change for requirement, correctness, and regression risks without editing."},
		{Scope: "subagent", Role: "verify", Label: "Run governed checks without editing and report exact outcomes."},
		{Scope: "subagent", Role: "worker", Label: "Implement one scoped coding task end-to-end and return verified evidence."},
	}})
	options := model.overlayOptions()
	if model.overlay != OverlaySettings {
		t.Fatalf("settings overlay = %q", model.overlay)
	}
	if len(options) != 13 {
		t.Fatalf("settings options = %#v", options)
	}
	for _, wanted := range []string{"Role models", "Plan model", "explore", "plan", "review", "verify", "worker", "Subagent runtime", "Max concurrency", "Codex subscription", "Fast mode", "Interface", "Language"} {
		found := false
		for _, option := range options {
			found = found || option.Label == wanted
		}
		if !found {
			t.Fatalf("settings options missing %q: %#v", wanted, options)
		}
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, rendered, "Settings", "[×]", "/ to search settings", "Role models", "Plan model", "Inherit from active agent", "explore", "chatgpt/old-worker/high", "Subagent runtime", "Codex subscription", "Interface")
	if strings.Contains(rendered, "Inspect the workspace") {
		t.Fatalf("collapsed setting leaked its description into the value column:\n%s", rendered)
	}

	model = expandSettingsEntry(t, model, settingsEntryRoute, settingsSectionModels, 1)
	expanded := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, expanded, "▾ explore", "Inspect the workspace")
	updated, _ := model.updateOverlayKey("left")
	model = updated.(AppModel)
	if len(model.settingsExpanded) != 0 {
		t.Fatalf("left did not collapse the focused setting: %v", model.settingsExpanded)
	}
	model = expandSettingsEntry(t, model, settingsEntryRoute, settingsSectionModels, 2)
	model.width, model.height = 100, 24
	descriptionClicked := false
	for row, line := range strings.Split(ansi.Strip(model.renderOverlay(model.width, model.height)), "\n") {
		if !strings.Contains(line, "decision-complete implementation plan") {
			continue
		}
		descriptionClicked = true
		left := strings.IndexRune(line, '│')
		updated, cmd := model.handleOverlayClick(tea.Mouse{X: left + 8, Y: row, Button: tea.MouseLeft})
		model = updated.(AppModel)
		if cmd != nil || model.pendingModelRoute != nil || model.overlay != OverlaySettings {
			t.Fatalf("clicking an expanded description activated its setting: cmd=%v pending=%#v overlay=%q", cmd != nil, model.pendingModelRoute, model.overlay)
		}
		break
	}
	if !descriptionClicked {
		t.Fatal("expanded plan description was not rendered")
	}

	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "Plan model") {
			if !strings.Contains(line, "›") || strings.Index(line, "Inherit from active agent")-strings.Index(line, "Plan model") < 8 {
				t.Fatalf("settings row is not a left-label/right-value layout: %q", line)
			}
			break
		}
	}
	if background := fmt.Sprint(model.theme.OverlaySelected.GetBackground()); background == fmt.Sprint(lipgloss.NewStyle().GetBackground()) {
		t.Fatal("settings selection has no highlighted background")
	}
	for _, viewport := range [][2]int{{40, 12}, {16, 8}, {8, 6}} {
		for row, line := range strings.Split(model.renderOverlay(viewport[0], viewport[1]), "\n") {
			if width := ansi.StringWidth(line); width > viewport[0] {
				t.Fatalf("settings viewport %dx%d row %d width=%d: %q", viewport[0], viewport[1], row, width, ansi.Strip(line))
			}
		}
	}
	if strings.Contains(rendered, "Compaction") {
		t.Fatalf("settings exposed non-subagent route:\n%s", rendered)
	}
	updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: '/', Text: "/"})
	model = updated.(AppModel)
	for _, key := range "explore" {
		updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	filtered := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, filtered, "Role models", "explore")
	if strings.Contains(filtered, "Plan model") || strings.Contains(filtered, "Subagent runtime") || strings.Contains(filtered, "Codex subscription") {
		t.Fatalf("settings search retained non-matching rows:\n%s", filtered)
	}
	updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(AppModel)
	if model.settingsSearch.Value() != "" {
		t.Fatalf("settings search did not clear: %q", model.settingsSearch.Value())
	}
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	assertTextContainsAll(t, ansi.Strip(model.renderOverlay(100, 24)), "设置", "搜索设置", "角色模型", "规划模型", "子代理运行", "Codex 订阅", "界面")
}

func TestSettingsMenuUpdatesPlanModelAndReturns(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryRoute, settingsSectionModels, 0)
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.pendingModelRoute == nil {
		t.Fatal("settings did not select the plan route")
	}
	if model.pendingModelRoute.Entry.Scope != "plan" || model.pendingModelRoute.Entry.Role != "" {
		t.Fatalf("selected route = %#v", model.pendingModelRoute)
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings model selection did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 {
		t.Fatalf("settings model save action = %#v", runtime.actions)
	}
	if runtime.actions[1].Kind != ActionSetModelRoute {
		t.Fatalf("settings model save kind = %q", runtime.actions[1].Kind)
	}
	if runtime.actions[1].Route == nil {
		t.Fatal("settings model save route is nil")
	}
	if runtime.actions[1].Route.Scope != "plan" || runtime.actions[1].Route.Route.Model != "grok-fast" {
		t.Fatalf("settings model save route = %#v", runtime.actions[1].Route)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan", Route: appModelRoute("grok", "grok-fast", "")},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
	}})
	if model.overlay != OverlaySettings {
		t.Fatalf("settings return after save = %q", model.overlay)
	}
}

func TestSettingsMenuReturnsFromLanguageAndResetsToInherit(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryLanguage, settingsSectionInterface, -1)
	languageCursor := model.overlayCursor
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayLanguage {
		t.Fatalf("settings language picker = %q", model.overlay)
	}
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlaySettings {
		t.Fatalf("settings language return = %q", model.overlay)
	}
	if model.overlayCursor != languageCursor {
		t.Fatalf("settings language cursor = %d", model.overlayCursor)
	}

	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryRoute, settingsSectionModels, 0)
	updated, cmd := model.updateOverlayKey("d")
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings inherit reset did not start")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 {
		t.Fatalf("settings reset action = %#v", runtime.actions)
	}
	if runtime.actions[1].Kind != ActionResetModelRoute {
		t.Fatalf("settings reset kind = %q", runtime.actions[1].Kind)
	}
	if runtime.actions[1].Route == nil {
		t.Fatal("settings reset route is nil")
	}
	if runtime.actions[1].Route.Scope != "plan" || runtime.actions[1].Route.Role != "" {
		t.Fatalf("settings reset route = %#v", runtime.actions[1].Route)
	}
}

func TestSettingsMenuUpdatesSubagentConcurrencyAndReturns(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryConcurrency, settingsSectionRuntime, -1)
	settingsCursor := model.overlayCursor
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlaySubagentConcurrency || model.overlayCursor != 1 {
		t.Fatalf("concurrency picker = overlay:%q cursor:%d", model.overlay, model.overlayCursor)
	}
	model.overlayCursor = 5
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("concurrency selection did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionSetSubagentConcurrency || runtime.actions[1].Target != "6" {
		t.Fatalf("concurrency action = %#v", runtime.actions)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"subagent_max_concurrency": "6"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace"},
	}})
	if model.overlay != OverlaySettings || model.subagentConcurrency != 6 || model.overlayCursor != settingsCursor {
		t.Fatalf("settings return = overlay:%q concurrency:%d cursor:%d", model.overlay, model.subagentConcurrency, model.overlayCursor)
	}
}

func TestSettingsMenuTogglesChatGPTFastMode(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryFastMode, settingsSectionCodex, -1)
	settingsCursor := model.overlayCursor
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("fast mode toggle did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionSetChatGPTFastMode || runtime.actions[1].Target != "true" {
		t.Fatalf("fast mode action = %#v", runtime.actions)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"chatgpt_fast_mode": "true"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "subagent", Role: "explore"},
	}})
	if model.overlay != OverlaySettings || !model.chatGPTFastMode || model.overlayCursor != settingsCursor {
		t.Fatalf("fast mode return = overlay:%q enabled:%v cursor:%d", model.overlay, model.chatGPTFastMode, model.overlayCursor)
	}
	collapsed := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, collapsed, "Fast mode", "On")
	if strings.Contains(collapsed, "1.5x") {
		t.Fatalf("collapsed fast mode leaked description:\n%s", collapsed)
	}
	model = expandSettingsEntry(t, model, settingsEntryFastMode, settingsSectionCodex, -1)
	assertTextContainsAll(t, ansi.Strip(model.renderOverlay(100, 24)), "1.5x faster")
}

func TestModelRoutingSelectionDoesNotMutateMainModel(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {{ID: "gpt-main", SupportsReasoning: true, ReasoningLevels: []string{"low", "high"}}},
		"grok":    {{ID: "grok-worker", SupportsReasoning: true, ReasoningLevels: []string{"low", "medium", "high"}}},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.updateUsage(map[string]string{"inputTokens": "120", "outputTokens": "30"})
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "compaction", Label: "Compaction"},
	}})

	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayModel || model.pendingModelRoute == nil {
		t.Fatalf("route editor did not open model picker: overlay=%q pending=%#v", model.overlay, model.pendingModelRoute)
	}
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning {
		t.Fatalf("reasoning picker overlay = %q", model.overlay)
	}
	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "medium" {
			model.overlayCursor = index
		}
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("route reasoning selection did not start save action")
	}
	_ = cmd()
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionSetModelRoute || runtime.actions[0].Route == nil {
		t.Fatalf("route save action = %#v", runtime.actions)
	}
	route := runtime.actions[0].Route
	if route.Scope != "compaction" || route.Route.Provider != "grok" || route.Route.Model != "grok-worker" || route.Route.Reasoning != "medium" {
		t.Fatalf("saved route = %#v", route)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" || model.usage.InputTokens != 120 || model.usage.OutputTokens != 30 {
		t.Fatalf("route editor mutated main selection: provider=%q model=%q reasoning=%q usage=%+v", model.provider, model.model, model.reasoning, model.usage)
	}
}

func TestModelRoutingNoReasoningResetAndEscape(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{"grok": {{ID: "grok-fast", SupportsReasoning: false}}}
	entry := app.ModelRouteEntry{Scope: "subagent", Role: "verify", Label: "Verify changes", Route: appModelRoute("chatgpt", "old", "high")}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{entry}})

	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("non-reasoning model did not save directly")
	}
	_ = cmd()
	if len(runtime.actions) != 1 || runtime.actions[0].Route == nil || runtime.actions[0].Route.Route.Reasoning != "" || runtime.actions[0].Route.Route.Model != "grok-fast" {
		t.Fatalf("non-reasoning save action = %#v", runtime.actions)
	}

	model.actionBusy = false
	model.pendingModelRoute = nil
	model.openOverlay(OverlayModelRoutes)
	updated, resetCmd := model.updateOverlayKey("R")
	model = updated.(AppModel)
	if resetCmd == nil {
		t.Fatal("R did not start reset action")
	}
	_ = resetCmd()
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionResetModelRoute || runtime.actions[1].Route == nil || runtime.actions[1].Route.Role != "verify" {
		t.Fatalf("route reset action = %#v", runtime.actions)
	}

	model.actionBusy = false
	model.pendingModelRoute = &pendingModelRoute{Entry: entry}
	model.openOverlay(OverlayModel)
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayModelRoutes || model.pendingModelRoute != nil {
		t.Fatalf("route child escape = overlay:%q pending:%#v", model.overlay, model.pendingModelRoute)
	}

	model.pendingModelRoute = &pendingModelRoute{Entry: entry, Provider: "grok", Model: "grok-fast"}
	model.overlay = OverlayReasoning
	updated, _ = model.Update(actionResultMsg{Action: Action{Kind: ActionSetModelRoute, Route: &entry}, Err: errors.New("save failed")})
	model = updated.(AppModel)
	if model.overlay != OverlayModelRoutes || model.pendingModelRoute != nil || !strings.Contains(model.errorBanner, "save failed") {
		t.Fatalf("failed route save cleanup = overlay:%q pending:%#v error:%q", model.overlay, model.pendingModelRoute, model.errorBanner)
	}
	model.openOverlay(OverlayModel)
	if model.pendingModelRoute != nil || model.provider != "chatgpt" || model.model != "gpt-main" {
		t.Fatalf("normal model picker was hijacked after route failure: pending=%#v provider=%q model=%q", model.pendingModelRoute, model.provider, model.model)
	}
}

func appModelRoute(provider, model, reasoning string) config.ModelRouteConfig {
	return config.ModelRouteConfig{Provider: provider, Model: model, Reasoning: reasoning}
}

func TestProviderCatalogsSurviveSwitchAndLoginSelectsProvider(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-old", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsTools":true,"supportsReasoning":true}]`,
	}})
	if model.provider != "chatgpt" || model.model != "gpt-5.6" || len(model.models) != 1 {
		t.Fatalf("ChatGPT selection changed while caching Grok: provider=%q model=%q models=%+v", model.provider, model.model, model.models)
	}

	model.applyActionResult(Action{Kind: ActionLogin, Target: "grok:import"})
	if model.provider != "grok" || model.model != "grok-4.20" || len(model.models) != 1 {
		t.Fatalf("Grok login selection = provider:%q model:%q models:%+v", model.provider, model.model, model.models)
	}

	updated, _ := model.executeCommand(Command{Name: "provider", Args: []string{"chatgpt"}})
	model = updated.(AppModel)
	if model.provider != "chatgpt" || model.model != "gpt-5.6" || len(model.models) != 1 {
		t.Fatalf("restored ChatGPT catalog = provider:%q model:%q models:%+v", model.provider, model.model, model.models)
	}
}

func TestModelPickerIncludesAllProviderCatalogs(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-old", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.openOverlay(OverlayModel)

	content := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{"CHATGPT", "GPT 5.6", "GROK", "Grok 4.20"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("grouped model picker missing %q:\n%s", wanted, content)
		}
	}
	if got := model.overlayOptionCount(); got != 2 {
		t.Fatalf("model picker option count = %d, want 2", got)
	}
}

func TestGroupedModelPickerSwitchesProviderOnSelection(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsReasoning":true}]`,
	}})
	model.openOverlay(OverlayModel)

	updated, _ := model.updateOverlayKey("down")
	model = updated.(AppModel)
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)

	if model.provider != "chatgpt" || model.model != "gpt-5.6" {
		t.Fatalf("model applied before reasoning confirm: %s/%s", model.provider, model.model)
	}
	if model.overlay != OverlayReasoning || model.pendingSessionModel == nil {
		t.Fatalf("expected reasoning chain: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
	if model.pendingSessionModel.Provider != "grok" || model.pendingSessionModel.Model != "grok-4.20" {
		t.Fatalf("pending session model = %#v", model.pendingSessionModel)
	}

	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "high" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)

	if model.provider != "grok" || model.model != "grok-4.20" || model.reasoning != "high" {
		t.Fatalf("grouped picker selection = %s/%s/%s, want grok/grok-4.20/high", model.provider, model.model, model.reasoning)
	}
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("picker state after confirm: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
}

func TestModelPickerChainsToReasoningBeforeApplying(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {
			{ID: "gpt-main", SupportsReasoning: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high"},
			{ID: "gpt-plain", SupportsReasoning: false},
		},
		"grok": {
			{ID: "grok-worker", SupportsReasoning: true, ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoning: "medium"},
		},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.updateUsage(map[string]string{"inputTokens": "42", "outputTokens": "7"})

	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning {
		t.Fatalf("overlay after model pick = %q, want reasoning", model.overlay)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" {
		t.Fatalf("selection mutated before confirm: %s/%s/%s", model.provider, model.model, model.reasoning)
	}
	if model.usage.InputTokens != 42 || model.usage.OutputTokens != 7 {
		t.Fatalf("usage cleared before confirm: %+v", model.usage)
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	for _, wanted := range []string{"THINKING LEVEL", "grok/grok-worker", "choose for the next turn", "medium"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("reasoning chain overlay missing %q:\n%s", wanted, rendered)
		}
	}

	// Esc cancels without applying the pending model.
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("esc cleanup = overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" {
		t.Fatalf("esc mutated selection: %s/%s/%s", model.provider, model.model, model.reasoning)
	}

	// Confirm applies provider/model/reasoning together.
	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "low" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.provider != "grok" || model.model != "grok-worker" || model.reasoning != "low" {
		t.Fatalf("confirmed selection = %s/%s/%s", model.provider, model.model, model.reasoning)
	}
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("confirm cleanup = overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.usage.InputTokens != 0 || model.usage.OutputTokens != 0 {
		t.Fatalf("usage should reset on model change: %+v", model.usage)
	}

	model.composer.SetValue("use chained model")
	updated, startCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("turn command is nil")
	}
	_ = startCmd()
	if runtime.request.Provider != "grok" || runtime.request.Model != "grok-worker" || runtime.request.Reasoning != "low" {
		t.Fatalf("turn request = %#v", runtime.request)
	}

	// Models without adjustable reasoning still apply immediately.
	model.status = "Ready"
	model.runID = ""
	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "chatgpt" && entry.Model.ID == "gpt-plain" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("plain model should apply immediately: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-plain" {
		t.Fatalf("plain model selection = %s/%s", model.provider, model.model)
	}
}
