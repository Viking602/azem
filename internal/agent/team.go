package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/multiagent"
	"github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/worker"
)

type TeamModels struct {
	Planner     string
	Implementer string
	Reviewer    string
	Reporter    string
}

type TeamExecution struct {
	RunID  string
	Result multiagent.DriveResult
}

type TeamEngineDecorator func(hyagent.Engine, multiagent.Dispatch, multiagent.AgentClass) hyagent.Engine
type TeamHooks struct {
	BeforeTask     func(context.Context, multiagent.Dispatch, multiagent.AgentClass) error
	PrepareEngine  func(context.Context, hyagent.Engine, multiagent.Dispatch, multiagent.AgentClass) (hyagent.Engine, error)
	DecorateEngine TeamEngineDecorator
}

func (s *Service) StartTeam(ctx context.Context, prompt string, models TeamModels, providers provider.Resolver) (TeamExecution, error) {
	runID, err := newID("team")
	if err != nil {
		return TeamExecution{}, err
	}
	return s.StartTeamWithID(ctx, runID, prompt, models, providers, nil)
}

// StartTeamWithID starts a replayable coding team using the caller's run ID.
// afterTick observes durable checkpoints and must not mutate the supplied state.
func (s *Service) StartTeamWithID(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	var tools *tool.Bus
	if s != nil {
		tools = s.tools
	}
	return s.StartTeamWithIDAndTools(ctx, runID, prompt, models, providers, tools, afterTick)
}

// StartTeamWithIDAndTools starts a coding team with the supplied governed tool bus.
func (s *Service) StartTeamWithIDAndTools(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	return s.StartTeamWithIDAndToolsMetadata(ctx, runID, prompt, models, providers, tools, nil, afterTick)
}

// StartTeamWithIDAndToolsMetadata persists the product routing metadata needed
// to rebuild the provider and governed tools after a process restart.
func (s *Service) StartTeamWithIDAndToolsMetadata(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, metadata map[string]string, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	return s.StartTeamWithIDAndToolsMetadataHooks(ctx, runID, prompt, models, providers, tools, metadata, TeamHooks{}, afterTick)
}

func (s *Service) StartTeamWithIDAndToolsMetadataHooks(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, metadata map[string]string, hooks TeamHooks, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	if s == nil || s.runner == nil {
		return TeamExecution{}, fmt.Errorf("coding team: service is not initialized")
	}
	if providers == nil {
		return TeamExecution{}, fmt.Errorf("coding team: provider resolver is nil")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return TeamExecution{}, fmt.Errorf("coding team: prompt is empty")
	}
	if strings.TrimSpace(runID) == "" {
		return TeamExecution{}, fmt.Errorf("coding team: run ID is empty")
	}
	rootID, err := newID("root")
	if err != nil {
		return TeamExecution{}, err
	}
	run, _, err := s.runner.StartRun(ctx, api.StartRunCommand{RunID: runID, RootTaskID: rootID, Request: prompt, Metadata: metadata})
	if err != nil {
		return TeamExecution{}, err
	}
	teamRunner, err := s.codingTeamRunner(prompt, models, providers, tools, hooks, afterTick)
	if err != nil {
		return TeamExecution{}, err
	}
	result, err := teamRunner.Start(ctx, run.ID)
	return TeamExecution{RunID: run.ID, Result: result}, err
}

func (s *Service) ResumeTeam(ctx context.Context, runID string, models TeamModels, providers provider.Resolver) (TeamExecution, error) {
	var tools *tool.Bus
	if s != nil {
		tools = s.tools
	}
	return s.ResumeTeamWithTools(ctx, runID, models, providers, tools, nil)
}

// ResumeTeamWithTools resumes a durable team checkpoint using a newly bound
// provider resolver and the current process's governed tool drivers.
func (s *Service) ResumeTeamWithTools(ctx context.Context, runID string, models TeamModels, providers provider.Resolver, tools *tool.Bus, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	return s.ResumeTeamWithToolsHooks(ctx, runID, models, providers, tools, TeamHooks{}, afterTick)
}

func (s *Service) ResumeTeamWithToolsHooks(ctx context.Context, runID string, models TeamModels, providers provider.Resolver, tools *tool.Bus, hooks TeamHooks, afterTick func(multiagent.TeamState)) (TeamExecution, error) {
	if s == nil || s.runner == nil {
		return TeamExecution{}, fmt.Errorf("coding team: service is not initialized")
	}
	run, err := s.runner.Run(ctx, runID)
	if err != nil {
		return TeamExecution{}, err
	}
	teamRunner, err := s.codingTeamRunner(run.Request, models, providers, tools, hooks, afterTick)
	if err != nil {
		return TeamExecution{}, err
	}
	result, err := teamRunner.Resume(ctx, runID)
	return TeamExecution{RunID: runID, Result: result}, err
}

