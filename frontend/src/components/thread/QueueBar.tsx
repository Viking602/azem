import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  ArrowDown, ArrowUp, CornerUpRight, GripVertical, ImagePlus, ListX, MoreHorizontal, Pencil, RotateCcw, Trash2,
} from "lucide-react";
import { translator } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { DeliveryMode, QueuedPrompt } from "../../types";

export function QueuedPrompts({ items, running, pauseReason, editingId, deliveryMode, onGuide, onRetry, onDelete, onEdit, onReorder, onResume, onToggleQueue }: {
  items: QueuedPrompt[];
  running: boolean;
  pauseReason: "interrupted" | undefined;
  editingId: string | null;
  deliveryMode: DeliveryMode;
  onGuide: (item: QueuedPrompt) => Promise<void>;
  onRetry: (id: string) => void;
  onDelete: (item: QueuedPrompt) => void;
  onEdit: (item: QueuedPrompt) => void;
  onReorder: (id: string, targetId: string) => void;
  onResume: () => void;
  onToggleQueue: () => void;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const [draggedId, setDraggedId] = useState("");
  const t = translator(snapshot.language);
  return <section className="queued-prompts" aria-label={`${t("queuedMessages")} (${items.length})`}>
    {pauseReason === "interrupted" && <header className="queue-paused" role="status">
      <span>{t("queueInterrupted")}</span>
      <button type="button" onClick={onResume}>{t("resumeQueue")}</button>
    </header>}
    <div className="queued-prompt-scroll">
      {items.map((item, index) => <div
        className="queued-prompt"
        data-state={item.state ?? "queued"}
        data-editing={String(item.id === editingId)}
        draggable
        key={item.id}
        onDragStart={(event) => {
          setDraggedId(item.id);
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData("text/plain", item.id);
        }}
        onDragEnd={() => setDraggedId("")}
        onDragOver={(event) => {
          if (draggedId && draggedId !== item.id) event.preventDefault();
        }}
        onDrop={(event) => {
          event.preventDefault();
          const source = draggedId || event.dataTransfer.getData("text/plain");
          if (source && source !== item.id) onReorder(source, item.id);
          setDraggedId("");
        }}
      >
        <span className="queue-drag" aria-hidden="true"><GripVertical size={14} /></span>
        <button className="queued-prompt-content" onClick={() => onEdit(item)} title={item.error || t("editMessage")}>
          {item.attachments.length > 0 && <ImagePlus size={14} />}
          <span>{item.text || item.attachments[0]?.name}</span>
        </button>
        {item.state === "failed"
          ? <button className="queued-guide" title={item.error || t("queuedMessageFailed")} onClick={() => onRetry(item.id)}><RotateCcw size={14} />{t("retryMessage")}</button>
          : <button className="queued-guide" disabled={!running} title={t("steerTooltip")} onClick={() => void onGuide(item)}><CornerUpRight size={14} />{t("guide")}</button>}
        <button className="queued-icon" title={t("deleteMessage")} aria-label={t("deleteMessage")} onClick={() => onDelete(item)}><Trash2 size={14} /></button>
        <QueueMenu
          item={item}
          queueing={deliveryMode === "queue"}
          canMoveUp={index > 0}
          canMoveDown={index < items.length - 1}
          onEdit={onEdit}
          onMoveUp={() => onReorder(item.id, items[index - 1]!.id)}
          onMoveDown={() => onReorder(item.id, items[index + 1]!.id)}
          onToggleQueue={onToggleQueue}
        />
      </div>)}
    </div>
  </section>;
}

function QueueMenu({ item, queueing, canMoveUp, canMoveDown, onEdit, onMoveUp, onMoveDown, onToggleQueue }: {
  item: QueuedPrompt;
  queueing: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onEdit: (item: QueuedPrompt) => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onToggleQueue: () => void;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const details = useRef<HTMLDetailsElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [menuBox, setMenuBox] = useState<{ bottom: number; left: number; width: number } | null>(null);
  const t = translator(snapshot.language);
  const place = useCallback(() => {
    const summary = details.current?.querySelector("summary");
    if (!details.current?.open || !summary) {
      setMenuBox(null);
      return;
    }
    const rect = summary.getBoundingClientRect();
    const width = 180;
    setMenuBox({
      bottom: Math.max(8, window.innerHeight - rect.top + 4),
      left: Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8)),
      width,
    });
  }, []);
  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (details.current?.contains(target) || menu.current?.contains(target)) return;
      if (details.current) details.current.open = false;
      setMenuBox(null);
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);
  useEffect(() => {
    if (!menuBox) return;
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [menuBox, place]);
  const choose = (action: () => void) => {
    action();
    if (details.current) details.current.open = false;
    setMenuBox(null);
  };
  const actions = <div ref={menu} className="queue-menu-popover" style={menuBox ?? undefined}>
    <button onClick={() => choose(() => onEdit(item))}><Pencil size={14} />{t("editMessage")}</button>
    <button disabled={!canMoveUp} onClick={() => choose(onMoveUp)}><ArrowUp size={14} />{t("moveMessageUp")}</button>
    <button disabled={!canMoveDown} onClick={() => choose(onMoveDown)}><ArrowDown size={14} />{t("moveMessageDown")}</button>
    <button onClick={() => choose(onToggleQueue)}><ListX size={14} />{t(queueing ? "turnOffQueueing" : "turnOnQueueing")}</button>
  </div>;
  return <>
    <details ref={details} className="queue-menu" onToggle={() => requestAnimationFrame(place)}>
      <summary aria-label={t("moreActions")} title={t("moreActions")}><MoreHorizontal size={15} /></summary>
    </details>
    {menuBox ? createPortal(actions, document.body) : null}
  </>;
}
