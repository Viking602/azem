# Agent Runtime

Last verified: 2026-08-29

Azem executes conversation, Team-role, Subagent, and security-automation model
work through Venat v0.16.1 (`github.com/Viking602/venat` in `go.mod`).
`internal/app` builds a direct `agent.Engine`; `internal/agent` binds that
transient engine to one stable `durable.Runtime` execution and projects the
result into Azem's application state. `go.mod` is authoritative; framework
verification always runs with `GOWORK=off` (AGENTS.md RUNTIME-001).

## Venat integration

`agent.NewService` (`internal/agent/service.go`) constructs exactly one
`durable.Runtime` for the service lifetime over
`internal/store/sqlite.DurableBackend`. No legacy `Runner`, worker deployment,
or compatibility facade participates in execution.

Division of ownership:

- Azem owns policy, application runs/tasks, approvals and resume tokens,
  resource claims, provider routing, tools, Team/Subagent orchestration,
  sessions, persistence composition, and UI projection.
- Venat durable owns one execution's immutable spec, lease, continuation
  checkpoint, model/tool effect attempts, settlement receipts, and exact
  start/resume/reconcile mechanics.
- The versioned `agent_execution_bindings` row connects those domains. Its
  immutable manifest identifies session, run, agent, execution kind, segment,
  provider/account/model, reasoning, Skills, tool schema, workspace, budgets,
  and static prompt identity.
- `retryProviderDriver` is the one pre-stream transport retry owner. llmux
  internal retries remain disabled. The durable interceptor records each model
  and tool effect independently, so retry cannot bypass response-loss or
  unknown-attempt handling.
- Main, Team-role, Subagent, and automation engines set
  `tool.ModeParallel`. Shell and Subagent schedulers retain their independent
  concurrency limits; Skill activation dependencies remain serialized.

This is a clean ownership cut, not a compatibility layer: Azem never asks
Venat to own sessions, Team/Subagent lifecycle, policy, or UI state, and Azem
never reimplements Venat's execution lease/checkpoint/attempt settlement. A
v0.15 non-terminal row has no implicit v1 continuation; only a new versioned
binding may enter the v0.16 runtime.


## Engine construction per execution

Durable coordination and transient engine construction stay separate:

1. `ProviderRuntime.Start` resolves the provider/account/model and calls
   `agent.Service.StartRunWithMetadata`. That transaction creates the Azem
   run, root task, envelope, and a pending v1 execution binding before any
   provider or tool effect.
2. `buildSingleRun` assembles workspace tools, Todo/plan/context-artifact/MCP/
   Subagent tools, Skills, instructions, context management, provider retry,
   and output guardrails, then builds a direct `agent.Engine`.
3. `SealExecutionProfile` stores the complete immutable manifest and its hash
   before `durable.Runtime.StartStream`. Completed Skill activations restore
   resource-read authorization without changing the advertised Skill tools or
   provider prefix. A model with provider-default reasoning stores the stable
   identity `provider-default` while sending no fabricated reasoning value on
   the provider wire.
4. `agent.Service.ExecuteRun` uses `StartStream` for a pending binding and
   `ResumeStreamWithOptions` for every persisted execution. Venat fences the
   lease, saves a continuation before effects, and returns unknown effects as
   `ErrReconcileRequired`; Azem never guesses or retries such an effect.
5. The transient `agent.Sink` may replay frames after restart. Session blocks,
   tool timeline rows, usage, and terminal projection therefore deduplicate by
   durable execution/operation identity rather than treating frames as an
   exactly-once log.

## Single and Team modes

Single mode is the default (`defaults.agent_mode`). Team mode preserves the
deterministic planner → implementer → reviewer → at most one revision →
reviewer → reporter policy in `internal/agent/scheduler.go`.

Team scheduling is application-owned. `team_runtime.go` calls
`orchestration.Drive` with `MaxTicks: 1` and the persisted orchestration state,
saves the returned state after every tick, then recomputes the next pure
scheduler batch. Every dispatch has a globally stable ID and an independent
v1 durable execution binding. A role's `agent.Result.Failure` becomes
orchestration outcome data; only an infrastructure error aborts the drive.
Azem remains authoritative for Team state, handoffs, role prompts/Skills/tool
allowlists, workspace policy, approval/UI state, concurrency, and the
configured tick ceiling.

