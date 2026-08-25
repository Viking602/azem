import type { AgentState, Block, ModelProvider, Session, Snapshot } from "../types";
import type { RuntimeData } from "./state";

export function hydrateData(snapshot: Snapshot, demo: boolean): Partial<RuntimeData> {
  if (!demo) return {
    snapshot,
    currentSessionId: snapshot.sessionId,
    approvalMode: snapshot.approvalMode,
    pullRequestMonitors: new Map((snapshot.pullRequestMonitors ?? []).map((monitor) => [monitor.number, monitor])),
  };
  const mode = new URLSearchParams(location.search).get("demo") ?? "running";
  const session: Session = {
    id: snapshot.sessionId, workspace: snapshot.workspace, title: "优化 Azem 的 UI 动效", providerId: snapshot.provider,
    modelId: snapshot.model, reasoning: snapshot.reasoning, agentMode: snapshot.agentMode,
    updatedAt: new Date().toISOString(),
  };
  const sessions: Session[] = [
    session,
    { ...session, id: "session-codex-plugins", title: "插件兼容设计", updatedAt: "2026-08-08T10:20:00Z" },
    { ...session, id: "session-semantic-context", title: "语义上下文重建", updatedAt: "2026-08-07T08:00:00Z" },
    { ...session, id: "session-llmux-limits", workspace: "/Users/viking/GolandProjects/llmux", title: "Normalize usage limits", updatedAt: "2026-08-09T08:00:00Z", unread: true },
    { ...session, id: "session-llmux-release", workspace: "/Users/viking/GolandProjects/llmux", title: "发布 v0.2.4", updatedAt: "2026-08-08T02:00:00Z" },
  ];
  const blocks: Block[] = mode === "empty" ? [] : demoBlocks(mode === "review");
  const subagentDemo = mode === "subagent" ? demoSubagentConversation() : null;
  return {
    snapshot,
    projects: [
      { workspace: "/Users/viking/GolandProjects/azem", updatedAt: "2026-08-09T09:00:00Z" },
      { workspace: "/Users/viking/GolandProjects/llmux", updatedAt: "2026-08-09T08:00:00Z" },
      { workspace: "/Users/viking/GolandProjects/venat", updatedAt: "2026-08-08T09:00:00Z" },
    ],
    sessions,
    currentSessionId: snapshot.sessionId,
    currentTitle: session.title,
    blocks,
    running: mode === "running",
    globalRunId: mode === "running" ? "run-demo" : "",
    globalRunSessionId: mode === "running" ? snapshot.sessionId : "",
    runId: mode === "running" ? "run-demo" : "",
    runStartedAt: Date.now() - 402_000,
    activity: mode === "running" ? "tool" : "completed",
    recap: mode === "empty" ? null : {
      sessionId: snapshot.sessionId,
      anchor: snapshot.workspace,
      coveredBoundary: "run-demo",
      goal: "完成桌面运行时体验优化",
      summary: "核心交互已完成，正在执行最终验证并整理交付证据。",
      openItems: "in_progress: 运行完整前端与桌面测试",
      revision: 3,
      updatedAt: new Date().toISOString(),
    },
    approvalMode: snapshot.approvalMode,
    pullRequestMonitors: new Map((snapshot.pullRequestMonitors ?? []).map((monitor) => [monitor.number, monitor])),
    branches: [{ name: "main", current: true }, { name: "feat/usage-store", current: false }],
    agents: mode === "review" ? demoReviewAgents() : subagentDemo ? [subagentDemo.agent] : [],
    selectedAgentId: subagentDemo?.agent.id ?? "",
    agentBlocks: subagentDemo?.blocks ?? [],
    skills: [
      { name: "frontend-design", description: "生产级界面设计与实现", sourcePath: "~/.agents/skills/frontend-design", bundled: false, eager: true, disabled: false, modelVisible: true, resourceCount: 4 },
      { name: "waza-ui", description: "产品界面与交互质量检查", sourcePath: "~/.codex/plugins/waza/ui", bundled: false, eager: false, disabled: false, modelVisible: true, resourceCount: 7 },
      { name: "github", description: "Pull Request、Issue 与检查状态", sourcePath: "~/.codex/plugins/github", bundled: false, eager: false, disabled: false, modelVisible: true, resourceCount: 3 },
    ],
    mcpServers: [
      { name: "grep", removable: false, enabled: true, state: "ready", transport: "streamable_http", target: "https://mcp.grep.app", url: "https://mcp.grep.app", args: [], inheritEnv: false, approval: "never", maxConcurrency: 2, toolCount: 1, tools: [{ name: "searchGitHub", description: "Search public GitHub code", effect: "read_only", requiresApproval: false }], error: "" },
      { name: "local-docs", removable: true, enabled: false, state: "disabled", transport: "stdio", target: "npx -y @modelcontextprotocol/server-filesystem", command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem"], inheritEnv: true, approval: "always", maxConcurrency: 1, toolCount: 0, tools: [], error: "" },
    ],
    securityConfig: {
      enabled: true, defaultMode: "standard", workers: 4, subagents: 3,
      stopAfterNoNew: 4, stopAfterConsecutiveErrors: 3, maxDiscoveryRuns: 40,
      maxTimeHours: 96,
      routes: {
        audit: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" },
        reducer: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" },
        fixer: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" },
        verifier: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" },
      },
    },
    plugins: [
      { id: "waza@demo", name: "waza", displayName: "Waza", version: "3.33.0", marketplace: "openai", origin: "codex", description: "工程健康、研究、UI 与写作工作流", developerName: "OpenAI", category: "Developer Tools", brandColor: "#3278ef", logoPath: "", enabled: true, skillCount: 6, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "MCP"], status: "ready", warning: "" },
      { id: "github@demo", name: "github", displayName: "GitHub", version: "0.1.9", marketplace: "openai", origin: "codex", description: "仓库、PR、Issue、Review 与 CI", developerName: "GitHub", category: "Developer Tools", brandColor: "#181717", logoPath: "", enabled: true, skillCount: 2, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: true, capabilities: ["Skills", "MCP"], status: "degraded", warning: "OAuth 等待授权" },
      { id: "kami@demo", name: "kami", displayName: "Kami", version: "1.12.0", marketplace: "kami", origin: "codex", description: "文档与产品页面排版", developerName: "Kami", category: "Productivity", brandColor: "#6d5efc", logoPath: "", enabled: true, skillCount: 1, mcpServerCount: 0, integratedMCPCount: 0, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills"], status: "ready", warning: "" },
      { id: "custom@demo", name: "custom", displayName: "Custom Toolkit", version: "0.8.0", marketplace: "local", origin: "local", description: "包含未信任的生命周期 Hooks", developerName: "Azem", category: "Local", brandColor: "#ff6a3d", logoPath: "", enabled: true, skillCount: 3, mcpServerCount: 0, integratedMCPCount: 0, hookCount: 2, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "Hooks"], status: "degraded", warning: "Hooks 等待显式信任" },
      { id: "disabled@demo", name: "disabled", displayName: "实验扩展", version: "0.1.0", marketplace: "local", origin: "local", description: "未启用的实验能力", developerName: "Azem", category: "Experimental", brandColor: "#8b8b84", logoPath: "", enabled: false, skillCount: 1, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "MCP"], status: "disabled", warning: "" },
    ],
    extensionThemes: [{ name: "demo-night", path: "demo://theme", vars: { accent: "#7aa2f7" }, colors: { accent: "accent", text: "#f4f4f1", muted: "#b3b4ae", selectedBg: "#2a2f45", userMessageBg: "#1f2335" }, source: "demo" }],
    hookCatalog: {
      enabled: true, trustHooks: false,
      sources: [{ id: "custom@demo", name: "Custom Toolkit", origin: "plugin", pluginId: "custom@demo", source: "", hookCount: 2, trusted: false, warning: "Hooks 等待显式信任" }],
      commands: [], diagnostics: [],
    },
    modelRoutes: [
      { scope: "main", role: "", label: "主会话", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "plan", role: "", label: "规划", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "approval", role: "", label: "审批", route: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" } },
      { scope: "vision", role: "", label: "视觉", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "recap", role: "", label: "会话回顾", route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
      { scope: "security", role: "audit", label: "Security audit", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "security", role: "reducer", label: "Security reducer", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "security", role: "fixer", label: "Security fixer", route: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" } },
      { scope: "security", role: "verifier", label: "Security verifier", route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { scope: "subagent", role: "research", label: "Research", route: { provider: "chatgpt", model: "gpt-5.3-spark", reasoning: "medium" } },
      { scope: "subagent", role: "review", label: "Review", route: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" } },
    ],
    modelsByProvider: {
      chatgpt: [
        { id: "gpt-5.6", name: "GPT-5.6", aliases: ["gpt-5.6-sol"], reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.5-codex", name: "GPT-5.5 Codex", reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "code"] },
        { id: "gpt-5.3-spark", name: "GPT-5.3 Spark", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "medium", capabilities: ["tools", "fast"] },
      ],
      grok: [
        { id: "grok-4.20", name: "Grok 4.20", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "grok-code-fast", name: "Grok Code Fast", reasoningLevels: ["low", "medium"], defaultReasoning: "medium", capabilities: ["tools", "code", "fast"] },
      ],
      cursor: [
        { id: "gpt-5.2-low", name: "GPT-5.2 Low", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-low-fast", name: "GPT-5.2 Low Fast", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.2-high", name: "GPT-5.2 High", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-low-thinking", name: "GPT-5.2 Low Thinking", reasoningLevels: ["low"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-high-thinking", name: "GPT-5.2 High Thinking", reasoningLevels: ["high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-high-fast", name: "GPT-5.2 High Fast", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.2-xhigh", name: "GPT-5.2 Extra High", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-xhigh-fast", name: "GPT-5.2 Extra High Fast", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.2-xhigh-thinking", name: "GPT-5.2 Extra High Thinking", reasoningLevels: ["xhigh"], defaultReasoning: "xhigh", capabilities: ["reasoning", "tools", "image"] },
        { id: "gpt-5.2-low-thinking-fast", name: "GPT-5.2 Low Thinking Fast", reasoningLevels: ["low"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.2-high-thinking-fast", name: "GPT-5.2 High Thinking Fast", reasoningLevels: ["high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "gpt-5.2-xhigh-thinking-fast", name: "GPT-5.2 Extra High Thinking Fast", reasoningLevels: ["xhigh"], defaultReasoning: "xhigh", capabilities: ["reasoning", "tools", "image", "fast"] },
        { id: "claude-fable-5-low", name: "Claude Fable 5 1M Low (NO ZDR)", reasoningLevels: ["low"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-medium", name: "Claude Fable 5 1M Medium (NO ZDR)", reasoningLevels: ["medium"], defaultReasoning: "medium", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-high", name: "Claude Fable 5 1M (NO ZDR)", reasoningLevels: ["high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-xhigh", name: "Claude Fable 5 1M Extra High (NO ZDR)", reasoningLevels: ["xhigh"], defaultReasoning: "xhigh", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-max", name: "Claude Fable 5 1M Max (NO ZDR)", reasoningLevels: ["max"], defaultReasoning: "max", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-thinking-low", name: "Claude Fable 5 1M Low Thinking (NO ZDR)", reasoningLevels: ["low"], defaultReasoning: "low", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-thinking-medium", name: "Claude Fable 5 1M Medium Thinking (NO ZDR)", reasoningLevels: ["medium"], defaultReasoning: "medium", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-thinking-high", name: "Claude Fable 5 1M Thinking (NO ZDR)", reasoningLevels: ["high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-thinking-xhigh", name: "Claude Fable 5 1M Extra High Thinking (NO ZDR)", reasoningLevels: ["xhigh"], defaultReasoning: "xhigh", capabilities: ["reasoning", "tools", "image"] },
        { id: "claude-fable-5-thinking-max", name: "Claude Fable 5 1M Max Thinking (NO ZDR)", reasoningLevels: ["max"], defaultReasoning: "max", capabilities: ["reasoning", "tools", "image"] },
        { id: "composer-2", name: "Composer 2", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools"] },
      ],
      openrouter: [
        { id: "claude-sonnet-4.5", name: "Claude Sonnet 4.5", reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
        { id: "gemini-2.5-pro", name: "Gemini 2.5 Pro", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "image"] },
      ],
    },
    modelProviders: demoModelProviders(),
    workspaceDirty: true,
    workspaceChangedFiles: 4,
    workspaceAdditions: 186,
    workspaceDeletions: 32,
    contextProfile: {
      source: "estimated", estimated: true,
      policyVersion: 3,
      canonicalHighWater: 128,
      rebuildReason: "automatic",
      segments: Array.from({ length: 4 }, (_, index) => ({
        kind: index === 0 ? "archive_carrier" : "hot_tail",
        mandatory: true,
        token_estimate: index === 0 ? 3400 : 2100,
        content_hash: `demo-segment-${index + 1}`,
      })),
      contributions: [
        { category: "core", name: "Instructions", tokens: 7340 },
        { category: "conversation", name: "Thread", tokens: 5250 },
        { category: "builtin_tools", name: "Tools", tokens: 2280 },
      ],
    },
    contextUsage: {
      inputTokens: 58_000, outputTokens: 4_000, contextLimit: 164_000, reported: true,
      cacheInputTokens: 96_000, cachedInputTokens: 62_000, cacheWriteTokens: 9_400, uncachedInputTokens: 6_960,
      cacheReported: true, mainCacheReported: true, cacheWriteReported: true,
    },
    todo: {
      goal: "完成 Azem 交互原型并验证动效边界",
      revision: 12,
      phases: [{
        id: "demo-plan",
        title: "执行计划",
        items: [
          { id: "demo-analyse", content: "分析当前界面与事件链", status: "completed" },
          { id: "demo-tokens", content: "建立视觉与动效 token", status: "completed" },
          { id: "demo-build", content: "制作完整交互页面", status: "in_progress" },
          { id: "demo-verify", content: "验证性能与减弱动效", status: "pending" },
        ],
      }],
    },
  };
}

function demoReviewAgents(): AgentState[] {
  const observedAt = Date.now();
  return [
    ["review-backend", "审查 Go 后端改动", "正在检查调度与持久化边界"],
    ["review-frontend", "审查前端改动", "正在核对事件投影与交互状态"],
    ["review-security", "审查安全边界", "正在检查审批与外部副作用"],
    ["review-architecture", "审查架构与文档一致性", "正在比对架构约束与维护文档"],
  ].map(([id, description, activity], index) => ({
    id,
    type: "review",
    description,
    model: "gpt-5.5-codex",
    background: false,
    capabilityMode: "read-only",
    isolation: "none",
    cwd: ".",
    activity,
    warning: "",
    worktreePath: "",
    toolCalls: index + 1,
    turns: 1,
    tokensUsed: 800 + index * 170,
    elapsedMs: 12_000 + index * 3_000,
    state: "running",
    summary: "",
    preview: activity,
    previewKind: "commentary",
    previewRunId: `run-${id}`,
    elapsedObservedAt: observedAt,
  }));
}

function demoBlocks(review: boolean): Block[] {
  const blocks: Block[] = [
    { id: "user-demo", kind: "user", runId: "demo-run", content: "给我一个具体优化这个项目 UI 的方案，页面切换和文字流式输出的动效都要有。", state: "submitted" },
    { id: "progress-structure", kind: "commentary", runId: "demo-run", title: "progress", content: "**读取当前前端结构**\nApp、Sidebar、Timeline 与顶部任务计划", textPhase: "commentary", state: "completed", data: { startedAt: "1000", completedAt: "1100" } },
    { id: "tool-structure", kind: "tool", runId: "demo-run", title: "coding.read_file", content: "{\"path\":\"frontend/src/components/Timeline.tsx\"}", state: "completed", data: { startedAt: "1100", completedAt: "2200", elapsedMs: "1100" } },
    { id: "progress-baseline", kind: "commentary", runId: "demo-run", title: "progress", content: "**提取视觉与动效基线**\n暖白纸面 · 8 个流式尾部节点 · reduced motion", textPhase: "commentary", state: "completed", data: { startedAt: "2300", completedAt: "2400" } },
    { id: "tool-baseline", kind: "tool", runId: "demo-run", title: "coding.search", content: "{\"query\":\"reduced-motion\",\"path\":\"frontend/src\"}", state: "completed", data: { startedAt: "2400", completedAt: "3100", elapsedMs: "700" } },
    { id: "progress-prototype", kind: "commentary", runId: "demo-run", title: "progress", content: "**构建高保真交互原型**\n页面、工具与文本共享一套节奏", textPhase: "commentary", state: "completed", data: { startedAt: "3200", completedAt: "3300" } },
    {
      id: "tool-prototype", kind: "tool", runId: "demo-run", title: "coding.edit_hashline", state: "running",
      data: {
        startedAt: "3300", elapsedMs: "4800",
        arguments: JSON.stringify({ input: "¶frontend/src/prototype.css#ABCD\nreplace 278:\n+.timeline-step { min-height: 31px; }\ninsert after 560:\n+.timeline-step[data-state=running] { color: var(--ink); }" }),
      },
    },
    { id: "commentary-demo", kind: "commentary", runId: "demo-run", title: "进度更新", content: "我会保留 Azem 现有的暖色工作台语气，把动效集中在状态发生变化的瞬间：页面切换建立空间关系，工具状态沿轨迹推进，流式文字只让新到达的尾部逐渐显现。", textPhase: "commentary", state: "streaming" },
    { id: "status-demo", kind: "status", runId: "demo-run", title: "渲染交互原型", content: "designs/azem-ui-motion-concept\n4 个文件 · 浏览器验证 · 示例图导出", state: "running", data: { variant: "artifact", progress: "38" } },
    { id: "conclusion-demo", kind: "status", runId: "demo-run", title: "方案结论", content: "", state: "ready", data: { variant: "section" } },
    { id: "assistant-demo", kind: "assistant", runId: "demo-run", title: "方案结论", content: "建议把 Azem 的视觉方向定义为“静谧机械感”。保留暖白纸面和橙色品牌点，把导航、检查器和运行状态组织成连续的空间层。\n\n页面切换使用 320ms 的轻微景深过渡；工具状态使用 180–240ms 的轨迹推进；流式文字让每个新字符从模糊、透明和轻微下移中逐字浮现，旧文本立即稳定，避免整段闪动。", textPhase: "final_answer", state: "streaming" },
  ];
  if (review) {
    blocks.push(
      { id: "diff-demo", kind: "diff", runId: "demo-run", title: "internal/desktop/bridge.go", content: "@@ -18,6 +18,10 @@\n type Bridge struct {\n+  sequence atomic.Uint64\n+  emit EventEmitter\n }", state: "ready", data: { additions: "4", deletions: "0" } },
      { id: "approval-demo", kind: "approval", runId: "demo-run", approvalId: "approval-demo", title: "写入桌面入口", content: "创建 Wails v3 桌面入口并更新 Go 模块依赖。", state: "pending", data: { risk: "medium" } },
    );
  }
  return blocks;
}

function demoModelProviders(): ModelProvider[] {
  const now = Date.now();
  const cursorCycleStart = Math.floor((now-(6*24+4)*60*60*1000)/1000);
  const cursorCycleEnd = Math.floor((now+(24*24+20)*60*60*1000)/1000);
  const cursorQuotaUpdatedAt = new Date(now-60_000).toISOString();
  return [
    {
      id: "chatgpt", displayName: "ChatGPT", backend: "subscription", defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true,
      credentialConfigured: true, credentialSource: "stored", subscription: true, accountLabel: "viking@example.com", accountPlan: "Pro 20x",
      quotaAvailable: true, quotaUsedPercent: 61.5, quotaResetsAt: 1786492800, quotaBalance: "US$12.50", modelsDevId: "openai", modelsSource: "subscription",
      models: [
        { id: "gpt-5.6", name: "GPT-5.6", contextWindow: 272000, reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "fast"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "gpt-5.5-codex", name: "GPT-5.5 Codex", contextWindow: 272000, reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text"], outputModalities: ["text"] },
        { id: "gpt-5.3-spark", name: "GPT-5.3 Spark", contextWindow: 128000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "medium", capabilities: ["tools", "fast"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
    {
      id: "grok", displayName: "Grok", backend: "subscription", defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true,
      credentialConfigured: true, credentialSource: "stored", subscription: true, accountLabel: "viking@example.com", accountPlan: "Plus",
      quotaAvailable: true, quotaUsedPercent: 28, quotaBalance: "无限额度", quotaUnlimited: true, modelsDevId: "xai", modelsSource: "subscription",
      models: [
        { id: "grok-4.20", name: "Grok 4.20", contextWindow: 256000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "grok-code-fast", name: "Grok Code Fast", contextWindow: 128000, reasoningLevels: ["low", "medium"], defaultReasoning: "medium", capabilities: ["tools"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
    {
      id: "cursor", displayName: "Cursor", backend: "subscription", defaultBaseUrl: "", baseUrl: "", envKey: "", enabled: true,
      credentialConfigured: true, credentialSource: "stored", subscription: true, accountLabel: "viking@example.com", accountPlan: "ultra",
      quotaAvailable: true, quotaPeriod: "monthly", quotaStartedAt: cursorCycleStart, quotaUsedPercent: 28,
      quotaBreakdown: [{ id: "cursor", usedPercent: 16 }, { id: "third_party", usedPercent: 74 }],
      quotaResetsAt: cursorCycleEnd, quotaUpdatedAt: cursorQuotaUpdatedAt, modelsDevId: "cursor", modelsSource: "subscription",
      models: [
        { id: "composer-2", name: "Composer 2", contextWindow: 200000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
    {
      id: "openrouter", displayName: "OpenRouter", backend: "openai_compat", defaultBaseUrl: "https://openrouter.ai/api/v1", baseUrl: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY", enabled: true,
      credentialConfigured: true, credentialSource: "stored", modelsDevId: "openrouter", modelsSource: "provider_api+models.dev", models: [
        { id: "claude-sonnet-4.5", name: "Claude Sonnet 4.5", contextWindow: 200000, reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "gemini-2.5-pro", name: "Gemini 2.5 Pro", contextWindow: 1000000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "deepseek-v3.2", name: "DeepSeek V3.2", contextWindow: 128000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "medium", capabilities: ["tools"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
  ];
}

function demoAgents(): AgentState[] {
  const elapsedObservedAt = Date.now();
  const shared = {
    model: "gpt-5.6-sol", background: true, capabilityMode: "read-only", isolation: "none",
    cwd: "", activity: "thinking", warning: "", worktreePath: "", turns: 1, tokensUsed: 3200,
    state: "running", previewKind: "thinking" as const, previewRunId: "demo-child", elapsedObservedAt,
  };
  return [
    { ...shared, id: "recovery", type: "review", description: "Adversarial recovery", toolCalls: 5, elapsedMs: 38000, summary: "", preview: "我会检查中断与恢复路径，确认终态不会被新的稀疏事件覆盖。" },
    { ...shared, id: "boundaries", type: "explore", description: "Adversarial boundaries", toolCalls: 4, elapsedMs: 33000, summary: "", preview: "先界定当前会话和工作区边界，再核对子智能体的可见范围。" },
    { ...shared, id: "assumptions", type: "review", description: "Adversarial assumptions", toolCalls: 4, elapsedMs: 28000, summary: "", preview: "我会按只读审查处理：先确定真实边界，再检查能够复现的假设冲突。" },
    { ...shared, id: "composition", type: "explore", description: "Adversarial composition", toolCalls: 3, elapsedMs: 23000, summary: "", preview: "我会先界定未提交差异的行为边界，再只读追踪组合失败面。" },
    { ...shared, id: "cascade", type: "review", description: "Adversarial cascade", toolCalls: 3, elapsedMs: 17000, summary: "", preview: "先做只读审查：确认仓库结构和相关调用链，只报告能够复现的级联问题。" },
    { ...shared, id: "abuse", type: "explore", description: "Adversarial abuse", toolCalls: 2, elapsedMs: 12000, summary: "", preview: "我会先按只读范围建立变更边界，再从对抗场景逐条核对失败面。" },
  ];
}

function demoSubagentConversation(): { agent: AgentState; blocks: Block[] } {
  const runId = "demo-security-review";
  return {
    agent: {
      id: "security-review", type: "review", description: "审查安全边界", model: "gpt-5.6-sol",
      background: true, capabilityMode: "read-only", isolation: "none", cwd: ".",
      activity: "", warning: "", worktreePath: "", toolCalls: 2, turns: 1, tokensUsed: 2_840,
      elapsedMs: 14_200, state: "completed", summary: "安全审查完成", preview: "未发现达到报告门槛的 finding。",
      previewKind: "assistant", previewRunId: runId, elapsedObservedAt: Date.now(),
    },
    blocks: [
      {
        id: "security-user", kind: "user", runId, state: "completed",
        content: "立即停止继续调查，不再调用任何工具。仅基于已经读取并验证的证据输出最终安全 findings；每条保留精确 file:line、触发、guard 分析、severity/confidence。",
      },
      {
        id: "security-progress", kind: "commentary", runId, state: "completed",
        content: "**整理已验证证据**\n归并已检查的边界与剩余缺口",
        data: { startedAt: "1000", completedAt: "3200", elapsedMs: "2200" },
      },
      {
        id: "security-search", kind: "tool", runId, title: "coding.search", state: "completed",
        content: "未发现可复现的高风险调用链。",
        data: { startedAt: "3200", completedAt: "8100", elapsedMs: "4900" },
      },
      {
        id: "security-answer", kind: "assistant", runId, textPhase: "final_answer", state: "completed",
        content: "## Verdict\n\nREVISE\n\n## Findings\n\n无法达到报告门槛的安全 finding。\n\n## Evidence reviewed\n\n- 已覆盖范围：审批、外部副作用与桌面 Bridge。\n- 未发现可给出精确 `file:line`、攻击调用链和触发条件的已验证证据。\n\n## Residual gaps\n\n完整安全审阅被停止指令阻断，未覆盖的边界不能据此确认为发布硬阻塞。",
      },
    ],
  };
}
