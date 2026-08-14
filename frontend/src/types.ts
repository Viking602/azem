import type { ActionKind, EventKind } from "./contracts";

export type { ActionKind, EventKind } from "./contracts";

export type View = "thread" | "projects" | "files" | "changes" | "pullRequests" | "runs" | "agents" | "extensions" | "recovery";
export type InspectorTab = "environment" | "changes" | "agents" | "context";
export type DeliveryMode = "queue" | "guide";
export type TextPhase = "commentary" | "final_answer";

export interface Snapshot {
  workspace: string;
  currentBranch?: string;
  sessionId: string;
  provider: string;
  model: string;
  reasoning: string;
  agentMode: string;
  language: "en" | "zh-CN";
  approvalMode: string;
  queueMode: DeliveryMode;
  subagentConcurrency: number;
  subagentMaxDepth?: number;
  shellConcurrency?: number;
  subagentAwaitSeconds?: number;
  chatgptFastMode: boolean;
  sequence: number;
  pullRequestMonitors?: PullRequestMonitorState[];
}

export interface Attachment {
  id: string;
  name: string;
  mimeType: string;
  path: string;
  size: number;
}

export interface QueuedPrompt {
  id: string;
  text: string;
  attachments: Attachment[];
  sessionId: string;
  state?: "queued" | "failed";
  error?: string;
}

export type TodoStatus = "pending" | "in_progress" | "completed" | "cancelled";

export interface TodoItem {
  id: string;
  content: string;
  status: TodoStatus;
  subagentRunId?: string;
}

export interface TodoPhase {
  id: string;
  title: string;
  items: TodoItem[];
}

export interface TodoList {
  goal: string;
  revision: number;
  phases: TodoPhase[];
  updatedAt?: string;
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
  kind: ActionKind;
  target?: string;
  decision?: string;
  sessionId?: string;
  name?: string;
  cwd?: string;
  offset?: number;
  limit?: number;
  route?: ModelRoute;
  provider?: ModelProvider;
  secret?: string;
  payload?: unknown;
}

export interface Session {
  id: string;
  workspace: string;
  title: string;
  providerId: string;
  modelId: string;
  reasoning: string;
  agentMode: string;
  pinned?: boolean;
  archived?: boolean;
  unread?: boolean;
  updatedAt: string;
}

export interface Project {
  workspace: string;
  updatedAt: string;
}

export interface WorkspaceEntry {
  name: string;
  path: string;
  directory: boolean;
  symlink?: boolean;
  hidden?: boolean;
  size?: number;
  modifiedAt?: string;
}

export interface WorkspaceDirectory {
  path: string;
  entries: WorkspaceEntry[];
  truncated?: boolean;
}

export interface WorkspaceFile {
  path: string;
  name: string;
  kind: "text" | "image" | "binary";
  language?: string;
  mediaType?: string;
  content?: string;
  size: number;
  lineCount?: number;
  modifiedAt?: string;
  truncated?: boolean;
}

export type WorkspaceChangeStatus = "modified" | "added" | "deleted" | "renamed" | "type_changed" | "untracked";

export interface WorkspaceChangeFile {
  path: string;
  status: WorkspaceChangeStatus;
  indexStatus?: WorkspaceChangeStatus;
  worktreeStatus?: WorkspaceChangeStatus;
  additions: number;
  deletions: number;
  binary?: boolean;
}

export interface WorkspaceChangeSet {
  repository: boolean;
  branch?: string;
  base?: string;
  additions: number;
  deletions: number;
  files: WorkspaceChangeFile[];
  truncated?: boolean;
}

export interface WorkspaceChange extends WorkspaceChangeFile {
  patch?: string;
  truncated?: boolean;
}

export type BlockKind = "user" | "thinking" | "commentary" | "assistant" | "tool" | "approval" | "question" | "plan" | "diff" | "status" | "agent" | "hook" | "error";

export interface Block {
  id: string;
  sequence?: number;
  kind: BlockKind;
  runId?: string;
  agentId?: string;
  toolCallId?: string;
  approvalId?: string;
  userInputId?: string;
  planId?: string;
  title?: string;
  content?: string;
  textPhase?: TextPhase;
  state?: string;
  collapsed?: boolean;
  data?: Record<string, string>;
  attachments?: Attachment[];
}

