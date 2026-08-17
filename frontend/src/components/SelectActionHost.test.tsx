import { act, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { SelectActionHost } from "./SelectActionHost";
import { buildSelectActionPrompt } from "./selectAction";

describe("SelectActionHost", () => {
  const mounted: Array<() => void> = [];
  afterEach(() => {
    mounted.splice(0).forEach((cleanup) => cleanup());
    window.getSelection()?.removeAllRanges();
  });

  async function renderHost(onSubmit: (text: string) => void = () => undefined) {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    function Harness() {
      const rootRef = useRef<HTMLDivElement>(null);
      return <div>
        <div className="transcript-viewport" ref={rootRef}>
          <article className="assistant-block markdown"><p>Pistachio holds the top slot all weekend.</p></article>
          <section className="reasoning-trace"><div className="reasoning-body">do not send thinking</div></section>
        </div>
        <SelectActionHost rootRef={rootRef} language="zh-CN" onSubmit={onSubmit} />
      </div>;
    }
    await act(async () => root.render(<Harness />));
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
      document.querySelector(".bui-action-island")?.remove();
    });
    return container;
  }

  async function selectAssistant(container: HTMLElement) {
    const node = container.querySelector(".assistant-block p")?.firstChild;
    if (!node) throw new Error("missing assistant text");
    const range = document.createRange();
    range.selectNodeContents(node);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    await act(async () => {
      document.dispatchEvent(new Event("selectionchange"));
      await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    });
  }

  it("shows the island on assistant selection and dismisses on Escape", async () => {
    const container = await renderHost();
    expect(document.querySelector(".bui-action-island")).toBeNull();
    await selectAssistant(container);
    const island = document.querySelector<HTMLElement>(".bui-action-island");
    expect(island?.getAttribute("role")).toBe("toolbar");
    expect(island?.textContent).toContain("解释");
    expect(island?.textContent).toContain("改进");
    expect(island?.querySelector("input")?.getAttribute("placeholder")).toBe("描述编辑");
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(document.querySelector(".bui-action-island")).toBeNull();
  });

  it("dismisses when the native selection becomes empty", async () => {
    const container = await renderHost();
    await selectAssistant(container);
    expect(document.querySelector(".bui-action-island")).not.toBeNull();
    document.querySelector<HTMLElement>(".bui-action-island")?.blur();
    window.getSelection()?.removeAllRanges();
    await act(async () => {
      document.dispatchEvent(new Event("selectionchange"));
      await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    });
    expect(document.querySelector(".bui-action-island")).toBeNull();
  });

  it("does not open on thinking text", async () => {
    const container = await renderHost();
    const node = container.querySelector(".reasoning-body")?.firstChild;
    if (!node) throw new Error("missing thinking text");
    const range = document.createRange();
    range.selectNodeContents(node);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    await act(async () => {
      document.dispatchEvent(new Event("selectionchange"));
      await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    });
    expect(document.querySelector(".bui-action-island")).toBeNull();
  });

  it("sends explain, improve, and describe-edit through the provided submit path with a quote", async () => {
    const sent: string[] = [];
    const container = await renderHost((text) => sent.push(text));
    const selected = "Pistachio holds the top slot all weekend.";
    await selectAssistant(container);
    await act(async () => {
      document.querySelector<HTMLButtonElement>(".bui-action-island-explain")?.click();
    });
    expect(sent).toEqual([buildSelectActionPrompt("zh-CN", "explain", selected)]);
    expect(sent[0]).toContain("> Pistachio holds the top slot all weekend.");

    await selectAssistant(container);
    await act(async () => {
      document.querySelector<HTMLButtonElement>(".bui-action-island-improve")?.click();
    });
    expect(sent[1]).toBe(buildSelectActionPrompt("zh-CN", "improve", selected));

    await selectAssistant(container);
    await act(async () => {
      const input = document.querySelector<HTMLInputElement>(".bui-action-island-input")!;
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
      setter?.call(input, "改成更口语");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      document.querySelector<HTMLButtonElement>(".bui-action-island-submit")?.click();
    });
    expect(sent[2]).toBe(buildSelectActionPrompt("zh-CN", "describe", selected, "改成更口语"));
    expect(sent[2]).toContain("要求：改成更口语");
    expect(document.querySelector(".bui-action-island")).toBeNull();
  });

  it("keeps describe-edit on the same submit callback the host was given", async () => {
    const calls: string[] = [];
    function ComposerPath() {
      const rootRef = useRef<HTMLDivElement>(null);
      const [prompt, setPrompt] = useState("");
      const submitTurn = (text: string) => {
        calls.push(text);
        setPrompt(text);
      };
      return <div>
        <div className="transcript-viewport" ref={rootRef}>
          <article className="assistant-block markdown"><p>Keep the quoted passage.</p></article>
        </div>
        <textarea data-testid="composer" value={prompt} readOnly />
        <SelectActionHost rootRef={rootRef} language="en" onSubmit={submitTurn} />
      </div>;
    }
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ComposerPath />));
    mounted.push(() => {
      act(() => root.unmount());
      container.remove();
      document.querySelector(".bui-action-island")?.remove();
    });
    const node = container.querySelector(".assistant-block p")?.firstChild;
    const range = document.createRange();
    range.selectNodeContents(node!);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    await act(async () => {
      document.dispatchEvent(new Event("selectionchange"));
      await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    });
    await act(async () => {
      document.querySelector<HTMLButtonElement>(".bui-action-island-explain")?.click();
    });
    expect(calls).toHaveLength(1);
    expect(calls[0]).toContain("> Keep the quoted passage.");
    expect(container.querySelector("textarea")?.value).toBe(calls[0]);
  });
});
