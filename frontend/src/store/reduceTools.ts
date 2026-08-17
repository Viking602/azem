import { translator } from "../i18n";
import type { RuntimeEvent } from "../types";
import { updateTool } from "./blocks";
import type { RuntimeData } from "./state";

/** Tool lifecycle, diffs, approvals, user input, and plan review. */
export function reduceToolEvent(next: RuntimeData, event: RuntimeEvent): void {
  const data = event.data ?? {};
  switch (event.kind) {
    case "tool_started":
    case "tool_update":
    case "tool_finished":
      if (event.agentId) {
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = updateTool(next.agentBlocks, event);
        }
      } else {
        next.blocks = updateTool(next.blocks, event);
        next.activity = event.kind === "tool_finished" ? "waiting_model" : "tool";
      }
      break;
    case "diff_ready":
      if (event.agentId) {
        if (event.agentId === next.selectedAgentId) {
          next.agentBlocks = [...next.agentBlocks, {
            id: event.toolCallId || `diff-${event.sequence}`,
            kind: "diff",
            runId: event.runId,
            agentId: event.agentId,
            toolCallId: event.toolCallId,
            title: data.path || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("change"),
            content: event.text ?? "",
            state: event.state || "ready",
            data,
          }];
        }
      } else {
        next.blocks = [...next.blocks, {
          id: event.toolCallId || `diff-${event.sequence}`,
          kind: "diff",
          runId: event.runId,
          toolCallId: event.toolCallId,
          title: data.path || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("change"),
          content: event.text ?? "",
          state: event.state || "ready",
          data,
        }];
      }
      break;
    case "approval_requested":
      if (event.state === "reviewing") {
        if (event.toolCallId) {
          next.blocks = next.blocks.map((block) => block.kind === "tool" && (block.toolCallId === event.toolCallId || block.id === event.toolCallId)
            ? { ...block, state: "reviewing_approval" }
            : block);
        }
        break;
      }
      next.blocks = [...next.blocks, {
        id: event.approvalId || `approval-${event.sequence}`,
        kind: "approval",
        runId: event.runId,
        toolCallId: event.toolCallId,
        approvalId: event.approvalId,
        title: data.action || data.name || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("needApproval"),
        content: event.text || data.reason || translator(next.snapshot?.language === "en" ? "en" : "zh-CN")("approvalConfirm"),
        state: "pending",
        data,
      }];
      next.activity = "approval";
      break;
    case "approval_resolved":
      if (event.state?.startsWith("auto_")) next.blocks = next.blocks.filter((block) => block.approvalId !== event.approvalId);
      else next.blocks = next.blocks.map((block) => block.approvalId === event.approvalId ? { ...block, state: event.state || data.decision || "resolved" } : block);
      next.activity = "waiting_model";
      break;
    case "user_input_requested":
      next.blocks = [...next.blocks, {
        id: event.userInputId || `question-${event.sequence}`,
        kind: "question",
        runId: event.runId,
        toolCallId: event.toolCallId,
        userInputId: event.userInputId,
        title: data.title || (next.snapshot?.language === "en" ? "Need your input" : "需要你的选择"),
        state: event.state || "pending",
        data,
      }];
      next.activity = "input";
      break;
    case "user_input_resolved":
      next.blocks = next.blocks.map((block) => block.userInputId === event.userInputId
        ? { ...block, state: event.state || "answered", data: { ...(block.data ?? {}), ...data } }
        : block);
      next.activity = "waiting_model";
      break;
    case "plan_proposed":
      next.blocks = next.blocks.map((block) => block.kind === "plan" && block.state === "proposed"
        ? { ...block, state: "superseded" }
        : block);
      next.blocks = [...next.blocks, {
        id: event.planId || `plan-${event.sequence}`,
        kind: "plan",
        runId: event.runId,
        toolCallId: event.toolCallId,
        planId: event.planId,
        title: data.title || (next.snapshot?.language === "en" ? "Implementation plan" : "实施计划"),
        content: event.text || "",
        state: event.state || "proposed",
        data,
      }];
      next.planMode = true;
      next.activity = "review";
      break;
    case "plan_resolved":
      next.blocks = next.blocks.map((block) => block.planId === event.planId
        ? { ...block, state: event.state === "executing" ? "approved" : (event.state || "approved") }
        : block);
      next.planMode = false;
      break;
  }
}
