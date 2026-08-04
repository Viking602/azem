import { useEffect, useMemo, useState } from "react";
import {
  Bot, FileDiff, FileImage, FolderGit2, GitBranch, Plus, SquareTerminal, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { AgentState, Snapshot } from "../types";
import MenuSelect from "./MenuSelect";

const AGENT_ACCENTS = [
  "#3478f6", "#ef4444", "#f59e0b", "#10b981", "#8b5cf6",
  "#06b6d4", "#ec4899", "#84cc16", "#f97316", "#6366f1",
  "#14b8a6", "#a855f7", "#e11d48", "#0ea5e9", "#d946ef",
];
const AGENT_LIST_PREVIEW = 12;

export default function Inspector() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceAdditions = useRuntimeStore((state) => state.workspaceAdditions);
  const workspaceDeletions = useRuntimeStore((state) => state.workspaceDeletions);
  const agents = useRuntimeStore((state) => state.agents);
  const backgroundProcesses = useRuntimeStore((state) => state.backgroundProcesses);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const sources = Array.from(new Map(blocks.flatMap((block) => block.attachments ?? []).map((item) => [item.id || item.path, item])).values());

  const action = async (kind: string, target = "", decision = "") => {
    try { await execute({ kind, target, decision, sessionId: currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  useEffect(() => {
    void execute({ kind: "list_background", sessionId: currentSessionId }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  }, [currentSessionId, setError]);

  const openAgent = (id: string) => {
    selectAgent(id);
    void action("inspect_agent", id);
  };

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
        {backgroundProcesses.length > 0 && <section className="inspector-section"><header className="inspector-section-header"><h2>{t("backgroundProcesses")}</h2></header>{backgroundProcesses.map((process) => <div className="process-row" key={process.id}><SquareTerminal size={14} /><span><strong>{process.name || t("backgroundTerminal")}</strong><small title={process.command}>{process.command}</small></span><em data-state={process.state}>{process.state === "running" ? t("running") : process.state}</em></div>)}</section>}
        {agents.length > 0 && <AgentList agents={agents} selectedAgentId={selectedAgentId} inspect={openAgent} language={snapshot.language} />}
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

/** Codex-style dense Subagents roster: colored glyph + nickname, scales to dozens. */
function AgentList({ agents, selectedAgentId, inspect, language }: {
  agents: AgentState[];
  selectedAgentId: string;
  inspect: (id: string) => void;
  language: Snapshot["language"];
}) {
  const [expanded, setExpanded] = useState(false);
  const labels = useMemo(() => agentRosterLabels(agents, language), [agents, language]);
  const visible = expanded || agents.length <= AGENT_LIST_PREVIEW
    ? agents
    : agents.slice(0, AGENT_LIST_PREVIEW);
  const hidden = Math.max(0, agents.length - visible.length);
  const title = language === "zh-CN" ? "子代理" : "Subagents";

  return <section className="inspector-section subagents-section">
    <header className="inspector-section-header">
      <h2>{title}</h2>
      <small>{agents.length}</small>
    </header>
    <div className="agent-roster" role="list">
      {visible.map((agent) => {
        const label = labels.get(agent.id) || agent.type || agent.id;
        const accent = agentAccent(agent.id);
        const summary = agent.summary || agent.activity || agent.description || "";
        const status = agentStateLabel(agent.state, language);
        return <button
          key={agent.id}
          type="button"
          role="listitem"
          className={`agent-roster-row ${selectedAgentId === agent.id ? "active" : ""}`}
          data-state={agent.state}
          onClick={() => inspect(agent.id)}
          title={[label, status, summary].filter(Boolean).join(" · ")}
        >
          <span className="agent-glyph" style={{ color: accent }} data-state={agent.state} aria-hidden="true">
            <span className="agent-glyph-mark">{glyphFor(agent)}</span>
          </span>
          <span className="agent-roster-name">{label}</span>
          {(agent.state === "running" || agent.state === "started") && <span className="agent-live-dot" aria-label={status} />}
        </button>;
      })}
    </div>
    {hidden > 0 && (
      <button type="button" className="agent-show-more" onClick={() => setExpanded(true)}>
        {language === "zh-CN" ? `显示另外 ${hidden} 个` : `Show ${hidden} more`}
      </button>
    )}
    {expanded && agents.length > AGENT_LIST_PREVIEW && (
      <button type="button" className="agent-show-more" onClick={() => setExpanded(false)}>
        {language === "zh-CN" ? "收起" : "Show less"}
      </button>
    )}
  </section>;
}

function agentRosterLabels(agents: AgentState[], language: Snapshot["language"]) {
  const typeCounts = new Map<string, number>();
  const typeIndex = new Map<string, number>();
  for (const agent of agents) {
    const key = (agent.type || "agent").toLowerCase();
    typeCounts.set(key, (typeCounts.get(key) || 0) + 1);
  }
  const labels = new Map<string, string>();
  for (const agent of agents) {
    const key = (agent.type || "agent").toLowerCase();
    const total = typeCounts.get(key) || 1;
    const next = (typeIndex.get(key) || 0) + 1;
    typeIndex.set(key, next);
    const role = agent.type || "agent";
    if (total <= 1) {
      labels.set(agent.id, role);
      continue;
    }
    // Codex-style ordinal nicknames: "Executor the 17th"
    labels.set(agent.id, language === "zh-CN"
      ? `${role} · ${next}`
      : `${role} the ${ordinal(next)}`);
  }
  return labels;
}

function ordinal(n: number) {
  const v = n % 100;
  if (v >= 11 && v <= 13) return `${n}th`;
  switch (n % 10) {
    case 1: return `${n}st`;
    case 2: return `${n}nd`;
    case 3: return `${n}rd`;
    default: return `${n}th`;
  }
}

function agentAccent(id: string) {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) hash = (hash * 33 + id.charCodeAt(i)) >>> 0;
  return AGENT_ACCENTS[hash % AGENT_ACCENTS.length]!;
}

function glyphFor(agent: AgentState) {
  const role = (agent.type || "A").trim();
  return role.slice(0, 1).toUpperCase() || "A";
}

function agentStateLabel(state: string | undefined, language: Snapshot["language"]) {
  const t = translator(language);
  if (state === "running" || state === "started") return t("running");
  if (state === "completed") return t("agentCompleted");
  if (state === "failed") return t("agentFailed");
  if (state === "cancelled" || state === "canceled") return t("agentCancelled");
  if (state === "queued") return t("agentQueued");
  return state || t("agentIdle");
}

function InspectorRow({ icon: Icon, label, children }: { icon: typeof Wrench; label: string; children: React.ReactNode }) {
  return <div className="inspector-row"><Icon size={14} /><strong>{label}</strong><span className="toolbar-spacer" />{children}</div>;
}

function basename(path: string) { return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace"; }
