import { useEffect, useRef, useState } from "react";
import {
  AlertTriangle, AppWindow, Box, Globe2, LockKeyhole, PackageOpen,
  Plug, Plus, RefreshCw, RotateCw, Server, Sparkles, Terminal, Trash2, Wrench, X,
} from "lucide-react";
import { tFormat, translator, type Language } from "../i18n";
import { listSkillCatalog } from "../bridge";
import { useRuntimeStore } from "../store";
import type { ActionRequest, MCPServerEntry, MCPServerMutation } from "../types";
import SkillCatalogManager from "./SkillCatalogManager";

type ExtensionTab = "mcp" | "skills" | "plugins";

interface ExtensionsSettingsProps {
  language: Language;
  sessionId: string;
  openAddRequest?: number;
  executeAction: (request: ActionRequest) => Promise<void>;
  onError: (message: string) => void;
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
}: ExtensionsSettingsProps) {
  const t = translator(language);
  const mcpServers = useRuntimeStore((state) => state.mcpServers);
  const skills = useRuntimeStore((state) => state.skills);
  const plugins = useRuntimeStore((state) => state.plugins);
  const [tab, setTab] = useState<ExtensionTab>("mcp");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const previousAddRequest = useRef(openAddRequest);
  const enabledSkills = skills.filter((skill) => !skill.disabled).length;
  const enabledPlugins = plugins.filter((plugin) => plugin.enabled && plugin.status !== "invalid").length;
  const connectedMCP = mcpServers.filter((server) => server.enabled && server.state === "ready").length;

  useEffect(() => {
    if (openAddRequest > previousAddRequest.current) {
      setTab("mcp");
      setDrawerOpen(true);
    }
    previousAddRequest.current = openAddRequest;
  }, [openAddRequest]);

  const run = async (request: ActionRequest) => {
    try {
      await executeAction({ ...request, sessionId });
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
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

  return <section className="extension-hub" aria-label={t("extensionOverview")}>
    <div className="extension-overview">
      <div className="extension-overview-copy">
        <span className="extension-overview-mark"><Plug size={17} /></span>
        <div><strong>{t("extensionOverview")}</strong><small>{t("extensionMCPHint")}</small></div>
      </div>
      <div className="extension-stat-grid">
        <ExtensionMetric value={`${connectedMCP} / ${mcpServers.length}`} label={t("mcpConnectedServicesMetric")} tone="green" />
        <ExtensionMetric value={`${enabledSkills}/${skills.length}`} label={t("extensionSkillsMetric")} />
        <ExtensionMetric value={`${enabledPlugins}/${plugins.length}`} label={t("extensionPluginsMetric")} />
      </div>
    </div>

    <div className="extension-tabbar" role="tablist" aria-label={t("extensionOverview")}>
      <ExtensionTabButton active={tab === "mcp"} icon={Server} label={t("extensionTabMCP")} count={mcpServers.length} onClick={() => setTab("mcp")} />
      <ExtensionTabButton active={tab === "skills"} icon={Sparkles} label={t("extensionTabSkills")} count={skills.length} onClick={() => setTab("skills")} />
      <ExtensionTabButton active={tab === "plugins"} icon={Plug} label={t("extensionTabPlugins")} count={plugins.length} onClick={() => setTab("plugins")} />
    </div>

    <ExtensionContent tab={tab} language={language} mcpServers={mcpServers} skills={skills} plugins={plugins} run={run} refreshSkills={refreshSkills} openDrawer={() => setDrawerOpen(true)} />

    {drawerOpen && <AddMCPDrawer language={language} onClose={() => setDrawerOpen(false)} onSave={async (payload) => {
      await run({ kind: "upsert_mcp_server", payload });
      setDrawerOpen(false);
    }} />}
  </section>;
}

function ExtensionMetric({ value, label, tone = "neutral" }: { value: React.ReactNode; label: string; tone?: "neutral" | "blue" | "green" }) {
  return <article data-tone={tone}><strong>{value}</strong><small>{label}</small></article>;
}

function ExtensionTabButton({ active, icon: Icon, label, count, onClick }: { active: boolean; icon: typeof Server; label: string; count: number; onClick: () => void }) {
  return <button type="button" role="tab" aria-selected={active} className={active ? "active" : ""} onClick={onClick}><Icon size={14} /><span>{label}</span><em>{count}</em></button>;
}

function ExtensionContent({ tab, language, mcpServers, skills, plugins, run, refreshSkills, openDrawer }: {
  tab: ExtensionTab;
  language: Language;
  mcpServers: ReturnType<typeof useRuntimeStore.getState>["mcpServers"];
  skills: ReturnType<typeof useRuntimeStore.getState>["skills"];
  plugins: ReturnType<typeof useRuntimeStore.getState>["plugins"];
  run: (request: ActionRequest) => Promise<void>;
  refreshSkills: () => Promise<void>;
  openDrawer: () => void;
}) {
  if (tab === "mcp") return <MCPServicesPanel language={language} servers={mcpServers} run={run} openDrawer={openDrawer} />;
  if (tab === "skills") return <section className="extension-panel" role="tabpanel"><SkillCatalogManager skills={skills} language={language} onReload={refreshSkills} onSetEnabled={(name, enabled) => run({ kind: "set_skill_enabled", target: name, decision: String(enabled) })} /></section>;
  return <PluginsPanel language={language} plugins={plugins} run={run} />;
}

function MCPServicesPanel({ language, servers, run, openDrawer }: {
  language: Language;
  servers: MCPServerEntry[];
  run: (request: ActionRequest) => Promise<void>;
  openDrawer: () => void;
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
    <header className="extension-panel-header"><div><h2>{t("extensionMCPTitle")}</h2><p>{t("mcpRuntimeHint")}</p></div><div className="extension-panel-actions"><button type="button" className="subtle-button" onClick={() => void run({ kind: "refresh_mcp" })}><RefreshCw size={13} />{t("refreshMCP")}</button><button type="button" className="primary-button" onClick={openDrawer}><Plus size={13} />{t("addMCPServer")}</button></div></header>
    {servers.length === 0 ? <ExtensionEmpty icon={Server} title={t("noMCPServers")} hint={t("noMCPServersHint")} action={<button type="button" className="primary-button" onClick={openDrawer}><Plus size={13} />{t("addMCPServer")}</button>} /> : <div className="mcp-server-list">{servers.map((server) => <MCPServerRow key={server.name} server={server} language={language} pending={pendingServers.has(server.name)} onToggle={() => void setEnabled(server)} onReconnect={() => void run({ kind: "reconnect_mcp", target: server.name })} onDelete={() => setDeleteTarget(server)} />)}</div>}
    </section>
    {deleteTarget && <DeleteMCPDialog server={deleteTarget} language={language} pending={pendingServers.has(deleteTarget.name)} onCancel={() => setDeleteTarget(null)} onConfirm={() => void remove(deleteTarget)} />}
  </>;
}

function PluginsPanel({ language, plugins, run }: { language: Language; plugins: ReturnType<typeof useRuntimeStore.getState>["plugins"]; run: (request: ActionRequest) => Promise<void> }) {
  const t = translator(language);
	const [pending, setPending] = useState<Set<string>>(new Set());
	const chooseImport = async (plugin: typeof plugins[number], imported: boolean) => {
		setPending((current) => new Set(current).add(plugin.id));
		try {
			await run({ kind: "set_plugin_imported", target: plugin.id, decision: String(imported) });
		} finally {
			setPending((current) => {
				const next = new Set(current);
				next.delete(plugin.id);
				return next;
			});
		}
	};
  return <section className="extension-panel" role="tabpanel">
    <header className="extension-panel-header"><div><h2>{t("extensionPluginsTitle")}</h2><p>{t("extensionPluginsHint")}</p></div></header>
    {plugins.length === 0 ? <ExtensionEmpty icon={PackageOpen} title={t("noPluginsInstalled")} hint={t("noPluginsInstalledHint")} /> : <div className="plugin-grid extension-plugin-grid">{plugins.map((plugin) => <PluginCard key={plugin.id} plugin={plugin} language={language} pending={pending.has(plugin.id)} onImport={(imported) => void chooseImport(plugin, imported)} />)}</div>}
  </section>;
}

function PluginCard({ plugin, language, pending, onImport }: { plugin: ReturnType<typeof useRuntimeStore.getState>["plugins"][number]; language: Language; pending: boolean; onImport: (imported: boolean) => void }) {
  const disabled = !plugin.enabled || plugin.status === "invalid";
	const canImport = plugin.origin === "codex_available";
	const canStopImport = plugin.origin === "codex";
	const icon = plugin.logoPath.startsWith("data:image/")
		? <img className="plugin-logo" src={plugin.logoPath} alt="" />
		: <Plug size={16} />;
  return <article className={`plugin-card ${disabled ? "disabled" : ""}`}>
    <div className="plugin-card-heading"><span className="plugin-mark" style={{ "--plugin-color": plugin.brandColor || "var(--muted)" } as React.CSSProperties}>{icon}</span><div><strong>{plugin.displayName || plugin.name}</strong><small>{pluginOriginLabel(plugin.origin, plugin.marketplace, plugin.version, language)}</small></div><em>{pluginStatusLabel(plugin.enabled, plugin.status, language)}</em></div>
    <p>{plugin.description}</p>
    <PluginCapabilities plugin={plugin} language={language} />
    {plugin.warning && <small className="plugin-warning">{plugin.warning}</small>}
		{(canImport || canStopImport) && <button type="button" className="subtle-button plugin-import-button" disabled={pending} onClick={() => onImport(canImport)}>{pending ? (language === "zh-CN" ? "保存中…" : "Saving…") : canImport ? (language === "zh-CN" ? "导入到 Azem" : "Import into Azem") : (language === "zh-CN" ? "停止导入" : "Stop importing")}</button>}
  </article>;
}

function pluginOriginLabel(origin: string, marketplace: string, version: string, language: Language) {
  const source = origin === "codex" || origin === "codex_available"
    ? (language === "zh-CN" ? `从 Codex 导入 · ${marketplace}` : `Imported from Codex · ${marketplace}`)
    : (language === "zh-CN" ? "Azem 目录" : "Azem directory");
  return version ? `${source} · ${version}` : source;
}

function pluginStatusLabel(enabled: boolean, status: string, language: Language) {
	if (status === "available") return language === "zh-CN" ? "可导入" : "Available";
	if (status === "restart_required") return language === "zh-CN" ? "重启后生效" : "Restart required";
  if (!enabled) return language === "zh-CN" ? "已停用" : "Disabled";
  if (status === "ready") return language === "zh-CN" ? "已接入" : "Connected";
  return language === "zh-CN" ? "部分接入" : "Partial";
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
    <span className="mcp-server-icon" data-ready={ready}><TransportIcon size={17} /></span>
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
    {server.removable && <button type="button" className="mcp-delete" onClick={onDelete} aria-label={deleteLabel} title={deleteLabel} disabled={pending}><Trash2 size={14} /></button>}
    {server.enabled && <button type="button" className="mcp-reconnect" onClick={onReconnect} aria-label={reconnectLabel} title={reconnectLabel} disabled={pending}><RotateCw size={14} /></button>}
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

function ExtensionEmpty({ icon: Icon, title, hint, action }: { icon: typeof Server; title: string; hint: string; action?: React.ReactNode }) {
  return <div className="extension-empty"><span><Icon size={20} /></span><strong>{title}</strong><small>{hint}</small>{action}</div>;
}

function AddMCPDrawer({ language, onClose, onSave }: { language: Language; onClose: () => void; onSave: (payload: MCPServerMutation) => Promise<void> }) {
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

  useEffect(() => form.current?.querySelector<HTMLInputElement>("input")?.focus(), []);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const draft: MCPFormDraft = { name, transport, command, args, cwd, inheritEnv, url, headerName, secretReference, enabled, approval };
    const validation = validateMCPDraft(draft, language);
    if (validation) { setError(validation); return; }
    setSaving(true);
    setError("");
    try { await onSave(buildMCPMutation(draft)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setSaving(false); }
  };

  return <div className="mcp-drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <aside className="mcp-drawer" role="dialog" aria-modal="true" aria-labelledby="add-mcp-title">
      <header><div><span><Plus size={15} /></span><div><h2 id="add-mcp-title">{t("addMCPTitle")}</h2><p>{t("addMCPHint")}</p></div></div><button type="button" className="icon-button" onClick={onClose} aria-label={t("mcpCancel")}><X size={17} /></button></header>
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
        <footer><button type="button" className="subtle-button" onClick={onClose}>{t("mcpCancel")}</button><button type="submit" className="settings-primary" disabled={saving}>{saving && <RefreshCw className="spin" size={13} />}{t("mcpSave")}</button></footer>
      </form>
    </aside>
  </div>;
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
    <fieldset className="mcp-auth-fields"><legend>{t("mcpAuthHeader")}</legend><label><span>{t("mcpHeaderName")}</span><input value={headerName} onChange={(event) => setHeaderName(event.target.value)} placeholder="Authorization" /></label><label><span>{t("mcpSecretReference")}</span><input value={secretReference} onChange={(event) => setSecretReference(event.target.value)} placeholder="env:MCP_TOKEN" /></label><small>{t("mcpSecretHint")}</small></fieldset>
  </>;
}

function MCPBooleanRow({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: () => void }) {
  return <div className="mcp-boolean-row"><div><strong>{label}</strong><small>{hint}</small></div><button type="button" role="switch" aria-checked={checked} className={`skill-state-switch ${checked ? "on" : ""}`} onClick={onChange}><i /></button></div>;
}
