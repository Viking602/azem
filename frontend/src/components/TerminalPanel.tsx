import { useEffect, useRef, type CSSProperties } from "react";
import { Eraser, Plus, SquareTerminal, X } from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import {
  closeTerminal, createTerminal, listTerminals, resizeTerminal, subscribeTerminals, writeTerminal,
} from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import {
  createTerminalOutputBuffer, createTerminalWriteQueue, decodeTerminalOutput,
  terminalFontFamily, terminalTabLabel, TERMINAL_FONT_STACK,
  type TerminalEvent, type TerminalSession,
} from "../terminal";
import { MIN_TERMINAL_HEIGHT, useTerminalStore } from "../terminalStore";

function scheduleTerminalFlush(flush: () => void) {
  if (typeof requestAnimationFrame === "function") {
    requestAnimationFrame(() => flush());
    return;
  }
  setTimeout(flush, 16);
}

// One frame-batched output path and one serialized, bounded write path per
// session. Both live at module scope so backlog survives panel remounts.
const terminalOutput = createTerminalOutputBuffer(scheduleTerminalFlush);
const terminalWrites = createTerminalWriteQueue(
  (id, data) => writeTerminal(id, data),
  (_id, cause) => useTerminalStore.getState().setError(cause instanceof Error ? cause.message : String(cause)),
);

function deliverTerminalOutput(event: TerminalEvent) {
  if (event.kind !== "terminal_output") return;
  terminalOutput.push(event.session.id, decodeTerminalOutput(event));
}

