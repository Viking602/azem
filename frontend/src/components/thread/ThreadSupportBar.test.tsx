import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getSessionTree, navigateSessionTree } from "../../bridge";
import { useRuntimeStore } from "../../store";
import { useTerminalStore } from "../../terminalStore";
import type { Snapshot, TodoList } from "../../types";
import { ThreadEnvironmentPanel, summarizeThreadPlan } from "./ThreadSupportBar";

vi.mock("../../bridge", () => ({
  attachmentDataURL: vi.fn(async () => "data:image/png;base64,AA=="),
  createSessionFork: vi.fn(async () => sessionTree),
  getSessionTree: vi.fn(async () => sessionTree),
  listWorkspaceChanges: vi.fn(async () => ({ repository: true, branch: "feature/environment", additions: 12, deletions: 3, files: [] })),
  navigateSessionTree: vi.fn(async () => null),
  openExternalURL: vi.fn(async () => undefined),
  setSessionEntryLabel: vi.fn(async () => sessionTree),
}));

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snapshot: Snapshot = {
  workspace: "/tmp/azem", currentBranch: "main", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

const todo: TodoList = {
  goal: "重构线程辅助信息",
  revision: 1,
  phases: [
    { id: "phase-1", title: "实现", items: [
      { id: "done", content: "移除顶部计划条", status: "completed" },
      { id: "current", content: "添加环境面板", status: "in_progress" },
      { id: "next", content: "恢复回顾与来源", status: "pending" },
    ] },
    { id: "phase-2", title: "验证", items: [
      { id: "cancelled", content: "废弃旧布局", status: "cancelled" },
      { id: "verify", content: "运行完整检查", status: "pending" },
    ] },
  ],
};

const sessionTree = {
  sessionId: "s1", rootSessionId: "s1", sourceKind: "native", activeBranch: "main", activeLeafEntryId: "entry-2",
  roots: [{ entry: { id: "entry-1", sequence: 1, kind: "user", createdAt: "2026-08-23T00:00:00Z" }, children: [
    { entry: { id: "entry-2", parentId: "entry-1", sequence: 2, kind: "assistant", label: "Current answer", createdAt: "2026-08-23T00:00:01Z" } },
    { entry: { id: "entry-3", parentId: "entry-1", sequence: 3, kind: "assistant", label: "Alternative", createdAt: "2026-08-23T00:00:02Z" } },
  ] }],
  branches: [{ name: "main", headEntryId: "entry-2", active: true, createdAt: "2026-08-23T00:00:00Z", updatedAt: "2026-08-23T00:00:01Z" }],
};

let root: Root | null = null;
let container: HTMLDivElement | null = null;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  root = null;
  container = null;
  vi.clearAllMocks();
});

