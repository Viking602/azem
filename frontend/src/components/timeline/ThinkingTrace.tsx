import { useEffect, useMemo, useState } from "react";
import type { Language } from "../../i18n";
import type { Block } from "../../types";
import { ThinkingState } from "../beautiful-ui/Primitives";
import { isActiveProcessBlock, thinkingStateLabel, thinkingTraceElapsedMs } from "../toolTimeline";
import { normalizeThinkingText, plainStreamingText } from "./streaming";
import { collectThinkingTrace } from "./thinkingTabs";

export function ThinkingTrace({
  blocks, language, defaultExpanded, live = false,
}: {
  blocks: Block[];
  language: Language;
  defaultExpanded?: boolean;
  live?: boolean;
}) {
  const parts = useMemo(() => collectThinkingTrace(blocks), [blocks]);
  const hasReasoningText = parts.reasoning.some((block) =>
    normalizeThinkingText(block.content || "").split(/\n{2,}/u).map(plainStreamingText).some(Boolean));
  const reasoningRunning = parts.reasoning.some((block) => isActiveProcessBlock(block) && Boolean(block.content?.trim()));
  const forceOpen = Boolean(defaultExpanded);
  const [open, setOpen] = useState(forceOpen);
  useEffect(() => {
    if (forceOpen) setOpen(true);
  }, [forceOpen]);

  if (!hasReasoningText) return null;

  return <div className="bui-thinking-stack" data-live={live || reasoningRunning || undefined} data-thinking-row="reasoning">
    <ThinkingState
      active={reasoningRunning}
      expanded={open}
      label={thinkingStateLabel(language, reasoningRunning, reasoningRunning ? 0 : thinkingTraceElapsedMs(parts.reasoning))}
      quiet
      panelId={`thinking-reason-${blocks[0]?.id.replace(/[^a-zA-Z0-9_-]/gu, "-") || "group"}`}
      onToggle={() => setOpen((value) => !value)}
    >
      <div className="bui-thinking-panel" data-tab="reasoning">
        <ReasoningPanel blocks={parts.reasoning} />
      </div>
    </ThinkingState>
  </div>;
}

export function ReasoningPanel({ blocks }: { blocks: Block[] }) {
  const steps = blocks.flatMap((block) =>
    normalizeThinkingText(block.content || "").split(/\n{2,}/u).map(plainStreamingText).filter(Boolean));
  if (!steps.length) return null;
  return <>{steps.map((step, index) => <p className="reasoning-step" key={`${step.slice(0, 24)}-${index}`}>{step}</p>)}</>;
}

