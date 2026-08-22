import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  ChevronRight, ExternalLink, FileImage, Globe, Link2, LoaderCircle, Plus, SquareTerminal, X,
} from "lucide-react";
import { attachmentDataURL, execute, openExternalURL } from "../bridge";
import { tFormat, translator } from "../i18n";
import {
  subagentDisplayName,
  subagentPreviewText,
  subagentStatusLabel,
  subagentSummaryLabel,
} from "../subagents";
import { useRuntimeStore } from "../store";
import { contextCacheMetrics, contextCategoryLabel, contextComposition, contextOccupancy, stickyCacheMetrics, type ContextCacheMetrics, type ContextCompositionGroup } from "../contextUsage";
import type { AgentState, Attachment, ContextProfile, SessionRecap, Snapshot, TodoItem, TodoList, TodoStatus } from "../types";
import SubagentGlyph from "./SubagentGlyph";
import { RollingLabel } from "./assistant-ui/Elements";
import { collectConversationSources, type ConversationSource } from "./inspectorSources";


export default function Inspector() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceAdditions = useRuntimeStore((state) => state.workspaceAdditions);
  const workspaceDeletions = useRuntimeStore((state) => state.workspaceDeletions);
  const workspaceChangedFiles = useRuntimeStore((state) => state.workspaceChangedFiles);
  const agents = useRuntimeStore((state) => state.agents);
  const todo = useRuntimeStore((state) => state.todo);
  const recap = useRuntimeStore((state) => state.recap);
  const backgroundProcesses = useRuntimeStore((state) => state.backgroundProcesses);
  const contextProfile = useRuntimeStore((state) => state.contextProfile);
  const contextUsage = useRuntimeStore((state) => state.contextUsage);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const running = useRuntimeStore((state) => state.running);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const setView = useRuntimeStore((state) => state.setView);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const t = translator(snapshot.language);
  const sources = collectConversationSources(blocks, snapshot.language);
  const [preview, setPreview] = useState<ConversationSource | null>(null);
  const currentBranch = branches.find((branch) => branch.current)?.name || "";
  const occupancy = contextOccupancy(contextUsage, contextProfile);
  const cacheReport = contextCacheMetrics(contextUsage);
  const cacheScope = `${currentSessionId}\u0000${snapshot.provider}\u0000${snapshot.model}`;
  const lastCache = useRef<{ scope: string; metrics: ContextCacheMetrics } | null>(null);
  const previousCache = lastCache.current?.scope === cacheScope ? lastCache.current.metrics : null;
  const cache = stickyCacheMetrics(cacheReport, previousCache, running);
  if (cache.reported) lastCache.current = { scope: cacheScope, metrics: cache };
  else if (!running) lastCache.current = null;
  const composition = contextComposition(contextUsage, contextProfile);
  const cacheHitLabel = cache.reported ? (cache.hitRate === null ? "—" : `${cache.hitRate}%`) : t(running ? "cachePending" : "cacheUnreported");
  const cacheHitsLabel = cache.reported ? formatCompactTokens(cache.cachedTokens) : "—";
  const cacheInputLabel = cache.reported ? formatCompactTokens(cache.totalCacheTokens) : "—";
  const prototypeDemo = typeof window !== "undefined" && new URLSearchParams(window.location.search).has("demo");

  useEffect(() => {
    void execute({ kind: "list_background", sessionId: currentSessionId }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  }, [currentSessionId, setError]);

  return (
    <aside className="context-inspector" aria-label={t("inspector")}>
      <header className="inspector-titlebar">
        <div><span>LIVE CONTEXT</span><strong>{t("environmentInfo")}</strong></div>
        <button type="button" aria-label={snapshot.language === "zh-CN" ? "关闭环境信息" : "Close environment"} onClick={() => setInspectorOpen(false)}><X size={15} /></button>
      </header>
      <div className="inspector-scroll">
        <section className="inspector-section inspector-context-section">
          <header className="inspector-section-header"><h2>{t("contextKernel")}</h2><small className="inspector-current">CURRENT</small></header>
          <div className="inspector-context-orbit" aria-label={`${occupancy.percentage}%`}>
            <span className="orbit arc-one" /><span className="orbit arc-two" /><span className="orbit arc-three" />
            <div>
              <strong><RollingLabel text={occupancy.limit > 0 ? `${occupancy.percentage}%` : "—"} active={false} /></strong>
              <small><RollingLabel text={occupancy.limit > 0 ? `${formatCompactTokens(occupancy.used)} / ${formatCompactTokens(occupancy.limit)}` : t("contextUnavailable")} active={false} /></small>
            </div>
          </div>
          <div className="inspector-cache-summary" aria-label={t("cacheHitRate")}>
            <div><span>{t("cacheHitRate")}</span><strong aria-live="polite"><RollingLabel text={cacheHitLabel} active={false} /></strong></div>
            <div><span>{t("cacheHits")}</span><strong><RollingLabel text={cacheHitsLabel} active={false} /></strong></div>
            <div><span>{t("cacheRequestInput")}</span><strong><RollingLabel text={cacheInputLabel} active={false} /></strong></div>
          </div>
          <ContextComposition groups={composition.groups} totalTokens={composition.totalTokens} estimated={composition.estimated} language={snapshot.language} />
          <ContextDiagnostics profile={contextProfile} language={snapshot.language} />
        </section>
        <RecapSummary recap={recap} language={snapshot.language} />
        {todo && todo.phases.length > 0 && <TodoPlan todo={todo} language={snapshot.language} />}
        {agents.length > 0 && (
          <SubagentSummary
            key={currentSessionId}
            agents={agents}
            language={snapshot.language}
            openAgent={selectAgent}
          />
        )}
        {backgroundProcesses.length > 0 && <section className="inspector-section"><header className="inspector-section-header"><h2>{t("backgroundProcesses")}</h2></header>{backgroundProcesses.map((process) => <div className="process-row" key={process.id}><SquareTerminal size={14} /><span><strong>{process.name || t("backgroundTerminal")}</strong><small>{process.command}</small></span><em data-state={process.state}>{process.state === "running" ? t("running") : process.state}</em></div>)}</section>}
        {sources.length > 0 && <section className="inspector-section">
          <header className="inspector-section-header"><h2>{t("sources")}</h2><button className="icon-button" aria-label={t("attach")} onClick={() => document.querySelector<HTMLInputElement>(".attach-button input")?.click()}><Plus size={15} /></button></header>
          {sources.map((source) => {
            const Icon = source.kind === "image" ? FileImage : source.kind === "search-url" ? Globe : Link2;
            const kindLabel = source.kind === "image" ? t("sourceImage") : source.kind === "search-url" ? t("sourceSearchURL") : t("sourceInputURL");
            return <button
              type="button"
              className="source-row"
              key={source.id}
              aria-label={`${t("openSource")}：${source.title}`}
              onClick={() => {
                if (source.kind === "image") {
                  setPreview(source);
                  return;
                }
                if (source.href) void openExternalURL(source.href).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
              }}
            >
              <Icon size={14} />
              <span>
                <strong>{source.title}</strong>
                <small>{kindLabel}{source.detail && source.detail !== source.title ? ` · ${source.detail}` : ""}</small>
              </span>
              <ExternalLink size={12} aria-hidden="true" />
            </button>;
          })}
        </section>}
        {preview?.attachment ? <SourceImageLightbox source={preview} sessionId={currentSessionId} language={snapshot.language} onClose={() => setPreview(null)} /> : null}
        <section className="inspector-section inspector-workspace-section">
          <header className="inspector-section-header"><h2>{t("workspace")}</h2><button type="button" aria-label={t("reviewChanges")} onClick={() => setView("changes")}>{t("reviewChanges")}</button></header>
          <div className="inspector-workspace-facts">
            <div><span>{t("branch")}</span><strong className="inspector-branch-value">{currentBranch || t("noBranches")}</strong></div>
            <div><span>{snapshot.language === "zh-CN" ? "文件" : "Files"}</span><strong>{workspaceChangedFiles > 0 ? `+${workspaceChangedFiles}` : "0"}</strong></div>
            {prototypeDemo
              ? <div><span>{snapshot.language === "zh-CN" ? "质量" : "Quality"}</span><strong>6242</strong></div>
              : <div><span>{snapshot.language === "zh-CN" ? "改动" : "Changes"}</span><strong>{workspaceAdditions + workspaceDeletions > 0 ? <><span className="plus">+{workspaceAdditions}</span> <span className="minus">−{workspaceDeletions}</span></> : "0"}</strong></div>}
          </div>
        </section>
      </div>
    </aside>
  );
}

