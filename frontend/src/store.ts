import { create } from "zustand";
import type {
  AgentCatalogEntry,
  AgentState,
  Attachment,
  Block,
  ContextProfile,
  GitBranch,
  InspectorTab,
  ModelRoute,
  RuntimeEvent,
  Session,
  SkillEntry,
  Snapshot,
  View,
} from "./types";

export interface ModelOption {
  id: string;
  name: string;
  reasoningLevels: string[];
  defaultReasoning?: string;
}

export interface RuntimeData {
  snapshot: Snapshot | null;
  sessions: Session[];
  currentSessionId: string;
  currentTitle: string;
  blocks: Block[];
  agents: AgentState[];
  selectedAgentId: string;
  agentBlocks: Block[];
  agentCatalog: AgentCatalogEntry[];
  skills: SkillEntry[];
  branches: GitBranch[];
  modelRoutes: ModelRoute[];
  modelsByProvider: Record<string, ModelOption[]>;
  contextProfile: ContextProfile | null;
  todo: unknown;
  recovery: Array<Record<string, unknown>>;
  runId: string;
  running: boolean;
  runStartedAt: number;
  activity: string;
  approvalMode: string;
  workspaceDirty: boolean;
  lastSequence: number;
  error: string;
  view: View;
  inspectorTab: InspectorTab;
  inspectorOpen: boolean;
  settingsOpen: boolean;
  commandOpen: boolean;
  planMode: boolean;
  attachments: Attachment[];
  theme: "system" | "light" | "dark";
}

interface RuntimeActions {
  hydrate: (snapshot: Snapshot, demo?: boolean) => void;
  applyEvents: (events: RuntimeEvent[]) => void;
  setView: (view: View) => void;
  setInspectorTab: (tab: InspectorTab) => void;
  setInspectorOpen: (open: boolean) => void;
  selectAgent: (agentId: string) => void;
  setSettingsOpen: (open: boolean) => void;
  setCommandOpen: (open: boolean) => void;
  setPlanMode: (enabled: boolean) => void;
  setTheme: (theme: RuntimeData["theme"]) => void;
  setLanguage: (language: "en" | "zh-CN") => void;
  setSessionModel: (provider: string, model: string, reasoning: string) => void;
  setChatGPTFastMode: (enabled: boolean) => void;
  addOptimisticUser: (content: string) => void;
  setRunId: (runId: string) => void;
  failRun: (message: string) => void;
  setError: (message: string) => void;
  addAttachment: (attachment: Attachment) => void;
  removeAttachment: (id: string) => void;
  clearAttachments: () => void;
}

const initialData: RuntimeData = {
  snapshot: null,
  sessions: [],
  currentSessionId: "",
  currentTitle: "",
  blocks: [],
  agents: [],
  selectedAgentId: "",
  agentBlocks: [],
  agentCatalog: [],
  skills: [],
  branches: [],
  modelRoutes: [],
  modelsByProvider: {},
  contextProfile: null,
  todo: null,
  recovery: [],
  runId: "",
  running: false,
  runStartedAt: 0,
  activity: "",
  approvalMode: "prompt",
  workspaceDirty: false,
  lastSequence: 0,
  error: "",
  view: "thread",
  inspectorTab: "environment",
  inspectorOpen: true,
  settingsOpen: false,
  commandOpen: false,
  planMode: false,
  attachments: [],
  theme: "system",
};

export const useRuntimeStore = create<RuntimeData & RuntimeActions>((set) => ({
  ...initialData,
  hydrate: (snapshot, demo = false) => set((state) => ({
    ...state,
    ...hydrateData(snapshot, demo),
  })),
  applyEvents: (events) => set((state) => reduceEvents(state, events)),
  setView: (view) => set({ view, commandOpen: false }),
  setInspectorTab: (inspectorTab) => set({ inspectorTab, inspectorOpen: true }),
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  selectAgent: (selectedAgentId) => set({ selectedAgentId, inspectorTab: "agents", inspectorOpen: true }),
  setSettingsOpen: (settingsOpen) => set({ settingsOpen, commandOpen: false }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setPlanMode: (planMode) => set({ planMode }),
  setTheme: (theme) => set({ theme }),
  setLanguage: (language) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, language } : state.snapshot,
  })),
  setSessionModel: (provider, model, reasoning) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, provider, model, reasoning } : state.snapshot,
  })),
  setChatGPTFastMode: (chatgptFastMode) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, chatgptFastMode } : state.snapshot,
  })),
  addOptimisticUser: (content) => set((state) => ({
    blocks: [...state.blocks, { id: `user-${Date.now()}`, kind: "user", content, state: "submitted", attachments: state.attachments }],
    running: true,
    runStartedAt: Date.now(),
    activity: "waiting_model",
    error: "",
  })),
  setRunId: (runId) => set({ runId }),
  failRun: (message) => set((state) => ({
    running: false,
    error: message,
    blocks: [...state.blocks, { id: `error-${Date.now()}`, kind: "error", title: "运行失败", content: message, state: "failed" }],
  })),
  setError: (error) => set({ error }),
  addAttachment: (attachment) => set((state) => ({ attachments: [...state.attachments, attachment] })),
  removeAttachment: (id) => set((state) => ({ attachments: state.attachments.filter((item) => item.id !== id) })),
  clearAttachments: () => set({ attachments: [] }),
}));

