import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  AlertTriangle, AppWindow, Box, ExternalLink, Globe2, LockKeyhole, PackageOpen,
  Plug, Plus, RefreshCw, RotateCw, Search, Server, Sparkles, Terminal, Trash2, Wrench, X,
} from "lucide-react";
import { tFormat, translator, type Language } from "../i18n";
import { listHookCatalog, listMarketplaceCatalog, listSkillCatalog, openExternalURL } from "../bridge";
import { pluginImportID, useRuntimeStore } from "../store";
import type { ActionRequest, MarketplaceCatalog, MarketplaceInstalledPlugin, MarketplacePlugin, MCPServerEntry, MCPServerMutation } from "../types";
import SkillCatalogManager from "./SkillCatalogManager";

type ExtensionTab = "mcp" | "skills" | "plugins" | "marketplace" | "hooks";
type PluginFilter = "all" | "imported" | "available";

interface ExtensionsSettingsProps {
  language: Language;
  sessionId: string;
  openAddRequest?: number;
  executeAction: (request: ActionRequest) => Promise<void>;
  onError: (message: string) => void;
  targetTab?: ExtensionTab;
}

interface MCPFormDraft {
  name: string;
  transport: MCPServerMutation["transport"];
  command: string;
  args: string;
  cwd: string;
  inheritEnv: boolean;
  url: string;
  headerName: string;
  secretReference: string;
  enabled: boolean;
  approval: "always" | "never";
}

