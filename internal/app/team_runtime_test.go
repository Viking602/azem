package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestConfiguredTeamTurnRunsAllRolesAndPersistsReporterAnswer(t *testing.T) {
	responses := []string{
		`{"plan":["inspect","implement","verify"],"risks":[],"acceptance_criteria":["works"]}`,
		"",
		`{"summary":"implemented","evidence":["check passed"],"files_changed":["team.txt"]}`,
		`{"verdict":"accept","findings":[],"evidence":["verified"]}`,
		`{"answer":"Team completed the task.","findings":[],"verification":["verified"]}`,
	}
	var mu sync.Mutex
	calls := 0
	var requestBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers missing")
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-team","title":"GPT Team","context_window":128000,"supports_tools":true}]}`))
		case "/responses":
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Errorf("read provider request: %v", readErr)
			}
			mu.Lock()
			index := calls
			calls++
			requestBodies = append(requestBodies, body)
			mu.Unlock()
			if index >= len(responses) {
				t.Errorf("unexpected provider call %d", index+1)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			if index == 1 {
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-write\",\"call_id\":\"team-write-1\",\"name\":\"coding.write_file\",\"arguments\":\"{\\\"path\\\":\\\"team.txt\\\",\\\"content\\\":\\\"written by team\\\\n\\\"}\"}}\n\n")
			} else {
				_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", responses[index])
			}
			_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-%d\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":4,\"cache_write_tokens\":2},\"output_tokens\":5,\"output_tokens_details\":{\"reasoning_tokens\":3},\"total_tokens\":15}}}\n\n", index+1)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
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
	workspace := t.TempDir()
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
	if _, err := sessions.Ensure(ctx, session.Session{ID: "default", Title: "Test", ProviderID: "chatgpt", ModelID: "gpt-team", Reasoning: "minimal", AgentMode: "team"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachProviderRuntime(providerRuntime)
	service.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	pngFixture, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	image, err := service.ImportImageBytes("default", "reference.png", "image/png", pngFixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendBlock(ctx, "default", session.Block{Kind: "assistant", Title: "Prior", Content: "planner-only historical marker", State: "completed"}); err != nil {
		t.Fatal(err)
	}

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "complete the task", Provider: "chatgpt", Model: "gpt-team", Reasoning: "minimal", AgentMode: "team", Images: []session.Attachment{image}})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	var answer string
	approved := false
	deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
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
		case EventAgentState:
			states[event.Data["role"]] = event.State
		case EventTextDelta:
			answer += event.Text
		case EventApprovalRequested:
			if event.Data["tool"] != "coding.write_file" || event.Data["target"] != "team.txt" {
				t.Fatalf("team approval event=%+v", event)
			}
			if err := service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
			approved = true
		case EventRunFailed:
			t.Fatalf("team run failed: %s", event.Text)
		case EventRunFinished:
			goto finished
		}
	}

finished:
	if calls != 5 || answer != "Team completed the task." || !approved {
		t.Fatalf("team calls=%d answer=%q approved=%v", calls, answer, approved)
	}
	type capturedRequest struct {
		PromptCacheKey string            `json:"prompt_cache_key"`
		Instructions   string            `json:"instructions"`
		Input          []json.RawMessage `json:"input"`
	}
	captured := make([]capturedRequest, len(requestBodies))
	for index, body := range requestBodies {
		if err := json.Unmarshal(body, &captured[index]); err != nil {
			t.Fatalf("decode provider request %d: %v", index, err)
		}
		encodedInput, err := json.Marshal(captured[index].Input)
		if err != nil {
			t.Fatal(err)
		}
		hasImage := strings.Contains(string(encodedInput), "input_image")
		hasHistory := strings.Contains(string(encodedInput), "planner-only historical marker")
		if hasImage != (index == 0) || hasHistory != (index == 0) {
			t.Fatalf("request %d planner image=%v history=%v input=%s", index, hasImage, hasHistory, encodedInput)
		}
	}
	if strings.Count(string(requestBodies[0]), "Analyze the coding request") != 1 {
		t.Fatalf("planner worker instructions were duplicated: %s", requestBodies[0])
	}
	if captured[1].PromptCacheKey == "" || captured[1].PromptCacheKey != captured[2].PromptCacheKey || captured[0].PromptCacheKey == captured[1].PromptCacheKey {
		t.Fatalf("team role cache keys=%+v", captured)
	}
	if len(captured[1].Input) >= len(captured[2].Input) {
		t.Fatalf("implementer continuation did not grow: first=%d second=%d", len(captured[1].Input), len(captured[2].Input))
	}
	for index := range captured[1].Input {
		if string(captured[1].Input[index]) != string(captured[2].Input[index]) {
			t.Fatalf("implementer request lost exact prefix at input item %d", index)
		}
	}
	written, err := os.ReadFile(filepath.Join(workspace, "team.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "written by team\n" {
		t.Fatalf("team write=%q", written)
	}
	for _, role := range []string{"planner", "implementer", "reviewer", "reporter"} {
		if states[role] != "finished" {
			t.Fatalf("role states=%v", states)
		}
	}
	projection, err := coding.Recover(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	durableRun, err := coding.Runner().Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if durableRun.Metadata["session_id"] != "default" || durableRun.Metadata["provider"] != "chatgpt" || durableRun.Metadata["model"] != "gpt-team" {
		t.Fatalf("team routing metadata=%v", durableRun.Metadata)
	}
	if projection.Run.Status != "completed" {
		t.Fatalf("team run status=%q", projection.Run.Status)
	}
	var actionAttempts int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='action_attempt' AND run_id=?`, runID).Scan(&actionAttempts); err != nil {
		t.Fatal(err)
	}
	if actionAttempts != 1 {
		t.Fatalf("team action attempts=%d", actionAttempts)
	}
	sessionProjection, err := sessions.LoadProjection(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionProjection.Blocks) != 3 || sessionProjection.Blocks[2].Content != answer {
		t.Fatalf("session blocks=%+v", sessionProjection.Blocks)
	}
	if sessionProjection.Usage.TeamInput != 50 || sessionProjection.Usage.TeamCached != 20 || sessionProjection.Usage.TeamOutput != 25 || sessionProjection.Usage.TeamReasoning != 15 || sessionProjection.Usage.TeamCacheWrite != 10 {
		t.Fatalf("team usage=%+v", sessionProjection.Usage)
	}
	if sessionProjection.Usage.InputTokens != 0 || sessionProjection.Usage.OutputTokens != 0 || sessionProjection.Usage.ContextLimit != 0 {
		t.Fatalf("team usage replaced main occupancy or catalog limit: %+v", sessionProjection.Usage)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
