import { ChevronDown, FilePenLine } from "lucide-react";
import { Fragment, memo, useEffect, useRef, useState } from "react";
import { tFormat, translator } from "../i18n";
import { isSubagentActive } from "../subagents";
import { useRuntimeStore } from "../store";
import type { Block, Snapshot } from "../types";
import { isRunningTool } from "./toolTimeline";
import { ThinkingState } from "./beautiful-ui/Primitives";
import type { EditedFileSummary } from "./fileChanges";
import {
  projectSessionDocument,
  turnEditedFiles,
  type SessionTurn,
  type SessionTurnItem,
} from "./sessionDocument";
import { isActiveReasoning, TimelineBlock } from "./timeline/blocks";
import { isSubagentSpawnBlock, ProcessEntries, ProcessFold } from "./timeline/process";
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
  const showThinkingPlaceholder = waitingForModel && !activeDelegation && !runningSpawn
    && !blocks.some((block) => isActiveReasoning(block)
      && (!activeRunId || !block.runId || block.runId === activeRunId));

  const multiTurn = projection.turns.length > 1;
  return <div className="timeline-feed session-document" ref={feed}>
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
  item, language, featured, collapseCompletedProcess,
}: {
  item: SessionTurnItem;
  language: Snapshot["language"];
  featured: boolean;
  foldActiveProcess?: boolean;
  collapseCompletedProcess: boolean;
}) {
  if (item.kind === "block") {
    return <TimelineBlock block={item.block} language={language} />;
  }
  // Live work stays expanded. Folding to a summary is only allowed after the
  // trail completes (“已处理”). foldActiveProcess is kept for callers but
  // cannot hide an in-flight process behind “处理中”.
  if (item.active) {
    return <ProcessEntries blocks={item.blocks} language={language} active />;
  }
  return <ProcessFold
    blocks={item.blocks}
    elapsedMs={item.elapsedMs}
    language={language}
    featured={featured}
    active={false}
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

function ThinkingPlaceholder({ language }: { language: Snapshot["language"] }) {
  const label = translator(language)("thinkingActive");
  return <div className="reasoning-placeholder" role="status" aria-live="polite">
    <ThinkingState active expanded={false} label={label} disabled />
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
