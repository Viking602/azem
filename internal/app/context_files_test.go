package app

import (
	"context"
	"encoding/json"
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
	harness.service.AttachContextFiles(result)
	ruleResult := rules.Result{Rules: []rules.Rule{
		{Name: "sticky", Path: "/workspace/.omp/RULES.md", Provider: "native", Level: "project", Content: "STICKY_RULE_SENTINEL", AlwaysApply: true},
		{Name: "go-domain", Path: "/workspace/.cursor/rules/go.mdc", Provider: "cursor", Level: "project", Content: "GO_DOMAIN_SENTINEL", Description: "Rules for Go", Globs: []string{"**/*.go"}},
	}}
	harness.service.AttachRules(ruleResult)
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "context-files", Prompt: "read context", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	var request struct {
		Instructions string `json:"instructions"`
		Input        any    `json:"input"`
	}
	if err := json.Unmarshal([]byte(captured), &request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.Instructions, "CONTEXT_FILE_SENTINEL") || !strings.Contains(request.Instructions, "<repo-rules>") ||
		!strings.Contains(request.Instructions, "STICKY_RULE_SENTINEL") || !strings.Contains(request.Instructions, "go-domain (**/*.go): Rules for Go") ||
		strings.Contains(request.Instructions, "GO_DOMAIN_SENTINEL") || strings.Contains(captured, "[Trusted private hook context]\\n<repo-rules>") {
		t.Fatalf("context files and rules were not part of static instructions: %s", request.Instructions)
	}
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "context-files")
	if err != nil {
		t.Fatal(err)
	}
	projectInstructions := contextfiles.Render(result) + "\n\n" + rules.Render(ruleResult, contextfiles.Render(result))
	_, expected := turnInstructionsWithProject(false, projectInstructions)
	if projection.ModelHistory.InstructionFingerprint != expected || projection.ModelHistory.StaticPrefixHash == mainInstructionFingerprint {
		t.Fatalf("context instruction identity = %#v, want %s", projection.ModelHistory, expected)
	}
}
