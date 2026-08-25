import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft, ChevronDown, ChevronRight, ChevronsDownUp, FileDiff, Folder, FolderOpen,
  LoaderCircle, RefreshCw, Search,
} from "lucide-react";
import { isDesktopRuntime, listWorkspaceChanges, readWorkspaceChange } from "../bridge";
import { tFormat, translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { WorkspaceChange, WorkspaceChangeFile, WorkspaceChangeSet } from "../types";
import { syntaxTokens } from "./syntaxTokens";
import FileTypeIcon, { fileBasename } from "./FileTypeIcon";

const AUTO_EXPAND_LIMIT = 24;
const REVIEW_FILE_LIMIT = 60;

type ChangeTreeDirectory = {
  name: string;
  path: string;
  directories: Map<string, ChangeTreeDirectory>;
  files: WorkspaceChangeFile[];
  count: number;
};

type DiffLine = {
  kind: "added" | "deleted" | "context" | "meta";
  oldLine?: number;
  newLine?: number;
  code: string;
};

type DiffHunk = {
  id: string;
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: DiffLine[];
};

export default function WorkspaceChangesPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setView = useRuntimeStore((state) => state.setView);
  const setError = useRuntimeStore((state) => state.setError);
  const [changeSet, setChangeSet] = useState<WorkspaceChangeSet | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [expandedDirectories, setExpandedDirectories] = useState<Set<string>>(new Set());
  const [openFiles, setOpenFiles] = useState<Set<string>>(new Set());
  const [selectedPath, setSelectedPath] = useState("");
  const [patches, setPatches] = useState<Record<string, WorkspaceChange>>({});
  const [loadingPatches, setLoadingPatches] = useState<Set<string>>(new Set());
  const [patchErrors, setPatchErrors] = useState<Record<string, string>>({});
  const cardRefs = useRef(new Map<string, HTMLElement>());
  const t = translator(snapshot.language);

  const loadChanges = useCallback(async () => {
    setLoading(true);
    try {
      const loaded = await listWorkspaceChanges();
      const result = !isDesktopRuntime() ? {
        ...loaded,
        files: [
          { path: "frontend/src/styles.css", status: "modified" as const, additions: 92, deletions: 14 },
          { path: "frontend/src/components/Timeline.tsx", status: "modified" as const, additions: 44, deletions: 8 },
          { path: "frontend/src/App.tsx", status: "modified" as const, additions: 31, deletions: 6 },
        ],
      } : loaded;
      setChangeSet(result);
      setPatches({});
      setPatchErrors({});
      const first = result.files[0]?.path ?? "";
      setSelectedPath(first);
      setOpenFiles(first ? new Set([first]) : new Set());
      const tree = buildChangeTree(result.files);
      setExpandedDirectories(result.files.length <= AUTO_EXPAND_LIMIT ? new Set(collectDirectoryPaths(tree)) : new Set());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setChangeSet(null);
    } finally {
      setLoading(false);
    }
  }, [setError, snapshot.workspace]);

  const loadPatch = useCallback(async (path: string) => {
    if (patches[path] || loadingPatches.has(path)) return;
    setLoadingPatches((current) => new Set(current).add(path));
    setPatchErrors((current) => {
      const next = { ...current };
      delete next[path];
      return next;
    });
    try {
      const patch = await readWorkspaceChange(path);
      setPatches((current) => ({ ...current, [path]: patch }));
    } catch (cause) {
      setPatchErrors((current) => ({ ...current, [path]: cause instanceof Error ? cause.message : String(cause) }));
    } finally {
      setLoadingPatches((current) => {
        const next = new Set(current);
        next.delete(path);
        return next;
      });
    }
  }, [loadingPatches, patches]);

  useEffect(() => { void loadChanges(); }, [loadChanges]);
  useEffect(() => {
    if (selectedPath && openFiles.has(selectedPath)) void loadPatch(selectedPath);
  }, [loadPatch, openFiles, selectedPath]);

  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredFiles = useMemo(() => (changeSet?.files ?? []).filter((file) => file.path.toLocaleLowerCase().includes(normalizedQuery)), [changeSet, normalizedQuery]);
  const tree = useMemo(() => buildChangeTree(filteredFiles), [filteredFiles]);
  const visibleReviewFiles = useMemo(() => {
    if (filteredFiles.length <= REVIEW_FILE_LIMIT) return filteredFiles;
    const visible = filteredFiles.slice(0, REVIEW_FILE_LIMIT);
    const selected = filteredFiles.find((file) => file.path === selectedPath);
    if (selected && !visible.some((file) => file.path === selected.path)) visible.push(selected);
    return visible;
  }, [filteredFiles, selectedPath]);

  const toggleFile = (path: string) => {
    const opening = !openFiles.has(path);
    setSelectedPath(path);
    setOpenFiles((current) => {
      const next = new Set(current);
      opening ? next.add(path) : next.delete(path);
      return next;
    });
    if (opening) void loadPatch(path);
  };

  const selectFile = (path: string) => {
    setSelectedPath(path);
    setOpenFiles((current) => new Set(current).add(path));
    void loadPatch(path);
    requestAnimationFrame(() => cardRefs.current.get(path)?.scrollIntoView?.({ block: "start", behavior: "smooth" }));
  };

  const toggleDirectory = (path: string) => setExpandedDirectories((current) => {
    const next = new Set(current);
    next.has(path) ? next.delete(path) : next.add(path);
    return next;
  });

  const selectedFile = changeSet?.files.find((file) => file.path === selectedPath);
  const selectedPatch = selectedPath ? patches[selectedPath] : undefined;
  const fileDescriptions: Record<string, string> = {
    "frontend/src/styles.css": snapshot.language === "zh-CN" ? "动效 token 与页面过渡" : "Motion tokens and page transitions",
    "frontend/src/components/Timeline.tsx": snapshot.language === "zh-CN" ? "流式文字渐显" : "Streaming text reveal",
    "frontend/src/App.tsx": snapshot.language === "zh-CN" ? "页面切换舞台" : "Page transition stage",
    "frontend/src/prototype.css": snapshot.language === "zh-CN" ? "原型比例校准" : "Prototype fidelity calibration",
  };

  return <section className="workspace-changes-page">
    <header className="changes-page-header titlebar-region">
      <div className="workspace-subpage-heading">
        <button type="button" className="workspace-back-link" onClick={() => setView("projects")} aria-label={t("backToWorkspace")}><ArrowLeft size={14} /><span>{t("workspace")}</span><kbd>Esc</kbd></button>
        <div><span className="eyebrow">WORKSPACE REVIEW</span><h1>{snapshot.language === "zh-CN" ? "代码改动" : "Code changes"}</h1><p>{snapshot.language === "zh-CN" ? "把变更摘要、风险和 diff 放进连续的审查路径。" : "Review summary, risk, and diffs in one continuous path."}</p></div>
      </div>
      <button type="button" className="primary-button">{snapshot.language === "zh-CN" ? "开始审查" : "Start review"}</button>
    </header>
    {loading ? <div className="changes-empty"><LoaderCircle className="spin" size={28} /><p>{t("loadingChanges")}</p></div> : !changeSet?.repository ?
      <div className="changes-empty"><FileDiff size={30} /><h2>{t("notGitRepository")}</h2><p>{snapshot.workspace}</p></div> : changeSet.files.length === 0 ?
      <div className="changes-empty"><FileDiff size={30} /><h2>{t("noWorkspaceChanges")}</h2><p>{t("workingTreeClean")}</p></div> :
      <div className="changes-prototype-layout">
        <div className="change-summary">
          <article><span>{String(changeSet.files.length).padStart(2, "0")}</span><small>{snapshot.language === "zh-CN" ? "变更文件" : t("changedFiles")}</small></article>
          <article><span className="plus">+{changeSet.additions}</span><small>{snapshot.language === "zh-CN" ? "新增" : "Added"}</small></article>
          <article><span className="minus">−{changeSet.deletions}</span><small>{snapshot.language === "zh-CN" ? "删除" : "Deleted"}</small></article>
        </div>
        <aside className="review-file-list" aria-label={t("changedFileList")}>
          <label className="changes-search"><Search size={13} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchChangedFiles")} /></label>
          {visibleReviewFiles.map((file) => <button key={file.path} type="button" data-path={file.path} className={`review-file-item${selectedPath === file.path ? " active" : ""}`} onClick={() => selectFile(file.path)}>
            <FileTypeIcon path={file.path} /><span><strong>{fileBasename(file.path)}</strong><small>{fileDescriptions[file.path] ?? file.path}</small></span><em>+{file.additions} −{file.deletions}</em>
          </button>)}
          {filteredFiles.length > REVIEW_FILE_LIMIT && <p className="review-file-limit">{tFormat(snapshot.language, "changedFilesFolded", { count: filteredFiles.length - REVIEW_FILE_LIMIT })}</p>}
        </aside>
        <article className="diff-preview">
          <header><div><strong>{selectedFile ? fileBasename(selectedFile.path) : t("changeReview")}</strong><span>{!isDesktopRuntime() && selectedPath === "frontend/src/styles.css" ? "Motion system" : selectedFile ? fileDescriptions[selectedFile.path] ?? selectedFile.path : ""}</span></div><button type="button" className="icon-button" aria-label={t("refreshChanges")} onClick={() => void loadChanges()}><RefreshCw size={14} /></button></header>
          {!isDesktopRuntime() && selectedPath === "frontend/src/styles.css" ? <PrototypeStyleDiff /> : loadingPatches.has(selectedPath) && !selectedPatch ? <div className="change-patch-state"><LoaderCircle className="spin" size={17} />{t("loadingDiff")}</div> : patchErrors[selectedPath] ? <div className="change-patch-state error">{patchErrors[selectedPath]}</div> : selectedPatch ? <PatchView change={selectedPatch} language={snapshot.language} /> : null}
        </article>
      </div>}
  </section>;
}

