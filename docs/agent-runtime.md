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
6. Desktop reconnect and session-selection projections load the Azem run
   aggregate and latest execution binding only. They must not call
   `DurableBackend.LoadExecution` or hash continuations on the IPC command
   sequencer (GPUI-003).

## Single and Team modes

Explicit TUI/API requests default to single mode (`defaults.agent_mode`). The
native composer selects Vibe or Fusion from `agents.workflow`. Team mode preserves the
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

### Vibe dispatch

The native workflow setting admits `agent_mode=vibe`; the legacy
`single` + `vibeMode=true` request is normalized to the same durable mode.
The director has read-only workspace tools and the five Vibe control tools;
fast/good workers use the existing governed subagent scheduler. `vibe_wait`
returns when any watched run settles, while ordinary subagent Query retains
wait-all semantics. Subscribe to state changes before reading snapshots so a
completion cannot be lost between observation and waiting.
Vibe policy belongs to the fingerprinted root instructions. Switching Vibe,
Fusion, or ordinary/Plan mode rebuilds context once from durable evidence, and
legacy private Vibe policy is not replayed into the new mode. This intentionally
changes the static cache prefix at a mode switch; repeated same-mode turns keep
it stable. Existing worker resume and background delivery use the shared runtime.

### Fusion handoffs

Fusion exposes one `sidekick` tool to the lead instead of arbitrary subagent
spawning. The lead plans, reads evidence, and reviews; the configured Sidekick
implements and verifies through the existing governed worker runtime. Each handoff
waits in the foreground, retains approvals and durable tool records, and is
cancelled when the parent tool wait is cancelled. The Sidekick cannot delegate.
Both models retain ordinary per-provider/model usage accounting; Fusion does not
promise a fixed cost reduction.

The display projection treats Fusion as one conversation. `fusionHost` forwards
child thinking, progress, reports and actual tools into the parent timeline with
source metadata, while suppressing worker lifecycle events. Child text remains
commentary in the parent projection so only the lead can finish the conversation.
Handoffs retain their actual prompt and result. Tool records retain their original
child run/call identity; only display IDs are namespaced. Selection and reconnect
identify Fusion through the durable parent `sidekick` call, hide worker rows, and
interleave child prose from the retained execution transcript with actual tools.
Display-only prose keeps the parent call's anchor without renumbering durable
blocks; resumed context before the current handoff is not repeated. Private system,
hook and compaction messages are excluded. Failures and approval/control routing
keep their original identities. Child prose and tool records never become the
lead's activated skills or tool-continuity context. Both histories and cached
prompt prefixes remain independent across handoffs.

Only `agent_mode=fusion` resolves the Sidekick route or exposes its tool and
read-only lead policy. Turning it off restores ordinary tools, even if the saved
Sidekick route is unavailable. Fusion policy belongs to the current root
instructions and their fingerprint, not replayed private-hook history. Switching
modes rebuilds model context from the durable conversation and tool evidence;
legacy checkpoints containing private Fusion policy rebuild once as well.
This intentionally changes the cache prefix on a mode switch. Repeated turns in
the same mode keep their prefix stable; ordinary sessions keep their existing
root instructions. Stored conversation and tool records are retained.

The Sidekick's stable identity includes the session, workspace, provider, account,
exact model and reasoning. Subsequent handoffs (including later user turns and
restored sessions) resume its full conversation, including tool call/result pairs
and provider state. Deterministic archives expand from verified session artifacts;
fresh public system instructions replace previous public system messages. Private
hook/deadline messages retain their original history positions; current notices
append beside the new handoff so a changing countdown does not rewrite the cache
prefix. Initial and resumed workers use the same canonical workspace path.
Other subagent resume paths retain their existing behavior. Changing the Sidekick route or account starts
an independent context. Unreadable or incomplete transcripts fail explicitly.
An explicit provider permission rejection ends the child as failed and returns
its reason to the lead. It must not park the child for unknown-attempt
reconciliation or automatically repeat the rejected handoff.


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

Live user guidance is also a durable Todo mutation boundary. Before acting on
newly added deliverables, the main agent appends each distinct item to an
existing phase using the latest revision. Withdrawn open work is cancelled;
only an explicitly erased non-current item is removed. The main instructions
and Todo tool definition carry the same rule, so Guide cannot silently add
untracked work or leave superseded work pending. This intentionally changes the
static provider prefix and tool schema once; subsequent turns preserve the new
prefix and message order for cache reuse (TODO-002).

Todo goals, phase labels, and item titles follow the current user's language, including explicit language requests. They describe concise outcomes; command sequences, file inventories, and host verification reminders belong in execution details rather than display titles. The same schema guidance applies to `init` items and `append` content. Existing durable labels are preserved, and required verification checks remain unchanged.

`current-work-verification` accepts equivalent evidence from governed dedicated
tools. In particular, one completed `coding.gofmt` result per required path
satisfies a deterministic `gofmt -d` check when its recorded post-format SHA
matches the current work revision. `changed=false` is an observation rather
than a mutation: it does not move the mutation high-water or make already-run
checks stale. This prevents a valid model final answer from triggering another
provider turn solely because the guard failed to recognize dedicated formatter
evidence (VERIF-002).

