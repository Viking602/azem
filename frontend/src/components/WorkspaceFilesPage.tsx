import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft, Binary, ChevronDown, ChevronRight, ChevronsDownUp, Copy, FileCode2, Folder, FolderOpen,
  RefreshCw, X, LoaderCircle, Search, Paperclip,
} from "lucide-react";
import { isDesktopRuntime, listWorkspaceEntries, readWorkspaceFile } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { WorkspaceDirectory, WorkspaceEntry, WorkspaceFile } from "../types";
import FileTypeIcon, { fileBasename } from "./FileTypeIcon";
import { syntaxTokens } from "./syntaxTokens";

const MAX_OPEN_TABS = 8;
const LINE_HEIGHT = 22;
const VIEWPORT_OVERSCAN = 24;

export default function WorkspaceFilesPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setView = useRuntimeStore((state) => state.setView);
  const setError = useRuntimeStore((state) => state.setError);
  const [directories, setDirectories] = useState<Record<string, WorkspaceDirectory>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loadingDirectories, setLoadingDirectories] = useState<Set<string>>(new Set());
  const [openPaths, setOpenPaths] = useState<string[]>([]);
  const [activePath, setActivePath] = useState("");
  const [files, setFiles] = useState<Record<string, WorkspaceFile>>({});
  const [loadingFile, setLoadingFile] = useState(false);
  const [query, setQuery] = useState("");
  const searchInput = useRef<HTMLInputElement>(null);
  const t = translator(snapshot.language);

  const loadDirectory = useCallback(async (path: string) => {
    setLoadingDirectories((current) => new Set(current).add(path));
    try {
      const directory = await listWorkspaceEntries(path);
      setDirectories((current) => ({ ...current, [path]: directory }));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoadingDirectories((current) => {
        const next = new Set(current);
        next.delete(path);
        return next;
      });
    }
  }, [setError]);

  const loadFile = useCallback(async (path: string) => {
    setActivePath(path);
    setOpenPaths((current) => {
      if (current.includes(path)) return current;
      const evicted = current.length >= MAX_OPEN_TABS ? current[0] : "";
      if (evicted) setFiles((known) => {
        const next = { ...known };
        delete next[evicted];
        return next;
      });
      return [...current.slice(-(MAX_OPEN_TABS - 1)), path];
    });
    setLoadingFile(true);
    try {
      const file = await readWorkspaceFile(path);
      setFiles((current) => ({ ...current, [path]: file }));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoadingFile(false);
    }
  }, [setError]);

  useEffect(() => {
    setDirectories({});
    setExpanded(new Set());
    setOpenPaths([]);
    setActivePath("");
    setFiles({});
    void loadDirectory("");
    if (!isDesktopRuntime()) {
      const demoPath = "frontend/src/components/Timeline.tsx";
      setExpanded(new Set(["frontend", "frontend/src"]));
      void Promise.all([loadDirectory("frontend"), loadDirectory("frontend/src"), loadDirectory("frontend/src/components")]);
      void loadFile(demoPath);
    }
  }, [loadDirectory, snapshot.workspace]);

  const toggleDirectory = (path: string) => {
    const opening = !expanded.has(path);
    setExpanded((current) => {
      const next = new Set(current);
      opening ? next.add(path) : next.delete(path);
      return next;
    });
    if (opening && !directories[path]) void loadDirectory(path);
  };

  const closeTab = (path: string) => {
    setFiles((current) => {
      const next = { ...current };
      delete next[path];
      return next;
    });
    setOpenPaths((current) => {
      const index = current.indexOf(path);
      const next = current.filter((item) => item !== path);
      if (activePath === path) setActivePath(next[Math.min(index, next.length - 1)] ?? "");
      return next;
    });
  };

  const refresh = () => {
    setDirectories({});
    setExpanded(new Set());
    void loadDirectory("");
    if (activePath) void loadFile(activePath);
  };

  const activeFile = files[activePath];
  return <section className="workspace-files-page">
    <header className="workspace-files-header titlebar-region">
      <div className="workspace-subpage-heading">
        <button type="button" className="workspace-back-link" onClick={() => setView("projects")} aria-label={t("backToWorkspace")}><ArrowLeft size={14} /><span>{t("workspace")}</span><kbd>Esc</kbd></button>
        <div><span className="eyebrow">WORKSPACE FILES</span><h1>{snapshot.language === "zh-CN" ? "项目文件" : "Project files"}</h1><p>{snapshot.language === "zh-CN" ? "让目录层级更清楚，预览与上下文动作保持同一视线。" : "Keep hierarchy, preview, and context actions in one sightline."}</p></div>
      </div>
      <div className="workspace-files-actions">
        <button type="button" className="small-button" onClick={() => searchInput.current?.focus()}><Search size={13} />{snapshot.language === "zh-CN" ? "搜索文件" : "Search files"}</button>
      </div>
    </header>
    <div className="workspace-browser">
      <aside className="workspace-tree-pane" aria-label={t("fileTree")}>
        <label className="workspace-tree-search"><Search size={13} /><input ref={searchInput} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={snapshot.language === "zh-CN" ? "按名称筛选" : "Filter by name"} /><kbd>⌘P</kbd></label>
        <div className="workspace-tree-scroll" role="tree">
          {loadingDirectories.has("") && !directories[""] ? <div className="workspace-tree-loading"><LoaderCircle className="spin" size={15} />{t("loadingFiles")}</div> :
            <TreeBranch parent="" depth={0} directories={directories} expanded={expanded} loading={loadingDirectories} activePath={activePath} query={query} onToggle={toggleDirectory} onOpen={(path) => void loadFile(path)} />}
          {directories[""]?.truncated && <div className="workspace-tree-notice">{t("fileListTruncated")}</div>}
        </div>
      </aside>
      <main className="workspace-viewer-pane">
        {activePath ? <>
          <div className="workspace-file-toolbar">
            <div className="workspace-active-file">{isDesktopRuntime() ? <FileTypeIcon path={activePath} /> : <span className={`prototype-file-kind ${activePath.endsWith(".css") ? "css" : "ts"}`}>{activePath.endsWith(".css") ? "#" : "TS"}</span>}<strong>{fileBasename(activePath)}</strong><span>{pathParts(activePath).slice(0, -1).join("/")}</span></div>
            <div className="workspace-file-meta"><button type="button" className="small-button"><Paperclip size={13} />{snapshot.language === "zh-CN" ? "加入上下文" : "Add to context"}</button><button type="button" className="icon-button" aria-label={t("copyPath")} onClick={() => void navigator.clipboard?.writeText(activePath)}><Copy size={14} /></button><button type="button" className="icon-button" aria-label={t("refreshFiles")} onClick={refresh}><RefreshCw size={14} /></button></div>
          </div>
          <div className="workspace-file-content">
            {loadingFile && !activeFile ? <div className="workspace-file-empty"><LoaderCircle className="spin" size={24} /><p>{t("loadingFile")}</p></div> : <FilePreview file={activeFile} />}
          </div>
        </> : <div className="workspace-file-empty"><FileCode2 size={34} /><h2>{t("selectFile")}</h2><p>{t("selectFileHint")}</p></div>}
      </main>
    </div>
  </section>;
}

