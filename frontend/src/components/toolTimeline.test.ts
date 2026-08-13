import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import {
  classifyToolCategory,
  formatToolPresentation,
  groupProcessTimelineBlocks,
  groupTimelineBlocks,
  isRunningTool,
  parseModelProgress,
  segmentProcessTrail,
  summarizeToolGroup,
} from "./toolTimeline";

function tool(id: string, title: string, state = "completed"): Block {
  return { id, kind: "tool", title, state, content: `${title} payload` };
}

describe("process trail segmentation", () => {
  it("folds completed thinking and tools under a process segment", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "fix", state: "submitted" },
      { id: "t1", kind: "thinking", runId: "r1", content: "plan", state: "completed", data: { elapsedMs: "154000" } },
      { id: "tool1", kind: "tool", runId: "r1", title: "read_file", state: "completed" },
      { id: "tool2", kind: "tool", runId: "r1", title: "shell", state: "completed" },
      { id: "a1", kind: "assistant", runId: "r1", content: "done", state: "completed" },
    ];
    const segments = segmentProcessTrail(blocks);
    expect(segments.map((item) => item.kind)).toEqual(["block", "process", "block"]);
    const process = segments[1];
    if (process.kind !== "process") throw new Error("expected process");
    expect(process.active).toBe(false);
    expect(process.elapsedMs).toBe(154000);
    expect(process.blocks).toHaveLength(3);
  });

  it("keeps one subagent conversation in one completed process segment", () => {
    const blocks: Block[] = [
      { id: "thinking-1", kind: "thinking", runId: "child", state: "completed" },
      { id: "progress-1", kind: "commentary", runId: "child", state: "completed" },
      { id: "tool-1", kind: "tool", runId: "child", state: "completed" },
      { id: "thinking-2", kind: "thinking", runId: "child", state: "completed" },
      { id: "progress-2", kind: "commentary", runId: "child", state: "completed" },
      { id: "tool-2", kind: "tool", runId: "child", state: "completed" },
      { id: "final", kind: "assistant", runId: "child", content: "done", state: "completed" },
    ];
    const segments = segmentProcessTrail(blocks);
    expect(segments.map((item) => item.kind)).toEqual(["process", "block"]);
    expect(segments[0]).toMatchObject({ kind: "process", active: false, blocks: blocks.slice(0, 6) });
  });

  it("keeps live diff projections inside the same process segment", () => {
    const blocks: Block[] = [
      { id: "progress-1", kind: "commentary", runId: "run-live", state: "completed" },
      { id: "edit", kind: "tool", runId: "run-live", title: "coding.edit_hashline", state: "completed" },
      { id: "diff", kind: "diff", runId: "run-live", title: "frontend/src/App.tsx", state: "ready" },
      { id: "progress-2", kind: "commentary", runId: "run-live", state: "streaming" },
    ];

    const segments = segmentProcessTrail(blocks, { activeRunId: "run-live", running: true });

    expect(segments).toHaveLength(1);
    expect(segments[0]).toMatchObject({ kind: "process", active: true, blocks });
  });

  it("keeps the active run process expanded and hides agent noise", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "go", state: "submitted" },
      { id: "agent1", kind: "agent", title: "spawn", content: "planner", state: "running" },
      { id: "tool1", kind: "tool", runId: "r2", title: "read_file", state: "running" },
    ];
    const segments = segmentProcessTrail(blocks, { activeRunId: "r2", running: true });
    expect(segments).toHaveLength(2);
    expect(segments[0]).toMatchObject({ kind: "block" });
    expect(segments[1]).toMatchObject({ kind: "process", active: true });
  });

  it("treats progress heartbeats as an active command", () => {
    expect(isRunningTool(tool("shell", "coding.shell", "progress"))).toBe(true);
    const segments = segmentProcessTrail([tool("shell", "coding.shell", "progress")]);
    expect(segments[0]).toMatchObject({ kind: "process", active: true });
  });

  it("keeps the observed live elapsed time when the running tail has no completion stamp", () => {
    const segments = segmentProcessTrail([
      { id: "progress", kind: "commentary", state: "completed", data: { startedAt: "1000", completedAt: "1100" } },
      { id: "tool", kind: "tool", state: "running", data: { startedAt: "1100", elapsedMs: "4800" } },
    ]);
    expect(segments[0]).toMatchObject({ kind: "process", active: true, elapsedMs: 4800 });
  });

  it("keeps an active process clock continuous after session rehydration", () => {
    const startedAt = Date.parse("2026-08-10T04:30:00Z");
    const reloadedAt = Date.parse("2026-08-10T04:31:35Z");
    const segments = segmentProcessTrail([
      {
        id: "progress", kind: "commentary", runId: "run", state: "completed",
        content: "**并行审阅高风险面**\n等待子智能体返回",
        data: { startedAt: String(startedAt), completedAt: String(startedAt + 3_000), elapsedMs: "3000" },
      },
      {
        id: "spawn", kind: "tool", runId: "run", state: "running",
        data: { startedAt: String(startedAt + 3_000) },
      },
    ], { activeRunId: "run", running: true, now: reloadedAt });

    expect(segments[0]).toMatchObject({ kind: "process", active: true, elapsedMs: 95_000 });
  });
});