export default function TerminalPanel() {
  const snapshot = useRuntimeStore((state) => state.snapshot);
  const open = useTerminalStore((state) => state.open);
  const height = useTerminalStore((state) => state.height);
  const sessions = useTerminalStore((state) => state.sessions);
  const activeId = useTerminalStore((state) => state.activeId);
  const error = useTerminalStore((state) => state.error);
  const language = snapshot?.language ?? "zh-CN";
  const t = translator(language);
  const active = sessions.find((session) => session.id === activeId) ?? sessions[0];

  useEffect(() => {
    const unsubscribe = subscribeTerminals((event) => {
      if (useTerminalStore.getState().applyEvent(event)) deliverTerminalOutput(event);
    });
    return () => {
      unsubscribe();
      terminalOutput.clear();
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void (async () => {
      try {
        const listed = await listTerminals();
        if (cancelled) return;
        if (listed.length > 0) {
          useTerminalStore.getState().replaceSessions(listed);
          return;
        }
        if (useTerminalStore.getState().sessions.length > 0) return;
        const created = await createTerminal(80, 24);
        if (cancelled) return;
        useTerminalStore.getState().applyEvent({ kind: "terminal_session", sequence: 0, session: created });
        useTerminalStore.getState().setActiveId(created.id);
      } catch (cause) {
        if (!cancelled) useTerminalStore.getState().setError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => { cancelled = true; };
  }, [open]);

  const addSession = async () => {
    try {
      const created = await createTerminal(80, 24);
      useTerminalStore.getState().applyEvent({ kind: "terminal_session", sequence: 0, session: created });
      useTerminalStore.getState().setActiveId(created.id);
      useTerminalStore.getState().setError("");
    } catch (cause) {
      useTerminalStore.getState().setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  // Remove the tab immediately and let the backend close converge in the
  // background (TOOL-002 spirit): a wedged shell must not freeze the panel.
  const closeTab = (id: string) => {
    const session = sessions.find((item) => item.id === id);
    terminalOutput.discard(id);
    terminalWrites.discard(id);
    useTerminalStore.getState().removeSession(id);
    if (session?.state === "running") {
      void closeTerminal(id).catch((cause) => {
        useTerminalStore.getState().setError(cause instanceof Error ? cause.message : String(cause));
      });
    }
  };

  const onResizePointer = (event: React.PointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = height;
    const maxHeight = Math.round(window.innerHeight * 0.7);
    const move = (moveEvent: PointerEvent) => {
      useTerminalStore.getState().setHeight(Math.min(maxHeight, startHeight + (startY - moveEvent.clientY)));
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };

  return (
    <section
      className="terminal-panel"
      data-open={String(open)}
      hidden={!open}
      style={{ height, "--terminal-mono": TERMINAL_FONT_STACK } as CSSProperties}
      aria-label={t("terminal")}
    >
      <div className="terminal-resize" role="separator" aria-orientation="horizontal" onPointerDown={onResizePointer} />
      <header className="terminal-toolbar">
        <div className="terminal-tabs" role="tablist" aria-label={t("terminal")}>
          {sessions.map((session) => (
            <div
              key={session.id}
              role="tab"
              tabIndex={0}
              aria-selected={session.id === active?.id}
              data-state={session.state}
              className="terminal-tab"
              onClick={() => useTerminalStore.getState().setActiveId(session.id)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  useTerminalStore.getState().setActiveId(session.id);
                }
              }}
            >
              <SquareTerminal size={13} />
              <span>{terminalTabLabel(session)}</span>
              <button
                type="button"
                className="terminal-tab-close"
                aria-label={t("closeTerminalTab")}
                onClick={(click) => { click.stopPropagation(); closeTab(session.id); }}
              >
                <X size={11} />
              </button>
            </div>
          ))}
          <button type="button" className="terminal-new" aria-label={t("newTerminal")} onClick={() => void addSession()}>
            <Plus size={14} />
          </button>
        </div>
        <p className="terminal-meta">
          <span>{active?.cwd || snapshot?.workspace || ""}</span>
          {active?.state === "exited" && <em>{t("terminalExited")}</em>}
        </p>
        <div className="terminal-toolbar-actions">
          <button type="button" className="terminal-tool" aria-label={t("clearTerminal")} data-clear-terminal="" disabled={!active} onClick={() => document.dispatchEvent(new CustomEvent("azem:terminal-clear", { detail: active?.id }))}>
            <Eraser size={14} />
          </button>
          <button type="button" className="terminal-tool" aria-label={t("closeTerminalPanel")} onClick={() => useTerminalStore.getState().setOpen(false)}>
            <X size={14} />
          </button>
        </div>
      </header>
      {error && <p className="terminal-error" role="alert">{error}</p>}
      <div className="terminal-body" style={{ minHeight: MIN_TERMINAL_HEIGHT - 44 }}>
        {sessions.map((session) => (
          <TerminalView key={session.id} session={session} active={session.id === active?.id} />
        ))}
        {sessions.length === 0 && <div className="terminal-empty">{t("newTerminal")}</div>}
      </div>
    </section>
  );
}

function TerminalView({ session, active }: { session: TerminalSession; active: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const activeRef = useRef(active);
  activeRef.current = active;

  useEffect(() => {
    const node = host.current;
    if (!node) return;
    const addon = new FitAddon();
    const terminal = new Terminal({
      convertEol: false,
      cursorBlink: true,
      customGlyphs: true,
      fontFamily: terminalFontFamily(),
      fontSize: 12,
      lineHeight: 1.35,
      letterSpacing: 0,
      scrollback: 4000,
      theme: terminalTheme(),
    });
    terminal.loadAddon(addon);
    terminal.open(node);
    term.current = terminal;
    fit.current = addon;
    const disposeOutputSink = terminalOutput.attach(session.id, (bytes) => terminal.write(bytes));
    const data = terminal.onData((text) => terminalWrites.enqueue(session.id, text));
    const lastSize = { cols: 0, rows: 0 };
    const resized = terminal.onResize(({ cols, rows }) => {
      if (cols === lastSize.cols && rows === lastSize.rows) return;
      lastSize.cols = cols;
      lastSize.rows = rows;
      void resizeTerminal(session.id, cols, rows);
    });
    const Observer = typeof ResizeObserver === "undefined" ? undefined : ResizeObserver;
    let fitScheduled = false;
    const observer = Observer ? new Observer(() => {
      if (!activeRef.current || node.clientHeight < 20 || fitScheduled) return;
      // Coalesce layout bursts (divider drags) into one fit per frame so a
      // resize storm cannot flood the Bridge with resize calls.
      fitScheduled = true;
      scheduleTerminalFlush(() => {
        fitScheduled = false;
        if (activeRef.current && node.clientHeight >= 20) addon.fit();
      });
    }) : undefined;
    observer?.observe(node);
    return () => {
      observer?.disconnect();
      data.dispose();
      resized.dispose();
      disposeOutputSink();
      terminal.dispose();
      term.current = null;
      fit.current = null;
    };
  }, [session.id]);

  useEffect(() => {
    if (!active) return;
    fit.current?.fit();
    term.current?.focus();
  }, [active]);

  useEffect(() => {
    const onClear = (event: Event) => {
      const id = event instanceof CustomEvent ? event.detail : "";
      if (id === session.id) term.current?.clear();
    };
    document.addEventListener("azem:terminal-clear", onClear);
    return () => document.removeEventListener("azem:terminal-clear", onClear);
  }, [session.id]);

  return <div className="terminal-xterm" data-active={String(active)} ref={host} hidden={!active} />;
}

function terminalTheme() {
  const css = getComputedStyle(document.documentElement);
  const token = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback;
  const paper = token("--paper", "#ffffff");
  const ink = token("--ink", "#171716");
  const muted = token("--muted", "#72726e");
  const faint = token("--faint", "#a6a6a0");
  const accent = token("--accent", "#ef6b3c");
  const red = token("--red", "#d94747");
  const green = token("--green", "#16884b");
  const amber = token("--amber", "#b76d0b");
  const blue = token("--blue", "#3478f6");
  return {
    background: paper,
    foreground: ink,
    cursor: accent,
    cursorAccent: paper,
    selectionBackground: accent + "3d",
    selectionForeground: ink,
    black: ink,
    red,
    green,
    yellow: amber,
    blue,
    magenta: "#7a5cff",
    cyan: blue,
    white: muted,
    brightBlack: faint,
    brightRed: red,
    brightGreen: green,
    brightYellow: amber,
    brightBlue: blue,
    brightMagenta: "#9b87ff",
    brightCyan: blue,
    brightWhite: ink,
  };
}
