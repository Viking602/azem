import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Hand, ShieldAlert, ShieldCheck } from "lucide-react";
import { translator } from "../../i18n";
import type { Snapshot } from "../../types";

const approvalModes = [
  { value: "prompt", labelKey: "promptApproval" as const, hintKey: "approvalAskHint" as const, Icon: Hand, danger: false },
  { value: "auto_review", labelKey: "autoReview" as const, hintKey: "approvalAutoHint" as const, Icon: ShieldCheck, danger: false },
  { value: "yolo", labelKey: "yolo" as const, hintKey: "approvalFullHint" as const, Icon: ShieldAlert, danger: true },
] as const;

/** Codex-style approval / permissions menu in the composer toolbar. */
export function ApprovalPicker({ value, disabled, language, onChange }: {
  value: string;
  disabled?: boolean;
  language: Snapshot["language"];
  onChange: (mode: string) => void;
}) {
  const details = useRef<HTMLDetailsElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const summary = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);
  const [box, setBox] = useState<{ top?: number; bottom?: number; left: number; width: number } | null>(null);
  const t = translator(language);
  const current = approvalModes.find((mode) => mode.value === value) ?? approvalModes[0];
  const CurrentIcon = current.Icon;

  const place = useCallback(() => {
    const el = summary.current;
    if (!el || !details.current?.open) {
      setBox(null);
      return;
    }
    const rect = el.getBoundingClientRect();
    const width = Math.min(294, window.innerWidth - 16);
    const left = Math.min(Math.max(8, rect.left), window.innerWidth - width - 8);
    const spaceAbove = rect.top - 8;
    const openUp = spaceAbove >= 220 || spaceAbove > window.innerHeight - rect.bottom;
    setBox(openUp
      ? { bottom: window.innerHeight - rect.top + 8, left, width }
      : { top: rect.bottom + 8, left, width });
  }, []);

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (details.current?.contains(target) || panel.current?.contains(target)) return;
      if (details.current) details.current.open = false;
      setOpen(false);
      setBox(null);
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);

  useEffect(() => {
    if (!open) return;
    place();
    const onReposition = () => place();
    window.addEventListener("resize", onReposition);
    window.addEventListener("scroll", onReposition, true);
    return () => {
      window.removeEventListener("resize", onReposition);
      window.removeEventListener("scroll", onReposition, true);
    };
  }, [open, place]);

  const menu = open && box ? createPortal(
    <div
      ref={panel}
      className="approval-picker-menu"
      role="menu"
      aria-label={t("approvalMenuTitle")}
      style={{ position: "fixed", top: box.top, bottom: box.bottom, left: box.left, width: box.width, zIndex: 220 }}
    >
      <header className="approval-picker-heading">
        <strong>{language === "zh-CN" ? "审批模式" : "Approval mode"}</strong>
        <small>{language === "zh-CN" ? "控制工具执行边界" : "Control tool execution boundaries"}</small>
      </header>
      {approvalModes.map((mode) => {
        const Icon = mode.Icon;
        const selected = mode.value === value;
        return (
          <button
            type="button"
            role="menuitemradio"
            aria-checked={selected}
            className={`approval-picker-option ${selected ? "selected" : ""} ${mode.danger ? "danger" : ""}`}
            key={mode.value}
            onClick={() => {
              onChange(mode.value);
              if (details.current) details.current.open = false;
              setOpen(false);
              setBox(null);
            }}
          >
            <Icon size={16} />
            <span className="approval-picker-copy">
              <strong>{t(mode.labelKey)}</strong>
              <small>{t(mode.hintKey)}</small>
            </span>
            <Check size={14} className="approval-picker-check" />
          </button>
        );
      })}
    </div>,
    document.body,
  ) : null;

  return <>
    <details
      ref={details}
      className="approval-picker"
      data-disabled={String(Boolean(disabled))}
      data-mode={current.value}
      onToggle={(event) => {
        if (disabled) {
          event.currentTarget.open = false;
          setOpen(false);
          setBox(null);
          return;
        }
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) requestAnimationFrame(place);
        else setBox(null);
      }}
    >
      <summary
        ref={summary as React.RefObject<HTMLElement>}
        aria-label={t(current.labelKey)}
        aria-disabled={disabled}
      >
        <CurrentIcon size={14} />
        <span>{t(current.labelKey)}</span>
        <ChevronDown size={11} />
      </summary>
    </details>
    {menu}
  </>;
}
