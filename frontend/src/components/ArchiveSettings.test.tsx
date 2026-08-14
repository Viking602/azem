import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProjectSession } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Session, Snapshot } from "../types";
import ArchiveSettings, { countArchivableSessions, groupArchivedSessions } from "./ArchiveSettings";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({
  execute: vi.fn(() => Promise.resolve()),
  openProjectSession: vi.fn(() => Promise.resolve()),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-current", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 4, chatgptFastMode: false, sequence: 0,
};

function session(partial: Partial<Session>): Session {
  return {
    id: "session", workspace: "/workspace/azem", title: "会话", providerId: "chatgpt",
    modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    ...partial,
  };
}

describe("ArchiveSettings", () => {
  afterEach(() => vi.clearAllMocks());

  it("groups archived sessions by owning project and keeps unassigned history separate", () => {
    const groups = groupArchivedSessions([
      session({ id: "a", workspace: "/Users/me/azem", title: "Azem 旧任务", archived: true, updatedAt: "2026-01-02T00:00:00.000Z" }),
      session({ id: "b", workspace: "/Users/me/venat", title: "Venat 审查", archived: true, updatedAt: "2026-02-01T00:00:00.000Z" }),
      session({ id: "c", workspace: "/Users/me/azem", title: "更早的 Azem", archived: true, updatedAt: "2026-01-01T00:00:00.000Z" }),
      session({ id: "d", workspace: "", title: "遗留会话", archived: true }),
      session({ id: "live", workspace: "/Users/me/azem", title: "仍在侧栏", archived: false }),
    ]);
    expect(groups.map((group) => group.name)).toEqual(["", "azem", "venat"]);
    expect(groups[0]?.sessions.map((item) => item.id)).toEqual(["d"]);
    expect(groups[1]?.sessions.map((item) => item.id)).toEqual(["a", "c"]);
    expect(groups[2]?.workspace).toBe("/Users/me/venat");
  });

  it("counts only unpinned idle sessions outside the current conversation", () => {
    const now = Date.parse("2026-08-14T00:00:00.000Z");
    const count = countArchivableSessions([
      session({ id: "old", updatedAt: "2026-06-01T00:00:00.000Z" }),
      session({ id: "pinned-old", pinned: true, updatedAt: "2026-06-01T00:00:00.000Z" }),
      session({ id: "session-current", updatedAt: "2026-06-01T00:00:00.000Z" }),
      session({ id: "archived", archived: true, updatedAt: "2026-06-01T00:00:00.000Z" }),
      session({ id: "fresh", updatedAt: "2026-08-10T00:00:00.000Z" }),
    ], 30, "session-current", now);
    expect(count).toBe(1);
  });

  it("archives idle sessions and restores an archived row back to its project", async () => {
    useRuntimeStore.setState({
      snapshot,
      sessions: [
        session({ id: "session-current", title: "当前会话", updatedAt: new Date(Date.now() - 40 * 24 * 60 * 60 * 1000).toISOString() }),
        session({ id: "old-azem", workspace: "/workspace/azem", title: "旧 Azem 会话", archived: false, updatedAt: new Date(Date.now() - 40 * 24 * 60 * 60 * 1000).toISOString() }),
        session({ id: "old-venat", workspace: "/workspace/venat", title: "旧 Venat 会话", archived: true, updatedAt: new Date(Date.now() - 80 * 24 * 60 * 60 * 1000).toISOString() }),
      ],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ArchiveSettings language="zh-CN" sessionId="session-current" onError={() => undefined} />));

    expect(container.textContent).toContain("旧 Venat 会话");
    expect(container.textContent).toContain("venat");
    expect(container.textContent).not.toContain("旧 Azem 会话");
    expect(container.querySelector("button.small-button")?.textContent).toContain("归档 1 个会话");

    await act(async () => container.querySelector<HTMLButtonElement>("button.small-button")!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "archive_inactive_sessions", target: "30", decision: "", sessionId: "session-current" });

    vi.mocked(execute).mockClear();
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".text-button")).find((button) => button.textContent?.includes("恢复"))!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "archive_session", target: "old-venat", decision: "false", sessionId: "session-current" });

    await act(async () => container.querySelector<HTMLButtonElement>(".archive-session-open")!.click());
    expect(openProjectSession).toHaveBeenCalledWith("/workspace/venat", "old-venat");

    await act(async () => root.unmount());
    container.remove();
  });
});
