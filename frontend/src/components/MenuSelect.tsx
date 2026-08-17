import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Search } from "lucide-react";

export type MenuSelectOption = { value: string; label: string; caption?: string; keywords?: string[]; icon?: ReactNode; disabled?: boolean };

type MenuCoords = {
  position: "fixed" | "absolute";
  top?: number;
  bottom?: number;
  left: number;
  width: number;
  maxHeight: number;
};

/** Modal <dialog> sits in the top layer; menus must portal into it or they paint behind and can't be clicked. */
function portalRoot(anchor: HTMLElement | null): HTMLElement {
  const dialog = anchor?.closest("dialog");
  if (dialog instanceof HTMLDialogElement && dialog.open) return dialog;
  return document.body;
}

export default function MenuSelect({ value, options, onChange, ariaLabel, className = "", panelClassName = "", disabled = false, placement = "bottom", fit = "default", searchable = false, searchPlaceholder = "Search…", emptyLabel = "No matches", showSelectedIcon = true, menuWidth, menuAlign = "left" }: {
  value: string;
  options: MenuSelectOption[];
  onChange: (value: string) => void;
  ariaLabel: string;
  className?: string;
  panelClassName?: string;
  disabled?: boolean;
  placement?: "top" | "bottom";
  /** full: show complete option labels in a wider floating menu */
  fit?: "default" | "full";
  searchable?: boolean;
  searchPlaceholder?: string;
  emptyLabel?: string;
  showSelectedIcon?: boolean;
  menuWidth?: number;
  menuAlign?: "left" | "right";
}) {
  const details = useRef<HTMLDetailsElement>(null);
  const optionsRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState<MenuCoords | null>(null);
  const [query, setQuery] = useState("");
  const selected = options.find((option) => option.value === value);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleOptions = normalizedQuery
    ? options.filter((option) => `${option.label} ${option.value} ${(option.keywords ?? []).join(" ")}`.toLocaleLowerCase().includes(normalizedQuery))
    : options;

  const updatePosition = useCallback(() => {
    const root = details.current;
    const summary = root?.querySelector("summary");
    if (!root?.open || !summary) {
      setCoords(null);
      return;
    }
    const host = portalRoot(root);
    const rect = summary.getBoundingClientRect();
    const hostRect = host.getBoundingClientRect();
    const inDialog = host instanceof HTMLDialogElement;
    const boundaryLeft = inDialog ? hostRect.left : 0;
    const boundaryTop = inDialog ? hostRect.top : 0;
    const boundaryWidth = inDialog && hostRect.width > 0 ? hostRect.width : window.innerWidth;
    const boundaryHeight = inDialog && hostRect.height > 0 ? hostRect.height : window.innerHeight;
    const boundaryRight = boundaryLeft + boundaryWidth;
    const boundaryBottom = boundaryTop + boundaryHeight;
    const gap = 6;
    const spaceBelow = boundaryBottom - rect.bottom - gap;
    const spaceAbove = rect.top - boundaryTop - gap;
    const preferBottom = placement === "bottom";
    const openBottom = preferBottom
      ? spaceBelow >= 140 || spaceBelow >= spaceAbove
      : spaceAbove < 140 && spaceBelow > spaceAbove;
    const available = Math.max(120, openBottom ? spaceBelow : spaceAbove);
    const maxHeight = Math.min(fit === "full" ? 360 : 280, available);
    const minWidth = menuWidth ?? (fit === "full" ? Math.max(rect.width, 240) : rect.width);
    const width = Math.min(Math.max(minWidth, rect.width), Math.max(160, boundaryWidth - 16));
    // Keep the panel inside the viewport or modal-dialog portal.
    const naturalLeft = menuAlign === "right" ? rect.right - width : rect.left;
    const viewportLeft = Math.min(Math.max(boundaryLeft + 8, naturalLeft), Math.max(boundaryLeft + 8, boundaryRight - width - 8));
    const left = viewportLeft - boundaryLeft;
    setCoords(openBottom
      ? { position: inDialog ? "absolute" : "fixed", top: rect.bottom - boundaryTop + gap, left, width, maxHeight }
      : { position: inDialog ? "absolute" : "fixed", bottom: boundaryBottom - rect.top + gap, left, width, maxHeight });
  }, [fit, menuAlign, menuWidth, placement]);

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (details.current?.contains(target) || optionsRef.current?.contains(target)) return;
      if (details.current) details.current.open = false;
      setOpen(false);
      setCoords(null);
      setQuery("");
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);

  useEffect(() => {
    if (!open) return;
    updatePosition();
    const onReposition = () => updatePosition();
    window.addEventListener("resize", onReposition);
    // Capture scroll from any ancestor (inspector-scroll, transcript, etc.).
    window.addEventListener("scroll", onReposition, true);
    return () => {
      window.removeEventListener("resize", onReposition);
      window.removeEventListener("scroll", onReposition, true);
    };
  }, [open, updatePosition, options.length]);

  useEffect(() => {
    if (open && coords && searchable) requestAnimationFrame(() => searchRef.current?.focus());
  }, [coords, open, searchable]);

  const focusOption = (edge: "selected" | "first" | "last") => requestAnimationFrame(() => {
    const items = Array.from(optionsRef.current?.querySelectorAll<HTMLButtonElement>(".menu-select-option:not(:disabled)") ?? []);
    const target = edge === "first" ? items[0] : edge === "last" ? items.at(-1) : items.find((item) => item.dataset.value === value) ?? items[0];
    target?.focus();
  });
  const close = () => {
    if (!details.current) return;
    details.current.open = false;
    setOpen(false);
    setCoords(null);
    setQuery("");
    details.current.querySelector<HTMLElement>("summary")?.focus();
  };
  const choose = (next: string) => { onChange(next); close(); };
  const move = (event: React.KeyboardEvent, offset: number) => {
    const items = Array.from(optionsRef.current?.querySelectorAll<HTMLButtonElement>(".menu-select-option:not(:disabled)") ?? []);
    if (!items.length) return;
    const index = Math.max(0, items.indexOf(event.target as HTMLButtonElement));
    items[(index + offset + items.length) % items.length]?.focus();
  };

  const menu = open && coords ? createPortal(
    <div
      ref={optionsRef}
      className={`menu-select-options menu-select-options-portal ${panelClassName}`.trim()}
      data-fit={fit}
      style={{
        position: coords.position,
        top: coords.top,
        bottom: coords.bottom,
        left: coords.left,
        width: coords.width,
        maxHeight: coords.maxHeight,
        zIndex: 200,
      }}
      onKeyDown={(event) => {
        if (event.target instanceof HTMLInputElement) {
          if (event.key === "ArrowDown") { event.preventDefault(); focusOption("first"); }
          else if (event.key === "Escape") { event.preventDefault(); close(); }
          else if (event.key === "Tab") close();
          return;
        }
        if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); move(event, event.key === "ArrowDown" ? 1 : -1); }
        else if (event.key === "Home" || event.key === "End") { event.preventDefault(); focusOption(event.key === "Home" ? "first" : "last"); }
        else if (event.key === "Escape") { event.preventDefault(); close(); }
        else if (event.key === "Tab") close();
      }}
    >
      {searchable && <label className="menu-select-search"><Search size={13} /><input ref={searchRef} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={searchPlaceholder} aria-label={searchPlaceholder} /></label>}
      <div className="menu-select-list" role="listbox" aria-label={ariaLabel}>
        {visibleOptions.map((option) => <button
          type="button"
          role="option"
          aria-selected={value === option.value}
          disabled={option.disabled}
          className={`menu-select-option ${option.icon ? "has-icon" : ""} ${value === option.value ? "selected" : ""}`}
          data-value={option.value}
          key={option.value}
          onClick={() => choose(option.value)}
        ><Check size={13} />{option.icon}<span className={option.caption ? "menu-select-option-copy" : undefined}><strong>{option.label}</strong>{option.caption && <small>{option.caption}</small>}</span></button>)}
        {visibleOptions.length === 0 && <div className="menu-select-empty" role="status">{emptyLabel}</div>}
      </div>
    </div>,
    portalRoot(details.current),
  ) : null;

  return <>
    <details
      ref={details}
      className={`menu-select ${className}`.trim()}
      data-placement={placement}
      data-fit={fit}
      data-disabled={String(disabled)}
      data-open={String(open)}
      onToggle={(event) => {
        if (disabled) {
          event.currentTarget.open = false;
          setOpen(false);
          setCoords(null);
          return;
        }
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) requestAnimationFrame(updatePosition);
        else { setCoords(null); setQuery(""); }
      }}
    >
      <summary
        aria-label={ariaLabel}
        aria-disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={(event) => { if (disabled) event.preventDefault(); }}
        onKeyDown={(event) => {
          if (disabled || !["ArrowDown", "ArrowUp"].includes(event.key)) return;
          event.preventDefault();
          if (details.current) details.current.open = true;
          setOpen(true);
          requestAnimationFrame(() => {
            updatePosition();
            focusOption(event.key === "ArrowDown" ? "first" : "last");
          });
        }}
      >{showSelectedIcon && selected?.icon}<span className={`menu-select-value ${selected?.caption ? "has-caption" : ""}`}><strong>{selected?.label ?? value}</strong>{selected?.caption && <small>{selected.caption}</small>}</span><ChevronDown size={12} /></summary>
    </details>
    {menu}
  </>;
}
