const { useMemo, useState } = React;

/**
 * Process evidence drafts that deliberately avoid the vertical timeline rail
 * shown in the current "已处理" expansion (dots + spine + sequential steps).
 * Mock content mirrors the MCP -32005 debugging session in the screenshot.
 */

const variants = {
  board: {
    label: "证据板",
    title: "按证据类型组织",
    blurb: "推荐：统计 + 分类卡片 + 一条结论，没有竖线和顺序节点。",
    tag: "推荐",
  },
  ledger: {
    label: "工作账本",
    title: "表格化工具台账",
    blurb: "适合审计：类型 / 动作 / 目标 / 耗时。可读，但不是故事线。",
    tag: "审计",
  },
  phases: {
    label: "阶段卡",
    title: "语义阶段而非逐步事件",
    blurb: "把 20+ 节点压成 4–6 个阶段。阶段有结论，工具只作标签。",
    tag: "长任务",
  },
  summary: {
    label: "仅摘要",
    title: "默认只给结论",
    blurb: "折叠态就够用；需要时再展开原始过程（仍可保留旧时间线作调试出口）。",
    tag: "极简",
  },
};

const stats = [
  { value: "3", label: "搜索" },
  { value: "2", label: "读取" },
  { value: "1", label: "命令" },
  { value: "1", label: "子智能体" },
];

const ledgerRows = [
  { kind: "judge", kindLabel: "判断", action: "定位传输拒绝根因", target: "JSON-RPC / MCP 调用链", time: "1s" },
  { kind: "agent", kindLabel: "子智能体", action: "建立可复现路径", target: "错误 -32005 传输层路径", time: "—" },
  { kind: "search", kindLabel: "搜索", action: "搜索代码", target: "3 次匹配聚合", time: "—" },
  { kind: "read", kindLabel: "读取", action: "读取文件", target: "2 个源文件", time: "—" },
  { kind: "shell", kindLabel: "命令", action: "运行命令", target: "1 条回归相关命令", time: "—" },
  { kind: "judge", kindLabel: "判断", action: "收窄 RPC 边界", target: "依赖错误文案 vs 仓库字面量", time: "5s" },
  { kind: "judge", kindLabel: "判断", action: "识别错误所属层", target: "MCP 客户端包装器", time: "18s" },
  { kind: "judge", kindLabel: "判断", action: "根因已确认", target: "manager.go:684–688", time: "21s" },
  { kind: "shell", kindLabel: "命令", action: "红灯 / 绿灯验证", target: "回归测试与端到端", time: "—" },
];

const phases = [
  {
    num: "01",
    title: "复现与定界",
    body: "确认分支与现有改动，追踪 -32005 从传输层到运行失败提示的完整路径。",
    time: "约 2m",
    tools: ["搜索 ×3", "读取 ×2", "命令 ×1"],
  },
  {
    num: "02",
    title: "分层归因",
    body: "排除 Grok 模型传输；确认错误格式来自 MCP 客户端包装器，并验证关闭竞态假设。",
    time: "约 6m",
    tools: ["读取", "判断"],
  },
  {
    num: "03",
    title: "根因锁定",
    body: "manager.go:684–688 把瞬时传输拒绝升格为执行级 Go 错误，代理引擎因此终止整轮运行。",
    time: "约 4m",
    tools: ["代码定位"],
  },
  {
    num: "04",
    title: "修复边界",
    body: "仅降级 RPCError.Code == -32005 为可恢复传输拒绝；保持连接可复用，不吞掉真实执行异常。",
    time: "约 5m",
    tools: ["编辑", "单测"],
  },
  {
    num: "05",
    title: "验证闭环",
    body: "红灯复现成立后补端到端：工具错误进入下一轮模型调用，run_finished，连接保持 Ready。",
    time: "约 7m",
    tools: ["e2e", "夹具重构"],
  },
];

const rawDump = `process trail (debug export)
- locate root cause · 1s
- subagent: establish repro path · -32005
- tool group: search×3 · read×2 · shell×1 · other×1
- narrow RPC boundary · 5s
- inspect Grok request transport · 15s
- identify error layer · MCP client wrapper · 18s
- validate close-race hypothesis · 19s
- confirm failure propagation · 18s
- root cause confirmed · manager.go:684-688 · 21s
- author regression guard · 55s
- red light verification · 5s
- red light reproduced · 41s
- refine recoverable boundary · 24s
- strengthen full-run guarantee · 12s
- cover real runtime events · 37s
- verify end-to-end guard · 14s
- keep existing success coverage · 29s
- fix test fixture structure · 45s
total · 24m14s`;

