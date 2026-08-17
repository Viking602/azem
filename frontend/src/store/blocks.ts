import type { Block, RuntimeEvent } from "../types";

export function isLiveBlock(block: Block) {
  return ["queued", "awaiting_approval", "reviewing_approval", "streaming", "running", "started", "progress"].includes(block.state || "");
}

export function settleTimedProcessBlock(block: Block, state = "completed", completedAt = Date.now()): Block {
  const startedAt = Number(block.data?.startedAt || 0);
  const data: Record<string, string> = {
    ...(block.data ?? {}),
    completedAt: String(completedAt),
  };
  if (Number.isFinite(startedAt) && startedAt > 0) {
    data.elapsedMs = String(Math.max(0, completedAt - startedAt));
  }
  return { ...block, state, data };
}

export function discardUncommittedAttempt(blocks: Block[], runId?: string): Block[] {
  let boundary = blocks.length;
  while (boundary > 0) {
    const block = blocks[boundary - 1]!;
    if (block.runId !== runId || !["thinking", "commentary", "assistant"].includes(block.kind)) break;
    boundary--;
  }
  return boundary === blocks.length ? blocks : blocks.slice(0, boundary);
}

function settleActiveProcessText(
  blocks: Block[],
  event: RuntimeEvent,
  includeCommentary = true,
  state = "completed",
) {
  const completedAt = Date.now();
  return blocks.map((block) => (block.kind === "thinking" || (includeCommentary && block.kind === "commentary"))
    && block.runId === event.runId
    && block.agentId === event.agentId
    && isLiveBlock(block)
    ? settleTimedProcessBlock(block, state, completedAt)
    : block);
}

export function appendDelta(
  blocks: Block[],
  event: RuntimeEvent,
  kind: "thinking" | "commentary" | "assistant",
  title: string,
): Block[] {
  if (kind === "commentary") blocks = settleActiveProcessText(blocks, event, false);
  if (kind === "assistant") blocks = settleActiveProcessText(blocks, event);
  const previousIndex = blocks.length - 1;
  const previous = blocks[previousIndex];
  const sameStream = previous?.kind === kind
    && previous.runId === event.runId
    && previous.agentId === event.agentId
    && isLiveBlock(previous);
  const chunk = event.text ?? "";
  if (!sameStream) {
    const stream = event.runId || event.agentId || "current";
    return [...blocks, {
      id: `${kind}-${stream}-${event.sequence || blocks.length + 1}`,
      kind,
      runId: event.runId,
      agentId: event.agentId,
      title,
      content: chunk,
      textPhase: event.textPhase,
      state: event.state || "streaming",
      data: { ...(event.data ?? {}), startedAt: event.data?.startedAt || String(Date.now()) },
    }];
  }
  return blocks.map((block, current) => current === previousIndex ? {
    ...block,
    content: kind === "thinking"
      ? joinThinkingContent(block.content ?? "", chunk)
      : `${block.content ?? ""}${chunk}`,
    textPhase: block.textPhase || event.textPhase,
    state: event.state || "streaming",
    title: block.title || title,
    data: event.data ? { ...(block.data ?? {}), ...event.data } : block.data,
  } : block);
}

/**
 * Models often emit discrete thinking blurbs as separate deltas, each wrapped
 * in ** markers. Naïve concatenation yields `**A****B**`; insert paragraph
 * breaks so the timeline can present each blurb as a separate plain-text step.
 */
function joinThinkingContent(existing: string, next: string) {
  if (!existing) return next;
  if (!next) return existing;
  const left = existing.replace(/[ \t]+$/u, "");
  const right = next.replace(/^[ \t]+/u, "");
  if (left.endsWith("**") && right.startsWith("**")) return `${left}\n\n${right}`;
  // New markdown block / list item arriving as a whole segment.
  if (!/\s$/u.test(left) && /^(?:#{1,6}\s|[-*+]\s|\d+\.\s)/u.test(right)) return `${left}\n\n${right}`;
  return existing + next;
}

export function updateTool(blocks: Block[], event: RuntimeEvent): Block[] {
  const id = event.toolCallId || `tool-${event.sequence}`;
  const index = blocks.findIndex((block) => block.toolCallId === id || block.id === id);
  const data = event.data ?? {};
  if (event.kind === "tool_started") {
    blocks = settleActiveProcessText(blocks, event);
    const completedAt = Date.now();
    blocks = blocks.map((block): Block => block.kind === "assistant"
      && block.runId === event.runId
      && block.agentId === event.agentId
      && isLiveBlock(block)
      ? { ...settleTimedProcessBlock(block, "completed", completedAt), kind: "commentary", title: "progress", textPhase: "commentary" }
      : block);
  }
  const text = event.text ?? "";
  const argumentsText = typeof data.arguments === "string" ? data.arguments : "";
  const pendingState = ["queued", "awaiting_approval", "reviewing_approval"].includes(event.state || "")
    ? event.state || ""
    : "";
  const finishedState = event.kind === "tool_update" && event.state === "finished"
    ? (data.status === "stopped" || Boolean(data.reason) || (data.exit_code !== undefined && data.exit_code !== "0") ? "failed" : "completed")
    : "";
  const nextState = event.kind === "tool_finished"
    ? event.state || "completed"
    : pendingState || finishedState || (event.kind === "tool_started" || event.kind === "tool_update" ? "running" : event.state || "");

  if (index < 0) {
    // Seed content from arguments so the timeline can preview path/command while running.
    const content = text
      ? (argumentsText ? `${argumentsText}\n${text}` : text)
      : argumentsText;
    const created: Block = {
      id, kind: "tool", runId: event.runId, agentId: event.agentId, toolCallId: id,
      title: data.name || "",
      content,
      state: nextState || "running",
      data: withToolStart(argumentsText ? { ...data, arguments: argumentsText } : { ...data }),
    };
    return [...blocks, settleFinishedTool(created, nextState)];
  }

  return blocks.map((block, current) => {
    if (current !== index) return block;
    const priorArgs = block.data?.arguments || "";
    const mergedArgs = argumentsText || priorArgs;
    let content = block.content || mergedArgs;
    if (text) {
      if (mergedArgs && (!content || content === mergedArgs)) content = `${mergedArgs}\n${text}`;
      else if (content && !content.endsWith(text)) {
        content = content.endsWith("\n") ? `${content}${text}` : `${content}\n${text}`;
      } else if (!content) content = text;
    } else if (!content && mergedArgs) {
      content = mergedArgs;
    }
    const next: Block = {
      ...block,
      title: data.name || block.title,
      content,
      state: nextState || block.state,
      data: withToolStart({
        ...block.data,
        ...data,
        ...(mergedArgs ? { arguments: mergedArgs } : {}),
      }),
    };
    return settleFinishedTool(next, nextState || block.state || "");
  });
}

function withToolStart(data: Record<string, string>): Record<string, string> {
  return data.startedAt ? data : { ...data, startedAt: String(Date.now()) };
}

function settleFinishedTool(block: Block, state: string): Block {
  if (!["completed", "failed", "cancelled"].includes(state)) return block;
  if (block.data?.elapsedMs) return { ...block, state };
  return settleTimedProcessBlock(block, state);
}

export function stampProcessElapsed(block: Block, elapsedMs: number): Block {
  // Tools keep their own start/finish clock. Copying the run duration onto
  // every tool made five completed rows all read as the step's 1m44s.
  if (!elapsedMs || block.kind !== "thinking") return block;
  if (block.data?.elapsedMs) return block;
  return { ...block, data: { ...block.data, elapsedMs: String(elapsedMs) } };
}
