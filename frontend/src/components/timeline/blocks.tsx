import {
  Bot, Check, ChevronDown, ChevronRight, CircleAlert, CircleStop, Clock3, Command, FileCode2,
  LoaderCircle, MessageCircleQuestion, PencilLine, Play, ShieldCheck, X,
} from "lucide-react";
import { memo, useMemo, useState } from "react";
import { Markdown } from "../Markdown";
import { execute } from "../../bridge";
import { tFormat, toolDisplayName, translator } from "../../i18n";
import { isSubagentActive } from "../../subagents";
import { useRuntimeStore } from "../../store";
import type { Block, Snapshot } from "../../types";
import { displayedToolState, formatDuration, formatToolPresentation, isRunningTool } from "../toolTimeline";
import AnsiText from "../AnsiText";
import AttachmentPreview from "../AttachmentPreview";
import CodeDiff from "../CodeDiff";
import { ApprovalCard, ThinkingState, ToolRow } from "../beautiful-ui/Primitives";
import {
  fileChangesForBlock, isActiveFileChangeBlock, isPendingFileChangeBlock, pendingFileChangeSummaryForBlock,
  pendingFileEditPaths,
  type EditedFileSummary, type FileChange,
} from "../fileChanges";
import { StreamingText, normalizeThinkingText, plainStreamingText } from "./streaming";
import { ToolExecutionLog } from "./ToolExecutionLog";

export type TimelineBlockProps = {
  block: Block;
  language: Snapshot["language"];
  compact?: boolean;
  nested?: boolean;
  siblings?: Block[];
};

function TimelineBlockView({ block, language, compact = false, nested = false, siblings }: TimelineBlockProps) {
  const sessionId = useRuntimeStore((state) => state.currentSessionId || state.snapshot?.sessionId || "");
  if (block.kind === "user") {
    if (block.state === "subagent_wake") {
      return <SubagentWakeNotice block={block} language={language} />;
    }
    return <article className="user-block" data-session-sequence={block.sequence}>
      {block.attachments?.length ? <div className="user-attachments">{block.attachments.map((item) => <AttachmentPreview key={item.id} attachment={item} sessionId={sessionId} language={language} variant="message" />)}</div> : null}
      {block.content ? <p>{block.content}</p> : null}
    </article>;
  }
  if (block.kind === "commentary") {
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    const label = visibleCommentaryTitle(block.title);
    return <article
      className={`commentary-block markdown ${active ? "active" : ""} ${compact ? "compact" : ""}`}
      data-state={active ? "active" : "completed"}
      aria-label={translator(language)("progressUpdate")}
    >
      <span className="commentary-marker" aria-hidden="true"><i /></span>
      <div className="commentary-content">
        {label ? <small className="commentary-label">{label}</small> : null}
        {active
          ? <StreamingText content={block.content || ""} />
          : <Markdown>{block.content || ""}</Markdown>}
      </div>
    </article>;
  }
  if (block.kind === "assistant") {
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    const phasePending = active && block.data?.textPhasePending === "true";
    // Prose body — primary transcript content (Synara ChatMarkdown tier).
    return <article className={`assistant-block markdown timeline-prose ${active ? "streaming" : ""} ${phasePending ? "phase-pending" : ""} ${compact ? "compact" : ""}`} aria-busy={active || undefined} data-session-sequence={block.sequence}>
      {active
        ? <StreamingText content={block.content || ""} debugReplay={import.meta.env.DEV && new URLSearchParams(window.location.search).get("demo") === "running"} />
        : <Markdown>{block.content || ""}</Markdown>}
    </article>;
  }
  if (block.kind === "question") return <QuestionBlock block={block} language={language} />;
  if (block.kind === "plan") return <PlanBlock block={block} language={language} />;
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") return <ToolTimelineBlock block={block} language={language} compact={compact} nested={nested} siblings={siblings} />;
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

export function visibleCommentaryTitle(title = "") {
  const normalized = title.trim().toLocaleLowerCase();
  const genericTitles = new Set(["progress", "commentary", "progress update", "进度", "进度更新"]);
  return genericTitles.has(normalized) ? "" : title.trim();
}

type SubagentWakeTask = { id: string; type: string; state: string };

function subagentWakeTasks(block: Block): SubagentWakeTask[] {
  const raw = block.data?.tasks;
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.flatMap((item) => {
      if (!item || typeof item !== "object") return [];
      const record = item as Record<string, unknown>;
      const id = typeof record.id === "string" ? record.id.trim() : "";
      if (!id) return [];
      return [{
        id,
        type: typeof record.type === "string" ? record.type : "",
        state: typeof record.state === "string" ? record.state : "",
      }];
    });
  } catch {
    return [];
  }
}

