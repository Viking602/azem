import { Children, useEffect, useState, type ComponentPropsWithoutRef, type CSSProperties, type ReactNode, type Ref, useId } from "react";
import { useReducedMotion } from "motion/react";

export type ThinkingTabId = "steps" | "reasoning" | "search" | "coding";

export type ThinkingTab = {
  id: ThinkingTabId;
  label: string;
  empty?: boolean;
};

type ThinkingStateProps = {
  active: boolean;
  expanded: boolean;
  label: string;
  /**
   * Identity of the label's meaning. The wording rolls when this changes,
   * so a ticking clock inside the label cannot flip on every frame.
   */
  labelKey?: string;
  /** Elapsed time or another honest suffix. Stays on the bar, never a second card. */
  meta?: ReactNode;
  panelId?: string;
  disabled?: boolean;
  expandable?: boolean;
  className?: string;
  tabs?: ThinkingTab[];
  activeTab?: ThinkingTabId;
  onTabChange?: (id: ThinkingTabId) => void;
  onToggle?: () => void;
  children?: ReactNode;
};

export function LoadingState({ label, className = "" }: { label: string; className?: string }) {
  return <section className={`bui-loading-state ${className}`.trim()} aria-busy="true">
    <span className="bui-loading-grid" aria-hidden="true">
      {Array.from({ length: 9 }, (_, index) => <i key={index} />)}
    </span>
    <span className="bui-loading-label reasoning-label bui-shimmer-label active">
      <span className="reasoning-label-base">{label}</span>
      <span className="reasoning-label-sweep" aria-hidden="true"><span className="reasoning-label-highlight">{label}</span></span>
    </span>
  </section>;
}

export function visibleThinkingTabs(tabs: ThinkingTab[] | undefined) {
  return (tabs ?? []).filter((tab) => !tab.empty);
}

/** Hide the switcher unless two real views exist. Empty tabs stay off the page. */
export function thinkingTablist(tabs: ThinkingTab[] | undefined) {
  const visible = visibleThinkingTabs(tabs);
  return visible.length >= 2 ? visible : [];
}

/** Vertical page-roll when the action meaning changes. Clock ticks stay still. */
function RollingLabel({
  text, textKey, active,
}: {
  text: string;
  textKey?: string;
  active: boolean;
}) {
  const identity = textKey ?? text;
  const reduceMotion = Boolean(useReducedMotion());
  const [shown, setShown] = useState(text);
  const [shownKey, setShownKey] = useState(identity);
  const [outgoing, setOutgoing] = useState<string | null>(null);

  useEffect(() => {
    if (identity === shownKey) {
      if (text !== shown) setShown(text);
      return;
    }
    if (reduceMotion || !shown) {
      setOutgoing(null);
      setShown(text);
      setShownKey(identity);
      return;
    }
    setOutgoing(shown);
    setShown(text);
    setShownKey(identity);
  }, [identity, reduceMotion, shown, shownKey, text]);

  useEffect(() => {
    if (!outgoing) return;
    const timer = window.setTimeout(() => setOutgoing(null), 400);
    return () => window.clearTimeout(timer);
  }, [outgoing, shownKey]);

  const rolling = Boolean(outgoing && outgoing !== shown);
  return <span className={`reasoning-label bui-shimmer-label ${active ? "active" : ""} ${rolling ? "rolling" : ""}`}>
    <span className="reasoning-label-roll">
      {rolling ? <span className="reasoning-label-out" aria-hidden="true">{outgoing}</span> : null}
      <span className="reasoning-label-base">{shown}</span>
    </span>
    {active ? <span className="reasoning-label-sweep" aria-hidden="true"><span className="reasoning-label-highlight">{shown}</span></span> : null}
  </span>;
}

