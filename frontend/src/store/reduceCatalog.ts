import type { RuntimeEvent } from "../types";
import {
  findModelOption,
  normalizeBranch,
  normalizeHookCatalog,
  normalizeModel,
  normalizePlugin,
  normalizeSkill,
  normalizeUsageReport,
  numberValue,
  parseArray,
  parseMCPServers,
} from "./normalize";
import type { RuntimeData } from "./state";

/** Settings and catalog projections: skills, plugins, hooks, MCP, models, approval, git, recovery. */
export function reduceCatalogEvent(next: RuntimeData, event: RuntimeEvent): void {
  const data = event.data ?? {};
  switch (event.kind) {
    case "skill_catalog":
      next.skills = (event.skillCatalog ?? []).map(normalizeSkill);
      break;
    case "plugin_catalog":
      next.plugins = (event.pluginCatalog ?? []).map(normalizePlugin);
      break;
    case "hook_catalog":
      next.hookCatalog = normalizeHookCatalog(event.hookCatalog);
      break;
    case "usage_report":
      next.usageReport = normalizeUsageReport(event.usageReport);
      break;
    case "mcp_state": {
      if (event.state === "snapshot") {
        next.mcpServers = parseMCPServers(data.servers);
        break;
      }
      const name = data.server?.trim();
      if (!name) break;
      const index = next.mcpServers.findIndex((server) => server.name === name);
      if (index < 0) break;
      next.mcpServers = next.mcpServers.map((server, serverIndex) => serverIndex === index ? {
        ...server,
        state: data.state || event.state || server.state,
        error: data.error || event.text || "",
      } : server);
      break;
    }
    case "model_routes":
      next.modelRoutes = event.modelRoutes ?? [];
      if (next.snapshot) next.snapshot = {
        ...next.snapshot,
        subagentConcurrency: numberValue(data.subagent_max_concurrency, next.snapshot.subagentConcurrency),
        subagentMaxDepth: numberValue(data.subagent_max_depth, next.snapshot.subagentMaxDepth ?? 2),
        shellConcurrency: numberValue(data.shell_max_concurrency, next.snapshot.shellConcurrency ?? 2),
        shellMaxWallClockSeconds: numberValue(data.shell_max_wall_clock_seconds, next.snapshot.shellMaxWallClockSeconds ?? 600),
        subagentAwaitSeconds: numberValue(data.subagent_await_seconds, next.snapshot.subagentAwaitSeconds ?? 0),
        subagentIdleSeconds: numberValue(data.subagent_idle_seconds, next.snapshot.subagentIdleSeconds ?? 0),
        chatgptFastMode: data.chatgpt_fast_mode === "true",
      };
      break;
	case "model_providers":
	  next.modelProviders = (event.modelProviders ?? []).map((provider) => ({ ...provider, models: provider.models ?? [] }));
	  for (const provider of next.modelProviders) {
		if (provider.enabled && provider.models.length > 0) next.modelsByProvider = {
		  ...next.modelsByProvider,
		  [provider.id]: provider.models.map((model) => normalizeModel(model as unknown as Record<string, unknown>)),
		};
	  }
	  break;
    case "model_catalog": {
      const provider = data.provider || "unknown";
      const models = parseArray(data.models).map(normalizeModel);
      if (models.length === 0 && (next.modelsByProvider[provider] ?? []).length > 0) {
        break;
      }
      next.modelsByProvider = { ...next.modelsByProvider, [provider]: models };
      if (next.snapshot?.provider === provider) {
        const contextLimit = findModelOption(next.modelsByProvider[provider] ?? [], next.snapshot?.model ?? "")?.contextWindow ?? 0;
        const subscription = provider === "chatgpt" || provider === "grok";
        if (contextLimit > 0 && (subscription || next.contextUsage.contextLimit === 0)) next.contextUsage = { ...next.contextUsage, contextLimit };
      }
      break;
    }
    case "approval_mode":
      next.approvalMode = event.state || next.approvalMode;
      break;
    case "git_branches":
      next.branches = (event.gitBranches ?? []).map(normalizeBranch);
      next.workspaceDirty = Boolean(event.workspaceDirty);
      next.workspaceAdditions = numberValue(data.additions, 0);
      next.workspaceDeletions = numberValue(data.deletions, 0);
      next.workspaceChangedFiles = numberValue(data.changed_files, 0);
      break;
    case "recovery_state":
      if (data.items) next.recovery = parseArray(data.items);
      else if (event.state === "suspended") next.recovery = [...next.recovery, {
        id: event.runId ?? `recovery-${event.sequence}`,
        runId: event.runId ?? "",
        kind: data.kind ?? "run",
        title: "Suspended run",
        detail: event.text ?? "The run needs attention before it can continue.",
        state: event.state,
      }];
      else if (event.state === "reconciled") next.recovery = next.recovery.filter((item) => String(item.id) !== event.text);
      break;
  }
}
