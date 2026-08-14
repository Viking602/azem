import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProjectSession } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Session, Snapshot } from "../types";
import ArchiveSettings, { ARCHIVE_PAGE_SIZE, countArchivableSessions, groupArchivedSessions } from "./ArchiveSettings";

async function enterInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

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

  it("keeps search matches inside their owning project and drops empty groups", () => {
    const groups = groupArchivedSessions([
      session({ id: "a", workspace: "/Users/me/azem", title: "Azem 旧任务", archived: true }),
      session({ id: "b", workspace: "/Users/me/venat", title: "Venat 审查", archived: true }),
      session({ id: "c", workspace: "", title: "遗留会话", archived: true }),
    ], "Azem");
    expect(groups).toHaveLength(1);
    expect(groups[0]?.workspace).toBe("/Users/me/azem");
    expect(groups[0]?.sessions.map((item) => item.id)).toEqual(["a"]);
    expect(groups[0]?.sessions.every((item) => item.workspace === "/Users/me/azem")).toBe(true);
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

    const venat = container.querySelector<HTMLElement>('[data-archive-project="/workspace/venat"]')!;
    expect(venat.getAttribute("data-expanded")).toBe("false");
    expect(venat.textContent).toContain("venat");
    expect(venat.querySelector(".archive-session-row")).toBeNull();
    expect(container.textContent).not.toContain("旧 Azem 会话");
    expect(container.querySelector("button.small-button")?.textContent).toContain("归档 1 个会话");

    await act(async () => container.querySelector<HTMLButtonElement>("button.small-button")!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "archive_inactive_sessions", target: "30", decision: "", sessionId: "session-current" });

    await act(async () => venat.querySelector<HTMLButtonElement>(".archive-project-toggle")!.click());
    expect(venat.getAttribute("data-expanded")).toBe("true");
    expect(venat.textContent).toContain("旧 Venat 会话");
    expect(venat.querySelector(".archive-session-project")?.textContent).toBe("venat");
    expect(venat.querySelector(".archive-session-path")?.textContent).toBe("/workspace/venat");

    vi.mocked(execute).mockClear();
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".text-button")).find((button) => button.textContent?.includes("恢复"))!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "archive_session", target: "old-venat", decision: "false", sessionId: "session-current" });

    await act(async () => container.querySelector<HTMLButtonElement>(".archive-session-open")!.click());
    expect(openProjectSession).toHaveBeenCalledWith("/workspace/venat", "old-venat");

    await act(async () => root.unmount());
    container.remove();
  });

  it("folds archived sessions by project and does not mix another project's rows into the open group", async () => {
    useRuntimeStore.setState({
      snapshot,
      sessions: [
        session({ id: "azem-old", workspace: "/workspace/azem", title: "旧 Azem 会话", archived: true, updatedAt: "2026-01-02T00:00:00.000Z" }),
        session({ id: "venat-old", workspace: "/workspace/venat", title: "旧 Venat 会话", archived: true, updatedAt: "2026-02-01T00:00:00.000Z" }),
        session({ id: "orphan", workspace: "", title: "遗留会话", archived: true, updatedAt: "2026-03-01T00:00:00.000Z" }),
      ],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ArchiveSettings language="zh-CN" sessionId="session-current" onError={() => undefined} />));

    const azem = container.querySelector<HTMLElement>('[data-archive-project="/workspace/azem"]')!;
    const venat = container.querySelector<HTMLElement>('[data-archive-project="/workspace/venat"]')!;
    const unassigned = container.querySelector<HTMLElement>('[data-archive-project="unassigned"]')!;
    expect(azem.getAttribute("data-expanded")).toBe("false");
    expect(venat.getAttribute("data-expanded")).toBe("false");
    expect(unassigned.getAttribute("data-expanded")).toBe("false");
    expect(unassigned.querySelector("strong")?.textContent).toBe("未归属项目");
    expect(azem.querySelector(".archive-session-row")).toBeNull();
    expect(venat.querySelector(".archive-session-row")).toBeNull();
    expect(container.querySelectorAll(".archive-project")).toHaveLength(3);

    await act(async () => azem.querySelector<HTMLButtonElement>(".archive-project-toggle")!.click());
    expect(azem.getAttribute("data-expanded")).toBe("true");
    expect(azem.textContent).toContain("旧 Azem 会话");
    expect(azem.querySelector(".archive-session-project")?.textContent).toBe("azem");
    expect(azem.querySelector(".archive-session-path")?.textContent).toBe("/workspace/azem");
    expect(azem.textContent).not.toContain("旧 Venat 会话");
    expect(azem.textContent).not.toContain("遗留会话");
    expect(venat.getAttribute("data-expanded")).toBe("false");
    expect(venat.querySelector(".archive-session-row")).toBeNull();

    await act(async () => azem.querySelector<HTMLButtonElement>(".archive-project-toggle")!.click());
    expect(azem.getAttribute("data-expanded")).toBe("false");
    expect(azem.querySelector(".archive-session-row")).toBeNull();
    expect(azem.querySelector(".archive-project-toggle")?.getAttribute("aria-expanded")).toBe("false");

    await enterInput(container.querySelector<HTMLInputElement>(".archive-search input")!, "Venat");
    expect(container.querySelector('[data-archive-project="/workspace/azem"]')).toBeNull();
    expect(container.querySelector('[data-archive-project="unassigned"]')).toBeNull();
    const matched = container.querySelector<HTMLElement>('[data-archive-project="/workspace/venat"]')!;
    expect(matched.getAttribute("data-expanded")).toBe("true");
    expect(matched.textContent).toContain("旧 Venat 会话");
    expect(matched.querySelector(".archive-session-project")?.textContent).toBe("venat");
    expect(matched.textContent).not.toContain("旧 Azem 会话");
    expect(container.querySelectorAll(".archive-project")).toHaveLength(1);

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps space between the archived heading, search field, and project folds", async () => {
    // @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
    const { readFileSync } = await import("node:fs");
    const css = readFileSync("src/styles/settings.css", "utf8");
    expect(css).toMatch(/\.archive-list-card > header\s*\{[^}]*padding:\s*18px 20px 4px;/);
    expect(css).toMatch(/\.archive-search\s*\{[^}]*margin:\s*20px 20px 0;/);
    expect(css).toMatch(/\.archive-projects\s*\{[^}]*gap:\s*8px;[^}]*padding:\s*18px 0 12px;/);
    expect(css).toMatch(/\.archive-project-toggle\s*\{[^}]*min-height:\s*56px;/);
    expect(css).toMatch(/\.archive-session-project\s*\{/);
    expect(css).toMatch(/\.archive-load-more\s*\{/);
  });

  it("paginates archived sessions inside a project group", async () => {
    expect(ARCHIVE_PAGE_SIZE).toBe(20);
    useRuntimeStore.setState({
      snapshot,
      sessions: Array.from({ length: 21 }, (_, index) => session({
        id: `azem-${index}`,
        workspace: "/workspace/azem",
        title: `归档会话 ${index + 1}`,
        archived: true,
        updatedAt: new Date(Date.parse("2026-01-01T00:00:00.000Z") + index * 60_000).toISOString(),
      })),
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ArchiveSettings language="zh-CN" sessionId="session-current" onError={() => undefined} />));

    const group = container.querySelector<HTMLElement>('[data-archive-project="/workspace/azem"]')!;
    expect(group.getAttribute("data-expanded")).toBe("false");
    expect(group.querySelectorAll(".archive-session-row")).toHaveLength(0);

    await act(async () => group.querySelector<HTMLButtonElement>(".archive-project-toggle")!.click());
    const titles = () => Array.from(group.querySelectorAll<HTMLElement>(".archive-session-open strong")).map((node) => node.textContent);
    expect(group.querySelectorAll(".archive-session-row")).toHaveLength(ARCHIVE_PAGE_SIZE);
    expect(titles()).toContain("归档会话 21");
    expect(titles()).not.toContain("归档会话 1");
    expect(group.querySelector(".archive-load-more")?.textContent).toContain("还有 1 个");
    expect(group.querySelectorAll(".archive-session-project")).toHaveLength(ARCHIVE_PAGE_SIZE);
    expect(group.querySelector(".archive-session-project")?.textContent).toBe("azem");

    await act(async () => group.querySelector<HTMLButtonElement>(".archive-load-more")!.click());
    expect(group.querySelectorAll(".archive-session-row")).toHaveLength(21);
    expect(titles()).toContain("归档会话 1");
    expect(group.querySelector(".archive-load-more")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });
});
