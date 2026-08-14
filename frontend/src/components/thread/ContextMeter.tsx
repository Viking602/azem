import { useEffect, useRef } from "react";
import { contextOccupancy } from "../../contextUsage";
import { translator } from "../../i18n";
import { useRuntimeStore } from "../../store";

export function ContextMeter() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const usage = useRuntimeStore((state) => state.contextUsage);
  const profile = useRuntimeStore((state) => state.contextProfile);
  const details = useRef<HTMLDetailsElement>(null);
  const t = translator(snapshot.language);
  const metrics = contextOccupancy(usage, profile);
  // Blue by default; only turn red when nearly full.
  const tone = metrics.percentage >= 90 ? "critical" : "normal";
  useEffect(() => {
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (details.current && !details.current.contains(event.target as Node)) details.current.open = false;
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer, true);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePointer, true);
  }, []);
  const label = metrics.limit > 0 ? `${t("contextUsage")} ${metrics.percentage}%` : t("contextUnavailable");
  return <details ref={details} className="context-meter" data-tone={tone}>
    <summary title={label} aria-label={label}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><circle className="context-ring-track" cx="10" cy="10" r="7.5" pathLength="100" /><circle className="context-ring-value" cx="10" cy="10" r="7.5" pathLength="100" strokeDasharray={`${metrics.percentage} 100`} /></svg>
    </summary>
    <div className="context-meter-popover">
      <header><strong>{t("contextUsage")}</strong><span>{metrics.limit > 0 ? `${metrics.estimated ? "~" : ""}${metrics.percentage}%` : "—"}</span></header>
      <div className="context-progress" role="progressbar" aria-label={t("contextUsage")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={metrics.percentage}><span style={{ width: `${metrics.percentage}%` }} /></div>
      <footer>{metrics.limit > 0 ? <><span>{metrics.estimated ? "~" : ""}{formatTokens(metrics.used)} / {formatTokens(metrics.limit)}</span><span>{t("contextRemaining")} {formatTokens(metrics.remaining)}</span></> : <span>{t("contextUnavailable")}</span>}</footer>
    </div>
  </details>;
}

function formatTokens(tokens: number) {
  if (tokens >= 1_000_000) return `${Number((tokens / 1_000_000).toFixed(tokens < 10_000_000 ? 1 : 0))}M`;
  if (tokens >= 1_000) return `${Number((tokens / 1_000).toFixed(tokens < 10_000 ? 1 : 0))}K`;
  return String(tokens);
}
