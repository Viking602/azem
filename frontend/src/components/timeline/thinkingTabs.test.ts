import { describe, expect, it } from "vitest";
import type { Block } from "../../types";
import {
  activityBarLabel, collectThinkingTrace, defaultThinkingTab, hasLiveSparkleWork, isCodingLikeTool,
  isSearchLikeTool, parseSearchTrace, populatedThinkingTabs, processActivityVisibility,
  resolveLiveThinkingTab, shouldShowThinkingTablist, thinkingSearchIsWeb, thinkingTabHasContent,
  thinkingWorkBlocks, visibleSearchHits,
} from "./thinkingTabs";

function tool(id: string, title: string, content = "", state = "completed"): Block {
  return { id, kind: "tool", title, content, state };
}

describe("thinking trace classification", () => {
  it("keeps search, coding, reasoning, and steps on real Azem tools", () => {
    const thinking: Block = { id: "t", kind: "thinking", content: "核对入口", state: "completed" };
    const search = tool("s", "coding.search", JSON.stringify({ query: "FrameToolCall" }));
    const web = tool("w", "web_search", JSON.stringify({
      query: "azem desktop",
      results: [{ title: "Azem desktop guide", url: "https://example.com/desktop" }],
    }));
    const edit = tool("e", "coding.edit_hashline", JSON.stringify({ path: "frontend/src/Timeline.tsx" }));
    const shell = tool("sh", "coding.shell", JSON.stringify({ command: "bun run test" }));
    const read = tool("r", "coding.read_file", JSON.stringify({ path: "frontend/src/App.tsx" }));
    const parts = collectThinkingTrace([thinking, search, web, edit, shell, read]);

    expect(isSearchLikeTool(search)).toBe(true);
    expect(isSearchLikeTool(web)).toBe(true);
    expect(isSearchLikeTool(read)).toBe(false);
    expect(isCodingLikeTool(edit)).toBe(true);
    expect(isCodingLikeTool(shell)).toBe(true);
    expect(isCodingLikeTool(search)).toBe(false);
    expect(parts.reasoning.map((block) => block.id)).toEqual(["t"]);
    expect(parts.search.map((block) => block.id)).toEqual(["s", "w"]);
    expect(parts.coding.map((block) => block.id)).toEqual(["e", "sh"]);
    expect(parts.steps.map((block) => block.id)).toEqual(["s", "w", "e", "sh", "r"]);
    expect(defaultThinkingTab(parts)).toBe("steps");
    expect(thinkingTabHasContent(parts, "coding")).toBe(true);
    expect(thinkingTabHasContent({ ...parts, coding: [] }, "coding")).toBe(false);
    expect(resolveLiveThinkingTab({ ...parts, coding: [] }, "coding")).toBe("steps");
    expect(resolveLiveThinkingTab(parts, "coding")).toBe("coding");
    expect(populatedThinkingTabs(parts)).toEqual(["reasoning", "steps", "search", "coding"]);
    expect(shouldShowThinkingTablist(parts)).toBe(true);
    expect(populatedThinkingTabs({ steps: [], reasoning: [thinking], search: [], coding: [] })).toEqual(["reasoning"]);
    expect(shouldShowThinkingTablist({ steps: [], reasoning: [thinking], search: [], coding: [] })).toBe(false);
    expect(thinkingWorkBlocks(parts).map((block) => block.id)).toEqual(["e", "sh", "r"]);
    expect(thinkingSearchIsWeb(parts)).toBe(true);
    expect(thinkingSearchIsWeb({ ...parts, search: [search] })).toBe(false);
  });

  it("parses web and code search hits without inventing vendors", () => {
    const web = parseSearchTrace(tool("w", "web_search", JSON.stringify({
      query: "azem desktop",
      results: [
        { title: "Azem desktop guide", url: "https://example.com/desktop" },
        { title: "Azem README", url: "https://github.com/Viking602/azem" },
      ],
    })));
    expect(web.query).toBe("azem desktop");
    expect(web.hits.map((hit) => [hit.title, hit.href])).toEqual([
      ["Azem desktop guide", "https://example.com/desktop"],
      ["Azem README", "https://github.com/Viking602/azem"],
    ]);
    expect(web.hits.join("")).not.toMatch(/waffle|Joy Cone|Webstaurant/iu);

    const code = parseSearchTrace(tool(
      "s",
      "coding.search",
      `${JSON.stringify({ query: "FrameToolCall", path: "frontend/src" })}\nfrontend/src/store.ts:12: export function FrameToolCall`,
    ));
    expect(code.query).toBe("FrameToolCall");
    expect(code.hits[0]).toMatchObject({ title: "store.ts", detail: expect.stringContaining("frontend/src/store.ts:12") });

    const preview = visibleSearchHits(Array.from({ length: 8 }, (_, index) => ({ title: `hit-${index}` })), false);
    expect(preview.visible).toHaveLength(5);
    expect(preview.hidden).toBe(3);
  });
});

