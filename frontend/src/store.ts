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
  MCPServerEntry,
  ModelRoute,
	ModelProvider,
  QueuedPrompt,
  PullRequest,
  PullRequestDashboard,
  PullRequestDetailResponse,
  PullRequestMonitorState,
  PluginEntry,
  Project,
  RuntimeEvent,
  Session,
  SessionSearchTarget,
  SettingsSearchTarget,
  SessionRecap,
  SkillEntry,
  Snapshot,
  TodoList,
  View,
} from "./types";

export interface ModelOption {
  id: string;
  name: string;
	disabled?: boolean;
	aliases?: string[];
  reasoningLevels: string[];
  defaultReasoning?: string;
  contextWindow?: number;
	capabilities?: string[];
	inputModalities?: string[];
	outputModalities?: string[];
}

function modelIDKey(id: string) {
	return id.trim().toLocaleLowerCase().replace(/^models\//, "").replace(/^~/, "").replaceAll("_", "-").split("/").at(-1) ?? "";
}

export function findModelOption(models: ModelOption[], id: string) {
	const key = modelIDKey(id);
	return models.find((model) => [model.id, ...(model.aliases ?? [])].some((candidate) => modelIDKey(candidate) === key));
}

export type UIFont = string;

export function normalizeUIFont(uiFont: string): UIFont {
  const font = uiFont.trim();
  return font === "system" || (font.length > 0 && font.length <= 128 && !/["\\;\u0000-\u001f]/.test(font)) ? font : "system";
}

export interface ContextUsage {
  inputTokens: number;
  outputTokens: number;
  contextLimit: number;
  reported: boolean;
  cacheInputTokens?: number;
  cachedInputTokens?: number;
  cacheWriteTokens?: number;
  uncachedInputTokens?: number;
  cacheReported?: boolean;
  mainCacheReported?: boolean;
  cacheWriteReported?: boolean;
}

export interface RuntimeData {
  snapshot: Snapshot | null;
  sessions: Session[];
  projects: Project[];
  currentSessionId: string;
  currentTitle: string;
  blocks: Block[];
  agents: AgentState[];
  backgroundProcesses: BackgroundProcess[];
  selectedAgentId: string;
  agentBlocks: Block[];
  agentCatalog: AgentCatalogEntry[];
  skills: SkillEntry[];
  mcpServers: MCPServerEntry[];
  plugins: PluginEntry[];
  branches: GitBranch[];
  modelRoutes: ModelRoute[];
	modelProviders: ModelProvider[];
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
  recap: SessionRecap | null;
  recovery: Array<Record<string, unknown>>;
  runId: string;
  running: boolean;
  globalRunId: string;
  globalRunSessionId: string;
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
  settingsTarget: SettingsSearchTarget | null;
  commandOpen: boolean;
  sessionSearchTarget: SessionSearchTarget | null;
  planMode: boolean;
  attachments: Attachment[];
  // Follow-up queues are process-local but session-scoped, matching Codex navigation behavior.
  queuedPrompts: QueuedPrompt[];
  queuePauseReasons: Record<string, "interrupted">;
  theme: "system" | "light" | "dark";
  uiFont: UIFont;
  uiFontSize: number;
}

interface RuntimeActions {
  hydrate: (snapshot: Snapshot, demo?: boolean) => void;
  applyEvents: (events: RuntimeEvent[]) => void;
  setView: (view: View) => void;
  startLocalDraft: () => void;
  setInspectorTab: (tab: InspectorTab) => void;
  setInspectorOpen: (open: boolean) => void;
  selectAgent: (agentId: string) => void;
  setSettingsOpen: (open: boolean, target?: SettingsSearchTarget) => void;
  setCommandOpen: (open: boolean) => void;
  setSessionSearchTarget: (target: SessionSearchTarget | null) => void;
  setPullRequestDashboard: (dashboard: PullRequestDashboard) => void;
  selectPullRequest: (number: number | null) => void;
  setPullRequestDetail: (response: PullRequestDetailResponse) => void;
  setPullRequestLoading: (loading: boolean) => void;
  setPullRequestMutating: (mutating: boolean) => void;
  setPullRequestError: (message: string) => void;
  updatePullRequestMonitor: (monitor: PullRequestMonitorState) => void;
  setPlanMode: (enabled: boolean) => void;
  setTheme: (theme: RuntimeData["theme"]) => void;
  setUIFont: (uiFont: UIFont) => void;
  setUIFontSize: (uiFontSize: number) => void;
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
  removeQueuedPrompt: (sessionId: string, id: string) => void;
  updateQueuedPrompt: (sessionId: string, id: string, text: string, attachments: Attachment[]) => void;
  failQueuedPrompt: (sessionId: string, id: string, message: string) => void;
  retryQueuedPrompt: (sessionId: string, id: string) => void;
  reorderQueuedPrompt: (sessionId: string, id: string, beforeId: string) => void;
  resumeQueuedPrompts: (sessionId: string) => void;
}

const initialData: RuntimeData = {
  snapshot: null,
  sessions: [],
  projects: [],
  currentSessionId: "",
  currentTitle: "",
  blocks: [],
  agents: [],
  backgroundProcesses: [],
  selectedAgentId: "",
  agentBlocks: [],
  agentCatalog: [],
  skills: [],
  mcpServers: [],
  plugins: [],
  branches: [],
  modelRoutes: [],
	modelProviders: [],
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
  recap: null,
  recovery: [],
  runId: "",
  running: false,
  runStartedAt: 0,
  globalRunId: "",
  globalRunSessionId: "",
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
  settingsTarget: null,
  commandOpen: false,
  sessionSearchTarget: null,
  planMode: false,
  attachments: [],
  queuedPrompts: [],
  queuePauseReasons: {},
  theme: "system",
  uiFont: "system",
  uiFontSize: 14,
};

export const useRuntimeStore = create<RuntimeData & RuntimeActions>((set) => ({
  ...initialData,
  hydrate: (snapshot, demo = false) => set((state) => {
    const hydrated = hydrateData(snapshot, demo);
    if (!demo && state.branches.length === 0 && snapshot.currentBranch) {
      hydrated.branches = [{ name: snapshot.currentBranch, current: true }];
    }
    return { ...state, ...hydrated };
  }),
  applyEvents: (events) => set((state) => reduceEvents(state, events)),
  setView: (view) => set({ view, commandOpen: false }),
  startLocalDraft: () => set({
    currentSessionId: "",
    currentTitle: "",
    blocks: [],
    running: false,
    globalRunId: "",
    globalRunSessionId: "",
    runId: "",
    runStartedAt: 0,
    activity: "",
    error: "",
    selectedAgentId: "",
    agentBlocks: [],
    recap: null,
    selectedPullRequestNumber: null,
    pullRequestDetail: null,
    view: "thread",
  }),
  setInspectorTab: (inspectorTab) => set({ inspectorTab, inspectorOpen: true }),
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  selectAgent: (selectedAgentId) => set((state) => ({
    selectedAgentId,
    selectedPullRequestNumber: selectedAgentId ? null : state.selectedPullRequestNumber,
    agentBlocks: selectedAgentId && selectedAgentId === state.selectedAgentId ? state.agentBlocks : [],
    inspectorTab: selectedAgentId ? "agents" : state.inspectorTab,
  })),
  setSettingsOpen: (settingsOpen, settingsTarget) => set({
    settingsOpen,
    settingsTarget: settingsOpen ? settingsTarget ?? null : null,
    commandOpen: false,
  }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setSessionSearchTarget: (sessionSearchTarget) => set({ sessionSearchTarget }),
  setPullRequestDashboard: (pullRequestDashboard) => set({ pullRequestDashboard, pullRequestLoading: false, pullRequestError: "" }),
  selectPullRequest: (selectedPullRequestNumber) => set((state) => ({
    selectedPullRequestNumber,
    pullRequestDetail: state.pullRequestDetail?.number === selectedPullRequestNumber ? state.pullRequestDetail : null,
    selectedAgentId: selectedPullRequestNumber ? "" : state.selectedAgentId,
    agentBlocks: selectedPullRequestNumber ? [] : state.agentBlocks,
    pullRequestLoading: selectedPullRequestNumber ? state.pullRequestLoading : false,
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
  setUIFont: (uiFont) => set({ uiFont: normalizeUIFont(uiFont) }),
  setUIFontSize: (uiFontSize) => set({ uiFontSize: Math.min(20, Math.max(11, Math.round(uiFontSize))) }),
  setLanguage: (language) => set((state) => ({
    snapshot: state.snapshot ? { ...state.snapshot, language } : state.snapshot,
  })),
  setSessionModel: (provider, model, reasoning) => set((state) => {
    const modelChanged = state.snapshot?.provider !== provider || state.snapshot?.model !== model;
    const contextLimit = findModelOption(state.modelsByProvider[provider] ?? [], model)?.contextWindow ?? 0;
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
            workspace: state.snapshot.workspace,
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
  enqueuePrompt: (text, attachments) => set((state) => {
    const sessionId = state.currentSessionId || state.snapshot?.sessionId || "";
    return {
      queuedPrompts: [...state.queuedPrompts, {
        id: crypto.randomUUID(), sessionId, text, attachments: [...attachments], state: "queued",
      }],
    };
  }),
  removeQueuedPrompt: (sessionId, id) => set((state) => {
    const removed = state.queuedPrompts.find((item) => item.id === id && item.sessionId === sessionId);
    if (!removed) return {};
    const queuedPrompts = state.queuedPrompts.filter((item) => item.id !== id || item.sessionId !== sessionId);
    if (queuedPrompts.some((item) => item.sessionId === sessionId)) return { queuedPrompts };
    const queuePauseReasons = { ...state.queuePauseReasons };
    delete queuePauseReasons[sessionId];
    return { queuedPrompts, queuePauseReasons };
  }),
  updateQueuedPrompt: (sessionId, id, text, attachments) => set((state) => ({
    queuedPrompts: state.queuedPrompts.map((item) => item.id === id && item.sessionId === sessionId
      ? { ...item, text, attachments: [...attachments], state: "queued", error: undefined }
      : item),
  })),
  failQueuedPrompt: (sessionId, id, message) => set((state) => ({
    queuedPrompts: state.queuedPrompts.map((item) => item.id === id && item.sessionId === sessionId
      ? { ...item, state: "failed", error: message }
      : item),
  })),
  retryQueuedPrompt: (sessionId, id) => set((state) => ({
    queuedPrompts: state.queuedPrompts.map((item) => item.id === id && item.sessionId === sessionId
      ? { ...item, state: "queued", error: undefined }
      : item),
  })),
  reorderQueuedPrompt: (sessionId, id, beforeId) => set((state) => {
    const source = state.queuedPrompts.find((item) => item.id === id);
    if (!source || source.sessionId !== sessionId) return {};
    return { queuedPrompts: reorderSessionQueue(state.queuedPrompts, id, beforeId) };
  }),
  resumeQueuedPrompts: (sessionId) => set((state) => {
    const queuePauseReasons = { ...state.queuePauseReasons };
    delete queuePauseReasons[sessionId];
    return { queuePauseReasons };
  }),
}));

export function reorderSessionQueue(items: QueuedPrompt[], id: string, beforeId: string): QueuedPrompt[] {
  if (id === beforeId) return items;
  const source = items.find((item) => item.id === id);
  const target = items.find((item) => item.id === beforeId);
  if (!source || !target || source.sessionId !== target.sessionId) return items;
  const scoped = items.filter((item) => item.sessionId === source.sessionId);
  const from = scoped.findIndex((item) => item.id === id);
  const to = scoped.findIndex((item) => item.id === beforeId);
  if (from < 0 || to < 0) return items;
  const [moved] = scoped.splice(from, 1);
  scoped.splice(to, 0, moved!);
  let index = 0;
  return items.map((item) => item.sessionId === source.sessionId ? scoped[index++]! : item);
}

const SESSION_SCOPED_EVENTS: Record<string, true> = {
  run_started: true,
  provider_retry: true,
  thinking_delta: true,
  text_delta: true,
  tool_started: true,
  tool_update: true,
  tool_finished: true,
  diff_ready: true,
  approval_requested: true,
  approval_resolved: true,
  user_input_requested: true,
  user_input_resolved: true,
  plan_proposed: true,
  plan_resolved: true,
  agent_state: true,
  context_profile: true,
  context_usage: true,
  todo_updated: true,
  recap_state: true,
  run_finished: true,
  run_cancelled: true,
  run_failed: true,
};
const UNREAD_SESSION_TERMINALS = new Set(["run_finished", "run_failed"]);

export function reduceEvents<T extends RuntimeData>(state: T, events: RuntimeEvent[]): T {
  let next = { ...state, blocks: [...state.blocks], sessions: [...state.sessions], agents: [...state.agents] };
  for (const event of events) next = reduceEvent(next, event);
  return next;
}

export const shouldMarkSessionUnread = (
  state: Pick<RuntimeData, "currentSessionId" | "globalRunId" | "globalRunSessionId">,
  event: RuntimeEvent,
) => [!event.agentId, Boolean(event.sessionId), event.sessionId !== state.currentSessionId,
  UNREAD_SESSION_TERMINALS.has(event.kind), Boolean(state.globalRunId), event.sessionId === state.globalRunSessionId,
  ["starting", event.runId ?? state.globalRunId].includes(state.globalRunId)].every(Boolean);

function reduceEvent<T extends RuntimeData>(state: T, event: RuntimeEvent): T {
  if (event.sequence && event.sequence <= state.lastSequence) return state;
  const markSessionUnread = shouldMarkSessionUnread(state, event);
  let next = { ...state, lastSequence: Math.max(state.lastSequence, event.sequence || 0) };
  const data = event.data ?? {};
  // The backend permits only one main run process-wide. Track that lifecycle before
  // filtering session-scoped events so a queue in another session waits instead of
  // attempting a concurrent turn and losing its prompt.
  if (!event.agentId && event.kind === "run_started" && event.sessionId) {
    next.globalRunId = event.runId ?? "starting";
    next.globalRunSessionId = event.sessionId;
  }
  if (!event.agentId && (event.kind === "run_finished" || event.kind === "run_cancelled" || event.kind === "run_failed")) {
    const isGlobalTerminal = Boolean(next.globalRunId) && (
      event.runId === next.globalRunId ||
      (next.globalRunId === "starting" && event.sessionId === next.globalRunSessionId)
    );
    if (isGlobalTerminal) {
      next.globalRunId = "";
      next.globalRunSessionId = "";
    }
    if (event.kind === "run_cancelled" && event.sessionId && next.queuedPrompts.some((item) => item.sessionId === event.sessionId)) {
      next.queuePauseReasons = { ...next.queuePauseReasons, [event.sessionId]: "interrupted" };
    }
  }
  if (markSessionUnread) {
    next.sessions = next.sessions.map((session) => session.id === event.sessionId ? { ...session, unread: true } : session);
  }
  if (event.sessionId && event.sessionId !== state.currentSessionId && SESSION_SCOPED_EVENTS[event.kind]) return next;


  switch (event.kind) {
    case "bootstrap_done":
      if (next.snapshot && event.text) next.snapshot = { ...next.snapshot, workspace: event.text };
      break;
    case "session_loaded":
      if (event.state === "list") {
        const loadedSessions = parseArray(data.sessions).map(normalizeSession);
        const loadedProjects = parseArray(data.projects).map(normalizeProject);
        const optimisticCurrent = next.running ? next.sessions.find((item) => item.id === next.currentSessionId) : undefined;
        next.sessions = optimisticCurrent && !loadedSessions.some((item) => item.id === optimisticCurrent.id)
          ? [optimisticCurrent, ...loadedSessions]
          : loadedSessions;
        next.projects = loadedProjects;
        next.currentTitle = next.sessions.find((item) => item.id === next.currentSessionId)?.title ?? next.currentTitle;
      } else if (event.sessionId) {
        next.currentSessionId = event.sessionId;
        next.sessions = next.sessions.map((session) => session.id === event.sessionId ? { ...session, unread: false } : session);
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
        const latestPlan = [...next.blocks].reverse().find((block) => block.kind === "plan");
        next.planMode = latestPlan?.state === "proposed";
        next.runId = data.activeRunID || data.lastRunID || "";
        next.running = data.active === "true";
        next.globalRunId = data.globalActiveRunID || (next.running ? next.runId : "");
        next.globalRunSessionId = data.globalActiveSessionID || (next.running ? event.sessionId : "");
        next.runStartedAt = next.running ? Date.now() : 0;
        next.activity = next.running ? "waiting_model" : "";
        next.error = "";
        next.selectedAgentId = "";
        next.agentBlocks = [];
        next.currentTitle = next.sessions.find((item) => item.id === event.sessionId)?.title ?? next.currentTitle;
        next.agents = (event.agentSnapshots ?? []).map(normalizeAgentSnapshot);
        next.todo = event.todo ?? null;
        next.recap = event.recap ?? null;
        next.attachments = [];
        next.contextProfile = null;
		const contextLimit = findModelOption(next.modelsByProvider[data.provider] ?? [], data.model)?.contextWindow ?? 0;
        next.contextUsage = parseContextUsage(data.usage, contextLimit);
        if ((data.provider === "chatgpt" || data.provider === "grok") && contextLimit > 0) {
          next.contextUsage.contextLimit = contextLimit;
        }
      }
      break;
    case "run_started":
      next.running = true;
      next.runId = event.runId ?? next.runId;
      next.runStartedAt = Date.now();
      next.activity = "waiting_model";
      break;
    case "provider_retry":
      if (event.state === "restarted") {
        if (event.agentId) {
          if (event.agentId === next.selectedAgentId) next.agentBlocks = discardUncommittedAttempt(next.agentBlocks, event.runId);
          next.agents = next.agents.map((agent) => agent.id === event.agentId
            ? { ...agent, preview: "", previewKind: "", previewRunId: "" }
            : agent);
        } else {
          next.blocks = discardUncommittedAttempt(next.blocks, event.runId);
        }
        next.activity = "waiting_model";
      }
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
      // Unphased provider text is the visible answer unless a later tool
      // boundary proves it was commentary. Keeping the natural-stop stream in
      // one assistant block prevents the final body from jumping out of the
      // process rail when the run finishes.
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
    case "user_input_requested":
      next.blocks = [...next.blocks, {
        id: event.userInputId || `question-${event.sequence}`,
        kind: "question",
        runId: event.runId,
        toolCallId: event.toolCallId,
        userInputId: event.userInputId,
        title: data.title || (next.snapshot?.language === "en" ? "Need your input" : "需要你的选择"),
        state: event.state || "pending",
        data,
      }];
      next.activity = "input";
      break;
    case "user_input_resolved":
      next.blocks = next.blocks.map((block) => block.userInputId === event.userInputId
        ? { ...block, state: event.state || "answered", data: { ...(block.data ?? {}), ...data } }
        : block);
      next.activity = "waiting_model";
      break;
    case "plan_proposed":
      next.blocks = next.blocks.map((block) => block.kind === "plan" && block.state === "proposed"
        ? { ...block, state: "superseded" }
        : block);
      next.blocks = [...next.blocks, {
        id: event.planId || `plan-${event.sequence}`,
        kind: "plan",
        runId: event.runId,
        toolCallId: event.toolCallId,
        planId: event.planId,
        title: data.title || (next.snapshot?.language === "en" ? "Implementation plan" : "实施计划"),
        content: event.text || "",
        state: event.state || "proposed",
        data,
      }];
      next.planMode = true;
      next.activity = "review";
      break;
    case "plan_resolved":
      next.blocks = next.blocks.map((block) => block.planId === event.planId
        ? { ...block, state: event.state === "executing" ? "approved" : (event.state || "approved") }
        : block);
      next.planMode = false;
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
    case "plugin_catalog":
      next.plugins = (event.pluginCatalog ?? []).map(normalizePlugin);
      break;
    case "mcp_state": {
      if (event.state === "snapshot") {
        next.mcpServers = parseMCPServers(data.servers);
        break;
      }
      const name = data.server?.trim();
      if (!name) break;
      const index = next.mcpServers.findIndex((server) => server.name === name);
      if (index < 0) break;
      next.mcpServers = next.mcpServers.map((server, serverIndex) => serverIndex === index ? {
        ...server,
        state: data.state || event.state || server.state,
        error: data.error || event.text || "",
      } : server);
      break;
    }
    case "context_profile":
      next.contextProfile = event.contextProfile ?? null;
      break;
    case "context_usage":
      if (!event.sessionId || event.sessionId === next.currentSessionId) next.contextUsage = projectContextUsage(next.contextUsage, data, event.state);
      break;
    case "todo_updated":
      next.todo = event.todo ?? null;
      break;
    case "recap_state":
      if (event.recap) next.recap = event.recap;
      break;
    case "model_routes":
      next.modelRoutes = event.modelRoutes ?? [];
      if (next.snapshot) next.snapshot = {
        ...next.snapshot,
        subagentConcurrency: numberValue(data.subagent_max_concurrency, next.snapshot.subagentConcurrency),
        shellConcurrency: numberValue(data.shell_max_concurrency, next.snapshot.shellConcurrency ?? 2),
        subagentAwaitSeconds: numberValue(data.subagent_await_seconds, next.snapshot.subagentAwaitSeconds ?? 600),
        chatgptFastMode: data.chatgpt_fast_mode === "true",
      };
      break;
	case "model_providers":
	  next.modelProviders = (event.modelProviders ?? []).map((provider) => ({ ...provider, Models: provider.Models ?? [] }));
	  for (const provider of next.modelProviders) {
		if (provider.Enabled && provider.Models.length > 0) next.modelsByProvider = {
		  ...next.modelsByProvider,
		  [provider.ID]: provider.Models.map((model) => normalizeModel(model as unknown as Record<string, unknown>)),
		};
	  }
	  break;
    case "model_catalog": {
      const provider = data.provider || "unknown";
      next.modelsByProvider = { ...next.modelsByProvider, [provider]: parseArray(data.models).map(normalizeModel) };
      if (next.snapshot?.provider === provider) {
        const contextLimit = findModelOption(next.modelsByProvider[provider] ?? [], next.snapshot?.model ?? "")?.contextWindow ?? 0;
        const subscription = provider === "chatgpt" || provider === "grok";
        if (contextLimit > 0 && (subscription || next.contextUsage.contextLimit === 0)) next.contextUsage = { ...next.contextUsage, contextLimit };
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
            return { ...stamped, state: stamped.kind === "tool" ? (terminalState === "cancelled" ? "cancelled" : "failed") : terminalState };
          }
          return stamped;
        });
        if (next.selectedAgentId) {
          next.agentBlocks = next.agentBlocks.map((block) => {
            if (!isLiveBlock(block)) return block;
            return { ...block, state: block.kind === "tool" ? (terminalState === "cancelled" ? "cancelled" : "failed") : terminalState };
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
      // Cross-session cancellation pause state is projected before session filtering.
      next.runId = "";
      if (event.kind === "run_failed") {
        next.error = "";
        const language = next.snapshot?.language === "en" ? "en" : "zh-CN";
        const failedText = event.text ?? "";
        const truncated = /token limit|output reached|max_turns|max_output|truncated/i.test(failedText);
        next.blocks = [...next.blocks, {
          id: `error-${event.sequence}`,
          kind: "error",
          title: translator(language)(truncated ? "outputTruncated" : "runFailed"),
          content: event.text,
          state: "failed",
        }];
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
      SessionID: snapshot.sessionId,
      Anchor: snapshot.workspace,
      CoveredBoundary: "run-demo",
      Goal: "完成桌面运行时体验优化",
      Summary: "核心交互已完成，正在执行最终验证并整理交付证据。",
      OpenItems: "in_progress: 运行完整前端与桌面测试",
      Revision: 3,
      UpdatedAt: new Date().toISOString(),
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
    plugins: [
      { id: "waza@demo", name: "waza", displayName: "Waza", version: "3.33.0", marketplace: "openai", origin: "codex", description: "工程健康、研究、UI 与写作工作流", developerName: "OpenAI", category: "Developer Tools", brandColor: "#3278ef", logoPath: "", enabled: true, skillCount: 6, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "MCP"], status: "ready", warning: "" },
      { id: "github@demo", name: "github", displayName: "GitHub", version: "0.1.9", marketplace: "openai", origin: "codex", description: "仓库、PR、Issue、Review 与 CI", developerName: "GitHub", category: "Developer Tools", brandColor: "#181717", logoPath: "", enabled: true, skillCount: 2, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: true, capabilities: ["Skills", "MCP"], status: "degraded", warning: "OAuth 等待授权" },
      { id: "kami@demo", name: "kami", displayName: "Kami", version: "1.12.0", marketplace: "kami", origin: "codex", description: "文档与产品页面排版", developerName: "Kami", category: "Productivity", brandColor: "#6d5efc", logoPath: "", enabled: true, skillCount: 1, mcpServerCount: 0, integratedMCPCount: 0, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills"], status: "ready", warning: "" },
      { id: "custom@demo", name: "custom", displayName: "Custom Toolkit", version: "0.8.0", marketplace: "local", origin: "local", description: "包含未信任的生命周期 Hooks", developerName: "Azem", category: "Local", brandColor: "#ff6a3d", logoPath: "", enabled: true, skillCount: 3, mcpServerCount: 0, integratedMCPCount: 0, hookCount: 2, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "Hooks"], status: "degraded", warning: "Hooks 等待显式信任" },
      { id: "disabled@demo", name: "disabled", displayName: "实验扩展", version: "0.1.0", marketplace: "local", origin: "local", description: "未启用的实验能力", developerName: "Azem", category: "Experimental", brandColor: "#8b8b84", logoPath: "", enabled: false, skillCount: 1, mcpServerCount: 1, integratedMCPCount: 1, hookCount: 0, hooksTrusted: false, hasApp: false, capabilities: ["Skills", "MCP"], status: "disabled", warning: "" },
    ],
    modelRoutes: [
      { Scope: "main", Role: "", Label: "主会话", Route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { Scope: "plan", Role: "", Label: "规划", Route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { Scope: "approval", Role: "", Label: "审批", Route: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" } },
      { Scope: "vision", Role: "", Label: "视觉", Route: { provider: "chatgpt", model: "gpt-5.6", reasoning: "high" } },
      { Scope: "compaction", Role: "", Label: "上下文压缩", Route: { provider: "chatgpt", model: "gpt-5.3-spark", reasoning: "low" } },
      { Scope: "recap", Role: "", Label: "会话回顾", Route: { provider: "chatgpt", model: "gpt-5.6-luna", reasoning: "low" } },
      { Scope: "subagent", Role: "research", Label: "Research", Route: { provider: "chatgpt", model: "gpt-5.3-spark", reasoning: "medium" } },
      { Scope: "subagent", Role: "review", Label: "Review", Route: { provider: "chatgpt", model: "gpt-5.5-codex", reasoning: "high" } },
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
      semanticRevision: 12,
      writerLag: 0,
      segments: Array.from({ length: 8 }, (_, index) => ({
        kind: index < 3 ? "canonical_turn" : "semantic_state",
        mandatory: index < 3,
        token_estimate: index < 3 ? 4200 : 2100,
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
      cacheInputTokens: 96_000, cachedInputTokens: 62_000, cacheWriteTokens: 9_400,
      cacheReported: true, cacheWriteReported: true,
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
    { id: "progress-structure", kind: "commentary", runId: "demo-run", title: "progress", content: "**读取当前前端结构**\nApp、Sidebar、Timeline 与 Inspector", textPhase: "commentary", state: "completed", data: { startedAt: "1000", completedAt: "1100" } },
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
  return [
    {
      ID: "chatgpt", DisplayName: "ChatGPT", Backend: "subscription", DefaultBaseURL: "", BaseURL: "", EnvKey: "", Enabled: true,
      CredentialConfigured: true, CredentialSource: "stored", Subscription: true, AccountLabel: "viking@example.com", AccountPlan: "Pro 20x",
      QuotaAvailable: true, QuotaUsedPercent: 61.5, QuotaResetsAt: 1786492800, QuotaBalance: "US$12.50", ModelsDevID: "openai", ModelsSource: "subscription",
      Models: [
        { id: "gpt-5.6", name: "GPT-5.6", contextWindow: 272000, reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools", "fast"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "gpt-5.5-codex", name: "GPT-5.5 Codex", contextWindow: 272000, reasoningLevels: ["low", "medium", "high", "xhigh"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text"], outputModalities: ["text"] },
        { id: "gpt-5.3-spark", name: "GPT-5.3 Spark", contextWindow: 128000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "medium", capabilities: ["tools", "fast"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
    {
      ID: "grok", DisplayName: "Grok", Backend: "subscription", DefaultBaseURL: "", BaseURL: "", EnvKey: "", Enabled: true,
      CredentialConfigured: true, CredentialSource: "stored", Subscription: true, AccountLabel: "viking@example.com", AccountPlan: "Plus",
      QuotaAvailable: true, QuotaUsedPercent: 28, QuotaBalance: "无限额度", QuotaUnlimited: true, ModelsDevID: "xai", ModelsSource: "subscription",
      Models: [
        { id: "grok-4.20", name: "Grok 4.20", contextWindow: 256000, reasoningLevels: ["low", "medium", "high"], defaultReasoning: "high", capabilities: ["reasoning", "tools"], inputModalities: ["text", "image"], outputModalities: ["text"] },
        { id: "grok-code-fast", name: "Grok Code Fast", contextWindow: 128000, reasoningLevels: ["low", "medium"], defaultReasoning: "medium", capabilities: ["tools"], inputModalities: ["text"], outputModalities: ["text"] },
      ],
    },
    {
      ID: "openrouter", DisplayName: "OpenRouter", Backend: "openai_compat", DefaultBaseURL: "https://openrouter.ai/api/v1", BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY", Enabled: true,
      CredentialConfigured: true, CredentialSource: "stored", ModelsDevID: "openrouter", ModelsSource: "provider_api+models.dev", Models: [
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

function isLiveBlock(block: Block) {
  return ["queued", "awaiting_approval", "reviewing_approval", "streaming", "running", "started", "progress"].includes(block.state || "");
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

function discardUncommittedAttempt(blocks: Block[], runId?: string): Block[] {
  let boundary = blocks.length;
  while (boundary > 0) {
    const block = blocks[boundary - 1]!;
    if (block.runId !== runId || !["thinking", "commentary", "assistant"].includes(block.kind)) break;
    boundary--;
  }
  return boundary === blocks.length ? blocks : blocks.slice(0, boundary);
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
      data: { ...(event.data ?? {}), startedAt: event.data?.startedAt || String(Date.now()) },
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
 * Models often emit discrete thinking blurbs as separate deltas, each wrapped
 * in ** markers. Naïve concatenation yields `**A****B**`; insert paragraph
 * breaks so the timeline can present each blurb as a separate plain-text step.
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
  if (event.kind === "tool_started") {
    blocks = settleActiveProcessText(blocks, event);
    const completedAt = Date.now();
    blocks = blocks.map((block): Block => block.kind === "assistant"
      && block.runId === event.runId
      && block.agentId === event.agentId
      && isLiveBlock(block)
      ? { ...settleTimedProcessBlock(block, "completed", completedAt), kind: "commentary", title: "progress", textPhase: "commentary" }
      : block);
  }
  const text = event.text ?? "";
  const argumentsText = typeof data.arguments === "string" ? data.arguments : "";
  const pendingState = ["queued", "awaiting_approval", "reviewing_approval"].includes(event.state || "")
    ? event.state || ""
    : "";
  const finishedState = event.kind === "tool_update" && event.state === "finished"
    ? (data.status === "stopped" || Boolean(data.reason) || (data.exit_code !== undefined && data.exit_code !== "0") ? "failed" : "completed")
    : "";
  const nextState = event.kind === "tool_finished"
    ? event.state || "completed"
    : pendingState || finishedState || (event.kind === "tool_started" || event.kind === "tool_update" ? "running" : event.state || "");

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
    id: stringValue(raw, "id", "ID"), workspace: stringValue(raw, "workspace", "Workspace"), title: stringValue(raw, "title", "Title") || "New session",
    providerId: stringValue(raw, "providerId", "ProviderID"), modelId: stringValue(raw, "modelId", "ModelID"),
    reasoning: stringValue(raw, "reasoning", "Reasoning"), agentMode: stringValue(raw, "agentMode", "AgentMode"),
    pinned: Boolean(raw.pinned ?? raw.Pinned), archived: Boolean(raw.archived ?? raw.Archived), unread: Boolean(raw.unread ?? raw.Unread),
    updatedAt: stringValue(raw, "updatedAt", "UpdatedAt"),
  };
}

function normalizeProject(raw: Record<string, unknown>): Project {
  return { workspace: stringValue(raw, "workspace", "Workspace"), updatedAt: stringValue(raw, "updatedAt", "UpdatedAt") };
}

function normalizeAttachment(raw: unknown): Attachment | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const item = raw as Record<string, unknown>;
  return {
    id: stringValue(item, "id", "ID"),
    name: stringValue(item, "name", "Name"),
    mimeType: stringValue(item, "mimeType", "MIMEType") || stringValue(item, "mime", "MIME"),
    path: stringValue(item, "path", "Path"),
    size: Number(item.size ?? item.Size ?? 0),
  };
}

function normalizeAttachments(raw: unknown): Attachment[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  return raw.map(normalizeAttachment).filter((item): item is Attachment => item !== null);
}

function normalizeBlock(raw: Record<string, unknown>, index: number): Block {
  const rawData = raw.data ?? raw.Data;
  const normalizedData = rawData && typeof rawData === "object" && !Array.isArray(rawData)
    ? Object.fromEntries(Object.entries(rawData as Record<string, unknown>).map(([key, value]) => [key, String(value ?? "")]))
    : undefined;
  const contentBytes = Number(raw.contentBytes ?? raw.ContentBytes ?? 0);
  const contentTruncated = Boolean(raw.contentTruncated ?? raw.ContentTruncated);
  const data = contentBytes > 0 || contentTruncated
    ? { ...(normalizedData ?? {}), contentBytes: String(contentBytes), contentTruncated: String(contentTruncated) }
    : normalizedData;
  const rawTextPhase = stringValue(raw, "textPhase", "TextPhase") || data?.phase || "";
  const textPhase = rawTextPhase === "commentary" || rawTextPhase === "final_answer" ? rawTextPhase : undefined;
  return {
    id: String(raw.id ?? raw.Sequence ?? `block-${index}`),
    kind: String(raw.kind ?? raw.Kind ?? "assistant") as Block["kind"],
    runId: stringValue(raw, "runId", "RunID"), agentId: stringValue(raw, "agentId", "AgentID"),
    toolCallId: stringValue(raw, "toolCallId", "ToolCallID"), title: stringValue(raw, "title", "Title"),
    approvalId: stringValue(raw, "approvalId", "ApprovalID") || data?.approvalId,
    userInputId: stringValue(raw, "userInputId", "UserInputID") || data?.userInputId,
    planId: stringValue(raw, "planId", "PlanID") || data?.planId,
    content: stringValue(raw, "content", "Content"), textPhase,
    state: stringValue(raw, "state", "State"),
    collapsed: Boolean(raw.collapsed ?? raw.Collapsed),
    data,
    attachments: normalizeAttachments(raw.attachments ?? raw.Attachments),
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
    const sequence = sequences[index] ?? index;
    result.push({ ...block, sequence });
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
    parentRunId: stringValue(raw, "parentRunId", "ParentRunID"), parentToolCallId: stringValue(raw, "parentToolCallId", "ParentToolCallID"),
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
    modelVisible: Boolean(raw.modelVisible ?? raw.ModelVisible),
    resourceCount: numberValue(raw.resourceCount ?? raw.ResourceCount),
  };
}

function normalizePlugin(raw: Record<string, unknown>): PluginEntry {
  return {
    id: stringValue(raw, "id", "ID"), name: stringValue(raw, "name", "Name"),
    displayName: stringValue(raw, "displayName", "DisplayName"), version: stringValue(raw, "version", "Version"),
    marketplace: stringValue(raw, "marketplace", "Marketplace"), origin: stringValue(raw, "origin", "Origin") || "local", description: stringValue(raw, "description", "Description"),
    developerName: stringValue(raw, "developerName", "DeveloperName"), category: stringValue(raw, "category", "Category"),
    brandColor: stringValue(raw, "brandColor", "BrandColor"), logoPath: stringValue(raw, "logoPath", "LogoPath"),
    enabled: Boolean(raw.enabled ?? raw.Enabled), skillCount: numberValue(raw.skillCount ?? raw.SkillCount),
    mcpServerCount: numberValue(raw.mcpServerCount ?? raw.MCPServerCount), integratedMCPCount: numberValue(raw.integratedMCPCount ?? raw.IntegratedMCPCount),
    hookCount: numberValue(raw.hookCount ?? raw.HookCount), hooksTrusted: Boolean(raw.hooksTrusted ?? raw.HooksTrusted),
    hasApp: Boolean(raw.hasApp ?? raw.HasApp), capabilities: ((raw.capabilities ?? raw.Capabilities ?? []) as unknown[]).map(String),
    status: stringValue(raw, "status", "Status"), warning: stringValue(raw, "warning", "Warning"),
		imported: Boolean(raw.imported ?? raw.Imported),
  };
}

function normalizeMCPServer(raw: Record<string, unknown>): MCPServerEntry {
  const rawTools = (raw.tools ?? raw.Tools ?? []) as Array<Record<string, unknown>>;
  return {
    name: stringValue(raw, "name", "Name"), removable: Boolean(raw.removable ?? raw.Removable), enabled: Boolean(raw.enabled ?? raw.Enabled),
    state: stringValue(raw, "state", "State"), transport: stringValue(raw, "transport", "Transport"),
    target: stringValue(raw, "target", "Target"), command: stringValue(raw, "command", "Command") || undefined,
    args: ((raw.args ?? raw.Args ?? []) as unknown[]).map(String), cwd: stringValue(raw, "cwd", "CWD") || undefined,
    inheritEnv: Boolean(raw.inheritEnv ?? raw.InheritEnv), url: stringValue(raw, "url", "URL") || undefined,
    approval: stringValue(raw, "approval", "Approval"), maxConcurrency: numberValue(raw.maxConcurrency ?? raw.MaxConcurrency),
    toolCount: numberValue(raw.toolCount ?? raw.ToolCount),
    tools: rawTools.map((tool) => ({
      name: stringValue(tool, "name", "Name"), description: stringValue(tool, "description", "Description"),
      effect: stringValue(tool, "effect", "Effect"), requiresApproval: Boolean(tool.requiresApproval ?? tool.RequiresApproval),
    })),
    error: stringValue(raw, "error", "Error"),
  };
}

export function parseMCPServers(raw: string | undefined): MCPServerEntry[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((entry): entry is Record<string, unknown> => Boolean(entry) && typeof entry === "object").map(normalizeMCPServer);
  } catch {
    return [];
  }
}

function normalizeBranch(raw: Record<string, unknown>): GitBranch {
  return { name: stringValue(raw, "name", "Name"), current: Boolean(raw.current ?? raw.Current) };
}

function normalizeModel(raw: Record<string, unknown>): ModelOption {
  const id = stringValue(raw, "id", "ID");
	const name = stringValue(raw, "name", "Name");
  const contextWindow = numberValue(raw.contextWindow ?? raw.ContextWindow);
	const capabilities = new Set(((raw.capabilities ?? raw.Capabilities ?? []) as unknown[]).map(String));
	if (raw.supportsTools ?? raw.SupportsTools) capabilities.add("tools");
	if (raw.supportsParallel ?? raw.SupportsParallel) capabilities.add("parallel-tools");
	if (raw.supportsReasoning ?? raw.SupportsReasoning) capabilities.add("reasoning");
	if (raw.supportsStructured ?? raw.SupportsStructured) capabilities.add("structured-output");
	const serviceTiers = (raw.serviceTiers ?? raw.ServiceTiers ?? []) as unknown[];
	const supportsPriorityTier = serviceTiers.some((tier) => {
		if (typeof tier === "string") return ["priority", "fast"].includes(tier.trim().toLocaleLowerCase());
		if (!tier || typeof tier !== "object") return false;
		const id = stringValue(tier as Record<string, unknown>, "id", "ID").trim().toLocaleLowerCase();
		return id === "priority" || id === "fast";
	});
	const additionalSpeedTiers = ((raw.additionalSpeedTiers ?? raw.AdditionalSpeedTiers ?? []) as unknown[])
		.map(String)
		.map((tier) => tier.trim().toLocaleLowerCase());
	if (supportsPriorityTier || additionalSpeedTiers.includes("fast")) capabilities.add("fast");
	const inputModalities = ((raw.inputModalities ?? raw.InputModalities ?? []) as unknown[]).map(String);
	const outputModalities = ((raw.outputModalities ?? raw.OutputModalities ?? []) as unknown[]).map(String);
  return {
	  id, name: modelDisplayName(id, name), aliases: ((raw.aliases ?? raw.Aliases ?? []) as unknown[]).map(String),
    reasoningLevels: ((raw.reasoningLevels ?? raw.ReasoningLevels ?? []) as unknown[]).map(String),
    defaultReasoning: stringValue(raw, "defaultReasoning", "DefaultReasoning"),
    ...(contextWindow > 0 ? { contextWindow } : {}),
	  ...(capabilities.size > 0 ? { capabilities: [...capabilities] } : {}),
	  ...(inputModalities.length > 0 ? { inputModalities } : {}),
	  ...(outputModalities.length > 0 ? { outputModalities } : {}),
	  ...(Boolean(raw.disabled ?? raw.Disabled) ? { disabled: true } : {}),
  };
}

export function modelDisplayName(id: string, name = "") {
	if (name.trim() && name.trim().toLocaleLowerCase() !== id.trim().toLocaleLowerCase()) return name.trim();
	const raw = id.replace(/^~/, "").split("/").at(-1) || id;
	const acronyms: Record<string, string> = { ai: "AI", api: "API", glm: "GLM", gpt: "GPT", oss: "OSS", vl: "VL", r1: "R1" };
	return raw.split(/[-_]+/).filter(Boolean).map((part) => acronyms[part.toLowerCase()] ?? (/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1))).join(" ");
}

export function providerDisplayName(id: string, providers: ModelProvider[]) {
	return providers.find((provider) => provider.ID === id)?.DisplayName ?? (id === "chatgpt" ? "ChatGPT" : id === "grok" ? "Grok" : id);
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
    const usage: ContextUsage = {
      inputTokens,
      outputTokens,
      contextLimit: numberValue(value.contextLimit ?? value.ContextLimit, fallbackLimit) || fallbackLimit,
      reported: Boolean(value.currentTurnMainReported ?? value.CurrentTurnMainReported ?? inputTokens > 0),
    };
    const cacheInput = value.cacheInputTokens ?? value.CacheInputTokens;
    const cachedInput = value.cachedInputTokens ?? value.CachedInputTokens;
    const cacheWrite = value.cacheWriteTokens ?? value.CacheWriteTokens;
    const uncachedInput = value.uncachedInputTokens ?? value.UncachedInputTokens;
    const cacheReported = value.cacheReported ?? value.CacheReported;
    const mainCacheReported = value.mainCacheReported ?? value.MainCacheReported;
    const cacheWriteReported = value.cacheWriteReported ?? value.CacheWriteReported;
    if (cacheInput !== undefined) usage.cacheInputTokens = numberValue(cacheInput);
    if (cachedInput !== undefined) usage.cachedInputTokens = numberValue(cachedInput);
    if (cacheWrite !== undefined) usage.cacheWriteTokens = numberValue(cacheWrite);
    if (uncachedInput !== undefined) usage.uncachedInputTokens = numberValue(uncachedInput);
    if (cacheReported !== undefined) usage.cacheReported = cacheReported === true || cacheReported === "true";
    if (mainCacheReported !== undefined) usage.mainCacheReported = mainCacheReported === true || mainCacheReported === "true";
    if (cacheWriteReported !== undefined) usage.cacheWriteReported = cacheWriteReported === true || cacheWriteReported === "true";
    return usage;
  } catch {
    return emptyContextUsage(fallbackLimit);
  }
}

function projectContextUsage(current: ContextUsage, data: Record<string, string>, state?: string): ContextUsage {
  if (data.factSnapshot === "true" && data.usageSnapshot) return parseContextUsage(data.usageSnapshot, current.contextLimit);
  const requestKind = data.requestKind || "main";
  const cacheReported = data.cacheStatus === "reported";
  const cacheWriteReported = data.cacheWriteStatus === "reported";
  const next: ContextUsage = {
    ...current,
    cacheInputTokens: (current.cacheInputTokens ?? 0) + (cacheReported && data.inputTokens !== undefined ? numberValue(data.inputTokens) : 0),
    cachedInputTokens: (current.cachedInputTokens ?? 0) + (data.cachedInputTokens === undefined ? 0 : numberValue(data.cachedInputTokens)),
    cacheWriteTokens: (current.cacheWriteTokens ?? 0) + (data.cacheWriteTokens === undefined ? 0 : numberValue(data.cacheWriteTokens)),
    cacheReported: current.cacheReported || cacheReported,
    cacheWriteReported: current.cacheWriteReported || cacheWriteReported,
  };
  if (requestKind !== "main" || data.aggregateOnly === "true") return next;
  const { uncachedInputTokens: _uncachedInputTokens, mainCacheReported: _mainCacheReported, ...main } = next;
  return {
    ...main,
    inputTokens: data.inputTokens === undefined ? current.inputTokens : numberValue(data.inputTokens),
    outputTokens: data.outputTokens === undefined ? current.outputTokens : numberValue(data.outputTokens),
    ...(cacheReported && data.uncachedInputTokens !== undefined ? {
      uncachedInputTokens: numberValue(data.uncachedInputTokens),
      mainCacheReported: true,
    } : {}),
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
      parentRunId: agent.parentRunId || current.parentRunId,
      parentToolCallId: agent.parentToolCallId || current.parentToolCallId,
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
