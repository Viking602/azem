package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/contextarchive"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func mustTestValue[T any](t *testing.T, value T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustTestNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func repeatedArchiveHistory() []message.Message {
	history := []message.Message{message.NewText(message.RoleSystem, "system rules")}
	for turn := range 5 {
		answer := fmt.Sprintf("answer-%d", turn)
		if turn < 2 {
			answer += strings.Repeat(" old-context", 3_000)
		}
		if turn == 2 || turn == 3 {
			answer += strings.Repeat(" aging-context", 1_000)
		}
		history = append(history,
			message.NewText(message.RoleUser, fmt.Sprintf("user-%d", turn)),
			message.NewText(message.RoleAssistant, answer),
		)
	}
	return history
}

func assertInitialArchiveSource(t *testing.T, ctx context.Context, sessions *session.Service, manifest contextarchive.Manifest) {
	t.Helper()
	artifact, err := sessions.LoadArtifact(ctx, "archive-session", manifest.SourceArtifactID)
	mustTestNoError(t, err)
	source, err := contextarchive.DecodeSource(artifact.Payload)
	mustTestNoError(t, err)
	if got := archiveUserMarkers(source.Messages); !strings.Contains(got, "user-0") || !strings.Contains(got, "user-1") || strings.Contains(got, "user-2") {
		t.Fatalf("first archive source users=%q", got)
	}
}

func assertRepeatedArchiveSource(t *testing.T, ctx context.Context, sessions *session.Service, manifest contextarchive.Manifest) {
	t.Helper()
	artifact, err := sessions.LoadArtifact(ctx, "archive-session", manifest.SourceArtifactID)
	mustTestNoError(t, err)
	source, err := contextarchive.DecodeSource(artifact.Payload)
	mustTestNoError(t, err)
	markers := archiveUserMarkers(source.Messages)
	for turn := range 3 {
		if !strings.Contains(markers, fmt.Sprintf("user-%d", turn)) {
			t.Fatalf("repeated archive lost user-%d: %q", turn, markers)
		}
	}
	for _, current := range source.Messages {
		if _, nested := archiveManifestFromMessage(current); nested {
			t.Fatal("repeated archive stored a carrier instead of expanding its durable source")
		}
	}
}

func TestArchiveCompactionKeepsLatestThreeCompleteTurnsAndExpandsOnRepeat(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	mustTestNoError(t, err)
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	_, err = sessions.Ensure(ctx, session.Session{ID: "archive-session", Title: "Archive"})
	mustTestNoError(t, err)
	attachments := NewAttachmentStore(filepath.Join(t.TempDir(), "attachments"))
	manager := archiveBackedTurnContext(sessions, attachments, "archive-session", "run-1", false)
	manager.coordinator = &compactionCoordinator{}

	history := repeatedArchiveHistory()
	first, err := manager.CompactTo(ctx, history, 9_000)
	mustTestNoError(t, err)
	assertCompleteRecentTurns(t, first, 2, 3, 4)
	firstManifest := archiveManifestInMessages(t, first)
	if firstManifest.Carrier != "artifact" || len(AttachmentsFromMessage(first[archiveCarrierIndex(first)])) != 0 {
		t.Fatalf("text-only archive leaked image frames: manifest=%+v", firstManifest)
	}
	assertInitialArchiveSource(t, ctx, sessions, firstManifest)

	secondInput := append(append([]message.Message(nil), first...),
		message.NewText(message.RoleUser, "user-5"), message.NewText(message.RoleAssistant, "answer-5"+strings.Repeat(" transition", 1_000)),
	)
	manager.runID = "run-2"
	second, err := manager.CompactTo(ctx, secondInput, 7_500)
	mustTestNoError(t, err)
	assertCompleteRecentTurns(t, second, 3, 4, 5)
	secondManifest := archiveManifestInMessages(t, second)
	assertRepeatedArchiveSource(t, ctx, sessions, secondManifest)
}

func TestHardArchiveCompactionPreservesTailWithinHardLimit(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "target-headroom", Title: "Target headroom"}); err != nil {
		t.Fatal(err)
	}
	manager := archiveBackedTurnContext(
		sessions,
		NewAttachmentStore(filepath.Join(t.TempDir(), "attachments")),
		"target-headroom",
		"run-1",
		false,
	)
	history := []message.Message{message.NewText(message.RoleSystem, "system rules")}
	for turn := range 5 {
		answer := fmt.Sprintf("answer-%d ", turn) + strings.Repeat("recent evidence ", 450)
		if turn < 2 {
			answer = fmt.Sprintf("answer-%d ", turn) + strings.Repeat("old archive evidence ", 2_500)
		}
		history = append(
			history,
			message.NewText(message.RoleUser, fmt.Sprintf("user-%d", turn)),
			message.NewText(message.RoleAssistant, answer),
		)
	}
	compacted, err := manager.CompactTo(ctx, history, 8_000)
	if err != nil {
		t.Fatal(err)
	}
	assertCompleteRecentTurns(t, compacted, 2, 3, 4)
	if tokens := estimateContextTokens(compacted); tokens > 8_000 {
		t.Fatalf("compacted tokens=%d, want within hard limit", tokens)
	}
}

