import { Sparkles } from "lucide-react";
import { useEffect, useState } from "react";

const logoAliases: Record<string, string> = {
	chatgpt: "openai",
	grok: "xai",
	ai302: "302ai",
	bigmodel: "zhipuai",
	cloudflare: "cloudflare-workers-ai",
	copilot: "github-copilot",
	github: "github-copilot",
	firepass: "fireworks-ai",
	fireworks: "fireworks-ai",
	gmi: "gmicloud",
	"gradient-ai": "digitalocean",
	kimi: "moonshotai",
	"meta-llama": "llama",
	nanogpt: "nano-gpt",
	novita: "novita-ai",
	"nvidia-nim": "nvidia",
	"opencode-zen": "opencode",
	"scx-ai": "scx",
	wafer: "wafer.ai",
	xiaomimimo: "xiaomi",
	"zhipu-v4": "zhipuai",
	ollama: "ollama-cloud",
};

export default function ProviderIcon({ provider, logoID, size = 16, className = "" }: { provider: string; logoID?: string; size?: number; className?: string }) {
	const normalized = provider.toLowerCase().replaceAll("_", "-");
	const logo = logoID || logoAliases[provider.toLowerCase()] || normalized;
	const [failed, setFailed] = useState(false);
	useEffect(() => setFailed(false), [logo]);
	if (failed) return <Sparkles className={`provider-icon ${className}`.trim()} width={size} height={size} aria-hidden="true" />;
	return <img className={`provider-icon ${className}`.trim()} src={`https://models.dev/logos/${encodeURIComponent(logo)}.svg`} width={size} height={size} alt="" aria-hidden="true" loading="lazy" onError={() => setFailed(true)} />;
}
