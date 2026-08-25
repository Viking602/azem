import type {
  AgentCatalogEntry,
  AgentState,
  Attachment,
  BackgroundProcess,
  Block,
  ContextProfile,
  ExtensionTheme,
  GitBranch,
  HookCatalog,
  MCPServerEntry,
  ModelRoute,
  MarketplaceCatalog,
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
  SecurityConfig,
  SecurityFinding,
  SecurityPatchResult,
  SecurityProjection,
  SecurityScan,
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
  marketplaceCatalog: MarketplaceCatalog;
  extensionThemes: ExtensionTheme[];
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
  securityScans: SecurityScan[];
  securityConfig: SecurityConfig | null;
  securityScansLoaded: boolean;
  securityProjection: SecurityProjection | null;
  securityProjections: Record<string, SecurityProjection>;
  securityFindings: SecurityFinding[];
  selectedSecurityFinding: SecurityFinding | null;
  securityPatch: SecurityPatchResult | null;
  securityFindingsByScan: Record<string, SecurityFinding[]>;
  securityExportPath: string;
  securityPublication: Record<string, string> | null;
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
  settingsOpen: boolean;
  settingsTarget: SettingsSearchTarget | null;
  commandOpen: boolean;
  sessionSearchTarget: SessionSearchTarget | null;
  planMode: boolean;
  attachments: Attachment[];
  // Follow-up queues are process-local but session-scoped, matching Codex navigation behavior.
  queuedPrompts: QueuedPrompt[];
  queuePauseReasons: Record<string, "interrupted">;
  theme: string;
  uiFont: UIFont;
  uiFontSize: number;
  chatFontSize: number;
  chatCodeFontSize: number;
}
