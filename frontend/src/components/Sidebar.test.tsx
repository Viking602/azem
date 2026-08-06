import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProject, openProjectSession, selectProjectFolder } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Session, Snapshot } from "../types";
import Sidebar from "./Sidebar";

vi.mock("../bridge", () => ({
  createProject: vi.fn(), execute: vi.fn(), openProject: vi.fn().mockResolvedValue(undefined), openProjectSession: vi.fn().mockResolvedValue(undefined), selectProjectFolder: vi.fn(),
  subscribeSessionMenu: vi.fn(() => () => undefined),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

describe("Sidebar project sessions", () => {
  afterEach(() => vi.clearAllMocks());

  it("shows five sessions until expanded and starts a new session from the project row", async () => {
    const sessions: Session[] = Array.from({ length: 7 }, (_, index) => ({
      id: `session-${index + 1}`, workspace: snapshot.workspace, title: `会话 ${index + 1}`, providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }));
    useRuntimeStore.setState({ snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions, currentSessionId: "session-1", view: "thread" });
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

  it("renders multiple durable projects and opens another project's session in its workspace", async () => {
    const other = "/workspace/synara";
    const sessions: Session[] = [{
      id: "session-other", workspace: other, title: "Synara 会话", providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }];
    useRuntimeStore.setState({ snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }, { workspace: other, updatedAt: "" }], sessions, currentSessionId: "session-1", view: "thread" });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    expect(Array.from(container.querySelectorAll(".project-toggle span")).map((node) => node.textContent)).toEqual(["azem", "synara"]);
    await act(async () => container.querySelectorAll<HTMLButtonElement>(".project-toggle")[1]!.click());
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list button")).find((button) => button.textContent === "Synara 会话")!.click());

    expect(openProjectSession).toHaveBeenCalledWith(other, "session-other");
    await act(async () => root.unmount());
  });

  it("opens a selected project folder in a new Azem window", async () => {
    useRuntimeStore.setState({ snapshot, sessions: [], currentSessionId: "session-1", view: "thread" });
    vi.mocked(selectProjectFolder).mockResolvedValueOnce("/workspace/next");
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    await act(async () => container.querySelector<HTMLButtonElement>(".project-add-button")!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('.project-add-menu [role="menuitem"]')!.click());

    expect(openProject).toHaveBeenCalledWith("/workspace/next");
    await act(async () => root.unmount());
  });
});
