export type TerminalSessionState = "running" | "exited";

export interface TerminalSession {
  id: string;
  title: string;
  cwd: string;
  shell: string;
  cols: number;
  rows: number;
  state: TerminalSessionState;
}

export interface TerminalEvent {
  sequence: number;
  kind: "terminal_session" | "terminal_output" | "terminal_exit";
  session: TerminalSession;
  data?: string;
  encoding?: string;
  exitCode?: number;
  at?: string;
}

export function isTerminalToggleKey(event: Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey"> & { code?: string }) {
  const backtick = event.key === "`" || event.code === "Backquote";
  return (event.metaKey || event.ctrlKey) && !event.altKey && backtick;
}

export function decodeTerminalOutput(event: Pick<TerminalEvent, "data" | "encoding">): Uint8Array {
  if (!event.data) return new Uint8Array();
  if (event.encoding === "base64") {
    const binary = atob(event.data);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return bytes;
  }
  return new TextEncoder().encode(event.data);
}

export function upsertTerminalSession(sessions: TerminalSession[], next: TerminalSession) {
  const index = sessions.findIndex((session) => session.id === next.id);
  if (index < 0) return [...sessions, next];
  const copy = sessions.slice();
  copy[index] = { ...copy[index], ...next };
  return copy;
}

export function applyTerminalEvent(
  sessions: TerminalSession[],
  activeId: string,
  event: TerminalEvent,
): { sessions: TerminalSession[]; activeId: string } {
  const incoming = event.session;
  if (!incoming?.id) return { sessions, activeId };
  const next: TerminalSession = {
    ...incoming,
    state: event.kind === "terminal_exit" ? "exited" : incoming.state || "running",
  };
  const updated = upsertTerminalSession(sessions, next);
  return { sessions: updated, activeId: activeId || next.id };
}

// Output flooding a PTY can freeze the renderer if every chunk becomes its
// own xterm write and React render. The buffer batches decoded bytes per
// session and delivers them once per scheduled flush (one animation frame),
// with a bounded backlog while no sink is attached (UI-002 spirit).
export const TERMINAL_BACKLOG_LIMIT = 512 * 1024;

export interface TerminalOutputBuffer {
  push(id: string, bytes: Uint8Array): void;
  attach(id: string, sink: (bytes: Uint8Array) => void): () => void;
  discard(id: string): void;
  clear(): void;
}

type outputQueue = { chunks: Uint8Array[]; bytes: number };

export function createTerminalOutputBuffer(
  schedule: (flush: () => void) => void,
  backlogLimit = TERMINAL_BACKLOG_LIMIT,
): TerminalOutputBuffer {
  const queues = new Map<string, outputQueue>();
  const sinks = new Map<string, (bytes: Uint8Array) => void>();
  let scheduled = false;

  const flush = () => {
    scheduled = false;
    for (const [id, queue] of [...queues]) {
      const sink = sinks.get(id);
      if (!sink || queue.bytes === 0) continue;
      queues.delete(id);
      sink(concatOutputChunks(queue));
    }
  };
  const request = () => {
    if (scheduled) return;
    scheduled = true;
    schedule(flush);
  };

  return {
    push(id, bytes) {
      if (!id || bytes.length === 0) return;
      const queue = queues.get(id) ?? { chunks: [], bytes: 0 };
      queue.chunks.push(bytes);
      queue.bytes += bytes.length;
      while (queue.bytes > backlogLimit && queue.chunks.length > 1) {
        const dropped = queue.chunks.shift()!;
        queue.bytes -= dropped.length;
      }
      if (queue.bytes > backlogLimit && queue.chunks.length === 1) {
        const chunk = queue.chunks[0]!;
        queue.chunks[0] = chunk.subarray(chunk.length - backlogLimit);
        queue.bytes = backlogLimit;
      }
      queues.set(id, queue);
      if (sinks.has(id)) request();
    },
    attach(id, sink) {
      sinks.set(id, sink);
      if (queues.has(id)) request();
      return () => {
        if (sinks.get(id) === sink) sinks.delete(id);
      };
    },
    discard(id) {
      queues.delete(id);
    },
    clear() {
      queues.clear();
    },
  };
}

function concatOutputChunks(queue: outputQueue): Uint8Array {
  if (queue.chunks.length === 1) return queue.chunks[0]!;
  const merged = new Uint8Array(queue.bytes);
  let offset = 0;
  for (const chunk of queue.chunks) {
    merged.set(chunk, offset);
    offset += chunk.length;
  }
  return merged;
}

