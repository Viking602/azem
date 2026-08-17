import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { formatRelativeTime, nextRelativeTimeDelay } from "./relativeTime";

describe("relative time", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-14T12:00:00.000Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("uses minute granularity instead of collapsing a whole hour to just now", () => {
    expect(formatRelativeTime(new Date(Date.now() - 20_000).toISOString(), "zh-CN")).toBe("刚刚");
    expect(formatRelativeTime(new Date(Date.now() - 5 * 60_000).toISOString(), "zh-CN")).toBe("5 分钟前");
    expect(formatRelativeTime(new Date(Date.now() - 3 * 60 * 60_000).toISOString(), "zh-CN")).toBe("3 小时前");
    expect(formatRelativeTime(new Date(Date.now() - 2 * 24 * 60 * 60_000).toISOString(), "en")).toBe("2d ago");
  });

  it("schedules the next tick at the next label boundary", () => {
    const now = Date.now();
    expect(nextRelativeTimeDelay([now - 10_000], now)).toBe(50_000);
    expect(nextRelativeTimeDelay([now - 5 * 60_000 - 1_000], now)).toBe(59_000);
  });
});
