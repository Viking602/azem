import { useEffect, useMemo, useState } from "react";
import type { Language } from "../../i18n";
import type { Block } from "../../types";
import { ReasoningPanel } from "../assistant-ui/Elements";
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

  return <div className="aui-reasoning-stack" data-live={live || reasoningRunning || undefined} data-thinking-row="reasoning">
    <ReasoningPanel
      active={reasoningRunning}
      expanded={open}
      label={thinkingStateLabel(language, reasoningRunning, reasoningRunning ? 0 : thinkingTraceElapsedMs(parts.reasoning))}
      quiet
      panelId={`thinking-reason-${blocks[0]?.id.replace(/[^a-zA-Z0-9_-]/gu, "-") || "group"}`}
      onToggle={() => setOpen((value) => !value)}
    >
      <div className="aui-reasoning-content" data-tab="reasoning">
        <ReasoningSteps blocks={parts.reasoning} />
      </div>
    </ReasoningPanel>
  </div>;
}

export function ReasoningSteps({ blocks }: { blocks: Block[] }) {
  const steps = blocks.flatMap((block) =>
    normalizeThinkingText(block.content || "").split(/\n{2,}/u).map(plainStreamingText).filter(Boolean));
  if (!steps.length) return null;
  return <>{steps.map((step, index) => <p className="reasoning-step" key={`${step.slice(0, 24)}-${index}`}>{step}</p>)}</>;
}