export type SettingsSection = "catalog" | "models" | "subagents" | "governance" | "appearance" | "extensions" | "archive" | "usage";

export type UsageScope = "project" | "all";

export interface UsageDay {
  date: string;
  tokens: number;
  requests: number;
}

export interface UsageKindRow {
  kind: string;
  tokens: number;
  requests: number;
}

export interface UsageModelRow {
  provider: string;
  model: string;
  tokens: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheReported: boolean;
  cacheWriteReported: boolean;
  requests: number;
}

export interface UsageSkillRow {
  name: string;
  activations: number;
}

export interface UsageReport {
  scope: UsageScope | string;
  workspace?: string;
  from: string;
  to: string;
  empty: boolean;
  requests: number;
  sessions: number;
  runs: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  reportedInputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheReported: boolean;
  cacheWriteReported: boolean;
  peakDay?: string;
  peakDayTokens: number;
  currentStreak: number;
  longestStreak: number;
  longestRunMs?: number;
  days: UsageDay[];
  kinds: UsageKindRow[];
  models: UsageModelRow[];
  skills: UsageSkillRow[];
}

export interface SettingsSearchTarget {
  section: SettingsSection;
  id: string;
}

export interface SessionSearchTarget {
  sessionId: string;
  sequence?: number;
}

export interface SessionSearchResult {
  sessionId: string;
  workspace: string;
  title: string;
  kind: "title" | "user" | "assistant";
  preview?: string;
  sequence?: number;
  updatedAt: string;
}

export type AgentPreviewKind = "" | "thinking" | "commentary" | "assistant";


export interface AgentState {
  id: string;
  type: string;
  description: string;
  parentRunId?: string;
  parentToolCallId?: string;
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
  preview: string;
  previewKind: AgentPreviewKind;
  previewRunId: string;
  elapsedObservedAt: number;
}

export interface BackgroundProcess {
  id: string;
  name: string;
  command: string;
  cwd: string;
  pid: number;
  state: string;
  exitCode: number;
  startedAt: string;
  finishedAt?: string;
  error?: string;
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
  logoPath?: string;
  bundled: boolean;
  eager: boolean;
  disabled: boolean;
  modelVisible: boolean;
  resourceCount: number;
}

export interface MCPToolEntry {
  name: string;
  description: string;
  effect: string;
  requiresApproval: boolean;
}

export interface MCPServerEntry {
  name: string;
  removable: boolean;
  enabled: boolean;
  state: "disabled" | "connecting" | "ready" | "degraded" | "stopped" | string;
  transport: "stdio" | "streamable_http" | string;
  target: string;
  command?: string;
  args: string[];
  cwd?: string;
  inheritEnv: boolean;
  url?: string;
  approval: "always" | "never" | string;
  maxConcurrency: number;
  toolCount: number;
  tools: MCPToolEntry[];
  error: string;
  icon?: string;
}

export interface MCPServerMutation {
  name: string;
  enabled: boolean;
  transport: "stdio" | "streamable_http";
  command?: string;
  args?: string[];
  cwd?: string;
  inheritEnv?: boolean;
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  connectTimeout?: string;
  callTimeout?: string;
  maxConcurrency?: number;
  approval?: "always" | "never";
}

export interface HookSourceEntry {
  id: string;
  name: string;
  origin: "plugin" | "user" | "project" | "additional" | string;
  pluginId?: string;
  source: string;
  hookCount: number;
  trusted: boolean;
  warning?: string;
  logoPath?: string;
}

export interface HookCommandEntry {
  id: string;
  name: string;
  event: string;
  matcher: string;
  command: string;
  source: string;
  origin: string;
  enabled: boolean;
}

export interface HookDiagnostic {
  source: string;
  event?: string;
  message: string;
}

export interface HookCatalog {
  enabled: boolean;
  trustHooks: boolean;
  sources: HookSourceEntry[];
  commands: HookCommandEntry[];
  diagnostics: HookDiagnostic[];
}

