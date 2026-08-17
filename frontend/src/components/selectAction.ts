import type { Language } from "../i18n";
import { tFormat } from "../i18n";

export type SelectActionKind = "explain" | "improve" | "describe";

export type SelectionRect = {
  left: number;
  right: number;
  top: number;
  bottom: number;
  width: number;
  height: number;
};

export type ClassifiedSelection =
  | { kind: "hit"; text: string; rect: SelectionRect }
  | { kind: "ignore" }
  | { kind: "miss" };

export const SELECTABLE_PROSE_SELECTOR = ".assistant-block, .commentary-block, .user-block";
export const ACTION_ISLAND_SELECTOR = ".bui-action-island";
export const EXCLUDED_SELECTION_SELECTOR = [
  ACTION_ISLAND_SELECTOR,
  ".reasoning-trace",
  ".reasoning-body",
  ".bui-thinking-state",
  ".bui-loading-state",
  ".reasoning-placeholder",
  ".tool-block",
  ".bui-tool-row",
  ".bui-tool-chip",
  ".bui-tool-chip-group",
  ".bui-file-change-pills",
  ".tool-detail",
  ".tool-log",
  ".tool-group",
  ".approval-block",
  ".bui-approval-card",
  ".subagent-wake-notice",
  ".code-diff-scroll",
  ".timeline-step",
  ".composer-card",
  "#azem-composer",
].join(", ");

export function quoteAsMarkdown(text: string): string {
  return text.replace(/\r\n/g, "\n").split("\n").map((line) => `> ${line}`).join("\n");
}

export function buildSelectActionPrompt(
  language: Language,
  kind: SelectActionKind,
  selectedText: string,
  instruction = "",
): string {
  const quote = quoteAsMarkdown(selectedText.trim());
  if (kind === "explain") return tFormat(language, "selectActionExplainPrompt", { quote });
  if (kind === "improve") return tFormat(language, "selectActionImprovePrompt", { quote });
  return tFormat(language, "selectActionDescribePrompt", { quote, instruction: instruction.trim() });
}

export function classifyTranscriptSelection(
  selection: Selection | null,
  root: Element | null,
): ClassifiedSelection {
  if (!selection || selection.rangeCount === 0) return { kind: "miss" };
  const range = selection.getRangeAt(0);
  if (isInsideIsland(range.startContainer) || isInsideIsland(range.endContainer)) return { kind: "ignore" };
  const text = selection.toString().replace(/\u00a0/g, " ").trim();
  if (!text || selection.isCollapsed || range.collapsed) return { kind: "miss" };
  if (!root || !rangeIntersectsRoot(range, root)) return { kind: "miss" };
  if (!isSelectableProseNode(range.startContainer) || !isSelectableProseNode(range.endContainer)) {
    return { kind: "miss" };
  }
  return { text, rect: selectionClientRect(range), kind: "hit" };
}

export function actionIslandPosition(
  selection: SelectionRect,
  viewport: { width: number; height: number },
  options: {
    islandWidth?: number;
    islandHeight?: number;
    gap?: number;
    margin?: number;
    composerTop?: number;
  } = {},
): { top: number; left: number; placement: "below" | "above" } {
  const islandWidth = options.islandWidth ?? 400;
  const islandHeight = options.islandHeight ?? 40;
  const gap = options.gap ?? 8;
  const margin = options.margin ?? 12;
  const floor = options.composerTop != null
    ? Math.min(viewport.height, options.composerTop) - margin
    : viewport.height - margin;
  const belowTop = selection.bottom + gap;
  const aboveTop = selection.top - gap - islandHeight;
  const fitsBelow = belowTop + islandHeight <= floor;
  const fitsAbove = aboveTop >= margin;
  const placement: "below" | "above" = fitsBelow || !fitsAbove ? "below" : "above";
  const top = clamp(placement === "below" ? belowTop : aboveTop, margin, Math.max(margin, floor - islandHeight));
  const center = (selection.left + selection.right) / 2;
  const left = clamp(center - islandWidth / 2, margin, Math.max(margin, viewport.width - margin - islandWidth));
  return { top, left, placement };
}

export function cycleIslandFocus(root: HTMLElement, event: KeyboardEvent): boolean {
  if (event.key !== "Tab") return false;
  const nodes = islandFocusables(root);
  if (nodes.length === 0) return false;
  const index = nodes.indexOf(document.activeElement as HTMLElement);
  const next = event.shiftKey
    ? (index <= 0 ? nodes.length - 1 : index - 1)
    : (index === nodes.length - 1 || index < 0 ? 0 : index + 1);
  event.preventDefault();
  nodes[next]?.focus();
  return true;
}

export function islandFocusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLInputElement | HTMLButtonElement>("input, button")].filter((node) => !node.disabled);
}

function isInsideIsland(node: Node): boolean {
  const element = node instanceof Element ? node : node.parentElement;
  return Boolean(element?.closest(ACTION_ISLAND_SELECTOR));
}

function isSelectableProseNode(node: Node): boolean {
  const element = node instanceof Element ? node : node.parentElement;
  if (!element) return false;
  if (element.closest(EXCLUDED_SELECTION_SELECTOR)) return false;
  return Boolean(element.closest(SELECTABLE_PROSE_SELECTOR));
}

function rangeIntersectsRoot(range: Range, root: Element): boolean {
  if (root.contains(range.commonAncestorContainer)) return true;
  return root.contains(range.startContainer) && root.contains(range.endContainer);
}

function selectionClientRect(range: Range): SelectionRect {
  const rects = rangeClientRects(range).filter((rect) => rect.width > 0 || rect.height > 0);
  const last = rects.at(-1);
  if (last) return box(last);
  const bounding = typeof range.getBoundingClientRect === "function" ? range.getBoundingClientRect() : new DOMRect();
  if (bounding.width > 0 || bounding.height > 0) return box(bounding);
  const element = range.startContainer instanceof Element
    ? range.startContainer
    : range.startContainer.parentElement;
  return box(element?.getBoundingClientRect() ?? new DOMRect());
}

function rangeClientRects(range: Range): DOMRect[] {
  if (typeof range.getClientRects !== "function") return [];
  try {
    return Array.from(range.getClientRects());
  } catch {
    return [];
  }
}

function box(rect: DOMRect): SelectionRect {
  return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}
