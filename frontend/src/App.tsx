import { useEffect, useRef, useState } from "react";
import { execute, initialise, isDesktopRuntime, subscribe } from "./bridge";
import CommandPalette from "./components/CommandPalette";
import Inspector from "./components/Inspector";
import Pages from "./components/Pages";
import SettingsDialog from "./components/SettingsDialog";
import Sidebar from "./components/Sidebar";
import ThreadSurface from "./components/ThreadSurface";
import { useRuntimeStore } from "./store";
import type { RuntimeEvent } from "./types";

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
  const settingsOpen = useRuntimeStore((state) => state.settingsOpen);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const commandOpen = useRuntimeStore((state) => state.commandOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const theme = useRuntimeStore((state) => state.theme);
  const [sidebarWidth, setSidebarWidth] = useState(238);
  const [inspectorWidth, setInspectorWidth] = useState(304);
  const queue = useRef<RuntimeEvent[]>([]);
  const frame = useRef(0);

  useEffect(() => {
    const flush = () => {
      frame.current = 0;
      const events = queue.current.splice(0);
      if (events.length) applyEvents(events);
    };
    const unsubscribe = subscribe((event) => {
      queue.current.push(event);
      if (!frame.current) frame.current = requestAnimationFrame(flush);
    });
    initialise()
      .then(async (value) => {
        hydrate(value, !isDesktopRuntime());
        const sessionId = new URLSearchParams(location.search).get("session");
        if (sessionId && isDesktopRuntime()) await execute({ kind: "resume_session", target: sessionId, sessionId });
      })
      .catch((error: unknown) => setError(error instanceof Error ? error.message : String(error)));
    return () => {
      unsubscribe();
      if (frame.current) cancelAnimationFrame(frame.current);
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
    const onKeyDown = (event: KeyboardEvent) => {
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
      } else if (event.key === "Escape" && !settingsOpen && !commandOpen && running) {
        document.querySelector<HTMLButtonElement>("[data-cancel-run]")?.click();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [commandOpen, running, setCommandOpen, setSettingsOpen, settingsOpen]);

  if (!snapshot) return <div className="app-loading"><span className="azem-mark" />正在加载 Azem…</div>;

  const hasContext = blocks.length > 0 || running;
  const showInspector = view === "thread" && hasContext && inspectorOpen;
  return (
    <div className="desktop-shell" data-runtime={String(isDesktopRuntime())} data-platform={navigator.platform} style={{ "--sidebar-width": `${sidebarWidth}px`, "--inspector-width": `${inspectorWidth}px` } as React.CSSProperties}>
      <div className={`workspace-grid ${showInspector ? "with-inspector" : ""}`}>
        <Sidebar />
        <ResizeHandle side="left" value={sidebarWidth} setValue={setSidebarWidth} min={210} max={320} />
        <main className="workspace-main">
          {view === "thread" ? <ThreadSurface /> : <Pages view={view} />}
        </main>
        {showInspector && (
          <>
            <ResizeHandle side="right" value={inspectorWidth} setValue={setInspectorWidth} min={280} max={420} />
            <Inspector />
          </>
        )}
      </div>
      {settingsOpen && <SettingsDialog />}
      {commandOpen && <CommandPalette />}
    </div>
  );
}

function ResizeHandle({ side, value, setValue, min, max }: { side: "left" | "right"; value: number; setValue: (value: number) => void; min: number; max: number }) {
  const onPointerDown = (event: React.PointerEvent) => {
    event.currentTarget.setPointerCapture(event.pointerId);
    const start = event.clientX;
    const initial = value;
    const move = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - start;
      setValue(Math.min(max, Math.max(min, initial + (side === "left" ? delta : -delta))));
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };
  return <div className={`resize-handle resize-${side}`} onPointerDown={onPointerDown} />;
}