export interface PluginEntry {
  id: string;
  name: string;
  displayName: string;
  version: string;
  marketplace: string;
  origin: "codex" | "local" | string;
  description: string;
  developerName: string;
  category: string;
  brandColor: string;
  logoPath: string;
  enabled: boolean;
  skillCount: number;
  mcpServerCount: number;
  integratedMCPCount: number;
  hookCount: number;
  hooksTrusted: boolean;
  hasApp: boolean;
  capabilities: string[];
  status: string;
  warning: string;
	imported?: boolean;
}

export interface GitBranch {
  name: string;
  current: boolean;
}
export interface PullRequestCapability {
  available: boolean;
  code?: "not_installed" | "unauthenticated" | "no_repository" | "offline" | "error";
  message?: string;
}

export interface PullRequestRepository {
  nameWithOwner: string;
  url: string;
  defaultBranch: string;
  viewerPermission: string;
  viewerLogin: string;
  allowedMergeMethods: Array<"merge" | "squash" | "rebase">;
}

export interface PullRequestActor {
  login: string;
  name?: string;
  url?: string;
  avatarUrl?: string;
  bot?: boolean;
}

export interface PullRequestCheckSummary {
  total: number;
  pending: number;
  passing: number;
  failing: number;
  neutral: number;
  skipped: number;
}

export interface PullRequestCheck {
  name: string;
  workflow?: string;
  status?: string;
  conclusion?: string;
  category: "pending" | "passing" | "failing" | "neutral" | "skipped";
  url?: string;
  startedAt?: string;
  completedAt?: string;
}

export interface PullRequestSummary {
  number: number;
  title: string;
  state: string;
  draft: boolean;
  author: PullRequestActor;
  headRefName: string;
  headRefOid?: string;
  headRepository?: string;
  baseRefName: string;
  additions: number;
  deletions: number;
  changedFiles: number;
  reviewDecision?: string;
  mergeable?: string;
  mergeStateStatus?: string;
  url: string;
  updatedAt: string;
  checks: PullRequestCheckSummary;
}

export interface PullRequestReview {
  id?: string;
  author: PullRequestActor;
  state: string;
  body?: string;
  submittedAt?: string;
  url?: string;
}

export interface PullRequestComment {
  id?: string;
  author: PullRequestActor;
  body: string;
  createdAt: string;
  updatedAt?: string;
  url?: string;
  viewerCanEdit?: boolean;
  viewerCanDelete?: boolean;
}

export interface PullRequestCommit {
  oid: string;
  headline: string;
  body?: string;
  authoredAt?: string;
  committedAt?: string;
  authors?: PullRequestActor[];
}

export interface PullRequestFile {
  path: string;
  additions: number;
  deletions: number;
}

export interface PullRequestActivity {
  kind: "created" | "commit" | "review" | "comment" | "merged" | "closed";
  actor: PullRequestActor;
  title: string;
  body?: string;
  state?: string;
  url?: string;
  oid?: string;
  at: string;
}

export interface PullRequest extends PullRequestSummary {
  body: string;
  createdAt: string;
  closedAt?: string;
  mergedAt?: string;
  maintainerCanModify: boolean;
  autoMergeEnabled: boolean;
  autoMergeMethod?: string;
  reviewRequests: PullRequestActor[];
  reviews: PullRequestReview[];
  comments: PullRequestComment[];
  commits: PullRequestCommit[];
  files: PullRequestFile[];
  checksDetail: PullRequestCheck[];
  activity: PullRequestActivity[];
  allowedMergeMethods: Array<"merge" | "squash" | "rebase">;
}

export interface PullRequestDashboard {
  capability: PullRequestCapability;
  repository?: PullRequestRepository;
  currentBranch?: string;
  current?: PullRequestSummary;
  createdByViewer: PullRequestSummary[];
  needsReview: PullRequestSummary[];
  open: PullRequestSummary[];
  refreshedAt: string;
}

export interface PullRequestMonitorState {
  number: number;
  enabled: boolean;
  status: "disabled" | "watching" | "pending" | "repairing" | "completed" | "error";
  message?: string;
  sessionId?: string;
  conflict?: boolean;
  failingChecks?: string[];
  fingerprint?: string;
  lastCheckedAt?: string;
  lastTriggeredAt?: string;
}

