# Native tool coverage

Verified against oh-my-pi commit
`85cb52df8d9554b2c83d861cbfeb3eb161fc3b3f` and its
`packages/coding-agent/src/tools/builtin-names.ts` registry on 2026-09-13.
Azem supplies the capabilities of all 29 public entries using Go and native
programs. Names and input schemas follow Azem's governed runtime; this is not
a drop-in OMP extension ABI. No TypeScript, Node, Bun, npm package, or bridge
runtime is required by these implementations.

## Inventory

| OMP entry | Azem implementation | Availability and behavior |
|---|---|---|
| `read` | `coding.read_file` | Existing files, source lines, URLs, documents, archives, images and resource URIs. |
| `bash` | `coding.shell` | Existing governed shell, limits, approvals and background jobs. |
| `edit` | `coding.edit_hashline` | Existing stale-tag checks, atomic commits and rollback. |
| `ast_grep`, `ast_edit` | Same names | Native `ast-grep`; bounded workspace search, rewrite previews and Hashline commits. |
| `ask` | `ask` | Existing session-scoped interactive questions. |
| `debug` | `debug` | Persistent native DAP client: launch, breakpoints, threads, stack, scopes, variables, evaluate, stepping, wait and stop. |
| `eval` | `coding.eval` | Persistent Python 3 cells, Unicode output, last-expression values, reset and timeout cleanup. OMP agent definitions normalize `eval` to this name. |
| `github` | `github` | Native `gh`: repository/file/search/Actions reads plus the existing guarded PR dashboard/detail/create/mutate service. Git checkout and arbitrary push commands remain available through `coding.shell`. |
| `glob`, `grep` | `coding.glob`, `coding.search` | Existing bounded search and source observations. |
| `lsp` | `lsp` | Native stdio LSP for Go, Rust, C/C++ and Python. Navigation, hover, references, symbols, diagnostics, rename, formatting, code actions and raw protocol requests. |
| `inspect_image` | `inspect_image` | Go image decoding and crop; actual image parts for model vision. |
| `browser` | `browser` | Native Chrome/Chromium CDP: tabs, navigation, accessibility snapshot, screenshot, click, type, keys, scroll and evaluation. |
| `computer` | `computer` | macOS CoreGraphics/Accessibility through system Swift: permission status, snapshot, screenshot, application launch and mouse/keyboard input. |
| `checkpoint`, `rewind` | Same names | Existing durable exploration checkpoints and retained reports. Context changes never undo files. |
| `security_scan` | Existing scan runtime | Native source snapshots, workers, evidence and reports. |
| `task` | `subagent.spawn` | Existing roles, capability intersection, scheduling, durability and cancellation. |
| `hub` | `hub` | Existing peers/jobs plus native named process start, stdin send, logs, status, wait, stop and restart. |
| `todo` | `todo` | Existing revisioned plan and completion verification. |
| `web_search` | `web_search` | Direct DuckDuckGo HTTP search; source URLs and snippets. Challenges and provider errors are failures, not empty successful searches. |
| `write` | `coding.write_file` | Existing governed file writes. |
| `memory_edit`, `retain`, `recall` | Same names | Existing workspace-scoped persistent memory. |
| `reflect` | `reflect` | Retrieves relevant memory evidence; the calling model synthesizes conclusions. No invented secondary model answer. |
| `learn` | `learn` | Retains one lesson with provenance through the existing memory store. |
| `manage_skill` | Existing managed skill runtime | Available in the governed learning workflow; authored skills remain separate. |

Additional requested media capabilities are `generate_image` (direct
OpenAI-compatible Images API generation/editing) and `tts` (native speech to
AIFF/WAV). OMP's hidden `goal`, `yield` and `think` are control-plane behavior:
Azem already owns goal state, turn settlement and provider reasoning.

## Prerequisites

| Capability | Host requirement |
|---|---|
| AST | Native `ast-grep`, e.g. `brew install ast-grep`. Project `sgconfig.yml` custom parsers are not loaded. |
| Python cells | `python3`; only Python's standard library is used by the kernel. |
| LSP | `gopls`, `rust-analyzer`, `clangd` or `pylsp` for the selected source language. No TS language server is bundled. |
| Debugger | `lldb-dap`/`lldb-vscode`; macOS also discovers Xcode's `lldb-dap` through `xcrun`. Python debugging additionally needs `debugpy` in the chosen Python installation. |
| Browser | Installed Chrome/Chromium. Each conversation/agent owns an isolated temporary profile; existing signed-in browser sessions are not imported. |
| Desktop | macOS, system Swift/Xcode command-line tools, and Accessibility/Screen Recording grants for applicable actions. `status` only reads grants. Other OSes return an explicit unsupported error. |
| GitHub | Installed authenticated `gh`; existing PR service requires it on the daemon's `PATH`. |
| Speech | macOS `say` (`.aiff`); elsewhere `espeak-ng`/`espeak` (`.wav`). |
| Image generation | `OPENAI_API_KEY`; optional trusted HTTPS `AZEM_IMAGE_BASE_URL` (default `https://api.openai.com/v1`). Default model `gpt-image-1`. |