## Subagent scheduling

`subagentRuntime` (`internal/app/subagent_runtime.go`) remains the
application-owned child lifecycle and scheduler. Each child worker is a
direct `agent.Engine` with its own v1 durable execution
(`StartRunWithMetadata` using a stable Subagent agent ID). The
`subagent_runs` store—not Venat—is authoritative for queueing, foreground/
background state, watchdog activity, peer delivery, completion, cancellation,
and wake batching. `subagent.spawn`, `subagent.get_output`, and
`subagent.kill` remain app tools; they are not replaced by synchronous
`agent.NewAgentTool` calls. Configuration lives under `agents.subagents`:

- `enabled` default true; `max_depth` default 2, `-1` unlimited, 0 disables
  delegation entirely. Spawn tools are re-exposed to children at `Depth+1`
  while `enabledForDepth` allows it.
- `max_concurrency` default 32; 0 means unbounded.
- `await_timeout` default `0s` waits until the foreground child completes. A
  positive duration is only the parent tool-call wait window, not a child
  execution timeout (see `docs/recovery.md` for detach semantics). `-1` is
  rejected.
- `idle_timeout` default `5m` cancels a running child that has no thinking,
  output, or tool activity for that window. Zero disables the watchdog. Open
  tools, including approval waits, and live `coding.shell` processes are not
  cancelled. Compaction and explicit wait summaries reset the clock. Empty
  thinking/text frames and elapsed-time UI ticks do not.
- States: `initializing → queued → running → completed/failed/cancelled/
  interrupted`, with `cancelling` as the transitional kill state.
- `evidenceStatus` is a projection, not a lifecycle transition.
  `provisional` means durable work exists without same-revision passing
  verification; `verified` requires an accepted disposition plus that passing
  result; `stale` means a newer work revision invalidated the terminal result.
  `subagentStateEvent` includes the value in the existing `agent_state`
  payload, and both desktop and TUI treat unknown/legacy values as absent.

`canStartLocked` enforces the concurrency limit with one exception: when the
scheduler is full, a running parent (depth > 0) may admit exactly one
re-entrant child so synchronous recursive delegation cannot deadlock
(SUBAGENT-002). Siblings stay queued until that child ends. Children are
cancelled by `subagent.kill`, an explicit include-children stop, application
shutdown (SUBAGENT-001), or an optional configured `idle_timeout`
(SUBAGENT-005).

Every active child serializes its own durable state writes without holding the
global scheduler mutex. Cancellation uses a bounded lifecycle-independent
context, supersedes any in-flight `queued`/`running` save, and precedes the
terminal save. Shutdown therefore cannot resurrect a cancelled child through a
late startup or heartbeat write (SUBAGENT-009).

The main agent prompt requires review or verification that gates later work
to stay foreground. If any child of the current run is still non-terminal
when the parent tries to finish, `pending-background-children` (`internal/app/
provider_subagent_guardrail.go`) keeps injecting a host retry listing those
task IDs and requiring `subagent.get_output`. The parent cannot claim the
work is independent and finish. The guardrail never cancels children;
`idle_timeout` may cancel a silent child (SUBAGENT-004, SUBAGENT-005).

The session Todo completion guard belongs to the parent run. A child still
executes `current-work-verification` for its own mutations and evidence, but it
does not retry on the parent's open Todo item. Otherwise a foreground child
that already produced a valid result could never become terminal, while the
parent simultaneously waits for that terminal state (SUBAGENT-008).

`current-work-verification` accepts equivalent evidence from governed dedicated
tools. In particular, one completed `coding.gofmt` result per required path
satisfies a deterministic `gofmt -d` check when its recorded post-format SHA
matches the current work revision. `changed=false` is an observation rather
than a mutation: it does not move the mutation high-water or make already-run
checks stale. This prevents a valid model final answer from triggering another
provider turn solely because the guard failed to recognize dedicated formatter
evidence (VERIF-002).

After the one allowed evidence retry, an `uncertain` or `fail` result remains
persisted in the verification store and blocks a successful terminal run.
The guardrail reason is surfaced as a host-owned run failure; it is never
appended, substituted, or assigned `RoleAssistant`, so the model-authored
stream cannot be mistaken for verified output. The desktop strips the two
exact historical notice suffixes when projecting legacy durable blocks and
drops a notice-only assistant block (VERIF-001).

