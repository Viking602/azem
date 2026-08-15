# Azem Agent Guide

Last verified: 2026-08-15

## Scope

This file applies to the entire repository. It is the working entry point for
maintainers and coding agents: current risks, behavior that must not regress,
verification requirements, and the documentation index all live here.

Only record facts supported by source code, tests, runtime results, or current
GitHub state. New issues require evidence. Update this file when a recorded
state changes; do not use it for temporary task progress.

## Before Starting Work

1. Read this file and the source and tests directly related to the change.
2. Inspect the worktree and preserve existing user changes.
3. Find every caller before changing an exported symbol.
4. Reproduce defects before fixing their root cause. Never hide failures by
   swallowing errors, downgrading the database, or clearing state.
5. Run the narrowest verification that covers the changed path, then the
   relevant complete check before delivery.
6. Update the indexed documentation when behavior, configuration, persistence,
   external dependencies, or operating procedures change.

## Sources of Truth

| Topic | Authoritative source |
|---|---|
| Build commands | `Makefile`, `frontend/package.json` |
| Go dependencies and version | `go.mod`, `go.sum` |
| Runtime configuration | `internal/config/`, README Configuration section |
| SQLite runtime migrations | `internal/store/sqlite/migrations.go` |
| SQLC compile-time schema | `internal/store/sqlite/dbgen/schema.sql` |
| Agent and scheduler runtime | `internal/agent/`, `internal/app/`, Venat API |
| Sessions and durable timeline | `internal/session/`, `internal/app/tool_timeline.go` |
| Desktop bridge | `internal/desktop/`, `internal/desktop/termhost/`, `cmd/azem-gui/`, `frontend/src/bridge.ts` |
| GitHub PR capability | `internal/githubpr/`, `frontend/src/components/PullRequestPanel.tsx`, `frontend/src/components/PullRequestsPage.tsx` |
| TUI | `internal/tui/` |
| Executable prompts | `internal/app/prompts/`, `internal/agent/prompts/`, `internal/config/prompts/` |
| User entry documentation | `README.md` |
| Architecture constraints | `.sentrux/rules.toml` |

`internal/**/prompts/*.md` and `internal/skills/bundled/**/SKILL.md` are runtime
inputs, not ordinary documentation. Changing them changes product behavior and
requires matching tests.

## Known Issues and Regression Record

Status values:

- **Unresolved**: users or maintainers are still affected.
- **Fixed, guarded**: the root cause is fixed; the invariant and regression
  coverage must remain.
- **Documentation gap**: implementation exists but independent maintenance
  documentation is incomplete.

