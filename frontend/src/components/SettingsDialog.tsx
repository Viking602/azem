import { useEffect, useRef, useState } from "react";
import { Bot, ChevronRight, Gauge, Languages, Palette, RefreshCw, Settings2, Sparkles, X } from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { ModelRoute, ModelRouteConfig } from "../types";

export default function SettingsDialog() {
  const dialog = useRef<HTMLDialogElement>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
  const skills = useRuntimeStore((state) => state.skills);
  const theme = useRuntimeStore((state) => state.theme);
  const approvalMode = useRuntimeStore((state) => state.approvalMode);
  const setTheme = useRuntimeStore((state) => state.setTheme);
  const setLanguage = useRuntimeStore((state) => state.setLanguage);
  const close = () => useRuntimeStore.getState().setSettingsOpen(false);
  const setError = useRuntimeStore((state) => state.setError);
  const [concurrency, setConcurrency] = useState(snapshot.subagentConcurrency);
  const t = translator(snapshot.language);

  useEffect(() => { dialog.current?.showModal(); }, []);
  const action = async (kind: string, target = "", route?: ModelRoute) => {
    try { await execute({ kind, target, route }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  return <dialog ref={dialog} className="settings-dialog" onCancel={(event) => { event.preventDefault(); close(); }} onClick={(event) => { if (event.target === dialog.current) close(); }}>
    <div className="dialog-frame">
      <header><div><span>Settings</span><h1>{t("settings")}</h1><p>先展开功能分类，再修改其中的具体设置。</p></div><button className="icon-button" onClick={close} aria-label="关闭"><X size={17} /></button></header>
      <div className="settings-scroll">
        <details className="setting-section" open>
          <summary><Bot size={16} /><strong>{t("roleModels")}</strong><span>{modelRoutes.length} 个可配置角色</span><ChevronRight size={15} /></summary>
          <div className="setting-body role-routes">{modelRoutes.map((route) => <RouteRow key={`${route.Scope}-${route.Role}`} route={route} action={action} />)}</div>
        </details>
        <details className="setting-section">
          <summary><Gauge size={16} /><strong>{t("subagentRuntime")}</strong><span>最多同时运行 {snapshot.subagentConcurrency} 个任务</span><ChevronRight size={15} /></summary>
          <div className="setting-body"><SettingRow label={t("concurrency")} description="只限制同时运行数量；排队任务会等待空闲槽位。"><input className="number-input" type="number" min={1} max={64} value={concurrency} onChange={(event) => setConcurrency(Number(event.target.value))} /><button className="small-button" onClick={() => action("set_subagent_concurrency", String(concurrency))}>保存</button></SettingRow></div>
        </details>
        <details className="setting-section">
          <summary><Sparkles size={16} /><strong>{t("codexSubscription")}</strong><span>{snapshot.chatgptFastMode ? "已开启 · Fast" : "已关闭 · 标准速度"}</span><ChevronRight size={15} /></summary>
          <div className="setting-body"><SettingRow label={t("fastMode")} description="仅对支持订阅加速的 ChatGPT/Codex 请求生效。"><label className="switch"><input type="checkbox" checked={snapshot.chatgptFastMode} onChange={(event) => action("set_chatgpt_fast_mode", String(event.target.checked))} /><span /></label></SettingRow></div>
        </details>
        <details className="setting-section">
          <summary><Settings2 size={16} /><strong>运行治理</strong><span>{approvalLabel(approvalMode)}</span><ChevronRight size={15} /></summary>
          <div className="setting-body"><SettingRow label="默认审批模式" description="高风险操作仍会由 Go 运行时执行最终校验。"><select value={approvalMode} onChange={(event) => action("set_approval_mode", event.target.value)}><option value="prompt">逐次审批</option><option value="auto_review">自动审查</option><option value="yolo">始终允许</option></select></SettingRow></div>
        </details>
        <details className="setting-section">
          <summary><Palette size={16} /><strong>{t("appearance")}</strong><span>{snapshot.language === "zh-CN" ? "简体中文" : "English"} · {themeLabel(theme)}</span><ChevronRight size={15} /></summary>
          <div className="setting-body"><SettingRow label={t("language")} description="同步修改运行时语言与桌面界面。"><select value={snapshot.language} onChange={(event) => { const value = event.target.value as "en" | "zh-CN"; setLanguage(value); action("set_language", value); }}><option value="zh-CN">简体中文</option><option value="en">English</option></select></SettingRow><SettingRow label={t("theme")} description="只保存桌面视觉偏好，不改变运行时配置。"><select value={theme} onChange={(event) => setTheme(event.target.value as "light" | "dark" | "system")}><option value="system">{t("system")}</option><option value="light">{t("light")}</option><option value="dark">{t("dark")}</option></select></SettingRow></div>
        </details>
        <details className="setting-section">
          <summary><Languages size={16} /><strong>Skills 与扩展</strong><span>{skills.filter((skill) => !skill.disabled).length} 个已启用</span><ChevronRight size={15} /></summary>
          <div className="setting-body"><div className="setting-inline-actions"><p>Skills 由运行时发现，设置界面不会直接修改本地文件。</p><button className="small-button" onClick={() => action("reload_skills")}><RefreshCw size={13} />重新加载</button></div>{skills.slice(0, 8).map((skill) => <div className="mini-skill" key={skill.name}><span>{skill.name}</span><small>{skill.disabled ? "已停用" : skill.eager ? "默认加载" : "按需加载"}</small></div>)}</div>
        </details>
      </div>
      <footer><span>Esc 关闭</span><button className="primary-button" onClick={close}>完成</button></footer>
    </div>
  </dialog>;
}

function RouteRow({ route, action }: { route: ModelRoute; action: (kind: string, target?: string, route?: ModelRoute) => Promise<void> }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const [value, setValue] = useState<ModelRouteConfig>({ ...route.Route });
  const inherited = !value.provider && !value.model && !value.reasoning;
  const canSave = Boolean(value.provider?.trim() && value.model?.trim());
  const update = (field: keyof ModelRouteConfig, next: string) => setValue((current) => ({ ...current, [field]: next }));
  return <div className="route-row"><div><strong>{route.Label || route.Role || route.Scope}</strong><small>{route.Scope === "plan" ? "只读调查并输出可执行计划" : route.Scope === "compaction" ? "压缩长会话时使用的模型路由" : `${route.Role || "agent"} 子代理的模型路由`}</small></div><div className="route-inputs"><input aria-label="Provider" placeholder={snapshot.provider} value={value.provider || ""} onChange={(event) => update("provider", event.target.value)} /><input aria-label="Model" placeholder={snapshot.model} value={value.model || ""} onChange={(event) => update("model", event.target.value)} /><select aria-label="Reasoning" value={value.reasoning || ""} onChange={(event) => update("reasoning", event.target.value)}><option value="">继承</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="xhigh">xhigh</option></select><button className="small-button" disabled={!canSave} onClick={() => action("set_model_route", "", { ...route, Route: value })}>保存</button><button className="text-button" disabled={inherited} onClick={() => { setValue({}); action("reset_model_route", "", route); }}>重置</button></div></div>;
}

function SettingRow({ label, description, children }: { label: string; description: string; children: React.ReactNode }) { return <div className="setting-row"><div><strong>{label}</strong><p>{description}</p></div><div>{children}</div></div>; }
function approvalLabel(value: string) { return value === "auto_review" ? "自动审查" : value === "yolo" ? "始终允许" : "逐次审批"; }
function themeLabel(value: string) { return value === "dark" ? "深色" : value === "light" ? "浅色" : "跟随系统"; }
