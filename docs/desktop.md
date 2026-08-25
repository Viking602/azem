# Desktop application

Last verified: 2026-08-25

Azem ships the established Wails/React desktop and an optional native GPUI
client. Both use the same Go runtime, typed desktop Bridge, SQLite stores,
approval rules, and recovery behavior as the TUI. React and GPUI own
presentation state only.

## Startup and Bridge

`cmd/azem-gui/main.go` creates the Wails application, embeds the production
frontend, and registers `internal/desktop.Bridge`. The Bridge exposes a closed
set of typed methods. Runtime mutations continue through validated application
actions; read-only desktop integrations use focused methods with their own
input and output limits.

Desktop bootstrap primes sessions, git branches, model routes, agent types,
Skills, plugins, marketplaces, and Hooks. Skills, Hooks, and marketplaces also
expose direct read-only Bridge methods (`SkillCatalog()`, `HookCatalog()`,
`MarketplaceCatalog()`) so Settings can project the current snapshot after
subscribe without waiting for an event emitted before the frontend listener.
`SessionTree()` provides the same direct readback for the Environment panel.
Usage is not primed: Settings → Usage calls `Bridge.UsageReport(scope)` only
when that page opens or the user refreshes. Catalog events remain replaceable:
a later sequence must not drop a snapshot the renderer still needs.

Runtime events follow this path:

```text
internal/app event broker
  -> desktop Bridge subscription
  -> frontend/src/bridge.ts
  -> frontend runtime store
  -> timeline and control surfaces
```

Session restoration and project ownership remain durable SQLite state. A Wails
window owns one workspace-scoped runtime in-process. GPUI connects to one
workspace-scoped daemon that may outlive any individual renderer connection.
Opening a session from a different project selects that project's runtime and
workspace. Every runtime retains the process-lifetime recovery fence. Starting
another workspace must not run crash recovery against a conversation still
executing elsewhere. Merely selecting another conversation also does not emit
`SessionEnd`; session hooks close once when their owning runtime shuts down.

Desktop bootstrap also installs the shared outbound proxy resolver before any
provider, authentication, model-catalog, MCP, or plugin client is constructed.
On macOS it reads SystemConfiguration directly, so launching Azem from Finder
uses the same active system proxy as Codex/Electron even though no terminal
environment variables are inherited. The resolver refreshes native settings
without restarting the app; matching environment variables remain explicit
per-process overrides.

## Native GPUI client and daemon

`make gpui` builds and signs `dist/Azem-GPUI.app` on macOS. The bundle contains
the Rust `Azem` renderer and the Go `azem-daemon`; the renderer discovers that
sibling before falling back to `AZEM_DAEMON_BINARY` or `azem daemon serve`.
`--workspace`, `--session`, `--config`, `--daemon`, and `--state-dir` select
startup state without changing the user's persisted workspace configuration.

Each canonical workspace maps to
`<stateDir>/gpui-daemons/<workspace-hash>/`. The daemon publishes
`endpoint.json` and an independent 256-bit token there with owner-only
permissions. Unix uses a mode-0600 domain socket. Windows uses a named pipe
whose ACL permits only the current owner and LocalSystem. Authentication is an
HMAC-SHA256 challenge bound to the nonce, client ID, workspace ID, and protocol
version; a copied response cannot authenticate another client or workspace.

The version-1 wire protocol uses a four-byte big-endian frame length and a wire
kind. JSON control frames are capped at 16 MiB. Attachments and PTY output use
binary chunks capped at 256 KiB; one attachment may reassemble to at most
256 MiB and must match its declared size, chunk order, and SHA-256 digest.
Secrets never enter event payloads. The dispatcher exposes exactly the
existing Bridge methods and rejects unknown JSON fields.

Daemon events carry one monotonic workspace sequence. The replay ring and each
client queue have byte budgets. Incremental text and thinking remain lossless;
replaceable snapshots coalesce only while pending. Eviction or client
backpressure sends `resync_required` instead of silently dropping lifecycle
state. The GPUI client then requests `ReconnectSnapshot`, which restores the
durable session projection, session tree, catalogs, PR dashboard, and terminal
roster. Per-terminal raw replay is separately bounded to 4 MiB and is parsed by
Alacritty's VTE state machine rather than painted as ANSI text.