| ID | Status | Issue and impact | Evidence | Required handling |
|---|---|---|---|---|
| DB-001 | Fixed, guarded | A user database reached schema 18 while source only supported 17, causing packaged startup to fail with `database schema 18 is newer than supported schema 17`. | `internal/store/sqlite/migrations.go`, `migrations_test.go`, real schema 18 reopen verification. | Keep `schemaVersion == len(migrations)`. Preserve migration 18 control-plane stores, schema 19 project ownership, schema 20 semantic context stores, upgrade/reopen coverage, and rejection of unknown future schemas. |
| DB-002 | Fixed, guarded | Runtime migrations and the SQLC schema are separate definitions that must remain synchronized. | `internal/store/sqlite/migrations.go`, `internal/store/sqlite/dbgen/schema.sql`, `docs/persistence.md`, `docs/decisions/0001-schema-versioning.md`. | Keep both definitions, the persistence guide, and the schema ADR synchronized. Every schema change tests upgrade, reopen, and data retention. |
| PROJECT-001 | Fixed, guarded | The desktop process previously exposed one fallback workspace while session history was global, so direct app launches lost the real project name, branch, and PR context and rendered every session under the wrong project. | Schema 19 project/session ownership tables, desktop bootstrap restore test, session catalog test, multi-project Sidebar test. | Keep project catalog state in SQLite rather than `config.yaml`; preserve one immutable project owner per session; direct desktop launch restores the most recently opened valid project; cross-project sessions open with that project's workspace. |
| STREAM-001 | Fixed, guarded | Commentary, reasoning, and final answers were previously merged, hiding progress around tool calls or duplicating final output. | Provider stream, app event, session timeline, and frontend reducer `TextPhase` tests. | Preserve `commentary` and `final_answer` end to end; reasoning must not impersonate commentary; final output renders once. Unphased live text uses the same body-prose chrome as a final answer; do not paint a pending blue-dot card while the phase is unresolved. |
| PROVIDER-001 | Fixed, guarded | Azem previously hard-coded ChatGPT and Grok, so other model providers and arbitrary model IDs could not be configured or routed from the desktop. | `internal/provider/llmux`, generic runtime resolution tests, model-provider action/Bridge/store/settings tests. | Keep ChatGPT/Grok subscription IDs reserved; keep llmux API keys out of YAML and events; validate configured models; preserve one retry owner and typed stream mapping. |
| PROVIDER-002 | Fixed, guarded | DeepSeek's Anthropic-compatible usage reports uncached input and cache-read input separately, while llmux v0.2.4 retained the values without marking the cache counter as reported. Real hits therefore rendered as `Not reported`, and context occupancy excluded cached input. Azem also hoisted every late private system message into Anthropic's top-level system field, rewriting the long-conversation prefix on each turn and reducing real cache hits to about 9%. | `normalizeProviderUsage`, `TestDeepSeekStreamReportsInclusiveCacheUsage`, `TestAnthropicConversionKeepsLatePrivateSystemContextInMessageTail`, and live DeepSeek V4 Flash long-conversation verification. | Keep DeepSeek input usage inclusive of cache-read/cache-write counters, mark zero and non-zero cache reads as reported, leave unknown provider cache support unreported, hoist only leading Anthropic system messages, and preserve later trusted host context at its original message-tail position. |
| CONTEXT-001 | Fixed, guarded | Rolling summaries mixed durable task state with short-lived transcript evidence and could drift across repeated compactions. | SemanticStateV1 validation, schema 20 state/events/manifests, unified planner tests, Artifact V2 tests, Inspector projection tests. | Keep one new kernel across automatic/manual/resume paths; preserve latest three user turns exactly, stable provenance, atomic tool groups, transactional cursor/manifest activation, and explicit mandatory-budget failure. Do not restore legacy fallback. |
| CONTEXT-002 | Fixed, guarded | Long subagent reviews could spend millions of cumulative input tokens and then fail when a valid semantic checkpoint exceeded the old 8,192-token writer ceiling; reasoning could also consume that allowance and return truncated JSON. Persisted private checkpoints were projected as visible assistant prose and re-seeded on resume. | Failed `subagent_runs` records with `summary output requires ... configured limit allows 16384`, compaction retry/limit tests, and subagent projection/resume tests. | Keep the default semantic state budget at 32,768 tokens, allow configurations up to one quarter of the writer context window, keep generation headroom separate from the durable state budget, and never project or resume-seed private/compaction messages. Mandatory state that still cannot fit must fail explicitly. |
| CONTEXT-003 | Fixed, guarded | Automatic compaction could fail a healthy run with `semantic state source is stale: expected revision 0, current revision 1` when a later writer still submitted the original revision after another activation had already persisted revision 1. The previous in-memory revision bump was not enough: leftover background prepares and Compact/CompactTo activation still treated the durable conflict as a terminal Provider error. | Desktop Provider error `agent: compact history: session: run checkpoint source is stale...`, `TestTurnContextCompactToAdoptsDurableRevisionBeforeWriting`, `TestTurnContextCompactAdoptsDurableRevisionBeforeWriting`, and `TestTurnContextCompactToRecoversStalePreparedActivation`. | Reload the durable semantic checkpoint before preparing or retrying activation. Cancel leftover background prepares when live history is no longer a prefix of the prepared source. Do not fail the provider run when a newer durable revision already exists and can be adopted. |
| CONTEXT-004 | Fixed, guarded | Semantic compaction writers such as DeepSeek V4 Flash often wrap SemanticStateV1 in a Markdown JSON fence. Host validation required a bare JSON object, so a long run failed with `semantic writer returned non-JSON output` after one repair retry. | Desktop compact-history failure, `unwrapWholeJSONFence` in `context_rebuild.go`, and `TestNormalizeSemanticStateAcceptsWholeResponseJSONFence`. | Accept only raw JSON or one whole-response ` ``` ` / ` ```json ` fence, matching APPROVAL-001. Never extract JSON from surrounding prose, nested fences, or unsupported fence languages. |
| CONTEXT-005 | Fixed, guarded | Semantic compaction writers such as DeepSeek often emit `StateFactV1.sources` as a JSON string or string array instead of `EvidenceRefV1` objects. Strict unmarshal then failed a healthy long run with `json: cannot unmarshal string into Go struct field StateFactV1.objective.sources of type app.EvidenceRefV1`. | Desktop compact-history failure after 97 tools / 12m17s, `decodeEvidenceRefs` in `context_rebuild.go`, and `TestNormalizeSemanticStateAcceptsStringSourcesThatCannotUnmarshalIntoEvidenceRefV1`. | Normalize string, string-array, and single-object `sources` on every fact into `EvidenceRefV1`. Map `kind:id` / `kind:id#range` and alternate `id`/`uri`/`path`/`quote`/`ref`/`source` strings. Fail closed only for incompatible types. Keep CONTEXT-004 fence rules. Do not skip activation when mandatory state is still invalid. |
| RUNTIME-001 | Fixed, guarded | Venat upgrades changed Usage Store and durable control-plane contracts; old semantics ignored aggregate query limits. | `go.mod`, `internal/store/sqlite/stores_governance.go`, Venat contract tests. | Do not restore old Usage semantics. Verify the real module with `GOWORK=off`, and run upstream contract tests during framework upgrades. |
| RUNTIME-002 | Fixed, guarded | Opening another project window previously ran global crash recovery against the shared SQLite database, expired leases owned by a still-live Azem process, and made the original run fail with `stale task version` or become `reconcile_required`. A second main task also surfaced a transient workspace claim conflict as a terminal provider failure. | `internal/store/sqlite/recovery_fence*`, recovery-fence tests, `TestMainRunWaitsForWorkspaceClaimInsteadOfFailing`, and durable run/tool/subagent evidence from `run_ee2cd85db941608e69b4de4b`. | Every process holds the shared runtime fence for its complete lifetime. Only a process that first acquires the exclusive fence may prepare crash recovery, session navigation must not emit `SessionEnd`, and main/subagent tasks must wait and retry transient resource-claim conflicts instead of persisting raw failures. |
| CONCURRENCY-001 | Fixed, guarded | Agent definitions omitted `toolMode`, so Venat defaulted whole tool batches to sequential execution. Foreground subagent spawns and shell calls appeared queued even when their runtime limits had free capacity. | `agentDefinitionForSpec`, `TestAgentDefinitionUsesParallelToolDispatch`, Venat parallel dispatch tests. | Keep main and subagent definitions in parallel tool mode. Preserve the separate shell and subagent concurrency limits and Venat's sequential skill-activation safeguard. |
| SUBAGENT-001 | Fixed, guarded | Foreground subagents were cancelled when a ten-minute parent wait window elapsed or the parent tool context ended, so valid long investigations could not finish despite unbounded token, turn, and wall-clock budgets. | `subagentSpawnDriver`, `continueInBackground`, long-running lifecycle tests. | A wait window of `0`/`0s` waits until the foreground child completes. A positive window releases only the parent call. Safe work continues durably in the background; unsafe shared-workspace writes keep waiting. Cancel children through explicit kill, an explicit include-children stop, application shutdown, or an optional configured `idle_timeout` (SUBAGENT-005). |
| SUBAGENT-002 | Fixed, guarded | Subagent concurrency was fixed to a small positive value and recursive delegation was hard-disabled, unlike OMP's scalable task runtime. A naive recursive queue can also deadlock when a waiting parent holds the only slot. | Recursive-depth tool exposure, unbounded-concurrency update, advisory-budget, and concurrency-one recursive completion tests. | Keep default concurrency 32 with zero meaning unbounded; keep recursive depth 2 with `-1` unlimited and `0` disabled. Permit only one re-entrant child per waiting parent beyond a full limit. Soft request budgets may advise but must never cancel work. |
| SUBAGENT-003 | Fixed, guarded | The default foreground wait was a positive duration (`10m`), so a still-running foreground child released the parent tool and, when safe, continued in the background. Users who wanted the parent to wait while work stayed foreground had to pick a finite cap; `0` and `-1` were rejected. | Default `await_timeout: 0s`, `waitForForeground`, `TestForegroundWaitUntilCompleteKeepsParentOnZeroWindow`, Settings 「直到完成」. | Keep `0`/`0s` as wait-until-the-foreground-child-completes. A positive window still only releases the parent (SUBAGENT-001). Do not treat `-1` as a second unlimited sentinel. Do not cancel children when a finite window ends. |
| SUBAGENT-004 | Fixed, guarded | The main prompt allowed review and verification to run in the background, and the runtime never checked for still-running children when the parent produced a final answer. Slow review children therefore finished after the parent had already moved on; later auto-wake turns only injected raw English user text. | `pending-background-children` OutputGuardrail, `TestPendingBackgroundChildrenGuardrailPromptsOnce`, `TestBackgroundCompletionsBatchIntoOneWake`, and the main prompt contract. | Gated review/verify stays foreground or must be consumed with `subagent.get_output` before the parent ends. Prompt at most once per run; never cancel children. Batch every undelivered terminal background completion into one wake turn. |
| SUBAGENT-005 | Fixed, guarded | After a tool completed, a long architecture-review child could stay `运行中` for 20+ minutes with no thinking or output because the next provider stream has no idle watchdog (streaming HTTP uses zero timeout). The first watchdog defaulted to `0s` (disabled), so silent stalls lasted until the user cancelled. Live thinking could also exist while the card/drawer showed only 运行中: `inspect_agent` replaced newer deltas, and projection resync reloaded only the main session. | Desktop silent-stall screenshot, `TestIdleTimeoutCancelsSilentRunningSubagent`, `TestIdleTimeoutSkipsOpenToolAndResetsOnThinking`, `TestIdleTimeoutResetsOnTextAndToolAndIgnoresEmptyThinking`, Settings 「无响应自动取消」, side-chat thinking tests. | Keep `idle_timeout` default `5m`. Zero still disables. A positive window cancels only a running child with no thinking, text, or tool activity and no open tool. Empty thinking/text frames and elapsed UI ticks are not activity. Compaction and explicit wait summaries reset the clock. Running cards/drawers must show live thinking or the wait pill, not a bare 运行中 body. A same-session `session_loaded` with `state=refreshed` must keep `selectedAgentId` and `agentBlocks` so the follow-up `inspect_agent` can merge. |
| SUBAGENT-006 | Fixed, guarded | Desktop stop called `CancelActive(false)`, so a user-stopped parent detached still-running children into the background. An architecture-review child stayed `运行中` after the stop button. | Desktop collaboration roster after stop, `TestUserStopCancelsRunningSubagents`, ThreadSurface stop-with-children test. | Desktop stop is an explicit include-children stop. Parent-wait expiry and TUI “parent only” still must not cancel children (SUBAGENT-001). |
| SUBAGENT-007 | Fixed, guarded | The subagent runtime had three blocking amplifiers: `Cancel` held the global runtime lock while writing SQLite, so a slow store stalled frame handling and scheduling for every child; the lease/workspace 100 ms retry loop refreshed `lastVisibleAt` and re-saved the run on every spin, so a purely waiting child never triggered the idle watchdog and hammered the store; and live `agent_state` events were not coalescible, so streaming children flooded the event queue. | `TestIdleTimeoutStillFiresDuringRepeatedWaitHeartbeat`, `TestEventBrokerReplacesLiveAgentStateByStreamIdentity`, `TestEventBrokerKeepsDistinctAgentStateTransitionsOrdered`, `TestEventBrokerKeepsTerminalAgentStatesOrdered`. | Never hold `r.mu` across store writes in Cancel. A new explicit wait summary still resets the idle clock once (SUBAGENT-005), but repeating the identical heartbeat must not refresh `lastVisibleAt`, write the store, or emit state. The broker collapses only same-state live `agent_state` snapshots per stream identity; distinct lifecycle transitions and terminal states stay ordered and are never dropped. |
| TERM-001 | Fixed, guarded | The embedded terminal could hang the Bridge and the window exit path: `shutdown()` waited on `done` without bound while `cmd.Wait()` can stall on an uninterruptible child, `CloseAll` shut sessions down serially, PTY writes blocked forever when the shell stopped reading, `Create` held the host lock across fork/exec and could leak a session past a racing `CloseAll`, and the frontend rerendered every tab per output chunk with an unbounded backlog and unordered parallel writes. | `TestShutdownReturnsWithinBudgetWhenSessionNeverConverges`, `TestCloseForgetsSessionThatNeverConverges`, `TestWriteTimesOutInsteadOfHangingWhenShellStopsReading`, `TestCloseAllRacingCreateLeavesNoRunningSession`, frontend output-buffer/write-queue/roster-identity tests. | Every termhost wait stays bounded: close budgets, background bounded reap, parallel `CloseAll` under one overall budget, and non-converged sessions leave the roster with a log line. Writes fail with `ErrWriteTimeout`/`ErrWriteBusy` instead of blocking; fork/exec happens outside the host lock with a closed re-check. The renderer keeps `terminal_output` out of session metadata, frame-batches xterm writes with a bounded backlog, serializes one timed Bridge write per session, skips unchanged resizes, and removes a closed tab before the backend close converges. |
| TOOL-001 | Fixed, guarded | Queued tools were once rendered as running, making approval wait time appear as execution time and leaving later calls spinning. Idle announcements later reused that queued label even when no sibling was executing. Auto-review batches later sat in 排队中 instead of reviewing together. | `provider_execution.go`, `provider_approval.go`, `frontend/src/store.ts`, `Timeline.tsx`, lifecycle tests. | Preserve `queued -> awaiting_approval -> running -> completed/failed` for permission waits. Auto-review announcements emit `reviewing_approval` and start reviews in parallel. Pending review shows a shield and 审核中, never 排队中. Do not show unexecuted writes as file changes; converge every non-terminal tool when a run ends. |
| TOOL-002 | Fixed, guarded | The desktop stop action synchronously waited for Venat to finish cancelling the active durable run before returning through the Bridge. A slow MCP/tool cleanup therefore left the stop button and run visibly stuck. | `CancelActiveWithChildren`, `TestCancelActiveReturnsBeforeUncooperativeExecutionFinishes`, and real desktop stop verification. | Deliver the cancellation request to the durable coordinator asynchronously so the Bridge returns immediately; keep the coordinator as the owner of the terminal cancellation cause, then cancel the app-owned run context after durable cleanup converges. |
| APPROVAL-001 | Fixed, guarded | Configurable Anthropic-compatible approval models can ignore native response-schema options and return the decision as a Markdown JSON fence, which previously caused `Automatic review failed (parse)` and prevented an authorized action from running. | `internal/provider/codex/guardian_policy.go`, `reviewer.go`, provider reviewer and app automatic-approval tests. | Keep the explicit JSON-only output contract and strict fail-closed validation. Accept only raw JSON or one whole-response JSON fence; never extract a decision from surrounding prose or execute after an invalid review. |
| SKILL-001 | Fixed, guarded | Skill activation is run-scoped Venat memory, while durable transcripts keep the prior `hydaelyn_activate_skill` result. After a failed compact or a later turn, the model treated `check` as already active and called `hydaelyn_read_skill_resource`, failing immediately with `skill "check" is not active`. | Desktop retry 0s failures, `loadSessionActivatedSkills`, `TestProviderRuntimeReplaysActivatedSkillsOnLaterTurn`, and `TestProviderRuntimeDoesNotReplayDisabledOrDeletedSkills`. | Replay completed activations from session tool records into the next run's Skills set. Drop names that are no longer resolvable. A session that never activated the skill still fails closed. |
| TODO-001 | Fixed, guarded | Parallel Todo mutations shared one stale revision; a later `start` could win first, demote the actual current item to pending, and make its `done` fail. Completed work then appeared to update only at the end or was never persisted. | `todo_tool.go`, `TestTodoStartCannotReplaceCurrentItem`, `TestTodoConcurrentMutationsCannotSkipCurrentItem`, executable main prompt contract. | Keep global tool dispatch parallel, but issue exactly one Todo mutation after each completed item and await its snapshot. `start` must never replace another current item; `done` remains the only normal transition that completes and advances work. The main prompt requires a Todo snapshot before investigation or modification; trivial chat without workspace work is exempt. |
| UI-001 | Fixed, guarded | Todo, Subagents, active-thinking motion, and process groups once existed only on an integration branch and disappeared from the GUI branch. | Commit `c2e4030`, frontend components/styles, frontend regression tests. | Move these projections and views as one unit. Do not restore the old `AgentList` or port styling without the store/event projection. |
| EXT-001 | Fixed, guarded | The Extensions surface previously exposed only raw Skills, then loaded imported plugins directly from Codex source/cache paths and described the whole capability as “Codex plugins”; a populated startup catalog could also appear empty when its initial event preceded the frontend listener. | `internal/plugins`, `ActionListPlugins`, desktop `plugin_catalog` origin projection, Extensions UI, plugin copy/local-discovery and snapshot replay tests. | Keep all runtime roots under Azem `plugin-packages`; direct installs use `local`, Codex entries remain catalog-only until explicitly selected in `plugins.codex_imports`, and selected imports use staged copies under `codex`. A Codex outage must not invalidate existing selected copies. Preserve validated image-data projection, manifest validation, existing Skill/MCP/hook boundaries, default-denied Hooks, and explicit OAuth/App degraded states. |
| EXT-002 | Fixed, guarded | The desktop Extensions surface derived MCP totals only from plugin metadata and never projected the live MCP manager, so configured servers disappeared and the page looked empty when no plugin was installed. | `internal/app/mcp_settings.go`, `mcp_state` frontend projection, `ExtensionsSettings` tests, and real `Azem.app` lifecycle verification. | Keep MCP independent from plugin discovery. Preserve typed add/enable/reconnect/delete actions, atomic configuration writes, persistent deletion tombstones, secret-free snapshots, asynchronous lifecycle work, explicit empty states, and live manager tool removal when disabled or deleted. Built-in and plugin-owned services are deletable; Codex-only `computer-use` launchers must never enter the Azem MCP runtime. |
| EXT-003 | Fixed, guarded | Clicking 导入 on a visible Codex plugin such as kami left the row unchanged. The settings dialog hid the Bridge error, a missing wire `id` sent an empty target, and a later `codex plugin list` failure aborted the copy even when the local marketplace checkout or cache was present. | Desktop kami row `Codex · kami · 1.12.0`, `TestSetCodexPluginImportedUsesKnownCatalogWhenCodexListFails`, `TestDiscoverImportsFromMarketplaceCheckoutWhenCodexListFails`, and ExtensionsSettings missing-id import test. | Reconstruct `name@marketplace` when `id` is omitted. Surface import failures on the Extensions/settings surface. Copy from the live listing, the last known catalog, the Codex cache, or `.tmp/marketplaces/<marketplace>/plugins/<name>`. A Codex CLI outage must not block importing a package already shown as 可导入. |
| EXT-004 | Fixed, guarded | The Hooks trust confirm reused the MCP delete grid with a `<header>`/`<h3>` title, so 「信任插件 Hooks？」 wrapped into three poster-sized lines. Clicks on 信任并启用 could hit the backdrop or miss the squeezed heading, so trust never persisted and the overlay could also block plugin import. | Desktop Extensions Hooks screenshot, `TrustHooksDialog` now matching `DeletePluginDialog`, ExtensionsSettings trust-action/error tests, and SettingsDialog overlay host test. | Keep the confirm as a 420px delete-density dialog with one-line `h2`, warning copy, and a clickable primary button. Host it on the settings `<dialog>`. Emit `set_plugin_hooks_trusted` with `decision: "true"`. Surface save failures. Do not default `plugins.trust_hooks` to true. |
| EXT-005 | Fixed, guarded | Settings → Extensions → Hooks showed 0/0 and 「还没有 Hooks」 after restart until refresh; trusting plugin hooks left the switch off; individual hooks could not be disabled. Desktop `prime()` omitted `list_hooks`, there was no `HookCatalog()` readback, and a late `hook_catalog` was dropped once `lastSequence` moved ahead. | `Bridge.HookCatalog`, prime `ActionListHooks`, `hooks.disabled` / `set_hook_enabled`, `TestBootstrapEmitsHookSnapshotAndDirectReadback`, `TestSetPluginHooksTrustedPersistsAndReloads`, `TestSetHookEnabledPersistsAndDoesNotExecute`, SettingsDialog/ExtensionsSettings/store catalog tests. | Keep catalog replay and sequence-0 readback after subscribe. Persist trust through `plugins.trust_hooks` and reload runtime; never default it true. Keep per-hook deny-list dispatch skip fail-closed: untrusted plugin hooks do not run even when enabled. |
| UI-002 | Fixed, guarded | A slow desktop renderer could exceed the runtime event high-water mark and terminate an otherwise healthy provider run with `UI event backlog exceeded the safe limit`. | `internal/app/event_broker.go`, `event_broker_test.go`, desktop/TUI refresh actions, frontend streaming tests. | UI projection pressure must never become a provider error. Keep replaceable deltas coalesced by stream, lifecycle and approval events lossless, durable projection resync after compaction, bounded tool previews, and lightweight live rendering. |
| UI-003 | Fixed, guarded | Switching live assistant output to immediate Markdown removed the per-delta text reveal because existing paragraph nodes no longer remounted; the old plain-text reveal could not be restored without delaying Markdown syntax. Marking every block child and the last eight ranges then replayed fade/blur on already-written final-answer lines each time the source grew. | `StreamingMarkdown`, `liveRevealRanges`, `StreamingText`, Timeline live Markdown/reveal tests, and the no-block-in CSS assertions. | Parse Markdown on every coalesced frame, wrap and animate only the newest provider range, keep existing block nodes mounted, never apply enter motion to settled siblings, and honor both system and explicit reduced-motion settings. |
| UI-004 | Fixed, guarded | A conversation could finish after the user switched away without leaving any visible notification, so the completed result was easy to miss. | Cross-session terminal reducer tests, Sidebar unread-dot test, and `TestMarkSessionUnreadPersistsUntilResume`. | Only a tracked foreign main run that succeeds or fails marks its session unread. Persist the blue dot through `session_ui_state`, clear it on resume, and exclude cancellation and subagent terminal events. |
| UI-005 | Fixed, guarded | Opening another project intentionally started an isolated desktop runtime with `--new-window`, but that flag also disabled Wails single-instance handling and every child used the regular macOS activation policy, producing one extra Azem Dock application per project. | `desktopMacOptions`, `TestIndependentWindowDoesNotRegisterAnotherMacApplication`, process argv verification, and real packaged multi-project launch. | Keep the primary process regular and every isolated project/session window accessory on macOS. Preserve isolated runtimes so background work survives project navigation, while secondary windows must not register another Dock or Cmd-Tab application entry. |
| UI-006 | Fixed, guarded | Active file edits previously used the generic tool disclosure and exposed raw Hashline arguments, then changed shape only after the tool completed. | `pendingFileChangeSummaryForBlock`, the live-to-completed Timeline regression test, and real packaged desktop verification. | Keep queued and approval-bound writes out of file-change projections. Active edits may show exact planned totals only when the arguments are unambiguous, and must transition in place to the completed structured diff. |
| UI-007 | Fixed, guarded | The Subagent conversation drawer previously split one transcript across task, metadata, activity, and content panels, so it no longer read like the main conversation and completed work remained visually expanded. Live process trails could also collapse to a 处理中 summary while work was still running. | `AgentSideChat`, `Timeline.tsx`, the process-only drawer regression in `App.test.tsx`, and packaged desktop verification. | Keep Subagent user prompts and final answers in the same transcript typography as the main conversation. Active commentary and tool trails stay expanded and cannot fold to 处理中. Only completed tool/diff trails fold under 已处理; they start folded and may be reopened. A live run keeps the open commentary and count-row trail. After that turn ends, fold the whole tool trail and keep the final answer in the reading column. Expanding a large fold must not mount every tool body at once. Thinking-only or commentary-only groups keep the live thinking header (UI-016). |
| UI-011 | Fixed, guarded | The desktop search field previously filtered only a small in-memory command/session-title list, so settings and durable conversation content could not be found or opened. | SQLite global search tests and benchmark, CommandPalette debounce/race/navigation tests, Settings target test, Timeline sequence-focus test, and packaged desktop verification. | Keep settings/catalog search local and session content on the bounded SQLite FTS path. Never copy complete transcripts into frontend search state; preserve stale-response rejection, cross-project ownership, stable block-sequence focus, and direct projection readback after resume. |
| UI-012 | Fixed, guarded | Opening a long Subagent drawer while it streamed made the side chat stutter: every subagent delta recopied the main `blocks` array, folded process trails still mounted their children, and header preview updates re-rendered the whole transcript. Expanding a large completed fold later mounted every tool body at once. | `reduceEvents` reference-stability test, folded `ProcessFold` mount test, deferred expand tests, `AgentSideChat` transcript split, and UI-003/UI-007 regressions. | Keep collection identities stable unless that collection changed. Folded process trails must not mount `ProcessEntries`. Expanding a large fold first paints collapsed chip headers and must not mount every diff, Markdown, Thinking panel, or Subagent list at once. Opening a settled count row must not mount the 思考 paragraphs until that chip is opened. Parse Markdown on every coalesced frame, but reuse equal reveal-range identities. Keep the drawer transcript on its own store subscription. |
| UI-013 | Fixed, guarded | Background subagent completion auto-wake persisted a `Kind=user` prompt titled `You`, so the desktop rendered host delivery text as a user bubble and the model treated it as ordinary chat. | `userTurnBlock` `state=subagent_wake`, Timeline wake-notice test, and `TestUserTurnBlockMarksSubagentWake`. | Keep wake blocks as `kind=user` so they remain in model context, but render `state=subagent_wake` as a left-aligned system notice. Legacy wake text without that state stays a user bubble. |
| UI-014 | Fixed, guarded | The main Inspector context kernel mixed subagent cache and child `context_profile` events into the parent occupancy and hit rate, so a long review child made the main conversation look full or highly cached. | `projectContextUsage` ignores `requestKind=subagent`, `Usage.applySubagentUsage`, and the main-kernel reducer test. | Keep the live Inspector and composer meter on the main agent only. Persist subagent totals on the session subagent counters for a later child-owned kernel. |
| UI-015 | Fixed, guarded | The embedded PTY used the chat `--mono` stack, so Powerlevel10k/starship Nerd Font glyphs rendered as tofu, and xterm's default `#000` viewport plus host-padding overflow showed a black hairline at the bottom. | Desktop terminal screenshot, `terminalFontStack` / tab-label tests, and `terminal.css` viewport/scrollbar assertions. | Keep a local Nerd Font fallback stack on the PTY (do not bundle an unlicensed font). FitAddon must measure `.xterm` padding; viewport background and scrollbars use `--paper`/`--ink`, never `#000`. Tab labels are one shell name plus cwd basename. |
| UI-016 | Fixed, guarded | A no-tool Q&A replaced the live 「思考了 Xs」 header with a shorter 「已处理 · 0s」 fold when the stream finished, so the answer jumped; the sparkle also started on a muted token and pulsed to ink. The first-token wait pill later stayed medium-gray because `ThinkingPlaceholder` renders a `disabled` `reasoning-summary` and inherited `button:disabled { opacity: .45 }`. An opacity-only absolute caret later stayed as a gray hairline between settled answer paragraphs. After a tool batch the same wait pill disappeared while the main run was still live, so the transcript looked dead. | Desktop streaming/complete/wait-pill/caret screenshots, `processTrailWorthFolding`, Timeline no-tool remount, between-batch wait, activity-bar identity, and completed-caret tests, and thinking-mark/wait-pill/caret CSS assertions. | Do not fold thinking-only or commentary-only trails. Keep the same thinking header and `data-testid="timeline-prose"` wrapper through completion. In-progress wait → thinking → search/tools stay on one ✦ sparkle bar: only the label changes; do not remount, reset the clock, or switch cards. Reserve the chevron slot so enabling expand does not jump the row. A thinking-only trail stays that sparkle row plus reasoning prose. An opened live step body reads reasoning first as quiet prose, then the tool rows it led to. A settled tool step is a plain count row (`N tool calls` / `N 次工具调用`) with no gray capsule, not the sparkle 「运行了 N 个工具」 bar. Opening it puts one 思考 chip first, then the tools, then file-change pills. Live wait, thinking, and running tools stay on the sparkle bar. Live reasoning stays prose and is not a chip. Do not put those views in a Steps / Reasoning / Search / Coding switcher. Chat thinking chrome uses `--ink` from first paint, including the disabled first-token wait; that wait uses the same left-aligned sparkle row as live and completed thinking, never a separate capsule. Reset that button to `opacity: 1` and never let global `button:disabled` wash it to gray. Shimmer brightness only, never a muted→ink swap. Hide the streaming caret with `content: none` when prose is not `.active`; do not leave an absolute/opacity-only box that can paint between settled paragraphs. A live caret may sit in-flow at the end of the latest block only. Real tool/diff trails may still fold (UI-007). After the last tool/thinking in a live run, keep the 思考 wait bar visible until new commentary, thinking, or a final answer arrives. Empty frames and hidden host fallback are not progress. Do not invent 「我准备」 (UI-008). Every row of that expanded chip list carries a status mark (check, spinner, hollow dot, dash) and one rail segment in a left gutter beside the marks, so an expanded body cannot break the thread. Marks report the real parallel aggregate rather than forcing one active row. New rows cascade at 120ms capped at six; segment IDs stay independent of collection size so appending a block cannot remount the group and replay that entrance, and a virtualized window never re-animates a row it has already shown. One model message plus the work that follows it is one step, and each step is one bar: `ProcessStep` renders `ThinkingState` and `activityBarLabel` owns the wait, thinking, search, running, and settled wording, with the live elapsed clock on one 正在处理 divider above the current turn's first message — thinking and tool rows do not print a second clock while the run is live. The 思考 label never includes that clock, and the bar hugs the current label instead of reserving a 12em gap. The model's own prose stays in the transcript; its tools, thinking, and file changes live inside that step's body and are never repeated outside it. A trail therefore reads message, bar, message, bar — `ProcessFold` only carries the trail and raises no bar of its own. The body opens from the bar and starts closed, except a live step that has not called a tool yet, which streams its reasoning until the first tool arrives; an explicit collapse or expand always wins. Opening reveals the body with one bounded fade, and the bar's label rolls vertically only when its meaning changes: a ticking clock inside the label must not restart that motion, and the running sparkle breathes on scale and brightness alone. Never hand a finished trail to a second disclosure card, and never let a chip group repeat the enclosing bar's totals — a summarized group prints no header, and an unsummarized one counts only the rows it heads. While the step is live the bar names the row that is executing and changes with it; only a settled step prints the step's own total. Parallel rows report 「正在运行 N 个工具」. The first-token wait and the wait between batches are that same step: the pending step enters the keyed entry list at the position the next step will take, so the element that shows 思考 is the element that later shows the tools. `ProcessStatusRule` owns the live clock, because ticking it in `ProcessStep` re-renders the whole body every second and replays row entrances (UI-012). An opened body longer than the deferred minimum stays windowed (UI-012). A bar appearing over an existing trail must not remount it: the body keeps one position whether or not a bar is present. A step with only a delegation card or an empty thinking heartbeat raises no bar while it runs. |
| UI-017 | Fixed, guarded | A settled tool step showed the same run duration on every row (all `1m44s`) because `run_finished` copied the step clock onto tools that had no per-call elapsed time. | Desktop screenshot of five tools sharing `1m44s`, `stampProcessElapsed`, store per-tool clock test, and Timeline distinct-duration test. | Stamp `startedAt` on `tool_started` and each tool's own `elapsedMs` on finish. Never copy the run duration onto a tool. The live 正在处理 rule keeps the overall clock; thinking and tool rows stay untimed until the run settles. Each finished tool still stores its own elapsed time. |
| UI-018 | Fixed, guarded | The last lines of a long answer sat under the floating composer, so headings such as 「结论」 could not be read without guessing there was more below. | Desktop screenshot of the composer covering the tail of a numbered list, `transcript-composer-clearance`, and composer overlay-gap tests. | Keep a measured spacer after the timeline instead of padding-bottom on `.transcript`. Floor the overlay gap at `COMPOSER_OVERLAY_MIN_GAP`. Do not let `content-visibility` on the last feed child drop that space from scroll height. |
| UI-008 | Fixed, guarded | Providers could start single or batched tools without first giving the user a progress update, while adjacent reasoning rendered as separate zero-second rows instead of part of the announced step. After the host fallback was hidden, each thinking span or fallback-glued batch became its own ✦ 思考了 row and replaced the model's 「我准备…」 prose. | Main prompt contract, provider sink fallback/order test, `groupProcessTimelineBlocks`, and active Timeline regression. | Visible commentary is only model-authored ordinary prose (`TextPhase=commentary`). Never invent 「我准备」 or un-hide `synthetic=tool_announcement`. Group adjacent thinking/tools/diffs under that announcement, or into one thinking trail when the model omitted commentary. Adjacent thinking spans share one ✦ 思考了 row when there are no tools (sum of thinking time or the group span), not one row per span. While that step is live, search/tools only retitle the same sparkle bar. After it completes, expand to one chip list with thinking as the first chip. Keep nested work visible. Do not render commentary as a titled duration card. Live wait between tool batches must stay visible without host boilerplate (UI-016). |
| UI-009 | Fixed, guarded | Durable session recaps were emitted by the backend but discarded by the desktop reducer, so the right Inspector could not restore or live-update continuity state; recap generation also reused the semantic compaction route. | Recap reducer/Inspector tests, independent model-route configuration tests, and provider runtime route tests. | Keep `session_loaded` and `recap_state` projected into one current-session recap card. Preserve an independent `agents.recap` route so changing the recap writer never changes semantic compaction. |
| UI-010 | Fixed, guarded | Thinking, tool-step, and tool-summary markers kept a paper-colored rail mask while their parent row switched to the muted hover surface, producing a detached white circle around the marker. | Process-rail CSS regression test and real browser hover verification. | Preserve the rail mask at rest, but remove its background and shadow for every highlighted process-row marker. |
| NETWORK-001 | Fixed, guarded | Finder-launched Azem ignored the active macOS system proxy because Go's default HTTP transport reads proxy environment variables but not SystemConfiguration. ChatGPT requests connected directly and failed with `EOF` or TLS handshake timeouts while Codex/Electron succeeded through the local proxy. | `internal/netproxy`, auth/provider transport tests, live ChatGPT proxy test resolving `127.0.0.1:6152`. | Keep desktop bootstrap and every Azem-owned HTTP transport on the shared resolver. Preserve scheme-specific environment overrides, native proxy refresh, bypass rules, streaming zero-timeout semantics, and the opt-in live endpoint test. |
| AUTH-001 | Fixed, guarded | Grok device login created and polled device codes without the current Grok Build request metadata, so xAI could reject the grant with `device token request returned HTTP 400: invalid_grant`. | `internal/auth/grok/client.go`, `TestDiscoveryAndDevicePolling`, and the Grok Build device-flow contract. | Keep `referrer=grok-build` on device-code creation and send the shared client-version plus `x-grok-client-surface=ui` headers on both creation and polling. Do not retry `invalid_grant` as `authorization_pending`. |
| AUTH-002 | Fixed, guarded | Connected Grok settings showed `anonymous-<hex>` and a red 获取失败 because device tokens have no top-level `account_id`/`email`, quota sent that hash as `x-userid`, and the UI hid `quotaWarning`. A later `/v1/user` fetch could also fail with `EOF` when an HTTP/2 stream through the desktop proxy was dropped. Live `/v1/billing?format=credits` then omitted `creditUsagePercent` on a unified weekly period, so Azem failed with `no weekly quota` while CodexBar showed 0% used from `onDemandUsed`/`onDemandCap` or a zero-usage current period. | JWT identity extraction, `/v1/user` then `/v1/billing?format=credits`, `TestGrokQuotaUsesLiveUserIDAndSurfacesHTTPReason`, `TestGrokQuotaRetriesTransientUserEOF`, `TestDecodeSubscriptionQuotas`, SettingsDialog quota-reason test. | Keep `/v1/user` as the only billing `x-userid` source. Persist email/display on the existing account id; do not rename stored anonymous ids. Surface `quotaWarning`. Retry those GETs once after EOF/reset; a second failure stays visible. Accept omitted `creditUsagePercent` via on-demand ratio or a parseable current period at 0%. Do not regress AUTH-001 headers or skip `internal/netproxy`. |
| PR-001 | Fixed, guarded | GitHub PR capability needs a visible entry point and explicit prerequisites. | README Pull Requests section, `internal/githubpr/`, desktop PR pages. | Keep README synchronized with mutations, head-OID protection, Monitor & Fix triggers, and unavailable states. |
| PR-002 | Documentation gap | PR operations depend on local `git`, GitHub CLI authentication, a resolvable GitHub remote, and repository permission. | `internal/githubpr/client.go`. | Document missing `gh`, logged-out, non-GitHub, permission, network, and rate-limit failures. Never represent unavailable capability as an empty list. |
| PR-003 | Documentation gap | PR monitoring polls, persists state, deduplicates fingerprints, and starts repair sessions; its maintenance contract is not independently documented. | `internal/githubpr/monitor.go`: 60-second interval, five-minute maximum backoff, state version 3. | Document the state machine, persistence file, trigger, retry, deduplication, concurrency, restart, and stop behavior. |
| PR-004 | Documentation gap | Merge, auto-merge, reviews, and other mutations have external side effects without a dedicated permission and safety guide. | `MutationRequest` in `internal/githubpr/types.go`. | `docs/security.md` must list each mutation, permissions, validation, head-OID protection, and audit evidence. |
| DOC-001 | Documentation gap | Core architecture, persistence, testing, configuration, provider streaming, security, agent-runtime, and recovery guides now exist, but PR, release, troubleshooting, accessibility, contribution, changelog, and ADR documentation remain. | Existing `docs/*.md` files and documentation index below. | Complete the remaining index incrementally; keep README as the short user entry point. |
| DOC-002 | Fixed, guarded | Project Layout previously omitted the Wails GUI, React frontend, desktop bridge, and GitHub PR package. | README Project Layout. | Update Project Layout whenever a top-level application or module boundary changes. |
| DOC-003 | Fixed, guarded | Development documentation previously covered only Go tests and formatting. | `docs/testing.md`, README Development section, `Makefile`. | Keep Go, frontend, desktop, SQLite, architecture, and GUI smoke commands synchronized with the real build. |
| SEC-001 | Fixed, guarded | Combined GitHub, OAuth/API-key credential, hook, MCP, and automated-repair risks previously lacked a full threat model. | README Security Model and `docs/security.md`. | Keep the threat model synchronized with every new Bridge action, credential path, external mutation, hook, MCP, and automated-repair capability. |
| OPS-001 | Unresolved | There is no complete database upgrade, backup, rollback, corruption recovery, or downgrade runbook. | Schema 17/18 incident; automatic `.bak` implementation. | Write `docs/release.md` and `docs/troubleshooting.md`; prohibit downgrade writes. |
| TEST-001 | Fixed, guarded | Maintainers previously lacked a unified command matrix by change type. | `docs/testing.md`, verification matrix below. | Keep commands executable and require a real GUI launch for desktop behavior changes. |

