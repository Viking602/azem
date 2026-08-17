import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { motion, useReducedMotion } from "motion/react";
import { Check, ChevronDown, ChevronRight, ChevronUp, Search, Zap } from "lucide-react";
import { reasoningHint, sortReasoningLevels } from "../../i18n";
import { providerDisplayName, useRuntimeStore } from "../../store";
import ReasoningEffortSlider, { isHighCostReasoning } from "../ReasoningEffortSlider";
import ProviderIcon from "../ProviderIcon";
import { modelKey, supportsFastMode, type ComposerModel } from "./composerModels";
import type { Snapshot } from "../../types";

type ModelControlOption = { value: string; label: string; provider?: string; hint?: string; searchText?: string };
type ModelControlView = "effort" | "advanced";
type ModelControlViewAction = "open" | "close" | "advanced" | "back";

export function nextModelControlView(current: ModelControlView, action: ModelControlViewAction): ModelControlView {
  return action === "advanced" ? "advanced" : action === "back" ? "effort" : current;
}

export function filterModelControlOptions(options: ModelControlOption[], query: string, provider: string) {
  const needle = query.trim().toLocaleLowerCase();
  return options.filter((option) =>
    (!provider || option.provider === provider)
    && (!needle || `${option.label} ${option.searchText ?? ""}`.toLocaleLowerCase().includes(needle))
  );
}

export function modelControlWidth(expanded: boolean, viewportWidth: number, contentWidth = 190) {
  return Math.min(expanded ? 248 : Math.min(300, Math.max(0, contentWidth)), viewportWidth * .52);
}

type ModelControlGroup = {
  label: string;
  current: string;
  selected: string;
  options: ModelControlOption[];
  select: (value: string) => void;
};

