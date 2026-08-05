import { useEffect } from "react";
import {
  Check, ChevronRight, Circle, CircleDot, FileDiff, FileImage, FolderGit2, GitBranch, ListChecks, Minus, Plus, SquareTerminal, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { tFormat, translator } from "../i18n";
import { subagentSummaryLabel } from "../subagents";
import { useRuntimeStore } from "../store";
import type { AgentState, Snapshot, TodoList, TodoStatus } from "../types";
import MenuSelect from "./MenuSelect";
import SubagentGlyph from "./SubagentGlyph";


export default function Inspector() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceAdditions = useRuntimeStore((state) => state.workspaceAdditions);
  const workspaceDeletions = useRuntimeStore((state) => state.workspaceDeletions);
  const agents = useRuntimeStore((state) => state.agents);
  const todo = useRuntimeStore((state) => state.todo);
  const backgroundProcesses = useRuntimeStore((state) => state.backgroundProcesses);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const setView = useRuntimeStore((state) => state.setView);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const t = translator(snapshot.language);
  const sources = Array.from(new Map(blocks.flatMap((block) => block.attachments ?? []).map((item) => [item.id || item.path, item])).values());

  const action = async (kind: string, target = "", decision = "") => {
    try { await execute({ kind, target, decision, sessionId: currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  useEffect(() => {
    void execute({ kind: "list_background", sessionId: currentSessionId }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  }, [currentSessionId, setError]);


  return (
    <aside className="context-inspector" aria-label={t("inspector")}>
      <div className="inspector-scroll">
        <section className="inspector-section">
          <header className="inspector-section-header"><h2>{t("environmentInfo")}</h2></header>
          <div className="inspector-stack">
            <InspectorRow icon={FileDiff} label={t("changes")}>
              {workspaceAdditions + workspaceDeletions > 0
                ? <><span className="plus">+{workspaceAdditions}</span><span className="minus">−{workspaceDeletions}</span></>
                : <span className="inspector-muted">{t("clean")}</span>}
            </InspectorRow>
            <InspectorRow icon={FolderGit2} label={t("local")}>
              <span className="inspector-value" title={snapshot.workspace}>{basename(snapshot.workspace)}</span>
            </InspectorRow>
            <div className="inspector-field">
              <div className="inspector-field-label"><GitBranch size={14} /><span>{t("branch")}</span></div>
              <MenuSelect
                className="inspector-branch-select"
                fit="full"
                placement="bottom"
                value={branches.find((branch) => branch.current)?.name || ""}
                options={branches.map((branch) => ({ value: branch.name, label: branch.name }))}
                onChange={(value) => void action("switch_git_branch", value)}
                ariaLabel={t("branch")}
              />
            </div>
          </div>
        </section>
        {todo && todo.phases.length > 0 && <TodoPlan todo={todo} language={snapshot.language} />}
        {backgroundProcesses.length > 0 && <section className="inspector-section"><header className="inspector-section-header"><h2>{t("backgroundProcesses")}</h2></header>{backgroundProcesses.map((process) => <div className="process-row" key={process.id}><SquareTerminal size={14} /><span><strong>{process.name || t("backgroundTerminal")}</strong><small title={process.command}>{process.command}</small></span><em data-state={process.state}>{process.state === "running" ? t("running") : process.state}</em></div>)}</section>}
        {agents.length > 0 && <SubagentSummary agents={agents} language={snapshot.language} open={() => { setInspectorOpen(false); setView("agents"); }} />}
        <section className="inspector-section">
          <header className="inspector-section-header">
            <h2>{t("sources")}</h2>
            <button className="icon-button" title={t("attach")} aria-label={t("attach")} onClick={() => document.querySelector<HTMLInputElement>(".attach-button input")?.click()}><Plus size={15} /></button>
          </header>
          {sources.length > 0
            ? sources.map((source) => <div className="source-row" key={source.id || source.path}><FileImage size={14} /><span title={source.name}>{source.name}</span></div>)
            : <p className="muted-copy">{t("noAttachments")}</p>}
        </section>
      </div>
    </aside>
  );
}

function TodoPlan({ todo, language }: { todo: TodoList; language: Snapshot["language"] }) {
  const t = translator(language);
  const items = todo.phases.flatMap((phase) => phase.items);
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  const open = items.length - completed;
  const percentage = items.length > 0 ? Math.round((completed / items.length) * 100) : 0;

  return <section className="inspector-section todo-section" aria-label={t("todoTitle")}>
    <header className="inspector-section-header">
      <h2><ListChecks size={14} />{t("todoTitle")}</h2>
      <small>{tFormat(language, "todoOpen", { count: open })}</small>
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

function SubagentSummary({ agents, language, open }: {
  agents: AgentState[];
  language: Snapshot["language"];
  open: () => void;
}) {
  const t = translator(language);
  const summary = subagentSummaryLabel(agents, language);
  const visibleGlyphs = [...agents].reverse().slice(0, 4);
  return (
    <section className="inspector-section subagents-section">
      <header className="inspector-section-header"><h2>{t("subagents")}</h2></header>
      <button type="button" className="subagent-summary-button" onClick={open} aria-label={`${t("openSubagents")}，${summary}`}>
        <span className="subagent-glyph-stack" aria-hidden="true">
          {visibleGlyphs.map((agent) => <SubagentGlyph key={agent.id} agent={agent} size={20} />)}
        </span>
        <span className="subagent-summary-copy" aria-live="polite">{summary}</span>
        <ChevronRight size={15} aria-hidden="true" />
      </button>
    </section>
  );
}

function InspectorRow({ icon: Icon, label, children }: { icon: typeof Wrench; label: string; children: React.ReactNode }) {
  return <div className="inspector-row"><Icon size={14} /><strong>{label}</strong><span className="toolbar-spacer" />{children}</div>;
}

function basename(path: string) { return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace"; }