export default function ExtensionsSettings({
  language,
  sessionId,
  openAddRequest = 0,
  executeAction,
  onError,
  targetTab,
}: ExtensionsSettingsProps) {
  const t = translator(language);
  const mcpServers = useRuntimeStore((state) => state.mcpServers);
  const skills = useRuntimeStore((state) => state.skills);
  const plugins = useRuntimeStore((state) => state.plugins);
  const hookCatalog = useRuntimeStore((state) => state.hookCatalog);
  const marketplaceCatalog = useRuntimeStore((state) => state.marketplaceCatalog);
  const [tab, setTab] = useState<ExtensionTab>("mcp");
  const [addOpen, setAddOpen] = useState(false);
  const [actionError, setActionError] = useState("");
  const previousAddRequest = useRef(openAddRequest);
  const addOpener = useRef<HTMLElement | null>(null);
  const openAdd = (event?: React.MouseEvent<HTMLButtonElement>) => {
    addOpener.current = event?.currentTarget ?? null;
    setAddOpen(true);
  };
  const enabledSkills = skills.filter((skill) => !skill.disabled).length;
  const enabledPlugins = plugins.filter((plugin) => plugin.enabled && plugin.status !== "invalid").length;
  const connectedMCP = mcpServers.filter((server) => server.enabled && server.state === "ready").length;
  const installedMarketplacePlugins = marketplaceCatalog.installed.filter((plugin) => plugin.enabled).length;

  useEffect(() => {
    if (openAddRequest > previousAddRequest.current) {
      setTab("mcp");
      addOpener.current = addOpener.current ?? document.querySelector<HTMLElement>(".extension-panel-actions .primary-button");
      setAddOpen(true);
    }
    previousAddRequest.current = openAddRequest;
  }, [openAddRequest]);

  useEffect(() => {
    if (targetTab) setTab(targetTab);
  }, [targetTab]);

  useEffect(() => {
    if (tab !== "marketplace") return;
    let active = true;
    void listMarketplaceCatalog()
      .then((catalog) => {
        if (!active) return;
        useRuntimeStore.getState().applyEvents([{ sequence: 0, kind: "marketplace_catalog", state: "listed", marketplaceCatalog: catalog }]);
      })
      .catch((cause) => {
        if (active) onError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { active = false; };
  }, [onError, tab]);

  const run = async (request: ActionRequest) => {
    try {
      setActionError("");
      await executeAction({ ...request, sessionId });
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      setActionError(message);
      onError(message);
      throw cause;
    }
  };
  const refreshSkills = async () => {
    await run({ kind: "reload_skills" });
    const catalog = await listSkillCatalog();
    useRuntimeStore.getState().applyEvents([{
      sequence: 0,
      kind: "skill_catalog",
      state: "reloaded",
      skillCatalog: catalog.entries as unknown as Array<Record<string, unknown>>,
    }]);
  };

  return <section className="extension-hub" aria-label={t("extensionOverview")} data-setting-id={`extensions:${tab}`}>
    <div className="extension-tabbar" role="tablist" aria-label={t("extensionOverview")}>
      <ExtensionTabButton targetID="extensions:mcp" active={tab === "mcp"} icon={Server} label={t("extensionTabMCP")} count={`${connectedMCP}/${mcpServers.length}`} countLabel={t("mcpConnectedServicesMetric")} onClick={() => setTab("mcp")} />
      <ExtensionTabButton targetID="extensions:skills" active={tab === "skills"} icon={Sparkles} label={t("extensionTabSkills")} count={`${enabledSkills}/${skills.length}`} countLabel={t("extensionSkillsMetric")} onClick={() => setTab("skills")} />
      <ExtensionTabButton targetID="extensions:plugins" active={tab === "plugins"} icon={Plug} label={t("extensionTabPlugins")} count={`${enabledPlugins}/${plugins.length}`} countLabel={t("extensionPluginsMetric")} onClick={() => setTab("plugins")} />
      <ExtensionTabButton targetID="extensions:marketplace" active={tab === "marketplace"} icon={PackageOpen} label={language === "zh-CN" ? "市场" : "Marketplace"} count={`${installedMarketplacePlugins}/${marketplaceCatalog.available.length}`} countLabel={language === "zh-CN" ? "已安装市场插件" : "Installed marketplace plugins"} onClick={() => setTab("marketplace")} />
      <ExtensionTabButton targetID="extensions:hooks" active={tab === "hooks"} icon={Wrench} label={t("extensionTabHooks")} count={`${hookCatalog.sources.filter((source) => source.trusted).length}/${hookCatalog.sources.length}`} countLabel={t("extensionHooksMetric")} onClick={() => setTab("hooks")} />
    </div>

    {actionError && <div className="extension-action-error" role="alert">{actionError}</div>}
    <ExtensionContent tab={tab} language={language} mcpServers={mcpServers} skills={skills} plugins={plugins} marketplaceCatalog={marketplaceCatalog} hookCatalog={hookCatalog} run={run} refreshSkills={refreshSkills} openAdd={openAdd} />

    {addOpen && <AddMCPDialog language={language} returnFocusTo={addOpener.current} onClose={() => setAddOpen(false)} onSave={async (payload) => {
      await run({ kind: "upsert_mcp_server", payload });
      setAddOpen(false);
    }} />}
  </section>;
}

function ExtensionTabButton({ active, icon: Icon, label, count, countLabel, onClick, targetID }: { active: boolean; icon: typeof Server; label: string; count: string; countLabel: string; onClick: () => void; targetID: string }) {
  return <button type="button" role="tab" data-setting-id={targetID} aria-selected={active} aria-label={`${label} · ${countLabel}`} className={active ? "active" : ""} onClick={onClick}><Icon size={14} /><span>{label}</span><em>{count}</em></button>;
}

function ExtensionContent({ tab, language, mcpServers, skills, plugins, marketplaceCatalog, hookCatalog, run, refreshSkills, openAdd }: {
  tab: ExtensionTab;
  language: Language;
  mcpServers: ReturnType<typeof useRuntimeStore.getState>["mcpServers"];
  skills: ReturnType<typeof useRuntimeStore.getState>["skills"];
  plugins: ReturnType<typeof useRuntimeStore.getState>["plugins"];
  marketplaceCatalog: MarketplaceCatalog;
  hookCatalog: ReturnType<typeof useRuntimeStore.getState>["hookCatalog"];
  run: (request: ActionRequest) => Promise<void>;
  refreshSkills: () => Promise<void>;
  openAdd: (event?: React.MouseEvent<HTMLButtonElement>) => void;
}) {
  if (tab === "mcp") return <MCPServicesPanel language={language} servers={mcpServers} run={run} openAdd={openAdd} />;
  if (tab === "skills") return <section className="extension-panel" role="tabpanel"><SkillCatalogManager skills={skills} language={language} onReload={refreshSkills} onSetEnabled={(name, enabled) => run({ kind: "set_skill_enabled", target: name, decision: String(enabled) })} /></section>;
  if (tab === "hooks") return <HooksPanel language={language} catalog={hookCatalog} run={run} />;
  if (tab === "marketplace") return <MarketplacePanel language={language} catalog={marketplaceCatalog} run={run} />;
  return <PluginsPanel language={language} plugins={plugins} run={run} />;
}


function MarketplacePanel({ language, catalog, run }: {
  language: Language;
  catalog: MarketplaceCatalog;
  run: (request: ActionRequest) => Promise<void>;
}) {
  const zh = language === "zh-CN";
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("");
  const [scope, setScope] = useState<"user" | "project">("user");
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState("");
  const refresh = async () => {
    const snapshot = await listMarketplaceCatalog();
    useRuntimeStore.getState().applyEvents([{ sequence: 0, kind: "marketplace_catalog", state: "listed", marketplaceCatalog: snapshot }]);
  };
  const mutate = async (key: string, request: ActionRequest): Promise<boolean> => {
    setBusy((current) => new Set(current).add(key));
    try {
      try {
        await run(request);
      } catch {
        return false;
      }
      try {
        await refresh();
      } catch (cause) {
        useRuntimeStore.getState().setError(cause instanceof Error ? cause.message : String(cause));
      }
      return true;
    } finally {
      setBusy((current) => {
        const next = new Set(current);
        next.delete(key);
        return next;
      });
    }
  };
  const plugins = useMemo(() => {
    const entries = [...catalog.available];
    for (const installed of catalog.installed) {
      if (!entries.some((entry) => entry.id === installed.id)) {
        entries.push({
          id: installed.id, name: installed.name, marketplace: installed.marketplace, version: installed.version,
          description: "", category: "", homepage: "", license: "", keywords: [], tags: [],
        });
      }
    }
    const normalized = query.trim().toLocaleLowerCase(language);
    return normalized ? entries.filter((plugin) => [
      plugin.id, plugin.name, plugin.marketplace, plugin.description, plugin.category, ...plugin.keywords, ...plugin.tags,
    ].join("\n").toLocaleLowerCase(language).includes(normalized)) : entries;
  }, [catalog.available, catalog.installed, language, query]);
  const addSource = (event: React.FormEvent) => {
    event.preventDefault();
    const value = source.trim();
    if (!value) return;
    void mutate(`source:${value}`, { kind: "marketplace_add", target: value }).then((saved) => { if (saved) setSource(""); });
  };
  return <section className="extension-panel marketplace-panel" role="tabpanel">
    <header className="extension-panel-header"><div><h2>{zh ? "插件市场" : "Plugin marketplace"}</h2><p>{zh ? "从 Git、目录或目录 JSON 中浏览和安装 OMP / Claude 兼容插件。" : "Browse and install OMP or Claude-compatible plugins from Git, directories, or catalog JSON."}</p></div><button type="button" className="subtle-button" onClick={() => void refresh().catch((cause) => useRuntimeStore.getState().setError(cause instanceof Error ? cause.message : String(cause)))}><RefreshCw size={13} />{zh ? "刷新" : "Refresh"}</button></header>
    <form className="marketplace-source-form" onSubmit={addSource}>
      <label><span>{zh ? "添加市场源" : "Add marketplace source"}</span><input value={source} onChange={(event) => setSource(event.target.value)} placeholder="owner/repo, https://…, ./path" /></label>
      <button type="submit" className="primary-button" disabled={!source.trim() || busy.has(`source:${source.trim()}`)}><Plus size={13} />{zh ? "添加" : "Add"}</button>
    </form>
    {catalog.marketplaces.length > 0 ? <div className="marketplace-source-list" aria-label={zh ? "已配置市场" : "Configured marketplaces"}>
      {catalog.marketplaces.map((marketplace) => {
        const key = `market:${marketplace.name}`;
        const removing = confirming === key;
        return <div className="marketplace-source-row" key={marketplace.name}>
          <div><strong>{marketplace.name}</strong><small>{marketplace.source}</small></div>
          <span>{marketplace.type}</span>
          <button type="button" disabled={busy.has(key)} aria-label={zh ? `更新 ${marketplace.name}` : `Update ${marketplace.name}`} onClick={() => void mutate(key, { kind: "marketplace_update", target: marketplace.name })}><RotateCw size={13} /></button>
          {removing ? <><button type="button" className="danger-text" onClick={() => void mutate(key, { kind: "marketplace_remove", target: marketplace.name }).then((removed) => { if (removed) setConfirming(""); })}>{zh ? "确认" : "Confirm"}</button><button type="button" aria-label={zh ? "取消移除" : "Cancel removal"} onClick={() => setConfirming("")}><X size={13} /></button></> : <button type="button" aria-label={zh ? `移除 ${marketplace.name}` : `Remove ${marketplace.name}`} onClick={() => setConfirming(key)}><Trash2 size={13} /></button>}
        </div>;
      })}
    </div> : <p className="marketplace-empty">{zh ? "还没有市场。添加目录源后即可浏览插件。" : "No marketplaces yet. Add a catalog source to browse plugins."}</p>}
    <div className="marketplace-browser-tools">
      <label className="marketplace-search"><Search size={13} aria-hidden="true" /><span className="sr-only">{zh ? "搜索市场插件" : "Search marketplace plugins"}</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={zh ? "搜索名称、说明、标签…" : "Search names, descriptions, or tags…"} /></label>
      <label className="marketplace-scope"><span>{zh ? "安装范围" : "Install scope"}</span><select value={scope} onChange={(event) => setScope(event.target.value as "user" | "project")}><option value="user">{zh ? "用户" : "User"}</option><option value="project">{zh ? "当前项目" : "Project"}</option></select></label>
    </div>
    {plugins.length > 0 ? <div className="marketplace-plugin-list">{plugins.map((plugin) => <MarketplacePluginRow
      key={plugin.id}
      plugin={plugin}
      installs={catalog.installed.filter((installed) => installed.id === plugin.id)}
      upgrades={catalog.upgrades}
      language={language}
      installScope={scope}
      busy={busy}
      confirming={confirming}
      onConfirm={setConfirming}
      onMutate={mutate}
    />)}</div> : <p className="marketplace-empty">{zh ? "没有匹配的市场插件。" : "No matching marketplace plugins."}</p>}
  </section>;
}

function MarketplacePluginRow({ plugin, installs, upgrades, language, installScope, busy, confirming, onConfirm, onMutate }: {
  plugin: MarketplacePlugin;
  installs: MarketplaceInstalledPlugin[];
  upgrades: MarketplaceCatalog["upgrades"];
  language: Language;
  installScope: "user" | "project";
  busy: Set<string>;
  confirming: string;
  onConfirm: (key: string) => void;
  onMutate: (key: string, request: ActionRequest) => Promise<boolean>;
}) {
  const zh = language === "zh-CN";
  return <article className="marketplace-plugin-row">
    <div className="marketplace-plugin-heading"><div><strong>{plugin.name}</strong><small>{plugin.marketplace}{plugin.version ? ` · ${plugin.version}` : ""}</small></div>{plugin.homepage ? <button type="button" aria-label={zh ? `打开 ${plugin.name} 主页` : `Open ${plugin.name} homepage`} onClick={() => void openExternalURL(plugin.homepage)}><ExternalLink size={13} /></button> : null}</div>
    {plugin.description ? <p>{plugin.description}</p> : null}
    <div className="marketplace-plugin-meta">{[plugin.category, plugin.license, ...plugin.tags.slice(0, 3)].filter(Boolean).map((item) => <span key={item}>{item}</span>)}</div>
    {installs.length === 0 ? <button type="button" className="primary-button marketplace-install" disabled={busy.has(`${plugin.id}:${installScope}`)} onClick={() => void onMutate(`${plugin.id}:${installScope}`, { kind: "marketplace_install", target: plugin.id, decision: installScope, payload: { scope: installScope } })}><PackageOpen size={13} />{zh ? `安装到${installScope === "user" ? "用户" : "项目"}` : `Install for ${installScope}`}</button> : <div className="marketplace-install-list">{installs.map((installed) => {
      const key = `${installed.id}:${installed.scope}`;
      const upgrade = upgrades.find((item) => item.plugin.id === installed.id && item.plugin.scope === installed.scope);
      const removing = confirming === key;
      return <div key={key} className="marketplace-install-row">
        <span><b>{installed.scope === "user" ? zh ? "用户" : "User" : zh ? "项目" : "Project"}</b>{installed.version}{upgrade ? ` → ${upgrade.latest}` : ""}</span>
        <button type="button" disabled={busy.has(key)} onClick={() => void onMutate(key, { kind: installed.enabled ? "marketplace_disable" : "marketplace_enable", target: installed.id, decision: installed.scope, payload: { scope: installed.scope } })}>{installed.enabled ? zh ? "停用" : "Disable" : zh ? "启用" : "Enable"}</button>
        {upgrade ? <button type="button" disabled={busy.has(key)} onClick={() => void onMutate(key, { kind: "marketplace_upgrade", target: installed.id, decision: installed.scope, payload: { scope: installed.scope } })}>{zh ? "升级" : "Upgrade"}</button> : null}
        {removing ? <><button type="button" className="danger-text" onClick={() => void onMutate(key, { kind: "marketplace_uninstall", target: installed.id, decision: installed.scope, payload: { scope: installed.scope } }).then((removed) => { if (removed) onConfirm(""); })}>{zh ? "确认卸载" : "Confirm"}</button><button type="button" aria-label={zh ? "取消卸载" : "Cancel uninstall"} onClick={() => onConfirm("")}><X size={12} /></button></> : <button type="button" aria-label={zh ? `卸载 ${plugin.name}` : `Uninstall ${plugin.name}`} onClick={() => onConfirm(key)}><Trash2 size={12} /></button>}
      </div>;
    })}</div>}
  </article>;
}
function MCPServicesPanel({ language, servers, run, openAdd }: {
  language: Language;
  servers: MCPServerEntry[];
  run: (request: ActionRequest) => Promise<void>;
  openAdd: (event?: React.MouseEvent<HTMLButtonElement>) => void;
}) {
  const t = translator(language);
  const [pendingServers, setPendingServers] = useState<Set<string>>(new Set());
  const [deleteTarget, setDeleteTarget] = useState<MCPServerEntry | null>(null);
  const setEnabled = async (server: MCPServerEntry) => {
    setPendingServers((current) => new Set(current).add(server.name));
    try { await run({ kind: "set_mcp_enabled", target: server.name, decision: String(!server.enabled) }); }
    finally {
      setPendingServers((current) => {
        const next = new Set(current);
        next.delete(server.name);
        return next;
      });
    }
  };
  const remove = async (server: MCPServerEntry) => {
    setPendingServers((current) => new Set(current).add(server.name));
    try {
      await run({ kind: "delete_mcp_server", target: server.name });
      setDeleteTarget(null);
    } catch {
      // run already reports the actionable error through the settings surface.
    } finally {
      setPendingServers((current) => {
        const next = new Set(current);
        next.delete(server.name);
        return next;
      });
    }
  };
  return <>
    <section className="extension-panel" role="tabpanel">
    <header className="extension-panel-header"><div><h2>{t("extensionMCPTitle")}</h2><p>{t("mcpRuntimeHint")}</p></div><div className="extension-panel-actions"><button type="button" className="subtle-button" onClick={() => void run({ kind: "refresh_mcp" })}><RefreshCw size={13} />{t("refreshMCP")}</button><button type="button" className="primary-button" onClick={openAdd}><Plus size={13} />{t("addMCPServer")}</button></div></header>
    {servers.length === 0 ? <ExtensionEmpty icon={Server} title={t("noMCPServers")} hint={t("noMCPServersHint")} action={<button type="button" className="primary-button" onClick={openAdd}><Plus size={13} />{t("addMCPServer")}</button>} /> : <div className="mcp-server-list">{servers.map((server) => <MCPServerRow key={server.name} server={server} language={language} pending={pendingServers.has(server.name)} onToggle={() => void setEnabled(server)} onReconnect={() => void run({ kind: "reconnect_mcp", target: server.name })} onDelete={() => setDeleteTarget(server)} />)}</div>}
    </section>
    {deleteTarget && <DeleteMCPDialog server={deleteTarget} language={language} pending={pendingServers.has(deleteTarget.name)} onCancel={() => setDeleteTarget(null)} onConfirm={() => void remove(deleteTarget)} />}
  </>;
}

async function applyHookCatalog() {
  const catalog = await listHookCatalog();
  useRuntimeStore.getState().applyEvents([{
    sequence: 0,
    kind: "hook_catalog",
    state: "listed",
    hookCatalog: catalog,
  }]);
}

function HooksPanel({ language, catalog, run }: {
  language: Language;
  catalog: ReturnType<typeof useRuntimeStore.getState>["hookCatalog"];
  run: (request: ActionRequest) => Promise<void>;
}) {
  const t = translator(language);
  const [pending, setPending] = useState(false);
  const [pendingHook, setPendingHook] = useState("");
  const [confirmTrust, setConfirmTrust] = useState(false);
  const pluginSources = catalog.sources.filter((source) => source.origin === "plugin");
  const localSources = catalog.sources.filter((source) => source.origin !== "plugin");
  const setTrusted = async (trusted: boolean) => {
    setPending(true);
    try {
      await run({ kind: "set_plugin_hooks_trusted", decision: String(trusted) });
      await applyHookCatalog();
      setConfirmTrust(false);
    } catch {
      // run already reports the actionable error through the settings surface.
    } finally {
      setPending(false);
    }
  };
  const setHookEnabled = async (id: string, enabled: boolean) => {
    setPendingHook(id);
    try {
      await run({ kind: "set_hook_enabled", target: id, decision: String(enabled) });
      await applyHookCatalog();
    } catch {
      // run already reports the actionable error through the settings surface.
    } finally {
      setPendingHook("");
    }
  };
  return <>
    <section className="extension-panel" role="tabpanel">
      <header className="extension-panel-header">
        <div>
          <h2>{t("extensionHooksTitle")}</h2>
          <p>{t("extensionHooksHint")}</p>
        </div>
        <div className="extension-panel-actions">
          <button type="button" className="subtle-button" onClick={() => void applyHookCatalog().catch(() => undefined)}><RefreshCw size={13} />{t("refresh")}</button>
        </div>
      </header>
      <article className="hook-trust-card">
        <div>
          <strong>{t("trustPluginHooks")}</strong>
          <small>{t("trustPluginHooksHint")}</small>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={catalog.trustHooks}
          className={`settings-switch ${catalog.trustHooks ? "on" : ""}`}
          disabled={pending}
          aria-label={t("trustPluginHooks")}
          onClick={() => {
            if (catalog.trustHooks) void setTrusted(false);
            else setConfirmTrust(true);
          }}
        ><span /></button>
      </article>
      {catalog.sources.length === 0 && catalog.commands.length === 0 ? <ExtensionEmpty icon={Wrench} title={t("noHooks")} hint={t("noHooksHint")} /> : <div className="hook-list">
        {pluginSources.length > 0 && <div className="plugin-group-heading">{t("hookPluginGroup")}<span>{pluginSources.length}</span></div>}
        {pluginSources.map((source) => <HookSourceRow key={source.id} source={source} language={language} />)}
        {localSources.length > 0 && <div className="plugin-group-heading">{t("hookLocalGroup")}<span>{localSources.length}</span></div>}
        {localSources.map((source) => <HookSourceRow key={source.id} source={source} language={language} />)}
        {catalog.commands.length > 0 && <div className="plugin-group-heading">{t("hookLoadedGroup")}<span>{catalog.commands.length}</span></div>}
        {catalog.commands.map((command, index) => <HookCommandRow key={command.id || `${command.event}:${command.name}:${command.source}:${index}`} command={command} language={language} pending={pendingHook === command.id} trustHooks={catalog.trustHooks} onToggle={() => void setHookEnabled(command.id, !command.enabled)} />)}
        {catalog.diagnostics.map((item, index) => <small className="plugin-warning" key={`${item.source}:${index}`}>{item.source}: {item.message}</small>)}
      </div>}
    </section>
    {confirmTrust && <TrustHooksDialog language={language} pending={pending} onCancel={() => setConfirmTrust(false)} onConfirm={() => void setTrusted(true)} />}
  </>;
}

function hookCommandStatus(command: ReturnType<typeof useRuntimeStore.getState>["hookCatalog"]["commands"][number], trustHooks: boolean, language: Language) {
  const t = translator(language);
  if (!command.enabled) return { label: t("hookDisabled"), tone: "off" };
  if (command.origin === "plugin" && !trustHooks) return { label: t("hookPendingTrust"), tone: "attention" };
  return { label: t("hookEnabled"), tone: "ready" };
}

function HookCommandRow({ command, language, pending, trustHooks, onToggle }: {
  command: ReturnType<typeof useRuntimeStore.getState>["hookCatalog"]["commands"][number];
  language: Language;
  pending: boolean;
  trustHooks: boolean;
  onToggle: () => void;
}) {
  const t = translator(language);
  const status = hookCommandStatus(command, trustHooks, language);
  const toggleLabel = tFormat(language, command.enabled ? "disableHook" : "enableHook", { name: command.name });
  return <article className={`hook-command-row ${command.enabled ? "" : "disabled"}`}>
    <span className="hook-event">{command.event}</span>
    <div>
      <strong>{command.name}</strong>
      <small>{[command.matcher && `${language === "zh-CN" ? "匹配" : "match"} ${command.matcher}`, command.command, command.source].filter(Boolean).join(" · ")}</small>
    </div>
    <div className="hook-command-state">
      <em className={`plugin-status ${status.tone}`}>{status.label}</em>
      <button type="button" role="switch" aria-checked={command.enabled} aria-label={toggleLabel} className={`skill-state-switch ${command.enabled ? "on" : ""} ${pending ? "pending" : ""}`} disabled={pending || !command.id} onClick={onToggle}><i /></button>
    </div>
  </article>;
}

function HookSourceRow({ source, language }: { source: ReturnType<typeof useRuntimeStore.getState>["hookCatalog"]["sources"][number]; language: Language }) {
  const t = translator(language);
  const count = source.hookCount > 0 ? tFormat(language, "hookSourceCount", { count: source.hookCount }) : t("hookSourcePresent");
  const origin = source.origin === "plugin" ? t("hookOriginPlugin") : source.origin === "project" ? t("hookOriginProject") : source.origin === "additional" ? t("hookOriginAdditional") : t("hookOriginUser");
  const icon = source.logoPath?.startsWith("data:image/") ? <img className="plugin-logo" src={source.logoPath} alt="" /> : <Wrench size={16} />;
  return <article className={`plugin-row ${source.trusted ? "" : "disabled"}`}>
    <span className="plugin-mark">{icon}</span>
    <div className="plugin-row-copy">
      <div className="plugin-row-title">
        <strong>{source.name}</strong>
        <em className={`plugin-status ${source.trusted ? "ready" : "attention"}`}>{source.trusted ? t("hookTrusted") : t("hookPendingTrust")}</em>
      </div>
      <small className="plugin-row-origin">{origin} · {count}</small>
      {source.source && <small className="plugin-row-desc">{source.source}</small>}
      {source.warning && <small className="plugin-warning">{source.warning}</small>}
    </div>
  </article>;
}

function settingsOverlayHost() {
  return document.querySelector<HTMLElement>("dialog.settings-dialog")
    ?? document.querySelector<HTMLElement>("dialog[open]");
}

function TrustHooksDialog({ language, pending, onCancel, onConfirm }: { language: Language; pending: boolean; onCancel: () => void; onConfirm: () => void }) {
  const t = translator(language);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const host = settingsOverlayHost();
  useEffect(() => {
    cancelRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !pending) onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onCancel, pending]);
  const dialog = <div className="mcp-delete-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !pending) onCancel(); }}>
    <section className="mcp-delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby="trust-hooks-title" aria-describedby="trust-hooks-description">
      <span className="mcp-delete-mark hook-trust-mark"><AlertTriangle size={17} /></span>
      <div>
        <h2 id="trust-hooks-title">{t("trustPluginHooksConfirmTitle")}</h2>
        <p id="trust-hooks-description">{t("trustPluginHooksConfirmHint")}</p>
      </div>
      <footer>
        <button ref={cancelRef} type="button" className="subtle-button" onClick={onCancel} disabled={pending}>{t("cancel")}</button>
        <button type="button" className="primary-button hook-trust-confirm" disabled={pending} onClick={onConfirm}>{pending ? t("saving") : t("trustPluginHooksConfirm")}</button>
      </footer>
    </section>
  </div>;
  return host ? createPortal(dialog, host) : dialog;
}

function isAvailablePlugin(plugin: { origin: string }) {
  return plugin.origin === "codex_available";
}

function PluginsPanel({ language, plugins, run }: { language: Language; plugins: ReturnType<typeof useRuntimeStore.getState>["plugins"]; run: (request: ActionRequest) => Promise<void> }) {
  const t = translator(language);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<PluginFilter>("all");
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [deleteTarget, setDeleteTarget] = useState<typeof plugins[number] | null>(null);
  const importedCount = plugins.filter((plugin) => !isAvailablePlugin(plugin)).length;
  const availableCount = plugins.length - importedCount;
  const visiblePlugins = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase(language);
    return plugins.filter((plugin) => {
      if (filter === "imported" && isAvailablePlugin(plugin)) return false;
      if (filter === "available" && !isAvailablePlugin(plugin)) return false;
      if (!normalized) return true;
      return `${plugin.displayName}\n${plugin.name}\n${plugin.description}\n${plugin.marketplace}\n${plugin.developerName}`.toLocaleLowerCase(language).includes(normalized);
    });
  }, [filter, language, plugins, query]);
  const imported = visiblePlugins.filter((plugin) => !isAvailablePlugin(plugin));
  const available = visiblePlugins.filter(isAvailablePlugin);
  const showGroups = filter === "all" && imported.length > 0 && available.length > 0;
  const chooseImport = async (plugin: typeof plugins[number], importedState: boolean) => {
    const pluginID = pluginImportID(plugin);
    if (!pluginID) {
      try {
        await run({ kind: "set_plugin_imported", target: "", decision: String(importedState) });
      } catch {
        // run already surfaces the missing-id error on the Extensions panel.
      }
      return;
    }
    setPending((current) => new Set(current).add(pluginID));
    try {
      await run({ kind: "set_plugin_imported", target: pluginID, decision: String(importedState) });
      if (!importedState) setDeleteTarget(null);
    } catch {
      // run already reports the actionable error through the settings surface.
    } finally {
      setPending((current) => {
        const next = new Set(current);
        next.delete(pluginID);
        return next;
      });
    }
  };
  return <>
    <section className="extension-panel" role="tabpanel">
    <header className="extension-panel-header"><div><h2>{t("extensionPluginsTitle")}</h2><p>{t("extensionPluginsHint")}</p></div></header>
    {plugins.length === 0 ? <ExtensionEmpty icon={PackageOpen} title={t("noPluginsInstalled")} hint={t("noPluginsInstalledHint")} /> : <>
      <div className="plugin-toolbar">
        <label className="skill-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchPlugins")} /></label>
        <div className="skill-filters" role="group" aria-label={t("filterPlugins")}>
          {([
            ["all", t("skillFilterAll"), plugins.length],
            ["imported", t("pluginFilterImported"), importedCount],
            ["available", t("pluginFilterAvailable"), availableCount],
          ] as const).map(([value, label, count]) => <button type="button" key={value} className={filter === value ? "active" : ""} aria-pressed={filter === value} onClick={() => setFilter(value)}>{label}<span>{count}</span></button>)}
        </div>
      </div>
      {visiblePlugins.length === 0 ? <div className="extension-empty compact"><span><Search size={18} /></span><strong>{t("noMatchingPlugins")}</strong></div> : <div className="plugin-list">
        {imported.length > 0 && <PluginGroup heading={showGroups ? t("pluginImportedGroup") : ""} count={imported.length} plugins={imported} language={language} pending={pending} onImport={chooseImport} onDelete={setDeleteTarget} />}
        {available.length > 0 && <PluginGroup heading={showGroups ? t("pluginAvailableGroup") : ""} count={available.length} plugins={available} language={language} pending={pending} onImport={chooseImport} onDelete={setDeleteTarget} />}
      </div>}
    </>}
    </section>
    {deleteTarget && <DeletePluginDialog plugin={deleteTarget} language={language} pending={pending.has(deleteTarget.id)} onCancel={() => setDeleteTarget(null)} onConfirm={() => void chooseImport(deleteTarget, false)} />}
  </>;
}

