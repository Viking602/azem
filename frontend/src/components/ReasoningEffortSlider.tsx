import { useCallback, useEffect, useId, useRef, useState } from "react";

/** Continuous 0–1 ratio from a pointer X on the track (inset usable range). */
export function ratioFromClientX(clientX: number, track: DOMRect, insetPx: number): number {
  const usable = Math.max(track.width - insetPx * 2, 1);
  const x = clientX - track.left - insetPx;
  return Math.max(0, Math.min(1, x / usable));
}

/** Map a pointer X into a discrete level index. */
export function indexFromClientX(clientX: number, track: DOMRect, count: number, insetPx: number): number {
  if (count <= 1) return 0;
  return Math.round(ratioFromClientX(clientX, track, insetPx) * (count - 1));
}

/** Resolve the selected level to a ladder index (falls back to 0). */
export function reasoningLevelIndex(levels: string[], value: string): number {
  const index = levels.indexOf(value);
  return index >= 0 ? index : 0;
}

/** High-cost tiers that warrant the usage-limit hint (Codex “极高+”). */
export function isHighCostReasoning(level: string): boolean {
  return level === "xhigh" || level === "max" || level === "ultra";
}

export type EffortIntensity = "low" | "mid" | "high";

export function effortIntensity(ratio: number): EffortIntensity {
  if (ratio >= 0.75) return "high";
  if (ratio >= 0.4) return "mid";
  return "low";
}

/** Half of the 28px Codex thumb — usable stop range is inset so dots stay inside the pill. */
export const THUMB_INSET_PX = 14;

/** Codex max-burst particle count (see model-picker-power-slider Burst). */
export const CODEX_BURST_COUNT = 16;

/**
 * Position along the track for stop `ratio` (0–1).
 * Inset by half-thumb so first/last stops keep ticks inside the rounded rail.
 */
export function stopStyle(ratio: number): { left: string } {
  return {
    left: `calc(${THUMB_INSET_PX}px + (100% - ${THUMB_INSET_PX * 2}px) * ${ratio})`,
  };
}

/**
 * Fill ends at the thumb center. At ratio 0 width is 0 so no blue
 * crescent peeks past the left of the thumb (Codex min state).
 */
export function fillWidthStyle(ratio: number): { width: string } {
  if (ratio <= 0) return { width: "0px" };
  return {
    width: `calc(${THUMB_INSET_PX}px + (100% - ${THUMB_INSET_PX * 2}px) * ${ratio})`,
  };
}

type Props = {
  levels: string[];
  value: string;
  onChange: (level: string) => void;
  labels: Record<string, string>;
  fasterLabel: string;
  smarterLabel: string;
  highCostHint?: string;
  fast?: boolean;
  disabled?: boolean;
  ariaLabel?: string;
};

/**
 * Codex-style discrete effort slider.
 *
 * Burst matches ChatGPT.app model-picker-power-slider:
 * - Mounted on the thumb (`MaxBurst` > `Burst` with 16 empty spans)
 * - Offsets via CSS nth-child + @property --particle-x/y
 * - Remount via maxBurstKey++ on enteredMax (visual, not only committed)
 */
