# Configuration

Last verified: 2026-08-24

`internal/config.Config` and `internal/config.Default` are authoritative. Azem
strictly decodes YAML, applies defaults, and validates the complete result
before runtime construction. The default file is `~/.azem/config.yaml`;
`AZEM_HOME` selects another home, and `-config` selects another file.

## Main sections

| Section | Purpose |
|---|---|
| `defaults` | Provider, model, reasoning, language, agent mode, approval mode, and queue mode for new sessions |
| `workspace` | Initial TUI root and file, shell, network, output, shell concurrency, and per-command shell wall-clock ceiling |
| `auth` | Credential backend plus optional Codex, Grok, and Cursor imports |
| `providers` | Subscription transports and llmux provider/model registry |
| `retry` | Agent retry count and exponential backoff bounds |
| `agents` | Main, Team, title, plan, approval, vision, recap, Advisor, Vibe, loop guards, context, and subagent routes/budgets |
| `ttsr` | Stream text/AST interruption rules, context handling, and repeat policy |
| `security` | Native Standard/Deep defaults, stopping conditions, budgets, and audit/reducer/fixer/verifier model routes |
| `skills` | Discovery, trust, eager activation, and disabled entries |
| `plugins` | Azem-owned/Codex/marketplace packages, auto-update, and explicit hook trust |
| `extensions` | Bun tools, commands, providers, agents, themes, and project-code trust |
| `autolearn` | Managed Skill learning threshold and automatic continuation |
| `discovery` | Cross-harness context, rules, Skills, MCP, and hook discovery |
| `mcp` | Stdio or HTTP servers, environment, headers, OAuth, resources, prompts, notifications, timeouts, and tool policies |
| `hooks` | Lifecycle command handlers and failure policy |
| `memory`, `recap`, `background` | Optional supporting runtime services |

