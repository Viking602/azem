# Testing

Last verified: 2026-08-06

Azem spans a Go runtime, SQLite, Bubble Tea, Wails, and a React frontend. Passing
one package is not enough when a change crosses those boundaries. Start with
the narrowest relevant check, then run the complete check for the affected
surface.

## Required tools

- Go 1.25.8 or later; `go.mod` selects toolchain 1.25.12.
- Bun 1.3.14 for the frontend lockfile and scripts.
- macOS or Windows for the Wails desktop entry point; the current packaged
  smoke procedure is documented for macOS.
- Sentrux 0.5.7 for repository architecture rules.
- Authenticated provider and GitHub CLI accounts only for explicit live tests.

## Core commands

Complete Go suite using declared modules rather than a local workspace:

```bash
GOWORK=off go test ./...
```

Frontend typecheck, unit tests, production build, and desktop Go tests:

```bash
make test-gui
```

Architecture constraints:

```bash
make architecture-check
```

Packaged desktop application:

```bash
make gui
```

## Verification by change type

| Change | Narrow check | Complete check |
|---|---|---|
| Go package | `go test ./internal/<package>` | `GOWORK=off go test ./...` when runtime or shared behavior changes |
| Go formatting | `gofmt -w <changed.go>` | `git diff --check` |
| React component/store/style | Run the matching Vitest file during iteration | `make test-gui` |
| Desktop Bridge or Wails lifecycle | `go test ./internal/desktop ./cmd/azem-gui` | `make test-gui`, `make gui`, real app launch |
| SQLite migration/adapter | `go test ./internal/store/sqlite` | `GOWORK=off go test ./...` plus real upgrade/reopen evidence |
| Venat version/contract | Affected agent and adapter packages | `GOWORK=off go mod tidy`, `GOWORK=off go test ./...`, `GOWORK=off make gui` |
| Provider streaming | Provider parser and driver tests | App runtime, session persistence, frontend reducer/timeline tests |
| GitHub PR backend | `go test ./internal/githubpr ./internal/desktop ./cmd/azem-gui` | Success and failure paths with authenticated `gh` when mutations change |
| Prompt or bundled Skill | Matching app/agent/config/Skills tests | Real conversation path |
| Documentation/build command | Link/path check and run every documented command | `git diff --check` |

## SQLite checks

Migration work must prove all four compatibility directions:

1. Upgrade from the immediately previous schema.
2. Reopen the current schema without another backup or mutation.
3. Preserve existing user data.
4. Reject a future schema without writing.

Keep `schemaVersion == len(migrations)` and synchronize
`migrations.go` with `dbgen/schema.sql`. See [Persistence](persistence.md).

## Desktop smoke test

A desktop behavior or build change is complete only after the packaged binary
starts. On macOS:

1. Run `make gui`.
2. Confirm `codesign --verify --deep --strict dist/Azem.app` passes (the build
   performs this check automatically).
3. Launch `open dist/Azem.app` or `dist/Azem.app/Contents/MacOS/Azem`.
4. Confirm the main window renders and the current workspace appears.
5. Open an existing session or create a new one.
6. Confirm the composer, model controls, timeline, and project navigation are
   interactive.
7. Exercise the changed path and one failure path.
8. Quit the application and confirm shutdown does not leave the database
   locked or the process running.

For visual-only changes, also check light/dark appearance, narrow layout,
keyboard focus, reduced motion where relevant, and readable approval/error
states.

## Live provider tests

Files guarded by the `live` build tag require
`AZEM_LIVE_ACCEPTANCE=1`, valid local credentials, network access, and explicit
acceptance of provider usage. Optional ChatGPT model and reasoning overrides
are `AZEM_LIVE_CHATGPT_MODEL` and `AZEM_LIVE_CHATGPT_REASONING`.

Do not enable live acceptance in the default suite or CI. These tests may call
real subscription services and consume credits. Standard tests must remain
offline and use fakes or local test servers.

## GitHub PR acceptance

Backend unit tests cover argv construction, normalization, monitor state,
deduplication, and error mapping. A changed remote mutation additionally needs
an authenticated test repository and both:

- A successful operation whose resulting remote state is read back.
- A denied, stale-head, invalid-input, or permission failure that remains a
  visible error rather than an empty result or success state.

Monitor tests must preserve the 60-second normal interval, exponential backoff
to five minutes, persisted state version 3, repository binding, and one repair
per failure fingerprint.

## Architecture and quality signals

`.sentrux/rules.toml` is the pass/fail policy: zero cycles, per-file coupling no
worse than B, and no God Files. `session_end` compares the current structural
signal with the task baseline. Neither replaces compilation, behavioral tests,
or real GUI validation.

## Delivery checklist

- Existing user changes remain intact.
- Changed code is formatted and focused tests pass.
- The relevant complete command passes.
- Architecture rules pass with no new cycle.
- Documentation matches current source and commands.
- `git diff --check` passes.
- Desktop changes include packaged-app smoke evidence.