When the session is idle, `AutoWakePending` collects every background child
that is terminal, not cancelled, and not yet `CompletionDelivered`, then
starts one wake turn. A successful parent `run_finished` marks that run's
terminal children delivered first, so a waited-out review does not start a
second stream. The wake user block keeps `kind=user` so it remains in
model context, but sets `state=subagent_wake` and structured `data.tasks`
(UI-013). Wake turns set `DisableSubagents` to prevent a spawn loop.

## Durable executions and resource claims

Every app run has a root task and one or more versioned execution bindings.
Each binding points to one stable Venat execution ID. Schema 27 persists the
immutable execution spec, current lease and continuation, provider/tool
attempts, settlement receipts, and the Azem binding. A single
`agent.Service` owns one `durable.Runtime` until shutdown; it does not create a
second runtime per turn or per resume.

Azem's application resource claims remain separate from Venat's execution
lease. Main sessions and shared-workspace Subagents do not take a global
workspace-write claim (RUNTIME-003). Any remaining real transient claim
conflict returns `TaskExecutionUnavailableError`; main and child loops wait and
retry instead of persisting the conflict as a provider failure. Only the
exclusive startup recovery owner expires orphaned legacy claims.
## Run resume

Startup and explicit resume classify the v1 binding before rebuilding an
engine:

1. A non-terminal v0.15 run with no v1 binding is marked
   `reconcile_required`. Its old lease, provider request, approval, and action
   evidence remains available, but Azem does not synthesize a continuation or
   execute a pending legacy tool.
2. Pending v1 bindings start from their sealed manifest. Running or suspended
   bindings load the persisted Venat execution and exact continuation.
   Terminal bindings replay their recorded result into the idempotent Azem
   projection.
3. The manifest version, ownership fields, profile hash, provider/account/
   model, reasoning, Skill/tool identities, workspace anchor, prompt identity,
   and session ownership must still match. Missing or changed facts move the
   run to reconciliation.
4. A suspended approval remains waiting. After a durable decision, main and
   recovered approval actions call `ResumeRunAtOperation`; app-owned child
   controllers select the latest durable decision and resume that exact
   operation. Parallel tool calls are never resumed through an ambiguous
   generic target.
5. Claiming a crashed execution atomically turns any in-flight model/tool
   attempt into `unknown`. The run stays `reconcile_required` until the user
   resolves the exact attempt number/version; no provider or tool replay occurs
   first.

Team recovery reloads application-owned orchestration state, then rebuilds an
independent engine for each unfinished dispatch. Subagent recovery first marks
the lifecycle row interrupted; rebuilding the parent runtime requeues its
existing durable child execution without replacing `subagent_runs`.

Session checkpoint ownership is separate from run resumption: run model
history is persisted through `session.SaveRunCheckpoint`, which rejects stale
writers (`ErrRunCheckpointStale`) so a superseded checkpoint can never
overwrite a newer one; `session.CompleteTurn` finalizes the durable turn
(CONTEXT-003 governs adopting a newer durable revision instead of failing).

## Usage persistence

`meteredProviderDriver` (`internal/app/provider_metering.go`) wraps main,
Team-role, Subagent, and automation provider drivers and persists one
`ProviderRequestFact` per request before and after streaming, including token
and cache counters. `ProviderRunTotalTokens` aggregates those facts for budget
restore. Provider usage facts remain an Azem session concern; Venat durable
attempt payloads are execution-settlement evidence and are not a second usage
ledger.

## Budgets

Hard budgets terminate; the soft budget only advises.

- Main run: `agents.main.max_tokens`, `max_tool_calls`, `max_wall_clock`
  (defaults 0 = unbounded) flow into `agentruntime.TaskBudget`, governance,
  and the immutable execution manifest. `budgetedProviderDriver` checks
  cumulative provider-reported usage between requests—the request in flight
  may finish, and the next is refused with `hyagent.ErrBudgetExhausted`.
  Compaction requests share the same `providerUsageBudget`.
