import { describe, expect, it } from "vitest";
import {
  clampThreadReferencePanelRect,
  initialThreadReferencePanelRect,
  isThreadReferencePanelDragGesture,
  moveThreadReferencePanelRect,
  resizeThreadReferencePanelRect,
  threadReferenceResizeCursor,
} from "./threadReferencePanelGeometry";

describe("thread reference panel geometry", () => {
  it("places the default panel twelve pixels from the host's bottom-right corner", () => {
    expect(initialThreadReferencePanelRect({ width: 1000, height: 700 })).toEqual({
      left: 668,
      top: 488,
      width: 320,
      height: 200,
    });
  });

  it("keeps moved panels inside every host edge", () => {
    const rect = { left: 200, top: 150, width: 320, height: 200 };
    expect(moveThreadReferencePanelRect(rect, { x: -1000, y: -1000 }, { width: 1000, height: 700 })).toMatchObject({ left: 12, top: 12 });
    expect(moveThreadReferencePanelRect(rect, { x: 1000, y: 1000 }, { width: 1000, height: 700 })).toMatchObject({ left: 668, top: 488 });
  });

  it("resizes from edges while preserving the opposite edge and size limits", () => {
    const rect = { left: 200, top: 180, width: 400, height: 300 };
    expect(resizeThreadReferencePanelRect(rect, { edge: "se", deltaX: 200, deltaY: 400 }, { width: 1000, height: 900 })).toEqual({
      left: 200,
      top: 180,
      width: 480,
      height: 520,
    });
    expect(resizeThreadReferencePanelRect(rect, { edge: "nw", deltaX: 100, deltaY: 60 }, { width: 1000, height: 900 })).toEqual({
      left: 280,
      top: 240,
      width: 320,
      height: 240,
    });
  });

  it("shrinks below the nominal minimum only when the host cannot fit it", () => {
    expect(clampThreadReferencePanelRect(
      { left: 999, top: 999, width: 480, height: 520 },
      { width: 300, height: 180 },
    )).toEqual({ left: 12, top: 12, width: 276, height: 156 });
  });

  it("uses Synara's drag threshold and edge cursors", () => {
    expect(isThreadReferencePanelDragGesture({ x: 2, y: 3 })).toBe(false);
    expect(isThreadReferencePanelDragGesture({ x: 4, y: 0 })).toBe(true);
    expect(threadReferenceResizeCursor("n")).toBe("ns-resize");
    expect(threadReferenceResizeCursor("e")).toBe("ew-resize");
    expect(threadReferenceResizeCursor("ne")).toBe("nesw-resize");
    expect(threadReferenceResizeCursor("sw")).toBe("nesw-resize");
    expect(threadReferenceResizeCursor("nw")).toBe("nwse-resize");
  });
});
