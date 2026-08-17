import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import {
  fileChangePillsForBlocks, formatProcessGroupCount, processGroupCountLabel, processGroupCounts, shellStatusLines,
  thinkingChipPreview, toolChipBasename, toolChipModel,
} from "./toolChip";

function tool(id: string, title: string, state = "completed", data: Block["data"] = {}): Block {
  return { id, kind: "tool", title, state, data };
}

describe("tool chip presentation", () => {
  it("maps a write to a line-count label and filename chip", () => {
    const model = toolChipModel(tool("write", "coding.write_file", "completed", {
      arguments: JSON.stringify({ path: "src/ChurnSchedule.tsx", content: "a\n".repeat(204) }),
    }), "en");
    expect(model.kind).toBe("write");
    expect(model.label).toBe("Write 204 lines");
    expect(model.chip).toBe("ChurnSchedule.tsx");
  });

  it("maps shell to a command chip and honest status lines", () => {
    const model = toolChipModel(tool("shell", "coding.shell", "completed", {
      arguments: JSON.stringify({ command: "npm run freeze" }),
      output: "✓ built in 1.2s\n✓ 34 checks passed\nrandom log",
    }), "en");
    expect(model.kind).toBe("shell");
    expect(model.chip).toBe("npm run freeze");
    expect(model.statusLines).toEqual(["built in 1.2s", "34 checks passed"]);
  });

  it("maps an image read to a filename chip and result metadata", () => {
    const model = toolChipModel(tool("read", "coding.read_file", "completed", {
      arguments: JSON.stringify({ path: "assets/flavor-chart.png" }),
    }), "zh-CN");
    const withResult = toolChipModel({
      ...tool("read", "coding.read_file", "completed", {
        arguments: JSON.stringify({ path: "assets/flavor-chart.png" }),
      }),
      content: `${JSON.stringify({ path: "assets/flavor-chart.png" })}\n1280 × 720 · line chart\nMint chip trends up 12%.`,
    }, "zh-CN");
    expect(model.kind).toBe("image");
    expect(model.label).toBe("读取图片");
    expect(model.chip).toBe("flavor-chart.png");
    expect(withResult.meta).toEqual(["1280 × 720 · line chart", "Mint chip trends up 12%."]);
  });

  it("maps search to a query chip without inventing a second search UI", () => {
    const model = toolChipModel(tool("search", "coding.search", "completed", {
      arguments: JSON.stringify({ query: "useComposerModels", path: "frontend/src" }),
    }), "zh-CN");
    expect(model.kind).toBe("search");
    expect(model.chip).toBe("useComposerModels");
    expect(model.label).toBe("搜索代码");
  });

  it("previews thinking as a capsule without keeping the full prose on the row", () => {
    expect(thinkingChipPreview([
      { id: "t1", kind: "thinking", content: "**Planning the churn schedule** for next week", state: "completed" },
    ])).toBe("Planning the churn schedule for next week");
    expect(thinkingChipPreview([
      { id: "t1", kind: "thinking", content: "A".repeat(60), state: "completed" },
    ])).toBe(`${"A".repeat(41)}…`);
  });

  it("counts process tools and commentary honestly and ignores thinking", () => {
    const blocks: Block[] = [
      { id: "c1", kind: "commentary", content: "先改计划", state: "completed" },
      { id: "t1", kind: "thinking", content: "Planning…", state: "completed" },
      tool("w1", "coding.write_file"),
      tool("s1", "coding.shell"),
      { id: "c2", kind: "commentary", content: "再验证", state: "completed" },
    ];
    expect(processGroupCounts(blocks)).toEqual({ tools: 2, messages: 2 });
    expect(processGroupCountLabel(blocks, "en")).toBe("2 tool calls, 2 messages");
    expect(processGroupCountLabel(blocks, "zh-CN")).toBe("2 次工具调用，2 条进度");
    expect(formatProcessGroupCount(4, 2, "en")).toBe("4 tool calls, 2 messages");
    expect(formatProcessGroupCount(6, 0, "zh-CN")).toBe("6 次工具调用");
  });

  it("does not count host fallback commentary as a visible progress message", () => {
    const blocks: Block[] = [
      {
        id: "fallback", kind: "commentary",
        content: "正在调用所需工具，并根据实际结果继续。",
        data: { synthetic: "tool_announcement" },
      },
      tool("r1", "coding.read_file"),
      tool("s1", "coding.search"),
    ];
    expect(processGroupCounts(blocks)).toEqual({ tools: 2, messages: 0 });
    expect(processGroupCountLabel(blocks, "zh-CN")).toBe("2 次工具调用");
  });

  it("keeps queued and approval-bound writes out of file-change pills", () => {
    const pills = fileChangePillsForBlocks([
      tool("queued", "coding.write_file", "queued", {
        arguments: JSON.stringify({ path: "src/secret.ts", content: "secret\n" }),
      }),
      tool("review", "coding.edit_hashline", "reviewing_approval", {
        arguments: JSON.stringify({ input: "¶src/app.ts#ABCD\nreplace 1:\n+secret" }),
      }),
      tool("done", "coding.write_file", "completed", {
        arguments: JSON.stringify({ path: "src/flavors.css", content: "a\n".repeat(13) }),
      }),
    ]);
    expect(pills.files).toEqual([{ path: "src/flavors.css", additions: 13, deletions: 0 }]);
    expect(toolChipBasename("frontend/src/flavors.css")).toBe("flavors.css");
  });

  it("does not invent shell status lines from ordinary output", () => {
    expect(shellStatusLines("error: missing file\nsee logs")).toEqual([]);
  });
});
