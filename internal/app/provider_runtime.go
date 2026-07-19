package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/multiagent"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/provider/xai"
	"github.com/Viking602/azem/internal/session"
)

type TurnRequest struct {
	SessionID         string
	Prompt            string
	Provider          string
	Model             string
	History           []session.Block
	Reasoning         string
	AgentMode         string
	DisableSubagents  bool
	ActiveSkills      []string
	Todo              session.TodoList
	privateContext    string
	historicalContext string
}

type turnContext struct {
	instructions        string
	privateContext      string
	historicalContext   string
	history             []session.Block
	reportContextTokens func(context.Context, int)
	compactHooks        func(context.Context, []message.Message, []message.Message, error) error
	todo                session.TodoList
	loadTodo            func(context.Context) (session.TodoList, error)
}

func (c turnContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	messages := make([]message.Message, 0, len(c.history)+2)
	if text := strings.TrimSpace(c.instructions); text != "" {
		messages = append(messages, message.NewText(message.RoleSystem, text))
	}
	if text := strings.TrimSpace(c.privateContext); text != "" {
		value := message.NewText(message.RoleSystem, "[Trusted private hook context]\n"+text)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	todo, err := c.currentTodo(ctx)
	if err != nil {
		return nil, err
	}
	if reminder := todoReminder(todo); reminder != "" {
		value := message.NewText(message.RoleSystem, reminder)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	historical := strings.TrimSpace(c.historicalContext)
	if historical != "" {
		policy := message.NewText(message.RoleSystem, historicalEvidencePolicy)
		policy.Visibility = message.VisibilityPrivate
		messages = append(messages, policy)
	}
	for _, block := range c.history {
		text := strings.TrimSpace(block.Content)
		if text == "" {
			continue
		}
		role := message.RoleAssistant
		if block.Kind == "user" {
			role = message.RoleUser
		}
		messages = append(messages, message.NewText(role, text))
	}
	if historical != "" {
		data := message.NewText(message.RoleUser, "<historical-evidence-json>\n"+historical+"\n</historical-evidence-json>")
		data.Visibility = message.VisibilityPrivate
		messages = append(messages, data)
	}
	if goal := strings.TrimSpace(task.Goal); goal != "" {
		messages = append(messages, message.NewText(message.RoleUser, goal))
	}
	return messages, nil
}

const todoReminderPrefix = "[Session Todo private reminder]"

func (c turnContext) currentTodo(ctx context.Context) (session.TodoList, error) {
	if c.loadTodo != nil {
		return c.loadTodo(ctx)
	}
	return c.todo.Clone(), nil
}

func todoReminder(todo session.TodoList) string {
	if strings.TrimSpace(todo.Goal) == "" && len(todo.Phases) == 0 {
		return ""
	}
	var open []string
	closed := 0
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			switch item.Status {
			case session.TodoPending, session.TodoInProgress:
				open = append(open, fmt.Sprintf("%s:%s:%s", item.ID, item.Status, item.Content))
			default:
				closed++
			}
		}
	}
	return fmt.Sprintf("%s goal=%q revision=%d open=[%s] closed=%d. Use the todo tool with expected_revision for updates.", todoReminderPrefix, todo.Goal, todo.Revision, strings.Join(open, "; "), closed)
}

func (c turnContext) refreshTodoReminder(ctx context.Context, history []message.Message) ([]message.Message, error) {
	todo, err := c.currentTodo(ctx)
	if err != nil {
		return nil, err
	}
	refreshed := make([]message.Message, 0, len(history)+1)
	insertAt := 0
	for _, current := range history {
		if current.Role == message.RoleSystem && strings.HasPrefix(current.Text, todoReminderPrefix) {
			continue
		}
		refreshed = append(refreshed, current)
		if current.Role == message.RoleSystem {
			insertAt = len(refreshed)
		}
	}
	if reminder := todoReminder(todo); reminder != "" {
		value := message.NewText(message.RoleSystem, reminder)
		value.Visibility = message.VisibilityPrivate
		refreshed = append(refreshed, message.Message{})
		copy(refreshed[insertAt+1:], refreshed[insertAt:])
		refreshed[insertAt] = value
	}
	return refreshed, nil
}

