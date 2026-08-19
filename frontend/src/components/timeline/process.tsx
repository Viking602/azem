import { ChevronDown, ChevronRight, Command } from "lucide-react";
import { startTransition, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { tFormat, translator } from "../../i18n";
import {
  isSubagentActive, isSubagentTerminal, subagentDisplayName, subagentPreviewText,
  subagentStatusLabel, subagentSummaryLabel,
} from "../../subagents";
import { useRuntimeStore } from "../../store";
import type { AgentState, Block, Snapshot } from "../../types";
import {
  displayedToolState, formatDuration, formatToolPresentation, formatWorkedDuration, groupProcessTimelineBlocks,
  isActiveProcessBlock, isHostFallbackCommentary, isRunningTool, processElapsedMs, summarizeToolGroup, thinkingStateLabel, thinkingTraceElapsedMs,
  type ModelProgressPresentation, type ProcessTimelineEntry,
} from "../toolTimeline";
import { fileChangePillsForBlocks, formatProcessGroupCount, processGroupCountLabel, processGroupCounts, thinkingChipPreview, toolChipModel } from "../toolChip";
import AnsiText from "../AnsiText";
import SubagentGlyph from "../SubagentGlyph";
import { ThinkingState } from "../beautiful-ui/Primitives";
import { StepRow } from "../beautiful-ui/StepRow";
import { FileChangePills, ToolChip, ToolChipMeta, ToolChipStatus } from "../beautiful-ui/ToolChip";
import {
  isActiveFileChangeBlock, isFileChangeTool, isPendingFileChangeBlock,
} from "../fileChanges";
import { TimelineBlock, ToolTimelineBlock, toolStatusLabel } from "./blocks";
import {
  DEFERRED_PROCESS_MIN_ROWS,
  DEFERRED_PROCESS_ROW_PX,
  DEFERRED_PROCESS_WINDOW,
  deferredProcessWindow,
  deferredProgressChip,
  flattenDeferredProcessRows,
  type DeferredProcessRow,
} from "./processDefer";
import { blocksMarkState, stepEdge, stepEntranceDelays, stepMarkState, type StepEdge } from "./stepRail";
import { activityBarLabel, processActivityVisibility } from "./thinkingTabs";
import { ReasoningPanel, ThinkingTrace } from "./ThinkingTrace";
import { ToolExecutionLog } from "./ToolExecutionLog";
import { useLiveElapsed } from "./useLiveElapsed";

/**
 * Codex: after the turn finishes, 耗时 hides the process. Opening it shows
 * commentary plus folded tool summaries. Thinking stays inside the duration
 * label and is not dumped as prose.
 */
export function ProcessFold({
  blocks, elapsedMs = 0, language, featured, active = false, collapseCompleted = false, waiting = false,
}: {
  blocks: Block[];
  elapsedMs: number;
  language: Snapshot["language"];
  featured: boolean;
  active?: boolean;
  collapseCompleted?: boolean;
  waiting?: boolean;
}) {
  const hasTools = hasProcessTools(blocks);
  const foldCompleted = Boolean(collapseCompleted && !active && !waiting && hasTools);
  const [open, setOpen] = useState(false);
  const t = translator(language);
  const duration = elapsedMs > 0 ? formatWorkedDuration(elapsedMs, language) : "";
  const foldLabel = duration ? tFormat(language, "processedFor", { duration }) : t("processed");
  const entries = <ProcessEntries
    blocks={blocks}
    language={language}
    active={active}
    waiting={waiting}
    omitThinking={foldCompleted}
  />;
  return <div
    className="process-fold"
    data-featured={featured || undefined}
    data-state={active ? "running" : "completed"}
    data-step={hasTools ? "tools" : "reasoning"}
    data-folded={foldCompleted || undefined}
    data-open={foldCompleted && open ? "true" : undefined}
    aria-busy={active || undefined}
  >
    {foldCompleted ? <>
      <button
        type="button"
        className="process-fold-summary"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="process-fold-label">{foldLabel}</span>
        <ChevronDown className="process-fold-chevron" size={12} aria-hidden="true" />
      </button>
      {open ? entries : null}
    </> : entries}
  </div>;
}





/**
 * One step: the tools and thinking that follow one model message. The bar is the
 * whole step — its rows live inside it and are never repeated outside.
 */
function ProcessStep({
  blocks, language, live = false, waiting = false, deferred = true, messageCount = 0,
  canSettle = true, hideClocks = false, children,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  live?: boolean;
  waiting?: boolean;
  deferred?: boolean;
  messageCount?: number;
  canSettle?: boolean;
  hideClocks?: boolean;
  children?: ReactNode;
}) {
  const hasTools = hasProcessTools(blocks);
  const running = waiting || (live && blocks.some(isActiveProcessBlock));
  const [choice, setChoice] = useState<boolean | null>(null);
  const expanded = choice ?? (running && !hasTools && blocks.length > 0);
  const panelId = `process-step-${(blocks[0]?.id || "pending").replace(/[^a-zA-Z0-9_-]/gu, "-")}`;
  if (!stepBarVisible(blocks, waiting, running)) return <>{children}</>;
  if (!running && canSettle && hasTools) {
    const open = choice === true;
    const tools = blocks.filter((block) => block.kind === "tool" || block.kind === "diff");
    const label = summarizeToolGroup(tools, language) || formatProcessGroupCount(tools.length, 0, language);
    return <div className="process-step" data-state="completed" data-step="tools" data-settled="" data-open={open || undefined}>

      <button
        type="button"
        className="process-fold-summary process-step-count"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setChoice(!open)}
      >
        <span className="process-fold-label">{label}</span>
        <ChevronDown className="process-fold-chevron" size={12} aria-hidden="true" />
      </button>
      {open ? <div className="process-step-body" id={panelId}>
        <ProcessStepBody blocks={blocks} language={language} deferred={deferred}>
          {children}
        </ProcessStepBody>
      </div> : null}
    </div>;
  }
  return <div
    className="process-step"
    data-state={running ? "running" : "completed"}
    data-step={hasTools ? "tools" : "reasoning"}
  >
    <ProcessStepBar
      blocks={blocks}
      language={language}
      hasTools={hasTools}
      active={running}
      waiting={waiting}
      live={live}
      expanded={expanded}
      panelId={panelId}
      onToggle={() => setChoice(!expanded)}
    />
    {expanded ? <div className="process-step-body" id={panelId}>
      <ProcessStepBody blocks={blocks} language={language} deferred={deferred && !running}>
        {children}
      </ProcessStepBody>
    </div> : null}
  </div>;
}


