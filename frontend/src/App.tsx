import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, GitBranch, Search } from "lucide-react";
import { execute, initialise, isDesktopRuntime, resumeSession, subscribe, subscribePullRequests } from "./bridge";
import Sidebar from "./components/Sidebar";
import { AppOverlays, AppWorkspace } from "./components/AppSurfaces";
import { tFormat, translator } from "./i18n";
import { isTerminalToggleKey } from "./terminal";
import { useTerminalStore } from "./terminalStore";
import {
  CHAT_CODE_FONT_STORAGE_KEY,
  CHAT_UI_FONT_STORAGE_KEY,
  applyChatTypography,
} from "./chatTypography";
import { normalizeUIFont, shouldMarkSessionUnread, useRuntimeStore } from "./store";
import { refreshPullRequestDashboard } from "./pullRequests";
import type { RuntimeEvent } from "./types";

const STREAM_FRAME_INTERVAL_MS = 32;
const PROJECTION_RESYNC_DELAY_MS = 32;
const STREAM_EVENT_KINDS = new Set(["text_delta", "thinking_delta"]);
const TERMINAL_EVENT_KINDS = new Set(["run_finished", "run_failed", "run_cancelled"]);
const HIGH_PRIORITY_EVENT_KINDS = new Set([
  ...TERMINAL_EVENT_KINDS,
  "approval_requested",
  "approval_resolved",
]);

// Interaction-blocking and terminal events must not wait behind the rAF-paced
// streaming queue (which can be paused in background windows or delayed by the
// per-frame text budget): dispatch them immediately.
export function isHighPriorityEvent(kind: string): boolean {
  return HIGH_PRIORITY_EVENT_KINDS.has(kind);
}
const SYSTEM_FONT_STACK = '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", "Noto Sans SC", "Microsoft YaHei", sans-serif';

function refreshProjection(event: RuntimeEvent, timers: Map<string, number>, setError: (message: string) => void) {
  const sessionId = event.sessionId;
  if (event.kind !== "projection_resync" || !sessionId || sessionId !== useRuntimeStore.getState().currentSessionId) return;
  window.clearTimeout(timers.get(sessionId));
  timers.set(sessionId, window.setTimeout(() => {
    timers.delete(sessionId);
    const selectedAgentId = useRuntimeStore.getState().selectedAgentId;
    const agentId = event.agentId || selectedAgentId;
    void execute({ kind: "refresh_session", target: sessionId, sessionId })
      .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
    if (agentId) {
      void execute({ kind: "inspect_agent", target: agentId, sessionId })
        .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
    }
  }, PROJECTION_RESYNC_DELAY_MS));
}

function persistForeignSessionCompletion(event: RuntimeEvent, setError: (message: string) => void) {
  if (!shouldMarkSessionUnread(useRuntimeStore.getState(), event)) return;
  void execute({ kind: "mark_session_unread", target: event.sessionId, sessionId: event.sessionId })
    .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
}

function interfaceFontStack(font: string) {
  if (font === "system") return SYSTEM_FONT_STACK;
  return `"${normalizeUIFont(font)}", ${SYSTEM_FONT_STACK}`;
}

function textBudget(queue: RuntimeEvent[], reducedMotion: boolean) {
  if (reducedMotion) return 2048;
  let pending = 0;
  let terminal = false;
  for (const event of queue) {
    if (STREAM_EVENT_KINDS.has(event.kind)) pending += event.text?.length ?? 0;
    if (TERMINAL_EVENT_KINDS.has(event.kind)) terminal = true;
    if (pending > 4096 && terminal) break;
  }
  if (terminal || pending > 4096) return 1024;
  if (pending > 1024) return 192;
  return 72;
}

function safeChunkEnd(text: string, limit: number) {
  let end = Math.min(text.length, limit);
  if (end < text.length && /[\uD800-\uDBFF]/u.test(text[end - 1] ?? "") && /[\uDC00-\uDFFF]/u.test(text[end] ?? "")) end--;
  return end || Math.min(2, text.length);
}

