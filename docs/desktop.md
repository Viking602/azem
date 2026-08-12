# Desktop application

Last verified: 2026-08-11

Azem's desktop application is a Wails window over the same Go runtime used by
the TUI. React owns presentation state; it does not duplicate provider,
approval, session, or persistence behavior.

## Startup and Bridge

`cmd/azem-gui/main.go` creates the Wails application, embeds the production
frontend, and registers `internal/desktop.Bridge`. The Bridge exposes a closed
set of typed methods. Runtime mutations continue through validated application
actions; read-only desktop integrations use focused methods with their own
input and output limits.

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
it does not expose a generic shell executor to the WebView.

The Workspace overview is the parent route for repository work. It combines the
current branch, bounded working-tree summary, current Pull Request, repository
status, and recent project sessions. Files and Changes are child routes and
always expose an explicit Back to Workspace control; Escape follows the same
route. Command-N starts a conversation in the current project, while Command-2
and Command-3 open Files and Changes.

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
navigation and a consistent content column. Model catalog, model routing,
Subagents, Governance and approvals, Appearance, and Extensions remain complete
sections rather than separate modal variants. The Subagents section updates the
live subagent capacity, independent shell capacity, and admission wait timeout,
then persists those validated values to the existing configuration file.
Extensions contains the shared Skill loading manager used by the secondary
Extensions page: discovered Skills stay searchable when stopped, and the
accessible switch sends only the typed `set_skill_enabled` action. The runtime
publishes the new catalog only after its node-preserving configuration write
succeeds, so a failed save cannot make the current window disagree with the
next launch. MCP is a separate first-class tab driven by `mcp_state` snapshots,
not by plugin counts. It exposes live server state, imported tool count,
refresh/reconnect controls, persistent enable switches, and a validated add
drawer for stdio and Streamable HTTP services. Every entry exposes a confirmed
delete action that removes the node-preserved configuration and live manager
entry together, then records a restart-safe tombstone so catalogs cannot
recreate it.
The add, enable, and delete actions reuse the active manager instance, while
connections start and stop in the background so Settings never blocks on MCP
lifecycle work.

The Plugins tab calls the capability simply **Plugins**, identifies local,
available-from-Codex, and selected Codex entries, and provides an explicit
per-plugin import control. Only selected entries are copied and later executed
from Azem's own `plugin-packages` data directory. Valid icons are projected as
bounded image data; the frontend never receives a local path or presents a
Codex cache path as an active runtime source. Opening Settings explicitly requests the
current plugin snapshot, so startup event timing cannot leave a populated
runtime looking like an empty catalog.

Live assistant text renders new grapheme clusters as a bounded per-character
fade-and-rise tail. The already settled prefix becomes plain text, so long
streams do not accumulate animation nodes. `prefers-reduced-motion` bypasses the
effect and renders the complete current text directly.

The Live context Inspector keeps provider facts and estimated wire composition
visually separate. Cache hit rate is calculated only from input for which the
provider reported cache semantics; unsupported providers show **Not reported**
instead of a misleading zero. The summary shows the matching cached-input
tokens as **Cache hits** and the cache-reporting input denominator as **Total
cache**, so both numbers reconcile with the displayed hit rate. Context
composition uses the runtime request profile and marks its token estimates
explicitly; category rows expand to the bounded concrete contributions such as
individual messages, tool results, Skill payloads, and MCP definitions.
The same scroll surface also restores the current session's durable recap and
updates it directly from `recap_state` after each successful turn. The card
shows the bounded summary, current goal, open items, covered run boundary, and
revision; an empty session renders an explicit not-yet-generated state instead
of silently omitting the capability.

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
- `frontend/src/components/Timeline.tsx` owns bounded streaming reveal and live
  Markdown rendering. The production renderer keeps the latest eight provider
  deltas as short fade/blur ranges inside the parsed Markdown tree, so headings,
  lists, emphasis, and code render immediately while only newly appended text
  animates. Full-response replay remains restricted to the development demo.
- `frontend/src/components/AttachmentPreview.tsx` owns image thumbnails in the
  composer and user transcript plus the full-size local viewer. Preview bytes
  come from the focused `AttachmentDataURL` Bridge method after the application
  validates session ownership and the detected MIME type.
- `frontend/src/components/SubagentsPage.tsx` owns the grouped current-session
  collaboration roster. `AgentSideChat.tsx` owns the overlay child transcript
  drawer and icon-only accessible member switcher.

Keep direct Bridge calls narrow. Do not add a generic path reader, command
runner, or mutation endpoint to support a presentation feature.

## Verification

Run the focused checks first:

```bash
GOWORK=off go test ./internal/desktop ./cmd/azem-gui
cd frontend && bun run typecheck && bun run test -- WorkspaceOverviewPage.test.tsx WorkspaceFilesPage.test.tsx WorkspaceChangesPage.test.tsx Inspector.test.tsx Timeline.test.tsx
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
conversation, settings retain all six sections, and streamed assistant text
reveals character by character without a cursor or layout shift. Open Live
context and verify cache metrics distinguish unsupported data from a zero hit
rate, then expand at least one composition category and confirm its item rows
remain inside the single Inspector scroll surface. Finally expand Subagent
collaboration, open a member, switch members with both pointer and arrow keys,
and confirm the parent transcript width does not change while the drawer is
open.
The complete smoke matrix is in [Testing](testing.md).
