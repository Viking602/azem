import { useMemo, useState } from "react";
import { ArchiveRestore, ChevronDown, ChevronRight, FolderOpen, Search } from "lucide-react";
import { execute, openProjectSession } from "../bridge";
import { tFormat, translator, type Language } from "../i18n";
import { formatRelativeTime, useRelativeNow } from "../relativeTime";
import { useRuntimeStore } from "../store";
import type { ActionKind, Session } from "../types";
import MenuSelect from "./MenuSelect";

const ARCHIVE_DAY_OPTIONS = [7, 14, 30, 90] as const;
export const ARCHIVE_PAGE_SIZE = 20;

export interface ArchivedProjectGroup {
  workspace: string;
  name: string;
  sessions: Session[];
}

export function archivedProjectKey(workspace: string) {
  return workspace || "unassigned";
}

export function groupArchivedSessions(sessions: Session[], query = ""): ArchivedProjectGroup[] {
  const needle = query.trim().toLocaleLowerCase();
  const archived = sessions.filter((session) => {
    if (!session.archived) return false;
    if (!needle) return true;
    const haystack = `${session.title} ${session.workspace} ${projectName(session.workspace)}`.toLocaleLowerCase();
    return haystack.includes(needle);
  });
  const groups = new Map<string, ArchivedProjectGroup>();
  for (const session of archived) {
    const workspace = session.workspace || "";
    const existing = groups.get(workspace);
    if (existing) {
      existing.sessions.push(session);
      continue;
    }
    groups.set(workspace, { workspace, name: projectName(workspace), sessions: [session] });
  }
  return [...groups.values()]
    .map((group) => ({
      ...group,
      sessions: [...group.sessions].sort((left, right) => Date.parse(right.updatedAt || "") - Date.parse(left.updatedAt || "")),
    }))
    .sort((left, right) => left.name.localeCompare(right.name, undefined, { sensitivity: "base" }));
}

export function countArchivableSessions(sessions: Session[], days: number, currentSessionId: string, now = Date.now()): number {
  const cutoff = now - days * 24 * 60 * 60 * 1000;
  return sessions.filter((session) => {
    if (session.archived || session.pinned || session.id === currentSessionId) return false;
    const updated = Date.parse(session.updatedAt);
    return Number.isFinite(updated) && updated < cutoff;
  }).length;
}