The maintained example in [README.md](../README.md#configuration) shows the
current field names and defaults. Duration values use Go duration syntax.

`workspace.shell.max_wall_clock` is the hard ceiling for one `coding.shell`
command (default `10m`). The model chooses a shorter deadline with
`wall_clock_seconds`. `timeout_seconds` remains the no-output watchdog and
cannot exceed that ceiling. Omitting `timeout_seconds` after setting
`wall_clock_seconds` lets a silent command run until the chosen wall clock.
`stdin` is optional UTF-8 fed to the process for scripted keystrokes or piped
input.

On POSIX, descriptor and pipeline syntax such as `2>&1`, `<&`, `&>`, `|&`,
and `&&` remain foreground syntax. Real `&` background operators are rejected:
a descendant can create another session and escape process-group cleanup even
when the original shell has a wall-clock limit. Known detachment primitives
(`setsid`, `daemonize`, `nohup`, and `disown`) are rejected after conservative
quote/backslash normalization as defense in depth. Use a foreground command or
an explicitly supervised Azem background process instead.

The desktop Subagents settings surface groups capacity and isolation controls,
shows parallel dispatch as a read-only product invariant, and lists
main-session display behavior separately. It edits recursive depth, two live
capacity limits, one per-command shell wall clock, one foreground wait window,
and one idle-cancel window without restarting the application:
`agents.subagents.max_depth`, `agents.subagents.max_concurrency`,
`workspace.shell.max_concurrency`, `workspace.shell.max_wall_clock`,
`agents.subagents.await_timeout`, and
`agents.subagents.idle_timeout`.
Subagent concurrency defaults to 32 and zero means unbounded. Recursive depth
defaults to 2; zero disables delegation and `-1` removes the recursion cap.
`await_timeout` defaults to `0s`, which keeps the parent tool waiting until the
foreground child completes. A positive duration never limits child runtime:
when it elapses, read-only or isolated worktree tasks continue in the background
and the parent can inspect them with `subagent.get_output`; a shared-workspace
writer keeps waiting in the foreground rather than racing the parent or being
cancelled. `-1` is not a second unlimited sentinel and is rejected.
`idle_timeout` defaults to `5m`. Zero disables the watchdog. A positive value
cancels a *running* child that has produced no thinking, output, or tool
activity for that duration. Empty thinking or text frames and elapsed-time UI
ticks do not count as activity. An open tool, including an approval wait, or a
live `coding.shell` process is not cancelled. Compaction and explicit wait states such as a workspace-claim
retry reset the idle clock. Settings updates must be `0` or 30–3600 seconds.
Changes pass through validated application actions, update the active runtime,
and are persisted with the same node-preserving YAML writer used by the other
runtime settings. Existing work is allowed to finish. An existing config that
already stores `idle_timeout: 0s` stays disabled.

Subagent token, tool-call, turn, and wall-clock budgets default to zero, which
means unbounded. `budget.soft_requests` defaults to 200 and injects one private
wrap-up reminder when crossed; it is advisory and never stops the run. Set it
to zero to disable the reminder. A child is cancelled by explicit
`subagent.kill`, a user stop that explicitly includes children, application
shutdown, or an optional configured `idle_timeout`. Provider context windows
still require deterministic archive compaction, but that is not a cumulative
task-size ceiling and does not depend on the semantic-index model.

## OMP-compatible modes and extension host

```yaml
ttsr:
  enabled: true
  context_mode: discard
  interrupt_mode: always
  repeat_mode: once
  repeat_gap: 10
  rules: []

agents:
  advisor:
    enabled: false
    provider: chatgpt
    model: gpt-5.6-luna
    reasoning: low
    catchup_timeout: 30s
  vibe:
    fast: { provider: chatgpt, model: gpt-5.6-luna, reasoning: low }
    good: {}
  loop_guards:
    thinking_enabled: true
    assistant_text_enabled: true
    tool_call_enabled: true
    tool_call_threshold: 5
    tool_call_exempt_tools: [hub, vibe_wait, subagent.get_output]
    unexpected_stop: mechanical
    unexpected_stop_retries: 2

extensions:
  enabled: true
  trust_project_code: false
  additional_tool_paths: []
  additional_command_dirs: []
  additional_extension_paths: []
  additional_agent_dirs: []
  additional_theme_dirs: []

autolearn:
  enabled: false
  auto_continue: false
  min_tool_calls: 5
```

Goal and checkpoint state is session data rather than global YAML. Vibe routes
inherit the active route when empty. `advisor.catchup_timeout` must be a valid
non-negative Go duration. TTSR rules validate text conditions, AST conditions,
scope, globs, and per-rule interruption mode. Project extension code remains
disabled until `extensions.trust_project_code` is explicit.

## Skills

Azem scans universal user Skills from `~/.agents/skills` by default, alongside
the legacy `~/.claude/skills`, Azem's configured directory, bundled Skills, and
explicit `skills.additional_dirs`. Project-local Skills remain gated by
`skills.trust_project`.

`skills.disabled` is the durable deny list for discovered Skill names. The
desktop Extensions page exposes every discovered Skill, including stopped
entries, with search and enabled/stopped filters. Its typed
`set_skill_enabled` action validates the name, rebuilds the prospective catalog,
runs configuration hooks, persists the selection atomically, and only then
publishes the new runtime snapshot.

A stopped Skill is excluded from the runtime registry, model-visible catalog,
eager activation, and composer slash suggestions. Keeping it in the management
catalog makes restoration possible without rediscovery or manual YAML edits.
Stopping an eager Skill also removes it from `skills.eager`; restoring it uses
on-demand activation so one control cannot produce an invalid eager-and-disabled
configuration.

## MCP servers

The desktop Extensions page uses the typed `refresh_mcp`, `reconnect_mcp`,
`set_mcp_enabled`, `upsert_mcp_server`, and `delete_mcp_server` actions. Its catalog comes from the
live MCP manager, so configured servers remain visible even when no Codex
plugin is installed. Enabling or adding a server first validates and atomically
updates `mcp.servers`, reconfigures the existing manager instance, publishes a
fresh runtime snapshot, and then connects asynchronously. Disabling a server
closes its current connection and removes its imported tools from subsequent
agent tool snapshots. Deleting any server atomically removes its YAML entry,
records the name in `mcp.removed_servers`, drops it from the live manager, and
closes its connection. The tombstone prevents built-in and plugin catalogs from
reconstructing the service after restart; adding the same name clears it.

Local entries use `transport: stdio`, a command, an argv list, and optional
working directory. Remote entries use `transport: streamable_http` and an HTTPS
URL; plain HTTP remains restricted to loopback hosts. Desktop header and
environment inputs accept only `env:NAME` or `keyring:NAME` references. Literal
secret values and plugin-scoped runtime variables are neither serialized nor
emitted to the frontend.

## Network proxy resolution

Azem resolves proxies at the shared HTTP transport boundary rather than as a
provider-specific setting. On macOS, a Finder-launched desktop build reads the
current SystemConfiguration proxy dictionary and refreshes it every five
seconds. HTTP, HTTPS, SOCKS, simple-hostname exclusion, exact hosts, wildcard
domains, and CIDR exceptions are supported. This gives ChatGPT OAuth and
streaming, Grok, llmux providers, models.dev discovery, remote MCP, and other
default HTTP clients the same active system proxy route.

`HTTP_PROXY`, `HTTPS_PROXY`, and their lowercase forms take precedence for the
matching request scheme. `NO_PROXY` keeps the standard Go bypass behavior. A
missing scheme-specific environment proxy does not suppress the native proxy
for that scheme; for example, `HTTP_PROXY` alone does not force an HTTPS request
to connect directly. Non-macOS builds continue to use these environment
variables. Proxy endpoints and credentials are runtime-only and are not
written to `config.yaml` or emitted to desktop events.

## Plugins

```yaml
plugins:
  enabled: true
  import_codex: true
  codex_imports: []
  trust_hooks: false
  marketplace_auto_update: notify  # off | notify | auto
```

The runtime always scans the Azem data directory at
`plugin-packages/`. Install Azem-only plugins under
`plugin-packages/local/<plugin>`. When `import_codex` is true, desktop startup
reads `codex plugin list --json` only to build an available-import catalog.
Nothing is copied or executed until its plugin ID is selected in
`codex_imports` through Settings. Selected packages are copied to
`plugin-packages/codex/<marketplace>/<plugin>` before scanning. Codex directories
are never runtime roots, and an existing selected Azem copy remains usable if
Codex is temporarily unavailable. Skills and eligible MCP servers from selected
copies are merged into the in-memory runtime configuration.
`trust_hooks` defaults to false because executable plugin hooks require an
explicit trust decision from the Extensions Hooks tab. Enabling it persists
the flag and loads those hook sources in the current process. Plugin Skills
and MCP still load without that decision. The compatibility matrix and
manifest rules are documented in
[plugins.md](plugins.md).

## Hooks

```yaml
hooks:
  enabled: true
  trust_project: false
  claude_compatibility: false
  default_timeout: 5s
  failure_policy: open
  additional_paths: []
  disabled: []
```

User and project hook files load from the existing discovery paths. Plugin
hook files are always cataloged so Extensions can list them; they enter the
runtime dispatcher only after `plugins.trust_hooks` is true.

`hooks.disabled` is the durable deny list for individual hook identities, in
the same shape as `skills.disabled`. Each identity is
`event`, `name`, cleaned `source` path, and `matcher`, joined by a unit
separator (`U+001F`). The desktop Extensions Hooks tab exposes every
discovered command with a per-row switch. Its typed `set_hook_enabled` action
validates the identity against the current catalog, persists the deny list
atomically, rediscovers the in-memory registry, and then publishes a fresh
`hook_catalog` snapshot. A disabled hook remains visible and is skipped at
dispatch; it does not get a second execution or approval path.

An enabled plugin hook still does not run until plugin hooks are trusted.
Closing trust unloads plugin sources immediately and leaves `hooks.disabled`
unchanged. An unknown `hooks.*` field fails closed on load.

## llmux providers and models

`providers.llmux` is keyed by a provider ID from llmux's profile registry.
ChatGPT, Grok, and Cursor remain Azem subscription transports and appear in
desktop Model settings as login cards rather than API-key profiles.

llmux v0.2.1 provider IDs use the canonical models.dev hyphen form, for example
`alibaba-coding-plan`. Azem accepts an existing underscore spelling while
loading and rewrites it to the canonical ID the next time that provider is
saved; the desktop catalog and logo URLs never expose the legacy spelling.

```yaml
providers:
  llmux:
    openrouter:
      enabled: true
      base_url: ""
```

- Empty `base_url` uses llmux's profile default. Overrides require HTTPS;
  plain HTTP is accepted only for loopback hosts.
- Model settings displays a profile's official default as read-only and saves
  an empty override so llmux remains the source of truth. Only profiles without
  a usable public default expose an editable address, and they cannot be
  enabled until a valid custom endpoint is supplied. Azem fills the currently
  omitted OpenCode Zen, FreeModel, and Xpersona defaults from models.dev.
- A provider may define at most 2,048 unique model IDs. Known context windows
  must be between 1,024 and 10,000,000 tokens; zero records that the upstream
  catalog did not publish a limit.
- Discovered and imported llmux catalogs are stored in SQLite table
  `llmux_provider_models`, not in `config.yaml`. YAML may still contain a
  legacy `models:` list; the next launch copies it into SQLite and rewrites
  the provider without that list.
- `disabled: true` on a stored model keeps it in Model settings for later
  re-enabling while removing it from model pickers and rejecting it at runtime.
- `max_output_tokens` is the positive per-request generation ceiling reported
  by the provider or models.dev. Zero means “unknown/unset”, not unlimited;
  when positive, Azem forwards it to llmux for main and subagent requests.
- Reasoning levels must be unique and the default, when set, must be one of
  them.
- A provider may be enabled before models are configured so Model settings can
  use its credential to fetch the live catalog. It cannot be selected for a
  turn until at least one returned or manually entered model is saved.
- The desktop can fetch models from the provider API and merge matching
  display names, aliases, capabilities, and reasoning options from models.dev.
  Fetch is a transient preview: only the explicit Save provider action stores
  the pending API key, returned metadata, YAML, and live runtime configuration.
  Aliases can resolve an existing route, but runtime requests always use the
  provider's actual `id`. API keys are never serialized to YAML.

Cursor is a reserved subscription (`providers.cursor`), not an llmux API-key
profile. Desktop Settings and `/login cursor` open Cursor's
`loginDeepControl` page and poll `api2.cursor.sh/auth/poll`, the same CLI
flow Oh My Pi uses. `/login cursor --import` stores `CURSOR_ACCESS_TOKEN`
and optional `CURSOR_REFRESH_TOKEN`. The live catalog is account-scoped
`GetUsableModels` over HTTP/2 protobuf. Authentication, network, decode, and
empty-response failures remain visible; only a previous successful catalog may
be shown through the explicit stale-cache path. Turns use Connect protobuf
`/agent.v1.AgentService/Run`. Native Composer execs stay on that stream:
Azem answers `request_context`, runs mapped coding tools through the
existing approval path, writes typed results, and deletes regular files
only after write approval.

Cursor's endpoint returns exact tier/Thinking/Fast IDs and a `max_mode` bit,
but no numeric context window. Azem keeps those exact IDs in configuration and
on the wire. Composer and route selectors show one base model. If the family
has a matching same-tier Thinking variant, new selections and later depth/Fast
changes use it automatically; no Thinking toggle or label is rendered.
Families without Thinking variants use their standard IDs. Fast changes inside
the model picker and appears outside only as `· Fast`; there is no standalone
composer lightning button. Provider settings list one family row. A family
switch writes every raw ID in `disabled_models` atomically; Thinking and Fast
variants stay in that family's inventory caption and are not independent catalog
cards.
`1M` labels, native Kimi K3 and GLM 5.2+ IDs, and Claude/Gemini max mode resolve
to a 1,000,000-token window. Unknown models remain at the conservative
200,000-token fallback; Fast variants without a `1M` signal keep their separate
200,000-token budget.

The selector displays both base-family and enabled raw-variant counts. Raw IDs
remain searchable after folding. Model settings also searches family names,
variant labels, aliases, and raw IDs while retaining every variant in a matching
family. A family availability switch writes all of its raw IDs atomically.
The catalog does not list Thinking or Fast variants as independent rows.
`GetUsableModels` is account-scoped and authoritative; Azem does not add static
OMP models that Cursor did not return. When a returned display name contains
`(NO ZDR)`, Azem stores the exact raw ID but presents a localized data-retention
warning. Such a model does not carry a zero-data-retention guarantee.

Cursor conversations reuse the runtime's logical prompt-cache keys rather than
minting an ID for every request. Main, Team-role, subagent, title, recap, and
vision routes remain isolated from one another. The transport replays
conversation checkpoints, services both blob reads and writes, and sends a
`resume_action` after assistant/tool output. Private evidence appended after a
shared user task stays provider context and cannot replace that task as the
active action. Cursor supplies output-token and context-occupancy signals but
not cache-read token counts; cache efficiency therefore remains **Not
reported**.

Native Cursor tools remain approval-gated and produce the same durable tool
records, live events, file observations, and restart replay as ordinary tools.
Server-confirmed Todo snapshots mirror into one durable Cursor phase. Image
attachments use the shared trusted-root loader, and image-only user turns stay
real user actions. Local checkpoints are account-scoped; malformed or
oversized frames/blobs are rejected before replacing the last known good
state. Kimi K3 reasoning is replayed only for the same Cursor model.

Settings quota prefers `cursor.com/api/usage-summary` with
`WorkosCursorSessionToken=<userId>::<accessToken>`. It projects
`totalPercentUsed` as Total, `autoPercentUsed` as Cursor, and
`apiPercentUsed` as Third Party. `billingCycleStart` and `billingCycleEnd`
drive the shared reset countdown, pace marker, deficit/reserve text, and
projected exhaustion time. `/api/auth/me` supplies the account email while
`membershipType` supplies labels such as Cursor Ultra. If the dashboard
summary is unavailable, Azem falls back to Bearer
`api2.cursor.sh/auth/usage`.

Cursor can return a raw model ID for every reasoning, Thinking, and Fast
combination. Model settings groups entries only when stripping recognized
suffixes yields the same base ID. The searchable version panel selects the raw
ID shown for inspection; the family switch enables or disables the complete
group in one configuration mutation. Route persistence and runtime requests
continue to use exact raw IDs. ChatGPT and Grok stay reserved separately. None
of this changes the static instruction prefix or provider message order.


## Credentials

API keys are resolved in this order:

1. Active credential stored through `internal/auth`.
2. The environment variable declared by the llmux provider profile.
3. No key, only for profiles that explicitly permit anonymous local access.

The UI sends a new API key only in the typed provider update or model-discovery
action. Discovery can use that pending value without storing it. Backend events
return `CredentialConfigured` and `CredentialSource`, never secret material.
An empty API-key field preserves and reuses the existing credential.

OpenAI/ChatGPT, Grok, and Cursor subscription entries reuse the existing
credential service and live subscription catalogs. They do not accept an API
base URL or API key in Model settings; login, account identity, plan,
provider-specific quota, reset time, available credit balance, model
availability controls, and logout are projected into the same provider
directory. Grok identity prefers
the email or handle from the ID token or CLI-proxy `/v1/user` profile; the
settings page never shows access tokens. Grok quota first loads `/v1/user`
without `x-userid`, then calls `/v1/billing?format=credits` with that live
user id. Both GETs use the shared `internal/netproxy` transport and retry
once after a connection EOF or reset. Billing parsing follows the Grok CLI
credits JSON: `config.creditUsagePercent`, then
`onDemandUsed`/`onDemandCap`, then a parseable `currentPeriod` as zero
usage. The settings card labels that window weekly, monthly, or credits
from `currentPeriod.type`. Quota failures keep the backend error on the
settings page. Disabled subscription IDs persist in
`providers.chatgpt.disabled_models`, `providers.grok.disabled_models`, or
`providers.cursor.disabled_models` and follow the same picker/runtime rules as
llmux models.

### Auth broker

```yaml
auth:
  broker:
    url: https://broker.example.com
    token: ""                 # prefer AZEM_AUTH_BROKER_TOKEN
    snapshot_cache: ""
    snapshot_ttl: 1h
    account_pool_file: ""
```

`auth.broker.url` requires HTTPS except for loopback HTTP. The token is omitted
from runtime JSON/events and may also come from `AZEM_AUTH_BROKER_TOKEN` or its
OMP-compatible alias `OMP_AUTH_BROKER_TOKEN`. Relative cache/pool paths resolve
from the config directory. The encrypted snapshot TTL defaults to `1h`; zero
forces a remote refresh. Broker-backed runtimes are read-only for credential
mutations and do not import local Codex/Grok credentials.

The operator registry also exposes `auth-broker serve`, `auth-gateway serve`,
token/status commands, and account-pool selection through flags/config. Service
bind addresses and bearer-token files are command arguments/environment, not
persisted secrets.

## Model routes

Desktop Role models configures these independent routes:

- `main`: `defaults.provider`, `defaults.model`, and `defaults.reasoning`.
- `title`, `plan`, `approval`, `vision`, and `recap`: matching entries under `agents`.
- `subagent`: the named role under `agents.subagents.routes`.

Non-main empty routes inherit from the active session except `vision`, which is
intentionally explicit: an empty `agents.vision` disables image fallback. Main
is an explicit default and cannot be reset to an empty route. Every selected
provider/model is resolved by the live provider runtime before the change is
persisted. A vision route whose catalog explicitly excludes image input is
rejected; models with incomplete modality metadata remain selectable for
provider compatibility.
Internal structured and short-text generation first honors the configured
reasoning effort. If a reasoning model exhausts its output budget before
emitting any final text, Azem retries that internal request once at `low`
reasoning; ordinary provider failures and empty completed responses still fail
explicitly.
`agents.approval` accepts any enabled subscription or llmux provider/model. If
unset, it inherits the active session route like the other non-main routes.
The reviewer always receives an explicit JSON-only output contract. Providers
whose native protocol does not implement response-schema enforcement may still
wrap that single object in one complete `json` Markdown fence; Azem unwraps
only that whole-response form and then applies the same strict field, enum, and
trailing-content validation. Prose mixed with a decision remains invalid and
fails closed.

`agents.recap` independently selects the lightweight model that writes the
bounded continuity summary shown in the desktop Inspector after a successful
turn. It defaults to ChatGPT Luna at low reasoning. Clearing the route restores
normal non-main inheritance from the active session; changing it never changes
the current conversation model or context archive.

Context archiving has no model route. Automatic, manual `/compact`, and
`/rebuild` paths all use the same deterministic host kernel. The kernel reads
the selected model's configured or persisted context-window and modality
metadata, but it does not resolve credentials, refresh the catalog, open a
provider connection, or request model-generated JSON. Missing local context
metadata fails explicitly.

`agents.context` controls this archive lifecycle:

|Field|Default|Behavior|
|---|---:|---|
|`enabled`|`true`|Enable automatic and explicit context archiving.|
|`reserve_tokens`|`16384`|Minimum headroom removed from the model context window before computing the archive trigger. The effective reserve is the larger of this value and 15% of the context window. Tool-definition tokens are removed separately.|
|`keep_recent_tokens`|`20000`|Preferred verbatim hot-tail floor. If that optional floor prevents the carrier from fitting, Azem relaxes it but still preserves the latest three complete shared user turns.|
|`large_tool_result_tokens`|`12000`|Artifact-offload threshold for large tool results.|
|`history_retrieval_tokens`|`4096`|Private session-history FTS evidence budget.|

Automatic archiving uses `effective_reserve = max(reserve_tokens,
floor(context_window * 0.15))` and runs when the larger of the local history
estimate and the current run's last completed provider-reported input (minus
tool-definition tokens) exceeds `context_window - tool_definition_tokens -
effective_reserve`. Before building an archive, Azem offloads eligible stale
tool results to exact durable artifacts.
It then preserves system messages, the latest three complete shared user turns,
assistant/tool-call/result atomicity, and current Todo guidance. Older history
is serialized losslessly to a session-scoped `context_archive` artifact.
Repeated archiving expands the previous artifact first, so archives never nest
or progressively summarize one another.

