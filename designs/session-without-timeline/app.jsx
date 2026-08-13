const { useEffect, useMemo, useState } = React;

/**
 * Azem non-timeline session draft
 * Maps every current Timeline block/state into semantic surfaces instead of
 * an event stream. Product source: Timeline.tsx, AgentSideChat, ThreadSurface.
 */

const layoutMeta = {
  document: {
    label: "工作文档",
    title: "正文稳定 · 过程旁置",
    summary: "推荐默认：主栏只读任务与答案；过程 / 变更 / 子智能体进工作现场。",
    tag: "推荐",
  },
  turns: {
    label: "回合卡片",
    title: "按问题组织 · 不按事件组织",
    summary: "每次用户输入形成一张完整回合卡；过程只作为卡片附件。",
    tag: "备选",
  },
  notebook: {
    label: "会话笔记本",
    title: "长任务可导航知识页",
    summary: "目标 / 决策 / 改动 / 验证分章，超长工程会话的阅读壳。",
    tag: "长任务",
  },
};

/** Scenario fixtures covering current Azem conversation surfaces. */
const scenarios = {
  empty: {
    label: "空会话",
    blurb: "新聊天 · 无 blocks",
    running: false,
    waiting: false,
  },
  waiting: {
    label: "等待模型",
    blurb: "ThinkingPlaceholder · 无正文",
    running: true,
    waiting: true,
  },
  streaming: {
    label: "流式成稿",
    blurb: "commentary + final_answer streaming",
    running: true,
    waiting: false,
  },
  tools: {
    label: "工具生命周期",
    blurb: "queued → approval → running → done",
    running: true,
    waiting: false,
  },
  approval: {
    label: "审批中断",
    blurb: "approval block 留在主栏",
    running: true,
    waiting: false,
  },
  plan: {
    label: "规划模式",
    blurb: "question + plan + 执行确认",
    running: false,
    waiting: false,
  },
  subagents: {
    label: "子智能体",
    blurb: "spawn 卡 + 抽屉 transcript",
    running: true,
    waiting: false,
  },
  files: {
    label: "文件改动",
    blurb: "active edit → structured diff",
    running: true,
    waiting: false,
  },
  multiturn: {
    label: "多轮会话",
    blurb: "历史回合压缩 + 当前任务",
    running: false,
    waiting: false,
  },
  error: {
    label: "失败收敛",
    blurb: "error + failed tools + stop",
    running: false,
    waiting: false,
  },
  complete: {
    label: "完成交付",
    blurb: "final_answer + edited files",
    running: false,
    waiting: false,
  },
};

/** Where each Timeline kind lands after leaving the stream. */
const projectionMap = [
  { kind: "user", surface: "任务 Brief / 回合标题", rule: "最新用户要求固定在主栏顶部；历史轮次压缩为标题条" },
  { kind: "attachments", surface: "任务 Brief 附件条", rule: "图片与文件挂在用户要求下，不单独成时间线节点" },
  { kind: "thinking", surface: "工作现场 · 推理", rule: "默认折叠；绝不伪装成 commentary 或正文" },
  { kind: "commentary", surface: "答案草稿 / 过程摘要", rule: "运行中写入「当前判断」；成熟后并入正文段落" },
  { kind: "assistant / final_answer", surface: "答案正文", rule: "唯一用户可见终端回答，出现一次，流式原位成稿" },
  { kind: "tool.queued", surface: "工作现场 · 过程", rule: "显示为排队，不可伪装成 running" },
  { kind: "tool.awaiting_approval", surface: "主栏中断卡 或 过程旁注", rule: "未授权写操作不进入文件变更投影" },
  { kind: "tool.running", surface: "工作现场 · 过程", rule: "状态图标 + 可选输出预览；不插入正文流" },
  { kind: "tool.completed/failed", surface: "工作现场 · 过程", rule: "完成后可折叠；失败保留原因" },
  { kind: "diff / file edits", surface: "工作现场 · 变更", rule: "排队写不展示；活动编辑仅在参数明确时显示计划总量" },
  { kind: "approval", surface: "主栏中断卡", rule: "需用户决策 → 留在主阅读流，不进折叠过程" },
  { kind: "question", surface: "主栏中断卡", rule: "规划选择题完整保留交互" },
  { kind: "plan", surface: "主栏中断卡", rule: "执行 / 修改 / 提问动作保留" },
  { kind: "status / answer section", surface: "答案分区标记", rule: "用轻量章节标签替代 Timeline section 节点" },
  { kind: "error", surface: "主栏告警卡", rule: "失败可见，不淹没在工具列表" },
  { kind: "agent spawn", surface: "工作现场 · 子智能体", rule: "主栏只显示摘要卡；完整 transcript 进抽屉" },
  { kind: "subagent drawer", surface: "同构阅读壳", rule: "子智能体正文与主会话同构；过程仅「处理中/已处理」折叠" },
  { kind: "edited files summary", surface: "答案尾部 或 变更页", rule: "run 结束后汇总一次，不按每个工具重复" },
  { kind: "waiting placeholder", surface: "状态条", rule: "waitingForModel 且无 active reasoning / spawn 时显示" },
  { kind: "queued prompts", surface: "Composer 上方队列", rule: "队列不属于 transcript 事件流" },
];

function GlobalSidebar({ unread }) {
  return (
    <aside className="global-sidebar" aria-label="项目和会话">
      <div className="sidebar-tabs"><button type="button">会话</button><button type="button">工作区</button></div>
      <button type="button" className="side-action"><span className="plus">＋</span><span>新对话</span></button>
      <button type="button" className="side-action"><span>⌕</span><span>搜索</span></button>
      <div className="section-label"><span>项目</span><span>＋</span></div>
      <div className="project-row">
        <span className="project-letter">A</span>
        <span className="project-copy"><strong>azem</strong><small>feat/desktop-runtime...</small></span>
        <span>···</span>
      </div>
      <div className="thread-list">
        <button type="button" className="thread-item">
          <span className="thread-dot" style={{ background: "var(--faint)" }}></span>
          <span className="thread-copy"><strong>Grok 登录问题</strong><small>昨天 · 已完成</small></span>
        </button>
        <button type="button" className="thread-item active">
          <span className="thread-dot"></span>
          <span className="thread-copy"><strong>非 Timeline 会话方案</strong><small>刚刚 · 运行中</small></span>
        </button>
        {unread ? (
          <button type="button" className="thread-item">
            <span className="thread-dot unread"></span>
            <span className="thread-copy"><strong>后台修复会话</strong><small>未读 · 已成功</small></span>
          </button>
        ) : null}
      </div>
      <div className="sidebar-spacer"></div>
      <div className="settings-row"><button type="button" className="side-action"><span>⚙</span><span>设置</span></button></div>
    </aside>
  );
}

function Composer({ queue, onSend }) {
  const [value, setValue] = useState("");
  return (
    <div className="composer">
      {queue ? (
        <div className="prompt-queue" aria-label="排队中的提示">
          <span className="queue-pill">排队 1</span>
          <span className="queue-text">{queue}</span>
        </div>
      ) : null}
      <div className="composer-card">
        <textarea
          value={value}
          onChange={(event) => setValue(event.target.value)}
          placeholder="继续补充要求…"
          aria-label="继续补充要求"
        ></textarea>
        <div className="composer-footer">
          <div className="composer-hints">
            <span>＋ 添加上下文</span>
            <span>自动审查</span>
            <span>GPT-5.6 Sol · 高</span>
          </div>
          <button
            type="button"
            className="send-button"
            disabled={!value.trim()}
            onClick={() => {
              onSend(value);
              setValue("");
            }}
            aria-label="发送"
          >↑</button>
        </div>
      </div>
    </div>
  );
}

function Workbench({ tab, setTab, onClose, scene }) {
  const counts = {
    process: (scene.tools || []).length + (scene.steps || []).length,
    changes: (scene.files || []).length,
    agents: (scene.agents || []).length,
    reasoning: scene.reasoning ? 1 : 0,
  };
  return (
    <aside className="workbench" aria-label="工作现场">
      <div className="workbench-inner">
        <header className="workbench-header">
          <div>
            <strong>工作现场</strong>
            <p>{scene.running || scene.waiting ? "实时证据面 · 不打断正文" : "可回看证据 · 不改主栏结构"}</p>
          </div>
          <button type="button" onClick={onClose} aria-label="收起工作现场">×</button>
        </header>
        <div className="workbench-stats" aria-label="现场摘要">
          <div><b>{scene.statusTime || "—"}</b><span>耗时</span></div>
          <div><b>{(scene.tools || []).length}</b><span>工具</span></div>
          <div><b>{(scene.files || []).length}</b><span>文件</span></div>
          <div><b>{scene.tokens || "—"}</b><span>tokens</span></div>
        </div>
        <nav className="workbench-tabs" aria-label="工作现场分类">
          {[
            ["process", "过程"],
            ["changes", "变更"],
            ["agents", "子智能体"],
            ["reasoning", "推理"],
          ].map(([key, label]) => (
            <button
              key={key}
              type="button"
              className={tab === key ? "active" : ""}
              onClick={() => setTab(key)}
            >
              {label}
              {counts[key] ? <i>{counts[key]}</i> : null}
            </button>
          ))}
        </nav>
        <div className="workbench-body">
          {tab === "process" && <ProcessPanel scene={scene} />}
          {tab === "changes" && <ChangesPanel scene={scene} />}
          {tab === "agents" && <AgentsPanel scene={scene} />}
          {tab === "reasoning" && <ReasoningPanel scene={scene} />}
        </div>
      </div>
    </aside>
  );
}

