import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { motion, useReducedMotion } from "motion/react";
import { ArrowDown } from "lucide-react";
import { cancelActive, execute, guide, importAttachment, importClipboardImage, startTurn } from "../bridge";
import { chatTypographyVars } from "../chatTypography";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";
import type { Attachment, DeliveryMode, QueuedPrompt, Snapshot } from "../types";
import { useTerminalStore } from "../terminalStore";
import { SelectActionHost } from "./SelectActionHost";
import { TimelineFeed } from "./Timeline";
import { Composer } from "./thread/Composer";
import { QueuedPrompts } from "./thread/QueueBar";
import { namedClipboardImage } from "./thread/clipboard";
import { parseSkillPrompt } from "./thread/slash";

const SESSION_STAGE_EASE = [0.16, 1, 0.3, 1] as const;

/** Paper between the last transcript card and the composer overlay. */
export const COMPOSER_OVERLAY_CLEARANCE = 24;
/** Fallback when the dock has not been measured — enough for the resting card. */
export const COMPOSER_OVERLAY_MIN_GAP = 148;

export function composerOverlayGap(dockHeight: number): number {
  const measured = Math.max(0, Math.ceil(dockHeight));
  if (measured <= 0) return COMPOSER_OVERLAY_MIN_GAP;
  return Math.max(COMPOSER_OVERLAY_MIN_GAP, measured + COMPOSER_OVERLAY_CLEARANCE);
}

export function transcriptFollowBehavior(running: boolean, sessionOpen = false): ScrollBehavior {
  return running || sessionOpen ? "instant" : "smooth";
}

export function pinTranscriptTail(viewport: HTMLElement, behavior: ScrollBehavior = "instant") {
  const top = Math.max(0, viewport.scrollHeight);
  // WKWebView ignores scrollTo({ behavior: "instant" }), so instant pins must
  // assign scrollTop. Otherwise session switch and send stay on the first line.
  if (behavior === "smooth") {
    viewport.scrollTo({ top, behavior: "smooth" });
    return;
  }
  viewport.scrollTop = top;
}

export function sessionStageMotion(reducedMotion: boolean) {
  if (reducedMotion) {
    return {
      initial: false as const,
      animate: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0 } },
    };
  }
  return {
    initial: { opacity: 0, y: 7, filter: "blur(2px)" },
    animate: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.24, ease: SESSION_STAGE_EASE } },
  };
}

