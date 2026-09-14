package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

func TestSessionReferenceReachesProviderAndSurvivesFollowUpAndRebuild(t *testing.T) {
	var captured []string
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: test\n---\ntest", nil, func(call int, body string, writer http.ResponseWriter) {
		captured = append(captured, body)
		writeProviderText(writer, fmt.Sprintf("reference-%d", call), "Reference received.")
	})
	svc := harness.service
	ctx := context.Background()
	svc.recap = recap.NewService(harness.store.DB(), harness.workspace)
	_, err := svc.sessions.Ensure(ctx, session.Session{ID: "source", Title: "设计方案", Workspace: harness.workspace})
	requireAppTestNoError(t, err)
	requireAppTestNoError(t, svc.sessions.SetWorkspaceSession(ctx, harness.workspace, "source"))
	for turn := 1; turn <= 4; turn++ {
		for _, kind := range []string{"user", "assistant"} {
			_, err := svc.sessions.AppendBlock(ctx, "source", session.Block{Kind: kind, RunID: fmt.Sprintf("old-%d", turn), Content: fmt.Sprintf("%s_ROUND_%d", kind, turn)})
			requireAppTestNoError(t, err)
		}
	}
	_, err = svc.sessions.AppendBlock(ctx, "source", session.Block{Kind: "tool", Content: "TOOL_PAYLOAD_MUST_NOT_LOAD"})
	requireAppTestNoError(t, err)
	// A corrupt unrelated tool proves that reference retrieval does not hydrate
	// the full source projection, provider checkpoint or tool payloads.
	_, err = harness.store.DB().ExecContext(ctx, `UPDATE session_blocks SET data='not-json',data_sha256='' WHERE session_id='source' AND kind='tool'`)
	requireAppTestNoError(t, err)
	_, err = svc.recap.Upsert(ctx, recap.Recap{SessionID: "source", Summary: "EXISTING_SUMMARY", Goal: "ORIGINAL_GOAL", OpenItems: "OPEN_DECISION"})
	requireAppTestNoError(t, err)
	run, err := svc.StartConfiguredTurn(TurnRequest{SessionID: "target", Prompt: "继续 @[显示标题](azem-session:source) 并检查 @README.md", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	requireAppTestNoError(t, err)
	waitForProviderRun(t, svc, run)
	projection, err := svc.sessions.LoadProjection(ctx, "target")
	requireAppTestNoError(t, err)
	block := projection.Blocks[0]
	if block.Content != "继续 @设计方案 并检查 @README.md" || block.Data[sessionReferenceDataKey] == "" {
		t.Fatalf("missing durable reference: %#v", block)
	}
	for _, sentinel := range []string{"EXISTING_SUMMARY", "ORIGINAL_GOAL", "OPEN_DECISION", "user_ROUND_2", "assistant_ROUND_4"} {
		if !strings.Contains(captured[0], sentinel) {
			t.Errorf("first provider request omitted %s", sentinel)
		}
	}
	for _, excluded := range []string{"ROUND_1", "TOOL_PAYLOAD_MUST_NOT_LOAD"} {
		if strings.Contains(captured[0], excluded) {
			t.Errorf("provider received excluded source content %s", excluded)
		}
	}
	var request map[string]any
	requireAppTestNoError(t, json.Unmarshal([]byte(captured[0]), &request))
	if strings.Contains(fmt.Sprint(request["instructions"]), "EXISTING_SUMMARY") {
		t.Fatal("referenced conversation was promoted into system instructions")
	}
	_, err = svc.sessions.AppendBlock(ctx, "source", session.Block{Kind: "user", Content: "LATER_SOURCE_MUTATION"})
	requireAppTestNoError(t, err)
	for _, prompt := range []string{"继续当前任务", "重建后继续"} {
		if prompt == "重建后继续" {
			// A canonical append invalidates the replaceable model checkpoint.
			_, err = svc.sessions.AppendBlock(ctx, "target", session.Block{Kind: "user", Content: "追加可持久化背景"})
			requireAppTestNoError(t, err)
		}
		run, err = svc.StartConfiguredTurn(TurnRequest{SessionID: "target", Prompt: prompt, Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
		requireAppTestNoError(t, err)
		waitForProviderRun(t, svc, run)
		body := captured[len(captured)-1]
		if !strings.Contains(body, "EXISTING_SUMMARY") || !strings.Contains(body, "assistant_ROUND_4") || strings.Contains(body, "LATER_SOURCE_MUTATION") {
			t.Fatal("follow-up did not retain the original immutable reference snapshot")
		}
	}
	// Repeated references are deduplicated and each source is checked against
	// durable project ownership, independent of the display title supplied by UI.
	_, data, err := svc.resolveSessionReferences(ctx, "target", "@[a](azem-session:source) @[b](azem-session:source)")
	requireAppTestNoError(t, err)
	var refs []referencedConversation
	requireAppTestNoError(t, json.Unmarshal([]byte(data), &refs))
	if len(refs) != 1 {
		t.Fatalf("references = %d", len(refs))
	}
	other := t.TempDir()
	_, err = svc.sessions.Ensure(ctx, session.Session{ID: "other", Workspace: other, Title: "其他项目"})
	requireAppTestNoError(t, err)
	requireAppTestNoError(t, svc.sessions.SetWorkspaceSession(ctx, other, "other"))
	_, err = svc.sessions.Ensure(ctx, session.Session{ID: "empty", Workspace: harness.workspace, Title: "空会话"})
	requireAppTestNoError(t, err)
	requireAppTestNoError(t, svc.sessions.SetWorkspaceSession(ctx, harness.workspace, "empty"))
	for _, id := range []string{"target", "other", "missing", "empty"} {
		if _, _, err := svc.resolveSessionReferences(ctx, "target", fmt.Sprintf("@[x](azem-session:%s)", id)); err == nil {
			t.Errorf("accepted invalid source %s", id)
		}
	}
	control := svc.TurnControl("reference-guide")
	svc.mu.Lock()
	svc.activeRun, svc.activeSession, svc.guidanceOpen = "reference-guide", "target", true
	svc.mu.Unlock()
	requireAppTestNoError(t, svc.GuideActiveTurn("target", "reference-guide", "参考 @[设计](azem-session:source)"))
	now := time.Now().UTC()
	queue, err := svc.sessions.SavePromptQueueCAS(ctx, "target", 0, session.PromptQueueV1{
		State: session.PromptQueuePaused,
		Items: []session.QueuedPromptV1{{ID: "queued-ref", Text: "继续 @[设计](azem-session:source)", State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}},
	})
	requireAppTestNoError(t, err)
	saved, err := svc.MutatePromptQueue(ctx, PromptQueueMutation{Operation: PromptQueueGuide, SessionID: "target", RunID: "reference-guide", ItemID: "queued-ref", MutationID: "guide-reference", ExpectedRevision: queue.Revision})
	requireAppTestNoError(t, err)
	if len(saved.Items) != 0 {
		t.Fatal("guided reference remained queued")
	}
	controls, err := control.Drain(ctx, turnControlBeforeModel)
	requireAppTestNoError(t, err)
	if len(controls) != 2 {
		t.Fatalf("guided messages = %d", len(controls))
	}
	for _, item := range controls {
		if !strings.Contains(item.Message.Text, "EXISTING_SUMMARY") || strings.Contains(item.Message.Text, "azem-session:") {
			t.Fatal("guidance did not resolve referenced history")
		}
	}
	svc.mu.Lock()
	svc.activeRun, svc.activeSession, svc.guidanceOpen = "", "", false
	svc.mu.Unlock()
	requireAppTestNoError(t, svc.sessions.SetArchived(ctx, "source", true))
	if _, _, err := svc.resolveSessionReferences(ctx, "target", "@[x](azem-session:source)"); err == nil {
		t.Fatal("accepted an archived conversation")
	}
}
