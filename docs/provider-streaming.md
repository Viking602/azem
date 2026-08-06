# Provider Streaming

Last verified: 2026-08-06

Azem normalizes every provider into Venat's `provider.Driver` contract. The
application runtime owns provider/model selection, retries, usage persistence,
tool execution, and UI events; transport packages own wire requests and stream
parsing.

## Transports

| Provider ID | Transport |
|---|---|
| `chatgpt` | Existing Codex Responses subscription driver |
| `grok` | Existing xAI API or CLI-proxy subscription driver |
| llmux profile IDs | `internal/provider/llmux`, backed by llmux v0.2.0 |

The llmux adapter supports its native OpenAI, Anthropic, Google, Mistral,
Cohere, and xAI providers plus its OpenAI-compatible registry. ChatGPT and Grok
IDs remain reserved so an existing subscription configuration cannot silently
change authentication or protocol.

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
llmux text delta        -> final_answer text delta
llmux reasoning delta   -> thinking delta
llmux tool call         -> Venat structured tool call
llmux finish            -> usage + stop reason + provider state
llmux error             -> typed Venat provider error
```

Tool-call finishes take precedence over a generic stop reason. Usage retains
input, cached input, cache write, reasoning, output, and total token fields when
the upstream protocol reports them. Encrypted or opaque provider continuation
state is returned to the runtime without exposing it as visible text.

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
