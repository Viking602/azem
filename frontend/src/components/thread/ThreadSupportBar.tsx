import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ExternalLink, FileImage, Globe, Link2, LoaderCircle, Minus, Plus, X } from "lucide-react";
import { attachmentDataURL, openExternalURL } from "../../bridge";
import { tFormat, translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { Attachment, SessionRecap, Snapshot, TodoItem, TodoList, TodoStatus } from "../../types";
import { collectConversationSources, type ConversationSource } from "../conversationSources";

export type ThreadSupportPanel = "plan" | "recap" | "sources";

export interface ThreadPlanSummary {
  items: TodoItem[];
  completed: number;
  percentage: number;
  current: TodoItem;
}

export function summarizeThreadPlan(todo: TodoList): ThreadPlanSummary | null {
  const items = todo.phases.flatMap((phase) => phase.items);
  if (items.length === 0) return null;
  const completed = items.filter((item) => item.status === "completed" || item.status === "cancelled").length;
  let currentIndex = items.findIndex((item) => item.status === "in_progress");
  if (currentIndex < 0) currentIndex = items.findIndex((item) => item.status === "pending");
  if (currentIndex < 0) currentIndex = items.length - 1;
  return {
    items,
    completed,
    percentage: Math.round((completed / items.length) * 100),
    current: items[currentIndex]!,
  };
}

export default function ThreadSupportBar() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const todo = useRuntimeStore((state) => state.todo);
  const recap = useRuntimeStore((state) => state.recap);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setError = useRuntimeStore((state) => state.setError);
  const [activePanel, setActivePanel] = useState<ThreadSupportPanel | null>(null);
  const [preview, setPreview] = useState<ConversationSource | null>(null);
  const shellRef = useRef<HTMLDivElement>(null);
  const triggerRefs = useRef<Partial<Record<ThreadSupportPanel, HTMLButtonElement | null>>>({});
  const panelId = useId();
  const language = snapshot.language;
  const t = translator(language);
  const plan = todo ? summarizeThreadPlan(todo) : null;
  const sources = useMemo(() => collectConversationSources(blocks, language), [blocks, language]);

  const close = useCallback((returnFocus = false) => {
    setActivePanel((current) => {
      if (returnFocus && current) requestAnimationFrame(() => triggerRefs.current[current]?.focus());
      return null;
    });
  }, []);

  useEffect(() => {
    close(false);
    setPreview(null);
  }, [close, currentSessionId]);

  useEffect(() => {
    if (!activePanel) return;
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
  }, [activePanel, close]);

  const toggle = (panel: ThreadSupportPanel) => setActivePanel((current) => current === panel ? null : panel);
  const complete = plan ? plan.completed === plan.items.length : false;
  const planCurrentLabel = complete ? t("todoCompleted") : language === "zh-CN" ? "当前" : "Current";
  const recapLabel = language === "zh-CN" ? "回顾" : "Recap";
  const sourcesLabel = language === "zh-CN" ? "来源" : "Sources";

  return <div className="thread-support-shell" ref={shellRef} data-slot="thread-support">
    <nav className="thread-support-bar" data-has-plan={String(Boolean(plan))} aria-label={language === "zh-CN" ? "线程辅助信息" : "Thread support"}>
      {plan ? <button
        ref={(node) => { triggerRefs.current.plan = node; }}
        type="button"
        className="thread-support-trigger thread-support-plan-trigger"
        aria-label={`${t("todoTitle")} ${plan.completed} / ${plan.items.length}。${planCurrentLabel}：${plan.current.content}`}
        aria-expanded={activePanel === "plan"}
        aria-controls={panelId}
        onClick={() => toggle("plan")}
      >
        <strong>{t("todoTitle")}</strong><em>{plan.completed} / {plan.items.length}</em><span>{planCurrentLabel} · {plan.current.content}</span>
      </button> : null}
      <button
        ref={(node) => { triggerRefs.current.recap = node; }}
        type="button"
        className="thread-support-trigger"
        aria-label={`${recapLabel} ${recap ? `r${recap.revision}` : "—"}`}
        aria-expanded={activePanel === "recap"}
        aria-controls={panelId}
        onClick={() => toggle("recap")}
      >
        <strong>{recapLabel}</strong><span className="thread-support-badge">{recap ? `r${recap.revision}` : "—"}</span>
      </button>
      <button
        ref={(node) => { triggerRefs.current.sources = node; }}
        type="button"
        className="thread-support-trigger"
        aria-label={`${sourcesLabel} ${sources.length}`}
        aria-expanded={activePanel === "sources"}
        aria-controls={panelId}
        onClick={() => toggle("sources")}
      >
        <strong>{sourcesLabel}</strong><span className="thread-support-badge">{sources.length}</span>
      </button>
    </nav>
    {activePanel ? <section id={panelId} className="thread-support-panel" role="region" aria-label={panelTitle(activePanel, language)}>
      <header className="thread-support-panel-header">
        <div><h2>{panelTitle(activePanel, language)}</h2><span>{panelMetric(activePanel, plan, recap?.revision ?? null, sources.length)}</span></div>
        <p>{panelDescription(activePanel, language)}</p>
        <button type="button" aria-label={language === "zh-CN" ? `关闭${panelTitle(activePanel, language)}` : `Close ${panelTitle(activePanel, language)}`} onClick={() => close(true)}><X size={15} /></button>
      </header>
      {activePanel === "plan" && plan && todo ? <PlanPanel todo={todo} summary={plan} language={language} /> : null}
      {activePanel === "recap" ? <RecapPanel recap={recap} language={language} /> : null}
      {activePanel === "sources" ? <SourcesPanel sources={sources} language={language} openSource={(source) => {
        if (source.kind === "image") {
          setPreview(source);
          return;
        }
        if (source.href) void openExternalURL(source.href).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
      }} /> : null}
    </section> : null}
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

function panelTitle(panel: ThreadSupportPanel, language: Snapshot["language"]) {
  if (panel === "plan") return translator(language)("todoTitle");
  if (panel === "recap") return language === "zh-CN" ? "会话回顾" : "Session recap";
  return language === "zh-CN" ? "来源" : "Sources";
}

function panelMetric(panel: ThreadSupportPanel, plan: ThreadPlanSummary | null, recapRevision: number | null, sourceCount: number) {
  if (panel === "plan") return plan ? `${plan.completed} / ${plan.items.length}` : "—";
  if (panel === "recap") return recapRevision == null ? "—" : `r${recapRevision}`;
  return String(sourceCount);
}

function panelDescription(panel: ThreadSupportPanel, language: Snapshot["language"]) {
  if (language === "zh-CN") {
    if (panel === "plan") return "完整阶段与任务状态。";
    if (panel === "recap") return "跨回合摘要、当前目标与未完成事项。";
    return "本轮引用的图片、网页和文件。";
  }
  if (panel === "plan") return "Complete phases and task states.";
  if (panel === "recap") return "Cross-turn summary, current goal, and open items.";
  return "Images, pages, and files referenced in this turn.";
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