describe("tool timeline grouping", () => {
  it("recognizes only the explicit model progress contract", () => {
    expect(parseModelProgress("**读取当前前端结构**\nApp、Sidebar、Timeline 与 Inspector"))
      .toEqual({ title: "读取当前前端结构", detail: "App、Sidebar、Timeline 与 Inspector" });
    expect(parseModelProgress("**构建高保真交互原"))
      .toEqual({ title: "构建高保真交互原", detail: "" });
    expect(parseModelProgress("我先读取当前结构，然后再修改。"))
      .toBeNull();
    expect(parseModelProgress("**跨行标题\n不应被猜测**"))
      .toBeNull();
  });

  it("attaches announced tools to model-authored progress steps", () => {
    const commentary: Block = {
      id: "progress", kind: "commentary", state: "completed",
      content: "**读取当前前端结构**\nTimeline 与事件投影",
    };
    const legacy: Block = {
      id: "legacy", kind: "commentary", state: "completed",
      content: "旧会话仍然保留自然段。",
    };
    const entries = groupProcessTimelineBlocks([
      commentary,
      tool("read", "coding.read_file"),
      tool("search", "coding.search"),
      legacy,
    ], "zh-CN");

    expect(entries.map((entry) => entry.kind)).toEqual(["model-progress", "block"]);
    expect(entries[0]).toMatchObject({
      kind: "model-progress",
      presentation: { title: "读取当前前端结构", detail: "Timeline 与事件投影" },
      blocks: [{ id: "read" }, { id: "search" }],
    });
    expect(entries[1]).toMatchObject({ kind: "block", block: { id: "legacy" } });
  });

  it("folds adjacent thinking and diffs into the announced tool step", () => {
    const commentary: Block = {
      id: "progress", kind: "commentary", state: "completed",
      content: "**核对调用路径**\n先定位入口，再验证实际结果",
    };
    const before: Block = { id: "thinking-before", kind: "thinking", state: "completed", content: "形成调查顺序" };
    const after: Block = { id: "thinking-after", kind: "thinking", state: "completed", content: "核对命中位置" };
    const diff: Block = { id: "diff", kind: "diff", state: "completed", content: "" };
    const entries = groupProcessTimelineBlocks([
      before,
      commentary,
      after,
      tool("read", "coding.read_file"),
      diff,
    ], "zh-CN");

    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      kind: "model-progress",
      blocks: [
        { id: "thinking-before" },
        { id: "thinking-after" },
        { id: "read" },
        { id: "diff" },
      ],
    });
  });

  it("classifies azem tool titles", () => {
    expect(classifyToolCategory("coding.search")).toBe("search");
    expect(classifyToolCategory("coding.read_file")).toBe("read");
    expect(classifyToolCategory("coding.edit_hashline")).toBe("edit");
    expect(classifyToolCategory("coding.git_diff")).toBe("diff");
    expect(classifyToolCategory("查看 Git 差异")).toBe("diff");
    expect(classifyToolCategory("coding.shell")).toBe("shell");
    expect(classifyToolCategory("subagent.spawn")).toBe("agent");
  });

  it("summarizes git diff groups without calling them edits", () => {
    expect(summarizeToolGroup([
      tool("1", "coding.git_diff"),
      tool("2", "coding.git_diff"),
      tool("3", "coding.git_diff"),
    ], "zh-CN")).toBe("查看了 3 处差异");
    expect(summarizeToolGroup([
      tool("1", "coding.git_diff"),
      tool("2", "coding.git_diff"),
    ], "en")).toBe("Viewed 2 diffs");
  });

  it("collapses consecutive settled tools into a summary", () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "go" },
      tool("t1", "coding.search"),
      tool("t2", "coding.search"),
      tool("t3", "coding.read_file"),
      tool("t4", "coding.search", "running"),
      { id: "a1", kind: "assistant", content: "done" },
    ];
    const entries = groupTimelineBlocks(blocks, "zh-CN");
    expect(entries.map((entry) => entry.kind)).toEqual(["block", "tool-group", "block", "block"]);
    const group = entries[1];
    expect(group?.kind).toBe("tool-group");
    if (group?.kind === "tool-group") {
      expect(group.blocks).toHaveLength(3);
      expect(group.summary).toContain("搜索了 2 次");
      expect(group.summary).toContain("读取了 1 个文件");
    }
  });

  it("keeps queued and approval-bound tools outside settled groups", () => {
    const entries = groupTimelineBlocks([
      tool("read-1", "coding.read_file"),
      tool("read-2", "coding.read_file"),
      tool("write-1", "coding.write_file", "awaiting_approval"),
      tool("shell-1", "coding.shell", "queued"),
      tool("search-1", "coding.search"),
      tool("search-2", "coding.search"),
    ], "zh-CN");
    expect(entries.map((entry) => entry.kind)).toEqual(["tool-group", "block", "block", "tool-group"]);
    expect(entries[1]).toMatchObject({ kind: "block", block: { id: "write-1", state: "awaiting_approval" } });
    expect(entries[2]).toMatchObject({ kind: "block", block: { id: "shell-1", state: "queued" } });
  });

  it("keeps single tools expanded", () => {
    const entries = groupTimelineBlocks([tool("t1", "coding.search")], "en");
    expect(entries).toHaveLength(1);
    expect(entries[0]?.kind).toBe("block");
  });

  it("summarizes english labels", () => {
    expect(summarizeToolGroup([
      tool("1", "coding.search"),
      tool("2", "coding.search"),
      tool("3", "coding.shell"),
    ], "en")).toBe("Searched 2 times, Ran 1 commands");
  });

  it("humanizes tool json arguments without showing raw json", () => {
    const raw = `{"path":"frontend/src/components/MenuSelect.tsx","endLine":300}`;
    const presentation = formatToolPresentation(raw, "zh-CN");
    expect(presentation.preview).toContain("MenuSelect.tsx");
    expect(presentation.preview).toContain("L");
    expect(presentation.preview).not.toContain("{");
    expect(presentation.preview).not.toContain("\"path\"");
    expect(presentation.fields[0]?.value).toContain("MenuSelect.tsx");
  });

  it("humanizes commands and search queries", () => {
    expect(formatToolPresentation(`{"command":"bun run test"}`, "en").preview).toBe("bun run test");
    expect(formatToolPresentation(`{"query":"useComposerModels","path":"frontend/src"}`, "zh-CN").preview).toContain("useComposerModels");
  });
});