Public issue state is not evidence that the product has no defects; the
evidence-backed internal record above remains the maintenance backlog.

## Product Contracts That Must Not Regress

### SQLite and upgrades

- `schemaVersion == len(migrations)`.
- Runtime migrations and `internal/store/sqlite/dbgen/schema.sql` stay aligned.
- Preserve the automatic backup before upgrading an existing database.
- Never delete or rebuild a user database to solve a version conflict.
- Test previous-version upgrade, current-version reopen, retained data, and
  safe rejection of a future schema.
- Hold the shared runtime recovery fence until the process has stopped its
  agents and closed persistence. A second desktop window must never recover or
  quarantine work owned by another live process.
- Schema 18 contains `agent_definition_snapshots`, `admission_reservations`,
  `resource_claims`, and every index defined by migration 18.
- Schema 19 contains `desktop_projects`, `session_workspaces`, and the
  `session_workspaces_workspace` index. Projects are application state, not a
  single `workspace.root` configuration value.
- Schema 20 contains semantic state, append-only semantic events, and context
  manifests. Its migration invalidates legacy replaceable ModelHistory/cache
  identity but preserves canonical transcript and all other durable stores.

### Provider text phases

The complete path must preserve `TextPhase`:

```text
provider stream
  -> app runtime event
  -> desktop bridge/session persistence
  -> frontend store reducer
  -> timeline rendering
```

