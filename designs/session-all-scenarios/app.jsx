const { useEffect, useMemo, useState } = React;

/**
 * Unified Azem session prototype:
 * - Card language for plan / planning / approval / subagents / file summary
 * - Quiet process (no timeline spine)
 * - All real product scenarios switchable
 */

function mark(state) {
  if (state === "running") return "●";
  if (state === "failed") return "✕";
  if (state === "awaiting") return "◇";
  if (state === "queued") return "○";
  return "✓";
}

function statusOfAgents(list) {
  const running = list.filter((a) => a.state === "running").length;
  const queued = list.filter((a) => a.state === "queued").length;
  const completed = list.filter((a) => a.state === "completed").length;
  if (running) return { state: "running", label: "运行中", detail: `${running} 个运行中${queued ? ` · ${queued} 个排队` : ""}` };
  if (queued) return { state: "queued", label: "排队中", detail: `${queued} 个排队中` };
  return { state: "completed", label: "已完成", detail: `${completed} 个已结束` };
}

function ProcessBlock({ process }) {
  if (!process) return null;
  const has = (process.notes && process.notes.length) || (process.tools && process.tools.length) || process.group;
  if (!has) return null;
  const running = process.label === "处理中";
  return (
    <details className="process-fold" data-state={running ? "running" : "completed"} open={process.open}>
      <summary>
        <span className="chev" aria-hidden="true">›</span>
        <span>{process.label}</span>
        {process.time ? <time>{process.time}</time> : null}
      </summary>
      <div className="process-fold-body">
        {(process.notes || []).map((n) => (
          <div className="note" key={n.title}>
            <strong>{n.title}</strong>
            <p>{n.body}</p>
          </div>
        ))}
        {process.group ? (
          <div className="group-line">
            <span>{process.group}</span>
            <span className="cnt">{(process.tools || []).length} 项</span>
          </div>
        ) : null}
        {(process.tools || []).length ? (
          <div className="tools">
            {process.tools.map((t, i) => (
              <div className="tool-line" data-state={t.state} key={`${t.name}-${i}`}>
                <span className="mark">{mark(t.state)}</span>
                <span>
                  <span className="lbl">{t.name}</span>
                  {t.preview ? <span className="prev">{t.preview}</span> : null}
                </span>
                {t.status ? <span className="st">{t.status}</span> : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </details>
  );
}

function PlanningCard({ planning }) {
  const [selected, setSelected] = useState({});
  if (!planning) return null;
  const pick = (qi, label) => {
    setSelected((cur) => ({ ...cur, [qi]: label }));
  };
  return (
    <article className="planning-question">
      <header>
        <span className="card-ico q" aria-hidden="true">?</span>
        <div>
          <small>规划问题</small>
          <strong>{planning.title}</strong>
        </div>
      </header>
      <div className="planning-question-list">
        {planning.questions.map((q, qi) => (
          <div className="pq-block" key={q.question}>
            <span className="meta">{q.header}</span>
            <span className="q">{q.question}</span>
            <div className="planning-options">
              {q.options.map((opt) => (
                <button
                  key={opt.label}
                  type="button"
                  data-active={String(selected[qi] === opt.label || (!selected[qi] && opt.recommended))}
                  onClick={() => pick(qi, opt.label)}
                >
                  <span className="opt-mark" aria-hidden="true"></span>
                  <span>
                    <strong>
                      {opt.label}
                      {opt.recommended ? <em>推荐</em> : null}
                    </strong>
                    <small>{opt.desc}</small>
                  </span>
                </button>
              ))}
            </div>
            <label className="planning-other">
              <span>其他</span>
              <input placeholder="输入其他答案" />
            </label>
          </div>
        ))}
      </div>
      <footer>
        <button type="button" className="primary">提交选择</button>
      </footer>
    </article>
  );
}

function PlanCard({ plan }) {
  if (!plan) return null;
  return (
    <article className="plan-review" data-state={plan.state || "proposed"}>
      <header>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span className="card-ico p" aria-hidden="true">✎</span>
          <div>
            <small>计划</small>
            <h3>{plan.title}</h3>
          </div>
        </div>
        <span>计划 {plan.version}</span>
      </header>
      <div className="plan-review-body">
        <h4>实施步骤</h4>
        <ol>
          {plan.steps.map((s) => <li key={s}>{s}</li>)}
        </ol>
      </div>
      <footer>
        <button type="button">提出疑问</button>
        <button type="button">修改计划</button>
        <button type="button" className="primary">执行计划</button>
      </footer>
    </article>
  );
}

function ApprovalCard({ approval }) {
  if (!approval) return null;
  return (
    <article className="approval-card">
      <header>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span className="card-ico a" aria-hidden="true">◎</span>
          <div>
            <small>需要确认</small>
            <strong>{approval.title}</strong>
          </div>
        </div>
      </header>
      <div className="target"><span>目标</span><code>{approval.target}</code></div>
      <p>{approval.body}</p>
      <footer>
        <button type="button">拒绝</button>
        <button type="button">仅此一次</button>
        <button type="button" className="primary">本会话允许</button>
      </footer>
    </article>
  );
}

function ErrorCard({ error }) {
  if (!error) return null;
  return (
    <article className="error-card">
      <header>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span className="card-ico e" aria-hidden="true">!</span>
          <div>
            <small>运行状态</small>
            <strong>{error.title}</strong>
          </div>
        </div>
      </header>
      <p>{error.body}</p>
      <footer>
        <button type="button">查看过程</button>
        <button type="button" className="primary">重试</button>
      </footer>
    </article>
  );
}

function SubagentCard({ list, expanded, onToggle, onOpenAgent, onOpenCenter }) {
  const meta = statusOfAgents(list);
  const terminal = list.filter((a) => a.state === "completed" || a.state === "failed").length;
  const progress = Math.round((terminal / list.length) * 100);
  const indeterminate = meta.state === "running" && progress === 0;
  return (
    <section className="subagent-run-card" data-state={meta.state}>
      <button type="button" className="subagent-run-card-summary" aria-expanded={expanded} onClick={onToggle}>
        <span className="subagent-run-mark" aria-hidden="true">⌘<i>{list.length}</i></span>
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
      <div className="subagent-run-list" hidden={!expanded}>
        {list.map((agent) => (
          <button
            key={agent.id}
            type="button"
            className="subagent-run-row"
            data-state={agent.state}
            onClick={(e) => { e.stopPropagation(); onOpenAgent(agent.id); }}
          >
            <span className={`glyph ${agent.faceClass}`}>{agent.face}</span>
            <span>
              <strong>{agent.name}</strong>
              <small>{agent.preview}</small>
            </span>
            <em>{agent.stateLabel}</em>
            <span className="go">›</span>
          </button>
        ))}
      </div>
      <div className="card-footer">
        <button type="button" onClick={(e) => { e.stopPropagation(); onOpenCenter(); }}>打开子智能体中心</button>
        <span>点行进入详情页</span>
      </div>
    </section>
  );
}

function EditedSummary({ edited }) {
  const [expanded, setExpanded] = useState(false);
  if (!edited?.files?.length) return null;
  const files = edited.files.map((f) => ({
    path: f.path,
    additions: Number(String(f.additions ?? f.plus ?? "0").replace(/[^\d]/g, "")) || 0,
    deletions: Number(String(f.deletions ?? f.minus ?? "0").replace(/[^\d]/g, "")) || 0,
  }));
  const additions = edited.additions ?? edited.plus ?? files.reduce((n, f) => n + f.additions, 0);
  const deletions = edited.deletions ?? edited.minus ?? files.reduce((n, f) => n + f.deletions, 0);
  const plusTotal = Number(String(additions).replace(/[^\d]/g, "")) || 0;
  const minusTotal = Number(String(deletions).replace(/[^\d]/g, "")) || 0;
  const previewLimit = 3;
  const collapsed = !expanded && files.length > previewLimit;
  const visible = collapsed ? files.slice(0, previewLimit) : files;
  const hidden = files.length - visible.length;
  const count = edited.count ?? files.length;
  const label = count === 1 ? "已编辑 1 个文件" : `已编辑 ${count} 个文件`;

  return (
    <article className="edited-files-summary">
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
            <b className="plus">+{plusTotal}</b>
            <b className="minus">−{minusTotal}</b>
          </span>
        </div>
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

function Answer({ answer }) {
  if (!answer) return null;
  const has = answer.lead || answer.streaming || (answer.sections && answer.sections.length);
  if (!has) return null;
  return (
    <React.Fragment>
      <div className="sep">最终回答</div>
      <article className="answer">
        {answer.lead ? <p>{answer.lead}</p> : null}
        {answer.streaming ? <p>{answer.streaming}</p> : null}
        {(answer.sections || []).map((s) => (
          <section key={s.h}>
            <h3>{s.h}</h3>
            <div dangerouslySetInnerHTML={{ __html: s.html }} />
          </section>
        ))}
      </article>
    </React.Fragment>
  );
}

function SubagentsCenter({ list, onClose, onOpenAgent }) {
  const groups = [
    { key: "active", label: "进行中", agents: list.filter((a) => a.state === "running") },
    { key: "queued", label: "排队", agents: list.filter((a) => a.state === "queued") },
    { key: "finished", label: "已结束", agents: list.filter((a) => a.state === "completed" || a.state === "failed") },
  ].filter((g) => g.agents.length);
  return (
    <div className="drawer-layer" onClick={onClose}>
      <section className="subagents-page" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
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
            <div className="subagents-overview">
              <div data-state="active"><strong>{groups.find((g) => g.key === "active")?.agents.length || 0}</strong><span>进行中</span></div>
              <div data-state="queued"><strong>{groups.find((g) => g.key === "queued")?.agents.length || 0}</strong><span>排队</span></div>
              <div data-state="finished"><strong>{groups.find((g) => g.key === "finished")?.agents.length || 0}</strong><span>已结束</span></div>
            </div>
            <div className="subagent-groups">
              {groups.map((group) => (
                <section className="subagent-group" data-group={group.key} key={group.key}>
                  <header><h2>{group.label}</h2><span>{group.agents.length}</span></header>
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
  const queued = agent.state === "queued";
  const completed = agent.state === "completed";
  const hasProcess = agent.process?.length > 0;
  const hasAnswer = agent.answerSections?.length > 0;
  const hasNotes = agent.notes?.length > 0;

  return (
    <div className="drawer-layer detail" onClick={onClose}>
      <aside className="agent-side-chat" role="dialog" aria-modal="true" aria-label={agent.name} onClick={(e) => e.stopPropagation()}>
        <header className="agent-side-chat-header">
          <div className="agent-side-chat-heading">
            <span className={`glyph ${agent.faceClass} lg`}>{agent.face}</span>
            <div className="agent-side-chat-title">
              <h2>{agent.name}</h2>
              <small>
                <em data-state={agent.state}>{agent.stateLabel}</em>
                {agent.elapsed && agent.elapsed !== "—" ? <time>{agent.elapsed}</time> : null}
                {agent.type ? <span className="meta-chip">{agent.type}</span> : null}
              </small>
            </div>
          </div>
          <div className="agent-side-chat-actions">
            {running ? <button type="button" className="icon-btn danger" aria-label="停止" title="停止">■</button> : null}
            <button type="button" className="icon-btn" onClick={onClose} aria-label="关闭">×</button>
          </div>
        </header>

        <div className="agent-meta-bar" aria-label="运行信息">
          <span><b>模型</b>{agent.model || "—"}</span>
          <span><b>能力</b>{agent.capability || "—"}</span>
          <span><b>工具</b>{agent.toolsCount ?? 0}</span>
          <span><b>用量</b>{agent.tokens || "—"}</span>
        </div>

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
                title={`${item.name} · ${item.stateLabel}`}
                onClick={() => onSelect(item.id)}
              >
                <span className={`glyph ${item.faceClass} sm`}>{item.face}</span>
                <i data-state={item.state} aria-hidden="true"></i>
              </button>
            ))}
          </div>
          <em>{list.length}</em>
        </div>

        <div className="agent-side-chat-scroll">
          <div className="agent-side-chat-transcript">
            <div className="detail-user">{agent.prompt}</div>

            {queued ? (
              <div className="detail-empty">
                <div className="detail-empty-mark">…</div>
                <strong>排队中</strong>
                <p>{agent.queuedReason || "等待并发槽位释放后启动。"}</p>
                <ul>
                  <li>不会提前显示假工具节点</li>
                  <li>槽位空出后自动进入「处理中」</li>
                  <li>可在子智能体中心查看队列位置</li>
                </ul>
              </div>
            ) : null}

            {!queued && (hasNotes || hasProcess) ? (
              <details
                className="process-fold detail-process"
                data-state={running ? "running" : "completed"}
                open={agent.processOpen || running}
              >
                <summary>
                  <span className="chev" aria-hidden="true">›</span>
                  <span>{agent.processLabel || (running ? "处理中" : "已处理")}</span>
                  {agent.processTime ? <time>{agent.processTime}</time> : null}
                  {hasProcess ? <span className="process-count">{agent.process.length} 个工具</span> : null}
                </summary>
                <div className="process-fold-body">
                  {(agent.notes || []).map((n) => (
                    <div className="note" key={n.title}>
                      <strong>{n.title}</strong>
                      <p>{n.body}</p>
                    </div>
                  ))}
                  {hasProcess ? (
                    <div className="tools">
                      {agent.process.map((t, i) => (
                        <div className="tool-line" data-state={t.state || "done"} key={`${t.name}-${i}`}>
                          <span className="mark">{mark(t.state || "done")}</span>
                          <span>
                            <span className="lbl">{t.name}</span>
                            {t.preview ? <span className="prev">{t.preview}</span> : null}
                          </span>
                          {t.status ? <span className="st">{t.status}</span> : null}
                        </div>
                      ))}
                    </div>
                  ) : null}
                </div>
              </details>
            ) : null}

            {hasAnswer ? (
              <React.Fragment>
                {!running ? <div className="sep">最终回答</div> : <div className="sep">当前结论</div>}
                <article className="detail-answer answer">
                  {agent.answerSections.map((s) => (
                    <section key={s.h}>
                      <h3>{s.h}</h3>
                      <div dangerouslySetInnerHTML={{ __html: s.html }} />
                    </section>
                  ))}
                </article>
              </React.Fragment>
            ) : null}

            {running && hasAnswer ? (
              <div className="detail-live">
                <i></i>
                <span>仍在补充细节，结论会原位更新</span>
              </div>
            ) : null}

            {running && !hasAnswer ? (
              <div className="detail-live">
                <i></i>
                <span>正在形成最终回答…</span>
              </div>
            ) : null}
          </div>
        </div>

        <footer className="agent-side-chat-foot">
          <span data-state={agent.state}>{agent.stateLabel}</span>
          <span>{agent.elapsed && agent.elapsed !== "—" ? agent.elapsed : "—"}</span>
          <span>{agent.toolsCount ?? 0} 工具</span>
          <span>{agent.tokens || "—"} tokens</span>
        </footer>
      </aside>
    </div>
  );
}

function Inspector({ scene }) {
  return (
    <aside className="inspector" aria-label="运行上下文">
      <h2>运行上下文</h2>
      <div className="insp-sec">
        <header><span>上下文</span><span>CURRENT</span></header>
        <div className="gauge" style={{ ["--pct"]: `${Math.max(6, scene.contextPct || 0)}%` }}>
          <span>{scene.contextPct || 0}%</span>
        </div>
        <div className="insp-row"><span>缓存命中</span><b>65%</b></div>
      </div>
      {(scene.todos || []).length ? (
        <div className="insp-sec">
          <header>
            <span>执行计划</span>
            <span>{scene.todos.filter((t) => t.done).length}/{scene.todos.length}</span>
          </header>
          <div className="todo">
            {scene.todos.map((t) => (
              <div key={t.text} className="todo-item" data-done={String(!!t.done)} data-active={String(!!t.active)}>
                <i></i><span>{t.text}</span>
              </div>
            ))}
          </div>
        </div>
      ) : null}
      {scene.edited?.files?.length ? (
        <div className="insp-sec">
          <header><span>工作区变更</span><span>{scene.edited.count}</span></header>
          <div className="files">
            {scene.edited.files.map((f) => (
              <div className="file" key={f.path}>
                <span>{f.path}</span>
                <span><span className="plus">{f.plus}</span> <span className="minus">{f.minus}</span></span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="insp-sec">
          <header><span>工作区</span></header>
          <div className="insp-row"><span>分支</span><b>main</b></div>
        </div>
      )}
    </aside>
  );
}

function HistoryTurn({ turn }) {
  const toolCount = turn.process?.tools?.length || 0;
  const hasProcess = turn.process && (
    (turn.process.notes && turn.process.notes.length)
    || toolCount
    || turn.process.group
  );
  return (
    <section className="turn-block history-turn" data-turn={turn.n}>
      <div className="turn-current-label history-turn-label">
        <span className="turn-badge">回合 {turn.n}</span>
        {hasProcess ? (
          <em>{turn.process.label}{turn.process.time ? ` · ${turn.process.time}` : ""}{toolCount ? ` · ${toolCount} 工具` : ""}</em>
        ) : null}
      </div>
      <div className="user history-user">{turn.q}</div>
      {hasProcess ? (
        <ProcessBlock process={{ ...turn.process, open: false }} />
      ) : null}
      {turn.answer ? <Answer answer={turn.answer} /> : (
        turn.a ? <article className="answer"><p>{turn.a}</p></article> : null
      )}
      {turn.edited ? <EditedSummary edited={turn.edited} /> : null}
    </section>
  );
}

function CurrentTurn({ scene, cardExpanded, setCardExpanded, onOpenAgent, onOpenCenter }) {
  const showTurnChrome = Boolean(scene.history?.length);
  return (
    <section className={`turn-block current-turn ${showTurnChrome ? "has-label" : ""}`}>
      {showTurnChrome ? (
        <div className="turn-current-label">
          <span className="turn-badge current">当前回合</span>
          {scene.runState === "running" ? <em data-live="true">处理中</em> : null}
          {scene.runState === "completed" ? <em>已完成</em> : null}
          {scene.runState === "failed" ? <em data-fail="true">已停止</em> : null}
        </div>
      ) : null}

      {scene.user ? <div className="user">{scene.user}</div> : null}

      {scene.runState === "running" && !scene.process && !scene.planning ? (
        <div className="waiting"><i></i>思考中</div>
      ) : null}

      <ProcessBlock process={scene.process} />
      <PlanningCard planning={scene.planning} />
      <PlanCard plan={scene.plan} />
      <ApprovalCard approval={scene.approval} />
      <ErrorCard error={scene.error} />

      {scene.agents ? (
        <SubagentCard
          list={scene.agents}
          expanded={cardExpanded}
          onToggle={() => setCardExpanded((v) => !v)}
          onOpenAgent={onOpenAgent}
          onOpenCenter={onOpenCenter}
        />
      ) : null}

      <Answer answer={scene.answer} />
      <EditedSummary edited={scene.edited} />
    </section>
  );
}

function Transcript({ scene, cardExpanded, setCardExpanded, onOpenAgent, onOpenCenter }) {
  if (scene.empty) {
    return (
      <div className="empty">
        <h2>从这里开始</h2>
        <p>描述任务、附上截图或指向仓库路径。可切换顶栏场景查看全部状态。</p>
      </div>
    );
  }
  return (
    <React.Fragment>
      {scene.history?.length ? (
        <div className="history-stack" aria-label="历史回合">
          {scene.history.map((h) => <HistoryTurn key={h.n} turn={h} />)}
        </div>
      ) : null}

      <CurrentTurn
        scene={scene}
        cardExpanded={cardExpanded}
        setCardExpanded={setCardExpanded}
        onOpenAgent={onOpenAgent}
        onOpenCenter={onOpenCenter}
      />
    </React.Fragment>
  );
}

function App() {
  const [id, setId] = useState("multiturn_live");
  const [cardExpanded, setCardExpanded] = useState(true);
  const [centerOpen, setCenterOpen] = useState(false);
  const [detailId, setDetailId] = useState("");
  const [hint, setHint] = useState(true);
  const scene = scenarios[id];
  const agentList = scene.agents || agents;

  useEffect(() => {
    setCardExpanded(id === "subagents");
    setCenterOpen(false);
    setDetailId("");
    setHint(true);
    const t = window.setTimeout(() => setHint(false), 5200);
    return () => window.clearTimeout(t);
  }, [id]);

  const hintText = useMemo(() => {
    const map = {
      complete: "完成态：答案 + 收起的过程 + 文件变更卡。",
      running: "运行中：过程展开，流式正文。",
      plan: "计划模式：规划问题卡 + 实施计划卡。",
      approval: "审批卡留在主栏，写操作未批准前不进变更。",
      subagents: "协作卡片 → 中心页 → 详情页。",
      multiturn: "多轮完成：往上滑即可看完整历史；每轮含答案与「已处理」。",
      multiturn_live: "多轮进行中：历史直接铺开，当前回合仍在「处理中」。",
      multiturn_tools: "每轮都有工具：整段会话连续滚动回看。",
      files: "活动编辑与已编辑文件汇总卡。",
      failed: "错误卡 + 部分正文保留。",
      empty: "空会话欢迎态。",
    };
    return map[id] || "";
  }, [id]);

  const openAgent = (aid) => {
    setDetailId(aid);
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
          <button type="button" onClick={() => setCenterOpen(true)}>子智能体中心</button>
        </div>
      </header>

      <nav className="scenario-rail" aria-label="全部场景">
        {scenarioOrder.map((key) => (
          <button
            key={key}
            type="button"
            aria-pressed={id === key}
            onClick={() => setId(key)}
          >{scenarios[key].label}</button>
        ))}
      </nav>

      <div className="workspace with-inspector">
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
            <div><strong>azem</strong><small>main · 全场景草稿</small></div>
          </div>
          <div className="threads">
            {scenarioOrder.filter((k) => k !== "empty").map((key) => (
              <button
                key={key}
                type="button"
                className={`thread ${id === key ? "active" : ""}`}
                onClick={() => setId(key)}
              >
                <span className="dot"></span>
                {scenarios[key].title}
              </button>
            ))}
            <button type="button" className={`thread ${id === "empty" ? "active" : ""}`} onClick={() => setId("empty")}>
              <span className="dot"></span>新对话
            </button>
          </div>
          <div className="foot"><button type="button" className="nav">⚙ 设置</button></div>
        </aside>

        <div className="main-col">
          <header className="head">
            <span className="k">TASK</span>
            <h1>{scene.title}</h1>
            {scene.planMode ? <span className="plan-badge">计划模式</span> : null}
            {scene.runLabel ? (
              <span className="pill" data-state={scene.runState === "completed" ? "completed" : undefined}>
                <i></i>{scene.runLabel}
              </span>
            ) : null}
          </header>

          <div className="scroll">
            <div className="transcript">
              <Transcript
                scene={scene}
                cardExpanded={cardExpanded}
                setCardExpanded={setCardExpanded}
                onOpenAgent={openAgent}
                onOpenCenter={() => setCenterOpen(true)}
              />
            </div>
          </div>

          <div className="composer-dock">
            <div className="composer">
              <textarea
                placeholder={scene.empty ? "描述任务、附加图片或引用文件…" : "输入下一轮消息…"}
                aria-label="消息输入"
              ></textarea>
              <div className="composer-foot">
                <span>＋ · 自动审查 · {scene.planMode ? "计划 · 开" : "计划"}</span>
                <span>GPT-5.6 · 高  ↑</span>
              </div>
            </div>
          </div>

          {centerOpen ? (
            <SubagentsCenter list={agentList} onClose={() => setCenterOpen(false)} onOpenAgent={openAgent} />
          ) : null}
          {detailId ? (
            <AgentDetail agentId={detailId} list={agentList} onClose={() => setDetailId("")} onSelect={setDetailId} />
          ) : null}
          {hint ? <div className="hint-bar" role="status">{hintText}</div> : null}
        </div>

        <Inspector scene={scene} />
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
