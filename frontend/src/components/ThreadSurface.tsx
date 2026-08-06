import { useCallback, useEffect, useRef, useState, type ComponentType } from "react";
import { createPortal } from "react-dom";
import {
  ArrowDown, ArrowUp, Bot, Box, Brain, Check, ChevronDown, ChevronLeft, ChevronRight, CircleDot, CircleStop,
  CornerUpRight, Folder, GitBranch, GripVertical, Hand, HardDrive, ImagePlus, Lightbulb, ListX, Minimize2,
  MoreHorizontal, Pencil, Plug, Plus, RefreshCw, RotateCcw, Search, Send, Settings, ShieldAlert, ShieldCheck,
  Sparkles, Trash2, WandSparkles, Zap, PanelRightClose, PanelRightOpen, X,
} from "lucide-react";
import { cancelActive, execute, guide, importAttachment, importClipboardImage, openProject, selectProjectFolder, startTurn } from "../bridge";
import { reasoningHint, sortReasoningLevels, tFormat, translator } from "../i18n";
import { useRuntimeStore, type ContextUsage } from "../store";
import type { Attachment, Block, ContextProfile, DeliveryMode, QueuedPrompt, SkillEntry, Snapshot } from "../types";
import ReasoningEffortSlider, { isHighCostReasoning } from "./ReasoningEffortSlider";
import { TimelineFeed } from "./Timeline";
import { formatDuration } from "./toolTimeline";
export { approvalPresentation } from "./Timeline";
export { formatDuration } from "./toolTimeline";

type SlashAction =
  | "new" | "compact" | "settings" | "skills" | "plan" | "fast"
  | "reasoning" | "approval" | "agents" | "mcp" | "reload-skills" | "inspector";
type SlashIcon = ComponentType<{ size?: number; className?: string }>;
type SlashSuggestion = {
  value: string;
  label: string;
  detail: string;
  kind: "command" | "skill";
  action?: SlashAction;
  skill?: string;
  badge?: string;
  icon: SlashIcon;
};
type SlashContext = {
  reasoningLabel?: string;
  approvalLabel?: string;
  planMode?: boolean;
  agentMode?: string;
  fast?: boolean;
  contextPercent?: number;
  provider?: string;
};

export function shouldReadNativeClipboard(clipboardData: DataTransfer): boolean {
  return pastedImages(clipboardData).length === 0 && !clipboardData.types.includes("text/plain");
}

const slashCommands: Array<{
  action: SlashAction;
  aliases: string[];
  zh: string;
  en: string;
  zhDetail: string;
  enDetail: string;
  icon: SlashIcon;
  when?: (context: SlashContext) => boolean;
  detail?: (context: SlashContext, language: Snapshot["language"]) => string;
}> = [
  {
    action: "mcp", aliases: ["mcp", "plugins"], zh: "MCP", en: "MCP",
    zhDetail: "刷新 MCP 服务器状态", enDetail: "Refresh MCP server status", icon: Plug,
  },
  {
    action: "compact", aliases: ["compact", "compress", "压缩"], zh: "压缩", en: "Compact",
    zhDetail: "压缩当前会话的上下文", enDetail: "Compact the current conversation context", icon: Minimize2,
    detail: (context, language) => context.contextPercent != null && context.contextPercent > 0
      ? (language === "zh-CN" ? `压缩此聊天的上下文（已使用 ${context.contextPercent}%）` : `Compact this chat context (${context.contextPercent}% used)`)
      : (language === "zh-CN" ? "压缩当前会话的上下文" : "Compact the current conversation context"),
  },
  {
    action: "settings", aliases: ["settings", "config", "设置"], zh: "设置", en: "Settings",
    zhDetail: "打开 Azem 设置", enDetail: "Open Azem settings", icon: Settings,
  },
  {
    action: "skills", aliases: ["skills", "extensions", "技能", "扩展"], zh: "技能", en: "Skills",
    zhDetail: "查看可用技能目录", enDetail: "Browse available skills", icon: Sparkles,
  },
  {
    action: "reload-skills", aliases: ["reload", "reload-skills", "重新加载"], zh: "重新加载技能", en: "Reload skills",
    zhDetail: "重新扫描本地技能目录", enDetail: "Rescan local skill directories", icon: RefreshCw,
  },
  {
    action: "plan", aliases: ["plan", "计划"], zh: "计划模式", en: "Plan mode",
    zhDetail: "切换只规划不实施的模式", enDetail: "Toggle planning without implementation", icon: CircleDot,
    detail: (context, language) => language === "zh-CN"
      ? (context.planMode ? "关闭计划模式" : "开启只规划不实施的模式")
      : (context.planMode ? "Turn off plan mode" : "Plan without implementing"),
  },
  // Team mode UI is intentionally unavailable until multi-agent is designed.
  {
    action: "agents", aliases: ["subagents", "子智能体", "智能体", "代理"], zh: "子智能体", en: "Subagents",
    zhDetail: "查看当前子智能体任务", enDetail: "Inspect current subagent tasks", icon: Bot,
  },
  {
    action: "approval", aliases: ["approval", "approve", "审批", "yolo"], zh: "审批模式", en: "Approval",
    zhDetail: "切换默认审批策略", enDetail: "Cycle the default approval policy", icon: ShieldCheck,
    detail: (context, language) => language === "zh-CN"
      ? `当前：${context.approvalLabel || "逐次审批"} · 点击切换`
      : `Current: ${context.approvalLabel || "Ask first"} · click to cycle`,
  },
  {
    action: "fast", aliases: ["fast", "speed", "快速"], zh: "快速", en: "Fast",
    zhDetail: "切换 ChatGPT 标准与快速速度", enDetail: "Toggle standard and fast ChatGPT speed", icon: Zap,
    when: (context) => context.provider === "chatgpt",
    detail: (context, language) => language === "zh-CN"
      ? (context.fast ? "当前快速模式 · 点击切回标准" : "1.5x 速度，用量更高")
      : (context.fast ? "Fast mode on · click for standard" : "1.5x speed, increased usage"),
  },
  {
    action: "reasoning", aliases: ["reasoning", "reason", "推理"], zh: "推理", en: "Reasoning",
    zhDetail: "循环切换推理强度", enDetail: "Cycle reasoning effort", icon: Brain,
    detail: (context, language) => language === "zh-CN"
      ? (context.reasoningLabel || "中")
      : (context.reasoningLabel || "Medium"),
  },
  {
    action: "inspector", aliases: ["inspector", "context", "环境"], zh: "环境信息", en: "Inspector",
    zhDetail: "打开右侧环境与变更面板", enDetail: "Open the environment and changes panel", icon: PanelRightOpen,
  },
  {
    action: "new", aliases: ["new", "chat", "新聊天", "新建"], zh: "新聊天", en: "New chat",
    zhDetail: "在同一工作区开启空白聊天", enDetail: "Start a blank chat in this workspace", icon: Plus,
  },
];

export function slashSuggestions(input: string, skills: SkillEntry[], language: Snapshot["language"], context: SlashContext = {}): SlashSuggestion[] {
  if (!input.startsWith("/") || /\s/.test(input.slice(1))) return [];
  const query = input.slice(1).toLowerCase();
  const commands = slashCommands
    .filter((item) => (item.when ? item.when(context) : true))
    .filter((item) => [item.action, ...item.aliases, item.zh, item.en].some((candidate) => slashMatch(candidate, query)))
    .map((item) => ({
      value: `/${item.action}`,
      label: language === "zh-CN" ? item.zh : item.en,
      detail: item.detail?.(context, language) ?? (language === "zh-CN" ? item.zhDetail : item.enDetail),
      kind: "command" as const,
      action: item.action,
      icon: item.icon,
    }));
  const skillItems = skills
    .filter((skill) => !skill.disabled)
    .filter((skill) => matchSkill(skill, query))
    .map((skill) => ({
      value: `/skill:${skill.name}`,
      label: skillTitle(skill.name),
      detail: skill.description || translator(language)("skillUseHint"),
      kind: "skill" as const,
      skill: skill.name,
      badge: skill.bundled ? translator(language)("skillBuiltin") : translator(language)("skillPersonal"),
      icon: Box,
    }));
  return [...commands, ...skillItems];
}

function matchSkill(skill: SkillEntry, query: string) {
  if (!query) return true;
  return [
    skill.name,
    skillTitle(skill.name),
    skill.description,
    `skill:${skill.name}`,
    skill.name.replace(/[-_]/g, " "),
  ].some((candidate) => slashMatch(candidate, query));
}

