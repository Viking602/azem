import { describe, expect, it } from "vitest";
import {
  CODEX_BURST_COUNT,
  effortIntensity,
  fillWidthStyle,
  indexFromClientX,
  isHighCostReasoning,
  ratioFromClientX,
  reasoningLevelIndex,
  stopStyle,
  THUMB_INSET_PX,
} from "./ReasoningEffortSlider";

describe("ReasoningEffortSlider helpers", () => {
  it("maps ladder values to indices and falls back safely", () => {
    const levels = ["low", "medium", "high", "xhigh"];
    expect(reasoningLevelIndex(levels, "high")).toBe(2);
    expect(reasoningLevelIndex(levels, "missing")).toBe(0);
    expect(reasoningLevelIndex([], "low")).toBe(0);
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
    expect(stopStyle(0).left).toContain(`${THUMB_INSET_PX}px`);
    expect(stopStyle(1).left).toContain(`100% - ${THUMB_INSET_PX * 2}px`);
    expect(fillWidthStyle(0.5).width).toContain(`100% - ${THUMB_INSET_PX * 2}px`);
  });

  it("uses zero fill width at the minimum stop (no blue crescent)", () => {
    expect(fillWidthStyle(0)).toEqual({ width: "0px" });
    expect(fillWidthStyle(-1)).toEqual({ width: "0px" });
  });

  it("matches Codex max-burst particle count", () => {
    expect(CODEX_BURST_COUNT).toBe(16);
  });
});