func TestArchiveCompactionUsesProviderReportedPressureWhenLocalEstimateIsLow(t *testing.T) {
	var archivedSource []byte
	pressure := &providerContextPressure{toolTokens: 100}
	pressure.observeInputTokens(2_600)
	manager := turnContext{
		archiveEnabled:   true,
		keepRecentTokens: 100,
		providerPressure: pressure,
	}
	manager.storeArchive = func(_ context.Context, result contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error) {
		archivedSource = append([]byte(nil), result.Source...)
		manifest := result.Manifest
		manifest.SourceArtifactID = "provider-pressure"
		return manifest, nil, nil
	}
	manager.loadArchiveSource = func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), archivedSource...), nil
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "user-0"),
		message.NewText(message.RoleAssistant, strings.Repeat("old evidence ", 500)),
		message.NewText(message.RoleUser, "user-1"),
		message.NewText(message.RoleAssistant, "answer-1"),
		message.NewText(message.RoleUser, "user-2"),
		message.NewText(message.RoleAssistant, "answer-2"),
		message.NewText(message.RoleUser, "user-3"),
		message.NewText(message.RoleAssistant, "answer-3"),
	}
	if estimated := estimateContextTokens(history); estimated >= 2_000 {
		t.Fatalf("test requires a low local estimate, got %d", estimated)
	}
	compacted, err := manager.CompactTo(context.Background(), history, 2_000)
	mustTestNoError(t, err)
	manifest := archiveManifestInMessages(t, compacted)
	if manifest.SourceArtifactID != "provider-pressure" {
		t.Fatalf("archive manifest=%+v", manifest)
	}
	source, err := contextarchive.DecodeSource(archivedSource)
	mustTestNoError(t, err)
	if markers := archiveUserMarkers(source.Messages); !strings.Contains(markers, "user-0") {
		t.Fatalf("archive source users=%q", markers)
	}
	if reported := pressure.reportedHistoryTokens.Load(); reported != 0 {
		t.Fatalf("provider pressure remained after activation: %d", reported)
	}
}

func TestArchiveCompactionRelaxesOptionalTokenReserveButKeepsLatestThreeTurns(t *testing.T) {
	var archivedSource []byte
	manager := turnContext{archiveEnabled: true, keepRecentTokens: 20_000}
	manager.storeArchive = func(_ context.Context, result contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error) {
		archivedSource = append([]byte(nil), result.Source...)
		manifest := result.Manifest
		manifest.SourceArtifactID = "reserve-fallback"
		return manifest, nil, nil
	}
	manager.loadArchiveSource = func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), archivedSource...), nil
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "user-0"),
		message.NewText(message.RoleAssistant, strings.Repeat("old-evidence ", 10_000)),
		message.NewText(message.RoleUser, "user-1"),
		message.NewText(message.RoleAssistant, "answer-1"),
		message.NewText(message.RoleUser, "user-2"),
		message.NewText(message.RoleAssistant, "answer-2"),
		message.NewText(message.RoleUser, "user-3"),
		message.NewText(message.RoleAssistant, "answer-3"),
	}
	compacted, err := manager.CompactTo(context.Background(), history, 2_000)
	mustTestNoError(t, err)
	assertCompleteRecentTurns(t, compacted, 1, 2, 3)
	if tokens := estimateContextTokens(compacted); tokens > 2_000 {
		t.Fatalf("compacted tokens=%d, want <= 2000", tokens)
	}
	source, err := contextarchive.DecodeSource(archivedSource)
	mustTestNoError(t, err)
	if markers := archiveUserMarkers(source.Messages); !strings.Contains(markers, "user-0") || strings.Contains(markers, "user-1") {
		t.Fatalf("fallback archive source users=%q", markers)
	}
}