function PluginGroup({ heading, count, plugins, language, pending, onImport, onDelete }: {
  heading: string;
  count: number;
  plugins: ReturnType<typeof useRuntimeStore.getState>["plugins"];
  language: Language;
  pending: Set<string>;
  onImport: (plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number], imported: boolean) => Promise<void>;
  onDelete: (plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number]) => void;
}) {
  return <>
    {heading && <div className="plugin-group-heading">{heading}<span>{count}</span></div>}
    {plugins.map((plugin) => <PluginRow key={pluginImportID(plugin) || plugin.name} plugin={plugin} language={language} pending={pending.has(pluginImportID(plugin))} onImport={() => void onImport(plugin, true)} onDelete={() => onDelete(plugin)} />)}
  </>;
}

function PluginRow({ plugin, language, pending, onImport, onDelete }: { plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number]; language: Language; pending: boolean; onImport: () => void; onDelete: () => void }) {
  const t = translator(language);
  const available = isAvailablePlugin(plugin);
  const faded = plugin.status === "invalid" || (!available && !plugin.enabled);
  const canImport = available;
  const canDelete = plugin.origin === "codex";
  const name = plugin.displayName || plugin.name;
  const icon = plugin.logoPath.startsWith("data:image/")
    ? <img className="plugin-logo" src={plugin.logoPath} alt="" />
    : <Plug size={16} />;
  return <article className={`plugin-row ${faded ? "disabled" : ""}`}>
    <span className="plugin-mark" style={{ "--plugin-color": plugin.brandColor || "var(--muted)" } as React.CSSProperties}>{icon}</span>
    <div className="plugin-row-copy">
      <div className="plugin-row-title"><strong>{name}</strong><em className={`plugin-status ${pluginStatusTone(plugin.enabled, plugin.status)}`}>{pluginStatusLabel(plugin.enabled, plugin.status, language)}</em></div>
      <small className="plugin-row-origin">{pluginOriginLabel(plugin.origin, plugin.marketplace, plugin.version, language)}</small>
      {plugin.description && <small className="plugin-row-desc">{plugin.description}</small>}
      <PluginCapabilities plugin={plugin} language={language} />
      {plugin.warning && <small className="plugin-warning">{plugin.warning}</small>}
    </div>
    {(canImport || canDelete) && <div className="plugin-row-actions">{canImport
      ? <button type="button" className="primary-button plugin-import-button" disabled={pending} aria-label={tFormat(language, "importPluginNamed", { plugin: name })} onClick={(event) => { event.preventDefault(); event.stopPropagation(); onImport(); }}>{pending ? t("saving") : t("importIntoAzem")}</button>
      : <button type="button" className="subtle-button plugin-import-button plugin-delete-button" disabled={pending} aria-label={tFormat(language, "deletePluginNamed", { plugin: name })} onClick={onDelete}>{t("deletePlugin")}</button>}</div>}
  </article>;
}

