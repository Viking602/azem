# Desktop Applications

Last verified: 2026-09-06

Azem ships the Rust GPUI desktop in `gpui/`, backed by a workspace-scoped
daemon and the authenticated local protocol in `internal/desktopipc/`.
The TUI shares the same runtime.

## Build

Requirements:

- Go from `go.mod`.
- Bun for the AST/LSP runtime packages.
- Rust from `gpui/rust-toolchain.toml` when building GPUI.
- macOS for the signed application bundles.

Build and verify the native GPUI application:

```bash
make gpui
codesign --verify --deep --strict dist/Azem-GPUI.app
```

The GPUI bundle contains:

```text
dist/Azem-GPUI.app/
  Contents/MacOS/Azem
  Contents/MacOS/azem-daemon
  Contents/MacOS/rg
  Contents/Resources/AppIcon.icns
  Contents/Resources/icons/
  Contents/Resources/logos/
  Contents/Resources/locales/
```

## Process and ownership models

GPUI and the interactive TUI are renderers over the same workspace-daemon
ownership model:

```text
GPUI window / Bubble Tea TUI
  -> reconnecting workspace client
  -> owner-only local endpoint
  -> authenticated framed IPC
  -> workspace azem-daemon
  -> internal/desktop closed operations
  -> internal/app runtime
  -> SQLite, providers, tools, terminals, scans
```

The daemon is selected by the canonical workspace path and launched under a
workspace-scoped start lock, so concurrent TUI and desktop launches converge on
one PID and daemon epoch. It owns provider streams, durable runs, approvals,
revisioned prompt queues, terminals, and SQLite. Closing a GPUI window or TUI disconnects that client. Once the last client disconnects, the
daemon starts a graceful shutdown after a five-second reconnect grace period
if no main run, detached child, scan, shell execution, or supervised background
process is active. Active work keeps it alive; it rechecks every second and
shuts down after that work finishes. A daemon with no initial client also exits
after five seconds. Shutdown closes PTYs, LSP/MCP processes, and persistence;
endpoint metadata remains for workspace restoration, but its token is removed.
GPUI exits when its last window closes and supports Cmd+Q; its daemon launcher
reaps exited children so switching workspaces does not leave zombie processes.
Reopening through any client
restores one typed snapshot and then consumes bounded replay from the snapshot's
wire cursor.

Provider and credential authority follows the same rule. The daemon emits one
secret-free `model_providers` projection containing subscription accounts,
llmux credential source/availability, and every persisted model. TUI and GPUI consume that projection; they never copy tokens or keep
renderer-local credential stores. The TUI provider and model pickers include
every enabled provider/model from it, while login/logout remain limited to the
three subscription transports.

An endpoint from an older wire protocol is never treated as a disposable stale
file while its daemon is reachable. Under the same start lock, the launcher
uses a codec pinned to that older version only to request a graceful stop with
`include_active=false`, waits for the old token to disappear, and then starts
the current daemon. An active legacy daemon is left running and startup reports
the blocker. Protocol 3 includes the daemon epoch, target-only session
selection/creation, ephemeral-selection identity, and preloaded context profile.
The version pin covers nested replay event envelopes as well as physical
frames. An older daemon can send replay before acknowledging the idle-stop
request; validating those nested events against the new protocol would abort
an otherwise valid upgrade. Only an explicit `daemon_stop_refused` response is
reported as an active-work blocker. Transport and protocol failures retain
their own cause instead of claiming a task is running.

The snapshot carries canonical session blocks, durable tool records, active run
and operation state, bounded live blocks, pending controls, recovery state,
terminals, and every non-empty prompt queue. Run hydration uses the Azem run
aggregate and the latest execution binding only. It must not load Venat
execution blobs, attempt graphs, receipts, or continuation hashes on the serial
IPC sequencer. Optional catalogs start only after that snapshot response has
been written. Renderer-local state is limited to draft text, selection, and
presentation state; it is never runtime authority.

Pending snapshot coalescing preserves wire order: remove the superseded
snapshot and append its replacement at the new sequence, never put a newer
sequence back in an earlier queue slot. Intervening incremental text, user
projections, and lifecycle events must still reach the client. Replacement
snapshots obey the same byte ceiling and explicit resync path as new events.
The Stop control sends the exact selected session and run identity over IPC.
Process-local active work keeps the non-blocking coordinator-signal path;
persisted pending or suspended work is cancelled through its durable binding.
Renderers disable the operation unless the connection is current and the run
projection advertises `stop`, show `stopping`, report rejected requests, clear
pending controls on the terminal event, and never treat `cancelled=false` as
success.

