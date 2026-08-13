# Changelog

## Unreleased

- OMP-style subagent scheduling: raise the default concurrency to 32, accept
  zero as unbounded, and support recursive delegation with a default depth of
  two (`-1` is unlimited, `0` disables delegation). A re-entrant child slot
  prevents parent/child deadlock when the configured concurrency is one while
  still queueing sibling work. The 200-request soft budget now emits one
  private wrap-up reminder only; it never cancels or fails business work.

- Long-running subagents: remove implicit cancellation when a foreground wait
  reaches `agents.subagents.await_timeout` or when the parent tool wait ends.
  Read-only and isolated worktree tasks can continue in the background and be
  polled with `subagent.get_output`; shared-workspace writers keep waiting
  safely. Default token, tool-call, turn, and wall-clock budgets remain truly
  unbounded, while explicit child cancellation and application shutdown still
  terminate work intentionally.

- Desktop global search: make Command-K search actions, settings and configured
  catalogs, session titles, and durable user/assistant conversation content
  across projects. Settings results focus the exact control; conversation
  results show a bounded FTS snippet and jump to the stable matching message,
  including across project windows. Debounce database reads, discard stale
  responses, cap results, and keep complete transcripts out of frontend state.

- Extensions: scan universal `~/.agents/skills` by default. Codex plugins are
  cataloged as optional imports and copied only after the user selects an
  individual plugin; local and imported plugin icons are validated and shown
  without exposing local filesystem paths.

- MCP services: make every stdio and Streamable HTTP service deletable,
  including built-in and plugin-provided entries. Deletion writes a persistent
  tombstone, removes future tool snapshots, and closes the live connection so
  bootstrap cannot recreate it. The Codex-only `computer-use` launcher is no
  longer registered, and stale direct Codex temporary-path references are
  removed during desktop startup.

- Desktop process rows: remove the white marker halo while hovering thinking,
  tool-step, and tool-summary rows. The compact dot/check marker now remains
  visually integrated with the row highlight instead of expanding into a
  detached white circle.

- Desktop recap: restore and live-update the durable session recap in the
  right-side Inspector, including its summary, goal, open items, boundary, and
  revision. Add an independent `agents.recap` model route to Role models with
  atomic YAML persistence and live runtime updates; recap generation no longer
  reuses the semantic compaction model.

- DeepSeek cache accounting: normalize Anthropic-compatible `input_tokens` plus
  `cache_read_input_tokens` into the inclusive input total used by Azem, and
  mark DeepSeek's cache-read counter as reported even when the hit is zero.
  Real cache hits now produce the correct desktop percentage instead of
  appearing as `Not reported`. Anthropic request conversion now hoists only
  leading system messages; later trusted host context remains in the message
  tail so it no longer rewrites the long-conversation prefix on every turn.
  The Inspector keeps two decimal places without rounding the hit rate to an
  integer, so a 99.52% result is no longer displayed as 100%.

- Image attachments: remove Azem's product-wide limit of six images per turn
  and 8 MiB per image. Trusted session ownership, symlink containment,
  regular-file checks, and detected image-format validation remain enforced;
  request limits now come from the selected provider instead of a shared local
  ceiling.

- Desktop progress: require one explicit title/detail update before every
  individual tool call or parallel tool batch. If a provider skips it, the
  runtime inserts one durable fallback before exposing the tool event. The
  desktop now keeps adjacent reasoning, tool calls, and diffs inside that same
  progress step; active steps open automatically instead of rendering separate
  zero-second thinking rows.

- Semantic context rebuild: raise the default durable semantic-state budget
  from 8,192 to 32,768 tokens and remove the fixed 8,192-token ceiling.
  Configured budgets may use up to one quarter of the writer context window;
  generation headroom remains separate from the durable state size. Reject every
  `length`/`max_turns` result even when it contains truncated JSON, then retry
  once at low reasoning. Successful activations now
  advance the shared in-memory semantic revision so repeated automatic
  compactions do not submit revision 0 after revision 1 is already durable.
  Oversized candidates use the configured durable-state budget during bounded
  repair instead of repeatedly spending the larger generation allowance.
  The same bounded reasoning fallback covers title and recap generation
  without hiding ordinary provider failures.

