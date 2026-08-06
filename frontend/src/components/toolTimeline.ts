import { toolGroupLabel, translator, type Language, type ToolCategory } from "../i18n";
import type { Block } from "../types";
import { plainAnsiText } from "./AnsiText";
import { isFileChangeTool } from "./fileChanges";

export type { ToolCategory };
export const MIN_TOOL_GROUP_SIZE = 2;

export type ToolPresentation = {
  preview: string;
  fields: Array<{ label: string; value: string }>;
  result?: string;
};

/** Humanize tool arguments/results — never surface raw JSON in the timeline. */
export function formatToolPresentation(content = "", language: Language = "zh-CN"): ToolPresentation {
  const trimmed = content.trim();
  if (!trimmed) return { preview: "", fields: [] };

  const { args, result } = splitToolPayload(trimmed);
  const plainResult = plainAnsiText(result);
  const fields = args ? humanizeToolArgs(args, language) : [];
  const preview = fields.map((field) => field.value).filter(Boolean).join(" · ")
    || (plainResult ? shorten(plainResult.replace(/\s+/g, " ").trim(), 80) : "")
    || shorten(stripJsonNoise(plainAnsiText(trimmed)), 72);

  return {
    preview,
    fields,
    // Tool results that are still raw JSON stay hidden; arguments are already humanized above.
    result: result && !looksLikeJson(result) ? result.trim() : undefined,
  };
}

function splitToolPayload(content: string): { args: Record<string, unknown> | null; result: string } {
  const tryParse = (raw: string) => {
    try {
      const value = JSON.parse(raw) as unknown;
      return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
    } catch {
      return null;
    }
  };

  const whole = tryParse(content);
  if (whole) return { args: whole, result: "" };

  // Arguments JSON on first line/object, then tool result text.
  const firstBrace = content.indexOf("{");
  if (firstBrace >= 0) {
    let depth = 0;
    for (let index = firstBrace; index < content.length; index += 1) {
      const char = content[index];
      if (char === "{") depth += 1;
      else if (char === "}") {
        depth -= 1;
        if (depth === 0) {
          const head = content.slice(firstBrace, index + 1);
          const args = tryParse(head);
          if (args) return { args, result: content.slice(index + 1).trim() };
          break;
        }
      }
    }
  }
  return { args: null, result: content };
}

function humanizeToolArgs(args: Record<string, unknown>, language: Language) {
  const t = translator(language);
  const fields: Array<{ label: string; value: string }> = [];
  const push = (label: string, value: unknown) => {
    const text = formatArgValue(value);
    if (text) fields.push({ label, value: text });
  };

  const path = firstString(args, "path", "file", "file_path", "filepath", "target", "filename");
  if (path) {
    const start = firstNumber(args, "startLine", "start_line", "offset", "from");
    const end = firstNumber(args, "endLine", "end_line", "limit", "to");
    let value = shortenPath(path);
    if (start != null && end != null) value += ` · L${start}–${end}`;
    else if (start != null) value += ` · L${start}+`;
    else if (end != null) value += language === "zh-CN" ? ` · 至 L${end}` : ` · to L${end}`;
    push(t("fieldPath"), value);
  }

  const paths = firstStringArray(args, "paths", "files");
  if (paths.length) push(t("fieldPaths"), paths.map(shortenPath).join(", "));

  const query = firstString(args, "query", "pattern", "search", "regex", "needle", "term");
  if (query) push(t("fieldQuery"), shorten(query, 64));

  const command = firstString(args, "command", "cmd", "shell", "script");
  if (command) push(t("fieldCommand"), shorten(command, 72));

  const cwd = firstString(args, "cwd", "working_directory", "workdir", "dir");
  if (cwd) push(t("fieldCwd"), shortenPath(cwd));

  const artifact = firstString(args, "artifact_id", "artifactId", "artifact");
  if (artifact) push(t("fieldArtifact"), shorten(artifact, 36));

  const skill = firstString(args, "skill", "name", "skill_name");
  if (skill && !path && !command && !query) push(t("fieldSkill"), skill);

  const description = firstString(args, "description", "prompt", "instruction", "message", "content");
  if (description && fields.length === 0) push(t("fieldDetail"), shorten(description, 80));

  // Fallback: pick a few primitive fields without dumping whole JSON.
  if (fields.length === 0) {
    for (const [key, value] of Object.entries(args)) {
      if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
        push(key, value);
      }
      if (fields.length >= 3) break;
    }
  }
  return fields;
}