function ProcessChrome({ children, openDefault = true }) {
  return (
    <details className="process-shell" open={openDefault}>
      <summary>
        <span>已处理</span>
        <span className="meta">24m14s · 非时间线投影</span>
      </summary>
      <div className="process-body">{children}</div>
    </details>
  );
}

function EvidenceBoard() {
  return (
    <ProcessChrome>
      <div className="evidence-stats">
        {stats.map((item) => (
          <div key={item.label} className="stat-chip">
            <strong>{item.value}</strong>
            <span>{item.label}</span>
          </div>
        ))}
      </div>
      <div className="evidence-grid">
        <section className="evidence-card">
          <header><strong>关键判断</strong><span>4</span></header>
          <ul>
            <li><i className="dot amber"></i><span>错误属于 MCP 客户端包装层，不是 Grok 模型传输</span></li>
            <li><i className="dot amber"></i><span>关闭竞态可解释部分 -32005，但不是唯一根因</span></li>
            <li><i className="dot green"></i><span>瞬时传输拒绝被升格为整轮运行失败</span></li>
            <li><i className="dot green"></i><span>修复边界应只降级 Code == -32005</span></li>
          </ul>
        </section>
        <section className="evidence-card">
          <header><strong>工具台账</strong><span>7 项聚合</span></header>
          <ul>
            <li><i className="dot blue"></i><span>搜索 3 次 · 读取 2 文件 · 命令 1 条</span></li>
            <li><i className="dot blue"></i><span>子智能体：建立可复现路径（-32005）</span></li>
            <li><i className="dot"></i><span>不在此列出每一步叙事；需要明细用账本视图</span></li>
          </ul>
        </section>
        <section className="evidence-card">
          <header><strong>改动与验证</strong><span>闭环</span></header>
          <ul>
            <li><i className="dot green"></i><span>红灯：未修复时仍因 -32005 整轮失败</span></li>
            <li><i className="dot green"></i><span>绿灯：可恢复拒绝后继续下一轮模型调用</span></li>
            <li><i className="dot green"></i><span>e2e：run_finished，连接保持 Ready</span></li>
          </ul>
        </section>
        <section className="finding">
          <strong>本轮结论（可进正文）</strong>
          <p>
            根因在 <span className="code-inline">internal/mcp/manager.go:684–688</span>：
            把 MCP 瞬时传输拒绝当作执行级错误返回，代理运行器因此终止整轮。
            修复只降级 <span className="code-inline">-32005</span>，不吞掉真实执行异常。
          </p>
        </section>
      </div>
    </ProcessChrome>
  );
}