- `commentary` explains work around tool calls and is not a final answer.
  Real model commentary is ordinary prose in the transcript, not a titled
  duration card. The executable main prompt requires the model to author
  that commentary itself; the host must not invent 我准备 text. The host
  fallback announcement is an internal grouping anchor and must not render
  as user-visible prose.
- `final_answer` is the user-visible terminal response and appears once.
- Reasoning/thinking never impersonates commentary.
- Persisting and reopening a session preserves phase and order.
- UI projection compaction never fails the provider run. Consumers reload the
  durable session projection after replaceable deltas are discarded, including
  one final reload after the terminal event.

### llmux providers

- `chatgpt` and `grok` remain reserved for their existing subscription drivers.
- llmux profile IDs resolve only when enabled and when the selected model exists
  and is not disabled in `providers.llmux.<id>.models`. Subscription
  `disabled_models` follows the same runtime and model-picker rule; Settings
  keeps disabled models visible so they can be re-enabled.
- Stored API keys use `internal/auth`; YAML and runtime events never contain
  them. Environment fallback uses the profile's declared variable.
- llmux internal retries remain disabled so Venat is the single retry owner.
- Image attachments retain the shared trusted-root, regular-file, and detected
  content-type validation before entering llmux. Do not impose a product-wide
  image count or per-image byte cap below the selected provider's own contract.
