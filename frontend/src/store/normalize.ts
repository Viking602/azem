import { isSubagentActive, isSubagentTerminal } from "../subagents";
import type {
  AgentCatalogEntry,
  AgentState,
  Attachment,
  BackgroundProcess,
  Block,
  GitBranch,
  HookCatalog,
  MCPServerEntry,
  ModelProvider,
  PluginEntry,
  Project,
  Session,
  SkillEntry,
  UsageDay,
  UsageKindRow,
  UsageModelRow,
  UsageReport,
  UsageSkillRow,
} from "../types";

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

export function normalizeSession(raw: Record<string, unknown>): Session {
  return {
    id: stringValue(raw, "id"), workspace: stringValue(raw, "workspace"), title: stringValue(raw, "title") || "New session",
    providerId: stringValue(raw, "providerId"), modelId: stringValue(raw, "modelId"),
    reasoning: stringValue(raw, "reasoning"), agentMode: stringValue(raw, "agentMode"),
    pinned: Boolean(raw.pinned), archived: Boolean(raw.archived), unread: Boolean(raw.unread),
    updatedAt: stringValue(raw, "updatedAt"),
  };
}

export function normalizeProject(raw: Record<string, unknown>): Project {
  return { workspace: stringValue(raw, "workspace"), updatedAt: stringValue(raw, "updatedAt") };
}

function normalizeAttachment(raw: unknown): Attachment | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const item = raw as Record<string, unknown>;
  return {
    id: stringValue(item, "id"),
    name: stringValue(item, "name"),
    mimeType: stringValue(item, "mimeType") || stringValue(item, "mime"),
    path: stringValue(item, "path"),
    size: Number(item.size ?? 0),
  };
}

function normalizeAttachments(raw: unknown): Attachment[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  return raw.map(normalizeAttachment).filter((item): item is Attachment => item !== null);
}