A shell check may also be wrapped in a single literal `eval '…'` argument.
The verifier unwraps that exact form before comparing the command and working
directory; it never evaluates shell text. Multiple arguments, quote
concatenation, dynamic double-quoted wrappers, extra statements, failed tools,
and records before the mutation boundary do not gain verification credit.

JavaScript verification is owned by the nearest valid `package.json`, not an
ancestor Go file that embeds the frontend. With a declared package manager,
available scripts select build checks for CSS, test checks for JS/TS test
files, and typecheck (or build) checks for other JS/TS sources. Go sources and
non-JavaScript embedded assets retain their Go package checks. A successful
scoped Vitest command can satisfy a frontend test check only in the required
working directory, after the current mutation boundary, and when it covers
every touched test file in that project. Unrelated tests, omitted directories,
and shell commands that mask failures are not equivalent evidence.

Main and resumed-main runs publish a final answer only after all output
guardrails allow completion and the canonical turn is persisted. Guard retries
remain private continuations of that run; rejected candidates are not shown as
final answers. This preserves the Todo, child-completion, and verification
gates rather than disabling them to avoid visible restarts (VERIF-003).

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
Live approvals wait on the current execution and continue that same execution
after the decision. They must not suspend and rebuild the main run merely to
approve a tool. The durable approval decision still survives process restart.

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

Request admission and terminal usage aggregation read only cache epoch,
checkpoint generation and the usage snapshot through `LoadProviderMeteringState`.
They must not hydrate provider history, transcript blocks or tool payloads.
The query reads current values for each call; it does not cache stale epochs or
relax integrity checks in ordinary history readers.

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

Workspace revision hashes stream complete source/data files within the existing
8 MiB aggregate evidence budget. The 1 MiB inline text preview limit does not
cap hashing. Media keeps its separate 32 MiB budget and rooted file access;
missing or over-budget evidence still blocks verification explicitly.

## Run controls

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
- TTSR evaluates configured text stream rules and reinjects one durable
  interruption according to repeat and context policy.
- Main turns with a known wall-clock deadline enqueue one private wrap-up
  steer during the last 90 seconds, capped at 20% of the remaining budget when
  the engine is bound. It asks the model to save required outputs, preserve
  passing work, stop optional optimization, and complete necessary checks.
  The control is checked before model calls and during reasoning; continuing
  reasoning can be interrupted through the same safe stream boundary as loop
  guards. It does not interrupt final prose, change the hard deadline, or bypass
  verification. Unbounded turns and independent child budgets keep their
  existing behavior. The reminder appends once to the private message tail;
  static instructions and earlier messages retain their cache prefixes.
- Loop detection also recognizes four exact repetitions of a short 8–64-word
  suffix with at least four distinct words, using a bounded 4096-byte tail.
  The existing minimum generated length and long-block/paragraph checks remain.
- Loop guards and interrupting TTSR rules close text-only generation at the next
  stream event boundary and record an aborted attempt before continuing the same
  run. They do not wait for a stalled provider to finish. Tool deltas/calls defer
  the interruption to the normal boundary so partial calls and provider-side
  effects remain intact. Ordinary user steering stays boundary-based. An early
  close leaves physical request usage unknown unless the provider reported it.
- Rejected text/reasoning loops use the existing discard policy for the next
  model request. Original attempt events remain durable, as do earlier messages
  and tool results; tool-call loops keep their existing context. After three
  retries, the fourth detection records a terminal control, settles through the
  same safe abort boundary, and fails in `BeforeModelCall` before opening another
  physical attempt. Throwing from the streaming `OnEvent` hook at exhaustion
  would leave an unsettled model attempt and mask the loop failure with
  reconciliation (LOOP-002). Provider errors and genuinely unknown effects still
  retain their normal reconciliation semantics.
- Prewalk and Plan YOLO translate an approved plan into execution context.
  Vibe owns persistent fast/good coding workers and does not expose ordinary
  workspace mutation tools to its director.
- The model-facing Hub combines peer messaging, background jobs, supervised
  processes, and waits. Parked agents revive on addressed messages without
  creating a second scheduler.

`ask` is a general single-agent interactive tool. Plan mode adds
`submit_plan`; it does not own a second question implementation. Headless
clients without a responder terminate the waiting context explicitly.

## Coding tool runtime

`internal/agent` owns the built-in read, write, Hashline edit, glob, grep,
format, test, diff, shell, background-job, and memory drivers. Shell execution
runs in a governed local runtime with policy and concurrency limits; `hub`
exposes background job wait/cancel/list operations, peer messaging and scoped
native process supervision. Native AST, LSP, Python, CDP, DAP, desktop and
media drivers share existing tool governance and durable result projection;
see [native tool coverage](native-tools.md) for prerequisites and limits.
Default workers receive these tools through both config validation and the
capability intersection. Vibe/Fusion directors retain their workflow-specific
tool boundaries. The new inventory changes the tool-schema fingerprint once;
stable subsequent turns preserve the prefix.

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

