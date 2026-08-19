import type { ContextUsage } from "./store";
import type { ContextProfile } from "./types";

export interface ContextCompositionItem {
  name: string;
  tokens: number;
}

export interface ContextCompositionGroup {
  category: string;
  tokens: number;
  percentage: number;
  items: ContextCompositionItem[];
}

export function contextOccupancy(usage: ContextUsage, profile: ContextProfile | null) {
  let used = (profile?.contributions ?? []).reduce((total, item) => total + Math.max(0, item.tokens), 0) + Math.max(0, usage.outputTokens);
  let estimated = Boolean(profile?.estimated);
  if ((profile?.reportedInputTokens ?? 0) > 0) {
    used = Math.max(0, profile!.reportedInputTokens!) + Math.max(0, profile!.reportedOutputTokens ?? 0);
    estimated = false;
  } else if (usage.inputTokens > 0) {
    used = Math.max(0, usage.inputTokens) + Math.max(0, usage.outputTokens);
    estimated = !usage.reported;
  }
  const limit = Math.max(0, usage.contextLimit);
  const percentage = limit > 0 ? Math.min(100, Math.round(used * 100 / limit)) : 0;
  return { used, limit, percentage, remaining: Math.max(0, limit - used), estimated };
}

export function contextCacheMetrics(usage: ContextUsage) {
  const reported = usage.mainCacheReported === true && usage.uncachedInputTokens !== undefined;
  const requestInputTokens = Math.max(0, usage.inputTokens);
  const cachedTokens = reported
    ? Math.max(0, requestInputTokens - Math.min(requestInputTokens, Math.max(0, usage.uncachedInputTokens ?? 0)))
    : 0;
  return {
    reported,
    hitRate: reported && requestInputTokens > 0 ? cacheHitPercentage(cachedTokens, requestInputTokens) : null,
    cachedTokens,
    totalCacheTokens: requestInputTokens,
  };
}

function cacheHitPercentage(cachedTokens: number, inputTokens: number) {
  return Math.min(100, Math.trunc(cachedTokens * 10_000 / inputTokens) / 100);
}

export function contextComposition(usage: ContextUsage, profile: ContextProfile | null) {
  const grouped = new Map<string, ContextCompositionItem[]>();
  const append = (category: string, item: ContextCompositionItem) => {
    if (item.tokens <= 0) return;
    grouped.set(category, [...(grouped.get(category) ?? []), item]);
  };
  for (const contribution of profile?.contributions ?? []) {
    append(contribution.category || "other", { name: contribution.name, tokens: Math.max(0, contribution.tokens) });
  }
  if (grouped.size === 0 && usage.inputTokens > 0) {
    append("provider_input", { name: "provider_input", tokens: Math.max(0, usage.inputTokens) });
  }
  append("current_output", { name: "current_output", tokens: Math.max(0, usage.outputTokens) });

  const totals = [...grouped.entries()].map(([category, items]) => ({
    category,
    items: [...items].sort((left, right) => right.tokens - left.tokens || left.name.localeCompare(right.name)),
    tokens: items.reduce((total, item) => total + item.tokens, 0),
  })).sort((left, right) => right.tokens - left.tokens || left.category.localeCompare(right.category));
  const totalTokens = totals.reduce((total, group) => total + group.tokens, 0);
  const groups: ContextCompositionGroup[] = totals.map((group) => ({
    ...group,
    percentage: totalTokens > 0 ? Math.round(group.tokens * 100 / totalTokens) : 0,
  }));
  return { groups, totalTokens, estimated: Boolean(profile?.estimated) || !usage.reported };
}