func TestArchiveCompactionRejectsLatestThreeTurnsThatExceedHardLimit(t *testing.T) {
	storeCalls := 0
	manager := turnContext{archiveEnabled: true}
	manager.storeArchive = func(_ context.Context, result contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error) {
		storeCalls++
		return result.Manifest, nil, nil
	}
	manager.loadArchiveSource = func(context.Context, string) ([]byte, error) { return nil, nil }
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "user-0"),
		message.NewText(message.RoleAssistant, "answer-0"),
		message.NewText(message.RoleUser, "user-1"),
		message.NewText(message.RoleAssistant, "answer-1"),
		message.NewText(message.RoleUser, "user-2"),
		message.NewText(message.RoleAssistant, "answer-2"),
		message.NewText(message.RoleUser, "user-3 "+strings.Repeat("mandatory-tail ", 8_000)),
		message.NewText(message.RoleAssistant, "answer-3"),
	}
	compacted, err := manager.CompactTo(context.Background(), history, 2_000)
	if !errors.Is(err, errArchiveCarrierTooLarge) {
		t.Fatalf("oversized mandatory tail error=%v", err)
	}
	if storeCalls != 0 {
		t.Fatalf("oversized mandatory tail persisted %d archive(s)", storeCalls)
	}
	if len(compacted) != len(history) {
		t.Fatalf("failed compaction changed live history: %d != %d", len(compacted), len(history))
	}
}

func TestArchiveCutPointErrorUsesProductNeutralLanguage(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "one large task"),
		message.NewText(message.RoleAssistant, "still working"),
	}
	_, err := snapcompactArchiveCutPoint(history, 1, 0)
	if err == nil {
		t.Fatal("single-turn history unexpectedly had an archive cut point")
	}
	if !strings.Contains(err.Error(), "no safe history cut point") {
		t.Fatalf("archive error exposed implementation branding: %q", err)
	}
}

func TestArchiveCompactionFitsOversizedAssistantGroupInRecentTurn(t *testing.T) {
	for _, turns := range []int{1, 3, 5} {
		t.Run(fmt.Sprintf("%d-users", turns), func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlitestore.Open(ctx, ":memory:")
			mustTestNoError(t, err)
			defer store.Close(ctx)
			sessions := session.NewService(store.DB(), store.Blobs())
			_, err = sessions.Ensure(ctx, session.Session{ID: "oversized", Title: "Oversized"})
			mustTestNoError(t, err)
			manager := archiveBackedTurnContext(sessions, NewAttachmentStore(t.TempDir()), "oversized", "run-1", false)
			manager.keepRecentTokens = 20_000
			history := []message.Message{message.NewText(message.RoleSystem, "system rules")}
			for turn := range turns {
				history = append(history, message.NewText(message.RoleUser, fmt.Sprintf("user-%d", turn)))
				if turn < turns-1 {
					history = append(history, message.NewText(message.RoleAssistant, fmt.Sprintf("answer-%d", turn)))
				}
			}
			groupStart := len(history)
			assistant := message.NewText(message.RoleAssistant, "working on the current task")
			assistant.Content = append(assistant.Content, message.ReasoningPart(strings.Repeat("still checking the same assignment. ", 16_000), "signature"))
			assistant.SyncLegacyContent()
			assistant.ToolCalls = []message.ToolCall{{ID: "completed-write", Name: "coding.write_file"}}
			history = append(history, assistant, message.NewToolResult(message.ToolResult{ToolCallID: "completed-write", Name: "coding.write_file", Content: "written"}))
			private := message.NewText(message.RoleUser, "host continuation; keep working")
			markPrivateMessage(&private)
			history = append(history, private)
			original := message.CloneMessages(history)
			compacted, err := manager.CompactTo(ctx, history, 20_000)
			mustTestNoError(t, err)
			mustTestNoError(t, message.ValidateCompleteTurns(compacted))
			if estimateContextTokens(compacted) > 20_000 || !reflect.DeepEqual(history, original) {
				t.Fatal("compaction exceeded the target or mutated its input")
			}
			if !reflect.DeepEqual(compacted[:groupStart], history[:groupStart]) || !reflect.DeepEqual(compacted[len(compacted)-1], private) {
				t.Fatal("compaction changed user instructions, prior turns, or host continuation")
			}
			manifest := archiveManifestInMessages(t, compacted)
			artifact, err := sessions.LoadArtifact(ctx, "oversized", manifest.SourceArtifactID)
			mustTestNoError(t, err)
			source, err := contextarchive.DecodeSource(artifact.Payload)
			mustTestNoError(t, err)
			if !reflect.DeepEqual(source.Messages, history[groupStart:groupStart+2]) {
				t.Fatal("archive lost reasoning, signature, or the completed tool pair")
			}
			expanded, err := manager.expandArchiveMessages(ctx, compacted, make(map[string]struct{}))
			mustTestNoError(t, err)
			if !reflect.DeepEqual(expanded, original) {
				t.Fatal("archive did not restore the original message order and contents")
			}
			// Repeated pressure must expand the existing source, not nest carriers.
			manager.providerPressure = &providerContextPressure{}
			manager.providerPressure.observeInputTokens(25_000)
			repeated, err := manager.CompactTo(ctx, compacted, 20_000)
			mustTestNoError(t, err)
			if got := archiveManifestInMessages(t, repeated); got.SourceSHA256 != manifest.SourceSHA256 {
				t.Fatal("repeated archive changed its complete source")
			}
		})
	}
}

