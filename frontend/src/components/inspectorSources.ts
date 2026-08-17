import type { Attachment, Block } from "../types";
import type { Language } from "../i18n";

export type SourceKind = "image" | "input-url" | "search-url";

export interface ConversationSource {
  id: string;
  kind: SourceKind;
  title: string;
  detail?: string;
  href?: string;
  attachment?: Attachment;
}

const MAX_SOURCES = 80;
const GENERIC_IMAGE = /^(image|img|photo|picture|untitled)(?:[-_\s]*\d+)?$/iu;
const PASTED_IMAGE = /^pasted-image(?:[-_].*)?$/iu;
const WEB_RESEARCH_TOOL = /web_?search|websearch|search_?web|web_?fetch|webfetch|web_?browse|open_?page|fetch_?url|exa_?search|tavily|brave_search|grok[^a-z0-9]*search|x_keyword_search|x_semantic_search/iu;
const URL_PATTERN = /https?:\/\/[^\s<>"'`）\]}>]+/giu;
const MARKDOWN_LINK = /\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/gu;

export function collectConversationSources(blocks: Block[], language: Language): ConversationSource[] {
  const sources: ConversationSource[] = [];
  const seen = new Set<string>();
  let imageIndex = 0;

  const add = (source: ConversationSource) => {
    if (sources.length >= MAX_SOURCES) return;
    const key = source.href ? normalizeHref(source.href) : `image:${source.attachment?.id || source.attachment?.path || source.id}`;
    if (!key || seen.has(key)) return;
    seen.add(key);
    sources.push(source);
  };

  for (const block of blocks) {
    if (block.kind === "user") {
      for (const attachment of block.attachments ?? []) {
        imageIndex += 1;
        add({
          id: attachment.id || attachment.path || `image-${imageIndex}`,
          kind: "image",
          title: imageSourceTitle(attachment.name, imageIndex, language),
          detail: attachment.name && attachment.name !== imageSourceTitle(attachment.name, imageIndex, language) ? attachment.name : undefined,
          attachment,
        });
      }
      for (const href of collectTextURLs(block.content || "")) {
        add({
          id: `input:${href}`,
          kind: "input-url",
          title: urlDisplayTitle(href),
          detail: href,
          href,
        });
      }
      continue;
    }
    if (block.kind !== "tool" || !isWebResearchTool(block.title || block.data?.name || "")) continue;
    for (const item of collectToolURLs(block)) {
      add({
        id: `search:${item.href}`,
        kind: "search-url",
        title: item.title || urlDisplayTitle(item.href),
        detail: item.href,
        href: item.href,
      });
    }
  }
  return sources;
}

export function imageSourceTitle(name: string, index: number, language: Language): string {
  const trimmed = name.trim();
  const stem = trimmed.replace(/\.[^.]+$/u, "") || trimmed;
  if (!stem || GENERIC_IMAGE.test(stem)) {
    return language === "zh-CN" ? `图片 ${index}` : `Image ${index}`;
  }
  if (PASTED_IMAGE.test(stem)) {
    return language === "zh-CN" ? `粘贴图片 ${index}` : `Pasted image ${index}`;
  }
  return trimmed;
}

export function isWebResearchTool(name: string): boolean {
  return WEB_RESEARCH_TOOL.test(name.toLowerCase().replaceAll("-", "_").replaceAll(".", "_"));
}

export function collectTextURLs(text: string): string[] {
  const found: string[] = [];
  for (const match of text.matchAll(URL_PATTERN)) {
    const href = normalizeHref(trimURLPunctuation(match[0]));
    if (href) found.push(href);
  }
  return found;
}

function collectToolURLs(block: Block): Array<{ href: string; title: string }> {
  const texts = [block.content || "", block.data?.arguments || "", block.data?.output || ""];
  const titled: Array<{ href: string; title: string }> = [];
  const seen = new Set<string>();
  const push = (href: string, title = "") => {
    const normalized = normalizeHref(href);
    if (!normalized || seen.has(normalized)) return;
    seen.add(normalized);
    titled.push({ href: normalized, title: title.trim() });
  };

  for (const text of texts) {
    for (const match of text.matchAll(MARKDOWN_LINK)) {
      push(trimURLPunctuation(match[2] || ""), match[1] || "");
    }
    collectJSONURLs(text, push);
    for (const href of collectTextURLs(text)) push(href);
  }
  return titled;
}

function collectJSONURLs(raw: string, push: (href: string, title?: string) => void) {
  const candidates = extractJSONValues(raw);
  for (const value of candidates) walkJSONForURLs(value, push);
}

function extractJSONValues(raw: string): unknown[] {
  const values: unknown[] = [];
  const trimmed = raw.trim();
  if (!trimmed) return values;
  const tryParse = (text: string) => {
    try {
      values.push(JSON.parse(text));
    } catch {
      return;
    }
  };
  tryParse(trimmed);
  const first = trimmed.indexOf("{");
  const last = trimmed.lastIndexOf("}");
  if (first >= 0 && last > first) tryParse(trimmed.slice(first, last + 1));
  const arrayFirst = trimmed.indexOf("[");
  const arrayLast = trimmed.lastIndexOf("]");
  if (arrayFirst >= 0 && arrayLast > arrayFirst) tryParse(trimmed.slice(arrayFirst, arrayLast + 1));
  return values;
}

function walkJSONForURLs(value: unknown, push: (href: string, title?: string) => void, titleHint = "") {
  if (!value) return;
  if (typeof value === "string") {
    for (const href of collectTextURLs(value)) push(href, titleHint);
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) walkJSONForURLs(item, push, titleHint);
    return;
  }
  if (typeof value !== "object") return;
  const record = value as Record<string, unknown>;
  const title = firstString(record, "title", "name", "heading", "text", "query");
  const href = firstString(record, "url", "link", "href", "source_url", "sourceUrl", "uri", "page_url");
  if (href) push(href, title || titleHint);
  for (const [key, nested] of Object.entries(record)) {
    if (key === "url" || key === "link" || key === "href") continue;
    walkJSONForURLs(nested, push, title || titleHint);
  }
}

function firstString(record: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function urlDisplayTitle(href: string): string {
  try {
    const parsed = new URL(href);
    const path = decodeURIComponent(parsed.pathname).replace(/\/+$/u, "");
    const leaf = path.split("/").filter(Boolean).at(-1);
    return leaf ? `${parsed.hostname} · ${leaf}` : parsed.hostname.replace(/^www\./u, "");
  } catch {
    return href;
  }
}

function trimURLPunctuation(value: string): string {
  return value.replace(/[),.;:!?，。；：！？]+$/u, "");
}

function normalizeHref(value: string): string {
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "";
    parsed.hash = "";
    if (parsed.pathname !== "/" && parsed.pathname.endsWith("/")) {
      parsed.pathname = parsed.pathname.slice(0, -1);
    }
    return parsed.toString();
  } catch {
    return "";
  }
}
