# Recovery

Last verified: 2026-08-31

This guide covers the runtime recovery contract: which process may recover,
what crash recovery restores, how interrupted work is represented, and which
failures pause instead of fail. Storage-level rules (paths, backups, schema
upgrades) live in `docs/persistence.md`; the runtime pieces documented here
are the invariants behind AGENTS.md rows RUNTIME-002, TOOL-002, SUBAGENT-001,
and CONTEXT-003.

## Process-lifetime recovery fence

Every process acquires `azem.db.runtime.lock` next to the database during
bootstrap (`sqlitestore.AcquireRecoveryFence`, called from
`bootstrapAssembly.build` in `internal/app/bootstrap.go`). The fence
(`internal/store/sqlite/recovery_fence_unix.go`, `flock`-based) has two
modes:

- The first live process takes the lock exclusively. A dirty or missing marker
  runs crash recovery before downgrading to a shared lock (`FinishRecovery`,
  called through `Service.finishRuntimeRecovery`). A clean last shutdown skips
  the recovery scan, marks the new process dirty, and immediately takes the
  shared runtime lock. Only the last clean closer may publish the clean marker;
  a crash therefore always leaves a recovery-required marker.
- Every later process (a daemon for another project, for example) waits for that
  boundary and holds only a shared lock. `AcquireRecoveryFence` returns
  `shouldRecover == false`, so it never expires leases, interrupts
  Subagents, or quarantines provider requests owned by another live process
  (RUNTIME-002). If the exclusive owner exits before publishing a completed
  recovery, one waiter takes over recovery instead of accepting a partial
  boundary.

Session navigation between projects must not emit `SessionEnd`; the single
renderer detaches and background work owned by the previous daemon keeps
running.
Closing a GPUI renderer asks an idle workspace daemon to stop; an active run
keeps the daemon alive. Explicit desktop Stop cancels main plus children.
`azem daemon stop` refuses an active main run unless `--include-active` is
supplied. Venat's durable runtime is execution state inside that daemon, not a
second daemon/session owner.


## Crash recovery sequence

Recovery runs only under the exclusive process fence:

1. `bootstrapAssembly.build` resolves the destination database path without
   migrating it and acquires the fence before any SQLite relocation/open.
2. SQLite opens, backs up an existing older schema, and migrates through
   schema 27.
3. `PrepareRecovery` runs before `agent.NewService` constructs
   `durable.Runtime`. It expires dead legacy leases/resource claims and
   quarantines incomplete legacy action attempts/provider requests.
4. `recovery.Service.RecoverPrepared` scans non-terminal application runs and
   pending approvals without repeating preparation. `ClassifyRunRecovery`
   validates each v1 binding, sealed manifest, profile hash, and persisted
   execution state.
5. Non-terminal v0.15 runs without a v1 binding are marked
   `reconcile_required`. Their durable history remains intact; recovery does
   not fabricate a continuation or execute an old pending tool.
6. Incomplete `subagent_runs` become `interrupted`. The `beforeResume` hook
   marks running tool timeline rows interrupted before any engine is rebuilt.
7. Parent/Team runs resume first. A pending v1 binding starts, a runnable
   continuation resumes from its exact checkpoint, a suspended approval stays
   waiting, a terminal execution replays its recorded result, and an unknown
   model/tool attempt stays paused for explicit reconciliation. App-owned
   Subagent recovery then requeues the existing child binding.
8. Reconcile attempts and pending approvals enter the recovery summary.
   `Service.emitRecoveryState` publishes the durable projection; only then
   does `FinishRecovery` downgrade the fence to a shared lifetime lock.

The UI must show attention-required state. It must not convert recovery to
success or start a replacement session.

## Side-effect reconciliation

A run becomes `reconcile_required` when automatic continuation cannot prove
the exact v1 identity or effect outcome:

- no v1 binding exists for a non-terminal legacy run,
- session/run/agent/kind/segment ownership or the sealed manifest is invalid,
- provider/account/model/reasoning/Skill/tool/workspace/static identity
  changed,
- a Team workspace/account binding changed, or
- a durable claim found an in-flight model/tool attempt whose response may
  have been lost.

Unknown attempts surface execution ID, operation ID, kind, attempt
number/version, checkpoint sequence, and continuation phase. The user resolves
that exact record through `ActionReconcileAttempt`; the receipt is idempotent.
Only then may the durable runtime resume. A succeeded attempt remains
anti-replay evidence when a model emits a fresh call ID for the same completed
non-idempotent input.

Approval decisions use a separate exact target. `ActionResolveApproval`
persists the decision, then calls `ResumeRecoveredRunAtOperation` with the
approval's durable `ActionID`. App-owned child controllers choose the latest
durable decision in the current model-complete checkpoint. Neither path falls
back to an ambiguous operation when parallel tool calls exist.

## Tool timeline and continuity evidence

