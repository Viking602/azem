# Changelog

## Unreleased
- Added an optional native GPUI desktop client built with `make gpui`. It uses
  virtualized transcript rendering, system light/dark appearance, AccessKit
  semantics, IME input, ANSI-aware terminal output, binary attachment
  streaming, and native surfaces for conversations, run controls, workspace
  review, pull requests, security, extensions/settings, usage, and terminals.

- Reworked the GPUI presentation to mirror the complete React desktop instead
  of showing a generic shell: integrated macOS traffic lights/titlebar,
  conversation/workspace sidebar, single-line PR/project/session rows, empty
  and active composers, models.dev provider logos, foldable
  thinking/tool/diff trails, structured file-edit and PR views, and the
  floating Environment panel with Plan, history, recap, sources, files,
  services, editor, and terminal access. Settings is now a rounded native modal
  with the complete provider/model catalog, quota state, enable controls,
  capability metadata, independently scrollable provider/model inventories,
  and compact model cards. Mouse-down explicitly focuses native text inputs;
  stale IME ranges are clamped safely after a field is cleared or refocused.

- Reduced cold GPUI readiness from the reproduced 5.419 seconds to
  1.196-1.606 seconds by returning durable reconnect state before optional
  provider/Skill/plugin/MCP/PR catalogs and emitting cache-backed provider
  inventory before remote subscription refresh.

- Added one workspace-scoped `azem-daemon` and a versioned authenticated local
  IPC protocol shared with the existing desktop Bridge. Closing or restarting
  GPUI no longer terminates active runs: the daemon retains the Go runtime,
  SQLite state, subagents, tools, and PTYs, then restores a durable snapshot and
  bounded event/terminal replay on reconnect. Snapshot ordering now prevents
  replayed transcript duplication, keeps pending approvals/questions and child
  activity available after replay eviction, preserves the selected session
  across reconnect, and ignores replayed `open_project` commands so they cannot
  recursively spawn renderers. Unix sockets and state files are owner-only;
  Windows named pipes use owner/System ACLs; attachments are chunked and
  SHA-256 verified. No database schema, runtime configuration default,
  executable prompt, static provider prefix, or provider message order changed.

- Completed the frozen OMP v18.0.3 behavioral-parity program at commit
  `160ed439ac0df594347e7d7018b813a7ffdb5e81`. The executable parity manifest
  now records all 71 in-scope coding-agent and operator capabilities as
  `complete` or `stronger`; public-library API compatibility and OMP visual
  identity remain outside that frozen scope.

- Upgraded the shared runtimes to Venat v0.15.4 and llmux v0.3.1. Provider
  streams now preserve commentary/final phases, terminal state, stop reason,
  reported usage/cache flags, sources/files/warnings, modalities, and
  compatibility descriptors. Venat adds validated tool arguments, duplicate
  registration rejection, per-tool concurrency, durable turn control,
  replay-safe Skill authorization, and unified subagent scheduling.

- Added OMP-compatible read/write/Hashline/glob/grep/AST/LSP/DAP/eval/browser/
  computer/web/GitHub/SSH/process/media/memory tools. Eval supports persistent
  Python, JavaScript, Ruby, and Julia kernels when the host runtime exists.
  Custom extensions may broker only permission-failed workspace-local ordinary
  file writes/deletes; non-permission failures, unresolved symlinks, escapes,
  archives, SQLite, and subprocess writes remain outside that seam.

- Added Goal, Advisor, TTSR, prewalk, Plan YOLO, Vibe, checkpoint/rewind,
  repeated-loop and unexpected-stop guards, batch structured subagents, Hub
  peer messaging/process supervision, and parked-agent revival. `ask` is now
  available in ordinary interactive single-agent turns; `submit_plan` remains
  plan-only.

- Added cross-harness context/rule/Skill/MCP/Hook discovery; MCP OAuth,
  resources, templates, prompts, subscriptions, and notifications; staged
  marketplace lifecycle; custom Bun tools, commands, providers, agents,
  themes, and file fallbacks; and managed automatic Skill learning. Desktop
  Extensions now includes a searchable marketplace with user/project scope,
  direct catalog readback, update/upgrade/enable/uninstall controls, and
  confirmation for destructive operations. TUI adds `/extensions` and
  `/marketplace`.

- Added parent-linked session trees, branches, labels, forks, Claude/Codex JSONL
  import, HTML/text/lossless JSON export, AES-256-GCM session sharing, and
  encrypted live collaboration. Desktop Environment exposes accessible session
  history navigation and labels. TUI adds `/tree`, `/branch`, `/fork`,
  `/label`, `/import`, `/export`, `/share`, `/usage`, and `/collab`.