function RecapSummary({ recap, language }: { recap: SessionRecap | null; language: Snapshot["language"] }) {
  const t = translator(language);
  return <section className="inspector-section recap-section" aria-label={t("recapTitle")}>
    <header className="inspector-section-header">
      <h2>{t("recapTitle")}</h2>
      {recap && <small>r{recap.revision}</small>}
    </header>
    {!recap ? <p className="recap-empty">{t("recapEmpty")}</p> : <div className="recap-content">
      {recap.summary && <p className="recap-summary">{recap.summary}</p>}
      {recap.goal && <div><span>{t("recapGoal")}</span><p>{recap.goal}</p></div>}
      {recap.openItems && <div><span>{t("recapOpenItems")}</span><p className="recap-open-items">{recap.openItems}</p></div>}
    </div>}
  </section>;
}

function ContextComposition({ groups, totalTokens, estimated, language }: {
  groups: ContextCompositionGroup[];
  totalTokens: number;
  estimated: boolean;
  language: Snapshot["language"];
}) {
  const t = translator(language);
  const compositionGroupsId = useId();
  const [expanded, setExpanded] = useState(false);
  return <div className="context-composition">
    <header>
      <strong>{t("contextComposition")}</strong>
      <small><RollingLabel text={totalTokens > 0 ? `${estimated ? `${t("estimated")} · ` : ""}${formatCompactTokens(totalTokens)}` : "—"} active={false} /></small>
    </header>
    {groups.length > 0 ? <>
      <button
        type="button"
        className="context-composition-bar"
        aria-controls={compositionGroupsId}
        aria-expanded={expanded}
        aria-label={language === "zh-CN" ? `${expanded ? "收起" : "展开"}上下文构成明细` : `${expanded ? "Collapse" : "Expand"} context composition details`}
        onClick={() => setExpanded((current) => !current)}
      >
        {groups.map((group) => <span key={group.category} data-category={group.category} style={{ width: `${group.percentage}%` }} />)}
      </button>
      <div id={compositionGroupsId} className="context-composition-groups" hidden={!expanded}>
        {groups.map((group) => <details data-category={group.category} key={group.category}>
          <summary>
            <i aria-hidden="true" />
            <span>{contextCategoryLabel(group.category, language)}</span>
            <em>{group.percentage}%</em>
            <strong>{formatCompactTokens(group.tokens)}</strong>
          </summary>
          <div className="context-composition-items">
            {group.items.map((item, index) => <div key={`${item.name}-${index}`}>
              <span>{contextContributionLabel(item.name, language)}</span>
              <em>{formatCompactTokens(item.tokens)}</em>
            </div>)}
          </div>
        </details>)}
      </div>
    </> : <p>{t("contextCompositionEmpty")}</p>}
  </div>;
}