function firstString(args: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = args[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function firstNumber(args: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = args[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() && !Number.isNaN(Number(value))) return Number(value);
  }
  return null;
}

function firstStringArray(args: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = args[key];
    if (Array.isArray(value)) return value.map(String).filter(Boolean);
  }
  return [] as string[];
}

function formatArgValue(value: unknown) {
  if (value == null) return "";
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.map((item) => typeof item === "string" ? item : JSON.stringify(item)).join(", ");
  return "";
}

function shortenPath(path: string) {
  // Keep paths fully readable in the timeline; only soft-trim extreme lengths.
  const normalized = path.replace(/\\/g, "/");
  if (normalized.length <= 96) return normalized;
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 2) return normalized;
  return `…/${parts.slice(-2).join("/")}`;
}

function shorten(text: string, max: number) {
  const value = text.trim();
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
}

function looksLikeJson(text: string) {
  const value = text.trim();
  return (value.startsWith("{") && value.endsWith("}")) || (value.startsWith("[") && value.endsWith("]"));
}

function stripJsonNoise(text: string) {
  return text.replace(/[{}"[\]]/g, " ").replace(/,/g, " · ").replace(/\s+/g, " ").trim();
}

export type TimelineEntry =
  | { kind: "block"; block: Block }
  | { kind: "tool-group"; id: string; blocks: Block[]; summary: string; running: boolean };

export function isRunningTool(block: Block) {
  return block.kind === "tool" && ["running", "started", "streaming", "progress"].includes(block.state || "");
}
function isPendingTool(block: Block) {
  return block.kind === "tool"
    && ["queued", "awaiting_approval", "reviewing_approval"].includes(block.state || "");
}


export function isCollapsibleTool(block: Block) {
  return block.kind === "tool"
    && !isRunningTool(block)
    && !isPendingTool(block)
    && !isFileChangeTool(block.title || block.data?.name || "");
}

export function classifyToolCategory(title = ""): ToolCategory {
  const raw = title.trim().toLowerCase();
  if (!raw) return "other";
  if (raw.includes("search") || raw.includes("搜索") || raw === "coding.search") return "search";
  if (
    raw.includes("read") || raw.includes("list") || raw.includes("读取") || raw.includes("列出")
    || raw.includes("read_file") || raw.includes("list_files") || raw.includes("read_artifact")
  ) return "read";
  // Real mutations first — never treat bare "diff" as an edit.
  if (
    raw.includes("write") || raw.includes("edit") || raw.includes("gofmt") || raw.includes("patch")
    || raw.includes("apply_diff") || raw.includes("hashline")
    || raw.includes("写入") || raw.includes("编辑") || raw.includes("格式化")
  ) return "edit";
  // Viewing git / file diffs is inspection, not editing.
  if (
    raw.includes("git_diff") || raw.includes("git-diff") || raw.includes("git.diff")
    || raw.includes("差异") || raw.includes("diff")
  ) return "diff";
  if (
    raw.includes("shell") || raw.includes("test") || raw.includes("command")
    || raw.includes("运行") || raw.includes("命令")
  ) return "shell";
  if (raw.includes("subagent") || raw.includes("spawn") || raw.includes("子智能体") || raw.includes("agent")) return "agent";
  return "other";
}

export function summarizeToolGroup(blocks: Block[], language: Language) {
  const counts = new Map<ToolCategory, number>();
  for (const block of blocks) {
    const category = classifyToolCategory(block.title || block.data?.tool || "");
    counts.set(category, (counts.get(category) ?? 0) + 1);
  }
  const order: ToolCategory[] = ["search", "read", "edit", "diff", "shell", "agent", "other"];
  const active = order.filter((category) => (counts.get(category) ?? 0) > 0);
  const parts = active.map((category) => toolGroupLabel(category, counts.get(category)!, language, active.length === 1));
  return parts.join(language === "zh-CN" ? " · " : ", ");
}

/** Collapse consecutive settled tool calls into summary groups (Synara-style). */
export function groupTimelineBlocks(blocks: Block[], language: Language): TimelineEntry[] {
  const entries: TimelineEntry[] = [];
  let index = 0;
  while (index < blocks.length) {
    const block = blocks[index]!;
    if (!isCollapsibleTool(block)) {
      entries.push({ kind: "block", block });
      index += 1;
      continue;
    }
    let end = index + 1;
    while (end < blocks.length && isCollapsibleTool(blocks[end]!)) end += 1;
    const group = blocks.slice(index, end);
    if (group.length < MIN_TOOL_GROUP_SIZE) {
      for (const item of group) entries.push({ kind: "block", block: item });
    } else {
      entries.push({
        kind: "tool-group",
        id: `tool-group-${group[0]!.id}-${group.length}`,
        blocks: group,
        summary: summarizeToolGroup(group, language),
        running: false,
      });
    }
    index = end;
  }
  return entries;
}

/** Kinds that form the collapsible process trail ("经过"), not final outcomes. */
export function isProcessBlock(block: Block) {
  return block.kind === "thinking" || block.kind === "commentary" || block.kind === "tool";
}

/** Agent / hook lifecycle noise — hide from the main transcript (Codex-style). */
export function isHiddenTimelineBlock(block: Block) {
  return block.kind === "agent" || block.kind === "hook";
}

export type ProcessSegment =
  | { kind: "block"; block: Block }
  | { kind: "process"; id: string; blocks: Block[]; elapsedMs: number; active: boolean };

/**
 * Segment the transcript so completed process trails can fold under “已处理”.
 * Active runs stay expanded (rendered flat via active=true).
 */
export function segmentProcessTrail(
  blocks: Block[],
  options: { activeRunId?: string; running?: boolean } = {},
): ProcessSegment[] {
  const visible = blocks.filter((block) => !isHiddenTimelineBlock(block));
  const segments: ProcessSegment[] = [];
  let index = 0;
  while (index < visible.length) {
    const block = visible[index]!;
    if (!isProcessBlock(block)) {
      segments.push({ kind: "block", block });
      index += 1;
      continue;
    }
    let end = index + 1;
    while (end < visible.length && isProcessBlock(visible[end]!)) end += 1;
    const processBlocks = visible.slice(index, end);
    const runId = processBlocks.find((item) => item.runId)?.runId || "";
    const active = processBlocks.some((item) => isActiveProcessBlock(item))
      || Boolean(options.running && runId && runId === options.activeRunId);
    segments.push({
      kind: "process",
      id: `process-${processBlocks[0]!.id}-${processBlocks.length}`,
      blocks: processBlocks,
      elapsedMs: processElapsedMs(processBlocks),
      active,
    });
    index = end;
  }
  return segments;
}

function isActiveProcessBlock(block: Block) {
  return ["running", "started", "streaming", "progress"].includes(block.state || "");
}

function processElapsedMs(blocks: Block[]) {
  let stamped = 0;
  let minStart = Infinity;
  let maxEnd = 0;
  for (const block of blocks) {
    const value = Number(block.data?.elapsedMs || 0);
    if (Number.isFinite(value) && value > stamped) stamped = value;
    const start = Number(block.data?.startedAt || 0);
    const end = Number(block.data?.completedAt || 0);
    if (start > 0) minStart = Math.min(minStart, start);
    if (end > 0) maxEnd = Math.max(maxEnd, end);
  }
  // Prefer wall-clock span across the process trail when timestamps exist.
  if (minStart < Infinity && maxEnd > minStart) return maxEnd - minStart;
  return stamped;
}

export function formatDuration(milliseconds: number) {
  const seconds = Math.floor(Math.max(0, milliseconds) / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  return hours
    ? `${hours}h${String(minutes).padStart(2, "0")}m${String(rest).padStart(2, "0")}s`
    : minutes
      ? `${minutes}m${String(rest).padStart(2, "0")}s`
      : `${rest}s`;
}
