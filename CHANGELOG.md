# Changelog

## Unreleased

- Desktop transcript: switching sessions opens at the latest message,
  not the first line. The stage remount now follows the tail, and the
  pin repeats after history turns grow past the 180px placeholder.

- Desktop transcript: sending a follow-up no longer jumps the viewport
  back to the first line of the session. Finished turns keep their
  mounted node, and the tail is pinned after layout.

- Desktop transcript: a live turn draws one `正在处理` divider above
  its first message. The label and elapsed clock sit above the rule,
  not on the same row as the line. Thinking and tool rows do not print
  their own times. Failure, queue, and approval labels stay. After the
  run settles, thinking-only trails keep the clock on the 思考 header
  and tool trails fold under 已处理.

- Desktop transcript: when a turn with tools finishes, the process
  trail folds under 已处理 and only the final answer stays in the
  reading column. The live step list is unchanged while the run is
  in progress. Thinking-only or commentary-only turns still do not
  use 已处理.

- Desktop transcript: new commentary after a settled count row stays
  in that same slot while it streams. Live process folds no longer
  reserve a 180px content-visibility box that parks the first tokens
  too low and then jumps them up.

- Desktop transcript: opening a settled `N 次工具调用` row only paints
  chip headers. The 思考 body mounts when that chip is opened, and
  the expand animation no longer clip-paths the whole tree.

- Subagent drawer: a projection resync no longer closes the open
  child chat. Refreshing the main transcript keeps the selected
  subagent and its live thinking so inspect can merge instead of
  dropping the follow-up.

- Desktop transcript: unphased streaming text is ordinary body prose
  from the first token. It no longer appears as a blue-dot pending
  card before being reclassified.

- Desktop transcript: streaming a final answer no longer flickers
  already-written lines. Only the newest provider range plays the
  enter motion; earlier paragraphs stay settled text.

- Desktop transcript: after tools finish, new model commentary is
  ordinary prose immediately. The previous step settles to
  `N 次工具调用` and no longer lingers as ✦「运行了 N 个工具」.

- Desktop transcript: the sparkle bar hugs `思考 · 4s` instead of
  reserving a 12em hollow gap. The clock stays in its own column
  beside the chevron, so a label roll still does not shove the seconds.

- Desktop transcript: wait, thinking, search, and tool labels on the
  sparkle bar roll vertically when the action changes, instead of
  snapping. Clock ticks do not restart that motion.

- Desktop transcript: a completed tool step is a plain count row
  (`N 次工具调用`), not the sparkle 「运行了 N 个工具」 bar and not a
  gray capsule. The current live step stays on the sparkle 思考 bar
  until a later step starts or the run ends, so it does not collapse
  and pop back when thinking resumes.

- Desktop transcript: the last answer is no longer hidden under the
  composer. A measured spacer sits below the timeline (WebKit
  content-visibility was dropping padding-bottom from scroll height),
  and the overlay fallback is tall enough for the resting input card.

- Desktop transcript: fenced code in answers uses the Beautiful UI
  Code Block card — white elevated paper, filename + language, Copy,
  and a line-number gutter. Keywords stay blue and strings stay green.

- Desktop transcript: a completed tool step is one gray Tool Chip card.
  The first row is the 思考 chip (preview capsule), then write/shell/read
  chips, then file-change pills. Live wait/thinking stays the sparkle bar
  plus prose and is not turned into that chip.

- Desktop frontend: the production bundle no longer ships one 666 kB
  entry chunk. xterm loads only after the terminal is first opened,
  Inspector and the subagent drawer stay on their own async chunks, and
  motion/xterm vendor groups are split out of the main entry.

- Desktop chrome: Workspace Files/Changes, Extensions, PR, Recovery,
  and the subagent drawer no longer use the 156–158px poster headers.
  Page titles sit on a compact bar. Change review no longer invents an
  architecture-violation count.

- Desktop transcript: first-token wait, live reasoning, and live
  search/tools now share one left-aligned sparkle row. Only the label
  changes; the header does not remount or reset its clock. The old wait
  capsule is gone. A thinking-only trail is still 「思考」 plus the
  reasoning prose; while the run is live the elapsed clock sits after
  正在处理, not on the thinking or tool rows. Each completed tool row keeps its own duration
  instead of repeating the step's total. After the current step completes with tools, it expands
  to one chip list: thinking as the first chip, then tool chips and
  file-change pills, with an honest `N tool calls, N messages` header.
  There is no Steps / Reasoning / Search / Coding switcher.

