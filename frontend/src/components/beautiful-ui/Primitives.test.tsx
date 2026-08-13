import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { ApprovalCard, StreamingText, TaskRow, ThinkingState, ToolRow } from "./Primitives";

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
      <ThinkingState active expanded={false} label="思考中" panelId="trace" onToggle={() => { toggled = true; }}><p>trace</p></ThinkingState>
      <StreamingText><span>answer</span></StreamingText>
      <ToolRow className="timeline-step" state="running"><summary>tool</summary></ToolRow>
      <ApprovalCard state="pending" data-risk="high">approval</ApprovalCard>
      <TaskRow state="in_progress">task</TaskRow>
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
  });
});
