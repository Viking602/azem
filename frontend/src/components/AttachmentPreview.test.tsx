import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { attachmentDataURL } from "../bridge";
import type { Attachment } from "../types";
import AttachmentPreview, { clearAttachmentPreviewCache } from "./AttachmentPreview";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", async (importOriginal) => {
  const original = await importOriginal<typeof import("../bridge")>();
  return { ...original, attachmentDataURL: vi.fn() };
});

const attachment: Attachment = { id: "image-1", name: "screen.png", mimeType: "image/png", path: "/tmp/screen.png", size: 42 };
let container: HTMLDivElement | null = null;
let root: Root | null = null;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  document.querySelector(".attachment-lightbox")?.remove();
  document.body.style.removeProperty("overflow");
  clearAttachmentPreviewCache();
  vi.mocked(attachmentDataURL).mockReset();
  container = null;
  root = null;
});

describe("AttachmentPreview", () => {
  it("renders the real image and opens the full-size viewer", async () => {
    vi.mocked(attachmentDataURL).mockResolvedValue("data:image/png;base64,iVBORw==");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    await act(async () => root?.render(<AttachmentPreview attachment={attachment} sessionId="session-1" language="zh-CN" variant="message" />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    const thumbnail = container.querySelector<HTMLImageElement>(".attachment-preview-image img");
    expect(thumbnail?.src).toBe("data:image/png;base64,iVBORw==");
    expect(attachmentDataURL).toHaveBeenCalledWith("session-1", attachment);
    Object.defineProperty(thumbnail!, "naturalWidth", { configurable: true, value: 800 });
    Object.defineProperty(thumbnail!, "naturalHeight", { configurable: true, value: 400 });
    await act(async () => thumbnail!.dispatchEvent(new Event("load")));
    const preview = container.querySelector<HTMLElement>(".attachment-preview");
    expect(preview?.getAttribute("data-sized")).toBe("true");
    expect(preview?.style.getPropertyValue("--preview-display-w")).toBe("420px");
    expect(preview?.style.getPropertyValue("--preview-display-h")).toBe("210px");

    await act(async () => container?.querySelector<HTMLButtonElement>(".attachment-preview-image")?.click());
    expect(document.querySelector('[role="dialog"] .attachment-lightbox-canvas img')?.getAttribute("src")).toBe("data:image/png;base64,iVBORw==");

    await act(async () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(document.querySelector(".attachment-lightbox")).toBeNull();
  });

  it("keeps removal separate from opening the image", async () => {
    vi.mocked(attachmentDataURL).mockResolvedValue("data:image/png;base64,iVBORw==");
    const remove = vi.fn();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<AttachmentPreview attachment={attachment} sessionId="session-1" language="zh-CN" variant="composer" onRemove={remove} />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));

    await act(async () => container?.querySelector<HTMLButtonElement>(".attachment-preview-remove")?.click());
    expect(remove).toHaveBeenCalledOnce();
    expect(document.querySelector(".attachment-lightbox")).toBeNull();
  });

  it("keeps a small conversation image at its natural size", async () => {
    vi.mocked(attachmentDataURL).mockResolvedValue("data:image/png;base64,iVBORw==");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(<AttachmentPreview attachment={attachment} sessionId="session-1" language="zh-CN" variant="message" />));
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    const thumbnail = container.querySelector<HTMLImageElement>(".attachment-preview-image img")!;
    Object.defineProperty(thumbnail, "naturalWidth", { configurable: true, value: 96 });
    Object.defineProperty(thumbnail, "naturalHeight", { configurable: true, value: 64 });
    await act(async () => thumbnail.dispatchEvent(new Event("load")));
    const preview = container.querySelector<HTMLElement>(".attachment-preview");
    expect(preview?.style.getPropertyValue("--preview-display-w")).toBe("96px");
    expect(preview?.style.getPropertyValue("--preview-display-h")).toBe("64px");
  });
});
