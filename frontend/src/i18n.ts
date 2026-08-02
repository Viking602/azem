const messages = {
  "zh-CN": {
    newSession: "新会话", search: "搜索", runs: "运行", agents: "Agents", extensions: "扩展",
    projects: "项目", workbench: "工作台", workspace: "工作区", settings: "设置",
    recovery: "恢复中心", promptTitle: "今天要在 Azem 中完成什么？",
    promptPlaceholder: "描述任务、附加图片或引用文件…", send: "发送", cancel: "取消",
    handoff: "转交", actions: "操作", environment: "环境", changes: "变更", context: "上下文",
    runtimeHealthy: "Runtime 正常", noChanges: "暂无变更", branch: "分支", local: "本地",
    running: "运行中", ready: "就绪", failed: "失败", completed: "完成", cancelled: "已取消",
    autoReview: "自动审查", promptApproval: "逐次审批", yolo: "始终允许", plan: "计划模式",
    team: "团队模式", single: "默认模式", model: "模型", reasoning: "推理强度", approve: "允许", deny: "拒绝", approveOnce: "仅本次允许",
    roleModels: "角色模型", subagentRuntime: "子代理运行", codexSubscription: "Codex 订阅", appearance: "界面",
    planModel: "规划模型", concurrency: "最大并发任务", fastMode: "Fast 模式", language: "语言",
    speed: "速度", standardSpeed: "标准", fastSpeed: "快速", fastModeHint: "快速模式仅对部分模型生效。",
    theme: "主题", light: "浅色", dark: "深色", system: "跟随系统", inspector: "上下文检查器",
    noAgents: "当前没有子代理", noSessions: "还没有历史会话", command: "搜索命令或会话…",
    keyboardTip: "↑↓ 选择 · Enter 打开 · Esc 关闭", loading: "正在加载 Azem…",
    attach: "添加图片", compact: "压缩会话", reviewChanges: "审查变更", resume: "继续会话",
    retry: "重试", reconnect: "重新连接", reload: "重新加载", modelInherit: "继承父代理",
    approvalTitle: "需要确认", approvalTarget: "即将执行", approvalWorkspace: "当前工作区", approvalOperation: "受限操作",
    approvalWrite: "此操作将修改工作区内容。", approvalExternal: "此操作可能影响工作区之外的系统。",
    approvalReadOnly: "此操作将读取受保护内容。", approvalConfirm: "此操作需要你的确认。", approveSession: "本会话允许",
    riskLow: "低风险", riskMedium: "中风险", riskHigh: "高风险",
    toolGeneric: "调用工具", toolReadFile: "读取文件", toolReadArtifact: "读取工件", toolWriteFile: "写入文件", toolEditFile: "编辑文件",
    toolSearch: "搜索代码", toolListFiles: "列出文件", toolShell: "运行命令", toolGoTest: "运行 Go 测试", toolGofmt: "格式化 Go 代码",
    toolGitDiff: "查看 Git 差异", toolActivateSkill: "加载技能", toolSpawn: "启动子代理", toolGetSubagentOutput: "获取子代理输出", toolStopSubagent: "停止子代理",
  },
  en: {
    newSession: "New thread", search: "Search", runs: "Runs", agents: "Agents", extensions: "Extensions",
    projects: "Projects", workbench: "Workbench", workspace: "Workspace", settings: "Settings",
    recovery: "Recovery", promptTitle: "What do you want to build in Azem?",
    promptPlaceholder: "Describe a task, attach an image, or reference a file…", send: "Send", cancel: "Cancel",
    handoff: "Handoff", actions: "Actions", environment: "Environment", changes: "Changes", context: "Context",
    runtimeHealthy: "Runtime healthy", noChanges: "No changes", branch: "Branch", local: "Local",
    running: "Running", ready: "Ready", failed: "Failed", completed: "Completed", cancelled: "Cancelled",
    autoReview: "Auto review", promptApproval: "Ask first", yolo: "Always allow", plan: "Plan mode",
    team: "Team mode", single: "Default mode", model: "Model", reasoning: "Reasoning effort", approve: "Allow", deny: "Deny", approveOnce: "Allow once",
    roleModels: "Role models", subagentRuntime: "Subagent runtime", codexSubscription: "Codex subscription", appearance: "Appearance",
    planModel: "Plan model", concurrency: "Max concurrent tasks", fastMode: "Fast mode", language: "Language",
    speed: "Speed", standardSpeed: "Standard", fastSpeed: "Fast", fastModeHint: "Fast mode only applies to some models.",
    theme: "Theme", light: "Light", dark: "Dark", system: "System", inspector: "Context inspector",
    noAgents: "No subagents for this thread", noSessions: "No previous threads", command: "Search commands or threads…",
    keyboardTip: "↑↓ select · Enter open · Esc close", loading: "Loading Azem…",
    attach: "Add image", compact: "Compact thread", reviewChanges: "Review changes", resume: "Resume thread",
    retry: "Retry", reconnect: "Reconnect", reload: "Reload", modelInherit: "Inherit parent agent",
    approvalTitle: "Confirmation required", approvalTarget: "About to run", approvalWorkspace: "Current workspace", approvalOperation: "Restricted operation",
    approvalWrite: "This operation will modify workspace content.", approvalExternal: "This operation may affect systems outside the workspace.",
    approvalReadOnly: "This operation will read protected content.", approvalConfirm: "This operation requires your confirmation.", approveSession: "Allow for session",
    riskLow: "Low risk", riskMedium: "Medium risk", riskHigh: "High risk",
    toolGeneric: "Use tool", toolReadFile: "Read File", toolReadArtifact: "Read Artifact", toolWriteFile: "Write File", toolEditFile: "Edit File",
    toolSearch: "Search Code", toolListFiles: "List Files", toolShell: "Run Command", toolGoTest: "Run Go Tests", toolGofmt: "Format Go Code",
    toolGitDiff: "View Git Diff", toolActivateSkill: "Load Skill", toolSpawn: "Start Subagent", toolGetSubagentOutput: "Get Subagent Output", toolStopSubagent: "Stop Subagent",
  },
} as const;

export type MessageKey = keyof (typeof messages)["zh-CN"];

const toolNameKeys: Record<string, MessageKey> = {
  "coding.read_file": "toolReadFile", "context.read_artifact": "toolReadArtifact", "coding.write_file": "toolWriteFile", "coding.edit_hashline": "toolEditFile",
  "coding.search": "toolSearch", "coding.list_files": "toolListFiles", "coding.shell": "toolShell", "coding.go_test": "toolGoTest",
  "coding.gofmt": "toolGofmt", "coding.git_diff": "toolGitDiff", hydaelyn_activate_skill: "toolActivateSkill",
  "subagent.spawn": "toolSpawn", "subagent.get_output": "toolGetSubagentOutput", "subagent.kill": "toolStopSubagent",
};

export function translator(language: "en" | "zh-CN") {
  return (key: MessageKey) => messages[language][key] ?? messages["zh-CN"][key];
}

export function toolDisplayName(name: string, language: "en" | "zh-CN") {
  const key = toolNameKeys[name];
  return key ? translator(language)(key) : name;
}
