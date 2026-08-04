import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, LoaderCircle, MessageSquare, Square, X } from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { AgentState, Block, Snapshot } from "../types";
import { formatDuration } from "./ThreadSurface";
import { TimelineFeed } from "./Timeline";

/** Codex-style docked side chat for subagent transcripts. */
export default function AgentSideChat() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const agents = useRuntimeStore((state) => state.agents);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const agentBlocks = useRuntimeStore((state) => state.agentBlocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const setError = useRuntimeStore((state) => state.setError);
  const scrollRef = useRef<HTMLDivElement>(null);
  const language = snapshot.language;
  const t = translator(language);
  const agent = agents.find((item) => item.id === selectedAgentId) || null;
  const running = agent?.state === "running" || agent?.state === "started";
  const role = agent?.type || selectedAgentId || "agent";
  const live = useLiveAgentStats(agent, agentBlocks, selectedAgentId, running);

  // Initial hydrate + light poll while running so the side chat stays live even if a frame was missed.
  useEffect(() => {
    if (!selectedAgentId) return;
    let cancelled = false;
    const inspect = () => {
      void execute({ kind: "inspect_agent", target: selectedAgentId, sessionId: currentSessionId })
        .catch((cause) => {
          if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
        });
    };
    inspect();
    const shouldPoll = running;
    if (!shouldPoll) return () => { cancelled = true; };
    const timer = window.setInterval(inspect, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [currentSessionId, selectedAgentId, running, setError]);

  useEffect(() => {
    const node = scrollRef.current;
    if (!node) return;
    node.scrollTop = node.scrollHeight;
  }, [agentBlocks, selectedAgentId]);

  const close = () => selectAgent("");
  const cancel = async () => {
    if (!selectedAgentId) return;
    try { await execute({ kind: "cancel_agent", target: selectedAgentId, sessionId: currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const switchAgent = (id: string) => {
    selectAgent(id);
  };

  return (
    <aside className="agent-side-chat" aria-label={t("sideChat")}>
      <header className="agent-side-chat-header">
        <div className="agent-side-chat-heading">
          <MessageSquare size={15} />
          <div className="agent-side-chat-title">
            <strong title={role}>{t("sideChat")}</strong>
            <small>
              <span className="agent-side-chat-role">{role}</span>
              <em data-state={agent?.state || "idle"}>{agentStateLabel(agent?.state, language)}</em>
              {(running || live.elapsedMs > 0) ? <time>{formatDuration(live.elapsedMs)}</time> : null}
            </small>
          </div>
        </div>
        <div className="agent-side-chat-actions">
          {running && (
            <button type="button" className="icon-button" title={t("toolStopSubagent")} aria-label={t("toolStopSubagent")} onClick={() => void cancel()}>
              <Square size={13} />
            </button>
          )}
          <button type="button" className="icon-button" title={t("closeSideChat")} aria-label={t("closeSideChat")} onClick={close}>
            <X size={15} />
          </button>
        </div>
      </header>

      {agents.length > 1 && (
        <div className="agent-side-chat-tabs" role="tablist" aria-label={t("subagents")}>
          {agents.map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              aria-selected={item.id === selectedAgentId}
              className={item.id === selectedAgentId ? "active" : ""}
              onClick={() => switchAgent(item.id)}
              title={item.summary || item.description || item.type}
            >
              <span className="agent-tab-dot" data-state={item.state} />
              <span>{item.type || item.id.slice(0, 8)}</span>
            </button>
          ))}
        </div>
      )}

      <AgentMetaBar agent={agent} language={language} toolCalls={live.toolCalls} tokensUsed={live.tokensUsed} />

      <div className="agent-side-chat-scroll" ref={scrollRef}>
        {agentBlocks.length === 0 ? (
          <div className="agent-side-chat-empty">
            {running ? <LoaderCircle className="spin" size={20} /> : <Bot size={22} />}
            <p>{running ? t("syncingAgentTimeline") : t("emptySideChat")}</p>
          </div>
        ) : (
          <div className="agent-side-chat-transcript">
            {/* Same full TimelineFeed as main thread — no compact truncation. */}
            <TimelineFeed blocks={agentBlocks} language={language} />
          </div>
        )}
      </div>
    </aside>
  );
}

/** Derive live tool/elapsed stats even when agent_state events lag behind the stream. */
function useLiveAgentStats(
  agent: AgentState | null,
  agentBlocks: Block[],
  agentId: string,
  running: boolean,
) {
  const timelineTools = useMemo(
    () => agentBlocks.filter((block) => block.kind === "tool").length,
    [agentBlocks],
  );
  const toolCalls = Math.max(agent?.toolCalls ?? 0, timelineTools);
  const tokensUsed = agent?.tokensUsed ?? 0;

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

  return { toolCalls, tokensUsed, elapsedMs };
}

function AgentMetaBar({ agent, language, toolCalls, tokensUsed }: {
  agent: AgentState | null;
  language: Snapshot["language"];
  toolCalls: number;
  tokensUsed: number;
}) {
  if (!agent) return null;
  const t = translator(language);
  // Meta row only: model / isolation / tools / tokens — not the long Findings summary.
  const bits = [
    agent.model || t("inheritModel"),
    agent.isolation && agent.isolation !== "none" ? agent.isolation : "",
    agent.capabilityMode,
    `${toolCalls} ${t("toolsLabel")}`,
    tokensUsed > 0 ? `${tokensUsed.toLocaleString()} tok` : "",
  ].filter(Boolean);
  if (bits.length === 0) return null;
  return (
    <div className="agent-side-chat-meta">
      <div>{bits.map((bit) => <span key={bit}>{bit}</span>)}</div>
    </div>
  );
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

