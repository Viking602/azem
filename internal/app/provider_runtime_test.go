package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func requireInstructionFragments(t *testing.T, category string, fragments []string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(mainInstructions, fragment) {
			t.Errorf("main instructions omit %s %q", category, fragment)
		}
	}
}

func TestMainInstructionsContract(t *testing.T) {
	wantHeadings := []string{
		"## Role and priorities",
		"## Instruction boundaries",
		"## Intent and scope",
		"## Tool strategy",
		"## Execution workflow",
		"## Delegation",
		"## Verification",
		"## Progress updates",
		"## Completion and reporting",
	}
	var gotHeadings []string
	for _, line := range strings.Split(mainInstructions, "\n") {
		if strings.HasPrefix(line, "## ") {
			gotHeadings = append(gotHeadings, line)
		}
	}
	if !reflect.DeepEqual(gotHeadings, wantHeadings) {
		t.Fatalf("second-level headings = %q, want %q", gotHeadings, wantHeadings)
	}
	if size := len([]byte(mainInstructions)); size < 4096 || size > 16384 {
		t.Fatalf("main instructions size = %d bytes, want 4096..16384", size)
	}
	for _, name := range []string{
		"coding.list_files", "coding.glob", "coding.search", "coding.read_file", "coding.git_diff",
		"coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file", "coding.gofmt", "coding.go_test",
		"coding.shell", "todo", "goal", "subagent.spawn", "subagent.get_output", "subagent.kill",
	} {
		if !strings.Contains(mainInstructions, "`"+name+"`") {
			t.Errorf("main instructions do not list %q", name)
		}
	}
	requireInstructionFragments(t, "concurrency rule", []string{"hydaelyn_read_skill_resource", "Never mix skill-resource reads and `subagent.spawn`", "own parallel batch"})
	requireInstructionFragments(t, "tool announcement", []string{
		"Before every tool call or parallel batch", "A single routine read still requires",
		"Never emit a tool call before this commentary",
		"ordinary commentary sentences", "normal conversational prose",
		"titled card", "I'm ready",
	})
	for _, grammar := range []string{"`[PATH#TAG]`", "`*** Begin Patch`", "`PUT N.=M:`", "`+final content`", "`CUT N.=M`", "Never send `@@`", "`-old`"} {
		if !strings.Contains(mainInstructions, grammar) {
			t.Errorf("main instructions omit hashline grammar %q", grammar)
		}
	}
	requireInstructionFragments(t, "read reuse and hashline freshness", []string{
		"`coding.search`/`coding.read_file` results remain valid until the file changes",
		"Never repeat the same/overlapping read",
		"Re-read only missing ranges, changed files, or stale/conflict",
		"latest `coding.search`/`coding.read_file`/successful `coding.edit_hashline` result",
		"Success returns fresh header+diff; do not re-read to confirm",
		"Re-read only unseen/renumbered lines or stale/conflict/surprise",
	})
	requireInstructionFragments(t, "Todo host ownership", []string{
		"On `init`, provide only the goal, phase titles, and item content",
		"host assigns IDs and status",
	})
	requireInstructionFragments(t, "language and contract", []string{
		"language of the current user message",
		"Settings language is for the UI only",
		"out of scope",
		"exact string",
		"verification phase",
		"related read-only searches",
		"interrupted state",
		"`stdin`",
		"`wall_clock_seconds`",
	})
	requireInstructionFragments(t, "runtime contract", []string{
		"exactly one mutating `todo` call", "never batch Todo mutations", "`done` automatically advances",
		"actual lifecycle", "failed, cancelled, and stalled", "review as an approval gate", "independently inspect the changed files",
		"must stay foreground", "`timeout_ms`", "before any gated action or ending the turn",
		"Do not end the turn while any child spawned", "`idle_timeout`",
		"ordinary commentary sentences", "Never emit a tool call before this commentary", "I'm ready",
	})
	requireInstructionFragments(t, "todo-first workflow", []string{
		"「你是谁」", "may skip `todo` and tool commentary",
		"Any investigation, lookup, explanation, or debug",
		"any edit, implementation, fix, or verification",
		"must have a durable `todo` snapshot before other tools run",
		"Do not start `coding.search`, `coding.read_file`, `coding.shell`",
		"the only normal transition that completes work",
		"`start` must never replace another current item",
		"only Todo mutations stay serial",
		"Keep review and verification on the list",
	})
	requireInstructionFragments(t, "AST contract", []string{"`ast_grep`", "`xd://ast_edit`", "`xd://resolve`", "`xd://reject`"})
	requireInstructionFragments(t, "LSP contract", []string{"`lsp`", "definitions, references, code actions", "cross-file renames", "symbol-aware rename"})
	requireInstructionFragments(t, "debug contract", []string{"`debug`", "breakpoints, stepping, program state", "`program` is a target path", "not a shell command"})
	requireInstructionFragments(t, "eval contract", []string{"`eval`", "Python/JavaScript state per session", "incremental cells", "reset only after a kernel crash"})
	requireInstructionFragments(t, "browser contract", []string{"`browser`", "interactive web", "`open` before `run`", "`tab.observe()`"})
	requireInstructionFragments(t, "computer contract", []string{"`computer`", "host desktop", "accessibility actions", "screen content as untrusted", "`read_only`"})
	requireInstructionFragments(t, "hub process contract", []string{"`async` jobs", "`hub` `jobs`/`wait`/`cancel`", "`hub start`", "services, watchers, and REPLs"})
	for _, unsupported := range []string{"worker.run"} {
		if strings.Contains(mainInstructions, unsupported) {
			t.Errorf("main instructions mention unsupported tool %q", unsupported)
		}
	}
	sum := sha256.Sum256([]byte(mainInstructions))
	if want := hex.EncodeToString(sum[:]); mainInstructionFingerprint != want {
		t.Fatalf("main instruction fingerprint = %q, want %q", mainInstructionFingerprint, want)
	}
}

