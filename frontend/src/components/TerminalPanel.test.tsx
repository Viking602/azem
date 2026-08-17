import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TerminalEvent, TerminalSession } from "../terminal";
import { useTerminalStore } from "../terminalStore";
import { useRuntimeStore } from "../store";
import type { Snapshot } from "../types";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const writes: string[] = [];
const created: TerminalSession[] = [];
const constructed: Array<Record<string, unknown>> = [];
const terminalListeners = new Set<(event: TerminalEvent) => void>();
let terminalSequence = 0;
const emitTerminal = (event: TerminalEvent) => {
  for (const listener of terminalListeners) listener(event);
};

vi.mock("@xterm/xterm", () => {
  class Terminal {
    written: string[] = [];
    onDataHandler: ((data: string) => void) | null = null;
    constructor(options: Record<string, unknown> = {}) { constructed.push(options); }
    open(node: HTMLElement) { node.dataset.xterm = "ready"; }
    write(data: string | Uint8Array) {
      const text = typeof data === "string" ? data : new TextDecoder().decode(data);
      this.written.push(text);
      writes.push(text);
    }
    clear() { this.written.push("<clear>"); writes.push("<clear>"); }
    focus() {}
    dispose() {}
    loadAddon() {}
    onData(handler: (data: string) => void) {
      this.onDataHandler = handler;
      return { dispose() {} };
    }
    onResize() { return { dispose() {} }; }
  }
  return { Terminal };
});

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
    proposeDimensions() { return { cols: 80, rows: 24 }; }
    dispose() {}
  },
}));

vi.mock("../bridge", () => ({
  listTerminals: vi.fn(async () => created.slice()),
  createTerminal: vi.fn(async () => {
    const session: TerminalSession = {
      id: `term-${created.length + 1}`,
      title: created.length ? `zsh · ${created.length + 1}` : "zsh",
      cwd: "/Users/viking/GolandProjects/azem",
      shell: "zsh",
      cols: 80,
      rows: 24,
      state: "running",
    };
    created.push(session);
    terminalSequence += 1;
    emitTerminal({ sequence: terminalSequence, kind: "terminal_session", session });
    terminalSequence += 1;
    emitTerminal({
      sequence: terminalSequence,
      kind: "terminal_output",
      session,
      encoding: "base64",
      data: btoa("boot-output\n"),
    });
    return session;
  }),
  writeTerminal: vi.fn(async () => undefined),
  resizeTerminal: vi.fn(async () => undefined),
  closeTerminal: vi.fn(async () => undefined),
  subscribeTerminals: vi.fn((listener: (event: TerminalEvent) => void) => {
    terminalListeners.add(listener);
    return () => { terminalListeners.delete(listener); };
  }),
}));

const snapshot: Snapshot = {
  workspace: "/Users/viking/GolandProjects/azem",
  sessionId: "session-1",
  provider: "chatgpt",
  model: "gpt-5.6",
  reasoning: "high",
  agentMode: "single",
  language: "zh-CN",
  approvalMode: "auto_review",
  queueMode: "queue",
  subagentConcurrency: 4,
  chatgptFastMode: false,
  sequence: 0,
};

let container: HTMLDivElement | null = null;
let root: Root | null = null;

async function renderPanel() {
  const { default: TerminalPanel } = await import("./TerminalPanel");
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  await act(async () => root?.render(<TerminalPanel />));
  await act(async () => { await Promise.resolve(); });
}

// PTY output is frame-batched before it reaches xterm, so tests must let one
// animation frame elapse before asserting on writes.
const nextFrame = () => new Promise<void>((resolve) => {
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(() => resolve());
  else setTimeout(resolve, 20);
});

describe("TerminalPanel", () => {
  beforeEach(() => {
    writes.length = 0;
    created.length = 0;
    constructed.length = 0;
    terminalSequence = 0;
    terminalListeners.clear();
    useTerminalStore.setState({
      open: false, height: 260, activeId: "", sessions: [], error: "", closedSessionIds: {}, lastSequenceBySession: {},
    });
    useRuntimeStore.setState({ snapshot });
  });

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    container?.remove();
    root = null;
    container = null;
  });

  it("stays closed until toggled and then creates a tab", async () => {
    await renderPanel();
    expect(container?.querySelector(".terminal-panel")?.hasAttribute("hidden")).toBe(true);
    await act(async () => useTerminalStore.getState().setOpen(true));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(container?.querySelector(".terminal-panel")?.getAttribute("data-open")).toBe("true");
    expect(container?.querySelector(".terminal-tab")?.textContent).toContain("zsh · azem");
    expect(container?.querySelector(".terminal-tab")?.textContent).not.toMatch(/zsh\s+zsh/);
    expect(container?.querySelector(".terminal-meta")?.textContent).toContain("/Users/viking/GolandProjects/azem");
    expect(String(constructed[0]?.fontFamily)).toContain("MesloLGS NF");
    expect(String(constructed[0]?.fontFamily)).toContain("Menlo");
  });

  it("replays PTY output that arrives before the xterm view is mounted", async () => {
    await renderPanel();
    await act(async () => useTerminalStore.getState().setOpen(true));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    await act(async () => { await nextFrame(); });
    expect(writes.join("")).toContain("boot-output");
  });

  it("writes mocked PTY output into the xterm instance", async () => {
    await renderPanel();
    await act(async () => useTerminalStore.getState().setOpen(true));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const session = created[0]!;
    await act(async () => {
      emitTerminal({
        sequence: 3,
        kind: "terminal_output",
        session,
        encoding: "base64",
        data: btoa("azem-term\n"),
      });
    });
    await act(async () => { await nextFrame(); });
    expect(writes.join("")).toContain("azem-term");
  });

  it("keeps the session roster identity stable while output streams", async () => {
    await renderPanel();
    await act(async () => useTerminalStore.getState().setOpen(true));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const session = created[0]!;
    const before = useTerminalStore.getState().sessions;
    await act(async () => {
      for (let index = 0; index < 20; index += 1) {
        emitTerminal({
          sequence: 100 + index,
          kind: "terminal_output",
          session,
          encoding: "base64",
          data: btoa(`chunk-${index}\n`),
        });
      }
    });
    // Output floods advance sequences only; tab metadata must not rerender
    // per PTY chunk (runtime blocking fix B1).
    expect(useTerminalStore.getState().sessions).toBe(before);
    await act(async () => { await nextFrame(); });
    expect(writes.join("")).toContain("chunk-19");
  });

  it("closes a tab without leaving it selected", async () => {
    const { closeTerminal } = await import("../bridge");
    await renderPanel();
    await act(async () => useTerminalStore.getState().setOpen(true));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const close = container?.querySelector<HTMLButtonElement>(".terminal-tab-close");
    await act(async () => close?.click());
    expect(closeTerminal).toHaveBeenCalled();
    await act(async () => {
      emitTerminal({
        sequence: 99,
        kind: "terminal_exit",
        session: { ...created[0]!, state: "exited" },
        exitCode: 0,
      });
    });
    expect(useTerminalStore.getState().sessions).toEqual([]);
  });
});
