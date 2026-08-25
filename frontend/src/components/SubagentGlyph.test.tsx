// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { subagentVisualIdentity } from "../subagents";
import SubagentGlyph from "./SubagentGlyph";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("SubagentGlyph", () => {
  it("maps specialized assignments to distinct Azem role marks", () => {
    expect(subagentVisualIdentity({ id: "architecture", type: "review", description: "审查架构与文档一致性" }).kind).toBe("architecture");
    expect(subagentVisualIdentity({ id: "security", type: "review", description: "审查安全边界" }).kind).toBe("security");
    expect(subagentVisualIdentity({ id: "frontend", type: "review", description: "审查前端交互状态" }).kind).toBe("interface");
    expect(subagentVisualIdentity({ id: "backend", type: "review", description: "审查 Go 后端运行时" }).kind).toBe("systems");
    expect(subagentVisualIdentity({ id: "verify", type: "verify", description: "执行验证矩阵" }).kind).toBe("verify");
  });

  it("renders an original vector mark without Codex-style petals", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(<SubagentGlyph
      agent={{ id: "security", type: "review", description: "审查安全边界", state: "running" }}
      size={28}
    />));

    const glyph = container.querySelector<HTMLElement>(".subagent-glyph");
    expect(glyph?.getAttribute("data-symbol")).toBe("security");
    expect(glyph?.querySelector("svg")).not.toBeNull();
    expect(glyph?.querySelectorAll(":scope > i")).toHaveLength(0);
    expect(glyph?.querySelector(".subagent-glyph-anchor")).not.toBeNull();

    await act(async () => root.unmount());
  });
  it("keeps the drawer, page, rows, glyphs, and motion definitions together", () => {
    const styles = readFileSync("src/styles/subagents.css", "utf8");
    for (const selector of [".subagents-drawer-layer", ".subagents-page", ".subagent-row > button", ".subagent-glyph", "@keyframes subagents-drawer-backdrop-in"]) {
      expect(styles).toContain(selector);
    }
  });

});