Closing a GPUI window sends `client_detach` and drops only that IPC connection.
The daemon, provider stream, tools, subagents, leases, SQLite state, and PTYs
continue. GPUI uses explicit quit mode, so reopening the application recreates
the window; restarting the renderer reconnects to the same daemon. Only
`azem daemon stop` terminates the daemon, and it refuses an active main run
unless `--include-active` is explicit.

The GPUI state model is split by connection, navigation, transcript, runtime
controls, catalogs, workspace, pull requests, security, terminals, and
settings. The transcript uses GPUI `ListState` bottom virtualization. The
native surfaces cover conversations and attachments, approvals/plans/Todo and
agents, files and changes, projects, PRs, security scans, settings/extensions,
usage/context/archive state, and an embedded terminal. GPUI follows the system
light/dark appearance, exposes AccessKit roles and labels, supports keyboard
focus and IME text input, and contains no animation when reduced motion is
preferred.

Native event flow:

```text
internal/app event broker
  -> internal/desktop Bridge
  -> internal/desktopipc EventHub
  -> authenticated framed IPC
  -> azem-ipc reconnect supervisor
  -> granular GPUI state and virtualized surfaces
```

## Application shell and navigation

The desktop shell keeps project and branch context in the compact global title
bar. The sidebar has two stable scopes: **Conversations** for project-owned
session history and **Workspace** for the active repository overview. Every
project row exposes a project-scoped new-conversation action; Pull Request state
stays attached to the owning project instead of becoming a global empty page.
Project headings are single-line rows containing only expand/collapse, the
project name, and the project-scoped new-conversation action. Conversation rows
show the status dot, title, and real running/unread state on one line. The
sidebar does not repeat branch/path context, project counts, monograms, or
relative ages; the title bar and Inspector own that supporting context.
The Workspace header also exposes a focused **Open terminal** action. Its Bridge
method launches the operating-system terminal with the active workspace as the
working directory by passing an argv-style command directly to the platform;
it does not expose a generic shell executor to the WebView. The in-app bottom
panel is a separate human-only PTY and does not replace that host-terminal
action.

Same-workspace session navigation is deterministic: Sidebar rows and global
search call `Bridge.ResumeSession` and apply the returned sequence-0 durable
projection directly in the initiating window. Bootstrap may still list
sessions, models, routes, and branches concurrently, but those event timings
cannot make the first session click a no-op. A different project's session
continues through `OpenProjectSession` so it opens under its owning workspace.

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

`agent_state.agent.evidenceStatus` carries only `provisional`, `verified`, or
`stale`. The backend derives it from the latest durable work disposition and a
passing verification result at the same revision; the event does not create a
second status store. `frontend/src/store/normalize.ts` preserves the value
across sparse live updates. The Subagents page and conversation drawer render
the localized evidence badge when the value is known, and omit it for legacy
or unrelated runs. `internal/tui` projects the same field in its agent detail
line, so GUI and TUI cannot disagree about stale work.

Settings use one Codex-style full-window layout with a searchable left
navigation, a consistent enlarged typography scale, and a bounded content
column. Opening Settings focuses the dialog surface rather than 返回工作台, so
the WebView does not draw a default focus ring on that control. Escape and the
back control still close Settings; Tab still reaches the back control and uses
the product `:focus-visible` ring. Model catalog, model routing, Subagents,
Approvals, Appearance, Extensions, Archive, and Usage remain complete sections
rather than separate modal variants. Route cards use bounded responsive grid
columns and contain their model, reasoning, and Fast controls inside the card.
Usage is a read-only ledger of completed `provider_requests` (and completed
skill activations when those rows exist). The query is bounded to the last 366
local-calendar days and at most 20 models and 20 skills. Its activity cells
scale across the complete report width instead of leaving a fixed-grid gap.
Cache read/write follow the inclusive reported-fact rule: unknown providers
stay unreported instead of becoming zero. Missing metrics render as —; an empty
database does not invent a heatmap.

The Cursor provider header shows the refreshed account email and normalized
subscription tier. Its quota section renders Total, Cursor, and Third Party
remaining lanes with one reset countdown and cycle-pace forecast. Cursor's raw
reasoning, Thinking, and Fast model IDs are grouped into searchable base-model
cards. The version panel can inspect one exact raw ID, while the family switch
enables or disables every grouped variant in one backend configuration update.
Partial family availability remains visible as an enabled count.

Archive lists archived
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

