import { create } from "zustand";
import { translator } from "./i18n";
import { isSubagentActive, isSubagentTerminal, projectSubagentPreview } from "./subagents";
import type {
  AgentCatalogEntry,
  AgentState,
  Attachment,
  BackgroundProcess,
  Block,
  ContextProfile,
  DeliveryMode,
  GitBranch,
  InspectorTab,
  ModelRoute,
  QueuedPrompt,
  PullRequest,
  PullRequestDashboard,
  PullRequestDetailResponse,
  PullRequestMonitorState,
  RuntimeEvent,
  Session,
  SkillEntry,
  Snapshot,
  TodoList,
  View,
} from "./types";

export interface ModelOption {
  id: string;
  name: string;
  reasoningLevels: string[];
  defaultReasoning?: string;
  contextWindow?: number;
}

export interface ContextUsage {
  inputTokens: number;
  outputTokens: number;
  contextLimit: number;
  reported: boolean;
}

export interface RuntimeData {
  snapshot: Snapshot | null;
  sessions: Session[];
  currentSessionId: string;
  currentTitle: string;
  blocks: Block[];
  agents: AgentState[];
  backgroundProcesses: BackgroundProcess[];
  selectedAgentId: string;
  agentBlocks: Block[];
  agentCatalog: AgentCatalogEntry[];
  skills: SkillEntry[];
  branches: GitBranch[];
  modelRoutes: ModelRoute[];
  pullRequestDashboard: PullRequestDashboard | null;
  selectedPullRequestNumber: number | null;
  pullRequestDetail: PullRequest | null;
  pullRequestMonitors: Map<number, PullRequestMonitorState>;
  pullRequestLoading: boolean;
  pullRequestMutating: boolean;
  pullRequestError: string;
  modelsByProvider: Record<string, ModelOption[]>;
  contextProfile: ContextProfile | null;
  contextUsage: ContextUsage;
  todo: TodoList | null;
  recovery: Array<Record<string, unknown>>;
  runId: string;
  running: boolean;
  runStartedAt: number;
  activity: string;
  approvalMode: string;
  workspaceDirty: boolean;
  workspaceAdditions: number;
  workspaceDeletions: number;
  workspaceChangedFiles: number;
  lastSequence: number;
  error: string;
  view: View;
  inspectorTab: InspectorTab;
  inspectorOpen: boolean;
  settingsOpen: boolean;
  commandOpen: boolean;
  planMode: boolean;
  attachments: Attachment[];
  // ponytail: queue is process-local; persist it with session blocks if crash recovery becomes necessary.
  queuedPrompts: QueuedPrompt[];
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
  setPullRequestDashboard: (dashboard: PullRequestDashboard) => void;
  selectPullRequest: (number: number | null) => void;
  setPullRequestDetail: (response: PullRequestDetailResponse) => void;
  setPullRequestLoading: (loading: boolean) => void;
  setPullRequestMutating: (mutating: boolean) => void;
  setPullRequestError: (message: string) => void;
  updatePullRequestMonitor: (monitor: PullRequestMonitorState) => void;
  setPlanMode: (enabled: boolean) => void;
  setTheme: (theme: RuntimeData["theme"]) => void;
  setLanguage: (language: "en" | "zh-CN") => void;
  setSessionModel: (provider: string, model: string, reasoning: string) => void;
  setChatGPTFastMode: (enabled: boolean) => void;
  setQueueMode: (mode: DeliveryMode) => void;
  addOptimisticUser: (content: string, attachments?: Attachment[]) => void;
  setRunId: (runId: string) => void;
  failRun: (message: string) => void;
  setError: (message: string) => void;
  addAttachment: (attachment: Attachment) => void;
  removeAttachment: (id: string) => void;
  replaceAttachments: (attachments: Attachment[]) => void;
  clearAttachments: () => void;
  enqueuePrompt: (text: string, attachments: Attachment[]) => void;
  removeQueuedPrompt: (id: string) => void;
}

