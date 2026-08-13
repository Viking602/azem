/** All Azem session scenarios for the unified card-style prototype. */

const agents = [
  {
    id: "a-ux", face: "UX", faceClass: "ux", name: "信息架构审查",
    type: "review", model: "gpt-5.6-sol · 中", capability: "只读",
    state: "completed", stateLabel: "已完成",
    preview: "主栏保留任务 / 答案 / 中断卡", elapsed: "2m18s",
    tokens: "4.2k", toolsCount: 5,
    prompt: "审查非 Timeline 会话的信息架构：哪些块必须留在主栏，哪些应进过程区或 Inspector。请给出可执行的边界清单。",
    notes: [
      { title: "对照现有块类型", body: "user / thinking / commentary / assistant / tool / approval / question / plan / error。" },
      { title: "区分决策与过程", body: "用户必须看见并操作的内容不能进折叠；工具与推理属于证据面。" },
    ],
    process: [
      { name: "读取", preview: "Timeline.tsx · sessionDocument.ts", state: "done" },
      { name: "搜索", preview: "BlockKind · interrupt", state: "done" },
      { name: "读取", preview: "AgentSideChat.tsx", state: "done" },
      { name: "搜索", preview: "isProcessBlock · segmentProcessTrail", state: "done" },
      { name: "读取", preview: "docs/desktop.md", state: "done" },
    ],
    processLabel: "已处理", processTime: "1m42s", processOpen: false,
    answerSections: [
      {
        h: "主栏必须保留",
        html: `<ul>
<li><strong>任务</strong>：最新用户要求（及附件）</li>
<li><strong>答案</strong>：final_answer，流式原位成稿</li>
<li><strong>中断卡</strong>：approval / question / plan / error</li>
</ul>`,
      },
      {
        h: "过程折叠",
        html: `<p>thinking、commentary、tool 进入「处理中 / 已处理」。完成后默认收起，可再打开回看。</p>`,
      },
      {
        h: "侧栏",
        html: `<p>文件变更、执行计划（Todo）、子智能体摘要继续放在 Inspector / 子智能体中心，不与正文抢主轴。</p>`,
      },
    ],
  },
  {
    id: "a-ui", face: "UI", faceClass: "ui", name: "视觉一致性检查",
    type: "review", model: "gpt-5.6-sol · 高", capability: "只读",
    state: "running", stateLabel: "运行中",
    preview: "核对卡片密度与状态色…", elapsed: "1m04s",
    tokens: "2.8k", toolsCount: 4,
    prompt: "检查协作卡片与侧栏详情页是否匹配 Azem 暖白工作台：间距、状态色、进度条、字号层级。不要发明第二套视觉系统。",
    notes: [
      { title: "对齐产品组件", body: "以 subagent-run-card 与 AgentSideChat 现网样式为基准，而不是概念画板。" },
      { title: "运行态提示", body: "运行中用蓝色描边与进度条即可，避免额外磁贴或彩条。" },
    ],
    process: [
      { name: "读取", preview: "frontend/src/prototype.css", state: "done" },
      { name: "读取", preview: "AgentSideChat.tsx", state: "done" },
      { name: "读取", preview: "Timeline.tsx · SubagentRunCard", state: "done" },
      { name: "搜索", preview: "subagent-run · status colors", state: "running", status: "运行中" },
    ],
    processLabel: "处理中", processTime: "1m04s", processOpen: true,
    answerSections: [
      {
        h: "已确认",
        html: `<ul>
<li>协作卡片保留四向边框、浅渐变与计数徽章</li>
<li>详情页正文与主会话同构：用户气泡 + 过程折叠 + 最终回答</li>
</ul>`,
      },
      {
        h: "进行中",
        html: `<p>正在核对运行态/完成态/失败态的状态色与进度条对比度，稍后补完整清单。</p>`,
      },
    ],
  },
  {
    id: "a-qa", face: "QA", faceClass: "qa", name: "场景覆盖检查",
    type: "review", model: "gpt-5.6-sol · 中", capability: "只读",
    state: "queued", stateLabel: "排队中",
    preview: "等待并发槽位…", elapsed: "—",
    tokens: "—", toolsCount: 0,
    prompt: "覆盖完成 / 运行中 / 失败 / 空 transcript 四类子智能体状态，并列出遗漏场景。",
    notes: [],
    process: [],
    processLabel: "排队", processTime: "", processOpen: false,
    answerSections: [],
    queuedReason: "子智能体并发上限 2；前两个审查仍在占用槽位。",
  },
  {
    id: "a-sec", face: "SEC", faceClass: "sec", name: "安全边界审查",
    type: "review", model: "gpt-5.6-sol · 高", capability: "只读",
    state: "completed", stateLabel: "已完成",
    preview: "审批中断不得折叠进过程", elapsed: "3m11s",
    tokens: "5.1k", toolsCount: 6,
    prompt: "确认审批、自动审查失败与运行错误在非 Timeline 投影中仍然可见、可操作，且不会被过程折叠吞掉。",
    notes: [
      { title: "对照审批路径", body: "resolve_approval · deny / once / session。" },
      { title: "失败可见性", body: "error 块与失败工具状态必须同时可到达。" },
    ],
    process: [
      { name: "读取", preview: "ApprovalBlock · approvalPresentation", state: "done" },
      { name: "搜索", preview: "awaiting_approval · reviewing_approval", state: "done" },
      { name: "读取", preview: "provider_approval.go", state: "done" },
      { name: "搜索", preview: "error block · CancelActive", state: "done" },
      { name: "读取", preview: "docs/security.md", state: "done" },
      { name: "搜索", preview: "Automatic review failed", state: "done" },
    ],
    processLabel: "已处理", processTime: "2m40s", processOpen: false,
    answerSections: [
      {
        h: "必须留在主栏",
        html: `<ul>
<li><code>approval</code>：未决策前不可塞进「已处理」</li>
<li><code>error</code>：停止/失败原因要可重试</li>
<li>自动审查 parse 失败：fail-closed，不得静默执行</li>
</ul>`,
      },
      {
        h: "过程区允许",
        html: `<p>工具的 queued / awaiting_approval / running / failed 状态可在过程折叠中展示，但<strong>写操作未批准前</strong>不得进入文件变更投影。</p>`,
      },
      {
        h: "结论",
        html: `<p>当前边界可用。子智能体失败要在协作卡片与中心页同时可见，避免只出现在 Inspector。</p>`,
      },
    ],
  },
];

