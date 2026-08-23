import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useRuntimeStore } from "../../store";
import type { Snapshot, TodoList } from "../../types";
import ThreadSupportBar, { summarizeThreadPlan } from "./ThreadSupportBar";

vi.mock("../../bridge", () => ({
  attachmentDataURL: vi.fn(async () => "data:image/png;base64,AA=="),
  openExternalURL: vi.fn(async () => undefined),
}));

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

const todo: TodoList = {
  goal: "重构线程辅助信息",
  revision: 1,
  phases: [
    { id: "phase-1", title: "实现", items: [
      { id: "done", content: "移除顶部计划条", status: "completed" },
      { id: "current", content: "添加输入框支撑栏", status: "in_progress" },
      { id: "next", content: "恢复回顾与来源", status: "pending" },
    ] },
    { id: "phase-2", title: "验证", items: [
      { id: "cancelled", content: "废弃旧布局", status: "cancelled" },
      { id: "verify", content: "运行完整检查", status: "pending" },
    ] },
  ],
};

let root: Root | null = null;
let container: HTMLDivElement | null = null;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  root = null;
  container = null;
});

describe("ThreadSupportBar", () => {
  it("derives honest progress and current work", () => {
    expect(summarizeThreadPlan(todo)).toMatchObject({
      completed: 2,
      percentage: 40,
      current: { id: "current" },
    });
    expect(summarizeThreadPlan({ ...todo, phases: [] })).toBeNull();
  });

  it("switches one upward panel between plan, recap, and sources", async () => {
    useRuntimeStore.setState({
      snapshot,
      currentSessionId: "s1",
      todo,
      recap: { sessionId: "s1", revision: 4, summary: "回顾摘要", goal: "当前目标", openItems: "未完成事项", updatedAt: "2026-08-23T00:00:00Z" },
      blocks: [
        { id: "user", kind: "user", content: "参考 https://example.com/spec", attachments: [{ id: "img", name: "image.png", mimeType: "image/png", path: "/tmp/image.png", size: 10 }] },
        { id: "search", kind: "tool", title: "web_search", content: JSON.stringify({ title: "Design source", url: "https://example.com/design" }) },
      ],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<ThreadSupportBar />));

    const plan = container.querySelector<HTMLButtonElement>('.thread-support-trigger[aria-label^="任务计划"]')!;
    const recap = container.querySelector<HTMLButtonElement>('.thread-support-trigger[aria-label^="回顾"]')!;
    const sources = container.querySelector<HTMLButtonElement>('.thread-support-trigger[aria-label^="来源"]')!;
    expect(plan.textContent).toContain("添加输入框支撑栏");
    expect(plan.textContent).not.toContain("恢复回顾与来源");
    expect(recap.textContent).toContain("r4");
    expect(sources.textContent).toContain("3");

    await act(async () => recap.click());
    expect(recap.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector(".thread-support-panel")?.textContent).toContain("回顾摘要");
    expect(container.querySelector(".thread-support-panel")?.textContent).toContain("未完成事项");

    await act(async () => sources.click());
    expect(recap.getAttribute("aria-expanded")).toBe("false");
    expect(sources.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelectorAll(".thread-support-source-row")).toHaveLength(3);

    await act(async () => plan.click());
    const panel = container.querySelector<HTMLElement>(".thread-support-panel")!;
    expect(panel.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(panel.querySelectorAll('.thread-support-phase li[data-state="in_progress"]')).toHaveLength(1);

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(container.querySelector(".thread-support-panel")).toBeNull();
    expect(document.activeElement).toBe(plan);
  });

  it("keeps recap and sources discoverable when no plan exists", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo: null, recap: null, blocks: [] });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => root?.render(<ThreadSupportBar />));
    expect(container.querySelector('.thread-support-bar')?.getAttribute("data-has-plan")).toBe("false");
    expect(container.querySelector('.thread-support-trigger[aria-label^="任务计划"]')).toBeNull();
    expect(container.querySelector('.thread-support-trigger[aria-label^="回顾"]')).not.toBeNull();
    expect(container.querySelector('.thread-support-trigger[aria-label^="来源"]')).not.toBeNull();
  });
});
