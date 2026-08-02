import { useEffect, useState, type CSSProperties } from "react";
import {
  Bot, Box, CircleDotDashed, Command, Folder, FolderOpen, History,
  MoreHorizontal, Plus, Search, Settings, SquarePen,
} from "lucide-react";
import { execute, subscribeSessionMenu } from "../bridge";
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
        <div className="sidebar-section-title">{t("projects")}</div>
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