export default function ThreadSurface() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const chatFontSize = useRuntimeStore((state) => state.chatFontSize);
  const chatCodeFontSize = useRuntimeStore((state) => state.chatCodeFontSize);
  const blocks = useRuntimeStore((state) => state.blocks);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId) || snapshot.sessionId;
  const running = useRuntimeStore((state) => state.running);
  const runId = useRuntimeStore((state) => state.runId);
  const globalRunSessionId = useRuntimeStore((state) => state.globalRunSessionId);
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
  const dock = useRef<HTMLDivElement>(null);
  const followingRef = useRef(following);
  const sessionFollow = useRef(currentSessionId);
  const pinInstant = useRef(false);
  const pinning = useRef(false);
  followingRef.current = following;
  if (sessionFollow.current !== currentSessionId) {
    sessionFollow.current = currentSessionId;
    pinInstant.current = true;
    followingRef.current = true;
    if (!following) setFollowing(true);
  }
  const reduceMotion = useReducedMotion();
  const t = translator(snapshot.language);
  const empty = blocks.length === 0 && !running;
  const runtimeBusy = running || Boolean(globalRunSessionId);
  const sessionMotion = sessionStageMotion(Boolean(reduceMotion));

  useLayoutEffect(() => {
    if (!following && !pinInstant.current) return;
    const node = viewport.current;
    if (!node) return;
    const pin = () => {
      if (!followingRef.current || viewport.current !== node) return;
      pinning.current = true;
      pinTranscriptTail(node, "instant");
      requestAnimationFrame(() => {
        pinning.current = false;
      });
    };
    // Opening a session remounts the stage at scrollTop 0. History turns start
    // at a 180px estimate, so pin instantly and again when height grows.
    pin();
    const transcript = node.querySelector(".transcript");
    if (!(transcript instanceof HTMLElement) || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      pin();
      pinInstant.current = false;
    });
    observer.observe(transcript);
    const retry = requestAnimationFrame(() => {
      pin();
      requestAnimationFrame(pin);
    });
    return () => {
      cancelAnimationFrame(retry);
      observer.disconnect();
    };
  }, [blocks, following, queuedPrompts.length, running, currentSessionId]);


  useEffect(() => setDeliveryMode(snapshot.queueMode ?? "queue"), [currentSessionId, snapshot.queueMode]);
  useEffect(() => {
    const composePlanFollowUp = (event: Event) => {
      const prefix = event instanceof CustomEvent && typeof event.detail?.prefix === "string" ? event.detail.prefix : "";
      setPlanMode(true);
      setPrompt(prefix);
      requestAnimationFrame(() => {
        const input = document.querySelector<HTMLTextAreaElement>("#azem-composer");
        input?.focus();
        if (input) input.setSelectionRange(input.value.length, input.value.length);
      });
    };
    window.addEventListener("azem:plan-compose", composePlanFollowUp);
    return () => window.removeEventListener("azem:plan-compose", composePlanFollowUp);
  }, [setPlanMode]);
  useEffect(() => {
    setPrompt("");
    clearAttachments();
    setEditingQueuedId(null);
  }, [clearAttachments, currentSessionId]);
  useLayoutEffect(() => {
    const node = dock.current;
    if (!node) return;
    const stage = node.closest<HTMLElement>(".thread-session-stage");
    if (!stage) return;
    const sync = () => {
      stage.style.setProperty("--transcript-bottom-gap", `${composerOverlayGap(node.offsetHeight)}px`);
    };
    sync();
    if (typeof ResizeObserver === "undefined") {
      return () => stage.style.removeProperty("--transcript-bottom-gap");
    }
    const observer = new ResizeObserver(sync);
    observer.observe(node);
    return () => {
      observer.disconnect();
      stage.style.removeProperty("--transcript-bottom-gap");
    };
  }, [empty]);
  const beginTurn = useTurnStarter(agentMode, planMode, setFollowing);
  useQueuedTurnRunner(
    queuedPrompts, runtimeBusy, queuePauseReason, editingQueuedId, beginTurn, removeQueuedPrompt, failQueuedPrompt,
  );

  const resetComposer = useCallback(() => {
    setPrompt("");
    clearAttachments();
    setEditingQueuedId(null);
  }, [clearAttachments]);

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

  const submitTurn = useCallback(async (
    text: string,
    images: Attachment[] = [],
    modeOverride?: DeliveryMode,
    source: "composer" | "select-action" = "composer",
  ) => {
    const trimmed = text.trim();
    if (!trimmed && images.length === 0) return;
    if (source === "composer" && editingQueuedId) {
      updateQueuedPrompt(currentSessionId, editingQueuedId, trimmed, images);
      resetComposer();
      setFollowing(true);
      return;
    }
    // The Go runtime admits one main run process-wide. A prompt composed in another
    // session while that run is active must remain queued until its terminal event.
    if (!runtimeBusy) {
      if (source === "composer") resetComposer();
      return void beginTurn(trimmed, images);
    }
    // Starting (the bridge has not returned runId yet), another session is active,
    // or Queue is selected: never attempt a concurrent turn.
    if (!running || !runId || (modeOverride ?? deliveryMode) === "queue") {
      if (source === "composer") resetComposer();
      enqueuePrompt(trimmed, images);
      setFollowing(true);
      return;
    }
    const sessionId = currentSessionId;
    if (source === "composer") resetComposer();
    await sendGuidance(sessionId, runId, trimmed, images, () => {
      if (!isCurrentSession(sessionId)) return;
      addOptimisticUser(trimmed, images);
      setFollowing(true);
    }, (message) => {
      if (!isCurrentSession(sessionId)) return;
      setError(message);
      if (source === "composer") {
        setPrompt((current) => current || trimmed);
        if (useRuntimeStore.getState().attachments.length === 0) replaceAttachments(images);
      }
    });
  }, [
    addOptimisticUser, beginTurn, currentSessionId, deliveryMode, editingQueuedId,
    enqueuePrompt, replaceAttachments, resetComposer, runId, running, runtimeBusy,
    setError, updateQueuedPrompt,
  ]);

  const submit = async (modeOverride?: DeliveryMode) => {
    await submitTurn(prompt.trim(), [...attachments], modeOverride, "composer");
  };

  const sendSelectAction = useCallback((text: string) => {
    void submitTurn(text, [], undefined, "select-action");
  }, [submitTurn]);

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
      await cancelActive(true);
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
    <section className={`thread-surface ${empty ? "empty-thread" : "active-thread"}`} style={chatTypographyVars(chatFontSize, chatCodeFontSize) as CSSProperties}>
      <ThreadHeader empty={empty} />
      <div className="thread-session-viewport">
        <motion.div
          key={currentSessionId}
          className="thread-session-stage"
          initial={sessionMotion.initial}
          animate={sessionMotion.animate}
        >
            {empty ? (
              <div className="empty-composer-wrap">
                <div className="empty-composer-heading"><h1>{t("promptTitle")}</h1><p>{t("promptSubtitle")}</p></div>
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
                  <div className="empty-task-suggestions" aria-label={t("taskSuggestions")}>
                    {emptySuggestions(snapshot.language).map((suggestion) => <button type="button" key={suggestion.title} onClick={() => {
                      setPrompt(suggestion.prompt);
                      requestAnimationFrame(() => document.querySelector<HTMLTextAreaElement>("#azem-composer")?.focus());
                    }}><strong>{suggestion.title}</strong><span>{suggestion.detail}</span></button>)}
                  </div>
                </div>
              </div>
            ) : (
              <>
                <div className="transcript-viewport" ref={viewport} onScroll={(event) => {
                  if (pinning.current) return;
                  const node = event.currentTarget;
                  setFollowing(node.scrollHeight - node.scrollTop - node.clientHeight < 72);
                }}>

                  <div className="transcript">
                    <TimelineFeed
                      blocks={blocks}
                      language={snapshot.language}
                      activeRunId={runId}
                      running={running}
                      waitingForModel={running}
                      collapseCompletedProcess
                    />
                    {error && <div className="inline-error" role="alert">{error}</div>}
                    <SelectActionHost
                      rootRef={viewport}
                      composerRef={dock}
                      language={snapshot.language}
                      onSubmit={sendSelectAction}
                    />
                    <div className="transcript-composer-clearance" aria-hidden="true" />
                  </div>
                </div>
                <div className="composer-dock" ref={dock}>
                  {!following && <button className="jump-latest" aria-label={t("jumpLatest")} onClick={() => setFollowing(true)}><ArrowDown size={16} /></button>}
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
        </motion.div>
      </div>
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

function ThreadHeader({ empty }: { empty: boolean }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const title = useRuntimeStore((state) => state.currentTitle);
  const running = useRuntimeStore((state) => state.running);
  const branches = useRuntimeStore((state) => state.branches);
  const t = translator(snapshot.language);
  if (empty) return null;
  const heading = title || t("newSession");
  const status = headerStatus(running, t);
  const stage = threadHeaderStage(running);
  const branch = branches.find((item) => item.current)?.name || snapshot.currentBranch || t("noBranches");
  const projectName = snapshot.workspace.split(/[\\/]/).filter(Boolean).at(-1) || t("workingTree");
  return <header className="thread-header titlebar-region">
    <div className="thread-heading-copy"><span className="thread-eyebrow">{heading.includes("UI") ? "DESIGN TASK" : "TASK"}</span><strong>{heading}</strong><small>{projectName} · {branch}</small></div>
    <div className="thread-stage" role="status" aria-label={status}>
      <span data-active={String(stage === "in-progress")}>{t("inProgress")}</span>
      <span data-active={String(stage === "completed")}>{t("completed")}</span>
    </div>
    <div className="thread-header-end">
      <span className="thread-runtime-status" data-running={String(running)}>{status}</span>
      <HeaderActions empty={empty} />
    </div>
  </header>;
}

export function threadHeaderStage(running: boolean): "in-progress" | "completed" {
  return running ? "in-progress" : "completed";
}

function headerStatus(running: boolean, t: ReturnType<typeof translator>) { return running ? t("running") : t("ready"); }

function HeaderActions({ empty }: { empty: boolean }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const inspectorOpen = useRuntimeStore((state) => state.inspectorOpen);
  const setInspectorOpen = useRuntimeStore((state) => state.setInspectorOpen);
  const terminalOpen = useTerminalStore((state) => state.open);
  const t = translator(snapshot.language);
  return <div className="thread-actions">
    <button hidden={empty} type="button" className="square-button terminal-toggle" data-open={String(terminalOpen)} aria-pressed={terminalOpen} title={t("toggleTerminal")} onClick={() => useTerminalStore.getState().toggle()}>{t("terminal")}</button>
    <button hidden={empty} className="square-button inspector-toggle" data-open={String(inspectorOpen)} aria-label={t("inspector")} onClick={() => setInspectorOpen(!inspectorOpen)}>{snapshot.language === "zh-CN" ? "侧栏" : "Panel"}</button>
  </div>;
}

function emptySuggestions(language: Snapshot["language"]) {
  return language === "zh-CN" ? [
    { title: "检查代码改动", detail: "查看当前工作树的风险", prompt: "分析当前项目的代码改动，按风险高低给出结论" },
    { title: "修复失败检查", detail: "定位测试与 CI 问题", prompt: "定位当前项目失败的测试或 CI 检查，并修复根因" },
    { title: "制定实现计划", detail: "拆分步骤与验收标准", prompt: "理解当前需求并整理一份可以直接执行的实现计划" },
  ] : [
    { title: "Review changes", detail: "Assess working tree risk", prompt: "Review the current project changes and rank findings by risk." },
    { title: "Fix checks", detail: "Trace test and CI failures", prompt: "Find the failed tests or CI checks and fix the root cause." },
    { title: "Build a plan", detail: "Define steps and acceptance", prompt: "Turn the current requirement into an executable implementation plan." },
  ];
}