function TreeBranch({ parent, depth, directories, expanded, loading, activePath, query, onToggle, onOpen }: {
  parent: string; depth: number; directories: Record<string, WorkspaceDirectory>; expanded: Set<string>; loading: Set<string>; activePath: string;
  query: string;
  onToggle: (path: string) => void; onOpen: (path: string) => void;
}) {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const entries = (directories[parent]?.entries ?? []).filter((entry) => !normalizedQuery || entry.directory || entry.name.toLocaleLowerCase().includes(normalizedQuery));
  return <>{entries.map((entry) => <div key={entry.path} role="treeitem" aria-expanded={entry.directory ? expanded.has(entry.path) : undefined}>
    <button type="button" className={`workspace-tree-row ${activePath === entry.path ? "active" : ""} ${entry.hidden ? "hidden" : ""}`} style={{ "--tree-depth": depth } as React.CSSProperties}
      onClick={() => entry.directory ? onToggle(entry.path) : onOpen(entry.path)}>
      <span className="tree-caret">{entry.directory ? loading.has(entry.path) ? <LoaderCircle className="spin" size={12} /> : expanded.has(entry.path) ? <ChevronDown size={13} /> : <ChevronRight size={13} /> : null}</span>
      {entry.directory
        ? isDesktopRuntime() ? expanded.has(entry.path) ? <FolderOpen size={15} /> : <Folder size={15} /> : null
        : isDesktopRuntime() ? <FileTypeIcon path={entry.path} /> : <span className={`prototype-file-kind ${entry.path.endsWith(".css") ? "css" : "ts"}`}>{entry.path.endsWith(".css") ? "#" : "TS"}</span>}
      <span className="tree-entry-name">{entry.name}</span>
      {entry.directory && typeof entry.size === "number" && <small>{entry.size}</small>}
      {!entry.directory && !isDesktopRuntime() && <small>M</small>}
      {entry.symlink && <small>↗</small>}
    </button>
    {entry.directory && expanded.has(entry.path) && <div role="group"><TreeBranch parent={entry.path} depth={depth + 1} directories={directories} expanded={expanded} loading={loading} activePath={activePath} query={query} onToggle={onToggle} onOpen={onOpen} /></div>}
  </div>)}</>;
}