const initialData: RuntimeData = {
  snapshot: null,
  sessions: [],
  currentSessionId: "",
  currentTitle: "",
  blocks: [],
  agents: [],
  backgroundProcesses: [],
  selectedAgentId: "",
  agentBlocks: [],
  agentCatalog: [],
  skills: [],
  branches: [],
  modelRoutes: [],
  pullRequestDashboard: null,
  selectedPullRequestNumber: null,
  pullRequestDetail: null,
  pullRequestMonitors: new Map(),
  pullRequestLoading: false,
  pullRequestMutating: false,
  pullRequestError: "",
  modelsByProvider: {},
  contextProfile: null,
  contextUsage: emptyContextUsage(),
  todo: null,
  recovery: [],
  runId: "",
  running: false,
  runStartedAt: 0,
  activity: "",
  approvalMode: "prompt",
  workspaceDirty: false,
  workspaceAdditions: 0,
  workspaceDeletions: 0,
  workspaceChangedFiles: 0,
  lastSequence: 0,
  error: "",
  view: "thread",
  inspectorTab: "environment",
  inspectorOpen: true,
  settingsOpen: false,
  commandOpen: false,
  planMode: false,
  attachments: [],
  queuedPrompts: [],
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
  selectAgent: (selectedAgentId) => set((state) => ({
    selectedAgentId,
    selectedPullRequestNumber: selectedAgentId ? null : state.selectedPullRequestNumber,
    agentBlocks: selectedAgentId && selectedAgentId === state.selectedAgentId ? state.agentBlocks : [],
    inspectorTab: selectedAgentId ? "agents" : state.inspectorTab,
    // Side chat is driven by selectedAgentId; keep env inspector closed while open.
    inspectorOpen: selectedAgentId ? false : state.inspectorOpen,
  })),
  setSettingsOpen: (settingsOpen) => set({ settingsOpen, commandOpen: false }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setPullRequestDashboard: (pullRequestDashboard) => set({ pullRequestDashboard, pullRequestLoading: false, pullRequestError: "" }),
  selectPullRequest: (selectedPullRequestNumber) => set((state) => ({
    selectedPullRequestNumber,
    pullRequestDetail: state.pullRequestDetail?.number === selectedPullRequestNumber ? state.pullRequestDetail : null,
    selectedAgentId: selectedPullRequestNumber ? "" : state.selectedAgentId,
    agentBlocks: selectedPullRequestNumber ? [] : state.agentBlocks,
    inspectorOpen: selectedPullRequestNumber ? false : state.inspectorOpen,
  })),
  setPullRequestDetail: ({ pullRequest, monitor }) => set((state) => {
    const pullRequestMonitors = new Map(state.pullRequestMonitors);
    pullRequestMonitors.set(monitor.number, monitor);
    return {
      pullRequestDetail: pullRequest,
      pullRequestMonitors,
      pullRequestLoading: false,
      pullRequestMutating: false,
      pullRequestError: "",
    };
  }),
  setPullRequestLoading: (pullRequestLoading) => set({ pullRequestLoading }),
  setPullRequestMutating: (pullRequestMutating) => set({ pullRequestMutating }),
  setPullRequestError: (pullRequestError) => set({ pullRequestError, pullRequestLoading: false, pullRequestMutating: false }),
  updatePullRequestMonitor: (monitor) => set((state) => {
    const pullRequestMonitors = new Map(state.pullRequestMonitors);
    pullRequestMonitors.set(monitor.number, monitor);
    return { pullRequestMonitors };
  }),
  setPlanMode: (planMode) => set({ planMode }),
  setTheme: (theme) => set({ theme }),
  setLanguage: (language) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, language } : state.snapshot,
  })),
  setSessionModel: (provider, model, reasoning) => set((state) => {
    const modelChanged = state.snapshot?.provider !== provider || state.snapshot?.model !== model;
    const contextLimit = state.modelsByProvider[provider]?.find((item) => item.id === model)?.contextWindow ?? 0;
    return {
      snapshot: state.snapshot ? { ...state.snapshot, provider, model, reasoning } : state.snapshot,
      contextUsage: modelChanged ? emptyContextUsage(contextLimit) : state.contextUsage,
    };
  }),
  setChatGPTFastMode: (chatgptFastMode) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, chatgptFastMode } : state.snapshot,
  })),
  setQueueMode: (queueMode) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, queueMode } : state.snapshot,
  })),
  addOptimisticUser: (content, attachments) => set((state) => {
    const sessionId = state.currentSessionId || state.snapshot?.sessionId || "";
    const existing = state.sessions.find((item) => item.id === sessionId);
    const optimisticSession = sessionId && state.snapshot
      ? {
          ...(existing ?? {
            id: sessionId,
            title: translator(state.snapshot.language === "en" ? "en" : "zh-CN")("newSession"),
            providerId: state.snapshot.provider,
            modelId: state.snapshot.model,
            reasoning: state.snapshot.reasoning,
            agentMode: state.snapshot.agentMode,
          }),
          updatedAt: new Date().toISOString(),
        }
      : null;
    return {
      blocks: [...state.blocks, { id: `user-${Date.now()}`, kind: "user", content, state: "submitted", attachments: attachments ?? state.attachments }],
      sessions: optimisticSession ? [optimisticSession, ...state.sessions.filter((item) => item.id !== sessionId)] : state.sessions,
      currentTitle: optimisticSession?.title ?? state.currentTitle,
      running: true,
      runStartedAt: state.running ? state.runStartedAt : Date.now(),
      activity: "waiting_model",
      error: "",
    };
  }),
  setRunId: (runId) => set({ runId }),
  failRun: (message) => set((state) => ({
    running: false,
    error: message,
    blocks: [...state.blocks, { id: `error-${Date.now()}`, kind: "error", title: translator(state.snapshot?.language === "en" ? "en" : "zh-CN")("runFailed"), content: message, state: "failed" }],
  })),
  setError: (error) => set({ error }),
  addAttachment: (attachment) => set((state) => ({ attachments: [...state.attachments, attachment] })),
  removeAttachment: (id) => set((state) => ({ attachments: state.attachments.filter((item) => item.id !== id) })),
  replaceAttachments: (attachments) => set({ attachments }),
  clearAttachments: () => set({ attachments: [] }),
  enqueuePrompt: (text, attachments) => set((state) => ({
    queuedPrompts: [...state.queuedPrompts, { id: crypto.randomUUID(), text, attachments: [...attachments] }],
  })),
  removeQueuedPrompt: (id) => set((state) => ({ queuedPrompts: state.queuedPrompts.filter((item) => item.id !== id) })),
}));