- Added normal text/NDJSON headless operation, JSON-RPC v1/v2, ACP v2, and the
  supported root Go embedding API. The operator registry now owns shell
  completion, setup/update/GC, auth broker/gateway services, usage reports,
  benchmark workflows, and signed GitHub webhook repair triggers.

- SQLite schema 24 adds session graphs/branches/labels; schema 25 adds
  auth-broker tokens, disable/block state, and usage observations; schema 26
  adds idempotent signed-webhook delivery receipts. Runtime migrations and the
  SQLC schema remain synchronized, preserve user data, and reject unknown future
  versions.

- These changes intentionally expand the static tool/instruction prefix and
  therefore start one new provider cache epoch. Stable tool order and message
  order are preserved afterward. Replaying an activated Skill now restores only
  resource-read authorization; it no longer changes the static Skill/tool
  prefix on the following turn.
- Restored the complete Subagents drawer/page/glyph stylesheet after the
  Inspector removal, kept the composer inside the Environment panel's reserved
  width at medium desktop sizes, routed Environment source attachment through
  the registry Composer control, and read live workspace change counts from the
  shared store so an open panel cannot go stale.


- Added native Azem Security scanning. Standard, scoped, committed-diff,
  working-tree, and Deep scans use host-resolved Azem routes and constrained
  read-only security subagents against immutable snapshots. Schema 23 stores
  durable scans, workers, usage, findings, triage, remediation, matching,
  artifacts, and at-most-once publication receipts. The host binds read
  receipts and finding locations to scope, preserves deleted-file diff evidence,
  and seals canonical manifest/findings/coverage/report/SARIF output. Desktop
  and TUI expose scan progress, blocked-scan resume, finding detail, export, and
  isolated independently verified patches; configured MCP publication remains
  an explicit governed TUI action. Desktop Settings now includes a Security
  scans section for execution limits, the run deadline, and model routes while
  keeping MCP publication arguments administrator-only and redacted. Native
  scans impose no Token/tool-call hard ceiling. No Codex CLI, Node, or Python
  runtime is added.

- Open Todo items (`pending` / `in_progress`) block a model-initiated finish
  even when the run did not mutate files. The host keeps retrying until those
  items are completed or cancelled. User stop still cancels. This injects a
  per-retry host user message and does not change the static instruction prefix.

- Desktop stop now signals the durable coordinator before returning, so a
  provider cannot finish in the gap and turn an explicit user cancellation
  into a failed run. Durable tool/provider cleanup still converges in the
  background, keeping the stop control responsive. Static instructions,
  provider messages, and tool schemas are unchanged.

- Desktop conversation UI now vendors assistant-ui Elements source components
  under `frontend/src/components/assistant-ui/` and shadcn registry output under
  `components/elements/`, without adding the assistant-ui runtime. Tailwind v4
  and shadcn compile the copied source; Azem remains the only event, state,
  approval, and persistence runtime. Registry `MessagePair` owns the turn plus
  user, assistant, progress, and error surfaces; registry `Composer` owns input,
  attachment, context, toolbar, and send chrome. ReasoningPanel, ToolTimeline,
  ApprovalCard, AgentPlan, and CodeBlock replace every runtime import from the
  former `beautiful-ui` directory. Official `data-slot` contracts and `aui-*`
  classes share the Azem token bridge. Plan cards expose real checklist
  progress, and the Inspector keeps a short 「任务计划」 heading with subordinate
  goal text.

- Chat file diffs now render through the registry-installed
  `@assistant-ui/elements-code-diff` component. A thin adapter maps durable
  `FileChange` data and removes duplicated file headers. Rows enter in 200ms
  with a 32ms stagger capped at six. The old table, line-number gutter, syntax
  highlighting, copy control, and vertical diff scroller are removed.

- Completed `coding.replace` and `coding.delete_file` calls now use the same
  structured live/replay file-change projection as hashline edits and writes.
  Deleted paths remain visible even without reconstructable diff lines, and
  replace/delete completion refreshes Git/workspace counts immediately.

- Messages and Composer now use the exact registry sources installed with
  `bunx --bun shadcn@latest add "@assistant-ui/elements-message-pair"` and
  `bunx --bun shadcn@latest add "@assistant-ui/elements-composer"`. Azem maps
  durable turn blocks into MessagePair slots and supplies ComposerContext with
  the actual ordered context groups and the same localized labels as Inspector.
  Provider input and current output are no longer mislabeled as fixed
  System/Tools/Messages buckets. IME, slash, attachment, approval, plan, model,
  queue/guide, and cancellation behavior remain Azem-owned.

