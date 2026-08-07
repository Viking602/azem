import { useEffect, useState } from "react";
import {
	AudioLines, Braces, Brain, Gauge, Image as ImageIcon, Layers, LogIn, LogOut, MessageSquareText,
	Plus, RefreshCw, Search, Trash2, Type, Wrench, Zap,
} from "lucide-react";
import { execute } from "../bridge";
import { reasoningLabel, sortReasoningLevels, tFormat, translator, type Language } from "../i18n";
import type { ModelOption } from "../store";
import type { LLMuxModelConfig, ModelProvider } from "../types";
import MenuSelect from "./MenuSelect";
import ProviderIcon from "./ProviderIcon";

const PROVIDER_PAGE_SIZE = 24;

export default function ModelProviderSettings({ providers, modelsByProvider, sessionId, language, setError }: { providers: ModelProvider[]; modelsByProvider: Record<string, ModelOption[]>; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [query, setQuery] = useState("");
	const [visibleCount, setVisibleCount] = useState(PROVIDER_PAGE_SIZE);
	const [loading, setLoading] = useState(providers.length === 0);
	const [loadError, setLoadError] = useState("");
	const configured = providers.filter((provider) => provider.Enabled || provider.Models.length > 0);
	const filtered = providers.filter((provider) => `${provider.DisplayName} ${provider.ID}`.toLowerCase().includes(query.trim().toLowerCase()));
	const visible = query.trim() ? filtered : [...configured, ...providers.filter((provider) => !configured.includes(provider)).slice(0, visibleCount)];
	const [selectedID, setSelectedID] = useState("");
	const selected = providers.find((provider) => provider.ID === selectedID) ?? configured[0] ?? providers[0];
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
				<label className="provider-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchProviders")} disabled={loading && providers.length === 0} /></label>
				<div className="provider-list" onScroll={(event) => loadMore(event.currentTarget)}>
					{visible.map((provider) => <button key={provider.ID} className={selected?.ID === provider.ID ? "active" : ""} onClick={() => setSelectedID(provider.ID)}><span className="provider-identity"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} /><span><strong>{provider.DisplayName}</strong><small>{provider.ID}</small></span></span><em className={provider.Enabled ? "enabled" : ""}>{provider.Subscription ? (provider.Enabled ? t("signedIn") : t("notSignedIn")) : provider.Enabled ? t("enabled") : provider.Backend}</em></button>)}
					{providers.length === 0 && <div className="settings-empty provider-list-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
				</div>
				{!query.trim() && providers.length > visible.length && <small className="provider-more" aria-live="polite">{tFormat(language, "moreProviders", { count: providers.length - visible.length })}</small>}
			</div>
			{selected ? selected.Subscription ? <SubscriptionProviderEditor key={selected.ID} provider={selected} models={modelsByProvider[selected.ID] ?? []} sessionId={sessionId} language={language} setError={setError} /> : <ProviderEditor key={selected.ID} provider={selected} sessionId={sessionId} language={language} setError={setError} /> : <div className="settings-card settings-empty" aria-live="polite">{loading && <span className="azem-mark" aria-hidden="true" />}{emptyMessage}</div>}
		</div>
	</section>;
}