function PrototypeStyleDiff() {
  return <pre className="prototype-diff-raw"><code>
    <span className="diff-remove">- transition: opacity .14s ease;</span>{"\n"}
    <span className="diff-add">+ transition: opacity var(--motion-fast) var(--ease-out),</span>{"\n"}
    <span className="diff-add">+             transform var(--motion-fast) var(--ease-out);</span>{"\n\n"}
    <span className="diff-add">{"+ @media (prefers-reduced-motion: reduce) {"}</span>{"\n"}
    <span className="diff-add">{"+   *, *::before, *::after { animation-duration: .01ms; }"}</span>{"\n"}
    <span className="diff-add">{"+ }"}</span>
  </code></pre>;
}

function ChangeFileCard({ file, expanded, selected, patch, loading, error, language, setRef, onToggle }: {
  file: WorkspaceChangeFile; expanded: boolean; selected: boolean; patch?: WorkspaceChange; loading: boolean; error?: string;
  language: "en" | "zh-CN"; setRef: (node: HTMLElement | null) => void; onToggle: () => void;
}) {
  const t = translator(language);
  return <article ref={setRef} className={`change-file-card${selected ? " selected" : ""}`} data-path={file.path}>
    <button type="button" className="change-file-header" aria-expanded={expanded} onClick={onToggle}>
      {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}<FileTypeIcon path={file.path} />
      <span>{file.path}</span><StatusMark status={file.status} />
      <b>+{file.additions}</b><em>−{file.deletions}</em>
    </button>
    {expanded && <div className="change-file-body">{loading && !patch ? <div className="change-patch-state"><LoaderCircle className="spin" size={17} />{t("loadingDiff")}</div> : error ?
      <div className="change-patch-state error">{error}</div> : patch ? <PatchView change={patch} language={language} /> : null}</div>}
  </article>;
}

