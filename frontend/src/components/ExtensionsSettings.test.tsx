import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useRuntimeStore } from "../store";
import type { ActionRequest } from "../types";
import ExtensionsSettings from "./ExtensionsSettings";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const mounted: Array<{ root: ReturnType<typeof createRoot>; host: HTMLDivElement }> = [];

afterEach(async () => {
  while (mounted.length > 0) {
    const entry = mounted.pop()!;
    await act(async () => entry.root.unmount());
    entry.host.remove();
  }
});

function renderSettings(executeAction: (request: ActionRequest) => Promise<void>, openAddRequest = 0) {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  mounted.push({ root, host });
  const render = async (request = openAddRequest) => {
    await act(async () => root.render(<ExtensionsSettings language="zh-CN" sessionId="session-1" openAddRequest={request} executeAction={executeAction} onError={() => undefined} />));
  };
  return { host, render };
}

describe("ExtensionsSettings", () => {
  it("renders runtime MCP services and toggles the selected server", async () => {
    useRuntimeStore.setState({
      mcpServers: [{
        name: "grep", removable: true, enabled: true, state: "ready", transport: "streamable_http", target: "https://mcp.grep.app",
        args: [], inheritEnv: false, approval: "always", maxConcurrency: 2, toolCount: 3,
        tools: [{ name: "search", description: "Search code", effect: "read_only", requiresApproval: true }], error: "",
      }],
      skills: [], plugins: [],
    });
    const executeAction = vi.fn(async (_request: ActionRequest) => undefined);
    const view = renderSettings(executeAction);
    await view.render();

    expect(view.host.textContent).toContain("grep");
    expect(view.host.textContent).toContain("https://mcp.grep.app");
    expect(view.host.textContent).toContain("3 个工具");
		const overviewMetrics = view.host.querySelectorAll(".extension-stat-grid article");
		expect(overviewMetrics).toHaveLength(3);
		expect(overviewMetrics[0]?.textContent).toContain("1 / 1已连接 MCP");
    const toggle = view.host.querySelector<HTMLButtonElement>('button[role="switch"][aria-label="停用 grep"]');
    expect(toggle).not.toBeNull();
    await act(async () => toggle!.click());
    expect(executeAction).toHaveBeenCalledWith(expect.objectContaining({ kind: "set_mcp_enabled", target: "grep", decision: "false", sessionId: "session-1" }));
		expect(view.host.querySelector('button[aria-label="删除 grep"]')).not.toBeNull();
  });

	it("confirms and deletes a user-owned MCP service", async () => {
		useRuntimeStore.setState({
			mcpServers: [{
				name: "local-review", removable: true, enabled: false, state: "disabled", transport: "stdio", target: "npx review",
				command: "npx", args: ["review"], inheritEnv: true, approval: "always", maxConcurrency: 1, toolCount: 0, tools: [], error: "",
			}],
			skills: [], plugins: [],
		});
		const executeAction = vi.fn(async (_request: ActionRequest) => undefined);
		const view = renderSettings(executeAction);
		await view.render();

		const remove = view.host.querySelector<HTMLButtonElement>('button[aria-label="删除 local-review"]');
		expect(remove).not.toBeNull();
		await act(async () => remove!.click());
		const dialog = view.host.querySelector<HTMLElement>('[role="alertdialog"]');
		expect(dialog?.textContent).toContain("删除 local-review？");
		const confirm = Array.from(dialog!.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "删除服务");
		await act(async () => confirm!.click());

		expect(executeAction).toHaveBeenCalledWith({ kind: "delete_mcp_server", target: "local-review", sessionId: "session-1" });
		expect(view.host.querySelector('[role="alertdialog"]')).toBeNull();
	});

  it("opens the add drawer and submits a validated local server", async () => {
    useRuntimeStore.setState({ mcpServers: [], skills: [], plugins: [] });
    const executeAction = vi.fn(async (_request: ActionRequest) => undefined);
    const view = renderSettings(executeAction);
    await view.render(0);
    await view.render(1);

    const name = view.host.querySelector<HTMLInputElement>('input[placeholder="local-tools"]')!;
    const command = view.host.querySelector<HTMLInputElement>('input[placeholder="例如 npx"]')!;
    await setInput(name, "local-review");
    await setInput(command, "npx");
    const form = view.host.querySelector<HTMLFormElement>(".mcp-drawer form")!;
    await act(async () => form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));

    expect(executeAction).toHaveBeenCalledWith(expect.objectContaining({
      kind: "upsert_mcp_server", sessionId: "session-1",
      payload: expect.objectContaining({ name: "local-review", transport: "stdio", command: "npx", enabled: true }),
    }));
  });

  it("explains Azem-owned plugin copies and distinguishes imported and local packages", async () => {
    const base = {
      version: "1.0.0", description: "Plugin", developerName: "Azem", category: "Developer Tools",
      brandColor: "", logoPath: "", enabled: true, skillCount: 1, mcpServerCount: 0,
      integratedMCPCount: 0, hookCount: 0, hooksTrusted: false, hasApp: false,
      capabilities: ["Skills"], status: "ready", warning: "",
    };
    useRuntimeStore.setState({
      mcpServers: [], skills: [], plugins: [
		{ ...base, id: "review@market", name: "review", displayName: "Review", marketplace: "market", origin: "codex", logoPath: "data:image/png;base64,aWNvbg==" },
		{ ...base, id: "local@local", name: "local", displayName: "Local", marketplace: "local", origin: "local" },
		{ ...base, id: "optional@market", name: "optional", displayName: "Optional", marketplace: "market", origin: "codex_available", enabled: false, status: "available" },
      ],
    });
		const executeAction = vi.fn(async (_request: ActionRequest) => undefined);
		const view = renderSettings(executeAction);
    await view.render();
    const pluginsTab = Array.from(view.host.querySelectorAll<HTMLButtonElement>('button[role="tab"]'))
      .find((button) => button.textContent?.includes("插件"));
    await act(async () => pluginsTab!.click());

    expect(view.host.textContent).toContain("插件可直接安装到 Azem 的 plugin-packages/local");
    expect(view.host.textContent).toContain("复制到 Azem 目录，不会直接读取 Codex 目录");
    expect(view.host.textContent).toContain("从 Codex 导入 · market · 1.0.0");
		expect(view.host.textContent).toContain("Azem 目录 · 1.0.0");
		expect(view.host.textContent).not.toContain("Codex 插件");
		expect(view.host.querySelector('img[src^="data:image/png;base64,"]')).not.toBeNull();
		const importButton = Array.from(view.host.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "导入到 Azem");
		await act(async () => importButton!.click());
		expect(executeAction).toHaveBeenCalledWith({ kind: "set_plugin_imported", target: "optional@market", decision: "true", sessionId: "session-1" });
  });
});

async function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
