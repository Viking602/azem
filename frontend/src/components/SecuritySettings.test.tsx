import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute } from "../bridge";
import { useRuntimeStore } from "../store";
import type { SecurityConfig } from "../types";
import SecuritySettings from "./SecuritySettings";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({ execute: vi.fn(() => Promise.resolve()) }));

const securityConfig: SecurityConfig = {
  enabled: true,
  defaultMode: "standard",
  workers: 4,
  subagents: 3,
  stopAfterNoNew: 4,
  stopAfterConsecutiveErrors: 3,
  maxDiscoveryRuns: 40,
  maxTimeHours: 96,
  publicationTool: "mcp__linear__create_issue",
  routes: { audit: {}, reducer: {}, fixer: {}, verifier: {} },
};

async function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("SecuritySettings", () => {
  let root: Root | undefined;
  let container: HTMLDivElement | undefined;

  afterEach(async () => {
    await act(async () => root?.unmount());
    container?.remove();
    vi.clearAllMocks();
  });

  it("saves scan policy without exposing usage ceilings", async () => {
    useRuntimeStore.setState({
      securityConfig,
      mcpServers: [{
        name: "linear", removable: true, enabled: true, state: "ready", transport: "stdio", target: "linear",
        args: [], inheritEnv: false, approval: "always", maxConcurrency: 1, toolCount: 1,
        tools: [{ name: "mcp__linear__create_issue", description: "Create issue", effect: "external_side_effect", requiresApproval: true }], error: "",
      }],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root!.render(<SecuritySettings language="zh-CN" sessionId="session-1" onError={vi.fn()} onDirtyChange={vi.fn()} />));

    expect(execute).toHaveBeenCalledWith({ kind: "get_security_config", sessionId: "session-1" });
    expect(container.textContent).toContain("不设置 Token 或工具调用硬上限");
    expect(container.textContent).toContain("mcp__linear__create_issue");
    expect(container.querySelector(".security-publication-locked")).not.toBeNull();
    expect(container.querySelector(".security-publication-grid")).toBeNull();

    const workersLabel = [...container.querySelectorAll<HTMLLabelElement>(".security-number-field")]
      .find((label) => label.textContent?.includes("深度扫描 Worker"));
    await setInput(workersLabel!.querySelector("input")!, "6");
    const save = [...container.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent?.includes("保存安全扫描设置"));
    await act(async () => {
      save?.click();
      await Promise.resolve();
    });

    expect(execute).toHaveBeenCalledWith({
      kind: "set_security_config",
      sessionId: "session-1",
      payload: {
        enabled: true,
        defaultMode: "standard",
        workers: 6,
        subagents: 3,
        stopAfterNoNew: 4,
        stopAfterConsecutiveErrors: 3,
        maxDiscoveryRuns: 40,
        maxTimeHours: 96,
      },
    });
  });
});
