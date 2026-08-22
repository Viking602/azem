# Security

Last verified: 2026-08-17

Azem is a local development agent. Its approvals, typed Bridge, credential
stores, and durable action ledger are governance boundaries, not an operating-
system sandbox. Run it under an OS identity, container, or VM whose access
matches the work you intend to authorize.

## Trust boundaries

- The TUI and React desktop UI request operations; `internal/app` validates
  them and owns durable state.
- The Wails Bridge exposes a closed action allowlist. Its workspace viewer has
  only bounded read methods; it does not expose an arbitrary shell, write, or
  unrestricted filesystem method.
- The embedded desktop terminal is a separate human-only Bridge surface
  (`CreateTerminal`, `WriteTerminal`, `ResizeTerminal`, `CloseTerminal`,
  `ListTerminals`). Those methods are not `ActionKind` values, are not on the
  Execute allowlist, and are not agent tools. The model cannot inject
  keystrokes into this PTY. Spawn uses a process argv for the user shell;
  typed input is written as bytes to the PTY. The initial `cwd` is the
  window's project workspace; the user may `cd` afterwards. Closing the
  window reaps every PTY. This does not replace `coding.shell` approvals.
- Global session search is a separate read-only Bridge method. It accepts at
  most 200 characters and returns at most 30 title/message matches. Message
  content remains in SQLite; the WebView receives only a short FTS snippet,
  session/project identity, and the stable block sequence needed to navigate.
  Cross-project launch arguments contain the sequence but never the search
  query or matched conversation text.
- The typed session-resume Bridge call clears only that session's unread state,
  updates the current workspace-session pointer, and returns the same bounded
  durable projection already used by the event stream. It does not add a new
  filesystem, shell, provider, or external-network capability.
- Built-in tools, MCP tools, hooks, and GitHub operations are separate external
  boundaries and remain subject to their own validation and approval policy.
- Model output, repository text, PR content, tool output, and remote responses
  are untrusted data, not authority to bypass policy.
- Planning questions and plan proposals are untrusted model output. Only the
  typed `resolve_user_input` and `resolve_plan` actions may change their durable
  state, and only an explicit `execute` decision starts implementation.

## Files, shell, and network

`allow_write: false` removes built-in write tools but cannot constrain an
approved shell process. `shell_policy` controls shell approval and
`allow_network` depends on tool declarations; neither is OS isolation. Shell
commands inherit the Azem process identity and may reach paths outside the
workspace. Use external sandboxing when that is unacceptable.

`coding.shell` accepts foreground commands only. Descriptor/pipeline syntax is
not confused with a background operator, but real POSIX `&` and normalized
detachment primitives are rejected because a new session can escape
process-group cleanup. Wall-clock and inactivity limits bound the owned group;
they are not a descendant sandbox.

`coding.delete_file` is a separate governed write. On supported Unix systems it
opens each parent directory relative to the workspace root with no-follow
semantics, verifies the leaf without following symlinks, and unlinks by
directory descriptor. A repository symlink therefore cannot redirect deletion
outside the workspace. Platforms without an equivalent implementation reject
the tool rather than fall back to lexical path checks.

Azem provider and HTTP integration traffic follows matching process proxy
environment variables and, on macOS, the active SystemConfiguration HTTP,
HTTPS, or SOCKS proxy plus its bypass list. Selecting a system proxy therefore
places that proxy on the encrypted transport path and subjects destination
metadata to the proxy operator's trust boundary. Azem does not persist proxy
endpoints or credentials and does not include them in events. Agent shell
commands remain separate child processes: they inherit environment variables
but Azem does not rewrite their networking from the desktop system proxy.

The desktop workspace viewer resolves every requested relative path and every
symlink against the active workspace before reading. Absolute paths, parent
escapes, NUL bytes, and links resolving outside the workspace are rejected.
Directory listings and previews have fixed limits; binary files are not copied
into the WebView, and supported raster images have a separate 8 MiB cap. This
boundary is read-only and does not replace approval for agent file tools.

The image-assistance route reuses the same trusted-root, symlink, regular-file,
and detected-MIME validation as the main provider path. It sends the image only
to the explicitly configured `agents.vision` provider. Its bounded textual result is
labelled as untrusted visual evidence and enters the main model as a private
user-evidence message, never as trusted system or hook instructions. Selecting
this route therefore authorizes image bytes to cross that provider boundary;
API credentials remain isolated to each provider driver.

