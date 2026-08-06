import { useMemo, type CSSProperties } from "react";

export interface AnsiSegment {
  text: string;
  style?: CSSProperties;
}

interface AnsiState {
  foreground?: string;
  background?: string;
  bold: boolean;
  dim: boolean;
  italic: boolean;
  underline: boolean;
  strike: boolean;
  inverse: boolean;
  hidden: boolean;
}

const standardColors = [
  "#1f2328", "#cf222e", "#1a7f37", "#9a6700", "#0969da", "#8250df", "#1b7c83", "#d0d7de",
];
const brightColors = [
  "#57606a", "#ff7b72", "#56d364", "#e3b341", "#58a6ff", "#d2a8ff", "#39c5cf", "#ffffff",
];
const csiPattern = /\u001b\[([0-?]*)([ -\/]*)([@-~])/g;
const oscPattern = /\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g;
const unsupportedControls = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g;

export function parseAnsi(text: string): AnsiSegment[] {
  const input = text.replace(oscPattern, "");
  const segments: AnsiSegment[] = [];
  const state: AnsiState = emptyState();
  let cursor = 0;
  let match: RegExpExecArray | null;
  csiPattern.lastIndex = 0;

  while ((match = csiPattern.exec(input)) !== null) {
    appendText(segments, input.slice(cursor, match.index), state);
    if (match[3] === "m") applySgr(state, match[1] || "0");
    cursor = match.index + match[0].length;
  }
  appendText(segments, input.slice(cursor), state);
  return segments;
}

export function plainAnsiText(text: string) {
  return parseAnsi(text).map((segment) => segment.text).join("");
}

export default function AnsiText({ text }: { text: string }) {
  const segments = useMemo(() => parseAnsi(text), [text]);
  return <>{segments.map((segment, index) => <span key={index} style={segment.style}>{segment.text}</span>)}</>;
}

function emptyState(): AnsiState {
  return {
    bold: false, dim: false, italic: false, underline: false,
    strike: false, inverse: false, hidden: false,
  };
}

function resetState(state: AnsiState) {
  Object.assign(state, emptyState(), { foreground: undefined, background: undefined });
}

function appendText(segments: AnsiSegment[], raw: string, state: AnsiState) {
  const text = raw.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(unsupportedControls, "");
  if (!text) return;
  const style = styleFor(state);
  const previous = segments.at(-1);
  if (previous && sameStyle(previous.style, style)) {
    previous.text += text;
    return;
  }
  segments.push({ text, style });
}

function styleFor(state: AnsiState): CSSProperties | undefined {
  const style: CSSProperties = {};
  if (state.inverse) {
    style.color = state.background || "var(--paper)";
    style.backgroundColor = state.foreground || "var(--ink)";
  } else {
    if (state.foreground) style.color = state.foreground;
    if (state.background) style.backgroundColor = state.background;
  }
  if (state.bold) style.fontWeight = 650;
  if (state.dim) style.opacity = 0.68;
  if (state.italic) style.fontStyle = "italic";
  const decorations = [state.underline ? "underline" : "", state.strike ? "line-through" : ""].filter(Boolean);
  if (decorations.length) style.textDecoration = decorations.join(" ");
  if (state.hidden) style.visibility = "hidden";
  return Object.keys(style).length ? style : undefined;
}

function sameStyle(left?: CSSProperties, right?: CSSProperties) {
  if (!left || !right) return left === right;
  const leftKeys = Object.keys(left) as Array<keyof CSSProperties>;
  const rightKeys = Object.keys(right) as Array<keyof CSSProperties>;
  return leftKeys.length === rightKeys.length && leftKeys.every((key) => left[key] === right[key]);
}

function applySgr(state: AnsiState, parameters: string) {
  const codes = parameters.split(";").map((value) => value === "" ? 0 : Number.parseInt(value, 10));
  for (let index = 0; index < codes.length; index += 1) {
    const code = Number.isFinite(codes[index]) ? codes[index]! : 0;
    if (code === 0) resetState(state);
    else if (code === 1) state.bold = true;
    else if (code === 2) state.dim = true;
    else if (code === 3) state.italic = true;
    else if (code === 4) state.underline = true;
    else if (code === 7) state.inverse = true;
    else if (code === 8) state.hidden = true;
    else if (code === 9) state.strike = true;
    else if (code === 22) { state.bold = false; state.dim = false; }
    else if (code === 23) state.italic = false;
    else if (code === 24) state.underline = false;
    else if (code === 27) state.inverse = false;
    else if (code === 28) state.hidden = false;
    else if (code === 29) state.strike = false;
    else if (code >= 30 && code <= 37) state.foreground = standardColors[code - 30];
    else if (code === 38) index = applyExtendedColor(state, "foreground", codes, index);
    else if (code === 39) state.foreground = undefined;
    else if (code >= 40 && code <= 47) state.background = standardColors[code - 40];
    else if (code === 48) index = applyExtendedColor(state, "background", codes, index);
    else if (code === 49) state.background = undefined;
    else if (code >= 90 && code <= 97) state.foreground = brightColors[code - 90];
    else if (code >= 100 && code <= 107) state.background = brightColors[code - 100];
  }
}

function applyExtendedColor(state: AnsiState, target: "foreground" | "background", codes: number[], index: number) {
  const mode = codes[index + 1];
  if (mode === 5 && Number.isFinite(codes[index + 2])) {
    state[target] = color256(codes[index + 2]!);
    return index + 2;
  }
  if (mode === 2 && codes.slice(index + 2, index + 5).every(Number.isFinite)) {
    const [red, green, blue] = codes.slice(index + 2, index + 5).map(clampByte);
    state[target] = `rgb(${red} ${green} ${blue})`;
    return index + 4;
  }
  return index;
}

function color256(index: number) {
  const color = Math.max(0, Math.min(255, index));
  if (color < 8) return standardColors[color];
  if (color < 16) return brightColors[color - 8];
  if (color < 232) {
    const offset = color - 16;
    const red = Math.floor(offset / 36);
    const green = Math.floor((offset % 36) / 6);
    const blue = offset % 6;
    const channel = (value: number) => value === 0 ? 0 : 55 + value * 40;
    return `rgb(${channel(red)} ${channel(green)} ${channel(blue)})`;
  }
  const gray = 8 + (color - 232) * 10;
  return `rgb(${gray} ${gray} ${gray})`;
}

function clampByte(value: number) {
  return Math.max(0, Math.min(255, value));
}
