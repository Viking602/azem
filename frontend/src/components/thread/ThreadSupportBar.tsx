import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import {
  Check, ChevronDown, ExternalLink, FileDiff, FileImage, FolderOpen, GitBranch, GitFork, Globe,
  History, Link2, ListTodo, LoaderCircle, Minus, PanelsTopLeft, Plus, Server,
  Settings, SquareTerminal, X,
} from "lucide-react";
import { attachmentDataURL, createSessionFork, getSessionTree, navigateSessionTree, openExternalURL, setSessionEntryLabel } from "../../bridge";
import { tFormat, translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import { useTerminalStore } from "../../terminalStore";
import type {
  Attachment, SessionRecap, SessionTree, Snapshot, TodoItem, TodoList, TodoStatus, WorkspaceChangeSet,
} from "../../types";
import { collectConversationSources, type ConversationSource } from "../conversationSources";
import { SessionTreePanel } from "./SessionTreePanel";

// Surface and row architecture adapted from Synara's EnvironmentPanel (MIT).
// See frontend/THIRD_PARTY_NOTICES/synara.txt.

export interface ThreadPlanSummary {
  items: TodoItem[];
  completed: number;
  percentage: number;
}

export interface ThreadEnvironmentPanelProps {
  open: boolean;
}

type EnvironmentSection = "plan" | "history" | "recap" | "sources";

export function summarizeThreadPlan(todo: TodoList): ThreadPlanSummary | null {
  const items = todo.phases.flatMap((phase) => phase.items);
  if (items.length === 0) return null;
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  return { items, completed, percentage: Math.round((completed / items.length) * 100) };
}

export function ThreadEnvironmentPanel({ open }: ThreadEnvironmentPanelProps) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const todo = useRuntimeStore((state) => state.todo);
  const recap = useRuntimeStore((state) => state.recap);
  const branches = useRuntimeStore((state) => state.branches);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setError = useRuntimeStore((state) => state.setError);
  const running = useRuntimeStore((state) => state.running);
  const applyEvents = useRuntimeStore((state) => state.applyEvents);
  const workspaceAdditions = useRuntimeStore((state) => state.workspaceAdditions);
  const workspaceDeletions = useRuntimeStore((state) => state.workspaceDeletions);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setView = useRuntimeStore((state) => state.setView);
  const terminalSessions = useTerminalStore((state) => state.sessions);
  const [expanded, setExpanded] = useState<EnvironmentSection | null>(null);
  const [preview, setPreview] = useState<ConversationSource | null>(null);
  const [sessionTree, setSessionTree] = useState<SessionTree | null>(null);
  const [treeLoading, setTreeLoading] = useState(false);
  const [busyEntry, setBusyEntry] = useState<string | undefined>();
  const panelId = useId();
  const language = snapshot.language;
  const t = translator(language);
  const sources = useMemo(() => collectConversationSources(blocks, language), [blocks, language]);
  const plan = todo ? summarizeThreadPlan(todo) : null;
  const currentBranch = branches.find((branch) => branch.current)?.name || snapshot.currentBranch || "—";
  const runningServers = terminalSessions.filter((session) => session.state === "running").length;

  useEffect(() => {
    setExpanded(null);
    setPreview(null);
    setSessionTree(null);
    setBusyEntry(undefined);
  }, [currentSessionId]);


  useEffect(() => {
    if (!open || expanded !== "history") return;
    let active = true;
    setTreeLoading(true);
    void getSessionTree(currentSessionId)
      .then((value) => { if (active) setSessionTree(value); })
      .catch((cause) => { if (active) setError(cause instanceof Error ? cause.message : String(cause)); })
      .finally(() => { if (active) setTreeLoading(false); });
    return () => { active = false; };
  }, [currentSessionId, expanded, open, setError]);

  const toggle = (section: EnvironmentSection) => setExpanded((current) => current === section ? null : section);
  const openSource = (source: ConversationSource) => {
    if (source.kind === "image") {
      setPreview(source);
      return;
    }
    if (source.href) void openExternalURL(source.href).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  };

  const navigateTree = async (entryId: string) => {
    setBusyEntry(entryId);
    try {
      const projection = await navigateSessionTree(currentSessionId, entryId);
      if (projection) applyEvents([projection]);
      setSessionTree(await getSessionTree(currentSessionId));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusyEntry(undefined);
    }
  };
  const labelTreeEntry = async (entryId: string, label: string) => {
    setBusyEntry(entryId);
    try {
      setSessionTree(await setSessionEntryLabel(currentSessionId, entryId, label));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      throw cause;
    } finally {
      setBusyEntry(undefined);
    }
  };
  const forkTree = async (targetId: string, entryId: string) => {
    setBusyEntry(entryId);
    try {
      setSessionTree(await createSessionFork(currentSessionId, targetId, entryId));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      throw cause;
    } finally {
      setBusyEntry(undefined);
    }
  };

  return <div className="thread-environment-overlay" data-open={String(open)} aria-hidden={!open}>
    <aside className="thread-environment-card" role="region" aria-label={t("environment")} inert={!open}>
      <div className="thread-environment-scroll">
        <div className="thread-environment-title">
          <span>{t("environment")}</span>
          <button type="button" aria-label={t("environmentSettings")} onClick={() => setSettingsOpen(true)}><Settings size={14} /></button>
        </div>

        <EnvironmentRow
          icon={<FileDiff size={16} />}
          label={t("changes")}
          trailing={<><b className="plus">+{workspaceAdditions.toLocaleString()}</b><b className="minus">−{workspaceDeletions.toLocaleString()}</b></>}
          onClick={() => setView("changes")}
        />
        <EnvironmentRow icon={<FolderOpen size={16} />} label={t("local")} trailing={<EnvironmentChevron />} onClick={() => setView("files")} />
        <EnvironmentRow icon={<GitBranch size={16} />} label={<span title={currentBranch}>{currentBranch}</span>} trailing={<EnvironmentChevron />} onClick={() => document.querySelector<HTMLButtonElement>(".titlebar-project")?.click()} />
        <EnvironmentRow
          icon={<Server size={16} />}
          label={t("localServers")}
          trailing={<><i className="thread-environment-live-dot" />{runningServers}<EnvironmentChevron /></>}
          onClick={() => useTerminalStore.getState().toggle()}
        />

        <EnvironmentDivider />
        <EnvironmentLabel>{t("environmentConversation")}</EnvironmentLabel>
        {plan ? <>
          <EnvironmentRow
            icon={<ListTodo size={16} />}
            label={t("planLabel")}
            trailing={<>{plan.completed} / {plan.items.length}<EnvironmentChevron /></>}
            expanded={expanded === "plan"}
            controls={`${panelId}-plan`}
            onClick={() => toggle("plan")}
          />
          <div id={`${panelId}-plan`} className="thread-environment-detail" data-open={String(expanded === "plan")}>
            <div className="thread-environment-progress" role="progressbar" aria-label={tFormat(language, "todoProgress", { done: plan.completed, total: plan.items.length })} aria-valuemin={0} aria-valuemax={plan.items.length} aria-valuenow={plan.completed}><span style={{ width: `${plan.percentage}%` }} /></div>
            <ul className="thread-environment-plan-items">
              {plan.items.map((item) => <li key={item.id || item.content} data-state={item.status} aria-label={`${item.content}，${statusLabel(item.status, language)}`}><TaskMark state={item.status} /><span>{item.content}</span></li>)}
            </ul>
          </div>
        </> : null}
        <EnvironmentRow
          icon={<GitFork size={16} />}
          label={language === "zh-CN" ? "会话历史" : "Session history"}
          trailing={<>{sessionTree ? sessionTree.branches.length : "—"}<EnvironmentChevron /></>}
          expanded={expanded === "history"}
          controls={`${panelId}-history`}
          onClick={() => toggle("history")}
        />
        <div id={`${panelId}-history`} className="thread-environment-detail" data-open={String(expanded === "history")}>
          {treeLoading && !sessionTree ? <p className="thread-environment-empty" role="status">{language === "zh-CN" ? "正在载入会话树…" : "Loading session tree…"}</p> : null}
          {sessionTree ? <SessionTreePanel tree={sessionTree} language={language} running={running} busyEntry={busyEntry} onNavigate={navigateTree} onLabel={labelTreeEntry} onFork={forkTree} /> : null}
        </div>
        <EnvironmentRow
          icon={<History size={16} />}
          label={t("recap")}
          trailing={<>{recap ? `r${recap.revision}` : "—"}<EnvironmentChevron /></>}
          expanded={expanded === "recap"}
          controls={`${panelId}-recap`}
          onClick={() => toggle("recap")}
        />
        <div id={`${panelId}-recap`} className="thread-environment-detail" data-open={String(expanded === "recap")}><RecapPanel recap={recap} language={language} /></div>
        <EnvironmentRow
          icon={<Link2 size={16} />}
          label={t("sources")}
          trailing={<>{sources.length}<EnvironmentChevron /></>}
          expanded={expanded === "sources"}
          controls={`${panelId}-sources`}
          onClick={() => toggle("sources")}
        />
        <div id={`${panelId}-sources`} className="thread-environment-detail" data-open={String(expanded === "sources")}><SourcesPanel sources={sources} language={language} openSource={openSource} /></div>

        <EnvironmentDivider />
        <EnvironmentLabel>{t("editor")}</EnvironmentLabel>
        <EnvironmentRow icon={<PanelsTopLeft size={16} />} label={t("editorView")} onClick={() => setView("files")} />
        <EnvironmentRow icon={<SquareTerminal size={16} />} label={t("terminal")} trailing={<EnvironmentChevron />} onClick={() => useTerminalStore.getState().toggle()} />
      </div>
    </aside>
    {preview?.attachment ? <SourceImageLightbox source={preview} sessionId={currentSessionId} language={language} onClose={() => setPreview(null)} /> : null}
  </div>;
}