export function takeRuntimeEventFrame(queue: RuntimeEvent[], reducedMotion = false) {
  const events: RuntimeEvent[] = [];
  let budget = textBudget(queue, reducedMotion);
  while (queue.length && events.length < 32) {
    const event = queue[0]!;
    const text = event.text ?? "";
    if (!STREAM_EVENT_KINDS.has(event.kind) || !text) {
      events.push(event);
      queue.shift();
      continue;
    }
    if (budget <= 0) break;
    if (text.length <= budget) {
      events.push(event);
      queue.shift();
      budget -= text.length;
      continue;
    }
    const end = safeChunkEnd(text, budget);
    events.push({ ...event, sequence: 0, text: text.slice(0, end) });
    queue[0] = { ...event, text: text.slice(end) };
    break;
  }
  return events;
}

export default function App() {
  const hydrate = useRuntimeStore((state) => state.hydrate);
  const applyEvents = useRuntimeStore((state) => state.applyEvents);
  const setError = useRuntimeStore((state) => state.setError);
  const snapshot = useRuntimeStore((state) => state.snapshot);
  const view = useRuntimeStore((state) => state.view);
  const blocks = useRuntimeStore((state) => state.blocks);
  const running = useRuntimeStore((state) => state.running);
  const inspectorOpen = useRuntimeStore((state) => state.inspectorOpen);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const selectedAgentId = useRuntimeStore((state) => state.selectedAgentId);
  const selectedPullRequestNumber = useRuntimeStore((state) => state.selectedPullRequestNumber);
  const settingsOpen = useRuntimeStore((state) => state.settingsOpen);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const commandOpen = useRuntimeStore((state) => state.commandOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const terminalOpen = useTerminalStore((state) => state.open);
  const [terminalMounted, setTerminalMounted] = useState(() => useTerminalStore.getState().open);
  const theme = useRuntimeStore((state) => state.theme);
  const uiFont = useRuntimeStore((state) => state.uiFont);
  const uiFontSize = useRuntimeStore((state) => state.uiFontSize);
  const chatFontSize = useRuntimeStore((state) => state.chatFontSize);
  const chatCodeFontSize = useRuntimeStore((state) => state.chatCodeFontSize);
  const [appearanceReady, setAppearanceReady] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(246);
  const queue = useRef<RuntimeEvent[]>([]);
  const frame = useRef(0);
  const lastFlush = useRef(-Infinity);

  useEffect(() => {
    if (terminalOpen) setTerminalMounted(true);
  }, [terminalOpen]);

  useEffect(() => {
    let workspaceRefreshTimer = 0;
    const projectionRefreshTimers = new Map<string, number>();
    const refreshWorkspace = () => {
      window.clearTimeout(workspaceRefreshTimer);
      workspaceRefreshTimer = window.setTimeout(() => {
        void execute({ kind: "list_git_branches" }).catch(() => undefined);
        void refreshPullRequestDashboard();
      }, 120);
    };
    const flush = (timestamp: number) => {
      frame.current = 0;
      if (timestamp - lastFlush.current < STREAM_FRAME_INTERVAL_MS) {
        frame.current = requestAnimationFrame(flush);
        return;
      }
      const events = takeRuntimeEventFrame(queue.current, window.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
      if (events.length) applyEvents(events);
      lastFlush.current = timestamp;
      if (queue.current.length) frame.current = requestAnimationFrame(flush);
    };
    const unsubscribe = subscribe((event) => {
      persistForeignSessionCompletion(event, setError);
      refreshProjection(event, projectionRefreshTimers, setError);
      if (isHighPriorityEvent(event.kind)) {
        // 高优先级事件不等 rAF：先整体应用已排队的流式事件（保持 sequence
        // 单调，避免低序号文本被 lastSequence 去重丢弃），再立即派发本事件。
        if (frame.current) { cancelAnimationFrame(frame.current); frame.current = 0; }
        const pending = queue.current;
        queue.current = [];
        if (pending.length) applyEvents(pending);
        applyEvents([event]);
        lastFlush.current = performance.now();
      } else {
        queue.current.push(event);
        if (!frame.current) frame.current = requestAnimationFrame(flush);
      }
      const tool = event.data?.name ?? "";
      if (TERMINAL_EVENT_KINDS.has(event.kind) ||
          (event.kind === "tool_finished" && ["coding.edit_hashline", "coding.write_file", "coding.gofmt"].includes(tool))) refreshWorkspace();
    });
    const unsubscribePullRequests = subscribePullRequests((monitor) => useRuntimeStore.getState().updatePullRequestMonitor(monitor));
    window.addEventListener("focus", refreshWorkspace);
    initialise()
      .then(async (value) => {
        hydrate(value, !isDesktopRuntime());
        void Promise.all([
          execute({ kind: "list_sessions" }),
          execute({ kind: "list_git_branches" }),
          execute({ kind: "list_models", sessionId: value.sessionId }),
          execute({ kind: "list_model_providers", sessionId: value.sessionId }),
          execute({ kind: "list_model_routes", sessionId: value.sessionId }),
        ]).catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
        void refreshPullRequestDashboard();
        const parameters = new URLSearchParams(location.search);
        const sessionId = parameters.get("session");
        const hasSearchSequence = parameters.has("searchSequence");
        const searchSequence = Number(parameters.get("searchSequence"));
        if (sessionId && isDesktopRuntime()) {
          if (hasSearchSequence && Number.isSafeInteger(searchSequence) && searchSequence >= 0) {
            useRuntimeStore.getState().setSessionSearchTarget({ sessionId, sequence: searchSequence });
          }
          const projection = await resumeSession(sessionId);
          if (projection) applyEvents([projection]);
        }
      })
      .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
    return () => {
      unsubscribe();
      unsubscribePullRequests();
      if (frame.current) cancelAnimationFrame(frame.current);
      window.clearTimeout(workspaceRefreshTimer);
      for (const timer of projectionRefreshTimers.values()) window.clearTimeout(timer);
      window.removeEventListener("focus", refreshWorkspace);
    };
  }, [applyEvents, hydrate, setError]);

  useEffect(() => {
    const saved = localStorage.getItem("azem:theme");
    if (saved === "light" || saved === "dark" || saved === "system") useRuntimeStore.getState().setTheme(saved);
    const savedFont = localStorage.getItem("azem:ui-font");
    if (savedFont) useRuntimeStore.getState().setUIFont(savedFont);
    const savedFontSize = Number(localStorage.getItem("azem:ui-font-size"));
    if (Number.isFinite(savedFontSize) && savedFontSize >= 11 && savedFontSize <= 20) useRuntimeStore.getState().setUIFontSize(savedFontSize);
    const savedChatFontSize = Number(localStorage.getItem(CHAT_UI_FONT_STORAGE_KEY));
    if (Number.isFinite(savedChatFontSize)) useRuntimeStore.getState().setChatFontSize(savedChatFontSize);
    const savedChatCodeFontSize = Number(localStorage.getItem(CHAT_CODE_FONT_STORAGE_KEY));
    if (Number.isFinite(savedChatCodeFontSize)) useRuntimeStore.getState().setChatCodeFontSize(savedChatCodeFontSize);
    setAppearanceReady(true);
  }, []);

  useEffect(() => {
    if (!appearanceReady) return;
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.setProperty("--ui-font-family", interfaceFontStack(uiFont));
    document.documentElement.style.setProperty("--ui-font-size", `${uiFontSize}px`);
    applyChatTypography(chatFontSize, chatCodeFontSize);
    localStorage.setItem("azem:theme", theme);
    localStorage.setItem("azem:ui-font", uiFont);
    localStorage.setItem("azem:ui-font-size", String(uiFontSize));
    localStorage.setItem(CHAT_UI_FONT_STORAGE_KEY, String(chatFontSize));
    localStorage.setItem(CHAT_CODE_FONT_STORAGE_KEY, String(chatCodeFontSize));
  }, [appearanceReady, theme, uiFont, uiFontSize, chatFontSize, chatCodeFontSize]);

  useEffect(() => {
    const preventNativeContextMenu = (event: MouseEvent) => event.preventDefault();
    document.addEventListener("contextmenu", preventNativeContextMenu);
    return () => document.removeEventListener("contextmenu", preventNativeContextMenu);
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return;
      const primary = event.metaKey || event.ctrlKey;
      if (isTerminalToggleKey(event)) {
        const ui = useRuntimeStore.getState();
        if (ui.settingsOpen || ui.commandOpen) return;
        event.preventDefault();
        useTerminalStore.getState().toggle();
      } else if (primary && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      } else if (primary && event.key.toLowerCase() === "n") {
        event.preventDefault();
        void execute({ kind: "new_session" }).then(() => useRuntimeStore.getState().setView("thread")).catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
      } else if (primary && event.key === "2") {
        event.preventDefault();
        useRuntimeStore.getState().setView("files");
      } else if (primary && event.key === "3") {
        event.preventDefault();
        useRuntimeStore.getState().setView("changes");
      } else if (primary && event.key === ",") {
        event.preventDefault();
        setSettingsOpen(true);
      } else if (primary && event.key.toLowerCase() === "l") {
        event.preventDefault();
        document.querySelector<HTMLTextAreaElement>("#azem-composer")?.focus();
      } else if (event.key === "Escape" && !settingsOpen && !commandOpen) {
        if ((event.target as HTMLElement | null)?.closest?.(".terminal-panel")) return;
        if (useRuntimeStore.getState().selectedPullRequestNumber) {
          event.preventDefault();
          useRuntimeStore.getState().selectPullRequest(null);
          return;
        }
        if (useRuntimeStore.getState().selectedAgentId) {
          event.preventDefault();
          useRuntimeStore.getState().selectAgent("");
          return;
        }
        if (useRuntimeStore.getState().view === "agents") {
          event.preventDefault();
          useRuntimeStore.getState().setView("thread");
          requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(".inspector-toggle")?.focus());
          return;
        }
        if (useRuntimeStore.getState().view === "files" || useRuntimeStore.getState().view === "changes") {
          event.preventDefault();
          useRuntimeStore.getState().setView("projects");
          return;
        }
        if (running) document.querySelector<HTMLButtonElement>("[data-cancel-run]")?.click();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [commandOpen, running, setCommandOpen, setError, setSettingsOpen, settingsOpen]);

  if (!snapshot) {
    // Language not hydrated yet — default to zh-CN until snapshot arrives.
    return <div className="app-loading"><span className="azem-mark" />{translator("zh-CN")("loading")}</div>;
  }

  const hasContext = blocks.length > 0 || running;
  const showPullRequest = Boolean(selectedPullRequestNumber);
  const showAgentDetailDrawer = !showPullRequest && (view === "thread" || view === "agents") && Boolean(selectedAgentId);
  const showAgentDrawer = view === "agents" && !selectedAgentId;
  const showInspector = !showPullRequest && view === "thread" && hasContext && inspectorOpen && !showAgentDetailDrawer;
  // Subagent inspection is an overlay, not another workspace column. Opening a
  // child conversation must never reflow or squeeze the parent transcript.
  const layoutMode = showPullRequest ? "pull-request" : showInspector ? "open" : "closed";
  const t = translator(snapshot.language);
  const lazyFallback = <div className="app-loading"><span className="azem-mark" />{t("loading")}</div>;
  return (
    <div className="desktop-shell" data-runtime={String(isDesktopRuntime())} data-platform={navigator.platform} style={{ "--sidebar-width": `${sidebarWidth}px` } as React.CSSProperties}>
      <AppTitleBar />
      <div className="workspace-grid" data-inspector={layoutMode}>
        <Sidebar />
        <ResizeHandle value={sidebarWidth} setValue={setSidebarWidth} min={224} max={340} />
        <AppWorkspace
          view={view}
          fallback={lazyFallback}
          terminalOpen={terminalOpen}
          terminalMounted={terminalMounted}
          showInspector={showInspector}
          showAgentDrawer={showAgentDrawer}
          showAgentDetailDrawer={showAgentDetailDrawer}
          showPullRequest={showPullRequest}
        />
      </div>
      <AppOverlays settingsOpen={settingsOpen} commandOpen={commandOpen} />
    </div>
  );
}

function AppTitleBar() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceChangedFiles = useRuntimeStore((state) => state.workspaceChangedFiles);
  const setError = useRuntimeStore((state) => state.setError);
  const [branchOpen, setBranchOpen] = useState(false);
  const [branchSearch, setBranchSearch] = useState("");
  const branchSwitch = useRef<HTMLDivElement>(null);
  const t = translator(snapshot.language);
  const project = snapshot.workspace.split(/[\\/]/).filter(Boolean).at(-1) || "workspace";
  const branch = branches.find((item) => item.current)?.name || snapshot.currentBranch || t("noBranches");
  const visibleBranches = branches
    .filter((item) => !branchSearch.trim() || item.name.toLowerCase().includes(branchSearch.trim().toLowerCase()))
    .slice()
    .sort((left, right) => Number(right.current) - Number(left.current) || left.name.localeCompare(right.name));

  useEffect(() => {
    if (!branchOpen) return;
    const close = (event: PointerEvent) => {
      if (branchSwitch.current && !branchSwitch.current.contains(event.target as Node)) setBranchOpen(false);
    };
    const closeWithKeyboard = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setBranchOpen(false);
        setBranchSearch("");
      }
    };
    document.addEventListener("pointerdown", close, true);
    document.addEventListener("keydown", closeWithKeyboard);
    return () => {
      document.removeEventListener("pointerdown", close, true);
      document.removeEventListener("keydown", closeWithKeyboard);
    };
  }, [branchOpen]);

  const switchBranch = async (name: string, confirmDirty = false) => {
    if (!name || name === branch) {
      setBranchOpen(false);
      setBranchSearch("");
      return;
    }
    try {
      await execute({
        kind: "switch_git_branch",
        target: name,
        decision: confirmDirty ? "confirm_dirty" : undefined,
      });
      setBranchOpen(false);
      setBranchSearch("");
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      if (!confirmDirty && /uncommitted changes/i.test(message)) {
        if (window.confirm(tFormat(snapshot.language, "dirtySwitchConfirm", { branch: name }))) {
          await switchBranch(name, true);
        }
        return;
      }
      setError(message);
    }
  };

  return <header className="app-titlebar titlebar-region">
    <div className="window-controls" aria-hidden="true"><i /><i /><i /></div>
    <div className="titlebar-project-switch" ref={branchSwitch}>
      <button type="button" className="titlebar-project" aria-label={snapshot.language === "zh-CN" ? "切换分支" : "Switch branch"} aria-haspopup="listbox" aria-expanded={branchOpen} onClick={() => setBranchOpen((open) => !open)}>
        <strong>{project}</strong><b aria-hidden="true">/</b><span>{branch}</span><ChevronDown size={14} />
      </button>
      {branchOpen && <section className="titlebar-project-popover" aria-label={snapshot.language === "zh-CN" ? "切换分支" : "Switch branch"}>
        <header><strong>{snapshot.language === "zh-CN" ? "切换分支" : "Switch branch"}</strong><span>{project}</span></header>
        <label className="titlebar-project-search"><Search size={14} /><input autoFocus value={branchSearch} onChange={(event) => setBranchSearch(event.target.value)} placeholder={`${t("searchBranches")}…`} aria-label={t("searchBranches")} /></label>
        <div className="titlebar-project-options" role="listbox">
          {visibleBranches.map((item) => {
            const currentDetail = workspaceChangedFiles > 0
              ? tFormat(snapshot.language, "uncommittedFiles", { count: workspaceChangedFiles })
              : t("clean");
            return <button key={item.name} type="button" role="option" aria-selected={item.current} onClick={() => void switchBranch(item.name)}>
              <span className="titlebar-project-letter"><GitBranch size={14} /></span>
              <span><strong>{item.name}</strong><small>{item.current ? currentDetail : t("local")}</small></span>
              <em>{item.current ? snapshot.language === "zh-CN" ? "当前" : "Current" : ""}</em>
              <Check size={14} />
            </button>;
          })}
        </div>
        {visibleBranches.length === 0 && <p>{t("noMatchingBranches")}</p>}
        <footer><span>↵ {snapshot.language === "zh-CN" ? "切换" : "Switch"}</span><span>esc {snapshot.language === "zh-CN" ? "关闭" : "Close"}</span></footer>
      </section>}
    </div>
  </header>;
}

function ResizeHandle({ value, setValue, min, max }: { value: number; setValue: (value: number) => void; min: number; max: number }) {
  const onPointerDown = (event: React.PointerEvent) => {
    event.currentTarget.setPointerCapture(event.pointerId);
    const start = event.clientX;
    const initial = value;
    const move = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - start;
      setValue(Math.min(max, Math.max(min, initial + delta)));
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };
  return <div className="resize-handle resize-left" onPointerDown={onPointerDown} />;
}
