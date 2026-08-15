# Provider Streaming

Last verified: 2026-08-15

Azem normalizes every provider into Venat's `provider.Driver` contract. The
application runtime owns provider/model selection, retries, usage persistence,
tool execution, and UI events; transport packages own wire requests and stream
parsing.

## Transports

| Provider ID | Transport |
|---|---|
| `chatgpt` | Existing Codex Responses subscription driver |
| `grok` | Existing xAI API or CLI-proxy subscription driver |
| llmux profile IDs | `internal/provider/llmux`, backed by llmux v0.2.4 |

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
trusted-root, symlink, regular-file, and detected-MIME validation before they
reach the SDK. Azem does not impose a shared image-count or per-image byte cap;
the selected provider remains authoritative for its request limits.

Anthropic Messages exposes one top-level system field but no mid-conversation
system role. The adapter hoists only the leading system messages into that
field. Later trusted host context remains at its original message-tail position
as a marked user message. This preserves the exact long-conversation prefix
required by DeepSeek's automatic context cache instead of rewriting the prefix
on every turn.

Automatic approval does not assume that every provider protocol implements
native response schemas. Its system policy carries the exact JSON decision
contract in addition to `ResponseFormat`. The decoder accepts a raw decision
object or a whole-response `json` fence, then rejects unknown fields, invalid
enums, empty rationale, trailing prose, nested fences, and tool calls. A parse
failure remains fail-closed and the protected action is not executed.

The selected model's advertised input modalities are authoritative. If a model
explicitly accepts text but not images, historical image parts are replaced by
a stable omission notice so a user can continue the session after switching
models. For an image on the current user turn, the runtime first uses the
independently configured `agents.vision` route to extract bounded textual
evidence, removes image parts from the text-only main request, and injects the
description as a private user-evidence message. The original user attachment
remains durable session data. A missing, unavailable, or explicitly text-only
vision route fails with an actionable error before the main provider call.
Models whose modalities are unknown keep the compatibility behavior instead of
being guessed text-only, while image-capable main models retain the native
direct-image path.

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
the upstream protocol reports them. DeepSeek's Anthropic-compatible usage
reports uncached input and cache-read input as separate counters; the adapter
normalizes them into Azem's inclusive input total and treats a reported zero as
a real zero-percent hit rather than an unsupported metric. Encrypted or opaque
provider continuation state is returned to the runtime without exposing it as
visible text.

## UI projection backpressure

Provider execution and UI delivery are separate reliability domains. Text,
reasoning, and tool-progress deltas are replaceable projections: the event
broker batches them by session, run, agent, tool, and text phase. Independent
interleaved streams therefore do not defeat coalescing.

When the projection queue reaches its byte or event high-water mark, Azem drops
only replaceable queued deltas and emits `projection_resync`. Desktop and TUI
consumers reload the durable session through the read-only `refresh_session`
action. If the dropped stream belonged to a subagent, the resync carries that
`agentId` and the desktop also re-runs `inspect_agent` for the open drawer so
child thinking is not lost when only the parent transcript is refreshed. If compaction happened during a run, the broker emits a final resync
after the terminal event so completed or failed persisted blocks replace any
partial display. Approval requests, tool lifecycle transitions, and terminal
events are not discarded. A slow or suspended renderer must never surface as a
provider error.

Interactive planning uses the same lossless lifecycle channel. Question
requested/resolved and plan proposed/resolved events are durable state
transitions, not replaceable text deltas, so UI backpressure cannot silently
drop a pending choice or an Execute Plan decision. Session refresh reconstructs
their question and plan blocks when a renderer reconnects.

Tool results remain complete in the durable tool record or Artifact V2 store.
The live event carries at most the existing 16 KiB content preview and omits
structured payloads larger than the 64 KiB inline limit. Streaming assistant
and commentary text is parsed as Markdown on every coalesced UI frame. Only the
latest eight provider ranges receive a bounded fade/blur reveal; older ranges
settle into ordinary text without growing the timeline DOM indefinitely.

Before every individual tool call or related parallel batch, the executable
main prompt emits commentary as ordinary user-visible prose: one or two short
sentences, not a titled card or a two-line `**title**` / detail pair. A related
parallel batch shares one update. If a provider emits a tool without the
required commentary, the host persists and projects one fallback sentence
before the first tool event, marked `data.synthetic=tool_announcement`; later
tools in the same batch do not duplicate it. That canned host line is a
grouping anchor only and is not user-visible transcript prose. Real model
commentary still renders as ordinary Markdown. The desktop keeps adjacent
reasoning, tools, and diffs underneath the announcement and does not wrap it
in a duration chip. Older two-line commentary still groups the same way and
is shown as Markdown text.

Session recap generation uses its own `agents.recap` model route and usage kind.
It no longer borrows `agents.compaction`, so choosing a cheap short-text model
for the Inspector summary cannot change the semantic context writer. The
result remains bounded and durable, and `recap_state` projects the saved
revision without mixing this private continuity data into assistant output.

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
deadlines from the run's caller terminate as aborted runs rather than retryable
provider failures. A response-header timeout or transport cancellation while
the caller context is still healthy is a retryable stream-open failure; this
distinction prevents one transient 30-second connection stall from terminating
a long-running main or subagent run.

## Stable error taxonomy

`internal/provider/errcode` classifies every terminal provider failure into a
stable, machine-readable code: `auth`, `quota`, `rate_limit`,
`context_overflow`, `empty_response`, `invalid_request`, `server`,
`transport`, `cancelled`, or `unknown`. Typed errors (the shared
`responses.APIError` and Venat's provider error kinds) win over transport
heuristics, and anything unrecognized classifies as `unknown` rather than a
guess.

The runtime attaches the code to the event payload as `Data["errorCode"]` on
`run_failed` (main and team runs) and on `provider_retry` waiting events when
a retry cause is known. Consumers use the code for presentation only — the
desktop titles the failure block from the code and the block keeps the
original error text — while Venat remains the single retry owner;
`errcode.Retryable` is UI guidance, never a runtime retry decision.

## Verification

Run the adapter, shared request, app runtime, desktop projection, and frontend
checks before release:

```bash
GOWORK=off go test ./internal/provider/llmux ./internal/provider/responses ./internal/app ./internal/desktop
cd frontend && bun run typecheck && bun run test && bun run build
```
