package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
)

type TeamModels struct {
	Planner     string
	Implementer string
	Reviewer    string
	Reporter    string
}

type TeamExecution struct {
	RunID  string
	Result agentruntime.TeamExecutionResult
}

type (
	TeamEngineDecorator func(hyagent.Engine, agentruntime.TeamDispatch, agentruntime.TeamAgentClass) hyagent.Engine
	TeamHooks           struct {
		BeforeTask     func(context.Context, agentruntime.TeamDispatch, agentruntime.TeamAgentClass) error
		PrepareEngine  func(context.Context, hyagent.Engine, agentruntime.TeamDispatch, agentruntime.TeamAgentClass) (hyagent.Engine, error)
		DecorateEngine TeamEngineDecorator
		RetryPolicy    agentruntime.RetryPolicy
		ResourceClaims []agentruntime.ResourceClaimSpec
	}
)

func CodingTeamClasses(models TeamModels) ([]agentruntime.TeamAgentClass, error) {
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
	loop := hyagent.LoopPolicy{UnlimitedIterations: true}
	return []agentruntime.TeamAgentClass{
		{
			Name: PlannerClass, Model: models.Planner,
			Description:  "Plan a coding task with repository-backed acceptance criteria without modifying files.",
			Instructions: strings.TrimSpace(plannerTeamInstructions),
			Tools:        []string{ToolListFiles, ToolGlob, ToolReadFile, ToolSearch, ToolGitDiff, "todo"},
			InputSchema:  inputSchema, OutputSchema: plannerOutput, LoopPolicy: loop,
		},
		{
			Name: ImplementerClass, Model: models.Implementer,
			Description:  "Implement one approved coding plan and verify the changed behavior.",
			Instructions: strings.TrimSpace(implementerTeamInstructions),
			Tools:        []string{ToolListFiles, ToolGlob, ToolReadFile, ToolSearch, ToolGitDiff, ToolEditHashline, ToolReplace, ToolWriteFile, ToolDeleteFile, ToolGofmt, ToolGoTest, ToolShell, "todo"},
			InputSchema:  inputSchema, OutputSchema: implementerOutput, LoopPolicy: loop,
		},
		{
			Name: ReviewerClass, Model: models.Reviewer,
			Description:  "Review the implementation against the request and run read-only verification.",
			Instructions: strings.TrimSpace(reviewerTeamInstructions),
			Tools:        []string{ToolListFiles, ToolGlob, ToolReadFile, ToolSearch, ToolGitDiff, ToolGoTest, ToolShell},
			InputSchema:  inputSchema, OutputSchema: reviewerOutput, LoopPolicy: loop,
		},
		{
			Name: ReporterClass, Model: models.Reporter,
			Description:  "Report only the team's verified result and unresolved findings.",
			Instructions: strings.TrimSpace(reporterTeamInstructions),
			InputSchema:  inputSchema, OutputSchema: reporterOutput, LoopPolicy: loop,
		},
	}, nil
}