function pluginOriginLabel(origin: string, marketplace: string, version: string, language: Language) {
  const source = origin === "codex" || origin === "codex_available"
    ? `Codex · ${marketplace}`
    : (language === "zh-CN" ? "本机安装" : "Installed locally");
  return version ? `${source} · ${version}` : source;
}

function pluginStatusLabel(enabled: boolean, status: string, language: Language) {
	if (status === "available") return language === "zh-CN" ? "可导入" : "Available";
	if (status === "restart_required") return language === "zh-CN" ? "重启后生效" : "Restart required";
  if (!enabled) return language === "zh-CN" ? "已停用" : "Disabled";
  if (status === "ready") return language === "zh-CN" ? "已接入" : "Connected";
  return language === "zh-CN" ? "部分接入" : "Partial";
}

function pluginStatusTone(enabled: boolean, status: string) {
  if (status === "available") return "available";
  if (status === "restart_required") return "attention";
  if (!enabled) return "off";
  if (status === "ready") return "ready";
  return "attention";
}

function PluginCapabilities({ plugin, language }: { plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number]; language: Language }) {
  const hookState = plugin.hooksTrusted ? (language === "zh-CN" ? "已信任" : "Trusted") : (language === "zh-CN" ? "待信任" : "Pending");
  return <div className="plugin-capabilities">
    {plugin.skillCount > 0 && <span><Sparkles size={12} />{plugin.skillCount} Skills</span>}
    {plugin.mcpServerCount > 0 && <span><Server size={12} />{plugin.integratedMCPCount}/{plugin.mcpServerCount} MCP</span>}
    {plugin.hookCount > 0 && <span className={!plugin.hooksTrusted ? "pending" : ""}><Wrench size={12} />Hooks {hookState}</span>}
    {plugin.hasApp && <span className="pending"><AppWindow size={12} />App OAuth</span>}
  </div>;
}