Cursor's bidirectional AgentService stream is an additional remote-control
boundary, not a bypass around Azem tools. Native exec requests bind to the
current main, Team-role, or subagent governed tool bus and retain ordinary
approval, durable timeline, and file-observation rules. Conversation
checkpoints and server-set blobs are isolated by account, size-bounded,
content-addressed, and validated before replacing known-good state. Cursor
image parts use the same trusted attachment loader described above. The
provider does not expose cache-read counters; Azem records that field as
unreported rather than inferring a hit or miss.

The authenticated Cursor `GetUsableModels` response is authoritative. HTTP,
authentication, protobuf/decode, and empty-catalog failures remain errors; only
the last successfully authenticated cache may be shown explicitly as stale.
Static bundled rows are never substituted into an authenticated account
catalog.

Local composer and transcript thumbnails use the focused `AttachmentDataURL`
Bridge method. It accepts the complete attachment record, validates that its
resolved path belongs to the requested session's durable attachment directory,
reapplies supported-image content detection, and only then returns a local
data URL to the WebView. It is not an arbitrary path reader and does not send
preview bytes over the network.

Workspace change review is also read-only. It runs fixed Git subcommands with
argv, a deadline, bounded stdout/stderr, external diffs disabled, and the pager
disabled. A patch can be requested only for a relative path currently returned
by `git status`; the Bridge never accepts an arbitrary revision, path outside
the workspace, shell fragment, staging operation, restore, commit, or push.

## Credentials

OAuth tokens and API keys use the configured `internal/auth` credential store:
system keyring, SQLite, or a permission-restricted file. SQLite and file stores
do not add application-level encryption; prefer the keyring for stronger local
protection. Large conversation payloads live beside that store as
mode-`0600` files under `~/.azem/blobs`; they are content-addressed
bytes, not a second credential store.

llmux API keys are write-only in Model settings. The UI submits a new key in a
single typed action; `config.yaml`, runtime events, logs, model metadata, and
frontend state contain only provider identity and credential availability.
Environment variables named by llmux profiles are supported and are inherited
by the Azem process and any child process allowed to receive the environment.

Provider and model selectors request public SVG logos from
`https://models.dev/logos/{provider}.svg`. Requests contain only the provider
identifier; credentials, model IDs, workspace data, and conversation content
are never included. llmux IDs are normalized to the models.dev hyphen form;
providers whose public catalog key differs by more than punctuation use an
explicit models.dev alias, and missing assets fall back to a local generic icon.

Model discovery sends the API key only to the validated HTTPS provider base URL
(plain HTTP remains limited to loopback). Redirects are not followed. The
separate public request to `https://models.dev/api.json` carries no provider
credential and is used only to enrich model capabilities and resolve the
models.dev provider logo ID.

「Fetch from API」/「从 API 获取」 is a read-only credential use: discovered
models are projected transiently to the current Settings UI. The pending key,
catalog rows, YAML, and runtime provider state are persisted only after the
explicit Save provider action. Pending secrets are never placed in events.

## MCP, Skills, and hooks

Trust project Skills only for repositories you trust. MCP servers can execute
their advertised operations and may receive conversation context. Configure
the smallest required environment and headers. Hooks execute local commands at
declared lifecycle points; keep hook sources trusted, bounded by timeout, and
reviewed like code.

The desktop MCP editor persists only validated configuration. Server names are
restricted, remote URLs require HTTPS except on loopback, and environment or
header credentials must be `env:` or `keyring:` references. Runtime snapshots
include connection metadata and imported tool descriptions but omit configured
environment maps, headers, resolved credentials, and plugin-scoped literal
values. Disabling a server closes its live client; already-running turns retain
only the immutable tool snapshot acquired when that turn began. Deleting a
user-owned server requires explicit confirmation and removes only its validated
local configuration and runtime connection. Built-in and plugin-contributed
servers cannot be deleted through this action, preventing an apparent deletion
that would silently reappear from its owning catalog on restart.

Plugin installation and plugin-hook trust are separate decisions. Azem loads
only from its permission-restricted `plugin-packages` data directory. A plugin
may be installed directly there; Codex integration uses Codex paths only as
copy sources, validates the staged copy, and then activates an Azem-owned
package. The runtime never executes a plugin from a Codex source or cache path.
Azem validates every manifest path against the active copied root and never
treats plugin Hooks as trusted by default. Literal plugin MCP environment values remain runtime-only;
they are not serialized into Azem configuration or desktop events. Remote MCP
descriptors without an explicit bearer-token environment variable remain
disabled until Azem has an authenticated connection, and `.app.json` metadata
does not grant access to a ChatGPT connector by itself.