export function skillTitle(name: string) {
  return name
    .split(/[-_]/g)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function slashMatch(candidate: string, query: string) {
  if (!query) return true;
  const value = candidate.toLowerCase();
  if (value.includes(query)) return true;
  return value.split(/[-_\s:/]+/).some((part) => part.startsWith(query));
}

export function parseSkillPrompt(input: string, language: Snapshot["language"]) {
  const match = /^\/skill:([^\s]+)(?:\s+([\s\S]*))?$/.exec(input.trim());
  if (!match) return null;
  const name = match[1];
  const instruction = match[2]?.trim() || (language === "zh-CN" ? `使用“${name}”技能处理当前工作区并报告结果。` : `Apply the "${name}" skill to the current workspace and report the result.`);
  return { name, instruction };
}

const approvalCycle = ["prompt", "auto_review", "yolo"] as const;

export default function ThreadSurface() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const blocks = useRuntimeStore((state) => state.blocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const running = useRuntimeStore((state) => state.running);
  const runId = useRuntimeStore((state) => state.runId);
  const globalRunSessionId = useRuntimeStore((state) => state.globalRunSessionId);
  const runStartedAt = useRuntimeStore((state) => state.runStartedAt);
  const activity = useRuntimeStore((state) => state.activity);
  const error = useRuntimeStore((state) => state.error);
  const planMode = useRuntimeStore((state) => state.planMode);
  const attachments = useRuntimeStore((state) => state.attachments);
  const allQueuedPrompts = useRuntimeStore((state) => state.queuedPrompts);
  const queuedPrompts = allQueuedPrompts.filter((item) => item.sessionId === currentSessionId);
  const queuePauseReason = useRuntimeStore((state) => state.queuePauseReasons[currentSessionId]);
  const addOptimisticUser = useRuntimeStore((state) => state.addOptimisticUser);
  const clearAttachments = useRuntimeStore((state) => state.clearAttachments);
  const replaceAttachments = useRuntimeStore((state) => state.replaceAttachments);
  const enqueuePrompt = useRuntimeStore((state) => state.enqueuePrompt);
  const removeQueuedPrompt = useRuntimeStore((state) => state.removeQueuedPrompt);
  const updateQueuedPrompt = useRuntimeStore((state) => state.updateQueuedPrompt);
  const failQueuedPrompt = useRuntimeStore((state) => state.failQueuedPrompt);
  const retryQueuedPrompt = useRuntimeStore((state) => state.retryQueuedPrompt);
  const reorderQueuedPrompt = useRuntimeStore((state) => state.reorderQueuedPrompt);
  const resumeQueuedPrompts = useRuntimeStore((state) => state.resumeQueuedPrompts);
  const setQueueMode = useRuntimeStore((state) => state.setQueueMode);
  const setError = useRuntimeStore((state) => state.setError);
  const setPlanMode = useRuntimeStore((state) => state.setPlanMode);
  const [prompt, setPrompt] = useState("");
  const [editingQueuedId, setEditingQueuedId] = useState<string | null>(null);
  const [deliveryMode, setDeliveryMode] = useState<DeliveryMode>(snapshot.queueMode ?? "queue");
  // Team mode is not productized yet — keep the composer on single-agent only.
  const agentMode = "single";
  const setAgentMode = (_value: string) => undefined;
  const [following, setFollowing] = useState(true);
  const viewport = useRef<HTMLDivElement>(null);
  const t = translator(snapshot.language);
  const empty = blocks.length === 0 && !running;
  const elapsed = useElapsed(runStartedAt, running);
  const runtimeBusy = running || Boolean(globalRunSessionId);

  useEffect(() => {
    if (following) viewport.current?.scrollTo({ top: viewport.current.scrollHeight, behavior: running ? "auto" : "smooth" });
  }, [blocks, following, running]);

  useEffect(() => setDeliveryMode(snapshot.queueMode ?? "queue"), [currentSessionId, snapshot.queueMode]);
  useEffect(() => {
    setPrompt("");
    clearAttachments();
    setEditingQueuedId(null);
  }, [clearAttachments, currentSessionId]);
  const beginTurn = useTurnStarter(agentMode, planMode, setFollowing);
  useQueuedTurnRunner(
    queuedPrompts, runtimeBusy, queuePauseReason, editingQueuedId, beginTurn, removeQueuedPrompt, failQueuedPrompt,
  );

  const resetComposer = () => {
    setPrompt("");
    clearAttachments();
    setEditingQueuedId(null);
  };

  const changeDeliveryMode = (mode: DeliveryMode) => {
    const previous = snapshot.queueMode;
    setDeliveryMode(mode);
    setQueueMode(mode);
    void execute({ kind: "set_queue_mode", target: mode, sessionId: currentSessionId }).catch((cause) => {
      setDeliveryMode(previous);
      setQueueMode(previous);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };

  const submit = async (modeOverride?: DeliveryMode) => {
    const text = prompt.trim();
    const images = [...attachments];
    if (!text && images.length === 0) return;
    if (editingQueuedId) {
      updateQueuedPrompt(currentSessionId, editingQueuedId, text, images);
      resetComposer();
      setFollowing(true);
      return;
    }
    // The Go runtime admits one main run process-wide. A prompt composed in another
    // session while that run is active must remain queued until its terminal event.
    if (!runtimeBusy) {
      resetComposer();
      return void beginTurn(text, images);
    }
    // Starting (the bridge has not returned runId yet), another session is active,
    // or Queue is selected: never attempt a concurrent turn.
    if (!running || !runId || (modeOverride ?? deliveryMode) === "queue") {
      resetComposer();
      enqueuePrompt(text, images);
      setFollowing(true);
      return;
    }
    const sessionId = currentSessionId;
    resetComposer();
    await sendGuidance(sessionId, runId, text, images, () => {
      if (!isCurrentSession(sessionId)) return;
      addOptimisticUser(text, images);
      setFollowing(true);
    }, (message) => {
      if (!isCurrentSession(sessionId)) return;
      setError(message);
      setPrompt((current) => current || text);
      if (useRuntimeStore.getState().attachments.length === 0) replaceAttachments(images);
    });
  };

  const editQueued = (item: QueuedPrompt) => {
    if (item.sessionId !== currentSessionId) return;
    setEditingQueuedId(item.id);
    setPrompt(item.text);
    replaceAttachments(item.attachments);
    requestAnimationFrame(() => document.querySelector<HTMLTextAreaElement>("#azem-composer")?.focus());
  };

  const deleteQueued = (item: QueuedPrompt) => {
    if (editingQueuedId === item.id) resetComposer();
    removeQueuedPrompt(item.sessionId, item.id);
  };

  const guideQueued = async (item: QueuedPrompt) => {
    if (!running || !runId || item.sessionId !== currentSessionId) return;
    const sessionId = item.sessionId;
    await sendGuidance(sessionId, runId, item.text, item.attachments, () => {
      removeQueuedPrompt(sessionId, item.id);
      if (isCurrentSession(sessionId)) addOptimisticUser(item.text, item.attachments);
    }, (message) => {
      if (isCurrentSession(sessionId)) setError(message);
    });
  };

  const attach = async (files: Iterable<File> | null) => {
    if (!files) return;
    const sessionId = currentSessionId;
    const timestamp = new Date();
    for (const [index, file] of Array.from(files).entries()) {
      if (!file.type.startsWith("image/")) continue;
      try {
        const image = file.name ? file : namedClipboardImage(file, timestamp, index);
        const imported = await importAttachment(sessionId, image);
        if (isCurrentSession(sessionId)) useRuntimeStore.getState().addAttachment(imported);
      } catch (cause) {
        if (isCurrentSession(sessionId)) setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
  };

  const attachClipboard = async (files: File[]) => {
    if (files.length > 0) {
      await attach(files);
      return;
    }
    const sessionId = currentSessionId;
    try {
      const image = await importClipboardImage(sessionId);
      if (image && isCurrentSession(sessionId)) useRuntimeStore.getState().addAttachment(image);
    } catch (cause) {
      if (isCurrentSession(sessionId)) setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const cancel = async () => {
    try {
      await cancelActive(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const queue = queuedPrompts.length > 0 ? <QueuedPrompts
    items={queuedPrompts} running={running} pauseReason={queuePauseReason} editingId={editingQueuedId}
    deliveryMode={deliveryMode} onGuide={guideQueued}
    onRetry={(id) => retryQueuedPrompt(currentSessionId, id)}
    onDelete={deleteQueued} onEdit={editQueued}
    onReorder={(id, beforeId) => reorderQueuedPrompt(currentSessionId, id, beforeId)}
    onResume={() => resumeQueuedPrompts(currentSessionId)}
    onToggleQueue={() => changeDeliveryMode(deliveryMode === "queue" ? "guide" : "queue")}
  /> : null;

  return (
    <section className={`thread-surface ${empty ? "empty-thread" : "active-thread"}`}>
      <ThreadHeader empty={empty} elapsed={elapsed} />
      {empty ? (
        <div className="empty-composer-wrap">
          <svg className="azem-symbol" viewBox="0 0 44 44" aria-hidden="true" focusable="false">
            <path className="azem-symbol-sun" d="M7 31.5C3.7 20.1 8.9 8.2 20 4.6c8.8-2.8 17.2 2.8 19.1 11.3" />
            <circle className="azem-symbol-sun-dot" cx="39.1" cy="15.9" r="2.8" />
            <path className="azem-symbol-road" d="M5.5 37.6c7.2-6.8 13.9-10.6 24.2-12.8" />
            <path className="azem-symbol-road azem-symbol-road-accent" d="M12 40c7.3-6.7 14.2-10.6 24.5-12.8" />
          </svg>
          <h1>{t("promptTitle")}</h1>
          <div className="composer-stack">
            {queue}
            <Composer
              prompt={prompt} setPrompt={setPrompt} submit={submit} attach={attach} attachClipboard={attachClipboard}
              agentMode={agentMode} setAgentMode={setAgentMode} planMode={planMode} setPlanMode={setPlanMode}
              running={running}
              busy={runtimeBusy}
              deliveryMode={deliveryMode}
              showContextBar
            />
          </div>
        </div>
      ) : (
        <>
          <div className="transcript-viewport" ref={viewport} onScroll={(event) => {
            const node = event.currentTarget;
            setFollowing(node.scrollHeight - node.scrollTop - node.clientHeight < 72);
          }}>
            <div className="transcript">
              <TimelineFeed
                blocks={blocks}
                language={snapshot.language}
                activeRunId={runId}
                running={running}
                waitingForModel={running && (activity === "waiting_model" || activity === "thinking")}
              />
              {error && <div className="inline-error" role="alert">{error}</div>}
            </div>
          </div>
          {!following && <button className="jump-latest" aria-label={t("jumpLatest")} title={t("jumpLatest")} onClick={() => setFollowing(true)}><ArrowDown size={16} /></button>}
          <div className="composer-dock">
            <div className="composer-stack">
              {queue}
              <Composer
                prompt={prompt} setPrompt={setPrompt} submit={submit} attach={attach} attachClipboard={attachClipboard}
                agentMode={agentMode} setAgentMode={setAgentMode} planMode={planMode} setPlanMode={setPlanMode}
                running={running} cancel={cancel}
                busy={runtimeBusy}
                deliveryMode={deliveryMode}
              />
            </div>
          </div>
        </>
      )}
    </section>
  );
}

function isCurrentSession(sessionId: string): boolean {
  const state = useRuntimeStore.getState();
  return (state.currentSessionId || state.snapshot?.sessionId || "") === sessionId;
}

async function sendGuidance(
  sessionId: string,
  runId: string,
  text: string,
  attachments: Attachment[],
  onSuccess: () => void,
  onError: (message: string) => void,
) {
  try {
    await guide(sessionId, runId, text, attachments);
    onSuccess();
  } catch (cause) {
    onError(cause instanceof Error ? cause.message : String(cause));
  }
}

function useTurnStarter(agentMode: string, planMode: boolean, setFollowing: (following: boolean) => void) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const skills = useRuntimeStore((state) => state.skills);
  const addOptimisticUser = useRuntimeStore((state) => state.addOptimisticUser);
  const setRunId = useRuntimeStore((state) => state.setRunId);
  const failRun = useRuntimeStore((state) => state.failRun);
  return useCallback(async (text: string, images: Attachment[]): Promise<string | null> => {
    const invocation = parseSkillPrompt(text, snapshot.language);
    const invokedSkill = invocation ? skills.find((skill) => !skill.disabled && skill.name.toLowerCase() === invocation.name.toLowerCase())?.name ?? "" : "";
    const prompt = invokedSkill ? invocation!.instruction : text;
    const activeSkills = [...new Set([...skills.filter((skill) => skill.eager && !skill.disabled).map((skill) => skill.name), ...(invokedSkill ? [invokedSkill] : [])])];
    addOptimisticUser(prompt, images);
    try {
      const nextRun = await startTurn({
        sessionId: currentSessionId, prompt, provider: snapshot.provider,
        model: snapshot.model, reasoning: snapshot.reasoning, agentMode,
        planMode, disableSubagents: false,
        activeSkills,
        images,
      });
      if (isCurrentSession(currentSessionId)) {
        setRunId(nextRun);
        setFollowing(true);
      }
      return null;
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      if (isCurrentSession(currentSessionId)) failRun(message);
      return message;
    }
  }, [addOptimisticUser, agentMode, currentSessionId, failRun, planMode, setFollowing, setRunId, skills, snapshot.language, snapshot.model, snapshot.provider, snapshot.reasoning]);
}

function useQueuedTurnRunner(
  queuedPrompts: QueuedPrompt[],
  busy: boolean,
  pauseReason: "interrupted" | undefined,
  editingQueuedId: string | null,
  beginTurn: (text: string, images: Attachment[]) => Promise<string | null>,
  removeQueuedPrompt: (sessionId: string, id: string) => void,
  failQueuedPrompt: (sessionId: string, id: string, message: string) => void,
) {
  const startingQueued = useRef("");
  useEffect(() => {
    const next = queuedPrompts[0];
    if (!next || busy || pauseReason || startingQueued.current || next.state === "failed" || next.id === editingQueuedId) return;
    startingQueued.current = next.id;
    void beginTurn(next.text, next.attachments)
      .then((error) => {
        if (!error) {
          removeQueuedPrompt(next.sessionId, next.id);
          return;
        }
        failQueuedPrompt(next.sessionId, next.id, error);
      })
      .finally(() => { startingQueued.current = ""; });
  }, [beginTurn, busy, editingQueuedId, failQueuedPrompt, pauseReason, queuedPrompts, removeQueuedPrompt]);
}

function ThreadHeader({ empty, elapsed }: { empty: boolean; elapsed: string }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const title = useRuntimeStore((state) => state.currentTitle);
  const running = useRuntimeStore((state) => state.running);
  const t = translator(snapshot.language);
  const heading = empty ? t("newSession") : title || t("newSession");
  const status = headerStatus(running, elapsed, t);
  return <header className="thread-header titlebar-region"><div><strong>{heading}</strong><span hidden={empty}>{status}</span></div><HeaderActions empty={empty} /></header>;
}

function headerStatus(running: boolean, elapsed: string, t: ReturnType<typeof translator>) { return running ? `${t("running")} · ${elapsed}` : t("ready"); }

function HeaderActions({ empty }: { empty: boolean }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const inspectorOpen = useRuntimeStore((state) => state.inspectorOpen);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const t = translator(snapshot.language);
  return <div className="thread-actions"><button hidden={empty} className="icon-button inspector-toggle" data-open={String(inspectorOpen)} title={t("inspector")} onClick={() => setInspectorOpen(!inspectorOpen)}><PanelRightClose className="inspector-open-icon" size={15} /><PanelRightOpen className="inspector-closed-icon" size={15} /></button></div>;
}

function Composer({ prompt, setPrompt, submit, attach, attachClipboard, agentMode, setAgentMode, planMode, setPlanMode, running, busy, cancel, deliveryMode, showContextBar = false }: {
  prompt: string; setPrompt: (value: string) => void; submit: (modeOverride?: DeliveryMode) => void;
  attach: (files: Iterable<File> | null) => void; attachClipboard: (files: File[]) => void;
  agentMode: string; setAgentMode: (value: string) => void; planMode: boolean; setPlanMode: (value: boolean) => void;
  running: boolean; busy: boolean; cancel?: () => void;
  deliveryMode: DeliveryMode;
  /** Project / local / branch chips — empty welcome only; hide during active threads. */
  showContextBar?: boolean;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const attachments = useRuntimeStore((state) => state.attachments);
  const skills = useRuntimeStore((state) => state.skills);
  const approvalMode = useRuntimeStore((state) => state.approvalMode) || snapshot.approvalMode;
  const contextUsage = useRuntimeStore((state) => state.contextUsage);
  const contextProfile = useRuntimeStore((state) => state.contextProfile);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const removeAttachment = useRuntimeStore((state) => state.removeAttachment);
  const setError = useRuntimeStore((state) => state.setError);
  const setView = useRuntimeStore((state) => state.setView);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const slashMenu = useRef<HTMLDivElement>(null);
  const [slashCursor, setSlashCursor] = useState(0);
  const [slashDismissed, setSlashDismissed] = useState(false);
  const t = translator(snapshot.language);
  const { modelChoices, reasoningLevels, selectedModelName, changeModel, changeReasoning, changeSpeed } = useComposerModels(snapshot);
  const reasoningNames: Record<string, string> = {
    minimal: t("reasoningMinimal"), low: t("reasoningLow"), medium: t("reasoningMedium"),
    high: t("reasoningHigh"), xhigh: t("reasoningXHigh"), max: t("reasoningMax"), ultra: t("reasoningUltra"),
  };
  const approvalLabels: Record<string, string> = {
    prompt: t("promptApproval"), auto_review: t("autoReview"), yolo: t("yolo"),
  };
  const selectedReasoningName = reasoningNames[snapshot.reasoning] ?? snapshot.reasoning;
  const contextPercent = contextOccupancy(contextUsage, contextProfile).percentage;
  const skillInvocation = parseSkillPrompt(prompt, snapshot.language);
  const selectedSkill = skillInvocation
    ? skills.find((skill) => !skill.disabled && skill.name.toLowerCase() === skillInvocation.name.toLowerCase())
    : undefined;
  const skillPrefix = selectedSkill ? `/skill:${selectedSkill.name} ` : "";
  const visiblePrompt = selectedSkill ? prompt.replace(/^\/skill:[^\s]+(?:\s+)?/, "") : prompt;
  const slashItems = slashSuggestions(prompt, skills, snapshot.language, {
    reasoningLabel: selectedReasoningName,
    approvalLabel: approvalLabels[approvalMode] ?? approvalMode,
    planMode,
    agentMode,
    fast: snapshot.chatgptFastMode,
    contextPercent,
    provider: snapshot.provider,
  });
  const slashOpen = !busy && !slashDismissed && slashItems.length > 0;
  const commandItems = slashItems.map((item, index) => ({ item, index })).filter(({ item }) => item.kind === "command");
  const skillItems = slashItems.map((item, index) => ({ item, index })).filter(({ item }) => item.kind === "skill");
  useEffect(() => { setSlashCursor(0); setSlashDismissed(false); }, [prompt]);
  useEffect(() => {
    if (slashOpen) slashMenu.current?.querySelector<HTMLElement>(`[data-index="${slashCursor}"]`)?.scrollIntoView({ block: "nearest" });
  }, [slashCursor, slashOpen]);
  const showCancel = cancelVisible(running, cancel, prompt, attachments.length);
  const cycleApproval = async () => {
    const index = Math.max(0, approvalCycle.indexOf(approvalMode as typeof approvalCycle[number]));
    const next = approvalCycle[(index + 1) % approvalCycle.length];
    await execute({ kind: "set_approval_mode", target: next });
  };
  const cycleReasoning = () => {
    if (!reasoningLevels.length) return;
    const index = Math.max(0, reasoningLevels.indexOf(snapshot.reasoning));
    changeReasoning(reasoningLevels[(index + 1) % reasoningLevels.length]!);
  };
  const chooseSlash = (item: SlashSuggestion) => {
    setSlashDismissed(true);
    if (item.kind === "skill") {
      setPrompt(`${item.value} `);
      requestAnimationFrame(() => document.querySelector<HTMLTextAreaElement>("#azem-composer")?.focus());
      return;
    }
    setPrompt("");
    const run = async () => {
      switch (item.action) {
        case "new": await execute({ kind: "new_session" }); setView("thread"); break;
        case "compact": await execute({ kind: "compact", target: currentSessionId }); break;
        case "settings": setSettingsOpen(true); break;
        case "skills": setView("extensions"); break;
        case "reload-skills": await execute({ kind: "reload_skills" }); break;
        case "plan": setPlanMode(!planMode); break;
        case "agents": setView("agents"); break;
        case "approval": await cycleApproval(); break;
        case "fast": changeSpeed(snapshot.chatgptFastMode ? "standard" : "fast"); break;
        case "reasoning": cycleReasoning(); break;
        case "mcp": await execute({ kind: "refresh_mcp" }); break;
        case "inspector": setView("thread"); setInspectorOpen(true); break;
      }
    };
    void run().catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  };
  const chooseCurrentSlash = () => {
    const item = slashItems[Math.min(slashCursor, slashItems.length - 1)];
    if (item) chooseSlash(item);
  };
  const submitOrChooseSlash = (modeOverride?: DeliveryMode) => slashOpen ? chooseCurrentSlash() : submit(modeOverride);

  return (
    <div className="composer-shell">
      {showContextBar ? <ComposerContextBar /> : null}
      <div className="composer-card">
        {slashOpen && <div id="slash-menu" ref={slashMenu} className="slash-menu" role="listbox" aria-label={t("slashCommands")}>
          {commandItems.length > 0 && <section className="slash-commands">
            {commandItems.map(({ item, index }) => {
              const Icon = item.icon;
              return <button
                type="button" id={`slash-option-${index}`} data-index={index} role="option" aria-selected={slashCursor === index}
                className={slashCursor === index ? "active" : ""} key={item.value}
                onMouseDown={(event) => event.preventDefault()} onMouseEnter={() => setSlashCursor(index)} onClick={() => chooseSlash(item)}
              ><Icon size={15} /><span className="slash-label">{item.label}</span><span className="slash-detail">{item.detail}</span></button>;
            })}
          </section>}
          {skillItems.length > 0 && <section className="slash-skills">
            <header>{t("skills")}</header>
            {skillItems.map(({ item, index }) => {
              const Icon = item.icon;
              return <button
                type="button" id={`slash-option-${index}`} data-index={index} role="option" aria-selected={slashCursor === index}
                className={slashCursor === index ? "active" : ""} key={item.value}
                onMouseDown={(event) => event.preventDefault()} onMouseEnter={() => setSlashCursor(index)} onClick={() => chooseSlash(item)}
              ><Icon size={15} /><span className="slash-skill-main"><span className="slash-label">{item.label}</span><span className="slash-detail">{item.detail}</span></span>{item.badge && <em className="slash-badge">{item.badge}</em>}</button>;
            })}
          </section>}
        </div>}
        {selectedSkill && <div className="composer-skill"><WandSparkles size={17} aria-hidden="true" /><span>{selectedSkill.name}</span></div>}
        {attachments.length > 0 && <div className="attachment-row">{attachments.map((item) => <span key={item.id}><ImagePlus size={13} />{item.name}<button aria-label={`移除 ${item.name}`} onClick={() => removeAttachment(item.id)}><X size={12} /></button></span>)}</div>}
        <textarea id="azem-composer" value={visiblePrompt} onChange={(event) => setPrompt(skillPrefix + event.target.value)} onPaste={(event) => {
          const images = pastedImages(event.clipboardData);
          if (images.length > 0) {
            event.preventDefault();
            const timestamp = new Date();
            void attachClipboard(images.map((file, index) => file.name ? file : namedClipboardImage(file, timestamp, index)));
            return;
          }
          if (!shouldReadNativeClipboard(event.clipboardData)) return;
          event.preventDefault();
          void attachClipboard([]);
        }} onFocus={() => setSlashDismissed(false)} onBlur={() => setSlashDismissed(true)}
          aria-autocomplete="list" aria-expanded={slashOpen} aria-controls={slashOpen ? "slash-menu" : undefined} aria-activedescendant={slashOpen ? `slash-option-${slashCursor}` : undefined}
          placeholder={busy ? running && deliveryMode === "guide" ? t("guidePlaceholder") : t("queuePlaceholder") : t("promptPlaceholder")} rows={2} onKeyDown={(event) => {
          if (selectedSkill && event.key === "Backspace" && !visiblePrompt) {
            event.preventDefault();
            setPrompt("");
            return;
          }
          if (slashOpen && ["ArrowDown", "ArrowUp", "Enter", "Tab", "Escape"].includes(event.key)) {
            event.preventDefault();
            if (event.key === "ArrowDown") setSlashCursor((slashCursor + 1) % slashItems.length);
            else if (event.key === "ArrowUp") setSlashCursor((slashCursor - 1 + slashItems.length) % slashItems.length);
            else if (event.key === "Escape") setSlashDismissed(true);
            else chooseCurrentSlash();
            return;
          }
          if (event.nativeEvent.isComposing) return;
          // Codex-style one-shot inversion: Cmd/Ctrl+Shift+Enter uses the opposite active-run mode.
          if (running && event.key === "Enter" && event.shiftKey && (event.metaKey || event.ctrlKey) && !event.altKey) {
            event.preventDefault();
            submitOrChooseSlash(deliveryMode === "queue" ? "guide" : "queue");
            return;
          }
          // Enter sends; Shift+Enter inserts a newline.
          if (event.key === "Enter" && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey) {
            event.preventDefault();
            submitOrChooseSlash();
          }
        }} />
        <div className="composer-toolbar">
          <label className="icon-button attach-button" title={t("attach")}>
            <Plus size={15} />
            <input type="file" accept="image/*" multiple onChange={(event) => { attach(event.target.files); event.target.value = ""; }} />
          </label>
          <ApprovalPicker value={approvalMode} disabled={running} language={snapshot.language} onChange={(mode) => void execute({ kind: "set_approval_mode", target: mode }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)))} />
          <button
            type="button"
            className="plan-mode-toggle"
            data-active={String(planMode)}
            disabled={running}
            title={planMode ? t("plan") : t("planHint")}
            aria-pressed={planMode}
            aria-label={t("plan")}
            onClick={() => setPlanMode(!planMode)}
          >
            <Lightbulb size={15} />
            {planMode ? <span>{t("planLabel")}</span> : null}
          </button>
          <span className="toolbar-spacer" />
          <ContextMeter />
          <ModelControls
            running={running} models={modelChoices} selectedModel={modelKey(snapshot.provider, snapshot.model)} selectedModelName={selectedModelName}
            reasoningLevels={reasoningLevels} selectedReasoning={snapshot.reasoning} selectedReasoningName={selectedReasoningName}
            fast={snapshot.chatgptFastMode} reasoningNames={reasoningNames} onModelChange={changeModel} onReasoningChange={changeReasoning} onSpeedChange={changeSpeed}
            modelLabel={t("model")} reasoningLabel={t("reasoning")} speedLabel={t("speed")} standardSpeed={t("standardSpeed")} fastSpeed={t("fastSpeed")} fastHint={t("fastModeHint")}
            fasterLabel={t("reasoningFaster")} smarterLabel={t("reasoningSmarter")} advancedLabel={t("reasoningAdvanced")} backLabel={t("reasoningBack")}
            highCostHint={t("reasoningMaxHint")} fastBoostTitle={t("fastBoostTitle")} fastBoostDetail={t("fastBoostDetail")} language={snapshot.language}
          />
          {showCancel ? <button className="cancel-button" data-cancel-run onClick={cancel} title={t("cancel")}><CircleStop size={16} /></button> : <button className="send-button" onClick={() => submitOrChooseSlash()} disabled={!prompt.trim() && attachments.length === 0} title={busy ? running && deliveryMode === "guide" ? t("guide") : t("queue") : t("send")}><Send size={15} /></button>}
        </div>
      </div>
    </div>
  );
}

export function pastedImages(clipboardData: DataTransfer): File[] {
  const files = Array.from(clipboardData.files);
  const candidates = files.length > 0
    ? files
    : Array.from(clipboardData.items)
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);
  return candidates.filter((file) => file.type.startsWith("image/"));
}

export function namedClipboardImage(file: File, timestamp: Date, index: number): File {
  const extension = file.type === "image/jpeg" || file.type === "image/jpg" ? "jpg"
    : file.type === "image/gif" ? "gif"
      : file.type === "image/webp" ? "webp"
        : "png";
  const suffix = index === 0 ? "" : `-${index + 1}`;
  const name = `pasted-image-${timestamp.toISOString().replace(/[:.]/g, "-")}${suffix}.${extension}`;
  return new File([file], name, { type: file.type || `image/${extension}`, lastModified: file.lastModified });
}

function workspaceBasename(path: string) {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace";
}

/** Codex-style chips: project · local · branch */
function ComposerContextBar() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const branches = useRuntimeStore((state) => state.branches);
  const workspaceDirty = useRuntimeStore((state) => state.workspaceDirty);
  const workspaceChangedFiles = useRuntimeStore((state) => state.workspaceChangedFiles);
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const project = workspaceBasename(snapshot.workspace);
  const currentBranch = branches.find((branch) => branch.current)?.name || "";
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const [newBranch, setNewBranch] = useState("");
  const [creatingBusy, setCreatingBusy] = useState(false);
  const branchMenu = useRef<HTMLDetailsElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const createRef = useRef<HTMLInputElement>(null);

  const filteredBranches = branches
    .filter((branch) => !query.trim() || branch.name.toLowerCase().includes(query.trim().toLowerCase()))
    .slice()
    .sort((left, right) => Number(right.current) - Number(left.current) || left.name.localeCompare(right.name));

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const node = branchMenu.current;
      if (!node?.open || node.contains(event.target as Node)) return;
      node.open = false;
      setQuery("");
      setCreating(false);
      setNewBranch("");
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);

  useEffect(() => {
    const node = branchMenu.current;
    if (!node) return;
    const onToggle = () => {
      if (node.open) {
        requestAnimationFrame(() => searchRef.current?.focus());
      } else {
        setQuery("");
        setCreating(false);
        setNewBranch("");
      }
    };
    node.addEventListener("toggle", onToggle);
    return () => node.removeEventListener("toggle", onToggle);
  }, []);

  useEffect(() => {
    if (creating) requestAnimationFrame(() => createRef.current?.focus());
  }, [creating]);

  const switchProject = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const path = await selectProjectFolder(t("chooseProjectFolder"), t("openProject"));
      if (path) await openProject(path);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  const switchBranch = async (name: string, confirmDirty = false) => {
    if (!name || name === currentBranch) return;
    try {
      await execute({
        kind: "switch_git_branch",
        target: name,
        decision: confirmDirty ? "confirm_dirty" : undefined,
      });
      if (branchMenu.current) branchMenu.current.open = false;
      setQuery("");
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      if (!confirmDirty && /uncommitted changes/i.test(message)) {
        const ok = window.confirm(tFormat(snapshot.language, "dirtySwitchConfirm", { branch: name }));
        if (ok) await switchBranch(name, true);
        return;
      }
      setError(message);
    }
  };

  const createBranch = async () => {
    const name = newBranch.trim();
    if (!name || creatingBusy) return;
    setCreatingBusy(true);
    try {
      await execute({ kind: "create_git_branch", target: name });
      if (branchMenu.current) branchMenu.current.open = false;
      setCreating(false);
      setNewBranch("");
      setQuery("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setCreatingBusy(false);
    }
  };

  return (
    <div className="composer-context-bar" aria-label={t("workspace")}>
      <button type="button" className="composer-chip composer-chip-action" title={`${t("switchProject")}: ${snapshot.workspace}`} disabled={busy} onClick={() => void switchProject()}>
        <Folder size={13} />
        <span>{project}</span>
      </button>
      <span className="composer-chip" title={snapshot.workspace}>
        <HardDrive size={13} />
        <span>{t("local")}</span>
      </span>
      {branches.length > 0 ? (
        <details ref={branchMenu} className="composer-branch-menu">
          <summary className="composer-chip composer-chip-action" title={t("branch")}>
            <GitBranch size={13} />
            <span>{currentBranch || t("branch")}</span>
            <ChevronDown size={11} />
          </summary>
          <div className="composer-branch-panel" role="listbox" aria-label={t("branch")}>
            <label className="composer-branch-search">
              <Search size={14} />
              <input
                ref={searchRef}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t("searchBranches")}
                aria-label={t("searchBranches")}
              />
            </label>
            <div className="composer-branch-section">{t("branchesSection")}</div>
            <div className="composer-branch-options">
              {filteredBranches.length === 0 ? (
                <div className="composer-branch-empty">{t("noMatchingBranches")}</div>
              ) : filteredBranches.map((branch) => (
                <button
                  type="button"
                  role="option"
                  aria-selected={branch.current}
                  className={branch.current ? "selected" : ""}
                  key={branch.name}
                  title={branch.name}
                  onClick={() => void switchBranch(branch.name)}
                >
                  <GitBranch size={14} />
                  <span className="composer-branch-meta">
                    <strong>{branch.name}</strong>
                    {branch.current && workspaceDirty && workspaceChangedFiles > 0 ? (
                      <small>{tFormat(snapshot.language, "uncommittedFiles", { count: workspaceChangedFiles })}</small>
                    ) : null}
                  </span>
                  {branch.current ? <Check size={15} className="composer-branch-check" /> : <span className="composer-branch-check" />}
                </button>
              ))}
            </div>
            <div className="composer-branch-footer">
              {creating ? (
                <form
                  className="composer-branch-create-form"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void createBranch();
                  }}
                >
                  <GitBranch size={14} />
                  <input
                    ref={createRef}
                    value={newBranch}
                    onChange={(event) => setNewBranch(event.target.value)}
                    placeholder={t("newBranchPlaceholder")}
                    aria-label={t("newBranchPlaceholder")}
                    disabled={creatingBusy}
                  />
                  <button type="submit" disabled={creatingBusy || !newBranch.trim()}>{t("createBranch")}</button>
                </form>
              ) : (
                <button type="button" className="composer-branch-create" onClick={() => setCreating(true)}>
                  <Plus size={14} />
                  <span>{t("createCheckoutBranch")}</span>
                </button>
              )}
            </div>
          </div>
        </details>
      ) : (
        <span className="composer-chip composer-chip-muted" title={t("noBranches")}>
          <GitBranch size={13} />
          <span>{t("noBranches")}</span>
        </span>
      )}
    </div>
  );
}

const approvalModes = [
  { value: "prompt", labelKey: "promptApproval" as const, hintKey: "approvalAskHint" as const, Icon: Hand, danger: false },
  { value: "auto_review", labelKey: "autoReview" as const, hintKey: "approvalAutoHint" as const, Icon: ShieldCheck, danger: false },
  { value: "yolo", labelKey: "yolo" as const, hintKey: "approvalFullHint" as const, Icon: ShieldAlert, danger: true },
] as const;

/** Codex-style approval / permissions menu in the composer toolbar. */
function ApprovalPicker({ value, disabled, language, onChange }: {
  value: string;
  disabled?: boolean;
  language: Snapshot["language"];
  onChange: (mode: string) => void;
}) {
  const details = useRef<HTMLDetailsElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const summary = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);
  const [box, setBox] = useState<{ top?: number; bottom?: number; left: number; width: number } | null>(null);
  const t = translator(language);
  const current = approvalModes.find((mode) => mode.value === value) ?? approvalModes[0];
  const CurrentIcon = current.Icon;

  const place = useCallback(() => {
    const el = summary.current;
    if (!el || !details.current?.open) {
      setBox(null);
      return;
    }
    const rect = el.getBoundingClientRect();
    const width = Math.min(340, window.innerWidth - 16);
    const left = Math.min(Math.max(8, rect.left), window.innerWidth - width - 8);
    const spaceAbove = rect.top - 8;
    const openUp = spaceAbove >= 220 || spaceAbove > window.innerHeight - rect.bottom;
    setBox(openUp
      ? { bottom: window.innerHeight - rect.top + 8, left, width }
      : { top: rect.bottom + 8, left, width });
  }, []);

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (details.current?.contains(target) || panel.current?.contains(target)) return;
      if (details.current) details.current.open = false;
      setOpen(false);
      setBox(null);
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);

  useEffect(() => {
    if (!open) return;
    place();
    const onReposition = () => place();
    window.addEventListener("resize", onReposition);
    window.addEventListener("scroll", onReposition, true);
    return () => {
      window.removeEventListener("resize", onReposition);
      window.removeEventListener("scroll", onReposition, true);
    };
  }, [open, place]);

  const menu = open && box ? createPortal(
    <div
      ref={panel}
      className="approval-picker-menu"
      role="menu"
      aria-label={t("approvalMenuTitle")}
      style={{ position: "fixed", top: box.top, bottom: box.bottom, left: box.left, width: box.width, zIndex: 220 }}
    >
      <header className="approval-picker-heading">
        <span>{t("approvalMenuTitle")}</span>
      </header>
      {approvalModes.map((mode) => {
        const Icon = mode.Icon;
        const selected = mode.value === value;
        return (
          <button
            type="button"
            role="menuitemradio"
            aria-checked={selected}
            className={`approval-picker-option ${selected ? "selected" : ""} ${mode.danger ? "danger" : ""}`}
            key={mode.value}
            onClick={() => {
              onChange(mode.value);
              if (details.current) details.current.open = false;
              setOpen(false);
              setBox(null);
            }}
          >
            <Icon size={16} />
            <span className="approval-picker-copy">
              <strong>{t(mode.labelKey)}</strong>
              <small>{t(mode.hintKey)}</small>
            </span>
            <Check size={14} className="approval-picker-check" />
          </button>
        );
      })}
    </div>,
    document.body,
  ) : null;

  return <>
    <details
      ref={details}
      className="approval-picker"
      data-disabled={String(Boolean(disabled))}
      data-mode={current.value}
      onToggle={(event) => {
        if (disabled) {
          event.currentTarget.open = false;
          setOpen(false);
          setBox(null);
          return;
        }
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) requestAnimationFrame(place);
        else setBox(null);
      }}
    >
      <summary
        ref={summary as React.RefObject<HTMLElement>}
        aria-label={t(current.labelKey)}
        aria-disabled={disabled}
        title={t(current.labelKey)}
      >
        <CurrentIcon size={14} />
        <span>{t(current.labelKey)}</span>
      </summary>
    </details>
    {menu}
  </>;
}