export function normalizeBlock(raw: Record<string, unknown>, index: number): Block {
  const rawData = raw.data;
  const normalizedData = rawData && typeof rawData === "object" && !Array.isArray(rawData)
    ? Object.fromEntries(Object.entries(rawData as Record<string, unknown>).map(([key, value]) => [key, String(value ?? "")]))
    : undefined;
  const contentBytes = Number(raw.contentBytes ?? 0);
  const contentTruncated = Boolean(raw.contentTruncated);
  const data = contentBytes > 0 || contentTruncated
    ? { ...(normalizedData ?? {}), contentBytes: String(contentBytes), contentTruncated: String(contentTruncated) }
    : normalizedData;
  const rawTextPhase = stringValue(raw, "textPhase") || data?.phase || "";
  const textPhase = rawTextPhase === "commentary" || rawTextPhase === "final_answer" ? rawTextPhase : undefined;
  return {
    id: String(raw.id ?? `block-${index}`),
    kind: String(raw.kind ?? "assistant") as Block["kind"],
    runId: stringValue(raw, "runId"), agentId: stringValue(raw, "agentId"),
    toolCallId: stringValue(raw, "toolCallId"), title: stringValue(raw, "title"),
    approvalId: stringValue(raw, "approvalId") || data?.approvalId,
    userInputId: stringValue(raw, "userInputId") || data?.userInputId,
    planId: stringValue(raw, "planId") || data?.planId,
    content: stringValue(raw, "content"), textPhase,
    state: stringValue(raw, "state"),
    collapsed: Boolean(raw.collapsed),
    data,
    attachments: normalizeAttachments(raw.attachments),
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
  const toolCallId = stringValue(raw, "toolCallId") || `tool-record-${index}`;
  const argumentsRaw = raw.arguments;
  const argumentText = typeof argumentsRaw === "string"
    ? argumentsRaw
    : argumentsRaw != null ? JSON.stringify(argumentsRaw) : "";
  const structuredRaw = raw.structured;
  const structuredText = typeof structuredRaw === "string"
    ? structuredRaw
    : structuredRaw != null ? JSON.stringify(structuredRaw) : "";
  const content = stringValue(raw, "content");
  const startedAt = parseTimestamp(raw.startedAt);
  const completedAt = parseTimestamp(raw.completedAt);
  const elapsedMs = startedAt && completedAt && completedAt >= startedAt ? completedAt - startedAt : 0;
  const state = stringValue(raw, "state") || "completed";
  const data: Record<string, string> = {};
  if (elapsedMs > 0) data.elapsedMs = String(elapsedMs);
  if (startedAt > 0) data.startedAt = String(startedAt);
  if (completedAt > 0) data.completedAt = String(completedAt);
  if (structuredText) data.structured = structuredText;
  const fileChange = stringValue(raw, "fileChange");
  if (fileChange) data.fileChange = fileChange;
  return {
    anchorSequence: numberValue(raw.anchorSequence, -1),
    startedAt: startedAt || index,
    block: {
      id: `tool-${toolCallId}`,
      kind: "tool" as const,
      runId: stringValue(raw, "runId"),
      toolCallId,
      title: stringValue(raw, "name"),
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

export function parseJSONValue(raw: unknown) {
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

export function normalizeAgentSnapshot(raw: Record<string, unknown>): AgentState {
  return normalizeAgent(stringValue(raw, "id"), (raw.agent ?? {}) as Record<string, unknown>, stringValue(raw, "state"), stringValue(raw, "summary"));
}

export function normalizeBackgroundProcess(raw: Record<string, unknown>): BackgroundProcess {
  return {
    id: stringValue(raw, "id"), name: stringValue(raw, "name"),
    command: stringValue(raw, "command"), cwd: stringValue(raw, "cwd"),
    pid: numberValue(raw.pid), state: stringValue(raw, "state"),
    exitCode: numberValue(raw.exitCode), startedAt: stringValue(raw, "startedAt"),
    finishedAt: stringValue(raw, "finishedAt") || undefined,
    error: stringValue(raw, "error") || undefined,
  };
}


export function normalizeAgent(id: string, raw: Record<string, unknown>, state = "", summary = ""): AgentState {
  const evidenceStatus = raw.evidenceStatus === "provisional" || raw.evidenceStatus === "verified" || raw.evidenceStatus === "stale"
    ? raw.evidenceStatus
    : "";
  return {
    id, type: stringValue(raw, "type"), description: stringValue(raw, "description"),
    parentRunId: stringValue(raw, "parentRunId"), parentToolCallId: stringValue(raw, "parentToolCallId"),
    model: stringValue(raw, "model"), background: Boolean(raw.background),
    capabilityMode: stringValue(raw, "capabilityMode"), isolation: stringValue(raw, "isolation"),
    cwd: stringValue(raw, "cwd"), activity: stringValue(raw, "activity"),
    warning: stringValue(raw, "warning"), evidenceStatus,
    worktreePath: stringValue(raw, "worktreePath"),
    toolCalls: numberValue(raw.toolCalls), turns: numberValue(raw.turns),
    tokensUsed: numberValue(raw.tokensUsed), elapsedMs: numberValue(raw.elapsedMs),
    state, summary, preview: "", previewKind: "", previewRunId: "", elapsedObservedAt: Date.now(),
  };
}

export function normalizeAgentCatalog(raw: Record<string, unknown>): AgentCatalogEntry {
  return {
    name: stringValue(raw, "name"), description: stringValue(raw, "description"),
    model: stringValue(raw, "model"), reasoning: stringValue(raw, "reasoning"),
    capabilityMode: stringValue(raw, "capabilityMode"), isolation: stringValue(raw, "isolation"),
    source: stringValue(raw, "source"), enabled: Boolean(raw.enabled),
  };
}

export function normalizeSkill(raw: Record<string, unknown>): SkillEntry {
  return {
    name: stringValue(raw, "name"), description: stringValue(raw, "description"),
    sourcePath: stringValue(raw, "sourcePath"), logoPath: stringValue(raw, "logoPath"), bundled: Boolean(raw.bundled),
    eager: Boolean(raw.eager), disabled: Boolean(raw.disabled),
    modelVisible: Boolean(raw.modelVisible),
    resourceCount: numberValue(raw.resourceCount),
  };
}

export function pluginImportID(plugin: Pick<PluginEntry, "id" | "name" | "marketplace">): string {
  const id = plugin.id.trim();
  if (id) return id;
  const name = plugin.name.trim();
  const marketplace = plugin.marketplace.trim();
  return name && marketplace ? `${name}@${marketplace}` : name;
}

export function normalizePlugin(raw: Record<string, unknown>): PluginEntry {
  const name = stringValue(raw, "name");
  const marketplace = stringValue(raw, "marketplace");
  return {
    id: pluginImportID({ id: stringValue(raw, "id"), name, marketplace }),
    name, displayName: stringValue(raw, "displayName"), version: stringValue(raw, "version"),
    marketplace, origin: stringValue(raw, "origin") || "local", description: stringValue(raw, "description"),
    developerName: stringValue(raw, "developerName"), category: stringValue(raw, "category"),
    brandColor: stringValue(raw, "brandColor"), logoPath: stringValue(raw, "logoPath"),
    enabled: Boolean(raw.enabled), skillCount: numberValue(raw.skillCount),
    mcpServerCount: numberValue(raw.mcpServerCount), integratedMCPCount: numberValue(raw.integratedMCPCount),
    hookCount: numberValue(raw.hookCount), hooksTrusted: Boolean(raw.hooksTrusted),
    hasApp: Boolean(raw.hasApp), capabilities: ((raw.capabilities ?? []) as unknown[]).map(String),
    status: stringValue(raw, "status"), warning: stringValue(raw, "warning"),
		imported: Boolean(raw.imported),
  };
}

export function normalizeHookCatalog(raw?: Record<string, unknown>): HookCatalog {
  const value = raw ?? {};
  const sources = Array.isArray(value.sources) ? value.sources : [];
  const commands = Array.isArray(value.commands) ? value.commands : [];
  const diagnostics = Array.isArray(value.diagnostics) ? value.diagnostics : [];
  return {
    enabled: Boolean(value.enabled ?? true),
    trustHooks: Boolean(value.trustHooks),
    sources: (sources as Array<Record<string, unknown>>).map((item) => ({
      id: stringValue(item, "id"),
      name: stringValue(item, "name"),
      origin: stringValue(item, "origin") || "user",
      pluginId: stringValue(item, "pluginId") || undefined,
      source: stringValue(item, "source"),
      hookCount: numberValue(item.hookCount),
      trusted: Boolean(item.trusted),
      warning: stringValue(item, "warning") || undefined,
      logoPath: stringValue(item, "logoPath") || undefined,
    })),
    commands: (commands as Array<Record<string, unknown>>).map((item) => ({
      id: stringValue(item, "id"),
      name: stringValue(item, "name"),
      event: stringValue(item, "event"),
      matcher: stringValue(item, "matcher"),
      command: stringValue(item, "command"),
      source: stringValue(item, "source"),
      origin: stringValue(item, "origin"),
      enabled: item.enabled !== false,
    })),
    diagnostics: (diagnostics as Array<Record<string, unknown>>).map((item) => ({
      source: stringValue(item, "source"),
      event: stringValue(item, "event") || undefined,
      message: stringValue(item, "message"),
    })),
  };
}

export function emptyUsageReport(): UsageReport {
  return {
    scope: "project", from: "", to: "", empty: true, requests: 0, sessions: 0, runs: 0,
    totalTokens: 0, inputTokens: 0, outputTokens: 0, reasoningTokens: 0, reportedInputTokens: 0,
    cacheReadTokens: 0, cacheWriteTokens: 0, cacheReported: false, cacheWriteReported: false,
    peakDayTokens: 0, currentStreak: 0, longestStreak: 0, days: [], kinds: [], models: [], skills: [],
  };
}

export function normalizeUsageReport(raw?: UsageReport | Record<string, unknown> | null): UsageReport {
  const value = (raw ?? {}) as Record<string, unknown>;
  const days = Array.isArray(value.days) ? value.days as Array<Record<string, unknown>> : [];
  const kinds = Array.isArray(value.kinds) ? value.kinds as Array<Record<string, unknown>> : [];
  const models = Array.isArray(value.models) ? value.models as Array<Record<string, unknown>> : [];
  const skills = Array.isArray(value.skills) ? value.skills as Array<Record<string, unknown>> : [];
  return {
    scope: stringValue(value, "scope") || "project",
    workspace: stringValue(value, "workspace") || undefined,
    from: stringValue(value, "from"),
    to: stringValue(value, "to"),
    empty: Boolean(value.empty),
    requests: numberValue(value.requests),
    sessions: numberValue(value.sessions),
    runs: numberValue(value.runs),
    totalTokens: numberValue(value.totalTokens),
    inputTokens: numberValue(value.inputTokens),
    outputTokens: numberValue(value.outputTokens),
    reasoningTokens: numberValue(value.reasoningTokens),
    reportedInputTokens: numberValue(value.reportedInputTokens),
    cacheReadTokens: numberValue(value.cacheReadTokens),
    cacheWriteTokens: numberValue(value.cacheWriteTokens),
    cacheReported: Boolean(value.cacheReported),
    cacheWriteReported: Boolean(value.cacheWriteReported),
    peakDay: stringValue(value, "peakDay") || undefined,
    peakDayTokens: numberValue(value.peakDayTokens),
    currentStreak: numberValue(value.currentStreak),
    longestStreak: numberValue(value.longestStreak),
    longestRunMs: numberValue(value.longestRunMs) || undefined,
    days: days.map((item): UsageDay => ({
      date: stringValue(item, "date"),
      tokens: numberValue(item.tokens),
      requests: numberValue(item.requests),
    })),
    kinds: kinds.map((item): UsageKindRow => ({
      kind: stringValue(item, "kind"),
      tokens: numberValue(item.tokens),
      requests: numberValue(item.requests),
    })),
    models: models.map((item): UsageModelRow => ({
      provider: stringValue(item, "provider"),
      model: stringValue(item, "model"),
      tokens: numberValue(item.tokens),
      inputTokens: numberValue(item.inputTokens),
      outputTokens: numberValue(item.outputTokens),
      cacheReadTokens: numberValue(item.cacheReadTokens),
      cacheWriteTokens: numberValue(item.cacheWriteTokens),
      cacheReported: Boolean(item.cacheReported),
      cacheWriteReported: Boolean(item.cacheWriteReported),
      requests: numberValue(item.requests),
    })),
    skills: skills.map((item): UsageSkillRow => ({
      name: stringValue(item, "name"),
      activations: numberValue(item.activations),
    })),
  };
}

function normalizeMCPServer(raw: Record<string, unknown>): MCPServerEntry {
  const rawTools = (raw.tools ?? []) as Array<Record<string, unknown>>;
  return {
    name: stringValue(raw, "name"), removable: Boolean(raw.removable), enabled: Boolean(raw.enabled),
    state: stringValue(raw, "state"), transport: stringValue(raw, "transport"),
    target: stringValue(raw, "target"), command: stringValue(raw, "command") || undefined,
    args: ((raw.args ?? []) as unknown[]).map(String), cwd: stringValue(raw, "cwd") || undefined,
    inheritEnv: Boolean(raw.inheritEnv), url: stringValue(raw, "url") || undefined,
    approval: stringValue(raw, "approval"), maxConcurrency: numberValue(raw.maxConcurrency),
    toolCount: numberValue(raw.toolCount),
    tools: rawTools.map((tool) => ({
      name: stringValue(tool, "name"), description: stringValue(tool, "description"),
      effect: stringValue(tool, "effect"), requiresApproval: Boolean(tool.requiresApproval),
    })),
    error: stringValue(raw, "error"), icon: stringValue(raw, "icon") || undefined,
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

export function normalizeBranch(raw: Record<string, unknown>): GitBranch {
  return { name: stringValue(raw, "name"), current: Boolean(raw.current) };
}

export function normalizeModel(raw: Record<string, unknown>): ModelOption {
  const id = stringValue(raw, "id");
	const name = stringValue(raw, "name");
  const contextWindow = numberValue(raw.contextWindow);
	const capabilities = new Set(((raw.capabilities ?? []) as unknown[]).map(String));
	if (raw.supportsTools) capabilities.add("tools");
	if (raw.supportsParallel) capabilities.add("parallel-tools");
	if (raw.supportsReasoning) capabilities.add("reasoning");
	if (raw.supportsStructured) capabilities.add("structured-output");
	const serviceTiers = (raw.serviceTiers ?? []) as unknown[];
	const supportsPriorityTier = serviceTiers.some((tier) => {
		if (typeof tier === "string") return ["priority", "fast"].includes(tier.trim().toLocaleLowerCase());
		if (!tier || typeof tier !== "object") return false;
		const id = stringValue(tier as Record<string, unknown>, "id").trim().toLocaleLowerCase();
		return id === "priority" || id === "fast";
	});
	const additionalSpeedTiers = ((raw.additionalSpeedTiers ?? []) as unknown[])
		.map(String)
		.map((tier) => tier.trim().toLocaleLowerCase());
	if (supportsPriorityTier || additionalSpeedTiers.includes("fast")) capabilities.add("fast");
	const inputModalities = ((raw.inputModalities ?? []) as unknown[]).map(String);
	const outputModalities = ((raw.outputModalities ?? []) as unknown[]).map(String);
  return {
	  id, name: modelDisplayName(id, name), aliases: ((raw.aliases ?? []) as unknown[]).map(String),
    reasoningLevels: ((raw.reasoningLevels ?? []) as unknown[]).map(String),
    defaultReasoning: stringValue(raw, "defaultReasoning"),
    ...(contextWindow > 0 ? { contextWindow } : {}),
	  ...(capabilities.size > 0 ? { capabilities: [...capabilities] } : {}),
	  ...(inputModalities.length > 0 ? { inputModalities } : {}),
	  ...(outputModalities.length > 0 ? { outputModalities } : {}),
	  ...(Boolean(raw.disabled) ? { disabled: true } : {}),
  };
}

export function modelDisplayName(id: string, name = "") {
	if (name.trim() && name.trim().toLocaleLowerCase() !== id.trim().toLocaleLowerCase()) return name.trim();
	const raw = id.replace(/^~/, "").split("/").at(-1) || id;
	const acronyms: Record<string, string> = { ai: "AI", api: "API", glm: "GLM", gpt: "GPT", oss: "OSS", vl: "VL", r1: "R1" };
	return raw.split(/[-_]+/).filter(Boolean).map((part) => acronyms[part.toLowerCase()] ?? (/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1))).join(" ");
}

export function providerDisplayName(id: string, providers: ModelProvider[]) {
	return providers.find((provider) => provider.id === id)?.displayName ?? (id === "chatgpt" ? "ChatGPT" : id === "grok" ? "Grok" : id);
}

export function emptyContextUsage(contextLimit = 0): ContextUsage {
  return { inputTokens: 0, outputTokens: 0, contextLimit, reported: false };
}

export function parseContextUsage(raw: string | undefined, fallbackLimit = 0): ContextUsage {
  if (!raw) return emptyContextUsage(fallbackLimit);
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    const inputTokens = numberValue(value.inputTokens);
    const outputTokens = numberValue(value.outputTokens);
    const usage: ContextUsage = {
      inputTokens,
      outputTokens,
      contextLimit: numberValue(value.contextLimit, fallbackLimit) || fallbackLimit,
      reported: Boolean(value.currentTurnMainReported ?? inputTokens > 0),
    };
    const cacheInput = value.cacheInputTokens;
    const cachedInput = value.cachedInputTokens;
    const cacheWrite = value.cacheWriteTokens;
    const uncachedInput = value.uncachedInputTokens;
    const cacheReported = value.cacheReported;
    const mainCacheReported = value.mainCacheReported;
    const cacheWriteReported = value.cacheWriteReported;
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

export function projectContextUsage(current: ContextUsage, data: Record<string, string>, state?: string): ContextUsage {
  if (data.factSnapshot === "true" && data.usageSnapshot) return parseContextUsage(data.usageSnapshot, current.contextLimit);
  const requestKind = data.requestKind || "main";
  if (requestKind === "subagent") return current;
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

export function upsertAgent(agents: AgentState[], agent: AgentState): AgentState[] {
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
      evidenceStatus: agent.evidenceStatus || current.evidenceStatus,
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

export function parseArray(value: string | undefined): Array<Record<string, unknown>> {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function stringValue(raw: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) if (raw[key] !== undefined && raw[key] !== null) return String(raw[key]);
  return "";
}

export function numberValue(value: unknown, fallback = 0): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