export default function ReasoningEffortSlider({
  levels,
  value,
  onChange,
  labels,
  fasterLabel,
  smarterLabel,
  highCostHint,
  fast = false,
  disabled = false,
  ariaLabel = "Reasoning effort",
}: Props) {
  const trackRef = useRef<HTMLDivElement>(null);
  const draggingRef = useRef(false);
  const rafRef = useRef(0);
  const pendingRatio = useRef<number | null>(null);
  const labelId = useId();
  const sorted = levels.length ? levels : [value].filter(Boolean);
  const lastIndex = Math.max(0, sorted.length - 1);
  const committed = reasoningLevelIndex(sorted, value);
  const committedRatio = lastIndex === 0 ? 0 : committed / lastIndex;

  const [dragging, setDragging] = useState(false);
  const [dragRatio, setDragRatio] = useState<number | null>(null);
  // Codex: maxBurstKey increments on enteredMax; Burst remounts with key={maxBurstKey}.
  const [maxBurstKey, setMaxBurstKey] = useState(0);
  // Seed as "already at max" when committed is max so reopening 超高 does not explode.
  const wasAtMax = useRef(sorted.length > 1 && committed >= lastIndex);

  const visualRatio = dragRatio ?? committedRatio;
  const visualIndex = lastIndex === 0 ? 0 : Math.round(visualRatio * lastIndex);
  const current = sorted[visualIndex] ?? value;
  const currentLabel = labels[current] ?? current;
  const intensity = effortIntensity(visualRatio);
  const high = isHighCostReasoning(current) || intensity === "high";
  const showHint = Boolean(high && highCostHint);
  const atMax = sorted.length > 1 && visualIndex >= lastIndex;
  // Only while dragging: mid/low → 更高效/更智能; high/ultra → 更快消耗使用额度.
  // Idle → no overlay (高级 / ⚡ only).
  const labelsMode = !dragging ? "none" : showHint ? "hint" : "ends";

  // Codex enteredMax only: fire when crossing into max, never on mount-at-max.
  useEffect(() => {
    if (atMax && !wasAtMax.current) {
      setMaxBurstKey((key) => key + 1);
    }
    wasAtMax.current = atMax;
  }, [atMax]);

  useEffect(() => () => {
    if (rafRef.current) cancelAnimationFrame(rafRef.current);
  }, []);

  const setVisualRatio = useCallback((ratio: number) => {
    pendingRatio.current = ratio;
    if (rafRef.current) return;
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = 0;
      if (pendingRatio.current === null) return;
      setDragRatio(pendingRatio.current);
      pendingRatio.current = null;
    });
  }, []);

  const commitIndex = useCallback((next: number) => {
    const level = sorted[next];
    if (!level || level === value) return;
    onChange(level);
  }, [onChange, sorted, value]);

  const ratioFromEvent = (clientX: number) => {
    const track = trackRef.current?.getBoundingClientRect();
    if (!track) return null;
    return ratioFromClientX(clientX, track, THUMB_INSET_PX);
  };

  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (disabled || sorted.length <= 1) return;
    event.preventDefault();
    draggingRef.current = true;
    setDragging(true);
    event.currentTarget.setPointerCapture(event.pointerId);
    const ratio = ratioFromEvent(event.clientX);
    if (ratio === null) return;
    setDragRatio(ratio);
  };

  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current || disabled) return;
    const ratio = ratioFromEvent(event.clientX);
    if (ratio === null) return;
    setVisualRatio(ratio);
  };

  const endDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current) return;
    draggingRef.current = false;
    setDragging(false);
    try { event.currentTarget.releasePointerCapture(event.pointerId); } catch { /* already released */ }
    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = 0;
    }
    const ratio = ratioFromEvent(event.clientX) ?? pendingRatio.current ?? dragRatio ?? committedRatio;
    pendingRatio.current = null;
    const next = lastIndex === 0 ? 0 : Math.round(ratio * lastIndex);
    setDragRatio(null);
    commitIndex(next);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (disabled || sorted.length <= 1) return;
    let next = committed;
    if (event.key === "ArrowLeft" || event.key === "ArrowDown") next = Math.max(0, committed - 1);
    else if (event.key === "ArrowRight" || event.key === "ArrowUp") next = Math.min(lastIndex, committed + 1);
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = lastIndex;
    else return;
    event.preventDefault();
    setDragRatio(null);
    commitIndex(next);
  };

  if (!sorted.length) return null;

  const thumbPos = stopStyle(visualRatio);
  const fillStyle = fillWidthStyle(visualRatio);
  // Codex: MaxBurst only while at max and after at least one enteredMax.
  const showBurst = atMax && maxBurstKey > 0;

  return (
    <div
      className="effort-slider"
      data-disabled={String(disabled)}
      data-high={String(high)}
      data-intensity={intensity}
      data-dragging={String(dragging)}
      data-max={String(atMax)}
      data-labels={labelsMode}
    >
      {/*
        Overlays (same band as 高级 / ⚡):
        - drag mid/low → 更高效 / 更智能
        - drag high/ultra → 更快消耗使用额度
        - idle → none (高级 + ⚡ visible)
      */}
      <div className="effort-slider-legend" id={labelId} data-mode={labelsMode} aria-hidden={labelsMode === "none"}>
        {labelsMode === "hint" ? (
          <span className="effort-slider-hint">{highCostHint}</span>
        ) : labelsMode === "ends" ? (
          <>
            <span className="effort-slider-end">{fasterLabel}</span>
            <span className="effort-slider-gap" />
            <span className="effort-slider-end">{smarterLabel}</span>
          </>
        ) : (
          <span className="sr-only">{currentLabel}</span>
        )}
      </div>
      <div
        ref={trackRef}
        className="effort-slider-track"
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label={ariaLabel}
        aria-labelledby={labelId}
        aria-valuemin={0}
        aria-valuemax={lastIndex}
        aria-valuenow={visualIndex}
        aria-valuetext={currentLabel}
        aria-disabled={disabled}
        data-dragging={String(dragging)}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onKeyDown={onKeyDown}
      >
        {/* Rail clips the fill so the blue pill never paints outside the gray track. */}
        <div className="effort-slider-rail" aria-hidden="true">
          <div
            className="effort-slider-fill"
            data-intensity={intensity}
            data-end={String(atMax)}
            style={fillStyle}
          />
        </div>
        <div className="effort-slider-ticks" aria-hidden="true">
          {sorted.map((level, i) => (
            <span
              key={level}
              className={`effort-slider-tick ${i <= visualIndex ? "active" : ""}`}
              style={stopStyle(lastIndex === 0 ? 0 : i / lastIndex)}
            />
          ))}
        </div>
        <div
          className="effort-slider-thumb-wrap"
          style={thumbPos}
          data-high={String(high)}
          data-end={String(atMax)}
        >
          {/* Codex MaxBurst: absolute inset 0 on the thumb; Burst remounts via key. */}
          {showBurst ? (
            <span className="effort-slider-max-burst" aria-hidden="true">
              <span className="effort-slider-burst" key={maxBurstKey} data-fast={String(fast)}>
                {Array.from({ length: CODEX_BURST_COUNT }, (_, i) => (
                  <span key={i} />
                ))}
              </span>
            </span>
          ) : null}
          <div className="effort-slider-thumb" data-high={String(high)} data-end={String(atMax)} />
        </div>
      </div>
    </div>
  );
}
