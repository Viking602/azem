const WEB_RESEARCH_TOOL = /web_?search|websearch|search_?web|web_?fetch|webfetch|web_?browse|open_?page|fetch_?url|exa_?search|tavily|brave_search|grok[^a-z0-9]*search|x_keyword_search|x_semantic_search/iu;

export function isWebResearchTool(name: string): boolean {
  return WEB_RESEARCH_TOOL.test(name.toLowerCase().replaceAll("-", "_").replaceAll(".", "_"));
}