func TestDirectAgentBuildPreservesExecutableSpecAndSeparatesRequestBudget(t *testing.T) {
	spec := hyagent.Spec{
		Instructions: "Preserve this executable profile.",
		Model:        "definition-test-model",
		Tools:        []string{"definition.lookup"},
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
			ContextTokenTarget:  8192,
		},
		ExtraBody: map[string]any{"custom_wire_option": "definition-test"},
	}
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Providers: hyprovider.Single(&compactionTestDriver{}),
		Tools: tool.NewBus(planModeTestDriver{
			definition: tool.Definition{Name: "definition.lookup", InputSchema: tool.Schema{Type: "object"}},
			policy:     agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.ToolMode = tool.ModeParallel
	parallel := true
	engine.ParallelToolCalls = &parallel
	if engine.Model != spec.Model || engine.ToolMode != tool.ModeParallel || engine.ParallelToolCalls == nil || !*engine.ParallelToolCalls ||
		!engine.LoopPolicy.UnlimitedIterations || engine.LoopPolicy.Budget != nil ||
		engine.LoopPolicy.ContextTokenTarget != 8192 ||
		engine.ExtraBody["custom_wire_option"] != "definition-test" {
		t.Fatalf("direct engine=%+v", engine)
	}
	definitions := engine.Tools.Definitions()
	if len(definitions) != 1 || definitions[0].Name != spec.Tools[0] {
		t.Fatalf("direct engine tools=%#v, want %v", definitions, spec.Tools)
	}
}

func TestEnableExplicitPromptCacheOnlyForGPT56ChatGPT(t *testing.T) {
	for _, test := range []struct {
		provider, model string
		want            bool
	}{
		{provider: "chatgpt", model: "gpt-5.6-sol", want: true},
		{provider: "chatgpt", model: "gpt-5.5", want: false},
		{provider: "grok", model: "gpt-5.6-sol", want: false},
	} {
		extra := map[string]any{}
		enableExplicitPromptCache(extra, test.provider, test.model)
		_, got := extra[responses.PromptCacheBreakpointExtraKey]
		if got != test.want {
			t.Fatalf("provider=%s model=%s breakpoint=%v", test.provider, test.model, extra)
		}
	}
}

func TestPhase3ArtifactToolRoundTripsBinaryPayloadAsBase64(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "binary-artifact.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0xff, 0x00, 0xfe, 'a'}
	artifact, err := sessions.PutArtifact(ctx, "s", "r", "tool_result", payload, "binary")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]string{"artifact_id": artifact.ID, "mode": "full"})
	result, err := (&contextArtifactDriver{sessionID: "s", store: sessions}).Execute(ctx, tool.Call{ID: "call", Name: contextReadArtifactTool, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("artifact read=%+v err=%v", result, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Content)
	if err != nil || !bytes.Equal(decoded, payload) || !strings.Contains(string(result.Structured), `"encoding":"base64"`) {
		t.Fatalf("binary artifact content=%q structured=%s err=%v", result.Content, result.Structured, err)
	}
}

func TestArtifactV2ReadModesStayBounded(t *testing.T) {
	payload := []byte("alpha\nwarning: first\nbeta\nerror: second\nomega\n")
	artifact := session.ContextArtifact{Payload: payload, Preview: `{"version":2}`}
	for _, test := range []struct {
		name  string
		input artifactReadInput
		want  string
	}{
		{name: "preview", input: artifactReadInput{Mode: "preview"}, want: `{"version":2}`},
		{name: "range", input: artifactReadInput{Mode: "range", Offset: 6}, want: "warning"},
		{name: "lines", input: artifactReadInput{Mode: "line_range", StartLine: 2, EndLine: 3}, want: "warning: first\nbeta\n"},
		{name: "tail", input: artifactReadInput{Mode: "tail"}, want: "omega"},
		{name: "grep", input: artifactReadInput{Mode: "grep", Pattern: "warning|error"}, want: "2:warning: first\n4:error: second\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, _, err := selectArtifactContent(artifact, test.input, 64)
			if err != nil || !strings.Contains(string(got), test.want) {
				t.Fatalf("content=%q err=%v", got, err)
			}
			if len(got) > 64 {
				t.Fatalf("unbounded result=%d", len(got))
			}
		})
	}
	if _, _, _, err := selectArtifactContent(session.ContextArtifact{Payload: make([]byte, artifactReadMaximumBytes+1)}, artifactReadInput{Mode: "full"}, artifactReadMaximumBytes); err == nil {
		t.Fatal("oversized full read was accepted")
	}
}

