import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
	AudioLines, Braces, Brain, Image as ImageIcon, Layers, MessageSquareText,
	RefreshCw, Search, ShieldAlert, Type, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { groupCursorModelVariants, type CursorModelGroup } from "../cursorModelVariants";
import { tFormat, translator, type Language, type MessageKey } from "../i18n";
import { formatRelativeTime, useRelativeNow } from "../relativeTime";
import { modelDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { LLMuxModelConfig, ModelProvider, ModelRoute } from "../types";
import ProviderIcon from "./ProviderIcon";

const PROVIDER_PAGE_SIZE = 24;

export default function ModelProviderSettings({ providers, modelsByProvider, sessionId, language, setError, addRequest = 0, selectedProviderID = "" }: { providers: ModelProvider[]; modelsByProvider: Record<string, ModelOption[]>; sessionId: string; language: Language; setError: (message: string) => void; addRequest?: number; selectedProviderID?: string }) {
	const t = translator(language);
	const [query, setQuery] = useState("");
	const [visibleCount, setVisibleCount] = useState(PROVIDER_PAGE_SIZE);
	const [loading, setLoading] = useState(providers.length === 0);
	const [loadError, setLoadError] = useState("");
	const configured = providers.filter((provider) => provider.enabled || provider.models.length > 0);
	const filtered = providers.filter((provider) => `${provider.displayName} ${provider.id}`.toLowerCase().includes(query.trim().toLowerCase()));
	const visible = query.trim() ? filtered : [...configured, ...providers.filter((provider) => !configured.includes(provider)).slice(0, visibleCount)];
	const [selectedID, setSelectedID] = useState("");
	const [customProvider, setCustomProvider] = useState<ModelProvider | null>(null);
	const selected = selectedID === "__custom__" ? customProvider : providers.find((provider) => provider.id === selectedID) ?? configured[0] ?? providers[0];
	useEffect(() => setVisibleCount(PROVIDER_PAGE_SIZE), [providers.length, query]);
	useEffect(() => {
		if (providers.length > 0) {
			setLoading(false);
			setLoadError("");
		}
	}, [providers.length]);
	useEffect(() => {
		if (!selectedID && selected) setSelectedID(selected.id);
	}, [selected, selectedID]);
	useEffect(() => {
		if (selectedProviderID && providers.some((provider) => provider.id === selectedProviderID)) {
			setSelectedID(selectedProviderID);
		}
	}, [providers, selectedProviderID]);
	useEffect(() => {
		if (addRequest <= 0) return;
		setCustomProvider(blankProvider(language));
		setSelectedID("__custom__");
	}, [addRequest, language]);
	const loadMore = (element: HTMLDivElement) => {
		if (!query.trim() && visible.length < providers.length && element.scrollHeight-element.scrollTop-element.clientHeight < 80) {
			setVisibleCount((count) => Math.min(count + PROVIDER_PAGE_SIZE, providers.length));
		}
	};
	const refresh = async () => {
		const waitingForCatalog = providers.length === 0;
		if (waitingForCatalog) setLoading(true);
		setLoadError("");
		try {
			await execute({ kind: "list_models", sessionId });
			await execute({ kind: "list_model_providers", sessionId });
			// Event delivery is async; clear a stuck spinner if the catalog never lands.
			if (waitingForCatalog) window.setTimeout(() => setLoading(false), 1500);
		} catch (cause) {
			const message = (cause instanceof Error ? cause.message : String(cause)).trim() || t("providersLoadFailed");
			setLoadError(message);
			setLoading(false);
			setError(message);
		}
	};
	useEffect(() => {
		if (providers.length > 0) return;
		void refresh();
		// Catalog pane reloads once when opened with an empty store.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [sessionId]);
	const emptyMessage = loadError || (loading ? t("loadingProviders") : t("noProviders"));
	return <section className="settings-pane" data-setting-id={selected ? `provider:${selected.id}` : undefined}><header data-setting-id="section:catalog"><div><h2>{t("modelSettings")}</h2><p>{t("modelSettingsHint")}</p></div><button className="small-button" disabled={loading} onClick={() => void refresh()}><RefreshCw size={13} className={loading ? "spin" : undefined} />{loading ? t("loadingProviders") : t("refresh")}</button></header>
		{loadError && <div className="settings-inline-error" role="alert">{loadError}</div>}
		<div className="provider-settings">
			<div className="provider-directory settings-card">
				<label className="provider-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={language === "zh-CN" ? "搜索供应商" : "Search providers"} disabled={loading && providers.length === 0} /></label>
				<div className="provider-list" onScroll={(event) => loadMore(event.currentTarget)}>
					{visible.map((provider) => <button key={provider.id} data-setting-id={`provider:${provider.id}`} className={selected?.id === provider.id ? "active" : ""} onClick={() => setSelectedID(provider.id)}><span className="provider-identity"><ProviderIcon provider={provider.id} logoID={provider.modelsDevId} /><span><strong>{provider.displayName}</strong><small>{providerDirectoryDetail(provider, language)}</small></span></span><em className={`${provider.enabled ? "enabled" : ""} ${provider.quotaAvailable ? "quota" : ""}`}>{providerDirectoryStatus(provider, language)}</em></button>)}
					{providers.length === 0 && <div className="settings-empty provider-list-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
					<button className={selectedID === "__custom__" ? "active custom-provider-entry" : "custom-provider-entry"} onClick={() => { setCustomProvider(blankProvider(language)); setSelectedID("__custom__"); }}><span className="provider-identity"><span className="provider-add-icon">+</span><span><strong>{language === "zh-CN" ? "自定义提供方" : "Custom provider"}</strong><small>{language === "zh-CN" ? "OpenAI 兼容 API" : "OpenAI compatible API"}</small></span></span><em>{language === "zh-CN" ? "未启用" : "Disabled"}</em></button>
				</div>
				{!query.trim() && providers.length > visible.length && <small className="provider-more" aria-live="polite">{tFormat(language, "moreProviders", { count: providers.length - visible.length })}</small>}
			</div>
			{selected ? selected.subscription ? <SubscriptionProviderEditor key={selected.id} provider={selected} models={modelsByProvider[selected.id] ?? []} sessionId={sessionId} language={language} setError={setError} /> : <ProviderEditor key={selectedID === "__custom__" ? `custom-${addRequest}` : selected.id} provider={selected} sessionId={sessionId} language={language} setError={setError} creating={selectedID === "__custom__"} /> : <div className="settings-card settings-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
		</div>
	</section>;
}

function SubscriptionProviderEditor({ provider, models, sessionId, language, setError }: { provider: ModelProvider; models: ModelOption[]; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [working, setWorking] = useState(false);
	const [workingModel, setWorkingModel] = useState("");
	const [discovering, setDiscovering] = useState(false);
	const now = useRelativeNow(provider.quotaUpdatedAt ? [provider.quotaUpdatedAt] : []);
	const plan = formatSubscriptionTier(provider.id, provider.accountPlan || "") || (language === "zh-CN" ? "订阅计划未报告" : "Plan not reported");
	const account = provider.accountLabel || provider.accountId || t("subscriptionLoginRequired");
	const updatedTime = provider.quotaUpdatedAt ? formatRelativeTime(provider.quotaUpdatedAt, language, now) : "";
	const compactUpdatedTime = language === "zh-CN" ? updatedTime.replaceAll(" ", "") : updatedTime;
	const headerDetail = provider.quotaUpdatedAt
		? tFormat(language, "quotaUpdated", { time: compactUpdatedTime })
		: (language === "zh-CN" ? "订阅驱动" : "Subscription");
	const providerTitle = ({ chatgpt: "ChatGPT", grok: "Grok", cursor: "Cursor" } as Record<string, string>)[provider.id]
		?? provider.displayName.replace(/\s*订阅$/u, "");
	const act = async (kind: "login" | "logout") => {
		setWorking(true);
		try { await execute({ kind, sessionId, target: kind === "login" ? provider.id : `${provider.id}/${provider.accountId}` }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setWorking(false); }
	};
	const toggleModel: ModelToggle = async (model, index, modelIDs, workingKey) => {
		const ids = modelIDs?.length ? modelIDs : [model.id];
		setWorkingModel(workingKey || `${index}:${model.id}`);
		try {
			await execute({
				kind: "set_model_enabled", sessionId, target: provider.id, name: model.id,
				decision: String(Boolean(model.disabled)),
				...(ids.length > 1 ? { payload: { modelIds: ids } } : {}),
			});
		}
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setWorkingModel(""); }
	};
	const refreshModels = async () => {
		setDiscovering(true);
		try { await execute({ kind: "discover_provider_models", sessionId, target: provider.id }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setDiscovering(false); }
	};
	const enabledCount = models.filter((model) => !model.disabled).length;
	return <div className="provider-workspace subscription-provider-stack">
		<section className="provider-editor provider-overview subscription-provider settings-card">
			<ProviderHeader provider={provider} title={providerTitle} detail={headerDetail} control={
				<div className="subscription-header-controls">
					<span className="subscription-account-identity" title={provider.enabled ? account : undefined}>
						<strong>{provider.enabled ? account : (language === "zh-CN" ? "未连接" : "Disconnected")}</strong>
						<small>{provider.enabled ? plan : t("subscriptionLoginRequired")}</small>
					</span>
					<button type="button" role="switch" aria-checked={provider.enabled} aria-label={provider.enabled ? t("logout") : t("login")} className="provider-state-control subscription-state-toggle" disabled={working} onClick={() => void act(provider.enabled ? "logout" : "login")}>
						<span className={`settings-switch ${provider.enabled ? "on" : ""}`} aria-hidden="true"><span /></span>
					</button>
				</div>
			} />
			{provider.enabled && <SubscriptionQuota provider={provider} language={language} now={now} />}
		</section>
		<ProviderModelCatalog
			provider={provider}
			models={models}
			enabledCount={enabledCount}
			description={provider.id === "cursor"
				? (language === "zh-CN" ? "思考与 Fast 变体已并入同一系列，不再单独列出；开关同时作用于该系列全部版本。" : "Thinking and Fast variants stay inside one family and are not listed separately; the switch applies to every version.")
				: (language === "zh-CN" ? "订阅服务实时同步；启用后可在模型路由中分配用途。" : t("subscriptionModelsHint"))}
			language={language}
			workingModel={workingModel}
			onToggle={toggleModel}
			badgesFor={subscriptionModelCapabilityBadges}
			emptyLabel={provider.enabled ? t("subscriptionModelsEmpty") : t("subscriptionLoginHint")}
			showModels={provider.enabled}
			action={provider.enabled ? <div className="provider-model-actions"><button className="small-button" disabled={discovering} onClick={() => void refreshModels()}><RefreshCw size={14} className={discovering ? "spin" : undefined} />{discovering ? t("discoveringModels") : t("refreshSubscriptionModels")}</button></div> : undefined}
		/>
	</div>;
}

function ProviderHeader({ provider, detail, control, title }: { provider: ModelProvider; detail: string; control: ReactNode; title?: string }) {
	return <header className="provider-workspace-header">
		<div className="provider-heading"><ProviderIcon provider={provider.id} logoID={provider.modelsDevId} size={22} /><span><strong>{title || provider.displayName}</strong><small>{detail}</small></span></div>
		{control}
	</header>;
}

type ModelCardItem = Pick<LLMuxModelConfig, "id" | "name" | "disabled" | "aliases" | "reasoningLevels" | "capabilities" | "inputModalities" | "outputModalities">;

type ModelBadges = (model: ModelCardItem, language: Language) => CapabilityBadge[];
type ModelToggle = (model: ModelCardItem, index: number, modelIDs?: string[], workingKey?: string) => void;

export function filterProviderCatalogModels<T extends { id: string; name?: string; aliases?: string[] }>(providerID: string, models: readonly T[], query: string): T[] {
	const normalized = query.trim().toLocaleLowerCase();
	if (!normalized) return [...models];
	const matches = (model: T) => [model.id, model.name ?? "", ...(model.aliases ?? [])]
		.some((value) => value.toLocaleLowerCase().includes(normalized));
	if (providerID !== "cursor") return models.filter(matches);
	const matchedIDs = new Set(groupCursorModelVariants(models)
		.filter((group) => group.id.includes(normalized) || group.name.toLocaleLowerCase().includes(normalized) || group.variants.some((variant) => matches(variant.model)))
		.flatMap((group) => group.variants.map((variant) => variant.model.id)));
	return models.filter((model) => matchedIDs.has(model.id));
}

function ModelCards({ provider, models, language, workingModel, onToggle, badgesFor, emptyLabel }: { provider: ModelProvider; models: ModelCardItem[]; language: Language; workingModel: string; onToggle: ModelToggle; badgesFor: ModelBadges; emptyLabel: string }) {
	const routes = useRuntimeStore((state) => state.modelRoutes);
	const activeProvider = useRuntimeStore((state) => state.snapshot?.provider);
	const activeModel = useRuntimeStore((state) => state.snapshot?.model);
	if (models.length === 0) return <small className="subscription-models-empty">{emptyLabel}</small>;
	if (provider.id === "cursor") {
		return <CursorModelCards provider={provider} models={models} language={language} workingModel={workingModel} onToggle={onToggle} routes={routes} activeProvider={activeProvider} activeModel={activeModel} />;
	}
	return <div className="subscription-models provider-model-grid">{models.map((model, index) => <ModelCard key={`${index}-${model.id}`} provider={provider} model={model} index={index} language={language} working={workingModel === `${index}:${model.id}`} badges={badgesFor(model, language)} useLabel={modelUseLabel(provider.id, model.id, routes, activeProvider, activeModel, language)} onToggle={onToggle} />)}</div>;
}

function CursorModelCards({ provider, models, language, workingModel, onToggle, routes, activeProvider, activeModel }: { provider: ModelProvider; models: ModelCardItem[]; language: Language; workingModel: string; onToggle: ModelToggle; routes: ModelRoute[]; activeProvider: string | undefined; activeModel: string | undefined }) {
	const groups = useMemo(() => groupCursorModelVariants(models), [models]);
	return <div className="cursor-model-groups" data-model-groups={groups.length}>{groups.map((group) => {
		const preferred = group.variants.find((variant) => activeProvider === provider.id && variant.model.id === activeModel)
			?? group.variants.find((variant) => routes.some((route) => route.route.provider === provider.id && route.route.model === variant.model.id))
			?? group.variants.find((variant) => !variant.model.disabled)
			?? group.variants[0];
		if (!preferred) return null;
		return <CursorFamilyCard
			key={group.id}
			provider={provider}
			group={group}
			model={preferred.model}
			index={preferred.sourceIndex}
			language={language}
			working={workingModel === `group:${group.id}`}
			useLabel={familyUseLabel(provider.id, group.variants.map((variant) => variant.model.id), routes, activeProvider, activeModel, language)}
			onToggle={onToggle}
		/>;
	})}</div>;
}

function CursorFamilyCard({ provider, group, model, index, language, working, useLabel, onToggle }: { provider: ModelProvider; group: CursorModelGroup<ModelCardItem>; model: ModelCardItem; index: number; language: Language; working: boolean; useLabel: string; onToggle: ModelToggle }) {
	const variantIDs = group.variants.map((variant) => variant.model.id);
	const enabledVariants = group.variants.filter((variant) => !variant.model.disabled).length;
	const allDisabled = enabledVariants === 0;
	const allEnabled = enabledVariants === group.variants.length;
	const stateLabel = allDisabled
		? (language === "zh-CN" ? "全部关闭" : "All off")
		: allEnabled
			? (language === "zh-CN" ? "全部启用" : "All enabled")
			: `${enabledVariants} / ${group.variants.length} ${language === "zh-CN" ? "已启用" : "enabled"}`;
	const noZDR = group.variants.some((variant) => variant.noZDR);
	const retentionWarning = noZDR ? (language === "zh-CN" ? "数据保留" : "Data retained") : undefined;
	const retentionDetail = noZDR ? (language === "zh-CN" ? "该系列不提供零数据保留；输入与输出可能由 Cursor 或模型提供方保存。" : "This family has no zero-data-retention guarantee; Cursor or the model provider may retain inputs and outputs.") : undefined;
	return <article className="cursor-family-row" data-disabled={allDisabled} data-working={working} data-model-group={group.id} data-model-id={model.id} data-variant-count={String(group.variants.length)}>
		<ProviderIcon provider={provider.id} logoID={provider.modelsDevId} size={18} />
		<span className="cursor-family-copy"><strong>{group.name}</strong>{retentionWarning && <em className="model-retention-warning" title={retentionDetail}><ShieldAlert size={11} />{retentionWarning}</em>}{useLabel && <em className="model-use-badge">{useLabel}</em>}<small className="cursor-model-inventory">{cursorFamilyInventory(group, language)}</small></span>
		<button className="model-card-toggle" disabled={!model.id || working} onClick={() => onToggle({ ...model, disabled: allDisabled }, index, variantIDs, `group:${group.id}`)} aria-pressed={!allDisabled} aria-label={tFormat(language, allDisabled ? "enableModel" : "disableModel", { model: group.name })}><span className="model-toggle-label">{stateLabel}</span><span className={`settings-switch ${allDisabled ? "" : "on"}`} aria-hidden="true"><span /></span></button>
	</article>;
}

function ModelCard({ provider, model, index, language, working, badges, useLabel, onToggle }: { provider: ModelProvider; model: ModelCardItem; index: number; language: Language; working: boolean; badges: CapabilityBadge[]; useLabel: string; onToggle: ModelToggle }) {
	const t = translator(language);
	const name = modelDisplayName(model.id, model.name);
	const [openCapability, setOpenCapability] = useState("");
	const enabledLabel = model.disabled ? (language === "zh-CN" ? "未启用" : "Disabled") : (language === "zh-CN" ? "已启用" : "Enabled");
	return <article className="subscription-model provider-model-card model-card" data-disabled={Boolean(model.disabled)} data-working={working} data-model-id={model.id}>
		<div className="provider-model-card-main">
			<div className="provider-model-identity"><ProviderIcon provider={provider.id} logoID={provider.modelsDevId} /><span><strong>{name}</strong>{useLabel && <em className="model-use-badge">{useLabel}</em>}</span></div>
			<div className="provider-model-state"><button className="model-card-toggle" disabled={!model.id || working} onClick={() => onToggle(model, index)} aria-pressed={!model.disabled} aria-label={tFormat(language, model.disabled ? "enableModel" : "disableModel", { model: name })}><span className="model-toggle-label">{enabledLabel}</span><span className={`settings-switch ${model.disabled ? "" : "on"}`} aria-hidden="true"><span /></span></button></div>
		</div>
		<div className="provider-model-card-footer"><span className="subscription-model-capabilities" role="list" aria-label={t("modelCapabilities")}>{badges.map((badge) => {
			const Icon = badge.icon;
			return <details key={badge.key} className="model-capability-popover" role="listitem" open={openCapability === badge.key}>
				<summary className="model-icon-badge" aria-label={badge.label} onClick={(event) => { event.preventDefault(); setOpenCapability((current) => current === badge.key ? "" : badge.key); }}><Icon size={13} aria-hidden="true" /></summary>
				<span className="model-capability-label" role="tooltip">{badge.label}</span>
			</details>;
		})}</span></div>
	</article>;
}

function ProviderModelCatalog({ provider, models, enabledCount, description, warning, language, workingModel, onToggle, badgesFor, emptyLabel, showModels = true, action }: { provider: ModelProvider; models: ModelCardItem[]; enabledCount: number; description: string; warning?: string; language: Language; workingModel: string; onToggle: ModelToggle; badgesFor: ModelBadges; emptyLabel: string; showModels?: boolean; action?: ReactNode }) {
	const [query, setQuery] = useState("");
	const filteredModels = filterProviderCatalogModels(provider.id, models, query);
	const groupCount = provider.id === "cursor" ? groupCursorModelVariants(models).length : models.length;
	const visibleCount = provider.id === "cursor" ? groupCursorModelVariants(filteredModels).length : filteredModels.length;
	const noMatches = query.trim() ? (language === "zh-CN" ? "没有匹配的模型系列或版本" : "No matching model families or variants") : emptyLabel;
	const catalogWarning = visibleCatalogWarning(warning);
	return <section className="provider-model-catalog subscription-model-note settings-card">
		<header className="provider-models-header"><div><strong>{language === "zh-CN" ? "模型" : "Models"}</strong><small>{description}</small>{catalogWarning && <small className="provider-model-warning">{catalogWarning}</small>}</div><div className="provider-model-summary"><em>{provider.id === "cursor" ? `${groupCount} ${language === "zh-CN" ? "个系列" : "families"} · ` : null}<b>{enabledCount}</b> / {models.length} {language === "zh-CN" ? "已启用" : "enabled"}</em>{action}</div></header>
		{showModels && models.length > 0 && <label className="provider-model-catalog-search"><Search size={15} aria-hidden="true" /><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={language === "zh-CN" ? "搜索模型系列、版本或原始 ID…" : "Search model family, variant, or raw ID…"} aria-label={language === "zh-CN" ? "搜索模型目录" : "Search model catalog"} /><span>{visibleCount} / {groupCount}</span></label>}
		<div className="provider-models">{showModels ? <ModelCards provider={provider} models={filteredModels} language={language} workingModel={workingModel} onToggle={onToggle} badgesFor={badgesFor} emptyLabel={noMatches} /> : <small className="subscription-models-empty">{emptyLabel}</small>}</div>
	</section>;
}

function visibleCatalogWarning(warning?: string) {
	if (!warning) return "";
	return warning.split(/;\s*/).map((part) => part.trim()).filter((part) => part && !/^models\.dev metadata did not match \d+ model\(s\)$/i.test(part)).join("; ");
}

function familyUseLabel(providerID: string, variantIDs: readonly string[], routes: ModelRoute[], activeProvider: string | undefined, activeModel: string | undefined, language: Language) {
	for (const modelID of variantIDs) {
		const label = modelUseLabel(providerID, modelID, routes, activeProvider, activeModel, language);
		if (label) return label;
	}
	return "";
}

function cursorFamilyInventory(group: CursorModelGroup<ModelCardItem>, language: Language) {
	const zh = language === "zh-CN";
	const capabilities = new Set<string>();
	const inputs = new Set<string>();
	let reasoning = false;
	let vision = false;
	for (const variant of group.variants) {
		for (const capability of variant.model.capabilities ?? []) capabilities.add(capability.trim().toLocaleLowerCase());
		for (const modality of variant.model.inputModalities ?? []) inputs.add(modality.trim().toLocaleLowerCase());
		if ((variant.model.reasoningLevels?.length ?? 0) > 0) reasoning = true;
		const identity = `${variant.model.id} ${variant.model.name ?? ""}`.toLocaleLowerCase();
		if (identity.includes("claude") || identity.includes("gemini") || identity.includes("gpt-") || identity.includes("codex")) vision = true;
	}
	const parts: string[] = [];
	if (capabilities.has("reasoning") || reasoning) parts.push(zh ? "推理" : "Reasoning");
	if (inputs.has("image") || vision) parts.push(zh ? "图像" : "Image");
	if (capabilities.has("tools")) parts.push(zh ? "工具" : "Tools");
	if (capabilities.has("structured-output")) parts.push(zh ? "结构化" : "Structured");
	if (parts.length > 0) return parts.join(" · ");
	return zh ? "推理 · 工具" : "Reasoning · tools";
}

function modelUseLabel(providerID: string, modelID: string, routes: ModelRoute[], activeProvider: string | undefined, activeModel: string | undefined, language: Language) {
	if (providerID === activeProvider && modelID === activeModel) return language === "zh-CN" ? "当前主模型" : "Current";
	const matchingRoutes = routes.filter((route) => route.route.provider === providerID && route.route.model === modelID);
	const scope = ["main", "approval", "plan", "title", "vision", "recap", "subagent"].find((candidate) => matchingRoutes.some((route) => route.scope === candidate));
	if (!scope) return "";
	const labels = language === "zh-CN"
		? { main: "主模型", approval: "审批", plan: "规划", title: "标题", vision: "视觉", recap: "回顾", subagent: "子智能体" }
		: { main: "Main", approval: "Approval", plan: "Plan", title: "Titles", vision: "Vision", recap: "Recap", subagent: "Subagent" };
	return labels[scope as keyof typeof labels];
}

function providerDirectoryDetail(provider: ModelProvider, language: Language) {
	if (provider.subscription) {
		const plan = formatSubscriptionTier(provider.id, provider.accountPlan || "");
		if (provider.id === "chatgpt") return [plan, provider.quotaBalance && `${language === "zh-CN" ? "额外" : "Extra"} ${provider.quotaBalance}`].filter(Boolean).join(" · ");
		return plan || (language === "zh-CN" ? "订阅驱动" : "Subscription");
	}
	return `${provider.backend === "openai_compat" ? "llmux" : provider.backend} · ${provider.models.length} ${language === "zh-CN" ? "个模型" : "models"}`;
}

function providerDirectoryStatus(provider: ModelProvider, language: Language) {
	if (provider.quotaAvailable) return `${Math.max(0, 100-(provider.quotaUsedPercent ?? 0)).toLocaleString(language, { maximumFractionDigits: 1 })}%`;
	return provider.enabled ? "" : language === "zh-CN" ? "未启用" : "Disabled";
}

type QuotaPace = {
	expectedRemaining: number;
	deltaPercent: number;
	state: "deficit" | "reserve" | "on_pace";
	exhaustsInMs: number | null;
	lastsToReset: boolean;
};

type SubscriptionQuotaRow = {
	id: string;
	label: string;
	usedPercent: number;
	remainingPercent: number;
	pace: QuotaPace | null;
};

function SubscriptionQuota({ provider, language, now }: { provider: ModelProvider; language: Language; now: number }) {
	const t = translator(language);
	const rows = subscriptionQuotaRows(provider, language, now);
	const resetText = provider.quotaResetsAt
		? tFormat(language, "quotaResetCountdown", { time: formatQuotaDuration(provider.quotaResetsAt*1000-now) })
		: "";
	return <section className="subscription-quota" aria-label={t("subscriptionQuota")}>
		{rows.length > 0 ? <div className="subscription-quota-grid">
			{rows.map((row) => {
				const formatted = row.remainingPercent.toLocaleString(language, { maximumFractionDigits: 0 });
				const remainingLabel = language === "zh-CN" ? `${formatted}% 剩余` : tFormat(language, "quotaRemaining", { percent: formatted });
				const paceLabel = row.pace ? quotaPaceLabel(row.pace, language) : null;
				return <article key={row.id} className="subscription-quota-item" data-quota={row.id}>
					<div className="subscription-quota-row-heading"><strong>{row.label} <span>{remainingLabel}</span></strong>{resetText && <em>{resetText}</em>}</div>
					<div className="subscription-quota-track" role="progressbar" aria-label={`${row.label} ${remainingLabel}`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={row.remainingPercent}>
						<span className="subscription-quota-fill" style={{ width: `${row.remainingPercent}%` }} />
						{[20, 50, 80].map((percent) => <i key={percent} className="subscription-quota-marker threshold" style={{ left: `${percent}%` }} aria-hidden="true" />)}
						{row.pace && row.pace.state !== "on_pace" && <i className="subscription-quota-marker pace" data-state={row.pace.state} style={{ left: `${row.pace.expectedRemaining}%` }} aria-hidden="true" />}
					</div>
					{paceLabel && <small className="subscription-quota-pace"><span>{paceLabel.left}</span><b aria-hidden="true">·</b><span>{paceLabel.right}</span></small>}
				</article>;
			})}
			{(provider.quotaUnlimited || provider.quotaBalance) && <footer className="subscription-quota-balance"><span><strong>{t("quotaBalance")}</strong><small>{quotaBalanceHint(provider.quotaPeriod, provider.quotaUnlimited, language)}</small></span><em>{provider.quotaUnlimited ? t("quotaUnlimited") : formatQuotaBalance(provider.quotaBalance!, language)}</em></footer>}
		</div> : !provider.quotaWarning && <small className="subscription-quota-empty">{t("quotaUnavailable")}</small>}
		{provider.quotaWarning && <small className="subscription-quota-error" role="status">{tFormat(language, "quotaUnavailableReason", { reason: provider.quotaWarning })}</small>}
	</section>;
}

function subscriptionQuotaRows(provider: ModelProvider, language: Language, now: number): SubscriptionQuotaRow[] {
	if (!provider.quotaAvailable) return [];
	const t = translator(language);
	const rows = [{
		id: "total",
		label: provider.id === "cursor" ? t("quotaTotal") : quotaWindowLabel(provider.quotaPeriod, t),
		usedPercent: provider.quotaUsedPercent ?? 0,
	}, ...(provider.quotaBreakdown ?? []).map((item) => ({
		id: item.id,
		label: quotaBreakdownLabel(item.id, t),
		usedPercent: item.usedPercent,
	}))];
	return rows.map((row) => {
		const usedPercent = clampPercent(row.usedPercent);
		return {
			...row,
			usedPercent,
			remainingPercent: 100-usedPercent,
			pace: quotaPace(provider.quotaStartedAt, provider.quotaResetsAt, usedPercent, now),
		};
	});
}

function quotaBreakdownLabel(id: string, t: (key: MessageKey) => string) {
	if (id === "cursor") return t("quotaCursor");
	if (id === "third_party") return t("quotaThirdParty");
	return id;
}

function quotaPace(startedAt: number | undefined, resetsAt: number | undefined, usedPercent: number, now: number): QuotaPace | null {
	if (!startedAt || !resetsAt) return null;
	const start = startedAt*1000;
	const reset = resetsAt*1000;
	if (reset <= start || now <= start || now >= reset) return null;
	const duration = reset-start;
	const elapsed = now-start;
	const untilReset = reset-now;
	const expectedUsed = clampPercent(elapsed/duration*100);
	const deltaPercent = usedPercent-expectedUsed;
	const roundedDelta = Math.round(Math.abs(deltaPercent));
	const state: QuotaPace["state"] = roundedDelta === 0 ? "on_pace" : deltaPercent > 0 ? "deficit" : "reserve";
	let exhaustsInMs: number | null = null;
	let lastsToReset = usedPercent <= 0;
	if (usedPercent >= 100) {
		exhaustsInMs = 0;
		lastsToReset = false;
	} else if (usedPercent > 0) {
		exhaustsInMs = (100-usedPercent)/(usedPercent/elapsed);
		lastsToReset = exhaustsInMs >= untilReset;
		if (lastsToReset) exhaustsInMs = null;
	}
	return { expectedRemaining: 100-expectedUsed, deltaPercent, state, exhaustsInMs, lastsToReset };
}

function quotaPaceLabel(pace: QuotaPace, language: Language) {
	const percent = Math.round(Math.abs(pace.deltaPercent));
	const left = pace.state === "deficit"
		? tFormat(language, "quotaDeficit", { percent })
		: pace.state === "reserve"
			? tFormat(language, "quotaReserve", { percent })
			: translator(language)("quotaOnPace");
	const right = pace.lastsToReset || pace.exhaustsInMs === null
		? translator(language)("quotaLastsReset")
		: tFormat(language, "quotaRunsOut", { time: formatQuotaDuration(pace.exhaustsInMs) });
	return { left, right };
}

function formatQuotaDuration(milliseconds: number) {
	const totalMinutes = Math.max(0, Math.floor(milliseconds/60_000));
	const days = Math.floor(totalMinutes/(24*60));
	const hours = Math.floor(totalMinutes/60)%24;
	const minutes = totalMinutes%60;
	if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
	if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
	return `${minutes}m`;
}

function clampPercent(value: number) {
	return Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0));
}


function quotaWindowLabel(period: string | undefined, t: (key: MessageKey) => string) {
	if (period === "monthly") return t("monthlyQuota");
	if (period === "credits") return t("creditsQuota");
	return t("weeklyQuota");
}

function quotaBalanceHint(period: string | undefined, unlimited: boolean | undefined, language: Language) {
	if (language === "zh-CN") {
		if (unlimited) return "当前订阅账户可用";
		if (period === "monthly") return "用完每月额度后继续使用";
		if (period === "credits") return "用完订阅额度后继续使用";
		return "用完每周额度后继续使用";
	}
	if (unlimited) return "Available on this subscription";
	if (period === "monthly") return "Available after the monthly allowance";
	if (period === "credits") return "Available after the included allowance";
	return "Available after the weekly allowance";
}

function formatQuotaBalance(value: string, language: Language) {
	const amount = Number(value);
	return Number.isFinite(amount) ? new Intl.NumberFormat(language, { style: "currency", currency: "USD" }).format(amount) : value;
}


function formatSubscriptionTier(provider: string, plan: string) {
	const normalized = plan.trim().toLowerCase();
	if (provider === "cursor") {
		if (/^cursor\s+/iu.test(plan)) return plan;
		const cursorPlan = ({
			ultra: "Ultra", pro_plus: "Pro+", pro: "Pro", pro_student: "Pro",
			free_trial: "Pro Trial", hobby: "Hobby", free: "Free", team: "Team",
			business: "Business", enterprise: "Enterprise",
		} as Record<string, string>)[normalized] ?? plan;
		return cursorPlan ? `Cursor ${cursorPlan}` : "";
	}
	if (provider === "chatgpt" && normalized === "pro") return "Pro 20x";
	return ({ prolite: "Pro 5x", plus: "Plus", team: "Team", business: "Business", enterprise: "Enterprise", free: "Free" } as Record<string, string>)[normalized] ?? plan;
}

function ProviderEditor({ provider, sessionId, language, setError, creating = false }: { provider: ModelProvider; sessionId: string; language: Language; setError: (message: string) => void; creating?: boolean }) {
	const t = translator(language);
	const [draft, setDraft] = useState<ModelProvider>(() => cloneProvider(provider));
	const [secret, setSecret] = useState("");
	const [saving, setSaving] = useState(false);
	const [discovering, setDiscovering] = useState(false);
	const [workingModel, setWorkingModel] = useState("");
	useEffect(() => setDraft(cloneProvider(provider)), [provider]);
	const updateModel = (index: number, update: Partial<LLMuxModelConfig>) => setDraft((current) => ({ ...current, models: current.models.map((model, currentIndex) => currentIndex === index ? { ...model, ...update } : model) }));
	const toggleModel = async (model: ModelCardItem, index: number) => {
		const enabled = Boolean(model.disabled);
		if (provider.modelsSource === "provider_api_preview") {
			updateModel(index, { disabled: !enabled });
			return;
		}
		const key = `${index}:${model.id}`;
		setWorkingModel(key);
		updateModel(index, { disabled: !enabled });
		try { await execute({ kind: "set_model_enabled", sessionId, target: provider.id, name: model.id, decision: String(enabled) }); }
		catch (cause) {
			updateModel(index, { disabled: Boolean(model.disabled) });
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally { setWorkingModel(""); }
	};
	const save = async () => {
		setSaving(true);
		try {
			await execute({ kind: "set_model_provider", sessionId, provider: draft, secret });
			setSecret("");
		} catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setSaving(false); }
	};
	const discover = async () => {
		setDiscovering(true);
		try { await execute({ kind: "discover_provider_models", sessionId, provider: draft, secret }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setDiscovering(false); }
	};
	const modelDescription = provider.modelsSource?.startsWith("provider_api") ? tFormat(language, provider.modelsSource.includes("models.dev") ? "discoveredModels" : "discoveredAPIModels", { count: draft.models.length }) : t("providerModelsHint");
	return <div className="provider-workspace">
		<section className="provider-editor provider-overview settings-card">
			<ProviderHeader provider={{ ...provider, displayName: creating ? (language === "zh-CN" ? "添加提供方" : "Add provider") : provider.displayName }} detail={creating ? "OpenAI compatible API" : `${provider.backend} · ${provider.id}`} control={
				<label className="provider-state-control provider-switch"><span className="provider-state-copy"><strong>{draft.enabled ? (language === "zh-CN" ? "已启用" : "Enabled") : (language === "zh-CN" ? "未启用" : "Disabled")}</strong><small>{t("enableProvider")}</small></span><input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} /><span className={`settings-switch ${draft.enabled ? "on" : ""}`} aria-hidden="true"><span /></span></label>
			} />
			<div className={`provider-fields ${creating ? "provider-fields-creating" : ""}`}>
				{creating && <><label><span>{language === "zh-CN" ? "提供方名称" : "Provider name"}</span><input value={draft.displayName} onChange={(event) => setDraft({ ...draft, displayName: event.target.value })} placeholder="Acme AI" /></label><label><span>{language === "zh-CN" ? "提供方 ID" : "Provider ID"}</span><input value={draft.id} onChange={(event) => setDraft({ ...draft, id: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "") })} placeholder="acme" /></label></>}
				<label><span>{t("apiBaseURL")}</span><input value={draft.baseUrl} readOnly={Boolean(provider.defaultBaseUrl)} onChange={(event) => setDraft({ ...draft, baseUrl: event.target.value })} placeholder={provider.defaultBaseUrl || t("customAPIBaseURL")} /><small>{provider.defaultBaseUrl ? t("officialAPIAddressLocked") : t("customAPIAddressHint")}</small></label>
				<label><span>{t("apiKey")}</span><input type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder={provider.credentialConfigured ? t("keepCredential") : provider.envKey} /><small>{credentialHint(provider, language)}</small></label>
			</div>
			<footer><button className="small-button primary" disabled={saving || !draft.id.trim() || !draft.displayName.trim() || !draft.baseUrl.trim()} onClick={() => void save()}>{saving ? t("saving") : t("saveProvider")}</button></footer>
		</section>
		<ProviderModelCatalog provider={provider} models={draft.models} enabledCount={draft.models.filter((model) => !model.disabled).length} description={modelDescription} warning={provider.modelsWarning} language={language} workingModel={workingModel} onToggle={toggleModel} badgesFor={modelCapabilityBadges} emptyLabel={t("noConfiguredModels")} action={<div className="provider-model-actions"><button className="small-button" disabled={!draft.enabled || discovering} onClick={() => void discover()}><RefreshCw size={14} />{discovering ? t("discoveringModels") : t("discoverModels")}</button></div>} />
	</div>;
}

function blankProvider(language: Language): ModelProvider {
	return {
		id: "custom", displayName: language === "zh-CN" ? "自定义提供方" : "Custom provider", backend: "openai_compat",
		defaultBaseUrl: "", baseUrl: "", envKey: "CUSTOM_API_KEY", enabled: false,
		credentialConfigured: false, credentialSource: "none", models: [],
	};
}

function cloneProvider(provider: ModelProvider): ModelProvider { return { ...provider, models: provider.models.map((model) => ({ ...model, aliases: [...(model.aliases ?? [])], reasoningLevels: [...(model.reasoningLevels ?? [])], capabilities: [...(model.capabilities ?? [])], inputModalities: [...(model.inputModalities ?? [])], outputModalities: [...(model.outputModalities ?? [])] })) }; }

type CapabilityBadge = { key: string; label: string; icon: typeof Wrench };

function modelCapabilityBadges(model: Pick<LLMuxModelConfig, "capabilities" | "inputModalities" | "outputModalities">, language: Language): CapabilityBadge[] {
	const zh = language === "zh-CN";
	const capabilityNames: Record<string, string> = zh
		? { tools: "工具调用", "parallel-tools": "并行工具", reasoning: "思考", "structured-output": "结构化输出" }
		: { tools: "Tools", "parallel-tools": "Parallel tools", reasoning: "Reasoning", "structured-output": "Structured output" };
	const capabilityIcons: Record<string, typeof Wrench> = {
		tools: Wrench, "parallel-tools": Layers, reasoning: Brain, "structured-output": Braces,
	};
	const modalityIcons: Record<string, typeof Wrench> = {
		text: Type, image: ImageIcon, audio: AudioLines, video: ImageIcon,
	};
	const badges: CapabilityBadge[] = [];
	for (const capability of model.capabilities ?? []) {
		badges.push({
			key: `cap-${capability}`,
			label: capabilityNames[capability] ?? capability,
			icon: capabilityIcons[capability] ?? Wrench,
		});
	}
	const inputModalities = [...(model.inputModalities ?? [])].sort((left, right) => Number(left === "text") - Number(right === "text"));
	for (const modality of inputModalities) {
		badges.push({
			key: `in-${modality}`,
			label: zh ? `输入: ${modality}` : `Input: ${modality}`,
			icon: modalityIcons[modality] ?? Type,
		});
	}
	for (const modality of model.outputModalities ?? []) {
		badges.push({
			key: `out-${modality}`,
			label: zh ? `输出: ${modality}` : `Output: ${modality}`,
			icon: modality === "text" ? MessageSquareText : (modalityIcons[modality] ?? MessageSquareText),
		});
	}
	return badges;
}

function subscriptionModelCapabilityBadges(model: Pick<ModelOption, "capabilities" | "inputModalities" | "outputModalities">, language: Language): CapabilityBadge[] {
	const defaults = modelCapabilityBadges({ capabilities: ["tools", "reasoning", "structured-output"], inputModalities: ["text"], outputModalities: ["text"] }, language);
	const badges = new Map(defaults.map((badge) => [badge.key, badge]));
	for (const badge of modelCapabilityBadges(model, language)) badges.set(badge.key, badge);
	return [...badges.values()];
}

function credentialHint(provider: ModelProvider, language: Language) {
	const t = translator(language);
	if (provider.credentialSource === "stored") return t("credentialStored");
	if (provider.credentialSource === "environment") return tFormat(language, "credentialEnvironment", { key: provider.envKey });
	if (provider.credentialSource === "pending") return t("credentialPending");
	if (provider.credentialConfigured) return t("credentialNotRequired");
	return tFormat(language, "credentialMissing", { key: provider.envKey });
}
