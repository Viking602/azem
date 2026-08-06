import type { CSSProperties } from "react";
import { subagentVisualIdentity } from "../subagents";
import type { AgentState } from "../types";

type GlyphStyle = CSSProperties & {
  "--subagent-accent": string;
  "--subagent-secondary": string;
  "--subagent-glyph-size": string;
};

export default function SubagentGlyph({ agent, size = 28, className = "" }: {
  agent: Pick<AgentState, "id" | "type" | "state">;
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
      data-variant={identity.variant}
      style={style}
      aria-hidden="true"
    >
      <i /><i /><i /><i />
    </span>
  );
}
