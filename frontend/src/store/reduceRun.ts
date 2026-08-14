import { translator, type MessageKey } from "../i18n";
import type { RuntimeEvent } from "../types";
import { discardUncommittedAttempt, isLiveBlock, settleTimedProcessBlock, stampProcessElapsed } from "./blocks";
import type { RuntimeData } from "./state";

// Stable provider error taxonomy (internal/provider/errcode) to display title.
// Unknown/cancelled codes keep the generic runFailed title.
const PROVIDER_ERROR_TITLES: Partial<Record<string, MessageKey>> = {
  auth: "errorAuth",
  quota: "errorQuota",
  rate_limit: "errorRateLimit",
  context_overflow: "errorContextOverflow",
  empty_response: "errorEmptyResponse",
  invalid_request: "errorInvalidRequest",
  server: "errorServer",
  transport: "errorTransport",
};

/** Run lifecycle: run_started / provider_retry / run_finished / run_cancelled / run_failed. */
export function reduceRunEvent(next: RuntimeData, event: RuntimeEvent): void {
  switch (event.kind) {
    case "run_started":
      next.running = true;
      next.runId = event.runId ?? next.runId;
      next.runStartedAt = Date.now();
      next.activity = "waiting_model";
      break;
    case "provider_retry":
      if (event.state === "restarted") {
        if (event.agentId) {
          if (event.agentId === next.selectedAgentId) next.agentBlocks = discardUncommittedAttempt(next.agentBlocks, event.runId);
          next.agents = next.agents.map((agent) => agent.id === event.agentId
            ? { ...agent, preview: "", previewKind: "", previewRunId: "" }
            : agent);
        } else {
          next.blocks = discardUncommittedAttempt(next.blocks, event.runId);
        }
        next.activity = "waiting_model";
      }
      break;
    case "run_finished":
    case "run_cancelled":
    case "run_failed": {
      const terminalState = event.kind === "run_finished" ? "completed" : event.kind === "run_cancelled" ? "cancelled" : "failed";
      const terminalRunId = event.runId || next.runId;
      const completedAt = Date.now();
      const elapsedMs = next.runStartedAt ? Math.max(0, completedAt - next.runStartedAt) : 0;
      next.running = false;
      next.activity = terminalState;
      if (terminalRunId) {
        next.blocks = next.blocks.map((block) => {
          if (block.runId !== terminalRunId) return block;
          const stamped = stampProcessElapsed(block, elapsedMs);
          // Settle streaming text, reasoning, and tools still active when the run ends.
          if (isLiveBlock(stamped)) {
            if (stamped.kind === "thinking" || stamped.kind === "commentary") {
              return settleTimedProcessBlock(stamped, terminalState, completedAt);
            }
            return { ...stamped, state: stamped.kind === "tool" ? (terminalState === "cancelled" ? "cancelled" : "failed") : terminalState };
          }
          return stamped;
        });
        if (next.selectedAgentId) {
          next.agentBlocks = next.agentBlocks.map((block) => {
            if (!isLiveBlock(block)) return block;
            return { ...block, state: block.kind === "tool" ? (terminalState === "cancelled" ? "cancelled" : "failed") : terminalState };
          });
        }
        if (event.kind === "run_cancelled" && !next.blocks.some((block) => block.kind === "status" && block.runId === terminalRunId)) {
          next.blocks = [...next.blocks, {
            id: `status-cancelled-${terminalRunId}-${event.sequence || next.blocks.length}`,
            kind: "status",
            runId: terminalRunId,
            title: "run_cancelled",
            state: "cancelled",
            data: { elapsedMs: String(elapsedMs) },
          }];
        }
      }
      // Cross-session cancellation pause state is projected before session filtering.
      next.runId = "";
      if (event.kind === "run_failed") {
        next.error = "";
        const language = next.snapshot?.language === "en" ? "en" : "zh-CN";
        const failedText = event.text ?? "";
        const truncated = /token limit|output reached|max_turns|max_output|truncated/i.test(failedText);
        const errorCode = event.data?.errorCode ?? "";
        const codeTitleKey = PROVIDER_ERROR_TITLES[errorCode];
        next.blocks = [...next.blocks, {
          id: `error-${event.sequence}`,
          kind: "error",
          title: translator(language)(codeTitleKey ?? (truncated ? "outputTruncated" : "runFailed")),
          content: event.text,
          state: "failed",
          data: errorCode ? { errorCode } : undefined,
        }];
      }
      break;
    }
  }
}
