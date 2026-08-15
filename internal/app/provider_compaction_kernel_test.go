package app

import (
	"bytes"
	"context"
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

	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

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
		return func(context.Context, string) (string, error) { return semanticStateForTest("resolved summary"), nil }, 1000, nil
	}}
	result, err := manager.Compact(context.Background(), history)
	_, manifest := extractContextCheckpoint(result)
	manifestHash := ""
	if manifest != nil {
		manifestHash = manifest.ManifestHash
	}
	if err != nil || len(result) >= len(history) || activated != activeCacheIdentity("static", manifestHash, compactionSummaryHash(result)) {
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
	manager := turnContext{structuredSummary: true, compactTargetTokens: 500, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		resolved.Do(func() { resolveCalls++ })
		return func(_ context.Context, input string) (string, error) {
			inputs = append(inputs, input)
			return semanticStateForTest("continue"), nil
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
	got, err := manager.CompactTo(context.Background(), history, 700)
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
	if estimateContextTokens(got) > 500 {
		t.Fatalf("compacted tokens=%d target=500", estimateContextTokens(got))
	}
	before := len(inputs)
	if _, err := manager.CompactTo(context.Background(), got, 500); err != nil || len(inputs) != before {
		t.Fatalf("repeat compact calls=%d want=%d err=%v", len(inputs), before, err)
	}
}

func TestPhase3IrreducibleAtomicGroupReturnsExplicitError(t *testing.T) {
	manager := turnContext{structuredSummary: true, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(context.Context, string) (string, error) { return semanticStateForTest("x"), nil }, 100, nil
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
			return semanticStateForTest("continue"), nil
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
			return semanticStateForTest("continue"), nil
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
			return semanticStateForTest("reduced"), nil
		}, 1000, nil
	}}
	previous := []string{semanticStateForTest("OLDEST"), semanticStateForTest("NEWEST")}
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
		message.NewText(message.RoleUser, strings.Repeat("old", 800)),
		message.NewText(message.RoleAssistant, "answer"),
		message.NewText(message.RoleUser, "follow-up one"),
		message.NewText(message.RoleAssistant, "answer one"),
		message.NewText(message.RoleUser, "follow-up two"),
		message.NewText(message.RoleAssistant, "answer two"),
		message.NewText(message.RoleUser, "latest"),
	}
	manager := turnContext{structuredSummary: true, compactTargetTokens: 400, resolveSummarizer: func(context.Context) (func(context.Context, string) (string, error), int, error) {
		return func(context.Context, string) (string, error) { return "", errors.New("map failed") }, 1000, nil
	}}
	got, err := manager.CompactTo(context.Background(), history, 500)
	if err == nil || !strings.Contains(err.Error(), "map failed") {
		t.Fatalf("error=%v", err)
	}
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("failed compaction mutated checkpoint: %#v", got)
	}
}

func TestPhase3DurableProvenanceUsesSequence(t *testing.T) {
	value, ok := blockMessage(session.Block{Sequence: 42, Kind: "user", Content: "source"})
	if !ok || !reflect.DeepEqual(messageStableReferences(value, ""), []string{"sequence:42"}) {
		t.Fatalf("message=%#v", value)
	}
	failed, ok := blockMessage(session.Block{Sequence: 43, Kind: "assistant", State: "failed", Content: "partial output"})
	if !ok || failed.Text != failedAssistantLabel+"partial output" {
		t.Fatalf("failed assistant message=%#v", failed)
	}
	if _, err := normalizeSemanticStateV1("not-json", map[string]string{"sequence:42": "user"}); err == nil {
		t.Fatal("accepted non-JSON semantic state")
	}
	normalized, err := normalizeSemanticStateV1(
		`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"kind":"sequence","id":"42"}]}}`,
		map[string]string{"sequence:42": "user"},
	)
	if err != nil {
		t.Fatalf("host provenance did not replace model references: %v", err)
	}
	var summary SemanticStateV1
	if err := json.Unmarshal([]byte(normalized), &summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary.Objective.Sources, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}) || summary.Objective.Authority != "user" {
		t.Fatalf("normalized provenance=%+v", summary.Objective)
	}
}

