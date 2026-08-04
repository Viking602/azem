import {
  Check, ChevronDown, ChevronRight, CircleStop, FileCode2, ImagePlus,
  LoaderCircle, ShieldCheck, Sparkles, X,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { execute } from "../bridge";
import { tFormat, toolDisplayName, translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { Block, Snapshot } from "../types";
import {
  formatDuration, formatToolPresentation, groupTimelineBlocks, isRunningTool, segmentProcessTrail,
} from "./toolTimeline";

export function TimelineFeed({ blocks, language, compact = false, activeRunId = "", running = false }: {
  blocks: Block[];
  language: Snapshot["language"];
  compact?: boolean;
  activeRunId?: string;
  running?: boolean;
}) {
  // Side chat stays flat; main transcript folds completed process trails like Codex.
  if (compact) {
    const entries = groupTimelineBlocks(blocks, language);
    return <div className="timeline-feed compact">
      {entries.map((entry) => entry.kind === "tool-group"
        ? <ToolGroup key={entry.id} blocks={entry.blocks} summary={entry.summary} language={language} />
        : <TimelineBlock key={entry.block.id} block={entry.block} language={language} compact />)}
    </div>;
  }

  const segments = segmentProcessTrail(blocks, { activeRunId, running });
  return <div className="timeline-feed">
    {segments.map((segment) => {
      if (segment.kind === "block") {
        return <TimelineBlock key={segment.block.id} block={segment.block} language={language} />;
      }
      if (segment.active) {
        return <ProcessEntries key={segment.id} blocks={segment.blocks} language={language} />;
      }
      return <ProcessFold key={segment.id} blocks={segment.blocks} elapsedMs={segment.elapsedMs} language={language} />;
    })}
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

function ProcessEntries({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const entries = groupTimelineBlocks(blocks, language);
  return <>
    {entries.map((entry) => entry.kind === "tool-group"
      ? <ToolGroup key={entry.id} blocks={entry.blocks} summary={entry.summary} language={language} />
      : <TimelineBlock key={entry.block.id} block={entry.block} language={language} />)}
  </>;
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
  if (block.kind === "assistant") {
    // Prose body — primary transcript content (Synara ChatMarkdown tier).
    return <article className={`assistant-block markdown timeline-prose ${compact ? "compact" : ""}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{block.content || ""}</ReactMarkdown>
    </article>;
  }
  if (block.kind === "thinking") {
    const t = translator(language);
    // Work-log tier — meta activity; still render markdown so **headings** display correctly.
    return <details className="thinking-block work-entry" open={block.state === "streaming"}>
      <summary>
        <span className="work-entry-icon" aria-hidden="true"><Sparkles size={13} /></span>
        <span className="work-entry-label">{t("thinking")}</span>
        {block.state === "streaming" && <span className="live-dot" />}
      </summary>
      {block.content ? (
        <div className="thinking-content markdown">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{normalizeThinkingMarkdown(block.content)}</ReactMarkdown>
        </div>
      ) : null}
    </details>;
  }
  if (block.kind === "tool") {
    const t = translator(language);
    const running = isRunningTool(block);
    const label = block.title ? toolDisplayName(block.title, language) : t("toolGeneric");
    // Prefer content; fall back to start-payload arguments so running tools still show path/command.
    const payload = block.content || block.data?.arguments || "";
    const presentation = formatToolPresentation(payload, language);
    const showPreview = Boolean(presentation.preview);
    const hasDetail = presentation.fields.length > 0 || Boolean(presentation.result);
    // Only auto-expand runners that have something to show — empty open rows thrash the UI.
    return <details className={`tool-block work-entry ${nested ? "nested" : ""} ${compact ? "compact" : ""}`} open={running && hasDetail && !nested}>
      <summary>
        <span className="tool-leading work-entry-icon" aria-hidden="true">
          <span className="tool-state">{running ? <LoaderCircle className="spin" size={12} /> : <Check size={12} />}</span>
          <span className="tool-chevron"><ChevronRight className="closed-chevron" size={12} /><ChevronDown className="open-chevron" size={12} /></span>
        </span>
        <span className="tool-summary-text">
          <strong className="work-entry-label">{label}</strong>
          {showPreview ? <em className="tool-preview">{presentation.preview}</em> : null}
        </span>
        {(running || block.state === "failed") ? <span className="tool-status">{toolStatusLabel(block.state, language)}</span> : null}
      </summary>
      {hasDetail ? (
        <div className="tool-detail">
          {presentation.fields.length > 0 && <dl className="tool-fields">
            {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
          </dl>}
          {presentation.result ? <pre className="tool-result">{presentation.result}</pre> : null}
        </div>
      ) : running ? (
        <div className="tool-detail tool-detail-empty">{t("toolExecuting")}</div>
      ) : null}
    </details>;
  }
  if (block.kind === "diff") return <DiffBlock block={block} />;
  if (block.kind === "approval") return <ApprovalBlock block={block} />;
  if (block.kind === "error") {
    return <article className="error-block"><CircleStop size={15} /><div><strong>{block.title}</strong><p>{block.content}</p></div></article>;
  }
  // agent / hook are filtered by segmentProcessTrail; keep a quiet fallback for nested callers.
  if (block.kind === "agent" || block.kind === "hook") return null;
  return null;
}

function toolStatusLabel(state: string | undefined, language: Snapshot["language"]) {
  const t = translator(language);
  if (state === "running" || state === "started" || state === "streaming") return t("toolStatusRunning");
  if (state === "failed") return t("toolStatusFailed");
  return t("toolStatusDone");
}

/** Repair glued thinking segments (`**A****B**` → separate markdown paragraphs). */
export function normalizeThinkingMarkdown(content: string) {
  return content
    .replace(/\*\*\*\*/g, "**\n\n**")
    .replace(/([^\n])\n\*\*/g, "$1\n\n**")
    .trim();
}

function DiffBlock({ block }: { block: Block }) {
  const lines = (block.content || "").split("\n");
  let oldLine = 0;
  let newLine = 0;
  return <article className="diff-block"><header><FileCode2 size={15} /><strong>{block.title}</strong><span className="toolbar-spacer" /><span className="plus">+{block.data?.additions || lines.filter((line) => line.startsWith("+")).length}</span><span className="minus">−{block.data?.deletions || lines.filter((line) => line.startsWith("-")).length}</span></header><pre>{lines.map((line, index) => {
    const hunk = line.startsWith("@@");
    const add = line.startsWith("+") && !line.startsWith("+++");
    const del = line.startsWith("-") && !line.startsWith("---");
    if (hunk) { const match = /@@ -(\d+)/.exec(line); oldLine = Number(match?.[1] || 0); const next = /\+(\d+)/.exec(line); newLine = Number(next?.[1] || 0); }
    else if (add) newLine += 1; else if (del) oldLine += 1; else { oldLine += 1; newLine += 1; }
    return <span key={`${index}-${line}`} className={hunk ? "hunk" : add ? "added" : del ? "deleted" : "context"}><i>{hunk ? "" : del ? oldLine : add ? newLine : newLine}</i><b>{hunk ? "" : add ? "+" : del ? "−" : " "}</b><code>{line.replace(/^[+-]/, "")}</code></span>;
  })}</pre></article>;
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
