package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const fusionInstructions = `[Host: Fusion mode]
You are the lead: understand the request, plan the work, and review the evidence.
Use sidekick for implementation, commands, and verification. Give it concrete tasks,
constraints, relevant context and acceptance checks. It starts without your history,
but retains its own complete conversation across handoffs in this session.
Each sidekick call waits for completion. Inspect its results and relevant files,
then call it again for fixes or further work. Do not claim success without evidence.
The configured Sidekick cannot delegate. You own the final answer to the user.`

func legacyFusionCheckpoint(messages []message.Message) bool {
	for _, current := range messages {
		if current.Role == message.RoleSystem && isPrivateMessage(current) &&
			strings.HasPrefix(current.Text, "[Trusted private hook context]\n") && strings.HasSuffix(current.Text, fusionInstructions) {
			return true
		}
	}
	return false
}

func (s *Service) validateFusion(request TurnRequest) error {
	if request.PlanMode || request.Prewalk != nil || request.PlanYolo != nil || request.VibeMode {
		return fmt.Errorf("Fusion cannot be combined with Plan, Prewalk, or Vibe")
	}
	s.mu.Lock()
	route := s.cfg.Agents.Fusion
	enabled := s.cfg.Agents.Subagents.Enabled && s.cfg.Agents.Subagents.MaxDepth != 0
	s.mu.Unlock()
	if !enabled || request.DisableSubagents {
		return fmt.Errorf("Fusion requires subagents to be enabled")
	}
	if route.Provider == "" || route.Model == "" {
		return fmt.Errorf("configure Fusion Sidekick in Settings → Model routes first")
	}
	return nil
}

func (s *Service) setSessionMode(ctx context.Context, sessionID, mode string) error {
	if mode != "single" && mode != "fusion" {
		return fmt.Errorf("session mode must be single or fusion")
	}
	if mode == "fusion" {
		if err := s.validateFusion(TurnRequest{}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	id, running := firstNonempty(sessionID, s.currentSession), s.activeRun != ""
	s.mu.Unlock()
	if running {
		return ErrRunActive
	}
	if s.sessions == nil || id == "" {
		return fmt.Errorf("select a session first")
	}
	current, err := s.sessions.LoadSession(ctx, id)
	if err != nil {
		return err
	}
	if err := s.sessions.UpdatePreferences(ctx, id, current.ProviderID, current.ModelID, current.Reasoning, mode); err != nil {
		return err
	}
	return s.emitSessionProjectionState(ctx, id)
}

type fusionDriver struct {
	runtime *subagentRuntime
	parent  subagentParentRuntime
}

func newFusionDriver(ctx context.Context, runtime *subagentRuntime, parent subagentParentRuntime, route config.ModelRouteConfig) (*fusionDriver, error) {
	if runtime == nil || !runtime.enabledForDepth(parent.Depth) {
		return nil, fmt.Errorf("Fusion requires an enabled subagent depth")
	}
	if route.Provider == "" || route.Model == "" || parent.ResolveAccountDriver == nil {
		return nil, fmt.Errorf("Fusion requires a configured Sidekick route and account resolver")
	}
	account, model, _, driver, err := parent.ResolveAccountDriver(ctx, route.Provider, route.Model, route.Reasoning, "")
	if err != nil {
		return nil, fmt.Errorf("resolve Fusion Sidekick: %w", err)
	}
	parent.ProviderID, parent.AccountID, parent.ModelID, parent.Reasoning, parent.Driver = route.Provider, account, model, route.Reasoning, driver
	parent.DirectorReadOnly, parent.DelegationDepthLimit = true, 1
	identity, _ := json.Marshal([]string{parent.SessionID, parent.WorkspaceRoot, route.Provider, account, model, route.Reasoning})
	digest := sha256.Sum256(identity)
	parent.PersistentKey = fmt.Sprintf("fusion-sidekick-%x", digest[:16])
	if parent.Host != nil {
		parent.Host = fusionHost{providerHost: parent.Host, parentRunID: parent.ParentRunID, model: model}
	}
	return &fusionDriver{runtime: runtime, parent: parent}, nil
}

func (*fusionDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: "sidekick", Description: "Hand work to the configured Fusion Sidekick and wait for its result. It retains its own complete history across calls; provide context on the first handoff. Include implementation constraints and verification requirements. Only one Sidekick runs at a time.",
		Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "fusion-sidekick",
		InputSchema: tool.Schema{Type: "object", Required: []string{"prompt"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"prompt": {Type: "string"}}}}
}

func (*fusionDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("fusion", "subagent")
	policy.Concurrency, policy.ConcurrencyGroup = tool.ConcurrencyExclusive, "fusion-sidekick"
	return policy
}

func (d *fusionDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return subagentToolError(call, err), nil
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" || len([]rune(input.Prompt)) > 100_000 {
		return subagentToolError(call, fmt.Errorf("prompt is required and must not exceed 100000 characters")), nil
	}
	if err := ctx.Err(); err != nil {
		return subagentToolError(call, err), nil
	}
	var latest agentservice.SubagentRun
	d.runtime.mu.Lock()
	for _, parked := range d.runtime.parked {
		if parked.name == d.parent.PersistentKey && parked.run.SessionID == d.parent.SessionID {
			latest = parked.run
			break
		}
	}
	d.runtime.mu.Unlock()
	run, err := d.runtime.spawn(subagentSpawnInput{Name: d.parent.PersistentKey, Prompt: input.Prompt,
		SubagentType: "worker", SubagentTypeSet: true, CapabilityMode: "all", CapabilityModeSet: true,
		Isolation: "none", IsolationSet: true, BackgroundSet: true, ResumeFrom: latest.ID, parentToolCallID: call.ID}, d.parent, nil)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	// Fusion handoffs stay foreground even when ordinary subagent waits detach.
	if !waitForSubagentDone(ctx, d.runtime.parentDone(run.ID)) {
		d.runtime.Cancel(d.parent.SessionID, run.ID)
		return subagentToolError(call, ctx.Err()), nil
	}
	if err := d.runtime.store.SetCompletionDelivered(context.WithoutCancel(ctx), run.ID, true); err != nil {
		return subagentToolError(call, err), nil
	}
	snapshot := d.runtime.snapshot(run.ID, d.parent.SessionID)
	result := subagentJSONResult(call, foregroundSubagentResult(snapshot))
	result.IsError = !snapshot.Found || snapshot.Run.State != agentservice.SubagentCompleted
	return result, nil
}

func fusionResumeSeed(ctx context.Context, encoded json.RawMessage, parent subagentParentRuntime) ([]message.Message, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("Sidekick transcript is empty")
	}
	history, err := agentruntime.UnmarshalMessages(encoded)
	if err != nil {
		return nil, err
	}
	manager := turnContext{}
	configureArchiveStorage(&manager, parent.Host, parent.SessionID, parent.ParentRunID)
	history, err = manager.expandArchiveMessages(ctx, history, make(map[string]struct{}))
	if err != nil {
		return nil, err
	}
	seed := make([]message.Message, 0, len(history))
	for _, item := range history {
		if item.Role != message.RoleSystem || agentruntime.MessageVisibilityOf(item) == agentruntime.MessageVisibilityPrivate {
			seed = append(seed, item)
		}
	}
	if len(seed) == 0 {
		return nil, fmt.Errorf("Sidekick transcript has no conversation")
	}
	return seed, message.ValidateCompleteTurns(seed)
}
