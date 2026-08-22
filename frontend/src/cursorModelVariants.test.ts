import { describe, expect, it } from "vitest";
import {
  cursorTiersForMode,
  cursorVariantForSelection,
  cursorVariantLabel,
  findCursorModelGroup,
  groupCursorModelVariants,
  preferredCursorVariant,
} from "./cursorModelVariants";

describe("Cursor model variants", () => {
  it("groups reasoning and Fast IDs without losing their raw model identity", () => {
    const models = [
      { id: "gpt-5.2-low", name: "GPT-5.2 Low" },
      { id: "gpt-5.2-low-fast", name: "GPT-5.2 Low Fast" },
      { id: "gpt-5.2-high", name: "GPT-5.2 High" },
      { id: "gpt-5.2-high-fast", name: "GPT-5.2 High Fast" },
      { id: "gpt-5.2-xhigh", name: "GPT-5.2 Extra High" },
      { id: "gpt-5.2-xhigh-fast", name: "GPT-5.2 Extra High Fast" },
      { id: "composer-2", name: "Composer 2" },
    ];

    const groups = groupCursorModelVariants(models);
    expect(groups).toHaveLength(2);
    expect(groups[0]).toMatchObject({ id: "gpt-5.2", name: "GPT-5.2" });
    expect(groups[0]?.variants.map((variant) => variant.model.id)).toEqual([
      "gpt-5.2-low", "gpt-5.2-low-fast", "gpt-5.2-high", "gpt-5.2-high-fast", "gpt-5.2-xhigh", "gpt-5.2-xhigh-fast",
    ]);
    expect(groups[0]?.variants.map((variant) => cursorVariantLabel(variant, "zh-CN"))).toEqual([
      "低", "低 · Fast", "高", "高 · Fast", "极高", "极高 · Fast",
    ]);
    expect(groups[0]?.variants.at(-1)).toMatchObject({ sourceIndex: 5, tier: "xhigh", fast: true, thinking: false });
    expect(groups[1]).toMatchObject({ id: "composer-2", name: "Composer 2" });
  });

  it("keeps Thinking as a separate dimension and preserves shared account qualifiers", () => {
    const groups = groupCursorModelVariants([
      { id: "claude-fable-5-high", name: "Claude Fable 5 1M (NO ZDR)" },
      { id: "claude-fable-5-thinking-high", name: "Claude Fable 5 1M Thinking (NO ZDR)" },
      { id: "claude-fable-5-thinking-high-fast", name: "Claude Fable 5 1M Thinking Fast (NO ZDR)" },
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ id: "claude-fable-5", name: "Claude Fable 5 1M" });
    expect(groups[0]?.variants.every((variant) => variant.noZDR)).toBe(true);
    expect(groups[0]?.variants.map((variant) => cursorVariantLabel(variant, "en"))).toEqual([
      "High", "High · Thinking", "High · Thinking · Fast",
    ]);
  });

  it("does not treat product-family words such as Mini or Flash as reasoning tiers", () => {
    const groups = groupCursorModelVariants([
      { id: "gpt-5-mini", name: "GPT-5 Mini" },
      { id: "gpt-5-low", name: "GPT-5 Low" },
      { id: "gemini-3-flash", name: "Gemini 3 Flash" },
    ]);

    expect(groups.map((group) => group.id)).toEqual(["gpt-5-mini", "gpt-5", "gemini-3-flash"]);
  });

  it("projects one picker row per family and resolves depth, Thinking, and Fast independently", () => {
    const models = [
      { id: "gpt-5.6-sol-low", name: "GPT-5.6 Sol 1M Low" },
      { id: "gpt-5.6-sol-medium", name: "GPT-5.6 Sol 1M" },
      { id: "gpt-5.6-sol-xhigh", name: "GPT-5.6 Sol 1M Extra High" },
      { id: "gpt-5.6-sol-low-thinking", name: "GPT-5.6 Sol 1M Low Thinking" },
      { id: "gpt-5.6-sol-xhigh-thinking", name: "GPT-5.6 Sol 1M Extra High Thinking" },
      { id: "gpt-5.6-sol-low-fast", name: "GPT-5.6 Sol Low Fast" },
      { id: "gpt-5.6-sol-xhigh-fast", name: "GPT-5.6 Sol Extra High Fast" },
      { id: "gpt-5.6-sol-low-thinking-fast", name: "GPT-5.6 Sol Low Thinking Fast" },
    ];
    const groups = groupCursorModelVariants(models);

    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ id: "gpt-5.6-sol", name: "GPT-5.6 Sol 1M" });
    const family = findCursorModelGroup(groups, "gpt-5.6-sol-medium")!;
    expect(cursorTiersForMode(family, false, false)).toEqual(["low", "medium", "xhigh"]);
    expect(cursorVariantForSelection(family, "xhigh", true, false)?.model.id).toBe("gpt-5.6-sol-xhigh-thinking");
    expect(cursorVariantForSelection(family, "low", true, true)?.model.id).toBe("gpt-5.6-sol-low-thinking-fast");
    expect(cursorVariantForSelection(family, "medium", true, false)).toBeUndefined();
    expect(preferredCursorVariant(family, "low").model.id).toBe("gpt-5.6-sol-low-thinking");
    expect(preferredCursorVariant(family, "low", false, false).model.id).toBe("gpt-5.6-sol-low");
  });

  it("keeps one NO ZDR family while retaining every exact mode combination", () => {
    const groups = groupCursorModelVariants([
      { id: "claude-fable-5-high", name: "Claude Fable 5 1M (NO ZDR)" },
      { id: "claude-fable-5-thinking-high", name: "Claude Fable 5 1M Thinking (NO ZDR)" },
      { id: "claude-fable-5-thinking-high-fast", name: "Claude Fable 5 1M Thinking Fast (NO ZDR)" },
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ name: "Claude Fable 5 1M" });
    expect(groups[0]?.variants.every((variant) => variant.noZDR)).toBe(true);
    expect(groups[0]?.variants).toHaveLength(3);
  });
});
