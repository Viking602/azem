package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

func TestTurnContextBuildFallsBackWhenInstructionFingerprintDiffers(t *testing.T) {
	boundary := int64(2)
	staleState := json.RawMessage(`[{"type":"reasoning","id":"stale"}]`)
	var degradedReason string
	manager := turnContext{
		instructions: mainInstructions,
		providerID:   "chatgpt",
		modelID:      "gpt-test",
		history: []session.Block{
			{Sequence: 1, Kind: "user", Content: "canonical request"},
			{Sequence: 2, Kind: "assistant", Content: "canonical answer"},
		},
		modelHistory: session.ModelHistory{
			ProviderID:             "chatgpt",
			ModelID:                "gpt-test",
			InstructionFingerprint: "stale-fingerprint",
			StaticPrefixHash:       "stale-fingerprint",
			WireVersion:            session.CurrentWireVersion,
			CoveredThroughSequence: &boundary,
			Messages: []message.Message{{
				Role:          message.RoleAssistant,
				Text:          "stale provider answer",
				ProviderState: staleState,
			}},
		},
		checkpointBoundary:        &boundary,
		reportCachePrefixDegraded: func(reason string) { degradedReason = reason },
	}
	messages, err := manager.Build(context.Background(), api.Task{Goal: "current goal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 || messages[0].Role != message.RoleSystem || messages[0].Text != mainInstructions {
		t.Fatalf("fallback prefix = %#v", messages)
	}
	if messages[1].Role != message.RoleUser || messages[1].Text != "canonical request" ||
		messages[2].Role != message.RoleAssistant || messages[2].Text != "canonical answer" ||
		messages[3].Role != message.RoleUser || messages[3].Text != "current goal" {
		t.Fatalf("fallback canonical history = %#v", messages)
	}
	for _, current := range messages {
		if len(current.ProviderState) > 0 || current.Text == "stale provider answer" {
			t.Fatalf("fallback reused stale provider state: %#v", messages)
		}
	}
	if !strings.Contains(degradedReason, "without provider state") {
		t.Fatalf("expected cache-prefix degradation report, got %q", degradedReason)
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
	inner := turnContext{instructions: "rules", summarize: func(context.Context, string) (string, error) { return semanticStateForTest("guidance summary"), nil }}
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
		message.NewText(message.RoleUser, "follow up one"),
		message.NewText(message.RoleAssistant, "answer one"),
		message.NewText(message.RoleUser, "follow up two"),
		message.NewText(message.RoleAssistant, "answer two"),
		message.NewText(message.RoleUser, "latest"),
	}
	prepared, err := manager.CompactTo(context.Background(), history, 300)
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
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0].Text != "terminal correction" {
		t.Fatalf("terminal guidance = %#v", pending)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "accepted after retry"); err != nil {
		t.Fatalf("guidance closed while terminal retry was required: %v", err)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0].Text != "accepted after retry" {
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

func TestActiveGuidanceKeepsImageAttachments(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	image, err := service.ImportImageBytes("session-guided", "guidance.png", "image/png", minimalPNG())
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.mu.Unlock()
	if err := service.GuideActiveTurnWithAttachments("session-guided", "run-guided", "inspect this update", []session.Attachment{image}); err != nil {
		t.Fatal(err)
	}
	pending := service.finishActiveGuidance("session-guided", "run-guided")
	messages := guidanceMessages(pending)
	if len(messages) != 1 || messages[0].Text != "inspect this update" {
		t.Fatalf("guidance messages = %#v", messages)
	}
	attachments := AttachmentsFromMessage(messages[0])
	if len(attachments) != 1 || attachments[0] != image {
		t.Fatalf("guidance attachments = %#v", attachments)
	}
}

func TestActiveGuidanceAppearsAtEveryPendingModelBoundary(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.activeGuidance = []activeGuidanceMessage{{Text: "change direction"}}
	service.mu.Unlock()
	hook := activeGuidanceModelHook{
		peek: func() activeGuidanceSnapshot {
			return service.peekActiveGuidance("session-guided", "run-guided")
		},
	}
	history := []message.Message{message.NewText(message.RoleUser, "original task")}
	for boundary := range 2 {
		got, err := hook.TransformContext(context.Background(), history)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[1].Role != message.RoleUser || got[1].Text != "change direction" {
			t.Fatalf("boundary %d messages = %#v", boundary, got)
		}
		if len(history) != 1 {
			t.Fatalf("hook mutated engine history: %#v", history)
		}
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
		return semanticStateForTest("summary"), nil
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
		return semanticStateForTest("model-generated team summary"), nil
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
		summarize: func(context.Context, string) (string, error) { return semanticStateForTest("todo summary"), nil },
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

func TestRecentUserSelectionIgnoresAgentBlocks(t *testing.T) {
	blocks := []session.Block{
		{Kind: "user", Content: "old"},
		{Kind: "assistant", Content: "old answer"},
		{Kind: "user", Content: "latest guidance"},
		{Kind: "agent", Content: "one"},
		{Kind: "agent", Content: "two"},
		{Kind: "agent", Content: "three"},
		{Kind: "agent", Content: "four"},
		{Kind: "agent", Content: "five"},
	}
	var messages []message.Message
	for _, block := range blocks {
		if current, ok := blockMessage(block); ok {
			messages = append(messages, current)
		}
	}
	if indexes := recentUserIndexes(messages, 0, contextRecentUserTurns); !reflect.DeepEqual(indexes, []int{0, 2}) {
		t.Fatalf("recent user indexes = %v", indexes)
	}
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
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-test", staticIdentity: "durable-static-prefix",
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-test",
			InstructionFingerprint: mainInstructionFingerprint, StaticPrefixHash: "durable-static-prefix",
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
	saved := []message.Message{
		message.NewText(message.RoleSystem, mainInstructions),
		message.NewText(message.RoleUser, "original request"),
		message.NewText(message.RoleAssistant, "semantic checkpoint"),
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
	if !strings.HasPrefix(captured[0].Instructions, mainInstructions) {
		t.Fatalf("first public instructions do not start with main instructions:\n%s", captured[0].Instructions)
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
