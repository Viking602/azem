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
  return name === "coding.edit_hashline" || name === "coding.write_file";
}

export function fileChangesForBlock(block: Block): FileChange[] {
  if (block.kind === "diff") return diffEventChanges(block);
  if (block.kind !== "tool" || !isFileChangeTool(block.title || block.data?.name || "")) return [];
  if (block.state && block.state !== "completed") return [];

  const name = block.title || block.data?.name || "";
  if (name === "coding.write_file") return writeFileChanges(block.data?.arguments || "");

  const structured = parseStructuredSections(block.data?.structured || "");
  return normalizeSections(structured.length ? structured : parseCompactEditOutput(block.content || ""));
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
    if (line.startsWith("¶")) {
      flush();
      current = { path: line.slice(1).split("#", 1)[0]?.trim() || "", firstChangedLine: 1, lines: [], inDiff: false };
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