- The macOS model picker now handles WKWebView's real tap-to-click ordering.
  Trackpad input can omit `pointerdown` or `click`, and can deliver release
  before a later-dispatched `buttons=0` press pair. The whole-chip toggle now
  accepts valid primary presses, unpaired primary releases, mouse-only
  sequences, click-only activation, keyboard, and assistive input, while
  deduplicating the remaining compatibility events. It does not infer a press
  from `button` alone, assume down-before-up order, or correlate timestamps.
  An inactive macOS window keeps the standard activation-only first click;
  losing focus closes the picker instead of enabling WebView-wide click-through.
  The persistent Portal, search, outside-click, Escape, model, run, and session
  dismissal paths remain intact.

- Desktop project and conversation rows are now compact single-line entries.
  Project headings keep only expand/collapse, the project name, and the
  project-scoped new-conversation action; monograms, branch/path subtitles, and
  session-count badges are removed. Session rows keep title and real
  running/unread state without relative-age labels.

- Expanding a completed pre-answer process no longer opens a second
  `56vh`/`520px` scroll pane. The elapsed-time row still folds the work, but
  opening it restores the complete process to the ordinary transcript flow
  immediately before the final answer, matching Codex.app. The transcript
  viewport remains the only conversation scrollbar.

- Assistant answers and progress prose now use the complete bounded transcript
  column instead of imposing a second `68ch` limit that left a large empty
  strip on the right. The transcript remains the single responsive width owner.

- Verification guardrail `uncertain` / `fail` verdicts remain durable internal
  state and now make the run terminally fail after the one allowed evidence
  retry. The host-owned reason is not inserted into the model's final answer.
  The desktop also strips the two exact legacy host-notice suffixes from
  existing assistant blocks, preserving real model text and hiding notice-only
  blocks.

- Core agent tool guidance now reuses completed search/read results for
  unchanged files, accepts hashline anchors from search, read, and successful
  edit results, batches same-snapshot changes, and re-reads only missing,
  changed, renumbered, or stale/conflicting ranges. Updating
  `internal/app/prompts/main.md` advances the static instruction fingerprint
  and provider cache epoch once; later turns reuse the stable new prefix.
  Tool schemas and provider message order are unchanged.

- Providers: upgrade to llmux v0.2.5. OpenRouter Responses streams that expose
  one tool call first as `fc_tmp_*` and then as a final UUID now produce one
  canonical SDK tool call. llmux correlates output indexes and raw/prefixed
  IDs, preserves progressive input events and distinct/id-less calls, makes
  duplicate terminal frames idempotent across every streaming protocol, and
  fails closed on conflicting identities, post-terminal calls, or bounded
  frame/state limits. Azem removes its temporary local alias deduplicator.
  Static instructions and provider message order are unchanged; canonical
  history now omits the phantom duplicate tail, so the cache epoch does not
  advance.

- Shell supervision distinguishes foreground descriptor/pipeline syntax such as
  `2>&1`, `<&`, `&>`, `|&`, and `&&`, but rejects every POSIX background
  operator. A new session can escape process-group cleanup even with a bounded
  wall clock, so foreground-only execution is the fail-closed boundary.
  Quote/backslash-concatenated detach primitives such as `set''sid` are also
  rejected. The advertised `coding.shell` definition changes, advancing static
  tool-definition cache identity once; instructions and provider message order
  are unchanged.

- llmux model catalogs now live in SQLite (`llmux_provider_models`, schema 22)
  instead of `config.yaml`. YAML keeps only provider `enabled` and `base_url`.
  Existing YAML `models:` lists import once on launch. Discover, enable, and
  disable update SQLite so OpenRouter-sized catalogs no longer bloat the
  config file. This does not change the static instruction prefix or provider
  message order.
  Startup also forwards the hydrated SQLite catalog into the provider runtime
  before the first turn, so a visible OpenRouter model is not rejected as
  unconfigured. Synchronous start failures render one timeline card instead
  of repeating below the transcript. This does not change the static
  instruction prefix or provider message order.

- Cursor model catalog now lists one family row. Thinking and Fast variants
  stay in that family's inventory caption and search aliases; they are not
  shown as independent cards or an exact-variant menu. A family switch still
  writes every raw ID atomically. This does not change the static instruction
  prefix or provider message order.

