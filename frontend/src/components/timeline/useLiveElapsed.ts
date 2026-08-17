import { useEffect, useRef, useState } from "react";

/** Keep a server-reported elapsed duration moving locally while work is active. */
export function useLiveElapsed(elapsedMs: number, active: boolean, intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now());
  const anchor = useRef({ elapsedMs: Math.max(0, elapsedMs), observedAt: now, active });

  useEffect(() => {
    const observedAt = Date.now();
    const current = anchor.current;
    const projected = current.active
      ? current.elapsedMs + Math.max(0, observedAt - current.observedAt)
      : current.elapsedMs;
    anchor.current = {
      elapsedMs: active ? Math.max(0, elapsedMs, projected) : Math.max(0, elapsedMs),
      observedAt,
      active,
    };
    setNow(observedAt);
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), Math.max(16, intervalMs));
    return () => window.clearInterval(timer);
  }, [active, elapsedMs, intervalMs]);

  const current = anchor.current;
  const projected = active && current.active
    ? current.elapsedMs + Math.max(0, now - current.observedAt)
    : elapsedMs;
  return Math.max(0, active ? Math.max(elapsedMs, projected) : elapsedMs);
}
