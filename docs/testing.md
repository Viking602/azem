# Testing

Last verified: 2026-09-03

Azem spans a Go runtime, SQLite, Bubble Tea, native GPUI and a
versioned local IPC boundary. Passing one package is not enough when a change
crosses those boundaries. Start with the narrowest relevant check, then run the
complete check for the affected surface.

## Required tools

- Go 1.25.8 or later; `go.mod` selects toolchain 1.25.12.
- Rust 1.97.1 for GPUI; `gpui/rust-toolchain.toml` selects the pinned toolchain.
- macOS for the packaged GPUI smoke procedures.
- Sentrux 0.5.7 for repository architecture rules.
- Authenticated provider and GitHub CLI accounts only for explicit live tests.

## Core commands

Complete Go suite using declared modules rather than a local workspace:

```bash
GOWORK=off go test ./...
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

Evidence-loading CPU/allocation ablation (isolated synthetic long conversation):

```bash
GOWORK=off go test ./internal/session -run '^$' -bench BenchmarkEvidenceProjectionAblation -benchmem -count=3
```

The fixed fixture contains 40 transcript blocks and 1,000 tool records, of which
100 belong to the selected run. Stages remove assistant-block hydration and then
unrelated tool records. Compare the same `CurrentRunOnly` benchmark on both
revisions; report `B/op` as transient allocation, not retained memory. For CPU and
peak RSS, compile each revision's test binary and run it separately with
`/usr/bin/time -l`, `-test.benchtime=1000x`, and the same benchmark filter. Do not
run builds or other benchmarks concurrently. These process totals include fixture
setup and do not measure the renderer or whole-app idle resource usage.

Measured on 2026-09-07, Apple M3 Max, macOS arm64, Go 1.25.12. Three
alternating before/after runs, 1,000 iterations each, after builds finished:

| Metric (median) | Decode-only run filtering | SQL run filtering |
|---|---:|---:|
| CPU time, user + system, including fixture setup | 11.84 s | 4.27 s |
| Peak process RSS, including fixture setup | 56.95 MiB | 46.42 MiB |
| Time per evidence projection | 7.693 ms | 2.172 ms |
| Allocated bytes per projection | 14.665 MB | 2.304 MB |
| Allocations per projection | 21,575 | 2,932 |

The same fixture's staged ablation used about 22.43 MB/op with the full
transcript, 14.86 MB/op with only user blocks but all tools, and 2.30 MB/op
with user blocks and SQL-selected current-run tools. Merely skipping unrelated
tool decoding still allocated 14.66 MB/op because SQL had already materialized
all payloads. The retained fix filters before row materialization, preserving
ordering, parameter binding, session isolation, and selected-record validation.
No renderer animation was removed and no cache or retention policy was added.

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

Cross-repository provider contracts:

```bash
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


Packaged native GPUI desktop:

```bash
make gpui
```

## Verification by change type

