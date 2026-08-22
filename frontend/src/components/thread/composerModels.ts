import { execute } from "../../bridge";
import {
  cursorTiersForMode,
  cursorVariantForSelection,
  findCursorModelGroup,
  groupCursorModelVariants,
  preferredCursorVariant,
  type CursorModelGroup,
} from "../../cursorModelVariants";
import { sortReasoningLevels } from "../../i18n";
import { findModelOption, modelDisplayName, useRuntimeStore, type ModelOption } from "../../store";
import type { ModelRoute, Snapshot } from "../../types";

export function supportsFastMode(provider: string, capabilities: readonly string[] = []): boolean {
  return provider.trim().toLocaleLowerCase() === "chatgpt"
    && capabilities.some((capability) => capability.trim().toLocaleLowerCase() === "fast");
}

type CursorComposerSource = ModelOption & { provider: string };

export type ComposerModel = {
  provider: string;
  id: string;
  name: string;
  aliases?: string[];
  reasoningLevels: string[];
  defaultReasoning?: string;
  capabilities?: string[];
  cursorGroup?: CursorModelGroup<CursorComposerSource>;
  cursorVariantCount?: number;
  cursorTierCount?: number;
  cursorHasFast?: boolean;
  cursorNoZDR?: boolean;
};

export type ComposerRoute = { provider: string; model: string; reasoning: string };

export function effectiveComposerRoute(snapshot: Snapshot, planMode: boolean, modelRoutes: ModelRoute[]): ComposerRoute {
  const plan = planMode ? modelRoutes.find((route) => route.scope === "plan" && !route.role)?.route : undefined;
  return {
    provider: plan?.provider?.trim() || snapshot.provider,
    model: plan?.model?.trim() || snapshot.model,
    reasoning: plan?.reasoning?.trim() || snapshot.reasoning,
  };
}

export function composerModelChoices(modelsByProvider: Record<string, ModelOption[]>, activeRoute: ComposerRoute): ComposerModel[] {
  return Object.entries(modelsByProvider).flatMap(([provider, models]) => {
    const enabled = models.filter((model) => !model.disabled).map((model) => ({ ...model, provider }));
    if (provider !== "cursor") return enabled;
    const groups = groupCursorModelVariants(enabled);
    const activeGroup = activeRoute.provider === provider ? findCursorModelGroup(groups, activeRoute.model) : undefined;
    const activeVariant = activeGroup?.variants.find((variant) => variant.model.id === activeRoute.model);
    return groups.map((group) => {
      const selectedID = activeGroup?.id === group.id ? activeRoute.model : "";
      const selectedFast = activeVariant?.fast ?? false;
      const defaultThinking = cursorTiersForMode(group, true, selectedFast).length > 0;
      const selected = preferredCursorVariant(
        group,
        activeVariant?.tier || activeRoute.reasoning,
        defaultThinking,
        selectedFast,
        selectedID,
      );
      return {
        ...selected.model,
        id: selected.model.id,
        name: group.name,
        aliases: [...new Set(group.variants.flatMap((variant) => [variant.model.id, ...(variant.model.aliases ?? [])]))],
        reasoningLevels: sortReasoningLevels(cursorTiersForMode(group, selected.thinking, selected.fast)),
        defaultReasoning: selected.tier,
        cursorGroup: group,
        cursorVariantCount: group.variants.length,
        cursorTierCount: new Set(group.variants.map((variant) => variant.tier)).size,
        cursorHasFast: group.variants.some((variant) => variant.fast),
        cursorNoZDR: selected.noZDR,
      };
    });
  });
}

