import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { execute, initialise, isDesktopRuntime, subscribe, subscribePullRequests } from "./bridge";
import AgentSideChat from "./components/AgentSideChat";
import Inspector from "./components/Inspector";
import Sidebar from "./components/Sidebar";
import ThreadSurface from "./components/ThreadSurface";
import { translator } from "./i18n";
import { useRuntimeStore } from "./store";
import { refreshPullRequestDashboard } from "./pullRequests";
import type { RuntimeEvent } from "./types";

// Secondary surfaces — split out of the main entry so the first paint stays lean.
const CommandPalette = lazy(() => import("./components/CommandPalette"));
const Pages = lazy(() => import("./components/Pages"));
const SubagentsDrawer = lazy(() => import("./components/SubagentsPage"));
const SettingsDialog = lazy(() => import("./components/SettingsDialog"));
const PullRequestPanel = lazy(() => import("./components/PullRequestPanel"));

const STREAM_FRAME_INTERVAL_MS = 32;
const STREAM_EVENT_KINDS = new Set(["text_delta", "thinking_delta"]);
const TERMINAL_EVENT_KINDS = new Set(["run_finished", "run_failed", "run_cancelled"]);

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
  const theme = useRuntimeStore((state) => state.theme);
  const [sidebarWidth, setSidebarWidth] = useState(238);
  const queue = useRef<RuntimeEvent[]>([]);
  const frame = useRef(0);
  const lastFlush = useRef(-Infinity);

  useEffect(() => {
    let workspaceRefreshTimer = 0;
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
      queue.current.push(event);
      if (!frame.current) frame.current = requestAnimationFrame(flush);
      const tool = event.data?.name ?? "";
      if (["run_finished", "run_failed", "run_cancelled"].includes(event.kind) ||
          (event.kind === "tool_finished" && ["coding.edit_hashline", "coding.write_file", "coding.gofmt"].includes(tool))) refreshWorkspace();
    });
    const unsubscribePullRequests = subscribePullRequests((monitor) => useRuntimeStore.getState().updatePullRequestMonitor(monitor));
    window.addEventListener("focus", refreshWorkspace);
    initialise()
      .then(async (value) => {
        hydrate(value, !isDesktopRuntime());
        void refreshPullRequestDashboard();
        const sessionId = new URLSearchParams(location.search).get("session");
        if (sessionId && isDesktopRuntime()) await execute({ kind: "resume_session", target: sessionId, sessionId });
      })
      .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
    return () => {
      unsubscribe();
      unsubscribePullRequests();
      if (frame.current) cancelAnimationFrame(frame.current);
      window.clearTimeout(workspaceRefreshTimer);
      window.removeEventListener("focus", refreshWorkspace);
    };
  }, [applyEvents, hydrate, setError]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("azem:theme", theme);
  }, [theme]);

  useEffect(() => {
    const saved = localStorage.getItem("azem:theme");
    if (saved === "light" || saved === "dark" || saved === "system") useRuntimeStore.getState().setTheme(saved);
  }, []);

  useEffect(() => {
    const preventNativeContextMenu = (event: MouseEvent) => event.preventDefault();
    document.addEventListener("contextmenu", preventNativeContextMenu);
    return () => document.removeEventListener("contextmenu", preventNativeContextMenu);
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return;
      const primary = event.metaKey || event.ctrlKey;
      if (primary && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      } else if (primary && event.key === ",") {
        event.preventDefault();
        setSettingsOpen(true);
      } else if (primary && event.key.toLowerCase() === "l") {
        event.preventDefault();
        document.querySelector<HTMLTextAreaElement>("#azem-composer")?.focus();
      } else if (event.key === "Escape" && !settingsOpen && !commandOpen) {
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
        if (running) document.querySelector<HTMLButtonElement>("[data-cancel-run]")?.click();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [commandOpen, running, setCommandOpen, setSettingsOpen, settingsOpen]);

  if (!snapshot) {
    // Language not hydrated yet — default to zh-CN until snapshot arrives.
    return <div className="app-loading"><span className="azem-mark" />{translator("zh-CN")("loading")}</div>;
  }

  const hasContext = blocks.length > 0 || running;
  const showPullRequest = Boolean(selectedPullRequestNumber);
  const showSideChat = !showPullRequest && (view === "thread" || view === "agents") && Boolean(selectedAgentId);
  const showAgentDrawer = view === "agents" && !selectedAgentId;
  const showInspector = !showPullRequest && view === "thread" && hasContext && inspectorOpen && !showSideChat;
  const layoutMode = showPullRequest ? "pull-request" : showSideChat ? "agent" : showInspector ? "open" : "closed";
  const t = translator(snapshot.language);
  const lazyFallback = <div className="app-loading"><span className="azem-mark" />{t("loading")}</div>;
  return (
    <div className="desktop-shell" data-runtime={String(isDesktopRuntime())} data-platform={navigator.platform} style={{ "--sidebar-width": `${sidebarWidth}px` } as React.CSSProperties}>
      <div className="workspace-grid" data-inspector={layoutMode}>
        <Sidebar />
        <ResizeHandle value={sidebarWidth} setValue={setSidebarWidth} min={210} max={320} />
        <main className="workspace-main">
          {view === "thread" || view === "agents" ? (
            <ThreadSurface />
          ) : (
            <Suspense fallback={lazyFallback}>
              <Pages view={view} />
            </Suspense>
          )}
          {showInspector && <Inspector />}
          {showAgentDrawer && <Suspense fallback={null}>
            <div className="subagents-drawer-layer" onClick={(event) => {
              if (event.target === event.currentTarget) useRuntimeStore.getState().setView("thread");
            }}>
              <SubagentsDrawer />
            </div>
          </Suspense>}
        </main>
        {showSideChat && <AgentSideChat />}
        {showPullRequest && <Suspense fallback={null}><PullRequestPanel /></Suspense>}
      </div>
      {settingsOpen && (
        <Suspense fallback={null}>
          <SettingsDialog />
        </Suspense>
      )}
      {commandOpen && (
        <Suspense fallback={null}>
          <CommandPalette />
        </Suspense>
      )}
    </div>
  );
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