/**
 * Live tools stay on the quiet 正在思考 row. The label rolls to the tool name;
 * a clock on this bar would switch chrome and replay as a second style.
 */
function ProcessStepBar({
  blocks, language, hasTools, active, waiting, live = false, expanded, panelId, onToggle,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  hasTools: boolean;
  active: boolean;
  waiting: boolean;
  live?: boolean;
  expanded: boolean;
  panelId: string;
  onToggle?: () => void;
}) {
  const reasoning = useMemo(() => blocks.filter((block) => block.kind === "thinking"), [blocks]);
  const reasoningRunning = reasoning.some((block) => isActiveProcessBlock(block) && Boolean(block.content?.trim()));
  const ticking = waiting || (hasTools ? active : reasoningRunning);
  const settledThinking = !hasTools && !reasoningRunning && reasoning.some((block) => Boolean(block.content?.trim()));
  const label = activityBarLabel(blocks, language, {
    waiting: waiting && !settledThinking,
    live: (ticking || waiting || live) && !settledThinking,
  });
  const expandable = hasTools || reasoning.some((block) => Boolean(block.content?.trim()));
  return <ThinkingState
    active={ticking}
    expanded={expanded}
    label={label}
    labelKey={label}
    quiet
    expandable={expandable}
    panelId={panelId}
    onToggle={onToggle}
  />;
}

