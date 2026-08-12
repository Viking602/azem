const { useEffect, useMemo, useState } = React;

/**
 * Subagent collaboration design:
 * 1) Main thread keeps the product SubagentRunCard (refined)
 * 2) Center page (SubagentsPage drawer)
 * 3) Detail page (AgentSideChat drawer) — same transcript language as main
 */

const agents = [
  {
    id: "a-ux",
    face: "UX",
    faceClass: "ux",
    name: "信息架构审查",
    type: "review",
    state: "completed",
    stateLabel: "已完成",
    preview: "3 条建议：主栏任务 / 过程折叠 / 中断卡边界",
    elapsed: "2m18s",
    prompt: "请审查非 Timeline 会话的信息架构：哪些块必须留在主栏，哪些应进过程区。",
    process: [
      { name: "读取", preview: "Timeline.tsx · sessionDocument.ts" },
      { name: "搜索", preview: "BlockKind · interrupt" },
    ],
    processOpen: false,
    processLabel: "已处理",
    processTime: "1m42s",
    answer: [
      "主栏只保留任务 Brief、答案正文，以及 approval / question / plan / error 中断。",
      "thinking、commentary、tool 进入过程折叠；文件变更与执行计划继续放在 Inspector。",
      "子智能体协作保留专用卡片，不要退化成普通工具行。",
    ],
  },
  {
    id: "a-ui",
    face: "UI",
    faceClass: "ui",
    name: "视觉一致性检查",
    type: "review",
    state: "running",
    stateLabel: "运行中",
    preview: "正在核对卡片密度与侧栏层级…",
    elapsed: "1m04s",
    prompt: "检查协作卡片与侧栏详情页是否匹配 Azem 暖白工作台：间距、状态色、进度条。",
    process: [
      { name: "读取", preview: "prototype.css · subagent-run-card" },
      { name: "读取", preview: "AgentSideChat.tsx" },
      { name: "搜索", preview: "status colors · progress", state: "running" },
    ],
    processOpen: true,
    processLabel: "处理中",
    processTime: "1m04s",
    answer: [
      "卡片应保持现有四向边框与浅渐变，运行态仅用蓝色描边提示，不要换成磁贴网格。",
      "详情页正文与主会话同构：用户气泡 + 过程折叠 + 最终回答。",
    ],
  },
  {
    id: "a-qa",
    face: "QA",
    faceClass: "qa",
    name: "场景覆盖检查",
    type: "review",
    state: "queued",
    stateLabel: "排队中",
    preview: "等待并发槽位…",
    elapsed: "—",
    prompt: "覆盖完成 / 运行中 / 失败 / 空 transcript 四类子智能体状态。",
    process: [],
    processOpen: false,
    processLabel: "排队",
    processTime: "",
    answer: [],
  },
  {
    id: "a-sec",
    face: "SEC",
    faceClass: "sec",
    name: "安全边界审查",
    type: "review",
    state: "completed",
    stateLabel: "已完成",
    preview: "无阻塞；审批中断不得折叠进过程",
    elapsed: "3m11s",
    prompt: "确认审批与失败态在非 Timeline 投影中仍然可见、可操作。",
    process: [
      { name: "读取", preview: "ApprovalBlock · resolve_approval" },
      { name: "搜索", preview: "awaiting_approval · error block" },
    ],
    processOpen: false,
    processLabel: "已处理",
    processTime: "2m40s",
    answer: [
      "approval / error 必须作为主栏中断卡，不能进入「已处理」折叠。",
      "子智能体失败要在协作卡片与中心页同时可见，避免只出现在 Inspector。",
    ],
  },
];

function statusOfAgents(list) {
  const running = list.filter((a) => a.state === "running" || a.state === "started").length;
  const queued = list.filter((a) => a.state === "queued").length;
  const failed = list.filter((a) => a.state === "failed").length;
  const completed = list.filter((a) => a.state === "completed").length;
  if (running) return { state: "running", label: "运行中", detail: `${running} 个运行中${queued ? ` · ${queued} 个排队` : ""}` };
  if (queued) return { state: "queued", label: "排队中", detail: `${queued} 个排队中` };
  if (failed) return { state: "failed", label: "有失败", detail: `${failed} 个失败 · ${completed} 个已结束` };
  return { state: "completed", label: "已完成", detail: `${completed} 个已结束` };
}

