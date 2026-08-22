import { isWebResearchTool } from "../inspectorSources";
import { tFormat, translator, type Language } from "../../i18n";
import type { Block } from "../../types";
import {
  classifyToolCategory,
  formatToolPresentation,
  isActiveProcessBlock,
  thinkingStateLabel,
  thinkingTraceElapsedMs,
} from "../toolTimeline";
import { isFileChangeTool } from "../fileChanges";
import { toolChipModel } from "../toolChip";

export type ThinkingTabId = "steps" | "reasoning" | "search" | "coding";

export type SearchHit = {
  title: string;
  detail?: string;
  href?: string;
};

export type SearchTrace = {
  query: string;
  hits: SearchHit[];
};

export type ThinkingTraceParts = {
  steps: Block[];
  reasoning: Block[];
  search: Block[];
  coding: Block[];
};

const SEARCH_NAME = /grep|glob|list_files|list-files|ripgrep/iu;
const URL_PATTERN = /https?:\/\/[^\s<>"'`）\]}>]+/giu;
const MARKDOWN_LINK = /\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/gu;
const FILE_HIT = /^(\S+?):(\d+):(?:\d+:)?\s*(.*)$/u;
const SEARCH_PREVIEW = 5;

export function isSearchLikeTool(block: Block): boolean {
  if (block.kind !== "tool") return false;
  const name = block.title || block.data?.name || "";
  if (isWebResearchTool(name)) return true;
  if (classifyToolCategory(name) === "search") return true;
  return SEARCH_NAME.test(name);
}

export function isCodingLikeTool(block: Block): boolean {
  if (block.kind === "diff") return true;
  if (block.kind !== "tool") return false;
  const name = block.title || block.data?.name || "";
  if (isFileChangeTool(name)) return true;
  const category = classifyToolCategory(name);
  if (category === "edit") return true;
  const raw = name.trim().toLowerCase().replaceAll("_", ".");
  return raw.includes("write") || raw.includes("hashline") || raw.includes("apply_patch")
    || raw.includes("gofmt") || raw === "coding.shell" || raw === "coding.go.test";
}

export function collectThinkingTrace(blocks: Block[]): ThinkingTraceParts {
  const steps: Block[] = [];
  const reasoning: Block[] = [];
  const search: Block[] = [];
  const coding: Block[] = [];
  for (const block of blocks) {
    if (block.kind === "thinking") {
      reasoning.push(block);
      continue;
    }
    if (block.kind !== "tool" && block.kind !== "diff") continue;
    steps.push(block);
    if (isSearchLikeTool(block)) search.push(block);
    if (isCodingLikeTool(block)) coding.push(block);
  }
  return { steps, reasoning, search, coding };
}

export function thinkingTabHasContent(parts: ThinkingTraceParts, tab: ThinkingTabId) {
  if (tab === "steps") return parts.steps.length > 0;
  if (tab === "search") return parts.search.length > 0;
  if (tab === "coding") return parts.coding.length > 0;
  return parts.reasoning.some((block) => Boolean(block.content?.trim()));
}

const THINKING_TAB_ORDER: ThinkingTabId[] = ["reasoning", "steps", "search", "coding"];

/** A lone reasoning (or lone tool) trail should not grow a four-tab switcher. */
export function populatedThinkingTabs(parts: ThinkingTraceParts): ThinkingTabId[] {
  return THINKING_TAB_ORDER.filter((tab) => thinkingTabHasContent(parts, tab));
}

export function shouldShowThinkingTablist(parts: ThinkingTraceParts) {
  return populatedThinkingTabs(parts).length >= 2;
}

/** Tools that are not already summarized on the search row. */
export function thinkingWorkBlocks(parts: ThinkingTraceParts) {
  const searchIds = new Set(parts.search.map((block) => block.id));
  return parts.steps.filter((block) => !searchIds.has(block.id));
}

export function thinkingSearchIsWeb(parts: ThinkingTraceParts) {
  return parts.search.some((block) => isWebResearchTool(block.title || block.data?.name || ""));
}

function isSparkleRunning(block: Block) {
  return ["running", "started", "streaming", "progress"].includes(block.state || "");
}

/** Live thinking or executing tools that should stay on the sparkle bar. */
export function hasLiveSparkleWork(blocks: Block[]) {
  return blocks.some((block) => {
    if (block.kind === "thinking") return isActiveProcessBlock(block) && Boolean(block.content?.trim());
    if (block.kind === "tool" || block.kind === "diff") return isSparkleRunning(block);
    return false;
  });
}

export function processActivityVisibility(blocks: Block[], waiting = false) {
  const hasTools = blocks.some((block) => block.kind === "tool" || block.kind === "diff");
  const hasReasoning = blocks.some((block) => block.kind === "thinking" && Boolean(block.content?.trim()));
  const liveWork = hasLiveSparkleWork(blocks);
  const liveThinking = blocks.some((block) => (
    block.kind === "thinking" && isActiveProcessBlock(block) && Boolean(block.content?.trim())
  ));
  const showBar = waiting || liveWork || (!hasTools && hasReasoning);
  return {
    showBar,
    liveWork,
    omitThinking: showBar && (!hasTools || liveThinking),
    omitLiveTools: liveWork,
  };
}

/**
 * One sparkle bar for the whole step: wait, thinking, search, running tools, and
 * the settled summary are all the same bar with a different label. A completed
 * trail must never hand off to a second card with its own counts.
 *
 * While the step is live the bar tracks the row that is actually executing, so
 * it keeps moving instead of freezing on one count for minutes.
 */
export function activityBarLabel(
  blocks: Block[],
  language: Language,
  options: { waiting?: boolean; live?: boolean } = {},
) {
  const parts = collectThinkingTrace(blocks);
  const waiting = Boolean(options.waiting);
  const live = Boolean(options.live) || waiting;
  const t = translator(language);
  const running = parts.steps.filter(isSparkleRunning);
  if (running.length) {
    const current = running[running.length - 1]!;
    const model = toolChipModel(current, language);
    const detail = model.chip ? `${model.label} "${model.chip}"` : model.label;
    return tFormat(language, "thinkingRunning", { detail });
  }
  if (!live && parts.steps.length) {
    if (!thinkingWorkBlocks(parts).length) {
      return thinkingSearchIsWeb(parts) ? t("thinkingSearchedWeb") : t("thinkingSearchedCode");
    }
    return parts.steps.length === 1
      ? t("thinkingRanOneTool")
      : tFormat(language, "thinkingRanTools", { count: parts.steps.length });
  }
  return thinkingStateLabel(language, live, live ? 0 : thinkingTraceElapsedMs(parts.reasoning));
}

export function defaultThinkingTab(parts: ThinkingTraceParts): ThinkingTabId {
  const reasoningActive = parts.reasoning.some((block) =>
    ["streaming", "running", "started", "progress"].includes(block.state || "")
    && Boolean(block.content?.trim()));
  if (reasoningActive) return "reasoning";
  if (parts.steps.length) return "steps";
  if (parts.reasoning.some((block) => Boolean(block.content?.trim()))) return "reasoning";
  if (parts.search.length) return "search";
  if (parts.coding.length) return "coding";
  return "reasoning";
}

/** Keep a live group on a tab that actually has content. */
export function resolveLiveThinkingTab(parts: ThinkingTraceParts, current: ThinkingTabId): ThinkingTabId {
  return thinkingTabHasContent(parts, current) ? current : defaultThinkingTab(parts);
}

export function parseSearchTrace(block: Block): SearchTrace {
  const payload = block.content || block.data?.arguments || "";
  const output = block.data?.output || "";
  const args = firstJSONObject(payload) ?? firstJSONObject(block.data?.arguments || "");
  const query = firstString(args, "query", "pattern", "search", "regex", "needle", "term", "glob")
    || formatToolPresentation(payload).fields.find((field) => /query|pattern|search|查询/iu.test(field.label))?.value
    || "";
  const hits = uniqueHits([
    ...hitsFromJSON(args),
    ...hitsFromJSON(firstJSONObject(payload)),
    ...hitsFromJSON(firstJSONObject(output)),
    ...hitsFromMarkup(payload),
    ...hitsFromMarkup(output),
    ...hitsFromFileLines(resultText(payload, output)),
  ]);
  if (!hits.length) {
    const preview = formatToolPresentation(payload).preview || formatToolPresentation(output).preview;
    if (preview) hits.push({ title: preview });
  }
  return { query, hits };
}

export function visibleSearchHits(hits: SearchHit[], expanded: boolean, limit = SEARCH_PREVIEW) {
  if (expanded || hits.length <= limit) return { visible: hits, hidden: 0 };
  return { visible: hits.slice(0, limit), hidden: hits.length - limit };
}

function resultText(payload: string, output: string) {
  const fromPayload = stripLeadingJSON(payload);
  return [output, fromPayload].filter(Boolean).join("\n");
}

function hitsFromJSON(value: unknown): SearchHit[] {
  if (!value) return [];
  const hits: SearchHit[] = [];
  const visit = (node: unknown, depth: number) => {
    if (!node) return;
    if (Array.isArray(node)) {
      for (const item of node) visit(item, depth + 1);
      return;
    }
    if (typeof node !== "object") return;
    const record = node as Record<string, unknown>;
    const title = firstString(record, "title", "name", "heading", "path", "file", "filename");
    const href = firstString(record, "url", "link", "href", "source_url", "sourceUrl", "uri");
    const detail = firstString(record, "snippet", "description", "text", "content", "domain", "hostname")
      || (href ? hostnameOf(href) : "");
    const searchArgs = Boolean(firstString(record, "query", "pattern", "search", "regex", "needle", "term", "glob"));
    const resultLike = Boolean(href || firstString(record, "title", "name", "snippet"));
    if ((title || href) && (depth > 0 || (!searchArgs && resultLike))) {
      hits.push({ title: title || hostnameOf(href) || href, detail: detail || undefined, href: href || undefined });
    }
    for (const [key, nested] of Object.entries(record)) {
      if (["results", "matches", "items", "hits", "files", "sources"].includes(key) || Array.isArray(nested)) {
        visit(nested, depth + 1);
      }
    }
  };
  visit(value, 0);
  return hits;
}

function hitsFromMarkup(text: string): SearchHit[] {
  const hits: SearchHit[] = [];
  const seen = new Set<string>();
  for (const match of text.matchAll(MARKDOWN_LINK)) {
    const href = trimURL(match[2] || "");
    if (!href || seen.has(href)) continue;
    seen.add(href);
    hits.push({ title: (match[1] || "").trim() || hostnameOf(href), detail: hostnameOf(href), href });
  }
  for (const match of text.matchAll(URL_PATTERN)) {
    const href = trimURL(match[0] || "");
    if (!href || seen.has(href)) continue;
    seen.add(href);
    hits.push({ title: hostnameOf(href) || href, detail: href, href });
  }
  return hits;
}

function hitsFromFileLines(text: string): SearchHit[] {
  const hits: SearchHit[] = [];
  for (const raw of text.replace(/\r\n?/gu, "\n").split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("{") || line.startsWith("[")) continue;
    const match = FILE_HIT.exec(line);
    if (!match) continue;
    const path = match[1] || "";
    const row = match[2] || "";
    const snippet = (match[3] || "").trim();
    hits.push({
      title: path.split("/").filter(Boolean).at(-1) || path,
      detail: snippet ? `${path}:${row} · ${snippet}` : `${path}:${row}`,
    });
  }
  return hits;
}

