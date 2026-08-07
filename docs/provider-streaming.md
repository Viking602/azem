# Provider Streaming

Last verified: 2026-08-07

Azem normalizes every provider into Venat's `provider.Driver` contract. The
application runtime owns provider/model selection, retries, usage persistence,
tool execution, and UI events; transport packages own wire requests and stream
parsing.

## Transports

| Provider ID | Transport |
|---|---|
| `chatgpt` | Existing Codex Responses subscription driver |
| `grok` | Existing xAI API or CLI-proxy subscription driver |
| llmux profile IDs | `internal/provider/llmux`, backed by llmux v0.2.3 |

The llmux adapter supports its native OpenAI, Anthropic, Google, Mistral,
Cohere, and xAI providers plus its OpenAI-compatible registry. ChatGPT and Grok
IDs remain reserved so an existing subscription configuration cannot silently
change authentication or protocol.

Those reserved subscription transports still appear in desktop Model settings
as login cards. Their actions call the existing ChatGPT browser OAuth and Grok
device authorization flows; successful login refreshes the authenticated model
catalog, while logout removes the active account projection.

The desktop catalog loads provider profiles in 24-item batches as its directory
scrolls, while search still matches the complete catalog. Protocol selection
comes from the llmux compatibility profile: an `anthropic-messages` profile is
constructed with llmux's Anthropic driver and calls `/v1/messages`, rather than
falling through to OpenAI Chat Completions.

After a provider is enabled, Model settings can call its authenticated model
listing endpoint. OpenAI-compatible, Anthropic, Google, Cohere, Mistral, and
xAI shapes are normalized into the shared catalog. Pagination is bounded to 20
pages and response bodies to 8 MiB. A successful API list is matched against
the public `https://models.dev/api.json` catalog by provider ID, model ID,
slug, aliases, vendor-qualified ID, and canonical model family. The same
resolver enriches ChatGPT and Grok subscription catalogs with models.dev names,
descriptions, token limits, input/output modalities, tool use, structured
output, and advertised reasoning-effort values. The picker displays the
models.dev name while requests retain the provider's actual model ID. API keys
are sent only to the configured provider endpoint and never to models.dev.

## Request mapping

The adapter converts Venat system/developer/user/assistant/tool messages,
structured tool schemas, stop sequences, output limits, response schemas,
reasoning effort, parallel-tool preference, provider state, and image
attachments into llmux requests. Attachment bytes pass through the existing
trusted-root, symlink, MIME, count, and size validation before they reach the
SDK.

Only `run_id`, `session_id`, and `agent_id` metadata cross the provider
boundary. Credentials are injected when the driver is constructed and are not
placed in message metadata or events.

## Stream mapping

```text
llmux response metadata -> provider request ID
llmux text delta        -> unphased text delta, classified when the turn ends or starts a tool
llmux reasoning delta   -> thinking delta
llmux tool call         -> Venat structured tool call
llmux finish            -> usage + stop reason + provider state
llmux error             -> typed Venat provider error
```

Tool-call finishes take precedence over a generic stop reason. Usage retains
input, cached input, cache write, reasoning, output, and total token fields when
the upstream protocol reports them. Encrypted or opaque provider continuation
state is returned to the runtime without exposing it as visible text.

Unlike OpenAI Responses, Anthropic Messages and the other llmux transports do
not label streamed text as commentary or final output. Azem keeps that text
streaming provisionally: if the provider starts a tool, the preceding text is
settled and persisted as commentary inside the process trail; if the turn ends
naturally, it remains the final assistant answer. This preserves the same
timeline hierarchy without pretending the wire protocol supplied a phase.

## Retry ownership

llmux's internal retry policy is set to one attempt. Azem uses Venat's
`OpenRetryingStream` and run retry policy as the single retry owner. This keeps
retry observation, delay caps, cancellation, and the rule against replay after
visible output consistent across transports.

Authentication, permission, invalid request, not found, rate limit, server,
and stream errors map to Venat's typed error categories. Cancellation and
deadlines terminate as aborted runs rather than retryable provider failures.

## Verification

Run the adapter, shared request, app runtime, desktop projection, and frontend
checks before release:

```bash
GOWORK=off go test ./internal/provider/llmux ./internal/provider/responses ./internal/app ./internal/desktop
cd frontend && bun run typecheck && bun run test && bun run build
```
