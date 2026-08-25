package usageview

import (
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestTextRendersTotalsCacheCalendarModelsKindsAndSkills(t *testing.T) {
	report := session.UsageReport{
		Scope: session.UsageScopeAll, From: "2026-01-01", To: "2026-01-10", Requests: 12, Sessions: 3, Runs: 4,
		TotalTokens: 1_250_000, InputTokens: 1_000_000, OutputTokens: 200_000, ReasoningTokens: 50_000,
		ReportedInput: 1_000_000, CacheReadTokens: 750_000, CacheReported: true,
		PeakDay: "2026-01-09", PeakDayTokens: 900_000, CurrentStreak: 2, LongestStreak: 5, LongestRunMS: 65_000,
		Days:   []session.UsageDay{{Date: "2026-01-01", Tokens: 100}, {Date: "2026-01-09", Tokens: 900_000}},
		Models: []session.UsageModelRow{{Provider: "openai", Model: "gpt-test", Tokens: 1_250_000, Requests: 12}},
		Kinds:  []session.UsageKindRow{{Kind: "main", Tokens: 1_000_000, Requests: 10}},
		Skills: []session.UsageSkillRow{{Name: "verify", Activations: 3}},
	}
	output := Text(report)
	for _, required := range []string{"All projects", "1.2M", "75.0%", "2026-01-01", "2026-01-10", "openai/gpt-test", "main", "verify", "1m5s"} {
		if !strings.Contains(output, required) {
			t.Fatalf("usage output missing %q:\n%s", required, output)
		}
	}
}

func TestTextExplainsEmptyAndUnreportedUsage(t *testing.T) {
	output := Text(session.UsageReport{Scope: session.UsageScopeProject, Workspace: "/tmp/project", From: "2026-01-01", To: "2026-01-02", Empty: true})
	if !strings.Contains(output, "No completed provider requests") || !strings.Contains(output, "/tmp/project") {
		t.Fatalf("empty usage=%s", output)
	}
}
