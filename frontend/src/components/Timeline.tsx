import {
  Check, ChevronDown, ChevronRight, CircleStop, Clock3, Command, FileCode2, FilePenLine,
  LoaderCircle, MessageCircleQuestion, PencilLine, Play, ShieldCheck, X,
} from "lucide-react";
import { Fragment, memo, useEffect, useMemo, useRef, useState } from "react";
import { Markdown, StreamingMarkdown, type StreamingRevealRange } from "./Markdown";
import { execute } from "../bridge";
import { tFormat, toolDisplayName, translator } from "../i18n";
import {
  isSubagentActive, isSubagentTerminal, subagentDisplayName, subagentPreviewText,
  subagentStatusLabel, subagentSummaryLabel,
} from "../subagents";
import { useRuntimeStore } from "../store";
import type { AgentState, Block, Snapshot } from "../types";
import {
  formatDuration, formatToolPresentation, groupProcessTimelineBlocks, isActiveProcessBlock, isRunningTool,
  processElapsedMs, summarizeToolGroup, type ProcessTimelineEntry,
} from "./toolTimeline";
import AnsiText from "./AnsiText";
import AttachmentPreview from "./AttachmentPreview";
import CodeDiff from "./CodeDiff";
import SubagentGlyph from "./SubagentGlyph";
import {
  fileChangesForBlock, isActiveFileChangeBlock, pendingFileChangeSummaryForBlock,
  type EditedFileSummary, type FileChange,
} from "./fileChanges";
import {
  projectSessionDocument,
  turnEditedFiles,
  type SessionTurn,
  type SessionTurnItem,
} from "./sessionDocument";

function TimelineFeedView({
  blocks, language, compact = false, activeRunId = "", running = false, waitingForModel = false,
  foldActiveProcess = true, collapseCompletedProcess = false,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  compact?: boolean;
  activeRunId?: string;
  running?: boolean;
  waitingForModel?: boolean;
  foldActiveProcess?: boolean;
  collapseCompletedProcess?: boolean;
}) {
  const activeDelegation = useRuntimeStore((state) => Boolean(activeRunId) && state.agents.some((agent) =>
    isSubagentActive(agent.state) && agent.parentRunId === activeRunId));
  // Side chat stays flat/compact; main session uses document projection.
  if (compact) {
    return <div className="timeline-feed compact">
      <ProcessEntries blocks={blocks} language={language} compact />
    </div>;
  }

  // Document mode projects blocks into reading turns. Process is a foldable
  // attachment by default so the answer body stays the primary surface.
  const foldProcess = foldActiveProcess;
  const collapseCompleted = collapseCompletedProcess;
  const projection = projectSessionDocument(blocks, { activeRunId, running });
  const runningSpawn = blocks.some((block) => isSubagentSpawnBlock(block) && isRunningTool(block));
  const showThinkingPlaceholder = waitingForModel && !activeDelegation && !runningSpawn
    && !blocks.some((block) => isActiveReasoning(block)
      && (!activeRunId || !block.runId || block.runId === activeRunId));

  const multiTurn = projection.turns.length > 1;
  return <div className="timeline-feed session-document">
    {projection.turns.map((turn, index) => {
      const current = index === projection.currentIndex;
      return current
        ? <CurrentSessionTurn
            key={turn.id}
            turn={turn}
            language={language}
            activeRunId={activeRunId}
            running={running}
            multiTurn={multiTurn}
            foldActiveProcess={foldProcess}
            collapseCompletedProcess={collapseCompleted}
          />
        : <HistorySessionTurn
            key={turn.id}
            turn={turn}
            language={language}
            index={index}
            activeRunId={activeRunId}
            running={running}
            foldActiveProcess={foldProcess}
            collapseCompletedProcess
          />;
    })}
    {showThinkingPlaceholder ? <ThinkingPlaceholder language={language} /> : null}
  </div>;
}

