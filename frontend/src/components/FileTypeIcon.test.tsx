import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import FileTypeIcon from "./FileTypeIcon";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

afterEach(async () => {
  await act(async () => root?.unmount());
  container?.remove();
});

describe("FileTypeIcon", () => {
  it("renders bundled vscode-icons for common file types", async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root.render(<>
      <FileTypeIcon path="components/Timeline.test.tsx" />
      <FileTypeIcon path="components/toolTimeline.test.ts" />
      <FileTypeIcon path="styles.css" />
      <FileTypeIcon path="internal/app.go" />
      <FileTypeIcon path="README.md" />
      <FileTypeIcon path="C:\\workspace\\package.json" />
      <FileTypeIcon path="unknown.extension-not-in-the-catalog" />
    </>));

    expect(Array.from(container.querySelectorAll("[data-file-icon]")).map((icon) => icon.getAttribute("data-file-icon"))).toEqual([
      "react-typescript", "typescript", "css", "go", "markdown", "npm",
    ]);
    expect(container.querySelectorAll(".vscode-icon svg")).toHaveLength(6);
    expect(container.querySelector(".file-type-icon.default svg")).not.toBeNull();
  });
});