function WorkLedger() {
  return (
    <ProcessChrome>
      <table className="ledger-table">
        <thead>
          <tr>
            <th>类型</th>
            <th>动作</th>
            <th>目标 / 结果</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          {ledgerRows.map((row) => (
            <tr key={`${row.kind}-${row.action}`}>
              <td><span className={`kind-pill ${row.kind}`}>{row.kindLabel}</span></td>
              <td>{row.action}</td>
              <td>{row.target}</td>
              <td>{row.time}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </ProcessChrome>
  );
}

function PhaseCards() {
  return (
    <ProcessChrome>
      <div className="phase-list">
        {phases.map((phase) => (
          <article key={phase.num} className="phase-card">
            <span className="phase-num">{phase.num}</span>
            <div>
              <strong>{phase.title}</strong>
              <p>{phase.body}</p>
              <div className="phase-tools">
                {phase.tools.map((tool) => <span key={tool} className="mini-chip">{tool}</span>)}
              </div>
            </div>
            <time>{phase.time}</time>
          </article>
        ))}
      </div>
    </ProcessChrome>
  );
}

function SummaryOnly() {
  const [showRaw, setShowRaw] = useState(false);
  return (
    <ProcessChrome>
      <div className="summary-only">
        <div className="summary-hero">
          <div>
            <strong>已完成 5 个阶段 · 7 类工具活动</strong>
            <p>根因已确认并完成红灯/绿灯与端到端守卫。默认不展开逐步叙事。</p>
          </div>
          <div className="summary-time">24m</div>
        </div>
        <div className="summary-actions">
          <button type="button" className="ghost-btn" aria-pressed={!showRaw} onClick={() => setShowRaw(false)}>阶段摘要</button>
          <button type="button" className="ghost-btn" aria-pressed={showRaw} onClick={() => setShowRaw(true)}>原始过程（调试）</button>
        </div>
        {showRaw ? (
          <pre className="raw-dump">{rawDump}</pre>
        ) : (
          <div className="phase-list">
            {phases.slice(2, 5).map((phase) => (
              <article key={phase.num} className="phase-card">
                <span className="phase-num">{phase.num}</span>
                <div>
                  <strong>{phase.title}</strong>
                  <p>{phase.body}</p>
                </div>
                <time>{phase.time}</time>
              </article>
            ))}
          </div>
        )}
      </div>
    </ProcessChrome>
  );
}

function App() {
  const [variant, setVariant] = useState("board");
  const meta = variants[variant];
  const processView = useMemo(() => {
    if (variant === "ledger") return <WorkLedger />;
    if (variant === "phases") return <PhaseCards />;
    if (variant === "summary") return <SummaryOnly />;
    return <EvidenceBoard />;
  }, [variant]);

  return (
    <div className="shell">
      <header className="topbar">
        <div className="topbar-title">
          <strong>过程证据 · 非时间线草稿</strong>
          <small>针对截图中「已处理」展开后的竖轨节点</small>
        </div>
        <nav className="variant-switch" aria-label="过程展示方向">
          {Object.entries(variants).map(([key, item]) => (
            <button
              key={key}
              type="button"
              aria-pressed={variant === key}
              onClick={() => setVariant(key)}
            >{item.label}</button>
          ))}
        </nav>
        <p className="top-note">{meta.blurb}</p>
      </header>

      <div className="problem-banner">
        <span className="problem-tag">问题</span>
        <div>
          <strong>上一版只把主栏改成了文档，过程区仍是 Timeline</strong>
          <p>
            竖线、圆点、逐步耗时、模型进度步——这是事件流的视觉语法。
            本草稿只改「已处理 / 处理中」的展开形态：按证据与阶段组织，而不是按发生顺序讲故事。
          </p>
        </div>
      </div>

      <div className="stage">
        <main className="reading">
          <div className="reading-inner">
            <div className="task-label">当前任务</div>
            <h1>定位 MCP jsonrpc error -32005 导致整轮运行失败的根因并修复</h1>
            <p className="lede">
              截图场景复刻：长过程折叠在答案旁。下面四种展开方式都没有左侧时间轴。
            </p>

            <article className="answer-card">
              <h2>最终回答（正文保持稳定）</h2>
              <p>
                根因在 <span className="code-inline">internal/mcp/manager.go:684–688</span>：
                MCP 瞬时传输拒绝被当作执行级 Go 错误返回，代理运行器把执行级错误视为整轮失败。
              </p>
              <p>
                修复仅降级 <span className="code-inline">RPCError.Code == -32005</span> 为可恢复传输拒绝，
                保持连接可复用；普通 MCP 故障仍按原行为中止，避免掩盖真实执行异常。
              </p>
            </article>

            {processView}
          </div>
        </main>

        <aside className="side">
          <h2>{meta.title}</h2>
          <p className="blurb">{meta.blurb}</p>
          <div className="compare">
            {Object.entries(variants).map(([key, item]) => (
              <button
                key={key}
                type="button"
                className="compare-item"
                data-active={String(variant === key)}
                onClick={() => setVariant(key)}
              >
                <strong>{item.label} · {item.tag}</strong>
                <span>{item.blurb}</span>
              </button>
            ))}
          </div>
          <div className="anti">
            <strong>明确不做</strong>
            <p>
              不再使用 process-entries 左侧竖轨、逐步圆点、把 commentary 串成“第 N 步”的叙事轴。
              若调试需要原始顺序，放到「原始过程」出口，而不是默认阅读形态。
            </p>
          </div>
        </aside>
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
