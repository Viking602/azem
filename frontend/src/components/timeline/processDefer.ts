import type { Block } from "../../types";
import type { Language } from "../../i18n";
import {
  groupProcessTimelineBlocks,
  isHostFallbackCommentary,
  type ModelProgressPresentation,
} from "../toolTimeline";

export const DEFERRED_PROCESS_WINDOW = 28;
export const DEFERRED_PROCESS_OVERSCAN = 6;
export const DEFERRED_PROCESS_ROW_PX = 36;
/** Small completed trails keep commentary grouping; large ones expand as chips. */
export const DEFERRED_PROCESS_MIN_ROWS = 16;

export type DeferredProcessRow =
  | { kind: "progress"; id: string; block: Block; presentation: ModelProgressPresentation }
  | { kind: "tool"; id: string; block: Block }
  | { kind: "thinking"; id: string; blocks: Block[] }
  | { kind: "subagent"; id: string; blocks: Block[] };

export function isDeferredSubagentSpawn(block: Block) {
  return block.kind === "tool" && (block.title || "").replaceAll("_", ".") === "subagent.spawn";
}

/** Flatten a completed process trail into compact rows. Host fallback commentary stays hidden. */
export function flattenDeferredProcessRows(blocks: Block[], language: Language): DeferredProcessRow[] {
  const rows: DeferredProcessRow[] = [];
  for (const entry of groupProcessTimelineBlocks(blocks, language)) {
    if (entry.kind === "model-progress") {
      if (!isHostFallbackCommentary(entry.block)) {
        rows.push({
          kind: "progress",
          id: `progress-${entry.block.id}`,
          block: entry.block,
          presentation: progressPresentation(entry.block, entry.presentation),
        });
      }
      pushDetailRows(rows, entry.blocks);
      continue;
    }
    if (entry.kind === "tool-group" || entry.kind === "thinking-trail") {
      pushDetailRows(rows, entry.blocks);
      continue;
    }
    pushDetailRows(rows, [entry.block]);
  }
  return hoistDeferredThinking(rows);
}

function pushDetailRows(rows: DeferredProcessRow[], blocks: Block[]) {
  const spawns = blocks.filter(isDeferredSubagentSpawn);
  const thinkings = blocks.filter((block) => block.kind === "thinking");
  let thinkingEmitted = false;
  for (const block of blocks) {
    if (isDeferredSubagentSpawn(block)) continue;
    if (block.kind === "thinking") {
      if (!thinkingEmitted && thinkings.length) {
        rows.push({ kind: "thinking", id: `thinking-${thinkings[0]!.id}`, blocks: thinkings });
        thinkingEmitted = true;
      }
      continue;
    }
    if (block.kind === "tool" || block.kind === "diff") {
      rows.push({ kind: "tool", id: `tool-${block.id}`, block });
    }
  }
  if (spawns.length) {
    rows.push({ kind: "subagent", id: `subagent-${spawns[0]!.id}`, blocks: spawns });
  }
}

/** A settled card always leads with 思考, even when reasoning arrived after tools. */
export function hoistDeferredThinking(rows: DeferredProcessRow[]): DeferredProcessRow[] {
  const thinking = rows.filter((row): row is Extract<DeferredProcessRow, { kind: "thinking" }> => row.kind === "thinking");
  if (!thinking.length) return rows;
  const rest = rows.filter((row) => row.kind !== "thinking");
  if (thinking.length === 1 && rows[0]?.kind === "thinking") return rows;
  return [{
    kind: "thinking",
    id: thinking[0]!.id,
    blocks: thinking.flatMap((row) => row.blocks),
  }, ...rest];
}

export function deferredProcessWindow(
  count: number,
  scrollTop: number,
  viewportHeight: number,
  listOffsetTop = 0,
): { start: number; end: number } {
  if (count <= DEFERRED_PROCESS_WINDOW) return { start: 0, end: count };
  if (viewportHeight <= 0) return { start: 0, end: Math.min(count, DEFERRED_PROCESS_WINDOW) };
  const first = Math.floor((scrollTop - listOffsetTop) / DEFERRED_PROCESS_ROW_PX);
  const visible = Math.max(1, Math.ceil(viewportHeight / DEFERRED_PROCESS_ROW_PX));
  const start = Math.max(0, first - DEFERRED_PROCESS_OVERSCAN);
  let end = Math.min(count, Math.max(first, 0) + visible + DEFERRED_PROCESS_OVERSCAN);
  if (end - start < DEFERRED_PROCESS_WINDOW) {
    end = Math.min(count, start + DEFERRED_PROCESS_WINDOW);
  }
  return { start, end };
}

function progressPresentation(block: Block, presentation: ModelProgressPresentation): ModelProgressPresentation {
  if (presentation.title.trim() || presentation.detail.trim()) return presentation;
  const text = (block.content || "").replace(/\s+/g, " ").trim();
  return { title: "", detail: text };
}

export function deferredProgressChip(presentation: ModelProgressPresentation, fallback: string) {
  const title = presentation.title.trim() || fallback;
  const detail = presentation.detail.trim();
  if (detail.length <= 42) return { title, chip: detail };
  return { title, chip: `${detail.slice(0, 41)}…` };
}
