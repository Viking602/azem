# Recovery

Last verified: 2026-08-15

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

- The first live process takes the lock exclusively, runs crash recovery,
  then downgrades to a shared lock (`FinishRecovery`, called through
  `Service.finishRuntimeRecovery` after recovery completes) and holds it for
  its whole lifetime.
- Every later process (a second project window, for example) waits for that
  boundary and holds only a shared lock. `AcquireRecoveryFence` returns
  `shouldRecover == false`, so it never expires leases, interrupts
  Subagents, or quarantines provider requests owned by another live process
  (RUNTIME-002). If the exclusive owner exits before publishing a completed
  recovery, one waiter takes over recovery instead of accepting a partial
  boundary.

Session navigation between projects must not emit `SessionEnd`; background
work owned by another window keeps running.

## Crash recovery sequence

`recovery.Service.Recover` (`internal/recovery/service.go`) runs only when the
process holds the exclusive fence:

1. `PrepareRecovery` expires active leases (they belonged to a dead process)
   and quarantines incomplete action attempts and provider requests so
   unknown side effects are never replayed blindly.
2. `SubagentInterrupter.InterruptIncomplete` marks non-terminal subagent
   projections `interrupted` with the reason `interrupted by process restart`.
3. A store scan collects every non-terminal run, whether it has durable team
   state, and its pending approval tokens.
4. The `beforeResume` hook (installed by `bootstrapAssembly.attachRecovery`)
   calls `session.InterruptRunningToolRecordsForRun` for each discovered run
   so the durable tool timeline shows honest `interrupted` states before
   anything re-executes.
5. Each run passes through `runner.Recover`; team runs go to
   `ProviderRuntime.ResumeTeam`, single runs with any status other than
   `reconcile_required` go to `ProviderRuntime.ResumeRun`.
6. Pending reconcile attempts are listed into the recovery summary.

`Service.emitRecoveryState` (`internal/app/app.go`) then publishes one
`recovery_state` event with state `attention_required` carrying the pending
approvals, unknown side effects, and counters (expired leases, quarantined
attempts, interrupted subagents). The UI must show this state; it must not
convert it to success or start a replacement session.

## Side-effect reconciliation

A run becomes `reconcile_required` (`agent.Service.RequireRunReconciliation`)
when it cannot be trusted to continue automatically:

- the durable run lost its session ownership metadata,
- the owning session projection no longer names it as `LastRunID`,
- the immutable `singleRunManifest` is missing or invalid,
- the rebuilt execution profile no longer matches the recorded static
  identity (`errResumeProfileChanged`), or
- a team run's `workspace_anchor` or provider account binding is missing or
  points at a different workspace.

Quarantined action attempts with unknown external outcomes surface as
"Unknown side effect" notices. `ActionReconcileAttempt`
(`internal/app/actions_runtime.go`) records the user's decision through
`ResolveReconcileAttempt`, resumes the run via `ResumeRecoveredRun`, and
emits `recovery_state` with state `reconciled`. Succeeded attempts also act
as an anti-replay ledger when a resumed model re-emits a completed
non-idempotent call.

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

The desktop stop action is an explicit include-children stop and stays
asynchronous: `CancelActiveWithChildren(true)` cancels the parent’s
subagents, then delivers the cancellation to the durable coordinator and
returns immediately, so a slow tool cleanup cannot freeze the stop button
(TOOL-002, SUBAGENT-006). A parent-wait expiry or TUI parent-only choice
still detaches safe children instead of cancelling them (SUBAGENT-001).

## Team resume

Team runs persist their scheduler state in the durable team-state store, and
their run metadata records `team=true`, the `workspace_anchor`, the provider
account, and the original prompt. `ProviderRuntime.ResumeTeam` validates the
anchor against the current workspace and requires the account binding; either
mismatch parks the run in `reconcile_required`. A valid team run reloads the
session projection and Todo list, then continues from the `TeamRunner`
checkpoint via `SpawnResumedProviderTeam` without blocking startup.

## Suspension

Suspension is a pause, not a failure. When `ExecuteRun` returns
`hyworker.ExecutionSuspended` (or a team run returns
`multiagent.ErrExecutionSuspended`), `runProviderTurn` and
`finishProviderTeam` emit `EventRecoveryState` (`recovery_state`) with state
`suspended` and the suspension kind/reason, then return without a terminal
event. `agent.Service.ReleaseRun` uses the same mechanism to durably suspend
a rebuilt run whose immutable profile cannot safely execute.

## Failure surfaces

| Outcome | Trigger | Surface |
|---|---|---|
| `run_failed` | Provider/tool error, exhausted hard budget, empty team answer, event-backlog delivery failure | Terminal `run_failed` event plus a persisted failed assistant/error block |
| `reconcile_required` | Lost session ownership, invalid manifest, changed execution profile, foreign workspace anchor, unknown side effect | Run stays paused; `attention_required`/reconcile notices until the user decides |
| Suspended (paused) | `ExecutionSuspended`, profile-mismatch release | `recovery_state` with state `suspended`; run remains resumable |
| Explicitly terminalized | Recovered run whose restored budget is already exhausted | `terminalizeRecoveredBudget` persists a failed block and completes the run with `hyagent.ErrBudgetExhausted` |
| Not a failure | Denied workspace resource claim | Wait/retry loop (`executeMainRunUntilAvailable`); never a terminal block |

UI projection pressure must never become a provider error (UI-002), and
cancellation is excluded from unread-notification marking (UI-004).

## Verification

```bash
go test ./internal/store/sqlite ./internal/recovery ./internal/agent ./internal/app ./internal/session
```

Guarded coverage includes runtime-fence ownership and failed-owner takeover
(`recovery_fence` tests), interrupted tool record persistence, tool
continuity `verified_unchanged`/`stale` evidence, wait-window detachment and
restart requeue in `subagent_runtime_test.go`/`subagent_tools` tests,
asynchronous stop (`TestCancelActiveReturnsBeforeUncooperativeExecutionFinishes`),
and stale-checkpoint adoption (`TestTurnContextCompact*` in CONTEXT-003).
