import { useEffect, useState } from "react";
import { Check, Save, ShieldCheck } from "lucide-react";
import { execute } from "../bridge";
import { useRuntimeStore } from "../store";
import type { SecurityConfig } from "../types";

type Language = "en" | "zh-CN";

const copy = {
  en: {
    loading: "Loading security configuration",
    execution: "Execution policy",
    executionHint: "Applies to new scans. Active scans keep their captured settings.",
    enabled: "Security scanning",
    enabledHint: "Allow new Standard and Deep scans without deleting prior results.",
    defaultMode: "Default mode",
    standard: "Standard",
    deep: "Deep",
    workers: "Deep workers",
    workersHint: "Concurrent independent audits (1–32).",
    subagents: "Investigators per audit",
    subagentsHint: "Read-only security children (0–32).",
    noNew: "No-new threshold",
    noNewHint: "Consecutive reductions without a new root finding.",
    errors: "Error threshold",
    errorsHint: "Consecutive failures before Deep Scan seals a partial result.",
    runs: "Discovery run cap",
    runsHint: "Maximum independent audits (1–1000).",
    budgets: "Run deadline",
    budgetsHint: "Azem does not impose token or tool-call hard ceilings, so a long security review is not stopped by partial usage accounting.",
    hours: "Deadline (hours)",
    publication: "Publication",
    disabled: "Disabled",
    save: "Save security settings",
    saving: "Saving…",
    saved: "Security configuration saved",
  },
  "zh-CN": {
    loading: "正在加载安全扫描配置",
    execution: "执行策略",
    executionHint: "仅作用于新扫描；运行中的扫描继续使用启动时封存的设置。",
    enabled: "启用安全扫描",
    enabledHint: "允许创建新的标准/深度扫描，不会删除已有结果。",
    defaultMode: "默认模式",
    standard: "标准",
    deep: "深度",
    workers: "深度扫描 Worker",
    workersHint: "并发独立审计数量（1–32）。",
    subagents: "每次审计的调查子代理",
    subagentsHint: "只读安全子代理数量（0–32）。",
    noNew: "无新增阈值",
    noNewHint: "连续多少次归并没有新根问题后停止。",
    errors: "错误阈值",
    errorsHint: "连续失败达到此值后封存部分结果。",
    runs: "发现轮次上限",
    runsHint: "独立审计最大次数（1–1000）。",
    budgets: "运行时限",
    budgetsHint: "Azem 不设置 Token 或工具调用硬上限，避免长时间安全审计因阶段性用量统计而被中途终止。",
    hours: "截止时间（小时）",
    publication: "发布配置",
    disabled: "不启用",
    save: "保存安全扫描设置",
    saving: "正在保存…",
    saved: "安全扫描配置已保存",
  },
} satisfies Record<Language, Record<string, string>>;