- Subagents: `agents.subagents.budget.max_tokens`, `max_tool_calls`,
  `max_turns`, `max_wall_clock` (defaults 0 = unbounded) bound each child.
- `agents.subagents.budget.soft_requests` (default 200, with
  `soft_request_notice` true) is advisory only: `advisoryBudgetDriver`
  injects one private wrap-up reminder before the next provider request and
  never cancels or fails the child (SUBAGENT-002).

Budget failures are wrapped with configuration hints
(`increase agents.main.max_tokens ...`) before they reach the UI.

## OMP-compatible run controls

Azem keeps every mode on the same application run, v1 durable execution, and
durable session:

- Steering, prewalk, loop guards, peer delivery, and queued follow-ups enter
  one Azem FIFO. `BeforeModelCall` injects reserved messages; the boundary
  observer acknowledges them only after the continuation is durable.
  Undelivered reservations are released on suspension/error. A write or
  external attempt with uncertain outcome blocks in reconciliation rather
  than being discarded to honor a steer.
- Goal state, checkpoint/rewind metadata, Todo, and provider-neutral loop-guard
  decisions persist with the session. Goal completion never bypasses unfinished
  Todo or verification guardrails.
- Advisor observes independently and may emit bounded inline advice without
  becoming the execution owner.
- TTSR evaluates configured text/AST stream rules and reinjects one durable
  interruption according to repeat and context policy.
- Prewalk and Plan YOLO translate an approved plan into execution context.
  Vibe owns persistent fast/good read-only workers and does not expose ordinary
  workspace mutation tools to its director.
- The model-facing Hub combines peer messaging, background jobs, supervised
  processes, and waits. Parked agents revive on addressed messages without
  creating a second scheduler.

`ask` is a general single-agent interactive tool. Plan mode adds
`submit_plan`; it does not own a second question implementation. Headless
clients without a responder terminate the waiting context explicitly.

## Coding tool runtime

`internal/agent` owns OMP-compatible read, write, Hashline edit, glob, grep,
AST, LSP, DAP, eval, browser, computer, web search, GitHub, SSH, jobs, media,
and memory drivers. Bun bridges are bounded subprocess protocols, not agent
loops. Python, JavaScript, Ruby, and Julia eval kernels are persistent per
session/language when the host runtime is available.

Custom extension file fallbacks are consulted only after an ordinary local
write/delete fails with `EACCES`, `EPERM`, or `EROFS`. Archive, SQLite,
unresolved-symlink, non-permission, and out-of-workspace mutations never reach
that seam.

## Background security runs

Security scans use `ProviderRuntime` through an internal automation profile,
not `Service.StartConfiguredTurn`. The profile creates an application-owned
run and v1 durable execution, uses the same engine coordinator and approval
hook as main/Team/Subagent work, binds a snapshot-rooted read-only tool set,
and retains the security store, deadline, native submission tools, and UI
events as application-owned state and does not set `TaskBudget` Token/tool-call
ceilings. Audit children are limited to bundled security roles,
cannot nest, and receive no project Skills/MCP/hooks. The scan coordinator owns
the persisted absolute deadline and convergence bounds. Native Desktop starts
therefore cannot be terminated by Azem's partial provider-usage accounting.
The scan deliberately does not claim the process-wide foreground run slot,
persist a hidden conversation session, or inherit live guidance.

App shutdown cancels the execution context, which leaves the scan blocked and
resumable while retaining its immutable source snapshot and worker artifacts.
Explicit scan cancellation terminalizes the scan and removes the snapshot.
Deep Scan reloads succeeded audit/reducer artifacts and advances worker
sequence after restart rather than replaying accepted work. See
[Security scanning](security-scanning.md).

## Verification

```bash
GOWORK=off go test ./internal/agent ./internal/app ./internal/recovery ./internal/store/sqlite ./internal/config
```

Guarded coverage includes direct-engine executable-spec/parallel-tool
preservation (`TestDirectAgentBuildPreservesExecutableSpecAndSeparatesRequestBudget`),
workspace-claim wait/retry, recursive completion at concurrency one,
unbounded-concurrency updates, advisory/combined usage budgets, immutable
binding/profile validation, exact approval resume, v0.15 reconciliation, and
restart restoration in the agent, app, recovery, and SQLite suites.
