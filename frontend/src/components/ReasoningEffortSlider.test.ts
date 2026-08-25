import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import ReasoningEffortSlider, {
  CODEX_BURST_COUNT,
  effortIntensity,
  fillWidthStyle,
  indexFromClientX,
  isHighCostReasoning,
  ratioFromClientX,
  reasoningLevelIndex,
  reasoningVisualRatio,
  stopStyle,
  THUMB_INSET_PX,
} from "./ReasoningEffortSlider";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("ReasoningEffortSlider helpers", () => {
  it("maps ladder values to indices and falls back safely", () => {
    const levels = ["low", "medium", "high", "xhigh"];
    expect(reasoningLevelIndex(levels, "high")).toBe(2);
    expect(reasoningLevelIndex(levels, "missing")).toBe(0);
    expect(reasoningLevelIndex([], "low")).toBe(0);
  });

  it("renders a one-level ladder at full width and removes all interaction", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const onChange = vi.fn();
    await act(async () => root.render(createElement(ReasoningEffortSlider, {
      levels: ["high"], value: "high", onChange, labels: { high: "高" },
      fasterLabel: "更高效", smarterLabel: "更智能", highCostHint: "更快消耗使用额度",
    })));
    const wrapper = container.querySelector<HTMLElement>(".effort-slider")!;
    const track = container.querySelector<HTMLElement>('[role="slider"]')!;
    const thumb = container.querySelector<HTMLElement>(".effort-slider-thumb-wrap")!;
    const tick = container.querySelector<HTMLElement>(".effort-slider-tick")!;
    expect(wrapper.dataset.fixed).toBe("true");
    expect(wrapper.dataset.disabled).toBe("false");
    expect(track.tabIndex).toBe(-1);
    expect(track.getAttribute("aria-disabled")).toBe("true");
    expect(thumb.style.left).toContain("100% - 36px");
    expect(tick.style.left).toContain("100% - 36px");
    expect(thumb.style.left).not.toBe(stopStyle(0).left);
    track.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    track.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    expect(onChange).not.toHaveBeenCalled();
    await act(async () => root.unmount());
    container.remove();
  });

  it("uses the full endpoint only for a fixed one-level ladder", () => {
    expect(reasoningVisualRatio(0, 0)).toBe(0);
    expect(reasoningVisualRatio(1, 0)).toBe(1);
    expect(reasoningVisualRatio(5, 2)).toBe(0.5);
  });

  it("maps continuous pointer ratios on the inset usable range", () => {
    const track = { left: 100, width: 200 } as DOMRect;
    const inset = THUMB_INSET_PX;
    expect(ratioFromClientX(100 + inset, track, inset)).toBe(0);
    expect(ratioFromClientX(100 + 100, track, inset)).toBeCloseTo((100 - inset) / (200 - inset * 2), 5);
    expect(ratioFromClientX(100 + 200 - inset, track, inset)).toBe(1);
    expect(ratioFromClientX(50, track, inset)).toBe(0);
    expect(ratioFromClientX(400, track, inset)).toBe(1);
    expect(indexFromClientX(100 + inset, track, 5, inset)).toBe(0);
    expect(indexFromClientX(100 + 200 - inset, track, 5, inset)).toBe(4);
    expect(indexFromClientX(200, track, 1, inset)).toBe(0);
  });

  it("flags high-cost reasoning tiers used for the usage hint", () => {
    expect(isHighCostReasoning("xhigh")).toBe(true);
    expect(isHighCostReasoning("max")).toBe(true);
    expect(isHighCostReasoning("ultra")).toBe(true);
    expect(isHighCostReasoning("high")).toBe(false);
    expect(isHighCostReasoning("low")).toBe(false);
  });

  it("maps fill ratio to intensity bands for color", () => {
    expect(effortIntensity(0)).toBe("low");
    expect(effortIntensity(0.25)).toBe("low");
    expect(effortIntensity(0.4)).toBe("mid");
    expect(effortIntensity(0.74)).toBe("mid");
    expect(effortIntensity(0.75)).toBe("high");
    expect(effortIntensity(1)).toBe("high");
  });

  it("insets stop positions so end ticks stay inside the pill", () => {
    expect(THUMB_INSET_PX).toBe(18);
    expect(stopStyle(0).left).toContain(`${THUMB_INSET_PX}px`);
    expect(stopStyle(1).left).toContain(`100% - ${THUMB_INSET_PX * 2}px`);
    expect(fillWidthStyle(0.5).clipPath).toContain(`100% - ${THUMB_INSET_PX * 2}px`);
  });

  it("uses zero fill width at the minimum stop (no blue crescent)", () => {
    expect(fillWidthStyle(0)).toEqual({ clipPath: "inset(0 100% 0 0)" });
    expect(fillWidthStyle(-1)).toEqual({ clipPath: "inset(0 100% 0 0)" });
  });

  it("matches Codex max-burst particle count", () => {
    expect(CODEX_BURST_COUNT).toBe(16);
  });
});
