const messages = {
  "zh-CN": {
    newSession: "新会话", search: "搜索", runs: "运行", agents: "子智能体", extensions: "扩展",
    projects: "项目", workbench: "工作台", workspace: "工作区", settings: "设置",
    recovery: "恢复中心", promptTitle: "今天要在 Azem 中完成什么？",
    promptPlaceholder: "描述任务、附加图片或引用文件…", send: "发送", cancel: "取消",
    deliveryMode: "跟进消息行为", queue: "队列", guide: "引导", queuedMessages: "已排队消息",
    queuePlaceholder: "输入下一轮消息…", guidePlaceholder: "引导当前任务…",
    editMessage: "编辑消息", deleteMessage: "删除排队消息", moreActions: "排队消息操作",
    queueInterrupted: "队列因你中断任务而暂停", resumeQueue: "继续", retryMessage: "重试",
    queuedMessageFailed: "此排队消息发送失败", steerTooltip: "在不中断模型的情况下提交",
    turnOffQueueing: "关闭排队", turnOnQueueing: "开启排队",
    moveMessageUp: "上移消息", moveMessageDown: "下移消息", runningMessageMode: "跟进消息行为",
    runningMessageModeHint: "任务运行时可将跟进消息排队，或用于引导当前任务。按 Cmd/Ctrl+Shift+Enter 可单次使用相反模式。",
    handoff: "转交", actions: "操作", environment: "环境", environmentInfo: "环境信息", changes: "变更", context: "上下文",
    backgroundProcesses: "后台进程", backgroundTerminal: "后台终端", sources: "来源",
    todoTitle: "任务计划", todoGoal: "目标", todoProgress: "进度 {done}/{total}", todoOpen: "{count} 个待办",
    todoPending: "待处理", todoInProgress: "进行中",
    contextUsage: "上下文占用", contextRemaining: "剩余", contextUnavailable: "上下文用量暂不可用",
    runtimeHealthy: "Runtime 正常", noChanges: "暂无变更", clean: "无变更", branch: "分支", local: "本地",
    switchProject: "切换项目", noBranches: "无分支",
    searchBranches: "搜索分支", branchesSection: "分支",
    uncommittedFiles: "未提交：{count} 个文件",
    createCheckoutBranch: "创建并检出新分支…",
    newBranchPlaceholder: "新分支名称",
    createBranch: "创建",
    dirtySwitchConfirm: "工作区有未提交变更。仍要切换到 “{branch}” 吗？",
    noMatchingBranches: "没有匹配的分支",
    running: "运行中", ready: "就绪", failed: "失败", completed: "完成", cancelled: "已取消", idle: "空闲", queued: "排队中",
    // Codex-style approval modes
    autoReview: "替我审批", promptApproval: "请求批准", yolo: "完全访问权限",
    approvalMenuTitle: "应如何批准操作？",
    approvalAskHint: "编辑外部文件和使用互联网时始终询问",
    approvalAutoHint: "仅对检测到的风险操作请求批准",
    approvalFullHint: "可不受限制地访问互联网和您电脑上的任何文件",
    plan: "计划模式", planLabel: "计划",
    planHint: "只规划不实施：调查工作区并输出可执行计划",
    team: "团队模式", single: "默认模式", model: "模型", reasoning: "推理强度", approve: "允许", deny: "拒绝", approveOnce: "仅本次允许",
    roleModels: "角色模型", subagentRuntime: "子智能体运行", codexSubscription: "Codex 订阅", appearance: "界面",
    planModel: "规划模型", concurrency: "最大并发任务", fastMode: "Fast 模式", language: "语言",
    speed: "速度", standardSpeed: "标准", fastSpeed: "快速", fastModeHint: "快速模式仅对部分模型生效。",
    theme: "主题", light: "浅色", dark: "深色", system: "跟随系统", inspector: "环境信息",
    sideChat: "子智能体详情", closeSideChat: "关闭子智能体详情",
    noAgents: "当前会话还没有子智能体", noSessions: "还没有历史会话", command: "搜索命令或会话…",
    projectDetails: "项目详情", showMore: "展开显示", showLess: "收起显示",
    addProject: "添加项目", chooseProjectFolder: "选择项目文件夹", chooseProjectFolderHint: "打开已有文件夹", openProject: "打开项目",
    newProject: "新建项目", newProjectHint: "在 ~/Documents 创建文件夹", newProjectLocation: "项目将创建在 ~/Documents 中。",
    projectName: "项目名称", projectNamePlaceholder: "例如：my-app", createProject: "创建项目",
    keyboardTip: "↑↓ 选择 · Enter 打开 · Esc 关闭", loading: "正在加载 Azem…",
    attach: "添加图片", compact: "压缩会话", reviewChanges: "审查变更", resume: "继续会话",
    retry: "重试", reconnect: "重新连接", reload: "重新加载", modelInherit: "继承父智能体",
    approvalTitle: "需要确认", approvalTarget: "即将执行", approvalWorkspace: "当前工作区", approvalOperation: "受限操作",
    approvalWrite: "此操作将修改工作区内容。", approvalExternal: "此操作可能影响工作区之外的系统。",
    approvalReadOnly: "此操作将读取受保护内容。", approvalConfirm: "此操作需要你的确认。", approveSession: "本会话允许",
    riskLow: "低风险", riskMedium: "中风险", riskHigh: "高风险",
    toolGeneric: "调用工具", toolReadFile: "读取文件", toolReadArtifact: "读取工件", toolWriteFile: "写入文件", toolEditFile: "编辑文件",
    toolSearch: "搜索代码", toolListFiles: "列出文件", toolShell: "运行命令", toolGoTest: "运行 Go 测试", toolGofmt: "格式化 Go 代码",
    toolGitDiff: "查看 Git 差异", toolActivateSkill: "加载技能", toolSpawn: "启动子智能体", toolGetSubagentOutput: "获取子智能体输出", toolStopSubagent: "停止子智能体",
    // Timeline / process
    thinking: "思考", thinkingActive: "思考中", thought: "思考过程", thoughtFor: "思考了 {duration}", progressUpdate: "进度更新", processed: "已处理", processedFor: "已处理 {duration}", processing: "处理中",
    stoppedAfter: "你在 {duration} 后停止了", stopped: "你停止了运行",
    editedFiles: "已编辑的文件", editedFileCount: "已编辑 {count} 个文件", editedOneFile: "已编辑 1 个文件",
    editedFileDiff: "文件编辑差异", copyDiff: "复制差异", toolExecuting: "正在执行…",
    toolStatusRunning: "运行中", toolStatusFailed: "失败", toolStatusDone: "完成",
    jumpLatest: "返回最新",
    // Tool group summaries ({n} = count)
    toolGroupSearch: "搜索了 {n} 次", toolGroupRead: "读取了 {n} 个文件", toolGroupEdit: "编辑了 {n} 个文件",
    toolGroupDiff: "查看了 {n} 处差异", toolGroupShell: "运行了 {n} 条命令", toolGroupAgent: "调用了 {n} 个子智能体",
    toolGroupOtherSole: "调用了 {n} 个工具", toolGroupOther: "{n} 个其他工具",
    // Tool field labels
    fieldPath: "路径", fieldPaths: "路径", fieldQuery: "查询", fieldCommand: "命令", fieldCwd: "目录",
    fieldArtifact: "工件", fieldSkill: "技能", fieldDetail: "说明",
    // Side chat / agents
    syncingAgentTimeline: "正在同步子智能体时间线…", emptySideChat: "此子智能体还没有消息",
    subagents: "子智能体", inheritModel: "继承父智能体模型", toolsLabel: "工具",
    openSubagents: "查看子智能体", closeSubagents: "关闭子智能体",
    subagentsStarted: "已启动 {count} 个子智能体", subagentsRunning: "{count} 个运行中", subagentsRunningQueued: "{running} 个运行中 · {queued} 个排队中",
    subagentsQueued: "{count} 个排队中", subagentsFinished: "{count} 个已结束", subagentsShowMore: "再显示 {count} 个", subagentsShowLess: "收起",
    subagentNoPreview: "等待进度更新",
    noAttachments: "还没有图片附件",
    agentInitializing: "正在启动", agentCompleted: "已完成", agentFailed: "失败", agentCancelling: "正在停止", agentCancelled: "已取消", agentInterrupted: "已中断", agentQueued: "排队中", agentIdle: "空闲",
    // Composer / slash
    slashCommands: "斜杠命令", skills: "技能", skillUseHint: "使用此技能处理当前任务",
    skillBuiltin: "内置", skillPersonal: "个人", agentMode: "代理模式",
    // Reasoning levels
    reasoningMinimal: "最小", reasoningLow: "轻度", reasoningMedium: "中", reasoningHigh: "高",
    reasoningXHigh: "极高", reasoningMax: "最高", reasoningUltra: "超高",
    reasoningMaxHint: "更快消耗使用额度",
    reasoningFaster: "更高效", reasoningSmarter: "更智能", reasoningAdvanced: "高级",
    reasoningBack: "返回",
    fastBoostTitle: "1.5 倍速", fastBoostDetail: "用量更多",
    // Sidebar / sessions
    renameChat: "重命名聊天", unread: "未读", sessionActionFailed: "会话操作失败",
    // Pull requests
    pullRequests: "拉取请求", currentPullRequest: "当前分支", createdByMe: "我创建的",
    needsMyReview: "请求我审查", allOpenPullRequests: "所有开放 PR", noPullRequests: "没有开放的拉取请求",
    noCurrentPullRequest: "当前分支没有关联的开放 PR", pullRequestDetails: "拉取请求详情",
    loadingPullRequests: "正在读取 GitHub…", refreshPullRequests: "刷新拉取请求",
    prUnavailable: "GitHub 拉取请求不可用", prNotInstalled: "请先安装 GitHub CLI（gh），然后重试。",
    prUnauthenticated: "请运行 gh auth login 登录 GitHub，然后重试。", prNoRepository: "当前项目没有可识别的 GitHub 远程仓库。",
    prOffline: "暂时无法连接 GitHub，请检查网络后重试。", openGitHub: "在 GitHub 中打开",
    closePullRequestPanel: "关闭拉取请求面板", closeDialog: "关闭对话框", description: "描述", checks: "检查", activityLog: "活动", comments: "评论",
    noComments: "暂无评论", noChecks: "无 CI 检查", requestedReviewers: "审查者", requestReview: "请求审查",
    removeReviewer: "移除审查者", reviewerPlaceholder: "GitHub 用户名或组织/团队",
    editPullRequest: "编辑 PR", prTitle: "标题", prBody: "描述", postComment: "发表评论",
    commentPlaceholder: "留下评论…", review: "审查", approvePR: "批准", requestChanges: "请求修改",
    reviewComment: "仅评论", reviewBodyPlaceholder: "填写审查说明…", readyForReview: "可供审查",
    convertToDraft: "转为草稿", closePullRequest: "关闭 PR", reopenPullRequest: "重新打开 PR",
    monitorAndFixPR: "监控并修复 PR", stopMonitoring: "停止监控", monitorWatching: "正在监控",
    monitorPending: "等待修复条件", monitorRepairing: "正在自动修复", monitorCompleted: "修复会话已完成",
    monitorError: "监控出错", openRepairSession: "打开修复会话", merge: "合并",
    enableAutoMerge: "启用自动合并", disableAutoMerge: "关闭自动合并", mergeCommit: "创建合并提交",
    squashMerge: "压缩并合并", rebaseMerge: "变基并合并", confirmMerge: "确认合并",
    confirmExternalAction: "确认 GitHub 操作", mergeWarning: "此操作会修改 GitHub，且合并后不可撤销。",
    changedFiles: "个文件", reviewers: "审查", ciChecks: "检查", openStatus: "开放",
    draftStatus: "草稿", closedStatus: "已关闭", mergedStatus: "已合并",
    checkPending: "进行中", checkPassing: "通过", checkFailing: "失败",
    // Settings
    searchSettings: "搜索设置…", closeSettings: "关闭设置", save: "保存", refresh: "刷新",
    backToApp: "返回应用", loadingRoles: "正在加载可配置角色…",
    resetInherit: "恢复继承",
    routeTitle: "会话标题", routePlan: "计划模式", routeCompaction: "上下文压缩",
    routeTitleHint: "根据首轮用户消息自动生成侧栏会话标题。",
    routePlanHint: "调查工作区并输出可执行计划，不修改文件。",
    routeCompactionHint: "长会话压缩与上下文续接使用的模型。",
    routeSubagentHint: "{role} 子智能体的模型路由。",
    settingsModels: "模型路由", settingsModelsHint: "为会话标题、计划模式、上下文压缩和每个子智能体选择独立模型；不设置时继承当前会话。",
    settingsSubagents: "子智能体运行", settingsSubagentsHint: "控制任务的并发上限，超出上限的任务会自动排队。",
    settingsGovernance: "运行治理", settingsGovernanceHint: "配置默认审批方式以及主任务运行时的新消息处理方式。",
    settingsAppearance: "界面", settingsAppearanceHint: "调整应用语言与显示主题。",
    settingsExtensions: "Skills 与扩展", settingsExtensionsHint: "查看运行时发现的本地能力目录。",
    settingsNavModels: "计划、压缩与各子智能体模型", settingsNavSubagents: "并发与任务调度",
    settingsNavGovernance: "审批与运行中消息", settingsNavAppearance: "语言与主题",
    settingsNavExtensions: "运行时能力目录",
    defaultApprovalMode: "默认审批模式", defaultApprovalModeHint: "高风险操作仍由 Go 运行时执行最终校验。",
    languageHint: "同步修改运行时语言与桌面界面。", themeHint: "只保存桌面视觉偏好，不改变运行时配置。",
    concurrencyHint: "最多同时运行的子智能体数量。",
    noSkills: "没有发现可用 Skill。", skillDisabled: "已停用", skillEager: "默认加载", skillOnDemand: "按需加载",
    routeInherited: "继承当前会话", routeCustom: "独立设置",
    provider: "提供商", reasoningEffort: "推理强度",
    langZh: "简体中文", langEn: "English",
    // Errors / misc
    runFailed: "运行失败", change: "变更", needApproval: "需要审批",
    waiting: "等待", viewThread: "查看会话 →", recentRun: "最近一次运行",
    status: "状态", toolCalls: "工具调用", pendingApprovals: "待审批",
    timelineBlocks: "{blocks} 个时间线块 · {tools} 次工具调用",
  },
  en: {
    newSession: "New thread", search: "Search", runs: "Runs", agents: "Agents", extensions: "Extensions",
    projects: "Projects", workbench: "Workbench", workspace: "Workspace", settings: "Settings",
    recovery: "Recovery", promptTitle: "What do you want to build in Azem?",
    promptPlaceholder: "Describe a task, attach an image, or reference a file…", send: "Send", cancel: "Cancel",
    deliveryMode: "Follow-up behavior", queue: "Queue", guide: "Steer", queuedMessages: "Queued messages",
    queuePlaceholder: "Add a message for the next turn…", guidePlaceholder: "Steer the current task…",
    editMessage: "Edit message", deleteMessage: "Delete queued message", moreActions: "Queued message actions",
    queueInterrupted: "Queue paused because you interrupted", resumeQueue: "Resume", retryMessage: "Retry",
    queuedMessageFailed: "This queued message could not be sent", steerTooltip: "Submit without interrupting the model",
    turnOffQueueing: "Turn off queueing", turnOnQueueing: "Turn on queueing",
    moveMessageUp: "Move message up", moveMessageDown: "Move message down", runningMessageMode: "Follow-up behavior",
    runningMessageModeHint: "Queue follow-ups while Azem runs or steer the current run. Press Cmd/Ctrl+Shift+Enter to do the opposite for one message.",
    handoff: "Handoff", actions: "Actions", environment: "Environment", environmentInfo: "Environment", changes: "Changes", context: "Context",
    backgroundProcesses: "Background processes", backgroundTerminal: "Background terminal", sources: "Sources",
    todoTitle: "Task plan", todoGoal: "Goal", todoProgress: "Progress {done}/{total}", todoOpen: "{count} open tasks",
    todoPending: "Pending", todoInProgress: "In progress",
    contextUsage: "Context usage", contextRemaining: "Remaining", contextUnavailable: "Context usage unavailable",
    runtimeHealthy: "Runtime healthy", noChanges: "No changes", clean: "Clean", branch: "Branch", local: "Local",
    switchProject: "Switch project", noBranches: "No branches",
    searchBranches: "Search branches", branchesSection: "Branches",
    uncommittedFiles: "Uncommitted: {count} files",
    createCheckoutBranch: "Create and check out new branch…",
    newBranchPlaceholder: "New branch name",
    createBranch: "Create",
    dirtySwitchConfirm: "Workspace has uncommitted changes. Switch to “{branch}” anyway?",
    noMatchingBranches: "No matching branches",
    running: "Running", ready: "Ready", failed: "Failed", completed: "Completed", cancelled: "Cancelled", idle: "Idle", queued: "Queued",
    // Codex-style approval modes
    autoReview: "On your behalf", promptApproval: "Ask for approval", yolo: "Full access",
    approvalMenuTitle: "How should operations be approved?",
    approvalAskHint: "Always ask when editing outside files or using the internet",
    approvalAutoHint: "Only ask for approval on detected risky operations",
    approvalFullHint: "Unrestricted access to the internet and any file on your computer",
    plan: "Plan mode", planLabel: "Plan",
    planHint: "Plan without implementing: inspect the workspace and produce an actionable plan",
    team: "Team mode", single: "Default mode", model: "Model", reasoning: "Reasoning effort", approve: "Allow", deny: "Deny", approveOnce: "Allow once",
    roleModels: "Role models", subagentRuntime: "Subagent runtime", codexSubscription: "Codex subscription", appearance: "Appearance",
    planModel: "Plan model", concurrency: "Max concurrent tasks", fastMode: "Fast mode", language: "Language",
    speed: "Speed", standardSpeed: "Standard", fastSpeed: "Fast", fastModeHint: "Fast mode only applies to some models.",
    theme: "Theme", light: "Light", dark: "Dark", system: "System", inspector: "Environment",
    sideChat: "Subagent details", closeSideChat: "Close subagent details",
    noAgents: "No subagents for this thread", noSessions: "No previous threads", command: "Search commands or threads…",
    projectDetails: "Project details", showMore: "Show more", showLess: "Show less",
    addProject: "Add project", chooseProjectFolder: "Choose project folder", chooseProjectFolderHint: "Open an existing folder", openProject: "Open project",
    newProject: "New project", newProjectHint: "Create a folder in ~/Documents", newProjectLocation: "The project will be created in ~/Documents.",
    projectName: "Project name", projectNamePlaceholder: "For example: my-app", createProject: "Create project",
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
    thinking: "Thinking", thinkingActive: "Thinking", thought: "Thought", thoughtFor: "Thought for {duration}", progressUpdate: "Progress update", processed: "Worked", processedFor: "Worked for {duration}", processing: "Working",
    stoppedAfter: "You stopped after {duration}", stopped: "You stopped the run",
    editedFiles: "Edited files", editedFileCount: "Edited {count} files", editedOneFile: "Edited 1 file",
    editedFileDiff: "File edit diff", copyDiff: "Copy diff", toolExecuting: "Running…",
    toolStatusRunning: "Running", toolStatusFailed: "Failed", toolStatusDone: "Done",
    jumpLatest: "Jump to latest",
    toolGroupSearch: "Searched {n} times", toolGroupRead: "Read {n} files", toolGroupEdit: "Edited {n} files",
    toolGroupDiff: "Viewed {n} diffs", toolGroupShell: "Ran {n} commands", toolGroupAgent: "Ran {n} agent tasks",
    toolGroupOtherSole: "Used {n} tools", toolGroupOther: "{n} other tool calls",
    fieldPath: "Path", fieldPaths: "Paths", fieldQuery: "Query", fieldCommand: "Command", fieldCwd: "Cwd",
    fieldArtifact: "Artifact", fieldSkill: "Skill", fieldDetail: "Detail",
    syncingAgentTimeline: "Syncing subagent timeline…", emptySideChat: "This subagent has no messages yet",
    subagents: "Subagents", inheritModel: "inherit parent model", toolsLabel: "tools",
    openSubagents: "Open subagents", closeSubagents: "Close subagents",
    subagentsStarted: "Started {count} subagents", subagentsRunning: "{count} running", subagentsRunningQueued: "{running} running · {queued} queued",
    subagentsQueued: "{count} queued", subagentsFinished: "{count} finished", subagentsShowMore: "Show {count} more", subagentsShowLess: "Show less",
    subagentNoPreview: "Waiting for a progress update",
    noAttachments: "No image attachments yet",
    agentInitializing: "starting", agentCompleted: "completed", agentFailed: "failed", agentCancelling: "stopping", agentCancelled: "cancelled", agentInterrupted: "interrupted", agentQueued: "queued", agentIdle: "idle",
    slashCommands: "Slash commands", skills: "Skills", skillUseHint: "Use this skill for the current task",
    skillBuiltin: "Built-in", skillPersonal: "Personal", agentMode: "Agent mode",
    reasoningMinimal: "Minimal", reasoningLow: "Low", reasoningMedium: "Medium", reasoningHigh: "High",
    reasoningXHigh: "Extra high", reasoningMax: "Maximum", reasoningUltra: "Ultra",
    reasoningMaxHint: "Uses your limit faster",
    reasoningFaster: "More efficient", reasoningSmarter: "Smarter", reasoningAdvanced: "Advanced",
    reasoningBack: "Back",
    fastBoostTitle: "1.5× speed", fastBoostDetail: "Uses more quota",
    renameChat: "Rename chat", unread: "Unread", sessionActionFailed: "Session action failed",
    // Pull requests
    pullRequests: "Pull requests", currentPullRequest: "Current branch", createdByMe: "Created by me",
    needsMyReview: "Review requested", allOpenPullRequests: "All open pull requests", noPullRequests: "No open pull requests",
    noCurrentPullRequest: "The current branch has no open pull request", pullRequestDetails: "Pull request details",
    loadingPullRequests: "Reading GitHub…", refreshPullRequests: "Refresh pull requests",
    prUnavailable: "GitHub pull requests unavailable", prNotInstalled: "Install GitHub CLI (gh), then retry.",
    prUnauthenticated: "Run gh auth login, then retry.", prNoRepository: "This project has no recognized GitHub remote.",
    prOffline: "GitHub is temporarily unreachable. Check the network and retry.", openGitHub: "Open on GitHub",
    closePullRequestPanel: "Close pull request panel", closeDialog: "Close dialog", description: "Description", checks: "Checks", activityLog: "Activity", comments: "Comments",
    noComments: "No comments", noChecks: "No CI checks", requestedReviewers: "Reviewers", requestReview: "Request review",
    removeReviewer: "Remove reviewer", reviewerPlaceholder: "GitHub login or organization/team",
    editPullRequest: "Edit PR", prTitle: "Title", prBody: "Description", postComment: "Post comment",
    commentPlaceholder: "Leave a comment…", review: "Review", approvePR: "Approve", requestChanges: "Request changes",
    reviewComment: "Comment only", reviewBodyPlaceholder: "Add review notes…", readyForReview: "Ready for review",
    convertToDraft: "Convert to draft", closePullRequest: "Close PR", reopenPullRequest: "Reopen PR",
    monitorAndFixPR: "Monitor and fix PR", stopMonitoring: "Stop monitoring", monitorWatching: "Monitoring",
    monitorPending: "Waiting to repair", monitorRepairing: "Repairing automatically", monitorCompleted: "Repair session completed",
    monitorError: "Monitor error", openRepairSession: "Open repair session", merge: "Merge",
    enableAutoMerge: "Enable auto-merge", disableAutoMerge: "Disable auto-merge", mergeCommit: "Create merge commit",
    squashMerge: "Squash and merge", rebaseMerge: "Rebase and merge", confirmMerge: "Confirm merge",
    confirmExternalAction: "Confirm GitHub action", mergeWarning: "This changes GitHub and a merge cannot be undone.",
    changedFiles: "files", reviewers: "Reviews", ciChecks: "Checks", openStatus: "Open",
    draftStatus: "Draft", closedStatus: "Closed", mergedStatus: "Merged",
    checkPending: "Pending", checkPassing: "Passing", checkFailing: "Failing",
    searchSettings: "Search settings…", closeSettings: "Close settings", save: "Save", refresh: "Refresh",
    backToApp: "Back to app", loadingRoles: "Loading configurable roles…",
    resetInherit: "Reset to inherit",
    routeTitle: "Session titles", routePlan: "Plan mode", routeCompaction: "Context compaction",
    routeTitleHint: "Automatically name the sidebar thread from the first user message.",
    routePlanHint: "Survey the workspace and produce an executable plan without editing files.",
    routeCompactionHint: "Model used for long-session compaction and context continuation.",
    routeSubagentHint: "Model route for the {role} subagent.",
    settingsModels: "Model routes", settingsModelsHint: "Pick independent models for session titles, plan mode, compaction, and each subagent; unset routes inherit the current session.",
    settingsSubagents: "Subagent runtime", settingsSubagentsHint: "Cap concurrent subagent tasks; extras queue automatically.",
    settingsGovernance: "Run governance", settingsGovernanceHint: "Default approval policy and how new messages are handled while a turn is running.",
    settingsAppearance: "Appearance", settingsAppearanceHint: "Language and display theme.",
    settingsExtensions: "Skills & extensions", settingsExtensionsHint: "Local capabilities discovered by the runtime.",
    settingsNavModels: "Plan, compaction, and subagent models", settingsNavSubagents: "Concurrency and scheduling",
    settingsNavGovernance: "Approvals and in-run messages", settingsNavAppearance: "Language and theme",
    settingsNavExtensions: "Runtime capability catalog",
    defaultApprovalMode: "Default approval mode", defaultApprovalModeHint: "High-risk operations are still enforced by the Go runtime.",
    languageHint: "Updates both the runtime language and the desktop UI.", themeHint: "Desktop visual preference only; does not change runtime config.",
    concurrencyHint: "Maximum number of subagents that may run at once.",
    noSkills: "No skills discovered.", skillDisabled: "Disabled", skillEager: "Eager", skillOnDemand: "On demand",
    routeInherited: "Inherit session", routeCustom: "Custom",
    provider: "Provider", reasoningEffort: "Reasoning",
    langZh: "简体中文", langEn: "English",
    runFailed: "Run failed", change: "Change", needApproval: "Needs approval",
    waiting: "Waiting", viewThread: "Open thread →", recentRun: "Latest run",
    status: "Status", toolCalls: "Tool calls", pendingApprovals: "Pending approvals",
    timelineBlocks: "{blocks} timeline blocks · {tools} tool calls",
  },
} as const;