function SubscriptionProviderEditor({ provider, models, sessionId, language, setError }: { provider: ModelProvider; models: ModelOption[]; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [working, setWorking] = useState(false);
	const act = async (kind: "login" | "logout") => {
		setWorking(true);
		try { await execute({ kind, sessionId, target: kind === "login" ? provider.ID : `${provider.ID}/${provider.AccountID}` }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setWorking(false); }
	};
	return <div className="provider-editor subscription-provider settings-card">
		<header><div className="provider-heading"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} size={20} /><span><strong>{provider.DisplayName}</strong><small>{t("subscriptionProvider")}</small></span></div><em className={provider.Enabled ? "enabled" : ""}>{provider.Enabled ? t("signedIn") : t("notSignedIn")}</em></header>
		<div className="subscription-provider-body">
			<div><strong>{provider.Enabled ? provider.AccountLabel : t("subscriptionLoginRequired")}</strong><small>{provider.Enabled ? [provider.AccountPlan && `${t("subscriptionTier")} ${formatSubscriptionTier(provider.ID, provider.AccountPlan)}`, provider.AccountID].filter(Boolean).join(" · ") : t("subscriptionLoginHint")}</small></div>
			<button className={`small-button ${provider.Enabled ? "" : "primary"}`} disabled={working} onClick={() => void act(provider.Enabled ? "logout" : "login")}>{provider.Enabled ? <LogOut size={13} /> : <LogIn size={13} />}{working ? t("working") : provider.Enabled ? t("logout") : t("login")}</button>
		</div>
		{provider.Enabled && <SubscriptionQuota provider={provider} language={language} />}
		<div className="subscription-model-note"><strong>{t("models")}</strong><small>{t("subscriptionModelsHint")}</small>
			{provider.Enabled && (models.length > 0 ? <div className="subscription-models">{models.map((model) => <div className="subscription-model" key={model.id}>
				<ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} />
				<span><strong>{model.name}</strong><small>{model.id}</small></span>
				<div className="subscription-model-capabilities" role="list" aria-label={t("modelCapabilities")}>{subscriptionModelCapabilityBadges(model, language).map((badge) => {
					const Icon = badge.icon;
					return <span key={badge.key} className="model-icon-badge" role="listitem" title={badge.label} aria-label={badge.label}><Icon size={13} aria-hidden="true" /></span>;
				})}</div>
			</div>)}</div> : <small className="subscription-models-empty">{t("subscriptionModelsEmpty")}</small>)}
		</div>
	</div>;
}