func (c turnContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	if c.compactHooks != nil {
		if err := c.compactHooks(ctx, history, nil, nil); err != nil {
			return history, err
		}
	}
	var err error
	history, err = c.refreshTodoReminder(ctx, history)
	if err != nil {
		return nil, err
	}
	const recentMessages = 16
	prefixEnd := 0
	for prefixEnd < len(history) && history[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	if len(history) <= recentMessages+prefixEnd {
		if c.compactHooks != nil {
			_ = c.compactHooks(ctx, history, history, nil)
		}
		return history, nil
	}
	start := len(history) - recentMessages
	if start < prefixEnd {
		start = prefixEnd
	}
	for start > prefixEnd && history[start].Role != message.RoleUser {
		start--
	}
	compacted := make([]message.Message, 0, len(history)-start+prefixEnd)
	compacted = append(compacted, history[:prefixEnd]...)
	compacted = append(compacted, history[start:]...)
	if c.compactHooks != nil {
		_ = c.compactHooks(ctx, history, compacted, nil)
	}
	return compacted, nil
}

func (c turnContext) CompactTo(ctx context.Context, history []message.Message, targetTokens int) (result []message.Message, resultErr error) {
	original := history
	if c.compactHooks != nil {
		if err := c.compactHooks(ctx, history, nil, nil); err != nil {
			return history, err
		}
		defer func() { _ = c.compactHooks(ctx, original, result, resultErr) }()
	}
	var err error
	history, err = c.refreshTodoReminder(ctx, history)
	if err != nil {
		return nil, err
	}
	report := func(prepared []message.Message) []message.Message {
		if c.reportContextTokens != nil {
			c.reportContextTokens(ctx, estimateContextTokens(prepared))
		}
		return prepared
	}
	if targetTokens <= 0 {
		return report(history), nil
	}
	if err := message.ValidateCompleteTurns(history); err != nil {
		return history, err
	}
	if estimateContextTokens(history) <= targetTokens {
		return report(history), nil
	}

	fitSuffix := func(prepared []message.Message) ([]message.Message, bool, error) {
		prefixEnd := 0
		for prefixEnd < len(prepared) && prepared[prefixEnd].Role == message.RoleSystem {
			prefixEnd++
		}
		latestUser := -1
		for index := len(prepared) - 1; index >= prefixEnd; index-- {
			if prepared[index].Role == message.RoleUser {
				latestUser = index
				break
			}
		}
		if latestUser < 0 {
			return nil, false, fmt.Errorf("compact context: no user turn can be preserved")
		}
		for preferred := prefixEnd; preferred <= latestUser; preferred++ {
			start, boundaryErr := message.CompleteTurnBoundary(prepared, preferred)
			if boundaryErr != nil {
				return nil, false, boundaryErr
			}
			if start > latestUser {
				break
			}
			compacted := make([]message.Message, 0, prefixEnd+1+len(prepared)-start)
			compacted = append(compacted, prepared[:prefixEnd]...)
			if omitted := start - prefixEnd; omitted > 0 {
				summary := message.NewText(message.RoleSystem, fmt.Sprintf("[Compacted context: %d earlier messages omitted]", omitted))
				summary.Kind = message.KindCompactionSummary
				summary.Visibility = message.VisibilityPrivate
				summary.CreatedAt = time.Time{}
				compacted = append(compacted, summary)
			}
			compacted = append(compacted, prepared[start:]...)
			if estimateContextTokens(compacted) <= targetTokens {
				return compacted, true, nil
			}
			if start > preferred {
				preferred = start
			}
		}
		return nil, false, nil
	}
	if compacted, fits, fitErr := fitSuffix(history); fitErr != nil {
		return history, fitErr
	} else if fits {
		return report(compacted), nil
	}

	toolResults := 0
	for _, current := range history {
		if current.ToolResult != nil {
			toolResults++
		}
	}
	if toolResults > 0 {
		maxResultBytes := contextTokenBytes(targetTokens) / toolResults
		for {
			prepared := append([]message.Message(nil), history...)
			for index := range prepared {
				if result := prepared[index].ToolResult; result != nil {
					prepared[index].ToolResult = truncateToolResult(result, maxResultBytes)
				}
			}
			if compacted, fits, fitErr := fitSuffix(prepared); fitErr != nil {
				return history, fitErr
			} else if fits {
				return report(compacted), nil
			}
			if maxResultBytes <= 1 {
				break
			}
			maxResultBytes /= 2
		}
	}
	return history, fmt.Errorf("compact context: required messages exceed %d-token target", targetTokens)
}

const estimatedBytesPerToken = 4

// estimateContextTokens follows the same bytes/4 heuristic as grok-build, but
// counts only fields that a provider can put on the wire. In particular, a
// tool result's Structured form is a fallback when Content is empty, not a
// second copy of the result sent to the model.
func estimateContextTokens(messages []message.Message) int {
	maxInt := int(^uint(0) >> 1)
	tokens, remainder := 0, 0
	addBytes := func(bytes int) {
		if bytes <= 0 || tokens == maxInt {
			return
		}
		whole, nextRemainder := bytes/estimatedBytesPerToken, bytes%estimatedBytesPerToken
		if whole > maxInt-tokens {
			tokens, remainder = maxInt, 0
			return
		}
		tokens += whole
		remainder += nextRemainder
		if remainder >= estimatedBytesPerToken {
			if tokens == maxInt {
				remainder = 0
				return
			}
			tokens++
			remainder -= estimatedBytesPerToken
		}
	}
	for _, current := range messages {
		addBytes(len(current.Text))
		addBytes(len(current.Thinking))
		addBytes(len(current.ThinkingSignature))
		addBytes(len(current.RedactedThinking))
		addBytes(len(current.ProviderState))
		for _, call := range current.ToolCalls {
			addBytes(len(call.ID))
			addBytes(len(call.Name))
			addBytes(len(call.Arguments))
		}
		if result := current.ToolResult; result != nil {
			addBytes(len(result.ToolCallID))
			addBytes(len(result.Name))
			if result.Content != "" {
				addBytes(len(result.Content))
			} else {
				addBytes(len(result.Structured))
			}
		}
	}
	if remainder > 0 && tokens < maxInt {
		tokens++
	}
	return tokens
}

func contextTokenBytes(tokens int) int {
	maxInt := int(^uint(0) >> 1)
	if tokens > maxInt/estimatedBytesPerToken {
		return maxInt
	}
	return tokens * estimatedBytesPerToken
}

func truncateToolResult(result *message.ToolResult, maxBytes int) *message.ToolResult {
	visible := result.Content
	if visible == "" {
		visible = string(result.Structured)
	}
	visible = strings.ToValidUTF8(visible, "�")
	if len(visible) <= maxBytes {
		return result
	}

	const marker = "\n[tool result truncated to fit model context]"
	keep := maxBytes - len(marker)
	if keep < 0 {
		keep = 0
	}
	if keep > len(visible) {
		keep = len(visible)
	}
	for keep > 0 && !utf8.ValidString(visible[:keep]) {
		keep--
	}
	truncated := visible[:keep] + marker
	if len(truncated) >= len(visible) {
		return result
	}
	cloned := *result
	cloned.Content = truncated
	cloned.Structured = nil
	return &cloned
}

type ProviderRuntime struct {
	cfg                  config.Config
	auth                 *auth.Service
	catalog              *catalog.Service
	coding               *agentservice.Service
	subagentWorktreeRoot string
	ChatGPTEndpoint      string
	GrokEndpoint         string

	mu              sync.RWMutex
	host            *Service
	mcp             *mcpruntime.Manager
	subagents       *subagentRuntime
	subagentInitErr error
}

type liveApproval struct {
	approvalID  string
	agentID     string
	agentType   string
	run         *agentservice.Run
	callID      string
	sessionID   string
	runID       string
	fingerprint string
	decision    chan agentservice.ApprovalMode
	resolving   bool
	resolved    bool
}

func NewProviderRuntime(cfg config.Config, authentication *auth.Service, modelCatalog *catalog.Service, codingService *agentservice.Service, subagentWorktreeRoot string) (*ProviderRuntime, error) {
	if authentication == nil || modelCatalog == nil || codingService == nil {
		return nil, fmt.Errorf("provider runtime dependencies are incomplete")
	}
	if strings.TrimSpace(subagentWorktreeRoot) == "" {
		return nil, fmt.Errorf("subagent worktree root is empty")
	}
	return &ProviderRuntime{
		cfg: cfg, auth: authentication, catalog: modelCatalog, coding: codingService,
		subagentWorktreeRoot: subagentWorktreeRoot,
	}, nil
}

func (r *ProviderRuntime) Attach(host *Service, manager *mcpruntime.Manager, subagentStore agentservice.SubagentRunStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host = host
	r.mcp = manager
	if r.subagents == nil && r.subagentInitErr == nil && host != nil && subagentStore != nil {
		r.subagents, r.subagentInitErr = newSubagentRuntime(host.ctx, r.cfg.Agents.Subagents, subagentStore, r.subagentWorktreeRoot)
	}
}

func (r *ProviderRuntime) Start(ctx context.Context, request TurnRequest) (*agentservice.Run, hyagent.Engine, error) {
	account, modelID, contextWindow, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	contextTarget, err := modelContextTokenTarget(modelID, contextWindow)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	run, err := r.coding.StartRun(ctx, request.Prompt)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	r.mu.RLock()
	host := r.host
	manager := r.mcp
	subagents := r.subagents
	subagentInitErr := r.subagentInitErr
	r.mu.RUnlock()
	if subagentInitErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, subagentInitErr.Error(), subagentInitErr)
		return nil, hyagent.Engine{}, subagentInitErr
	}

	workspaceDrivers, err := r.coding.WorkspaceDrivers(ctx, r.cfg.Workspace.Root)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	drivers := make([]tool.Driver, 0, len(workspaceDrivers)+9)
	toolNames := make([]string, 0, len(workspaceDrivers)+8)
	for _, workspaceDriver := range workspaceDrivers {
		definition := workspaceDriver.Definition()
		governed := &governedAgentTool{definition: definition, driver: workspaceDriver, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
		metadata := hooks.Metadata{SessionID: request.SessionID, RunID: run.RunID, AgentID: "main", AgentType: "main", CWD: r.cfg.Workspace.Root}
		drivers = append(drivers, wrapHookDriver(host, metadata, governed))
		toolNames = append(toolNames, definition.Name)
	}
	if host != nil && host.sessions != nil {
		drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), &todoDriver{sessionID: request.SessionID, store: host.sessions, emit: func(event Event) bool {
			return host.emitTodoUpdated(request.SessionID, *event.Todo)
		}}))
		toolNames = append(toolNames, "todo")
	}
	if manager != nil {
		for _, external := range manager.Snapshot() {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	if subagents != nil && !request.DisableSubagents {
		subagentDrivers, buildErr := subagents.Drivers(subagentParentRuntime{
			SessionID: request.SessionID, ParentRunID: run.RunID, ParentAgentID: run.HolderID,
			ProviderID: request.Provider, ModelID: modelID, Reasoning: request.Reasoning,
			WorkspaceRoot: r.cfg.Workspace.Root, Driver: driver, Coding: r.coding, Host: host,
		})
		if buildErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, buildErr.Error(), buildErr)
			return nil, hyagent.Engine{}, buildErr
		}
		for _, external := range subagentDrivers {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	skillSnapshot := r.coding.SkillSnapshot()
	activeSkills := mergeSkillNames(skillSnapshot.Eager, request.ActiveSkills)
	instructions := "You are Azem, a local coding agent. Inspect the workspace before changing it. Use the provided governed tools. Keep changes focused, preserve user work, and verify the requested behavior before reporting completion."
	spec := hyagent.Spec{
		Instructions:    instructions,
		Skills:          activeSkills,
		AvailableSkills: skillSnapshot.Available,
		Model:           modelID,
		Tools:           toolNames,
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
			MaxWallClock:        20 * time.Minute,
			ContextTokenTarget:  contextTarget,
		},
	}
	contextManager := turnContext{instructions: instructions, privateContext: request.privateContext, historicalContext: request.historicalContext, history: request.History, todo: request.Todo}
	if host != nil {
		contextManager.compactHooks = host.autoCompactHooks(host.hookMetadata(request.SessionID, run.RunID))
		if host.sessions != nil {
			contextManager.loadTodo = func(ctx context.Context) (session.TodoList, error) {
				return host.sessions.LoadTodo(ctx, request.SessionID)
			}
		}
		contextManager.reportContextTokens = func(_ context.Context, tokens int) {
			host.emit(host.ctx, Event{Kind: EventContextUsage, SessionID: request.SessionID, RunID: run.RunID, State: "estimated", Data: map[string]string{
				"inputTokens": fmt.Sprint(tokens), "outputTokens": "0", "totalTokens": fmt.Sprint(tokens), "cacheStatus": "pending",
			}})
		}
	}
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Providers:      hyprovider.Single(driver),
		Skills:         skillSnapshot.Registry,
		Tools:          tool.NewBus(drivers...),
		ContextManager: contextManager,
	})
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	if host != nil {
		metadata := host.hookMetadata(request.SessionID, run.RunID)
		engine.OutputGuardrails = append(engine.OutputGuardrails, host.stopHookGuardrail(metadata, hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
			messages := append(append([]message.Message(nil), input.Messages...), input.Output)
			return writeSessionHookTranscript(request.SessionID, messages)
		}))
	}
	_ = account
	return run, engine, nil
}

func modelContextTokenTarget(modelID string, contextWindow int) (int, error) {
	if contextWindow <= 0 {
		return 0, fmt.Errorf("model %q catalog omitted a positive context window", modelID)
	}
	target := contextWindow/4*3 + (contextWindow%4)*3/4
	if target <= 0 {
		return 0, fmt.Errorf("model %q context window is too small", modelID)
	}
	return target, nil
}