function CurrentSessionTurn({
  turn, language, activeRunId, running, multiTurn, foldActiveProcess, collapseCompletedProcess,
}: {
  turn: SessionTurn;
  language: Snapshot["language"];
  activeRunId: string;
  running: boolean;
  multiTurn: boolean;
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
}) {
  const t = translator(language);
  const fileSummary = turnEditedFiles(turn, { activeRunId, running });
  return <section
    className={`session-turn session-turn-current${multiTurn ? " has-history-context" : ""}`}
    data-screen-label="current-turn"
  >
    {multiTurn ? <div className="session-turn-label">
      <span className="session-turn-index current">{t("currentTurn")}</span>
      {running ? <em data-live="true">{t("processing")}</em> : null}
    </div> : null}
    {turn.user ? <TimelineBlock block={turn.user} language={language} /> : null}
    <TurnItems
      turn={turn}
      language={language}
      foldActiveProcess={foldActiveProcess}
      collapseCompletedProcess={collapseCompletedProcess}
    />
    {fileSummary ? <EditedFilesSummary summary={fileSummary} language={language} /> : null}
  </section>;
}

/** Past turns stay fully expanded so users scroll up — no fold-row chrome. */
function HistorySessionTurn({
  turn, language, index, activeRunId, running, foldActiveProcess, collapseCompletedProcess,
}: {
  turn: SessionTurn;
  language: Snapshot["language"];
  index: number;
  activeRunId: string;
  running: boolean;
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
}) {
  const t = translator(language);
  const fileSummary = turnEditedFiles(turn, { activeRunId, running });
  return <section className="session-turn session-history-turn" data-turn={String(index + 1).padStart(2, "0")}>
    <div className="session-turn-label">
      <span className="session-turn-index">{tFormat(language, "turnIndex", { n: String(index + 1).padStart(2, "0") })}</span>
    </div>
    {turn.user ? <TimelineBlock block={turn.user} language={language} /> : null}
    <TurnItems
      turn={turn}
      language={language}
      foldActiveProcess={foldActiveProcess}
      collapseCompletedProcess={collapseCompletedProcess}
    />
    {fileSummary ? <EditedFilesSummary summary={fileSummary} language={language} /> : null}
  </section>;
}

function TurnItems({
  turn, language, foldActiveProcess, collapseCompletedProcess,
}: {
  turn: SessionTurn;
  language: Snapshot["language"];
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
}) {
  const processIndexes = turn.items
    .map((item, index) => item.kind === "process" ? index : -1)
    .filter((index) => index >= 0);
  const latestProcessIndex = processIndexes.at(-1) ?? -1;
  const processRuns = new Set(
    turn.items.flatMap((item) => {
      if (item.kind === "process") {
        return item.blocks.map((block) => block.runId || "").filter(Boolean);
      }
      return item.block.runId ? [item.block.runId] : [];
    }),
  );

  return <>
    {turn.items.map((item, index) => {
      const previous = turn.items[index - 1];
      return <Fragment key={itemKey(item)}>
        {shouldShowAnswerSection(item, previous, processRuns) ? <AnswerSection language={language} /> : null}
        <TurnItemView
          item={item}
          language={language}
          featured={item.kind === "process" && index === latestProcessIndex}
          foldActiveProcess={foldActiveProcess}
          collapseCompletedProcess={collapseCompletedProcess}
        />
      </Fragment>;
    })}
  </>;
}

function itemKey(item: SessionTurnItem) {
  return item.kind === "process" ? item.id : item.block.id;
}

function shouldShowAnswerSection(
  item: SessionTurnItem,
  previous: SessionTurnItem | undefined,
  processRuns: Set<string>,
) {
  if (item.kind !== "block" || item.block.kind !== "assistant") return false;
  if (item.block.data?.textPhasePending === "true") return false;
  const explicit = previous?.kind === "block"
    && previous.block.kind === "status"
    && previous.block.data?.variant === "section";
  if (explicit) return false;
  const runId = item.block.runId || "";
  return Boolean(runId && processRuns.has(runId));
}

function TurnItemView({
  item, language, featured, foldActiveProcess, collapseCompletedProcess,
}: {
  item: SessionTurnItem;
  language: Snapshot["language"];
  featured: boolean;
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
}) {
  if (item.kind === "block") {
    return <TimelineBlock block={item.block} language={language} />;
  }
  if (item.active && !foldActiveProcess) {
    return <ProcessEntries blocks={item.blocks} language={language} active />;
  }
  return <ProcessFold
    blocks={item.blocks}
    elapsedMs={item.elapsedMs}
    language={language}
    featured={featured}
    active={item.active}
    collapseCompleted={collapseCompletedProcess}
  />;
}