function uniqueHits(hits: SearchHit[]): SearchHit[] {
  const seen = new Set<string>();
  const result: SearchHit[] = [];
  for (const hit of hits) {
    const key = hit.href || `${hit.title}|${hit.detail || ""}`;
    if (!key || seen.has(key)) continue;
    seen.add(key);
    result.push(hit);
  }
  return result;
}

function firstJSONObject(raw: string): Record<string, unknown> | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const tryParse = (text: string) => {
    try {
      const value = JSON.parse(text) as unknown;
      return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
    } catch {
      return null;
    }
  };
  const whole = tryParse(trimmed);
  if (whole) return whole;
  const start = trimmed.indexOf("{");
  if (start < 0) return null;
  let depth = 0;
  for (let index = start; index < trimmed.length; index += 1) {
    const char = trimmed[index];
    if (char === "{") depth += 1;
    else if (char === "}") {
      depth -= 1;
      if (depth === 0) return tryParse(trimmed.slice(start, index + 1));
    }
  }
  return null;
}

function stripLeadingJSON(raw: string) {
  const object = firstJSONObject(raw);
  if (!object) return raw.trim();
  let depth = 0;
  for (let cursor = raw.indexOf("{"); cursor >= 0 && cursor < raw.length; cursor += 1) {
    if (raw[cursor] === "{") depth += 1;
    else if (raw[cursor] === "}") {
      depth -= 1;
      if (depth === 0) return raw.slice(cursor + 1).trim();
    }
  }
  return raw.trim();
}

function firstString(record: Record<string, unknown> | null, ...keys: string[]) {
  if (!record) return "";
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function hostnameOf(href: string) {
  try {
    return new URL(href).hostname.replace(/^www\./u, "");
  } catch {
    return "";
  }
}

function trimURL(value: string) {
  return value.replace(/[),.;:!?，。；：！？]+$/u, "");
}
