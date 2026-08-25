import type { ContextUsage } from "./store";
import { translator, type Language } from "./i18n";
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

export function contextCategoryLabel(category: string, language: Language) {
  const t = translator(language);
  return ({
    core: t("contextCore"),
    conversation: t("contextConversation"),
    builtin_tools: t("contextBuiltinTools"),
    skills: t("contextSkills"),
    mcp: t("contextMCP"),
    current_output: t("contextCurrentOutput"),
    provider_input: t("contextProviderInput"),
    other: t("contextOther"),
  } as Record<string, string>)[category] ?? category.replaceAll("_", " ");
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
