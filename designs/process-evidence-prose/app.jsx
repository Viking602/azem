const { useMemo, useState } = React;

/**
 * Process evidence redesign without cards and without timeline rails.
 * Type hierarchy only: sections, definition lists, footnotes, plain log.
 */

const variants = {
  outline: {
    label: "纲要",
    note: "像文章小节：标题 + 段落 + 列表。没有框、没有磁贴。",
  },
  inventory: {
    label: "清单",
    note: "悬挂标签的定义列表。一行一类证据，可读但不叙事。",
  },
  endnotes: {
    label: "尾注",
    note: "正文里只留引用编号；过程全部沉到答案下方的注释。",
  },
  worklog: {
    label: "工作日志",
    note: "等宽连续文本。有顺序，但不是竖轨节点 UI。",
  },
};

function AnswerBody({ withRefs = false }) {
  return (
    <div className="answer">
      <h2>最终回答</h2>
      <p>
        根因在 <code>internal/mcp/manager.go:684–688</code>
        {withRefs ? <sup className="ref">1</sup> : null}
        ：MCP 瞬时传输拒绝被当作执行级 Go 错误返回，代理运行器因此终止整轮运行
        {withRefs ? <sup className="ref">2</sup> : null}
        。
      </p>
      <p>
        修复只降级 <code>RPCError.Code == -32005</code> 为可恢复传输拒绝，并保持连接可复用
        {withRefs ? <sup className="ref">3</sup> : null}
        ；普通 MCP 故障仍按原行为中止，避免掩盖真实执行异常
        {withRefs ? <sup className="ref">4</sup> : null}
        。
      </p>
    </div>
  );
}

function ProcessShell({ children, note }) {
  return (
    <details className="process" open>
      <summary>
        <span className="chev" aria-hidden="true">›</span>
        <span>已处理</span>
        <span className="dur">24m14s</span>
      </summary>
      <div className="process-inner">
        {note ? <p className="variant-note">{note}</p> : null}
        {children}
      </div>
    </details>
  );
}

function OutlineProcess({ note }) {
  return (
    <ProcessShell note={note}>
      <div className="outline">
        <section>
          <h3>做了什么</h3>
          <p className="muted">
            搜索 3 次，读取 2 个文件，运行 1 条命令，启动 1 个子智能体建立 -32005 可复现路径。
            工具明细不逐条展开；需要时可切换到「清单」或「工作日志」。
          </p>
        </section>
        <section>
          <h3>关键判断</h3>
          <ul>
            <li>错误格式来自 MCP 客户端包装层，不是 Grok 模型传输本身。</li>
            <li>关闭竞态可解释部分 -32005，但不是唯一根因。</li>
            <li>瞬时传输拒绝被升格为整轮运行失败，导致“运行失败”提示。</li>
          </ul>
        </section>
        <section>
          <h3>根因</h3>
          <p>
            <code>manager.go:684–688</code> 将 MCP 的瞬时传输拒绝作为执行级错误返回；
            代理运行器把执行级错误视为整轮失败。
          </p>
        </section>
        <section>
          <h3>验证</h3>
          <p className="muted">
            未修复时红灯复现成立；修复后可恢复拒绝进入下一轮模型调用，端到端以
            <code> run_finished </code> 结束且连接保持 Ready。既有成功路径覆盖保留。
          </p>
        </section>
      </div>
    </ProcessShell>
  );
}

function InventoryProcess({ note }) {
  const rows = [
    { label: "范围", body: "定位 jsonrpc error -32005 导致整轮失败的根因并修复", detail: "", time: "—" },
    { label: "工具", body: "搜索 ×3 · 读取 ×2 · 命令 ×1 · 其他 ×1", detail: "聚合计数，不列逐步事件", time: "—" },
    { label: "子智能体", body: "建立可复现路径", detail: "从传输层到运行失败提示", time: "—" },
    { label: "分层", body: "错误属于 MCP 客户端包装层", detail: "排除 Grok 模型传输误判", time: "18s" },
    { label: "根因", body: "manager.go:684–688 升格瞬时拒绝", detail: "执行级错误 → 整轮终止", time: "21s" },
    { label: "修复", body: "仅降级 Code == -32005", detail: "连接可复用；其他故障不吞", time: "24s" },
    { label: "验证", body: "红灯 / 绿灯 / 端到端", detail: "run_finished · 连接 Ready", time: "—" },
  ];
  return (
    <ProcessShell note={note}>
      <div className="inventory-head">
        <span>合计 <b>24m14s</b></span>
        <span>证据行 <b>7</b></span>
        <span>工具活动 <b>7</b></span>
      </div>
      <dl className="inventory">
        {rows.map((row) => (
          <div className="inventory-row" key={row.label}>
            <dt>{row.label}</dt>
            <dd>
              {row.body}
              {row.detail ? <small>{row.detail}</small> : null}
            </dd>
            <time>{row.time}</time>
          </div>
        ))}
      </dl>
    </ProcessShell>
  );
}

