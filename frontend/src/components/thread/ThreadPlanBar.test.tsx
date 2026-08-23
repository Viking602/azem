import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { useRuntimeStore } from "../../store";
import type { Snapshot, TodoList } from "../../types";
import ThreadPlanBar, { summarizeThreadPlan } from "./ThreadPlanBar";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

const todo: TodoList = {
  goal: "移除右侧上下文并迁移计划",
  revision: 1,
  phases: [
    { id: "phase-1", title: "实现", items: [
      { id: "done", content: "移除右侧上下文", status: "completed" },
      { id: "current", content: "迁移任务计划", status: "in_progress" },
      { id: "next", content: "更新交互", status: "pending" },
    ] },
    { id: "phase-2", title: "验证", items: [
      { id: "cancelled", content: "废弃旧截图", status: "cancelled" },
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

describe("ThreadPlanBar", () => {
  it("derives honest progress, current work, and next work", () => {
    expect(summarizeThreadPlan(todo)).toMatchObject({
      completed: 2,
      percentage: 40,
      current: { id: "current" },
      next: { id: "next" },
    });
    expect(summarizeThreadPlan({ ...todo, phases: [] })).toBeNull();
  });

  it("keeps a one-line summary and expands the durable plan without reflow state", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<ThreadPlanBar />));
    const trigger = container.querySelector<HTMLButtonElement>(".thread-plan-trigger")!;
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(trigger.textContent).toContain("任务计划");
    expect(trigger.textContent).toContain("2 / 5");
    expect(trigger.textContent).toContain("迁移任务计划");
    expect(trigger.textContent).toContain("更新交互");
    expect(container.querySelector(".thread-plan-panel")).toBeNull();

    await act(async () => trigger.click());
    const panel = container.querySelector<HTMLElement>(".thread-plan-panel")!;
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(panel.textContent).toContain("移除右侧上下文并迁移计划");
    expect(panel.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(panel.querySelectorAll('.thread-plan-phase li[data-state="in_progress"]')).toHaveLength(1);

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(container.querySelector(".thread-plan-panel")).toBeNull();
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(trigger);
  });

  it("labels a fully completed plan instead of calling its final item current", async () => {
    const finished: TodoList = {
      ...todo,
      phases: todo.phases.map((phase) => ({
        ...phase,
        items: phase.items.map((item) => ({ ...item, status: "completed" as const })),
      })),
    };
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo: finished });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => root?.render(<ThreadPlanBar />));
    expect(container.querySelector(".thread-plan-current small")?.textContent).toBe("已完成");
    expect(container.querySelector(".thread-plan-trigger")?.getAttribute("aria-label")).toContain("已完成");
  });

  it("renders nothing for an empty conversation or an empty plan", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => root?.render(<ThreadPlanBar hidden />));
    expect(container.childElementCount).toBe(0);

    await act(async () => {
      useRuntimeStore.setState({ todo: { ...todo, phases: [] } });
      root?.render(<ThreadPlanBar />);
    });
    expect(container.childElementCount).toBe(0);
  });
});
