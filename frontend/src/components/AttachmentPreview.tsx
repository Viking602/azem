import { ImagePlus, LoaderCircle, Maximize2, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { attachmentDataURL } from "../bridge";
import type { Attachment, Snapshot } from "../types";

type AttachmentPreviewProps = {
  attachment: Attachment;
  sessionId: string;
  language: Snapshot["language"];
  variant: "composer" | "message";
  onRemove?: () => void;
};

const previewSources = new Map<string, Promise<string>>();
const MESSAGE_PREVIEW_MAX_WIDTH = 420;
const MESSAGE_PREVIEW_MAX_HEIGHT = 320;

function messagePreviewSize(width: number, height: number) {
  const scale = Math.min(1, MESSAGE_PREVIEW_MAX_WIDTH / width, MESSAGE_PREVIEW_MAX_HEIGHT / height);
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

function previewKey(sessionId: string, attachment: Attachment) {
  return `${sessionId}:${attachment.id}:${attachment.path}:${attachment.size}`;
}

function loadPreview(sessionId: string, attachment: Attachment) {
  const key = previewKey(sessionId, attachment);
  const cached = previewSources.get(key);
  if (cached) return cached;
  const pending = attachmentDataURL(sessionId, attachment).catch((error) => {
    previewSources.delete(key);
    throw error;
  });
  previewSources.set(key, pending);
  return pending;
}

export function clearAttachmentPreviewCache() {
  previewSources.clear();
}

export default function AttachmentPreview({ attachment, sessionId, language, variant, onRemove }: AttachmentPreviewProps) {
  const [source, setSource] = useState("");
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [natural, setNatural] = useState<{ width: number; height: number } | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const viewLabel = language === "zh-CN" ? `查看完整图片：${attachment.name}` : `View full image: ${attachment.name}`;
  const closeLabel = language === "zh-CN" ? "关闭图片" : "Close image";

  useEffect(() => {
    let active = true;
    setLoading(true);
    setNatural(null);
    void loadPreview(sessionId, attachment)
      .then((value) => { if (active) setSource(value); })
      .catch(() => { if (active) setSource(""); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [attachment, sessionId]);

  useEffect(() => {
    if (!open) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", closeOnEscape);
    requestAnimationFrame(() => closeButton.current?.focus());
    return () => {
      document.removeEventListener("keydown", closeOnEscape);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  const frame = variant === "message" && natural ? messagePreviewSize(natural.width, natural.height) : null;
  return <>
    <figure
      className="attachment-preview"
      data-variant={variant}
      data-sized={frame ? "true" : undefined}
      style={frame ? { "--preview-display-w": `${frame.width}px`, "--preview-display-h": `${frame.height}px` } as React.CSSProperties : undefined}
    >
      <button type="button" className="attachment-preview-image" aria-label={viewLabel} title={viewLabel} disabled={!source} onClick={() => setOpen(true)}>
        {source
          ? <><img src={source} alt={attachment.name} onLoad={(event) => {
            const image = event.currentTarget;
            if (image.naturalWidth > 0 && image.naturalHeight > 0) setNatural({ width: image.naturalWidth, height: image.naturalHeight });
          }} /><span className="attachment-preview-expand" aria-hidden="true"><Maximize2 size={12} /></span></>
          : <span className="attachment-preview-placeholder" aria-hidden="true">{loading ? <LoaderCircle className="spin" size={16} /> : <ImagePlus size={16} />}</span>}
      </button>
      <figcaption title={attachment.name}>{attachment.name}</figcaption>
      {onRemove ? <button type="button" className="attachment-preview-remove" aria-label={`${language === "zh-CN" ? "移除" : "Remove"} ${attachment.name}`} onClick={onRemove}><X size={12} /></button> : null}
    </figure>
    {open && source ? createPortal(
      <div className="attachment-lightbox" role="dialog" aria-modal="true" aria-label={viewLabel} onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false); }}>
        <section>
          <header><strong>{attachment.name}</strong><button ref={closeButton} type="button" aria-label={closeLabel} title={closeLabel} onClick={() => setOpen(false)}><X size={17} /></button></header>
          <div className="attachment-lightbox-canvas"><img src={source} alt={attachment.name} /></div>
        </section>
      </div>,
      document.body,
    ) : null}
  </>;
}
