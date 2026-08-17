import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Search, Zap } from "lucide-react";
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
  const root = useRef<HTMLDetailsElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [position, setPosition] = useState<Position | null>(null);
  const zh = props.language === "zh-CN";
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleModels = normalizedQuery
    ? props.models.filter((model) => `${model.name} ${model.id} ${model.provider} ${(model.aliases ?? []).join(" ")}`.toLocaleLowerCase().includes(normalizedQuery))
    : props.models;
  const fastAvailable = props.fastAvailable;

  const place = useCallback(() => {
    const summary = root.current?.querySelector("summary");
    if (!summary) return;
    const rect = summary.getBoundingClientRect();
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
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (root.current?.contains(target) || panel.current?.contains(target)) return;
      if (root.current) root.current.open = false;
      setOpen(false);
      setPosition(null);
      setQuery("");
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);

  const close = () => {
    if (root.current) root.current.open = false;
    setOpen(false);
    setPosition(null);
    setQuery("");
  };

  const choose = (model: ComposerPickerModel) => {
    props.onModelChange(`${model.provider}/${model.id}`);
    close();
  };

  const popover = open && position ? createPortal(
    <section ref={panel} className="composer-model-popover" style={{ position: "fixed", left: position.left, bottom: position.bottom, width: position.width }} aria-label={zh ? "模型与思考" : "Model and reasoning"}>
      <header><strong>{zh ? "模型与思考" : "Model and reasoning"}</strong><small>{zh ? "仅应用到当前会话" : "Applies to this conversation"}</small></header>
      <label className="composer-model-search"><Search size={14} /><input autoFocus type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={zh ? "搜索模型或提供方" : "Search models or providers"} /><kbd>⌘F</kbd></label>
      <div className="composer-model-list" role="listbox">
        {visibleModels.map((model) => {
          const selected = `${model.provider}/${model.id}` === props.selectedModel;
          return <button key={`${model.provider}:${model.id}`} type="button" role="option" aria-selected={selected} className={selected ? "selected" : ""} onClick={() => choose(model)}>
            <ProviderIcon provider={model.provider} size={20} />
            <span><strong>{model.name}</strong><small>{providerLabel(model.provider)} · {modelHint(model, zh)}</small></span>
            <Check size={14} />
          </button>;
        })}
        {!visibleModels.length && <p>{zh ? "没有匹配的模型" : "No matching models"}</p>}
      </div>
      <section className="composer-effort-row">
        <header>
          <span><strong>{zh ? "思考深度" : "Reasoning effort"}</strong><small>{zh ? "越高越深入，响应时间更长" : "Higher is deeper and takes longer"}</small></span>
          {fastAvailable && <span className="composer-popover-fast"><button type="button" aria-label={zh ? "Fast 模式" : "Fast mode"} aria-pressed={props.fast} className={props.fast ? "on" : ""} onClick={() => props.onSpeedChange(props.fast ? "standard" : "fast")}><Zap size={17} /></button><span role="tooltip"><strong>{props.fastBoostTitle}</strong><small>{props.fastBoostDetail}</small></span></span>}
        </header>
        <ReasoningEffortSlider levels={props.reasoningLevels} value={props.selectedReasoning} onChange={props.onReasoningChange} labels={props.reasoningNames} fasterLabel={props.fasterLabel} smarterLabel={props.smarterLabel} highCostHint={props.highCostHint} fast={props.fast} ariaLabel={zh ? "思考深度" : "Reasoning effort"} />
      </section>
    </section>,
    document.body,
  ) : null;

  return <>
    <details ref={root} className="model-controls" data-disabled={String(props.running)} onToggle={(event) => {
      if (props.running) {
        event.currentTarget.open = false;
        return;
      }
      const next = event.currentTarget.open;
      setOpen(next);
      next ? requestAnimationFrame(place) : setPosition(null);
    }}>
      <summary aria-disabled={props.running} aria-expanded={open}>
        <ProviderIcon provider={props.selectedProvider} size={14} />
        <span>{props.selectedModelName}</span><small>{props.selectedReasoningName}</small><ChevronDown size={12} className="model-controls-chevron" />
      </summary>
    </details>
    {popover}
  </>;
}

function providerLabel(provider: string) {
  const normalized = provider.toLocaleLowerCase();
  if (normalized === "chatgpt" || normalized === "openai") return "ChatGPT";
  if (normalized === "grok" || normalized === "xai") return "Grok";
  if (normalized === "openrouter") return "OpenRouter";
  return provider;
}

function modelHint(model: ComposerPickerModel, zh: boolean) {
  const id = `${model.id} ${model.name}`.toLocaleLowerCase();
  if (id.includes("codex") || id.includes("code")) return zh ? "代码 · 工具调用" : "Code · tools";
  if (id.includes("spark") || id.includes("fast")) return zh ? "快速响应 · 低成本" : "Fast · efficient";
  return zh ? "推理 · 图像 · 工具" : "Reasoning · image · tools";
}
