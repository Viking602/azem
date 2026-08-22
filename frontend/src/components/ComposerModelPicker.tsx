import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Search, ShieldAlert, Zap } from "lucide-react";
import ProviderIcon from "./ProviderIcon";
import ReasoningEffortSlider from "./ReasoningEffortSlider";

export type ComposerPickerModel = {
  provider: string;
  id: string;
  name: string;
  aliases?: string[];
  reasoningLevels: string[];
  defaultReasoning?: string;
  capabilities?: string[];
  inputModalities?: string[];
  cursorVariantCount?: number;
  cursorTierCount?: number;
  cursorHasFast?: boolean;
  cursorNoZDR?: boolean;
};

type Props = {
  running: boolean;
  models: ComposerPickerModel[];
  selectedModel: string;
  selectedModelName: string;
  selectedProvider: string;
  reasoningLevels: string[];
  selectedReasoning: string;
  selectedReasoningName: string;
  fast: boolean;
  fastAvailable: boolean;
  reasoningNames: Record<string, string>;
  onModelChange: (value: string) => void;
  onReasoningChange: (value: string) => void;
  onSpeedChange: (value: string) => void;
  fasterLabel: string;
  smarterLabel: string;
  highCostHint: string;
  fastBoostTitle: string;
  fastBoostDetail: string;
  language: "en" | "zh-CN";
};

type Position = { left: number; bottom: number; width: number };

export default function ComposerModelPicker(props: Props) {
  const root = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const activationSequence = useRef(0);
  const pendingActivation = useRef(0);
  const activationResetTimer = useRef(0);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [position, setPosition] = useState<Position | null>(null);
  const zh = props.language === "zh-CN";
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleModels = normalizedQuery
    ? props.models.filter((model) => `${model.name} ${model.id} ${model.provider} ${(model.aliases ?? []).join(" ")}`.toLocaleLowerCase().includes(normalizedQuery))
    : props.models;
  const fastAvailable = props.fastAvailable;
  const cursorProvider = props.selectedProvider.trim().toLocaleLowerCase() === "cursor";
  const selectedModelInfo = props.models.find((model) => `${model.provider}/${model.id}` === props.selectedModel);
  const retentionLabel = zh ? "数据保留" : "Data retained";
  const retentionDetail = zh
    ? "NO ZDR：该模型不提供零数据保留；输入与输出可能由 Cursor 或模型提供方保存。"
    : "NO ZDR: this model has no zero-data-retention guarantee; Cursor or the model provider may retain inputs and outputs.";

  const place = useCallback(() => {
    const trigger = root.current?.querySelector<HTMLButtonElement>(".model-controls-trigger");
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const width = Math.min(336, window.innerWidth - 24);
    const left = Math.min(Math.max(12, rect.right - width), window.innerWidth - width - 12);
    setPosition({ left, bottom: window.innerHeight - rect.top + 8, width });
  }, []);

  useEffect(() => {
    if (!open) return;
    place();
    const reposition = () => place();
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [open, place]);

  useEffect(() => {
    const resetAfterRelease = () => {
      const sequence = pendingActivation.current;
      if (!sequence) return;
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = window.setTimeout(() => {
        if (pendingActivation.current === sequence) pendingActivation.current = 0;
        activationResetTimer.current = 0;
      }, 0);
    };
    const resetImmediately = () => {
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = 0;
      pendingActivation.current = 0;
    };
    document.addEventListener("pointerup", resetAfterRelease);
    document.addEventListener("mouseup", resetAfterRelease);
    document.addEventListener("pointercancel", resetImmediately, true);
    window.addEventListener("blur", resetImmediately);
    return () => {
      document.removeEventListener("pointerup", resetAfterRelease);
      document.removeEventListener("mouseup", resetAfterRelease);
      document.removeEventListener("pointercancel", resetImmediately, true);
      window.removeEventListener("blur", resetImmediately);
      resetImmediately();
    };
  }, []);

  const closePicker = useCallback(() => {
    setOpen(false);
    setQuery("");
  }, []);

  const togglePicker = useCallback(() => {
    if (props.running) return;
    if (open) {
      closePicker();
      return;
    }
    place();
    setOpen(true);
  }, [props.running, open, closePicker, place]);

  const beginActivation = useCallback(() => {
    if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
    activationResetTimer.current = 0;
    pendingActivation.current = ++activationSequence.current;
    togglePicker();
  }, [togglePicker]);

  const beginReleaseFallback = useCallback(() => {
    if (!pendingActivation.current) beginActivation();
  }, [beginActivation]);

  const completeClickActivation = useCallback(() => {
    if (pendingActivation.current) {
      pendingActivation.current = 0;
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = 0;
      return;
    }
    togglePicker();
  }, [togglePicker]);

  useLayoutEffect(() => {
    if (props.running && open) closePicker();
  }, [props.running, open, closePicker]);

  useLayoutEffect(() => {
    if (open && position) searchInput.current?.focus();
  }, [open, position]);

  useLayoutEffect(() => () => {
    const node = panel.current;
    if (!node) return;
    node.hidden = true;
    node.style.display = "none";
  }, []);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (root.current?.contains(target) || panel.current?.contains(target)) return;
      closePicker();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      closePicker();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    window.addEventListener("blur", closePicker);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("blur", closePicker);
    };
  }, [open, closePicker]);

  const choose = (model: ComposerPickerModel) => {
    props.onModelChange(`${model.provider}/${model.id}`);
    closePicker();
  };

  const popover = position ? createPortal(
    <section ref={panel} hidden={!open} data-open={String(open)} className="composer-model-popover" style={{ position: "fixed", left: position.left, bottom: position.bottom, width: position.width }} aria-label={zh ? "模型与思考" : "Model and reasoning"}>
      <header><strong>{zh ? "模型与思考" : "Model and reasoning"}</strong><small>{zh ? "仅应用到当前会话" : "Applies to this conversation"}</small></header>
      <label className="composer-model-search"><Search size={14} /><input ref={searchInput} autoFocus type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={zh ? "搜索模型或提供方" : "Search models or providers"} /><kbd>⌘F</kbd></label>
      <div className="composer-model-list" role="listbox">
        {visibleModels.map((model) => {
          const selected = `${model.provider}/${model.id}` === props.selectedModel;
          return <button key={`${model.provider}:${model.id}`} type="button" role="option" aria-selected={selected} className={selected ? "selected" : ""} onClick={() => choose(model)}>
            <ProviderIcon provider={model.provider} size={20} />
            <span>
              <span className="composer-model-name"><strong>{model.name}</strong>{model.cursorNoZDR && <em className="model-retention-warning" title={retentionDetail}><ShieldAlert size={11} />{retentionLabel}</em>}</span>
              <small>{providerLabel(model.provider)} · {modelHint(model, zh)}</small>
            </span>
            <Check size={14} />
          </button>;
        })}
        {!visibleModels.length && <p>{zh ? "没有匹配的模型" : "No matching models"}</p>}
      </div>
      <section className="composer-effort-row">
        <header>
          <span><strong>{zh ? "思考深度" : "Reasoning effort"}</strong><small>{cursorProvider ? (zh ? "档位会切换 Cursor 的实际模型版本" : "Each tier selects the matching Cursor model variant") : (zh ? "越高越深入，响应时间更长" : "Higher is deeper and takes longer")}</small></span>
          {fastAvailable && <span className="composer-popover-modes">
            <span className="composer-popover-mode" data-mode="fast">
              <button type="button" aria-label={zh ? "Fast 模式" : "Fast mode"} aria-pressed={props.fast} className={props.fast ? "on" : ""} onClick={() => props.onSpeedChange(props.fast ? "standard" : "fast")}><Zap size={17} /></button>
              <span role="tooltip"><strong>{props.fastBoostTitle}</strong><small>{props.fastBoostDetail}</small></span>
            </span>
          </span>}
        </header>
        <ReasoningEffortSlider levels={props.reasoningLevels} value={props.selectedReasoning} onChange={props.onReasoningChange} labels={props.reasoningNames} fasterLabel={props.fasterLabel} smarterLabel={props.smarterLabel} highCostHint={props.highCostHint} fast={props.fast} ariaLabel={zh ? "思考深度" : "Reasoning effort"} />
      </section>
    </section>,
    document.body,
  ) : null;

  return <>
    <div ref={root} className="model-controls" data-open={String(open)} data-disabled={String(props.running)}>
      <button
        type="button"
        className="model-controls-trigger"
        aria-disabled={props.running}
        aria-expanded={open}
        onPointerDown={(event) => {
          if (props.running || event.button !== 0 || (event.pointerType === "touch" && !event.isPrimary)) return;
          if ((event.buttons & 1) === 0) return;
          beginActivation();
        }}
        onPointerUp={(event) => {
          if (props.running || event.button !== 0 || (event.pointerType === "touch" && !event.isPrimary)) return;
          beginReleaseFallback();
        }}
        onMouseDown={(event) => {
          if (props.running || event.button !== 0 || (event.buttons & 1) === 0 || pendingActivation.current) return;
          beginActivation();
        }}
        onMouseUp={(event) => {
          if (props.running || event.button !== 0) return;
          beginReleaseFallback();
        }}
        onClick={completeClickActivation}
      >
        <ProviderIcon provider={props.selectedProvider} size={14} />
        <span className="model-selected-name">{props.selectedModelName}{selectedModelInfo?.cursorNoZDR && <span className="model-retention-icon" aria-label={retentionLabel} title={retentionDetail}><ShieldAlert size={11} aria-hidden="true" /></span>}</span><small>{props.selectedReasoningName}</small>
      </button>
    </div>
    {popover}
  </>;
}

