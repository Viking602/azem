import { useEffect, useRef, useState, type CSSProperties, type FormEvent } from "react";
import { createPortal } from "react-dom";
import {
  Box, CircleDotDashed, Command, Folder, FolderOpen, FolderPlus, GitBranch, Github,
  GitPullRequest, MoreHorizontal, Plus, Search, Settings, SquarePen,
} from "lucide-react";
import { createProject, execute, openExternalURL, openProject, openProjectSession, selectProjectFolder, subscribeSessionMenu } from "../bridge";
import { translator } from "../i18n";
import { openPullRequest, refreshPullRequestDashboard } from "../pullRequests";
import { useRuntimeStore } from "../store";
import type { View } from "../types";

export default function Sidebar() {
  const [openProjects, setOpenProjects] = useState<Record<string, boolean>>({});
  const [showAllSessions, setShowAllSessions] = useState(false);
  const [renaming, setRenaming] = useState<{ id: string; title: string } | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = useRuntimeStore((state) => state.sessions);
  const projects = useRuntimeStore((state) => state.projects);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const view = useRuntimeStore((state) => state.view);
  const setView = useRuntimeStore((state) => state.setView);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const setError = useRuntimeStore((state) => state.setError);
  const pullRequestDashboard = useRuntimeStore((state) => state.pullRequestDashboard);
  const branches = useRuntimeStore((state) => state.branches);
  const selectPullRequest = useRuntimeStore((state) => state.selectPullRequest);
  const t = translator(snapshot.language);
  const catalog = projects.some((project) => project.workspace === snapshot.workspace)
    ? projects
    : [{ workspace: snapshot.workspace, updatedAt: "" }, ...projects];
  const currentBranch = pullRequestDashboard?.currentBranch || branches.find((branch) => branch.current)?.name || "";
  const currentPullRequest = pullRequestDashboard?.current;

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

  const run = async (kind: string, target = "") => {
    try {
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
      <div className="sidebar-brand titlebar-region">
        <button className="icon-button" aria-label={t("search")} onClick={() => setCommandOpen(true)}><Command size={15} /></button>
      </div>
      <div className="sidebar-switcher" role="tablist">
        {[[t("projects"), "thread"], [t("workspace"), "projects"]].map(([label, target]) => <button key={label} className={view === target ? "active" : ""} onClick={() => setView(target as View)}>{label}</button>)}
      </div>
      <nav className="primary-nav" aria-label="Primary">
        <button className={view === "thread" && !currentSessionId ? "active" : ""} onClick={() => run("new_session")}><Plus size={15} />{t("newSession")}</button>
        <button onClick={() => setCommandOpen(true)}><Search size={15} />{t("search")}<kbd>⌘K</kbd></button>
        <button className={view === "pullRequests" ? "active" : ""} onClick={() => { setView("pullRequests"); void refreshPullRequestDashboard(); }}><GitPullRequest size={15} />{t("pullRequests")}</button>
        <button className={view === "extensions" ? "active" : ""} onClick={() => setView("extensions")}><Box size={15} />{t("extensions")}</button>
      </nav>
      <section className="project-tree">
        <div className="sidebar-section-header">
          <div className="sidebar-section-title">{t("projects")}</div>
          <ProjectLauncher language={snapshot.language} setError={setError} />
        </div>
        {catalog.map((item) => {
          const active = item.workspace === snapshot.workspace;
          const projectOpen = openProjects[item.workspace] ?? active;
          const projectSessions = sessions.filter((session) => !session.archived && (session.workspace === item.workspace || (!session.workspace && active)));
          const visibleSessions = showAllSessions && active ? projectSessions : projectSessions.slice(0, 5);
          const startProjectSession = () => active ? void run("new_session") : launchProject(item.workspace);
          return <div className="project-node" key={item.workspace}>
            <div className="project-heading">
              <button className="project-toggle" aria-expanded={projectOpen} onClick={() => setOpenProjects((open) => ({ ...open, [item.workspace]: !projectOpen }))}>
                {projectOpen ? <FolderOpen size={16} /> : <Folder size={16} />}<span>{basename(item.workspace)}</span>
              </button>
              <button className="project-action" aria-label={t("projectDetails")} title={t("projectDetails")} onClick={() => active ? setView("projects") : launchProject(item.workspace)}><MoreHorizontal size={16} /></button>
              <button className="project-action" aria-label={t("newSession")} title={t("newSession")} onClick={startProjectSession}><SquarePen size={15} /></button>
            </div>
            {projectOpen && <>
              {active && currentBranch && <div className="sidebar-branch-row"><GitBranch size={13} /><span>{currentBranch}</span></div>}
              {active && currentPullRequest && <div className="sidebar-pr-row">
                <button type="button" className="sidebar-pr-main" title={currentPullRequest.title} onClick={() => void openPullRequest(currentPullRequest.number)}>
                  <span className="sidebar-pr-icon"><GitPullRequest size={14} /><i /></span><span>{currentPullRequest.title}</span><small>#{currentPullRequest.number}</small>
                </button>
                <button type="button" className="sidebar-pr-github" aria-label={t("openGitHub")} title={t("openGitHub")} onClick={() => void openExternalURL(currentPullRequest.url)}><Github size={14} /></button>
              </div>}
              <div className="thread-list">
                {projectSessions.length === 0 && <div className="empty-sidebar"><CircleDotDashed size={13} />{t("noSessions")}</div>}
                {visibleSessions.map((session) => renaming?.id === session.id ? (
                  <form key={session.id} className="thread-rename" onSubmit={(event) => { event.preventDefault(); void commitRename(); }}>
                    <input autoFocus value={renaming.title} onChange={(event) => setRenaming({ id: session.id, title: event.target.value })}
                      onBlur={() => setRenaming(null)} onKeyDown={(event) => { if (event.key === "Escape") setRenaming(null); }} aria-label={t("renameChat")} />
                  </form>
                ) : (
                  <button key={session.id} className={session.id === currentSessionId && view === "thread" ? "active" : ""} onClick={() => active ? void run("resume_session", session.id) : launchProject(item.workspace, session.id)} title={session.title}
                    style={{ "--custom-contextmenu": session.pinned ? "session-pinned" : "session", "--custom-contextmenu-data": session.id } as CSSProperties}>
                    <span>{session.title || t("newSession")}</span>
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

function ProjectLauncher({ language, setError }: { language: "en" | "zh-CN"; setError: (message: string) => void }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
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

  const submitProject = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      const path = await createProject(name);
      await openProject(path);
      setCreating(false);
      setName("");
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
        <h2 id="project-create-title">{t("newProject")}</h2>
        <p>{t("newProjectLocation")}</p>
        <label htmlFor="project-name">{t("projectName")}</label>
        <input id="project-name" autoFocus maxLength={100} required value={name} placeholder={t("projectNamePlaceholder")}
          onChange={(event) => setName(event.target.value)} />
        <footer>
          <button type="button" className="small-button" disabled={busy} onClick={() => setCreating(false)}>{t("cancel")}</button>
          <button type="submit" className="primary-button" disabled={busy || !name.trim()}>{t("createProject")}</button>
        </footer>
      </form>
    </div>, document.body)}
  </div>;
}
