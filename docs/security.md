# Security

Last verified: 2026-08-10

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
protection.

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
only the immutable tool snapshot acquired when that turn began.

Codex plugin installation and plugin-hook trust are separate decisions. Azem
validates every manifest path against the plugin root, imports only plugins
reported as installed and enabled by Codex, and never treats plugin Hooks as
trusted by default. Literal plugin MCP environment values remain runtime-only;
they are not serialized into Azem configuration or desktop events. Remote MCP
descriptors without an explicit bearer-token environment variable remain
disabled until Azem has an authenticated connection, and `.app.json` metadata
does not grant access to a ChatGPT connector by itself.

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

## Operational guidance

- Use prompt or auto-review approval for normal work; YOLO removes important
  confirmation points.
- Configure the auto-review route with a model you trust. The reviewer receives
  only bounded approval evidence, has no tools, must return the strict decision
  schema, and fails closed when its provider is unavailable or its output is invalid.
  For protocols without native JSON-schema enforcement, Azem accepts only raw
  JSON or one whole-response JSON fence; it never extracts a decision from
  surrounding prose.
- Use separate least-privilege provider and GitHub credentials where possible.
- Do not place secrets in prompts, repository files, MCP configuration values,
  hook output, or `config.yaml`.
- Review external mutations and retained credentials before sharing a machine
  or workspace.
- Treat filesystem permissions and backups as part of the security boundary.