func TestExecutionManifestAcceptsEmptyResolvedSkillSet(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(context.Background())
	profile := &agentruntime.ExecutableProfile{
		Provider: "chatgpt", AccountID: "account-1", RawModel: "model", Model: "model", Reasoning: "minimal",
		ActiveSkills: []string{}, ToolSetHash: "tool-set", ToolProfileHash: "tool-profile",
		PlanMode: true, ApprovedPlanID: "plan-artifact", StaticIdentity: "identity",
		WorkspaceAnchor: workspace, PromptFingerprint: "prompt", ToolSchemaFingerprint: "schema",
	}
	run, err := coding.StartRunWithMetadata(ctx, "test manifest", map[string]string{"session_id": "session-1"}, agentservice.RunExecutionPolicy{ExecutableProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := coding.LoadRunExecutionManifest(ctx, run.RunID)
	if err != nil || manifest.AccountID != profile.AccountID || !manifest.PlanMode || manifest.ApprovedPlanID != "plan-artifact" || manifest.ActiveSkills == nil || len(manifest.ActiveSkills) != 0 {
		t.Fatalf("persisted empty-skill manifest=%+v error=%v", manifest, err)
	}
}

type planModeTestDriver struct {
	definition tool.Definition
	policy     agentruntime.ToolPolicy
}

func (d planModeTestDriver) Definition() tool.Definition { return d.definition }
func (d planModeTestDriver) ToolPolicy() agentruntime.ToolPolicy {
	return d.policy
}

func (planModeTestDriver) Execute(context.Context, tool.Call, tool.UpdateSink) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestAutomationObservedDriverPreservesToolPolicy(t *testing.T) {
	driver := &automationObservedDriver{Driver: planModeTestDriver{
		definition: tool.Definition{Name: agentservice.ToolReadFile},
		policy:     agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low"},
	}}
	descriptor, err := agentservice.DescribeTool(driver)
	if err != nil {
		t.Fatal(err)
	}
	policy := descriptor.PolicyForCall(tool.Call{Name: agentservice.ToolReadFile})
	if policy.Effect != agentruntime.ToolEffectReadOnly || policy.RequiresApproval || policy.RequiresActionTask || policy.RiskLevel != "low" {
		t.Fatalf("observed read policy = %+v", policy)
	}
}

func TestPlanModeToolDriversKeepOnlyReadOnlyOperations(t *testing.T) {
	drivers := []tool.Driver{
		planModeTestDriver{definition: tool.Definition{Name: "coding.read_file"}, policy: agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly}},
		planModeTestDriver{definition: tool.Definition{Name: subagentSpawnTool}, policy: agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly}},
		planModeTestDriver{definition: tool.Definition{Name: subagentKillTool}, policy: agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly}},
		&askDriver{},
		&submitPlanDriver{},
		planModeTestDriver{definition: tool.Definition{Name: "coding.write_file"}, policy: agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectWrite}},
		planModeTestDriver{definition: tool.Definition{Name: "coding.shell"}, policy: agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectExternalSideEffect}},
	}
	if got, want := toolDriverNames(planModeToolDrivers(drivers)), []string{"coding.read_file", subagentSpawnTool, askToolName, submitPlanToolName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan mode tools = %v, want %v", got, want)
	}
	for _, required := range []string{"read-only tools", "do not implement it", "submit_plan", "Execute plan", "why", "how", "Execution graph", "depends_on", "Mermaid", "Plan mode has no `todo` tool", "converts that graph into `todo`"} {
		if !strings.Contains(planModeInstructions, required) {
			t.Fatalf("plan mode instructions omit %q", required)
		}
	}
	planInstructions, planFingerprint := turnInstructions(true)
	_, normalFingerprint := turnInstructions(false)
	if planFingerprint == normalFingerprint || !strings.Contains(planInstructions, planModeInstructions) {
		t.Fatalf("plan instruction identity was not isolated from normal mode")
	}
}

