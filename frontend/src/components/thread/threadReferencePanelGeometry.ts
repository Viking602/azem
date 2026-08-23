// Geometry adapted from Synara's FloatingBrowserPanel (MIT).
// See frontend/THIRD_PARTY_NOTICES/synara.txt.

export interface ThreadReferencePanelRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface ThreadReferencePanelHostSize {
  width: number;
  height: number;
}

export interface ThreadReferencePanelSize {
  width: number;
  height: number;
}

export type ThreadReferenceResizeEdge = "n" | "e" | "s" | "w" | "ne" | "nw" | "se" | "sw";

export const THREAD_REFERENCE_PANEL_MARGIN_PX = 12;
export const THREAD_REFERENCE_PANEL_DEFAULT_SIZE: ThreadReferencePanelSize = { width: 320, height: 200 };
export const THREAD_REFERENCE_PANEL_MIN_SIZE: ThreadReferencePanelSize = { width: 320, height: 200 };
export const THREAD_REFERENCE_PANEL_MAX_SIZE: ThreadReferencePanelSize = { width: 480, height: 520 };
export const THREAD_REFERENCE_PANEL_DRAG_THRESHOLD_PX = 4;
export const THREAD_REFERENCE_RESIZE_EDGES: readonly ThreadReferenceResizeEdge[] = ["n", "e", "s", "w", "ne", "nw", "se", "sw"];

interface AxisConstraints {
  min: number;
  max: number;
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min;
  return Math.min(max, Math.max(min, value));
}

function resolveHostLength(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 1;
}

function resolveAxisConstraints(hostLength: number, minimum: number, maximum: number): AxisConstraints {
  const available = Math.max(1, hostLength - THREAD_REFERENCE_PANEL_MARGIN_PX * 2);
  const min = Math.min(minimum, available);
  return { min, max: Math.max(min, Math.min(maximum, available)) };
}

export function clampThreadReferencePanelRect(
  rect: ThreadReferencePanelRect,
  host: ThreadReferencePanelHostSize,
): ThreadReferencePanelRect {
  const hostWidth = resolveHostLength(host.width);
  const hostHeight = resolveHostLength(host.height);
  const widthConstraint = resolveAxisConstraints(hostWidth, THREAD_REFERENCE_PANEL_MIN_SIZE.width, THREAD_REFERENCE_PANEL_MAX_SIZE.width);
  const heightConstraint = resolveAxisConstraints(hostHeight, THREAD_REFERENCE_PANEL_MIN_SIZE.height, THREAD_REFERENCE_PANEL_MAX_SIZE.height);
  const width = clamp(rect.width, widthConstraint.min, widthConstraint.max);
  const height = clamp(rect.height, heightConstraint.min, heightConstraint.max);
  const maxLeft = Math.max(0, hostWidth - THREAD_REFERENCE_PANEL_MARGIN_PX - width);
  const maxTop = Math.max(0, hostHeight - THREAD_REFERENCE_PANEL_MARGIN_PX - height);

  return {
    left: clamp(rect.left, Math.min(THREAD_REFERENCE_PANEL_MARGIN_PX, maxLeft), maxLeft),
    top: clamp(rect.top, Math.min(THREAD_REFERENCE_PANEL_MARGIN_PX, maxTop), maxTop),
    width,
    height,
  };
}

export function initialThreadReferencePanelRect(host: ThreadReferencePanelHostSize): ThreadReferencePanelRect {
  const hostWidth = resolveHostLength(host.width);
  const hostHeight = resolveHostLength(host.height);
  const { width, height } = THREAD_REFERENCE_PANEL_DEFAULT_SIZE;
  return clampThreadReferencePanelRect({
    left: hostWidth - THREAD_REFERENCE_PANEL_MARGIN_PX - width,
    top: hostHeight - THREAD_REFERENCE_PANEL_MARGIN_PX - height,
    width,
    height,
  }, { width: hostWidth, height: hostHeight });
}

export function moveThreadReferencePanelRect(
  rect: ThreadReferencePanelRect,
  delta: { x: number; y: number },
  host: ThreadReferencePanelHostSize,
): ThreadReferencePanelRect {
  return clampThreadReferencePanelRect({
    ...rect,
    left: rect.left + delta.x,
    top: rect.top + delta.y,
  }, host);
}

export function resizeThreadReferencePanelRect(
  rect: ThreadReferencePanelRect,
  input: { edge: ThreadReferenceResizeEdge; deltaX: number; deltaY: number },
  host: ThreadReferencePanelHostSize,
): ThreadReferencePanelRect {
  const clampedRect = clampThreadReferencePanelRect(rect, host);
  const widthConstraint = resolveAxisConstraints(resolveHostLength(host.width), THREAD_REFERENCE_PANEL_MIN_SIZE.width, THREAD_REFERENCE_PANEL_MAX_SIZE.width);
  const heightConstraint = resolveAxisConstraints(resolveHostLength(host.height), THREAD_REFERENCE_PANEL_MIN_SIZE.height, THREAD_REFERENCE_PANEL_MAX_SIZE.height);
  const resizeWest = input.edge.includes("w");
  const resizeNorth = input.edge.includes("n");
  const resizeEast = input.edge.includes("e");
  const resizeSouth = input.edge.includes("s");
  const width = clamp(
    resizeWest ? clampedRect.width - input.deltaX : resizeEast ? clampedRect.width + input.deltaX : clampedRect.width,
    widthConstraint.min,
    widthConstraint.max,
  );
  const height = clamp(
    resizeNorth ? clampedRect.height - input.deltaY : resizeSouth ? clampedRect.height + input.deltaY : clampedRect.height,
    heightConstraint.min,
    heightConstraint.max,
  );

  return clampThreadReferencePanelRect({
    left: resizeWest ? clampedRect.left + clampedRect.width - width : clampedRect.left,
    top: resizeNorth ? clampedRect.top + clampedRect.height - height : clampedRect.top,
    width,
    height,
  }, host);
}

export function threadReferenceResizeCursor(edge: ThreadReferenceResizeEdge): string {
  if (edge === "n" || edge === "s") return "ns-resize";
  if (edge === "e" || edge === "w") return "ew-resize";
  if (edge === "ne" || edge === "sw") return "nesw-resize";
  return "nwse-resize";
}

export function isThreadReferencePanelDragGesture(
  delta: { x: number; y: number },
  thresholdPx = THREAD_REFERENCE_PANEL_DRAG_THRESHOLD_PX,
): boolean {
  return Math.hypot(delta.x, delta.y) >= thresholdPx;
}