Native subprocess discovery also checks Homebrew, `/usr/local/bin`, and the
user's `go/bin`, `.cargo/bin`, and `.local/bin`. These directories are appended
to child PATH without executing shell startup files or changing global state.
Missing dependencies produce actionable tool errors. An advertised tool does
not imply an installed language server, a cloud credential or an OS grant.

## Invocation examples

```json
{"code":"answer = 41\nanswer + 1"}
{"action":"definition","file":"main.go","line":12,"symbol":"Handle"}
{"action":"rename","file":"main.go","line":12,"symbol":"Handle","new_name":"Serve","patch":true}
{"pat":"const $NAME = $VALUE","lang":"go","path":"internal/**/*.go"}
{"pat":"const $NAME = $VALUE","lang":"go","path":"fixture.go","rewrite":"const $NAME = 43","apply":true}
{"op":"start","name":"server","application":"go","args":["run","./cmd/server"]}
{"op":"logs","name":"server"}
{"action":"open","url":"https://example.org","headless":true}
{"action":"snapshot"}
{"action":"start","program":"build/demo","file":"main.c","lines":[12]}
{"action":"wait"}
{"op":"search_code","repo":"owner/repo","query":"Handle","limit":10}
{"path":"assets/source.png","crop":[0,0,256,256]}
{"prompt":"A blue circle on white","output_path":"assets/circle.png"}
{"text":"Verification finished.","output_path":"artifacts/verification.aiff"}
```

Examples are separate calls to the corresponding tools. LSP `patch:true`
returns a reviewable Hashline patch; apply that patch with
`coding.edit_hashline`. It never claims to have applied a preview. Code actions
return alternatives for the caller to inspect. LSP positions are one-based
Unicode characters; the wire converts them to UTF-16 positions.

## State, policy and bounds

Python, LSP, browser, debugger and named Hub process sessions are keyed by
workspace, conversation and agent. They persist across tool calls while the
daemon lives. Daemon shutdown terminates owned process groups and removes
temporary browser profiles; restart creates new protocol/kernel state. Durable
tool records remain authoritative and completed effects are not replayed.
Named Hub processes participate in daemon active-work detection.

The native service caps retained process slots at 64, reclaims settled slots
when needed, serializes each protocol session and bounds frames at 4 MiB
(browser CDP responses at 8 MiB). Hub stdout/stderr retain the latest 4 MiB
each with a truncation marker. Hub supervises pipes, not a PTY; interactive
terminal applications use Azem's existing desktop terminal.

Execution tools use the existing approval/action boundary. Language servers
may execute compiler plugins and build scripts. Python, LSP, debugger and Hub
reject network-deny workspaces because host processes cannot enforce that
restriction. Search, GitHub, browser and image APIs require network `allow`.
Desktop control is an explicit external side effect. OS sandboxing remains the
operator's responsibility; these tools are not process or network sandboxes.

AST scope is limited to 1,000 UTF-8 files, 1 MiB per file, and at most 500
matches. Incomplete edits fail before mutation. Hashline guards the actual
commit against stale content. AST commits retain the same durable file
observations and UI diffs as ordinary Hashline edits.

Media outputs must be new workspace files. Root-relative writes reject
symlink escapes and existing destinations before contacting a chargeable
provider. Image input/output is limited to 4 MiB and 32 million pixels;
speech output to 32 MiB. Image responses must contain one valid base64 PNG.
Requests do not follow credential-bearing redirects or echo provider error
bodies. Generated files are recorded and their verification hashes are read
incrementally, without decoding media or consuming the source-text budget.
Each evidence capture has a separate 32 MiB media budget.

Tool definitions are stable for a fixed workspace policy and role. Adding
this inventory changes the tool-schema fingerprint once; subsequent turns
retain the same prefix. Default Vibe/Fusion workers receive the full governed
inventory. Explore/plan/review receive native read tools. Explicit user role
allowlists still intersect with capability mode and are not silently widened.

## Verification

`internal/agent/native_tools_test.go` exercises real Python persistence,
isolation/reset/timeout, native AST rewrites, real gopls navigation, named
process lifecycle, media HTTP contracts, crop and output path guards. Tests
requiring installed language tools skip with an explicit reason when missing.

```sh
GOWORK=off go test ./internal/agent ./internal/app ./internal/config ./internal/toolview
AZEM_NATIVE_SMOKE=1 GOWORK=off go test ./internal/agent -run TestNative -count=1
AZEM_NATIVE_NETWORK_SMOKE=1 GOWORK=off go test ./internal/agent -run TestNativePublicNetworkSmoke -count=1
GOWORK=off go test ./internal/agent ./internal/app -race -run 'Test(Native|Vibe|AgentHub|Checkpoint)' -count=1
GOWORK=off go test ./...
```

The native smoke opens an isolated headless Chrome, compiles and debugs a
temporary C program, synthesizes a temporary audio file, and queries existing
macOS permissions. The network smoke reads public search and GitHub results.
Image API tests use a local HTTP fixture and do not charge a real account.
