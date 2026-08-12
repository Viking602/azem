import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot } from "../types";
import Inspector from "./Inspector";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({ execute: vi.fn().mockResolvedValue(undefined) }));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

afterEach(() => vi.clearAllMocks());

describe("Inspector", () => {
  it("opens workspace change review from the environment summary", async () => {
    useRuntimeStore.setState({
      snapshot, view: "thread", currentSessionId: "session-1", blocks: [], agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 12, workspaceDeletions: 4, todo: null, contextProfile: null,
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Inspector />));
    const review = container.querySelector<HTMLButtonElement>('[aria-label="审查变更"]')!;
    expect(container.querySelector(".inspector-workspace-facts")?.textContent).toContain("+12");
    await act(async () => review.click());

    expect(useRuntimeStore.getState().view).toBe("changes");
    expect(execute).toHaveBeenCalledWith({ kind: "list_background", sessionId: "session-1" });
    await act(async () => root.unmount());
  });

  it("shows provider cache efficiency and an expandable context composition", async () => {
    useRuntimeStore.setState({
      snapshot, view: "thread", currentSessionId: "session-1", blocks: [], agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
      todo: null,
      contextUsage: {
        inputTokens: 14_000, outputTokens: 1_000, contextLimit: 128_000, reported: true,
        cacheInputTokens: 20_000, cachedInputTokens: 15_000, cacheWriteTokens: 2_000,
        uncachedInputTokens: 2_800, cacheReported: true, mainCacheReported: true, cacheWriteReported: true,
      },
      contextProfile: {
        source: "request", estimated: true, contributions: [
          { category: "core", name: "azem.core_instructions", tokens: 7_000 },
          { category: "conversation", name: "message:user:1", tokens: 4_000 },
          { category: "builtin_tools", name: "coding.read_file", tokens: 3_000 },
        ],
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Inspector />));
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("缓存命中率80%");
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("命中缓存11k");
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("总缓存14k");
    expect(container.querySelector(".context-composition")?.textContent).toContain("上下文构成");
    const toggle = container.querySelector<HTMLButtonElement>(".context-composition-bar")!;
    const groups = container.querySelector<HTMLDivElement>(".context-composition-groups")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(groups.hidden).toBe(true);

    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(groups.hidden).toBe(false);
    expect(groups.textContent).toContain("核心指令");
    expect(groups.textContent).toContain("会话消息");

    const core = container.querySelector<HTMLDetailsElement>('details[data-category="core"]')!;
    core.open = true;
    expect(core.textContent).toContain("Azem 核心指令");

    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(groups.hidden).toBe(true);
    await act(async () => root.unmount());
  });
});
