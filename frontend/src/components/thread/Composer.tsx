import { useCallback, useEffect, useRef, useState } from "react";
import {
  Check, ChevronDown, Folder, GitBranch, HardDrive, Lightbulb, Plus, Search, WandSparkles,
} from "lucide-react";
import { execute, openProject, selectProjectFolder } from "../../bridge";
import { contextCategoryLabel, contextComposition, contextOccupancy } from "../../contextUsage";
import { tFormat, translator, type Language, type MessageKey } from "../../i18n";
import { useRuntimeStore } from "../../store";
import type { DeliveryMode } from "../../types";
import AttachmentPreview from "../AttachmentPreview";
import ComposerModelPicker from "../ComposerModelPicker";
import { ApprovalPicker } from "./ApprovalPicker";
import { namedClipboardImage, pastedImages, shouldReadNativeClipboard } from "./clipboard";
import { effectiveComposerRoute, useComposerModels } from "./composerModels";
import { parseSkillPrompt, slashSuggestions, type SlashSuggestion } from "./slash";
import {
  Composer as ComposerElement,
  ComposerAttachButton,
  ComposerAttachments,
  ComposerBar,
  ComposerContext,
  ComposerMenu,
  ComposerSend,
  ComposerTextarea,
  ComposerToolbar,
  type ComposerUsage,
} from "../elements/composer";

export function composerPromptPlaceholder(
  t: (key: MessageKey) => string,
  options: { busy: boolean; running: boolean; deliveryMode: DeliveryMode; showContextBar: boolean; language: Language },
) {
  if (options.busy && options.running) {
    return options.deliveryMode === "guide" ? t("guidePlaceholder") : t("runningPlaceholder");
  }
  if (options.busy) return t("queuePlaceholder");
  if (options.showContextBar) {
    return options.language === "zh-CN"
      ? "描述要完成的任务，@ 引用文件，/ 使用技能…"
      : "Describe a task, @ reference files, or / use skills…";
  }
  return options.language === "zh-CN" ? "继续描述你想调整的界面…" : "Continue describing what you want to adjust…";
}

export function branchMenuLayout(
  trigger: { top: number; bottom: number },
  viewportHeight: number,
  edgeInset = 12,
  gap = 8,
): { placement: "above" | "below"; maxHeight: number } {
  const above = Math.max(0, Math.min(trigger.top, viewportHeight) - edgeInset - gap);
  const below = Math.max(0, viewportHeight - Math.max(trigger.bottom, 0) - edgeInset - gap);
  const placement = above >= below ? "above" : "below";
  return { placement, maxHeight: Math.floor(Math.min(420, placement === "above" ? above : below)) };
}

const approvalCycle = ["prompt", "auto_review", "yolo"] as const;