export type MessageKey = keyof (typeof messages)["zh-CN"];
export type Language = "en" | "zh-CN";

const toolNameKeys: Record<string, MessageKey> = {
  "coding.read_file": "toolReadFile", "context.read_artifact": "toolReadArtifact", "coding.write_file": "toolWriteFile", "coding.edit_hashline": "toolEditFile",
  "coding.search": "toolSearch", "coding.list_files": "toolListFiles", "coding.shell": "toolShell", "coding.go_test": "toolGoTest",
  "coding.gofmt": "toolGofmt", "coding.git_diff": "toolGitDiff", hydaelyn_activate_skill: "toolActivateSkill",
  "subagent.spawn": "toolSpawn", "subagent.get_output": "toolGetSubagentOutput", "subagent.kill": "toolStopSubagent",
};

export function translator(language: Language) {
  return (key: MessageKey): string => messages[language][key] ?? messages["zh-CN"][key];
}

export function tFormat(language: Language, key: MessageKey, vars: Record<string, string | number>): string {
  let text: string = translator(language)(key);
  for (const [name, value] of Object.entries(vars)) {
    text = text.replaceAll(`{${name}}`, String(value));
  }
  return text;
}

export function toolDisplayName(name: string, language: Language) {
  const key = toolNameKeys[name];
  return key ? translator(language)(key) : name;
}

