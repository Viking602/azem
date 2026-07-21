package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/multiagent"
	"github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestCodingSchedulerReplaysOneRevisionDeterministically(t *testing.T) {
	classes, err := CodingTeamClasses(TeamModels{Implementer: "model"})
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]multiagent.AgentClass, len(classes))
	for _, class := range classes {
		byName[class.Name] = class
	}
	scheduler := CodingScheduler{Prompt: "fix it", Classes: byName}
	state := multiagent.TeamState{RunID: "run-team"}

	planner := nextDispatch(t, scheduler, state, PlannerClass)
	if err := multiagent.ValidateDispatch(planner); err != nil {
		t.Fatalf("planner dispatch invalid: %v", err)
	}
	replayed := nextDispatch(t, scheduler, state, PlannerClass)
	if !reflect.DeepEqual(planner, replayed) {
		t.Fatalf("same state produced different dispatches:\nfirst=%#v\nsecond=%#v", planner, replayed)
	}
	state = finishDispatch(state, planner, map[string]any{
		"plan": []any{"edit"}, "risks": []any{}, "acceptance_criteria": []any{"passes"},
	})

	implementer := nextDispatch(t, scheduler, state, ImplementerClass)
	state = finishDispatch(state, implementer, map[string]any{"summary": "first", "evidence": []any{"test"}})
	reviewer := nextDispatch(t, scheduler, state, ReviewerClass)
	state = finishDispatch(state, reviewer, map[string]any{"verdict": "revise", "findings": []any{"bug"}, "evidence": []any{"failure"}})

	revision := nextDispatch(t, scheduler, state, ImplementerClass)
	if revision.Task.ID != "run-team-implementer-attempt-2" {
		t.Fatalf("revision task id = %q", revision.Task.ID)
	}
	state = finishDispatch(state, revision, map[string]any{"summary": "fixed", "evidence": []any{"pass"}})
	secondReview := nextDispatch(t, scheduler, state, ReviewerClass)
	if secondReview.Task.ID != "run-team-reviewer-attempt-2" {
		t.Fatalf("review retry task id = %q", secondReview.Task.ID)
	}
	state = finishDispatch(state, secondReview, map[string]any{"verdict": "accept", "findings": []any{}, "evidence": []any{"pass"}})

	reporter := nextDispatch(t, scheduler, state, ReporterClass)
	state = finishDispatch(state, reporter, map[string]any{"answer": "done"})
	dispatches, err := scheduler.Next(context.Background(), state)
	if err != nil || len(dispatches) != 0 {
		t.Fatalf("terminal dispatches=%#v error=%v", dispatches, err)
	}
}

