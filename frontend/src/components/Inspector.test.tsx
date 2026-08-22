import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { attachmentDataURL, execute, openExternalURL } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot } from "../types";
import Inspector from "./Inspector";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({
  execute: vi.fn().mockResolvedValue(undefined),
  openExternalURL: vi.fn().mockResolvedValue(undefined),
  attachmentDataURL: vi.fn().mockResolvedValue("data:image/png;base64,abc"),
}));

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
      branches: [{ name: "main", current: true }], workspaceAdditions: 12, workspaceDeletions: 4, todo: null, recap: null, contextProfile: null,
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
      recap: {
        sessionId: "session-1", anchor: "/workspace/azem", coveredBoundary: "run-7", revision: 3,
        goal: "补齐右侧栏回顾", summary: "回顾已投影到当前会话。", openItems: "pending: 验证模型路由", updatedAt: "2026-08-12T00:00:00Z",
      },
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
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("请求输入14k");
    expect(container.querySelector(".context-composition")?.textContent).toContain("上下文构成");
    expect(container.querySelector(".recap-section")?.textContent).toContain("会话回顾r3");
    expect(container.querySelector(".recap-section")?.textContent).toContain("回顾已投影到当前会话。");
    expect(container.querySelector(".recap-section")?.textContent).toContain("验证模型路由");
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

    await act(async () => useRuntimeStore.setState({
      running: true,
      contextUsage: {
        inputTokens: 58_000, outputTokens: 0, contextLimit: 128_000, reported: false,
        cacheInputTokens: 100_000, cachedInputTokens: 88_000, cacheReported: true,
      },
    }));
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("80%");
    expect(container.querySelector(".inspector-cache-summary")?.textContent).not.toContain("等待上报");

    await act(async () => root.unmount());
    useRuntimeStore.setState({ running: false });
  });

  it("labels a new running request as pending instead of showing aggregate history", async () => {
    useRuntimeStore.setState({
      snapshot, view: "thread", currentSessionId: "session-1", blocks: [], agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
      todo: null, recap: null, contextProfile: null, running: true,
      contextUsage: {
        inputTokens: 58_000, outputTokens: 0, contextLimit: 128_000, reported: false,
        cacheInputTokens: 100_000, cachedInputTokens: 88_000, cacheReported: true,
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Inspector />));
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("缓存命中率等待上报");
    expect(container.querySelector(".inspector-cache-summary")?.textContent).not.toContain("88%");

    await act(async () => root.unmount());
    useRuntimeStore.setState({ running: false });
  });

  it("labels completed Cursor cache telemetry as unreported", async () => {
    useRuntimeStore.setState({
      snapshot: { ...snapshot, provider: "cursor", model: "composer-2" },
      view: "thread", currentSessionId: "session-1", blocks: [], agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
      todo: null, recap: null, contextProfile: null, running: false,
      contextUsage: {
        inputTokens: 0, outputTokens: 7, contextLimit: 200_000, reported: true,
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Inspector />));
    expect(container.querySelector(".inspector-cache-summary")?.textContent).toContain("缓存命中率未上报");
    expect(container.querySelector(".inspector-cache-summary")?.textContent).not.toContain("等待上报");

    await act(async () => root.unmount());
  });

  it("lists distinct image names with typed and web-search URLs, and opens them", async () => {
    useRuntimeStore.setState({
      snapshot, view: "thread", currentSessionId: "session-1", agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 0, workspaceDeletions: 0, todo: null, recap: null, contextProfile: null,
      blocks: [
        {
          id: "user-1", kind: "user", content: "看这个 https://github.com/Viking602/azem",
          attachments: [{ id: "img-1", name: "image.png", mimeType: "image/png", path: "/tmp/image.png", size: 12 }],
        },
        {
          id: "search-1", kind: "tool", title: "web_search",
          content: JSON.stringify({ results: [{ title: "Azem 文档", url: "https://example.com/azem" }] }),
        },
      ],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<Inspector />));

    const rows = Array.from(container.querySelectorAll<HTMLButtonElement>(".source-row"));
    expect(rows.map((row) => row.querySelector("strong")?.textContent)).toEqual(["图片 1", "github.com · azem", "Azem 文档"]);
    expect(container.textContent).toContain("输入链接");
    expect(container.textContent).toContain("网页搜索");

    await act(async () => rows[1]!.click());
    expect(openExternalURL).toHaveBeenCalledWith("https://github.com/Viking602/azem");

    await act(async () => rows[0]!.click());
    expect(attachmentDataURL).toHaveBeenCalled();
    expect(document.querySelector(".attachment-lightbox strong")?.textContent).toBe("图片 1");

    await act(async () => rows[2]!.click());
    expect(openExternalURL).toHaveBeenCalledWith("https://example.com/azem");

    await act(async () => root.unmount());
    container.remove();
  });

  it("renders todo phases as a heading plus task list", async () => {
    useRuntimeStore.setState({
      snapshot, view: "thread", currentSessionId: "session-1", blocks: [], agents: [], backgroundProcesses: [],
      branches: [{ name: "main", current: true }], workspaceAdditions: 0, workspaceDeletions: 0, recap: null, contextProfile: null,
      todo: {
        goal: "核验供应商并映射库存",
        revision: 1,
        phases: [
          {
            id: "phase-1", title: "核验供应商", items: [
              { id: "item-1", content: "匹配税号", status: "completed" },
              { id: "item-2", content: "核对联系人", status: "completed" },
            ],
          },
          {
            id: "phase-2", title: "映射库存", items: [
              { id: "item-3", content: "读取库存文件", status: "in_progress" },
              { id: "item-4", content: "评估缺货", status: "pending" },
            ],
          },
        ],
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Inspector />));

    const section = container.querySelector(".todo-section")!;
    expect(section.getAttribute("data-slot")).toBe("todo-list");
    expect(section.getAttribute("aria-label")).toBe("任务计划: 核验供应商并映射库存");
    expect(section.querySelector(".todo-title")?.textContent).toBe("任务计划");
    expect(section.querySelector(".todo-goal")?.textContent).toBe("核验供应商并映射库存");
    expect(section.querySelector(".todo-kicker")).toBeNull();
    expect(section.querySelector(".inspector-section-header small")?.textContent).toBe("2 / 4");
    expect(section.querySelectorAll(".aui-agent-plan")).toHaveLength(0);

    const phases = Array.from(section.querySelectorAll(".todo-phase"));
    expect(phases).toHaveLength(2);
    expect(phases[0]!.querySelector(".todo-phase-title")?.textContent).toBe("核验供应商");
    expect(phases[0]!.getAttribute("data-state")).toBe("completed");
    expect(Array.from(phases[0]!.querySelectorAll(".todo-task-label")).map((node) => node.textContent)).toEqual(["匹配税号", "核对联系人"]);

    expect(phases[1]!.querySelector(".todo-phase-title")?.textContent).toBe("映射库存");
    expect(phases[1]!.getAttribute("data-state")).toBe("in_progress");
    const tasks = Array.from(phases[1]!.querySelectorAll(".todo-task"));
    expect(tasks[0]!.getAttribute("data-status")).toBe("in_progress");
    expect(tasks[0]!.textContent).toContain("读取库存文件");
    expect(tasks[1]!.getAttribute("data-status")).toBe("pending");
    expect(tasks[1]!.textContent).toContain("评估缺货");

    await act(async () => root.unmount());
  });
});
