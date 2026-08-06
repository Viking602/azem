import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import App, { takeRuntimeEventFrame } from "./App";
import { useRuntimeStore } from "./store";
import type { RuntimeEvent, Snapshot } from "./types";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });

let container: HTMLDivElement | null = null;
let root: Root | null = null;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  root = null;
  container = null;
});

describe("application interactions", () => {
  it("paces streaming text without breaking Unicode or event order", () => {
    const text = `${"a".repeat(1023)}😀${"b".repeat(1100)}`;
    const queue: RuntimeEvent[] = [
      { sequence: 10, kind: "text_delta", runId: "run", text, state: "streaming" },
      { sequence: 11, kind: "run_finished", runId: "run" },
    ];
    const delivered: RuntimeEvent[] = [];

    while (queue.length) delivered.push(...takeRuntimeEventFrame(queue));

    expect(delivered.filter((event) => event.kind === "text_delta").map((event) => event.text).join("")).toBe(text);
    expect(delivered.at(-1)?.kind).toBe("run_finished");
    expect(delivered.slice(0, -2).every((event) => event.sequence === 0)).toBe(true);
    expect(delivered.at(-2)?.sequence).toBe(10);
    expect(delivered[0]?.text?.endsWith("\uD83D")).toBe(false);

    const calm = [{ sequence: 1, kind: "text_delta", text: "x".repeat(100) }];
    const reduced = [{ sequence: 1, kind: "text_delta", text: "x".repeat(3000) }];
    expect(takeRuntimeEventFrame(calm)[0]?.text).toHaveLength(72);
    expect(takeRuntimeEventFrame(reduced, true)[0]?.text).toHaveLength(2048);
  });

  it("suppresses the browser context menu across the document", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<App />));

    const event = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
    document.body.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
  });

  it("keeps the thread mounted while subagents open in a drawer", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    useRuntimeStore.setState({
      snapshot,
      view: "agents",
      blocks: [{ id: "answer-1", kind: "assistant", content: "主会话仍然可见" }],
      selectedAgentId: "",
      agents: [{
        id: "agent-1", type: "worker", description: "检查界面", model: "gpt-5.6-sol",
        background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
        activity: "", warning: "", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 20,
        elapsedMs: 1000, state: "completed", summary: "已完成", preview: "已完成",
        previewKind: "assistant", previewRunId: "child-1", elapsedObservedAt: Date.now(),
      }],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));

    expect(container.querySelector(".thread-surface")).not.toBeNull();
    expect(container.querySelector(".subagents-drawer-layer")).not.toBeNull();
    await act(async () => container?.querySelector<HTMLButtonElement>(".subagent-row > button")?.click());
    expect(container.querySelector(".subagents-drawer-layer")).toBeNull();
    expect(container.querySelector(".agent-side-chat")).not.toBeNull();
    await act(async () => container?.querySelector<HTMLButtonElement>(".agent-side-chat-actions button:last-child")?.click());
    expect(container.querySelector(".subagents-drawer-layer")).not.toBeNull();
    await act(async () => container?.querySelector<HTMLDivElement>(".subagents-drawer-layer")?.click());
    expect(useRuntimeStore.getState().view).toBe("thread");
  });
});
