# Configuration

Last verified: 2026-08-11

`internal/config.Config` and `internal/config.Default` are authoritative. Azem
strictly decodes YAML, applies defaults, and validates the complete result
before runtime construction. The default file is `azem/config.yaml` under the
operating-system user configuration directory; `-config` selects another file.

## Main sections

| Section | Purpose |
|---|---|
| `defaults` | Provider, model, reasoning, language, agent mode, approval mode, and queue mode for new sessions |
| `workspace` | Initial TUI root and file, shell, network, output, and shell concurrency policy |
| `auth` | Credential backend plus optional Codex and Grok imports |
| `providers` | Subscription transports and llmux provider/model registry |
| `retry` | Agent retry count and exponential backoff bounds |
| `agents` | Main, Team, title, plan, compaction, context, and subagent routes/budgets |
| `skills` | Discovery, trust, eager activation, and disabled entries |
| `plugins` | Codex plugin-directory import and explicit hook trust |
| `mcp` | Stdio or HTTP servers, environment, headers, timeouts, and tool policies |
| `hooks` | Lifecycle command handlers and failure policy |
| `memory`, `recap`, `background` | Optional supporting runtime services |

The maintained example in [README.md](../README.md#configuration) shows the
current field names and defaults. Duration values use Go duration syntax.

The desktop Subagents settings surface edits three live runtime limits without
restarting the application: `agents.subagents.max_concurrency`,
`workspace.shell.max_concurrency`, and `agents.subagents.await_timeout`.
Changes pass through validated application actions, update the active runtime,
and are persisted with the same node-preserving YAML writer used by the other
runtime settings. Existing in-flight shell commands and subagents are allowed
to finish; the new capacity applies to subsequent admission decisions.

## Skills

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
`set_mcp_enabled`, and `upsert_mcp_server` actions. Its catalog comes from the
live MCP manager, so configured servers remain visible even when no Codex
plugin is installed. Enabling or adding a server first validates and atomically
updates `mcp.servers`, reconfigures the existing manager instance, publishes a
fresh runtime snapshot, and then connects asynchronously. Disabling a server
closes its current connection and removes its imported tools from subsequent
agent tool snapshots.

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
  trust_hooks: false
```

`import_codex` reads the installed and enabled catalog returned by
`codex plugin list --json` when the desktop runtime starts. Skills and eligible
MCP servers are merged into the in-memory runtime configuration; this never
rewrites `config.yaml`. `trust_hooks` defaults to false because executable
plugin hooks require an explicit trust decision. Plugin changes take effect in
a newly started desktop session. The compatibility matrix and manifest rules
are documented in [plugins.md](plugins.md).

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
logout are projected into the same provider directory. Disabled subscription
IDs persist in `providers.chatgpt.disabled_models` or
`providers.grok.disabled_models` and follow the same picker/runtime rules as
llmux models.

## Model routes

Desktop Role models configures these independent routes:

- `main`: `defaults.provider`, `defaults.model`, and `defaults.reasoning`.
- `title`, `plan`, `approval`, `vision`, and `compaction`: matching entries under `agents`.
- `subagent`: the named role under `agents.subagents.routes`.

Non-main empty routes inherit from the active session except `vision`, which is
intentionally explicit: an empty `agents.vision` disables image fallback. Main
is an explicit default and cannot be reset to an empty route. Every selected
provider/model is resolved by the live provider runtime before the change is
persisted. A vision route whose catalog explicitly excludes image input is
rejected; models with incomplete modality metadata remain selectable for
provider compatibility.
`agents.approval` accepts any enabled subscription or llmux provider/model. If
unset, it inherits the active session route like the other non-main routes.
The reviewer always receives an explicit JSON-only output contract. Providers
whose native protocol does not implement response-schema enforcement may still
wrap that single object in one complete `json` Markdown fence; Azem unwraps
only that whole-response form and then applies the same strict field, enum, and
trailing-content validation. Prose mixed with a decision remains invalid and
fails closed.

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

Theme, interface font, and interface font size are desktop-only preferences.
The searchable font picker reads installed families and their localized names
from macOS AppKit, Linux fontconfig, or the Windows installed font collection.
Preferences apply immediately, persist in the WebView's local storage, and do
not modify `config.yaml`. Interface font size is clamped to 11–20 px; the
default is the operating-system UI font at 14 px. Code blocks and tool output
retain their dedicated monospace stack.
