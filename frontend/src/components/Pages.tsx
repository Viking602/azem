import {
  AlertTriangle, Bot, Box, CheckCircle2, FileClock, FolderGit2,
  RefreshCw, RotateCcw, ShieldAlert, Sparkles, Wrench,
} from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { View } from "../types";
import PullRequestsPage from "./PullRequestsPage";
import SubagentsPage from "./SubagentsPage";

export default function Pages({ view }: { view: Exclude<View, "thread"> }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setView = useRuntimeStore((state) => state.setView);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const action = async (kind: string, target = "", decision = "") => {
    try { await execute({ kind, target, decision, sessionId: useRuntimeStore.getState().currentSessionId }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  if (view === "pullRequests") return <PullRequestsPage />;
  if (view === "agents") return <SubagentsPage />;
  if (view === "extensions") return <ExtensionsPage action={action} />;
  if (view === "recovery") return <RecoveryPage action={action} />;
  if (view === "projects") return <ProjectsPage open={() => setView("thread")} />;
  return <RunsPage open={() => setView("thread")} title={t("runs")} />;
}

function PageFrame({ eyebrow, title, description, action, children }: { eyebrow: string; title: string; description: string; action?: React.ReactNode; children: React.ReactNode }) {
  return <section className="secondary-page"><header><div><span>{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>{action}</header><div className="page-content">{children}</div></section>;
}


function ExtensionsPage({ action }: { action: (kind: string) => Promise<void> }) {
  const skills = useRuntimeStore((state) => state.skills);
  return <PageFrame eyebrow="Capabilities" title="设置与扩展" description="Skills 由 Go 运行时发现和校验；前端只呈现目录状态。" action={<button className="subtle-button" onClick={() => action("reload_skills")}><RefreshCw size={14} />重新加载</button>}>
    <div className="extension-summary"><Sparkles size={20} /><div><strong>{skills.filter((skill) => !skill.disabled).length} 个 Skills 可用</strong><p>启用的 Skill 会根据任务描述按需加载。</p></div></div>
    <div className="catalog-list">{skills.map((skill) => <div key={skill.name} className={skill.disabled ? "disabled" : ""}><Box size={15} /><span><strong>{skill.name}</strong><small>{skill.description || skill.sourcePath}</small></span><em>{skill.eager ? "默认加载" : "按需"} · {skill.resourceCount} resources</em></div>)}</div>
  </PageFrame>;
}

function RecoveryPage({ action }: { action: (kind: string, target?: string, decision?: string) => Promise<void> }) {
  const recovery = useRuntimeStore((state) => state.recovery);
  return <PageFrame eyebrow="Safety" title="恢复中心" description="中断运行、未知副作用和恢复审批必须逐项确认。">
    {recovery.length === 0 ? <div className="quiet-empty"><CheckCircle2 size={28} /><h2>无需恢复</h2><p>没有中断运行或状态未知的工具操作。</p></div> : <div className="recovery-list">{recovery.map((item, index) => {
      const id = stringField(item, "id", "ID") || `recovery-${index}`;
      const kind = stringField(item, "kind", "Kind");
      return <article key={id}><ShieldAlert size={18} /><div><span>{kind || "run"}</span><h3>{stringField(item, "title", "Title") || "需要确认的运行"}</h3><p>{stringField(item, "detail", "Detail") || "检查外部操作结果后再继续。"}</p><small>{stringField(item, "toolName", "ToolName")}</small></div><div>{kind === "reconcile" ? <><button onClick={() => action("reconcile_attempt", id, "failed")}>标记失败</button><button className="primary" onClick={() => action("reconcile_attempt", id, "completed")}>确认完成</button></> : <button className="primary" onClick={() => action("resolve_approval", id, "once")}>继续</button>}</div></article>;
    })}</div>}
  </PageFrame>;
}

function ProjectsPage({ open }: { open: () => void }) {
  const allSessions = useRuntimeStore((state) => state.sessions);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = allSessions.filter((session) => session.workspace === snapshot.workspace || !session.workspace);
  return <PageFrame eyebrow="Workspace" title={basename(snapshot.workspace)} description={snapshot.workspace}>
    <div className="project-overview"><FolderGit2 size={26} /><div><strong>{sessions.length} 个会话</strong><p>所有会话共享当前工作区的运行时、Skills 与恢复状态。</p></div></div>
    <div className="catalog-list">{sessions.map((session) => <button key={session.id} onClick={async () => { await execute({ kind: "resume_session", target: session.id }); open(); }}><FileClock size={15} /><span><strong>{session.title || "新会话"}</strong><small>{session.modelId} · {session.reasoning}</small></span><em>{dateLabel(session.updatedAt)}</em></button>)}</div>
  </PageFrame>;
}

function RunsPage({ open, title }: { open: () => void; title: string }) {
  const blocks = useRuntimeStore((state) => state.blocks);
  const running = useRuntimeStore((state) => state.running);
  const runId = useRuntimeStore((state) => state.runId);
  const toolCount = blocks.filter((block) => block.kind === "tool").length;
  return <PageFrame eyebrow="Timeline" title={title} description="当前会话的运行状态和审计摘要。">
    <div className="stat-grid"><Stat icon={running ? RotateCcw : CheckCircle2} label="状态" value={running ? "运行中" : "空闲"} /><Stat icon={Wrench} label="工具调用" value={toolCount} /><Stat icon={AlertTriangle} label="待审批" value={blocks.filter((block) => block.kind === "approval" && block.state === "pending").length} /></div>
    <button className="run-card" onClick={open}><StateBadge state={running ? "running" : "completed"} /><div><strong>{runId || "最近一次运行"}</strong><p>{blocks.length} 个时间线块 · {toolCount} 次工具调用</p></div><span>查看会话 →</span></button>
  </PageFrame>;
}

function Stat({ icon: Icon, label, value }: { icon: typeof Bot; label: string; value: React.ReactNode }) { return <div className="stat-card"><Icon size={17} /><span>{label}</span><strong>{value}</strong></div>; }
function StateBadge({ state }: { state: string }) { return <span className={`state-badge ${state}`}><i />{state === "running" ? "运行中" : state === "completed" ? "已完成" : state || "等待"}</span>; }
function stringField(item: Record<string, unknown>, ...keys: string[]) { for (const key of keys) if (item[key] != null) return String(item[key]); return ""; }
function basename(path: string) { return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace"; }
function dateLabel(value: string) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? "" : date.toLocaleDateString(); }