export function reduceEvents<T extends RuntimeData>(state: T, events: RuntimeEvent[]): T {
  let next = { ...state, blocks: [...state.blocks], sessions: [...state.sessions], agents: [...state.agents] };
  for (const event of events) next = reduceEvent(next, event);
  return next;
}

function reduceEvent<T extends RuntimeData>(state: T, event: RuntimeEvent): T {
  if (event.sequence && event.sequence <= state.lastSequence) return state;
  let next = { ...state, lastSequence: Math.max(state.lastSequence, event.sequence || 0) };
  const data = event.data ?? {};

  switch (event.kind) {
    case "bootstrap_done":
      if (next.snapshot && event.text) next.snapshot = { ...next.snapshot, workspace: event.text };
      break;
    case "session_loaded":
      if (event.state === "list") {
        next.sessions = parseArray(data.sessions).map(normalizeSession);
      } else if (event.sessionId) {
        next.currentSessionId = event.sessionId;
        next.snapshot = {
          ...next.snapshot!,
          provider: data.provider,
          model: data.model,
          reasoning: data.reasoning,
          agentMode: data.agentMode,
        };
        next.blocks = parseArray(data.blocks).map(normalizeBlock);
        next.runId = data.lastRunID ?? "";
        next.currentTitle = next.sessions.find((item) => item.id === event.sessionId)?.title ?? next.currentTitle;
        next.agents = (event.agentSnapshots ?? []).map(normalizeAgentSnapshot);
        next.attachments = [];
      }
      break;
    case "run_started":
      next.running = true;
      next.runId = event.runId ?? next.runId;
      next.runStartedAt = Date.now();
      next.activity = "waiting_model";
      break;
    case "thinking_delta":
      next.blocks = appendDelta(next.blocks, event, "thinking", "思考中");
      next.activity = "thinking";
      break;
    case "text_delta":
      next.blocks = appendDelta(next.blocks, event, "assistant", "Azem");
      next.activity = "responding";
      break;
    case "tool_started":
    case "tool_update":
    case "tool_finished":
      next.blocks = updateTool(next.blocks, event);
      next.activity = event.kind === "tool_finished" ? "waiting_model" : "tool";
      break;
    case "diff_ready":
      next.blocks = [...next.blocks, {
        id: event.toolCallId || `diff-${event.sequence}`,
        kind: "diff",
        runId: event.runId,
        toolCallId: event.toolCallId,
        title: data.path || "变更",
        content: event.text ?? "",
        state: event.state || "ready",
        data,
      }];
      break;
    case "approval_requested":
      next.blocks = [...next.blocks, {
        id: event.approvalId || `approval-${event.sequence}`,
        kind: "approval",
        runId: event.runId,
        toolCallId: event.toolCallId,
        approvalId: event.approvalId,
        title: data.action || data.name || "需要审批",
        content: event.text || data.reason || "此操作需要你的确认。",
        state: "pending",
        data,
      }];
      next.activity = "approval";
      break;
    case "approval_resolved":
      next.blocks = next.blocks.map((block) => block.approvalId === event.approvalId ? { ...block, state: event.state || data.decision || "resolved" } : block);
      next.activity = "waiting_model";
      break;
    case "agent_state":
      if (event.agent) next.agents = upsertAgent(next.agents, normalizeAgent(event.agentId ?? "", event.agent, event.state, event.text));
      break;
    case "agent_detail":
      if (event.state === "agent_types") next.agentCatalog = (event.agentCatalog ?? []).map(normalizeAgentCatalog);
      if (event.state === "detail") {
        next.selectedAgentId = event.agentId ?? next.selectedAgentId;
        next.agentBlocks = (event.agentBlocks ?? []).map(normalizeBlock);
      }
      break;
    case "skill_catalog":
      next.skills = (event.skillCatalog ?? []).map(normalizeSkill);
      break;
    case "context_profile":
      next.contextProfile = event.contextProfile ?? null;
      break;
    case "todo_updated":
      next.todo = event.todo ?? null;
      break;
    case "model_routes":
      next.modelRoutes = event.modelRoutes ?? [];
      if (next.snapshot) next.snapshot = {
        ...next.snapshot,
        subagentConcurrency: numberValue(data.subagent_max_concurrency, next.snapshot.subagentConcurrency),
        chatgptFastMode: data.chatgpt_fast_mode === "true",
      };
      break;
    case "model_catalog": {
      const provider = data.provider || "unknown";
      next.modelsByProvider = { ...next.modelsByProvider, [provider]: parseArray(data.models).map(normalizeModel) };
      break;
    }
    case "approval_mode":
      next.approvalMode = event.state || next.approvalMode;
      break;
    case "git_branches":
      next.branches = (event.gitBranches ?? []).map(normalizeBranch);
      next.workspaceDirty = Boolean(event.workspaceDirty);
      break;
    case "recovery_state":
      if (data.items) next.recovery = parseArray(data.items);
      else if (event.state === "suspended") next.recovery = [...next.recovery, {
        id: event.runId ?? `recovery-${event.sequence}`,
        runId: event.runId ?? "",
        kind: data.kind ?? "run",
        title: "Suspended run",
        detail: event.text ?? "The run needs attention before it can continue.",
        state: event.state,
      }];
      else if (event.state === "reconciled") next.recovery = next.recovery.filter((item) => String(item.id ?? item.ID) !== event.text);
      break;
    case "run_finished":
    case "run_cancelled":
      next.running = false;
      next.activity = event.kind === "run_finished" ? "completed" : "cancelled";
      next.runId = "";
      break;
    case "run_failed":
      next.running = false;
      next.activity = "failed";
      next.error = event.text ?? "Run failed";
      next.runId = "";
      next.blocks = [...next.blocks, { id: `error-${event.sequence}`, kind: "error", title: "运行失败", content: event.text, state: "failed" }];
      break;
    case "bridge_error":
      next.error = event.text ?? "Desktop bridge failed";
      break;
  }
  return next;
}

