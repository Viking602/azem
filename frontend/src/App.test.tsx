import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import App, { isHighPriorityEvent, takeRuntimeEventFrame } from "./App";
import { execute } from "./bridge";
import { useRuntimeStore } from "./store";
import type { RuntimeEvent, Session, Snapshot } from "./types";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });

const prototypeStyles = readFileSync("src/prototype.css", "utf8");
const applicationStyles = readFileSync("src/styles.css", "utf8");
const conceptStyles = readFileSync("../designs/azem-ui-motion-concept/styles.css", "utf8");
const bridgeRuntime = vi.hoisted(() => ({ listener: null as ((event: RuntimeEvent) => void) | null }));

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

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  localStorage.clear();
  document.documentElement.style.removeProperty("--ui-font-family");
  document.documentElement.style.removeProperty("--ui-font-size");
  root = null;
  container = null;
  bridgeRuntime.listener = null;
  vi.mocked(execute).mockClear();
});

describe("application interactions", () => {
  it("reserves a macOS titlebar safe area before the project switcher", () => {
    expect(prototypeStyles).toMatch(/\.desktop-shell\s*\{[^}]*--mac-titlebar-safe-area:\s*96px;/s);
    expect(prototypeStyles).toMatch(/data-platform\*="mac"[^}]*\.titlebar-project-switch\s*\{[^}]*padding-left:\s*var\(--mac-titlebar-safe-area\);/s);
    expect(prototypeStyles).toMatch(/data-platform\*="mac"[^}]*\.titlebar-project\s*\{[^}]*padding-left:\s*6px;/s);
    expect(conceptStyles).toMatch(/\.project-switch\s*\{[^}]*margin-left:\s*96px;/s);
    expect(conceptStyles).toMatch(/\.project-switch-popover\s*\{[^}]*left:\s*96px;/s);
  });

  it("centers the command trigger against the full titlebar", () => {
    expect(prototypeStyles).toMatch(/\.titlebar-command\s*\{[^}]*position:\s*absolute;[^}]*left:\s*50%;[^}]*transform:\s*translateX\(-50%\);/s);
    expect(conceptStyles).toMatch(/\.command-trigger\s*\{[^}]*position:\s*absolute;[^}]*left:\s*50%;[^}]*transform:\s*translateX\(-50%\);/s);
    expect(conceptStyles).toMatch(/\.command-trigger:hover\s*\{[^}]*transform:\s*translate\(-50%,\s*-1px\);/s);
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

  it("keeps the empty launcher title without a logo", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => useRuntimeStore.setState({ view: "thread", blocks: [], running: false }));

    expect(container.querySelector(".empty-composer-heading h1")?.textContent).toBe("准备开始什么？");
    expect(container.querySelector(".empty-launch-mark")).toBeNull();
  });

  it("uses the titlebar control for branches and keeps the project label intact", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    const trigger = container.querySelector<HTMLButtonElement>(".titlebar-project");
    expect(trigger?.getAttribute("aria-label")).toBe("切换分支");
    expect(trigger?.querySelector("strong")?.textContent).toBe("azem");

    await act(async () => trigger?.click());
    const popover = container.querySelector<HTMLElement>(".titlebar-project-popover");
    expect(popover?.getAttribute("aria-label")).toBe("切换分支");
    expect(popover?.textContent).not.toContain("项目与分支");
    expect(popover?.textContent).not.toContain("llmux");
    expect(popover?.textContent).toContain("feat/usage-store");

    vi.mocked(execute).mockClear();
    const branchOption = Array.from(popover?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
      .find((option) => option.textContent?.includes("feat/usage-store"));
    await act(async () => branchOption?.click());
    expect(execute).toHaveBeenCalledWith({ kind: "switch_git_branch", target: "feat/usage-store", decision: undefined });
  });

  it("keeps long branch catalogs inside a scrollable viewport", () => {
    expect(applicationStyles).toMatch(/\.titlebar-project-popover\s*\{[^}]*max-height:\s*min\(540px,\s*calc\(100vh - 58px\)\);[^}]*overflow:\s*hidden;/s);
    expect(applicationStyles).toMatch(/\.titlebar-project-options\s*\{[^}]*max-height:\s*min\(420px,\s*calc\(100vh - 160px\)\);[^}]*overflow-y:\s*auto;[^}]*overscroll-behavior:\s*contain;/s);
  });

  it("paces streaming text without breaking Unicode or event order", () => {
    const text = `${"a".repeat(1023)}😀${"b".repeat(1100)}`;
    const queue: RuntimeEvent[] = [
      { sequence: 10, kind: "text_delta", runId: "run", text, state: "streaming" },
      { sequence: 11, kind: "run_finished", runId: "run" },
    ];
    const delivered: RuntimeEvent[] = [];

    while (queue.length) delivered.push(...takeRuntimeEventFrame(queue));

    expect(delivered.filter((event) => event.kind === "text_delta").map((event) => event.text).join("")).toBe(text);
    expect(delivered.at(-1)?.kind).toBe("run_finished");
    expect(delivered.slice(0, -2).every((event) => event.sequence === 0)).toBe(true);
    expect(delivered.at(-2)?.sequence).toBe(10);
    expect(delivered[0]?.text?.endsWith("\uD83D")).toBe(false);

    const calm = [{ sequence: 1, kind: "text_delta", text: "x".repeat(100) }];
    const reduced = [{ sequence: 1, kind: "text_delta", text: "x".repeat(3000) }];
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
    useRuntimeStore.setState({ uiFont: "system", uiFontSize: 14 });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));

    expect(useRuntimeStore.getState().uiFont).toBe("Songti SC");
    expect(useRuntimeStore.getState().uiFontSize).toBe(17);
    expect(document.documentElement.style.getPropertyValue("--ui-font-family")).toContain('"Songti SC"');
    expect(document.documentElement.style.getPropertyValue("--ui-font-size")).toBe("17px");
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
        activity: "", warning: "", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 20,
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
    await act(async () => container?.querySelector<HTMLButtonElement>(".subagent-row > button")?.click());
    await vi.waitFor(() => expect(container?.querySelector(".subagents-drawer-layer")).toBeNull());
    await vi.waitFor(() => expect(container?.querySelector(".subagent-detail-drawer-layer")).not.toBeNull());
    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    expect(container.querySelector(".workspace-grid")?.getAttribute("data-inspector")).not.toBe("agent");
    const agentTabs = [...container.querySelectorAll<HTMLButtonElement>(".agent-side-chat-tabs button")];
    expect(agentTabs).toHaveLength(3);
    expect(agentTabs.every((button) => button.textContent === "" && Boolean(button.title) && Boolean(button.getAttribute("aria-label")))).toBe(true);
    await act(async () => container?.querySelector<HTMLButtonElement>(".agent-side-chat-actions button:last-child")?.click());
    await vi.waitFor(() => expect(container?.querySelector(".subagents-drawer-layer")).not.toBeNull());
    await act(async () => container?.querySelector<HTMLDivElement>(".subagents-drawer-layer")?.click());
    expect(useRuntimeStore.getState().view).toBe("thread");
  });

  it("expands the inspector roster before opening a subagent conversation drawer", async () => {
    const snapshot: Snapshot = {
      workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
      reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
      queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
    };
    const agent = {
      id: "agent-direct", type: "review", description: "审查前端改动", model: "gpt-5.6-sol",
      background: true, capabilityMode: "read-only", isolation: "none", cwd: "/tmp/azem",
      activity: "正在核对交互状态", warning: "", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 20,
      elapsedMs: 1000, state: "running", summary: "", preview: "正在核对交互状态",
      previewKind: "thinking", previewRunId: "child-direct", elapsedObservedAt: Date.now(),
    } as const;
    useRuntimeStore.setState({ snapshot });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<App />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 20)));
    await act(async () => useRuntimeStore.setState({
      snapshot,
      view: "thread",
      currentSessionId: "s1",
      blocks: [{ id: "answer-direct", kind: "assistant", content: "主会话" }],
      inspectorOpen: true,
      selectedAgentId: "",
      agents: [agent],
    }));

    await vi.waitFor(() => expect(container?.querySelector(".subagent-summary-button")).not.toBeNull());
    const summary = container!.querySelector<HTMLButtonElement>(".subagent-summary-button")!;
    const inspectorList = container!.querySelector<HTMLDivElement>(".inspector-subagent-list")!;
    expect(summary.getAttribute("aria-expanded")).toBe("false");
    expect(inspectorList.hidden).toBe(true);
    await act(async () => summary.click());
    expect(summary.getAttribute("aria-expanded")).toBe("true");
    expect(inspectorList.hidden).toBe(false);

    await act(async () => container?.querySelector<HTMLButtonElement>(".inspector-subagent-row")?.click());
    await vi.waitFor(() => expect(container?.querySelector(".subagent-detail-drawer-layer")).not.toBeNull());
    await vi.waitFor(() => expect(container?.querySelector(".agent-side-chat")).not.toBeNull());
    expect(useRuntimeStore.getState()).toMatchObject({ view: "thread", selectedAgentId: "agent-direct" });
    expect(execute).toHaveBeenCalledWith({ kind: "inspect_agent", target: "agent-direct", sessionId: "s1" });
    await act(async () => useRuntimeStore.setState({
      agentBlocks: [{
        id: "child-progress", kind: "commentary", runId: "child-direct", title: "progress",
        content: "正在核对交互状态", state: "completed", data: { elapsedMs: "1000" },
      }],
    }));
    expect(container?.querySelector(".agent-side-chat .process-fold-label")?.textContent).toBe("处理中");
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
    const process = transcript?.querySelector<HTMLDetailsElement>('.process-fold[data-state="completed"]');
    const user = transcript?.querySelector(".user-block");
    const answer = transcript?.querySelector(".assistant-block");

    expect(transcript?.classList.contains("transcript")).toBe(true);
    expect(container?.querySelector(".agent-side-chat-feed-title")).toBeNull();
    expect(container?.querySelector(".agent-side-chat-brief")).toBeNull();
    expect(container?.querySelector(".agent-side-chat-meta")).toBeNull();
    expect(user?.closest(".process-fold")).toBeNull();
    expect(answer?.closest(".process-fold")).toBeNull();
    expect(process?.open).toBe(false);
    expect(process?.querySelector(".process-fold-label")?.textContent).toBe("已处理");

    await act(async () => process?.querySelector<HTMLElement>("summary")?.click());
    expect(process?.open).toBe(true);
    expect(process?.textContent).toContain("核对安全边界");
  });
});