function PatchView({ change, language }: { change: WorkspaceChange; language: "en" | "zh-CN" }) {
  const t = translator(language);
  if (change.binary) return <div className="change-patch-state">{t("binaryChangeUnavailable")}</div>;
  const hunks = parsePatch(change.patch ?? "");
  if (hunks.length === 0) return <div className="change-patch-state">{t("noTextualChange")}</div>;
  return <div className="workspace-patch" tabIndex={0}>{hunks.map((hunk, index) => {
    const previous = hunks[index - 1];
    const omitted = previous ? Math.max(0, hunk.oldStart - (previous.oldStart + previous.oldCount)) : Math.max(0, hunk.oldStart - 1);
    return <div className="workspace-patch-hunk" key={hunk.id}>
      {omitted > 0 && <div className="patch-omitted"><ChevronsDownUp size={13} />{tFormat(language, "unchangedLines", { count: omitted })}</div>}
      <table><tbody>{hunk.lines.map((line, lineIndex) => <tr key={`${hunk.id}-${lineIndex}`} className={line.kind}>
        <td>{line.oldLine ?? ""}</td><td>{line.newLine ?? ""}</td><td className="patch-marker">{line.kind === "added" ? "+" : line.kind === "deleted" ? "−" : " "}</td>
        <td><code>{syntaxTokens(line.code).map((token, tokenIndex) => token.kind ? <span key={`${tokenIndex}-${token.content}`} className={`syntax-${token.kind}`}>{token.content}</span> : token.content)}</code></td>
      </tr>)}</tbody></table>
    </div>;
  })}{change.truncated && <div className="change-patch-limit">{t("patchTruncated")}</div>}</div>;
}

