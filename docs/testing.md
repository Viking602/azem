# Testing

Last verified: 2026-08-25

Azem spans a Go runtime, SQLite, Bubble Tea, Wails/React, native GPUI, and a
versioned local IPC boundary. Passing one package is not enough when a change
crosses those boundaries. Start with the narrowest relevant check, then run the
complete check for the affected surface.

## Required tools

- Go 1.25.8 or later; `go.mod` selects toolchain 1.25.12.
- Bun 1.3.14 for the frontend lockfile and scripts.
- Rust 1.97.1 for GPUI; `gpui/rust-toolchain.toml` selects the pinned toolchain.
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

Native IPC, daemon, Rust formatting, strict Clippy, protocol tests, and GPUI
state/UI-model tests:

```bash
make test-gpui
```

IPC throughput and allocation benchmarks:

```bash
GOWORK=off go test -run '^$' -bench 'Benchmark(EventHub|Codec)' -benchmem -count=3 ./internal/desktopipc
```

Headless Terminal-Bench runner (Harbor):

```bash
make azem-eval-linux
PYTHONPATH="$PWD" python3 -m unittest eval.harbor.timeout_test
cd . && PYTHONPATH="$PWD" harbor run -d terminal-bench/terminal-bench-2 -a eval.harbor.azem_agent:Azem -m chatgpt/gpt-5.6-sol -n 1 -k 1 --yes -i hello-world
```

See [eval/README.md](../eval/README.md).

Architecture constraints:

```bash
make architecture-check
```

Frozen OMP parity and cross-repository provider contracts:

```bash
GOWORK=off go test ./internal/parity
(cd ../llmux && GOWORK=off go test ./...)
(cd ../venat && GOWORK=off go test ./...)
GOWORK=off go test ./internal/provider/... ./internal/auth/...
```

Packaged desktop applications:

```bash
make gui
make gpui
```

Windows desktop cross-build (amd64 by default; set `WINDOWS_ARCH=arm64` for
Windows on Arm):

```bash
make gui-windows
```

## Verification by change type

| Change | Narrow check | Complete check |
|---|---|---|
| Go package | `go test ./internal/<package>` | `GOWORK=off go test ./...` when runtime or shared behavior changes |
| Go formatting | `gofmt -w <changed.go>` | `git diff --check` |
| React component/store/style | Run the matching Vitest file during iteration | `make test-gui` |
| Desktop Bridge or Wails lifecycle | `go test ./internal/desktop ./internal/desktop/termhost ./cmd/azem-gui` | `make test-gui`, `make gui`, real app launch |
| GPUI IPC or daemon lifecycle | `GOWORK=off go test ./internal/desktopipc ./internal/daemon` | `make test-gpui`, `make gpui`, renderer detach/reconnect smoke |
| GPUI renderer/state | Matching `cargo test -p azem-gpui <test>` from `gpui/` | `make test-gpui`, `make gpui`, real native window launch |
| Workspace file browser | `go test ./internal/desktop -run Workspace` and `cd frontend && bun run test -- WorkspaceFilesPage.test.tsx` | `make test-gui`, `make gui`, real tree/text/image/binary smoke |
| SQLite migration/adapter | `go test ./internal/store/sqlite` | `GOWORK=off go test ./...` plus real upgrade/reopen evidence |
| Venat version/contract | Affected agent and adapter packages | `GOWORK=off go mod tidy`, `GOWORK=off go test ./...`, `GOWORK=off make gui` |
| Provider streaming | Provider parser and driver tests | App runtime, session persistence, frontend reducer/timeline tests |
| GitHub PR backend | `go test ./internal/githubpr ./internal/desktop ./cmd/azem-gui` | Success and failure paths with authenticated `gh` when mutations change |
| Prompt or bundled Skill | Matching app/agent/config/Skills tests | Real conversation path |
| Native security scan | `go test ./internal/securityscan ./internal/app ./internal/store/sqlite` and the matching SecurityPage Vitest | `GOWORK=off go test ./...`, `make test-gui`, `make gui`, real Standard scan/start/cancel/finding/export keyboard smoke |
| OMP parity manifest | `GOWORK=off go test ./internal/parity` | Every frozen v18.0.3 capability is `complete` or `stronger`; then run all relevant surface checks |
| Coding tools / extension broker | Matching `internal/agent` and `internal/customtools` cases | Real read/write/Hashline/AST/shell fixtures plus browser/DAP/LSP smoke |
| Session tree/import/export/share | `go test ./internal/session ./internal/sessionimport ./internal/sessionexport ./internal/sessionshare` | SQLite migration/reopen and collaboration/protocol suites |
| JSON-RPC / ACP / headless / Go API | `go test . ./internal/rpc ./internal/acp ./internal/headless` | Actual CLI startup or client fixture for the changed transport |
| Marketplace | `go test ./internal/plugins ./internal/app` plus `ExtensionsSettings.test.tsx` | Desktop/TUI source, discover, scoped install, update, upgrade, disable, and uninstall paths |
| Auth broker/gateway/webhook | Matching `internal/authbroker`, `authgateway`, or `githubwebhook` package | Bearer/HMAC failure, redelivery/cache, refresh/block, and successful forwarding/trigger paths |
| Harbor eval adapter | `PYTHONPATH="$PWD" python3 -m unittest eval.harbor.timeout_test` | `make azem-eval-linux` and a real `harbor run` when the adapter command or timeout wiring changes |
| Adaptive eval / learning | `GOWORK=off go test ./internal/eval ./internal/workrevision ./internal/evidence ./internal/codingmemory ./internal/assets ./internal/routeeval ./internal/training ./internal/toollab ./internal/adapterdeployment` | Add `./internal/app ./internal/tui` when adapter routing or evidence-status projection changes; production routing must remain unchanged unless a validated registry is explicitly attached |
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
6. Confirm the composer, model controls, timeline, project navigation, and
   Workspace file tree/viewer are interactive.
