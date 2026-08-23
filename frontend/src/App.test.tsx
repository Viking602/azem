import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import App, { isHighPriorityEvent, takeRuntimeEventFrame, toolCompletionRefreshesWorkspace } from "./App";
import { execute } from "./bridge";
import { useRuntimeStore } from "./store";
import { useTerminalStore } from "./terminalStore";
import type { RuntimeEvent, Session, Snapshot } from "./types";
import { readStylesheetTree } from "./testStyles";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });

const prototypeStyles = readFileSync("src/prototype.css", "utf8");
const applicationStyles = readStylesheetTree("src/styles.css");
const conceptStyles = readFileSync("../designs/azem-ui-motion-concept/styles.css", "utf8");
const bridgeRuntime = vi.hoisted(() => ({ listener: null as ((event: RuntimeEvent) => void) | null }));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open(node: HTMLElement) { node.dataset.xterm = "ready"; }
    write() {}
    clear() {}
    focus() {}
    dispose() {}
    loadAddon() {}
    onData() { return { dispose() {} }; }
    onResize() { return { dispose() {} }; }
  },
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class { fit() {} proposeDimensions() { return { cols: 80, rows: 24 }; } dispose() {} },
}));

vi.mock("./bridge", async (importOriginal) => {
  const original = await importOriginal<typeof import("./bridge")>();
  return {
    ...original,
    execute: vi.fn(original.execute),
    subscribe: vi.fn((listener: (event: RuntimeEvent) => void) => {
      bridgeRuntime.listener = listener;
      return () => { if (bridgeRuntime.listener === listener) bridgeRuntime.listener = null; };
    }),
  };
});

let container: HTMLDivElement | null = null;
let root: Root | null = null;

async function dispatchReleaseFirstActivation(target: HTMLElement, detail: number, timeStamp: number) {
  const eventAt = <T extends Event>(event: T, value: number) => {
    Object.defineProperty(event, "timeStamp", { value });
    return event;
  };
  await act(async () => {
    target.dispatchEvent(eventAt(new PointerEvent("pointerup", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail, pointerType: "mouse", isPrimary: true }), timeStamp + 4));
    target.dispatchEvent(eventAt(new MouseEvent("mouseup", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail }), timeStamp + 4));
    target.dispatchEvent(eventAt(new PointerEvent("pointerdown", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail, pointerType: "mouse", isPrimary: true }), timeStamp));
    target.dispatchEvent(eventAt(new MouseEvent("mousedown", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail }), timeStamp));
  });
  await act(async () => new Promise((resolve) => window.setTimeout(resolve, 0)));
}

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  localStorage.clear();
  document.documentElement.style.removeProperty("--ui-font-family");
  document.documentElement.style.removeProperty("--ui-font-size");
  document.documentElement.style.removeProperty("--chat-ui-font-size");
  document.documentElement.style.removeProperty("--chat-code-font-size");
  root = null;
  container = null;
  bridgeRuntime.listener = null;
  vi.mocked(execute).mockClear();
  useTerminalStore.setState({ open: false, height: 260, activeId: "", sessions: [], error: "" });
});