func TestPlanModeModelRouteOverridesOnlyPlanTurns(t *testing.T) {
	cfg := config.Default()
	cfg.Agents.Plan = config.ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}
	runtime := &ProviderRuntime{cfg: cfg}
	normal := TurnRequest{Provider: "chatgpt", Model: "gpt-main", Reasoning: "medium"}
	if got := runtime.routeTurn(normal); got.Provider != normal.Provider || got.Model != normal.Model || got.Reasoning != normal.Reasoning {
		t.Fatalf("normal route changed: %#v", got)
	}
	plan := normal
	plan.PlanMode = true
	if got := runtime.routeTurn(plan); got.Provider != "grok" || got.Model != "grok-plan" || got.Reasoning != "high" {
		t.Fatalf("plan route = %#v", got)
	}
	runtime.cfg.Agents.Plan.Reasoning = ""
	if got := runtime.routeTurn(plan); got.Reasoning != normal.Reasoning {
		t.Fatalf("plan route did not inherit reasoning: %#v", got)
	}
}

type retryConfiguredDriver struct{ calls int }

type retryConfiguredError struct{}

func (retryConfiguredError) Error() string   { return "retry fixture" }
func (retryConfiguredError) Retryable() bool { return true }

func (d *retryConfiguredDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "retry-configured"}
}

func (d *retryConfiguredDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	d.calls++
	if d.calls <= 2 {
		return nil, retryConfiguredError{}
	}
	return hyprovider.NewSliceStream([]hyprovider.Event{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}), nil
}