func TestCodingSchedulerStopsRevisionLoopAtReporter(t *testing.T) {
	classes, err := CodingTeamClasses(TeamModels{Implementer: "model"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]multiagent.AgentClass{}
	for _, class := range classes {
		byName[class.Name] = class
	}
	scheduler := CodingScheduler{Prompt: "fix", Classes: byName}
	state := multiagent.TeamState{RunID: "run"}
	sequence := []struct {
		class  string
		report map[string]any
	}{
		{PlannerClass, map[string]any{"plan": []any{}, "risks": []any{}, "acceptance_criteria": []any{}}},
		{ImplementerClass, map[string]any{"summary": "one", "evidence": []any{}}},
		{ReviewerClass, map[string]any{"verdict": "revise", "findings": []any{}, "evidence": []any{}}},
		{ImplementerClass, map[string]any{"summary": "two", "evidence": []any{}}},
		{ReviewerClass, map[string]any{"verdict": "revise", "findings": []any{"still broken"}, "evidence": []any{}}},
	}
	for _, step := range sequence {
		state = finishDispatch(state, nextDispatch(t, scheduler, state, step.class), step.report)
	}
	reporter := nextDispatch(t, scheduler, state, ReporterClass)
	var input map[string]any
	if err := json.Unmarshal(reporter.Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["revision_limit_reached"] != true {
		t.Fatalf("reporter input = %#v", input)
	}
}

func TestCodingTeamRolePermissions(t *testing.T) {
	classes, err := CodingTeamClasses(TeamModels{Implementer: "model"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]multiagent.AgentClass{}
	for _, class := range classes {
		byName[class.Name] = class
	}
	if len(byName[ReporterClass].Tools) != 0 {
		t.Fatalf("reporter tools = %v", byName[ReporterClass].Tools)
	}
	for _, forbidden := range []string{"coding.edit_hashline", "coding.write_file", "coding.gofmt"} {
		if containsString(byName[PlannerClass].Tools, forbidden) || containsString(byName[ReviewerClass].Tools, forbidden) {
			t.Fatalf("read-only role received %q", forbidden)
		}
	}
	for _, class := range classes {
		if containsString(class.Tools, ToolShell) {
			t.Fatalf("%s role received disabled %s", class.Name, ToolShell)
		}
	}
	if !containsString(byName[ImplementerClass].Tools, "coding.edit_hashline") || !containsString(byName[ImplementerClass].Tools, "coding.write_file") || !containsString(byName[ImplementerClass].Tools, "coding.go_test") {
		t.Fatalf("implementer tools = %v", byName[ImplementerClass].Tools)
	}
	if !containsString(byName[ReviewerClass].Tools, "coding.go_test") {
		t.Fatalf("reviewer tools = %v", byName[ReviewerClass].Tools)
	}
}

func TestTeamRunnerPersistsAndResumesCodingTeam(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(ctx) }()

	driver := roleDriver{}
	models := TeamModels{Planner: "planner-model", Implementer: "implementer-model", Reviewer: "reviewer-model", Reporter: "reporter-model"}
	execution, err := service.StartTeam(ctx, "change safely", models, provider.Single(driver))
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Ticks != 4 || len(execution.Result.State.Instances) != 4 {
		t.Fatalf("team result = %#v", execution.Result)
	}
	for _, instance := range execution.Result.State.Instances {
		if instance.State != multiagent.InstanceStateFinished {
			t.Fatalf("instance = %#v", instance)
		}
	}

	resumed, err := service.ResumeTeam(ctx, execution.RunID, models, provider.Single(driver))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Result.Ticks != execution.Result.Ticks || len(resumed.Result.State.Instances) != 4 {
		t.Fatalf("resumed result = %#v", resumed.Result)
	}
	uow, err := service.runner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoffs, err := uow.Handoffs().ListHandoffs(ctx, api.HandoffSelector{RunID: execution.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(handoffs) != 3 {
		t.Fatalf("handoffs = %#v", handoffs)
	}
}

func TestCodingTeamReceivesSkillCatalog(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "team-skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillCatalog, err := skills.Load(skills.LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{skillRoot},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir(), WithSkills(skillCatalog))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(ctx) }()

	driver := &catalogRoleDriver{}
	models := TeamModels{Planner: "planner-model", Implementer: "implementer-model", Reviewer: "reviewer-model", Reporter: "reporter-model"}
	if _, err := service.StartTeam(ctx, "inspect safely", models, provider.Single(driver)); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	requests := append([]provider.Request(nil), driver.requests...)
	driver.mu.Unlock()
	var catalogVisible bool
	for _, request := range requests {
		var text strings.Builder
		for _, current := range request.Messages {
			text.WriteString(current.Text)
		}
		if strings.Contains(text.String(), "DEMO_BODY_SECRET") {
			t.Fatalf("team role %q eagerly received the skill body", request.Model)
		}
		if strings.Contains(text.String(), "demo catalog") && strings.Contains(text.String(), "demo") {
			catalogVisible = true
		}
	}
	if !catalogVisible {
		t.Fatalf("no team role received the skill catalog across %d requests", len(requests))
	}
}

type catalogRoleDriver struct {
	mu       sync.Mutex
	requests []provider.Request
}

func (*catalogRoleDriver) Metadata() provider.Metadata {
	return roleDriver{}.Metadata()
}

func (d *catalogRoleDriver) Stream(ctx context.Context, request provider.Request) (provider.Stream, error) {
	d.mu.Lock()
	d.requests = append(d.requests, request)
	d.mu.Unlock()
	return roleDriver{}.Stream(ctx, request)
}

type roleDriver struct{}

func (roleDriver) Metadata() provider.Metadata {
	return provider.Metadata{Name: "roles", Models: []string{"planner-model", "implementer-model", "reviewer-model", "reporter-model"}}
}

func (roleDriver) Stream(_ context.Context, request provider.Request) (provider.Stream, error) {
	outputs := map[string]string{
		"planner-model":     `{"plan":["inspect","change","verify"],"risks":[],"acceptance_criteria":["passes"]}`,
		"implementer-model": `{"summary":"implemented","evidence":["tests pass"],"files_changed":[]}`,
		"reviewer-model":    `{"verdict":"accept","findings":[],"evidence":["tests pass"]}`,
		"reporter-model":    `{"answer":"implemented and verified","findings":[],"verification":["tests pass"]}`,
	}
	return provider.NewSliceStream([]provider.Event{
		{Kind: provider.EventTextDelta, Text: outputs[request.Model]},
		{Kind: provider.EventDone, StopReason: provider.StopReasonComplete},
	}), nil
}

func nextDispatch(t *testing.T, scheduler CodingScheduler, state multiagent.TeamState, className string) multiagent.Dispatch {
	t.Helper()
	dispatches, err := scheduler.Next(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatches) != 1 || dispatches[0].ClassName != className {
		t.Fatalf("dispatches = %#v, want %s", dispatches, className)
	}
	return dispatches[0]
}

func finishDispatch(state multiagent.TeamState, dispatch multiagent.Dispatch, structured map[string]any) multiagent.TeamState {
	report := api.TypedReport{Status: api.ReportStatusSuccess, Structured: structured}
	task := dispatch.Task
	task.Status = api.TaskStatusCompleted
	task.Result = &report
	state.Tasks = append(state.Tasks, task)
	state.Instances = append(state.Instances, multiagent.AgentInstance{
		ID: dispatch.To, ClassName: dispatch.ClassName, RunID: state.RunID,
		TaskID: dispatch.Task.ID, State: multiagent.InstanceStateFinished,
	})
	state.Tick++
	return state
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
