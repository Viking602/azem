import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { reduceEvents, type RuntimeData, useRuntimeStore } from "./store";
import type { Snapshot } from "./types";
import Inspector from "./components/Inspector";
import ThreadSurface, { approvalPresentation, ContextMeter, contextOccupancy, formatDuration } from "./components/ThreadSurface";
import { toolDisplayName } from "./i18n";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

function state(): RuntimeData {
  return {
    snapshot, sessions: [], currentSessionId: "s1", currentTitle: "", blocks: [], agents: [], backgroundProcesses: [], selectedAgentId: "", agentBlocks: [], agentCatalog: [],
    skills: [], branches: [], pullRequestDashboard: null, selectedPullRequestNumber: null, pullRequestDetail: null,
    pullRequestMonitors: new Map(), pullRequestLoading: false, pullRequestMutating: false, pullRequestError: "",
    modelRoutes: [], modelsByProvider: {}, contextProfile: null,
    contextUsage: { inputTokens: 0, outputTokens: 0, contextLimit: 0, reported: false }, todo: null, recovery: [],
    runId: "", running: false, runStartedAt: 0, activity: "", approvalMode: "prompt", workspaceDirty: false,
    workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
    lastSequence: 0, error: "", view: "thread", inspectorTab: "environment", inspectorOpen: true,
    settingsOpen: false, commandOpen: false, planMode: false, attachments: [], queuedPrompts: [], theme: "system",
  };
}

