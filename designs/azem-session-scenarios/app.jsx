const { useEffect, useMemo, useState } = React;

/**
 * Full Azem desktop shell with real conversation scenarios.
 * Process expansion uses quiet work rows + plain notes — no timeline spine, no card grid.
 */

function markIcon(state) {
  if (state === "running") return "●";
  if (state === "failed") return "✕";
  if (state === "awaiting") return "◇";
  if (state === "queued") return "○";
  return "✓";
}

function ProcessBlock({ scene }) {
  if (!scene.notes?.length && !scene.tools?.length && !scene.group && !scene.subagents) return null;
  const running = scene.runState === "running";
  return (
    <details className="process" data-state={running ? "running" : "completed"} open={scene.processOpen}>
      <summary>
        <span className="chev" aria-hidden="true">›</span>
        <span>{running ? "处理中" : "已处理"}</span>
        {scene.duration ? <time>{scene.duration}</time> : null}
      </summary>
      <div className="process-body">
        {(scene.notes || []).map((note) => (
          <div className="note" key={note.title}>
            <strong>{note.title}</strong>
            <p>{note.body}</p>
          </div>
        ))}

        {scene.group ? (
          <div className="tool-group-line">
            <span>{scene.group}</span>
            <span className="count">{(scene.tools || []).length} 项</span>
          </div>
        ) : null}

        {(scene.tools || []).length ? (
          <div className="tools" role="list">
            {scene.tools.map((tool, index) => (
              <div className="tool" role="listitem" data-state={tool.state} key={`${tool.name}-${index}`}>
                <span className="mark" aria-hidden="true">{markIcon(tool.state)}</span>
                <span className="summary">
                  <span className="label">{tool.name}</span>
                  {tool.preview ? <span className="preview">{tool.preview}</span> : null}
                </span>
                {tool.status ? <span className="status">{tool.status}</span> : null}
              </div>
            ))}
          </div>
        ) : null}

        {scene.subagents ? (
          <div className="subagents-line">
            {scene.subagents.total} 个子智能体协作
            {" · "}
            {scene.subagents.running} 运行中
            {scene.subagents.queued ? ` · ${scene.subagents.queued} 排队` : ""}
            {scene.subagents.done ? ` · ${scene.subagents.done} 已结束` : ""}
            {" · "}
            <button type="button">打开侧栏查看 transcript</button>
          </div>
        ) : null}
      </div>
    </details>
  );
}

function AnswerBlock({ answer, showSep }) {
  if (!answer) return null;
  const hasBody = answer.lead || answer.streaming || (answer.sections && answer.sections.length);
  if (!hasBody) return null;
  return (
    <React.Fragment>
      {showSep ? <div className="answer-sep">最终回答</div> : null}
      <article className="answer">
        {answer.lead ? <p>{answer.lead}</p> : null}
        {answer.streaming ? <p>{answer.streaming}</p> : null}
        {(answer.sections || []).map((section) => (
          <section key={section.h}>
            <h3>{section.h}</h3>
            <div dangerouslySetInnerHTML={{ __html: section.html }} />
          </section>
        ))}
      </article>
    </React.Fragment>
  );
}

function Interrupt({ interrupt }) {
  if (!interrupt) return null;
  if (interrupt.kind === "approval") {
    return (
      <div className="interrupt approval">
        <header>
          <small>需要确认</small>
          <strong>{interrupt.title}</strong>
        </header>
        <div className="target"><span>目标</span><code>{interrupt.target}</code></div>
        <p>{interrupt.body}</p>
        <footer>
          <button type="button" className="btn ghost">拒绝</button>
          <button type="button" className="btn ghost">仅此一次</button>
          <button type="button" className="btn primary">本会话允许</button>
        </footer>
      </div>
    );
  }
  return (
    <div className="interrupt error">
      <header>
        <small>运行状态</small>
        <strong>{interrupt.title}</strong>
      </header>
      <p>{interrupt.body}</p>
      <footer>
        <button type="button" className="btn ghost">查看过程</button>
        <button type="button" className="btn primary">重试</button>
      </footer>
    </div>
  );
}