- Grok subscriptions: accept both object- and tier-array pricing metadata from
  the live xAI model catalog so an authenticated account can finish catalog
  loading instead of failing during login restoration.

- Runtime scheduling: a transient workspace resource-claim conflict now keeps
  the main task queued and retries it after the current writer releases or its
  lease expires. The conflict no longer becomes a raw terminal Provider error.

- Desktop progress: ignore one decorative emoji prefix before the explicit
  two-line progress contract. Skill-authored updates now keep the same compact
  title/detail typography as ordinary model progress while streaming.

- Desktop Subagents: render the detail drawer with the same reading width,
  message typography, and hierarchy as the main conversation. User instructions
  and final answers stay in the transcript while only process work uses the
  toggleable `处理中` / `已处理` disclosure; completed work starts folded.
- Desktop file edits: render active `coding.edit_hashline` and
  `coding.write_file` calls with the same file-change row used after
  completion, including exact planned line totals when they can be derived
  safely. The row now transitions in place from “Editing files” to the real
  completed diff instead of exposing raw edit arguments during execution.

- MCP runtime: treat JSON-RPC `-32005` transport rejections as recoverable tool
  errors instead of terminating the entire agent run. The model can now report
  or retry the failed tool call while the reusable connection remains ready;
  Azem does not automatically replay potentially side-effecting MCP calls.

- Grok authentication: align device-code creation and polling with the current
  Grok Build compatibility contract by sending the required referrer, client
  version, and interactive-surface metadata. This prevents valid browser
  authorization attempts from being rejected as `HTTP 400: invalid_grant`.

- macOS project windows: keep project and session runtimes isolated so active
  work survives cross-project navigation, but launch secondary windows with an
  accessory activation policy. Opening another project no longer creates an
  extra Azem icon in the Dock or Cmd-Tab application list.

- Desktop MCP management: replace the blank plugin-derived Extensions view
  with a real capability hub backed by live MCP snapshots. Settings now shows
  configured local and remote services even when no plugin is installed, with
  connection state, imported tool count, approval policy, refresh/reconnect,
  and persistent enable switches. A validated add drawer creates stdio or
  Streamable HTTP servers using secret references only; writes are atomic and
  enabled connections start asynchronously without freezing Settings.

- Desktop Skills: add a searchable Skill loading manager to both Extensions
  surfaces. Any discovered Skill can be stopped or restored without editing
  YAML. Stopped Skills remain visible for later restoration but are removed
  from the runtime registry, model context, eager activation, and composer
  slash suggestions; the selection is persisted atomically in
  `skills.disabled`.

- macOS networking: make Finder-launched Azem use the active SystemConfiguration
  HTTP, HTTPS, and SOCKS proxy just like Codex/Electron. The shared resolver
  covers subscription authentication and streaming, llmux providers, model
  discovery, remote MCP, and default HTTP clients; it refreshes native settings,
  respects bypass rules, and keeps scheme-specific environment proxies as
  explicit overrides instead of silently connecting ChatGPT directly.

- Subagents: prevent long reviews from failing after context compaction when a
  valid semantic checkpoint is slightly larger than the former 16 KiB ceiling.
  The writer now has an 8,192-token default budget and two bounded repair
  attempts with headroom. Private semantic checkpoints remain durable but no
  longer appear in the side chat or get re-seeded into resumed subagents.

- Runtime isolation: keep live conversations running when another session or
  project window is opened. Every Azem process now holds a shared SQLite
  recovery fence for its lifetime; only the first exclusive owner performs
  crash recovery, so a new window cannot expire another process's leases or
  turn its active task into `reconcile_required`. Conversation navigation no
  longer emits `SessionEnd`. Skill-resource reads complete before foreground
  Subagents are spawned in their own parallel batch, preventing the old Venat
  safeguard from serializing each ten-minute foreground wait.

- Desktop progress timeline: make the model's own commentary the primary work
  step. A strict short-title/detail contract now renders the current action,
  target, elapsed time, completed check, and active ring in one rail, while the
  tool calls announced by that update stay available inside the step instead
  of replacing its title. Existing unformatted commentary keeps its prose
  presentation.

