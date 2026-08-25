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

function CursorMark({ size, className }: { size: number; className: string }) {
	return <svg className={`provider-icon ${className}`.trim()} width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
		<path d="M11.503.131 1.891 5.678a.84.84 0 0 0-.42.726v11.188c0 .3.162.575.42.724l9.609 5.55a1 1 0 0 0 .998 0l9.61-5.55a.84.84 0 0 0 .42-.724V6.404a.84.84 0 0 0-.42-.726L12.497.131a1.01 1.01 0 0 0-.996 0M2.657 6.338h18.55c.263 0 .43.287.297.515L12.23 22.918c-.062.107-.229.064-.229-.06V12.335a.59.59 0 0 0-.295-.51l-9.11-5.257c-.109-.063-.064-.23.061-.23" />
	</svg>;
}

export default function ProviderIcon({ provider, logoID, size = 16, className = "" }: { provider: string; logoID?: string; size?: number; className?: string }) {
	const normalized = provider.toLowerCase().replaceAll("_", "-");
	const logo = logoID || logoAliases[provider.toLowerCase()] || normalized;
	const [failed, setFailed] = useState(false);
	useEffect(() => setFailed(false), [logo]);
	if (logo === "cursor" || normalized === "cursor") {
		return <CursorMark size={size} className={className} />;
	}
	if (failed) return <Sparkles className={`provider-icon ${className}`.trim()} width={size} height={size} aria-hidden="true" />;
	return <img className={`provider-icon ${className}`.trim()} src={`https://models.dev/logos/${encodeURIComponent(logo)}.svg`} width={size} height={size} alt="" aria-hidden="true" loading="lazy" onError={() => setFailed(true)} />;
}