type TimelineFeedProps = Parameters<typeof TimelineFeedView>[0];

function sameTimelineFeed(previous: TimelineFeedProps, next: TimelineFeedProps) {
  return previous.blocks === next.blocks
    && previous.language === next.language
    && previous.compact === next.compact
    && previous.activeRunId === next.activeRunId
    && previous.running === next.running
    && previous.waitingForModel === next.waitingForModel
    && previous.foldActiveProcess === next.foldActiveProcess
    && previous.collapseCompletedProcess === next.collapseCompletedProcess;
}

// Model/effort changes update the composer store, not the transcript. Keep the
// potentially large timeline out of those render paths.
export const TimelineFeed = memo(TimelineFeedView, sameTimelineFeed);

function ProcessFold({ blocks, elapsedMs, language, featured, active = false, collapseCompleted = false }: {
  blocks: Block[];
  elapsedMs: number;
  language: Snapshot["language"];
  featured: boolean;
  active?: boolean;
  collapseCompleted?: boolean;
}) {
  const [expanded, setExpanded] = useState(active || (featured && !collapseCompleted));
  const previousState = useRef({ featured, active });
  useEffect(() => {
    const previous = previousState.current;
    previousState.current = { featured, active };
    if (previous.active && !active && collapseCompleted) {
      setExpanded(false);
      return;
    }
    if (!previous.active && active) {
      setExpanded(true);
      return;
    }
    if (previous.featured !== featured && !collapseCompleted) setExpanded(featured);
  }, [active, collapseCompleted, featured]);
  const label = translator(language)(active ? "processing" : "processed");
  const liveElapsedMs = useLiveElapsed(elapsedMs, active);
  const duration = liveElapsedMs > 0 ? formatDuration(liveElapsedMs) : "";
  return <details
    className="process-fold"
    data-featured={featured || undefined}
    data-state={active ? "running" : "completed"}
    aria-busy={active || undefined}
    open={expanded}
    onToggle={(event) => setExpanded(event.currentTarget.open)}
  >
    <summary>
      <span className="process-fold-chevrons" aria-hidden="true">
        <ChevronRight className="closed-chevron" size={14} />
        <ChevronDown className="open-chevron" size={14} />
      </span>
      <span className="process-fold-label">{label}</span>
      {duration ? <time>{duration}</time> : null}
    </summary>
    <div className="process-fold-body">
      <ProcessEntries blocks={blocks} language={language} active={active} />
    </div>
  </details>;
}

/** Keep a server-reported elapsed duration moving locally while work is active. */
function useLiveElapsed(elapsedMs: number, active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  const anchor = useRef({ elapsedMs: Math.max(0, elapsedMs), observedAt: now, active });

  useEffect(() => {
    const observedAt = Date.now();
    const current = anchor.current;
    const projected = current.active
      ? current.elapsedMs + Math.max(0, observedAt - current.observedAt)
      : current.elapsedMs;
    anchor.current = {
      elapsedMs: active ? Math.max(0, elapsedMs, projected) : Math.max(0, elapsedMs),
      observedAt,
      active,
    };
    setNow(observedAt);
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active, elapsedMs]);

  const current = anchor.current;
  const projected = active && current.active
    ? current.elapsedMs + Math.max(0, now - current.observedAt)
    : elapsedMs;
  return Math.max(0, active ? Math.max(elapsedMs, projected) : elapsedMs);
}

function AnswerSection({ language }: { language: Snapshot["language"] }) {
  return <div className="run-status-marker section-marker final-answer-marker" role="separator">
    <span>{translator(language)("finalAnswer")}</span>
  </div>;
}

function ProcessEntries({ blocks, language, active = false, compact = false }: {
  blocks: Block[];
  language: Snapshot["language"];
  active?: boolean;
  compact?: boolean;
}) {
  const entries = groupProcessTimelineBlocks(blocks, language);
  const visibleEntries = active ? activeProcessEntries(entries) : entries;
  return <div className={`process-entries ${active ? "active" : ""}`} aria-live={active ? "polite" : undefined} aria-busy={active || undefined}>
    {visibleEntries.map((entry) => <ProcessEntry key={entry.kind === "block" ? entry.block.id : entry.id} entry={entry} language={language} compact={compact} />)}
  </div>;
}