- Cursor's authenticated `GetUsableModels` response is fail-closed: HTTP,
  authentication, decode, and empty-catalog failures no longer become fresh
  bundled model rows. Existing successfully authenticated cached rows may still
  be surfaced through the catalog's explicit stale path.

- Settings 「从 API 获取」 is now a read-only probe. It projects discovered
  models into the current UI without storing the pending API key, SQLite
  catalog, YAML, or runtime provider configuration; only 「保存提供商」 persists
  those values.

- `coding.delete_file` now removes Unix files through root-anchored,
  no-symlink `openat`/`unlinkat` traversal. A repository symlink cannot redirect
  deletion outside the workspace; unsupported platforms fail closed.

- Subagents: the parent can no longer finish while any child of the current
  run is still running, queued, or initializing. `pending-background-children`
  keeps retrying until every child is terminal. The host still does not
  cancel a slow child; `idle_timeout` may cancel a silent one. A successful
  parent finish marks that run's terminal children delivered so auto-wake
  does not start a second stream. Changing `internal/app/prompts/main.md`
  rewrites the static instruction prefix, so provider prefix-cache hits
  reset once and then stay stable for later turns.

- Subagents no longer inherit the parent session's unfinished Todo completion
  gate. A child can finish its assigned read/review task after its own evidence
  checks; the parent still cannot finish until `pending-background-children`
  observes that child as terminal.

- Cold-start sidebar session clicks now use the same direct durable
  `ResumeSession` readback as global search. The initiating window applies the
  returned projection immediately instead of waiting only for a broadcast event
  racing bootstrap catalog/session requests.


- `coding.search` now scans every Git-tracked and unignored untracked text file
  instead of stopping after the first 1,000 walker entries. Dependency/build
  trees no longer crowd real source files out of the search set, `maxResults`
  caps matched lines rather than files examined, and matched files still enter
  the shared Hashline snapshot store for safe follow-up edits.

- Verification guardrails now accept current-SHA evidence from dedicated
  `coding.gofmt` calls instead of requiring the model to repeat the same work
  as a literal `gofmt -d` shell command. A no-op formatter no longer advances
  the mutation boundary, so valid final answers are not followed by a false
  verification retry.

- Timeline process totals inspect formatter results: `coding.gofmt` with
  `changed=false` remains visible as a tool step but no longer claims that a
  file was edited.


- Coding tools: `coding.glob`, `coding.replace`, and `coding.delete_file` are
  now first-class workspace tools for every provider. Hashline remains the
  default edit path; `coding.replace` applies unique `old_text`/`new_text`
  pairs through hashline. `coding.delete_file` removes one regular file after
  write approval. Cursor `pi_find`/`pi_edit`/`delete` map onto these names.
  Changing `internal/app/prompts/main.md` rewrites the static instruction
  prefix, so provider prefix-cache hits reset once and then stay stable.

- Coding tool adapters now preserve the outer call identity and exact trailing
  bytes. `coding.replace` refuses to rewrite from a truncated read,
  unsuccessful `coding.go_test` executions are tool errors, and successful
  replace/delete operations enter both durable file observations and completed
  change projections. A completed delete records expected absence without a
  false capture error; continuity verifies continued absence and marks a
  recreated path stale.

- Governed tool calls now reject duplicate JSON argument keys before approval
  or execution, including duplicates nested in arrays/objects or spelled with
  equivalent escapes. Ambiguous provider output returns a recoverable tool
  error with the original call identity and cannot select an unintended target.
  This host-side check does not change the static instruction prefix, message
  order, or provider cache identity.

- Todo initialization now matches the host-owned OMP lifecycle: the model
  supplies only a goal, phase titles, and item content. Azem assigns every
  phase/item ID and status, so a phase/item naming collision cannot invalidate
  the first durable snapshot. A complete goal-plus-phases payload is also
  treated as `init` when a provider omits only that discriminator; no other
  missing operation is inferred. The tool schema and executable prompt change
  the static prefix once; later turns retain the new stable prefix and cache
  identity.

- Trajectory export now recognizes schema-21's `{}` JSON spill sentinel as
  externalized data, loads the referenced BlobStore payload, and verifies its
  SHA-256. Real session exports no longer fail with a false inline-digest
  mismatch; non-sentinel mismatches and missing/corrupt blobs still fail closed.

