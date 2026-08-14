import { Check, ChevronDown, ChevronRight, Command, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { tFormat, toolDisplayName, translator } from "../../i18n";
import {
  isSubagentActive, isSubagentTerminal, subagentDisplayName, subagentPreviewText,
  subagentStatusLabel, subagentSummaryLabel,
} from "../../subagents";
import { useRuntimeStore } from "../../store";
import type { AgentState, Block, Snapshot } from "../../types";
import {
  displayedToolState, formatDuration, formatToolPresentation, groupProcessTimelineBlocks, isActiveProcessBlock,
  isRunningTool, processElapsedMs, summarizeToolGroup, type ProcessTimelineEntry,
} from "../toolTimeline";
import AnsiText from "../AnsiText";
import SubagentGlyph from "../SubagentGlyph";
import { ToolRow } from "../beautiful-ui/Primitives";
import {
  fileChangesForBlock, isActiveFileChangeBlock, isPendingFileChangeBlock, pendingFileChangeSummaryForBlock,
} from "../fileChanges";
import { DiffBlock, ReasoningTrace, TimelineBlock, ToolTimelineBlock, toolStatusLabel } from "./blocks";
import { ToolExecutionLog } from "./ToolExecutionLog";
import { useLiveElapsed } from "./useLiveElapsed";

export function ProcessFold({ blocks, elapsedMs, language, featured, active = false, collapseCompleted = false }: {
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
  const open = active || expanded;
  return <details
    className="process-fold"
    data-featured={featured || undefined}
    data-state={active ? "running" : "completed"}
    aria-busy={active || undefined}
    open={open}
    onToggle={(event) => {
      if (active) {
        event.currentTarget.open = true;
        return;
      }
      setExpanded(event.currentTarget.open);
    }}
  >
    <summary>
      <span className="process-fold-chevrons" aria-hidden="true">
        <ChevronRight className="closed-chevron" size={14} />
        <ChevronDown className="open-chevron" size={14} />
      </span>
      <span className="process-fold-label">{label}</span>
      {duration ? <time>{duration}</time> : null}
    </summary>
    {open ? <div className="process-fold-body">
      <ProcessEntries blocks={blocks} language={language} active={active} />
    </div> : null}
  </details>;
}

export function ProcessEntries({ blocks, language, active = false, compact = false }: {
  blocks: Block[];
  language: Snapshot["language"];
  active?: boolean;
  compact?: boolean;
}) {
  const entries = groupProcessTimelineBlocks(blocks, language);
  const visibleEntries = active ? activeProcessEntries(entries) : entries;
  return <div className={`process-entries ${active ? "active" : ""}`} aria-live={active ? "polite" : undefined} aria-busy={active || undefined}>
    {visibleEntries.map((entry) => <ProcessEntry key={entry.kind === "block" ? entry.block.id : entry.id} entry={entry} language={language} compact={compact} siblings={blocks} />)}
  </div>;
}

type ModelProgressEntry = Extract<ProcessTimelineEntry, { kind: "model-progress" }>;
type ActiveProcessEntry = ProcessTimelineEntry | { kind: "tool-steps"; id: string; blocks: Block[] };

function ProcessEntry({ entry, language, compact, siblings }: {
  entry: ActiveProcessEntry;
  language: Snapshot["language"];
  compact: boolean;
  siblings: Block[];
}) {
  if (entry.kind === "model-progress") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const detailBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      <ModelProgressStep entry={entry} detailBlocks={detailBlocks} language={language} siblings={siblings} />
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "tool-steps") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const toolBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      {toolBlocks.length ? <ToolStepList blocks={toolBlocks} language={language} siblings={siblings} /> : null}
      {spawnBlocks.length ? <SubagentRunCard blocks={spawnBlocks} language={language} /> : null}
    </>;
  }
  if (entry.kind === "tool-group") {
    const spawnBlocks = entry.blocks.filter(isSubagentSpawnBlock);
    const toolBlocks = entry.blocks.filter((block) => !isSubagentSpawnBlock(block));
    return <>
      {toolBlocks.length ? <ToolGroup blocks={toolBlocks} summary={summarizeToolGroup(toolBlocks, language)} language={language} siblings={siblings} /> : null}
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

function ModelProgressStep({ entry, detailBlocks = entry.blocks, language, siblings }: {
  entry: ModelProgressEntry;
  detailBlocks?: Block[];
  language: Snapshot["language"];
  siblings: Block[];
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
      {detailBlocks.map((block) => <ModelProgressDetail key={block.id} block={block} language={language} siblings={siblings} />)}
    </div> : null}
  </details>;
}

function ModelProgressDetail({ block, language, siblings }: { block: Block; language: Snapshot["language"]; siblings: Block[] }) {
  if (block.kind === "thinking") return <ReasoningTrace block={block} language={language} />;
  if (block.kind === "tool") return <ToolTimelineBlock block={block} language={language} compact nested siblings={siblings} />;
  if (block.kind === "diff") return <DiffBlock block={block} language={language} nested />;
  return <TimelineBlock block={block} language={language} compact siblings={siblings} />;
}

export function isSubagentSpawnBlock(block: Block) {
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


function ToolGroup({ blocks, summary, language, siblings }: { blocks: Block[]; summary: string; language: Snapshot["language"]; siblings: Block[] }) {
  if (blocks.every((block) => block.data?.presentation === "steps")) {
    return <ToolStepList blocks={blocks} language={language} siblings={siblings} />;
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
      {blocks.map((block) => <TimelineBlock key={block.id} block={block} language={language} compact nested siblings={siblings} />)}
    </div>
  </details>;
}

function ToolStepList({ blocks, language, siblings = blocks }: { blocks: Block[]; language: Snapshot["language"]; siblings?: Block[] }) {
  return <div className="timeline-step-list" role="list">
    {blocks.map((block) => fileChangesForBlock(block).length
      || pendingFileChangeSummaryForBlock(block)
      || isActiveFileChangeBlock(block)
      || isPendingFileChangeBlock(block)
      ? <ToolTimelineBlock key={block.id} block={block} language={language} siblings={siblings} />
      : <ToolStep key={block.id} block={block} language={language} siblings={siblings} />)}
  </div>;
}

function ToolStep({ block, language, siblings }: { block: Block; language: Snapshot["language"]; siblings: Block[] }) {
  const [opened, setOpened] = useState(false);
  const storeBlocks = useRuntimeStore((state) => state.blocks);
  const peers = siblings.length ? siblings : storeBlocks;
  const state = displayedToolState(block, peers);
  const running = state === "running" || isRunningTool(block);
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
  const time = elapsedMs > 0
    ? formatDuration(elapsedMs)
    : running ? translator(language)("running") : completed ? "" : toolStatusLabel(state, language);
  const label = block.title ? toolDisplayName(block.title, language) : translator(language)("toolGeneric");
  return <ToolRow className="timeline-step" state={state} role="listitem" aria-current={running ? "step" : undefined} onToggle={(event) => setOpened(event.currentTarget.open)}>
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
  </ToolRow>;
}
