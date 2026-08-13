import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, listWorkspaceChanges, openWorkspaceTerminal } from "../bridge";
import { openPullRequest, refreshPullRequestDashboard } from "../pullRequests";
import { useRuntimeStore } from "../store";
import type { PullRequestDashboard, Snapshot, WorkspaceChangeSet } from "../types";
import WorkspaceOverviewPage from "./WorkspaceOverviewPage";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({
  execute: vi.fn().mockResolvedValue(undefined),
  isDesktopRuntime: vi.fn(() => true),
  listWorkspaceChanges: vi.fn(),
  openWorkspaceTerminal: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../pullRequests", () => ({
  openPullRequest: vi.fn().mockResolvedValue(undefined),
  refreshPullRequestDashboard: vi.fn().mockResolvedValue(undefined),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0, currentBranch: "main",
};

const changes: WorkspaceChangeSet = {
  repository: true, branch: "codex/workspace-ui", base: "HEAD", additions: 12, deletions: 3,
  files: [
    { path: "frontend/src/App.tsx", status: "modified", additions: 8, deletions: 2 },
    { path: "frontend/src/styles.css", status: "modified", additions: 4, deletions: 1 },
  ],
};

const pullRequests: PullRequestDashboard = {
  capability: { available: true },
  repository: {
    nameWithOwner: "Viking602/azem", url: "https://github.com/Viking602/azem", defaultBranch: "main",
    viewerPermission: "ADMIN", viewerLogin: "viking", allowedMergeMethods: ["squash", "rebase"],
  },
  currentBranch: "codex/workspace-ui",
  current: {
    number: 24, title: "feat: implement workspace UI", state: "OPEN", draft: false,
    url: "https://github.com/Viking602/azem/pull/24", author: { login: "viking" }, baseRefName: "main",
    headRefName: "codex/workspace-ui", headRefOid: "abc", mergeable: "MERGEABLE", reviewDecision: "APPROVED",
    additions: 12, deletions: 3, changedFiles: 2,
    checks: { total: 6, passing: 6, failing: 0, pending: 0, neutral: 0, skipped: 0 },
    updatedAt: "2026-08-09T06:00:00Z",
  },
  open: [], createdByViewer: [], needsReview: [], refreshedAt: "2026-08-09T06:00:00Z",
};

let container: HTMLDivElement;
let root: Root;

afterEach(async () => {
  await act(async () => root?.unmount());
  container?.remove();
  vi.clearAllMocks();
});

describe("WorkspaceOverviewPage", () => {
  it("projects real workspace, session, change, and pull-request state into one navigable page", async () => {
    vi.mocked(listWorkspaceChanges).mockResolvedValue(changes);
    useRuntimeStore.setState({
      snapshot, view: "projects", currentSessionId: "session-1", running: true,
      sessions: [{
        id: "session-1", title: "实施完整界面", workspace: snapshot.workspace, updatedAt: "2026-08-09T06:00:00Z",
        providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
      }],
      pullRequestDashboard: pullRequests, pullRequestLoading: false, error: "",
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceOverviewPage />));
    await act(async () => Promise.resolve());

    expect(listWorkspaceChanges).toHaveBeenCalledTimes(1);
    expect(refreshPullRequestDashboard).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain("codex/workspace-ui");
    expect(container.textContent).toContain("2 个文件 · +12 −3");
    expect(container.textContent).toContain("#24");
    expect(container.textContent).toContain("实施完整界面");

    const filesTab = Array.from(container.querySelectorAll<HTMLButtonElement>(".workspace-page-tabs button")).find((button) => button.textContent?.includes("项目文件"))!;
    await act(async () => filesTab.click());
    expect(useRuntimeStore.getState().view).toBe("files");

    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".workspace-activity-list button"))[0]!.click());
    expect(execute).toHaveBeenCalledWith({ kind: "resume_session", target: "session-1", sessionId: "session-1" });
    expect(useRuntimeStore.getState().view).toBe("thread");

    await act(async () => container.querySelector<HTMLButtonElement>(".workspace-open-terminal")!.click());
    expect(openWorkspaceTerminal).toHaveBeenCalledTimes(1);

    await act(async () => container.querySelector<HTMLButtonElement>(".workspace-current-pr")!.click());
    expect(openPullRequest).toHaveBeenCalledWith(24);
  });

  it("creates a conversation already locked to the current project", async () => {
    vi.mocked(listWorkspaceChanges).mockResolvedValue({ ...changes, files: [], additions: 0, deletions: 0 });
    useRuntimeStore.setState({ snapshot, view: "projects", currentSessionId: "session-1", sessions: [], pullRequestDashboard: null, error: "" });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceOverviewPage />));
    await act(async () => Promise.resolve());
    await act(async () => container.querySelector<HTMLButtonElement>(".workspace-new-thread")!.click());

    expect(execute).toHaveBeenCalledWith({ kind: "new_session", sessionId: "session-1" });
    expect(useRuntimeStore.getState().view).toBe("thread");
  });
});
