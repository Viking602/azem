package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/venat/message"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

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
		compactTargetTokens: 512, coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			return semanticStateForTest("summary"), nil
		},
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
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 512,
		coordinator: &compactionCoordinator{}, summarize: func(ctx context.Context, _ string) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
				return semanticStateForTest("summary"), nil
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
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 512,
		coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			<-release
			return semanticStateForTest("prepared summary"), nil
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
	if estimateContextTokens(got) > 512 || calls.Load() != backgroundCalls {
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
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 512,
		coordinator: &compactionCoordinator{},
		summarize:   func(context.Context, string) (string, error) { return semanticStateForTest("prepared state"), nil },
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
		softTriggerTokens: tokensA - 1, backgroundPrepare: true, compactTargetTokens: 800,
		coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			return semanticStateForTest("source-specific summary"), nil
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
		softTriggerTokens: tokens - 1, backgroundPrepare: false, compactTargetTokens: 512,
		coordinator: &compactionCoordinator{},
		summarize: func(context.Context, string) (string, error) {
			calls.Add(1)
			return semanticStateForTest("synchronous summary"), nil
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
	got, err := manager.CompactTo(context.Background(), history, tokens-1)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || pre.Load() != 1 || post.Load() != 1 || activations.Load() != 1 {
		t.Fatalf("calls=%d pre=%d post=%d activations=%d", calls.Load(), pre.Load(), post.Load(), activations.Load())
	}
	if again, againErr := manager.CompactTo(context.Background(), got, 512); againErr != nil || !reflect.DeepEqual(again, got) {
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
		softTriggerTokens: tokens - 1, backgroundPrepare: true, compactTargetTokens: 512,
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
	snapshot := activeGuidanceSnapshot{values: []activeGuidanceMessage{{Text: "first correction"}, {Text: "second correction"}}}
	acknowledged := false
	manager := activeGuidanceContext{
		inner: turnContext{summarize: func(context.Context, string) (string, error) { return semanticStateForTest("retain guidance"), nil }},
		peek:  func() activeGuidanceSnapshot { return snapshot },
		acknowledge: func(got activeGuidanceSnapshot) {
			acknowledged = reflect.DeepEqual(got, snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old ", 400)),
		message.NewText(message.RoleAssistant, "done"),
		message.NewText(message.RoleUser, "follow up one"),
		message.NewText(message.RoleAssistant, "done one"),
		message.NewText(message.RoleUser, "follow up two"),
		message.NewText(message.RoleAssistant, "done two"),
		message.NewText(message.RoleUser, "latest"),
	}
	got, err := manager.CompactTo(context.Background(), history, 300)
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
	if len(remaining) != 1 || remaining[0].Text != "do not lose this" {
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
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return semanticStateForTest("summary"), nil }}
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
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return semanticStateForTest("summary"), nil }}
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

	const target = 300
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return semanticStateForTest("summary"), nil }}
	compacted, err := manager.CompactTo(context.Background(), history, target)
	if err != nil || reflect.DeepEqual(compacted, history) {
		t.Fatalf("mandatory oversized result history=%#v error=%v", compacted, err)
	}
	assertRollingToolCheckpoint(t, compacted, history[len(history)-3])
}
