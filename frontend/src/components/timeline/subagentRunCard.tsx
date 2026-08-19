import { ChevronRight, Command } from "lucide-react";
import { useEffect, useState } from "react";
import { tFormat, translator } from "../../i18n";
import {
  isSubagentActive,
  isSubagentTerminal,
  subagentDisplayName,
  subagentPreviewText,
  subagentStatusLabel,
  subagentSummaryLabel,
} from "../../subagents";
import { useRuntimeStore } from "../../store";
import type { AgentState, Block, Snapshot } from "../../types";
import { isRunningTool } from "../toolTimeline";
import SubagentGlyph from "../SubagentGlyph";

export function isSubagentSpawnBlock(block: Block) {
  return block.kind === "tool" && block.title?.replaceAll("_", ".") === "subagent.spawn";
}

export function SubagentRunCard({ blocks, language }: { blocks: Block[]; language: Snapshot["language"] }) {
  const agents = useRuntimeStore((state) => state.agents);
  const selectAgent = useRuntimeStore((state) => state.selectAgent);
  const runId = blocks.find((block) => block.runId)?.runId || "";
  const callIds = new Set(blocks.map((block) => block.toolCallId).filter(Boolean));
  const descriptions = blocks.map(subagentSpawnDescription).filter(Boolean);
  const runAgents = agents.filter((agent) => !runId || agent.parentRunId === runId);
  const exactAgents = runAgents.filter((agent) => agent.parentToolCallId && callIds.has(agent.parentToolCallId));
  const describedAgents = runAgents.filter((agent) => descriptions.includes(agent.description));
  const cardAgents = exactAgents.length
    ? exactAgents
    : describedAgents.length
      ? describedAgents
      : runAgents.length === blocks.length ? runAgents : [];
  const count = Math.max(blocks.length, cardAgents.length);
  const activeCount = cardAgents.filter((agent) => isSubagentActive(agent.state)).length;
  const queuedCount = cardAgents.filter((agent) => agent.state === "queued").length;
  const terminalCount = cardAgents.filter((agent) => isSubagentTerminal(agent.state)).length;
  const failedCount = cardAgents.filter((agent) => agent.state === "failed").length;
  const toolRunning = blocks.some(isRunningTool);
  const active = activeCount > 0 || toolRunning;
  const [expanded, setExpanded] = useState(active);
  useEffect(() => {
    if (active) setExpanded(true);
  }, [active]);
  const state = active ? "running" : queuedCount > 0 ? "queued" : failedCount > 0 ? "failed" : "completed";
  const status = cardAgents.length
    ? subagentSummaryLabel(cardAgents, language)
    : toolRunning
      ? tFormat(language, "subagentsRunning", { count })
      : tFormat(language, "subagentsStarted", { count });
  const progress = count > 0 ? Math.min(100, Math.round((terminalCount / count) * 100)) : 0;
  const listId = `subagent-run-${blocks[0]?.id.replace(/[^a-zA-Z0-9_-]/gu, "-") || "group"}`;
  const t = translator(language);
  const headline = cardAgents.find((agent) => isSubagentActive(agent.state)) ?? cardAgents[0];
  const livePreview = headline
    ? subagentPreviewText(headline, subagentDisplayName(headline, cardAgents, language), language)
    : "";

  return <section className="subagent-run-card" data-state={state} aria-label={tFormat(language, "subagentRunTitle", { count })}>
    <button
      type="button"
      className="subagent-run-card-summary"
      aria-expanded={expanded}
      aria-controls={listId}
      aria-label={`${expanded ? t("subagentRunCollapse") : t("subagentRunExpand")}，${status}`}
      onClick={() => setExpanded((value) => !value)}
    >
      <span className="subagent-run-mark" aria-hidden="true">
        <Command size={18} />
        <i>{count}</i>
      </span>
      <span className="subagent-run-copy">
        <span>{t("subagentCenterEyebrow")}</span>
        <strong>{tFormat(language, "subagentRunTitle", { count })}</strong>
        <small>{status}</small>
        {active && livePreview ? <small className="subagent-run-live-preview">{livePreview}</small> : null}
      </span>
      <span className="subagent-run-status" data-state={state}>
        <strong>{failedCount > 0 && !active ? tFormat(language, "subagentRunFailed", { count: failedCount }) : active ? t("running") : t("completed")}</strong>
        <small>{tFormat(language, "subagentRunProgress", { completed: terminalCount, count })}</small>
        <span className="subagent-run-progress" aria-hidden="true">
          <i style={{ width: `${progress}%` }} data-indeterminate={active && progress === 0 || undefined} />
        </span>
      </span>
      <ChevronRight className="subagent-run-chevron" size={17} aria-hidden="true" />
    </button>
    {expanded ? <div className="subagent-run-list" id={listId}>
      {cardAgents.length ? cardAgents.map((agent) => <SubagentRunRow
        key={agent.id}
        agent={agent}
        agents={cardAgents}
        language={language}
        open={() => selectAgent(agent.id)}
      />) : descriptions.map((description, index) => <div className="subagent-run-pending-row" key={`${description}-${index}`}>
        <span className="subagent-run-pending-mark" aria-hidden="true" />
        <strong>{description}</strong>
        <em>{toolRunning ? t("agentInitializing") : t("agentQueued")}</em>
      </div>)}
    </div> : null}
  </section>;
}

function SubagentRunRow({ agent, agents, language, open }: {
  agent: AgentState;
  agents: AgentState[];
  language: Snapshot["language"];
  open: () => void;
}) {
  const name = subagentDisplayName(agent, agents, language);
  const preview = subagentPreviewText(agent, name, language);
  return <button type="button" className="subagent-run-row" data-state={agent.state} onClick={open}>
    <SubagentGlyph agent={agent} size={26} />
    <span><strong>{name}</strong><small>{preview}</small></span>
    <em>{subagentStatusLabel(agent.state, language)}</em>
    <ChevronRight size={14} aria-hidden="true" />
  </button>;
}

function subagentSpawnDescription(block: Block) {
  const raw = block.data?.arguments || block.content || "";
  for (const candidate of [raw, raw.split("\n")[0] || ""]) {
    try {
      const parsed = JSON.parse(candidate) as Record<string, unknown>;
      const value = String(parsed.description || parsed.prompt || "").trim();
      if (value) return value;
    } catch { /* durable tool content may append a result after the JSON arguments */ }
  }
  const match = raw.match(/"(?:description|prompt)"\s*:\s*"((?:\\.|[^"\\])*)"/u);
  if (!match?.[1]) return "";
  try { return JSON.parse(`"${match[1]}"`) as string; } catch { return match[1]; }
}
