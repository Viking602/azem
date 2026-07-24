package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/multiagent"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPhase3ArtifactToolRoundTripsBinaryPayloadAsBase64(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "binary-artifact.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0xff, 0x00, 0xfe, 'a'}
	artifact, err := sessions.PutArtifact(ctx, "s", "r", "tool_result", payload, "binary")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]string{"artifact_id": artifact.ID})
	result, err := (&contextArtifactDriver{sessionID: "s", store: sessions}).Execute(ctx, tool.Call{ID: "call", Name: contextReadArtifactTool, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("artifact read=%+v err=%v", result, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Content)
	if err != nil || !bytes.Equal(decoded, payload) || !strings.Contains(string(result.Structured), `"encoding":"base64"`) {
		t.Fatalf("binary artifact content=%q structured=%s err=%v", result.Content, result.Structured, err)
	}
}

func TestExecutionCheckpointFactsCaptureTodoToolsAndWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, arguments := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Azem Test"},
	} {
		if _, err := gitOutput(ctx, root, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	tracked := filepath.Join(root, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(ctx, root, "add", "tracked.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(ctx, root, "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("package sample\n\nconst Changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(outside, []byte("TOP-SECRET-MUST-NOT-BE-CAPTURED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string][]byte{}
	manager := turnContext{
		runID: "run-checkpoint",
		todo: session.TodoList{Goal: "ship safely", Revision: 7, Phases: []session.TodoPhase{{
			ID: "build", Items: []session.TodoItem{{ID: "verify", Content: "run tests", Status: session.TodoInProgress}},
		}}},
		captureWorkspace: func(ctx context.Context) (workspaceCheckpointWitness, error) {
			return captureGitWorkspace(ctx, root)
		},
		putArtifact: func(_ context.Context, kind string, payload []byte, _ string) (session.ContextArtifact, error) {
			id := fmt.Sprintf("%s-%d", kind, len(artifacts)+1)
			artifacts[id] = append([]byte(nil), payload...)
			return session.ContextArtifact{ID: id}, nil
		},
	}
	omitted := []message.Message{
		message.NewText(message.RoleUser, "implement the checkpoint"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call-1", Name: "shell", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "call-1", Name: "shell", Content: "ok", IsError: false}),
	}
	checkpoint, err := manager.buildExecutionCheckpointMessage(ctx, nil, omitted, true)
	if err != nil {
		t.Fatal(err)
	}
	facts, ok := parseExecutionCheckpoint(checkpoint)
	if !ok {
		t.Fatalf("invalid checkpoint message: %#v", checkpoint)
	}
	if facts.RunID != "run-checkpoint" || facts.Todo.Revision != 7 || len(facts.Tools) != 1 || facts.Tools[0].ResultSHA256 == "" {
		t.Fatalf("checkpoint facts=%+v", facts)
	}
	if !facts.Workspace.Complete || facts.Workspace.Head == "" || len(facts.Workspace.Files) != 3 {
		t.Fatalf("workspace witness=%+v", facts.Workspace)
	}
	encodedFacts, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	for id, payload := range artifacts {
		if bytes.Contains(payload, []byte("TOP-SECRET-MUST-NOT-BE-CAPTURED")) {
			t.Fatalf("artifact %s captured secret content: %q", id, payload)
		}
	}
	if bytes.Contains(encodedFacts, []byte("TOP-SECRET-MUST-NOT-BE-CAPTURED")) ||
		bytes.Contains(encodedFacts, []byte("const Changed = true")) || bytes.Contains(encodedFacts, []byte("new evidence")) {
		t.Fatalf("workspace witness captured file contents: %s", encodedFacts)
	}
	if len(facts.SourceArtifacts) != 1 || len(artifacts[facts.SourceArtifacts[0].ID]) == 0 {
		t.Fatalf("source evidence=%+v artifacts=%+v", facts.SourceArtifacts, artifacts)
	}
}

func TestRunStepCheckpointPersistsOnlyProtocolCompleteBoundaries(t *testing.T) {
	var saved [][]message.Message
	recorder := &runStepCheckpoint{save: func(_ context.Context, history []message.Message, _ *int64) error {
		saved = append(saved, append([]message.Message(nil), history...))
		return nil
	}}
	base := []message.Message{message.NewText(message.RoleSystem, "rules"), message.NewText(message.RoleUser, "continue")}
	request := hyprovider.Request{Messages: base}
	if err := recorder.BeforeModelCall(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	call := message.ToolCall{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	for _, event := range []hyprovider.Event{
		{Kind: hyprovider.EventToolCall, ToolCall: &call},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonToolUse, ProviderState: json.RawMessage(`[{"id":"provider-turn"}]`)},
	} {
		if err := recorder.OnEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	result := message.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "contents"}
	if err := recorder.Emit(context.Background(), stream.Frame{Kind: stream.FrameToolResult, ToolResult: &result}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordStep(context.Background(), hyagent.Step{Index: 0, Decision: hyagent.StepDecisionContinue}); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("checkpoint saves = %d, want initial boundary and finalized tool turn", len(saved))
	}
	got := saved[1]
	if err := message.ValidateCompleteTurns(got); err != nil {
		t.Fatalf("saved protocol is incomplete: %v\n%#v", err, got)
	}
	if len(got) != 4 || len(got[2].ToolCalls) != 1 || got[3].ToolResult == nil ||
		string(got[2].ProviderState) != `[{"id":"provider-turn"}]` {
		t.Fatalf("saved tool boundary = %#v", got)
	}
}

func TestRunStepCheckpointPersistsFinalAssistantAtFinishDecision(t *testing.T) {
	var saved []message.Message
	recorder := &runStepCheckpoint{save: func(_ context.Context, history []message.Message, _ *int64) error {
		saved = append([]message.Message(nil), history...)
		return nil
	}}
	request := hyprovider.Request{Messages: []message.Message{message.NewText(message.RoleUser, "finish")}}
	if err := recorder.BeforeModelCall(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	_ = recorder.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "done"})
	_ = recorder.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete})
	if err := recorder.RecordStep(context.Background(), hyagent.Step{Decision: hyagent.StepDecisionFinish}); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[1].Role != message.RoleAssistant || saved[1].Text != "done" {
		t.Fatalf("final checkpoint = %#v", saved)
	}
}

func TestSingleRunManifestAcceptsEmptyResolvedSkillSet(t *testing.T) {
	manifest := singleRunManifest{
		Version: 1, Provider: "chatgpt", Model: "model", Reasoning: "minimal",
		ActiveSkills: []string{}, StaticIdentity: "identity", StartedAt: time.Now().UTC(),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSingleRunManifest(string(encoded))
	if err != nil || decoded.ActiveSkills == nil || len(decoded.ActiveSkills) != 0 {
		t.Fatalf("decoded empty-skill manifest=%+v error=%v", decoded, err)
	}
}

func TestRunStepCheckpointPersistsGuardrailRetryContext(t *testing.T) {
	var saved []message.Message
	recorder := &runStepCheckpoint{save: func(_ context.Context, history []message.Message, _ *int64) error {
		saved = append([]message.Message(nil), history...)
		return nil
	}}
	request := hyprovider.Request{Messages: []message.Message{message.NewText(message.RoleUser, "original")}}
	if err := recorder.BeforeModelCall(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	guardrail := checkpointGuardrail{recorder: recorder, inner: hyagent.NewOutputGuardrail("retry", func(context.Context, hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		return hyagent.RetryOutput(message.NewText(message.RoleUser, "late guidance")), nil
	})}
	if _, err := guardrail.Check(context.Background(), hyagent.OutputGuardrailInput{}); err != nil {
		t.Fatal(err)
	}
	_ = recorder.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "rejected"})
	_ = recorder.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete})
	if err := recorder.RecordStep(context.Background(), hyagent.Step{Decision: hyagent.StepDecisionContinue}); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[1].Role != message.RoleUser || saved[1].Text != "late guidance" {
		t.Fatalf("guardrail retry checkpoint=%#v", saved)
	}
}

func TestExecutionCheckpointRefreshHasStableIdentity(t *testing.T) {
	ctx := context.Background()
	artifactWrites := 0
	manager := turnContext{
		runID: "run-stable",
		todo:  session.TodoList{Goal: "continue", Revision: 2},
		putArtifact: func(_ context.Context, _ string, _ []byte, _ string) (session.ContextArtifact, error) {
			artifactWrites++
			return session.ContextArtifact{ID: "source-stable"}, nil
		},
	}
	omitted := []message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call-stable", Name: "read", Arguments: json.RawMessage(`{"path":"stable.go"}`)}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "call-stable", Name: "read", Content: "stable result"}),
	}
	checkpoint, err := manager.buildExecutionCheckpointMessage(ctx, nil, omitted, true)
	if err != nil {
		t.Fatal(err)
	}
	summary := message.NewText(message.RoleAssistant, "stable semantic summary")
	summary.Kind = message.KindCompactionSummary
	summary.Visibility = message.VisibilityPrivate
	history := []message.Message{summary, executionCheckpointPolicyMessage(), checkpoint}
	first, err := manager.refreshExecutionCheckpoint(ctx, history)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.refreshExecutionCheckpoint(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash, secondHash := session.ModelCheckpointHash(first), session.ModelCheckpointHash(second); firstHash == "" || firstHash != secondHash {
		t.Fatalf("checkpoint identity changed across equivalent refreshes: first=%q second=%q", firstHash, secondHash)
	}
	if artifactWrites != 1 {
		t.Fatalf("equivalent refresh persisted %d source artifacts, want 1", artifactWrites)
	}
}

func TestExecutionCheckpointFactsDoNotCrossRunBoundary(t *testing.T) {
	old := executionCheckpointFacts{Version: 1, RunID: "run-old", Tools: []checkpointToolFact{{
		CallID: "call-old", Name: "shell", Outcome: "terminal_success_do_not_replay",
	}}}
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	facts := message.NewText(message.RoleAssistant, executionCheckpointFactsPrefix+string(encoded))
	facts.Kind = message.KindCustom
	facts.Visibility = message.VisibilityPrivate
	facts.Metadata = map[string]string{executionCheckpointMetadataKey: "1"}
	summary := message.NewText(message.RoleAssistant, "preserve semantic history")
	summary.Kind = message.KindCompactionSummary
	history := []message.Message{summary, executionCheckpointPolicyMessage(), facts}

	filtered := checkpointMessagesForRun(history, "run-new")
	if len(filtered) != 1 || filtered[0].Kind != message.KindCompactionSummary {
		t.Fatalf("cross-run checkpoint facts survived: %#v", filtered)
	}
}

func TestWorkspaceEvidenceRejectsOversizedFileWithoutReadingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxWorkspaceFileBytes+1); err != nil {
		t.Fatal(err)
	}
	remaining := int64(maxWorkspaceTotalBytes)
	if _, err := readWorkspaceEvidence(root, "large.bin", &remaining); !errors.Is(err, errWorkspaceEvidenceLimit) {
		t.Fatalf("oversized workspace evidence error=%v", err)
	}
	if remaining != maxWorkspaceTotalBytes {
		t.Fatalf("oversized file consumed evidence budget: %d", remaining)
	}
}

func TestIncompleteWorkspaceWitnessUsesFailClosedPolicy(t *testing.T) {
	savedFacts := executionCheckpointFacts{
		Version: 1, RunID: "run", Workspace: workspaceCheckpointWitness{
			VCS: "git", Head: "old", Complete: false, ErrorCode: "limit_exceeded",
		},
	}
	encoded, err := json.Marshal(savedFacts)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := message.NewText(message.RoleAssistant, executionCheckpointFactsPrefix+string(encoded))
	checkpoint.Kind = message.KindCustom
	checkpoint.Visibility = message.VisibilityPrivate
	checkpoint.Metadata = map[string]string{executionCheckpointMetadataKey: "1"}
	manager := turnContext{runID: "run", captureWorkspace: func(context.Context) (workspaceCheckpointWitness, error) {
		return workspaceCheckpointWitness{VCS: "git", Head: "new", Complete: false, ErrorCode: "capture_failed"}, errors.New("private local path")
	}}
	messages := manager.workspaceReconciliationMessages(context.Background(), []message.Message{checkpoint})
	if len(messages) != 2 || messages[0].Text != workspaceUnverifiedPolicy || strings.Contains(messages[1].Text, "private local path") {
		t.Fatalf("incomplete workspace reconciliation=%#v", messages)
	}
}

func TestWorkspaceCheckpointDriftTargetsOnlyChangedPaths(t *testing.T) {
	saved := workspaceCheckpointWitness{Head: "head", Files: []workspaceFileWitness{
		{Path: "stable.go", Status: " M", CurrentSHA256: "same", BaseSHA256: "base"},
		{Path: "changed.go", Status: " M", CurrentSHA256: "old", BaseSHA256: "base"},
	}}
	current := workspaceCheckpointWitness{Head: "head", Files: []workspaceFileWitness{
		{Path: "stable.go", Status: " M", CurrentSHA256: "same", BaseSHA256: "base"},
		{Path: "changed.go", Status: " M", CurrentSHA256: "new", BaseSHA256: "base"},
		{Path: "added.go", Status: "??", CurrentSHA256: "added"},
	}}
	got := workspaceDriftPaths(saved, current)
	slices.Sort(got)
	want := []string{"added.go", "changed.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale paths=%v want=%v", got, want)
	}
}

func TestWorkspaceReconciliationBaselineAdvancesOnlyAfterSuccessfulTargetedRead(t *testing.T) {
	oldWitness := workspaceCheckpointWitness{VCS: "git", Head: "old", Complete: true, Files: []workspaceFileWitness{{Path: "a.go", CurrentSHA256: "old"}}}
	newWitness := workspaceCheckpointWitness{VCS: "git", Head: "new", Complete: true, Files: []workspaceFileWitness{{Path: "a.go", CurrentSHA256: "new"}}}
	manager := turnContext{
		runID: "run", executionCheckpoints: true, pendingWorkspacePaths: []string{"a.go"},
		captureWorkspace: func(context.Context) (workspaceCheckpointWitness, error) { return newWitness, nil },
	}
	first, err := manager.buildExecutionCheckpointMessage(context.Background(), []executionCheckpointFacts{{Version: 1, RunID: "run", Workspace: oldWitness}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	firstFacts, ok := parseExecutionCheckpoint(first)
	if !ok || !reflect.DeepEqual(firstFacts.WorkspacePending, []string{"a.go"}) || firstFacts.Workspace.Head != "old" {
		t.Fatalf("pending reconciliation advanced baseline: %+v", firstFacts)
	}
	manager.captureWorkspace = func(context.Context) (workspaceCheckpointWitness, error) { return oldWitness, nil }
	regenerated := manager.workspaceReconciliationMessages(context.Background(), []message.Message{first})
	if len(regenerated) != 2 || !strings.Contains(regenerated[1].Text, `"a.go"`) {
		t.Fatalf("durable pending path was not regenerated after interruption: %#v", regenerated)
	}
	manager.captureWorkspace = func(context.Context) (workspaceCheckpointWitness, error) { return newWitness, nil }
	call := message.ToolCall{ID: "read-a", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	history := []message.Message{{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{call}}, message.NewToolResult(message.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "current"})}
	manager.pendingWorkspacePaths = nil
	second, err := manager.buildExecutionCheckpointMessage(context.Background(), []executionCheckpointFacts{firstFacts}, history, false)
	if err != nil {
		t.Fatal(err)
	}
	secondFacts, ok := parseExecutionCheckpoint(second)
	if !ok || len(secondFacts.WorkspacePending) != 0 || secondFacts.Workspace.Head != "new" {
		t.Fatalf("completed reconciliation did not advance baseline: %+v", secondFacts)
	}
}

func TestShellArtifactSinkPersistsAfterExecutionCancellation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "shell-artifact.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Shell artifact"}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("shell-output\n"), 100)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err := newShellArtifactSink(sessions)(cancelled, agentservice.ShellExecutionSnapshot{
		SessionID: "session", RunID: "run", Output: string(payload[:64]),
	}, payload)
	if err != nil || !strings.HasPrefix(result.Reference, "artifact:") {
		t.Fatalf("artifact result=%#v err=%v", result, err)
	}
	artifact, err := sessions.LoadArtifact(ctx, "session", strings.TrimPrefix(result.Reference, "artifact:"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact.Payload, payload) || artifact.RunID != "run" || artifact.Kind != "shell_output" {
		t.Fatalf("persisted artifact=%#v", artifact)
	}
}

