# Azem

Azem is a local terminal AI coding agent written in Go. It uses the current directory as its workspace and provides streaming conversations, governed file and shell tools, persistent sessions, crash recovery, MCP integrations, Agent Skills, team execution, and subagents.

> [!WARNING]
> Azem can read and modify files, run shell commands, and access the network when allowed by its configuration. Review your workspace and approval policies before running it. Commit or back up important changes first.

## Features

- Keyboard-driven terminal interface built with Bubble Tea
- ChatGPT (Codex-compatible OAuth) and Grok providers
- Streaming text, reasoning status, tool progress, and token usage
- File reading, search, patch editing, formatting, testing, and shell tools
- Prompt, Auto Review, and YOLO approval modes
- SQLite persistence for sessions, run state, and approvals
- Crash recovery and reconciliation of unknown external side effects
- MCP integrations over stdio and Streamable HTTP
- Dynamic Agent Skills discovery, activation, and reload
- Planner, Implementer, Reviewer, and Reporter team mode
- Background, resumable subagents with optional Git worktree isolation

## Requirements

- Go 1.25.8 or later
- The Go 1.25.12 toolchain declared by this project is recommended
- A supported ChatGPT or Grok account or credential
- Git for worktree isolation; the workspace must be inside a Git repository

## Build

```bash
git clone https://github.com/Viking602/azem.git
cd azem
go build -o azem ./cmd/azem
```

Run the test suite:

```bash
go test ./...
```

You can also run Azem directly from source:

```bash
go run ./cmd/azem
```

## Quick Start

Change into the project you want Azem to work on, then start the binary:

```bash
cd /path/to/your/project
/path/to/azem
```

Azem uses the startup directory as its workspace by default. On first launch, sign in to a provider from the interface:

```text
/login chatgpt
/login grok
```

Azem can also import credentials from an existing client installation:

```text
/login chatgpt --import-codex
/login grok --import
```

By default, Azem attempts to import existing credentials from `CODEX_HOME` (or `~/.codex`) and `~/.grok`. ChatGPT login uses Codex-compatible OAuth endpoints. The Grok OAuth-compatible flow is experimental and is not a stable third-party authentication contract provided specifically for Azem.

### Command-Line Options

```text
azem [-config /path/to/config.yaml]
azem -version
```

- `-config`: use a specific YAML configuration file; the default is `azem/config.yaml` in the system user configuration directory
- `-version`: print build version information

## Usage

### Keyboard Shortcuts

| Shortcut | Action |
|---|---|
| `Enter` | Submit input or confirm a selection |
| `Ctrl+J` | Insert a newline in the input box |
| `Esc` | Close a dialog or cancel the active run |
| `Ctrl+C` | Cancel the active run, or quit while idle |
| `Ctrl+P` | Open the command palette |
| `Ctrl+M` | Select a model |
| `Ctrl+R` | Select reasoning effort |
| `Ctrl+B` | View subagents |
| `Shift+Tab` | Cycle the approval mode |
| `PageUp` / `PageDown` | Scroll through conversation history |
| `Ctrl+Home` / `Ctrl+End` | Jump to the beginning or end of the conversation |
| `?` | Open help when the input box is empty |

### Slash Commands

| Command | Description |
|---|---|
| `/models` | Search for and select a model |
| `/provider [chatgpt\|grok]` | Switch providers |
| `/reasoning [level]` | Set reasoning effort |
| `/login [provider]` | Sign in to a provider |
| `/logout [provider]` | Sign out of a provider account |
| `/skills [reload]` | View or reload Agent Skills |
| `/skill <name> [instruction]` | Activate a Skill and run one turn |
| `/team on\|off` | Enable or disable team mode |
| `/agents [cancel <id>]` | View or cancel subagents |
| `/agent-types` | View available subagent types |
| `/personas` | View subagent personas |
| `/new` | Create a new session |
| `/sessions` | List saved sessions |
| `/resume` | Resume a saved session |
| `/compact` | Compact the current session context |
| `/mcp [refresh\|reconnect <server>]` | View or update MCP servers |
| `/reconcile <attempt-id> <result>` | Reconcile an unknown side effect |
| `/cancel` | Cancel the active run |
| `/help` | Open help |
| `/quit` | Quit Azem |

## Configuration

Azem uses built-in defaults when no configuration file exists. Use `-config` to load a custom configuration:

