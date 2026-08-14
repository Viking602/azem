import type { RuntimeEvent } from "../types";
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
        next.selectedAgentId = event.agentId ?? next.selectedAgentId;
        next.agentBlocks = (event.agentBlocks ?? []).map(normalizeBlock);
      }
      break;
  }
}