func TestNormalizeSemanticStateAcceptsWholeResponseJSONFence(t *testing.T) {
	authorities := map[string]string{"sequence:42": "user"}
	raw := "```json\n" + `{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"kind":"sequence","id":"42"}]}}` + "\n```"
	normalized, err := normalizeSemanticStateV1(raw, authorities)
	if err != nil {
		t.Fatalf("whole-response JSON fence rejected: %v", err)
	}
	var state SemanticStateV1
	if err := json.Unmarshal([]byte(normalized), &state); err != nil {
		t.Fatal(err)
	}
	if state.Objective.Text != "x" {
		t.Fatalf("objective=%q", state.Objective.Text)
	}
	if _, err := normalizeSemanticStateV1("Here is the state:\n```json\n"+`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"kind":"sequence","id":"42"}]}}`+"\n```", authorities); err == nil {
		t.Fatal("accepted prose around a JSON fence")
	}
	if _, err := normalizeSemanticStateV1("```json\n"+`{"version":1}`+"\n```\n```json\n"+`{"version":1}`+"\n```", authorities); err == nil {
		t.Fatal("accepted nested JSON fences")
	}
	if _, err := normalizeSemanticStateV1("```markdown\n"+`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"kind":"sequence","id":"42"}]}}`+"\n```", authorities); err == nil {
		t.Fatal("accepted unsupported fence language")
	}
}

func TestPhase3ProvenanceMismatchDowngradesToSupportedAgentFact(t *testing.T) {
	normalized, err := normalizeSemanticStateV1(
		`{"version":1,"objective":{"text":"continue the review","status":"active","authority":"agent","confidence":"inferred","sources":[{"kind":"checkpoint","id":"carried:evidence"}]},"constraints":[{"text":"Plugin manifest paths must remain inside the plugin root.","status":"active","authority":"workspace","confidence":"verified","sources":[{"kind":"checkpoint","id":"invented:workspace"}]}]}`,
		map[string]string{"checkpoint:carried:evidence": "agent"},
	)
	if err != nil {
		t.Fatalf("mismatched carried provenance rejected: %v", err)
	}
	var state SemanticStateV1
	if err := json.Unmarshal([]byte(normalized), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Constraints) != 1 {
		t.Fatalf("constraints=%+v", state.Constraints)
	}
	fact := state.Constraints[0]
	if fact.Authority != "agent" || fact.Confidence != "inferred" ||
		!reflect.DeepEqual(fact.Sources, []EvidenceRefV1{{Kind: "checkpoint", ID: "carried:evidence"}}) {
		t.Fatalf("downgraded fact=%+v", fact)
	}
}

func TestSemanticPatchSkipsUnchangedFacts(t *testing.T) {
	fact := StateFactV1{ID: "objective-1", Text: "ship", Status: "active", Authority: "user", Confidence: "reported", Sources: []EvidenceRefV1{{Kind: "sequence", ID: "1"}}}
	state := SemanticStateV1{Version: 1, Objective: fact}
	patch := semanticStatePatch(2, session.WriterCursorV1{CanonicalSequence: 3}, "digest", state, state)
	if len(patch.Operations) != 0 {
		t.Fatalf("unchanged facts produced operations: %+v", patch.Operations)
	}
}

