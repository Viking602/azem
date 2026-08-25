import {
  Bot, Check, ChevronDown, CircleAlert, CircleStop, Command,
  Pencil,
} from "lucide-react";
import { memo, useMemo, useState } from "react";
import { tFormat, translator } from "../../i18n";
import { isSubagentActive } from "../../subagents";
import { useRuntimeStore } from "../../store";
import type { Block, Snapshot } from "../../types";
import { displayedToolState, formatDuration, formatToolPresentation, isHostFallbackCommentary, isRunningTool, thinkingStateLabel } from "../toolTimeline";
import { toolChipBasename, toolChipModel } from "../toolChip";
import AnsiText from "../AnsiText";
import AttachmentPreview from "../AttachmentPreview";
import { CodeDiff, ReasoningPanel } from "../assistant-ui/Elements";
import {
  MessagePairAssistant as AssistantMessage,
  MessagePairError as ErrorMessage,
  MessagePairProgress as ProgressMessage,
  MessagePairUser as UserMessage,
} from "../elements/message-pair";
import { ToolTimelineItem, ToolTimelineMeta, ToolTimelinePending, ToolTimelineStatus } from "../assistant-ui/ToolTimeline";
import {
  fileChangesForBlock, isActiveFileChangeBlock, isPendingFileChangeBlock, pendingFileChangeSummaryForBlock,
  pendingFileEditPaths,
  type EditedFileSummary, type FileChange,
} from "../fileChanges";
import { TimelineProse, normalizeThinkingText, plainStreamingText } from "./streaming";
import { ToolExecutionLog } from "./ToolExecutionLog";
import { ApprovalBlock, PlanBlock, QuestionBlock } from "./planning";
export { approvalPresentation } from "./planning";

export type TimelineBlockProps = {
  block: Block;
  language: Snapshot["language"];
  compact?: boolean;
  nested?: boolean;
  siblings?: Block[];
};

const hostVerificationNotices = [
  "Verification evidence is missing or stale for the current workspace snapshot after one retry. The work remains uncertain and is not reported as complete.",
  "Verification failed for the current workspace snapshot. The attempted result is not reported as complete; inspect the failed checks and correct the work before retrying.",
] as const;

export function visibleAssistantContent(content = "") {
  const trimmed = content.trimEnd();
  for (const notice of hostVerificationNotices) {
    if (trimmed.endsWith(notice)) {
      return trimmed.slice(0, -notice.length).trimEnd();
    }
  }
  return content;
}