- Image-capable main models receive validated attachments directly. A main
  model whose catalog explicitly excludes images must use the independently
  configured `agents.vision` route; its bounded output enters the main context
  as private, untrusted user evidence and the text-only main request contains
  no image parts. Unknown modality metadata keeps the native compatibility
  path, and a missing or unusable helper fails explicitly.

### Venat integration

- `go.mod` is authoritative; do not rely on a local `go.work` or adjacent
  checkout.
- Release and final integration validation use `GOWORK=off`.
- Store adapters satisfy the current Venat contracts without retaining old
  compatibility branches.
- Recheck definition snapshots, admission reservations, resource claims,
  recovery, and usage contracts on each Venat upgrade.

### Tool dispatch concurrency

- Main and subagent definitions use parallel tool dispatch.
- The executable main prompt requires a durable `todo` snapshot before any
  workspace investigation or modification. Trivial chat that needs no
  workspace work is exempt.
- Subagents may recursively delegate up to `agents.subagents.max_depth`; the
  default is 2, `-1` is unlimited, and 0 disables delegation.
- Subagent concurrency defaults to 32; zero is unbounded. A full scheduler may
  admit one re-entrant child per active parent to avoid recursive wait deadlock.
- Soft request budgets are advisory only and never terminate a child.
- Shell execution and subagent scheduling continue to enforce their independent
  configured concurrency limits.
