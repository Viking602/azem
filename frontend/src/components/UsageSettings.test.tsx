import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import { listUsageReport } from "../bridge";
import { useRuntimeStore, type ModelOption } from "../store";
import type { ModelProvider, Snapshot, UsageReport } from "../types";
import UsageSettings, { activityHeatmap, activityLevel, cacheHitLabel, formatUsageCount, formatUsageDuration, usageModelTitle, usageShares } from "./UsageSettings";

const settingsCss = readFileSync("src/styles/settings.css", "utf8");

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({
  listUsageReport: vi.fn(() => Promise.resolve(emptyReport())),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 4, chatgptFastMode: false, sequence: 0,
};

function testProvider(id: string, displayName: string, modelsDevId?: string): ModelProvider {
  return {
    id, displayName, backend: "openai_compat", defaultBaseUrl: "", baseUrl: "", envKey: "",
    enabled: true, credentialConfigured: true, credentialSource: "stored", modelsDevId, models: [],
  };
}

function usageCatalog(): { modelsByProvider: Record<string, ModelOption[]>; modelProviders: ModelProvider[] } {
  return {
    modelsByProvider: {
      chatgpt: [{ id: "gpt-5.6", name: "GPT-5.6 Sol", aliases: ["gpt-5.6-sol"], reasoningLevels: [] }],
      deepseek: [{ id: "deepseek-v4-flash", name: "DeepSeek V4 Flash", reasoningLevels: [] }],
    },
    modelProviders: [
      testProvider("chatgpt", "ChatGPT", "openai"),
      testProvider("deepseek", "DeepSeek", "deepseek"),
    ],
  };
}

function emptyReport(): UsageReport {
  return {
    scope: "project", from: "2025-08-14", to: "2026-08-14", empty: true, requests: 0, sessions: 0, runs: 0,
    totalTokens: 0, inputTokens: 0, outputTokens: 0, reasoningTokens: 0, reportedInputTokens: 0,
    cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false,
    peakDayTokens: 0, currentStreak: 0, longestStreak: 0, days: [], kinds: [], models: [], skills: [],
  };
}

function populatedReport(): UsageReport {
  return {
    scope: "project", from: "2026-07-01", to: "2026-08-14", empty: false,
    requests: 4, sessions: 2, runs: 3, totalTokens: 125_000, inputTokens: 100_000, outputTokens: 25_000,
    reasoningTokens: 800, reportedInputTokens: 100_000, cacheReadTokens: 40_000, cacheWriteTokens: 0,
    cacheReported: true, cacheWriteReported: false, peakDay: "2026-08-14", peakDayTokens: 80_000,
    currentStreak: 2, longestStreak: 3, longestRunMs: 3_600_000,
    days: [
      { date: "2026-08-13", tokens: 45_000, requests: 2 },
      { date: "2026-08-14", tokens: 80_000, requests: 2 },
    ],
    kinds: [
      { kind: "main", tokens: 100_000, requests: 3 },
      { kind: "subagent", tokens: 25_000, requests: 1 },
    ],
    models: [
      {
        provider: "deepseek", model: "deepseek-v4-flash", tokens: 125_000,
        inputTokens: 100_000, outputTokens: 25_000, cacheReadTokens: 40_000, cacheWriteTokens: 0,
        cacheReported: true, cacheWriteReported: false, requests: 4,
      },
      {
        provider: "chatgpt", model: "gpt-5.6-sol", tokens: 12_000,
        inputTokens: 10_000, outputTokens: 2_000, cacheReadTokens: 0, cacheWriteTokens: 0,
        cacheReported: false, cacheWriteReported: false, requests: 2,
      },
      {
        provider: "custom", model: "local-experimental", tokens: 1_000,
        inputTokens: 800, outputTokens: 200, cacheReadTokens: 0, cacheWriteTokens: 0,
        cacheReported: false, cacheWriteReported: false, requests: 1,
      },
    ],
    skills: [
      { name: "think", activations: 2 },
      { name: "a-very-long-skill-name-that-must-not-wrap-the-row", activations: 12 },
    ],
  };
}

describe("UsageSettings helpers", () => {
  it("formats Chinese and English token counts", () => {
    expect(formatUsageCount(12_400, "zh-CN")).toBe("1.2万");
    expect(formatUsageCount(220_000_000, "zh-CN")).toBe("2.2亿");
    expect(formatUsageCount(1_400_000, "en")).toBe("1.4M");
    expect(formatUsageDuration(0, "zh-CN")).toBe("—");
    expect(formatUsageDuration(3_600_000, "zh-CN")).toBe("1 小时");
    expect(cacheHitLabel({ cacheReported: false, cacheReadTokens: 0, reportedInputTokens: 0 }, "zh-CN")).toBe("—");
    expect(cacheHitLabel({ cacheReported: true, cacheReadTokens: 40, reportedInputTokens: 100 }, "zh-CN")).toBe("40%");
    expect(activityLevel(0, 100)).toBe(0);
    expect(activityLevel(80, 100)).toBe(3);
  });

  it("resolves catalog aliases and falls back to the raw model id", () => {
    const catalog = usageCatalog().modelsByProvider;
    expect(usageModelTitle("deepseek", "deepseek-v4-flash", catalog, "未标记")).toBe("DeepSeek V4 Flash");
    expect(usageModelTitle("chatgpt", "gpt-5.6-sol", catalog, "未标记")).toBe("GPT-5.6 Sol");
    expect(usageModelTitle("custom", "local-experimental", catalog, "未标记")).toBe("local-experimental");
    expect(usageModelTitle("chatgpt", "", catalog, "未标记")).toBe("未标记");
  });

  it("fills a Sunday-start week grid from sparse daily facts", () => {
    const heat = activityHeatmap("2026-08-12", "2026-08-14", [
      { date: "2026-08-13", tokens: 45_000, requests: 2 },
      { date: "2026-08-14", tokens: 80_000, requests: 2 },
    ], "daily", "zh-CN");
    expect(heat.weeks).toHaveLength(1);
    expect(heat.weeks[0]).toHaveLength(7);
    const inRange = heat.weeks[0].filter((cell) => cell.inRange);
    expect(inRange.map((cell) => cell.date)).toEqual(["2026-08-12", "2026-08-13", "2026-08-14"]);
    expect(inRange.map((cell) => cell.tokens)).toEqual([0, 45_000, 80_000]);
    expect(heat.peak).toBe(80_000);
    expect(heat.months[0]?.label).toBe("8月");
  });

  it("paints weekly columns from the same day series and cumulative only on active days", () => {
    const days = [
      { date: "2026-08-13", tokens: 45_000, requests: 2 },
      { date: "2026-08-14", tokens: 80_000, requests: 2 },
    ];
    const weekly = activityHeatmap("2026-08-12", "2026-08-14", days, "weekly", "zh-CN");
    expect(weekly.weeks[0].filter((cell) => cell.inRange).every((cell) => cell.tokens === 125_000)).toBe(true);
    expect(weekly.peak).toBe(125_000);

    const cumulative = activityHeatmap("2026-08-12", "2026-08-14", days, "cumulative", "en");
    const byDate = Object.fromEntries(cumulative.weeks[0].filter((cell) => cell.inRange).map((cell) => [cell.date, cell.tokens]));
    expect(byDate["2026-08-12"]).toBe(0);
    expect(byDate["2026-08-13"]).toBe(45_000);
    expect(byDate["2026-08-14"]).toBe(125_000);
    expect(cumulative.peak).toBe(125_000);
    expect(cumulative.months[0]?.label).toBe("Aug");
  });

  it("keeps the top four model shares and folds the rest", () => {
    const shares = usageShares([
      { provider: "a", model: "one", tokens: 50, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false, requests: 1 },
      { provider: "a", model: "two", tokens: 30, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false, requests: 1 },
      { provider: "a", model: "three", tokens: 10, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false, requests: 1 },
      { provider: "a", model: "four", tokens: 6, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false, requests: 1 },
      { provider: "a", model: "five", tokens: 4, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false, requests: 1 },
    ], "其他");
    expect(shares.map((share) => share.label)).toEqual(["one", "two", "three", "four", "其他"]);
    expect(shares[0]?.percent).toBe(50);
    expect(shares[4]?.tokens).toBe(4);
  });
});

describe("UsageSettings", () => {
  afterEach(async () => {
    vi.clearAllMocks();
    (document.activeElement instanceof HTMLElement ? document.activeElement : null)?.blur();
    useRuntimeStore.setState({ usageReport: null });
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
  });

  it("renders the empty state without a fake calendar", async () => {
    vi.mocked(listUsageReport).mockResolvedValueOnce(emptyReport());
    useRuntimeStore.setState({ snapshot, usageReport: null });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<UsageSettings language="zh-CN" onError={() => undefined} />));
    await act(async () => Promise.resolve());
    expect(container.querySelector(".usage-empty")?.textContent).toContain("还没有用量");
    expect(container.querySelector(".usage-heatmap")).toBeNull();
    expect(container.querySelector(".usage-calendar")).toBeNull();
    expect(container.querySelector("[title]")).toBeNull();
    await act(async () => root.unmount());
    container.remove();
  });

  it("renders ledger, heatmap, models, and short skill rows, then refreshes", async () => {
    vi.mocked(listUsageReport).mockResolvedValueOnce(populatedReport());
    useRuntimeStore.setState({ snapshot, usageReport: null, ...usageCatalog() });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<UsageSettings language="zh-CN" onError={() => undefined} />));
    await act(async () => Promise.resolve());
    expect(container.querySelector(".usage-ledger-total")?.textContent).toBe("12.5万");
    expect(container.querySelector(".usage-report")).not.toBeNull();
    expect(container.querySelectorAll(".usage-report .settings-card")).toHaveLength(0);
    expect(container.querySelector(".usage-split")?.parentElement?.classList.contains("usage-report")).toBe(true);
    expect(container.textContent).toContain("Token 活动");
    expect(container.textContent).toContain("每日");
    expect(container.textContent).toContain("主会话");
    expect(container.textContent).not.toContain("而不是插件排行");
    expect(container.textContent).not.toContain("按请求类型分解");
    const modelRows = container.querySelectorAll(".usage-model-row");
    expect(modelRows).toHaveLength(3);
    expect(modelRows[0]?.querySelector("strong")?.textContent).toBe("DeepSeek V4 Flash");
    expect(modelRows[0]?.querySelector("small")?.textContent).toContain("DeepSeek");
    expect(modelRows[0]?.querySelector("small")?.textContent).toContain("deepseek-v4-flash");
    expect(modelRows[1]?.querySelector("strong")?.textContent).toBe("GPT-5.6 Sol");
    expect(modelRows[2]?.querySelector("strong")?.textContent).toBe("local-experimental");
    expect(container.querySelectorAll(".usage-model-row .provider-icon")).toHaveLength(3);
    expect(modelRows[0]?.querySelector(".provider-icon")?.getAttribute("src")).toContain("models.dev/logos/deepseek");
    expect(modelRows[1]?.querySelector(".provider-icon")?.getAttribute("src")).toContain("models.dev/logos/openai");
    expect(container.textContent).toContain("think");
    expect(container.textContent).toContain("12 次");
    expect(container.textContent).not.toContain("次激活");
    expect(container.textContent).not.toContain("次调用");
    const heatmap = container.querySelector(".usage-heatmap");
    expect(heatmap).not.toBeNull();
    expect(container.querySelector(".usage-activity-visual")?.contains(heatmap)).toBe(true);
    expect(heatmap?.getAttribute("data-grain")).toBe("daily");
    const weeks = container.querySelectorAll(".usage-heatmap-week");
    expect(weeks.length).toBeGreaterThan(1);
    weeks.forEach((week) => expect(week.querySelectorAll(".usage-heat-cell")).toHaveLength(7));
    expect(container.querySelector(".usage-month")).toBeNull();
    expect(container.querySelector("[title]")).toBeNull();
    expect(container.querySelector(".usage-heatmap-tip")).toBeNull();
    await act(async () => {
      container.querySelectorAll<HTMLButtonElement>(".usage-activity-grains button")[1]!.click();
    });
    expect(container.querySelector(".usage-heatmap")).toBe(heatmap);
    expect(heatmap?.getAttribute("data-grain")).toBe("weekly");
    expect(container.querySelector("[title]")).toBeNull();
    vi.mocked(listUsageReport).mockResolvedValueOnce(emptyReport());
    await act(async () => container.querySelector<HTMLButtonElement>(".usage-toolbar .small-button")!.click());
    expect(listUsageReport).toHaveBeenCalledTimes(2);
    await act(async () => root.unmount());
    container.remove();
  });

  it("opens a day breakdown from a heatmap cell and keeps the skyline donut", async () => {
    vi.mocked(listUsageReport).mockResolvedValueOnce(populatedReport());
    useRuntimeStore.setState({ snapshot, usageReport: null, ...usageCatalog() });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<UsageSettings language="zh-CN" onError={() => undefined} />));
    await act(async () => Promise.resolve());
    expect(container.querySelector(".usage-donut")?.getAttribute("aria-label")).toBe("用量天际线");
    expect(container.querySelector(".usage-share")).not.toBeNull();
    expect(container.querySelector("[data-testid='usage-day-panel']")).toBeNull();
    await act(async () => {
      container.querySelector<HTMLButtonElement>(".usage-heat-cell[data-date='2026-08-14']")!.click();
    });
    const panel = container.querySelector("[data-testid='usage-day-panel']");
    expect(panel?.textContent).toContain("2026-08-14");
    expect(panel?.textContent).toContain("8万");
    expect(panel?.textContent).toContain("占总用量 64.0%");
    expect(container.querySelector(".usage-heat-cell[data-date='2026-08-14']")?.hasAttribute("data-selected")).toBe(true);
    await act(async () => {
      container.querySelector<HTMLButtonElement>("[data-testid='usage-day-panel'] .small-button")!.click();
    });
    expect(container.querySelector("[data-testid='usage-day-panel']")).toBeNull();
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps Codex-sized heat cells and shows a custom usage tip on hover and focus", async () => {
    expect(settingsCss).toMatch(/--heat-size:\s*10px/);
    expect(settingsCss).toMatch(/--heat-gap:\s*2px/);
    expect(settingsCss).toMatch(/\.usage-heat-cell[\s\S]*?border-radius:\s*2px/);
    expect(settingsCss).toMatch(/\.usage-activity-visual\s*\{[^}]*width:\s*100%[^}]*\}\s*\/\*\s*活动区铺满报告宽度\s*\*\//);
    expect(settingsCss).not.toMatch(/活动网格居中/);
    expect(settingsCss).not.toMatch(/\.usage-heatmap-grid[^{]*\{[^}]*minmax\(0,\s*1fr\)/);
    expect(settingsCss).not.toMatch(/#2ea44f|#3fb950|#216e39|github.*green/i);
    expect(settingsCss).toMatch(/\.usage-skyline\s*\{[^}]*grid-template-columns:\s*auto minmax\(0,\s*1fr\)/);
    expect(settingsCss).toMatch(/\.usage-donut\s*\{/);
    expect(settingsCss).toMatch(/usage-day-in/);
    expect(settingsCss).toMatch(/usage-share-in/);

    expect(settingsCss).toMatch(/\.usage-split\s*\{[^}]*align-items:\s*stretch/);
    expect(settingsCss).toMatch(/\.usage-split\s*>\s*\.usage-breakdown\s*\{[^}]*height:\s*100%/);
    expect(settingsCss).toMatch(/\.usage-heat-cell\s*\{[^}]*transition:[^;]*background-color 220ms/);
    expect(settingsCss).toMatch(/@media \(prefers-reduced-motion: reduce\) \{[\s\S]*?\.usage-heat-cell[\s\S]*?transition:\s*none/);
    expect(settingsCss).toMatch(/@media \(max-width: 920px\)[\s\S]*?\.usage-split\s*>\s*\.usage-breakdown\s*\{[^}]*height:\s*auto/);
    expect(settingsCss).toMatch(/\.usage-model-identity[\s\S]*?white-space:\s*nowrap/);
    expect(settingsCss).toMatch(/\.usage-breakdown li strong[\s\S]*?white-space:\s*nowrap/);

    vi.mocked(listUsageReport).mockResolvedValueOnce(populatedReport());
    useRuntimeStore.setState({ snapshot, usageReport: null, ...usageCatalog() });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<UsageSettings language="zh-CN" onError={() => undefined} />));
    await act(async () => Promise.resolve());

    const peak = container.querySelector<HTMLButtonElement>('.usage-heat-cell[data-date="2026-08-14"]');
    const empty = container.querySelector<HTMLButtonElement>('.usage-heat-cell[data-date="2026-08-12"]');
    expect(peak).not.toBeNull();
    expect(empty).not.toBeNull();
    expect(peak?.getAttribute("title")).toBeNull();
    expect(empty?.getAttribute("title")).toBeNull();

    await act(async () => {
      peak!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, cancelable: true }));
    });
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-14 · 8万");
    expect(container.querySelector("[title]")).toBeNull();

    await act(async () => {
      empty!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, cancelable: true }));
    });
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-12 · 无记录");

    await act(async () => {
      container.querySelector(".usage-heatmap-grid")!.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, cancelable: true, relatedTarget: document.body }));
    });
    expect(container.querySelector(".usage-heatmap-tip")).toBeNull();

    await act(async () => peak!.focus());
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-14 · 8万");
    expect(container.querySelector("[title]")).toBeNull();

    await act(async () => peak!.blur());
    await act(async () => {
      container.querySelectorAll<HTMLButtonElement>(".usage-activity-grains button")[1]!.click();
    });
    const weeklyPeak = container.querySelector<HTMLButtonElement>('.usage-heat-cell[data-date="2026-08-14"]')!;
    await act(async () => weeklyPeak.focus());
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-09 – 2026-08-14 · 12.5万");

    await act(async () => weeklyPeak.blur());
    await act(async () => {
      container.querySelectorAll<HTMLButtonElement>(".usage-activity-grains button")[2]!.click();
    });
    const cumulativePeak = container.querySelector<HTMLButtonElement>('.usage-heat-cell[data-date="2026-08-14"]')!;
    const cumulativeEmpty = container.querySelector<HTMLButtonElement>('.usage-heat-cell[data-date="2026-08-12"]')!;
    await act(async () => cumulativePeak.focus());
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-14 · 12.5万");
    await act(async () => cumulativeEmpty.focus());
    expect(container.querySelector(".usage-heatmap-tip")?.textContent).toBe("2026-08-12 · 无记录");
    expect(container.querySelector("[title]")).toBeNull();
    expect(container.querySelector(".usage-report .usage-split .usage-breakdown")).not.toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps English copy on the usage page", async () => {
    vi.mocked(listUsageReport).mockResolvedValueOnce(emptyReport());
    useRuntimeStore.setState({ snapshot: { ...snapshot, language: "en" }, usageReport: null });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<UsageSettings language="en" onError={() => undefined} />));
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("No usage yet");
    expect(container.textContent).toContain("This project");
    await act(async () => root.unmount());
    container.remove();
  });
});
