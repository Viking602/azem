# Desktop application

Last verified: 2026-08-17

Azem's desktop application is a Wails window over the same Go runtime used by
the TUI. React owns presentation state; it does not duplicate provider,
approval, session, or persistence behavior.

## Startup and Bridge

`cmd/azem-gui/main.go` creates the Wails application, embeds the production
frontend, and registers `internal/desktop.Bridge`. The Bridge exposes a closed
set of typed methods. Runtime mutations continue through validated application
actions; read-only desktop integrations use focused methods with their own
input and output limits.

Desktop bootstrap primes sessions, git branches, model routes, agent types,
Skills, plugins, and Hooks. Skills and Hooks also expose direct read-only
Bridge methods (`SkillCatalog()`, `HookCatalog()`) so Settings can project the
current snapshot after subscribe, without waiting for a `list_*` event that
may have been emitted before the frontend listener. Usage is not primed:
Settings → Usage calls `Bridge.UsageReport(scope)` only when that page opens
or the user refreshes, so the event broker is not polled. `hook_catalog`,
`skill_catalog`, `plugin_catalog`, and `usage_report` remain replaceable: a
later sequence must not drop a catalog snapshot that the renderer still needs.

Runtime events follow this path:

```text
internal/app event broker
  -> desktop Bridge subscription
  -> frontend/src/bridge.ts
  -> frontend runtime store
  -> timeline and control surfaces
```

Session restoration and project ownership remain durable SQLite state. One
desktop window owns one workspace-scoped runtime; opening a session from a
different project opens it with that project's workspace. All windows retain a
shared process-lifetime recovery fence. Starting the new workspace runtime does
not run crash recovery against a conversation still executing in an existing
window. Merely selecting another conversation also does not emit `SessionEnd`;
session hooks close once when their owning application process shuts down.

Desktop bootstrap also installs the shared outbound proxy resolver before any
provider, authentication, model-catalog, MCP, or plugin client is constructed.
On macOS it reads SystemConfiguration directly, so launching Azem from Finder
uses the same active system proxy as Codex/Electron even though no terminal
environment variables are inherited. The resolver refreshes native settings
without restarting the app; matching environment variables remain explicit
per-process overrides.

## Application shell and navigation

The desktop shell keeps project and branch context in the compact global title
bar. The sidebar has two stable scopes: **Conversations** for project-owned
session history and **Workspace** for the active repository overview. Every
project row exposes a project-scoped new-conversation action; Pull Request state
stays attached to the owning project instead of becoming a global empty page.
The Workspace header also exposes a focused **Open terminal** action. Its Bridge
method launches the operating-system terminal with the active workspace as the
working directory by passing an argv-style command directly to the platform;
it does not expose a generic shell executor to the WebView. The in-app bottom
panel is a separate human-only PTY and does not replace that host-terminal
action.

The Workspace overview is the parent route for repository work. It combines the
current branch, bounded working-tree summary, current Pull Request, repository
status, and recent project sessions. Files and Changes are child routes and
always expose an explicit Back to Workspace control; Escape follows the same
route. Command-N starts a conversation in the current project, while Command-2
and Command-3 open Files and Changes.

Command-K opens one global search surface over actions, settings, session
titles, and durable conversation content. Settings and configured catalog
entries are filtered in memory because that catalog is already bounded UI
state. Session content stays in SQLite and uses the existing FTS5 history
index; the direct read-only Bridge method returns at most 30 rows containing
only the session owner, title, match kind, stable block sequence, timestamp,
and a 24-token snippet. The command surface waits 160 ms after input, ignores
responses from older queries, and never downloads complete transcripts.

Selecting a settings result opens its owning section and scrolls to the stable
setting identifier. Selecting a message result resumes the owning session and
reads its durable projection directly back to the initiating window, then
scrolls to the canonical block sequence. This readback avoids relying on the
timing of a cross-window event broadcast. Long transcripts temporarily realize
only the matching turn before measuring its scroll position, then return to
normal `content-visibility` virtualization. A result owned by another project
opens that isolated workspace runtime with only the session ID and sequence in
the startup arguments; the search text is not passed to the child process.