- Desktop and TUI now treat Cursor as a reserved subscription like ChatGPT
  and Grok. Login uses Oh My Pi's `loginDeepControl` PKCE poll against
  `api2.cursor.sh`; models come from `GetUsableModels` with a bundled
  fallback; turns stream over Connect protobuf `AgentService/Run`. Native
  Cursor `read`/`bash`/`write`/`delete`/`grep`/`ls` and `pi_*` execs run
  on that same stream: Azem answers `request_context`, executes mapped
  coding tools through the existing approval path, and writes typed
  results. Existing-file writes become `coding.edit_hashline`. Native
  `delete` removes a regular workspace file only after write approval.
  `pi_edit` applies unique replace pairs then hashline; `pi_find` lists
  files by glob. Fetch, diagnostics, and other unknown execs are rejected
  on the Cursor stream so they cannot fail the Venat run as an unknown tool.
  Cursor now derives `conversation_id` from Azem's existing main, Team-role,
  subagent, title, recap, and vision cache keys; reuses checkpoints and blobs
  across driver instances; answers both KV get/set operations; rebuilds
  structured turns from canonical history; and sends `resume_action` after
  assistant/tool output. A shared user task remains the active action when
  private historical or vision evidence follows it, so that evidence stays
  context instead of replacing the request.
  Native execs now keep approval waits, durable tool records, file
  observations, live events, and exact call/result replay instead of
  disappearing inside the provider stream. Team roles and subagents bind
  request-scoped tool hosts, preventing one shared Cursor driver from routing
  calls to the wrong run. Server-confirmed Todo snapshots mirror into durable
  Todo state. Validated image and image-only messages use Cursor's native
  selected-image protocol, while Kimi K3 reasoning replays only from the same
  Cursor model. Local conversation state is account-scoped; frame, protobuf,
  checkpoint, and server-set blob validation prevents malformed remote state
  from replacing the last known good checkpoint. Remote protobuf decoding now
  caps field descriptors, aggregate decoded bytes, and nested Value depth
  before provider-controlled frames allocate maps/slices or recurse. A poisoned
  `resource_exhausted` conversation rotates its wire ID once without dropping
  validated state.
  Token deltas count as output, while checkpoint occupancy feeds context
  pressure without being recorded as billable input. Cursor does not expose
  cache-read tokens, so completed cache cards now say 未上报 / Not reported
  rather than waiting forever or retaining another provider's value. Cursor
  transport metadata advances to version 3,
  resetting only Cursor's derived cache identity once; subsequent turns keep
  an append-stable root prefix.
  Cursor settings read account email and membership tier from the dashboard,
  show Total, Cursor, and Third Party remaining lanes from `usage-summary`,
  and derive reset countdowns plus deficit/reserve and exhaustion forecasts
  from the billing-cycle boundary. They fall back to
  `api2.cursor.sh/auth/usage` when that dashboard request is unavailable.
  Cursor's raw tier, Thinking, and Fast model IDs now collapse into one
  base-model row in the composer and route selectors, matching Cursor's
  model-first interaction. Thinking now defaults on whenever the family has a
  matching same-tier variant and is no longer shown as a separate control or
  label. Reasoning depth chooses the tier and Fast remains optional; a family
  with no Thinking variants continues to use its standard IDs. Every change
  still persists and sends the selected raw Cursor ID. The model catalog card
  retains its exact-variant menu so individual raw IDs can still be managed.
  Fast remains configurable inside the model picker but the separate composer
  lightning button is removed; both Cursor and ChatGPT/Codex subscriptions show
  active Fast state only as `· Fast` in the selected-model summary. One-level
  reasoning ladders render full, align their sole label to the maximum end, and
  are removed from pointer and keyboard interaction.
  Folded rows now report both base-family and exact-variant counts, retain every
  raw ID as a search alias, and show tier/Fast availability. Cursor's
  account-scoped `GetUsableModels` response remains authoritative; static OMP
  rows absent from that response are not injected as potentially unusable
  models. Raw `(NO ZDR)` text is removed from the model name and replaced with
  a localized data-retention warning and explanation.
  Model catalog search matches a family name, variant label, alias, or exact raw
  ID while retaining the complete matching family. A family switch now changes
  every variant in one atomic configuration update; partial families show an
  explicit enabled count. The variant panel separates current version, family
  capabilities, availability, and a searchable exact-version selector.
  Grok OAuth now overlays and injects the nine current chat-capable curated
  models instead of replacing the catalog with a partial two-row response.
  Reasoning tiers are normalized when cached rows load, so restarting no
  longer disables the depth control until a manual model refresh. A later
  subscription catalog refresh also no longer wipes a live quota card.
  These catalog and projection changes do not alter the static instruction
  prefix or provider message order. Choosing a different Cursor tier changes
  the model identity intentionally; later turns on that exact tier keep the
  same cache identity.
