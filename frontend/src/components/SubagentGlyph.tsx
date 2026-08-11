import type { CSSProperties } from "react";
import { subagentVisualIdentity, type SubagentGlyphKind } from "../subagents";
import type { AgentState } from "../types";

type GlyphStyle = CSSProperties & {
  "--subagent-accent": string;
  "--subagent-secondary": string;
  "--subagent-glyph-size": string;
};

export default function SubagentGlyph({ agent, size = 28, className = "" }: {
  agent: Pick<AgentState, "id" | "type" | "state"> & Partial<Pick<AgentState, "description">>;
  size?: number;
  className?: string;
}) {
  const identity = subagentVisualIdentity(agent);
  const style: GlyphStyle = {
    "--subagent-accent": identity.accent,
    "--subagent-secondary": identity.secondary,
    "--subagent-glyph-size": `${size}px`,
  };
  return (
    <span
      className={`subagent-glyph ${className}`.trim()}
      data-state={agent.state}
      data-symbol={identity.kind}
      style={style}
      aria-hidden="true"
    >
      <svg viewBox="0 0 24 24" focusable="false">
        <RoleMark kind={identity.kind} />
      </svg>
    </span>
  );
}

function RoleMark({ kind }: { kind: SubagentGlyphKind }) {
  if (kind === "architecture") return <>
    <path className="subagent-glyph-structure" d="M7 9.5v2.5h10V9.5M12 12v2.5" />
    <rect className="subagent-glyph-structure" x="3.75" y="3.75" width="6.5" height="5.75" rx="1.4" />
    <rect className="subagent-glyph-secondary" x="13.75" y="3.75" width="6.5" height="5.75" rx="1.4" />
    <rect className="subagent-glyph-structure" x="8.75" y="14.5" width="6.5" height="5.75" rx="1.4" />
    <circle className="subagent-glyph-anchor" cx="12" cy="12" r="1.35" />
  </>;
  if (kind === "security") return <>
    <path className="subagent-glyph-structure" d="M12 3.25 19 6v5.5c0 4.25-2.55 7.2-7 9.1-4.45-1.9-7-4.85-7-9.1V6z" />
    <path className="subagent-glyph-secondary" d="M12 11.8v3.3" />
    <circle className="subagent-glyph-anchor" cx="12" cy="9.7" r="1.65" />
  </>;
  if (kind === "interface") return <>
    <rect className="subagent-glyph-structure" x="3.5" y="4.25" width="17" height="15.5" rx="2.4" />
    <path className="subagent-glyph-secondary" d="M3.75 8.8h16.5M8 13h8M8 16h5" />
    <circle className="subagent-glyph-anchor" cx="6.5" cy="6.55" r="1.05" />
  </>;
  if (kind === "systems") return <>
    <rect className="subagent-glyph-structure" x="3.5" y="4" width="17" height="6" rx="2" />
    <rect className="subagent-glyph-secondary" x="3.5" y="14" width="17" height="6" rx="2" />
    <path className="subagent-glyph-structure" d="M9.5 7h7M9.5 17h7" />
    <circle className="subagent-glyph-anchor" cx="6.6" cy="7" r="1.25" />
    <circle className="subagent-glyph-anchor subagent-glyph-anchor-muted" cx="6.6" cy="17" r="1.25" />
  </>;
  if (kind === "verify") return <>
    <circle className="subagent-glyph-structure" cx="12" cy="12" r="8.25" />
    <path className="subagent-glyph-secondary" d="m7.8 12.2 2.7 2.8 5.9-6.2" />
    <circle className="subagent-glyph-anchor" cx="18.35" cy="6.55" r="1.35" />
  </>;
  if (kind === "plan") return <>
    <path className="subagent-glyph-structure" d="M6 17.5V7h6v10h6V7" />
    <circle className="subagent-glyph-secondary" cx="6" cy="18" r="2.1" />
    <circle className="subagent-glyph-secondary" cx="12" cy="6" r="2.1" />
    <circle className="subagent-glyph-secondary" cx="18" cy="6" r="2.1" />
    <circle className="subagent-glyph-anchor" cx="12" cy="18" r="1.65" />
  </>;
  if (kind === "explore") return <>
    <path className="subagent-glyph-structure" d="M18.4 15.7A7.7 7.7 0 1 1 19 9.9" />
    <path className="subagent-glyph-secondary" d="m13.4 10.6 5.3-5.3-2 6.9-6.9 2z" />
    <circle className="subagent-glyph-anchor" cx="14.25" cy="9.75" r="1.35" />
  </>;
  if (kind === "research") return <>
    <path className="subagent-glyph-structure" d="M3.75 5.25c3.7-.7 6.45.25 8.25 2.3v11.2c-1.8-2.05-4.55-3-8.25-2.3z" />
    <path className="subagent-glyph-secondary" d="M20.25 5.25c-3.7-.7-6.45.25-8.25 2.3v11.2c1.8-2.05 4.55-3 8.25-2.3z" />
    <circle className="subagent-glyph-anchor" cx="12" cy="7.55" r="1.3" />
  </>;
  if (kind === "report") return <>
    <path className="subagent-glyph-structure" d="M6 3.75h8l4 4v12.5H6zM14 3.75v4h4" />
    <path className="subagent-glyph-secondary" d="M9 12h6M9 15.5h4" />
    <circle className="subagent-glyph-anchor" cx="8.2" cy="8.15" r="1.2" />
  </>;
  if (kind === "build") return <>
    <path className="subagent-glyph-structure" d="M8 4H4v16h4M16 4h4v16h-4" />
    <path className="subagent-glyph-secondary" d="m12 6 4.5 6-4.5 6-4.5-6z" />
    <circle className="subagent-glyph-anchor" cx="12" cy="12" r="1.7" />
  </>;
  if (kind === "review") return <>
    <path className="subagent-glyph-structure" d="M12 3.5 20.5 12 12 20.5 3.5 12z" />
    <path className="subagent-glyph-secondary" d="M7.2 12c1.3-1.85 2.9-2.75 4.8-2.75s3.5.9 4.8 2.75c-1.3 1.85-2.9 2.75-4.8 2.75S8.5 13.85 7.2 12Z" />
    <circle className="subagent-glyph-anchor" cx="12" cy="12" r="1.55" />
  </>;
  return <>
    <path className="subagent-glyph-structure" d="m12 3.5 7.35 4.25v8.5L12 20.5l-7.35-4.25v-8.5zM7 9.2l5 2.8 5-2.8M12 12v5.1" />
    <circle className="subagent-glyph-anchor" cx="12" cy="12" r="1.55" />
  </>;
}
