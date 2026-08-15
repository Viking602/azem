import { describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import {
  applyTerminalEvent, createTerminalOutputBuffer, createTerminalWriteQueue, decodeTerminalOutput,
  detectInstalledNerdFont, isTerminalToggleKey, terminalFontFamily, terminalFontStack,
  terminalTabLabel, upsertTerminalSession, TERMINAL_FONT_STACK,
} from "./terminal";
import { useTerminalStore } from "./terminalStore";

const terminalStyles = readFileSync("src/styles/terminal.css", "utf8");

describe("terminal helpers", () => {
  it("toggles on primary+backtick and ignores alt combinations", () => {
    expect(isTerminalToggleKey({ key: "`", metaKey: true, ctrlKey: false, altKey: false })).toBe(true);
    expect(isTerminalToggleKey({ key: "`", metaKey: false, ctrlKey: true, altKey: false })).toBe(true);
    expect(isTerminalToggleKey({ key: "`", metaKey: true, ctrlKey: false, altKey: true })).toBe(false);
    expect(isTerminalToggleKey({ key: "k", metaKey: true, ctrlKey: false, altKey: false })).toBe(false);
  });

  it("decodes base64 PTY bytes without treating them as markdown", () => {
    const bytes = decodeTerminalOutput({ encoding: "base64", data: btoa("azem-term\n") });
    expect(new TextDecoder().decode(bytes)).toBe("azem-term\n");
  });

  it("upserts sessions and marks exit events", () => {
    const first = {
      id: "term-1", title: "zsh", cwd: "/workspace", shell: "zsh", cols: 80, rows: 24, state: "running" as const,
    };
    const sessions = upsertTerminalSession([], first);
    const exited = applyTerminalEvent(sessions, "term-1", {
      sequence: 2, kind: "terminal_exit", session: first, exitCode: 0,
    });
    expect(exited.sessions[0]?.state).toBe("exited");
    expect(exited.activeId).toBe("term-1");
  });

  it("prefers a local Nerd Font before Menlo so Powerline glyphs are not tofu", () => {
    expect(TERMINAL_FONT_STACK).toContain('"MesloLGS NF"');
    expect(TERMINAL_FONT_STACK).toContain('"Hack Nerd Font"');
    expect(TERMINAL_FONT_STACK).toContain('"JetBrainsMono Nerd Font"');
    expect(TERMINAL_FONT_STACK).toMatch(/Menlo.*Monaco.*ui-monospace.*monospace/);
    expect(detectInstalledNerdFont((family) => family === "Hack Nerd Font")).toBe("Hack Nerd Font");
    expect(terminalFontFamily((family) => family === "Hack Nerd Font")).toMatch(/^"Hack Nerd Font"/);
    expect(terminalFontStack()).toBe(TERMINAL_FONT_STACK);
    expect(terminalStyles).toContain("--terminal-mono");
    expect(terminalStyles).toContain("MesloLGS NF");
    expect(terminalStyles).toContain("Hack Nerd Font");
  });

  it("labels a tab with one shell name and the cwd basename", () => {
    expect(terminalTabLabel({ title: "zsh", shell: "zsh", cwd: "/Users/viking/GolandProjects/azem" })).toBe("zsh · azem");
    expect(terminalTabLabel({ title: "zsh · 2", shell: "zsh", cwd: "/Users/viking/GolandProjects/azem" })).toBe("zsh · azem · 2");
    expect(terminalTabLabel({ title: "zsh", shell: "zsh", cwd: "" })).toBe("zsh");
  });

  it("batches output chunks into one sink delivery per scheduled flush", () => {
    const flushes: Array<() => void> = [];
    const buffer = createTerminalOutputBuffer((flush) => { flushes.push(flush); });
    const delivered: string[] = [];
    buffer.attach("term-1", (bytes) => delivered.push(new TextDecoder().decode(bytes)));

    buffer.push("term-1", new TextEncoder().encode("one "));
    buffer.push("term-1", new TextEncoder().encode("two "));
    buffer.push("term-1", new TextEncoder().encode("three"));
    expect(delivered).toEqual([]);
    expect(flushes).toHaveLength(1);
    flushes.shift()!();
    expect(delivered).toEqual(["one two three"]);

    buffer.push("term-1", new TextEncoder().encode("four"));
    flushes.shift()!();
    expect(delivered).toEqual(["one two three", "four"]);
  });

  it("replays a bounded backlog when the sink attaches late and drops the oldest overflow", () => {
    const flushes: Array<() => void> = [];
    const buffer = createTerminalOutputBuffer((flush) => { flushes.push(flush); }, 8);
    buffer.push("term-1", new TextEncoder().encode("aaaa"));
    buffer.push("term-1", new TextEncoder().encode("bbbb"));
    buffer.push("term-1", new TextEncoder().encode("cccc"));

    const delivered: string[] = [];
    buffer.attach("term-1", (bytes) => delivered.push(new TextDecoder().decode(bytes)));
    flushes.shift()!();
    // 12 bytes exceeded the 8-byte backlog: the oldest chunk was dropped.
    expect(delivered).toEqual(["bbbbcccc"]);
  });

  it("discard and detach stop deliveries for a closed session", () => {
    const flushes: Array<() => void> = [];
    const buffer = createTerminalOutputBuffer((flush) => { flushes.push(flush); });
    const delivered: string[] = [];
    const detach = buffer.attach("term-1", (bytes) => delivered.push(new TextDecoder().decode(bytes)));
    buffer.push("term-1", new TextEncoder().encode("late"));
    buffer.discard("term-1");
    detach();
    for (const flush of flushes.splice(0, flushes.length)) flush();
    expect(delivered).toEqual([]);
  });

  it("serializes PTY writes per session and coalesces queued keystrokes", async () => {
    const calls: Array<{ data: string; resolve: () => void }> = [];
    const queue = createTerminalWriteQueue(
      (_id, data) => new Promise<void>((resolve) => calls.push({ data, resolve })),
      () => { throw new Error("unexpected write error"); },
    );
    queue.enqueue("term-1", "a");
    queue.enqueue("term-1", "b");
    queue.enqueue("term-1", "c");
    // Exactly one Bridge write in flight; the rest wait in order.
    expect(calls.map((call) => call.data)).toEqual(["a"]);
    calls[0]!.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(calls.map((call) => call.data)).toEqual(["a", "bc"]);
  });

  it("surfaces a write timeout instead of hanging and drops the stuck backlog", async () => {
    vi.useFakeTimers();
    try {
      const failures: unknown[] = [];
      const queue = createTerminalWriteQueue(
        () => new Promise<void>(() => {}),
        (_id, cause) => failures.push(cause),
        50,
      );
      queue.enqueue("term-1", "stuck");
      queue.enqueue("term-1", "queued-behind");
      await vi.advanceTimersByTimeAsync(60);
      expect(failures).toHaveLength(1);
      expect(String(failures[0])).toContain("timed out");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps the session roster untouched while output events stream", () => {
    useTerminalStore.setState({
      open: true, height: 260, activeId: "", sessions: [], error: "",
      closedSessionIds: {}, lastSequenceBySession: {},
    });
    const session = {
      id: "term-1", title: "zsh", cwd: "/workspace", shell: "zsh", cols: 80, rows: 24, state: "running" as const,
    };
    const store = useTerminalStore.getState();
    expect(store.applyEvent({ sequence: 1, kind: "terminal_session", session })).toBe(true);
    const sessionsAfterUpsert = useTerminalStore.getState().sessions;

    expect(useTerminalStore.getState().applyEvent({
      sequence: 2, kind: "terminal_output", session, encoding: "base64", data: btoa("bytes"),
    })).toBe(true);
    expect(useTerminalStore.getState().sessions).toBe(sessionsAfterUpsert);
    expect(useTerminalStore.getState().lastSequenceBySession["term-1"]).toBe(2);

    // Duplicate output is still deduplicated by sequence.
    expect(useTerminalStore.getState().applyEvent({
      sequence: 2, kind: "terminal_output", session, encoding: "base64", data: btoa("bytes"),
    })).toBe(false);
  });

  it("styles the xterm scrollbar with paper/ink tokens instead of a black bar", () => {
    expect(terminalStyles).toMatch(/\.terminal-xterm \.xterm-viewport\s*\{[^}]*background-color:\s*var\(--paper\)/s);
    expect(terminalStyles).toMatch(/\.terminal-xterm \.xterm-viewport\s*\{[^}]*overflow-x:\s*hidden/s);
    expect(terminalStyles).toMatch(/scrollbar-color:\s*color-mix\(in srgb, var\(--ink\)/s);
    expect(terminalStyles).toMatch(/::-webkit-scrollbar\s*\{[^}]*background:\s*var\(--paper\)/s);
    expect(terminalStyles).not.toMatch(/scrollbar[^;{]*#000|scrollbar[^;{]*\bblack\b/i);
    expect(terminalStyles).not.toMatch(/\.xterm-viewport[^{]*\{[^}]*#000/s);
    expect(terminalStyles).not.toMatch(/\.xterm-viewport[^{]*\{[^}]*\bblack\b/s);
  });
});
