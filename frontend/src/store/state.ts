import type {
  AgentCatalogEntry,
  AgentState,
  Attachment,
  BackgroundProcess,
  Block,
  ContextProfile,
  GitBranch,
  HookCatalog,
  InspectorTab,
  MCPServerEntry,
  ModelRoute,
	ModelProvider,
  QueuedPrompt,
  PullRequest,
  PullRequestDashboard,
  PullRequestMonitorState,
  PluginEntry,
  Project,
  Session,
  SessionSearchTarget,
  SettingsSearchTarget,
  SessionRecap,
  SkillEntry,
  Snapshot,
  TodoList,
  UsageReport,
  View,
} from "../types";
import type { ContextUsage, ModelOption, UIFont } from "./normalize";

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
  hookCatalog: HookCatalog;
  usageReport: UsageReport | null;
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
