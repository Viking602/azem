import { create } from "zustand";
import { applyTerminalEvent, type TerminalEvent, type TerminalSession } from "./terminal";

export const DEFAULT_TERMINAL_HEIGHT = 260;
export const MIN_TERMINAL_HEIGHT = 140;

type TerminalStore = {
  open: boolean;
  height: number;
  activeId: string;
  sessions: TerminalSession[];
  error: string;
  closedSessionIds: Record<string, true>;
  lastSequenceBySession: Record<string, number>;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  setHeight: (height: number) => void;
  setActiveId: (id: string) => void;
  setError: (error: string) => void;
  replaceSessions: (sessions: TerminalSession[]) => void;
  removeSession: (id: string) => void;
  applyEvent: (event: TerminalEvent) => boolean;
};

export const useTerminalStore = create<TerminalStore>((set) => ({
  open: false,
  height: DEFAULT_TERMINAL_HEIGHT,
  activeId: "",
  sessions: [],
  error: "",
  setOpen: (open) => set({ open }),
  toggle: () => set((state) => ({ open: !state.open })),
  setHeight: (height) => set((state) => {
    const next = Math.max(MIN_TERMINAL_HEIGHT, Math.round(height));
    return state.height === next ? state : { height: next };
  }),
  setActiveId: (activeId) => set({ activeId }),
  setError: (error) => set({ error }),
  replaceSessions: (sessions) => set((state) => ({
    sessions,
    activeId: state.activeId && sessions.some((session) => session.id === state.activeId)
      ? state.activeId
      : sessions[0]?.id ?? "",
    closedSessionIds: pruneIDs(state.closedSessionIds, new Set(sessions.map((session) => session.id))),
  })),
  removeSession: (id) => set((state) => {
    const sessions = state.sessions.filter((session) => session.id !== id);
    return {
      sessions,
      activeId: state.activeId === id ? sessions[0]?.id ?? "" : state.activeId,
      closedSessionIds: { ...state.closedSessionIds, [id]: true },
      lastSequenceBySession: omitID(state.lastSequenceBySession, id),
    };
  }),
  applyEvent: (event) => {
    let accepted = false;
    set((state) => {
      const id = event.session?.id ?? "";
      const sequence = Number(event.sequence) || 0;
      const lastSequence = id ? state.lastSequenceBySession[id] ?? 0 : 0;
      if (!id || state.closedSessionIds[id] || (sequence > 0 && sequence <= lastSequence)) return {};
      accepted = true;
      const nextSequences = sequence > 0
        ? { ...state.lastSequenceBySession, [id]: sequence }
        : state.lastSequenceBySession;
      // Output floods must never touch session metadata: rerendering every
      // tab per PTY chunk can freeze the renderer. Bytes flow through the
      // output buffer; only session/exit events update the roster.
      if (event.kind === "terminal_output") {
        return { lastSequenceBySession: nextSequences };
      }
      const next = applyTerminalEvent(state.sessions, state.activeId, event);
      return {
        sessions: next.sessions,
        activeId: next.activeId,
        lastSequenceBySession: nextSequences,
      };
    });
    return accepted;
  },
  closedSessionIds: {},
  lastSequenceBySession: {},
}));

function omitID<T>(values: Record<string, T>, id: string) {
  const next = { ...values };
  delete next[id];
  return next;
}

function pruneIDs<T>(values: Record<string, T>, keep: Set<string>) {
  const next: Record<string, T> = {};
  for (const [id, value] of Object.entries(values)) {
    if (!keep.has(id)) next[id] = value;
  }
  return next;
}
