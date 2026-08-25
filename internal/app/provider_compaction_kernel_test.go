package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/contextarchive"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

// compactionTestDriver is retained as the package-wide deterministic provider
// fixture. The name is historical; archive compaction itself never calls it.
type compactionTestDriver struct {
	requests []hyprovider.Request
	streams  [][]hyprovider.Event
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

func TestContextBudgetUsesSnapcompactReserveAndRecentFloor(t *testing.T) {
	got, err := calculateContextBudget("model", 100_000, 300, config.ContextConfig{
		ReserveTokens:    1_000,
		KeepRecentTokens: 20_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextWindow != 100_000 || got.Trigger != 84_700 || got.KeepRecent != 20_000 {
		t.Fatalf("budget=%+v", got)
	}
}

func TestContextBudgetTriggersBeforeReproducedGrok98PercentOccupancy(t *testing.T) {
	const (
		contextWindow        = 500_000
		toolDefinitionTokens = 3_685
		localEstimate        = 476_653
		providerInput        = 489_188
	)
	budget, err := calculateContextBudget("grok-4.6", contextWindow, toolDefinitionTokens, config.ContextConfig{
		ReserveTokens:    16_384,
		KeepRecentTokens: 20_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if budget.Trigger != 421_315 {
		t.Fatalf("trigger=%d, want 421315", budget.Trigger)
	}
	pressure := &providerContextPressure{toolTokens: toolDefinitionTokens}
	pressure.observeInputTokens(providerInput)
	if got := pressure.tokens(localEstimate); got != providerInput-toolDefinitionTokens {
		t.Fatalf("pressure=%d, want %d", got, providerInput-toolDefinitionTokens)
	}
	if localEstimate <= budget.Trigger {
		t.Fatalf("reproduced local estimate %d did not cross safe trigger %d", localEstimate, budget.Trigger)
	}
}

func TestContextBudgetRejectsRecentFloorAtTrigger(t *testing.T) {
	_, err := calculateContextBudget("model", 10_000, 1_000, config.ContextConfig{
		ReserveTokens:    1_000,
		KeepRecentTokens: 8_000,
	})
	if err == nil {
		t.Fatal("expected keep_recent_tokens validation error")
	}
}

func TestManualCompactionArchivesDurableToolMessages(t *testing.T) {
	ctx := t.Context()
	blocks := make([]session.Block, 0, 8)
	saved := []message.Message{message.NewText(message.RoleSystem, mainInstructions)}
	for turn := range 4 {
		user := session.Block{Sequence: int64(turn*2 + 1), Kind: "user", RunID: "run-1", Content: "user-" + string(rune('0'+turn))}
		assistant := session.Block{Sequence: int64(turn*2 + 2), Kind: "assistant", RunID: "run-1", Content: "answer-" + string(rune('0'+turn))}
		blocks = append(blocks, user, assistant)
		userMessage, _ := blockMessage(user)
		saved = append(saved, userMessage)
		if turn == 0 {
			saved = append(saved,
				message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{
					ID: "read-1", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"old.txt"}`),
				}}},
				message.NewToolResult(message.ToolResult{
					ToolCallID: "read-1", Name: "coding.read_file", Content: "exact durable result",
				}),
			)
		}
		assistantMessage, _ := blockMessage(assistant)
		saved = append(saved, assistantMessage)
	}
	boundary := int64(8)
	projection := session.Projection{
		Session: session.Session{ID: "manual-tools", ProviderID: "provider", ModelID: "model"},
		Blocks:  blocks,
		ToolRecords: []session.ToolRecord{{
			RunID: "run-1", ToolCallID: "read-1", AnchorSequence: 1, Name: "coding.read_file",
			Arguments: json.RawMessage(`{"path":"old.txt"}`), State: session.ToolCompleted, Content: "exact durable result",
		}},
		ModelHistory: session.ModelHistory{
			ProviderID: "provider", ModelID: "model", InstructionFingerprint: mainInstructionFingerprint,
			StaticPrefixHash: mainInstructionFingerprint, WireVersion: session.CurrentWireVersion,
			CoveredThroughSequence: &boundary, Messages: saved,
		},
	}
	manager := turnContext{
		sessionID: "manual-tools", runID: "manual-compaction", providerID: "provider", modelID: "model",
		instructions: mainInstructions, instructionFingerprint: mainInstructionFingerprint,
		history: blocks, modelHistory: projection.ModelHistory, checkpointBoundary: &boundary,
		staticIdentity: mainInstructionFingerprint, toolRecords: projection.ToolRecords,
	}
	var archivedSource []byte
	manager.storeArchive = func(_ context.Context, result contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error) {
		archivedSource = append([]byte(nil), result.Source...)
		manifest := result.Manifest
		manifest.SourceArtifactID = "manual-tool-source"
		return manifest, nil, nil
	}
	manager.loadArchiveSource = func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), archivedSource...), nil
	}
	messages, err := manualCompactionMessages(ctx, manager, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.prepareArchiveCompaction(ctx, messages, 100_000, "manual"); err != nil {
		t.Fatal(err)
	}
	source, err := contextarchive.DecodeSource(archivedSource)
	if err != nil {
		t.Fatal(err)
	}
	var callFound, resultFound bool
	for _, current := range source.Messages {
		for _, call := range current.ToolCalls {
			callFound = callFound || call.ID == "read-1" && call.Name == "coding.read_file"
		}
		resultFound = resultFound || current.ToolResult != nil &&
			current.ToolResult.ToolCallID == "read-1" &&
			current.ToolResult.Content == "exact durable result"
	}
	if !callFound || !resultFound {
		t.Fatalf("manual archive lost tool group: calls=%v results=%v source=%#v", callFound, resultFound, source.Messages)
	}

	incompatible := projection
	incompatible.ModelHistory.ModelID = "other-model"
	manager.modelHistory = incompatible.ModelHistory
	if _, err := manualCompactionMessages(ctx, manager, incompatible); err == nil ||
		!strings.Contains(err.Error(), "current model history is unavailable") {
		t.Fatalf("incompatible tool history error = %v", err)
	}
}

func TestManualCompactionRestoresDurableToolPairMissingFromLegacyHistory(t *testing.T) {
	ctx := t.Context()
	user := session.Block{Sequence: 1, Kind: "user", RunID: "legacy-run", Content: "inspect old state"}
	assistant := session.Block{Sequence: 2, Kind: "assistant", RunID: "legacy-run", Content: "old answer"}
	userMessage, _ := blockMessage(user)
	assistantMessage, _ := blockMessage(assistant)
	boundary := int64(2)
	record := session.ToolRecord{
		RunID: "legacy-run", ToolCallID: "legacy-read", AnchorSequence: 1, Name: "coding.read_file",
		Arguments: json.RawMessage(`{"path":"old.txt"}`), State: session.ToolCompleted,
		Content: "durable result", Structured: json.RawMessage(`{"path":"old.txt"}`),
	}
	artifactRecord := session.ToolRecord{
		RunID: "legacy-run", ToolCallID: "legacy-shell", AnchorSequence: 1, Name: "coding.shell",
		Arguments: json.RawMessage(`{"command":"printf old"}`), State: session.ToolCompleted,
		Content: "preview", ArtifactID: "legacy-tool-result",
	}
	projection := session.Projection{
		Session:     session.Session{ID: "legacy-tools", ProviderID: "provider", ModelID: "model"},
		Blocks:      []session.Block{user, assistant},
		ToolRecords: []session.ToolRecord{record, artifactRecord},
		ModelHistory: session.ModelHistory{
			ProviderID: "provider", ModelID: "model", InstructionFingerprint: mainInstructionFingerprint,
			StaticPrefixHash: mainInstructionFingerprint, WireVersion: session.CurrentWireVersion,
			CoveredThroughSequence: &boundary,
			Messages:               []message.Message{message.NewText(message.RoleSystem, mainInstructions), userMessage, assistantMessage},
		},
	}
	manager := turnContext{
		sessionID: "legacy-tools", runID: "manual-compaction", providerID: "provider", modelID: "model",
		instructions: mainInstructions, instructionFingerprint: mainInstructionFingerprint,
		history: projection.Blocks, modelHistory: projection.ModelHistory, checkpointBoundary: &boundary,
		staticIdentity: mainInstructionFingerprint, toolRecords: projection.ToolRecords,
	}
	manager.loadArchiveSource = func(_ context.Context, artifactID string) ([]byte, error) {
		if artifactID != artifactRecord.ArtifactID {
			t.Fatalf("unexpected artifact %q", artifactID)
		}
		return []byte(`{"content":"full durable output","structured":{"exitCode":0}}`), nil
	}

	messages, err := manualCompactionMessages(ctx, manager, projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.ValidateCompleteTurns(messages); err != nil {
		t.Fatalf("restored history is invalid: %v", err)
	}
	calls, results := manualCompactionToolNames(messages)
	for _, current := range []session.ToolRecord{record, artifactRecord} {
		if calls[current.ToolCallID] != current.Name || results[current.ToolCallID] != current.Name {
			t.Fatalf("restored pair %q calls=%v results=%v", current.ToolCallID, calls, results)
		}
	}
	var restoredOrder []string
	for _, current := range messages {
		for _, call := range current.ToolCalls {
			if call.ID == record.ToolCallID || call.ID == artifactRecord.ToolCallID {
				restoredOrder = append(restoredOrder, "call:"+call.ID)
			}
		}
		if current.ToolResult != nil && (current.ToolResult.ToolCallID == record.ToolCallID || current.ToolResult.ToolCallID == artifactRecord.ToolCallID) {
			restoredOrder = append(restoredOrder, "result:"+current.ToolResult.ToolCallID)
		}
	}
	wantOrder := []string{
		"call:" + record.ToolCallID, "result:" + record.ToolCallID,
		"call:" + artifactRecord.ToolCallID, "result:" + artifactRecord.ToolCallID,
	}
	if !reflect.DeepEqual(restoredOrder, wantOrder) {
		t.Fatalf("same-anchor restored order=%v want=%v", restoredOrder, wantOrder)
	}
	var restoredArtifact *message.ToolResult
	for _, current := range messages {
		if current.ToolResult != nil && current.ToolResult.ToolCallID == artifactRecord.ToolCallID {
			restoredArtifact = current.ToolResult
			break
		}
	}
	if restoredArtifact == nil || restoredArtifact.Content != "full durable output" ||
		string(restoredArtifact.Structured) != `{"exitCode":0}` {
		t.Fatalf("restored artifact result = %#v", restoredArtifact)
	}
	repeated, err := manualCompactionMessages(ctx, manager, projection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(messages, repeated) {
		t.Fatal("legacy tool restoration is not deterministic")
	}
}
