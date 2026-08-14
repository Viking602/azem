import { useEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent } from "react";
import { createPortal } from "react-dom";
import {
  ChevronDown, ChevronRight, CircleDotDashed, FolderOpen, FolderPlus,
  GitPullRequest, Plus, Search, Settings, X,
} from "lucide-react";
import { createProject, execute, isDesktopRuntime, openProject, openProjectSession, selectProjectFolder, subscribeSessionMenu } from "../bridge";
import { translator } from "../i18n";
import { formatRelativeTime, useRelativeNow } from "../relativeTime";
import { openPullRequest, refreshPullRequestDashboard } from "../pullRequests";
import { useRuntimeStore } from "../store";
import type { ActionKind, View } from "../types";

export default function Sidebar() {
  const [openProjects, setOpenProjects] = useState<Record<string, boolean>>({});
  const [showAllSessions, setShowAllSessions] = useState(false);
  const [renaming, setRenaming] = useState<{ id: string; title: string } | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = useRuntimeStore((state) => state.sessions);
  const projects = useRuntimeStore((state) => state.projects);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const blocks = useRuntimeStore((state) => state.blocks);
  const running = useRuntimeStore((state) => state.running);
  const globalRunSessionId = useRuntimeStore((state) => state.globalRunSessionId);
  const view = useRuntimeStore((state) => state.view);
  const setView = useRuntimeStore((state) => state.setView);
  const startLocalDraft = useRuntimeStore((state) => state.startLocalDraft);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const setError = useRuntimeStore((state) => state.setError);
  const pullRequestDashboard = useRuntimeStore((state) => state.pullRequestDashboard);
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceChangedFiles = useRuntimeStore((state) => state.workspaceChangedFiles);
  const selectPullRequest = useRuntimeStore((state) => state.selectPullRequest);
  const t = translator(snapshot.language);
  const catalog = projects.some((project) => project.workspace === snapshot.workspace)
    ? projects
    : [{ workspace: snapshot.workspace, updatedAt: "" }, ...projects];
  const currentBranch = pullRequestDashboard?.currentBranch || branches.find((branch) => branch.current)?.name || "";
  const currentPullRequest = pullRequestDashboard?.current;
  const runningSessionId = globalRunSessionId || (running ? currentSessionId : "");
  const sessionTimes = useMemo(() => sessions.map((session) => session.updatedAt).filter(Boolean), [sessions]);
  const now = useRelativeNow(sessionTimes);

  useEffect(() => subscribeSessionMenu((event) => {
    if (event.action === "error") {
      setError(event.error || t("sessionActionFailed"));
      return;
    }
    const session = sessions.find((item) => item.id === event.sessionId);
    if (event.action === "rename" && session) setRenaming({ id: session.id, title: session.title });
  }), [sessions, setError]);

  const commitRename = async () => {
    if (!renaming) return;
    const next = renaming.title.trim();
    setRenaming(null);
    if (!next) return;
    try {
      await execute({ kind: "rename_session", target: renaming.id, name: next, sessionId: renaming.id });
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  };

  const run = async (kind: ActionKind, target = "") => {
    try {
      if (kind === "new_session" && !isDesktopRuntime()) startLocalDraft();
      if (kind === "resume_session" && !isDesktopRuntime()) {
        const url = new URL(window.location.href);
        url.searchParams.set("demo", "running");
        window.location.assign(url);
        return;
      }
      await execute({ kind, target, sessionId: currentSessionId });
      selectPullRequest(null);
      setView("thread");
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  };

  const launchProject = (workspace: string, sessionId = "") => {
    const action = sessionId ? openProjectSession(workspace, sessionId) : openProject(workspace);
    void action.catch((error) => setError(error instanceof Error ? error.message : String(error)));
  };

  return (
    <aside className="workspace-sidebar">
      <div
        className="sidebar-switcher"
        role="tablist"
        data-active={view === "thread" ? "projects" : view === "projects" ? "workspace" : "none"}
      >
        {[[t("conversations"), "thread"], [t("workspace"), "projects"]].map(([label, target]) => (
          <button
            key={label}
            role="tab"
            aria-selected={view === target}
            className={view === target ? "active" : ""}
            onClick={() => setView(target as View)}
          >
            {label}
          </button>
        ))}
      </div>
      <nav className="primary-nav" aria-label="Primary">
        <button className={view === "thread" && blocks.length === 0 && !running ? "active" : ""} onClick={() => run("new_session")}><Plus size={15} />{t("newSession")}<kbd>⌘N</kbd></button>
        <button onClick={() => setCommandOpen(true)}><Search size={15} />{t("search")}<kbd>⌘K</kbd></button>
      </nav>
      <section className="project-tree">
        <div className="sidebar-section-header">
          <div className="sidebar-section-title">{t("projects")}</div>
          <ProjectLauncher language={snapshot.language} setError={setError} />
        </div>
        {catalog.map((item) => {
          const active = item.workspace === snapshot.workspace;
          const projectName = basename(item.workspace);
          const projectOpen = openProjects[item.workspace] ?? (active || projectName === "llmux");
          const projectSessions = sessions.filter((session) => !session.archived && (session.workspace === item.workspace || (!session.workspace && active)));
          const demoPRCount = projectName === "llmux" ? 1 : projectName === "venat" ? 2 : 0;
          const prototypeDemo = snapshot.workspace.endsWith("/azem");
          const prototypePR = active && currentPullRequest ? {
            title: currentPullRequest.title,
            number: currentPullRequest.number,
            detail: currentPullRequest.checks.total > 0
              ? `${currentPullRequest.checks.passing}/${currentPullRequest.checks.total} 检查通过`
              : "等待检查",
            state: currentPullRequest.checks.failing > 0 ? "failing" : "passing",
            open: () => void openPullRequest(currentPullRequest.number),
          } : prototypeDemo && projectName === "llmux" ? {
            title: "Normalize usage limits", number: 45, detail: "2 项检查失败", state: "failing",
            open: () => undefined,
          } : null;
          const sidebarSessions = prototypePR
            ? projectSessions.filter((session) => session.title !== prototypePR.title)
            : projectSessions;
          const visibleSessions = showAllSessions && active ? sidebarSessions : sidebarSessions.slice(0, 5);
          const startProjectSession = () => active ? void run("new_session") : launchProject(item.workspace);
          return <div
            className={`project-node ${active ? "active" : ""}`}
            data-expanded={String(projectOpen)}
            data-project-layout={prototypePR ? "pull-request" : "sessions-only"}
            key={item.workspace}
          >
            <div className="project-heading">
              <button className="project-toggle" aria-expanded={projectOpen} onClick={() => setOpenProjects((open) => ({ ...open, [item.workspace]: !projectOpen }))}>
                {projectOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                <span className="project-initial" aria-hidden="true">{projectName.slice(0, 1).toUpperCase()}</span>
                <span className="project-heading-copy"><strong>{projectName}</strong><small>{active ? `${currentBranch || t("noBranches")}${workspaceChangedFiles > 0 ? ` · ${workspaceChangedFiles} 个改动` : ` · ${t("workingTreeClean")}`}` : projectName === "llmux" ? "feat/usage-store" : projectName === "venat" ? `main · ${t("workingTreeClean")}` : compactProjectPath(item.workspace)}</small></span>
                <em>{active ? projectSessions.length || "" : demoPRCount ? `${demoPRCount} PR` : projectSessions.length || ""}</em>
              </button>
              <button className="project-action project-new-session" aria-label={t("newSession")} title={t("newSession")} onClick={startProjectSession}><Plus size={15} /></button>
            </div>
            {projectOpen && <>
              {prototypePR && <div className={`sidebar-pr-row ${prototypePR.state}`}>
                <button type="button" className="sidebar-pr-main" title={prototypePR.title} onClick={prototypePR.open}>
                  <span className="sidebar-pr-icon"><GitPullRequest size={14} /></span>
                  <span className="sidebar-pr-copy"><strong>{prototypePR.title}</strong><small>#{prototypePR.number} · {prototypePR.detail}</small></span>
                  <em>PR</em>
                </button>
              </div>}
              <div className="thread-list">
                {sidebarSessions.length === 0 && <div className="empty-sidebar"><CircleDotDashed size={13} />{t("noSessions")}</div>}
                {visibleSessions.map((session) => renaming?.id === session.id ? (
                  <form key={session.id} className="thread-rename" onSubmit={(event) => { event.preventDefault(); void commitRename(); }}>
                    <input autoFocus value={renaming.title} onChange={(event) => setRenaming({ id: session.id, title: event.target.value })}
                      onBlur={() => setRenaming(null)} onKeyDown={(event) => { if (event.key === "Escape") setRenaming(null); }} aria-label={t("renameChat")} />
                  </form>
                ) : (
                  <button key={session.id} className={session.id === currentSessionId && view === "thread" ? "active" : ""} onClick={() => active ? void run("resume_session", session.id) : launchProject(item.workspace, session.id)} title={session.title}
                    aria-busy={session.id === runningSessionId}
                    style={{ "--custom-contextmenu": session.pinned ? "session-pinned" : "session", "--custom-contextmenu-data": session.id } as CSSProperties}>
                    <span className="session-state-dot" data-running={String(session.id === runningSessionId)} aria-hidden="true" />
                    <span className="session-copy"><strong>{session.title || t("newSession")}</strong><small>{sidebarSessionLabel(session.title, session.updatedAt, session.id === runningSessionId, snapshot.language, now)}</small></span>
                    {session.id === runningSessionId && <i className="session-running-indicator" aria-hidden="true" />}
                    {session.unread && <i className="session-unread" title={t("unread")} />}
                  </button>
                ))}
                {active && projectSessions.length > 5 && <button className="show-more-sessions" onClick={() => setShowAllSessions((show) => !show)}>{t(showAllSessions ? "showLess" : "showMore")}</button>}
              </div>
            </>}
          </div>;
        })}
      </section>
      <div className="sidebar-footer">
        <button onClick={() => setSettingsOpen(true)}><Settings size={15} />{t("settings")}<kbd>⌘,</kbd></button>
      </div>
    </aside>
  );
}

function basename(path: string) {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace";
}

function compactProjectPath(path: string) {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.slice(-2, -1)[0] || path;
}

function sidebarSessionLabel(title: string, updatedAt: string, running: boolean, language: "en" | "zh-CN", now: number) {
  if (language === "zh-CN" && !isDesktopRuntime()) {
    if (title === "插件兼容设计") return "昨天 · 已完成";
    if (title === "语义上下文重建") return "8 月 7 日 · 已完成";
    if (title === "发布 v0.2.4") return "周五 · 等待检查";
  }
  const time = formatRelativeTime(updatedAt, language, now);
  if (running) return language === "zh-CN" ? `${time} · 运行中` : `${time} · Running`;
  return time;
}

function ProjectLauncher({ language, setError }: { language: "en" | "zh-CN"; setError: (message: string) => void }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("new-project");
  const [location, setLocation] = useState("~/Projects");
  const [initialiseGit, setInitialiseGit] = useState(true);
  const [busy, setBusy] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const t = translator(language);

  useEffect(() => {
    if (!menuOpen && !creating) return;
    const onPointerDown = (event: PointerEvent) => {
      if (menuOpen && root.current && !root.current.contains(event.target as Node)) setMenuOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) {
        setMenuOpen(false);
        setCreating(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [busy, creating, menuOpen]);

  const chooseFolder = async () => {
    setMenuOpen(false);
    setBusy(true);
    try {
      const path = await selectProjectFolder(t("chooseProjectFolder"), t("openProject"));
      if (path) await openProject(path);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  const chooseLocation = async () => {
    setBusy(true);
    try {
      const path = await selectProjectFolder(language === "zh-CN" ? "选择项目存放位置" : "Choose project location", language === "zh-CN" ? "选择" : "Choose");
      if (path) setLocation(path);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  const submitProject = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      const path = await createProject(name, location, initialiseGit);
      await openProject(path);
      setCreating(false);
      setName("new-project");
      setLocation("~/Projects");
      setInitialiseGit(true);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  return <div className="project-launcher" ref={root}>
    <button className="project-add-button" aria-label={t("addProject")} aria-expanded={menuOpen} aria-haspopup="menu" title={t("addProject")}
      disabled={busy} onClick={() => setMenuOpen((open) => !open)}><Plus size={14} /></button>
    {menuOpen && <div className="project-add-menu" role="menu">
      <button role="menuitem" onClick={() => void chooseFolder()}>
        <FolderOpen size={16} /><span><strong>{t("chooseProjectFolder")}</strong><small>{t("chooseProjectFolderHint")}</small></span>
      </button>
      <button role="menuitem" onClick={() => { setMenuOpen(false); setCreating(true); }}>
        <FolderPlus size={16} /><span><strong>{t("newProject")}</strong><small>{t("newProjectHint")}</small></span>
      </button>
    </div>}
    {creating && createPortal(<div className="project-create-backdrop" onMouseDown={(event) => {
      if (event.target === event.currentTarget && !busy) setCreating(false);
    }}>
      <form className="project-create-dialog" role="dialog" aria-modal="true" aria-labelledby="project-create-title" onSubmit={submitProject}>
        <header><div><span>NEW PROJECT</span><h2 id="project-create-title">{t("newProject")}</h2></div><button type="button" onClick={() => setCreating(false)} aria-label={t("cancel")}><X size={17} /></button></header>
        <div className="project-create-body">
          <label htmlFor="project-name"><span>{t("projectName")}</span><input id="project-name" type="text" autoFocus maxLength={100} required value={name} placeholder={t("projectNamePlaceholder")} onFocus={(event) => event.currentTarget.select()} onChange={(event) => setName(event.target.value)} /></label>
          <label><span>{language === "zh-CN" ? "存放位置" : "Location"}</span><div className="path-field"><input type="text" value={location} onChange={(event) => setLocation(event.target.value)} /><button type="button" onClick={() => void chooseLocation()}>{language === "zh-CN" ? "浏览…" : "Browse…"}</button></div></label>
          <label className="checkbox-row"><input type="checkbox" checked={initialiseGit} onChange={(event) => setInitialiseGit(event.target.checked)} /><span><strong>{language === "zh-CN" ? "初始化 Git 仓库" : "Initialise Git repository"}</strong><small>{language === "zh-CN" ? "创建 .git 并使用 main 作为默认分支" : "Create .git and use main as the default branch"}</small></span></label>
        </div>
        <footer>
          <button type="button" className="small-button" disabled={busy} onClick={() => setCreating(false)}>{t("cancel")}</button>
          <button type="submit" className="settings-primary" disabled={busy || !name.trim()}>{language === "zh-CN" ? "创建并打开" : "Create and open"}</button>
        </footer>
      </form>
    </div>, document.body)}
  </div>;
}
