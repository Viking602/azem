import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import type { Block } from "../types";
import { TimelineFeed } from "./Timeline";

describe("Codex-style process timeline", () => {
  it("marks only live assistant output as animated and busy", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const live: Block = { id: "live", kind: "assistant", content: "正在输出", state: "streaming" };

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    expect(container.querySelector(".assistant-block.streaming")?.getAttribute("aria-busy")).toBe("true");
    expect(container.querySelector(".stream-cursor")).not.toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [{ ...live, state: "completed" }], language: "zh-CN" })));
    expect(container.querySelector(".stream-cursor")).toBeNull();
    await act(async () => root.unmount());
  });

  it("renders progressive reasoning, highlighted edit diffs, a completed file summary, and a stop separator", async () => {
    const blocks: Block[] = [
      { id: "user", kind: "user", runId: "run-1", content: "调整时间线", state: "submitted" },
      { id: "thinking-1", kind: "thinking", runId: "run-1", content: "先检查现有事件顺序。", state: "completed", data: { elapsedMs: "65000" } },
      {
        id: "edit", kind: "tool", runId: "run-1", title: "coding.edit_hashline", state: "completed",
        data: {
          structured: JSON.stringify({ sections: [{ path: "frontend/src/ThreadSurface.tsx", firstChangedLine: 223, diff: "-const oldValue = read();\n+const nextValue = parse();" }] }),
        },
      },
      { id: "thinking-2", kind: "thinking", runId: "run-1", content: "再验证完成后的展示。", state: "completed" },
      {
        id: "diff", kind: "diff", runId: "run-1", title: "frontend/src/plain.ts", state: "completed",
        content: "@@ -1 +1 @@\n-old\n+new",
      },
      { id: "assistant", kind: "assistant", runId: "run-1", content: "已经完成。", state: "cancelled" },
      { id: "status", kind: "status", runId: "run-1", title: "run_cancelled", state: "cancelled", data: { elapsedMs: "65000" } },
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelectorAll(".reasoning-step")).toHaveLength(2);
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>(".reasoning-summary"))
      .every((summary) => summary.getAttribute("aria-expanded") === "false")).toBe(true);
    expect(container.querySelectorAll(".file-change-entry")).toHaveLength(2);
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>(".file-change-entry"))
      .every((details) => !details.open)).toBe(true);
    expect(container.querySelector(".file-change-entry")?.textContent).toContain("已编辑的文件");
    expect(container.querySelector(".file-change-entry > .code-diff-stack.process-rail-inset")).not.toBeNull();
    expect(container.querySelector(".code-diff tr.deleted")?.textContent).toContain("oldValue");
    expect(container.querySelector(".code-diff tr.added")?.textContent).toContain("nextValue");
    expect(container.querySelector(".syntax-keyword")?.textContent).toBe("const");
    expect(Array.from(container.querySelectorAll(".syntax-function")).map((node) => node.textContent)).toEqual(["read", "parse"]);
    expect(container.querySelector(".edited-files-summary")?.textContent).toContain("已编辑 1 个文件");
    expect(container.querySelector(".edited-files-summary")?.textContent).toContain("frontend/src/ThreadSurface.tsx");
    expect(container.querySelector(".run-status-marker")?.textContent).toBe("你在 1m05s 后停止了");
    expect(container.querySelector<HTMLButtonElement>('.code-diff button[aria-label="复制差异"]')).not.toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("groups settled work without hiding queued and approval-bound tools", async () => {
    const blocks: Block[] = [
      { id: "read-1", kind: "tool", runId: "run-pending", title: "coding.read_file", state: "completed" },
      { id: "read-2", kind: "tool", runId: "run-pending", title: "coding.read_file", state: "completed" },
      {
        id: "write-1", kind: "tool", runId: "run-pending", title: "coding.write_file", state: "awaiting_approval",
        data: { arguments: JSON.stringify({ path: "src/new.go", content: "package main\n" }) },
      },
      { id: "shell-1", kind: "tool", runId: "run-pending", title: "coding.shell", state: "queued" },
      {
        id: "shell-running", kind: "tool", runId: "run-pending", title: "coding.shell", state: "running",
        data: { arguments: JSON.stringify({ command: "bun run build" }), output: "building\n" },
      },
      { id: "search-1", kind: "tool", runId: "run-pending", title: "coding.search", state: "completed" },
      { id: "search-2", kind: "tool", runId: "run-pending", title: "coding.search", state: "completed" },
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run-pending", running: true,
    })));

    expect(container.querySelectorAll(".tool-group")).toHaveLength(2);
    expect(container.querySelectorAll(".process-entries > .tool-block")).toHaveLength(3);
    expect(container.textContent).toContain("需要审批");
    expect(container.textContent).toContain("排队中");
    expect(container.querySelector(".file-change-entry")).toBeNull();
    expect(container.querySelector(".tool-block .spin")).not.toBeNull();
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>(".tool-block, .tool-group"))
      .every((details) => !details.open)).toBe(true);

    await act(async () => root.unmount());
    container.remove();
  });
  it("replaces the model-wait placeholder with live reasoning without reopening a user-collapsed trace", async () => {
    const user: Block = { id: "user", kind: "user", runId: "run-live", content: "检查推理", state: "submitted" };
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user], language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".reasoning-placeholder")?.textContent).toContain("思考中");

    const live: Block = {
      id: "thinking-live", kind: "thinking", runId: "run-live",
      content: "**检查事件管线**\n\n确认 `stream` 和 [事件顺序](https://example.com)。", state: "streaming",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, live], language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    expect(container.querySelector(".reasoning-trace.streaming")?.textContent).toContain("确认 stream 和 事件顺序。");
    expect(container.querySelector(".reasoning-label-sweep")).not.toBeNull();
    expect(container.querySelector(".azem-thinking-mark.active")?.childElementCount).toBe(2);
    expect(container.querySelector(".reasoning-spark")).toBeNull();
    expect(container.querySelector<HTMLDivElement>(".reasoning-body")?.hidden).toBe(true);
    expect(container.querySelector(".reasoning-step strong, .reasoning-step code, .reasoning-step a")).toBeNull();
    expect(container.querySelector(".reasoning-body")?.textContent).toContain("确认 stream 和 事件顺序。");
    const summary = container.querySelector<HTMLButtonElement>(".reasoning-summary")!;
    expect(summary.textContent).toContain("思考中");

    await act(async () => summary.click());
    expect(summary.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector<HTMLDivElement>(".reasoning-body")?.hidden).toBe(false);
    await act(async () => summary.click());
    expect(summary.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector<HTMLDivElement>(".reasoning-body")?.hidden).toBe(true);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...live, content: `${live.content}\n\n**继续验证**` }],
      language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".reasoning-summary")?.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector<HTMLDivElement>(".reasoning-body")?.hidden).toBe(true);
    expect(container.querySelector(".reasoning-body")?.textContent).toContain("继续验证");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...live, state: "completed", data: { elapsedMs: "4200" } }],
      language: "zh-CN", activeRunId: "", running: false,
    })));
    expect(container.querySelector(".reasoning-trace.completed")?.textContent).toContain("思考了 4s");
    expect(container.querySelector(".reasoning-label-sweep")).toBeNull();
    expect(container.querySelector(".azem-thinking-mark.active")).toBeNull();
    expect(container.querySelector(".reasoning-summary")?.getAttribute("aria-expanded")).toBe("false");

    await act(async () => root.unmount());
    container.remove();
  });
  it("renders ANSI command output as styled text without escape glyphs", async () => {
    const ansi = "\u001b[1m\u001b[30m\u001b[46m RUN \u001b[49m\u001b[39m\u001b[22m \u001b[36mv4.1.10\u001b[39m";
    const block: Block = {
      id: "shell-ansi", kind: "tool", runId: "run-ansi", title: "coding.shell", state: "completed",
      content: `{"command":"bun run test"}\n${ansi}`,
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [block], language: "zh-CN" })));

    const result = container.querySelector<HTMLElement>(".tool-result")!;
    expect(result.textContent).toContain("RUN  v4.1.10");
    expect(result.textContent).not.toContain("\u001b");
    expect(container.querySelector(".tool-preview")?.textContent).not.toContain("\u001b");
    expect(Array.from(result.querySelectorAll<HTMLElement>("span")).some((span) => Boolean(span.style.backgroundColor))).toBe(true);

    await act(async () => root.unmount());
  });
});