Session rows project the process-wide main-run owner independently from the
conversation currently on screen. If that foreign run succeeds or fails, the
frontend immediately adds a blue unread dot and persists it through the typed
`mark_session_unread` action. Cancellation does not create a notification, and
subagent terminal events cannot mark a main conversation unread. Resuming the
session clears the persisted flag before its transcript is projected.

Subagent activity uses one two-level interaction model. The parent conversation
shows a compact collaboration summary and an expandable roster with every real
task name; it never substitutes an opaque `+N` count. Selecting a member opens
a right overlay drawer with the task, live status and elapsed time, runtime
metadata, and durable child transcript. The drawer overlays the parent surface
instead of adding another workspace grid column, so inspecting a child cannot
reflow or narrow the parent conversation. The member switcher stays icon-only
but exposes each complete name and state through its accessible label, title,
and keyboard tab behavior.

Settings use one Codex-style full-window layout with a searchable left
navigation and a consistent content column. Opening Settings focuses the dialog
surface rather than 返回工作台, so the WebView does not draw a default focus
ring on that control. Escape and the back control still close Settings; Tab
still reaches the back control and uses the product `:focus-visible` ring. Model catalog, model routing,
Subagents, Approvals, Appearance, Extensions, Archive, and Usage remain
complete sections rather than separate modal variants. Usage is a read-only
ledger of completed `provider_requests` (and completed skill activations when
those rows exist). The query is bounded to the last 366 local-calendar days
and at most 20 models and 20 skills. Cache read/write follow the inclusive
reported-fact rule: unknown providers stay unreported instead of becoming
zero. Missing metrics render as —; an empty database does not invent a
heatmap. Archive lists archived
conversations grouped by owning project; groups start collapsed and paginate
rows, and each row shows its project. It can bulk-archive unpinned
sessions that have been idle for a chosen number of days, and restores a
conversation to that project's sidebar. The current conversation and pinned
rows are never bulk-archived. Opening an archived conversation unarchives it. The Subagents section groups capacity and isolation controls, a read-only
parallel scheduling note, and main-session display behavior. It updates the
live subagent capacity, recursive depth, independent shell capacity,
foreground wait window, and idle-cancel window, then persists those validated
values to the existing configuration file. The wait defaults to until the
foreground child completes (`0` / `0s`). A limited window never cancels a
child: ending it only releases the parent call, safe work becomes background
work, and shared-workspace writes keep waiting. Idle cancel defaults to 5 minutes
(`5m`); `0` / `0s` disables it. A positive window cancels a running child that
stays silent. Opening a child drawer hydrates through `inspect_agent` and
keeps later thinking deltas; a `projection_resync` for that child also
re-inspects the open drawer instead of refreshing only the main session.
Extensions contains the shared Skill loading manager used by the secondary
Extensions page: discovered Skills stay searchable when stopped, and the
accessible switch sends only the typed `set_skill_enabled` action. The runtime
publishes the new catalog only after its node-preserving configuration write
succeeds, so a failed save cannot make the current window disagree with the
next launch. MCP is a separate first-class tab driven by `mcp_state` snapshots,
not by plugin counts. It exposes live server state, imported tool count,
refresh/reconnect controls, persistent enable switches, and a validated add
modal for stdio and Streamable HTTP services. The add form is a centered
overlay hosted on the settings `<dialog>`, not a side drawer. Every entry exposes a confirmed
delete action that removes the node-preserved configuration and live manager
entry together, then records a restart-safe tombstone so catalogs cannot
recreate it.
The add, enable, and delete actions reuse the active manager instance, while
connections start and stop in the background so Settings never blocks on MCP
lifecycle work.

