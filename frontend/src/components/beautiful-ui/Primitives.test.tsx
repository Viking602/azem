import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodeBlock, parseCodeFenceInfo } from "./CodeBlock";
import { ActionIsland, ApprovalCard, LoadingState, PromptBar, StreamingText, TaskRow, ThinkingState, ToolRow } from "./Primitives";
import { StepRow } from "./StepRow";
import { FileChangePills, ToolChip } from "./ToolChip";

describe("Beautiful UI primitives", () => {
  const mounted: Array<() => void> = [];
  afterEach(() => mounted.splice(0).forEach((cleanup) => cleanup()));

  async function render(node: React.ReactNode) {
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(node));
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
    });
    return container;
  }

  it("keeps the copied interaction states semantic and accessible", async () => {
    let toggled = false;
    const container = await render(<>
      <ThinkingState
        active
        expanded={false}
        label="思考"
        panelId="trace"
        tabs={[
          { id: "steps", label: "步骤" },
          { id: "reasoning", label: "推理" },
          { id: "search", label: "搜索", empty: true },
          { id: "coding", label: "编码", empty: true },
        ]}
        activeTab="steps"
        onToggle={() => { toggled = true; }}
      ><p>trace</p></ThinkingState>
      <ThinkingState active expanded={false} label="思考" disabled />
      <LoadingState label="加载中" />
      <StreamingText><span>answer</span></StreamingText>
      <ToolRow className="timeline-step" state="running"><summary>tool</summary></ToolRow>
      <ApprovalCard state="pending" data-risk="high">approval</ApprovalCard>
      <TaskRow state="in_progress">task</TaskRow>
      <PromptBar className="composer-card">prompt</PromptBar>
    </>);

    const thinking = container.querySelector<HTMLButtonElement>(".reasoning-summary")!;
    expect(thinking.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector<HTMLDivElement>("#trace")?.hidden).toBe(true);
    await act(async () => thinking.click());
    expect(toggled).toBe(true);
    expect(container.querySelector(".bui-streaming-text.active")).not.toBeNull();
    expect(container.querySelector(".bui-tool-row")?.getAttribute("data-state")).toBe("running");
    expect(container.querySelector(".bui-approval-card")?.getAttribute("data-risk")).toBe("high");
    expect(container.querySelector(".bui-task-row")?.getAttribute("data-status")).toBe("in_progress");
    expect(container.querySelector(".bui-task-row")?.getAttribute("data-variant")).toBe("capsules");
    const wait = container.querySelector(".reasoning-summary:disabled")?.closest(".bui-thinking-state");
    expect(wait?.className).toContain("streaming");
    expect(wait?.querySelector(".azem-thinking-mark")).not.toBeNull();
    expect(wait?.querySelector(".reasoning-label-base")?.textContent).toBe("思考");
    expect(wait?.querySelector(".reasoning-chevron")?.getAttribute("data-reserved")).toBe("true");
    expect(container.querySelector(".bui-thinking-pill")).toBeNull();
    expect(container.querySelector(".bui-loading-grid")?.childElementCount).toBe(9);
    expect(container.querySelector(".bui-thinking-state .bui-loading-grid")).toBeNull();
    expect(Array.from(container.querySelectorAll(".bui-thinking-tab")).map((tab) => tab.textContent)).toEqual(["步骤", "推理"]);
    expect(container.querySelector('.bui-thinking-tab[aria-selected="true"]')?.textContent).toBe("步骤");
    expect(container.querySelectorAll(".bui-thinking-tab[disabled]")).toHaveLength(0);
    expect(container.querySelector(".bui-prompt-bar.composer-card")?.textContent).toBe("prompt");
  });

  it("rolls the thinking label when its meaning changes and not on remount of the same wording", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(
      <ThinkingState active expanded={false} label="思考" labelKey="思考" />,
    ));
    const label = container.querySelector(".reasoning-label");
    expect(label?.querySelector(".reasoning-label-base")?.textContent).toBe("思考");
    expect(label?.classList.contains("rolling")).toBe(false);

    await act(async () => root.render(
      <ThinkingState active expanded={false} label="搜索了网页" labelKey="搜索了网页" />,
    ));
    expect(container.querySelector(".reasoning-label")).toBe(label);
    expect(label?.classList.contains("rolling")).toBe(true);
    expect(label?.querySelector(".reasoning-label-out")?.textContent).toBe("思考");
    expect(label?.querySelector(".reasoning-label-base")?.textContent).toBe("搜索了网页");

    await act(async () => root.unmount());
    container.remove();
  });

  it("renders quiet Codex wait as gray text with a cadenced sweep", async () => {
    vi.useFakeTimers();
    const container = await render(
      <ThinkingState active expanded={false} label="正在思考" quiet disabled />,
    );
    const wait = container.querySelector(".bui-thinking-state.quiet");
    const shimmer = container.querySelector(".bui-cadenced-shimmer");
    expect(wait).not.toBeNull();
    expect(shimmer?.querySelector(".bui-cadenced-shimmer-text")?.textContent).toBe("正在思考");
    expect(shimmer?.querySelector(".bui-cadenced-shimmer-sweep")).not.toBeNull();
    expect(wait?.querySelector(".azem-thinking-mark")).toBeNull();
    expect(wait?.querySelector(".bui-thinking-meta")).toBeNull();
    expect(wait?.querySelector(".reasoning-chevron")).toBeNull();
    expect(wait?.querySelector(".reasoning-label-sweep, .bui-shimmer-label, .reasoning-summary")).toBeNull();
    expect(shimmer?.classList.contains("bui-cadenced-shimmer-active")).toBe(false);
    await act(async () => { vi.advanceTimersByTime(600); });
    expect(shimmer?.classList.contains("bui-cadenced-shimmer-active")).toBe(true);
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(shimmer?.classList.contains("bui-cadenced-shimmer-active")).toBe(false);
    vi.useRealTimers();
  });

  it("rolls the quiet thinking label when a tool name replaces 正在思考", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(
      <ThinkingState active expanded={false} label="正在思考" labelKey="正在思考" quiet expandable />,
    ));
    const shimmer = container.querySelector(".bui-cadenced-shimmer");
    expect(shimmer?.querySelector(".bui-cadenced-shimmer-text")?.textContent).toBe("正在思考");
    expect(shimmer?.classList.contains("rolling")).toBe(false);

    await act(async () => root.render(
      <ThinkingState active expanded={false} label="搜索代码" labelKey="搜索代码" quiet expandable />,
    ));
    expect(container.querySelector(".bui-cadenced-shimmer")).toBe(shimmer);
    expect(shimmer?.classList.contains("rolling")).toBe(true);
    expect(shimmer?.querySelector(".reasoning-label-out")?.textContent).toBe("正在思考");
    expect(shimmer?.querySelector(".bui-cadenced-shimmer-text")?.textContent).toBe("搜索代码");
    expect(container.querySelector(".azem-thinking-mark")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });



  it("hides the thinking tablist when only reasoning exists", async () => {
    const container = await render(
      <ThinkingState
        active={false}
        expanded
        label="思考"
        meta={<time>0.8s</time>}
        panelId="reason-only"
        tabs={[
          { id: "steps", label: "步骤", empty: true },
          { id: "reasoning", label: "推理" },
          { id: "search", label: "搜索", empty: true },
          { id: "coding", label: "编码", empty: true },
        ]}
        activeTab="reasoning"
      >
        <p>trace</p>
      </ThinkingState>,
    );
    expect(container.querySelector(".bui-thinking-tabs")).toBeNull();
    expect(container.querySelector("#reason-only")?.textContent).toContain("trace");
  });

  it("renders the capsule Task Row shape: status mark, title, badge, and expand rail", async () => {
    let expanded = true;
    const container = await render(
      <TaskRow
        state="completed"
        title="Verified vendor records"
        metric="12 suppliers"
        statusLabel="Completed"
        expanded={expanded}
        onToggle={() => { expanded = false; }}
        steps={[
          { label: "Matched tax and contact IDs", value: "12/12" },
          { label: "Scored credit risk", value: "3 files" },
        ]}
      />,
    );

    const row = container.querySelector(".bui-task-row")!;
    expect(row.getAttribute("data-status")).toBe("completed");
    expect(row.getAttribute("data-variant")).toBe("capsules");
    expect(row.getAttribute("data-expanded")).toBe("true");
    expect(container.querySelector(".bui-task-mark")?.getAttribute("data-state")).toBe("completed");
    expect(container.querySelector(".bui-task-title")?.textContent).toBe("Verified vendor records");
    expect(container.querySelector(".bui-task-metric")?.textContent).toBe("12 suppliers");
    expect(container.querySelector(".bui-task-badge")?.textContent).toBe("Completed");
    const toggle = container.querySelector<HTMLButtonElement>(".bui-task-header")!;
    expect(toggle.tagName).toBe("BUTTON");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector(".bui-task-chevron")).not.toBeNull();
    expect(container.querySelector(".bui-task-rail")).not.toBeNull();
    expect(Array.from(container.querySelectorAll(".bui-task-steps li")).map((item) => item.textContent)).toEqual([
      "Matched tax and contact IDs12/12",
      "Scored credit risk3 files",
    ]);
    await act(async () => toggle.click());
    expect(expanded).toBe(false);
  });

  it("shows a numbered progress ring for an in-progress Task Row", async () => {
    const container = await render(
      <TaskRow state="in_progress" title="Mapped inventory signals" statusLabel="In progress" index={2} progress={0.75} />,
    );
    expect(container.querySelector(".bui-task-mark")?.getAttribute("data-state")).toBe("in_progress");
    expect(container.querySelector(".bui-task-mark em")?.textContent).toBe("2");
    expect(container.querySelector(".bui-task-ring-value")).not.toBeNull();
    expect(container.querySelector(".bui-task-chevron")).toBeNull();
    expect(container.querySelector(".bui-task-header")?.tagName).toBe("DIV");
  });

  it("renders the Select Action island with describe, explain, improve, and submit", async () => {
    const actions: string[] = [];
    const container = await render(
      <ActionIsland
        instruction=""
        onInstructionChange={() => undefined}
        onExplain={() => actions.push("explain")}
        onImprove={() => actions.push("improve")}
        onSubmit={() => actions.push("describe")}
        labels={{ toolbar: "选择行动", describe: "描述编辑", explain: "解释", improve: "改进", submit: "提交编辑" }}
      />,
    );
    const island = container.querySelector<HTMLElement>(".bui-action-island")!;
    expect(island.getAttribute("role")).toBe("toolbar");
    expect(island.getAttribute("aria-label")).toBe("选择行动");
    expect(island.querySelector("input")?.getAttribute("placeholder")).toBe("描述编辑");
    expect(island.querySelector(".bui-action-island-submit")?.hasAttribute("disabled")).toBe(true);
    await act(async () => container.querySelector<HTMLButtonElement>(".bui-action-island-explain")?.click());
    await act(async () => container.querySelector<HTMLButtonElement>(".bui-action-island-improve")?.click());
    expect(actions).toEqual(["explain", "improve"]);
  });

  it("renders Code Block chrome with filename, language, copy, and line numbers", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const container = await render(
      <CodeBlock filename="churn.ts" language="TypeScript" copyLabel="Copy" copiedLabel="Copied">
        {"export async function churnBatch() {\n  return true;\n}"}
      </CodeBlock>,
    );
    expect(container.querySelector(".bui-code-filename")?.textContent).toBe("churn.ts");
    expect(container.querySelector(".bui-code-lang")?.textContent).toBe("TypeScript");
    expect(Array.from(container.querySelectorAll(".bui-code-gutter span")).map((node) => node.textContent)).toEqual(["1", "2", "3"]);
    const copy = container.querySelector<HTMLButtonElement>(".bui-code-copy")!;
    expect(copy.textContent).toContain("Copy");
    await act(async () => copy.click());
    expect(writeText).toHaveBeenCalledWith("export async function churnBatch() {\n  return true;\n}");
    expect(copy.textContent).toContain("Copied");
  });

  it("treats a path-like fence info-string as the filename", () => {
    expect(parseCodeFenceInfo("churn.ts")).toEqual({ filename: "churn.ts", language: "TypeScript" });
    expect(parseCodeFenceInfo("typescript")).toEqual({ filename: "", language: "TypeScript" });
    expect(parseCodeFenceInfo("ts", "churn.ts")).toEqual({ filename: "churn.ts", language: "TypeScript" });
  });

  it("renders a Tool Chip row with icon, bold label, and detail chip", async () => {
    const container = await render(
      <ToolChip state="completed" kind="write" label="Write 204 lines" chip="ChurnSchedule.tsx" />,
    );
    expect(container.querySelector(".bui-tool-chip")?.getAttribute("data-state")).toBe("completed");
    expect(container.querySelector(".bui-tool-chip-label")?.textContent).toBe("Write 204 lines");
    expect(container.querySelector(".bui-tool-chip-detail")?.textContent).toBe("ChurnSchedule.tsx");
    expect(container.querySelector(".bui-tool-chip-icon")?.getAttribute("data-icon")).toBe("write");
    expect(container.querySelector(".bui-thinking-state")).toBeNull();
    expect(container.querySelector(".bui-task-row")).toBeNull();
  });

  it("renders file-change pills with add/del counts and a +more control", async () => {
    const container = await render(
      <FileChangePills
        language="en"
        files={[
          { path: "flavors.css", additions: 13, deletions: 0 },
          { path: "ChurnSchedule.tsx", additions: 74, deletions: 41 },
          { path: "menu.ts", additions: 8, deletions: 2 },
          { path: "extra-a.ts", additions: 1, deletions: 0 },
          { path: "extra-b.ts", additions: 1, deletions: 0 },
        ]}
      />,
    );
    const pills = Array.from(container.querySelectorAll(".bui-file-change-pill")).map((node) => node.textContent);
    expect(pills).toEqual(["flavors.css+13", "ChurnSchedule.tsx+74-41", "menu.ts+8-2"]);
    expect(container.querySelector(".bui-file-change-more")?.textContent).toBe("+2 more");
  });

  it("gives every step row one rail node and keeps the rail out of the accessibility tree", async () => {
    const container = await render(<div role="list">
      <StepRow mark="done" edge="first"><span>read</span></StepRow>
      <StepRow mark="running" edge={undefined} delayMs={120}><span>shell</span></StepRow>
      <StepRow mark="pending" edge={undefined}><span>queued</span></StepRow>
      <StepRow mark="failed" edge={undefined}><span>broken</span></StepRow>
      <StepRow mark="note" edge="last"><span>message</span></StepRow>
    </div>);

    const rows = Array.from(container.querySelectorAll<HTMLElement>(".timeline-step-row"));
    expect(rows.map((row) => row.dataset.stepState)).toEqual(["done", "running", "pending", "failed", "note"]);
    expect(rows.every((row) => row.getAttribute("role") === "listitem")).toBe(true);
    expect(container.querySelectorAll(".bui-step-mark[aria-hidden='true']")).toHaveLength(5);
    expect(container.querySelectorAll(".bui-step-mark-glyph")).toHaveLength(5);
    expect(container.querySelectorAll(".bui-step-spinner")).toHaveLength(1);
    expect(container.querySelectorAll(".bui-step-dot[data-variant='hollow']")).toHaveLength(1);
    expect(container.querySelectorAll(".bui-step-dot[data-variant='note']")).toHaveLength(1);
    expect(container.querySelectorAll(".bui-step-mark-glyph svg")).toHaveLength(2);

    expect(rows[1]?.dataset.stepEnter).toBe("true");
    expect(rows[1]?.style.getPropertyValue("--step-enter-delay")).toBe("120ms");
    expect(rows[0]?.dataset.stepEnter).toBeUndefined();
    expect(rows.at(-1)?.dataset.stepEdge).toBe("last");
  });

});
