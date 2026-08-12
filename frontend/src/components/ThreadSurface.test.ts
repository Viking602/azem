import { describe, expect, it } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import type { SkillEntry, Snapshot } from "../types";
import { branchMenuLayout, effectiveComposerRoute, filterModelControlOptions, modelControlWidth, namedClipboardImage, nextModelControlView, parseSkillPrompt, pastedImages, sessionStageMotion, shouldReadNativeClipboard, skillTitle, slashSuggestions, supportsFastMode, threadHeaderStage } from "./ThreadSurface";
import { visibleCommentaryTitle } from "./Timeline";

const styles = readFileSync("src/styles.css", "utf8");
const prototypeStyles = readFileSync("src/prototype.css", "utf8");
const threadSurface = readFileSync("src/components/ThreadSurface.tsx", "utf8");
const composerModelPicker = readFileSync("src/components/ComposerModelPicker.tsx", "utf8");
const agentSideChat = readFileSync("src/components/AgentSideChat.tsx", "utf8");
const timeline = readFileSync("src/components/Timeline.tsx", "utf8");

const skills: SkillEntry[] = [
  { name: "animation-systems", description: "Build polished motion", sourcePath: "~/.agents/skills/animation-systems", bundled: false, eager: false, disabled: false, modelVisible: true, resourceCount: 0 },
  { name: "verify", description: "Audit, verify, then explain simply", sourcePath: "bundled/verify", bundled: true, eager: false, disabled: false, modelVisible: true, resourceCount: 0 },
  { name: "disabled-skill", description: "Hidden", sourcePath: "", bundled: false, eager: false, disabled: true, modelVisible: false, resourceCount: 0 },
];

