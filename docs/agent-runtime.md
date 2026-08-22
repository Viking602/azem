# Agent Runtime

Last verified: 2026-08-15

Azem executes every conversation turn through the Venat agent framework
(`github.com/Viking602/venat` in `go.mod`). `internal/agent` wraps Venat's
durable runner and worker APIs behind `agent.Service`; `internal/app` rebuilds
a transient provider engine for each turn and binds it to the durable run.
`go.mod` is authoritative for the framework version; final validation runs with
`GOWORK=off` (AGENTS.md RUNTIME-001).

## Venat integration

`agent.NewService` (`internal/agent/service.go`) constructs the production
runner with `venat.NewProduction(api.Config{StoreProvider, PolicyEngine})`.
The store provider is the SQLite adapter set in `internal/store/sqlite`; the
policy engine is Azem's `ApprovalPolicy`. The main agent registers as profile
`azem-main` with role `coding`; each subagent type registers on demand as
`azem-subagent-<type>`.

Division of ownership:

- Venat owns runs, tasks, leases, admission, resource claims, retries,
  approvals, resume tokens, and action attempts. Azem persists all of them
  through the SQLite store adapters (`docs/persistence.md`).
- Azem owns providers, tools, sessions, context management, and UI projection.
- Venat is the single retry owner. Configured `retry` maps to
  `api.RetryPolicy{MaxAttempts: MaxRetries + 1, Backoff, MaxBackoff}` on run
  start (`ProviderRuntime.Start`); llmux drivers pin
  `sdk.RetryPolicy{MaxAttempts: 1}` so providers never retry internally.
- Main and subagent definitions declare `ToolMode: api.ToolModeParallel`
  (`agentDefinitionForSpec`), so Venat dispatches whole tool batches in
  parallel (CONCURRENCY-001). Shell execution and subagent scheduling still
  enforce their own concurrency limits, and skill-activation batches remain
  sequential inside Venat.

## Engine construction per turn

Durable coordination and transient execution are separate objects:

1. `ProviderRuntime.Start` (`internal/app/provider_runtime.go`) resolves the
   provider driver, then calls `agent.Service.StartRunWithMetadata`. That
   creates a `hyworker.SingleRunner` (runner + `hyworker.AgentWorker` with the
   10-minute `defaultRunLeaseTTL` + `hyworker.StandardAdmissionController`)
   and starts the durable run with the budget, retry policy, and resource
   claims from `RunExecutionPolicy`. Agent ID, agent version, and governance
   are persisted in run metadata (`azem.single_agent_*` keys) so a later
   process can rebuild the same coordinator.
2. `buildSingleRun` rebuilds the transient side: workspace tool drivers,
   Todo/plan/context-artifact/MCP/subagent tools, Skills, instructions, the
   semantic `ContextManager`, and the immutable static-identity hash. It wraps
   the provider driver with `budgetedProviderDriver` and stores an immutable
   `singleRunManifest` (version 2: provider, account, model, reasoning, active
   skills, plan state, `StaticIdentity`, budgets, `StartedAt`) in run metadata
   for resume.
3. `materializeAgentDefinition` deploys the content-hashed agent definition
   through `hyworker.DefinitionDeployment`, which persists
   `agent_definition_snapshots` rows (schema 18).
4. `agent.Service.ExecuteRun` binds the engine and stream sink to the
   coordinator. The lease is acquired only at this point; the
   `OnLeaseAcquired` observer copies `EnvelopeID`, `LeaseID`, `TaskVersion`,
   and `HolderID` into the tracked run so governed tools can prove
   lease-scoped authorization.

## Single and Team modes

Single mode is the default (`defaults.agent_mode`). Team mode
(`AgentMode == "team"`) runs through `agent.Service.StartTeamWithID`
(`internal/agent/team.go`): a `worker.TeamRunner` executes the `multiagent`
team `coding-team` with four classes — planner, implementer, reviewer,
reporter — driven by the replay-safe `CodingScheduler`
(`internal/agent/scheduler.go`), which permits one revision loop between
reviewer and implementer. `agents.team.max_concurrency` (default 2) and
`agents.team.max_ticks` (default 12) bound the runner. Only classes whose
tools can mutate the workspace receive workspace resource claims
(`codingClassMayMutateWorkspace`). Team runs are marked with run metadata
`team=true`, a `workspace_anchor`, and provider/account bindings so recovery
can route them to `ResumeTeam`.

## Subagent scheduling

`subagentRuntime` (`internal/app/subagent_runtime.go`) schedules children
outside Venat's admission controller but gives each child its own durable
Venat run (`StartRunWithMetadata` with agent ID `azem-subagent-<type>`).
Configuration lives under `agents.subagents` (`internal/config/config.go`):

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

## Durable runs, tasks, and leases