## Offline learning, generated tools, and adapters

Trajectory export and all replay, noise, synthesis, training, and route
evaluation packages are offline consumers. Their records carry evidence and
digests but grant no tool, admission, retry, approval, or mutation authority.
Training targets are admitted only when validator-backed at the same authority
and work revision; held-out release reports fail on task/project/content
contamination, synthetic-only gains, safety or retention regression, false-pass
increase, or incomplete cost accounting.

Generated coding tools remain outside the production registry.
`internal/toollab` accepts only a closed JSON input schema and historical-gap
evidence, rejects network and process permissions, and runs fuzz, static
analysis, permission audit, and behavior checks in a pinned Docker image with
no network, read-only mounts, dropped capabilities, `no-new-privileges`, and
bounded CPU, memory, PIDs, output, and time. A passing report is still marked
non-installable. Promotion requires a separate, evidence-bearing human review;
candidate and report digests make later mutation fail closed.

Adapter deployment accepts only a digest-bound passing held-out report, exact
base-model and tokenizer identities, train-data lineage, a same-provider
serving model, and a rollback target. The target is validated by the existing
provider route resolver. The registry performs one exact base-route
substitution; it does not bypass account, model-catalog, credential, or
approval checks and does not add a second router. The kill switch restores the
named prior artifact or the base route.

## GitHub operations and repair sessions

GitHub operations run `git` and `gh` with argv rather than assembled shell
commands. Inputs are validated at the client boundary. Merge-like mutations
respect repository permissions and merge methods and pin the displayed head
OID. Monitor-and-fix deduplicates failure fingerprints and starts an isolated
repair session; it does not grant that session authority to merge.

## Approved-plan handoff

Planning mode exposes read-only tools plus the bounded `ask` and `submit_plan`
tools. A submitted proposal cannot grant itself write, shell, network, MCP, or
approval authority. The desktop Bridge accepts structured question answers and
an explicit plan decision only for the active session; the application service
validates IDs and state transitions against durable blocks and artifacts.

When the user executes a plan, Azem starts a separate ordinary turn. The
approved `plan_v1` artifact is loaded by the backend and inserted through a
private trusted-context field, rather than copied from editable UI text. Normal
tool approval, filesystem, shell, MCP, hook, credential, and provider policies
still apply to that implementation turn.

## Embedded desktop terminal

The bottom terminal panel is an interactive login shell owned by the desktop
process, not a second Azem-owned shell-tool executor.

- Only the human in that window can write to the PTY, through the typed
  Bridge methods above. There is no `write_terminal` tool and no Venat route
  for these keystrokes.
- Spawn is confined to the window workspace at start. Later `cd` is expected
  for a real terminal and is not re-validated by Azem.
- The renderer receives only session identity, title, spawn cwd, shell name,
  grid size, exit code, and bounded base64 output. It does not receive the
  environment, PTY device path, or process credentials.
- Agent file, shell, network, MCP, hook, and approval policy still apply to
  model-driven tools. Using the embedded terminal does not bypass TOOL-001.

## Operational guidance

- Use prompt or auto-review approval for normal work; YOLO removes important
  confirmation points.
- Configure the auto-review route with a model you trust. The reviewer receives
  only bounded approval evidence, has no tools, must return the strict decision
  schema, and fails closed when its provider is unavailable or its output is invalid.
  The host then applies the Codex guardian matrix: `low` and `medium` risk
  allow without a person, `high` allows when the current user turn authorizes
  the action (for example “帮我提交并推送” for `git push`), and `critical` denies.
  Routine `coding.shell` commands such as `go test` are classified low and skip
  the model. For protocols without native JSON-schema enforcement, Azem accepts
  only raw JSON or one whole-response JSON fence; it never extracts a decision
  from surrounding prose.
- Use separate least-privilege provider and GitHub credentials where possible.
- Do not place secrets in prompts, repository files, MCP configuration values,
  hook output, or `config.yaml`.
- Review external mutations and retained credentials before sharing a machine
  or workspace.
- Treat filesystem permissions and backups as part of the security boundary.
