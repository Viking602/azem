# Configuration

Last verified: 2026-08-06

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
ChatGPT and Grok are reserved for Azem's subscription transports and therefore
do not appear in desktop Model settings.

```yaml
providers:
  llmux:
    openrouter:
      enabled: true
      base_url: ""
      models:
        - id: openai/gpt-5.4
          name: GPT-5.4
          context_window: 272000
          reasoning_levels: [low, medium, high, xhigh]
          default_reasoning: high
```

- Empty `base_url` uses llmux's profile default. Overrides require HTTPS;
  plain HTTP is accepted only for loopback hosts.
- A provider may define at most 256 unique model IDs. Context windows must be
  between 1,024 and 10,000,000 tokens.
- Reasoning levels must be unique and the default, when set, must be one of
  them.
- Enabling a provider requires at least one configured model before it can be
  selected by the runtime.
- The desktop can configure every field above. It never serializes API keys to
  YAML.

## Credentials

API keys are resolved in this order:

1. Active credential stored through `internal/auth`.
2. The environment variable declared by the llmux provider profile.
3. No key, only for profiles that explicitly permit anonymous local access.

The UI sends a new API key only in the `set_model_provider` action. Backend
events return `CredentialConfigured` and `CredentialSource`, never secret
material. An empty API-key field preserves the existing credential.

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
