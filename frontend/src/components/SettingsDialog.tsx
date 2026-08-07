import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft, Bot, Database, Gauge, Languages, Minus, Palette, Plus, RefreshCw, Search, Settings2, X,
} from "lucide-react";
import { execute, listSystemFonts, type SystemFont } from "../bridge";
import { reasoningLabel as i18nReasoningLabel, sortReasoningLevels, tFormat, translator, type Language } from "../i18n";
import { findModelOption, modelDisplayName, providerDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { DeliveryMode, ModelProvider, ModelRoute, ModelRouteConfig } from "../types";
import MenuSelect from "./MenuSelect";
import ModelProviderSettings from "./ModelProviderSettings";
import ProviderIcon from "./ProviderIcon";

type SettingsSection = "catalog" | "models" | "subagents" | "governance" | "appearance" | "extensions";

export default function SettingsDialog() {
  const dialog = useRef<HTMLDialogElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
	const roleModelRoutes = modelRoutes.filter((route) => route.Scope !== "main");
	const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const agentCatalog = useRuntimeStore((state) => state.agentCatalog);
  const skills = useRuntimeStore((state) => state.skills);
  const theme = useRuntimeStore((state) => state.theme);
  const uiFont = useRuntimeStore((state) => state.uiFont);
  const uiFontSize = useRuntimeStore((state) => state.uiFontSize);
  const approvalMode = useRuntimeStore((state) => state.approvalMode);
  const setTheme = useRuntimeStore((state) => state.setTheme);
  const setUIFont = useRuntimeStore((state) => state.setUIFont);
  const setUIFontSize = useRuntimeStore((state) => state.setUIFontSize);
  const setLanguage = useRuntimeStore((state) => state.setLanguage);
  const setQueueMode = useRuntimeStore((state) => state.setQueueMode);
  const setError = useRuntimeStore((state) => state.setError);
  const [activeSection, setActiveSection] = useState<SettingsSection>("models");
  const [query, setQuery] = useState("");
  const [concurrency, setConcurrency] = useState(snapshot.subagentConcurrency);
  const [systemFonts, setSystemFonts] = useState<SystemFont[]>([]);
  const t = translator(snapshot.language);
  const fontOptions = [
    { value: "system", label: t("systemFont") },
    ...systemFontOptions(uiFont, systemFonts),
  ];
  const close = () => useRuntimeStore.getState().setSettingsOpen(false);
  const sections: Array<{ id: SettingsSection; label: string; description: string; icon: typeof Bot }> = [
	{ id: "catalog", label: t("modelSettings"), description: t("settingsNavCatalog"), icon: Database },
    { id: "models", label: t("roleModels"), description: t("settingsNavModels"), icon: Bot },
    { id: "subagents", label: t("subagentRuntime"), description: t("settingsNavSubagents"), icon: Gauge },
    { id: "governance", label: t("settingsGovernance"), description: t("settingsNavGovernance"), icon: Settings2 },
    { id: "appearance", label: t("appearance"), description: t("settingsNavAppearance"), icon: Palette },
    { id: "extensions", label: t("settingsExtensions"), description: t("settingsNavExtensions"), icon: Languages },
  ];
  const filteredSections = sections.filter((section) => `${section.label}${section.description}`.toLowerCase().includes(query.trim().toLowerCase()));
  const current = sections.find((section) => section.id === activeSection)!;
  const descriptions = useMemo(() => new Map(agentCatalog.map((agent) => [agent.name, agent.description])), [agentCatalog]);
	const catalogPage: Partial<Record<SettingsSection, React.ReactNode>> = {
		catalog: <ModelProviderSettings providers={modelProviders} modelsByProvider={modelsByProvider} sessionId={snapshot.sessionId} language={snapshot.language} setError={setError} />,
	};

  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    previouslyFocused.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!node.open) node.showModal();
    requestAnimationFrame(() => {
      node.querySelector<HTMLElement>(".settings-back, button, input, [tabindex]:not([tabindex='-1'])")?.focus();
    });
    const onCancel = (event: Event) => {
      event.preventDefault();
      close();
    };
    node.addEventListener("cancel", onCancel);
    void Promise.all([
      execute({ kind: "list_model_routes", sessionId: snapshot.sessionId }),
      execute({ kind: "list_agent_types", sessionId: snapshot.sessionId }),
      execute({ kind: "list_models", sessionId: snapshot.sessionId }),
	  execute({ kind: "list_model_providers", sessionId: snapshot.sessionId }),
    ]).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
    return () => {
      node.removeEventListener("cancel", onCancel);
      if (node.open) node.close();
      previouslyFocused.current?.focus?.();
    };
  }, [setError, snapshot.sessionId]);

  useEffect(() => {
    void listSystemFonts(snapshot.language).then(setSystemFonts).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  }, [setError, snapshot.language]);

  useEffect(() => setConcurrency(snapshot.subagentConcurrency), [snapshot.subagentConcurrency]);

  const action = async (kind: string, target = "", route?: ModelRoute) => {
    try { await execute({ kind, target, route, sessionId: snapshot.sessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const changeQueueMode = async (queueMode: DeliveryMode) => {
    const previous = snapshot.queueMode;
    setQueueMode(queueMode);
    try { await execute({ kind: "set_queue_mode", target: queueMode, sessionId: snapshot.sessionId }); }
    catch (cause) {
      setQueueMode(previous);
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return <dialog ref={dialog} className="settings-dialog" aria-label={t("settings")}>
    <div className="settings-shell">
      <aside className="settings-sidebar">
        <button className="settings-back" onClick={close}><ArrowLeft size={15} />{t("backToApp")}</button>
        <label className="settings-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchSettings")} /></label>
        <div className="settings-nav-group"><span>Azem</span>{filteredSections.map((section) => <button key={section.id} className={activeSection === section.id ? "active" : ""} onClick={() => setActiveSection(section.id)}><section.icon size={15} /><span><strong>{section.label}</strong><small>{section.description}</small></span></button>)}</div>
      </aside>
      <main className="settings-main">
        <header className="settings-page-header"><div><h1>{current.label}</h1><p>{current.description}</p></div><button className="icon-button" onClick={close} aria-label={t("closeSettings")}><X size={17} /></button></header>
        <div className="settings-content">
		  {catalogPage[activeSection]}
          {activeSection === "models" && <SettingsPane title={t("settingsModels")} description={t("settingsModelsHint")} action={<button className="small-button" onClick={() => void action("list_model_routes")}><RefreshCw size={13} />{t("refresh")}</button>}>
            <div className="settings-card role-routes">
              {roleModelRoutes.length === 0 ? <div className="settings-empty"><span className="azem-mark" />{t("loadingRoles")}</div> : <>
                <div className="route-table-header" aria-hidden="true"><span /><div><span>{t("provider")}</span><span>{t("model")}</span><span>{t("reasoning")}</span><span /></div></div>
                {roleModelRoutes.map((route) => <RouteRow key={`${route.Scope}-${route.Role}`} route={route} description={descriptions.get(route.Role) || route.Label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}
              </>}
            </div>
          </SettingsPane>}
          {activeSection === "subagents" && <SettingsPane title={t("settingsSubagents")} description={t("settingsSubagentsHint")}>
            <div className="settings-card"><SettingRow label={t("concurrency")} description={t("concurrencyHint")}><input className="number-input" type="number" min={1} max={64} value={concurrency} onChange={(event) => setConcurrency(Number(event.target.value))} /><button className="small-button" onClick={() => void action("set_subagent_concurrency", String(concurrency))}>{t("save")}</button></SettingRow></div>
          </SettingsPane>}
          {activeSection === "governance" && <SettingsPane title={t("settingsGovernance")} description={t("settingsGovernanceHint")}>
            <div className="settings-card"><SettingRow label={t("defaultApprovalMode")} description={t("defaultApprovalModeHint")}><MenuSelect className="setting-menu" value={approvalMode} options={[{ value: "prompt", label: t("promptApproval") }, { value: "auto_review", label: t("autoReview") }, { value: "yolo", label: t("yolo") }]} onChange={(value) => void action("set_approval_mode", value)} ariaLabel={t("defaultApprovalMode")} /></SettingRow><SettingRow label={t("runningMessageMode")} description={t("runningMessageModeHint")}><MenuSelect className="setting-menu" value={snapshot.queueMode} options={[{ value: "queue", label: t("queue") }, { value: "guide", label: t("guide") }]} onChange={(value) => void changeQueueMode(value as DeliveryMode)} ariaLabel={t("runningMessageMode")} /></SettingRow></div>
          </SettingsPane>}
          {activeSection === "appearance" && <SettingsPane title={t("settingsAppearance")} description={t("settingsAppearanceHint")}>
            <div className="settings-card">
              <SettingRow label={t("language")} description={t("languageHint")}><MenuSelect className="setting-menu" value={snapshot.language} options={[{ value: "zh-CN", label: t("langZh") }, { value: "en", label: t("langEn") }]} onChange={(value) => { const language = value as "en" | "zh-CN"; setLanguage(language); void action("set_language", language); }} ariaLabel={t("language")} /></SettingRow>
              <SettingRow label={t("theme")} description={t("themeHint")}><MenuSelect className="setting-menu" value={theme} options={[{ value: "system", label: t("system") }, { value: "light", label: t("light") }, { value: "dark", label: t("dark") }]} onChange={(value) => setTheme(value as "light" | "dark" | "system")} ariaLabel={t("theme")} /></SettingRow>
              <SettingRow label={t("interfaceFont")} description={t("interfaceFontHint")}><MenuSelect className="setting-menu font-family-menu" value={uiFont} options={fontOptions} onChange={setUIFont} ariaLabel={t("interfaceFont")} fit="full" searchable searchPlaceholder={t("searchFonts")} emptyLabel={t("noMatchingFonts")} /></SettingRow>
              <SettingRow label={t("interfaceFontSize")} description={t("interfaceFontSizeHint")}><div className="font-size-control"><button type="button" onClick={() => setUIFontSize(uiFontSize - 1)} disabled={uiFontSize <= 11} aria-label={t("decreaseFontSize")}><Minus size={13} /></button><output aria-live="polite">{uiFontSize} px</output><button type="button" onClick={() => setUIFontSize(uiFontSize + 1)} disabled={uiFontSize >= 20} aria-label={t("increaseFontSize")}><Plus size={13} /></button><button type="button" className="text-button" onClick={() => setUIFontSize(14)} disabled={uiFontSize === 14}>{t("resetFontSize")}</button></div></SettingRow>
            </div>
          </SettingsPane>}
          {activeSection === "extensions" && <SettingsPane title={t("settingsExtensions")} description={t("settingsExtensionsHint")} action={<button className="small-button" onClick={() => void action("reload_skills")}><RefreshCw size={13} />{t("reload")}</button>}>
            <div className="settings-card skill-settings">{skills.length === 0 ? <div className="settings-empty">{t("noSkills")}</div> : skills.map((skill) => <div className="mini-skill" key={skill.name}><span><strong>{skill.name}</strong><small>{skill.description}</small></span><em>{skill.disabled ? t("skillDisabled") : skill.eager ? t("skillEager") : t("skillOnDemand")}</em></div>)}</div>
          </SettingsPane>}
        </div>
      </main>
    </div>
  </dialog>;
}

function SettingsPane({ title, description, action, children }: { title: string; description: string; action?: React.ReactNode; children: React.ReactNode }) {
  return <section className="settings-pane"><header><div><h2>{title}</h2><p>{description}</p></div>{action}</header>{children}</section>;
}

function RouteRow({ route, description, modelsByProvider, modelProviders, action, language }: {
  route: ModelRoute;
  description: string;
  modelsByProvider: Record<string, ModelOption[]>;
  modelProviders: ModelProvider[];
  action: (kind: string, target?: string, route?: ModelRoute) => Promise<void>;
  language: Language;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const t = translator(language);
  const [value, setValue] = useState<ModelRouteConfig>({ ...route.Route });
  useEffect(() => setValue({ ...route.Route }), [route.Route.model, route.Route.provider, route.Route.reasoning]);

  const provider = value.provider || snapshot.provider;
  const providerModels = modelsByProvider[provider] ?? [];
  const model = value.model || (provider === snapshot.provider ? snapshot.model : providerModels[0]?.id || "");
  const modelInfo = findModelOption(providerModels, model);
  const reasoning = value.reasoning || modelInfo?.defaultReasoning || snapshot.reasoning;
  const inherited = !value.provider && !value.model && !value.reasoning;
  const title = routeTitle(route, language);
  const providerOptions = unique([snapshot.provider, value.provider, ...Object.keys(modelsByProvider)]).map((id) => ({ value: id, label: providerDisplayName(id, modelProviders), icon: <ProviderIcon provider={id} /> }));
  const modelOptions = unique([model, ...providerModels.map((item) => item.id)]).map((id) => {
	const info = providerModels.find((item) => item.id === id);
	return { value: id, label: modelDisplayName(id, info?.name), keywords: info?.aliases ?? [], icon: <ProviderIcon provider={provider} /> };
  });
  const reasoningOptions = sortReasoningLevels(modelInfo?.reasoningLevels.length ? modelInfo.reasoningLevels : [reasoning].filter(Boolean)).map((level) => ({ value: level, label: i18nReasoningLabel(level, language) }));
  const setProvider = (next: string) => {
    const nextModel = modelsByProvider[next]?.[0];
    setValue({ provider: next, model: next === snapshot.provider ? snapshot.model : nextModel?.id || "", reasoning: nextModel?.defaultReasoning || snapshot.reasoning });
  };
  const setModel = (next: string) => {
    const nextModel = providerModels.find((item) => item.id === next);
    setValue((current) => ({ ...current, provider, model: next, reasoning: current.reasoning || nextModel?.defaultReasoning || snapshot.reasoning }));
  };
  const setReasoning = (next: string) => setValue((current) => ({ ...current, provider, model, reasoning: next }));
  const save = () => action("set_model_route", "", { ...route, Route: { provider, model, reasoning } });
  const reset = () => {
    setValue({});
    void action("reset_model_route", "", route);
  };

  return <div className="route-row">
    <div className="route-copy"><div><strong>{title}</strong><span>{inherited ? t("routeInherited") : t("routeCustom")}</span></div><small>{routeDescription(route, description, language)}</small></div>
    <div className="route-controls">
      <label><span>{t("provider")}</span><MenuSelect className="route-menu" value={provider} options={providerOptions} onChange={setProvider} ariaLabel={`${title} ${t("provider")}`} /></label>
      <label><span>{t("model")}</span><MenuSelect className="route-menu route-model-menu" value={model} options={modelOptions} onChange={setModel} ariaLabel={`${title} ${t("model")}`} searchable searchPlaceholder={t("searchModels")} emptyLabel={t("noMatchingModels")} /></label>
      <label><span>{t("reasoning")}</span><MenuSelect className="route-menu" value={reasoning} options={reasoningOptions} onChange={setReasoning} ariaLabel={`${title} ${t("reasoning")}`} /></label>
      <div className="route-actions"><button className="small-button" disabled={!provider || !model} onClick={() => void save()}>{t("save")}</button>{route.Scope !== "main" && <button className="text-button" disabled={inherited} onClick={reset}>{t("resetInherit")}</button>}</div>
    </div>
  </div>;
}

function SettingRow({ label, description, children }: { label: string; description: string; children: React.ReactNode }) { return <div className="setting-row"><div><strong>{label}</strong><p>{description}</p></div><div>{children}</div></div>; }
function systemFontOptions(selected: string, fonts: SystemFont[]) {
  const options = new Map(fonts.map((font) => [font.family, { value: font.family, label: font.label || font.family }]));
  if (selected !== "system" && !options.has(selected)) options.set(selected, { value: selected, label: selected });
  return [...options.values()];
}
function routeTitle(route: ModelRoute, language: Language) {
  const t = translator(language);
	if (route.Scope === "main") return t("routeMain");
  if (route.Scope === "title") return t("routeTitle");
  if (route.Scope === "plan") return t("routePlan");
  if (route.Scope === "compaction") return t("routeCompaction");
  return route.Role || route.Label;
}
function routeDescription(route: ModelRoute, description: string, language: Language) {
  const t = translator(language);
	if (route.Scope === "main") return t("routeMainHint");
  if (route.Scope === "title") return t("routeTitleHint");
  if (route.Scope === "plan") return t("routePlanHint");
  if (route.Scope === "compaction") return t("routeCompactionHint");
  return description || tFormat(language, "routeSubagentHint", { role: route.Role || route.Label });
}
function unique(values: Array<string | undefined>) { return [...new Set(values.filter((value): value is string => Boolean(value)))]; }