- Skill activation batches remain sequential because later calls can depend on
  tools registered by the activation call.
- With Venat versions that serialize skill-resource batches, finish
  `hydaelyn_read_skill_resource` calls before launching foreground Subagents;
  launch independent Subagents together in the following parallel batch.
- A later turn in the same session must treat completed
  `hydaelyn_activate_skill` records as already active when the skill is still
  resolvable. Disabled or deleted skills are not replayed. Reading a skill
  resource that was never activated in the session still fails closed.
- Review or verification that gates later work stays foreground, or the parent
  must consume it with `subagent.get_output` before ending the turn. A parent
  that still has running background children of the current run receives one
  `pending-background-children` retry and never cancels those children
  (SUBAGENT-001, SUBAGENT-004).
- `agents.subagents.idle_timeout` defaults to `5m`. Zero disables the
  watchdog. A positive window cancels only a running child with no thinking,
  output, or tool activity and no open tool. Empty thinking/text frames and
  elapsed-time UI ticks are not activity. Compaction and explicit wait
  summaries reset the clock (SUBAGENT-005).
- Desktop stop is an explicit include-children stop. Parent-wait expiry and
  the TUI parent-only choice still detach safe children instead of
  cancelling them (SUBAGENT-001, SUBAGENT-006).
- Background completion auto-wake batches every undelivered terminal child in
  the session into one turn. The wake user block keeps `kind=user` with
  `state=subagent_wake` (UI-013).
