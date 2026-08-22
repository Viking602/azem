export type CursorVariantModel = {
  id: string;
  name?: string;
};

export type CursorVariantTier = "default" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";

export type CursorModelVariant<T extends CursorVariantModel = CursorVariantModel> = {
  model: T;
  sourceIndex: number;
  tier: CursorVariantTier;
  thinking: boolean;
  fast: boolean;
  noZDR: boolean;
};

export type CursorModelGroup<T extends CursorVariantModel = CursorVariantModel> = {
  id: string;
  name: string;
  variants: CursorModelVariant<T>[];
};


const tierOrder: Record<CursorVariantTier, number> = {
  default: 0,
  none: 1,
  minimal: 2,
  low: 3,
  medium: 4,
  high: 5,
  xhigh: 6,
  max: 7,
};

const tierSuffixes: Record<string, true> = { none: true, minimal: true, low: true, medium: true, high: true, xhigh: true, max: true };

const variantLabels: Record<CursorVariantTier, [string, string]> = {
  default: ["默认", "Default"],
  none: ["无思考", "No reasoning"],
  minimal: ["极低", "Minimal"],
  low: ["低", "Low"],
  medium: ["中", "Medium"],
  high: ["高", "High"],
  xhigh: ["极高", "Extra high"],
  max: ["Max", "Max"],
};

export function groupCursorModelVariants<T extends CursorVariantModel>(models: readonly T[]): CursorModelGroup<T>[] {
  const groups: CursorModelGroup<T>[] = [];
  const byID = new Map<string, CursorModelGroup<T>>();
  models.forEach((model, sourceIndex) => {
    const parsed = parseCursorVariantID(model.id);
    let group = byID.get(parsed.baseID);
    if (!group) {
      group = { id: parsed.baseID, name: "", variants: [] };
      byID.set(parsed.baseID, group);
      groups.push(group);
    }
    group.variants.push({ model, sourceIndex, tier: parsed.tier, thinking: parsed.thinking, fast: parsed.fast, noZDR: /\(NO ZDR\)/iu.test(model.name || "") });
  });
  for (const group of groups) {
    group.variants.sort((left, right) =>
      tierOrder[left.tier]-tierOrder[right.tier]
      || Number(left.thinking)-Number(right.thinking)
      || Number(left.fast)-Number(right.fast)
      || left.model.id.localeCompare(right.model.id));
    const standard = group.variants.filter((variant) => !variant.thinking && !variant.fast);
    const nameVariants = standard.length > 0 ? standard : group.variants;
    const names = [...new Set(nameVariants.map((variant) => cursorVariantBaseName(variant.model.name || variant.model.id)).filter(Boolean))]
      .sort((left, right) => left.length-right.length || left.localeCompare(right));
    group.name = names[0] || group.variants[0]?.model.name || group.id;
  }
  return groups;
}

export function findCursorModelGroup<T extends CursorVariantModel>(
  groups: readonly CursorModelGroup<T>[],
  modelID: string,
): CursorModelGroup<T> | undefined {
  return groups.find((group) => group.variants.some((variant) => variant.model.id === modelID));
}

export function cursorVariantForSelection<T extends CursorVariantModel>(
  group: CursorModelGroup<T> | undefined,
  tier: string,
  thinking: boolean,
  fast: boolean,
): CursorModelVariant<T> | undefined {
  return group?.variants.find((variant) =>
    variant.tier === tier && variant.thinking === thinking && variant.fast === fast);
}

export function cursorTiersForMode<T extends CursorVariantModel>(
  group: CursorModelGroup<T> | undefined,
  thinking: boolean,
  fast: boolean,
): CursorVariantTier[] {
  return [...new Set((group?.variants ?? [])
    .filter((variant) => variant.thinking === thinking && variant.fast === fast)
    .map((variant) => variant.tier))];
}

export function preferredCursorVariant<T extends CursorVariantModel>(
  group: CursorModelGroup<T>,
  requestedTier = "",
  thinking = true,
  fast = false,
  selectedModelID = "",
): CursorModelVariant<T> {
  return group.variants.find((variant) => variant.model.id === selectedModelID)
    ?? cursorVariantForSelection(group, requestedTier, thinking, fast)
    ?? cursorVariantForSelection(group, requestedTier, false, false)
    ?? group.variants.find((variant) => variant.tier === requestedTier)
    ?? cursorVariantForSelection(group, "default", thinking, fast)
    ?? cursorVariantForSelection(group, "default", false, false)
    ?? cursorVariantForSelection(group, "medium", false, false)
    ?? cursorVariantForSelection(group, "high", false, false)
    ?? group.variants[0]!;
}

export function cursorVariantTier(modelID: string): CursorVariantTier {
  return parseCursorVariantID(modelID).tier;
}

export function cursorVariantLabel(variant: CursorModelVariant, language: "en" | "zh-CN"): string {
  const parts = [variantLabels[variant.tier][language === "zh-CN" ? 0 : 1]];
  if (variant.thinking) parts.push("Thinking");
  if (variant.fast) parts.push("Fast");
  return parts.join(" · ");
}

function parseCursorVariantID(id: string): { baseID: string; tier: CursorVariantTier; thinking: boolean; fast: boolean } {
  const parts = id.trim().toLocaleLowerCase().split("-").filter(Boolean);
  let fast = false;
  let thinking = false;
  let tier: CursorVariantTier = "default";
  if (parts.at(-1) === "fast") {
    fast = true;
    parts.pop();
  }
  if (parts.at(-1) === "thinking") {
    thinking = true;
    parts.pop();
  }
  if (parts.length >= 2 && parts.at(-2) === "extra" && parts.at(-1) === "high") {
    tier = "xhigh";
    parts.splice(-2, 2);
  } else if (tierSuffixes[parts.at(-1) ?? ""]) {
    tier = parts.pop() as CursorVariantTier;
  }
  if (parts.at(-1) === "thinking") {
    thinking = true;
    parts.pop();
  }
  return { baseID: parts.join("-") || id, tier, thinking, fast };
}

function cursorVariantBaseName(value: string): string {
  let name = value.replace(/\s*\(NO ZDR\)\s*/giu, " ").trim();
  for (let pass = 0; pass < 4; pass++) {
    const previous = name;
    name = name
      .replace(/\s+Fast$/iu, "")
      .replace(/\s+Thinking$/iu, "")
      .replace(/\s+(?:Extra High|High|Medium|Low|Max|Minimal|None)$/iu, "")
      .trim();
    if (name === previous) break;
  }
  return name;
}