An unmodified `Escape` key never stops a run. It may dismiss a focused surface
that explicitly owns Escape; otherwise it is ignored. Queue and Guide are
different consent boundaries. Guide sends text to the exact active run only
while its projection advertises `guide`. Queue persists a revisioned,
session-owned FIFO row in SQLite; the daemon, not a renderer timer, dispatches
it after the global main run becomes available. Queue edits, reorder, retry,
pause, resume, and removal use compare-and-swap revisions. Cancellation or
suspension pauses remaining rows until explicit resume. Host
completion-verification retries remain private continuations of the same run;
they are not new sessions or user-authored turns.

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
`internal/desktopipc/dispatcher.go` maps its named operations onto versioned
requests for TUI and GPUI. `internal/desktopclient` is the
reconnecting Go facade used by the TUI; it does not expose a second runtime or an
arbitrary filesystem/shell endpoint.

`cmd/gen-contracts` renders protocol methods plus `internal/app` action/event
lists into the Rust IPC contract.
`make contracts-check` fails when a client drifts. Runtime-identity mutations
carry a client-generated mutation ID and the exact session/run identity.
Server receipts deduplicate successful retries by client, mutation, method, and
payload digest.

Workspace browsing is read-only and bounded:

- paths are project-relative and symlink-resolved;
- file trees, Git output, file bytes, and time are capped;
- binary files return metadata or a bounded preview;
- search uses durable SQLite FTS for sessions and bounded project operations
  for files.

The embedded terminal is the deliberate write-capable exception. Go owns the
PTY; GPUI uses bounded binary IPC and forwards only explicit human keystrokes. Terminal sessions are
not agent tools.

## Projection and reconnect

Bubble Tea reducers in `internal/tui/` and GPUI reducers consume the same typed daemon stream. The reconnecting client
tracks `connecting`, `connected`, `reconnecting`, `resyncing`, and `offline`;
renderers keep confirmed content visible but disable mutations outside
`connected`.

The first reconnect response atomically restores durable session/project state,
active runs and operations, bounded live blocks, pending controls, recovery,
terminals, prompt queues, and the static preloaded context profile. Optional
provider, model, extension, usage, and security catalogs stream after first
paint. All renderers reduce `model_providers`; TUI does not reconstruct a
subscription-only list. Replay starts strictly after the snapshot wire cursor.
A replay gap, epoch change, or bounded-queue overflow loads a fresh snapshot;
renderer pressure never becomes a provider failure.

Recursive session trees are fetched through `session_tree` only when requested;
startup snapshots retain the complete flat transcript without embedding the
tree. This prevents long parent chains from exceeding native JSON depth limits.
Native reconnect backoff resets only after the snapshot has been installed,
and snapshot failures are logged with their actual decode or transport error.

Text phases remain distinct end to end:

```text
provider -> app event -> durable session block -> workspace daemon
                                         \-> IPC -> TUI / GPUI timeline
             commentary | reasoning | final_answer
```

Final output renders once. Reasoning never impersonates commentary. Live
transcript following uses bounded frame-paced rendering and respects reduced
motion.
Main and resumed-main candidate final answers stay private until the engine's
output guards accept them and the canonical turn is committed. The accepted
answer then appears once; commentary, thinking, and tool progress remain live.
This prevents a visible final answer from being followed by a host-required
verification continuation. Interrupted provider partials remain explicitly
unsuccessful.

Same-workspace selection changes the client-local row and title synchronously.
A previously viewed session restores immediately from the renderer's bounded
view cache; a first visit shows a dedicated loading surface in the same
transcript viewport, including the Environment panel inset, instead of the old
conversation. The one docked composer remains mounted below loading and active
content; its textarea stays editable, but mutations remain disabled until the
selection receipt arrives. Prompt and attachment drafts are client-local per
Session and restore on return. The sequenced `select_session` request returns
only the target session/run/live/control/queue projection plus the static
preloaded main-agent context profile. Full reconnect snapshots carry that
profile as well and remain for startup, workspace rebind, and resync. Stale
responses after a newer click are ignored, and failure restores the cached
prior view. Session-list and exact request-profile follow-up work remain
asynchronous; navigation never decodes provider `ModelHistory`.

