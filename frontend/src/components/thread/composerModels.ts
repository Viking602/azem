import { execute } from "../../bridge";
import { sortReasoningLevels } from "../../i18n";
import { findModelOption, modelDisplayName, useRuntimeStore } from "../../store";
import type { ModelRoute, Snapshot } from "../../types";

export function supportsFastMode(provider: string, capabilities: readonly string[] = []): boolean {
  return provider.trim().toLocaleLowerCase() === "chatgpt"
    && capabilities.some((capability) => capability.trim().toLocaleLowerCase() === "fast");
}

export type ComposerModel = { provider: string; id: string; name: string; aliases?: string[]; reasoningLevels: string[]; defaultReasoning?: string; capabilities?: string[] };

export type ComposerRoute = { provider: string; model: string; reasoning: string };

export function effectiveComposerRoute(snapshot: Snapshot, planMode: boolean, modelRoutes: ModelRoute[]): ComposerRoute {
  const plan = planMode ? modelRoutes.find((route) => route.scope === "plan" && !route.role)?.route : undefined;
  return {
    provider: plan?.provider?.trim() || snapshot.provider,
    model: plan?.model?.trim() || snapshot.model,
    reasoning: plan?.reasoning?.trim() || snapshot.reasoning,
  };
}

export function useComposerModels(snapshot: Snapshot, activeRoute: ComposerRoute, routeScope: "" | "plan") {
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setSessionModel = useRuntimeStore((state) => state.setSessionModel);
  const setChatGPTFastMode = useRuntimeStore((state) => state.setChatGPTFastMode);
  const setError = useRuntimeStore((state) => state.setError);
  const providerModels = modelsByProvider[activeRoute.provider] ?? [];
  const catalogModel = findModelOption(providerModels, activeRoute.model);
  const fallbackModel: ComposerModel = { provider: activeRoute.provider, id: activeRoute.model, name: modelDisplayName(activeRoute.model), aliases: [], reasoningLevels: [activeRoute.reasoning].filter(Boolean) };
  const catalogModels = Object.entries(modelsByProvider).flatMap(([provider, models]) => models.filter((model) => !model.disabled).map((model) => ({ ...model, provider })));
  const modelChoices = [...new Map([...(catalogModel ? [] : [fallbackModel]), ...catalogModels].map((model) => [modelKey(model.provider, model.id), model])).values()];
  const catalogLevels = catalogModel?.reasoningLevels ?? [];
  // Keep Codex order (轻度→最高); never pin the current value to the top of the list.
  const reasoningLevels = sortReasoningLevels(catalogLevels.length > 0 ? catalogLevels : [activeRoute.reasoning].filter(Boolean));
  const selectedModel = modelKey(activeRoute.provider, catalogModel?.id ?? activeRoute.model);
  const selectedModelName = catalogModel?.name ?? modelDisplayName(activeRoute.model);
  const fastAvailable = supportsFastMode(activeRoute.provider, catalogModel?.capabilities);
  const persistSessionPreferences = (provider: string, model: string, reasoning: string, previous: { provider: string; model: string; reasoning: string }) => {
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
  const changeModel = (value: string) => {
    const choice = modelChoices.find((model) => modelKey(model.provider, model.id) === value)!;
    const reasoning = [choice.defaultReasoning, ...choice.reasoningLevels, activeRoute.reasoning].filter(Boolean)[0]!;
    if (routeScope === "plan") {
      persistPlanRoute(choice.provider, choice.id, reasoning);
      return;
    }
    const previous = { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning };
    setSessionModel(choice.provider, choice.id, reasoning);
    persistSessionPreferences(choice.provider, choice.id, reasoning, previous);
  };
  const changeReasoning = (reasoning: string) => {
    if (routeScope === "plan") {
      persistPlanRoute(activeRoute.provider, activeRoute.model, reasoning);
      return;
    }
    const previous = { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning };
    setSessionModel(snapshot.provider, snapshot.model, reasoning);
    persistSessionPreferences(snapshot.provider, snapshot.model, reasoning, previous);
  };
  const changeSpeed = (speed: string) => {
    const enabled = speed === "fast";
    const previous = snapshot.chatgptFastMode;
    setChatGPTFastMode(enabled);
    execute({ kind: "set_chatgpt_fast_mode", target: String(enabled) }).catch((cause) => {
      setChatGPTFastMode(previous);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };
  return { modelChoices, reasoningLevels, selectedModel, selectedModelName, fastAvailable, changeModel, changeReasoning, changeSpeed };
}

export function modelKey(provider: string, model: string) { return `${provider}/${model}`; }