function TimelineBlockView({ block, language, compact = false, nested = false, siblings }: TimelineBlockProps) {
  const sessionId = useRuntimeStore((state) => state.currentSessionId || state.snapshot?.sessionId || "");
  if (block.kind === "user") {
    if (block.state === "subagent_wake") {
      return <SubagentWakeNotice block={block} language={language} />;
    }
    return <UserMessage data-session-sequence={block.sequence}>
      {block.attachments?.length ? <div className="user-attachments">{block.attachments.map((item) => <AttachmentPreview key={item.id} attachment={item} sessionId={sessionId} language={language} variant="message" />)}</div> : null}
      {block.content ? <p>{block.content}</p> : null}
    </UserMessage>;
  }
  if (block.kind === "commentary") {
    if (isHostFallbackCommentary(block)) return null;
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    return <ProgressMessage
      className={`markdown timeline-prose ${active ? "active" : ""} ${compact ? "compact" : ""}`}
      data-state={active ? "active" : "completed"}
      aria-label={translator(language)("progressUpdate")}
    >
      <TimelineProse content={block.content || ""} active={active} />
    </ProgressMessage>;
  }
  if (block.kind === "assistant") {
    const active = ["streaming", "running", "started", "progress"].includes(block.state || "");
    const content = visibleAssistantContent(block.content);
    if (!content && !active) return null;
    // Unphased text stays an assistant body. textPhasePending only affects
    // section markers and later commentary promotion — not a pending chrome.
    return <AssistantMessage
      className={`markdown timeline-prose ${active ? "streaming" : ""} ${compact ? "compact" : ""}`}
      data-testid="timeline-prose"
      aria-busy={active || undefined}
      data-session-sequence={block.sequence}
    >
      <TimelineProse
        content={content}
        active={active}
        debugReplay={import.meta.env.DEV && new URLSearchParams(window.location.search).get("demo") === "running"}
      />
    </AssistantMessage>;
  }
  if (block.kind === "question") return <QuestionBlock block={block} language={language} />;
  if (block.kind === "plan") return <PlanBlock block={block} language={language} />;
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") return <ToolTimelineBlock block={block} language={language} compact={compact} nested={nested} siblings={siblings} />;
  if (block.kind === "diff") return <DiffBlock block={block} language={language} nested={nested} />;
  if (block.kind === "approval") return <ApprovalBlock block={block} />;
  if (block.kind === "status") return <RunStatusMarker block={block} language={language} />;
  if (block.kind === "error") {
    return <ErrorMessage><CircleStop size={15} /><div><strong>{block.title}</strong><p>{block.content}</p></div></ErrorMessage>;
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
  return <ToolTimelinePending
    state={status}
    label={t("toolEditFile")}
    chip={paths.length ? paths.map(toolChipBasename).join(" · ") : undefined}
    status={toolStatusLabel(status, language)}
    nested={nested}
  />;
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
  const model = useMemo(() => toolChipModel(block, language), [block, language]);
  const payload = block.content || block.data?.arguments || "";
  const liveOutput = block.data?.output || "";
  const presentation = useMemo(
    () => opened ? formatToolPresentation(payload, language) : null,
    [opened, payload, language],
  );
  const truncated = block.data?.contentTruncated === "true";
  return <ToolTimelineItem
    className={`${nested ? "nested" : ""} ${compact ? "compact" : ""}`.trim()}
    state={state}
    kind={model.kind}
    label={model.label}
    chip={model.chip}
    status={(running || pending || block.state === "failed") ? toolStatusLabel(state, language) : undefined}
    nested={nested}
    compact={compact}
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
    {opened ? (
      <div className="tool-detail">
        <ToolTimelineStatus lines={model.statusLines} />
        <ToolTimelineMeta lines={model.meta} />
        {presentation?.fields.length ? <dl className="tool-fields">
          {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
        </dl> : null}
        {running && liveOutput
          ? <ToolExecutionLog output={liveOutput} label={`${model.label} · ${t("fieldDetail")}`} />
          : presentation?.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
        {truncated ? <p className="tool-result-truncated">{language === "zh-CN" ? "结果过大，已显示安全预览。请缩小路径或搜索范围。" : "Large result: showing a safe preview. Narrow the path or query for more detail."}</p> : null}
        {!presentation?.fields.length && !presentation?.result && !liveOutput && running
          ? <div className="tool-detail-empty">{t("toolExecuting")}</div>
          : null}
      </div>
    ) : null}
  </ToolTimelineItem>;
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
  const active = isActiveReasoning(block);
  const delegated = useRuntimeStore((state) => active && state.agents.some((agent) =>
    isSubagentActive(agent.state) && (!block.runId || agent.parentRunId === block.runId)));
  const [open, setOpen] = useState(false);
  const normalized = normalizeThinkingText(block.content || "");
  const steps = normalized.split(/\n{2,}/u).map(plainStreamingText).filter(Boolean);
  const label = thinkingStateLabel(language, active, Number(block.data?.elapsedMs || 0));
  const panelId = `reasoning-${block.id.replace(/[^a-zA-Z0-9_-]/gu, "-")}`;

  // While delegated work is visible in the conversation, an empty reasoning
  // heartbeat adds a second, non-interactive "thinking" status. The agent card
  // is the actionable source of truth; retain reasoning only once it has text.
  if (delegated && steps.length === 0) return null;

  return <ReasoningPanel
    active={active}
    expanded={open}
    label={label}
    quiet
    panelId={panelId}
    disabled={!steps.length}
    onToggle={() => setOpen((value) => !value)}
  >
    {steps.length ? <div className="aui-reasoning-content" data-tab="reasoning">
      {steps.map((step, index) => <p className="reasoning-step" key={index}>{step}</p>)}
    </div> : null}
  </ReasoningPanel>;
}

export function FileChangeBlock({ changes, summary, language, nested, running = false }: {
  changes: FileChange[];
  summary?: EditedFileSummary;
  language: Snapshot["language"];
  nested: boolean;
  running?: boolean;
}) {
  const t = translator(language);
  const [opened, setOpened] = useState(false);
  const additions = summary?.additions ?? changes.reduce((total, change) => total + change.additions, 0);
  const deletions = summary?.deletions ?? changes.reduce((total, change) => total + change.deletions, 0);
  const pathChip = (summary?.files[0]?.path || changes[0]?.path) ? toolChipBasename(summary?.files[0]?.path || changes[0]?.path || "") : "";
  return <details
    className={`file-change-entry work-entry aui-tool-timeline-item ${nested ? "nested" : ""}`}
    data-state={running ? "running" : "completed"}
    aria-busy={running || undefined}
    onToggle={(event) => setOpened(event.currentTarget.open)}
  >
    <summary>
      <span className="work-entry-icon aui-tool-timeline-icon" data-icon="pencil" aria-hidden="true"><Pencil size={13} /></span>
      <strong className="work-entry-label aui-tool-timeline-label">{t(running ? "editingFiles" : "editedFiles")}</strong>
      {pathChip ? <span className="aui-tool-timeline-detail">{pathChip}</span> : null}
      <span className="file-change-chevron" aria-hidden="true"><ChevronDown size={13} /></span>
      {additions > 0 || deletions > 0 ? <span className="file-change-totals">{additions > 0 ? <span className="plus">+{additions}</span> : null}{deletions > 0 ? <span className="minus">-{deletions}</span> : null}</span> : null}
    </summary>
    {running
      ? <div className="tool-detail-empty">{t("toolExecuting")}</div>
      : opened ? <CodeDiff changes={changes} language={language} insetFromProcessRail /> : null}
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