- Reading an undeclared skill resource such as `SKILL.md`, or a resource
  from a skill that is not active, now returns an error tool result instead
  of aborting the Main, Team, or subagent run. The model sees the miss and
  can continue; the desktop no longer shows `missing tool result` for that
  call. Requires Venat v0.14.2.

- Live assistant text now sharpens from a readable blur tail, matching the
  Meta AI stream: newest increments fade in over 260ms with a 5px blur, up
  to eight in-flight ranges stay visible, and Markdown reparse resumes the
  same motion instead of replaying settled lines.

- Context compaction now has one deterministic host path across automatic,
  `/compact`, `/rebuild`, Main, Team, and subagent runs. It serializes omitted
  history into an exact SHA-verified session artifact, preserves the latest
  three complete shared user turns and atomic tool groups, and makes no
  compaction-provider request. Image-capable models receive bounded Silver-font
  bitmap frames; text-only, unknown, or over-budget routes receive a bounded
  preview plus `context.read_artifact` reference. Repeated compaction expands
  the old source, and restart recovery repairs missing frames from that source.
  `keep_recent_tokens` is now an optional hot-tail floor: Azem relaxes it when
  necessary but never drops the latest three complete turns. `agents.compaction`,
  model-written `SemanticStateV1`, background preparation, semantic revision
  activation, and their output-budget/retry settings were removed. ModelHistory
  wire version 3 and archive policy version 3 reset old derived cache identity
  once; later archives retain the stable system prefix and message ordering.

- Fixed automatic archiving that could remain idle while the provider reported
  98% context occupancy because the trigger trusted only the lower local
  `bytes/4` estimate and a fixed 16,384-token reserve. The trigger now uses the
  larger of local history pressure and current-run provider-reported input
  after tool definitions, and reserves at least 15% of every model context
  window. Successful archive activation clears the prior provider pressure.
  Static instructions and provider message order are unchanged; only an
  earlier archive carrier can start a new cache epoch.

- Fixed deterministic context activation after manifest-free pruning. The
  checkpoint CAS boundary now comes directly from the current run's persisted
  canonical user block, including resumed runs, rather than the pre-turn
  checkpoint or provider-facing message metadata. This prevents healthy long
  runs from failing with a stale checkpoint while preserving CAS rejection
  when another canonical message is appended after preparation. Static
  instructions, message order, and provider payloads are unchanged, so existing
  prefix cache hits survive.

- Manual context compaction now repairs legacy tool history before Snapcompact
  archiving. When both the call and result are absent from an older
  ModelHistory but the terminal durable tool record still exists, Azem restores
  the pair at its canonical anchor, including full artifact-backed output and
  failed state. Partial pairs, name mismatches, missing artifacts, and invalid
  records still fail closed. Static instructions are unchanged; a successful
  manual archive intentionally replaces only the derived history tail, so the
  stable provider prefix remains reusable.

- Evaluation and adaptation now use a versioned, evidence-bound offline
  pipeline. `azem-eval` exports deterministic durable trajectories and baseline
  identities; replay/noise fixtures, verification plans, structural retrieval,
  opt-in coding memory, shadow route calibration, isolated task synthesis,
  adapter comparison, generated-tool sandboxing, and held-out release gates
  remain outside live admission. Subagent cards expose whether work is
  provisional, verified, or stale. A validated adapter can replace one exact
  base model through the existing provider route and can be rolled back or
  killed without a second router.
  No executable prompt, static prefix, or injected-message ordering changed;
  existing provider prefix-cache identity is preserved.

- Settings usage now has a Lody-style skyline: a model donut beside the year
  heatmap, a stacked by-model bar, and a click-to-open day panel. Totals snap
  on first paint and ease on later refreshes. No invented cost figures.

- Settings layout now uses one enlarged typography scale across navigation,
  cards, route rows, controls, model catalogs, and Usage. Model route controls
  use bounded grid columns and clipped card geometry so Fast/reasoning controls
  cannot leave the card. The Usage activity heatmap uses responsive square
  cells across the full report width instead of a fixed 10px grid with a large
  empty right side.

- Persistence: schema 21 keeps SQLite as the catalog, FTS index, and Venat
  control plane, and stores large payloads as content-addressed files under
  `~/.azem/blobs`. Artifact bytes always live on disk. Tool
  content and structured results, subagent output/transcript, thinking/agent
  blocks, provider model history, and Venat event/record bodies spill when
  they exceed 4 KiB. User and completed assistant blocks stay inline so
  conversation search keeps working. Upgrading extracts existing inline bytes
  and vacuums the database. A 2.5 GiB developer `azem.db` was almost entirely
  Venat `events`; those rows now keep only a hash.
  Blob writes now track the atomic file creator and clean up unreferenced files
  under a SQLite writer lock after failed or ignored catalog writes.

