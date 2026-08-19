package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

func TestProviderRuntimeLazySkillActivation(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			if !strings.Contains(body, "demo catalog") || !strings.Contains(body, "demo") {
				t.Errorf("first request omitted the skill catalog: %s", body)
			}
			if strings.Contains(body, "DEMO_BODY_SECRET") {
				t.Errorf("first request eagerly disclosed the skill body: %s", body)
			}
			writeProviderToolCall(writer, "response-1", "activate-1", "hydaelyn_activate_skill", `{"name":"demo"}`)
		case 2:
			if !strings.Contains(body, "DEMO_BODY_SECRET") {
				t.Errorf("second request omitted the activated skill body: %s", body)
			}
			writeProviderText(writer, "response-2", "activated")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "response-extra", "unexpected")
		}
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "lazy", Prompt: "inspect parser", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", harness.calls.Load())
	}
}

func TestProviderRuntimeManualSkillActivation(t *testing.T) {
	const prompt = "inspect parser"
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		if call != 1 {
			t.Errorf("unexpected provider call %d", call)
		}
		if !strings.Contains(body, "DEMO_BODY_SECRET") || !strings.Contains(body, prompt) {
			t.Errorf("manual activation request omitted body or prompt: %s", body)
		}
		writeProviderText(writer, "response-manual", "done")
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "manual", Prompt: prompt, Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", harness.calls.Load())
	}
}

func TestProviderRuntimeUsesFixedSkillSnapshot(t *testing.T) {
	var harness skillRuntimeHarness
	harness = newSkillRuntimeHarness(t, "---\nname: demo\ndescription: old catalog\n---\nOLD_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			updated := "---\nname: demo\ndescription: new catalog\n---\nNEW_BODY_SECRET\n"
			if err := os.WriteFile(harness.definitionPath, []byte(updated), 0o600); err != nil {
				t.Errorf("update skill: %v", err)
			}
			if err := harness.catalog.Reload(); err != nil {
				t.Errorf("reload skills: %v", err)
			}
			writeProviderToolCall(writer, "response-old-1", "activate-old", "hydaelyn_activate_skill", `{"name":"demo"}`)
		case 2:
			if !strings.Contains(body, "OLD_BODY_SECRET") || strings.Contains(body, "NEW_BODY_SECRET") {
				t.Errorf("running engine did not retain its original snapshot: %s", body)
			}
			writeProviderText(writer, "response-old-2", "old snapshot")
		case 3:
			if !strings.Contains(body, "new catalog") || strings.Contains(body, "NEW_BODY_SECRET") {
				t.Errorf("new engine did not receive the reloaded catalog lazily: %s", body)
			}
			writeProviderText(writer, "response-new", "new snapshot")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "response-extra", "unexpected")
		}
	})
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "fixed-old", Prompt: "use demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	secondRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "fixed-new", Prompt: "inspect demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, secondRun)
	if harness.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", harness.calls.Load())
	}
}

func TestProviderRuntimeSkillResourceRequiresActivation(t *testing.T) {
	const fixture = "REFERENCE_FIXTURE"
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n",
		map[string]string{"reference.txt": fixture},
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				if strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("first request eagerly disclosed the skill body: %s", body)
				}
				writeProviderToolCall(writer, "resource-1", "read-before", "hydaelyn_read_skill_resource", `{"skill":"demo","path":"reference.txt"}`)
			case 2:
				if !strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("manually activated body missing from second request: %s", body)
				}
				writeProviderToolCall(writer, "resource-2", "read-after", "hydaelyn_read_skill_resource", `{"skill":"demo","path":"reference.txt"}`)
			case 3:
				if !strings.Contains(body, fixture) {
					t.Errorf("resource fixture missing from third request: %s", body)
				}
				writeProviderText(writer, "resource-3", "resource read")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "resource-extra", "unexpected")
			}
		},
	)
	blockedRunID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "resource-blocked", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		event, nextErr := harness.service.NextEvent(ctx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if event.RunID != blockedRunID {
			continue
		}
		if event.Kind == EventRunFailed {
			if !strings.Contains(event.Text, `skill "demo" is not active`) {
				t.Fatalf("resource guard error = %q", event.Text)
			}
			break
		}
		if event.Kind == EventRunFinished || event.Kind == EventRunCancelled {
			t.Fatalf("unactivated resource run ended as %s", event.Kind)
		}
	}
	activeRunID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "resource-active", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, activeRunID)
	if harness.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", harness.calls.Load())
	}
}

func TestProviderRuntimeReplaysActivatedSkillsOnLaterTurn(t *testing.T) {
	const fixture = "REFERENCE_FIXTURE"
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n",
		map[string]string{"reference.txt": fixture},
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				writeProviderToolCall(writer, "replay-1", "activate-demo", "hydaelyn_activate_skill", `{"name":"demo"}`)
			case 2:
				if !strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("first turn omitted the activated skill body: %s", body)
				}
				writeProviderText(writer, "replay-2", "activated")
			case 3:
				if !strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("replayed turn omitted the previously activated skill body: %s", body)
				}
				writeProviderToolCall(writer, "replay-3", "read-after-replay", "hydaelyn_read_skill_resource", `{"skill":"demo","path":"reference.txt"}`)
			case 4:
				if !strings.Contains(body, fixture) {
					t.Errorf("replayed resource fixture missing: %s", body)
				}
				writeProviderText(writer, "replay-4", "resource read")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "replay-extra", "unexpected")
			}
		},
	)
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "replay-active", Prompt: "activate demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	secondRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "replay-active", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, secondRun)
	if harness.calls.Load() != 4 {
		t.Fatalf("provider calls = %d, want 4", harness.calls.Load())
	}
}

