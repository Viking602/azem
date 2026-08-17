import type { Block } from "../../types";

export type StepMarkState = "done" | "running" | "pending" | "failed" | "note";

/** Position of a row on the shared rail. Interior rows carry no edge. */
export type StepEdge = "first" | "last" | "only" | undefined;

export const STEP_STAGGER_MS = 120;
/** Cap the cascade so a replayed trail cannot delay its last row by seconds. */
export const STEP_STAGGER_MAX = 6;

const RUNNING = ["running", "started", "streaming", "progress"];
const FAILED = ["failed", "cancelled", "error"];
const PENDING = ["queued", "pending", "awaiting_approval", "reviewing_approval"];

export function stepMarkState(state: string | undefined): StepMarkState {
  const value = (state || "").trim();
  if (RUNNING.includes(value)) return "running";
  if (FAILED.includes(value)) return "failed";
  if (PENDING.includes(value)) return "pending";
  return "done";
}

/**
 * Azem dispatches tools in parallel, so several rows can legitimately spin at
 * once. Report the real aggregate instead of forcing a single active row.
 */
export function blocksMarkState(blocks: readonly Block[]): StepMarkState {
  if (!blocks.length) return "done";
  const states = blocks.map((block) => stepMarkState(block.state));
  if (states.includes("running")) return "running";
  if (states.includes("failed")) return "failed";
  if (states.every((state) => state === "pending")) return "pending";
  return "done";
}

export function stepEdge(index: number, count: number): StepEdge {
  if (count <= 1) return "only";
  if (index === 0) return "first";
  if (index === count - 1) return "last";
  return undefined;
}

export function stepEnterDelay(order: number) {
  return Math.min(Math.max(order, 0), STEP_STAGGER_MAX) * STEP_STAGGER_MS;
}

/**
 * Only rows that were not on screen before may animate. A virtualized window
 * remounts existing rows while scrolling and must never replay their entrance.
 */
export function stepEntranceDelays(
  ids: readonly string[],
  seen: ReadonlySet<string> | null,
): Map<string, number> {
  const delays = new Map<string, number>();
  let order = 0;
  for (const id of ids) {
    if (seen?.has(id)) continue;
    delays.set(id, stepEnterDelay(order));
    order += 1;
  }
  return delays;
}
