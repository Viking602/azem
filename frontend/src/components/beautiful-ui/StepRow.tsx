import type { CSSProperties, ReactNode } from "react";
import type { StepEdge, StepMarkState } from "../timeline/stepRail";

/**
 * One row of a process trail: a rail node plus whatever chip renders the work.
 * The rail itself is a per-row CSS segment, so expanded bodies keep it whole.
 */
export function StepRow({
  mark,
  edge,
  delayMs,
  children,
}: {
  mark: StepMarkState;
  edge: StepEdge;
  delayMs?: number;
  children: ReactNode;
}) {
  const style = delayMs === undefined
    ? undefined
    : { "--step-enter-delay": `${delayMs}ms` } as CSSProperties;
  return <div
    className="timeline-step-row"
    role="listitem"
    data-step-state={mark}
    data-step-edge={edge}
    data-step-enter={delayMs === undefined ? undefined : "true"}
    style={style}
  >
    <span className="bui-step-mark" data-step-mark={mark} aria-hidden="true">
      <StepMarkGlyph state={mark} />
    </span>
    <div className="timeline-step-row-body">{children}</div>
  </div>;
}

function StepMarkGlyph({ state }: { state: StepMarkState }) {
  if (state === "running") {
    return <span className="bui-step-mark-glyph bui-step-spinner" />;
  }
  if (state === "pending") {
    return <span className="bui-step-mark-glyph bui-step-dot" data-variant="hollow" />;
  }
  if (state === "note") {
    return <span className="bui-step-mark-glyph bui-step-dot" data-variant="note" />;
  }
  return <span className="bui-step-mark-glyph">
    <svg
      width="11"
      height="11"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.6"
      strokeLinecap="round"
      strokeLinejoin="round"
    >{state === "failed" ? <path d="M6 12h12" /> : <path d="M20 6L9 17l-5-5" />}</svg>
  </span>;
}