func TestArchiveCompactionContinuesAfterLengthLimitedReasoning(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	mustTestNoError(t, err)
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	_, err = sessions.Ensure(ctx, session.Session{ID: "length", Title: "Length"})
	mustTestNoError(t, err)
	manager := archiveBackedTurnContext(sessions, NewAttachmentStore(t.TempDir()), "length", "run-1", false)
	manager.keepRecentTokens = 20_000
	// Reproduce the Anko shape: one user, completed tool work, then a 128k
	// output-limit response. The continuation must not replay the completed tool.
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "finish the typed variable assignment task"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "already-written", Name: "coding.write_file"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "already-written", Name: "coding.write_file", Content: "written"}),
	}
	thinking := strings.Repeat("Still checking the same variable assignment. ", 16_000)
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: thinking},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonLength, Usage: hyprovider.Usage{InputTokens: 110_659, OutputTokens: 128_000, TotalTokens: 238_659}},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: "Completed after context recovery."},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	result, err := (hyagent.Engine{Provider: driver}).RunMessages(ctx, hyagent.LoopInput{
		Model: "test", Messages: history, ContextTokenTarget: 100_000, MaxIterations: 2,
		CompactTo: manager.CompactTo,
		OutputGuardrails: []hyagent.OutputGuardrail{newUnexpectedStopGuard(config.LoopGuardConfig{
			UnexpectedStop: "mechanical", UnexpectedStopRetries: 1,
		})},
	})
	mustTestNoError(t, err)
	if result.StopReason != hyprovider.StopReasonComplete || len(driver.requests) != 2 || result.ToolCallsUsed != 0 {
		t.Fatalf("continuation: stop=%s requests=%d tool replays=%d", result.StopReason, len(driver.requests), result.ToolCallsUsed)
	}
	next := driver.requests[1].Messages
	if !reflect.DeepEqual(next[:len(history)], history) || estimateContextTokens(next) > 100_000 {
		t.Fatal("continuation lost prior work or exceeded its context target")
	}
	manifest := archiveManifestInMessages(t, next)
	artifact, err := sessions.LoadArtifact(ctx, "length", manifest.SourceArtifactID)
	mustTestNoError(t, err)
	source, err := contextarchive.DecodeSource(artifact.Payload)
	mustTestNoError(t, err)
	if len(source.Messages) != 1 || source.Messages[0].ReasoningContent() != thinking {
		t.Fatal("length-limited reasoning was not preserved in its exact source")
	}
	t.Logf("stop=%s requests=%d tool_replays=%d next_context_tokens=%d source_sha256=%s", result.StopReason, len(driver.requests), result.ToolCallsUsed, estimateContextTokens(next), manifest.SourceSHA256)
}