func TestLegacyCompactResolvesSummarizerLazily(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleSystem, "rules")}
	for index := 0; index < 10; index++ {
		history = append(history, message.NewText(message.RoleUser, fmt.Sprintf("question %d", index)), message.NewText(message.RoleAssistant, "answer"))
	}
	activated := ""
	manager := turnContext{staticIdentity: "static", activateCompaction: func(_ context.Context, _ []message.Message, identity string) error {
		activated = identity
		return nil
	}, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(context.Context, string) (string, error) { return "resolved summary", nil }, 1000, nil
	}}
	result, err := manager.Compact(context.Background(), history)
	if err != nil || len(result) >= len(history) || activated != activeCacheIdentity("static", compactionSummaryHash(result)) {
		t.Fatalf("legacy compact messages=%d/%d identity=%q err=%v", len(result), len(history), activated, err)
	}
}

func TestLazyCompactionResolverRetriesAfterCancellation(t *testing.T) {
	calls := 0
	resolver := lazyCompactionResolver(func(ctx context.Context, _, _, _ string) (string, int, hyprovider.Driver, error) {
		calls++
		if calls == 1 {
			return "", 0, nil, context.Canceled
		}
		return "model", 32000, &compactionTestDriver{}, nil
	}, config.ModelRouteConfig{}, "chatgpt", "model", "low", "cache", nil, nil)
	if _, _, err := resolver(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("first resolution error=%v", err)
	}
	if summarizer, budget, err := resolver(context.Background()); err != nil || summarizer == nil || budget <= 0 || calls != 2 {
		t.Fatalf("retry summarizer=%v budget=%d calls=%d err=%v", summarizer != nil, budget, calls, err)
	}
}

type compactionTestDriver struct {
	requests []hyprovider.Request
	streams  [][]hyprovider.Event
}

func TestPhase3BoundedCompactionUsesResolvedWindowAndPreservesTurns(t *testing.T) {
	var inputs []string
	resolveCalls := 0
	var resolved sync.Once
	manager := turnContext{structuredSummary: true, compactTargetTokens: 250, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		resolved.Do(func() { resolveCalls++ })
		return func(_ context.Context, input string) (string, error) {
			inputs = append(inputs, input)
			return `{"version":2,"objective":"continue","source_references":[]}`, nil
		}, 350, nil
	}}
	history := []message.Message{message.NewText(message.RoleSystem, "rules")}
	for i := range 8 {
		history = append(history, message.NewText(message.RoleUser, fmt.Sprintf("turn-%d %s", i, strings.Repeat("u", 220))))
		history = append(history, message.NewText(message.RoleAssistant, fmt.Sprintf("answer-%d %s", i, strings.Repeat("a", 220))))
	}
	history = append(history, message.NewText(message.RoleUser, "latest request"))
	if unchanged, err := manager.CompactTo(context.Background(), history, estimateContextTokens(history)); err != nil || resolveCalls != 0 || !reflect.DeepEqual(unchanged, history) {
		t.Fatalf("subthreshold resolved or changed: calls=%d err=%v", resolveCalls, err)
	}
	got, err := manager.CompactTo(context.Background(), history, 500)
	if err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || len(inputs) < 3 {
		t.Fatalf("resolve=%d bounded calls=%d", resolveCalls, len(inputs))
	}
	for i, input := range inputs {
		if len(input) > contextTokenBytes(350) {
			t.Fatalf("compactor input %d = %d bytes, budget=%d", i, len(input), contextTokenBytes(350))
		}
	}
	if estimateContextTokens(got) > 250 {
		t.Fatalf("compacted tokens=%d target=250", estimateContextTokens(got))
	}
	before := len(inputs)
	if _, err := manager.CompactTo(context.Background(), got, 500); err != nil || len(inputs) != before {
		t.Fatalf("repeat compact calls=%d want=%d err=%v", len(inputs), before, err)
	}
}

func TestPhase3IrreducibleAtomicGroupReturnsExplicitError(t *testing.T) {
	manager := turnContext{structuredSummary: true, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(context.Context, string) (string, error) { return `{"version":2,"objective":"x"}`, nil }, 100, nil
	}}
	_, err := manager.summarizeBounded(context.Background(), nil, []message.Message{
		message.NewText(message.RoleUser, strings.Repeat("x", 500)), message.NewText(message.RoleAssistant, "answer"),
	})
	if err == nil || !strings.Contains(err.Error(), "atomic group") {
		t.Fatalf("error=%v", err)
	}
}

func TestPhase3CompactsCompletedToolGroupsWithinLatestUserTurn(t *testing.T) {
	var inputs []string
	manager := turnContext{structuredSummary: true, compactTargetTokens: 430, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(_ context.Context, input string) (string, error) {
			inputs = append(inputs, input)
			return `{"version":2,"objective":"continue"}`, nil
		}, 500, nil
	}}
	latest := message.NewText(message.RoleUser, "keep this latest user message exactly")
	history := []message.Message{message.NewText(message.RoleSystem, "rules"), latest}
	appendGroups := func(dst []message.Message, first, count int) []message.Message {
		for i := first; i < first+count; i++ {
			id := fmt.Sprintf("call-%02d", i)
			dst = append(dst,
				message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "read"}}},
				message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "read", Content: id + strings.Repeat("x", 280)}),
			)
		}
		return dst
	}
	history = appendGroups(history, 0, 12)
	first, err := manager.CompactTo(context.Background(), history, 900)
	if err != nil {
		t.Fatal(err)
	}
	assertRollingToolCheckpoint(t, first, latest)
	if len(first) >= len(history) {
		t.Fatalf("first compaction did not reclaim messages: %d >= %d", len(first), len(history))
	}

	secondSource := appendGroups(append([]message.Message(nil), first...), 12, 8)
	second, err := manager.CompactTo(context.Background(), secondSource, 900)
	if err != nil {
		t.Fatal(err)
	}
	assertRollingToolCheckpoint(t, second, latest)
	if reflect.DeepEqual(second, secondSource) {
		t.Fatal("second rolling compaction made no progress")
	}
	if len(inputs) < 2 {
		t.Fatalf("summarizer calls=%d, want rolling calls", len(inputs))
	}
}

func TestPhase3RollingCompactionCarriesTrustedExecutionFacts(t *testing.T) {
	manager := turnContext{
		runID: "run-facts", executionCheckpoints: true, compactTargetTokens: 2600,
		todo:      session.TodoList{Goal: "continue without rereading", Revision: 3},
		summarize: func(context.Context, string) (string, error) { return "semantic state", nil },
	}
	latest := message.NewText(message.RoleUser, "implement everything")
	history := []message.Message{message.NewText(message.RoleSystem, "rules"), latest}
	for index := range 10 {
		id := fmt.Sprintf("fact-call-%d", index)
		history = append(history,
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "shell", Arguments: json.RawMessage(fmt.Sprintf(`{"command":"step-%d"}`, index))}}},
			message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "shell", Content: strings.Repeat("result ", 300)}),
		)
	}
	first, err := manager.CompactTo(context.Background(), history, 3500)
	if err != nil {
		t.Fatal(err)
	}
	var firstFacts executionCheckpointFacts
	for _, current := range first {
		if parsed, ok := parseExecutionCheckpoint(current); ok {
			firstFacts = parsed
		}
	}
	if firstFacts.Version != 1 || firstFacts.RunID != "run-facts" || firstFacts.Todo.Revision != 3 || len(firstFacts.Tools) == 0 {
		t.Fatalf("first execution facts=%+v history=%+v", firstFacts, first)
	}
	secondSource := append([]message.Message(nil), first...)
	for index := 10; index < 16; index++ {
		id := fmt.Sprintf("fact-call-%d", index)
		secondSource = append(secondSource,
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "shell"}}},
			message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "shell", Content: strings.Repeat("new result ", 300)}),
		)
	}
	second, err := manager.CompactTo(context.Background(), secondSource, 3500)
	if err != nil {
		t.Fatal(err)
	}
	var secondFacts executionCheckpointFacts
	for _, current := range second {
		if parsed, ok := parseExecutionCheckpoint(current); ok {
			secondFacts = parsed
		}
	}
	if secondFacts.Version != 1 || len(secondFacts.Tools) < len(firstFacts.Tools) {
		t.Fatalf("rolling execution facts regressed: first=%+v second=%+v", firstFacts, secondFacts)
	}
	if err := message.ValidateCompleteTurns(second); err != nil {
		t.Fatal(err)
	}
}

func assertRollingToolCheckpoint(t *testing.T, history []message.Message, latest message.Message) {
	t.Helper()
	if err := message.ValidateCompleteTurns(history); err != nil {
		t.Fatalf("invalid compacted history: %v", err)
	}
	user := -1
	summary := -1
	for i, current := range history {
		if current.Role == message.RoleUser {
			user = i
		}
		if current.Kind == message.KindCompactionSummary {
			summary = i
		}
	}
	if user < 0 || !reflect.DeepEqual(history[user], latest) || summary != user+1 {
		t.Fatalf("latest user/summary placement: user=%d summary=%d history=%#v", user, summary, history)
	}
	for i := summary + 1; i < len(history); i += 2 {
		if i+1 >= len(history) || len(history[i].ToolCalls) == 0 || history[i+1].ToolResult == nil || history[i].ToolCalls[0].ID != history[i+1].ToolResult.ToolCallID {
			t.Fatalf("split tool group at hot-tail message %d", i)
		}
	}
}

func TestPhase3SingleUserTurnLargerThanCompactorWindowChunksByToolGroup(t *testing.T) {
	var inputs []string
	manager := turnContext{structuredSummary: true, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(_ context.Context, input string) (string, error) {
			inputs = append(inputs, input)
			return `{"version":2,"objective":"continue"}`, nil
		}, 500, nil
	}}
	omitted := []message.Message{message.NewText(message.RoleUser, "one turn")}
	for i := range 10 {
		id := fmt.Sprintf("chunk-%d", i)
		omitted = append(omitted,
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "read"}}},
			message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "read", Content: id + strings.Repeat("z", 240)}),
		)
	}
	if _, err := manager.summarizeBounded(context.Background(), nil, omitted); err != nil {
		t.Fatal(err)
	}
	if len(inputs) < 2 {
		t.Fatalf("single turn was not chunked: calls=%d", len(inputs))
	}
	for n, input := range inputs {
		for i := range 10 {
			id := fmt.Sprintf("chunk-%d", i)
			if strings.Contains(input, `"id":"`+id+`"`) != strings.Contains(input, `"tool_call_id":"`+id+`"`) {
				t.Fatalf("input %d split atomic tool group %s", n, id)
			}
		}
	}
}

func TestPhase3SummaryReductionPreservesPreviousChronology(t *testing.T) {
	var inputs []string
	manager := turnContext{structuredSummary: true, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(_ context.Context, input string) (string, error) {
			inputs = append(inputs, input)
			return `{"version":2,"objective":"reduced"}`, nil
		}, 1000, nil
	}}
	previous := []string{
		`{"version":2,"objective":"OLDEST"}`,
		`{"version":2,"objective":"NEWEST"}`,
	}
	if _, err := manager.summarizeBounded(context.Background(), previous, []message.Message{message.NewText(message.RoleUser, "current")}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(inputs, "\n")
	oldest := strings.Index(joined, "OLDEST")
	newest := strings.Index(joined, "NEWEST")
	if oldest < 0 || newest < 0 || oldest >= newest {
		t.Fatalf("previous summary chronology reversed: %s", joined)
	}
}

func TestPhase3MapFailureLeavesHistoryCheckpointUnchanged(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old", 200)),
		message.NewText(message.RoleAssistant, "answer"),
		message.NewText(message.RoleUser, "latest"),
	}
	manager := turnContext{structuredSummary: true, compactTargetTokens: 20, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(context.Context, string) (string, error) { return "", errors.New("map failed") }, 1000, nil
	}}
	got, err := manager.CompactTo(context.Background(), history, 50)
	if err == nil || !strings.Contains(err.Error(), "map failed") {
		t.Fatalf("error=%v", err)
	}
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("failed compaction mutated checkpoint: %#v", got)
	}
}

func TestPhase3DurableProvenanceUsesSequence(t *testing.T) {
	value, ok := blockMessage(session.Block{Sequence: 42, Kind: "user", Content: "source"})
	if !ok || messageSourceReference(value, 0, 0) != "sequence:42" {
		t.Fatalf("message=%#v", value)
	}
	failed, ok := blockMessage(session.Block{Sequence: 43, Kind: "assistant", State: "failed", Content: "partial output"})
	if !ok || failed.Text != failedAssistantLabel+"partial output" {
		t.Fatalf("failed assistant message=%#v", failed)
	}
	if _, err := normalizeSummaryV2(`{"version":2,"objective":"x","source_references":["message:0"]}`, nil); err == nil {
		t.Fatal("accepted ambiguous per-chunk message provenance")
	}
	normalized, err := normalizeSummaryV2(
		`{"version":2,"objective":"x","covered":["transcript: invented prose"],"source_references":["transcript: invented prose"]}`,
		[]string{"sequence:42"},
	)
	if err != nil {
		t.Fatalf("host provenance did not replace model references: %v", err)
	}
	var summary compactionSummaryV2
	if err := json.Unmarshal([]byte(normalized), &summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary.Sources, []string{"sequence:42"}) || !reflect.DeepEqual(summary.Covered, []string{"sequence:42"}) {
		t.Fatalf("normalized provenance=%+v", summary)
	}
}