Tool calls are durable records (`session_tool_records`). Two mechanisms keep
them honest across interruptions:

- `session.InterruptRunningToolRecordsForRun` marks still-running records
  `interrupted`. It runs during crash recovery (before resume) and at the end
  of every provider turn (`runProviderTurn`), so a cancelled or failed turn
  never leaves records spinning (TOOL-001 convergence).
- On resume, `toolContinuityMessages` (`internal/app/tool_timeline.go`)
  injects two private messages into the model context: a fixed policy text
  and a JSON evidence payload (bounded to the latest 128 tool facts and 512
  file facts). Each completed file observation is re-hashed against the
  current workspace: a matching SHA-256 becomes `verified_unchanged`, a
  mismatch becomes `stale`, and an unreadable path stays `unverified` with an
  error code. The policy forbids replaying completed side effects and forbids
  re-reading `verified_unchanged` paths merely because execution resumed;
  `stale` paths may be re-read only when their contents are needed. All
  embedded names are untrusted data, never instructions.

Private continuity and compaction messages are never projected as visible
assistant prose or re-seeded on resume (CONTEXT-002).

## Background subagent continuation

Foreground `subagent.spawn` waits inside the parent tool call, but a wait
boundary releases only the parent call — it never cancels the child
(SUBAGENT-001, `internal/app/subagent_tools.go`):

- When `agents.subagents.await_timeout` is `0`/`0s` (the default), the parent
  tool waits until the foreground child completes. A positive window is not a
  child timeout: when it elapses, `detachAfterWaitWindow` calls
  `subagentRuntime.continueInBackground` with `safeOnly=true`. Read-only
  children and worktree-isolated writers (`subagentMayRunInBackground`) detach
  and keep running durably in the background; unsafe shared-workspace writers
  keep the parent waiting.
- When the parent tool context itself ends, `detachAfterParentWait` detaches
  unconditionally; the tool result carries
  `continuing_in_background: true` and an explicit not-cancelled warning.
- Children end through `subagent.kill`, an explicit include-children
  stop, application shutdown, or `agents.subagents.idle_timeout` (default
  `5m`; `0s` disables) when a running child stays silent. A process restart marks incomplete children
  `interrupted`; `subagentRuntime.recoverInterrupted` requeues restart-
  interrupted children that still have a durable child run.

The desktop stop action is an explicit include-children stop.
`CancelActiveWithChildren(true)` cancels the parent’s subagents and signals the
active durable coordinator synchronously, so the explicit cancellation cause
cannot lose a race with provider completion. It then returns immediately while
bounded durable cleanup continues in the background, so a slow tool cannot
freeze the stop button (TOOL-002, SUBAGENT-006). A parent-wait expiry or TUI
parent-only choice still detaches safe children instead of cancelling them
(SUBAGENT-001).

## Team resume

Team state remains application-owned. Recovery reloads the persisted
orchestration state, validates workspace/provider/account identity, and calls
`orchestration.Drive` one tick at a time. Every unfinished role dispatch
rebuilds its own direct engine and resumes its independent v1 execution
binding. There is no `TeamRunner` checkpoint or legacy worker deployment.

## Suspension

Suspension is a pause, not a failure. `agent.Service.ExecuteRun` reports
`ExecutionSuspended` for `durable.ErrSuspended` and
`SuspensionReconciliation` for `durable.ErrReconcileRequired`.
`runProviderTurn`/Team/Subagent controllers publish recovery state without a
terminal success/failure event. A waiting approval remains suspended until its
durable decision names the exact operation to resume.

## Failure surfaces

| Outcome | Trigger | Surface |
|---|---|---|
| `run_failed` | Provider/tool infrastructure error, exhausted hard budget, empty Team answer, failed terminal projection | Terminal `run_failed` event plus persisted failed assistant/error state |
| `reconcile_required` | Legacy non-terminal run, invalid/missing binding or manifest, changed identity/workspace, unknown model/tool effect | Run stays paused with exact recovery facts until an explicit decision |
| Suspended (paused) | Durable approval wait or explicit runtime suspension | `recovery_state` with state `suspended`; no terminal event |
| Explicitly terminalized | Recovered run whose restored budget is exhausted | Failed block plus `hyagent.ErrBudgetExhausted` |
| Not a failure | Real transient resource-claim conflict | Wait/retry loop; never a terminal provider block |

UI projection pressure must never become a provider error (UI-002), and
cancellation is excluded from unread-notification marking (UI-004).

## Verification

```bash
GOWORK=off go test ./internal/store/sqlite ./internal/recovery ./internal/agent ./internal/app ./internal/session
```

Guarded coverage includes recovery-fence ownership/takeover, preparation before
runtime construction, v1 pending/suspended/terminal/unknown paths, v0.15
reconciliation without replay, exact approval resume, interrupted tool rows,
Subagent detach/requeue/cancel, one-tick Team resume, asynchronous explicit
stop, and stale context-checkpoint adoption.