function cancelVisible(running: boolean, cancel: (() => void) | undefined, prompt: string, attachmentCount = 0) {
  return running && Boolean(cancel) && !prompt.trim() && attachmentCount === 0;
}

function QueuedPrompts({ items, running, pauseReason, editingId, deliveryMode, onGuide, onRetry, onDelete, onEdit, onReorder, onResume, onToggleQueue }: {
  items: QueuedPrompt[];
  running: boolean;
  pauseReason: "interrupted" | undefined;
  editingId: string | null;
  deliveryMode: DeliveryMode;
  onGuide: (item: QueuedPrompt) => Promise<void>;
  onRetry: (id: string) => void;
  onDelete: (item: QueuedPrompt) => void;
  onEdit: (item: QueuedPrompt) => void;
  onReorder: (id: string, targetId: string) => void;
  onResume: () => void;
  onToggleQueue: () => void;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const [draggedId, setDraggedId] = useState("");
  const t = translator(snapshot.language);
  return <section className="queued-prompts" aria-label={`${t("queuedMessages")} (${items.length})`}>
    {pauseReason === "interrupted" && <header className="queue-paused" role="status">
      <span>{t("queueInterrupted")}</span>
      <button type="button" onClick={onResume}>{t("resumeQueue")}</button>
    </header>}
    <div className="queued-prompt-scroll">
      {items.map((item, index) => <div
        className="queued-prompt"
        data-state={item.state ?? "queued"}
        data-editing={String(item.id === editingId)}
        draggable
        key={item.id}
        onDragStart={(event) => {
          setDraggedId(item.id);
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData("text/plain", item.id);
        }}
        onDragEnd={() => setDraggedId("")}
        onDragOver={(event) => {
          if (draggedId && draggedId !== item.id) event.preventDefault();
        }}
        onDrop={(event) => {
          event.preventDefault();
          const source = draggedId || event.dataTransfer.getData("text/plain");
          if (source && source !== item.id) onReorder(source, item.id);
          setDraggedId("");
        }}
      >
        <span className="queue-drag" aria-hidden="true"><GripVertical size={14} /></span>
        <button className="queued-prompt-content" onClick={() => onEdit(item)} title={item.error || t("editMessage")}>
          {item.attachments.length > 0 && <ImagePlus size={14} />}
          <span>{item.text || item.attachments[0]?.name}</span>
        </button>
        {item.state === "failed"
          ? <button className="queued-guide" title={item.error || t("queuedMessageFailed")} onClick={() => onRetry(item.id)}><RotateCcw size={14} />{t("retryMessage")}</button>
          : <button className="queued-guide" disabled={!running} title={t("steerTooltip")} onClick={() => void onGuide(item)}><CornerUpRight size={14} />{t("guide")}</button>}
        <button className="queued-icon" title={t("deleteMessage")} aria-label={t("deleteMessage")} onClick={() => onDelete(item)}><Trash2 size={14} /></button>
        <QueueMenu
          item={item}
          queueing={deliveryMode === "queue"}
          canMoveUp={index > 0}
          canMoveDown={index < items.length - 1}
          onEdit={onEdit}
          onMoveUp={() => onReorder(item.id, items[index - 1]!.id)}
          onMoveDown={() => onReorder(item.id, items[index + 1]!.id)}
          onToggleQueue={onToggleQueue}
        />
      </div>)}
    </div>
  </section>;
}

function QueueMenu({ item, queueing, canMoveUp, canMoveDown, onEdit, onMoveUp, onMoveDown, onToggleQueue }: {
  item: QueuedPrompt;
  queueing: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onEdit: (item: QueuedPrompt) => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onToggleQueue: () => void;
}) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const details = useRef<HTMLDetailsElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [menuBox, setMenuBox] = useState<{ bottom: number; left: number; width: number } | null>(null);
  const t = translator(snapshot.language);
  const place = useCallback(() => {
    const summary = details.current?.querySelector("summary");
    if (!details.current?.open || !summary) {
      setMenuBox(null);
      return;
    }
    const rect = summary.getBoundingClientRect();
    const width = 180;
    setMenuBox({
      bottom: Math.max(8, window.innerHeight - rect.top + 4),
      left: Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8)),
      width,
    });
  }, []);
  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (details.current?.contains(target) || menu.current?.contains(target)) return;
      if (details.current) details.current.open = false;
      setMenuBox(null);
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, []);
  useEffect(() => {
    if (!menuBox) return;
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [menuBox, place]);
  const choose = (action: () => void) => {
    action();
    if (details.current) details.current.open = false;
    setMenuBox(null);
  };
  const actions = <div ref={menu} className="queue-menu-popover" style={menuBox ?? undefined}>
    <button onClick={() => choose(() => onEdit(item))}><Pencil size={14} />{t("editMessage")}</button>
    <button disabled={!canMoveUp} onClick={() => choose(onMoveUp)}><ArrowUp size={14} />{t("moveMessageUp")}</button>
    <button disabled={!canMoveDown} onClick={() => choose(onMoveDown)}><ArrowDown size={14} />{t("moveMessageDown")}</button>
    <button onClick={() => choose(onToggleQueue)}><ListX size={14} />{t(queueing ? "turnOffQueueing" : "turnOnQueueing")}</button>
  </div>;
  return <>
    <details ref={details} className="queue-menu" onToggle={() => requestAnimationFrame(place)}>
      <summary aria-label={t("moreActions")} title={t("moreActions")}><MoreHorizontal size={15} /></summary>
    </details>
    {menuBox ? createPortal(actions, document.body) : null}
  </>;
}

