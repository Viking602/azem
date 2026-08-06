# Security

Last verified: 2026-08-06

Azem is a local development agent. Its approvals, typed Bridge, credential
stores, and durable action ledger are governance boundaries, not an operating-
system sandbox. Run it under an OS identity, container, or VM whose access
matches the work you intend to authorize.

## Trust boundaries

- The TUI and React desktop UI request operations; `internal/app` validates
  them and owns durable state.
- The Wails Bridge exposes a closed action allowlist. It does not expose an
  arbitrary shell or filesystem method.
- Built-in tools, MCP tools, hooks, and GitHub operations are separate external
  boundaries and remain subject to their own validation and approval policy.
- Model output, repository text, PR content, tool output, and remote responses
  are untrusted data, not authority to bypass policy.

## Files, shell, and network

`allow_write: false` removes built-in write tools but cannot constrain an
approved shell process. `shell_policy` controls shell approval and
`allow_network` depends on tool declarations; neither is OS isolation. Shell
commands inherit the Azem process identity and may reach paths outside the
workspace. Use external sandboxing when that is unacceptable.

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
are never included.

## MCP, Skills, and hooks

Trust project Skills only for repositories you trust. MCP servers can execute
their advertised operations and may receive conversation context. Configure
the smallest required environment and headers. Hooks execute local commands at
declared lifecycle points; keep hook sources trusted, bounded by timeout, and
reviewed like code.

## GitHub operations and repair sessions

GitHub operations run `git` and `gh` with argv rather than assembled shell
commands. Inputs are validated at the client boundary. Merge-like mutations
respect repository permissions and merge methods and pin the displayed head
OID. Monitor-and-fix deduplicates failure fingerprints and starts an isolated
repair session; it does not grant that session authority to merge.

## Operational guidance

- Use prompt or auto-review approval for normal work; YOLO removes important
  confirmation points.
- Use separate least-privilege provider and GitHub credentials where possible.
- Do not place secrets in prompts, repository files, MCP configuration values,
  hook output, or `config.yaml`.
- Review external mutations and retained credentials before sharing a machine
  or workspace.
- Treat filesystem permissions and backups as part of the security boundary.
