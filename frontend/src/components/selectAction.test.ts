import { describe, expect, it } from "vitest";
import {
  actionIslandPosition,
  buildSelectActionPrompt,
  classifyTranscriptSelection,
  quoteAsMarkdown,
} from "./selectAction";

describe("select action prompts", () => {
  it("quotes the selected passage for explain, improve, and describe-edit", () => {
    const selected = "Pistachio holds the top slot.\nChurn it Saturday.";
    const quote = quoteAsMarkdown(selected);
    expect(quote).toBe("> Pistachio holds the top slot.\n> Churn it Saturday.");
    expect(buildSelectActionPrompt("zh-CN", "explain", selected)).toContain(quote);
    expect(buildSelectActionPrompt("zh-CN", "explain", selected)).toContain("请解释");
    expect(buildSelectActionPrompt("zh-CN", "improve", selected)).toContain("请改进");
    expect(buildSelectActionPrompt("zh-CN", "describe", selected, "改成更口语")).toContain("要求：改成更口语");
    expect(buildSelectActionPrompt("en", "explain", selected)).toContain("Explain the following passage");
    expect(buildSelectActionPrompt("en", "improve", selected)).toContain("Improve and rewrite");
    expect(buildSelectActionPrompt("en", "describe", selected, "shorten")).toContain("Request: shorten");
  });

  it("places the island below the highlight unless that would cover the composer", () => {
    const selection = { left: 200, right: 360, top: 120, bottom: 140, width: 160, height: 20 };
    expect(actionIslandPosition(selection, { width: 800, height: 700 }, { islandWidth: 400, islandHeight: 40 })).toMatchObject({
      placement: "below",
      top: 148,
    });
    expect(actionIslandPosition(selection, { width: 800, height: 700 }, {
      islandWidth: 400, islandHeight: 40, composerTop: 170,
    })).toMatchObject({ placement: "above" });
  });
});

describe("select action targeting", () => {
  function fixture() {
    const root = document.createElement("div");
    root.className = "transcript-viewport";
    root.innerHTML = `
      <article class="assistant-block markdown"><p>assistant final answer</p></article>
      <article class="commentary-block markdown"><p>progress commentary</p></article>
      <article class="user-block"><p>user bubble</p></article>
      <section class="reasoning-trace"><div class="reasoning-body">hidden thinking</div></section>
      <details class="tool-block"><pre class="tool-log">tool dump output</pre></details>
      <article class="subagent-wake-notice"><p>wake notice</p></article>
    `;
    document.body.append(root);
    return root;
  }

  function selectText(root: HTMLElement, selector: string) {
    const node = root.querySelector(selector)?.firstChild;
    if (!node || node.nodeType !== Node.TEXT_NODE) throw new Error(`no text in ${selector}`);
    const range = document.createRange();
    range.selectNodeContents(node);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    return selection;
  }

  it("accepts assistant, commentary, and user prose and ignores thinking, tools, and wake notices", () => {
    const root = fixture();
    expect(classifyTranscriptSelection(selectText(root, ".assistant-block p"), root).kind).toBe("hit");
    expect(classifyTranscriptSelection(selectText(root, ".commentary-block p"), root).kind).toBe("hit");
    expect(classifyTranscriptSelection(selectText(root, ".user-block p"), root).kind).toBe("hit");
    expect(classifyTranscriptSelection(selectText(root, ".reasoning-body"), root).kind).toBe("miss");
    expect(classifyTranscriptSelection(selectText(root, ".tool-log"), root).kind).toBe("miss");
    expect(classifyTranscriptSelection(selectText(root, ".subagent-wake-notice p"), root).kind).toBe("miss");
    root.remove();
  });

  it("ignores a selection that lives inside the island so the snapshot can stay", () => {
    const root = fixture();
    const island = document.createElement("div");
    island.className = "bui-action-island";
    island.innerHTML = `<input class="bui-action-island-input" value="描述编辑" />`;
    document.body.append(island);
    const input = island.querySelector("input")!;
    input.focus();
    input.setSelectionRange(0, 2);
    expect(classifyTranscriptSelection(window.getSelection(), root)).toEqual({ kind: "ignore" });
    island.remove();
    root.remove();
  });
});
