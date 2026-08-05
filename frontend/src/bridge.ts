import { Browser, Call, Dialogs, Events } from "@wailsio/runtime";
import type {
  ActionRequest, Attachment, PullRequest, PullRequestDashboard, PullRequestDetailResponse,
  PullRequestMonitorState, PullRequestMutationRequest, RuntimeEvent, Snapshot, TurnRequest,
} from "./types";

const EVENT_NAME = "azem:event";
const SESSION_MENU_EVENT = "azem:session-menu";
const PULL_REQUEST_EVENT = "azem:pull-request";
const bridgeName = "github.com/Viking602/azem/internal/desktop.Bridge";

export const isDesktopRuntime = () =>
  typeof window !== "undefined" &&
  (location.protocol === "wails:" || Boolean((window as Window & { _wails?: { environment?: { OS?: string } } })._wails?.environment?.OS));

export const demoSnapshot: Snapshot = {
  workspace: "/workspace/azem",
  sessionId: "session-demo",
  provider: "chatgpt",
  model: "gpt-5.6-sol",
  reasoning: "high",
  agentMode: "single",
  language: "zh-CN",
  approvalMode: "auto_review",
  queueMode: "queue",
  subagentConcurrency: 2,
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

export async function createProject(name: string): Promise<string> {
  if (!isDesktopRuntime()) return "";
  return Call.ByName(`${bridgeName}.CreateProject`, name) as Promise<string>;
}

export async function openProject(path: string): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.OpenProject`, path);
}

export async function cancelActive(includeChildren = false): Promise<boolean> {
  if (!isDesktopRuntime()) return true;
  return Call.ByName(`${bridgeName}.CancelActive`, includeChildren) as Promise<boolean>;
}

export async function guide(sessionId: string, runId: string, text: string): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.Guide`, sessionId, runId, text);
}

export async function importAttachment(sessionId: string, file: File): Promise<Attachment> {
  const encoded = await fileToBase64(file);
  if (!isDesktopRuntime()) {
    return { id: `demo-${Date.now()}`, name: file.name, mimeType: file.type, path: file.name, size: file.size };
  }
  return Call.ByName(`${bridgeName}.ImportAttachment`, sessionId, file.name, file.type, encoded) as Promise<Attachment>;
}

export async function importClipboardImage(sessionId: string): Promise<Attachment | null> {
  if (!isDesktopRuntime()) return null;
  return Call.ByName(`${bridgeName}.ImportClipboardImage`, sessionId) as Promise<Attachment | null>;
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

const demoAuthor = { login: "Viking602", name: "Viking", url: "https://github.com/Viking602", avatarUrl: "https://avatars.githubusercontent.com/Viking602?size=64" };
let demoMonitor: PullRequestMonitorState = { number: 24, enabled: false, status: "disabled" };
const demoMonitorListeners = new Set<(state: PullRequestMonitorState) => void>();

let demoPullRequest: PullRequest = {
  number: 24,
  title: "feat: add Wails desktop GUI",
  state: "OPEN",
  draft: false,
  author: demoAuthor,
  headRefName: "codex/gui-desktop-experience",
  headRefOid: "22043f967b31270b19f6a7b772b9154988e83061",
  baseRefName: "main",
  additions: 4332,
  deletions: 52,
  changedFiles: 38,
  reviewDecision: "",
  mergeable: "MERGEABLE",
  mergeStateStatus: "CLEAN",
  url: "https://github.com/Viking602/azem/pull/24",
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
  checks: { total: 0, pending: 0, passing: 0, failing: 0, neutral: 0, skipped: 0 },
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
