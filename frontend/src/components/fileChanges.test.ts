import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import {
  aggregateEditedFiles, fileChangesForBlock, isActiveFileChangeBlock, isPendingFileChangeBlock,
  pendingFileChangeSummaryForBlock, pendingFileEditPaths,
} from "./fileChanges";

describe("file change extraction", () => {
  it("reads structured hashline edits and write-file arguments without raw patch parsing", () => {
    const edit: Block = {
      id: "edit", kind: "tool", title: "coding.edit_hashline", state: "completed",
      data: { structured: JSON.stringify({ sections: [
        { path: "src/app.ts", firstChangedLine: 12, diff: "-const before = 1;\n+const after = 2;" },
        { path: "src/theme.css", firstChangedLine: 4, diff: "+color: red;" },
      ] }) },
    };
    const write: Block = {
      id: "write", kind: "tool", title: "coding.write_file", state: "completed",
      data: { arguments: JSON.stringify({ path: "src/new.go", content: "package main\n\nfunc main() {}\n" }) },
    };

    expect(fileChangesForBlock(edit)).toMatchObject([
      { path: "src/app.ts", firstChangedLine: 12, additions: 1, deletions: 1 },
      { path: "src/theme.css", firstChangedLine: 4, additions: 1, deletions: 0 },
    ]);
    expect(fileChangesForBlock(write)[0]).toMatchObject({ path: "src/new.go", firstChangedLine: 1, additions: 3, deletions: 0 });

    const summary = aggregateEditedFiles([edit, write]);
    expect(summary).toEqual({
      files: [
        { path: "src/app.ts", additions: 1, deletions: 1 },
        { path: "src/theme.css", additions: 1, deletions: 0 },
        { path: "src/new.go", additions: 3, deletions: 0 },
      ],
      additions: 5,
      deletions: 1,
    });
  });

  it("prefers the backend fileChange projection over local payload parsing", () => {
    const block: Block = {
      id: "projected", kind: "tool", title: "coding.edit_hashline", state: "completed",
      data: {
        fileChange: JSON.stringify({
          files: [{ path: "src/app.ts", firstChangedLine: 12, diff: "-old\n+new", additions: 1, deletions: 1 }],
          additions: 1, deletions: 1,
        }),
        // Conflicting local payloads must be ignored when the projection exists.
        structured: JSON.stringify({ sections: [{ path: "other.ts", firstChangedLine: 1, diff: "+wrong" }] }),
      },
    };
    expect(fileChangesForBlock(block)).toEqual([
      { path: "src/app.ts", firstChangedLine: 12, diff: "-old\n+new", additions: 1, deletions: 1 },
    ]);

    const malformed: Block = {
      id: "malformed", kind: "tool", title: "coding.edit_hashline", state: "completed",
      data: {
        fileChange: "{not json",
        structured: JSON.stringify({ sections: [{ path: "fallback.ts", firstChangedLine: 2, diff: "+ok" }] }),
      },
    };
    expect(fileChangesForBlock(malformed)).toMatchObject([{ path: "fallback.ts", additions: 1, deletions: 0 }]);
  });

  it("does not present an unexecuted write as an edited file", () => {
    for (const state of ["queued", "awaiting_approval", "reviewing_approval", "running", "failed", "cancelled", "interrupted"]) {
      const block: Block = {
        id: state,
        kind: "tool",
        title: "coding.write_file",
        state,
        data: { arguments: JSON.stringify({ path: "src/not-written.go", content: "package pending\n" }) },
      };
      expect(fileChangesForBlock(block)).toEqual([]);
    }
  });

  it("summarizes an active hashline edit without marking queued or ambiguous edits as changed", () => {
    const running: Block = {
      id: "running-edit", kind: "tool", title: "coding.edit_hashline", state: "running",
      data: { arguments: JSON.stringify({ input: [
        "¶src/app.ts#ABCD",
        "replace 4:",
        "+const next = 2;",
        "insert after 8:",
        "+line one",
        "+line two",
        "",
        "¶src/theme.ts#1234",
        "delete 2..3",
      ].join("\n") }) },
    };

    expect(pendingFileChangeSummaryForBlock(running)).toEqual({
      files: [
        { path: "src/app.ts", additions: 3, deletions: 1 },
        { path: "src/theme.ts", additions: 0, deletions: 2 },
      ],
      additions: 3,
      deletions: 3,
    });
    expect(pendingFileChangeSummaryForBlock({ ...running, state: "queued" })).toBeNull();
    expect(pendingFileChangeSummaryForBlock({
      ...running,
      data: { arguments: JSON.stringify({ input: "¶src/app.ts#ABCD\nreplace block 4:\n+func next() {}" }) },
    })).toBeNull();
    expect(isActiveFileChangeBlock({
      ...running,
      data: { arguments: JSON.stringify({ input: "¶src/app.ts#ABCD\nreplace block 4:\n+func next() {}" }) },
    })).toBe(true);
    expect(isActiveFileChangeBlock({ ...running, state: "queued" })).toBe(false);
    expect(isActiveFileChangeBlock({
      ...running,
      data: { arguments: JSON.stringify({ input: "¶src/app.ts#ABCD\ndelete 4", dryRun: true }) },
    })).toBe(false);
    expect(isPendingFileChangeBlock({ ...running, state: "reviewing_approval" })).toBe(true);
    expect(pendingFileEditPaths({
      ...running,
      state: "reviewing_approval",
      data: { arguments: JSON.stringify({ input: "¶src/app.ts#ABCD\ndelete 2..3\n+secret body" }) },
    })).toEqual(["src/app.ts"]);
  });

  it("falls back to the compact edit result stored in durable tool output", () => {
    const block: Block = {
      id: "edit", kind: "tool", title: "coding.edit_hashline", state: "completed",
      content: "¶src/main.go#ABCD\nupdated src/main.go\nfirstChangedLine: 8\n\n--- compact diff ---\n-return oldValue\n+return newValue",
    };
    expect(fileChangesForBlock(block)).toEqual([{
      path: "src/main.go", firstChangedLine: 8, diff: "-return oldValue\n+return newValue", additions: 1, deletions: 1,
    }]);
  });
});
