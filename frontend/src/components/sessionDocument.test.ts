import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import {
  projectSessionDocument,
  turnAnswerPreview,
  turnEditedFiles,
  turnQuestionPreview,
} from "./sessionDocument";

describe("session document projection", () => {
  it("splits the transcript into user turns while keeping process folded as items", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "分析 Timeline", state: "completed" },
      { id: "t1", kind: "thinking", runId: "run-1", content: "思考", state: "completed" },
      { id: "tool1", kind: "tool", runId: "run-1", title: "coding.read_file", state: "completed" },
      { id: "a1", kind: "assistant", runId: "run-1", content: "过程透明但难读", textPhase: "final_answer", state: "completed" },
      { id: "u2", kind: "user", content: "做非 Timeline 方案", state: "completed" },
      { id: "a2", kind: "assistant", runId: "run-2", content: "采用工作文档投影", textPhase: "final_answer", state: "completed" },
    ];

    const projection = projectSessionDocument(blocks);
    expect(projection.turns).toHaveLength(2);
    expect(projection.currentIndex).toBe(1);
    expect(projection.turns[0]?.user?.id).toBe("u1");
    expect(projection.turns[0]?.items.map((item) => item.kind)).toEqual(["process", "block"]);
    expect(projection.turns[1]?.user?.content).toContain("非 Timeline");
    expect(turnQuestionPreview(projection.turns[0]!)).toContain("分析 Timeline");
    expect(turnAnswerPreview(projection.turns[0]!)).toContain("过程透明");
  });

  it("keeps approval and plan interrupts as main-column blocks", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "改配置", state: "completed" },
      {
        id: "appr", kind: "approval", runId: "run", state: "pending", approvalId: "a1",
        data: { tool: "apply_patch", target: "config.yaml", effect: "write" },
      },
      { id: "plan", kind: "plan", planId: "p1", state: "proposed", title: "实施计划", content: "步骤一" },
    ];
    const projection = projectSessionDocument(blocks);
    expect(projection.turns).toHaveLength(1);
    expect(projection.turns[0]?.items.map((item) => item.kind === "block" ? item.block.kind : item.kind))
      .toEqual(["approval", "plan"]);
  });

  it("does not project file changes for the still-running active run", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "编辑", state: "completed" },
      {
        id: "edit", kind: "tool", runId: "run-live", title: "coding.write_file", state: "completed",
        data: {
          arguments: JSON.stringify({ path: "a.ts", content: "export const n = 1;\n" }),
        },
      },
    ];
    const turn = projectSessionDocument(blocks, { activeRunId: "run-live", running: true }).turns[0]!;
    expect(turnEditedFiles(turn, { activeRunId: "run-live", running: true })).toBeNull();
    expect(turnEditedFiles(turn, { running: false })?.files.map((file) => file.path)).toEqual(["a.ts"]);
  });
});