Session creation uses the sequenced `create_session` request and selects
its typed response directly. The empty session remains ephemeral until its
first canonical user turn, stays out of the catalog, and is reused when another
New session action arrives before the first send. Cancelling an untouched
composer therefore creates neither a durable row nor duplicate client rows.
Selection, ordinary projection refresh, and reconnect do not establish history
membership: new rows require a canonical user block, while existing catalog
rows and legacy run-backed history remain visible. Reconnect retains the
matching client-local draft when the daemon has no durable projection for it;
drafts never enter the saved-session view cache. This does not change the IPC
wire, provider prompt prefix, or message order.

Sending immediately displays a session-local user bubble and clears the
submitted draft without waiting for the start receipt. This pending bubble is
presentation only: it never creates a catalog row or a runtime run. The input
remains editable while duplicate submission is blocked. A new matching
canonical user block replaces the bubble; after the receipt, the exact run ID
owns reconciliation, including prompts transformed by Skills or commands.
An older identical message or an assistant block cannot acknowledge a send.
Failure restores an untouched draft, or retains the failed local message with
error feedback if newer typing already exists. Session switching preserves
that ownership. These delivery changes leave the provider prefix, request
message order, persistence schema, and IPC DTOs unchanged.

The new-conversation launcher omits the normal thread header and divider. Its
larger composer is raised above viewport center. Focus does not alter its
neutral border, background, or shadow; only the text caret indicates input
focus. Active conversation composers follow the same flat rule.

Tool activity is rendered as one compact fold per model step. It starts
collapsed; opening it restores the complete body to transcript flow with all
reasoning prose first and de-duplicated tool rows after it. Starting later text
or a tool settles prior streaming prose, so one run has only one live activity
status. Tool labels contain only the action and target; a spinner, check, alert,
or hollow mark carries state without repeating 「正在运行」 in every row.
A standalone live thinking row uses the ordinary processing status. Once
settled it becomes plain 「思考」, without a dedicated icon or a one-off
completed-thinking duration.
Multiline command arguments reduce to one executable preview. Command and test
output render as one safely fenced code block rather than arbitrary Markdown.
Late start metadata repairs unnamed progress rows; bare transport states such
as `running` never become user-visible targets or details. Any tool with
bounded details is a keyboard-focusable disclosure: failures show the recorded
reason, successful calls show their result, and edits prefer the structured
compact diff from the shared `fileChange.files` projection, falling back to structured
edit sections. Diff details open once by default, preserve manual toggles, and use
Synara-style per-file cards: compact filename/path headers, change counts,
independent disclosure, old/new line gutters, subtle red/green rows and edge markers.
Source lines use extension-based syntax highlighting. Successful file reads use the
same card instead of Markdown: Hashline `[PATH#TAG]` headers and `N:` prefixes are
stripped, a single line gutter remains, and the header shows `Lstart–end`.
Long diffs and reads initially show 16 lines; Show more expands in transcript flow. Line
numbers come from the recorded first-change location, Hashline numbers, or unified hunks; missing
locations remain blank. Todo result JSON is rendered as task titles and status marks;
the model-facing tool protocol remains unchanged. Clickable tool lines remain visually flat with no full-row hover
capsule. Details stay in transcript flow without a nested scroller. Copy,
feedback, and fork controls appear once, after the run's last completed
`final_answer`, never after intermediate or unphased prose. Reduced motion
suppresses status and disclosure animation without hiding state.

Parallel Subagent launches render as one bounded collaboration card. Its
horizontal margins are included in layout rather than added to `width:100%`.
Live, queued, and failed work retains progress detail; completed work collapses
to one compact summary without a duplicate completion column.
Todo detail preserves the durable phase hierarchy, shows overall and per-phase
completion counts, and updates in place on every `todo_updated` event. Newly
guided items therefore appear under their chosen phase; cancelled items retain
an explicit struck-through terminal state instead of remaining pending.

Settled and live tool lists use one 180ms opacity/translate disclosure; closing
keeps the body mounted but inert until the transition ends. Large expanded
histories window their rows, coalesce scroll work to one animation frame, stop
updating outside a one-viewport overscan region, and release RAF, observer, and
listener resources on unmount. The transcript remains the only vertical scroll
owner.