The Hooks tab lists plugin, user, and project hook sources together with every
discovered command. Opening Settings and the refresh control read
`Bridge.HookCatalog()` directly, so a populated runtime cannot look empty
until the user clicks refresh. Plugin hooks remain untrusted until the user
turns on **Trust plugin hooks**. That control persists `plugins.trust_hooks`,
reloads the plugin hook sources immediately, and asks for confirmation before
enabling. The confirm overlay matches the MCP/plugin delete dialog: one-line
title, warning copy, and right-aligned actions. It is hosted on the settings
`<dialog>` so the primary **信任并启用** button stays clickable above the
scrollable settings page. After a successful write the tab reads the catalog
back so the switch stays on and imported plugin commands enter the list. A
failed write stays on the Extensions error banner and does not flip the
switch. Turning it off unloads plugin hooks without removing the packages.
Each visible command has its own switch (`set_hook_enabled` /
`hooks.disabled`). An untrusted plugin hook that is marked enabled still does
not run.

The Plugins tab calls the capability simply **Plugins**, lists local,
available-from-Codex, and selected Codex entries as compact rows grouped by
import state, and provides an explicit per-plugin import control. Selecting a
Codex entry copies it into Azem's `plugin-packages` directory and loads its
Skills and MCP servers immediately. Removing the selection unloads those
capabilities without deleting the dormant Azem copy. Valid icons are projected as
bounded image data; the frontend never receives a local path or presents a
Codex cache path as an active runtime source. Opening Settings explicitly requests the
current plugin snapshot, so startup event timing cannot leave a populated
runtime looking like an empty catalog.

Live assistant text renders new grapheme clusters as a bounded per-character
fade-and-rise tail. The already settled prefix becomes plain text, so long
streams do not accumulate animation nodes. `prefers-reduced-motion` bypasses the
effect and renders the complete current text directly.

The Live context Inspector keeps provider facts and estimated wire composition
visually separate. Occupancy and cache hit rate come from the main agent only;
subagent usage is persisted on the session's subagent counters and is not
mixed into this kernel. Cache hit rate is calculated only from input for which the
provider reported cache semantics; unsupported providers show **Not reported**
instead of a misleading zero. The summary shows the matching cached-input
tokens as **Cache hits** and the cache-reporting input denominator as **Total
cache**, so both numbers reconcile with the displayed hit rate. Context
composition uses the runtime request profile and marks its token estimates
explicitly; category rows expand to the bounded concrete contributions such as
individual messages, tool results, Skill payloads, and MCP definitions.
Sources in the same Inspector collect image attachments, URLs typed into user
messages, and URLs returned by web-search or web-fetch tools. Generic
attachment names such as `image.png` become numbered labels. Clicking an image
opens the existing preview lightbox; clicking a URL opens it in the system
browser through `openExternalURL`.

The same scroll surface also restores the current session's durable recap and
updates it directly from `recap_state` after each successful turn. The card
shows the bounded summary, current goal, open items, covered run boundary, and
revision; an empty session renders an explicit not-yet-generated state instead
of silently omitting the capability.

The Inspector task plan renders each durable Todo phase as a Beautiful UI Task
Row capsule: status mark, title, completed/total metric from the phase items,
status badge, and an expand rail for those items. Item status stays
`pending` / `in_progress` / `completed` / `cancelled`. The section still shows
the plan-level `done / total` progress bar. Commentary and thinking chrome are
unchanged.

## Workspace file browser

The Workspace tab provides a Codex-style file tree and read-only file viewer.
Directories load only when expanded. Open files remain in a bounded tab strip,
and long text files use a virtual line renderer so scrolling cost depends on
the viewport rather than the whole document.

The backend, not the React client, enforces the file boundary:

- paths must be relative to the active workspace;
- `..`, absolute paths, NUL bytes, and symlinks resolving outside the workspace
  are rejected;
