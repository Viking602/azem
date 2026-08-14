import { useEffect, useRef, useState } from "react";
import { Bot, LoaderCircle, Square, X } from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { isSubagentActive, subagentDisplayName, subagentStatusLabel } from "../subagents";
import { useRuntimeStore } from "../store";
import type { AgentState } from "../types";
import SubagentGlyph from "./SubagentGlyph";
import { formatDuration } from "./ThreadSurface";
import { TimelineFeed } from "./Timeline";

/** Focused drawer for one subagent transcript. */
export default function AgentSideChat() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const agents = useRuntimeStore((state) => state.agents);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const agentBlocks = useRuntimeStore((state) => state.agentBlocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const scrollRef = useRef<HTMLDivElement>(null);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const followTail = useRef(true);
  const language = snapshot.language;
  const t = translator(language);
  const agent = agents.find((item) => item.id === selectedAgentId) || null;
  const running = isSubagentActive(agent?.state);
  const cancellable = agent?.state === "initializing" || agent?.state === "queued" || agent?.state === "running" || agent?.state === "started";
  const role = agent ? subagentDisplayName(agent, agents, language) : selectedAgentId || t("subagents");
  const activeRunId = agentBlocks.reduce((latest, block) => block.runId || latest, "") || agent?.previewRunId || "";
  const liveElapsedMs = useLiveAgentElapsed(agent, selectedAgentId, running);

  // Hydrate once. Live agent events are the source of truth; polling the full
  // transcript repeatedly made large subagent chats deserialize and rerender
  // every 1.5 seconds.
  useEffect(() => {
    if (!selectedAgentId) return;
    let cancelled = false;
    followTail.current = true;
    requestAnimationFrame(() => titleRef.current?.focus());
    void execute({ kind: "inspect_agent", target: selectedAgentId, sessionId: currentSessionId })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; };
  }, [currentSessionId, selectedAgentId, setError]);

  useEffect(() => {
    const node = scrollRef.current;
    if (!node || !followTail.current) return;
    const frame = requestAnimationFrame(() => {
      if (followTail.current) node.scrollTop = node.scrollHeight;
    });
    return () => cancelAnimationFrame(frame);
  }, [agentBlocks, selectedAgentId]);

  const close = () => {
    selectAgent("");
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(".subagent-summary-button, .subagent-run-card-summary")?.focus());
  };
  const cancel = async () => {
    if (!selectedAgentId) return;
    try { await execute({ kind: "cancel_agent", target: selectedAgentId, sessionId: currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const switchAgent = (id: string) => {
    selectAgent(id);
  };
  const navigateTabs = (event: React.KeyboardEvent<HTMLButtonElement>, id: string) => {
    const current = agents.findIndex((item) => item.id === id);
    let next = current;
    if (event.key === "ArrowRight") next = (current + 1) % agents.length;
    else if (event.key === "ArrowLeft") next = (current - 1 + agents.length) % agents.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = agents.length - 1;
    else return;
    event.preventDefault();
    const nextId = agents[next]?.id;
    if (!nextId) return;
    switchAgent(nextId);
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(`[data-agent-tab="${CSS.escape(nextId)}"]`)?.focus());
  };

  return (
    <aside className="agent-side-chat" role="dialog" aria-modal="true" aria-labelledby="subagent-detail-title">
      <header className="agent-side-chat-header">
        <div className="agent-side-chat-heading">
          {agent ? <SubagentGlyph agent={agent} size={34} /> : <Bot size={24} aria-hidden="true" />}
          <div className="agent-side-chat-title">
            <h2 id="subagent-detail-title" ref={titleRef} tabIndex={-1}>{role}</h2>
            <small>
              <em data-state={agent?.state || "idle"}>{subagentStatusLabel(agent?.state, language)}</em>
              {(running || liveElapsedMs > 0) ? <time>{formatDuration(liveElapsedMs)}</time> : null}
            </small>
          </div>
        </div>
        <div className="agent-side-chat-actions">
          {cancellable && (
            <button type="button" className="icon-button" aria-label={t("toolStopSubagent")} onClick={() => void cancel()}>
              <Square size={13} />
            </button>
          )}
          <button type="button" className="icon-button" aria-label={t("closeSideChat")} onClick={close}>
            <X size={15} />
          </button>
        </div>
      </header>

      {agents.length > 1 && (
        <div className="agent-side-chat-switcher">
          <span>{t("subagentTeam")}</span>
          <div className="agent-side-chat-tabs" role="tablist" aria-label={t("subagents")}>
            {agents.map((item) => {
              const itemName = subagentDisplayName(item, agents, language);
              return <button
                key={item.id}
                type="button"
                role="tab"
                aria-selected={item.id === selectedAgentId}
                tabIndex={item.id === selectedAgentId ? 0 : -1}
                aria-controls="subagent-detail-panel"
                className={item.id === selectedAgentId ? "active" : ""}
                data-agent-tab={item.id}
                onClick={() => switchAgent(item.id)}
                onKeyDown={(event) => navigateTabs(event, item.id)}
                aria-label={`${itemName}，${subagentStatusLabel(item.state, language)}`}
              >
                <SubagentGlyph agent={item} size={24} />
              </button>;
            })}
          </div>
          <em>{agents.length}</em>
        </div>
      )}

      <div
        className="agent-side-chat-scroll"
        id="subagent-detail-panel"
        role="tabpanel"
        aria-label={role}
        ref={scrollRef}
        onScroll={(event) => {
          const node = event.currentTarget;
          followTail.current = node.scrollHeight - node.scrollTop - node.clientHeight < 48;
        }}
      >
        {agentBlocks.length === 0 ? (
          <div className="agent-side-chat-empty">
            {running ? <LoaderCircle className="spin" size={20} /> : <Bot size={22} />}
            <p>{agent?.description || (running ? t("syncingAgentTimeline") : t("emptySideChat"))}</p>
          </div>
        ) : (
          <div className="agent-side-chat-transcript transcript">
            <TimelineFeed
              blocks={agentBlocks}
              language={language}
              activeRunId={activeRunId}
              running={running}
              collapseCompletedProcess
            />
          </div>
        )}
      </div>
    </aside>
  );
}

/** Derive live tool/elapsed stats even when agent_state events lag behind the stream. */
function useLiveAgentElapsed(
  agent: AgentState | null,
  agentId: string,
  running: boolean,
) {
  const [now, setNow] = useState(() => Date.now());
  const startedAt = useRef(0);
  const trackedId = useRef("");

  useEffect(() => {
    if (!running || !agentId) {
      startedAt.current = 0;
      trackedId.current = "";
      return;
    }
    if (trackedId.current !== agentId || !startedAt.current) {
      trackedId.current = agentId;
      // Prefer server-reported elapsed so reopen mid-run doesn't reset the clock.
      startedAt.current = Date.now() - Math.max(0, agent?.elapsedMs ?? 0);
    } else if ((agent?.elapsedMs ?? 0) > 0) {
      const serverStart = Date.now() - agent!.elapsedMs;
      // Only rewind the base if the server is ahead (never jump the clock backwards mid-tick).
      if (serverStart < startedAt.current) startedAt.current = serverStart;
    }
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [running, agentId, agent?.elapsedMs]);

  const elapsedMs = running && startedAt.current
    ? Math.max(0, now - startedAt.current)
    : Math.max(0, agent?.elapsedMs ?? 0);

  return elapsedMs;
}
