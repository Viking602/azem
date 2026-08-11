import { useEffect, useId, useState } from "react";
import {
  Check, ChevronRight, Circle, CircleDot, FileImage, ListChecks, Minus, Plus, SquareTerminal, X,
} from "lucide-react";
import { execute } from "../bridge";
import { tFormat, translator } from "../i18n";
import {
  subagentDisplayName,
  subagentPreviewText,
  subagentStatusLabel,
  subagentSummaryLabel,
} from "../subagents";
import { useRuntimeStore } from "../store";
import { contextCacheMetrics, contextComposition, contextOccupancy } from "../contextUsage";
import type { ContextCompositionGroup } from "../contextUsage";
import type { AgentState, ContextProfile, Snapshot, TodoList, TodoStatus } from "../types";
import SubagentGlyph from "./SubagentGlyph";


export default function Inspector() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceAdditions = useRuntimeStore((state) => state.workspaceAdditions);
  const workspaceDeletions = useRuntimeStore((state) => state.workspaceDeletions);
  const workspaceChangedFiles = useRuntimeStore((state) => state.workspaceChangedFiles);
  const agents = useRuntimeStore((state) => state.agents);
  const todo = useRuntimeStore((state) => state.todo);
  const backgroundProcesses = useRuntimeStore((state) => state.backgroundProcesses);
  const contextProfile = useRuntimeStore((state) => state.contextProfile);
  const contextUsage = useRuntimeStore((state) => state.contextUsage);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const setView = useRuntimeStore((state) => state.setView);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const t = translator(snapshot.language);
  const sources = Array.from(new Map(blocks.flatMap((block) => block.attachments ?? []).map((item) => [item.id || item.path, item])).values());
  const currentBranch = branches.find((branch) => branch.current)?.name || "";
  const occupancy = contextOccupancy(contextUsage, contextProfile);
  const cache = contextCacheMetrics(contextUsage);
  const composition = contextComposition(contextUsage, contextProfile);
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
            <div><strong>{occupancy.limit > 0 ? `${occupancy.percentage}%` : "—"}</strong><small>{occupancy.limit > 0 ? `${formatCompactTokens(occupancy.used)} / ${formatCompactTokens(occupancy.limit)}` : t("contextUnavailable")}</small></div>
          </div>
          <div className="inspector-cache-summary" aria-label={t("cacheHitRate")}>
            <div><span>{t("cacheHitRate")}</span><strong>{cache.reported ? (cache.hitRate === null ? "—" : `${cache.hitRate}%`) : t("cacheUnreported")}</strong></div>
            <div><span>{t("cacheHits")}</span><strong>{cache.reported ? formatCompactTokens(cache.cachedTokens) : "—"}</strong></div>
            <div><span>{t("totalCache")}</span><strong>{cache.reported ? formatCompactTokens(cache.totalCacheTokens) : "—"}</strong></div>
          </div>
          <ContextComposition groups={composition.groups} totalTokens={composition.totalTokens} estimated={composition.estimated} language={snapshot.language} />
          <ContextDiagnostics profile={contextProfile} language={snapshot.language} />
        </section>
        {todo && todo.phases.length > 0 && <TodoPlan todo={todo} language={snapshot.language} />}
        {agents.length > 0 && (
          <SubagentSummary
            key={currentSessionId}
            agents={agents}
            language={snapshot.language}
            openAgent={selectAgent}
          />
        )}
        {backgroundProcesses.length > 0 && <section className="inspector-section"><header className="inspector-section-header"><h2>{t("backgroundProcesses")}</h2></header>{backgroundProcesses.map((process) => <div className="process-row" key={process.id}><SquareTerminal size={14} /><span><strong>{process.name || t("backgroundTerminal")}</strong><small title={process.command}>{process.command}</small></span><em data-state={process.state}>{process.state === "running" ? t("running") : process.state}</em></div>)}</section>}
        {sources.length > 0 && <section className="inspector-section">
          <header className="inspector-section-header"><h2>{t("sources")}</h2><button className="icon-button" title={t("attach")} aria-label={t("attach")} onClick={() => document.querySelector<HTMLInputElement>(".attach-button input")?.click()}><Plus size={15} /></button></header>
          {sources.map((source) => <div className="source-row" key={source.id || source.path}><FileImage size={14} /><span title={source.name}>{source.name}</span></div>)}
        </section>}
        <section className="inspector-section inspector-workspace-section">
          <header className="inspector-section-header"><h2>{t("workspace")}</h2><button type="button" aria-label={t("reviewChanges")} onClick={() => setView("changes")}>{t("reviewChanges")}</button></header>
          <div className="inspector-workspace-facts">
            <div><span>{t("branch")}</span><strong className="inspector-branch-value" title={currentBranch}>{currentBranch || t("noBranches")}</strong></div>
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
      <small>{totalTokens > 0 ? `${estimated ? `${t("estimated")} · ` : ""}${formatCompactTokens(totalTokens)}` : "—"}</small>
    </header>
    {groups.length > 0 ? <>
      <button
        type="button"
        className="context-composition-bar"
        aria-controls={compositionGroupsId}
        aria-expanded={expanded}
        aria-label={language === "zh-CN" ? `${expanded ? "收起" : "展开"}上下文构成明细` : `${expanded ? "Collapse" : "Expand"} context composition details`}
        title={language === "zh-CN" ? `点击${expanded ? "收起" : "展开"}上下文构成明细` : `Click to ${expanded ? "collapse" : "expand"} context composition details`}
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
              <span title={item.name}>{contextContributionLabel(item.name, language)}</span>
              <em>{formatCompactTokens(item.tokens)}</em>
            </div>)}
          </div>
        </details>)}
      </div>
    </> : <p>{t("contextCompositionEmpty")}</p>}
  </div>;
}