export interface PullRequestDetailResponse {
  pullRequest: PullRequest;
  monitor: PullRequestMonitorState;
}

export interface PullRequestMutationRequest {
  number: number;
  kind: "edit" | "add_reviewer" | "remove_reviewer" | "comment" | "review" | "ready" | "draft" | "close" | "reopen" | "merge" | "enable_auto_merge" | "disable_auto_merge";
  title?: string;
  body?: string;
  login?: string;
  reviewKind?: "approve" | "comment" | "request_changes";
  mergeMethod?: "merge" | "squash" | "rebase";
  expectedHeadOid?: string;
  expectedRepository?: string;
}


export interface ModelRouteConfig {
  provider?: string;
  model?: string;
  reasoning?: string;
}

export interface ModelRoute {
  scope: string;
  role: string;
  label: string;
  route: ModelRouteConfig;
}

export interface LLMuxModelConfig {
	id: string;
	disabled?: boolean;
	name?: string;
	aliases?: string[];
	description?: string;
	contextWindow: number;
	maxOutputTokens?: number;
	reasoningLevels?: string[];
	defaultReasoning?: string;
	capabilities?: string[];
	inputModalities?: string[];
	outputModalities?: string[];
}

export interface ModelProvider {
	id: string;
	displayName: string;
	backend: string;
	defaultBaseUrl: string;
	baseUrl: string;
	envKey: string;
	enabled: boolean;
	credentialConfigured: boolean;
	credentialSource: "stored" | "environment" | "pending" | "none";
	subscription?: boolean;
	accountId?: string;
	accountLabel?: string;
	accountPlan?: string;
	quotaAvailable?: boolean;
	quotaPeriod?: "weekly" | "monthly" | "credits" | string;
	quotaUsedPercent?: number;
	quotaResetsAt?: number;
	quotaBalance?: string;
	quotaUnlimited?: boolean;
	quotaWarning?: string;
	modelsDevId?: string;
	modelsSource?: string;
	modelsWarning?: string;
	models: LLMuxModelConfig[];
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
  manifestHash?: string;
  semanticRevision?: number;
  semanticCursor?: {
    canonical_sequence: number;
    todo_revision: number;
    tool_completed_at_ns: number;
    tool_run_id?: string;
    tool_call_id?: string;
    subagent_finished_at_ns: number;
    subagent_id?: string;
  };
  canonicalHighWater?: number;
  policyVersion?: number;
  rebuildReason?: string;
  writerLag?: number;
  segments?: Array<{ kind: string; mandatory: boolean; token_estimate: number; content_hash: string; source_refs?: string[] }>;
  exclusions?: Array<{ source_ref: string; reason: string }>;
}

export interface SessionRecap {
  sessionId: string;
  anchor?: string;
  coveredBoundary?: string;
  goal?: string;
  summary?: string;
  openItems?: string;
  revision: number;
  updatedAt: string;
}

export interface RuntimeEvent {
  sequence: number;
  kind: EventKind;
  sessionId?: string;
  runId?: string;
  agentId?: string;
  toolCallId?: string;
  approvalId?: string;
  userInputId?: string;
  planId?: string;
  text?: string;
  textPhase?: TextPhase;
  state?: string;
  data?: Record<string, string>;
  agent?: Record<string, unknown>;
  agentBlocks?: Array<Record<string, unknown>>;
  agentCatalog?: Array<Record<string, unknown>>;
  agentSnapshots?: Array<Record<string, unknown>>;
  skillCatalog?: Array<Record<string, unknown>>;
  pluginCatalog?: Array<Record<string, unknown>>;
  hookCatalog?: Record<string, unknown>;
  usageReport?: UsageReport;
  contextProfile?: ContextProfile;
  todo?: TodoList;
  recap?: SessionRecap;
  modelRoutes?: ModelRoute[];
	modelProviders?: ModelProvider[];
  background?: Array<Record<string, unknown>>;
  gitBranches?: Array<Record<string, unknown>>;
  workspaceDirty?: boolean;
  at?: string;
}
