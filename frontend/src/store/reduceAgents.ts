import type { Block, RuntimeEvent } from "../types";
import { normalizeAgent, normalizeAgentCatalog, normalizeBackgroundProcess, normalizeBlock, upsertAgent } from "./normalize";
import type { RuntimeData } from "./state";

/** Subagent and background process projections: agent_state / background_state / agent_detail. */
export function reduceAgentEvent(next: RuntimeData, event: RuntimeEvent): void {
  switch (event.kind) {
    case "agent_state":
      if (event.agent) next.agents = upsertAgent(next.agents, normalizeAgent(event.agentId ?? "", event.agent, event.state, event.text));
      break;
    case "background_state":
      next.backgroundProcesses = (event.background ?? []).map(normalizeBackgroundProcess);
      break;
    case "agent_detail":
      if (event.state === "agent_types") next.agentCatalog = (event.agentCatalog ?? []).map(normalizeAgentCatalog);
      if (event.state === "detail") {
        if (event.agentId && event.agentId !== next.selectedAgentId) break;
        next.agentBlocks = mergeAgentDetailBlocks(next.agentBlocks, (event.agentBlocks ?? []).map(normalizeBlock));
      }
      break;
  }
}

/** Keep live thinking/text that arrived after inspect_agent copied a stale snapshot. */
export function mergeAgentDetailBlocks(current: Block[], incoming: Block[]): Block[] {
  if (incoming.length === 0) return current;
  if (current.length === 0) return incoming;
  const incomingIds = new Set(incoming.map((block) => block.id));
  const currentById = new Map(current.map((block) => [block.id, block]));
  const merged = incoming.map((block) => {
    const live = currentById.get(block.id);
    if (!live) return block;
    return (live.content?.length ?? 0) > (block.content?.length ?? 0) ? live : block;
  });
  const extras = current.filter((block) => !incomingIds.has(block.id));
  return extras.length ? [...merged, ...extras] : merged;
}
