import type { Block } from "../types";

export interface FileChange {
  path: string;
  firstChangedLine: number;
  diff: string;
  additions: number;
  deletions: number;
}

export interface EditedFileSummary {
  files: Array<{ path: string; additions: number; deletions: number }>;
  additions: number;
  deletions: number;
}

type RawSection = {
  path?: unknown;
  firstChangedLine?: unknown;
  diff?: unknown;
};

export function isFileChangeTool(name = "") {
  return name === "coding.edit_hashline" || name === "coding.replace"
    || name === "coding.write_file" || name === "coding.delete_file";
}

const pendingFileChangeStates = new Set(["queued", "awaiting_approval", "reviewing_approval"]);

export function isPendingFileChangeBlock(block: Block) {
  const name = block.title || block.data?.name || "";
  return block.kind === "tool" && pendingFileChangeStates.has(block.state || "") && isFileChangeTool(name);
}

export function pendingFileEditPaths(block: Block): string[] {
  if (!isFileChangeTool(block.title || block.data?.name || "")) return [];
  const argumentsText = block.data?.arguments || block.content || "";
  try {
    const parsed = JSON.parse(argumentsText) as { path?: unknown; input?: unknown };
    if (typeof parsed.path === "string" && parsed.path.trim()) return [parsed.path.trim()];
    if (typeof parsed.input === "string") return hashlineHeaderPaths(parsed.input);
  } catch {
    return hashlineHeaderPaths(argumentsText);
  }
  return [];
}

export function isActiveFileChangeBlock(block: Block) {
  const name = block.title || block.data?.name || "";
  if (block.kind !== "tool"
    || !["running", "started", "streaming", "progress"].includes(block.state || "")
    || !isFileChangeTool(name)) return false;
  if (name !== "coding.edit_hashline") return true;
  try {
    const input = JSON.parse(block.data?.arguments || block.content || "") as { dryRun?: unknown };
    return input.dryRun !== true;
  } catch {
    return true;
  }
}

export function pendingFileChangeSummaryForBlock(block: Block): EditedFileSummary | null {
  if (!isActiveFileChangeBlock(block)) return null;
  const name = block.title || block.data?.name || "";
  const argumentsText = block.data?.arguments || block.content || "";
  if (name === "coding.write_file") {
    const changes = writeFileChanges(argumentsText);
    return changes.length ? summarizeChanges(changes) : null;
  }
  if (name !== "coding.edit_hashline") return null;
  try {
    const input = JSON.parse(argumentsText) as { input?: unknown; dryRun?: unknown };
    if (input.dryRun === true || typeof input.input !== "string") return null;
    return summarizeHashlinePlan(input.input);
  } catch {
    return null;
  }
}

export function fileChangesForBlock(block: Block): FileChange[] {
  if (block.kind === "diff") return diffEventChanges(block);
  if (block.kind !== "tool" || !isFileChangeTool(block.title || block.data?.name || "")) return [];
  if (block.state && block.state !== "completed") return [];

  // The backend projects completed file changes once (internal/toolview) and
  // ships them on live tool_finished events and durable tool records. Local
  // parsing below remains only as a fallback for sessions persisted before
  // the shared projection existed.
  const projected = projectedFileChanges(block.data?.fileChange || "");
  if (projected) return projected;

  const name = block.title || block.data?.name || "";
  if (name === "coding.write_file") return writeFileChanges(block.data?.arguments || "");

  const structured = parseStructuredSections(block.data?.structured || "");
  return normalizeSections(structured.length ? structured : parseCompactEditOutput(block.content || ""));
}

function projectedFileChanges(payload: string): FileChange[] | null {
  if (!payload.trim()) return null;
  try {
    const parsed = JSON.parse(payload) as { files?: unknown };
    if (!Array.isArray(parsed.files)) return null;
    const changes: FileChange[] = [];
    for (const raw of parsed.files as Array<Record<string, unknown>>) {
      const path = typeof raw.path === "string" ? raw.path.trim() : "";
      const diff = typeof raw.diff === "string" ? raw.diff : "";
      if (!path) continue;
      changes.push({
        path,
        firstChangedLine: positiveInteger(raw.firstChangedLine, 1),
        diff,
        additions: positiveCount(raw.additions),
        deletions: positiveCount(raw.deletions),
      });
    }
    return changes;
  } catch {
    return null;
  }
}