func mergeSkillNames(eager, requested []string) []string {
	if len(requested) == 0 {
		return eager
	}
	seen := make(map[string]struct{}, len(eager)+len(requested))
	merged := make([]string, 0, len(eager)+len(requested))
	for _, names := range [][]string{eager, requested} {
		for _, name := range names {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			merged = append(merged, name)
		}
	}
	return merged
}

func (r *ProviderRuntime) CancelSubagent(sessionID, id string) agentservice.SubagentCancelOutcome {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return agentservice.SubagentCancelOutcome{Outcome: "not_found"}
	}
	return runtime.Cancel(sessionID, id)
}

func (r *ProviderRuntime) HasActiveForegroundSubagents(sessionID, parentRunID string) bool {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	return runtime != nil && runtime.HasForegroundByParentRun(sessionID, parentRunID)
}

func (r *ProviderRuntime) CancelParentSubagents(sessionID, parentRunID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.CancelByParentRun(sessionID, parentRunID, true)
	}
}

func (r *ProviderRuntime) AutoWakePending(sessionID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.AutoWakePending(sessionID)
	}
}

func (r *ProviderRuntime) ListSubagents(ctx context.Context, sessionID string) []agentservice.SubagentSnapshot {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.List(ctx, sessionID)
}

func (r *ProviderRuntime) DetailSubagent(ctx context.Context, sessionID, id string) ([]AgentTranscriptBlock, error) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil, api.ErrNotFound
	}
	return runtime.Detail(ctx, sessionID, id)
}

func (r *ProviderRuntime) Shutdown(ctx context.Context) error {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.Shutdown(ctx)
}

func (r *ProviderRuntime) TeamResolver(ctx context.Context, request TurnRequest) (string, hyprovider.Resolver, error) {
	_, modelID, _, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return "", nil, err
	}
	return modelID, hyprovider.Single(driver), nil
}

func (r *ProviderRuntime) ApprovalReviewer(ctx context.Context) (*codex.Reviewer, error) {
	accounts, err := r.auth.Accounts(ctx, "chatgpt")
	if err != nil {
		return nil, err
	}
	var accountID string
	for _, account := range accounts {
		if account.Status == "active" {
			accountID = account.ID
			break
		}
	}
	if accountID == "" {
		return nil, fmt.Errorf("no active ChatGPT account is available")
	}
	driver, err := codex.New(r.auth, accountID, r.ChatGPTEndpoint, []string{codex.ApprovalReviewerModel}, "low")
	if err != nil {
		return nil, err
	}
	return codex.NewReviewer(driver)
}

// ResumeTeam rebuilds provider and tool bindings from durable run metadata,
// then resumes the TeamRunner checkpoint without blocking startup.
func (r *ProviderRuntime) ResumeTeam(_ context.Context, runID string) error {
	run, err := r.coding.Runner().Run(context.Background(), runID)
	if err != nil {
		return err
	}
	request := TurnRequest{
		SessionID:      firstNonempty(run.Metadata["session_id"], "default"),
		Prompt:         run.Request,
		Provider:       firstNonempty(run.Metadata["provider"], r.cfg.Defaults.Provider),
		Model:          firstNonempty(run.Metadata["model"], r.cfg.Defaults.Model),
		Reasoning:      firstNonempty(run.Metadata["reasoning"], r.cfg.Defaults.Reasoning),
		AgentMode:      "team",
		privateContext: run.Metadata["hook_private_context"],
	}
	modelID, resolver, err := r.TeamResolver(context.Background(), request)
	if err != nil {
		return err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host == nil {
		return fmt.Errorf("resume team %s: application runtime is unavailable", runID)
	}
	runCtx, cancel := context.WithCancel(host.ctx)
	host.mu.Lock()
	if host.activeRun != "" {
		host.mu.Unlock()
		cancel()
		return ErrRunActive
	}
	host.activeRun = runID
	host.activeEnd = cancel
	host.mu.Unlock()
	host.wg.Add(1)
	originalPrompt := firstNonempty(run.Metadata["original_prompt"], request.Prompt)
	historicalContext := host.loadHistoricalContext(host.ctx, request.SessionID, originalPrompt)
	go host.runResumedProviderTeam(runCtx, request.SessionID, runID, request.Prompt, originalPrompt, modelID, request.privateContext, historicalContext, resolver)
	return nil
}

func (r *ProviderRuntime) resolveDriver(ctx context.Context, providerID, modelID, requestedReasoning string) (auth.Account, string, int, hyprovider.Driver, error) {
	accounts, err := r.auth.Accounts(ctx, providerID)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	var account auth.Account
	for _, candidate := range accounts {
		if candidate.Status == "active" {
			account = candidate
			break
		}
	}
	if account.ID == "" {
		return auth.Account{}, "", 0, nil, fmt.Errorf("sign in to %s before starting a turn", providerID)
	}
	models, err := r.catalog.List(ctx, providerID, account.ID, false)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	if modelID == "" && len(models.Models) > 0 {
		modelID = models.Models[0].ID
	}
	var selectedModel catalog.Model
	for _, model := range models.Models {
		if model.ID == modelID {
			selectedModel = model
			break
		}
	}
	if selectedModel.ID == "" {
		return auth.Account{}, "", 0, nil, fmt.Errorf("model %q is not available for %s account %s", modelID, providerID, account.ID)
	}
	reasoningEffort, err := catalog.ResolveReasoningEffort(providerID, selectedModel, requestedReasoning)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	modelIDs := []string{modelID}
	switch providerID {
	case "chatgpt":
		driver, err := codex.New(r.auth, account.ID, r.ChatGPTEndpoint, modelIDs, reasoningEffort)
		return account, modelID, selectedModel.ContextWindow, driver, err
	case "grok":
		var transport xai.Transport
		switch r.cfg.Providers.Grok.Transport {
		case "", "api":
			transport = &xai.StandardTransport{Auth: r.auth, AccountID: account.ID, Endpoint: r.GrokEndpoint}
		case "cli_proxy":
			transport = &xai.CLIProxyTransport{Token: func(ctx context.Context) (string, error) {
				credential, err := r.auth.Credential(ctx, "grok", account.ID)
				return credential.AccessToken, err
			}}
		default:
			return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported Grok transport %q", r.cfg.Providers.Grok.Transport)
		}
		driver, err := xai.New(transport, modelIDs, reasoningEffort)
		return account, modelID, selectedModel.ContextWindow, driver, err
	default:
		return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported provider %q", providerID)
	}
}

type governedAgentTool struct {
	definition       tool.Definition
	driver           tool.Driver
	coding           *agentservice.Service
	run              *agentservice.Run
	host             *Service
	sessionID        string
	agentID          string
	agentType        string
	parentToolCallID string
	streamRunID      string
	update           func(tool.Update)
}

func (d *governedAgentTool) Definition() tool.Definition { return d.definition }

func (d *governedAgentTool) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	updates := func(update tool.Update) error {
		if d.update != nil {
			d.update(update)
		}
		if d.host != nil {
			runID := firstNonempty(d.streamRunID, d.run.RunID)
			d.host.emit(ctx, Event{
				Kind: EventToolUpdate, SessionID: d.sessionID, RunID: runID, AgentID: d.agentID,
				ToolCallID: call.ID, State: update.Kind, Text: update.Message,
				Data: childFrameData("child:"+d.agentID, d.parentToolCallID, update.Data),
			})
		}
		if sink != nil {
			return sink(update)
		}
		return nil
	}
	var execution agentservice.ExecutionResult
	var err error
	if hooks.PreToolPermissionFromContext(ctx) == "ask" && d.driver != nil {
		execution, err = d.coding.ExecuteDriver(ctx, d.run, approvalRequiredDriver{inner: d.driver}, call, updates)
	} else {
		execution, err = d.execute(ctx, call, updates)
	}
	if err != nil {
		return execution.Result, err
	}
	if execution.Approval == nil {
		return execution.Result, nil
	}
	if d.host == nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "approval UI is unavailable", IsError: true}, nil
	}
	resolution, err := d.host.awaitApproval(ctx, d.sessionID, d.agentID, d.agentType, d.run, call, *execution.Approval)
	if err != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
	}
	if resolution.Mode == agentservice.ApprovalDenied {
		if resolution.Prevent {
			return tool.Result{ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation"}, hooks.ErrPreventContinuation
		}
		message := firstNonempty(resolution.DenialMessage, "Denied by user")
		if resolution.Retry {
			message += " PermissionDenied hook permits retrying this tool request."
		}
		return tool.Result{
			ToolCallID: call.ID, Name: call.Name,
			Content: message, IsError: true,
		}, nil
	}
	execution, err = d.execute(ctx, call, updates)
	return execution.Result, err
}

func (d *governedAgentTool) execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (agentservice.ExecutionResult, error) {
	if d.driver != nil {
		return d.coding.ExecuteDriver(ctx, d.run, d.driver, call, sink)
	}
	return d.coding.ExecuteTool(ctx, d.run, call, sink)
}

type approvalReviewRequest struct {
	Goal            string
	AgentID         string
	AgentType       string
	ToolName        string
	Arguments       json.RawMessage
	Target          string
	Effect          string
	Risk            string
	RequestedAction string
	RequestedReason string
}