type ModelControlOption = { value: string; label: string; hint?: string };
type ModelControlGroup = {
  label: string;
  current: string;
  selected: string;
  options: ModelControlOption[];
  select: (value: string) => void;
};

function ModelControls({ running, models, selectedModel, selectedModelName, reasoningLevels, selectedReasoning, selectedReasoningName, fast, reasoningNames, onModelChange, onReasoningChange, onSpeedChange, modelLabel, reasoningLabel, speedLabel, standardSpeed, fastSpeed, fastHint, fasterLabel, smarterLabel, advancedLabel, backLabel, highCostHint, fastBoostTitle, fastBoostDetail, language }: {
  running: boolean; models: ComposerModel[]; selectedModel: string; selectedModelName: string; reasoningLevels: string[]; selectedReasoning: string; selectedReasoningName: string;
  fast: boolean; reasoningNames: Record<string, string>; onModelChange: (value: string) => void; onReasoningChange: (value: string) => void; onSpeedChange: (value: string) => void;
  modelLabel: string; reasoningLabel: string; speedLabel: string; standardSpeed: string; fastSpeed: string; fastHint: string;
  fasterLabel: string; smarterLabel: string; advancedLabel: string; backLabel: string; highCostHint: string;
  fastBoostTitle: string; fastBoostDetail: string;
  language: Snapshot["language"];
}) {
  const root = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const rowRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  const [open, setOpen] = useState(false);
  // Plan A: effort slider is the default surface; advanced keeps the classic menus.
  const [view, setView] = useState<"effort" | "advanced">("effort");
  const [activeGroup, setActiveGroup] = useState<string | null>(null);
  const [menuBox, setMenuBox] = useState<{ top?: number; bottom?: number; left: number; width: number } | null>(null);
  const [subBox, setSubBox] = useState<{ top: number; left: number; width: number; maxHeight: number } | null>(null);

  const levels = sortReasoningLevels(reasoningLevels);
  const groups: ModelControlGroup[] = [
    { label: modelLabel, current: selectedModelName, selected: selectedModel, options: models.map((model) => ({ value: modelKey(model.provider, model.id), label: model.name })), select: onModelChange },
    {
      label: reasoningLabel,
      current: selectedReasoningName,
      selected: selectedReasoning,
      options: levels.map((level) => ({
        value: level,
        label: reasoningNames[level] ?? level,
        hint: reasoningHint(level, language),
      })),
      select: onReasoningChange,
    },
    { label: speedLabel, current: fast ? fastSpeed : standardSpeed, selected: fast ? "fast" : "standard", options: [{ value: "standard", label: standardSpeed }, { value: "fast", label: fastSpeed }], select: onSpeedChange },
  ];

  const placeMenu = useCallback(() => {
    const el = summary.current;
    if (!el || !root.current?.open) {
      setMenuBox(null);
      return;
    }
    const rect = el.getBoundingClientRect();
    // Reverse-engineered from Codex model-picker menu: compact chip, not a wide sheet.
    const preferred = view === "effort" ? 248 : Math.max(280, rect.width + 40);
    const width = Math.min(preferred, Math.min(320, window.innerWidth - 16));
    const spaceAbove = rect.top - 8;
    const spaceBelow = window.innerHeight - rect.bottom - 8;
    const openUp = spaceAbove >= 160 || spaceAbove > spaceBelow;
    const left = Math.min(Math.max(8, rect.right - width), window.innerWidth - width - 8);
    setMenuBox(openUp
      ? { bottom: window.innerHeight - rect.top + 8, left, width }
      : { top: rect.bottom + 8, left, width });
  }, [view]);

  const placeSubmenu = useCallback((label: string) => {
    const row = rowRefs.current[label];
    const panel = menu.current;
    if (!row || !panel) {
      setSubBox(null);
      return;
    }
    const rowRect = row.getBoundingClientRect();
    const menuRect = panel.getBoundingClientRect();
    const group = groups.find((item) => item.label === label);
    const options = group?.options ?? [];
    const longest = options.reduce((max, option) => Math.max(max, option.label.length + (option.hint?.length ? 4 : 0)), 0) || 12;
    const width = Math.min(Math.max(180, longest * 9 + 56), Math.min(320, window.innerWidth - 16));
    const spaceLeft = menuRect.left - 8;
    const spaceRight = window.innerWidth - menuRect.right - 8;
    const openRight = spaceRight >= width || spaceRight >= spaceLeft;
    const left = openRight
      ? Math.min(menuRect.right + 8, window.innerWidth - width - 8)
      : Math.max(8, menuRect.left - width - 8);
    const titleH = 28;
    const pad = 12;
    const rowH = options.reduce((sum, option) => sum + (option.hint ? 48 : 36), 0);
    const contentHeight = titleH + pad + rowH;
    const maxHeight = Math.min(Math.max(contentHeight, 80), window.innerHeight - 16);
    let top = rowRect.top - 8;
    if (top + Math.min(contentHeight, maxHeight) > window.innerHeight - 8) {
      top = Math.max(8, window.innerHeight - 8 - Math.min(contentHeight, maxHeight));
    }
    top = Math.max(8, top);
    setSubBox({ top, left, width, maxHeight });
  }, [groups]);

  const resetClosed = useCallback(() => {
    setOpen(false);
    setView("effort");
    setActiveGroup(null);
    setMenuBox(null);
    setSubBox(null);
  }, []);

  useEffect(() => {
    const close = (event: PointerEvent) => {
      const target = event.target as Node;
      if (root.current?.contains(target) || menu.current?.contains(target)) return;
      if ((target as HTMLElement).closest?.(".model-control-submenu-portal")) return;
      if (root.current) root.current.open = false;
      resetClosed();
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, [resetClosed]);

  useEffect(() => {
    if (!open) return;
    placeMenu();
    const onReposition = () => {
      placeMenu();
      if (view === "advanced" && activeGroup) placeSubmenu(activeGroup);
    };
    window.addEventListener("resize", onReposition);
    window.addEventListener("scroll", onReposition, true);
    return () => {
      window.removeEventListener("resize", onReposition);
      window.removeEventListener("scroll", onReposition, true);
    };
  }, [open, view, activeGroup, placeMenu, placeSubmenu]);

  useEffect(() => {
    if (!open || view !== "advanced" || !activeGroup) {
      setSubBox(null);
      return;
    }
    const frame = requestAnimationFrame(() => placeSubmenu(activeGroup));
    return () => cancelAnimationFrame(frame);
  }, [open, view, activeGroup, placeSubmenu]);

  const closeAll = () => {
    if (root.current) root.current.open = false;
    resetClosed();
    summary.current?.focus();
  };
  const choose = (select: (value: string) => void, value: string) => {
    select(value);
    // Keep the advanced panel open when changing model so the user can also adjust effort.
    if (view === "advanced") {
      setActiveGroup(null);
      setSubBox(null);
      return;
    }
    closeAll();
  };

  const active = groups.find((group) => group.label === activeGroup) ?? null;
  const highEffort = isHighCostReasoning(selectedReasoning);

  const menuPortal = open && menuBox ? createPortal(
    <div
      ref={menu}
      className={`model-control-menu model-control-menu-portal model-control-menu-${view}`}
      style={{
        position: "fixed",
        top: menuBox.top,
        bottom: menuBox.bottom,
        left: menuBox.left,
        width: menuBox.width,
        zIndex: 210,
      }}
    >
      {view === "effort" ? (
        <div className="effort-panel">
          <div className="effort-panel-toolbar">
            <button
              type="button"
              className="effort-panel-advanced"
              disabled={running}
              onClick={() => {
                setView("advanced");
                setActiveGroup(null);
                setSubBox(null);
                requestAnimationFrame(placeMenu);
              }}
            >
              <span>{advancedLabel}</span>
              <ChevronRight size={12} />
            </button>
            <div className={`effort-panel-speed-wrap ${fast ? "active" : ""}`}>
              <button
                type="button"
                className={`effort-panel-speed ${fast ? "active" : ""}`}
                disabled={running}
                title={fast ? `${fastBoostTitle} · ${fastBoostDetail}` : `${standardSpeed} · ${fastHint}`}
                aria-label={speedLabel}
                aria-pressed={fast}
                onClick={() => onSpeedChange(fast ? "standard" : "fast")}
              >
                <Zap size={16} />
              </button>
              <div className="effort-speed-tip" role="tooltip">
                <strong>{fastBoostTitle}</strong>
                <span>{fastBoostDetail}</span>
              </div>
            </div>
          </div>
          {levels.length > 1 ? (
            <ReasoningEffortSlider
              levels={levels}
              value={selectedReasoning}
              onChange={onReasoningChange}
              labels={reasoningNames}
              fasterLabel={fasterLabel}
              smarterLabel={smarterLabel}
              highCostHint={highCostHint}
              fast={fast}
              disabled={running}
              ariaLabel={reasoningLabel}
            />
          ) : (
            <p className="effort-panel-single">{selectedReasoningName || reasoningLabel}</p>
          )}
        </div>
      ) : (
        <>
          <button
            type="button"
            className="model-control-back"
            onClick={() => {
              setView("effort");
              setActiveGroup(null);
              setSubBox(null);
            }}
          >
            <ChevronLeft size={13} />
            <span>{backLabel}</span>
            <small>{reasoningLabel}</small>
          </button>
          {groups.map((group) => (
            <div
              className={`model-control-item ${activeGroup === group.label ? "open" : ""}`}
              key={group.label}
              onMouseEnter={() => !running && setActiveGroup(group.label)}
              onFocus={() => !running && setActiveGroup(group.label)}
            >
              <button
                type="button"
                ref={(node) => { rowRefs.current[group.label] = node; }}
                className="model-control-row"
                disabled={running}
                aria-haspopup="menu"
                aria-expanded={activeGroup === group.label}
                onClick={() => !running && setActiveGroup((current) => current === group.label ? null : group.label)}
              >
                <span>{group.label}</span>
                <small title={group.current}>{group.current}</small>
                <ChevronRight size={13} />
              </button>
            </div>
          ))}
          <p>{fastHint}</p>
        </>
      )}
    </div>,
    document.body,
  ) : null;

  const subPortal = open && view === "advanced" && active && subBox ? createPortal(
    <div
      className="model-control-submenu model-control-submenu-portal"
      role="menu"
      aria-label={active.label}
      style={{
        position: "fixed",
        top: subBox.top,
        left: subBox.left,
        width: subBox.width,
        maxHeight: subBox.maxHeight,
        zIndex: 211,
      }}
      onMouseEnter={() => setActiveGroup(active.label)}
    >
      <header className="model-control-submenu-title">{active.label}</header>
      {active.options.map((option) => (
        <button
          type="button"
          role="menuitemradio"
          aria-checked={active.selected === option.value}
          disabled={running}
          className={active.selected === option.value ? "selected" : ""}
          key={option.value}
          title={option.hint ? `${option.label} — ${option.hint}` : option.label}
          onClick={() => choose(active.select, option.value)}
        >
          <span className="model-control-option-copy">
            <span>{option.label}</span>
            {option.hint ? <small>{option.hint}</small> : null}
          </span>
          <Check size={13} />
        </button>
      ))}
    </div>,
    document.body,
  ) : null;

  return <>
    <details
      ref={root}
      className="model-controls"
      data-disabled={String(running)}
      data-open={String(open)}
      onToggle={(event) => {
        if (running) {
          event.currentTarget.open = false;
          resetClosed();
          return;
        }
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) {
          setView("effort");
          setActiveGroup(null);
          requestAnimationFrame(placeMenu);
        } else {
          resetClosed();
        }
      }}
    >
      <summary
        ref={summary as React.RefObject<HTMLElement>}
        aria-disabled={running}
        aria-expanded={open}
        title={fast
          ? `${fastBoostTitle} · ${selectedModelName} · ${selectedReasoningName}`
          : `${selectedModelName} · ${selectedReasoningName}`}
        data-fast={String(fast)}
        data-high={String(highEffort)}
      >
        {fast ? <Zap size={12} className="model-controls-fast-icon" aria-hidden="true" /> : null}
        <span>{selectedModelName}</span>
        <small data-high={String(highEffort)}>{selectedReasoningName}</small>
        <ChevronDown size={12} className="model-controls-chevron" />
      </summary>
    </details>
    {menuPortal}
    {subPortal}
  </>;
}

export function ContextMeter() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const usage = useRuntimeStore((state) => state.contextUsage);
  const profile = useRuntimeStore((state) => state.contextProfile);
  const details = useRef<HTMLDetailsElement>(null);
  const t = translator(snapshot.language);
  const metrics = contextOccupancy(usage, profile);
  // Blue by default; only turn red when nearly full.
  const tone = metrics.percentage >= 90 ? "critical" : "normal";
  useEffect(() => {
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (details.current && !details.current.contains(event.target as Node)) details.current.open = false;
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer, true);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePointer, true);
  }, []);
  const label = metrics.limit > 0 ? `${t("contextUsage")} ${metrics.percentage}%` : t("contextUnavailable");
  return <details ref={details} className="context-meter" data-tone={tone}>
    <summary title={label} aria-label={label}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><circle className="context-ring-track" cx="10" cy="10" r="7.5" pathLength="100" /><circle className="context-ring-value" cx="10" cy="10" r="7.5" pathLength="100" strokeDasharray={`${metrics.percentage} 100`} /></svg>
    </summary>
    <div className="context-meter-popover">
      <header><strong>{t("contextUsage")}</strong><span>{metrics.limit > 0 ? `${metrics.estimated ? "~" : ""}${metrics.percentage}%` : "—"}</span></header>
      <div className="context-progress" role="progressbar" aria-label={t("contextUsage")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={metrics.percentage}><span style={{ width: `${metrics.percentage}%` }} /></div>
      <footer>{metrics.limit > 0 ? <><span>{metrics.estimated ? "~" : ""}{formatTokens(metrics.used)} / {formatTokens(metrics.limit)}</span><span>{t("contextRemaining")} {formatTokens(metrics.remaining)}</span></> : <span>{t("contextUnavailable")}</span>}</footer>
    </div>
  </details>;
}

