import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { createPortal } from "react-dom";
import { Check, Ellipsis, ExternalLink, FileImage, Globe, Link2, ListTodo, LoaderCircle, Minus, Plus, X } from "lucide-react";
import { attachmentDataURL, openExternalURL } from "../../bridge";
import { tFormat, translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { Attachment, SessionRecap, Snapshot, TodoItem, TodoList, TodoStatus } from "../../types";
import { collectConversationSources, type ConversationSource } from "../conversationSources";
import {
  clampThreadReferencePanelRect,
  initialThreadReferencePanelRect,
  isThreadReferencePanelDragGesture,
  moveThreadReferencePanelRect,
  resizeThreadReferencePanelRect,
  THREAD_REFERENCE_PANEL_DEFAULT_SIZE,
  THREAD_REFERENCE_PANEL_MARGIN_PX,
  THREAD_REFERENCE_RESIZE_EDGES,
  threadReferenceResizeCursor,
  type ThreadReferencePanelHostSize,
  type ThreadReferencePanelRect,
  type ThreadReferenceResizeEdge,
} from "./threadReferencePanelGeometry";

export interface ThreadPlanSummary {
  items: TodoItem[];
  completed: number;
  percentage: number;
}

export function summarizeThreadPlan(todo: TodoList): ThreadPlanSummary | null {
  const items = todo.phases.flatMap((phase) => phase.items);
  if (items.length === 0) return null;
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  return { items, completed, percentage: Math.round((completed / items.length) * 100) };
}

export function ThreadPlanControl() {
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
  }, [close, currentSessionId]);

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

  if (!todo || !summary) return null;

  const planLabel = `${t("todoTitle")} ${summary.completed} / ${summary.items.length}`;
  const shortPlanLabel = language === "zh-CN" ? "计划" : "Plan";
  const complete = summary.completed === summary.items.length;
  return <div className="thread-plan-control" ref={shellRef} data-slot="thread-plan-control">
    <button ref={triggerRef} type="button" className="thread-plan-control-trigger" aria-label={planLabel} aria-expanded={expanded} aria-controls={panelId} onClick={() => setExpanded((value) => !value)}>
      <ListTodo size={14} aria-hidden="true" /><strong>{shortPlanLabel}</strong><em>{summary.completed} / {summary.items.length}</em>
    </button>
    {expanded ? <section id={panelId} className="thread-support-panel thread-plan-panel" role="region" aria-label={t("todoTitle")}>
      <header className="thread-support-panel-header">
        <div><h2>{t("todoTitle")}</h2><span>{summary.completed} / {summary.items.length}</span></div>
        <p>{complete ? (language === "zh-CN" ? "当前计划已经完成。" : "The current plan is complete.") : (language === "zh-CN" ? "完整阶段与任务状态。" : "Complete phases and task states.")}</p>
        <button type="button" aria-label={language === "zh-CN" ? "关闭任务计划" : "Close task plan"} onClick={() => close(true)}><X size={15} /></button>
      </header>
      <PlanPanel todo={todo} summary={summary} language={language} />
    </section> : null}
  </div>;
}

const DEFAULT_THREAD_REFERENCE_PANEL_RECT: ThreadReferencePanelRect = {
  left: THREAD_REFERENCE_PANEL_MARGIN_PX,
  top: THREAD_REFERENCE_PANEL_MARGIN_PX,
  ...THREAD_REFERENCE_PANEL_DEFAULT_SIZE,
};

function threadReferencePanelHostSize(host: HTMLElement): ThreadReferencePanelHostSize {
  const bounds = host.getBoundingClientRect();
  return {
    width: host.clientWidth || bounds.width,
    height: host.clientHeight || bounds.height,
  };
}

function sameThreadReferencePanelRect(left: ThreadReferencePanelRect, right: ThreadReferencePanelRect): boolean {
  return left.left === right.left && left.top === right.top && left.width === right.width && left.height === right.height;
}

