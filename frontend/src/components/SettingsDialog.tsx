import { useEffect, useMemo, useRef, useState } from "react";
import {
  Archive, ArrowLeft, BarChart3, Bot, Check, CornerDownRight, Database, Gauge, Hand, List, Minus, Palette, Plus, Puzzle,
  RefreshCw, Search, Settings2, ShieldAlert, ShieldCheck, X, Zap,
} from "lucide-react";
import { execute, listHookCatalog, listSkillCatalog, listSystemFonts, type SystemFont } from "../bridge";
import {
  CHAT_CODE_FONT_MAX,
  CHAT_CODE_FONT_MIN,
  CHAT_UI_FONT_MAX,
  CHAT_UI_FONT_MIN,
} from "../chatTypography";
import { cursorTiersForMode, cursorVariantForSelection, findCursorModelGroup, groupCursorModelVariants, preferredCursorVariant } from "../cursorModelVariants";
import { reasoningLabel, sortReasoningLevels, tFormat, translator, type Language } from "../i18n";
import { routeSearchID, settingsSectionSearchAliases } from "../settingsSearch";
import { findModelOption, modelDisplayName, providerDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { ActionKind, DeliveryMode, ModelProvider, ModelRoute, ModelRouteConfig, SettingsSection } from "../types";
import MenuSelect from "./MenuSelect";
import ModelProviderSettings from "./ModelProviderSettings";
import ProviderIcon from "./ProviderIcon";
import ArchiveSettings from "./ArchiveSettings";
import ExtensionsSettings from "./ExtensionsSettings";
import UsageSettings from "./UsageSettings";
import SecuritySettings from "./SecuritySettings";

export default function SettingsDialog() {
  const dialog = useRef<HTMLDialogElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
  const coreModelRoutes = modelRoutes.filter((route) => route.scope !== "subagent" && route.scope !== "security" && route.scope !== "main");
  const securityModelRoutes = modelRoutes.filter((route) => route.scope === "security");
  const subagentModelRoutes = modelRoutes.filter((route) => route.scope === "subagent");
  const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const agentCatalog = useRuntimeStore((state) => state.agentCatalog);
  const theme = useRuntimeStore((state) => state.theme);
  const extensionThemes = useRuntimeStore((state) => state.extensionThemes);
  const uiFont = useRuntimeStore((state) => state.uiFont);
  const uiFontSize = useRuntimeStore((state) => state.uiFontSize);
  const chatFontSize = useRuntimeStore((state) => state.chatFontSize);
  const chatCodeFontSize = useRuntimeStore((state) => state.chatCodeFontSize);
  const approvalMode = useRuntimeStore((state) => state.approvalMode);
  const settingsTarget = useRuntimeStore((state) => state.settingsTarget);
  const setTheme = useRuntimeStore((state) => state.setTheme);
  const setUIFont = useRuntimeStore((state) => state.setUIFont);
  const setUIFontSize = useRuntimeStore((state) => state.setUIFontSize);
  const setChatFontSize = useRuntimeStore((state) => state.setChatFontSize);
  const setChatCodeFontSize = useRuntimeStore((state) => state.setChatCodeFontSize);
  const setLanguage = useRuntimeStore((state) => state.setLanguage);
  const setQueueMode = useRuntimeStore((state) => state.setQueueMode);
  const setError = useRuntimeStore((state) => state.setError);
  const error = useRuntimeStore((state) => state.error);
  const [activeSection, setActiveSection] = useState<SettingsSection>(() => settingsTarget?.section ?? "catalog");
  const [query, setQuery] = useState("");
  const [concurrency, setConcurrency] = useState(snapshot.subagentConcurrency);
  const [maxDepth, setMaxDepth] = useState(snapshot.subagentMaxDepth ?? 2);
  const [shellConcurrency, setShellConcurrency] = useState(snapshot.shellConcurrency ?? 2);
  const [shellMaxWallClockSeconds, setShellMaxWallClockSeconds] = useState(snapshot.shellMaxWallClockSeconds ?? 600);
  const [awaitSeconds, setAwaitSeconds] = useState(snapshot.subagentAwaitSeconds ?? 0);
  const [idleSeconds, setIdleSeconds] = useState(snapshot.subagentIdleSeconds ?? 0);
  const [addProviderRequest, setAddProviderRequest] = useState(0);
  const [securityDirty, setSecurityDirty] = useState(false);
  const [systemFonts, setSystemFonts] = useState<SystemFont[]>([]);
  const [reducedMotion, setReducedMotion] = useState(() => localStorage.getItem("azem-reduced-motion") === "true");
  const t = translator(snapshot.language);
  const fontOptions = [
    { value: "system", label: t("systemFont"), caption: snapshot.language === "zh-CN" ? "SF Pro Text · 苹方" : "SF Pro Text · PingFang" },
    ...systemFontOptions(uiFont, systemFonts),
  ];
  const confirmDiscardSecurity = () => activeSection !== "security" || !securityDirty || window.confirm(snapshot.language === "zh-CN" ? "尚未保存安全扫描设置。放弃这些更改？" : "Security scan settings are not saved. Discard these changes?");
  const close = () => {
    if (!confirmDiscardSecurity()) return;
    useRuntimeStore.getState().setSettingsOpen(false);
  };
  const selectSection = (section: SettingsSection) => {
    if (section === activeSection) return;
    if (!confirmDiscardSecurity()) return;
    setSecurityDirty(false);
    setActiveSection(section);
  };
  const sections: Array<{ id: SettingsSection; label: string; description: string; icon: typeof Bot }> = [
	{ id: "catalog", label: t("modelSettings"), description: t("modelSettingsHint"), icon: Database },
    { id: "models", label: t("roleModels"), description: t("settingsModelsHint"), icon: Bot },
    { id: "subagents", label: t("subagentRuntime"), description: t("settingsSubagentsHint"), icon: Gauge },
    { id: "security", label: snapshot.language === "zh-CN" ? "安全扫描" : "Security scans", description: snapshot.language === "zh-CN" ? "执行、时限、模型与发布" : "Execution, deadline, models, and publication", icon: ShieldCheck },
    { id: "governance", label: t("settingsGovernance"), description: t("settingsGovernanceHint"), icon: Settings2 },
    { id: "appearance", label: t("appearance"), description: t("settingsAppearanceHint"), icon: Palette },
    { id: "extensions", label: t("settingsExtensions"), description: t("settingsExtensionsHint"), icon: Puzzle },
    { id: "archive", label: t("settingsArchive"), description: t("settingsArchiveHint"), icon: Archive },
    { id: "usage", label: t("settingsUsage"), description: t("settingsNavUsage"), icon: BarChart3 },
  ];
  const filteredSections = sections.filter((section) => `${section.label} ${section.description} ${(settingsSectionSearchAliases[section.id] ?? []).join(" ")}`.toLowerCase().includes(query.trim().toLowerCase()));
  const current = sections.find((section) => section.id === activeSection)!;
  const descriptions = useMemo(() => new Map(agentCatalog.map((agent) => [agent.name, agent.description])), [agentCatalog]);
	const catalogPage: Partial<Record<SettingsSection, React.ReactNode>> = {
		catalog: <ModelProviderSettings providers={modelProviders} modelsByProvider={modelsByProvider} sessionId={snapshot.sessionId} language={snapshot.language} setError={setError} addRequest={addProviderRequest} selectedProviderID={settingsTarget?.id.startsWith("provider:") ? settingsTarget.id.slice("provider:".length) : ""} />,
	};

  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    previouslyFocused.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!node.open) node.showModal();
    node.focus();
    requestAnimationFrame(() => {
      node.focus();
    });
    const onCancel = (event: Event) => {
      event.preventDefault();
      close();
    };
    node.addEventListener("cancel", onCancel);
	const refreshSkillCatalog = listSkillCatalog().then((catalog) => {
		useRuntimeStore.getState().applyEvents([{
			sequence: 0,
			kind: "skill_catalog",
			state: "listed",
			skillCatalog: catalog.entries as unknown as Array<Record<string, unknown>>,
		}]);
	});
	const refreshHookCatalog = listHookCatalog().then((catalog) => {
		useRuntimeStore.getState().applyEvents([{
			sequence: 0,
			kind: "hook_catalog",
			state: "listed",
			hookCatalog: catalog,
		}]);
	});
    void Promise.all([
      execute({ kind: "list_model_routes", sessionId: snapshot.sessionId }),
      execute({ kind: "list_agent_types", sessionId: snapshot.sessionId }),
      execute({ kind: "list_models", sessionId: snapshot.sessionId }),
	  execute({ kind: "list_model_providers", sessionId: snapshot.sessionId }),
	  refreshSkillCatalog,
	  execute({ kind: "list_plugins", sessionId: snapshot.sessionId }),
	  refreshHookCatalog,
	  execute({ kind: "refresh_mcp", sessionId: snapshot.sessionId }),
	  execute({ kind: "list_sessions", sessionId: snapshot.sessionId }),
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
  useEffect(() => setMaxDepth(snapshot.subagentMaxDepth ?? 2), [snapshot.subagentMaxDepth]);
  useEffect(() => setShellConcurrency(snapshot.shellConcurrency ?? 2), [snapshot.shellConcurrency]);
  useEffect(() => setShellMaxWallClockSeconds(snapshot.shellMaxWallClockSeconds ?? 600), [snapshot.shellMaxWallClockSeconds]);
  useEffect(() => setAwaitSeconds(snapshot.subagentAwaitSeconds ?? 0), [snapshot.subagentAwaitSeconds]);
  useEffect(() => setIdleSeconds(snapshot.subagentIdleSeconds ?? 0), [snapshot.subagentIdleSeconds]);
  useEffect(() => {
    document.documentElement.dataset.reduceMotion = String(reducedMotion);
    localStorage.setItem("azem-reduced-motion", String(reducedMotion));
  }, [reducedMotion]);

  useEffect(() => {
    if (settingsTarget) selectSection(settingsTarget.section);
  }, [settingsTarget]);

  useEffect(() => {
    if (!settingsTarget || settingsTarget.section !== activeSection) return;
    let clearTimer = 0;
    let waitTimer = 0;
    let observer: MutationObserver | null = null;
    const focusTarget = () => {
      const target = Array.from(dialog.current?.querySelectorAll<HTMLElement>("[data-setting-id]") ?? [])
        .find((node) => node.dataset.settingId === settingsTarget.id);
      if (!target) return false;
      observer?.disconnect();
      target.scrollIntoView?.({ block: "center", behavior: reducedMotion ? "auto" : "smooth" });
      target.classList.add("settings-search-focus");
      clearTimer = window.setTimeout(() => {
        target.classList.remove("settings-search-focus");
        useRuntimeStore.setState({ settingsTarget: null });
      }, 1600);
      return true;
    };
    const frame = requestAnimationFrame(() => {
      if (focusTarget()) return;
      const content = dialog.current?.querySelector<HTMLElement>(".settings-content");
      if (!content) return;
      observer = new MutationObserver(() => { focusTarget(); });
      observer.observe(content, { childList: true, subtree: true });
      waitTimer = window.setTimeout(() => {
        observer?.disconnect();
        useRuntimeStore.setState({ settingsTarget: null });
      }, 2000);
    });
    return () => {
      cancelAnimationFrame(frame);
      window.clearTimeout(clearTimer);
      window.clearTimeout(waitTimer);
      observer?.disconnect();
    };
  }, [activeSection, reducedMotion, settingsTarget]);

  const action = async (kind: ActionKind, target = "", route?: ModelRoute) => {
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
  return <dialog ref={dialog} className="settings-dialog" tabIndex={-1} aria-label={t("settings")}>
    <div className="settings-shell">
      <aside className="settings-sidebar">
        <button className="settings-back" onClick={close}><ArrowLeft size={15} />{t("backToApp")}</button>
        <label className="settings-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchSettings")} /><kbd>⌘F</kbd></label>
        <div className="settings-nav-group">
          <span>{snapshot.language === "zh-CN" ? "系统" : "System"}</span>
          {filteredSections.filter((section) => ["catalog", "models", "subagents", "security"].includes(section.id)).map((section) => <button key={section.id} className={activeSection === section.id ? "active" : ""} onClick={() => selectSection(section.id)}><section.icon size={15} /><span><strong>{section.label}</strong><small>{section.description}</small></span></button>)}
          <span>{snapshot.language === "zh-CN" ? "偏好" : "Preferences"}</span>
          {filteredSections.filter((section) => ["governance", "appearance", "extensions", "archive", "usage"].includes(section.id)).map((section) => <button key={section.id} className={activeSection === section.id ? "active" : ""} onClick={() => selectSection(section.id)}><section.icon size={15} /><span><strong>{section.label}</strong><small>{section.description}</small></span></button>)}
        </div>
        <footer><small>{activeSection === "security" ? (snapshot.language === "zh-CN" ? "执行与预算需保存 · 模型路由自动保存" : "Execution and budgets require Save · model routes autosave") : (snapshot.language === "zh-CN" ? "配置自动保存到本机" : "Saved locally")}</small><small>Azem v0.8.0</small></footer>
      </aside>
      <main className="settings-main" data-section={activeSection}>
        <header className="settings-page-header"><div><h1>{current.label}</h1><p>{current.description}</p></div>{activeSection === "catalog" && <button className="settings-primary" onClick={() => setAddProviderRequest((value) => value + 1)}><Plus size={13} />{snapshot.language === "zh-CN" ? "添加提供方" : "Add provider"}</button>}{activeSection === "models" && <span className="settings-valid"><i />{snapshot.language === "zh-CN" ? "配置有效" : "Valid configuration"}</span>}<button className="icon-button settings-close" onClick={close} aria-label={t("closeSettings")}><X size={17} /></button></header>
        <div className="settings-content">
          {error && <div className="settings-inline-error" role="alert">{error}</div>}
		  {catalogPage[activeSection]}
          {activeSection === "models" && <SettingsPane settingID="section:models" title={t("settingsModels")} description={t("settingsModelsHint")} className="model-routes-pane" action={<button className="small-button" onClick={() => void action("list_model_routes")}><RefreshCw size={13} />{t("refresh")}</button>}>
            {modelRoutes.length === 0 ? <div className="settings-card settings-empty"><span className="azem-mark" />{t("loadingRoles")}</div> : <div className="route-groups">
              <section className="settings-card route-card"><header><div><strong>{snapshot.language === "zh-CN" ? "核心工作流" : "Core workflows"}</strong><small>{snapshot.language === "zh-CN" ? "主会话与关键判断" : "Main conversation and critical decisions"}</small></div></header>{coreModelRoutes.map((route) => <RouteRow key={`${route.scope}-${route.role}`} route={route} description={descriptions.get(route.role) || route.label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}</section>
              <section className="settings-card route-card"><header><div><strong>{snapshot.language === "zh-CN" ? "子智能体默认模型" : "Subagent defaults"}</strong><small>{snapshot.language === "zh-CN" ? "角色仍可在定义中覆盖此配置" : "Roles can still override this setting"}</small></div></header>{subagentModelRoutes.map((route) => <RouteRow key={`${route.scope}-${route.role}`} route={route} description={descriptions.get(route.role) || route.label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}</section>
            </div>}
          </SettingsPane>}
          {activeSection === "subagents" && <SettingsPane settingID="section:subagents" title={t("settingsSubagents")} description={t("settingsSubagentsHint")} className="subagent-settings-pane">
            <section className="settings-card subagent-capacity" data-setting-id="subagents:capacity" aria-labelledby="subagent-capacity-title">
              <header>
                <div>
                  <strong id="subagent-capacity-title">{t("subagentCapacityTitle")}</strong>
                  <small>{t("subagentCapacityHint")}</small>
                </div>
              </header>
              <SettingRow settingID="subagents:concurrency" label={t("subagentConcurrencyLabel")} description={t("subagentConcurrencyHint")}>
                <CompactStepper
                  value={concurrency}
                  displayValue={concurrency === 0 ? t("subagentUnlimited") : undefined}
                  min={0}
                  max={64}
                  decreaseLabel={t("decreaseValue")}
                  increaseLabel={t("increaseValue")}
                  decrease={() => { const value = Math.max(0, concurrency - 1); setConcurrency(value); void action("set_subagent_concurrency", String(value)); }}
                  increase={() => { const value = Math.min(64, concurrency + 1); setConcurrency(value); void action("set_subagent_concurrency", String(value)); }}
                />
              </SettingRow>
              <SettingRow settingID="subagents:depth" label={t("subagentDepthLabel")} description={t("subagentDepthHint")}>
                <MenuSelect
                  className="capacity-depth-menu"
                  value={String(maxDepth)}
                  options={[{ value: "-1", label: t("subagentUnlimited") }, { value: "0", label: t("subagentDepthNone") }, ...[1, 2, 3].map((depth) => ({ value: String(depth), label: String(depth) }))]}
                  onChange={(value) => { const depth = Number(value); setMaxDepth(depth); void action("set_subagent_depth", value); }}
                  ariaLabel={t("subagentDepthLabel")}
                />
              </SettingRow>
              <SettingRow settingID="subagents:shell" label={t("subagentShellLabel")} description={t("subagentShellHint")}>
                <CompactStepper
                  value={shellConcurrency}
                  min={1}
                  max={16}
                  decreaseLabel={t("decreaseValue")}
                  increaseLabel={t("increaseValue")}
                  decrease={() => { const value = Math.max(1, shellConcurrency - 1); setShellConcurrency(value); void action("set_shell_concurrency", String(value)); }}
                  increase={() => { const value = Math.min(16, shellConcurrency + 1); setShellConcurrency(value); void action("set_shell_concurrency", String(value)); }}
                />
              </SettingRow>
              <SettingRow settingID="subagents:shell-wall" label={t("shellWallClockLabel")} description={t("shellWallClockHint")}>
                <MenuSelect
                  className="capacity-timeout-menu"
                  value={String(shellMaxWallClockSeconds)}
                  options={[60, 120, 300, 600, 900, 1800, 3600, 7200].map((seconds) => ({
                    value: String(seconds),
                    label: tFormat(snapshot.language, "subagentMinutes", { n: seconds / 60 }),
                  }))}
                  onChange={(value) => { const seconds = Number(value); setShellMaxWallClockSeconds(seconds); void action("set_shell_max_wall_clock", value); }}
                  ariaLabel={t("shellWallClockLabel")}
                />
              </SettingRow>
              <SettingRow settingID="subagents:timeout" label={t("subagentAwaitLabel")} description={t("subagentAwaitHint")}>
                <MenuSelect
                  className="capacity-timeout-menu"
                  value={String(awaitSeconds)}
                  options={[
                    { value: "0", label: t("subagentAwaitUntilDone") },
                    ...[30, 60, 300, 600, 1800].map((seconds) => ({
                      value: String(seconds),
                      label: seconds < 60 ? tFormat(snapshot.language, "subagentSeconds", { n: seconds }) : tFormat(snapshot.language, "subagentMinutes", { n: seconds / 60 }),
                    })),
                  ]}
                  onChange={(value) => { const seconds = Number(value); setAwaitSeconds(seconds); void action("set_subagent_await_timeout", value); }}
                  ariaLabel={t("subagentAwaitLabel")}
                />
              </SettingRow>
              <SettingRow settingID="subagents:idle" label={t("subagentIdleLabel")} description={t("subagentIdleHint")}>
                <MenuSelect
                  className="capacity-timeout-menu"
                  value={String(idleSeconds)}
                  options={[
                    { value: "0", label: t("subagentIdleOff") },
                    ...[60, 120, 300, 600, 900, 1800].map((seconds) => ({
                      value: String(seconds),
                      label: tFormat(snapshot.language, "subagentMinutes", { n: seconds / 60 }),
                    })),
                  ]}
                  onChange={(value) => { const seconds = Number(value); setIdleSeconds(seconds); void action("set_subagent_idle_timeout", value); }}
                  ariaLabel={t("subagentIdleLabel")}
                />
              </SettingRow>
            </section>
            <section className="settings-card subagent-scheduling" data-setting-id="subagents:scheduling" aria-labelledby="subagent-scheduling-title">
              <header>
                <div>
                  <strong id="subagent-scheduling-title">{t("subagentSchedulingTitle")}</strong>
                  <small>{t("subagentSchedulingHint")}</small>
                </div>
              </header>
              <div className="subagent-policy" role="note">
                <div>
                  <strong>{t("subagentSchedulingPolicy")}</strong>
                  <p>{t("subagentSchedulingPolicyHint")}</p>
                </div>
                <div className="subagent-policy-value">
                  <b>{t("subagentSchedulingParallel")}</b>
                  <span>{t("subagentSchedulingReadOnly")}</span>
                </div>
              </div>
            </section>
            <section className="settings-card subagent-display" data-setting-id="subagents:display" aria-labelledby="subagent-display-title">
              <header>
                <div>
                  <strong id="subagent-display-title">{t("subagentDisplayTitle")}</strong>
                  <small>{t("subagentDisplayHint")}</small>
                </div>
              </header>
              <DisplayFact settingID="subagents:progress" label={t("subagentShowProgress")} hint={t("subagentShowProgressHint")} status={t("subagentAlwaysOn")} />
              <DisplayFact settingID="subagents:cards" label={t("subagentKeepCards")} hint={t("subagentKeepCardsHint")} status={t("subagentAlwaysOn")} />
              <DisplayFact settingID="subagents:queue" label={t("subagentQueueWhenFull")} hint={t("subagentQueueWhenFullHint")} status={t("subagentAlwaysOn")} />
            </section>
          </SettingsPane>}
          {activeSection === "security" && <SettingsPane settingID="section:security" title={snapshot.language === "zh-CN" ? "安全扫描" : "Security scans"} description={snapshot.language === "zh-CN" ? "控制新扫描的执行边界、运行时限、模型职责与外部发布。" : "Control execution boundaries, run deadline, model responsibilities, and external publication for new scans."} className="security-settings-pane">
            <SecuritySettings language={snapshot.language} sessionId={snapshot.sessionId} onError={setError} onDirtyChange={setSecurityDirty} />
            <section className="settings-card route-card security-route-card" data-setting-id="security:routes">
              <header><div><strong>{snapshot.language === "zh-CN" ? "安全模型路由" : "Security model routes"}</strong><small>{snapshot.language === "zh-CN" ? "分别选择审计、归并、修复和独立验证模型；留空时继承默认模型。" : "Choose audit, reduction, fixing, and independent verification models; empty routes inherit the default."}</small></div></header>
              {securityModelRoutes.length === 0 ? <div className="settings-empty"><span className="azem-mark" />{t("loadingRoles")}</div> : securityModelRoutes.map((route) => <RouteRow key={`${route.scope}-${route.role}`} route={route} description={route.label} modelsByProvider={modelsByProvider} modelProviders={modelProviders} action={action} language={snapshot.language} />)}
            </section>
          </SettingsPane>}
          {activeSection === "governance" && <SettingsPane settingID="section:governance" title={t("settingsGovernance")} description={t("settingsGovernanceHint")} className="governance-pane">
            <div className="settings-card governance-settings">
              <GovernanceRow settingID="governance:approval" label={t("defaultApprovalMode")} description={snapshot.language === "zh-CN" ? "控制工具执行边界" : "Control tool execution boundaries"}>
                <div className="governance-options governance-options-three" role="radiogroup" aria-label={t("defaultApprovalMode")}>
                  <GovernanceOption icon={Hand} label={t("promptApproval")} hint={snapshot.language === "zh-CN" ? "逐次确认" : "Confirm each time"} selected={approvalMode === "prompt"} onClick={() => void action("set_approval_mode", "prompt")} />
                  <GovernanceOption icon={ShieldCheck} label={t("autoReview")} hint={snapshot.language === "zh-CN" ? "策略判断" : "Policy review"} badge={snapshot.language === "zh-CN" ? "推荐" : "Recommended"} selected={approvalMode === "auto_review"} onClick={() => void action("set_approval_mode", "auto_review")} />
                  <GovernanceOption icon={ShieldAlert} label={t("yolo")} hint={snapshot.language === "zh-CN" ? "硬性边界" : "Hard boundaries"} selected={approvalMode === "yolo"} onClick={() => void action("set_approval_mode", "yolo")} />
                </div>
              </GovernanceRow>
              <GovernanceRow settingID="governance:messages" label={t("runningMessageMode")} description={snapshot.language === "zh-CN" ? "收到补充输入时" : "When follow-up input arrives"}>
                <div className="governance-options governance-options-two" role="radiogroup" aria-label={t("runningMessageMode")}>
                  <GovernanceOption icon={List} label={snapshot.language === "zh-CN" ? "加入队列" : t("queue")} hint={snapshot.language === "zh-CN" ? "按顺序处理" : "Process in order"} selected={snapshot.queueMode === "queue"} onClick={() => void changeQueueMode("queue")} />
                  <GovernanceOption icon={CornerDownRight} label={snapshot.language === "zh-CN" ? "实时引导" : t("guide")} hint={snapshot.language === "zh-CN" ? "尽快注入" : "Steer immediately"} selected={snapshot.queueMode === "guide"} onClick={() => void changeQueueMode("guide")} />
                </div>
              </GovernanceRow>
              <div className="governance-integrity"><span><ShieldCheck size={15} /></span><div><strong>{snapshot.language === "zh-CN" ? "审批决策校验" : "Approval decision validation"}</strong><small>{snapshot.language === "zh-CN" ? "仅接受完整 JSON；无效内容不会执行原操作" : "Only complete JSON is accepted; invalid decisions never execute the original action"}</small></div><em><i />{snapshot.language === "zh-CN" ? "失败关闭" : "Fail closed"}</em></div>
            </div>
          </SettingsPane>}
          {activeSection === "appearance" && <SettingsPane settingID="section:appearance" title={t("settingsAppearance")} description={t("settingsAppearanceHint")} className="appearance-pane">
            <div className="settings-card appearance-card">
              <SettingRow settingID="appearance:language" label={snapshot.language === "zh-CN" ? "界面语言" : "Interface language"} description={snapshot.language === "zh-CN" ? "应用菜单、按钮与系统消息" : "Application menus, buttons, and system messages"}><div className="appearance-segmented" role="radiogroup"><button type="button" className={snapshot.language === "zh-CN" ? "selected" : ""} onClick={() => { setLanguage("zh-CN"); void action("set_language", "zh-CN"); }}>{t("langZh")}</button><button type="button" className={snapshot.language === "en" ? "selected" : ""} onClick={() => { setLanguage("en"); void action("set_language", "en"); }}>English</button></div></SettingRow>
              <SettingRow settingID="appearance:theme" label={t("theme")} description={snapshot.language === "zh-CN" ? "跟随系统可自动切换明暗" : "Follow the system appearance automatically"}><div className="theme-preview-group" role="radiogroup">{[...(["light", "dark", "system"] as const), ...extensionThemes.map((entry) => entry.name)].map((item) => <button type="button" key={item} className={theme === item ? "selected" : ""} onClick={() => setTheme(item)}><span data-theme-preview={item}><i /><b /></span><small>{item === "light" ? (snapshot.language === "zh-CN" ? "暖白" : "Warm light") : item === "dark" ? (snapshot.language === "zh-CN" ? "夜间" : "Night") : item === "system" ? t("system") : item}</small></button>)}</div></SettingRow>
              <SettingRow settingID="appearance:font" label={t("interfaceFont")} description={t("interfaceFontHint")}><MenuSelect className="setting-menu font-family-menu" value={uiFont} options={fontOptions} onChange={setUIFont} ariaLabel={t("interfaceFont")} fit="full" searchable searchPlaceholder={t("searchFonts")} emptyLabel={t("noMatchingFonts")} /></SettingRow>
              <SettingRow settingID="appearance:font-size" label={t("interfaceFontSize")} description={t("interfaceFontSizeHint")}><FontSizeControl value={uiFontSize} min={11} max={20} onChange={setUIFontSize} decreaseLabel={t("decreaseFontSize")} increaseLabel={t("increaseFontSize")} /></SettingRow>
              <SettingRow settingID="appearance:motion" label={snapshot.language === "zh-CN" ? "减弱动态效果" : "Reduce motion"} description={snapshot.language === "zh-CN" ? "将场景切换与流式渐显缩短为即时更新" : "Make scene transitions and streaming reveals immediate"}><button type="button" role="switch" aria-checked={reducedMotion} className={`settings-switch ${reducedMotion ? "on" : ""}`} onClick={() => setReducedMotion((value) => !value)}><span /></button></SettingRow>
            </div>
            <section className="settings-card appearance-card appearance-chat-card" data-setting-id="appearance:chat-text" aria-labelledby="chat-text-title">
              <header>
                <div>
                  <strong id="chat-text-title">{t("chatTextControls")}</strong>
                  <small>{t("chatTextControlsHint")}</small>
                </div>
              </header>
              <SettingRow settingID="appearance:chat-font-size" label={t("chatUIFontSize")} description={t("chatUIFontSizeHint")}>
                <FontSizeControl value={chatFontSize} min={CHAT_UI_FONT_MIN} max={CHAT_UI_FONT_MAX} onChange={setChatFontSize} decreaseLabel={t("decreaseChatUIFontSize")} increaseLabel={t("increaseChatUIFontSize")} />
              </SettingRow>
              <SettingRow settingID="appearance:chat-code-font-size" label={t("chatCodeFontSize")} description={t("chatCodeFontSizeHint")}>
                <FontSizeControl value={chatCodeFontSize} min={CHAT_CODE_FONT_MIN} max={CHAT_CODE_FONT_MAX} onChange={setChatCodeFontSize} decreaseLabel={t("decreaseChatCodeFontSize")} increaseLabel={t("increaseChatCodeFontSize")} />
              </SettingRow>
            </section>
          </SettingsPane>}
          {activeSection === "extensions" && <ExtensionsSettings language={snapshot.language} sessionId={snapshot.sessionId} executeAction={execute} onError={setError} targetTab={settingsTarget?.id === "extensions:skills" ? "skills" : settingsTarget?.id === "extensions:plugins" ? "plugins" : settingsTarget?.id === "extensions:marketplace" ? "marketplace" : settingsTarget?.id === "extensions:hooks" ? "hooks" : "mcp"} />}
          {activeSection === "archive" && <SettingsPane settingID="section:archive" title={t("settingsArchive")} description={t("settingsArchiveHint")} className="archive-settings-pane"><ArchiveSettings language={snapshot.language} sessionId={snapshot.sessionId} onError={setError} /></SettingsPane>}
          {activeSection === "usage" && <SettingsPane settingID="section:usage" title={t("settingsUsage")} description={t("settingsUsageHint")} className="usage-settings-pane"><UsageSettings language={snapshot.language} onError={setError} /></SettingsPane>}
        </div>
      </main>
    </div>
  </dialog>;
}

function SettingsPane({ title, description, action, className = "", settingID, children }: { title: string; description: string; action?: React.ReactNode; className?: string; settingID?: string; children: React.ReactNode }) {
  return <section className={`settings-pane ${className}`.trim()} data-setting-id={settingID}><header><div><h2>{title}</h2><p>{description}</p></div>{action}</header>{children}</section>;
}

function GovernanceRow({ label, description, settingID, children }: { label: string; description: string; settingID?: string; children: React.ReactNode }) {
  return <div className="governance-row" data-setting-id={settingID}><div className="governance-row-copy"><strong>{label}</strong><small>{description}</small></div>{children}</div>;
}

function GovernanceOption({ icon: Icon, label, hint, badge, selected, onClick }: { icon: typeof Hand; label: string; hint: string; badge?: string; selected: boolean; onClick: () => void }) {
  return <button type="button" role="radio" aria-checked={selected} className={selected ? "selected" : ""} onClick={onClick}><Icon size={15} /><span><strong>{label}</strong><small>{hint}</small></span>{badge && <em>{badge}</em>}{selected && <Check className="governance-check" size={13} />}</button>;
}

function CompactStepper({ value, displayValue, min, max, decrease, increase, decreaseLabel, increaseLabel }: {
  value: number;
  displayValue?: string;
  min: number;
  max: number;
  decrease: () => void;
  increase: () => void;
  decreaseLabel: string;
  increaseLabel: string;
}) {
  return <div className="compact-stepper">
    <button type="button" aria-label={decreaseLabel} disabled={value <= min} onClick={decrease}><Minus size={13} /></button>
    <output aria-live="polite">{displayValue ?? value}</output>
    <button type="button" aria-label={increaseLabel} disabled={value >= max} onClick={increase}><Plus size={13} /></button>
  </div>;
}

function DisplayFact({ label, hint, settingID, status }: { label: string; hint: string; settingID: string; status: string }) {
  return <div className="setting-row subagent-display-row" data-setting-id={settingID}>
    <div><strong>{label}</strong><p>{hint}</p></div>
    <div>
      <span className="settings-switch on" role="switch" aria-checked="true" aria-disabled="true" aria-label={status}><span /></span>
    </div>
  </div>;
}

function RouteRow({ route, description, modelsByProvider, modelProviders, action, language }: {
  route: ModelRoute;
  description: string;
  modelsByProvider: Record<string, ModelOption[]>;
  modelProviders: ModelProvider[];
  action: (kind: ActionKind, target?: string, route?: ModelRoute) => Promise<void>;
  language: Language;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const t = translator(language);
  const [value, setValue] = useState<ModelRouteConfig>({ ...route.route });
  useEffect(() => setValue({ ...route.route }), [route.route.model, route.route.provider, route.route.reasoning]);

  const requiresExplicitRoute = route.scope === "vision";
  const provider = value.provider || (requiresExplicitRoute ? "" : snapshot.provider);
  const providerModels = (modelsByProvider[provider] ?? []).filter((item) => !item.disabled);
  const requestedModel = value.model || (provider === snapshot.provider ? snapshot.model : "");
  const model = findModelOption(providerModels, requestedModel)?.id || providerModels[0]?.id || "";
  const modelInfo = findModelOption(providerModels, model);
  const cursorGroups = provider === "cursor" ? groupCursorModelVariants(providerModels) : [];
  const cursorGroup = findCursorModelGroup(cursorGroups, model);
  const cursorVariant = cursorGroup?.variants.find((variant) => variant.model.id === model);
  const reasoning = cursorVariant?.tier || value.reasoning || modelInfo?.defaultReasoning || snapshot.reasoning;
  const cursorThinking = cursorTiersForMode(cursorGroup, true, cursorVariant?.fast ?? false).length > 0;
  const reasoningLevels = sortReasoningLevels(cursorVariant
    ? cursorTiersForMode(cursorGroup, cursorThinking, cursorVariant.fast)
    : [...(modelInfo?.reasoningLevels ?? []), reasoning]);
  const fastCounterpart = cursorVariant && cursorVariantForSelection(
    cursorGroup, reasoning, cursorThinking, !cursorVariant.fast,
  );
  const routeIsEmpty = !value.provider && !value.model && !value.reasoning;
  const title = routeTitle(route, language);
  const configuredModelOptions = routeModelOptions(
    modelsByProvider,
    modelProviders,
    requiresExplicitRoute,
    t("modelImageUnsupported"),
    provider,
    model,
    reasoning,
    language,
  );
  const canClearRoute = requiresExplicitRoute || route.scope === "security";
  const allModelOptions = canClearRoute
    ? [{ value: "::", label: route.scope === "security" ? (language === "zh-CN" ? "继承默认模型" : "Inherit default model") : t("routeNotConfigured") }, ...configuredModelOptions]
    : configuredModelOptions;
  const selectedValue = canClearRoute && routeIsEmpty ? "::" : `${provider}::${model}`;
  const saveValue = (nextValue: ModelRouteConfig) => {
    setValue(nextValue);
    const clear = !nextValue.provider && !nextValue.model && !nextValue.reasoning;
    void action(clear ? "reset_model_route" : "set_model_route", "", { ...route, route: nextValue });
  };
  const selectModel = (next: string) => {
    if (canClearRoute && next === "::") {
      saveValue({ provider: "", model: "", reasoning: "" });
      return;
    }
    const separator = next.indexOf("::");
    const nextProvider = separator >= 0 ? next.slice(0, separator) : provider;
    const nextModelID = separator >= 0 ? next.slice(separator + 2) : next;
    const nextModel = findModelOption(modelsByProvider[nextProvider] ?? [], nextModelID);
    const nextCursorGroup = nextProvider === "cursor"
      ? findCursorModelGroup(groupCursorModelVariants((modelsByProvider[nextProvider] ?? []).filter((item) => !item.disabled)), nextModelID)
      : undefined;
    const nextCursorVariant = nextCursorGroup?.variants.find((variant) => variant.model.id === nextModelID);
    const nextReasoning = nextCursorVariant?.tier || nextModel?.defaultReasoning || reasoning || snapshot.reasoning;
    saveValue({ provider: nextProvider, model: nextModelID, reasoning: nextReasoning });
  };
  const selectReasoning = (nextReasoning: string) => {
    const nextVariant = cursorVariant && cursorVariantForSelection(
      cursorGroup, nextReasoning, cursorThinking, cursorVariant.fast,
    );
    saveValue({
      provider,
      model: nextVariant?.model.id || model,
      reasoning: nextVariant?.tier || nextReasoning,
    });
  };
  const selectCursorFast = () => {
    if (!fastCounterpart) return;
    saveValue({ provider, model: fastCounterpart.model.id, reasoning: fastCounterpart.tier });
  };

  return <div className="route-row" data-setting-id={routeSearchID(route)}>
    <ProviderIcon provider={provider || snapshot.provider} size={19} className="route-provider-icon" />
    <div className="route-copy"><strong>{title}</strong><small>{routeDescription(route, description, language)}</small></div>
    <div className="route-controls">
      <MenuSelect className="route-model-menu" panelClassName="route-model-options" value={selectedValue} options={allModelOptions} onChange={selectModel} ariaLabel={`${title} ${t("model")}`} searchable searchPlaceholder={t("searchModels")} emptyLabel={t("noMatchingModels")} fit="full" showSelectedIcon={false} menuWidth={292} menuAlign="right" />
      <div className="route-effort-controls">
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
        {cursorVariant && <span className="route-cursor-modes">
          <button type="button" className={cursorVariant.fast ? "on" : ""} aria-label={`${title} Fast`} aria-pressed={cursorVariant.fast} disabled={!fastCounterpart} onClick={selectCursorFast}><Zap size={14} /></button>
        </span>}
      </div>
    </div>
  </div>;
}


function routeModelOptions(
  modelsByProvider: Record<string, ModelOption[]>,
  modelProviders: ModelProvider[],
  requiresImages: boolean,
  imageUnsupportedLabel: string,
  selectedProvider: string,
  selectedModel: string,
  selectedReasoning: string,
  language: Language,
) {
  return Object.entries(modelsByProvider).flatMap(([providerID, models]) => {
    const enabled = models.filter((item) => !item.disabled);
    const cursorGroups = providerID === "cursor" ? groupCursorModelVariants(enabled) : [];
    const selectedCursorGroup = selectedProvider === providerID
      ? findCursorModelGroup(cursorGroups, selectedModel)
      : undefined;
    const selectedCursorVariant = selectedCursorGroup?.variants.find((variant) => variant.model.id === selectedModel);
    const choices = providerID === "cursor"
      ? cursorGroups.map((group) => {
        const selectedID = selectedCursorGroup?.id === group.id ? selectedModel : "";
        const selectedFast = selectedCursorVariant?.fast ?? false;
        const defaultThinking = cursorTiersForMode(group, true, selectedFast).length > 0;
        const variant = preferredCursorVariant(
          group,
          selectedCursorVariant?.tier || selectedReasoning,
          defaultThinking,
          selectedFast,
          selectedID,
        );
        return {
          item: variant.model,
          label: group.name,
          keywords: group.variants.flatMap((candidate) => [
            candidate.model.id,
            candidate.model.name ?? "",
            ...(candidate.model.aliases ?? []),
          ]),
          noZDR: variant.noZDR,
        };
      })
      : enabled.map((item) => ({
        item,
        label: modelDisplayName(item.id, item.name),
        keywords: [item.id, ...(item.aliases ?? [])],
        noZDR: false,
      }));
    return choices.map(({ item, label, keywords, noZDR }) => {
      const supportsImages = !item.inputModalities?.length
        || item.inputModalities.some((modality) => modality.toLocaleLowerCase() === "image");
      const imageUnsupported = requiresImages && !supportsImages;
      const providerName = providerDisplayName(providerID, modelProviders);
      const retention = noZDR ? (language === "zh-CN" ? "数据会被保留" : "data retained") : "";
      return {
        value: `${providerID}::${item.id}`,
        label,
        caption: [providerName, imageUnsupported ? imageUnsupportedLabel : "", retention].filter(Boolean).join(" · "),
        keywords: [providerID, providerName, ...keywords],
        icon: <ProviderIcon provider={providerID} />,
        disabled: imageUnsupported,
      };
    });
  });
}

function SettingRow({ label, description, settingID, children }: { label: string; description: string; settingID?: string; children: React.ReactNode }) { return <div className="setting-row" data-setting-id={settingID}><div><strong>{label}</strong><p>{description}</p></div><div>{children}</div></div>; }
function FontSizeControl({ value, min, max, onChange, decreaseLabel, increaseLabel }: {
  value: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
  decreaseLabel: string;
  increaseLabel: string;
}) {
  return <div className="font-size-control">
    <button type="button" onClick={() => onChange(value - 1)} disabled={value <= min} aria-label={decreaseLabel}><span>A−</span></button>
    <output aria-live="polite">{value} px</output>
    <button type="button" onClick={() => onChange(value + 1)} disabled={value >= max} aria-label={increaseLabel}><span>A+</span></button>
  </div>;
}
function systemFontOptions(selected: string, fonts: SystemFont[]) {
  const options = new Map(fonts.map((font) => [font.family, { value: font.family, label: font.label || font.family, caption: font.family }]));
  if (selected !== "system" && !options.has(selected)) options.set(selected, { value: selected, label: selected, caption: selected });
  return [...options.values()];
}
function routeTitle(route: ModelRoute, language: Language) {
  const t = translator(language);
	if (route.scope === "main") return t("routeMain");
  if (route.scope === "title") return t("routeTitle");
  if (route.scope === "plan") return t("routePlan");
  if (route.scope === "approval") return t("routeApproval");
  if (route.scope === "vision") return t("routeVision");
  if (route.scope === "recap") return t("routeRecap");
  if (route.scope === "security") {
    const labels: Record<string, string> = language === "zh-CN"
      ? { audit: "安全审计", reducer: "语义归并", fixer: "修复生成", verifier: "独立验证" }
      : { audit: "Security audit", reducer: "Semantic reducer", fixer: "Fix generation", verifier: "Independent verification" };
    return labels[route.role] || route.label;
  }
  if (route.role === "research") return language === "zh-CN" ? "研究与文档" : "Research and documentation";
  if (route.role === "review") return language === "zh-CN" ? "编码与审查" : "Coding and review";
  return route.role || route.label;
}
function routeDescription(route: ModelRoute, description: string, language: Language) {
  const t = translator(language);
	if (route.scope === "main") return t("routeMainHint");
  if (route.scope === "title") return t("routeTitleHint");
  if (route.scope === "plan") return t("routePlanHint");
  if (route.scope === "approval") return t("routeApprovalHint");
  if (route.scope === "vision") return t("routeVisionHint");
	if (route.scope === "recap") return t("routeRecapHint");
  if (route.scope === "security") {
    const descriptions: Record<string, string> = language === "zh-CN"
      ? { audit: "威胁建模、源码追踪与发现提交", reducer: "合并独立审计结果并保留不确定性", fixer: "在隔离 Worktree 中生成受限修复", verifier: "只读检查变更并提交验证结论" }
      : { audit: "Threat modeling, source tracing, and finding submission", reducer: "Merge independent audits without dropping uncertainty", fixer: "Generate scoped changes in an isolated worktree", verifier: "Read-only change inspection and verification result" };
    return descriptions[route.role] || description;
  }
	if (route.role === "research") return language === "zh-CN" ? "检索、映射、说明文档" : "Research, mapping, and documentation";
	if (route.role === "review") return language === "zh-CN" ? "实现、调试、架构判断" : "Implementation, debugging, and architecture";
  return description || tFormat(language, "routeSubagentHint", { role: route.role || route.label });
}