export default function SecuritySettings({ language, sessionId, onError, onDirtyChange }: { language: Language; sessionId: string; onError: (message: string) => void; onDirtyChange: (dirty: boolean) => void }) {
  const persisted = useRuntimeStore((state) => state.securityConfig);
  const [draft, setDraft] = useState<SecurityConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [dirty, setDirty] = useState(false);
  const t = copy[language];

  useEffect(() => {
    void execute({ kind: "get_security_config", sessionId }).catch((cause: unknown) => onError(cause instanceof Error ? cause.message : String(cause)));
  }, [onError, sessionId]);
  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    if (!persisted || dirty) return;
    setDraft(JSON.parse(JSON.stringify(persisted)) as SecurityConfig);
  }, [dirty, persisted]);

  const markDirty = () => {
    setDirty(true);
    setStatus("");
  };
  if (!draft) {
    return <div className="settings-card settings-empty" role="status"><span className="azem-mark" />{t.loading}</div>;
  }

  const setNumber = (key: keyof SecurityConfig, value: string) => {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return;
    setDraft((current) => current ? { ...current, [key]: parsed } : current);
    markDirty();
  };
  const save = async () => {
    const next: SecurityConfig = { ...draft };
    const payload = {
      enabled: next.enabled,
      defaultMode: next.defaultMode,
      workers: next.workers,
      subagents: next.subagents,
      stopAfterNoNew: next.stopAfterNoNew,
      stopAfterConsecutiveErrors: next.stopAfterConsecutiveErrors,
      maxDiscoveryRuns: next.maxDiscoveryRuns,
      maxTimeHours: next.maxTimeHours,
    };
    setBusy(true);
    setStatus("");
    onError("");
    try {
      await execute({ kind: "set_security_config", sessionId, payload });
      setDraft(next);
      useRuntimeStore.setState({ securityConfig: next });
      setDirty(false);
      setStatus(t.saved);
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  return <div className="security-settings-stack">
    <section className="settings-card security-settings-card" data-setting-id="security:execution" aria-labelledby="security-execution-title">
      <header><div><strong id="security-execution-title">{t.execution}</strong><small>{t.executionHint}</small></div><button type="button" className={`settings-switch ${draft.enabled ? "on" : ""}`} role="switch" aria-checked={draft.enabled} aria-label={t.enabled} onClick={() => { setDraft({ ...draft, enabled: !draft.enabled }); markDirty(); }}><span /></button></header>
      <div className="security-setting-row"><div><strong>{t.enabled}</strong><small>{t.enabledHint}</small></div><span className={draft.enabled ? "security-config-state enabled" : "security-config-state"}>{draft.enabled ? "ON" : "OFF"}</span></div>
      <fieldset className="security-mode-field"><legend>{t.defaultMode}</legend><div role="radiogroup" aria-label={t.defaultMode}>{(["standard", "deep"] as const).map((mode) => <button key={mode} type="button" role="radio" aria-checked={draft.defaultMode === mode} className={draft.defaultMode === mode ? "selected" : ""} onClick={() => { setDraft({ ...draft, defaultMode: mode }); markDirty(); }}>{mode === "standard" ? t.standard : t.deep}</button>)}</div></fieldset>
      <div className="security-number-grid">
        <NumberField label={t.workers} hint={t.workersHint} value={draft.workers} min={1} max={32} onChange={(value) => setNumber("workers", value)} />
        <NumberField label={t.subagents} hint={t.subagentsHint} value={draft.subagents} min={0} max={32} onChange={(value) => setNumber("subagents", value)} />
        <NumberField label={t.noNew} hint={t.noNewHint} value={draft.stopAfterNoNew} min={1} max={1000} onChange={(value) => setNumber("stopAfterNoNew", value)} />
        <NumberField label={t.errors} hint={t.errorsHint} value={draft.stopAfterConsecutiveErrors} min={1} max={1000} onChange={(value) => setNumber("stopAfterConsecutiveErrors", value)} />
        <NumberField label={t.runs} hint={t.runsHint} value={draft.maxDiscoveryRuns} min={1} max={1000} onChange={(value) => setNumber("maxDiscoveryRuns", value)} />
      </div>
    </section>

    <section className="settings-card security-settings-card" data-setting-id="security:deadline" aria-labelledby="security-deadline-title">
      <header><div><strong id="security-deadline-title">{t.budgets}</strong><small>{t.budgetsHint}</small></div><ShieldCheck size={18} aria-hidden="true" /></header>
      <div className="security-number-grid deadline-grid">
        <NumberField label={t.hours} value={draft.maxTimeHours} min={0.5} max={96} step={0.5} onChange={(value) => setNumber("maxTimeHours", value)} />
      </div>
    </section>

    <section className="settings-card security-settings-card" data-setting-id="security:publication" aria-labelledby="security-publication-title">
      <header><div><strong id="security-publication-title">{t.publication}</strong><small>{language === "zh-CN" ? "外部副作用工具由受信任的 Host 管理员配置，Desktop 只显示状态。" : "External side-effect tools are configured by a trusted host administrator; Desktop shows status only."}</small></div></header>
      <div className="security-publication-locked"><ShieldCheck size={18} aria-hidden="true" /><div><strong>{draft.publicationTool || t.disabled}</strong><small>{language === "zh-CN" ? "在 config.yaml 中管理工具、字段映射和非敏感基础参数。Desktop 不会读取基础参数或密钥。" : "Manage the tool, field mapping, and non-secret base arguments in config.yaml. Desktop never reads base arguments or credentials."}</small></div></div>
    </section>

    <div className="security-settings-save"><span role="status">{status && <><Check size={14} />{status}</>}</span><button type="button" className="settings-primary" disabled={busy || !dirty} onClick={() => void save()}><Save size={14} />{busy ? t.saving : t.save}</button></div>
  </div>;
}

function NumberField({ label, hint, value, min, max, step = 1, onChange }: { label: string; hint?: string; value: number; min: number; max?: number; step?: number; onChange: (value: string) => void }) {
  return <label className="security-number-field"><span><strong>{label}</strong>{hint && <small>{hint}</small>}</span><input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} /></label>;
}
