import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, listHookCatalog, listSkillCatalog, listSystemFonts, listUsageReport } from "../bridge";
import { useRuntimeStore } from "../store";
import type { Snapshot } from "../types";
import SettingsDialog from "./SettingsDialog";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
  configurable: true,
  value(this: HTMLDialogElement) { this.setAttribute("open", ""); },
});
Object.defineProperty(HTMLDialogElement.prototype, "close", {
  configurable: true,
  value(this: HTMLDialogElement) { this.removeAttribute("open"); },
});

vi.mock("../bridge", () => ({
	execute: vi.fn(() => Promise.resolve()),
	listSkillCatalog: vi.fn(() => Promise.resolve({
		entries: [],
		diagnostics: [],
	})),
	listHookCatalog: vi.fn(() => Promise.resolve({
		enabled: true, trustHooks: false, sources: [], commands: [], diagnostics: [],
	})),
	listUsageReport: vi.fn(() => Promise.resolve({
		scope: "project", from: "", to: "", empty: true, requests: 0, sessions: 0, runs: 0,
		totalTokens: 0, inputTokens: 0, outputTokens: 0, reasoningTokens: 0, reportedInputTokens: 0,
		cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false,
		peakDayTokens: 0, currentStreak: 0, longestStreak: 0, days: [], kinds: [], models: [], skills: [],
	})),
	listSystemFonts: vi.fn(() => Promise.resolve([
		{ family: "PingFang SC", label: "苹方-简" },
		{ family: "Songti SC", label: "宋体-简" },
	])),
}));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 4, chatgptFastMode: false, sequence: 0,
};

async function enterInput(input: HTMLInputElement, value: string) {
	const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
	await act(async () => {
		setter?.call(input, value);
		input.dispatchEvent(new Event("input", { bubbles: true }));
	});
}

