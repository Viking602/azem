import { FileText, Image, Pencil, Search, ShieldCheck, SquareTerminal, Wrench } from "lucide-react";
import { useState, type ReactNode, type ToggleEvent } from "react";
import { tFormat, type Language } from "../../i18n";
import { FILE_CHANGE_PILL_LIMIT, toolChipBasename, type ToolChipKind } from "../toolChip";
import { ToolRow } from "./Primitives";

export function ToolChip({
  state,
  kind,
  label,
  chip,
  status,
  expandable = true,
  className = "",
  nested = false,
  compact = false,
  role,
  "aria-current": ariaCurrent,
  onToggle,
  children,
}: {
  state?: string;
  kind: ToolChipKind;
  label: string;
  chip?: string;
  status?: string;
  expandable?: boolean;
  className?: string;
  nested?: boolean;
  compact?: boolean;
  role?: string;
  "aria-current"?: "step";
  onToggle?: (event: ToggleEvent<HTMLDetailsElement>) => void;
  children?: ReactNode;
}) {
  const approval = state === "awaiting_approval" || state === "reviewing_approval";
  return <ToolRow
    className={`bui-tool-chip tool-block work-entry ${nested ? "nested" : ""} ${compact ? "compact" : ""} ${className}`.trim()}
    state={state}
    role={role}
    aria-current={ariaCurrent}
    onToggle={onToggle}
  >
    <summary>
      <span className="bui-tool-chip-icon work-entry-icon" data-icon={approval ? "shield" : kind} aria-hidden="true">
        <ToolChipIcon kind={kind} approval={approval} />
      </span>
      <strong className="bui-tool-chip-label work-entry-label">{label}</strong>
      {chip ? <span className="bui-tool-chip-detail">{chip}</span> : null}
      {status ? <span className="tool-status">{status}</span> : null}
    </summary>
    {children}
  </ToolRow>;
}

export function ToolChipPending({
  state,
  label,
  chip,
  status,
  className = "",
  nested = false,
}: {
  state: string;
  label: string;
  chip?: string;
  status: string;
  className?: string;
  nested?: boolean;
}) {
  return <article
    className={`bui-tool-chip file-change-entry work-entry pending-file-edit ${nested ? "nested" : ""} ${className}`.trim()}
    data-state={state}
    aria-busy="true"
  >
    <div className="file-change-pending-summary">
      <span className="work-entry-icon bui-tool-chip-icon" data-icon="shield" aria-hidden="true">
        <ShieldCheck size={13} />
      </span>
      <strong className="bui-tool-chip-label work-entry-label">{label}</strong>
      {chip ? <span className="bui-tool-chip-detail">{chip}</span> : null}
      <span className="tool-status">{status}</span>
    </div>
  </article>;
}

export function ToolChipStatus({ lines }: { lines: string[] }) {
  if (!lines.length) return null;
  return <ul className="bui-tool-chip-status">
    {lines.map((line, index) => <li key={`${line}-${index}`}><span aria-hidden="true">✓</span>{line}</li>)}
  </ul>;
}

export function ToolChipMeta({ lines }: { lines: string[] }) {
  if (!lines.length) return null;
  return <div className="bui-tool-chip-meta">{lines.map((line, index) => <p key={`${line}-${index}`}>{line}</p>)}</div>;
}

export function FileChangePills({
  files,
  language,
  limit = FILE_CHANGE_PILL_LIMIT,
}: {
  files: Array<{ path: string; additions: number; deletions: number }>;
  language: Language;
  limit?: number;
}) {
  const [expanded, setExpanded] = useState(false);
  if (!files.length) return null;
  const hidden = Math.max(0, files.length - limit);
  const visible = expanded ? files : files.slice(0, limit);
  return <div className="bui-file-change-pills" role="list">
    {visible.map((file) => <span key={file.path} className="bui-file-change-pill" role="listitem">
      <span className="bui-file-change-pill-name">{toolChipBasename(file.path)}</span>
      {file.additions > 0 ? <span className="plus">+{file.additions}</span> : null}
      {file.deletions > 0 ? <span className="minus">-{file.deletions}</span> : null}
    </span>)}
    {hidden > 0 && !expanded ? <button
      type="button"
      className="bui-file-change-more"
      onClick={() => setExpanded(true)}
    >
      {tFormat(language, "fileChangeMore", { count: hidden })}
    </button> : null}
  </div>;
}

function ToolChipIcon({ kind, approval }: { kind: ToolChipKind; approval: boolean }) {
  if (approval) return <ShieldCheck size={13} />;
  if (kind === "thinking") {
    return <span className="azem-thinking-mark bui-thinking-mark" aria-hidden="true"><i /><i /></span>;
  }
  if (kind === "write" || kind === "edit") return <Pencil size={13} />;
  if (kind === "image") return <Image size={13} />;
  if (kind === "read") return <FileText size={13} />;
  if (kind === "shell") return <SquareTerminal size={13} />;
  if (kind === "search") return <Search size={13} />;
  return <Wrench size={13} />;
}
