import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, listSkillCatalog, listSystemFonts } from "../bridge";
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
  afterEach(() => vi.clearAllMocks());

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

  it("loads role routes and saves a subagent model selection", async () => {
    useRuntimeStore.setState({
      snapshot,
      approvalMode: snapshot.approvalMode,
      modelRoutes: [
		{ Scope: "main", Role: "", Label: "Main", Route: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high" } },
        { Scope: "title", Role: "", Label: "Title", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
        { Scope: "plan", Role: "", Label: "Plan", Route: {} },
		{ Scope: "approval", Role: "", Label: "Approval", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
		{ Scope: "vision", Role: "", Label: "Vision", Route: {} },
		{ Scope: "recap", Role: "", Label: "Recap", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
        { Scope: "subagent", Role: "explore", Label: "Explore", Route: {} },
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
		ID: "openrouter", DisplayName: "OpenRouter", Backend: "openai_compat",
		DefaultBaseURL: "https://openrouter.ai/api/v1", BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY",
		Enabled: false, CredentialConfigured: false, CredentialSource: "none",
		Models: [{ id: "openai/gpt-test", name: "GPT Test", contextWindow: 128000, reasoningLevels: ["low", "high"], defaultReasoning: "high" }],
	  }],
      skills: [],
      settingsOpen: true,
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<SettingsDialog />));
	expect(container.querySelector(".settings-search kbd")?.textContent).toBe("⌘F");
	expect(container.querySelector(".settings-nav-group button em")?.textContent).toBe("1");
    const routesNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型路由"))!;
    await act(async () => routesNav.click());
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_routes", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_agent_types", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_models", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_providers", sessionId: "session-1" });
	expect(execute).toHaveBeenCalledWith({ kind: "list_plugins", sessionId: "session-1" });
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
      route: { Scope: "subagent", Role: "explore", Label: "Explore", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "medium" } },
    });
    vi.mocked(execute).mockClear();
    const reasoningMenu = explore.querySelector<HTMLDetailsElement>(".route-reasoning-menu")!;
    expect(reasoningMenu.querySelector("summary")?.getAttribute("aria-label")).toBe("explore 推理强度");
    reasoningMenu.open = true;
    await act(async () => reasoningMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
    await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="low"]')!.click());
    expect(execute).toHaveBeenCalledWith({
      kind: "set_model_route", target: "", sessionId: "session-1",
      route: { Scope: "subagent", Role: "explore", Label: "Explore", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
    });
	const subagentsNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("子智能体"))!;
	await act(async () => subagentsNav.click());
	const subagentPane = container.querySelector(".subagent-settings-pane")!;
	expect(subagentPane.querySelectorAll(".subagent-capacity-grid > section")).toHaveLength(3);
	expect(subagentPane.querySelectorAll(".subagent-scheduling .runtime-invariant")).toHaveLength(3);
    await act(async () => root.unmount());
    container.remove();
  });

	it("saves an llmux provider and sends the API key only in the action", async () => {
		const extraProviders = Array.from({ length: 55 }, (_, index) => ({
			ID: index === 0 ? "ai302" : `provider-${String(index).padStart(2, "0")}`, DisplayName: index === 0 ? "302.AI" : `Provider ${index}`, Backend: "openai-compatible",
			DefaultBaseURL: `https://provider-${index}.example/v1`, BaseURL: "", EnvKey: `PROVIDER_${index}_API_KEY`,
			Enabled: false, CredentialConfigured: false, CredentialSource: "none" as const, ModelsDevID: index === 0 ? "302ai" : undefined, Models: [],
		}));
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {}, agentCatalog: [], skills: [], settingsOpen: true,
			modelProviders: [{
				ID: "openrouter", DisplayName: "OpenRouter", Backend: "openai_compat",
				DefaultBaseURL: "https://openrouter.ai/api/v1", BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY",
				Enabled: false, CredentialConfigured: false, CredentialSource: "none",
				Models: [{ id: "openai/gpt-test", name: "GPT Test", contextWindow: 128000, maxOutputTokens: 32000, reasoningLevels: ["low", "high"], defaultReasoning: "high", capabilities: ["tools", "reasoning"], inputModalities: ["text", "image"], outputModalities: ["text"] }],
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
			provider: expect.objectContaining({ ID: "openrouter", Enabled: true }),
		});
		vi.mocked(execute).mockClear();
		await act(async () => editor.querySelector<HTMLButtonElement>("footer .small-button")!.click());
		expect(execute).toHaveBeenCalledWith({
			kind: "set_model_provider", sessionId: "session-1", secret: "sk-test",
			provider: expect.objectContaining({ ID: "openrouter", Enabled: true, Models: [expect.objectContaining({ id: "openai/gpt-test" })] }),
		});
		await act(async () => root.unmount());
		container.remove();
	});

	it("shows subscription providers in model settings and starts the existing login flow", async () => {
		useRuntimeStore.setState({
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [
				{ Scope: "approval", Role: "", Label: "Approval", Route: { provider: "chatgpt", model: "codex-auto-review", reasoning: "high" } },
				{ Scope: "subagent", Role: "review", Label: "Review", Route: { provider: "chatgpt", model: "gpt-review-worker", reasoning: "high" } },
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
				ID: "chatgpt", DisplayName: "OpenAI / ChatGPT 订阅", Backend: "subscription", Subscription: true,
				DefaultBaseURL: "", BaseURL: "", EnvKey: "", Enabled: true, CredentialConfigured: true, CredentialSource: "stored", AccountID: "account-1", AccountLabel: "user@example.com", AccountPlan: "pro", ModelsDevID: "openai", Models: [],
				QuotaAvailable: true, QuotaUsedPercent: 61.5, QuotaResetsAt: 1786500000, QuotaBalance: "12.50",
			}, {
				ID: "grok", DisplayName: "Grok 订阅", Backend: "subscription", Subscription: true,
				DefaultBaseURL: "", BaseURL: "", EnvKey: "", Enabled: true, CredentialConfigured: true, CredentialSource: "stored", AccountID: "account-2", AccountLabel: "grok@example.com", ModelsDevID: "xai", Models: [], QuotaWarning: "grok quota returned HTTP 500",
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
		await act(async () => useRuntimeStore.setState({ modelProviders: useRuntimeStore.getState().modelProviders.map((provider) => provider.ID === "chatgpt" ? { ...provider, AccountPlan: "prolite" } : provider) }));
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("订阅驱动 · Pro 5x");
		const grok = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-list button")).find((button) => button.textContent?.includes("Grok 订阅"))!;
		await act(async () => grok.click());
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("获取失败");
		expect(container.querySelector(".subscription-provider")?.textContent).not.toContain("HTTP 500");
		expect(container.querySelector(".subscription-model-note")?.textContent).toContain("Grok 4.20");
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

	it("updates the live subagent, shell, and admission timeout limits", async () => {
		useRuntimeStore.setState({
			snapshot: { ...snapshot, subagentConcurrency: 4, shellConcurrency: 3, subagentAwaitSeconds: 600 },
			approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {},
			agentCatalog: [], skills: [], modelProviders: [], settingsOpen: true,
		});
		const container = document.createElement("div");
		document.body.append(container);
		const root = createRoot(container);
		await act(async () => root.render(<SettingsDialog />));

		const runtimeNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.querySelector("strong")?.textContent === "子智能体")!;
		await act(async () => runtimeNav.click());
		const capacityControls = container.querySelectorAll<HTMLElement>(".subagent-capacity-grid > section");
		expect(capacityControls).toHaveLength(3);
		vi.mocked(execute).mockClear();
		await act(async () => capacityControls[0].querySelectorAll<HTMLButtonElement>("button")[1].click());
		await act(async () => capacityControls[1].querySelectorAll<HTMLButtonElement>("button")[0].click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_concurrency", sessionId: "session-1", target: "5" });
		expect(execute).toHaveBeenCalledWith({ kind: "set_shell_concurrency", sessionId: "session-1", target: "2" });

		const timeoutMenu = capacityControls[2].querySelector<HTMLDetailsElement>(".capacity-timeout-menu")!;
		timeoutMenu.open = true;
		await act(async () => timeoutMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
		await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="30"]')!.click());
		expect(execute).toHaveBeenCalledWith({ kind: "set_subagent_await_timeout", sessionId: "session-1", target: "30" });

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
});
