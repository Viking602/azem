import {
  Check, ChevronDown, ChevronRight, CircleStop, Clock3, FileCode2, FilePenLine, ImagePlus,
  LoaderCircle, ShieldCheck, X,
} from "lucide-react";
import { Fragment, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { execute } from "../bridge";
import { tFormat, toolDisplayName, translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { Block, Snapshot } from "../types";
import {
  formatDuration, formatToolPresentation, groupTimelineBlocks, isRunningTool, segmentProcessTrail,
} from "./toolTimeline";
import AnsiText from "./AnsiText";
import CodeDiff from "./CodeDiff";
import { aggregateEditedFiles, fileChangesForBlock, type EditedFileSummary, type FileChange } from "./fileChanges";

export function TimelineFeed({
  blocks, language, compact = false, activeRunId = "", running = false, waitingForModel = false,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  compact?: boolean;
  activeRunId?: string;
  running?: boolean;
  waitingForModel?: boolean;
}) {
  // Side chat stays flat; main transcript folds completed process trails like Codex.
  if (compact) {
    return <div className="timeline-feed compact">
      <ProcessEntries blocks={blocks} language={language} compact />
    </div>;
  }

  const segments = segmentProcessTrail(blocks, { activeRunId, running });
  const decorated = segments.map((segment, index) => ({
    segment,
    index,
    runId: segment.kind === "block"
      ? segment.block.runId || ""
      : segment.blocks.find((block) => block.runId)?.runId || "",
  }));
  const blocksByRun = new Map<string, Block[]>();
  for (const block of blocks) {
    if (!block.runId) continue;
    const runBlocks = blocksByRun.get(block.runId) ?? [];
    runBlocks.push(block);
    blocksByRun.set(block.runId, runBlocks);
  }
  const changesByRun = new Map<string, EditedFileSummary>();
  for (const [runId, runBlocks] of blocksByRun) {
    const summary = aggregateEditedFiles(runBlocks);
    if (summary.files.length) changesByRun.set(runId, summary);
  }
  const lastContentIndexByRun = new Map<string, number>();
  for (const item of decorated) {
    if (!item.runId || (item.segment.kind === "block" && item.segment.block.kind === "status")) continue;
    lastContentIndexByRun.set(item.runId, item.index);
  }
  const showThinkingPlaceholder = waitingForModel && !blocks.some((block) => isActiveReasoning(block)
    && (!activeRunId || !block.runId || block.runId === activeRunId));

  return <div className="timeline-feed">
    {decorated.map(({ segment, index, runId }) => {
      const content = segment.kind === "block"
        ? <TimelineBlock block={segment.block} language={language} />
        : segment.active
          ? <ProcessEntries blocks={segment.blocks} language={language} active />
          : <ProcessFold blocks={segment.blocks} elapsedMs={segment.elapsedMs} language={language} />;
      const summary = changesByRun.get(runId);
      const showSummary = summary
        && lastContentIndexByRun.get(runId) === index
        && !(running && runId === activeRunId);
      const key = segment.kind === "block" ? segment.block.id : segment.id;
      return <Fragment key={key}>
        {content}
        {showSummary ? <EditedFilesSummary summary={summary} language={language} /> : null}
      </Fragment>;
    })}
    {showThinkingPlaceholder ? <ThinkingPlaceholder language={language} /> : null}
  </div>;
}

function ProcessFold({ blocks, elapsedMs, language }: {
  blocks: Block[];
  elapsedMs: number;
  language: Snapshot["language"];
}) {
  const label = elapsedMs > 0
    ? tFormat(language, "processedFor", { duration: formatDuration(elapsedMs) })
    : translator(language)("processed");
  return <details className="process-fold">
    <summary>
      <span className="process-fold-chevrons" aria-hidden="true">
        <ChevronRight className="closed-chevron" size={14} />
        <ChevronDown className="open-chevron" size={14} />
      </span>
      <span className="process-fold-label">{label}</span>
    </summary>
    <div className="process-fold-body">
      <ProcessEntries blocks={blocks} language={language} />
    </div>
  </details>;
}

function ProcessEntries({ blocks, language, active = false, compact = false }: {
  blocks: Block[];
  language: Snapshot["language"];
  active?: boolean;
  compact?: boolean;
}) {
  const entries = groupTimelineBlocks(blocks, language);
  return <div className={`process-entries ${active ? "active" : ""}`} aria-live={active ? "polite" : undefined} aria-busy={active || undefined}>
    {entries.map((entry) => entry.kind === "tool-group"
      ? <ToolGroup key={entry.id} blocks={entry.blocks} summary={entry.summary} language={language} />
      : <TimelineBlock key={entry.block.id} block={entry.block} language={language} compact={compact} />)}
  </div>;
}


function ToolGroup({ blocks, summary, language }: { blocks: Block[]; summary: string; language: Snapshot["language"] }) {
  return <details className="tool-group work-entry">
    <summary>
      <span className="tool-leading work-entry-icon" aria-hidden="true">
        <span className="tool-state"><Check size={12} /></span>
        <span className="tool-chevron"><ChevronRight className="closed-chevron" size={12} /><ChevronDown className="open-chevron" size={12} /></span>
      </span>
      <span className="tool-summary-text"><strong className="work-entry-label">{summary}</strong></span>
      <span className="tool-count">{blocks.length}</span>
    </summary>
    <div className="tool-group-body">
      {blocks.map((block) => <TimelineBlock key={block.id} block={block} language={language} compact nested />)}
    </div>
  </details>;
}

export function TimelineBlock({ block, language, compact = false, nested = false }: {
  block: Block;
  language: Snapshot["language"];
  compact?: boolean;
  nested?: boolean;
}) {
  if (block.kind === "user") {
    return <article className="user-block">
      {block.attachments?.length ? <div className="user-attachments">{block.attachments.map((item) => <span key={item.id}><ImagePlus size={13} />{item.name}</span>)}</div> : null}
      <p>{block.content}</p>
    </article>;
  }
  if (block.kind === "commentary") {
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    return <article
      className={`commentary-block markdown ${active ? "active" : ""} ${compact ? "compact" : ""}`}
      aria-label={translator(language)("progressUpdate")}
    >
      <span className="commentary-marker" aria-hidden="true"><i /></span>
      <div className="commentary-content">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{block.content || ""}</ReactMarkdown>
      </div>
    </article>;
  }
  if (block.kind === "assistant") {
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    // Prose body — primary transcript content (Synara ChatMarkdown tier).
    return <article className={`assistant-block markdown timeline-prose ${active ? "streaming" : ""} ${compact ? "compact" : ""}`} aria-busy={active || undefined}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{block.content || ""}</ReactMarkdown>
    </article>;
  }
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") {
    const t = translator(language);
    const running = isRunningTool(block);
    const fileChanges = fileChangesForBlock(block);
    if (fileChanges.length) {
      return <FileChangeBlock changes={fileChanges} language={language} nested={nested} />;
    }
    const queued = block.state === "queued";
    const awaitingApproval = block.state === "awaiting_approval" || block.state === "reviewing_approval";
    const pending = queued || awaitingApproval;
    const stateIcon = running
      ? <LoaderCircle className="spin" size={12} />
      : awaitingApproval
        ? <ShieldCheck size={12} />
        : queued
          ? <Clock3 size={12} />
          : block.state === "failed" || block.state === "cancelled"
            ? <CircleStop size={12} />
            : <Check size={12} />;
    const label = block.title ? toolDisplayName(block.title, language) : t("toolGeneric");
    // Prefer content; fall back to start-payload arguments so running tools still show path/command.
    const payload = block.content || block.data?.arguments || "";
    const presentation = formatToolPresentation(payload, language);
    const liveOutput = block.data?.output || "";
    const showPreview = Boolean(presentation.preview);
    const hasDetail = presentation.fields.length > 0 || Boolean(presentation.result) || Boolean(liveOutput);
    // Keep disclosure state browser-owned so live log updates never undo the user's toggle.
    return <details
      className={`tool-block work-entry ${nested ? "nested" : ""} ${compact ? "compact" : ""}`}
      onToggle={(event) => {
        if (!event.currentTarget.open) return;
        const log = event.currentTarget.querySelector<HTMLElement>(".tool-log");
        if (log) log.scrollTop = log.scrollHeight;
      }}
    >
      <summary>
        <span className="tool-leading work-entry-icon" aria-hidden="true">
          <span className="tool-state">{stateIcon}</span>
          <span className="tool-chevron"><ChevronRight className="closed-chevron" size={12} /><ChevronDown className="open-chevron" size={12} /></span>
        </span>
        <span className="tool-summary-text">
          <strong className="work-entry-label">{label}</strong>
          {showPreview ? <em className="tool-preview">{presentation.preview}</em> : null}
        </span>
        {(running || pending || block.state === "failed") ? <span className="tool-status">{toolStatusLabel(block.state, language)}</span> : null}
      </summary>
      {hasDetail ? (
        <div className="tool-detail">
          {presentation.fields.length > 0 && <dl className="tool-fields">
            {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
          </dl>}
          {running && liveOutput
            ? <ToolExecutionLog output={liveOutput} label={`${label} · ${t("fieldDetail")}`} />
            : presentation.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
        </div>
      ) : running ? (
        <div className="tool-detail tool-detail-empty">{t("toolExecuting")}</div>
      ) : null}
    </details>;
  }
  if (block.kind === "diff") return <DiffBlock block={block} language={language} nested={nested} />;
  if (block.kind === "approval") return <ApprovalBlock block={block} />;
  if (block.kind === "status") return <RunStatusMarker block={block} language={language} />;
  if (block.kind === "error") {
    return <article className="error-block"><CircleStop size={15} /><div><strong>{block.title}</strong><p>{block.content}</p></div></article>;
  }
  // agent / hook are filtered by segmentProcessTrail; keep a quiet fallback for nested callers.
  if (block.kind === "agent" || block.kind === "hook") return null;
  return null;
}

function ToolExecutionLog({ output, label }: { output: string; label: string }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [output]);
  return <pre ref={ref} className="tool-result tool-log" tabIndex={0} aria-label={label} aria-live="off"><AnsiText text={output} /></pre>;
}

function toolStatusLabel(state: string | undefined, language: Snapshot["language"]) {
  const t = translator(language);
  if (state === "running" || state === "started" || state === "streaming" || state === "progress") return t("toolStatusRunning");
  if (state === "queued") return t("queued");
  if (state === "awaiting_approval" || state === "reviewing_approval") return t("needApproval");
  if (state === "failed") return t("toolStatusFailed");
  if (state === "cancelled") return t("cancelled");
  return t("toolStatusDone");
}

function normalizeThinkingText(content: string) {
  return content
    .replace(/\*\*\*\*/g, "**\n\n**")
    .replace(/([^\n])\n\*\*/g, "$1\n\n**")
    .trim();
}

function plainThinkingText(content: string) {
  return content
    .replace(/^```[^\n]*\n?/gmu, "")
    .replace(/^```$/gmu, "")
    .replace(/^#{1,6}[ \t]+/gmu, "")
    .replace(/^>[ \t]?/gmu, "")
    .replace(/^(\s*)(?:[-*+]|\d+[.)])[ \t]+/gmu, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/gu, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/gu, "$1")
    .replace(/`([^`\n]+)`/gu, "$1")
    .replace(/\*\*([^*]+)\*\*/gu, "$1")
    .replace(/__([^_]+)__/gu, "$1")
    .replace(/~~([^~]+)~~/gu, "$1")
    .trim();
}
function isActiveReasoning(block: Block) {
  return block.kind === "thinking" && ["streaming", "running", "started", "progress"].includes(block.state || "");
}


function ReasoningTrace({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const t = translator(language);
  const active = isActiveReasoning(block);
  const [open, setOpen] = useState(false);
  const normalized = normalizeThinkingText(block.content || "");
  const steps = normalized.split(/\n{2,}/u).map(plainThinkingText).filter(Boolean);
  const elapsedMs = Number(block.data?.elapsedMs || 0);
  const label = active
    ? t("thinkingActive")
    : elapsedMs > 0
      ? tFormat(language, "thoughtFor", { duration: formatDuration(elapsedMs) })
      : t("thought");
  const panelId = `reasoning-${block.id.replace(/[^a-zA-Z0-9_-]/gu, "-")}`;

  return <section className={`reasoning-trace ${active ? "streaming" : "completed"} ${open ? "open" : ""}`} aria-busy={active || undefined}>
    <button
      className="reasoning-summary"
      type="button"
      aria-expanded={open}
      aria-controls={steps.length ? panelId : undefined}
      onClick={() => setOpen((value) => !value)}
      disabled={!steps.length}
    >
      <ReasoningMark active={active} />
      <ReasoningLabel label={label} active={active} />
      {steps.length ? <ChevronDown className="reasoning-chevron" size={13} aria-hidden="true" /> : null}
    </button>
    {steps.length ? <div className="reasoning-body" id={panelId} hidden={!open}>
      {steps.map((step, index) => <p className="reasoning-step" key={index}>{step}</p>)}
    </div> : null}
  </section>;
}

function ThinkingPlaceholder({ language }: { language: Snapshot["language"] }) {
  const label = translator(language)("thinkingActive");
  return <div className="reasoning-trace reasoning-placeholder streaming" role="status" aria-live="polite" aria-busy="true">
    <div className="reasoning-summary">
      <ReasoningMark active />
      <ReasoningLabel label={label} active />
    </div>
  </div>;
}

function ReasoningMark({ active }: { active: boolean }) {
  return <span className={`azem-thinking-mark ${active ? "active" : ""}`} aria-hidden="true">
    <i /><i />
  </span>;
}

function ReasoningLabel({ label, active }: { label: string; active: boolean }) {
  return <span className={`reasoning-label ${active ? "active" : ""}`}>
    <span className="reasoning-label-base">{label}</span>
    {active ? <span className="reasoning-label-sweep" aria-hidden="true">
      <span className="reasoning-label-highlight">{label}</span>
    </span> : null}
  </span>;
}

function FileChangeBlock({ changes, language, nested }: {
  changes: FileChange[];
  language: Snapshot["language"];
  nested: boolean;
}) {
  const t = translator(language);
  const additions = changes.reduce((total, change) => total + change.additions, 0);
  const deletions = changes.reduce((total, change) => total + change.deletions, 0);
  return <details className={`file-change-entry work-entry ${nested ? "nested" : ""}`}>
    <summary>
      <span className="work-entry-icon" aria-hidden="true"><FileCode2 size={13} /></span>
      <span className="work-entry-label">{t("editedFiles")}</span>
      <span className="file-change-chevron" aria-hidden="true"><ChevronDown size={13} /></span>
      <span className="file-change-totals"><span className="plus">+{additions}</span><span className="minus">−{deletions}</span></span>
    </summary>
    <CodeDiff changes={changes} language={language} insetFromProcessRail />
  </details>;
}

function RunStatusMarker({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const elapsedMs = Number(block.data?.elapsedMs || 0);
  const label = elapsedMs > 0
    ? tFormat(language, "stoppedAfter", { duration: formatDuration(elapsedMs) })
    : translator(language)("stopped");
  return <div className="run-status-marker"><span>{label}</span></div>;
}

function EditedFilesSummary({ summary, language }: { summary: EditedFileSummary; language: Snapshot["language"] }) {
  const t = translator(language);
  const label = summary.files.length === 1
    ? t("editedOneFile")
    : tFormat(language, "editedFileCount", { count: String(summary.files.length) });
  return <article className="edited-files-summary">
    <header>
      <span className="edited-files-icon" aria-hidden="true"><FilePenLine size={16} /></span>
      <div><strong>{label}</strong><span><b className="plus">+{summary.additions}</b><b className="minus">−{summary.deletions}</b></span></div>
    </header>
    <ul>{summary.files.map((file) => <li key={file.path}>
      <span title={file.path}>{file.path}</span>
      <span><b className="plus">+{file.additions}</b><b className="minus">−{file.deletions}</b></span>
    </li>)}</ul>
  </article>;
}

function DiffBlock({ block, language, nested }: { block: Block; language: Snapshot["language"]; nested: boolean }) {
  const changes = fileChangesForBlock(block);
  return changes.length ? <FileChangeBlock changes={changes} language={language} nested={nested} /> : null;
}

function ApprovalBlock({ block }: { block: Block }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const details = approvalPresentation(block, snapshot.language);
  const resolve = async (decision: string) => {
    try { await execute({ kind: "resolve_approval", target: block.approvalId, decision }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const pending = block.state === "pending";
  const denied = block.state === "deny" || block.state === "denied";
  const resolvedLabel = denied ? t("deny") : block.state === "session" ? t("approveSession") : t("approveOnce");
  return <article className={`approval-block ${pending ? "pending" : "resolved"}`} data-risk={details.riskTone}>
    <header className="approval-heading"><span className="approval-icon"><ShieldCheck size={17} /></span><div><small>{t("approvalTitle")}</small><strong>{details.tool}</strong></div><span className="approval-risk">{details.riskLabel}</span></header>
    <div className="approval-target"><span>{t("approvalTarget")}</span><code>{details.target}</code></div>
    <footer className="approval-footer"><p>{details.description}</p><div className="approval-actions">{pending ? <><button onClick={() => resolve("deny")}>{t("deny")}</button><button onClick={() => resolve("once")}>{t("approveOnce")}</button><button className="primary" onClick={() => resolve("session")}>{t("approveSession")}</button></> : <span className={denied ? "denied" : "approved"}>{denied ? <X size={14} /> : <Check size={14} />}{resolvedLabel}</span>}</div></footer>
  </article>;
}

export function approvalPresentation(block: Block, language: Snapshot["language"]) {
  const t = translator(language);
  const riskTone = block.data?.risk === "low" || block.data?.risk === "high" ? block.data.risk : "medium";
  const riskLabel = riskTone === "low" ? t("riskLow") : riskTone === "high" ? t("riskHigh") : t("riskMedium");
  const effect = block.data?.effect;
  const description = effect === "write" ? t("approvalWrite") : effect === "external_side_effect" ? t("approvalExternal") : effect === "read_only" ? t("approvalReadOnly") : t("approvalConfirm");
  return {
    tool: block.data?.tool ? toolDisplayName(block.data.tool, language) : t("approvalOperation"),
    target: block.data?.target?.trim() || t("approvalWorkspace"),
    riskTone,
    riskLabel,
    description,
  };
}