func (r approvalReviewRequest) codexRequest() (codex.ApprovalReviewRequest, error) {
	if len(r.Arguments) == 0 || !json.Valid(r.Arguments) {
		return codex.ApprovalReviewRequest{}, fmt.Errorf("tool arguments are not valid JSON")
	}
	return codex.ApprovalReviewRequest{
		Goal: r.Goal, AgentID: r.AgentID, AgentType: r.AgentType, ToolName: r.ToolName,
		Arguments: r.Arguments, Target: r.Target, Effect: r.Effect, Risk: r.Risk,
		RequestedAction: r.RequestedAction, RequestedReason: r.RequestedReason,
	}, nil
}

func runApprovalReviewRequest(run *agentservice.Run, agentID, agentType string, call tool.Call, pending agentservice.PendingApproval) approvalReviewRequest {
	return approvalReviewRequest{
		Goal: run.Goal, AgentID: agentID, AgentType: agentType, ToolName: call.Name,
		Arguments: call.Arguments, Target: pending.Scope.Target, Effect: pending.Effect, Risk: pending.Scope.Risk,
		RequestedAction: pending.Request.RequestedAction, RequestedReason: pending.Request.Reason,
	}
}

func teamApprovalReviewRequest(goal, runID string, call tool.Call, definition tool.Definition) approvalReviewRequest {
	target := teamToolTarget(call)
	return approvalReviewRequest{
		Goal: goal, AgentID: runID, AgentType: "team", ToolName: call.Name, Arguments: call.Arguments,
		Target: target, Effect: string(definition.EffectType),
		Risk:            firstNonempty(definition.RiskLevel, definition.Security.RiskLevel, "high"),
		RequestedAction: call.Name + " · " + target, RequestedReason: "team agent requested a governed tool action",
	}
}

type approvalResolution struct {
	Mode          agentservice.ApprovalMode
	DenialMessage string
	Retry         bool
	Prevent       bool
}

type permissionHookDecision struct {
	behavior  string
	message   string
	interrupt bool
	name      string
}

func (s *Service) permissionHook(ctx context.Context, metadata hooks.Metadata, call tool.Call) permissionHookDecision {
	s.mu.Lock()
	mode := claudePermissionMode(s.approvalMode)
	s.mu.Unlock()
	result := s.hooks.Dispatch(ctx, hooks.Envelope{
		SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID, AgentType: metadata.AgentType,
		ParentRunID: metadata.ParentRunID, ParentToolCallID: metadata.ParentToolCallID, CWD: metadata.CWD,
		HookEventName: hooks.PermissionRequest, ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name,
		ToolInput: call.Arguments, PermissionMode: mode,
	})
	if result.PreventContinuation {
		return permissionHookDecision{behavior: "deny", message: result.StopReason, interrupt: true, name: "continue:false"}
	}
	if result.Denied {
		name := "unknown"
		if len(result.Runs) > 0 {
			name = result.Runs[len(result.Runs)-1].Name
		}
		return permissionHookDecision{behavior: "deny", message: result.Reason, interrupt: true, name: name}
	}
	var allowed permissionHookDecision
	for _, run := range result.Runs {
		decision := run.Output.HookSpecificOutput.Decision
		behavior := strings.ToLower(strings.TrimSpace(decision.Behavior))
		if behavior == "" || behavior == "ask" {
			continue
		}
		if behavior == "allow" && len(decision.UpdatedInput) > 0 && string(decision.UpdatedInput) != string(call.Arguments) {
			return permissionHookDecision{behavior: "deny", message: "PermissionRequest updatedInput is not supported because the modified input has not passed approval policy validation", interrupt: true, name: run.Name}
		}
		if behavior == "allow" && decision.UpdatedPermissions != nil {
			return permissionHookDecision{behavior: "deny", message: "PermissionRequest updatedPermissions is not supported by this permission store", interrupt: true, name: run.Name}
		}
		if behavior == "deny" {
			return permissionHookDecision{behavior: behavior, message: decision.Message, interrupt: decision.Interrupt, name: run.Name}
		}
		if behavior == "allow" && allowed.behavior == "" {
			allowed = permissionHookDecision{behavior: behavior, message: decision.Message, interrupt: decision.Interrupt, name: run.Name}
		}
	}
	return allowed
}

func claudePermissionMode(mode ApprovalMode) string {
	if mode == ApprovalModeYolo {
		return "bypassPermissions"
	}
	return "default"
}

func (s *Service) observePermissionDenied(ctx context.Context, metadata hooks.Metadata, call tool.Call, reason string, interrupt bool) (bool, bool) {
	result := s.hooks.Dispatch(ctx, hooks.Envelope{SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
		AgentType: metadata.AgentType, CWD: metadata.CWD, HookEventName: hooks.PermissionDenied,
		ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name, ToolInput: call.Arguments, Reason: reason, IsInterrupt: interrupt})
	if result.PreventContinuation {
		return false, true
	}
	for _, run := range result.Runs {
		if run.Output.HookSpecificOutput.Retry {
			return true, false
		}
	}
	return false, false
}

type autoReviewDenialTracker struct {
	recent          [50]bool
	reviewCount     int
	next            int
	recentDenials   int
	consecutiveDeny int
}

type AutoReviewDenialLimitError struct {
	RunID              string
	ConsecutiveDenials int
	RecentDenials      int
}

func (e *AutoReviewDenialLimitError) Error() string {
	return fmt.Sprintf(
		"automatic approval review denied too many actions for run %s (%d consecutive, %d in the last 50 reviews)",
		e.RunID, e.ConsecutiveDenials, e.RecentDenials,
	)
}

type teamApprovalDriver struct {
	inner     tool.Driver
	host      *Service
	sessionID string
	runID     string
	goal      string
}

type approvalRequiredDriver struct{ inner tool.Driver }

func (d approvalRequiredDriver) Definition() tool.Definition {
	definition := d.inner.Definition()
	definition.RequiresApproval = true
	definition.Security.RequiresApproval = true
	return definition
}

func (d approvalRequiredDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	return d.inner.Execute(ctx, call, sink)
}

func (d *teamApprovalDriver) Definition() tool.Definition {
	definition := d.inner.Definition()
	if teamToolHasSideEffect(definition) {
		// The outer TeamRunner gate sees a read-only adapter; Execute records
		// the real side-effect attempt after Azem receives approval.
		definition.EffectType = tool.EffectReadOnly
		definition.RequiresApproval = false
		definition.RequiresActionTask = false
		definition.Security.RequiresApproval = false
	}
	return definition
}

