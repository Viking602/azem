import { describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync, readdirSync } from "node:fs";
import type { SkillEntry, Snapshot } from "../types";
import { branchMenuLayout, COMPOSER_OVERLAY_CLEARANCE, COMPOSER_OVERLAY_MIN_GAP, composerOverlayGap, composerPromptPlaceholder, effectiveComposerRoute, filterModelControlOptions, modelControlWidth, namedClipboardImage, nextModelControlView, parseSkillPrompt, pastedImages, pinTranscriptTail, sessionStageMotion, shouldReadNativeClipboard, skillTitle, slashSuggestions, supportsFastMode, threadHeaderStage, transcriptFollowBehavior } from "./ThreadSurface";
import { translator } from "../i18n";
import { visibleCommentaryTitle } from "./Timeline";

// styles.css is an import hub; concatenate the imported files in cascade order.
const styles = readFileSync("src/styles.css", "utf8")
	.split("\n")
	.map((line: string) => /^@import "\.\/(.+)";$/.exec(line)?.[1])
	.filter((path: string | undefined): path is string => Boolean(path))
	.map((path: string) => readFileSync(`src/${path}`, "utf8"))
	.join("\n");
const prototypeStyles = readFileSync("src/prototype.css", "utf8");
const beautifulUIStyles = readFileSync("src/components/beautiful-ui/beautiful-ui.css", "utf8");
// ThreadSurface.tsx is a composition root; include its thread/ submodules so
// source-content assertions keep covering the complete composer surface.
const threadSurface = [
	"src/components/ThreadSurface.tsx",
	...readdirSync("src/components/thread")
		.slice()
		.sort()
		.map((name: string) => `src/components/thread/${name}`),
]
	.map((path: string) => readFileSync(path, "utf8"))
	.join("\n");