describe("thread environment panel", () => {
  it("derives honest plan progress", () => {
    expect(summarizeThreadPlan(todo)).toMatchObject({ completed: 2, percentage: 40 });
    expect(summarizeThreadPlan({ ...todo, phases: [] })).toBeNull();
  });

  it("projects real workspace, terminal, plan, recap, and source state into Synara rows", async () => {
    useRuntimeStore.setState({
      snapshot,
      currentSessionId: "s1",
      view: "thread",
      todo,
      settingsOpen: false,
      branches: [{ name: "feature/environment", current: true }],
      workspaceAdditions: 12,
      workspaceDeletions: 3,
      recap: { sessionId: "s1", revision: 4, summary: "回顾摘要", goal: "当前目标", openItems: "未完成事项", updatedAt: "2026-08-23T00:00:00Z" },
      blocks: [
        { id: "user", kind: "user", content: "参考 https://example.com/spec", attachments: [{ id: "img", name: "image.png", mimeType: "image/png", path: "/tmp/image.png", size: 10 }] },
        { id: "search", kind: "tool", title: "web_search", content: JSON.stringify({ title: "Design source", url: "https://example.com/design" }) },
      ],
    });
    useTerminalStore.setState({
      open: false,
      sessions: [{ id: "term-1", title: "zsh", cwd: "/tmp/azem", shell: "zsh", cols: 80, rows: 24, state: "running" }],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(<ThreadEnvironmentPanel open />);
      await Promise.resolve();
    });

    const overlay = container.querySelector<HTMLElement>(".thread-environment-overlay")!;
    const card = overlay.querySelector<HTMLElement>(".thread-environment-card")!;
    const rows = Array.from(card.querySelectorAll<HTMLButtonElement>(".thread-environment-row"));
    expect(overlay.dataset.open).toBe("true");
    expect(card.hasAttribute("inert")).toBe(false);
    expect(card.querySelector(".thread-environment-title > span")?.textContent).toBe("环境");
    expect(card.textContent).toContain("+12");
    expect(card.textContent).toContain("−3");
    expect(card.textContent).toContain("feature/environment");
    await act(async () => useRuntimeStore.setState({ workspaceAdditions: 21, workspaceDeletions: 4 }));
    expect(card.textContent).toContain("+21");
    expect(card.textContent).toContain("−4");
    expect(rows.find((row) => row.textContent?.includes("本地服务"))?.textContent).toContain("1");

    const plan = rows.find((row) => row.textContent?.includes("计划"))!;
    await act(async () => plan.click());
    expect(plan.getAttribute("aria-expanded")).toBe("true");
    expect(card.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(card.querySelectorAll(".thread-environment-plan-items li")).toHaveLength(5);


    const history = rows.find((row) => row.textContent?.includes("会话历史"))!;
    await act(async () => { history.click(); await Promise.resolve(); });
    expect(getSessionTree).toHaveBeenCalledWith("s1");
    expect(card.querySelectorAll(".session-tree-list > li")).toHaveLength(3);
    const alternative = Array.from(card.querySelectorAll<HTMLButtonElement>(".session-tree-entry")).find((button) => button.textContent?.includes("Alternative"))!;
    await act(async () => { alternative.click(); await Promise.resolve(); });
    expect(navigateSessionTree).toHaveBeenCalledWith("s1", "entry-3");
    const recap = rows.find((row) => row.textContent?.includes("回顾"))!;
    await act(async () => recap.click());
    expect(recap.getAttribute("aria-expanded")).toBe("true");
    expect(card.textContent).toContain("回顾摘要");

    const sources = rows.find((row) => row.textContent?.includes("来源"))!;
    await act(async () => sources.click());
    expect(sources.getAttribute("aria-expanded")).toBe("true");
    expect(card.querySelectorAll(".thread-environment-source-row")).toHaveLength(3);
    const composerAttach = document.createElement("button");
    composerAttach.dataset.slot = "composer-attach";
    const openAttachmentPicker = vi.fn();
    composerAttach.addEventListener("click", openAttachmentPicker);
    document.body.append(composerAttach);
    await act(async () => card.querySelector<HTMLButtonElement>(".thread-environment-source-add")?.click());
    expect(openAttachmentPicker).toHaveBeenCalledOnce();
    composerAttach.remove();

    await act(async () => card.querySelector<HTMLButtonElement>('.thread-environment-title button')?.click());
    expect(useRuntimeStore.getState().settingsOpen).toBe(true);
  });

  it("localizes every panel chrome label through the shared translator", async () => {
    useRuntimeStore.setState({
      snapshot: { ...snapshot, language: "en" },
      currentSessionId: "s1",
      todo,
      recap: null,
      blocks: [],
    });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => {
      root?.render(<ThreadEnvironmentPanel open />);
      await Promise.resolve();
    });
    const card = container.querySelector<HTMLElement>(".thread-environment-card")!;
    const text = card.textContent || "";
    expect(card.querySelector(".thread-environment-title > span")?.textContent).toBe("Environment");
    expect(text).toContain("Changes");
    expect(text).toContain("Local servers");
    expect(text).toContain("Conversation");
    expect(text).toContain("Plan");
    expect(text).toContain("Recap");
    expect(text).toContain("Sources");
    expect(text).toContain("Editor");
    expect(text).toContain("Editor view");
    expect(text).toContain("Terminal");
  });

  it("keeps the closed panel mounted but inert for the slide transition", async () => {
    useRuntimeStore.setState({ snapshot, currentSessionId: "s1", todo: null, recap: null, blocks: [] });
    container = document.createElement("div");
    root = createRoot(container);
    await act(async () => root?.render(<ThreadEnvironmentPanel open={false} />));
    expect(container.querySelector(".thread-environment-card")?.hasAttribute("inert")).toBe(true);
  });
});
