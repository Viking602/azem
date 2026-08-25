import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle, ArrowLeft, Ban, CheckCircle2, FileJson2, GitPullRequest, LoaderCircle, Play, RotateCcw, ShieldCheck, ShieldX,
} from "lucide-react";
import { execute } from "../../bridge";
import { useRuntimeStore } from "../../store";
import type { SecurityFinding, SecurityPatchResult, SecurityScan, SecurityTriage } from "../../types";
interface SecurityCopy {
  eyebrow: string; title: string; description: string; standard: string; deep: string;
  empty: string; emptyHint: string; loading: string; findings: string; coverage: string; reviewed: string;
  workers: string; model: string; cancel: string; resume: string; export: string; patch: string;
  noFindings: string; noFindingsHint: string; scanning: string; scanningHint: string;
  remediation: string; locations: string; verification: string; selectFinding: string;
  exportReady: string; scanList: string; startScan: string; severity: string; confidence: string; taxonomy: string;
  confirmDeep: string; confirmCancel: string; confirmPatch: string; confirmTriage: string; workspace: string;
  triage: string; open: string; falsePositive: string; alreadyFixed: string; wontFix: string;
}

const copy: Record<"en" | "zh-CN", SecurityCopy> = {
  en: {
    eyebrow: "Application security", title: "Security", description: "Evidence-backed source review on an immutable project snapshot.",
    standard: "Standard scan", deep: "Deep scan", empty: "No security scans yet", emptyHint: "Start a Standard scan for one complete audit or a Deep scan for independent repeated audits.",
    loading: "Loading security scans", findings: "Findings", coverage: "Coverage", reviewed: "Reviewed files", workers: "Workers", model: "Model", cancel: "Cancel scan", resume: "Resume scan", export: "Export SARIF",
    patch: "Patch and verify", noFindings: "No reportable findings", noFindingsHint: "Check coverage before treating this as a clean bill of health.",
    scanning: "Review in progress", scanningHint: "Findings appear after the immutable snapshot has been reviewed and finalized.",
    remediation: "Remediation", locations: "Locations", verification: "Verification", selectFinding: "Select a finding to inspect complete evidence.",
    exportReady: "Export saved", scanList: "Security scans", startScan: "Start a security scan", severity: "Severity", confidence: "Confidence", taxonomy: "Taxonomy",
    confirmDeep: "Deep Scan runs repeated independent audits and can use substantially more provider time. Continue?",
    confirmCancel: "Cancel this scan permanently? A canceled scan cannot be resumed.",
    confirmPatch: "Create an isolated worktree, apply a fix to the finding's authorized files, and run independent verification?", confirmTriage: "Close this finding with the selected reason?", workspace: "Workspace",
    triage: "Triage", open: "Open", falsePositive: "False positive", alreadyFixed: "Already fixed", wontFix: "Won't fix",
  },
  "zh-CN": {
    eyebrow: "应用安全", title: "安全扫描", description: "基于不可变项目快照的证据化源码审计。",
    standard: "标准扫描", deep: "深度扫描", empty: "还没有安全扫描", emptyHint: "标准扫描执行一次完整审计；深度扫描会运行多次独立审计并做语义归并。",
    loading: "正在加载安全扫描", findings: "安全发现", coverage: "覆盖率", reviewed: "已审查文件", workers: "审计 Worker", model: "模型", cancel: "取消扫描", resume: "继续扫描", export: "导出 SARIF",
    patch: "修复并验证", noFindings: "没有可报告的发现", noFindingsHint: "在将结果视为安全结论前，请先检查覆盖率。",
    scanning: "正在审查", scanningHint: "不可变快照完成审查并封印后，安全发现会显示在这里。",
    remediation: "修复建议", locations: "代码位置", verification: "验证", selectFinding: "选择一条安全发现以查看完整证据。",
    exportReady: "导出已保存", scanList: "安全扫描列表", startScan: "启动安全扫描", severity: "严重程度", confidence: "置信度", taxonomy: "分类",
    confirmDeep: "深度扫描会执行多轮独立审计，可能显著增加模型运行时间。是否继续？",
    confirmCancel: "永久取消此扫描？取消后无法继续。",
    confirmPatch: "创建隔离 Worktree、仅修改该发现授权的文件并执行独立验证？", confirmTriage: "按所选原因关闭这条安全发现？", workspace: "工作区",
    triage: "分类处理", open: "重新打开", falsePositive: "误报", alreadyFixed: "已修复", wontFix: "不修复",
  },
};

