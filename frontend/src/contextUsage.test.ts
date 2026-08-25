import { describe, expect, it } from "vitest";
import { contextCategoryLabel, contextComposition } from "./contextUsage";
import type { ContextUsage } from "./store";
import type { ContextProfile } from "./types";

const usage: ContextUsage = {
  inputTokens: 58_000,
  outputTokens: 4_000,
  contextLimit: 164_000,
  reported: true,
  cacheInputTokens: 100_000,
  cachedInputTokens: 74_000,
  cacheWriteTokens: 12_000,
  cacheReported: true,
  cacheWriteReported: true,
};


describe("context composition", () => {
  it("groups the wire contributions and keeps concrete items for inspection", () => {
    const profile: ContextProfile = {
      source: "request",
      estimated: true,
      contributions: [
        { category: "core", name: "azem.core_instructions", tokens: 7_340 },
        { category: "conversation", name: "message:user:1", tokens: 3_250 },
        { category: "conversation", name: "tool_result:coding.read_file", tokens: 2_000 },
        { category: "builtin_tools", name: "coding.read_file", tokens: 2_280 },
      ],
    };
    const composition = contextComposition(usage, profile);

    expect(composition.totalTokens).toBe(18_870);
    expect(composition.estimated).toBe(true);
    expect(composition.groups.map((group) => [group.category, group.tokens, group.percentage])).toEqual([
      ["core", 7_340, 39],
      ["conversation", 5_250, 28],
      ["current_output", 4_000, 21],
      ["builtin_tools", 2_280, 12],
    ]);
    expect(composition.groups.map((group) => contextCategoryLabel(group.category, "zh-CN"))).toEqual([
      "核心指令",
      "会话消息",
      "当前输出",
      "内置工具",
    ]);
    expect(composition.groups[1]?.items.map((item) => item.name)).toEqual([
      "message:user:1",
      "tool_result:coding.read_file",
    ]);
  });
});
