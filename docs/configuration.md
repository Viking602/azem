# Configuration

Last verified: 2026-08-07

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
| `mcp` | Stdio or HTTP servers, environment, headers, timeouts, and tool policies |
| `hooks` | Lifecycle command handlers and failure policy |
| `memory`, `recap`, `background` | Optional supporting runtime services |

The maintained example in [README.md](../README.md#configuration) shows the
current field names and defaults. Duration values use Go duration syntax.

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
  and configure every field above. Aliases can resolve an existing route, but
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
quota, reset time, available credit balance, read-only live model catalog, and
logout are projected into the same provider directory.

## Model routes

Desktop Role models configures these independent routes:

- `main`: `defaults.provider`, `defaults.model`, and `defaults.reasoning`.
- `title`, `plan`, and `compaction`: matching entries under `agents`.
- `subagent`: the named role under `agents.subagents.routes`.

Non-main empty routes inherit from the active session. Main is an explicit
default and cannot be reset to an empty route. Every selected provider/model is
resolved by the live provider runtime before the change is persisted.

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