- `.git` and `.DS_Store` are omitted from directory results;
- one directory response is capped at 2,000 entries;
- text previews are capped at 2 MiB and report truncation;
- PNG, JPEG, GIF, and WebP previews are capped at 8 MiB;
- binary files return metadata but never arbitrary bytes to the WebView.

The tree is a browsing surface, not an agent tool. It cannot write files and
does not bypass tool approval rules. Editing remains on the governed tool path.

## Workspace change review

The Environment panel's Changes row opens a dedicated read-only review page.
The first request returns only the current branch, aggregate line counts, and
changed-file metadata. File patches are fetched individually when a review card
is expanded, so a large working tree does not send every diff to the WebView at
once.

Changed files are grouped by relative directory and can be filtered by path.
Directories expand automatically only for small change sets; more than 24 files
start folded. The main review column renders at most 60 file headers at once and
keeps the remainder reachable through the directory tree. Git hunk gaps render
as folded unmodified-line rows. Binary files, a patch larger than 6 MiB, and a
change list larger than 4,000 files have explicit bounded states.

`WorkspaceChanges` and `WorkspaceChange` invoke Git with argv, disable external
diffs and pagers, use a 12-second deadline, and capture bounded output. The
single-file endpoint accepts only a path currently reported by Git status;
absolute paths, NUL bytes, parent traversal, and unchanged paths are rejected.
Both methods are read-only and never stage, restore, commit, or mutate files.

## Frontend ownership

- `frontend/src/App.tsx` owns top-level navigation and keeps the session surface
  mounted where required by active runs.
- `frontend/src/store.ts` owns runtime projection state.
- `frontend/src/bridge.ts` is the typed Wails call boundary and supplies demo
  data only outside the desktop runtime.
- `frontend/src/components/WorkspaceFilesPage.tsx` owns tree expansion, file
  tabs, preview selection, lightweight syntax coloring, and viewport
  virtualization. The tree omits files ignored by Git so generated binaries,
  build directories, and local tool output do not obscure project sources.
- `frontend/src/components/WorkspaceChangesPage.tsx` owns changed-file tree
  folding, lazy patch loading, hunk parsing, and review rendering.
- `frontend/src/components/WorkspaceOverviewPage.tsx` owns the repository
  overview and routes into Files, Changes, Pull Requests, and project sessions.
- `frontend/src/components/TerminalPanel.tsx` owns the bottom PTY panel, tabs,
  and one xterm instance per session. Session state lives in
  `frontend/src/terminalStore.ts`, not the runtime transcript store.
- `frontend/src/components/ThreadSurface.tsx` owns the session transcript and
  the bottom composer. The composer stop control calls `CancelActive(true)`
  so a user stop cancels the parent and every child of that run
  (SUBAGENT-006). The dock overlays the timeline as a transparent,
  `pointer-events: none` layer so conversation remains visible and scrollable
  beside the solid input card; only the card, queue, and jump-latest control
  receive pointer events. There is no fade, mask, or scrim above or around
  the card; the dock itself is never an opaque full-width mask. Transcript
  bottom padding equals the measured dock height plus 16px so the last live
  tool card keeps a clear gap above the input; the empty welcome composer
  does not use that overlay gap. Inspector, when open, stays a normal
  right-hand panel.