- Desktop packaging: version the `wails://` document URL with the build
  timestamp so a newly installed build cannot reopen a cached HTML entry point
  and mix the previous settings UI with the current hashed asset graph.

- Desktop streaming: restore the live text reveal without delaying Markdown.
  The current Markdown tree is rendered immediately while the latest eight
  provider deltas fade in through a short blur; previous text and block geometry
  stay mounted, and full-text replay remains development-only. Unphased
  Anthropic/llmux text still stays visually distinct until its tool or terminal
  boundary resolves the final text phase.

- Desktop conversations: restore the approved session-switch motion on the
  content stage. The title bar and sidebar stay fixed while the selected
  conversation is replaced immediately and the next one rises seven pixels
  through a subtle two-pixel blur; reduced-motion users receive an immediate
  swap. A main run that succeeds or fails after the user switches to another
  conversation now leaves a persisted blue unread dot on its session row;
  opening that conversation clears the notification.

- Desktop live context: preserve provider-reported cache input, cache-hit, and
  cache-write totals in the React projection. The Inspector now separates an
  unsupported cache metric from a real zero-percent hit rate, labels cached
  input as **Cache hits**, displays the cache-reporting input denominator as
  **Total cache**, and presents the current context as an expandable category
  and item breakdown covering core instructions, conversation messages,
  Skills, built-in tools, MCP tools, and current output.

- Desktop navigation: implement the approved Codex/Zed-inspired application
  shell across the real React/Wails routes. The compact title bar preserves
  project and branch context; the sidebar groups conversations and Pull Request
  state by project; the new Workspace overview links to real file, change, PR,
  and session data; and child pages expose an explicit route back to Workspace.
  Settings now open on the complete searchable model catalog and retain model
  routes, Subagents, Governance and approvals, Appearance, and Extensions in a
  consistent full-window layout. Each project can start an explicitly scoped
  conversation, the Workspace header can open the system terminal at the active
  repository, and Subagent settings update and persist the live subagent, shell,
  and admission-timeout limits. The empty conversation page now keeps the
  reference composer hierarchy intact, including the dedicated Fast control
  beside the searchable model and reasoning selector. Fast uses one blue,
  borderless glyph; the duplicate glyph beside the model name is removed.

- Desktop settings: anchor searchable menus to their triggering control inside
  the modal coordinate space. Font, model, route, and timeout menus now stay
  aligned with their field and automatically choose the available side instead
  of drifting toward the lower-right corner of the window.

- Desktop composer: keep the branch picker within the visible window. The menu
  now measures the space above and below its trigger, selects the roomier side,
  and limits its scrollable height whenever the window or visual viewport
  changes.

- Approvals: make configurable Anthropic-compatible reviewers reliable when
  their protocol ignores native response-schema settings. The Guardian prompt
  now includes an explicit JSON-only contract, and the fail-closed parser
  accepts a raw decision or one whole-response JSON fence while continuing to
  reject prose, extra fields, invalid enums, nested fences, and tool calls.

- Providers: add an independent image-assistance model route. Image-capable
  main models keep their native attachment path; known text-only main models
  send validated current-turn images to the configured helper and receive a
  bounded, private, explicitly untrusted textual description instead. The
  route works for single-agent and Team turns, filters known text-only helpers
  in Settings, records helper usage, and fails explicitly when no usable helper
  is configured.

- Desktop workspace browser: keep Git-ignored build outputs out of the source
  tree, use a compact title bar that remains visible below native window chrome,
  and restore a true monospace, syntax-colored text preview.

- Planning: add a durable interactive planning workflow shared by the desktop
  and terminal applications. The planner can ask bounded selectable questions,
  publish versioned proposals, accept follow-up questions and revision requests,
  and starts implementation only after an explicit Execute Plan action. The
  approved proposal is attached to a new ordinary turn as trusted private
  context, with unresolved questions and proposals restored after restart.
  Non-trivial proposals now include dependency-aware execution graphs, and the
  approved turn schedules independent, exclusively owned tasks in parallel via
  suitable subagents while the parent retains integration and final verification.