The Marketplace tab reads configured Git/local/direct-JSON catalogs, searches
available entries, and exposes explicit user/project install scope. Add,
remove, update, install, upgrade, enable/disable, and uninstall use validated
`marketplace_*` actions. Destructive remove/uninstall requires an inline
confirmation. The UI never receives credentials or executes a catalog path.
After a mutation it reads `MarketplaceCatalog()` directly so event timing
cannot leave stale inventory.

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

Conversation presentation vendors assistant-ui Elements source components under
`frontend/src/components/assistant-ui/` and shadcn registry output under
`frontend/src/components/elements/`. The assistant-ui runtime is not installed;
Tailwind v4 and shadcn compile copied source while Azem remains the owner of
durable events, store projection, tools, approvals, session navigation, and
submission. Session turns use registry `MessagePairRoot` and its user,
assistant, progress, and error slots. The input uses registry Composer,
ComposerBar, ComposerMenu, ComposerAttachments, ComposerTextarea,
ComposerToolbar, ComposerAttachButton, ComposerContext, and ComposerSend.
Official `data-slot` attributes identify every surface. Assistant prose remains
the primary reading layer; quiet work rows remain in normal transcript flow.

ComposerContext uses the same `contextComposition` groups and
`contextCategoryLabel` localization as Inspector. A provider-only report
therefore renders **模型输入 / Provider input** and **当前输出 / Current
output** with their actual token counts and used-token percentages. Detailed
profiles render their real core, conversation, tool, Skill, MCP, output, and
other groups in the same order as Inspector. It never fills absent categories
with zero or relabels provider input as messages.

The composer model/reasoning picker uses one whole-chip button with no separate
chevron. macOS keeps the standard activation-only first click for an inactive
window; Azem does not install a WebView-wide click-through override that could
activate unrelated mutating controls. Losing window focus closes an open picker.
Within an active window the trigger accepts valid primary presses, unpaired
primary releases, mouse-only sequences, click-only activation, keyboard, and
assistive input while deduplicating compatibility events. The persistent opaque
Portal closes through `hidden`/`display:none` without blur or transform
animation. Outside click, Escape, model selection, run transition, component
teardown, and session transition also dismiss it.

Within that reading column, assistant answers and progress prose use the full
available width. The transcript container owns responsive line length; message
children do not add a second `ch`-based maximum that leaves a dead strip on the
right.

Assistant-ui `Message` components never present host verification verdicts as
model prose. After the one allowed retry, a new uncertain/failed verification
decision makes the run terminally fail with a host-owned reason while leaving
the model-authored stream untouched. For legacy durable sessions, the Timeline
removes only the two exact historical verification suffixes, retains preceding
model text, and omits a notice-only answer together with its final-answer
marker.

A completed process before a final answer may collapse under its elapsed-time
row. Opening that row restores all commentary, reasoning, tools, and diffs to
ordinary transcript flow immediately before the answer. The outer transcript
viewport is the only conversation scroll owner; the expanded fold has no
height clamp, nested scrollbar, or contained overscroll.

The Inspector task plan uses the Elements-style agent-plan hierarchy rather
than phase capsules. Its heading is the fixed localized **Task plan** label;
the potentially long durable goal sits below it as bounded subordinate text.
The header retains the honest `done / total` count, followed by a one-pixel
progress rule and phase/task rows with completed, active, pending, or cancelled
marks. Item state remains `pending` / `in_progress` / `completed` /
`cancelled`; the UI does not invent task metrics.

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

## Session tree and portability

The Environment panel's **Session history** row calls `SessionTree()` only when
expanded. It renders a semantic nested-history list with the active path,
branch inventory, native-button navigation, entry labels, and an explicit fork
target. Navigation is disabled during a live run and applies the
`NavigateSessionTree` durable projection returned directly by the Bridge.
`CreateSessionFork` and `SetSessionEntryLabel` return the updated tree.

`ExportSession` supports HTML, text, or lossless JSON. `ShareSession` seals the
redacted snapshot before blob/gist publication. These methods accept a session
ID owned by the active runtime and do not expose raw database or blob paths.

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

## Security page

The project-level Security route lists durable scans and projects live
`security_*` events without occupying the foreground conversation. It offers
Standard/Deep start controls, blocked-scan resume, textual status plus redundant
severity markers, coverage/file/worker progress, finding list/detail and
triage, SARIF export with the saved path, explicit cancellation, and isolated
patch/verification actions. Errors remain visible on the page. Semantic lists,
buttons and `progress`, a short status-only live region, visible keyboard focus,
independent finding/detail scrolling, reduced-motion behavior, and
forced-colors fallbacks cover keyboard and assistive-technology use.