describe("process activity bar", () => {
  it("changes only the sparkle label for wait, search, and tools", () => {
    const thinking: Block = { id: "t", kind: "thinking", content: "规划日程", state: "streaming" };
    const search = tool("s", "coding.search", JSON.stringify({ query: "churn" }), "running");
    const web = tool("w", "web_search", JSON.stringify({ query: "azem" }), "running");
    const write = tool("e", "coding.write_file", JSON.stringify({ path: "src/a.tsx" }), "running");

    expect(activityBarLabel([], "zh-CN", { waiting: true })).toBe("思考");
    expect(activityBarLabel([thinking], "zh-CN", { live: true })).toBe("思考");
    // Live, the bar names the row that is executing, so it keeps moving instead
    // of freezing on one count for minutes.
    expect(activityBarLabel([thinking, search], "zh-CN", { live: true })).toBe("搜索代码");
    expect(activityBarLabel([thinking, web], "zh-CN", { live: true })).toBe("搜索了网页");
    expect(activityBarLabel([thinking, write], "zh-CN", { live: true })).toBe("写入文件");
    expect(activityBarLabel(
      [thinking, write, tool("s2", "coding.shell", "", "running"), tool("done", "coding.read_file")],
      "zh-CN",
      { live: true },
    )).toBe("正在运行 2 个工具");
    // Settled, it summarizes the whole step.
    expect(activityBarLabel(
      [thinking, tool("done", "coding.read_file"), tool("done2", "coding.shell")],
      "zh-CN",
    )).toBe("运行了 2 个工具");
    expect(hasLiveSparkleWork([thinking])).toBe(true);
    expect(hasLiveSparkleWork([tool("q", "coding.search", "", "queued")])).toBe(false);
    expect(processActivityVisibility([thinking], false)).toMatchObject({
      showBar: true, omitThinking: true, omitLiveTools: true,
    });
    expect(processActivityVisibility([
      { ...thinking, state: "completed" },
      { ...write, state: "completed" },
    ], true)).toMatchObject({
      showBar: true, omitThinking: false, omitLiveTools: false,
    });
  });

  it("summarizes a settled trail on the same bar instead of a second card", () => {
    const thinking: Block = { id: "t", kind: "thinking", content: "规划日程", state: "completed" };
    const done = (id: string, name: string) => tool(id, name, "", "completed");

    // Nothing is running, so the bar must still report what the step actually did.
    expect(activityBarLabel([thinking, done("e", "coding.write_file")], "zh-CN")).toBe("运行了 1 个工具");
    expect(activityBarLabel(
      [thinking, done("e", "coding.write_file"), done("r", "coding.read_file")],
      "zh-CN",
    )).toBe("运行了 2 个工具");
    // A search-only step keeps the search wording rather than a tool count.
    expect(activityBarLabel([done("s", "coding.search")], "zh-CN")).toBe("搜索了代码");
    expect(activityBarLabel([done("w", "web_search")], "zh-CN")).toBe("搜索了网页");
    // Thinking-only keeps 思考; the overall clock lives on that sparkle bar.
    expect(activityBarLabel([thinking], "zh-CN")).toBe("思考");
  });
});
