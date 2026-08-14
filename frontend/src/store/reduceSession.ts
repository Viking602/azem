import type { RuntimeEvent } from "../types";
import {
  findModelOption,
  mergeSessionTranscript,
  normalizeAgentSnapshot,
  normalizeBlock,
  normalizeProject,
  normalizeSession,
  parseArray,
  parseContextUsage,
  parseJSONValue,
  projectContextUsage,
} from "./normalize";
import type { RuntimeData } from "./state";

/** Session continuity: bootstrap, session_loaded, context, todo, recap, bridge errors. */
export function reduceSessionEvent(next: RuntimeData, event: RuntimeEvent): void {
  const data = event.data ?? {};
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
    case "bridge_error":
      next.error = event.text ?? "Desktop bridge failed";
      break;
  }
}