- Embedded terminal: every PTY host wait is now bounded. Closing a tab or the
  window kills and reaps sessions in parallel under a fixed budget, a child
  stuck in an uninterruptible state no longer hangs the Bridge or the exit
  path, and writes to a shell that stopped reading fail with an explicit
  timeout instead of blocking. The panel batches output into xterm once per
  frame with a bounded backlog, serializes keystroke writes with a timeout,
  skips unchanged resizes, and removes a closed tab immediately while the
  backend close converges in the background (TERM-001).

- Subagents: cancelling no longer holds the runtime lock across a store
  write, a child that only spins on lease/workspace retries can now be
  cancelled by the idle watchdog (a new wait summary still resets the clock
  once), and repeated same-state live roster events coalesce in the event
  broker while lifecycle transitions stay ordered (SUBAGENT-007).

- Desktop transcript: after a tool batch finishes or fails, a still-live main
  run now keeps the sparkle 思考 wait pill in the transcript until new
  commentary, thinking, or a final answer arrives. Empty thinking/text frames
  and the hidden host fallback do not count as progress. The composer no
  longer looks idle with 「输入下一轮消息」 while that run is active.

- Semantic compaction: writer output that puts `Fact.sources` as a string
  or string array, which previously failed with `cannot unmarshal string
  into ... EvidenceRefV1`, is now normalized into `EvidenceRefV1` objects
  on every fact. Number, bool, and unmappable object sources still fail
  closed. The static compaction writer prompt now says `sources` must be
  an object array; that prefix-cache reset applies only to the compaction
  writer, not the main conversation (PROVIDER-002).

- Desktop transcript: adjacent thinking spans (and hidden host fallbacks)
  share one ✦ 思考了 header; only the model's own commentary prose is shown.

- Agent workflow: investigation and modification must create or refresh a
  durable Todo list before search, read, shell, edit, or delegation. Before
  each tool batch the model must itself write one or two ordinary-prose
  commentary sentences (「我准备…」 / 「接下来…」); the host does not invent
  that text. Trivial chat with no workspace work, such as 「你是谁」, may skip
  both Todo and tool commentary. Changing `internal/app/prompts/main.md`
  rewrites the static instruction prefix, so provider prefix-cache hits reset
  once and then stay stable for later turns.

- Desktop transcript: expanding a large completed 已处理 fold first paints
  collapsed tool/progress chips and only mounts that row’s diff, Markdown,
  Thinking panel, or Subagent list when the row is opened.

- Subagents: a child that stays `运行中` with no thinking, text, or tool
  activity is now cancelled after `agents.subagents.idle_timeout` (default
  `5m`; `0s` still disables). Open tools and approval waits are not
  cancelled. Empty thinking/text frames and UI elapsed ticks do not reset
  the clock. The running collaboration card and side-chat drawer show live
  thinking or the first-token wait pill instead of a bare 运行中 body;
  `inspect_agent` no longer wipes newer deltas, and a projection resync
  reloads the open child transcript.

- Desktop transcript: the host fallback line used when a model starts tools
  without commentary (`正在调用所需工具，并根据实际结果继续。`) stays as an
  internal grouping anchor and is no longer shown as chat prose. Real model
  commentary is unchanged.

- Desktop: a user-operated bottom terminal panel (xterm.js) talks to a Go
  PTY in the current project workspace. `Cmd+`` / `Ctrl+`` or the 终端
  control toggles it. Tabs can be created and killed; hiding the panel
  leaves sessions running until the tab or window closes. This is not the
  agent shell tool and does not bypass tool approval. The PTY prefers a
  locally installed Nerd Font so Powerlevel10k/starship glyphs are not
  tofu, and the light-theme scrollbar uses paper/ink tokens instead of a
  black bar.

- Desktop Appearance: conversation text is now two independent controls
  under **聊天文本** — **UI 文本** (12–20 px, default 13) and **代码字体大小**
  (11–18 px, default 12). They persist in WebView local storage
  (`azem:chat-font-size`, `azem:chat-code-font-size`) and apply only on the
  thread surface and subagent side-chat transcript. Sidebar, Settings chrome,
  and Inspector chrome keep the existing global interface font size.

- Desktop transcript: first-token wait uses the Thinking sparkle pill
  (`思考` / `思考 0.3s`), not the pixel-grid Loading icon. The wait pill
  keeps the same `--ink` charcoal as the later 「思考了 Xs」 header from
  first paint; a disabled summary no longer inherits the global
  `button:disabled` 45% fade. Live reasoning and process traces keep
  expandable Thinking. Reasoning, search, and other tools stack as
  separate sparkle rows instead of a tab switcher.
  Completing a stream keeps the same mounted Markdown tree and only stops
  reveal/caret motion; the idle caret uses `content: none` so it cannot
  remain as a hairline between settled paragraphs. Elapsed time still
  starts at `0.1s` and never shows `0s`.

