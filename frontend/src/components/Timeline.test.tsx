import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useRuntimeStore } from "../store";
import type { AgentState, Block } from "../types";
import { TimelineFeed } from "./Timeline";

/** Live sparkle label, or the settled gray group header. */
function foldLabel(fold: Element | null | undefined) {
  return fold?.querySelector(".process-fold-summary strong")?.textContent
    ?? fold?.querySelector(".reasoning-label-base")?.textContent
    ?? fold?.querySelector(".bui-tool-chip-group-header strong")?.textContent
    ?? null;
}

function foldClock(fold: Element | null | undefined) {
  return fold?.querySelector(".bui-thinking-meta")?.textContent ?? "";
}

function processRule(container: Element | null | undefined) {
  return container?.querySelector("[data-testid='process-status-rule']") ?? null;
}

function ruleClock(container: Element | null | undefined) {
  return processRule(container)?.querySelector(".bui-thinking-meta")?.textContent ?? "";
}

function foldOpen(fold: Element | null | undefined) {
  return fold?.querySelector(".process-fold-summary, .reasoning-summary, .bui-tool-chip-group-header")?.getAttribute("aria-expanded") === "true";
}

async function toggleFold(fold: Element | null | undefined) {
  await act(async () => fold?.querySelector<HTMLButtonElement>(
    ".process-fold-summary, .reasoning-summary, .bui-tool-chip-group-header",
  )?.click());
}

/** Open every collapsed step so its rows can be inspected. */
async function openSteps(container: Element) {
  const processed = Array.from(container.querySelectorAll<HTMLButtonElement>(
    ".process-fold-summary",
  )).filter((bar) => bar.getAttribute("aria-expanded") !== "true");
  for (const bar of processed) await act(async () => bar.click());
  const bars = Array.from(container.querySelectorAll<HTMLButtonElement>(
    ".process-step .reasoning-summary, .process-step .bui-tool-chip-group-header",
  )).filter((bar) => bar.getAttribute("aria-expanded") !== "true");
  for (const bar of bars) await act(async () => bar.click());
}

function siblingsBetween(start: Element | null | undefined, end: Element | null | undefined) {
  const nodes: Element[] = [];
  if (!start || !end || start.parentElement !== end.parentElement) return nodes;
  for (let node = start.nextElementSibling; node && node !== end; node = node.nextElementSibling) {
    nodes.push(node);
  }
  return nodes;
}

async function openThinkingChip(container: Element) {
  const chip = container.querySelector<HTMLDetailsElement>(".thinking-chip");
  await act(async () => {
    if (!chip) return;
    chip.open = true;
    chip.dispatchEvent(new Event("toggle"));
  });
}

/** Every step in a trail: one bar each, rows only inside their own bar. */
function steps(container: Element) {
  return Array.from(container.querySelectorAll<HTMLElement>(".process-step")).map((step) => ({
    element: step,
    state: step.getAttribute("data-state"),
    open: foldOpen(step),
    label: foldLabel(step),
  }));
}

/** The step that is executing, or the wait for the one about to start. */
function liveStep(container: Element) {
  const step = container.querySelector<HTMLElement>('.process-step[data-state="running"]')
    ?? container.querySelector<HTMLElement>(".process-step");
  return {
    state: step?.getAttribute("data-state") ?? null,
    open: foldOpen(step),
    label: foldLabel(step),
  };
}

function runningAgent(id: string, toolCallId: string, description: string): AgentState {
  return {
    id, type: "review", description, parentRunId: "run-delegated", parentToolCallId: toolCallId,
    model: "gpt-5.6-sol", background: false, capabilityMode: "read-only", isolation: "none", cwd: ".",
    activity: "正在审查", warning: "", worktreePath: "", toolCalls: 1, turns: 1, tokensUsed: 1200,
    elapsedMs: 12_000, state: "running", summary: "", preview: "正在审查", previewKind: "commentary",
    previewRunId: `child-${id}`, elapsedObservedAt: Date.now(),
  };
}