- Desktop: make the Environment “Changes” summary open a dedicated workspace
  review page. The page groups uncommitted files into a searchable directory
  tree, automatically folds large file sets, fetches unified patches only when
  a file is expanded, folds unchanged hunk ranges, and reports binary and
  truncated diffs explicitly.

- Desktop: add a Codex-style read-only workspace browser with lazy directory
  loading and bundled vscode-icons file glyphs shared with change review,
  bounded file tabs, virtualized large-text
  rendering, raster-image previews, and explicit binary/truncation states.
  Workspace path resolution rejects traversal and symlink escapes before any
  file reaches the WebView.

- Extensions: present the capability as Plugins and load only from Azem's own
  `plugin-packages` data directory. Plugins can be installed directly under
  `local`, while Codex imports are copied through a validated staged replacement
  under `codex`; runtime paths never point at Codex storage and the last valid
  copy survives a later Codex import failure. Settings explicitly reloads the
  plugin snapshot so early startup events cannot leave the catalog empty. The
  existing Skill, MCP, opt-in Hook, and App/OAuth degraded-state boundaries
  remain unchanged.

- Models: use one card layout for subscription and llmux catalogs and add a
  persistent one-click model availability toggle. Disabled models remain in
  Settings for re-enabling, but are hidden from session and role selectors and
  rejected by subscription and llmux runtimes. Catalog usage labels now come
  only from an exact provider/model route binding; model names containing
  `codex` or `spark` no longer invent Subagent or Title assignments.

- Windows: run foreground and background tools through PowerShell (PowerShell
  7 preferred, built-in Windows PowerShell fallback), accept PowerShell's call
  operator, stop background process trees, support system PowerShell hooks,
  store application data under `%AppData%`, and add an amd64/arm64 desktop
  build target and smoke-test matrix.

- Approvals: make “Approve for me” a normal configurable model route. Role
  settings can select any enabled ChatGPT, Grok, or llmux model; unavailable or
  invalid reviewers continue to fail closed without executing the action.

- Runtime: decouple provider execution from UI event backpressure. Replaceable
  text, reasoning, and tool-progress projections now coalesce across
  interleaved streams and fall back to durable session resync at the queue
  high-water mark; approvals and lifecycle events remain lossless, large tool
  results are bounded before agent checkpoints and use bounded, lazily rendered
  UI previews, and live text avoids full Markdown parsing until completion.
  Subagent detail hydration is event-driven instead of retransmitting the full
  transcript every 1.5 seconds, so large child runs no longer stall the WebView
  or make composer model controls drop frames.

- Providers: integrate `github.com/Viking602/llmux` v0.2.4 as the generic
  language-model transport for native and OpenAI-compatible providers while
  retaining the existing ChatGPT and Grok subscription drivers. Main and
  subagent requests now apply each configured model's output-token limit, so
  Anthropic-compatible providers no longer silently fall back to 4,096 tokens;
  a provider `length` stop is reported as truncation instead of success.
  OpenCode Go and Zen use their corrected endpoints, and legacy `opencode`
  configuration resolves to `opencode-zen`. When a text-only model follows a
  vision-capable model in the same session, historical images become an
  explicit text omission marker and current-turn images are rejected locally,
  rather than sending an unsupported `image_url` part upstream.
- Desktop: add searchable Model settings for provider enablement, API base URL,
  protected API-key storage, arbitrary model IDs, context windows, and
  reasoning levels. Configured models are immediately available to the default
  session, title, plan, compaction, and subagent routes.
- Desktop: widen Model settings, align provider credential fields, use the
  available viewport for the provider directory, and show provider logos in
  provider catalogs and every model-selection surface.
- Desktop: progressively load the complete llmux provider catalog while
  scrolling, and preserve llmux's Anthropic Messages preference for compatible
  providers instead of routing them through OpenAI Chat Completions.
- Desktop: enabled llmux providers can fetch their authenticated model list
  directly from the provider API. Matching models.dev records enrich the list
  with names, descriptions, context/output limits, modalities, tool and
  structured-output capabilities, and exact reasoning-effort levels. Provider
  icons use models.dev IDs throughout the catalog and model selectors.