func TestLatestSubagentCursorUsesStableFinishOrder(t *testing.T) {
	finished := time.Unix(100, 0).UTC()
	gotTime, gotID := latestSubagentCursor([]agentservice.SubagentSnapshot{
		{Run: agentservice.SubagentRun{ID: "agent-a", FinishedAt: finished}},
		{Run: agentservice.SubagentRun{ID: "agent-b", FinishedAt: finished}},
		{Run: agentservice.SubagentRun{ID: "running"}},
	})
	if gotTime != finished.UnixNano() || gotID != "agent-b" {
		t.Fatalf("cursor=%d/%q", gotTime, gotID)
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

func TestTurnContextCompactPreservesFullSystemPrefixAndFreshTodo(t *testing.T) {
	latest := session.TodoList{Goal: "ship", Revision: 4, Phases: []session.TodoPhase{{
		ID: "phase", Title: "Build", Items: []session.TodoItem{{ID: "current", Content: "verify", Status: session.TodoInProgress}},
	}}}
	manager := turnContext{
		loadTodo:  func(context.Context) (session.TodoList, error) { return latest, nil },
		summarize: func(context.Context, string) (string, error) { return semanticStateForTest("todo summary"), nil },
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
		summarize: func(context.Context, string) (string, error) { return semanticStateForTest("todo summary"), nil },
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
		message.NewText(message.RoleAssistant, semanticStateSafetyLabel+semanticStateForTest("todo summary")),
		history[7],
		manager.todoReminderMessage(todoReminder(latest)),
	}
	expected[2].Kind = message.KindCompactionSummary
	expected[2].Visibility = message.VisibilityPrivate
	expected[2].CreatedAt = time.Time{}
	target := 1600
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
	manager := turnContext{summarize: func(context.Context, string) (string, error) {
		return semanticStateForTest("current work summary"), nil
	}}
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
	if reflect.DeepEqual(compacted, history) || !slices.ContainsFunc(compacted, func(current message.Message) bool { return current.Kind == message.KindCompactionSummary }) {
		t.Fatalf("history did not switch to a semantic checkpoint: %#v", compacted)
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
	old := message.NewText(message.RoleAssistant, semanticStateSafetyLabel+semanticStateForTest("preserve the old decision"))
	old.Kind = message.KindCompactionSummary
	old.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"), old,
		message.NewText(message.RoleUser, "older request"),
		message.NewText(message.RoleAssistant, strings.Repeat("older work ", 300)),
		message.NewText(message.RoleUser, "newest request"),
	}
	var inputs []string
	calls := 0
	manager := turnContext{summarize: func(_ context.Context, transcript string) (string, error) {
		calls++
		inputs = append(inputs, transcript)
		return semanticStateForTest("newest request; old decision retained; continue in provider_context.go"), nil
	}}
	compacted, err := manager.CompactTo(context.Background(), history, 300)
	if err != nil {
		t.Fatal(err)
	}
	joinedInputs := strings.Join(inputs, "\n")
	if !strings.Contains(joinedInputs, "old decision") {
		t.Fatalf("recursive summary input omitted history: %q", joinedInputs)
	}
	if !slices.ContainsFunc(compacted, func(current message.Message) bool {
		return current.Role == message.RoleUser && current.Text == "older request"
	}) {
		t.Fatalf("recent exact user evidence was lost: %#v", compacted)
	}
	summaries := 0
	for _, current := range compacted {
		if current.Kind == message.KindCompactionSummary {
			summaries++
			if current.Role != message.RoleAssistant || current.Visibility != message.VisibilityPrivate || !strings.Contains(current.Text, "Host-validated semantic state") || !strings.Contains(current.Text, "newest request") {
				t.Fatalf("generated summary = %#v", current)
			}
		}
	}
	if calls < 1 || summaries != 1 || compacted[len(compacted)-1].Text != "newest request" {
		t.Fatalf("compacted history = %#v", compacted)
	}
}

func TestTurnContextRollsOversizedCompletedToolResultIntoSummary(t *testing.T) {
	previous := message.NewText(message.RoleAssistant, semanticStateSafetyLabel+semanticStateForTest("keep state"))
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
		return semanticStateForTest("rolled oversized tool evidence"), nil
	}}
	got, err := manager.CompactTo(context.Background(), history, 500)
	if err != nil || reflect.DeepEqual(got, history) || calls == 0 {
		t.Fatalf("oversized tool result = calls:%d history:%#v error:%v", calls, got, err)
	}
	assertRollingToolCheckpoint(t, got, history[2])
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

func TestCompactionSummaryRetriesWithLowReasoningAfterReasoningExhaustsOutput(t *testing.T) {
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: "reasoning consumed the output budget"},
			{Kind: hyprovider.EventTextDelta, Text: `{"version":1,"objective":{"text":"truncated`},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonMaxTurns},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: "summary"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}

	got, err := compactionSummarizer(driver, "deepseek", "deepseek-v4-flash", "max", "cache", 128_000, 8_192)(context.Background(), "history")
	if err != nil || got != "summary" {
		t.Fatalf("compaction summary=%q error=%v", got, err)
	}
	if len(driver.requests) != 2 {
		t.Fatalf("compaction requests=%d, want reasoning fallback retry", len(driver.requests))
	}
	if got := driver.requests[0].Metadata["reasoning_effort"]; got != "max" {
		t.Fatalf("initial reasoning effort=%q", got)
	}
	if got := driver.requests[1].Metadata["reasoning_effort"]; got != "low" {
		t.Fatalf("fallback reasoning effort=%q", got)
	}
	if got := driver.requests[0].ExtraBody["max_output_tokens"]; got != 32_768 {
		t.Fatalf("reasoning generation budget=%v, want 32768 tokens of headroom", got)
	}
	if got := driver.requests[1].ExtraBody["max_output_tokens"]; got != 16_384 {
		t.Fatalf("low-reasoning generation budget=%v, want 16384 tokens of JSON completion headroom", got)
	}
}