describe("application interactions", () => {
  it("reserves a macOS titlebar safe area before the project switcher", () => {
    expect(prototypeStyles).toMatch(/\.desktop-shell\s*\{[^}]*--mac-titlebar-safe-area:\s*96px;/s);
    expect(prototypeStyles).toMatch(/data-platform\*="mac"[^}]*\.titlebar-project-switch\s*\{[^}]*padding-left:\s*var\(--mac-titlebar-safe-area\);/s);
    expect(prototypeStyles).toMatch(/data-platform\*="mac"[^}]*\.titlebar-project\s*\{[^}]*padding-left:\s*6px;/s);
    expect(conceptStyles).toMatch(/\.project-switch\s*\{[^}]*margin-left:\s*96px;/s);
    expect(conceptStyles).toMatch(/\.project-switch-popover\s*\{[^}]*left:\s*96px;/s);
  });

  it("omits the titlebar command bar and keeps sidebar search plus ⌘K", async () => {
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
      configurable: true,
      value(this: HTMLDialogElement) { this.setAttribute("open", ""); },
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    expect(container.querySelector(".titlebar-command")).toBeNull();
    expect(container.textContent).not.toContain("搜索、跳转或执行命令");
    expect(prototypeStyles).not.toMatch(/\.titlebar-command\s*\{/);
    expect(applicationStyles).not.toMatch(/\.titlebar-command\s*\{/);
    const sidebarSearch = Array.from(container.querySelectorAll<HTMLButtonElement>(".primary-nav button"))
      .find((button) => button.textContent?.includes("搜索"));
    expect(sidebarSearch?.textContent).toContain("⌘K");

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true }));
    });
    expect(useRuntimeStore.getState().commandOpen).toBe(true);
    await act(async () => {
      const started = Date.now();
      while (!container?.querySelector(".command-dialog") && Date.now() - started < 2000) {
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
    });
    expect(container.querySelector(".command-dialog")).not.toBeNull();
  });

  it("renders one 终端 label on the thread header control", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    await act(async () => {
      const snapshot = useRuntimeStore.getState().snapshot;
      useRuntimeStore.setState({
        view: "thread",
        running: false,
        blocks: [{ id: "answer-1", kind: "assistant", content: "已完成" }],
        snapshot: snapshot ? { ...snapshot, language: "zh-CN" } : snapshot,
      });
    });

    const toggle = container.querySelector<HTMLButtonElement>(".thread-header .terminal-toggle");
    expect(toggle).not.toBeNull();
    expect(toggle?.textContent).toBe("终端");
    expect(toggle?.childNodes).toHaveLength(1);
    expect(toggle?.firstChild?.nodeType).toBe(Node.TEXT_NODE);
    expect(toggle?.querySelector(".streaming-text")).toBeNull();
    expect(toggle?.getAttribute("aria-label")).toBeNull();
    expect(toggle?.getAttribute("title")).toBe("打开或收起终端");
    expect(toggle?.getAttribute("aria-pressed")).toBe("false");

    const status = container.querySelector(".thread-header .thread-runtime-status");
    expect(status?.textContent).toBe("就绪");
    expect(toggle?.contains(status)).toBe(false);
    expect(status?.closest(".thread-header-end")).not.toBeNull();
    expect(toggle?.closest(".thread-header-end")).toBe(status?.closest(".thread-header-end"));
    expect(status?.nextElementSibling).toBe(toggle?.closest(".thread-actions"));
  });

  it("toggles the embedded terminal with the primary backtick shortcut", async () => {
    useRuntimeStore.setState({ commandOpen: false, settingsOpen: false });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    expect(container.querySelector(".terminal-panel")).toBeNull();
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "`", code: "Backquote", metaKey: true, bubbles: true }));
    });
    expect(useTerminalStore.getState().open).toBe(true);
    await vi.waitFor(() => expect(container?.querySelector(".terminal-panel")?.getAttribute("data-open")).toBe("true"));
  });

  it("lets long branch names grow across the titlebar", () => {
    expect(prototypeStyles).toMatch(/\.titlebar-project-switch\s*\{[^}]*width:\s*min\(560px,\s*calc\(100% - 24px\)\);/s);
    expect(prototypeStyles).toMatch(/\.titlebar-project\s*\{[^}]*width:\s*fit-content;[^}]*max-width:\s*calc\(100% - 82px\);/s);
    expect(prototypeStyles).toMatch(/\.titlebar-project span\s*\{[^}]*overflow:\s*hidden;[^}]*text-overflow:\s*ellipsis;/s);
  });

  it("keeps the interface font stepper centered inside all three grid columns", () => {
    expect(prototypeStyles).toMatch(/\.appearance-card \.font-size-control > button:not\(\.text-button\),\s*\.appearance-card \.font-size-control output\s*\{[^}]*width:\s*100%;[^}]*height:\s*100%;[^}]*white-space:\s*nowrap;/s);
    expect(prototypeStyles).toMatch(/\.appearance-card \.font-size-control output\s*\{[^}]*border-inline:\s*1px solid var\(--line\);/s);
  });

  it("pins the remaining-provider notice to the directory footer", () => {
    expect(prototypeStyles).toMatch(/\.provider-directory\s*\{[^}]*display:\s*grid;[^}]*grid-template-rows:\s*auto minmax\(0,\s*1fr\) auto;/s);
    expect(prototypeStyles).toMatch(/\.provider-list\s*\{[^}]*min-height:\s*0;[^}]*max-height:\s*none;/s);
    expect(prototypeStyles).toMatch(/\.provider-more\s*\{[^}]*align-self:\s*end;/s);
  });

  it("refreshes session, Git, and model catalogs after initialisation", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    expect(execute).toHaveBeenCalledWith({ kind: "list_sessions" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_git_branches" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_models", sessionId: "session-demo" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_providers", sessionId: "session-demo" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_routes", sessionId: "session-demo" });
    expect(container.textContent).not.toContain("重播流式输出");
    expect(container.textContent).not.toContain("Replay stream");
  });

  it("refreshes Git after every file mutation tool", () => {
    for (const tool of ["coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file", "coding.gofmt"]) {
      expect(toolCompletionRefreshesWorkspace("tool_finished", tool)).toBe(true);
    }
    expect(toolCompletionRefreshesWorkspace("tool_started", "coding.replace")).toBe(false);
    expect(toolCompletionRefreshesWorkspace("tool_finished", "coding.read_file")).toBe(false);
  });

  it("renders the compact reference-led launcher without suggestion cards", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({ view: "thread", blocks: [], running: false }));

    const launchStage = container.querySelector(".empty-launch-stage");
    expect(launchStage?.querySelector(".empty-composer-heading h1")?.textContent).toBe("准备开始什么？");
    expect(launchStage?.querySelector("#azem-composer")).not.toBeNull();
    expect(container.querySelector(".empty-task-suggestions")).toBeNull();
    expect(container.textContent).not.toContain("检查代码改动");
    expect(container.querySelector(".empty-launch-mark")).toBeNull();
    expect(container.querySelector(".thread-header .titlebar-project")).toBeNull();
    expect(container.querySelector(".thread-heading-rule")).toBeNull();
  });

  it("mounts compact plan count above the composer and a Synara-style floating reference panel", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      view: "thread",
      currentSessionId: "session-demo",
      blocks: [
        { id: "user-source", kind: "user", content: "参考 https://example.com/design" },
        { id: "answer-plan", kind: "assistant", content: "主会话保持全宽" },
      ],
      running: false,
      todo: {
        goal: "迁移任务计划",
        revision: 1,
        phases: [{ id: "phase", title: "实现", items: [
          { id: "done", content: "移除右栏", status: "completed" },
          { id: "current", content: "添加输入框支撑栏", status: "in_progress" },
          { id: "next", content: "验证响应式", status: "pending" },
        ] }],
      },
      recap: { sessionId: "session-demo", revision: 4, summary: "回顾摘要", goal: "当前目标", openItems: "未完成事项", updatedAt: "2026-08-23T00:00:00Z" },
    }));

    const thread = container.querySelector(".thread-surface")!;
    const plan = thread.querySelector<HTMLButtonElement>(".composer-stack .thread-plan-control-trigger")!;
    const referenceHost = thread.querySelector(".thread-reference-host")!;
    const reference = referenceHost.querySelector(".thread-reference-card")!;
    const tabs = reference.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    expect(plan.querySelector("svg")).not.toBeNull();
    expect(plan.textContent).toBe("计划1 / 3");
    expect(plan.textContent).not.toContain("添加输入框支撑栏");
    expect(tabs).toHaveLength(2);
    expect(tabs[0]?.textContent).toContain("r4");
    expect(tabs[1]?.textContent).toContain("1");
    expect(reference.parentElement).toBe(referenceHost);
    expect(referenceHost.parentElement).toBe(thread);
    expect(reference.querySelectorAll("[data-thread-reference-resize-edge]")).toHaveLength(8);
    expect(reference.querySelector(".thread-reference-drag")).not.toBeNull();
    expect(thread.querySelector(".composer-workbench")).toBeNull();
    expect(container.querySelector(".workspace-grid")?.getAttribute("data-panel")).toBe("closed");
    expect(container.querySelector(".context-inspector")).toBeNull();

  });

  it("normalizes the new-conversation branch menu across trackpad event orderings", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      view: "thread", blocks: [], running: false,
      branches: [{ name: "main", current: true }, { name: "feature", current: false }],
    }));

    const details = container.querySelector<HTMLDetailsElement>(".composer-branch-menu")!;
    const summary = details.querySelector<HTMLElement>("summary")!;
    await act(async () => summary.click());
    expect(details.open).toBe(true);
    await dispatchReleaseFirstActivation(summary, 2, 500);
    expect(details.open).toBe(false);
    await act(async () => summary.click());
    await act(async () => window.dispatchEvent(new Event("blur")));
    expect(details.open).toBe(false);
    await act(async () => summary.click());
    const currentBranch = details.querySelector<HTMLButtonElement>('[role="option"][aria-selected="true"]')!;
    await act(async () => currentBranch.click());
    expect(details.open).toBe(false);
  });

  it("uses the thread header control for branches and keeps the project label intact", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    await act(async () => useRuntimeStore.setState({
      view: "thread",
      currentTitle: "统一工具调用展示样式",
      blocks: [{ id: "answer-1", kind: "assistant", content: "已完成" }],
    }));

    expect(container.querySelector(".app-titlebar .titlebar-project")).toBeNull();
    const heading = container.querySelector(".thread-heading-copy");
    expect(heading?.querySelector("strong")?.textContent).toBe("统一工具调用展示样式");
    expect(heading?.querySelector(".thread-heading-rule")?.textContent).toBe("|");
    const trigger = container.querySelector<HTMLButtonElement>(".thread-header .titlebar-project");
    expect(trigger?.getAttribute("aria-label")).toBe("切换分支");
    expect(trigger?.getAttribute("title")).toBeNull();
    expect(trigger?.querySelector("strong")?.textContent).toBe("azem");
    expect(heading?.querySelector(".thread-heading-rule")?.nextElementSibling).toBe(trigger?.closest(".titlebar-project-switch"));
    await act(async () => trigger?.click());
    let popover = container.querySelector<HTMLElement>(".titlebar-project-popover");
    expect(popover?.getAttribute("aria-label")).toBe("切换分支");
    expect(popover?.textContent).not.toContain("项目与分支");
    expect(popover?.textContent).not.toContain("llmux");
    expect(popover?.textContent).toContain("feat/usage-store");
    await dispatchReleaseFirstActivation(trigger!, 2, 500);
    expect(container.querySelector(".titlebar-project-popover")).toBeNull();
    await act(async () => trigger?.click());
    popover = container.querySelector<HTMLElement>(".titlebar-project-popover");

    vi.mocked(execute).mockClear();
    const branchOption = Array.from(popover?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
      .find((option) => option.textContent?.includes("feat/usage-store"));
    await act(async () => branchOption?.click());
    expect(execute).toHaveBeenCalledWith({ kind: "switch_git_branch", target: "feat/usage-store", decision: undefined });
  });

  it("keeps long branch catalogs inside a scrollable viewport", () => {
    expect(applicationStyles).toMatch(/\.titlebar-project-popover\s*\{[^}]*max-height:\s*min\(540px,\s*calc\(100vh - 58px\)\);[^}]*overflow:\s*hidden;/s);
    expect(applicationStyles).toMatch(/\.titlebar-project-options\s*\{[^}]*max-height:\s*min\(420px,\s*calc\(100vh - 160px\)\);[^}]*overflow-y:\s*auto;[^}]*overscroll-behavior:\s*contain;/s);
    expect(prototypeStyles).toMatch(/\.thread-heading-copy \.titlebar-project-switch,\s*\.desktop-shell\[data-runtime="true"\]\[data-platform\*="mac" i\] \.thread-heading-copy \.titlebar-project-switch\s*\{[^}]*padding-left:\s*0;/s);
  });

  it("paces streaming text without breaking Unicode or event order", () => {
    const text = `${"a".repeat(1023)}😀${"b".repeat(1100)}`;
    const queue: RuntimeEvent[] = [
      { sequence: 10, kind: "text_delta" as const, runId: "run", text, state: "streaming" },
      { sequence: 11, kind: "run_finished", runId: "run" },
    ];
    const delivered: RuntimeEvent[] = [];

    while (queue.length) delivered.push(...takeRuntimeEventFrame(queue));

    expect(delivered.filter((event) => event.kind === "text_delta").map((event) => event.text).join("")).toBe(text);
    expect(delivered.at(-1)?.kind).toBe("run_finished");
    expect(delivered.slice(0, -2).every((event) => event.sequence === 0)).toBe(true);
    expect(delivered.at(-2)?.sequence).toBe(10);
    expect(delivered[0]?.text?.endsWith("\uD83D")).toBe(false);

    const calm = [{ sequence: 1, kind: "text_delta" as const, text: "x".repeat(100) }];
    const reduced = [{ sequence: 1, kind: "text_delta" as const, text: "x".repeat(3000) }];
    expect(takeRuntimeEventFrame(calm)[0]?.text).toHaveLength(72);
    expect(takeRuntimeEventFrame(reduced, true)[0]?.text).toHaveLength(2048);
  });

  it("classifies interaction and terminal events as high priority", () => {
    expect(isHighPriorityEvent("approval_requested")).toBe(true);
    expect(isHighPriorityEvent("approval_resolved")).toBe(true);
    expect(isHighPriorityEvent("run_finished")).toBe(true);
    expect(isHighPriorityEvent("run_cancelled")).toBe(true);
    expect(isHighPriorityEvent("run_failed")).toBe(true);
    expect(isHighPriorityEvent("text_delta")).toBe(false);
    expect(isHighPriorityEvent("thinking_delta")).toBe(false);
    expect(isHighPriorityEvent("tool_finished")).toBe(false);
    expect(isHighPriorityEvent("session_loaded")).toBe(false);
  });

  it("persists an unread marker when a foreign main run finishes", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<App />));

    const sessions: Session[] = [
      { id: "session-a", workspace: "/tmp/azem", title: "后台任务", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
      { id: "session-b", workspace: "/tmp/azem", title: "当前会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString() },
    ];
    await act(async () => useRuntimeStore.setState({
      sessions, currentSessionId: "session-b", globalRunId: "run-a", globalRunSessionId: "session-a",
    }));
    vi.mocked(execute).mockClear();

    await act(async () => bridgeRuntime.listener?.({
      sequence: 1, kind: "run_finished", sessionId: "session-a", runId: "run-a",
    }));

    expect(execute).toHaveBeenCalledWith({
      kind: "mark_session_unread", target: "session-a", sessionId: "session-a",
    });
    expect(useRuntimeStore.getState().sessions.find((session) => session.id === "session-a")?.unread).toBe(true);
  });

  it("suppresses the browser context menu across the document", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<App />));

    const event = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
    document.body.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
  });

  it("restores and applies the saved global interface typography", async () => {
    localStorage.setItem("azem:ui-font", "Songti SC");
    localStorage.setItem("azem:ui-font-size", "17");
    localStorage.setItem("azem:chat-font-size", "16");
    localStorage.setItem("azem:chat-code-font-size", "14");
    useRuntimeStore.setState({ uiFont: "system", uiFontSize: 14, chatFontSize: 13, chatCodeFontSize: 12 });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));

    expect(useRuntimeStore.getState().uiFont).toBe("Songti SC");
    expect(useRuntimeStore.getState().uiFontSize).toBe(17);
    expect(useRuntimeStore.getState().chatFontSize).toBe(16);
    expect(useRuntimeStore.getState().chatCodeFontSize).toBe(14);
    expect(document.documentElement.style.getPropertyValue("--ui-font-family")).toContain('"Songti SC"');
    expect(document.documentElement.style.getPropertyValue("--ui-font-size")).toBe("17px");
    expect(document.documentElement.style.getPropertyValue("--chat-ui-font-size")).toBe("16px");
    expect(document.documentElement.style.getPropertyValue("--chat-code-font-size")).toBe("14px");
  });

  it("lets the single-line sidebar labels follow the interface font size", () => {
    const desktopBlocks = [...applicationStyles.matchAll(/@media \(min-width: 981px\) \{[\s\S]*?\n\}/g)].map((match) => match[0]);
    expect(desktopBlocks.length).toBeGreaterThan(0);
    for (const block of desktopBlocks) {
      expect(block).not.toMatch(/\.session-copy strong\s*\{[^}]*font-size:\s*\d+px/);
      expect(block).not.toMatch(/\.project-heading-copy strong\s*\{[^}]*font-size:\s*\d+px/);
    }
    expect(applicationStyles).toMatch(/\.session-copy strong\s*\{[^}]*font-size:\s*var\(--text-sm\)/);
    expect(applicationStyles).not.toMatch(/\.session-copy small/);
    expect(applicationStyles).not.toMatch(/\.project-heading-copy small/);
    expect(applicationStyles).not.toMatch(/\.project-initial/);
  });

  it("keeps sidebar session titles on one line with ellipsis and no wrap", () => {
    expect(applicationStyles).toMatch(/\.session-copy strong\s*\{[^}]*overflow:\s*hidden;[^}]*text-overflow:\s*ellipsis;[^}]*white-space:\s*nowrap;/s);
    expect(applicationStyles).not.toMatch(/\.thread-list button \.session-copy\s*\{[^}]*white-space:\s*normal/);
    expect(applicationStyles).not.toMatch(/\.session-copy strong\s*\{[^}]*white-space:\s*normal/);
    expect(applicationStyles).not.toMatch(/\.session-copy strong\s*\{[^}]*overflow-wrap:\s*anywhere/);
  });

  it("keeps the thread mounted while subagents open in a drawer", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    const agent = {
        id: "agent-1", type: "worker", description: "检查界面", model: "gpt-5.6-sol",
        background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
        activity: "", warning: "", evidenceStatus: "verified", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 20,
        elapsedMs: 1000, state: "completed", summary: "已完成", preview: "已完成",
        previewKind: "assistant", previewRunId: "child-1", elapsedObservedAt: Date.now(),
      } as const;
    const runningAgent = {
      ...agent,
      id: "agent-2",
      type: "review",
      description: "审查交互层级",
      state: "running",
      preview: "正在审查交互层级",
      previewRunId: "child-2",
    } as const;
    const queuedAgent = {
      ...agent,
      id: "agent-3",
      type: "explore",
      description: "验证抽屉信息流",
      state: "queued",
      preview: "等待执行",
      previewRunId: "child-3",
    } as const;
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 20)));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      view: "agents",
      blocks: [{ id: "answer-1", kind: "assistant", content: "主会话仍然可见" }],
      selectedAgentId: "",
      agents: [agent, runningAgent, queuedAgent],
    }));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    expect(useRuntimeStore.getState().view).toBe("agents");
    expect(useRuntimeStore.getState().selectedAgentId).toBe("");
    expect(useRuntimeStore.getState().agents).toHaveLength(3);
    expect(container.querySelector(".thread-surface")).not.toBeNull();
    await vi.waitFor(() => expect(container?.querySelector(".subagents-drawer-layer")).not.toBeNull());
    expect(container.querySelectorAll(".subagent-group")).toHaveLength(3);
    expect(container.textContent).toContain("运行中");
    expect(container.textContent).toContain("排队中");
    expect(container.textContent).toContain("已结束");
    expect(container.querySelector(".subagent-evidence-status")?.textContent).toBe("证据已验证");
    await act(async () => container?.querySelector<HTMLButtonElement>(".subagent-row > button")?.click());
    await vi.waitFor(() => expect(container?.querySelector(".subagents-drawer-layer")).toBeNull());
    await vi.waitFor(() => expect(container?.querySelector(".subagent-detail-drawer-layer")).not.toBeNull());
    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    expect(container.querySelector(".workspace-grid")?.getAttribute("data-panel")).not.toBe("agent");
    expect(container.querySelector(".agent-side-chat .subagent-evidence-status")?.textContent).toBe("证据已验证");
    const agentTabs = [...container.querySelectorAll<HTMLButtonElement>(".agent-side-chat-tabs button")];
    expect(agentTabs).toHaveLength(3);
    expect(agentTabs.every((button) => button.textContent === "" && !button.title && Boolean(button.getAttribute("aria-label")))).toBe(true);
    await act(async () => container?.querySelector<HTMLButtonElement>(".agent-side-chat-actions button:last-child")?.click());
    await vi.waitFor(() => expect(container?.querySelector(".subagents-drawer-layer")).not.toBeNull());
    await act(async () => container?.querySelector<HTMLDivElement>(".subagents-drawer-layer")?.click());
    expect(useRuntimeStore.getState().view).toBe("thread");
  });


  it("renders a completed subagent transcript like the main conversation and folds only its process trail", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    const agent = {
      id: "agent-finished", type: "review", description: "审查安全边界", model: "gpt-5.6-sol",
      background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
      activity: "", warning: "", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 860,
      elapsedMs: 4_200, state: "completed", summary: "审查完成", preview: "审查完成",
      previewKind: "assistant", previewRunId: "child-finished", elapsedObservedAt: Date.now(),
    } as const;
    const agentBlocks = [
      { id: "child-user", kind: "user", runId: "child-finished", content: "只报告已经验证的安全 finding。", state: "completed" },
      { id: "child-progress", kind: "commentary", runId: "child-finished", content: "**核对安全边界**\n检查审批与外部副作用", state: "completed" },
      { id: "child-tool", kind: "tool", runId: "child-finished", title: "coding.search", content: "ok", state: "completed" },
      { id: "child-answer", kind: "assistant", runId: "child-finished", content: "## Verdict\n\n未发现达到门槛的 finding。", state: "completed" },
    ] as const;

    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      view: "thread",
      currentSessionId: "s1",
      blocks: [{ id: "main-answer", kind: "assistant", content: "主会话" }],
      selectedAgentId: agent.id,
      agents: [agent],
      agentBlocks: [...agentBlocks],
    }));

    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    const transcript = container!.querySelector(".agent-side-chat-transcript");
    const process = transcript?.querySelector('.process-fold[data-state="completed"]');
    const user = transcript?.querySelector(".user-block");
    const answer = transcript?.querySelector(".assistant-block");

    expect(transcript?.classList.contains("transcript")).toBe(true);
    expect(container?.querySelector(".agent-side-chat-feed-title")).toBeNull();
    expect(container?.querySelector(".agent-side-chat-brief")).toBeNull();
    expect(container?.querySelector(".agent-side-chat-meta")).toBeNull();
    expect(user?.closest(".process-fold")).toBeNull();
    expect(answer?.closest(".process-fold")).toBeNull();
    expect(process?.getAttribute("data-folded")).toBe("true");
    expect(process?.querySelector(".commentary-block")).toBeNull();
    expect(process?.querySelector(".timeline-step")).toBeNull();



  });

  it("shows live thinking in a running subagent drawer instead of a bare 运行中 body", async () => {

    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    const agent = {
      id: "agent-think", type: "review", description: "审查架构边界", model: "gpt-5.6-sol",
      background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
      activity: "先核对模块边界", warning: "", worktreePath: "", toolCalls: 0, turns: 1, tokensUsed: 80,
      elapsedMs: 8_000, state: "running", summary: "", preview: "先核对模块边界",
      previewKind: "thinking", previewRunId: "child-think", elapsedObservedAt: Date.now(),
    } as const;
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      view: "thread",
      currentSessionId: "s1",
      blocks: [{ id: "main-answer", kind: "assistant", content: "主会话" }],
      selectedAgentId: agent.id,
      agents: [agent],
      agentBlocks: [{
        id: "child-thinking", kind: "thinking", runId: "child-think", title: "思考",
        content: "先核对模块边界", state: "streaming",
      }],
    }));

    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    const drawer = container!.querySelector(".agent-side-chat")!;
    expect(drawer.querySelector(".agent-side-chat-empty")).toBeNull();
    expect(drawer.textContent).toContain("运行中");
    expect(drawer.textContent).toContain("先核对模块边界");
    expect(drawer.querySelector(".reasoning-placeholder")).toBeNull();
    expect(drawer.querySelector(".aui-reasoning-panel, .reasoning-summary, [data-testid='timeline-prose']")).not.toBeNull();
  });

  it("shows the thinking wait pill while a running subagent has no tokens yet", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    const agent = {
      id: "agent-wait", type: "review", description: "审查安全边界", model: "gpt-5.6-sol",
      background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
      activity: "", warning: "", worktreePath: "", toolCalls: 0, turns: 0, tokensUsed: 0,
      elapsedMs: 12_000, state: "running", summary: "", preview: "",
      previewKind: "", previewRunId: "child-wait", elapsedObservedAt: Date.now(),
    } as const;
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      view: "thread",
      currentSessionId: "s1",
      blocks: [{ id: "main-answer", kind: "assistant", content: "主会话" }],
      selectedAgentId: agent.id,
      agents: [agent],
      agentBlocks: [],
    }));

    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    const drawer = container!.querySelector(".agent-side-chat")!;
    expect(drawer.querySelector(".agent-side-chat-empty")).toBeNull();
    // SUBAGENT-005: the wait is the running step's own bar, not a bare 运行中.
    expect(drawer.querySelector(".process-fold .aui-reasoning-panel.streaming")).not.toBeNull();
    expect(drawer.textContent).toContain("正在思考");
  });

  it("reloads the open subagent drawer after a projection resync", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      currentSessionId: "s1",
      selectedAgentId: "agent-1",
      agentBlocks: [{ id: "think-1", kind: "thinking", runId: "child-1", content: "先看 diff" }],
      agents: [{
        id: "agent-1", type: "review", description: "审查", model: "gpt-5.6-sol",
        background: false, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
        activity: "", warning: "", worktreePath: "", toolCalls: 0, turns: 0, tokensUsed: 0,
        elapsedMs: 1000, state: "running", summary: "", preview: "正在思考",
        previewKind: "thinking", previewRunId: "child-1", elapsedObservedAt: Date.now(),
      }],
    }));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 20)));
    await act(async () => {
      bridgeRuntime.listener?.({
        sequence: 9, kind: "projection_resync", sessionId: "s1", runId: "child-1",
        agentId: "agent-1", state: "degraded",
      });
    });
    await vi.waitFor(() => {
      expect(execute).toHaveBeenCalledWith({ kind: "refresh_session", target: "s1", sessionId: "s1" });
      expect(execute).toHaveBeenCalledWith({ kind: "inspect_agent", target: "agent-1", sessionId: "s1" });
    });
    await act(async () => {
      bridgeRuntime.listener?.({
        sequence: 10, kind: "session_loaded", sessionId: "s1", state: "refreshed",
        data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
      });
    });
    expect(useRuntimeStore.getState().selectedAgentId).toBe("agent-1");
    expect(useRuntimeStore.getState().agentBlocks[0]).toMatchObject({ content: "先看 diff" });
    await act(async () => {
      bridgeRuntime.listener?.({
        sequence: 11, kind: "agent_detail", agentId: "agent-1", state: "detail",
        agentBlocks: [{ id: "think-1", kind: "thinking", runId: "child-1", content: "先看 diff" }],
      });
    });
    expect(useRuntimeStore.getState().agentBlocks[0]).toMatchObject({ content: "先看 diff" });
    expect(container.querySelector(".agent-side-chat")).not.toBeNull();
  });
});
