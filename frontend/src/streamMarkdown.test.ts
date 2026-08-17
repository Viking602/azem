import { describe, expect, it } from "vitest";
import { stabilizeStreamingMarkdown } from "./streamMarkdown";

describe("stabilizeStreamingMarkdown", () => {
  it("closes incomplete bold so raw asterisks never flash", () => {
    expect(stabilizeStreamingMarkdown("先看 **架构")).toBe("先看 **架构**");
    expect(stabilizeStreamingMarkdown("先看 **架构**")).toBe("先看 **架构**");
  });

  it("opens an unfinished fence as a code block instead of a paragraph of backticks", () => {
    expect(stabilizeStreamingMarkdown("```ts\nconst ready = true")).toBe("```ts\nconst ready = true\n```");
    expect(stabilizeStreamingMarkdown("```ts\nconst ready = true\n```")).toBe("```ts\nconst ready = true\n```");
  });

  it("holds back a heading or list marker until the item text exists", () => {
    expect(stabilizeStreamingMarkdown("结果\n##")).toBe("结果\n");
    expect(stabilizeStreamingMarkdown("结果\n## 分层")).toBe("结果\n## 分层");
    expect(stabilizeStreamingMarkdown("步骤\n-")).toBe("步骤\n");
    expect(stabilizeStreamingMarkdown("步骤\n- 读取入口")).toBe("步骤\n- 读取入口");
  });

  it("closes an unfinished markdown link", () => {
    expect(stabilizeStreamingMarkdown("见 [Azem](https://github.com/Viking602/azem")).toBe("见 [Azem](https://github.com/Viking602/azem)");
    expect(stabilizeStreamingMarkdown("见 [Azem](https://github.com/Viking602/azem)")).toBe("见 [Azem](https://github.com/Viking602/azem)");
  });
});