- Home directory: configuration, SQLite, blobs, plugins, attachments, logs,
  and hook transcripts now live in `~/.azem` (or `$AZEM_HOME`). The first
  default-home launch moves `~/.config/azem`, the previous platform data
  directory, and the cache directory into that home. It fails if the old
  database is still locked. Project-local `{workspace}/.azem` is unchanged.

- Auto-review follows the Codex guardian matrix on the host: `low`/`medium`
  denials no longer fall back to a person unless the rationale is an explicit
  injection or tenant deny. `coding.shell` is classified per command, so
  `go test` and `git status` auto-allow without a model call. User-requested
  `git push` / `git commit` score high authorization from the turn goal, matching
  Codex, instead of falling back to a person just because the command is high-risk.
  Parse failures remain fail-closed.


- Agent instructions follow the current user-message language. Settings
  language is UI-only. Stated requirements stay in scope, verification
  stays on the Todo list, and exact strings or hidden cases must be
  observed before completion. Related read-only searches may share one
  commentary update. Package-manager work must not leave apt/dpkg/yum
  interrupted or remove tools later checks need.

- Headless `azem-eval` skips session title and recap side routes so those
  tokens stay on the coding turn.

- A remaining runtime deadline is injected as private tail context, not
  the static instruction prefix, so the model can finish a verifiable
  subset before a hard stop.

- Subagent idle cancellation now also treats a live `coding.shell` as
  activity. Silent children with no thinking still cancel; a running
  command does not.

- `coding.shell` accepts `stdin` for scripted keystrokes or piped input.

- Shell: `workspace.shell.max_wall_clock` (default `10m`) is the global
  per-command ceiling. `coding.shell` accepts `wall_clock_seconds` so the
  model can request how long a command may run, up to that ceiling.
  Settings → Subagents can change the ceiling without restarting.

- Eval: Harbor trials now install a CA bundle. Bare Ubuntu task images
  have no `ca-certificates`, so Grok catalog HTTPS failed with
  `x509: certificate signed by unknown authority` and Harbor recorded 0
  instead of a real score.

- Eval: add `azem-eval` and a Harbor installed-agent adapter so Azem can
  run Terminal-Bench (`tbench.ai`) unattended. Trials use YOLO approvals,
  copy only auth rows from the desktop database, and leave scoring to
  Harbor's verifier. The adapter reads Harbor's computed agent timeout
  (task.toml / override / multiplier) and stops `azem-eval` before
  Harbor's `wait_for`, so a still-running turn is not recorded as
  `AgentTimeoutError`. Before each Grok trial the host refreshes the
  subscription token and writes rotated credentials back after the
  container exits, so later trials do not reuse a spent refresh token.

- Grok OAuth refresh and revoke send the same client-version and
  `x-grok-client-surface=ui` headers as device login, and refresh HTTP
  errors include the OAuth error body.

- Grok 4.6 accepts the same `low` / `medium` / `high` / `xhigh` efforts as
  Grok 4.20. The previous ID fallback treated 4.6 as having no reasoning
  levels, so desktop `xhigh` never reached the API.

- Desktop transcript: sending a message no longer invents a `正在处理`
  rule or an empty 思考 row. ChatGPT.app only renders a Thinking
  placeholder in the assistant column after the model starts work, and
  drops it once answer text exists. Azem now waits for real thinking or
  tools before drawing that chrome, so the user bubble stays put.

- Desktop transcript: live assistant answers stream as ordinary prose.
  Unphased text no longer paints as a blue-dot pending card. Streaming
  and completed answers keep the same renderer and list spacing, so a
  bullet list does not snap in after the last token. Thinking-only turns
  stay as thinking plus prose instead of switching to an 已处理 fold.

- Desktop model catalog: attach the cached Grok and ChatGPT subscription
  catalogs to `list_model_providers` so Settings no longer shows `0/0`
  after a missed or empty `model_catalog` event. Prefer the signed-in
  account that already has cached models when more than one Grok account
  is active. An empty later catalog event does not wipe models that already
  rendered.

- Sessions: stop taking an exclusive workspace-write lock for main runs and
  shared-workspace subagents. Multiple conversations in the same project run
  in parallel. Crash recovery expires leftover exclusive claims from older
  builds so a stuck `reconcile_required` run cannot queue later sessions.