function contextContributionLabel(name: string, language: Snapshot["language"]) {
  const t = translator(language);
  if (name === "azem.core_instructions") return t("azemCoreInstructions");
  if (name === "current_output") return t("contextCurrentOutput");
  if (name === "provider_input") return t("contextProviderInput");
  if (name === "azem.context.remaining_items") return language === "zh-CN" ? "其余项目" : "Remaining items";
  if (name === "catalog.overhead") return language === "zh-CN" ? "技能目录开销" : "Skill catalog overhead";
  if (name === "runtime.overhead") return language === "zh-CN" ? "技能运行时开销" : "Skill runtime overhead";
  const messageMatch = /^message:(\w+):(\d+)$/.exec(name);
  if (messageMatch) {
    const role = ({ user: language === "zh-CN" ? "用户消息" : "User message", assistant: language === "zh-CN" ? "助手消息" : "Assistant message", system: language === "zh-CN" ? "系统消息" : "System message" } as Record<string, string>)[messageMatch[1]!] ?? messageMatch[1]!;
    return `${role} ${messageMatch[2]}`;
  }
  if (name.startsWith("tool_result:")) return `${language === "zh-CN" ? "工具结果" : "Tool result"} · ${name.slice("tool_result:".length)}`;
  if (name.startsWith("compaction:")) return `${language === "zh-CN" ? "上下文摘要" : "Context summary"} ${name.slice("compaction:".length)}`;
  if (name.startsWith("system:")) return `${language === "zh-CN" ? "系统消息" : "System message"} ${name.slice("system:".length)}`;
  return name;
}