- The live Inspector context kernel and composer occupancy meter count only
  the main agent. Subagent `context_usage` and child `context_profile` events
  must not replace those totals (UI-014).

### Plugins

- `.codex-plugin/plugin.json` is the required plugin entry point; every
  referenced path begins with `./` and remains inside the plugin root.
- Load only from Azem's `plugin-packages` data directory. Direct packages live
  under `local`; Codex paths are copy sources only and imported packages live
  under `codex/<marketplace>/<plugin>`.
- List packages reported installed by the current Codex catalog, but import
  only IDs explicitly selected in `plugins.codex_imports`. A failed later
  import must not remove the last valid selected Azem copy.
- Unselected Codex plugins contribute no runtime Skills, MCP servers, Hooks, or
  Apps. Plugin icons are read from Azem copies, bounded and type-checked, then
  projected as image data instead of local filesystem paths.
- Plugin Skills and MCP servers reuse the existing runtimes; do not create a
  second execution or approval path.
- Installing or enabling a plugin does not trust its Hooks. Keep
  `plugins.trust_hooks` false by default and provide `PLUGIN_ROOT`,
  `CLAUDE_PLUGIN_ROOT`, and `PLUGIN_DATA` only after explicit trust.
  Catalog plugin hook files before trust; execute them only when trusted
  and not listed in `hooks.disabled`. `set_hook_enabled` is the only
  per-hook control and reuses the existing dispatcher.
- OAuth-only MCP and `.app.json` connections remain visibly unavailable until
  a separately authenticated connection exists.

### GitHub pull requests

The complete PR contract includes capability detection, dashboard lists,
details, checks, reviews, comments, commits, files, activity, every mutation,
external links, failure/conflict monitoring, fingerprint deduplication,
isolated repair sessions, and these states: `disabled`, `watching`, `pending`,
`repairing`, `completed`, `error`.

Security requirements:

- Execute GitHub CLI with argv; never assemble a shell command.
- Validate user input at the existing client boundary.
- Respect repository merge methods and permissions.
- Return explicit success or failure for remote mutations.
- Never start duplicate repairs for the same failure fingerprint.

## Documentation Index

### Existing documentation

