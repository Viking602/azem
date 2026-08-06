const logoAliases: Record<string, string> = {
	chatgpt: "openai",
	grok: "xai",
};

export default function ProviderIcon({ provider, size = 16, className = "" }: { provider: string; size?: number; className?: string }) {
	const logo = logoAliases[provider.toLowerCase()] ?? provider.toLowerCase();
	return <img className={`provider-icon ${className}`.trim()} src={`https://models.dev/logos/${encodeURIComponent(logo)}.svg`} width={size} height={size} alt="" aria-hidden="true" loading="lazy" />;
}
