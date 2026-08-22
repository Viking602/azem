import { act, createElement, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { liveRevealRanges, Markdown, MAX_LIVE_REVEAL_RANGES, STREAM_REVEAL_MS, StreamingMarkdown } from "./Markdown";

describe("Markdown code blocks", () => {
  const mounted: Array<() => void> = [];
  afterEach(() => mounted.splice(0).forEach((cleanup) => cleanup()));

  async function render(node: ReactNode) {
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(node));
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
    });
    return container;
  }

  it("renders a fenced block with Beautiful UI header, copy, and line numbers", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const source = "```churn.ts\nexport async function churnBatch(flavor: string) {\n  return flavor;\n}\n```";
    const container = await render(createElement(Markdown, { children: source }));
    const card = container.querySelector(".bui-code-block");
    expect(card?.querySelector(".bui-code-filename")?.textContent).toBe("churn.ts");
    expect(card?.querySelector(".bui-code-lang")?.textContent).toBe("TypeScript");
    expect(card?.querySelector(".syntax-keyword")?.textContent).toBe("export");
    expect(Array.from(card?.querySelectorAll(".bui-code-gutter span") ?? []).map((node) => node.textContent)).toEqual(["1", "2", "3"]);
    expect(container.querySelector("p .bui-code-block")).toBeNull();
    const copy = container.querySelector<HTMLButtonElement>(".bui-code-copy")!;
    expect(copy.textContent).toContain("复制");
    await act(async () => copy.click());
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("export async function churnBatch"));
  });

  it("shows only the language label when the info-string is not a filename", async () => {
    const container = await render(createElement(Markdown, { children: "```typescript\nconst ready = true;\n```" }));
    expect(container.querySelector(".bui-code-filename")?.textContent).toBe("TypeScript");
    expect(container.querySelector(".bui-code-lang")).toBeNull();
    expect(container.querySelector(".bui-code-copy")).not.toBeNull();
  });

  it("leaves inline code as ordinary marks instead of a Code Block card", async () => {
    const container = await render(createElement(Markdown, { children: "使用 `测试` 核对。" }));
    expect(container.querySelector(".bui-code-block")).toBeNull();
    expect(container.querySelector("p code")?.textContent).toBe("测试");
  });

  it("keeps a streaming fenced card mounted while lines append", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
    });
    const first = "```churn.ts\nexport async function churnBatch(flavor: string) {\n";
    await act(async () => root.render(createElement(StreamingMarkdown, {
      children: first,
      ranges: [{ id: 0, start: 0, end: first.length }],
    })));
    const card = container.querySelector(".bui-code-block");
    const initialLines = card?.querySelectorAll(".bui-code-gutter span").length ?? 0;
    expect(card?.querySelector(".bui-code-filename")?.textContent).toBe("churn.ts");
    expect(card?.querySelector(".bui-code-copy")).not.toBeNull();
    expect(initialLines).toBeGreaterThan(0);

    const next = `${first}  const base = await getFlavor(flavor);\n`;
    await act(async () => root.render(createElement(StreamingMarkdown, {
      children: next,
      ranges: [{ id: 1, start: first.length, end: next.length }],
    })));
    expect(container.querySelector(".bui-code-block")).toBe(card);
    expect(card?.textContent).toContain("getFlavor");
    expect(card?.querySelectorAll(".bui-code-gutter span").length ?? 0).toBeGreaterThan(initialLines);
  });

  it("keeps only the newest unsettled reveal ranges live so earlier lines stay settled", () => {
    expect(liveRevealRanges([])).toEqual([]);
    expect(liveRevealRanges([
      { id: 1, start: 0, end: 12 },
      { id: 4, start: 40, end: 52 },
      { id: 3, start: 24, end: 40 },
    ])).toEqual([
      { id: 1, start: 0, end: 12 },
      { id: 3, start: 24, end: 40 },
      { id: 4, start: 40, end: 52 },
    ]);
    const now = 10_000;
    expect(liveRevealRanges([
      { id: 1, start: 0, end: 12, bornAt: now - STREAM_REVEAL_MS },
      { id: 2, start: 12, end: 20, bornAt: now - 40 },
    ], now)).toEqual([{ id: 2, start: 12, end: 20, bornAt: now - 40 }]);
    const many = Array.from({ length: MAX_LIVE_REVEAL_RANGES + 3 }, (_, id) => ({
      id, start: id * 4, end: id * 4 + 4,
    }));
    expect(liveRevealRanges(many).map((range) => range.id)).toEqual(
      Array.from({ length: MAX_LIVE_REVEAL_RANGES }, (_, index) => index + 3),
    );
  });

  it("does not wrap already-written paragraphs with reveal spans", async () => {
    const first = "第一段已经写完。\n\n第二段也已经写完。";
    const next = `${first}\n\n第三段正在输出。`;
    const container = document.createElement("div");
    const root = createRoot(container);
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
    });
    const settledAt = Date.now() - STREAM_REVEAL_MS;
    await act(async () => root.render(createElement(StreamingMarkdown, {
      children: first,
      ranges: [
        { id: 0, start: 0, end: 8, bornAt: settledAt },
        { id: 1, start: 10, end: first.length },
      ],
    })));
    expect(container.querySelectorAll("p")).toHaveLength(2);
    expect(container.querySelectorAll(".streaming-text-reveal")).toHaveLength(1);
    expect(container.querySelector("[data-stream-reveal='1']")?.textContent).toBe("第二段也已经写完。");
    expect(container.querySelector("p")?.querySelector(".streaming-text-reveal")).toBeNull();

    await act(async () => root.render(createElement(StreamingMarkdown, {
      children: next,
      ranges: [
        { id: 1, start: 10, end: first.length, bornAt: settledAt },
        { id: 2, start: first.length + 2, end: next.length },
      ],
    })));
    const paragraphs = Array.from(container.querySelectorAll("p"));
    expect(paragraphs).toHaveLength(3);
    expect(paragraphs[0]?.querySelector(".streaming-text-reveal")).toBeNull();
    expect(paragraphs[1]?.querySelector(".streaming-text-reveal")).toBeNull();
    expect(paragraphs[2]?.querySelector(".streaming-text-reveal")?.textContent).toBe("第三段正在输出。");
    expect(container.querySelectorAll(".streaming-text-reveal")).toHaveLength(1);
  });

  it("resumes in-flight reveal motion instead of replaying from the start", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
    });
    const now = 20_000;
    const dateNow = vi.spyOn(Date, "now").mockReturnValue(now);
    await act(async () => root.render(createElement(StreamingMarkdown, {
      children: "正在输出，继续",
      ranges: [{ id: 3, start: 2, end: 4, bornAt: now - 80 }],
    })));
    const reveal = container.querySelector<HTMLElement>(".streaming-text-reveal");
    expect(reveal?.textContent).toBe("输出");
    expect(reveal?.style.animationDelay).toBe("-80ms");
    dateNow.mockRestore();
  });

});
