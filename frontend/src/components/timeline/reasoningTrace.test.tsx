import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { useRuntimeStore } from "../../store";
import type { Snapshot } from "../../types";
import { ReasoningTrace } from "./blocks";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

function renderTrace(block: Parameters<typeof ReasoningTrace>[0]["block"]) {
  useRuntimeStore.setState({ snapshot, agents: [] });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  void act(() => root.render(<ReasoningTrace block={block} language="zh-CN" />));
  return { container, root };
}

describe("ReasoningTrace empty heartbeats", () => {
  afterEach(() => {
    document.body.replaceChildren();
  });

  it("mounts no collapsible panel shell for an empty thinking heartbeat", () => {
    const { container } = renderTrace({ id: "think-empty", kind: "thinking", state: "streaming", content: "" });
    expect(container.querySelector(".reasoning-body-clip")).toBeNull();
    expect(container.querySelector(".bui-thinking-panel")).toBeNull();
    expect(container.textContent).toContain("正在思考");
  });

  it("keeps the reasoning panel mounted once thinking has text", () => {
    const { container } = renderTrace({ id: "think-full", kind: "thinking", state: "completed", content: "先看 diff 再改守卫" });
    expect(container.querySelector(".reasoning-body-clip")).not.toBeNull();
    expect(container.querySelectorAll(".reasoning-step").length).toBeGreaterThan(0);
  });
});