const SESSION_SCOPED_EVENTS: Record<string, true> = {
  run_started: true,
  thinking_delta: true,
  text_delta: true,
  tool_started: true,
  tool_update: true,
  tool_finished: true,
  diff_ready: true,
  approval_requested: true,
  approval_resolved: true,
  agent_state: true,
  context_profile: true,
  context_usage: true,
  todo_updated: true,
  run_finished: true,
  run_cancelled: true,
  run_failed: true,
};

export function reduceEvents<T extends RuntimeData>(state: T, events: RuntimeEvent[]): T {
  let next = { ...state, blocks: [...state.blocks], sessions: [...state.sessions], agents: [...state.agents] };
  for (const event of events) next = reduceEvent(next, event);
  return next;
}

function reduceEvent<T extends RuntimeData>(state: T, event: RuntimeEvent): T {
  if (event.sequence && event.sequence <= state.lastSequence) return state;
  let next = { ...state, lastSequence: Math.max(state.lastSequence, event.sequence || 0) };
  const data = event.data ?? {};
  if (event.sessionId && event.sessionId !== state.currentSessionId && SESSION_SCOPED_EVENTS[event.kind]) return next;


  switch (event.kind) {
    case "bootstrap_done":
      if (next.snapshot && event.text) next.snapshot = { ...next.snapshot, workspace: event.text };
      break;
    case "session_loaded":
      if (event.state === "list") {
        const loadedSessions = parseArray(data.sessions).map(normalizeSession);
        const optimisticCurrent = next.running ? next.sessions.find((item) => item.id === next.currentSessionId) : undefined;
        next.sessions = optimisticCurrent && !loadedSessions.some((item) => item.id === optimisticCurrent.id)
          ? [optimisticCurrent, ...loadedSessions]
          : loadedSessions;
        next.currentTitle = next.sessions.find((item) => item.id === next.currentSessionId)?.title ?? next.currentTitle;
      } else if (event.sessionId) {
        next.currentSessionId = event.sessionId;
        next.snapshot = {
          ...next.snapshot!,
          provider: data.provider,
          model: data.model,
          reasoning: data.reasoning,
          agentMode: data.agentMode,
        };
        // Restore the process trail: session blocks alone drop tools; merge durable toolRecords.
        next.blocks = mergeSessionTranscript(
          parseArray(data.blocks).map(normalizeBlock),
          parseJSONValue(data.blockSequences),
          parseJSONValue(data.toolRecords),
        );
        next.runId = data.activeRunID || data.lastRunID || "";
        next.running = data.active === "true";
        next.runStartedAt = next.running ? Date.now() : 0;
        next.activity = next.running ? "waiting_model" : "";
        next.error = "";
        next.selectedAgentId = "";
        next.agentBlocks = [];
        next.currentTitle = next.sessions.find((item) => item.id === event.sessionId)?.title ?? next.currentTitle;
        next.agents = (event.agentSnapshots ?? []).map(normalizeAgentSnapshot);
        next.todo = event.todo ?? null;
        next.attachments = [];
        next.queuedPrompts = [];
        next.contextProfile = null;
        const contextLimit = next.modelsByProvider[data.provider]?.find((item) => item.id === data.model)?.contextWindow ?? 0;
        next.contextUsage = parseContextUsage(data.usage, contextLimit);
      }
      break;
    case "run_started":
      next.running = true;
      next.runId = event.runId ?? next.runId;
      next.runStartedAt = Date.now();
      next.activity = "waiting_model";
      break;
    case "thinking_delta": {
      const thinkingTitle = translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("thinking");
      // Subagent frames carry agentId — stream them into the side chat, not the main feed.
      if (event.agentId) {
        next.agents = projectSubagentPreview(next.agents, event.agentId, event.runId ?? "", "thinking", event.text ?? "");
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = appendDelta(next.agentBlocks, event, "thinking", thinkingTitle);
        }
      } else {
        next.blocks = appendDelta(next.blocks, event, "thinking", thinkingTitle);
        next.activity = "thinking";
      }
      break;
    }
    case "text_delta": {
      const commentary = event.textPhase === "commentary";
      const kind = commentary ? "commentary" : "assistant";
      const title = commentary
        ? translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("progressUpdate")
        : "Azem";
      if (event.agentId) {
        next.agents = projectSubagentPreview(next.agents, event.agentId, event.runId ?? "", kind, event.text ?? "");
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = appendDelta(next.agentBlocks, event, kind, title);
        }
      } else {
        next.blocks = appendDelta(next.blocks, event, kind, title);
        next.activity = commentary ? "thinking" : "responding";
      }
      break;
    }
    case "tool_started":
    case "tool_update":
    case "tool_finished":
      if (event.agentId) {
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = updateTool(next.agentBlocks, event);
        }
      } else {
        next.blocks = updateTool(next.blocks, event);
        next.activity = event.kind === "tool_finished" ? "waiting_model" : "tool";
      }
      break;
    case "diff_ready":
      if (event.agentId) {
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = [...next.agentBlocks, {
            id: event.toolCallId || `diff-${event.sequence}`,
            kind: "diff",
            runId: event.runId,
            agentId: event.agentId,
            toolCallId: event.toolCallId,
            title: data.path || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("change"),
            content: event.text ?? "",
            state: event.state || "ready",
            data,
          }];
        }
      } else {
        next.blocks = [...next.blocks, {
          id: event.toolCallId || `diff-${event.sequence}`,
          kind: "diff",
          runId: event.runId,
          toolCallId: event.toolCallId,
          title: data.path || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("change"),
          content: event.text ?? "",
          state: event.state || "ready",
          data,
        }];
      }
      break;
    case "approval_requested":
      if (event.state === "reviewing") break;
      next.blocks = [...next.blocks, {
        id: event.approvalId || `approval-${event.sequence}`,
        kind: "approval",
        runId: event.runId,
        toolCallId: event.toolCallId,
        approvalId: event.approvalId,
        title: data.action || data.name || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("needApproval"),
        content: event.text || data.reason || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("approvalConfirm"),
        state: "pending",
        data,
      }];
      next.activity = "approval";
      break;
    case "approval_resolved":
      if (event.state?.startsWith("auto_")) next.blocks = next.blocks.filter((block) => block.approvalId !== event.approvalId);
      else next.blocks = next.blocks.map((block) => block.approvalId === event.approvalId ? { ...block, state: event.state || data.decision || "resolved" } : block);
      next.activity = "waiting_model";
      break;
    case "agent_state":
      if (event.agent) next.agents = upsertAgent(next.agents, normalizeAgent(event.agentId ?? "", event.agent, event.state, event.text));
      break;
    case "background_state":
      next.backgroundProcesses = (event.background ?? []).map(normalizeBackgroundProcess);
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
    case "context_usage":
      if (!event.sessionId || event.sessionId === next.currentSessionId) next.contextUsage = projectContextUsage(next.contextUsage, data, event.state);
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
      if (next.snapshot?.provider === provider && next.contextUsage.contextLimit === 0) {
        const contextLimit = next.modelsByProvider[provider]?.find((item) => item.id === next.snapshot?.model)?.contextWindow ?? 0;
        if (contextLimit > 0) next.contextUsage = { ...next.contextUsage, contextLimit };
      }
      break;
    }
    case "approval_mode":
      next.approvalMode = event.state || next.approvalMode;
      break;
    case "git_branches":
      next.branches = (event.gitBranches ?? []).map(normalizeBranch);
      next.workspaceDirty = Boolean(event.workspaceDirty);
      next.workspaceAdditions = numberValue(data.additions, 0);
      next.workspaceDeletions = numberValue(data.deletions, 0);
      next.workspaceChangedFiles = numberValue(data.changed_files, 0);
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
    case "run_failed": {
      const terminalState = event.kind === "run_finished" ? "completed" : event.kind === "run_cancelled" ? "cancelled" : "failed";
      const terminalRunId = event.runId || next.runId;
      const completedAt = Date.now();
      const elapsedMs = next.runStartedAt ? Math.max(0, completedAt - next.runStartedAt) : 0;
      next.running = false;
      next.activity = terminalState;
      if (terminalRunId) {
        next.blocks = next.blocks.map((block) => {
          if (block.runId !== terminalRunId) return block;
          const stamped = stampProcessElapsed(block, elapsedMs);
          // Settle streaming text, reasoning, and tools still active when the run ends.
          if (isLiveBlock(stamped)) {
            if (stamped.kind === "thinking" || stamped.kind === "commentary") {
              return settleTimedProcessBlock(stamped, terminalState, completedAt);
            }
            return { ...stamped, state: terminalState === "completed" && stamped.kind === "tool" ? "completed" : terminalState };
          }
          return stamped;
        });
        if (next.selectedAgentId) {
          next.agentBlocks = next.agentBlocks.map((block) => {
            if (!["streaming", "running", "started"].includes(block.state || "")) return block;
            return { ...block, state: terminalState === "completed" && block.kind === "tool" ? "completed" : terminalState };
          });
        }
        if (event.kind === "run_cancelled" && !next.blocks.some((block) => block.kind === "status" && block.runId === terminalRunId)) {
          next.blocks = [...next.blocks, {
            id: `status-cancelled-${terminalRunId}-${event.sequence || next.blocks.length}`,
            kind: "status",
            runId: terminalRunId,
            title: "run_cancelled",
            state: "cancelled",
            data: { elapsedMs: String(elapsedMs) },
          }];
        }
      }
      next.runId = "";
      if (event.kind === "run_failed") {
        next.error = event.text ?? "Run failed";
        next.blocks = [...next.blocks, { id: `error-${event.sequence}`, kind: "error", title: translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("runFailed"), content: event.text, state: "failed" }];
      }
      break;
    }
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
    pullRequestMonitors: new Map((snapshot.pullRequestMonitors ?? []).map((monitor) => [monitor.number, monitor])),
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
    pullRequestMonitors: new Map((snapshot.pullRequestMonitors ?? []).map((monitor) => [monitor.number, monitor])),
    branches: [{ name: "codex/gui-desktop-experience", current: true }, { name: "main", current: false }],
    agents: demoAgents(),
    skills: [{ name: "frontend-design", description: "Production UI design guidance", sourcePath: "~/.agents/skills/frontend-design", bundled: false, eager: true, disabled: false, resourceCount: 4 }],
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
    contextUsage: { inputTokens: 68_000, outputTokens: 4_000, contextLimit: 272_000, reported: true },
  };
}

