import { useEffect, useRef, useState } from "react";
import {
  ArrowDown, ArrowUp, Bot, Check, ChevronDown, ChevronRight, Circle, CircleStop,
  FileCode2, GitBranch, ImagePlus, LoaderCircle, Paperclip, Send, ShieldCheck,
  PanelRightClose, PanelRightOpen, Sparkles, Users, X,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { cancelActive, execute, guide, importAttachment, startTurn } from "../bridge";
import { toolDisplayName, translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { Block, Snapshot } from "../types";

export default function ThreadSurface() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const running = useRuntimeStore((state) => state.running);
  const runId = useRuntimeStore((state) => state.runId);
  const runStartedAt = useRuntimeStore((state) => state.runStartedAt);
  const activity = useRuntimeStore((state) => state.activity);
  const error = useRuntimeStore((state) => state.error);
  const approvalMode = useRuntimeStore((state) => state.approvalMode) || snapshot.approvalMode;
  const planMode = useRuntimeStore((state) => state.planMode);
  const attachments = useRuntimeStore((state) => state.attachments);
  const branches = useRuntimeStore((state) => state.branches);
  const skills = useRuntimeStore((state) => state.skills);
  const addOptimisticUser = useRuntimeStore((state) => state.addOptimisticUser);
  const setRunId = useRuntimeStore((state) => state.setRunId);
  const failRun = useRuntimeStore((state) => state.failRun);
  const clearAttachments = useRuntimeStore((state) => state.clearAttachments);
  const setError = useRuntimeStore((state) => state.setError);
  const setPlanMode = useRuntimeStore((state) => state.setPlanMode);
  const [prompt, setPrompt] = useState("");
  const snapshotAgentMode = snapshot.agentMode || "single";
  const [agentMode, setAgentMode] = useState(snapshotAgentMode);
  const [following, setFollowing] = useState(true);
  const viewport = useRef<HTMLDivElement>(null);
  const t = translator(snapshot.language);
  const empty = blocks.length === 0 && !running;
  const elapsed = useElapsed(runStartedAt, running);

  useEffect(() => {
    if (following) viewport.current?.scrollTo({ top: viewport.current.scrollHeight, behavior: "smooth" });
  }, [blocks, following]);

  useEffect(() => setAgentMode(snapshotAgentMode), [currentSessionId, snapshotAgentMode]);

  const submit = async () => {
    const text = prompt.trim();
    if (!text) return;
    setPrompt("");
    addOptimisticUser(text);
    try {
      if (running && runId) await guide(currentSessionId, runId, text);
      else {
        const nextRun = await startTurn({
          sessionId: currentSessionId, prompt: text, provider: snapshot.provider,
          model: snapshot.model, reasoning: snapshot.reasoning, agentMode,
          planMode, disableSubagents: false,
          activeSkills: skills.filter((skill) => skill.eager && !skill.disabled).map((skill) => skill.name),
          images: attachments,
        });
        setRunId(nextRun);
      }
      clearAttachments();
      setFollowing(true);
    } catch (cause) {
      failRun(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const attach = async (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) {
      try {
        useRuntimeStore.getState().addAttachment(await importAttachment(currentSessionId, file));
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
  };

  const cancel = async () => {
    try {
      await cancelActive(agentMode === "team");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return (
    <section className={`thread-surface ${empty ? "empty-thread" : "active-thread"}`}>
      <ThreadHeader empty={empty} elapsed={elapsed} agentMode={agentMode} setAgentMode={setAgentMode} />
      {empty ? (
        <div className="empty-composer-wrap">
          <div className="azem-symbol" aria-hidden="true"><span /><span /></div>
          <h1>{t("promptTitle")}</h1>
          <Composer
            prompt={prompt} setPrompt={setPrompt} submit={submit} attach={attach}
            agentMode={agentMode} setAgentMode={setAgentMode} planMode={planMode} setPlanMode={setPlanMode}
            approvalMode={approvalMode} branches={branches} running={running}
          />
        </div>
      ) : (
        <>
          <div className="transcript-viewport" ref={viewport} onScroll={(event) => {
            const node = event.currentTarget;
            setFollowing(node.scrollHeight - node.scrollTop - node.clientHeight < 72);
          }}>
            <div className="transcript">
              {blocks.map((block) => <TimelineBlock key={block.id} block={block} language={snapshot.language} />)}
              {running && <div className="run-status"><LoaderCircle className="spin" size={15} /><span>{activityLabel(activity, snapshot.language)}</span><time>{elapsed}</time></div>}
              {error && <div className="inline-error" role="alert">{error}</div>}
            </div>
          </div>
          {!following && <button className="jump-latest" onClick={() => setFollowing(true)}><ArrowDown size={14} />返回最新</button>}
          <div className="composer-dock">
            <Composer
              prompt={prompt} setPrompt={setPrompt} submit={submit} attach={attach}
              agentMode={agentMode} setAgentMode={setAgentMode} planMode={planMode} setPlanMode={setPlanMode}
              approvalMode={approvalMode} branches={branches} running={running} cancel={cancel}
            />
          </div>
        </>
      )}
    </section>
  );
}

function ThreadHeader({ empty, elapsed, agentMode, setAgentMode }: { empty: boolean; elapsed: string; agentMode: string; setAgentMode: (value: string) => void }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const title = useRuntimeStore((state) => state.currentTitle);
  const running = useRuntimeStore((state) => state.running);
  const t = translator(snapshot.language);
  const heading = empty ? t("newSession") : title || t("newSession");
  const status = headerStatus(running, elapsed, t);
  return <header className="thread-header titlebar-region"><div><strong>{heading}</strong><span hidden={empty}>{status}</span></div><HeaderActions empty={empty} agentMode={agentMode} setAgentMode={setAgentMode} /></header>;
}

function headerStatus(running: boolean, elapsed: string, t: ReturnType<typeof translator>) { return running ? `${t("running")} · ${elapsed}` : t("ready"); }

function HeaderActions({ empty, agentMode, setAgentMode }: { empty: boolean; agentMode: string; setAgentMode: (value: string) => void }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const inspectorOpen = useRuntimeStore((state) => state.inspectorOpen);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  return <div className="thread-actions"><button hidden={empty} className="subtle-button" onClick={() => execute({ kind: "compact", target: currentSessionId }).catch((cause) => setError(String(cause)))}>{t("compact")}</button><button className="subtle-button" onClick={() => setAgentMode("team")}><Users size={14} />{agentMode === "team" ? t("team") : t("handoff")}</button><button className="subtle-button" onClick={() => setCommandOpen(true)}><Sparkles size={14} />{t("actions")}</button><button hidden={empty} className="icon-button inspector-toggle" data-open={String(inspectorOpen)} title={t("inspector")} onClick={() => setInspectorOpen(!inspectorOpen)}><PanelRightClose className="inspector-open-icon" size={15} /><PanelRightOpen className="inspector-closed-icon" size={15} /></button></div>;
}

function Composer({ prompt, setPrompt, submit, attach, agentMode, setAgentMode, planMode, setPlanMode, approvalMode, branches, running, cancel }: {
  prompt: string; setPrompt: (value: string) => void; submit: () => void; attach: (files: FileList | null) => void;
  agentMode: string; setAgentMode: (value: string) => void; planMode: boolean; setPlanMode: (value: boolean) => void;
  approvalMode: string; branches: Array<{ name: string; current: boolean }>; running: boolean; cancel?: () => void;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const attachments = useRuntimeStore((state) => state.attachments);
  const removeAttachment = useRuntimeStore((state) => state.removeAttachment);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const { modelChoices, reasoningLevels, selectedModelName, changeModel, changeReasoning, changeSpeed } = useComposerModels(snapshot);
  const reasoningNames: Record<string, string> = snapshot.language === "zh-CN"
    ? { minimal: "最小", low: "轻度", medium: "中", high: "高", xhigh: "极高", max: "最高", ultra: "超高" }
    : { minimal: "Minimal", low: "Low", medium: "Medium", high: "High", xhigh: "Extra high", max: "Maximum", ultra: "Ultra" };
  const selectedReasoningName = reasoningNames[snapshot.reasoning] ?? snapshot.reasoning;
  const switchBranch = async (target: string) => {
    try { await execute({ kind: "switch_git_branch", target }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const setApproval = async (target: string) => {
    try { await execute({ kind: "set_approval_mode", target }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  return (
    <div className="composer-card">
      {attachments.length > 0 && <div className="attachment-row">{attachments.map((item) => <span key={item.id}><ImagePlus size={13} />{item.name}<button aria-label={`移除 ${item.name}`} onClick={() => removeAttachment(item.id)}><X size={12} /></button></span>)}</div>}
      <textarea id="azem-composer" value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder={running ? "继续输入…" : t("promptPlaceholder")} rows={2} onKeyDown={(event) => {
        if ((event.metaKey || event.ctrlKey) && event.key === "Enter") { event.preventDefault(); submit(); }
      }} />
      <div className="composer-toolbar">
        <label className="icon-button attach-button" title={t("attach")}><Paperclip size={15} /><input type="file" accept="image/*" multiple onChange={(event) => { attach(event.target.files); event.target.value = ""; }} /></label>
        <select className="accent-select" value={approvalMode} onChange={(event) => setApproval(event.target.value)} aria-label="Approval mode">
          <option value="prompt">{t("promptApproval")}</option><option value="auto_review">{t("autoReview")}</option><option value="yolo">{t("yolo")}</option>
        </select>
        <button className={planMode ? "mode-chip active" : "mode-chip"} onClick={() => setPlanMode(!planMode)}><Circle size={11} />{t("plan")}</button>
        <span className="toolbar-spacer" />
        <select value={agentMode} onChange={(event) => setAgentMode(event.target.value)} aria-label="Agent mode"><option value="single">{t("single")}</option><option value="team">{t("team")}</option></select>
        <ModelControls
          running={running} models={modelChoices} selectedModel={modelKey(snapshot.provider, snapshot.model)} selectedModelName={selectedModelName}
          reasoningLevels={reasoningLevels} selectedReasoning={snapshot.reasoning} selectedReasoningName={selectedReasoningName}
          fast={snapshot.chatgptFastMode} reasoningNames={reasoningNames} onModelChange={changeModel} onReasoningChange={changeReasoning} onSpeedChange={changeSpeed}
          modelLabel={t("model")} reasoningLabel={t("reasoning")} speedLabel={t("speed")} standardSpeed={t("standardSpeed")} fastSpeed={t("fastSpeed")} fastHint={t("fastModeHint")}
        />
        {running && cancel ? <button className="cancel-button" data-cancel-run onClick={cancel} title={t("cancel")}><CircleStop size={16} /></button> : <button className="send-button" onClick={submit} disabled={!prompt.trim()} title={t("send")}><Send size={15} /></button>}
      </div>
      <div className="composer-meta"><span><FolderName path={snapshot.workspace} /></span><span>▰ {t("local")}</span><label><GitBranch size={12} /><select disabled={running} value={branches.find((branch) => branch.current)?.name || ""} onChange={(event) => switchBranch(event.target.value)}>{branches.map((branch) => <option key={branch.name}>{branch.name}</option>)}</select></label><span className="toolbar-spacer" />⌘↵ {t("send")}</div>
    </div>
  );
}

function ModelControls({ running, models, selectedModel, selectedModelName, reasoningLevels, selectedReasoning, selectedReasoningName, fast, reasoningNames, onModelChange, onReasoningChange, onSpeedChange, modelLabel, reasoningLabel, speedLabel, standardSpeed, fastSpeed, fastHint }: {
  running: boolean; models: ComposerModel[]; selectedModel: string; selectedModelName: string; reasoningLevels: string[]; selectedReasoning: string; selectedReasoningName: string;
  fast: boolean; reasoningNames: Record<string, string>; onModelChange: (value: string) => void; onReasoningChange: (value: string) => void; onSpeedChange: (value: string) => void;
  modelLabel: string; reasoningLabel: string; speedLabel: string; standardSpeed: string; fastSpeed: string; fastHint: string;
}) {
  return <details className="model-controls" data-disabled={String(running)}><summary aria-disabled={running}><span>{selectedModelName}</span><small>{selectedReasoningName}</small><ChevronDown size={12} /></summary><div className="model-control-menu">
    <label><span>{modelLabel}</span><select disabled={running} aria-label={modelLabel} value={selectedModel} onChange={(event) => onModelChange(event.target.value)}>{models.map((model) => <option key={modelKey(model.provider, model.id)} value={modelKey(model.provider, model.id)}>{model.name}</option>)}</select><ChevronRight size={13} /></label>
    <label><span>{reasoningLabel}</span><select disabled={running} aria-label={reasoningLabel} value={selectedReasoning} onChange={(event) => onReasoningChange(event.target.value)}>{reasoningLevels.map((level) => <option key={level} value={level}>{reasoningNames[level] ?? level}</option>)}</select><ChevronRight size={13} /></label>
    <label><span>{speedLabel}</span><select disabled={running} aria-label={speedLabel} value={fast ? "fast" : "standard"} onChange={(event) => onSpeedChange(event.target.value)}><option value="standard">{standardSpeed}</option><option value="fast">{fastSpeed}</option></select><ChevronRight size={13} /></label>
    <p>{fastHint}</p>
  </div></details>;
}

function TimelineBlock({ block, language }: { block: Block; language: Snapshot["language"] }) {
  if (block.kind === "user") return <article className="user-block">{block.attachments?.length ? <div className="user-attachments">{block.attachments.map((item) => <span key={item.id}><ImagePlus size={13} />{item.name}</span>)}</div> : null}<p>{block.content}</p></article>;
  if (block.kind === "assistant") return <article className="assistant-block markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{block.content || ""}</ReactMarkdown></article>;
  if (block.kind === "thinking") return <details className="thinking-block" open={block.state === "streaming"}><summary><Sparkles size={14} /><strong>{block.title || "思考中"}</strong>{block.state === "streaming" && <span className="live-dot" />}</summary><div className="thinking-content">{block.content}</div></details>;
  if (block.kind === "tool") return <details className="tool-block" open={block.state === "running"}><summary><span className="tool-state">{block.state === "running" ? <LoaderCircle className="spin" size={14} /> : <Check size={14} />}</span><span className="tool-chevron"><ChevronRight className="closed-chevron" size={15} /><ChevronDown className="open-chevron" size={15} /></span><strong>{block.title ? toolDisplayName(block.title, language) : translator(language)("toolGeneric")}</strong><span>{stateLabel(block.state)}</span></summary>{block.content && <pre>{block.content}</pre>}</details>;
  if (block.kind === "diff") return <DiffBlock block={block} />;
  if (block.kind === "approval") return <ApprovalBlock block={block} />;
  if (block.kind === "error") return <article className="error-block"><CircleStop size={16} /><div><strong>{block.title}</strong><p>{block.content}</p></div></article>;
  return <article className="event-block"><Bot size={14} /><div><strong>{block.title || block.kind}</strong><p>{block.content}</p></div></article>;
}

function DiffBlock({ block }: { block: Block }) {
  const lines = (block.content || "").split("\n");
  let oldLine = 0;
  let newLine = 0;
  return <article className="diff-block"><header><FileCode2 size={15} /><strong>{block.title}</strong><span className="toolbar-spacer" /><span className="plus">+{block.data?.additions || lines.filter((line) => line.startsWith("+")).length}</span><span className="minus">−{block.data?.deletions || lines.filter((line) => line.startsWith("-")).length}</span></header><pre>{lines.map((line, index) => {
    const hunk = line.startsWith("@@");
    const add = line.startsWith("+") && !line.startsWith("+++");
    const del = line.startsWith("-") && !line.startsWith("---");
    if (hunk) { const match = /@@ -(\d+)/.exec(line); oldLine = Number(match?.[1] || 0); const next = /\+(\d+)/.exec(line); newLine = Number(next?.[1] || 0); }
    else if (add) newLine += 1; else if (del) oldLine += 1; else { oldLine += 1; newLine += 1; }
    return <span key={`${index}-${line}`} className={hunk ? "hunk" : add ? "added" : del ? "deleted" : "context"}><i>{hunk ? "" : del ? oldLine : add ? newLine : newLine}</i><b>{hunk ? "" : add ? "+" : del ? "−" : " "}</b><code>{line.replace(/^[+-]/, "")}</code></span>;
  })}</pre></article>;
}

function ApprovalBlock({ block }: { block: Block }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const details = approvalPresentation(block, snapshot.language);
  const resolve = async (decision: string) => {
    try { await execute({ kind: "resolve_approval", target: block.approvalId, decision }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const pending = block.state === "pending";
  const denied = block.state === "deny" || block.state === "denied";
  const resolvedLabel = denied ? t("deny") : block.state === "session" ? t("approveSession") : t("approveOnce");
  return <article className={`approval-block ${pending ? "pending" : "resolved"}`} data-risk={details.riskTone}>
    <header className="approval-heading"><span className="approval-icon"><ShieldCheck size={17} /></span><div><small>{t("approvalTitle")}</small><strong>{details.tool}</strong></div><span className="approval-risk">{details.riskLabel}</span></header>
    <div className="approval-target"><span>{t("approvalTarget")}</span><code>{details.target}</code></div>
    <footer className="approval-footer"><p>{details.description}</p><div className="approval-actions">{pending ? <><button onClick={() => resolve("deny")}>{t("deny")}</button><button onClick={() => resolve("once")}>{t("approveOnce")}</button><button className="primary" onClick={() => resolve("session")}>{t("approveSession")}</button></> : <span className={denied ? "denied" : "approved"}>{denied ? <X size={14} /> : <Check size={14} />}{resolvedLabel}</span>}</div></footer>
  </article>;
}

export function approvalPresentation(block: Block, language: Snapshot["language"]) {
  const t = translator(language);
  const riskTone = block.data?.risk === "low" || block.data?.risk === "high" ? block.data.risk : "medium";
  const riskLabel = riskTone === "low" ? t("riskLow") : riskTone === "high" ? t("riskHigh") : t("riskMedium");
  const effect = block.data?.effect;
  const description = effect === "write" ? t("approvalWrite") : effect === "external_side_effect" ? t("approvalExternal") : effect === "read_only" ? t("approvalReadOnly") : t("approvalConfirm");
  return {
    tool: block.data?.tool ? toolDisplayName(block.data.tool, language) : t("approvalOperation"),
    target: block.data?.target?.trim() || t("approvalWorkspace"),
    riskTone,
    riskLabel,
    description,
  };
}

function useElapsed(start: number, running: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [running]);
  return formatDuration(start ? Math.max(0, now - start) : 0);
}

export function formatDuration(milliseconds: number) {
  const seconds = Math.floor(milliseconds / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  return hours ? `${hours}h${String(minutes).padStart(2, "0")}m${String(rest).padStart(2, "0")}s` : minutes ? `${minutes}m${String(rest).padStart(2, "0")}s` : `${rest}s`;
}

function activityLabel(activity: string, language: "en" | "zh-CN") {
  const labels: Record<string, [string, string]> = {
    waiting_model: ["正在等待模型", "Waiting for model"], thinking: ["思考中", "Thinking"],
    responding: ["正在接收回复", "Receiving response"], tool: ["正在调用工具", "Running tool"], approval: ["等待审批", "Waiting for approval"],
  };
  return (labels[activity] || ["运行中", "Running"])[language === "en" ? 1 : 0];
}

function stateLabel(state?: string) {
  if (state === "running" || state === "started") return "运行中";
  if (state === "failed") return "失败";
  return "完成";
}

function FolderName({ path }: { path: string }) {
  return <>{path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace"}</>;
}

type ComposerModel = { provider: string; id: string; name: string; reasoningLevels: string[]; defaultReasoning?: string };

function useComposerModels(snapshot: Snapshot) {
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const setSessionModel = useRuntimeStore((state) => state.setSessionModel);
  const setChatGPTFastMode = useRuntimeStore((state) => state.setChatGPTFastMode);
  const setError = useRuntimeStore((state) => state.setError);
  const fallbackModel: ComposerModel = { provider: snapshot.provider, id: snapshot.model, name: snapshot.model, reasoningLevels: [snapshot.reasoning].filter(Boolean) };
  const catalogModels = Object.entries(modelsByProvider).flatMap(([provider, models]) => models.map((model) => ({ ...model, provider })));
  const modelChoices = [...new Map([fallbackModel, ...catalogModels].map((model) => [modelKey(model.provider, model.id), model])).values()];
  const providerModels = modelsByProvider[snapshot.provider] ?? [];
  const catalogLevels = providerModels.find((model) => model.id === snapshot.model)?.reasoningLevels ?? ["minimal", "low", "medium", "high", "xhigh"];
  const reasoningLevels = [...new Set([snapshot.reasoning, ...catalogLevels])].filter(Boolean);
  const selectedModelName = modelChoices.find((model) => modelKey(model.provider, model.id) === modelKey(snapshot.provider, snapshot.model))?.name ?? snapshot.model;
  const changeModel = (value: string) => {
    const choice = modelChoices.find((model) => modelKey(model.provider, model.id) === value)!;
    const reasoning = [choice.defaultReasoning, ...choice.reasoningLevels, snapshot.reasoning].filter(Boolean)[0]!;
    setSessionModel(choice.provider, choice.id, reasoning);
  };
  const changeReasoning = (reasoning: string) => setSessionModel(snapshot.provider, snapshot.model, reasoning);
  const changeSpeed = (speed: string) => {
    const enabled = speed === "fast";
    const previous = snapshot.chatgptFastMode;
    setChatGPTFastMode(enabled);
    execute({ kind: "set_chatgpt_fast_mode", target: String(enabled) }).catch((cause) => {
      setChatGPTFastMode(previous);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };
  return { modelChoices, reasoningLevels, selectedModelName, changeModel, changeReasoning, changeSpeed };
}

function modelKey(provider: string, model: string) { return `${provider}/${model}`; }