/** Canonical low → high order (Codex-style). */
export const REASONING_LEVEL_ORDER = ["minimal", "low", "medium", "high", "xhigh", "max", "ultra"] as const;

export function sortReasoningLevels(levels: string[]): string[] {
  return [...new Set(levels.filter(Boolean))].sort((a, b) => {
    const ia = (REASONING_LEVEL_ORDER as readonly string[]).indexOf(a);
    const ib = (REASONING_LEVEL_ORDER as readonly string[]).indexOf(b);
    if (ia === -1 && ib === -1) return a.localeCompare(b);
    if (ia === -1) return 1;
    if (ib === -1) return -1;
    return ia - ib;
  });
}

export function reasoningLabel(level: string, language: Language) {
  const map: Record<string, MessageKey> = {
    minimal: "reasoningMinimal", low: "reasoningLow", medium: "reasoningMedium",
    high: "reasoningHigh", xhigh: "reasoningXHigh", max: "reasoningMax", ultra: "reasoningUltra",
  };
  const key = map[level];
  return key ? translator(language)(key) : level;
}

export function reasoningHint(level: string, language: Language): string | undefined {
  // Codex shows the usage-limit hint from 极高 (xhigh) upward.
  if (level === "xhigh" || level === "max" || level === "ultra") return translator(language)("reasoningMaxHint");
  return undefined;
}

export type ToolCategory = "search" | "read" | "edit" | "diff" | "shell" | "agent" | "other";

export function toolGroupLabel(category: ToolCategory, count: number, language: Language, sole: boolean) {
  const key: MessageKey = category === "search" ? "toolGroupSearch"
    : category === "read" ? "toolGroupRead"
      : category === "edit" ? "toolGroupEdit"
        : category === "diff" ? "toolGroupDiff"
          : category === "shell" ? "toolGroupShell"
            : category === "agent" ? "toolGroupAgent"
              : sole ? "toolGroupOtherSole" : "toolGroupOther";
  return tFormat(language, key, { n: count });
}