- Desktop transcript: tool calls use Beautiful UI Tool Chips (icon, bold
  label, mono path/command/query chip). Process folds keep 已处理 / 处理中
  and may append honest tool/progress counts. Executed file edits add white
  `+N`/`-N` pills. Reasoning is unchanged. Queued and approval-bound writes
  stay out of file-change pills until they run.

- Desktop transcript: fenced Markdown code now uses the Beautiful UI Code
  Block card (filename or language, copy, line numbers) while the agent is
  still streaming and after the answer settles. File-change diffs are
  unchanged.

- Desktop Inspector: the task plan uses Beautiful UI Task Row capsules. Each
  Todo phase is a white rounded card with a status mark (green check or
  numbered progress ring), title, honest `done/total` count, status badge, and
  an expand rail for the phase items. Commentary and thinking chrome are
  unchanged.

- Desktop transcript: highlighting assistant, commentary, or user prose opens
  a Beautiful UI Select Action island. 解释, 改进, and a custom 描述编辑 send
  a normal user turn through the existing composer path with the quoted
  passage. Thinking, tool dumps, and wake notices are ignored. An active run
  still follows queue / steer rules.

- Desktop stop now cancels the active run and its subagents together. The
  previous stop left children running in the background after the parent
  ended. TUI still offers a parent-only choice when children are active.

- Desktop session: Beautiful UI cool-gray / blue tokens stay global so the
  chrome does not fall back to the prototype warm palette. Progress
  commentary is ordinary prose instead of a titled duration card, and it is
  not laid out on the old 15px marker grid. Subagent run cards keep their
  full-width frame. The first-token wait uses the Thinking sparkle pill;
  live reasoning uses the expandable Thinking row. Elapsed time stays in that
  label, appears only after the first tenth of a second, and never shows
  `0s`. Changing
  `internal/app/prompts/main.md` rewrites the static instruction prefix, so
  provider prefix-cache hits reset once and then stay stable for later turns.

- Subagents: Settings and `agents.subagents.idle_timeout` can cancel a
  running child that produces no thinking, output, or tool activity for a
  chosen window. The default is now `5m` (see Unreleased). Open tools,
  including approval waits, are not cancelled; elapsed-time UI ticks do not
  count as activity.

- Desktop Inspector: the context kernel occupancy and cache hit rate now
  count only the main agent. Subagent usage is stored separately and no
  longer replaces the main profile or cache totals.

- Subagents: review or verification that gates later work must stay
  foreground, or the parent must consume it with `subagent.get_output` before
  ending the turn. If the parent still tries to finish while its background
  children are running, the host injects one retry and does not cancel those
  children. Changing `internal/app/prompts/main.md` rewrites the static
  instruction prefix, so provider prefix-cache hits reset once and then stay
  stable for later turns.

- Subagents: background completion auto-wake now batches every undelivered
  terminal child in the session into one turn. The wake message stays in the
  model context as a user block, but the desktop renders it as a system
  notice instead of a user bubble.

- Semantic compaction: the host now accepts a SemanticStateV1 object that is
  wrapped in one whole-response JSON fence, matching automatic-review
  validation. Prose around a fence, nested fences, and other fence languages
  still fail closed.

- Skills: a later turn in the same session replays completed
  `hydaelyn_activate_skill` records so previously loaded skills stay active.
  Disabled or deleted skills are not replayed. Reading a resource that was
  never activated still fails closed.

- Subagent drawer: streaming no longer recopies the main transcript, folded
  process trails stay unmounted until expanded, and the side chat transcript
  no longer re-renders with every header preview update.

- Desktop settings: **用量 / Usage** is a read-only token ledger under
  Preferences. It aggregates completed `provider_requests` for the current
  project or all projects (366-day window, bounded model/skill lists). Cache
  read/write follow reported inclusive semantics; unknown providers stay
  unreported. Token activity is a 7×week heatmap with daily / weekly /
  cumulative views over the same day series; empty days stay empty. The page
  is loaded on demand through `Bridge.UsageReport`, not primed or polled.

- Desktop settings: the English Preferences nav and page title for 治理与审批
  is now **Approvals** instead of Governance & approvals. Chinese is unchanged.
  Searching the previous English names still opens the same section.

- Extensions Hooks: opening Settings or starting the desktop reads the current
  hook catalog directly, so a populated list no longer appears as 0/0 until
  refresh. Trusting plugin hooks persists `plugins.trust_hooks`, reloads the
  runtime, and keeps the switch on. Each command can be enabled or disabled
  through `hooks.disabled` / `set_hook_enabled`; untrusted plugin hooks still
  do not run.