function positiveCount(value: unknown) {
  const number = Number(value);
  return Number.isInteger(number) && number > 0 ? number : 0;
}

export function aggregateEditedFiles(blocks: Block[]): EditedFileSummary {
  const byPath = new Map<string, { path: string; additions: number; deletions: number }>();
  for (const block of blocks) {
    if (block.kind !== "tool" || !isFileChangeTool(block.title || block.data?.name || "")) continue;
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

function summarizeChanges(changes: FileChange[]): EditedFileSummary {
  const files = changes.map(({ path, additions, deletions }) => ({ path, additions, deletions }));
  return {
    files,
    additions: files.reduce((total, file) => total + file.additions, 0),
    deletions: files.reduce((total, file) => total + file.deletions, 0),
  };
}

function hashlineHeaderPaths(input: string): string[] {
  const paths: string[] = [];
  for (const rawLine of input.replace(/\r\n?/gu, "\n").split("\n")) {
    const line = rawLine.trim();
    const marker = line.startsWith("¶") ? "¶" : line.startsWith("[") ? "[" : "";
    if (!marker) continue;
    const hash = line.lastIndexOf("#");
    if (hash <= marker.length) continue;
    const path = line.slice(marker.length, hash).trim();
    if (path && !paths.includes(path)) paths.push(path);
  }
  return paths;
}

function summarizeHashlinePlan(input: string): EditedFileSummary | null {
  const state: HashlinePlanState = { byPath: new Map(), current: null, bodyRequired: false, bodyLines: 0 };
  for (const rawLine of input.replace(/\r\n?/gu, "\n").split("\n")) {
    if (!consumeHashlinePlanLine(state, rawLine.trimEnd())) return null;
  }
  if (!finishHashlineOperation(state) || state.byPath.size === 0) return null;

  const files = [...state.byPath.values()];
  return {
    files,
    additions: files.reduce((total, file) => total + file.additions, 0),
    deletions: files.reduce((total, file) => total + file.deletions, 0),
  };
}

type HashlinePlanFile = { path: string; additions: number; deletions: number };
type HashlinePlanState = {
  byPath: Map<string, HashlinePlanFile>;
  current: HashlinePlanFile | null;
  bodyRequired: boolean;
  bodyLines: number;
};

function consumeHashlinePlanLine(state: HashlinePlanState, line: string) {
  if (line === "*** Begin Patch" || line === "*** End Patch") return finishHashlineOperation(state);
  if (line.startsWith("[") || line.startsWith("¶")) return openHashlinePlanSection(state, line);
  if (!line.trim()) return finishHashlineOperation(state);
  if (!state.current) return false;
  if (line.startsWith("+")) return appendHashlinePlanBody(state);
  if (!finishHashlineOperation(state)) return false;

  const operation = plannedHashlineOperation(line);
  if (!operation) return false;
  state.current.deletions += operation.deletions;
  state.bodyRequired = operation.bodyRequired;
  return true;
}

function openHashlinePlanSection(state: HashlinePlanState, line: string) {
  if (!finishHashlineOperation(state)) return false;
  const markerLength = line.startsWith("¶") ? 1 : line.startsWith("[") ? 1 : 0;
  const hash = line.lastIndexOf("#");
  const path = hash > markerLength ? line.slice(markerLength, hash).trim() : "";
  if (!path) return false;
  state.current = state.byPath.get(path) ?? { path, additions: 0, deletions: 0 };
  state.byPath.set(path, state.current);
  return true;
}

function appendHashlinePlanBody(state: HashlinePlanState) {
  if (!state.bodyRequired || !state.current) return false;
  state.current.additions += 1;
  state.bodyLines += 1;
  return true;
}

function finishHashlineOperation(state: HashlinePlanState) {
  if (state.bodyRequired && state.bodyLines === 0) return false;
  state.bodyRequired = false;
  state.bodyLines = 0;
  return true;
}

function plannedHashlineOperation(line: string): { deletions: number; bodyRequired: boolean } | null {
  const range = /^PUT (\d+)\.=(\d+):$/u.exec(line);
  if (range) {
    const start = Number(range[1]);
    const end = Number(range[2]);
    if (start < 1 || end < start) return null;
    return { deletions: end - start + 1, bodyRequired: true };
  }
  const cut = /^CUT (\d+)\.=(\d+)(?: @[A-Za-z0-9_-]+)?$/u.exec(line);
  if (cut) {
    const start = Number(cut[1]);
    const end = Number(cut[2]);
    if (start < 1 || end < start) return null;
    return { deletions: end - start + 1, bodyRequired: false };
  }
  if (/^PUT (?:<\d+|>\d+|>\$):$/u.test(line)) return { deletions: 0, bodyRequired: true };
  if (/^(?:MV .+|REM)$/u.test(line)) return { deletions: 0, bodyRequired: false };
  return null;
}

function parseStructuredSections(value: string): RawSection[] {
  if (!value.trim()) return [];
  try {
    const parsed = JSON.parse(value) as { sections?: unknown; Sections?: unknown };
    const sections = parsed.sections ?? parsed.Sections;
    return Array.isArray(sections) ? sections as RawSection[] : [];
  } catch {
    return [];
  }
}

function writeFileChanges(argumentsText: string): FileChange[] {
  try {
    const input = JSON.parse(argumentsText) as { path?: unknown; content?: unknown };
    const path = typeof input.path === "string" ? input.path.trim() : "";
    const content = typeof input.content === "string" ? input.content : "";
    if (!path) return [];
    const lines = content ? content.replace(/\n$/u, "").split("\n") : [];
    return normalizeSections([{ path, firstChangedLine: 1, diff: lines.map((line) => `+${line}`).join("\n") }]);
  } catch {
    return [];
  }
}

function normalizeSections(sections: RawSection[]): FileChange[] {
  const changes: FileChange[] = [];
  for (const section of sections) {
    const path = typeof section.path === "string" ? section.path.trim() : "";
    const diff = typeof section.diff === "string" ? section.diff.replace(/\r\n?/gu, "\n") : "";
    if (!path || !diff) continue;
    const firstChangedLine = positiveInteger(section.firstChangedLine, 1);
    const { additions, deletions } = countChanges(diff);
    changes.push({ path, firstChangedLine, diff, additions, deletions });
  }
  return changes;
}

function parseCompactEditOutput(output: string): RawSection[] {
  const sections: RawSection[] = [];
  let current: { path: string; firstChangedLine: number; lines: string[]; inDiff: boolean } | null = null;
  const flush = () => {
    if (current?.path && current.lines.length) {
      sections.push({ path: current.path, firstChangedLine: current.firstChangedLine, diff: current.lines.join("\n") });
    }
  };
  for (const line of output.replace(/\r\n?/gu, "\n").split("\n")) {
    if ((line.startsWith("[") && line.endsWith("]")) || line.startsWith("¶")) {
      flush();
      const marker = 1;
      const hash = line.lastIndexOf("#");
      current = { path: hash > marker ? line.slice(marker, hash).trim() : "", firstChangedLine: 1, lines: [], inDiff: false };
      continue;
    }
    if (!current) continue;
    if (line.startsWith("firstChangedLine: ")) {
      current.firstChangedLine = positiveInteger(line.slice("firstChangedLine: ".length), 1);
      continue;
    }
    if (line === "--- compact diff ---") {
      current.inDiff = true;
      continue;
    }
    if (current.inDiff) current.lines.push(line);
  }
  flush();
  return sections;
}

function diffEventChanges(block: Block): FileChange[] {
  const content = (block.content || "").replace(/\r\n?/gu, "\n");
  if (!content.trim()) return [];
  const lines = content.split("\n");
  const hunk = lines.find((line) => line.startsWith("@@")) || "";
  const firstChangedLine = positiveInteger(/\+(\d+)/u.exec(hunk)?.[1] || /-(\d+)/u.exec(hunk)?.[1], 1);
  const body = lines.filter((line) => !line.startsWith("diff --git ") && !line.startsWith("--- ") && !line.startsWith("+++ ") && !line.startsWith("@@"));
  const diff = body.join("\n");
  const { additions, deletions } = countChanges(diff);
  return [{ path: block.title || block.data?.path || "change", firstChangedLine, diff, additions, deletions }];
}

function countChanges(diff: string) {
  let additions = 0;
  let deletions = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) additions += 1;
    else if (line.startsWith("-") && !line.startsWith("---")) deletions += 1;
  }
  return { additions, deletions };
}

function positiveInteger(value: unknown, fallback: number) {
  const number = Number(value);
  return Number.isInteger(number) && number > 0 ? number : fallback;
}
