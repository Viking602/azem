# Azem Agent Guide

Last verified: 2026-08-06

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
| DB-001 | Fixed, guarded | A user database reached schema 18 while source only supported 17, causing packaged startup to fail with `database schema 18 is newer than supported schema 17`. | `internal/store/sqlite/migrations.go`, `migrations_test.go`, real schema 18 reopen verification. | Keep `schemaVersion == len(migrations)`. Preserve migration 18 control-plane tables and indexes, v17-to-v18 upgrade/reopen coverage, and rejection of unknown future schemas. |
| DB-002 | Fixed, guarded | Runtime migrations and the SQLC schema are separate definitions that must remain synchronized. | `internal/store/sqlite/migrations.go`, `internal/store/sqlite/dbgen/schema.sql`, `docs/persistence.md`, `docs/decisions/0001-schema-versioning.md`. | Keep both definitions, the persistence guide, and the schema ADR synchronized. Every schema change tests upgrade, reopen, and data retention. |
| PROJECT-001 | Fixed, guarded | The desktop process previously exposed one fallback workspace while session history was global, so direct app launches lost the real project name, branch, and PR context and rendered every session under the wrong project. | Schema 19 project/session ownership tables, desktop bootstrap restore test, session catalog test, multi-project Sidebar test. | Keep project catalog state in SQLite rather than `config.yaml`; preserve one immutable project owner per session; direct desktop launch restores the most recently opened valid project; cross-project sessions open with that project's workspace. |
| STREAM-001 | Fixed, guarded | Commentary, reasoning, and final answers were previously merged, hiding progress around tool calls or duplicating final output. | Provider stream, app event, session timeline, and frontend reducer `TextPhase` tests. | Preserve `commentary` and `final_answer` end to end; reasoning must not impersonate commentary; final output renders once. |
| RUNTIME-001 | Fixed, guarded | Venat upgrades changed Usage Store and durable control-plane contracts; old semantics ignored aggregate query limits. | `go.mod`, `internal/store/sqlite/stores_governance.go`, Venat contract tests. | Do not restore old Usage semantics. Verify the real module with `GOWORK=off`, and run upstream contract tests during framework upgrades. |
| CONCURRENCY-001 | Fixed, guarded | Agent definitions omitted `toolMode`, so Venat defaulted whole tool batches to sequential execution. Foreground subagent spawns and shell calls appeared queued even when their runtime limits had free capacity. | `agentDefinitionForSpec`, `TestAgentDefinitionUsesParallelToolDispatch`, Venat parallel dispatch tests. | Keep main and subagent definitions in parallel tool mode. Preserve the separate shell and subagent concurrency limits and Venat's sequential skill-activation safeguard. |
| TOOL-001 | Fixed, guarded | Queued tools were once rendered as running, making approval wait time appear as execution time and leaving later calls spinning. | `provider_execution.go`, `provider_approval.go`, `frontend/src/store.ts`, `Timeline.tsx`, lifecycle tests. | Preserve `queued -> awaiting_approval -> running -> completed/failed`; do not show unexecuted writes as file changes; converge every non-terminal tool when a run ends. |
| UI-001 | Fixed, guarded | Todo, Subagents, active-thinking motion, and process groups once existed only on an integration branch and disappeared from the GUI branch. | Commit `c2e4030`, frontend components/styles, frontend regression tests. | Move these projections and views as one unit. Do not restore the old `AgentList` or port styling without the store/event projection. |
| PR-001 | Fixed, guarded | GitHub PR capability needs a visible entry point and explicit prerequisites. | README Pull Requests section, `internal/githubpr/`, desktop PR pages. | Keep README synchronized with mutations, head-OID protection, Monitor & Fix triggers, and unavailable states. |
| PR-002 | Documentation gap | PR operations depend on local `git`, GitHub CLI authentication, a resolvable GitHub remote, and repository permission. | `internal/githubpr/client.go`. | Document missing `gh`, logged-out, non-GitHub, permission, network, and rate-limit failures. Never represent unavailable capability as an empty list. |
| PR-003 | Documentation gap | PR monitoring polls, persists state, deduplicates fingerprints, and starts repair sessions; its maintenance contract is not independently documented. | `internal/githubpr/monitor.go`: 60-second interval, five-minute maximum backoff, state version 3. | Document the state machine, persistence file, trigger, retry, deduplication, concurrency, restart, and stop behavior. |
| PR-004 | Documentation gap | Merge, auto-merge, reviews, and other mutations have external side effects without a dedicated permission and safety guide. | `MutationRequest` in `internal/githubpr/types.go`. | `docs/security.md` must list each mutation, permissions, validation, head-OID protection, and audit evidence. |
| DOC-001 | Documentation gap | Core architecture, persistence, and testing guides now exist, but PR, security, release, desktop, configuration, recovery, troubleshooting, accessibility, contribution, changelog, and ADR documentation remain. | `docs/architecture.md`, `docs/persistence.md`, `docs/testing.md`, documentation index below. | Complete the remaining index incrementally; keep README as the short user entry point. |
| DOC-002 | Fixed, guarded | Project Layout previously omitted the Wails GUI, React frontend, desktop bridge, and GitHub PR package. | README Project Layout. | Update Project Layout whenever a top-level application or module boundary changes. |
| DOC-003 | Fixed, guarded | Development documentation previously covered only Go tests and formatting. | `docs/testing.md`, README Development section, `Makefile`. | Keep Go, frontend, desktop, SQLite, architecture, and GUI smoke commands synchronized with the real build. |
| SEC-001 | Unresolved | The README describes approvals as governance rather than an OS sandbox, but combined GitHub, OAuth/CLI credential, hook, MCP, and automated-repair risks lack a full threat model. | README Security Model. | Write `docs/security.md` with trust boundaries and deployment guidance. |
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
- Schema 18 contains `agent_definition_snapshots`, `admission_reservations`,
  `resource_claims`, and every index defined by migration 18.
- Schema 19 contains `desktop_projects`, `session_workspaces`, and the
  `session_workspaces_workspace` index. Projects are application state, not a
  single `workspace.root` configuration value.

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
| P0 | `docs/security.md` | Pending | Trust boundaries, approval modes, shell/filesystem/network, MCP, hooks, credentials, GitHub mutations, repair isolation. |
| P0 | `docs/testing.md` | Complete | Test levels and commands for Go, frontend, desktop, SQLite, PR, real GUI smoke, and live-test conditions. |
| P0 | `docs/release.md` | Pending | Versioning, dependency locks, `GOWORK=off`, complete tests, macOS packaging, schema upgrades, backup, rollback limits, release verification. |
| P1 | `docs/agent-runtime.md` | Pending | Venat integration, Single/Team agents, scheduling, leases, admission, resources, resume, usage, budgets. |
| P1 | `docs/provider-streaming.md` | Pending | Transports, retries, frames, `TextPhase`, tool calls, usage, terminal events, errors. |
| P1 | `docs/desktop.md` | Pending | Wails startup, Bridge allowlist, event projection, React store, windows, deep links, build, smoke test. |
| P1 | `docs/configuration.md` | Pending | Complete schema, defaults, environment, routes, roles, MCP, hooks, credentials, examples. |
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
