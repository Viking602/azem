import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionTree } from "../../types";
import { flattenSessionTree, SessionTreePanel } from "./SessionTreePanel";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const tree: SessionTree = {
  sessionId: "s1", rootSessionId: "s1", sourceKind: "native", activeBranch: "main", activeLeafEntryId: "a2",
  roots: [{ entry: { id: "u1", sequence: 1, kind: "user", createdAt: "2026-08-23T00:00:00Z" }, children: [
    { entry: { id: "a1", parentId: "u1", sequence: 2, kind: "assistant", label: "Alternative", createdAt: "2026-08-23T00:00:01Z" } },
    { entry: { id: "a2", parentId: "u1", sequence: 3, kind: "assistant", label: "Current answer", createdAt: "2026-08-23T00:00:02Z" } },
  ] }],
  branches: [{ name: "main", headEntryId: "a2", active: true, createdAt: "2026-08-23T00:00:00Z", updatedAt: "2026-08-23T00:00:02Z" }],
};

let root: Root | null = null;
let container: HTMLDivElement | null = null;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  root = null;
  container = null;
});

async function renderPanel(overrides: Partial<Parameters<typeof SessionTreePanel>[0]> = {}) {
  const props = {
    tree, language: "en" as const, running: false,
    onNavigate: vi.fn(async () => undefined),
    onLabel: vi.fn(async () => undefined),
    onFork: vi.fn(async () => undefined),
    ...overrides,
  };
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  await act(async () => root?.render(<SessionTreePanel {...props} />));
  return props;
}

function fill(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("SessionTreePanel", () => {
  it("flattens active ancestry without pretending the list implements an ARIA tree", async () => {
    expect(flattenSessionTree(tree).map((row) => [row.entry.id, row.depth, row.activePath])).toEqual([
      ["u1", 0, true], ["a1", 1, false], ["a2", 1, true],
    ]);
    await renderPanel();
    expect(container?.querySelector('[role="tree"]')).toBeNull();
    expect(container?.querySelectorAll(".session-tree-list > li")).toHaveLength(3);
    expect(container?.querySelector('[aria-current="step"]')?.textContent).toContain("Current answer");
  });

  it("navigates only to inactive entries and disables navigation while a run is active", async () => {
    const onNavigate = vi.fn(async () => undefined);
    await renderPanel({ onNavigate });
    const alternative = Array.from(container!.querySelectorAll<HTMLButtonElement>(".session-tree-entry")).find((button) => button.textContent?.includes("Alternative"))!;
    await act(async () => alternative.click());
    expect(onNavigate).toHaveBeenCalledWith("a1");
    expect(container?.querySelector<HTMLButtonElement>('[aria-current="step"]')?.disabled).toBe(true);
  });

  it("submits labels and explicit fork targets through native forms", async () => {
    const onLabel = vi.fn(async () => undefined);
    const onFork = vi.fn(async () => undefined);
    await renderPanel({ onLabel, onFork });

    const edit = container!.querySelectorAll<HTMLButtonElement>(".session-tree-edit")[1];
    await act(async () => edit.click());
    const labelInput = container!.querySelector<HTMLInputElement>(".session-tree-label-form input")!;
    await act(async () => fill(labelInput, "Reviewed branch"));
    await act(async () => container!.querySelector<HTMLFormElement>(".session-tree-label-form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(onLabel).toHaveBeenCalledWith("a1", "Reviewed branch");

    await act(async () => container!.querySelector<HTMLButtonElement>(".session-tree-fork > button")!.click());
    const target = container!.querySelector<HTMLInputElement>(".session-tree-fork input")!;
    await act(async () => fill(target, "session-fork"));
    await act(async () => container!.querySelector<HTMLFormElement>(".session-tree-fork form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(onFork).toHaveBeenCalledWith("session-fork", "a2");
  });
});