function Transcript({ scene }) {
  if (scene.empty) {
    return (
      <div className="empty">
        <h2>从这里开始</h2>
        <p>描述任务、附上截图或指向仓库路径。</p>
      </div>
    );
  }

  const hasProcess = (scene.notes && scene.notes.length) || (scene.tools && scene.tools.length) || scene.group || scene.subagents;
  const showSep = hasProcess && scene.answer && (scene.answer.lead || scene.answer.sections?.length);

  return (
    <React.Fragment>
      {scene.history?.length ? (
        <div className="history" aria-label="历史回合">
          {scene.history.map((item) => (
            <details key={item.n}>
              <summary>
                <span className="n">{item.n}</span>
                <span className="q">{item.q}</span>
                <span className="a">{item.a}</span>
              </summary>
              <div className="body">{item.detail}</div>
            </details>
          ))}
        </div>
      ) : null}

      {scene.user ? <div className="user-msg">{scene.user}</div> : null}

      {scene.runState === "running" && !hasProcess && !scene.interrupt ? (
        <div className="waiting"><i></i><span>思考中</span></div>
      ) : null}

      <ProcessBlock scene={scene} />
      <Interrupt interrupt={scene.interrupt} />
      <AnswerBlock answer={scene.answer} showSep={showSep} />
    </React.Fragment>
  );
}

