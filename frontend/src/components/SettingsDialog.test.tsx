import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute } from "../bridge";
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

vi.mock("../bridge", () => ({ execute: vi.fn(() => Promise.resolve()) }));

const snapshot: Snapshot = {
  workspace: "/workspace/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "auto_review",
  queueMode: "queue", subagentConcurrency: 4, chatgptFastMode: false, sequence: 0,
};

describe("SettingsDialog", () => {
  afterEach(() => vi.clearAllMocks());

  it("loads role routes and saves a subagent model selection", async () => {
    useRuntimeStore.setState({
      snapshot,
      approvalMode: snapshot.approvalMode,
      modelRoutes: [
        { Scope: "plan", Role: "", Label: "Plan", Route: {} },
        { Scope: "subagent", Role: "explore", Label: "Explore", Route: {} },
      ],
      agentCatalog: [{ name: "explore", description: "只读探索代码库", model: "", reasoning: "", capabilityMode: "read-only", isolation: "none", source: "builtin", enabled: true }],
      modelsByProvider: {
        chatgpt: [
          { id: "gpt-5.6-sol", name: "5.6 Sol", reasoningLevels: ["medium", "high"], defaultReasoning: "high" },
          { id: "gpt-5.6-luna", name: "5.6 Luna", reasoningLevels: ["low", "medium"], defaultReasoning: "medium" },
        ],
      },
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
    expect(container.querySelectorAll(".route-row")).toHaveLength(2);

    vi.mocked(execute).mockClear();
    const explore = Array.from(container.querySelectorAll<HTMLElement>(".route-row")).find((row) => row.textContent?.includes("explore"))!;
    const modelMenu = explore.querySelector<HTMLDetailsElement>(".route-model-menu")!;
    modelMenu.open = true;
    await act(async () => modelMenu.dispatchEvent(new Event("toggle", { bubbles: true })));
    await act(async () => container.querySelector<HTMLButtonElement>('.menu-select-options-portal [data-value="gpt-5.6-luna"]')!.click());
    await act(async () => explore.querySelector<HTMLButtonElement>(".route-actions .small-button")!.click());

    expect(execute).toHaveBeenCalledWith({
      kind: "set_model_route", target: "", sessionId: "session-1",
      route: { Scope: "subagent", Role: "explore", Label: "Explore", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "medium" } },
    });
    await act(async () => root.unmount());
    container.remove();
  });
});