function MCPServerRow({ server, language, pending, onToggle, onReconnect, onDelete }: { server: MCPServerEntry; language: Language; pending: boolean; onToggle: () => void; onReconnect: () => void; onDelete: () => void }) {
  const t = translator(language);
  const ready = server.enabled && server.state === "ready";
  const TransportIcon = server.transport === "stdio" ? Terminal : Globe2;
  return <article className={`mcp-server-row ${!server.enabled ? "disabled" : ""}`}>
    <span className="mcp-server-icon" data-ready={ready} data-image={Boolean(server.icon?.startsWith("data:image/"))}>{server.icon?.startsWith("data:image/") ? <img src={server.icon} alt="" /> : <TransportIcon size={17} />}</span>
    <div className="mcp-server-copy">
      <div className="mcp-server-title"><strong>{server.name}</strong><span className={`mcp-state ${server.state}`}><i />{t(mcpStateKey(server.state))}</span></div>
      <small className="mcp-server-target">{server.target || (server.transport === "stdio" ? t("mcpLocalProcess") : t("mcpRemoteHTTP"))}</small>
      <MCPServerMeta server={server} language={language} />
      <MCPServerError error={server.error} />
    </div>
    <MCPServerActions server={server} language={language} pending={pending} onToggle={onToggle} onReconnect={onReconnect} onDelete={onDelete} />
  </article>;
}

