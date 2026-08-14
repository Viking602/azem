import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useRuntimeStore } from "../store";
import type { AgentState, Block } from "../types";
import { TimelineFeed } from "./Timeline";

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
      expect(container.querySelectorAll(".timeline-step")).toHaveLength(1);
      expect(container.querySelector(".reasoning-placeholder")).toBeNull();
      expect(container.querySelector(".reasoning-trace")).toBeNull();
      expect(container.textContent).not.toContain("思考中");

      const summary = card?.querySelector<HTMLButtonElement>(".subagent-run-card-summary");
      await act(async () => summary?.click());
      expect(summary?.getAttribute("aria-expanded")).toBe("true");
      expect(card?.querySelectorAll(".subagent-run-row")).toHaveLength(4);
      for (const description of descriptions) expect(card?.textContent).toContain(description);

      await act(async () => card?.querySelector<HTMLButtonElement>(".subagent-run-row")?.click());
      expect(useRuntimeStore.getState().selectedAgentId).toBe("agent-0");

      await act(async () => useRuntimeStore.setState({
        agents: agents.map((agent) => ({ ...agent, state: "completed", summary: "审查完成" })),
      }));
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
      expect(container.querySelector(".process-fold")).toBeNull();
      expect(container.textContent).toContain("分派专项审查");
      expect(container.querySelector(".model-progress-step time")?.textContent).toBe("2s");

      await act(async () => { vi.advanceTimersByTime(2100); });
      expect(container.querySelector(".model-progress-step time")?.textContent).toBe("4s");

      await act(async () => root.render(createElement(TimelineFeed, {
        blocks: [progress, { ...tool, state: "completed", data: { elapsedMs: "4300" } }],
        language: "zh-CN", running: false, foldActiveProcess: true,
      })));
      expect(container.querySelector(".process-fold")?.getAttribute("data-state")).toBe("completed");
      expect(container.querySelector(".process-fold-label")?.textContent).toBe("已处理");
      expect(container.querySelector(".process-fold > summary time")?.textContent).toBe("4s");
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
    expect(container.querySelector(".process-fold")).toBeNull();
    expect(container.textContent).toContain("核对边界");

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [progress, { ...tool, state: "completed" }], language: "zh-CN",
      foldActiveProcess: true, collapseCompletedProcess: true,
    })));
    const process = container.querySelector<HTMLDetailsElement>(".process-fold");
    expect(process?.open).toBe(false);
    expect(process?.querySelector(".process-fold-label")?.textContent).toBe("已处理");

    await act(async () => process?.querySelector<HTMLElement>("summary")?.click());
    expect(process?.open).toBe(true);
    expect(process?.textContent).toContain("核对边界");
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
      expect(container.querySelector(".model-progress-step time")?.textContent).toBe("1m35s");

      await act(async () => { vi.advanceTimersByTime(2_100); });
      expect(container.querySelector(".model-progress-step time")?.textContent).toBe("1m37s");
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

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [thinking, answer], language: "zh-CN", activeRunId: "run", running: true })));
    const streamingNode = container.querySelector(".assistant-block");
    expect(streamingNode).not.toBeNull();
    expect(streamingNode?.closest(".process-entries")).toBeNull();

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [thinking, { ...answer, state: "completed" }], language: "zh-CN" })));
    expect(container.querySelector(".assistant-block")).toBe(streamingNode);
    expect(container.querySelector(".assistant-block")?.closest(".process-fold")).toBeNull();
    await act(async () => root.unmount());
  });

  it("shows the newest completed process as the conversation context while older work stays folded", async () => {
    const blocks: Block[] = [
      { id: "old-thinking", kind: "thinking", runId: "old-run", content: "旧过程", state: "completed" },
      { id: "old-answer", kind: "assistant", runId: "old-run", content: "旧回答", textPhase: "final_answer", state: "completed" },
      { id: "new-thinking", kind: "thinking", runId: "new-run", content: "新过程", state: "completed", data: { elapsedMs: "2300" } },
      { id: "new-answer", kind: "assistant", runId: "new-run", content: "新回答", textPhase: "final_answer", state: "completed" },
    ];
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));

    const folds = Array.from(container.querySelectorAll<HTMLDetailsElement>(".process-fold"));
    expect(folds).toHaveLength(2);
    expect(folds[0]?.open).toBe(false);
    expect(folds[1]?.open).toBe(true);
    expect(folds[1]?.querySelector(".process-entries")?.textContent).toContain("新过程");
    expect(Array.from(container.querySelectorAll(".final-answer-marker")).map((node) => node.textContent))
      .toEqual(["最终回答", "最终回答"]);

    await act(async () => {
      folds[1]!.open = false;
      folds[1]!.dispatchEvent(new Event("toggle"));
    });
    await act(async () => root.render(createElement(TimelineFeed, { blocks, language: "zh-CN" })));
    expect(container.querySelectorAll<HTMLDetailsElement>(".process-fold")[1]?.open).toBe(false);

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

  it("visually separates an unresolved unphased stream from settled final prose", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const pending: Block = {
      id: "pending-phase", kind: "assistant", runId: "run", content: "最后确认测试和 diff。",
      textPhase: "final_answer", state: "streaming", data: { textPhasePending: "true" },
    };

    await act(async () => root.render(createElement(TimelineFeed, {
      blocks: [pending], language: "zh-CN", activeRunId: "run", running: true,
    })));
    expect(container.querySelector(".assistant-block.phase-pending")).not.toBeNull();
    expect(container.querySelector(".assistant-block.phase-pending .streaming-text p")?.textContent).toBe("最后确认测试和 diff。");

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

    expect(Array.from(container.querySelectorAll(".commentary-label")).map((node) => node.textContent)).toEqual(["视觉核对"]);
    expect(container.textContent).not.toContain("progress");
    expect(container.textContent).not.toContain("进度更新");
    await act(async () => root.unmount());
  });

  it("uses model commentary as the primary progress step and nests its tools", async () => {
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
      blocks, language: "zh-CN", activeRunId: "run", running: true,
    })));

    const steps = Array.from(container.querySelectorAll<HTMLDetailsElement>(".model-progress-step"));
    expect(steps).toHaveLength(2);
    expect(steps[0]?.querySelector("strong")?.textContent).toBe("读取当前前端结构");
    expect(steps[0]?.querySelector("small")?.textContent).toBe("App、Sidebar、Timeline 与 Inspector");
    expect(steps[0]?.querySelector("time")?.textContent).toBe("1s");
    expect(steps[0]?.getAttribute("data-state")).toBe("settled");
    expect(steps[0]?.querySelector(".timeline-step-neutral-dot")).not.toBeNull();
    expect(steps[0]?.querySelector(".timeline-step-mark svg")).toBeNull();
    expect(steps[1]?.getAttribute("data-state")).toBe("running");
    expect(steps[1]?.getAttribute("aria-current")).toBe("step");
    expect(steps[1]?.querySelector("time")?.textContent).toBe("4s");
    expect(container.querySelector(".commentary-block")?.textContent).toContain("不能被前端擅自截成标题");
    expect(steps[0]?.querySelector(".model-progress-tools")).toBeNull();
    expect(steps[1]?.open).toBe(true);
    expect(steps[1]?.querySelector(".model-progress-tools")?.textContent).toContain("编辑文件");

    await act(async () => {
      steps[0]!.open = true;
      steps[0]!.dispatchEvent(new Event("toggle"));
    });
    expect(steps[0]?.querySelector(".model-progress-tools")?.textContent).toContain("读取文件");
    expect(container.querySelectorAll(".process-entries > .tool-block")).toHaveLength(0);

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

    const step = container.querySelector<HTMLDetailsElement>(".model-progress-step");
    expect(step?.open).toBe(true);
    expect(step?.querySelector("strong")?.textContent).toBe("调查运行时入口");
    expect(step?.querySelectorAll(".model-progress-tools .reasoning-trace")).toHaveLength(2);
    expect(step?.querySelector('.model-progress-tools .tool-block[data-state="running"]')).not.toBeNull();
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

    const step = container.querySelector<HTMLDetailsElement>(".model-progress-step");
    expect(step?.querySelector("strong")?.textContent).toBe("加载深审规则");
    expect(step?.querySelector("small")?.textContent).toBe("读取整库审计模式后建立证据清单。");
    expect(step?.querySelector("time")?.textContent).toBe("6s");
    expect(container.querySelector(".commentary-block")).toBeNull();
    expect(container.textContent).not.toContain("🥷");

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

    const progress = container.querySelector<HTMLDetailsElement>(".model-progress-step");
    expect(progress?.getAttribute("data-state")).toBe("settled");
    expect(progress?.querySelector(".timeline-step-neutral-dot")).not.toBeNull();
    expect(progress?.querySelector(":scope > summary .timeline-step-mark svg")).toBeNull();

    await act(async () => {
      progress!.open = true;
      progress!.dispatchEvent(new Event("toggle"));
    });
    expect(progress?.querySelector('.tool-block[data-state="failed"] .tool-status')?.textContent).toBe("失败");

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

    await act(async () => root.render(createElement(TimelineFeed, { blocks: [{ ...live, state: "completed" }], language: "zh-CN" })));
    expect(container.querySelector(".streaming-text")).toBeNull();
    expect(container.querySelector(".assistant-block h2")?.textContent).toBe("检查结果");
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

    expect(container.querySelectorAll(".timeline-step-list")).toHaveLength(1);
    expect(container.querySelectorAll(".timeline-step")).toHaveLength(6);
    expect(container.querySelectorAll(".tool-group, .process-entries > .tool-block")).toHaveLength(0);
    expect(container.textContent).toContain("需要审批");
    expect(container.textContent).toContain("排队中");
    expect(container.querySelector('.pending-file-edit[data-state="awaiting_approval"]')).not.toBeNull();
    expect(container.querySelector('.pending-file-edit .work-entry-icon[data-icon="shield"]')).not.toBeNull();
    expect(container.textContent).not.toContain("package main");
    expect(container.querySelector('.timeline-step[data-state="running"]')?.getAttribute("aria-current")).toBe("step");
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
    const process = container.querySelector<HTMLDetailsElement>(".session-turn-current .process-fold");
    expect(process?.getAttribute("data-state")).toBe("completed");
    expect(process?.open).toBe(false);

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
    expect(container.querySelector(".tool-preview")?.textContent).not.toContain("\u001b");
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
});