func TestPhase3ContextBudgetUsesConfiguredReservesOnce(t *testing.T) {
	cfg := config.ContextConfig{HardTriggerRatio: .8, TargetRatio: .5, SafetyMarginRatio: .1, ReserveOutputTokens: 1000, ReserveReasoningTokens: 500}
	got, err := calculateContextBudget("chatgpt", "model", 100_000, 300, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Usable != 80_008 || got.HardTrigger != 64_006 || got.Target != 40_004 {
		t.Fatalf("budget=%+v", got)
	}
}

func (d *compactionTestDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test"}
}

func (d *compactionTestDriver) Stream(_ context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	d.requests = append(d.requests, request)
	events := d.streams[0]
	d.streams = d.streams[1:]
	return hyprovider.NewSliceStream(events), nil
}

func TestFailedProviderTurnPersistsStreamedBreakpoint(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "failed-session", Title: "Failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "failed-session", session.Block{Kind: "user", RunID: "completed-run", Content: "previous request"}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.CompleteTurn(ctx, "failed-session", session.Block{
		Kind: "assistant", RunID: "completed-run", Content: "previous answer", State: "completed",
	}, session.ModelHistory{
		ProviderID: "test", ModelID: "test", InstructionFingerprint: mainInstructionFingerprint,
		StaticPrefixHash: mainInstructionFingerprint, WireVersion: session.CurrentWireVersion,
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "test"), message.NewText(message.RoleUser, "previous request"),
			message.NewText(message.RoleAssistant, "previous answer"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	beforeFailure, err := sessions.LoadProjection(ctx, "failed-session")
	if err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "keep the failed breakpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "failed-session", session.Block{
		Kind: "user", RunID: run.RunID, Title: "You", Content: "keep the failed breakpoint",
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, coding)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	service.wg.Add(1)
	service.runProviderTurn(ctx, TurnRequest{
		SessionID: "failed-session", Prompt: "keep the failed breakpoint", Provider: "test", Model: "test",
	}, run, hyagent.Engine{
		Provider: &compactionTestDriver{streams: [][]hyprovider.Event{{
			{Kind: hyprovider.EventTextDelta, Text: "work completed before "},
			{Kind: hyprovider.EventTextDelta, Text: "the provider failed"},
			{Kind: hyprovider.EventError, Err: errors.New("provider failed")},
		}}},
		Model: "test", ContextBuilder: turnContext{instructions: "test"},
	})
	projection, err := sessions.LoadProjection(ctx, "failed-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 4 || projection.Blocks[3].Kind != "assistant" || projection.Blocks[3].State != "failed" ||
		projection.Blocks[3].Content != "work completed before the provider failed" {
		t.Fatalf("failed breakpoint blocks=%+v", projection.Blocks)
	}
	if !reflect.DeepEqual(projection.ModelHistory, beforeFailure.ModelHistory) ||
		projection.CheckpointGeneration != beforeFailure.CheckpointGeneration {
		t.Fatalf("failed breakpoint changed checkpoint:\n got=%+v generation=%d\nwant=%+v generation=%d",
			projection.ModelHistory, projection.CheckpointGeneration, beforeFailure.ModelHistory, beforeFailure.CheckpointGeneration)
	}
}

func TestRecapInputIncludesGoalAnswerAndOnlyOpenTodoItems(t *testing.T) {
	encoded, err := recapInput(recapGenerationRequest{
		Goal:   "Ship concise recap",
		Answer: "The implementation is complete with many details.",
		Todo: session.TodoList{Phases: []session.TodoPhase{{Title: "Work", Items: []session.TodoItem{
			{Content: "Run focused tests", Status: session.TodoInProgress},
			{Content: "Open PR", Status: session.TodoPending},
			{Content: "Inspect implementation", Status: session.TodoCompleted},
		}}}},
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"goal":"Ship concise recap"`) ||
		!strings.Contains(encoded, `"latest_answer":"The implementation is complete with many details."`) ||
		!strings.Contains(encoded, `"in_progress: Run focused tests"`) ||
		!strings.Contains(encoded, `"pending: Open PR"`) || strings.Contains(encoded, "Inspect implementation") {
		t.Fatalf("recap input = %s", encoded)
	}
}

func TestRecapInputBoundsLongAnswerByKeepingItsTail(t *testing.T) {
	encoded, err := recapInput(recapGenerationRequest{Goal: "goal", Answer: strings.Repeat("prefix ", 200) + "final outcome and next action"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 200 || !strings.Contains(encoded, "final outcome and next action") || !strings.Contains(encoded, "[truncated]") {
		t.Fatalf("bounded recap input (%d bytes) = %s", len(encoded), encoded)
	}
}

func TestCollectProviderTextReturnsOnlyGeneratedRecap(t *testing.T) {
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "Goal is complete. "},
		{Kind: hyprovider.EventTextDelta, Text: "Next: open the PR."},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	request := hyprovider.Request{Model: "model", Messages: []message.Message{message.NewText(message.RoleUser, "full answer")}}
	got, err := collectProviderText(context.Background(), driver, request, "recap")
	if err != nil || got != "Goal is complete. Next: open the PR." {
		t.Fatalf("generated recap = %q, %v", got, err)
	}
	if len(driver.requests) != 1 || driver.requests[0].Messages[0].Text != "full answer" {
		t.Fatalf("recap request = %#v", driver.requests)
	}
}

func TestRecapPromptRequiresAConcisePlainTextStatus(t *testing.T) {
	for _, requirement := range []string{"under 40 words", "plain text only", "single next action", "Do not repeat the full answer"} {
		if !strings.Contains(recapPrompt, requirement) {
			t.Fatalf("recap prompt omitted %q: %s", requirement, recapPrompt)
		}
	}
}

func TestAuthenticatedTurnStreamsGovernedWriteAndCompletesDurably(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	var responseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers = %q %q", request.Header.Get("Authorization"), request.Header.Get("ChatGPT-Account-ID"))
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-test","title":"GPT Test","context_window":128000,"supported_reasoning_levels":["minimal","high"],"default_reasoning_level":"high","supports_tools":true}]}`))
		case "/responses":
			var payload struct {
				Reasoning map[string]any `json:"reasoning"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode provider request: %v", err)
			} else if payload.Reasoning["effort"] != "minimal" {
				t.Errorf("reasoning effort = %v, want minimal", payload.Reasoning)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			if responseCalls.Add(1) == 1 {
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"write-1\",\"name\":\"coding.write_file\",\"arguments\":\"{\\\"path\\\":\\\"created.txt\\\",\\\"content\\\":\\\"created by agent\\\\n\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14,\"input_tokens_details\":{\"cached_tokens\":6,\"cache_write_tokens\":2}}}}\n\n")
				return
			}
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Created and verified.\"}\n\n")
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-2\",\"status\":\"completed\",\"usage\":{\"input_tokens\":20,\"output_tokens\":6,\"total_tokens\":26,\"input_tokens_details\":{\"cached_tokens\":15,\"cache_write_tokens\":3}}}}\n\n")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL + "/models"
	modelCatalog.AdditionalEndpoints["chatgpt"] = nil
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "default", Title: "Test", ProviderID: "chatgpt", ModelID: "gpt-test", Reasoning: "high", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachProviderRuntime(providerRuntime)

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "create created.txt", Provider: "chatgpt", Model: "gpt-test", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	approved := false
	var contextUsage [][3]string
	var cacheWrites []string
	estimatedUsage := 0
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(deadline)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventTextDelta:
			output.WriteString(event.Text)
		case EventContextUsage:
			if event.State == "reported" {
				if event.Data["inputTokens"] != "" {
					contextUsage = append(contextUsage, [3]string{event.Data["inputTokens"], event.Data["cachedInputTokens"], event.Data["outputTokens"]})
				}
				if event.Data["cacheWriteTokens"] != "" {
					cacheWrites = append(cacheWrites, event.Data["cacheWriteTokens"])
				}
			} else if event.State == "estimated" {
				estimatedUsage++
			}
		case EventApprovalRequested:
			if event.ToolCallID != "write-1" || event.Data["tool"] != "coding.write_file" {
				t.Fatalf("approval event = %+v", event)
			}
			if err := service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
			approved = true
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunFinished:
			goto finished
		}
	}

finished:
	if !approved || output.String() != "Created and verified." || responseCalls.Load() != 2 {
		t.Fatalf("turn = approved:%v output:%q response calls:%d", approved, output.String(), responseCalls.Load())
	}
	if want := [][3]string{{"10", "6", "4"}, {"20", "15", "6"}}; !reflect.DeepEqual(contextUsage, want) {
		t.Fatalf("context usage events = %v, want %v", contextUsage, want)
	}
	if !reflect.DeepEqual(cacheWrites, []string{"2", "3"}) {
		t.Fatalf("cache write events = %v", cacheWrites)
	}
	if estimatedUsage < 2 {
		t.Fatalf("estimated context usage events = %d, want at least 2", estimatedUsage)
	}
	contents, err := os.ReadFile(filepath.Join(workspace, "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "created by agent\n" {
		t.Fatalf("created file = %q", contents)
	}
	projection, err := coding.Recover(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != "completed" {
		t.Fatalf("durable run status = %q", projection.Run.Status)
	}
	sessionProjection, err := sessions.LoadProjection(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if sessionProjection.Session.Reasoning != "minimal" {
		t.Fatalf("persisted reasoning = %q, want minimal", sessionProjection.Session.Reasoning)
	}
	if sessionProjection.Usage.CacheWriteTokens != 5 || sessionProjection.Usage.MainCacheWrite != 5 {
		t.Fatalf("persisted cache writes = %+v", sessionProjection.Usage)
	}
	if len(sessionProjection.Blocks) != 2 || sessionProjection.Blocks[1].Content != "Created and verified." {
		t.Fatalf("session projection = %+v", sessionProjection.Blocks)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestTurnContextBuildsPriorConversationBeforeCurrentRequest(t *testing.T) {
	contextManager := turnContext{
		instructions: "system rules",
		history: []session.Block{
			{Kind: "user", Content: "first request"},
			{Kind: "assistant", Content: "first answer"},
		},
	}
	messages, err := contextManager.Build(context.Background(), api.Task{Goal: "follow-up request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("message count=%d", len(messages))
	}
	if messages[0].Role != message.RoleSystem || messages[0].Text != "system rules" ||
		messages[1].Role != message.RoleUser || messages[1].Text != "first request" ||
		messages[2].Role != message.RoleAssistant || messages[2].Text != "first answer" ||
		messages[3].Role != message.RoleUser || messages[3].Text != "follow-up request" {
		t.Fatalf("messages=%+v", messages)
	}
}

func TestActiveGuidanceIsFIFOAndInjectedAtModelBoundaries(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.mu.Unlock()

	for _, text := range []string{"first correction", "second correction"} {
		if err := service.GuideActiveTurn("session-guided", "run-guided", text); err != nil {
			t.Fatal(err)
		}
	}
	inner := turnContext{instructions: "rules", summarize: func(context.Context, string) (string, error) { return "guidance summary", nil }}
	manager := activeGuidanceContext{
		inner: inner,
		peek:  func() activeGuidanceSnapshot { return service.peekActiveGuidance("session-guided", "run-guided") },
		acknowledge: func(snapshot activeGuidanceSnapshot) {
			service.acknowledgeActiveGuidance("session-guided", "run-guided", snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old context ", 500)),
		message.NewText(message.RoleAssistant, "old answer"),
	}
	prepared, err := manager.CompactTo(context.Background(), history, 100)
	if err != nil {
		t.Fatal(err)
	}
	latest := prepared[len(prepared)-1]
	firstIndex, secondIndex := strings.Index(latest.Text, "first correction"), strings.Index(latest.Text, "second correction")
	if latest.Role != message.RoleUser || firstIndex < 0 || secondIndex <= firstIndex {
		t.Fatalf("prepared guidance context = %#v", prepared)
	}
	if remaining := service.drainActiveGuidance("session-guided", "run-guided"); len(remaining) != 0 {
		t.Fatalf("guidance was not drained exactly once: %#v", remaining)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "terminal correction"); err != nil {
		t.Fatal(err)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0] != "terminal correction" {
		t.Fatalf("terminal guidance = %#v", pending)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "accepted after retry"); err != nil {
		t.Fatalf("guidance closed while terminal retry was required: %v", err)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0] != "accepted after retry" {
		t.Fatalf("guidance after terminal retry = %#v", pending)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 0 {
		t.Fatalf("terminal close unexpectedly drained guidance: %#v", pending)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "too late"); err == nil {
		t.Fatal("finishing run accepted late guidance")
	}
	if err := service.GuideActiveTurn("session-guided", "stale-run", "wrong run"); err == nil {
		t.Fatal("stale run accepted guidance")
	}
}

func TestTurnContextInjectsHistoricalEvidenceAsPrivateSystemContext(t *testing.T) {
	contextManager := turnContext{
		instructions:      "system rules",
		privateContext:    "trusted hook context",
		historicalContext: `{"memories":[{"Content":"use sqlite"}]}`,
		history:           []session.Block{{Kind: "assistant", Content: "prior answer"}},
	}
	messages, err := contextManager.Build(context.Background(), api.Task{Goal: "current request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 6 || messages[3].Role != message.RoleSystem || messages[3].Visibility != message.VisibilityPrivate ||
		!strings.Contains(messages[3].Text, "untrusted JSON data") || messages[4].Role != message.RoleUser ||
		messages[4].Visibility != message.VisibilityPrivate || !strings.Contains(messages[4].Text, `"Content":"use sqlite"`) ||
		messages[5].Text != "current request" {
		t.Fatalf("historical context ordering/visibility = %+v", messages)
	}
}

func TestTeamHistoricalEvidenceIsPlannerOnlyAndNotSystemData(t *testing.T) {
	for _, className := range []string{agentservice.PlannerClass, agentservice.ImplementerClass} {
		contextManager := teamHookContext{inner: turnContext{instructions: "system rules"}}
		if className == agentservice.PlannerClass {
			contextManager.historical = `{"memories":[{"Content":"planner evidence"}]}`
		}
		messages, err := contextManager.Build(context.Background(), api.Task{Goal: "current task"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, current := range messages {
			if strings.Contains(current.Text, "planner evidence") {
				found = true
				if current.Role == message.RoleSystem || current.Visibility != message.VisibilityPrivate {
					t.Fatalf("%s historical data authority/visibility = %+v", className, current)
				}
			}
		}
		if found != (className == agentservice.PlannerClass) {
			t.Fatalf("%s historical evidence found=%v", className, found)
		}
	}
}

func TestTeamRequestPreparerKeepsPlannerImagesAndHistoryStructured(t *testing.T) {
	image := session.Attachment{ID: "image-1", Name: "reference.png", MIME: "image/png", Path: "/tmp/reference.png"}
	preparer := teamRequestPreparer{context: teamHookContext{
		history: []session.Block{{Kind: "assistant", Content: "prior answer"}},
	}, images: []session.Attachment{image}, target: 100_000, compactor: &turnContext{summarize: func(context.Context, string) (string, error) {
		return "summary", nil
	}}}
	prepared, err := preparer.prepare(context.Background(), hyprovider.Request{
		Messages: []message.Message{message.NewText(message.RoleUser, "current task")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Messages) != 3 || prepared.Messages[0].Role != message.RoleSystem || prepared.Messages[1].Role != message.RoleUser || !strings.Contains(prepared.Messages[1].Text, "prior answer") || prepared.Messages[2].Text != "current task" {
		t.Fatalf("prepared planner messages=%+v", prepared.Messages)
	}
	attachments := AttachmentsFromMessage(prepared.Messages[2])
	if len(attachments) != 1 || attachments[0] != image {
		t.Fatalf("prepared planner attachments=%+v", attachments)
	}
}

func TestTeamRequestPreparerAppendsTodoUpdatesWithoutChangingWirePrefix(t *testing.T) {
	current := session.TodoList{Goal: "ship", Revision: 1, Phases: []session.TodoPhase{{
		ID: "phase-1", Title: "Build", Items: []session.TodoItem{{ID: "item-1", Content: "implement", Status: session.TodoInProgress}},
	}}}
	preparer := teamRequestPreparer{
		runID: "team-role-1", loadTodo: func(context.Context) (session.TodoList, error) { return current, nil },
	}
	taskMessage := message.NewText(message.RoleUser, "current task")
	first, err := preparer.prepare(context.Background(), hyprovider.Request{Messages: []message.Message{
		taskMessage,
	}})
	if err != nil {
		t.Fatal(err)
	}
	current.Revision = 2
	current.Phases[0].Items[0].Status = session.TodoCompleted
	second, err := preparer.prepare(context.Background(), hyprovider.Request{Messages: []message.Message{
		taskMessage,
		message.NewText(message.RoleAssistant, "tool update completed"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Messages) <= len(first.Messages) {
		t.Fatalf("todo continuation did not grow: first=%+v second=%+v", first.Messages, second.Messages)
	}
	for index := range first.Messages {
		if !reflect.DeepEqual(first.Messages[index], second.Messages[index]) {
			t.Fatalf("todo update changed wire prefix at message %d: first=%+v second=%+v", index, first.Messages[index], second.Messages[index])
		}
	}
	if !strings.Contains(first.Messages[len(first.Messages)-1].Text, "revision=1") || !strings.Contains(second.Messages[len(second.Messages)-1].Text, "revision=2") {
		t.Fatalf("todo reminders were not appended by revision: first=%+v second=%+v", first.Messages, second.Messages)
	}
}

func TestTeamRequestPreparerUsesModelCompactionAtContextTarget(t *testing.T) {
	messages := make([]message.Message, 0, 21)
	for index := 0; index < 10; index++ {
		messages = append(messages,
			message.NewText(message.RoleUser, fmt.Sprintf("request-%d %s", index, strings.Repeat("x", 400))),
			message.NewText(message.RoleAssistant, fmt.Sprintf("answer-%d %s", index, strings.Repeat("y", 400))),
		)
	}
	messages = append(messages, message.NewText(message.RoleUser, "latest request"))
	summaryCalls := 0
	preparer := teamRequestPreparer{target: 600, compactor: &turnContext{summarize: func(context.Context, string) (string, error) {
		summaryCalls++
		return "model-generated team summary", nil
	}}}
	prepared, err := preparer.prepare(context.Background(), hyprovider.Request{Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if summaryCalls != 1 || len(prepared.Messages) >= len(messages) {
		t.Fatalf("team compaction calls=%d messages=%d want fewer than %d", summaryCalls, len(prepared.Messages), len(messages))
	}
	foundSummary := false
	for _, current := range prepared.Messages {
		foundSummary = foundSummary || current.Kind == message.KindCompactionSummary
	}
	if !foundSummary {
		t.Fatalf("team compaction omitted model summary: %+v", prepared.Messages)
	}
	continuedMessages := append(append([]message.Message(nil), messages...), message.NewText(message.RoleAssistant, "short continuation"))
	continued, err := preparer.prepare(context.Background(), hyprovider.Request{Messages: continuedMessages})
	if err != nil {
		t.Fatal(err)
	}
	if summaryCalls != 1 {
		t.Fatalf("unchanged compacted prefix was summarized again: calls=%d", summaryCalls)
	}
	if len(continued.Messages) <= len(prepared.Messages) {
		t.Fatalf("compacted continuation did not grow: first=%d second=%d", len(prepared.Messages), len(continued.Messages))
	}
	for index := range prepared.Messages {
		if !reflect.DeepEqual(prepared.Messages[index], continued.Messages[index]) {
			t.Fatalf("compacted continuation changed provider prefix at message %d", index)
		}
	}
}

func TestTurnContextRefreshesTodoReminderAfterMutation(t *testing.T) {
	latest := session.TodoList{
		Goal: "ship todo", Revision: 2,
		Phases: []session.TodoPhase{{ID: "phase-1", Title: "Build", Items: []session.TodoItem{
			{ID: "item-1", Content: "finished", Status: session.TodoCompleted},
			{ID: "item-2", Content: "verify", Status: session.TodoInProgress},
		}}},
	}
	manager := turnContext{
		loadTodo:  func(context.Context) (session.TodoList, error) { return latest, nil },
		summarize: func(context.Context, string) (string, error) { return "todo summary", nil },
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleSystem, todoReminder(session.TodoList{Goal: "ship todo", Revision: 1})),
		message.NewText(message.RoleUser, "continue"),
	}
	refreshed, err := manager.CompactTo(context.Background(), history, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed) != len(history)+1 || !reflect.DeepEqual(refreshed[:len(history)], history) {
		t.Fatalf("todo refresh changed provider-visible prefix: before=%+v after=%+v", history, refreshed)
	}
	latestReminder := refreshed[len(refreshed)-1].Text
	if !strings.Contains(latestReminder, "revision=2") || !strings.Contains(latestReminder, "item-2:in_progress:verify") {
		t.Fatalf("latest todo reminder: %q", latestReminder)
	}
	if refreshed[len(refreshed)-1].Role != message.RoleSystem || refreshed[len(refreshed)-1].Visibility != message.VisibilityPrivate {
		t.Fatalf("todo update must remain in the private input tail: %+v", refreshed[len(refreshed)-1])
	}
	repeated, err := manager.CompactTo(context.Background(), refreshed, 0)
	if err != nil || !reflect.DeepEqual(repeated, refreshed) {
		t.Fatalf("unchanged todo appended another update: history=%+v error=%v", repeated, err)
	}

	latest = session.TodoList{Revision: 3}
	cleared, err := manager.CompactTo(context.Background(), refreshed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != len(refreshed)+1 || !reflect.DeepEqual(cleared[:len(refreshed)], refreshed) {
		t.Fatalf("clearing todo changed provider-visible prefix: before=%+v after=%+v", refreshed, cleared)
	}
	if text := cleared[len(cleared)-1].Text; !strings.Contains(text, "revision=3") || !strings.Contains(text, todoReminderCleared) {
		t.Fatalf("todo clear tombstone = %q", text)
	}
}

func TestTurnContextCompactPreservesFullSystemPrefixAndFreshTodo(t *testing.T) {
	latest := session.TodoList{Goal: "ship", Revision: 4, Phases: []session.TodoPhase{{
		ID: "phase", Title: "Build", Items: []session.TodoItem{{ID: "current", Content: "verify", Status: session.TodoInProgress}},
	}}}
	manager := turnContext{
		loadTodo:  func(context.Context) (session.TodoList, error) { return latest, nil },
		summarize: func(context.Context, string) (string, error) { return "todo summary", nil },
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		manager.todoReminderMessage(todoReminder(session.TodoList{Goal: "ship", Revision: 1})),
		message.NewText(message.RoleUser, "setup"),
		message.NewText(message.RoleAssistant, "setup complete"),
		manager.todoReminderMessage(todoReminder(latest)),
	}
	for index := 0; index < 20; index++ {
		role := message.RoleUser
		if index%2 == 1 {
			role = message.RoleAssistant
		}
		history = append(history, message.NewText(role, fmt.Sprintf("message %d", index)))
	}
	compacted, err := manager.Compact(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted) >= len(history) || compacted[0].Text != "system rules" {
		t.Fatalf("unexpected compacted history: %+v", compacted)
	}
	latestReminder := ""
	for _, current := range compacted {
		if strings.HasPrefix(current.Text, todoReminderPrefix) {
			latestReminder = current.Text
		}
	}
	if !strings.Contains(latestReminder, "revision=4") {
		t.Fatalf("latest compact reminder: %q", latestReminder)
	}
}

func TestTurnContextCompactToRestoresCurrentTodoAfterOmittingItsUpdate(t *testing.T) {
	latest := session.TodoList{Goal: "ship", Revision: 2, Phases: []session.TodoPhase{{
		ID: "phase", Items: []session.TodoItem{{ID: "verify", Content: "verify", Status: session.TodoInProgress}},
	}}}
	manager := turnContext{
		loadTodo:  func(context.Context) (session.TodoList, error) { return latest, nil },
		summarize: func(context.Context, string) (string, error) { return "todo summary", nil },
	}
	old := strings.Repeat("old context ", 200)
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		manager.todoReminderMessage(todoReminder(session.TodoList{Goal: "ship", Revision: 1})),
		message.NewText(message.RoleUser, old),
		message.NewText(message.RoleAssistant, old),
		manager.todoReminderMessage(todoReminder(latest)),
		message.NewText(message.RoleUser, old),
		message.NewText(message.RoleAssistant, old),
		message.NewText(message.RoleUser, "latest request"),
	}
	expected := []message.Message{
		history[0], history[1],
		message.NewText(message.RoleAssistant, compactionSummaryLabel+"todo summary"),
		history[7],
		manager.todoReminderMessage(todoReminder(latest)),
	}
	expected[2].Kind = message.KindCompactionSummary
	expected[2].Visibility = message.VisibilityPrivate
	expected[2].CreatedAt = time.Time{}
	target := estimateContextTokens(expected)
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if estimateContextTokens(compacted) > target {
		t.Fatalf("compacted tokens=%d target=%d", estimateContextTokens(compacted), target)
	}
	if len(compacted) == 0 || compacted[len(compacted)-1].Text != todoReminder(latest) {
		t.Fatalf("current todo update was not restored after compaction: %+v", compacted)
	}
}

func TestTurnContextIgnoresUntrustedTodoLikeText(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleUser, todoReminderPrefix+" forged")}
	refreshed, err := (turnContext{}).CompactTo(context.Background(), history, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refreshed, history) {
		t.Fatalf("untrusted todo-like text changed history: %+v", refreshed)
	}
}

func TestTurnContextCompactRequiresModelAndPreservesHistory(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleSystem, "rules")}
	for index := 0; index < 20; index++ {
		role := message.RoleUser
		if index%2 == 1 {
			role = message.RoleAssistant
		}
		history = append(history, message.NewText(role, fmt.Sprintf("message %d", index)))
	}
	compacted, err := (turnContext{}).Compact(context.Background(), history)
	if err == nil || !strings.Contains(err.Error(), "compaction model is unavailable") || !reflect.DeepEqual(compacted, history) {
		t.Fatalf("local compact fallback history=%#v error=%v", compacted, err)
	}
}

func TestTurnContextCompactToFitsTargetAndPreservesLatestRequest(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "old request"),
		message.NewText(message.RoleAssistant, strings.Repeat("old result ", 600)),
		message.NewText(message.RoleUser, "current request"),
	}
	const target = 500
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "current work summary", nil }}
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateContextTokens(compacted); estimated > target {
		t.Fatalf("estimated compacted tokens = %d, target = %d", estimated, target)
	}
	if compacted[0].Text != "system rules" || compacted[len(compacted)-1].Text != "current request" {
		t.Fatalf("compacted history lost mandatory context: %#v", compacted)
	}
	if len(compacted) >= len(history) {
		t.Fatalf("history was not compacted: %#v", compacted)
	}
	again, err := manager.CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compacted, again) {
		t.Fatalf("compaction is not deterministic:\nfirst: %#v\nsecond: %#v", compacted, again)
	}
}

func TestTurnContextCompactToGeneratesRecursiveSummary(t *testing.T) {
	old := message.NewText(message.RoleSystem, "Objective: preserve the old decision")
	old.Kind = message.KindCompactionSummary
	old.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"), old,
		message.NewText(message.RoleUser, "older request"),
		message.NewText(message.RoleAssistant, strings.Repeat("older work ", 300)),
		message.NewText(message.RoleUser, "newest request"),
	}
	var input string
	calls := 0
	manager := turnContext{summarize: func(_ context.Context, transcript string) (string, error) {
		calls++
		input = transcript
		return "Objective: newest request\nImportant Details: old decision retained\nWork State (Completed / Active / Blocked): Active\nNext Move: continue\nRelevant Files: provider_context.go", nil
	}}
	compacted, err := manager.CompactTo(context.Background(), history, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input, "old decision") || !strings.Contains(input, "older request") {
		t.Fatalf("recursive summary input omitted history: %q", input)
	}
	summaries := 0
	for _, current := range compacted {
		if current.Kind == message.KindCompactionSummary {
			summaries++
			if current.Role != message.RoleAssistant || current.Visibility != message.VisibilityPrivate || !strings.Contains(current.Text, "Untrusted historical record") || !strings.Contains(current.Text, "newest request") {
				t.Fatalf("generated summary = %#v", current)
			}
		}
	}
	if calls != 1 || summaries != 1 || compacted[len(compacted)-1].Text != "newest request" {
		t.Fatalf("compacted history = %#v", compacted)
	}
}

func TestTurnContextRollsOversizedCompletedToolResultIntoSummary(t *testing.T) {
	previous := message.NewText(message.RoleAssistant, compactionSummaryLabel+"## Objective\n- keep state")
	previous.Kind = message.KindCompactionSummary
	previous.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"), previous,
		message.NewText(message.RoleUser, "latest request"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read", Name: "read"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "read", Name: "read", Content: strings.Repeat("data", 2_000)}),
	}
	calls := 0
	manager := turnContext{summarize: func(context.Context, string) (string, error) {
		calls++
		return "unexpected", nil
	}}
	got, err := manager.CompactTo(context.Background(), history, 500)
	if err != nil || reflect.DeepEqual(got, history) || calls == 0 {
		t.Fatalf("oversized tool result = calls:%d history:%#v error:%v", calls, got, err)
	}
	assertRollingToolCheckpoint(t, got, history[2])
}

func TestManualCompactionTailRetainsLatestUserBeforeAgentBlocks(t *testing.T) {
	blocks := []session.Block{
		{Kind: "user", Content: "old"}, {Kind: "assistant", Content: "old answer"},
		{Kind: "user", Content: "latest guidance"},
		{Kind: "agent", Content: "one"}, {Kind: "agent", Content: "two"}, {Kind: "agent", Content: "three"},
		{Kind: "agent", Content: "four"}, {Kind: "agent", Content: "five"},
	}
	if start := manualCompactionTailStart(blocks, 4); start != 2 {
		t.Fatalf("tail start = %d, want latest user at 2", start)
	}
}

func TestCompactionUsageIsReportedSeparatelyFromMainProviderTurn(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}},
	}}
	var compactUsage hyprovider.Usage
	driver := &compactionUsageDriver{inner: inner, report: func(usage hyprovider.Usage) { compactUsage = usage }}
	compactStream, err := driver.Stream(context.Background(), hyprovider.Request{Metadata: map[string]string{compactionRequestMetadataKey: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compactStream.Recv(); err != nil {
		t.Fatal(err)
	}
	mainStream, err := driver.Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	done, err := mainStream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if compactUsage.InputTokens != 7 || compactUsage.OutputTokens != 3 || compactUsage.TotalTokens != 10 {
		t.Fatalf("compaction usage = %#v", compactUsage)
	}
	if done.Usage.InputTokens != 15 || done.Usage.OutputTokens != 5 || done.Usage.TotalTokens != 20 {
		t.Fatalf("main usage was contaminated = %#v", done.Usage)
	}
}

func TestProviderUsageBudgetIncludesCompactionWithoutMergingUsage(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{TotalTokens: 6}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{TotalTokens: 6}}},
	}}
	driver := &budgetedProviderDriver{inner: inner, budget: &providerUsageBudget{maxTokens: 10}}
	for index := 0; index < 2; index++ {
		stream, err := driver.Stream(context.Background(), hyprovider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := driver.Stream(context.Background(), hyprovider.Request{}); !errors.Is(err, hyagent.ErrBudgetExhausted) {
		t.Fatalf("combined usage budget error=%v", err)
	}
	if len(inner.requests) != 2 {
		t.Fatalf("provider received %d requests after budget exhaustion", len(inner.requests))
	}
}

func TestLazyCompactionRouteUsesIndependentDriverCacheKeyAndUsage(t *testing.T) {
	compact := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete, Usage: hyprovider.Usage{TotalTokens: 10}},
	}}}
	resolveCalls := 0
	var reported hyprovider.Usage
	summarize := lazyCompactionSummarizer(func(_ context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
		resolveCalls++
		if provider != "grok" || model != "summary-model" || reasoning != "high" {
			t.Fatalf("resolved route = %s/%s/%s", provider, model, reasoning)
		}
		return model, 8_000, compact, nil
	}, config.ModelRouteConfig{Provider: "grok", Model: "summary-model", Reasoning: "high"}, "chatgpt", "main-model", "low", "session-1:compaction", nil, func(_, _, _, _ string, usage hyprovider.Usage, _, _ int) { reported = usage })
	if resolveCalls != 0 {
		t.Fatal("compaction driver resolved eagerly")
	}
	if _, err := summarize(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || len(compact.requests) != 1 || compact.requests[0].Model != "summary-model" {
		t.Fatalf("resolve calls=%d requests=%#v", resolveCalls, compact.requests)
	}
	request := compact.requests[0]
	if request.ExtraBody["prompt_cache_key"] != "session-1:compaction" || request.Metadata["reasoning_effort"] != "high" {
		t.Fatalf("compaction request metadata=%#v extra=%#v", request.Metadata, request.ExtraBody)
	}
	if reported.TotalTokens != 10 {
		t.Fatalf("reported compaction usage = %#v", reported)
	}
}

func TestLazyCompactionDefaultsToLowReasoning(t *testing.T) {
	compact := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	summarize := lazyCompactionSummarizer(func(_ context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
		if provider != "grok" || model != "grok-4.5" || reasoning != "low" {
			t.Fatalf("inherited compaction route = %s/%s/%s", provider, model, reasoning)
		}
		return model, 500_000, compact, nil
	}, config.ModelRouteConfig{}, "grok", "grok-4.5", "high", "session-1:compaction", nil, nil)
	if _, err := summarize(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	request := compact.requests[0]
	if request.Metadata["reasoning_effort"] != "low" || request.ExtraBody["prompt_cache_key"] != "session-1:compaction" {
		t.Fatalf("default compaction request metadata=%#v extra=%#v", request.Metadata, request.ExtraBody)
	}
}

func TestCompactionSummarizerRejectsOversizedInputWithoutClipping(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "## Objective\n- continue"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	transcript := "header\n<transcript>\n" + strings.Repeat("ROLE assistant\nTEXT old data\n", 500) + "ROLE user\nTEXT newest evidence\n</transcript>"
	if _, err := compactionSummarizer(inner, "grok", "model", "low", "cache", 1_000, 200)(context.Background(), transcript); err == nil || len(inner.requests) != 0 {
		t.Fatalf("oversized summary input requests=%d error=%v", len(inner.requests), err)
	}
	oversizedOutput := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("summary", 200)},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	if _, err := compactionSummarizer(oversizedOutput, "grok", "model", "low", "cache", 1_000, 200)(context.Background(), "small input"); err == nil || !strings.Contains(err.Error(), "summary output requires") {
		t.Fatalf("oversized summary output error=%v", err)
	}
	chatGPT := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	if _, err := compactionSummarizer(chatGPT, "chatgpt", "model", "low", "cache", 1_000, 200)(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	if extra := chatGPT.requests[0].ExtraBody; extra["prompt_cache_key"] != "cache" || extra["max_output_tokens"] != nil {
		t.Fatalf("ChatGPT summary extra body: %#v", extra)
	}
}

func TestManualCompactWaitsForConfiguredModelAndPersistsSummary(t *testing.T) {
	ctx := context.Background()
	var responseCalls atomic.Int32
	var responseBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-main","title":"Main","context_window":128000,"supported_reasoning_levels":["minimal"],"default_reasoning_level":"minimal","supports_tools":true},{"slug":"gpt-summary","title":"Summary","context_window":128000,"supported_reasoning_levels":["minimal"],"default_reasoning_level":"minimal","supports_tools":true}]}`))
		case "/responses":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read compaction request: %v", err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			responseBody = string(body)
			responseCalls.Add(1)
			writer.Header().Set("Content-Type", "text/event-stream")
			writeProviderText(writer, "compact-response", "## Objective\n- preserve the task")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL + "/models"
	modelCatalog.AdditionalEndpoints["chatgpt"] = nil
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)

	cfg := config.Default()
	cfg.Agents.Compaction = config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-summary", Reasoning: "minimal"}
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Compact", ProviderID: "chatgpt", ModelID: "gpt-main", Reasoning: "minimal"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		kind := "user"
		if index%2 == 1 {
			kind = "assistant"
		}
		if _, err := sessions.AppendBlock(ctx, "session-1", session.Block{Kind: kind, Content: fmt.Sprintf("message %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	todo, err := sessions.UpdateTodo(ctx, "session-1", 0, func(todo *session.TodoList) error {
		todo.Goal = "retain todo"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	host := NewService(ctx, cfg)
	host.AttachDurable(sessions, coding)
	host.AttachProviderRuntime(providerRuntime)

	if err := host.ExecuteAction(ctx, Action{Kind: ActionCompact, Target: "session-1"}); err != nil {
		t.Fatal(err)
	}
	if responseCalls.Load() != 1 || !strings.Contains(responseBody, `"model":"gpt-summary"`) {
		t.Fatalf("compaction requests=%d body=%s", responseCalls.Load(), responseBody)
	}
	var event Event
	usageReported := false
	for event.Kind != EventSessionLoaded {
		event, err = host.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == EventContextUsage && event.Data["requestKind"] == "compaction" {
			usageReported = event.Data["inputTokens"] == "10" && event.Data["outputTokens"] == "4" && event.Data["transport"] == "chatgpt-codex-responses"
		}
	}
	if event.Kind != EventSessionLoaded || event.State != "compacted" || event.Todo == nil || event.Todo.Revision != todo.Revision {
		t.Fatalf("compaction event = %+v", event)
	}
	if !usageReported {
		t.Fatal("manual compaction usage was not reported independently")
	}
	projection, err := sessions.LoadProjection(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 8 || projection.ModelHistory.SummaryHash == "" || projection.ModelHistory.CoveredThroughSequence == nil {
		t.Fatalf("persisted compaction = %#v", projection.Blocks)
	}
	if projection.Usage.CompactionInput != 10 || projection.Usage.CompactionOutput != 4 || projection.Usage.InputTokens != 0 || projection.Usage.OutputTokens != 0 {
		t.Fatalf("persisted manual compaction usage = %#v", projection.Usage)
	}
}

func TestTurnContextCompactToSummaryFailurePreservesHistory(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, "old"),
		message.NewText(message.RoleAssistant, strings.Repeat("work ", 300)),
		message.NewText(message.RoleUser, "latest"),
	}
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "partial", errors.New("offline") }}
	got, err := manager.CompactTo(context.Background(), history, 100)
	if err == nil || !strings.Contains(err.Error(), "offline") || !reflect.DeepEqual(got, history) {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestTurnContextCompactToHooksOnlyBracketRequiredCompaction(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, "old"),
		message.NewText(message.RoleAssistant, strings.Repeat("work ", 300)),
		message.NewText(message.RoleUser, "latest"),
	}
	summaries, pre, post := 0, 0, 0
	manager := turnContext{
		summarize: func(context.Context, string) (string, error) {
			summaries++
			return "summary", nil
		},
		compactHooks: func(_ context.Context, _ []message.Message, compacted []message.Message, _ error) error {
			if compacted == nil {
				pre++
			} else {
				post++
			}
			return nil
		},
	}
	for range 100 {
		if got, err := manager.CompactTo(context.Background(), history, estimateContextTokens(history)); err != nil || !reflect.DeepEqual(got, history) {
			t.Fatalf("subthreshold compaction changed history: got=%#v err=%v", got, err)
		}
	}
	if summaries != 0 || pre != 0 || post != 0 {
		t.Fatalf("subthreshold calls: summarize=%d pre=%d post=%d", summaries, pre, post)
	}
	if _, err := manager.CompactTo(context.Background(), history, 100); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 || pre != 1 || post != 1 {
		t.Fatalf("changed call: summarize=%d pre=%d post=%d", summaries, pre, post)
	}
}