function EnvironmentRow({ icon, label, trailing, onClick, expanded, controls }: {
  icon: ReactNode;
  label: ReactNode;
  trailing?: ReactNode;
  onClick?: () => void;
  expanded?: boolean;
  controls?: string;
}) {
  const content = <>
    <span className="thread-environment-row-icon" aria-hidden="true">{icon}</span>
    <span className="thread-environment-row-label">{label}</span>
    {trailing ? <span className="thread-environment-row-trailing">{trailing}</span> : null}
  </>;
  if (!onClick) return <div className="thread-environment-row">{content}</div>;
  return <button type="button" className="thread-environment-row" aria-expanded={expanded} aria-controls={controls} onClick={onClick}>{content}</button>;
}

function EnvironmentChevron() {
  return <ChevronDown className="thread-environment-chevron" size={12} aria-hidden="true" />;
}

function EnvironmentDivider() {
  return <div className="thread-environment-divider" aria-hidden="true" />;
}

function EnvironmentLabel({ children }: { children: ReactNode }) {
  return <p className="thread-environment-label">{children}</p>;
}

function RecapPanel({ recap, language }: { recap: SessionRecap | null; language: Snapshot["language"] }) {
  const t = translator(language);
  if (!recap) return <p className="thread-environment-empty">{t("recapEmpty")}</p>;
  return <div className="thread-environment-recap">
    {recap.summary ? <p>{recap.summary}</p> : null}
    {recap.goal ? <div><span>{t("recapCurrentGoal")}</span><p>{recap.goal}</p></div> : null}
    {recap.openItems ? <div><span>{t("recapOpenItems")}</span><p>{recap.openItems}</p></div> : null}
  </div>;
}

