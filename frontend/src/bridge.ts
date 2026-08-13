import { Browser, Call, Dialogs, Events } from "@wailsio/runtime";
import type {
  ActionRequest, Attachment, PullRequest, PullRequestDashboard, PullRequestDetailResponse,
  PullRequestMonitorState, PullRequestMutationRequest, RuntimeEvent, Snapshot, TurnRequest,
  SessionSearchResult, SkillEntry, WorkspaceChange, WorkspaceChangeSet, WorkspaceDirectory, WorkspaceFile,
} from "./types";

const EVENT_NAME = "azem:event";
const SESSION_MENU_EVENT = "azem:session-menu";
const PULL_REQUEST_EVENT = "azem:pull-request";
const bridgeName = "github.com/Viking602/azem/internal/desktop.Bridge";

export const isDesktopRuntime = () =>
  typeof window !== "undefined" &&
  (location.protocol === "wails:" || Boolean((window as Window & { _wails?: { environment?: { OS?: string } } })._wails?.environment?.OS));

export const demoSnapshot: Snapshot = {
  workspace: "/Users/viking/GolandProjects/azem",
  sessionId: "session-demo",
  provider: "chatgpt",
  model: "gpt-5.6",
  reasoning: "high",
  agentMode: "single",
  language: "zh-CN",
  approvalMode: "auto_review",
  queueMode: "queue",
  subagentConcurrency: 6,
  subagentMaxDepth: 2,
  shellConcurrency: 4,
  subagentAwaitSeconds: 30,
  chatgptFastMode: false,
  sequence: 0,
  pullRequestMonitors: [],
};

export async function initialise(): Promise<Snapshot> {
  if (!isDesktopRuntime()) return demoSnapshot;
  return Call.ByName(`${bridgeName}.Initialise`) as Promise<Snapshot>;
}

export async function startTurn(request: TurnRequest): Promise<string> {
  if (!isDesktopRuntime()) return `demo-${Date.now()}`;
  return Call.ByName(`${bridgeName}.StartTurn`, request) as Promise<string>;
}

export async function execute(request: ActionRequest): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.Execute`, request);
}

export interface SkillCatalogSnapshot {
  entries: SkillEntry[];
  diagnostics: Array<{ path: string; message: string }>;
}

export async function listSkillCatalog(): Promise<SkillCatalogSnapshot> {
  if (!isDesktopRuntime()) return { entries: [], diagnostics: [] };
  const result = await Call.ByName(`${bridgeName}.SkillCatalog`) as Partial<SkillCatalogSnapshot> | null;
  return {
    entries: Array.isArray(result?.entries) ? result.entries : [],
    diagnostics: Array.isArray(result?.diagnostics) ? result.diagnostics : [],
  };
}

export interface SystemFont {
  family: string;
  label: string;
}

export async function listSystemFonts(language: "en" | "zh-CN"): Promise<SystemFont[]> {
  if (!isDesktopRuntime()) return language === "zh-CN"
    ? [{ family: "PingFang SC", label: "苹方-简" }, { family: "Songti SC", label: "宋体-简" }, { family: "Menlo", label: "Menlo" }]
    : [{ family: "Menlo", label: "Menlo" }, { family: "PingFang SC", label: "PingFang SC" }, { family: "Songti SC", label: "Songti SC" }];
  return Call.ByName(`${bridgeName}.SystemFonts`, language) as Promise<SystemFont[]>;
}

export async function selectProjectFolder(title: string, buttonText: string): Promise<string> {
  if (!isDesktopRuntime()) return "";
  return Dialogs.OpenFile({
    AllowsMultipleSelection: false,
    ButtonText: buttonText,
    CanChooseDirectories: true,
    CanChooseFiles: false,
    CanCreateDirectories: true,
    ResolvesAliases: true,
    Title: title,
  });
}

export async function createProject(name: string, location: string, initialiseGit: boolean): Promise<string> {
  if (!isDesktopRuntime()) return "";
  return Call.ByName(`${bridgeName}.CreateProject`, name, location, initialiseGit) as Promise<string>;
}

export async function openProject(path: string): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.OpenProject`, path);
}

export async function openWorkspaceTerminal(): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.OpenTerminal`);
}

export async function openProjectSession(path: string, sessionId: string, sequence?: number): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.OpenProjectSession`, path, sessionId, sequence ?? -1);
}

export async function searchSessions(query: string, limit = 20): Promise<SessionSearchResult[]> {
  if (!isDesktopRuntime()) return [];
  const result = await Call.ByName(`${bridgeName}.SearchSessions`, query, limit) as SessionSearchResult[] | null;
  return Array.isArray(result) ? result : [];
}