func phase5History() []message.Message {
	return []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old request ", 120)),
		message.NewText(message.RoleAssistant, strings.Repeat("old answer ", 120)),
		message.NewText(message.RoleUser, "latest request"),
	}
}

func TestPhase5BelowSoftDoesNotPrepareOrHook(t *testing.T) {
	history := phase5History()
	var calls, hooks atomic.Int32
	manager := turnContext{
		softTriggerTokens: estimateContextTokens(history) + 1, backgroundPrepare: true,
		compactTargetTokens: 100, coordinator: &compactionCoordinator{},
		summarize:    func(context.Context, string) (string, error) { calls.Add(1); return "summary", nil },
		compactHooks: func(context.Context, []message.Message, []message.Message, error) error { hooks.Add(1); return nil },
	}
	for range 100 {
		got, err := manager.CompactTo(context.Background(), history, estimateContextTokens(history)+100)
		if err != nil || !reflect.DeepEqual(got, history) {
			t.Fatalf("below-soft result changed: err=%v", err)
		}
	}
	if calls.Load() != 0 || hooks.Load() != 0 {
		t.Fatalf("calls=%d hooks=%d", calls.Load(), hooks.Load())
	}
}

func TestPhase5SoftPrepareReturnsImmediatelyAndStartsOnce(t *testing.T) {
	history := phase5History()
	tokens := estimateContextTokens(history)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	manager := turnContext{
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{}, summarize: func(ctx context.Context, _ string) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
				return "summary", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
	if got, err := manager.CompactTo(context.Background(), history, tokens+100); err != nil || !reflect.DeepEqual(got, history) {
		t.Fatalf("soft result changed: err=%v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	for range 20 {
		if _, err := manager.CompactTo(context.Background(), history, tokens+100); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("workers=%d", calls.Load())
	}
	close(release)
	select {
	case <-manager.coordinator.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestPhase5HardTriggerUsesPreparedResultAndActivatesOnce(t *testing.T) {
	history := phase5History()
	tokens := estimateContextTokens(history)
	release := make(chan struct{})
	var calls, pre, post, activations atomic.Int32
	manager := turnContext{
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			<-release
			return "prepared summary", nil
		},
		compactHooks: func(_ context.Context, _ []message.Message, compacted []message.Message, _ error) error {
			if compacted == nil {
				pre.Add(1)
			} else {
				post.Add(1)
			}
			return nil
		},
		activateCompaction: func(context.Context, []message.Message, string) error { activations.Add(1); return nil },
	}
	if got, err := manager.CompactTo(context.Background(), history, tokens+100); err != nil || !reflect.DeepEqual(got, history) {
		t.Fatalf("soft result changed: err=%v", err)
	}
	close(release)
	select {
	case <-manager.coordinator.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	backgroundCalls := calls.Load()
	got, err := manager.CompactTo(context.Background(), history, tokens-1)
	if err != nil {
		t.Fatal(err)
	}
	if estimateContextTokens(got) > 100 || calls.Load() != backgroundCalls {
		t.Fatalf("prepared result tokens=%d calls=%d background=%d", estimateContextTokens(got), calls.Load(), backgroundCalls)
	}
	if pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("pre=%d post=%d activations=%d", pre.Load(), post.Load(), activations.Load())
	}
	if again, againErr := manager.CompactTo(context.Background(), history, tokens-1); againErr != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("repeated prepared activation result changed: err=%v", againErr)
	}
	if pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("repeated activation: pre=%d post=%d activations=%d", pre.Load(), post.Load(), activations.Load())
	}
}

func TestPhase5CompletedSoftPreparationActivatesBeforeHardLimit(t *testing.T) {
	history := phase5History()
	tokens := estimateContextTokens(history)
	var activations atomic.Int32
	manager := turnContext{
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		summarize:   func(context.Context, string) (string, error) { return "prepared state", nil },
		activateCompaction: func(context.Context, []message.Message, string) error {
			activations.Add(1)
			return nil
		},
	}
	if got, err := manager.CompactTo(context.Background(), history, tokens+100); err != nil || !reflect.DeepEqual(got, history) {
		t.Fatalf("soft preparation changed first request: history=%+v error=%v", got, err)
	}
	<-manager.coordinator.done
	got, err := manager.CompactTo(context.Background(), history, tokens+100)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got, history) || activations.Load() != 1 {
		t.Fatalf("completed soft checkpoint was not activated: activations=%d history=%+v", activations.Load(), got)
	}
}

