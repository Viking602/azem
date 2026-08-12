# Azem Agent Guide

Last verified: 2026-08-11

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
| Desktop bridge | `internal/desktop/`, `cmd/azem-gui/`, `frontend/src/bridge.ts` |
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
| STREAM-001 | Fixed, guarded | Commentary, reasoning, and final answers were previously merged, hiding progress around tool calls or duplicating final output. | Provider stream, app event, session timeline, and frontend reducer `TextPhase` tests. | Preserve `commentary` and `final_answer` end to end; reasoning must not impersonate commentary; final output renders once. |
| PROVIDER-001 | Fixed, guarded | Azem previously hard-coded ChatGPT and Grok, so other model providers and arbitrary model IDs could not be configured or routed from the desktop. | `internal/provider/llmux`, generic runtime resolution tests, model-provider action/Bridge/store/settings tests. | Keep ChatGPT/Grok subscription IDs reserved; keep llmux API keys out of YAML and events; validate configured models; preserve one retry owner and typed stream mapping. |
| PROVIDER-002 | Fixed, guarded | DeepSeek's Anthropic-compatible usage reports uncached input and cache-read input separately, while llmux v0.2.4 retained the values without marking the cache counter as reported. Real hits therefore rendered as `Not reported`, and context occupancy excluded cached input. Azem also hoisted every late private system message into Anthropic's top-level system field, rewriting the long-conversation prefix on each turn and reducing real cache hits to about 9%. | `normalizeProviderUsage`, `TestDeepSeekStreamReportsInclusiveCacheUsage`, `TestAnthropicConversionKeepsLatePrivateSystemContextInMessageTail`, and live DeepSeek V4 Flash long-conversation verification. | Keep DeepSeek input usage inclusive of cache-read/cache-write counters, mark zero and non-zero cache reads as reported, leave unknown provider cache support unreported, hoist only leading Anthropic system messages, and preserve later trusted host context at its original message-tail position. |
| CONTEXT-001 | Fixed, guarded | Rolling summaries mixed durable task state with short-lived transcript evidence and could drift across repeated compactions. | SemanticStateV1 validation, schema 20 state/events/manifests, unified planner tests, Artifact V2 tests, Inspector projection tests. | Keep one new kernel across automatic/manual/resume paths; preserve latest three user turns exactly, stable provenance, atomic tool groups, transactional cursor/manifest activation, and explicit mandatory-budget failure. Do not restore legacy fallback. |
| CONTEXT-002 | Fixed, guarded | Long subagent reviews could spend millions of cumulative input tokens and then fail when a valid semantic checkpoint exceeded the old 8,192-token writer ceiling; reasoning could also consume that allowance and return truncated JSON. Persisted private checkpoints were projected as visible assistant prose and re-seeded on resume. | Failed `subagent_runs` records with `summary output requires ... configured limit allows 16384`, compaction retry/limit tests, and subagent projection/resume tests. | Keep the default semantic state budget at 32,768 tokens, allow configurations up to one quarter of the writer context window, keep generation headroom separate from the durable state budget, and never project or resume-seed private/compaction messages. Mandatory state that still cannot fit must fail explicitly. |
| RUNTIME-001 | Fixed, guarded | Venat upgrades changed Usage Store and durable control-plane contracts; old semantics ignored aggregate query limits. | `go.mod`, `internal/store/sqlite/stores_governance.go`, Venat contract tests. | Do not restore old Usage semantics. Verify the real module with `GOWORK=off`, and run upstream contract tests during framework upgrades. |
| RUNTIME-002 | Fixed, guarded | Opening another project window previously ran global crash recovery against the shared SQLite database, expired leases owned by a still-live Azem process, and made the original run fail with `stale task version` or become `reconcile_required`. A second main task also surfaced a transient workspace claim conflict as a terminal provider failure. | `internal/store/sqlite/recovery_fence*`, recovery-fence tests, `TestMainRunWaitsForWorkspaceClaimInsteadOfFailing`, and durable run/tool/subagent evidence from `run_ee2cd85db941608e69b4de4b`. | Every process holds the shared runtime fence for its complete lifetime. Only a process that first acquires the exclusive fence may prepare crash recovery, session navigation must not emit `SessionEnd`, and main/subagent tasks must wait and retry transient resource-claim conflicts instead of persisting raw failures. |
| CONCURRENCY-001 | Fixed, guarded | Agent definitions omitted `toolMode`, so Venat defaulted whole tool batches to sequential execution. Foreground subagent spawns and shell calls appeared queued even when their runtime limits had free capacity. | `agentDefinitionForSpec`, `TestAgentDefinitionUsesParallelToolDispatch`, Venat parallel dispatch tests. | Keep main and subagent definitions in parallel tool mode. Preserve the separate shell and subagent concurrency limits and Venat's sequential skill-activation safeguard. |
| SUBAGENT-001 | Fixed, guarded | Foreground subagents were cancelled when a ten-minute parent wait window elapsed or the parent tool context ended, so valid long investigations could not finish despite unbounded token, turn, and wall-clock budgets. | `subagentSpawnDriver`, `continueInBackground`, long-running lifecycle tests. | A wait window releases only the parent call. Safe work continues durably in the background; unsafe shared-workspace writes keep waiting. Cancel children only through explicit kill, an explicit include-children stop, or application shutdown. |
| TOOL-001 | Fixed, guarded | Queued tools were once rendered as running, making approval wait time appear as execution time and leaving later calls spinning. | `provider_execution.go`, `provider_approval.go`, `frontend/src/store.ts`, `Timeline.tsx`, lifecycle tests. | Preserve `queued -> awaiting_approval -> running -> completed/failed`; do not show unexecuted writes as file changes; converge every non-terminal tool when a run ends. |
| TOOL-002 | Fixed, guarded | The desktop stop action synchronously waited for Venat to finish cancelling the active durable run before returning through the Bridge. A slow MCP/tool cleanup therefore left the stop button and run visibly stuck. | `CancelActiveWithChildren`, `TestCancelActiveReturnsBeforeUncooperativeExecutionFinishes`, and real desktop stop verification. | Deliver the cancellation request to the durable coordinator asynchronously so the Bridge returns immediately; keep the coordinator as the owner of the terminal cancellation cause, then cancel the app-owned run context after durable cleanup converges. |
| APPROVAL-001 | Fixed, guarded | Configurable Anthropic-compatible approval models can ignore native response-schema options and return the decision as a Markdown JSON fence, which previously caused `Automatic review failed (parse)` and prevented an authorized action from running. | `internal/provider/codex/guardian_policy.go`, `reviewer.go`, provider reviewer and app automatic-approval tests. | Keep the explicit JSON-only output contract and strict fail-closed validation. Accept only raw JSON or one whole-response JSON fence; never extract a decision from surrounding prose or execute after an invalid review. |
| TODO-001 | Fixed, guarded | Parallel Todo mutations shared one stale revision; a later `start` could win first, demote the actual current item to pending, and make its `done` fail. Completed work then appeared to update only at the end or was never persisted. | `todo_tool.go`, `TestTodoStartCannotReplaceCurrentItem`, `TestTodoConcurrentMutationsCannotSkipCurrentItem`, executable main prompt contract. | Keep global tool dispatch parallel, but issue exactly one Todo mutation after each completed item and await its snapshot. `start` must never replace another current item; `done` remains the only normal transition that completes and advances work. |
| UI-001 | Fixed, guarded | Todo, Subagents, active-thinking motion, and process groups once existed only on an integration branch and disappeared from the GUI branch. | Commit `c2e4030`, frontend components/styles, frontend regression tests. | Move these projections and views as one unit. Do not restore the old `AgentList` or port styling without the store/event projection. |
| EXT-001 | Fixed, guarded | The Extensions surface previously exposed only raw Skills, then loaded imported plugins directly from Codex source/cache paths and described the whole capability as “Codex plugins”; a populated startup catalog could also appear empty when its initial event preceded the frontend listener. | `internal/plugins`, `ActionListPlugins`, desktop `plugin_catalog` origin projection, Extensions UI, plugin copy/local-discovery and snapshot replay tests. | Keep all runtime roots under Azem `plugin-packages`; direct installs use `local`, Codex entries remain catalog-only until explicitly selected in `plugins.codex_imports`, and selected imports use staged copies under `codex`. A Codex outage must not invalidate existing selected copies. Preserve validated image-data projection, manifest validation, existing Skill/MCP/hook boundaries, default-denied Hooks, and explicit OAuth/App degraded states. |
| EXT-002 | Fixed, guarded | The desktop Extensions surface derived MCP totals only from plugin metadata and never projected the live MCP manager, so configured servers disappeared and the page looked empty when no plugin was installed. | `internal/app/mcp_settings.go`, `mcp_state` frontend projection, `ExtensionsSettings` tests, and real `Azem.app` lifecycle verification. | Keep MCP independent from plugin discovery. Preserve typed add/enable/reconnect/delete actions, atomic configuration writes, persistent deletion tombstones, secret-free snapshots, asynchronous lifecycle work, explicit empty states, and live manager tool removal when disabled or deleted. Built-in and plugin-owned services are deletable; Codex-only `computer-use` launchers must never enter the Azem MCP runtime. |
| UI-002 | Fixed, guarded | A slow desktop renderer could exceed the runtime event high-water mark and terminate an otherwise healthy provider run with `UI event backlog exceeded the safe limit`. | `internal/app/event_broker.go`, `event_broker_test.go`, desktop/TUI refresh actions, frontend streaming tests. | UI projection pressure must never become a provider error. Keep replaceable deltas coalesced by stream, lifecycle and approval events lossless, durable projection resync after compaction, bounded tool previews, and lightweight live rendering. |
| UI-003 | Fixed, guarded | Switching live assistant output to immediate Markdown removed the per-delta text reveal because existing paragraph nodes no longer remounted; the old plain-text reveal could not be restored without delaying Markdown syntax. | `StreamingMarkdown`, `StreamingText`, and Timeline live Markdown/reveal tests. | Parse Markdown on every coalesced frame, animate only the bounded latest provider ranges, keep existing block nodes mounted, and honor both system and explicit reduced-motion settings. |
| UI-004 | Fixed, guarded | A conversation could finish after the user switched away without leaving any visible notification, so the completed result was easy to miss. | Cross-session terminal reducer tests, Sidebar unread-dot test, and `TestMarkSessionUnreadPersistsUntilResume`. | Only a tracked foreign main run that succeeds or fails marks its session unread. Persist the blue dot through `session_ui_state`, clear it on resume, and exclude cancellation and subagent terminal events. |
| UI-005 | Fixed, guarded | Opening another project intentionally started an isolated desktop runtime with `--new-window`, but that flag also disabled Wails single-instance handling and every child used the regular macOS activation policy, producing one extra Azem Dock application per project. | `desktopMacOptions`, `TestIndependentWindowDoesNotRegisterAnotherMacApplication`, process argv verification, and real packaged multi-project launch. | Keep the primary process regular and every isolated project/session window accessory on macOS. Preserve isolated runtimes so background work survives project navigation, while secondary windows must not register another Dock or Cmd-Tab application entry. |
| UI-006 | Fixed, guarded | Active file edits previously used the generic tool disclosure and exposed raw Hashline arguments, then changed shape only after the tool completed. | `pendingFileChangeSummaryForBlock`, the live-to-completed Timeline regression test, and real packaged desktop verification. | Keep queued and approval-bound writes out of file-change projections. Active edits may show exact planned totals only when the arguments are unambiguous, and must transition in place to the completed structured diff. |
| UI-007 | Fixed, guarded | The Subagent conversation drawer previously split one transcript across task, metadata, activity, and content panels, so it no longer read like the main conversation and completed work remained visually expanded. | `AgentSideChat`, the process-only drawer regression in `App.test.tsx`, and packaged desktop verification. | Keep Subagent user prompts and final answers in the same transcript typography as the main conversation. Only commentary and tool trails belong inside the toggleable `处理中` / `已处理` disclosure; completed trails start folded and may be reopened. |
| UI-011 | Fixed, guarded | The desktop search field previously filtered only a small in-memory command/session-title list, so settings and durable conversation content could not be found or opened. | SQLite global search tests and benchmark, CommandPalette debounce/race/navigation tests, Settings target test, Timeline sequence-focus test, and packaged desktop verification. | Keep settings/catalog search local and session content on the bounded SQLite FTS path. Never copy complete transcripts into frontend search state; preserve stale-response rejection, cross-project ownership, stable block-sequence focus, and direct projection readback after resume. |
| UI-008 | Fixed, guarded | Providers could start single or batched tools without first giving the user a progress update, while adjacent reasoning rendered as separate zero-second rows instead of part of the announced step. | Main prompt contract, provider sink fallback/order test, `groupProcessTimelineBlocks`, and active Timeline regression. | Every individual tool or parallel batch must have one preceding title/detail commentary. Keep one host fallback per missing batch, group adjacent thinking/tools/diffs into that step, and auto-expand it while nested work is active. |
| UI-009 | Fixed, guarded | Durable session recaps were emitted by the backend but discarded by the desktop reducer, so the right Inspector could not restore or live-update continuity state; recap generation also reused the semantic compaction route. | Recap reducer/Inspector tests, independent model-route configuration tests, and provider runtime route tests. | Keep `session_loaded` and `recap_state` projected into one current-session recap card. Preserve an independent `agents.recap` route so changing the recap writer never changes semantic compaction. |
| UI-010 | Fixed, guarded | Thinking, tool-step, and tool-summary markers kept a paper-colored rail mask while their parent row switched to the muted hover surface, producing a detached white circle around the marker. | Process-rail CSS regression test and real browser hover verification. | Preserve the rail mask at rest, but remove its background and shadow for every highlighted process-row marker. |
| NETWORK-001 | Fixed, guarded | Finder-launched Azem ignored the active macOS system proxy because Go's default HTTP transport reads proxy environment variables but not SystemConfiguration. ChatGPT requests connected directly and failed with `EOF` or TLS handshake timeouts while Codex/Electron succeeded through the local proxy. | `internal/netproxy`, auth/provider transport tests, live ChatGPT proxy test resolving `127.0.0.1:6152`. | Keep desktop bootstrap and every Azem-owned HTTP transport on the shared resolver. Preserve scheme-specific environment overrides, native proxy refresh, bypass rules, streaming zero-timeout semantics, and the opt-in live endpoint test. |
| AUTH-001 | Fixed, guarded | Grok device login created and polled device codes without the current Grok Build request metadata, so xAI could reject the grant with `device token request returned HTTP 400: invalid_grant`. | `internal/auth/grok/client.go`, `TestDiscoveryAndDevicePolling`, and the Grok Build device-flow contract. | Keep `referrer=grok-build` on device-code creation and send the shared client-version plus `x-grok-client-surface=ui` headers on both creation and polling. Do not retry `invalid_grant` as `authorization_pending`. |
| PR-001 | Fixed, guarded | GitHub PR capability needs a visible entry point and explicit prerequisites. | README Pull Requests section, `internal/githubpr/`, desktop PR pages. | Keep README synchronized with mutations, head-OID protection, Monitor & Fix triggers, and unavailable states. |
| PR-002 | Documentation gap | PR operations depend on local `git`, GitHub CLI authentication, a resolvable GitHub remote, and repository permission. | `internal/githubpr/client.go`. | Document missing `gh`, logged-out, non-GitHub, permission, network, and rate-limit failures. Never represent unavailable capability as an empty list. |
| PR-003 | Documentation gap | PR monitoring polls, persists state, deduplicates fingerprints, and starts repair sessions; its maintenance contract is not independently documented. | `internal/githubpr/monitor.go`: 60-second interval, five-minute maximum backoff, state version 3. | Document the state machine, persistence file, trigger, retry, deduplication, concurrency, restart, and stop behavior. |
| PR-004 | Documentation gap | Merge, auto-merge, reviews, and other mutations have external side effects without a dedicated permission and safety guide. | `MutationRequest` in `internal/githubpr/types.go`. | `docs/security.md` must list each mutation, permissions, validation, head-OID protection, and audit evidence. |
| DOC-001 | Documentation gap | Core architecture, persistence, testing, configuration, provider streaming, and security guides now exist, but PR, release, desktop, recovery, troubleshooting, accessibility, contribution, changelog, and ADR documentation remain. | Existing `docs/*.md` files and documentation index below. | Complete the remaining index incrementally; keep README as the short user entry point. |
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
- Shell execution and subagent scheduling continue to enforce their independent
  configured concurrency limits.
- Skill activation batches remain sequential because later calls can depend on
  tools registered by the activation call.
- With Venat versions that serialize skill-resource batches, finish
  `hydaelyn_read_skill_resource` calls before launching foreground Subagents;
  launch independent Subagents together in the following parallel batch.

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
| P1 | `docs/agent-runtime.md` | Pending | Venat integration, Single/Team agents, scheduling, leases, admission, resources, resume, usage, budgets. |
| P1 | `docs/provider-streaming.md` | Complete | Transports, retries, frames, `TextPhase`, tool calls, usage, terminal events, errors. |
| P1 | `docs/desktop.md` | Complete | Wails startup, Bridge allowlist, event projection, React store, workspace browser, build, smoke test. |
| P1 | `docs/configuration.md` | Complete | Complete schema, defaults, environment, routes, roles, MCP, hooks, credentials, examples. |
| P1 | `docs/recovery.md` | Pending | Crash recovery, side-effect reconciliation, tool timeline, background work, Team resume, failures. |
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