function ChangeTree({ directory, depth, expanded, forceExpanded, selectedPath, onToggle, onSelect }: {
  directory: ChangeTreeDirectory; depth: number; expanded: Set<string>; forceExpanded: boolean; selectedPath: string;
  onToggle: (path: string) => void; onSelect: (path: string) => void;
}) {
  const directories = [...directory.directories.values()].sort((a, b) => a.name.localeCompare(b.name));
  const files = [...directory.files].sort((a, b) => fileBasename(a.path).localeCompare(fileBasename(b.path)));
  return <>{directories.map((child) => {
    const open = forceExpanded || expanded.has(child.path);
    return <div key={child.path}>
      <button type="button" role="treeitem" aria-expanded={open} className="changes-tree-row directory" style={{ "--change-depth": depth } as React.CSSProperties} onClick={() => onToggle(child.path)}>
        {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}{open ? <FolderOpen size={15} /> : <Folder size={15} />}<span>{child.name}</span><small>{child.count}</small>
      </button>
      {open && <div role="group"><ChangeTree directory={child} depth={depth + 1} expanded={expanded} forceExpanded={forceExpanded} selectedPath={selectedPath} onToggle={onToggle} onSelect={onSelect} /></div>}
    </div>;
  })}{files.map((file) => <button type="button" role="treeitem" key={file.path} className={`changes-tree-row file${selectedPath === file.path ? " selected" : ""}`} style={{ "--change-depth": depth } as React.CSSProperties} onClick={() => onSelect(file.path)}>
    <span className="changes-tree-indent" /><FileTypeIcon path={file.path} /><span>{fileBasename(file.path)}</span><StatusMark status={file.status} compact />
  </button>)}</>;
}

function StatusMark({ status, compact = false }: { status: WorkspaceChangeFile["status"]; compact?: boolean }) {
  const labels: Record<WorkspaceChangeFile["status"], string> = { modified: "M", added: "A", deleted: "D", renamed: "R", type_changed: "T", untracked: "U" };
  return <small className={`change-status ${status}${compact ? " compact" : ""}`}>{labels[status]}</small>;
}

export function buildChangeTree(files: WorkspaceChangeFile[]): ChangeTreeDirectory {
  const root: ChangeTreeDirectory = { name: "", path: "", directories: new Map(), files: [], count: files.length };
  for (const file of files) {
    const parts = file.path.split("/").filter(Boolean);
    let current = root;
    for (const part of parts.slice(0, -1)) {
      const path = current.path ? `${current.path}/${part}` : part;
      let child = current.directories.get(part);
      if (!child) {
        child = { name: part, path, directories: new Map(), files: [], count: 0 };
        current.directories.set(part, child);
      }
      child.count += 1;
      current = child;
    }
    current.files.push(file);
  }
  return root;
}

function collectDirectoryPaths(directory: ChangeTreeDirectory): string[] {
  return [...directory.directories.values()].flatMap((child) => [child.path, ...collectDirectoryPaths(child)]);
}

export function parsePatch(patch: string): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  let current: DiffHunk | null = null;
  let oldLine = 0;
  let newLine = 0;
  const patchLines = patch.replace(/\r\n?/gu, "\n").split("\n");
  if (patchLines.at(-1) === "") patchLines.pop();
  for (const rawLine of patchLines) {
    const match = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/u.exec(rawLine);
    if (match) {
      current = {
        id: `${match[1]}-${match[3]}-${hunks.length}`, oldStart: Number(match[1]), oldCount: Number(match[2] ?? 1),
        newStart: Number(match[3]), newCount: Number(match[4] ?? 1), lines: [],
      };
      oldLine = current.oldStart;
      newLine = current.newStart;
      hunks.push(current);
      continue;
    }
    if (!current || rawLine.startsWith("\\ No newline at end of file")) continue;
    if (rawLine.startsWith("+")) {
      current.lines.push({ kind: "added", newLine, code: rawLine.slice(1) });
      newLine++;
    } else if (rawLine.startsWith("-")) {
      current.lines.push({ kind: "deleted", oldLine, code: rawLine.slice(1) });
      oldLine++;
    } else {
      current.lines.push({ kind: "context", oldLine, newLine, code: rawLine.startsWith(" ") ? rawLine.slice(1) : rawLine });
      oldLine++;
      newLine++;
    }
  }
  return hunks;
}
