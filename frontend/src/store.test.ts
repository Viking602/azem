import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { mergeSessionTranscript, reduceEvents, reorderSessionQueue, type RuntimeData, useRuntimeStore } from "./store";
import type { Snapshot } from "./types";
import Inspector from "./components/Inspector";
import ThreadSurface, { approvalPresentation, ContextMeter, contextOccupancy, formatDuration } from "./components/ThreadSurface";
import { TimelineBlock } from "./components/Timeline";
import SubagentsPage from "./components/SubagentsPage";
import { toolDisplayName } from "./i18n";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });
Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => undefined });

async function enterComposerText(textarea: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(textarea, value);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

function state(): RuntimeData {
  return {
    snapshot, sessions: [], projects: [], currentSessionId: "s1", currentTitle: "", blocks: [], agents: [], backgroundProcesses: [], selectedAgentId: "", agentBlocks: [], agentCatalog: [],
    skills: [], branches: [], pullRequestDashboard: null, selectedPullRequestNumber: null, pullRequestDetail: null,
    pullRequestMonitors: new Map(), pullRequestLoading: false, pullRequestMutating: false, pullRequestError: "",
    modelRoutes: [], modelsByProvider: {}, contextProfile: null,
    contextUsage: { inputTokens: 0, outputTokens: 0, contextLimit: 0, reported: false }, todo: null, recovery: [],
    runId: "", running: false, globalRunId: "", globalRunSessionId: "", runStartedAt: 0, activity: "", approvalMode: "prompt", workspaceDirty: false,
    workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
    lastSequence: 0, error: "", view: "thread", inspectorTab: "environment", inspectorOpen: true,
    settingsOpen: false, commandOpen: false, planMode: false, attachments: [], queuedPrompts: [], queuePauseReasons: {}, theme: "system",
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

  it("projects a first-turn session immediately and replaces its generated title", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().addOptimisticUser("修复会话标题", []);
    expect(useRuntimeStore.getState().sessions[0]).toMatchObject({ id: "s1", title: "新会话" });

    useRuntimeStore.getState().applyEvents([{
      sequence: 1, kind: "session_loaded", state: "list", data: { sessions: "[]" },
    }]);
    expect(useRuntimeStore.getState().sessions[0]).toMatchObject({ id: "s1", title: "新会话" });

    useRuntimeStore.getState().applyEvents([{
      sequence: 2,
      kind: "session_loaded",
      state: "list",
      data: {
        sessions: JSON.stringify([{
          id: "s1", title: "修复会话标题", providerId: "chatgpt", modelId: "gpt-5.6-sol",
          reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
        }]),
      },
    }]);
    expect(useRuntimeStore.getState().sessions[0]?.title).toBe("修复会话标题");
    expect(useRuntimeStore.getState().currentTitle).toBe("修复会话标题");
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
      {
        sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "read-1", state: "progress",
        data: { output: "phase 1\n", output_bytes: "8" },
      },
    ]);
    expect(started.blocks[0]).toMatchObject({
      kind: "tool", toolCallId: "read-1", state: "running", title: "coding.read_file",
    });
    expect(started.blocks[0]?.content).toContain("frontend/src/App.tsx");
    expect(started.blocks[1]?.content).toContain("MenuSelect");
    expect(started.blocks[0]).toMatchObject({ state: "running", data: { output: "phase 1\n", output_bytes: "8" } });

    const finished = reduceEvents(started, [
      { sequence: 5, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "export default function App" },
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);
    expect(finished.blocks.find((block) => block.toolCallId === "read-1")).toMatchObject({ state: "completed" });
    // Search never finished; run end must stop it without claiming execution succeeded.
    expect(finished.blocks.find((block) => block.toolCallId === "search-1")).toMatchObject({ state: "failed" });
    expect(finished.running).toBe(false);
  });

  it("settles shell commands as soon as their finished update arrives", () => {
    const started = reduceEvents(state(), [
      { sequence: 1, kind: "tool_started", runId: "r1", toolCallId: "failed", state: "running", data: { name: "coding.shell" } },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "passed", state: "running", data: { name: "coding.shell" } },
    ]);
    const settled = reduceEvents(started, [
      { sequence: 3, kind: "tool_update", runId: "r1", toolCallId: "failed", state: "finished", data: { status: "exited", exit_code: "1", output: "HTTP 401" } },
      { sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "passed", state: "finished", data: { status: "exited", exit_code: "0", output: "ok" } },
    ]);

    expect(settled.blocks.find((block) => block.toolCallId === "failed")).toMatchObject({ state: "failed" });
    expect(settled.blocks.find((block) => block.toolCallId === "passed")).toMatchObject({ state: "completed" });
  });

  it("preserves queued and approval states until a tool actually runs", () => {
    const queued = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "write-1", state: "queued", data: { name: "coding.write_file" } },
      { sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "read-1", state: "queued", data: { name: "coding.read_file" } },
    ]);
    expect(queued.blocks.map((block) => block.state)).toEqual(["queued", "queued"]);

    const awaiting = reduceEvents(queued, [
      { sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "write-1", state: "awaiting_approval" },
    ]);
    expect(awaiting.blocks.map((block) => block.state)).toEqual(["awaiting_approval", "queued"]);

    const running = reduceEvents(awaiting, [
      { sequence: 5, kind: "tool_update", runId: "r1", toolCallId: "write-1", state: "running" },
    ]);
    expect(running.blocks.map((block) => block.state)).toEqual(["running", "queued"]);

    const finished = reduceEvents(running, [
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);
    expect(finished.blocks.map((block) => block.state)).toEqual(["failed", "failed"]);
  });

  it("shows cumulative command output without resetting the open detail", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const block = {
      id: "shell-1", kind: "tool" as const, title: "coding.shell", state: "running",
      content: JSON.stringify({ command: "bun run dev", timeout_seconds: 30 }),
      data: { output: "ready\nrequest 1\n", output_bytes: "16" },
    };
    await act(async () => root.render(createElement(TimelineBlock, { block, language: "zh-CN" })));
    const details = container.querySelector("details")!;
    details.open = true;
    details.dispatchEvent(new Event("toggle"));
    const log = container.querySelector<HTMLPreElement>(".tool-log")!;
    expect(log.textContent).toBe("ready\nrequest 1\n");
    expect(log.getAttribute("aria-live")).toBe("off");
    expect(log.getAttribute("tabindex")).toBe("0");

    await act(async () => root.render(createElement(TimelineBlock, {
      block: { ...block, data: { output: "ready\nrequest 1\nrequest 2\n", output_bytes: "26" } },
      language: "zh-CN",
    })));
    expect(details.open).toBe(true);
    expect(container.querySelector(".tool-log")?.textContent).toContain("request 2");
    await act(async () => root.unmount());
    container.remove();
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
      {
        sequence: 1, kind: "agent_state", agentId: "agent-1", state: "running", text: "",
        agent: { type: "explore", description: "检查变更", toolCalls: 0, turns: 0, tokensUsed: 0, elapsedMs: 0 },
      },
      { sequence: 2, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "先看 diff" },
      { sequence: 3, kind: "tool_started", runId: "child-1", agentId: "agent-1", toolCallId: "c1", data: { name: "coding.git_diff" }, state: "running" },
      { sequence: 4, kind: "tool_finished", runId: "child-1", agentId: "agent-1", toolCallId: "c1", text: "ok", state: "completed" },
      { sequence: 5, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "结论" },
      // Unrelated main-run frame still goes to the main transcript.
      { sequence: 6, kind: "thinking_delta", runId: "main-1", text: "主会话思考" },
    ]);
    expect(projected.blocks).toHaveLength(1);
    expect(projected.blocks[0]).toMatchObject({ kind: "thinking", content: "主会话思考" });
    expect(projected.agentBlocks.map((block) => block.kind)).toEqual(["thinking", "tool", "assistant"]);
    expect(projected.agentBlocks[0]).toMatchObject({ kind: "thinking", title: "思考", content: "先看 diff" });
    expect(projected.agentBlocks[1]).toMatchObject({ kind: "tool", title: "coding.git_diff", state: "completed" });
    expect(projected.agentBlocks[2]).toMatchObject({ kind: "assistant", content: "结论" });
    expect(projected.agents[0]).toMatchObject({ preview: "结论", previewKind: "assistant", previewRunId: "child-1" });

    const finished = reduceEvents(projected, [{
      sequence: 7, kind: "agent_state", agentId: "agent-1", state: "completed", text: "已检查全部变更",
      agent: { type: "explore", description: "检查变更", summary: "已检查全部变更" },
    }]);
    expect(finished.agents[0]).toMatchObject({ state: "completed", preview: "已检查全部变更", previewKind: "assistant" });
  });

  it("ignores other agents' live frames while a different side chat is open", () => {
    const projected = reduceEvents({ ...state(), selectedAgentId: "agent-a" }, [
      { sequence: 1, kind: "thinking_delta", runId: "c", agentId: "agent-b", text: "不该出现" },
    ]);
    expect(projected.agentBlocks).toEqual([]);
    expect(projected.blocks).toEqual([]);
  });

  it("clears stale detail blocks only when switching subagents", () => {
    const detail = [{ id: "answer-1", kind: "assistant" as const, content: "detail" }];
    useRuntimeStore.setState({ ...state(), selectedAgentId: "agent-a", agentBlocks: detail });
    useRuntimeStore.getState().selectAgent("agent-a");
    expect(useRuntimeStore.getState().agentBlocks).toEqual(detail);
    useRuntimeStore.getState().selectAgent("agent-b");
    expect(useRuntimeStore.getState()).toMatchObject({ selectedAgentId: "agent-b", agentBlocks: [] });
  });

  it("keeps the inspector preference across temporary pages and side panels", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().setView("extensions");
    useRuntimeStore.getState().selectAgent("agent-a");
    useRuntimeStore.getState().selectAgent("");
    useRuntimeStore.getState().selectPullRequest(42);
    useRuntimeStore.getState().selectPullRequest(null);
    useRuntimeStore.getState().setView("thread");
    expect(useRuntimeStore.getState().inspectorOpen).toBe(true);

    useRuntimeStore.getState().setInspectorOpen(false);
    useRuntimeStore.getState().setView("extensions");
    useRuntimeStore.getState().selectAgent("agent-a");
    useRuntimeStore.getState().selectAgent("");
    useRuntimeStore.getState().selectPullRequest(42);
    useRuntimeStore.getState().selectPullRequest(null);
    useRuntimeStore.getState().setView("thread");
    expect(useRuntimeStore.getState().inspectorOpen).toBe(false);
  });

  it("separates discrete thinking blurbs instead of gluing **A****B**", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "**Planning analysis**" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "**Inspecting files**" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: " mid-sentence" },
    ]);
    expect(projected.blocks[0]?.content).toBe("**Planning analysis**\n\n**Inspecting files** mid-sentence");
  });

  it("keeps reasoning steps in event order when tools run between model updates", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "先读取文件。" },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      { sequence: 3, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "done" },
      { sequence: 4, kind: "thinking_delta", runId: "r1", text: "再检查结果。" },
      { sequence: 5, kind: "text_delta", runId: "r1", text: "完成。" },
    ]);
    expect(projected.blocks.map((block) => block.kind)).toEqual(["thinking", "tool", "thinking", "assistant"]);
    expect(projected.blocks.filter((block) => block.kind === "thinking").map((block) => block.content)).toEqual(["先读取文件。", "再检查结果。"]);
    expect(projected.blocks.filter((block) => block.kind === "thinking").every((block) => block.state === "completed")).toBe(true);
  });

  it("keeps commentary in the process trail before tools and final answers", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "分析协议。" },
      { sequence: 2, kind: "text_delta", runId: "r1", text: "协议已确认，", textPhase: "commentary" },
      { sequence: 3, kind: "text_delta", runId: "r1", text: "接下来读取文件。", textPhase: "commentary" },
      { sequence: 4, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      { sequence: 5, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "done" },
      { sequence: 6, kind: "text_delta", runId: "r1", text: "检查完成。", textPhase: "final_answer" },
      { sequence: 7, kind: "run_finished", runId: "r1", state: "completed" },
    ]);

    expect(projected.blocks.map((block) => block.kind)).toEqual(["thinking", "commentary", "tool", "assistant"]);
    expect(projected.blocks[1]).toMatchObject({
      content: "协议已确认，接下来读取文件。",
      state: "completed",
      textPhase: "commentary",
    });
    expect(projected.blocks[3]).toMatchObject({
      content: "检查完成。",
      state: "completed",
      textPhase: "final_answer",
    });
  });

  it("drops an uncommitted provider attempt before projecting its retry", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "第一次思考" },
      { sequence: 3, kind: "text_delta", runId: "r1", text: "第一次答案", textPhase: "final_answer" },
      { sequence: 4, kind: "provider_retry", runId: "r1", state: "restarted" },
      { sequence: 5, kind: "thinking_delta", runId: "r1", text: "恢复后思考" },
      { sequence: 6, kind: "text_delta", runId: "r1", text: "最终答案", textPhase: "final_answer" },
      { sequence: 7, kind: "run_finished", runId: "r1", state: "completed" },
    ]);

    expect(projected.blocks.map((block) => [block.kind, block.content])).toEqual([
      ["thinking", "恢复后思考"],
      ["assistant", "最终答案"],
    ]);

    const subagent = reduceEvents({
      ...state(),
      selectedAgentId: "agent-1",
      agentBlocks: [
        { id: "old-thinking", kind: "thinking", runId: "child-1", content: "第一次思考" },
        { id: "old-answer", kind: "assistant", runId: "child-1", content: "第一次答案" },
      ],
    }, [
      { sequence: 1, kind: "provider_retry", runId: "child-1", agentId: "agent-1", state: "restarted" },
      { sequence: 2, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "子智能体最终答案" },
    ]);
    expect(subagent.agentBlocks.map((block) => block.content)).toEqual(["子智能体最终答案"]);
  });

  it("timestamps a reasoning segment and settles it before tool work begins", () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_000);
    try {
      const streaming = reduceEvents(state(), [
        { sequence: 1, kind: "thinking_delta", runId: "r1", text: "检查实现" },
      ]);
      expect(streaming.blocks[0]).toMatchObject({
        kind: "thinking",
        state: "streaming",
        data: { startedAt: "1000" },
      });

      clock.mockReturnValue(2_500);
      const settled = reduceEvents(streaming, [
        { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      ]);
      expect(settled.blocks[0]).toMatchObject({
        kind: "thinking",
        state: "completed",
        data: { startedAt: "1000", completedAt: "2500", elapsedMs: "1500" },
      });
      expect(settled.blocks[1]).toMatchObject({ kind: "tool", state: "running" });
    } finally {
      clock.mockRestore();
    }
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

  it("adds a timed Codex-style separator when the user stops a run", () => {
    const current: RuntimeData = {
      ...state(),
      running: true,
      runId: "r1",
      runStartedAt: Date.now() - 65_000,
      blocks: [{ id: "thinking", kind: "thinking", runId: "r1", content: "处理中", state: "streaming" }],
    };
    const projected = reduceEvents(current, [{ sequence: 1, kind: "run_cancelled", runId: "r1" }]);
    expect(projected.blocks.at(-1)).toMatchObject({ kind: "status", runId: "r1", title: "run_cancelled", state: "cancelled" });
    expect(Number(projected.blocks.at(-1)?.data?.elapsedMs)).toBeGreaterThanOrEqual(65_000);
  });

  it("restores structured edit sections from durable tool records", () => {
    const restored = mergeSessionTranscript(
      [{ id: "user", kind: "user", runId: "r1", content: "edit" }],
      [1],
      [{
        runId: "r1", toolCallId: "edit-1", name: "coding.edit_hashline", anchorSequence: 1, state: "completed",
        arguments: { input: "patch" },
        structured: { sections: [{ path: "src/app.ts", firstChangedLine: 3, diff: "-old\n+next" }] },
      }],
    );
    expect(JSON.parse(restored[1]?.data?.structured || "{}")).toMatchObject({
      sections: [{ path: "src/app.ts", firstChangedLine: 3, diff: "-old\n+next" }],
    });
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

  it("starts an image-only turn from the composer", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [] });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const image = { id: "image-only", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
    await act(async () => useRuntimeStore.setState({ attachments: [image] }));
    const send = container.querySelector<HTMLButtonElement>(".send-button")!;
    expect(send.disabled).toBe(false);
    await act(async () => send.click());
    expect(useRuntimeStore.getState()).toMatchObject({ running: true, attachments: [] });
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "", attachments: [image] });
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows a selected skill inside the composer and clears it after sending", async () => {
    useRuntimeStore.setState({
      ...state(),
      blocks: [],
      skills: [{ name: "aside-browser", description: "Control the browser", sourcePath: "~/.agents/skills/aside-browser", bundled: false, eager: false, disabled: false, resourceCount: 0 }],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const textarea = container.querySelector<HTMLTextAreaElement>("#azem-composer")!;
    await enterComposerText(textarea, "/aside");
    await act(async () => container.querySelector<HTMLButtonElement>(".slash-skills button")!.click());

    expect(container.querySelector(".composer-skill")?.textContent).toContain("aside-browser");
    expect(textarea.value).toBe("");
    await enterComposerText(textarea, "检查当前页面");
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "检查当前页面" });
    expect(container.querySelector(".composer-skill")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("queues, edits, and removes follow-up messages without restarting the active timer", () => {
    useRuntimeStore.setState({ ...state(), running: true, runStartedAt: 123 });
    const image = { id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
    useRuntimeStore.getState().enqueuePrompt("下一轮", [image]);
    const queued = useRuntimeStore.getState().queuedPrompts[0]!;
    expect(queued).toMatchObject({ text: "下一轮", attachments: [image] });
    useRuntimeStore.getState().addOptimisticUser("当前引导", []);
    expect(useRuntimeStore.getState().runStartedAt).toBe(123);
    useRuntimeStore.getState().removeQueuedPrompt(queued.sessionId, queued.id);
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
  });

  it("shows queued guidance as an attached row without a delivery dropdown", async () => {
    useRuntimeStore.setState({
      ...state(),
      blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }],
      running: true,
      runId: "r1",
      runStartedAt: Date.now(),
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));

    expect(container.querySelector(".delivery-menu")).toBeNull();
    await enterComposerText(container.querySelector<HTMLTextAreaElement>("#azem-composer")!, "下一轮再处理");
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());

    expect(useRuntimeStore.getState().queuedPrompts[0]?.text).toBe("下一轮再处理");
    expect(container.querySelector(".queued-prompts + .composer-shell")).not.toBeNull();
    expect(container.querySelector(".queued-guide")?.textContent).toContain("引导");
    expect(container.querySelector(".queued-icon")).not.toBeNull();
    expect(container.querySelector(".queue-menu")).not.toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps queues scoped to their session and reorders only that session", () => {
    const first = { id: "a", sessionId: "s1", text: "first", attachments: [], state: "queued" as const };
    const other = { id: "x", sessionId: "s2", text: "other", attachments: [], state: "queued" as const };
    const second = { id: "b", sessionId: "s1", text: "second", attachments: [], state: "queued" as const };
    expect(reorderSessionQueue([first, other, second], "b", "a").map((item) => item.id)).toEqual(["b", "x", "a"]);
    const switched = reduceEvents({ ...state(), queuedPrompts: [first] }, [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]);
    expect(switched.queuedPrompts).toEqual([first]);
  });

  it("holds a prompt in the visible session while another session owns the global run", async () => {
    useRuntimeStore.setState({
      ...state(),
      currentSessionId: "s2",
      globalRunId: "run-a",
      globalRunSessionId: "s1",
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await enterComposerText(container.querySelector<HTMLTextAreaElement>("#azem-composer")!, "wait for session A");
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());
    expect(useRuntimeStore.getState()).toMatchObject({
      currentSessionId: "s2",
      running: false,
      queuedPrompts: [{ sessionId: "s2", text: "wait for session A", state: "queued" }],
    });
    expect(container.querySelector(".queued-prompt-content")?.textContent).toBe("wait for session A");
    await act(async () => root.unmount());
    container.remove();
  });

  it("tracks a foreign main run through session-scoped event filtering", () => {
    const running = reduceEvents({ ...state(), currentSessionId: "s2" }, [{
      sequence: 1, kind: "run_started", sessionId: "s1", runId: "run-a",
    }]);
    expect(running).toMatchObject({
      currentSessionId: "s2",
      running: false,
      globalRunId: "run-a",
      globalRunSessionId: "s1",
    });
    const finished = reduceEvents(running, [{
      sequence: 2, kind: "run_finished", sessionId: "s1", runId: "run-a",
    }]);
    expect(finished).toMatchObject({ running: false, globalRunId: "", globalRunSessionId: "" });
  });

  it("rejects queue mutations from a different session", () => {
    const queued = { id: "a", sessionId: "s1", text: "first", attachments: [], state: "queued" as const };
    useRuntimeStore.setState({ ...state(), currentSessionId: "s2", queuedPrompts: [queued] });
    useRuntimeStore.getState().updateQueuedPrompt("s2", queued.id, "cross-session", []);
    useRuntimeStore.getState().failQueuedPrompt("s2", queued.id, "cross-session");
    useRuntimeStore.getState().retryQueuedPrompt("s2", queued.id);
    useRuntimeStore.getState().removeQueuedPrompt("s2", queued.id);
    expect(useRuntimeStore.getState().queuedPrompts).toEqual([queued]);
  });

  it("clears a queue editor when the visible session changes", async () => {
    const queued = { id: "a", sessionId: "s1", text: "session A draft", attachments: [], state: "queued" as const };
    useRuntimeStore.setState({
      ...state(),
      blocks: [{ id: "assistant-1", kind: "assistant", content: "running" }],
      running: true,
      runId: "run-a",
      queuedPrompts: [queued],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await act(async () => container.querySelector<HTMLButtonElement>(".queued-prompt-content")!.click());
    expect(container.querySelector<HTMLTextAreaElement>("#azem-composer")!.value).toBe("session A draft");
    await act(async () => useRuntimeStore.getState().applyEvents([{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]));
    expect(container.querySelector<HTMLTextAreaElement>("#azem-composer")!.value).toBe("");
    expect(useRuntimeStore.getState().queuedPrompts).toEqual([queued]);
    await act(async () => root.unmount());
    container.remove();
  });

  it("pauses queued follow-ups after interruption until Resume", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    useRuntimeStore.getState().enqueuePrompt("中断后继续", []);
    useRuntimeStore.getState().applyEvents([{ sequence: 1, kind: "run_cancelled", sessionId: "s1", runId: "r1" }]);
    expect(useRuntimeStore.getState().queuePauseReasons.s1).toBe("interrupted");
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(1);
    expect(container.textContent).toContain("队列因你中断任务而暂停");
    await act(async () => container.querySelector<HTMLButtonElement>(".queue-paused button")!.click());
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().running).toBe(true);
    await act(async () => root.unmount());
    container.remove();
  });

  it("uses Cmd+Shift+Enter to invert Queue into Steer for one follow-up", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const textarea = container.querySelector<HTMLTextAreaElement>("#azem-composer")!;
    await enterComposerText(textarea, "立即调整方向");
    await act(async () => textarea.dispatchEvent(new KeyboardEvent("keydown", {
      bubbles: true, cancelable: true, key: "Enter", code: "Enter", metaKey: true, shiftKey: true,
    })));
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "立即调整方向" });
    await act(async () => root.unmount());
    container.remove();
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

  it("restores, updates, and renders the current Todo plan", async () => {
    const initialTodo = {
      goal: "在桌面端展示任务进度", revision: 1,
      phases: [{ id: "phase-1", title: "实现", items: [
        { id: "item-1", content: "同步 Todo 状态", status: "completed" as const },
        { id: "item-2", content: "添加 Inspector 展示", status: "in_progress" as const },
        { id: "item-3", content: "运行验证", status: "pending" as const },
      ] }],
    };
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded", todo: initialTodo,
      data: { blocks: "[]", provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single" },
    }]);
    expect(restored.todo).toEqual(initialTodo);

    const updatedTodo = {
      ...initialTodo, revision: 2,
      phases: [{ ...initialTodo.phases[0]!, items: initialTodo.phases[0]!.items.map((item) => item.id === "item-2" ? { ...item, status: "completed" as const } : item) }],
    };
    const updated = reduceEvents(restored, [{ sequence: 2, kind: "todo_updated", sessionId: "s1", todo: updatedTodo }]);
    expect(updated.todo?.revision).toBe(2);

    useRuntimeStore.setState(updated);
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(Inspector)));
    expect(container.textContent).toContain("任务计划");
    expect(container.textContent).toContain("在桌面端展示任务进度");
    expect(container.textContent).toContain("添加 Inspector 展示");
    expect(container.textContent).toContain("1 个待办");
    expect(container.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(container.querySelector('[data-status="in_progress"]')).toBeNull();
    await act(async () => root.unmount());
  });

  it("summarizes subagents in the inspector and opens the expandable live roster", async () => {
    let projected = state();
    const states = ["running", "running", "queued", "completed", "completed", "failed"];
    for (let index = 0; index < states.length; index += 1) {
      projected = reduceEvents(projected, [{
        sequence: index + 1,
        kind: "agent_state",
        agentId: `agent-${index + 1}`,
        state: states[index],
        text: states[index] === "completed" ? `完成任务 ${index + 1}` : "",
        agent: {
          type: index < 2 ? "explore" : "worker",
          description: `任务 ${index + 1}`,
          summary: states[index] === "completed" ? `完成任务 ${index + 1}` : "",
          activity: states[index] === "running" ? `正在检查文件 ${index + 1}` : "",
          elapsedMs: (index + 1) * 1000,
        },
      }]);
    }
    useRuntimeStore.setState(projected);
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(Inspector)));
    const summary = container.querySelector<HTMLButtonElement>(".subagent-summary-button")!;
    expect(summary.textContent).toContain("2 个运行中 · 1 个排队中");
    expect(container.querySelector(".agent-roster")).toBeNull();
    await act(async () => summary.click());
    expect(useRuntimeStore.getState().view).toBe("agents");
    expect(useRuntimeStore.getState().inspectorOpen).toBe(true);

    await act(async () => root.render(createElement(SubagentsPage)));
    expect(container.querySelectorAll(".subagent-row")).toHaveLength(4);
    expect(container.textContent).toContain("已启动 6 个子智能体");
    const showMore = Array.from(container.querySelectorAll<HTMLButtonElement>(".subagents-show-more"))
      .find((button) => button.textContent?.includes("再显示 2 个"))!;
    await act(async () => showMore.click());
    expect(container.querySelectorAll(".subagent-row")).toHaveLength(6);

    const rows = container.querySelectorAll<HTMLButtonElement>(".subagent-row > button");
    await act(async () => rows[0]!.click());
    const firstSelected = useRuntimeStore.getState().selectedAgentId;
    expect(firstSelected).not.toBe("");
    await act(async () => useRuntimeStore.setState({ agentBlocks: [{ id: "stale", kind: "assistant", content: "旧详情" }] }));
    await act(async () => rows[1]!.click());
    expect(useRuntimeStore.getState().selectedAgentId).not.toBe(firstSelected);
    expect(useRuntimeStore.getState().agentBlocks).toEqual([]);

    await act(async () => container.querySelector<HTMLButtonElement>(".subagents-close")!.click());
    expect(useRuntimeStore.getState()).toMatchObject({ view: "thread", selectedAgentId: "" });
    await act(async () => root.unmount());
    container.remove();
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

  it("clears pull request detail loading when its panel closes", () => {
    useRuntimeStore.setState({ ...state(), selectedPullRequestNumber: 42, pullRequestLoading: true });
    useRuntimeStore.getState().selectPullRequest(null);
    expect(useRuntimeStore.getState()).toMatchObject({ selectedPullRequestNumber: null, pullRequestLoading: false });
  });

  it("renders the real environment panel without the old tabs or runtime placeholder", async () => {
    const projected = reduceEvents({
      ...state(),
      blocks: [{ id: "user-1", kind: "user", content: "检查截图", attachments: [{ id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 }] }],
      branches: [{ name: "codex/read-only-branch", current: true }],
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
    expect(container.querySelector(".inspector-branch-value")?.textContent).toBe("codex/read-only-branch");
    expect(container.querySelector(".inspector-branch-select")).toBeNull();
    expect(container.textContent).not.toContain("上下文检查器");
    expect(container.textContent).not.toContain("Runtime 正常");
    await act(async () => root.unmount());
  });
});
