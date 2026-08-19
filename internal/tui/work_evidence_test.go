package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
)

func TestAgentViewProjectsEvidenceStatus(t *testing.T) {
	t.Parallel()
	view := agentViewFromPayload("child", "completed", "done", &app.AgentStatePayload{Type: "review", EvidenceStatus: "stale"})
	if view.EvidenceStatus != "stale" || view.State != "completed" {
		t.Fatalf("agent view = %+v", view)
	}
}

func TestSparseAgentStatePreservesEvidenceStatus(t *testing.T) {
	t.Parallel()
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.updateAgent(app.Event{
		Kind: app.EventAgentState, AgentID: "child", State: "running",
		Agent: &app.AgentStatePayload{Type: "review", EvidenceStatus: "verified"},
	})
	model.updateAgent(app.Event{
		Kind: app.EventAgentState, AgentID: "child", State: "running",
		Agent: &app.AgentStatePayload{Type: "review", Activity: "coding.search"},
	})
	if len(model.agents) != 1 || model.agents[0].EvidenceStatus != "verified" {
		t.Fatalf("sparse event erased evidence status: %#v", model.agents)
	}
}

func TestAgentEvidenceStatusRendersLocalizedLabels(t *testing.T) {
	t.Parallel()
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.agents = []AgentView{{ID: "child", Role: "review", State: "completed", EvidenceStatus: "verified"}}
	model.overlay = OverlayAgents
	english := ansi.Strip(model.renderOverlay(120, 20))
	if !strings.Contains(english, "Verified evidence") {
		t.Fatalf("agent list omitted verified evidence status: %s", english)
	}
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	chinese := ansi.Strip(model.renderOverlay(120, 20))
	if !strings.Contains(chinese, "证据已验证") {
		t.Fatalf("agent list omitted localized evidence status: %s", chinese)
	}
}
