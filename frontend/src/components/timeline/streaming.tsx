import { useEffect, useRef, useState } from "react";
import { stabilizeStreamingMarkdown } from "../../streamMarkdown";
import { sameRevealRanges, StreamingMarkdown, type StreamingRevealRange } from "../Markdown";
import { StreamingText as BeautifulStreamingText } from "../beautiful-ui/Primitives";

function revealGlyphs(text: string) {
  if (typeof Intl.Segmenter === "function") {
    return Array.from(new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(text), (entry) => entry.segment);
  }
  return Array.from(text);
}

function replayChunks(text: string) {
  const glyphs = revealGlyphs(text);
  const chunks: string[] = [];
  for (let index = 0; index < glyphs.length;) {
    const newline = glyphs[index] === "\n";
    const size = newline ? 1 : 4;
    chunks.push(glyphs.slice(index, index + size).join(""));
    index += size;
  }
  return chunks;
}

type StreamingPresentation = {
  rendered: string;
  ranges: StreamingRevealRange[];
  nextID: number;
};

const MAX_LIVE_REVEAL_CHUNKS = 1;
const MAX_REVEAL_CHUNK_GLYPHS = 96;

function revealTailOffset(text: string) {
  const glyphs = revealGlyphs(text);
  return glyphs.length > MAX_REVEAL_CHUNK_GLYPHS
    ? glyphs.slice(0, glyphs.length - MAX_REVEAL_CHUNK_GLYPHS).join("").length
    : 0;
}

function emptyStreamingPresentation(text: string): StreamingPresentation {
  return { rendered: text, ranges: [], nextID: 0 };
}

function initialStreamingPresentation(text: string): StreamingPresentation {
  if (!text) return emptyStreamingPresentation("");
  return {
    rendered: text,
    ranges: [{ id: 0, start: revealTailOffset(text), end: text.length }],
    nextID: 1,
  };
}

function appendStreamingPresentation(current: StreamingPresentation, text: string): StreamingPresentation {
  if (current.rendered === text) return current;
  if (!text.startsWith(current.rendered)) return initialStreamingPresentation(text);
  const appended = text.slice(current.rendered.length);
  if (!appended) return { ...current, rendered: text };
  const range = {
    id: current.nextID,
    start: current.rendered.length + revealTailOffset(appended),
    end: text.length,
  };
  const ranges = [...current.ranges, range].slice(-MAX_LIVE_REVEAL_CHUNKS);
  return {
    rendered: text,
    ranges: sameRevealRanges(current.ranges, ranges) ? current.ranges : ranges,
    nextID: current.nextID + 1,
  };
}

function prefersReducedMotion() {
  if (typeof document !== "undefined" && document.documentElement.dataset.reduceMotion === "true") return true;
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches === true;
}

function nextStreamingPresentation(
  current: StreamingPresentation | null,
  text: string,
  active: boolean,
): StreamingPresentation {
  if (prefersReducedMotion()) {
    return current?.rendered === text ? current : emptyStreamingPresentation(text);
  }
  if (!current) return active ? initialStreamingPresentation(text) : emptyStreamingPresentation(text);
  if (!active) {
    return current.rendered === text ? current : { ...current, rendered: text };
  }
  return appendStreamingPresentation(current, text);
}

export function StreamingText({ content, active = true, debugReplay = false }: { content: string; active?: boolean; debugReplay?: boolean }) {
  // Production renders the provider's current buffer directly through the same
  // Markdown path as completed answers. The replay clock exists only for the
  // visual demo and advances in small batches so structural Markdown settles
  // quickly instead of exposing one control character at a time.
  const [replayContent, setReplayContent] = useState(() => debugReplay ? "" : content);
  useEffect(() => {
    if (!debugReplay) return;
    const chunks = replayChunks(content);
    let index = 0;
    let timer = 0;
    const delayForChunk = (chunk: string) => {
      if (chunk === "\n") return 72;
      if (/[。！？!?]\s*$/u.test(chunk)) return 62;
      if (/[，、；：,;:]\s*$/u.test(chunk)) return 45;
      return 34;
    };
    setReplayContent("");
    const reveal = () => {
      const chunk = chunks[index];
      if (chunk === undefined) return;
      setReplayContent((value) => value + chunk);
      index += 1;
      if (index < chunks.length) timer = window.setTimeout(reveal, delayForChunk(chunk));
    };
    timer = window.setTimeout(reveal, 90);
    return () => window.clearTimeout(timer);
  }, [content, debugReplay]);
  const visibleContent = debugReplay ? replayContent : content;
  const presentationRef = useRef<StreamingPresentation | null>(null);
  const presentation = nextStreamingPresentation(presentationRef.current, visibleContent, active);
  presentationRef.current = presentation;
  const stableContent = active ? stabilizeStreamingMarkdown(visibleContent) : visibleContent;
  const ranges = active ? presentation.ranges : [];

  return <BeautifulStreamingText active={active}>
    <StreamingMarkdown ranges={ranges}>{stableContent}</StreamingMarkdown>
  </BeautifulStreamingText>;
}

/** One Markdown tree for live and settled prose. Completion only stops reveal CSS. */
export function TimelineProse({ content, active, debugReplay = false }: { content: string; active: boolean; debugReplay?: boolean }) {
  return <StreamingText content={content} active={active} debugReplay={debugReplay} />;
}

export function normalizeThinkingText(content: string) {
  return content
    .replace(/\*\*\*\*/g, "**\n\n**")
    .replace(/([^\n])\n\*\*/g, "$1\n\n**")
    .trim();
}

export function plainStreamingText(content: string) {
  return content
    .replace(/^```[^\n]*\n?/gmu, "")
    .replace(/^```$/gmu, "")
    .replace(/^#{1,6}[ \t]+/gmu, "")
    .replace(/^>[ \t]?/gmu, "")
    .replace(/^(\s*)(?:[-*+]|\d+[.)])[ \t]+/gmu, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/gu, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/gu, "$1")
    .replace(/`([^`\n]+)`/gu, "$1")
    .replace(/\*\*([^*]+)\*\*/gu, "$1")
    .replace(/__([^_]+)__/gu, "$1")
    .replace(/~~([^~]+)~~/gu, "$1")
    .trim();
}
