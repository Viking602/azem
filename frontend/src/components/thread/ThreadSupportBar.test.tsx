import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useRuntimeStore } from "../../store";
import type { Snapshot, TodoList } from "../../types";
import { ThreadPlanControl, ThreadReferenceCard, summarizeThreadPlan } from "./ThreadSupportBar";

vi.mock("../../bridge", () => ({
  attachmentDataURL: vi.fn(async () => "data:image/png;base64,AA=="),
  openExternalURL: vi.fn(async () => undefined),
}));

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let resizeObserverCallback: ResizeObserverCallback | null = null;
let testResizeObserver: TestResizeObserver | null = null;

class TestResizeObserver {
  constructor(callback: ResizeObserverCallback) {
    resizeObserverCallback = callback;
    testResizeObserver = this;
  }

  observe() {}
  unobserve() {}
  disconnect() {}
}

beforeEach(() => {
  resizeObserverCallback = null;
  testResizeObserver = null;
  vi.stubGlobal("ResizeObserver", TestResizeObserver);
});

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
  document.querySelectorAll(".thread-reference-pointer-overlay").forEach((node) => node.remove());
  root = null;
  container = null;
  vi.unstubAllGlobals();
});

describe("thread composer support controls", () => {
  it("derives honest plan progress", () => {
    expect(summarizeThreadPlan(todo)).toMatchObject({ completed: 2, percentage: 40 });
    expect(summarizeThreadPlan({ ...todo, phases: [] })).toBeNull();
  });

  it("keeps the left plan control to icon, label, and count", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<ThreadPlanControl />));

    const trigger = container.querySelector<HTMLButtonElement>(".thread-plan-control-trigger")!;
    expect(trigger.querySelector("svg")).not.toBeNull();
    expect(trigger.textContent).toBe("计划2 / 5");
    expect(trigger.textContent).not.toContain("添加输入框支撑栏");

    await act(async () => trigger.click());
    const panel = container.querySelector<HTMLElement>(".thread-plan-panel")!;
    expect(panel.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(panel.querySelectorAll('.thread-support-phase li[data-state="in_progress"]')).toHaveLength(1);

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(container.querySelector(".thread-plan-panel")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("keeps recap and sources in one bounded floating panel", async () => {
    useRuntimeStore.setState({
      snapshot,
      currentSessionId: "s1",
      recap: { sessionId: "s1", revision: 4, summary: "回顾摘要", goal: "当前目标", openItems: "未完成事项", updatedAt: "2026-08-23T00:00:00Z" },
      blocks: [
        { id: "user", kind: "user", content: "参考 https://example.com/spec", attachments: [{ id: "img", name: "image.png", mimeType: "image/png", path: "/tmp/image.png", size: 10 }] },
        { id: "search", kind: "tool", title: "web_search", content: JSON.stringify({ title: "Design source", url: "https://example.com/design" }) },
      ],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<ThreadReferenceCard />));

    const host = container.querySelector<HTMLElement>(".thread-reference-host")!;
    const card = host.querySelector<HTMLElement>(".thread-reference-card")!;
    Object.defineProperties(host, {
      clientWidth: { configurable: true, value: 900 },
      clientHeight: { configurable: true, value: 600 },
    });
    await act(async () => resizeObserverCallback?.([], testResizeObserver as unknown as ResizeObserver));

    const tabs = card.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    const drag = card.querySelector<HTMLButtonElement>(".thread-reference-drag")!;
    expect(host.dataset.floatingReferenceHost).toBe("true");
    expect(card.dataset.floatingReferencePanel).toBe("true");
    expect(card.style.left).toBe("568px");
    expect(card.style.top).toBe("388px");
    expect(card.style.width).toBe("320px");
    expect(card.style.height).toBe("200px");
    expect(card.querySelectorAll("[data-thread-reference-resize-edge]")).toHaveLength(8);
    expect(drag.getAttribute("aria-keyshortcuts")).toContain("Alt+ArrowRight");
    expect(tabs).toHaveLength(2);
    expect(tabs[0]?.getAttribute("aria-selected")).toBe("true");
    expect(card.textContent).toContain("回顾摘要");
    expect(tabs[1]?.textContent).toContain("3");

    await act(async () => drag.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true })));
    expect(card.style.left).toBe("558px");
    await act(async () => drag.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", altKey: true, bubbles: true })));
    expect(card.style.width).toBe("330px");
    await act(async () => drag.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    expect(card.style.left).toBe("568px");

    await act(async () => tabs[1]?.click());
    expect(tabs[1]?.getAttribute("aria-selected")).toBe("true");
    expect(card.querySelectorAll(".thread-support-source-row")).toHaveLength(3);
  });

  it("keeps the reference card visible when recap and sources are empty", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", recap: null, blocks: [] });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => root?.render(<ThreadReferenceCard />));
    expect(container.querySelector(".thread-reference-card")).not.toBeNull();
    expect(container.querySelector('[role="tab"]')?.textContent).toContain("—");
    expect(container.querySelector(".thread-support-empty")?.textContent).toContain("会话回顾");
  });
});