function ProcessPanel({ scene }) {
  const tools = scene.tools || [];
  const steps = scene.steps || [];
  const doneSteps = steps.filter((s) => s.state === "done").length;
  return (
    <React.Fragment>
      {steps.length ? (
        <section className="work-section">
          <header>
            <strong>执行清单</strong>
            <span>{scene.stepProgress || `${doneSteps} / ${steps.length}`}</span>
          </header>
          <div className="step-rail">
            <div className="step-rail-track" aria-hidden="true">
              <i style={{ width: `${Math.max(8, (doneSteps / Math.max(1, steps.length)) * 100)}%` }}></i>
            </div>
            <div className="step-list">
              {steps.map((step) => (
                <div key={step.label} className={`step ${step.state || ""}`}>
                  <span className="step-mark">{step.state === "done" ? "✓" : step.state === "active" ? "" : ""}</span>
                  <div className="step-copy">
                    <span>{step.label}</span>
                    {step.note ? <small>{step.note}</small> : null}
                  </div>
                  <time>{step.time}</time>
                </div>
              ))}
            </div>
          </div>
        </section>
      ) : null}
      {scene.judgement ? (
        <section className="work-section">
          <header><strong>当前判断</strong><span>自动保存</span></header>
          <div className="thought-card live">
            <strong>{scene.judgement.title}</strong>
            <p>{scene.judgement.body}</p>
            {scene.judgement.hint ? <em>{scene.judgement.hint}</em> : null}
          </div>
        </section>
      ) : null}
      <section className="work-section">
        <header>
          <strong>工具活动</strong>
          <span>{tools.length ? `${tools.length} 项 · 非时间线` : "非时间线"}</span>
        </header>
        {tools.length === 0 ? (
          <div className="empty-hint">当前场景无工具事件。状态机仍可审计，只是此刻没有证据行。</div>
        ) : (
          <div className="tool-stack">
            {tools.map((tool) => (
              <div key={tool.id} className="tool-row rich" data-state={tool.state}>
                <span className="tool-state-dot" aria-hidden="true"></span>
                <span className="activity-copy">
                  <strong>{tool.name}</strong>
                  <small>{tool.preview}</small>
                  {tool.snippet ? <code className="tool-snippet">{tool.snippet}</code> : null}
                </span>
                <span className="tool-meta">
                  <span className="tool-state-label">{tool.stateLabel}</span>
                  {tool.duration ? <time>{tool.duration}</time> : null}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>
    </React.Fragment>
  );
}

function ChangesPanel({ scene }) {
  const files = scene.files || [];
  const plusTotal = files.reduce((n, f) => n + (parseInt(String(f.plus || "0").replace(/\D/g, ""), 10) || 0), 0);
  const minusTotal = files.reduce((n, f) => n + (parseInt(String(f.minus || "0").replace(/\D/g, ""), 10) || 0), 0);
  return (
    <React.Fragment>
      <section className="work-section">
        <header>
          <strong>文件变更</strong>
          <span>{files.length ? `${files.length} 个文件` : "无"}</span>
        </header>
        {files.length ? (
          <div className="change-totals">
            <span className="plus">+{plusTotal || 0}</span>
            <span className="minus">−{minusTotal || 0}</span>
            <em>未批准写操作不会出现在这里</em>
          </div>
        ) : null}
        {files.length === 0 ? (
          <div className="empty-hint">排队中的写操作不进入此投影。审批通过后才会出现 structured diff 摘要。</div>
        ) : files.map((file) => (
          <div key={file.path} className="file-card">
            <div className="file-row">
              <span className="activity-icon">{file.icon}</span>
              <span className="activity-copy"><strong>{file.path}</strong><small>{file.note}</small></span>
              <span className="change-count">
                {file.plus ? <i className="plus">{file.plus}</i> : null}
                {file.minus ? <i className="minus">{file.minus}</i> : null}
                {file.pending ? <i className="pending">{file.pending}</i> : null}
              </span>
            </div>
            {file.diff ? (
              <pre className="mini-diff" aria-label={`${file.path} 预览`}>{file.diff}</pre>
            ) : null}
          </div>
        ))}
      </section>
      {scene.changeNote ? (
        <section className="work-section">
          <div className="thought-card"><strong>投影规则</strong><p>{scene.changeNote}</p></div>
        </section>
      ) : null}
    </React.Fragment>
  );
}

function AgentsPanel({ scene }) {
  const agents = scene.agents || [];
  const running = agents.filter((a) => /运行/.test(a.meta || "")).length;
  return (
    <section className="work-section">
      <header>
        <strong>子智能体</strong>
        <span>{agents.length || 0} 个{running ? ` · ${running} 运行中` : ""}</span>
      </header>
      {agents.length === 0 ? (
        <div className="empty-hint">无子智能体任务。spawn 后只在这里与摘要卡出现，不插入正文事件流。</div>
      ) : (
        <div className="agent-stack">
          {agents.map((agent) => (
            <button key={agent.id} type="button" className="agent-card" onClick={scene.onOpenAgent}>
              <span className="agent-face" style={agent.faceStyle || undefined}>{agent.face}</span>
              <span className="activity-copy">
                <strong>{agent.name}</strong>
                <small>{agent.meta}</small>
                {agent.preview ? <em className="agent-preview">{agent.preview}</em> : null}
              </span>
              <span className="agent-go">›</span>
            </button>
          ))}
        </div>
      )}
      {agents.length ? (
        <p className="work-footnote">点击进入同构 transcript：用户提示 + 过程折叠 + 最终回答。</p>
      ) : null}
    </section>
  );
}

function ReasoningPanel({ scene }) {
  return (
    <section className="work-section">
      <header><strong>推理轨迹</strong><span>thinking · 永不伪装正文</span></header>
      {scene.reasoning ? (
        <details className="reasoning-fold" open={scene.running}>
          <summary>
            <span>{scene.running ? "推理中" : "已折叠推理"}</span>
            <time>{scene.reasoning.duration}</time>
          </summary>
          <div className="reasoning-body">
            <p>{scene.reasoning.body}</p>
            {scene.reasoning.bullets?.length ? (
              <ul>{scene.reasoning.bullets.map((b) => <li key={b}>{b}</li>)}</ul>
            ) : null}
          </div>
        </details>
      ) : (
        <div className="empty-hint">本场景无 reasoning 块；thinking 永不伪装成 commentary 或 final_answer。</div>
      )}
    </section>
  );
}

function TaskBrief({ title, body, attachments, planMode, meta }) {
  return (
    <header className="task-brief">
      <div className="task-brief-label">
        <span>当前任务</span>
        {planMode ? <em className="plan-badge">计划模式</em> : null}
        {meta ? <small>{meta}</small> : null}
      </div>
      <div className="task-brief-body">
        <h2>{title}</h2>
        {body ? <p>{body}</p> : null}
        {attachments?.length ? (
          <div className="attachment-row">
            {attachments.map((item) => (
              <span key={item} className="attachment-chip">
                <i aria-hidden="true">⎘</i>
                {item}
              </span>
            ))}
          </div>
        ) : null}
      </div>
    </header>
  );
}

function StatusStrip({ running, waiting, label, time, onStop, detail }) {
  const state = waiting ? "waiting" : running ? "running" : "completed";
  return (
    <div className="doc-status" data-state={state}>
      <div className="doc-status-main">
        <i></i>
        <strong>{label}</strong>
        <span>{detail || (waiting ? "等待模型首包" : running ? "正文与工作现场同步更新" : "结构保持不变 · 仅内容成熟")}</span>
      </div>
      <div className="doc-status-side">
        <time>{time}</time>
        {running || waiting ? (
          <button type="button" className="stop-chip" onClick={onStop}>停止</button>
        ) : null}
      </div>
    </div>
  );
}

function InterruptCard({ kind, title, children, footer }) {
  return (
    <article className={`interrupt-card interrupt-${kind}`} data-screen-label={`中断-${kind}`}>
      <header>
        <span className="interrupt-kind">{kind}</span>
        <strong>{title}</strong>
      </header>
      <div className="interrupt-body">{children}</div>
      {footer ? <footer className="interrupt-footer">{footer}</footer> : null}
    </article>
  );
}

function HistoryTurns({ turns }) {
  if (!turns?.length) return null;
  return (
    <section className="history-turns" aria-label="历史回合">
      {turns.map((turn) => (
        <details key={turn.id} className="history-turn">
          <summary>
            <span className="turn-num">{turn.num}</span>
            <span className="turn-q">{turn.question}</span>
            <span className="turn-a">{turn.answer}</span>
          </summary>
          <div className="history-turn-body">
            <p>{turn.detail}</p>
          </div>
        </details>
      ))}
    </section>
  );
}

function DocLead({ lead }) {
  if (!lead) return null;
  return (
    <aside className="doc-lead" aria-label="结论摘要">
      <div className="doc-lead-kicker">
        <span>{lead.kicker || "结论"}</span>
        {lead.badge ? <em>{lead.badge}</em> : null}
      </div>
      <strong>{lead.title}</strong>
      <p>{lead.body}</p>
      {lead.points?.length ? (
        <ul className="doc-lead-points">
          {lead.points.map((point) => <li key={point}>{point}</li>)}
        </ul>
      ) : null}
    </aside>
  );
}

/** Product-aligned edited files card (Timeline.tsx · EditedFilesSummary). */
function EditedFilesSummary({ summary }) {
  const [expanded, setExpanded] = useState(false);
  if (!summary?.files?.length) return null;

  const files = summary.files.map((f) => (
    typeof f === "string"
      ? { path: f, additions: 0, deletions: 0 }
      : {
          path: f.path,
          additions: Number(String(f.additions ?? f.plus ?? "0").replace(/[^\d]/g, "")) || 0,
          deletions: Number(String(f.deletions ?? f.minus ?? "0").replace(/[^\d]/g, "")) || 0,
        }
  ));
  const additions = summary.additions ?? files.reduce((n, f) => n + f.additions, 0);
  const deletions = summary.deletions ?? files.reduce((n, f) => n + f.deletions, 0);
  const previewLimit = summary.previewLimit ?? 3;
  const collapsed = !expanded && files.length > previewLimit;
  const visible = collapsed ? files.slice(0, previewLimit) : files;
  const hidden = files.length - visible.length;
  const label = files.length === 1 ? "已编辑 1 个文件" : `已编辑 ${files.length} 个文件`;
  const showActions = Boolean(summary.actions);

  return (
    <article className="edited-files-summary" data-screen-label="已编辑文件">
      <header>
        <span className="edited-files-icon" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
        </span>
        <div className="edited-files-copy">
          <strong>{label}</strong>
          <span>
            <b className="plus">+{additions}</b>
            <b className="minus">−{deletions}</b>
          </span>
        </div>
        {showActions ? (
          <div className="edited-files-actions">
            <button type="button" className="edited-files-ghost">撤销 ↩</button>
            <button type="button" className="edited-files-review">审核</button>
          </div>
        ) : null}
      </header>
      <ul>
        {visible.map((file) => (
          <li key={file.path}>
            <span title={file.path}>{file.path}</span>
            <span>
              <b className="plus">+{file.additions}</b>
              <b className="minus">−{file.deletions}</b>
            </span>
          </li>
        ))}
      </ul>
      {hidden > 0 ? (
        <button type="button" className="edited-files-more" onClick={() => setExpanded(true)}>
          再显示 {hidden} 个文件 <span aria-hidden="true">▾</span>
        </button>
      ) : null}
      {expanded && files.length > previewLimit ? (
        <button type="button" className="edited-files-more" onClick={() => setExpanded(false)}>
          收起 <span aria-hidden="true">▴</span>
        </button>
      ) : null}
    </article>
  );
}

function SlotMap({ slots }) {
  if (!slots?.length) return null;
  return (
    <div className="slot-map" aria-label="语义槽位">
      {slots.map((slot) => (
        <div key={slot.key} className="slot-card" data-tone={slot.tone || "neutral"}>
          <header>
            <span className="slot-key">{slot.key}</span>
            <strong>{slot.title}</strong>
          </header>
          <p>{slot.body}</p>
          <small>{slot.surface}</small>
        </div>
      ))}
    </div>
  );
}

function SplitMatrix({ matrix }) {
  if (!matrix) return null;
  return (
    <div className="split-matrix" aria-label={matrix.title || "边界对照"}>
      <div className="split-col stay">
        <header><span>留在主栏</span><strong>{matrix.stayTitle || "阅读与决策"}</strong></header>
        <ul>
          {(matrix.stay || []).map((item) => <li key={item}>{item}</li>)}
        </ul>
      </div>
      <div className="split-col move">
        <header><span>进入工作现场</span><strong>{matrix.moveTitle || "过程与证据"}</strong></header>
        <ul>
          {(matrix.move || []).map((item) => <li key={item}>{item}</li>)}
        </ul>
      </div>
    </div>
  );
}

function RuleList({ rules }) {
  if (!rules?.length) return null;
  return (
    <ol className="rule-list">
      {rules.map((rule, index) => (
        <li key={rule.title || rule}>
          <span className="rule-index">{String(index + 1).padStart(2, "0")}</span>
          <div>
            <strong>{typeof rule === "string" ? rule : rule.title}</strong>
            {rule.detail ? <p>{rule.detail}</p> : null}
          </div>
        </li>
      ))}
    </ol>
  );
}

function SourceStrip({ sources }) {
  if (!sources?.length) return null;
  return (
    <div className="source-strip" aria-label="证据来源">
      <span className="source-label">证据</span>
      <div className="source-chips">
        {sources.map((src) => (
          <code key={src}>{src}</code>
        ))}
      </div>
    </div>
  );
}

function DecisionBlock({ decision, interactive }) {
  const [picked, setPicked] = useState(() => {
    const rec = (decision || []).find((d) => d.recommended);
    return rec ? rec.title : decision?.[0]?.title || "";
  });
  if (!decision?.length) return null;
  return (
    <div className="decision-block" role={interactive ? "radiogroup" : "list"}>
      {decision.map((item) => {
        const active = picked === item.title;
        return (
          <button
            key={item.title}
            type="button"
            className={`decision-item ${item.recommended ? "recommended" : ""} ${active ? "active" : ""}`}
            data-active={String(active)}
            role={interactive ? "radio" : undefined}
            aria-checked={interactive ? active : undefined}
            onClick={() => interactive && setPicked(item.title)}
          >
            <header>
              <strong>{item.title}</strong>
              {item.recommended ? <em>推荐</em> : null}
            </header>
            <span>{item.body}</span>
            {item.meta ? <small>{item.meta}</small> : null}
          </button>
        );
      })}
    </div>
  );
}

function AnswerBody({ scene }) {
  if (scene.empty) {
    return (
      <div className="empty-session">
        <div className="empty-mark">Azem</div>
        <h2>从这里开始</h2>
        <p>空会话没有 Timeline 节点。非时间线模型下，主栏只保留欢迎态与 Composer，不预置假事件。</p>
        <div className="empty-starters">
          <button type="button">重构会话阅读模型</button>
          <button type="button">审查审批与过程边界</button>
          <button type="button">覆盖子智能体场景</button>
        </div>
      </div>
    );
  }

  if (scene.waiting && !scene.answerSections?.length) {
    return (
      <div className="waiting-shell">
        <div className="waiting-pulse" aria-hidden="true"></div>
        <div>
          <strong>模型准备中</strong>
          <p>对应现有 ThinkingPlaceholder：无 active reasoning / spawn 时显示轻量状态，不创建伪工具节点。</p>
        </div>
      </div>
    );
  }

  return (
    <article className="answer-document">
      <DocLead lead={scene.lead} />
      {scene.slots ? <SlotMap slots={scene.slots} /> : null}

      {(scene.answerSections || []).map((section, index) => (
        <section
          key={section.heading}
          id={section._id || `sec-${index}`}
          className={section.tone ? `sec-${section.tone}` : undefined}
        >
          {section.kicker ? <span className="sec-kicker">{section.kicker}</span> : null}
          <h3>{section.heading}</h3>
          {(section.paragraphs || []).map((p) => <p key={p}>{p}</p>)}
          {section.list ? (
            <ul className="plain-list">{section.list.map((item) => <li key={item}>{item}</li>)}</ul>
          ) : null}
          {section.rules ? <RuleList rules={section.rules} /> : null}
          {section.matrix ? <SplitMatrix matrix={section.matrix} /> : null}
          {section.decision ? (
            <DecisionBlock decision={section.decision} interactive={Boolean(section.interactive)} />
          ) : null}
          {section.sources ? <SourceStrip sources={section.sources} /> : null}
        </section>
      ))}

      {scene.streamingTail ? (
        <p className="streaming-tail">
          {scene.streamingTail}
          <i className="stream-caret" aria-hidden="true"></i>
        </p>
      ) : null}

      {scene.answerProgress ? (
        <div className="answer-progress" data-state={scene.running ? "running" : "completed"}>
          <header>
            <div>
              <strong>{scene.answerProgress.title}</strong>
              <p>{scene.answerProgress.note}</p>
            </div>
            <span>{scene.answerProgress.percent}</span>
          </header>
          <div className="progress-track"><i style={{ width: scene.answerProgress.width }}></i></div>
          {scene.answerProgress.steps?.length ? (
            <div className="progress-steps">
              {scene.answerProgress.steps.map((s) => (
                <span key={s.label} data-state={s.state}>{s.label}</span>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {scene.fileSummary ? <EditedFilesSummary summary={scene.fileSummary} /> : null}
    </article>
  );
}

function DocumentOutline({ sections, active }) {
  if (!sections?.length) return null;
  return (
    <nav className="doc-outline" aria-label="正文目录">
      {sections.map((s, i) => (
        <a
          key={s}
          href={`#sec-${i}`}
          className={active === i ? "active" : ""}
          onClick={(e) => {
            e.preventDefault();
            document.getElementById(`sec-${i}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
          }}
        >
          <i>{String(i + 1).padStart(2, "0")}</i>
          <span>{s}</span>
        </a>
      ))}
    </nav>
  );
}

function DocumentScreen({ scene, focused, setFocused, onStop, onOpenAgent }) {
  const [tab, setTab] = useState(scene.defaultTab || "process");
  useEffect(() => {
    setTab(scene.defaultTab || "process");
  }, [scene.id, scene.defaultTab]);

  const workbenchScene = useMemo(() => ({ ...scene, onOpenAgent }), [scene, onOpenAgent]);
  const outline = useMemo(
    () => (scene.answerSections || []).map((s) => s.heading).filter(Boolean).slice(0, 6),
    [scene.answerSections],
  );

  // Outline is design-lab only; Codex/product chat has no section nav.
  const showOutline = false;

  return (
    <div className={`screen document-screen ${focused ? "focused" : ""}`} data-screen-label="工作文档">
      <div className="document-scroll">
        <div className={`reading-shell ${showOutline ? "with-outline" : "solo"}`}>
          {showOutline ? <DocumentOutline sections={outline} active={0} /> : null}
          <main className="reading-column">
            {scene.history ? <HistoryTurns turns={scene.history} /> : null}
            {!scene.empty ? (
              <TaskBrief
                title={scene.taskTitle}
                body={scene.taskBody}
                attachments={scene.attachments}
                planMode={scene.planMode}
                meta={scene.taskMeta}
              />
            ) : null}
            {!scene.empty ? (
              <StatusStrip
                running={scene.running}
                waiting={scene.waiting}
                label={scene.statusLabel}
                time={scene.statusTime}
                detail={scene.statusDetail}
                onStop={onStop}
              />
            ) : null}

            {(scene.interrupts || []).map((card) => (
              <InterruptCard key={card.id} kind={card.kind} title={card.title} footer={card.footer}>
                {card.body}
              </InterruptCard>
            ))}

            <AnswerBody
              scene={{
                ...scene,
                answerSections: (scene.answerSections || []).map((section, index) => ({
                  ...section,
                  heading: section.heading,
                  _id: `sec-${index}`,
                })),
              }}
            />
          </main>
        </div>
      </div>
      {!scene.empty ? (
        <Workbench tab={tab} setTab={setTab} onClose={() => setFocused(true)} scene={workbenchScene} />
      ) : null}
      {focused && !scene.empty ? (
        <button
          type="button"
          className="text-action reveal-workbench"
          onClick={() => setFocused(false)}
        >显示工作现场</button>
      ) : null}
    </div>
  );
}

function TurnScreen({ scene }) {
  const turns = scene.turnCards || [];
  return (
    <div className="screen turn-screen" data-screen-label="回合卡片">
      <main className="turn-column">
        <header className="turn-intro">
          <div>
            <h2>把会话切成完整回合</h2>
            <p>一个问题、一份答案、一组附属过程；阅读时不再追逐事件节点。</p>
          </div>
          <span>{turns.length || 0} TURNS</span>
        </header>
        {scene.empty ? (
          <div className="empty-session compact"><p>空会话：仅 Composer，无回合卡。</p></div>
        ) : null}
        {turns.map((turn) => (
          <article key={turn.id} className={`turn-card ${turn.muted ? "muted" : ""}`}>
            <header className="turn-question">
              <span className="turn-number">{turn.num}</span>
              <p>{turn.question}</p>
              <time>{turn.time}</time>
            </header>
            <div className="turn-answer">
              <h3>{turn.heading}</h3>
              <p>{turn.body}</p>
              {turn.list ? <ul>{turn.list.map((item) => <li key={item}>{item}</li>)}</ul> : null}
              {turn.interrupt ? (
                <div className="turn-interrupt">{turn.interrupt}</div>
              ) : null}
            </div>
            <details className="turn-process" open={turn.processOpen}>
              <summary>
                {turn.processTitle}
                <span>{turn.processMeta}</span>
              </summary>
              <div className="turn-process-body">
                {(turn.chips || []).map((chip) => (
                  <div key={chip.title} className="process-chip">
                    <strong>{chip.title}</strong>
                    <small>{chip.meta}</small>
                  </div>
                ))}
              </div>
            </details>
          </article>
        ))}
      </main>
    </div>
  );
}

const notebookSections = {
  goal: {
    index: "01", title: "目标", subtitle: "用户想解决什么",
    heading: "让当前会话先回答问题，再解释过程",
    body: (
      <React.Fragment>
        <p>非 Timeline 模式的核心不是隐藏过程，而是重新确定阅读主次。用户进入会话时，第一眼应看到当前任务与已经形成的答案。</p>
        <ul>
          <li>正文结构在运行中保持稳定</li>
          <li>完成后不需要重新学习页面</li>
          <li>任何执行证据仍然可追溯</li>
        </ul>
      </React.Fragment>
    ),
  },
  decisions: {
    index: "02", title: "决策", subtitle: "为什么这样组织",
    heading: "默认采用工作文档，保留两种适配模式",
    body: (
      <React.Fragment>
        <p>短会话使用回合卡片，超长工程任务切换到会话笔记本。三种模式共享同一份底层事件，不要求改 Venat / session 契约。</p>
        <p>展示层只需要把事件投影到 <span className="inline-code">task / answer / interrupt / evidence</span> 四个语义槽。</p>
      </React.Fragment>
    ),
  },
  changes: {
    index: "03", title: "改动", subtitle: "涉及哪些界面",
    heading: "从 TimelineFeed 转向 SessionDocument 投影",
    body: (
      <React.Fragment>
        <p>保留底层事件与持久化顺序，新增面向阅读的语义投影。主视图消费投影，调试与审计仍可查看原始事件。</p>
        <ul>
          <li>任务 Brief：最近用户要求 + 附件</li>
          <li>答案正文：commentary 成熟视图 + final_answer</li>
          <li>中断卡：approval / question / plan / error</li>
          <li>工作现场：tool 生命周期、diff、subagents、thinking</li>
        </ul>
      </React.Fragment>
    ),
  },
  verification: {
    index: "04", title: "验证", subtitle: "如何判断有效",
    heading: "用定位速度和页面稳定性验收",
    body: (
      <React.Fragment>
        <p>需要验证用户是否可以在 5 秒内找到当前结论、最近改动和失败原因，并确保运行到完成没有大面积布局重排。</p>
        <ul>
          <li>运行中 / 完成后 DOM 主结构一致</li>
          <li>工具状态机仍可审计：queued → awaiting_approval → running → terminal</li>
          <li>子智能体抽屉与主会话同构</li>
        </ul>
      </React.Fragment>
    ),
  },
};

function NotebookScreen() {
  const [section, setSection] = useState("goal");
  const current = notebookSections[section];
  return (
    <div className="screen notebook-screen" data-screen-label="会话笔记本">
      <nav className="notebook-outline" aria-label="会话目录">
        <h2>会话目录</h2>
        <div className="outline-list">
          {Object.entries(notebookSections).map(([key, item]) => (
            <button
              key={key}
              type="button"
              className={`outline-button ${section === key ? "active" : ""}`}
              onClick={() => setSection(key)}
            >
              <span>{item.index}</span>
              <strong>{item.title}</strong>
            </button>
          ))}
        </div>
      </nav>
      <main className="notebook-document">
        <article className="notebook-page">
          <header>
            <span>SESSION NOTE / {current.index}</span>
            <h2>{current.heading}</h2>
            <p>{current.subtitle} · 自动从当前会话整理</p>
          </header>
          <div className="notebook-body" key={section}>
            <h3>{current.title}</h3>
            {current.body}
          </div>
        </article>
      </main>
      <aside className="notebook-evidence">
        <header><strong>关联证据</strong><span>随当前章节切换</span></header>
        <div className="evidence-block">
          <strong>来源文件</strong>
          <div className="evidence-link">frontend/src/components/Timeline.tsx</div>
          <div className="evidence-link">frontend/src/components/AgentSideChat.tsx</div>
          <div className="evidence-link">frontend/src/components/toolTimeline.ts</div>
        </div>
        <div className="evidence-block">
          <strong>会话状态</strong>
          <p>笔记本是长任务阅读壳。短问答仍用工作文档或回合卡片投影同一事件源。</p>
        </div>
        <div className="evidence-block">
          <strong>设计判断</strong>
          <p>
            {section === "goal"
              ? "用户目标优先于执行顺序。"
              : section === "decisions"
                ? "同一数据支持多种阅读投影。"
                : section === "changes"
                  ? "先改投影层，不动事件契约。"
                  : "以定位速度和状态机完整性验收。"}
          </p>
        </div>
      </aside>
    </div>
  );
}

function AgentDrawer({ open, onClose, running }) {
  if (!open) return null;
  return (
    <div className="drawer-layer" onClick={onClose}>
      <aside className="agent-drawer" role="dialog" aria-modal="true" aria-label="子智能体会话" onClick={(e) => e.stopPropagation()}>
        <header className="agent-drawer-header">
          <div>
            <h2>视觉一致性检查</h2>
            <small><em data-state={running ? "running" : "completed"}>{running ? "运行中" : "已完成"}</em><time>{running ? "1m04s" : "2m18s"}</time></small>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">×</button>
        </header>
        <div className="agent-drawer-body">
          <div className="user-chip">请检查非 Timeline 草稿的间距与状态色是否与主产品一致。</div>
          <details className="process-fold-lite" open={running}>
            <summary>{running ? "处理中" : "已处理"} · {running ? "1m04s" : "1m42s"}</summary>
            <div className="process-fold-lite-body">
              <div className="tool-row" data-state="completed"><span className="tool-state-dot"></span><span className="activity-copy"><strong>read_file</strong><small>prototype.css</small></span></div>
              <div className="tool-row" data-state={running ? "running" : "completed"}><span className="tool-state-dot"></span><span className="activity-copy"><strong>grep</strong><small>status colors</small></span></div>
            </div>
          </details>
          <div className="assistant-prose">
            <p>主栏应使用与 Azem 相同的纸面色与状态色。工具生命周期不要重新发明一套时间线，只在工作现场用状态列表呈现。</p>
            {!running ? <p>完成：间距与状态色对齐，无阻塞项。</p> : null}
          </div>
        </div>
      </aside>
    </div>
  );
}

function MapPanel({ open, onClose }) {
  if (!open) return null;
  return (
    <div className="map-layer" onClick={onClose}>
      <div className="map-panel" role="dialog" aria-modal="true" aria-label="场景映射表" onClick={(e) => e.stopPropagation()}>
        <header>
          <div>
            <h2>Timeline → 非时间线投影</h2>
            <p>覆盖当前产品所有会话块类型与关键状态机。底层事件顺序仍持久化；默认 UI 不再按事件流阅读。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭映射表">×</button>
        </header>
        <div className="map-table-wrap">
          <table className="map-table">
            <thead>
              <tr><th>来源</th><th>落到哪里</th><th>规则</th></tr>
            </thead>
            <tbody>
              {projectionMap.map((row) => (
                <tr key={row.kind}>
                  <td><code>{row.kind}</code></td>
                  <td>{row.surface}</td>
                  <td>{row.rule}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function buildScene(id, { forceRunning }) {
  const base = scenarios[id];
  const running = forceRunning == null ? base.running : forceRunning;
  const waiting = forceRunning === false ? false : base.waiting;

  const common = {
    id,
    running,
    waiting,
    empty: false,
    planMode: false,
    defaultTab: "process",
    statusTime: running || waiting ? "02:14" : "03:08",
    statusLabel: waiting ? "等待模型" : running ? "正在形成答案" : "答案已完成",
  };

  if (id === "empty") {
    return {
      ...common,
      empty: true,
      taskTitle: "",
      turnCards: [],
    };
  }

  if (id === "waiting") {
    return {
      ...common,
      taskTitle: "如果不使用 Timeline，会话区可以怎么组织？",
      taskBody: "结合当前产品里所有会话场景给一个草稿方案。",
      attachments: ["timeline-screenshot.png"],
      tools: [],
      steps: [],
      judgement: null,
      reasoning: { duration: "—", body: "首包尚未到达，不投影假 reasoning。" },
      turnCards: [{
        id: "t1", num: "01", time: "刚刚", muted: false,
        question: "如果不使用 Timeline，会话区可以怎么组织？",
        heading: "等待模型首包",
        body: "回合卡已建立，正文仍为空；状态条显示等待。",
        processTitle: "本回合尚未开始处理",
        processMeta: "0s",
        processOpen: false,
        chips: [{ title: "状态", meta: "waitingForModel" }],
      }],
    };
  }

  if (id === "streaming") {
    return {
      ...common,
      taskTitle: "把当前会话从 Timeline 改成稳定的工作文档",
      taskBody: "如果不按事件发生顺序展示，会话应该如何组织，既保留过程透明度，又让最终正文更容易阅读？",
      taskMeta: "流式成稿 · 结构已定",
      tokens: "9.6k",
      statusDetail: "正文与工作现场同步更新",
      attachments: ["session-current.png"],
      lead: {
        kicker: "进行中",
        badge: "原位成稿",
        title: "页面骨架已锁定，正在填充答案成熟度",
        body: "任务 Brief、答案区与工作现场同时存在。流式只改文字，不重排阅读壳。",
        points: ["结构先于内容", "commentary → 当前判断", "final_answer 原位生长"],
      },
      slots: [
        { key: "TASK", title: "任务 Brief", body: "已固定，不随流式重排", surface: "主栏顶部", tone: "ink" },
        { key: "ANSWER", title: "答案正文", body: "段落持续写入", surface: "主栏中部", tone: "blue" },
        { key: "INTERRUPT", title: "中断卡", body: "暂无决策中断", surface: "主栏内联", tone: "accent" },
        { key: "EVIDENCE", title: "工作现场", body: "工具与清单同步推进", surface: "右侧旁置", tone: "muted" },
      ],
      judgement: {
        title: "正在整理正文结构",
        body: "会话应该围绕可复用的结论组织，而不是把每个工具事件都变成阅读主线。",
        hint: "commentary 进入「当前判断」，成熟段落写入答案区",
      },
      reasoning: {
        duration: "18s",
        body: "先区分最终答案与过程证据，再决定哪些中断必须留在主栏。",
        bullets: ["不发明第二套时间线", "保留工具状态机可审计", "子智能体走同构抽屉"],
      },
      answerSections: [
        {
          kicker: "默认模式",
          heading: "建议采用「工作文档」作为默认模式",
          paragraphs: [
            "主区域只保留任务、当前结论和最终交付。模型的推理摘要、工具调用、文件变化与子智能体状态集中到右侧「工作现场」。",
            "这样运行中和完成后的页面结构一致：变化的是内容成熟度，不是页面形状。",
          ],
        },
        {
          kicker: "阅读壳",
          heading: "三个可选方向",
          paragraphs: ["同一事件源，三种投影；默认先锁工作文档。"],
          interactive: true,
          decision: [
            { title: "工作文档", body: "正文稳定，过程旁置。适合作为默认会话模式。", meta: "推荐", recommended: true },
            { title: "回合卡片", body: "每个用户问题形成一张完整卡片，适合多轮短问答。", meta: "短问答" },
            { title: "会话笔记本", body: "按章节导航目标、决策、改动和验证，适合超长任务。", meta: "长任务" },
          ],
        },
      ],
      streamingTail: "接着补齐审批与规划中断卡的位置，并核对写操作未批准前不进变更投影……",
      answerProgress: {
        title: "正在完成验证章节",
        percent: "62%",
        width: "62%",
        note: "final_answer 原位生长；commentary 不再与答案混排成事件流。",
        steps: [
          { label: "约束", state: "done" },
          { label: "架构", state: "done" },
          { label: "草稿", state: "active" },
          { label: "验收", state: "todo" },
        ],
      },
      tools: [
        { id: "1", name: "read_file", preview: "Timeline.tsx", state: "completed", stateLabel: "完成", duration: "1.4s", snippet: "process fold · TextPhase" },
        { id: "2", name: "grep", preview: "BlockKind", state: "completed", stateLabel: "完成", duration: "0.6s" },
        { id: "3", name: "read_file", preview: "styles.css", state: "running", stateLabel: "执行中", duration: "…" },
      ],
      steps: [
        { label: "读取会话展示约束", state: "done", time: "18s", note: "样式与 block 契约" },
        { label: "重组语义信息架构", state: "done", time: "34s", note: "四个语义槽" },
        { label: "生成非 Timeline 草稿", state: "active", time: "进行中", note: "正文成稿中" },
        { label: "浏览器检查与交付", state: "", time: "待处理" },
      ],
      stepProgress: "2 / 4",
      files: [
        { icon: "C", path: "styles.css", note: "活动编辑", pending: "约 3 处" },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "10:41", muted: false,
        question: "设计非 Timeline 的会话草稿",
        heading: "答案正在原位成稿",
        body: "回合内正文持续更新；过程芯片反映工具进度，不插入中间事件气泡。",
        processTitle: "本回合正在处理",
        processMeta: "2m14s",
        processOpen: true,
        chips: [
          { title: "读取", meta: "2 文件" },
          { title: "成稿", meta: "streaming" },
          { title: "验证", meta: "待处理" },
        ],
      }],
    };
  }

  if (id === "tools") {
    return {
      ...common,
      taskTitle: "展示完整工具生命周期",
      taskBody: "queued → awaiting_approval → running → completed/failed，不能把排队画成运行中。",
      defaultTab: "process",
      judgement: {
        title: "工具状态机保持可审计",
        body: "主栏不展开每个 tool disclosure；工作现场用状态列表投影。写操作未授权前不进变更页。",
      },
      tools: [
        { id: "1", name: "shell", preview: "go test ./frontend/...", state: "queued", stateLabel: "排队" },
        { id: "2", name: "apply_patch", preview: "app.jsx", state: "awaiting_approval", stateLabel: "待审批" },
        { id: "3", name: "read_file", preview: "types.ts", state: "running", stateLabel: "执行中" },
        { id: "4", name: "grep", preview: "process fold", state: "completed", stateLabel: "完成" },
        { id: "5", name: "shell", preview: "bun test", state: "failed", stateLabel: "失败" },
      ],
      steps: [
        { label: "收集工具状态约束", state: "done", time: "12s" },
        { label: "投影状态列表", state: "active", time: "进行中" },
        { label: "校验审批边界", state: "", time: "待处理" },
      ],
      stepProgress: "1 / 3",
      files: [],
      changeNote: "apply_patch 仍在 awaiting_approval，因此变更页为空。",
      answerSections: [
        {
          heading: "工具不再是阅读主线",
          paragraphs: [
            "用户需要知道「有没有在干活、卡在哪、改了什么」，不需要每个参数展开占满主栏。",
            "失败工具保留在过程列表；可从状态标签直接跳到输出。",
          ],
          list: [
            "queued 显示时钟态，不显示 spinner",
            "awaiting_approval 显示盾牌态，且不进文件变更",
            "running / completed / failed / cancelled 语义与现网一致",
          ],
        },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "10:44", muted: false,
        question: "展示完整工具生命周期",
        heading: "过程作为回合附件",
        body: "五个工具状态同时可见，但都收在回合底部过程区。",
        processTitle: "本回合正在处理",
        processMeta: "5 tools",
        processOpen: true,
        chips: [
          { title: "排队", meta: "1" },
          { title: "待审批", meta: "1" },
          { title: "执行中", meta: "1" },
          { title: "完成", meta: "1" },
          { title: "失败", meta: "1" },
        ],
      }],
    };
  }

  if (id === "approval") {
    return {
      ...common,
      taskTitle: "写入配置前需要你的授权",
      taskBody: "自动审查通过后仍按策略弹出审批。审批是中断，不是普通工具行。",
      defaultTab: "process",
      tools: [
        { id: "1", name: "apply_patch", preview: "config.yaml", state: "awaiting_approval", stateLabel: "待审批" },
      ],
      interrupts: [{
        id: "appr-1",
        kind: "approval",
        title: "apply_patch · 写入工作区",
        body: (
          <React.Fragment>
            <p className="interrupt-target"><span>目标</span><code>internal/config/config.yaml</code></p>
            <p>风险：写操作。未批准前不投影到文件变更，也不显示为已编辑。</p>
          </React.Fragment>
        ),
        footer: (
          <React.Fragment>
            <button type="button" className="btn ghost">拒绝</button>
            <button type="button" className="btn ghost">仅此一次</button>
            <button type="button" className="btn primary">本会话允许</button>
          </React.Fragment>
        ),
      }],
      answerSections: [
        {
          heading: "为什么审批留在主栏",
          paragraphs: [
            "审批、规划提问、计划确认和错误都属于「需要用户决策或必须看见的中断」。",
            "把它们塞进可折叠过程区会降低可见性，并违背现有 Approval 交互。",
          ],
        },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "10:46", muted: false,
        question: "写入配置前需要你的授权",
        heading: "中断卡插入回合正文",
        body: "审批动作保留 deny / once / session 三键。",
        interrupt: "审批：apply_patch → config.yaml",
        processTitle: "本回合过程",
        processMeta: "阻塞于审批",
        processOpen: true,
        chips: [{ title: "待审批", meta: "apply_patch" }],
      }],
    };
  }

  if (id === "plan") {
    return {
      ...common,
      planMode: true,
      running: false,
      waiting: false,
      statusLabel: "等待你的规划决策",
      statusTime: "—",
      taskTitle: "把会话从 Timeline 迁到语义文档投影",
      taskBody: "计划模式：只规划不实施，直到你确认执行。",
      interrupts: [
        {
          id: "q1",
          kind: "question",
          title: "规划问题 · 默认阅读模式",
          body: (
            <div className="option-grid">
              <button type="button" className="option active" data-active="true"><strong>工作文档</strong><span>正文稳定，过程旁置（推荐）</span></button>
              <button type="button" className="option"><strong>回合卡片</strong><span>一问一答，过程作附件</span></button>
              <button type="button" className="option"><strong>会话笔记本</strong><span>长任务分章导航</span></button>
            </div>
          ),
          footer: <button type="button" className="btn primary">提交选择</button>,
        },
        {
          id: "p1",
          kind: "plan",
          title: "实施计划",
          body: (
            <ol className="plan-list">
              <li>新增 SessionDocument 投影，不改 block 持久化顺序</li>
              <li>主栏渲染 task / answer / interrupt</li>
              <li>工作现场渲染 process / changes / agents / reasoning</li>
              <li>子智能体抽屉复用同一投影</li>
            </ol>
          ),
          footer: (
            <React.Fragment>
              <button type="button" className="btn ghost">提出疑问</button>
              <button type="button" className="btn ghost">修改计划</button>
              <button type="button" className="btn primary">执行计划</button>
            </React.Fragment>
          ),
        },
      ],
      answerSections: [
        {
          heading: "计划本身就是答案的一部分",
          paragraphs: [
            "question 与 plan 不是「过程噪音」，而是用户决策界面。非时间线模型仍把它们放在主阅读流。",
          ],
        },
      ],
      tools: [],
      turnCards: [{
        id: "t1", num: "01", time: "10:50", muted: false,
        question: "把会话从 Timeline 迁到语义文档投影",
        heading: "规划回合",
        body: "选择题与计划卡都在回合正文内完成，不混入工具时间线。",
        interrupt: "规划问题 + 实施计划待确认",
        processTitle: "本回合过程",
        processMeta: "无副作用",
        processOpen: false,
        chips: [{ title: "计划模式", meta: "只规划" }],
      }],
    };
  }

  if (id === "subagents") {
    return {
      ...common,
      defaultTab: "agents",
      taskTitle: "并行审查非 Timeline 草稿",
      taskBody: "主会话只显示 spawn 摘要；完整 transcript 进抽屉，并与主会话同构。",
      agents: [
        { id: "a1", face: "UX", name: "信息架构审查", meta: "已完成 · 3 条建议" },
        { id: "a2", face: "UI", name: "视觉一致性检查", meta: running ? "运行中 · 检查间距" : "已完成 · 无阻塞", faceStyle: { background: "#ece4f7", color: "#7553a5" } },
        { id: "a3", face: "QA", name: "场景覆盖检查", meta: "排队中", faceStyle: { background: "#ecece8", color: "#74746e" } },
      ],
      tools: [
        { id: "1", name: "subagent.spawn", preview: "信息架构审查", state: "completed", stateLabel: "完成" },
        { id: "2", name: "subagent.spawn", preview: "视觉一致性检查", state: "running", stateLabel: "执行中" },
        { id: "3", name: "subagent.spawn", preview: "场景覆盖检查", state: "queued", stateLabel: "排队" },
      ],
      judgement: {
        title: "并行 spawn 不占主栏带宽",
        body: "主栏保留任务与汇总结论；子智能体明细在工作现场与抽屉中展开。",
      },
      answerSections: [
        {
          heading: "子智能体与主会话同构",
          paragraphs: [
            "抽屉内：用户提示与最终答案用正文排版；commentary 与工具只进入「处理中 / 已处理」折叠。",
            "完成后默认折叠过程，可再打开回看——对应 UI-007。",
          ],
        },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "10:52", muted: false,
        question: "并行审查非 Timeline 草稿",
        heading: "主回合汇总，子任务侧开",
        body: "三个子智能体状态在过程芯片中可见，点进抽屉读完整 transcript。",
        processTitle: "本回合正在处理",
        processMeta: "2 running / 1 queued",
        processOpen: true,
        chips: [
          { title: "UX", meta: "完成" },
          { title: "UI", meta: "运行中" },
          { title: "QA", meta: "排队" },
        ],
      }],
    };
  }

  if (id === "files") {
    return {
      ...common,
      defaultTab: "changes",
      taskTitle: "活动文件编辑如何从计划总量过渡到 diff",
      taskBody: "对应 UI-006：排队与审批中的写不进变更；参数明确时显示计划总量，完成后原位变结构化 diff。",
      files: [
        { icon: "J", path: "app.jsx", note: "活动编辑", pending: "约 3 处" },
        { icon: "C", path: "prototype.css", note: "已完成", plus: "+84", minus: "−12" },
      ],
      changeNote: "未批准或参数不明的写操作不会出现在此列表。",
      tools: [
        { id: "1", name: "apply_patch", preview: "app.jsx", state: "running", stateLabel: "执行中" },
        { id: "2", name: "apply_patch", preview: "prototype.css", state: "completed", stateLabel: "完成" },
      ],
      answerSections: [
        {
          heading: "变更页是证据面，不是事件流",
          paragraphs: [
            "用户问「改了什么」时打开变更；问「结论是什么」时看正文。两者不再按时间戳交错。",
          ],
        },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "10:55", muted: false,
        question: "活动文件编辑如何从计划总量过渡到 diff",
        heading: "变更证据挂在回合下",
        body: "过程芯片显示写操作状态，明细进变更页。",
        processTitle: "本回合正在处理",
        processMeta: "2 files",
        processOpen: true,
        chips: [
          { title: "app.jsx", meta: "编辑中" },
          { title: "prototype.css", meta: "+84 / −12" },
        ],
      }],
    };
  }

  if (id === "multiturn") {
    return {
      ...common,
      running: false,
      waiting: false,
      statusLabel: "答案已完成",
      statusTime: "18m",
      history: [
        {
          id: "h1", num: "01",
          question: "Timeline 现在有什么问题？",
          answer: "过程透明，但阅读主线被事件打散。",
          detail: "工具、思考和最终内容按发生顺序混排，任务越长越难定位结论。",
        },
        {
          id: "h2", num: "02",
          question: "哪些块必须保留在主栏？",
          answer: "审批、规划提问、计划、错误。",
          detail: "这些是用户决策或失败可见性，不能塞进默认折叠过程。",
        },
      ],
      taskTitle: "综合给出可落地的非 Timeline 草稿",
      taskBody: "在已有两轮结论上，输出完整方案与场景覆盖。",
      answerSections: [
        {
          heading: "历史回合压缩，当前任务展开",
          paragraphs: [
            "多轮不是无限增长的事件河。历史轮次折叠为「问题 → 一句话结论」；当前任务保持完整 Brief + 正文。",
          ],
          list: [
            "最新用户要求永远在阅读起点附近",
            "旧回合可展开回看完整答案",
            "每轮的过程证据跟随该轮，不回流成全局时间线",
          ],
        },
        {
          heading: "三种布局共享投影",
          paragraphs: ["工作文档适合默认；回合卡片适合短问答；笔记本适合超长任务。"],
          decision: [
            { title: "工作文档", body: "默认模式", recommended: true },
            { title: "回合卡片", body: "多轮短协作" },
            { title: "会话笔记本", body: "长工程任务" },
          ],
        },
      ],
      fileSummary: {
        additions: 166,
        deletions: 62,
        previewLimit: 3,
        actions: true,
        files: [
          { path: "internal/dtos/validation_v2.go", additions: 4, deletions: 2 },
          { path: "internal/models/validation_v2.go", additions: 2, deletions: 0 },
          { path: "internal/repo/mysql/migrations/runner_test.go", additions: 22, deletions: 0 },
          { path: "frontend/src/components/Timeline.tsx", additions: 46, deletions: 8 },
          { path: "frontend/src/styles.css", additions: 38, deletions: 4 },
          { path: "frontend/src/components/sessionDocument.ts", additions: 18, deletions: 6 },
          { path: "internal/app/tool_timeline.go", additions: 12, deletions: 3 },
          { path: "docs/desktop.md", additions: 9, deletions: 2 },
          { path: "AGENTS.md", additions: 5, deletions: 1 },
          { path: "CHANGELOG.md", additions: 4, deletions: 0 },
          { path: "designs/session-without-timeline/app.jsx", additions: 4, deletions: 18 },
          { path: "designs/session-without-timeline/prototype.css", additions: 2, deletions: 18 },
        ],
      },
      tools: [
        { id: "1", name: "read_file", preview: "Timeline.tsx", state: "completed", stateLabel: "完成" },
      ],
      turnCards: [
        {
          id: "t1", num: "01", time: "10:18", muted: true,
          question: "Timeline 现在有什么问题？",
          heading: "过程透明，但阅读主线被事件打散。",
          body: "长任务里结论难定位。",
          processTitle: "本回合过程",
          processMeta: "已处理 4m",
          processOpen: false,
          chips: [{ title: "分析", meta: "完成" }],
        },
        {
          id: "t2", num: "02", time: "10:27", muted: true,
          question: "哪些块必须保留在主栏？",
          heading: "审批、规划、计划、错误",
          body: "其余过程旁置。",
          processTitle: "本回合过程",
          processMeta: "已处理 3m",
          processOpen: false,
          chips: [{ title: "决策", meta: "完成" }],
        },
        {
          id: "t3", num: "03", time: "10:58", muted: false,
          question: "综合给出可落地的非 Timeline 草稿",
          heading: "三种布局 + 完整场景映射",
          body: "本草稿即该轮交付。",
          processTitle: "本回合过程",
          processMeta: "已处理 6m",
          processOpen: false,
          chips: [
            { title: "设计", meta: "完成" },
            { title: "映射表", meta: "20 项" },
          ],
        },
      ],
    };
  }

  if (id === "error") {
    return {
      ...common,
      running: false,
      waiting: false,
      statusLabel: "运行失败",
      statusTime: "01:22",
      taskTitle: "继续生成会话草稿",
      taskBody: "上次在工具清理阶段被取消 / 失败，需要清晰的失败面。",
      interrupts: [{
        id: "err1",
        kind: "error",
        title: "运行已停止",
        body: <p>取消请求已异步送达协调器。部分工具未完成；失败原因保留在过程列表，不伪装成成功正文。</p>,
        footer: (
          <React.Fragment>
            <button type="button" className="btn ghost">查看过程</button>
            <button type="button" className="btn primary">重试</button>
          </React.Fragment>
        ),
      }],
      tools: [
        { id: "1", name: "read_file", preview: "app.jsx", state: "completed", stateLabel: "完成" },
        { id: "2", name: "shell", preview: "bun test", state: "failed", stateLabel: "失败" },
        { id: "3", name: "apply_patch", preview: "styles", state: "cancelled", stateLabel: "已取消" },
      ],
      answerSections: [
        {
          heading: "失败时正文与证据仍然分开",
          paragraphs: [
            "若已有部分 final_answer，保留已写内容；错误卡置顶可见。",
            "取消不标记侧边栏未读（仅跨会话成功/失败主 run 标记未读）。",
          ],
        },
      ],
      turnCards: [{
        id: "t1", num: "01", time: "11:02", muted: false,
        question: "继续生成会话草稿",
        heading: "运行在工具阶段失败",
        body: "错误卡与失败工具同时可见。",
        interrupt: "错误：运行已停止",
        processTitle: "本回合过程",
        processMeta: "含失败",
        processOpen: true,
        chips: [
          { title: "完成", meta: "1" },
          { title: "失败", meta: "1" },
          { title: "取消", meta: "1" },
        ],
      }],
    };
  }

  // complete
  return {
    ...common,
    running: false,
    waiting: false,
    statusLabel: "答案已完成",
    statusDetail: "结构保持不变 · 仅内容成熟",
    statusTime: "03:08",
    tokens: "18.4k",
    taskTitle: "把当前会话从 Timeline 改成稳定的工作文档",
    taskBody: "如果不按事件发生顺序展示，会话应该如何组织，既保留过程透明度，又让最终正文更容易阅读？",
    taskMeta: "会话投影 · 全场景草稿",
    attachments: ["session-current.png", "timeline-baseline.png"],
    lead: {
      kicker: "结论",
      badge: "默认推荐",
      title: "用工作文档承接会话，而不是换一条更漂亮的时间线",
      body: "主栏只读任务、答案与必须决策的中断；过程、变更、子智能体和 thinking 进入右侧工作现场。运行中与完成后页面骨架一致。",
      points: [
        "阅读主轴稳定：task → answer → interrupt",
        "证据可回看但不打断成稿",
        "底层事件顺序仍持久化，只改投影",
      ],
    },
    slots: [
      { key: "TASK", title: "任务 Brief", body: "最新用户要求与附件固定置顶", surface: "主栏顶部", tone: "ink" },
      { key: "ANSWER", title: "答案正文", body: "final_answer 原位成稿，只出现一次", surface: "主栏中部", tone: "blue" },
      { key: "INTERRUPT", title: "中断卡", body: "approval / question / plan / error", surface: "主栏内联", tone: "accent" },
      { key: "EVIDENCE", title: "工作现场", body: "过程 · 变更 · 子智能体 · 推理", surface: "右侧旁置", tone: "muted" },
    ],
    judgement: {
      title: "方案已收敛",
      body: "保留工作文档作为默认模式；长任务可切换笔记本；短问答可用回合卡片。同一事件源，三种阅读壳。",
      hint: "commentary 成熟后并入正文，不再单独占一条事件轨",
    },
    reasoning: {
      duration: "42s",
      body: "最终选择语义投影而不是另一条更漂亮的时间线。",
      bullets: [
        "用户要的是「当前结论在哪」，不是「第 47 个事件是什么」",
        "工具状态机仍可审计，只是退出主阅读流",
        "子智能体抽屉复用同一套阅读语法，避免第二套 UI",
      ],
    },
    steps: [
      { label: "读取会话展示与样式约束", state: "done", time: "18s", note: "Timeline / ThreadSurface / AgentSideChat" },
      { label: "重组语义信息架构", state: "done", time: "34s", note: "task · answer · interrupt · evidence" },
      { label: "生成非 Timeline 草稿", state: "done", time: "1m12s", note: "三方向同屏可切换" },
      { label: "浏览器检查与交付", state: "done", time: "26s", note: "全场景覆盖" },
    ],
    stepProgress: "4 / 4",
    tools: [
      { id: "1", name: "read_file", preview: "Timeline.tsx", state: "completed", stateLabel: "完成", duration: "2.1s", snippet: "segmentProcessTrail · TextPhase" },
      { id: "2", name: "grep", preview: "BlockKind · interrupt", state: "completed", stateLabel: "完成", duration: "0.8s", snippet: "approval · question · plan · error" },
      { id: "3", name: "write", preview: "designs/session-without-timeline", state: "completed", stateLabel: "完成", duration: "4.6s", snippet: "document · turns · notebook" },
    ],
    files: [
      {
        icon: "H", path: "index.html", note: "页面入口", plus: "+28", minus: "−2",
        diff: "  <title>Azem · 非 Timeline 会话</title>\n+ <meta name=\"design_doc_mode\" content=\"prototype\">",
      },
      {
        icon: "C", path: "prototype.css", note: "工作文档布局", plus: "+720", minus: "−48",
        diff: "+ .document-screen { grid: 1fr 318px }\n+ .workbench { evidence surface }",
      },
      {
        icon: "J", path: "app.jsx", note: "全场景草稿", plus: "+900", minus: "−120",
        diff: "+ buildScene(id) covers 11 scenarios\n+ Document / Turn / Notebook shells",
      },
    ],
    agents: [
      { id: "a1", face: "UX", name: "信息架构审查", meta: "已完成 · 3 条建议", preview: "主栏只留决策与答案" },
      { id: "a2", face: "UI", name: "视觉一致性检查", meta: "已完成 · 无阻塞", preview: "对齐暖白纸面与状态色", faceStyle: { background: "#ece4f7", color: "#7553a5" } },
    ],
    answerSections: [
      {
        kicker: "默认模式",
        heading: "建议采用「工作文档」作为默认模式",
        paragraphs: [
          "主区域只保留任务、当前结论和最终交付。模型的推理摘要、工具调用、文件变化与子智能体状态集中到右侧「工作现场」。",
          "这样运行中和完成后的页面结构一致：变化的是内容成熟度，不是页面形状。用户不用在「过程页」和「结果页」之间来回切换心智模型。",
        ],
        sources: ["Timeline.tsx", "sessionDocument.ts", "AgentSideChat.tsx"],
      },
      {
        kicker: "投影边界",
        heading: "哪些留在主栏，哪些进工作现场",
        paragraphs: [
          "边界按「用户是否必须看见并决策」划分，而不是按事件发生先后。",
        ],
        matrix: {
          stayTitle: "决策与交付",
          moveTitle: "过程与证据",
          stay: [
            "最新任务 Brief 与附件",
            "final_answer 正文（流式原位）",
            "approval / question / plan / error",
            "本轮改动汇总（完成后）",
          ],
          move: [
            "thinking / commentary 过程摘要",
            "tool 生命周期与输出预览",
            "active edit → structured diff",
            "子智能体 spawn 与 transcript",
          ],
        },
      },
      {
        kicker: "阅读壳",
        heading: "三个可选方向",
        paragraphs: ["同一事件源，三种阅读投影。默认工作文档；顶栏可切换对照。"],
        interactive: true,
        decision: [
          { title: "工作文档", body: "正文稳定、过程旁置。适合作为默认会话模式。", meta: "推荐 · 长任务友好", recommended: true },
          { title: "回合卡片", body: "每个用户问题形成一张完整卡片，过程作附件。", meta: "短问答 · 多轮回看" },
          { title: "会话笔记本", body: "按章节导航目标、决策、改动和验证。", meta: "超长工程 · 可导航" },
        ],
      },
      {
        kicker: "交互契约",
        heading: "关键交互规则",
        paragraphs: ["这些规则直接对应现网必须保留的产品契约。"],
        rules: [
          { title: "任务 Brief 固定置顶", detail: "最新用户要求不再像气泡一样漂移；历史轮次可压缩。" },
          { title: "答案原位成稿", detail: "commentary 成熟并入正文；final_answer 只出现一次，流式不换壳。" },
          { title: "中断卡留在主栏", detail: "approval / question / plan / error 必须可见可操作，禁止塞进折叠过程。" },
          { title: "工具状态机可审计", detail: "queued → awaiting_approval → running → terminal；排队不得伪装成运行。" },
          { title: "子智能体同构", detail: "抽屉内用户提示 + 过程折叠 + 最终回答；完成后过程默认收起。" },
        ],
      },
    ],
    answerProgress: {
      title: "交付已就绪",
      percent: "100%",
      width: "100%",
      note: "正文、变更和验证证据已经对齐。",
      steps: [
        { label: "约束", state: "done" },
        { label: "架构", state: "done" },
        { label: "草稿", state: "done" },
        { label: "验收", state: "done" },
      ],
    },
    fileSummary: {
      additions: 420,
      deletions: 1345,
      previewLimit: 5,
      files: [
        { path: "internal/mcp/manager_test.go", additions: 1, deletions: 393 },
        { path: "internal/mcp/manager.go", additions: 7, deletions: 571 },
        { path: "internal/app/mcp_runtime_test.go", additions: 406, deletions: 380 },
        { path: "AGENTS.md", additions: 1, deletions: 1 },
        { path: "CHANGELOG.md", additions: 5, deletions: 0 },
      ],
    },
    turnCards: [
      {
        id: "t1", num: "01", time: "10:18", muted: true,
        question: "分析一下当前 Timeline 的问题。",
        heading: "Timeline 对执行过程透明，但会让用户承担事件重组成本。",
        body: "工具、思考和最终内容按发生顺序混排，任务越长，真正需要回看的结论越难定位。",
        processTitle: "本回合过程",
        processMeta: "已处理 4m12s",
        processOpen: false,
        chips: [
          { title: "读取", meta: "6 个文件" },
          { title: "搜索", meta: "12 个匹配" },
          { title: "结论", meta: "3 条约束" },
        ],
      },
      {
        id: "t2", num: "02", time: "10:27", muted: false,
        question: "如果不使用 Timeline，可以做成什么样？",
        heading: "推荐把每一轮当作「决策卡片」，而不是事件容器。",
        body: "卡片顶部保留用户问题，中间是可持续更新的答案，底部只保留一条可展开的过程附件。",
        list: [
          "适合一问一答式工程协作",
          "历史轮次可以压缩为标题与结论",
          "过程仍然可审计，但不抢正文注意力",
        ],
        processTitle: "本回合过程",
        processMeta: "已处理 3m08s",
        processOpen: false,
        chips: [
          { title: "信息架构", meta: "工作文档" },
          { title: "交互原型", meta: "3 个方向" },
          { title: "验证", meta: "通过" },
        ],
      },
    ],
  };
}

function App() {
  const [layout, setLayout] = useState("document");
  const [scenarioId, setScenarioId] = useState("streaming");
  const [forceRunning, setForceRunning] = useState(null);
  const [focused, setFocused] = useState(false);
  const [mapOpen, setMapOpen] = useState(false);
  const [agentOpen, setAgentOpen] = useState(false);
  const [toast, setToast] = useState("");

  const scene = useMemo(
    () => buildScene(scenarioId, { forceRunning }),
    [scenarioId, forceRunning],
  );
  const meta = layoutMeta[layout];
  const queue = scenarioId === "streaming" || scenarioId === "tools"
    ? "补充：再加一版暗色对照"
    : "";

  useEffect(() => {
    setFocused(false);
    setAgentOpen(false);
    setForceRunning(null);
  }, [layout, scenarioId]);

  useEffect(() => {
    if (!toast) return undefined;
    const timer = window.setTimeout(() => setToast(""), 1800);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const screen = useMemo(() => {
    if (layout === "turns") return <TurnScreen scene={scene} />;
    if (layout === "notebook") return <NotebookScreen />;
    return (
      <DocumentScreen
        scene={scene}
        focused={focused}
        setFocused={setFocused}
        onStop={() => {
          setForceRunning(false);
          setToast("已发送停止（异步返回，不阻塞 Bridge）");
        }}
        onOpenAgent={() => setAgentOpen(true)}
      />
    );
  }, [focused, layout, scene]);

  return (
    <div className="prototype-shell">
      <header className="topbar">
        <div className="window-meta">
          <span className="traffic"><i></i><i></i><i></i></span>
          <span className="project-path"><strong>azem</strong> · feat/desktop-runtime-workspace-overhaul</span>
        </div>
        <nav className="variant-switch" aria-label="布局方向">
          {Object.entries(layoutMeta).map(([key, item]) => (
            <button
              key={key}
              type="button"
              aria-pressed={layout === key}
              onClick={() => setLayout(key)}
            >{item.label}</button>
          ))}
        </nav>
        <div className="top-actions">
          <button type="button" className="text-action" onClick={() => setMapOpen(true)}>场景映射表</button>
          <button
            type="button"
            className="text-action"
            onClick={() => setForceRunning((value) => {
              if (value == null) return !scene.running;
              return !value;
            })}
          >{scene.running || scene.waiting ? "模拟完成" : "模拟运行"}</button>
          <button
            type="button"
            className="run-pill"
            data-state={scene.running || scene.waiting ? "running" : "completed"}
            onClick={() => setForceRunning((value) => {
              if (value == null) return !scene.running;
              return !value;
            })}
          >
            <i></i>
            {scene.waiting ? "等待中" : scene.running ? "运行中 · 2m14s" : "已完成 · 3m08s"}
          </button>
        </div>
      </header>

      <div className="scenario-bar" aria-label="会话场景">
        {Object.entries(scenarios).map(([key, item]) => (
          <button
            key={key}
            type="button"
            className={scenarioId === key ? "active" : ""}
            onClick={() => setScenarioId(key)}
            title={item.blurb}
          >
            <strong>{item.label}</strong>
            <small>{item.blurb}</small>
          </button>
        ))}
      </div>

      <div className="app-grid">
        <GlobalSidebar unread={scenarioId === "complete" || scenarioId === "error"} />
        <section className="session-stage">
          <header className="session-header">
            <div className="session-title">
              <span className="session-index">DRAFT v3.1</span>
              <div>
                <h1>{meta.title}</h1>
                <p>非 Timeline 会话 · Codex 字阶对齐</p>
              </div>
            </div>
            <div className="concept-summary">
              <span className="concept-tag">{meta.tag}</span>
              <span>{meta.summary}</span>
            </div>
          </header>
          <div className="content-stage">
            {screen}
            {layout !== "notebook" ? (
              <Composer
                queue={queue}
                onSend={(value) => setToast(`已接收补充：${value.slice(0, 28)}${value.length > 28 ? "…" : ""}`)}
              />
            ) : null}
            {toast ? <div className="toast" role="status">{toast}</div> : null}
          </div>
        </section>
      </div>

      <AgentDrawer open={agentOpen} onClose={() => setAgentOpen(false)} running={scene.running} />
      <MapPanel open={mapOpen} onClose={() => setMapOpen(false)} />
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
