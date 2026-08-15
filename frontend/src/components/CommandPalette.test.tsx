import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProjectSession, resumeSession, searchSessions } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot } from "../types";
import CommandPalette from "./CommandPalette";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
  configurable: true,
  value(this: HTMLDialogElement) { this.setAttribute("open", ""); },
});

vi.mock("../bridge", () => ({
  execute: vi.fn(() => Promise.resolve()),
  openProjectSession: vi.fn(() => Promise.resolve()),
  resumeSession: vi.fn(() => Promise.resolve({ kind: "session_loaded", sessionId: "session-result", state: "loaded", data: { blocks: "[]" } })),
  searchSessions: vi.fn(() => Promise.resolve([])),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-current", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 4, shellConcurrency: 2, subagentAwaitSeconds: 600, subagentIdleSeconds: 0,
  chatgptFastMode: false, sequence: 0,
};

async function enterInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function renderPalette() {
  useRuntimeStore.setState({
    snapshot,
    currentSessionId: "session-current",
    modelRoutes: [],
    modelProviders: [],
    commandOpen: true,
    settingsOpen: false,
    settingsTarget: null,
    sessionSearchTarget: null,
    error: "",
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<CommandPalette />));
  return { container, root };
}

describe("CommandPalette global search", () => {
  afterEach(() => {
    vi.clearAllMocks();
    vi.useRealTimers();
    document.body.replaceChildren();
  });

  it("opens and focuses an exact setting result", async () => {
    vi.useFakeTimers();
    const { container, root } = await renderPalette();
    await enterInput(container.querySelector("input")!, "字体大小");
    await act(async () => { vi.advanceTimersByTime(160); await Promise.resolve(); });
    const result = Array.from(container.querySelectorAll<HTMLButtonElement>(".command-list button"))
      .find((button) => button.textContent?.includes("字体大小"));
    expect(result).toBeTruthy();
    await act(async () => result?.click());
    expect(useRuntimeStore.getState().settingsOpen).toBe(true);
    expect(useRuntimeStore.getState().settingsTarget).toEqual({ section: "appearance", id: "appearance:font-size" });
    await act(async () => root.unmount());
  });

  it("debounces content search and opens the matching message sequence", async () => {
    vi.useFakeTimers();
    vi.mocked(searchSessions).mockResolvedValueOnce([{
      sessionId: "session-result", workspace: snapshot.workspace, title: "Search result", kind: "assistant",
      preview: "matched durable conversation content", sequence: 42, updatedAt: "2026-08-12T00:00:00Z",
    }]);
    const { container, root } = await renderPalette();
    await enterInput(container.querySelector("input")!, "matched");
    expect(searchSessions).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(159); await Promise.resolve(); });
    expect(searchSessions).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(1); await Promise.resolve(); });
    expect(searchSessions).toHaveBeenCalledWith("matched", 24);
    const result = Array.from(container.querySelectorAll<HTMLButtonElement>(".command-list button"))
      .find((button) => button.textContent?.includes("matched durable conversation content"));
    expect(result).toBeTruthy();
    await act(async () => { result?.click(); await Promise.resolve(); });
    expect(useRuntimeStore.getState().sessionSearchTarget).toEqual({ sessionId: "session-result", sequence: 42 });
    expect(resumeSession).toHaveBeenCalledWith("session-result");
    await act(async () => root.unmount());
  });

  it("reloads a matching current session before focusing its durable message", async () => {
    vi.useFakeTimers();
    vi.mocked(searchSessions).mockResolvedValueOnce([{
      sessionId: snapshot.sessionId, workspace: snapshot.workspace, title: "Current session", kind: "user",
      preview: "current session durable match", sequence: 0, updatedAt: "2026-08-12T00:00:00Z",
    }]);
    const { container, root } = await renderPalette();
    await enterInput(container.querySelector("input")!, "durable match");
    await act(async () => { vi.advanceTimersByTime(160); await Promise.resolve(); });
    const result = Array.from(container.querySelectorAll<HTMLButtonElement>(".command-list button"))
      .find((button) => button.textContent?.includes("current session durable match"));
    await act(async () => { result?.click(); await Promise.resolve(); });
    expect(useRuntimeStore.getState().sessionSearchTarget).toEqual({ sessionId: snapshot.sessionId, sequence: 0 });
    expect(resumeSession).toHaveBeenCalledWith(snapshot.sessionId);
    await act(async () => root.unmount());
  });

  it("passes only the durable sequence when opening a cross-project result", async () => {
    vi.useFakeTimers();
    vi.mocked(searchSessions).mockResolvedValueOnce([{
      sessionId: "session-other", workspace: "/workspace/other", title: "Other project", kind: "user",
      preview: "cross project match", sequence: 7, updatedAt: "2026-08-12T00:00:00Z",
    }]);
    const { container, root } = await renderPalette();
    await enterInput(container.querySelector("input")!, "cross");
    await act(async () => { vi.advanceTimersByTime(160); await Promise.resolve(); });
    const result = Array.from(container.querySelectorAll<HTMLButtonElement>(".command-list button"))
      .find((button) => button.textContent?.includes("cross project match"));
    await act(async () => { result?.click(); await Promise.resolve(); });
    expect(openProjectSession).toHaveBeenCalledWith("/workspace/other", "session-other", 7);
    expect(useRuntimeStore.getState().sessionSearchTarget).toBeNull();
    await act(async () => root.unmount());
  });

  it("discards an older database response after the query changes", async () => {
    vi.useFakeTimers();
    let resolveFirst!: (value: Awaited<ReturnType<typeof searchSessions>>) => void;
    let resolveSecond!: (value: Awaited<ReturnType<typeof searchSessions>>) => void;
    vi.mocked(searchSessions)
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveSecond = resolve; }));
    const { container, root } = await renderPalette();
    const input = container.querySelector("input")!;
    await enterInput(input, "older-query");
    await act(async () => { vi.advanceTimersByTime(160); });
    await enterInput(input, "newer-query");
    await act(async () => { vi.advanceTimersByTime(160); });
    await act(async () => resolveSecond([{
      sessionId: "new", workspace: snapshot.workspace, title: "New result", kind: "title", updatedAt: "2026-08-12T00:00:00Z",
    }]));
    expect(container.textContent).toContain("New result");
    await act(async () => resolveFirst([{
      sessionId: "old", workspace: snapshot.workspace, title: "Old result", kind: "title", updatedAt: "2026-08-12T00:00:00Z",
    }]));
    expect(container.textContent).toContain("New result");
    expect(container.textContent).not.toContain("Old result");
    await act(async () => root.unmount());
  });
});