export function ThinkingState({
  active, expanded, label, labelKey, meta, panelId, disabled, expandable, className = "", tabs, activeTab, onTabChange, onToggle, children,
}: ThinkingStateProps) {
  const tablist = thinkingTablist(tabs);
  const hasDetails = expandable || Children.count(children) > 0 || tablist.length > 0;
  // A caller may own the panel itself so the bar can appear without remounting
  // the body it labels; then this bar renders the header only.
  const ownsPanel = Children.count(children) > 0 || tablist.length > 0;
  return <section
    className={`reasoning-trace bui-thinking-state ${active ? "streaming" : "completed"} ${expanded ? "open" : ""} ${className}`.trim()}
    data-testid="thinking-header"
    aria-busy={active || undefined}
  >
    <button
      className="reasoning-summary"
      type="button"
      aria-expanded={expanded}
      aria-controls={hasDetails ? panelId : undefined}
      onClick={onToggle}
      disabled={disabled}
    >
      <span className={`azem-thinking-mark bui-thinking-mark ${active ? "active" : ""}`} aria-hidden="true"><i /><i /></span>
      <RollingLabel text={label} textKey={labelKey} active={active} />
      <span className="bui-thinking-meta" data-empty={meta ? undefined : "true"}>{meta}</span>
      <svg
        className="reasoning-chevron"
        data-reserved={hasDetails ? undefined : "true"}
        width="13"
        height="13"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      ><path d="M6 9l6 6 6-6" /></svg>
    </button>
    {ownsPanel ? <div className="reasoning-body bui-thinking-body" id={panelId} hidden={!expanded}>
      {tablist.length ? <div className="bui-thinking-tabs" role="tablist">
        {tablist.map((tab) => <button
          key={tab.id}
          type="button"
          role="tab"
          className="bui-thinking-tab"
          aria-selected={tab.id === activeTab}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            onTabChange?.(tab.id);
          }}
        >{tab.label}</button>)}
      </div> : null}
      {children}
    </div> : null}
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

export type TaskRowStep = {
  key?: string;
  label: string;
  value?: string;
};

type TaskRowProps = Omit<ComponentPropsWithoutRef<"div">, "title"> & {
  state?: string;
  title?: ReactNode;
  metric?: ReactNode;
  statusLabel?: string;
  index?: number;
  progress?: number;
  expanded?: boolean;
  onToggle?: () => void;
  steps?: TaskRowStep[];
};

export function TaskRow({
  className = "",
  state = "pending",
  title,
  metric,
  statusLabel,
  index,
  progress,
  expanded = false,
  onToggle,
  steps,
  children,
  ...props
}: TaskRowProps) {
  const stepsId = useId();
  const heading = title ?? children;
  const markState = state === "failed" ? "cancelled" : state;
  const hasSteps = (steps?.length ?? 0) > 0;
  const canToggle = hasSteps && typeof onToggle === "function";
  const header = <>
    <TaskStatusMark state={markState} index={index} progress={progress} />
    <span className="bui-task-heading">
      <strong className="bui-task-title">{heading}</strong>
      {metric ? <span className="bui-task-metric">{metric}</span> : null}
    </span>
    {statusLabel ? <span className="bui-task-badge">{statusLabel}</span> : null}
    {canToggle ? <svg className="bui-task-chevron" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg> : null}
  </>;
  return <div
    {...props}
    className={`todo-item bui-task-row ${className}`.trim()}
    data-status={state}
    data-variant="capsules"
    data-expanded={canToggle ? String(expanded) : undefined}
  >
    {canToggle
      ? <button type="button" className="bui-task-header" aria-expanded={expanded} aria-controls={stepsId} onClick={onToggle}>{header}</button>
      : <div className="bui-task-header">{header}</div>}
    {hasSteps ? <div className="bui-task-body" id={stepsId} hidden={!expanded}>
      <i className="bui-task-rail" aria-hidden="true" />
      <ul className="bui-task-steps">
        {(steps ?? []).map((step, stepIndex) => <li key={step.key ?? `${step.label}-${stepIndex}`}>
          <span>{step.label}</span>
          {step.value ? <em>{step.value}</em> : null}
        </li>)}
      </ul>
    </div> : null}
  </div>;
}

