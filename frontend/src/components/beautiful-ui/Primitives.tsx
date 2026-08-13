import { Children, type ComponentPropsWithoutRef, type ReactNode } from "react";

type ThinkingStateProps = {
  active: boolean;
  expanded: boolean;
  label: string;
  panelId?: string;
  disabled?: boolean;
  onToggle?: () => void;
  children?: ReactNode;
};

export function ThinkingState({ active, expanded, label, panelId, disabled, onToggle, children }: ThinkingStateProps) {
  const hasDetails = Children.count(children) > 0;
  return <section className={`reasoning-trace bui-thinking-state ${active ? "streaming" : "completed"} ${expanded ? "open" : ""}`} aria-busy={active || undefined}>
    <button
      className="reasoning-summary"
      type="button"
      aria-expanded={expanded}
      aria-controls={hasDetails ? panelId : undefined}
      onClick={onToggle}
      disabled={disabled}
    >
      <span className={`azem-thinking-mark bui-thinking-mark ${active ? "active" : ""}`} aria-hidden="true"><i /><i /></span>
      <span className={`reasoning-label bui-shimmer-label ${active ? "active" : ""}`}>
        <span className="reasoning-label-base">{label}</span>
        {active ? <span className="reasoning-label-sweep" aria-hidden="true"><span className="reasoning-label-highlight">{label}</span></span> : null}
      </span>
      {hasDetails ? <svg className="reasoning-chevron" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg> : null}
    </button>
    {hasDetails ? <div className="reasoning-body bui-thinking-body" id={panelId} hidden={!expanded}>{children}</div> : null}
  </section>;
}

export function StreamingText({ children, active = true }: { children: ReactNode; active?: boolean }) {
  return <div className={`streaming-text bui-streaming-text ${active ? "active" : ""}`}>{children}</div>;
}

type StatefulElementProps<T extends "article" | "details" | "div"> = ComponentPropsWithoutRef<T> & {
  state?: string;
};

export function ApprovalCard({ className = "", state, ...props }: StatefulElementProps<"article">) {
  return <article {...props} className={`approval-block bui-approval-card ${className}`.trim()} data-state={state} />;
}

export function ToolRow({ className = "", state, ...props }: StatefulElementProps<"details">) {
  return <details {...props} className={`bui-tool-row ${className}`.trim()} data-state={state} />;
}

export function TaskRow({ className = "", state, ...props }: StatefulElementProps<"div">) {
  return <div {...props} className={`todo-item bui-task-row ${className}`.trim()} data-status={state} />;
}