func TestPhase5PreparedResultAcceptsAppendOnlyTail(t *testing.T) {
	historyA := phase5History()
	tokensA := estimateContextTokens(historyA)
	var calls, pre, post, activations atomic.Int32
	manager := turnContext{
		softTriggerTokens: tokensA - 1, backgroundPrepare: true, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			return "source-specific summary", nil
		},
		compactHooks: func(_ context.Context, _ []message.Message, compacted []message.Message, _ error) error {
			if compacted == nil {
				pre.Add(1)
			} else {
				post.Add(1)
			}
			return nil
		},
		activateCompaction: func(context.Context, []message.Message, string) error { activations.Add(1); return nil },
	}
	if _, err := manager.CompactTo(context.Background(), historyA, tokensA+100); err != nil {
		t.Fatal(err)
	}
	<-manager.coordinator.done
	preparedA := append([]message.Message(nil), manager.coordinator.result...)
	historyB := append(append([]message.Message(nil), historyA...),
		message.NewText(message.RoleAssistant, "answer before source B"),
		message.NewText(message.RoleUser, "SOURCE B CURRENT CONTENT"))
	got, err := manager.CompactTo(context.Background(), historyB, estimateContextTokens(historyB)-1)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got, preparedA) {
		t.Fatal("prepared checkpoint did not include the uncovered tail")
	}
	if !strings.Contains(got[len(got)-1].Text, "SOURCE B CURRENT CONTENT") {
		t.Fatalf("current source B content lost: %#v", got)
	}
	if calls.Load() != 1 || pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("calls=%d pre=%d post=%d activations=%d", calls.Load(), pre.Load(), post.Load(), activations.Load())
	}
}

func TestPhase5SynchronousHardActivationOnce(t *testing.T) {
	history := phase5History()
	tokens := estimateContextTokens(history)
	var calls, pre, post, activations atomic.Int32
	manager := turnContext{
		softTriggerTokens: tokens - 1, backgroundPrepare: false, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		summarize:   func(context.Context, string) (string, error) { calls.Add(1); return "synchronous summary", nil },
		compactHooks: func(_ context.Context, _ []message.Message, compacted []message.Message, _ error) error {
			if compacted == nil {
				pre.Add(1)
			} else {
				post.Add(1)
			}
			return nil
		},
		activateCompaction: func(context.Context, []message.Message, string) error { activations.Add(1); return nil },
	}
	got, err := manager.CompactTo(context.Background(), history, tokens-1)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("calls=%d pre=%d post=%d activations=%d", calls.Load(), pre.Load(), post.Load(), activations.Load())
	}
	if again, againErr := manager.CompactTo(context.Background(), got, 100); againErr != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("already compacted result changed: err=%v", againErr)
	}
	if calls.Load() != 1 || pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("repeated result calls=%d pre=%d post=%d activations=%d", calls.Load(), pre.Load(), post.Load(), activations.Load())
	}
}

func TestPhase5CancelledOrFailedPreparationPreservesHistoryWithoutPostHook(t *testing.T) {
	history := phase5History()
	tokens := estimateContextTokens(history)
	started := make(chan struct{})
	var hooks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	manager := turnContext{
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		summarize: func(ctx context.Context, _ string) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
		compactHooks: func(context.Context, []message.Message, []message.Message, error) error { hooks.Add(1); return nil },
	}
	got, err := manager.CompactTo(ctx, history, tokens+100)
	if err != nil || !reflect.DeepEqual(got, history) {
		t.Fatalf("soft result changed: err=%v", err)
	}
	<-started
	cancel()
	select {
	case <-manager.coordinator.done:
	case <-time.After(time.Second):
		t.Fatal("cancelled worker did not finish")
	}
	if hooks.Load() != 0 {
		t.Fatalf("cancelled preparation hooks=%d", hooks.Load())
	}
}

