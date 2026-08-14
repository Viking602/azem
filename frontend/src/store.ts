import { create } from "zustand";
import { translator } from "./i18n";
import type {
  Attachment,
  DeliveryMode,
  InspectorTab,
  PullRequestDashboard,
  PullRequestDetailResponse,
  PullRequestMonitorState,
  QueuedPrompt,
  RuntimeEvent,
  SessionSearchTarget,
  SettingsSearchTarget,
  Snapshot,
  View,
} from "./types";
import { emptyContextUsage, findModelOption, normalizeUIFont, type UIFont } from "./store/normalize";
import type { RuntimeData } from "./store/state";
import { hydrateData } from "./store/hydrate";
import { reduceAgentEvent } from "./store/reduceAgents";
import { reduceCatalogEvent } from "./store/reduceCatalog";
import { reduceRunEvent } from "./store/reduceRun";
import { reduceSessionEvent } from "./store/reduceSession";
import { reduceTextEvent } from "./store/reduceText";
import { reduceToolEvent } from "./store/reduceTools";

export {
  findModelOption,
  mergeSessionTranscript,
  modelDisplayName,
  normalizeUIFont,
  parseMCPServers,
  pluginImportID,
  providerDisplayName,
} from "./store/normalize";
export type { ContextUsage, ModelOption, UIFont } from "./store/normalize";
export type { RuntimeData } from "./store/state";

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
  hookCatalog: { enabled: true, trustHooks: false, sources: [], commands: [], diagnostics: [] },
  usageReport: null,
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
  let next = state;
  for (const event of events) next = reduceEvent(next, event);
  return next;
}

export const shouldMarkSessionUnread = (
  state: Pick<RuntimeData, "currentSessionId" | "globalRunId" | "globalRunSessionId">,
  event: RuntimeEvent,
) => [!event.agentId, Boolean(event.sessionId), event.sessionId !== state.currentSessionId,
  UNREAD_SESSION_TERMINALS.has(event.kind), Boolean(state.globalRunId), event.sessionId === state.globalRunSessionId,
  ["starting", event.runId ?? state.globalRunId].includes(state.globalRunId)].every(Boolean);

const REPLACEABLE_CATALOG_KINDS = new Set(["skill_catalog", "plugin_catalog", "hook_catalog", "usage_report"]);

function reduceEvent<T extends RuntimeData>(state: T, event: RuntimeEvent): T {
  if (event.sequence && event.sequence <= state.lastSequence && !REPLACEABLE_CATALOG_KINDS.has(event.kind)) return state;
  const markSessionUnread = shouldMarkSessionUnread(state, event);
  const next = { ...state, lastSequence: Math.max(state.lastSequence, event.sequence || 0) };
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
    case "session_loaded":
    case "context_profile":
    case "context_usage":
    case "todo_updated":
    case "recap_state":
    case "bridge_error":
      reduceSessionEvent(next, event);
      break;
    case "run_started":
    case "provider_retry":
    case "run_finished":
    case "run_cancelled":
    case "run_failed":
      reduceRunEvent(next, event);
      break;
    case "thinking_delta":
    case "text_delta":
      reduceTextEvent(next, event);
      break;
    case "tool_started":
    case "tool_update":
    case "tool_finished":
    case "diff_ready":
    case "approval_requested":
    case "approval_resolved":
    case "user_input_requested":
    case "user_input_resolved":
    case "plan_proposed":
    case "plan_resolved":
      reduceToolEvent(next, event);
      break;
    case "agent_state":
    case "background_state":
    case "agent_detail":
      reduceAgentEvent(next, event);
      break;
    case "skill_catalog":
    case "plugin_catalog":
    case "hook_catalog":
    case "usage_report":
    case "mcp_state":
    case "model_routes":
    case "model_providers":
    case "model_catalog":
    case "approval_mode":
    case "git_branches":
    case "recovery_state":
      reduceCatalogEvent(next, event);
      break;
  }
  return next;
}
