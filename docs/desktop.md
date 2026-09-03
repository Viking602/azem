# Native Desktop

Last verified: 2026-09-01

Azem ships one desktop implementation: the Rust GPUI client in `gpui/`.
The single GPUI window connects to one workspace-scoped Go daemon at a time
through the authenticated local protocol in `internal/desktopipc/`.

## Build

Requirements:

- Go and Rust versions declared by `go.mod` and `gpui/rust-toolchain.toml`.
- Bun for the AST/LSP runtime packages in `runtime-js/`.
- macOS for the signed application bundle.

```bash
make gpui
codesign --verify --deep --strict dist/Azem-GPUI.app
```

`make gui` is a compatibility alias for `make gpui`; `make test-gui`
aliases `make test-gpui`.

The macOS bundle contains:

```text
dist/Azem-GPUI.app/
  Contents/MacOS/Azem
  Contents/MacOS/azem-daemon
  Contents/Resources/AppIcon.icns
  Contents/Resources/icons/
  Contents/Resources/logos/
  Contents/Resources/locales/
```

## Process and ownership model

```text
single GPUI window
  -> owner-only Unix socket
  -> authenticated framed IPC
  -> workspace azem-daemon
  -> internal/desktop closed operations
  -> internal/app runtime
  -> SQLite, providers, tools, terminals, scans
```

The daemon owns provider streams, durable runs, approvals, terminals, and
SQLite. Closing a renderer does not stop an active run. Reopening the project
authenticates to the existing endpoint and restores the durable snapshot plus
bounded event and terminal replay.

The Stop control sends the selected session and run identity to the daemon.
Process-local active work keeps the non-blocking coordinator-signal path;
persisted pending or suspended work is cancelled through its durable binding.
The renderer shows `stopping`, reports rejected requests, clears pending
controls on the terminal event, and never silently treats `cancelled=false` as
success.

The endpoint lives below
`~/.azem/gpui-daemons/<workspace-hash>/`. Endpoint metadata and socket
permissions are owner-only. The client rejects an unexpected peer identity,
protocol version, workspace binding, or oversized frame.

Opening another project keeps the existing window and reconnects its renderer
to that project's isolated workspace daemon. The previous daemon remains
independent, so active work continues without running recovery against another
daemon holding the shared database fence. One session keeps one immutable
project owner; cross-project navigation reconnects to that owning workspace.

## Closed operation boundary

`internal/desktop/bridge.go` is the product operation boundary.
`internal/desktopipc/dispatcher.go` maps versioned IPC requests onto those
named operations. The transport must not expose an arbitrary filesystem or
shell endpoint.

Workspace browsing is read-only and bounded:

- paths are project-relative and symlink-resolved;
- file trees, Git output, file bytes, and time are capped;
- binary files return metadata or a bounded preview;
- search uses durable SQLite FTS for sessions and bounded project operations
  for files.

The embedded terminal is the deliberate write-capable exception. Go owns the
PTY; GPUI displays bounded output and forwards explicit human keystrokes.
Terminal sessions are not agent tools.

## Projection and reconnect

Daemon events carry monotonically increasing sequence numbers. GPUI reducers in
`gpui/crates/azem-gpui/src/state.rs` keep conversation text phases, tool
lifecycle, approvals, Todos, agents, terminals, settings, projects, PRs, usage,
and security scans typed.

The first reconnect response contains durable session, project, run, approval,
and terminal state. Optional provider, model, extension, usage, and security
catalogs stream after first paint. A replay gap or explicit
`projection_resync` reloads the durable projection; renderer pressure never
becomes a provider failure.

Text phases remain distinct end to end:

```text
provider -> app event -> durable session block -> IPC -> GPUI timeline
             commentary | reasoning | final_answer
```

Final output renders once. Reasoning never impersonates commentary. Live
transcript following uses bounded frame-paced rendering and respects reduced
motion.

Tool activity is rendered as one compact fold per model step. It starts
collapsed; opening it restores the complete body to transcript flow with all
reasoning prose first and de-duplicated tool rows after it. Starting later text
or a tool settles prior streaming prose, so one run has only one live activity
status. Tool labels contain only the action and target; a spinner, check, alert,
or hollow mark carries state without repeating 「正在运行」 in every row.
Multiline command arguments reduce to one executable preview. Any tool with
bounded details is a keyboard-focusable disclosure: failures show the recorded
reason, successful calls show their result, and edits prefer the structured
compact diff. Clickable tool lines remain visually flat with no full-row hover
capsule, so their text keeps the native selection treatment. Details remain in
transcript flow without a nested scroller. Subagent batches keep one click
disclosure per parent call. Reduced motion suppresses status and disclosure
animation without hiding state.

## Desktop surfaces

The native application owns these user-visible flows:

- project/session catalog, global search, archive, and session trees;
- conversation composer, attachments, provider/model selection, Queue/Steer,
  planning, approvals, Todos, tools, diffs, and subagent inspection;
- workspace files, bounded previews, changes, editor view, and structured pull
  requests;
- embedded terminal creation, input, output, resize, close, and reconnect;
- Standard and Deep security scan start, progress, cancel, findings, triage,
  remediation, and SARIF export;
- Settings for providers, model routes, agents, context archive, governance,
  appearance, MCP, Skills, plugins, hooks, usage, and security.

Project rows expose an app-only 「从 Azem 中移除」 action. It changes only the
durable project catalog: workspace files and owned sessions are untouched.
Normal restart access preserves the hidden state; explicitly opening the path
restores it. Removing the active project reconnects the same window to another
visible project; Azem refuses to remove the only active project.

Settings mutations must complete
`event -> daemon state -> persistence -> GPUI redraw -> restart readback`.
Discovery-only provider probes remain transient until the user explicitly
saves.

## Security scan lifecycle

Security scans operate on immutable snapshots. Starting a scan creates the
scan, workers, and governed automation runs. Cancel first cancels every
subagent descended from the scan worker, then cancels the worker run and writes
the terminal scan state. No child execution may remain active after the GUI
shows `canceled`.

Completed scans own canonical reports and SARIF below
`~/.azem/security-scans/`. Failed or canceled scans keep their diagnostic
state without fabricating findings.

## Verification

Run the complete native gate:

```bash
make test-gpui
make gpui
```

For a release or desktop-lifecycle change, verify the signed bundle with a real
GUI session:

1. Open a project and confirm its branch, dirty state, sessions, and PR context.
2. Exercise conversation, model selection, approvals, Todo/agent projections,
   file/change/PR surfaces, settings, extensions, usage, and terminal.
3. Start a Standard scan and a Deep scan; verify progress and cancel
   propagation, then export a completed scan when one is available.
4. Close the renderer during an active turn and reconnect to the same daemon.
5. Restart after persistent settings changes and confirm the visible readback.
6. Confirm the signed bundle still passes `codesign --verify --deep --strict`.

Use `azem daemon status --workspace <path>` before stopping a daemon.
`azem daemon stop` must refuse an active run unless the user explicitly
includes active work.
