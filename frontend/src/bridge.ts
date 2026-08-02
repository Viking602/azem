import { Call, Events } from "@wailsio/runtime";
import type { ActionRequest, Attachment, RuntimeEvent, Snapshot, TurnRequest } from "./types";

const EVENT_NAME = "azem:event";
const SESSION_MENU_EVENT = "azem:session-menu";
const bridgeName = "github.com/Viking602/azem/internal/desktop.Bridge";

export const isDesktopRuntime = () =>
  typeof window !== "undefined" &&
  (location.protocol === "wails:" || Boolean((window as Window & { _wails?: { environment?: { OS?: string } } })._wails?.environment?.OS));

export const demoSnapshot: Snapshot = {
  workspace: "/workspace/azem",
  sessionId: "session-demo",
  provider: "chatgpt",
  model: "gpt-5.6-sol",
  reasoning: "high",
  agentMode: "single",
  language: "zh-CN",
  approvalMode: "auto_review",
  subagentConcurrency: 2,
  chatgptFastMode: false,
  sequence: 0,
};

export async function initialise(): Promise<Snapshot> {
  if (!isDesktopRuntime()) return demoSnapshot;
  return Call.ByName(`${bridgeName}.Initialise`) as Promise<Snapshot>;
}

export async function startTurn(request: TurnRequest): Promise<string> {
  if (!isDesktopRuntime()) return `demo-${Date.now()}`;
  return Call.ByName(`${bridgeName}.StartTurn`, request) as Promise<string>;
}

export async function execute(request: ActionRequest): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.Execute`, request);
}

export async function cancelActive(includeChildren = false): Promise<boolean> {
  if (!isDesktopRuntime()) return true;
  return Call.ByName(`${bridgeName}.CancelActive`, includeChildren) as Promise<boolean>;
}

export async function guide(sessionId: string, runId: string, text: string): Promise<void> {
  if (!isDesktopRuntime()) return;
  await Call.ByName(`${bridgeName}.Guide`, sessionId, runId, text);
}

export async function importAttachment(sessionId: string, file: File): Promise<Attachment> {
  const encoded = await fileToBase64(file);
  if (!isDesktopRuntime()) {
    return { id: `demo-${Date.now()}`, name: file.name, mimeType: file.type, path: file.name, size: file.size };
  }
  return Call.ByName(`${bridgeName}.ImportAttachment`, sessionId, file.name, file.type, encoded) as Promise<Attachment>;
}

export function subscribe(onEvent: (event: RuntimeEvent) => void): () => void {
  if (!isDesktopRuntime()) return () => undefined;
  return Events.On(EVENT_NAME, (payload: unknown) => {
    const value = payload as Record<string, unknown>;
    onEvent((value.data ?? payload) as RuntimeEvent);
  });
}

export interface SessionMenuEvent {
  action: "rename" | "error";
  sessionId?: string;
  error?: string;
}

export function subscribeSessionMenu(onEvent: (event: SessionMenuEvent) => void): () => void {
  if (!isDesktopRuntime()) return () => undefined;
  return Events.On(SESSION_MENU_EVENT, (payload: unknown) => {
    const value = payload as Record<string, unknown>;
    onEvent((value.data ?? payload) as SessionMenuEvent);
  });
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("attachment read failed"));
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1] ?? "");
    reader.readAsDataURL(file);
  });
}