Every run owns a root task; execution requires a task lease with the
10-minute `defaultRunLeaseTTL`. Lease persistence uses compare-and-swap
versioning in `internal/store/sqlite/stores_governance.go`
(`AcquireWithExpectedVersion`, `ExtendLease`, `ReleaseExpiredLease`); the
`leases` table's active-slot unique index guarantees one active lease per
task. Schema 18 adds the control-plane tables `agent_definition_snapshots`,
`admission_reservations`, and `resource_claims`
(`internal/store/sqlite/migrations.go`). Only a process that first acquires
the exclusive recovery fence may expire leases (RUNTIME-002,
`docs/recovery.md`).

## Admission and resource claims

`hyworker.StandardAdmissionController` persists admission reservations before
execution. Workspace mutation is serialized through exclusive resource claims
keyed `azem:workspace-write:<canonical-root>` (`internal/app/
workspace_claims.go`); the root is symlink-resolved and case-folded on macOS
and Windows. The main run requests the claim unless `workspace.allow_write`
is false and `workspace.shell_policy` is `deny`
(`topLevelWorkspaceWriteClaims`). Subagents with worktree isolation or
read-only tools skip the claim.

A denied claim is not a failure. `executeMainRunUntilAvailable`
(`internal/app/provider_execution.go`) retries
`hyworker.TaskExecutionUnavailableError` after `resourceClaimRetryDelay`
(bounded between 100 ms and 1 s by the earliest conflicting claim expiry);
the subagent execute loop waits the same way. Neither path persists a raw
"resource claims denied" terminal block (RUNTIME-002).

## Run resume

`ProviderRuntime.ResumeRun` (`internal/app/provider_resume.go`) rebuilds a
single-agent engine around the durable run:

1. Terminal and `reconcile_required` runs are left alone.
2. The session named by run metadata must still own the run
   (`projection.LastRunID == runID`); otherwise the run is marked for
   reconciliation through `RequireRunReconciliation`.
3. The immutable `singleRunManifest` restores provider/account/model/
   reasoning, active skills, plan state, budgets, and `StaticIdentity`.
   A missing or invalid manifest also forces reconciliation.
4. `buildSingleRun` recomputes the static identity; a mismatch surfaces as
   `errResumeProfileChanged`, which suspends the run via
   `agent.Service.ReleaseRun` and marks it `reconcile_required` instead of
   silently executing with a different profile.
5. Consumed budget is restored: `ProviderRunTotalTokens` seeds the token
   budget, elapsed wall clock is subtracted, and an already-exhausted budget
   terminalizes the run explicitly (`terminalizeRecoveredBudget`) rather than
   re-executing it.

`ResumeRecoveredRun` routes `team=true` metadata to `ResumeTeam`, which
validates the stored `workspace_anchor` and provider account binding before
resuming the `TeamRunner` checkpoint.

Session checkpoint ownership is separate from run resumption: run model
history is persisted through `session.SaveRunCheckpoint`, which rejects stale
writers (`ErrRunCheckpointStale`) so a superseded checkpoint can never
overwrite a newer one; `session.CompleteTurn` finalizes the durable turn
(CONTEXT-003 governs adopting a newer durable revision instead of failing).

## Usage persistence

`meteredProviderDriver` (`internal/app/provider_metering.go`) wraps every main
provider driver and persists one `ProviderRequestFact` per request into the
`provider_requests` table before and after streaming, including token and
cache counters. Team and compaction requests report through their own usage
reporters. `ProviderRunTotalTokens` aggregates the facts per run for budget
restore. Venat's own usage store is served by `AppendUsage`/`QueryUsage` in
`stores_governance.go`; aggregate query limits follow the current Venat
contract and must not revert to the old semantics (RUNTIME-001).

## Budgets

Hard budgets terminate; the soft budget only advises.

- Main run: `agents.main.max_tokens`, `max_tool_calls`, `max_wall_clock`
  (defaults 0 = unbounded) flow into `api.TaskBudget` and the run governance
  snapshot. `budgetedProviderDriver` checks cumulative provider-reported
  usage between requests — the request in flight may finish, the next one is
  refused with `hyagent.ErrBudgetExhausted`. Compaction requests share the
  same `providerUsageBudget`.
- Subagents: `agents.subagents.budget.max_tokens`, `max_tool_calls`,
  `max_turns`, `max_wall_clock` (defaults 0 = unbounded) bound each child.
- `agents.subagents.budget.soft_requests` (default 200, with
  `soft_request_notice` true) is advisory only: `advisoryBudgetDriver`
  injects one private wrap-up reminder before the next provider request and
  never cancels or fails the child (SUBAGENT-002).

Budget failures are wrapped with configuration hints
(`increase agents.main.max_tokens ...`) before they reach the UI.

## Verification

```bash
go test ./internal/agent ./internal/app ./internal/store/sqlite ./internal/config
```

Guarded coverage includes parallel tool dispatch
(`TestAgentDefinitionUsesParallelToolDispatch`), workspace-claim wait/retry
(`TestMainRunWaitsForWorkspaceClaimInsteadOfFailing`), recursive completion at
concurrency one, unbounded-concurrency updates, advisory budgets, combined
usage budgets, and resume manifest/budget restoration in
`provider_runtime_test.go` and `subagent_runtime_test.go`.