export default function SecurityPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const scans = useRuntimeStore((state) => state.securityScans);
  const scansLoaded = useRuntimeStore((state) => state.securityScansLoaded);
  const latestProjection = useRuntimeStore((state) => state.securityProjection);
  const projections = useRuntimeStore((state) => state.securityProjections);
  const findingsByScan = useRuntimeStore((state) => state.securityFindingsByScan);
  const selectedFinding = useRuntimeStore((state) => state.selectedSecurityFinding);
  const patch = useRuntimeStore((state) => state.securityPatch);
  const exportPath = useRuntimeStore((state) => state.securityExportPath);
  const error = useRuntimeStore((state) => state.error);
  const setError = useRuntimeStore((state) => state.setError);
  const setView = useRuntimeStore((state) => state.setView);
  const [busy, setBusy] = useState("");
  const [listFailed, setListFailed] = useState(false);
  const [selectedScanId, setSelectedScanId] = useState("");
  const t = copy[snapshot.language];
  const projection = selectedScanId ? projections[selectedScanId] ?? (latestProjection?.scan.id === selectedScanId ? latestProjection : null) : latestProjection;
  const selected = projection?.scan ?? scans.find((scan) => scan.id === selectedScanId) ?? scans[0] ?? null;
  const findings = selected ? findingsByScan[selected.id] ?? projection?.findings ?? [] : [];
  const selectedTriage = selectedFinding ? projection?.triage?.[selectedFinding.occurrenceId] : undefined;

  useEffect(() => {
    let active = true;
    let fallback = 0;
    setListFailed(false);
    void execute({ kind: "list_security_scans", target: snapshot.workspace, limit: 100, sessionId: snapshot.sessionId })
      .then(() => {
        fallback = window.setTimeout(() => {
          if (active && !useRuntimeStore.getState().securityScansLoaded) setListFailed(true);
        }, 100);
      })
      .catch((error: unknown) => {
        if (active) setListFailed(true);
        setError(error instanceof Error ? error.message : String(error));
      });
    return () => {
      active = false;
      window.clearTimeout(fallback);
    };
  }, [setError, snapshot.sessionId, snapshot.workspace]);

  useEffect(() => {
    if (!selectedScanId && scans[0]) setSelectedScanId(scans[0].id);
  }, [scans, selectedScanId]);

  const start = async (mode: "standard" | "deep") => {
    if (mode === "deep" && !window.confirm(t.confirmDeep)) return;
    setSelectedScanId("");
    setBusy(mode);
    setError("");
    try {
      await execute({
        kind: "start_security_scan", sessionId: snapshot.sessionId,
        payload: {
          projectId: snapshot.workspace, repository: snapshot.workspace, targetKind: "repository", mode,
          route: { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning },
        },
      });
    } catch (error) { setError(error instanceof Error ? error.message : String(error)); }
    finally { setBusy(""); }
  };

  const openScan = async (scan: SecurityScan) => {
    setSelectedScanId(scan.id);
    try {
      await execute({ kind: "get_security_scan", target: scan.id, sessionId: snapshot.sessionId });
      await execute({ kind: "list_security_findings", target: scan.id, sessionId: snapshot.sessionId });
    } catch (error) { setError(error instanceof Error ? error.message : String(error)); }
  };

  const openFinding = (finding: SecurityFinding) => execute({ kind: "get_security_finding", target: finding.occurrenceId, sessionId: snapshot.sessionId })
    .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));

  const runScanAction = async (name: string, action: Parameters<typeof execute>[0]) => {
    setBusy(name);
    setError("");
    try {
      await execute(action);
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : String(actionError));
    } finally {
      setBusy("");
    }
  };

  const progress = projection?.progress;
  const reviewPercent = progress?.filesTotal ? Math.min(100, Math.round((progress.filesCompleted / progress.filesTotal) * 100)) : 0;
  const severityCounts = useMemo(() => findings.reduce<Record<string, number>>((result, finding) => {
    result[finding.severity.level] = (result[finding.severity.level] ?? 0) + 1;
    return result;
  }, {}), [findings]);
  const scanActive = selected ? !["complete", "failed", "canceled"].includes(selected.status) : false;
  const liveStatus = selected
    ? `${t.reviewed}: ${progress?.filesCompleted ?? 0}/${progress?.filesTotal ?? 0} · ${selected.status}`
    : "";

  return <main className="security-page" aria-labelledby="security-title">
    <header className="security-page-header">
      <div className="workspace-subpage-heading">
        <button type="button" className="workspace-back-link" onClick={() => setView("projects")} aria-label={t.workspace}><ArrowLeft size={14} /><span>{t.workspace}</span><kbd>Esc</kbd></button>
        <div><span className="security-eyebrow">{t.eyebrow}</span><h1 id="security-title">{t.title}</h1><p>{t.description}</p></div>
      </div>
      <div className="security-start-actions" aria-label={t.startScan}>
        <button className="primary" disabled={Boolean(busy)} onClick={() => void start("standard")}><Play size={15} />{t.standard}</button>
        <button disabled={Boolean(busy)} onClick={() => void start("deep")}><ShieldCheck size={15} />{t.deep}</button>
      </div>
    </header>

    <div className="security-layout">
      <nav className="security-scan-rail" aria-label={t.scanList}>
        {!(scansLoaded || listFailed) && scans.length === 0 ? <div className="security-empty-rail" role="status"><LoaderCircle className="security-spinner" size={22} /><strong>{t.loading}</strong></div>
          : scans.length === 0 ? <div className="security-empty-rail"><ShieldCheck size={22} /><strong>{t.empty}</strong><p>{t.emptyHint}</p></div>
            : <ul>{scans.map((scan) => <li key={scan.id}>
              <button className={selected?.id === scan.id ? "active" : ""} onClick={() => void openScan(scan)} aria-pressed={selected?.id === scan.id}>
                <span className={`security-status status-${scan.status}`}>{scan.status}</span>
                <strong>{scan.mode === "deep" ? t.deep : t.standard}</strong>
                <small title={`${shortDate(scan.createdAt)} · ${scan.route.model}`}>{shortDate(scan.createdAt)} · {scan.route.model}</small>
              </button>
            </li>)}</ul>}
      </nav>

      <section className="security-content">
        <span className="sr-only" role="status" aria-live="polite">{liveStatus}</span>
        {error && <div className="security-error" role="alert">{error}</div>}
        {exportPath && <div className="security-export-status" role="status"><strong>{t.exportReady}:</strong> <code>{exportPath}</code></div>}
        {!selected ? (!(scansLoaded || listFailed)
          ? <div className="security-empty" role="status"><LoaderCircle className="security-spinner" size={38} /><h2>{t.loading}</h2></div>
          : <div className="security-empty"><ShieldCheck size={38} /><h2>{t.empty}</h2><p>{t.emptyHint}</p></div>) : <>
          <div className="security-scan-summary" aria-busy={scanActive}>
            <div className="security-summary-title"><StatusIcon status={selected.status} /><div><span>{selected.id}</span><h2>{selected.target.displayName}</h2><span className={`security-state-badge status-${selected.status}`}>{selected.status}</span><p>{selected.blockingReason || selected.warning || selected.failureMessage || progress?.message || selected.phase}</p></div></div>
            <div className="security-summary-actions">
              {selected.status === "blocked" && <button disabled={Boolean(busy)} onClick={() => void runScanAction("resume", { kind: "resume_security_scan", target: selected.id, sessionId: snapshot.sessionId })}><RotateCcw size={14} />{t.resume}</button>}
              {scanActive && <button disabled={Boolean(busy)} onClick={() => { if (window.confirm(t.confirmCancel)) void runScanAction("cancel", { kind: "cancel_security_scan", target: selected.id, sessionId: snapshot.sessionId }); }}><Ban size={14} />{t.cancel}</button>}
              {selected.status === "complete" && <button disabled={Boolean(busy)} onClick={() => void runScanAction("export", { kind: "export_security_scan", target: selected.id, decision: "sarif", sessionId: snapshot.sessionId })}><FileJson2 size={14} />{t.export}</button>}
            </div>
          </div>

          <dl className="security-metrics">
            <div><dt>{t.coverage}</dt><dd>{selected.completeness || "—"}</dd></div>
            <div><dt>{t.reviewed}</dt><dd>{progress ? `${progress.filesCompleted}/${progress.filesTotal}` : "—"}</dd></div>
            <div><dt>{t.workers}</dt><dd>{progress ? `${progress.workersDone ?? 0}/${progress.workersPlanned ?? 0}` : "—"}</dd></div>
            <div><dt>{t.model}</dt><dd>{selected.route.provider}/{selected.route.model}</dd></div>
          </dl>
          <progress className="security-progress" max={100} value={reviewPercent} aria-label={`${t.reviewed}: ${reviewPercent}%`} />

          <div className="security-findings-heading"><div><span>{t.findings}</span><strong>{findings.length}</strong></div><div className="security-severity-summary">{["critical", "high", "medium", "low"].map((level) => <span key={level}>{level} {severityCounts[level] ?? 0}</span>)}</div></div>
          {findings.length === 0 ? (scanActive
            ? <div className="security-no-findings in-progress"><LoaderCircle className="security-spinner" size={25} aria-hidden="true" /><div><h3>{t.scanning}</h3><p>{t.scanningHint}</p></div></div>
            : <div className="security-no-findings"><CheckCircle2 size={25} /><div><h3>{t.noFindings}</h3><p>{t.noFindingsHint}</p></div></div>)
            : <div className="security-finding-grid">
            <ul className="security-finding-list">{findings.map((finding) => <li key={finding.occurrenceId}><button aria-pressed={selectedFinding?.occurrenceId === finding.occurrenceId} className={selectedFinding?.occurrenceId === finding.occurrenceId ? "active" : ""} onClick={() => void openFinding(finding)}>
              <span className={`severity-mark severity-${finding.severity.level}`}>{finding.severity.level}</span><div><strong title={finding.title}>{finding.title}</strong><p>{finding.summary}</p><small title={`${finding.locations[0]?.path}:${finding.locations[0]?.startLine}`}>{finding.locations[0]?.path}:{finding.locations[0]?.startLine}</small></div>
            </button></li>)}</ul>
            <FindingDetail finding={selectedFinding} triage={selectedTriage} patch={patch} label={t} sessionId={snapshot.sessionId} provider={snapshot.provider} model={snapshot.model} reasoning={snapshot.reasoning} />
          </div>}
        </>}
      </section>
    </div>
  </main>;
}