export async function resumeSession(sessionId: string): Promise<RuntimeEvent | null> {
  if (!isDesktopRuntime()) return null;
  return Call.ByName(`${bridgeName}.ResumeSession`, sessionId) as Promise<RuntimeEvent>;
}

export async function listWorkspaceEntries(path = ""): Promise<WorkspaceDirectory> {
  if (!isDesktopRuntime()) return demoWorkspaceDirectory(path);
  return Call.ByName(`${bridgeName}.WorkspaceEntries`, path) as Promise<WorkspaceDirectory>;
}

export async function readWorkspaceFile(path: string): Promise<WorkspaceFile> {
  if (!isDesktopRuntime()) return demoWorkspaceFile(path);
  return Call.ByName(`${bridgeName}.WorkspaceFile`, path) as Promise<WorkspaceFile>;
}

export async function listWorkspaceChanges(): Promise<WorkspaceChangeSet> {
  if (!isDesktopRuntime()) return demoWorkspaceChanges();
  return Call.ByName(`${bridgeName}.WorkspaceChanges`) as Promise<WorkspaceChangeSet>;
}

export async function readWorkspaceChange(path: string): Promise<WorkspaceChange> {
  if (!isDesktopRuntime()) return demoWorkspaceChange(path);
  return Call.ByName(`${bridgeName}.WorkspaceChange`, path) as Promise<WorkspaceChange>;
}

export async function cancelActive(includeChildren = false): Promise<boolean> {
  if (!isDesktopRuntime()) return true;
  return Call.ByName(`${bridgeName}.CancelActive`, includeChildren) as Promise<boolean>;
}