func (d *teamApprovalDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	definition := d.inner.Definition()
	if hooks.PreToolPermissionFromContext(ctx) == "ask" || teamToolRequiresApproval(definition, call) {
		resolution, err := d.host.awaitTeamApproval(ctx, d.sessionID, d.runID, d.goal, call, definition)
		if err != nil {
			return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
		}
		if resolution.Mode == agentservice.ApprovalDenied {
			if resolution.Prevent {
				return tool.Result{ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation"}, hooks.ErrPreventContinuation
			}
			message := firstNonempty(resolution.DenialMessage, "Denied by user")
			if resolution.Retry {
				message += " PermissionDenied hook permits retrying this tool request."
			}
			return tool.Result{
				ToolCallID: call.ID, Name: call.Name,
				Content: message, IsError: true,
			}, nil
		}
	}
	if teamToolHasSideEffect(definition) {
		return d.host.coding.ExecuteTeamDriver(ctx, d.runID, d.inner, call, sink)
	}
	return d.inner.Execute(ctx, call, sink)
}

func (s *Service) teamToolBus(ctx context.Context, sessionID, runID, goal string) (*tool.Bus, error) {
	drivers, err := s.coding.WorkspaceDrivers(ctx, s.cfg.Workspace.Root)
	if err != nil {
		return nil, err
	}
	if s.sessions != nil {
		drivers = append(drivers, &todoDriver{sessionID: sessionID, store: s.sessions, emit: func(event Event) bool {
			return s.emitTodoUpdated(sessionID, *event.Todo)
		}})
	}
	governed := make([]tool.Driver, 0, len(drivers))
	for _, driver := range drivers {
		approval := &teamApprovalDriver{inner: driver, host: s, sessionID: sessionID, runID: runID, goal: goal}
		metadata := hooks.Metadata{SessionID: sessionID, RunID: runID, AgentID: "team", AgentType: "team", CWD: s.cfg.Workspace.Root}
		governed = append(governed, hooks.WrapDriver(s.hooks, metadata, approval))
	}
	return tool.NewBus(governed...), nil
}

func teamToolHasSideEffect(definition tool.Definition) bool {
	return definition.RequiresActionTask || definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
}

func teamToolRequiresApproval(definition tool.Definition, call tool.Call) bool {
	required := definition.RequiresApproval || definition.Security.RequiresApproval || definition.RequiresActionTask ||
		definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
	if definition.Metadata["approval"] == "allow" {
		required = definition.Metadata["network"] == "prompt" && toolArgumentsRequestNetwork(call.Arguments)
	}
	return required
}

func toolArgumentsRequestNetwork(arguments json.RawMessage) bool {
	var input struct {
		Network bool `json:"network"`
	}
	return json.Unmarshal(arguments, &input) == nil && input.Network
}

func teamToolTarget(call tool.Call) string {
	var input map[string]any
	if json.Unmarshal(call.Arguments, &input) == nil {
		for _, key := range []string{"path", "command", "query", "url"} {
			if value := strings.TrimSpace(fmt.Sprint(input[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return "workspace"
}

func teamToolFingerprint(definition tool.Definition, call tool.Call) string {
	return call.Name + "\x00" + string(definition.EffectType) + "\x00" + teamToolTarget(call)
}

func (s *Service) awaitTeamApproval(ctx context.Context, sessionID, runID, goal string, call tool.Call, definition tool.Definition) (approvalResolution, error) {
	if strings.TrimSpace(call.ID) == "" {
		return approvalResolution{}, fmt.Errorf("team tool %q requires a call ID for approval", call.Name)
	}
	approvalID, err := randomID("approval")
	if err != nil {
		return approvalResolution{}, err
	}
	request := teamApprovalReviewRequest(goal, runID, call, definition)
	fingerprint := teamToolFingerprint(definition, call)
	target := request.Target
	action := request.RequestedAction
	event := Event{
		Kind: EventApprovalRequested, SessionID: sessionID, RunID: runID, ToolCallID: call.ID,
		ApprovalID: approvalID, Text: action,
		Data: map[string]string{
			"tool": call.Name, "target": target, "risk": request.Risk, "effect": request.Effect,
			"action": action, "agent_type": "team",
		},
	}
	metadata := hooks.Metadata{SessionID: sessionID, RunID: runID, AgentID: "team", AgentType: "team", CWD: s.cfg.Workspace.Root}
	s.mu.Lock()
	mode := s.approvalMode
	_, granted := s.teamApprovals[fingerprint]
	s.mu.Unlock()
	if mode == ApprovalModeYolo {
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if granted {
		return approvalResolution{Mode: agentservice.ApprovalSession}, nil
	}
	if mode == ApprovalModeAutoReview {
		resolution, approvalErr := s.automaticApproval(ctx, event, request, func(decisionCtx context.Context, mode agentservice.ApprovalMode, decidedBy string) error {
			return s.recordTeamApprovalDecision(decisionCtx, event, request, mode, decidedBy)
		})
		if approvalErr == nil && resolution.Mode == agentservice.ApprovalDenied {
			resolution.Retry, resolution.Prevent = s.observePermissionDenied(ctx, metadata, call, resolution.DenialMessage, false)
		}
		return resolution, approvalErr
	}
	hookDecision := s.permissionHook(ctx, metadata, call)
	if hookDecision.behavior == "allow" || hookDecision.behavior == "deny" {
		resolvedMode := agentservice.ApprovalOnce
		if hookDecision.behavior == "deny" {
			resolvedMode = agentservice.ApprovalDenied
		}
		if err := s.recordTeamApprovalDecision(ctx, event, request, resolvedMode, "hook:"+hookDecision.name); err != nil {
			return approvalResolution{}, err
		}
		if resolvedMode == agentservice.ApprovalDenied {
			message := firstNonempty(hookDecision.message, "Denied by permission hook")
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, message, hookDecision.interrupt)
			return approvalResolution{Mode: resolvedMode, DenialMessage: message, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
	live := &liveApproval{
		approvalID: approvalID, agentType: "team", runID: runID, callID: call.ID,
		sessionID: sessionID, fingerprint: fingerprint, decision: make(chan agentservice.ApprovalMode, 1),
	}
	s.mu.Lock()
	if _, exists := s.liveApprovals[approvalID]; exists {
		s.mu.Unlock()
		return approvalResolution{}, fmt.Errorf("approval ID collision")
	}
	s.liveApprovals[approvalID] = live
	s.mu.Unlock()
	defer s.finishLiveApproval(live)
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", action)
	s.emit(s.ctx, event)
	select {
	case <-ctx.Done():
		return approvalResolution{}, ctx.Err()
	case resolvedMode := <-live.decision:
		if resolvedMode == agentservice.ApprovalDenied {
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, "Denied by user", false)
			return approvalResolution{Mode: resolvedMode, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
}

func (s *Service) bindProviderEngine(engine hyagent.Engine) hyagent.Engine {
	if engine.Tools == nil {
		return engine
	}
	bound := make([]tool.Driver, 0)
	for _, definition := range engine.Tools.Definitions() {
		driver, ok := engine.Tools.Driver(definition.Name)
		if !ok {
			continue
		}
		if governed, ok := driver.(*governedAgentTool); ok {
			clone := *governed
			clone.host = s
			bound = append(bound, &clone)
		} else {
			bound = append(bound, driver)
		}
	}
	engine.Tools = tool.NewBus(bound...)
	return engine
}

func (s *Service) awaitApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error) {
	approvalID, err := randomID("approval")
	if err != nil {
		return approvalResolution{}, err
	}
	request := runApprovalReviewRequest(run, agentID, agentType, call, pending)
	event := Event{
		Kind: EventApprovalRequested, SessionID: sessionID, RunID: run.RunID, AgentID: agentID,
		ToolCallID: call.ID, ApprovalID: approvalID, Text: pending.Request.RequestedAction,
		Data: map[string]string{
			"tool": call.Name, "target": pending.Scope.Target, "risk": pending.Scope.Risk,
			"effect": pending.Effect, "action": pending.Request.RequestedAction, "agent_type": agentType,
		},
	}
	metadata := hooks.Metadata{SessionID: sessionID, RunID: run.RunID, AgentID: agentID, AgentType: agentType, CWD: s.cfg.Workspace.Root}
	s.mu.Lock()
	mode := s.approvalMode
	s.mu.Unlock()
	if mode == ApprovalModeYolo {
		if s.coding == nil {
			return approvalResolution{}, fmt.Errorf("coding runtime is unavailable")
		}
		if err := s.coding.ResolveApproval(ctx, run, call.ID, agentservice.ApprovalOnce, "approval-mode:yolo"); err != nil {
			return approvalResolution{}, err
		}
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if mode == ApprovalModeAutoReview {
		resolution, approvalErr := s.automaticApproval(ctx, event, request, func(decisionCtx context.Context, mode agentservice.ApprovalMode, decidedBy string) error {
			if s.coding == nil {
				return fmt.Errorf("coding runtime is unavailable")
			}
			return s.coding.ResolveApproval(decisionCtx, run, call.ID, mode, decidedBy)
		})
		if approvalErr == nil && resolution.Mode == agentservice.ApprovalDenied {
			resolution.Retry, resolution.Prevent = s.observePermissionDenied(ctx, metadata, call, resolution.DenialMessage, false)
		}
		return resolution, approvalErr
	}
	hookDecision := s.permissionHook(ctx, metadata, call)
	if hookDecision.behavior == "allow" || hookDecision.behavior == "deny" {
		resolvedMode := agentservice.ApprovalOnce
		if hookDecision.behavior == "deny" {
			resolvedMode = agentservice.ApprovalDenied
		}
		if s.coding == nil {
			return approvalResolution{}, fmt.Errorf("coding runtime is unavailable")
		}
		if err := s.coding.ResolveApproval(ctx, run, call.ID, resolvedMode, "hook:"+hookDecision.name); err != nil {
			return approvalResolution{}, err
		}
		if resolvedMode == agentservice.ApprovalDenied {
			message := firstNonempty(hookDecision.message, "Denied by permission hook")
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, message, hookDecision.interrupt)
			return approvalResolution{Mode: resolvedMode, DenialMessage: message, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
	live := &liveApproval{
		approvalID: approvalID, agentID: agentID, agentType: agentType, run: run, runID: run.RunID,
		callID: call.ID, sessionID: sessionID, decision: make(chan agentservice.ApprovalMode, 1),
	}
	s.mu.Lock()
	if _, exists := s.liveApprovals[approvalID]; exists {
		s.mu.Unlock()
		return approvalResolution{}, fmt.Errorf("approval ID collision")
	}
	s.liveApprovals[approvalID] = live
	s.mu.Unlock()
	defer s.finishLiveApproval(live)
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", pending.Request.RequestedAction)
	s.emit(s.ctx, event)
	select {
	case <-ctx.Done():
		return approvalResolution{}, ctx.Err()
	case resolvedMode := <-live.decision:
		if resolvedMode == agentservice.ApprovalDenied {
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, "Denied by user", false)
			return approvalResolution{Mode: resolvedMode, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
}

func (s *Service) recordTeamApprovalDecision(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	mode agentservice.ApprovalMode,
	decidedBy string,
) error {
	if s.coding == nil {
		return fmt.Errorf("coding runtime is unavailable")
	}
	decision := "rejected"
	if mode == agentservice.ApprovalOnce || mode == agentservice.ApprovalSession {
		decision = "approved"
	}
	return s.coding.Runner().AppendEvent(ctx, api.Event{
		RunID: event.RunID, Type: api.EventApprovalDecided, RecordedAt: time.Now().UTC(),
		Payload: map[string]any{
			"approvalId": event.ApprovalID, "actionId": event.ToolCallID,
			"decidedBy": decidedBy, "decision": decision, "reason": request.RequestedReason,
			"tool": request.ToolName, "target": request.Target, "risk": request.Risk,
		},
	})
}

func (s *Service) automaticApproval(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	decide func(context.Context, agentservice.ApprovalMode, string) error,
) (approvalResolution, error) {
	event.State = "reviewing"
	event.Data["reviewer"] = codex.ApprovalReviewerModel
	s.emit(s.ctx, event)

	providerRequest, err := request.codexRequest()
	failureKind := codex.ReviewFailureInvalidRequest
	var assessment codex.ApprovalReview
	if err == nil {
		if s.authentication == nil {
			err = fmt.Errorf("authentication is unavailable")
			failureKind = codex.ReviewFailureAuthentication
		} else if active, authErr := s.authentication.HasActiveChatGPTAccount(ctx); authErr != nil {
			err = authErr
			failureKind = codex.ReviewFailureAuthentication
		} else if !active {
			err = fmt.Errorf("no active ChatGPT account is available")
			failureKind = codex.ReviewFailureAuthentication
		}
	}
	if err == nil {
		if s.providers == nil {
			err = fmt.Errorf("provider runtime is unavailable")
			failureKind = codex.ReviewFailureProvider
		} else {
			var reviewer *codex.Reviewer
			reviewer, err = s.providers.ApprovalReviewer(ctx)
			if err == nil {
				assessment, err = reviewer.Review(ctx, providerRequest)
			}
			if err != nil {
				failureKind = codex.ReviewFailure(err)
			}
		}
	}

	if err != nil {
		state := "auto_failed"
		message := fmt.Sprintf("Automatic review failed (%s); action did not run.", failureKind)
		detail := strings.TrimSpace(err.Error())
		rationale := "Automatic approval review failed (" + string(failureKind) + "): " + detail
		if strings.HasPrefix(detail, "automatic approval review failed") {
			rationale = "Automatic" + strings.TrimPrefix(detail, "automatic")
		}
		rationale = boundedReviewText(rationale, 600)
		if failureKind == codex.ReviewFailureTimeout {
			state = "auto_timed_out"
			message = "Automatic review timed out; action did not run."
			rationale = "Automatic approval review timed out while evaluating the requested action."
		}
		decisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		decisionErr := decide(decisionCtx, agentservice.ApprovalDenied, "system:auto-review-failure")
		cancel()
		s.recordAutoReview(event.RunID, false)
		// Reviewer infrastructure failures are fail-closed assessments, not a
		// successful classification of the request's original risk.
		s.emitAutomaticApprovalResolved(event, state, "high", "unknown", rationale, string(failureKind), message)
		if decisionErr != nil {
			return approvalResolution{}, errors.Join(err, fmt.Errorf("record fail-closed approval decision: %w", decisionErr))
		}
		return approvalResolution{Mode: agentservice.ApprovalDenied, DenialMessage: message}, nil
	}

	rationale := boundedReviewText(assessment.Rationale, 600)
	decisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if assessment.Outcome == "allow" {
		decisionErr := decide(decisionCtx, agentservice.ApprovalOnce, codex.ApprovalReviewerModel)
		cancel()
		s.recordAutoReview(event.RunID, false)
		if decisionErr != nil {
			message := "Automatic review could not record approval; action did not run."
			s.emitAutomaticApprovalResolved(event, "auto_failed", assessment.RiskLevel, assessment.UserAuthorization, rationale, "decision", message)
			return approvalResolution{}, fmt.Errorf("record automatic approval: %w", decisionErr)
		}
		message := "Approved by automatic review: " + rationale
		s.emitAutomaticApprovalResolved(event, "auto_approved", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message)
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}

	decisionErr := decide(decisionCtx, agentservice.ApprovalDenied, codex.ApprovalReviewerModel)
	cancel()
	limitErr := s.recordAutoReview(event.RunID, true)
	message := "Denied by automatic review: " + rationale +
		"\nDo not retry the same outcome through a variant or workaround. Choose a materially safer alternative or ask the user."
	if decisionErr != nil {
		s.emitAutomaticApprovalResolved(event, "auto_failed", assessment.RiskLevel, assessment.UserAuthorization, rationale, "decision", "Automatic review denial could not be recorded; action did not run.")
		return approvalResolution{}, fmt.Errorf("record automatic denial: %w", decisionErr)
	}
	s.emitAutomaticApprovalResolved(event, "auto_denied", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message)
	if limitErr != nil {
		return approvalResolution{}, limitErr
	}
	return approvalResolution{Mode: agentservice.ApprovalDenied, DenialMessage: message}, nil
}

func (s *Service) emitAutomaticApprovalResolved(event Event, state, risk, authorization, rationale, errorKind, text string) {
	s.emit(s.ctx, Event{
		Kind: EventApprovalResolved, SessionID: event.SessionID, RunID: event.RunID, AgentID: event.AgentID,
		ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID, State: state, Text: text,
		Data: map[string]string{
			"reviewer": codex.ApprovalReviewerModel, "risk": risk, "user_authorization": authorization,
			"rationale": rationale, "error_kind": errorKind, "tool": event.Data["tool"],
			"target": event.Data["target"], "effect": event.Data["effect"],
		},
	})
}

func (s *Service) recordAutoReview(runID string, denied bool) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tracker := s.autoReviewDenials[runID]
	if tracker == nil {
		tracker = &autoReviewDenialTracker{}
		s.autoReviewDenials[runID] = tracker
	}
	if tracker.reviewCount == len(tracker.recent) {
		if tracker.recent[tracker.next] {
			tracker.recentDenials--
		}
	} else {
		tracker.reviewCount++
	}
	tracker.recent[tracker.next] = denied
	tracker.next = (tracker.next + 1) % len(tracker.recent)
	if denied {
		tracker.recentDenials++
		tracker.consecutiveDeny++
	} else {
		tracker.consecutiveDeny = 0
	}
	if tracker.consecutiveDeny >= 3 || tracker.recentDenials >= 10 {
		return &AutoReviewDenialLimitError{
			RunID: runID, ConsecutiveDenials: tracker.consecutiveDeny, RecentDenials: tracker.recentDenials,
		}
	}
	return nil
}

func (s *Service) clearAutoReviewTracker(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	delete(s.autoReviewDenials, runID)
	s.mu.Unlock()
}

func boundedReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (s *Service) setApprovalMode(ctx context.Context, mode ApprovalMode) error {
	if mode != ApprovalModePrompt && mode != ApprovalModeAutoReview && mode != ApprovalModeYolo {
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	if mode == ApprovalModeAutoReview {
		if s.authentication == nil {
			s.mu.Lock()
			s.approvalMode = ApprovalModePrompt
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			return fmt.Errorf("authentication is unavailable")
		}
		active, err := s.authentication.HasActiveChatGPTAccount(ctx)
		if err != nil || !active {
			s.mu.Lock()
			s.approvalMode = ApprovalModePrompt
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			if err != nil {
				return err
			}
			return fmt.Errorf("Approve for me requires an active ChatGPT account")
		}
	}
	s.mu.Lock()
	previousMode := s.approvalMode
	s.approvalMode = mode
	if mode == ApprovalModePrompt || mode == ApprovalModeAutoReview {
		s.mu.Unlock()
		if err := s.persistApprovalMode(mode); err != nil {
			s.mu.Lock()
			s.approvalMode = previousMode
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			return err
		}
		s.emitApprovalMode(s.ctx)
		return nil
	}
	approvalIDs := make([]string, 0, len(s.liveApprovals))
	for approvalID, live := range s.liveApprovals {
		if !live.resolving && !live.resolved {
			approvalIDs = append(approvalIDs, approvalID)
		}
	}
	s.mu.Unlock()

	var failures []error
	for _, approvalID := range approvalIDs {
		resolved, err := s.resolveLiveApproval(ctx, approvalID, "once", "approval-mode:yolo")
		if err == nil || !resolved {
			continue
		}
		s.mu.Lock()
		live := s.liveApprovals[approvalID]
		superseded := live == nil || live.resolving || live.resolved
		s.mu.Unlock()
		if !superseded {
			failures = append(failures, err)
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		s.mu.Lock()
		if s.approvalMode == ApprovalModeYolo {
			s.approvalMode = ApprovalModePrompt
		}
		s.mu.Unlock()
	} else if persistErr := s.persistApprovalMode(mode); persistErr != nil {
		s.mu.Lock()
		s.approvalMode = previousMode
		s.mu.Unlock()
		err = persistErr
	}
	s.emitApprovalMode(s.ctx)
	return err
}

func (s *Service) persistApprovalMode(mode ApprovalMode) error {
	if err := s.dispatchLifecycle(s.ctx, hooks.ConfigChange, s.hookMetadata(s.currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateDefault(s.configPath, "approval_mode", string(mode))
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Defaults.ApprovalMode = string(mode)
	s.mu.Unlock()
	return nil
}

func (s *Service) resolveLiveApproval(ctx context.Context, approvalID, decision, decidedBy string) (bool, error) {
	if strings.TrimSpace(decidedBy) == "" {
		return true, fmt.Errorf("approval decider is empty")
	}
	s.mu.Lock()
	live := s.liveApprovals[approvalID]
	if live == nil {
		s.mu.Unlock()
		return false, nil
	}
	if live.resolving || live.resolved {
		s.mu.Unlock()
		return true, fmt.Errorf("approval %q is already being resolved", approvalID)
	}
	mode, err := approvalDecisionMode(decision)
	if err != nil {
		s.mu.Unlock()
		return true, err
	}
	live.resolving = true
	s.mu.Unlock()

	if live.run != nil {
		if err := s.coding.ResolveApproval(ctx, live.run, live.callID, mode, decidedBy); err != nil {
			s.mu.Lock()
			if current := s.liveApprovals[approvalID]; current == live {
				live.resolving = false
			}
			s.mu.Unlock()
			return true, err
		}
	}
	s.mu.Lock()
	if current := s.liveApprovals[approvalID]; current != live {
		s.mu.Unlock()
		return true, fmt.Errorf("approval %q is no longer pending", approvalID)
	}
	if live.run == nil && mode == agentservice.ApprovalSession {
		s.teamApprovals[live.fingerprint] = struct{}{}
	}
	live.resolving = false
	live.resolved = true
	s.mu.Unlock()
	select {
	case live.decision <- mode:
	default:
		return true, fmt.Errorf("approval %q was already resolved", approvalID)
	}
	s.emit(ctx, Event{
		Kind: EventApprovalResolved, SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID,
		ToolCallID: live.callID, ApprovalID: approvalID, State: decision, Data: map[string]string{"decided_by": decidedBy},
	})
	if s.providers != nil {
		s.providers.AutoWakePending(live.sessionID)
	}
	return true, nil
}

func approvalDecisionMode(decision string) (agentservice.ApprovalMode, error) {
	switch decision {
	case "once", "approved", "approve":
		return agentservice.ApprovalOnce, nil
	case "session":
		return agentservice.ApprovalSession, nil
	case "deny", "denied", "reject", "rejected":
		return agentservice.ApprovalDenied, nil
	default:
		return "", fmt.Errorf("invalid approval decision %q", decision)
	}
}

func (s *Service) finishLiveApproval(live *liveApproval) {
	s.mu.Lock()
	cancelled := false
	if s.liveApprovals[live.approvalID] == live {
		delete(s.liveApprovals, live.approvalID)
		cancelled = !live.resolved
	}
	s.mu.Unlock()
	if cancelled {
		s.emit(s.ctx, Event{
			Kind: EventApprovalResolved, SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID,
			ToolCallID: live.callID, ApprovalID: live.approvalID, State: "cancelled",
		})
		if s.providers != nil {
			s.providers.AutoWakePending(live.sessionID)
		}
	}
}

func (s *Service) providerStreamSink(sessionID, runID string) stream.Sink {
	return stream.SinkFunc(func(ctx context.Context, frame stream.Frame) error {
		data := map[string]string{}
		switch frame.Kind {
		case stream.FrameText:
			s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Text, Data: data})
		case stream.FrameThinking:
			s.emit(ctx, Event{Kind: EventThinkingDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Thinking, Data: data})
		case stream.FrameToolCall:
			if frame.ToolCall != nil {
				data["name"] = frame.ToolCall.Name
				data["arguments"] = string(frame.ToolCall.Arguments)
				s.emit(ctx, Event{Kind: EventToolStarted, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolCall.ID, State: "running", Data: data})
			}
		case stream.FrameToolResult:
			if frame.ToolResult != nil {
				state := "completed"
				if frame.ToolResult.IsError {
					state = "failed"
				}
				data["name"] = frame.ToolResult.Name
				if len(frame.ToolResult.Structured) > 0 {
					data["structured"] = string(frame.ToolResult.Structured)
				}
				s.emit(ctx, Event{Kind: EventToolFinished, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolResult.ToolCallID, State: state, Text: frame.ToolResult.Content, Data: data})
			}
		case stream.FrameDone:
			usage := map[string]string{}
			if frame.Usage.InputTokens != 0 || frame.Usage.OutputTokens != 0 || frame.Usage.TotalTokens != 0 {
				usage["inputTokens"] = fmt.Sprint(frame.Usage.InputTokens)
				usage["cachedInputTokens"] = fmt.Sprint(frame.Usage.CachedInputTokens)
				usage["outputTokens"] = fmt.Sprint(frame.Usage.OutputTokens)
				usage["totalTokens"] = fmt.Sprint(frame.Usage.TotalTokens)
				usage["cacheStatus"] = "reported"
			}
			s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: sessionID, RunID: runID, State: "reported", Data: usage})
		case stream.FrameError:
			if frame.Err != nil {
				return frame.Err
			}
		}
		return nil
	})
}

func normalizeTurnRequest(request TurnRequest, defaults config.DefaultsConfig) TurnRequest {
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.SessionID == "" {
		request.SessionID = "default"
	}
	if request.Provider == "" {
		request.Provider = defaults.Provider
	}
	if request.Model == "" {
		request.Model = defaults.Model
	}
	if request.Reasoning == "" {
		request.Reasoning = defaults.Reasoning
	}
	if request.AgentMode == "" {
		request.AgentMode = defaults.AgentMode
	}
	return request
}

func (s *Service) runProviderTurn(ctx context.Context, request TurnRequest, run *agentservice.Run, engine hyagent.Engine) {
	defer s.wg.Done()
	defer s.clearRun(run.RunID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: run.RunID, State: "running"})
	task := api.Task{
		ID: run.TaskID, RunID: run.RunID, Type: api.TaskTypeWorker, Goal: request.Prompt,
		Budget: &api.TaskBudget{
			MaxTokens: s.cfg.Agents.Main.MaxTokens, MaxWallClock: 20 * time.Minute,
			MaxToolCalls: s.cfg.Agents.Main.MaxToolCalls,
		},
	}
	result := engine.RunStream(ctx, task, hyagent.OutputPolicy{}, s.providerStreamSink(request.SessionID, run.RunID))
	var runErr error
	if result.Failure != nil {
		runErr = result.Failure
		if errors.Is(runErr, hyagent.ErrBudgetExhausted) && strings.Contains(runErr.Error(), "max tokens") {
			runErr = fmt.Errorf("%w (increase agents.main.max_tokens in config.yaml for unusually large tasks)", runErr)
		}
		if errors.Is(runErr, hyagent.ErrBudgetExhausted) && strings.Contains(runErr.Error(), "max tool calls") {
			runErr = fmt.Errorf("%w (increase agents.main.max_tool_calls, or set it to 0 for unbounded, in config.yaml)", runErr)
		}
	}
	completionCtx, completionCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completionCancel()
	if err := s.coding.CompleteRun(completionCtx, run, result.Text, runErr); err != nil {
		if runErr == nil {
			runErr = err
		} else {
			runErr = fmt.Errorf("%v; durable completion: %w", runErr, err)
		}
	}
	if ctx.Err() != nil {
		s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "cancelled", ctx.Err())
		s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: request.SessionID, RunID: run.RunID, State: "cancelled"})
		return
	}
	if runErr != nil {
		s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "failed", runErr)
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: runErr.Error()})
		return
	}
	if s.sessions != nil && strings.TrimSpace(result.Text) != "" {
		if err := s.sessions.AppendBlock(ctx, request.SessionID, session.Block{Kind: "assistant", RunID: run.RunID, Title: "Azem", Content: result.Text, State: "completed"}); err != nil {
			s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "persist_failed", err)
			s.emit(ctx, Event{Kind: EventRunFailed, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: err.Error()})
			return
		}
	}
	if err := s.persistRecap(ctx, request.SessionID, run.RunID, request.Prompt, result.Text, request.Todo); err != nil {
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: err.Error()})
	}
	s.emit(ctx, Event{Kind: EventRunFinished, SessionID: request.SessionID, RunID: run.RunID, State: "completed"})
}

func teamPrompt(request TurnRequest) string {
	reminder := todoReminder(request.Todo)
	if len(request.History) == 0 && reminder == "" {
		return request.Prompt
	}
	var context strings.Builder
	if len(request.History) > 0 {
		context.WriteString("Continue this coding session using the prior conversation as context.\n\n")
	}
	for _, block := range request.History {
		text := strings.TrimSpace(block.Content)
		if text == "" {
			continue
		}
		fmt.Fprintf(&context, "%s: %s\n", firstNonempty(block.Title, block.Kind), text)
	}
	if reminder != "" {
		context.WriteString("\n")
		context.WriteString(reminder)
		context.WriteString("\n")
	}
	context.WriteString("\nCurrent request: ")
	context.WriteString(request.Prompt)
	return context.String()
}

func (s *Service) runProviderTeam(ctx context.Context, request TurnRequest, runID, goal, modelID string, resolver hyprovider.Resolver) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: runID, State: "running"})
	models := agentservice.TeamModels{Planner: modelID, Implementer: modelID, Reviewer: modelID, Reporter: modelID}
	toolBus, toolErr := s.teamToolBus(ctx, request.SessionID, runID, goal)
	if toolErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, agentservice.TeamExecution{}, toolErr)
		return
	}
	execution, err := s.coding.StartTeamWithIDAndToolsMetadataHooks(ctx, runID, goal, models, resolver, toolBus, map[string]string{
		"session_id":           request.SessionID,
		"provider":             request.Provider,
		"model":                modelID,
		"reasoning":            request.Reasoning,
		"original_prompt":      request.Prompt,
		"hook_private_context": request.privateContext,
	}, s.teamHooks(request.SessionID, runID, request.privateContext, request.historicalContext), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: request.SessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, execution, err)
}

