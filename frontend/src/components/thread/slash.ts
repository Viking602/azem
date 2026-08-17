import type { ComponentType } from "react";
import {
  Bot, Box, Brain, CircleDot, Minimize2, Plug, Plus, RefreshCw, RotateCcw, Settings,
  ShieldCheck, Zap,
} from "lucide-react";
import { translator } from "../../i18n";
import type { SkillEntry, Snapshot } from "../../types";

export type SlashAction =
  | "new" | "compact" | "settings" | "skills" | "plan" | "fast"
  | "reasoning" | "approval" | "agents" | "mcp" | "reload-skills" | "inspector";
type SlashIcon = ComponentType<{ size?: number; className?: string }>;
export type SlashSuggestion = {
  value: string;
  label: string;
  detail: string;
  kind: "command" | "skill";
  action?: SlashAction;
  skill?: string;
  badge?: string;
  icon: SlashIcon;
};
export type SlashContext = {
  reasoningLabel?: string;
  approvalLabel?: string;
  planMode?: boolean;
  agentMode?: string;
  fast?: boolean;
  fastAvailable?: boolean;
  contextPercent?: number;
};

const slashCommands: Array<{
  action: SlashAction;
  value?: string;
  aliases: string[];
  zh: string;
  en: string;
  zhDetail: string;
  enDetail: string;
  icon: SlashIcon;
  when?: (context: SlashContext) => boolean;
  detail?: (context: SlashContext, language: Snapshot["language"]) => string;
}> = [
  {
    action: "mcp", aliases: ["mcp", "plugins"], zh: "MCP", en: "MCP",
    zhDetail: "刷新 MCP 服务器状态", enDetail: "Refresh MCP server status", icon: Plug,
  },
  {
    action: "compact", aliases: ["compact", "compress", "压缩"], zh: "压缩", en: "Compact",
    zhDetail: "压缩当前会话的上下文", enDetail: "Compact the current conversation context", icon: Minimize2,
    detail: (context, language) => context.contextPercent != null && context.contextPercent > 0
      ? (language === "zh-CN" ? `压缩此聊天的上下文（已使用 ${context.contextPercent}%）` : `Compact this chat context (${context.contextPercent}% used)`)
      : (language === "zh-CN" ? "压缩当前会话的上下文" : "Compact the current conversation context"),
  },
  {
    action: "compact", value: "/rebuild", aliases: ["rebuild", "重建"], zh: "重建上下文", en: "Rebuild context",
    zhDetail: "立即运行新的语义压缩内核", enDetail: "Run the semantic context kernel now", icon: RotateCcw,
  },
  {
    action: "settings", aliases: ["settings", "config", "设置"], zh: "设置", en: "Settings",
    zhDetail: "打开 Azem 设置", enDetail: "Open Azem settings", icon: Settings,
  },
  {
    action: "reload-skills", aliases: ["reload", "reload-skills", "重新加载"], zh: "重新加载技能", en: "Reload skills",
    zhDetail: "重新扫描本地技能目录", enDetail: "Rescan local skill directories", icon: RefreshCw,
  },
  {
    action: "plan", aliases: ["plan", "计划"], zh: "计划模式", en: "Plan mode",
    zhDetail: "切换只规划不实施的模式", enDetail: "Toggle planning without implementation", icon: CircleDot,
    detail: (context, language) => language === "zh-CN"
      ? (context.planMode ? "关闭计划模式" : "开启只规划不实施的模式")
      : (context.planMode ? "Turn off plan mode" : "Plan without implementing"),
  },
  // Team mode UI is intentionally unavailable until multi-agent is designed.
  {
    action: "agents", aliases: ["subagents", "子智能体", "智能体", "代理"], zh: "子智能体", en: "Subagents",
    zhDetail: "查看当前子智能体任务", enDetail: "Inspect current subagent tasks", icon: Bot,
  },
  {
    action: "approval", aliases: ["approval", "approve", "审批", "yolo"], zh: "审批模式", en: "Approval",
    zhDetail: "切换默认审批策略", enDetail: "Cycle the default approval policy", icon: ShieldCheck,
    detail: (context, language) => language === "zh-CN"
      ? `当前：${context.approvalLabel || "逐次审批"} · 点击切换`
      : `Current: ${context.approvalLabel || "Ask first"} · click to cycle`,
  },
  {
    action: "fast", aliases: ["fast", "speed", "快速"], zh: "快速", en: "Fast",
    zhDetail: "切换 ChatGPT 标准与快速速度", enDetail: "Toggle standard and fast ChatGPT speed", icon: Zap,
    when: (context) => Boolean(context.fastAvailable),
    detail: (context, language) => language === "zh-CN"
      ? (context.fast ? "当前快速模式 · 点击切回标准" : "1.5x 速度，用量更高")
      : (context.fast ? "Fast mode on · click for standard" : "1.5x speed, increased usage"),
  },
  {
    action: "reasoning", aliases: ["reasoning", "reason", "推理"], zh: "推理", en: "Reasoning",
    zhDetail: "循环切换推理强度", enDetail: "Cycle reasoning effort", icon: Brain,
    detail: (context, language) => language === "zh-CN"
      ? (context.reasoningLabel || "中")
      : (context.reasoningLabel || "Medium"),
  },
  {
    action: "new", aliases: ["new", "chat", "新聊天", "新建"], zh: "新聊天", en: "New chat",
    zhDetail: "在同一工作区开启空白聊天", enDetail: "Start a blank chat in this workspace", icon: Plus,
  },
];