export async function guide(sessionId: string, runId: string, text: string, attachments: Attachment[] = []): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.Guide`, sessionId, runId, text, attachments);
}

export async function importAttachment(sessionId: string, file: File): Promise<Attachment> {
  const encoded = await fileToBase64(file);
  if (!isDesktopRuntime()) {
    return { id: `demo-${Date.now()}`, name: file.name, mimeType: file.type, path: `data:${file.type};base64,${encoded}`, size: file.size };
  }
  return Call.ByName(`${bridgeName}.ImportAttachment`, sessionId, file.name, file.type, encoded) as Promise<Attachment>;
}

export async function importClipboardImage(sessionId: string): Promise<Attachment | null> {
  if (!isDesktopRuntime()) return null;
  return Call.ByName(`${bridgeName}.ImportClipboardImage`, sessionId) as Promise<Attachment | null>;
}

export async function attachmentDataURL(sessionId: string, attachment: Attachment): Promise<string> {
  if (!isDesktopRuntime()) return attachment.path.startsWith("data:image/") ? attachment.path : "";
  return Call.ByName(`${bridgeName}.AttachmentDataURL`, sessionId, attachment) as Promise<string>;
}
export async function getPullRequestDashboard(): Promise<PullRequestDashboard> {
  if (!isDesktopRuntime()) return demoDashboard();
  return Call.ByName(`${bridgeName}.PullRequestDashboard`) as Promise<PullRequestDashboard>;
}

export async function getPullRequestDetail(number: number): Promise<PullRequestDetailResponse> {
  if (!isDesktopRuntime()) return { pullRequest: cloneDemo(demoPullRequest), monitor: cloneDemo(demoMonitor) };
  return Call.ByName(`${bridgeName}.PullRequestDetail`, number) as Promise<PullRequestDetailResponse>;
}

export async function mutatePullRequest(request: PullRequestMutationRequest): Promise<PullRequestDetailResponse> {
  if (!isDesktopRuntime()) {
    applyDemoMutation(request);
    return { pullRequest: cloneDemo(demoPullRequest), monitor: cloneDemo(demoMonitor) };
  }
  return Call.ByName(`${bridgeName}.MutatePullRequest`, request) as Promise<PullRequestDetailResponse>;
}

export async function setPullRequestMonitor(number: number, enabled: boolean): Promise<PullRequestMonitorState> {
  if (!isDesktopRuntime()) {
    demoMonitor = {
      number,
      enabled,
      status: enabled ? "watching" : "disabled",
      lastCheckedAt: enabled ? new Date().toISOString() : undefined,
    };
    emitDemoMonitor();
    return cloneDemo(demoMonitor);
  }
  return Call.ByName(`${bridgeName}.SetPullRequestMonitor`, number, enabled) as Promise<PullRequestMonitorState>;
}

export async function openExternalURL(rawURL: string): Promise<void> {
  const url = new URL(rawURL);
  if (url.protocol !== "https:" && url.protocol !== "http:") throw new Error("Only HTTP(S) links can be opened");
  if (!isDesktopRuntime()) {
    window.open(url, "_blank", "noopener,noreferrer");
    return;
  }
  await Browser.OpenURL(url);
}


export function subscribe(onEvent: (event: RuntimeEvent) => void): () => void {
  if (!isDesktopRuntime()) return () => undefined;
  return Events.On(EVENT_NAME, (payload: unknown) => {
    const value = payload as Record<string, unknown>;
    onEvent((value.data ?? payload) as RuntimeEvent);
  });
}
export function subscribePullRequests(onEvent: (state: PullRequestMonitorState) => void): () => void {
  if (!isDesktopRuntime()) {
    demoMonitorListeners.add(onEvent);
    return () => demoMonitorListeners.delete(onEvent);
  }
  return Events.On(PULL_REQUEST_EVENT, (payload: unknown) => {
    const value = payload as Record<string, unknown>;
    onEvent((value.data ?? payload) as PullRequestMonitorState);
  });
}


export interface SessionMenuEvent {
  action: "rename" | "error";
  sessionId?: string;
  error?: string;
}

export function subscribeSessionMenu(onEvent: (event: SessionMenuEvent) => void): () => void {
  if (!isDesktopRuntime()) return () => undefined;
  return Events.On(SESSION_MENU_EVENT, (payload: unknown) => {
    const value = payload as Record<string, unknown>;
    onEvent((value.data ?? payload) as SessionMenuEvent);
  });
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("attachment read failed"));
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1] ?? "");
    reader.readAsDataURL(file);
  });
}

const demoWorkspaceDirectories: Record<string, WorkspaceDirectory> = {
  "": { path: "", entries: [
    { name: "frontend", path: "frontend", directory: true, size: 31 },
  ] },
  frontend: { path: "frontend", entries: [
    { name: "src", path: "frontend/src", directory: true, size: 28 },
  ] },
  "frontend/src": { path: "frontend/src", entries: [
    { name: "Timeline.tsx", path: "frontend/src/components/Timeline.tsx", directory: false, size: 28420 },
    { name: "styles.css", path: "frontend/src/styles.css", directory: false, size: 48210 },
    { name: "App.tsx", path: "frontend/src/App.tsx", directory: false, size: 17321 },
    { name: "components", path: "frontend/src/components", directory: true, size: 19 },
  ] },
  "frontend/src/components": { path: "frontend/src/components", entries: [
    { name: "Timeline.tsx", path: "frontend/src/components/Timeline.tsx", directory: false, size: 28420 },
    { name: "Sidebar.tsx", path: "frontend/src/components/Sidebar.tsx", directory: false, size: 10320 },
    { name: "ThreadSurface.tsx", path: "frontend/src/components/ThreadSurface.tsx", directory: false, size: 64210 },
  ] },
  internal: { path: "internal", entries: [
    { name: "app", path: "internal/app", directory: true },
    { name: "desktop", path: "internal/desktop", directory: true },
  ] },
};

function demoWorkspaceDirectory(path: string): WorkspaceDirectory {
  return structuredClone(demoWorkspaceDirectories[path] ?? { path, entries: [] });
}

function demoWorkspaceFile(path: string): WorkspaceFile {
  const samples: Record<string, string> = {
    "README.md": "# Azem\n\nA local-first coding agent with a shared Go runtime and desktop workspace.\n",
    "go.mod": "module github.com/Viking602/azem\n\ngo 1.25.0\n",
    "frontend/src/App.tsx": "export default function App() {\n  return <div className=\"desktop-shell\">Azem</div>;\n}\n",
    "frontend/src/components/Timeline.tsx": "return <div className=\"streaming-text\">\n  {reduceMotion ? text : presentation.chunks.map(\n    (chunk) => <motion.span\n      initial={{ opacity: 0, filter: \"blur(5px)\" }}\n      animate={{ opacity: 1, filter: \"blur(0px)\" }}\n      transition={streamReveal}\n    >{chunk.text}</motion.span>\n  )}\n</div>;\n",
  };
  const content = samples[path] ?? `// Preview for ${path}\n`;
  return { path, name: path.split("/").at(-1) ?? path, kind: "text", language: path.split(".").at(-1), content, size: content.length, lineCount: content.split("\n").length };
}