function mcpStateKey(state: string): "mcpStatusReady" | "mcpStatusConnecting" | "mcpStatusDegraded" | "mcpStatusDisabled" | "mcpStatusStopped" {
  if (state === "ready") return "mcpStatusReady";
  if (state === "connecting") return "mcpStatusConnecting";
  if (state === "degraded") return "mcpStatusDegraded";
  if (state === "disabled") return "mcpStatusDisabled";
  return "mcpStatusStopped";
}

function MCPServerMeta({ server, language }: { server: MCPServerEntry; language: Language }) {
  const t = translator(language);
  const toolLabel = server.toolCount > 0 ? tFormat(language, "mcpTools", { count: server.toolCount }) : t("mcpNoTools");
  const approvalLabel = server.approval === "never" ? t("mcpApprovalNever") : t("mcpApprovalAlways");
  const transportLabel = server.transport === "stdio" ? t("mcpLocalProcess") : t("mcpRemoteHTTP");
  return <div className="mcp-server-meta"><span><Box size={11} />{toolLabel}</span><span><LockKeyhole size={11} />{approvalLabel}</span><span>{transportLabel}</span></div>;
}

function MCPServerError({ error }: { error: string }) {
  if (!error) return null;
  return <small className="mcp-server-error"><AlertTriangle size={11} />{error}</small>;
}