function FindingDetail({ finding, triage, patch, label, sessionId, provider, model, reasoning }: { finding: SecurityFinding | null; triage?: SecurityTriage; patch: SecurityPatchResult | null; label: SecurityCopy; sessionId: string; provider: string; model: string; reasoning: string }) {
  const setError = useRuntimeStore((state) => state.setError);
  const [patching, setPatching] = useState(false);
  const [triaging, setTriaging] = useState("");
  if (!finding) return <aside className="security-finding-detail empty"><p>{label.selectFinding}</p></aside>;
  const patchFinding = async () => {
    if (!window.confirm(label.confirmPatch)) return;
    setPatching(true);
    setError("");
    try {
      await execute({ kind: "patch_security_findings", sessionId, payload: { occurrenceIds: [finding.occurrenceId], route: { provider, model, reasoning } } });
    } catch (patchError) {
      setError(patchError instanceof Error ? patchError.message : String(patchError));
    } finally {
      setPatching(false);
    }
  };
  const setTriage = async (status: "open" | "closed", closeReason = "") => {
    if (status === "closed" && !window.confirm(label.confirmTriage)) return;
    setTriaging(closeReason || status);
    setError("");
    try {
      await execute({
        kind: "set_security_finding_triage", sessionId,
        payload: { occurrenceId: finding.occurrenceId, status, closeReason },
      });
    } catch (triageError) {
      setError(triageError instanceof Error ? triageError.message : String(triageError));
    } finally {
      setTriaging("");
    }
  };
  return <aside className="security-finding-detail" aria-labelledby="security-finding-title">
    <header><span>{finding.ruleId}</span><h3 id="security-finding-title">{finding.title}</h3><p>{finding.summary}</p></header>
    <dl className="security-finding-classification">
      <div><dt>{label.severity}</dt><dd>{finding.severity.level}</dd></div>
      <div><dt>{label.confidence}</dt><dd>{finding.confidence.level}</dd></div>
      <div><dt>{label.taxonomy}</dt><dd>{[finding.taxonomy.category, ...(finding.taxonomy.cwe ?? [])].filter(Boolean).join(" · ")}</dd></div>
    </dl>
    <section><h4>{label.locations}</h4><ul>{finding.locations.map((location) => <li key={`${location.path}:${location.startLine}`}><code>{location.path}:{location.startLine}{location.endLine && location.endLine !== location.startLine ? `-${location.endLine}` : ""}</code><span>{location.role}</span></li>)}</ul></section>
    <section><h4>{label.remediation}</h4><p>{finding.remediation}</p></section>
    {patch?.occurrenceId === finding.occurrenceId && <section role="status"><h4>{label.verification}</h4><p>{patch.verification || patch.reason || patch.status}</p></section>}
    <section className="security-triage"><h4>{label.triage}</h4><p role="status">{triage?.status === "closed" ? `${triage.status} · ${triage.closeReason}` : label.open}</p><div>
      <button disabled={Boolean(triaging) || triage?.status !== "closed"} onClick={() => void setTriage("open")}>{label.open}</button>
      <button disabled={Boolean(triaging) || triage?.closeReason === "false_positive"} onClick={() => void setTriage("closed", "false_positive")}>{label.falsePositive}</button>
      <button disabled={Boolean(triaging) || triage?.closeReason === "already_fixed"} onClick={() => void setTriage("closed", "already_fixed")}>{label.alreadyFixed}</button>
      <button disabled={Boolean(triaging) || triage?.closeReason === "wont_fix"} onClick={() => void setTriage("closed", "wont_fix")}>{label.wontFix}</button>
    </div></section>
    <button disabled={patching} className="primary security-patch-button" onClick={() => void patchFinding()}><GitPullRequest size={15} />{label.patch}</button>
  </aside>;
}

function StatusIcon({ status }: { status: SecurityScan["status"] }) {
  if (status === "complete") return <CheckCircle2 className="status-icon complete" aria-hidden="true" />;
  if (status === "failed") return <ShieldX className="status-icon failed" aria-hidden="true" />;
  if (status === "canceled") return <Ban className="status-icon failed" aria-hidden="true" />;
  if (status === "blocked") return <AlertTriangle className="status-icon blocked" aria-hidden="true" />;
  return <LoaderCircle className="status-icon running security-spinner" aria-hidden="true" />;
}

function shortDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
}