function contextCategoryLabel(category: string, language: Snapshot["language"]) {
  const t = translator(language);
  return ({
    core: t("contextCore"), conversation: t("contextConversation"), builtin_tools: t("contextBuiltinTools"),
    skills: t("contextSkills"), mcp: t("contextMCP"), current_output: t("contextCurrentOutput"),
    provider_input: t("contextProviderInput"), other: t("contextOther"),
  } as Record<string, string>)[category] ?? category.replaceAll("_", " ");
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
      <span>{t("semanticRevision")}</span><strong>r{profile.semanticRevision ?? 0}</strong>
      <span>{t("rebuildReason")}</span><strong>{profile.rebuildReason || "—"}</strong>
      <span>{t("writerLag")}</span><strong data-state={(profile.writerLag ?? 0) > 0 ? "pending" : "current"}>{profile.writerLag ?? 0}</strong>
      <span>{t("contextSegments")}</span><strong>{segments.length}</strong>
    </div>
    {profile.manifestHash && <code className="context-manifest-hash" title={profile.manifestHash}>{profile.manifestHash.slice(0, 12)}</code>}
    {segments.length > 0 && <div className="context-segment-list" aria-label={t("contextSegments")}>
      {segments.map((segment, index) => <div key={`${segment.kind}-${segment.content_hash}-${index}`}><span>{segment.kind.replaceAll("_", " ")}</span><em>~{formatCompactTokens(segment.token_estimate)}</em></div>)}
    </div>}
  </details>;
}

function formatCompactTokens(tokens: number) {
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(tokens >= 10_000 ? 0 : 1)}k`;
  return String(tokens);
}

function TodoPlan({ todo, language }: { todo: TodoList; language: Snapshot["language"] }) {
  const t = translator(language);
  const items = todo.phases.flatMap((phase) => phase.items);
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  const percentage = items.length > 0 ? Math.round((completed / items.length) * 100) : 0;

  return <section className="inspector-section todo-section" aria-label={t("todoTitle")}>
    <header className="inspector-section-header">
      <h2><ListChecks size={14} />{language === "zh-CN" ? "执行计划" : "Execution plan"}</h2>
      <small>{completed} / {items.length}</small>
    </header>
    {todo.goal && <p className="todo-goal"><span>{t("todoGoal")}</span>{todo.goal}</p>}
    <div className="todo-progress-row">
      <div className="todo-progress-track" role="progressbar" aria-label={tFormat(language, "todoProgress", { done: completed, total: items.length })} aria-valuemin={0} aria-valuemax={items.length} aria-valuenow={completed}>
        <span style={{ width: `${percentage}%` }} />
      </div>
      <span>{completed}/{items.length}</span>
    </div>
    <div className="todo-phases">
      {todo.phases.map((phase) => <div className="todo-phase" key={phase.id || phase.title}>
        {phase.title && <h3>{phase.title}</h3>}
        <div className="todo-items">
          {phase.items.map((item) => {
            const Icon = todoStatusIcon(item.status);
            return <div className="todo-item" data-status={item.status} key={item.id || item.content} title={todoStatusLabel(item.status, language)}>
              <Icon size={14} aria-hidden="true" />
              <span>{item.content}</span>
            </div>;
          })}
        </div>
      </div>)}
    </div>
  </section>;
}

function todoStatusIcon(status: TodoStatus) {
  if (status === "completed") return Check;
  if (status === "cancelled") return Minus;
  if (status === "in_progress") return CircleDot;
  return Circle;
}

function todoStatusLabel(status: TodoStatus, language: Snapshot["language"]) {
  const t = translator(language);
  if (status === "completed") return t("completed");
  if (status === "cancelled") return t("cancelled");
  if (status === "in_progress") return t("todoInProgress");
  return t("todoPending");
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
              <strong title={name}>{name}</strong>
              <small title={preview}>{preview}</small>
            </span>
            <em>{status}</em>
          </button>;
        })}
      </div>
    </section>
  );
}