function MCPServerActions({ server, language, pending, onToggle, onReconnect, onDelete }: { server: MCPServerEntry; language: Language; pending: boolean; onToggle: () => void; onReconnect: () => void; onDelete: () => void }) {
  const t = translator(language);
  const reconnectLabel = tFormat(language, "reconnectMCP", { server: server.name });
  const deleteLabel = tFormat(language, "deleteMCP", { server: server.name });
  const toggleLabel = tFormat(language, server.enabled ? "disableMCP" : "enableMCP", { server: server.name });
  return <div className="mcp-server-actions">
    {server.removable && <button type="button" className="mcp-delete" onClick={onDelete} aria-label={deleteLabel} disabled={pending}><Trash2 size={14} /></button>}
    {server.enabled && <button type="button" className="mcp-reconnect" onClick={onReconnect} aria-label={reconnectLabel} disabled={pending}><RotateCw size={14} /></button>}
    <div className="mcp-server-state"><span>{server.enabled ? t("skillEnabled") : t("skillDisabled")}</span><button type="button" role="switch" aria-checked={server.enabled} aria-label={toggleLabel} className={`skill-state-switch ${server.enabled ? "on" : ""} ${pending ? "pending" : ""}`} disabled={pending} onClick={onToggle}><i /></button></div>
  </div>;
}

function DeleteMCPDialog({ server, language, pending, onCancel, onConfirm }: { server: MCPServerEntry; language: Language; pending: boolean; onCancel: () => void; onConfirm: () => void }) {
  const t = translator(language);
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    cancelRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !pending) onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onCancel, pending]);
  return <div className="mcp-delete-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !pending) onCancel(); }}>
    <section className="mcp-delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby="delete-mcp-title" aria-describedby="delete-mcp-description">
      <span className="mcp-delete-mark"><Trash2 size={17} /></span>
      <div><h2 id="delete-mcp-title">{tFormat(language, "deleteMCPTitle", { server: server.name })}</h2><p id="delete-mcp-description">{t("deleteMCPHint")}</p></div>
      <footer><button ref={cancelRef} type="button" className="subtle-button" onClick={onCancel} disabled={pending}>{t("mcpCancel")}</button><button type="button" className="mcp-delete-confirm" onClick={onConfirm} disabled={pending}>{pending ? t("deletingMCP") : t("deleteMCPConfirm")}</button></footer>
    </section>
  </div>;
}

function DeletePluginDialog({ plugin, language, pending, onCancel, onConfirm }: { plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number]; language: Language; pending: boolean; onCancel: () => void; onConfirm: () => void }) {
  const t = translator(language);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const name = plugin.displayName || plugin.name;
  useEffect(() => {
    cancelRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !pending) onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onCancel, pending]);
  return <div className="mcp-delete-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !pending) onCancel(); }}>
    <section className="mcp-delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby="delete-plugin-title" aria-describedby="delete-plugin-description">
      <span className="mcp-delete-mark"><Trash2 size={17} /></span>
      <div><h2 id="delete-plugin-title">{tFormat(language, "deletePluginTitle", { plugin: name })}</h2><p id="delete-plugin-description">{t("deletePluginHint")}</p></div>
      <footer><button ref={cancelRef} type="button" className="subtle-button" onClick={onCancel} disabled={pending}>{t("mcpCancel")}</button><button type="button" className="mcp-delete-confirm" onClick={onConfirm} disabled={pending}>{pending ? t("deletingPlugin") : t("deletePluginConfirm")}</button></footer>
    </section>
  </div>;
}

function ExtensionEmpty({ icon: Icon, title, hint, action }: { icon: typeof Server; title: string; hint: string; action?: React.ReactNode }) {
  return <div className="extension-empty"><span><Icon size={20} /></span><strong>{title}</strong><small>{hint}</small>{action}</div>;
}

const mcpAddFocusable = 'button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

function isMCPDraftDirty(draft: MCPFormDraft) {
  return draft.name.trim() !== ""
    || draft.transport !== "stdio"
    || draft.command.trim() !== ""
    || draft.args.trim() !== ""
    || draft.cwd.trim() !== ""
    || !draft.inheritEnv
    || draft.url.trim() !== ""
    || draft.headerName.trim() !== ""
    || draft.secretReference.trim() !== ""
    || !draft.enabled
    || draft.approval !== "always";
}

function AddMCPDialog({ language, onClose, onSave, returnFocusTo }: { language: Language; onClose: () => void; onSave: (payload: MCPServerMutation) => Promise<void>; returnFocusTo?: HTMLElement | null }) {
  const t = translator(language);
  const [name, setName] = useState("");
  const [transport, setTransport] = useState<MCPServerMutation["transport"]>("stdio");
  const [command, setCommand] = useState("");
  const [args, setArgs] = useState("");
  const [cwd, setCWD] = useState("");
  const [inheritEnv, setInheritEnv] = useState(true);
  const [url, setURL] = useState("");
  const [headerName, setHeaderName] = useState("");
  const [secretReference, setSecretReference] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [approval, setApproval] = useState<"always" | "never">("always");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const dialogRef = useRef<HTMLElement>(null);
  const returnFocus = useRef<HTMLElement | null>(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const host = settingsOverlayHost();
  const draft: MCPFormDraft = { name, transport, command, args, cwd, inheritEnv, url, headerName, secretReference, enabled, approval };
  const dirty = isMCPDraftDirty(draft);

  useEffect(() => {
    form.current?.querySelector<HTMLInputElement>("input")?.focus();
    const previous = returnFocusTo ?? returnFocus.current;
    return () => { previous?.focus?.(); };
  }, [returnFocusTo]);

  useEffect(() => {
    const hostNode = settingsOverlayHost();
    const onCancel = (event: Event) => {
      event.preventDefault();
      event.stopImmediatePropagation();
      if (!saving) onClose();
    };
    hostNode?.addEventListener("cancel", onCancel, true);
    return () => hostNode?.removeEventListener("cancel", onCancel, true);
  }, [onClose, saving]);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const validation = validateMCPDraft(draft, language);
    if (validation) { setError(validation); return; }
    setSaving(true);
    setError("");
    try { await onSave(buildMCPMutation(draft)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setSaving(false); }
  };

  const onDialogKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      if (!saving) onClose();
      return;
    }
    if (event.key !== "Tab" || !dialogRef.current) return;
    const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>(mcpAddFocusable));
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable.at(-1)!;
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };

  const dialog = <div className="mcp-delete-backdrop" role="presentation" onMouseDown={(event) => {
    if (event.target !== event.currentTarget || saving || dirty) return;
    onClose();
  }}>
    <section ref={dialogRef} className="mcp-add-dialog" role="dialog" aria-modal="true" aria-labelledby="add-mcp-title" onKeyDown={onDialogKeyDown}>
      <header><div><span><Plus size={15} /></span><div><h2 id="add-mcp-title">{t("addMCPTitle")}</h2><p>{t("addMCPHint")}</p></div></div><button type="button" className="icon-button" onClick={onClose} aria-label={t("mcpCancel")} disabled={saving}><X size={17} /></button></header>
      <form ref={form} onSubmit={submit}>
        <div className="mcp-form-scroll">
          <label className="mcp-field"><span>{t("mcpName")}</span><input value={name} onChange={(event) => setName(event.target.value.toLowerCase())} placeholder="local-tools" autoComplete="off" /><small>{t("mcpNameHint")}</small></label>
          <fieldset className="mcp-field"><legend>{t("mcpTransport")}</legend><div className="mcp-transport-options"><button type="button" className={transport === "stdio" ? "active" : ""} onClick={() => setTransport("stdio")}><Terminal size={16} /><span><strong>stdio</strong><small>{t("mcpLocalProcess")}</small></span></button><button type="button" className={transport === "streamable_http" ? "active" : ""} onClick={() => setTransport("streamable_http")}><Globe2 size={16} /><span><strong>HTTP</strong><small>{t("mcpRemoteHTTP")}</small></span></button></div></fieldset>
          <MCPTransportFields language={language} transport={transport} command={command} setCommand={setCommand} args={args} setArgs={setArgs} cwd={cwd} setCWD={setCWD} inheritEnv={inheritEnv} setInheritEnv={setInheritEnv} url={url} setURL={setURL} headerName={headerName} setHeaderName={setHeaderName} secretReference={secretReference} setSecretReference={setSecretReference} />
          <fieldset className="mcp-field"><legend>{t("mcpApproval")}</legend><div className="mcp-approval-options"><button type="button" className={approval === "always" ? "active" : ""} onClick={() => setApproval("always")}><LockKeyhole size={14} /><span><strong>{t("mcpApprovalAlways")}</strong><small>{t("mcpApprovalHint")}</small></span></button><button type="button" className={approval === "never" ? "active" : ""} onClick={() => setApproval("never")}><Box size={14} /><span><strong>{t("mcpApprovalNever")}</strong><small>{language === "zh-CN" ? "仅用于你完全信任的服务" : "Only for services you fully trust"}</small></span></button></div></fieldset>
          <MCPBooleanRow label={t("mcpStartEnabled")} hint={t("mcpStartEnabledHint")} checked={enabled} onChange={() => setEnabled((value) => !value)} />
          <div className="mcp-security-notice"><AlertTriangle size={14} /><span>{t("mcpConfigNotice")}</span></div>
          {error && <div className="mcp-form-error" role="alert">{error}</div>}
        </div>
        <footer><button type="button" className="subtle-button" onClick={onClose} disabled={saving}>{t("mcpCancel")}</button><button type="submit" className="settings-primary" disabled={saving}>{saving && <RefreshCw className="spin" size={13} />}{t("mcpSave")}</button></footer>
      </form>
    </section>
  </div>;
  return host ? createPortal(dialog, host) : dialog;
}