export function slashSuggestions(input: string, skills: SkillEntry[], language: Snapshot["language"], context: SlashContext = {}): SlashSuggestion[] {
  if (!input.startsWith("/") || /\s/.test(input.slice(1))) return [];
  const query = input.slice(1).toLowerCase();
  const commands = slashCommands
    .filter((item) => (item.when ? item.when(context) : true))
    .filter((item) => [item.action, ...item.aliases, item.zh, item.en].some((candidate) => slashMatch(candidate, query)))
    .map((item) => ({
      value: item.value ?? `/${item.action}`,
      label: language === "zh-CN" ? item.zh : item.en,
      detail: item.detail?.(context, language) ?? (language === "zh-CN" ? item.zhDetail : item.enDetail),
      kind: "command" as const,
      action: item.action,
      icon: item.icon,
    }));
  const skillItems = skills
    .filter((skill) => !skill.disabled)
    .filter((skill) => matchSkill(skill, query))
    .map((skill) => ({
      value: `/skill:${skill.name}`,
      label: skillTitle(skill.name),
      detail: skill.description || translator(language)("skillUseHint"),
      kind: "skill" as const,
      skill: skill.name,
      badge: skill.bundled ? translator(language)("skillBuiltin") : translator(language)("skillPersonal"),
      icon: Box,
    }));
  return [...commands, ...skillItems];
}

function matchSkill(skill: SkillEntry, query: string) {
  if (!query) return true;
  return [
    skill.name,
    skillTitle(skill.name),
    skill.description,
    `skill:${skill.name}`,
    skill.name.replace(/[-_]/g, " "),
  ].some((candidate) => slashMatch(candidate, query));
}

export function skillTitle(name: string) {
  return name
    .split(/[-_]/g)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function slashMatch(candidate: string, query: string) {
  if (!query) return true;
  const value = candidate.toLowerCase();
  if (value.includes(query)) return true;
  return value.split(/[-_\s:/]+/).some((part) => part.startsWith(query));
}

export function parseSkillPrompt(input: string, language: Snapshot["language"]) {
  const match = /^\/skill:([^\s]+)(?:\s+([\s\S]*))?$/.exec(input.trim());
  if (!match) return null;
  const name = match[1];
  const instruction = match[2]?.trim() || (language === "zh-CN" ? `使用“${name}”技能处理当前工作区并报告结果。` : `Apply the "${name}" skill to the current workspace and report the result.`);
  return { name, instruction };
}