function subagentWakeTitleKey(tasks: SubagentWakeTask[]): "subagentWakeFailed" | "subagentWakeCompleted" | "subagentWakeMixed" {
  if (tasks.length > 0 && tasks.every((task) => task.state === "failed")) return "subagentWakeFailed";
  if (tasks.length > 0 && tasks.every((task) => task.state === "completed")) return "subagentWakeCompleted";
  return "subagentWakeMixed";
}

function SubagentWakeNotice({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const t = translator(language);
  const tasks = subagentWakeTasks(block);
  const failed = tasks.some((task) => task.state === "failed") || (tasks.length === 0 && /reached failed/i.test(block.content || ""));
  const title = t(subagentWakeTitleKey(tasks));
  return <article
    className={`subagent-wake-notice${failed ? " failed" : ""}`}
    data-session-sequence={block.sequence}
    data-state={block.state}
  >
    <span className="subagent-wake-marker" aria-hidden="true">{failed ? <CircleAlert size={15} /> : <Bot size={15} />}</span>
    <div className="subagent-wake-body">
      <strong>{title}</strong>
      {tasks.length ? <ul>
        {tasks.map((task) => <li key={task.id}>
          {tFormat(language, "subagentWakeTask", {
            type: task.type || "subagent",
            id: task.id,
            state: task.state || "unknown",
          })}
        </li>)}
      </ul> : null}
      {block.content ? <details>
        <summary>{t("subagentWakeShowResult")}</summary>
        <pre>{block.content}</pre>
      </details> : null}
    </div>
  </article>;
}

function sameTimelineBlock(previous: TimelineBlockProps, next: TimelineBlockProps) {
  return previous.block === next.block
  && previous.language === next.language
  && previous.compact === next.compact
  && previous.nested === next.nested
  && previous.siblings === next.siblings;
}

export const TimelineBlock = memo(TimelineBlockView, sameTimelineBlock);

export function ToolTimelineBlock({ block, language, compact = false, nested = false, siblings }: TimelineBlockProps) {
  const storeBlocks = useRuntimeStore((state) => state.blocks);
  const peers = siblings?.length ? siblings : storeBlocks;
  const fileChanges = fileChangesForBlock(block);
  if (fileChanges.length) {
    return <FileChangeBlock changes={fileChanges} language={language} nested={nested} />;
  }
  if (isPendingFileChangeBlock(block)) {
    return <PendingFileEditRow block={block} language={language} nested={nested} />;
  }
  const pendingSummary = pendingFileChangeSummaryForBlock(block);
  if (pendingSummary || isActiveFileChangeBlock(block)) {
    return <FileChangeBlock changes={[]} summary={pendingSummary ?? undefined} language={language} nested={nested} running />;
  }
  return <ToolDisclosure block={block} language={language} compact={compact} nested={nested} siblings={peers} />;
}

function PendingFileEditRow({ block, language, nested = false }: { block: Block; language: Snapshot["language"]; nested?: boolean; siblings?: Block[] }) {
  const t = translator(language);
  const awaiting = block.state === "awaiting_approval";
  const status = awaiting ? "awaiting_approval" : "reviewing_approval";
  const paths = pendingFileEditPaths(block);
  return <article
    className={`file-change-entry work-entry pending-file-edit ${nested ? "nested" : ""}`}
    data-state={status}
    aria-busy="true"
  >
    <div className="file-change-pending-summary">
      <span className="work-entry-icon" data-icon="shield" aria-hidden="true">
        <ShieldCheck size={13} />
      </span>
      <span className="work-entry-label">{t("toolEditFile")}</span>
      {paths.length ? <span className="pending-file-edit-path">{paths.join(" · ")}</span> : null}
      <span className="tool-status">{toolStatusLabel(status, language)}</span>
    </div>
  </article>;
}

function ToolDisclosure({ block, language, compact = false, nested = false, siblings }: TimelineBlockProps) {
  const t = translator(language);
  const [opened, setOpened] = useState(false);
  const storeBlocks = useRuntimeStore((state) => state.blocks);
  const peers = siblings?.length ? siblings : storeBlocks;
  const state = displayedToolState(block, peers);
  const running = state === "running" || isRunningTool(block);
  const queued = state === "queued";
  const reviewing = block.state === "reviewing_approval";
  const awaitingApproval = block.state === "awaiting_approval" || reviewing;
  const pending = queued || awaitingApproval;
  const stateIcon = reviewing || block.state === "awaiting_approval"
    ? <ShieldCheck size={12} />
    : running
      ? <LoaderCircle className="spin" size={12} />
      : queued
        ? <Clock3 size={12} />
        : block.state === "failed" || block.state === "cancelled"
          ? <CircleStop size={12} />
          : <Check size={12} />;
  const label = block.title ? toolDisplayName(block.title, language) : t("toolGeneric");
  const payload = block.content || block.data?.arguments || "";
  const liveOutput = block.data?.output || "";
  const previewPayload = payload.length > 8192 ? payload.slice(0, 8192) : payload;
  const preview = useMemo(
    () => formatToolPresentation(previewPayload, language).preview,
    [previewPayload, language],
  );
  const presentation = useMemo(
    () => opened ? formatToolPresentation(payload, language) : null,
    [opened, payload, language],
  );
  const truncated = block.data?.contentTruncated === "true";
  return <ToolRow
    className={`tool-block work-entry ${nested ? "nested" : ""} ${compact ? "compact" : ""}`}
    state={state}
    onToggle={(event) => {
      const target = event.currentTarget;
      const next = target.open;
      setOpened(next);
      if (!next) return;
      requestAnimationFrame(() => {
        const log = target.querySelector<HTMLElement>(".tool-log");
        if (log) log.scrollTop = log.scrollHeight;
      });
    }}
  >
    <summary>
      <span className="tool-leading work-entry-icon" aria-hidden="true">
        <span className="tool-state">{stateIcon}</span>
        <span className="tool-chevron"><ChevronRight className="closed-chevron" size={12} /><ChevronDown className="open-chevron" size={12} /></span>
      </span>
      <span className="tool-summary-text">
        <strong className="work-entry-label">{label}</strong>
        {preview ? <em className="tool-preview">{preview}</em> : null}
      </span>
      {(running || pending || block.state === "failed") ? <span className="tool-status">{toolStatusLabel(state, language)}</span> : null}
    </summary>
    {opened ? (
      <div className="tool-detail">
        {presentation?.fields.length ? <dl className="tool-fields">
          {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
        </dl> : null}
        {running && liveOutput
          ? <ToolExecutionLog output={liveOutput} label={`${label} · ${t("fieldDetail")}`} />
          : presentation?.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
        {truncated ? <p className="tool-result-truncated">{language === "zh-CN" ? "结果过大，已显示安全预览。请缩小路径或搜索范围。" : "Large result: showing a safe preview. Narrow the path or query for more detail."}</p> : null}
        {!presentation?.fields.length && !presentation?.result && !liveOutput && running
          ? <div className="tool-detail-empty">{t("toolExecuting")}</div>
          : null}
      </div>
    ) : null}
  </ToolRow>;
}

type PlanningQuestion = {
  id: string;
  header: string;
  question: string;
  options: Array<{ label: string; description: string; recommended?: boolean }>;
  allow_multiple?: boolean;
};

function parsePlanningQuestions(value = ""): PlanningQuestion[] {
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function planningQuestionAnswered(question: PlanningQuestion, selected: Record<string, string[]>, other: Record<string, string>) {
  return (selected[question.id]?.length ?? 0) > 0 || Boolean(other[question.id]?.trim());
}

function togglePlanningSelection(question: PlanningQuestion, values: string[], label: string) {
  if (!question.allow_multiple) return [label];
  return values.includes(label) ? values.filter((item) => item !== label) : [...values, label];
}

function planningLabels(language: Snapshot["language"]) {
  if (language === "en") return {
    question: "Planning question", recommended: "Recommended", other: "Other",
    otherPlaceholder: "Type another answer", submit: "Submit answers", submitting: "Submitting…", answered: "Answered",
    ask: "Ask about plan", revise: "Request changes", revisePrefix: "Revise the plan with these changes:\n",
    execute: "Execute plan", starting: "Starting…",
  };
  return {
    question: "规划问题", recommended: "推荐", other: "其他",
    otherPlaceholder: "输入其他答案", submit: "提交选择", submitting: "提交中…", answered: "已回答",
    ask: "提出疑问", revise: "修改计划", revisePrefix: "请根据以下要求修改计划：\n",
    execute: "执行计划", starting: "启动中…",
  };
}

function QuestionBlock({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const questions = parsePlanningQuestions(block.data?.questions);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const labels = planningLabels(language);
  const pending = block.state === "pending";
  const ready = questions.length > 0 && questions.every((question) => planningQuestionAnswered(question, selected, other));
  const choose = (question: PlanningQuestion, label: string) => setSelected((current) => {
    const values = current[question.id] ?? [];
    return { ...current, [question.id]: togglePlanningSelection(question, values, label) };
  });
  const submit = async () => {
    if (!ready || submitting) return;
    setSubmitting(true);
    try {
      await execute({
        kind: "resolve_user_input",
        target: block.userInputId || block.data?.userInputId,
        sessionId: currentSessionId,
        payload: {
          answers: questions.map((question) => ({
            question_id: question.id,
            selected: selected[question.id] ?? [],
            other: other[question.id]?.trim() || undefined,
          })),
        },
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setSubmitting(false);
    }
  };
  if (!questions.length) return null;
  return <article className={`planning-question ${pending ? "pending" : "resolved"}`}>
    <header><MessageCircleQuestion size={17} /><div><small>{labels.question}</small><strong>{block.title}</strong></div></header>
    <div className="planning-question-list">
      {questions.map((question) => <fieldset key={question.id} disabled={!pending || submitting}>
        <legend><span>{question.header}</span><strong>{question.question}</strong></legend>
        <div className="planning-options">
          {question.options.map((option) => {
            const active = selected[question.id]?.includes(option.label) ?? false;
            return <button key={option.label} type="button" data-active={String(active)} onClick={() => choose(question, option.label)}>
              <span className="planning-option-mark">{active ? <Check size={13} /> : null}</span>
              <span><strong>{option.label}{option.recommended ? <em>{labels.recommended}</em> : null}</strong><small>{option.description}</small></span>
            </button>;
          })}
          <label className="planning-other"><span>{labels.other}</span><input value={other[question.id] ?? ""} onChange={(event) => setOther((current) => ({ ...current, [question.id]: event.target.value }))} placeholder={labels.otherPlaceholder} /></label>
        </div>
      </fieldset>)}
    </div>
    <footer>{pending
      ? <button className="primary" disabled={!ready || submitting} onClick={submit}>{submitting ? labels.submitting : labels.submit}</button>
      : <span><Check size={14} />{labels.answered}</span>}
    </footer>
  </article>;
}

function planReviewStateLabel(state: string | undefined, language: Snapshot["language"]) {
  if (state === "superseded") return language === "en" ? "Superseded" : "已被新版本替代";
  if (state === "approved") return language === "en" ? "Executing" : "已进入执行";
  return language === "en" ? "Ready for review" : "等待审阅";
}

function PlanBlock({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const running = useRuntimeStore((state) => state.running);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const [submitting, setSubmitting] = useState(false);
  const labels = planningLabels(language);
  const proposed = block.state === "proposed";
  const compose = (prefix: string) => window.dispatchEvent(new CustomEvent("azem:plan-compose", { detail: { prefix } }));
  const executePlan = async () => {
    if (!proposed || running || submitting) return;
    setSubmitting(true);
    try {
      await execute({ kind: "resolve_plan", target: block.planId || block.data?.planId, sessionId: currentSessionId, decision: "execute" });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setSubmitting(false);
    }
  };
  const stateLabel = planReviewStateLabel(block.state, language);
  return <article className="plan-review" data-state={block.state || "proposed"}>
    <header><div><small>{language === "en" ? `Plan v${block.data?.version || "1"}` : `计划 v${block.data?.version || "1"}`}</small><h3>{block.title}</h3></div><span>{stateLabel}</span></header>
    <div className="plan-review-body markdown"><Markdown>{block.content || ""}</Markdown></div>
    {proposed ? <footer>
      <button onClick={() => compose("")}><MessageCircleQuestion size={14} />{labels.ask}</button>
      <button onClick={() => compose(labels.revisePrefix)}><PencilLine size={14} />{labels.revise}</button>
      <button className="primary" disabled={running || submitting} onClick={executePlan}><Play size={14} />{submitting ? labels.starting : labels.execute}</button>
    </footer> : null}
  </article>;
}

export function toolStatusLabel(state: string | undefined, language: Snapshot["language"]) {
  const t = translator(language);
  if (state === "running" || state === "started" || state === "streaming" || state === "progress") return t("toolStatusRunning");
  if (state === "queued") return t("queued");
  if (state === "reviewing_approval") return t("reviewingApproval");
  if (state === "awaiting_approval") return t("needApproval");
  if (state === "failed") return t("toolStatusFailed");
  if (state === "cancelled") return t("cancelled");
  return t("toolStatusDone");
}

export function isActiveReasoning(block: Block) {
  return block.kind === "thinking" && ["streaming", "running", "started", "progress"].includes(block.state || "");
}


export function ReasoningTrace({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const t = translator(language);
  const active = isActiveReasoning(block);
  const delegated = useRuntimeStore((state) => active && state.agents.some((agent) =>
    isSubagentActive(agent.state) && (!block.runId || agent.parentRunId === block.runId)));
  const [open, setOpen] = useState(false);
  const normalized = normalizeThinkingText(block.content || "");
  const steps = normalized.split(/\n{2,}/u).map(plainStreamingText).filter(Boolean);
  const elapsedMs = Number(block.data?.elapsedMs || 0);
  const label = active
    ? t("thinkingActive")
    : elapsedMs > 0
      ? tFormat(language, "thoughtFor", { duration: formatDuration(elapsedMs) })
      : t("thought");
  const panelId = `reasoning-${block.id.replace(/[^a-zA-Z0-9_-]/gu, "-")}`;

  // While delegated work is visible in the conversation, an empty reasoning
  // heartbeat adds a second, non-interactive "thinking" status. The agent card
  // is the actionable source of truth; retain reasoning only once it has text.
  if (delegated && steps.length === 0) return null;

  return <ThinkingState active={active} expanded={open} label={label} panelId={panelId} disabled={!steps.length} onToggle={() => setOpen((value) => !value)}>
    {steps.map((step, index) => <p className="reasoning-step" key={index}>{step}</p>)}
  </ThinkingState>;
}

export function FileChangeBlock({ changes, summary, language, nested, running = false }: {
  changes: FileChange[];
  summary?: EditedFileSummary;
  language: Snapshot["language"];
  nested: boolean;
  running?: boolean;
}) {
  const t = translator(language);
  const additions = summary?.additions ?? changes.reduce((total, change) => total + change.additions, 0);
  const deletions = summary?.deletions ?? changes.reduce((total, change) => total + change.deletions, 0);
  return <details
    className={`file-change-entry work-entry ${nested ? "nested" : ""}`}
    data-state={running ? "running" : "completed"}
    aria-busy={running || undefined}
  >
    <summary>
      <span className="work-entry-icon" aria-hidden="true"><FileCode2 size={13} /></span>
      <span className="work-entry-label">{t(running ? "editingFiles" : "editedFiles")}</span>
      <span className="file-change-chevron" aria-hidden="true"><ChevronDown size={13} /></span>
      {additions > 0 || deletions > 0 ? <span className="file-change-totals">{additions > 0 ? <span className="plus">+{additions}</span> : null}{deletions > 0 ? <span className="minus">-{deletions}</span> : null}</span> : null}
    </summary>
    {running
      ? <div className="tool-detail-empty">{t("toolExecuting")}</div>
      : <CodeDiff changes={changes} language={language} insetFromProcessRail />}
  </details>;
}

export function RunStatusMarker({ block, language }: { block: Block; language: Snapshot["language"] }) {
  if (block.data?.variant === "artifact") {
    const [path, details = ""] = (block.content || "").split("\n", 2);
    const progress = Math.min(100, Math.max(0, Number(block.data?.progress || 0)));
    return <article className="timeline-artifact-card">
      <Command size={15} />
      <div><strong>{block.title}</strong><small>{path}</small><footer>{details.split(" · ").filter(Boolean).map((item) => <span key={item}>{item}</span>)}</footer></div>
      <aside><span>{block.state === "running" ? translator(language)("running") : translator(language)("completed")}</span><i><b style={{ width: `${progress}%` }} /></i></aside>
    </article>;
  }
  if (block.data?.variant === "section") {
    return <div className="run-status-marker section-marker"><span>{block.title}</span></div>;
  }
  const elapsedMs = Number(block.data?.elapsedMs || 0);
  const label = elapsedMs > 0
    ? tFormat(language, "stoppedAfter", { duration: formatDuration(elapsedMs) })
    : translator(language)("stopped");
  return <div className="run-status-marker"><span>{label}</span></div>;
}

export function DiffBlock({ block, language, nested }: { block: Block; language: Snapshot["language"]; nested: boolean }) {
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
  return <ApprovalCard className={pending ? "pending" : "resolved"} state={pending ? "pending" : "resolved"} data-risk={details.riskTone}>
    <header className="approval-heading"><span className="approval-icon"><ShieldCheck size={17} /></span><div><small>{t("approvalTitle")}</small><strong>{details.tool}</strong></div><span className="approval-risk">{details.riskLabel}</span></header>
    <div className="approval-target"><span>{t("approvalTarget")}</span><code>{details.target}</code></div>
    <footer className="approval-footer"><p>{details.description}</p><div className="approval-actions">{pending ? <><button onClick={() => resolve("deny")}>{t("deny")}</button><button onClick={() => resolve("once")}>{t("approveOnce")}</button><button className="primary" onClick={() => resolve("session")}>{t("approveSession")}</button></> : <span className={denied ? "denied" : "approved"}>{denied ? <X size={14} /> : <Check size={14} />}{resolvedLabel}</span>}</div></footer>
  </ApprovalCard>;
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