func (s *Service) codingTeamRunner(prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, hooks TeamHooks, afterTick func(multiagent.TeamState)) (worker.TeamRunner, error) {
	classes, err := CodingTeamClasses(models)
	if err != nil {
		return worker.TeamRunner{}, err
	}
	skillSnapshot := s.SkillSnapshot()
	if tools == nil {
		tools = tool.NewBus()
	}
	available := make(map[string]bool)
	for _, definition := range tools.Definitions() {
		available[definition.Name] = true
	}
	for index := range classes {
		filtered := classes[index].Tools[:0]
		for _, name := range classes[index].Tools {
			if available[name] {
				filtered = append(filtered, name)
			}
		}
		classes[index].Tools = filtered
		classes[index].Skills = append([]string(nil), skillSnapshot.Eager...)
		classes[index].AvailableSkills = append([]string(nil), skillSnapshot.Available...)
	}
	byName := make(map[string]multiagent.AgentClass, len(classes))
	team := multiagent.NewTeam("coding-team")
	for _, class := range classes {
		byName[class.Name] = class
		team.AddRole(class)
	}
	team.WithScheduler(CodingScheduler{Prompt: prompt, Classes: byName})
	return worker.TeamRunner{
		Runner:     s.runner,
		Team:       *team,
		BuildDeps:  hyagent.BuildDeps{Providers: providers, Tools: tools, Skills: skillSnapshot.Registry},
		BeforeTask: hooks.BeforeTask, PrepareEngine: hooks.PrepareEngine, DecorateEngine: hooks.DecorateEngine,
		Options: multiagent.DriveOptions{MaxConcurrency: s.teamMaxConcurrency, MaxTicks: s.teamMaxTicks, AfterTick: func(_ context.Context, state multiagent.TeamState) error {
			if afterTick != nil {
				afterTick(state)
			}
			return nil
		}},
		TTL: 10 * time.Minute,
	}, nil
}

func CodingTeamClasses(models TeamModels) ([]multiagent.AgentClass, error) {
	if models.Planner == "" {
		models.Planner = models.Implementer
	}
	if models.Reviewer == "" {
		models.Reviewer = models.Implementer
	}
	if models.Reporter == "" {
		models.Reporter = models.Implementer
	}
	if models.Implementer == "" || models.Planner == "" || models.Reviewer == "" || models.Reporter == "" {
		return nil, fmt.Errorf("coding team: every role needs a model")
	}

	inputSchema := json.RawMessage(`{"type":"object","required":["request"],"properties":{"request":{"type":"string"},"previous":{"type":"object"},"revision":{"type":"integer"},"revision_limit_reached":{"type":"boolean"}},"additionalProperties":false}`)
	plannerOutput := json.RawMessage(`{"type":"object","required":["plan","risks","acceptance_criteria"],"properties":{"plan":{"type":"array","items":{"type":"string"}},"risks":{"type":"array","items":{"type":"string"}},"acceptance_criteria":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)
	implementerOutput := json.RawMessage(`{"type":"object","required":["summary","evidence"],"properties":{"summary":{"type":"string"},"evidence":{"type":"array","items":{"type":"string"}},"files_changed":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)
	reviewerOutput := json.RawMessage(`{"type":"object","required":["verdict","findings","evidence"],"properties":{"verdict":{"type":"string","enum":["accept","revise"]},"findings":{"type":"array","items":{"type":"string"}},"evidence":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)
	reporterOutput := json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"},"findings":{"type":"array","items":{"type":"string"}},"verification":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)
	loop := hyagent.LoopPolicy{MaxIterations: 16, MaxWallClock: 10 * time.Minute}
	return []multiagent.AgentClass{
		{
			Name: PlannerClass, Model: models.Planner,
			Description:  "Plan a coding task without modifying the workspace.",
			Instructions: `Analyze the coding request. Inspect the workspace with read-only tools when needed. For a multi-step request, call todo view, then initialize or update the durable phased todo list before returning the plan. Do not edit files. Return exactly one JSON object with only these fields: {"plan":["step"],"risks":["risk"],"acceptance_criteria":["criterion"]}. Every field is required and every value is an array of strings.`,
			Tools:        []string{coding.ToolListFiles, coding.ToolReadFile, coding.ToolSearch, coding.ToolGitDiff, "todo"},
			InputSchema:  inputSchema, OutputSchema: plannerOutput, LoopPolicy: loop,
		},
		{
			Name: ImplementerClass, Model: models.Implementer,
			Description:  "Implement and verify the approved coding plan.",
			Instructions: `Implement the request using the supplied plan or review feedback. Use governed tools; never bypass approvals. Create files only with coding.write_file and modify existing files only with coding.edit_hashline. Keep the durable todo list current as work is started and completed. Verify the changed behavior. Return exactly one JSON object with only these fields: {"summary":"result","evidence":["evidence"],"files_changed":["path"]}. summary and evidence are required; files_changed is optional. Do not add status or other fields.`,
			Tools:        []string{coding.ToolListFiles, coding.ToolReadFile, coding.ToolSearch, coding.ToolGitDiff, coding.ToolEditHashline, coding.ToolWriteFile, coding.ToolGofmt, coding.ToolGoTest, ToolShell, "todo"},
			InputSchema:  inputSchema, OutputSchema: implementerOutput, LoopPolicy: loop,
		},
		{
			Name: ReviewerClass, Model: models.Reviewer,
			Description:  "Review the implementation and run governed verification.",
			Instructions: `Review the implementation against the request and acceptance criteria. Read the workspace and run governed verification when needed. Use verdict accept only when evidence supports completion; otherwise use revise. Return exactly one JSON object with only these fields: {"verdict":"accept","findings":["finding"],"evidence":["evidence"]}. Every field is required; verdict must be accept or revise. Do not add status or other fields.`,
			Tools:        []string{coding.ToolListFiles, coding.ToolReadFile, coding.ToolSearch, coding.ToolGitDiff, coding.ToolGoTest, ToolShell},
			InputSchema:  inputSchema, OutputSchema: reviewerOutput, LoopPolicy: loop,
		},
		{
			Name: ReporterClass, Model: models.Reporter,
			Description:  "Summarize the team's verified result for the user.",
			Instructions: `Produce the final user-facing result from the review. If the revision limit was reached, state the unresolved findings plainly. Do not modify the workspace. Return exactly one JSON object with only these fields: {"answer":"answer","findings":["finding"],"verification":["check"]}. answer is required; findings and verification are optional. Do not add status or other fields.`,
			InputSchema:  inputSchema, OutputSchema: reporterOutput, LoopPolicy: loop,
		},
	}, nil
}
