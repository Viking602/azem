import { describe, expect, it } from "vitest";
import { reduceEvents, type RuntimeData } from "./store";
import type { Snapshot } from "./types";
import { formatDuration } from "./components/ThreadSurface";

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

  it("formats elapsed time through hours without dropping seconds", () => {
    expect(formatDuration(3_000)).toBe("3s");
    expect(formatDuration(3_723_000)).toBe("1h02m03s");
  });
});