7. Exercise the changed path and one failure path.
8. Quit the application and confirm shutdown does not leave the database
   locked or the process running.

For visual-only changes, also check light/dark appearance, narrow layout,
keyboard focus, reduced motion where relevant, and readable approval/error
states.

For the native GPUI client:

1. Run `make gpui`; the target signs and verifies
   `dist/Azem-GPUI.app`, including its bundled `azem-daemon`.
2. Launch `open dist/Azem-GPUI.app` or
   `dist/Azem-GPUI.app/Contents/MacOS/Azem --workspace "$PWD"`.
3. Confirm the window reaches `Azem GPUI window ready`, the daemon endpoint is
   created under `~/.azem/gpui-daemons/<workspace-hash>/`, and the current
   project/session snapshot renders.
4. Start a turn, close the window, and reconnect. The daemon PID and active run
   must remain; transcript and terminal state must restore.
5. Exercise conversation, approval/Todo/agent, file/change, PR/security,
   settings/extension/usage, attachment, and terminal navigation as applicable.
6. Run `azem daemon status --workspace "$PWD"`, then stop only after the run is
   terminal. `azem daemon stop` must refuse an active run without
   `--include-active`.

On Windows, launch `dist\windows-amd64\Azem.exe` and repeat steps 4–8. Also
verify one foreground PowerShell command, cancellation of a command that has a
child process, a background command stop, system-font enumeration, clipboard
image paste, browser login, and Credential Manager storage. The WebView2
Runtime is required; Bash hooks require Git Bash, while PowerShell hooks work
with either PowerShell 7 or the built-in Windows PowerShell.

Compilation-only checks for both supported Windows architectures can run on a
non-Windows host without executing the generated test binaries:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -exec=/usr/bin/true ./...
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go test -exec=/usr/bin/true ./...
```

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

The final parity gate also runs Sentrux `scan`, `check_rules`, and `session_end`.
`check_rules` must report zero violations and `session_end` must report no new
cycle or rule regression. `internal/parity.OpenCapabilities()` must be empty.

## Delivery checklist

- Existing user changes remain intact.
- Changed code is formatted and focused tests pass.
- The relevant complete command passes.
- Architecture rules pass with no new cycle.
- Documentation matches current source and commands.
- `git diff --check` passes.
- Desktop changes include packaged-app smoke evidence.