The archive carrier follows the active model's catalog modality. Explicit
image support enables bounded PNG bitmap frames backed by an exact source
artifact. Explicit text-only support, unknown metadata, an oversized frame, or
an unavailable renderer uses a bounded text/artifact carrier. This path does
not call `agents.vision`: the archive source remains recoverable independently
of image understanding.

When the selected main model advertises text input but no image input, Azem
sends the current turn's validated images to `agents.vision`. The helper returns
bounded textual evidence, which is marked untrusted and supplied privately to
the main model; the main provider request contains no image parts. If the main
model supports images, Azem keeps the native direct-image path. If modality
metadata is unknown, Azem also preserves the native path rather than guessing.
For a known text-only model, missing or failing vision configuration produces an
actionable turn error instead of silently discarding the image.

## Security scanning

`security.enabled` gates new scan actions without deleting existing records or
artifacts. It defaults to `true`; disabling it hides new execution while
retaining completed scans, findings, and exports.

| Field | Default | Behavior |
|---|---:|---|
| `default_mode` | `standard` | Default new-scan mode. |
| `workers` | `4` | Concurrent independent Deep audit workers; range 1–32. |
| `subagents` | `3` | Read-only security child allowance per audit; range 0–32. |
| `stop_after_no_new` | `4` | Consecutive reductions with no new root finding before saturation. |
| `stop_after_consecutive_errors` | `3` | Consecutive audit/reducer failures before terminal failure. |
| `max_discovery_runs` | `40` | Maximum independent Deep audits; range 1–1000. |
| `max_time_hours` | `96` | Positive absolute scan deadline, persisted across restart, at most 96 hours. |
| `max_cost_usd` | `0` | Reserved for trusted provider pricing and must remain zero. |
| `publication_tool` | empty | Exact configured MCP tool allowed for explicit TUI publication. |
| `publication_destination` | tool name | Stable receipt/deduplication destination key. |
| `publication_arguments` | empty | Host-owned MCP arguments merged with generated title/description. |
| `publication_title_field` | `title` | Configured MCP title property. |
| `publication_description_field` | `description` | Configured MCP description property. |

