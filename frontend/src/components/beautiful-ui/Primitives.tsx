import { Children, useEffect, useRef, useState, type ComponentPropsWithoutRef, type CSSProperties, type ReactNode, type Ref, useId } from "react";
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
  /** Codex wait: gray “正在思考”, cadenced sweep, no sparkle or clock. */
  quiet?: boolean;
  /** Quiet live tools: show the actual tool mark before 正在运行. */
  quietMark?: boolean;
  mark?: ReactNode;
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

const SHIMMER_DELAY_MS = 600;
const SHIMMER_PULSE_MS = 1000;
const SHIMMER_INTERVAL_MS = 4000;
const ROLL_MS = 400;

/** Page-roll identity. A new rollId remounts in/out spans so a second
 *  tool→思考 swap restarts CSS animation even if `.rolling` never left. */
function useRollingText(text: string, textKey?: string, enabled = true) {
  const identity = textKey ?? text;
  const reduceMotion = Boolean(useReducedMotion());
  const [shown, setShown] = useState(text);
  const [shownKey, setShownKey] = useState(identity);
  const [outgoing, setOutgoing] = useState<string | null>(null);
  const [rollId, setRollId] = useState(0);

  useEffect(() => {
    if (!enabled) return;
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
    setRollId((value) => value + 1);
  }, [enabled, identity, reduceMotion, shown, shownKey, text]);

  useEffect(() => {
    if (!outgoing) return;
    const timer = window.setTimeout(() => setOutgoing(null), ROLL_MS);
    return () => window.clearTimeout(timer);
  }, [outgoing, rollId]);

  return {
    shown,
    outgoing,
    rolling: Boolean(outgoing && outgoing !== shown),
    rollId,
    reduceMotion,
  };
}

/** ChatGPT Codex thinking-shimmer: one 1s sweep after 600ms, then every 4s. */
export function CadencedShimmer({
  children, className = "", active = true, textKey,
}: {
  children: ReactNode;
  className?: string;
  active?: boolean;
  textKey?: string;
}) {
  const text = typeof children === "string" ? children : "";
  const { shown, outgoing, rolling, rollId, reduceMotion } = useRollingText(text, textKey, Boolean(text));
  const ref = useRef<HTMLSpanElement>(null);
  const enabled = active && !reduceMotion;

  useEffect(() => {
    const node = ref.current;
    if (!enabled || !node) return;
    let pulseTimer = 0;
    let interval = 0;
    const stopPulse = () => {
      if (pulseTimer) window.clearTimeout(pulseTimer);
      pulseTimer = 0;
    };
    const pulse = () => {
      stopPulse();
      node.classList.remove("bui-cadenced-shimmer-active");
      void node.offsetWidth;
      node.classList.add("bui-cadenced-shimmer-active");
      pulseTimer = window.setTimeout(() => {
        node.classList.remove("bui-cadenced-shimmer-active");
        pulseTimer = 0;
      }, SHIMMER_PULSE_MS);
    };
    const start = window.setTimeout(() => {
      pulse();
      interval = window.setInterval(pulse, SHIMMER_INTERVAL_MS);
    }, SHIMMER_DELAY_MS);
    return () => {
      stopPulse();
      window.clearTimeout(start);
      if (interval) window.clearInterval(interval);
      node.classList.remove("bui-cadenced-shimmer-active");
    };
  }, [enabled]);

  const display = text ? shown : children;
  return <span ref={enabled ? ref : undefined} className={`bui-cadenced-shimmer ${className} ${rolling ? "rolling" : ""}`.trim()}>
    <span className="reasoning-label-roll">
      {rolling ? <span key={`out-${rollId}`} className="reasoning-label-out" aria-hidden="true">{outgoing}</span> : null}
      <span key={`in-${rollId}`} className="bui-cadenced-shimmer-text">{display}</span>
    </span>
    {active ? <span aria-hidden="true" className="bui-cadenced-shimmer-sweep">
      <span className="bui-cadenced-shimmer-highlight">{display}</span>
    </span> : null}
  </span>;
}


/** Vertical page-roll when the action meaning changes. Clock ticks stay still. */
export function RollingLabel({
  text, textKey, active,
}: {
  text: string;
  textKey?: string;
  active: boolean;
}) {
  const { shown, outgoing, rolling, rollId } = useRollingText(text, textKey);
  return <span className={`reasoning-label bui-shimmer-label ${active ? "active" : ""} ${rolling ? "rolling" : ""}`}>
    <span className="reasoning-label-roll">
      {rolling ? <span key={`out-${rollId}`} className="reasoning-label-out" aria-hidden="true">{outgoing}</span> : null}
      <span key={`in-${rollId}`} className="reasoning-label-base">{shown}</span>
    </span>
    {active ? <span className="reasoning-label-sweep" aria-hidden="true"><span className="reasoning-label-highlight">{shown}</span></span> : null}
  </span>;
}

export function ThinkingState({
  active, expanded, label, labelKey, meta, panelId, disabled, expandable, quiet = false, quietMark = false, mark, className = "", tabs, activeTab, onTabChange, onToggle, children,
}: ThinkingStateProps) {
  const tablist = thinkingTablist(tabs);
  const hasDetails = expandable || Children.count(children) > 0 || tablist.length > 0;
  const ownsPanel = Children.count(children) > 0 || tablist.length > 0;
  const showChrome = !quiet;
  const waitLabel = <CadencedShimmer key="thinking-wait" className="bui-thinking-wait" active={active} textKey={labelKey}>{label}</CadencedShimmer>;
  return <section
    className={`reasoning-trace bui-thinking-state ${active ? "streaming" : "completed"} ${expanded ? "open" : ""} ${quiet ? "quiet" : ""} ${className}`.trim()}
    data-testid="thinking-header"
    aria-busy={active || undefined}
  >
    {quiet && !hasDetails && !quietMark ? waitLabel : <button
      className={`reasoning-summary ${quiet ? "quiet" : ""}`}
      type="button"
      aria-expanded={hasDetails ? expanded : undefined}
      aria-controls={hasDetails ? panelId : undefined}
      onClick={hasDetails ? onToggle : undefined}
      disabled={disabled || !hasDetails}
    >
      {quiet ? <span className="bui-thinking-lead" data-empty={quietMark ? undefined : "true"} aria-hidden="true">
        {quietMark ? mark ?? <span className="bui-running-mark" /> : null}
      </span> : null}
      {showChrome ? <span className={`azem-thinking-mark bui-thinking-mark ${active ? "active" : ""}`} aria-hidden="true"><i /><i /></span> : null}
      {quiet ? waitLabel : <RollingLabel text={label} textKey={labelKey} active={active} />}
      {showChrome ? <span className="bui-thinking-meta" data-empty={meta ? undefined : "true"}>{meta}</span> : null}
      {!quiet && (hasDetails || showChrome) ? <svg
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
      ><path d="M6 9l6 6 6-6" /></svg> : null}
    </button>}
    {ownsPanel ? <div className="reasoning-body-clip" data-open={expanded ? "true" : "false"}>
      <div className="reasoning-body bui-thinking-body" id={panelId} inert={expanded ? undefined : true} aria-hidden={expanded ? undefined : true}>
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
      </div>
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
