package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

func TestCheckpointControllerPersistsRewindsAndRestores(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "checkpoint", Title: "Checkpoint"}); err != nil {
		t.Fatal(err)
	}
	controller, err := newCheckpointController(ctx, sessions, "checkpoint", "run")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := controller.drivers()[0]
	rewind := controller.drivers()[1]
	created := executeCheckpoint(t, ctx, checkpoint, `{"goal":"inspect persistence paths"}`)
	if created.IsError || !strings.Contains(created.Content, "Finish exploration") {
		t.Fatalf("checkpoint create = %#v", created)
	}
	if duplicate := executeCheckpoint(t, ctx, checkpoint, `{"goal":"duplicate"}`); !duplicate.IsError || !strings.Contains(duplicate.Content, "already active") {
		t.Fatalf("duplicate checkpoint = %#v", duplicate)
	}
	base := []message.Message{
		message.NewText(message.RoleUser, "investigate"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "checkpoint", Name: checkpointToolName}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "checkpoint", Name: checkpointToolName, Content: created.Content, Structured: created.Structured}),
	}
	if _, err := controller.Apply(ctx, base, []tool.Result{created}); err != nil {
		t.Fatal(err)
	}
	guarded, err := controller.guardrail().Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "premature")})
	if err != nil || guarded.Action != hyagent.OutputGuardrailActionRetry {
		t.Fatalf("active checkpoint guard = %#v, %v", guarded, err)
	}
	report := "The durable source is internal/session/service.go; preserve artifact digest validation."
	rewound := executeCheckpoint(t, ctx, rewind, `{"report":"`+report+`"}`)
	if rewound.IsError {
		t.Fatalf("rewind = %#v", rewound)
	}
	exploration := append(cloneCheckpointMessages(base),
		message.NewText(message.RoleAssistant, "INTERMEDIATE_EXPLORATION"),
		message.NewToolResult(message.ToolResult{ToolCallID: "read", Name: "coding.read_file", Content: "SECRET_INTERMEDIATE_BYTES"}),
		message.NewToolResult(message.ToolResult{ToolCallID: "rewind", Name: rewindToolName, Content: rewound.Content, Structured: rewound.Structured}),
	)
	result, err := controller.Apply(ctx, exploration, []tool.Result{rewound})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "INTERMEDIATE_EXPLORATION") || strings.Contains(string(encoded), "SECRET_INTERMEDIATE_BYTES") || !strings.Contains(string(encoded), report) {
		t.Fatalf("rewound context = %s", encoded)
	}
	allowed, err := controller.guardrail().Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "final")})
	if err != nil || allowed.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("rewound checkpoint guard = %#v, %v", allowed, err)
	}

	restarted, err := newCheckpointController(ctx, session.NewService(provider.DB(), provider.Blobs()), "checkpoint", "run-two")
	if err != nil || restarted.active() || restarted.state.Report != report {
		t.Fatalf("restored checkpoint = %#v, %v", restarted, err)
	}
	if repeated := executeCheckpoint(t, ctx, restarted.drivers()[1], `{"report":"again"}`); !repeated.IsError || !strings.Contains(repeated.Content, "already completed") {
		t.Fatalf("repeated rewind = %#v", repeated)
	}
	if next := executeCheckpoint(t, ctx, restarted.drivers()[0], `{"goal":"new investigation"}`); next.IsError {
		t.Fatalf("new checkpoint after rewind = %#v", next)
	}
	history, err := sessions.SearchHistory(ctx, "checkpoint", "digest validation", 10, 1000, 4000)
	if err != nil || len(history) != 0 {
		t.Fatalf("private checkpoint entered history recall: %#v, %v", history, err)
	}
}

func TestProviderRuntimeCheckpointRewindRemovesExplorationContext(t *testing.T) {
	ctx := context.Background()
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			writeProviderToolCall(writer, "checkpoint-1", "checkpoint-call", checkpointToolName, `{"goal":"inspect the fixture"}`)
		case 2:
			if !strings.Contains(body, "Exploration checkpoint active") {
				t.Errorf("checkpoint notice missing: %s", body)
			}
			writeProviderToolCall(writer, "checkpoint-2", "read-call", "coding.read_file", `{"path":"checkpoint-fixture.txt"}`)
		case 3:
			if !strings.Contains(body, "SECRET_EXPLORATION_BYTES") {
				t.Errorf("exploration result missing before rewind: %s", body)
			}
			writeProviderToolCall(writer, "checkpoint-3", "rewind-call", rewindToolName, `{"report":"Fixture contains the expected marker; no code change is needed."}`)
		case 4:
			if !strings.Contains(body, "Fixture contains the expected marker") || strings.Contains(body, "SECRET_EXPLORATION_BYTES") {
				t.Errorf("rewound provider context is wrong: %s", body)
			}
			writeProviderText(writer, "checkpoint-4", "Checkpoint investigation complete.")
		default:
			t.Errorf("unexpected checkpoint provider call %d", call)
			writeProviderText(writer, "checkpoint-extra", "unexpected")
		}
	})
	if err := os.WriteFile(filepath.Join(harness.workspace, "checkpoint-fixture.txt"), []byte("SECRET_EXPLORATION_BYTES\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "checkpoint-e2e", Prompt: "Investigate with a context checkpoint", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 4 {
		t.Fatalf("checkpoint provider calls = %d, want 4", harness.calls.Load())
	}
	projection, err := harness.service.sessions.LoadProjection(ctx, "checkpoint-e2e")
	if err != nil {
		t.Fatal(err)
	}
	modelHistory, _ := json.Marshal(projection.ModelHistory.Messages)
	if strings.Contains(string(modelHistory), "SECRET_EXPLORATION_BYTES") || !strings.Contains(string(modelHistory), "Fixture contains the expected marker") {
		t.Fatalf("durable rewound model history = %s", modelHistory)
	}
	if len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "Checkpoint investigation complete." {
		t.Fatalf("checkpoint final projection = %#v", projection.Blocks)
	}
}

func executeCheckpoint(t *testing.T, ctx context.Context, driver tool.Driver, input string) tool.Result {
	t.Helper()
	result, err := driver.Execute(ctx, tool.Call{ID: driver.Definition().Name, Name: driver.Definition().Name, Arguments: json.RawMessage(input)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