## Desktop surfaces

The desktop clients own these user-visible flows:

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

Native GPUI provider marks use bundled `assets/logos/<modelsDevId>.svg` files.
The model picker, composer chip, and route rows resolve `modelsDevId` from the
provider catalog, then the same models.dev alias map as Settings (`novita` →
`novita-ai`). A missing local logo still falls back to `icons/bot.svg`.

Project rows expose an app-only 「从 Azem 中移除」 action. It changes only the
durable project catalog: workspace files and owned sessions are untouched.
Normal restart access preserves the hidden state; explicitly opening the path
restores it. Removing the active project reconnects the same window to another
visible project; Azem refuses to remove the only active project.

Settings mutations must complete
`event -> daemon state -> persistence -> GPUI redraw -> restart readback`.
Discovery-only provider probes remain transient until the user explicitly
saves. Native catalog details for llmux providers show the API base URL and an
API key field; Fetch models and Save provider send the typed secret, and
`credentialSource` `none` is not rendered as the account subtitle.

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

Run the complete native GPUI gate and build:

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

An existing project directory without Git metadata opens with an empty branch
list. Missing directories, cancelled requests, and other Git failures still
produce errors; Azem does not initialize repositories merely by opening them.

### GPUI structured runtime projection

The native client consumes the current `SessionProjection`, `runs`, `liveBlocks`,
`controls`, and `promptQueues` fields from reconnect/session-selection snapshots.
It restores Todo and durable tool ordering through the existing transcript reducer.
`run_state`, `session_projection`, and revisioned `prompt_queue_state` events update
that same state. Creating a session uses `create_session`, not the global
`execute(new_session)` action. Fast selection keeps workspace catalogs intact.

Reconnect buffers bounded live events until the snapshot is installed, then applies
only events beyond its wire boundary. A different daemon epoch resets the cursor.
The backend owns queued-message persistence and dispatch. Native enqueue, edit,
remove, reorder, retry, resume and guide use `mutate_prompt_queue` with an expected
revision. Submission immediately shows a pending queue row; another mutation for
that session waits for acknowledgement. The draft is cleared only after a successful
mutation for the same session. Failures retain the draft and refresh server state.
Guide lives on queued rows and requires the exact run's `guide` capability. The
backend consumes the queued item and appends its guidance block in one transaction;
stale runs and persistence failures leave the queue untouched. There is no separate
composer Guide button. The Todo retains its 11/12-width rail. Queued messages use compact 32px rows
below Todo and outside the composer, aligned to its 11/12-width rail with a
divider and Queue heading. Without Todo, the queue retains its own heading. The queue scrolls after 144px.

### Synara runtime transcript reference

The native transcript follows the locally observed Synara runtime flow: the
active tool group opens automatically, earlier groups settle closed, and manual
toggles remain effective until the group changes between live and settled states.
Thinking content is rendered as quiet Markdown, including standalone thinking;
reasoning and tools retain their canonical order rather than moving all thoughts
to the top of a group. The live turn has one elapsed header.

Both live `complete` and restored `completed` are successful text states.
Only an accepted `final_answer` with thinking or tool activity folds the
preceding process into a duration disclosure. The answer stays outside the fold;
streaming candidates and errors are never folded this way. Thinking followed by an accepted final uses the same duration header and divider even when no tools ran.
The turn, tool-group, and tool-result disclosures share a 220 ms CSS ease-out
height/opacity transition with a synchronized quarter-turn chevron. Closing keeps
the content mounted until the transition ends; reversing a toggle continues from
the current position. Reduced motion settles immediately. Native measured heights
replace CSS grid interpolation, and nested disclosures remeasure the enclosing
virtualized row. Subagent details retain their dedicated direct renderer.

During long live tool batches, the preview keeps the latest four tools plus all
active, approval-waiting, and failed rows. Older successful tools remain available
through the more-tools disclosure; opening history shows the complete group.

The native composer shows the active execution plan above its input, using the Synara task-banner layout (an inset 11/12-width rail with only top corners rounded, no bottom border, and a one-pixel overlap with the input): completed count, numbered status rows, running spinner, and muted struck-through terminal items. The banner can collapse to its header and opens the environment plan details. Lists scroll after 224 px; reduced motion keeps status glyphs static. Unfinished items remain visible after interruption; a fully completed/cancelled plan leaves the composer and remains available in the environment panel.