| Change | Narrow check | Complete check |
|---|---|---|
| Go package | `go test ./internal/<package>` | `GOWORK=off go test ./...` when runtime or shared behavior changes |
| Go formatting | `gofmt -w <changed.go>` | `git diff --check` |
| Workspace IPC, daemon lifecycle, or client takeover | `GOWORK=off go test ./internal/desktopipc ./internal/daemon ./internal/desktopclient` | `make test-gpui`, `make gpui`, and TUI/GPUI detach-reconnect smoke |
| GPUI renderer/state | Matching `cargo test -p azem-gpui <test>` from `gpui/` | `make test-gpui`, `make gpui`, real native window launch |
| Workspace file browser | `go test ./internal/desktop -run Workspace` and matching GPUI state tests | `make test-gpui`, `make gpui`, real tree/text/image/binary smoke |
| SQLite migration/adapter | `GOWORK=off go test ./internal/store/sqlite ./internal/session` | `GOWORK=off go test ./...` plus previous-schema upgrade/reopen, retained rows, queue spill hydration, and future rejection |
| Venat version/contract | Pinned upstream command above, then affected Azem packages | `GOWORK=off go mod tidy`, full Go suite, race boundary, contracts/architecture checks, native desktop build and smoke |
| Provider streaming | Provider parser/driver plus durable model/tool-attempt tests | App runtime, recovery, session persistence, and GPUI state tests |
| GitHub PR backend | `go test ./internal/githubpr ./internal/desktop` | Success and failure paths with authenticated `gh` when mutations change |
| Prompt or bundled Skill | Matching app/agent/config/Skills tests | Real conversation path |
| Native security scan | `go test ./internal/securityscan ./internal/app ./internal/store/sqlite` and matching GPUI state tests | `GOWORK=off go test ./...`, `make test-gpui`, `make gpui`, real Standard/Deep start-cancel and export smoke |
| Coding tools | Matching `internal/agent` cases | Real read/write/Hashline/shell/job fixtures |
| Session tree/import/export/share | `go test ./internal/session ./internal/sessionimport ./internal/sessionexport ./internal/sessionshare` | SQLite migration/reopen and collaboration/protocol suites |
| JSON-RPC / ACP / headless / Go API | `go test . ./internal/rpc ./internal/acp ./internal/headless` | Actual CLI startup or client fixture for the changed transport |
| Marketplace | `go test ./internal/plugins ./internal/app` plus matching GPUI settings tests | Desktop/TUI source, discover, scoped install, update, upgrade, disable, and uninstall paths |
| Auth broker/gateway/webhook | Matching `internal/authbroker`, `authgateway`, or `githubwebhook` package | Bearer/HMAC failure, redelivery/cache, refresh/block, and successful forwarding/trigger paths |
| Harbor eval adapter | `PYTHONPATH="$PWD" python3 -m unittest eval.harbor.timeout_test` | `make azem-eval-linux` and a real `harbor run` when the adapter command or timeout wiring changes |
| Adaptive eval / learning | `GOWORK=off go test ./internal/eval ./internal/workrevision ./internal/evidence ./internal/codingmemory ./internal/assets ./internal/routeeval ./internal/training ./internal/toollab ./internal/adapterdeployment` | Add `./internal/app ./internal/tui` when adapter routing or evidence-status projection changes; production routing must remain unchanged unless a validated registry is explicitly attached |
| Documentation/build command | Link/path check and run every documented command | `git diff --check` |