export function useComposerModels(snapshot: Snapshot, activeRoute: ComposerRoute, routeScope: "" | "plan") {
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setSessionModel = useRuntimeStore((state) => state.setSessionModel);
  const setChatGPTFastMode = useRuntimeStore((state) => state.setChatGPTFastMode);
  const setError = useRuntimeStore((state) => state.setError);
  const providerModels = modelsByProvider[activeRoute.provider] ?? [];
  const catalogModel = findModelOption(providerModels, activeRoute.model);
  const fallbackModel: ComposerModel = {
    provider: activeRoute.provider,
    id: activeRoute.model,
    name: modelDisplayName(activeRoute.model),
    aliases: [],
    reasoningLevels: [activeRoute.reasoning].filter(Boolean),
  };
  const catalogModels = composerModelChoices(modelsByProvider, activeRoute);
  const modelChoices = [...new Map([
    ...(catalogModel ? [] : [fallbackModel]),
    ...catalogModels,
  ].map((model) => [modelKey(model.provider, model.id), model])).values()];
  const cursorGroups = activeRoute.provider === "cursor"
    ? groupCursorModelVariants(providerModels.filter((model) => !model.disabled).map((model) => ({ ...model, provider: "cursor" })))
    : [];
  const activeCursorGroup = findCursorModelGroup(cursorGroups, activeRoute.model);
  const activeCursorVariant = activeCursorGroup?.variants.find((variant) => variant.model.id === activeRoute.model);
  const cursorFast = activeCursorVariant?.fast ?? false;
  const cursorThinking = cursorTiersForMode(activeCursorGroup, true, cursorFast).length > 0;
  const selectedReasoning = activeCursorVariant?.tier || activeRoute.reasoning;
  const catalogLevels = activeCursorVariant
    ? cursorTiersForMode(activeCursorGroup, cursorThinking, cursorFast)
    : (catalogModel?.reasoningLevels ?? []);
  // Keep Codex order (轻度→最高); never pin the current value to the top of the list.
  const reasoningLevels = sortReasoningLevels(catalogLevels.length > 0 ? catalogLevels : [selectedReasoning].filter(Boolean));
  const selectedModel = modelKey(activeRoute.provider, catalogModel?.id ?? activeRoute.model);
  const selectedModelName = activeCursorGroup?.name ?? catalogModel?.name ?? modelDisplayName(activeRoute.model);
  const cursorFastAvailable = Boolean(activeCursorVariant && cursorVariantForSelection(
    activeCursorGroup, selectedReasoning, cursorThinking, !cursorFast,
  ));
  const fastAvailable = activeRoute.provider === "cursor"
    ? cursorFastAvailable
    : supportsFastMode(activeRoute.provider, catalogModel?.capabilities);
  const fast = activeRoute.provider === "cursor" ? cursorFast : snapshot.chatgptFastMode;
  const persistSessionPreferences = (
    provider: string,
    model: string,
    reasoning: string,
    previous: { provider: string; model: string; reasoning: string },
  ) => {
    execute({
      kind: "set_session_preferences",
      sessionId: currentSessionId,
      route: { scope: "session", role: "", label: "", route: { provider, model, reasoning } },
    }).catch((cause) => {
      setSessionModel(previous.provider, previous.model, previous.reasoning);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };
  const persistPlanRoute = (provider: string, model: string, reasoning: string) => {
    execute({
      kind: "set_model_route",
      sessionId: currentSessionId,
      route: { scope: "plan", role: "", label: "Plan", route: { provider, model, reasoning } },
    }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  };
  const changeRoute = (provider: string, model: string, reasoning: string) => {
    if (routeScope === "plan") {
      persistPlanRoute(provider, model, reasoning);
      return;
    }
    const previous = { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning };
    setSessionModel(provider, model, reasoning);
    persistSessionPreferences(provider, model, reasoning, previous);
  };
  const changeModel = (value: string) => {
    const choice = modelChoices.find((model) => modelKey(model.provider, model.id) === value);
    if (!choice) return;
    if (choice.cursorGroup) {
      const variant = preferredCursorVariant(
        choice.cursorGroup, selectedReasoning, cursorThinking, cursorFast,
      );
      changeRoute(choice.provider, variant.model.id, variant.tier);
      return;
    }
    const reasoning = [choice.defaultReasoning, ...choice.reasoningLevels, selectedReasoning].filter(Boolean)[0]!;
    changeRoute(choice.provider, choice.id, reasoning);
  };
  const changeReasoning = (reasoning: string) => {
    if (activeCursorVariant) {
      const variant = cursorVariantForSelection(activeCursorGroup, reasoning, cursorThinking, cursorFast);
      if (variant) {
        changeRoute(activeRoute.provider, variant.model.id, variant.tier);
      } else {
        setError(snapshot.language === "zh-CN" ? "当前 Cursor 模式不支持这个思考档位" : "This Cursor mode does not support that reasoning tier");
      }
      return;
    }
    changeRoute(activeRoute.provider, activeRoute.model, reasoning);
  };
  const changeSpeed = (speed: string) => {
    const enabled = speed === "fast";
    if (activeCursorVariant) {
      const variant = cursorVariantForSelection(activeCursorGroup, selectedReasoning, cursorThinking, enabled);
      if (variant) {
        changeRoute(activeRoute.provider, variant.model.id, variant.tier);
      } else {
        setError(snapshot.language === "zh-CN" ? "当前档位没有对应的 Cursor Fast 变体" : "This tier has no matching Cursor Fast variant");
      }
      return;
    }
    const previous = snapshot.chatgptFastMode;
    setChatGPTFastMode(enabled);
    execute({ kind: "set_chatgpt_fast_mode", target: String(enabled) }).catch((cause) => {
      setChatGPTFastMode(previous);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };
  return {
    modelChoices,
    reasoningLevels,
    selectedModel,
    selectedModelName,
    selectedReasoning,
    fast,
    fastAvailable,
    changeModel,
    changeReasoning,
    changeSpeed,
  };
}

export function modelKey(provider: string, model: string) { return `${provider}/${model}`; }