function Inspector({ scene }) {
  return (
    <aside className="inspector" aria-label="运行上下文">
      <h2>运行上下文</h2>
      <div className="insp-sec">
        <header><span>上下文</span><span>CURRENT</span></header>
        <div className="gauge" style={{ ["--pct"]: `${Math.max(8, scene.contextPct || 0)}%` }}>
          <span>{scene.contextPct || 0}%</span>
        </div>
        <div className="insp-row"><span>缓存命中</span><b>65%</b></div>
        <div className="insp-row"><span>总缓存</span><b>62k</b></div>
      </div>

      {(scene.todos || []).length ? (
        <div className="insp-sec">
          <header>
            <span>执行计划</span>
            <span>{scene.todos.filter((t) => t.done).length}/{scene.todos.length}</span>
          </header>
          <div className="todo">
            {scene.todos.map((item) => (
              <div
                className="todo-item"
                key={item.text}
                data-done={String(Boolean(item.done))}
                data-active={String(Boolean(item.active))}
              >
                <i></i>
                <span>{item.text}</span>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {(scene.files || []).length ? (
        <div className="insp-sec">
          <header><span>工作区变更</span><span>{scene.files.length}</span></header>
          <div className="files">
            {scene.files.map((file) => (
              <div className="file" key={file.path}>
                <span>{file.path}</span>
                <span>
                  {file.plus ? <span className="plus">{file.plus}</span> : null}
                  {file.minus ? <>{" "}<span className="minus">{file.minus}</span></> : null}
                </span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="insp-sec">
          <header><span>工作区</span></header>
          <div className="insp-row"><span>分支</span><b>main</b></div>
          <div className="insp-row"><span>文件</span><b>+166</b></div>
        </div>
      )}
    </aside>
  );
}

function App() {
  const order = ["complete", "running", "approval", "subagents", "multiturn", "failed", "empty"];
  const [id, setId] = useState("complete");
  const [inspector, setInspector] = useState(true);
  const [hint, setHint] = useState(true);
  const scene = scenarios[id];

  useEffect(() => {
    setHint(true);
    const t = window.setTimeout(() => setHint(false), 4200);
    return () => window.clearTimeout(t);
  }, [id]);

  const hintText = useMemo(() => {
    const map = {
      complete: "完成态：过程默认收起，答案先行。点「已处理」展开——无竖轨、无卡片。",
      running: "运行中：过程默认展开，工具行显示执行中状态。",
      approval: "审批中断留在主栏，写操作未批准前不进变更列表。",
      subagents: "子智能体只占一行摘要，transcript 走侧栏。",
      multiturn: "历史回合压成一行，当前问题仍是右侧气泡。",
      failed: "失败卡可见，已形成的正文不抹掉。",
      empty: "空会话只有欢迎与 Composer。",
    };
    return map[id];
  }, [id]);

  return (
    <div className="desktop">
      <header className="top-chrome">
        <div style={{ display: "flex", alignItems: "center" }}>
          <span className="traffic"><i></i><i></i><i></i></span>
          <span className="project-chip"><b>azem</b> / main</span>
        </div>
        <div className="cmd-search"><span>搜索、跳转或执行命令</span><kbd>⌘K</kbd></div>
        <div className="top-right">
          <button type="button">草稿场景</button>
        </div>
      </header>

      <div className="workspace">
        <aside className="sidebar" aria-label="会话导航">
          <div className="side-tabs">
            <button type="button" className="active">会话</button>
            <button type="button">工作区</button>
          </div>
          <button type="button" className="nav-btn"><span>＋</span><span>新对话</span><span className="kbd">⌘N</span></button>
          <button type="button" className="nav-btn"><span>⌕</span><span>搜索</span><span className="kbd">⌘K</span></button>
          <div className="sec-label">项目</div>
          <div className="project">
            <span className="letter">A</span>
            <span className="meta">
              <strong>azem</strong>
              <small>main · 166 个文件</small>
            </span>
          </div>
          <div className="threads">
            {order.filter((key) => key !== "empty").map((key) => (
              <button
                key={key}
                type="button"
                className={`thread ${id === key ? "active" : ""}`}
                onClick={() => setId(key)}
              >
                <span className="dot"></span>
                <span className="title">{scenarios[key].title}</span>
              </button>
            ))}
            <button type="button" className={`thread ${id === "empty" ? "active" : ""}`} onClick={() => setId("empty")}>
              <span className="dot"></span>
              <span className="title">新对话</span>
            </button>
          </div>
          <div className="side-foot">
            <button type="button" className="nav-btn"><span>⚙</span><span>设置</span></button>
          </div>
        </aside>

        <section className={`main ${inspector ? "" : "no-inspector"}`}>
          <div className="thread-col">
            <header className="thread-head">
              <span className="task-k">TASK</span>
              <h1>{scene.title}</h1>
              <nav className="scenario-pills" aria-label="场景">
                {order.map((key) => (
                  <button
                    key={key}
                    type="button"
                    aria-pressed={id === key}
                    onClick={() => setId(key)}
                  >{scenarios[key].label}</button>
                ))}
              </nav>
              <div className="head-actions">
                {scene.runLabel ? (
                  <span className="run-pill" data-state={scene.runState}>
                    <i></i>{scene.runLabel}
                  </span>
                ) : null}
                <button
                  type="button"
                  className="icon-btn"
                  aria-label="切换检查器"
                  onClick={() => setInspector((v) => !v)}
                >☰</button>
              </div>
            </header>

            <div className="transcript-view">
              <div className="transcript">
                <Transcript scene={scene} />
              </div>
            </div>

            {!scene.empty || true ? (
              <div className="composer-dock">
                <div className="composer">
                  <textarea
                    placeholder={scene.empty ? "描述任务、附加图片或引用文件…" : "输入下一轮消息…"}
                    aria-label="消息输入"
                    defaultValue=""
                  ></textarea>
                  <div className="composer-foot">
                    <div className="composer-tools">
                      <span>＋</span>
                      <span>自动审查</span>
                      <span>计划</span>
                    </div>
                    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                      <span style={{ color: "var(--faint)", fontSize: 11 }}>GPT-5.6 · 高</span>
                      {scene.runState === "running" ? (
                        <button type="button" className="send stop" aria-label="停止">■</button>
                      ) : (
                        <button type="button" className="send" aria-label="发送">↑</button>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            ) : null}
          </div>

          {inspector ? <Inspector scene={scene} /> : null}
        </section>
      </div>

      {hint ? <div className="scenario-hint" role="status">{hintText}</div> : null}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
