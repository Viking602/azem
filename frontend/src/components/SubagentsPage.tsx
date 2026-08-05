import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Plus, X } from "lucide-react";
import { execute } from "../bridge";
import { tFormat, translator } from "../i18n";
import {
  formatSubagentElapsed,
  isSubagentActive,
  subagentDisplayName,
  subagentElapsedMs,
  subagentPreviewText,
  subagentStatusLabel,
} from "../subagents";
import { useRuntimeStore } from "../store";
import type { AgentState } from "../types";
import SubagentGlyph from "./SubagentGlyph";

const INITIAL_VISIBLE_AGENTS = 4;

export default function SubagentsPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const agents = useRuntimeStore((state) => state.agents);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setView = useRuntimeStore((state) => state.setView);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const [expanded, setExpanded] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const titleRef = useRef<HTMLHeadingElement>(null);
  const language = snapshot.language;
  const t = translator(language);
  const orderedAgents = useMemo(() => [...agents].reverse(), [agents]);
  const hasActiveAgents = agents.some((agent) => isSubagentActive(agent.state));
  const visibleAgents = expanded ? orderedAgents : orderedAgents.slice(0, INITIAL_VISIBLE_AGENTS);
  const hidden = Math.max(0, orderedAgents.length - visibleAgents.length);

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
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(".inspector-toggle")?.focus());
  };

  const newThread = async () => {
    try {
      selectAgent("");
      await execute({ kind: "new_session", sessionId: currentSessionId });
      setView("thread");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const inspectAgent = async (agentId: string) => {
    selectAgent(agentId);
    try {
      await execute({ kind: "inspect_agent", target: agentId, sessionId: currentSessionId });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return (
    <section className="subagents-page" aria-labelledby="subagents-page-title">
      <header className="subagents-page-bar titlebar-region">
        <div className="subagents-page-tab" aria-current="page">
          <Bot size={16} aria-hidden="true" />
          <h1 id="subagents-page-title" ref={titleRef} tabIndex={-1}>{t("subagents")}</h1>
          <button type="button" className="subagents-close" onClick={closePage} title={t("closeSubagents")} aria-label={t("closeSubagents")}>
            <X size={14} />
          </button>
        </div>
        <button type="button" className="icon-button subagents-new-thread" onClick={() => void newThread()} title={t("newSession")} aria-label={t("newSession")}>
          <Plus size={17} />
        </button>
      </header>

      <div className="subagents-page-scroll">
        <div className="subagents-page-content">
          <p className="subagents-page-count" aria-live="polite">
            {tFormat(language, "subagentsStarted", { count: agents.length })}
          </p>

          {agents.length === 0 ? (
            <div className="subagents-empty">
              <Bot size={26} aria-hidden="true" />
              <p>{t("noAgents")}</p>
            </div>
          ) : (
            <>
              <ul className="subagents-list">
                {visibleAgents.map((agent) => (
                  <SubagentRow
                    key={agent.id}
                    agent={agent}
                    agents={agents}
                    language={language}
                    now={now}
                    selected={selectedAgentId === agent.id}
                    inspect={inspectAgent}
                  />
                ))}
              </ul>
              {hidden > 0 && (
                <button type="button" className="subagents-show-more" onClick={() => setExpanded(true)}>
                  {tFormat(language, "subagentsShowMore", { count: hidden })}
                </button>
              )}
              {expanded && orderedAgents.length > INITIAL_VISIBLE_AGENTS && (
                <button type="button" className="subagents-show-more" onClick={() => setExpanded(false)}>
                  {t("subagentsShowLess")}
                </button>
              )}
            </>
          )}
        </div>
      </div>
    </section>
  );
}

function SubagentRow({ agent, agents, language, now, selected, inspect }: {
  agent: AgentState;
  agents: AgentState[];
  language: "en" | "zh-CN";
  now: number;
  selected: boolean;
  inspect: (agentId: string) => Promise<void>;
}) {
  const name = subagentDisplayName(agent, agents, language);
  const preview = subagentPreviewText(agent, name, language);
  const status = subagentStatusLabel(agent.state, language);
  const elapsed = subagentElapsedMs(agent, now);
  const elapsedLabel = formatSubagentElapsed(elapsed);
  const active = isSubagentActive(agent.state);
  const meta = agent.state === "queued"
    ? status
    : active
      ? elapsedLabel
      : elapsed > 0
        ? `${status} · ${elapsedLabel}`
        : status;

  return (
    <li className="subagent-row" data-state={agent.state}>
      <button
        type="button"
        className={selected ? "active" : ""}
        onClick={() => void inspect(agent.id)}
        aria-label={`${name}，${status}，${meta}`}
      >
        <SubagentGlyph agent={agent} size={34} />
        <span className="subagent-row-copy">
          <strong title={name}>{name}</strong>
          <span title={preview}>{preview}</span>
        </span>
        {active ? <time className="subagent-row-meta" aria-label={`${status} ${elapsedLabel}`}>{elapsedLabel}</time> : <span className="subagent-row-meta">{meta}</span>}
      </button>
    </li>
  );
}