function EndnotesProcess({ note }) {
  return (
    <ProcessShell note={note}>
      <ol className="endnotes">
        <li>
          <span>
            <strong>代码位置。</strong>
            读取与搜索将错误处理收敛到 <code>internal/mcp/manager.go</code> 的传输拒绝分支。
          </span>
        </li>
        <li>
          <span>
            <strong>失败传播。</strong>
            任意 MCP 传输异常若以 Go error 返回，代理引擎会终止整轮运行，而不是把错误交给模型继续。
          </span>
        </li>
        <li>
          <span>
            <strong>修复边界。</strong>
            只把 <code>RPCError.Code == -32005</code> 标为可恢复；保持连接复用。
          </span>
        </li>
        <li>
          <span>
            <strong>回归。</strong>
            红灯确认未修复时仍失败；e2e 确认修复后工具错误可进入下一轮并正常 <code>run_finished</code>。
          </span>
        </li>
      </ol>
    </ProcessShell>
  );
}

function WorklogProcess({ note }) {
  const log = `\
<span class="dim"># process · 24m14s · settled</span>
<span class="hl">scope</span>     定位 -32005 整轮失败根因并修复
<span class="hl">tools</span>     search×3  read×2  shell×1  other×1
<span class="hl">agent</span>     建立可复现路径  (-32005 transport → run failure)
<span class="hl">layer</span>     MCP client wrapper, not Grok model transport
<span class="hl">cause</span>     manager.go:684-688 promotes transient reject to exec error
<span class="ok">fix</span>       downgrade only RPCError.Code == -32005; keep conn reusable
<span class="ok">verify</span>    red → green → e2e run_finished, connection Ready
<span class="dim"># no spine · no step dots · order preserved as text only</span>`;
  return (
    <ProcessShell note={note}>
      <pre className="worklog" dangerouslySetInnerHTML={{ __html: log }} />
    </ProcessShell>
  );
}

function App() {
  const [variant, setVariant] = useState("outline");
  const meta = variants[variant];

  const process = useMemo(() => {
    if (variant === "inventory") return <InventoryProcess note={meta.note} />;
    if (variant === "endnotes") return <EndnotesProcess note={meta.note} />;
    if (variant === "worklog") return <WorklogProcess note={meta.note} />;
    return <OutlineProcess note={meta.note} />;
  }, [variant, meta.note]);

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <strong>过程证据 · 无卡片</strong>
          <small>不要时间线，也不要用卡片壳</small>
        </div>
        <nav className="switch" aria-label="排版方向">
          {Object.entries(variants).map(([key, item]) => (
            <button
              key={key}
              type="button"
              aria-pressed={variant === key}
              onClick={() => setVariant(key)}
            >{item.label}</button>
          ))}
        </nav>
      </header>

      <div className="page">
        <div className="column">
          <p className="kicker">当前任务</p>
          <h1 className="page-title">
            定位 MCP jsonrpc error -32005 导致整轮运行失败的根因并修复
          </h1>
          <p className="lead">
            针对「已处理」展开区重做。去掉竖轨节点，也去掉上一稿的统计磁贴、证据卡、阶段卡。
            只保留排版层级与文字结构。
          </p>

          <AnswerBody withRefs={variant === "endnotes"} />
          {process}

          <div className="constraints">
            <h3>本版约束</h3>
            <p>不用卡片：无圆角面板、无描边容器堆叠、无 stat tile / chip grid 充当内容块。</p>
            <p>不用时间线：无左侧竖线、无逐步圆点、不把 commentary 画成第 N 步。</p>
            <p>默认阅读靠标题、段落、列表、定义行或尾注；原始顺序如需保留，用纯文本日志，而不是事件 UI。</p>
          </div>
        </div>
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
