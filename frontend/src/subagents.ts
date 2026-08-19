import { tFormat, translator, type Language } from "./i18n";
import type { AgentPreviewKind, AgentState } from "./types";

const ACTIVE_STATES: Record<string, true> = { initializing: true, started: true, running: true, cancelling: true };
const TERMINAL_STATES: Record<string, true> = { completed: true, failed: true, cancelled: true, canceled: true, interrupted: true };
const PREVIEW_LIMIT = 240;
const LIFECYCLE_COPY = /^(?:initializing|started|running|queued|waiting|editing|cancelling|completed|failed|cancelled|canceled|interrupted|idle)$/i;
const TOOL_ACTIVITY = /^(?:coding[._]|subagent[._]|context[._]|hydaelyn[._])/i;

export type SubagentGlyphKind =
  | "architecture"
  | "security"
  | "interface"
  | "systems"
  | "verify"
  | "plan"
  | "explore"
  | "research"
  | "report"
  | "build"
  | "review"
  | "general";

const ROLE_IDENTITIES: Record<SubagentGlyphKind, readonly [string, string]> = {
  architecture: ["#6474b9", "#9aa5d7"],
  security: ["#bf5d71", "#e0a0ad"],
  interface: ["#8b6cc7", "#b8a5df"],
  systems: ["#4f83a6", "#91b7ce"],
  verify: ["#318a68", "#82bda7"],
  plan: ["#a66c3f", "#d2a57f"],
  explore: ["#3f8c98", "#87bbc2"],
  research: ["#5979ad", "#96acd0"],
  report: ["#8a7462", "#b9aa9d"],
  build: ["#c35e4c", "#df9b8e"],
  review: ["#c07a3e", "#dda977"],
  general: ["#6f746f", "#a9ada8"],
};

const ROLE_RULES: readonly [SubagentGlyphKind, RegExp][] = [
  ["security", /security|secure|permission|approval|compliance|auth|threat|abuse|安全|权限|审批|合规|认证|授权|威胁|滥用/i],
  ["architecture", /architecture|architect|module|boundary|coupling|dependency|架构|模块|边界|依赖|耦合|文档一致/i],
  ["interface", /frontend|front-end|\bui\b|\bux\b|accessib|render|前端|界面|交互|可访问|渲染|样式/i],
  ["systems", /backend|back-end|\bgo\b|runtime|server|database|storage|scheduler|persistence|后端|运行时|服务端|数据库|存储|调度|持久化/i],
  ["verify", /verify|verification|\btest|\bqa\b|quality|regression|验证|测试|工程保障|质量|回归/i],
  ["plan", /\bplan|planner|planning|规划|计划|方案/i],
  ["explore", /explore|investigat|debug|diagnos|定位|探索|调查|排查|分析/i],
  ["research", /research|search|资料|研究|调研|检索/i],
  ["report", /report|reporter|document|writer|汇报|报告|文档|总结/i],
  ["build", /worker|implement|engineer|develop|coding|\bcode\b|\bfix\b|实现|编码|修复|开发/i],
  ["review", /review|audit|审查|审阅|评估|复核/i],
];

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
export function subagentEvidenceStatusLabel(status: AgentState["evidenceStatus"], language: Language) {
  if (status === "verified") return language === "zh-CN" ? "证据已验证" : "Verified evidence";
  if (status === "stale") return language === "zh-CN" ? "证据已过期" : "Stale evidence";
  if (status === "provisional") return language === "zh-CN" ? "证据待验证" : "Provisional evidence";
  return "";
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
  if (isSubagentActive(agent.state)) return translator(language)("subagentWaitingModel");
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

export function subagentVisualIdentity(agent: Pick<AgentState, "id" | "type"> & Partial<Pick<AgentState, "description">>) {
  const source = `${agent.type || ""} ${agent.id || ""} ${agent.description || ""}`.trim();
  const kind = ROLE_RULES.find(([, pattern]) => pattern.test(source))?.[0] || "general";
  const palette = ROLE_IDENTITIES[kind];
  return { kind, accent: palette[0], secondary: palette[1] };
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
