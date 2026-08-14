import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import { collectConversationSources, imageSourceTitle, isWebResearchTool } from "./inspectorSources";

function block(partial: Partial<Block>): Block {
  return { id: "block", kind: "user", ...partial };
}

describe("inspectorSources", () => {
  it("renames generic image attachments instead of repeating image", () => {
    expect(imageSourceTitle("image.png", 1, "zh-CN")).toBe("图片 1");
    expect(imageSourceTitle("image", 2, "en")).toBe("Image 2");
    expect(imageSourceTitle("pasted-image-20260814-120000.png", 1, "zh-CN")).toBe("粘贴图片 1");
    expect(imageSourceTitle("screen.png", 1, "zh-CN")).toBe("screen.png");
  });

  it("collects user images, typed URLs, and web-search results without treating code search as a source", () => {
    const sources = collectConversationSources([
      block({
        id: "user-1",
        content: "对照 https://github.com/Viking602/azem 和这张图",
        attachments: [
          { id: "img-1", name: "image.png", mimeType: "image/png", path: "/tmp/image.png", size: 12 },
          { id: "img-2", name: "diagram.svg", mimeType: "image/svg+xml", path: "/tmp/diagram.svg", size: 20 },
        ],
      }),
      block({
        id: "search-1",
        kind: "tool",
        title: "web_search",
        content: JSON.stringify({
          query: "azem desktop",
          results: [
            { title: "Azem desktop guide", url: "https://example.com/desktop/" },
            { title: "Azem desktop guide", url: "https://example.com/desktop" },
          ],
        }),
      }),
      block({
        id: "code-search",
        kind: "tool",
        title: "coding.search",
        content: JSON.stringify({ path: "frontend/src/App.tsx", query: "https://should-not-appear.example" }),
      }),
    ], "zh-CN");

    expect(sources.map((item) => [item.kind, item.title])).toEqual([
      ["image", "图片 1"],
      ["image", "diagram.svg"],
      ["input-url", "github.com · azem"],
      ["search-url", "Azem desktop guide"],
    ]);
    expect(sources[3]?.href).toBe("https://example.com/desktop");
  });

  it("recognizes common web research tool names", () => {
    expect(isWebResearchTool("web_search")).toBe(true);
    expect(isWebResearchTool("mcp.exa.search")).toBe(true);
    expect(isWebResearchTool("web.fetch")).toBe(true);
    expect(isWebResearchTool("coding.search")).toBe(false);
  });
});