| Path | Status | Purpose | Follow-up |
|---|---|---|---|
| `AGENTS.md` | Current | Agent rules, regression record, documentation index, verification matrix. | Update issue and document state changes. |
| `README.md` | Current user entry | Quick start, features, configuration, security overview, project navigation. | Keep concise and link detailed guides. |
| `docs/architecture.md` | Complete | Shared runtime, package boundaries, event flow, key call paths, extension points. | Update for module or event-boundary changes. |
| `docs/persistence.md` | Complete | Paths, schema, SQLC synchronization, backup, compatibility, durable stores, recovery. | Add schema ADR. |
| `docs/testing.md` | Complete | Test levels, command selection, desktop smoke test, live-test conditions. | Update whenever commands or build targets change. |
| `docs/desktop.md` | Complete | Wails startup, Bridge boundary, frontend ownership, workspace browser limits, build and smoke checks. | Update for new Bridge methods, windows, navigation, or WebView behavior. |
| `docs/agent-runtime.md` | Complete | Venat integration, Single/Team agents, subagent scheduling, leases, admission, resource claims, resume, usage, budgets. | Update for Venat upgrades, scheduling, budget, or resume-contract changes. |
| `docs/recovery.md` | Complete | Recovery fence, crash recovery, side-effect reconciliation, tool timeline continuity, background subagent continuation, Team resume, suspension, failure surfaces. | Update for recovery, reconciliation, or run-state changes. |
| `docs/context-rebuild-plan.md` | Complete | New semantic compaction kernel, persistence, provenance, Artifact V2, activation, and diagnostics. | Keep synchronized with context policy and schema changes. |
| `docs/plugins.md` | Complete | Shared Codex plugin standard, compatibility matrix, discovery, trust, and runtime projection. | Update when the upstream manifest or supported capability boundary changes. |
| `CHANGELOG.md` | Current | User-visible compatibility and behavior changes. | Update for schema, configuration, dependency, or behavior changes. |
| `docs/decisions/README.md` | Current | ADR index and status rules. | Add and supersede ADRs through the index. |
| `docs/decisions/0001-schema-versioning.md` | Accepted | Schema compatibility, dual definitions, backup, and rollback rules. | Update only by superseding ADR. |
| `internal/app/prompts/*.md` | Executable | Main and plan agent behavior. | Change only with behavior tests. |
| `internal/agent/prompts/team/*.md` | Executable | Team role behavior. | Run team/subagent tests when changed. |
| `internal/config/prompts/subagents/*.md` | Executable | Default subagent prompts. | Keep config overrides aligned. |
| `internal/skills/bundled/*/SKILL.md` | Executable | Bundled Skill rules. | Run Skills catalog tests when changed. |

### Required maintainer documentation

| Priority | Target | Status | Minimum content |
|---|---|---|---|
| P0 | `docs/architecture.md` | Complete | Shared runtime, package boundaries, event flow, Agent/Store/Provider relationships, key call paths, extension points. |
| P0 | `docs/persistence.md` | Complete | SQLite paths, migration rules, SQLC sync, backups, compatibility, durable stores, recovery. |
| P0 | `docs/pull-requests.md` | Pending | Prerequisites, capability detection, lists/details, mutations, monitor state machine, polling/backoff, fingerprints, repair sessions, errors. |
| P0 | `docs/security.md` | Complete | Trust boundaries, approval modes, shell/filesystem/network, MCP, hooks, credentials, GitHub mutations, repair isolation. |
| P0 | `docs/testing.md` | Complete | Test levels and commands for Go, frontend, desktop, SQLite, PR, real GUI smoke, and live-test conditions. |
| P0 | `docs/release.md` | Pending | Versioning, dependency locks, `GOWORK=off`, complete tests, macOS packaging, schema upgrades, backup, rollback limits, release verification. |
| P1 | `docs/agent-runtime.md` | Complete | Venat integration, Single/Team agents, scheduling, leases, admission, resources, resume, usage, budgets. |
| P1 | `docs/provider-streaming.md` | Complete | Transports, retries, frames, `TextPhase`, tool calls, usage, terminal events, errors. |
| P1 | `docs/desktop.md` | Complete | Wails startup, Bridge allowlist, event projection, React store, workspace browser, build, smoke test. |
| P1 | `docs/configuration.md` | Complete | Complete schema, defaults, environment, routes, roles, MCP, hooks, credentials, examples. |
| P1 | `docs/recovery.md` | Complete | Crash recovery, side-effect reconciliation, tool timeline, background work, Team resume, failures. |
| P1 | `docs/troubleshooting.md` | Pending | Login, streams, SQLite, `gh`, PR monitor, Wails/WebView, Bun, CGO, logs. |
| P2 | `docs/accessibility.md` | Pending | Keyboard, focus, ARIA, announcements, contrast, reduced motion, screen reader, WebView verification. |
| P2 | `CONTRIBUTING.md` | Pending | Environment, branches, commits, style, tests, documentation, PR checklist. |
| P2 | `CHANGELOG.md` | Current | User-visible changes, especially schema, configuration, dependencies, compatibility. |
| P2 | `docs/decisions/README.md` | Current | ADR numbering, status, supersession, index rules. |
| P2 | `docs/decisions/0001-schema-versioning.md` | Accepted | Future-schema rejection, dual definitions, backup, rollback principles. |
| P2 | `docs/decisions/0002-github-cli-boundary.md` | Pending | Why `gh`, argv boundary, authentication/permission, no stored GitHub token. |
| P2 | `docs/decisions/0003-shared-runtime.md` | Pending | Why TUI and GUI share runtime/store and Bridge exposes a closed operation set. |

## Documentation Update Rules

| Change | Required documentation |
|---|---|
| User feature | README, matching `docs/*.md`, optionally CHANGELOG |
| Configuration field/default | `docs/configuration.md`, README example, configuration tests |
| SQLite schema/persistence semantics | `docs/persistence.md`, schema ADR, CHANGELOG |
| Provider event/text phase | `docs/provider-streaming.md`, architecture flow |
| Agent/Venat scheduling | `docs/agent-runtime.md`, optionally ADR |
| Desktop Bridge/frontend event | `docs/desktop.md`, security exposure |
| GitHub mutation/monitor | `docs/pull-requests.md`, `docs/security.md` |
| Build/test/release command | `docs/testing.md`, `docs/release.md`, README quick entry |
| Evidence-backed defect/incident | This issue table and, for user impact, a linked GitHub issue |
| Prompt text, tool order, or system/context injection | State the prefix-cache impact in the change description: whether the static prefix, message order, or per-turn injected content changes, and why cache hits survive (see PROVIDER-002) |

Every documented command must have been run. Paths, fields, states, and defaults
must come from current source, not memory.

## Verification Matrix

Run the narrowest relevant check first, then the complete check for the affected
surface before delivery.

| Change surface | Minimum verification |
|---|---|
| Go file | `gofmt -w <changed files>`; `go test <affected packages>` |
| Global Go/runtime/dependency | `GOWORK=off go test ./...` |
| React/frontend | `cd frontend && bun run typecheck && bun run test && bun run build` |
| Desktop Bridge/Wails | `make test-gui`; `make gui`; launch `dist/Azem.app/Contents/MacOS/Azem` and exercise the path |
| SQLite migration | `go test ./internal/store/sqlite`; verify previous-schema upgrade, current reopen, retained data, future rejection |
| Venat upgrade | `GOWORK=off go mod tidy`; adapter/agent tests; `GOWORK=off go test ./...`; `GOWORK=off make gui` |
| GitHub PR backend | `go test ./internal/githubpr ./internal/desktop ./cmd/azem-gui`; cover missing/logged-out `gh`, no remote, permission, network failure |
| GitHub PR frontend | Frontend typecheck/test/build; verify list, detail, failure feedback, and changed mutation in an authenticated workspace |
| Provider streaming | Provider parser, app runtime, session persistence, frontend reducer/timeline tests |
| Prompt/bundled Skill | Matching app/agent/config/Skills tests and a real conversation path |
| Architecture | `sentrux check .` and `session_end` with zero new cycles or violations |
| README/documentation | Validate paths, commands, links, and descriptions against current source |

A single passing test does not complete a feature. GUI changes require a real
GUI launch; migrations require opening a real database; external mutations
require both success and failure paths.

## Issue Record Format

When adding an issue row, include:

- A stable domain prefix and sequence, for example `DB-003`.
- One of the defined statuses.
- Observable user impact rather than a vague possibility.
- A test, error, source path, runtime log, or GitHub link as evidence.
- A confirmed root cause, or an explicit statement that it is unknown.
- The boundary to change and the regression coverage to preserve.
- The documentation index entry that must be updated.

Do not delete fixed incidents. Mark them **Fixed, guarded** and retain their
invariants and regression tests. Do not record transient build failures,
immediately corrected typos, or personal task progress.
