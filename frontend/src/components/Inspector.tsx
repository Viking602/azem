import {
  Bot, CheckCircle2, CircleDot, FileDiff, FolderGit2, GitBranch, Layers3,
  MemoryStick, PanelRightClose, Square, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { InspectorTab } from "../types";
import { formatDuration } from "./ThreadSurface";

export default function Inspector() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const tab = useRuntimeStore((state) => state.inspectorTab);
  const setTab = useRuntimeStore((state) => state.setInspectorTab);
  const setOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const blocks = useRuntimeStore((state) => state.blocks);
  const branches = useRuntimeStore((state) => state.branches);
  const agents = useRuntimeStore((state) => state.agents);
  const context = useRuntimeStore((state) => state.contextProfile);
  const todo = useRuntimeStore((state) => state.todo);
  const recovery = useRuntimeStore((state) => state.recovery);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const agentBlocks = useRuntimeStore((state) => state.agentBlocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const tabs: Array<[InspectorTab, string]> = [["environment", t("environment")], ["changes", t("changes")], ["agents", t("agents")], ["context", t("context")]];
  const diffs = blocks.filter((block) => block.kind === "diff");

  const action = async (kind: string, target = "", decision = "") => {
    try { await execute({ kind, target, decision, sessionId: currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  return (
    <aside className="context-inspector">
      <header><span>{t("inspector")}</span><button className="icon-button" onClick={() => setOpen(false)}><PanelRightClose size={15} /></button></header>
      <div className="inspector-tabs" role="tablist">{tabs.map(([value, label]) => <button key={value} role="tab" aria-selected={tab === value} className={tab === value ? "active" : ""} onClick={() => setTab(value)}>{label}</button>)}</div>
      <div className="inspector-scroll">
        {tab === "environment" && <>
          <section className="inspector-card">
            <InspectorRow icon={FileDiff} label={t("changes")}><span className="plus">+{sum(diffs, "additions")}</span><span className="minus">−{sum(diffs, "deletions")}</span></InspectorRow>
            <InspectorRow icon={FolderGit2} label={basename(snapshot.workspace)}><span>{t("local")}</span></InspectorRow>
            <InspectorRow icon={GitBranch} label={t("branch")}><select value={branches.find((branch) => branch.current)?.name || ""} onChange={(event) => action("switch_git_branch", event.target.value)}>{branches.map((branch) => <option key={branch.name}>{branch.name}</option>)}</select></InspectorRow>
            <InspectorRow icon={CheckCircle2} label={t("runtimeHealthy")}><span className="status-dot success" /></InspectorRow>
          </section>
          {recovery.length > 0 && <section className="inspector-card warning-card"><h3>需要处理</h3><p>{recovery.length} 个恢复项正在等待确认。</p></section>}
          <AgentList agents={agents} selectedAgentId={selectedAgentId} inspect={(id) => action("inspect_agent", id)} />
        </>}
        {tab === "changes" && <section className="inspector-card"><h3>{t("changes")}</h3>{diffs.length ? diffs.map((diff) => <button className="change-row" key={diff.id}><FileDiff size={14} /><span>{diff.title}</span><small><b className="plus">+{diff.data?.additions || 0}</b> <b className="minus">−{diff.data?.deletions || 0}</b></small></button>) : <Empty icon={FileDiff} text={t("noChanges")} />}</section>}
        {tab === "agents" && <>
          <AgentList agents={agents} selectedAgentId={selectedAgentId} inspect={(id) => action("inspect_agent", id)} />
          {selectedAgentId && <section className="inspector-card agent-transcript"><h3>Agent timeline</h3>{agentBlocks.length ? agentBlocks.map((block) => <div key={block.id}><span>{block.title || block.kind}</span><p>{block.content}</p></div>) : <p className="muted-copy">正在读取 Agent 时间线…</p>}<button className="danger-text" onClick={() => action("cancel_agent", selectedAgentId)}>取消这个 Agent</button></section>}
        </>}
        {tab === "context" && <>
          <section className="inspector-card"><h3><MemoryStick size={14} />上下文</h3>{context?.contributions?.length ? context.contributions.map((item) => <div className="context-row" key={`${item.category}-${item.name}`}><span>{item.name}</span><b>{formatTokens(item.tokens)}</b><i style={{ width: `${Math.min(100, item.tokens / Math.max(1, totalTokens(context.contributions)) * 100)}%` }} /></div>) : <Empty icon={Layers3} text="等待上下文快照" />}</section>
          <section className="inspector-card"><h3><CircleDot size={14} />Todo</h3>{todo ? <pre className="json-preview">{JSON.stringify(todo, null, 2)}</pre> : <p className="muted-copy">当前没有任务清单。</p>}</section>
        </>}
      </div>
    </aside>
  );
}

function AgentList({ agents, selectedAgentId, inspect }: { agents: ReturnType<typeof useRuntimeStore.getState>["agents"]; selectedAgentId: string; inspect: (id: string) => void }) {
  return <section className="inspector-card"><h3><Bot size={14} />Agents</h3>{agents.length ? agents.map((agent) => <button key={agent.id} className={`agent-row ${selectedAgentId === agent.id ? "active" : ""}`} onClick={() => inspect(agent.id)}><span className="agent-avatar">{(agent.type || "A").slice(0, 1).toUpperCase()}</span><span><strong>{agent.type || agent.id}</strong><small>{agent.summary || agent.activity || agent.description}</small><i><em style={{ width: agent.state === "completed" ? "100%" : agent.state === "running" ? "64%" : "10%" }} /></i></span><time>{agent.elapsedMs ? formatDuration(agent.elapsedMs) : agent.state}</time></button>) : <Empty icon={Bot} text="当前没有子代理" />}</section>;
}

function InspectorRow({ icon: Icon, label, children }: { icon: typeof Wrench; label: string; children: React.ReactNode }) {
  return <div className="inspector-row"><Icon size={14} /><strong>{label}</strong><span className="toolbar-spacer" />{children}</div>;
}

function Empty({ icon: Icon, text }: { icon: typeof Square; text: string }) {
  return <div className="inspector-empty"><Icon size={18} /><span>{text}</span></div>;
}

function sum(blocks: ReturnType<typeof useRuntimeStore.getState>["blocks"], key: "additions" | "deletions") {
  return blocks.reduce((total, block) => total + Number(block.data?.[key] || 0), 0);
}

function basename(path: string) { return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace"; }
function formatTokens(value: number) { return value > 999 ? `${(value / 1000).toFixed(1)}k` : String(value); }
function totalTokens(items: Array<{ tokens: number }>) { return items.reduce((total, item) => total + item.tokens, 0); }
