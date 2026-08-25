import type { MCPServerEntry, ModelProvider, ModelRoute, PluginEntry, SettingsSection, SkillEntry } from "./types";

export interface SettingsSearchEntry {
  id: string;
  section: SettingsSection;
  title: string;
  description: string;
  keywords: string[];
}

export const settingsSectionSearchAliases: Partial<Record<SettingsSection, readonly string[]>> = {
  governance: ["governance", "governance and approval", "governance & approvals", "governance and approvals"],
  usage: ["usage", "token", "tokens", "用量", "token 记录", "token记录"],
  security: ["security", "security scan", "security scanning", "安全", "安全扫描", "审计"],
};

type Copy = [id: string, section: SettingsSection, zhTitle: string, enTitle: string, zhDescription: string, enDescription: string, keywords?: string[]];

const staticSettings: Copy[] = [
  ["section:catalog", "catalog", "模型目录", "Model catalog", "管理订阅与 API 模型提供方", "Manage subscription and API model providers", ["provider", "供应商", "登录", "login", "api"]],
  ["section:models", "models", "模型路由", "Model routing", "为标题、规划、审批、视觉、回顾和子智能体分配模型", "Assign models for titles, planning, approval, vision, recap, and subagents", ["route", "reasoning", "思考深度", "recap"]],
  ["section:subagents", "subagents", "子智能体", "Subagents", "并发容量、调度与主会话展示", "Capacity, scheduling, and main-session display", ["subagent", "团队"]],
  ["subagents:concurrency", "subagents", "子智能体并发", "Subagent concurrency", "设置同时运行的团队成员上限", "Set the maximum number of concurrent team members", ["agent", "并发", "capacity"]],
  ["subagents:depth", "subagents", "递归深度", "Recursion depth", "子智能体继续委派的层数", "Levels of nested delegation", ["recursion", "depth", "递归", "委派"]],
  ["subagents:shell", "subagents", "Shell 并发", "Shell concurrency", "设置本地命令的独立并发容量", "Set independent local command capacity", ["terminal", "命令", "并发"]],
  ["subagents:shell-wall", "subagents", "单条命令墙钟", "Command wall clock", "一条本地命令的最长运行时间", "Maximum lifetime of one local command", ["wall clock", "timeout", "墙钟", "shell", "max_wall_clock"]],
  ["subagents:timeout", "subagents", "前台等待窗口", "Foreground wait window", "默认等到前台完成；有限窗口到期只释放父调用，不取消", "Wait until the foreground child finishes by default; a limited window only releases the parent", ["await", "background", "等待", "后台", "长程", "直到完成", "until", "done"]],
  ["subagents:idle", "subagents", "无响应自动取消", "Cancel when idle", "没有思考、输出或工具活动时取消子代理；默认 5 分钟", "Cancel a silent subagent with no thinking, output, or tool activity; default 5 minutes", ["idle", "stuck", "卡住", "无响应", "取消", "timeout"]],
  ["subagents:scheduling", "subagents", "调度策略", "Scheduling policy", "并行工具分发是产品不变量，不能改为串行", "Parallel tool dispatch is a product invariant and cannot be changed to sequential", ["parallel", "调度"]],
  ["subagents:display", "subagents", "主会话展示", "Main-session display", "主会话中的进度、结果卡片与排队呈现", "Progress, result cards, and queuing in the main conversation", ["progress", "queued", "排队", "卡片"]],
  ["section:security", "security", "安全扫描", "Security scans", "配置扫描模式、深度、运行时限、模型路由和发布边界", "Configure scan mode, depth, runtime, model routes, and publication boundary", ["audit", "sarif", "deep", "finding"]],
  ["security:execution", "security", "执行策略", "Execution policy", "启用状态、默认模式与深度扫描收敛参数", "Enablement, default mode, and Deep Scan convergence", ["worker", "subagent", "saturation", "并发"]],
  ["security:deadline", "security", "运行时限", "Run deadline", "只设置最长运行时间，不设置 Token 或工具调用硬中断", "Set only the maximum runtime; no hard token or tool-call interruption", ["deadline", "timeout", "时限"]],
  ["security:routes", "security", "安全模型路由", "Security model routes", "为审计、归并、修复和验证选择模型", "Choose models for audit, reduction, fixing, and verification", ["audit", "reducer", "fixer", "verifier"]],
  ["security:publication", "security", "发布边界", "Publication boundary", "查看由受信任 Host 管理的 MCP 发布工具状态", "Inspect the MCP publication tool managed by the trusted host", ["mcp", "linear", "publication", "发布"]],
  ["section:governance", "governance", "治理与审批", "Approvals", "设置默认审批边界和运行中消息处理方式", "Default approval policy and how new messages are handled while a turn is running", ["governance", "governance and approval", "governance & approvals", "governance and approvals", "policy", "审批"]],
  ["governance:approval", "governance", "默认审批模式", "Default approval mode", "逐次确认、自动审查或 YOLO", "Prompt, automatic review, or YOLO", ["approval", "auto review", "审批", "governance"]],
  ["governance:messages", "governance", "运行中消息", "Messages while running", "选择加入队列或实时引导", "Choose queueing or immediate guidance", ["queue", "guide", "队列", "引导"]],
  ["appearance:language", "appearance", "界面语言", "Interface language", "切换中文或英文界面", "Switch between Chinese and English", ["语言", "english", "中文"]],
  ["appearance:theme", "appearance", "主题", "Theme", "选择暖白、夜间或跟随系统", "Choose warm light, night, or system appearance", ["dark", "light", "外观"]],
  ["appearance:font", "appearance", "界面字体", "Interface font", "选择系统中可用的界面字体", "Choose an installed interface font", ["字体", "font family"]],
  ["appearance:font-size", "appearance", "字体大小", "Font size", "调整界面文字大小", "Adjust interface text size", ["字号", "font size"]],
  ["appearance:chat-text", "appearance", "聊天文本", "Chat text", "只调整对话区正文字号和代码块字号", "Adjust only conversation prose and fenced code", ["聊天", "对话", "transcript"]],
  ["appearance:chat-font-size", "appearance", "UI 文本", "UI text", "调整对话气泡、正文、解说和输入框字号", "Adjust conversation bubbles, prose, commentary, and composer size", ["聊天", "对话", "字号", "chat font"]],
  ["appearance:chat-code-font-size", "appearance", "代码字体大小", "Code font size", "调整对话里围栏代码块的等宽字号", "Adjust fenced code block size in the transcript", ["代码", "等宽", "code font", "monospace"]],
  ["appearance:motion", "appearance", "减弱动态效果", "Reduce motion", "关闭场景切换与流式渐显动画", "Disable scene and streaming reveal motion", ["animation", "动效", "动画"]],
  ["extensions:mcp", "extensions", "MCP 服务", "MCP services", "添加、启停、重连或删除 MCP 服务", "Add, enable, reconnect, or delete MCP services", ["server", "工具", "tool"]],
  ["extensions:skills", "extensions", "Skills", "Skills", "管理通用 .agents 技能", "Manage shared .agents skills", ["技能", ".agents"]],
  ["extensions:plugins", "extensions", "插件", "Plugins", "选择并导入插件到 Azem 目录", "Select and import plugins into Azem's directory", ["plugin", "codex", "导入"]],
  ["extensions:marketplace", "extensions", "插件市场", "Marketplace", "添加市场源，浏览、安装、升级和卸载插件", "Add catalog sources and browse, install, upgrade, or uninstall plugins", ["marketplace", "catalog", "install", "市场", "安装"]],
  ["extensions:hooks", "extensions", "Hooks", "Hooks", "查看生命周期 Hooks，单独信任插件 Hooks，并逐条启用或停用", "Inspect lifecycle hooks, explicitly trust plugin hooks, and enable or disable each command", ["hook", "trust_hooks", "hooks.disabled", "set_hook_enabled", "生命周期"]],
  ["section:archive", "archive", "归档", "Archive", "归档过久未活动的会话，并按所属项目查看或恢复", "Archive inactive conversations and restore them by project", ["归档", "archive", "不活跃", "inactive"]],
  ["archive:inactive", "archive", "归档不活跃会话", "Archive inactive conversations", "将超过指定天数未更新且未置顶的会话移出侧栏", "Move unpinned conversations that have been idle past the selected age out of the sidebar", ["inactive", "过期", "清理"]],
  ["archive:list", "archive", "已归档会话", "Archived conversations", "按所属项目查看已归档会话并恢复", "Browse archived conversations by project and restore them", ["恢复", "restore", "项目"]],
  ["section:usage", "usage", "用量", "Usage", "查看本机已记录的 Token、热力与模型分解", "Inspect recorded tokens, heatmap, and model breakdown", ["token", "tokens", "用量", "token 记录", "cache", "缓存"]],
  ["usage:ledger", "usage", "用量账本", "Usage ledger", "累计 Token、输入输出与缓存", "Total tokens, input, output, and cache", ["累计", "total", "cache"]],
  ["usage:activity", "usage", "Token 活动", "Token activity", "近一年热力网格", "Year-long token heatmap", ["热力", "calendar", "heatmap", "连续", "活动"]],
  ["usage:kinds", "usage", "主会话与子智能体", "Main and subagents", "按请求类型分解用量", "Break usage down by request kind", ["subagent", "子智能体", "main"]],
  ["usage:models", "usage", "模型用量", "Model usage", "按提供方与模型汇总", "Totals by provider and model", ["chatgpt", "grok", "cursor", "llmux", "deepseek"]],
  ["usage:skills", "usage", "技能", "Skills", "技能调用次数", "Skill call counts", ["skill", "技能"]],
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
      id: `provider:${provider.id}`,
      section: "catalog",
      title: provider.displayName || provider.id,
      description: language === "zh-CN" ? "模型提供方、登录与模型目录" : "Model provider, login, and model catalog",
      keywords: [provider.id, provider.backend, "provider", "模型提供方"],
    });
    for (const model of provider.models) {
      entries.push({
        id: `provider:${provider.id}`,
        section: "catalog",
        title: model.name || model.id,
        description: `${provider.displayName || provider.id} · ${language === "zh-CN" ? "模型目录" : "Model catalog"}`,
        keywords: [model.id, provider.id, ...(model.capabilities ?? [])],
      });
    }
  }
  for (const route of routes) {
    if (route.scope === "main") continue;
    const title = routeSearchTitle(route, language);
    entries.push({
      id: routeSearchID(route),
      section: "models",
      title,
      description: language === "zh-CN" ? "模型、提供方与思考深度" : "Model, provider, and reasoning effort",
      keywords: [route.scope, route.role, route.label, "route", "模型路由", "reasoning"].filter(Boolean),
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
  return `route:${route.scope}:${route.role || "default"}`;
}

function routeSearchTitle(route: ModelRoute, language: "zh-CN" | "en") {
  const names: Record<string, [string, string]> = {
    title: ["会话标题模型", "Conversation title model"],
    plan: ["规划模型", "Planning model"],
    approval: ["审批模型", "Approval model"],
    vision: ["视觉模型", "Vision model"],
    recap: ["回顾模型", "Recap model"],
  };
  const pair = names[route.scope];
  if (pair) return language === "zh-CN" ? pair[0] : pair[1];
  return route.role || route.label || (language === "zh-CN" ? "子智能体模型" : "Subagent model");
}
