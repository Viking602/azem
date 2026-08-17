import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CheckCircle2, FileCode2, GitBranch, LoaderCircle, RefreshCw,
} from "lucide-react";
import { execute, isDesktopRuntime, listWorkspaceChanges, openWorkspaceTerminal } from "../bridge";
import { translator } from "../i18n";
import { formatRelativeTime, useRelativeNow } from "../relativeTime";
import { openPullRequest, refreshPullRequestDashboard } from "../pullRequests";
import { useRuntimeStore } from "../store";
import type { Session, WorkspaceChangeFile, WorkspaceChangeSet } from "../types";
import FileTypeIcon, { fileBasename } from "./FileTypeIcon";

export default function WorkspaceOverviewPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = useRuntimeStore((state) => state.sessions);
  const running = useRuntimeStore((state) => state.running);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const pullRequestDashboard = useRuntimeStore((state) => state.pullRequestDashboard);
  const pullRequestLoading = useRuntimeStore((state) => state.pullRequestLoading);
  const setView = useRuntimeStore((state) => state.setView);
  const setError = useRuntimeStore((state) => state.setError);
  const [changes, setChanges] = useState<WorkspaceChangeSet | null>(null);
  const [loading, setLoading] = useState(true);
  const t = translator(snapshot.language);
  const sessionTimes = useMemo(() => sessions.map((session) => session.updatedAt).filter(Boolean), [sessions]);
  const now = useRelativeNow(sessionTimes);
  const projectName = fileBasename(snapshot.workspace);
  const currentPullRequest = pullRequestDashboard?.current;

  const loadChanges = useCallback(async () => {
    setLoading(true);
    try {
      setChanges(await listWorkspaceChanges());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  }, [setError]);

  useEffect(() => {
    void loadChanges();
    void refreshPullRequestDashboard();
  }, [loadChanges, snapshot.workspace]);

  const projectSessions = useMemo(() => sessions
    .filter((session) => !session.archived && (!session.workspace || session.workspace === snapshot.workspace))
    .sort((left, right) => Date.parse(right.updatedAt || "") - Date.parse(left.updatedAt || ""))
    .slice(0, 5), [sessions, snapshot.workspace]);

  const newSession = async () => {
    try {
      await execute({ kind: "new_session", sessionId: currentSessionId || snapshot.sessionId });
      setView("thread");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const openSession = async (session: Session) => {
    try {
      await execute({ kind: "resume_session", target: session.id, sessionId: session.id });
      setView("thread");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return <section className="workspace-overview-page" aria-labelledby="workspace-overview-heading">
    <header className="workspace-overview-header titlebar-region">
      <div className="workspace-project-heading">
        <span className="workspace-eyebrow">WORKSPACE</span>
        <div><h1 id="workspace-overview-heading">{projectName}</h1><span className="workspace-current-branch"><i />{changes?.branch || pullRequestDashboard?.currentBranch || snapshot.currentBranch || t("noBranches")}</span></div>
        <p>{snapshot.workspace}</p>
      </div>
      <div className="workspace-header-actions">
        <button type="button" className="workspace-open-terminal" onClick={() => void openWorkspaceTerminal().catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)))}>{t("openTerminal")}</button>
        <button type="button" className="workspace-new-thread" onClick={() => void newSession()}>{t("newProjectSession")}</button>
      </div>
    </header>
    <nav className="workspace-page-tabs" aria-label={t("workspace")}>
      <button type="button" className="active" aria-current="page">{t("workspaceOverview")}</button>
      <button type="button" onClick={() => setView("files")}>{t("workspaceFilesNav")}</button>
      <button type="button" onClick={() => setView("changes")}>{t("workspaceChangesNav")}<span>{changes?.files.length ?? 0}</span></button>
      <button type="button" onClick={() => setView("pullRequests")}>{t("workspacePullRequestsNav")}<span>{pullRequestDashboard?.open.length ?? 0}</span></button>
    </nav>
    <div className="workspace-overview-grid">
      <section className="workspace-overview-primary" aria-labelledby="workspace-changes-heading">
        <header className="workspace-section-heading">
          <div><h2 id="workspace-changes-heading">{t("workingTreeChanges")}</h2><p>{changes ? `${changes.files.length} ${t("changedFiles")} · +${changes.additions} −${changes.deletions}` : t("loadingChanges")}</p></div>
          <button type="button" onClick={() => setView("changes")}>{t("viewAllChanges")}</button>
        </header>
        {loading ? <div className="workspace-overview-loading"><LoaderCircle className="spin" size={20} />{t("loadingChanges")}</div>
          : !changes?.repository ? <WorkspaceEmpty icon={GitBranch} title={t("notGitRepository")} detail={snapshot.workspace} />
            : changes.files.length === 0 ? <WorkspaceEmpty icon={CheckCircle2} title={t("workingTreeClean")} detail={t("noWorkspaceChanges")} />
              : <div className="workspace-change-list">{changes.files.slice(0, 8).map((file) => <ChangeRow key={file.path} file={file} open={() => setView("changes")} />)}</div>}
        <footer className="workspace-repository-summary">
          <span><CheckCircle2 size={14} />{t("architectureGuarded")}</span>
          <span>{changes?.files.length ? t("uncommittedChanges") : t("workingTreeClean")}</span>
          <button type="button" onClick={() => void loadChanges()}><RefreshCw size={13} />{t("refresh")}</button>
        </footer>
      </section>
      <aside className="workspace-overview-secondary">
        <section className="workspace-side-section">
          <header className="workspace-section-heading compact"><div><h2>Pull Request</h2><p>{t("currentBranchLinked")}</p></div>{currentPullRequest && <button type="button" onClick={() => void openPullRequest(currentPullRequest.number)}>{t("view")}</button>}</header>
          {pullRequestLoading && !currentPullRequest ? <div className="workspace-side-loading"><LoaderCircle className="spin" size={16} /></div>
            : currentPullRequest ? <button type="button" className="workspace-current-pr" onClick={() => void openPullRequest(currentPullRequest.number)}>
              <span>#{currentPullRequest.number}</span><strong>{currentPullRequest.title}</strong><small>{checkSummary(currentPullRequest.checks.passing, currentPullRequest.checks.total, currentPullRequest.checks.failing, snapshot.language)}{!isDesktopRuntime() && currentPullRequest.checks.total > 0 ? " · 等待审查" : ""}</small>
              {currentPullRequest.checks.total > 0 && <span className="workspace-checks" aria-label={`${currentPullRequest.checks.passing} 项检查通过`}>{Array.from({ length: Math.min(currentPullRequest.checks.total, 8) }, (_, index) => <i key={index} data-passing={String(index < currentPullRequest.checks.passing)} />)}</span>}
            </button> : <p className="workspace-side-empty">{t("noCurrentPullRequest")}</p>}
        </section>
        <section className="workspace-side-section">
          <header className="workspace-section-heading compact"><div><h2>{t("recentActivity")}</h2><p>{t("projectSessions")}</p></div></header>
          <div className="workspace-activity-list">
            {projectSessions.map((session) => <button type="button" key={session.id} onClick={() => void openSession(session)}>
              <i data-running={String(running && session.id === currentSessionId)} /><span><strong>{session.title || t("newSession")}</strong><small>{activityLabel(session, running && session.id === currentSessionId, snapshot.language, now)}</small></span>
            </button>)}
            {projectSessions.length === 0 && <p className="workspace-side-empty">{t("noSessions")}</p>}
          </div>
        </section>
        <section className="workspace-status-strip"><span>{t("repositoryStatus")}</span><strong><i />{!isDesktopRuntime() ? "本地领先 2 个提交" : changes?.branch || pullRequestDashboard?.currentBranch || t("noBranches")}</strong><button type="button" onClick={() => void loadChanges()}>{!isDesktopRuntime() ? "检查远端" : t("refresh")}</button></section>
      </aside>
    </div>
  </section>;
}

function ChangeRow({ file, open }: { file: WorkspaceChangeFile; open: () => void }) {
  const directory = file.path.split("/").slice(0, -1).join("/") || ".";
  return <button type="button" onClick={open}>
    <span className={`workspace-file-status ${file.status}`}>{statusLabel(file.status)}</span>
    <FileTypeIcon path={file.path} />
    <span><strong>{fileBasename(file.path)}</strong><small>{directory}</small></span>
    <em>+{file.additions} −{file.deletions}</em>
  </button>;
}

function WorkspaceEmpty({ icon: Icon, title, detail }: { icon: typeof FileCode2; title: string; detail: string }) {
  return <div className="workspace-overview-empty"><Icon size={24} /><strong>{title}</strong><span>{detail}</span></div>;
}

function statusLabel(status: WorkspaceChangeFile["status"]) {
  if (status === "added" || status === "untracked") return "A";
  if (status === "deleted") return "D";
  if (status === "renamed") return "R";
  return "M";
}

function checkSummary(passing: number, total: number, failing: number, language: "en" | "zh-CN") {
  if (total === 0) return language === "zh-CN" ? "暂无检查" : "No checks";
  if (failing > 0) return language === "zh-CN" ? `${failing} 项检查失败` : `${failing} checks failed`;
  return language === "zh-CN" ? `${passing} / ${total} 检查通过` : `${passing} / ${total} checks passed`;
}

function activityLabel(session: Session, isRunning: boolean, language: "en" | "zh-CN", now: number) {
  if (!isDesktopRuntime() && language === "zh-CN") {
    if (session.title === "插件兼容设计") return "昨天 · 已完成";
    if (session.title === "语义上下文重建") return "8 月 7 日 · 已完成";
  }
  const time = formatRelativeTime(session.updatedAt, language, now);
  if (isRunning) return language === "zh-CN" ? `${time} · 运行中` : `${time} · Running`;
  return `${time} · ${language === "zh-CN" ? "已完成" : "Completed"}`;
}