// PTY writes can block on the backend when the shell stops reading. The
// queue keeps exactly one Bridge write in flight per session so keystrokes
// stay ordered, later input never piles up as parallel blocked calls, and a
// timed-out write surfaces an error instead of hanging silently.
export const TERMINAL_WRITE_TIMEOUT_MS = 3000;
export const TERMINAL_WRITE_PENDING_LIMIT = 64 * 1024;

export interface TerminalWriteQueue {
  enqueue(id: string, data: string): void;
  discard(id: string): void;
}

export function createTerminalWriteQueue(
  write: (id: string, data: string) => Promise<void>,
  onError: (id: string, cause: unknown) => void,
  timeoutMs = TERMINAL_WRITE_TIMEOUT_MS,
): TerminalWriteQueue {
  const queues = new Map<string, { pending: string; busy: boolean }>();

  const drain = async (id: string) => {
    const queue = queues.get(id);
    if (!queue || queue.busy) return;
    queue.busy = true;
    try {
      while (queue.pending.length > 0 && queues.get(id) === queue) {
        const data = queue.pending;
        queue.pending = "";
        try {
          await promiseWithTimeout(write(id, data), timeoutMs);
        } catch (cause) {
          queue.pending = "";
          onError(id, cause);
          return;
        }
      }
    } finally {
      queue.busy = false;
      if (queue.pending.length === 0 && queues.get(id) === queue) queues.delete(id);
    }
  };

  return {
    enqueue(id, data) {
      if (!id || !data) return;
      const queue = queues.get(id) ?? { pending: "", busy: false };
      if (queue.pending.length + data.length > TERMINAL_WRITE_PENDING_LIMIT) {
        onError(id, new Error("terminal input backlog overflow"));
        return;
      }
      queue.pending += data;
      queues.set(id, queue);
      void drain(id);
    },
    discard(id) {
      queues.delete(id);
    },
  };
}

function promiseWithTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("terminal write timed out")), timeoutMs);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (cause) => {
        clearTimeout(timer);
        reject(cause);
      },
    );
  });
}

export const TERMINAL_NERD_FONTS = [
  "MesloLGS NF",
  "MesloLGS Nerd Font",
  "Hack Nerd Font",
  "JetBrainsMono Nerd Font",
  "FiraCode Nerd Font",
  "SauceCodePro Nerd Font",
] as const;

export const TERMINAL_FALLBACK_FONTS = ["Menlo", "Monaco", "ui-monospace", "monospace"] as const;

export function quoteFontFamily(name: string) {
  return /\s/.test(name) ? `"${name}"` : name;
}

export function terminalFontStack(preferred = "") {
  const names = [preferred, ...TERMINAL_NERD_FONTS, ...TERMINAL_FALLBACK_FONTS]
    .filter((name, index, all) => Boolean(name) && all.indexOf(name) === index);
  return names.map(quoteFontFamily).join(", ");
}

export const TERMINAL_FONT_STACK = terminalFontStack();

export function detectInstalledNerdFont(check?: (family: string) => boolean): string {
  const available = check ?? systemFontAvailable;
  for (const family of TERMINAL_NERD_FONTS) {
    if (available(family)) return family;
  }
  return "";
}

export function terminalFontFamily(check?: (family: string) => boolean) {
  return terminalFontStack(detectInstalledNerdFont(check));
}

function systemFontAvailable(family: string): boolean {
  if (typeof document === "undefined") return false;
  try {
    if (document.fonts?.check(`12px ${quoteFontFamily(family)}`)) return true;
  } catch {
    // WKWebView may reject an unknown family name.
  }
  return canvasFontDiffersFromMonospace(family);
}

function canvasFontDiffersFromMonospace(family: string): boolean {
  if (typeof document === "undefined") return false;
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) return false;
  const sample = "\uE0A0\uE0B0\uF126 Wmmmmmmmmmmlli";
  ctx.font = "72px monospace";
  const fallback = ctx.measureText(sample).width;
  ctx.font = `72px ${quoteFontFamily(family)}, monospace`;
  const measured = ctx.measureText(sample).width;
  return measured > 0 && measured !== fallback;
}

export function pathBasename(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  if (!trimmed) return "";
  const parts = trimmed.split(/[\\/]/);
  return parts[parts.length - 1] ?? "";
}

export function terminalTabLabel(session: Pick<TerminalSession, "title" | "shell" | "cwd">): string {
  const shell = pathBasename(session.shell || session.title || "sh") || "sh";
  const folder = pathBasename(session.cwd);
  const index = (session.title || "").match(/·\s*(\d+)\s*$/)?.[1];
  const parts = [shell];
  if (folder) parts.push(folder);
  if (index && index !== "1") parts.push(index);
  return parts.join(" · ");
}
