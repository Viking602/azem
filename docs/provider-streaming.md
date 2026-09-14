# Provider Streaming

Last verified: 2026-09-07

Azem normalizes every provider into Venat v0.16.1's `provider.Driver`
contract. The application runtime owns provider/account/model selection,
pre-stream retry, approval policy, usage persistence, tools, and UI events;
transport packages own wire requests and parsing. A direct `agent.Engine`
consumes the stream, while `durable.Runtime` settles provider/tool effects.

## Transports

| Provider ID | Transport |
|---|---|
| `chatgpt` | Existing Codex Responses subscription driver |
| `grok` | Existing xAI API or CLI-proxy subscription driver |
| `cursor` | Native `api2.cursor.sh` Connect protobuf agent driver |
| `devin` | Personal Devin CLI browser Auth and Codeium Cascade Connect protobuf driver |
| llmux profile IDs | `internal/provider/llmux`, backed by llmux v0.3.2 |

Codex requests send the stable prompt-cache identity as `session-id` and
`thread-id`, matching the [official client's session headers](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/requests/headers.rs); the legacy
`conversation_id`/`session_id` headers retain the same value. Instructions, tool
definitions and prior input items remain unchanged within a turn. Identical
prefixes permit cache reuse but do not guarantee a hit on every request.
Each ChatGPT logical turn also owns an in-memory routing map. When a successful
response first returns `x-codex-turn-state`, subsequent requests and retries
replay that exact value. The scope includes account, endpoint, exact model and
prompt-cache identity. A new main/Team/Sidekick turn starts with an empty map;
tokens are neither persisted nor retained on shared transport drivers. This
follows the [Codex per-turn routing contract](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs).

Devin uses the personal-account CLI flow: PKCE browser authorization at
`https://app.devin.ai/auth/cli/continue`, a state-checked loopback callback on
`127.0.0.1:59653`, and token exchange at `https://api.devin.ai/auth/cli/token`.
The daemon's existing credential store owns the resulting session token;
Settings and TUI never receive it. There is no separate refresh-token grant;
expired credentials require another login.

`GetCliModelConfigs` is the authenticated model inventory. Discovery advertises
the native `chisel` dev-channel identity; chat/auth use `devin-cli` version
`3000.6.2` with `ideType=chisel`. Disabled/internal models are removed, exact
model IDs and account-advertised capabilities are retained, and a successful
fetch replaces the account cache. Empty or failed discovery is an error;
only the shared catalog's explicitly stale last-successful cache may survive.
Effort variants remain exact model rows. Native family/depth/Fast controls resolve
only enabled account IDs, using account labels for opaque legacy IDs and keeping
context sizes and Fusion companions separate. No wire reasoning flag is invented.
Adaptive routers resolve through `AssignModel` before `GetChatMessage`.

The driver preserves ordered private context, images, tool IDs/results,
same-account/model reasoning signatures, phase markers and stable conversation
IDs derived from the existing prompt-cache key. Tool calls finalize once,
only after a successful Connect completion trailer and validated JSON arguments.
Transport version 2 records the updated wire contract in durable execution
profiles. SWE rejects dotted function names and mid-conversation `SYSTEM_PROMPT` rows.
Tool names use the shared collision-safe ASCII mapping; the CLI-native `read`,
`write`, `edit`, and `exec` map back to the existing governed file/shell tools.
`edit` translates one unique `old_string`/`new_string` pair; `exec` supports a
supervised command and optional absolute `workdir`. Persistent shell IDs,
interactive/background options and replace-all are not advertised by this
native subset. Existing extensions win name collisions, and incompatible
older batch/async history retains its generic tool schema. Native arguments
are retained in account/model-scoped provider state for exact history replay;
approval, execution and durable records still use canonical Azem calls.
Late host context uses a labeled USER envelope at the same message position,
as in the Anthropic adapter; it is never hoisted into the cached system prefix
or selected as the Adaptive router's active user action.
Truncation, errors and incomplete tools fail explicitly. Transport/decompressed
frames and requests are bounded to 16 MiB, decoded protobuf fields to 65,536,
and tool calls to 1,024. Auth redirects cannot forward credentials to arbitrary
hosts. Input usage includes cache reads/writes; absent stream usage remains
unreported. Account quota comes from the read-only
`/exa.seat_management_pb.SeatManagementService/GetUserStatus` RPC using the same
CLI session metadata, bounded unary transport, and shared asynchronous Settings
refresh. Daily/weekly percentages, reset timestamps and any reported overage
balance are projected without exposing the session. Structured response format
is unsupported. The endpoint and quota field names were verified against the
[official CLI release](https://static.devin.ai/cli/current/manifest.json).

Wire references: [devin-gateway login](https://github.com/CaiJingLong/devin-gateway)
and [Oh My Pi Devin protocol](https://github.com/can1357/oh-my-pi/blob/main/packages/catalog/src/discovery/devin-proto.ts).
The [devin-opencode](https://github.com/karthiknish/devin-opencode) plugin
exposes cloud-session tools with API keys; it does not supply personal Auth
or a model transport.

Received HTTP/Connect failures retain bounded, credential-redacted details.
Non-retryable rejections settle immediately. Retryable response failures settle
after the existing retry budget; both are failed model attempts, rather than
unknown outcomes left running. Lost transport without a failure response still
requires reconciliation and must not be replayed automatically.

Cursor still advertises Azem tools as MCP definitions. Composer also emits
built-in execs (`read`/`shell`/`write`/`delete`/`grep`/`ls` and `pi_*`
aliases) on the open `AgentService/Run` stream. The driver answers
`request_context` on that same stream and maps those execs onto the
global coding tools: `coding.read_file`, `coding.shell`,
`coding.write_file`, `coding.search`, `coding.list_files`,
`coding.glob`, `coding.replace`, and `coding.delete_file`. Existing-file
writes are rewritten to `coding.edit_hashline`. Hashline remains the
default edit path. Results are typed `ExecClientMessage`s plus
`stream_close`. Native execs use the same approval-aware governed drivers as
ordinary Venat tools, persist start/result records and file observations, and
project live tool events. Resolved native calls are also carried in Cursor
provider state so a restart rebuilds the exact call/result pairs. Server-owned
Todo completion snapshots replace the durable Cursor phase. Unmapped execs
(`fetch`, `diagnostics`, and others) return a typed reject or throw and never
become Venat `EventToolCall`s.
Cursor binds `conversation_id` to the existing logical prompt-cache key:
the main session ID, one key per Team role, one key per subagent run, and
operation-scoped keys for title, recap, and vision requests. Local checkpoint
and blob state is additionally scoped by Cursor account. A runtime-owned
conversation cache retains that state across short-lived provider-driver
instances. Each request rebuilds `root_prompt_messages_json` and `turns` from
canonical Venat history while preserving checkpoint-owned todos, file state,
summaries, and other non-history fields when the leading system prompt is
unchanged.

Only the active non-private user message becomes `user_message_action`.
Private hook, historical, vision, Todo, and deadline tail messages keep their
relative order in root immediately before that action. The same normalization
is replayed when the turn becomes history, so appending the next turn does not
rewrite the prior wire prefix. A trailing assistant or tool result uses
`resume_action`. The KV bridge handles both `get_blob` and `set_blob`.
Image-capable Cursor routes receive validated text-plus-image or image-only
messages through the shared trusted attachment loader. Kimi K3 reasoning is
replayed only when the source provider state identifies the same Cursor model.
Connect frames, protobuf lengths, server-set blobs, decoded field counts,
aggregate decoded bytes, and nested protobuf Value depth are bounded before
state is committed or provider-controlled values become Go maps/slices. A
per-conversation `resource_exhausted` rotates the wire ID once while retaining
the validated checkpoint.
Cursor token deltas count as output, while checkpoint `used_tokens` feed
context-pressure decisions without being recorded as billable input. Cursor
controls prompt caching automatically but does not report cache-read tokens,
so the Inspector shows **Not reported**, never a fabricated 0% hit rate.
Changing the advertised coding tools or the Cursor wire version resets the
Cursor cache identity once; later turns extend the stable root prefix.

The llmux adapter supports its native OpenAI, Anthropic, Google, Mistral,
Cohere, and xAI providers plus its OpenAI-compatible registry. ChatGPT, Grok, Cursor, and Devin
IDs remain reserved so an existing subscription configuration cannot silently
change authentication or protocol.

Those reserved subscription transports still appear in desktop Model settings
as login cards. Their actions call ChatGPT browser OAuth, Grok device authorization,
Cursor's `loginDeepControl` poll, or Devin CLI browser Auth; successful login refreshes the authenticated model
catalog, while logout removes the active account projection.

The desktop catalog loads provider profiles in 24-item batches as its directory
scrolls, while search still matches the complete catalog. Protocol selection
comes from the llmux compatibility profile: an `anthropic-messages` profile is
constructed with llmux's Anthropic driver and calls `/v1/messages`, rather than
falling through to OpenAI Chat Completions.

After a provider is enabled, Model settings can call its authenticated model
listing endpoint. OpenAI-compatible, Anthropic, Google, Cohere, Mistral, and
xAI shapes are normalized into the shared catalog. Cursor lists models through
`GetUsableModels` rather than REST `/v1/models`. Pagination is bounded to 20
pages and response bodies to 8 MiB. A successful API list is matched against
the public `https://models.dev/api.json` catalog by provider ID, model ID,
slug, aliases, vendor-qualified ID, and canonical model family. The same
resolver enriches ChatGPT and Grok subscription catalogs with models.dev names,
descriptions, token limits, input/output modalities, tool use, structured
output, and advertised reasoning-effort values. The picker displays the
models.dev name while requests retain the provider's actual model ID. API keys
are sent only to the configured provider endpoint and never to models.dev.

Grok OAuth treats the combined successful `/v1/models` and
`/v1/language-models` account response as the complete availability catalog.
Azem normalizes only returned chat-capable rows and removes image, speech, and
voice-only IDs from the chat picker. A successful refresh transaction replaces
the persisted account catalog, so a model omitted by the API immediately leaves
Settings, pickers, and route validation. No curated/static Grok rows are
injected. A failed refresh may surface only the last successful account cache,
marked stale. Provider-specific reasoning policy is applied both when fresh rows
are saved and when SQLite rows are loaded, so a restart does not temporarily
remove an advertised model's effort control while a refresh is pending.

`GetUsableModels` does not publish a context-window field. Cursor metadata
therefore follows explicit protocol signals: a `1M` display label, native Kimi
K3 or GLM 5.2+ identity, or Claude/Gemini `max_mode` raises the effective window
to 1,000,000 tokens; other unknown rows stay at 200,000.
Azem preserves Cursor's returned `max_mode` bit and writes it to both
`ModelDetails.max_mode` and `RequestedModel.max_mode`. Model IDs that encode
`none`/`low`/`medium`/`high`/`xhigh`/`max` remain the wire source of truth.
Desktop composer and route pickers collapse every tier/Thinking/Fast sibling
into one base-model row. Thinking is implicit: when a family exposes a
same-tier Thinking variant, selection, tier changes, and Fast changes use that
raw ID by default. Families without Thinking variants continue to use standard
IDs. The UI exposes only reasoning depth and optional Fast controls; there is
no separate Thinking label or toggle. Provider settings list one family row.
Availability remains an atomic write of that family's raw IDs.

The family row reports how many enabled raw variants it contains and keeps
every raw ID as a search alias, so folding does not make inventory invisible.
The authenticated account's `GetUsableModels` response is authoritative.
Azem does not inject legacy static fallback rows that the account endpoint
omitted because selecting one could fail at request time.
Authentication, network, protobuf/decode, and empty-response failures are not
converted into bundled rows. The catalog service may retain the last successful
account-scoped result and surface it explicitly as stale; a failed refresh
never becomes fresh model authority.

Cursor marks retention exceptions with `(NO ZDR)`. Azem removes that acronym
from the primary model name and shows a localized retention warning instead.
It means the model does not have a zero-data-retention guarantee; inputs and
outputs may be retained under Cursor or the upstream provider's policy. Cursor
documents Claude Fable 5 specifically as a retained-data model used for
automated and human harm-prevention review:
<https://prod.cursor.com/docs/enterprise/privacy-and-data-governance#models-with-data-retention>.

## Request mapping

The shared Codex/Grok Responses adapter preserves native tool-result images
inside their original `function_call_output`, together with ordered text parts
and the original call ID. Image bytes become data URLs using detected PNG,
JPEG, GIF, or WebP MIME types; missing or unsupported image data fails explicitly.
Text-only results retain their existing string representation and structured
fallback. `inspect_image`, image reads, and browser screenshots must not become
empty strings or metadata-only results. `TestInspectImageReachesResponsesProvider`
checks the native tool through the durable runtime to the HTTP request.

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

## Stream and durable-effect mapping

```text
llmux response metadata -> provider request ID
llmux text delta        -> agent text frame; Azem settles commentary/final phase
llmux reasoning delta   -> agent thinking frame
llmux tool call         -> structured agent tool call with stable operation ID
llmux finish            -> usage + stop reason + provider state
llmux error             -> typed provider error
```

Before opening a provider stream, Venat durable persists the continuation and a
versioned model attempt. Before each tool, it persists the model-complete
continuation; Azem's `BeforeToolCall` approval hook may suspend at that safe
boundary before the tool attempt starts. The durable interceptor then settles
model/tool success or failure. A lost response becomes an `unknown` attempt on
the next claim and requires explicit reconciliation—retry never guesses.

The `agent.Sink` is transient and may replay frames after recovery. Azem
deduplicates session blocks, tool timeline rows, usage, and terminal output by
execution/operation identity. Tool-call finishes take precedence over a
generic stop reason. Usage retains input, cached input, cache write, reasoning,
output, and total token fields when the upstream protocol reports them.
DeepSeek's separate uncached/cache-read counters become inclusive input, and a
reported zero remains a real zero rather than unsupported telemetry. Opaque
provider continuation state remains private.

Main and resumed-main final publication is gated at the application boundary.
Venat emits text and `FrameDone` before evaluating output guardrails, so those
frames are candidates, not accepted answers. Explicit `final_answer` text and
unphased terminal text stay private until the engine succeeds and
`CompleteTurn` commits the canonical answer. Publish that accepted answer once;
discard rejected candidates on a guard continuation. Explicit commentary,
thinking, and tools remain live. Unphased text becomes commentary only when a
tool boundary establishes that role. Genuine interrupted provider output may
still be shown as a failed partial, never as an accepted completion.
This changes publication timing, not the static prompt, tool schema, provider
message order, or prefix-cache identity.

Some OpenAI Responses-compatible streams expose one logical tool call first
with a provisional `item_id` such as `fc_tmp_*`, then with the final
`call_id`. llmux v0.2.5 owns this protocol boundary: it correlates output
indexes, item IDs, canonical call IDs, raw/prefixed aliases, and custom-tool
inputs before emitting one executable call. The same release makes duplicate
terminal frames idempotent across Chat Completions, Anthropic, Bedrock, Cohere,
and Google, fails closed on conflicting identity reuse or post-terminal calls,
and bounds SSE frames plus per-stream tool identity state. Azem now forwards
the canonical llmux tool event directly; it does not maintain a second local
deduplication path and never suppresses a legitimate later model turn.

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
Choosing a cheap short-text recap model cannot change the current conversation
or deterministic context archive. The result remains bounded and durable, and
`recap_state` projects the saved revision without mixing this private
continuity data into assistant output.

Unlike OpenAI Responses, Anthropic Messages and the other llmux transports do
not label streamed text as commentary or final output. Azem keeps that text
streaming provisionally: if the provider starts a tool, the preceding text is
settled and persisted as commentary inside the process trail; if the turn ends
naturally, it remains the final assistant answer. This preserves the same
timeline hierarchy without pretending the wire protocol supplied a phase.

## Retry ownership

llmux's internal retry policy is one attempt. Azem's
`retryProviderDriver` is the only pre-stream transport retry owner for main,
Team-role, Subagent, automation, vision, recap, and title requests. It retries
only before visible output/effect settlement, preserves delay/cancellation
events, and never substitutes for durable unknown-attempt reconciliation.

Authentication, permission, invalid request, not found, rate limit, server,
and stream errors retain typed provider categories. Caller cancellation and
deadlines abort rather than retry. A response-header timeout or transport
cancellation while the caller remains healthy may retry only at the
pre-stream boundary.

Confirmed permission rejections (including HTTP 403) become terminal error
events after that retry boundary. Durability records a failed model attempt,
not an unknown outcome requiring reconciliation. Network failures with unknown
outcomes retain their existing non-replay protection. Streaming authentication
leaves the rejection body available to the provider parser, which reads the
bounded code/detail before cancelling its request context. A 403 does not trigger
token refresh or an automatic retry. Grok's `SAFETY_CHECK_TYPE_DATA_LEAKAGE` is a
server content rejection, not evidence of an expired login; preserve that reason
and stop the handoff without executing tools.

Context archiving does not open a provider stream and therefore has no retry,
inactivity-watchdog, or model-usage path. Automatic, manual, rebuild, main,
Team, and subagent maintenance all call the same host archive kernel.

## Stable error taxonomy

`internal/provider/errcode` classifies every terminal provider failure into a
stable, machine-readable code: `auth`, `quota`, `rate_limit`,
`context_overflow`, `empty_response`, `invalid_request`, `server`,
`transport`, `timeout`, `cancelled`, or `unknown`. Typed errors (the shared
`responses.APIError` and Venat's provider error kinds) win over transport
heuristics, and anything unrecognized classifies as `unknown` rather than a
guess. `context.DeadlineExceeded` and Go 1.25 HTTP header timeouts classify
as `timeout`, not `cancelled`; user stop remains `cancelled`. The desktop
titles a timeout and replaces the raw Go sentinel with product copy.

The runtime attaches the code to the event payload as `Data["errorCode"]` on
`run_failed` (main and Team runs) and on `provider_retry` waiting events when a
retry cause is known. Consumers use the code for presentation only—the desktop
titles the failure block from the code and keeps the original error text.
`retryProviderDriver` remains the retry decision owner; `errcode.Retryable`
alone never schedules an attempt.

## Portable provider contract

Azem pins Venat v0.16.1 and llmux v0.3.2. The shared contract preserves
commentary/final text phase, terminal state, distinct length/error stop reasons,
reported usage flags, cache reads/writes, sources, files, warnings, portable
modality metadata, and provider compatibility descriptors. Tool argument
objects are duplicate-key checked before approval or execution.

llmux protocol parsers own provisional/canonical tool identity correlation and
at-most-once finalization. Azem forwards canonical calls and never deduplicates
across streams or model turns. Azem owns pre-stream retries; Venat v0.16
durable execution rejects duplicate tool registrations/invalid arguments and
fences each effect attempt.

Cross-repository release verification uses declared modules:

```bash
(cd ../llmux && GOWORK=off go test ./...)
GOWORK=off go test github.com/Viking602/venat/agent github.com/Viking602/venat/message github.com/Viking602/venat/provider/... github.com/Viking602/venat/tool/... github.com/Viking602/venat/skill/... github.com/Viking602/venat/orchestration github.com/Viking602/venat/durable/...
GOWORK=off go test ./internal/provider/... ./internal/auth/...
```

## Verification

Run the adapter, shared request, app runtime, desktop projection, and GPUI
checks before release:

```bash
GOWORK=off go test ./internal/provider/llmux ./internal/provider/responses ./internal/app ./internal/desktop
make test-gpui
```

### Empty model responses

The shared provider retry boundary rejects a completed turn with no text, reasoning, or finalized tool call before it reaches Venat continuation validation. It uses the existing pre-emission retry budget and never reruns earlier tool calls. With retries disabled or exhausted, it reports an explicit empty_response provider error instead of accepting an empty final answer. EOF before any output is handled the same way. Cancellation, explicit failures, and content filtering retain their terminal classification.