function SourcesPanel({ sources, language, openSource }: { sources: ConversationSource[]; language: Snapshot["language"]; openSource: (source: ConversationSource) => void }) {
  const t = translator(language);
  return <div className="thread-environment-sources">
    <button type="button" className="thread-environment-source-add" onClick={() => document.querySelector<HTMLButtonElement>('[data-slot="composer-attach"]')?.click()}><Plus size={13} /><span>{t("attach")}</span></button>
    {sources.length > 0 ? sources.map((source) => {
      const Icon = source.kind === "image" ? FileImage : source.kind === "search-url" ? Globe : Link2;
      const kindLabel = source.kind === "image" ? t("sourceImage") : source.kind === "search-url" ? t("sourceWebSearch") : t("sourceTypedLink");
      return <button type="button" className="thread-environment-source-row" key={source.id} aria-label={`${t("openSource")}：${source.title}`} onClick={() => openSource(source)}>
        <span className="thread-environment-source-icon"><Icon size={13} /></span>
        <span><strong>{source.title}</strong><small>{kindLabel}{source.detail && source.detail !== source.title ? ` · ${source.detail}` : ""}</small></span>
        <ExternalLink size={12} aria-hidden="true" />
      </button>;
    }) : <p className="thread-environment-empty">{t("sourcesEmpty")}</p>}
  </div>;
}

function TaskMark({ state }: { state: TodoStatus }) {
  if (state === "completed") return <span className="thread-environment-task-mark" data-state={state} aria-hidden="true"><Check size={10} strokeWidth={2.2} /></span>;
  if (state === "cancelled") return <span className="thread-environment-task-mark" data-state={state} aria-hidden="true"><Minus size={9} strokeWidth={2.2} /></span>;
  return <span className="thread-environment-task-mark" data-state={state} aria-hidden="true" />;
}

function statusLabel(status: TodoStatus, language: Snapshot["language"]) {
  const t = translator(language);
  if (status === "completed") return t("todoCompleted");
  if (status === "cancelled") return t("cancelled");
  if (status === "in_progress") return t("todoInProgress");
  return t("todoPending");
}

function SourceImageLightbox({ source, sessionId, language, onClose }: { source: ConversationSource; sessionId: string; language: Snapshot["language"]; onClose: () => void }) {
  const attachment = source.attachment as Attachment;
  const [image, setImage] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    void attachmentDataURL(sessionId, attachment)
      .then((value) => { if (active) setImage(value); })
      .catch(() => { if (active) setImage(""); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [attachment, sessionId]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const closeLabel = translator(language)("closeSourceImage");
  return createPortal(
    <div className="attachment-lightbox" role="dialog" aria-modal="true" aria-label={source.title} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section>
        <header><strong>{source.title}</strong><button type="button" aria-label={closeLabel} onClick={onClose}><X size={17} /></button></header>
        <div className="attachment-lightbox-canvas">
          {image ? <img src={image} alt={source.title} /> : <span className="attachment-preview-placeholder" aria-hidden="true">{loading ? <LoaderCircle className="spin" size={16} /> : <FileImage size={16} />}</span>}
        </div>
      </section>
    </div>,
    document.body,
  );
}