function demoBlocks(review: boolean): Block[] {
  const blocks: Block[] = [
    { id: "user-demo", kind: "user", runId: "demo-run", content: "参考 Synara UI 与 Codex App，重新设计 Azem GUI，并保留现有 Agent 治理和会话恢复能力。", state: "submitted" },
    { id: "thinking-demo", kind: "thinking", runId: "demo-run", title: "思考中", content: "先检查现有事件模型和 TUI Block 结构，再建立桌面端的信息架构、状态投影和视觉规范。", state: "completed", data: { elapsedMs: "154000" } },
    { id: "commentary-demo", kind: "commentary", runId: "demo-run", title: "进度更新", content: "事件链已经确认，接下来核对桌面投影与会话恢复。", textPhase: "commentary", state: "completed" },
    { id: "tool-demo", kind: "tool", runId: "demo-run", title: "检查桌面事件投影", content: "internal/app/events.go · internal/app/event_broker.go · internal/session/service.go", state: "completed", data: { elapsedMs: "154000" } },
    { id: "assistant-demo", kind: "assistant", runId: "demo-run", content: "界面采用两种密度状态：新会话时居中输入；开始执行后 Composer 固定到底部，并显示会话时间线与右侧 Inspector。", textPhase: "final_answer", state: "completed" },
  ];
  if (review) {
    blocks.push(
      { id: "diff-demo", kind: "diff", runId: "demo-run", title: "internal/desktop/bridge.go", content: "@@ -18,6 +18,10 @@\n type Bridge struct {\n+  sequence atomic.Uint64\n+  emit EventEmitter\n }", state: "ready", data: { additions: "4", deletions: "0" } },
      { id: "approval-demo", kind: "approval", runId: "demo-run", approvalId: "approval-demo", title: "写入桌面入口", content: "创建 Wails v3 桌面入口并更新 Go 模块依赖。", state: "pending", data: { risk: "medium" } },
    );
  }
  return blocks;
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

function isLiveBlock(block: Block) {
  return ["streaming", "running", "started", "progress"].includes(block.state || "");
}

function settleTimedProcessBlock(block: Block, state = "completed", completedAt = Date.now()): Block {
  const startedAt = Number(block.data?.startedAt || 0);
  const data: Record<string, string> = {
    ...(block.data ?? {}),
    completedAt: String(completedAt),
  };
  if (Number.isFinite(startedAt) && startedAt > 0) {
    data.elapsedMs = String(Math.max(0, completedAt - startedAt));
  }
  return { ...block, state, data };
}

function settleActiveProcessText(
  blocks: Block[],
  event: RuntimeEvent,
  includeCommentary = true,
  state = "completed",
) {
  const completedAt = Date.now();
  return blocks.map((block) => (block.kind === "thinking" || (includeCommentary && block.kind === "commentary"))
    && block.runId === event.runId
    && block.agentId === event.agentId
    && isLiveBlock(block)
    ? settleTimedProcessBlock(block, state, completedAt)
    : block);
}

function appendDelta(
  blocks: Block[],
  event: RuntimeEvent,
  kind: "thinking" | "commentary" | "assistant",
  title: string,
): Block[] {
  if (kind === "commentary") blocks = settleActiveProcessText(blocks, event, false);
  if (kind === "assistant") blocks = settleActiveProcessText(blocks, event);
  const previousIndex = blocks.length - 1;
  const previous = blocks[previousIndex];
  const sameStream = previous?.kind === kind
    && previous.runId === event.runId
    && previous.agentId === event.agentId
    && isLiveBlock(previous);
  const chunk = event.text ?? "";
  if (!sameStream) {
    const stream = event.runId || event.agentId || "current";
    return [...blocks, {
      id: `${kind}-${stream}-${event.sequence || blocks.length + 1}`,
      kind,
      runId: event.runId,
      agentId: event.agentId,
      title,
      content: chunk,
      textPhase: event.textPhase,
      state: event.state || "streaming",
      data: kind !== "assistant"
        ? { ...(event.data ?? {}), startedAt: event.data?.startedAt || String(Date.now()) }
        : event.data,
    }];
  }
  return blocks.map((block, current) => current === previousIndex ? {
    ...block,
    content: kind === "thinking"
      ? joinThinkingContent(block.content ?? "", chunk)
      : `${block.content ?? ""}${chunk}`,
    textPhase: block.textPhase || event.textPhase,
    state: event.state || "streaming",
    title: block.title || title,
    data: event.data ? { ...(block.data ?? {}), ...event.data } : block.data,
  } : block);
}

/**
 * Models often emit discrete thinking blurbs as separate deltas, each wrapped in
 * **bold**. Naïve concatenation yields `**A****B**`; insert paragraph breaks so
 * markdown can render them as separate lines.
 */
function joinThinkingContent(existing: string, next: string) {
  if (!existing) return next;
  if (!next) return existing;
  const left = existing.replace(/[ \t]+$/u, "");
  const right = next.replace(/^[ \t]+/u, "");
  if (left.endsWith("**") && right.startsWith("**")) return `${left}\n\n${right}`;
  // New markdown block / list item arriving as a whole segment.
  if (!/\s$/u.test(left) && /^(?:#{1,6}\s|[-*+]\s|\d+\.\s)/u.test(right)) return `${left}\n\n${right}`;
  return existing + next;
}

function updateTool(blocks: Block[], event: RuntimeEvent): Block[] {
  const id = event.toolCallId || `tool-${event.sequence}`;
  const index = blocks.findIndex((block) => block.toolCallId === id || block.id === id);
  const data = event.data ?? {};
  if (event.kind === "tool_started") blocks = settleActiveProcessText(blocks, event);
  const text = event.text ?? "";
  const argumentsText = typeof data.arguments === "string" ? data.arguments : "";
  const nextState = event.kind === "tool_finished"
    ? event.state || "completed"
    : event.kind === "tool_started" || event.kind === "tool_update"
      ? "running"
      : event.state || "";

  if (index < 0) {
    // Seed content from arguments so the timeline can preview path/command while running.
    const content = text
      ? (argumentsText ? `${argumentsText}\n${text}` : text)
      : argumentsText;
    return [...blocks, {
      id, kind: "tool", runId: event.runId, agentId: event.agentId, toolCallId: id,
      title: data.name || "",
      content,
      state: nextState || "running",
      data: argumentsText ? { ...data, arguments: argumentsText } : data,
    }];
  }

  return blocks.map((block, current) => {
    if (current !== index) return block;
    const priorArgs = block.data?.arguments || "";
    const mergedArgs = argumentsText || priorArgs;
    let content = block.content || mergedArgs;
    if (text) {
      if (mergedArgs && (!content || content === mergedArgs)) content = `${mergedArgs}\n${text}`;
      else if (content && !content.endsWith(text)) {
        content = content.endsWith("\n") ? `${content}${text}` : `${content}\n${text}`;
      } else if (!content) content = text;
    } else if (!content && mergedArgs) {
      content = mergedArgs;
    }
    return {
      ...block,
      title: data.name || block.title,
      content,
      state: nextState || block.state,
      data: {
        ...block.data,
        ...data,
        ...(mergedArgs ? { arguments: mergedArgs } : {}),
      },
    };
  });
}

function normalizeSession(raw: Record<string, unknown>): Session {
  return {
    id: stringValue(raw, "id", "ID"), title: stringValue(raw, "title", "Title") || "New session",
    providerId: stringValue(raw, "providerId", "ProviderID"), modelId: stringValue(raw, "modelId", "ModelID"),
    reasoning: stringValue(raw, "reasoning", "Reasoning"), agentMode: stringValue(raw, "agentMode", "AgentMode"),
    pinned: Boolean(raw.pinned ?? raw.Pinned), archived: Boolean(raw.archived ?? raw.Archived), unread: Boolean(raw.unread ?? raw.Unread),
    updatedAt: stringValue(raw, "updatedAt", "UpdatedAt"),
  };
}

function normalizeBlock(raw: Record<string, unknown>, index: number): Block {
  const data = raw.data && typeof raw.data === "object" && !Array.isArray(raw.data)
    ? Object.fromEntries(Object.entries(raw.data as Record<string, unknown>).map(([key, value]) => [key, String(value ?? "")]))
    : undefined;
  const rawTextPhase = stringValue(raw, "textPhase", "TextPhase") || data?.phase || "";
  const textPhase = rawTextPhase === "commentary" || rawTextPhase === "final_answer" ? rawTextPhase : undefined;
  return {
    id: String(raw.id ?? raw.Sequence ?? `block-${index}`),
    kind: String(raw.kind ?? raw.Kind ?? "assistant") as Block["kind"],
    runId: stringValue(raw, "runId", "RunID"), agentId: stringValue(raw, "agentId", "AgentID"),
    toolCallId: stringValue(raw, "toolCallId", "ToolCallID"), title: stringValue(raw, "title", "Title"),
    content: stringValue(raw, "content", "Content"), textPhase,
    state: stringValue(raw, "state", "State"),
    collapsed: Boolean(raw.collapsed ?? raw.Collapsed),
    data,
    attachments: (raw.attachments ?? raw.Attachments) as Attachment[] | undefined,
  };
}

/** Rebuild the live timeline when loading a session: blocks + durable toolRecords. */
export function mergeSessionTranscript(
  blocks: Block[],
  blockSequences: unknown,
  toolRecords: unknown,
): Block[] {
  const sequences = Array.isArray(blockSequences)
    ? blockSequences.map((value) => Number(value)).map((value, index) => Number.isFinite(value) ? value : index)
    : blocks.map((_, index) => index);
  const tools = (Array.isArray(toolRecords) ? toolRecords : []).map((raw, index) => normalizeToolRecord(raw as Record<string, unknown>, index));
  tools.sort((left, right) => left.anchorSequence - right.anchorSequence
    || left.startedAt - right.startedAt
    || left.block.id.localeCompare(right.block.id));

  const byAnchor = new Map<number, Block[]>();
  for (const tool of tools) {
    const list = byAnchor.get(tool.anchorSequence) ?? [];
    list.push(tool.block);
    byAnchor.set(tool.anchorSequence, list);
  }

  const result: Block[] = [];
  const usedAnchors = new Set<number>();
  blocks.forEach((block, index) => {
    result.push(block);
    const sequence = sequences[index] ?? index;
    const anchored = byAnchor.get(sequence);
    if (!anchored) return;
    result.push(...anchored);
    usedAnchors.add(sequence);
  });
  for (const [anchor, list] of byAnchor) {
    if (!usedAnchors.has(anchor)) result.push(...list);
  }
  return result;
}

function normalizeToolRecord(raw: Record<string, unknown>, index: number) {
  const toolCallId = stringValue(raw, "toolCallId", "ToolCallID") || `tool-record-${index}`;
  const argumentsRaw = raw.arguments ?? raw.Arguments;
  const argumentText = typeof argumentsRaw === "string"
    ? argumentsRaw
    : argumentsRaw != null ? JSON.stringify(argumentsRaw) : "";
  const structuredRaw = raw.structured ?? raw.Structured;
  const structuredText = typeof structuredRaw === "string"
    ? structuredRaw
    : structuredRaw != null ? JSON.stringify(structuredRaw) : "";
  const content = stringValue(raw, "content", "Content");
  const startedAt = parseTimestamp(raw.startedAt ?? raw.StartedAt);
  const completedAt = parseTimestamp(raw.completedAt ?? raw.CompletedAt);
  const elapsedMs = startedAt && completedAt && completedAt >= startedAt ? completedAt - startedAt : 0;
  const state = stringValue(raw, "state", "State") || "completed";
  const data: Record<string, string> = {};
  if (elapsedMs > 0) data.elapsedMs = String(elapsedMs);
  if (startedAt > 0) data.startedAt = String(startedAt);
  if (completedAt > 0) data.completedAt = String(completedAt);
  if (structuredText) data.structured = structuredText;
  return {
    anchorSequence: numberValue(raw.anchorSequence ?? raw.AnchorSequence, -1),
    startedAt: startedAt || index,
    block: {
      id: `tool-${toolCallId}`,
      kind: "tool" as const,
      runId: stringValue(raw, "runId", "RunID"),
      toolCallId,
      title: stringValue(raw, "name", "Name"),
      content: [argumentText, content].filter(Boolean).join(argumentText && content ? "\n" : ""),
      state,
      data: Object.keys(data).length ? data : undefined,
    } satisfies Block,
  };
}

function parseTimestamp(value: unknown) {
  if (typeof value === "number" && Number.isFinite(value)) {
    // Go may emit unix nanos for unfinished paths; treat huge numbers as nanos.
    return value > 1e15 ? Math.floor(value / 1e6) : value > 1e12 ? value : value * 1000;
  }
  if (typeof value === "string" && value.trim()) {
    const parsed = Date.parse(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

function stampProcessElapsed(block: Block, elapsedMs: number): Block {
  if (!elapsedMs || (block.kind !== "thinking" && block.kind !== "tool")) return block;
  if (block.data?.elapsedMs) return block;
  return { ...block, data: { ...block.data, elapsedMs: String(elapsedMs) } };
}

function parseJSONValue(raw: unknown) {
  if (raw == null || raw === "") return [];
  if (Array.isArray(raw)) return raw;
  if (typeof raw !== "string") return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function normalizeAgentSnapshot(raw: Record<string, unknown>): AgentState {
  return normalizeAgent(stringValue(raw, "id", "ID"), (raw.agent ?? raw.Agent ?? {}) as Record<string, unknown>, stringValue(raw, "state", "State"), stringValue(raw, "summary", "Summary"));
}

function normalizeBackgroundProcess(raw: Record<string, unknown>): BackgroundProcess {
  return {
    id: stringValue(raw, "id", "ID"), name: stringValue(raw, "name", "Name"),
    command: stringValue(raw, "command", "Command"), cwd: stringValue(raw, "cwd", "CWD"),
    pid: numberValue(raw.pid ?? raw.PID), state: stringValue(raw, "state", "State"),
    exitCode: numberValue(raw.exitCode ?? raw.ExitCode), startedAt: stringValue(raw, "startedAt", "StartedAt"),
    finishedAt: stringValue(raw, "finishedAt", "FinishedAt") || undefined,
    error: stringValue(raw, "error", "Error") || undefined,
  };
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
    state, summary, preview: "", previewKind: "", previewRunId: "", elapsedObservedAt: Date.now(),
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
    sourcePath: stringValue(raw, "sourcePath", "SourcePath"), bundled: Boolean(raw.bundled ?? raw.Bundled),
    eager: Boolean(raw.eager ?? raw.Eager), disabled: Boolean(raw.disabled ?? raw.Disabled),
    resourceCount: numberValue(raw.resourceCount ?? raw.ResourceCount),
  };
}

function normalizeBranch(raw: Record<string, unknown>): GitBranch {
  return { name: stringValue(raw, "name", "Name"), current: Boolean(raw.current ?? raw.Current) };
}

function normalizeModel(raw: Record<string, unknown>): ModelOption {
  const id = stringValue(raw, "id", "ID");
  const contextWindow = numberValue(raw.contextWindow ?? raw.ContextWindow);
  return {
    id, name: [stringValue(raw, "name", "Name"), id].filter(Boolean)[0]!,
    reasoningLevels: ((raw.reasoningLevels ?? raw.ReasoningLevels ?? []) as unknown[]).map(String),
    defaultReasoning: stringValue(raw, "defaultReasoning", "DefaultReasoning"),
    ...(contextWindow > 0 ? { contextWindow } : {}),
  };
}

function emptyContextUsage(contextLimit = 0): ContextUsage {
  return { inputTokens: 0, outputTokens: 0, contextLimit, reported: false };
}

function parseContextUsage(raw: string | undefined, fallbackLimit = 0): ContextUsage {
  if (!raw) return emptyContextUsage(fallbackLimit);
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    const inputTokens = numberValue(value.inputTokens ?? value.InputTokens);
    const outputTokens = numberValue(value.outputTokens ?? value.OutputTokens);
    return {
      inputTokens,
      outputTokens,
      contextLimit: numberValue(value.contextLimit ?? value.ContextLimit, fallbackLimit) || fallbackLimit,
      reported: Boolean(value.currentTurnMainReported ?? value.CurrentTurnMainReported ?? inputTokens > 0),
    };
  } catch {
    return emptyContextUsage(fallbackLimit);
  }
}

function projectContextUsage(current: ContextUsage, data: Record<string, string>, state?: string): ContextUsage {
  if (data.factSnapshot === "true" && data.usageSnapshot) return parseContextUsage(data.usageSnapshot, current.contextLimit);
  const requestKind = data.requestKind || "main";
  if (requestKind !== "main" || data.aggregateOnly === "true") return current;
  return {
    inputTokens: data.inputTokens === undefined ? current.inputTokens : numberValue(data.inputTokens),
    outputTokens: data.outputTokens === undefined ? current.outputTokens : numberValue(data.outputTokens),
    contextLimit: data.contextLimit === undefined ? current.contextLimit : numberValue(data.contextLimit, current.contextLimit),
    reported: current.reported || state === "reported" || data.cacheStatus === "reported",
  };
}

function upsertAgent(agents: AgentState[], agent: AgentState): AgentState[] {
  const index = agents.findIndex((current) => current.id === agent.id);
  if (index < 0) return [...agents, agent];
  const now = Date.now();
  return agents.map((current, currentIndex) => {
    if (currentIndex !== index) return current;
    const elapsedAdvanced = agent.elapsedMs > current.elapsedMs;
    const becameTerminal = isSubagentActive(current.state) && isSubagentTerminal(agent.state);
    const terminalSummary = isSubagentTerminal(agent.state)
      && agent.summary
      && !/^(?:completed|failed|cancelled|canceled|interrupted)$/i.test(agent.summary)
      ? agent.summary
      : "";
    const elapsedMs = becameTerminal && !elapsedAdvanced
      ? Math.max(current.elapsedMs, current.elapsedMs + Math.max(0, now - current.elapsedObservedAt))
      : Math.max(current.elapsedMs, agent.elapsedMs);
    const elapsedObservedAt = isSubagentTerminal(agent.state)
      ? now
      : elapsedAdvanced
        ? agent.elapsedObservedAt
        : current.elapsedObservedAt || agent.elapsedObservedAt;
    // Partial agent_state payloads often omit counters and metadata; preserve
    // the richer projection accumulated from live child frames.
    return {
      ...current,
      ...agent,
      type: agent.type || current.type,
      description: agent.description || current.description,
      model: agent.model || current.model,
      capabilityMode: agent.capabilityMode || current.capabilityMode,
      isolation: agent.isolation || current.isolation,
      cwd: agent.cwd || current.cwd,
      activity: agent.activity || current.activity,
      warning: agent.warning || current.warning,
      worktreePath: agent.worktreePath || current.worktreePath,
      summary: agent.summary || current.summary,
      state: agent.state || current.state,
      toolCalls: Math.max(current.toolCalls, agent.toolCalls),
      turns: Math.max(current.turns, agent.turns),
      tokensUsed: Math.max(current.tokensUsed, agent.tokensUsed),
      elapsedMs,
      elapsedObservedAt,
      preview: terminalSummary || current.preview || agent.preview,
      previewKind: terminalSummary ? "assistant" : current.previewKind || agent.previewKind,
      previewRunId: current.previewRunId || agent.previewRunId,
    };
  });
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
