package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers missing")
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-team","title":"GPT Team","supports_tools":true}]}`))
		case "/responses":
			mu.Lock()
			index := calls
			calls++
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
			_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-%d\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n", index+1)
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

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "complete the task", Provider: "chatgpt", Model: "gpt-team", Reasoning: "minimal", AgentMode: "team"})
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
	if len(sessionProjection.Blocks) != 2 || sessionProjection.Blocks[1].Content != answer {
		t.Fatalf("session blocks=%+v", sessionProjection.Blocks)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
