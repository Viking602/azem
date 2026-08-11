import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listWorkspaceChanges, readWorkspaceChange } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot, WorkspaceChangeSet } from "../types";
import WorkspaceChangesPage, { buildChangeTree, parsePatch } from "./WorkspaceChangesPage";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => undefined });

vi.mock("../bridge", () => ({
  isDesktopRuntime: vi.fn(() => true),
  listWorkspaceChanges: vi.fn(),
  readWorkspaceChange: vi.fn(),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

const changes: WorkspaceChangeSet = {
  repository: true, branch: "main", base: "HEAD", additions: 3, deletions: 1,
  files: [
    { path: "frontend/src/App.tsx", status: "modified", additions: 1, deletions: 1 },
    { path: "internal/desktop/workspace_changes.go", status: "untracked", additions: 2, deletions: 0 },
  ],
};

let container: HTMLDivElement;
let root: Root;

afterEach(async () => {
  await act(async () => root?.unmount());
  container?.remove();
  vi.clearAllMocks();
});

describe("WorkspaceChangesPage", () => {
  it("opens the first patch lazily and navigates the changed-file tree", async () => {
    vi.mocked(listWorkspaceChanges).mockResolvedValue(changes);
    vi.mocked(readWorkspaceChange).mockImplementation(async (path) => ({
      ...changes.files.find((file) => file.path === path)!,
      patch: `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -2,2 +2,2 @@\n-old value\n+new value\n context`,
    }));
    useRuntimeStore.setState({ snapshot, view: "changes", error: "" });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceChangesPage />));
    await act(async () => Promise.resolve());
    expect(listWorkspaceChanges).toHaveBeenCalledTimes(1);
    expect(readWorkspaceChange).toHaveBeenCalledWith("frontend/src/App.tsx");
    expect(container.textContent).toContain("new value");
    expect(container.textContent).toContain("+3");
    expect(container.textContent).toContain("−1");
    expect(container.textContent).not.toContain("@@");
    expect(container.querySelector(".patch-hunk-heading")).toBeNull();

    const secondFile = container.querySelector<HTMLButtonElement>('.review-file-item[data-path="internal/desktop/workspace_changes.go"]')!;
    await act(async () => secondFile.click());
    expect(readWorkspaceChange).toHaveBeenCalledWith("internal/desktop/workspace_changes.go");
    expect(secondFile.classList.contains("active")).toBe(true);
  });

  it("collapses directories and the review list when a workspace has many files", async () => {
    const many: WorkspaceChangeSet = {
      repository: true, branch: "main", base: "HEAD", additions: 80, deletions: 0,
      files: Array.from({ length: 80 }, (_, index) => ({ path: `generated/part-${index}.go`, status: "modified" as const, additions: 1, deletions: 0 })),
    };
    vi.mocked(listWorkspaceChanges).mockResolvedValue(many);
    vi.mocked(readWorkspaceChange).mockResolvedValue({ ...many.files[0]!, patch: "@@ -1 +1 @@\n-old\n+new" });
    useRuntimeStore.setState({ snapshot, view: "changes", error: "" });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceChangesPage />));
    await act(async () => Promise.resolve());

    expect(container.querySelectorAll(".review-file-item")).toHaveLength(60);
    expect(container.textContent).toContain("其余 20 个文件已折叠");
  });

  it("parses multiple hunks so unchanged ranges can stay folded", () => {
    const hunks = parsePatch("@@ -2,2 +2,2 @@\n-old\n+new\n context\n@@ -10,2 +10,3 @@\n next\n+added");
    expect(hunks).toHaveLength(2);
    expect(hunks[0]?.lines.map((line) => line.kind)).toEqual(["deleted", "added", "context"]);
    expect(hunks[1]?.oldStart - ((hunks[0]?.oldStart ?? 0) + (hunks[0]?.oldCount ?? 0))).toBe(6);
  });

  it("builds directory counts without eagerly flattening every path", () => {
    const tree = buildChangeTree(changes.files);
    expect(tree.directories.get("frontend")?.count).toBe(1);
    expect(tree.directories.get("internal")?.directories.get("desktop")?.files[0]?.path).toBe("internal/desktop/workspace_changes.go");
  });
});
