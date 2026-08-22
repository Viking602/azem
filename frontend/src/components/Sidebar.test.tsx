import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProject, openProjectSession, resumeSession, selectProjectFolder } from "../bridge";
import { useRuntimeStore } from "../store";
import type { RuntimeEvent, Session, Snapshot } from "../types";
import Sidebar from "./Sidebar";

vi.mock("../bridge", () => ({
  createProject: vi.fn(), execute: vi.fn(), openProject: vi.fn().mockResolvedValue(undefined), openProjectSession: vi.fn().mockResolvedValue(undefined), resumeSession: vi.fn().mockResolvedValue(null), selectProjectFolder: vi.fn(),
  isDesktopRuntime: vi.fn(() => true),
  subscribeSessionMenu: vi.fn(() => () => undefined),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

describe("Sidebar project sessions", () => {
  afterEach(() => vi.clearAllMocks());

  it("slides the shared switcher indicator between projects and workspace", async () => {
    useRuntimeStore.setState({ snapshot, projects: [], sessions: [], currentSessionId: "session-1", view: "thread" });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    const switcher = container.querySelector<HTMLElement>(".sidebar-switcher")!;
    const tabs = container.querySelectorAll<HTMLButtonElement>('.sidebar-switcher [role="tab"]');
    expect(switcher.dataset.active).toBe("projects");
    expect(tabs[0]!.getAttribute("aria-selected")).toBe("true");
    expect(tabs[1]!.getAttribute("aria-selected")).toBe("false");

    await act(async () => tabs[1]!.click());
    expect(switcher.dataset.active).toBe("workspace");
    expect(tabs[0]!.getAttribute("aria-selected")).toBe("false");
    expect(tabs[1]!.getAttribute("aria-selected")).toBe("true");
    await act(async () => root.unmount());
  });

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

    await act(async () => container.querySelector<HTMLButtonElement>('.project-action[aria-label="新对话"]')!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "new_session", target: "", sessionId: "session-1" });
    await act(async () => root.unmount());
  });

  it("applies the direct session projection when a cold-start sidebar row is clicked", async () => {
    const sessions: Session[] = [
      { id: "session-1", workspace: snapshot.workspace, title: "当前会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
      { id: "session-2", workspace: snapshot.workspace, title: "冷启动目标", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
    ];
    const projection: RuntimeEvent = {
      sequence: 0,
      kind: "session_loaded",
      sessionId: "session-2",
      state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    };
    vi.mocked(resumeSession).mockResolvedValueOnce(projection);
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread", lastSequence: 42,
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    const target = Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list > button"))
      .find((button) => button.textContent?.includes("冷启动目标"))!;
    await act(async () => target.click());

    expect(resumeSession).toHaveBeenCalledWith("session-2");
    expect(execute).not.toHaveBeenCalledWith(expect.objectContaining({ kind: "resume_session" }));
    expect(useRuntimeStore.getState().currentSessionId).toBe("session-2");
    await act(async () => root.unmount());
  });

  it("marks an expanded project without a pull request as a sessions-only tree", async () => {
    const sessions: Session[] = [{
      id: "session-1", workspace: snapshot.workspace, title: "分析工作区修改内容", providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }];
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread", pullRequestDashboard: null,
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    const project = container.querySelector<HTMLElement>(".project-node")!;
    expect(project.dataset.projectLayout).toBe("sessions-only");
    expect(project.dataset.expanded).toBe("true");
    expect(project.querySelector(":scope > .project-heading + .thread-list")).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("hides archived sessions from the project sidebar", async () => {
    const sessions: Session[] = [
      { id: "session-1", workspace: snapshot.workspace, title: "当前会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
      { id: "session-archived", workspace: snapshot.workspace, title: "已归档会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", archived: true, updatedAt: new Date().toISOString() },
    ];
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread",
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Sidebar />));
    const titles = Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list > button .session-copy strong")).map((node) => node.textContent);
    expect(titles).toContain("当前会话");
    expect(titles).not.toContain("已归档会话");
    expect(container.querySelector(".thread-list > button[title]")).toBeNull();
    await act(async () => root.unmount());
  });

  it("keeps the full session title in the row without a native tooltip", async () => {
    const title = "调查 Azem 全栈架构与回归测试";
    const sessions: Session[] = [{
      id: "session-1", workspace: snapshot.workspace, title, providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }];
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread",
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Sidebar />));
    const button = container.querySelector<HTMLButtonElement>(".thread-list > button");
    const strong = button?.querySelector(".session-copy strong");
    expect(strong?.textContent).toBe(title);
    expect(button?.getAttribute("title")).toBeNull();
    expect(strong?.getAttribute("title")).toBeNull();
    await act(async () => root.unmount());
  });

  it("keeps project and session rows single-line without decorative metadata", async () => {
    const sessions: Session[] = [{
      id: "session-1", workspace: snapshot.workspace, title: "分析当前变更内容", providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
      updatedAt: new Date(Date.now() - 17 * 60_000).toISOString(),
    }];
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread",
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Sidebar />));
    const project = container.querySelector(".project-toggle")!;
    const session = container.querySelector(".thread-list > button")!;
    expect(project.querySelector(".project-heading-copy strong")?.textContent).toBe("azem");
    expect(project.querySelector(".project-initial")).toBeNull();
    expect(project.querySelector(".project-heading-copy small")).toBeNull();
    expect(project.querySelector(":scope > em")).toBeNull();
    expect(session.querySelector(".session-copy strong")?.textContent).toBe("分析当前变更内容");
    expect(session.querySelector(".session-copy small")).toBeNull();
    expect(container.textContent).not.toContain("17 分钟前");
    await act(async () => root.unmount());
  });

  it("shows a lightweight spinner only on the running session", async () => {
    const sessions: Session[] = ["session-1", "session-2"].map((id) => ({
      id, workspace: snapshot.workspace, title: id === "session-1" ? "当前会话" : "后台运行会话", providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    }));
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread", running: false,
      globalRunId: "run-2", globalRunSessionId: "session-2",
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list > button"));
    const current = buttons.find((button) => button.querySelector(".session-copy strong")?.textContent === "当前会话")!;
    const active = buttons.find((button) => button.querySelector(".session-copy strong")?.textContent === "后台运行会话")!;
    expect(current.querySelector(".session-running-indicator")).toBeNull();
    expect(current.getAttribute("aria-busy")).toBe("false");
    expect(active.querySelector(".session-running-indicator")).not.toBeNull();
    expect(active.getAttribute("aria-busy")).toBe("true");

    await act(async () => useRuntimeStore.setState({ globalRunId: "", globalRunSessionId: "" }));
    expect(container.querySelector(".session-running-indicator")).toBeNull();

    await act(async () => useRuntimeStore.setState({ running: true }));
    expect(current.querySelector(".session-running-indicator")).not.toBeNull();
    expect(active.querySelector(".session-running-indicator")).toBeNull();
    await act(async () => root.unmount());
  });

  it("shows an unread blue dot only for a background session that completed away from view", async () => {
    const sessions: Session[] = [
      { id: "session-1", workspace: snapshot.workspace, title: "当前会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
      { id: "session-2", workspace: snapshot.workspace, title: "后台已完成", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", unread: true, updatedAt: new Date().toISOString() },
    ];
    useRuntimeStore.setState({
      snapshot, projects: [{ workspace: snapshot.workspace, updatedAt: "" }], sessions,
      currentSessionId: "session-1", view: "thread", running: false, globalRunId: "", globalRunSessionId: "",
    });
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<Sidebar />));
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list > button"));
    expect(buttons.find((button) => button.querySelector(".session-copy strong")?.textContent === "当前会话")?.querySelector(".session-unread")).toBeNull();
    const unread = buttons.find((button) => button.querySelector(".session-copy strong")?.textContent === "后台已完成")?.querySelector<HTMLElement>(".session-unread");
    expect(unread).not.toBeNull();
    expect(unread?.getAttribute("title")).toBeNull();
    expect(unread?.getAttribute("aria-label")).toBe("未读");
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
    expect(Array.from(container.querySelectorAll(".project-heading-copy strong")).map((node) => node.textContent)).toEqual(["azem", "synara"]);
    await act(async () => container.querySelectorAll<HTMLButtonElement>(".project-toggle")[1]!.click());
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".thread-list button")).find((button) => button.textContent?.includes("Synara 会话"))!.click());

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