type ModelProgressEntry = Extract<ProcessTimelineEntry, { kind: "model-progress" }>;
type ActiveProcessEntry = ProcessTimelineEntry | { kind: "tool-steps"; id: string; blocks: Block[] };

function ProcessEntry({ entry, language, compact }: {
  entry: ActiveProcessEntry;
  language: Snapshot["language"];
  compact: boolean;
}) {
  if (entry.kind === "model-progress") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const detailBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      <ModelProgressStep entry={entry} detailBlocks={detailBlocks} language={language} />
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "tool-steps") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const toolBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      {toolBlocks.length ? <ToolStepList blocks={toolBlocks} language={language} /> : null}
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "tool-group") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const toolBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      {toolBlocks.length ? <ToolGroup blocks={toolBlocks} summary={summarizeToolGroup(toolBlocks, language)} language={language} /> : null}
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  return <TimelineBlock block={entry.block} language={language} compact={compact} />;
}

function activeProcessEntries(entries: ProcessTimelineEntry[]): ActiveProcessEntry[] {
  const result: ActiveProcessEntry[] = [];
  let steps: Block[] = [];
  const flush = () => {
    if (!steps.length) return;
    result.push({ kind: "tool-steps", id: `active-tool-steps-${steps[0]!.id}-${steps.length}`, blocks: steps });
    steps = [];
  };
  for (const entry of entries) {
    const toolBlocks = entry.kind === "tool-group"
      ? entry.blocks
      : entry.kind === "block" && entry.block.kind === "tool" ? [entry.block] : [];
    if (toolBlocks.length) {
      steps.push(...toolBlocks);
      continue;
    }
    flush();
    result.push(entry);
  }
  flush();
  return result;
}

function ModelProgressStep({ entry, detailBlocks = entry.blocks, language }: {
  entry: ModelProgressEntry;
  detailBlocks?: Block[];
  language: Snapshot["language"];
}) {
  const [opened, setOpened] = useState(false);
  const blocks = useMemo(() => [entry.block, ...entry.blocks], [entry.block, entry.blocks]);
  const running = blocks.some(isActiveProcessBlock);
  // This row is model-authored narration of the current work, not an
  // executable lifecycle node. Nested tools keep their own failure/pending
  // states; those outcomes must not be promoted to the narration row.
  const state = running ? "running" : "settled";
  const observedElapsedMs = useMemo(
    () => processElapsedMs(blocks, running ? Date.now() : 0),
    [blocks, running],
  );
  const elapsedMs = useLiveElapsed(observedElapsedMs, running);
  const time = elapsedMs > 0
    ? formatDuration(elapsedMs)
    : running ? translator(language)("running") : "";
  return <details
    className="timeline-step model-progress-step"
    data-state={state}
    role="listitem"
    aria-current={running ? "step" : undefined}
    aria-label={entry.presentation.title}
    open={running || opened}
    onToggle={(event) => {
      if (!running) setOpened(event.currentTarget.open);
    }}
  >
    <summary>
      <span className="timeline-step-mark" aria-hidden="true">
        {running ? <span /> : <span className="timeline-step-neutral-dot" />}
      </span>
      <div>
        <strong>{entry.presentation.title}</strong>
        {entry.presentation.detail ? <small>{entry.presentation.detail}</small> : null}
      </div>
      {time ? <time>{time}</time> : null}
    </summary>
    {(running || opened) && detailBlocks.length ? <div className="timeline-step-detail model-progress-tools">
      {detailBlocks.map((block) => <ModelProgressDetail key={block.id} block={block} language={language} />)}
    </div> : null}
  </details>;
}

function ModelProgressDetail({ block, language }: { block: Block; language: Snapshot["language"] }) {
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") return <ToolTimelineBlock block={block} language={language} compact nested />;
  if (block.kind === "diff") return <DiffBlock block={block} language={language} nested />;
  return <TimelineBlock block={block} language={language} compact />;
}

function isSubagentSpawnBlock(block: Block) {
  return block.kind === "tool" && block.title?.replaceAll("_", ".") === "subagent.spawn";
}