func TestProviderRuntimeDoesNotReplayDisabledOrDeletedSkills(t *testing.T) {
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n",
		map[string]string{"reference.txt": "REFERENCE_FIXTURE"},
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				writeProviderToolCall(writer, "gone-1", "activate-demo", "hydaelyn_activate_skill", `{"name":"demo"}`)
			case 2:
				writeProviderText(writer, "gone-2", "activated")
			case 3:
				if strings.Count(body, "--- skill: demo ---") != 1 || strings.Contains(body, `"name":"hydaelyn_read_skill_resource"`) {
					t.Errorf("disabled skill remained activated or readable: %s", body)
				}
				writeProviderText(writer, "gone-3", "disabled")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "gone-extra", "unexpected")
			}
		},
	)
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "replay-disabled", Prompt: "activate demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	if err := harness.catalog.UpdateConfig(config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{filepath.Dir(filepath.Dir(harness.definitionPath))},
		Disabled:       []string{"demo"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	blockedRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "replay-disabled", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, blockedRun)
	if harness.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", harness.calls.Load())
	}
}

func TestSessionActivatedSkillNamesIgnoresFailedAndUnknownSkills(t *testing.T) {
	records := []session.ToolRecord{
		{Name: "hydaelyn_activate_skill", State: session.ToolCompleted, Arguments: []byte(`{"name":"demo"}`)},
		{Name: "hydaelyn_activate_skill", State: session.ToolFailed, Arguments: []byte(`{"name":"broken"}`)},
		{Name: "hydaelyn_activate_skill", State: session.ToolCompleted, Structured: []byte(`{"name":"demo"}`)},
		{Name: "coding.read_file", State: session.ToolCompleted, Arguments: []byte(`{"path":"x"}`)},
	}
	if got := sessionActivatedSkillNames(records); !reflect.DeepEqual(got, []string{"demo"}) {
		t.Fatalf("activated=%v", got)
	}
	if got := filterResolvableSkills(nil, []string{"demo"}); got != nil {
		t.Fatalf("nil registry=%v", got)
	}
}

func TestSkillAllowedToolsDoNotBypassApproval(t *testing.T) {
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\nallowed-tools: coding.write_file\n---\nUse the governed file tool when requested.\n",
		nil,
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				writeProviderToolCall(writer, "approval-1", "write-approval", "coding.write_file", `{"path":"approval-marker.txt","content":"skill-approval"}`)
			case 2:
				if !strings.Contains(body, "skill-approval") {
					t.Errorf("approved file output missing from second request: %s", body)
				}
				writeProviderText(writer, "approval-2", "approved")
			case 3:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"approval-read-item\",\"call_id\":\"approval-read\",\"name\":\"coding.read_file\",\"arguments\":\"{\\\"path\\\":\\\"approval-marker.txt\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"approval-diff-item\",\"call_id\":\"approval-diff\",\"name\":\"coding.shell\",\"arguments\":\"{\\\"command\\\":\\\"git diff --check -- approval-marker.txt\\\",\\\"wall_clock_seconds\\\":60}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"approval-3\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n")
			case 4:
				writeProviderText(writer, "approval-4", "approved")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "approval-extra", "unexpected")
			}
		},
	)
	markerPath := filepath.Join(harness.workspace, "approval-marker.txt")
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "allowed-tools", Prompt: "run the approved command", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approved := false
	for {
		event, nextErr := harness.service.NextEvent(ctx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventApprovalRequested:
			switch event.ToolCallID {
			case "write-approval":
				if event.Data["tool"] != "coding.write_file" {
					t.Fatalf("approval event = %+v", event)
				}
				if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
					t.Fatalf("file was created before approval, stat error = %v", err)
				}
				approved = true
			case "approval-diff":
				if !approved || event.Data["tool"] != "coding.shell" {
					t.Fatalf("verification approval event = %+v", event)
				}
			default:
				t.Fatalf("unexpected approval event = %+v", event)
			}
			if err := harness.service.ExecuteAction(context.Background(), Action{
				Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once",
			}); err != nil {
				t.Fatal(err)
			}
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunCancelled:
			t.Fatal("run cancelled")
		case EventRunFinished:
			if !approved {
				t.Fatal("skill allowed-tools bypassed the approval event")
			}
			content, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "skill-approval" {
				t.Fatalf("approved marker = %q", content)
			}
			if harness.calls.Load() != 4 {
				t.Fatalf("provider calls = %d, want 4", harness.calls.Load())
			}
			return
		}
	}
}