func TestTurnContextAdvancesSemanticRevisionAfterEachActivation(t *testing.T) {
	manager := turnContext{
		sessionID:          "session-1",
		runID:              "run-1",
		staticIdentity:     "static",
		coordinator:        &compactionCoordinator{},
		semanticCheckpoint: session.SemanticCheckpointV1{SessionID: "session-1", State: json.RawMessage(`{"version":1}`)},
	}
	persistedRevision := int64(0)
	manager.activateCompaction = func(_ context.Context, messages []message.Message, _ string) error {
		_, commit := extractContextCheckpointMetadata(messages)
		if commit == nil {
			return errors.New("missing semantic commit")
		}
		if commit.BaseRevision != persistedRevision {
			return fmt.Errorf("semantic state source is stale: expected revision %d, current revision %d", commit.BaseRevision, persistedRevision)
		}
		persistedRevision++
		return nil
	}

	for revision := int64(0); revision < 2; revision++ {
		source := []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, fmt.Sprintf("request-%d", revision)),
		}
		summary := message.NewText(message.RoleAssistant, semanticStateSafetyLabel+semanticStateForTest(fmt.Sprintf("objective-%d", revision)))
		summary.Kind = message.KindCompactionSummary
		metadata, err := buildContextCheckpointMetadata(manager, "automatic_hard", source, []message.Message{summary}, semanticStateForTest(fmt.Sprintf("objective-%d", revision)), nil, 512)
		if err != nil {
			t.Fatal(err)
		}
		summary, err = attachContextCheckpoint(summary, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = manager.activateCompactionResult(context.Background(), []message.Message{summary}); err != nil {
			t.Fatalf("activation %d failed: %v", revision+1, err)
		}
	}
	if persistedRevision != 2 {
		t.Fatalf("persisted semantic revision=%d, want 2", persistedRevision)
	}
}

type fakeSemanticStore struct {
	mu         sync.Mutex
	checkpoint session.SemanticCheckpointV1
}

func newFakeSemanticStore(revision int64) *fakeSemanticStore {
	return &fakeSemanticStore{checkpoint: session.SemanticCheckpointV1{
		SessionID:    "session-1",
		Revision:     revision,
		State:        json.RawMessage(semanticStateForTest("already durable")),
		SourceDigest: strings.Repeat("a", 64),
		Cursor:       session.WriterCursorV1{CanonicalSequence: -1},
	}}
}

func (s *fakeSemanticStore) activate(_ context.Context, messages []message.Message, _ string) error {
	_, commit := extractContextCheckpointMetadata(messages)
	if commit == nil {
		return errors.New("missing semantic commit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkpoint.Revision == commit.BaseRevision+1 && s.checkpoint.SourceDigest == commit.SourceDigest {
		if s.checkpoint.Cursor != commit.Cursor || !bytes.Equal(s.checkpoint.State, commit.State) {
			return fmt.Errorf("%w: %w: semantic state diverged for source digest", session.ErrRunCheckpointStale, session.ErrSemanticStateStale)
		}
		return nil
	}
	if s.checkpoint.Revision != commit.BaseRevision {
		return fmt.Errorf("%w: %w: expected revision %d, current revision %d", session.ErrRunCheckpointStale, session.ErrSemanticStateStale, commit.BaseRevision, s.checkpoint.Revision)
	}
	s.checkpoint = session.SemanticCheckpointV1{
		ID: commit.CheckpointID, SessionID: "session-1", Revision: commit.BaseRevision + 1,
		Cursor: commit.Cursor, State: append(json.RawMessage(nil), commit.State...), SourceDigest: commit.SourceDigest,
	}
	return nil
}

func (s *fakeSemanticStore) load(_ context.Context) (session.SemanticCheckpointV1, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSemanticCheckpoint(s.checkpoint), nil
}

func (s *fakeSemanticStore) revision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoint.Revision
}

