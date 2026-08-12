import type { MCPServerEntry, ModelProvider, ModelRoute, PluginEntry, SettingsSection, SkillEntry } from "./types";

export interface SettingsSearchEntry {
  id: string;
  section: SettingsSection;
  title: string;
  description: string;
  keywords: string[];
}

type Copy = [id: string, section: SettingsSection, zhTitle: string, enTitle: string, zhDescription: string, enDescription: string, keywords?: string[]];

const staticSettings: Copy[] = [
  ["section:catalog", "catalog", "模型目录", "Model catalog", "管理订阅与 API 模型提供方", "Manage subscription and API model providers", ["provider", "供应商", "登录", "login", "api"]],
  ["section:models", "models", "模型路由", "Model routing", "为标题、规划、审批、视觉、压缩、回顾和子智能体分配模型", "Assign models for titles, planning, approval, vision, compaction, recap, and subagents", ["route", "reasoning", "思考深度", "recap"]],
  ["subagents:concurrency", "subagents", "子智能体并发", "Subagent concurrency", "设置同时运行的团队成员上限", "Set the maximum number of concurrent team members", ["agent", "并发", "capacity"]],
  ["subagents:shell", "subagents", "Shell 并发", "Shell concurrency", "设置本地命令的独立并发容量", "Set independent local command capacity", ["terminal", "命令", "并发"]],
  ["subagents:timeout", "subagents", "前台等待窗口", "Foreground wait window", "窗口结束后安全任务转为后台继续，不会被取消", "Safe tasks continue in the background when the window ends; they are not cancelled", ["await", "background", "等待", "后台", "长程"]],
  ["subagents:scheduling", "subagents", "调度策略", "Scheduling policy", "查看并行分发、进度投影和资源排队行为", "Review parallel dispatch, progress projection, and capacity queuing", ["parallel", "queued", "排队"]],
  ["governance:approval", "governance", "默认审批模式", "Default approval mode", "逐次确认、自动审查或 YOLO", "Prompt, automatic review, or YOLO", ["approval", "auto review", "审批"]],
  ["governance:messages", "governance", "运行中消息", "Messages while running", "选择加入队列或实时引导", "Choose queueing or immediate guidance", ["queue", "guide", "队列", "引导"]],
  ["appearance:language", "appearance", "界面语言", "Interface language", "切换中文或英文界面", "Switch between Chinese and English", ["语言", "english", "中文"]],
  ["appearance:theme", "appearance", "主题", "Theme", "选择暖白、夜间或跟随系统", "Choose warm light, night, or system appearance", ["dark", "light", "外观"]],
  ["appearance:font", "appearance", "界面字体", "Interface font", "选择系统中可用的界面字体", "Choose an installed interface font", ["字体", "font family"]],
  ["appearance:font-size", "appearance", "字体大小", "Font size", "调整界面文字大小", "Adjust interface text size", ["字号", "font size"]],
  ["appearance:motion", "appearance", "减弱动态效果", "Reduce motion", "关闭场景切换与流式渐显动画", "Disable scene and streaming reveal motion", ["animation", "动效", "动画"]],
  ["extensions:mcp", "extensions", "MCP 服务", "MCP services", "添加、启停、重连或删除 MCP 服务", "Add, enable, reconnect, or delete MCP services", ["server", "工具", "tool"]],
  ["extensions:skills", "extensions", "Skills", "Skills", "管理通用 .agents 技能", "Manage shared .agents skills", ["技能", ".agents"]],
  ["extensions:plugins", "extensions", "插件", "Plugins", "选择并导入插件到 Azem 目录", "Select and import plugins into Azem's directory", ["plugin", "codex", "导入"]],
];

export function settingsSearchEntries(
  language: "zh-CN" | "en",
  routes: ModelRoute[],
  providers: ModelProvider[],
  mcpServers: MCPServerEntry[] = [],
  skills: SkillEntry[] = [],
  plugins: PluginEntry[] = [],
): SettingsSearchEntry[] {
  const entries = staticSettings.map(([id, section, zhTitle, enTitle, zhDescription, enDescription, keywords = []]) => ({
    id,
    section,
    title: language === "zh-CN" ? zhTitle : enTitle,
    description: language === "zh-CN" ? zhDescription : enDescription,
    keywords,
  }));
  for (const provider of providers) {
    entries.push({
      id: `provider:${provider.ID}`,
      section: "catalog",
      title: provider.DisplayName || provider.ID,
      description: language === "zh-CN" ? "模型提供方、登录与模型目录" : "Model provider, login, and model catalog",
      keywords: [provider.ID, provider.Backend, "provider", "模型提供方"],
    });
    for (const model of provider.Models) {
      entries.push({
        id: `provider:${provider.ID}`,
        section: "catalog",
        title: model.name || model.id,
        description: `${provider.DisplayName || provider.ID} · ${language === "zh-CN" ? "模型目录" : "Model catalog"}`,
        keywords: [model.id, provider.ID, ...(model.capabilities ?? [])],
      });
    }
  }
  for (const route of routes) {
    if (route.Scope === "main") continue;
    const title = routeSearchTitle(route, language);
    entries.push({
      id: routeSearchID(route),
      section: "models",
      title,
      description: language === "zh-CN" ? "模型、提供方与思考深度" : "Model, provider, and reasoning effort",
      keywords: [route.Scope, route.Role, route.Label, "route", "模型路由", "reasoning"].filter(Boolean),
    });
  }
  for (const server of mcpServers) {
    entries.push({ id: "extensions:mcp", section: "extensions", title: server.name, description: language === "zh-CN" ? "MCP 服务" : "MCP service", keywords: [server.transport, server.command, server.url].filter((value): value is string => Boolean(value)) });
  }
  for (const skill of skills) {
    entries.push({ id: "extensions:skills", section: "extensions", title: skill.name, description: skill.description || (language === "zh-CN" ? "Skill" : "Skill"), keywords: [skill.sourcePath, "skill", "技能"] });
  }
  for (const plugin of plugins) {
    entries.push({ id: "extensions:plugins", section: "extensions", title: plugin.displayName || plugin.name || plugin.id, description: plugin.description || (language === "zh-CN" ? "插件" : "Plugin"), keywords: [plugin.id, plugin.name, plugin.origin, plugin.marketplace, plugin.developerName, "plugin", "插件"] });
  }
  return entries;
}

export function filterSettings(entries: SettingsSearchEntry[], query: string) {
  const tokens = query.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean);
  if (!tokens.length) return [];
  return entries.filter((entry) => {
    const haystack = `${entry.title} ${entry.description} ${entry.keywords.join(" ")}`.toLocaleLowerCase();
    return tokens.every((token) => haystack.includes(token));
  });
}

export function routeSearchID(route: ModelRoute) {
  return `route:${route.Scope}:${route.Role || "default"}`;
}

function routeSearchTitle(route: ModelRoute, language: "zh-CN" | "en") {
  const names: Record<string, [string, string]> = {
    title: ["会话标题模型", "Conversation title model"],
    plan: ["规划模型", "Planning model"],
    approval: ["审批模型", "Approval model"],
    vision: ["视觉模型", "Vision model"],
    compaction: ["上下文压缩模型", "Context compaction model"],
    recap: ["回顾模型", "Recap model"],
  };
  const pair = names[route.Scope];
  if (pair) return language === "zh-CN" ? pair[0] : pair[1];
  return route.Role || route.Label || (language === "zh-CN" ? "子智能体模型" : "Subagent model");
}
