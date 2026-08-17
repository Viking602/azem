import { useEffect, useState } from "react";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

export function formatRelativeTime(value: string, language: "en" | "zh-CN", now = Date.now()): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return language === "zh-CN" ? "最近" : "Recently";
  const age = Math.max(0, now - timestamp);
  if (age < MINUTE) return language === "zh-CN" ? "刚刚" : "Just now";
  if (age < HOUR) {
    const minutes = Math.floor(age / MINUTE);
    return language === "zh-CN" ? `${minutes} 分钟前` : `${minutes}m ago`;
  }
  if (age < DAY) {
    const hours = Math.floor(age / HOUR);
    return language === "zh-CN" ? `${hours} 小时前` : `${hours}h ago`;
  }
  const days = Math.floor(age / DAY);
  if (days < 30) return language === "zh-CN" ? `${days} 天前` : `${days}d ago`;
  const date = new Date(timestamp);
  return language === "zh-CN"
    ? `${date.getMonth() + 1} 月 ${date.getDate()} 日`
    : date.toLocaleDateString("en", { month: "short", day: "numeric" });
}

export function nextRelativeTimeDelay(timestamps: number[], now = Date.now()): number {
  let delay = HOUR;
  for (const timestamp of timestamps) {
    if (!Number.isFinite(timestamp)) continue;
    const age = Math.max(0, now - timestamp);
    const next = age < MINUTE
      ? MINUTE - age
      : age < HOUR
        ? MINUTE - (age % MINUTE)
        : age < DAY
          ? HOUR - (age % HOUR)
          : DAY - (age % DAY);
    if (next < delay) delay = next;
  }
  return Math.max(1_000, Math.min(delay, HOUR));
}

export function useRelativeNow(updatedAts: readonly string[]): number {
  const [now, setNow] = useState(() => Date.now());
  const key = updatedAts.filter(Boolean).slice().sort().join("\n");
  useEffect(() => {
    let timer = 0;
    const timestamps = key ? key.split("\n").map((value) => Date.parse(value)) : [];
    const clear = () => {
      if (timer) window.clearTimeout(timer);
      timer = 0;
    };
    const arm = (origin: number) => {
      clear();
      timer = window.setTimeout(() => {
        const current = Date.now();
        setNow(current);
        arm(current);
      }, nextRelativeTimeDelay(timestamps, origin));
    };
    const onVisibility = () => {
      if (document.hidden) {
        clear();
        return;
      }
      const current = Date.now();
      setNow(current);
      arm(current);
    };
    if (!document.hidden) arm(Date.now());
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      clear();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [key]);
  return now;
}
