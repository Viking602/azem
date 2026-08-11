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
        name: "grep", enabled: true, state: "ready", transport: "streamable_http", target: "https://mcp.grep.app",
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
    const toggle = view.host.querySelector<HTMLButtonElement>('button[role="switch"][aria-label="停用 grep"]');
    expect(toggle).not.toBeNull();
    await act(async () => toggle!.click());
    expect(executeAction).toHaveBeenCalledWith(expect.objectContaining({ kind: "set_mcp_enabled", target: "grep", decision: "false", sessionId: "session-1" }));
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
});

async function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