function hydrateData(snapshot: Snapshot, demo: boolean): Partial<RuntimeData> {
  if (!demo) return {
    snapshot,
    currentSessionId: snapshot.sessionId,
    approvalMode: snapshot.approvalMode,
    lastSequence: snapshot.sequence,
  };
  const mode = new URLSearchParams(location.search).get("demo") ?? "running";
  const session: Session = {
    id: snapshot.sessionId, title: "分析 Azem GUI 设计方案", providerId: snapshot.provider,
    modelId: snapshot.model, reasoning: snapshot.reasoning, agentMode: snapshot.agentMode,
    updatedAt: new Date().toISOString(),
  };
  const blocks: Block[] = mode === "empty" ? [] : demoBlocks(mode === "review");
  return {
    snapshot,
    sessions: [session],
    currentSessionId: snapshot.sessionId,
    currentTitle: session.title,
    blocks,
    running: mode === "running",
    runId: mode === "running" ? "run-demo" : "",
    runStartedAt: Date.now() - 402_000,
    activity: mode === "running" ? "tool" : "completed",
    approvalMode: snapshot.approvalMode,
    branches: [{ name: "feat/gui-wails-v3", current: true }, { name: "main", current: false }],
    agents: demoAgents(),
    skills: [{ name: "frontend-design", description: "Production UI design guidance", sourcePath: "~/.agents/skills/frontend-design", eager: true, disabled: false, resourceCount: 4 }],
    modelRoutes: [
      { Scope: "plan", Role: "", Label: "Plan", Route: {} },
      { Scope: "compaction", Role: "", Label: "Compaction", Route: {} },
      { Scope: "subagent", Role: "explore", Label: "Explore", Route: {} },
      { Scope: "subagent", Role: "worker", Label: "Worker", Route: {} },
      { Scope: "subagent", Role: "review", Label: "Review", Route: {} },
      { Scope: "subagent", Role: "verify", Label: "Verify", Route: {} },
    ],
    modelsByProvider: {
      chatgpt: [
        { id: "gpt-5.6-sol", name: "GPT-5.6 Sol", reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high" },
        { id: "gpt-5.6-luna", name: "GPT-5.6 Luna", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "medium" },
      ],
      grok: [{ id: "grok-4.20", name: "Grok 4.20", reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high" }],
    },
    contextProfile: {
      source: "estimated", estimated: true,
      contributions: [
        { category: "core", name: "Instructions", tokens: 7340 },
        { category: "conversation", name: "Thread", tokens: 5250 },
        { category: "builtin_tools", name: "Tools", tokens: 2280 },
      ],
    },
  };
}

function demoBlocks(review: boolean): Block[] {
  const blocks: Block[] = [
    { id: "user-demo", kind: "user", content: "参考 Synara UI 与 Codex App，重新设计 Azem GUI，并保留现有 Agent 治理和会话恢复能力。", state: "submitted" },
    { id: "thinking-demo", kind: "thinking", title: "思考中", content: "先检查现有事件模型和 TUI Block 结构，再建立桌面端的信息架构、状态投影和视觉规范。", state: "completed" },
    { id: "tool-demo", kind: "tool", title: "检查桌面事件投影", content: "internal/app/events.go · internal/app/event_broker.go · internal/session/service.go", state: "completed" },
    { id: "assistant-demo", kind: "assistant", content: "界面采用两种密度状态：新会话时居中输入；开始执行后 Composer 固定到底部，并显示会话时间线与右侧 Inspector。", state: "completed" },
  ];
  if (review) {
    blocks.push(
      { id: "diff-demo", kind: "diff", title: "internal/desktop/bridge.go", content: "@@ -18,6 +18,10 @@\n type Bridge struct {\n+  sequence atomic.Uint64\n+  emit EventEmitter\n }", state: "ready", data: { additions: "4", deletions: "0" } },
      { id: "approval-demo", kind: "approval", approvalId: "approval-demo", title: "写入桌面入口", content: "创建 Wails v3 桌面入口并更新 Go 模块依赖。", state: "pending", data: { risk: "medium" } },
    );
  }
  return blocks;
}

function demoAgents(): AgentState[] {
  return [
    { id: "planner", type: "Planner", description: "完成界面与事件协议", model: "gpt-5.6-sol", background: false, capabilityMode: "read-only", isolation: "none", cwd: "", activity: "completed", warning: "", worktreePath: "", toolCalls: 6, turns: 2, tokensUsed: 9400, elapsedMs: 105000, state: "completed", summary: "完成界面与事件协议" },
    { id: "implementer", type: "Implementer", description: "正在生成桌面骨架", model: "gpt-5.6-sol", background: true, capabilityMode: "all", isolation: "worktree", cwd: "", activity: "editing", warning: "", worktreePath: "/tmp/azem-gui", toolCalls: 11, turns: 3, tokensUsed: 13800, elapsedMs: 267000, state: "running", summary: "正在生成桌面骨架" },
    { id: "reviewer", type: "Reviewer", description: "等待变更完成", model: "gpt-5.6-sol", background: false, capabilityMode: "read-only", isolation: "none", cwd: "", activity: "waiting", warning: "", worktreePath: "", toolCalls: 0, turns: 0, tokensUsed: 0, elapsedMs: 0, state: "queued", summary: "等待变更完成" },
  ];
}

function appendDelta(blocks: Block[], event: RuntimeEvent, kind: "thinking" | "assistant", title: string): Block[] {
  const id = `${kind}-${event.runId || "current"}`;
  const index = blocks.findIndex((block) => block.id === id);
  if (index < 0) return [...blocks, { id, kind, runId: event.runId, title, content: event.text ?? "", state: "streaming" }];
  return blocks.map((block, current) => current === index ? { ...block, content: `${block.content ?? ""}${event.text ?? ""}`, state: event.state || "streaming" } : block);
}

function updateTool(blocks: Block[], event: RuntimeEvent): Block[] {
  const id = event.toolCallId || `tool-${event.sequence}`;
  const index = blocks.findIndex((block) => block.toolCallId === id || block.id === id);
  const data = event.data ?? {};
  const text = event.text ?? "";
  if (index < 0) return [...blocks, {
    id, kind: "tool", runId: event.runId, toolCallId: id,
    title: data.name || "调用工具", content: text, state: event.state || (event.kind === "tool_finished" ? "completed" : "running"), data,
  }];
  return blocks.map((block, current) => current === index ? {
    ...block,
    content: text ? [block.content, text].filter(Boolean).join(block.content?.endsWith("\n") ? "" : "\n") : block.content,
    state: event.state || (event.kind === "tool_finished" ? "completed" : block.state),
    data: { ...block.data, ...data },
  } : block);
}

function normalizeSession(raw: Record<string, unknown>): Session {
  return {
    id: stringValue(raw, "id", "ID"), title: stringValue(raw, "title", "Title") || "New session",
    providerId: stringValue(raw, "providerId", "ProviderID"), modelId: stringValue(raw, "modelId", "ModelID"),
    reasoning: stringValue(raw, "reasoning", "Reasoning"), agentMode: stringValue(raw, "agentMode", "AgentMode"),
    updatedAt: stringValue(raw, "updatedAt", "UpdatedAt"),
  };
}

function normalizeBlock(raw: Record<string, unknown>, index: number): Block {
  return {
    id: String(raw.id ?? raw.Sequence ?? `block-${index}`),
    kind: String(raw.kind ?? raw.Kind ?? "assistant") as Block["kind"],
    runId: stringValue(raw, "runId", "RunID"), agentId: stringValue(raw, "agentId", "AgentID"),
    toolCallId: stringValue(raw, "toolCallId", "ToolCallID"), title: stringValue(raw, "title", "Title"),
    content: stringValue(raw, "content", "Content"), state: stringValue(raw, "state", "State"),
    collapsed: Boolean(raw.collapsed ?? raw.Collapsed), attachments: (raw.attachments ?? raw.Attachments) as Attachment[] | undefined,
  };
}

function normalizeAgentSnapshot(raw: Record<string, unknown>): AgentState {
  return normalizeAgent(stringValue(raw, "id", "ID"), (raw.agent ?? raw.Agent ?? {}) as Record<string, unknown>, stringValue(raw, "state", "State"), stringValue(raw, "summary", "Summary"));
}

function normalizeAgent(id: string, raw: Record<string, unknown>, state = "", summary = ""): AgentState {
  return {
    id, type: stringValue(raw, "type", "Type"), description: stringValue(raw, "description", "Description"),
    model: stringValue(raw, "model", "Model"), background: Boolean(raw.background ?? raw.Background),
    capabilityMode: stringValue(raw, "capabilityMode", "CapabilityMode"), isolation: stringValue(raw, "isolation", "Isolation"),
    cwd: stringValue(raw, "cwd", "CWD"), activity: stringValue(raw, "activity", "Activity"),
    warning: stringValue(raw, "warning", "Warning"), worktreePath: stringValue(raw, "worktreePath", "WorktreePath"),
    toolCalls: numberValue(raw.toolCalls ?? raw.ToolCalls), turns: numberValue(raw.turns ?? raw.Turns),
    tokensUsed: numberValue(raw.tokensUsed ?? raw.TokensUsed), elapsedMs: numberValue(raw.elapsedMs ?? raw.ElapsedMS),
    state, summary,
  };
}

function normalizeAgentCatalog(raw: Record<string, unknown>): AgentCatalogEntry {
  return {
    name: stringValue(raw, "name", "Name"), description: stringValue(raw, "description", "Description"),
    model: stringValue(raw, "model", "Model"), reasoning: stringValue(raw, "reasoning", "Reasoning"),
    capabilityMode: stringValue(raw, "capabilityMode", "CapabilityMode"), isolation: stringValue(raw, "isolation", "Isolation"),
    source: stringValue(raw, "source", "Source"), enabled: Boolean(raw.enabled ?? raw.Enabled),
  };
}

function normalizeSkill(raw: Record<string, unknown>): SkillEntry {
  return {
    name: stringValue(raw, "name", "Name"), description: stringValue(raw, "description", "Description"),
    sourcePath: stringValue(raw, "sourcePath", "SourcePath"), eager: Boolean(raw.eager ?? raw.Eager),
    disabled: Boolean(raw.disabled ?? raw.Disabled), resourceCount: numberValue(raw.resourceCount ?? raw.ResourceCount),
  };
}

function normalizeBranch(raw: Record<string, unknown>): GitBranch {
  return { name: stringValue(raw, "name", "Name"), current: Boolean(raw.current ?? raw.Current) };
}

function normalizeModel(raw: Record<string, unknown>): ModelOption {
  const id = stringValue(raw, "id", "ID");
  return {
    id, name: [stringValue(raw, "name", "Name"), id].filter(Boolean)[0]!,
    reasoningLevels: ((raw.reasoningLevels ?? raw.ReasoningLevels ?? []) as unknown[]).map(String),
    defaultReasoning: stringValue(raw, "defaultReasoning", "DefaultReasoning"),
  };
}

function upsertAgent(agents: AgentState[], agent: AgentState): AgentState[] {
  const index = agents.findIndex((current) => current.id === agent.id);
  if (index < 0) return [...agents, agent];
  return agents.map((current, currentIndex) => currentIndex === index ? { ...current, ...agent } : current);
}

function parseArray(value: string | undefined): Array<Record<string, unknown>> {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function stringValue(raw: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) if (raw[key] !== undefined && raw[key] !== null) return String(raw[key]);
  return "";
}

function numberValue(value: unknown, fallback = 0): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