func compactionHistoryForStaleTest() []message.Message {
	return []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "old request"),
		message.NewText(message.RoleAssistant, strings.Repeat("old result ", 600)),
		message.NewText(message.RoleUser, "current request"),
	}
}

func TestTurnContextCompactToAdoptsDurableRevisionBeforeWriting(t *testing.T) {
	store := newFakeSemanticStore(1)
	manager := turnContext{
		sessionID:              "session-1",
		runID:                  "run-1",
		staticIdentity:         "static",
		coordinator:            &compactionCoordinator{},
		semanticCheckpoint:     session.SemanticCheckpointV1{SessionID: "session-1", State: json.RawMessage(`{"version":1}`)},
		activateCompaction:     store.activate,
		loadSemanticCheckpoint: store.load,
		summarize:              func(context.Context, string) (string, error) { return semanticStateForTest("rebased work"), nil },
	}
	history := compactionHistoryForStaleTest()
	compacted, err := manager.CompactTo(context.Background(), history, 500)
	if err != nil {
		t.Fatalf("CompactTo failed after durable revision 1 already existed: %v", err)
	}
	_, commit := extractContextCheckpointMetadata(compacted)
	if commit == nil || commit.BaseRevision != 1 {
		t.Fatalf("compacted commit=%+v, want base revision 1", commit)
	}
	if store.revision() != 2 {
		t.Fatalf("durable revision=%d, want 2 after rebase", store.revision())
	}
}

func TestTurnContextCompactAdoptsDurableRevisionBeforeWriting(t *testing.T) {
	store := newFakeSemanticStore(1)
	manager := turnContext{
		sessionID:              "session-1",
		runID:                  "run-1",
		staticIdentity:         "static",
		compactTargetTokens:    500,
		coordinator:            &compactionCoordinator{},
		semanticCheckpoint:     session.SemanticCheckpointV1{SessionID: "session-1", State: json.RawMessage(`{"version":1}`)},
		activateCompaction:     store.activate,
		loadSemanticCheckpoint: store.load,
		summarize:              func(context.Context, string) (string, error) { return semanticStateForTest("rebased compact"), nil },
	}
	compacted, err := manager.Compact(context.Background(), compactionHistoryForStaleTest())
	if err != nil {
		t.Fatalf("Compact failed after durable revision 1 already existed: %v", err)
	}
	_, commit := extractContextCheckpointMetadata(compacted)
	if commit == nil || commit.BaseRevision != 1 {
		t.Fatalf("compacted commit=%+v, want base revision 1", commit)
	}
}