- Desktop composer: the bottom input dock is a transparent overlay instead of
  an opaque full-width paper bar, so the timeline stays visible and scrollable
  beside the solid input card. There is no fade or mask above the card; the
  card itself stays opaque. The last live tool card keeps 16px of space above
  the input; the empty welcome composer does not use that overlay gap.

- Subagent foreground wait: default `agents.subagents.await_timeout` is now
  `0s`, so the parent keeps waiting while a child is still running in the
  foreground. Settings adds **直到完成 / Until done**. A positive window still
  only releases the parent call; safe work continues in the background and is
  not cancelled.

- Desktop MCP: adding a service opens a centered settings modal instead of a
  right-hand drawer. Fields, validation, and `upsert_mcp_server` are unchanged;
  the overlay is hosted on the settings `<dialog>`.

- Extensions Hooks: the trust confirmation uses the same single-line dialog
  as MCP/plugin delete, stays clickable inside the settings `<dialog>`, and
  shows a save error instead of closing with no action. Plugin hooks remain
  off until the user confirms **信任并启用**.

- Extensions plugins: clicking 导入 now copies a visible Codex package even
  when a later `codex plugin list` fails, keeps the action error on the
  settings page, and reconstructs `name@marketplace` when the wire omits `id`.

- Grok subscription settings: show the account email or handle instead of
  `anonymous-<hash>`, fetch live quota with the CLI-proxy `/v1/user` id as
  `x-userid`, and surface the real quota error instead of only 获取失败.
  `/v1/user` and `/v1/billing` retry once after a connection EOF or reset so a
  dropped HTTP/2 stream through the desktop proxy does not fail the quota line;
  a second failure still shows the real transport error. Grok billing now
  accepts the live CLI-proxy credits shape used by CodexBar: omitted
  `creditUsagePercent` falls back to on-demand used/cap or a zero-usage
  current period, and the settings card labels weekly, monthly, or credits
  from the reported period.

- Desktop Extensions: add a Hooks tab that lists plugin, user, and project
  hook sources plus loaded commands. Plugin hooks stay off until the user
  explicitly trusts them; the decision persists as `plugins.trust_hooks` and
  reloads the runtime immediately.

- Desktop Inspector sources: keep user-typed URLs and web-search URLs
  alongside images, give generic attachments numbered names instead of
  repeating `image`, and open a source in a preview or the system browser.

- Desktop archive: archive long-idle unpinned conversations, hide them from
  the sidebar, and inspect or restore them in Settings grouped by project.
  Opening an archived conversation restores it.

- Desktop sidebar: session ages use minutes and update on the next label
  boundary with a single timeout. Hidden windows do not tick.

- Grok subscription catalog: fetch models from the Grok CLI proxy instead of
  api.x.ai, keep optional language-model metadata best-effort, and add a
  Fetch models button on the subscription catalog page.

- Extensions catalog: show a plugin, skill, or MCP icon when the package
  provides one (`composerIcon`/`logo`, skill `icon.svg`/`icon.png`, or the
  parent plugin mark on plugin-owned MCP servers).

- Extensions plugins: importing or removing a Codex plugin copies or unloads
  it immediately. Skills and MCP from that package no longer wait for restart.

- Desktop image attachments: conversation thumbnails follow the real image
  aspect ratio up to 420×320, without a fixed 218×150 letterbox.

- Extensions plugins: imported Codex packages now use 删除 instead of 停止导入,
  with a confirm step. Import stays 导入. Local packages are labeled 本机安装.

- Desktop transcript: an in-flight process stays expanded. It cannot collapse
  to a 处理中 summary. Only after the trail finishes can it fold under 已处理.

- Approvals: automatic review starts as soon as tools are announced, including
  in-workspace file edits, so a batch of writes is reviewed in parallel
  instead of sitting in 排队中. Reviewing rows show a shield and 审核中.
  Pending review never uses the capacity-queue label. Review and approval
  rows only show the file name; Hashline bodies and write payloads stay
  hidden until the edit actually runs.

- Desktop sidebar: session titles, timestamps, and the project heading now
  follow the Appearance font size. The desktop layout pass no longer pins
  those labels to 11px / 9px.

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

- Semantic context rebuild: if automatic compaction tries to commit
  revision 0 after revision 1 is already durable, reload the durable
  checkpoint and retry instead of failing the run with
  `semantic state source is stale`. Cancel leftover background prepares
  when the live history is no longer a prefix of the prepared source.

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