-   `frontend/src/components/Timeline.tsx` owns bounded streaming reveal and live
  Markdown rendering. The production renderer keeps the latest eight provider
  deltas as short fade/blur ranges inside the parsed Markdown tree, so headings,
  lists, emphasis, and code render immediately while only newly appended text
  animates.   Completion leaves that same mounted tree in place and only stops
  reveal/caret CSS (`content: none` on the idle caret, not an opacity-only
  leftover); it does not swap to a second Markdown renderer. Fenced
  code uses the Beautiful UI Code Block card (filename when
  the info-string looks like a path, otherwise a language label, plus copy and
  a line-number gutter) through the shared `StreamingMarkdown`
  renderer. File-change diffs stay on the existing diff view. Full-response
  replay remains restricted to the development demo.
  Session progress commentary is ordinary prose in the transcript; the host
  fallback announcement (`data.synthetic=tool_announcement`) stays as a
  grouping anchor and is not rendered as visible prose. Tool rows stay
  underneath that announcement as Beautiful UI Tool Chips (icon, bold
  label, mono detail chip). Completed process folds keep the 已处理 label and
  may show honest tool-call and commentary counts. Executed file changes
  also render compact white `path +N -N` pills; queued and approval-bound
  writes do not. ChatGPT.app does not draw a turn-level `正在处理`
  rule and does not invent an empty Thinking row on send. Azem waits
  until the model emits thinking or tools, then keeps one sparkle row
  (`思考` / `搜索了代码` / `运行命令`) under the user message.
  Only the sparkle label changes; the header stays mounted and
  the clock does not reset. The chevron slot is reserved so expanding later
  does not jump the row. This is not a separate capsule and not the
  pixel-grid Loading icon. Empty thinking or
  text frames and the hidden host fallback do not count as live progress.
  While that run is active the composer placeholder says the model is thinking
  instead of looking idle. A thinking-only trail stays that header plus
  reasoning prose. After the current step completes with tools, it expands
  to one chip list: thinking as the first chip (sparkle + preview capsule),
  then tool chips, then file-change pills. The group header may show
  `N tool calls, N messages`.
  Elapsed time sits after the sparkle label,
  appears only after the first tenth of a second (`0.1s`, `1.2s`, then
  `1m05s`), and never shows `0s`. After the turn settles, thinking-only
  trails keep the clock on the 思考 header and tool trails fold under 已处理.
  `beautiful-ui.css` is imported last so its cool-gray /
  blue tokens and the full-width subagent run card win over the prototype
  warm palette and the old 15px commentary marker grid.
- `frontend/src/components/AttachmentPreview.tsx` owns image thumbnails in the
  composer and user transcript plus the full-size local viewer. Preview bytes
  come from the focused `AttachmentDataURL` Bridge method after the application
  validates session ownership and the detected MIME type.
- `frontend/src/components/SubagentsPage.tsx` owns the grouped current-session
  collaboration roster. `AgentSideChat.tsx` owns the overlay child transcript
  drawer and icon-only accessible member switcher. The drawer transcript
  subscribes only to `agentBlocks`; `reduceEvents` must not replace the main
  `blocks` array on a subagent delta. Folded `已处理` trails stay unmounted
  until the user expands them, while live trails remain expanded (UI-007).
  Expanding a large completed fold first paints collapsed chip headers and
  does not mount every tool body at once (UI-012).
  Background completion wake blocks stay `kind=user` for model context, but
  `state=subagent_wake` renders as a left-aligned system notice instead of a
  user bubble (UI-013). Legacy wake text without that state keeps the bubble.

Highlighting assistant, commentary, or user prose in the main transcript
opens a Select Action island (`frontend/src/components/SelectActionHost.tsx`).
Explain, Improve, and a custom describe-edit submit a normal user turn through
ThreadSurface `submitTurn` with a Markdown-quoted selection. The island does
not add a second agent API, does not persist a fake user notice, and does not
open on thinking, tool dumps, or subagent-wake blocks. Selection state stays
local to the island and is cleared on dismiss. An active run still follows the
existing queue / steer / stop contracts.

Keep direct Bridge calls narrow. Do not add a generic path reader, command
runner, or mutation endpoint to support a presentation feature.

## Embedded terminal

The desktop window has a user-operated bottom terminal panel. It is not the
agent `coding.shell` tool and is not `internal/tui`.

`internal/desktop/termhost` starts a login shell (`$SHELL`, or `/bin/zsh` on
macOS) with `cwd` equal to that window's project workspace (schema 19). The
renderer never assembles `sh -c`. Bridge methods are desktop-only:

- `ListTerminals()`
- `CreateTerminal(cols, rows)`
- `WriteTerminal(id, data)` — raw keystroke/paste bytes
- `ResizeTerminal(id, cols, rows)`
- `CloseTerminal(id)`

PTY output uses the dedicated `azem:terminal` channel
(`terminal_session`, `terminal_output`, `terminal_exit`) so it cannot occupy
the runtime event broker. Output chunks are coalesced (16 ms / 32 KiB) and
base64-encoded. Events never include environment maps, PTY master paths, or
credentials.

Every host wait is bounded (TERM-001). `Close`/`CloseAll` kill, then reap the
child within a fixed budget; `CloseAll` shuts sessions down in parallel under
one overall budget because `Bridge.Close` runs before runtime shutdown. A
child stuck in an uninterruptible state is logged, dropped from the roster,
and left to the OS instead of hanging the Bridge or the window exit path.
Writes never block a Bridge goroutine indefinitely: when the shell stops
reading and the kernel buffer fills, the write fails with an explicit
timeout, and later writes fail fast while the stuck bytes drain. Session
creation forks outside the host lock and re-checks the closed flag, so a
`CloseAll` race cannot leak a fresh PTY.

The renderer side is bounded too: `terminal_output` events only advance the
per-session sequence (tab metadata never rerenders per chunk), decoded bytes
are batched into xterm once per animation frame with a 512 KiB per-session
backlog cap, keystrokes flow through one serialized Bridge write per session
with a timeout surfaced in the panel error area, unchanged resize dimensions
are skipped on both sides, and closing a tab removes the UI immediately while
the backend close converges in the background.

`frontend/src/components/TerminalPanel.tsx` renders one xterm.js instance per
tab. `Cmd+`` / `Ctrl+`` toggles the panel; the thread header **终端** button
and Command Palette do the same. Closing the panel leaves sessions running.
Closing a tab or the Azem window kills and reaps those PTYs. Workspace
overview **打开终端** still launches the operating-system Terminal app.
Prompt themes that use Nerd Font / Powerline icons render correctly when a
font such as MesloLGS NF is installed locally; Azem does not bundle that font.

Windows ConPTY is out of scope. Split panes are out of scope.

## Verification

Run the focused checks first:

```bash
GOWORK=off go test ./internal/desktop ./internal/desktop/termhost ./cmd/azem-gui
cd frontend && bun run typecheck && bun run test -- WorkspaceOverviewPage.test.tsx WorkspaceFilesPage.test.tsx WorkspaceChangesPage.test.tsx Inspector.test.tsx Timeline.test.tsx TerminalPanel.test.tsx terminal.test.ts
```

Then run the complete desktop gate and package the app:

```bash
make test-gui
make gui
```

Packaged windows append the build timestamp to the `wails://` document URL.
This invalidates WKWebView's document cache between builds while Vite's hashed
asset names continue to provide immutable JavaScript and CSS resources.

Launch `dist/Azem.app/Contents/MacOS/Azem`, open Workspace, expand nested
directories, preview a text file and an image, verify binary and oversized
states, switch tabs, and confirm the file viewer and sidebar scroll
independently. Then open Environment, click Changes, filter and expand a changed
file, verify added/deleted lines, and confirm a large change set starts folded.
Verify every child route returns to Workspace, each project can create a new
conversation, settings retain all eight sections including Usage, and streamed assistant text
reveals character by character without a cursor or layout shift. Open Live
context and verify cache metrics distinguish unsupported data from a zero hit
rate, then expand at least one composition category and confirm its item rows
remain inside the single Inspector scroll surface. Finally expand Subagent
collaboration, open a member, switch members with both pointer and arrow keys,
and confirm the parent transcript width does not change while the drawer is
open.
The complete smoke matrix is in [Testing](testing.md).
