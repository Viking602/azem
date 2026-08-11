import { useEffect, useState, type ReactNode } from "react";
import {
	AudioLines, Braces, Brain, Image as ImageIcon, Layers, MessageSquareText,
	RefreshCw, Search, Type, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { tFormat, translator, type Language } from "../i18n";
import { modelDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { LLMuxModelConfig, ModelProvider, ModelRoute } from "../types";
import ProviderIcon from "./ProviderIcon";

const PROVIDER_PAGE_SIZE = 24;

export default function ModelProviderSettings({ providers, modelsByProvider, sessionId, language, setError, addRequest = 0 }: { providers: ModelProvider[]; modelsByProvider: Record<string, ModelOption[]>; sessionId: string; language: Language; setError: (message: string) => void; addRequest?: number }) {
	const t = translator(language);
	const [query, setQuery] = useState("");
	const [visibleCount, setVisibleCount] = useState(PROVIDER_PAGE_SIZE);
	const [loading, setLoading] = useState(providers.length === 0);
	const [loadError, setLoadError] = useState("");
	const configured = providers.filter((provider) => provider.Enabled || provider.Models.length > 0);
	const filtered = providers.filter((provider) => `${provider.DisplayName} ${provider.ID}`.toLowerCase().includes(query.trim().toLowerCase()));
	const visible = query.trim() ? filtered : [...configured, ...providers.filter((provider) => !configured.includes(provider)).slice(0, visibleCount)];
	const [selectedID, setSelectedID] = useState("");
	const [customProvider, setCustomProvider] = useState<ModelProvider | null>(null);
	const selected = selectedID === "__custom__" ? customProvider : providers.find((provider) => provider.ID === selectedID) ?? configured[0] ?? providers[0];
	useEffect(() => setVisibleCount(PROVIDER_PAGE_SIZE), [providers.length, query]);
	useEffect(() => {
		if (providers.length > 0) {
			setLoading(false);
			setLoadError("");
		}
	}, [providers.length]);
	useEffect(() => {
		if (!selectedID && selected) setSelectedID(selected.ID);
	}, [selected, selectedID]);
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
	return <section className="settings-pane"><header><div><h2>{t("modelSettings")}</h2><p>{t("modelSettingsHint")}</p></div><button className="small-button" disabled={loading} onClick={() => void refresh()}><RefreshCw size={13} className={loading ? "spin" : undefined} />{loading ? t("loadingProviders") : t("refresh")}</button></header>
		{loadError && <div className="settings-inline-error" role="alert">{loadError}</div>}
		<div className="provider-settings">
			<div className="provider-directory settings-card">
				<label className="provider-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={language === "zh-CN" ? "搜索供应商" : "Search providers"} disabled={loading && providers.length === 0} /></label>
				<div className="provider-list" onScroll={(event) => loadMore(event.currentTarget)}>
					{visible.map((provider) => <button key={provider.ID} className={selected?.ID === provider.ID ? "active" : ""} onClick={() => setSelectedID(provider.ID)}><span className="provider-identity"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} /><span><strong>{provider.DisplayName}</strong><small>{providerDirectoryDetail(provider, language)}</small></span></span><em className={`${provider.Enabled ? "enabled" : ""} ${provider.QuotaAvailable ? "quota" : ""}`}>{providerDirectoryStatus(provider, language)}</em></button>)}
					{providers.length === 0 && <div className="settings-empty provider-list-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
					<button className={selectedID === "__custom__" ? "active custom-provider-entry" : "custom-provider-entry"} onClick={() => { setCustomProvider(blankProvider(language)); setSelectedID("__custom__"); }}><span className="provider-identity"><span className="provider-add-icon">+</span><span><strong>{language === "zh-CN" ? "自定义提供方" : "Custom provider"}</strong><small>{language === "zh-CN" ? "OpenAI 兼容 API" : "OpenAI compatible API"}</small></span></span><em>{language === "zh-CN" ? "未启用" : "Disabled"}</em></button>
				</div>
				{!query.trim() && providers.length > visible.length && <small className="provider-more" aria-live="polite">{tFormat(language, "moreProviders", { count: providers.length - visible.length })}</small>}
			</div>
			{selected ? selected.Subscription ? <SubscriptionProviderEditor key={selected.ID} provider={selected} models={modelsByProvider[selected.ID] ?? []} sessionId={sessionId} language={language} setError={setError} /> : <ProviderEditor key={selectedID === "__custom__" ? `custom-${addRequest}` : selected.ID} provider={selected} sessionId={sessionId} language={language} setError={setError} creating={selectedID === "__custom__"} /> : <div className="settings-card settings-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
		</div>
	</section>;
}

function SubscriptionProviderEditor({ provider, models, sessionId, language, setError }: { provider: ModelProvider; models: ModelOption[]; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [working, setWorking] = useState(false);
	const [workingModel, setWorkingModel] = useState("");
	const act = async (kind: "login" | "logout") => {
		setWorking(true);
		try { await execute({ kind, sessionId, target: kind === "login" ? provider.ID : `${provider.ID}/${provider.AccountID}` }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setWorking(false); }
	};
	const toggleModel = async (model: ModelCardItem, index: number) => {
		setWorkingModel(`${index}:${model.id}`);
		try { await execute({ kind: "set_model_enabled", sessionId, target: provider.ID, name: model.id, decision: String(Boolean(model.disabled)) }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setWorkingModel(""); }
	};
	const enabledCount = models.filter((model) => !model.disabled).length;
	return <div className="provider-workspace subscription-provider-stack">
		<section className="provider-editor provider-overview subscription-provider settings-card">
			<ProviderHeader provider={provider} detail={language === "zh-CN" ? `订阅驱动 · ${formatSubscriptionTier(provider.ID, provider.AccountPlan || "")}` : `Subscription · ${formatSubscriptionTier(provider.ID, provider.AccountPlan || "")}`} control={
				<button type="button" role="switch" aria-checked={provider.Enabled} aria-label={provider.Enabled ? t("logout") : t("login")} className="provider-state-control" disabled={working} onClick={() => void act(provider.Enabled ? "logout" : "login")}>
					<span className="provider-state-copy"><strong>{provider.Enabled ? (language === "zh-CN" ? "已连接" : "Connected") : (language === "zh-CN" ? "未连接" : "Disconnected")}</strong><small>{provider.Enabled ? (language === "zh-CN" ? "订阅账户可用" : "Subscription ready") : t("subscriptionLoginRequired")}</small></span>
					<span className={`settings-switch ${provider.Enabled ? "on" : ""}`} aria-hidden="true"><span /></span>
				</button>
			} />
			<div className="subscription-provider-body">
				<div><small>{language === "zh-CN" ? "身份验证" : "Authentication"}</small><strong><i />{provider.Enabled ? `${language === "zh-CN" ? "已连接" : "Connected"} · ${provider.AccountLabel}` : t("subscriptionLoginRequired")}</strong></div>
				<div><small>{language === "zh-CN" ? "API 地址" : "API address"}</small><strong>{language === "zh-CN" ? "由订阅驱动管理" : "Managed by subscription driver"}<em>{language === "zh-CN" ? "只读" : "Read only"}</em></strong></div>
			</div>
			{provider.Enabled && <SubscriptionQuota provider={provider} language={language} />}
		</section>
		<ProviderModelCatalog
			provider={provider}
			models={models}
			enabledCount={enabledCount}
			description={language === "zh-CN" ? "订阅服务实时同步；启用后可在模型路由中分配用途。" : t("subscriptionModelsHint")}
			language={language}
			workingModel={workingModel}
			onToggle={toggleModel}
			badgesFor={subscriptionModelCapabilityBadges}
			emptyLabel={provider.Enabled ? t("subscriptionModelsEmpty") : t("subscriptionLoginHint")}
			showModels={provider.Enabled}
		/>
	</div>;
}

function ProviderHeader({ provider, detail, control }: { provider: ModelProvider; detail: string; control: ReactNode }) {
	return <header className="provider-workspace-header">
		<div className="provider-heading"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} size={22} /><span><strong>{provider.DisplayName}</strong><small>{detail}</small></span></div>
		{control}
	</header>;
}

type ModelCardItem = Pick<LLMuxModelConfig, "id" | "name" | "disabled" | "reasoningLevels" | "capabilities" | "inputModalities" | "outputModalities">;

type ModelBadges = (model: ModelCardItem, language: Language) => CapabilityBadge[];

function ModelCards({ provider, models, language, workingModel, onToggle, badgesFor, emptyLabel }: { provider: ModelProvider; models: ModelCardItem[]; language: Language; workingModel: string; onToggle: (model: ModelCardItem, index: number) => void; badgesFor: ModelBadges; emptyLabel: string }) {
	const routes = useRuntimeStore((state) => state.modelRoutes);
	const activeProvider = useRuntimeStore((state) => state.snapshot?.provider);
	const activeModel = useRuntimeStore((state) => state.snapshot?.model);
	if (models.length === 0) return <small className="subscription-models-empty">{emptyLabel}</small>;
	return <div className="subscription-models provider-model-grid">{models.map((model, index) => <ModelCard key={`${index}-${model.id}`} provider={provider} model={model} index={index} language={language} working={workingModel === `${index}:${model.id}`} badges={badgesFor(model, language)} useLabel={modelUseLabel(provider.ID, model.id, routes, activeProvider, activeModel, language)} onToggle={onToggle} />)}</div>;
}

function ModelCard({ provider, model, index, language, working, badges, useLabel, onToggle }: { provider: ModelProvider; model: ModelCardItem; index: number; language: Language; working: boolean; badges: CapabilityBadge[]; useLabel: string; onToggle: (model: ModelCardItem, index: number) => void }) {
	const t = translator(language);
	const name = modelDisplayName(model.id, model.name);
	const [openCapability, setOpenCapability] = useState("");
	return <article className="subscription-model provider-model-card model-card" data-disabled={Boolean(model.disabled)} data-working={working}>
		<div className="provider-model-card-main">
			<div className="provider-model-identity"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} /><span><strong>{name}</strong></span></div>
			<div className="provider-model-state">{useLabel && <em className="model-use-badge">{useLabel}</em>}<button className="model-card-toggle" disabled={!model.id || working} onClick={() => onToggle(model, index)} aria-pressed={!model.disabled} aria-label={tFormat(language, model.disabled ? "enableModel" : "disableModel", { model: name })}><span className="model-toggle-label">{model.disabled ? (language === "zh-CN" ? "未启用" : "Disabled") : (language === "zh-CN" ? "已启用" : "Enabled")}</span><span className={`settings-switch ${model.disabled ? "" : "on"}`} aria-hidden="true"><span /></span></button></div>
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

function ProviderModelCatalog({ provider, models, enabledCount, description, warning, language, workingModel, onToggle, badgesFor, emptyLabel, showModels = true, action }: { provider: ModelProvider; models: ModelCardItem[]; enabledCount: number; description: string; warning?: string; language: Language; workingModel: string; onToggle: (model: ModelCardItem, index: number) => void; badgesFor: ModelBadges; emptyLabel: string; showModels?: boolean; action?: ReactNode }) {
	return <section className="provider-model-catalog subscription-model-note settings-card">
		<header className="provider-models-header"><div><strong>{language === "zh-CN" ? "模型" : "Models"}</strong><small>{description}</small>{warning && <small className="provider-model-warning">{warning}</small>}</div><div className="provider-model-summary"><em><b>{enabledCount}</b> / {models.length} {language === "zh-CN" ? "已启用" : "enabled"}</em>{action}</div></header>
		<div className="provider-models">{showModels ? <ModelCards provider={provider} models={models} language={language} workingModel={workingModel} onToggle={onToggle} badgesFor={badgesFor} emptyLabel={emptyLabel} /> : <small className="subscription-models-empty">{emptyLabel}</small>}</div>
	</section>;
}

function modelUseLabel(providerID: string, modelID: string, routes: ModelRoute[], activeProvider: string | undefined, activeModel: string | undefined, language: Language) {
	if (providerID === activeProvider && modelID === activeModel) return language === "zh-CN" ? "当前主模型" : "Current";
	const matchingRoutes = routes.filter((route) => route.Route.provider === providerID && route.Route.model === modelID);
	const scope = ["main", "approval", "plan", "title", "vision", "compaction", "subagent"].find((candidate) => matchingRoutes.some((route) => route.Scope === candidate));
	if (!scope) return "";
	const labels = language === "zh-CN"
		? { main: "主模型", approval: "审批", plan: "规划", title: "标题", vision: "视觉", compaction: "压缩", subagent: "子智能体" }
		: { main: "Main", approval: "Approval", plan: "Plan", title: "Titles", vision: "Vision", compaction: "Compaction", subagent: "Subagent" };
	return labels[scope as keyof typeof labels];
}

function providerDirectoryDetail(provider: ModelProvider, language: Language) {
	if (provider.Subscription) {
		const plan = formatSubscriptionTier(provider.ID, provider.AccountPlan || "");
		if (provider.ID === "chatgpt") return [plan, provider.QuotaBalance && `${language === "zh-CN" ? "额外" : "Extra"} ${provider.QuotaBalance}`].filter(Boolean).join(" · ");
		return language === "zh-CN" ? "订阅驱动 · 额外额度可用" : "Subscription · Extra quota";
	}
	return `${provider.Backend === "openai_compat" ? "llmux" : provider.Backend} · ${provider.Models.length} ${language === "zh-CN" ? "个模型" : "models"}`;
}

function providerDirectoryStatus(provider: ModelProvider, language: Language) {
	if (provider.QuotaAvailable) return `${Math.max(0, 100-(provider.QuotaUsedPercent ?? 0)).toLocaleString(language, { maximumFractionDigits: 1 })}%`;
	return provider.Enabled ? "" : language === "zh-CN" ? "未启用" : "Disabled";
}

function SubscriptionQuota({ provider, language }: { provider: ModelProvider; language: Language }) {
	const t = translator(language);
	const remaining = Math.max(0, Math.min(100, 100-(provider.QuotaUsedPercent ?? 0)));
	const formatted = remaining.toLocaleString(language, { maximumFractionDigits: 1 });
	return <section className="subscription-quota" aria-label={t("subscriptionQuota")}>
		<div className="subscription-quota-heading"><strong>{t("subscriptionQuota")}</strong><small>{language === "zh-CN" ? "来自订阅服务的实时额度" : t("subscriptionQuotaHint")}</small></div>
		{provider.QuotaAvailable || provider.QuotaUnlimited || provider.QuotaBalance ? <div className="subscription-quota-grid">
			{provider.QuotaAvailable && <div className="subscription-quota-item">
				<div><strong>{t("weeklyQuota")}</strong><span>{tFormat(language, "quotaRemaining", { percent: formatted })}</span></div>
				<div className="subscription-quota-track" role="progressbar" aria-label={t("weeklyQuota")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={remaining}><span style={{ width: `${remaining}%` }} /></div>
				{Boolean(provider.QuotaResetsAt) && <small>{tFormat(language, "quotaResetsAt", { time: formatQuotaReset(provider.QuotaResetsAt!*1000, language) })}</small>}
			</div>}
			{(provider.QuotaUnlimited || provider.QuotaBalance) && <div className="subscription-quota-item subscription-quota-balance"><div><strong>{t("quotaBalance")}</strong><span>{provider.QuotaUnlimited ? t("quotaUnlimited") : formatQuotaBalance(provider.QuotaBalance!, language)}</span><small>{language === "zh-CN" ? (provider.QuotaUnlimited ? "当前订阅账户可用" : "用完每周额度后继续使用") : "Available after the weekly allowance"}</small></div></div>}
		</div> : !provider.QuotaWarning && <small className="subscription-quota-empty">{t("quotaUnavailable")}</small>}
		{provider.QuotaWarning && <small className="provider-model-warning" role="status">{t("quotaUnavailable")}</small>}
	</section>;
}

function formatQuotaBalance(value: string, language: Language) {
	const amount = Number(value);
	return Number.isFinite(amount) ? new Intl.NumberFormat(language, { style: "currency", currency: "USD" }).format(amount) : value;
}

function formatQuotaReset(timestamp: number, language: Language) {
	const value = new Date(timestamp);
	if (language === "zh-CN") return `${value.getMonth()+1} 月 ${value.getDate()} 日 ${String(value.getHours()).padStart(2, "0")}:${String(value.getMinutes()).padStart(2, "0")}`;
	return new Intl.DateTimeFormat(language, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(value);
}

function formatSubscriptionTier(provider: string, plan: string) {
	if (provider === "chatgpt" && plan.toLowerCase() === "pro") return "Pro 20x";
	return ({ prolite: "Pro 5x", plus: "Plus", team: "Team", business: "Business", enterprise: "Enterprise", free: "Free" } as Record<string, string>)[plan.toLowerCase()] ?? plan;
}

function ProviderEditor({ provider, sessionId, language, setError, creating = false }: { provider: ModelProvider; sessionId: string; language: Language; setError: (message: string) => void; creating?: boolean }) {
	const t = translator(language);
	const [draft, setDraft] = useState<ModelProvider>(() => cloneProvider(provider));
	const [secret, setSecret] = useState("");
	const [saving, setSaving] = useState(false);
	const [discovering, setDiscovering] = useState(false);
	const [workingModel, setWorkingModel] = useState("");
	useEffect(() => setDraft(cloneProvider(provider)), [provider]);
	const updateModel = (index: number, update: Partial<LLMuxModelConfig>) => setDraft((current) => ({ ...current, Models: current.Models.map((model, currentIndex) => currentIndex === index ? { ...model, ...update } : model) }));
	const toggleModel = async (model: ModelCardItem, index: number) => {
		const enabled = Boolean(model.disabled);
		const key = `${index}:${model.id}`;
		setWorkingModel(key);
		updateModel(index, { disabled: !enabled });
		try { await execute({ kind: "set_model_enabled", sessionId, target: provider.ID, name: model.id, decision: String(enabled) }); }
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
	const modelDescription = provider.ModelsSource?.startsWith("provider_api") ? tFormat(language, provider.ModelsSource.includes("models.dev") ? "discoveredModels" : "discoveredAPIModels", { count: draft.Models.length }) : t("providerModelsHint");
	return <div className="provider-workspace">
		<section className="provider-editor provider-overview settings-card">
			<ProviderHeader provider={{ ...provider, DisplayName: creating ? (language === "zh-CN" ? "添加提供方" : "Add provider") : provider.DisplayName }} detail={creating ? "OpenAI compatible API" : `${provider.Backend} · ${provider.ID}`} control={
				<label className="provider-state-control provider-switch"><span className="provider-state-copy"><strong>{draft.Enabled ? (language === "zh-CN" ? "已启用" : "Enabled") : (language === "zh-CN" ? "未启用" : "Disabled")}</strong><small>{t("enableProvider")}</small></span><input type="checkbox" checked={draft.Enabled} onChange={(event) => setDraft({ ...draft, Enabled: event.target.checked })} /><span className={`settings-switch ${draft.Enabled ? "on" : ""}`} aria-hidden="true"><span /></span></label>
			} />
			<div className={`provider-fields ${creating ? "provider-fields-creating" : ""}`}>
				{creating && <><label><span>{language === "zh-CN" ? "提供方名称" : "Provider name"}</span><input value={draft.DisplayName} onChange={(event) => setDraft({ ...draft, DisplayName: event.target.value })} placeholder="Acme AI" /></label><label><span>{language === "zh-CN" ? "提供方 ID" : "Provider ID"}</span><input value={draft.ID} onChange={(event) => setDraft({ ...draft, ID: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "") })} placeholder="acme" /></label></>}
				<label><span>{t("apiBaseURL")}</span><input value={draft.BaseURL} readOnly={Boolean(provider.DefaultBaseURL)} onChange={(event) => setDraft({ ...draft, BaseURL: event.target.value })} placeholder={provider.DefaultBaseURL || t("customAPIBaseURL")} title={provider.DefaultBaseURL ? t("officialAPIAddressLocked") : t("customAPIAddressHint")} /><small>{provider.DefaultBaseURL ? t("officialAPIAddressLocked") : t("customAPIAddressHint")}</small></label>
				<label><span>{t("apiKey")}</span><input type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder={provider.CredentialConfigured ? t("keepCredential") : provider.EnvKey} /><small>{credentialHint(provider, language)}</small></label>
			</div>
			<footer><button className="small-button primary" disabled={saving || !draft.ID.trim() || !draft.DisplayName.trim() || !draft.BaseURL.trim()} onClick={() => void save()}>{saving ? t("saving") : t("saveProvider")}</button></footer>
		</section>
		<ProviderModelCatalog provider={provider} models={draft.Models} enabledCount={draft.Models.filter((model) => !model.disabled).length} description={modelDescription} warning={provider.ModelsWarning} language={language} workingModel={workingModel} onToggle={toggleModel} badgesFor={modelCapabilityBadges} emptyLabel={t("noConfiguredModels")} action={<div className="provider-model-actions"><button className="small-button" disabled={!draft.Enabled || discovering} onClick={() => void discover()}><RefreshCw size={14} />{discovering ? t("discoveringModels") : t("discoverModels")}</button></div>} />
	</div>;
}

function blankProvider(language: Language): ModelProvider {
	return {
		ID: "custom", DisplayName: language === "zh-CN" ? "自定义提供方" : "Custom provider", Backend: "openai_compat",
		DefaultBaseURL: "", BaseURL: "", EnvKey: "CUSTOM_API_KEY", Enabled: false,
		CredentialConfigured: false, CredentialSource: "none", Models: [],
	};
}

function cloneProvider(provider: ModelProvider): ModelProvider { return { ...provider, Models: provider.Models.map((model) => ({ ...model, aliases: [...(model.aliases ?? [])], reasoningLevels: [...(model.reasoningLevels ?? [])], capabilities: [...(model.capabilities ?? [])], inputModalities: [...(model.inputModalities ?? [])], outputModalities: [...(model.outputModalities ?? [])] })) }; }

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
	if (provider.CredentialSource === "stored") return t("credentialStored");
	if (provider.CredentialSource === "environment") return tFormat(language, "credentialEnvironment", { key: provider.EnvKey });
	if (provider.CredentialSource === "pending") return t("credentialPending");
	if (provider.CredentialConfigured) return t("credentialNotRequired");
	return tFormat(language, "credentialMissing", { key: provider.EnvKey });
}