function SubagentRunCard({ list, expanded, onToggle, onOpenAgent, onOpenCenter }) {
  const meta = statusOfAgents(list);
  const terminal = list.filter((a) => a.state === "completed" || a.state === "failed").length;
  const progress = Math.round((terminal / list.length) * 100);
  const indeterminate = meta.state === "running" && progress === 0;

  return (
    <section className="subagent-run-card" data-state={meta.state} aria-label={`${list.length} 个子智能体协作`}>
      <button
        type="button"
        className="subagent-run-card-summary"
        aria-expanded={expanded}
        aria-controls="subagent-run-list"
        onClick={onToggle}
      >
        <span className="subagent-run-mark" aria-hidden="true">
          ⌘
          <i>{list.length}</i>
        </span>
        <span className="subagent-run-copy">
          <span>子智能体中心</span>
          <strong>{list.length} 个子智能体协作</strong>
          <small>{meta.detail}</small>
        </span>
        <span className="subagent-run-status" data-state={meta.state}>
          <strong>{meta.label}</strong>
          <small>{terminal} / {list.length} 已结束</small>
          <span className="subagent-run-progress" aria-hidden="true">
            <i style={{ width: `${progress}%` }} data-indeterminate={indeterminate || undefined} />
          </span>
        </span>
        <span className="subagent-run-chevron" aria-hidden="true">›</span>
      </button>

      <div className="subagent-run-list" id="subagent-run-list" hidden={!expanded}>
        {list.map((agent) => (
          <button
            key={agent.id}
            type="button"
            className="subagent-run-row"
            data-state={agent.state}
            onClick={(event) => {
              event.stopPropagation();
              onOpenAgent(agent.id);
            }}
          >
            <span className={`glyph ${agent.faceClass}`}>{agent.face}</span>
            <span>
              <strong>{agent.name}</strong>
              <small>{agent.preview}</small>
            </span>
            <em>{agent.stateLabel}</em>
            <span className="go" aria-hidden="true">›</span>
          </button>
        ))}
      </div>

      <div className="card-footer">
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            onOpenCenter();
          }}
        >打开子智能体中心</button>
        <span>点行进入详情页 · 与主会话同构</span>
      </div>
    </section>
  );
}

