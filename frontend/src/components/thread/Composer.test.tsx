import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute, openProject, selectProjectFolder } from "../../bridge";
import { useRuntimeStore, type RuntimeData } from "../../store";
import type { Snapshot } from "../../types";
import { Composer, composerContextUsage } from "./Composer";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => undefined });

vi.mock("../../bridge", () => ({
  execute: vi.fn(() => Promise.resolve()),
  openProject: vi.fn(() => Promise.resolve()),
  selectProjectFolder: vi.fn(() => Promise.resolve()),
}));

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "s1", provider: "chatgpt", model: "gpt-5.6-sol",
  reasoning: "high", agentMode: "single", language: "zh-CN", approvalMode: "prompt",
  queueMode: "queue", subagentConcurrency: 2, chatgptFastMode: false, sequence: 0,
};

function state(): RuntimeData {
  return {
    snapshot, sessions: [], projects: [], currentSessionId: "s1", currentTitle: "", blocks: [], agents: [], backgroundProcesses: [], selectedAgentId: "", agentBlocks: [], agentCatalog: [],
    skills: [], mcpServers: [], plugins: [], marketplaceCatalog: { marketplaces: [], available: [], installed: [], upgrades: [] }, extensionThemes: [], hookCatalog: { enabled: true, trustHooks: false, sources: [], commands: [], diagnostics: [] }, usageReport: null, branches: [], pullRequestDashboard: null, selectedPullRequestNumber: null, pullRequestDetail: null,
    pullRequestMonitors: new Map(), pullRequestLoading: false, pullRequestMutating: false, pullRequestError: "",
    modelRoutes: [], modelProviders: [], modelsByProvider: {}, contextProfile: null,
    contextUsage: { inputTokens: 0, outputTokens: 0, contextLimit: 0, reported: false }, todo: null, recap: null,
    securityScans: [], securityConfig: null, securityScansLoaded: false, securityProjection: null, securityProjections: {}, securityFindings: [], securityFindingsByScan: {}, selectedSecurityFinding: null, securityPatch: null, securityExportPath: "", securityPublication: null, recovery: [],
    runId: "", running: false, globalRunId: "", globalRunSessionId: "", runStartedAt: 0, activity: "", approvalMode: "prompt", workspaceDirty: false,
    workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0,
    lastSequence: 0, error: "", view: "thread",
    settingsOpen: false, settingsTarget: null, commandOpen: false, sessionSearchTarget: null, planMode: false, attachments: [], queuedPrompts: [], queuePauseReasons: {}, theme: "system", uiFont: "system", uiFontSize: 14, chatFontSize: 13, chatCodeFontSize: 12,
  };
}

async function renderComposer(submit: (modeOverride?: string) => void) {
  useRuntimeStore.setState(state());
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<Composer
    prompt="hello" setPrompt={() => undefined} submit={submit} attach={() => undefined} attachClipboard={() => undefined}
    agentMode="single" setAgentMode={() => undefined} planMode={false} setPlanMode={() => undefined}
    running={false} busy={false} deliveryMode="queue"
  />));
  return root;
}

function pressEnter(timeStamp: number, isComposing = false) {
  const textarea = document.querySelector<HTMLTextAreaElement>("#azem-composer")!;
  const event = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
  Object.defineProperty(event, "timeStamp", { value: timeStamp });
  Object.defineProperty(event, "isComposing", { value: isComposing });
  act(() => textarea.dispatchEvent(event));
}

describe("Composer IME Enter guard", () => {
  afterEach(() => {
    vi.clearAllMocks();
    document.body.replaceChildren();
  });

  it("sends on a plain Enter outside composition", async () => {
    const submit = vi.fn();
    await renderComposer(submit);
    expect(document.querySelector('[data-slot="composer"]')).not.toBeNull();
    expect(document.querySelector(".composer-card")?.getAttribute("data-slot")).toBe("composer-bar");
    expect(document.querySelector("#azem-composer")?.getAttribute("data-slot")).toBe("composer-input");
    expect(document.querySelector(".composer-toolbar")?.getAttribute("data-slot")).toBe("composer-toolbar");
    expect(document.querySelector('[data-slot="composer-attach"]')).not.toBeNull();
    expect(document.querySelector('[data-slot="composer-context"]')).not.toBeNull();
    expect(document.querySelector('[data-slot="composer-send"]')).not.toBeNull();
    pressEnter(2000);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("maps actual context categories into assistant-ui ComposerContext", () => {
    expect(composerContextUsage({
      inputTokens: 8_500, outputTokens: 1_500, contextLimit: 20_000, reported: true,
    }, {
      source: "request", estimated: false, reportedInputTokens: 8_500, reportedOutputTokens: 1_500,
      contributions: [
        { category: "core", name: "instructions", tokens: 4_000 },
        { category: "builtin_tools", name: "coding.read_file", tokens: 2_000 },
        { category: "conversation", name: "message:user:1", tokens: 2_500 },
      ],
    }, "zh-CN")).toEqual({
      segments: [
        { key: "core", label: "核心指令", tokens: 4_000, tone: "primary" },
        { key: "conversation", label: "会话消息", tokens: 2_500, tone: "tertiary" },
        { key: "builtin_tools", label: "内置工具", tokens: 2_000, tone: "tertiary" },
        { key: "current_output", label: "当前输出", tokens: 1_500, tone: "secondary" },
      ],
      total: 20_000,
    });
  });

  it("ignores the composing Enter (Chromium order)", async () => {
    const submit = vi.fn();
    await renderComposer(submit);
    pressEnter(1000, true);
    expect(submit).not.toHaveBeenCalled();
  });

  it("ignores the commit Enter right after compositionend (WebKit/WKWebView order)", async () => {
    const submit = vi.fn();
    await renderComposer(submit);
    const textarea = document.querySelector<HTMLTextAreaElement>("#azem-composer")!;
    const end = new CompositionEvent("compositionend", { bubbles: true });
    Object.defineProperty(end, "timeStamp", { value: 1000 });
    act(() => textarea.dispatchEvent(end));
    pressEnter(1050);
    expect(submit).not.toHaveBeenCalled();
  });
});
