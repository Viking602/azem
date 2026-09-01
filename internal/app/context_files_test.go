package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/contextfiles"
	"github.com/Viking602/azem/internal/rules"
)

func TestDiscoveredContextFilesJoinStaticInstructionIdentity(t *testing.T) {
	var captured string
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		if call != 1 {
			t.Errorf("unexpected context-file provider call %d", call)
		}
		captured = body
		writeProviderText(writer, "context-files", "context loaded")
	})
	result := contextfiles.Result{Files: []contextfiles.File{{
		Path: "/workspace/AGENTS.md", Provider: "agents-md", Level: "project", Content: "CONTEXT_FILE_SENTINEL",
	}}}
	ruleResult := rules.Result{Rules: []rules.Rule{
		{Name: "sticky", Path: "/workspace/.omp/RULES.md", Provider: "native", Level: "project", Content: "STICKY_RULE_SENTINEL", AlwaysApply: true},
		{Name: "go-domain", Path: "/workspace/.cursor/rules/go.mdc", Provider: "cursor", Level: "project", Content: "GO_DOMAIN_SENTINEL", Description: "Rules for Go", Globs: []string{"**/*.go"}},
	}}
	loads := 0
	harness.service.AttachProjectContextLoader(func(context.Context) (string, []string, error) {
		loads++
		rendered := contextfiles.Render(result)
		return rendered + "\n\n" + rules.Render(ruleResult, rendered), nil, nil
	})
	if loads != 0 {
		t.Fatal("project context loaded before a turn started")
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "context-files", Prompt: "read context", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	requireAppTestNoError(t, err)
	waitForProviderRun(t, harness.service, runID)
	if loads != 1 {
		t.Fatalf("project context loads = %d, want 1", loads)
	}
	var request struct {
		Instructions string `json:"instructions"`
		Input        any    `json:"input"`
	}
	requireAppTestNoError(t, json.Unmarshal([]byte(captured), &request))
	requireStaticProjectInstructions(t, request.Instructions, captured)
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "context-files")
	requireAppTestNoError(t, err)
	projectInstructions := contextfiles.Render(result) + "\n\n" + rules.Render(ruleResult, contextfiles.Render(result))
	_, expected := turnInstructionsWithProject(false, projectInstructions)
	if projection.ModelHistory.InstructionFingerprint != expected || projection.ModelHistory.StaticPrefixHash == mainInstructionFingerprint {
		t.Fatalf("context instruction identity = %#v, want %s", projection.ModelHistory, expected)
	}
}

func requireStaticProjectInstructions(t *testing.T, instructions, request string) {
	t.Helper()
	for _, expected := range []string{"CONTEXT_FILE_SENTINEL", "<repo-rules>", "STICKY_RULE_SENTINEL", "go-domain (**/*.go): Rules for Go"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("static instructions omit %q: %s", expected, instructions)
		}
	}
	for _, unexpected := range []string{"GO_DOMAIN_SENTINEL", "[Trusted private hook context]\\n<repo-rules>"} {
		if strings.Contains(request, unexpected) {
			t.Fatalf("static instructions include %q: %s", unexpected, instructions)
		}
	}
}

func TestProjectContextLoadFailureReleasesStartingRun(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(int, string, http.ResponseWriter) {
		t.Error("provider must not run when project context fails")
	})
	harness.service.AttachProjectContextLoader(func(context.Context) (string, []string, error) {
		return "", nil, errors.New("context unavailable")
	})
	if _, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "context-error", Prompt: "read context", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"}); err == nil || !strings.Contains(err.Error(), "context unavailable") {
		t.Fatalf("start error = %v", err)
	}
	if runID, _ := harness.service.ActiveRun(); runID != "" {
		t.Fatalf("active run after context failure = %q", runID)
	}
}
