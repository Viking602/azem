import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, X } from "lucide-react";
import { translator } from "../i18n";
import {
  formatSubagentElapsed,
  isSubagentActive,
  subagentDisplayName,
  subagentEvidenceStatusLabel,
  subagentElapsedMs,
  subagentPreviewText,
  subagentSummaryLabel,
  subagentStatusLabel,
} from "../subagents";
import { useRuntimeStore } from "../store";
import type { AgentState } from "../types";
import SubagentGlyph from "./SubagentGlyph";

export default function SubagentsPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const agents = useRuntimeStore((state) => state.agents);
  const setView = useRuntimeStore((state) => state.setView);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const [now, setNow] = useState(() => Date.now());
  const titleRef = useRef<HTMLHeadingElement>(null);
  const language = snapshot.language;
  const t = translator(language);
  const orderedAgents = useMemo(() => [...agents].reverse(), [agents]);
  const hasActiveAgents = agents.some((agent) => isSubagentActive(agent.state));
  const groups = useMemo(() => [
    { key: "active", label: t("subagentActiveGroup"), agents: orderedAgents.filter((agent) => isSubagentActive(agent.state)) },
    { key: "queued", label: t("subagentQueuedGroup"), agents: orderedAgents.filter((agent) => agent.state === "queued") },
    { key: "finished", label: t("subagentFinishedGroup"), agents: orderedAgents.filter((agent) => !isSubagentActive(agent.state) && agent.state !== "queued") },
  ].filter((group) => group.agents.length > 0), [orderedAgents, t]);
  const activeCount = groups.find((group) => group.key === "active")?.agents.length ?? 0;
  const queuedCount = groups.find((group) => group.key === "queued")?.agents.length ?? 0;
  const finishedCount = groups.find((group) => group.key === "finished")?.agents.length ?? 0;

  useEffect(() => {
    titleRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!hasActiveAgents) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [hasActiveAgents]);

  const closePage = () => {
    selectAgent("");
    setView("thread");
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(".thread-plan-trigger, .terminal-toggle")?.focus());
  };

  const inspectAgent = (agentId: string) => selectAgent(agentId);

  return (
    <section className="subagents-page" role="dialog" aria-modal="true" aria-labelledby="subagents-page-title">
      <header className="subagents-page-bar titlebar-region">
        <div className="subagents-page-heading">
          <span>{t("subagentCenterEyebrow")}</span>
          <h1 id="subagents-page-title" ref={titleRef} tabIndex={-1}>{t("subagentCenter")}</h1>
          <p aria-live="polite">{subagentSummaryLabel(agents, language)}</p>
        </div>
        <button type="button" className="subagents-close" onClick={closePage} aria-label={t("closeSubagents")}>
          <X size={16} />
        </button>
      </header>

      <div className="subagents-page-scroll">
        <div className="subagents-page-content">
          <div className="subagents-overview" aria-label={t("subagentCenter")}>
            <div data-state="active"><strong>{activeCount}</strong><span>{t("subagentActiveGroup")}</span></div>
            <div data-state="queued"><strong>{queuedCount}</strong><span>{t("subagentQueuedGroup")}</span></div>
            <div data-state="finished"><strong>{finishedCount}</strong><span>{t("subagentFinishedGroup")}</span></div>
          </div>

          {agents.length === 0 ? (
            <div className="subagents-empty">
              <Bot size={26} aria-hidden="true" />
              <p>{t("noAgents")}</p>
            </div>
          ) : (
            <div className="subagent-groups">
              {groups.map((group) => <section className="subagent-group" data-group={group.key} key={group.key}>
                <header><h2>{group.label}</h2><span>{group.agents.length}</span></header>
                <ul className="subagents-list">
                  {group.agents.map((agent) => <SubagentRow
                    key={agent.id}
                    agent={agent}
                    agents={agents}
                    language={language}
                    now={now}
                    inspect={inspectAgent}
                  />)}
                </ul>
              </section>)}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function SubagentRow({ agent, agents, language, now, inspect }: {
  agent: AgentState;
  agents: AgentState[];
  language: "en" | "zh-CN";
  now: number;
  inspect: (agentId: string) => void;
}) {
  const name = subagentDisplayName(agent, agents, language);
  const preview = subagentPreviewText(agent, name, language);
  const status = subagentStatusLabel(agent.state, language);
  const evidenceStatus = subagentEvidenceStatusLabel(agent.evidenceStatus, language);
  const elapsed = subagentElapsedMs(agent, now);
  const elapsedLabel = formatSubagentElapsed(elapsed);
  const active = isSubagentActive(agent.state);
  const showElapsed = agent.state !== "queued" && (active || elapsed > 0);

  return (
    <li className="subagent-row" data-state={agent.state}>
      <button
        type="button"
        onClick={() => inspect(agent.id)}
        aria-label={`${name}，${status}${evidenceStatus ? `，${evidenceStatus}` : ""}${showElapsed ? `，${elapsedLabel}` : ""}`}
      >
        <SubagentGlyph agent={agent} size={34} />
        <span className="subagent-row-copy">
          <strong>{name}</strong>
          <span>{preview}</span>
        </span>
        <span className="subagent-row-meta">
          <em data-state={agent.state}>{status}</em>
          {evidenceStatus ? <span className="subagent-evidence-status" data-evidence-status={agent.evidenceStatus}>{evidenceStatus}</span> : null}
          {showElapsed ? <time aria-label={`${status} ${elapsedLabel}`}>{elapsedLabel}</time> : null}
        </span>
      </button>
    </li>
  );
}
