import { tFormat, toolDisplayName, translator, type Language } from "../i18n";
import type { Block } from "../types";
import {
  fileChangesForBlock,
  isPendingFileChangeBlock,
  pendingFileChangeSummaryForBlock,
  pendingFileEditPaths,
  type EditedFileSummary,
} from "./fileChanges";
import { classifyToolCategory, formatToolPresentation, isHostFallbackCommentary } from "./toolTimeline";

export const FILE_CHANGE_PILL_LIMIT = 3;

export type ToolChipKind = "read" | "write" | "edit" | "shell" | "search" | "image" | "thinking" | "other";

const THINKING_CHIP_LIMIT = 42;

/** First reasoning sentence for the expanded process-chip preview capsule. */
export function thinkingChipPreview(blocks: Block[], limit = THINKING_CHIP_LIMIT) {
  for (const block of blocks) {
    const text = (block.content || "")
      .replace(/```[\s\S]*?```/gu, " ")
      .replace(/[#*_`>\[\]]/gu, "")
      .replace(/\s+/gu, " ")
      .trim();
    if (!text) continue;
    return text.length <= limit ? text : `${text.slice(0, Math.max(1, limit - 1))}…`;
  }
  return "";
}

export type ToolChipModel = {
  kind: ToolChipKind;
  label: string;
  chip: string;
  lines: number;
  meta: string[];
  statusLines: string[];
};

export function processGroupCounts(blocks: Block[]) {
  let tools = 0;
  let messages = 0;
  for (const block of blocks) {
    if (block.kind === "tool") tools += 1;
    if (block.kind === "commentary" && !isHostFallbackCommentary(block)) messages += 1;
  }
  return { tools, messages };
}

export function formatProcessGroupCount(tools: number, messages: number, language: Language) {
  if (tools <= 0 && messages <= 0) return "";
  if (tools > 0 && messages > 0) return tFormat(language, "toolChipGroupWithMessages", { tools, messages });
  if (tools > 0) return tFormat(language, "toolChipGroup", { count: tools });
  return tFormat(language, "toolChipMessages", { count: messages });
}

export function processGroupCountLabel(blocks: Block[], language: Language) {
  const { tools, messages } = processGroupCounts(blocks);
  return formatProcessGroupCount(tools, messages, language);
}

export function fileChangePillsForBlocks(blocks: Block[]): EditedFileSummary {
  const byPath = new Map<string, { path: string; additions: number; deletions: number }>();
  for (const block of blocks) {
    if (isPendingFileChangeBlock(block)) continue;
    for (const change of fileChangesForBlock(block)) {
      const current = byPath.get(change.path) ?? { path: change.path, additions: 0, deletions: 0 };
      current.additions += change.additions;
      current.deletions += change.deletions;
      byPath.set(change.path, current);
    }
  }
  const files = [...byPath.values()];
  return {
    files,
    additions: files.reduce((total, file) => total + file.additions, 0),
    deletions: files.reduce((total, file) => total + file.deletions, 0),
  };
}

export function toolChipBasename(path: string) {
  const normalized = path.replace(/\\/g, "/").replace(/\/+$/u, "");
  const parts = normalized.split("/").filter(Boolean);
  return parts.at(-1) || normalized;
}

export function isImageToolPath(path: string) {
  return /\.(png|jpe?g|gif|webp|svg|bmp|ico|avif)$/iu.test(path);
}

export function shellStatusLines(result = "") {
  const lines: string[] = [];
  for (const raw of result.replace(/\r\n?/gu, "\n").split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    if (!/^[✓✔]/u.test(line) && !/\b(?:built in|checks? passed|passed|ok)\b/iu.test(line)) continue;
    lines.push(line.replace(/^[✓✔]\s*/u, ""));
    if (lines.length >= 6) break;
  }
  return lines;
}

export function toolChipModel(block: Block, language: Language): ToolChipModel {
  const t = translator(language);
  const title = block.title || block.data?.name || block.data?.tool || "";
  const category = classifyToolCategory(title);
  const args = parseToolArgs(block);
  const payload = block.content || block.data?.arguments || "";
  const presentation = formatToolPresentation(payload.length > 8192 ? payload.slice(0, 8192) : payload, language);
  const result = presentation.result || block.data?.output || "";

  const executed = fileChangesForBlock(block);
  const planned = pendingFileChangeSummaryForBlock(block);
  const files = executed.length ? executed : planned?.files ?? [];
  const path = files[0]?.path
    || pendingFileEditPaths(block)[0]
    || firstString(args, "path", "file", "file_path", "filepath", "target", "filename");
  const command = firstString(args, "command", "cmd", "shell", "script");
  const query = firstString(args, "query", "pattern", "search", "regex", "needle", "term");
  const additions = planned?.additions ?? files.reduce((total, file) => total + file.additions, 0);
  const deletions = planned?.deletions ?? files.reduce((total, file) => total + file.deletions, 0);
  const writeLines = writeLineCount(block, additions);
  const image = category === "read" && Boolean(path) && isImageToolPath(path);
  const write = title === "coding.write_file" || title.toLowerCase().includes("write_file") || title.toLowerCase().includes("write");
  const kind: ToolChipKind = image
    ? "image"
    : write || (category === "edit" && deletions === 0 && writeLines > 0)
      ? "write"
      : category === "edit"
        ? "edit"
        : category === "read" || category === "diff"
          ? "read"
          : category === "shell"
            ? "shell"
            : category === "search"
              ? "search"
              : "other";

  let label = title ? toolDisplayName(title, language) : t("toolGeneric");
  if (image) label = t("toolReadImage");
  else if (kind === "write" && writeLines > 0) label = tFormat(language, "toolWriteLines", { count: writeLines });
  else if (kind === "edit" && files.length === 1 && additions + deletions > 0) {
    label = tFormat(language, "toolEditLines", { count: additions + deletions });
  }

  const chip = command
    || query
    || (path ? toolChipBasename(path) : "")
    || chipFromPreview(presentation.preview);

  return {
    kind,
    label,
    chip,
    lines: writeLines || additions,
    meta: image ? imageMetaLines(result) : [],
    statusLines: kind === "shell" ? shellStatusLines(result) : [],
  };
}

function writeLineCount(block: Block, additions: number) {
  if (additions > 0) return additions;
  const title = block.title || block.data?.name || "";
  if (title !== "coding.write_file") return 0;
  try {
    const parsed = JSON.parse(block.data?.arguments || block.content || "") as { content?: unknown };
    if (typeof parsed.content !== "string" || !parsed.content) return 0;
    return parsed.content.replace(/\n$/u, "").split("\n").length;
  } catch {
    return 0;
  }
}

function parseToolArgs(block: Block): Record<string, unknown> | null {
  const raw = (block.data?.arguments || block.content || "").trim();
  if (!raw) return null;
  const tryParse = (value: string) => {
    try {
      const parsed = JSON.parse(value) as unknown;
      return parsed && typeof parsed === "object" && !Array.isArray(parsed)
        ? parsed as Record<string, unknown>
        : null;
    } catch {
      return null;
    }
  };
  const whole = tryParse(raw);
  if (whole) return whole;
  const start = raw.indexOf("{");
  if (start < 0) return null;
  let depth = 0;
  for (let index = start; index < raw.length; index += 1) {
    if (raw[index] === "{") depth += 1;
    else if (raw[index] === "}") {
      depth -= 1;
      if (depth === 0) return tryParse(raw.slice(start, index + 1));
    }
  }
  return null;
}

function firstString(args: Record<string, unknown> | null, ...keys: string[]) {
  if (!args) return "";
  for (const key of keys) {
    const value = args[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function chipFromPreview(preview: string) {
  const value = preview.trim();
  if (!value) return "";
  const path = value.split(" · ")[0]?.trim() || value;
  return path.includes("/") ? toolChipBasename(path) : path;
}

function imageMetaLines(result: string) {
  return result.replace(/\r\n?/gu, "\n").split("\n")
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("{") && !line.startsWith("["))
    .slice(0, 2)
    .map((line) => (line.length > 88 ? `${line.slice(0, 87)}…` : line));
}