Older files may still contain `max_tokens` or `max_tool_calls`. Azem accepts
and preserves those keys for compatibility, but native scans ignore them and
Desktop neither exposes nor sends them. Azem therefore has no per-scan Token or
tool-call hard ceiling. Provider and account usage limits still apply.

`security.routes.audit`, `reducer`, `fixer`, and `verifier` use the existing
`ModelRouteConfig` shape. Empty routes inherit the configured default model.
The resolved provider/account/model/reasoning snapshot is persisted with the
scan and never changes silently on resume. Renderer payloads cannot override
routes, the absolute deadline, repository identity, or Deep fan-out.

Desktop users can edit these values in **Settings → Security scans**. The pane
persists enablement, default mode, Deep convergence limits, and the deadline,
plus the four security model routes. Changes apply to new scans;
active scans keep their sealed startup snapshot. External publication tools,
field mappings, and bounded non-secret base arguments remain trusted
administrator-only `config.yaml` settings; Desktop displays only the tool name
and never receives publication arguments or credentials.

## Safe updates

Runtime settings use the node-preserving YAML updater in
`internal/config/loader.go`, retaining unrelated keys and comments and writing
atomically. Configuration hooks run before the mutation. Secret values use the
selected credential backend and never pass through that YAML writer.

## Desktop appearance preferences

Theme, interface font, interface font size, and chat-surface text sizes are
desktop-only preferences. The searchable font picker reads installed families
and their localized names from macOS AppKit, Linux fontconfig, or the Windows
installed font collection. Preferences apply immediately, persist in the
WebView's local storage, and do not modify `config.yaml`. Interface font size
is clamped to 11–20 px; the default is the operating-system UI font at 14 px
and applies to chrome such as the sidebar, Settings, and Inspector shell.
Conversation UI text (`azem:chat-font-size`, default 13 px, 12–20 px) and
fenced code (`azem:chat-code-font-size`, default 12 px, 11–18 px) are
independent and apply only on `.thread-surface` and the subagent side-chat
transcript. Code blocks keep their dedicated monospace stack.

## Usage ledger

Settings → Usage is not a configuration surface. It reads completed
`provider_requests` (and completed `hydaelyn_activate_skill` rows when present)
for the current project or all projects. The query window is 366 local-calendar
days, with at most 20 models and 20 skills returned. Cache counters use only
`cache_reported` / `cache_write_reported` facts; unknown providers stay
unreported. There is no YAML field and no usage polling.