const scenarios = {
  complete: {
    id: "complete",
    label: "完成",
    title: "定位 MCP -32005 运行失败",
    runState: "completed",
    runLabel: "就绪",
    planMode: false,
    contextPct: 42,
    user: "运行时偶发 jsonrpc error -32005，整轮直接失败。帮我定位根因并修掉。",
    process: {
      open: false,
      label: "已处理",
      time: "24m14s",
      notes: [
        { title: "定位传输拒绝根因", body: "核对调用链与错误触发条件。" },
        { title: "根因已确认", body: "manager.go:684–688 将瞬时拒绝升格为执行级错误。" },
      ],
      group: "搜索 3 · 读取 2 · 命令 2 · 编辑 1",
      tools: [
        { name: "搜索", preview: "jsonrpc · -32005", state: "done" },
        { name: "读取", preview: "internal/mcp/manager.go", state: "done" },
        { name: "编辑", preview: "manager.go · 降级 -32005", state: "done" },
        { name: "命令", preview: "go test ./internal/mcp/…", state: "done" },
      ],
    },
    answer: {
      lead: "根因与修复已落地，并通过红灯 / 绿灯与端到端守卫。",
      sections: [
        {
          h: "结论",
          html: `<p>根因在 <code>internal/mcp/manager.go:684–688</code>：MCP 瞬时传输拒绝被当作执行级错误返回。</p>
<p>修复仅降级 <code>RPCError.Code == -32005</code>；其它故障仍中止。</p>`,
        },
      ],
    },
    edited: {
      count: 2,
      plus: 112,
      minus: 8,
      files: [
        { path: "internal/mcp/manager.go", plus: "+18", minus: "−6" },
        { path: "internal/mcp/manager_test.go", plus: "+94", minus: "−2" },
      ],
    },
    todos: [
      { text: "定位根因", done: true },
      { text: "实现可恢复边界", done: true },
      { text: "红灯 / 绿灯", done: true },
      { text: "端到端守卫", done: true },
    ],
  },

  running: {
    id: "running",
    label: "运行中",
    title: "优化 Azem 的 UI 动效",
    runState: "running",
    runLabel: "运行中",
    contextPct: 38,
    user: "优化 App / Sidebar / Timeline / Inspector 的切换与流式动效。",
    process: {
      open: true,
      label: "处理中",
      time: "2m14s",
      notes: [
        { title: "提取视觉与动效基线", body: "暖白纸面、流式尾部节点、reduced motion。" },
        { title: "构建高保真交互原型", body: "工具状态用轨迹推进，流式文字尾部显现。" },
      ],
      tools: [
        { name: "读取", preview: "styles.css", state: "done" },
        { name: "编辑", preview: "azem-ui-motion-concept/…", state: "running", status: "运行中" },
      ],
    },
    answer: {
      streaming: "建议把视觉方向定义为“静谧机械感”。保留暖白纸面和橙色品牌点…",
    },
    todos: [
      { text: "分析当前界面", done: true },
      { text: "建立动效 token", done: false, active: true },
      { text: "制作完整页面", done: false },
    ],
  },

  plan: {
    id: "plan",
    label: "计划",
    title: "把会话迁到语义文档投影",
    runState: "idle",
    runLabel: "计划模式",
    planMode: true,
    contextPct: 18,
    user: "如果不使用 Timeline，会话区如何组织？先只规划不实施。",
    process: {
      open: false,
      label: "已处理",
      time: "36s",
      notes: [{ title: "收集约束", body: "读取 Timeline 与会话持久化边界。" }],
      tools: [
        { name: "读取", preview: "Timeline.tsx", state: "done" },
        { name: "搜索", preview: "segmentProcessTrail", state: "done" },
      ],
    },
    planning: {
      title: "需要你的选择",
      questions: [
        {
          header: "默认阅读模式",
          question: "主会话默认用哪种组织方式？",
          options: [
            { label: "工作文档", desc: "正文稳定，过程折叠旁置", recommended: true },
            { label: "回合卡片", desc: "一问一答，过程作附件" },
            { label: "会话笔记本", desc: "长任务分章导航" },
          ],
        },
        {
          header: "过程默认",
          question: "完成后的过程折叠默认状态？",
          options: [
            { label: "收起", desc: "答案先行，需要再展开", recommended: true },
            { label: "展开最近一次", desc: "保留最新过程可见" },
          ],
        },
      ],
    },
    plan: {
      title: "实施计划",
      version: "v2",
      state: "proposed",
      steps: [
        "新增 SessionDocument 投影层，不改 block 持久化顺序",
        "主栏渲染 task / answer / interrupt（含 plan / question）",
        "过程折叠改为 note + tool row，去掉竖轨",
        "子智能体保留协作卡片 + 中心页 + 详情页",
        "用现有 Timeline 回归测试 + 新增 document 投影测试兜住",
      ],
    },
    todos: [
      { text: "确认阅读模式", done: false, active: true },
      { text: "确认过程默认", done: false },
      { text: "用户批准后执行", done: false },
    ],
  },

  approval: {
    id: "approval",
    label: "审批",
    title: "写入配置前需要确认",
    runState: "running",
    runLabel: "等待确认",
    contextPct: 22,
    user: "把默认审批策略改成 session，并更新配置说明。",
    process: {
      open: true,
      label: "处理中",
      time: "48s",
      notes: [{ title: "读取配置边界", body: "确认 approvalMode 写入路径。" }],
      tools: [
        { name: "读取", preview: "internal/config/config.go", state: "done" },
        { name: "编辑", preview: "config.yaml", state: "awaiting", status: "待审批" },
      ],
    },
    approval: {
      title: "apply_patch · 写入工作区",
      target: "config.yaml",
      body: "此操作将修改工作区内容。未批准前不投影为已编辑文件。",
    },
    todos: [
      { text: "读取配置", done: true },
      { text: "等待写入授权", done: false, active: true },
    ],
  },

  subagents: {
    id: "subagents",
    label: "子智能体",
    title: "并行审查非 Timeline 草稿",
    runState: "running",
    runLabel: "运行中",
    contextPct: 51,
    user: "并行审查信息架构、视觉一致性和场景覆盖。主会话只保留汇总结论。",
    process: {
      open: true,
      label: "处理中",
      time: "3m02s",
      notes: [{ title: "分派专项审查", body: "覆盖架构、视觉、场景与安全边界。" }],
      tools: [],
    },
    agents,
    answer: {
      lead: "主会话汇总进度；子智能体协作使用专用卡片。",
      sections: [
        {
          h: "当前状态",
          html: `<ul>
<li>信息架构：已完成</li>
<li>视觉一致性：运行中</li>
<li>场景覆盖：排队</li>
<li>安全边界：已完成</li>
</ul>`,
        },
      ],
    },
    todos: [
      { text: "启动审查子智能体", done: true },
      { text: "汇总审查结果", done: false, active: true },
    ],
  },

  multiturn: {
    id: "multiturn",
    label: "多轮·完成",
    title: "分析超长 JSON 加载性能",
    runState: "completed",
    runLabel: "就绪",
    contextPct: 19,
    history: [
      {
        n: "01",
        q: "工作区文件预览会卡死吗？",
        a: "有 2MiB 上限，不会整文件进内存。",
        process: {
          open: false,
          label: "已处理",
          time: "1m08s",
          notes: [
            { title: "定位预览路径", body: "GUI 工作区文件预览有硬上限。" },
          ],
          group: "搜索 1 · 读取 2",
          tools: [
            { name: "搜索", preview: "maxPreviewBytes", state: "done" },
            { name: "读取", preview: "workspace_files.go:22", state: "done" },
            { name: "读取", preview: "WorkspaceFilesPage.tsx", state: "done" },
          ],
        },
        answer: {
          lead: "预览路径安全。",
          sections: [
            {
              h: "结论",
              html: `<p><code>maxPreviewBytes = 2 MiB</code>。大 JSON 只读前缀，不会卡死主线程。</p>`,
            },
          ],
        },
      },
      {
        n: "02",
        q: "Agent 的 coding.read_file 呢？",
        a: "默认 1MiB，最大 16MiB，超限拒绝。",
        process: {
          open: false,
          label: "已处理",
          time: "52s",
          notes: [
            { title: "核对 venat 读文件边界", body: "defaultMaxReadBytes / defaultMaxFileBytes。" },
          ],
          tools: [
            { name: "搜索", preview: "defaultMaxReadBytes", state: "done" },
            { name: "读取", preview: "coding/workspace.go", state: "done" },
          ],
        },
        answer: {
          lead: "Agent 读文件同样有硬上限。",
          sections: [
            {
              h: "边界",
              html: `<ul><li>默认 1 MiB</li><li>最大 16 MiB</li><li>超限直接拒绝，不整文件加载</li></ul>`,
            },
          ],
        },
      },
    ],
    user: "综合一下：加载 50m / 100m json 会不会卡顿？把各路径列清楚。",
    process: {
      open: false,
      label: "已处理",
      time: "2m14s",
      notes: [
        { title: "汇总三轮证据", body: "预览、read_file、search 路径均有上限。" },
      ],
      group: "搜索 2 · 读取 3",
      tools: [
        { name: "搜索", preview: "ListFiles · DefaultMaxFileBytes", state: "done" },
        { name: "读取", preview: "coding/workspace.go:407", state: "done" },
        { name: "读取", preview: "glob.go:22", state: "done" },
      ],
    },
    answer: {
      lead: "三轮结论一致：50MB / 100MB 不会卡死。",
      sections: [
        {
          h: "路径对照",
          html: `<ul>
<li><strong>GUI 预览</strong>：2 MiB 前缀</li>
<li><strong>read_file</strong>：1–16 MiB，超限拒绝</li>
<li><strong>search</strong>：自动跳过过大文件</li>
</ul>`,
        },
      ],
    },
    todos: [
      { text: "核对预览上限", done: true },
      { text: "核对 Agent 读文件", done: true },
      { text: "输出综合结论", done: true },
    ],
  },

  multiturn_live: {
    id: "multiturn_live",
    label: "多轮·当前处理中",
    title: "继续追问登录超时",
    runState: "running",
    runLabel: "运行中",
    contextPct: 34,
    history: [
      {
        n: "01",
        q: "Grok 设备登录报 invalid_grant 是什么原因？",
        a: "设备码请求缺了 Grok Build 元数据。",
        process: {
          open: false,
          label: "已处理",
          time: "3m40s",
          notes: [
            { title: "对照 AUTH-001", body: "device token 创建/轮询需要 referrer 与客户端头。" },
          ],
          tools: [
            { name: "读取", preview: "internal/auth/grok/client.go", state: "done" },
            { name: "搜索", preview: "invalid_grant · device", state: "done" },
            { name: "读取", preview: "TestDiscoveryAndDevicePolling", state: "done" },
          ],
        },
        answer: {
          lead: "根因在设备码请求元数据不完整。",
          sections: [
            {
              h: "结论",
              html: `<p>创建时带 <code>referrer=grok-build</code>；创建与轮询都要带 client-version 与 <code>x-grok-client-surface=ui</code>。</p>`,
            },
          ],
        },
      },
      {
        n: "02",
        q: "那为什么 Codex 同一代理能通？",
        a: "Codex 走了系统代理；Azem 之前只读环境变量。",
        process: {
          open: false,
          label: "已处理",
          time: "2m05s",
          notes: [
            { title: "对照 NETWORK-001", body: "Finder 启动忽略 macOS SystemConfiguration 代理。" },
          ],
          tools: [
            { name: "读取", preview: "internal/netproxy", state: "done" },
            { name: "搜索", preview: "ProxyFromEnvironment", state: "done" },
          ],
        },
        answer: {
          lead: "代理解析路径不同，不是模型本身差异。",
          sections: [
            {
              h: "说明",
              html: `<p>Azem 桌面启动应挂共享代理解析器；环境变量可覆盖，但默认要读系统代理。</p>`,
            },
          ],
        },
      },
    ],
    user: "现在偶发还是超时，帮我再核对一轮：到底是代理刷新，还是设备码轮询退避？",
    process: {
      open: true,
      label: "处理中",
      time: "1m26s",
      notes: [
        { title: "区分两类超时", body: "TLS 握手失败 vs authorization_pending 轮询过密。" },
        { title: "核对代理刷新", body: "native proxy refresh 是否在长会话中失效。" },
      ],
      tools: [
        { name: "读取", preview: "internal/netproxy/resolver.go", state: "done" },
        { name: "读取", preview: "internal/auth/grok/client.go", state: "done" },
        { name: "搜索", preview: "authorization_pending · backoff", state: "running", status: "运行中" },
        { name: "命令", preview: "go test ./internal/auth/grok -run Poll", state: "queued", status: "排队" },
      ],
    },
    answer: {
      streaming: "先把超时样本按「连接层 / 授权轮询」切开；目前更像轮询在 invalid_grant 上错误重试…",
    },
    todos: [
      { text: "复现超时样本", done: true },
      { text: "核对代理刷新", done: true },
      { text: "核对设备码退避", done: false, active: true },
      { text: "补回归测试", done: false },
    ],
  },

  multiturn_tools: {
    id: "multiturn_tools",
    label: "多轮·每轮都有工具",
    title: "分步修 Todo 并发",
    runState: "completed",
    runLabel: "就绪",
    contextPct: 48,
    history: [
      {
        n: "01",
        q: "Todo 并行 mutation 为什么会丢当前项？",
        a: "多个 start 共用陈旧 revision，后写覆盖当前项。",
        process: {
          open: false,
          label: "已处理",
          time: "4m10s",
          notes: [
            { title: "复现 TODO-001", body: "并行 start 竞争同一 revision。" },
          ],
          tools: [
            { name: "搜索", preview: "TodoStart · revision", state: "done" },
            { name: "读取", preview: "todo_tool.go", state: "done" },
            { name: "读取", preview: "TestTodoStartCannotReplaceCurrentItem", state: "done" },
          ],
        },
        answer: {
          lead: "start 绝不能替换另一个 current 项。",
          sections: [{ h: "根因", html: `<p>并行 mutation 共享陈旧 revision；后到的 start 赢了写竞争。</p>` }],
        },
      },
      {
        n: "02",
        q: "那 done 呢？会不会也踩坑？",
        a: "done 是唯一正常完成并推进的转换；需要 await 快照。",
        process: {
          open: false,
          label: "已处理",
          time: "2m33s",
          notes: [
            { title: "核对状态机", body: "done 完成后才推进下一项。" },
          ],
          tools: [
            { name: "读取", preview: "todo_tool.go · done", state: "done" },
            { name: "搜索", preview: "TestTodoConcurrentMutations", state: "done" },
            { name: "命令", preview: "go test ./internal/app -run Todo", state: "done" },
          ],
        },
        answer: {
          lead: "保持全局并行，但 Todo 每次只发一个 mutation 并 await。",
          sections: [{ h: "规则", html: `<ul><li>完成一项后恰好一次 mutation</li><li>await 快照再继续</li><li>start 不替换 current</li></ul>` }],
        },
      },
      {
        n: "03",
        q: "主提示词要改吗？",
        a: "要。可执行主提示需写明 await 与 start 约束。",
        process: {
          open: false,
          label: "已处理",
          time: "1m48s",
          notes: [
            { title: "对齐可执行提示", body: "主提示与测试契约一致。" },
          ],
          tools: [
            { name: "读取", preview: "internal/app/prompts/main.md", state: "done" },
            { name: "编辑", preview: "main.md · Todo 约束", state: "done" },
            { name: "命令", preview: "go test ./internal/app -run Prompt", state: "done" },
          ],
        },
        answer: {
          lead: "提示词与实现双写，避免模型再次并发 start。",
          sections: [{ h: "改动", html: `<p>主提示明确：完成当前项后单次 mutation + await；禁止并行 start。</p>` }],
        },
        edited: {
          count: 1,
          plus: 12,
          minus: 2,
          files: [{ path: "internal/app/prompts/main.md", plus: "+12", minus: "−2" }],
        },
      },
    ],
    user: "把这三轮收成一段可进 AGENTS 的回归说明。",
    process: {
      open: false,
      label: "已处理",
      time: "58s",
      notes: [
        { title: "整理回归记录", body: "对应 TODO-001：现象、边界、测试名。" },
      ],
      tools: [
        { name: "读取", preview: "Agents.md · TODO-001", state: "done" },
        { name: "搜索", preview: "TestTodoStartCannotReplaceCurrentItem", state: "done" },
      ],
    },
    answer: {
      lead: "可写入维护记录的摘要如下。",
      sections: [
        {
          h: "TODO-001 回归要点",
          html: `<ul>
<li>现象：并行 start 覆盖 current，done 失败或延后</li>
<li>边界：全局并行保留；Todo 串行 mutation + await</li>
<li>测试：<code>TestTodoStartCannotReplaceCurrentItem</code>、<code>TestTodoConcurrentMutationsCannotSkipCurrentItem</code></li>
</ul>`,
        },
      ],
    },
    todos: [
      { text: "定位竞争", done: true },
      { text: "固化状态机", done: true },
      { text: "更新提示词", done: true },
      { text: "写回归说明", done: true },
    ],
  },

  files: {
    id: "files",
    label: "文件变更",
    title: "活动编辑到 structured diff",
    runState: "running",
    runLabel: "运行中",
    contextPct: 33,
    user: "把过程折叠从竖轨改成 note + tool row，并更新样式。",
    process: {
      open: true,
      label: "处理中",
      time: "1m12s",
      notes: [{ title: "实现过程投影", body: "去掉 process-entries 左侧竖线。" }],
      tools: [
        { name: "编辑", preview: "Timeline.tsx", state: "running", status: "约 3 处" },
        { name: "编辑", preview: "styles.css", state: "done" },
      ],
    },
    answer: {
      streaming: "过程折叠展开后改为判断短文 + 工具行，与主产品 work-entry 密度一致…",
    },
    edited: {
      count: 2,
      plus: 84,
      minus: 12,
      files: [
        { path: "frontend/src/components/Timeline.tsx", plus: "+46", minus: "−8" },
        { path: "frontend/src/styles.css", plus: "+38", minus: "−4" },
      ],
    },
    todos: [
      { text: "改过程投影", done: false, active: true },
      { text: "补回归测试", done: false },
    ],
  },

  failed: {
    id: "failed",
    label: "失败",
    title: "继续生成会话草稿",
    runState: "failed",
    runLabel: "已停止",
    contextPct: 27,
    user: "继续生成非 Timeline 会话草稿，覆盖审批与子智能体。",
    process: {
      open: true,
      label: "已处理",
      time: "1m22s",
      notes: [{ title: "读取现有草稿", body: "已打开 designs/session-without-timeline。" }],
      tools: [
        { name: "读取", preview: "app.jsx", state: "done" },
        { name: "命令", preview: "bun test", state: "failed", status: "失败" },
        { name: "编辑", preview: "prototype.css", state: "failed", status: "已取消" },
      ],
    },
    error: {
      title: "运行已停止",
      body: "取消请求已异步送达。部分工具未完成；失败原因保留在过程列表。",
    },
    answer: {
      lead: "已形成的方向仍保留，可重试继续。",
      sections: [
        {
          h: "已形成的方向",
          html: `<p>主栏保持任务与答案；过程用可折叠工作行；子智能体保留协作卡片。</p>`,
        },
      ],
    },
    todos: [
      { text: "读取草稿", done: true },
      { text: "生成下一版", done: false, active: true },
    ],
  },

  empty: {
    id: "empty",
    label: "空会话",
    title: "新对话",
    runState: "idle",
    runLabel: "",
    empty: true,
    contextPct: 0,
    user: "",
    todos: [],
  },
};

const scenarioOrder = [
  "complete", "running", "plan", "approval", "subagents",
  "multiturn", "multiturn_live", "multiturn_tools",
  "files", "failed", "empty",
];

Object.assign(window, { scenarios, scenarioOrder, agents });