func TestActiveGuidanceSurvivesGeneratedCompaction(t *testing.T) {
	snapshot := activeGuidanceSnapshot{values: []string{"first correction", "second correction"}}
	acknowledged := false
	manager := activeGuidanceContext{
		inner: turnContext{summarize: func(context.Context, string) (string, error) { return "Objective: retain guidance", nil }},
		peek:  func() activeGuidanceSnapshot { return snapshot },
		acknowledge: func(got activeGuidanceSnapshot) {
			acknowledged = reflect.DeepEqual(got, snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old ", 400)),
		message.NewText(message.RoleAssistant, "done"),
	}
	got, err := manager.CompactTo(context.Background(), history, 100)
	if err != nil {
		t.Fatal(err)
	}
	latest := got[len(got)-1].Text
	if !strings.Contains(latest, "first correction") || !strings.Contains(latest, "second correction") {
		t.Fatalf("trailing guidance lost: %#v", got)
	}
	if !acknowledged {
		t.Fatal("successful compaction did not acknowledge guidance")
	}
}

func TestActiveGuidanceRemainsQueuedWhenCompactionFails(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.mu.Unlock()
	if err := service.GuideActiveTurn("session-guided", "run-guided", "do not lose this"); err != nil {
		t.Fatal(err)
	}
	manager := activeGuidanceContext{
		inner: turnContext{},
		peek:  func() activeGuidanceSnapshot { return service.peekActiveGuidance("session-guided", "run-guided") },
		acknowledge: func(snapshot activeGuidanceSnapshot) {
			service.acknowledgeActiveGuidance("session-guided", "run-guided", snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("mandatory context ", 100)),
	}
	if _, err := manager.CompactTo(context.Background(), history, 1); err == nil {
		t.Fatal("expected compaction failure")
	}
	remaining := service.drainActiveGuidance("session-guided", "run-guided")
	if len(remaining) != 1 || remaining[0] != "do not lose this" {
		t.Fatalf("guidance after failed compaction = %#v", remaining)
	}
}

func TestTurnContextCompactToSummarizesOversizedCompletedToolResult(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "read the file"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "read_file"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "read-1", Name: "read_file", Content: "prefix" + string([]byte{0xff}) + strings.Repeat("文件内容", 2_000)}),
	}
	const target = 1_000
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "summary", nil }}
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil || reflect.DeepEqual(compacted, history) {
		t.Fatalf("oversized tool result history=%#v error=%v", compacted, err)
	}
	assertRollingToolCheckpoint(t, compacted, history[1])
}

func TestTurnContextCompactToSummarizesDuplicatedStructuredToolOutput(t *testing.T) {
	content := strings.Repeat("package recovery\n", 800)
	structured, err := json.Marshal(map[string]any{"content": content, "lineCount": 800})
	if err != nil {
		t.Fatal(err)
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "inspect recovery"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{
			ToolCallID: "read-1", Name: "coding.read_file", Content: content, Structured: structured,
		}),
	}

	const target = 2_000
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "summary", nil }}
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil || reflect.DeepEqual(compacted, history) {
		t.Fatalf("duplicated structured output history=%#v error=%v", compacted, err)
	}
	assertRollingToolCheckpoint(t, compacted, history[1])
}

func TestTurnContextCompactToUsesContentInsteadOfDuplicatedStructuredOutput(t *testing.T) {
	content := strings.Repeat("x", 3_000)
	structured, err := json.Marshal(map[string]string{"content": strings.Repeat("y", 8_000)})
	if err != nil {
		t.Fatal(err)
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "inspect"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{
			ToolCallID: "read-1", Name: "coding.read_file", Content: content, Structured: structured,
		}),
	}

	const target = 1_000
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	result := compacted[len(compacted)-1].ToolResult
	if result == nil || result.Content != content || !bytes.Equal(result.Structured, structured) {
		t.Fatalf("provider-visible content was needlessly compacted: %#v", result)
	}
}

func TestTurnContextExternalizesLargeToolResultBeforeSoftTrigger(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleUser, "inspect"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "read-1", Name: "coding.read_file", Content: strings.Repeat("payload", 100)}),
	}
	var stored []byte
	manager := turnContext{
		largeToolTokens: 1, softTriggerTokens: 100_000, compactTargetTokens: 100,
		coordinator: &compactionCoordinator{},
		putArtifact: func(_ context.Context, kind string, payload []byte, preview string) (session.ContextArtifact, error) {
			stored = append([]byte(nil), payload...)
			return session.ContextArtifact{ID: "artifact-1", SHA256: "digest"}, nil
		},
	}
	prepared, err := manager.CompactTo(context.Background(), history, 90_000)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != history[2].ToolResult.Content {
		t.Fatalf("stored payload length=%d, want %d", len(stored), len(history[2].ToolResult.Content))
	}
	result := prepared[2].ToolResult
	if result == nil || !strings.Contains(result.Content, `"artifact_ref":"artifact-1"`) || len(result.Structured) != 0 {
		t.Fatalf("provider result was not replaced before threshold evaluation: %#v", result)
	}
}

func TestTurnContextCompactToCanSummarizeLatestCompletedResultThatCannotFit(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleSystem, "rules")}
	for index := 0; index < 260; index++ {
		id := fmt.Sprintf("old-%d", index)
		history = append(history,
			message.NewText(message.RoleUser, "old"),
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "read"}}},
			message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "read", Content: "ok"}),
		)
	}
	history = append(history,
		message.NewText(message.RoleUser, "latest"),
		message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "latest", Name: "read"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "latest", Name: "read", Content: strings.Repeat("z", 2_000)}),
	)

	const target = 64
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "summary", nil }}
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil || reflect.DeepEqual(compacted, history) {
		t.Fatalf("mandatory oversized result history=%#v error=%v", compacted, err)
	}
	assertRollingToolCheckpoint(t, compacted, history[len(history)-3])
}

func TestProviderStreamSinkDoesNotReportMissingUsageAsZero(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	if err := service.providerStreamSink("session", "run", "grok", "model", "high", "xai-responses").Emit(context.Background(), stream.Frame{Kind: stream.FrameDone}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventContextUsage || event.State != "reported" {
		t.Fatalf("event = %+v", event)
	}
	if event.Data["inputTokens"] != "" || event.Data["cachedInputTokens"] != "" || event.Data["outputTokens"] != "" || event.Data["totalTokens"] != "" || event.Data["cacheStatus"] != "" {
		t.Fatalf("missing provider usage was reported as tokens: %+v", event.Data)
	}
}

func TestModelContextTokenTargetRequiresCatalogMetadataAndAvoidsOverflow(t *testing.T) {
	if _, err := modelContextTokenTarget("grok", "missing", 0, 0); err == nil {
		t.Fatal("missing context window was accepted")
	}
	maxInt := int(^uint(0) >> 1)
	target, err := modelContextTokenTarget("chatgpt", "large", maxInt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if target <= 0 || target > maxInt {
		t.Fatalf("large context target = %d", target)
	}
	if target, err := modelContextTokenTarget("grok", "grok-4.5", 500_000, 2_000); err != nil || target != 169_808 {
		t.Fatalf("xAI long-context target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("grok", "grok-build", 256_000, 0); err != nil || target != 171_808 {
		t.Fatalf("xAI build target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("chatgpt", "gpt", 500_000, 2_000); err != nil || target != 364_808 {
		t.Fatalf("standard target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("chatgpt", "gpt-5.6-sol", 1_050_000, 2_000); err != nil || target != 239_808 {
		t.Fatalf("GPT-5.6 pricing-aware target = %d, error=%v", target, err)
	}
}

type yoloApprovalDriver struct{}

func (yoloApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "test.write", Description: "write", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (yoloApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "written"}, nil
}

func TestYoloApprovalModeResolvesDurableCodingApproval(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	host := NewService(ctx, config.Default())
	host.coding = coding
	defer host.cancel()
	if err := host.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "write")
	if err != nil {
		t.Fatal(err)
	}
	call := tool.Call{ID: "write-1", Name: "test.write"}
	execution, err := coding.ExecuteDriver(ctx, run, yoloApprovalDriver{}, call, nil)
	if err != nil || execution.Approval == nil || execution.Executed {
		t.Fatalf("pending execution = %#v err:%v", execution, err)
	}
	resolution, err := host.awaitApproval(ctx, "session", "", "main", run, call, *execution.Approval)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("yolo resolution = mode:%q err:%v", resolution.Mode, err)
	}
	if decider := durableApprovalDecider(t, coding, run.RunID); decider != "approval-mode:yolo" {
		t.Fatalf("YOLO durable decider=%q", decider)
	}
	execution, err = coding.ExecuteDriver(ctx, run, yoloApprovalDriver{}, call, nil)
	if err != nil || !execution.Executed || execution.Result.IsError || execution.Result.Content != "written" {
		t.Fatalf("approved execution = %#v err:%v", execution, err)
	}
	modeEvent, err := host.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "yolo" {
		t.Fatalf("YOLO mode projection=%+v error=%v", modeEvent, err)
	}
	if err := coding.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
}

func TestYoloApprovalModeDrainsPendingAndSkipsFuturePrompts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service := NewService(ctx, config.Default())
	definition := tool.Definition{
		Name: "coding.write_file", EffectType: tool.EffectWrite, RequiresApproval: true, RiskLevel: "high",
	}
	type approvalResult struct {
		mode agentservice.ApprovalMode
		err  error
	}
	await := func(call tool.Call) <-chan approvalResult {
		result := make(chan approvalResult, 1)
		go func() {
			resolution, err := service.awaitTeamApproval(ctx, "session", "run", "goal", call, definition)
			result <- approvalResult{mode: resolution.Mode, err: err}
		}()
		return result
	}

	first := await(tool.Call{ID: "write-1", Name: definition.Name})
	requested, err := service.NextEvent(ctx)
	if err != nil || requested.Kind != EventApprovalRequested || requested.ToolCallID != "write-1" {
		t.Fatalf("initial prompt = event:%+v err:%v", requested, err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	if result := <-first; result.err != nil || result.mode != agentservice.ApprovalOnce {
		t.Fatalf("drained approval = mode:%q err:%v", result.mode, result.err)
	}
	resolved, err := service.NextEvent(ctx)
	if err != nil || resolved.Kind != EventApprovalResolved || resolved.ApprovalID != requested.ApprovalID {
		t.Fatalf("drained event = event:%+v err:%v", resolved, err)
	}
	modeEvent, err := service.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "yolo" {
		t.Fatalf("YOLO mode event=%+v error=%v", modeEvent, err)
	}

	resolution, err := service.awaitTeamApproval(ctx, "session", "run", "goal", tool.Call{ID: "write-2", Name: definition.Name}, definition)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("yolo approval = mode:%q err:%v", resolution.Mode, err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModePrompt)}); err != nil {
		t.Fatal(err)
	}
	modeEvent, err = service.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "prompt" {
		t.Fatalf("prompt mode event=%+v error=%v", modeEvent, err)
	}
	third := await(tool.Call{ID: "write-3", Name: definition.Name})
	requested, err = service.NextEvent(ctx)
	if err != nil || requested.Kind != EventApprovalRequested || requested.ToolCallID != "write-3" {
		t.Fatalf("restored prompt = event:%+v err:%v", requested, err)
	}
	if _, err := service.resolveLiveApproval(ctx, requested.ApprovalID, "once", "user"); err != nil {
		t.Fatal(err)
	}
	if result := <-third; result.err != nil || result.mode != agentservice.ApprovalOnce {
		t.Fatalf("prompt approval = mode:%q err:%v", result.mode, result.err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: "unsafe"}); err == nil {
		t.Fatal("invalid approval mode was accepted")
	}
}

type skillRuntimeHarness struct {
	service        *Service
	calls          *atomic.Int32
	workspace      string
	catalog        *skills.Catalog
	definitionPath string
}

func newSkillRuntimeHarness(t *testing.T, definition string, resources map[string]string, respond func(int, string, http.ResponseWriter)) skillRuntimeHarness {
	t.Helper()
	ctx := context.Background()
	workspace := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range resources {
		path := filepath.Join(skillDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var responseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-skill","title":"GPT Skill","context_window":128000,"supported_reasoning_levels":["minimal"],"default_reasoning_level":"minimal","supports_tools":true}]}`))
		case "/responses":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read provider request: %v", err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			respond(int(responseCalls.Add(1)), string(body), writer)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))

	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL + "/models"
	modelCatalog.AdditionalEndpoints["chatgpt"] = nil
	skillCatalog, err := skills.Load(skills.LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{skillRoot},
	}})
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, workspace, agentservice.WithSkills(skillCatalog))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	service := NewService(ctx, cfg)
	service.AttachDurable(session.NewService(store.DB()), coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachProviderRuntime(providerRuntime)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		server.Close()
		if err := store.Close(shutdownCtx); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return skillRuntimeHarness{
		service: service, calls: &responseCalls, workspace: workspace,
		catalog: skillCatalog, definitionPath: filepath.Join(skillDir, "SKILL.md"),
	}
}

func writeProviderToolCall(writer http.ResponseWriter, responseID, callID, name, arguments string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":%q,\"call_id\":%q,\"name\":%q,\"arguments\":%q}}\n\n", responseID+"-item", callID, name, arguments)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func writeProviderText(writer http.ResponseWriter, responseID, text string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func waitForProviderRun(t *testing.T, service *Service, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventRunFinished:
			return
		case EventRunFailed, EventRunCancelled:
			t.Fatalf("run ended as %s: %s", event.Kind, event.Text)
		}
	}
}

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
			if event.ToolCallID != "write-approval" || event.Data["tool"] != "coding.write_file" {
				t.Fatalf("approval event = %+v", event)
			}
			if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("file was created before approval, stat error = %v", err)
			}
			if err := harness.service.ExecuteAction(context.Background(), Action{
				Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once",
			}); err != nil {
				t.Fatal(err)
			}
			approved = true
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
			if harness.calls.Load() != 2 {
				t.Fatalf("provider calls = %d, want 2", harness.calls.Load())
			}
			return
		}
	}
}

type countedApprovalDriver struct {
	executions *atomic.Int32
}

func (d countedApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "test.auto_write", Description: "write under automatic review", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (d countedApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.executions.Add(1)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "executed"}, nil
}

type autoReviewHarness struct {
	host           *Service
	runtime        *ProviderRuntime
	coding         *agentservice.Service
	authentication *auth.Service
	run            *agentservice.Run
	driver         countedApprovalDriver
}