Tool-group headers and expanded bodies share the same centered chat-column container. The disclosure clip must not fill the wider transcript viewport independently; otherwise expanded tool rows shift left relative to prose and the composer.

Native streaming Markdown fades its trailing 16 characters on each content update
for both reasoning and assistant prose; settled text remains fully visible and
reduced motion disables the reveal. Reasoning fences keep monospace text with a
subtle left rule and the same muted color as reasoning prose, without the normal
answer code card background or language header.

Native sidebar selection, search-result navigation, and reply-fork selection use
`select_session` so switching conversations in the same workspace preserves the
project PR dashboard. `resume_session` returns a full reconnect snapshot and must
not be used for these navigation actions.

GPUI saves window size, the display UUID, and the window offset within that display
in `gpui-window.json` on exit. On launch it restores the connected display by UUID,
clamps the window to its current bounds, and falls back to the primary display
when the saved display is unavailable. Older size-only files open centered on the
primary display.

Queued native image submissions translate the upload `mimeType` field to the
durable queue `mime` field; editing a queue item back into the composer translates
it back for a normal turn. Failed and cancelled runs pause their queue before
releasing the active run, so a failed final cannot silently dispatch the next task.
Diff line-number gutters grow with the source line count and never wrap digits.

### Native pending approvals

A pending manual approval is shown above the GPUI composer with the requested action, Allow once, and Reject. The thread status reads Approval required: the governed command has not executed. Decisions use the existing session-scoped resolve_approval action; duplicate clicks are blocked until acknowledgement, and failed submissions retain the approval for retry. Reconnect snapshots retain the requested action, tool, target, and risk. For an older daemon that omitted metadata, GPUI displays the matching tool arguments. Navigating to another session never displays or resolves the first session's approval.

### Synara motion reference (2026-09-08)

Reference was inspected in the installed Synara desktop app (conversation
navigation, process disclosure, model/effort menu, sidebar and split review),
then checked against `/Users/viking/agents_dev/synara/apps/web/src/index.css`,
`components/ui/{sidebar,dialog,popover,tooltip}.tsx`, and
`components/chat/composerPickerStyles.ts`.

| Surface | Reference | Native handling |
| --- | --- | --- |
| Conversation entry | 140 ms CSS ease-out, opacity only | Plays after the selected session changes; ordinary stream updates do not restart it. Returning to a previously visited session plays again. |
| User message insertion | 180 ms ease-out, 3 px rise, 0.992 scale | 180 ms and 3 px rise; no scale-induced text rasterization or layout changes. Restored history does not replay send motion. |
| Tool/process disclosure | 220 ms height/opacity and chevron | Existing disclosure motion, canonical chronology and manual disclosure remain authoritative. |
| Left sidebar and side panel | 300 ms cubic-bezier(0.32,0.72,0,1) | Reversible width motion uses the reference duration and curve, retaining resize and narrow-window constraints. The titlebar sidebar control supports mouse and keyboard; collapsed navigation is removed from the accessibility tree. |
| Model, permission and branch menus | Rounded floating surface, short opacity/scale transitions | 200 ms reversible CSS ease-in-out opacity; model menu retains its route and placement through exit. Closed menu actions are blocked; settled menus are unmounted. |
| Settings/search/rename | Route-level settings; short dialog transitions | Settings fills the workspace. Catalog and usage retain their existing full-width layouts. Model routes, appearance, governance, subagents, security, extensions and archive share a centered 800 px content column with section labels above white/light-theme cards on a muted page, following the Synara/ZCode grouping hierarchy. Each card owns its outer border; the shared row stack draws dividers only between neighboring rows and preserves content height while the page scrolls. Model-route controls use a compact, single-line muted model/reasoning pair with a subtle internal divider; provider names remain in the accessible label and selection menu. Route model menus use compact single-line model/provider rows, and reasoning menus use shorter normal-weight options; both use subtle shadows and rounded corners. Model-route menus use local anchors and fit within the window, including routes near its bottom edge. Settings rows keep controls right-aligned and wrap at narrow widths; extension tabs wrap without clipping. Card contents clip to rounded corners; deferred popup menus remain outside that clip. Search and rename retain their contents through the 200 ms exit. |
| Composer and Todo/queue | Centered 11/12 rail above full composer | Existing rail remains outside the input, retaining backend queue ownership. |
| Streaming thoughts/text | Local live-edge emphasis | Existing bounded tail reveal; no full transcript relayout to animate new tokens. |
| Diff/read details | Per-file rows and bounded code surface | Existing native per-file diffs, fixed gutters and explicit disclosure remain. |