Native tool protocol and OS smoke commands, prerequisites and their external
effects are documented in [native tool coverage](native-tools.md#verification).
Image-provider tests use local HTTP fixtures; optional browser/debugger/speech
smokes exercise actual native programs in temporary workspaces.

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

For schema 29, upgrade a schema-28 fixture containing existing session state,
then reopen it. Verify `session_prompt_queues` starts empty without changing the
session, and cover CAS conflict projection, one-winner concurrent updates,
4 KiB BlobStore spill/reopen, attachment retention, invalid/corrupt document
rejection, FIFO dispatch, cancellation pause, and dispatching-run restart
reconciliation.


## Desktop smoke tests

An interactive-client or desktop behavior change is complete only after the
packaged clients and workspace daemon start. On macOS:

1. Run `make gpui`; the target bundles, signs, and verifies
   `dist/Azem-GPUI.app`, including `azem-daemon` and its sibling `rg`.
2. Run
   `env -i PATH=/nonexistent dist/Azem-GPUI.app/Contents/MacOS/rg --version`
   to prove packaged search has no system-`PATH` dependency.
3. Launch `open dist/Azem-GPUI.app` or
   `dist/Azem-GPUI.app/Contents/MacOS/Azem --workspace "$PWD"`.
4. Confirm the window reaches `Azem GPUI window ready`, the daemon endpoint is
   created under `~/.azem/gpui-daemons/<workspace-hash>/`, and the current
   project/session snapshot renders.
5. Start a turn, close the window, and reconnect. The daemon PID and active run
   must remain; transcript and terminal state must restore.
6. Exercise conversation, approval/Todo/agent, file/change, PR/security,
   settings/extension/usage, attachment, and terminal navigation as applicable.
7. Run `azem daemon status --workspace "$PWD"`, then stop only after the run is
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

Fusion has a separate opt-in test using an isolated workspace, database and
configuration. It reads local ChatGPT/Grok credentials without modifying their
source store. Exact requested model IDs must appear in the account catalogs;
there is no model fallback. Its default pair is Astra and Grok 4.6, with `high`
reasoning where supported:

```bash
AZEM_LIVE_FUSION=1 \
AZEM_LIVE_FUSION_LEAD_MODEL=gpt-6-astra \
AZEM_LIVE_FUSION_SIDEKICK_MODEL=grok-4.6 \
AZEM_LIVE_FUSION_SCENARIO=transfer \
AZEM_LIVE_FUSION_EVIDENCE_DIR=/private/tmp/azem-fusion-evidence \
GOWORK=off go test -tags=live ./internal/app \
  -run '^TestLiveFusionCrossProviderHandoffs$' -count=1 -timeout=22m -v
```

Run separately with `transfer`, `repair`, `restart`, and `long`. They cover two handoffs,
diagnosis and repair of failing Python tests with an independent hidden check,
and a second user turn after runtime restart with the original input file removed.
The restart harness must explicitly select the original session after Bootstrap;
the newly bootstrapped session ID is not evidence of restored history.
The long case uses six continuous handoffs to implement and verify a CSV CLI;
the harness independently checks all 5,000 input rows and preserves the tests.
Evidence includes persisted root-run/model bindings, complete Sidekick tool
call/result pairs, exact final answers, output hashes and both model usage rows.
Five-second runtime snapshots capture state, active children, Stop availability,
pending controls and projection latency; completion must settle without active work.
The actual `provider_requests` ledger is exported for per-request cache analysis.
Live-only transport instrumentation records request-field/input-item hashes and
bounded, redacted rejection details without exporting credentials or prompt bodies.
Compute token-weighted cache hits from reported cached/input tokens, separately
for each model, retaining failed and unreported requests instead of treating them
as zero hits. Inspect the first request, later requests, and handoff boundaries
separately; a short live run does not qualify hours-long operation or compaction.
See [the Astra/Grok cache verification report](../experiments/fusion-cache-20260912/README.md)
for measured handoff cache behavior, and
[the follow-up diagnosis](../experiments/fusion-cache-auth-20260912/README.md)
for request-prefix checks, corrected restart testing and typed 403 settlement.

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

The final architecture gate also runs Sentrux `scan`, `check_rules`, and
`session_end`. `check_rules` must report zero violations and `session_end` must
report no new cycle or rule regression.

## Delivery checklist

- Existing user changes remain intact.
- Changed code is formatted and focused tests pass.
- The relevant complete command passes.
- Architecture rules pass with no new cycle.
- Documentation matches current source and commands.
- `git diff --check` passes.
- Desktop changes include packaged-app smoke evidence.

## Devin personal Auth and transport

`GOWORK=off go test -race ./internal/auth/devin ./internal/provider/devin`
checks PKCE/state, cancellation, credential-response bounds, account model
inventory, router assignment, two-turn tool replay, phase/usage mapping,
truncation/error handling, protobuf/gzip bounds and account isolation.
`TestPersonalDevinLoginPersistsAndExpires` covers SQLite login/logout and expiry;
`TestDevinAccountCatalogRoutesAndRefresh` covers exact account routing and
replacement/stale catalogs. The shared model-availability regression runs
against all four subscription providers.

Live acceptance: build `make gpui`, open Settings → Models → Devin and sign
in with a personal account. Verify the returned catalog, select one returned
model and complete two turns including a read-only tool. Restart and confirm
account/catalog persistence. Protocol fixtures do not establish live account
entitlement, quota, or current upstream compatibility.

`TestQuotaUsesPersonalSessionAndReportedWindows` covers personal CLI metadata,
daily/weekly quotas, omitted zero percentages, 64-bit timestamps/balances,
missing/invalid payloads and redacted authentication failures.
`TestDevinQuotaUsesSharedAsyncProjection` verifies native quota delivery without
blocking the provider directory. Native model-selection tests cover legacy opaque
IDs, depth/Fast switching, 1M boundaries, Fusion companions and disabled variants.