function providerLabel(provider: string) {
  const normalized = provider.toLocaleLowerCase();
  if (normalized === "chatgpt" || normalized === "openai") return "ChatGPT";
  if (normalized === "grok" || normalized === "xai") return "Grok";
  if (normalized === "cursor") return "Cursor";
  if (normalized === "openrouter") return "OpenRouter";
  return provider;
}

function modelHint(model: ComposerPickerModel, zh: boolean) {
  const capabilities = new Set((model.capabilities ?? []).map((item) => item.trim().toLocaleLowerCase()));
  const inputs = new Set((model.inputModalities ?? []).map((item) => item.trim().toLocaleLowerCase()));
  const identity = `${model.id} ${model.name}`.toLocaleLowerCase();
  const parts: string[] = [];
  if (capabilities.has("reasoning") || (model.reasoningLevels?.length ?? 0) > 0) parts.push(zh ? "推理" : "Reasoning");
  if (inputs.has("image") || identity.includes("claude") || identity.includes("gemini") || identity.includes("gpt-") || identity.includes("codex")) parts.push(zh ? "图像" : "Image");
  if (capabilities.has("tools")) parts.push(zh ? "工具" : "Tools");
  if (capabilities.has("structured-output")) parts.push(zh ? "结构化" : "Structured");
  if (parts.length > 0) return parts.join(" · ");
  if (identity.includes("codex") || identity.includes("code")) return zh ? "代码 · 工具调用" : "Code · tools";
  if (identity.includes("spark") || identity.includes("fast")) return zh ? "快速响应 · 低成本" : "Fast · efficient";
  return zh ? "推理 · 图像 · 工具" : "Reasoning · image · tools";
}