func TestRetryProviderDriverUsesConfiguredAttemptCount(t *testing.T) {
	policy := config.Default().Retry
	policy.Enabled = true
	policy.MaxRetries = 2
	policy.BaseDelayDuration = 0
	driver := &retryConfiguredDriver{}

	retrying := retryProviderDriver(context.Background(), nil, "session", "run", "provider", policy, driver)
	stream, err := retrying.Stream(context.Background(), hyprovider.Request{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, recvErr := stream.Recv(); recvErr != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v error=%v", event, recvErr)
	}
	if driver.calls != 3 {
		t.Fatalf("physical calls=%d, want initial plus two retries", driver.calls)
	}
}

func TestTitleModelRouteIsIndependentFromPlanAndRecap(t *testing.T) {
	cfg := config.Default()
	cfg.Agents.Plan = config.ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}
	cfg.Agents.Recap = config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-recap", Reasoning: "low"}
	runtime := &ProviderRuntime{cfg: cfg}
	if initial := runtime.titleModelRouteSnapshot(); initial != (config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"}) {
		t.Fatalf("default title route = %#v", initial)
	}
	title := config.ModelRouteConfig{Provider: "grok", Model: "grok-title", Reasoning: "low"}
	runtime.UpdateModelRoute("title", "", title)
	if got := runtime.titleModelRouteSnapshot(); got != title {
		t.Fatalf("title route = %#v", got)
	}
	if runtime.cfg.Agents.Plan != cfg.Agents.Plan || runtime.cfg.Agents.Recap != cfg.Agents.Recap {
		t.Fatalf("title route changed other routes: plan=%#v recap=%#v", runtime.cfg.Agents.Plan, runtime.cfg.Agents.Recap)
	}
}

func TestRecapModelRouteIsIndependentFromPlan(t *testing.T) {
	cfg := config.Default()
	cfg.Agents.Plan = config.ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}
	runtime := &ProviderRuntime{cfg: cfg}
	if initial := runtime.recapModelRouteSnapshot(); initial != (config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"}) {
		t.Fatalf("default recap route = %#v", initial)
	}
	recapRoute := config.ModelRouteConfig{Provider: "deepseek", Model: "deepseek-v4-flash", Reasoning: "low"}
	runtime.UpdateModelRoute("recap", "", recapRoute)
	if got := runtime.recapModelRouteSnapshot(); got != recapRoute {
		t.Fatalf("recap route = %#v", got)
	}
	if runtime.cfg.Agents.Plan != cfg.Agents.Plan {
		t.Fatalf("recap route changed plan route: %#v", runtime.cfg.Agents.Plan)
	}
}

func TestModelMaxOutputTokensUsesConfiguredCatalogLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.LLMux["deepseek"] = config.LLMuxProviderConfig{Enabled: true, Models: []config.LLMuxModelConfig{{
		ID: "deepseek-v4-flash", Aliases: []string{"deepseek-v4"}, MaxOutputTokens: 384000,
	}}}
	runtime := &ProviderRuntime{cfg: cfg}
	if got := runtime.modelMaxOutputTokens("deepseek", "deepseek-v4"); got != 384000 {
		t.Fatalf("max output tokens = %d, want 384000", got)
	}
	if got := runtime.modelMaxOutputTokens("chatgpt", "gpt-5.6-sol"); got != 0 {
		t.Fatalf("subscription max output tokens = %d, want 0", got)
	}
	cfg.Providers.LLMux["opencode-zen"] = config.LLMuxProviderConfig{Enabled: true, Models: []config.LLMuxModelConfig{{ID: "gpt-test", MaxOutputTokens: 8192}}}
	if got := runtime.modelMaxOutputTokens("opencode", "gpt-test"); got != 8192 {
		t.Fatalf("legacy OpenCode max output tokens = %d, want 8192", got)
	}
}

