import { tFormat, translator, type Language } from "./i18n";
import type { AgentPreviewKind, AgentState } from "./types";

const ACTIVE_STATES: Record<string, true> = { initializing: true, started: true, running: true, cancelling: true };
const TERMINAL_STATES: Record<string, true> = { completed: true, failed: true, cancelled: true, canceled: true, interrupted: true };
const PREVIEW_LIMIT = 240;
const LIFECYCLE_COPY = /^(?:initializing|started|running|queued|waiting|editing|cancelling|completed|failed|cancelled|canceled|interrupted|idle)$/i;
const TOOL_ACTIVITY = /^(?:coding[._]|subagent[._]|context[._]|hydaelyn[._])/i;

const IDENTITY_PALETTES = [
  ["#e66aa0", "#f3a8c7"],
  ["#67c6a6", "#a7e2ce"],
  ["#8d73e8", "#c1b2f5"],
  ["#ec7b70", "#f3aaa3"],
  ["#4ba7d8", "#9bd0eb"],
  ["#d79545", "#efc083"],
] as const;

export function isSubagentActive(state: string | undefined) {
  return Boolean(ACTIVE_STATES[(state || "").toLowerCase()]);
}

export function isSubagentTerminal(state: string | undefined) {
  return Boolean(TERMINAL_STATES[(state || "").toLowerCase()]);
}

export function subagentStatusLabel(state: string | undefined, language: Language) {
  const t = translator(language);
  if (state === "initializing") return t("agentInitializing");
  if (state === "running" || state === "started") return t("running");
  if (state === "cancelling") return t("agentCancelling");
  if (state === "completed") return t("agentCompleted");
  if (state === "failed") return t("agentFailed");
  if (state === "cancelled" || state === "canceled") return t("agentCancelled");
  if (state === "interrupted") return t("agentInterrupted");
  if (state === "queued") return t("agentQueued");
  return state || t("agentIdle");
}

export function subagentSummaryLabel(agents: AgentState[], language: Language) {
  const active = agents.filter((agent) => isSubagentActive(agent.state)).length;
  const queued = agents.filter((agent) => agent.state === "queued").length;
  if (active > 0 && queued > 0) return tFormat(language, "subagentsRunningQueued", { running: active, queued });
  if (active > 0) return tFormat(language, "subagentsRunning", { count: active });
  if (queued > 0) return tFormat(language, "subagentsQueued", { count: queued });
  return tFormat(language, "subagentsFinished", { count: agents.length });
}

export function subagentDisplayName(agent: AgentState, agents: AgentState[], language: Language) {
  const fallback = language === "zh-CN" ? "子智能体" : "Subagent";
  const base = truncateLine(compactLine(agent.description) || compactLine(agent.type) || compactLine(agent.id) || fallback, 72);
  const duplicates = agents.filter((candidate) => {
    const candidateBase = truncateLine(compactLine(candidate.description) || compactLine(candidate.type) || compactLine(candidate.id) || fallback, 72);
    return candidateBase.toLocaleLowerCase() === base.toLocaleLowerCase();
  });
  if (duplicates.length <= 1) return base;
  const ordinal = Math.max(0, duplicates.findIndex((candidate) => candidate.id === agent.id)) + 1;
  return `${base} · ${ordinal}`;
}

export function subagentPreviewText(agent: AgentState, displayName: string, language: Language) {
  const candidates = [agent.preview, agent.summary, agent.activity, agent.description, agent.type];
  for (const candidate of candidates) {
    const value = compactLine(candidate);
    if (!value || value === displayName || LIFECYCLE_COPY.test(value) || TOOL_ACTIVITY.test(value)) continue;
    return truncateLine(value, PREVIEW_LIMIT);
  }
  return translator(language)("subagentNoPreview");
}

export function projectSubagentPreview(
  agents: AgentState[],
  agentId: string,
  runId: string,
  kind: Exclude<AgentPreviewKind, "">,
  chunk: string,
) {
  if (!agentId || !chunk) return agents;
  const index = agents.findIndex((agent) => agent.id === agentId);
  if (index < 0) return agents;
  const current = agents[index]!;
  const sameStream = current.previewKind === kind && current.previewRunId === runId;
  const nextChunk = chunk.replace(/\r/g, "");
  const startsBlock = kind === "thinking" && /^(?:\*\*|#{1,6}\s|[-*+]\s|\d+\.\s)/u.test(nextChunk.trimStart());
  const combined = sameStream
    ? `${current.preview}${startsBlock && current.preview ? " " : ""}${nextChunk}`
    : nextChunk;
  const preview = truncateLine(compactLine(combined), PREVIEW_LIMIT);
  if (!preview || (preview === current.preview && sameStream)) return agents;
  return agents.map((agent, currentIndex) => currentIndex === index
    ? { ...agent, preview, previewKind: kind, previewRunId: runId }
    : agent);
}

export function subagentElapsedMs(agent: AgentState, now: number) {
  if (!isSubagentActive(agent.state) || agent.elapsedObservedAt <= 0) return Math.max(0, agent.elapsedMs);
  return Math.max(0, agent.elapsedMs + Math.max(0, now - agent.elapsedObservedAt));
}

export function formatSubagentElapsed(elapsedMs: number) {
  const seconds = Math.max(0, Math.floor(elapsedMs / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remaining = seconds % 60;
  if (minutes < 60) return remaining > 0 ? `${minutes}m ${remaining}s` : `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return remainingMinutes > 0 ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
}

export function subagentVisualIdentity(agent: Pick<AgentState, "id" | "type">) {
  const source = agent.id || agent.type || "subagent";
  let hash = 2166136261;
  for (let index = 0; index < source.length; index += 1) {
    hash ^= source.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  const normalized = hash >>> 0;
  const palette = IDENTITY_PALETTES[normalized % IDENTITY_PALETTES.length]!;
  return { accent: palette[0], secondary: palette[1], variant: normalized % 4 };
}

function compactLine(value: string | undefined) {
  return (value || "")
    .replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
    .replace(/[`*_>#~]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function truncateLine(value: string, limit: number) {
  if (value.length <= limit) return value;
  return `${value.slice(0, Math.max(0, limit - 1)).trimEnd()}…`;
}
