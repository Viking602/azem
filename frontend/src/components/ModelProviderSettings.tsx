import { useEffect, useState } from "react";
import { Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { execute } from "../bridge";
import { tFormat, translator, type Language } from "../i18n";
import type { LLMuxModelConfig, ModelProvider } from "../types";
import ProviderIcon from "./ProviderIcon";

export default function ModelProviderSettings({ providers, sessionId, language, setError }: { providers: ModelProvider[]; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [query, setQuery] = useState("");
	const configured = providers.filter((provider) => provider.Enabled || provider.Models.length > 0);
	const filtered = providers.filter((provider) => `${provider.DisplayName} ${provider.ID}`.toLowerCase().includes(query.trim().toLowerCase()));
	const visible = query.trim() ? filtered : [...configured, ...providers.filter((provider) => !configured.includes(provider)).slice(0, 24)];
	const [selectedID, setSelectedID] = useState("");
	const selected = providers.find((provider) => provider.ID === selectedID) ?? configured[0] ?? providers[0];
	useEffect(() => {
		if (!selectedID && selected) setSelectedID(selected.ID);
	}, [selected, selectedID]);
	const refresh = async () => {
		try { await execute({ kind: "list_model_providers", sessionId }); }
		catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
	};
	return <section className="settings-pane"><header><div><h2>{t("modelSettings")}</h2><p>{t("modelSettingsHint")}</p></div><button className="small-button" onClick={() => void refresh()}><RefreshCw size={13} />{t("refresh")}</button></header>
		<div className="provider-settings">
			<div className="provider-directory settings-card">
				<label className="provider-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchProviders")} /></label>
				<div className="provider-list">{visible.map((provider) => <button key={provider.ID} className={selected?.ID === provider.ID ? "active" : ""} onClick={() => setSelectedID(provider.ID)}><span className="provider-identity"><ProviderIcon provider={provider.ID} /><span><strong>{provider.DisplayName}</strong><small>{provider.ID}</small></span></span><em className={provider.Enabled ? "enabled" : ""}>{provider.Enabled ? t("enabled") : provider.Backend}</em></button>)}</div>
				{!query.trim() && providers.length > visible.length && <small className="provider-more">{tFormat(language, "searchMoreProviders", { count: providers.length - visible.length })}</small>}
			</div>
			{selected ? <ProviderEditor key={selected.ID} provider={selected} sessionId={sessionId} language={language} setError={setError} /> : <div className="settings-card settings-empty">{t("noProviders")}</div>}
		</div>
	</section>;
}

function ProviderEditor({ provider, sessionId, language, setError }: { provider: ModelProvider; sessionId: string; language: Language; setError: (message: string) => void }) {
	const t = translator(language);
	const [draft, setDraft] = useState<ModelProvider>(() => cloneProvider(provider));
	const [secret, setSecret] = useState("");
	const [saving, setSaving] = useState(false);
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
	return <div className="provider-editor settings-card">
		<header><div className="provider-heading"><ProviderIcon provider={provider.ID} size={20} /><span><strong>{provider.DisplayName}</strong><small>{provider.Backend} · {provider.ID}</small></span></div><label className="provider-switch"><input type="checkbox" checked={draft.Enabled} onChange={(event) => setDraft({ ...draft, Enabled: event.target.checked })} /><span>{t("enableProvider")}</span></label></header>
		<div className="provider-fields">
			<label><span>{t("apiBaseURL")}</span><input value={draft.BaseURL} onChange={(event) => setDraft({ ...draft, BaseURL: event.target.value })} placeholder={provider.DefaultBaseURL} /></label>
			<label><span>{t("apiKey")}</span><input type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder={provider.CredentialConfigured ? t("keepCredential") : provider.EnvKey} /><small>{credentialHint(provider, language)}</small></label>
		</div>
		<div className="provider-models-header"><div><strong>{t("models")}</strong><small>{t("providerModelsHint")}</small></div><button className="small-button" onClick={() => setDraft({ ...draft, Models: [...draft.Models, emptyModel()] })}><Plus size={13} />{t("addModel")}</button></div>
		<div className="provider-models">{draft.Models.length === 0 ? <div className="settings-empty">{t("noConfiguredModels")}</div> : draft.Models.map((model, index) => <div className="provider-model-row" key={`${index}-${model.id}`}>
			<label><span>{t("modelID")}</span><input value={model.id} onChange={(event) => updateModel(index, { id: event.target.value })} placeholder="provider/model-name" /></label>
			<label><span>{t("displayName")}</span><input value={model.name ?? ""} onChange={(event) => updateModel(index, { name: event.target.value })} /></label>
			<label><span>{t("contextWindow")}</span><input type="number" min={1024} max={10000000} value={model.contextWindow} onChange={(event) => updateModel(index, { contextWindow: Number(event.target.value) })} /></label>
			<label><span>{t("reasoningLevels")}</span><input value={(model.reasoningLevels ?? []).join(", ")} onChange={(event) => updateModel(index, { reasoningLevels: splitLevels(event.target.value) })} placeholder="low, medium, high" /></label>
			<label><span>{t("defaultReasoning")}</span><input value={model.defaultReasoning ?? ""} onChange={(event) => updateModel(index, { defaultReasoning: event.target.value })} /></label>
			<button className="icon-button provider-remove" aria-label={t("removeModel")} onClick={() => setDraft({ ...draft, Models: draft.Models.filter((_, currentIndex) => currentIndex !== index) })}><Trash2 size={14} /></button>
		</div>)}</div>
		<footer><button className="small-button primary" disabled={saving || draft.Enabled && draft.Models.length === 0} onClick={() => void save()}>{saving ? t("saving") : t("saveProvider")}</button></footer>
	</div>;
}

function cloneProvider(provider: ModelProvider): ModelProvider { return { ...provider, Models: provider.Models.map((model) => ({ ...model, reasoningLevels: [...(model.reasoningLevels ?? [])] })) }; }
function emptyModel(): LLMuxModelConfig { return { id: "", name: "", contextWindow: 128000, reasoningLevels: [], defaultReasoning: "" }; }
function splitLevels(value: string) { return [...new Set(value.split(",").map((level) => level.trim()).filter(Boolean))]; }
function credentialHint(provider: ModelProvider, language: Language) {
	const t = translator(language);
	if (provider.CredentialSource === "stored") return t("credentialStored");
	if (provider.CredentialSource === "environment") return tFormat(language, "credentialEnvironment", { key: provider.EnvKey });
	if (provider.CredentialConfigured) return t("credentialNotRequired");
	return tFormat(language, "credentialMissing", { key: provider.EnvKey });
}
