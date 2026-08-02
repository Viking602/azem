import { useEffect, useRef, useState, type CSSProperties, type FormEvent } from "react";
import { createPortal } from "react-dom";
import {
  Bot, Box, CircleDotDashed, Command, Folder, FolderOpen, FolderPlus, History,
  MoreHorizontal, Plus, Search, Settings, SquarePen,
} from "lucide-react";
import { createProject, execute, openProject, selectProjectFolder, subscribeSessionMenu } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { View } from "../types";

export default function Sidebar() {
  const [projectOpen, setProjectOpen] = useState(true);
  const [showAllSessions, setShowAllSessions] = useState(false);
  const [renaming, setRenaming] = useState<{ id: string; title: string } | null>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = useRuntimeStore((state) => state.sessions);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const view = useRuntimeStore((state) => state.view);
  const setView = useRuntimeStore((state) => state.setView);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const project = basename(snapshot.workspace);
  const projectSessions = sessions.filter((session) => !session.archived);
  const visibleSessions = showAllSessions ? projectSessions : projectSessions.slice(0, 5);

  useEffect(() => subscribeSessionMenu((event) => {
    if (event.action === "error") {
      setError(event.error || "会话操作失败");
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
      setView("thread");
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  };

  const nav: Array<{ view: View; label: string; icon: typeof History }> = [
    { view: "runs", label: t("runs"), icon: History },
    { view: "agents", label: t("agents"), icon: Bot },
    { view: "extensions", label: t("extensions"), icon: Box },
  ];

  return (
    <aside className="workspace-sidebar">
      <div className="sidebar-brand titlebar-region">
        <button className="icon-button" aria-label={t("search")} onClick={() => setCommandOpen(true)}><Command size={15} /></button>
      </div>
      <div className="sidebar-switcher" role="tablist">
        {[[t("workbench"), "runs"], [t("projects"), "thread"], [t("workspace"), "projects"]].map(([label, target]) => <button key={label} className={view === target ? "active" : ""} onClick={() => setView(target as View)}>{label}</button>)}
      </div>
      <nav className="primary-nav" aria-label="Primary">
        <button className={view === "thread" && !currentSessionId ? "active" : ""} onClick={() => run("new_session")}><Plus size={15} />{t("newSession")}</button>
        <button onClick={() => setCommandOpen(true)}><Search size={15} />{t("search")}<kbd>⌘K</kbd></button>
        {nav.map((item) => <button key={item.view} className={view === item.view ? "active" : ""} onClick={() => setView(item.view)}><item.icon size={15} />{item.label}</button>)}
      </nav>
      <section className="project-tree">
        <div className="sidebar-section-header">
          <div className="sidebar-section-title">{t("projects")}</div>
          <ProjectLauncher language={snapshot.language} setError={setError} />
        </div>
        <div className="project-heading">
          <button className="project-toggle" aria-expanded={projectOpen} onClick={() => setProjectOpen((open) => !open)}>
            {projectOpen ? <FolderOpen size={16} /> : <Folder size={16} />}<span>{project}</span>
          </button>
          <button className="project-action" aria-label={t("projectDetails")} title={t("projectDetails")} onClick={() => setView("projects")}><MoreHorizontal size={16} /></button>
          <button className="project-action" aria-label={t("newSession")} title={t("newSession")} onClick={() => run("new_session")}><SquarePen size={15} /></button>
        </div>
        {projectOpen && <div className="thread-list">
          {projectSessions.length === 0 && <div className="empty-sidebar"><CircleDotDashed size={13} />{t("noSessions")}</div>}
          {visibleSessions.map((session) => renaming?.id === session.id ? (
            <form key={session.id} className="thread-rename" onSubmit={(event) => { event.preventDefault(); void commitRename(); }}>
              <input autoFocus value={renaming.title} onChange={(event) => setRenaming({ id: session.id, title: event.target.value })}
                onBlur={() => setRenaming(null)} onKeyDown={(event) => { if (event.key === "Escape") setRenaming(null); }} aria-label={snapshot.language === "zh-CN" ? "重命名聊天" : "Rename chat"} />
            </form>
          ) : (
            <button key={session.id} className={session.id === currentSessionId && view === "thread" ? "active" : ""} onClick={() => run("resume_session", session.id)} title={session.title}
              style={{ "--custom-contextmenu": session.pinned ? "session-pinned" : "session", "--custom-contextmenu-data": session.id } as CSSProperties}>
              <span>{session.title || t("newSession")}</span>
              {session.unread && <i className="session-unread" title={snapshot.language === "zh-CN" ? "未读" : "Unread"} />}
            </button>
          ))}
          {projectSessions.length > 5 && <button className="show-more-sessions" onClick={() => setShowAllSessions((show) => !show)}>{t(showAllSessions ? "showLess" : "showMore")}</button>}
        </div>}
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