function SubagentRunCard({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const agents = useRuntimeStore((state) => state.agents);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const [expanded, setExpanded] = useState(false);
  const runId = blocks.find((block) => block.runId)?.runId || "";
  const callIds = new Set(blocks.map((block) => block.toolCallId).filter(Boolean));
  const descriptions = blocks.map(subagentSpawnDescription).filter(Boolean);
  const runAgents = agents.filter((agent) => !runId || agent.parentRunId === runId);
  const exactAgents = runAgents.filter((agent) => agent.parentToolCallId && callIds.has(agent.parentToolCallId));
  const describedAgents = runAgents.filter((agent) => descriptions.includes(agent.description));
  const cardAgents = exactAgents.length
    ? exactAgents
    : describedAgents.length
      ? describedAgents
      : runAgents.length === blocks.length ? runAgents : [];
  const count = Math.max(blocks.length, cardAgents.length);
  const activeCount = cardAgents.filter((agent) => isSubagentActive(agent.state)).length;
  const queuedCount = cardAgents.filter((agent) => agent.state === "queued").length;
  const terminalCount = cardAgents.filter((agent) => isSubagentTerminal(agent.state)).length;
  const failedCount = cardAgents.filter((agent) => agent.state === "failed").length;
  const toolRunning = blocks.some(isRunningTool);
  const active = activeCount > 0 || toolRunning;
  const state = active ? "running" : queuedCount > 0 ? "queued" : failedCount > 0 ? "failed" : "completed";
  const status = cardAgents.length
    ? subagentSummaryLabel(cardAgents, language)
    : toolRunning
      ? tFormat(language, "subagentsRunning", { count })
      : tFormat(language, "subagentsStarted", { count });
  const progress = count > 0 ? Math.min(100, Math.round((terminalCount / count) * 100)) : 0;
  const listId = `subagent-run-${blocks[0]?.id.replace(/[^a-zA-Z0-9_-]/gu, "-") || "group"}`;
  const t = translator(language);

  return <section className="subagent-run-card" data-state={state} aria-label={tFormat(language, "subagentRunTitle", { count })}>
    <button
      type="button"
      className="subagent-run-card-summary"
      aria-expanded={expanded}
      aria-controls={listId}
      aria-label={`${expanded ? t("subagentRunCollapse") : t("subagentRunExpand")}，${status}`}
      onClick={() => setExpanded((value) => !value)}
    >
      <span className="subagent-run-mark" aria-hidden="true">
        <Command size={18} />
        <i>{count}</i>
      </span>
      <span className="subagent-run-copy">
        <span>{t("subagentCenterEyebrow")}</span>
        <strong>{tFormat(language, "subagentRunTitle", { count })}</strong>
        <small>{status}</small>
      </span>
      <span className="subagent-run-status" data-state={state}>
        <strong>{failedCount > 0 && !active ? tFormat(language, "subagentRunFailed", { count: failedCount }) : active ? t("running") : t("completed")}</strong>
        <small>{tFormat(language, "subagentRunProgress", { completed: terminalCount, count })}</small>
        <span className="subagent-run-progress" aria-hidden="true">
          <i style={{ width: `${progress}%` }} data-indeterminate={active && progress === 0 || undefined} />
        </span>
      </span>
      <ChevronRight className="subagent-run-chevron" size={17} aria-hidden="true" />
    </button>
    <div className="subagent-run-list" id={listId} hidden={!expanded}>
      {cardAgents.length ? cardAgents.map((agent) => <SubagentRunRow
        key={agent.id}
        agent={agent}
        agents={cardAgents}
        language={language}
        open={() => selectAgent(agent.id)}
      />) : descriptions.map((description, index) => <div className="subagent-run-pending-row" key={`${description}-${index}`}>
        <span className="subagent-run-pending-mark" aria-hidden="true" />
        <strong>{description}</strong>
        <em>{toolRunning ? t("agentInitializing") : t("agentQueued")}</em>
      </div>)}
    </div>
  </section>;
}

function SubagentRunRow({ agent, agents, language, open }: {
  agent: AgentState;
  agents: AgentState[];
  language: Snapshot["language"];
  open: () => void;
}) {
  const name = subagentDisplayName(agent, agents, language);
  const preview = subagentPreviewText(agent, name, language);
  return <button type="button" className="subagent-run-row" data-state={agent.state} onClick={open}>
    <SubagentGlyph agent={agent} size={26} />
    <span><strong>{name}</strong><small>{preview}</small></span>
    <em>{subagentStatusLabel(agent.state, language)}</em>
    <ChevronRight size={14} aria-hidden="true" />
  </button>;
}

function subagentSpawnDescription(block: Block) {
  const raw = block.data?.arguments || block.content || "";
  for (const candidate of [raw, raw.split("\n")[0] || ""]) {
    try {
      const parsed = JSON.parse(candidate) as Record<string, unknown>;
      const value = String(parsed.description || parsed.prompt || "").trim();
      if (value) return value;
    } catch { /* durable tool content may append a result after the JSON arguments */ }
  }
  const match = raw.match(/"(?:description|prompt)"\s*:\s*"((?:\\.|[^"\\])*)"/u);
  if (!match?.[1]) return "";
  try { return JSON.parse(`"${match[1]}"`) as string; } catch { return match[1]; }
}