export function ThreadReferenceCard() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const recap = useRuntimeStore((state) => state.recap);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setError = useRuntimeStore((state) => state.setError);
  const [tab, setTab] = useState<"recap" | "sources">("recap");
  const [preview, setPreview] = useState<ConversationSource | null>(null);
  const [panelRect, setPanelRect] = useState<ThreadReferencePanelRect>(DEFAULT_THREAD_REFERENCE_PANEL_RECT);
  const hostRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLElement>(null);
  const panelRectRef = useRef<ThreadReferencePanelRect>(DEFAULT_THREAD_REFERENCE_PANEL_RECT);
  const activeInteractionCleanupRef = useRef<(() => void) | null>(null);
  const interactingRef = useRef(false);
  const hasMeasuredHostRef = useRef(false);
  const panelId = useId();
  const language = snapshot.language;
  const sources = useMemo(() => collectConversationSources(blocks, language), [blocks, language]);
  const recapLabel = language === "zh-CN" ? "回顾" : "Recap";
  const sourcesLabel = language === "zh-CN" ? "来源" : "Sources";
  const moveLabel = language === "zh-CN" ? "移动回顾和来源面板" : "Move recap and sources panel";
  const moveInstructions = language === "zh-CN"
    ? "拖动移动；方向键微调；Option 加方向键缩放；Home 复位"
    : "Drag to move; use arrow keys to nudge; Option plus arrow keys to resize; Home resets";

  const applyPanelRect = useCallback((next: ThreadReferencePanelRect, host: HTMLElement) => {
    const clamped = clampThreadReferencePanelRect(next, threadReferencePanelHostSize(host));
    panelRectRef.current = clamped;
    const panel = panelRef.current;
    if (panel) {
      panel.style.left = `${clamped.left}px`;
      panel.style.top = `${clamped.top}px`;
      panel.style.width = `${clamped.width}px`;
      panel.style.height = `${clamped.height}px`;
    }
    return clamped;
  }, []);

  const commitPanelRect = useCallback((next: ThreadReferencePanelRect, host: HTMLElement) => {
    setPanelRect(applyPanelRect(next, host));
  }, [applyPanelRect]);

  useLayoutEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    activeInteractionCleanupRef.current?.();
    activeInteractionCleanupRef.current = null;
    hasMeasuredHostRef.current = false;

    const measure = () => {
      if (interactingRef.current) return;
      const size = threadReferencePanelHostSize(host);
      if (size.width <= 0 || size.height <= 0) return;
      if (!hasMeasuredHostRef.current) {
        hasMeasuredHostRef.current = true;
        commitPanelRect(initialThreadReferencePanelRect(size), host);
        return;
      }
      const clamped = clampThreadReferencePanelRect(panelRectRef.current, size);
      if (!sameThreadReferencePanelRect(clamped, panelRectRef.current)) commitPanelRect(clamped, host);
    };

    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(host);
    return () => observer.disconnect();
  }, [commitPanelRect, currentSessionId]);

  useEffect(() => {
    setTab("recap");
    setPreview(null);
  }, [currentSessionId]);

  useEffect(() => () => {
    activeInteractionCleanupRef.current?.();
    activeInteractionCleanupRef.current = null;
  }, []);

  const startPointerInteraction = useCallback((
    event: ReactPointerEvent<HTMLElement>,
    cursor: string,
    waitForDragThreshold: boolean,
    nextRect: (
      start: ThreadReferencePanelRect,
      delta: { x: number; y: number },
      host: ThreadReferencePanelHostSize,
    ) => ThreadReferencePanelRect,
  ) => {
    const host = hostRef.current;
    if (!host || event.button !== 0 || (event.pointerType === "touch" && !event.isPrimary)) return;
    event.preventDefault();
    event.stopPropagation();
    activeInteractionCleanupRef.current?.();

    const startClientX = event.clientX;
    const startClientY = event.clientY;
    const startRect = panelRectRef.current;
    const overlay = document.createElement("div");
    overlay.className = "thread-reference-pointer-overlay";
    overlay.style.cursor = cursor;
    document.body.append(overlay);
    const previousBodyCursor = document.body.style.cursor;
    const previousBodyUserSelect = document.body.style.userSelect;
    let moved = !waitForDragThreshold;
    let finished = false;
    interactingRef.current = moved;

    const finish = () => {
      if (finished) return;
      finished = true;
      window.removeEventListener("pointermove", onPointerMove, true);
      window.removeEventListener("pointerup", finish, true);
      window.removeEventListener("pointercancel", finish, true);
      window.removeEventListener("blur", finish);
      overlay.remove();
      document.body.style.cursor = previousBodyCursor;
      document.body.style.userSelect = previousBodyUserSelect;
      interactingRef.current = false;
      if (activeInteractionCleanupRef.current === finish) activeInteractionCleanupRef.current = null;
      if (moved) commitPanelRect(panelRectRef.current, host);
    };

    const onPointerMove = (moveEvent: PointerEvent) => {
      const delta = { x: moveEvent.clientX - startClientX, y: moveEvent.clientY - startClientY };
      if (!moved) {
        if (!isThreadReferencePanelDragGesture(delta)) return;
        moved = true;
        interactingRef.current = true;
      }
      document.body.style.cursor = cursor;
      document.body.style.userSelect = "none";
      applyPanelRect(nextRect(startRect, delta, threadReferencePanelHostSize(host)), host);
    };

    if (moved) {
      document.body.style.cursor = cursor;
      document.body.style.userSelect = "none";
    }
    window.addEventListener("pointermove", onPointerMove, true);
    window.addEventListener("pointerup", finish, true);
    window.addEventListener("pointercancel", finish, true);
    window.addEventListener("blur", finish);
    activeInteractionCleanupRef.current = finish;
  }, [applyPanelRect, commitPanelRect]);

  const startDrag = (event: ReactPointerEvent<HTMLButtonElement>) => {
    startPointerInteraction(event, "grabbing", true, (start, delta, host) => moveThreadReferencePanelRect(start, delta, host));
  };

  const startResize = (event: ReactPointerEvent<HTMLSpanElement>, edge: ThreadReferenceResizeEdge) => {
    startPointerInteraction(event, threadReferenceResizeCursor(edge), false, (start, delta, host) => resizeThreadReferencePanelRect(start, {
      edge,
      deltaX: delta.x,
      deltaY: delta.y,
    }, host));
  };

  const moveWithKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const host = hostRef.current;
    if (!host) return;
    if (event.key === "Home") {
      event.preventDefault();
      commitPanelRect(initialThreadReferencePanelRect(threadReferencePanelHostSize(host)), host);
      return;
    }
    const step = event.shiftKey ? 30 : 10;
    const delta = {
      ArrowLeft: { x: -step, y: 0 },
      ArrowRight: { x: step, y: 0 },
      ArrowUp: { x: 0, y: -step },
      ArrowDown: { x: 0, y: step },
    }[event.key];
    if (!delta) return;
    event.preventDefault();
    const size = threadReferencePanelHostSize(host);
    const next = event.altKey
      ? resizeThreadReferencePanelRect(panelRectRef.current, {
        edge: delta.x === 0 ? "s" : "e",
        deltaX: delta.x,
        deltaY: delta.y,
      }, size)
      : moveThreadReferencePanelRect(panelRectRef.current, delta, size);
    commitPanelRect(next, host);
  };

  return <div ref={hostRef} className="thread-reference-host" data-floating-reference-host="true">
    <aside
      ref={panelRef}
      className="thread-reference-card"
      data-floating-reference-panel="true"
      role="region"
      aria-label={language === "zh-CN" ? "回顾和来源" : "Recap and sources"}
      style={{ left: panelRect.left, top: panelRect.top, width: panelRect.width, height: panelRect.height }}
    >
      <div className="thread-reference-card-content">
        <div className="thread-reference-tabs" role="tablist" aria-label={language === "zh-CN" ? "参考信息" : "Reference information"}>
          <button type="button" role="tab" aria-selected={tab === "recap"} aria-controls={panelId} onClick={() => setTab("recap")}><span>{recapLabel}</span><em>{recap ? `r${recap.revision}` : "—"}</em></button>
          <button type="button" role="tab" aria-selected={tab === "sources"} aria-controls={panelId} onClick={() => setTab("sources")}><span>{sourcesLabel}</span><em>{sources.length}</em></button>
        </div>
        <div id={panelId} className="thread-reference-body" role="tabpanel">
          {tab === "recap" ? <RecapPanel recap={recap} language={language} /> : <SourcesPanel sources={sources} language={language} openSource={(source) => {
            if (source.kind === "image") {
              setPreview(source);
              return;
            }
            if (source.href) void openExternalURL(source.href).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
          }} />}
        </div>
      </div>
      <div className="thread-reference-controls">
        <button
          type="button"
          className="thread-reference-drag"
          aria-label={moveLabel}
          aria-keyshortcuts="ArrowLeft ArrowRight ArrowUp ArrowDown Alt+ArrowLeft Alt+ArrowRight Alt+ArrowUp Alt+ArrowDown Home"
          title={moveInstructions}
          onPointerDown={startDrag}
          onKeyDown={moveWithKeyboard}
        >
          <Ellipsis size={14} aria-hidden="true" />
        </button>
      </div>
      {THREAD_REFERENCE_RESIZE_EDGES.map((edge) => <span
        key={edge}
        className="thread-reference-resize-handle"
        data-edge={edge}
        data-thread-reference-resize-edge={edge}
        aria-hidden="true"
        onPointerDown={(event) => startResize(event, edge)}
      />)}
    </aside>
    {preview?.attachment ? <SourceImageLightbox source={preview} sessionId={currentSessionId} language={language} onClose={() => setPreview(null)} /> : null}
  </div>;
}