func (s *Service) runResumedProviderTeam(ctx context.Context, sessionID, runID, durableGoal, recapGoal, modelID, privateContext, historicalContext string, resolver hyprovider.Resolver) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: sessionID, RunID: runID, State: "resuming"})
	models := agentservice.TeamModels{Planner: modelID, Implementer: modelID, Reviewer: modelID, Reporter: modelID}
	toolBus, toolErr := s.teamToolBus(ctx, sessionID, runID, durableGoal)
	if toolErr != nil {
		s.finishProviderTeam(ctx, sessionID, runID, recapGoal, session.TodoList{}, agentservice.TeamExecution{}, toolErr)
		return
	}
	execution, err := s.coding.ResumeTeamWithToolsHooks(ctx, runID, models, resolver, toolBus, s.teamHooks(sessionID, runID, privateContext, historicalContext), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: sessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, sessionID, runID, recapGoal, session.TodoList{}, execution, err)
}

func (s *Service) teamHooks(sessionID, parentRunID, rootContext, historicalContext string) agentservice.TeamHooks {
	beforeTask := func(ctx context.Context, dispatch multiagent.Dispatch, class multiagent.AgentClass) error {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		return s.dispatchLifecycle(ctx, hooks.TaskCreated, metadata, func(e *hooks.Envelope) {
			e.TaskID, e.TaskSubject = dispatch.Task.ID, dispatch.Task.Goal
		})
	}
	prepare := func(ctx context.Context, engine hyagent.Engine, dispatch multiagent.Dispatch, class multiagent.AgentClass) (hyagent.Engine, error) {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		decision := s.hooks.Dispatch(ctx, hooks.Envelope{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To,
			AgentType: class.Name, ParentRunID: parentRunID, CWD: metadata.CWD, HookEventName: hooks.SubagentStart})
		if decision.PreventContinuation {
			return engine, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
		}
		var additional []string
		for _, run := range decision.Runs {
			if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
				additional = append(additional, text)
			}
		}
		contextParts := append([]string{strings.TrimSpace(rootContext)}, additional...)
		contextText := strings.TrimSpace(strings.Join(contextParts, "\n"))
		historical := ""
		if class.Name == agentservice.PlannerClass {
			historical = historicalContext
		}
		if contextText != "" || historical != "" {
			engine.ContextBuilder = teamHookContext{inner: engine.ContextBuilder, additional: contextText, historical: historical}
		}
		return engine, nil
	}
	decorate := func(engine hyagent.Engine, dispatch multiagent.Dispatch, class multiagent.AgentClass) hyagent.Engine {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To,
			AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		taskGuardrail := func(event hooks.Event, defaultReason string, add func(*hooks.Envelope)) hyagent.OutputGuardrail {
			return hyagent.NewOutputGuardrail("claude-"+strings.ToLower(string(event))+"-hook", func(ctx context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
				envelope := hooks.Envelope{SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
					AgentType: metadata.AgentType, ParentRunID: metadata.ParentRunID, CWD: metadata.CWD, HookEventName: event,
					LastAssistantMessage: strings.TrimSpace(input.Output.Text)}
				add(&envelope)
				decision := s.hooks.Dispatch(ctx, envelope)
				if decision.PreventContinuation {
					return hyagent.OutputGuardrailResult{}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
				}
				if !decision.Denied {
					return hyagent.AllowOutput(), nil
				}
				return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true},
					message.NewText(message.RoleUser, firstNonempty(strings.TrimSpace(decision.Reason), defaultReason))), nil
			})
		}
		guardrails := []hyagent.OutputGuardrail{
			s.stopHookGuardrail(metadata, hooks.SubagentStop, func(input hyagent.OutputGuardrailInput) string {
				transcript, err := json.Marshal(append(append([]message.Message(nil), input.Messages...), input.Output))
				if err != nil {
					return ""
				}
				path, _ := writeSubagentHookTranscript(s.cfg.Workspace.Root, dispatch.To, transcript)
				return path
			}),
			taskGuardrail(hooks.TaskCompleted, "A TaskCompleted hook requested more work before completion.", func(e *hooks.Envelope) {
				e.TaskID, e.TaskSubject = dispatch.Task.ID, dispatch.Task.Goal
			}),
			taskGuardrail(hooks.TeammateIdle, "A TeammateIdle hook requested that this teammate continue working.", func(e *hooks.Envelope) {
				e.TeammateName, e.TeamName = firstNonempty(class.Name, dispatch.To), parentRunID
			}),
		}
		if class.Name == agentservice.ReporterClass {
			guardrails = append(guardrails, s.stopHookGuardrail(s.hookMetadata(sessionID, parentRunID), hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
				return writeSessionHookTranscript(sessionID, append(append([]message.Message(nil), input.Messages...), input.Output))
			}))
		}
		engine.OutputGuardrails = append(engine.OutputGuardrails, guardrails...)
		return engine
	}
	return agentservice.TeamHooks{BeforeTask: beforeTask, PrepareEngine: prepare, DecorateEngine: decorate}
}