export default function ArchiveSettings({ language, sessionId, onError }: {
  language: Language;
  sessionId: string;
  onError: (message: string) => void;
}) {
  const sessions = useRuntimeStore((state) => state.sessions);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setView = useRuntimeStore((state) => state.setView);
  const t = translator(language);
  const [days, setDays] = useState<number>(30);
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<Record<string, true>>({});
  const [visibleCount, setVisibleCount] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState(false);
  const archivedTimes = useMemo(() => sessions.filter((session) => session.archived).map((session) => session.updatedAt).filter(Boolean), [sessions]);
  const now = useRelativeNow(archivedTimes);
  const groups = useMemo(() => groupArchivedSessions(sessions, query), [query, sessions]);
  const archivedCount = sessions.filter((session) => session.archived).length;
  const pendingCount = countArchivableSessions(sessions, days, sessionId, now);
  const searching = query.trim().length > 0;

  const toggleGroup = (key: string) => {
    setExpanded((current) => {
      if (current[key]) {
        const next = { ...current };
        delete next[key];
        return next;
      }
      return { ...current, [key]: true };
    });
  };

  const showMore = (key: string, total: number) => {
    setVisibleCount((current) => ({
      ...current,
      [key]: Math.min(total, (current[key] ?? ARCHIVE_PAGE_SIZE) + ARCHIVE_PAGE_SIZE),
    }));
  };

  const run = async (kind: ActionKind, target: string, decision = "") => {
    try {
      setBusy(true);
      await execute({ kind, target, decision, sessionId });
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  const openSession = async (session: Session) => {
    try {
      setBusy(true);
      if (session.workspace && session.workspace !== snapshot.workspace) {
        await openProjectSession(session.workspace, session.id);
      } else {
        await execute({ kind: "resume_session", target: session.id, sessionId: session.id });
        setView("thread");
      }
      setSettingsOpen(false);
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  return <div className="archive-settings">
    <section className="settings-card archive-inactive-card" data-setting-id="archive:inactive">
      <header>
        <div>
          <strong>{t("archiveInactiveTitle")}</strong>
          <small>{t("archiveInactiveHint")}</small>
        </div>
      </header>
      <div className="archive-inactive-controls">
        <MenuSelect
          className="archive-days-menu"
          value={String(days)}
          options={ARCHIVE_DAY_OPTIONS.map((value) => ({ value: String(value), label: tFormat(language, "archiveInactiveDays", { days: value }) }))}
          onChange={(value) => setDays(Number(value))}
          ariaLabel={t("archiveInactiveTitle")}
        />
        <button
          type="button"
          className="small-button"
          disabled={busy || pendingCount === 0}
          onClick={() => void run("archive_inactive_sessions", String(days))}
        >
          {pendingCount > 0 ? tFormat(language, "archiveInactiveCount", { count: pendingCount }) : t("archiveInactiveNone")}
        </button>
      </div>
    </section>

    <section className="settings-card archive-list-card" data-setting-id="archive:list">
      <header>
        <div>
          <strong>{t("archivedSessionsTitle")}</strong>
          <small>{t("archivedSessionsHint")}</small>
        </div>
        {archivedCount > 0 ? <em>{tFormat(language, "archivedSessionCount", { count: archivedCount })}</em> : null}
      </header>
      {archivedCount > 0 ? <label className="archive-search"><Search size={14} /><input value={query} onChange={(event) => {
        setQuery(event.target.value);
        setVisibleCount({});
      }} placeholder={t("searchArchivedSessions")} /></label> : null}
      {archivedCount === 0 ? <div className="archive-empty">{t("archivedEmpty")}</div> : groups.length === 0 ? <div className="archive-empty">{t("noMatchingArchivedSessions")}</div> : <div className="archive-projects">
        {groups.map((group) => {
          const key = archivedProjectKey(group.workspace);
          const name = group.workspace ? group.name : t("archivedUnassigned");
          const isExpanded = searching || Boolean(expanded[key]);
          const limit = visibleCount[key] ?? ARCHIVE_PAGE_SIZE;
          const visibleSessions = group.sessions.slice(0, limit);
          const remaining = group.sessions.length - visibleSessions.length;
          return <article key={key} className="archive-project" data-archive-project={key} data-expanded={String(isExpanded)}>
            <button
              type="button"
              className="archive-project-toggle"
              aria-expanded={isExpanded}
              onClick={() => toggleGroup(key)}
            >
              <span className="archive-project-caret" aria-hidden="true">{isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</span>
              <span className="archive-project-mark" aria-hidden="true"><FolderOpen size={14} /></span>
              <div>
                <strong>{name}</strong>
                {group.workspace ? <small>{group.workspace}</small> : null}
              </div>
              <em>{tFormat(language, "archivedSessionCount", { count: group.sessions.length })}</em>
            </button>
            {isExpanded ? <ul>
              {visibleSessions.map((session) => (
                <li key={session.id} className="archive-session-row">
                  <button type="button" className="archive-session-open" onClick={() => void openSession(session)} disabled={busy} aria-label={t("openArchivedSession")}>
                    <strong>{session.title || t("newSession")}</strong>
                    <small className="archive-session-meta">
                      <span className="archive-session-project">{name}</span>
                      {group.workspace ? <span className="archive-session-path">{group.workspace}</span> : null}
                      <span>{formatRelativeTime(session.updatedAt, language, now)}</span>
                    </small>
                  </button>
                  <button type="button" className="text-button" onClick={() => void run("archive_session", session.id, "false")} disabled={busy}>
                    <ArchiveRestore size={13} />{t("restoreSession")}
                  </button>
                </li>
              ))}
              {remaining > 0 ? <li className="archive-load-more-row">
                <button type="button" className="archive-load-more" onClick={() => showMore(key, group.sessions.length)}>
                  {tFormat(language, "archiveLoadMore", { count: remaining })}
                </button>
              </li> : null}
            </ul> : null}
          </article>;
        })}
      </div>}
    </section>
  </div>;
}

function projectName(workspace: string) {
  return workspace.split(/[\\/]/).filter(Boolean).at(-1) || "";
}
