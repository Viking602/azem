import { translator } from "../i18n";
import { projectSubagentPreview } from "../subagents";
import type { RuntimeEvent } from "../types";
import { appendDelta } from "./blocks";
import type { RuntimeData } from "./state";

/** Text streams: thinking_delta / text_delta phases into main feed or side chat. */
export function reduceTextEvent(next: RuntimeData, event: RuntimeEvent): void {
  switch (event.kind) {
    case "thinking_delta": {
      const thinkingTitle = translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("thinking");
      // Subagent frames carry agentId — stream them into the side chat, not the main feed.
      if (event.agentId) {
        next.agents = projectSubagentPreview(next.agents, event.agentId, event.runId ?? "", "thinking", event.text ?? "");
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = appendDelta(next.agentBlocks, event, "thinking", thinkingTitle);
        }
      } else {
        next.blocks = appendDelta(next.blocks, event, "thinking", thinkingTitle);
        next.activity = "thinking";
      }
      break;
    }
    case "text_delta": {
      // Unphased provider text is the visible answer unless a later tool
      // boundary proves it was commentary. Keeping the natural-stop stream in
      // one assistant block prevents the final body from jumping out of the
      // process rail when the run finishes.
      const commentary = event.textPhase === "commentary";
      const kind = commentary ? "commentary" : "assistant";
      const title = commentary
        ? translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("progressUpdate")
        : "Azem";
      if (event.agentId) {
        next.agents = projectSubagentPreview(next.agents, event.agentId, event.runId ?? "", kind, event.text ?? "");
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = appendDelta(next.agentBlocks, event, kind, title);
        }
      } else {
        next.blocks = appendDelta(next.blocks, event, kind, title);
        next.activity = commentary ? "thinking" : "responding";
      }
      break;
    }
  }
}