export function ModelControls({ running, models, selectedModel, selectedModelName, selectedProvider, reasoningLevels, selectedReasoning, selectedReasoningName, fast, reasoningNames, onModelChange, onReasoningChange, onSpeedChange, modelLabel, reasoningLabel, speedLabel, standardSpeed, fastSpeed, fastHint, fasterLabel, smarterLabel, advancedLabel, backLabel, highCostHint, fastBoostTitle, fastBoostDetail, language }: {
  running: boolean; models: ComposerModel[]; selectedModel: string; selectedModelName: string; selectedProvider: string; reasoningLevels: string[]; selectedReasoning: string; selectedReasoningName: string;
  fast: boolean; reasoningNames: Record<string, string>; onModelChange: (value: string) => void; onReasoningChange: (value: string) => void; onSpeedChange: (value: string) => void;
  modelLabel: string; reasoningLabel: string; speedLabel: string; standardSpeed: string; fastSpeed: string; fastHint: string;
  fasterLabel: string; smarterLabel: string; advancedLabel: string; backLabel: string; highCostHint: string;
  fastBoostTitle: string; fastBoostDetail: string;
  language: Snapshot["language"];
}) {
  const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const root = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const advancedPage = useRef<HTMLDivElement>(null);
  const effortPage = useRef<HTMLDivElement>(null);
  const rowRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  const hoverReady = useRef(false);
  const closedWidth = useRef(190);
  const [open, setOpen] = useState(false);
  // Plan A: effort slider is the default surface; advanced keeps the classic menus.
  const [view, setView] = useState<ModelControlView>("effort");
  const [viewTransitioning, setViewTransitioning] = useState(false);
  const [pageHeights, setPageHeights] = useState({ advanced: 0, effort: 0 });
  const [activeGroup, setActiveGroup] = useState<string | null>(null);
  const [modelQuery, setModelQuery] = useState("");
  const [modelProvider, setModelProvider] = useState("");
  const [menuBox, setMenuBox] = useState<{ top?: number; bottom?: number; left: number; width: number } | null>(null);
  const [subBox, setSubBox] = useState<{ top: number; left: number; width: number; maxHeight: number } | null>(null);
  const reduceMotion = useReducedMotion();

  const selectedChoice = models.find((model) => modelKey(model.provider, model.id) === selectedModel);
  const fastAvailable = supportsFastMode(selectedProvider, selectedChoice?.capabilities);
  const fastActive = fastAvailable && fast;
  const levels = sortReasoningLevels(reasoningLevels);
  const groups: ModelControlGroup[] = [
    { label: modelLabel, current: selectedModelName, selected: selectedModel, options: models.map((model) => ({ value: modelKey(model.provider, model.id), label: model.name, provider: model.provider, searchText: [model.id, ...(model.aliases ?? [])].join(" ") })), select: onModelChange },
    {
      label: reasoningLabel,
      current: selectedReasoningName,
      selected: selectedReasoning,
      options: levels.map((level) => ({
        value: level,
        label: reasoningNames[level] ?? level,
        hint: reasoningHint(level, language),
      })),
      select: onReasoningChange,
    },
    ...(fastAvailable ? [{ label: speedLabel, current: fastActive ? fastSpeed : standardSpeed, selected: fastActive ? "fast" : "standard", options: [{ value: "standard", label: standardSpeed }, { value: "fast", label: fastSpeed }], select: onSpeedChange }] : []),
  ];
  const groupsRef = useRef(groups);
  groupsRef.current = groups;

  useLayoutEffect(() => {
    if (!open || !menuBox || !advancedPage.current || !effortPage.current) return;
    const measure = () => {
      const next = { advanced: advancedPage.current!.scrollHeight, effort: effortPage.current!.scrollHeight };
      setPageHeights((current) => current.advanced === next.advanced && current.effort === next.effort ? current : next);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(advancedPage.current);
    observer.observe(effortPage.current);
    return () => observer.disconnect();
  }, [open, menuBox?.width, groups.length, levels.length]);

  const measureClosedWidth = useCallback(() => {
    const element = summary.current;
    const control = root.current;
    if (!element || !control) return;
    const parts = Array.from(element.children).filter((child) => !child.classList.contains("model-controls-chevron"));
    const contentWidth = parts.reduce((total, child) => {
      const range = document.createRange();
      range.selectNodeContents(child);
      const textWidth = typeof range.getBoundingClientRect === "function" ? range.getBoundingClientRect().width : 0;
      return total + (textWidth || child.getBoundingClientRect().width);
    }, 0);
    if (contentWidth <= 0) return;
    const style = window.getComputedStyle(element);
    const gap = Number.parseFloat(style.columnGap || style.gap) || 0;
    const padding = (Number.parseFloat(window.getComputedStyle(control).getPropertyValue("--model-control-closed-padding")) || 0) * 2;
    const width = modelControlWidth(false, window.innerWidth, Math.ceil(contentWidth + gap * Math.max(0, parts.length - 1) + padding));
    closedWidth.current = width;
    control.style.setProperty("--model-control-closed-width", `${width}px`);
  }, []);

  useLayoutEffect(measureClosedWidth);
  useEffect(() => {
    window.addEventListener("resize", measureClosedWidth);
    return () => window.removeEventListener("resize", measureClosedWidth);
  }, [measureClosedWidth]);

  const placeMenu = useCallback(() => {
    const el = summary.current;
    if (!el || !root.current?.open) {
      setMenuBox(null);
      return;
    }
    const rect = el.getBoundingClientRect();
    // Reverse-engineered from Codex model-picker menu: compact chip, not a wide sheet.
    const width = modelControlWidth(true, window.innerWidth);
    const spaceAbove = rect.top - 8;
    const spaceBelow = window.innerHeight - rect.bottom - 8;
    const openUp = spaceAbove >= 160 || spaceAbove > spaceBelow;
    const left = Math.min(Math.max(8, rect.right - width), window.innerWidth - width - 8);
    setMenuBox(openUp
      ? { bottom: window.innerHeight - rect.top + 8, left, width }
      : { top: rect.bottom + 8, left, width });
  }, [view]);

  const placeSubmenu = useCallback((label: string) => {
    const row = rowRefs.current[label];
    const panel = menu.current;
    if (!row || !panel) {
      setSubBox(null);
      return;
    }
    const rowRect = row.getBoundingClientRect();
    const menuRect = panel.getBoundingClientRect();
    const group = groupsRef.current.find((item) => item.label === label);
    const options = group?.options ?? [];
    const longest = options.reduce((max, option) => Math.max(max, option.label.length + (option.hint?.length ? 4 : 0)), 0) || 12;
    const modelMenu = label === modelLabel;
    const width = Math.min(Math.max(modelMenu ? 280 : 180, longest * 9 + 56), Math.min(320, window.innerWidth - 16));
    const spaceLeft = menuRect.left - 8;
    const spaceRight = window.innerWidth - menuRect.right - 8;
    const openRight = spaceRight >= width || spaceRight >= spaceLeft;
    const left = openRight
      ? Math.min(menuRect.right + 8, window.innerWidth - width - 8)
      : Math.max(8, menuRect.left - width - 8);
    const titleH = 28;
    const pad = 12;
    const rowH = options.reduce((sum, option) => sum + (option.hint ? 48 : 36), 0);
    const contentHeight = titleH + pad + rowH + (modelMenu ? 76 : 0);
    const maxHeight = Math.min(Math.max(contentHeight, 80), window.innerHeight - 16);
    let top = rowRect.top - 8;
    if (top + Math.min(contentHeight, maxHeight) > window.innerHeight - 8) {
      top = Math.max(8, window.innerHeight - 8 - Math.min(contentHeight, maxHeight));
    }
    top = Math.max(8, top);
    setSubBox({ top, left, width, maxHeight });
  }, [modelLabel]);

  const resetClosed = useCallback(() => {
    setOpen(false);
    setViewTransitioning(false);
    setView((current) => nextModelControlView(current, "close"));
    setActiveGroup(null);
    setModelQuery("");
    setModelProvider("");
    setMenuBox(null);
    setSubBox(null);
  }, []);

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (root.current?.contains(target) || menu.current?.contains(target)) return;
      if ((target as HTMLElement).closest?.(".model-control-submenu-portal")) return;
      if (root.current) root.current.open = false;
      resetClosed();
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, [resetClosed]);

  useEffect(() => {
    if (!open) return;
    placeMenu();
    const onReposition = () => {
      placeMenu();
      if (view === "advanced" && activeGroup) placeSubmenu(activeGroup);
    };
    window.addEventListener("resize", onReposition);
    window.addEventListener("scroll", onReposition, true);
    return () => {
      window.removeEventListener("resize", onReposition);
      window.removeEventListener("scroll", onReposition, true);
    };
  }, [open, view, activeGroup, placeMenu, placeSubmenu]);

  useEffect(() => {
    if (!open || view !== "advanced" || !activeGroup) {
      setSubBox(null);
      return;
    }
    const frame = requestAnimationFrame(() => placeSubmenu(activeGroup));
    return () => cancelAnimationFrame(frame);
  }, [open, view, activeGroup, groups.find((group) => group.label === activeGroup)?.options.length, placeSubmenu]);

  const transitionModelControlView = useCallback((action: "advanced" | "back") => {
    const next = nextModelControlView(view, action);
    if (next === view) return;
    hoverReady.current = false;
    setViewTransitioning(true);
    setActiveGroup(null);
    setSubBox(null);
    if (action === "back") {
      setModelQuery("");
      setModelProvider("");
    }
    setView(next);
    requestAnimationFrame(placeMenu);
  }, [placeMenu, view]);

  const closeAll = () => {
    if (root.current) root.current.open = false;
    resetClosed();
    summary.current?.focus();
  };
  const choose = (select: (value: string) => void, value: string) => {
    select(value);
    // Keep the advanced panel open when changing model so the user can also adjust effort.
    if (view === "advanced") {
      setActiveGroup(null);
      setModelQuery("");
      setModelProvider("");
      setSubBox(null);
      return;
    }
    closeAll();
  };

  const active = groups.find((group) => group.label === activeGroup) ?? null;
  const modelGroupActive = active?.label === modelLabel;
  const providerOptions = [...new Set(groups[0]!.options.flatMap((option) => option.provider ? [option.provider] : []))];
  const activeOptions = modelGroupActive && active ? filterModelControlOptions(active.options, modelQuery, modelProvider) : active?.options ?? [];
  const highEffort = isHighCostReasoning(selectedReasoning);
  const activePageHeight = pageHeights[view] || "auto";
  const pageTransition = reduceMotion
    ? { duration: 0 }
    : { duration: .22, ease: [.23, 1, .32, 1] as [number, number, number, number] };

  const menuPortal = open && menuBox ? createPortal(
    <motion.div
      ref={menu}
      className="model-control-menu model-control-menu-portal"
      data-view={view}
      data-transitioning={String(viewTransitioning)}
      onPointerMove={() => { if (view === "advanced") hoverReady.current = true; }}
      style={{
        position: "fixed",
        top: menuBox.top,
        bottom: menuBox.bottom,
        left: menuBox.left,
        width: menuBox.width,
        zIndex: 210,
      }}
    >
      <motion.div
        className="model-control-page-stack"
        initial={false}
        animate={{ height: activePageHeight }}
        transition={pageTransition}
      >
        <motion.div
          ref={advancedPage}
          className="model-control-page model-control-page-advanced"
          data-active={String(view === "advanced")}
          aria-hidden={view !== "advanced"}
          inert={view !== "advanced"}
          initial={false}
          animate={{ y: view === "advanced" ? 0 : -pageHeights.advanced }}
          transition={pageTransition}
          onAnimationComplete={() => setViewTransitioning(false)}
        >
          {groups.map((group) => (
            <div
              className={`model-control-item ${activeGroup === group.label ? "open" : ""}`}
              key={group.label}
              onMouseEnter={() => !running && hoverReady.current && setActiveGroup(group.label)}
              onFocus={() => !running && setActiveGroup(group.label)}
            >
              <button
                type="button"
                ref={(node) => { rowRefs.current[group.label] = node; }}
                className="model-control-row"
                disabled={running}
                aria-haspopup="menu"
                aria-expanded={activeGroup === group.label}
                onClick={() => !running && setActiveGroup((current) => current === group.label ? null : group.label)}
              >
                <span>{group.label}</span>
                <small>{group.current}</small>
                <ChevronRight size={13} />
              </button>
            </div>
          ))}
          <div className="model-control-advanced-footer">
            <button
              type="button"
              className="model-control-back"
              aria-label={backLabel}
              onClick={() => {
                transitionModelControlView("back");
              }}
            >
              <span>{advancedLabel}</span>
              <ChevronUp size={13} />
            </button>
          </div>
        </motion.div>
        <motion.div
          ref={effortPage}
          className="model-control-page model-control-page-effort"
          data-active={String(view === "effort")}
          aria-hidden={view !== "effort"}
          inert={view !== "effort"}
          initial={false}
          animate={{ y: view === "effort" ? 0 : pageHeights.advanced }}
          transition={pageTransition}
        >
          <div className="effort-panel">
            <div className="effort-panel-toolbar">
              <button
                type="button"
                className="effort-panel-advanced"
                disabled={running}
                onClick={() => {
                  transitionModelControlView("advanced");
                }}
              >
                <span>{advancedLabel}</span>
                <ChevronRight size={12} />
              </button>
              {fastAvailable ? <div className={`effort-panel-speed-wrap ${fastActive ? "active" : ""}`}>
                <button
                  type="button"
                  className={`effort-panel-speed ${fastActive ? "active" : ""}`}
                  disabled={running}
                  aria-label={speedLabel}
                  aria-pressed={fastActive}
                  onClick={() => onSpeedChange(fastActive ? "standard" : "fast")}
                >
                  <Zap size={16} />
                </button>
                <div className="effort-speed-tip" role="tooltip">
                  <strong>{fastBoostTitle}</strong>
                  <span>{fastBoostDetail}</span>
                </div>
              </div> : null}
            </div>
            {levels.length > 1 ? (
              <ReasoningEffortSlider
                levels={levels}
                value={selectedReasoning}
                onChange={onReasoningChange}
                labels={reasoningNames}
                fasterLabel={fasterLabel}
                smarterLabel={smarterLabel}
                highCostHint={highCostHint}
                fast={fastActive}
                disabled={running}
                ariaLabel={reasoningLabel}
              />
            ) : (
              <p className="effort-panel-single">{selectedReasoningName || reasoningLabel}</p>
            )}
          </div>
        </motion.div>
      </motion.div>
    </motion.div>,
    document.body,
  ) : null;

  const subPortal = open && view === "advanced" && active && subBox ? createPortal(
    <div
      className="model-control-submenu model-control-submenu-portal"
      role="menu"
      aria-label={active.label}
      style={{
        position: "fixed",
        top: subBox.top,
        left: subBox.left,
        width: subBox.width,
        maxHeight: subBox.maxHeight,
        zIndex: 211,
      }}
      onMouseEnter={() => setActiveGroup(active.label)}
    >
      <header className="model-control-submenu-title">{active.label}</header>
      {modelGroupActive ? <div className="model-control-model-tools">
        <label className="model-control-model-search">
          <Search size={14} />
          <input
            autoFocus
            type="search"
            value={modelQuery}
            onChange={(event) => setModelQuery(event.target.value)}
            placeholder={language === "zh-CN" ? "搜索模型…" : "Search models…"}
            aria-label={language === "zh-CN" ? "搜索模型" : "Search models"}
          />
        </label>
        <div className="model-control-provider-filters" role="group" aria-label={language === "zh-CN" ? "按供应商筛选" : "Filter by provider"}>
          <button type="button" className={!modelProvider ? "selected" : ""} aria-pressed={!modelProvider} onClick={() => setModelProvider("")}>
            {language === "zh-CN" ? "全部" : "All"}
          </button>
          {providerOptions.map((provider) => <button type="button" className={modelProvider === provider ? "selected" : ""} aria-pressed={modelProvider === provider} key={provider} onClick={() => setModelProvider(provider)}>
            <ProviderIcon provider={provider} size={12} />
            <span>{providerDisplayName(provider, modelProviders)}</span>
          </button>)}
        </div>
      </div> : null}
      {activeOptions.map((option) => (
        <button
          type="button"
          role="menuitemradio"
          aria-checked={active.selected === option.value}
          disabled={running}
          className={`${option.provider ? "has-provider-icon" : ""} ${active.selected === option.value ? "selected" : ""}`.trim()}
          key={option.value}
          onClick={() => choose(active.select, option.value)}
        >
          {option.provider ? <ProviderIcon provider={option.provider} /> : null}
          <span className="model-control-option-copy">
            <span>{option.label}</span>
            {option.hint ? <small>{option.hint}</small> : null}
          </span>
          <Check size={13} />
        </button>
      ))}
      {activeOptions.length === 0 ? <p className="model-control-empty">{language === "zh-CN" ? "没有匹配的模型" : "No matching models"}</p> : null}
    </div>,
    document.body,
  ) : null;

  return <>
    <details
      ref={root}
      className="model-controls"
      data-disabled={String(running)}
      data-open={String(open)}
      onToggle={(event) => {
        if (running) {
          event.currentTarget.open = false;
          resetClosed();
          return;
        }
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) {
          setView((current) => nextModelControlView(current, "open"));
          setActiveGroup(null);
          requestAnimationFrame(placeMenu);
        } else {
          resetClosed();
        }
      }}
    >
      <summary
        ref={summary as React.RefObject<HTMLElement>}
        aria-disabled={running}
        aria-expanded={open}
        data-fast={String(fastActive)}
        data-high={String(highEffort)}
      >
        <ProviderIcon provider={selectedProvider} size={14} />
        <span>{selectedModelName}</span>
        <small data-high={String(highEffort)}>{selectedReasoningName}</small>
        <ChevronDown size={12} className="model-controls-chevron" />
      </summary>
    </details>
    {menuPortal}
    {subPortal}
  </>;
}