function FilePreview({ file }: { file?: WorkspaceFile }) {
  const t = translator(useRuntimeStore.getState().snapshot?.language ?? "zh-CN");
  if (!file) return null;
  if (file.kind === "image") return <div className="workspace-image-preview"><img src={`data:${file.mediaType};base64,${file.content}`} alt={file.name} /><span>{file.name} · {formatBytes(file.size)}</span></div>;
  if (file.kind === "binary") return <div className="workspace-file-empty"><Binary size={34} /><h2>{t("binaryPreviewUnavailable")}</h2><p>{t("binaryPreviewHint")}</p></div>;
  return <><VirtualCode content={file.content ?? ""} language={file.language} startLine={!isDesktopRuntime() && file.path.endsWith("Timeline.tsx") ? 462 : 1} />{file.truncated && <div className="workspace-preview-limit">{t("previewTruncated")}</div>}</>;
}

function VirtualCode({ content, language, startLine = 1 }: { content: string; language?: string; startLine?: number }) {
  const lines = useMemo(() => {
    const result = content.split("\n");
    if (result.length > 1 && result.at(-1) === "") result.pop();
    return result.length ? result : [""];
  }, [content]);
  const viewport = useRef<HTMLDivElement>(null);
  const scrollFrame = useRef(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [height, setHeight] = useState(600);
  useEffect(() => {
    if (!viewport.current) return;
    const observer = new ResizeObserver(([entry]) => setHeight(entry?.contentRect.height ?? 600));
    observer.observe(viewport.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (viewport.current) {
      viewport.current.scrollTop = 0;
      viewport.current.scrollLeft = 0;
    }
    setScrollTop(0);
    return () => cancelAnimationFrame(scrollFrame.current);
  }, [content]);
  const start = Math.max(0, Math.floor(scrollTop / LINE_HEIGHT) - VIEWPORT_OVERSCAN);
  const end = Math.min(lines.length, Math.ceil((scrollTop + height) / LINE_HEIGHT) + VIEWPORT_OVERSCAN);
  return <div className="workspace-code-viewport" ref={viewport} onScroll={(event) => {
    const nextTop = event.currentTarget.scrollTop;
    cancelAnimationFrame(scrollFrame.current);
    scrollFrame.current = requestAnimationFrame(() => setScrollTop(nextTop));
  }}>
    <div className="workspace-code-canvas" style={{ height: lines.length * LINE_HEIGHT }}>
      {lines.slice(start, end).map((line, offset) => <div className="workspace-code-line" key={start + offset} style={{ top: (start + offset) * LINE_HEIGHT }}>
        <span>{startLine + start + offset}</span><code>{line ? renderCodeLine(line, language) : " "}</code>
      </div>)}
    </div>
  </div>;
}

function renderCodeLine(line: string, language?: string) {
  if (!language || language === "text") return line;
  return syntaxTokens(line).map((token, index) => token.kind
    ? <span key={`${index}-${token.content}`} className={`syntax-${token.kind}`}>{token.content}</span>
    : token.content);
}

function pathParts(path: string) { return path.split("/").filter(Boolean); }
function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(size < 10 * 1024 ? 1 : 0)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}
