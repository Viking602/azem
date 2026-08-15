import { ChevronDown, FilePenLine } from "lucide-react";
import { Fragment, memo, useEffect, useRef, useState } from "react";
import { tFormat, translator } from "../i18n";
import { isSubagentActive } from "../subagents";
import { useRuntimeStore } from "../store";
import type { Block, Snapshot } from "../types";
import { isRunningTool, processTrailWorthFolding, shouldShowThinkingWait } from "./toolTimeline";
import type { EditedFileSummary } from "./fileChanges";
import {
  projectSessionDocument,
  turnEditedFiles,
  type SessionTurn,
  type SessionTurnItem,
} from "./sessionDocument";
import { TimelineBlock } from "./timeline/blocks";
import { isSubagentSpawnBlock, ProcessEntries, ProcessFold, ProcessStatusRule } from "./timeline/process";
export { approvalPresentation, TimelineBlock, visibleCommentaryTitle } from "./timeline/blocks";

function TimelineFeedView({
  blocks, language, compact = false, activeRunId = "", running = false, waitingForModel = false,
  foldActiveProcess = false, collapseCompletedProcess = false,
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
  const searchTarget = useRuntimeStore((state) => state.sessionSearchTarget);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const feed = useRef<HTMLDivElement>(null);
  const activeDelegation = useRuntimeStore((state) => Boolean(activeRunId) && state.agents.some((agent) =>
    isSubagentActive(agent.state) && agent.parentRunId === activeRunId));
  useEffect(() => {
    if (!searchTarget || searchTarget.sessionId !== currentSessionId) return;
    if (searchTarget.sequence == null) {
      useRuntimeStore.getState().setSessionSearchTarget(null);
      return;
    }
    let clearTimer = 0;
    let revealRoot: HTMLElement | null = null;
    const frame = requestAnimationFrame(() => {
      const node = feed.current?.querySelector<HTMLElement>(`[data-session-sequence="${searchTarget.sequence}"]`);
      if (!node) return;
      revealRoot = node.closest<HTMLElement>(".session-history-turn, .session-turn-current");
      revealRoot?.classList.add("timeline-search-reveal");
      // Long transcripts use content-visibility for normal scrolling. Force the
      // selected turn to acquire its real height before scrollIntoView so the
      // browser cannot center the match using a stale intrinsic placeholder.
      if (revealRoot) void revealRoot.offsetHeight;
      node.scrollIntoView?.({ block: "center", behavior: document.documentElement.dataset.reduceMotion === "true" ? "auto" : "smooth" });
      node.classList.add("timeline-search-hit");
      clearTimer = window.setTimeout(() => {
        node.classList.remove("timeline-search-hit");
        revealRoot?.classList.remove("timeline-search-reveal");
        useRuntimeStore.getState().setSessionSearchTarget(null);
      }, 1800);
    });
    return () => {
      cancelAnimationFrame(frame);
      window.clearTimeout(clearTimer);
      revealRoot?.classList.remove("timeline-search-reveal");
    };
  }, [blocks, currentSessionId, searchTarget]);
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
  // First-token and between-batch waits use the same thinking row as a live
  // or completed trace. Completed/failed tools and empty thinking frames are
  // not live progress (UI-016 / UI-008).
  const waiting = shouldShowThinkingWait(blocks, {
    waiting: waitingForModel || running,
    activeRunId,
    activeDelegation,
    runningSpawn,
  });
  const turns = projection.turns.length > 0
    ? projection.turns
    : waiting || running ? [{ id: "current-empty", items: [] }] : [];
  const currentIndex = Math.max(0, turns.length - 1);
  const multiTurn = turns.length > 1;
  return <div className="timeline-feed session-document" ref={feed}>
    {turns.map((turn, index) => {
      const current = index === currentIndex;
      return <SessionTurnView
        key={turn.id}
        turn={turn}
        language={language}
        index={index}
        current={current}
        multiTurn={multiTurn}
        activeRunId={activeRunId}
        running={running}
        waiting={waiting}
        foldActiveProcess={foldProcess}
        collapseCompletedProcess={current ? collapseCompleted : true}
      />;
    })}
  </div>;
}

function SessionTurnView({
  turn, language, index, current, multiTurn, activeRunId, running, waiting,
  foldActiveProcess, collapseCompletedProcess,
}: {
  turn: SessionTurn;
  language: Snapshot["language"];
  index: number;
  current: boolean;
  multiTurn: boolean;
  activeRunId: string;
  running: boolean;
  waiting: boolean;
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
}) {
  const t = translator(language);
  const fileSummary = turnEditedFiles(turn, { activeRunId, running });
  const liveTurn = Boolean(current && (waiting || turn.items.some((item) => item.kind === "process" && item.active)));
  return <section
    className={`session-turn ${current ? `session-turn-current${multiTurn ? " has-history-context" : ""}` : "session-history-turn"}`}
    data-screen-label={current ? "current-turn" : undefined}
    data-turn={current ? undefined : String(index + 1).padStart(2, "0")}
  >
    {current
      ? multiTurn
        ? <div className="session-turn-label">
          <span className="session-turn-index current">{t("currentTurn")}</span>
        </div>
        : null
      : <div className="session-turn-label">
        <span className="session-turn-index">{tFormat(language, "turnIndex", { n: String(index + 1).padStart(2, "0") })}</span>
      </div>}
    {liveTurn ? <ProcessStatusRule blocks={turnProcessBlocks(turn)} live language={language} /> : null}
    {turn.user ? <TimelineBlock block={turn.user} language={language} /> : null}
    <TurnItems
      turn={turn}
      language={language}
      foldActiveProcess={foldActiveProcess}
      collapseCompletedProcess={collapseCompletedProcess}
      waiting={current && waiting}
      pendingStep={current && waiting}
    />
    {fileSummary ? <EditedFilesSummary summary={fileSummary} language={language} /> : null}
  </section>;
}

function TurnItems({
  turn, language, foldActiveProcess, collapseCompletedProcess, waiting = false, pendingStep = false,
}: {
  turn: SessionTurn;
  language: Snapshot["language"];
  foldActiveProcess: boolean;
  collapseCompletedProcess: boolean;
  waiting?: boolean;
  /** Render the first-token wait as the step that is about to receive blocks. */
  pendingStep?: boolean;
}) {
  const processIndexes = turn.items
    .map((item, index) => item.kind === "process" ? index : -1)
    .filter((index) => index >= 0);
  const latestProcessIndex = processIndexes.at(-1) ?? -1;
  const foldableProcessRuns = new Set(
    turn.items.flatMap((item) => {
      if (item.kind !== "process" || !processTrailWorthFolding(item.blocks)) return [];
      return item.blocks.map((block) => block.runId || "").filter(Boolean);
    }),
  );

  // UI-016: the wait, the first thinking and the tool step are one bar. Keying a
  // step by its ordinal instead of its first block keeps that single element
  // alive from the empty wait through the blocks that land in it.
  const stepKeys = new Map(processIndexes.map((index, ordinal) => [index, `process-step-${ordinal}`]));
  const children = turn.items.map((item, index) => {
    const previous = turn.items[index - 1];
    return <Fragment key={stepKeys.get(index) ?? itemKey(item)}>
      {shouldShowAnswerSection(item, previous, foldableProcessRuns) ? <AnswerSection language={language} /> : null}
      <TurnItemView
        item={item}
        language={language}
        featured={item.kind === "process" && index === latestProcessIndex}
        foldActiveProcess={foldActiveProcess}
        collapseCompletedProcess={collapseCompletedProcess}
        waiting={waiting && index === latestProcessIndex}
      />
    </Fragment>;
  });
  // The wait joins the same keyed list, so the element it hands over to is the
  // one already on screen rather than a replacement in another slot.
  if (pendingStep && latestProcessIndex < 0) {
    const pending: SessionTurnItem = {
      kind: "process", id: "pending-step", blocks: NO_BLOCKS, elapsedMs: 0, active: true,
    };
    children.push(<Fragment key={`process-step-${processIndexes.length}`}>
      {null}
      <TurnItemView
        item={pending}
        language={language}
        featured
        foldActiveProcess={foldActiveProcess}
        collapseCompletedProcess={collapseCompletedProcess}
        waiting
      />
    </Fragment>);
  }
  return <>{children}</>;
}

const NO_BLOCKS: Block[] = [];

function turnProcessBlocks(turn: SessionTurn) {
  return turn.items.flatMap((item) => item.kind === "process" ? item.blocks : []);
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
  item, language, featured, collapseCompletedProcess, waiting = false,
}: {
  item: SessionTurnItem;
  language: Snapshot["language"];
  featured: boolean;
  foldActiveProcess?: boolean;
  collapseCompletedProcess: boolean;
  waiting?: boolean;
}) {
  if (item.kind === "block") {
    return <TimelineBlock block={item.block} language={language} />;
  }
  // Every process step — live or settled — is one sparkle bar plus its body.
  // A running step stays expanded and cannot be collapsed away.
  return <ProcessFold
    blocks={item.blocks}
    elapsedMs={item.elapsedMs}
    language={language}
    featured={featured}
    active={item.active}
    waiting={waiting}
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

function AnswerSection({ language }: { language: Snapshot["language"] }) {
  return <div className="run-status-marker section-marker final-answer-marker" role="separator">
    <span>{translator(language)("finalAnswer")}</span>
  </div>;
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
      <span>{file.path}</span>
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