describe("SettingsDialog", () => {
  afterEach(() => {
    vi.clearAllMocks();
    document.documentElement.style.removeProperty("--chat-ui-font-size");
    document.documentElement.style.removeProperty("--chat-code-font-size");
  });

	it("reads the current skill catalog directly when settings opens", async () => {
		vi.mocked(listSkillCatalog).mockResolvedValueOnce({
			entries: [{ name: "shared-review", description: "Shared review", sourcePath: "/home/.agents/skills/shared-review/SKILL.md", bundled: false, eager: false, disabled: false, modelVisible: true, resourceCount: 0 }],
			diagnostics: [],
		});
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => Promise.resolve());

		expect(listSkillCatalog).toHaveBeenCalledTimes(1);
		expect(useRuntimeStore.getState().skills.map((skill) => skill.name)).toContain("shared-review");
		await act(async () => root.unmount());
		container.remove();
	});

	it("reads the current hook catalog directly when settings opens", async () => {
		vi.mocked(listHookCatalog).mockResolvedValueOnce({
			enabled: true, trustHooks: true,
			sources: [{ id: "demo@local", name: "Demo", origin: "plugin", hookCount: 1, trusted: true }],
			commands: [{ id: "hook-1", name: "notify", event: "SessionStart", matcher: "", command: "true", source: "/hooks.json", origin: "plugin", enabled: true }],
			diagnostics: [],
		});
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true,
			hookCatalog: { enabled: true, trustHooks: false, sources: [], commands: [], diagnostics: [] },
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => Promise.resolve());

		expect(listHookCatalog).toHaveBeenCalledTimes(1);
		expect(useRuntimeStore.getState().hookCatalog.trustHooks).toBe(true);
		expect(useRuntimeStore.getState().hookCatalog.commands.map((command) => command.name)).toContain("notify");
		await act(async () => root.unmount());
		container.remove();
	});

  it("loads role routes and saves a subagent model selection", async () => {
    useRuntimeStore.setState({
      snapshot,
      approvalMode: snapshot.approvalMode,
      modelRoutes: [
		{ scope: "main", role: "", label: "Main", route: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high" } },
        { scope: "title", role: "", label: "Title", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
        { scope: "plan", role: "", label: "Plan", route: {} },
		{ scope: "approval", role: "", label: "Approval", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
		{ scope: "vision", role: "", label: "Vision", route: {} },
		{ scope: "recap", role: "", label: "Recap", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
        { scope: "subagent", role: "explore", label: "Explore", route: {} },
      ],
      agentCatalog: [{ name: "explore", description: "只读探索代码库", model: "", reasoning: "", capabilityMode: "read-only", isolation: "none", source: "builtin", enabled: true }],
      modelsByProvider: {
        chatgpt: [
		  { id: "gpt-5.6-sol", name: "5.6 Sol", disabled: true, reasoningLevels: ["medium", "high"], defaultReasoning: "high" },
		  { id: "gpt-5.6-luna", name: "5.6 Luna", aliases: ["codex-fast"], reasoningLevels: ["low", "medium"], defaultReasoning: "medium", inputModalities: ["text", "image"] },
		  { id: "gpt-text-only", name: "Text only", reasoningLevels: ["low"], defaultReasoning: "low", inputModalities: ["text"] },
        ],
      },
	  modelProviders: [{
		id: "openrouter", displayName: "OpenRouter", backend: "openai_compat",
		defaultBaseUrl: "https://openrouter.ai/api/v1", baseUrl: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY",
		enabled: false, credentialConfigured: false, credentialSource: "none",
		models: [{ id: "openai/gpt-test", name: "GPT Test", contextWindow: 128000, reasoningLevels: ["low", "high"], defaultReasoning: "high" }],
	  }],
      skills: [],
      settingsOpen: true,
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<SettingsDialog />));
	expect(container.querySelector(".settings-search kbd")?.textContent).toBe("⌘F");
	const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型目录"))!;
	expect(catalogNav.querySelector("em")).toBeNull();
    const routesNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型路由"))!;
    await act(async () => routesNav.click());
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_routes", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_agent_types", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_models", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_providers", sessionId: "session-1" });
	expect(execute).toHaveBeenCalledWith({ kind: "list_plugins", sessionId: "session-1" });
	expect(listHookCatalog).toHaveBeenCalled();
	expect(execute).toHaveBeenCalledWith({ kind: "list_sessions", sessionId: "session-1" });
    expect(container.querySelectorAll(".route-row")).toHaveLength(6);
    expect(container.querySelectorAll(".route-card")).toHaveLength(2);
    expect(container.querySelector(".model-routes-pane > .route-groups")).not.toBeNull();
    expect(container.querySelectorAll(".route-card > header")[0]?.textContent).toContain("核心工作流");
    expect(container.querySelectorAll(".route-card > header")[1]?.textContent).toContain("子智能体默认模型");
    expect(Array.from(container.querySelectorAll(".route-copy strong")).some((node) => node.textContent === "主模型")).toBe(false);
    const titleRoute = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("会话标题"))!;
    expect(titleRoute.textContent).toContain("根据首轮用户消息自动生成侧栏会话标题");
    expect(titleRoute.textContent).toContain("5.6 Luna");
	const approvalRoute = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("审批模型"))!;
	expect(approvalRoute.textContent).toContain("5.6 Luna");
	const recapRoute = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("回顾模型"))!;
	expect(recapRoute.textContent).toContain("每轮完成后生成右侧栏的简短会话回顾");
	expect(recapRoute.textContent).toContain("5.6 Luna");
	const visionRoute = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("视觉模型"))!;
	expect(visionRoute.textContent).toContain("未配置");
	const visionModelMenu = visionRoute.querySelector<HTMLDetailsElement>(".route-model-menu")!;
	visionModelMenu.open = true;
	await act(async () => visionModelMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
	expect(container.querySelector('.menu-select-options-portal [data-value="chatgpt::gpt-text-only"]')).toBeNull();
	visionModelMenu.open = false;
	await act(async () => visionModelMenu.dispatchEvent(new Event("toggle", { bubbles: true })));

    vi.mocked(execute).mockClear();
    const explore = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("explore"))!;
    const modelMenu = explore.querySelector<HTMLDetailsElement>(".route-model-menu")!;
    modelMenu.open = true;
    await act(async () => modelMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
	expect(container.querySelector('.menu-select-options-portal [data-value="chatgpt::gpt-5.6-sol"]')).toBeNull();
	const modelSearch = container.querySelector<HTMLInputElement>('.menu-select-options-portal input[placeholder="搜索模型名称或别名…"]')!;
	await enterInput(modelSearch, "codex-fast");
	expect(container.querySelector('.menu-select-options-portal [data-value="chatgpt::gpt-5.6-sol"]')).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="chatgpt::gpt-5.6-luna"]')!.click());

    expect(execute).toHaveBeenCalledWith({
      kind: "set_model_route", target: "", sessionId: "session-1",
      route: { scope: "subagent", role: "explore", label: "Explore", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "medium" } },
    });
    vi.mocked(execute).mockClear();
    const reasoningMenu = explore.querySelector<HTMLDetailsElement>(".route-reasoning-menu")!;
    expect(reasoningMenu.querySelector("summary")?.getAttribute("aria-label")).toBe("explore 推理强度");
    reasoningMenu.open = true;
    await act(async () => reasoningMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
    await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="low"]')!.click());
    expect(execute).toHaveBeenCalledWith({
      kind: "set_model_route", target: "", sessionId: "session-1",
      route: { scope: "subagent", role: "explore", label: "Explore", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
    });
	const subagentsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("子智能体"))!;
	await act(async () => subagentsNav.click());
	const subagentPane = container.querySelector(".subagent-settings-pane")!;
	expect(subagentPane.querySelector(".subagent-capacity")?.textContent).toContain("容量与隔离");
	expect(subagentPane.querySelectorAll(".subagent-capacity .setting-row")).toHaveLength(6);
	expect(subagentPane.querySelector(".subagent-scheduling")?.textContent).toContain("调度");
	expect(subagentPane.querySelector(".subagent-scheduling .subagent-policy")?.textContent).toContain("只读");
	expect(subagentPane.querySelector(".subagent-scheduling .subagent-policy")?.textContent).toContain("并行");
	expect(subagentPane.querySelector(".subagent-scheduling button")).toBeNull();
	expect(subagentPane.querySelector(".subagent-scheduling [role='switch']")).toBeNull();
	expect(subagentPane.querySelector(".subagent-display")?.textContent).toContain("主会话展示");
	expect(subagentPane.querySelectorAll(".subagent-display [role='switch'][aria-disabled='true']")).toHaveLength(3);
    await act(async () => root.unmount());
    container.remove();
  });

	it("saves an llmux provider and sends the API key only in the action", async () => {
		const extraProviders = Array.from({ length: 55 }, (_, index) => ({
			id: index === 0 ? "ai302" : `provider-${String(index).padStart(2, "0")}`, displayName: index === 0 ? "302.AI" : `Provider ${index}`, backend: "openai-compatible",
			defaultBaseUrl: `https://provider-${index}.example/v1`, baseUrl: "", envKey: `PROVIDER_${index}_API_KEY`,
			enabled: false, credentialConfigured: false, credentialSource: "none" as const, modelsDevId: index === 0 ? "302ai" : undefined, models: [],
		}));
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {}, agentCatalog: [], skills: [], settingsOpen: true,
			modelProviders: [{
				id: "openrouter", displayName: "OpenRouter", backend: "openai_compat",
				defaultBaseUrl: "https://openrouter.ai/api/v1", baseUrl: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY",
				enabled: false, credentialConfigured: false, credentialSource: "none",
				models: [{ id: "openai/gpt-test", name: "GPT Test", contextWindow: 128000, maxOutputTokens: 32000, reasoningLevels: ["low", "high"], defaultReasoning: "high", capabilities: ["tools", "reasoning"], inputModalities: ["text", "image"], outputModalities: ["text"] }],
			}, ...extraProviders],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型目录"))!;
		await act(async () => catalogNav.click());
		const catalogMain = container.querySelector<HTMLElement>('.settings-main[data-section="catalog"]')!;
		expect(catalogMain.querySelector(".settings-content > .settings-pane > .provider-settings")).not.toBeNull();
		expect(catalogMain.querySelector(".provider-directory > .provider-list")).not.toBeNull();
		expect(catalogMain.querySelector(".provider-workspace > .provider-model-catalog > .provider-models > .provider-model-grid")).not.toBeNull();
		const editor = container.querySelector<HTMLElement>(".provider-workspace")!;
		const directory = container.querySelector<HTMLDivElement>(".provider-list")!;
		expect(directory.querySelectorAll("button")).toHaveLength(26);
		Object.defineProperties(directory, { scrollHeight: { configurable: true, value: 1000 }, clientHeight: { configurable: true, value: 400 } });
		directory.scrollTop = 560;
		await act(async () => directory.dispatchEvent(new Event("scroll", { bubbles: true })));
		expect(directory.querySelectorAll("button")).toHaveLength(50);
		const providerMore = container.querySelector<HTMLElement>(".provider-more")!;
		expect(providerMore.textContent).toContain("剩余 7 个");
		expect(providerMore.parentElement?.classList.contains("provider-directory")).toBe(true);
		expect(directory.contains(providerMore)).toBe(false);
		expect(container.querySelector<HTMLImageElement>('.provider-list img[src="https://models.dev/logos/openrouter.svg"]')).not.toBeNull();
		expect(container.querySelector<HTMLImageElement>('.provider-list img[src="https://models.dev/logos/302ai.svg"]')).not.toBeNull();
		expect(editor.querySelector<HTMLImageElement>('header img[src="https://models.dev/logos/openrouter.svg"]')).not.toBeNull();
		const capabilities = editor.querySelector(".subscription-model-capabilities")!;
		expect(capabilities.querySelectorAll(".model-capability-popover")).toHaveLength(5);
		expect(capabilities.querySelector(".model-capability-text")).toBeNull();
		expect(editor.querySelector(".model-capability-overflow")).toBeNull();
		expect(editor.querySelector(".provider-model-identity small")).toBeNull();
		expect(capabilities.querySelector('[aria-label="工具调用"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="思考"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="输入: text"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="输出: text"]')).not.toBeNull();
		const toolsCapability = capabilities.querySelector<HTMLDetailsElement>('.model-capability-popover:has([aria-label="工具调用"])')!;
		expect(toolsCapability.open).toBe(false);
		await act(async () => toolsCapability.querySelector<HTMLElement>("summary")!.click());
		expect(toolsCapability.open).toBe(true);
		expect(toolsCapability.querySelector('[role="tooltip"]')?.textContent).toBe("工具调用");
		const reasoningCapability = capabilities.querySelector<HTMLDetailsElement>('.model-capability-popover:has([aria-label="思考"])')!;
		await act(async () => reasoningCapability.querySelector<HTMLElement>("summary")!.click());
		expect(toolsCapability.open).toBe(false);
		expect(reasoningCapability.open).toBe(true);
		expect(editor.querySelector(".provider-model-row")).toBeNull();
		expect(editor.querySelector(".provider-model-catalog")).not.toBeNull();
		expect(editor.querySelector(".provider-model-grid")).not.toBeNull();
		expect(editor.querySelector(".provider-state-control")).not.toBeNull();
		expect(editor.querySelector(".model-toggle-label")?.textContent).toBe("已启用");
		expect(editor.querySelector('[aria-label="禁用 GPT Test"]')).not.toBeNull();
		vi.mocked(execute).mockClear();
		await act(async () => editor.querySelector<HTMLButtonElement>('[aria-label="禁用 GPT Test"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_model_enabled", sessionId: "session-1", target: "openrouter", name: "openai/gpt-test", decision: "false" });
		const apiAddress = editor.querySelector<HTMLInputElement>('.provider-fields input:not([type="password"])')!;
		expect(apiAddress.value).toBe("https://openrouter.ai/api/v1");
		expect(apiAddress.readOnly).toBe(true);
		await act(async () => editor.querySelector<HTMLInputElement>('.provider-switch input')!.click());
		await enterInput(editor.querySelector<HTMLInputElement>('input[type="password"]')!, "sk-test");
		vi.mocked(execute).mockClear();
		await act(async () => editor.querySelector<HTMLButtonElement>(".provider-model-actions .small-button")!.click());
		expect(execute).toHaveBeenCalledWith({
			kind: "discover_provider_models", sessionId: "session-1", secret: "sk-test",
			provider: expect.objectContaining({ id: "openrouter", enabled: true }),
		});
		vi.mocked(execute).mockClear();
		await act(async () => editor.querySelector<HTMLButtonElement>("footer .small-button")!.click());
		expect(execute).toHaveBeenCalledWith({
			kind: "set_model_provider", sessionId: "session-1", secret: "sk-test",
			provider: expect.objectContaining({ id: "openrouter", enabled: true, models: [expect.objectContaining({ id: "openai/gpt-test" })] }),
		});
		await act(async () => root.unmount());
		container.remove();
	});

	it("shows subscription providers in model settings and starts the existing login flow", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [
				{ scope: "approval", role: "", label: "Approval", route: { provider: "chatgpt", model: "codex-auto-review", reasoning: "high" } },
				{ scope: "subagent", role: "review", label: "Review", route: { provider: "chatgpt", model: "gpt-review-worker", reasoning: "high" } },
			], modelsByProvider: {
				chatgpt: [
					{ id: "gpt-5.6-sol", name: "GPT-5.6 Sol", reasoningLevels: ["medium", "high"] },
					{ id: "codex-auto-review", name: "Codex Auto Review", disabled: true, reasoningLevels: ["medium", "high"] },
					{ id: "gpt-5.3-codex-spark", name: "GPT-5.3 Codex Spark", disabled: true, reasoningLevels: ["medium", "high"] },
					{ id: "gpt-review-worker", name: "GPT Review Worker", reasoningLevels: ["medium", "high"] },
				],
				grok: [{ id: "grok-4.20", name: "Grok 4.20", reasoningLevels: ["low", "medium", "high"] }],
			}, agentCatalog: [], skills: [], settingsOpen: true,
			modelProviders: [{
				id: "chatgpt", displayName: "OpenAI / ChatGPT 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true, credentialConfigured: true, credentialSource: "stored", accountId: "account-1", accountLabel: "user@example.com", accountPlan: "pro", modelsDevId: "openai", models: [],
				quotaAvailable: true, quotaUsedPercent: 61.5, quotaResetsAt: 1786500000, quotaBalance: "12.50",
			}, {
				id: "grok", displayName: "Grok 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true, credentialConfigured: true, credentialSource: "stored", accountId: "account-2", accountLabel: "grok@example.com", modelsDevId: "xai", models: [], quotaWarning: "grok quota returned HTTP 500",
			}],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型目录"))!;
		await act(async () => catalogNav.click());
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("每周额度");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("订阅驱动 · Pro 20x");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("剩余 38.5%");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("US$12.50");
		expect(container.querySelector(".subscription-model-note")?.textContent).toContain("GPT-5.6 Sol");
		expect(container.querySelector(".subscription-provider")?.closest(".provider-workspace")?.querySelector(".provider-model-catalog")).not.toBeNull();
		expect(container.querySelector(".subscription-provider")?.querySelector(".provider-state-control")).not.toBeNull();
		const modelRows = Array.from(container.querySelectorAll<HTMLElement>(".subscription-model"));
		const currentModel = modelRows.find((row) => row.textContent?.includes("GPT-5.6 Sol"))!;
		const approvalModel = modelRows.find((row) => row.textContent?.includes("Codex Auto Review"))!;
		const unassignedCodexModel = modelRows.find((row) => row.textContent?.includes("GPT-5.3 Codex Spark"))!;
		const subagentModel = modelRows.find((row) => row.textContent?.includes("GPT Review Worker"))!;
		expect(currentModel.querySelector(".model-use-badge")?.textContent).toBe("当前主模型");
		expect(approvalModel.querySelector(".model-use-badge")?.textContent).toBe("审批");
		expect(unassignedCodexModel.querySelector(".model-use-badge")).toBeNull();
		expect(subagentModel.querySelector(".model-use-badge")?.textContent).toBe("子智能体");
		const subscriptionCapabilities = container.querySelector(".subscription-model-capabilities")!;
		expect(subscriptionCapabilities.querySelectorAll(".model-capability-popover")).toHaveLength(5);
		expect(subscriptionCapabilities.querySelector(".model-capability-text")).toBeNull();
		expect(container.querySelector(".model-capability-overflow")).toBeNull();
		expect(container.querySelector(".provider-model-identity small")).toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="工具调用"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="思考"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="结构化输出"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="输入: text"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="输出: text"]')).not.toBeNull();
		vi.mocked(execute).mockClear();
		await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="禁用 GPT-5.6 Sol"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_model_enabled", sessionId: "session-1", target: "chatgpt", name: "gpt-5.6-sol", decision: "false" });
		await act(async () => useRuntimeStore.setState({ modelProviders: useRuntimeStore.getState().modelProviders.map((provider) => provider.id === "chatgpt" ? { ...provider, accountPlan: "prolite" } : provider) }));
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("订阅驱动 · Pro 5x");
		const grok = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-list button")).find((button) => button.textContent?.includes("Grok 订阅"))!;
		await act(async () => grok.click());
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("已连接 · grok@example.com");
		expect(container.querySelector(".subscription-quota-error")?.textContent).toContain("获取失败：grok quota returned HTTP 500");
		expect(container.querySelector(".subscription-quota-heading")?.textContent).toContain("来自订阅服务的实时额度");
		expect(container.querySelector(".subscription-model-note")?.textContent).toContain("Grok 4.20");
		const refreshModels = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-model-actions button")).find((button) => button.textContent?.includes("获取模型"));
		expect(refreshModels).not.toBeNull();
		vi.mocked(execute).mockClear();
		await act(async () => refreshModels!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "discover_provider_models", sessionId: "session-1", target: "grok" });
		const chatgpt = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-list button")).find((button) => button.textContent?.includes("ChatGPT 订阅"))!;
		await act(async () => chatgpt.click());
		vi.mocked(execute).mockClear();
		await act(async () => container.querySelector<HTMLButtonElement>('.subscription-provider [role="switch"][aria-label="退出登录"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "logout", sessionId: "session-1", target: "chatgpt/account-1" });
		await act(async () => root.unmount());
		container.remove();
	});

	it("updates the global interface font and size from appearance settings", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], modelProviders: [], settingsOpen: true, uiFont: "system", uiFontSize: 14,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => Promise.resolve());

		const appearanceNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("外观"))!;
		await act(async () => appearanceNav.click());
		const fontMenu = container.querySelector<HTMLDetailsElement>(".font-family-menu")!;
		fontMenu.open = true;
		await act(async () => fontMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		const search = container.querySelector<HTMLInputElement>('.menu-select-options-portal input[placeholder="搜索字体名称…"]')!;
		await enterInput(search, "宋体");
		expect(container.querySelector('.menu-select-options-portal [data-value="PingFang SC"]')).toBeNull();
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="Songti SC"]')!.click());
		await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="增大界面字号"]')!.click());

		expect(listSystemFonts).toHaveBeenCalledWith("zh-CN");
		expect(useRuntimeStore.getState().uiFont).toBe("Songti SC");
		expect(useRuntimeStore.getState().uiFontSize).toBe(15);
		expect(container.querySelector(".font-size-control output")?.textContent).toBe("15 px");
		expect(container.querySelector<HTMLButtonElement>('[aria-label="增大界面字号"]')?.textContent).toBe("A+");
		await act(async () => root.unmount());
		container.remove();
	});

	it("updates chat-surface UI and code font sizes from appearance settings", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], modelProviders: [], settingsOpen: true, chatFontSize: 13, chatCodeFontSize: 12,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => Promise.resolve());

		const appearanceNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("外观"))!;
		await act(async () => appearanceNav.click());
		const chatCard = container.querySelector<HTMLElement>('[data-setting-id="appearance:chat-text"]')!;
		expect(chatCard.textContent).toContain("聊天文本");
		expect(chatCard.textContent).toContain("UI 文本");
		expect(chatCard.textContent).toContain("代码字体大小");
		const uiRow = container.querySelector<HTMLElement>('[data-setting-id="appearance:chat-font-size"]')!;
		const codeRow = container.querySelector<HTMLElement>('[data-setting-id="appearance:chat-code-font-size"]')!;
		expect(uiRow.querySelector("output")?.textContent).toBe("13 px");
		expect(codeRow.querySelector("output")?.textContent).toBe("12 px");

		await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="增大聊天 UI 文本"]')!.click());
		await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="增大代码字体"]')!.click());

		expect(useRuntimeStore.getState().chatFontSize).toBe(14);
		expect(useRuntimeStore.getState().chatCodeFontSize).toBe(13);
		expect(uiRow.querySelector("output")?.textContent).toBe("14 px");
		expect(codeRow.querySelector("output")?.textContent).toBe("13 px");
		expect(document.documentElement.style.getPropertyValue("--chat-ui-font-size")).toBe("14px");
		expect(document.documentElement.style.getPropertyValue("--chat-code-font-size")).toBe("13px");
		await act(async () => root.unmount());
		container.remove();
	});

	it("updates the live subagent, shell, and admission timeout limits", async () => {
		useRuntimeStore.setState({
			snapshot: { ...snapshot, subagentConcurrency: 4, subagentMaxDepth: 2, shellConcurrency: 3, shellMaxWallClockSeconds: 600, subagentAwaitSeconds: 600, subagentIdleSeconds: 0 },
			approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));

		const runtimeNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.querySelector("strong")?.textContent === "子智能体")!;
		await act(async () => runtimeNav.click());
		const pane = container.querySelector(".subagent-settings-pane")!;
		expect(pane.querySelector(".subagent-capacity")?.textContent).toContain("容量与隔离");
		expect(pane.querySelector(".subagent-scheduling .subagent-policy")?.getAttribute("role")).toBe("note");
		expect(pane.querySelector(".subagent-scheduling")?.textContent).toContain("只读");
		expect(pane.querySelector(".subagent-display")?.textContent).toContain("主会话展示");
		const concurrency = pane.querySelector<HTMLElement>('[data-setting-id="subagents:concurrency"]')!;
		const depth = pane.querySelector<HTMLElement>('[data-setting-id="subagents:depth"]')!;
		const shell = pane.querySelector<HTMLElement>('[data-setting-id="subagents:shell"]')!;
		const shellWall = pane.querySelector<HTMLElement>('[data-setting-id="subagents:shell-wall"]')!;
		const timeout = pane.querySelector<HTMLElement>('[data-setting-id="subagents:timeout"]')!;
		const idle = pane.querySelector<HTMLElement>('[data-setting-id="subagents:idle"]')!;
		expect(pane.querySelectorAll(".subagent-capacity .setting-row")).toHaveLength(6);
		vi.mocked(execute).mockClear();
		await act(async () => concurrency.querySelectorAll<HTMLButtonElement>("button")[1].click());
		await act(async () => shell.querySelectorAll<HTMLButtonElement>("button")[0].click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_concurrency", sessionId: "session-1", target: "5" });
		expect(execute).toHaveBeenCalledWith({ kind: "set_shell_concurrency", sessionId: "session-1", target: "2" });
		expect(shellWall.textContent).toContain("一条 coding.shell 命令的最长运行时间");
		const wallMenu = shellWall.querySelector<HTMLDetailsElement>(".capacity-timeout-menu")!;
		wallMenu.open = true;
		await act(async () => wallMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="1800"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_shell_max_wall_clock", sessionId: "session-1", target: "1800" });
		expect(shellWall.querySelector(".menu-select-value")?.textContent).toBe("30 分钟");
		const depthMenu = depth.querySelector<HTMLDetailsElement>(".capacity-depth-menu")!;
		depthMenu.open = true;
		await act(async () => depthMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="3"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_depth", sessionId: "session-1", target: "3" });

		expect(timeout.textContent).toContain("默认等到前台完成");
		const timeoutMenu = timeout.querySelector<HTMLDetailsElement>(".capacity-timeout-menu")!;
		timeoutMenu.open = true;
		await act(async () => timeoutMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		expect(container.querySelector('.menu-select-options-portal [data-value="0"]')?.textContent).toContain("直到完成");
		expect(container.querySelector('.menu-select-options-portal [data-value="30"]')).not.toBeNull();
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="0"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_await_timeout", sessionId: "session-1", target: "0" });
		timeoutMenu.open = true;
		await act(async () => timeoutMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="30"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_await_timeout", sessionId: "session-1", target: "30" });

		const displaySwitch = pane.querySelector<HTMLElement>(".subagent-display [role='switch']")!;
		expect(displaySwitch.tagName).toBe("SPAN");
		expect(displaySwitch.getAttribute("aria-disabled")).toBe("true");
		vi.mocked(execute).mockClear();
		await act(async () => displaySwitch.dispatchEvent(new MouseEvent("click", { bubbles: true })));
		expect(execute).not.toHaveBeenCalled();
		expect(pane.querySelector(".subagent-scheduling [title]")).toBeNull();
		expect(pane.querySelector(".subagent-display [title]")).toBeNull();
		expect(pane.querySelector(".subagent-capacity [title]")).toBeNull();
		expect(timeout.querySelector("summary")?.getAttribute("title")).toBeNull();
		expect(timeout.querySelector(".menu-select-value")?.textContent).toBe("30 秒");

		expect(idle.textContent).toContain("没有思考、输出或工具活动");
		const idleMenu = idle.querySelector<HTMLDetailsElement>(".capacity-timeout-menu")!;
		idleMenu.open = true;
		await act(async () => idleMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		const idlePortal = Array.from(container.querySelectorAll<HTMLElement>(".menu-select-options-portal")).find((portal) => portal.textContent?.includes("关闭"));
		expect(idlePortal).not.toBeNull();
		await act(async () => idlePortal!.querySelector<HTMLButtonElement>('[data-value="300"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_idle_timeout", sessionId: "session-1", target: "300" });
		expect(idle.querySelector(".menu-select-value")?.textContent).toBe("5 分钟");

		// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
		const { readFileSync } = await import("node:fs");
		const settingsStyles = readFileSync("src/styles/settings.css", "utf8");
		const prototypeStyles = readFileSync("src/prototype.css", "utf8");
		expect(settingsStyles).toMatch(/\.subagent-settings-pane\s*\{[^}]*width:\s*min\(760px,\s*100%\);[^}]*margin-inline:\s*auto;[^}]*grid-template-columns:\s*1fr;/s);
		expect(settingsStyles).toMatch(/\.capacity-timeout-menu\s*>\s*summary,\s*\n\.capacity-depth-menu\s*>\s*summary\s*\{[^}]*justify-content:\s*center;[^}]*padding-inline:\s*22px;/s);
		expect(settingsStyles).toMatch(/\.capacity-timeout-menu\s+\.menu-select-value,\s*\n\.capacity-depth-menu\s+\.menu-select-value\s*\{[^}]*text-align:\s*center;/s);
		expect(settingsStyles).toMatch(/\.capacity-depth-menu\s*\{\s*width:\s*112px;/s);
		expect(settingsStyles).toMatch(/\.capacity-timeout-menu\s*\{\s*width:\s*128px;/s);
		expect(settingsStyles).toMatch(/\.compact-stepper\s*\{[^}]*width:\s*112px;/s);
		expect(prototypeStyles).toMatch(/\.model-routes-pane,\s*\n\.governance-pane,\s*\n\.appearance-pane\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*none;/s);
		expect(prototypeStyles).not.toMatch(/\.subagent-settings-pane\s*\{[^}]*grid-template-columns:\s*minmax\(330px/);
		expect(prototypeStyles).not.toMatch(/\.subagent-settings-pane[^{]*\{[^}]*margin-inline:\s*0/);
		expect(prototypeStyles).not.toMatch(/\.runtime-invariant/);
		expect(prototypeStyles).not.toMatch(/\.subagent-capacity-grid/);
		expect(depth.querySelector(".capacity-depth-menu")?.classList.contains("menu-select")).toBe(true);
		expect(depth.querySelector(".menu-select-value")?.textContent).toBe("3");

		await act(async () => root.unmount());
		container.remove();
	});

	it("disables a skill from settings while keeping it visible for re-enabling", async () => {
		const entries = [
			{ name: "verify", description: "验证当前工作区", sourcePath: "bundled/verify", bundled: true, eager: true, disabled: false, modelVisible: true, resourceCount: 2 },
			{ name: "unused-design", description: "当前项目不需要加载", sourcePath: "~/.codex/skills/unused-design", bundled: false, eager: false, disabled: true, modelVisible: false, resourceCount: 4 },
		];
		vi.mocked(listSkillCatalog).mockResolvedValueOnce({ entries, diagnostics: [] });
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {}, agentCatalog: [], modelProviders: [], settingsOpen: true,
			skills: entries,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));

		const extensionsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("扩展"))!;
		await act(async () => extensionsNav.click());
		const skillsTab = Array.from(container.querySelectorAll<HTMLButtonElement>('.extension-tabbar [role="tab"]')).find((button) => button.textContent?.includes("Skills"))!;
		await act(async () => skillsTab.click());
		expect(container.querySelector(".skill-manager")?.textContent).toContain("unused-design");
		vi.mocked(execute).mockClear();
		await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"][aria-label="停用 verify"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_skill_enabled", target: "verify", decision: "false", sessionId: "session-1" });

		await act(async () => root.unmount());
		container.remove();
	});

	it("opens the archive section with conversations grouped by project", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {}, agentCatalog: [], modelProviders: [],
			skills: [], plugins: [], settingsOpen: true,
			sessions: [
				{ id: "archived-1", workspace: "/workspace/azem", title: "旧 Azem 会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", archived: true, updatedAt: "2026-01-01T00:00:00.000Z" },
				{ id: "archived-2", workspace: "/workspace/venat", title: "旧 Venat 会话", providerId: "chatgpt", modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", archived: true, updatedAt: "2026-02-01T00:00:00.000Z" },
			],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const archiveNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.querySelector("strong")?.textContent === "归档")!;
		expect(archiveNav.querySelector("em")).toBeNull();
		expect(archiveNav.textContent).not.toContain("2");
		await act(async () => archiveNav.click());
		expect(container.querySelector(".settings-main")?.getAttribute("data-section")).toBe("archive");
		const azem = container.querySelector<HTMLElement>('[data-archive-project="/workspace/azem"]')!;
		const venat = container.querySelector<HTMLElement>('[data-archive-project="/workspace/venat"]')!;
		expect(azem.getAttribute("data-expanded")).toBe("false");
		expect(venat.getAttribute("data-expanded")).toBe("false");
		expect(azem.textContent).toContain("/workspace/azem");
		expect(venat.textContent).toContain("/workspace/venat");
		expect(azem.querySelector(".archive-session-row")).toBeNull();
		expect(venat.querySelector(".archive-session-row")).toBeNull();
		await act(async () => azem.querySelector<HTMLButtonElement>(".archive-project-toggle")!.click());
		expect(azem.textContent).toContain("旧 Azem 会话");
		expect(azem.querySelector(".archive-session-project")?.textContent).toBe("azem");
		expect(venat.querySelector(".archive-session-row")).toBeNull();
		await act(async () => root.unmount());
		container.remove();
	});

	it("does not show a count badge on the catalog sidebar row", async () => {
		const unusedModels = Array.from({ length: 16 }, (_, index) => ({
			id: `unused-${index}`, name: `Unused ${index}`, contextWindow: 128000, reasoningLevels: ["low"], defaultReasoning: "low",
		}));
		const openrouterModels = [
			...Array.from({ length: 10 }, (_, index) => ({
				id: `openrouter-on-${index}`, name: `OpenRouter ${index}`, contextWindow: 128000, reasoningLevels: ["low"], defaultReasoning: "low",
			})),
			...Array.from({ length: 5 }, (_, index) => ({
				id: `openrouter-off-${index}`, name: `Disabled ${index}`, contextWindow: 128000, disabled: true, reasoningLevels: ["low"], defaultReasoning: "low",
			})),
		];
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], agentCatalog: [], skills: [], plugins: [], settingsOpen: true,
			modelsByProvider: {
				chatgpt: [
					{ id: "gpt-a", name: "A", reasoningLevels: ["low"] },
					{ id: "gpt-b", name: "B", reasoningLevels: ["low"] },
					{ id: "gpt-c", name: "C", reasoningLevels: ["low"] },
					{ id: "gpt-off-1", name: "Off 1", disabled: true, reasoningLevels: ["low"] },
					{ id: "gpt-off-2", name: "Off 2", disabled: true, reasoningLevels: ["low"] },
				],
				grok: [
					{ id: "grok-a", name: "GA", reasoningLevels: ["low"] },
					{ id: "grok-b", name: "GB", reasoningLevels: ["low"] },
					{ id: "grok-off", name: "GO", disabled: true, reasoningLevels: ["low"] },
				],
			},
			modelProviders: [{
				id: "chatgpt", displayName: "OpenAI / ChatGPT 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true, credentialConfigured: true, credentialSource: "stored", models: [],
			}, {
				id: "grok", displayName: "Grok 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true, credentialConfigured: true, credentialSource: "stored", models: [],
			}, {
				id: "openrouter", displayName: "OpenRouter", backend: "openai_compat",
				defaultBaseUrl: "https://openrouter.ai/api/v1", baseUrl: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY",
				enabled: true, credentialConfigured: true, credentialSource: "stored", models: openrouterModels,
			}, {
				id: "unused", displayName: "Unused", backend: "openai_compat",
				defaultBaseUrl: "https://unused.example/v1", baseUrl: "https://unused.example/v1", envKey: "UNUSED_API_KEY",
				enabled: false, credentialConfigured: false, credentialSource: "none", models: unusedModels,
			}],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型目录"))!;
		expect(catalogNav.querySelector("em")).toBeNull();
		expect(catalogNav.textContent).not.toContain("15");
		expect(catalogNav.textContent).not.toContain("31");
		await act(async () => root.unmount());
		container.remove();
	});

	it("confirms plugin hook trust from the settings dialog overlay", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true,
			hookCatalog: {
				enabled: true, trustHooks: false,
				sources: [{
					id: "demo@local", name: "Ponytail", origin: "plugin", pluginId: "demo@local", source: "",
					hookCount: 2, trusted: false, warning: "Hooks 等待用户信任",
				}],
				commands: [], diagnostics: [],
			},
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const extensionsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("扩展"))!;
		await act(async () => extensionsNav.click());
		const hooksTab = Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]')).find((button) => button.textContent?.includes("Hooks"));
		await act(async () => hooksTab!.click());
		vi.mocked(execute).mockClear();
		const toggle = container.querySelector<HTMLButtonElement>('button[role="switch"][aria-label="信任插件 Hooks"]');
		await act(async () => toggle!.click());
		await act(async () => Promise.resolve());
		const settingsDialog = container.querySelector("dialog.settings-dialog");
		const overlay = settingsDialog?.querySelector<HTMLElement>('[role="alertdialog"]')
			?? document.querySelector<HTMLElement>('[role="alertdialog"]');
		expect(overlay?.closest("dialog.settings-dialog")).toBe(settingsDialog);
		expect(overlay?.querySelector("h2")?.textContent).toBe("信任插件 Hooks？");
		expect(overlay?.querySelector("h3")).toBeNull();
		const confirm = Array.from(overlay!.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "信任并启用");
		expect(confirm?.disabled).toBe(false);
		await act(async () => confirm!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_plugin_hooks_trusted", decision: "true", sessionId: "session-1" });
		await act(async () => root.unmount());
		container.remove();
	});

	it("hosts the add MCP modal on the settings dialog", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true, mcpServers: [],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
		const extensionsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("扩展"))!;
		await act(async () => extensionsNav.click());
		const add = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("添加 MCP 服务"));
		await act(async () => add!.click());
		const settingsDialog = container.querySelector("dialog.settings-dialog");
		const overlay = settingsDialog?.querySelector<HTMLElement>(".mcp-add-dialog")
			?? document.querySelector<HTMLElement>(".mcp-add-dialog");
		expect(overlay?.closest("dialog.settings-dialog")).toBe(settingsDialog);
		expect(overlay?.getAttribute("role")).toBe("dialog");
		expect(overlay?.getAttribute("aria-modal")).toBe("true");
		expect(container.querySelector(".mcp-drawer")).toBeNull();
		expect(document.activeElement).toBe(overlay?.querySelector('input[placeholder="local-tools"]'));
		expect(document.activeElement).not.toBe(container.querySelector(".settings-back"));

		const name = overlay!.querySelector<HTMLInputElement>('input[placeholder="local-tools"]')!;
		const command = overlay!.querySelector<HTMLInputElement>('input[placeholder="例如 npx"]')!;
		await enterInput(name, "local-review");
		await enterInput(command, "npx");
		vi.mocked(execute).mockClear();
		await act(async () => overlay!.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
		await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
		expect(execute).toHaveBeenCalledWith(expect.objectContaining({
			kind: "upsert_mcp_server", sessionId: "session-1",
			payload: expect.objectContaining({ name: "local-review", transport: "stdio", command: "npx", enabled: true }),
		}));
		expect(container.querySelector(".mcp-add-dialog")).toBeNull();
		expect(document.activeElement).toBe(add);
		await act(async () => root.unmount());
		container.remove();
	});

	it("does not show a count badge on the extensions sidebar row", async () => {
		const plugins = Array.from({ length: 13 }, (_, index) => ({
			id: `plugin-${index}@market`, name: `plugin-${index}`, displayName: `Plugin ${index}`, version: "1.0.0",
			marketplace: "market", origin: "codex", description: "Plugin", developerName: "Azem", category: "Developer Tools",
			brandColor: "", logoPath: "", enabled: true, skillCount: 0, mcpServerCount: 0, integratedMCPCount: 0,
			hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: [], status: "ready", warning: "",
		}));
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins, modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const extensionsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("扩展"))!;
		expect(extensionsNav.querySelector("em")).toBeNull();
		expect(extensionsNav.textContent).not.toContain("13");
		await act(async () => root.unmount());
		container.remove();
	});

	it("keeps subscription model enable toggles aligned when a use badge is present", async () => {
		// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
		const { readFileSync } = await import("node:fs");
		const prototypeStyles = readFileSync("src/prototype.css", "utf8");
		expect(prototypeStyles).toMatch(/\.provider-model-card-main\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto;[^}]*align-items:\s*center;/s);
		expect(prototypeStyles).toMatch(/\.provider-model-identity > span\s*\{[^}]*display:\s*flex;[^}]*align-items:\s*center;/s);
		expect(prototypeStyles).toMatch(/\.provider-model-state\s*\{[^}]*display:\s*flex;[^}]*align-items:\s*center;/s);
		expect(prototypeStyles).not.toMatch(/\.provider-model-state\s*\{[^}]*display:\s*grid;/s);

		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [
				{ scope: "subagent", role: "review", label: "Review", route: { provider: "grok", model: "grok-4.6", reasoning: "high" } },
			], modelsByProvider: {
				grok: [
					{ id: "grok-4.5", name: "Grok 4.5", reasoningLevels: ["low", "medium", "high"] },
					{ id: "grok-4.6", name: "Grok 4.6", reasoningLevels: ["low", "medium", "high"] },
				],
			}, agentCatalog: [], skills: [], settingsOpen: true,
			modelProviders: [{
				id: "grok", displayName: "Grok 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true, credentialConfigured: true, credentialSource: "stored", accountId: "account-2", accountLabel: "grok@example.com", modelsDevId: "xai", models: [],
			}],
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型目录"))!;
		await act(async () => catalogNav.click());
		const modelRows = Array.from(container.querySelectorAll<HTMLElement>(".subscription-model"));
		const plainModel = modelRows.find((row) => row.textContent?.includes("Grok 4.5"))!;
		const badgedModel = modelRows.find((row) => row.textContent?.includes("Grok 4.6"))!;
		expect(plainModel.querySelector(".model-use-badge")).toBeNull();
		expect(badgedModel.querySelector(".provider-model-identity .model-use-badge")?.textContent).toBe("子智能体");
		expect(badgedModel.querySelector(".provider-model-state .model-use-badge")).toBeNull();
		expect(plainModel.querySelector(".provider-model-state")?.children).toHaveLength(1);
		expect(badgedModel.querySelector(".provider-model-state")?.children).toHaveLength(1);
		expect(plainModel.querySelector(".provider-model-state .model-card-toggle")).not.toBeNull();
		expect(badgedModel.querySelector(".provider-model-state .model-card-toggle")).not.toBeNull();
		await act(async () => root.unmount());
		container.remove();
	});

	it("focuses the dialog on open instead of drawing a back-button outline", async () => {
		// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
		const { readFileSync } = await import("node:fs");
		const settingsStyles = readFileSync("src/styles/settings.css", "utf8");
		const prototypeStyles = readFileSync("src/prototype.css", "utf8");
		for (const css of [settingsStyles, prototypeStyles]) {
			expect(css).toMatch(/\.settings-back:focus\s*\{[^}]*outline:\s*none;/);
			expect(css).toMatch(/\.settings-back:focus-visible\s*\{[^}]*outline:\s*2px solid color-mix\(in srgb, var\(--blue\) 75%, white\);[^}]*outline-offset:\s*2px;/);
			expect(css).not.toMatch(/\.settings-back\s*\{[^}]*outline:\s*none;/);
		}

		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

		const dialog = container.querySelector<HTMLDialogElement>("dialog.settings-dialog")!;
		const back = container.querySelector<HTMLButtonElement>(".settings-back")!;
		expect(dialog.getAttribute("tabindex")).toBe("-1");
		expect(document.activeElement).toBe(dialog);
		expect(document.activeElement).not.toBe(back);
		expect(back.matches(":focus")).toBe(false);

		await act(async () => back.focus());
		expect(document.activeElement).toBe(back);

		await act(async () => dialog.dispatchEvent(new Event("cancel", { bubbles: true })));
		expect(useRuntimeStore.getState().settingsOpen).toBe(false);

		await act(async () => root.unmount());
		container.remove();
	});

	it("opens a global-search target on the exact settings control", async () => {
		const scrollIntoView = vi.fn();
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {}, agentCatalog: [], modelProviders: [],
			skills: [], plugins: [], settingsOpen: true, settingsTarget: { section: "appearance", id: "appearance:font-size" },
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

		expect(container.querySelector(".settings-main")?.getAttribute("data-section")).toBe("appearance");
		const target = container.querySelector<HTMLElement>('[data-setting-id="appearance:font-size"]');
		expect(target).not.toBeNull();
		expect(scrollIntoView).toHaveBeenCalled();

		await act(async () => root.unmount());
		container.remove();
	});

	it("uses Approvals as the English nav and page title and still finds the section by the old English name", async () => {
		useRuntimeStore.setState({
			snapshot: { ...snapshot, language: "en" }, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		const navLabel = (button: HTMLButtonElement) => button.querySelector("strong")?.textContent;
		const approvalsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => navLabel(button) === "Approvals");
		expect(approvalsNav).toBeTruthy();
		expect(approvalsNav?.textContent).not.toMatch(/Governance &|Governance and/);
		await act(async () => approvalsNav!.click());
		expect(container.querySelector(".settings-main h1")?.textContent).toBe("Approvals");
		expect(container.querySelector(".settings-main")?.getAttribute("data-section")).toBe("governance");

		const search = container.querySelector<HTMLInputElement>(".settings-search input")!;
		await enterInput(search, "governance");
		expect(Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).some((button) => navLabel(button) === "Approvals")).toBe(true);
		await enterInput(search, "governance & approvals");
		expect(Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).some((button) => navLabel(button) === "Approvals")).toBe(true);

		await act(async () => root.unmount());
		container.remove();
	});

	it("opens the usage section and does not fetch usage until that page is selected", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], plugins: [], modelProviders: [], settingsOpen: true, usageReport: null,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));
		await act(async () => Promise.resolve());
		expect(listUsageReport).not.toHaveBeenCalled();
		const usageNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.querySelector("strong")?.textContent === "用量")!;
		expect(usageNav.querySelector("small")?.textContent).toBe("Token 与活动记录");
		await act(async () => usageNav.click());
		expect(container.querySelector(".settings-main")?.getAttribute("data-section")).toBe("usage");
		expect(container.querySelector(".settings-main h1")?.textContent).toBe("用量");
		await act(async () => Promise.resolve());
		expect(listUsageReport).toHaveBeenCalledWith("project");
		expect(container.querySelector(".usage-empty")?.textContent).toContain("还没有用量");
		await act(async () => root.unmount());
		container.remove();
	});
});
