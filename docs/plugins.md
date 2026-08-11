# Plugin compatibility

Last verified: 2026-08-08

Azem follows the universal plugin directory shared by ChatGPT and Codex. The
current authoritative specification is the OpenAI
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
only the fields needed for runtime integration and the Extensions catalog; it
preserves the plugin directory as the source of truth.

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
| Interface assets | Validated and cataloged; the desktop currently renders a native plugin mark rather than reading arbitrary local images |

Azem reads the installed/enabled state from `codex plugin list --json`, then
uses the installed source or the standard cache location
`~/.codex/plugins/cache/<marketplace>/<plugin>/<version>/`. Plugin changes are
loaded at desktop startup, matching the upstream rule that plugin state applies
to a new session.

## Security boundary

- Installation does not imply hook trust.
- Invalid or escaping manifest paths are rejected.
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
