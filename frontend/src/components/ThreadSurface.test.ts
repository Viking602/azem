import { describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync, readdirSync } from "node:fs";
import type { SkillEntry, Snapshot } from "../types";
import {
  COMPOSER_OVERLAY_CLEARANCE,
  COMPOSER_OVERLAY_MIN_GAP,
  composerOverlayGap,
  pinTranscriptTail,
  sessionStageMotion,
  transcriptFollowBehavior,
} from "./ThreadSurface";
import { branchMenuLayout, composerPromptPlaceholder } from "./thread/Composer";
import { composerModelChoices, effectiveComposerRoute, supportsFastMode } from "./thread/composerModels";
import { filterModelControlOptions, modelControlWidth, nextModelControlView } from "./thread/ModelControls";
import { namedClipboardImage, pastedImages, shouldReadNativeClipboard } from "./thread/clipboard";
import { parseSkillPrompt, skillTitle, slashSuggestions } from "./thread/slash";
import { translator } from "../i18n";
import { visibleCommentaryTitle } from "./Timeline";
import { readStylesheetTree } from "../testStyles";

const styles = readStylesheetTree("src/styles.css");
const prototypeStyles = readFileSync("src/prototype.css", "utf8");
const assistantUIStyles = readFileSync("src/components/assistant-ui/elements.css", "utf8");
const assistantMessageElements = readFileSync("src/components/elements/message-pair.tsx", "utf8");
const assistantComposerElements = readFileSync("src/components/elements/composer.tsx", "utf8");
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

	it("collapses every Cursor mode into one family row while retaining the active wire model", () => {
		const choices = composerModelChoices({
			cursor: [
				{ id: "gpt-5.6-sol-low", name: "GPT-5.6 Sol 1M Low", reasoningLevels: ["low"] },
				{ id: "gpt-5.6-sol-xhigh", name: "GPT-5.6 Sol 1M Extra High", reasoningLevels: ["xhigh"] },
				{ id: "gpt-5.6-sol-low-thinking", name: "GPT-5.6 Sol 1M Low Thinking", reasoningLevels: ["low"] },
				{ id: "gpt-5.6-sol-xhigh-thinking", name: "GPT-5.6 Sol 1M Extra High Thinking", reasoningLevels: ["xhigh"] },
				{ id: "gpt-5.6-sol-low-fast", name: "GPT-5.6 Sol Low Fast", reasoningLevels: ["low"] },
			],
		}, { provider: "cursor", model: "gpt-5.6-sol-xhigh-thinking", reasoning: "low" });

		expect(choices).toHaveLength(1);
		expect(choices[0]).toMatchObject({
			id: "gpt-5.6-sol-xhigh-thinking",
			name: "GPT-5.6 Sol 1M",
			reasoningLevels: ["low", "xhigh"],
			defaultReasoning: "xhigh",
		});
		expect(choices[0]?.cursorGroup?.variants).toHaveLength(5);
	});

	it("defaults a newly selected Cursor family to its Thinking variant", () => {
		const choices = composerModelChoices({
			cursor: [
				{ id: "claude-4-sonnet", name: "Claude Sonnet 4", reasoningLevels: ["default"] },
				{ id: "claude-4-sonnet-thinking", name: "Claude Sonnet 4 Thinking", reasoningLevels: ["default"] },
			],
		}, { provider: "chatgpt", model: "gpt-5.6", reasoning: "default" });

		expect(choices).toHaveLength(1);
		expect(choices[0]).toMatchObject({ id: "claude-4-sonnet-thinking", name: "Claude Sonnet 4" });
	});

	it("keeps a folded Cursor family's exact inventory and retention metadata visible", () => {
		const choices = composerModelChoices({
			cursor: [
				{ id: "claude-fable-5-low", name: "Claude Fable 5 1M Low (NO ZDR)", reasoningLevels: ["low"] },
				{ id: "claude-fable-5-high", name: "Claude Fable 5 1M (NO ZDR)", reasoningLevels: ["high"] },
				{ id: "claude-fable-5-thinking-high", name: "Claude Fable 5 1M Thinking (NO ZDR)", reasoningLevels: ["high"] },
			],
		}, { provider: "cursor", model: "claude-fable-5-high", reasoning: "high" });

		expect(choices).toHaveLength(1);
		expect(choices[0]).toMatchObject({
			name: "Claude Fable 5 1M",
			cursorVariantCount: 3,
			cursorTierCount: 2,
			cursorHasFast: false,
			cursorNoZDR: true,
		});
		expect(choices[0]?.aliases).toEqual(expect.arrayContaining([
			"claude-fable-5-low", "claude-fable-5-high", "claude-fable-5-thinking-high",
		]));
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

	it("keeps content text selectable while chrome stays non-selectable", () => {
		expect(styles).not.toMatch(/\.desktop-shell\s*\{[^}]*user-select:\s*none;/s);
		expect(styles).toMatch(/\.titlebar-region[^{]*\{[^}]*user-select:\s*none;/s);
		expect(styles).toMatch(/\.resize-handle\s*\{[^}]*user-select:\s*none;/s);
		expect(styles).toMatch(/\.desktop-shell :where\(img, svg\)\s*\{[^}]*-webkit-user-drag:\s*none;/s);
	});

	it("does not restart smooth scrolling for every streaming frame", () => {
		expect(transcriptFollowBehavior(true)).toBe("instant");
		expect(transcriptFollowBehavior(false)).toBe("smooth");
		expect(transcriptFollowBehavior(false, true)).toBe("instant");
		expect(threadSurface).toContain("pinTranscriptTail(node, \"instant\")");
	});


	it("opens a switched session at the tail instead of the first line", () => {
		expect(threadSurface).toContain("sessionFollow.current !== currentSessionId");
		expect(threadSurface).toContain("pinInstant.current = true");
		expect(threadSurface).toContain("querySelector(\".transcript\")");
		expect(threadSurface).toContain("typeof ResizeObserver === \"undefined\"");
		expect(threadSurface).toContain("observer.observe(transcript)");
		expect(threadSurface).toContain("[blocks, following, queuedPrompts.length, running, currentSessionId]");
		expect(threadSurface).toContain("viewport.style.scrollBehavior = \"auto\"");
		expect(threadSurface).toContain("viewport.scrollTop = top");
		expect(threadSurface).toContain("if (pinning.current || pinInstant.current) return");
		expect(styles).not.toMatch(/\.transcript-viewport\s*\{[^}]*scroll-behavior:\s*smooth/);
		const viewport = { scrollHeight: 2400, scrollTop: 0, scrollTo: vi.fn(), style: { scrollBehavior: "smooth" } };
		pinTranscriptTail(viewport as unknown as HTMLElement, "instant");
		expect(viewport.scrollTop).toBe(2400);
		expect(viewport.style.scrollBehavior).toBe("auto");
		expect(viewport.scrollTo).not.toHaveBeenCalled();
		pinTranscriptTail(viewport as unknown as HTMLElement, "smooth");
		expect(viewport.scrollTo).toHaveBeenCalledWith({ top: 2400, behavior: "smooth" });
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
		expect(styles).toMatch(/\.transcript\s*\{[^}]*padding:\s*34px 0 0/s);
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
		expect(threadSurface).toContain('className="empty-launch-stage"');
		expect(styles).toMatch(/\.empty-composer-wrap\s*\{[^}]*display:\s*flex;[^}]*padding:\s*clamp\(40px,\s*7vh,\s*76px\)/s);
		expect(styles).toMatch(/\.empty-launch-stage\s*\{[^}]*position:\s*relative;[^}]*width:\s*min\(840px,\s*100%\);[^}]*min-width:\s*0;/s);
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
		expect(styles).toMatch(/\.thread-surface \.aui-code-block,\s*\n\.agent-side-chat \.aui-code-block\s*\{[^}]*--aui-code-size:\s*var\(--chat-code-font-size/s);
		expect(threadSurface).toContain("chatTypographyVars(chatFontSize, chatCodeFontSize)");
		expect(agentSideChat).toContain("chatTypographyVars(chatFontSize, chatCodeFontSize)");
		expect(assistantUIStyles).toMatch(/\.aui-code-block\s*\{[^}]*--aui-code-size:\s*var\(--chat-code-font-size/s);
		expect(assistantUIStyles).toMatch(/\.aui-code-block\s*\{[^}]*--aui-code-radius:\s*18px/s);
		expect(assistantUIStyles).toMatch(/\.process-step\.aui-tool-timeline\[data-settled\][^{]*\{[^}]*background:\s*transparent/s);
		expect(assistantUIStyles).toMatch(/\.aui-code-block\s*\{[^}]*border:\s*0/s);
		expect(assistantUIStyles).toMatch(/\.aui-code-header\s*\{[^}]*min-height:\s*44px/s);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-panel \.reasoning-summary\s*\{[^}]*grid-template-columns:\s*16px max-content max-content 13px/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-reasoning-panel \.reasoning-summary\s*\{[^}]*minmax\(12em/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-reasoning-meta\s*\{[^}]*min-width:\s*4\.5em/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-reasoning-panel \.reasoning-label\s*\{[^}]*min-width:\s*12em/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-reasoning-panel\.completed\.quiet \.reasoning-summary::after\s*\{[^}]*height:\s*1px/s);
		expect(styles).not.toMatch(/\.process-fold\[data-folded\] \.process-fold-summary::after\s*\{[^}]*height:\s*1px/s);
	});

	it("does not show an in-progress / completed stage switch in the header", () => {
		expect(threadSurface).not.toContain('className="thread-stage"');
		expect(threadSurface).not.toContain('{t("inProgress")}');
		expect(threadSurface).not.toContain("threadHeaderStage");
	});

	it("keeps the header status beside icon controls and hosts the fixed Environment panel below", () => {
		expect(threadSurface).toContain('className="thread-header-end"');
		expect(threadSurface).toContain('className="square-button thread-environment-toggle"');
		expect(threadSurface).toContain('className="square-button terminal-toggle"');
		expect(threadSurface).toContain("<ThreadEnvironmentPanel open={environmentOpen}");
		expect(threadSurface).toContain("<SquareTerminal");
		expect(threadSurface).not.toContain("streaming-text");
		expect(styles).toMatch(/\.thread-header-end\s*\{[^}]*display:\s*flex;[^}]*align-items:\s*center;/s);
		expect(styles).toMatch(/\.thread-header-end\s*\{[^}]*grid-column:\s*3;[^}]*justify-self:\s*end;/s);
		expect(styles).toMatch(/\.thread-environment-overlay\s*\{[^}]*padding:\s*12px;[^}]*pointer-events:\s*none;/s);
		expect(styles).toMatch(/\.thread-environment-card\s*\{[^}]*width:\s*min\(288px,[^}]*border-radius:\s*18px;/s);
		expect(styles).toMatch(/\.thread-surface\[data-environment-open="true"\] \.transcript-viewport,[\s\S]*?padding-right:\s*312px;/s);
		expect(styles).not.toMatch(/\.thread-runtime-status\s*\{[^}]*grid-column:\s*3;[^}]*grid-row:\s*1;[^}]*margin-right:\s*45px;/s);
		expect(styles).not.toMatch(/\.thread-actions\s*\{[^}]*grid-column:\s*3;[^}]*grid-row:\s*1;/s);
		expect(styles).not.toMatch(/\.thread-header[^{]*\{[^}]*\.streaming-text/s);
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

	it("keeps assistant-ui Elements blue tokens global and does not wrap commentary on the marker grid", () => {
		expect(assistantUIStyles).toMatch(/:root\s*\{[^}]*--accent:\s*#0285ff;/s);
		// One muted token for every thinking state, light and dark. The regression
		// is a per-state colour, not the shade itself (UI-016).
		expect(assistantUIStyles.match(/--thinking-ink:\s*var\(--muted\);/gu)).toHaveLength(2);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-mark\s*\{[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-mark\.active\s*\{[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-panel \.reasoning-summary:disabled\s*\{[^}]*opacity:\s*1;[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-panel \.reasoning-label,\s*\.aui-reasoning-panel \.reasoning-label-base\s*\{[^}]*color:\s*var\(--thinking-ink\);[^}]*opacity:\s*1;/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-reasoning-pill/);
		expect(styles).toMatch(/button\.reasoning-summary:disabled\s*\{[^}]*opacity:\s*1;[^}]*color:\s*var\(--thinking-ink\);/s);
		expect(prototypeStyles).toMatch(/\.azem-thinking-mark\.active i:first-child\s*\{[^}]*animation:\s*none;/s);
		expect(assistantUIStyles).toMatch(/\.aui-streaming-text > :last-child::after\s*\{[^}]*content:\s*none;[^}]*display:\s*none;/s);
		expect(assistantUIStyles).toMatch(/\.aui-streaming-text.active > :last-child::after\s*\{[^}]*display:\s*inline-block;/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-streaming-text > :last-child::after\s*\{[^}]*position:\s*absolute;/s);
		// Idle cadenced sweep must not paint. WKWebView left a hairline under
		// 正在思考 the same way an absolute caret did between paragraphs (UI-016).
		expect(assistantUIStyles).toMatch(/\.aui-cadenced-shimmer-sweep\s*\{[^}]*display:\s*none;/s);
		expect(assistantUIStyles).toMatch(/\.aui-cadenced-shimmer-active \.aui-cadenced-shimmer-sweep\s*\{[^}]*display:\s*block;/s);
		expect(assistantUIStyles).toMatch(/\.aui-cadenced-shimmer\.rolling \.aui-cadenced-shimmer-sweep\s*\{[^}]*display:\s*none;/s);
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-wait,\s*\.aui-cadenced-shimmer\s*\{[^}]*line-height:\s*1\.2;/s);
		expect(styles).toMatch(/\.reasoning-label-roll\s*\{[^}]*overflow:\s*hidden;[^}]*line-height:\s*1\.2;/s);
		// Settled block children must not replay enter motion while the tail streams (UI-003).
		expect(styles).not.toMatch(/\.streaming-text\.active\s*>\s*:where\([^)]*\)\s*\{[^}]*streaming-block-in/s);
		expect(assistantUIStyles).not.toMatch(/\.streaming-text\.active\s*>\s*\.aui-code-block\s*\{[^}]*streaming-block-in/s);
		expect(styles).toMatch(/\.streaming-text-reveal\s*\{[^}]*streaming-text-reveal-in 260ms cubic-bezier\(\.16,\s*1,\s*\.3,\s*1\) both/s);
		expect(styles).toMatch(/@keyframes streaming-text-reveal-in\s*\{\s*from\s*\{[^}]*opacity:\s*\.28;[^}]*blur\(5px\)/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-streaming-text \.streaming-text-reveal \{[^}]*animation-duration:\s*\.1s/);
		expect(styles).not.toMatch(/\.assistant-block\.phase-pending::before/);
		expect(styles).toMatch(/\.timeline-feed > \.process-fold,\s*\.timeline-feed > \.session-turn-current,\s*\.timeline-feed > \.session-history-turn:last-of-type[\s\S]*?contain-intrinsic-size:\s*none/s);
		expect(styles).toMatch(/\.timeline-feed > \.session-history-turn\s*\{[^}]*contain-intrinsic-size:\s*180px/);
		expect(styles).not.toMatch(/\.timeline-feed > \.session-history-turn\s*\{[^}]*contain-intrinsic-size:\s*auto/);

		expect(styles).not.toMatch(/\.process-status-rule/);
		// The running sparkle breathes on scale and brightness, never on a
		// muted-to-ink color swap (UI-016).
		expect(assistantUIStyles).toMatch(/\.aui-reasoning-mark\.active\s*\{[^}]*color:\s*var\(--thinking-ink\);[^}]*animation:\s*aui-reasoning-pulse/s);
		expect(assistantUIStyles).toMatch(/@keyframes aui-reasoning-pulse\s*\{[\s\S]*?transform:\s*scale\(1\.16\);/);
		expect(assistantUIStyles).toMatch(/\.commentary-block\s*\{[^}]*display:\s*block;[^}]*grid-template-columns:\s*none;/s);
		expect(assistantUIStyles).toMatch(/\.subagent-run-card\s*\{[^}]*width:\s*100%;/s);
		expect(assistantUIStyles).toMatch(/\.aui-agent-plan\s*\{[^}]*border-radius:\s*var\(--aui-radius-task\);[^}]*background:\s*var\(--paper\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-agent-plan-mark\[data-state="completed"\]\s*\{[^}]*background:\s*var\(--green\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-tool-timeline-detail\s*\{[^}]*background:\s*transparent;[^}]*color:\s*color-mix\(in srgb, var\(--ink\) 40%, transparent\);/s);
		expect(assistantUIStyles).toMatch(/\.aui-tool-timeline-file\s*\{[^}]*border-radius:\s*999px;[^}]*background:\s*var\(--paper\);/s);
	});

	it("draws the step rail as per-row segments so an expanded body cannot break the thread", () => {
		expect(assistantUIStyles).toMatch(/\.timeline-step-row::before\s*\{[^}]*top:\s*0;[^}]*bottom:\s*0;[^}]*width:\s*1px;/s);
		expect(assistantUIStyles).toMatch(/\[data-step-edge="first"\]::before\s*\{\s*top:\s*var\(--step-rail-lead\);/);
		expect(assistantUIStyles).toMatch(/\[data-step-edge="last"\]::before\s*\{\s*bottom:\s*calc\(100% - var\(--step-rail-lead\)\);/);
		expect(assistantUIStyles).toMatch(/\[data-step-edge="only"\]::before\s*\{\s*content:\s*none;/);
		// The virtualized window scrolls past spacers; the rail must survive them.
		expect(assistantUIStyles).toMatch(/\.deferred-process-spacer::before\s*\{[^}]*width:\s*1px;/s);
		// UI-010: the thread runs in its own gutter, so no node needs a paper mask
		// that could survive as a detached disc on a lit row.
		expect(assistantUIStyles).toMatch(/\.timeline-step-row\s*\{[^}]*padding-left:\s*var\(--step-rail-gutter\);/s);
		expect(assistantUIStyles).not.toMatch(/\.aui-tool-timeline-mark-glyph\s*\{[^}]*box-shadow:/s);
		expect(assistantUIStyles).toMatch(
			/@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.aui-tool-timeline-spinner\s*\{\s*animation:\s*none;[^}]*\}\s*\.timeline-step-row\[data-step-enter="true"\]\s*\{\s*animation:\s*none;/s,
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

	it("keeps one compact whole-chip model trigger without a chevron target", () => {
		expect([modelControlWidth(false, 1200), modelControlWidth(true, 1200)]).toEqual([190, 248]);
		expect(modelControlWidth(false, 1200, 84)).toBe(84);
		expect(modelControlWidth(false, 1200, 236)).toBe(236);
		expect(modelControlWidth(false, 1200, 420)).toBe(300);
		expect(modelControlWidth(false, 400, 236)).toBe(208);
		expect(modelControlWidth(true, 400)).toBe(208);
		expect(styles).toMatch(/\.model-controls\s*\{[^}]*flex:\s*0 0 auto;/s);
		expect(styles).toMatch(/\.model-controls\s*\{[^}]*--model-control-closed-padding:\s*8px;/s);
		expect(threadSurface).toContain('getPropertyValue("--model-control-closed-padding")');
		expect(styles).toMatch(/\.model-controls > \.model-controls-trigger\s*\{[^}]*background:\s*transparent;/s);
		expect(styles).toMatch(/\.model-controls > \.model-controls-trigger:hover\s*\{[^}]*background:\s*var\(--paper-muted\);/s);
		expect(composerModelPicker).toContain('className="model-controls-trigger"');
		expect(composerModelPicker).not.toContain("model-controls-chevron");
		expect(composerModelPicker).not.toContain("ChevronDown");
		expect(composerModelPicker).toContain('data-open={String(open)}');
		expect(composerModelPicker).not.toContain("<details");
		expect(composerModelPicker).not.toContain("<summary");
		expect(composerModelPicker).toContain("hidden={!open}");
		expect(composerModelPicker).toContain("const popover = position ? createPortal");
		expect(composerModelPicker).not.toContain("open && position ? createPortal");
		expect(prototypeStyles).toMatch(/\.composer-model-popover\[hidden\]\s*\{[^}]*display:\s*none !important;/s);
		expect(prototypeStyles).not.toMatch(/\.composer-model-popover\s*\{[^}]*(?:backdrop-filter|animation)\s*:/s);
		expect(prototypeStyles).not.toContain("@keyframes composer-model-in");
		expect(styles).toMatch(/\.model-control-back\s*\{[^}]*width:\s*fit-content;[^}]*min-height:\s*32px;/s);
		expect(styles).not.toMatch(/\.model-control-back:hover[^}]*background:/s);
	});

	it("only exposes fast mode for capable ChatGPT subscription models", () => {
		expect(supportsFastMode("chatgpt", ["tools", "fast"])).toBe(true);
		expect(supportsFastMode("chatgpt", ["tools"])).toBe(false);
		expect(supportsFastMode("deepseek", ["fast"])).toBe(false);
		expect(supportsFastMode("openai", ["fast"])).toBe(false);
	});

	it("keeps the new-conversation composer as one compact reference-led action", () => {
		expect(threadSurface).not.toContain("empty-task-suggestions");
		expect(threadSurface).not.toContain("emptySuggestions(");
		expect(styles).not.toContain(".empty-task-suggestions");
		expect(prototypeStyles).not.toContain(".empty-task-suggestions");
		expect(assistantUIStyles).not.toContain(".empty-task-suggestions");
		expect(prototypeStyles).toMatch(/\.empty-thread \.empty-launch-stage::before,\s*\.empty-thread \.empty-launch-stage::after\s*\{[^}]*content:\s*none;/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.empty-composer-wrap \.empty-composer-heading h1\s*\{[^}]*font-size:\s*clamp\(30px,\s*3vw,\s*36px\);/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-context-bar\s*\{[^}]*min-height:\s*30px;[^}]*gap:\s*6px;[^}]*border:\s*0;[^}]*background:\s*transparent;/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-branch-menu > summary\s*\{[^}]*border-radius:\s*999px;[^}]*background:\s*color-mix\(in srgb,\s*var\(--ink\)\s*3%,\s*transparent\);/s);
		expect(prototypeStyles).not.toContain("--empty-workbench-rail");
		expect(prototypeStyles).toMatch(/\.empty-thread \.composer-card > textarea\s*\{[^}]*min-height:\s*88px;[^}]*font-size:\s*15px;/s);
		expect(prototypeStyles).toMatch(/\.empty-thread \.send-button\s*\{[^}]*width:\s*32px;[^}]*border-radius:\s*999px;/s);
		expect(assistantUIStyles).toMatch(/\[data-slot="empty-state"\] \[data-slot="composer-bar"\]\.composer-card\s*\{[^}]*gap:\s*8px;[^}]*padding:\s*10px;[^}]*border-radius:\s*24px;/s);
	});

	it("keeps Thinking implicit and exposes Fast only inside the picker and summary", () => {
		expect(threadSurface).not.toContain('className="azem-mark empty-launch-mark"');
		expect(threadSurface).toContain('className="empty-composer-heading"');
		expect(threadSurface).toContain('<h1>{t("promptTitle")}</h1>');
		expect(threadSurface).toContain('{showContextBar ? <ComposerContextBar /> : null}');
		expect(threadSurface).toContain('className={`effort-panel-speed');
		expect(threadSurface).toContain('fast ? "Fast" : ""');
		expect(threadSurface).not.toContain('className={`composer-fast-mode');
		expect(threadSurface).not.toContain('thinkingAvailable={thinkingAvailable}');
		expect(threadSurface).not.toContain('onThinkingChange={changeThinking}');
		expect(composerModelPicker).toContain("const fastAvailable = props.fastAvailable;");
		expect(composerModelPicker).toContain('data-mode="fast"');
		expect(composerModelPicker).not.toContain('data-mode="thinking"');
		expect(threadSurface).not.toContain('className="model-controls-fast-icon"');
		expect(composerModelPicker).not.toContain('className="model-controls-fast-icon"');
		expect(prototypeStyles).not.toMatch(/\.composer-fast-mode/);
		expect(prototypeStyles).toMatch(/\.composer-popover-mode > button\s*\{[^}]*color:\s*var\(--faint\);/s);
		expect(prototypeStyles).toMatch(/\.composer-popover-mode\[data-mode="fast"\] > button\.on svg\s*\{[^}]*fill:\s*currentColor;/s);
		expect(prototypeStyles).toMatch(/\.composer-model-popover \.effort-slider\[data-fixed="true"\] \.effort-slider-labels\s*\{[^}]*justify-content:\s*flex-end;/s);
		expect(styles).toMatch(/\.effort-slider\[data-fixed="true"\]\s*\{[^}]*pointer-events:\s*none;/s);
		expect(styles).toMatch(/\.effort-panel-speed\.active\s*\{[^}]*color:\s*var\(--blue\);/s);
	});

	it("keeps route controls contained and applies one enlarged settings type scale", () => {
		expect(prototypeStyles).toMatch(/\.route-card\s*\{[^}]*min-width:\s*0;[^}]*overflow:\s*hidden;/s);
		expect(prototypeStyles).toMatch(/\.route-card \.route-row\s*\{[^}]*grid-template-columns:\s*22px minmax\(0, 1fr\) minmax\(270px, 44%\);/s);
		expect(prototypeStyles).toMatch(/\.route-card \.route-controls\s*\{[^}]*max-width:\s*100%;[^}]*overflow:\s*hidden;/s);
		expect(styles).toMatch(/\.settings-dialog\s*\{[^}]*--text-2xs:\s*calc\(var\(--ui-font-size\) - 1px\);[^}]*--text-sm:\s*calc\(var\(--ui-font-size\) \+ 1px\);/s);
		expect(prototypeStyles).toMatch(/\.route-copy strong\s*\{[^}]*font-size:\s*var\(--text-sm\);/s);
		expect(prototypeStyles).toMatch(/\.route-copy small\s*\{[^}]*font-size:\s*var\(--text-xs\);/s);
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
    expect(items.some((item) => item.value === "/inspector" || item.label === "环境信息")).toBe(false);
  });

  it("does not surface removed catalog or context-panel commands", () => {
    expect(slashSuggestions("/skills", skills, "zh-CN").some((item) => item.action === "skills" || item.label === "技能")).toBe(false);
    expect(slashSuggestions("/inspector", skills, "zh-CN").filter((item) => item.kind === "command")).toEqual([]);
    expect(slashSuggestions("/环境", skills, "zh-CN").filter((item) => item.kind === "command")).toEqual([]);
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

  it("keeps the plain composer submit path without the Select Action island", () => {
    expect(threadSurface).not.toContain("<SelectActionHost");
    expect(threadSurface).not.toContain("sendSelectAction");
    expect(threadSurface).toContain('source === "composer" && editingQueuedId');
    expect(assistantUIStyles).toMatch(/\.assistant-block ::selection,\s*\.commentary-block ::selection,\s*\.user-block ::selection/);
  });

  it("adapts assistant-ui Elements as semantic presentation slots without replacing runtime state", () => {
    expect(threadSurface).toContain('data-slot="thread"');
    expect(threadSurface).toContain('data-slot="empty-state"');
    expect(threadSurface).toContain('data-slot="thread-messages"');
    expect(assistantMessageElements).toContain('data-slot="assistant-message"');
    expect(assistantMessageElements).toContain('data-slot="user-message"');
    expect(timeline).toContain("<AssistantMessage");
    expect(timeline).toContain("<UserMessage");
    expect(timeline).toContain('data-slot="agent-plan"');
    expect(timeline).toContain('data-slot="tool-timeline"');
    expect(assistantUIStyles).toMatch(/--aui-element-surface:/);
    expect(assistantUIStyles).toMatch(/\[data-slot="composer-bar"\]\.composer-card\s*\{[^}]*backdrop-filter:\s*none;/s);
    expect(assistantUIStyles).toMatch(/\.aui-tool-timeline,\s*\.aui-tool-timeline\[data-settled\]\s*\{[^}]*background:\s*transparent;/s);
    expect(assistantUIStyles).toMatch(/\[data-slot="agent-plan"\]\.plan-review\s*\{[^}]*--aui-element-shadow/s);
    expect(assistantComposerElements).toContain('data-slot="composer"');
    expect(assistantComposerElements).toContain('data-slot="composer-bar"');
    expect(assistantComposerElements).toContain('data-slot="composer-context"');
    expect(assistantComposerElements).toContain('data-slot="composer-send"');
    expect(threadSurface).toContain('from "../elements/composer"');
    expect(`${threadSurface}\n${timeline}\n${assistantMessageElements}`).not.toContain("beautiful-ui");
  });

  it("keeps an expanded completed process in the transcript scroll instead of nesting a second scroller", () => {
    expect(styles).toMatch(/\.transcript-viewport\s*\{[^}]*overflow:\s*auto;/s);
    const expandedBody = styles.match(/\.process-fold-clip\[data-open="true"\]\s*>\s*\.process-fold-body\s*\{[^}]*\}/s)?.[0] ?? "";
    expect(expandedBody).toContain("overflow: visible");
    expect(expandedBody).not.toMatch(/max-height|overflow:\s*auto|overscroll-behavior/);
    expect(timeline).toContain("<FoldClip open={open}>{entries}</FoldClip>");
  });

  it("lets assistant and progress prose use the complete transcript column", () => {
    const proseWidth = assistantUIStyles.match(/\[data-slot="assistant-message"\],\s*\[data-slot="assistant-progress"\]\s*\{[^}]*\}/s)?.[0] ?? "";
    expect(proseWidth).toContain("width: 100%");
    expect(proseWidth).toContain("max-width: none");
    expect(proseWidth).not.toContain("68ch");
  });
});
