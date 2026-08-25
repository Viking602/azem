import { useMemo, useState, type CSSProperties } from "react";
import { Check, GitFork, Pencil, X } from "lucide-react";
import type { SessionGraphEntry, SessionTree, SessionTreeNode, Snapshot } from "../../types";

interface FlatEntry {
  entry: SessionGraphEntry;
  depth: number;
  current: boolean;
  activePath: boolean;
}

export interface SessionTreePanelProps {
  tree: SessionTree;
  language: Snapshot["language"];
  running: boolean;
  busyEntry?: string;
  onNavigate: (entryId: string) => Promise<void>;
  onLabel: (entryId: string, label: string) => Promise<void>;
  onFork: (targetId: string, entryId: string) => Promise<void>;
}

export function flattenSessionTree(tree: SessionTree): FlatEntry[] {
  const parents = new Map<string, string>();
  const collectParents = (nodes: SessionTreeNode[]) => nodes.forEach((node) => {
    if (node.entry.parentId) parents.set(node.entry.id, node.entry.parentId);
    collectParents(node.children ?? []);
  });
  collectParents(tree.roots);
  const active = new Set<string>();
  let cursor = tree.activeLeafEntryId ?? "";
  while (cursor && !active.has(cursor)) {
    active.add(cursor);
    cursor = parents.get(cursor) ?? "";
  }
  const result: FlatEntry[] = [];
  const visit = (nodes: SessionTreeNode[], depth: number) => nodes.forEach((node) => {
    result.push({ entry: node.entry, depth, current: node.entry.id === tree.activeLeafEntryId, activePath: active.has(node.entry.id) });
    visit(node.children ?? [], depth + 1);
  });
  visit(tree.roots, 0);
  return result;
}

export function SessionTreePanel({ tree, language, running, busyEntry, onNavigate, onLabel, onFork }: SessionTreePanelProps) {
  const entries = useMemo(() => flattenSessionTree(tree), [tree]);
  const [editing, setEditing] = useState<string | null>(null);
  const [label, setLabel] = useState("");
  const [forkOpen, setForkOpen] = useState(false);
  const [targetId, setTargetId] = useState("");
  const zh = language === "zh-CN";
  const branchSummary = tree.branches.map((branch) => `${branch.name}${branch.active ? " · current" : ""}`).join(" · ");

  const beginLabel = (entry: SessionGraphEntry) => {
    setEditing(entry.id);
    setLabel(entry.label ?? "");
  };

  return <div className="session-tree-panel">
    <p className="session-tree-branches"><span>{zh ? "分支" : "Branches"}</span>{branchSummary || "main"}</p>
    {entries.length === 0 ? <p className="thread-environment-empty">{zh ? "当前会话还没有消息。" : "This session has no entries yet."}</p> : <ol className="session-tree-list" aria-label={zh ? "会话树" : "Session tree"}>
      {entries.map(({ entry, depth, current, activePath }) => <li key={entry.id} data-current={String(current)} data-active-path={String(activePath)} style={{ "--tree-depth": depth } as CSSProperties}>
        <span className="session-tree-mark" aria-hidden="true">{current ? <Check size={9} /> : ""}</span>
        <button
          type="button"
          className="session-tree-entry"
          disabled={running || current || busyEntry === entry.id}
          aria-current={current ? "step" : undefined}
          aria-label={`${entry.label || entry.kind}, ${zh ? `序号 ${entry.sequence}` : `sequence ${entry.sequence}`}${current ? zh ? "，当前条目" : ", current entry" : ""}`}
          onClick={() => void onNavigate(entry.id)}
        >
          <strong>{entry.label || entry.kind.replaceAll("_", " ")}</strong><small>#{entry.sequence}</small>
        </button>
        <button type="button" className="session-tree-edit" aria-label={zh ? "编辑标签" : "Edit label"} onClick={() => beginLabel(entry)}><Pencil size={11} /></button>
        {editing === entry.id ? <form className="session-tree-label-form" onSubmit={(event) => {
          event.preventDefault();
          void onLabel(entry.id, label).then(() => setEditing(null));
        }}>
          <label><span>{zh ? "条目标签" : "Entry label"}</span><input value={label} maxLength={128} autoFocus onChange={(event) => setLabel(event.target.value)} /></label>
          <button type="submit">{zh ? "保存" : "Save"}</button>
          <button type="button" aria-label={zh ? "取消编辑" : "Cancel editing"} onClick={() => setEditing(null)}><X size={12} /></button>
        </form> : null}
      </li>)}
    </ol>}
    <div className="session-tree-fork">
      {!forkOpen ? <button type="button" disabled={running} onClick={() => setForkOpen(true)}><GitFork size={12} />{zh ? "从当前位置创建分支" : "Fork from current entry"}</button> : <form onSubmit={(event) => {
        event.preventDefault();
        const target = targetId.trim();
        if (!target) return;
        void onFork(target, tree.activeLeafEntryId ?? "").then(() => { setTargetId(""); setForkOpen(false); });
      }}>
        <label><span>{zh ? "新会话 ID" : "New session ID"}</span><input value={targetId} maxLength={200} required autoFocus onChange={(event) => setTargetId(event.target.value)} /></label>
        <button type="submit" disabled={!targetId.trim()}>{zh ? "创建" : "Create"}</button>
        <button type="button" aria-label={zh ? "取消创建" : "Cancel fork"} onClick={() => setForkOpen(false)}><X size={12} /></button>
      </form>}
    </div>
  </div>;
}