func TestAutoReviewAllowUsesGoalArgumentsAndApprovesOnlyOnce(t *testing.T) {
	var requestChecked atomic.Bool
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != codex.ApprovalReviewerModel {
			t.Errorf("review model=%v", body["model"])
		}
		if _, found := body["tools"]; found {
			t.Errorf("automatic reviewer received tools: %v", body["tools"])
		}
		input, _ := body["input"].([]any)
		entry, _ := input[0].(map[string]any)
		content, _ := entry["content"].([]any)
		part, _ := content[0].(map[string]any)
		var evidence map[string]any
		if err := json.Unmarshal([]byte(part["text"].(string)), &evidence); err != nil {
			t.Error(err)
		}
		arguments, _ := evidence["arguments"].(map[string]any)
		if evidence["goal"] != "original user goal" || evidence["agent_id"] != "agent-1" ||
			evidence["tool_name"] != "test.auto_write" || arguments["path"] != "precise.txt" {
			t.Errorf("review evidence=%v", evidence)
		}
		requestChecked.Store(true)
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized bounded write"}`, true)
	})
	modeEvent := nextApprovalEvent(t, harness.host, EventApprovalMode)
	if modeEvent.State != "auto_review" || modeEvent.Data["auto_review_available"] != "true" {
		t.Fatalf("automatic capability event=%+v", modeEvent)
	}
	call := tool.Call{ID: "allow-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"precise.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("automatic allow=%+v error=%v", resolution, err)
	}
	if !requestChecked.Load() {
		t.Fatal("automatic review request was not inspected")
	}
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if reviewing.State != "reviewing" || reviewing.ApprovalID == "" {
		t.Fatalf("reviewing event=%+v", reviewing)
	}
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["risk"] != "medium" ||
		resolved.Data["user_authorization"] != "high" || resolved.Data["reviewer"] != codex.ApprovalReviewerModel {
		t.Fatalf("resolved event=%+v", resolved)
	}
	executed, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || !executed.Executed || harness.driver.executions.Load() != 1 {
		t.Fatalf("approved execution=%+v count=%d error=%v", executed, harness.driver.executions.Load(), err)
	}
	repeated, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || repeated.Executed || repeated.Approval == nil || harness.driver.executions.Load() != 1 {
		t.Fatalf("approval was not once-only: result=%+v count=%d error=%v", repeated, harness.driver.executions.Load(), err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerModel {
		t.Fatalf("durable decider=%q", decider)
	}
}

func TestAutoReviewTeamDecisionWritesDurableAudit(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized team write"}`, true)
	})
	call := tool.Call{ID: "team-allow", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team.txt"}`)}
	resolution, err := harness.host.awaitTeamApproval(
		context.Background(), "session", harness.run.RunID, "team user goal", call, harness.driver.Definition(),
	)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team automatic approval=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" {
		t.Fatalf("team automatic event=%+v", resolved)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerModel {
		t.Fatalf("team durable decider=%q", decider)
	}
}

func TestAutoReviewTeamDenyFallsBackToUserApproval(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"team action needs confirmation"}`, true)
	})
	call := tool.Call{ID: "team-deny", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team.txt"}`)}
	type approvalResult struct {
		resolution approvalResolution
		err        error
	}
	result := make(chan approvalResult, 1)
	go func() {
		resolution, err := harness.host.awaitTeamApproval(
			context.Background(), "session", harness.run.RunID, "team user goal", call, harness.driver.Definition(),
		)
		result <- approvalResult{resolution: resolution, err: err}
	}()
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if reviewing.State != "reviewing" {
		t.Fatalf("team reviewing event=%+v", reviewing)
	}
	denied := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if denied.State != "auto_denied" {
		t.Fatalf("team automatic denial=%+v", denied)
	}
	prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
		t.Fatalf("team manual fallback=%+v", prompt)
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
	}); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team user approval=%+v error=%v", outcome.resolution, outcome.err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != "user" {
		t.Fatalf("team durable decider=%q", decider)
	}
}

func TestAutoReviewDoesNotInvokeInteractivePermissionHooks(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	var reviews atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		reviews.Add(1)
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized write"}`, true)
	})
	hookPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(hookPath, []byte(`{"hooks":{"PermissionRequest":[{"matcher":"*","hooks":[{"name":"interactive-bridge","type":"command","command":"printf 'interactive permission hook ran' >&2; exit 2"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.host.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: hookPath, Trusted: true}}}),
	})

	call := tool.Call{ID: "hook-skip", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"reviewed.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("automatic approval was intercepted by interactive hook: resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved); resolved.State != "auto_approved" {
		t.Fatalf("automatic resolution=%+v", resolved)
	}

	teamCall := tool.Call{ID: "team-hook-skip", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team-reviewed.txt"}`)}
	resolution, err = harness.host.awaitTeamApproval(context.Background(), "session", harness.run.RunID, "team goal", teamCall, harness.driver.Definition())
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team automatic approval was intercepted by interactive hook: resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved); resolved.State != "auto_approved" {
		t.Fatalf("team automatic resolution=%+v", resolved)
	}
	if reviews.Load() != 2 {
		t.Fatalf("automatic reviewer calls=%d, want 2", reviews.Load())
	}
}

func TestAutoReviewDenyFallsBackToUserWhileMalformedFailureStaysClosed(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantState  string
		wantText   string
		errorKind  string
		promptUser bool
	}{
		{
			name:      "explicit deny",
			output:    `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"target is not authorized"}`,
			wantState: "auto_denied", wantText: "Denied by automatic review", errorKind: "", promptUser: true,
		},
		{
			name: "malformed output", output: `{`,
			wantState: "auto_failed", wantText: "Automatic review failed", errorKind: "parse",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
				writeAutomaticReview(writer, test.output, true)
			})
			call := tool.Call{ID: "denied-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
			pending := prepareAutomaticApproval(t, harness, call)
			type approvalResult struct {
				resolution approvalResolution
				err        error
			}
			result := make(chan approvalResult, 1)
			go func() {
				resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
				result <- approvalResult{resolution: resolution, err: err}
			}()
			reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
			if reviewing.State != "reviewing" {
				t.Fatalf("reviewing event=%+v", reviewing)
			}
			resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
			if resolved.State != test.wantState || resolved.Data["error_kind"] != test.errorKind {
				t.Fatalf("resolved event=%+v", resolved)
			}
			if !strings.Contains(resolved.Text, test.wantText) {
				t.Fatalf("automatic resolution text=%q", resolved.Text)
			}
			if test.wantState == "auto_failed" && (resolved.Data["risk"] != "high" ||
				!strings.Contains(resolved.Data["rationale"], "Automatic approval review failed (parse)")) {
				t.Fatalf("fail-closed review omitted diagnostic assessment: %+v", resolved)
			}
			if test.promptUser {
				prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
				if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
					t.Fatalf("manual fallback event=%+v", prompt)
				}
				if err := harness.host.ExecuteAction(context.Background(), Action{
					Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
				}); err != nil {
					t.Fatal(err)
				}
			}
			outcome := <-result
			wantMode := agentservice.ApprovalDenied
			wantDecider := "system:auto-review-failure"
			if test.promptUser {
				wantMode = agentservice.ApprovalOnce
				wantDecider = "user"
			}
			if outcome.err != nil || outcome.resolution.Mode != wantMode {
				t.Fatalf("approval outcome=%+v error=%v", outcome.resolution, outcome.err)
			}
			if harness.driver.executions.Load() != 0 {
				t.Fatalf("approval flow executed tool prematurely %d times", harness.driver.executions.Load())
			}
			if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != wantDecider {
				t.Fatalf("durable decider=%q", decider)
			}
		})
	}
}

func TestAutoReviewTimeoutFallsBackToUserApproval(t *testing.T) {
	var modelsMu sync.Mutex
	var models []string
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		modelsMu.Lock()
		models = append(models, body.Model)
		modelsMu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	harness.runtime.approvalReviewTimeout = 20 * time.Millisecond
	call := tool.Call{ID: "timeout-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	type approvalResult struct {
		resolution approvalResolution
		err        error
	}
	result := make(chan approvalResult, 1)
	go func() {
		resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
		result <- approvalResult{resolution: resolution, err: err}
	}()
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_timed_out" || resolved.Data["error_kind"] != "timeout" {
		t.Fatalf("timeout event=%+v", resolved)
	}
	modelsMu.Lock()
	gotModels := append([]string(nil), models...)
	modelsMu.Unlock()
	wantModels := []string{codex.ApprovalReviewerModel, codex.ApprovalReviewerFallbackModel, codex.ApprovalReviewerModel}
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("review models=%v, want %v", gotModels, wantModels)
	}
	prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
		t.Fatalf("timeout manual fallback=%+v", prompt)
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("timed-out review executed tool")
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
	}); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("timeout resolution=%+v error=%v", outcome.resolution, outcome.err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != "user" {
		t.Fatalf("timeout decider=%q", decider)
	}
	executed, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || !executed.Executed || harness.driver.executions.Load() != 1 {
		t.Fatalf("user-approved execution=%+v count=%d error=%v", executed, harness.driver.executions.Load(), err)
	}
}

