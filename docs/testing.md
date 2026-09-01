# Testing

Last verified: 2026-08-30

Azem spans a Go runtime, SQLite, Bubble Tea, native GPUI, and a
versioned local IPC boundary. Passing one package is not enough when a change
crosses those boundaries. Start with the narrowest relevant check, then run the
complete check for the affected surface.

## Required tools

- Go 1.25.8 or later; `go.mod` selects toolchain 1.25.12.
- Bun 1.3.14 or later for the AST/LSP runtime packages in `runtime-js/`.
- Rust 1.97.1 for GPUI; `gpui/rust-toolchain.toml` selects the pinned toolchain.
- macOS for the current packaged GPUI smoke procedure.
- Sentrux 0.5.7 for repository architecture rules.
- Authenticated provider and GitHub CLI accounts only for explicit live tests.

## Core commands

Complete Go suite using declared modules rather than a local workspace:

```bash
GOWORK=off go test ./...
```

Native IPC, daemon, Rust formatting, strict Clippy, protocol tests, and GPUI
state/UI-model tests (`make test-gui` is an alias):

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

Pinned Venat v0.16.1 contract surface:

```bash
GOWORK=off go test \
  github.com/Viking602/venat/agent \
  github.com/Viking602/venat/message \
  github.com/Viking602/venat/provider/... \
  github.com/Viking602/venat/tool/... \
  github.com/Viking602/venat/skill/... \
  github.com/Viking602/venat/orchestration \
  github.com/Viking602/venat/durable/...
```

Azem runtime integration and race boundary:

```bash
GOWORK=off go test ./internal/agent ./internal/app ./internal/recovery ./internal/mcp ./internal/hooks ./internal/provider/cursor
GOWORK=off go test -race ./internal/agent ./internal/app ./internal/recovery ./internal/mcp ./internal/hooks ./internal/provider/cursor
```


Packaged native desktop:

```bash
make gui
```

## Verification by change type

| Change | Narrow check | Complete check |
|---|---|---|
| Go package | `go test ./internal/<package>` | `GOWORK=off go test ./...` when runtime or shared behavior changes |
| Go formatting | `gofmt -w <changed.go>` | `git diff --check` |
| GPUI IPC or daemon lifecycle | `GOWORK=off go test ./internal/desktopipc ./internal/daemon` | `make test-gpui`, `make gpui`, renderer detach/reconnect smoke |
| GPUI renderer/state | Matching `cargo test -p azem-gpui <test>` from `gpui/` | `make test-gpui`, `make gpui`, real native window launch |
| Workspace file browser | `go test ./internal/desktop -run Workspace` and matching GPUI state tests | `make test-gpui`, `make gpui`, real tree/text/image/binary smoke |
| SQLite migration/adapter | `GOWORK=off go test ./internal/store/sqlite` | `GOWORK=off go test ./...` plus a real schema-26 copy upgraded/reopened with retained rows and v1 blob verification |
| Venat version/contract | Pinned upstream command above, then affected Azem packages | `GOWORK=off go mod tidy`, full Go suite, race boundary, contracts/architecture checks, both desktop builds and smoke |
| Provider streaming | Provider parser/driver plus durable model/tool-attempt tests | App runtime, recovery, session persistence, and GPUI state tests |
| GitHub PR backend | `go test ./internal/githubpr ./internal/desktop` | Success and failure paths with authenticated `gh` when mutations change |
| Prompt or bundled Skill | Matching app/agent/config/Skills tests | Real conversation path |
| Native security scan | `go test ./internal/securityscan ./internal/app ./internal/store/sqlite` and matching GPUI state tests | `GOWORK=off go test ./...`, `make test-gpui`, `make gpui`, real Standard/Deep start-cancel and export smoke |
| OMP parity manifest | `GOWORK=off go test ./internal/parity` | Every frozen v18.0.3 capability is `complete` or `stronger`; then run all relevant surface checks |
| Coding tools / extension broker | Matching `internal/agent` and `internal/customtools` cases | Real read/write/Hashline/AST/shell fixtures plus browser/DAP/LSP smoke |
| Session tree/import/export/share | `go test ./internal/session ./internal/sessionimport ./internal/sessionexport ./internal/sessionshare` | SQLite migration/reopen and collaboration/protocol suites |
| JSON-RPC / ACP / headless / Go API | `go test . ./internal/rpc ./internal/acp ./internal/headless` | Actual CLI startup or client fixture for the changed transport |
| Marketplace | `go test ./internal/plugins ./internal/app` plus matching GPUI settings tests | Desktop/TUI source, discover, scoped install, update, upgrade, disable, and uninstall paths |
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

For schema 27, also copy a real schema-26 database to an isolated temporary
Azem home, upgrade it, reopen it, and prove legacy run/tool/approval rows remain
readable. Exercise a v1 execution whose continuation or manifest spills to
BlobStore, then verify digest-checked restart hydration, response-loss
reconciliation, and unknown-attempt non-replay. Never run upgrade experiments
against the user's live database.

For schema 28, upgrade a schema-27 fixture with visible project rows and a
stranded running subagent, then reopen it. Verify visibility defaults to 1,
the stale child becomes interrupted, and hiding/reopening a project leaves its
files and `session_workspaces` ownership unchanged.


## Desktop smoke test

A desktop behavior or build change is complete only after the packaged GPUI
binary starts. On macOS:

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