export function Composer({ prompt, setPrompt, submit, attach, attachClipboard, agentMode, setAgentMode, planMode, setPlanMode, running, busy, cancel, deliveryMode, showContextBar = false }: {
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
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
  const slashMenu = useRef<HTMLDivElement>(null);
  const attachmentInput = useRef<HTMLInputElement>(null);
  const imeEndedAt = useRef(-Infinity);
  const [slashCursor, setSlashCursor] = useState(0);
  const [slashDismissed, setSlashDismissed] = useState(false);
  const t = translator(snapshot.language);
  const composerRoute = effectiveComposerRoute(snapshot, planMode, modelRoutes);
  const { modelChoices, reasoningLevels, selectedModel, selectedModelName, selectedReasoning, fast, fastAvailable, changeModel, changeReasoning, changeSpeed } = useComposerModels(snapshot, composerRoute, planMode ? "plan" : "");
  const reasoningNames: Record<string, string> = {
    default: snapshot.language === "zh-CN" ? "默认" : "Default",
    none: snapshot.language === "zh-CN" ? "无思考" : "No reasoning",
    minimal: t("reasoningMinimal"), low: t("reasoningLow"), medium: t("reasoningMedium"),
    high: t("reasoningHigh"), xhigh: t("reasoningXHigh"), max: t("reasoningMax"), ultra: t("reasoningUltra"),
  };
  const approvalLabels: Record<string, string> = {
    prompt: t("promptApproval"), auto_review: t("autoReview"), yolo: t("yolo"),
  };
  const cursorSelected = composerRoute.provider === "cursor";
  const selectedReasoningName = [
    reasoningNames[selectedReasoning] ?? selectedReasoning,
    fast ? "Fast" : "",
  ].filter(Boolean).join(" · ");
  const fastModeTitle = cursorSelected ? "Cursor Fast" : t("fastBoostTitle");
  const fastModeDetail = cursorSelected
    ? (snapshot.language === "zh-CN" ? "切换到当前档位对应的 Fast 变体" : "Use the matching Fast variant for this tier")
    : t("fastBoostDetail");
  const contextPercent = contextOccupancy(contextUsage, contextProfile).percentage;
  const assistantContextUsage = composerContextUsage(contextUsage, contextProfile, snapshot.language);
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
    fastAvailable,
    contextPercent,
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
    const index = Math.max(0, reasoningLevels.indexOf(composerRoute.reasoning));
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
    <ComposerElement className="composer-shell max-w-none">
      <ComposerBar className="composer-card gap-0 p-0">
        {showContextBar ? <ComposerContextBar /> : null}
        {slashOpen && <ComposerMenu open id="slash-menu" ref={slashMenu} className="slash-menu" role="listbox" aria-label={t("slashCommands")}>
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
        </ComposerMenu>}
        {selectedSkill && <div className="composer-skill"><WandSparkles size={17} aria-hidden="true" /><span>{selectedSkill.name}</span></div>}
        {attachments.length > 0 && <ComposerAttachments className="attachment-row">{attachments.map((item) => <AttachmentPreview key={item.id} attachment={item} sessionId={currentSessionId} language={snapshot.language} variant="composer" onRemove={() => removeAttachment(item.id)} />)}</ComposerAttachments>}
        <ComposerTextarea id="azem-composer" value={visiblePrompt} onChange={(event) => setPrompt(skillPrefix + event.target.value)} onPaste={(event) => {
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
          placeholder={composerPromptPlaceholder(t, { busy, running, deliveryMode, showContextBar, language: snapshot.language })} rows={2} onCompositionEnd={(event) => { imeEndedAt.current = event.timeStamp; }} onKeyDown={(event) => {
          // WebKit/WKWebView 下选字确认的回车，keydown 在 compositionend 之后触发且 isComposing 已复位，
          // 只能按时间窗补判：紧随组合结束的这一次 Enter 属于同一次按键，人手连按远慢于此间隔。
          if (event.nativeEvent.isComposing || event.timeStamp - imeEndedAt.current < 100) return;
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
        <ComposerToolbar className="composer-toolbar">
          <ComposerAttachButton onClick={() => attachmentInput.current?.click()} aria-label={t("attach")} />
          <input ref={attachmentInput} type="file" accept="image/*" multiple hidden onChange={(event) => { attach(event.target.files); event.target.value = ""; }} />
          <ApprovalPicker value={approvalMode} disabled={running} language={snapshot.language} onChange={(mode) => void execute({ kind: "set_approval_mode", target: mode }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)))} />
          <button
            type="button"
            className="plan-mode-toggle"
            data-active={String(planMode)}
            disabled={running}
            aria-pressed={planMode}
            aria-label={t("plan")}
            onClick={() => setPlanMode(!planMode)}
          >
            <Lightbulb size={15} />
            <span>{t("planLabel")}</span>
          </button>
          <span className="toolbar-spacer" />
          <ComposerContext usage={assistantContextUsage} label={t("contextComposition")} totalLabel={t("quotaTotal")} className="composer-context-element" />
          <ComposerModelPicker
            running={running} models={modelChoices} selectedModel={selectedModel} selectedModelName={selectedModelName} selectedProvider={composerRoute.provider}
            reasoningLevels={reasoningLevels} selectedReasoning={selectedReasoning} selectedReasoningName={selectedReasoningName}
            fast={fast} fastAvailable={fastAvailable} reasoningNames={reasoningNames} onModelChange={changeModel} onReasoningChange={changeReasoning} onSpeedChange={changeSpeed}
            fasterLabel={t("reasoningFaster")} smarterLabel={t("reasoningSmarter")}
            highCostHint={t("reasoningMaxHint")} fastBoostTitle={fastModeTitle} fastBoostDetail={fastModeDetail} language={snapshot.language}
          />
          <ComposerSend
            streaming={showCancel}
            idle={!prompt.trim() && attachments.length === 0}
            className={showCancel ? "cancel-button" : "send-button"}
            data-cancel-run={showCancel || undefined}
            onClick={showCancel ? cancel : () => submitOrChooseSlash()}
            disabled={!showCancel && !prompt.trim() && attachments.length === 0}
            aria-label={showCancel ? t("cancel") : busy ? running && deliveryMode === "guide" ? t("guide") : t("queue") : t("send")}
          />
        </ComposerToolbar>
      </ComposerBar>
    </ComposerElement>
  );
}

export function composerContextUsage(
  usage: Parameters<typeof contextComposition>[0],
  profile: Parameters<typeof contextComposition>[1],
  language: Language,
): ComposerUsage {
  const occupancy = contextOccupancy(usage, profile);
  const composition = contextComposition(usage, profile);
  return {
    segments: composition.groups.map((group, index) => ({
      key: group.category,
      label: contextCategoryLabel(group.category, language),
      tokens: group.tokens,
      tone: group.category === "current_output" ? "secondary" : index === 0 ? "primary" : "tertiary",
    })),
    total: occupancy.limit,
  };
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
  const [branchLayout, setBranchLayout] = useState<ReturnType<typeof branchMenuLayout>>({ placement: "above", maxHeight: 420 });
  const branchMenu = useRef<HTMLDetailsElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const createRef = useRef<HTMLInputElement>(null);

  const updateBranchLayout = useCallback(() => {
    const node = branchMenu.current;
    const summary = node?.querySelector(":scope > summary");
    if (!node?.open || !(summary instanceof HTMLElement)) return;
    const rect = summary.getBoundingClientRect();
    setBranchLayout(branchMenuLayout(rect, window.visualViewport?.height ?? window.innerHeight));
  }, []);

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
        updateBranchLayout();
        requestAnimationFrame(() => {
          updateBranchLayout();
          searchRef.current?.focus();
        });
      } else {
        setQuery("");
        setCreating(false);
        setNewBranch("");
      }
    };
    node.addEventListener("toggle", onToggle);
    return () => node.removeEventListener("toggle", onToggle);
  }, [updateBranchLayout]);

  useEffect(() => {
    window.addEventListener("resize", updateBranchLayout);
    window.visualViewport?.addEventListener("resize", updateBranchLayout);
    return () => {
      window.removeEventListener("resize", updateBranchLayout);
      window.visualViewport?.removeEventListener("resize", updateBranchLayout);
    };
  }, [updateBranchLayout]);

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
      <button type="button" className="composer-chip composer-chip-action" aria-label={`${t("switchProject")}: ${project}`} disabled={busy} onClick={() => void switchProject()}>
        <Folder size={13} />
        <span>{project}</span>
      </button>
      <span className="composer-chip">
        <HardDrive size={13} />
        <span>{t("local")}</span>
      </span>
      {branches.length > 0 ? (
        <details ref={branchMenu} className="composer-branch-menu">
          <summary className="composer-chip composer-chip-action">
            <GitBranch size={13} />
            <span>{currentBranch || t("branch")}</span>
            <ChevronDown size={11} />
          </summary>
          <div
            className="composer-branch-panel"
            data-placement={branchLayout.placement}
            style={{ maxHeight: branchLayout.maxHeight }}
            role="listbox"
            aria-label={t("branch")}
          >
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
        <span className="composer-chip composer-chip-muted">
          <GitBranch size={13} />
          <span>{t("noBranches")}</span>
        </span>
      )}
    </div>
  );
}

function cancelVisible(running: boolean, cancel: (() => void) | undefined, prompt: string, attachmentCount = 0) {
  return running && Boolean(cancel) && !prompt.trim() && attachmentCount === 0;
}
