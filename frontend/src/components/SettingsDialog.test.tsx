import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, listSystemFonts } from "../bridge";
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

  it("loads role routes and saves a subagent model selection", async () => {
    useRuntimeStore.setState({
      snapshot,
      approvalMode: snapshot.approvalMode,
      modelRoutes: [
		{ Scope: "main", Role: "", Label: "Main", Route: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high" } },
        { Scope: "title", Role: "", Label: "Title", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
        { Scope: "plan", Role: "", Label: "Plan", Route: {} },
        { Scope: "subagent", Role: "explore", Label: "Explore", Route: {} },
      ],
      agentCatalog: [{ name: "explore", description: "只读探索代码库", model: "", reasoning: "", capabilityMode: "read-only", isolation: "none", source: "builtin", enabled: true }],
      modelsByProvider: {
        chatgpt: [
          { id: "gpt-5.6-sol", name: "5.6 Sol", reasoningLevels: ["medium", "high"], defaultReasoning: "high" },
		  { id: "gpt-5.6-luna", name: "5.6 Luna", aliases: ["codex-fast"], reasoningLevels: ["low", "medium"], defaultReasoning: "medium" },
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
    expect(execute).toHaveBeenCalledWith({ kind: "list_model_routes", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_agent_types", sessionId: "session-1" });
    expect(execute).toHaveBeenCalledWith({ kind: "list_models", sessionId: "session-1" });
	expect(execute).toHaveBeenCalledWith({ kind: "list_model_providers", sessionId: "session-1" });
	expect(container.querySelectorAll(".route-row")).toHaveLength(3);
	expect(container.querySelectorAll(".route-table-header")).toHaveLength(1);
	expect(container.querySelector(".route-table-header")?.textContent).toContain("提供商模型推理强度");
	expect(container.textContent).not.toContain("默认会话模型");
    const titleRoute = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("会话标题"))!;
    expect(titleRoute.textContent).toContain("根据首轮用户消息自动生成侧栏会话标题");
    expect(titleRoute.textContent).toContain("5.6 Luna");

    vi.mocked(execute).mockClear();
    const explore = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("explore"))!;
    const modelMenu = explore.querySelector<HTMLDetailsElement>(".route-model-menu")!;
    modelMenu.open = true;
    await act(async () => modelMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
	const modelSearch = container.querySelector<HTMLInputElement>('.menu-select-options-portal input[placeholder="搜索模型名称或别名…"]')!;
	await enterInput(modelSearch, "codex-fast");
	expect(container.querySelector('.menu-select-options-portal [data-value="gpt-5.6-sol"]')).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="gpt-5.6-luna"]')!.click());
    await act(async () => explore.querySelector<HTMLButtonElement>(".route-actions .small-button")!.click());

    expect(execute).toHaveBeenCalledWith({
      kind: "set_model_route", target: "", sessionId: "session-1",
      route: { Scope: "subagent", Role: "explore", Label: "Explore", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "medium" } },
    });
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
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型设置"))!;
		await act(async () => catalogNav.click());
		const editor = container.querySelector<HTMLElement>(".provider-editor")!;
		const directory = container.querySelector<HTMLDivElement>(".provider-list")!;
		expect(directory.querySelectorAll("button")).toHaveLength(25);
		Object.defineProperties(directory, { scrollHeight: { configurable: true, value: 1000 }, clientHeight: { configurable: true, value: 400 } });
		directory.scrollTop = 560;
		await act(async () => directory.dispatchEvent(new Event("scroll", { bubbles: true })));
		expect(directory.querySelectorAll("button")).toHaveLength(49);
		expect(container.querySelector(".provider-more")?.textContent).toContain("剩余 7 个");
		expect(container.querySelector<HTMLImageElement>('.provider-list img[src="https://models.dev/logos/openrouter.svg"]')).not.toBeNull();
		expect(container.querySelector<HTMLImageElement>('.provider-list img[src="https://models.dev/logos/302ai.svg"]')).not.toBeNull();
		expect(editor.querySelector<HTMLImageElement>('header img[src="https://models.dev/logos/openrouter.svg"]')).not.toBeNull();
		const capabilities = editor.querySelector(".model-capabilities")!;
		expect(capabilities.querySelector('[aria-label="工具调用"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="思考"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="输入: text"]')).not.toBeNull();
		expect(capabilities.querySelector('[aria-label="输出: text"]')).not.toBeNull();
		expect(editor.querySelector('.provider-model-levels [aria-label="轻度"]')).not.toBeNull();
		expect(editor.querySelector('.provider-model-levels [aria-label="高"]')).not.toBeNull();
		expect(editor.querySelector(".provider-default-menu")).not.toBeNull();
		expect(editor.querySelector(".provider-default-menu summary")?.textContent).toContain("高");
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
			snapshot, approvalMode: snapshot.approvalMode, modelRoutes: [], modelsByProvider: {
				chatgpt: [{ id: "gpt-5.6-sol", name: "GPT-5.6 Sol", reasoningLevels: ["medium", "high"] }],
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
		const catalogNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("模型设置"))!;
		await act(async () => catalogNav.click());
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("每周额度");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("订阅等级 Pro 20x");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("剩余 38.5%");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("US$12.50");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("GPT-5.6 Sol");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("gpt-5.6-sol");
		const subscriptionCapabilities = container.querySelector(".subscription-model-capabilities")!;
		expect(subscriptionCapabilities.querySelector('[aria-label="工具调用"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="思考"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="结构化输出"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="输入: text"]')).not.toBeNull();
		expect(subscriptionCapabilities.querySelector('[aria-label="输出: text"]')).not.toBeNull();
		await act(async () => useRuntimeStore.setState({ modelProviders: useRuntimeStore.getState().modelProviders.map((provider) => provider.ID === "chatgpt" ? { ...provider, AccountPlan: "prolite" } : provider) }));
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("订阅等级 Pro 5x");
		const grok = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-list button")).find((button) => button.textContent?.includes("Grok 订阅"))!;
		await act(async () => grok.click());
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("获取失败");
		expect(container.querySelector(".subscription-provider")?.textContent).not.toContain("HTTP 500");
		expect(container.querySelector(".subscription-provider")?.textContent).toContain("Grok 4.20");
		const chatgpt = Array.from(container.querySelectorAll<HTMLButtonElement>(".provider-list button")).find((button) => button.textContent?.includes("ChatGPT 订阅"))!;
		await act(async () => chatgpt.click());
		vi.mocked(execute).mockClear();
		await act(async () => container.querySelector<HTMLButtonElement>(".subscription-provider-body button")!.click());
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

		const appearanceNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-nav-group button")).find((button) => button.textContent?.includes("界面"))!;
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
		await act(async () => root.unmount());
		container.remove();
	});
});