`frontend/src/components/security/SecurityPage.tsx` owns this surface;
`store/reduceSecurity.ts` owns per-scan projections so a live update cannot
replace the scan a user is inspecting. Escape and the visible back link return
to the Workspace parent route. Patch remains a typed desktop action. External
MCP publication is deliberately absent from the WebView allowlist and requires
the explicit configured TUI command.

**Settings → Security scans** is the Desktop configuration surface. It exposes
the new-scan enable switch, Standard/Deep default, Deep worker/subagent and
stopping limits, the absolute deadline, and audit/reducer/fixer/verifier model
routes. It does not expose or send Token/tool-call hard ceilings. Saving uses
the typed `set_security_config` action and node-preserving atomic YAML writer;
active scans are not mutated. MCP publication arguments remain administrator-only
YAML and are redacted from Desktop events.

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
- `frontend/src/components/security/SecurityPage.tsx` owns scan history,
  progress, finding detail, export, cancellation, and remediation controls.
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
- `frontend/src/components/Timeline.tsx` owns bounded streaming reveal and live
  Markdown rendering. The production renderer keeps the latest eight provider
  deltas as short fade/blur ranges inside the parsed Markdown tree, so headings,
  lists, emphasis, and code render immediately while only newly appended text
  animates. Completion leaves that same mounted tree in place and only stops
  reveal/caret CSS (`content: none` on the idle caret, not an opacity-only
  leftover); it does not swap to a second Markdown renderer. Fenced code uses
  the assistant-ui `CodeBlock` component (filename when the info-string looks
  like a path, otherwise a language label, plus copy and a line-number gutter)
  through the shared `StreamingMarkdown` renderer. File changes use the
  registry-installed `@assistant-ui/elements-code-diff` source through
  `assistant-ui/CodeDiff.tsx`, which maps durable file records and removes
  duplicated file headers. The element owns tinted rows and horizontal code
  overflow; it does not create another vertical scroll pane. Full-response
  replay remains restricted to the development demo.
  Session progress commentary is an assistant-ui `ProgressMessage` in the
  ordinary transcript flow; the host fallback announcement
  (`data.synthetic=tool_announcement`) stays as a grouping anchor and is not
  rendered as visible prose. Tool rows stay underneath as `ToolTimelineItem`
  disclosures with an icon, verb, target, and honest status. Completed process
  folds keep the elapsed-time label and may show real tool-call and commentary
  counts. Executed file changes render compact `path +N -N` statistics; queued
  and approval-bound writes do not.
  ChatGPT.app does not draw a turn-level `正在处理`
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
  reasoning prose. After the current step completes with tools, it expands to
  one `ToolTimeline`: reasoning first, then tool items, then file statistics.
  The group header may show `N tool calls, N messages`.
  Elapsed time sits after the sparkle label,
  appears only after the first tenth of a second (`0.1s`, `1.2s`, then
  `1m05s`), and never shows `0s`. After the turn settles, thinking-only
  trails keep the clock on the 思考 header and tool trails fold under 已处理.
  `frontend/src/components/assistant-ui/elements.css` is imported last so the
  Elements token layer and full-width subagent run card win over the prototype
  warm palette and old commentary marker grid.
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
cd frontend && bun run typecheck && bun run test -- WorkspaceOverviewPage.test.tsx WorkspaceFilesPage.test.tsx WorkspaceChangesPage.test.tsx Inspector.test.tsx Timeline.test.tsx TerminalPanel.test.tsx SecurityPage.test.tsx terminal.test.ts
```

Then run the complete desktop gate and package the app:

```bash
make test-gui
make gui
```

Packaged windows append the build timestamp to the `wails://` document URL.
This invalidates WKWebView's document cache between builds while Vite's hashed
asset names continue to provide immutable JavaScript and CSS resources.

Launch `dist/Azem.app/Contents/MacOS/Azem`. On a cold launch, click a
non-current session in the active project's sidebar before background catalog
refreshes settle and confirm its transcript opens on the first click.

Then open Workspace, expand nested directories, preview a text file and an
image, verify binary and oversized states, switch tabs, and confirm the file
viewer and sidebar scroll
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