func TestAutoReviewTimeoutSwitchesModelAndUsesSuccessfulRetry(t *testing.T) {
	var requests atomic.Int32
	var modelsMu sync.Mutex
	var models []string
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		modelsMu.Lock()
		models = append(models, body.Model)
		modelsMu.Unlock()
		if requests.Add(1) == 1 {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			return
		}
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"fallback model completed review"}`, true)
	})
	harness.runtime.approvalReviewTimeout = 20 * time.Millisecond
	call := tool.Call{ID: "timeout-retry", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"retried.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce || resolution.NeedsUserApproval {
		t.Fatalf("retry resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["error_kind"] != "" {
		t.Fatalf("retry event=%+v", resolved)
	}
	modelsMu.Lock()
	gotModels := append([]string(nil), models...)
	modelsMu.Unlock()
	wantModels := []string{codex.ApprovalReviewerModel, codex.ApprovalReviewerFallbackModel}
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("review models=%v, want %v", gotModels, wantModels)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerFallbackModel {
		t.Fatalf("retry decider=%q", decider)
	}
}

func TestAutoReviewCallerDeadlineStopsWithoutRetryOrManualApproval(t *testing.T) {
	var requests atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	harness.runtime.approvalReviewTimeout = time.Second
	call := tool.Call{ID: "caller-deadline", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"cancelled.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resolution, err := harness.host.awaitApproval(ctx, "session", "agent-1", "main", harness.run, call, pending)
	if !errors.Is(err, context.DeadlineExceeded) || resolution.NeedsUserApproval || requests.Load() != 1 {
		t.Fatalf("caller deadline resolution=%+v requests=%d error=%v", resolution, requests.Load(), err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	noEventCtx, noEventCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer noEventCancel()
	if event, nextErr := harness.host.NextEvent(noEventCtx); nextErr == nil {
		t.Fatalf("caller deadline emitted follow-up approval event=%+v", event)
	}
	events, listErr := harness.coding.Runner().ListEvents(context.Background(), harness.run.RunID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, event := range events {
		if event.Type == api.EventApprovalDecided {
			t.Fatalf("caller deadline recorded approval decision=%+v", event)
		}
	}
}

func TestAutoReviewModeRequiresChatGPTAndDoesNotTakePendingHumanApproval(t *testing.T) {
	unauthed := NewService(context.Background(), config.Default())
	if err := unauthed.setApprovalMode(context.Background(), ApprovalModeAutoReview); err == nil {
		t.Fatal("automatic mode accepted without authentication")
	}
	if unauthed.approvalMode != ApprovalModePrompt {
		t.Fatalf("unauthorized mode=%q", unauthed.approvalMode)
	}

	var reviews atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		reviews.Add(1)
		writeAutomaticReview(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe"}`, true)
	})
	if err := harness.host.setApprovalMode(context.Background(), ApprovalModePrompt); err != nil {
		t.Fatal(err)
	}
	definition := harness.driver.Definition()
	result := make(chan approvalResolution, 1)
	errs := make(chan error, 1)
	go func() {
		resolution, err := harness.host.awaitTeamApproval(
			context.Background(), "session", "team-run", "team goal",
			tool.Call{ID: "human-pending", Name: definition.Name, Arguments: json.RawMessage(`{"path":"manual.txt"}`)}, definition,
		)
		if err != nil {
			errs <- err
			return
		}
		result <- resolution
	}()
	pending := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if pending.State != "pending" {
		t.Fatalf("pending event=%+v", pending)
	}
	if err := harness.host.setApprovalMode(context.Background(), ApprovalModeAutoReview); err != nil {
		t.Fatal(err)
	}
	select {
	case resolution := <-result:
		t.Fatalf("automatic mode took over human approval: %+v", resolution)
	case err := <-errs:
		t.Fatalf("pending approval failed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := harness.host.resolveLiveApproval(context.Background(), pending.ApprovalID, "once", "user"); err != nil {
		t.Fatal(err)
	}
	select {
	case resolution := <-result:
		if resolution.Mode != agentservice.ApprovalOnce {
			t.Fatalf("human resolution=%+v", resolution)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("human approval was not delivered")
	}
	if err := harness.authentication.Logout(context.Background(), "chatgpt", "acct"); err != nil {
		t.Fatal(err)
	}
	loggedOut := nextApprovalEvent(t, harness.host, EventAuthState)
	if loggedOut.State != "logged_out" || loggedOut.Data["provider"] != "chatgpt" {
		t.Fatalf("logout event=%+v", loggedOut)
	}
	modeEvent := nextApprovalEvent(t, harness.host, EventApprovalMode)
	if modeEvent.State != "prompt" || modeEvent.Data["auto_review_available"] != "false" ||
		harness.host.approvalMode != ApprovalModePrompt {
		t.Fatalf("logout mode projection=%+v service_mode=%q", modeEvent, harness.host.approvalMode)
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionSetApprovalMode, Target: string(ApprovalModeAutoReview),
	}); err == nil {
		t.Fatal("direct automatic mode action succeeded after logout")
	}
	harness.host.mu.Lock()
	harness.host.approvalMode = ApprovalModeAutoReview
	harness.host.mu.Unlock()
	call := tool.Call{ID: "auth-race", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	reviewPending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(
		context.Background(), "session", "agent-1", "main", harness.run, call, reviewPending,
	)
	if err != nil || resolution.Mode != agentservice.ApprovalDenied ||
		!strings.Contains(resolution.DenialMessage, "(authentication)") {
		t.Fatalf("post-logout review=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	failed := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if failed.State != "auto_failed" || failed.Data["error_kind"] != "authentication" || reviews.Load() != 0 {
		t.Fatalf("post-logout review event=%+v reviewer_calls=%d", failed, reviews.Load())
	}
}

func TestAutoReviewDenialTrackerThresholdsIsolationAndCleanup(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	for attempt := 1; attempt <= 3; attempt++ {
		err := service.recordAutoReview("run-consecutive", true)
		if attempt < 3 && err != nil {
			t.Fatalf("early consecutive limit at %d: %v", attempt, err)
		}
		if attempt == 3 {
			var limit *AutoReviewDenialLimitError
			if !errors.As(err, &limit) || limit.ConsecutiveDenials != 3 {
				t.Fatalf("consecutive limit=%v", err)
			}
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := service.recordAutoReview("isolated-run", true); err != nil {
			t.Fatalf("denials leaked between runs: %v", err)
		}
	}
	for denial := 1; denial <= 10; denial++ {
		err := service.recordAutoReview("run-window", true)
		if denial < 10 && err != nil {
			t.Fatalf("early window limit at %d: %v", denial, err)
		}
		if denial == 10 {
			var limit *AutoReviewDenialLimitError
			if !errors.As(err, &limit) || limit.RecentDenials != 10 || limit.ConsecutiveDenials != 1 {
				t.Fatalf("window limit=%v", err)
			}
			break
		}
		if err := service.recordAutoReview("run-window", false); err != nil {
			t.Fatal(err)
		}
	}
	service.clearRun("run-consecutive")
	service.mu.Lock()
	_, retained := service.autoReviewDenials["run-consecutive"]
	_, isolated := service.autoReviewDenials["isolated-run"]
	service.mu.Unlock()
	if retained || !isolated {
		t.Fatalf("tracker cleanup retained=%v isolated=%v", retained, isolated)
	}
}

func TestAutoReviewRepeatedDenialsStillRequireUserDecision(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"not authorized"}`, true)
	})
	for attempt := 1; attempt <= 3; attempt++ {
		call := tool.Call{ID: fmt.Sprintf("deny-%d", attempt), Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
		pending := prepareAutomaticApproval(t, harness, call)
		type approvalResult struct {
			resolution approvalResolution
			err        error
		}
		result := make(chan approvalResult, 1)
		go func() {
			resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
			result <- approvalResult{resolution: resolution, err: err}
		}()
		reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
		denied := nextApprovalEvent(t, harness.host, EventApprovalResolved)
		prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
		if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
			t.Fatalf("manual fallback %d=%+v", attempt, prompt)
		}
		if attempt == 3 && !strings.Contains(denied.Text, "Repeated automatic denials") {
			t.Fatalf("repeated-denial warning missing: %+v", denied)
		}
		if err := harness.host.ExecuteAction(context.Background(), Action{
			Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "deny",
		}); err != nil {
			t.Fatal(err)
		}
		outcome := <-result
		if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalDenied {
			t.Fatalf("user denial %d=%+v error=%v", attempt, outcome.resolution, outcome.err)
		}
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("denial threshold executed tool")
	}
}

func newAutoReviewHarness(t *testing.T, handler http.HandlerFunc) autoReviewHarness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	chatClient := chatgpt.NewClient()
	chatClient.RevokeURL = ""
	authentication := auth.NewService(store.DB(), credentials, chatClient, grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coding.Close(context.Background()) })
	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	runtime, err := NewProviderRuntime(cfg, authentication, catalog.NewService(store.DB(), authentication), coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ChatGPTEndpoint = server.URL
	host := NewService(ctx, cfg)
	host.AttachDurable(nil, coding)
	host.AttachAuth(authentication, nil)
	host.AttachProviderRuntime(runtime)
	if err := host.setApprovalMode(ctx, ApprovalModeAutoReview); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "original user goal")
	if err != nil {
		t.Fatal(err)
	}
	counter := &atomic.Int32{}
	return autoReviewHarness{
		host: host, runtime: runtime, coding: coding, authentication: authentication, run: run,
		driver: countedApprovalDriver{executions: counter},
	}
}

func prepareAutomaticApproval(t *testing.T, harness autoReviewHarness, call tool.Call) agentservice.PendingApproval {
	t.Helper()
	execution, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || execution.Executed || execution.Approval == nil {
		t.Fatalf("prepare approval=%+v error=%v", execution, err)
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("tool executed before automatic review")
	}
	return *execution.Approval
}

func writeAutomaticReview(writer http.ResponseWriter, output string, completed bool) {
	writer.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", delta)
	if completed {
		_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}
}

func nextApprovalEvent(t *testing.T, service *Service, kind EventKind) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == kind {
			return event
		}
	}
}

func durableApprovalDecider(t *testing.T, coding *agentservice.Service, runID string) string {
	t.Helper()
	events, err := coding.Runner().ListEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == api.EventApprovalDecided {
			return fmt.Sprint(events[index].Payload["decidedBy"])
		}
	}
	t.Fatalf("run %s has no durable approval decision", runID)
	return ""
}

func TestTurnContextBuildReplaysCompatibleHistoryAndAppendsDynamicTail(t *testing.T) {
	boundary := int64(1)
	saved := []message.Message{
		message.NewText(message.RoleSystem, mainInstructions),
		{
			Role: message.RoleSystem, Text: "saved skill catalog",
			Metadata: map[string]string{"hydaelyn.skill.context": "catalog"},
		},
		message.NewText(message.RoleUser, "old request"),
		{
			Role: message.RoleAssistant, Text: "old answer",
			ProviderState: json.RawMessage(`[{"type":"reasoning","id":"reasoning-1"}]`),
		},
	}
	manager := turnContext{
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-test",
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-test",
			InstructionFingerprint: mainInstructionFingerprint, StaticPrefixHash: mainInstructionFingerprint,
			WireVersion: session.CurrentWireVersion, CoveredThroughSequence: &boundary, Messages: saved,
		},
		history: []session.Block{
			{Sequence: 0, Kind: "user", Content: "old request"},
			{Sequence: 1, Kind: "assistant", Content: "old answer"},
			{Sequence: 2, Kind: "user", Content: "current hook user", State: "hook"},
		},
		checkpointBoundary: &boundary,
		privateContext:     "current trusted hook",
		historicalContext:  `{"memories":["current evidence"]}`,
		todo:               session.TodoList{Goal: "current todo", Revision: 3},
	}
	got, err := manager.Build(context.Background(), api.Task{Goal: "new request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(saved)+6 || !reflect.DeepEqual(got[:len(saved)], saved) {
		t.Fatalf("saved prefix changed:\n got=%#v\nwant=%#v", got, saved)
	}
	tail := got[len(saved):]
	if tail[0].Role != message.RoleSystem || tail[0].Visibility != message.VisibilityPrivate ||
		!strings.Contains(tail[0].Text, "current trusted hook") {
		t.Fatalf("private hook tail = %#v", tail[0])
	}
	if tail[1].Role != message.RoleSystem || tail[1].Visibility != message.VisibilityPrivate ||
		!strings.HasPrefix(tail[1].Text, todoReminderPrefix) {
		t.Fatalf("todo tail = %#v", tail[1])
	}
	if tail[2].Text != historicalEvidencePolicy || tail[2].Visibility != message.VisibilityPrivate {
		t.Fatalf("historical policy tail = %#v", tail[2])
	}
	if tail[3].Role != message.RoleUser || tail[3].Text != "current hook user" {
		t.Fatalf("hook user tail = %#v", tail[3])
	}
	if tail[4].Role != message.RoleUser || tail[4].Visibility != message.VisibilityPrivate ||
		!strings.Contains(tail[4].Text, "current evidence") {
		t.Fatalf("historical data tail = %#v", tail[4])
	}
	if tail[5].Role != message.RoleUser || tail[5].Text != "new request" {
		t.Fatalf("goal tail = %#v", tail[5])
	}
}

func TestTurnContextResumeDoesNotDuplicateCheckpointedUser(t *testing.T) {
	boundary := int64(1)
	staticIdentity := "static-resume"
	factsValue := executionCheckpointFacts{Version: 1, RunID: "run-resume"}
	encoded, err := json.Marshal(factsValue)
	if err != nil {
		t.Fatal(err)
	}
	facts := message.NewText(message.RoleAssistant, executionCheckpointFactsPrefix+string(encoded))
	facts.Kind = message.KindCustom
	facts.Visibility = message.VisibilityPrivate
	facts.Metadata = map[string]string{executionCheckpointMetadataKey: "1"}
	saved := []message.Message{
		message.NewText(message.RoleSystem, mainInstructions),
		message.NewText(message.RoleUser, "original request"),
		message.NewText(message.RoleAssistant, "semantic checkpoint"),
		executionCheckpointPolicyMessage(), facts,
	}
	saved[2].Kind = message.KindCompactionSummary
	manager := turnContext{
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-test", runID: "run-resume", resuming: true,
		staticIdentity: staticIdentity, checkpointBoundary: &boundary,
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-test", InstructionFingerprint: mainInstructionFingerprint,
			StaticPrefixHash: staticIdentity, WireVersion: session.CurrentWireVersion,
			CoveredThroughSequence: &boundary, Messages: saved,
		},
		history: []session.Block{
			{Sequence: 1, Kind: "user", RunID: "run-resume", Content: "original request"},
			{Sequence: 2, Kind: "user", RunID: "run-resume", Content: "late guidance", State: "guidance"},
		},
	}
	got, err := manager.Build(context.Background(), api.Task{Goal: "original request"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, current := range got {
		if current.Role == message.RoleUser {
			counts[current.Text]++
		}
	}
	if counts["original request"] != 1 || counts["late guidance"] != 1 {
		t.Fatalf("resumed user messages=%v history=%#v", counts, got)
	}
}

func TestTurnContextBuildFallsBackWhenModelHistoryScopeDiffers(t *testing.T) {
	manager := turnContext{
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-new",
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-old",
			InstructionFingerprint: mainInstructionFingerprint,
			Messages: []message.Message{{
				Role: message.RoleAssistant, Text: "stale answer",
				ProviderState: json.RawMessage(`[{"type":"reasoning","id":"stale"}]`),
			}},
		},
		history: []session.Block{
			{Kind: "user", Content: "visible request"},
			{Kind: "assistant", Content: "visible answer"},
		},
	}
	got, err := manager.Build(context.Background(), api.Task{Goal: "switched request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Role != message.RoleSystem || got[0].Text != mainInstructions ||
		got[1].Role != message.RoleUser || got[1].Text != "visible request" ||
		got[2].Role != message.RoleAssistant || got[2].Text != "visible answer" ||
		got[3].Role != message.RoleUser || got[3].Text != "switched request" {
		t.Fatalf("fallback messages = %#v", got)
	}
	for _, current := range got {
		if len(current.ProviderState) != 0 || current.Text == "stale answer" {
			t.Fatalf("stale exact state leaked into fallback: %#v", current)
		}
	}
}

func TestTeamPrepareEnginePartitionsPromptCacheKeysAndPreservesOptions(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	hooks := service.teamHooks(TurnRequest{SessionID: "session-1", Provider: "chatgpt", Model: "gpt-team"}, "team-parent", teamExecutionPolicy{})
	base := hyagent.Engine{ExtraBody: map[string]any{"parallel_tool_calls": false}}
	prepare := func(runID, role string) hyagent.Engine {
		t.Helper()
		prepared, err := hooks.PrepareEngine(context.Background(), base, multiagent.Dispatch{
			Task: api.Task{RunID: runID}, To: role,
		}, multiagent.AgentClass{Name: role})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.ExtraBody["parallel_tool_calls"] != false {
			t.Fatalf("existing provider option lost: %#v", prepared.ExtraBody)
		}
		return prepared
	}
	first := prepare("child-run-1", agentservice.ImplementerClass)
	repeated := prepare("child-run-2", agentservice.ImplementerClass)
	secondRole := prepare("child-run-2", agentservice.ReviewerClass)
	if first.ExtraBody["prompt_cache_key"] != "session-1:team:chatgpt:gpt-team:implementer" ||
		repeated.ExtraBody["prompt_cache_key"] != first.ExtraBody["prompt_cache_key"] ||
		secondRole.ExtraBody["prompt_cache_key"] == first.ExtraBody["prompt_cache_key"] {
		t.Fatalf("team cache keys first=%#v repeated=%#v secondRole=%#v", first.ExtraBody, repeated.ExtraBody, secondRole.ExtraBody)
	}
	if _, mutated := base.ExtraBody["prompt_cache_key"]; mutated {
		t.Fatalf("base engine ExtraBody mutated: %#v", base.ExtraBody)
	}
}

func TestMainTurnsKeepSerializedPrefixStableAndAppendRawOutputAndNewTail(t *testing.T) {
	const firstOutput = `[{"type":"reasoning","id":"rs_first","encrypted_content":"opaque"},{"type":"message","id":"msg_first","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}]`
	const secondOutput = `[{"type":"message","id":"msg_second","role":"assistant","content":[{"type":"output_text","text":"second answer"}]}]`
	type capturedRequest struct {
		PromptCacheKey string            `json:"prompt_cache_key"`
		Instructions   string            `json:"instructions"`
		Input          []json.RawMessage `json:"input"`
	}
	var captured []capturedRequest
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		var request capturedRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Errorf("decode captured request %d: %v", call, err)
		}
		captured = append(captured, request)
		switch call {
		case 1:
			_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"first answer"}`+"\n\n")
			_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+firstOutput+`}}`+"\n\n")
		case 2:
			_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"second answer"}`+"\n\n")
			_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+secondOutput+`}}`+"\n\n")
		default:
			t.Errorf("unexpected provider request %d", call)
		}
	})
	ctx := context.Background()
	sessions := harness.service.sessions
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: "cache-session", Title: "Cache", ProviderID: "chatgpt", ModelID: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.UpdateTodo(ctx, "cache-session", 0, func(todo *session.TodoList) error {
		todo.Goal = "first todo"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "cache-session", Prompt: "first request", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	if _, err := sessions.UpdateTodo(ctx, "cache-session", 1, func(todo *session.TodoList) error {
		todo.Goal = "second todo"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secondRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "cache-session", Prompt: "second request", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, secondRun)

	if len(captured) != 2 || captured[0].PromptCacheKey != "cache-session" ||
		captured[1].PromptCacheKey != "cache-session" || captured[0].Instructions != captured[1].Instructions {
		t.Fatalf("captured cache requests = %#v", captured)
	}
	var rawOutput []json.RawMessage
	if err := json.Unmarshal([]byte(firstOutput), &rawOutput); err != nil {
		t.Fatal(err)
	}
	wantPrefix := append(append([]json.RawMessage(nil), captured[0].Input...), rawOutput...)
	if len(captured[1].Input) <= len(wantPrefix) || !reflect.DeepEqual(captured[1].Input[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("second input did not preserve first request plus raw output:\nfirst=%s\nsecond=%s", captured[0].Input, captured[1].Input)
	}
	tailJSON, err := json.Marshal(captured[1].Input[len(wantPrefix):])
	if err != nil {
		t.Fatal(err)
	}
	tail := string(tailJSON)
	if !strings.Contains(tail, "second todo") || !strings.Contains(tail, "second request") ||
		strings.Contains(tail, "first todo") || strings.Contains(tail, "first request") {
		t.Fatalf("second dynamic tail = %s", tail)
	}
	projection, err := sessions.LoadProjection(ctx, "cache-session")
	if err != nil {
		t.Fatal(err)
	}
	if projection.ModelHistory.ProviderID != "chatgpt" || projection.ModelHistory.ModelID != "gpt-skill" ||
		projection.ModelHistory.InstructionFingerprint != mainInstructionFingerprint ||
		len(projection.ModelHistory.Messages) == 0 ||
		string(projection.ModelHistory.Messages[len(projection.ModelHistory.Messages)-1].ProviderState) != secondOutput {
		t.Fatalf("persisted exact history = %#v", projection.ModelHistory)
	}
}

func TestMainTurnReplacesMismatchedSnapshotOnlyAfterSuccessfulFallback(t *testing.T) {
	const freshOutput = `[{"type":"message","id":"fresh_message","role":"assistant","content":[{"type":"output_text","text":"fresh answer"}]}]`
	var capturedBody string
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		if call != 1 {
			t.Errorf("unexpected provider request %d", call)
		}
		capturedBody = body
		_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"fresh answer"}`+"\n\n")
		_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+freshOutput+`}}`+"\n\n")
	})
	ctx := context.Background()
	sessions := harness.service.sessions
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: "mismatch-session", Title: "Mismatch", ProviderID: "chatgpt", ModelID: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "mismatch-session", session.Block{Kind: "user", RunID: "old-run", Content: "visible request"}); err != nil {
		t.Fatal(err)
	}
	stale := session.ModelHistory{
		ProviderID: "chatgpt", ModelID: "different-model", InstructionFingerprint: mainInstructionFingerprint,
		Messages: []message.Message{{
			Role: message.RoleAssistant, Text: "stale hidden answer",
			ProviderState: json.RawMessage(`[{"type":"reasoning","id":"stale_reasoning"}]`),
		}},
	}
	if err := sessions.CompleteTurn(ctx, "mismatch-session", session.Block{
		Kind: "assistant", RunID: "old-run", Content: "visible answer",
	}, stale); err != nil {
		t.Fatal(err)
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "mismatch-session", Prompt: "follow up", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if !strings.Contains(capturedBody, "visible request") || !strings.Contains(capturedBody, "visible answer") ||
		strings.Contains(capturedBody, "stale hidden answer") || strings.Contains(capturedBody, "stale_reasoning") {
		t.Fatalf("mismatch fallback request = %s", capturedBody)
	}
	projection, err := sessions.LoadProjection(ctx, "mismatch-session")
	if err != nil {
		t.Fatal(err)
	}
	got := projection.ModelHistory
	if got.ModelID != "gpt-skill" || got.InstructionFingerprint != mainInstructionFingerprint ||
		len(got.Messages) == 0 || string(got.Messages[len(got.Messages)-1].ProviderState) != freshOutput {
		t.Fatalf("replacement snapshot = %#v", got)
	}
}
