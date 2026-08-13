import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listWorkspaceEntries, readWorkspaceFile } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot, WorkspaceDirectory, WorkspaceFile } from "../types";
import WorkspaceFilesPage from "./WorkspaceFilesPage";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class TestResizeObserver {
  observe() {}
  disconnect() {}
}
globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;

vi.mock("../bridge", () => ({
  isDesktopRuntime: () => true,
  listWorkspaceEntries: vi.fn(),
  readWorkspaceFile: vi.fn(),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

const rootDirectory: WorkspaceDirectory = {
  path: "",
  entries: [
    { name: "frontend", path: "frontend", directory: true, size: 0 },
    { name: "README.md", path: "README.md", directory: false, size: 20 },
  ],
};

let container: HTMLDivElement;
let root: Root;

afterEach(async () => {
  await act(async () => root?.unmount());
  container?.remove();
  vi.clearAllMocks();
});

describe("WorkspaceFilesPage", () => {
  it("loads directories lazily and previews a selected file", async () => {
    vi.mocked(listWorkspaceEntries).mockImplementation(async (path) => path === "frontend" ? {
      path,
      entries: [{ name: "package.json", path: "frontend/package.json", directory: false, size: 42 }],
    } : rootDirectory);
    const file: WorkspaceFile = {
      path: "frontend/package.json", name: "package.json", kind: "text", language: "json",
      content: "{\n  \"name\": \"azem\"\n}\n", size: 42, lineCount: 3,
    };
    vi.mocked(readWorkspaceFile).mockResolvedValue(file);
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceFilesPage />));
    await act(async () => Promise.resolve());
    expect(listWorkspaceEntries).toHaveBeenCalledWith("");
    expect(container.textContent).toContain("README.md");

    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".workspace-tree-row")).find((button) => button.textContent?.includes("frontend"))!.click());
    expect(listWorkspaceEntries).toHaveBeenCalledWith("frontend");
    expect(container.textContent).toContain("package.json");
    expect(container.querySelector('.workspace-tree-row [data-file-icon="npm"]')).not.toBeNull();
    const packageRow = Array.from(container.querySelectorAll<HTMLButtonElement>(".workspace-tree-row")).find((button) => button.textContent?.includes("package.json"))!;
    expect(packageRow.querySelector(":scope > .file-type-icon + .tree-entry-name")?.textContent).toBe("package.json");

    await act(async () => packageRow.click());
    expect(readWorkspaceFile).toHaveBeenCalledWith("frontend/package.json");
    expect(container.querySelector(".workspace-active-file")?.textContent).toContain("package.json");
    expect(container.textContent).toContain('"name": "azem"');
    expect(container.querySelector(".workspace-code-line .syntax-string")?.textContent).toBe('"name"');
  });

  it("shows the binary state instead of rendering arbitrary bytes", async () => {
    vi.mocked(listWorkspaceEntries).mockResolvedValue(rootDirectory);
    vi.mocked(readWorkspaceFile).mockResolvedValue({ path: "README.md", name: "README.md", kind: "binary", size: 20 });
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<WorkspaceFilesPage />));
    await act(async () => Promise.resolve());
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>(".workspace-tree-row")).find((button) => button.textContent?.includes("README.md"))!.click());

    expect(container.textContent).toContain("无法预览二进制文件");
  });
});