const composerModelPicker = readFileSync("src/components/ComposerModelPicker.tsx", "utf8");
const agentSideChat = readFileSync("src/components/AgentSideChat.tsx", "utf8");
// Timeline.tsx is a composition root; include its timeline/ submodules so
// source-content assertions keep covering the complete process rendering.
const timeline = [
	"src/components/Timeline.tsx",
	...readdirSync("src/components/timeline")
		.slice()
		.sort()
		.map((name: string) => `src/components/timeline/${name}`),
]
	.map((path: string) => readFileSync(path, "utf8"))
	.join("\n");

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
		const routes = [{ scope: "plan", role: "", label: "Plan", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } }];
		expect(effectiveComposerRoute(snapshot, true, routes)).toEqual({ provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" });
		expect(effectiveComposerRoute(snapshot, false, routes)).toEqual({ provider: "deepseek", model: "deepseek-v4-flash", reasoning: "max" });
		expect(effectiveComposerRoute(snapshot, true, [{ scope: "plan", role: "", label: "Plan", route: {} }])).toEqual({ provider: "deepseek", model: "deepseek-v4-flash", reasoning: "max" });
	});

	it("does not look idle while a run is active", () => {
		const t: ReturnType<typeof translator> = translator("zh-CN");
		expect(composerPromptPlaceholder(t, {
			busy: true, running: true, deliveryMode: "queue", showContextBar: false, language: "zh-CN",
		})).toBe("模型正在思考，输入将排入下一轮…");
		expect(composerPromptPlaceholder(t, {
			busy: true, running: true, deliveryMode: "guide", showContextBar: false, language: "zh-CN",
		})).toBe("引导当前任务…");
		expect(composerPromptPlaceholder(t, {
			busy: true, running: false, deliveryMode: "queue", showContextBar: false, language: "zh-CN",
		})).toBe("输入下一轮消息…");
		expect(threadSurface).toContain("composerPromptPlaceholder(t, { busy, running, deliveryMode, showContextBar, language: snapshot.language })");
		expect(threadSurface).toContain("waitingForModel={running}");
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
		expect(transcriptFollowBehavior(true)).toBe("instant");
		expect(transcriptFollowBehavior(false)).toBe("smooth");
		expect(transcriptFollowBehavior(false, true)).toBe("instant");
		expect(threadSurface).toContain("transcriptFollowBehavior(running, pinInstant.current)");
	});

	it("opens a switched session at the tail instead of the first line", () => {
		expect(threadSurface).toContain("sessionFollow.current !== currentSessionId");
		expect(threadSurface).toContain("pinInstant.current = true");
		expect(threadSurface).toContain("querySelector(\".transcript\")");
		expect(threadSurface).toContain("typeof ResizeObserver === \"undefined\"");
		expect(threadSurface).toContain("observer.observe(transcript)");
		expect(threadSurface).toContain("[blocks, following, queuedPrompts.length, running, currentSessionId]");
		const viewport = { scrollHeight: 2400, scrollTo: vi.fn() };
		pinTranscriptTail(viewport as unknown as HTMLElement, "instant");
		expect(viewport.scrollTo).toHaveBeenCalledWith({ top: 2400, behavior: "instant" });
	});

	it("overlays a transparent composer dock so the timeline stays visible and scrollable", () => {
		expect(COMPOSER_OVERLAY_CLEARANCE).toBe(24);
		expect(COMPOSER_OVERLAY_MIN_GAP).toBe(148);
		expect(composerOverlayGap(0)).toBe(COMPOSER_OVERLAY_MIN_GAP);
		expect(composerOverlayGap(140)).toBe(140 + COMPOSER_OVERLAY_CLEARANCE);
		expect(composerOverlayGap(156)).toBe(156 + COMPOSER_OVERLAY_CLEARANCE);
		expect(threadSurface).toContain('className="transcript-composer-clearance"');
		expect(styles).toMatch(/\.transcript-composer-clearance\s*\{[^}]*height:\s*var\(--transcript-bottom-gap\)/s);
		expect(styles).toMatch(/\.thread-session-stage\s*\{[^}]*--transcript-bottom-gap:\s*148px/s);
		expect(styles).toMatch(/\.transcript\s*\{[^}]*padding:\s*31px 0 0/s);
		expect(styles).toMatch(/\.composer-dock\s*\{[^}]*position:\s*absolute;[^}]*background:\s*transparent;[^}]*pointer-events:\s*none;/s);
		expect(styles).toMatch(/\.composer-dock \.composer-stack,\s*\.composer-dock \.jump-latest\s*\{[^}]*pointer-events:\s*auto;/s);
		expect(styles).not.toMatch(/\.composer-dock \.composer-card::before/);
		expect(styles).not.toMatch(/\.composer-dock \.composer-stack::before/);
		expect(styles).not.toMatch(/\.composer-dock \.composer-card::after/);
		expect(styles).not.toMatch(/\.composer-dock \.composer-stack::after/);
		expect(styles).not.toMatch(/\.composer-dock\s*\{[^}]*background:\s*var\(--paper\)/s);
		expect(styles).not.toMatch(/\.thread-surface:has\(\.queued-prompts\) \.transcript\s*\{[^}]*padding-bottom:/s);
		expect(threadSurface).toContain("composerOverlayGap(node.offsetHeight)");
		expect(threadSurface).toContain("COMPOSER_OVERLAY_CLEARANCE");
		expect(threadSurface).toContain("new ResizeObserver(sync)");
		expect(threadSurface).toContain("useLayoutEffect(() => {");
		expect(threadSurface).toContain("[blocks, following, queuedPrompts.length, running, currentSessionId]");
		expect(threadSurface).toContain('className="empty-composer-wrap"');
		expect(styles).toMatch(/\.empty-composer-wrap\s*\{[^}]*padding:\s*20px 32px 80px;/s);
		expect(styles).not.toMatch(/\.empty-composer-wrap\s*\{[^}]*--transcript-bottom-gap/s);
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

	it("scopes chat UI and code font sizes to the thread and side-chat surfaces", () => {
		expect(styles).toMatch(/\.thread-surface,\s*\n\.agent-side-chat\s*\{[^}]*--text-chat:\s*var\(--chat-ui-font-size/s);
		expect(styles).toMatch(/\.thread-surface \.bui-code-block,\s*\n\.agent-side-chat \.bui-code-block\s*\{[^}]*--bui-code-size:\s*var\(--chat-code-font-size/s);
		expect(threadSurface).toContain("chatTypographyVars(chatFontSize, chatCodeFontSize)");
		expect(agentSideChat).toContain("chatTypographyVars(chatFontSize, chatCodeFontSize)");
		expect(beautifulUIStyles).toMatch(/\.bui-code-block\s*\{[^}]*--bui-code-size:\s*var\(--chat-code-font-size/s);
		expect(beautifulUIStyles).toMatch(/\.bui-code-block\s*\{[^}]*--bui-code-radius:\s*18px/s);
		expect(beautifulUIStyles).toMatch(/\.process-step\.bui-tool-chip-group\[data-settled\][^{]*\{[^}]*background:\s*transparent/s);
		expect(beautifulUIStyles).toMatch(/\.bui-code-block\s*\{[^}]*border:\s*0/s);
		expect(beautifulUIStyles).toMatch(/\.bui-code-header\s*\{[^}]*min-height:\s*44px/s);
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-state \.reasoning-summary\s*\{[^}]*grid-template-columns:\s*16px max-content max-content 13px/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-thinking-state \.reasoning-summary\s*\{[^}]*minmax\(12em/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-thinking-meta\s*\{[^}]*min-width:\s*4\.5em/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-thinking-state \.reasoning-label\s*\{[^}]*min-width:\s*12em/s);
	});

	it("treats thinking and tool execution as one in-progress header state", () => {
		expect(threadHeaderStage(true)).toBe("in-progress");
		expect(threadHeaderStage(false)).toBe("completed");
		expect(threadSurface).toContain('{t("inProgress")}');
		expect(styles).toMatch(/\.thread-stage\s*\{[^}]*grid-template-columns:\s*repeat\(2,/s);
	});

	it("keeps the header status chip beside the terminal control instead of stacking them", () => {
		expect(threadSurface).toContain('className="thread-header-end"');
		expect(threadSurface).toContain('className="square-button terminal-toggle"');
		expect(threadSurface).toContain('{t("terminal")}');
		expect(threadSurface).not.toContain("streaming-text");
		expect(styles).toMatch(/\.thread-header-end\s*\{[^}]*display:\s*flex;[^}]*align-items:\s*center;/s);
		expect(styles).toMatch(/\.thread-header-end\s*\{[^}]*grid-column:\s*3;[^}]*justify-self:\s*end;/s);
		expect(styles).not.toMatch(/\.thread-runtime-status\s*\{[^}]*grid-column:\s*3;[^}]*grid-row:\s*1;[^}]*margin-right:\s*45px;/s);
		expect(styles).not.toMatch(/\.thread-actions\s*\{[^}]*grid-column:\s*3;[^}]*grid-row:\s*1;/s);
		expect(styles).not.toMatch(/\.thread-header[^{]*\{[^}]*\.streaming-text/s);
		expect(styles).not.toMatch(/\.terminal-toggle[^{]*\{[^}]*\.streaming-text/s);
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
		// The trailing auto column keeps the duration on the bar itself (UI-016).
		expect(prototypeStyles).toMatch(/\.reasoning-summary\s*\{[^}]*grid-template-columns:\s*15px minmax\(0, 1fr\) auto auto;[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(prototypeStyles).toMatch(/button\.reasoning-summary:hover \.azem-thinking-mark,[\s\S]*?\.timeline-step > summary:hover \.timeline-step-mark,[\s\S]*?\.tool-block summary:hover \.tool-leading,[\s\S]*?\.tool-group summary:hover \.tool-leading\s*\{[^}]*background:\s*transparent;[^}]*box-shadow:\s*none;/s);
		expect(prototypeStyles).toMatch(/\.reasoning-body\s*\{[^}]*margin:\s*-1px 0 5px 7px;[^}]*line-height:\s*1\.68;/s);
		expect(prototypeStyles).toMatch(/\.reasoning-step::before\s*\{[^}]*display:\s*none;/s);
	});

	it("keeps Beautiful UI blue tokens global and does not wrap commentary on the marker grid", () => {
		expect(beautifulUIStyles).toMatch(/:root\s*\{[^}]*--accent:\s*#0285ff;/s);
		// One muted token for every thinking state, light and dark. The regression
		// is a per-state colour, not the shade itself (UI-016).
		expect(beautifulUIStyles.match(/--thinking-ink:\s*var\(--muted\);/gu)).toHaveLength(2);
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-mark\s*\{[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-mark\.active\s*\{[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-state \.reasoning-summary:disabled\s*\{[^}]*opacity:\s*1;[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-state \.reasoning-label,\s*\.bui-thinking-state \.reasoning-label-base\s*\{[^}]*color:\s*var\(--thinking-ink\);[^}]*opacity:\s*1;/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-thinking-pill/);
		expect(styles).toMatch(/button\.reasoning-summary:disabled\s*\{[^}]*opacity:\s*1;[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(prototypeStyles).toMatch(/\.azem-thinking-mark\.active i:first-child\s*\{[^}]*animation:\s*none;/s);
		expect(beautifulUIStyles).toMatch(/\.bui-streaming-text > :last-child::after\s*\{[^}]*content:\s*none;[^}]*display:\s*none;/s);
		expect(beautifulUIStyles).toMatch(/\.bui-streaming-text.active > :last-child::after\s*\{[^}]*display:\s*inline-block;/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-streaming-text > :last-child::after\s*\{[^}]*position:\s*absolute;/s);
		// Settled block children must not replay enter motion while the tail streams (UI-003).
		expect(styles).not.toMatch(/\.streaming-text\.active\s*>\s*:where\([^)]*\)\s*\{[^}]*streaming-block-in/s);
		expect(beautifulUIStyles).not.toMatch(/\.streaming-text\.active\s*>\s*\.bui-code-block\s*\{[^}]*streaming-block-in/s);
		expect(styles).toMatch(/\.streaming-text-reveal\s*\{[^}]*streaming-text-reveal-in/s);
		expect(styles).not.toMatch(/\.assistant-block\.phase-pending::before/);
		expect(styles).toMatch(/\.timeline-feed > \.process-fold,\s*\.timeline-feed > \.session-turn-current,\s*\.timeline-feed > \.session-history-turn:last-of-type[\s\S]*?contain-intrinsic-size:\s*none/s);
		expect(styles).toMatch(/\.process-status-rule\s*\{[^}]*flex-direction:\s*column;/s);
		expect(styles).toMatch(/\.process-status-rule-copy\s*\{[^}]*display:\s*flex;/s);
		expect(styles).toMatch(/\.process-status-rule::after\s*\{[^}]*width:\s*100%;[^}]*height:\s*1px;[^}]*background:\s*var\(--line\);/s);
		// The running sparkle breathes on scale and brightness, never on a
		// muted-to-ink color swap (UI-016).
		expect(beautifulUIStyles).toMatch(/\.bui-thinking-mark\.active\s*\{[^}]*color:\s*var\(--thinking-ink\);[^}]*animation:\s*bui-thinking-pulse/s);
		expect(beautifulUIStyles).toMatch(/@keyframes bui-thinking-pulse\s*\{[\s\S]*?transform:\s*scale\(1\.16\);/);
		expect(beautifulUIStyles).toMatch(/\.commentary-block\s*\{[^}]*display:\s*block;[^}]*grid-template-columns:\s*none;/s);
		expect(beautifulUIStyles).toMatch(/\.subagent-run-card\s*\{[^}]*width:\s*100%;/s);
		expect(beautifulUIStyles).toMatch(/\.bui-task-row\s*\{[^}]*border-radius:\s*var\(--bui-radius-task\);[^}]*background:\s*var\(--paper\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-task-mark\[data-state="completed"\]\s*\{[^}]*background:\s*var\(--green\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-tool-chip-detail\s*\{[^}]*border-radius:\s*999px;[^}]*font-family:\s*var\(--mono\);/s);
		expect(beautifulUIStyles).toMatch(/\.bui-file-change-pill\s*\{[^}]*border-radius:\s*999px;[^}]*background:\s*var\(--paper\);/s);
	});

	it("draws the step rail as per-row segments so an expanded body cannot break the thread", () => {
		expect(beautifulUIStyles).toMatch(/\.timeline-step-row::before\s*\{[^}]*top:\s*0;[^}]*bottom:\s*0;[^}]*width:\s*1px;/s);
		expect(beautifulUIStyles).toMatch(/\[data-step-edge="first"\]::before\s*\{\s*top:\s*var\(--step-rail-lead\);/);
		expect(beautifulUIStyles).toMatch(/\[data-step-edge="last"\]::before\s*\{\s*bottom:\s*calc\(100% - var\(--step-rail-lead\)\);/);
		expect(beautifulUIStyles).toMatch(/\[data-step-edge="only"\]::before\s*\{\s*content:\s*none;/);
		// The virtualized window scrolls past spacers; the rail must survive them.
		expect(beautifulUIStyles).toMatch(/\.deferred-process-spacer::before\s*\{[^}]*width:\s*1px;/s);
		// UI-010: the thread runs in its own gutter, so no node needs a paper mask
		// that could survive as a detached disc on a lit row.
		expect(beautifulUIStyles).toMatch(/\.timeline-step-row\s*\{[^}]*padding-left:\s*var\(--step-rail-gutter\);/s);
		expect(beautifulUIStyles).not.toMatch(/\.bui-step-mark-glyph\s*\{[^}]*box-shadow:/s);
		expect(beautifulUIStyles).toMatch(
			/@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.bui-step-spinner\s*\{\s*animation:\s*none;[^}]*\}\s*\.timeline-step-row\[data-step-enter="true"\]\s*\{\s*animation:\s*none;/s,
		);
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
    expect(items.some((item) => item.action === "skills" || item.value === "/skills" || item.label === "技能")).toBe(false);
    expect(items.some((item) => item.action === "inspector" || item.value === "/inspector" || item.label === "环境信息")).toBe(false);
  });

  it("does not surface the skills catalog or inspector commands when those queries are typed", () => {
    expect(slashSuggestions("/skills", skills, "zh-CN").some((item) => item.action === "skills" || item.label === "技能")).toBe(false);
    expect(slashSuggestions("/inspector", skills, "zh-CN").some((item) => item.action === "inspector" || item.label === "环境信息")).toBe(false);
    expect(slashSuggestions("/环境", skills, "zh-CN").some((item) => item.action === "inspector" || item.label === "环境信息")).toBe(false);
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

  it("sends Select Action through the existing composer submitTurn path", () => {
    expect(threadSurface).toContain("<SelectActionHost");
    expect(threadSurface).toContain("onSubmit={sendSelectAction}");
    expect(threadSurface).toContain('source: "composer" | "select-action"');
    expect(threadSurface).toContain('void submitTurn(text, [], undefined, "select-action")');
    expect(threadSurface).toContain('source === "composer" && editingQueuedId');
    expect(threadSurface).not.toContain("kind: \"select_action\"");
    expect(beautifulUIStyles).toMatch(/\.bui-action-island\s*\{[^}]*border-radius:\s*999px;/s);
    expect(beautifulUIStyles).toMatch(/\.assistant-block ::selection,\s*\.commentary-block ::selection,\s*\.user-block ::selection/);
  });
});