function validateMCPDraft(draft: MCPFormDraft, language: Language) {
  const t = translator(language);
  if (!/^[a-z0-9_-]+$/.test(draft.name.trim())) return t("mcpValidationName");
  if (draft.transport === "stdio") return draft.command.trim() ? "" : t("mcpValidationCommand");
  return validateRemoteMCPDraft(draft, language);
}

function validateRemoteMCPDraft(draft: MCPFormDraft, language: Language) {
  const t = translator(language);
  let endpoint: URL;
  try { endpoint = new URL(draft.url); }
  catch { return t("mcpValidationURL"); }
  const local = ["localhost", "127.0.0.1", "::1"].includes(endpoint.hostname);
  if (endpoint.protocol !== "https:" && !(endpoint.protocol === "http:" && local)) return t("mcpValidationURL");
  const header = draft.headerName.trim();
  const secret = draft.secretReference.trim();
  if ((header && !/^(env|keyring):[^\s]+$/.test(secret)) || (!header && secret)) return t("mcpSecretHint");
  return "";
}

function buildMCPMutation(draft: MCPFormDraft): MCPServerMutation {
  const common = {
    name: draft.name.trim(), enabled: draft.enabled, transport: draft.transport, approval: draft.approval,
    maxConcurrency: 2, connectTimeout: "30s", callTimeout: "60s",
  };
  if (draft.transport === "stdio") return {
    ...common, command: draft.command.trim(), args: draft.args.split("\n").map((value) => value.trim()).filter(Boolean),
    cwd: draft.cwd.trim() || undefined, inheritEnv: draft.inheritEnv,
  };
  const header = draft.headerName.trim();
  return { ...common, url: draft.url.trim(), headers: header ? { [header]: draft.secretReference.trim() } : undefined };
}

function MCPTransportFields({ language, transport, command, setCommand, args, setArgs, cwd, setCWD, inheritEnv, setInheritEnv, url, setURL, headerName, setHeaderName, secretReference, setSecretReference }: {
  language: Language; transport: MCPServerMutation["transport"];
  command: string; setCommand: (value: string) => void; args: string; setArgs: (value: string) => void;
  cwd: string; setCWD: (value: string) => void; inheritEnv: boolean; setInheritEnv: (value: boolean) => void;
  url: string; setURL: (value: string) => void; headerName: string; setHeaderName: (value: string) => void;
  secretReference: string; setSecretReference: (value: string) => void;
}) {
  const t = translator(language);
  if (transport === "stdio") return <>
    <label className="mcp-field"><span>{t("mcpCommand")}</span><input value={command} onChange={(event) => setCommand(event.target.value)} placeholder={t("mcpCommandPlaceholder")} /></label>
    <label className="mcp-field"><span>{t("mcpArgs")}</span><textarea rows={4} value={args} onChange={(event) => setArgs(event.target.value)} placeholder={"-y\n@modelcontextprotocol/server-filesystem\n/path/to/workspace"} /><small>{t("mcpArgsHint")}</small></label>
    <label className="mcp-field"><span>{t("mcpCWD")}</span><input value={cwd} onChange={(event) => setCWD(event.target.value)} placeholder="/path/to/workspace" /></label>
    <MCPBooleanRow label={t("mcpInheritEnv")} hint={t("mcpInheritHint")} checked={inheritEnv} onChange={() => setInheritEnv(!inheritEnv)} />
  </>;
  return <>
    <label className="mcp-field"><span>{t("mcpURL")}</span><input type="url" value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://mcp.example.com" /><small>{t("mcpURLHint")}</small></label>
    <fieldset className="mcp-field mcp-auth-fields"><legend>{t("mcpAuthHeader")}</legend><div className="mcp-auth-pair"><label className="mcp-field"><span>{t("mcpHeaderName")}</span><input value={headerName} onChange={(event) => setHeaderName(event.target.value)} placeholder="Authorization" /></label><label className="mcp-field"><span>{t("mcpSecretReference")}</span><input value={secretReference} onChange={(event) => setSecretReference(event.target.value)} placeholder="env:MCP_TOKEN" /></label></div><small>{t("mcpSecretHint")}</small></fieldset>
  </>;
}

function MCPBooleanRow({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: () => void }) {
  return <div className="mcp-boolean-row"><div><strong>{label}</strong><small>{hint}</small></div><button type="button" role="switch" aria-checked={checked} className={`skill-state-switch ${checked ? "on" : ""}`} onClick={onChange}><i /></button></div>;
}