function SubscriptionQuota({ provider, language }: { provider: ModelProvider; language: Language }) {
	const t = translator(language);
	const remaining = Math.max(0, Math.min(100, 100-(provider.QuotaUsedPercent ?? 0)));
	const formatted = remaining.toLocaleString(language, { maximumFractionDigits: 1 });
	return <section className="subscription-quota" aria-label={t("subscriptionQuota")}>
		<div className="subscription-quota-heading"><strong>{t("subscriptionQuota")}</strong><small>{t("subscriptionQuotaHint")}</small></div>
		{provider.QuotaAvailable || provider.QuotaUnlimited || provider.QuotaBalance ? <div className="subscription-quota-grid">
			{provider.QuotaAvailable && <div className="subscription-quota-item">
				<div><strong>{t("weeklyQuota")}</strong><span>{tFormat(language, "quotaRemaining", { percent: formatted })}</span></div>
				<div className="subscription-quota-track" role="progressbar" aria-label={t("weeklyQuota")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={remaining}><span style={{ width: `${remaining}%` }} /></div>
				{Boolean(provider.QuotaResetsAt) && <small>{tFormat(language, "quotaResetsAt", { time: new Intl.DateTimeFormat(language, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(provider.QuotaResetsAt!*1000) })}</small>}
			</div>}
			{(provider.QuotaUnlimited || provider.QuotaBalance) && <div className="subscription-quota-item subscription-quota-balance"><div><strong>{t("quotaBalance")}</strong><span>{provider.QuotaUnlimited ? t("quotaUnlimited") : formatQuotaBalance(provider.QuotaBalance!, language)}</span></div></div>}
		</div> : !provider.QuotaWarning && <small className="subscription-quota-empty">{t("quotaUnavailable")}</small>}
		{provider.QuotaWarning && <small className="provider-model-warning" role="status">{t("quotaUnavailable")}</small>}
	</section>;
}

function formatQuotaBalance(value: string, language: Language) {
	const amount = Number(value);
	return Number.isFinite(amount) ? new Intl.NumberFormat(language, { style: "currency", currency: "USD" }).format(amount) : value;
}

function formatSubscriptionTier(provider: string, plan: string) {
	if (provider === "chatgpt" && plan.toLowerCase() === "pro") return "Pro 20x";
	return ({ prolite: "Pro 5x", plus: "Plus", team: "Team", business: "Business", enterprise: "Enterprise", free: "Free" } as Record<string, string>)[plan.toLowerCase()] ?? plan;
}

function ProviderEditor({ provider, sessionId, language, setError }: { provider: ModelProvider; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [draft, setDraft] = useState<ModelProvider>(() => cloneProvider(provider));
	const [secret, setSecret] = useState("");
	const [saving, setSaving] = useState(false);
	const [discovering, setDiscovering] = useState(false);
	useEffect(() => setDraft(cloneProvider(provider)), [provider]);
	const updateModel = (index: number, update: Partial<LLMuxModelConfig>) => setDraft((current) => ({ ...current, Models: current.Models.map((model, currentIndex) => currentIndex === index ? { ...model, ...update } : model) }));
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
	return <div className="provider-editor settings-card">
		<header><div className="provider-heading"><ProviderIcon provider={provider.ID} logoID={provider.ModelsDevID} size={20} /><span><strong>{provider.DisplayName}</strong><small>{provider.Backend} · {provider.ID}</small></span></div><label className="provider-switch"><input type="checkbox" checked={draft.Enabled} onChange={(event) => setDraft({ ...draft, Enabled: event.target.checked })} /><span>{t("enableProvider")}</span></label></header>
		<div className="provider-fields">
			<label><span>{t("apiBaseURL")}</span><input value={draft.BaseURL} readOnly={Boolean(provider.DefaultBaseURL)} onChange={(event) => setDraft({ ...draft, BaseURL: event.target.value })} placeholder={provider.DefaultBaseURL || t("customAPIBaseURL")} title={provider.DefaultBaseURL ? t("officialAPIAddressLocked") : t("customAPIAddressHint")} /><small>{provider.DefaultBaseURL ? t("officialAPIAddressLocked") : t("customAPIAddressHint")}</small></label>
			<label><span>{t("apiKey")}</span><input type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder={provider.CredentialConfigured ? t("keepCredential") : provider.EnvKey} /><small>{credentialHint(provider, language)}</small></label>
		</div>
		<div className="provider-models-header"><div><strong>{t("models")}</strong><small>{provider.ModelsSource?.startsWith("provider_api") ? tFormat(language, provider.ModelsSource.includes("models.dev") ? "discoveredModels" : "discoveredAPIModels", { count: draft.Models.length }) : t("providerModelsHint")}</small>{provider.ModelsWarning && <small className="provider-model-warning">{provider.ModelsWarning}</small>}</div><div className="provider-model-actions"><button className="small-button" disabled={!draft.Enabled || discovering} onClick={() => void discover()}><RefreshCw size={13} />{discovering ? t("discoveringModels") : t("discoverModels")}</button><button className="small-button" onClick={() => setDraft({ ...draft, Models: [...draft.Models, emptyModel()] })}><Plus size={13} />{t("addModel")}</button></div></div>
		<div className="provider-models">{draft.Models.length === 0 ? <div className="settings-empty">{t("noConfiguredModels")}</div> : draft.Models.map((model, index) => {
			const levels = sortReasoningLevels(model.reasoningLevels ?? []);
			const defaultReasoning = model.defaultReasoning ?? "";
			const defaultOptions = [
				{ value: "", label: t("noDefaultReasoning") },
				...levels.map((level) => ({ value: level, label: reasoningLabel(level, language) })),
			];
			if (defaultReasoning && !levels.includes(defaultReasoning)) {
				defaultOptions.push({ value: defaultReasoning, label: reasoningLabel(defaultReasoning, language) });
			}
			return <div className="provider-model-row" key={`${index}-${model.id}`}>
				<label><span>{t("modelID")}</span><input value={model.id} onChange={(event) => updateModel(index, { id: event.target.value })} placeholder="provider/model-name" /></label>
				<label><span>{t("displayName")}</span><input value={model.name ?? ""} onChange={(event) => updateModel(index, { name: event.target.value })} /></label>
				<label><span>{t("contextWindow")}</span><input type="number" min={1024} max={10000000} value={model.contextWindow} onChange={(event) => updateModel(index, { contextWindow: Number(event.target.value) })} /></label>
				<label><span>{t("maxOutputTokens")}</span><input type="number" min={0} max={10000000} value={model.maxOutputTokens ?? 0} onChange={(event) => updateModel(index, { maxOutputTokens: Number(event.target.value) })} /></label>
				<label className="provider-model-levels"><span>{t("reasoningLevels")}</span>
					<div className="model-icon-badges" role="list" aria-label={t("reasoningLevels")}>
						{levels.length === 0
							? <span className="model-icon-badge muted" title={t("noReasoningLevels")} aria-label={t("noReasoningLevels")}><Brain size={13} aria-hidden="true" /></span>
							: levels.map((level) => {
								const Icon = reasoningLevelIcon(level);
								const label = reasoningLabel(level, language);
								return <span key={level} className="model-icon-badge" role="listitem" title={label} aria-label={label}><Icon size={13} aria-hidden="true" /></span>;
							})}
					</div>
					<input className="provider-levels-edit" value={(model.reasoningLevels ?? []).join(", ")} onChange={(event) => {
						const next = splitLevels(event.target.value);
						const nextDefault = model.defaultReasoning && next.includes(model.defaultReasoning) ? model.defaultReasoning : (next[0] ?? "");
						updateModel(index, { reasoningLevels: next, defaultReasoning: nextDefault });
					}} placeholder="low, medium, high" aria-label={t("reasoningLevels")} />
				</label>
				<label className="provider-model-default"><span>{t("defaultReasoning")}</span>
					<MenuSelect className="route-menu provider-default-menu" value={defaultReasoning} options={defaultOptions} onChange={(value) => updateModel(index, { defaultReasoning: value })} ariaLabel={t("defaultReasoning")} disabled={levels.length === 0} />
				</label>
				<button className="icon-button provider-remove" aria-label={t("removeModel")} onClick={() => setDraft({ ...draft, Models: draft.Models.filter((_, currentIndex) => currentIndex !== index) })}><Trash2 size={14} /></button>
				<div className="model-capabilities" role="list" aria-label={t("modelCapabilities")}>
					{modelCapabilityBadges(model, language).map((badge) => {
						const Icon = badge.icon;
						return <span key={badge.key} className="model-icon-badge" role="listitem" title={badge.label} aria-label={badge.label}><Icon size={13} aria-hidden="true" /></span>;
					})}
				</div>
			</div>;
		})}</div>
		<footer><button className="small-button primary" disabled={saving} onClick={() => void save()}>{saving ? t("saving") : t("saveProvider")}</button></footer>
	</div>;
}

function cloneProvider(provider: ModelProvider): ModelProvider { return { ...provider, Models: provider.Models.map((model) => ({ ...model, aliases: [...(model.aliases ?? [])], reasoningLevels: [...(model.reasoningLevels ?? [])], capabilities: [...(model.capabilities ?? [])], inputModalities: [...(model.inputModalities ?? [])], outputModalities: [...(model.outputModalities ?? [])] })) }; }
function emptyModel(): LLMuxModelConfig { return { id: "", name: "", aliases: [], contextWindow: 128000, maxOutputTokens: 0, reasoningLevels: [], defaultReasoning: "", capabilities: [], inputModalities: [], outputModalities: [] }; }
function splitLevels(value: string) { return [...new Set(value.split(",").map((level) => level.trim()).filter(Boolean))]; }

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
	for (const modality of model.inputModalities ?? []) {
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

function subscriptionModelCapabilityBadges(model: ModelOption, language: Language): CapabilityBadge[] {
	const defaults = modelCapabilityBadges({ capabilities: ["tools", "reasoning", "structured-output"], inputModalities: ["text"], outputModalities: ["text"] }, language);
	const badges = new Map(defaults.map((badge) => [badge.key, badge]));
	for (const badge of modelCapabilityBadges(model, language)) badges.set(badge.key, badge);
	return [...badges.values()];
}

function reasoningLevelIcon(level: string): typeof Brain {
	switch (level) {
		case "minimal":
		case "low":
			return Gauge;
		case "medium":
			return Brain;
		case "high":
		case "xhigh":
		case "max":
		case "ultra":
			return Zap;
		default:
			return Brain;
	}
}
function credentialHint(provider: ModelProvider, language: Language) {
	const t = translator(language);
	if (provider.CredentialSource === "stored") return t("credentialStored");
	if (provider.CredentialSource === "environment") return tFormat(language, "credentialEnvironment", { key: provider.EnvKey });
	if (provider.CredentialSource === "pending") return t("credentialPending");
	if (provider.CredentialConfigured) return t("credentialNotRequired");
	return tFormat(language, "credentialMissing", { key: provider.EnvKey });
}
