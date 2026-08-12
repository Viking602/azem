# Plugin compatibility

Last verified: 2026-08-12

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

The root may also contain `skills/`, `hooks/`, `.mcp.json`, `.app.json`, and
assets. `plugin.json` can declare `skills`, `mcpServers`, `apps`, `hooks`, and
interface metadata. Every declared path begins with `./`, is relative to the
plugin root, and must remain inside that root after symlink resolution.

The manifest supports identity and discovery fields (`name`, `version`,
`description`, `author`, `homepage`, `repository`, `license`, `keywords`) and
an `interface` object for display name, developer, category, capabilities,
URLs, prompts, color, icons, logos, and screenshots. Azem currently consumes
only the fields needed for runtime integration and the Extensions catalog.

## Azem plugin directory

The runtime scans only `<Azem data>/plugin-packages`; it never executes a
plugin directly from a Codex installation or cache directory.

```text
plugin-packages/
  local/<plugin>/                    # installed directly for Azem
  codex/<marketplace>/<plugin>/      # Azem-owned copy imported from Codex
```

On macOS and Windows, `<Azem data>` is the operating-system user configuration
directory plus `azem`; on Linux it is `${XDG_DATA_HOME:-~/.local/share}/azem`.
`XDG_DATA_HOME` overrides the data root on every platform.

To install directly, copy a complete plugin directory under
`plugin-packages/local/`. To import from Codex, enable `plugins.import_codex`.
At desktop startup Azem reads `codex plugin list --json`, copies every reported
installed package into a staging directory, validates the copied manifest, and
atomically replaces its previous copy. The copied package records the Codex
enabled state and source identity. If Codex is unavailable later, the existing
Azem copy remains discoverable; the running plugin root and every `PLUGIN_ROOT`
value still point at Azem storage.

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
| Hooks | Cataloged; executed only when `plugins.trust_hooks: true` |
| `.app.json` | Cataloged as an App requirement; requires separate connector authorization |
| Interface assets | Validated and cataloged; supported icons up to 1 MiB render from bounded image data |

Directly installed plugins are loaded at desktop startup. Codex plugins first
appear as available choices; selecting one persists its ID in
`plugins.codex_imports`, and the next desktop startup copies and loads only that
selection. Removing the selection stops loading it after restart while leaving
the dormant copy recoverable.

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