type teamHookContext struct {
	inner      hyagent.ContextManager
	additional string
	historical string
}

func (c teamHookContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	messages, err := c.inner.Build(ctx, task)
	if err != nil {
		return nil, err
	}
	prefix := make([]message.Message, 0, 2)
	if c.additional != "" {
		value := message.NewText(message.RoleSystem, "[Trusted SubagentStart hook context]\n"+c.additional)
		value.Visibility = message.VisibilityPrivate
		prefix = append(prefix, value)
	}
	if c.historical != "" {
		policy := message.NewText(message.RoleSystem, historicalEvidencePolicy)
		policy.Visibility = message.VisibilityPrivate
		prefix = append(prefix, policy)
	}
	result := append(prefix, messages...)
	if c.historical == "" {
		return result, nil
	}
	insertAt := 0
	for insertAt < len(result) && result[insertAt].Role == message.RoleSystem {
		insertAt++
	}
	data := message.NewText(message.RoleUser, "<historical-evidence-json>\n"+c.historical+"\n</historical-evidence-json>")
	data.Visibility = message.VisibilityPrivate
	result = append(result, message.Message{})
	copy(result[insertAt+1:], result[insertAt:])
	result[insertAt] = data
	return result, nil
}

func (c teamHookContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	return c.inner.Compact(ctx, history)
}

