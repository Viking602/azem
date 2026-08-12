import { describe, expect, it } from "vitest";
import { contextCacheMetrics, contextComposition } from "./contextUsage";
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

describe("context cache metrics", () => {
  it("derives the hit rate only from cache-reporting input", () => {
    expect(contextCacheMetrics(usage)).toEqual({
      reported: true,
      hitRate: 74,
      cachedTokens: 74_000,
      totalCacheTokens: 100_000,
    });
  });

  it("keeps unsupported provider data distinct from a real zero-percent hit rate", () => {
    expect(contextCacheMetrics({ inputTokens: 12, outputTokens: 2, contextLimit: 128_000, reported: true }))
      .toMatchObject({ reported: false, hitRate: null });
    expect(contextCacheMetrics({
      inputTokens: 12, outputTokens: 2, contextLimit: 128_000, reported: true,
      cacheInputTokens: 12, cachedInputTokens: 0, cacheReported: true,
    })).toMatchObject({ reported: true, hitRate: 0 });
  });

  it("prefers the latest main request over cold-start and auxiliary request totals", () => {
    const current = {
      ...usage,
      inputTokens: 32_456,
      uncachedInputTokens: 5_320,
      mainCacheReported: true,
      cacheInputTokens: 171_338,
      cachedInputTokens: 103_936,
    } as ContextUsage;

    expect(contextCacheMetrics(current)).toEqual({
      reported: true,
      hitRate: 83.6,
      cachedTokens: 27_136,
      totalCacheTokens: 32_456,
    });
  });

  it("shows two decimal places without rounding the cache hit rate to an integer", () => {
    expect(contextCacheMetrics({
      inputTokens: 55_817,
      uncachedInputTokens: 265,
      outputTokens: 10,
      contextLimit: 1_000_000,
      reported: true,
      mainCacheReported: true,
    })).toEqual({
      reported: true,
      hitRate: 99.52,
      cachedTokens: 55_552,
      totalCacheTokens: 55_817,
    });
  });
});

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
    expect(composition.groups[1]?.items.map((item) => item.name)).toEqual([
      "message:user:1",
      "tool_result:coding.read_file",
    ]);
  });
});