function ProcessStepBody({ blocks, language, deferred, children }: {
  blocks: Block[];
  language: Snapshot["language"];
  deferred: boolean;
  children?: ReactNode;
}) {
  const rows = useMemo(
    () => deferred ? flattenDeferredProcessRows(blocks, language) : [],
    [blocks, deferred, language],
  );
  if (rows.length > DEFERRED_PROCESS_MIN_ROWS) {
    return <DeferredProcessEntries blocks={blocks} language={language} rows={rows} />;
  }
  return <>{children}</>;
}
export function ProcessEntries({
  blocks, language, active = false, compact = false, deferBodies = false,
  omitThinking = false, omitLiveTools = false, summarized = false, stepHasTools = false,
  waiting = false, hideClocks = false,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  active?: boolean;
  compact?: boolean;
  deferBodies?: boolean;
  omitThinking?: boolean;
  omitLiveTools?: boolean;
  summarized?: boolean;
  stepHasTools?: boolean;
  waiting?: boolean;
  hideClocks?: boolean;
}) {
  if (deferBodies && !active) {
    const deferredRows = flattenDeferredProcessRows(blocks, language);
    if (deferredRows.length > DEFERRED_PROCESS_MIN_ROWS) {
      return <DeferredProcessEntries blocks={blocks} language={language} rows={deferredRows} />;
    }
  }
  const entries = groupProcessTimelineBlocks(blocks, language);
  const visibleEntries = active ? activeProcessEntries(entries) : entries;
  const lastRealIndex = visibleEntries.length - 1;
  const lastReal = lastRealIndex >= 0 ? visibleEntries[lastRealIndex] : undefined;
  const hostWaitOnLast = Boolean(waiting && lastReal && processEntryCanHostWait(lastReal));
  const lastIsLiveProse = lastReal?.kind === "model-progress"
    && isActiveProcessBlock(lastReal.block)
    && !isHostFallbackCommentary(lastReal.block);
  const listed = waiting && !hostWaitOnLast && !lastIsLiveProse
    ? [...visibleEntries, PENDING_ENTRY]
    : visibleEntries;
  return <div className={`process-entries ${active ? "active" : ""}`} aria-live={active ? "polite" : undefined} aria-busy={active || undefined}>
    {listed.map((entry, index) => {
      const pending = entry === PENDING_ENTRY;
      const lastRealEntry = !pending && index === lastRealIndex;
      return <ProcessEntry
        key={`entry-${index}`}
        entry={entry}
        language={language}
        compact={compact}
        siblings={blocks}
        live={active && (pending || lastRealEntry)}
        waiting={pending || (hostWaitOnLast && lastRealEntry)}
        canSettle={!active || (!pending && index < lastRealIndex)}
        omitThinking={omitThinking}
        omitLiveTools={omitLiveTools}
        summarized={summarized}
        stepHasTools={stepHasTools}
        hideClocks={hideClocks}
      />;
    })}
  </div>;
}
function processEntryWork(entry: ActiveProcessEntry) {
  if (entry.kind === "block") return [entry.block];
  return entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
}

function processEntryCanHostWait(entry: ActiveProcessEntry) {
  const work = processEntryWork(entry);
  return hasProcessTools(work) || work.some((block) => block.kind === "thinking" && Boolean(block.content?.trim()));
}


const PENDING_ENTRY: ActiveProcessEntry = { kind: "tool-steps", id: "pending-step", blocks: [] };

function DeferredProcessEntries({ blocks, language, rows }: {
  blocks: Block[];
  language: Snapshot["language"];
  rows: DeferredProcessRow[];
}) {
  const { listRef, start, end } = useDeferredProcessWindow(rows.length);
  const pills = fileChangePillsForBlocks(blocks);
  const visible = rows.slice(start, end);
  return <div
    ref={listRef}
    className="process-entries deferred"
    data-deferred="true"
    data-testid="deferred-process-list"
  >
    <div className="timeline-step-rows" role="list">
      {start > 0 ? <div className="deferred-process-spacer" style={{ height: start * DEFERRED_PROCESS_ROW_PX }} aria-hidden="true" /> : null}
      {visible.map((row, offset) => <DeferredProcessRowView
        key={row.id}
        row={row}
        language={language}
        siblings={blocks}
        edge={stepEdge(start + offset, rows.length)}
      />)}
      {end < rows.length ? <div className="deferred-process-spacer" style={{ height: (rows.length - end) * DEFERRED_PROCESS_ROW_PX }} aria-hidden="true" /> : null}
    </div>
    {pills.files.length ? <FileChangePills files={pills.files} language={language} /> : null}
  </div>;
}

