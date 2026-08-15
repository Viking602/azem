import { describe, expect, it } from "vitest";
import type { Block } from "../../types";
import {
  DEFERRED_PROCESS_MIN_ROWS,
  DEFERRED_PROCESS_WINDOW,
  deferredProcessWindow,
  deferredProgressChip,
  flattenDeferredProcessRows,
} from "./processDefer";

function commentary(id: string, content: string, extra: Partial<Block> = {}): Block {
  return { id, kind: "commentary", runId: "run", state: "completed", content, ...extra };
}

function tool(id: string, title: string, extra: Partial<Block> = {}): Block {
  return { id, kind: "tool", runId: "run", title, state: "completed", ...extra };
}

describe("flattenDeferredProcessRows", () => {
  it("keeps host fallback commentary hidden and flattens tools plus real progress", () => {
    const rows = flattenDeferredProcessRows([
      commentary("fallback", "正在调用所需工具，并根据实际结果继续。", { data: { synthetic: "tool_announcement" } }),
      tool("read", "coding.read_file", { content: JSON.stringify({ path: "a.ts" }) }),
      commentary("real", "**核对入口**\n只保留已验证证据"),
      { id: "think", kind: "thinking", runId: "run", content: "先看边界", state: "completed" },
      tool("search", "coding.search"),
      tool("spawn", "subagent.spawn", { data: { arguments: JSON.stringify({ description: "审查" }) } }),
    ], "zh-CN");

    expect(rows.map((row) => row.kind)).toEqual(["thinking", "tool", "progress", "tool", "subagent"]);
    expect(rows[0]).toMatchObject({ kind: "thinking", id: "thinking-think" });
    expect(rows[1]).toMatchObject({ kind: "tool", id: "tool-read" });
    expect(rows[2]).toMatchObject({ kind: "progress", id: "progress-real" });
    if (rows[2]?.kind === "progress") {
      expect(rows[2].presentation).toEqual({ title: "核对入口", detail: "只保留已验证证据" });
    }
    expect(rows[4]).toMatchObject({ kind: "subagent", id: "subagent-spawn" });
  });

  it("keeps plain commentary as a detail chip when there is no **title**", () => {
    const rows = flattenDeferredProcessRows([
      commentary("plain", "我先核对入口，再根据结果继续。"),
      tool("read", "coding.read_file"),
    ], "zh-CN");
    expect(rows[0]).toMatchObject({ kind: "progress" });
    if (rows[0]?.kind === "progress") {
      expect(rows[0].presentation.detail).toContain("我先核对入口，再根据结果继续。");
    }
  });

  it("merges adjacent thinking spans into one deferred row", () => {
    const rows = flattenDeferredProcessRows([
      { id: "t1", kind: "thinking", runId: "run", content: "看分组", state: "completed", data: { elapsedMs: "1200" } },
      { id: "t2", kind: "thinking", runId: "run", content: "再看时长", state: "completed", data: { elapsedMs: "1300" } },
      { id: "t3", kind: "thinking", runId: "run", content: "不要拆行", state: "completed", data: { elapsedMs: "1700" } },
      tool("read", "coding.read_file"),
    ], "zh-CN");
    expect(rows.map((row) => row.kind)).toEqual(["thinking", "tool"]);
    expect(rows[0]).toMatchObject({ kind: "thinking", id: "thinking-t1" });
    if (rows[0]?.kind === "thinking") {
      expect(rows[0].blocks.map((block) => block.id)).toEqual(["t1", "t2", "t3"]);
    }
  });

  it("puts thinking first when reasoning arrives after tools", () => {
    const rows = flattenDeferredProcessRows([
      tool("read", "coding.read_file"),
      tool("search", "coding.search"),
      { id: "think", kind: "thinking", runId: "run", content: "The user wants me to analyze the package", state: "completed" },
      tool("list", "coding.list_files"),
    ], "zh-CN");
    expect(rows.map((row) => row.kind)).toEqual(["thinking", "tool", "tool", "tool"]);
    expect(rows[0]).toMatchObject({ kind: "thinking", id: "thinking-think" });
  });

  it("flattens grouped settled tools into individual chip rows", () => {
    const rows = flattenDeferredProcessRows([
      tool("a", "coding.read_file"),
      tool("b", "coding.read_file"),
      tool("c", "coding.search"),
    ], "zh-CN");
    expect(rows).toHaveLength(3);
    expect(rows.every((row) => row.kind === "tool")).toBe(true);
  });
});

describe("deferredProcessWindow", () => {
  it("shows every row when the trail is small", () => {
    expect(DEFERRED_PROCESS_MIN_ROWS).toBeLessThan(DEFERRED_PROCESS_WINDOW);
    expect(deferredProcessWindow(8, 0, 720)).toEqual({ start: 0, end: 8 });
  });

  it("keeps the first page when the scrollport is unknown", () => {
    expect(deferredProcessWindow(74, 0, 0)).toEqual({ start: 0, end: DEFERRED_PROCESS_WINDOW });
  });

  it("windows by transcript scroll with overscan", () => {
    const view = deferredProcessWindow(74, 36 * 40, 36 * 10, 0);
    expect(view.start).toBeGreaterThan(0);
    expect(view.end).toBeLessThan(74);
    expect(view.end - view.start).toBeGreaterThanOrEqual(DEFERRED_PROCESS_WINDOW);
    expect(view.start).toBeLessThanOrEqual(40);
    expect(view.end).toBeGreaterThan(50);
  });
});

describe("deferredProgressChip", () => {
  it("uses the parsed title and a bounded detail chip", () => {
    expect(deferredProgressChip({ title: "核对入口", detail: "只保留已验证证据" }, "进度更新"))
      .toEqual({ title: "核对入口", chip: "只保留已验证证据" });
    expect(deferredProgressChip({ title: "", detail: "x".repeat(50) }, "进度更新").title).toBe("进度更新");
    expect(deferredProgressChip({ title: "", detail: "x".repeat(50) }, "进度更新").chip).toHaveLength(42);
  });
});
