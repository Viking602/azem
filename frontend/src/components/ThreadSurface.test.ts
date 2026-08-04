import { describe, expect, it } from "vitest";
import type { SkillEntry } from "../types";
import { parseSkillPrompt, skillTitle, slashSuggestions } from "./ThreadSurface";

const skills: SkillEntry[] = [
  { name: "animation-systems", description: "Build polished motion", sourcePath: "~/.agents/skills/animation-systems", bundled: false, eager: false, disabled: false, resourceCount: 0 },
  { name: "verify", description: "Audit, verify, then explain simply", sourcePath: "bundled/verify", bundled: true, eager: false, disabled: false, resourceCount: 0 },
  { name: "disabled-skill", description: "Hidden", sourcePath: "", bundled: false, eager: false, disabled: true, resourceCount: 0 },
];

describe("composer slash commands", () => {
  it("lists settings commands and filters enabled skills", () => {
    const items = slashSuggestions("/", skills, "zh-CN", { provider: "chatgpt", contextPercent: 74 });
    expect(items.some((item) => item.value === "/new")).toBe(true);
    expect(items.some((item) => item.value === "/mcp")).toBe(true);
    expect(items.some((item) => item.value === "/compact" && item.detail.includes("74%"))).toBe(true);
    expect(items.some((item) => item.value === "/fast")).toBe(true);
    expect(items.some((item) => item.value === "/skill:animation-systems")).toBe(true);
    expect(items.some((item) => item.value === "/skill:disabled-skill")).toBe(false);
  });

  it("matches skills by name or description and shows source badges", () => {
    expect(slashSuggestions("/ani", skills, "zh-CN").map((item) => item.value)).toEqual(["/skill:animation-systems"]);
    expect(slashSuggestions("/polish", skills, "en").map((item) => item.value)).toEqual(["/skill:animation-systems"]);
    const verify = slashSuggestions("/verify", skills, "zh-CN").find((item) => item.kind === "skill");
    expect(verify).toMatchObject({ label: "Verify", badge: "内置" });
    const personal = slashSuggestions("/animation", skills, "zh-CN").find((item) => item.kind === "skill");
    expect(personal).toMatchObject({ badge: "个人" });
  });

  it("hides chatgpt-only commands for other providers", () => {
    expect(slashSuggestions("/", skills, "en", { provider: "xai" }).some((item) => item.value === "/fast")).toBe(false);
  });

  it("formats skill titles and turns a selected skill into the real turn instruction", () => {
    expect(skillTitle("animation-systems")).toBe("Animation Systems");
    expect(parseSkillPrompt("/skill:verify 检查改动", "zh-CN")).toEqual({ name: "verify", instruction: "检查改动" });
    expect(parseSkillPrompt("/skill:verify", "zh-CN")?.instruction).toContain("verify");
  });
});