function useDeferredProcessWindow(count: number) {
  const listRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState(() => ({
    start: 0,
    end: Math.min(count, DEFERRED_PROCESS_WINDOW),
  }));
  useEffect(() => {
    if (count <= DEFERRED_PROCESS_WINDOW) {
      setView({ start: 0, end: count });
      return;
    }
    const list = listRef.current;
    const scroller = list?.closest(".transcript-viewport, .agent-side-chat-scroll");
    const sync = () => {
      const scrollTop = scroller instanceof HTMLElement ? scroller.scrollTop : 0;
      const height = scroller instanceof HTMLElement ? scroller.clientHeight : 0;
      let listOffset = 0;
      if (list && scroller instanceof HTMLElement) {
        listOffset = list.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scrollTop;
      }
      const next = deferredProcessWindow(count, scrollTop, height, listOffset);
      setView((current) => current.start === next.start && current.end === next.end ? current : next);
    };
    sync();
    if (!(scroller instanceof HTMLElement)) return;
    const onScroll = () => startTransition(sync);
    scroller.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      scroller.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [count]);
  return { listRef, start: view.start, end: view.end };
}

function DeferredProcessRowView({ row, language, siblings, edge }: {
  row: DeferredProcessRow;
  language: Snapshot["language"];
  siblings: Block[];
  edge: StepEdge;
}) {
  if (row.kind === "progress") {
    return <StepRow mark="note" edge={edge}>
      <DeferredProgressRow block={row.block} presentation={row.presentation} language={language} siblings={siblings} />
    </StepRow>;
  }
  if (row.kind === "thinking") {
    return <StepRow mark={blocksMarkState(row.blocks)} edge={edge}>
      <DeferredThinkingRow blocks={row.blocks} language={language} />
    </StepRow>;
  }
  if (row.kind === "subagent") {
    return <StepRow mark={blocksMarkState(row.blocks)} edge={edge}>
      <div data-deferred-row="subagent"><SubagentRunCard blocks={row.blocks} language={language} /></div>
    </StepRow>;
  }
  return <StepRow mark={stepMarkState(row.block.state)} edge={edge}>
    <div data-deferred-row="tool">
      <ToolTimelineBlock block={row.block} language={language} compact nested siblings={siblings} />
    </div>
  </StepRow>;
}

function DeferredProgressRow({ block, presentation, language, siblings }: {
  block: Block;
  presentation: ModelProgressPresentation;
  language: Snapshot["language"];
  siblings: Block[];
}) {
  const [opened, setOpened] = useState(false);
  const t = translator(language);
  const chip = deferredProgressChip(presentation, t("progressUpdate"));
  return <div data-deferred-row="progress">
    <ToolChip
      className="deferred-progress-row"
      kind="other"
      label={chip.title}
      chip={chip.chip || undefined}
      onToggle={(event) => setOpened(event.currentTarget.open)}
    >
      {opened ? <TimelineBlock block={block} language={language} siblings={siblings} /> : null}
    </ToolChip>
  </div>;
}

function DeferredThinkingRow({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  return <div data-deferred-row="thinking">
    <ThinkingChip blocks={blocks} language={language} />
  </div>;
}

type ModelProgressEntry = Extract<ProcessTimelineEntry, { kind: "model-progress" }>;
type ActiveProcessEntry = ProcessTimelineEntry | { kind: "tool-steps"; id: string; blocks: Block[] };
function ProcessEntry({
  entry, language, compact, siblings, live = false, waiting = false, canSettle = true,
  omitThinking = false, omitLiveTools = false,
  summarized = false, stepHasTools = false, hideClocks = false,
}: {
  entry: ActiveProcessEntry;
  language: Snapshot["language"];
  compact: boolean;
  siblings: Block[];
  live?: boolean;
  waiting?: boolean;
  canSettle?: boolean;
  omitThinking?: boolean;
  omitLiveTools?: boolean;
  summarized?: boolean;
  stepHasTools?: boolean;
  hideClocks?: boolean;
}) {
  if (entry.kind === "model-progress") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const detailBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      <ModelProgressProse entry={entry} language={language} siblings={siblings} />
      <ProcessStep blocks={detailBlocks} language={language} live={live} waiting={waiting} canSettle={canSettle} messageCount={1} hideClocks={hideClocks}>
        <ProcessTrail
          blocks={detailBlocks}
          language={language}
          siblings={siblings}
          live={live}
          omitThinking={omitThinking}
          omitLiveTools={omitLiveTools}
          summarized
          stepHasTools={hasProcessTools(detailBlocks)}
          wrapClassName="model-progress-tools"
          hideClocks={hideClocks}
        />
      </ProcessStep>
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "thinking-trail") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const detailBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      <ProcessStep blocks={detailBlocks} language={language} live={live} waiting={waiting} canSettle={canSettle} hideClocks={hideClocks}>
        <ProcessTrail
          blocks={detailBlocks}
          language={language}
          siblings={siblings}
          live={live}
          omitThinking={omitThinking}
          omitLiveTools={omitLiveTools}
          summarized
          stepHasTools={hasProcessTools(detailBlocks)}
          hideClocks={hideClocks}
        />
      </ProcessStep>
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "tool-steps" || entry.kind === "tool-group") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const toolBlocks = visibleActivityBlocks(
      entry.blocks.filter((block) => !isSubagentSpawnBlock(block)),
      { omitThinking, omitLiveTools },
    );
    return <>
      <ProcessStep
        blocks={toolBlocks}
        language={language}
        live={live}
        waiting={waiting || entry === PENDING_ENTRY}
        canSettle={canSettle}
        hideClocks={hideClocks}
      >
        <ProcessTrail
          blocks={toolBlocks}
          language={language}
          siblings={siblings}
          live={live}
          summarized
          stepHasTools={hasProcessTools(toolBlocks)}
          hideClocks={hideClocks}
        />
      </ProcessStep>
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  return <TimelineBlock block={entry.block} language={language} compact={compact} siblings={siblings} />;
}