func TestTurnContextCompactToRecoversStalePreparedActivation(t *testing.T) {
	store := newFakeSemanticStore(1)
	history := compactionHistoryForStaleTest()
	manager := turnContext{
		sessionID:              "session-1",
		runID:                  "run-1",
		staticIdentity:         "static",
		compactTargetTokens:    500,
		softTriggerTokens:      100,
		coordinator:            &compactionCoordinator{},
		semanticCheckpoint:     session.SemanticCheckpointV1{SessionID: "session-1", State: json.RawMessage(`{"version":1}`)},
		activateCompaction:     store.activate,
		loadSemanticCheckpoint: store.load,
		summarize: func(context.Context, string) (string, error) {
			return semanticStateForTest("prepared then rebased"), nil
		},
	}
	summary := message.NewText(message.RoleAssistant, semanticStateSafetyLabel+semanticStateForTest("stale prepared state"))
	summary.Kind = message.KindCompactionSummary
	summary.Visibility = message.VisibilityPrivate
	metadata, err := buildContextCheckpointMetadata(manager, "automatic_soft", history, []message.Message{summary}, semanticStateForTest("stale prepared state"), nil, 500)
	if err != nil {
		t.Fatal(err)
	}
	summary, err = attachContextCheckpoint(summary, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Commit.BaseRevision != 0 {
		t.Fatalf("prepared commit base=%d, want 0", metadata.Commit.BaseRevision)
	}
	prepared := []message.Message{history[0], summary, history[len(history)-1]}
	done := make(chan struct{})
	close(done)
	manager.coordinator.hash = compactionSourceHash(history, 500, "static")
	manager.coordinator.source = append([]message.Message(nil), history...)
	manager.coordinator.done = done
	manager.coordinator.result = prepared

	compacted, err := manager.CompactTo(context.Background(), history, 400)
	if err != nil {
		t.Fatalf("stale prepared activation failed the run: %v", err)
	}
	_, commit := extractContextCheckpointMetadata(compacted)
	if commit == nil || commit.BaseRevision != 1 {
		t.Fatalf("recovered commit=%+v, want base revision 1", commit)
	}
	if store.revision() != 2 {
		t.Fatalf("durable revision=%d, want 2 after stale prepared recovery", store.revision())
	}
}

func TestShortInternalGenerationRetriesWithLowReasoningAfterOutputExhaustion(t *testing.T) {
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: "reasoning consumed the output budget"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonMaxTurns},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: "concise result"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	request := hyprovider.Request{
		Model:     "deepseek-v4-flash",
		Metadata:  map[string]string{"reasoning_effort": "max"},
		ExtraBody: map[string]any{"max_output_tokens": 256},
	}

	got, err := collectProviderTextWithReasoningFallback(context.Background(), driver, request, "recap")
	if err != nil || got != "concise result" {
		t.Fatalf("short generation=%q error=%v", got, err)
	}
	if len(driver.requests) != 2 || driver.requests[1].Metadata["reasoning_effort"] != "low" {
		t.Fatalf("short generation requests=%#v", driver.requests)
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
	oversizedOutput := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("summary", 200)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("summary", 200)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("summary", 200)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	if _, err := compactionSummarizer(oversizedOutput, "grok", "model", "low", "cache", 2_000, 200)(context.Background(), "small input"); err == nil || !strings.Contains(err.Error(), "after 2 repairs") {
		t.Fatalf("oversized summary output error=%v", err)
	}
	if len(oversizedOutput.requests) != 3 || !strings.Contains(oversizedOutput.requests[1].Messages[1].Text, "Rewrite the candidate") || !strings.Contains(oversizedOutput.requests[1].Messages[1].Text, "at most 544 UTF-8 bytes") {
		t.Fatalf("oversized summary repair requests=%#v", oversizedOutput.requests)
	}
	if got := oversizedOutput.requests[1].ExtraBody["max_output_tokens"]; got != 200 {
		t.Fatalf("repair generation budget=%v, want final-state limit 200", got)
	}
	twoStageRepair := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("x", 900)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("x", 810)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("x", 500)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	if got, err := compactionSummarizer(twoStageRepair, "chatgpt", "model", "low", "cache", 2_000, 200)(context.Background(), "small input"); err != nil || len(got) != 500 || len(twoStageRepair.requests) != 3 {
		t.Fatalf("two-stage repaired summary bytes=%d requests=%d error=%v", len(got), len(twoStageRepair.requests), err)
	}
	repairedOutput := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventTextDelta, Text: strings.Repeat("summary", 200)},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: "compact summary"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	if got, err := compactionSummarizer(repairedOutput, "chatgpt", "model", "low", "cache", 2_000, 200)(context.Background(), "small input"); err != nil || got != "compact summary" {
		t.Fatalf("repaired summary=%q error=%v", got, err)
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
	instructions := chatGPT.requests[0].Messages[0].Text
	if !strings.Contains(instructions, "at most 800 UTF-8 bytes") || !strings.Contains(instructions, "hard limit") {
		t.Fatalf("ChatGPT summary instructions do not carry the configured output limit: %q", instructions)
	}
}

func TestCompactionSummaryLimitRetainsLargeSemanticState(t *testing.T) {
	summaryTokens, inputTokens := resolveCompactionLimits(272_000, 32768)
	if summaryTokens != 32768 || inputTokens != 238_208 {
		t.Fatalf("compaction limits = summary %d input %d", summaryTokens, inputTokens)
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
			writeProviderText(writer, "compact-response", semanticStateForTest("preserve the task"))
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
			return semanticStateForTest("summary"), nil
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
	if _, err := manager.CompactTo(context.Background(), history, 300); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 || pre != 1 || post != 1 {
		t.Fatalf("changed call: summarize=%d pre=%d post=%d", summaries, pre, post)
	}
}
