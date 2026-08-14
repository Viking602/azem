import { useEffect, useRef, useState } from "react";
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

const MAX_LIVE_REVEAL_CHUNKS = 8;
const MAX_REVEAL_CHUNK_GLYPHS = 96;

function revealTailOffset(text: string) {
  const glyphs = revealGlyphs(text);
  return glyphs.length > MAX_REVEAL_CHUNK_GLYPHS
    ? glyphs.slice(0, glyphs.length - MAX_REVEAL_CHUNK_GLYPHS).join("").length
    : 0;
}

function initialStreamingPresentation(text: string): StreamingPresentation {
  if (!text) return { rendered: "", ranges: [], nextID: 0 };
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

export function StreamingText({ content, debugReplay = false }: { content: string; debugReplay?: boolean }) {
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
  const presentationRef = useRef<StreamingPresentation>(initialStreamingPresentation(visibleContent));
  const presentation = appendStreamingPresentation(presentationRef.current, visibleContent);
  presentationRef.current = presentation;

  return <BeautifulStreamingText>
    <StreamingMarkdown ranges={presentation.ranges}>{visibleContent}</StreamingMarkdown>
  </BeautifulStreamingText>;
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
