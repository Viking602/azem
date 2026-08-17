# Configuration

Last verified: 2026-08-17

`internal/config.Config` and `internal/config.Default` are authoritative. Azem
strictly decodes YAML, applies defaults, and validates the complete result
before runtime construction. The default file is `azem/config.yaml` under the
operating-system user configuration directory; `-config` selects another file.

## Main sections

| Section | Purpose |
|---|---|
| `defaults` | Provider, model, reasoning, language, agent mode, approval mode, and queue mode for new sessions |
| `workspace` | Initial TUI root and file, shell, network, output, shell concurrency, and per-command shell wall-clock ceiling |
| `auth` | Credential backend plus optional Codex and Grok imports |
| `providers` | Subscription transports and llmux provider/model registry |
| `retry` | Agent retry count and exponential backoff bounds |
| `agents` | Main, Team, title, plan, compaction, context, and subagent routes/budgets |
| `skills` | Discovery, trust, eager activation, and disabled entries |
| `plugins` | Azem-owned plugin packages, optional Codex copy import, and explicit hook trust |
| `mcp` | Stdio or HTTP servers, environment, headers, timeouts, and tool policies |
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
shutdown, or an optional configured `idle_timeout`. Provider
context windows still require semantic compaction, but that is not a cumulative
task-size ceiling.

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
ChatGPT and Grok remain Azem subscription transports and appear in desktop
Model settings as login cards rather than API-key profiles.

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
      models:
        - id: openai/gpt-5.4
          disabled: false
          name: GPT-5.4
          aliases: [gpt-latest]
          context_window: 272000
          max_output_tokens: 128000
          reasoning_levels: [low, medium, high, xhigh]
          default_reasoning: high
          capabilities: [tools, reasoning, structured-output]
          input_modalities: [text, image]
          output_modalities: [text]
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
- `disabled: true` keeps an llmux model in Model settings for later re-enabling
  while removing it from model pickers and rejecting it at runtime.
- `max_output_tokens` is the positive per-request generation ceiling reported
  by the provider or models.dev. Zero means “unknown/unset”, not unlimited;
  when positive, Azem forwards it to llmux for main and subagent requests.
- Reasoning levels must be unique and the default, when set, must be one of
  them.
- A provider may be enabled before models are configured so Model settings can
  use its credential to fetch the live catalog. It cannot be selected for a
  turn until at least one returned or manually entered model is saved.
- The desktop can fetch models from the provider API, merge matching
  display names, aliases, capabilities, and reasoning options from models.dev,
  and persist the returned metadata above. Aliases can resolve an existing route, but
  runtime requests always use the provider's actual `id`. It never serializes
  API keys to YAML.

## Credentials

API keys are resolved in this order:

1. Active credential stored through `internal/auth`.
2. The environment variable declared by the llmux provider profile.
3. No key, only for profiles that explicitly permit anonymous local access.

The UI sends a new API key only in the typed provider update or model-discovery
action. Discovery can use that pending value without storing it. Backend events
return `CredentialConfigured` and `CredentialSource`, never secret material.
An empty API-key field preserves and reuses the existing credential.

OpenAI/ChatGPT and Grok subscription entries reuse the existing OAuth/CLI
credential service and live subscription catalogs. They do not accept an API
base URL or API key in Model settings; login, account status, plan, live weekly
quota, reset time, available credit balance, model availability controls, and
logout are projected into the same provider directory. Grok identity prefers
the email or handle from the ID token or CLI-proxy `/v1/user` profile; the
settings page never shows access tokens. Grok quota first loads `/v1/user`
without `x-userid`, then calls `/v1/billing?format=credits` with that live
user id. Both GETs use the shared `internal/netproxy` transport and retry
once after a connection EOF or reset. Billing parsing follows the Grok CLI
credits JSON: `config.creditUsagePercent`, then
`onDemandUsed`/`onDemandCap`, then a parseable `currentPeriod` as zero
usage. The settings card labels that window weekly, monthly, or credits
from `currentPeriod.type`. Quota failures keep the backend error on the
settings page. Disabled
subscription IDs persist in `providers.chatgpt.disabled_models` or
`providers.grok.disabled_models` and follow the same picker/runtime rules as
llmux models.

## Model routes

Desktop Role models configures these independent routes:

- `main`: `defaults.provider`, `defaults.model`, and `defaults.reasoning`.
- `title`, `plan`, `approval`, `vision`, `compaction`, and `recap`: matching entries under `agents`.
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
the semantic compaction route or the current conversation model.

When the selected main model advertises text input but no image input, Azem
sends the current turn's validated images to `agents.vision`. The helper returns
bounded textual evidence, which is marked untrusted and supplied privately to
the main model; the main provider request contains no image parts. If the main
model supports images, Azem keeps the native direct-image path. If modality
metadata is unknown, Azem also preserves the native path rather than guessing.
For a known text-only model, missing or failing vision configuration produces an
actionable turn error instead of silently discarding the image.

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
