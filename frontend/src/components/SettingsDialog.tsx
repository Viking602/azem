import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft, Bot, Check, CornerDownRight, Database, Gauge, Hand, Languages, List, Minus, Palette, Plus,
  RefreshCw, Search, Settings2, ShieldAlert, ShieldCheck, X,
} from "lucide-react";
import { execute, listSystemFonts, type SystemFont } from "../bridge";
import { reasoningLabel, sortReasoningLevels, tFormat, translator, type Language } from "../i18n";
import { findModelOption, modelDisplayName, providerDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { DeliveryMode, ModelProvider, ModelRoute, ModelRouteConfig } from "../types";
import MenuSelect from "./MenuSelect";
import ModelProviderSettings from "./ModelProviderSettings";
import ProviderIcon from "./ProviderIcon";
import ExtensionsSettings from "./ExtensionsSettings";

type SettingsSection = "catalog" | "models" | "subagents" | "governance" | "appearance" | "extensions";

export default function SettingsDialog() {
  const dialog = useRef<HTMLDialogElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
  const coreModelRoutes = modelRoutes.filter((route) => route.Scope !== "subagent" && route.Scope !== "main");
	const subagentModelRoutes = modelRoutes.filter((route) => route.Scope === "subagent");
  const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const catalogModelCount = modelProviders.reduce((total, provider) => total + provider.Models.length, 0);
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const agentCatalog = useRuntimeStore((state) => state.agentCatalog);
  const plugins = useRuntimeStore((state) => state.plugins);
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
  const [activeSection, setActiveSection] = useState<SettingsSection>("catalog");
  const [query, setQuery] = useState("");
  const [concurrency, setConcurrency] = useState(snapshot.subagentConcurrency);
  const [shellConcurrency, setShellConcurrency] = useState(snapshot.shellConcurrency ?? 2);
  const [awaitSeconds, setAwaitSeconds] = useState(snapshot.subagentAwaitSeconds ?? 600);
  const [addProviderRequest, setAddProviderRequest] = useState(0);
  const [addMCPRequest, setAddMCPRequest] = useState(0);
  const [systemFonts, setSystemFonts] = useState<SystemFont[]>([]);
  const [reducedMotion, setReducedMotion] = useState(() => localStorage.getItem("azem-reduced-motion") === "true");
  const t = translator(snapshot.language);
  const fontOptions = [
    { value: "system", label: t("systemFont"), caption: snapshot.language === "zh-CN" ? "SF Pro Text · 苹方" : "SF Pro Text · PingFang" },
    ...systemFontOptions(uiFont, systemFonts),
  ];
  const close = () => useRuntimeStore.getState().setSettingsOpen(false);
  const sections: Array<{ id: SettingsSection; label: string; description: string; icon: typeof Bot }> = [
	{ id: "catalog", label: t("modelSettings"), description: t("modelSettingsHint"), icon: Database },
    { id: "models", label: t("roleModels"), description: t("settingsModelsHint"), icon: Bot },
    { id: "subagents", label: t("subagentRuntime"), description: t("settingsSubagentsHint"), icon: Gauge },
    { id: "governance", label: t("settingsGovernance"), description: t("settingsGovernanceHint"), icon: Settings2 },
    { id: "appearance", label: t("appearance"), description: t("settingsAppearanceHint"), icon: Palette },
    { id: "extensions", label: t("settingsExtensions"), description: t("settingsExtensionsHint"), icon: Languages },
  ];
  const filteredSections = sections.filter((section) => `${section.label}${section.description}`.toLowerCase().includes(query.trim().toLowerCase()));
  const current = sections.find((section) => section.id === activeSection)!;
  const descriptions = useMemo(() => new Map(agentCatalog.map((agent) => [agent.name, agent.description])), [agentCatalog]);
	const catalogPage: Partial<Record<SettingsSection, React.ReactNode>> = {
		catalog: <ModelProviderSettings providers={modelProviders} modelsByProvider={modelsByProvider} sessionId={snapshot.sessionId} language={snapshot.language} setError={setError} addRequest={addProviderRequest} />,
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
	  execute({ kind: "list_skills", sessionId: snapshot.sessionId }),
	  execute({ kind: "refresh_mcp", sessionId: snapshot.sessionId }),
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
  useEffect(() => setShellConcurrency(snapshot.shellConcurrency ?? 2), [snapshot.shellConcurrency]);
  useEffect(() => setAwaitSeconds(snapshot.subagentAwaitSeconds ?? 600), [snapshot.subagentAwaitSeconds]);
  useEffect(() => {
    document.documentElement.dataset.reduceMotion = String(reducedMotion);
    localStorage.setItem("azem-reduced-motion", String(reducedMotion));
  }, [reducedMotion]);

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
        <label className="settings-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchSettings")} /><kbd>⌘F</kbd></label>
        <div className="settings-nav-group">
          <span>{snapshot.language === "zh-CN" ? "系统" : "System"}</span>
          {filteredSections.filter((section) => ["catalog", "models", "subagents"].includes(section.id)).map((section) => <button key={section.id} className={activeSection === section.id ? "active" : ""} onClick={() => setActiveSection(section.id)}><section.icon size={15} /><span><strong>{section.label}</strong><small>{section.description}</small></span>{section.id === "catalog" && catalogModelCount > 0 ? <em>{catalogModelCount}</em> : null}</button>)}
          <span>{snapshot.language === "zh-CN" ? "偏好" : "Preferences"}</span>
          {filteredSections.filter((section) => ["governance", "appearance", "extensions"].includes(section.id)).map((section) => <button key={section.id} className={activeSection === section.id ? "active" : ""} onClick={() => setActiveSection(section.id)}><section.icon size={15} /><span><strong>{section.label}</strong><small>{section.description}</small></span>{section.id === "extensions" && plugins.length > 0 ? <em>{plugins.length}</em> : null}</button>)}
        </div>
        <footer><small>{snapshot.language === "zh-CN" ? "配置自动保存到本机" : "Saved locally"}</small><small>Azem v0.8.0</small></footer>
      </aside>
      <main className="settings-main" data-section={activeSection}>
        <header className="settings-page-header"><div><h1>{current.label}</h1><p>{current.description}</p></div>{activeSection === "catalog" && <button className="settings-primary" onClick={() => setAddProviderRequest((value) => value + 1)}><Plus size={13} />{snapshot.language === "zh-CN" ? "添加提供方" : "Add provider"}</button>}{activeSection === "models" && <span className="settings-valid"><i />{snapshot.language === "zh-CN" ? "配置有效" : "Valid configuration"}</span>}{activeSection === "extensions" && <button className="settings-primary" onClick={() => setAddMCPRequest((value) => value + 1)}><Plus size={13} />{t("addMCPServer")}</button>}<button className="icon-button settings-close" onClick={close} aria-label={t("closeSettings")}><X size={17} /></button></header>
        <div className="settings-content">
		  {catalogPage[activeSection]}
          {activeSection === "models" && <SettingsPane title={t("settingsModels")} description={t("settingsModelsHint")} className="model-routes-pane" action={<button className="small-button" onClick={() => void action("list_model_routes")}><RefreshCw size={13} />{t("refresh")}</button>}>
            {modelRoutes.length === 0 ? <div className="settings-card settings-empty"><span className="azem-mark" />{t("loadingRoles")}</div> : <div className="route-groups">
              <section className="settings-card route-card"><header><div><strong>{snapshot.language === "zh-CN" ? "核心工作流" : "Core workflows"}</strong><small>{snapshot.language === "zh-CN" ? "主会话与关键判断" : "Main conversation and critical decisions"}</small></div></header>{coreModelRoutes.map((route) => <RouteRow key={`${route.Scope}-${route.Role}`} route={route} description={descriptions.get(route.Role) || route.Label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}</section>
              <section className="settings-card route-card"><header><div><strong>{snapshot.language === "zh-CN" ? "子智能体默认模型" : "Subagent defaults"}</strong><small>{snapshot.language === "zh-CN" ? "角色仍可在定义中覆盖此配置" : "Roles can still override this setting"}</small></div></header>{subagentModelRoutes.map((route) => <RouteRow key={`${route.Scope}-${route.Role}`} route={route} description={descriptions.get(route.Role) || route.Label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}</section>
            </div>}
          </SettingsPane>}
          {activeSection === "subagents" && <SettingsPane title={t("settingsSubagents")} description={t("settingsSubagentsHint")} className="subagent-settings-pane">
            <div className="subagent-capacity-grid" aria-label={snapshot.language === "zh-CN" ? "并发与超时" : "Concurrency and timeout"}>
              <CapacityControl label={snapshot.language === "zh-CN" ? "子智能体并发" : "Subagent concurrency"} description={snapshot.language === "zh-CN" ? "团队成员上限" : "Team member limit"}><CompactStepper value={concurrency} min={1} max={64} decrease={() => { const value = Math.max(1, concurrency - 1); setConcurrency(value); void action("set_subagent_concurrency", String(value)); }} increase={() => { const value = Math.min(64, concurrency + 1); setConcurrency(value); void action("set_subagent_concurrency", String(value)); }} /></CapacityControl>
              <CapacityControl label={snapshot.language === "zh-CN" ? "Shell 并发" : "Shell concurrency"} description={snapshot.language === "zh-CN" ? "本地命令独立容量" : "Independent command capacity"}><CompactStepper value={shellConcurrency} min={1} max={16} decrease={() => { const value = Math.max(1, shellConcurrency - 1); setShellConcurrency(value); void action("set_shell_concurrency", String(value)); }} increase={() => { const value = Math.min(16, shellConcurrency + 1); setShellConcurrency(value); void action("set_shell_concurrency", String(value)); }} /></CapacityControl>
              <CapacityControl label={snapshot.language === "zh-CN" ? "启动超时" : "Start timeout"} description={snapshot.language === "zh-CN" ? "资源与租约等待上限" : "Resource and lease wait limit"}><MenuSelect className="capacity-timeout-menu" value={String(awaitSeconds)} options={[30, 60, 300, 600, 1800].map((seconds) => ({ value: String(seconds), label: seconds < 60 ? `${seconds} ${snapshot.language === "zh-CN" ? "秒" : "sec"}` : `${seconds / 60} ${snapshot.language === "zh-CN" ? "分钟" : "min"}` }))} onChange={(value) => { const seconds = Number(value); setAwaitSeconds(seconds); void action("set_subagent_await_timeout", value); }} ariaLabel={snapshot.language === "zh-CN" ? "启动超时" : "Start timeout"} /></CapacityControl>
            </div>
            <div className="settings-card subagent-scheduling">
              <header><div><strong>{snapshot.language === "zh-CN" ? "调度策略" : "Scheduling policy"}</strong><small>{snapshot.language === "zh-CN" ? "当前定义使用并行工具分发" : "Definitions use parallel tool dispatch"}</small></div><em><i />Parallel</em></header>
              <RuntimeInvariant label={snapshot.language === "zh-CN" ? "在主会话中显示子智能体进度" : "Show subagent progress in the main conversation"} hint={snapshot.language === "zh-CN" ? "状态摘要投影到当前任务，不混入最终回答" : "Project state summaries without mixing them into the final answer"} />
              <RuntimeInvariant label={snapshot.language === "zh-CN" ? "完成后保留结果卡片" : "Keep result cards after completion"} hint={snapshot.language === "zh-CN" ? "会话重开后仍可检查任务、耗时和输出" : "Inspect tasks, duration, and output after reopening"} />
              <RuntimeInvariant label={snapshot.language === "zh-CN" ? "资源不足时排队" : "Queue when capacity is unavailable"} hint={snapshot.language === "zh-CN" ? "保持 queued 状态，不提前显示为运行中" : "Keep queued state without presenting it as running"} />
            </div>
          </SettingsPane>}
          {activeSection === "governance" && <SettingsPane title={t("settingsGovernance")} description={t("settingsGovernanceHint")} className="governance-pane">
            <div className="settings-card governance-settings">
              <GovernanceRow label={t("defaultApprovalMode")} description={snapshot.language === "zh-CN" ? "控制工具执行边界" : "Control tool execution boundaries"}>
                <div className="governance-options governance-options-three" role="radiogroup" aria-label={t("defaultApprovalMode")}>
                  <GovernanceOption icon={Hand} label={t("promptApproval")} hint={snapshot.language === "zh-CN" ? "逐次确认" : "Confirm each time"} selected={approvalMode === "prompt"} onClick={() => void action("set_approval_mode", "prompt")} />
                  <GovernanceOption icon={ShieldCheck} label={t("autoReview")} hint={snapshot.language === "zh-CN" ? "策略判断" : "Policy review"} badge={snapshot.language === "zh-CN" ? "推荐" : "Recommended"} selected={approvalMode === "auto_review"} onClick={() => void action("set_approval_mode", "auto_review")} />
                  <GovernanceOption icon={ShieldAlert} label={t("yolo")} hint={snapshot.language === "zh-CN" ? "硬性边界" : "Hard boundaries"} selected={approvalMode === "yolo"} onClick={() => void action("set_approval_mode", "yolo")} />
                </div>
              </GovernanceRow>
              <GovernanceRow label={t("runningMessageMode")} description={snapshot.language === "zh-CN" ? "收到补充输入时" : "When follow-up input arrives"}>
                <div className="governance-options governance-options-two" role="radiogroup" aria-label={t("runningMessageMode")}>
                  <GovernanceOption icon={List} label={snapshot.language === "zh-CN" ? "加入队列" : t("queue")} hint={snapshot.language === "zh-CN" ? "按顺序处理" : "Process in order"} selected={snapshot.queueMode === "queue"} onClick={() => void changeQueueMode("queue")} />
                  <GovernanceOption icon={CornerDownRight} label={snapshot.language === "zh-CN" ? "实时引导" : t("guide")} hint={snapshot.language === "zh-CN" ? "尽快注入" : "Steer immediately"} selected={snapshot.queueMode === "guide"} onClick={() => void changeQueueMode("guide")} />
                </div>
              </GovernanceRow>
              <div className="governance-integrity"><span><ShieldCheck size={15} /></span><div><strong>{snapshot.language === "zh-CN" ? "审批决策校验" : "Approval decision validation"}</strong><small>{snapshot.language === "zh-CN" ? "仅接受完整 JSON；无效内容不会执行原操作" : "Only complete JSON is accepted; invalid decisions never execute the original action"}</small></div><em><i />{snapshot.language === "zh-CN" ? "失败关闭" : "Fail closed"}</em></div>
            </div>
          </SettingsPane>}
          {activeSection === "appearance" && <SettingsPane title={t("settingsAppearance")} description={t("settingsAppearanceHint")} className="appearance-pane">
            <div className="settings-card appearance-card">
              <SettingRow label={snapshot.language === "zh-CN" ? "界面语言" : "Interface language"} description={snapshot.language === "zh-CN" ? "应用菜单、按钮与系统消息" : "Application menus, buttons, and system messages"}><div className="appearance-segmented" role="radiogroup"><button type="button" className={snapshot.language === "zh-CN" ? "selected" : ""} onClick={() => { setLanguage("zh-CN"); void action("set_language", "zh-CN"); }}>{t("langZh")}</button><button type="button" className={snapshot.language === "en" ? "selected" : ""} onClick={() => { setLanguage("en"); void action("set_language", "en"); }}>English</button></div></SettingRow>
              <SettingRow label={t("theme")} description={snapshot.language === "zh-CN" ? "跟随系统可自动切换明暗" : "Follow the system appearance automatically"}><div className="theme-preview-group" role="radiogroup">{(["light", "dark", "system"] as const).map((item) => <button type="button" key={item} className={theme === item ? "selected" : ""} onClick={() => setTheme(item)}><span data-theme-preview={item}><i /><b /></span><small>{item === "light" ? (snapshot.language === "zh-CN" ? "暖白" : "Warm light") : item === "dark" ? (snapshot.language === "zh-CN" ? "夜间" : "Night") : t("system")}</small></button>)}</div></SettingRow>
              <SettingRow label={t("interfaceFont")} description={t("interfaceFontHint")}><MenuSelect className="setting-menu font-family-menu" value={uiFont} options={fontOptions} onChange={setUIFont} ariaLabel={t("interfaceFont")} fit="full" searchable searchPlaceholder={t("searchFonts")} emptyLabel={t("noMatchingFonts")} /></SettingRow>
              <SettingRow label={t("interfaceFontSize")} description={t("interfaceFontSizeHint")}><div className="font-size-control"><button type="button" onClick={() => setUIFontSize(uiFontSize - 1)} disabled={uiFontSize <= 11} aria-label={t("decreaseFontSize")}><span>A−</span></button><output aria-live="polite">{uiFontSize} px</output><button type="button" onClick={() => setUIFontSize(uiFontSize + 1)} disabled={uiFontSize >= 20} aria-label={t("increaseFontSize")}><span>A+</span></button></div></SettingRow>
              <SettingRow label={snapshot.language === "zh-CN" ? "减弱动态效果" : "Reduce motion"} description={snapshot.language === "zh-CN" ? "将场景切换与流式渐显缩短为即时更新" : "Make scene transitions and streaming reveals immediate"}><button type="button" role="switch" aria-checked={reducedMotion} className={`settings-switch ${reducedMotion ? "on" : ""}`} onClick={() => setReducedMotion((value) => !value)}><span /></button></SettingRow>
            </div>
          </SettingsPane>}
          {activeSection === "extensions" && <ExtensionsSettings language={snapshot.language} sessionId={snapshot.sessionId} openAddRequest={addMCPRequest} executeAction={execute} onError={setError} />}
        </div>
      </main>
    </div>
  </dialog>;
}

function SettingsPane({ title, description, action, className = "", children }: { title: string; description: string; action?: React.ReactNode; className?: string; children: React.ReactNode }) {
  return <section className={`settings-pane ${className}`.trim()}><header><div><h2>{title}</h2><p>{description}</p></div>{action}</header>{children}</section>;
}

function GovernanceRow({ label, description, children }: { label: string; description: string; children: React.ReactNode }) {
  return <div className="governance-row"><div className="governance-row-copy"><strong>{label}</strong><small>{description}</small></div>{children}</div>;
}

function GovernanceOption({ icon: Icon, label, hint, badge, selected, onClick }: { icon: typeof Hand; label: string; hint: string; badge?: string; selected: boolean; onClick: () => void }) {
  return <button type="button" role="radio" aria-checked={selected} className={selected ? "selected" : ""} onClick={onClick}><Icon size={15} /><span><strong>{label}</strong><small>{hint}</small></span>{badge && <em>{badge}</em>}{selected && <Check className="governance-check" size={13} />}</button>;
}

function CapacityControl({ label, description, children }: { label: string; description: string; children: React.ReactNode }) {
  return <section><div><strong>{label}</strong><small>{description}</small></div>{children}</section>;
}

function CompactStepper({ value, min, max, decrease, increase }: { value: number; min: number; max: number; decrease: () => void; increase: () => void }) {
  return <div className="compact-stepper"><button type="button" aria-label="Decrease" disabled={value <= min} onClick={decrease}><Minus size={13} /></button><output>{value}</output><button type="button" aria-label="Increase" disabled={value >= max} onClick={increase}><Plus size={13} /></button></div>;
}

function RuntimeInvariant({ label, hint }: { label: string; hint: string }) {
  return <div className="runtime-invariant"><div><strong>{label}</strong><small>{hint}</small></div><span aria-label="Enabled"><i /></span></div>;
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

  const requiresExplicitRoute = route.Scope === "vision";
  const provider = value.provider || (requiresExplicitRoute ? "" : snapshot.provider);
  const providerModels = (modelsByProvider[provider] ?? []).filter((item) => isRouteModelVisible(item, requiresExplicitRoute));
  const requestedModel = value.model || (provider === snapshot.provider ? snapshot.model : "");
  const model = findModelOption(providerModels, requestedModel)?.id || providerModels[0]?.id || "";
  const modelInfo = findModelOption(providerModels, model);
  const reasoning = value.reasoning || modelInfo?.defaultReasoning || snapshot.reasoning;
  const reasoningLevels = sortReasoningLevels([
    ...(modelInfo?.reasoningLevels ?? []),
    reasoning,
  ]);
  const inherited = !value.provider && !value.model && !value.reasoning;
  const title = routeTitle(route, language);
  const configuredModelOptions = routeModelOptions(modelsByProvider, modelProviders, requiresExplicitRoute);
  const allModelOptions = requiresExplicitRoute
    ? [{ value: "::", label: t("routeNotConfigured") }, ...configuredModelOptions]
    : configuredModelOptions;
  const selectedValue = `${provider}::${model}`;
  const selectModel = (next: string) => {
    if (requiresExplicitRoute && next === "::") {
      const nextValue = { provider: "", model: "", reasoning: "" };
      setValue(nextValue);
      void action("set_model_route", "", { ...route, Route: nextValue });
      return;
    }
    const separator = next.indexOf("::");
    const nextProvider = separator >= 0 ? next.slice(0, separator) : provider;
    const nextModelID = separator >= 0 ? next.slice(separator + 2) : next;
    const nextModel = findModelOption(modelsByProvider[nextProvider] ?? [], nextModelID);
    const nextReasoning = nextModel?.defaultReasoning || reasoning || snapshot.reasoning;
    const nextValue = { provider: nextProvider, model: nextModelID, reasoning: nextReasoning };
    setValue(nextValue);
    void action("set_model_route", "", { ...route, Route: nextValue });
  };
  const selectReasoning = (nextReasoning: string) => {
    const nextValue = { provider, model, reasoning: nextReasoning };
    setValue(nextValue);
    void action("set_model_route", "", { ...route, Route: nextValue });
  };

  return <div className="route-row">
    <ProviderIcon provider={provider || snapshot.provider} size={19} className="route-provider-icon" />
    <div className="route-copy"><strong>{title}</strong><small>{routeDescription(route, description, language)}</small></div>
    <div className="route-controls">
      <MenuSelect className="route-model-menu" panelClassName="route-model-options" value={selectedValue} options={allModelOptions} onChange={selectModel} ariaLabel={`${title} ${t("model")}`} searchable searchPlaceholder={t("searchModels")} emptyLabel={t("noMatchingModels")} fit="full" showSelectedIcon={false} menuWidth={292} menuAlign="right" />
      <MenuSelect
        className="route-reasoning-menu"
        panelClassName="route-reasoning-options"
        value={reasoning}
        options={reasoningLevels.map((level) => ({ value: level, label: reasoningLabel(level, language) }))}
        onChange={selectReasoning}
        ariaLabel={`${title} ${t("reasoning")}`}
        disabled={reasoningLevels.length <= 1}
        showSelectedIcon={false}
        menuWidth={148}
        menuAlign="right"
      />
    </div>
  </div>;
}

function isRouteModelVisible(item: ModelOption, requiresImages: boolean) {
  if (item.disabled) return false;
  if (!requiresImages || !item.inputModalities?.length) return true;
  return item.inputModalities.some((modality) => modality.toLocaleLowerCase() === "image");
}

function routeModelOptions(modelsByProvider: Record<string, ModelOption[]>, modelProviders: ModelProvider[], requiresImages: boolean) {
  return Object.entries(modelsByProvider).flatMap(([providerID, models]) => models
    .filter((item) => isRouteModelVisible(item, requiresImages))
    .map((item) => ({
      value: `${providerID}::${item.id}`,
      label: modelDisplayName(item.id, item.name),
      caption: providerDisplayName(providerID, modelProviders),
      keywords: [providerID, providerDisplayName(providerID, modelProviders), ...(item.aliases ?? [])],
      icon: <ProviderIcon provider={providerID} />,
    })));
}

function SettingRow({ label, description, children }: { label: string; description: string; children: React.ReactNode }) { return <div className="setting-row"><div><strong>{label}</strong><p>{description}</p></div><div>{children}</div></div>; }
function systemFontOptions(selected: string, fonts: SystemFont[]) {
  const options = new Map(fonts.map((font) => [font.family, { value: font.family, label: font.label || font.family, caption: font.family }]));
  if (selected !== "system" && !options.has(selected)) options.set(selected, { value: selected, label: selected, caption: selected });
  return [...options.values()];
}
function routeTitle(route: ModelRoute, language: Language) {
  const t = translator(language);
	if (route.Scope === "main") return t("routeMain");
  if (route.Scope === "title") return t("routeTitle");
  if (route.Scope === "plan") return t("routePlan");
  if (route.Scope === "approval") return t("routeApproval");
  if (route.Scope === "vision") return t("routeVision");
  if (route.Scope === "compaction") return t("routeCompaction");
  if (route.Role === "research") return language === "zh-CN" ? "研究与文档" : "Research and documentation";
  if (route.Role === "review") return language === "zh-CN" ? "编码与审查" : "Coding and review";
  return route.Role || route.Label;
}
function routeDescription(route: ModelRoute, description: string, language: Language) {
  const t = translator(language);
	if (route.Scope === "main") return t("routeMainHint");
  if (route.Scope === "title") return t("routeTitleHint");
  if (route.Scope === "plan") return t("routePlanHint");
  if (route.Scope === "approval") return t("routeApprovalHint");
  if (route.Scope === "vision") return t("routeVisionHint");
	if (route.Scope === "compaction") return t("routeCompactionHint");
	if (route.Role === "research") return language === "zh-CN" ? "检索、映射、说明文档" : "Research, mapping, and documentation";
	if (route.Role === "review") return language === "zh-CN" ? "实现、调试、架构判断" : "Implementation, debugging, and architecture";
  return description || tFormat(language, "routeSubagentHint", { role: route.Role || route.Label });
}
