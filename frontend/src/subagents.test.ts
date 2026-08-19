import { describe, expect, it } from "vitest";
import type { AgentState } from "./types";
import { subagentEvidenceStatusLabel, subagentPreviewText } from "./subagents";

function agent(overrides: Partial<AgentState> = {}): AgentState {
  return {
    id: "agent-1", type: "审查架构边界", description: "审查架构边界", parentRunId: "run",
    parentToolCallId: "spawn", model: "gpt-5.6-sol", background: false,
    capabilityMode: "read-only", isolation: "none", cwd: ".", activity: "",
    warning: "", worktreePath: "", toolCalls: 0, turns: 0, tokensUsed: 0,
    elapsedMs: 1000, state: "running", summary: "", preview: "", previewKind: "",
    previewRunId: "child-1", elapsedObservedAt: Date.now(),
    ...overrides,
  };
}

describe("subagentPreviewText", () => {
  it("prefers live thinking over a bare running label", () => {
    expect(subagentPreviewText(agent({ preview: "先核对模块边界", previewKind: "thinking" }), "审查架构边界", "zh-CN"))
      .toBe("先核对模块边界");
  });

  it("does not treat lifecycle or tool names as progress", () => {
    expect(subagentPreviewText(agent({ activity: "running", summary: "running" }), "审查架构边界", "zh-CN"))
      .toBe("等待模型输出");
    expect(subagentPreviewText(agent({ activity: "coding.search" }), "审查架构边界", "zh-CN"))
      .toBe("等待模型输出");
  });

  it("keeps the finished empty-state copy after completion", () => {
    expect(subagentPreviewText(agent({ state: "completed", preview: "" }), "审查架构边界", "zh-CN"))
      .toBe("等待进度更新");
  });
});

describe("subagentEvidenceStatusLabel", () => {
  it("localizes the three durable evidence states and omits unknown status", () => {
    expect(subagentEvidenceStatusLabel("provisional", "en")).toBe("Provisional evidence");
    expect(subagentEvidenceStatusLabel("verified", "zh-CN")).toBe("证据已验证");
    expect(subagentEvidenceStatusLabel("stale", "en")).toBe("Stale evidence");
    expect(subagentEvidenceStatusLabel("", "en")).toBe("");
  });
});