function ToolGroup({ blocks, summary, language }: { blocks: Block[]; summary: string; language: Snapshot["language"] }) {
  if (blocks.every((block) => block.data?.presentation === "steps")) {
    return <ToolStepList blocks={blocks} language={language} />;
  }
  return <details className="tool-group work-entry" data-state="completed">
    <summary>
      <span className="tool-leading work-entry-icon" aria-hidden="true">
        <span className="tool-state"><Check size={12} /></span>
        <span className="tool-chevron"><ChevronRight className="closed-chevron" size={12} /><ChevronDown className="open-chevron" size={12} /></span>
      </span>
      <span className="tool-summary-text"><strong className="work-entry-label">{summary}</strong></span>
      <span className="tool-count">{blocks.length}{language === "zh-CN" ? " 项" : ""}</span>
    </summary>
    <div className="tool-group-body">
      {blocks.map((block) => <TimelineBlock key={block.id} block={block} language={language} compact nested />)}
    </div>
  </details>;
}

function ToolStepList({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  return <div className="timeline-step-list" role="list">
    {blocks.map((block) => fileChangesForBlock(block).length
      || pendingFileChangeSummaryForBlock(block)
      || isActiveFileChangeBlock(block)
      ? <ToolTimelineBlock key={block.id} block={block} language={language} />
      : <ToolStep key={block.id} block={block} language={language} />)}
  </div>;
}

function ToolStep({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const [opened, setOpened] = useState(false);
  const running = isRunningTool(block);
  const completed = ["completed", "ready", "success"].includes(block.state || "");
  const failed = block.state === "failed" || block.state === "cancelled";
  const payload = block.content || block.data?.arguments || "";
  const liveOutput = block.data?.output || "";
  const previewPayload = payload.length > 8192 ? payload.slice(0, 8192) : payload;
  const preview = block.data?.presentation === "steps"
    ? block.content || ""
    : formatToolPresentation(previewPayload, language).preview;
  const presentation = useMemo(
    () => opened ? formatToolPresentation(payload, language) : null,
    [opened, payload, language],
  );
  const observedElapsedMs = useMemo(
    () => processElapsedMs([block], running ? Date.now() : 0),
    [block, running],
  );
  const elapsedMs = useLiveElapsed(observedElapsedMs, running);
  const state = running ? "running" : block.state || "completed";
  const time = elapsedMs > 0
    ? formatDuration(elapsedMs)
    : running ? translator(language)("running") : completed ? "" : toolStatusLabel(state, language);
  const label = block.title ? toolDisplayName(block.title, language) : translator(language)("toolGeneric");
  return <details className="timeline-step" data-state={state} role="listitem" aria-current={running ? "step" : undefined} onToggle={(event) => setOpened(event.currentTarget.open)}>
    <summary>
      <span className="timeline-step-mark" aria-hidden="true">
        {running ? <span /> : completed ? <Check size={11} /> : failed ? <X size={10} /> : <span className="timeline-step-pending-dot" />}
      </span>
      <div><strong>{label}</strong>{preview ? <small>{preview}</small> : null}</div>
      {time ? <time>{time}</time> : null}
    </summary>
    {opened ? <div className="timeline-step-detail">
      {presentation?.fields.length ? <dl className="tool-fields">
        {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
      </dl> : null}
      {running && liveOutput
        ? <ToolExecutionLog output={liveOutput} label={`${label} · ${translator(language)("fieldDetail")}`} />
        : presentation?.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
    </div> : null}
  </details>;
}

type TimelineBlockProps = {
  block: Block;
  language: Snapshot["language"];
  compact?: boolean;
  nested?: boolean;
};

function TimelineBlockView({ block, language, compact = false, nested = false }: TimelineBlockProps) {
  const sessionId = useRuntimeStore((state) => state.currentSessionId || state.snapshot?.sessionId || "");
  if (block.kind === "user") {
    return <article className="user-block">
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
    return <article className={`assistant-block markdown timeline-prose ${active ? "streaming" : ""} ${phasePending ? "phase-pending" : ""} ${compact ? "compact" : ""}`} aria-busy={active || undefined}>
      {active
        ? <StreamingText content={block.content || ""} debugReplay={import.meta.env.DEV && new URLSearchParams(window.location.search).get("demo") === "running"} />
        : <Markdown>{block.content || ""}</Markdown>}
    </article>;
  }
  if (block.kind === "question") return <QuestionBlock block={block} language={language} />;
  if (block.kind === "plan") return <PlanBlock block={block} language={language} />;
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") return <ToolTimelineBlock block={block} language={language} compact={compact} nested={nested} />;
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

function sameTimelineBlock(previous: TimelineBlockProps, next: TimelineBlockProps) {
  return previous.block === next.block
  && previous.language === next.language
  && previous.compact === next.compact
  && previous.nested === next.nested;
}

export const TimelineBlock = memo(TimelineBlockView, sameTimelineBlock);

function ToolTimelineBlock({ block, language, compact = false, nested = false }: TimelineBlockProps) {
  const fileChanges = fileChangesForBlock(block);
  if (fileChanges.length) {
    return <FileChangeBlock changes={fileChanges} language={language} nested={nested} />;
  }
  const pendingSummary = pendingFileChangeSummaryForBlock(block);
  if (pendingSummary || isActiveFileChangeBlock(block)) {
    return <FileChangeBlock changes={[]} summary={pendingSummary ?? undefined} language={language} nested={nested} running />;
  }
  return <ToolDisclosure block={block} language={language} compact={compact} nested={nested} />;
}

function ToolDisclosure({ block, language, compact = false, nested = false }: TimelineBlockProps) {
  const t = translator(language);
  const [opened, setOpened] = useState(false);
  const running = isRunningTool(block);
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
  return <details
    className={`tool-block work-entry ${nested ? "nested" : ""} ${compact ? "compact" : ""}`}
    data-state={block.state || "completed"}
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
      {(running || pending || block.state === "failed") ? <span className="tool-status">{toolStatusLabel(block.state, language)}</span> : null}
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
  </details>;
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

function revealGlyphs(text: string) {
  if (typeof Intl.Segmenter === "function") {
    return Array.from(new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(text), (entry) => entry.segment);
  }
  return Array.from(text);
}

function replayChunks(text: string) {
  const glyphs = revealGlyphs(text);
  const chunks: string[] = [];
  for (let index = 0; index < glyphs.length;) {
    const newline = glyphs[index] === "\n";
    const size = newline ? 1 : 4;
    chunks.push(glyphs.slice(index, index + size).join(""));
    index += size;
  }
  return chunks;
}

type StreamingPresentation = {
  rendered: string;
  ranges: StreamingRevealRange[];
  nextID: number;
};

const MAX_LIVE_REVEAL_CHUNKS = 8;
const MAX_REVEAL_CHUNK_GLYPHS = 96;

function revealTailOffset(text: string) {
  const glyphs = revealGlyphs(text);
  return glyphs.length > MAX_REVEAL_CHUNK_GLYPHS
    ? glyphs.slice(0, glyphs.length - MAX_REVEAL_CHUNK_GLYPHS).join("").length
    : 0;
}

function initialStreamingPresentation(text: string): StreamingPresentation {
  if (!text) return { rendered: "", ranges: [], nextID: 0 };
  return {
    rendered: text,
    ranges: [{ id: 0, start: revealTailOffset(text), end: text.length }],
    nextID: 1,
  };
}

function appendStreamingPresentation(current: StreamingPresentation, text: string): StreamingPresentation {
  if (current.rendered === text) return current;
  if (!text.startsWith(current.rendered)) return initialStreamingPresentation(text);
  const appended = text.slice(current.rendered.length);
  if (!appended) return { ...current, rendered: text };
  const range = {
    id: current.nextID,
    start: current.rendered.length + revealTailOffset(appended),
    end: text.length,
  };
  return {
    rendered: text,
    ranges: [...current.ranges, range].slice(-MAX_LIVE_REVEAL_CHUNKS),
    nextID: current.nextID + 1,
  };
}

function StreamingText({ content, debugReplay = false }: { content: string; debugReplay?: boolean }) {
  // Production renders the provider's current buffer directly through the same
  // Markdown path as completed answers. The replay clock exists only for the
  // visual demo and advances in small batches so structural Markdown settles
  // quickly instead of exposing one control character at a time.
  const [replayContent, setReplayContent] = useState(() => debugReplay ? "" : content);
  useEffect(() => {
    if (!debugReplay) return;
    const chunks = replayChunks(content);
    let index = 0;
    let timer = 0;
    const delayForChunk = (chunk: string) => {
      if (chunk === "\n") return 72;
      if (/[。！？!?]\s*$/u.test(chunk)) return 62;
      if (/[，、；：,;:]\s*$/u.test(chunk)) return 45;
      return 34;
    };
    setReplayContent("");
    const reveal = () => {
      const chunk = chunks[index];
      if (chunk === undefined) return;
      setReplayContent((value) => value + chunk);
      index += 1;
      if (index < chunks.length) timer = window.setTimeout(reveal, delayForChunk(chunk));
    };
    timer = window.setTimeout(reveal, 90);
    return () => window.clearTimeout(timer);
  }, [content, debugReplay]);
  const visibleContent = debugReplay ? replayContent : content;
  const presentationRef = useRef<StreamingPresentation>(initialStreamingPresentation(visibleContent));
  const presentation = appendStreamingPresentation(presentationRef.current, visibleContent);
  presentationRef.current = presentation;

  return <div className="streaming-text">
    <StreamingMarkdown ranges={presentation.ranges}>{visibleContent}</StreamingMarkdown>
  </div>;
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

function plainStreamingText(content: string) {
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

function FileChangeBlock({ changes, summary, language, nested, running = false }: {
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

function RunStatusMarker({ block, language }: { block: Block; language: Snapshot["language"] }) {
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

const COLLAPSED_EDITED_FILE_COUNT = 3;

function EditedFilesSummary({ summary, language }: { summary: EditedFileSummary; language: Snapshot["language"] }) {
  const t = translator(language);
  const [expanded, setExpanded] = useState(false);
  const label = summary.files.length === 1
    ? t("editedOneFile")
    : tFormat(language, "editedFileCount", { count: String(summary.files.length) });
  const hiddenCount = Math.max(0, summary.files.length - COLLAPSED_EDITED_FILE_COUNT);
  const visibleFiles = expanded ? summary.files : summary.files.slice(0, COLLAPSED_EDITED_FILE_COUNT);
  return <article className="edited-files-summary">
    <header>
      <span className="edited-files-icon" aria-hidden="true"><FilePenLine size={18} /></span>
      <div><strong>{label}</strong><span><b className="plus">+{summary.additions}</b><b className="minus">−{summary.deletions}</b></span></div>
    </header>
    <ul>{visibleFiles.map((file) => <li key={file.path}>
      <span title={file.path}>{file.path}</span>
      <span><b className="plus">+{file.additions}</b><b className="minus">−{file.deletions}</b></span>
    </li>)}</ul>
    {hiddenCount > 0 ? <button
      type="button"
      className="edited-files-toggle"
      aria-expanded={expanded}
      onClick={() => setExpanded((value) => !value)}
    >
      <span>{expanded
        ? t("editedFilesShowLess")
        : tFormat(language, "editedFilesShowMore", { count: String(hiddenCount) })}</span>
      <ChevronDown size={14} aria-hidden="true" />
    </button> : null}
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