function PlanPanel({ todo, summary, language }: { todo: TodoList; summary: ThreadPlanSummary; language: Snapshot["language"] }) {
  return <>
    <div className="thread-support-progress" role="progressbar" aria-label={tFormat(language, "todoProgress", { done: summary.completed, total: summary.items.length })} aria-valuemin={0} aria-valuemax={summary.items.length} aria-valuenow={summary.completed}>
      <span style={{ width: `${summary.percentage}%` }} />
    </div>
    <div className="thread-support-scroll">
      {todo.phases.map((phase, phaseIndex) => {
        const phaseKey = phase.id || phase.title || String(phaseIndex);
        return <section className="thread-support-phase" key={phaseKey}>
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
  </>;
}

function RecapPanel({ recap, language }: { recap: SessionRecap | null; language: Snapshot["language"] }) {
  if (!recap) return <p className="thread-support-empty">{language === "zh-CN" ? "成功完成一个回合后会生成会话回顾。" : "A recap appears after a turn completes successfully."}</p>;
  return <div className="thread-support-recap">
    {recap.summary ? <p>{recap.summary}</p> : null}
    {recap.goal ? <div><span>{language === "zh-CN" ? "当前目标" : "Current goal"}</span><p>{recap.goal}</p></div> : null}
    {recap.openItems ? <div><span>{language === "zh-CN" ? "未完成事项" : "Open items"}</span><p className="thread-support-open-items">{recap.openItems}</p></div> : null}
  </div>;
}

function SourcesPanel({ sources, language, openSource }: { sources: ConversationSource[]; language: Snapshot["language"]; openSource: (source: ConversationSource) => void }) {
  const addLabel = language === "zh-CN" ? "添加图片" : "Add image";
  return <div className="thread-support-sources">
    <button type="button" className="thread-support-source-add" onClick={() => document.querySelector<HTMLInputElement>(".attach-button input")?.click()}><Plus size={13} /><span>{addLabel}</span></button>
    {sources.length > 0 ? sources.map((source) => {
      const Icon = source.kind === "image" ? FileImage : source.kind === "search-url" ? Globe : Link2;
      const kindLabel = source.kind === "image" ? (language === "zh-CN" ? "图片" : "Image") : source.kind === "search-url" ? (language === "zh-CN" ? "网页搜索" : "Web search") : (language === "zh-CN" ? "输入链接" : "Typed link");
      return <button type="button" className="thread-support-source-row" key={source.id} aria-label={`${language === "zh-CN" ? "打开来源" : "Open source"}：${source.title}`} onClick={() => openSource(source)}>
        <span className="thread-support-source-icon"><Icon size={13} /></span>
        <span><strong>{source.title}</strong><small>{kindLabel}{source.detail && source.detail !== source.title ? ` · ${source.detail}` : ""}</small></span>
        <ExternalLink size={12} aria-hidden="true" />
      </button>;
    }) : <p className="thread-support-empty">{language === "zh-CN" ? "本轮还没有图片或链接来源。" : "No image or link sources in this turn yet."}</p>}
  </div>;
}

function TaskMark({ state }: { state: TodoStatus }) {
  if (state === "completed") return <span className="thread-support-task-mark" data-state={state} aria-hidden="true"><Check size={11} strokeWidth={2.2} /></span>;
  if (state === "cancelled") return <span className="thread-support-task-mark" data-state={state} aria-hidden="true"><Minus size={10} strokeWidth={2.2} /></span>;
  return <span className="thread-support-task-mark" data-state={state} aria-hidden="true" />;
}

function statusLabel(status: TodoStatus, language: Snapshot["language"]) {
  const t = translator(language);
  if (status === "completed") return t("todoCompleted");
  if (status === "cancelled") return t("cancelled");
  if (status === "in_progress") return t("todoInProgress");
  return t("todoPending");
}

function SourceImageLightbox({ source, sessionId, language, onClose }: { source: ConversationSource; sessionId: string; language: Snapshot["language"]; onClose: () => void }) {
  const attachment = source.attachment as Attachment;
  const [image, setImage] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    void attachmentDataURL(sessionId, attachment)
      .then((value) => { if (active) setImage(value); })
      .catch(() => { if (active) setImage(""); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [attachment, sessionId]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const closeLabel = language === "zh-CN" ? "关闭来源图片" : "Close source image";
  return createPortal(
    <div className="attachment-lightbox" role="dialog" aria-modal="true" aria-label={source.title} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section>
        <header><strong>{source.title}</strong><button type="button" aria-label={closeLabel} onClick={onClose}><X size={17} /></button></header>
        <div className="attachment-lightbox-canvas">
          {image ? <img src={image} alt={source.title} /> : <span className="attachment-preview-placeholder" aria-hidden="true">{loading ? <LoaderCircle className="spin" size={16} /> : <FileImage size={16} />}</span>}
        </div>
      </section>
    </div>,
    document.body,
  );
}