export function contextOccupancy(usage: ContextUsage, profile: ContextProfile | null) {
  let used = (profile?.contributions ?? []).reduce((total, item) => total + Math.max(0, item.tokens), 0) + Math.max(0, usage.outputTokens);
  let estimated = Boolean(profile?.estimated);
  if ((profile?.reportedInputTokens ?? 0) > 0) {
    used = Math.max(0, profile!.reportedInputTokens!) + Math.max(0, profile!.reportedOutputTokens ?? 0);
    estimated = false;
  } else if (usage.inputTokens > 0) {
    used = Math.max(0, usage.inputTokens) + Math.max(0, usage.outputTokens);
    estimated = !usage.reported;
  }
  const limit = Math.max(0, usage.contextLimit);
  const percentage = limit > 0 ? Math.min(100, Math.round(used * 100 / limit)) : 0;
  return { used, limit, percentage, remaining: Math.max(0, limit - used), estimated };
}

function formatTokens(tokens: number) {
  if (tokens >= 1_000_000) return `${Number((tokens / 1_000_000).toFixed(tokens < 10_000_000 ? 1 : 0))}M`;
  if (tokens >= 1_000) return `${Number((tokens / 1_000).toFixed(tokens < 10_000 ? 1 : 0))}K`;
  return String(tokens);
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

type ComposerModel = { provider: string; id: string; name: string; reasoningLevels: string[]; defaultReasoning?: string };

function useComposerModels(snapshot: Snapshot) {
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const setSessionModel = useRuntimeStore((state) => state.setSessionModel);
  const setChatGPTFastMode = useRuntimeStore((state) => state.setChatGPTFastMode);
  const setError = useRuntimeStore((state) => state.setError);
  const fallbackModel: ComposerModel = { provider: snapshot.provider, id: snapshot.model, name: snapshot.model, reasoningLevels: [snapshot.reasoning].filter(Boolean) };
  const catalogModels = Object.entries(modelsByProvider).flatMap(([provider, models]) => models.map((model) => ({ ...model, provider })));
  const modelChoices = [...new Map([fallbackModel, ...catalogModels].map((model) => [modelKey(model.provider, model.id), model])).values()];
  const providerModels = modelsByProvider[snapshot.provider] ?? [];
  const catalogLevels = providerModels.find((model) => model.id === snapshot.model)?.reasoningLevels ?? ["low", "medium", "high", "xhigh", "max"];
  // Keep Codex order (轻度→最高); never pin the current value to the top of the list.
  const reasoningLevels = sortReasoningLevels([snapshot.reasoning, ...catalogLevels]);
  const selectedModelName = modelChoices.find((model) => modelKey(model.provider, model.id) === modelKey(snapshot.provider, snapshot.model))?.name ?? snapshot.model;
  const persistSessionPreferences = (provider: string, model: string, reasoning: string, previous: { provider: string; model: string; reasoning: string }) => {
    execute({
      kind: "set_session_preferences",
      sessionId: currentSessionId,
      route: { Scope: "session", Role: "", Label: "", Route: { provider, model, reasoning } },
    }).catch((cause) => {
      setSessionModel(previous.provider, previous.model, previous.reasoning);
      setError(cause instanceof Error ? cause.message : String(cause));
    });
  };
  const changeModel = (value: string) => {
    const choice = modelChoices.find((model) => modelKey(model.provider, model.id) === value)!;
    const reasoning = [choice.defaultReasoning, ...choice.reasoningLevels, snapshot.reasoning].filter(Boolean)[0]!;
    const previous = { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning };
    setSessionModel(choice.provider, choice.id, reasoning);
    persistSessionPreferences(choice.provider, choice.id, reasoning, previous);
  };
  const changeReasoning = (reasoning: string) => {
    const previous = { provider: snapshot.provider, model: snapshot.model, reasoning: snapshot.reasoning };
    setSessionModel(snapshot.provider, snapshot.model, reasoning);
    persistSessionPreferences(snapshot.provider, snapshot.model, reasoning, previous);
  };
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