describe("runtime event projection", () => {
  it("keeps bootstrap events emitted before initialise returns", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().hydrate({ ...snapshot, sequence: 6 });
    useRuntimeStore.getState().applyEvents([{
      sequence: 4,
      kind: "model_routes",
      modelRoutes: [{ Scope: "plan", Role: "", Label: "Plan", Route: {} }],
    }]);
    expect(useRuntimeStore.getState().modelRoutes).toHaveLength(1);
  });

  it("isolates background automation events from the visible session", () => {
    const current: RuntimeData = { ...state(), running: true, runId: "visible-run", blocks: [{ id: "tool-1", kind: "tool", state: "running", content: "", title: "visible tool" }] };
    const projected = reduceEvents(current, [
      { sequence: 1, kind: "run_started", sessionId: "repair-session", runId: "repair-run" },
      { sequence: 2, kind: "text_delta", sessionId: "repair-session", runId: "repair-run", text: "background output" },
      { sequence: 3, kind: "run_finished", sessionId: "repair-session", runId: "repair-run" },
    ]);
    expect(projected.currentSessionId).toBe("s1");
    expect(projected.runId).toBe("visible-run");
    expect(projected.running).toBe(true);
    expect(projected.blocks).toEqual(current.blocks);
    expect(projected.lastSequence).toBe(3);
  });

  it("shows tool arguments while running and settles unfinished tools when the run ends", () => {
    const started = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      {
        sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", state: "running",
        data: { name: "coding.read_file", arguments: JSON.stringify({ path: "frontend/src/App.tsx", limit: 40 }) },
      },
      {
        sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "search-1", state: "running",
        data: { name: "coding.search", arguments: JSON.stringify({ query: "MenuSelect", path: "frontend/src" }) },
      },
    ]);
    expect(started.blocks[0]).toMatchObject({
      kind: "tool", toolCallId: "read-1", state: "running", title: "coding.read_file",
    });
    expect(started.blocks[0]?.content).toContain("frontend/src/App.tsx");
    expect(started.blocks[1]?.content).toContain("MenuSelect");

    const finished = reduceEvents(started, [
      { sequence: 4, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "export default function App" },
      { sequence: 5, kind: "run_finished", runId: "r1" },
    ]);
    expect(finished.blocks.find((block) => block.toolCallId === "read-1")).toMatchObject({ state: "completed" });
    // Search never finished; run end must not leave it spinning forever.
    expect(finished.blocks.find((block) => block.toolCallId === "search-1")).toMatchObject({ state: "completed" });
    expect(finished.running).toBe(false);
  });

  it("keeps growing subagent tool and elapsed counters across sparse agent_state updates", () => {
    const projected = reduceEvents(state(), [
      {
        sequence: 1, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", model: "gpt-5.6-luna", capabilityMode: "read-only", toolCalls: 2, turns: 1, tokensUsed: 100, elapsedMs: 5000, activity: "coding.read_file" },
      },
      // Sparse lifecycle event without counters must not reset stats to zero.
      {
        sequence: 2, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", activity: "coding.search" },
      },
      {
        sequence: 3, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", toolCalls: 5, elapsedMs: 12000, activity: "coding.git_diff" },
      },
    ]);
    expect(projected.agents[0]).toMatchObject({
      id: "a1", state: "running", toolCalls: 5, elapsedMs: 12000, activity: "coding.git_diff", model: "gpt-5.6-luna",
    });
  });

  it("streams subagent frames into the side chat instead of the main feed", () => {
    const base = { ...state(), selectedAgentId: "agent-1" };
    const projected = reduceEvents(base, [
      { sequence: 1, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "先看 diff" },
      { sequence: 2, kind: "tool_started", runId: "child-1", agentId: "agent-1", toolCallId: "c1", data: { name: "coding.git_diff" }, state: "running" },
      { sequence: 3, kind: "tool_finished", runId: "child-1", agentId: "agent-1", toolCallId: "c1", text: "ok", state: "completed" },
      { sequence: 4, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "结论" },
      // Unrelated main-run frame still goes to the main transcript.
      { sequence: 5, kind: "thinking_delta", runId: "main-1", text: "主会话思考" },
    ]);
    expect(projected.blocks).toHaveLength(1);
    expect(projected.blocks[0]).toMatchObject({ kind: "thinking", content: "主会话思考" });
    expect(projected.agentBlocks.map((block) => block.kind)).toEqual(["thinking", "tool", "assistant"]);
    expect(projected.agentBlocks[0]).toMatchObject({ kind: "thinking", title: "思考", content: "先看 diff" });
    expect(projected.agentBlocks[1]).toMatchObject({ kind: "tool", title: "coding.git_diff", state: "completed" });
    expect(projected.agentBlocks[2]).toMatchObject({ kind: "assistant", content: "结论" });
  });

  it("ignores other agents' live frames while a different side chat is open", () => {
    const projected = reduceEvents({ ...state(), selectedAgentId: "agent-a" }, [
      { sequence: 1, kind: "thinking_delta", runId: "c", agentId: "agent-b", text: "不该出现" },
    ]);
    expect(projected.agentBlocks).toEqual([]);
    expect(projected.blocks).toEqual([]);
  });

  it("separates discrete thinking markdown segments instead of gluing **A****B**", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "**Planning analysis**" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "**Inspecting files**" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: " mid-sentence" },
    ]);
    expect(projected.blocks[0]?.content).toBe("**Planning analysis**\n\n**Inspecting files** mid-sentence");
  });

  it("coalesces streaming deltas and ignores replayed sequence numbers", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "先检查" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: "事件模型" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: "重复" },
    ]);
    expect(projected.running).toBe(true);
    expect(projected.blocks).toHaveLength(1);
    expect(projected.blocks[0]?.content).toBe("先检查事件模型");
  });

  it("stops the thinking animation when the run finishes", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "检查状态" },
      { sequence: 3, kind: "run_finished", runId: "r1" },
    ]);
    expect(projected.running).toBe(false);
    expect(projected.blocks[0]).toMatchObject({ kind: "thinking", runId: "r1", state: "completed" });
  });

  it("keeps approval beside the tool timeline and resolves it in place", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "approval_requested", approvalId: "a1", toolCallId: "t1", text: "write file" },
      { sequence: 2, kind: "approval_resolved", approvalId: "a1", state: "approved" },
    ]);
    expect(projected.blocks[0]).toMatchObject({ kind: "approval", approvalId: "a1", state: "approved" });
  });

  it("hides automatic approval reviews and keeps the later user prompt", () => {
    const reviewed = reduceEvents(state(), [
      { sequence: 1, kind: "approval_requested", approvalId: "a1", state: "reviewing", text: "{\"command\":\"git status\"}" },
      { sequence: 2, kind: "approval_resolved", approvalId: "a1", state: "auto_approved" },
    ]);
    expect(reviewed.blocks).toHaveLength(0);
    const prompted = reduceEvents(reviewed, [{ sequence: 3, kind: "approval_requested", approvalId: "a1", state: "pending", data: { tool: "coding.shell", target: "git status", effect: "external_side_effect", risk: "high" } }]);
    expect(prompted.blocks[0]).toMatchObject({ kind: "approval", approvalId: "a1", state: "pending" });
  });

  it("builds approval UI fields without exposing the structured payload", () => {
    const details = approvalPresentation({ id: "a1", kind: "approval", content: "{\"command\":\"secret raw payload\"}", data: { tool: "coding.shell", target: "git status --short", effect: "external_side_effect", risk: "high" } }, "zh-CN");
    expect(details).toMatchObject({ tool: "运行命令", target: "git status --short", riskLabel: "高风险", description: "此操作可能影响工作区之外的系统。" });
    expect(JSON.stringify(details)).not.toContain("secret raw payload");
  });

  it("uses the same localized tool names as the TUI", () => {
    expect(toolDisplayName("coding.shell", "zh-CN")).toBe("运行命令");
    expect(toolDisplayName("coding.git_diff", "en")).toBe("View Git Diff");
    expect(toolDisplayName("custom.tool", "zh-CN")).toBe("custom.tool");
  });

  it("formats elapsed time through hours without dropping seconds", () => {
    expect(formatDuration(3_000)).toBe("3s");
    expect(formatDuration(3_723_000)).toBe("1h02m03s");
  });

  it("queues, edits, and removes follow-up messages without restarting the active timer", () => {
    useRuntimeStore.setState({ ...state(), running: true, runStartedAt: 123 });
    const image = { id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
    useRuntimeStore.getState().enqueuePrompt("下一轮", [image]);
    const queued = useRuntimeStore.getState().queuedPrompts[0]!;
    expect(queued).toMatchObject({ text: "下一轮", attachments: [image] });
    useRuntimeStore.getState().addOptimisticUser("当前引导", []);
    expect(useRuntimeStore.getState().runStartedAt).toBe(123);
    useRuntimeStore.getState().removeQueuedPrompt(queued.id);
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
  });

  it("starts the next queued message after the active run finishes", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    useRuntimeStore.getState().enqueuePrompt("继续检查", []);
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    expect(container.textContent).toContain("继续检查");
    await act(async () => useRuntimeStore.getState().applyEvents([{ sequence: 1, kind: "run_finished", runId: "r1" }]));
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "继续检查" });
    expect(useRuntimeStore.getState().running).toBe(true);
    await act(async () => root.unmount());
    container.remove();
  });

  it("restores tool process trails when switching sessions", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: {
        provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
        blocks: JSON.stringify([
          { kind: "user", runId: "r9", content: "检查样式", state: "submitted" },
          { kind: "assistant", runId: "r9", content: "已完成", state: "completed" },
        ]),
        blockSequences: JSON.stringify([1, 2]),
        toolRecords: JSON.stringify([
          {
            runId: "r9", toolCallId: "c1", name: "read_file", anchorSequence: 1, state: "completed",
            arguments: { path: "frontend/src/styles.css" },
            startedAt: "2026-08-01T10:00:00.000Z", completedAt: "2026-08-01T10:00:04.000Z",
          },
          {
            runId: "r9", toolCallId: "c2", name: "shell", anchorSequence: 1, state: "completed",
            arguments: { command: "npm test" },
            startedAt: "2026-08-01T10:00:05.000Z", completedAt: "2026-08-01T10:00:12.000Z",
          },
        ]),
      },
    }]);
    expect(projected.blocks.map((block) => block.kind)).toEqual(["user", "tool", "tool", "assistant"]);
    expect(projected.blocks[1]).toMatchObject({ kind: "tool", toolCallId: "c1", title: "read_file", state: "completed" });
    expect(projected.blocks[2]).toMatchObject({ kind: "tool", toolCallId: "c2", title: "shell" });
    expect(projected.running).toBe(false);
  });

  it("restores and changes the active session model", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { blocks: "[]", provider: "grok", model: "grok-4.20", reasoning: "medium", agentMode: "team" },
    }]);
    expect(projected.snapshot).toMatchObject({ provider: "grok", model: "grok-4.20", reasoning: "medium", agentMode: "team" });

    useRuntimeStore.getState().hydrate(snapshot);
    useRuntimeStore.getState().setSessionModel("chatgpt", "gpt-5.6-luna", "low");
    expect(useRuntimeStore.getState().snapshot).toMatchObject({ provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" });
    useRuntimeStore.getState().setChatGPTFastMode(true);
    expect(useRuntimeStore.getState().snapshot?.chatgptFastMode).toBe(true);
  });

  it("projects the persisted Codex speed into the composer state", () => {
    const projected = reduceEvents(state(), [{ sequence: 1, kind: "model_routes", data: { chatgpt_fast_mode: "true" } }]);
    expect(projected.snapshot?.chatgptFastMode).toBe(true);
  });

  it("projects the model catalog used by the composer switcher", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "model_catalog",
      data: { provider: "chatgpt", models: JSON.stringify([{ id: "gpt-5.6-sol", name: "5.6 Sol", contextWindow: 272_000, reasoningLevels: ["medium", "high"], defaultReasoning: "high" }, { id: "gpt-5.6-terra", name: "5.6 Terra", reasoningLevels: ["low", "medium"] }]) },
    }]);
    expect(projected.modelsByProvider.chatgpt).toEqual([
      { id: "gpt-5.6-sol", name: "5.6 Sol", contextWindow: 272_000, reasoningLevels: ["medium", "high"], defaultReasoning: "high" },
      { id: "gpt-5.6-terra", name: "5.6 Terra", reasoningLevels: ["low", "medium"], defaultReasoning: "" },
    ]);
    expect(projected.contextUsage.contextLimit).toBe(272_000);
  });

  it("restores and renders the current conversation context occupancy", async () => {
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: { blocks: "[]", provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", usage: JSON.stringify({ inputTokens: 68_000, outputTokens: 4_000, contextLimit: 288_000, currentTurnMainReported: true }) },
    }]);
    expect(restored.contextUsage).toEqual({ inputTokens: 68_000, outputTokens: 4_000, contextLimit: 288_000, reported: true });
    expect(contextOccupancy(restored.contextUsage, null)).toMatchObject({ used: 72_000, limit: 288_000, percentage: 25, remaining: 216_000, estimated: false });

    useRuntimeStore.setState(restored);
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(ContextMeter)));
    expect(container.querySelector("summary")?.getAttribute("aria-label")).toBe("上下文占用 25%");
    expect(container.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("25");
    expect(container.textContent).toContain("72K / 288K");
    const details = container.querySelector("details")!;
    details.open = true;
    await act(async () => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(details.open).toBe(false);
    await act(async () => root.unmount());
  });

  it("updates context occupancy from provider fact snapshots", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1, kind: "context_usage", sessionId: "s1", state: "reported",
      data: { factSnapshot: "true", usageSnapshot: JSON.stringify({ inputTokens: 90_000, outputTokens: 6_000, contextLimit: 128_000, currentTurnMainReported: true }) },
    }]);
    expect(projected.contextUsage).toEqual({ inputTokens: 90_000, outputTokens: 6_000, contextLimit: 128_000, reported: true });
  });

  it("projects current workspace line changes from git status events", async () => {
    const changed = reduceEvents(state(), [{
      sequence: 1, kind: "git_branches", workspaceDirty: true,
      data: { additions: "21", deletions: "4", changed_files: "54" },
    }]);
    expect(changed).toMatchObject({ workspaceDirty: true, workspaceAdditions: 21, workspaceDeletions: 4, workspaceChangedFiles: 54 });
    useRuntimeStore.setState(changed);
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(Inspector)));
    expect(container.textContent).toContain("+21");

    const clean = reduceEvents(changed, [{ sequence: 2, kind: "git_branches", workspaceDirty: false, data: { additions: "0", deletions: "0", changed_files: "0" } }]);
    expect(clean).toMatchObject({ workspaceDirty: false, workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0 });
    await act(async () => useRuntimeStore.setState(clean));
    expect(container.textContent).not.toContain("+0");
    expect(container.textContent).not.toContain("−0");
    await act(async () => root.unmount());
  });

  it("renders the real environment panel without the old tabs or runtime placeholder", async () => {
    const projected = reduceEvents({
      ...state(),
      blocks: [{ id: "user-1", kind: "user", content: "检查截图", attachments: [{ id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 }] }],
    }, [{
      sequence: 1,
      kind: "background_state",
      background: [{ id: "terminal-1", name: "后台终端", command: "bun run dev", cwd: "/tmp/azem", pid: 123, state: "running", exitCode: 0, startedAt: "2026-08-02T12:00:00Z" }],
    }]);
    useRuntimeStore.setState(projected);
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(Inspector)));
    expect(container.textContent).toContain("环境信息");
    expect(container.textContent).toContain("bun run dev");
    expect(container.textContent).toContain("screen.png");
    expect(container.textContent).not.toContain("上下文检查器");
    expect(container.textContent).not.toContain("Runtime 正常");
    await act(async () => root.unmount());
  });
});
