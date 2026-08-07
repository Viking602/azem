import { describe, expect, it } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import type { SkillEntry } from "../types";
import { filterModelControlOptions, modelControlWidth, namedClipboardImage, nextModelControlView, parseSkillPrompt, pastedImages, shouldReadNativeClipboard, skillTitle, slashSuggestions, supportsFastMode } from "./ThreadSurface";

const styles = readFileSync("src/styles.css", "utf8");
const threadSurface = readFileSync("src/components/ThreadSurface.tsx", "utf8");

const skills: SkillEntry[] = [
  { name: "animation-systems", description: "Build polished motion", sourcePath: "~/.agents/skills/animation-systems", bundled: false, eager: false, disabled: false, resourceCount: 0 },
  { name: "verify", description: "Audit, verify, then explain simply", sourcePath: "bundled/verify", bundled: true, eager: false, disabled: false, resourceCount: 0 },
  { name: "disabled-skill", description: "Hidden", sourcePath: "", bundled: false, eager: false, disabled: true, resourceCount: 0 },
];

describe("composer slash commands", () => {
	it("does not restart smooth scrolling for every streaming frame", () => {
		expect(threadSurface).toContain('behavior: running ? "instant" : "smooth"');
	});

	it("keeps the model chip compact, expands to the effort panel width, and respects narrow viewports", () => {
		expect([modelControlWidth(false, 1200), modelControlWidth(true, 1200)]).toEqual([190, 248]);
		expect(modelControlWidth(false, 1200, 84)).toBe(84);
		expect(modelControlWidth(false, 1200, 236)).toBe(236);
		expect(modelControlWidth(false, 1200, 420)).toBe(300);
		expect(modelControlWidth(false, 400, 236)).toBe(208);
		expect(modelControlWidth(true, 400)).toBe(208);
		expect(styles).toMatch(/\.model-controls\s*\{[^}]*flex:\s*0 0 auto;/s);
		expect(styles).toMatch(/\.model-controls\s*\{[^}]*--model-control-closed-padding:\s*8px;/s);
		expect(threadSurface).toContain('getPropertyValue("--model-control-closed-padding")');
		expect(styles).toMatch(/\.model-controls > summary\s*\{[^}]*background:\s*transparent;/s);
		expect(styles).toMatch(/\.model-controls > summary:hover\s*\{[^}]*background:\s*var\(--paper-muted\);/s);
		expect(styles).toMatch(/\.model-control-back\s*\{[^}]*width:\s*fit-content;[^}]*min-height:\s*32px;/s);
		expect(styles).not.toMatch(/\.model-control-back:hover[^}]*background:/s);
	});

	it("only exposes fast mode for the ChatGPT subscription provider", () => {
		expect(supportsFastMode("chatgpt")).toBe(true);
		expect(["openai", "grok", "deepseek"].some(supportsFastMode)).toBe(false);
	});

	it("searches model names, IDs, and aliases, then filters by provider", () => {
		const options = [
			{ value: "chatgpt:gpt-5.6-sol", label: "GPT-5.6 Sol", provider: "chatgpt", searchText: "gpt-5.6-sol codex-latest" },
			{ value: "grok:grok-4", label: "Grok 4", provider: "grok", searchText: "grok-4" },
		];
		expect(filterModelControlOptions(options, "latest", "")).toEqual([options[0]]);
		expect(filterModelControlOptions(options, "", "grok")).toEqual([options[1]]);
		expect(filterModelControlOptions(options, "sol", "grok")).toEqual([]);
	});

	it("keeps advanced model controls selected across close and reopen until Back", () => {
		let view = nextModelControlView("effort", "advanced");
		view = nextModelControlView(view, "close");
		view = nextModelControlView(view, "open");
		expect(view).toBe("advanced");
		expect(nextModelControlView(view, "back")).toBe("effort");
		expect(threadSurface).toContain("const transitionModelControlView = useCallback");
		expect(threadSurface).toContain('transitionModelControlView("advanced")');
		expect(threadSurface).toContain('transitionModelControlView("back")');
		expect(threadSurface).toContain('className="model-control-page-stack"');
		expect(threadSurface).toContain('className="model-control-page model-control-page-advanced"');
		expect(threadSurface).toContain('className="model-control-page model-control-page-effort"');
		expect(threadSurface).toContain('animate={{ height: activePageHeight }}');
		expect(threadSurface).toContain('view === "advanced" ? 0 : -pageHeights.advanced');
		expect(threadSurface).toContain('data-transitioning={String(viewTransitioning)}');
		expect(threadSurface).toContain("hoverReady.current && setActiveGroup(group.label)");
		expect(styles).toMatch(/\.model-control-page-stack\s*\{[^}]*display:\s*grid;/s);
		expect(styles).toMatch(/\.model-control-page-effort\s*\{[^}]*align-content:\s*start;/s);
		expect(styles).toMatch(/\.model-control-menu-portal\[data-transitioning="true"\]\s*\{[^}]*overflow:\s*hidden;/s);
	});

  it("lists settings commands and filters enabled skills", () => {
    const items = slashSuggestions("/", skills, "zh-CN", { provider: "chatgpt", contextPercent: 74 });
    expect(items.some((item) => item.value === "/new")).toBe(true);
    expect(items.some((item) => item.value === "/mcp")).toBe(true);
    expect(items.some((item) => item.value === "/compact" && item.detail.includes("74%"))).toBe(true);
    expect(items.some((item) => item.value === "/rebuild")).toBe(true);
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

  it("extracts pasted images from clipboard files without treating text as an attachment", () => {
    const image = new File([new Uint8Array([137, 80, 78, 71])], "screenshot.png", { type: "image/png" });
    const text = new File(["notes"], "notes.txt", { type: "text/plain" });
    const clipboard = { files: [image, text], items: [] } as unknown as DataTransfer;
    expect(pastedImages(clipboard)).toEqual([image]);
  });

  it("falls back to clipboard items when the files collection is empty", () => {
    const image = new File([new Uint8Array([255, 216, 255])], "clipboard.jpg", { type: "image/jpeg" });
    const clipboard = {
      files: [],
      items: [
        { kind: "string", getAsFile: () => null },
        { kind: "file", getAsFile: () => image },
      ],
    } as unknown as DataTransfer;
    expect(pastedImages(clipboard)).toEqual([image]);
  });

  it("gives unnamed clipboard blobs a stable image filename", () => {
    const image = new File([new Uint8Array([137, 80, 78, 71])], "", { type: "image/png" });
    const named = namedClipboardImage(image, new Date("2026-08-04T05:00:00.000Z"), 0);
    expect(named.name).toBe("pasted-image-2026-08-04T05-00-00-000Z.png");
    expect(named.type).toBe("image/png");
  });

  it("uses native clipboard fallback only when WebView exposes neither images nor text", () => {
    const emptyClipboard = { files: [], items: [], types: [] } as unknown as DataTransfer;
    const textClipboard = { files: [], items: [], types: ["text/plain"] } as unknown as DataTransfer;
    expect(shouldReadNativeClipboard(emptyClipboard)).toBe(true);
    expect(shouldReadNativeClipboard(textClipboard)).toBe(false);
  });
});
