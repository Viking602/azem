import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Check, ChevronDown, Minus, X } from "lucide-react";
import { tFormat, translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { Snapshot, TodoItem, TodoList, TodoStatus } from "../../types";

export interface ThreadPlanSummary {
  items: TodoItem[];
  completed: number;
  percentage: number;
  current: TodoItem;
  next: TodoItem | null;
}

export function summarizeThreadPlan(todo: TodoList): ThreadPlanSummary | null {
  const items = todo.phases.flatMap((phase) => phase.items);
  if (items.length === 0) return null;
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  let currentIndex = items.findIndex((item) => item.status === "in_progress");
  if (currentIndex < 0) currentIndex = items.findIndex((item) => item.status === "pending");
  if (currentIndex < 0) currentIndex = items.length - 1;
  const current = items[currentIndex]!;
  const next = items.slice(currentIndex + 1).find((item) => item.status === "in_progress" || item.status === "pending") ?? null;
  return {
    items,
    completed,
    percentage: Math.round((completed / items.length) * 100),
    current,
    next,
  };
}

export default function ThreadPlanBar({ hidden = false }: { hidden?: boolean }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const todo = useRuntimeStore((state) => state.todo);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const [expanded, setExpanded] = useState(false);
  const shellRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelId = useId();
  const language = snapshot.language;
  const t = translator(language);
  const summary = todo ? summarizeThreadPlan(todo) : null;

  const close = useCallback((returnFocus = false) => {
    setExpanded(false);
    if (returnFocus) requestAnimationFrame(() => triggerRef.current?.focus());
  }, []);

  useEffect(() => {
    close(false);
  }, [close, currentSessionId, hidden]);

  useEffect(() => {
    if (!todo) close(false);
  }, [close, todo]);

  useEffect(() => {
    if (!expanded) return;
    const outside = (event: PointerEvent) => {
      if (!shellRef.current?.contains(event.target as Node)) close(false);
    };
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === "Escape") close(true);
    };
    const blur = () => close(false);
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", keyboard);
    window.addEventListener("blur", blur);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", keyboard);
      window.removeEventListener("blur", blur);
    };
  }, [close, expanded]);

  if (hidden || !todo || !summary) return null;

  const complete = summary.completed === summary.items.length;
  const currentLabel = complete ? t("todoCompleted") : language === "zh-CN" ? "当前" : "Current";
  const nextLabel = language === "zh-CN" ? "下一步" : "Next";
  const closeLabel = language === "zh-CN" ? "关闭任务计划" : "Close task plan";
  const planLabel = language === "zh-CN"
    ? `${t("todoTitle")} ${summary.completed} / ${summary.items.length}。${currentLabel}：${summary.current.content}${summary.next ? `。${nextLabel}：${summary.next.content}` : ""}`
    : `${t("todoTitle")} ${summary.completed} of ${summary.items.length}. ${currentLabel}: ${summary.current.content}${summary.next ? `. ${nextLabel}: ${summary.next.content}` : ""}`;

  return <div className="thread-plan-shell" ref={shellRef} data-slot="thread-plan">
    <button
      ref={triggerRef}
      type="button"
      className="thread-plan-trigger"
      aria-label={planLabel}
      aria-expanded={expanded}
      aria-controls={panelId}
      onClick={() => setExpanded((value) => !value)}
    >
      <span className="thread-plan-summary">
        <strong>{t("todoTitle")}</strong>
        <span className="thread-plan-count">{summary.completed} / {summary.items.length}</span>
        <span className="thread-plan-meter" aria-hidden="true"><span style={{ width: `${summary.percentage}%` }} /></span>
      </span>
      <span className="thread-plan-current"><small>{currentLabel}</small><span>{summary.current.content}</span></span>
      {summary.next ? <span className="thread-plan-next"><small>{nextLabel}</small><span>{summary.next.content}</span></span> : <span className="thread-plan-next" />}
      <span className="thread-plan-chevron" aria-hidden="true"><ChevronDown size={14} /></span>
    </button>
    {expanded ? <section id={panelId} className="thread-plan-panel" role="region" aria-label={t("todoTitle")}>
      <header className="thread-plan-panel-header">
        <div><h2>{t("todoTitle")}</h2><span>{summary.completed} / {summary.items.length}</span></div>
        {todo.goal?.trim() ? <p>{todo.goal.trim()}</p> : null}
        <button type="button" aria-label={closeLabel} onClick={() => close(true)}><X size={15} /></button>
      </header>
      <div className="thread-plan-progress" role="progressbar" aria-label={tFormat(language, "todoProgress", { done: summary.completed, total: summary.items.length })} aria-valuemin={0} aria-valuemax={summary.items.length} aria-valuenow={summary.completed}>
        <span style={{ width: `${summary.percentage}%` }} />
      </div>
      <div className="thread-plan-scroll">
        {todo.phases.map((phase, phaseIndex) => {
          const phaseKey = phase.id || phase.title || String(phaseIndex);
          return <section className="thread-plan-phase" key={phaseKey} data-state={phaseState(phase.items)}>
            {phase.title ? <h3>{phase.title}</h3> : null}
            <ul>
              {phase.items.map((item) => <li key={item.id || item.content} data-state={item.status} aria-current={item.status === "in_progress" ? "step" : undefined} aria-label={`${item.content}，${statusLabel(item.status, language)}`}>
                <TaskMark state={item.status} />
                <span>{item.content}</span>
                <em>{statusLabel(item.status, language)}</em>
              </li>)}
            </ul>
          </section>;
        })}
      </div>
    </section> : null}
  </div>;
}

function TaskMark({ state }: { state: TodoStatus }) {
  if (state === "completed") return <span className="thread-plan-task-mark" data-state={state} aria-hidden="true"><Check size={11} strokeWidth={2.2} /></span>;
  if (state === "cancelled") return <span className="thread-plan-task-mark" data-state={state} aria-hidden="true"><Minus size={10} strokeWidth={2.2} /></span>;
  return <span className="thread-plan-task-mark" data-state={state} aria-hidden="true" />;
}

function phaseState(items: TodoItem[]): TodoStatus {
  if (items.some((item) => item.status === "in_progress")) return "in_progress";
  if (items.some((item) => item.status === "pending")) return "pending";
  if (items.length > 0 && items.every((item) => item.status === "cancelled")) return "cancelled";
  return "completed";
}

function statusLabel(status: TodoStatus, language: Snapshot["language"]) {
  const t = translator(language);
  if (status === "completed") return t("todoCompleted");
  if (status === "cancelled") return t("cancelled");
  if (status === "in_progress") return t("todoInProgress");
  return t("todoPending");
}