- Desktop startup: load persisted `hooks.disabled` identities instead of
  rejecting `config.yaml` with `field disabled not found in type
  config.HooksConfig`. Accept `agents.subagents.await_timeout: 0s` as
  wait-until the foreground child completes.

- Main agent prompt: before tools, write one or two ordinary English
  sentences of intent. Do not use a titled card or filler such as
  "I'm ready".

- Desktop transcript: switching sessions opens at the latest message,
  not the first line. The stage remount now follows the tail, and the
  pin repeats after history turns grow past the 180px placeholder.

- Desktop transcript: sending a follow-up no longer jumps the viewport
  back to the first line of the session. Finished turns keep their
  mounted node, and the tail is pinned after layout.

- Desktop transcript: thinking and individual tool rows do not print a
  second clock while the sparkle bar already shows elapsed time.
  Failure, queue, and approval labels stay. After the run settles,
  thinking-only trails keep the clock on the 思考 header and tool
  trails fold under 已处理.

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

- Desktop transcript: fenced code in answers uses the assistant-ui `CodeBlock`
  component with elevated paper, filename/language, Copy, and a line-number
  gutter. A completed tool step opens one flat `ToolTimeline`: reasoning first,
  then write/shell/read items and file statistics. Live wait and reasoning stay
  on the shared `ReasoningPanel` bar.

- Desktop frontend: the production bundle no longer ships one 666 kB
  entry chunk. xterm loads only after the terminal is first opened,
  Inspector and the subagent drawer stay on their own async chunks, and
  motion/xterm vendor groups are split out of the main entry.

- Desktop chrome: Workspace Files/Changes, Extensions, PR, Recovery,
  and the subagent drawer no longer use the 156–158px poster headers.
  Page titles sit on a compact bar. Change review no longer invents an
  architecture-violation count.

- Desktop transcript: first-token wait, live reasoning, and live search/tools
  share one left-aligned `ReasoningPanel` bar. Only the label changes; the
  element does not remount or reset its clock. A thinking-only trail remains
  「思考」 plus reasoning prose, while individual tool rows retain only their own
  completed duration. A settled tool step opens one `ToolTimeline` containing
  reasoning, tool items, and file statistics with an honest
  `N tool calls, N messages` header. There is no Steps / Reasoning / Search /
  Coding switcher.

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

- Desktop transcript: expanding a large completed process first paints
  collapsed ToolTimeline rows and mounts each row's diff, Markdown, reasoning,
  or Subagent detail only when that row is opened.

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

- Desktop transcript: first-token wait uses the shared `ReasoningPanel`
  (`思考` / `思考 0.3s`), not the pixel-grid LoadingState. The disabled wait
  trigger keeps the same charcoal ink as its settled label. Live reasoning,
  search, and tool states update that one mounted panel instead of creating a
  tab switcher.
  Completing a stream keeps the same mounted Markdown tree and only stops
  reveal/caret motion; the idle caret uses `content: none` so it cannot
  remain as a hairline between settled paragraphs. Elapsed time still
  starts at `0.1s` and never shows `0s`.

- Desktop transcript: tool calls render through assistant-ui
  `ToolTimelineItem` disclosures with an icon, verb, target, and status.
  Process folds may append honest tool/progress counts; executed file edits add
  compact `path +N -N` statistics only after execution.

- Desktop transcript: fenced Markdown code uses the assistant-ui `CodeBlock`
  component while streaming and after settlement. File-change diffs retain
  their existing review surface.

- Desktop Inspector: the task plan uses the assistant-ui AgentPlan hierarchy:
  a fixed short heading, bounded goal, honest `done/total` rule, phase labels,
  and completed/running/pending/cancelled marks.

- Desktop stop now cancels the active run and its subagents together. The
  previous stop left children running in the background after the parent
  ended. TUI still offers a parent-only choice when children are active.

- Desktop session: the assistant-ui Elements token layer stays global so chrome
  does not fall back to the prototype warm palette. Progress is ordinary
  `ProgressMessage` prose, not a duration card or old marker grid; user and
  assistant blocks use `Message` components. First-token wait and live
  reasoning use `ReasoningPanel`, with elapsed time in the same stable label.

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

- Desktop sidebar session titles and project headings follow the Appearance
  font size. The desktop layout pass no longer pins those labels to 11px.

- Subagent scheduling: raise the default concurrency to 32, accept zero
  as unbounded, and support recursive delegation with a default depth of
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
