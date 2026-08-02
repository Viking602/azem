import { describe, expect, it } from "vitest";
import { reduceEvents, type RuntimeData, useRuntimeStore } from "./store";
import type { Snapshot } from "./types";
import { approvalPresentation, formatDuration } from "./components/ThreadSurface";

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

function state(): RuntimeData {
  return {
    snapshot, sessions: [], currentSessionId: "s1", currentTitle: "", blocks: [], agents: [], selectedAgentId: "", agentBlocks: [], agentCatalog: [],
    skills: [], branches: [], modelRoutes: [], modelsByProvider: {}, contextProfile: null, todo: null, recovery: [],
    runId: "", running: false, runStartedAt: 0, activity: "", approvalMode: "prompt", workspaceDirty: false,
    lastSequence: 0, error: "", view: "thread", inspectorTab: "environment", inspectorOpen: true,
    settingsOpen: false, commandOpen: false, planMode: false, attachments: [], theme: "system",
  };
}

describe("runtime event projection", () => {
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
    expect(details).toMatchObject({ tool: "coding.shell", target: "git status --short", riskLabel: "高风险", description: "此操作可能影响工作区之外的系统。" });
    expect(JSON.stringify(details)).not.toContain("secret raw payload");
  });

  it("formats elapsed time through hours without dropping seconds", () => {
    expect(formatDuration(3_000)).toBe("3s");
    expect(formatDuration(3_723_000)).toBe("1h02m03s");
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
});
