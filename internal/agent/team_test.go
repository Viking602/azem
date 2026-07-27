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
	if planner.Task.Budget == nil || planner.Task.Budget.MaxTokens != 0 || planner.Task.Budget.MaxToolCalls != 0 || planner.Task.Budget.MaxWallClock != 0 {
		t.Fatalf("planner task budget = %#v, want unbounded", planner.Task.Budget)
	}
	if !byName[PlannerClass].LoopPolicy.UnlimitedIterations || byName[PlannerClass].LoopPolicy.MaxWallClock != 0 {
		t.Fatalf("planner loop policy = %#v, want unbounded", byName[PlannerClass].LoopPolicy)
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
	if !containsString(byName[ImplementerClass].Tools, ToolShell) || !containsString(byName[ReviewerClass].Tools, ToolShell) {
		t.Fatalf("execution roles did not receive %s", ToolShell)
	}
	if !containsString(byName[ImplementerClass].Tools, "coding.edit_hashline") || !containsString(byName[ImplementerClass].Tools, "coding.write_file") || !containsString(byName[ImplementerClass].Tools, "coding.go_test") {
		t.Fatalf("implementer tools = %v", byName[ImplementerClass].Tools)
	}
	if !containsString(byName[ReviewerClass].Tools, "coding.go_test") {
		t.Fatalf("reviewer tools = %v", byName[ReviewerClass].Tools)
	}
}

func TestCodingTeamRolePromptContracts(t *testing.T) {
	classes, err := CodingTeamClasses(TeamModels{Implementer: "model"})
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]multiagent.AgentClass, len(classes))
	for _, class := range classes {
		byName[class.Name] = class
	}
	expectations := map[string]struct {
		description string
		required    []string
		properties  []string
		evidence    []string
		allows      bool
	}{
		PlannerClass: {
			"Plan a coding task with repository-backed acceptance criteria without modifying files.",
			[]string{"plan", "risks", "acceptance_criteria"},
			[]string{"plan", "risks", "acceptance_criteria"},
			[]string{"Treat `request` as immutable.", "repository evidence", "observable acceptance criteria"},
			false,
		},
		ImplementerClass: {
			"Implement one approved coding plan and verify the changed behavior.",
			[]string{"summary", "evidence"},
			[]string{"summary", "evidence", "files_changed"},
			[]string{"planner report or reviewer feedback", "only observed command or scenario results", "repository-relative paths"},
			true,
		},
		ReviewerClass: {
			"Review the implementation against the request and run read-only verification.",
			[]string{"verdict", "findings", "evidence"},
			[]string{"verdict", "findings", "evidence"},
			[]string{"original `request`", "immediately preceding implementer report", "do not receive the planner report or its acceptance criteria", "concrete workspace and verification evidence"},
			true,
		},
		ReporterClass: {
			"Report only the team's verified result and unresolved findings.",
			[]string{"answer"},
			[]string{"answer", "findings", "verification"},
			[]string{"only the original `request` and the latest review report", "`revision_limit_reached` is true", "Do not infer unreported changes"},
			false,
		},
	}
	scheduler := CodingScheduler{Prompt: "change safely", Classes: byName}
	for name, expected := range expectations {
		class := byName[name]
		if class.Description != expected.description {
			t.Errorf("%s description = %q, want %q", name, class.Description, expected.description)
		}
		for _, phrase := range append(expected.evidence,
			"Return raw JSON only",
			"Do not use Markdown fences.",
			"Do not add any JSON fields.",
		) {
			if !strings.Contains(class.Instructions, phrase) {
				t.Errorf("%s instructions missing %q:\n%s", name, phrase, class.Instructions)
			}
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(class.OutputSchema, &raw); err != nil {
			t.Fatalf("%s output schema: %v", name, err)
		}
		if string(raw["additionalProperties"]) != "false" {
			t.Errorf("%s additionalProperties = %s", name, raw["additionalProperties"])
		}
		var schema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(class.OutputSchema, &schema); err != nil {
			t.Fatalf("%s typed output schema: %v", name, err)
		}
		if !reflect.DeepEqual(schema.Required, expected.required) || len(schema.Properties) != len(expected.properties) {
			t.Errorf("%s schema required=%q properties=%v", name, schema.Required, schema.Properties)
		}
		for _, property := range expected.properties {
			if _, ok := schema.Properties[property]; !ok {
				t.Errorf("%s schema is missing property %q", name, property)
			}
		}
		if name == ReviewerClass {
			var verdict struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Properties["verdict"], &verdict); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(verdict.Enum, []string{"accept", "revise"}) {
				t.Errorf("reviewer verdict enum = %q", verdict.Enum)
			}
		}

		dispatches, err := scheduler.dispatch(multiagent.TeamState{RunID: "prompt-contract"}, name, nil, map[string]any{"request": "change safely"})
		if err != nil || len(dispatches) != 1 {
			t.Fatalf("%s dispatches=%#v error=%v", name, dispatches, err)
		}
		dispatch := dispatches[0]
		if dispatch.Task.Goal != class.Instructions ||
			!reflect.DeepEqual(dispatch.Task.OutputSchema, class.OutputSchema) ||
			!reflect.DeepEqual(dispatch.OutputPolicy.Schema, class.OutputSchema) ||
			!dispatch.OutputPolicy.Validate || dispatch.Task.AllowsAction != expected.allows {
			t.Errorf("%s dispatch contract = %#v", name, dispatch)
		}
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