function ContextDiagnostics({ profile, language }: { profile?: ContextProfile | null; language: Snapshot["language"] }) {
  if (!profile || (!profile.manifestHash && !profile.rebuildReason && (profile.segments?.length ?? 0) === 0)) return null;
  const t = translator(language);
  const segments = profile.segments ?? [];
  return <details className="context-diagnostics">
    <summary>{language === "zh-CN" ? "上下文详情" : "Context details"}</summary>
    <div className="context-kernel-grid">
      <span>{t("archivePolicy")}</span><strong>v{profile.policyVersion ?? 0}</strong>
      <span>{t("rebuildReason")}</span><strong>{profile.rebuildReason || "—"}</strong>
      <span>{t("canonicalHighWater")}</span><strong>{profile.canonicalHighWater ?? "—"}</strong>
      <span>{t("contextSegments")}</span><strong>{segments.length}</strong>
      {profile.archive && <>
        <span>{language === "zh-CN" ? "归档载体" : "Archive carrier"}</span><strong>{profile.archive.carrier}</strong>
        <span>{language === "zh-CN" ? "归档帧" : "Archive frames"}</span><strong>{profile.archive.frameCount ?? 0} / {profile.archive.totalPages ?? 0}</strong>
        <span>{language === "zh-CN" ? "帧数据" : "Frame payload"}</span><strong>{formatArchiveBytes(profile.archive.frameBytes ?? 0)}</strong>
        <span>{language === "zh-CN" ? "未成像字符" : "Unimaged characters"}</span><strong>{(profile.archive.truncatedCharacters ?? 0).toLocaleString()}</strong>
      </>}
    </div>
    {profile.manifestHash && <code className="context-manifest-hash">{profile.manifestHash}</code>}
    {profile.archive?.sourceArtifactId && <code className="context-manifest-hash">{profile.archive.sourceArtifactId}</code>}
    {segments.length > 0 && <div className="context-segment-list" aria-label={t("contextSegments")}>
      {segments.map((segment, index) => <div key={`${segment.kind}-${segment.content_hash}-${index}`}><span>{segment.kind.replaceAll("_", " ")}</span><em>~{formatCompactTokens(segment.token_estimate)}</em></div>)}
    </div>}
  </details>;
}

function formatCompactTokens(tokens: number) {
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(tokens >= 10_000 ? 0 : 1)}k`;
  return String(tokens);
}

function formatArchiveBytes(bytes: number) {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${bytes} B`;
}

function TodoPlan({ todo, language }: { todo: TodoList; language: Snapshot["language"] }) {
  const t = translator(language);
  const items = todo.phases.flatMap((phase) => phase.items);
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  const percentage = items.length > 0 ? Math.round((completed / items.length) * 100) : 0;
  const heading = t("todoTitle");
  const goal = todo.goal?.trim() || "";

  return <section className="inspector-section todo-section" data-slot="todo-list" aria-label={goal ? `${heading}: ${goal}` : heading}>
    <header className="inspector-section-header">
      <h2 className="todo-title">{heading}</h2>
      <small>{completed} / {items.length}</small>
    </header>
    {goal ? <p className="todo-goal">{goal}</p> : null}
    {percentage > 0 ? <div className="todo-progress-row">
      <div className="todo-progress-track" role="progressbar" aria-label={tFormat(language, "todoProgress", { done: completed, total: items.length })} aria-valuemin={0} aria-valuemax={items.length} aria-valuenow={completed}>
        <span style={{ width: `${percentage}%` }} />
      </div>
    </div> : null}
    <div className="todo-phases">
      {todo.phases.map((phase, phaseIndex) => {
        const phaseKey = phase.id || phase.title || String(phaseIndex);
        return <section key={phaseKey} className="todo-phase" data-state={todoPhaseState(phase.items)}>
          {phase.title && phase.items.length > 1 ? <h3 className="todo-phase-title">{phase.title}</h3> : null}
          <ul className="todo-items">
            {phase.items.map((item) => <li key={item.id || item.content} className="todo-task" data-status={item.status} aria-label={`${item.content}，${todoStatusLabel(item.status, language)}`}>
              <TodoTaskMark state={item.status} />
              <span className="todo-task-label">{item.content}</span>
            </li>)}
          </ul>
        </section>;
      })}
    </div>
  </section>;
}