func TestArchiveFramesRepairFromDurableSourceAfterRestart(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	mustTestNoError(t, err)
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	_, err = sessions.Ensure(ctx, session.Session{ID: "repair-session", Title: "Repair"})
	mustTestNoError(t, err)
	attachments := NewAttachmentStore(filepath.Join(t.TempDir(), "attachments"))
	manager := archiveBackedTurnContext(sessions, attachments, "repair-session", "run-1", true)

	history := []message.Message{message.NewText(message.RoleSystem, "system rules")}
	for turn := range 5 {
		answer := fmt.Sprintf("回答-%d ", turn)
		if turn < 2 {
			answer += strings.Repeat("中文归档证据与 tool output。", 8_000)
		}
		history = append(history,
			message.NewText(message.RoleUser, fmt.Sprintf("用户-%d", turn)),
			message.NewText(message.RoleAssistant, answer),
		)
	}
	compacted, err := manager.CompactTo(ctx, history, 30_000)
	mustTestNoError(t, err)
	carrierIndex := archiveCarrierIndex(compacted)
	if carrierIndex < 0 {
		t.Fatal("archive carrier is missing")
	}
	manifest, _ := archiveManifestFromMessage(compacted[carrierIndex])
	archiveAttachments := AttachmentsFromMessage(compacted[carrierIndex])
	if manifest.Carrier != "bitmap" || len(archiveAttachments) == 0 {
		t.Fatalf("visual archive manifest=%+v attachments=%+v", manifest, archiveAttachments)
	}
	original, err := os.ReadFile(archiveAttachments[0].Path)
	mustTestNoError(t, err)
	mustTestNoError(t, os.WriteFile(archiveAttachments[0].Path, []byte("corrupt"), 0o600))

	boundary := int64(0)
	restarted := archiveBackedTurnContext(sessions, attachments, "repair-session", "run-2", true)
	restarted.instructions = "system rules"
	restarted.instructionFingerprint = "static"
	restarted.providerID = "provider"
	restarted.modelID = "model"
	restarted.checkpointBoundary = &boundary
	restarted.modelHistory = session.ModelHistory{
		ProviderID: "provider", ModelID: "model", InstructionFingerprint: "static", StaticPrefixHash: "static",
		WireVersion: session.CurrentWireVersion, CoveredThroughSequence: &boundary, Messages: compacted,
	}
	rebuilt, err := restarted.Build(ctx, hyagent.Request{})
	mustTestNoError(t, err)
	repairedIndex := archiveCarrierIndex(rebuilt)
	if repairedIndex < 0 {
		t.Fatal("repaired archive carrier is missing")
	}
	repairedAttachments := AttachmentsFromMessage(rebuilt[repairedIndex])
	repaired, err := os.ReadFile(repairedAttachments[0].Path)
	mustTestNoError(t, err)
	if repairedAttachments[0].ID != archiveAttachments[0].ID || !bytes.Equal(repaired, original) {
		t.Fatalf("frame repair changed identity or bytes: before=%+v after=%+v", archiveAttachments[0], repairedAttachments[0])
	}
}

func archiveBackedTurnContext(sessions *session.Service, attachments AttachmentStore, sessionID, runID string, visual bool) turnContext {
	manager := turnContext{sessionID: sessionID, runID: runID, archiveEnabled: true, archiveVisual: visual}
	manager.storeArchive = func(ctx context.Context, result contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error) {
		artifact, err := sessions.PutArtifact(ctx, sessionID, runID, "context_archive", result.Source, "archive integration test")
		if err != nil {
			return contextarchive.Manifest{}, nil, err
		}
		manifest := result.Manifest
		manifest.SourceArtifactID = artifact.ID
		stored := make([]session.Attachment, 0, len(result.Frames))
		for _, frame := range result.Frames {
			attachment, err := attachments.ImportGeneratedImageBytes(sessionID, fmt.Sprintf("frame-%03d.png", frame.Page), "image/png", frame.Bytes)
			if err != nil {
				return contextarchive.Manifest{}, nil, err
			}
			stored = append(stored, attachment)
		}
		return manifest, stored, nil
	}
	manager.loadArchiveSource = func(ctx context.Context, artifactID string) ([]byte, error) {
		artifact, err := sessions.LoadArtifact(ctx, sessionID, artifactID)
		return artifact.Payload, err
	}
	manager.readArchiveAttachment = func(_ context.Context, attachment session.Attachment) ([]byte, error) {
		return attachments.Read(sessionID, attachment)
	}
	return manager
}

func archiveManifestInMessages(t *testing.T, messages []message.Message) contextarchive.Manifest {
	t.Helper()
	for _, current := range messages {
		if manifest, ok := archiveManifestFromMessage(current); ok {
			return manifest
		}
	}
	t.Fatal("archive manifest is missing")
	return contextarchive.Manifest{}
}

func archiveCarrierIndex(messages []message.Message) int {
	for index := range messages {
		if _, ok := archiveManifestFromMessage(messages[index]); ok {
			return index
		}
	}
	return -1
}

func assertCompleteRecentTurns(t *testing.T, messages []message.Message, turns ...int) {
	t.Helper()
	joined := archiveUserMarkers(messages)
	for _, turn := range turns {
		if !strings.Contains(joined, fmt.Sprintf("user-%d", turn)) || !strings.Contains(joined, fmt.Sprintf("answer-%d", turn)) {
			t.Fatalf("recent turn %d is incomplete: %q", turn, joined)
		}
	}
}

func archiveUserMarkers(messages []message.Message) string {
	var values []string
	for _, current := range messages {
		values = append(values, current.Text)
	}
	return strings.Join(values, "\n")
}