func TestNormalizeGeneratedSessionTitle(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: "<title>Fix sidebar title updates</title>", want: "Fix sidebar title updates"},
		{raw: "<title>修复会话标题。</title>", want: "修复会话标题"},
		{raw: `"Generate concise titles!"`, want: "Generate concise titles"},
		{raw: "<title/>", want: ""},
		{raw: strings.Repeat("long ", 20), want: ""},
	} {
		if got := normalizeGeneratedSessionTitle(test.raw); got != test.want {
			t.Fatalf("normalize %q = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestRecapInputIncludesGoalAnswerAndOnlyOpenTodoItems(t *testing.T) {
	encoded, err := recapInput(recapGenerationRequest{
		Goal:   "Ship concise recap",
		Answer: "The implementation is complete with many details.",
		Todo: session.TodoList{Phases: []session.TodoPhase{{Title: "Work", Items: []session.TodoItem{
			{Content: "Run focused tests", Status: session.TodoInProgress},
			{Content: "Open PR", Status: session.TodoPending},
			{Content: "Inspect implementation", Status: session.TodoCompleted},
		}}}},
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"goal":"Ship concise recap"`) ||
		!strings.Contains(encoded, `"latest_answer":"The implementation is complete with many details."`) ||
		!strings.Contains(encoded, `"in_progress: Run focused tests"`) ||
		!strings.Contains(encoded, `"pending: Open PR"`) || strings.Contains(encoded, "Inspect implementation") {
		t.Fatalf("recap input = %s", encoded)
	}
}

func TestRecapInputBoundsLongAnswerByKeepingItsTail(t *testing.T) {
	encoded, err := recapInput(recapGenerationRequest{Goal: "goal", Answer: strings.Repeat("prefix ", 200) + "final outcome and next action"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 200 || !strings.Contains(encoded, "final outcome and next action") || !strings.Contains(encoded, "[truncated]") {
		t.Fatalf("bounded recap input (%d bytes) = %s", len(encoded), encoded)
	}
}

func TestCollectProviderTextReturnsOnlyGeneratedRecap(t *testing.T) {
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "Goal is complete. "},
		{Kind: hyprovider.EventTextDelta, Text: "Next: open the PR."},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	request := hyprovider.Request{Model: "model", Messages: []message.Message{message.NewText(message.RoleUser, "full answer")}}
	got, err := collectProviderText(context.Background(), driver, request, "recap")
	if err != nil || got != "Goal is complete. Next: open the PR." {
		t.Fatalf("generated recap = %q, %v", got, err)
	}
	if len(driver.requests) != 1 || driver.requests[0].Messages[0].Text != "full answer" {
		t.Fatalf("recap request = %#v", driver.requests)
	}
}

func TestRecapPromptRequiresAConcisePlainTextStatus(t *testing.T) {
	for _, requirement := range []string{"under 40 words", "plain text only", "single next action", "Do not repeat the full answer"} {
		if !strings.Contains(recapPrompt, requirement) {
			t.Fatalf("recap prompt omitted %q: %s", requirement, recapPrompt)
		}
	}
}

func TestMainRunWaitsForWorkspaceClaimInsteadOfFailing(t *testing.T) {
	calls := 0
	outcome, err := executeMainRunUntilAvailable(context.Background(), func() (agentservice.ExecutionOutcome, error) {
		calls++
		if calls == 1 {
			return agentservice.ExecutionOutcome{}, &agentservice.TaskExecutionUnavailableError{
				TaskID: "waiting-task",
				ResourceClaims: agentruntime.ResourceClaimDecision{
					Reason: agentruntime.ResourceClaimDeniedConflict,
					Conflicts: []agentruntime.ResourceClaim{{
						ID: "active-writer", ExpiresAt: time.Now().UTC().Add(time.Millisecond),
					}},
				},
			}
		}
		return agentservice.ExecutionOutcome{State: agentservice.ExecutionCompleted}, nil
	})
	if err != nil || outcome.State != agentservice.ExecutionCompleted || calls != 2 {
		t.Fatalf("outcome=%+v calls=%d error=%v", outcome, calls, err)
	}
}

func TestResolveLLMuxDriverUsesConfiguredModelAndStoredCredential(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, nil, nil)
	if _, err := authentication.SetAPIKey(ctx, "openai", "sk-test"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["openai"] = config.LLMuxProviderConfig{Enabled: true, Models: []config.LLMuxModelConfig{{
		ID: "gpt-test", ContextWindow: 128000, ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high",
	}}}
	runtime := &ProviderRuntime{cfg: cfg, auth: authentication}
	account, model, window, driver, err := runtime.resolveLLMuxDriverForAccount(ctx, "openai", "gpt-test", "low", "")
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "api-key" || model != "gpt-test" || window != 128000 || driver.Metadata().Name != "llmux:openai" {
		t.Fatalf("resolution = account:%+v model:%q window:%d metadata:%+v", account, model, window, driver.Metadata())
	}
}

type automationCoordinatorHost struct {
	*Service
	bound bool
}

func (h *automationCoordinatorHost) BindProviderEngine(engine hyagent.Engine) hyagent.Engine {
	h.bound = true
	return h.Service.BindProviderEngine(engine)
}

func TestAutomationEngineUsesSharedHostCoordinator(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	cfg := config.Default()
	hostService := NewService(ctx, cfg)
	defer hostService.Shutdown(ctx)
	host := &automationCoordinatorHost{Service: hostService}
	runtime := &ProviderRuntime{cfg: cfg, coding: coding, host: host}
	metadata := map[string]string{
		"session_id":         "automation-session",
		"automation_kind":    "security_scan",
		"automation_version": "v1",
	}
	run, err := coding.StartRunWithMetadata(ctx, "inspect the workspace", metadata, agentservice.RunExecutionPolicy{AgentVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	request := TurnRequest{
		SessionID: "automation-session", Prompt: "inspect the workspace", Provider: "test", Model: "model", Reasoning: "high",
		DisableSubagents: true,
		automation: &automationTurn{
			Kind: "security_scan", Version: "v1", WorkspaceRoot: workspace, Instructions: "Inspect the workspace without changing it.",
			AllowedTools: map[string]bool{agentservice.ToolReadFile: true},
		},
	}
	_, engine, err := runtime.buildAutomationRun(ctx, request, run, "account", "model", 128_000, metadataOnlyDriver{})
	if err != nil {
		t.Fatal(err)
	}
	if !host.bound {
		t.Fatal("automation engine bypassed the shared host coordinator")
	}
	driver, ok := engine.Tools.Driver(agentservice.ToolReadFile)
	if !ok {
		t.Fatalf("automation engine omitted %s", agentservice.ToolReadFile)
	}
	governed, ok := driver.(*governedAgentTool)
	if !ok || governed.host != hostService || governed.approvalGate == nil {
		t.Fatalf("automation tool is not bound to shared approval coordinator: %#v", driver)
	}
}

func TestAutomationSessionIsMaterializedAndArchived(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	host := NewService(ctx, config.Default())
	host.AttachDurable(sessions, nil)
	defer host.Shutdown(ctx)
	runtime := &ProviderRuntime{host: host}
	request := TurnRequest{
		SessionID: "security:scan:worker", Provider: "test", Model: "model", Reasoning: "high",
	}
	if err := runtime.ensureAutomationSession(ctx, request, "security_audit"); err != nil {
		t.Fatal(err)
	}
	created, err := sessions.LoadSession(ctx, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "security_audit" || created.ProviderID != "test" || created.ModelID != "model" {
		t.Fatalf("automation session = %+v", created)
	}
	var archived bool
	if err := store.DB().QueryRowContext(ctx, `SELECT archived FROM session_ui_state WHERE session_id=?`, request.SessionID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("automation session was not archived")
	}

	if _, err := sessions.Ensure(ctx, session.Session{ID: "user-session", Title: "User session"}); err != nil {
		t.Fatal(err)
	}
	request.SessionID = "user-session"
	if err := runtime.ensureAutomationSession(ctx, request, "security_audit"); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COALESCE((SELECT archived FROM session_ui_state WHERE session_id='user-session'),0)`).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived {
		t.Fatal("existing user session was archived")
	}
}

func TestSecurityExecutionErrorPreservesBatchFailures(t *testing.T) {
	err := securityExecutionError(&tool.BatchExecutionError{Failures: []tool.CallExecutionError{
		{CallID: "read-1", Err: errors.New("first failure")},
		{CallID: "read-2", Err: errors.New("second failure")},
	}})
	if message := err.Error(); !strings.Contains(message, "read-1") || !strings.Contains(message, "first failure") ||
		!strings.Contains(message, "read-2") || !strings.Contains(message, "second failure") {
		t.Fatalf("batch error lost call details: %q", message)
	}
}