function TodoTaskMark({ state }: { state: TodoStatus }) {
  if (state === "completed") {
    return <span className="todo-task-mark" data-state="completed" aria-hidden="true">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3.6 8.2 6.7 11.2 12.4 4.8" /></svg>
    </span>;
  }
  if (state === "cancelled") {
    return <span className="todo-task-mark" data-state="cancelled" aria-hidden="true">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4.5 8h7" /></svg>
    </span>;
  }
  return <span className="todo-task-mark" data-state={state} aria-hidden="true" />;
}

function todoPhaseState(items: TodoItem[]): TodoStatus {
  if (items.some((item) => item.status === "in_progress")) return "in_progress";
  if (items.some((item) => item.status === "pending")) return "pending";
  if (items.length > 0 && items.every((item) => item.status === "cancelled")) return "cancelled";
  return "completed";
}

function todoStatusLabel(status: TodoStatus, language: Snapshot["language"]) {
  const t = translator(language);
  if (status === "completed") return t("todoCompleted");
  if (status === "cancelled") return t("cancelled");
  if (status === "in_progress") return t("todoInProgress");
  return t("todoPending");
}

function SourceImageLightbox({ source, sessionId, language, onClose }: {
  source: ConversationSource;
  sessionId: string;
  language: Snapshot["language"];
  onClose: () => void;
}) {
  const attachment = source.attachment as Attachment;
  const [image, setImage] = useState("");
  const [loading, setLoading] = useState(true);
  const t = translator(language);

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
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return createPortal(
    <div className="attachment-lightbox" role="dialog" aria-modal="true" aria-label={source.title} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section>
        <header><strong>{source.title}</strong><button type="button" aria-label={t("closeSourceImage")} onClick={onClose}><X size={17} /></button></header>
        <div className="attachment-lightbox-canvas">
          {image ? <img src={image} alt={source.title} /> : <span className="attachment-preview-placeholder" aria-hidden="true">{loading ? <LoaderCircle className="spin" size={16} /> : <FileImage size={16} />}</span>}
        </div>
      </section>
    </div>,
    document.body,
  );
}

function SubagentSummary({ agents, language, openAgent }: {
  agents: AgentState[];
  language: Snapshot["language"];
  openAgent: (agentId: string) => void;
}) {
  const t = translator(language);
  const listId = useId();
  const [expanded, setExpanded] = useState(false);
  const summary = subagentSummaryLabel(agents, language);
  const orderedAgents = [...agents].reverse();
  const visibleGlyphs = orderedAgents.slice(0, 4);
  return (
    <section className="inspector-section subagents-section">
      <header className="inspector-section-header"><h2>{t("subagentCenter")}</h2><small>{agents.length}</small></header>
      <button
        type="button"
        className="subagent-summary-button"
        aria-controls={listId}
        aria-expanded={expanded}
        aria-label={`${expanded ? t("showLess") : t("showMore")}，${t("subagents")}，${summary}`}
        onClick={() => setExpanded((current) => !current)}
      >
        <span className="subagent-glyph-stack" aria-hidden="true">
          {visibleGlyphs.map((agent) => <SubagentGlyph key={agent.id} agent={agent} size={20} />)}
        </span>
        <span className="subagent-summary-copy" aria-live="polite">{summary}</span>
        <ChevronRight size={15} aria-hidden="true" />
      </button>
      <div id={listId} className="inspector-subagent-list" aria-label={t("subagents")} hidden={!expanded}>
        {orderedAgents.map((agent) => {
          const name = subagentDisplayName(agent, agents, language);
          const preview = subagentPreviewText(agent, name, language);
          const status = subagentStatusLabel(agent.state, language);
          return <button
            type="button"
            className="inspector-subagent-row"
            data-state={agent.state}
            key={agent.id}
            onClick={() => openAgent(agent.id)}
            aria-label={`${name}，${status}`}
          >
            <SubagentGlyph agent={agent} size={24} />
            <span>
              <strong>{name}</strong>
              <small>{preview}</small>
            </span>
            <em>{status}</em>
          </button>;
        })}
      </div>
    </section>
  );
}
