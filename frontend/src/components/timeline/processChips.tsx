import { ChevronDown } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { Block, Snapshot } from "../../types";
import {
  displayedToolState,
  formatDuration,
  formatToolPresentation,
  isActiveProcessBlock,
  isRunningTool,
  processElapsedMs,
  thinkingStateLabel,
  thinkingTraceElapsedMs,
} from "../toolTimeline";
import {
  fileChangePillsForBlocks,
  processGroupCountLabel,
  thinkingChipPreview,
  toolChipModel,
} from "../toolChip";
import AnsiText from "../AnsiText";
import { ToolTimelineStep } from "../assistant-ui/ToolTimelineStep";
import { ToolTimelineFiles, ToolTimelineItem, ToolTimelineMeta, ToolTimelineStatus } from "../assistant-ui/ToolTimeline";
import {
  isActiveFileChangeBlock,
  isFileChangeTool,
  isPendingFileChangeBlock,
} from "../fileChanges";
import { ToolTimelineBlock, toolStatusLabel } from "./blocks";
import { blocksMarkState, stepEdge, stepEntranceDelays, stepMarkState } from "./stepRail";
import { ReasoningSteps } from "./ThinkingTrace";
import { ToolExecutionLog } from "./ToolExecutionLog";
import { useLiveElapsed } from "./useLiveElapsed";

export function ProcessChipList({ blocks, language, siblings, live = false, summarized = false, hideClocks = false }: {
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
  return <div className={`timeline-step-list${grouped ? " aui-tool-timeline" : ""}`} data-settled={settledCard || undefined}>
    {countLabel && tools.length >= 2 ? <div className="aui-tool-timeline-header">
      <ChevronDown className="aui-tool-timeline-item-chevron" size={14} aria-hidden="true" />
      <strong>{countLabel}</strong>
    </div> : null}
    <div className="timeline-step-rows" role="list">
      {thinking.length ? <ToolTimelineStep
        key={rowIds[0]}
        mark={blocksMarkState(thinking)}
        edge={stepEdge(index++, count)}
        delayMs={delays.get(rowIds[0]!)}
      >
        <ThinkingChip blocks={thinking} language={language} />
      </ToolTimelineStep> : null}
      {tools.map((block) => <ToolTimelineStep
        key={block.id}
        mark={stepMarkState(displayedToolState(block, peers))}
        edge={stepEdge(index++, count)}
        delayMs={delays.get(block.id)}
      >
        {toolRow(block)}
      </ToolTimelineStep>)}
    </div>
    {pills.files.length ? <ToolTimelineFiles files={pills.files} language={language} /> : null}
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

export function ThinkingChip({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const [opened, setOpened] = useState(false);
  const running = blocks.some((block) => isActiveProcessBlock(block) && Boolean(block.content?.trim()));
  const preview = thinkingChipPreview(blocks);
  return <ToolTimelineItem
    className="timeline-step thinking-chip"
    state={running ? "running" : "completed"}
    kind="thinking"
    label={thinkingStateLabel(language, running, running ? 0 : thinkingTraceElapsedMs(blocks))}
    chip={preview || undefined}
    onToggle={(event) => setOpened(event.currentTarget.open)}
  >
    {opened ? <div className="timeline-step-detail aui-reasoning-content" data-tab="reasoning">
      <ReasoningSteps blocks={blocks} />
    </div> : null}
  </ToolTimelineItem>;
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
  return <ToolTimelineItem
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
      <ToolTimelineStatus lines={model.statusLines} />
      <ToolTimelineMeta lines={model.meta} />
      {presentation?.fields.length ? <dl className="tool-fields">
        {presentation.fields.map((field) => <div key={`${field.label}-${field.value}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}
      </dl> : null}
      {running && liveOutput
        ? <ToolExecutionLog output={liveOutput} label={`${model.label} · ${translator(language)("fieldDetail")}`} />
        : presentation?.result ? <pre className="tool-result"><AnsiText text={presentation.result} /></pre> : null}
    </div> : null}
  </ToolTimelineItem>;
}
