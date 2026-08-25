import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { CodeDiff as InstalledCodeDiff } from "../elements/code-diff";
import CodeDiff, { assistantDiffLines } from "./CodeDiff";

it("maps Azem file changes into the installed assistant-ui code diff element", async () => {
  expect(assistantDiffLines("--- a/thread.tsx\n+++ b/thread.tsx\n@@ -1 +1 @@\n-oldValue\n+nextValue\n context")).toEqual([
    { kind: "context", text: "@@ -1 +1 @@" },
    { kind: "removed", text: "oldValue" },
    { kind: "added", text: "nextValue" },
    { kind: "context", text: "context" },
  ]);
  expect(assistantDiffLines("")).toEqual([]);

  const container = document.createElement("div");
  const root = createRoot(container);
  await act(async () => root.render(<CodeDiff
    language="en"
    insetFromProcessRail
    changes={[{
      path: "frontend/src/thread.tsx",
      firstChangedLine: 1,
      diff: "-oldValue\n+nextValue",
      additions: 1,
      deletions: 1,
    }]}
  />));

  expect(container.querySelector('[data-slot="code-diff-stack"]')?.classList.contains("process-rail-inset")).toBe(true);
  const diff = container.querySelector('[data-slot="code-diff"]');
  expect(diff).not.toBeNull();
  expect(diff?.classList.contains("max-w-none")).toBe(true);
  expect(diff?.getAttribute("aria-label")).toContain("frontend/src/thread.tsx");
  expect(diff?.querySelector('[data-kind="removed"]')?.textContent).toContain("oldValue");
  expect(diff?.querySelector('[data-kind="added"]')?.textContent).toContain("nextValue");
  expect(diff?.textContent).toContain("+1");
  expect(diff?.textContent).toContain("−1");
  expect(diff?.querySelector("table")).toBeNull();

  await act(async () => root.render(<InstalledCodeDiff
    filename="motion.ts"
    additions={0}
    deletions={0}
    lines={Array.from({ length: 8 }, (_, index) => ({ kind: "context" as const, text: `line ${index}` }))}
    cycle={1}
  />));
  const animatedRows = Array.from(container.querySelectorAll<HTMLElement>('[data-slot="code-diff"] [data-kind]'));
  expect(animatedRows[0]?.classList.contains("duration-200")).toBe(true);
  expect(animatedRows[0]?.style.animationDelay).toBe("0ms");
  expect(animatedRows[1]?.style.animationDelay).toBe("32ms");
  expect(animatedRows[6]?.style.animationDelay).toBe("192ms");
  expect(animatedRows[7]?.style.animationDelay).toBe("192ms");

  await act(async () => root.unmount());
});