Reduced motion disables conversation and popup entry and preserves immediate
navigation. Completed conversation transitions stop requesting animation frames.
The initial restored window is visible immediately. Opening search over a
conversation does not replay its entry. Native popup surfaces remain opaque;
CSS backdrop blur and nested-dialog scale are not yet native equivalents.
Synara-only product pages and remaining individual control effects still require
parity work; this table is not a claim of complete product reproduction.

Native acceptance: the packaged window was exercised through session A → B → A, model search filtering and Escape dismissal, and left-sidebar close/reopen. Screenshots confirmed stable transcript/composer layout; accessibility inspection confirmed the collapsed navigation subtree was absent. Timing/re-entry and reduced-motion behavior are additionally guarded by deterministic tests.

Popup lifetime is frame-driven and supports reversal from the current opacity. Reduced motion snaps both entry and exit. No popup timer remains after settling. Model, permission and branch actions reject calls after closing, and closing popup containers capture mouse and keyboard input.

The final packaged build was additionally checked for full-workspace Settings, the centered Appearance page, populated virtual provider/model lists, model/permission/branch menu open and Escape close, removal of settled menus from the accessibility tree, and conversation A → B → A with project PR controls retained. No provider, permission or branch selection was changed during acceptance. `make test-gpui` and `make gpui` passed, including strict codesign verification.

Model routing uses stacked full-width workflow and subagent groups. Each row keeps its purpose beside aligned model/provider and reasoning selectors, wrapping controls on narrow windows. Both route picker kinds use the shared reversible popup lifecycle; opening reasoning must not be gated on the model-only picker kind.

Non-catalog Settings content and headers must not flex-shrink: the outer scrolling viewport measures the full intrinsic content height. Each section (including extension sub-tabs) has its own stable scroll element ID. Native acceptance reproduced the previously immobile routes page and verified scrolling through the final worker row after the fix, with readable purpose labels and the reasoning picker visible.

The composer plan rail follows Synara ActiveTaskListCard spacing/status conventions, with an Azem-specific compact default: progress plus the current in-progress task (or first pending task) in one line. Expand reveals the numbered list; the detail action still opens the environment plan. Expansion is session-scoped for the current window. Rows do not flex-shrink inside the bounded scroll region, status markers align with the first text baseline, and quieter 12 px top corners match the compact rail. Both compact and expanded layouts were verified in the packaged native window.

The native composer shows unfinished Todo items only while the current run is live. After completion, failure, or cancellation, the active rail clears without rewriting stored Todo statuses; historical plan details and the existing stopped-duration timeline remain available. Queued messages stay outside the composer in the aligned Queue section, using compact rows and thin separators. Guidance remains gated by the backend-advertised active run; stopping does not consume or automatically resume a paused queue.

Before the first provider thinking delta, native send feedback displays only the processing timer; it does not claim that thinking has started. Queue drops onto the same item are no-ops. Native reorder dispatch requires both items to remain editable in the current session, and the server preserves queue contents and revision for a valid self-drop while retaining revision and missing-item validation.

Settled standalone thinking renders its body without a redundant Thinking label; only active thinking shows that activity heading. The containing turn owns the duration disclosure.

The environment card omits Todo and groups workspace controls, Editor view, and Recap with dividers. Recap does not expose its internal revision. Todo remains in the composer rail, where the expand control opens its rows; the redundant environment-details shortcut is removed. Chinese Todo labels use 待办, distinct from 计划 mode.

The environment Recap is always visible below its section label, without a disclosure arrow or toggle state.

The native window uses one 46 px top row: traffic lights and the sidebar toggle share the conversation title/actions row. Sidebar content starts below that row; collapsing the sidebar reserves space for window controls before the title. There is no separate full-width titlebar strip.

Settings also paints through the native titlebar: only its navigation content reserves the 46 px window-control area, so the sidebar background reaches the window top without a separate white strip.