function demoWorkspaceChanges(): WorkspaceChangeSet {
  return {
    repository: true, branch: "main", base: "HEAD", additions: 186, deletions: 32,
    files: [
      { path: "frontend/src/components/Sidebar.tsx", status: "modified", additions: 38, deletions: 12 },
      { path: "frontend/src/styles.css", status: "modified", additions: 92, deletions: 14 },
      { path: "frontend/src/components/WorkspaceFilesPage.tsx", status: "added", additions: 41, deletions: 0 },
      { path: "docs/desktop.md", status: "modified", additions: 15, deletions: 6 },
    ],
  };
}

function demoWorkspaceChange(path: string): WorkspaceChange {
  const file = demoWorkspaceChanges().files.find((item) => item.path === path) ?? { path, status: "modified" as const, additions: 0, deletions: 0 };
  if (path === "frontend/src/styles.css") return {
    ...file,
    patch: `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1,1 +1,5 @@\n- transition: opacity .14s ease;\n+ transition: opacity var(--motion-fast) var(--ease-out),\n+             transform var(--motion-fast) var(--ease-out);\n+ @media (prefers-reduced-motion: reduce) {\n+   *, *::before, *::after { animation-duration: .01ms; }\n+ }`,
  };
  return {
    ...file,
    patch: `diff --git a/${file.path} b/${file.path}\n--- a/${file.path}\n+++ b/${file.path}\n@@ -18,5 +18,9 @@\n export default function App() {\n-  return <main>Azem</main>;\n+  return <main className=\"workspace\">Azem</main>;\n+  // Preserve reduced-motion behavior.\n+  // Reveal only the newest streaming glyphs.\n+  // Keep the transition within the shared motion tokens.\n }`,
  };
}

const demoAuthor = { login: "Viking602", name: "Viking", url: "https://github.com/Viking602", avatarUrl: "https://avatars.githubusercontent.com/Viking602?size=64" };
let demoMonitor: PullRequestMonitorState = { number: 128, enabled: false, status: "disabled" };
const demoMonitorListeners = new Set<(state: PullRequestMonitorState) => void>();

let demoPullRequest: PullRequest = {
  number: 128,
  title: "UI motion system",
  state: "OPEN",
  draft: false,
  author: demoAuthor,
  headRefName: "main",
  headRefOid: "22043f967b31270b19f6a7b772b9154988e83061",
  baseRefName: "main",
  additions: 4332,
  deletions: 52,
  changedFiles: 38,
  reviewDecision: "",
  mergeable: "MERGEABLE",
  mergeStateStatus: "CLEAN",
  url: "https://github.com/Viking602/azem/pull/128",
  createdAt: "2026-08-01T16:16:46Z",
  updatedAt: "2026-08-02T13:14:54Z",
  body: "## Summary\n\n- add a Wails v3 desktop entry backed by the existing Azem bootstrap and controlled action bridge\n- implement the supplied three-panel GUI design across timeline, diffs, approvals, subagents, recovery, extensions, command palette, and categorized settings\n- batch high-frequency runtime events per animation frame and keep secondary surfaces layered over the main workspace\n\n## Verification\n\n- `go test ./...`\n- `make test-gui`\n- `make gui`",
  maintainerCanModify: true,
  autoMergeEnabled: false,
  reviewRequests: [],
  reviews: [],
  comments: [],
  commits: [
    {
      oid: "22043f967b31270b19f6a7b772b9154988e83061",
      headline: "feat: add Wails desktop workspace",
      committedAt: "2026-08-01T16:16:46Z",
      authors: [demoAuthor],
    },
    {
      oid: "6f52f713fcd1de818e05c7c38c3df1ac9621b968",
      headline: "fix(gui): integrate native chrome and model picker",
      committedAt: "2026-08-02T02:06:42Z",
      authors: [demoAuthor],
    },
  ],
  files: [
    { path: "frontend/src/App.tsx", additions: 132, deletions: 18 },
    { path: "frontend/src/styles.css", additions: 1180, deletions: 20 },
    { path: "internal/desktop/bridge.go", additions: 270, deletions: 0 },
  ],
  checks: { total: 6, pending: 0, passing: 6, failing: 0, neutral: 0, skipped: 0 },
  checksDetail: [],
  activity: [
    { kind: "created", actor: demoAuthor, title: "opened pull request", at: "2026-08-01T16:16:46Z", url: "https://github.com/Viking602/azem/pull/24" },
    { kind: "commit", actor: demoAuthor, title: "feat: add Wails desktop workspace", at: "2026-08-01T16:16:46Z", oid: "22043f967b31270b19f6a7b772b9154988e83061" },
    { kind: "commit", actor: demoAuthor, title: "fix(gui): integrate native chrome and model picker", at: "2026-08-02T02:06:42Z", oid: "6f52f713fcd1de818e05c7c38c3df1ac9621b968" },
  ],
  allowedMergeMethods: ["merge", "squash", "rebase"],
};