function TaskStatusMark({ state, index, progress }: { state: string; index?: number; progress?: number }) {
  if (state === "completed") {
    return <span className="bui-task-mark" data-state="completed" aria-hidden="true">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3.6 8.2 6.7 11.2 12.4 4.8" /></svg>
    </span>;
  }
  if (state === "cancelled") {
    return <span className="bui-task-mark" data-state="cancelled" aria-hidden="true">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4.5 8h7" /></svg>
    </span>;
  }
  const ratio = Math.max(0, Math.min(1, progress ?? 0));
  const radius = 8;
  const circumference = 2 * Math.PI * radius;
  const showRing = state === "in_progress" || ratio > 0;
  return <span className="bui-task-mark" data-state={state || "pending"} aria-hidden="true">
    <svg className="bui-task-ring" viewBox="0 0 22 22">
      <circle className="bui-task-ring-track" cx="11" cy="11" r={radius} />
      {showRing ? <circle
        className="bui-task-ring-value"
        cx="11"
        cy="11"
        r={radius}
        strokeDasharray={`${circumference * ratio} ${circumference}`}
        transform="rotate(-90 11 11)"
      /> : null}
    </svg>
    {index != null ? <em>{index}</em> : null}
  </span>;
}

export function PromptBar({ className = "", variant = "rounded", ...props }: ComponentPropsWithoutRef<"div"> & { variant?: "rounded" | "pill" }) {
  return <div {...props} className={`bui-prompt-bar bui-prompt-bar-${variant} ${className}`.trim()} />;
}

export type ActionIslandLabels = {
  toolbar: string;
  describe: string;
  explain: string;
  improve: string;
  submit: string;
};

export function ActionIsland({
  className = "",
  style,
  instruction,
  onInstructionChange,
  onExplain,
  onImprove,
  onSubmit,
  onKeyDown,
  labels,
  islandRef,
}: {
  className?: string;
  style?: CSSProperties;
  instruction: string;
  onInstructionChange: (value: string) => void;
  onExplain: () => void;
  onImprove: () => void;
  onSubmit: () => void;
  onKeyDown?: ComponentPropsWithoutRef<"div">["onKeyDown"];
  labels: ActionIslandLabels;
  islandRef?: Ref<HTMLDivElement>;
}) {
  const canSubmit = instruction.trim().length > 0;
  return <div
    ref={islandRef}
    className={`bui-action-island ${className}`.trim()}
    role="toolbar"
    tabIndex={-1}
    aria-label={labels.toolbar}
    style={style}
    onKeyDown={onKeyDown}
    onMouseDown={(event) => {
      if (event.target instanceof HTMLInputElement) return;
      event.preventDefault();
    }}
  >
    <input
      className="bui-action-island-input"
      value={instruction}
      placeholder={labels.describe}
      aria-label={labels.describe}
      onChange={(event) => onInstructionChange(event.target.value)}
      onKeyDown={(event) => {
        if (event.key !== "Enter" || event.nativeEvent.isComposing) return;
        if (!canSubmit) return;
        event.preventDefault();
        onSubmit();
      }}
    />
    <span className="bui-action-island-divider" aria-hidden="true" />
    <button type="button" className="bui-action-island-action bui-action-island-explain" onClick={onExplain}>
      <ActionIslandHelpIcon />
      {labels.explain}
    </button>
    <button type="button" className="bui-action-island-action bui-action-island-improve" onClick={onImprove}>
      <ActionIslandSparkleIcon />
      {labels.improve}
    </button>
    <button
      type="button"
      className="bui-action-island-submit"
      aria-label={labels.submit}
      disabled={!canSubmit}
      onClick={() => { if (canSubmit) onSubmit(); }}
    >
      <ActionIslandChevronIcon />
    </button>
  </div>;
}

function ActionIslandHelpIcon() {
  return <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="12" cy="12" r="9" />
    <path d="M9.6 9.4a2.5 2.5 0 1 1 3.7 2.2c-.8.4-1.3 1-1.3 1.9" />
    <path d="M12 17.2v.2" />
  </svg>;
}

function ActionIslandSparkleIcon() {
  return <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3.2 13.7 8.6 19 10.4l-5.3 1.8L12 17.6l-1.7-5.4L5 10.4l5.3-1.8L12 3.2z" />
  </svg>;
}

function ActionIslandChevronIcon() {
  return <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M9 6l6 6-6 6" />
  </svg>;
}