describe("composer slash commands", () => {
	it("keeps the branch menu inside the visible viewport", () => {
		expect(branchMenuLayout({ top: 180, bottom: 210 }, 240)).toEqual({ placement: "above", maxHeight: 160 });
		expect(branchMenuLayout({ top: 80, bottom: 110 }, 700)).toEqual({ placement: "below", maxHeight: 420 });
		expect(branchMenuLayout({ top: 700, bottom: 730 }, 800)).toEqual({ placement: "above", maxHeight: 420 });
	});

	it("projects the configured plan model while plan mode is active and restores the session model afterwards", () => {
		const snapshot: Snapshot = {
			workspace: "/tmp/azem", sessionId: "session-1", provider: "deepseek", model: "deepseek-v4-flash", reasoning: "max",
			agentMode: "single", language: "zh-CN", approvalMode: "prompt", queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
		};
		const routes = [{ Scope: "plan", Role: "", Label: "Plan", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } }];
		expect(effectiveComposerRoute(snapshot, true, routes)).toEqual({ provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" });
		expect(effectiveComposerRoute(snapshot, false, routes)).toEqual({ provider: "deepseek", model: "deepseek-v4-flash", reasoning: "max" });
		expect(effectiveComposerRoute(snapshot, true, [{ Scope: "plan", Role: "", Label: "Plan", Route: {} }])).toEqual({ provider: "deepseek", model: "deepseek-v4-flash", reasoning: "max" });
	});

	it("does not start native text selection from the composer toolbar blank area", () => {
		expect(styles).toMatch(/\.composer-toolbar\s*\{[^}]*-webkit-user-select:\s*none;[^}]*user-select:\s*none;/s);
		expect(styles).not.toMatch(/\.composer-card\s*\{[^}]*user-select:\s*none;/s);
		expect(styles).not.toMatch(/\.composer-card textarea\s*\{[^}]*user-select:\s*none;/s);
	});

	it("uses desktop selection behavior for chrome while preserving selectable content", () => {
		expect(styles).toMatch(/\.desktop-shell\s*\{[^}]*-webkit-user-select:\s*none;[^}]*user-select:\s*none;[^}]*-webkit-touch-callout:\s*none;/s);
		expect(styles).toMatch(/\.desktop-shell :where\([^}]*input[^}]*\.markdown[^}]*\.workspace-code-viewport[^}]*\)\s*\{[^}]*-webkit-user-select:\s*text;[^}]*user-select:\s*text;/s);
		expect(styles).toMatch(/\.desktop-shell :where\(img, svg\)\s*\{[^}]*-webkit-user-drag:\s*none;/s);
	});

	it("does not restart smooth scrolling for every streaming frame", () => {
		expect(threadSurface).toContain('behavior: running ? "instant" : "smooth"');
	});

	it("keeps the active composer in document flow instead of reserving a viewport-sized blank", () => {
		expect(styles).toMatch(/\.composer-dock\s*\{[^}]*position:\s*relative;[^}]*flex:\s*0 0 auto;[^}]*justify-content:\s*center;/s);
		expect(styles).not.toMatch(/\.thread-surface:has\(\.queued-prompts\) \.transcript\s*\{[^}]*padding-bottom:/s);
		expect(threadSurface).toContain("[blocks, following, queuedPrompts.length, running]");
	});

	it("restores the approved content-stage transition when switching sessions", () => {
		expect(sessionStageMotion(false)).toMatchObject({
			initial: { opacity: 0, y: 7, filter: "blur(2px)" },
			animate: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.24 } },
		});
		expect(sessionStageMotion(true).initial).toBe(false);
		expect(threadSurface).toContain("key={currentSessionId}");
		expect(threadSurface).toContain('className="thread-session-stage"');
		expect(threadSurface).not.toContain("<AnimatePresence");
		expect(styles).toMatch(/\.thread-session-viewport\s*\{[^}]*position:\s*relative;[^}]*overflow:\s*hidden;/s);
		expect(styles).toMatch(/\.thread-session-stage\s*\{[^}]*position:\s*absolute;[^}]*inset:\s*0;/s);
	});

	it("treats thinking and tool execution as one in-progress header state", () => {
		expect(threadHeaderStage(true)).toBe("in-progress");
		expect(threadHeaderStage(false)).toBe("completed");
		expect(threadSurface).toContain('{t("inProgress")}');
		expect(styles).toMatch(/\.thread-stage\s*\{[^}]*grid-template-columns:\s*repeat\(2,/s);
	});

	it("uses an editorial process rail without repeating generic progress labels", () => {
		expect(visibleCommentaryTitle("progress")).toBe("");
		expect(visibleCommentaryTitle("commentary")).toBe("");
		expect(visibleCommentaryTitle("方案结论")).toBe("方案结论");
		expect(styles).toMatch(/\.process-entries::before\s*\{[^}]*linear-gradient/s);
		expect(prototypeStyles).toMatch(/\.process-entries\s*\{[^}]*--process-rail-inset:\s*4px;[^}]*padding:\s*2px 0 4px var\(--process-rail-inset\);/s);
		expect(prototypeStyles).toMatch(/\.process-entries::before\s*\{[^}]*left:\s*calc\(var\(--process-rail-inset\) \+ 7px\);/s);
		expect(styles).toMatch(/\.tool-block summary,[\s\S]*?grid-template-columns:\s*16px minmax\(0, 1fr\) auto;/s);
		expect(prototypeStyles).toMatch(/\.commentary-block\s*\{[^}]*line-height:\s*1\.72;[^}]*text-wrap:\s*pretty;/s);
		expect(prototypeStyles).toMatch(/\.commentary-block\.active \.commentary-marker i\s*\{[^}]*background:\s*var\(--blue\);/s);
		expect(prototypeStyles).toMatch(/\.reasoning-summary\s*\{[^}]*grid-template-columns:\s*15px minmax\(0, 1fr\) auto;/s);
		expect(prototypeStyles).toMatch(/button\.reasoning-summary:hover \.azem-thinking-mark,[\s\S]*?\.timeline-step > summary:hover \.timeline-step-mark,[\s\S]*?\.tool-block summary:hover \.tool-leading,[\s\S]*?\.tool-group summary:hover \.tool-leading\s*\{[^}]*background:\s*transparent;[^}]*box-shadow:\s*none;/s);
		expect(prototypeStyles).toMatch(/\.reasoning-body\s*\{[^}]*margin:\s*-1px 0 5px 7px;[^}]*line-height:\s*1\.68;/s);
		expect(prototypeStyles).toMatch(/\.reasoning-step::before\s*\{[^}]*display:\s*none;/s);
	});

	it("gives the subagent collaboration card a restrained four-sided frame", () => {
		expect(prototypeStyles).toMatch(/\.subagent-run-card\s*\{[^}]*border:\s*1px solid color-mix\([^;]+;[^}]*border-radius:\s*11px;/s);
		expect(prototypeStyles).toMatch(/\.subagent-run-card\s*\{[^}]*background:\s*linear-gradient\([^;]*var\(--paper\)[^;]*var\(--canvas\)[^;]*\);/s);
		expect(prototypeStyles).toMatch(/\.subagent-run-card:hover\s*\{[^}]*border-color:\s*color-mix\([^;]+;/s);
		expect(prototypeStyles).toMatch(/\.subagent-run-card\[data-state="running"\]\s*\{[^}]*border-color:\s*color-mix\([^;]+;/s);
	});

	it("keeps subagent hydration event-driven and isolates composer updates from the timeline", () => {
		expect(agentSideChat).not.toContain("setInterval(inspect");
		expect(agentSideChat).toContain("followTail.current");
		expect(timeline).toContain("export const TimelineFeed = memo");
		expect(timeline).toContain("opened ? formatToolPresentation");
	});

	it("avoids layout-width animation for the model chip and slider fill", () => {
		expect(threadSurface).not.toContain("animateControlWidth");
		expect(styles).toMatch(/\.effort-slider-fill\s*\{[^}]*transition:\s*clip-path/s);
		expect(styles).not.toMatch(/\.effort-slider-fill\s*\{[^}]*will-change:\s*width/s);
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

	it("only exposes fast mode for capable ChatGPT subscription models", () => {
		expect(supportsFastMode("chatgpt", ["tools", "fast"])).toBe(true);
		expect(supportsFastMode("chatgpt", ["tools"])).toBe(false);
		expect(supportsFastMode("deepseek", ["fast"])).toBe(false);
		expect(supportsFastMode("openai", ["fast"])).toBe(false);
	});

	it("keeps the complete new-conversation launcher and direct fast-mode control", () => {
		expect(threadSurface).not.toContain('className="azem-mark empty-launch-mark"');
		expect(threadSurface).toContain('className="empty-composer-heading"><h1>{t("promptTitle")}</h1>');
		expect(threadSurface).toContain('{showContextBar ? <ComposerContextBar /> : null}');
		expect(threadSurface).toContain('className={`effort-panel-speed');
		expect(threadSurface).toContain('onClick={() => onSpeedChange(fastActive ? "standard" : "fast")}');
		expect(threadSurface).toContain('className={`composer-fast-mode');
		expect(threadSurface).toContain('aria-label={snapshot.language === "zh-CN" ? "Fast 模式" : "Fast mode"}');
		expect(threadSurface).toContain('fastAvailable={fastAvailable}');
		expect(composerModelPicker).toContain("const fastAvailable = props.fastAvailable;");
		expect(threadSurface).not.toContain('className="model-controls-fast-icon"');
		expect(composerModelPicker).not.toContain('className="model-controls-fast-icon"');
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-card\s*\{[^}]*padding:\s*11px 13px 10px;/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-context-bar > \.composer-chip:nth-child\(2\)\s*\{[^}]*display:\s*none;/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-branch-menu > summary::before\s*\{[^}]*height:\s*14px;[^}]*transform:\s*translateY\(-50%\);/s);
		expect(prototypeStyles).toMatch(/\.composer-fast-mode > button\s*\{[^}]*width:\s*27px;/s);
		expect(prototypeStyles).toMatch(/\.composer-fast-mode > button\s*\{[^}]*background:\s*transparent;[^}]*color:\s*var\(--faint\);/s);
		expect(prototypeStyles).toMatch(/\.composer-fast-mode > button svg\s*\{[^}]*fill:\s*none;[^}]*stroke:\s*currentColor;/s);
		expect(prototypeStyles).toMatch(/\.composer-fast-mode\.active > button\s*\{[^}]*color:\s*var\(--blue\);/s);
		expect(prototypeStyles).toMatch(/\.composer-fast-mode\.active > button svg\s*\{[^}]*fill:\s*currentColor;[^}]*stroke:\s*currentColor;/s);
		expect(prototypeStyles).toMatch(/\.composer-popover-fast > button\s*\{[^}]*color:\s*var\(--faint\);/s);
		expect(prototypeStyles).toMatch(/\.composer-popover-fast > button\.on svg\s*\{[^}]*fill:\s*currentColor;/s);
		expect(styles).toMatch(/\.effort-panel-speed\.active\s*\{[^}]*color:\s*var\(--blue\);/s);
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
    const items = slashSuggestions("/", skills, "zh-CN", { fastAvailable: true, contextPercent: 74 });
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

  it("hides fast commands when the selected model lacks the capability", () => {
    expect(slashSuggestions("/", skills, "en", { fastAvailable: false }).some((item) => item.value === "/fast")).toBe(false);
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