- Providers: adopt llmux v0.2.1's canonical hyphenated provider IDs, read
  legacy underscore IDs for compatibility, and rewrite them canonically on the
  next settings save. The remaining non-identical provider names use the full
  current models.dev logo alias set rather than broken image requests.
- Desktop: Model settings now includes the existing OpenAI/ChatGPT and Grok
  subscription login flows with account status, live remaining weekly quota, reset
  time, available credit balance, and logout controls. Official
  llmux API base URLs are visible but read-only; only profiles that explicitly
  require a self-hosted endpoint remain editable. Missing OpenCode Zen,
  FreeModel, and Xpersona defaults are filled from models.dev.
- Desktop: list Model settings providers immediately without waiting on
  subscription quota HTTP calls; quota is refreshed asynchronously after the
  catalog is shown, and the pane shows loading and error states instead of a
  blank “no providers” screen.
- Providers: map Venat dotted tool names (for example `coding.read_file`) to
  OpenAI/DeepSeek-safe `^[a-zA-Z0-9_-]+$` wire names on every llmux request, and
  restore the canonical names on streamed tool calls so DeepSeek and other
  strict OpenAI-compatible providers no longer reject tool definitions.
- Models: normalize ChatGPT, Grok, and llmux catalogs through one models.dev
  resolver. Provider IDs, slugs, and aliases remain valid protocol identifiers,
  while every model picker displays the models.dev name and role-model search
  accepts aliases without sending them to the provider.
- Desktop: show each signed-in ChatGPT and Grok subscription's live model
  catalog with capability badges on its provider card, and use provider display
  names consistently in model selectors instead of exposing lowercase protocol IDs.
- Providers: use llmux v0.2.2's provider-native model discovery API instead of
  maintaining duplicate model-list transports in Azem; models.dev remains the
  metadata source and fallback catalog.
- Desktop: add persistent global interface font and 11–20 px font-size controls
  under Appearance, backed by the font families installed on the host Mac,
  localized labels, search, immediate preview, and a one-click default reset.
- Desktop: compact the advanced model-control footer and keep its active state
  on the “Advanced” label instead of highlighting the full row.
- Desktop: compact Model routes into a constrained, single-line route table
  with shared column labels and responsive stacked controls on narrow windows.
- Desktop: explicitly reload durable session history after frontend
  initialisation so a missed one-shot startup event cannot leave the sidebar empty.

- Context: replace the rolling summary implementation with one semantic
  compaction kernel shared by automatic, manual `/compact`, explicit
  `/rebuild`, Team, subagent, and resume paths. It preserves the latest three
  user turns verbatim, validates stable provenance, keeps tool groups atomic,
  and emits deterministic context manifests.
- Context: Artifact V2 stores bounded head/tail/error/warning previews and the
  read tool now supports bounded preview, byte range, line range, tail, grep,
  and size-limited full modes.
- Persistence: schema 20 adds semantic state, append-only semantic events, and
  context manifests. The upgrade invalidates legacy replaceable ModelHistory
  and cache identity while retaining canonical conversation and durable state.
- Desktop: Inspector now shows semantic revision, writer lag, rebuild reason,
  manifest hash, and segment token estimates.
- Runtime: dispatch independent tool calls in parallel so foreground subagents
  and shell commands no longer queue behind unrelated calls; their existing
  runtime concurrency limits still apply.
- Desktop: frame-pace streamed text with a restrained activity cursor, bounded
  catch-up, and reduced-motion support so long output does not monopolize UI
  rendering.
- Packaging: use the supplied borderless icon for the macOS bundle and ad-hoc
  sign and verify local `make gui` builds so Finder can launch the result.
- Desktop: persist multiple opened projects in SQLite, restore the most recent
  valid project on app launch, and group sessions by their owning project.
- Desktop: opening a session from another project now launches it with that
  project's workspace so branch, Pull Request, Skills, hooks, and tool context
  remain aligned.
- Persistence: schema 19 adds `desktop_projects` and `session_workspaces`.
  Existing sessions are adopted automatically only when one valid project can
  be identified; ambiguous legacy ownership is not guessed.
- Configuration: project history is no longer written to `workspace.root`.
