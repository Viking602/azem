# Plugin compatibility

Last verified: 2026-08-24

Azem supports the OpenAI plugin package format, but owns its plugin storage.
The current authoritative package specification is the OpenAI
[plugin builder guide](https://developers.openai.com/plugins/build/plugins) and
[plugin concepts guide](https://developers.openai.com/plugins/concepts/plugins).

## Standard package shape

A plugin is a directory whose only required entry is:

```text
plugin-root/
  .codex-plugin/
    plugin.json
```

The root may also contain `skills/`, `hooks/`, `tools/`, `commands/`,
`agents/`, `themes/`, extension modules, `.mcp.json`, `.app.json`, LSP/DAP
descriptors, and assets. `plugin.json` can declare those paths. Every declared
path begins with `./`, is relative to the plugin root, and must remain inside
that root after symlink resolution.

The manifest supports identity and discovery fields (`name`, `version`,
`description`, `author`, `homepage`, `repository`, `license`, `keywords`) and
an `interface` object for display name, developer, category, capabilities,
URLs, prompts, color, icons, logos, and screenshots. Azem projects
`composerIcon` or `logo` as bounded image data in the Extensions catalog, and
reuses that mark on plugin-owned MCP rows. Skill directories may also supply
`icon.svg` / `icon.png` (or a relative `metadata.icon`) for the Skills list.

## Azem plugin directory

The runtime scans only `<Azem data>/plugin-packages`; it never executes a
plugin directly from a Codex installation or cache directory.

```text
plugin-packages/
  local/<plugin>/                    # installed directly for Azem
  codex/<marketplace>/<plugin>/      # Azem-owned copy imported from Codex
  marketplace/<market>/<name>/       # user-scoped marketplace install
<workspace>/.azem/plugin-packages/marketplace/<market>/<name>/ # project scope
```

`<Azem data>` is `~/.azem`, or `$AZEM_HOME`. Plugins therefore live at
`~/.azem/plugin-packages` unless `AZEM_HOME` points elsewhere.

To install directly, copy a complete plugin directory under
`plugin-packages/local/`. To import from Codex, enable `plugins.import_codex`.
At desktop startup Azem reads Codex's local `config.toml` and plugin package
directories; it never launches the Codex CLI. Selected packages are copied into
a staging directory, validated, and atomically replace the previous Azem-owned
copy. The copied package records the Codex enabled state and source identity.
If Codex storage is unavailable later, the existing Azem copy remains
discoverable; the running plugin root and every `PLUGIN_ROOT` value still point
at Azem storage.

`.mcp.json` may be a direct server map or wrap the map in `mcpServers` or
`mcp_servers`. Azem recognizes stdio descriptors (`command`, `args`, `cwd`,
`env`, `env_vars`) and HTTP descriptors (`type`, `url`, `headers`,
`bearer_token_env_var`).

## Azem compatibility matrix

| Capability | Azem behavior |
|---|---|
| Skills | Fully integrated through the existing Skill catalog and activation path |
| Local stdio MCP | Integrated with plugin-root working directory and `PLUGIN_ROOT` / `PLUGIN_DATA` environment |
| HTTP MCP with bearer env | Integrated; the token stays an environment reference |
| OAuth-only HTTP MCP | Cataloged but disabled until Azem has an authenticated connection |
| Hooks | Cataloged in the Extensions Hooks tab even before trust; executed only when `plugins.trust_hooks: true` and the command is not listed in `hooks.disabled` |
| `.app.json` | Cataloged as an App requirement; requires separate connector authorization |
| Interface assets | Validated and cataloged; supported icons up to 1 MiB render from bounded image data |
| Commands | Markdown commands and extension-registered handlers share the normal slash-command path |
| Tools and extensions | Loaded in the bounded Bun host with duplicate-name rejection and governed tool definitions |
| Agents and providers | Validated and merged into the existing subagent/provider registries |
| Themes | Discovered from validated plugin roots and projected as token maps |
| LSP/DAP descriptors | Copied and resolved through the existing language/debug runtimes |

Directly installed plugins are loaded at desktop startup. Codex plugins first
appear as available choices; selecting one persists its ID in
`plugins.codex_imports`, then copies the package into Azem and loads its Skills
and MCP servers immediately. Import prefers the current directory-scan result,
then the last known catalog, then the local Codex cache and
`.tmp/marketplaces/<marketplace>/plugins/<name>` checkout. Removing the
selection unloads those capabilities in the current process while leaving the
dormant copy recoverable.

## Marketplace lifecycle

`internal/plugins.MarketplaceManager` accepts GitHub shorthand, Git/SSH/HTTP
repositories, local directories, and direct catalog JSON. Catalogs use
`.omp-plugin/marketplace.json` with the Claude-compatible
`.claude-plugin/marketplace.json` fallback. Names and paths are validated,
catalogs are bounded, and relative plugin sources cannot escape the staged
marketplace root.

Installs are identified by `name@marketplace` and scoped to `user` or
`project`. Enabled project installs shadow enabled user installs. Add, remove,
update, discover, install, uninstall, upgrade, enable, and disable are
serialized through one manager and atomically update the registry. Updating a
catalog does not silently reinstall a plugin; upgrading compares the installed
and advertised version. `plugins.marketplace_auto_update` is `off`, `notify`
(default), or `auto`.

Desktop Settings → Extensions → Marketplace and TUI `/marketplace` expose the
same application actions. The desktop re-reads the typed catalog after each
mutation. Neither UI receives a cache path as an executable capability.

Custom extension modules may register file write/delete fallbacks. The Bun host
runs handlers in registration order, skips a throwing handler, and accepts the
first explicit `true`. Azem calls this seam only after a local ordinary-file
mutation fails with `EACCES`, `EPERM`, or `EROFS`; it passes the resolved
workspace destination and preserves the original error if no handler accepts.

## Security boundary

- Installation does not imply hook trust.
- Codex paths are import sources only; runtime loading is restricted to the
  Azem package directory.
- Codex discovery does not imply import. Unselected packages contribute no
  Skills, MCP servers, Hooks, or Apps.
- Invalid or escaping manifest paths are rejected.
- Plugin icons are limited to supported image formats and 1 MiB, then projected
  as image data rather than local paths.
- Descriptor files are bounded to 1 MiB and must contain one JSON document.
- Plugin MCP names are namespaced to avoid overriding user-configured servers.
- Plugin environment and headers remain runtime-only and are never written to
  `config.yaml` or emitted to the UI.
- Remote servers without an explicit supported credential stay disabled rather
  than repeatedly attempting unauthenticated startup.

The desktop Extensions page exposes partial-integration warnings so a missing
credential, pending hook trust, or App authorization cannot look like a fully
active plugin.

Plugin-contributed Skills share the same loading control as personal, project,
configured, and bundled Skills. Stopping one records its Skill name in
`skills.disabled`; the plugin remains installed and enabled, while only that
Skill is removed from model context, slash suggestions, eager activation, and
the runtime registry. The stopped entry remains visible in Extensions for
restoration.

Plugin hook files are always copied into the catalog so the Hooks tab can
show name, source, and event before trust. Execution still requires
`plugins.trust_hooks`. After that global decision, each command can be stopped
independently through `hooks.disabled` / `set_hook_enabled`. An untrusted
plugin hook that is marked enabled still does not run.