func (c teamHookContext) CompactTo(ctx context.Context, history []message.Message, target int) ([]message.Message, error) {
	if targeter, ok := c.inner.(hyagent.TargetContextManager); ok {
		return targeter.CompactTo(ctx, history, target)
	}
	return c.inner.Compact(ctx, history)
}

func (s *Service) finishProviderTeam(ctx context.Context, sessionID, runID, goal string, todo session.TodoList, execution agentservice.TeamExecution, err error) {
	if ctx.Err() != nil {
		s.observeStop(sessionID, runID, hooks.StopFailure, "cancelled", ctx.Err())
		s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: sessionID, RunID: runID, State: "cancelled"})
		return
	}
	if err != nil {
		s.observeStop(sessionID, runID, hooks.StopFailure, "failed", err)
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
		return
	}
	answer := teamAnswer(execution.Result.State)
	if strings.TrimSpace(answer) == "" {
		s.observeStop(sessionID, runID, hooks.StopFailure, "empty_answer", errors.New("coding team completed without a reporter answer"))
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: "coding team completed without a reporter answer"})
		return
	}
	s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: answer})
	if s.sessions != nil {
		if err := s.sessions.AppendBlock(ctx, sessionID, session.Block{Kind: "assistant", RunID: runID, Title: "Azem team", Content: answer, State: "completed"}); err != nil {
			s.observeStop(sessionID, runID, hooks.StopFailure, "persist_failed", err)
			s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
			return
		}
	}
	if err := s.persistRecap(ctx, sessionID, runID, goal, answer, todo); err != nil {
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
	}
	s.emit(ctx, Event{Kind: EventRunFinished, SessionID: sessionID, RunID: runID, State: "completed"})
}

func teamAnswer(state multiagent.TeamState) string {
	for index := len(state.Tasks) - 1; index >= 0; index-- {
		result := state.Tasks[index].Result
		if result == nil || result.Structured == nil {
			continue
		}
		if answer, ok := result.Structured["answer"].(string); ok && strings.TrimSpace(answer) != "" {
			return answer
		}
	}
	return ""
}
