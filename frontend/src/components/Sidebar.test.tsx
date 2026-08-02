import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Session, Snapshot } from "../types";
import Sidebar from "./Sidebar";

vi.mock("../bridge", () => ({ execute: vi.fn(), subscribeSessionMenu: vi.fn(() => () => undefined) }));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

describe("Sidebar project sessions", () => {
  afterEach(() => vi.clearAllMocks());

  it("shows five sessions until expanded and starts a new session from the project row", async () => {
    const sessions: Session[] = Array.from({ length: 7 }, (_, index) => ({
      id: `session-${index + 1}`, title: `会话 ${index + 1}`, providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }));
    useRuntimeStore.setState({ snapshot, sessions, currentSessionId: "session-1", view: "thread" });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    expect(container.querySelectorAll(".thread-list > button:not(.show-more-sessions)")).toHaveLength(5);

    await act(async () => container.querySelector<HTMLButtonElement>(".show-more-sessions")!.click());
    expect(container.querySelectorAll(".thread-list > button:not(.show-more-sessions)")).toHaveLength(7);

    await act(async () => container.querySelector<HTMLButtonElement>('.project-action[aria-label="新会话"]')!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "new_session", target: "", sessionId: "session-1" });
    await act(async () => root.unmount());
  });
});