```bash
azem -config ./config.yaml
```

Minimal example:

```yaml
version: 1

defaults:
  provider: chatgpt
  model: gpt-5.6-sol
  reasoning: high
  agent_mode: single

workspace:
  # Relative paths are resolved from the configuration file directory.
  # When omitted, the startup directory is used.
  root: .
  allow_write: true
  shell_policy: prompt       # prompt | deny | allow
  allow_network: prompt      # prompt | deny | allow

auth:
  store: keyring             # sqlite | keyring | file
  import_codex: true
  import_grok: true

agents:
  main:
    # A value of 0 means that no limit is set for this budget.
    max_tokens: 0
    max_tool_calls: 0
  team:
    max_concurrency: 2
    max_ticks: 12
  subagents:
    enabled: true
    max_depth: 1
    max_concurrency: 2
    await_timeout: 10m
    auto_wake: true
    budget:
      max_tokens: 128000
      max_tool_calls: 64
      max_turns: 32
      max_wall_clock: 20m

skills:
  enabled: true
  trust_project: true
  additional_dirs: []
  eager: []
  disabled: []

mcp:
  servers: {}
```

Configuration fields are validated strictly. Unknown fields, invalid enum values, and invalid durations prevent startup.

### MCP Examples

stdio server:

```yaml
mcp:
  servers:
    local_tools:
      enabled: true
      transport: stdio
      command: /path/to/mcp-server
      args: []
      inherit_env: true
      connect_timeout: 30s
      call_timeout: 60s
      max_concurrency: 2
      approval: always
```

Streamable HTTP server:

```yaml
mcp:
  servers:
    remote_tools:
      enabled: true
      transport: streamable_http
      url: https://example.com/mcp
      headers:
        Authorization: env:MCP_AUTHORIZATION
      connect_timeout: 30s
      call_timeout: 60s
      max_concurrency: 2
      approval: always
```

MCP secrets must use references and cannot be embedded directly in the configuration:

- `env:NAME`: read an environment variable
- `keyring:NAME`: read a system keyring entry

Remote MCP URLs must use HTTPS. HTTP is allowed only for localhost or loopback addresses.

## Data Directories

Azem follows operating-system user directory conventions and creates an `azem` subdirectory:

- Configuration: `azem/config.yaml` in the user configuration directory
- Database: `azem/azem.db` in the user data directory
- State files: `azem/` in the user cache or state directory

On Linux, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `XDG_STATE_HOME` override the corresponding base directories. Authentication data can be stored in SQLite, the system keyring, or a permission-restricted JSON file. SQLite and file storage rely on filesystem permissions and do not provide application-level encryption at rest.

## Security Notes

Azem's approval system and persistent action boundaries reduce the risk of accidental operations and duplicate side effects, but they are not an operating-system sandbox:

- `workspace.root` is the shell's initial working directory; it does not prevent commands from accessing paths outside the workspace.
- `allow_write: false` removes built-in write tools, but it cannot prevent an approved shell command from writing files.
- `allow_network` depends on tools correctly declaring network access and does not provide OS-level network isolation.
- `shell_policy: allow` and YOLO approval mode significantly reduce manual confirmation and should be used only in trusted environments.
- If worktree isolation for a subagent fails, Azem may fall back to the shared workspace and report a warning in the run information.

For strict isolation, run Azem in a container, virtual machine, or restricted operating-system account, and enforce filesystem and network policies externally.

## Project Structure

```text
cmd/azem/               Application entry point
internal/agent/         Tool governance, persistent runs, and team agents
internal/app/           Application orchestration, providers, and subagent runtime
internal/auth/          OAuth, credential import, and credential storage
internal/config/        Configuration, paths, and subagent profiles
internal/mcp/           MCP server management
internal/provider/      ChatGPT/Codex and Grok drivers
internal/recovery/      Crash recovery and side-effect reconciliation
internal/session/       Session persistence and compaction
internal/skills/        Agent Skills discovery and activation
internal/store/sqlite/  SQLite schema and storage implementation
internal/tui/           Bubble Tea terminal interface
```

## Development

Run the test suite before submitting changes:

```bash
go test ./...
```

Live provider acceptance tests use the `live` build tag and require valid credentials plus an explicit environment switch. The standard test suite does not access real accounts.

## License

This project is licensed under the [MIT License](LICENSE).