function demoDashboard(): PullRequestDashboard {
  const summary = summaryFromDemo();
  return {
    capability: { available: true },
    repository: {
      nameWithOwner: "Viking602/azem",
      url: "https://github.com/Viking602/azem",
      defaultBranch: "main",
      viewerPermission: "ADMIN",
      viewerLogin: "Viking602",
      allowedMergeMethods: ["merge", "squash", "rebase"],
    },
    currentBranch: demoPullRequest.headRefName,
    current: demoPullRequest.state === "OPEN" ? summary : undefined,
    createdByViewer: demoPullRequest.state === "OPEN" ? [summary] : [],
    needsReview: [],
    open: demoPullRequest.state === "OPEN" ? [summary] : [],
    refreshedAt: new Date().toISOString(),
  };
}

function summaryFromDemo() {
  const {
    body: _body, createdAt: _createdAt, closedAt: _closedAt, mergedAt: _mergedAt,
    maintainerCanModify: _maintainerCanModify, autoMergeEnabled: _autoMergeEnabled,
    autoMergeMethod: _autoMergeMethod, reviewRequests: _reviewRequests, reviews: _reviews,
    comments: _comments, commits: _commits, files: _files, checksDetail: _checksDetail,
    activity: _activity, allowedMergeMethods: _allowedMergeMethods, ...summary
  } = demoPullRequest;
  return cloneDemo(summary);
}

function applyDemoMutation(request: PullRequestMutationRequest) {
  const now = new Date().toISOString();
  switch (request.kind) {
    case "edit":
      demoPullRequest = { ...demoPullRequest, title: request.title || demoPullRequest.title, body: request.body ?? demoPullRequest.body, updatedAt: now };
      break;
    case "add_reviewer":
      if (request.login && !demoPullRequest.reviewRequests.some((actor) => actor.login === request.login)) {
        demoPullRequest = { ...demoPullRequest, reviewRequests: [...demoPullRequest.reviewRequests, { login: request.login }] };
      }
      break;
    case "remove_reviewer":
      demoPullRequest = { ...demoPullRequest, reviewRequests: demoPullRequest.reviewRequests.filter((actor) => actor.login !== request.login) };
      break;
    case "comment": {
      const comment = { id: `demo-comment-${Date.now()}`, author: demoAuthor, body: request.body || "", createdAt: now };
      demoPullRequest = {
        ...demoPullRequest,
        comments: [...demoPullRequest.comments, comment],
        activity: [...demoPullRequest.activity, { kind: "comment", actor: demoAuthor, title: "commented", body: comment.body, at: now }],
      };
      break;
    }
    case "review": {
      const state = request.reviewKind === "approve" ? "APPROVED" : request.reviewKind === "request_changes" ? "CHANGES_REQUESTED" : "COMMENTED";
      demoPullRequest = {
        ...demoPullRequest,
        reviewDecision: state === "APPROVED" ? "APPROVED" : state === "CHANGES_REQUESTED" ? "CHANGES_REQUESTED" : demoPullRequest.reviewDecision,
        reviews: [...demoPullRequest.reviews, { id: `demo-review-${Date.now()}`, author: demoAuthor, state, body: request.body, submittedAt: now }],
        activity: [...demoPullRequest.activity, { kind: "review", actor: demoAuthor, title: "submitted a review", body: request.body, state, at: now }],
      };
      break;
    }
    case "ready":
      demoPullRequest = { ...demoPullRequest, draft: false };
      break;
    case "draft":
      demoPullRequest = { ...demoPullRequest, draft: true };
      break;
    case "close":
      demoPullRequest = { ...demoPullRequest, state: "CLOSED", closedAt: now };
      break;
    case "reopen":
      demoPullRequest = { ...demoPullRequest, state: "OPEN", closedAt: undefined };
      break;
    case "merge":
      demoPullRequest = { ...demoPullRequest, state: "MERGED", mergedAt: now, autoMergeEnabled: false };
      break;
    case "enable_auto_merge":
      demoPullRequest = { ...demoPullRequest, autoMergeEnabled: true, autoMergeMethod: request.mergeMethod };
      break;
    case "disable_auto_merge":
      demoPullRequest = { ...demoPullRequest, autoMergeEnabled: false, autoMergeMethod: undefined };
      break;
  }
}

function emitDemoMonitor() {
  for (const listener of demoMonitorListeners) listener(cloneDemo(demoMonitor));
}

function cloneDemo<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
