import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import * as bridge from "./bridge";
import { findModelOption, mergeSessionTranscript, modelDisplayName, providerDisplayName, reduceEvents, reorderSessionQueue, shouldMarkSessionUnread, type RuntimeData, useRuntimeStore } from "./store";
import type { Session, Snapshot } from "./types";
import ThreadSurface from "./components/ThreadSurface";
import { ComposerContext } from "./components/elements/composer";
import { composerContextUsage } from "./components/thread/Composer";
import { approvalPresentation, TimelineBlock } from "./components/Timeline";
import { formatDuration } from "./components/toolTimeline";
import { contextOccupancy } from "./contextUsage";
import { toolDisplayName } from "./i18n";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: () => undefined });
Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => undefined });

async function enterComposerText(textarea: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(textarea, value);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

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

describe("runtime event projection", () => {
  it("projects the complete Desktop security configuration", () => {
    const securityConfig = {
      enabled: true, defaultMode: "standard" as const, workers: 4, subagents: 3,
      stopAfterNoNew: 4, stopAfterConsecutiveErrors: 3, maxDiscoveryRuns: 40,
      maxTimeHours: 96,
      publicationTool: "mcp__linear__create_issue",
      routes: { audit: {}, reducer: {}, fixer: {}, verifier: {} },
    };
    const projected = reduceEvents(state(), [{
      sequence: 1, kind: "security_config_state", state: "loaded", securityConfig,
    }]);
    expect(projected.securityConfig).toEqual(securityConfig);
  });
  it("hydrates demo Security settings without a Wails event", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().hydrate(snapshot, true);
    expect(useRuntimeStore.getState().securityConfig).toMatchObject({
      enabled: true, workers: 4, maxTimeHours: 96,
    });
    expect(useRuntimeStore.getState().modelRoutes.filter((route) => route.scope === "security")).toHaveLength(4);
  });
  it("projects interactive planning questions and versioned plan review state", () => {
    const questions = JSON.stringify([{
      id: "scope", header: "范围", question: "选择范围",
      options: [{ label: "最小", description: "仅目标路径" }, { label: "完整", description: "包含配套验证" }],
    }]);
    const requested = reduceEvents(state(), [{
      sequence: 1, kind: "user_input_requested", sessionId: "s1", runId: "run-plan",
      userInputId: "ask-1", state: "pending", data: { questions },
    }]);
    expect(requested.blocks[0]).toMatchObject({ kind: "question", userInputId: "ask-1", state: "pending" });
    expect(requested.activity).toBe("input");

    const answered = reduceEvents(requested, [{
      sequence: 2, kind: "user_input_resolved", sessionId: "s1", userInputId: "ask-1",
      state: "answered", data: { answers: "[]" },
    }]);
    expect(answered.blocks[0]).toMatchObject({ state: "answered", data: { answers: "[]" } });

    const first = reduceEvents(answered, [{
      sequence: 3, kind: "plan_proposed", sessionId: "s1", runId: "run-plan", planId: "plan-1",
      state: "proposed", text: "First body", data: { title: "First", version: "1" },
    }]);
    const revised = reduceEvents(first, [{
      sequence: 4, kind: "plan_proposed", sessionId: "s1", runId: "run-plan-2", planId: "plan-2",
      state: "proposed", text: "Revised body", data: { title: "Revised", version: "2" },
    }]);
    expect(revised.planMode).toBe(true);
    expect(revised.blocks.filter((block) => block.kind === "plan").map((block) => block.state)).toEqual(["superseded", "proposed"]);

    const executing = reduceEvents(revised, [{
      sequence: 5, kind: "plan_resolved", sessionId: "s1", planId: "plan-2", state: "executing",
    }]);
    expect(executing.planMode).toBe(false);
    expect(executing.blocks.find((block) => block.planId === "plan-2")?.state).toBe("approved");
  });

  it("restores plan mode from the latest durable proposal", () => {
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: {
        provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
        blocks: JSON.stringify([{ id: "p1", kind: "plan", state: "proposed", title: "Plan", content: "Body", data: { planId: "artifact-1", version: "1" } }]),
        blockSequences: "[1]", toolRecords: "[]",
      },
    }]);
    expect(restored.planMode).toBe(true);
    expect(restored.blocks[0]).toMatchObject({ kind: "plan", planId: "artifact-1", state: "proposed" });
  });

  it("restores and updates the current session recap without leaking foreign session events", () => {
    const persisted = {
      sessionId: "s1", anchor: "/tmp/azem", coveredBoundary: "run-1", revision: 1,
      goal: "补齐回顾", summary: "已恢复持久化回顾。", openItems: "pending: 验证更新", updatedAt: "2026-08-12T00:00:00Z",
    };
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded", recap: persisted,
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]", blockSequences: "[]", toolRecords: "[]" },
    }]);
    expect(restored.recap).toEqual(persisted);

    const foreign = reduceEvents(restored, [{
      sequence: 2, kind: "recap_state", sessionId: "s2", state: "updated",
      recap: { ...persisted, sessionId: "s2", summary: "其他会话", revision: 2 },
    }]);
    expect(foreign.recap).toEqual(persisted);

    const updated = reduceEvents(foreign, [{
      sequence: 3, kind: "recap_state", sessionId: "s1", state: "updated",
      recap: { ...persisted, summary: "当前会话已实时更新。", revision: 2 },
    }]);
    expect(updated.recap).toMatchObject({ summary: "当前会话已实时更新。", revision: 2 });
  });

  it("restores persisted attachment MIME metadata after reopening a session", () => {
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: {
        provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
        blocks: JSON.stringify([{
          kind: "user", content: "检查截图",
          attachments: [{ id: "image-1", name: "screen.png", mime: "image/png", path: "/tmp/screen.png", size: 42 }],
        }]),
        blockSequences: "[1]", toolRecords: "[]",
      },
    }]);

    expect(restored.blocks[0]?.attachments).toEqual([{
      id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42,
    }]);
  });

	it("projects the plugin catalog and capability counts", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "plugin_catalog", pluginCatalog: [{
				id: "demo@market", name: "demo", displayName: "Demo", version: "1.0.0", marketplace: "market", origin: "codex",
				enabled: true, skillCount: 2, mcpServerCount: 2, integratedMCPCount: 1,
				hookCount: 1, hooksTrusted: false, hasApp: true, capabilities: ["Read"], status: "degraded",
			}],
		}]);
		expect(projected.plugins[0]).toMatchObject({ id: "demo@market", displayName: "Demo", origin: "codex", skillCount: 2, integratedMCPCount: 1, hasApp: true });
	});

	it("projects marketplace inventory, installs, and upgrades as one catalog", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1,
			kind: "marketplace_catalog",
			marketplaceCatalog: {
				marketplaces: [{ name: "official", source: "owner/repo", type: "github", cachePath: "/cache", updatedAt: "2026-08-23T00:00:00Z" }],
				available: [{ id: "demo@official", name: "demo", marketplace: "official", version: "2.0.0", description: "Demo", category: "development", homepage: "", license: "MIT", keywords: [], tags: [] }],
				installed: [{ id: "demo@official", name: "demo", marketplace: "official", version: "1.0.0", scope: "project", enabled: true, path: "/plugins/demo" }],
				upgrades: [{ plugin: { id: "demo@official", name: "demo", marketplace: "official", version: "1.0.0", scope: "project", enabled: true, path: "/plugins/demo" }, current: "1.0.0", latest: "2.0.0" }],
			},
		}]);
		expect(projected.marketplaceCatalog.marketplaces[0]?.name).toBe("official");
		expect(projected.marketplaceCatalog.installed[0]).toMatchObject({ id: "demo@official", scope: "project", enabled: true });
		expect(projected.marketplaceCatalog.upgrades[0]?.latest).toBe("2.0.0");
	});

	it("composes a Codex plugin id from name and marketplace when the wire omits id", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "plugin_catalog", pluginCatalog: [{
				name: "kami", displayName: "kami", version: "1.12.0", marketplace: "kami", origin: "codex_available",
				enabled: false, status: "available",
			}],
		}]);
		expect(projected.plugins[0]?.id).toBe("kami@kami");
	});

	it("projects MCP snapshots and live connection transitions", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "mcp_state", state: "snapshot", data: { servers: JSON.stringify([{
				name: "grep", enabled: true, state: "ready", transport: "streamable_http", target: "https://mcp.grep.app",
				approval: "never", maxConcurrency: 2, toolCount: 1, tools: [{ name: "searchGitHub", effect: "read_only" }],
				resourceCount: 1, resources: [{ server: "grep", uri: "file:///guide.md", name: "Guide" }],
				resourceTemplateCount: 1, resourceTemplates: [{ server: "grep", uriTemplate: "file:///{name}.md", name: "Markdown" }],
				promptCount: 1, prompts: [{ server: "grep", name: "summarize", arguments: [{ name: "text", required: true }] }], error: "",
			}]) },
		}, {
			sequence: 2, kind: "mcp_state", state: "degraded", text: "offline", data: { server: "grep", state: "degraded", error: "offline" },
		}, {
			sequence: 3, kind: "mcp_state", state: "notification", data: { server: "grep", notification: "resources/updated", uri: "file:///guide.md" },
		}]);
		expect(projected.mcpServers[0]).toMatchObject({
			name: "grep", state: "degraded", toolCount: 1, resourceCount: 1, resourceTemplateCount: 1, promptCount: 1, error: "offline",
			resources: [{ server: "grep", uri: "file:///guide.md", name: "Guide" }],
			resourceTemplates: [{ server: "grep", uriTemplate: "file:///{name}.md", name: "Markdown" }],
			prompts: [{ server: "grep", name: "summarize", arguments: [{ name: "text", required: true }] }],
		});
	});

	it("projects extension theme catalogs", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "theme_catalog", state: "snapshot",
			data: { themes: JSON.stringify([{ name: "custom-dark", path: "/theme.json", colors: { accent: "#fff" }, source: "user" }]) },
		}]);
		expect(projected.extensionThemes).toEqual([{ name: "custom-dark", path: "/theme.json", colors: { accent: "#fff" }, source: "user" }]);
	});

	it("projects configured provider reasoning levels and resolves model aliases", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "model_providers", modelProviders: [{
				id: "deepseek", displayName: "DeepSeek", backend: "anthropic",
				defaultBaseUrl: "https://api.deepseek.com/anthropic", baseUrl: "", envKey: "DEEPSEEK_API_KEY",
				enabled: true, credentialConfigured: true, credentialSource: "stored",
				models: [{ id: "deepseek-v4-flash", aliases: ["deepseek/deepseek-v4-flash"], contextWindow: 1_000_000, reasoningLevels: ["low", "high", "max"], defaultReasoning: "max" }],
			}],
		}]);
		const models = projected.modelsByProvider.deepseek ?? [];
		expect(findModelOption(models, "deepseek/deepseek-v4-flash")?.reasoningLevels).toEqual(["low", "high", "max"]);
	});

	it("projects llmux provider settings without exposing a secret field", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "model_providers", modelProviders: [{
				id: "openrouter", displayName: "OpenRouter", backend: "openai_compat",
				defaultBaseUrl: "https://openrouter.ai/api/v1", baseUrl: "", envKey: "OPENROUTER_API_KEY",
				enabled: true, credentialConfigured: true, credentialSource: "stored",
				models: [{ id: "openai/gpt-test", contextWindow: 128000 }],
			}],
		}]);
		expect(projected.modelProviders[0]).toMatchObject({ id: "openrouter", credentialSource: "stored" });
		expect(projected.modelProviders[0]).not.toHaveProperty("secret");
	});

	it("does not let subscription provider snapshots wipe an enriched Cursor catalog", () => {
		const projected = reduceEvents(state(), [{
			sequence: 1, kind: "model_catalog", data: {
				provider: "cursor",
				models: JSON.stringify([{
					id: "claude-4.5-sonnet", name: "Claude Sonnet 4.5",
					supportsTools: true, supportsReasoning: true,
					inputModalities: ["text", "image"], outputModalities: ["text"],
				}]),
			},
		}, {
			sequence: 2, kind: "model_providers", modelProviders: [{
				id: "cursor", displayName: "Cursor 订阅", backend: "subscription", subscription: true,
				defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true,
				credentialConfigured: true, credentialSource: "stored",
				models: [{ id: "claude-4.5-sonnet", name: "Claude Sonnet 4.5", contextWindow: 200000, capabilities: ["tools", "reasoning"] }],
			}],
		}]);
		expect(projected.modelsByProvider.cursor?.[0]?.inputModalities).toEqual(["text", "image"]);
		expect(projected.modelsByProvider.cursor?.[0]?.capabilities).toEqual(expect.arrayContaining(["tools", "reasoning"]));
	});

  it("keeps bootstrap events emitted before initialise returns", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().hydrate({ ...snapshot, sequence: 6 });
    useRuntimeStore.getState().applyEvents([{
      sequence: 4,
      kind: "model_routes",
      modelRoutes: [{ scope: "plan", role: "", label: "Plan", route: {} }],
    }]);
    expect(useRuntimeStore.getState().modelRoutes).toHaveLength(1);
  });

  it("applies a late usage report even after later sequences have been seen", () => {
    const projected = reduceEvents(state(), [
      { sequence: 8, kind: "plugin_catalog", pluginCatalog: [] },
      {
        sequence: 3, kind: "usage_report",
        usageReport: {
          scope: "project", from: "2025-08-14", to: "2026-08-14", empty: false,
          requests: 2, sessions: 1, runs: 1, totalTokens: 40, inputTokens: 30, outputTokens: 10,
          reasoningTokens: 0, reportedInputTokens: 30, cacheReadTokens: 12, cacheWriteTokens: 0,
          cacheReported: true, cacheWriteReported: false, peakDayTokens: 40, currentStreak: 1, longestStreak: 1,
          days: [{ date: "2026-08-14", tokens: 40, requests: 2 }],
          kinds: [{ kind: "main", tokens: 40, requests: 2 }],
          models: [{ provider: "chatgpt", model: "gpt-5.6", tokens: 40, inputTokens: 30, outputTokens: 10, cacheReadTokens: 12, cacheWriteTokens: 0, cacheReported: true, cacheWriteReported: false, requests: 2 }],
          skills: [],
        },
      },
    ]);
    expect(projected.usageReport?.totalTokens).toBe(40);
    expect(projected.usageReport?.models[0]).toMatchObject({ provider: "chatgpt", cacheReported: true });
    expect(JSON.stringify(projected.usageReport)).not.toMatch(/secret|apiKey|api_key/i);
  });

  it("applies a late hook catalog even after later sequences have been seen", () => {
    const projected = reduceEvents(state(), [
      { sequence: 8, kind: "plugin_catalog", pluginCatalog: [] },
      {
        sequence: 3, kind: "hook_catalog",
        hookCatalog: {
          enabled: true, trustHooks: true,
          sources: [{ id: "demo@local", name: "Demo", origin: "plugin", hookCount: 1, trusted: true }],
          commands: [{ id: "hook-1", name: "notify", event: "SessionStart", enabled: true }],
        },
      },
    ]);
    expect(projected.hookCatalog.trustHooks).toBe(true);
    expect(projected.hookCatalog.commands[0]).toMatchObject({ id: "hook-1", name: "notify", enabled: true });
  });

  it("shows the snapshot branch before the full git branch event arrives", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().hydrate({ ...snapshot, currentBranch: "main" });
    expect(useRuntimeStore.getState().branches).toEqual([{ name: "main", current: true }]);

    useRuntimeStore.getState().applyEvents([{
      sequence: 1,
      kind: "git_branches",
      gitBranches: [{ name: "feature", current: true }, { name: "main", current: false }],
    }]);
    useRuntimeStore.getState().hydrate({ ...snapshot, currentBranch: "main" });
    expect(useRuntimeStore.getState().branches).toEqual([{ name: "feature", current: true }, { name: "main", current: false }]);
  });

  it("projects a first-turn session immediately and replaces its generated title", () => {
    useRuntimeStore.setState(state());
    useRuntimeStore.getState().addOptimisticUser("修复会话标题", []);
    expect(useRuntimeStore.getState().sessions[0]).toMatchObject({ id: "s1", title: "新对话" });

    useRuntimeStore.getState().applyEvents([{
      sequence: 1, kind: "session_loaded", state: "list", data: { sessions: "[]" },
    }]);
    expect(useRuntimeStore.getState().sessions[0]).toMatchObject({ id: "s1", title: "新对话" });

    useRuntimeStore.getState().applyEvents([{
      sequence: 2,
      kind: "session_loaded",
      state: "list",
      data: {
        sessions: JSON.stringify([{
          id: "s1", title: "修复会话标题", providerId: "chatgpt", modelId: "gpt-5.6-sol",
          reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
        }]),
      },
    }]);
    expect(useRuntimeStore.getState().sessions[0]?.title).toBe("修复会话标题");
    expect(useRuntimeStore.getState().currentTitle).toBe("修复会话标题");
  });

  it("isolates background automation events from the visible session", () => {
    const current: RuntimeData = { ...state(), running: true, runId: "visible-run", blocks: [{ id: "tool-1", kind: "tool", state: "running", content: "", title: "visible tool" }] };
    const projected = reduceEvents(current, [
      { sequence: 1, kind: "run_started", sessionId: "repair-session", runId: "repair-run" },
      { sequence: 2, kind: "text_delta", sessionId: "repair-session", runId: "repair-run", text: "background output" },
      { sequence: 3, kind: "run_finished", sessionId: "repair-session", runId: "repair-run" },
    ]);
    expect(projected.currentSessionId).toBe("s1");
    expect(projected.runId).toBe("visible-run");
    expect(projected.running).toBe(true);
    expect(projected.blocks).toEqual(current.blocks);
    expect(projected.lastSequence).toBe(3);
  });

  it("shows tool arguments while running and settles unfinished tools when the run ends", () => {
    const started = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      {
        sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", state: "running",
        data: { name: "coding.read_file", arguments: JSON.stringify({ path: "frontend/src/App.tsx", limit: 40 }) },
      },
      {
        sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "search-1", state: "running",
        data: { name: "coding.search", arguments: JSON.stringify({ query: "MenuSelect", path: "frontend/src" }) },
      },
      {
        sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "read-1", state: "progress",
        data: { output: "phase 1\n", output_bytes: "8" },
      },
    ]);
    expect(started.blocks[0]).toMatchObject({
      kind: "tool", toolCallId: "read-1", state: "running", title: "coding.read_file",
    });
    expect(started.blocks[0]?.content).toContain("frontend/src/App.tsx");
    expect(started.blocks[1]?.content).toContain("MenuSelect");
    expect(started.blocks[0]).toMatchObject({ state: "running", data: { output: "phase 1\n", output_bytes: "8" } });

    const finished = reduceEvents(started, [
      { sequence: 5, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "export default function App" },
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);
    expect(finished.blocks.find((block) => block.toolCallId === "read-1")).toMatchObject({ state: "completed" });
    // Search never finished; run end must stop it without claiming execution succeeded.
    expect(finished.blocks.find((block) => block.toolCallId === "search-1")).toMatchObject({ state: "failed" });
    expect(finished.running).toBe(false);
  });

  it("keeps a distinct elapsed clock on each tool instead of the run duration", () => {
    const clock = vi.spyOn(Date, "now");
    try {
      clock.mockReturnValue(1_000_000);
      const started = reduceEvents(state(), [
        { sequence: 1, kind: "run_started", runId: "r1" },
        { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", state: "running", data: { name: "coding.read_file" } },
        { sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "list-1", state: "running", data: { name: "coding.list_files" } },
      ]);
      expect(started.blocks[0]?.data?.startedAt).toBe("1000000");
      expect(started.blocks[1]?.data?.startedAt).toBe("1000000");

      clock.mockReturnValue(1_002_400);
      const firstDone = reduceEvents(started, [
        { sequence: 4, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed" },
      ]);
      clock.mockReturnValue(1_008_000);
      const secondDone = reduceEvents(firstDone, [
        { sequence: 5, kind: "tool_finished", runId: "r1", toolCallId: "list-1", state: "completed" },
      ]);
      clock.mockReturnValue(1_104_000);
      const finished = reduceEvents(secondDone, [{ sequence: 6, kind: "run_finished", runId: "r1" }]);

      expect(finished.blocks.find((block) => block.toolCallId === "read-1")?.data).toMatchObject({
        startedAt: "1000000", elapsedMs: "2400",
      });
      expect(finished.blocks.find((block) => block.toolCallId === "list-1")?.data).toMatchObject({
        startedAt: "1000000", elapsedMs: "8000",
      });
      expect(finished.blocks.every((block) => block.data?.elapsedMs !== "104000")).toBe(true);
    } finally {
      clock.mockRestore();
    }
  });

  it("settles shell commands as soon as their finished update arrives", () => {
    const started = reduceEvents(state(), [
      { sequence: 1, kind: "tool_started", runId: "r1", toolCallId: "failed", state: "running", data: { name: "coding.shell" } },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "passed", state: "running", data: { name: "coding.shell" } },
    ]);
    const settled = reduceEvents(started, [
      { sequence: 3, kind: "tool_update", runId: "r1", toolCallId: "failed", state: "finished", data: { status: "exited", exit_code: "1", output: "HTTP 401" } },
      { sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "passed", state: "finished", data: { status: "exited", exit_code: "0", output: "ok" } },
    ]);

    expect(settled.blocks.find((block) => block.toolCallId === "failed")).toMatchObject({ state: "failed" });
    expect(settled.blocks.find((block) => block.toolCallId === "passed")).toMatchObject({ state: "completed" });
  });

	it("projects a run failure once inside the timeline", () => {
		const failed = reduceEvents({ ...state(), running: true, runId: "r1" }, [{
			sequence: 1, kind: "run_failed", sessionId: "s1", runId: "r1", text: "provider unavailable",
		}]);
		expect(failed.error).toBe("");
		expect(failed.blocks.filter((block) => block.kind === "error")).toHaveLength(1);
		expect(failed.blocks.at(-1)?.content).toBe("provider unavailable");
	});

	it("does not repeat a synchronously rejected turn below its failure card", () => {
		useRuntimeStore.setState(state());
		useRuntimeStore.getState().failRun("model unavailable");
		const failed = useRuntimeStore.getState();
		expect(failed.error).toBe("");
		expect(failed.blocks.filter((block) => block.kind === "error")).toHaveLength(1);
		expect(failed.blocks.at(-1)?.content).toBe("model unavailable");
	});

	it("titles a run failure from the stable provider error code", () => {
		const failed = reduceEvents({ ...state(), running: true, runId: "r1" }, [{
			sequence: 1, kind: "run_failed", sessionId: "s1", runId: "r1",
			text: "HTTP 401: token expired", data: { errorCode: "auth" },
		}]);
		const block = failed.blocks.at(-1);
		expect(block?.kind).toBe("error");
		expect(block?.title).toBe("认证失败");
		expect(block?.data?.errorCode).toBe("auth");
	});

  it("preserves queued and approval states until a tool actually runs", () => {
    const queued = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "write-1", state: "queued", data: { name: "coding.write_file" } },
      { sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "read-1", state: "queued", data: { name: "coding.read_file" } },
    ]);
    expect(queued.blocks.map((block) => block.state)).toEqual(["queued", "queued"]);

    const awaiting = reduceEvents(queued, [
      { sequence: 4, kind: "tool_update", runId: "r1", toolCallId: "write-1", state: "awaiting_approval" },
    ]);
    expect(awaiting.blocks.map((block) => block.state)).toEqual(["awaiting_approval", "queued"]);

    const running = reduceEvents(awaiting, [
      { sequence: 5, kind: "tool_update", runId: "r1", toolCallId: "write-1", state: "running" },
    ]);
    expect(running.blocks.map((block) => block.state)).toEqual(["running", "queued"]);

    const finished = reduceEvents(running, [
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);
    expect(finished.blocks.map((block) => block.state)).toEqual(["failed", "failed"]);
  });

  it("shows cumulative command output without resetting the open detail", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const block = {
      id: "shell-1", kind: "tool" as const, title: "coding.shell", state: "running",
      content: JSON.stringify({ command: "bun run dev", timeout_seconds: 30 }),
      data: { output: "ready\nrequest 1\n", output_bytes: "16" },
    };
    await act(async () => root.render(createElement(TimelineBlock, { block, language: "zh-CN" })));
    const details = container.querySelector("details")!;
    await act(async () => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    const log = container.querySelector<HTMLPreElement>(".tool-log")!;
    expect(log.textContent).toBe("ready\nrequest 1\n");
    expect(log.getAttribute("aria-live")).toBe("off");
    expect(log.getAttribute("tabindex")).toBe("0");

    await act(async () => root.render(createElement(TimelineBlock, {
      block: { ...block, data: { output: "ready\nrequest 1\nrequest 2\n", output_bytes: "26" } },
      language: "zh-CN",
    })));
    expect(details.open).toBe(true);
    expect(container.querySelector(".tool-log")?.textContent).toContain("request 2");
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps growing subagent tool and elapsed counters across sparse agent_state updates", () => {
    const projected = reduceEvents(state(), [
      {
        sequence: 1, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", parentRunId: "parent-run", parentToolCallId: "spawn-call", model: "gpt-5.6-luna", capabilityMode: "read-only", evidenceStatus: "provisional", toolCalls: 2, turns: 1, tokensUsed: 100, elapsedMs: 5000, activity: "coding.read_file" },
      },
      // Sparse lifecycle event without counters must not reset stats to zero.
      {
        sequence: 2, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", activity: "coding.search", evidenceStatus: "stale" },
      },
      {
        sequence: 3, kind: "agent_state", agentId: "a1", state: "running", text: "",
        agent: { type: "explore", toolCalls: 5, elapsedMs: 12000, activity: "coding.git_diff" },
      },
    ]);
    expect(projected.agents[0]).toMatchObject({
      id: "a1", state: "running", parentRunId: "parent-run", parentToolCallId: "spawn-call",
      toolCalls: 5, elapsedMs: 12000, activity: "coding.git_diff", model: "gpt-5.6-luna", evidenceStatus: "stale",
    });
  });

  it("streams subagent frames into the side chat instead of the main feed", () => {
    const base = { ...state(), selectedAgentId: "agent-1" };
    const projected = reduceEvents(base, [
      {
        sequence: 1, kind: "agent_state", agentId: "agent-1", state: "running", text: "",
        agent: { type: "explore", description: "检查变更", toolCalls: 0, turns: 0, tokensUsed: 0, elapsedMs: 0 },
      },
      { sequence: 2, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "先看 diff" },
      { sequence: 3, kind: "tool_started", runId: "child-1", agentId: "agent-1", toolCallId: "c1", data: { name: "coding.git_diff" }, state: "running" },
      { sequence: 4, kind: "tool_finished", runId: "child-1", agentId: "agent-1", toolCallId: "c1", text: "ok", state: "completed" },
      { sequence: 5, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "结论" },
      // Unrelated main-run frame still goes to the main transcript.
      { sequence: 6, kind: "thinking_delta", runId: "main-1", text: "主会话思考" },
    ]);
    expect(projected.blocks).toHaveLength(1);
    expect(projected.blocks[0]).toMatchObject({ kind: "thinking", content: "主会话思考" });
    expect(projected.agentBlocks.map((block) => block.kind)).toEqual(["thinking", "tool", "assistant"]);
    expect(projected.agentBlocks[0]).toMatchObject({ kind: "thinking", title: "正在思考", content: "先看 diff" });
    expect(projected.agentBlocks[1]).toMatchObject({ kind: "tool", title: "coding.git_diff", state: "completed" });
    expect(projected.agentBlocks[2]).toMatchObject({ kind: "assistant", content: "结论" });
    expect(projected.agents[0]).toMatchObject({ preview: "结论", previewKind: "assistant", previewRunId: "child-1" });

    const finished = reduceEvents(projected, [{
      sequence: 7, kind: "agent_state", agentId: "agent-1", state: "completed", text: "已检查全部变更",
      agent: { type: "explore", description: "检查变更", summary: "已检查全部变更" },
    }]);
    expect(finished.agents[0]).toMatchObject({ state: "completed", preview: "已检查全部变更", previewKind: "assistant" });
  });

  it("keeps the main transcript array identity across subagent text deltas", () => {
    const blocks = [{ id: "main-1", kind: "assistant" as const, content: "主会话" }];
    const base = { ...state(), selectedAgentId: "agent-1", blocks };
    const projected = reduceEvents(base, [
      { sequence: 1, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "侧栏增量" },
    ]);
    expect(projected.blocks).toBe(blocks);
    expect(projected.agentBlocks).not.toBe(base.agentBlocks);
    expect(projected.agentBlocks.at(-1)).toMatchObject({ kind: "assistant", content: "侧栏增量" });
  });

  it("ignores other agents' live frames while a different side chat is open", () => {
    const projected = reduceEvents({ ...state(), selectedAgentId: "agent-a" }, [
      { sequence: 1, kind: "thinking_delta", runId: "c", agentId: "agent-b", text: "不该出现" },
    ]);
    expect(projected.agentBlocks).toEqual([]);
    expect(projected.blocks).toEqual([]);
  });

  it("does not let a stale inspect snapshot wipe live subagent thinking", () => {
    const live = reduceEvents({ ...state(), selectedAgentId: "agent-1" }, [
      { sequence: 1, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "先看 diff" },
      { sequence: 2, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "再看测试" },
    ]);
    expect(live.agentBlocks[0]).toMatchObject({ kind: "thinking", content: "先看 diff再看测试" });

    const merged = reduceEvents(live, [{
      sequence: 3, kind: "agent_detail", agentId: "agent-1", state: "detail",
      agentBlocks: [{ id: live.agentBlocks[0]!.id, kind: "thinking", runId: "child-1", content: "先看 diff" }],
    }]);
    expect(merged.agentBlocks[0]).toMatchObject({ kind: "thinking", content: "先看 diff再看测试" });

    const extra = reduceEvents(live, [{
      sequence: 4, kind: "agent_detail", agentId: "agent-1", state: "detail",
      agentBlocks: [{ id: "msg-0-user", kind: "user", runId: "child-1", content: "审查变更" }],
    }]);
    expect(extra.agentBlocks.map((block) => block.content)).toEqual(["审查变更", "先看 diff再看测试"]);

    const stale = reduceEvents({ ...state(), selectedAgentId: "agent-b", agentBlocks: [] }, [{
      sequence: 5, kind: "agent_detail", agentId: "agent-1", state: "detail",
      agentBlocks: [{ id: "old", kind: "assistant", content: "不该出现" }],
    }]);
    expect(stale.selectedAgentId).toBe("agent-b");
    expect(stale.agentBlocks).toEqual([]);
  });

  it("keeps the open subagent drawer across a same-session projection refresh", () => {
    const live = reduceEvents({ ...state(), selectedAgentId: "agent-1" }, [
      { sequence: 1, kind: "thinking_delta", runId: "child-1", agentId: "agent-1", text: "先看 diff" },
    ]);
    const refreshed = reduceEvents(live, [{
      sequence: 2, kind: "session_loaded", sessionId: "s1", state: "refreshed",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]);
    expect(refreshed.selectedAgentId).toBe("agent-1");
    expect(refreshed.agentBlocks[0]).toMatchObject({ kind: "thinking", content: "先看 diff" });

    const merged = reduceEvents(refreshed, [{
      sequence: 3, kind: "agent_detail", agentId: "agent-1", state: "detail",
      agentBlocks: [{ id: refreshed.agentBlocks[0]!.id, kind: "thinking", runId: "child-1", content: "先看 diff" }],
    }]);
    expect(merged.agentBlocks[0]).toMatchObject({ kind: "thinking", content: "先看 diff" });

    const navigated = reduceEvents(refreshed, [{
      sequence: 4, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]);
    expect(navigated.selectedAgentId).toBe("");
    expect(navigated.agentBlocks).toEqual([]);
  });

  it("clears stale detail blocks only when switching subagents", () => {
    const detail = [{ id: "answer-1", kind: "assistant" as const, content: "detail" }];
    useRuntimeStore.setState({ ...state(), selectedAgentId: "agent-a", agentBlocks: detail });
    useRuntimeStore.getState().selectAgent("agent-a");
    expect(useRuntimeStore.getState().agentBlocks).toEqual(detail);
    useRuntimeStore.getState().selectAgent("agent-b");
    expect(useRuntimeStore.getState()).toMatchObject({ selectedAgentId: "agent-b", agentBlocks: [] });
  });


  it("separates discrete thinking blurbs instead of gluing **A****B**", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "**Planning analysis**" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "**Inspecting files**" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: " mid-sentence" },
    ]);
    expect(projected.blocks[0]?.content).toBe("**Planning analysis**\n\n**Inspecting files** mid-sentence");
  });

  it("keeps reasoning steps in event order when tools run between model updates", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "先读取文件。" },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      { sequence: 3, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "done" },
      { sequence: 4, kind: "thinking_delta", runId: "r1", text: "再检查结果。" },
      { sequence: 5, kind: "text_delta", runId: "r1", text: "完成。" },
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);
    expect(projected.blocks.map((block) => block.kind)).toEqual(["thinking", "tool", "thinking", "assistant"]);
    expect(projected.blocks.filter((block) => block.kind === "thinking").map((block) => block.content)).toEqual(["先读取文件。", "再检查结果。"]);
    expect(projected.blocks.filter((block) => block.kind === "thinking").every((block) => block.state === "completed")).toBe(true);
  });

  it("keeps commentary in the process trail before tools and final answers", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "thinking_delta", runId: "r1", text: "分析协议。" },
      { sequence: 2, kind: "text_delta", runId: "r1", text: "协议已确认，", textPhase: "commentary" },
      { sequence: 3, kind: "text_delta", runId: "r1", text: "接下来读取文件。", textPhase: "commentary" },
      { sequence: 4, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      { sequence: 5, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed", text: "done" },
      { sequence: 6, kind: "text_delta", runId: "r1", text: "检查完成。", textPhase: "final_answer" },
      { sequence: 7, kind: "run_finished", runId: "r1", state: "completed" },
    ]);

    expect(projected.blocks.map((block) => block.kind)).toEqual(["thinking", "commentary", "tool", "assistant"]);
    expect(projected.blocks[1]).toMatchObject({
      content: "协议已确认，接下来读取文件。",
      state: "completed",
      textPhase: "commentary",
    });
    expect(projected.blocks[3]).toMatchObject({
      content: "检查完成。",
      state: "completed",
      textPhase: "final_answer",
    });
  });

  it("keeps the host fallback synthetic marker on live commentary", () => {
    const projected = reduceEvents(state(), [
      {
        sequence: 1, kind: "text_delta", runId: "r1",
        text: "正在调用所需工具，并根据实际结果继续。",
        textPhase: "commentary",
        data: { synthetic: "tool_announcement" },
      },
      { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
    ]);

    expect(projected.blocks[0]).toMatchObject({
      kind: "commentary",
      content: "正在调用所需工具，并根据实际结果继续。",
      textPhase: "commentary",
      data: expect.objectContaining({ synthetic: "tool_announcement" }),
    });
    expect(projected.blocks[1]).toMatchObject({ kind: "tool" });
  });

  it("drops an uncommitted provider attempt before projecting its retry", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "第一次思考" },
      { sequence: 3, kind: "text_delta", runId: "r1", text: "第一次答案", textPhase: "final_answer" },
      { sequence: 4, kind: "provider_retry", runId: "r1", state: "restarted" },
      { sequence: 5, kind: "thinking_delta", runId: "r1", text: "恢复后思考" },
      { sequence: 6, kind: "text_delta", runId: "r1", text: "最终答案", textPhase: "final_answer" },
      { sequence: 7, kind: "run_finished", runId: "r1", state: "completed" },
    ]);

    expect(projected.blocks.map((block) => [block.kind, block.content])).toEqual([
      ["thinking", "恢复后思考"],
      ["assistant", "最终答案"],
    ]);

    const subagent = reduceEvents({
      ...state(),
      selectedAgentId: "agent-1",
      agentBlocks: [
        { id: "old-thinking", kind: "thinking", runId: "child-1", content: "第一次思考" },
        { id: "old-answer", kind: "assistant", runId: "child-1", content: "第一次答案" },
      ],
    }, [
      { sequence: 1, kind: "provider_retry", runId: "child-1", agentId: "agent-1", state: "restarted" },
      { sequence: 2, kind: "text_delta", runId: "child-1", agentId: "agent-1", text: "子智能体最终答案" },
    ]);
    expect(subagent.agentBlocks.map((block) => block.content)).toEqual(["子智能体最终答案"]);
  });

  it("timestamps a reasoning segment and settles it before tool work begins", () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_000);
    try {
      const streaming = reduceEvents(state(), [
        { sequence: 1, kind: "thinking_delta", runId: "r1", text: "检查实现" },
      ]);
      expect(streaming.blocks[0]).toMatchObject({
        kind: "thinking",
        state: "streaming",
        data: { startedAt: "1000" },
      });

      clock.mockReturnValue(2_500);
      const settled = reduceEvents(streaming, [
        { sequence: 2, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      ]);
      expect(settled.blocks[0]).toMatchObject({
        kind: "thinking",
        state: "completed",
        data: { startedAt: "1000", completedAt: "2500", elapsedMs: "1500" },
      });
      expect(settled.blocks[1]).toMatchObject({ kind: "tool", state: "running" });
    } finally {
      clock.mockRestore();
    }
  });

  it("moves tool-turn prose into the process trail and keeps the natural stop as the final answer", () => {
    const streaming = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "text_delta", runId: "r1", text: "先检查代码。", state: "streaming" },
    ]);
    expect(streaming.blocks[0]).toMatchObject({
      kind: "assistant",
      state: "streaming",
      content: "先检查代码。",
    });

    const projected = reduceEvents(streaming, [
      { sequence: 3, kind: "tool_started", runId: "r1", toolCallId: "read-1", data: { name: "coding.read_file" } },
      { sequence: 4, kind: "tool_finished", runId: "r1", toolCallId: "read-1", state: "completed" },
      { sequence: 5, kind: "text_delta", runId: "r1", text: "这是最终回答。", state: "streaming" },
      { sequence: 6, kind: "run_finished", runId: "r1" },
    ]);

    expect(projected.blocks.map(({ kind, state: blockState, content }) => ({ kind, state: blockState, content }))).toEqual([
      { kind: "commentary", state: "completed", content: "先检查代码。" },
      { kind: "tool", state: "completed", content: "" },
      { kind: "assistant", state: "completed", content: "这是最终回答。" },
    ]);
  });

  it("keeps an unphased natural-stop stream in one final-answer block", () => {
    const first = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "text_delta", runId: "r1", text: "最终", state: "streaming" },
    ]);
    expect(first.blocks).toHaveLength(1);
    expect(first.blocks[0]).toMatchObject({ kind: "assistant", state: "streaming", content: "最终" });
    const blockId = first.blocks[0]!.id;

    const completed = reduceEvents(first, [
      { sequence: 3, kind: "text_delta", runId: "r1", text: "正文", state: "streaming" },
      { sequence: 4, kind: "run_finished", runId: "r1" },
    ]);
    expect(completed.blocks).toHaveLength(1);
    expect(completed.blocks[0]).toMatchObject({
      id: blockId,
      kind: "assistant",
      state: "completed",
      content: "最终正文",
    });
  });

  it("coalesces streaming deltas and ignores replayed sequence numbers", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "先检查" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: "事件模型" },
      { sequence: 3, kind: "thinking_delta", runId: "r1", text: "重复" },
    ]);
    expect(projected.running).toBe(true);
    expect(projected.blocks).toHaveLength(1);
    expect(projected.blocks[0]?.content).toBe("先检查事件模型");
  });

  it("stops the thinking animation when the run finishes", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "run_started", runId: "r1" },
      { sequence: 2, kind: "thinking_delta", runId: "r1", text: "检查状态" },
      { sequence: 3, kind: "run_finished", runId: "r1" },
    ]);
    expect(projected.running).toBe(false);
    expect(projected.blocks[0]).toMatchObject({ kind: "thinking", runId: "r1", state: "completed" });
  });

  it("adds a timed Codex-style separator when the user stops a run", () => {
    const current: RuntimeData = {
      ...state(),
      running: true,
      runId: "r1",
      runStartedAt: Date.now() - 65_000,
      blocks: [{ id: "thinking", kind: "thinking", runId: "r1", content: "处理中", state: "streaming" }],
    };
    const projected = reduceEvents(current, [{ sequence: 1, kind: "run_cancelled", runId: "r1" }]);
    expect(projected.blocks.at(-1)).toMatchObject({ kind: "status", runId: "r1", title: "run_cancelled", state: "cancelled" });
    expect(Number(projected.blocks.at(-1)?.data?.elapsedMs)).toBeGreaterThanOrEqual(65_000);
  });

  it("restores structured edit sections from durable tool records", () => {
    const restored = mergeSessionTranscript(
      [{ id: "user", kind: "user", runId: "r1", content: "edit" }],
      [1],
      [{
        runId: "r1", toolCallId: "edit-1", name: "coding.edit_hashline", anchorSequence: 1, state: "completed",
        arguments: { input: "patch" },
        structured: { sections: [{ path: "src/app.ts", firstChangedLine: 3, diff: "-old\n+next" }] },
      }],
    );
    expect(JSON.parse(restored[1]?.data?.structured || "{}")).toMatchObject({
      sections: [{ path: "src/app.ts", firstChangedLine: 3, diff: "-old\n+next" }],
    });
    expect(restored[0]?.sequence).toBe(1);
  });

  it("keeps approval beside the tool timeline and resolves it in place", () => {
    const projected = reduceEvents(state(), [
      { sequence: 1, kind: "approval_requested", approvalId: "a1", toolCallId: "t1", text: "write file" },
      { sequence: 2, kind: "approval_resolved", approvalId: "a1", state: "approved" },
    ]);
    expect(projected.blocks[0]).toMatchObject({ kind: "approval", approvalId: "a1", state: "approved" });
  });

  it("hides automatic approval reviews and keeps the later user prompt", () => {
    const reviewed = reduceEvents(state(), [
      { sequence: 1, kind: "approval_requested", approvalId: "a1", state: "reviewing", text: "{\"command\":\"git status\"}" },
      { sequence: 2, kind: "approval_resolved", approvalId: "a1", state: "auto_approved" },
    ]);
    expect(reviewed.blocks).toHaveLength(0);
    const prompted = reduceEvents(reviewed, [{ sequence: 3, kind: "approval_requested", approvalId: "a1", state: "pending", data: { tool: "coding.shell", target: "git status", effect: "external_side_effect", risk: "high" } }]);
    expect(prompted.blocks[0]).toMatchObject({ kind: "approval", approvalId: "a1", state: "pending" });
  });

  it("moves a queued tool into reviewing instead of leaving it queued during auto-review", () => {
    const queued = reduceEvents(state(), [
      { sequence: 1, kind: "tool_started", runId: "r1", toolCallId: "t1", state: "queued", data: { name: "coding.shell" } },
    ]);
    const reviewing = reduceEvents(queued, [
      { sequence: 2, kind: "approval_requested", approvalId: "a1", toolCallId: "t1", state: "reviewing" },
    ]);
    expect(reviewing.blocks).toEqual([
      expect.objectContaining({ kind: "tool", toolCallId: "t1", state: "reviewing_approval" }),
    ]);
  });

  it("builds approval UI fields without exposing the structured payload", () => {
    const details = approvalPresentation({ id: "a1", kind: "approval", content: "{\"command\":\"secret raw payload\"}", data: { tool: "coding.shell", target: "git status --short", effect: "external_side_effect", risk: "high" } }, "zh-CN");
    expect(details).toMatchObject({ tool: "运行命令", target: "git status --short", riskLabel: "高风险", description: "此操作可能影响工作区之外的系统。" });
    expect(JSON.stringify(details)).not.toContain("secret raw payload");
  });

  it("uses the same localized tool names as the TUI", () => {
    expect(toolDisplayName("coding.shell", "zh-CN")).toBe("运行命令");
    expect(toolDisplayName("coding.git_diff", "en")).toBe("View Git Diff");
    expect(toolDisplayName("custom.tool", "zh-CN")).toBe("custom.tool");
  });

  it("formats elapsed time through hours without dropping seconds", () => {
    expect(formatDuration(3_000)).toBe("3s");
    expect(formatDuration(3_723_000)).toBe("1h02m03s");
  });

  it("starts an image-only turn from the composer", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [] });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const image = { id: "image-only", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
    await act(async () => useRuntimeStore.setState({ attachments: [image] }));
    const send = container.querySelector<HTMLButtonElement>(".send-button")!;
    expect(send.disabled).toBe(false);
    expect(send.getAttribute("title")).toBeNull();
    expect(send.getAttribute("aria-label")).toBe("发送");
    expect(container.querySelector(".approval-picker > summary")?.getAttribute("title")).toBeNull();
    expect(container.querySelector(".plan-mode-toggle")?.getAttribute("title")).toBeNull();
    expect(container.querySelector('[data-slot="composer-attach"]')?.getAttribute("title")).toBeNull();
    expect(container.querySelector('[data-slot="composer-attach"]')?.getAttribute("aria-label")).toBe("添加图片");
    await act(async () => send.click());
    expect(useRuntimeStore.getState()).toMatchObject({ running: true, attachments: [] });
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "", attachments: [image] });
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows a selected skill inside the composer and clears it after sending", async () => {
    useRuntimeStore.setState({
      ...state(),
      blocks: [],
      skills: [{ name: "aside-browser", description: "Control the browser", sourcePath: "~/.agents/skills/aside-browser", bundled: false, eager: false, disabled: false, modelVisible: true, resourceCount: 0 }],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const textarea = container.querySelector<HTMLTextAreaElement>("#azem-composer")!;
    await enterComposerText(textarea, "/aside");
    await act(async () => container.querySelector<HTMLButtonElement>(".slash-skills button")!.click());

    expect(container.querySelector(".composer-skill")?.textContent).toContain("aside-browser");
    expect(textarea.value).toBe("");
    await enterComposerText(textarea, "检查当前页面");
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "检查当前页面" });
    expect(container.querySelector(".composer-skill")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("queues, edits, and removes follow-up messages without restarting the active timer", () => {
    useRuntimeStore.setState({ ...state(), running: true, runStartedAt: 123 });
    const image = { id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
    useRuntimeStore.getState().enqueuePrompt("下一轮", [image]);
    const queued = useRuntimeStore.getState().queuedPrompts[0]!;
    expect(queued).toMatchObject({ text: "下一轮", attachments: [image] });
    useRuntimeStore.getState().addOptimisticUser("当前引导", []);
    expect(useRuntimeStore.getState().runStartedAt).toBe(123);
    useRuntimeStore.getState().removeQueuedPrompt(queued.sessionId, queued.id);
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
  });

  it("delivers Queue mode to the active backend run as a non-interrupting follow-up", async () => {
    const followUp = vi.spyOn(bridge, "followUp").mockResolvedValue();
    useRuntimeStore.setState({
      ...state(),
      blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }],
      running: true,
      runId: "r1",
      runStartedAt: Date.now(),
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));

    expect(container.querySelector(".delivery-menu")).toBeNull();
    expect(container.querySelector(".cancel-button")).not.toBeNull();
    await enterComposerText(container.querySelector<HTMLTextAreaElement>("#azem-composer")!, "下一轮再处理");
    expect(container.querySelector(".cancel-button")).toBeNull();
    expect(container.querySelector(".send-button")).not.toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());

    expect(followUp).toHaveBeenCalledWith("s1", "r1", "下一轮再处理", []);
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "下一轮再处理" });
    expect(container.querySelector(".queued-prompts")).toBeNull();

    followUp.mockRestore();
    await act(async () => root.unmount());
    container.remove();
  });

  it("stops the parent run together with its subagents", async () => {
    const cancelActive = vi.spyOn(bridge, "cancelActive").mockResolvedValue(true);
    useRuntimeStore.setState({
      ...state(),
      blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }],
      running: true,
      runId: "r1",
      runStartedAt: Date.now(),
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await act(async () => container.querySelector<HTMLButtonElement>(".cancel-button")!.click());
    expect(cancelActive).toHaveBeenCalledWith(true);
    cancelActive.mockRestore();
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps queues scoped to their session and reorders only that session", () => {
    const first = { id: "a", sessionId: "s1", text: "first", attachments: [], state: "queued" as const };
    const other = { id: "x", sessionId: "s2", text: "other", attachments: [], state: "queued" as const };
    const second = { id: "b", sessionId: "s1", text: "second", attachments: [], state: "queued" as const };
    expect(reorderSessionQueue([first, other, second], "b", "a").map((item) => item.id)).toEqual(["b", "x", "a"]);
    const switched = reduceEvents({ ...state(), queuedPrompts: [first] }, [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]);
    expect(switched.queuedPrompts).toEqual([first]);
  });

  it("holds a prompt in the visible session while another session owns the global run", async () => {
    useRuntimeStore.setState({
      ...state(),
      currentSessionId: "s2",
      globalRunId: "run-a",
      globalRunSessionId: "s1",
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await enterComposerText(container.querySelector<HTMLTextAreaElement>("#azem-composer")!, "wait for session A");
    await act(async () => container.querySelector<HTMLButtonElement>(".send-button")!.click());
    expect(useRuntimeStore.getState()).toMatchObject({
      currentSessionId: "s2",
      running: false,
      queuedPrompts: [{ sessionId: "s2", text: "wait for session A", state: "queued" }],
    });
    expect(container.querySelector(".queued-prompt-content")?.textContent).toBe("wait for session A");
    await act(async () => root.unmount());
    container.remove();
  });

  it("tracks a foreign main run through session-scoped event filtering", () => {
    const foreignSession: Session = {
      id: "s1", workspace: snapshot.workspace, title: "后台任务", providerId: "chatgpt",
      modelId: "gpt-5.6-sol", reasoning: "high", agentMode: "single", updatedAt: new Date().toISOString(),
    };
    const running = reduceEvents({ ...state(), currentSessionId: "s2", sessions: [foreignSession] }, [{
      sequence: 1, kind: "run_started", sessionId: "s1", runId: "run-a",
    }]);
    expect(running).toMatchObject({
      currentSessionId: "s2",
      running: false,
      globalRunId: "run-a",
      globalRunSessionId: "s1",
    });
    const finished = reduceEvents(running, [{
      sequence: 2, kind: "run_finished", sessionId: "s1", runId: "run-a",
    }]);
    expect(finished).toMatchObject({ running: false, globalRunId: "", globalRunSessionId: "" });
    expect(finished.sessions[0]?.unread).toBe(true);
    expect(shouldMarkSessionUnread(running, { sequence: 0, kind: "run_finished", sessionId: "s1", runId: "run-a" })).toBe(true);

    const opened = reduceEvents(finished, [{
      sequence: 3, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]);
    expect(opened.sessions[0]?.unread).toBe(false);
  });

  it("does not mark cancellations, subagents, or the visible session unread", () => {
    const running = { ...state(), currentSessionId: "s2", globalRunId: "run-a", globalRunSessionId: "s1" };
    expect(shouldMarkSessionUnread(running, { sequence: 0, kind: "run_cancelled", sessionId: "s1", runId: "run-a" })).toBe(false);
    expect(shouldMarkSessionUnread(running, { sequence: 0, kind: "run_finished", sessionId: "s1", runId: "run-a", agentId: "agent-1" })).toBe(false);
    expect(shouldMarkSessionUnread({ ...running, currentSessionId: "s1" }, { sequence: 0, kind: "run_finished", sessionId: "s1", runId: "run-a" })).toBe(false);
  });

  it("rejects queue mutations from a different session", () => {
    const queued = { id: "a", sessionId: "s1", text: "first", attachments: [], state: "queued" as const };
    useRuntimeStore.setState({ ...state(), currentSessionId: "s2", queuedPrompts: [queued] });
    useRuntimeStore.getState().updateQueuedPrompt("s2", queued.id, "cross-session", []);
    useRuntimeStore.getState().failQueuedPrompt("s2", queued.id, "cross-session");
    useRuntimeStore.getState().retryQueuedPrompt("s2", queued.id);
    useRuntimeStore.getState().removeQueuedPrompt("s2", queued.id);
    expect(useRuntimeStore.getState().queuedPrompts).toEqual([queued]);
  });

  it("clears a queue editor when the visible session changes", async () => {
    const queued = { id: "a", sessionId: "s1", text: "session A draft", attachments: [], state: "queued" as const };
    useRuntimeStore.setState({
      ...state(),
      blocks: [{ id: "assistant-1", kind: "assistant", content: "running" }],
      running: true,
      runId: "run-a",
      queuedPrompts: [queued],
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await act(async () => container.querySelector<HTMLButtonElement>(".queued-prompt-content")!.click());
    expect(container.querySelector<HTMLTextAreaElement>("#azem-composer")!.value).toBe("session A draft");
    await act(async () => useRuntimeStore.getState().applyEvents([{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", blocks: "[]" },
    }]));
    expect(container.querySelector<HTMLTextAreaElement>("#azem-composer")!.value).toBe("");
    expect(useRuntimeStore.getState().queuedPrompts).toEqual([queued]);
    await act(async () => root.unmount());
    container.remove();
  });

  it("pauses queued follow-ups after interruption until Resume", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    useRuntimeStore.getState().enqueuePrompt("中断后继续", []);
    useRuntimeStore.getState().applyEvents([{ sequence: 1, kind: "run_cancelled", sessionId: "s1", runId: "r1" }]);
    expect(useRuntimeStore.getState().queuePauseReasons.s1).toBe("interrupted");
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(1);
    expect(container.textContent).toContain("队列因你中断任务而暂停");
    await act(async () => container.querySelector<HTMLButtonElement>(".queue-paused button")!.click());
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().running).toBe(true);
    await act(async () => root.unmount());
    container.remove();
  });

  it("uses Cmd+Shift+Enter to invert Queue into Steer for one follow-up", async () => {
    const guide = vi.spyOn(bridge, "guide").mockResolvedValue();
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    const textarea = container.querySelector<HTMLTextAreaElement>("#azem-composer")!;
    await enterComposerText(textarea, "立即调整方向");
    await act(async () => textarea.dispatchEvent(new KeyboardEvent("keydown", {
      bubbles: true, cancelable: true, key: "Enter", code: "Enter", metaKey: true, shiftKey: true,
    })));
    await act(async () => Promise.resolve());
    expect(guide).toHaveBeenCalledWith("s1", "r1", "立即调整方向", []);
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    guide.mockRestore();
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "立即调整方向" });
    await act(async () => root.unmount());
    container.remove();
  });

  it("starts the next queued message after the active run finishes", async () => {
    useRuntimeStore.setState({ ...state(), blocks: [{ id: "assistant-1", kind: "assistant", content: "处理中" }], running: true, runId: "r1", runStartedAt: Date.now() });
    useRuntimeStore.getState().enqueuePrompt("继续检查", []);
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(createElement(ThreadSurface)));
    expect(container.textContent).toContain("继续检查");
    await act(async () => useRuntimeStore.getState().applyEvents([{ sequence: 1, kind: "run_finished", runId: "r1" }]));
    await act(async () => Promise.resolve());
    expect(useRuntimeStore.getState().queuedPrompts).toHaveLength(0);
    expect(useRuntimeStore.getState().blocks.at(-1)).toMatchObject({ kind: "user", content: "继续检查" });
    expect(useRuntimeStore.getState().running).toBe(true);
    await act(async () => root.unmount());
    container.remove();
  });

  it("restores tool process trails when switching sessions", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: {
        provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single",
        blocks: JSON.stringify([
          { kind: "user", runId: "r9", content: "检查样式", state: "submitted" },
          { kind: "assistant", runId: "r9", content: "已完成", state: "completed" },
        ]),
        blockSequences: JSON.stringify([1, 2]),
        toolRecords: JSON.stringify([
          {
            runId: "r9", toolCallId: "c1", name: "read_file", anchorSequence: 1, state: "completed",
            arguments: { path: "frontend/src/styles.css" },
            startedAt: "2026-08-01T10:00:00.000Z", completedAt: "2026-08-01T10:00:04.000Z",
          },
          {
            runId: "r9", toolCallId: "c2", name: "shell", anchorSequence: 1, state: "completed",
            arguments: { command: "npm test" },
            startedAt: "2026-08-01T10:00:05.000Z", completedAt: "2026-08-01T10:00:12.000Z",
          },
        ]),
      },
    }]);
    expect(projected.blocks.map((block) => block.kind)).toEqual(["user", "tool", "tool", "assistant"]);
    expect(projected.blocks[1]).toMatchObject({ kind: "tool", toolCallId: "c1", title: "read_file", state: "completed" });
    expect(projected.blocks[2]).toMatchObject({ kind: "tool", toolCallId: "c2", title: "shell" });
    expect(projected.running).toBe(false);
  });

  it("restores and changes the active session model", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "session_loaded",
      sessionId: "s2",
      state: "loaded",
      data: { blocks: "[]", provider: "grok", model: "grok-4.20", reasoning: "medium", agentMode: "team" },
    }]);
    expect(projected.snapshot).toMatchObject({ provider: "grok", model: "grok-4.20", reasoning: "medium", agentMode: "team" });

    useRuntimeStore.getState().hydrate(snapshot);
    useRuntimeStore.getState().setSessionModel("chatgpt", "gpt-5.6-luna", "low");
    expect(useRuntimeStore.getState().snapshot).toMatchObject({ provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" });
    useRuntimeStore.getState().setChatGPTFastMode(true);
    expect(useRuntimeStore.getState().snapshot?.chatgptFastMode).toBe(true);
  });

  it("projects the persisted Codex speed into the composer state", () => {
    const projected = reduceEvents(state(), [{ sequence: 1, kind: "model_routes", data: { chatgpt_fast_mode: "true" } }]);
    expect(projected.snapshot?.chatgptFastMode).toBe(true);
  });

  it("projects the live shell wall-clock ceiling from model routes", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "model_routes",
      data: { shell_max_wall_clock_seconds: "1800" },
    }]);
    expect(projected.snapshot?.shellMaxWallClockSeconds).toBe(1800);
  });

  it("does not wipe a subscription catalog when a later empty catalog event arrives", () => {
    const loaded = reduceEvents(state(), [{
      sequence: 1,
      kind: "model_catalog",
      data: { provider: "grok", models: JSON.stringify([{ id: "grok-4.6", name: "Grok 4.6", contextWindow: 500_000 }]) },
    }]);
    const wiped = reduceEvents(loaded, [{
      sequence: 2,
      kind: "model_catalog",
      data: { provider: "grok" },
    }]);
    expect(wiped.modelsByProvider.grok).toEqual([
      { id: "grok-4.6", name: "Grok 4.6", aliases: [], reasoningLevels: [], defaultReasoning: "", contextWindow: 500_000 },
    ]);
  });

  it("projects the model catalog used by the composer switcher", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1,
      kind: "model_catalog",
      data: { provider: "chatgpt", models: JSON.stringify([{ id: "gpt-5.6-sol", name: "GPT-5.6 Sol", aliases: ["gpt-latest"], contextWindow: 272_000, reasoningLevels: ["medium", "high"], defaultReasoning: "high", supportsTools: true, supportsReasoning: true, supportsStructured: true, serviceTiers: [{ id: "priority" }], inputModalities: ["text"], outputModalities: ["text"] }, { id: "gpt-5.6-terra", name: "GPT-5.6 Terra", reasoningLevels: ["low", "medium"], disabled: true }]) },
    }]);
    expect(projected.modelsByProvider.chatgpt).toEqual([
	  { id: "gpt-5.6-sol", name: "GPT-5.6 Sol", aliases: ["gpt-latest"], contextWindow: 272_000, reasoningLevels: ["medium", "high"], defaultReasoning: "high", capabilities: ["tools", "reasoning", "structured-output", "fast"], inputModalities: ["text"], outputModalities: ["text"] },
	  { id: "gpt-5.6-terra", name: "GPT-5.6 Terra", aliases: [], reasoningLevels: ["low", "medium"], defaultReasoning: "", disabled: true },
    ]);
    expect(projected.contextUsage.contextLimit).toBe(272_000);
	expect(modelDisplayName("openai/gpt-5.6-sol", "openai/gpt-5.6-sol")).toBe("GPT 5.6 Sol");
	expect(providerDisplayName("deepseek", [{ ...state().modelProviders[0]!, id: "deepseek", displayName: "DeepSeek" }])).toBe("DeepSeek");
  });

  it("uses the subscription catalog context limit regardless of startup event order", () => {
    const catalogEvent = {
      sequence: 1,
      kind: "model_catalog" as const,
      data: { provider: "chatgpt", models: JSON.stringify([{ id: "gpt-5.6-sol", contextWindow: 272_000 }]) },
    };
    const loadedEvent = {
      sequence: 2,
      kind: "session_loaded" as const,
      sessionId: "s1",
      state: "loaded",
      data: { blocks: "[]", provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", usage: JSON.stringify({ inputTokens: 8_400, contextLimit: 1_050_000 }) },
    };
    const catalogLast = reduceEvents(state(), [{ ...loadedEvent, sequence: 1 }, { ...catalogEvent, sequence: 2 }]);
    const sessionLast = reduceEvents(state(), [catalogEvent, loadedEvent]);
    expect(catalogLast.contextUsage.contextLimit).toBe(272_000);
    expect(sessionLast.contextUsage.contextLimit).toBe(272_000);
  });

  it("restores and renders context occupancy through assistant-ui ComposerContext", async () => {
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded",
      data: { blocks: "[]", provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single", usage: JSON.stringify({ inputTokens: 68_000, outputTokens: 4_000, contextLimit: 288_000, currentTurnMainReported: true }) },
    }]);
    expect(restored.contextUsage).toEqual({ inputTokens: 68_000, outputTokens: 4_000, contextLimit: 288_000, reported: true });
    expect(contextOccupancy(restored.contextUsage, null)).toMatchObject({ used: 72_000, limit: 288_000, percentage: 25, remaining: 216_000, estimated: false });
    const usage = composerContextUsage(restored.contextUsage, null, "zh-CN");
    expect(usage).toEqual({
      segments: [
        { key: "provider_input", label: "模型输入", tokens: 68_000, tone: "primary" },
        { key: "current_output", label: "当前输出", tokens: 4_000, tone: "secondary" },
      ],
      total: 288_000,
    });

    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(ComposerContext, { usage, label: "上下文构成", totalLabel: "总计" })));
    expect(container.querySelector('[data-slot="composer-context"]')).not.toBeNull();
    expect(container.querySelector("button")?.getAttribute("aria-label")).toBe("上下文构成");
    expect(container.textContent).toContain("模型输入94%68k");
    expect(container.textContent).toContain("当前输出6%4k");
    expect(container.textContent).toContain("总计72k / 288k");
    await act(async () => root.unmount());
  });

  it("updates context occupancy from provider fact snapshots", () => {
    const projected = reduceEvents(state(), [{
      sequence: 1, kind: "context_usage", sessionId: "s1", state: "reported",
      data: { factSnapshot: "true", usageSnapshot: JSON.stringify({
        inputTokens: 90_000, outputTokens: 6_000, contextLimit: 128_000, currentTurnMainReported: true,
        cacheInputTokens: 120_000, cachedInputTokens: 72_000, cacheWriteTokens: 9_000,
        uncachedInputTokens: 18_000, cacheReported: true, mainCacheReported: true, cacheWriteReported: true,
      }) },
    }]);
    expect(projected.contextUsage).toEqual({
      inputTokens: 90_000, outputTokens: 6_000, contextLimit: 128_000, reported: true,
      cacheInputTokens: 120_000, cachedInputTokens: 72_000, cacheWriteTokens: 9_000,
      uncachedInputTokens: 18_000, cacheReported: true, mainCacheReported: true, cacheWriteReported: true,
    });
  });

  it("accumulates cache totals without replacing main-context occupancy for aggregate requests", () => {
    const projected = reduceEvents(state(), [
      {
        sequence: 1, kind: "context_usage", sessionId: "s1", state: "reported",
        data: { requestKind: "main", inputTokens: "100", outputTokens: "9", cachedInputTokens: "40", cacheWriteTokens: "6", cacheStatus: "reported", cacheWriteStatus: "reported" },
      },
      {
        sequence: 2, kind: "context_usage", sessionId: "s1", state: "reported",
        data: { requestKind: "team", aggregateOnly: "true", inputTokens: "60", outputTokens: "3", cachedInputTokens: "30", cacheWriteTokens: "4", cacheStatus: "reported", cacheWriteStatus: "reported" },
      },
    ]);
    expect(projected.contextUsage).toEqual({
      inputTokens: 100, outputTokens: 9, contextLimit: 0, reported: true,
      cacheInputTokens: 160, cachedInputTokens: 70, cacheWriteTokens: 10,
      cacheReported: true, cacheWriteReported: true,
    });
  });

  it("keeps the main context kernel free of subagent occupancy and cache", () => {
    const mainProfile = {
      source: "request", estimated: true,
      contributions: [{ category: "core", name: "azem.core_instructions", tokens: 1_200 }],
    };
    const projected = reduceEvents(state(), [
      {
        sequence: 1, kind: "context_profile", sessionId: "s1", state: "estimated",
        contextProfile: mainProfile,
      },
      {
        sequence: 2, kind: "context_usage", sessionId: "s1", state: "reported",
        data: { requestKind: "main", inputTokens: "100", outputTokens: "9", cachedInputTokens: "40", cacheWriteTokens: "6", cacheStatus: "reported", cacheWriteStatus: "reported" },
      },
      {
        sequence: 3, kind: "context_profile", sessionId: "s1", agentId: "child-1", state: "estimated",
        contextProfile: {
          source: "request", estimated: true,
          contributions: [{ category: "conversation", name: "message:user:1", tokens: 88_000 }],
        },
      },
      {
        sequence: 4, kind: "context_usage", sessionId: "s1", state: "reported",
        data: { requestKind: "subagent", aggregateOnly: "true", inputTokens: "80", outputTokens: "12", cachedInputTokens: "70", cacheWriteTokens: "8", cacheStatus: "reported", cacheWriteStatus: "reported" },
      },
      {
        sequence: 5, kind: "context_usage", sessionId: "s1", state: "pending",
        data: { requestKind: "autolearn", factSnapshot: "true", usageSnapshot: JSON.stringify({ inputTokens: 999, outputTokens: 99, reported: true }) },
      },
    ]);
    expect(projected.contextProfile).toEqual(mainProfile);
    expect(projected.contextUsage).toEqual({
      inputTokens: 100, outputTokens: 9, contextLimit: 0, reported: true,
      cacheInputTokens: 100, cachedInputTokens: 40, cacheWriteTokens: 6,
      cacheReported: true, cacheWriteReported: true,
    });
  });

  it("restores and updates the current Todo plan", () => {
    const initialTodo = {
      goal: "在桌面端展示任务进度", revision: 1,
      phases: [{ id: "phase-1", title: "实现", items: [
        { id: "item-1", content: "同步 Todo 状态", status: "completed" as const },
        { id: "item-2", content: "添加顶部计划条", status: "in_progress" as const },
        { id: "item-3", content: "运行验证", status: "pending" as const },
      ] }],
    };
    const restored = reduceEvents(state(), [{
      sequence: 1, kind: "session_loaded", sessionId: "s1", state: "loaded", todo: initialTodo,
      data: { blocks: "[]", provider: "chatgpt", model: "gpt-5.6-sol", reasoning: "high", agentMode: "single" },
    }]);
    expect(restored.todo).toEqual(initialTodo);

    const updatedTodo = {
      ...initialTodo, revision: 2,
      phases: [{ ...initialTodo.phases[0]!, items: initialTodo.phases[0]!.items.map((item) => item.id === "item-2" ? { ...item, status: "completed" as const } : item) }],
    };
    const updated = reduceEvents(restored, [{ sequence: 2, kind: "todo_updated", sessionId: "s1", todo: updatedTodo }]);
    expect(updated.todo).toEqual(updatedTodo);
  });


  it("localizes built-in skill runtime tools", () => {
    expect(toolDisplayName("hydaelyn_activate_skill", "zh-CN")).toBe("加载技能");
    expect(toolDisplayName("hydaelyn_read_skill_resource", "zh-CN")).toBe("读取技能资源");
    expect(toolDisplayName("hydaelyn_read_skill_resource", "en")).toBe("Read Skill Resource");
  });

  it("projects current workspace line changes from git status events", () => {
    const changed = reduceEvents(state(), [{
      sequence: 1, kind: "git_branches", workspaceDirty: true,
      data: { additions: "21", deletions: "4", changed_files: "54" },
    }]);
    expect(changed).toMatchObject({ workspaceDirty: true, workspaceAdditions: 21, workspaceDeletions: 4, workspaceChangedFiles: 54 });

    const clean = reduceEvents(changed, [{ sequence: 2, kind: "git_branches", workspaceDirty: false, data: { additions: "0", deletions: "0", changed_files: "0" } }]);
    expect(clean).toMatchObject({ workspaceDirty: false, workspaceAdditions: 0, workspaceDeletions: 0, workspaceChangedFiles: 0 });
  });


  it("clears pull request detail loading when its panel closes", () => {
    useRuntimeStore.setState({ ...state(), selectedPullRequestNumber: 42, pullRequestLoading: true });
    useRuntimeStore.getState().selectPullRequest(null);
    expect(useRuntimeStore.getState()).toMatchObject({ selectedPullRequestNumber: null, pullRequestLoading: false });
  });

});
