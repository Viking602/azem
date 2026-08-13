/** Real Azem conversation fixtures for design review. */

const scenarios = {
  complete: {
    id: "complete",
    label: "完成",
    title: "定位 MCP -32005 运行失败",
    runState: "completed",
    runLabel: "就绪",
    duration: "24m14s",
    processOpen: false,
    contextPct: 42,
    user: "运行时偶发 jsonrpc error -32005，整轮直接失败。帮我定位根因并修掉，保留正常 MCP 失败语义。",
    notes: [
      {
        title: "定位传输拒绝根因",
        body: "核对未提交修复、JSON-RPC 调用链与错误触发条件，补可复现测试。",
      },
      {
        title: "识别错误所属层",
        body: "jsonrpc error 格式来自 MCP 客户端包装器，不是 Grok 模型传输。",
      },
      {
        title: "根因已确认",
        body: "manager.go:684–688 把 MCP 瞬时传输拒绝升格为执行级错误，代理运行器因此终止整轮。",
      },
    ],
    tools: [
      { name: "搜索", preview: "jsonrpc · -32005 · manager", state: "done" },
      { name: "读取", preview: "internal/mcp/manager.go", state: "done" },
      { name: "读取", preview: "internal/provider/…", state: "done" },
      { name: "命令", preview: "go test ./internal/mcp/…", state: "done" },
      { name: "编辑", preview: "manager.go · 降级 -32005", state: "done" },
      { name: "命令", preview: "go test ./… -count=1", state: "done" },
    ],
    group: "搜索 3 · 读取 2 · 命令 2 · 编辑 1",
    answer: {
      lead: "根因与修复已落地，并通过红灯 / 绿灯与端到端守卫。",
      sections: [
        {
          h: "结论",
          html: `<p>根因在 <code>internal/mcp/manager.go:684–688</code>：MCP 瞬时传输拒绝被当作执行级 Go 错误返回，代理运行器把执行级错误视为整轮失败。</p>
<p>修复仅降级 <code>RPCError.Code == -32005</code> 为可恢复传输拒绝，保持连接可复用；其它 MCP 故障仍按原行为中止。</p>`,
        },
        {
          h: "验证",
          html: `<ul>
<li>未修复时红灯复现：仍因 -32005 整轮失败。</li>
<li>修复后：工具错误进入下一轮模型调用，最终 <code>run_finished</code>，连接 Ready。</li>
<li>既有成功路径覆盖保留。</li>
</ul>`,
        },
      ],
    },
    files: [
      { path: "internal/mcp/manager.go", plus: "+18", minus: "−6" },
      { path: "internal/mcp/manager_test.go", plus: "+94", minus: "−2" },
    ],
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
    duration: "2m14s",
    processOpen: true,
    contextPct: 38,
    user: "优化 App / Sidebar / Timeline / Inspector 的切换与流式动效，保留暖白工作台气质。",
    notes: [
      {
        title: "提取视觉与动效基线",
        body: "暖白纸面、8 个流式尾部节点、reduced motion。",
      },
      {
        title: "构建高保真交互原型",
        body: "页面、工具与文本共享一套节奏；工具状态用轨迹推进。",
      },
    ],
    tools: [
      { name: "读取", preview: "frontend/src/styles.css", state: "done" },
      { name: "读取", preview: "Timeline.tsx", state: "done" },
      { name: "搜索", preview: "motion · transition", state: "done" },
      { name: "编辑", preview: "azem-ui-motion-concept/…", state: "running", status: "运行中" },
    ],
    group: null,
    answer: {
      lead: null,
      streaming: "建议把 Azem 的视觉方向定义为“静谧机械感”。保留暖白纸面和橙色品牌点，把导航、检查器和运行状态组织成连续的空间层…",
      sections: [],
    },
    files: [
      { path: "designs/azem-ui-motion-concept/…", plus: "+4 文件", minus: "" },
    ],
    todos: [
      { text: "分析当前界面与事件链", done: true },
      { text: "建立视觉与动效 token", done: true, active: true },
      { text: "制作完整交互页面", done: false },
      { text: "浏览器验证", done: false },
    ],
  },

  approval: {
    id: "approval",
    label: "审批",
    title: "写入配置前需要确认",
    runState: "running",
    runLabel: "等待确认",
    duration: "48s",
    processOpen: true,
    contextPct: 22,
    user: "把默认审批策略改成 session，并更新配置说明。",
    notes: [
      { title: "读取配置边界", body: "确认 approvalMode 写入路径与热加载行为。" },
    ],
    tools: [
      { name: "读取", preview: "internal/config/config.go", state: "done" },
      { name: "编辑", preview: "config.yaml", state: "awaiting", status: "待审批" },
    ],
    group: null,
    interrupt: {
      kind: "approval",
      title: "apply_patch · 写入工作区",
      target: "config.yaml",
      body: "此操作将修改工作区内容。未批准前不投影为已编辑文件。",
    },
    answer: { lead: null, sections: [], streaming: null },
    files: [],
    todos: [
      { text: "读取配置", done: true },
      { text: "等待写入授权", done: false, active: true },
      { text: "更新说明", done: false },
    ],
  },

  subagents: {
    id: "subagents",
    label: "子智能体",
    title: "并行审查非 Timeline 草稿",
    runState: "running",
    runLabel: "运行中",
    duration: "3m02s",
    processOpen: true,
    contextPct: 51,
    user: "并行审查信息架构、视觉一致性和场景覆盖，主会话只保留汇总结论。",
    notes: [
      { title: "分派专项审查", body: "覆盖架构、视觉与场景完整性。" },
    ],
    tools: [
      { name: "子智能体", preview: "信息架构审查", state: "done" },
      { name: "子智能体", preview: "视觉一致性检查", state: "running", status: "运行中" },
      { name: "子智能体", preview: "场景覆盖检查", state: "queued", status: "排队" },
    ],
    group: null,
    subagents: { total: 3, running: 1, queued: 1, done: 1 },
    answer: {
      lead: "主会话只汇总，不展开每个子智能体 transcript。",
      sections: [
        {
          h: "当前状态",
          html: `<ul>
<li>信息架构：已完成，3 条建议待并入正文。</li>
<li>视觉一致性：运行中。</li>
<li>场景覆盖：排队。</li>
</ul>`,
        },
      ],
    },
    files: [],
    todos: [
      { text: "启动审查子智能体", done: true },
      { text: "汇总审查结果", done: false, active: true },
      { text: "并入最终方案", done: false },
    ],
  },

  multiturn: {
    id: "multiturn",
    label: "多轮",
    title: "分析超长 JSON 加载性能",
    runState: "completed",
    runLabel: "就绪",
    duration: "4m22s",
    processOpen: false,
    contextPct: 9,
    history: [
      {
        n: "01",
        q: "工作区文件预览会卡死吗？",
        a: "有 2MiB 上限，不会整文件进内存。",
        detail: "GUI 预览路径有 maxPreviewBytes 保护。",
      },
      {
        n: "02",
        q: "Agent read_file 呢？",
        a: "默认 1MiB / 最大 16MiB，超限直接拒绝。",
        detail: "venat coding/workspace 边界。",
      },
    ],
    user: "分析一下当前项目如果加载超长 json（比如 50m / 100m）会不会出现卡顿或加载缓慢？",
    notes: [],
    tools: [
      { name: "搜索", preview: "maxPreviewBytes · MaxReadBytes", state: "done" },
      { name: "读取", preview: "workspace_files.go", state: "done" },
      { name: "读取", preview: "WorkspaceFilesPage.tsx", state: "done" },
    ],
    group: "搜索 2 · 读取 4",
    answer: {
      lead: "分析完成。以下是基于代码证据的结论。",
      sections: [
        {
          h: "结论",
          html: `<p>当前项目所有“加载大 JSON”的入口都有硬性大小上限。50MB / 100MB 文件不会导致卡死或持续卡顿。各路径要么直接拒绝，要么只读取/传输截断后的有限前缀。</p>`,
        },
        {
          h: "各加载路径分析",
          html: `<ul>
<li><strong>GUI 工作区文件预览</strong> — <code>maxPreviewBytes = 2 MiB</code>，大文件只从前缀读取。</li>
<li><strong>Agent coding.read_file</strong> — 默认 1 MiB，最大 16 MiB，超限拒绝。</li>
<li><strong>coding.search</strong> — 自动跳过过大文件。</li>
</ul>`,
        },
      ],
    },
    files: [],
    todos: [
      { text: "核对预览上限", done: true },
      { text: "核对 Agent 读文件", done: true },
      { text: "输出结论", done: true },
    ],
  },

  failed: {
    id: "failed",
    label: "失败",
    title: "继续生成会话草稿",
    runState: "failed",
    runLabel: "已停止",
    duration: "1m22s",
    processOpen: true,
    contextPct: 27,
    user: "继续生成非 Timeline 会话草稿，覆盖审批与子智能体场景。",
    notes: [
      { title: "读取现有草稿", body: "已打开 designs/session-without-timeline。" },
    ],
    tools: [
      { name: "读取", preview: "app.jsx", state: "done" },
      { name: "命令", preview: "bun test", state: "failed", status: "失败" },
      { name: "编辑", preview: "prototype.css", state: "failed", status: "已取消" },
    ],
    group: null,
    interrupt: {
      kind: "error",
      title: "运行已停止",
      body: "取消请求已异步送达。部分工具未完成；失败原因保留在过程列表，不伪装成成功正文。",
    },
    answer: {
      lead: "已完成的部分结论仍保留在下方，可重试继续。",
      sections: [
        {
          h: "已形成的方向",
          html: `<p>主栏保持任务与答案；过程用可折叠工作行，而不是竖轨时间线。</p>`,
        },
      ],
    },
    files: [],
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
    notes: [],
    tools: [],
    answer: { sections: [] },
    files: [],
    todos: [],
  },
};

Object.assign(window, { scenarios });