Python verification uses touched project files for syntax checks and unittest
modules identified by their imports. It never imposes Azem-specific test paths
on unrelated workspaces. Interrupted tool records remain unsuccessful in the UI.

The `replace` driver requests an explicit source read starting at line 1. Default read-file structure summaries are for model navigation and cannot provide replacement text or Hashline anchors; they must not be mistaken for a missing file. The bounded-read and stale-tag protections still apply.

A host verification retry remains part of the same user task. After satisfying the missing checks, the model must report the original request, implementation and verified evidence from the entire run; it must not present only the retry as a new task or deny the earlier work.

### Search snapshots and successive Hashline edits

Search snapshots still hash the entire matched file, but request only one source
line for the tag instead of rendering and serializing the full file. Successful
Hashline edits return the new header and up to 20 numbered source lines near the
first change (at most 8 KiB). This context is omitted if a concurrent write changed
the observed tag; stale hashes remain rejected. Compact-diff recovery stops before
this source context so source lines never become synthetic changes.

### Verification before final Todo completion

The last `todo done` performs the existing deterministic evidence checks before
changing the Todo revision. Missing or failed checks leave that item in progress
and return the specific checks to finish. Main and Team tools share this rule.
`todo verify` is read-only and uses the same gate to return `verification.ready`
and missing-check/error details without changing the Todo revision. A successful
`done` that leaves one open item also includes this preview, so required commands
arrive before final completion. Preview failure does not undo that successful
Todo mutation; final `done` still rechecks and rejects missing evidence.
Run each listed command verbatim in a separate `coding.shell` call with its
listed directory and environment. Appended `echo`, combined checks and alternate
package managers do not prove the selected check. Exit status is already recorded.

Successful native `coding.read_file` results with structured `kind=directory`
are directory listings, not file-byte observations. Keep their tool records and
listing content, but do not add the directory to file revision hashing. The same
classification applies when deriving evidence from older records that already
contain a directory observation with `limit_exceeded`. Ordinary file errors,
unknown/malformed result kinds, writes and full-file hash limits remain enforced.
No stored history is rewritten to repair the classification (VERIF-009).

Readback freshness follows the observed file's last mutation and current SHA;
an unrelated file edit does not invalidate it. Mutations without file observations
remain a conservative global boundary. Command checks retain the global mutation
boundary and exact matching. These tool/prompt updates change the static prefix
once; no per-call dynamic instructions are inserted into that prefix.

The implementation prompt asks for an early runnable version and measured tests,
then completion once the requested behavior and required checks pass. It does
not lower reasoning depth, relax requirements, or extend deadlines.

Hashline's schema presents a concrete valid patch, with operation grammar outside
the delimiters. Syntax rejection states that no files changed and preserves the
current tags for retry. Inverted ranges explicitly direct insertion to gap
locators. Stale tags still require a new read; parsing never guesses corrected
ranges or silently accepts a different edit.

The final-output guard still checks freshness, but successful current evidence
does not request another test run. Evidence hydration omits ModelHistory and
assistant blocks and selects only the relevant runs’ tool records in SQLite
before allocating or decoding their payloads; full transcript
loading retains its existing integrity validation.

### On-demand history retrieval

New sends and restored Team runs build their input from canonical messages,
current model history and the existing archive/checkpoint. They do not perform
keyword history recall or query-based memory/recap injection before admission.
The IPC receipt therefore does not wait for optional recall.

Explicit composer conversation mentions are the exception: a user-selected
`@[title](azem-session:id)` reference loads the source Recap (when available)
and recent visible prose on its active branch before accepting the message.
Sources must belong to the current project and cannot be current, archived,
missing or empty sessions. Up to four distinct references are allowed. Retrieval
has a two-second deadline, examines at most 64 recent prose blocks, and retains
the latest three user exchanges with a 4,000-character per-message and
16,000-character per-source excerpt budget, marking omissions explicitly.
It does not hydrate tool payloads or provider checkpoints, generate a new summary,
or recursively copy references held by the source. The immutable JSON snapshot
is saved in the user block's `data.sessionReferences` alongside the visible
title. Provider assembly and canonical replay include it as historical user
data; instructions and permissions still come from the current task.

Main and Team agents can explicitly call `context.search_history` when prior
context is missing. It is a read-only, current-session tool with a focused query,
eight-result limit, `history_retrieval_tokens` budget, 16 KiB content cap and a
two-second cancellable deadline. Search failures are tool errors, not hidden
startup delays. Source primary-key lookups retain canonical-ID, source-state,
private-artifact and session checks. Artifact bodies remain accessible through
`context.read_artifact`. Existing memory management and archive persistence are
unchanged; this change adds no background retrieval or summary-generation job.

Regression coverage: `TestHistorySearchLongSessionUsesBoundedLookup`,
`TestHistoryToolIsExplicitAndSessionScoped`,
`TestTurnAdmissionDoesNotSearchLongHistory`, and `TestProviderHistoryRecallIsLazy`
(first model request has no recalled payload; an explicit tool call returns it).
