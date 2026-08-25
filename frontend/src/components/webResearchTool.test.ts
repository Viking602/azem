import { describe, expect, it } from "vitest";
import { isWebResearchTool } from "./webResearchTool";

describe("isWebResearchTool", () => {
  it("recognizes research tools without classifying workspace search", () => {
    expect(isWebResearchTool("web_search")).toBe(true);
    expect(isWebResearchTool("mcp.exa.search")).toBe(true);
    expect(isWebResearchTool("web.fetch")).toBe(true);
    expect(isWebResearchTool("coding.search")).toBe(false);
  });
});
