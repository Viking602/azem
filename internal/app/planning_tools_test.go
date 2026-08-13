package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

func newPlanningToolHarness(t *testing.T) (*Service, *session.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "planning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "planning-session", Title: "Planning"}); err != nil {
		t.Fatal(err)
	}
	host := NewService(ctx, config.Default())
	host.AttachDurable(sessions, nil)
	t.Cleanup(func() { _ = host.Shutdown(context.Background()) })
	return host, sessions, ctx
}

func TestAskDriverPersistsAndResolvesInteractiveQuestion(t *testing.T) {
	host, sessions, ctx := newPlanningToolHarness(t)
	driver := &askDriver{sessionID: "planning-session", runID: "run-plan", host: host}
	arguments := json.RawMessage(`{"questions":[{"id":"scope","header":"Scope","question":"Which scope?","options":[{"label":"Focused","description":"Only the required path","recommended":true},{"label":"Broad","description":"Include adjacent cleanup"}]}]}`)
	result := make(chan tool.Result, 1)
	go func() {
		resolved, _ := driver.Execute(ctx, tool.Call{ID: "ask-call", Name: askToolName, Arguments: arguments}, nil)
		result <- resolved
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		host.mu.Lock()
		pending := host.liveUserInputs["ask-call"] != nil
		host.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask tool did not enter pending state")
		}
		time.Sleep(time.Millisecond)
	}
	payload := json.RawMessage(`{"answers":[{"question_id":"scope","selected":["Focused"]}]}`)
	if err := host.resolveUserInput(ctx, "other-session", "ask-call", payload); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-session answer error = %v", err)
	}
	if err := host.resolveUserInput(ctx, "planning-session", "ask-call", payload); err != nil {
		t.Fatal(err)
	}
	select {
	case resolved := <-result:
		if resolved.IsError || !strings.Contains(resolved.Content, "scope: Focused") {
			t.Fatalf("resolved ask result = %+v", resolved)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask tool did not resume")
	}
	projection, err := sessions.LoadProjection(ctx, "planning-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 1 || projection.Blocks[0].Kind != "question" || projection.Blocks[0].State != "answered" || projection.Blocks[0].Data["answers"] == "" {
		t.Fatalf("question projection = %+v", projection.Blocks)
	}
}

func TestSubmitPlanVersionsAndSupersedesPreviousProposal(t *testing.T) {
	host, sessions, ctx := newPlanningToolHarness(t)
	driver := &submitPlanDriver{sessionID: "planning-session", runID: "run-plan", host: host}
	first, err := driver.Execute(ctx, tool.Call{ID: "plan-1", Name: submitPlanToolName, Arguments: json.RawMessage(`{"title":"First","plan":"1. Inspect\n2. Implement"}`)}, nil)
	if err != nil || first.IsError {
		t.Fatalf("first plan = %+v, err = %v", first, err)
	}
	second, err := driver.Execute(ctx, tool.Call{ID: "plan-2", Name: submitPlanToolName, Arguments: json.RawMessage(`{"title":"Revised","plan":"1. Inspect\n2. Implement\n3. Verify"}`)}, nil)
	if err != nil || second.IsError {
		t.Fatalf("second plan = %+v, err = %v", second, err)
	}
	projection, err := sessions.LoadProjection(ctx, "planning-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 2 {
		t.Fatalf("plan blocks = %+v", projection.Blocks)
	}
	if projection.Blocks[0].State != "superseded" || projection.Blocks[1].State != "proposed" || projection.Blocks[1].Data["version"] != "2" {
		t.Fatalf("versioned plans = %+v", projection.Blocks)
	}
	planID := projection.Blocks[1].Data["planId"]
	artifact, err := sessions.LoadArtifact(ctx, "planning-session", planID)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "plan_v1" || !strings.Contains(string(artifact.Payload), "Revised") {
		t.Fatalf("plan artifact = %+v", artifact)
	}
}

func TestApprovedPlanContextIsTrustedPrivateExecutionInput(t *testing.T) {
	host, sessions, ctx := newPlanningToolHarness(t)
	payload, _ := json.Marshal(planArtifactV1{Version: 1, Title: "Ship it", Body: "Implement and verify the approved scope."})
	artifact, err := sessions.PutArtifact(ctx, "planning-session", "planning-run", "plan_v1", payload, "Ship it")
	if err != nil {
		t.Fatal(err)
	}
	contextText, err := host.approvedPlanContext(ctx, "planning-session", artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contextText, "Trusted") && !strings.Contains(contextText, "approved plan") {
		t.Fatalf("approved plan context = %q", contextText)
	}
	if !strings.Contains(contextText, "Implement and verify") {
		t.Fatalf("approved plan body missing: %q", contextText)
	}
	for _, required := range []string{"dependency-ready task frontier", "subagent.spawn", "parallel tool batch", "exclusive file or symbol ownership", "returned task or run ID", "failed, cancelled, and stalled", "never treat its pass result as approval", "independently inspect the relevant diff", "tiny linear task"} {
		if !strings.Contains(contextText, required) {
			t.Fatalf("approved plan execution context omits %q: %q", required, contextText)
		}
	}
}
