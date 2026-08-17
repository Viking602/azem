import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import {
  classifyToolCategory,
  displayedToolState,
  formatThinkingDuration,
  formatToolPresentation,
  thinkingStateLabel,
  groupProcessTimelineBlocks,
  groupTimelineBlocks,
  isActiveProcessBlock,
  isCapacityQueued,
  hasVisibleLiveProgress,
  isHostFallbackCommentary,
  isRunningTool,
  shouldShowThinkingWait,
  parseModelProgress,
  processTrailWorthFolding,
  segmentProcessTrail,
  summarizeToolGroup,
  thinkingTraceElapsedMs,
} from "./toolTimeline";
import { fileChangePillsForBlocks, processGroupCounts } from "./toolChip";

function tool(id: string, title: string, state = "completed"): Block {
  return { id, kind: "tool", title, state, content: `${title} payload` };
}

describe("process trail segmentation", () => {
  it("shows the thinking wait after a finished tool until new live text arrives", () => {
    const failed: Block = {
      id: "shell", kind: "tool", runId: "run-live", title: "coding.shell", state: "failed",
      content: "rg -n session-turn-label",
    };
    const emptyThinking: Block = {
      id: "think", kind: "thinking", runId: "run-live", content: "", state: "streaming",
    };
    const fallback: Block = {
      id: "fallback", kind: "commentary", runId: "run-live", title: "progress", state: "streaming",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    };
    expect(hasVisibleLiveProgress(failed)).toBe(false);
    expect(hasVisibleLiveProgress(emptyThinking)).toBe(false);
    expect(hasVisibleLiveProgress(fallback)).toBe(false);
    expect(shouldShowThinkingWait([failed, emptyThinking, fallback], {
      waiting: true, activeRunId: "run-live",
    })).toBe(true);
    expect(shouldShowThinkingWait([
      failed,
      { id: "live", kind: "thinking", runId: "run-live", content: "先改等待 pill", state: "streaming" },
    ], { waiting: true, activeRunId: "run-live" })).toBe(false);
    expect(shouldShowThinkingWait([
      failed,
      { id: "say", kind: "commentary", runId: "run-live", content: "我先核对失败原因。", state: "streaming" },
    ], { waiting: true, activeRunId: "run-live" })).toBe(false);
  });

  it("only folds process trails that contain tools or diffs", () => {
    expect(processTrailWorthFolding([
      { id: "t1", kind: "thinking", content: "plan", state: "completed" },
    ])).toBe(false);
    expect(processTrailWorthFolding([
      { id: "c1", kind: "commentary", content: "先说明范围", state: "completed" },
    ])).toBe(false);
    expect(processTrailWorthFolding([
      { id: "t1", kind: "thinking", content: "plan", state: "completed" },
      { id: "c1", kind: "commentary", content: "先说明范围", state: "completed" },
    ])).toBe(false);
    expect(processTrailWorthFolding([
      { id: "t1", kind: "thinking", content: "plan", state: "completed" },
      { id: "tool1", kind: "tool", title: "coding.search", state: "completed" },
    ])).toBe(true);
    expect(processTrailWorthFolding([
      { id: "diff1", kind: "diff", title: "a.ts", state: "ready" },
    ])).toBe(true);
  });

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

  it("keeps a process live while tools are queued or waiting for approval", () => {
    expect(isActiveProcessBlock(tool("edit", "coding.edit_hashline", "queued"))).toBe(true);
    expect(isActiveProcessBlock(tool("write", "coding.write_file", "awaiting_approval"))).toBe(true);
    const segments = segmentProcessTrail([
      tool("edit-1", "coding.edit_hashline", "queued"),
      tool("edit-2", "coding.edit_hashline", "queued"),
    ]);
    expect(segments[0]).toMatchObject({ kind: "process", active: true });
  });

  it("queues only when another tool in the same run is executing", () => {
    const idle: Block[] = [
      { id: "edit-1", kind: "tool", runId: "run-idle", title: "coding.edit_hashline", state: "queued" },
      { id: "edit-2", kind: "tool", runId: "run-idle", title: "coding.edit_hashline", state: "queued" },
    ];
    expect(isCapacityQueued(idle[0]!, idle)).toBe(false);
    expect(displayedToolState(idle[0]!, idle)).toBe("running");

    const reviewing: Block[] = [
      { id: "shell-1", kind: "tool", runId: "run-review", title: "coding.shell", state: "reviewing_approval" },
      { id: "shell-2", kind: "tool", runId: "run-review", title: "coding.shell", state: "queued" },
    ];
    expect(isCapacityQueued(reviewing[1]!, reviewing)).toBe(false);
    expect(displayedToolState(reviewing[1]!, reviewing)).toBe("running");

    const busy: Block[] = [
      { id: "shell-running", kind: "tool", runId: "run-busy", title: "coding.shell", state: "running" },
      { id: "shell-wait", kind: "tool", runId: "run-busy", title: "coding.shell", state: "queued" },
    ];
    expect(isCapacityQueued(busy[1]!, busy)).toBe(true);
    expect(displayedToolState(busy[1]!, busy)).toBe("queued");
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

  it("only folds process trails that actually ran tools", () => {
    expect(processTrailWorthFolding([
      { id: "think", kind: "thinking", content: "plan", state: "completed" },
    ])).toBe(false);
    expect(processTrailWorthFolding([
      { id: "note", kind: "commentary", content: "下一步读入口", state: "completed" },
    ])).toBe(false);
    expect(processTrailWorthFolding([
      { id: "think", kind: "thinking", content: "plan", state: "completed" },
      { id: "read", kind: "tool", title: "coding.read_file", state: "completed" },
    ])).toBe(true);
  });
});

describe("tool timeline grouping", () => {
  /**
   * UI-012: an appended block must reconcile in place. Encoding the collection
   * size in the key remounts the whole trail, resetting every expanded tool and
   * replaying step entrances on each delta.
   */
  it("keeps process, tool-group, and thinking-trail identities stable as blocks append", () => {
    const grow = (count: number): Block[] => Array.from({ length: count }, (_, index) => ({
      id: `tool-${index}`, kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
    }));

    const [before] = segmentProcessTrail(grow(3));
    const [after] = segmentProcessTrail(grow(4));
    expect(before?.kind).toBe("process");
    expect(after && "id" in after ? after.id : "").toBe(before && "id" in before ? before.id : "?");

    const groupId = (count: number) => {
      const entry = groupTimelineBlocks(grow(count), "zh-CN")[0];
      return entry && "id" in entry ? entry.id : "";
    };
    expect(groupId(4)).toBe(groupId(3));

    const trailId = (count: number) => {
      const blocks: Block[] = [
        { id: "think-1", kind: "thinking", runId: "run", content: "核对入口", state: "completed" },
        ...grow(count),
      ];
      const entry = groupProcessTimelineBlocks(blocks, "zh-CN")[0];
      return entry && "id" in entry ? entry.id : "";
    };
    expect(trailId(4)).toBe(trailId(3));
    expect(trailId(3)).toBe("thinking-trail-think-1");
  });

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

  it("attaches following tools to any commentary announcement", () => {
    const commentary: Block = {
      id: "progress", kind: "commentary", state: "completed",
      content: "**读取当前前端结构**\nTimeline 与事件投影",
    };
    const prose: Block = {
      id: "legacy", kind: "commentary", state: "completed",
      content: "旧会话仍然保留自然段。",
    };
    const entries = groupProcessTimelineBlocks([
      commentary,
      tool("read", "coding.read_file"),
      tool("search", "coding.search"),
      prose,
    ], "zh-CN");

    expect(entries.map((entry) => entry.kind)).toEqual(["model-progress", "model-progress"]);
    expect(entries[0]).toMatchObject({
      kind: "model-progress",
      presentation: { title: "读取当前前端结构", detail: "Timeline 与事件投影" },
      blocks: [{ id: "read" }, { id: "search" }],
    });
    expect(entries[1]).toMatchObject({ kind: "model-progress", block: { id: "legacy" }, blocks: [] });
  });

  it("keeps host fallback commentary as a grouping anchor", () => {
    const fallback: Block = {
      id: "fallback", kind: "commentary", state: "completed",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    };
    const legacy: Block = {
      id: "legacy", kind: "commentary", state: "completed",
      content: "正在调用所需工具，并根据实际结果继续。",
    };
    const real: Block = {
      id: "real", kind: "commentary", state: "completed",
      content: "我先核对入口，再根据结果继续。",
    };
    const entries = groupProcessTimelineBlocks([
      fallback,
      tool("read", "coding.read_file"),
      tool("search", "coding.search"),
    ], "zh-CN");

    expect(isHostFallbackCommentary(fallback)).toBe(true);
    expect(isHostFallbackCommentary(legacy)).toBe(true);
    expect(isHostFallbackCommentary(real)).toBe(false);
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      kind: "tool-group",
      blocks: [{ id: "read" }, { id: "search" }],
    });
  });

  it("merges adjacent thinking spans without commentary into one trail", () => {
    const entries = groupProcessTimelineBlocks([
      { id: "t1", kind: "thinking", state: "completed", content: "看分组", data: { elapsedMs: "1200" } },
      { id: "t2", kind: "thinking", state: "completed", content: "再看时长", data: { elapsedMs: "1300" } },
      { id: "t3", kind: "thinking", state: "completed", content: "不要拆行", data: { elapsedMs: "1700" } },
    ], "zh-CN");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      kind: "thinking-trail",
      blocks: [{ id: "t1" }, { id: "t2" }, { id: "t3" }],
    });
  });

  it("does not let hidden fallbacks split thinking headers", () => {
    const fallback = (id: string): Block => ({
      id, kind: "commentary", state: "completed",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    });
    const entries = groupProcessTimelineBlocks([
      fallback("fallback-1"),
      { id: "t1", kind: "thinking", state: "completed", content: "先读入口", data: { elapsedMs: "1200" } },
      tool("read", "coding.read_file"),
      fallback("fallback-2"),
      { id: "t2", kind: "thinking", state: "completed", content: "再搜调用", data: { elapsedMs: "1300" } },
      tool("search", "coding.search"),
    ], "zh-CN");
    expect(entries).toHaveLength(1);
    expect(entries[0]?.kind).toBe("thinking-trail");
    if (entries[0]?.kind !== "thinking-trail") throw new Error("expected thinking-trail");
    expect(entries[0].blocks.map((block) => block.id)).toEqual(["t1", "read", "t2", "search"]);
  });

  it("keeps one thinking trail under real model commentary", () => {
    const entries = groupProcessTimelineBlocks([
      {
        id: "progress", kind: "commentary", state: "completed",
        content: "我准备先查块高度是从哪算出来的。",
      },
      { id: "t1", kind: "thinking", state: "completed", content: "看 processElapsedMs" },
      { id: "t2", kind: "thinking", state: "completed", content: "再对一下调用点" },
      tool("read", "coding.read_file"),
    ], "zh-CN");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      kind: "model-progress",
      block: { id: "progress" },
      blocks: [{ id: "t1" }, { id: "t2" }, { id: "read" }],
    });
  });

  it("absorbs hidden fallbacks into the preceding real commentary group", () => {
    const fallback: Block = {
      id: "fallback", kind: "commentary", state: "completed",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    };
    const entries = groupProcessTimelineBlocks([
      { id: "progress", kind: "commentary", state: "completed", content: "我先核对入口。" },
      { id: "t1", kind: "thinking", state: "completed" },
      tool("read", "coding.read_file"),
      fallback,
      { id: "t2", kind: "thinking", state: "completed" },
      tool("search", "coding.search"),
    ], "zh-CN");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      kind: "model-progress",
      blocks: [{ id: "t1" }, { id: "read" }, { id: "t2" }, { id: "search" }],
    });
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

  it("does not preview raw hashline bodies", () => {
    const presentation = formatToolPresentation(JSON.stringify({
      input: "¶frontend/src/i18n.ts#1A65 replace 36:\n+ recapTitle: \"secret recap copy\"",
    }), "zh-CN");
    expect(presentation.preview).toContain("i18n.ts");
    expect(presentation.preview).not.toContain("secret recap copy");
    expect(presentation.preview).not.toContain("replace 36");
    expect(presentation.fields.every((field) => !field.value.includes("secret recap copy"))).toBe(true);
  });

  it("humanizes commands and search queries", () => {
    expect(formatToolPresentation(`{"command":"bun run test"}`, "en").preview).toBe("bun run test");
    expect(formatToolPresentation(`{"query":"useComposerModels","path":"frontend/src"}`, "zh-CN").preview).toContain("useComposerModels");
  });

  it("keeps thinking out of tool-chip counts and file-change pills", () => {
    const thinking: Block = { id: "think", kind: "thinking", content: "Planning…", state: "completed" };
    const queuedWrite = tool("write", "coding.write_file", "queued");
    queuedWrite.data = { arguments: JSON.stringify({ path: "src/secret.ts", content: "nope\n" }) };
    expect(processGroupCounts([thinking, queuedWrite, tool("read", "coding.read_file")])).toEqual({
      tools: 2, messages: 0,
    });
    expect(fileChangePillsForBlocks([thinking, queuedWrite]).files).toEqual([]);
  });

  it("formats thinking duration from tenths of a second and keeps the 思考 label clockless", () => {
    expect(formatThinkingDuration(0)).toBe("");
    expect(formatThinkingDuration(99)).toBe("");
    expect(formatThinkingDuration(100)).toBe("0.1s");
    expect(formatThinkingDuration(300)).toBe("0.3s");
    expect(formatThinkingDuration(1_200)).toBe("1.2s");
    expect(formatThinkingDuration(12_400)).toBe("12.4s");
    expect(formatThinkingDuration(65_000)).toBe("1m05s");
    expect(thinkingStateLabel("zh-CN", true)).toBe("思考");
    expect(thinkingStateLabel("zh-CN", false)).toBe("思考");
    expect(thinkingStateLabel("en", true)).toBe("Thinking");
    expect(thinkingStateLabel("en", false)).toBe("Thinking");
  });

  it("sums thinking spans for one trail clock and ignores tool duration", () => {
    expect(thinkingTraceElapsedMs([
      { id: "t1", kind: "thinking", data: { elapsedMs: "1200" } },
      { id: "t2", kind: "thinking", data: { elapsedMs: "1300" } },
      { id: "t3", kind: "thinking", data: { elapsedMs: "1700" } },
      { id: "t4", kind: "thinking", data: { elapsedMs: "500" } },
      { id: "t5", kind: "thinking", data: { elapsedMs: "22700" } },
      tool("read", "coding.read_file"),
    ])).toBe(27_400);
  });
});