function SubagentsCenter({ list, onClose, onOpenAgent }) {
  const groups = [
    {
      key: "active",
      label: "进行中",
      agents: list.filter((a) => a.state === "running" || a.state === "started" || a.state === "initializing"),
    },
    {
      key: "queued",
      label: "排队",
      agents: list.filter((a) => a.state === "queued"),
    },
    {
      key: "finished",
      label: "已结束",
      agents: list.filter((a) => a.state === "completed" || a.state === "failed"),
    },
  ].filter((g) => g.agents.length);

  const counts = {
    active: groups.find((g) => g.key === "active")?.agents.length || 0,
    queued: groups.find((g) => g.key === "queued")?.agents.length || 0,
    finished: groups.find((g) => g.key === "finished")?.agents.length || 0,
  };

  return (
    <div className="drawer-layer" onClick={onClose}>
      <section
        className="subagents-page"
        role="dialog"
        aria-modal="true"
        aria-label="子智能体中心"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="subagents-page-bar">
          <div className="subagents-page-heading">
            <span>Subagent center</span>
            <h1>子智能体</h1>
            <p>{statusOfAgents(list).detail}</p>
          </div>
          <button type="button" className="subagents-close" onClick={onClose} aria-label="关闭">×</button>
        </header>
        <div className="subagents-page-scroll">
          <div className="subagents-page-content">
            <div className="subagents-overview" aria-label="概览">
              <div data-state="active"><strong>{counts.active}</strong><span>进行中</span></div>
              <div data-state="queued"><strong>{counts.queued}</strong><span>排队</span></div>
              <div data-state="finished"><strong>{counts.finished}</strong><span>已结束</span></div>
            </div>
            <div className="subagent-groups">
              {groups.map((group) => (
                <section className="subagent-group" data-group={group.key} key={group.key}>
                  <header>
                    <h2>{group.label}</h2>
                    <span>{group.agents.length}</span>
                  </header>
                  <ul className="subagents-list">
                    {group.agents.map((agent) => (
                      <li className="subagent-row" key={agent.id}>
                        <button type="button" onClick={() => onOpenAgent(agent.id)}>
                          <span className={`glyph ${agent.faceClass}`} style={{ width: 34, height: 34, borderRadius: 10 }}>{agent.face}</span>
                          <span className="subagent-row-copy">
                            <strong>{agent.name}</strong>
                            <span>{agent.preview}</span>
                          </span>
                          <span className="subagent-row-meta">
                            <em data-state={agent.state}>{agent.stateLabel}</em>
                            <time>{agent.elapsed}</time>
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

function AgentDetail({ agentId, list, onClose, onSelect }) {
  const agent = list.find((a) => a.id === agentId) || list[0];
  if (!agent) return null;
  const running = agent.state === "running" || agent.state === "started";

  return (
    <div className="drawer-layer detail" onClick={onClose}>
      <aside
        className="agent-side-chat"
        role="dialog"
        aria-modal="true"
        aria-label={agent.name}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="agent-side-chat-header">
          <div className="agent-side-chat-heading">
            <span className={`glyph ${agent.faceClass}`} style={{ width: 34, height: 34, borderRadius: 10 }}>{agent.face}</span>
            <div className="agent-side-chat-title">
              <h2>{agent.name}</h2>
              <small>
                <em data-state={agent.state}>{agent.stateLabel}</em>
                {agent.elapsed !== "—" ? <time>{agent.elapsed}</time> : null}
              </small>
            </div>
          </div>
          <div className="agent-side-chat-actions">
            {running ? (
              <button type="button" className="icon-btn danger" aria-label="停止" title="停止">■</button>
            ) : null}
            <button type="button" className="icon-btn" aria-label="关闭" onClick={onClose}>×</button>
          </div>
        </header>

        <div className="agent-side-chat-switcher">
          <span>协作组</span>
          <div className="agent-side-chat-tabs" role="tablist" aria-label="子智能体">
            {list.map((item) => (
              <button
                key={item.id}
                type="button"
                role="tab"
                aria-selected={item.id === agent.id}
                className={item.id === agent.id ? "active" : ""}
                title={item.name}
                onClick={() => onSelect(item.id)}
              >
                <span className={`glyph ${item.faceClass}`} style={{ width: 24, height: 24, borderRadius: 8, fontSize: 9 }}>{item.face}</span>
              </button>
            ))}
          </div>
          <em>{list.length}</em>
        </div>

        <div className="agent-side-chat-scroll">
          <div className="agent-side-chat-transcript">
            <div className="detail-user">{agent.prompt}</div>

            {agent.process?.length ? (
              <details className="process" data-state={running ? "running" : "completed"} open={agent.processOpen}>
                <summary>
                  <span className="chev" aria-hidden="true">›</span>
                  <span>{agent.processLabel}</span>
                  {agent.processTime ? <time>{agent.processTime}</time> : null}
                </summary>
                <div className="process-body">
                  {agent.process.map((tool, index) => (
                    <div className="tool-row" key={`${tool.name}-${index}`}>
                      <span className="m">{tool.state === "running" ? "●" : "✓"}</span>
                      <span>
                        <strong style={{ fontWeight: 500 }}>{tool.name}</strong>
                        <span className="p">{tool.preview}</span>
                      </span>
                    </div>
                  ))}
                </div>
              </details>
            ) : (
              <div className="note" style={{ marginBottom: 16 }}>
                <strong>等待启动</strong>
                <p>该子智能体仍在排队，尚未产生 transcript。</p>
              </div>
            )}

            {agent.answer?.length ? (
              <div className="detail-answer">
                {agent.answer.map((p) => <p key={p}>{p}</p>)}
              </div>
            ) : running ? (
              <div className="detail-answer">
                <p>正在形成最终回答…</p>
              </div>
            ) : null}
          </div>
        </div>
      </aside>
    </div>
  );
}

function App() {
  const [expanded, setExpanded] = useState(true);
  const [centerOpen, setCenterOpen] = useState(false);
  const [detailId, setDetailId] = useState("");
  const [hint, setHint] = useState(true);

  useEffect(() => {
    const t = window.setTimeout(() => setHint(false), 5000);
    return () => window.clearTimeout(t);
  }, []);

  const openAgent = (id) => {
    setDetailId(id);
    setCenterOpen(false);
  };

  return (
    <div className="desktop">
      <header className="topbar">
        <div>
          <span className="traffic"><i></i><i></i><i></i></span>
          <span className="proj"><b>azem</b> / main</span>
        </div>
        <div className="search"><span>搜索、跳转或执行命令</span><kbd>⌘K</kbd></div>
        <div className="top-actions">
          <button type="button" aria-pressed={centerOpen} onClick={() => setCenterOpen(true)}>子智能体中心</button>
          <button
            type="button"
            aria-pressed={Boolean(detailId)}
            onClick={() => openAgent(agents.find((a) => a.state === "running")?.id || agents[0].id)}
          >打开详情页</button>
        </div>
      </header>

      <div className="workspace">
        <aside className="sidebar">
          <div className="tabs">
            <button type="button" className="active">会话</button>
            <button type="button">工作区</button>
          </div>
          <button type="button" className="nav">＋ 新对话</button>
          <button type="button" className="nav">⌕ 搜索</button>
          <div className="sec">项目</div>
          <div className="project">
            <span className="A">A</span>
            <div>
              <strong>azem</strong>
              <small>main · 并行审查</small>
            </div>
          </div>
          <div className="threads">
            <button type="button" className="thread active">
              <span className="dot"></span>
              并行审查非 Timeline 草稿
            </button>
            <button type="button" className="thread">
              <span className="dot"></span>
              定位 MCP -32005
            </button>
          </div>
          <div className="foot">
            <button type="button" className="nav">⚙ 设置</button>
          </div>
        </aside>

        <section className="main">
          <header className="head">
            <span className="k">TASK</span>
            <h1>并行审查非 Timeline 草稿</h1>
            <span className="pill" data-state="running"><i></i>运行中</span>
          </header>

          <div className="scroll">
            <div className="transcript">
              <div className="user">
                并行审查信息架构、视觉一致性和场景覆盖。主会话只保留汇总结论；子智能体协作保留卡片，并能进入各自详情页。
              </div>

              <div className="note">
                <strong>分派专项审查</strong>
                <p>覆盖架构、视觉、场景与安全边界。完成后汇总到主会话正文。</p>
              </div>

              <SubagentRunCard
                list={agents}
                expanded={expanded}
                onToggle={() => setExpanded((v) => !v)}
                onOpenAgent={openAgent}
                onOpenCenter={() => setCenterOpen(true)}
              />

              <div className="sep">最终回答</div>
              <article className="answer">
                <p>
                  主会话保持任务与答案；过程用安静折叠。
                  <strong>子智能体协作继续用专用卡片</strong>
                  ，展开后是成员列表，点击进入详情页（与主会话同构的 transcript）。
                </p>
                <h3>交互约定</h3>
                <ul>
                  <li>卡片摘要：数量 · 运行状态 · 进度条</li>
                  <li>展开列表：名称 · 预览 · 状态；点击打开详情</li>
                  <li>「打开子智能体中心」：按进行中 / 排队 / 已结束分组</li>
                  <li>详情页：顶部切换协作组，正文 = 用户提示 + 过程折叠 + 最终回答</li>
                </ul>
              </article>
            </div>
          </div>

          <div className="composer-dock">
            <div className="composer">
              <textarea placeholder="继续补充审查要求…" aria-label="消息输入"></textarea>
              <div className="composer-foot">
                <span>＋ · 自动审查 · 计划</span>
                <span>GPT-5.6 · 高  ↑</span>
              </div>
            </div>
          </div>

          {centerOpen ? (
            <SubagentsCenter
              list={agents}
              onClose={() => setCenterOpen(false)}
              onOpenAgent={openAgent}
            />
          ) : null}

          {detailId ? (
            <AgentDetail
              agentId={detailId}
              list={agents}
              onClose={() => setDetailId("")}
              onSelect={setDetailId}
            />
          ) : null}
        </section>
      </div>

      {hint ? (
        <div className="hint" role="status">
          点协作卡片展开成员 → 点某一行打开详情页；或用右上角「子智能体中心 / 打开详情页」。
        </div>
      ) : null}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