function activeProcessEntries(entries: ProcessTimelineEntry[]): ActiveProcessEntry[] {
  const result: ActiveProcessEntry[] = [];
  let steps: Block[] = [];
  const flush = () => {
    if (!steps.length) return;
    result.push({ kind: "tool-steps", id: `active-tool-steps-${steps[0]!.id}`, blocks: steps });
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

function ModelProgressProse({ entry, language, siblings }: {
  entry: ModelProgressEntry;
  language: Snapshot["language"];
  siblings: Block[];
}) {
  const running = entry.blocks.some(isActiveProcessBlock) || isActiveProcessBlock(entry.block);
  const hiddenAnnouncement = isHostFallbackCommentary(entry.block);
  if (hiddenAnnouncement) return null;
  return <section
    className="model-progress-prose"
    data-state={running ? "running" : "settled"}
    aria-busy={running || undefined}
  >
    <TimelineBlock block={entry.block} language={language} siblings={siblings} />
  </section>;
}

/**
 * Whether a step deserves its own sparkle bar. Delegation cards and hidden
 * heartbeats carry no step of their own, so a bar over them says nothing.
 */
function stepBarVisible(blocks: Block[], waiting: boolean, active: boolean) {
  if (waiting) return true;
  // While children run, the delegation card is the live indicator; a bar over it
  // would only repeat it. Once the step settles it summarizes like any other.
  const work = active ? blocks.filter((block) => !isSubagentSpawnBlock(block)) : blocks;
  if (work.some((block) => block.kind === "tool" || block.kind === "diff")) return true;
  return work.some((block) => block.kind === "thinking" && Boolean(block.content?.trim()));
}

export function isSubagentSpawnBlock(block: Block) {
  return block.kind === "tool" && block.title?.replaceAll("_", ".") === "subagent.spawn";
}

function SubagentRunCard({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const agents = useRuntimeStore((state) => state.agents);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
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
  const [expanded, setExpanded] = useState(active);
  useEffect(() => {
    if (active) setExpanded(true);
  }, [active]);
  const state = active ? "running" : queuedCount > 0 ? "queued" : failedCount > 0 ? "failed" : "completed";
  const status = cardAgents.length
    ? subagentSummaryLabel(cardAgents, language)
    : toolRunning
      ? tFormat(language, "subagentsRunning", { count })
      : tFormat(language, "subagentsStarted", { count });
  const progress = count > 0 ? Math.min(100, Math.round((terminalCount / count) * 100)) : 0;
  const listId = `subagent-run-${blocks[0]?.id.replace(/[^a-zA-Z0-9_-]/gu, "-") || "group"}`;
  const t = translator(language);
  const headline = cardAgents.find((agent) => isSubagentActive(agent.state)) ?? cardAgents[0];
  const livePreview = headline
    ? subagentPreviewText(headline, subagentDisplayName(headline, cardAgents, language), language)
    : "";

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
        {active && livePreview ? <small className="subagent-run-live-preview">{livePreview}</small> : null}
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
    {expanded ? <div className="subagent-run-list" id={listId}>
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
    </div> : null}
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


function hasProcessTools(blocks: Block[]) {
  return blocks.some((block) => block.kind === "tool" || block.kind === "diff");
}

function isFileChangePresentation(block: Block) {
  return block.kind === "diff"
    || isFileChangeTool(block.title || block.data?.name || "")
    || isActiveFileChangeBlock(block)
    || isPendingFileChangeBlock(block);
}

function isLiveGenericTool(block: Block) {
  if (block.kind !== "tool" && block.kind !== "diff") return false;
  if (isFileChangePresentation(block)) return false;
  return ["running", "started", "streaming", "progress"].includes(block.state || "");
}

function visibleActivityBlocks(
  blocks: Block[],
  options: { omitThinking?: boolean; omitLiveTools?: boolean } = {},
) {
  return blocks.filter((block) => {
    if (options.omitThinking && block.kind === "thinking") return false;
    if (options.omitLiveTools && isLiveGenericTool(block)) return false;
    return true;
  });
}

/** Stable sparkle bar for wait / thinking / live tools. Completed tool trails stay in ProcessEntries. */
export function ProcessActivity({
  blocks, language, waiting = false,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  waiting?: boolean;
  live?: boolean;
}) {
  const reasoning = useMemo(() => blocks.filter((block) => block.kind === "thinking"), [blocks]);
  const hasTools = hasProcessTools(blocks);
  const hasReasoning = reasoning.some((block) => Boolean(block.content?.trim()));
  const { showBar, liveWork } = processActivityVisibility(blocks, waiting);
  const clockActive = waiting || liveWork;
  const [open, setOpen] = useState(false);
  const placeholder = waiting && !liveWork && !hasReasoning;
  const settledThinking = !hasTools && hasReasoning && !liveWork;
  const label = activityBarLabel(blocks, language, {
    waiting: waiting && !settledThinking,
    live: clockActive && !settledThinking,
  });
  const expandable = hasReasoning && !placeholder;
  const panelId = expandable
    ? `activity-${(blocks[0]?.id || "wait").replace(/[^a-zA-Z0-9_-]/gu, "-")}`
    : undefined;

  if (!showBar) return null;

  return <div
    className={[
      placeholder ? "reasoning-placeholder" : "process-activity",
      !hasTools && hasReasoning ? "bui-thinking-stack" : "",
    ].filter(Boolean).join(" ")}
    data-thinking-row={!hasTools && hasReasoning ? "reasoning" : undefined}
    role={placeholder ? "status" : undefined}
    aria-live={placeholder ? "polite" : undefined}
  >
    <ThinkingState
      active={clockActive}
      expanded={open && expandable}
      label={label}
      quiet
      labelKey={label}
      disabled={!expandable}
      expandable={expandable}
      panelId={panelId}
      onToggle={expandable ? () => setOpen((value) => !value) : undefined}
    >
      {expandable ? <div className="bui-thinking-panel" data-tab="reasoning">
        <ReasoningPanel blocks={reasoning} />
      </div> : null}
    </ThinkingState>
  </div>;
}

function ProcessTrail({
  blocks, language, siblings, live = false, omitThinking = false, omitLiveTools = false,
  summarized = false, stepHasTools = false, wrapClassName, hideClocks = false,
}: {
  blocks: Block[];
  language: Snapshot["language"];
  siblings: Block[];
  live?: boolean;
  omitThinking?: boolean;
  omitLiveTools?: boolean;
  summarized?: boolean;
  stepHasTools?: boolean;
  wrapClassName?: string;
  hideClocks?: boolean;
}) {
  const visible = visibleActivityBlocks(blocks, { omitThinking, omitLiveTools });
  if (!visible.length) return null;
  // A group never grows its own sparkle bar under a step bar. Live reasoning
  // stays prose. A settled tool card puts the 思考 chip first, then the tools.
  const toolWork = visible.filter((block) => block.kind !== "thinking");
  const body = summarized
    ? <>
      {live || !toolWork.length ? <SummarizedReasoning blocks={visible} /> : null}
      {toolWork.length ? <ProcessChipList
        blocks={live ? toolWork : visible}
        language={language}
        siblings={siblings}
        live={live}
        summarized
        hideClocks={hideClocks}
      /> : null}
    </>
    : hasProcessTools(visible) || stepHasTools
      ? <ProcessChipList blocks={visible} language={language} siblings={siblings} live={live} hideClocks={hideClocks} />
      : omitThinking ? null : <ThinkingTrace blocks={visible} language={language} live={live} />;
  if (!body) return null;
  return wrapClassName ? <div className={wrapClassName}>{body}</div> : body;
}

/** Reasoning prose only: the enclosing step bar already carries 思考 and its clock. */
function SummarizedReasoning({ blocks }: { blocks: Block[] }) {
  const reasoning = blocks.filter((block) => block.kind === "thinking");
  if (!reasoning.length) return null;
  return <div className="bui-thinking-panel" data-tab="reasoning">
    <ReasoningPanel blocks={reasoning} />
  </div>;
}

function ProcessChipList({ blocks, language, siblings, live = false, summarized = false, hideClocks = false }: {
  blocks: Block[];
  language: Snapshot["language"];
  siblings: Block[];
  live?: boolean;
  summarized?: boolean;
  hideClocks?: boolean;
}) {
  const peers = siblings.length ? siblings : blocks;
  // An empty thinking frame is a heartbeat, not a step (UI-016).
  const thinking = blocks.filter((block) => block.kind === "thinking" && Boolean(block.content?.trim()));
  const tools = blocks.filter((block) => block.kind === "tool" || block.kind === "diff");
  // Report only this group's own rows. Reusing the whole trail's totals printed
  // the same count above every group, and an enclosing bar already says it once.
  const countLabel = summarized ? "" : processGroupCountLabel(blocks, language);
  const settledCard = summarized && !live && tools.length > 0;
  // Settled cards keep the group class so opened count-rows match the chip
  // list. Unsummarized trails still grow their own headered group.
  const grouped = settledCard || (!summarized && thinking.length + tools.length >= 1);
  const rowIds = [
    ...(thinking.length ? [`thinking-${thinking[0]!.id}`] : []),
    ...tools.map((block) => block.id),
  ];
  const delays = useStepEntrance(rowIds, live);
  const count = rowIds.length;

  const pills = settledCard ? fileChangePillsForBlocks(tools) : { files: [] };
  let index = 0;
  const toolRow = (block: Block) => (
    block.kind === "diff"
      || isFileChangeTool(block.title || block.data?.name || "")
      || isActiveFileChangeBlock(block)
      || isPendingFileChangeBlock(block)
      ? <ToolTimelineBlock block={block} language={language} siblings={siblings} />
      : <ToolStep block={block} language={language} siblings={siblings} hideTime={settledCard || hideClocks || live} />
  );
  return <div className={`timeline-step-list${grouped ? " bui-tool-chip-group" : ""}`} data-settled={settledCard || undefined}>
    {countLabel && tools.length >= 2 ? <div className="bui-tool-chip-group-header">
      <ChevronDown className="bui-tool-chip-chevron" size={14} aria-hidden="true" />
      <strong>{countLabel}</strong>
    </div> : null}
    <div className="timeline-step-rows" role="list">
      {thinking.length ? <StepRow
        key={rowIds[0]}
        mark={blocksMarkState(thinking)}
        edge={stepEdge(index++, count)}
        delayMs={delays.get(rowIds[0]!)}
      >
        <ThinkingChip blocks={thinking} language={language} />
      </StepRow> : null}
      {tools.map((block) => <StepRow
        key={block.id}
        mark={stepMarkState(displayedToolState(block, peers))}
        edge={stepEdge(index++, count)}
        delayMs={delays.get(block.id)}
      >
        {toolRow(block)}
      </StepRow>)}
    </div>
    {pills.files.length ? <FileChangePills files={pills.files} language={language} /> : null}
  </div>;
}

/** Rows already committed to the DOM must not replay their entrance on rerender. */
function useStepEntrance(ids: string[], enabled: boolean) {
  const seenRef = useRef<ReadonlySet<string> | null>(null);
  const delays = enabled ? stepEntranceDelays(ids, seenRef.current) : EMPTY_DELAYS;
  useEffect(() => {
    seenRef.current = new Set(ids);
  });
  return delays;
}

const EMPTY_DELAYS: ReadonlyMap<string, number> = new Map();

function ThinkingChip({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const [opened, setOpened] = useState(false);
  const running = blocks.some((block) => isActiveProcessBlock(block) && Boolean(block.content?.trim()));
  const preview = thinkingChipPreview(blocks);
  return <ToolChip
    className="timeline-step thinking-chip"
    state={running ? "running" : "completed"}
    kind="thinking"
    label={thinkingStateLabel(language, running, running ? 0 : thinkingTraceElapsedMs(blocks))}
    chip={preview || undefined}
    onToggle={(event) => setOpened(event.currentTarget.open)}
  >
    {opened ? <div className="timeline-step-detail bui-thinking-panel" data-tab="reasoning">
      <ReasoningPanel blocks={blocks} />
    </div> : null}
  </ToolChip>;
}

function ToolStep({ block, language, siblings, hideTime = false }: {
  block: Block;
  language: Snapshot["language"];
  siblings: Block[];
  hideTime?: boolean;
}) {
  const [opened, setOpened] = useState(false);
  const storeBlocks = useRuntimeStore((state) => state.blocks);
  const peers = siblings.length ? siblings : storeBlocks;
  const state = displayedToolState(block, peers);
  const running = state === "running" || isRunningTool(block);
  const completed = ["completed", "ready", "success"].includes(block.state || "");
  const payload = block.content || block.data?.arguments || "";
  const liveOutput = block.data?.output || "";
  const model = useMemo(() => toolChipModel(block, language), [block, language]);
  const presentation = useMemo(
    () => opened ? formatToolPresentation(payload, language) : null,
    [opened, payload, language],
  );
  const observedElapsedMs = useMemo(
    () => hideTime ? 0 : processElapsedMs([block], running ? Date.now() : 0),
    [block, hideTime, running],
  );
  const elapsedMs = useLiveElapsed(observedElapsedMs, !hideTime && running);
  const time = hideTime
    ? (running || completed ? "" : toolStatusLabel(state, language))
    : elapsedMs > 0
      ? formatDuration(elapsedMs)
      : running ? translator(language)("running") : completed ? "" : toolStatusLabel(state, language);
  return <ToolChip
    className="timeline-step"
    state={state}
    kind={model.kind}
    label={model.label}
    chip={model.chip}
    status={time || undefined}
    aria-current={running ? "step" : undefined}
    onToggle={(event) => setOpened(event.currentTarget.open)}
  >
    {opened ? <div className="timeline-step-detail">
      <ToolChipStatus lines={model.statusLines} />
      <ToolChipMeta lines={model.meta} />
      {presentation?.fields.length ? <dl className="tool-fields">
        {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
      </dl> : null}
      {running && liveOutput
        ? <ToolExecutionLog output={liveOutput} label={`${model.label} · ${translator(language)("fieldDetail")}`} />
        : presentation?.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
    </div> : null}
  </ToolChip>;
}
