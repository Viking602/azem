import type { Block } from "../types";
import {
  processElapsedMs,
  segmentProcessTrail,
  type ProcessSegment,
} from "./toolTimeline";
import { aggregateEditedFiles, type EditedFileSummary } from "./fileChanges";

/** One user turn projected for document-style reading (not an event stream). */
export type SessionTurn = {
  id: string;
  user?: Block;
  items: SessionTurnItem[];
};

export type SessionTurnItem =
  | { kind: "process"; id: string; blocks: Block[]; elapsedMs: number; active: boolean }
  | { kind: "block"; block: Block };

export type SessionDocumentProjection = {
  turns: SessionTurn[];
  /** Index of the turn that should render fully expanded. */
  currentIndex: number;
};

/**
 * Project durable timeline blocks into reading turns.
 * Persistence order is preserved inside each turn; the feed is no longer a
 * flat event stream of every tool tick.
 */
export function projectSessionDocument(
  blocks: Block[],
  options: { activeRunId?: string; running?: boolean; now?: number } = {},
): SessionDocumentProjection {
  const segments = segmentProcessTrail(blocks, options);
  const turns: SessionTurn[] = [];
  let current: SessionTurn | null = null;

  const openTurn = (user?: Block): SessionTurn => {
    const turn: SessionTurn = {
      id: user?.id || `turn-${turns.length + 1}`,
      user,
      items: [],
    };
    current = turn;
    turns.push(turn);
    return turn;
  };

  for (const segment of segments) {
    if (segment.kind === "block" && segment.block.kind === "user") {
      openTurn(segment.block);
      continue;
    }
    const turn = current ?? openTurn();
    turn.items.push(toTurnItem(segment));
  }

  return {
    turns,
    currentIndex: Math.max(0, turns.length - 1),
  };
}

function toTurnItem(segment: ProcessSegment): SessionTurnItem {
  if (segment.kind === "process") {
    return {
      kind: "process",
      id: segment.id,
      blocks: segment.blocks,
      elapsedMs: segment.elapsedMs,
      active: segment.active,
    };
  }
  return { kind: "block", block: segment.block };
}

/** Blocks that must stay in the main reading column (user decisions / failures). */
export function isInterruptBlock(block: Block) {
  return block.kind === "approval"
    || block.kind === "question"
    || block.kind === "plan"
    || block.kind === "error";
}

/** Final answer prose that should read as the document body. */
export function isAnswerBlock(block: Block) {
  return block.kind === "assistant";
}

/** One-line preview for collapsed historical turns. */
export function turnAnswerPreview(turn: SessionTurn, max = 96): string {
  const answers = turn.items
    .filter((item): item is Extract<SessionTurnItem, { kind: "block" }> => item.kind === "block")
    .map((item) => item.block)
    .filter((block) => block.kind === "assistant" || block.kind === "error")
    .map((block) => (block.content || block.title || "").replace(/\s+/g, " ").trim())
    .filter(Boolean);
  const text = answers.at(-1) || "";
  if (!text) return "";
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

export function turnQuestionPreview(turn: SessionTurn, max = 72): string {
  const text = (turn.user?.content || "").replace(/\s+/g, " ").trim();
  if (!text) return "";
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

/** Aggregate file edits for a turn when its runs have finished. */
export function turnEditedFiles(
  turn: SessionTurn,
  options: { activeRunId?: string; running?: boolean } = {},
): EditedFileSummary | null {
  const blocks: Block[] = [];
  for (const item of turn.items) {
    if (item.kind === "process") blocks.push(...item.blocks);
    else blocks.push(item.block);
  }
  const byRun = new Map<string, Block[]>();
  for (const block of blocks) {
    if (!block.runId) continue;
    if (options.running && options.activeRunId && block.runId === options.activeRunId) continue;
    const list = byRun.get(block.runId) ?? [];
    list.push(block);
    byRun.set(block.runId, list);
  }
  let merged: EditedFileSummary | null = null;
  for (const runBlocks of byRun.values()) {
    const summary = aggregateEditedFiles(runBlocks);
    if (!summary.files.length) continue;
    if (!merged) {
      merged = {
        files: summary.files.map((file) => ({ ...file })),
        additions: summary.additions,
        deletions: summary.deletions,
      };
      continue;
    }
    for (const file of summary.files) {
      const existing = merged.files.find((item) => item.path === file.path);
      if (existing) {
        existing.additions += file.additions;
        existing.deletions += file.deletions;
      } else {
        merged.files.push({ ...file });
      }
    }
    merged.additions += summary.additions;
    merged.deletions += summary.deletions;
  }
  return merged?.files.length ? merged : null;
}

/** True when a turn still has live process work. */
export function turnHasActiveProcess(turn: SessionTurn) {
  return turn.items.some((item) => item.kind === "process" && item.active);
}

export function recomputeProcessElapsed(blocks: Block[], active: boolean, now = Date.now()) {
  return processElapsedMs(blocks, active ? now : 0);
}
