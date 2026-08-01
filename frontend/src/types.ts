export type View = "thread" | "projects" | "runs" | "agents" | "extensions" | "recovery";
export type InspectorTab = "environment" | "changes" | "agents" | "context";

export interface Snapshot {
  workspace: string;
  sessionId: string;
  provider: string;
  model: string;
  reasoning: string;
  agentMode: string;
  language: "en" | "zh-CN";
  approvalMode: string;
  subagentConcurrency: number;
  chatgptFastMode: boolean;
  sequence: number;
}

export interface Attachment {
  id: string;
  name: string;
  mimeType: string;
  path: string;
  size: number;
}

export interface TurnRequest {
  sessionId: string;
  prompt: string;
  provider: string;
  model: string;
  reasoning: string;
  agentMode: string;
  planMode: boolean;
  disableSubagents: boolean;
  activeSkills: string[];
  images: Attachment[];
}

export interface ActionRequest {
  kind: string;
  target?: string;
  decision?: string;
  sessionId?: string;
  name?: string;
  cwd?: string;
  offset?: number;
  limit?: number;
  route?: ModelRoute;
}

export interface Session {
  id: string;
  title: string;
  providerId: string;
  modelId: string;
  reasoning: string;
  agentMode: string;
  updatedAt: string;
}

export type BlockKind = "user" | "thinking" | "assistant" | "tool" | "approval" | "diff" | "agent" | "hook" | "error";

export interface Block {
  id: string;
  kind: BlockKind;
  runId?: string;
  agentId?: string;
  toolCallId?: string;
  approvalId?: string;
  title?: string;
  content?: string;
  state?: string;
  collapsed?: boolean;
  data?: Record<string, string>;
  attachments?: Attachment[];
}

export interface AgentState {
  id: string;
  type: string;
  description: string;
  model: string;
  background: boolean;
  capabilityMode: string;
  isolation: string;
  cwd: string;
  activity: string;
  warning: string;
  worktreePath: string;
  toolCalls: number;
  turns: number;
  tokensUsed: number;
  elapsedMs: number;
  state: string;
  summary: string;
}

export interface AgentCatalogEntry {
  name: string;
  description: string;
  model: string;
  reasoning: string;
  capabilityMode: string;
  isolation: string;
  source: string;
  enabled: boolean;
}

export interface SkillEntry {
  name: string;
  description: string;
  sourcePath: string;
  eager: boolean;
  disabled: boolean;
  resourceCount: number;
}

export interface GitBranch {
  name: string;
  current: boolean;
}

export interface ModelRouteConfig {
  provider?: string;
  model?: string;
  reasoning?: string;
}

export interface ModelRoute {
  Scope: string;
  Role: string;
  Label: string;
  Route: ModelRouteConfig;
}

export interface ContextContribution {
  category: string;
  name: string;
  tokens: number;
}

export interface ContextProfile {
  source: string;
  estimated: boolean;
  contributions: ContextContribution[];
  reportedInputTokens?: number;
  reportedOutputTokens?: number;
}

export interface RuntimeEvent {
  sequence: number;
  kind: string;
  sessionId?: string;
  runId?: string;
  agentId?: string;
  toolCallId?: string;
  approvalId?: string;
  text?: string;
  state?: string;
  data?: Record<string, string>;
  agent?: Record<string, unknown>;
  agentBlocks?: Array<Record<string, unknown>>;
  agentCatalog?: Array<Record<string, unknown>>;
  agentSnapshots?: Array<Record<string, unknown>>;
  skillCatalog?: Array<Record<string, unknown>>;
  contextProfile?: ContextProfile;
  todo?: unknown;
  recap?: unknown;
  modelRoutes?: ModelRoute[];
  gitBranches?: Array<Record<string, unknown>>;
  workspaceDirty?: boolean;
  at?: string;
}
