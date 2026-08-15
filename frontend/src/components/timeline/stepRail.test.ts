import { describe, expect, it } from "vitest";
import type { Block } from "../../types";
import {
  blocksMarkState, STEP_STAGGER_MAX, STEP_STAGGER_MS, stepEdge, stepEnterDelay,
  stepEntranceDelays, stepMarkState,
} from "./stepRail";

function block(id: string, state: string, kind: Block["kind"] = "tool"): Block {
  return { id, kind, title: "coding.read_file", content: "", state };
}

describe("step mark state", () => {
  it("maps every live tool lifecycle state onto a rail node", () => {
    expect(stepMarkState("running")).toBe("running");
    expect(stepMarkState("streaming")).toBe("running");
    expect(stepMarkState("started")).toBe("running");
    expect(stepMarkState("progress")).toBe("running");
    expect(stepMarkState("queued")).toBe("pending");
    expect(stepMarkState("awaiting_approval")).toBe("pending");
    expect(stepMarkState("reviewing_approval")).toBe("pending");
    expect(stepMarkState("failed")).toBe("failed");
    expect(stepMarkState("cancelled")).toBe("failed");
    expect(stepMarkState("completed")).toBe("done");
    expect(stepMarkState(undefined)).toBe("done");
  });

  it("keeps parallel dispatch honest instead of forcing one active row", () => {
    const parallel = [block("a", "running"), block("b", "running"), block("c", "completed")];
    expect(parallel.map((entry) => stepMarkState(entry.state))).toEqual(["running", "running", "done"]);
    expect(blocksMarkState(parallel)).toBe("running");
    expect(blocksMarkState([block("a", "completed"), block("b", "failed")])).toBe("failed");
    expect(blocksMarkState([block("a", "queued"), block("b", "awaiting_approval")])).toBe("pending");
    expect(blocksMarkState([block("a", "queued"), block("b", "completed")])).toBe("done");
    expect(blocksMarkState([])).toBe("done");
  });
});

describe("step rail edges", () => {
  it("clips only the outermost segments so the thread starts and ends on a node", () => {
    expect([0, 1, 2, 3].map((index) => stepEdge(index, 4))).toEqual(["first", undefined, undefined, "last"]);
    expect(stepEdge(0, 1)).toBe("only");
    expect(stepEdge(0, 0)).toBe("only");
  });

  it("uses absolute indexes so a virtualized window keeps an unbroken rail", () => {
    const total = 40;
    const window = [12, 13, 14].map((index) => stepEdge(index, total));
    expect(window).toEqual([undefined, undefined, undefined]);
    expect(stepEdge(total - 1, total)).toBe("last");
  });
});

describe("step entrance stagger", () => {
  it("delays each newly appended row and caps the cascade", () => {
    expect(stepEnterDelay(0)).toBe(0);
    expect(stepEnterDelay(2)).toBe(2 * STEP_STAGGER_MS);
    expect(stepEnterDelay(STEP_STAGGER_MAX + 5)).toBe(STEP_STAGGER_MAX * STEP_STAGGER_MS);

    const first = stepEntranceDelays(["a", "b", "c"], null);
    expect([...first.entries()]).toEqual([["a", 0], ["b", 120], ["c", 240]]);
  });

  it("never replays rows that were already on screen", () => {
    const seen = new Set(["a", "b", "c"]);
    const appended = stepEntranceDelays(["a", "b", "c", "d", "e"], seen);
    expect([...appended.entries()]).toEqual([["d", 0], ["e", 120]]);

    const scrolled = stepEntranceDelays(["b", "c"], seen);
    expect(scrolled.size).toBe(0);
  });
});