describe("Codex-style process timeline", () => {
  it("renders sent image attachments as clickable thumbnails", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const image = "data:image/png;base64,iVBORw==";
    const block: Block = {
      id: "user-image", kind: "user", content: "检查这张图",
      attachments: [{ id: "image-1", name: "screen.png", mimeType: "image/png", path: image, size: 4 }],
    };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [block], language: "zh-CN" })));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    const thumbnail = container.querySelector<HTMLImageElement>('.user-attachments .attachment-preview-image img');
    expect(thumbnail?.getAttribute("src")).toBe(image);
    await act(async () => container.querySelector<HTMLButtonElement>('.attachment-preview-image')?.click());
    expect(document.querySelector('.attachment-lightbox-canvas img')?.getAttribute("src")).toBe(image);
    await act(async () => document.querySelector<HTMLButtonElement>('.attachment-lightbox header button')?.click());

    await act(async () => root.unmount());
    container.remove();
  });

  it("renders background subagent wake notices instead of user bubbles", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const wake: Block = {
      id: "wake-1", kind: "user", state: "subagent_wake", sequence: 12,
      title: "Subagent completion",
      content: "Background subagent results are available.\nreview `child-1` reached failed.",
      data: { tasks: JSON.stringify([{ id: "child-1", type: "review", state: "failed" }]) },
    };
    const legacy: Block = {
      id: "user-1", kind: "user", content: "Background subagent child-1 (review) reached failed.",
    };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [wake, legacy], language: "zh-CN" })));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    const notice = container.querySelector<HTMLElement>(".subagent-wake-notice");
    expect(notice?.textContent).toContain("后台子代理失败");
    expect(notice?.textContent).toContain("review · child-1 · failed");
    expect(notice?.classList.contains("failed")).toBe(true);
    expect(container.querySelectorAll(".user-block")).toHaveLength(1);
    expect(container.querySelector(".user-block")?.textContent).toContain("Background subagent child-1");

    await act(async () => root.unmount());
    container.remove();
  });

  it("promotes parallel subagents into one clickable run card and hides the empty thinking heartbeat", async () => {
    const previousAgents = useRuntimeStore.getState().agents;
    const previousSelection = useRuntimeStore.getState().selectedAgentId;
    const descriptions = ["审查架构与模块边界", "审查安全与系统边界", "评估前端复杂度与性能", "评估测试与工程保障"];
    const agents = descriptions.map((description, index) => runningAgent(`agent-${index}`, `spawn-${index}`, description));
    const blocks: Block[] = [
      {
        id: "progress", kind: "commentary", runId: "run-delegated", title: "progress", state: "completed",
        content: "**并行审阅高风险面**\n覆盖架构、安全、前端与工程保障",
      },
      { id: "empty-thinking", kind: "thinking", runId: "run-delegated", content: "", state: "streaming" },
      ...descriptions.map((description, index): Block => ({
        id: `spawn-${index}`, toolCallId: `spawn-${index}`, kind: "tool", runId: "run-delegated",
        title: "subagent.spawn", state: "completed", data: { arguments: JSON.stringify({ description, subagent_type: "review" }) },
      })),
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    try {
      await act(async () => useRuntimeStore.setState({ agents, selectedAgentId: "" }));
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks, language: "zh-CN", activeRunId: "run-delegated", running: true, waitingForModel: true,
      })));

      const card = container.querySelector<HTMLElement>(".subagent-run-card");
      expect(card?.getAttribute("data-state")).toBe("running");
      expect(card?.textContent).toContain("4 个子智能体协作");
      expect(card?.textContent).toContain("4 个运行中");
      expect(card?.textContent).toContain("0 / 4 已结束");
      expect(card?.querySelector(".subagent-run-mark")).not.toBeNull();
      expect(card?.querySelector(".subagent-run-glyphs")).toBeNull();
      expect(card?.textContent).not.toContain("+2");
      expect(container.querySelector(".commentary-block")?.textContent).toContain("并行审阅高风险面");
      expect(container.querySelectorAll(".timeline-step")).toHaveLength(0);
      expect(container.querySelector(".bui-loading-state")).toBeNull();
      expect(container.querySelector(".reasoning-placeholder")).toBeNull();
      expect(container.querySelector(".reasoning-trace")).toBeNull();
      expect(container.textContent).not.toContain("思考");

      const summary = card?.querySelector<HTMLButtonElement>(".subagent-run-card-summary");
      expect(summary?.getAttribute("aria-expanded")).toBe("true");
      expect(card?.querySelectorAll(".subagent-run-row")).toHaveLength(4);
      expect(card?.querySelector(".subagent-run-live-preview")?.textContent).toBe("正在审查");
      for (const description of descriptions) expect(card?.textContent).toContain(description);

      await act(async () => card?.querySelector<HTMLButtonElement>(".subagent-run-row")?.click());
      expect(useRuntimeStore.getState().selectedAgentId).toBe("agent-0");

      await act(async () => useRuntimeStore.setState({
        agents: agents.map((agent) => ({ ...agent, state: "completed", summary: "审查完成" })),
      }));
      // UI-012: a bar appearing over the step must not remount the trail below it.
      expect(container.querySelectorAll(".subagent-run-card").length).toBe(1);
      expect(container.querySelector(".subagent-run-card")).toBe(card);
      expect(card?.getAttribute("data-state")).toBe("completed");
      expect(card?.textContent).toContain("4 个已结束");
      expect(card?.querySelector<HTMLElement>(".subagent-run-progress > i")?.style.width).toBe("100%");
    } finally {
      await act(async () => root.unmount());
      useRuntimeStore.setState({ agents: previousAgents, selectedAgentId: previousSelection });
      container.remove();
    }
  });

  it("renders planning questions and the latest executable plan as durable cards", async () => {
    const questions = JSON.stringify([{
      id: "scope", header: "范围", question: "选择实现范围",
      options: [
        { label: "完整", description: "包含实现和验证", recommended: true },
        { label: "最小", description: "仅修改核心路径" },
      ],
    }]);
    const blocks: Block[] = [
      { id: "ask-1", kind: "question", userInputId: "ask-1", state: "pending", title: "需要你的选择", data: { questions } },
      { id: "plan-1", kind: "plan", planId: "plan-1", state: "proposed", title: "规划交互", content: "## 实现\n\n接入 `ask` 工具。", data: { version: "2" } },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelector(".planning-question")?.textContent).toContain("选择实现范围");
    expect(container.querySelector(".planning-options button em")?.textContent).toBe("推荐");
    expect(container.querySelector(".plan-review")?.textContent).toContain("计划 v2");
    expect(container.querySelector(".plan-review h2")?.textContent).toBe("实现");
    expect(container.querySelectorAll(".plan-review footer button")).toHaveLength(3);
    await act(async () => root.unmount());
  });

  it("marks live assistant output as busy without rendering a cursor inside Markdown", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const live: Block = { id: "live", kind: "assistant", content: "正在输出", state: "streaming" };

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    expect(container.querySelector(".assistant-block.streaming")?.getAttribute("aria-busy")).toBe("true");
    expect(container.querySelector(".stream-cursor")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [{ ...live, state: "completed" }], language: "zh-CN" })));
    expect(container.querySelector(".stream-cursor")).toBeNull();
    await act(async () => root.unmount());
  });

  it("keeps an active process expanded and only folds it after it finishes", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-10T00:00:00Z"));
    const container = document.createElement("div");
    const root = createRoot(container);
    const progress: Block = {
      id: "progress", kind: "commentary", runId: "child-run", title: "progress", state: "completed",
      content: "**分派专项审查**\n覆盖安全、架构和前端边界",
    };
    const tool: Block = {
      id: "tool", kind: "tool", runId: "child-run", title: "subagent.spawn", state: "running",
      data: { elapsedMs: "2100" },
    };

    try {
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [progress, tool], language: "zh-CN", activeRunId: "child-run", running: true, foldActiveProcess: true,
      })));
      // A pure delegation raises no step bar; the run card is the indicator.
      expect(steps(container)).toEqual([]);
      expect(container.querySelector(".commentary-block")?.textContent).toContain("分派专项审查");
      expect(container.querySelector(".subagent-run-card")).not.toBeNull();

      await act(async () => { vi.advanceTimersByTime(2100); });
      expect(container.querySelector(".commentary-block")?.textContent).toContain("分派专项审查");

      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [progress, { ...tool, state: "completed", data: { elapsedMs: "4300" } }],
        language: "zh-CN", running: false, foldActiveProcess: true,
      })));
      // Delegation is reported by its run card, not by a second bar over it.
      expect(steps(container)).toEqual([]);
      expect(container.querySelector(".subagent-run-card")?.getAttribute("data-state")).toBe("completed");
    } finally {
      await act(async () => root.unmount());
      vi.useRealTimers();
    }
  });

  it("collapses a Subagent process when it settles and keeps the completed trail toggleable", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const progress: Block = {
      id: "child-progress", kind: "commentary", runId: "child", state: "completed",
      content: "**核对边界**\n只保留已验证证据",
    };
    const tool: Block = {
      id: "child-tool", kind: "tool", runId: "child", title: "coding.search", state: "running",
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [progress, tool], language: "zh-CN", activeRunId: "child", running: true,
      foldActiveProcess: true, collapseCompletedProcess: true,
    })));
    expect(liveStep(container).state).toBe("running");
    // The model's own message stays in the transcript; only its work folds.
    expect(container.querySelector(".commentary-block")?.textContent).toContain("核对边界");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [progress, { ...tool, state: "completed" }], language: "zh-CN",
      foldActiveProcess: true, collapseCompletedProcess: true,
    })));
    const process = container.querySelector(".process-fold");
    expect(foldOpen(process)).toBe(false);
    expect(foldLabel(process)).toBe("已处理");
    expect(container.querySelector(".process-step")).toBeNull();
    expect(container.querySelector(".commentary-block")).toBeNull();

    await toggleFold(process);
    expect(foldOpen(process)).toBe(true);
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("1 次工具调用，1 条进度");
    expect(container.querySelector(".commentary-block")?.textContent).toContain("核对边界");
    await act(async () => root.unmount());
  });

  it("keeps the live step list during a run and folds it to 已处理 when the turn completes", async () => {
    const user: Block = { id: "user", kind: "user", content: "分析当前代码", state: "submitted" };
    const c1: Block = {
      id: "c1", kind: "commentary", runId: "run", title: "progress", state: "completed",
      content: "先建立分析计划，再查看仓库结构和关键代码。", textPhase: "commentary",
    };
    const t1: Block = {
      id: "t1", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
      content: JSON.stringify({ path: "README.md" }),
    };
    const c2: Block = {
      id: "c2", kind: "commentary", runId: "run", title: "progress", state: "streaming",
      content: "接下来盘点仓库根目录。", textPhase: "commentary",
    };
    const answer: Block = {
      id: "answer", kind: "assistant", runId: "run", content: "这是 Azem 本体。",
      textPhase: "final_answer", state: "completed",
    };
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, c1, t1, c2], language: "zh-CN",
      activeRunId: "run", running: true, collapseCompletedProcess: true,
    })));
    expect(container.textContent).not.toContain("已处理");
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("1 次工具调用，1 条进度");
    const liveProse = Array.from(container.querySelectorAll(".commentary-block")).map((node) => node.textContent).join("\n");
    expect(liveProse).toContain("先建立分析计划");
    expect(liveProse).toContain("接下来盘点仓库根目录");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...c1 }, { ...t1 }, { ...c2, state: "completed" }, answer],
      language: "zh-CN", collapseCompletedProcess: true,
    })));
    expect(foldLabel(container.querySelector(".process-fold"))).toBe("已处理");
    expect(container.querySelector(".bui-tool-chip-group-header")).toBeNull();
    expect(container.querySelector(".commentary-block")).toBeNull();
    expect(container.querySelector(".assistant-block")?.textContent).toContain("这是 Azem 本体");
    expect(container.querySelector(".assistant-block")?.closest(".process-fold")).toBeNull();

    await toggleFold(container.querySelector(".process-fold"));
    expect(container.querySelector(".commentary-block")?.textContent).toContain("先建立分析计划");
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("1 次工具调用");
    await act(async () => root.unmount());
  });

  it("does not mount folded process entries until the trail is expanded", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const progress: Block = {
      id: "folded-progress", kind: "commentary", runId: "child", state: "completed",
      content: "**已完成核对**\n只在展开后出现",
    };
    const tool: Block = {
      id: "folded-tool", kind: "tool", runId: "child", title: "coding.search", state: "completed",
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [progress, tool], language: "zh-CN", collapseCompletedProcess: true,
    })));
    const process = container.querySelector(".process-fold");
    expect(foldLabel(process)).toBe("已处理");
    expect(foldOpen(process)).toBe(false);
    expect(process?.querySelector(".process-entries")).toBeNull();
    expect(container.querySelector(".timeline-step")).toBeNull();

    await toggleFold(process);
    expect(foldOpen(process)).toBe(true);
    expect(process?.querySelector(".process-entries")).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("does not mount thinking paragraphs when opening a settled count row", async () => {
    const thinking: Block = {
      id: "think-heavy", kind: "thinking", runId: "run-heavy", state: "completed",
      content: Array.from({ length: 40 }, (_, index) => `段落 ${index + 1} 的审查说明。`).join("\n\n"),
    };
    const tool: Block = {
      id: "read-heavy", kind: "tool", runId: "run-heavy", title: "coding.read_file", state: "completed",
      content: JSON.stringify({ path: "docs/security.md" }),
    };
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, tool], language: "zh-CN", collapseCompletedProcess: true,
    })));
    await toggleFold(container.querySelector(".process-fold"));
    await toggleFold(container.querySelector(".process-step"));
    expect(foldLabel(container.querySelector(".process-step"))).toBe("1 次工具调用");
    expect(container.querySelector(".thinking-chip")).not.toBeNull();
    expect(container.querySelectorAll(".reasoning-step")).toHaveLength(0);
    expect(container.querySelector(".thinking-chip .bui-thinking-panel")).toBeNull();

    await openThinkingChip(container);
    expect(container.querySelectorAll(".reasoning-step").length).toBeGreaterThan(20);
    await act(async () => root.unmount());
  });

  it("expands a large completed fold with chip headers instead of every tool body", async () => {
    const blocks: Block[] = [];
    for (let index = 0; index < 8; index += 1) {
      if (index === 0) {
        blocks.push({
          id: "progress-0", kind: "commentary", runId: "run", state: "completed",
          content: "**进度 0**\n说明 0",
        });
      }
      for (let offset = 0; offset < 8; offset += 1) {
        const n = index * 8 + offset;
        blocks.push(n === 0
          ? {
            id: `edit-${n}`, kind: "tool", runId: "run", title: "coding.edit_hashline", state: "completed",
            data: {
              structured: JSON.stringify({
                sections: [{ path: "src/heavy.ts", firstChangedLine: 1, diff: "-oldValue\n+newValue" }],
              }),
            },
          }
          : {
            id: `tool-${n}`, kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
            content: JSON.stringify({ path: `src/file-${n}.ts` }),
          });
      }
    }
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", collapseCompletedProcess: true,
    })));

    const fold = container.querySelector(".process-fold");
    expect(foldLabel(fold)).toBe("已处理");
    expect(foldOpen(fold)).toBe(false);
    expect(container.querySelector(".timeline-step")).toBeNull();
    await toggleFold(fold);
    const process = container.querySelector(".process-step");
    expect(foldLabel(process)).toBe("64 次工具调用，1 条进度");
    await toggleFold(process);
    const list = process?.querySelector('[data-testid="deferred-process-list"]');
    // The model's message heads the step from outside, so the windowed body
    // carries tool rows only.
    expect(list?.querySelectorAll('[data-deferred-row="progress"]')).toHaveLength(0);
    expect(container.querySelector(".model-progress-prose")?.textContent).toContain("进度 0");
    expect(list?.querySelectorAll('[data-deferred-row="tool"]').length).toBeGreaterThan(0);
    expect(list?.querySelectorAll('[data-deferred-row="tool"]').length).toBeLessThan(64);
    expect(list?.querySelector(".commentary-block")).toBeNull();
    expect(list?.querySelector(".code-diff-stack")).toBeNull();
    expect(list?.querySelector(".tool-result, .tool-detail, .timeline-step-detail")).toBeNull();
    expect(list?.querySelector(".bui-thinking-panel, .reasoning-body")).toBeNull();

    const fileChange = list?.querySelector<HTMLDetailsElement>(".file-change-entry");
    await act(async () => {
      if (!fileChange) return;
      fileChange.open = true;
      fileChange.dispatchEvent(new Event("toggle"));
    });
    expect(list?.querySelectorAll(".code-diff-stack")).toHaveLength(1);
    expect(list?.querySelector(".code-diff tr.added")?.textContent).toContain("newValue");
    expect(list?.querySelector(".commentary-block")).toBeNull();

    const readTool = Array.from(list?.querySelectorAll<HTMLDetailsElement>('[data-deferred-row="tool"] details') ?? [])
      .find((node) => !node.classList.contains("file-change-entry"));
    await act(async () => {
      if (!readTool) return;
      readTool.open = true;
      readTool.dispatchEvent(new Event("toggle"));
    });
    expect(list?.querySelectorAll(".tool-detail, .timeline-step-detail")).toHaveLength(1);
    expect(list?.querySelectorAll(".code-diff-stack")).toHaveLength(1);
    expect(list?.querySelectorAll(".commentary-block")).toHaveLength(0);

    await act(async () => root.unmount());
  });

  it("does not defer or window an active process trail so the running row stays mounted", async () => {
    const blocks: Block[] = Array.from({ length: 40 }, (_, index) => ({
      id: `tool-${index}`, kind: "tool", runId: "run", title: "coding.read_file",
      state: index === 39 ? "running" : "completed",
      content: JSON.stringify({ path: `src/file-${index}.ts` }),
    }));
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run", running: true,
    })));

    await openSteps(container);

    expect(liveStep(container).state).toBe("running");
    expect(container.querySelector(".process-entries")?.getAttribute("data-deferred")).toBeNull();
    expect(container.querySelector('[data-testid="thinking-header"]')?.textContent).toContain("读取文件");
    expect(container.querySelector('.bui-tool-chip[data-state="running"], .timeline-step[data-state="running"]')).not.toBeNull();
    expect(container.querySelectorAll(".bui-tool-chip, .timeline-step").length).toBeGreaterThan(30);
    expect(container.querySelector(".code-diff-stack, .commentary-block")).toBeNull();

    await act(async () => root.unmount());
  });

  it("restores a running progress timer from durable timestamps after switching sessions", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-10T04:31:35Z"));
    const container = document.createElement("div");
    const root = createRoot(container);
    const startedAt = Date.parse("2026-08-10T04:30:00Z");
    const progress: Block = {
      id: "progress", kind: "commentary", runId: "run", title: "progress", state: "completed",
      content: "**并行审阅高风险面**\n等待子智能体返回",
      data: { startedAt: String(startedAt), completedAt: String(startedAt + 3_000), elapsedMs: "3000" },
    };
    const spawn: Block = {
      id: "spawn", kind: "tool", runId: "run", title: "subagent.spawn", state: "running",
      data: { startedAt: String(startedAt + 3_000) },
    };

    try {
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [progress, spawn], language: "zh-CN", activeRunId: "run", running: true,
      })));
      expect(container.querySelector(".model-progress-prose .commentary-block")?.textContent).toContain("并行审阅高风险面");
      expect(container.querySelector(".subagent-run-card")?.getAttribute("data-state")).toBe("running");

      await act(async () => { vi.advanceTimersByTime(2_100); });
      expect(container.querySelector(".subagent-run-card")?.getAttribute("data-state")).toBe("running");
    } finally {
      await act(async () => root.unmount());
      vi.useRealTimers();
    }
  });

  it("keeps the streaming final answer outside the process rail without remounting it on completion", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const thinking: Block = { id: "thinking", kind: "thinking", runId: "run", content: "检查完成", state: "completed" };
    const answer: Block = { id: "answer", kind: "assistant", runId: "run", content: "最终正文", textPhase: "final_answer", state: "streaming" };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, answer], language: "zh-CN", activeRunId: "run", running: true, collapseCompletedProcess: true,
    })));
    const streamingNode = container.querySelector('[data-testid="timeline-prose"]');
    const thinkingHeader = container.querySelector('[data-testid="thinking-header"]');
    expect(streamingNode).not.toBeNull();
    expect(thinkingHeader).not.toBeNull();
    expect(streamingNode?.closest(".process-entries")).toBeNull();
    expect(streamingNode?.closest(".process-fold")).toBeNull();
    expect(container.textContent).not.toContain("已处理");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, { ...answer, state: "completed" }], language: "zh-CN", collapseCompletedProcess: true,
    })));
    expect(container.querySelector('[data-testid="timeline-prose"]')).toBe(streamingNode);
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(thinkingHeader);
    expect(container.querySelector(".assistant-block")?.closest(".process-fold")).toBeNull();
    expect(container.textContent).not.toContain("已处理");
    expect(container.querySelector(".reasoning-trace")?.textContent).toContain("思考");
    await act(async () => root.unmount());
  });

  it("does not inject 已处理 above a no-tool streamed answer when commentary settles", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run", content: "组织自我介绍", state: "completed",
      data: { elapsedMs: "1900" },
    };
    const commentary: Block = {
      id: "progress", kind: "commentary", runId: "run", title: "progress", state: "streaming",
      content: "我是 Azem，负责在这个工作区里回答问题。",
    };
    const answer: Block = {
      id: "answer", kind: "assistant", runId: "run", content: "你可以直接说想做什么。",
      textPhase: "final_answer", state: "streaming",
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, commentary, answer], language: "zh-CN",
      activeRunId: "run", running: true, collapseCompletedProcess: true,
    })));
    const prose = container.querySelector('[data-testid="timeline-prose"]');
    const header = container.querySelector('[data-testid="thinking-header"]');
    expect(prose?.textContent).toContain("你可以直接说想做什么");
    expect(foldLabel(header)).toBe("思考");
    expect(foldClock(header)).toBe("");
    expect(processRule(container)?.textContent).toContain("正在处理");
    expect(ruleClock(container)).toBe("1.9s");
    expect(container.querySelector(".process-fold")?.getAttribute("data-step")).toBe("reasoning");
    expect(container.querySelector(".assistant-block")?.closest(".process-fold")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [
        thinking,
        { ...commentary, state: "completed" },
        { ...answer, state: "completed" },
      ],
      language: "zh-CN", collapseCompletedProcess: true,
    })));
    expect(container.querySelector('[data-testid="timeline-prose"]')).toBe(prose);
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(header);
    expect(container.querySelector(".assistant-block")?.closest(".process-fold")).toBeNull();
    expect(container.textContent).not.toContain("已处理");
    expect(processRule(container)).toBeNull();
    expect(foldLabel(container.querySelector(".reasoning-trace.completed"))).toBe("思考");
    expect(foldClock(container.querySelector(".reasoning-trace.completed"))).toBe("1.9s");
    await act(async () => root.unmount());
  });

  it("shows the newest completed process as the conversation context while older work stays folded", async () => {
    const blocks: Block[] = [
      { id: "old-thinking", kind: "thinking", runId: "old-run", content: "旧过程", state: "completed" },
      { id: "old-tool", kind: "tool", runId: "old-run", title: "coding.search", state: "completed" },
      { id: "old-answer", kind: "assistant", runId: "old-run", content: "旧回答", textPhase: "final_answer", state: "completed" },
      { id: "new-thinking", kind: "thinking", runId: "new-run", content: "新过程", state: "completed", data: { elapsedMs: "2300" } },
      { id: "new-tool", kind: "tool", runId: "new-run", title: "coding.read_file", state: "completed" },
      { id: "new-answer", kind: "assistant", runId: "new-run", content: "新回答", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    await openSteps(container);

    const trails = Array.from(container.querySelectorAll(".process-fold"));
    expect(trails).toHaveLength(2);
    // Every step is opened on demand, and an opened one stays opened.
    expect(steps(container).map((step) => step.open)).toEqual([true, true]);
    expect(trails[1]?.querySelector(".process-step-body")?.textContent).toContain("新过程");
    expect(Array.from(container.querySelectorAll(".final-answer-marker")).map((node) => node.textContent))
      .toEqual(["最终回答", "最终回答"]);

    await toggleFold(trails[1]?.querySelector(".process-step"));
    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));
    expect(steps(container).map((step) => step.open)).toEqual([true, false]);

    await act(async () => root.unmount());
  });

  it("does not promote ambiguous live text or duplicate an explicit answer section", async () => {
    const blocks: Block[] = [
      { id: "thinking", kind: "thinking", runId: "run", content: "检查", state: "completed" },
      { id: "pending", kind: "assistant", runId: "run", content: "待确认", textPhase: "final_answer", state: "streaming", data: { textPhasePending: "true" } },
      { id: "section", kind: "status", runId: "run", title: "方案结论", state: "ready", data: { variant: "section" } },
      { id: "answer", kind: "assistant", runId: "run", content: "完成", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelectorAll(".section-marker")).toHaveLength(1);
    expect(container.querySelector(".section-marker")?.textContent).toBe("方案结论");
    await act(async () => root.unmount());
  });

  it("renders unphased live text as ordinary body prose instead of a pending card", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const pending: Block = {
      id: "pending-phase", kind: "assistant", runId: "run", content: "最后确认测试和 diff。",
      textPhase: "final_answer", state: "streaming", data: { textPhasePending: "true" },
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [pending], language: "zh-CN", activeRunId: "run", running: true,
    })));
    const prose = container.querySelector('[data-testid="timeline-prose"]');
    expect(prose?.classList.contains("assistant-block")).toBe(true);
    expect(prose?.classList.contains("streaming")).toBe(true);
    expect(prose?.classList.contains("phase-pending")).toBe(false);
    expect(container.querySelector(".assistant-block.phase-pending")).toBeNull();
    expect(prose?.querySelector(".streaming-text p")?.textContent).toBe("最后确认测试和 diff。");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...pending, state: "completed" }], language: "zh-CN",
    })));
    expect(container.querySelector(".assistant-block.phase-pending")).toBeNull();
    expect(container.querySelector(".assistant-block.timeline-prose")?.textContent).toContain("最后确认测试和 diff。");
    await act(async () => root.unmount());
  });

  it("keeps generic progress labels out of the visual process trail", async () => {
    const blocks: Block[] = [
      { id: "progress-en", kind: "commentary", title: "progress", content: "读取当前状态。", state: "completed" },
      { id: "progress-zh", kind: "commentary", title: "进度更新", content: "继续核对样式。", state: "completed" },
      { id: "progress-specific", kind: "commentary", title: "视觉核对", content: "保留有意义的阶段标题。", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelector(".commentary-label")).toBeNull();
    expect(container.textContent).toContain("读取当前状态。");
    expect(container.textContent).toContain("继续核对样式。");
    expect(container.textContent).toContain("保留有意义的阶段标题。");
    expect(container.textContent).not.toContain("progress");
    expect(container.textContent).not.toContain("进度更新");
    await act(async () => root.unmount());
  });

  it("hides host fallback commentary and keeps tools grouped under it", async () => {
    const blocks: Block[] = [
      {
        id: "fallback", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "正在调用所需工具，并根据实际结果继续。",
        textPhase: "commentary",
        data: { synthetic: "tool_announcement" },
      },
      {
        id: "read", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
        content: "{\"path\":\"README.md\"}",
      },
      {
        id: "legacy", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "正在调用所需工具，并根据实际结果继续。",
      },
      {
        id: "search", kind: "tool", runId: "run", title: "coding.search", state: "completed",
        content: JSON.stringify({ query: "main" }),
      },
      {
        id: "real", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "我先核对入口，再根据结果继续。",
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    await openSteps(container);

    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");
    expect(container.textContent).toContain("我先核对入口，再根据结果继续。");
    expect(container.querySelector(".commentary-block")?.textContent).toContain("我先核对入口，再根据结果继续。");
    expect(container.querySelectorAll(".commentary-block")).toHaveLength(1);
    expect(container.querySelectorAll('[data-host-fallback="true"]')).toHaveLength(0);
    expect(container.textContent).toContain("读取文件");
    expect(container.textContent).toContain("搜索代码");
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelector(".bui-thinking-stack")).toBeNull();
    expect(container.querySelector(".timeline-step-list, .bui-tool-chip")).not.toBeNull();
    expect(container.querySelector(".model-progress-step")).toBeNull();

    await act(async () => root.unmount());
  });

  it("shows the thinking wait pill after a finished tool while the run is still live", async () => {
    const user: Block = { id: "user", kind: "user", content: "继续修等待态", state: "submitted" };
    const fallback: Block = {
      id: "fallback", kind: "commentary", runId: "run-live", title: "progress", state: "completed",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    };
    const failed: Block = {
      id: "shell", kind: "tool", runId: "run-live", title: "coding.shell", state: "failed",
      content: JSON.stringify({ command: "rg -n 'session-turn-label'" }),
    };
    const emptyThinking: Block = {
      id: "empty-think", kind: "thinking", runId: "run-live", content: "", state: "streaming",
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, fallback, failed, emptyThinking],
      language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
      collapseCompletedProcess: true,
    })));

    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");
    expect(container.textContent).not.toContain("我准备");
    // UI-016: the wait rides the step's own bar. A second detached pill below
    // the finished tools is what made the step look like two separate things.
    expect(liveStep(container)).toEqual({ state: "running", open: false, label: "思考" });
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    await openSteps(container);
    expect(container.textContent).toContain("失败");
    const bar = container.querySelector(".process-fold .bui-thinking-state.streaming");
    expect(bar).not.toBeNull();
    expect(bar?.querySelector(".bui-thinking-pill")).toBeNull();
    expect(bar?.querySelector(".reasoning-chevron")).not.toBeNull();
    const waitHeader = container.querySelector('[data-testid="thinking-header"]');

    const liveThinking: Block = {
      id: "think-live", kind: "thinking", runId: "run-live", content: "先看失败原因", state: "streaming",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, fallback, failed, liveThinking],
      language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
      collapseCompletedProcess: true,
    })));
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(waitHeader);
    expect(container.textContent).toContain("先看失败原因");
    expect(container.textContent).toContain("失败");

    const commentary: Block = {
      id: "next", kind: "commentary", runId: "run-live", title: "progress", state: "streaming",
      content: "我先换一条更窄的搜索。",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, fallback, failed, { ...liveThinking, state: "completed" }, commentary],
      language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
      collapseCompletedProcess: true,
    })));
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    expect(container.querySelector(".commentary-block")?.textContent).toContain("我先换一条更窄的搜索。");
    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");

    await act(async () => root.unmount());
  });

  it("settles tools as a count row as soon as the next commentary is ordinary prose", async () => {
    const user: Block = { id: "user", kind: "user", content: "核对约定", state: "submitted" };
    const plan: Block = {
      id: "c1", kind: "commentary", runId: "run-prose", title: "progress", state: "completed",
      content: "先摸清仓库再决定读哪些入口。", textPhase: "commentary",
    };
    const tools: Block[] = [
      { id: "r1", kind: "tool", runId: "run-prose", title: "coding.read_file", state: "completed", content: JSON.stringify({ path: "a.ts" }) },
      { id: "r2", kind: "tool", runId: "run-prose", title: "coding.read_file", state: "completed", content: JSON.stringify({ path: "b.ts" }) },
      { id: "r3", kind: "tool", runId: "run-prose", title: "coding.read_file", state: "completed", content: JSON.stringify({ path: "c.ts" }) },
      { id: "r4", kind: "tool", runId: "run-prose", title: "coding.read_file", state: "completed", content: JSON.stringify({ path: "d.ts" }) },
      { id: "r5", kind: "tool", runId: "run-prose", title: "coding.search", state: "completed", content: JSON.stringify({ query: "idle" }) },
      { id: "r6", kind: "tool", runId: "run-prose", title: "coding.search", state: "completed", content: JSON.stringify({ query: "fence" }) },
    ];
    const next: Block = {
      id: "c2", kind: "commentary", runId: "run-prose", title: "progress", state: "streaming",
      content: "工作区很脏，我会只读不改。接下来跑审计信号脚本，并继续核对架构与约定。",
      textPhase: "commentary",
    };
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, plan, ...tools, next],
      language: "zh-CN", activeRunId: "run-prose", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("6 次工具调用，1 条进度");
    expect(Array.from(container.querySelectorAll(".commentary-block")).map((node) => node.textContent).join("\n"))
      .toContain("工作区很脏，我会只读不改");
    expect(container.textContent).not.toContain("运行了 6 个工具");
    expect(container.querySelector(".process-step .reasoning-label")?.textContent ?? "").not.toContain("运行了");
    const header = container.querySelector(".bui-tool-chip-group-header");
    const nextProse = Array.from(container.querySelectorAll(".commentary-block"))
      .find((node) => node.textContent?.includes("工作区很脏"));
    expect(header).not.toBeNull();
    expect(nextProse).not.toBeNull();
    const between = siblingsBetween(header?.closest(".process-step"), nextProse?.closest(".model-progress-prose, .commentary-block"));
    expect(between.some((node) => node.matches(".process-step, .reasoning-trace, .process-step-body"))).toBe(false);
    await act(async () => root.unmount());
  });

  it("does not collapse the current live step into a count row between tool batches", async () => {
    const user: Block = { id: "user", kind: "user", content: "对照实现", state: "submitted" };
    const commentary: Block = {
      id: "c1", kind: "commentary", runId: "run-hold", title: "progress", state: "completed",
      content: "结构已经够用，我继续读入口。", textPhase: "commentary",
    };
    const read: Block = {
      id: "read", kind: "tool", runId: "run-hold", title: "coding.read_file", state: "completed",
      content: JSON.stringify({ path: "README.md" }),
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, commentary, read],
      language: "zh-CN", activeRunId: "run-hold", running: true, waitingForModel: true,
    })));
    const header = container.querySelector('[data-testid="thinking-header"]');
    expect(liveStep(container)).toEqual({ state: "running", open: false, label: "思考" });
    expect(container.querySelector(".bui-tool-chip-group-header")).toBeNull();
    expect(container.textContent).not.toContain("1 次工具调用");

    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run-hold", state: "streaming",
      content: "先看入口再核对约束",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, commentary, read, thinking],
      language: "zh-CN", activeRunId: "run-hold", running: true, waitingForModel: true,
    })));
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(header);
    expect(liveStep(container)).toEqual({ state: "running", open: false, label: "思考" });
    expect(container.querySelector(".bui-tool-chip-group-header")).toBeNull();

    const next: Block = {
      id: "c2", kind: "commentary", runId: "run-hold", title: "progress", state: "completed",
      content: "入口清楚了，继续核对运行时。", textPhase: "commentary",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, commentary, read, { ...thinking, state: "completed" }, next],
      language: "zh-CN", activeRunId: "run-hold", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("1 次工具调用，1 条进度");
    expect(liveStep(container)).toEqual({ state: "running", open: false, label: "思考" });

    await act(async () => root.unmount());
  });

  it("keeps the first-token wait pill when only host fallback commentary exists", async () => {
    const user: Block = { id: "user", kind: "user", content: "继续", state: "submitted" };
    const fallback: Block = {
      id: "fallback", kind: "commentary", runId: "run-live", title: "progress", state: "streaming",
      content: "正在调用所需工具，并根据实际结果继续。",
      data: { synthetic: "tool_announcement" },
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, fallback], language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));

    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");
    expect(liveStep(container)).toEqual({ state: "running", open: false, label: "思考" });

    await act(async () => root.unmount());
  });

  it("renders one model commentary and a process chip list for a multi-span tool group", async () => {
    const blocks: Block[] = [
      {
        id: "progress", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "我准备先查块高度是从哪算出来的。", textPhase: "commentary",
      },
      {
        id: "t1", kind: "thinking", runId: "run", content: "看 processElapsedMs", state: "completed",
        data: { elapsedMs: "1200" },
      },
      {
        id: "t2", kind: "thinking", runId: "run", content: "再对一下调用点", state: "completed",
        data: { elapsedMs: "1300" },
      },
      {
        id: "read", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
        content: JSON.stringify({ path: "frontend/src/components/toolTimeline.ts" }),
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    await openSteps(container);

    expect(container.querySelectorAll(".commentary-block")).toHaveLength(1);
    expect(container.querySelector(".commentary-block")?.textContent).toContain("我准备先查块高度是从哪算出来的。");
    expect(container.querySelector(".commentary-block")?.textContent).not.toContain("看 processElapsedMs");
    expect(container.querySelector(".bui-thinking-stack")).toBeNull();
    expect(container.querySelector(".thinking-chip")).not.toBeNull();
    expect(container.querySelector(".thinking-chip .bui-thinking-panel")).toBeNull();
    await openThinkingChip(container);
    expect(container.querySelector('.bui-thinking-panel[data-tab="reasoning"]')?.textContent).toContain("看 processElapsedMs");
    expect(container.querySelector(".model-progress-tools")?.textContent).toContain("读取文件");
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");
    await act(async () => root.unmount());
  });

  it("expands a process trail into thinking and tool chips plus file pills", async () => {
    const blocks: Block[] = [
      {
        id: "c1", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "I'll plan the weekend drop first.", textPhase: "commentary",
      },
      {
        id: "think", kind: "thinking", runId: "run", state: "completed",
        content: "Planning the churn schedule for the weekend drop",
      },
      {
        id: "write", kind: "tool", runId: "run", title: "coding.write_file", state: "completed",
        data: { arguments: JSON.stringify({ path: "src/ChurnSchedule.tsx", content: "a\n".repeat(204) }) },
      },
      {
        id: "shell", kind: "tool", runId: "run", title: "coding.shell", state: "completed",
        data: { arguments: JSON.stringify({ command: "npm run freeze" }) },
      },
      {
        id: "image", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
        data: { arguments: JSON.stringify({ path: "assets/flavor-chart.png" }) },
      },
      {
        id: "css", kind: "tool", runId: "run", title: "coding.edit_hashline", state: "completed",
        data: {
          arguments: JSON.stringify({ input: "¶src/flavors.css#ABCD\nreplace 1:\n+a\n+b\n+c" }),
          output: JSON.stringify({ path: "src/flavors.css", additions: 13, deletions: 0 }),
        },
      },
      {
        id: "c2", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "Then I'll rebuild and check the flavor chart.", textPhase: "commentary",
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "en" })));

    await openSteps(container);

    // A settled step is a plain count row, not the sparkle bar.
    const list = container.querySelector(".bui-tool-chip-group[data-settled]");
    expect(list).not.toBeNull();
    expect(container.querySelector(".bui-thinking-stack, .bui-thinking-tabs")).toBeNull();
    expect(foldLabel(container.querySelector(".process-fold"))).toBe("4 tool calls, 1 messages");
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("4 tool calls, 1 messages");
    expect(container.querySelector(".process-step > .reasoning-trace")).toBeNull();
    // Settled card: 思考 chip first, then tools.
    const chipRows = Array.from(list?.querySelectorAll(".timeline-step-row") ?? []);
    expect(chipRows[0]?.querySelector(".thinking-chip")).not.toBeNull();
    expect(chipRows[0]?.textContent).toContain("Thinking");
    expect(container.querySelector(".thinking-chip .bui-thinking-panel")).toBeNull();
    await openThinkingChip(container);
    expect(container.querySelector('.thinking-chip .bui-thinking-panel[data-tab="reasoning"]')?.textContent)
      .toContain("Planning the churn schedule");
    expect(Array.from(list?.querySelectorAll(".bui-tool-chip-label") ?? []).map((node) => node.textContent))
      .toEqual(expect.arrayContaining(["Run Command", "Read image"]));
    expect(Array.from(list?.querySelectorAll(".bui-tool-chip-detail") ?? []).map((node) => node.textContent))
      .toEqual(expect.arrayContaining(["ChurnSchedule.tsx", "npm run freeze", "flavor-chart.png"]));
    expect(list?.querySelector(".bui-file-change-pills")).not.toBeNull();

    await act(async () => root.unmount());
  });

  it("keeps the 思考 chip first when reasoning arrives after the tools", async () => {
    const blocks: Block[] = [
      {
        id: "read", kind: "tool", runId: "run-late", title: "coding.read_file", state: "completed",
        content: JSON.stringify({ path: "CHANGELOG.md" }),
      },
      {
        id: "search", kind: "tool", runId: "run-late", title: "coding.search", state: "completed",
        content: JSON.stringify({ query: "idle_timeout" }),
      },
      {
        id: "think", kind: "thinking", runId: "run-late", state: "completed",
        content: "The user wants me to analyze the package structure.",
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));
    await openSteps(container);
    const rows = Array.from(container.querySelectorAll(".bui-tool-chip-group[data-settled] .timeline-step-row"));
    expect(rows[0]?.querySelector(".thinking-chip")).not.toBeNull();
    expect(rows[0]?.textContent).toContain("思考");
    expect(rows[0]?.textContent).toContain("The user wants me to analyze t");
    expect(rows[1]?.textContent).toContain("读取文件");
    expect(rows[2]?.textContent).toContain("搜索代码");
    await act(async () => root.unmount());
  });

  it("opens a step with its reasoning above the rows and crossfades only a changed label", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-12T00:00:00Z"));
    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run-open", state: "completed",
      content: "先确认块高度是从哪算出来的", data: { elapsedMs: "1200" },
    };
    const read: Block = {
      id: "read", kind: "tool", runId: "run-open", title: "coding.read_file", state: "running",
      data: { arguments: JSON.stringify({ path: "src/App.tsx" }) },
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    try {
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [thinking, read], language: "zh-CN", activeRunId: "run-open", running: true,
      })));
      const label = () => container.querySelector(".process-step .reasoning-label");
      const naming = label();
      expect(naming?.textContent).toContain("读取文件");

      // A ticking clock is the same label, so it must not restart the roll.
      await act(async () => { vi.advanceTimersByTime(2000); });
      expect(label()).toBe(naming);

      await openSteps(container);
      const body = container.querySelector(".process-step-body");
      // 思考 reads as prose at the top of the step; the tools follow it.
      expect(body?.querySelector(':scope .bui-thinking-panel[data-tab="reasoning"]')?.textContent)
        .toContain("先确认块高度是从哪算出来的");
      expect(body?.querySelector(".thinking-chip")).toBeNull();
      const order = Array.from(body?.querySelectorAll('.bui-thinking-panel[data-tab="reasoning"], .timeline-step-row') ?? [])
        .map((node) => node.className.includes("thinking-panel") ? "reasoning" : "row");
      expect(order).toEqual(["reasoning", "row"]);

      // A different meaning replaces the label element so the new wording can
      // cross in instead of snapping.
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [thinking, { ...read, state: "completed" }], language: "zh-CN",
      })));
      expect(container.querySelector(".process-step .reasoning-label")).toBeNull();
      expect(container.querySelector(".bui-tool-chip-group-header strong")?.textContent).toBe("1 次工具调用");
    } finally {
      await act(async () => root.unmount());
      vi.useRealTimers();
    }
  });

  it("keeps one sparkle bar through wait and search, then expands to the chip list", async () => {
    const user: Block = { id: "user", kind: "user", content: "安排发布", state: "submitted" };
    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run-bar", state: "streaming",
      content: "Planning the churn schedule",
    };
    const search: Block = {
      id: "search", kind: "tool", runId: "run-bar", title: "coding.search", state: "running",
      data: { arguments: JSON.stringify({ query: "churn schedule" }) },
    };
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user], language: "zh-CN", activeRunId: "run-bar", running: true, waitingForModel: true,
    })));
    const header = container.querySelector('[data-testid="thinking-header"]');
    expect(header?.textContent).toContain("思考");
    expect(container.querySelector(".thinking-chip, .timeline-step-list")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, thinking], language: "zh-CN", activeRunId: "run-bar", running: true, waitingForModel: true,
    })));
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(header);
    expect(container.querySelector(".thinking-chip")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, thinking, search], language: "zh-CN", activeRunId: "run-bar", running: true,
    })));
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(header);
    // Live, the bar names the row that is executing instead of freezing on a
    // count; the rows themselves stay visible underneath it.
    expect(header?.textContent).toContain("搜索代码");
    await openSteps(container);
    expect(container.querySelector('.bui-thinking-panel[data-tab="reasoning"]')).not.toBeNull();
    expect(container.querySelector(".timeline-step:not(.thinking-chip)")).not.toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...thinking, state: "completed" }, { ...search, state: "completed" }],
      language: "zh-CN",
    })));
    const settledRows = Array.from(container.querySelectorAll(".bui-tool-chip-group[data-settled] .timeline-step-row"));
    expect(settledRows[0]?.querySelector(".thinking-chip")).not.toBeNull();
    expect(settledRows[0]?.textContent).toContain("思考");
    expect(container.querySelector(".thinking-chip .bui-thinking-panel")).toBeNull();
    await openThinkingChip(container);
    expect(container.querySelector('.thinking-chip .bui-thinking-panel[data-tab="reasoning"]')?.textContent)
      .toContain("Planning the churn schedule");
    expect(container.querySelector(".timeline-step:not(.thinking-chip) .bui-tool-chip-detail")?.textContent)
      .toContain("churn schedule");
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    // Finished tool work leaves the sparkle bar and becomes a plain count row.
    expect(container.querySelector(".process-step .reasoning-summary")).toBeNull();
    expect(foldLabel(container.querySelector(".process-fold"))).toBe("1 次工具调用");
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("1 次工具调用");

    await act(async () => root.unmount());
  });

  it("keeps a multi-group trail on one bar instead of repeating the totals per group", async () => {
    const blocks: Block[] = [
      { id: "c1", kind: "commentary", runId: "run", state: "completed", content: "先建立分析计划。", textPhase: "commentary" },
      { id: "todo", kind: "tool", runId: "run", title: "todo", state: "completed" },
      { id: "c2", kind: "commentary", runId: "run", state: "completed", content: "先盘点仓库顶层结构。", textPhase: "commentary" },
      { id: "ls-1", kind: "tool", runId: "run", title: "coding.list_files", state: "completed" },
      { id: "ls-2", kind: "tool", runId: "run", title: "coding.list_files", state: "completed" },
      { id: "c3", kind: "commentary", runId: "run", state: "completed", content: "顶层是 Go 项目。", textPhase: "commentary" },
      { id: "read-1", kind: "tool", runId: "run", title: "coding.read_file", state: "completed" },
      { id: "read-2", kind: "tool", runId: "run", title: "coding.read_file", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    await openSteps(container);

    // Each message owns the step that follows it, and no step repeats another's
    // totals.
    expect(steps(container).map((step) => step.label)).toEqual([
      "1 次工具调用，1 条进度",
      "2 次工具调用，1 条进度",
      "2 次工具调用，1 条进度",
    ]);
    expect(container.querySelectorAll(".bui-tool-chip-group-header")).toHaveLength(3);
    expect(container.textContent).not.toContain("5 次工具调用");

    await act(async () => root.unmount());
  });

  it("merges adjacent thinking-only spans into one header without inventing commentary", async () => {
    const blocks: Block[] = [
      { id: "t1", kind: "thinking", runId: "run", content: "看分组", state: "completed", data: { elapsedMs: "1200" } },
      { id: "t2", kind: "thinking", runId: "run", content: "再看时长", state: "completed", data: { elapsedMs: "1300" } },
      { id: "t3", kind: "thinking", runId: "run", content: "不要拆行", state: "completed", data: { elapsedMs: "1700" } },
      { id: "t4", kind: "thinking", runId: "run", content: "继续", state: "completed", data: { elapsedMs: "500" } },
      { id: "t5", kind: "thinking", runId: "run", content: "收束", state: "completed", data: { elapsedMs: "22700" } },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelector(".commentary-block")).toBeNull();
    expect(container.querySelectorAll('[data-testid="thinking-header"]')).toHaveLength(1);
    expect(foldLabel(container.querySelector('[data-testid="thinking-header"]'))).toBe("思考");
    expect(foldClock(container.querySelector('[data-testid="thinking-header"]'))).toBe("27.4s");
    expect(container.textContent).not.toContain("我准备");
    expect(container.textContent).not.toContain("正在调用所需工具，并根据实际结果继续。");
    await act(async () => root.unmount());
  });

  it("does not render thinking text as commentary", async () => {
    const blocks: Block[] = [{
      id: "think", kind: "thinking", runId: "run", state: "completed",
      content: "我准备先查块高度是从哪算出来的。",
      data: { elapsedMs: "1200" },
    }];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    expect(container.querySelector(".commentary-block")).toBeNull();
    expect(foldLabel(container.querySelector(".reasoning-summary"))).toBe("思考");
    expect(foldClock(container.querySelector(".reasoning-summary"))).toBe("1.2s");
    expect(container.querySelector(".reasoning-summary")?.textContent).not.toContain("我准备先查块高度");
    await act(async () => root.unmount());
  });

  it("renders model commentary as ordinary prose and keeps its tools below", async () => {
    const blocks: Block[] = [
      {
        id: "progress-done", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "**读取当前前端结构**\nApp、Sidebar、Timeline 与 Inspector",
        data: { startedAt: "1000", completedAt: "1100" },
      },
      {
        id: "read", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
        content: "{\"path\":\"frontend/src/components/Timeline.tsx\"}",
        data: { startedAt: "1100", completedAt: "2200", elapsedMs: "1100" },
      },
      {
        id: "progress-live", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "**构建高保真交互原型**\n页面、工具与文本共享一套节奏",
      },
      {
        id: "edit", kind: "tool", runId: "run", title: "coding.edit_hashline", state: "running",
        content: "{\"path\":\"frontend/src/prototype.css\"}", data: { elapsedMs: "4200" },
      },
      {
        id: "legacy", kind: "commentary", runId: "run", title: "progress", state: "streaming",
        content: "这是一段旧会话模型输出，不能被前端擅自截成标题。",
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run", running: true, waitingForModel: true,
    })));

    await openSteps(container);

    const groups = Array.from(container.querySelectorAll<HTMLElement>(".model-progress-prose"));
    expect(groups).toHaveLength(3);
    expect(groups[0]?.querySelector(".commentary-block")?.textContent).toContain("读取当前前端结构");
    expect(groups[0]?.querySelector(".commentary-block")?.textContent).toContain("App、Sidebar、Timeline 与 Inspector");
    expect(groups[0]?.querySelector(":scope > time")).toBeNull();
    expect(groups[0]?.getAttribute("data-state")).toBe("settled");
    expect(groups[1]?.getAttribute("data-state")).toBe("running");
    expect(groups[2]?.textContent).toContain("不能被前端擅自截成标题");
    // Each message is followed by its own step; the work belongs to that step
    // and is never repeated beside the prose.
    expect(groups[0]?.querySelector(".model-progress-tools")).toBeNull();
    const stepBodies = Array.from(container.querySelectorAll<HTMLElement>(".process-step .model-progress-tools"));
    expect(stepBodies[0]?.textContent).toContain("读取文件");
    expect(stepBodies[1]?.textContent).toContain("编辑文件");
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelector(".bui-thinking-stack")).toBeNull();
    expect(container.querySelector(".model-progress-step")).toBeNull();
    expect(container.querySelectorAll(".process-entries > .tool-block")).toHaveLength(0);
    expect(container.querySelector(".bui-loading-state, .reasoning-placeholder")).toBeNull();

    await act(async () => root.unmount());
  });

  it("keeps reasoning inside the active announced tool step", async () => {
    const blocks: Block[] = [
      {
        id: "thinking-before", kind: "thinking", runId: "run", state: "completed",
        content: "先确认调用边界", data: { elapsedMs: "1200" },
      },
      {
        id: "progress", kind: "commentary", runId: "run", title: "progress", state: "completed",
        content: "**调查运行时入口**\n读取流事件与前端投影",
      },
      {
        id: "thinking-live", kind: "thinking", runId: "run", state: "streaming",
        content: "正在核对事件顺序",
      },
      {
        id: "search", kind: "tool", runId: "run", title: "coding.search", state: "running",
        content: JSON.stringify({ query: "FrameToolCall" }),
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run", running: true,
    })));

    await openSteps(container);

    const step = container.querySelector<HTMLElement>(".model-progress-prose");
    expect(step?.querySelector(".commentary-block")?.textContent).toContain("调查运行时入口");
    expect(step?.querySelector(".commentary-block")?.closest(".bui-thinking-state")).toBeNull();
    // The announced step keeps its reasoning as the first chip on its own rail,
    // under the one bar that names what is running.
    expect(container.querySelector('.process-step .bui-thinking-panel[data-tab="reasoning"]')).not.toBeNull();
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelectorAll(".reasoning-summary")).toHaveLength(1);
    expect(container.querySelector('[data-testid="thinking-header"]')?.textContent).toContain("搜索代码");
    expect(container.textContent).toContain("正在核对事件顺序");
    expect(container.querySelectorAll(".process-entries > .reasoning-trace")).toHaveLength(0);

    await act(async () => root.unmount());
  });

  it("keeps emoji-prefixed model progress on the same compact step projection", async () => {
    const blocks: Block[] = [{
      id: "progress-skill", kind: "commentary", runId: "run", title: "progress", state: "completed",
      content: "🥷\n\n**加载深审规则**\n读取整库审计模式后建立证据清单。",
      data: { startedAt: "1000", completedAt: "7000" },
    }];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    const step = container.querySelector<HTMLElement>(".model-progress-prose");
    expect(step?.querySelector(".commentary-block")?.textContent).toContain("加载深审规则");
    expect(step?.querySelector(".commentary-block")?.textContent).toContain("读取整库审计模式后建立证据清单。");
    expect(step?.querySelector(":scope > time")).toBeNull();
    expect(container.querySelector(".model-progress-step")).toBeNull();

    await act(async () => root.unmount());
  });

  it("keeps model-authored progress neutral when a nested command fails", async () => {
    const blocks: Block[] = [
      {
        id: "progress-validation", kind: "commentary", runId: "run-validation", title: "progress", state: "completed",
        content: "**执行项目验证矩阵**\n继续运行其余验证命令",
      },
      {
        id: "go-test", kind: "tool", runId: "run-validation", title: "coding.shell", state: "failed",
        content: JSON.stringify({ command: "GOWORK=off go test ./..." }),
      },
      {
        id: "frontend-test", kind: "tool", runId: "run-validation", title: "coding.shell", state: "completed",
        content: JSON.stringify({ command: "cd frontend && bun run test" }),
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    await openSteps(container);

    const progress = container.querySelector<HTMLElement>(".model-progress-prose");
    expect(progress?.getAttribute("data-state")).toBe("settled");
    expect(progress?.querySelector(".commentary-block")?.textContent).toContain("执行项目验证矩阵");
    expect(container.querySelector('.tool-block[data-state="failed"] .tool-status')?.textContent).toBe("失败");

    await act(async () => root.unmount());
  });

  it("shows each completed tool's own duration instead of the step clock", async () => {
    const blocks: Block[] = [
      {
        id: "read", kind: "tool", runId: "run-clocks", title: "coding.read_file", state: "completed",
        content: JSON.stringify({ path: "CHANGELOG.md" }),
        data: { startedAt: "1000", completedAt: "2800", elapsedMs: "1800" },
      },
      {
        id: "list", kind: "tool", runId: "run-clocks", title: "coding.list_files", state: "completed",
        content: JSON.stringify({ path: "." }),
        data: { startedAt: "1000", completedAt: "4300", elapsedMs: "3300" },
      },
      {
        id: "search", kind: "tool", runId: "run-clocks", title: "coding.search", state: "completed",
        content: JSON.stringify({ query: "idle" }),
        data: { startedAt: "1200", completedAt: "104000", elapsedMs: "102800" },
      },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));
    await openSteps(container);
    const group = container.querySelector(".bui-tool-chip-group[data-settled]");
    expect(group).not.toBeNull();
    expect(group?.querySelectorAll(".tool-status")).toHaveLength(0);
    expect(container.querySelector(".bui-tool-chip-group-header")?.textContent).toContain("3 次工具调用");
    expect(container.querySelector(".process-step .reasoning-summary")).toBeNull();
    await act(async () => root.unmount());
  });

  it("renders live Markdown immediately and keeps settled structure stable as content grows", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const live: Block = {
      id: "live-markdown", kind: "assistant", state: "streaming",
      content: "## 检查结果\n\n- **架构**通过\n- `测试`通过\n\n---\n\n继续核对。",
    };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    const heading = container.querySelector(".streaming-text h2");
    expect(heading?.textContent).toBe("检查结果");
    expect(container.querySelectorAll(".streaming-text li")).toHaveLength(2);
    expect(container.querySelector(".streaming-text strong")?.textContent).toBe("架构");
    expect(container.querySelector(".streaming-text code")?.textContent).toBe("测试");
    expect(container.querySelector(".streaming-text hr")).not.toBeNull();
    expect(container.querySelector(".streaming-text-reveal")).not.toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: `${live.content}\n\n### 新证据\n\n第三段。` }], language: "zh-CN",
    })));
    expect(container.querySelector(".streaming-text h2")).toBe(heading);
    expect(container.querySelector(".streaming-text h3")?.textContent).toBe("新证据");
    const revealIDs = Array.from(container.querySelectorAll<HTMLElement>("[data-stream-reveal]"));
    const latestRevealID = Math.max(...revealIDs.map((node) => Number(node.dataset.streamReveal)));
    expect(revealIDs.filter((node) => Number(node.dataset.streamReveal) === latestRevealID).map((node) => node.textContent).join(""))
      .toBe("新证据第三段。");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: `${live.content}\n\n### 新证据\n\n第三段。`, state: "completed" }], language: "zh-CN",
    })));
    expect(container.querySelector(".assistant-block > .streaming-text")).not.toBeNull();
    expect(container.querySelector(".assistant-block > h2, .assistant-block > p")).toBeNull();
    expect(container.querySelector(".streaming-text h2")).toBe(heading);
    expect(container.querySelector(".streaming-text h3")?.textContent).toBe("新证据");
    expect(container.querySelector(".bui-streaming-text.active")).toBeNull();
    await act(async () => root.unmount());
  });

  it("hides the streaming caret and process rail on a completed two-paragraph final answer", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run", content: "组织自我介绍", state: "completed",
      data: { elapsedMs: "2500" },
    };
    const answer: Block = {
      id: "answer", kind: "assistant", runId: "run",
      content: "我是 **Azem**，一个本地编码助手，负责读仓库、改代码、查问题和验证结果。\n\n当前对话里没有未完成的任务。",
      textPhase: "final_answer", state: "streaming",
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, answer], language: "zh-CN", activeRunId: "run", running: true,
    })));
    const prose = container.querySelector('[data-testid="timeline-prose"]');
    const live = prose?.querySelector(".streaming-text");
    expect(prose?.querySelectorAll("p")).toHaveLength(2);
    expect(live?.classList.contains("active")).toBe(true);
    expect(prose?.closest(".process-entries")).toBeNull();
    expect(prose?.classList.contains("phase-pending")).toBe(false);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [thinking, { ...answer, state: "completed" }], language: "zh-CN",
    })));
    expect(container.querySelector('[data-testid="timeline-prose"]')).toBe(prose);
    expect(container.querySelector(".assistant-block > .streaming-text")).toBe(live);
    expect(live?.classList.contains("active")).toBe(false);
    expect(container.querySelector(".bui-streaming-text.active")).toBeNull();
    expect(container.querySelector(".streaming-text.active")).toBeNull();
    expect(prose?.classList.contains("phase-pending")).toBe(false);
    expect(prose?.closest(".process-entries")).toBeNull();
    expect(prose?.querySelectorAll("p")).toHaveLength(2);
    expect(foldLabel(container.querySelector(".reasoning-trace"))).toBe("思考");
    expect(foldClock(container.querySelector(".reasoning-trace"))).toBe("2.5s");
    await act(async () => root.unmount());
  });

  it("renders a settled answer through the same Markdown chrome without a reveal remount", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{
        id: "settled-markdown", kind: "assistant", state: "completed",
        content: "## 检查结果\n\n- **架构**通过",
      }],
      language: "zh-CN",
    })));
    expect(container.querySelector(".assistant-block > .streaming-text")).not.toBeNull();
    expect(container.querySelector(".streaming-text h2")?.textContent).toBe("检查结果");
    expect(container.querySelector(".streaming-text strong")?.textContent).toBe("架构");
    expect(container.querySelector(".bui-streaming-text.active")).toBeNull();
    expect(container.querySelector(".streaming-text-reveal")).toBeNull();
    await act(async () => root.unmount());
  });

  it("keeps streamed commentary markdown mounted after the step settles", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const live: Block = {
      id: "live-commentary", kind: "commentary", runId: "run", state: "streaming", title: "progress",
      content: "正在核对 **架构**。",
    };
    const tool: Block = { id: "search", kind: "tool", runId: "run", title: "coding.search", state: "running" };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live, tool], language: "zh-CN" })));
    const emphasis = container.querySelector(".commentary-block .streaming-text strong");
    expect(emphasis?.textContent).toBe("架构");

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [{ ...live, state: "completed" }, tool], language: "zh-CN" })));
    expect(container.querySelector(".commentary-block .streaming-text strong")).toBe(emphasis);
    expect(container.querySelector(".bui-streaming-text.active")).toBeNull();
    await act(async () => root.unmount());
  });

  it("reveals only appended text while keeping the live paragraph mounted", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const live: Block = { id: "live-tail", kind: "assistant", state: "streaming", content: "正在输出" };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    const paragraph = container.querySelector(".streaming-text p");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: "正在输出，继续" }], language: "zh-CN",
    })));
    expect(container.querySelector(".streaming-text p")).toBe(paragraph);
    const revealIDs = Array.from(container.querySelectorAll<HTMLElement>("[data-stream-reveal]"));
    const latestRevealID = Math.max(...revealIDs.map((node) => Number(node.dataset.streamReveal)));
    expect(revealIDs.filter((node) => Number(node.dataset.streamReveal) === latestRevealID).map((node) => node.textContent).join(""))
      .toBe("，继续");
    expect(paragraph?.querySelector(".streaming-text-reveal")?.textContent).toBe("，继续");
    expect(paragraph?.textContent?.startsWith("正在输出")).toBe(true);
    await act(async () => root.unmount());
  });

  it("does not replay reveal motion on already-written final-answer lines", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const first = "第一段已经写完。\n\n第二段也已经写完。";
    const live: Block = {
      id: "live-final", kind: "assistant", state: "streaming", textPhase: "final_answer",
      content: first,
    };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    const firstParagraph = container.querySelector(".streaming-text p");
    expect(firstParagraph?.textContent).toBe("第一段已经写完。");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: `${first}\n\n第三段正在输出。` }], language: "zh-CN",
    })));
    const paragraphs = Array.from(container.querySelectorAll(".streaming-text p"));
    expect(paragraphs[0]).toBe(firstParagraph);
    expect(paragraphs).toHaveLength(3);
    expect(paragraphs[0]?.querySelector(".streaming-text-reveal")).toBeNull();
    expect(paragraphs[1]?.querySelector(".streaming-text-reveal")).toBeNull();
    expect(paragraphs[2]?.querySelector(".streaming-text-reveal")?.textContent).toBe("第三段正在输出。");
    expect(container.querySelectorAll(".streaming-text-reveal")).toHaveLength(1);
    await act(async () => root.unmount());
  });

  it("streams a fenced code card with Beautiful UI chrome without remounting it", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const container = document.createElement("div");
    const root = createRoot(container);
    const first = "先看实现。\n\n```churn.ts\nexport async function churnBatch(flavor: string) {\n  const base = await getFlavor(flavor);\n";
    const live: Block = { id: "live-code", kind: "assistant", state: "streaming", content: first };
    await act(async () => root.render(createElement(TimelineFeed, { blocks: [live], language: "zh-CN" })));
    const heading = container.querySelector(".streaming-text p");
    const card = container.querySelector(".bui-code-block");
    expect(heading?.textContent).toBe("先看实现。");
    expect(card?.querySelector(".bui-code-filename")?.textContent).toBe("churn.ts");
    expect(card?.querySelector(".bui-code-lang")?.textContent).toBe("TypeScript");
    expect(card?.querySelector(".bui-code-copy")?.textContent).toContain("复制");
    expect(card?.querySelector(".syntax-keyword")?.textContent).toBe("export");

    const next = `${first}  return base;\n}\n\`\`\`\n\n完成。`;
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: next }], language: "zh-CN",
    })));
    expect(container.querySelector(".streaming-text p")).toBe(heading);
    expect(container.querySelector(".bui-code-block")).toBe(card);
    expect(card?.textContent).toContain("return");
    expect(Array.from(container.querySelectorAll(".streaming-text p")).at(-1)?.textContent).toBe("完成。");

    await act(async () => container.querySelector<HTMLButtonElement>(".bui-code-copy")?.click());
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("export async function churnBatch"));

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{ ...live, content: next, state: "completed" }], language: "zh-CN",
    })));
    expect(container.querySelector(".bui-code-block")).toBe(card);
    expect(container.querySelector(".bui-streaming-text.active")).toBeNull();
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

    await openSteps(container);

    expect(container.querySelectorAll(".reasoning-step")).toHaveLength(0);
    expect(container.querySelector(".bui-tool-chip-group[data-settled] .timeline-step-row .thinking-chip")).not.toBeNull();
    const settledRows = Array.from(container.querySelectorAll(".bui-tool-chip-group[data-settled] .timeline-step-row"));
    expect(settledRows[0]?.querySelector(".thinking-chip")).not.toBeNull();
    expect(settledRows[0]?.textContent).toContain("思考");
    await openThinkingChip(container);
    expect(container.querySelectorAll(".reasoning-step")).toHaveLength(2);
    expect(container.querySelector('.thinking-chip .bui-thinking-panel[data-tab="reasoning"]')?.textContent).toContain("先检查现有事件顺序");
    expect(container.querySelector(".bui-thinking-stack")).toBeNull();
    // Finished tool work is a plain count row; thinking stays a chip in the list.
    expect(container.querySelector(".process-step .reasoning-summary")).toBeNull();
    expect(container.querySelector(".bui-tool-chip-group-header")).not.toBeNull();
    expect(container.querySelectorAll(".file-change-entry")).toHaveLength(2);
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>(".file-change-entry"))
      .every((details) => !details.open)).toBe(true);
    expect(container.querySelector(".file-change-entry")?.textContent).toContain("已编辑的文件");
    expect(container.querySelector(".file-change-entry .bui-tool-chip-detail")?.textContent).toBe("ThreadSurface.tsx");
    expect(container.querySelector(".file-change-entry .bui-tool-chip-icon")?.getAttribute("data-icon")).toBe("pencil");
    expect(Array.from(container.querySelectorAll(".bui-file-change-pill")).map((node) => node.textContent)).toEqual([
      "ThreadSurface.tsx+1-1",
      "plain.ts+1-1",
    ]);
    expect(container.querySelector(".code-diff-stack")).toBeNull();

    const firstEdit = container.querySelector<HTMLDetailsElement>(".file-change-entry");
    await act(async () => {
      if (!firstEdit) return;
      firstEdit.open = true;
      firstEdit.dispatchEvent(new Event("toggle"));
    });
    expect(container.querySelector(".file-change-entry > .code-diff-stack.process-rail-inset")).not.toBeNull();
    expect(container.querySelectorAll(".code-diff-stack")).toHaveLength(1);
    expect(container.querySelector(".code-diff tr.deleted")?.textContent).toContain("oldValue");
    expect(container.querySelector(".code-diff tr.added")?.textContent).toContain("nextValue");
    expect(container.querySelector(".syntax-keyword")?.textContent).toBe("const");
    expect(Array.from(container.querySelectorAll(".syntax-function")).map((node) => node.textContent)).toEqual(["read", "parse"]);
    expect(container.querySelector(".edited-files-summary")?.textContent).toContain("已编辑 1 个文件");
    expect(container.querySelector(".edited-files-summary")?.textContent).toContain("frontend/src/ThreadSurface.tsx");
    expect(container.querySelector(".run-status-marker:not(.section-marker)")?.textContent).toBe("你在 1m05s 后停止了");
    expect(container.querySelector<HTMLButtonElement>('.code-diff button[aria-label="复制差异"]')).not.toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("matches Codex file-summary density by folding after three files", async () => {
    const files = ["one.go", "two.go", "three.go", "four.go", "five.go"];
    const blocks: Block[] = [
      { id: "user-files", kind: "user", content: "修改这些文件", state: "completed" },
      {
        id: "edit-files", kind: "tool", runId: "run-files", title: "coding.edit_hashline", state: "completed",
        data: { structured: JSON.stringify({ sections: files.map((path) => ({
          path,
          firstChangedLine: 1,
          diff: "-old\n+new",
        })) }) },
      },
      { id: "answer-files", kind: "assistant", runId: "run-files", content: "完成。", state: "completed" },
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    const summary = container.querySelector(".edited-files-summary")!;
    expect(summary.querySelectorAll("li")).toHaveLength(3);
    expect(summary.textContent).toContain("再显示 2 个文件");
    const toggle = summary.querySelector<HTMLButtonElement>(".edited-files-toggle")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");

    await act(async () => toggle.click());

    expect(summary.querySelectorAll("li")).toHaveLength(5);
    expect(summary.textContent).toContain("收起");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    await act(async () => root.unmount());
    container.remove();
  });

  it("omits zero file-change totals and uses a compact deletion sign", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const blocks: Block[] = [{
      id: "insert-only", kind: "tool", runId: "run-insert", title: "coding.edit_hashline", state: "completed",
      data: { structured: JSON.stringify({ sections: [{ path: "src/app.ts", firstChangedLine: 1, diff: "+const added = true;" }] }) },
    }];

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    const totals = container.querySelector(".file-change-totals");
    expect(totals?.textContent).toBe("+1");
    expect(totals?.querySelector(".minus")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps a running hashline edit in the file-change presentation as it completes", async () => {
    const runningEdit: Block = {
      id: "edit-live", kind: "tool", runId: "run-live-edit", title: "coding.edit_hashline", state: "running",
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
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [runningEdit], language: "zh-CN", activeRunId: "run-live-edit", running: true,
    })));

    await openSteps(container);

    const runningEntry = container.querySelector<HTMLDetailsElement>('.file-change-entry[data-state="running"]');
    expect(runningEntry?.getAttribute("aria-busy")).toBe("true");
    expect(runningEntry?.textContent).toContain("正在编辑文件");
    expect(runningEntry?.textContent).toContain("+3");
    expect(runningEntry?.textContent).toContain("-3");
    expect(container.querySelector(".tool-block")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{
        ...runningEdit,
        state: "completed",
        data: { structured: JSON.stringify({ sections: [
          { path: "src/app.ts", firstChangedLine: 4, diff: "-const current = 1;\n+const next = 2;\n+line one\n+line two" },
          { path: "src/theme.ts", firstChangedLine: 2, diff: "-old one\n-old two" },
        ] }) },
      }],
      language: "zh-CN", activeRunId: "run-live-edit", running: true,
    })));

    await openSteps(container);

    const completedEntry = container.querySelector<HTMLDetailsElement>('.file-change-entry[data-state="completed"]');
    expect(completedEntry).toBe(runningEntry);
    expect(completedEntry?.getAttribute("aria-busy")).toBeNull();
    expect(completedEntry?.textContent).toContain("已编辑的文件");

    await act(async () => root.unmount());
    container.remove();
  });

  it("shows a shield and hides edit bodies while a file write is under review", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const rawEdit = "¶frontend/src/i18n.ts#1A65 replace 36:\n+ recapTitle: \"secret recap copy\"";

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{
        id: "edit-review", kind: "tool", runId: "run-review", title: "coding.edit_hashline",
        state: "reviewing_approval",
        data: { arguments: JSON.stringify({ input: rawEdit }) },
      }],
      language: "zh-CN", activeRunId: "run-review", running: true,
    })));

    await openSteps(container);

    expect(container.querySelector(".pending-file-edit")?.getAttribute("data-state")).toBe("reviewing_approval");
    expect(container.querySelector('.pending-file-edit .work-entry-icon[data-icon="shield"]')).not.toBeNull();
    expect(container.textContent).toContain("编辑文件");
    expect(container.textContent).toContain("审核中");
    expect(container.textContent).toContain("i18n.ts");
    expect(container.textContent).not.toContain("secret recap copy");
    expect(container.textContent).not.toContain("replace 36");
    expect(container.textContent).not.toContain("¶frontend");
    expect(container.querySelector(".tool-block")).toBeNull();
    expect(container.querySelector(".file-change-entry[data-state='running']")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("never exposes raw edit arguments while an active file change is not yet parseable", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const rawEdit = "¶src/app.ts#ABCD\nreplace block 4:\n+const unrendered = true;";

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{
        id: "edit-partial", kind: "tool", runId: "run-live-edit", title: "coding.edit_hashline", state: "running",
        data: { arguments: JSON.stringify({ input: rawEdit }) },
      }],
      language: "zh-CN", activeRunId: "run-live-edit", running: true,
    })));

    await openSteps(container);

    expect(container.querySelector('.file-change-entry[data-state="running"]')).not.toBeNull();
    expect(container.querySelector(".tool-block")).toBeNull();
    expect(container.textContent).toContain("正在编辑文件");
    expect(container.textContent).not.toContain("unrendered");
    expect(container.querySelector(".file-change-totals")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [{
        id: "edit-partial-step", kind: "tool", runId: "run-live-edit", title: "coding.edit_hashline", state: "running",
        content: rawEdit,
        data: { arguments: JSON.stringify({ input: rawEdit }), presentation: "steps" },
      }],
      language: "zh-CN", activeRunId: "run-live-edit", running: true,
    })));

    await openSteps(container);

    expect(container.querySelector('.file-change-entry[data-state="running"]')).not.toBeNull();
    expect(container.querySelector(".timeline-step")).toBeNull();
    expect(container.textContent).not.toContain("unrendered");

    await act(async () => root.unmount());
    container.remove();
  });

  it("reviews idle file edits together with a shield and hides edit bodies", async () => {
    const rawEdit = "¶frontend/src/i18n.ts#1A65 replace 36:\n+ recapTitle: \"secret recap copy\"";
    const edits: Block[] = ["edit-a", "edit-b", "edit-c"].map((id) => ({
      id, kind: "tool", runId: "run-idle-edits", title: "coding.edit_hashline", state: "queued",
      data: { arguments: JSON.stringify({ input: rawEdit }) },
    }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: edits, language: "zh-CN", activeRunId: "run-idle-edits", running: true,
    })));

    await openSteps(container);

    expect(container.querySelectorAll(".pending-file-edit[data-state='reviewing_approval']")).toHaveLength(3);
    expect(container.querySelectorAll('.pending-file-edit .work-entry-icon[data-icon="shield"]')).toHaveLength(3);
    expect(container.textContent).toContain("审核中");
    expect(container.textContent).not.toContain("排队中");
    expect(container.textContent).not.toContain("secret recap copy");
    expect(container.textContent).not.toContain("replace 36");
    expect(container.querySelector(".tool-block")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps a queued file edit behind an executing sibling", async () => {
    const rawEdit = "¶src/app.ts#ABCD replace 4:\n+const next = 2;";
    const blocks: Block[] = [
      {
        id: "edit-running", kind: "tool", runId: "run-busy-edit", title: "coding.edit_hashline", state: "running",
        data: { arguments: JSON.stringify({ input: rawEdit }) },
      },
      {
        id: "edit-queued", kind: "tool", runId: "run-busy-edit", title: "coding.edit_hashline", state: "queued",
        data: { arguments: JSON.stringify({ input: rawEdit }) },
      },
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run-busy-edit", running: true,
    })));

    await openSteps(container);

    expect(container.querySelector(".file-change-entry[data-state='running']")?.textContent).toContain("正在编辑文件");
    expect(container.querySelector(".pending-file-edit[data-state='reviewing_approval']")?.textContent).toContain("审核中");
    expect(container.querySelector('.pending-file-edit .work-entry-icon[data-icon="shield"]')).not.toBeNull();
    expect(container.textContent).not.toContain("排队中");
    expect(container.textContent).not.toContain("const next");

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

    await openSteps(container);

    expect(container.querySelectorAll(".timeline-step-list")).toHaveLength(1);
    expect(container.querySelectorAll(".timeline-step")).toHaveLength(6);
    expect(container.querySelectorAll(".tool-group, .process-entries > .tool-block")).toHaveLength(0);
    // The bar names the running shell; the rows below it are the whole step, so
    // there is no second header repeating the count.
    expect(container.querySelector(".bui-tool-chip-group-header")).toBeNull();
    expect(foldLabel(container.querySelector(".process-fold"))).toBe("运行命令");
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(Array.from(container.querySelectorAll(".bui-tool-chip-detail")).map((node) => node.textContent)).toEqual(
      expect.arrayContaining(["new.go"]),
    );
    // The running shell is a row of the step now, so its command shows once
    // there instead of being hoisted onto the bar.
    expect(container.querySelector('.timeline-step[data-state="running"]')?.textContent).toContain("bun run build");
    expect(container.textContent).toContain("需要审批");
    expect(container.textContent).toContain("排队中");
    expect(container.querySelector('.pending-file-edit[data-state="awaiting_approval"]')).not.toBeNull();
    expect(container.querySelector('.pending-file-edit .work-entry-icon[data-icon="shield"]')).not.toBeNull();
    expect(container.querySelector(".pending-file-edit .bui-tool-chip-detail")?.textContent).toBe("new.go");
    expect(container.querySelector(".bui-file-change-pills")).toBeNull();
    expect(container.textContent).not.toContain("package main");
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>(".timeline-step"))
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
    // UI-016: the wait is the step that is about to receive blocks, so the very
    // same bar carries it through thinking and tools.
    const wait = container.querySelector(".process-fold");
    expect(wait?.textContent).toContain("思考");
    expect(wait?.textContent).not.toMatch(/0s|0\.0s/u);
    expect(wait?.querySelector(".bui-thinking-state.streaming")).not.toBeNull();
    expect(wait?.querySelector(".bui-thinking-pill")).toBeNull();
    expect(wait?.querySelector(".azem-thinking-mark")).not.toBeNull();
    expect(wait?.querySelector(".bui-loading-state, .bui-loading-grid")).toBeNull();
    expect(wait?.querySelector(".reasoning-label-sweep")).not.toBeNull();
    expect(wait?.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    const waitHeader = wait?.querySelector('[data-testid="thinking-header"]');

    const live: Block = {
      id: "thinking-live", kind: "thinking", runId: "run-live",
      content: "**检查事件管线**\n\n确认 `stream` 和 [事件顺序](https://example.com)。", state: "streaming",
    };
    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, live], language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));
    expect(container.querySelector(".bui-loading-state, .bui-loading-grid")).toBeNull();
    expect(container.querySelector(".reasoning-placeholder")).toBeNull();
    expect(container.querySelector('[data-testid="thinking-header"]')).toBe(waitHeader);
    expect(container.querySelector(".reasoning-label-sweep")).not.toBeNull();
    expect(container.querySelector(".azem-thinking-mark.active")?.childElementCount).toBe(2);
    expect(container.querySelector(".reasoning-spark")).toBeNull();
    // SUBAGENT-005: a live step shows its own thinking instead of a bare body.
    expect(container.querySelector(".reasoning-step strong, .reasoning-step code, .reasoning-step a")).toBeNull();
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelector(".bui-thinking-panel[data-tab='reasoning']")?.textContent)
      .toContain("确认 stream 和 事件顺序。");
    const summary = container.querySelector<HTMLButtonElement>(".reasoning-summary")!;
    expect(summary.textContent).toContain("思考");

    await act(async () => summary.click());
    expect(summary.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector(".process-step-body")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...live, content: `${live.content}\n\n**继续验证**` }],
      language: "zh-CN", activeRunId: "run-live", running: true, waitingForModel: true,
    })));
    // A collapsed trace stays collapsed while new reasoning keeps arriving.
    expect(container.querySelector(".reasoning-summary")?.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector(".process-step-body")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [user, { ...live, state: "completed", data: { elapsedMs: "4200" } }],
      language: "zh-CN", activeRunId: "", running: false,
    })));
    expect(foldLabel(container.querySelector(".reasoning-trace.completed"))).toBe("思考");
    expect(foldClock(container.querySelector(".reasoning-trace.completed"))).toBe("4.2s");
    expect(container.querySelector(".reasoning-label-sweep")).toBeNull();
    expect(container.querySelector(".azem-thinking-mark.active")).toBeNull();
    expect(container.querySelector(".reasoning-summary")?.getAttribute("aria-expanded")).toBe("false");

    await act(async () => root.unmount());
    container.remove();
  });
  it("ticks first-token and live reasoning from tenths of a second without a 0s clock", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-14T15:00:00Z"));
    const user: Block = { id: "user", kind: "user", runId: "run-clock", content: "开始", state: "submitted" };
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    try {
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [user], language: "zh-CN", activeRunId: "run-clock", running: true, waitingForModel: true,
      })));
      const wait = container.querySelector(".process-fold");
      const waitHeader = wait?.querySelector('[data-testid="thinking-header"]');
      expect(foldLabel(wait)).toBe("思考");
      expect(foldClock(waitHeader)).toBe("");
      const firstMessage = container.querySelector(".user-block");
      expect(processRule(container)?.textContent).toContain("正在处理");
      expect(ruleClock(container)).toBe("");
      expect(wait?.querySelector("[data-testid='process-status-rule']")).toBeNull();
      expect(container.querySelectorAll("[data-testid='process-status-rule']")).toHaveLength(1);
      expect(processRule(container)!.compareDocumentPosition(firstMessage!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(wait?.querySelector(".bui-thinking-state.streaming")).not.toBeNull();
      expect(wait?.querySelector(".bui-thinking-pill")).toBeNull();
      expect(wait?.querySelector(".azem-thinking-mark")).not.toBeNull();
      expect(wait?.querySelector(".bui-loading-grid")).toBeNull();

      await act(async () => { vi.advanceTimersByTime(100); });
      expect(foldLabel(wait)).toBe("思考");
      expect(foldClock(waitHeader)).toBe("");
      expect(ruleClock(container)).toBe("0.1s");
      await act(async () => { vi.advanceTimersByTime(200); });
      expect(foldLabel(wait)).toBe("思考");
      expect(foldClock(waitHeader)).toBe("");
      expect(ruleClock(container)).toBe("0.3s");

      const live: Block = {
        id: "thinking-live", kind: "thinking", runId: "run-clock",
        content: "核对事件顺序", state: "streaming",
      };
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [user, live], language: "zh-CN", activeRunId: "run-clock", running: true, waitingForModel: true,
      })));
      const trace = container.querySelector(".reasoning-trace.streaming");
      expect(container.querySelector(".reasoning-placeholder")).toBeNull();
      expect(container.querySelector('[data-testid="thinking-header"]')).toBe(waitHeader);
      expect(foldLabel(trace)).toBe("思考");
      expect(foldClock(trace)).toBe("");
      expect(ruleClock(container)).toMatch(/^0\.[3-9]s$/u);

      await act(async () => { vi.advanceTimersByTime(1200); });
      expect(foldLabel(container.querySelector(".reasoning-trace"))).toBe("思考");
      expect(foldClock(container.querySelector(".reasoning-trace"))).toBe("");
      expect(ruleClock(container)).toMatch(/^1\.[5-9]s$/u);

      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [user, { ...live, state: "completed", data: { elapsedMs: "65000" } }],
        language: "zh-CN", activeRunId: "", running: false,
      })));
      const settled = container.querySelector(".reasoning-trace.completed");
      expect(foldLabel(settled)).toBe("思考");
      expect(foldClock(settled)).toBe("1m05s");
    } finally {
      await act(async () => root.unmount());
      container.remove();
      vi.useRealTimers();
    }
  });

  it("keeps the live clock on the 正在处理 rule and not on thinking or tool rows", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-14T16:00:00Z"));
    const user: Block = { id: "user", kind: "user", runId: "run-rule", content: "继续", state: "submitted" };
    const commentary: Block = {
      id: "plan", kind: "commentary", runId: "run-rule", title: "progress", state: "completed",
      content: "我先读入口再跑检查。", textPhase: "commentary",
    };
    const thinking: Block = {
      id: "think", kind: "thinking", runId: "run-rule", content: "核对调用顺序", state: "streaming",
    };
    const read: Block = {
      id: "read", kind: "tool", runId: "run-rule", title: "coding.read_file", state: "completed",
      content: JSON.stringify({ path: "README.md" }),
      data: { startedAt: "1000", completedAt: "2800", elapsedMs: "1800" },
    };
    const shell: Block = {
      id: "shell", kind: "tool", runId: "run-rule", title: "coding.shell", state: "running",
      content: JSON.stringify({ command: "bun run test" }),
      data: { startedAt: String(Date.now() - 4200), elapsedMs: "4200" },
    };
    const failed: Block = {
      id: "failed", kind: "tool", runId: "run-rule", title: "coding.search", state: "failed",
      content: JSON.stringify({ query: "idle" }),
    };
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    try {
      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [user, commentary, thinking, read, shell, failed],
        language: "zh-CN", activeRunId: "run-rule", running: true, waitingForModel: true,
      })));
      await openSteps(container);
      const fold = container.querySelector(".process-fold");
      const rule = processRule(container);
      const firstMessage = container.querySelector(".user-block");
      const prose = container.querySelector(".commentary-block");
      const header = container.querySelector('[data-testid="thinking-header"]');
      expect(rule).not.toBeNull();
      expect(firstMessage).not.toBeNull();
      expect(header).not.toBeNull();
      expect(prose).not.toBeNull();
      expect(rule?.textContent).toContain("正在处理");
      expect(ruleClock(container)).toMatch(/s$/u);
      expect(container.querySelectorAll("[data-testid='process-status-rule']")).toHaveLength(1);
      expect(fold?.querySelector("[data-testid='process-status-rule']")).toBeNull();
      expect(foldLabel(header)).toBe("运行命令");
      expect(foldClock(header)).toBe("");
      expect(firstMessage?.textContent).toContain("继续");
      expect(prose?.textContent).toContain("我先读入口再跑检查。");
      expect(rule!.compareDocumentPosition(firstMessage!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(firstMessage!.compareDocumentPosition(prose!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(container.querySelector('.timeline-step[data-state="completed"] .tool-status')).toBeNull();
      expect(container.querySelector('.timeline-step[data-state="running"] .tool-status')).toBeNull();
      expect(container.querySelector('.timeline-step[data-state="failed"] .tool-status')?.textContent).toBe("失败");
      expect(rule?.querySelectorAll("time")).toHaveLength(1);
    } finally {
      await act(async () => root.unmount());
      container.remove();
      vi.useRealTimers();
    }
  });

  it("keeps the finished turn mounted when a follow-up message starts", async () => {
    const first: Block[] = [
      { id: "u1", kind: "user", content: "分析当前代码", state: "completed" },
      { id: "a1", kind: "assistant", runId: "run-1", content: "这是第一轮的最终回答。", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks: first, language: "zh-CN" })));
    const firstUser = container.querySelector(".session-turn-current .user-block");
    const firstAnswer = container.querySelector(".session-turn-current .assistant-block");
    expect(firstUser?.textContent).toContain("分析当前代码");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [
        ...first,
        { id: "u2", kind: "user", content: "继续核对边界", state: "submitted" },
      ],
      language: "zh-CN", activeRunId: "run-2", running: true, waitingForModel: true,
    })));

    expect(container.querySelector(".session-history-turn .user-block")).toBe(firstUser);
    expect(container.querySelector(".session-history-turn .assistant-block")).toBe(firstAnswer);
    const nextUser = container.querySelector(".session-turn-current .user-block");
    expect(nextUser?.textContent).toContain("继续核对边界");
    expect(container.querySelectorAll("[data-testid='process-status-rule']")).toHaveLength(1);
    expect(processRule(container)).not.toBeNull();
    expect(nextUser).not.toBeNull();
    expect(processRule(container)!.compareDocumentPosition(nextUser!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    await act(async () => root.unmount());
  });

  it("projects multi-turn sessions as continuous scroll with user bubbles and process folds", async () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "分析 Timeline 问题", state: "completed" },
      { id: "a1", kind: "assistant", runId: "run-1", content: "过程透明但难读", textPhase: "final_answer", state: "completed" },
      { id: "u2", kind: "user", content: "给出非 Timeline 方案", state: "completed" },
      {
        id: "tool", kind: "tool", runId: "run-2", title: "coding.read_file", state: "completed",
        data: { elapsedMs: "1200" },
      },
      { id: "a2", kind: "assistant", runId: "run-2", content: "采用工作文档投影", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", collapseCompletedProcess: true,
    })));

    expect(container.querySelector(".timeline-feed.session-document")).not.toBeNull();
    // History stays expanded for continuous scroll — no fold-row chrome.
    expect(container.querySelector(".session-history-turn > summary")).toBeNull();
    expect(container.querySelectorAll(".session-history-turn")).toHaveLength(1);
    expect(container.querySelector(".session-history-turn .session-turn-index")?.textContent).toBe("回合 01");
    expect(container.querySelector(".session-history-turn .user-block")?.textContent).toContain("分析 Timeline 问题");
    expect(container.querySelector(".session-history-turn .assistant-block")?.textContent).toContain("过程透明但难读");
    // Current turn uses the same user bubble, not a task-brief card.
    expect(container.querySelector(".session-turn-current.task-brief")).toBeNull();
    expect(container.querySelector(".session-turn-current .task-brief")).toBeNull();
    expect(container.querySelector(".session-turn-current .session-turn-index")?.textContent).toBe("当前回合");
    expect(container.querySelector(".session-turn-current .user-block")?.textContent).toContain("给出非 Timeline 方案");
    expect(container.querySelector(".session-turn-current .assistant-block")?.textContent).toContain("采用工作文档投影");
    const process = container.querySelector(".session-turn-current .process-fold");
    expect(process?.getAttribute("data-state")).toBe("completed");
    expect(foldOpen(process)).toBe(false);

    await act(async () => root.unmount());
  });

  it("does not show a live processing badge after the current-turn label", async () => {
    const blocks: Block[] = [
      { id: "u1", kind: "user", content: "上一问", state: "completed" },
      { id: "a1", kind: "assistant", runId: "run-1", content: "上一答", textPhase: "final_answer", state: "completed" },
      { id: "u2", kind: "user", content: "这一问", state: "completed" },
      { id: "a2", kind: "assistant", runId: "run-2", content: "这一答", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run-2", running: true,
    })));

    const label = container.querySelector(".session-turn-current .session-turn-label");
    expect(label?.textContent).toBe("当前回合");
    expect(label?.querySelector("em")).toBeNull();
    expect(label?.textContent).not.toContain("处理中");

    await act(async () => root.unmount());
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

    expect(container.querySelector(".tool-result")).toBeNull();
    const details = container.querySelector<HTMLDetailsElement>(".tool-block")!;
    await act(async () => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    const result = container.querySelector<HTMLElement>(".tool-result")!;
    expect(result.textContent).toContain("RUN  v4.1.10");
    expect(result.textContent).not.toContain("\u001b");
    expect(container.querySelector(".bui-tool-chip-detail")?.textContent).toBe("bun run test");
    expect(container.querySelector(".bui-tool-chip-detail")?.textContent).not.toContain("\u001b");
    expect(Array.from(result.querySelectorAll<HTMLElement>("span")).some((span) => Boolean(span.style.backgroundColor))).toBe(true);

    await act(async () => root.unmount());
  });

  it("focuses the durable message sequence selected by global search", async () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    useRuntimeStore.setState({ currentSessionId: "session-search", sessionSearchTarget: { sessionId: "session-search", sequence: 42 } });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const block: Block = { id: "answer-search", sequence: 42, kind: "assistant", content: "命中的持久化回答", state: "completed" };

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [block], language: "zh-CN" })));
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

    const target = container.querySelector<HTMLElement>('[data-session-sequence="42"]');
    expect(target?.textContent).toContain("命中的持久化回答");
    expect(target?.classList.contains("timeline-search-hit")).toBe(true);
    expect(target?.closest(".session-turn-current")?.classList.contains("timeline-search-reveal")).toBe(true);
    expect(scrollIntoView).toHaveBeenCalled();

    await act(async () => root.unmount());
    useRuntimeStore.getState().setSessionSearchTarget(null);
    container.remove();
  });

  it("threads live process rows onto one rail and staggers only newly appended steps", async () => {
    const base: Block[] = [
      { id: "think-1", kind: "thinking", runId: "run-rail", content: "先确认入口", state: "completed" },
      { id: "read-1", kind: "tool", runId: "run-rail", title: "coding.read_file", state: "completed" },
      { id: "shell-1", kind: "tool", runId: "run-rail", title: "coding.shell", state: "queued" },
    ];
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: base, language: "zh-CN", activeRunId: "run-rail", running: true,
    })));

    await openSteps(container);

    const rows = () => Array.from(container.querySelectorAll<HTMLElement>(".timeline-step-row"));
    // Reasoning leads the step as prose, so only the tools sit on the rail.
    expect(container.querySelector('.bui-thinking-panel[data-tab="reasoning"]')?.textContent)
      .toContain("先确认入口");
    // TOOL-001: a lone queued tool is displayed as executing, so it owns the spinner.
    expect(rows().map((row) => row.dataset.stepState)).toEqual(["done", "running"]);
    expect(rows().map((row) => row.dataset.stepEdge)).toEqual(["first", "last"]);
    expect(container.querySelectorAll(".bui-step-spinner")).toHaveLength(1);
    expect(rows().map((row) => row.style.getPropertyValue("--step-enter-delay")))
      .toEqual(["0ms", "120ms"]);
    expect(rows().every((row) => row.getAttribute("role") === "listitem")).toBe(true);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [
        ...base.map((block) => block.id === "shell-1" ? { ...block, state: "completed" } : block),
        { id: "search-1", kind: "tool", runId: "run-rail", title: "coding.search", state: "queued" },
      ],
      language: "zh-CN",
      activeRunId: "run-rail",
      running: true,
    })));

    await openSteps(container);

    expect(rows().map((row) => row.dataset.stepState)).toEqual(["done", "done", "running"]);
    expect(container.querySelectorAll(".bui-step-spinner")).toHaveLength(1);
    expect(rows().map((row) => row.dataset.stepEdge)).toEqual(["first", undefined, "last"]);
    expect(rows().map((row) => row.dataset.stepEnter)).toEqual([undefined, undefined, "true"]);
    expect(rows().at(-1)?.style.getPropertyValue("--step-enter-delay")).toBe("0ms");

    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps a completed virtualized trail on the rail without replaying entrances", async () => {
    const blocks: Block[] = Array.from({ length: 24 }, (_, index) => ({
      id: `tool-${index}`,
      kind: "tool",
      runId: "run-done",
      title: index % 2 === 0 ? "coding.read_file" : "coding.search",
      state: index === 5 ? "failed" : "completed",
    }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks, language: "zh-CN", activeRunId: "run-done",
    })));

    const list = container.querySelector('[data-testid="deferred-process-list"]');
    expect(list).not.toBeNull();
    const rows = Array.from(container.querySelectorAll<HTMLElement>(".timeline-step-row"));
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.every((row) => row.dataset.stepEnter === undefined)).toBe(true);
    expect(container.querySelectorAll(".bui-step-spinner")).toHaveLength(0);
    expect(rows[0]?.dataset.stepEdge).toBe("first");
    expect(rows.filter((row) => row.dataset.stepState === "failed")).toHaveLength(1);
    expect(rows.filter((row) => row.dataset.stepEdge === "only")).toHaveLength(0);

    await act(async () => root.unmount());
    container.remove();
  });
});
