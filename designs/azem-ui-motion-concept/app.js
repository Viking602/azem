(() => {
  const root = document.documentElement;
  const workspace = document.querySelector(".workspace");
  const navItems = [...document.querySelectorAll("[data-view]")];
  const viewPanels = [...document.querySelectorAll("[data-view-panel]")];
  const threadStageTabs = [...document.querySelectorAll("[data-thread-stage-target]")];
  const threadStagePanels = [...document.querySelectorAll("[data-thread-stage-panel]")];
  const threadStatus = document.querySelector("[data-thread-status]");
  const threadStatusLabel = document.querySelector("[data-thread-status-label]");
  const transcriptViewport = document.querySelector(".transcript-viewport");
  const contextValue = document.querySelector("[data-context-value]");
  const contextDetail = document.querySelector("[data-context-detail]");
  const planHeading = document.querySelector("[data-plan-heading]");
  const planCount = document.querySelector("[data-plan-count]");
  const planProgressBar = document.querySelector("[data-plan-progress-bar]");
  const planList = document.querySelector("[data-plan-list]");
  const commandBackdrop = document.querySelector("[data-command-backdrop]");
  const commandInput = document.querySelector("[data-command-input]");
  const motionSheet = document.querySelector("[data-motion-sheet]");
  const settingsBackdrop = document.querySelector("[data-settings-backdrop]");
  const settingsSearch = document.querySelector("[data-settings-search]");
  const providerSearch = document.querySelector("[data-provider-search]");
  const providerEntries = [...document.querySelectorAll("[data-provider-entry]")];
  const providerEmpty = document.querySelector("[data-provider-empty]");
  const customSelects = [...document.querySelectorAll("[data-custom-select]")];
  const composerTextarea = document.querySelector(".composer-card textarea");
  const composerModelSearch = document.querySelector("[data-model-search]");
  const fastControl = document.querySelector("[data-fast-control]");
  const fastToggle = document.querySelector("[data-fast-toggle]");
  const fastIndicator = document.querySelector("[data-fast-indicator]");
  const routeModelPopover = document.querySelector("[data-route-model-popover]");
  const routeModelSearch = document.querySelector("[data-route-model-search]");
  const routeModelOptions = [...document.querySelectorAll("[data-route-model-option]")];
  const settingsScroll = document.querySelector("[data-settings-scroll]");
  const projectAddToggle = document.querySelector("[data-project-add-toggle]");
  const projectAddPopover = document.querySelector("[data-project-add-popover]");
  const projectCreateBackdrop = document.querySelector("[data-project-create-backdrop]");
  const projectSwitch = document.querySelector("[data-project-switch]");
  const projectSwitchPopover = document.querySelector("[data-project-switch-popover]");
  const projectSwitchSearch = document.querySelector("[data-project-switch-search]");
  const projectSwitchOptions = [...document.querySelectorAll("[data-project-switch-option]")];
  const projectSwitchEmpty = document.querySelector("[data-project-switch-empty]");
  const threadLayer = document.querySelector("[data-view-panel='thread']");
  const newThreadBlank = document.querySelector("[data-new-thread-blank]");
  const homeProjectTrigger = document.querySelector("[data-home-project-trigger]");
  const homeProjectMenu = document.querySelector("[data-home-project-menu]");
  const homeTaskInput = document.querySelector("[data-home-task-input]");
  const homeTaskSubmit = document.querySelector("[data-home-task-submit]");
  const homeFastToggle = document.querySelector("[data-home-fast-toggle]");
  const toast = document.querySelector("[data-toast-output]");
  const streamOutput = document.querySelector("[data-stream-output]");
  const streamAnnouncement = document.querySelector("[data-stream-announcement]");
  const assistantMessage = streamOutput.closest(".assistant-message");
  const toolCard = document.querySelector(".tool-card");
  const toolLabel = toolCard.querySelector(".tool-state-label");
  const toolProgress = toolCard.querySelector(".tool-progress span");
  let streamTimers = [];
  let toastTimer = 0;
  let toolTimer = 0;
  let elapsed = 4.6;
  let activeRouteTrigger = null;
  let fastTipTimer = 0;
  let activeThreadOutput = null;
  let homeSelectedProject = "azem";
  let lastConversationView = "thread";

  const streamChunks = [
    "建议把 Azem 的视觉方向定义为“静谧机械感”。",
    "保留暖白纸面和橙色品牌点，",
    "把导航、检查器和运行状态组织成连续的空间层。\n\n",
    "页面切换使用 320ms 的轻微景深过渡；",
    "工具状态使用 180–240ms 的轨迹推进；",
    "流式文字让每个新字符从模糊、透明和轻微下移中逐字浮现，",
    "旧文本立即稳定，避免整段闪动。"
  ];

  activeThreadOutput = streamChunks;

  const threadStageData = {
    thinking: {
      status: "thinking",
      statusLabel: "思考中",
      contextValue: "31%",
      contextDetail: "51k / 164k",
      planHeading: "思考路线",
      planCount: "1 / 3",
      progress: "34%",
      planItems: [
        { state: "done", marker: "✓", label: "识别产品主路径" },
        { state: "current", marker: "", label: "整理任务生命周期", detail: "正在推演" },
        { state: "", marker: "3", label: "匹配组件与事件" },
      ],
    },
    executing: {
      status: "running",
      statusLabel: "运行中",
      contextValue: "38%",
      contextDetail: "62k / 164k",
      planHeading: "执行计划",
      planCount: "2 / 4",
      progress: "56%",
      planItems: [
        { state: "done", marker: "✓", label: "分析当前界面与事件链" },
        { state: "done", marker: "✓", label: "建立视觉与动效 token" },
        { state: "current", marker: "", label: "制作完整交互页面", detail: "正在执行" },
        { state: "", marker: "4", label: "验证性能与减弱动效" },
      ],
    },
    completed: {
      status: "completed",
      statusLabel: "已完成",
      contextValue: "41%",
      contextDetail: "67k / 164k",
      planHeading: "交付清单",
      planCount: "4 / 4",
      progress: "100%",
      planItems: [
        { state: "done", marker: "✓", label: "分析当前界面与事件链" },
        { state: "done", marker: "✓", label: "建立视觉与动效 token" },
        { state: "done", marker: "✓", label: "完成全部页面与状态" },
        { state: "done", marker: "✓", label: "浏览器与架构验证通过" },
      ],
    },
  };

  const threadData = {
    motion: {
      title: "优化 Azem 的 UI 动效",
      eyebrow: "DESIGN TASK",
      output: streamChunks,
      stage: "executing",
    },
    plugins: {
      title: "Codex 插件兼容设计",
      eyebrow: "EXTENSIONS",
      output: ["插件界面需要优先表达三种状态：", "已接入、部分接入和待授权。", "状态变化沿同一条能力轨迹推进，", "不使用突兀的整页刷新。"],
      stage: "completed",
    },
    context: {
      title: "语义上下文重建",
      eyebrow: "CONTEXT KERNEL",
      output: ["上下文内核适合使用低频、可读的状态动效。", "语义版本更新时只推进版本号和环形轨迹，", "正文保持稳定，", "避免持续旋转干扰阅读。"],
      stage: "completed",
    },
    release: {
      title: "发布 v0.2.4",
      eyebrow: "LLMUX RELEASE",
      output: ["发布任务已关联 llmux 项目的 PR #45。", "当前有两项检查失败，", "项目列表会保留失败提示，", "让多个项目并行操作时不丢失远端状态。"],
      stage: "thinking",
    },
    scheduler: {
      title: "调度器并发验证",
      eyebrow: "VENAT RUNTIME",
      output: ["Venat 项目的调度验证已经完成。", "折叠项目时仍保留 PR 数量提示，", "展开后再显示具体 PR 与会话，", "避免侧栏在多项目下失控增长。"],
      stage: "completed",
    },
  };

  const filePreviews = {
    "Timeline.tsx": `<span class="ln">462</span>return &lt;div className="streaming-text"&gt;
<span class="ln">463</span>  {reduceMotion ? text : tailCharacters.map(
<span class="ln hot">464</span>    (character) =&gt; &lt;motion.span
<span class="ln hot">465</span>      initial={{ opacity: 0, filter: "blur(4px)", y: 6 }}
<span class="ln hot">466</span>      animate={{ opacity: 1, filter: "blur(0px)", y: 0 }}
<span class="ln">467</span>      transition={characterReveal}
<span class="ln">468</span>    &gt;{character.text}&lt;/motion.span&gt;
<span class="ln">469</span>  ).slice(-8)}
<span class="ln">470</span>&lt;/div&gt;;`,
    "styles.css": `<span class="ln">31</span>--motion-fast: 170ms;
<span class="ln hot">32</span>--motion-base: 240ms;
<span class="ln hot">33</span>--motion-scene: 320ms;
<span class="ln">34</span>--ease-out: cubic-bezier(.22, 1, .36, 1);
<span class="ln">35</span>--ease-emphasis: cubic-bezier(.16, 1, .3, 1);`,
    "App.tsx": `<span class="ln">248</span>&lt;AnimatePresence initial={false} mode="popLayout"&gt;
<span class="ln hot">249</span>  &lt;motion.main key={view} variants={sceneMotion}&gt;
<span class="ln">250</span>    {view === "thread" ? &lt;ThreadSurface /&gt; : &lt;Pages /&gt;}
<span class="ln">251</span>  &lt;/motion.main&gt;
<span class="ln">252</span>&lt;/AnimatePresence&gt;`,
  };

  function clearTimers() {
    streamTimers.forEach((timer) => window.clearTimeout(timer));
    streamTimers = [];
    window.clearTimeout(toolTimer);
  }

  function setToolRunning() {
    toolCard.dataset.toolState = "running";
    toolLabel.innerHTML = "<i></i>运行中";
    toolLabel.style.color = "var(--blue)";
    toolProgress.style.animation = "none";
    toolProgress.style.width = "12%";
    toolProgress.style.background = "var(--blue)";
    requestAnimationFrame(() => {
      toolProgress.style.animation = "tool-progress 4.2s var(--ease-out) forwards";
      toolProgress.style.width = "";
    });
    toolTimer = window.setTimeout(() => {
      toolCard.dataset.toolState = "completed";
      toolLabel.innerHTML = "✓ 已完成";
      toolLabel.style.color = "var(--green)";
      toolProgress.style.animation = "none";
      toolProgress.style.width = "100%";
      toolProgress.style.background = "var(--green)";
    }, 4200);
  }

  function playStream(chunks = streamChunks) {
    clearTimers();
    streamOutput.replaceChildren();
    streamAnnouncement.textContent = "";
    assistantMessage.classList.remove("stream-complete");
    setToolRunning();
    const text = chunks.join("");
    const reduced = root.dataset.motion === "reduced" || window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      streamOutput.textContent = text;
      streamAnnouncement.textContent = text;
      assistantMessage.classList.add("stream-complete");
      return;
    }

    const characters = Array.from(text);
    const stableText = document.createTextNode("");
    const animatedTail = [];
    const viewport = document.querySelector(".transcript-viewport");
    streamOutput.append(stableText);

    const settleOldestCharacter = () => {
      const oldest = animatedTail.shift();
      if (!oldest) return;
      stableText.data += oldest.dataset.character;
      oldest.remove();
    };

    const finishStream = () => {
      while (animatedTail.length) settleOldestCharacter();
      assistantMessage.classList.add("stream-complete");
      streamAnnouncement.textContent = text;
    };

    const delayForCharacter = (character) => {
      if (character === "\n") return 105;
      if (/[。！？!?]/u.test(character)) return 88;
      if (/[，、；：,;:]/u.test(character)) return 52;
      if (/\s/u.test(character)) return 18;
      return 30;
    };

    let index = 0;
    const revealNextCharacter = () => {
      const character = characters[index];
      const span = document.createElement("span");
      span.className = character === "\n" ? "stream-character stream-break" : "stream-character";
      span.dataset.character = character;
      span.textContent = character;
      animatedTail.push(span);
      streamOutput.append(span);
      while (animatedTail.length > 8) settleOldestCharacter();
      viewport.scrollTo({ top: viewport.scrollHeight, behavior: "smooth" });

      index += 1;
      if (index >= characters.length) {
        const finishTimer = window.setTimeout(finishStream, 300);
        streamTimers.push(finishTimer);
        return;
      }

      const nextTimer = window.setTimeout(revealNextCharacter, delayForCharacter(character));
      streamTimers.push(nextTimer);
    };

    const startTimer = window.setTimeout(revealNextCharacter, 140);
    streamTimers.push(startTimer);
  }

  function renderPlanItems(items) {
    planList.replaceChildren(...items.map((item) => {
      const row = document.createElement("li");
      if (item.state) row.className = item.state;
      const marker = document.createElement("span");
      marker.textContent = item.marker;
      const copy = document.createElement("p");
      copy.textContent = item.label;
      if (item.detail) {
        const detail = document.createElement("small");
        detail.textContent = item.detail;
        copy.append(detail);
      }
      row.append(marker, copy);
      return row;
    }));
  }

  function switchThreadStage(stage, replay = true) {
    const next = threadStagePanels.find((panel) => panel.dataset.threadStagePanel === stage);
    const current = threadStagePanels.find((panel) => panel.classList.contains("active"));
    const state = threadStageData[stage];
    if (!next || !state) return;

    if (current !== next) {
      const reduced = root.dataset.motion === "reduced";
      current?.classList.add("leaving");
      next.classList.add("active");
      next.removeAttribute("inert");
      next.setAttribute("aria-hidden", "false");
      window.setTimeout(() => {
        current?.classList.remove("active", "leaving");
        if (current) {
          current.setAttribute("inert", "");
          current.setAttribute("aria-hidden", "true");
        }
      }, reduced ? 0 : 190);
    }

    threadStageTabs.forEach((tab) => {
      const selected = tab.dataset.threadStageTarget === stage;
      tab.classList.toggle("active", selected);
      tab.setAttribute("aria-selected", String(selected));
    });
    threadStatus.dataset.threadStatus = state.status;
    threadStatusLabel.textContent = state.statusLabel;
    contextValue.textContent = state.contextValue;
    contextDetail.textContent = state.contextDetail;
    planHeading.textContent = state.planHeading;
    planCount.textContent = state.planCount;
    planProgressBar.style.width = state.progress;
    renderPlanItems(state.planItems);
    transcriptViewport.scrollTop = 0;

    if (stage === "executing" && replay) playStream(activeThreadOutput);
    else if (stage !== "executing") clearTimers();
  }

  function showToast(message) {
    window.clearTimeout(toastTimer);
    toast.textContent = message;
    toast.classList.add("show");
    toastTimer = window.setTimeout(() => toast.classList.remove("show"), 1600);
  }

  function switchView(name) {
    const current = viewPanels.find((panel) => panel.classList.contains("active"));
    const next = viewPanels.find((panel) => panel.dataset.viewPanel === name);
    if (!next || current === next) return;
    const reduced = root.dataset.motion === "reduced";
    current?.classList.add("leaving");
    next.classList.add("active");
    next.removeAttribute("inert");
    next.setAttribute("aria-hidden", "false");
    window.setTimeout(() => {
      current?.classList.remove("active", "leaving");
      if (current) {
        current.setAttribute("inert", "");
        current.setAttribute("aria-hidden", "true");
      }
    }, reduced ? 0 : 230);
    navItems.forEach((item) => {
      item.classList.toggle("active", item.dataset.view === name);
    });
    const workspaceMode = ["workspace", "files", "changes"].includes(name);
    const modeSwitcher = document.querySelector(".mode-switch");
    modeSwitcher.dataset.mode = workspaceMode ? "workspace" : "chat";
    modeSwitcher.querySelectorAll("[data-mode-target]").forEach((button) => {
      button.classList.toggle("active", button.dataset.modeTarget === (workspaceMode ? "workspace" : "chat"));
    });
    if (!workspaceMode) lastConversationView = name;
    if (workspaceMode) workspace.dataset.inspector = "closed";
    if (name === "home") {
      workspace.dataset.inspector = "closed";
      const activeProjectName = document.querySelector("[data-project-node].active")?.dataset.projectNode || homeSelectedProject;
      const activeOption = document.querySelector(`[data-home-project-option="${activeProjectName}"]`);
      if (activeOption) selectHomeProject(activeOption, false);
    } else {
      toggleHomeProjectMenu(false);
    }
  }

  function openCommand() {
    commandBackdrop.hidden = false;
    requestAnimationFrame(() => commandInput.focus());
  }

  function closeCommand() {
    commandBackdrop.hidden = true;
    commandInput.value = "";
  }

  function openMotionSheet() {
    motionSheet.classList.add("open");
    motionSheet.removeAttribute("inert");
    motionSheet.setAttribute("aria-hidden", "false");
  }

  function closeMotionSheet() {
    motionSheet.classList.remove("open");
    motionSheet.setAttribute("inert", "");
    motionSheet.setAttribute("aria-hidden", "true");
  }

  function openSettings(section = "catalog") {
    closeCommand();
    closeMotionSheet();
    activateSettingsSection(section);
    settingsBackdrop.hidden = false;
    requestAnimationFrame(() => settingsSearch.focus());
  }

  function closeSettings() {
    closeRouteModelPicker();
    settingsBackdrop.hidden = true;
    settingsSearch.value = "";
    document.querySelectorAll("[data-settings-section]").forEach((button) => { button.hidden = false; });
  }

  function activateSettingsSection(section) {
    closeRouteModelPicker();
    const button = document.querySelector(`[data-settings-section="${section}"]`);
    const page = document.querySelector(`[data-settings-page="${section}"]`);
    if (!button || !page) return;
    document.querySelectorAll("[data-settings-section]").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll("[data-settings-page]").forEach((item) => item.classList.toggle("active", item === page));
    document.querySelector("[data-settings-scroll]").scrollTop = 0;
  }

  function toggleProjectMenu(force) {
    const shouldOpen = typeof force === "boolean" ? force : projectAddPopover.hidden;
    projectAddPopover.hidden = !shouldOpen;
    projectAddToggle.setAttribute("aria-expanded", String(shouldOpen));
  }

  function filterProjectSwitchOptions(query = "") {
    const normalized = query.trim().toLowerCase();
    let visibleCount = 0;
    projectSwitchOptions.forEach((option) => {
      option.hidden = Boolean(normalized) && !option.textContent.toLowerCase().includes(normalized);
      if (!option.hidden) visibleCount += 1;
    });
    projectSwitchEmpty.hidden = visibleCount > 0;
  }

  function toggleProjectSwitchPopover(force) {
    const shouldOpen = typeof force === "boolean" ? force : projectSwitchPopover.hidden;
    projectSwitchPopover.hidden = !shouldOpen;
    projectSwitch.setAttribute("aria-expanded", String(shouldOpen));
    if (!shouldOpen) return;
    toggleProjectMenu(false);
    toggleHomeProjectMenu(false);
    projectSwitchSearch.value = "";
    filterProjectSwitchOptions();
    requestAnimationFrame(() => projectSwitchSearch.focus({ preventScroll: true }));
  }

  function activateProject(projectName, expand = true) {
    const project = document.querySelector(`[data-project-node="${projectName}"]`);
    if (!project) return null;
    document.querySelectorAll("[data-project-node]").forEach((item) => item.classList.toggle("active", item === project));
    if (expand) {
      project.dataset.expanded = "true";
      project.querySelector(".project-chevron").textContent = "⌄";
    }
    const branch = project.querySelector(".project-identity small").textContent.trim().split("·")[0].trim();
    document.querySelector("[data-project-switch-name]").textContent = projectName;
    document.querySelector("[data-project-switch-branch]").textContent = branch;
    document.querySelector("[data-project-switch-branch]").title = branch;
    projectSwitchOptions.forEach((option) => {
      const selected = option.dataset.projectSwitchOption === projectName;
      option.classList.toggle("selected", selected);
      option.setAttribute("aria-selected", String(selected));
    });
    return { project, branch };
  }

  function selectProjectSwitchOption(option) {
    const projectName = option.dataset.projectSwitchOption;
    const homeOption = document.querySelector(`[data-home-project-option="${projectName}"]`);
    if (homeOption) selectHomeProject(homeOption, false);
    else activateProject(projectName, true);
    toggleProjectSwitchPopover(false);
    projectSwitch.focus({ preventScroll: true });
    showToast(`已切换到 ${projectName} · ${option.dataset.projectSwitchOptionBranch}`);
  }

  function createNewThread(projectName) {
    const activeProject = document.querySelector("[data-project-node].active")?.dataset.projectNode || "azem";
    const target = activateProject(projectName || activeProject, true);
    if (!target) return;
    const resolvedName = target.project.dataset.projectNode;
    document.querySelectorAll("[data-thread]").forEach((item) => item.classList.remove("active"));
    threadLayer.dataset.threadMode = "new";
    newThreadBlank.hidden = false;
    newThreadBlank.setAttribute("aria-hidden", "false");
    document.querySelector("[data-new-thread-project-name]").textContent = resolvedName;
    document.querySelector("[data-new-thread-project-path]").textContent = resolvedName;
    document.querySelector("[data-new-thread-project-branch]").textContent = target.branch;
    document.querySelector("[data-thread-title]").textContent = "新对话";
    document.querySelector("[data-thread-eyebrow]").textContent = "NEW CHAT";
    document.querySelector("[data-thread-workspace]").textContent = `${resolvedName} · ${target.branch}`;
    composerTextarea.value = "";
    composerTextarea.placeholder = `在 ${resolvedName} 中描述你想完成的任务…`;
    activeThreadOutput = [];
    clearTimers();
    workspace.dataset.inspector = "closed";
    switchView("thread");
    transcriptViewport.scrollTop = 0;
    requestAnimationFrame(() => composerTextarea.focus({ preventScroll: true }));
    showToast(`已在 ${resolvedName} 中新建对话`);
  }

  function toggleHomeProjectMenu(force) {
    const shouldOpen = typeof force === "boolean" ? force : homeProjectMenu.hidden;
    homeProjectMenu.hidden = !shouldOpen;
    homeProjectTrigger.setAttribute("aria-expanded", String(shouldOpen));
  }

  function selectHomeProject(option, announce = true) {
    if (!option) return;
    homeSelectedProject = option.dataset.homeProjectOption;
    const branch = option.dataset.homeProjectOptionBranch;
    document.querySelector("[data-home-project-label]").textContent = homeSelectedProject;
    document.querySelector("[data-home-project-branch]").textContent = branch;
    document.querySelectorAll("[data-home-project-option]").forEach((item) => {
      const selected = item === option;
      item.classList.toggle("selected", selected);
      item.setAttribute("aria-selected", String(selected));
    });
    activateProject(homeSelectedProject, false);
    toggleHomeProjectMenu(false);
    if (announce) showToast(`新任务将绑定到 ${homeSelectedProject}`);
  }

  function submitHomeTask() {
    const prompt = homeTaskInput.value.trim();
    if (!prompt) {
      showToast("先描述要完成的任务");
      homeTaskInput.focus({ preventScroll: true });
      return;
    }
    createNewThread(homeSelectedProject);
    composerTextarea.value = prompt;
    composerTextarea.dispatchEvent(new Event("input", { bubbles: true }));
    showToast(`任务已绑定到 ${homeSelectedProject}`);
  }

  function openProjectCreate() {
    toggleProjectMenu(false);
    projectCreateBackdrop.hidden = false;
    requestAnimationFrame(() => document.querySelector("[data-project-name]").select());
  }

  function closeProjectCreate() {
    projectCreateBackdrop.hidden = true;
  }

  function updateFastAvailability(providerLogo) {
    const available = providerLogo === "openai";
    fastControl.hidden = !available;
    fastIndicator.toggleAttribute("hidden", !available || fastToggle.getAttribute("aria-pressed") !== "true");
    if (!available) fastControl.classList.remove("show-tip");
  }

  function setFastMode(enabled, announce = true) {
    fastToggle.setAttribute("aria-pressed", String(enabled));
    fastIndicator.toggleAttribute("hidden", !enabled || fastControl.hidden);
    fastControl.classList.add("show-tip");
    window.clearTimeout(fastTipTimer);
    fastTipTimer = window.setTimeout(() => fastControl.classList.remove("show-tip"), 1400);
    if (announce) showToast(enabled ? "Fast 模式已开启 · 1.5 倍速，用量更多" : "已切换为标准速度");
  }

  function filterComposerModels(query = "") {
    const normalized = query.trim().toLowerCase();
    let visibleCount = 0;
    document.querySelectorAll("[data-model-option]").forEach((button) => {
      const matches = !normalized || button.dataset.modelKeywords.toLowerCase().includes(normalized);
      button.hidden = !matches;
      if (matches) visibleCount += 1;
    });
    document.querySelector("[data-model-search-empty]").hidden = visibleCount > 0;
  }

  function filterProviders(query = "") {
    const normalized = query.trim().toLocaleLowerCase();
    const visibleByGroup = { enabled: 0, disabled: 0 };
    providerEntries.forEach((entry) => {
      const searchable = `${entry.textContent} ${entry.dataset.providerKeywords || ""}`.toLocaleLowerCase();
      const matches = !normalized || searchable.includes(normalized);
      entry.hidden = !matches;
      if (matches) visibleByGroup[entry.dataset.providerGroup] += 1;
    });
    document.querySelectorAll("[data-provider-group-label]").forEach((label) => {
      label.hidden = visibleByGroup[label.dataset.providerGroupLabel] === 0;
    });
    providerEmpty.hidden = providerEntries.some((entry) => !entry.hidden);
  }

  function closeCustomSelect(select, restoreFocus = false) {
    if (!select || !select.classList.contains("open")) return;
    select.classList.remove("open");
    select.querySelector("[data-custom-select-menu]").hidden = true;
    const trigger = select.querySelector("[data-custom-select-trigger]");
    trigger.setAttribute("aria-expanded", "false");
    if (restoreFocus) trigger.focus({ preventScroll: true });
  }

  function closeCustomSelects(except = null) {
    customSelects.forEach((select) => { if (select !== except) closeCustomSelect(select); });
  }

  function openCustomSelect(select, focusOption = false) {
    closeCustomSelects(select);
    select.classList.add("open");
    select.querySelector("[data-custom-select-menu]").hidden = false;
    select.querySelector("[data-custom-select-trigger]").setAttribute("aria-expanded", "true");
    if (focusOption) {
      const option = select.querySelector('[data-custom-select-option][aria-selected="true"]') || select.querySelector("[data-custom-select-option]");
      requestAnimationFrame(() => option?.focus({ preventScroll: true }));
    }
  }

  function applyCustomSelectValue(select, value, announce = true) {
    const option = [...select.querySelectorAll("[data-custom-select-option]")].find((item) => item.dataset.value === value);
    if (!option) return;
    select.dataset.value = value;
    select.querySelector("[data-custom-select-label]").textContent = option.dataset.label;
    select.querySelector("[data-custom-select-caption]").textContent = option.dataset.caption;
    select.querySelectorAll("[data-custom-select-option]").forEach((item) => item.setAttribute("aria-selected", String(item === option)));
    if (select.dataset.customSelect === "font") {
      root.dataset.font = value;
      if (announce) showToast(`界面字体：${option.dataset.label}`);
    } else if (select.dataset.customSelect === "theme") {
      root.dataset.theme = value;
      document.querySelectorAll("[data-settings-theme]").forEach((button) => button.classList.toggle("active", button.dataset.settingsTheme === value));
    } else if (select.dataset.customSelect === "motion") {
      root.dataset.motion = value;
      document.querySelector("[data-reduce-motion]").checked = value === "reduced";
      if (announce) playStream(activeThreadOutput);
    }
  }

  function moveCustomSelectFocus(option, key) {
    const options = [...option.closest("[data-custom-select-menu]").querySelectorAll("[data-custom-select-option]")];
    const index = options.indexOf(option);
    const nextIndex = key === "Home" ? 0 : key === "End" ? options.length - 1 : Math.max(0, Math.min(options.length - 1, index + (key === "ArrowDown" ? 1 : -1)));
    options[nextIndex].focus({ preventScroll: true });
  }

  function closeComposerMenus() {
    document.querySelectorAll("[data-composer-popover]").forEach((popover) => { popover.hidden = true; });
    document.querySelectorAll("[data-composer-menu-toggle]").forEach((button) => button.setAttribute("aria-expanded", "false"));
    composerModelSearch.value = "";
    filterComposerModels();
    fastControl.classList.remove("show-tip");
  }

  function toggleComposerMenu(name) {
    const popover = document.querySelector(`[data-composer-popover="${name}"]`);
    const trigger = document.querySelector(`[data-composer-menu-toggle="${name}"]`);
    const shouldOpen = popover.hidden;
    closeComposerMenus();
    popover.hidden = !shouldOpen;
    trigger.setAttribute("aria-expanded", String(shouldOpen));
    if (shouldOpen && name === "model") requestAnimationFrame(() => composerModelSearch.focus());
  }

  function filterRouteModels(query = "") {
    const normalized = query.trim().toLowerCase();
    let visibleCount = 0;
    routeModelOptions.forEach((option) => {
      const matches = !normalized || option.dataset.search.toLowerCase().includes(normalized);
      option.hidden = !matches;
      if (matches) visibleCount += 1;
    });
    document.querySelector("[data-route-model-empty]").hidden = visibleCount > 0;
    if (!routeModelPopover.hidden) positionRouteModelPopover();
  }

  function positionRouteModelPopover() {
    if (!activeRouteTrigger || routeModelPopover.hidden) return;
    const triggerRect = activeRouteTrigger.getBoundingClientRect();
    const menuRect = routeModelPopover.getBoundingClientRect();
    const menuWidth = Math.max(292, triggerRect.width);
    const left = Math.min(window.innerWidth - menuWidth - 10, Math.max(10, triggerRect.right - menuWidth));
    const openAbove = window.innerHeight - triggerRect.bottom < Math.min(menuRect.height + 10, 320);
    const top = openAbove ? Math.max(10, triggerRect.top - menuRect.height - 6) : triggerRect.bottom + 6;
    routeModelPopover.dataset.placement = openAbove ? "top" : "bottom";
    routeModelPopover.style.width = `${menuWidth}px`;
    routeModelPopover.style.left = `${left}px`;
    routeModelPopover.style.top = `${top}px`;
  }

  function closeRouteModelPicker(restoreFocus = false) {
    if (routeModelPopover.hidden) return;
    const previousTrigger = activeRouteTrigger;
    routeModelPopover.hidden = true;
    routeModelSearch.value = "";
    filterRouteModels();
    document.querySelectorAll("[data-route-model-trigger]").forEach((trigger) => trigger.setAttribute("aria-expanded", "false"));
    activeRouteTrigger = null;
    if (restoreFocus && previousTrigger) previousTrigger.focus({ preventScroll: true });
  }

  function openRouteModelPicker(trigger) {
    if (activeRouteTrigger === trigger && !routeModelPopover.hidden) {
      closeRouteModelPicker(true);
      return;
    }
    closeRouteModelPicker();
    activeRouteTrigger = trigger;
    trigger.setAttribute("aria-expanded", "true");
    routeModelOptions.forEach((option) => {
      const selected = option.dataset.modelId === trigger.dataset.modelId;
      option.classList.toggle("selected", selected);
      option.setAttribute("aria-selected", String(selected));
    });
    routeModelPopover.hidden = false;
    positionRouteModelPopover();
    requestAnimationFrame(() => routeModelSearch.focus());
  }

  function selectRouteModel(option) {
    if (!activeRouteTrigger) return;
    const row = activeRouteTrigger.closest(".route-row");
    activeRouteTrigger.dataset.modelId = option.dataset.modelId;
    activeRouteTrigger.dataset.providerLogo = option.dataset.providerLogo;
    activeRouteTrigger.querySelector("[data-route-model-name]").textContent = option.dataset.modelName;
    activeRouteTrigger.querySelector("[data-route-provider-name]").textContent = option.dataset.providerName;
    row.querySelector(".route-provider-icon").src = `https://models.dev/logos/${option.dataset.providerLogo}.svg`;
    const routeName = row.querySelector(":scope > span > strong").textContent;
    showToast(`${routeName}已切换为 ${option.dataset.modelName}`);
    closeRouteModelPicker(true);
  }

  navItems.forEach((item) => item.addEventListener("click", () => switchView(item.dataset.view)));
  projectSwitch.addEventListener("click", () => toggleProjectSwitchPopover());
  projectSwitchSearch.addEventListener("input", () => filterProjectSwitchOptions(projectSwitchSearch.value));
  projectSwitchSearch.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      const firstVisible = projectSwitchOptions.find((option) => !option.hidden);
      if (firstVisible) { event.preventDefault(); firstVisible.focus(); }
    } else if (event.key === "Escape") {
      event.preventDefault();
      toggleProjectSwitchPopover(false);
      projectSwitch.focus({ preventScroll: true });
    }
  });
  projectSwitchOptions.forEach((option) => {
    option.addEventListener("click", () => selectProjectSwitchOption(option));
    option.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        toggleProjectSwitchPopover(false);
        projectSwitch.focus({ preventScroll: true });
        return;
      }
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      const visibleOptions = projectSwitchOptions.filter((item) => !item.hidden);
      const currentIndex = visibleOptions.indexOf(option);
      const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? visibleOptions.length - 1 : Math.max(0, Math.min(visibleOptions.length - 1, currentIndex + (event.key === 'ArrowDown' ? 1 : -1)));
      event.preventDefault();
      visibleOptions[nextIndex]?.focus();
    });
  });
  homeProjectTrigger.addEventListener("click", () => toggleHomeProjectMenu());
  document.querySelectorAll("[data-home-project-option]").forEach((option) => option.addEventListener("click", () => selectHomeProject(option)));
  document.querySelectorAll("[data-home-suggestion]").forEach((button) => button.addEventListener("click", () => {
    homeTaskInput.value = button.dataset.homeSuggestion;
    homeTaskInput.focus({ preventScroll: true });
  }));
  homeTaskSubmit.addEventListener("click", submitHomeTask);
  homeTaskInput.addEventListener("keydown", (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      submitHomeTask();
    }
  });
  homeFastToggle.addEventListener("click", () => {
    const enabled = homeFastToggle.getAttribute("aria-pressed") !== "true";
    homeFastToggle.setAttribute("aria-pressed", String(enabled));
    showToast(enabled ? "Fast 模式已开启 · 1.5 倍速" : "已切换为标准速度");
  });
  document.querySelectorAll("[data-new-thread-project]").forEach((button) => button.addEventListener("click", () => createNewThread(button.dataset.newThreadProject)));
  document.querySelectorAll("[data-new-thread-prompt]").forEach((button) => button.addEventListener("click", () => {
    composerTextarea.value = button.dataset.newThreadPrompt;
    composerTextarea.focus({ preventScroll: true });
  }));
  threadStageTabs.forEach((tab, index) => {
    tab.addEventListener("click", () => switchThreadStage(tab.dataset.threadStageTarget));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const nextIndex = event.key === "Home" ? 0 : event.key === "End" ? threadStageTabs.length - 1 : (index + (event.key === "ArrowRight" ? 1 : -1) + threadStageTabs.length) % threadStageTabs.length;
      threadStageTabs[nextIndex].focus();
      switchThreadStage(threadStageTabs[nextIndex].dataset.threadStageTarget);
    });
  });
  document.querySelectorAll("[data-view-jump]").forEach((button) => button.addEventListener("click", () => switchView(button.dataset.viewJump)));
  document.querySelectorAll("[data-workspace-back]").forEach((button) => button.addEventListener("click", () => switchView("workspace")));
  document.querySelector("[data-revise-plan]").addEventListener("click", () => {
    switchThreadStage("thinking", false);
    composerTextarea.placeholder = "说明这版计划还需要调整的地方…";
    composerTextarea.focus({ preventScroll: true });
    showToast("已返回计划调整");
  });
  document.querySelector("[data-start-implementation]").addEventListener("click", () => {
    composerTextarea.placeholder = "实现过程中可以继续补充要求…";
    switchThreadStage("executing");
    showToast("已开始实现方案");
  });
  document.querySelectorAll("[data-inspector-toggle]").forEach((button) => button.addEventListener("click", () => {
    workspace.dataset.inspector = workspace.dataset.inspector === "open" ? "closed" : "open";
  }));
  document.querySelectorAll("[data-mode-target]").forEach((button) => button.addEventListener("click", () => {
    const target = button.dataset.modeTarget === "workspace" ? "workspace" : lastConversationView;
    switchView(target);
  }));

  document.querySelectorAll("[data-project-toggle]").forEach((button) => button.addEventListener("click", () => {
    const project = button.closest("[data-project-node]");
    const expanded = project.dataset.expanded !== "true";
    project.dataset.expanded = String(expanded);
    button.querySelector(".project-chevron").textContent = expanded ? "⌄" : "›";
    const projectName = project.dataset.projectNode;
    activateProject(projectName, false);
  }));

  projectAddToggle.addEventListener("click", () => toggleProjectMenu());
  document.querySelector("[data-choose-folder]").addEventListener("click", () => {
    toggleProjectMenu(false);
    showToast("已打开项目文件夹选择器");
  });
  document.querySelector("[data-project-create-open]").addEventListener("click", openProjectCreate);
  document.querySelectorAll("[data-project-create-close]").forEach((button) => button.addEventListener("click", closeProjectCreate));
  projectCreateBackdrop.addEventListener("click", (event) => { if (event.target === projectCreateBackdrop) closeProjectCreate(); });
  document.querySelector("[data-project-create-form]").addEventListener("submit", (event) => {
    event.preventDefault();
    const name = document.querySelector("[data-project-name]").value.trim() || "new-project";
    closeProjectCreate();
    showToast(`已创建并加入项目：${name}`);
  });

  document.querySelectorAll("[data-thread]").forEach((button) => button.addEventListener("click", () => {
    const data = threadData[button.dataset.thread];
    const projectName = button.dataset.threadProject || button.closest("[data-project-node]")?.dataset.projectNode || "azem";
    const wasActive = threadLayer.classList.contains("active");
    const projectContext = activateProject(projectName, true);
    document.querySelectorAll("[data-thread]").forEach((item) => item.classList.toggle("active", item === button));
    threadLayer.dataset.threadMode = "existing";
    newThreadBlank.hidden = true;
    newThreadBlank.setAttribute("aria-hidden", "true");
    document.querySelector("[data-thread-workspace]").textContent = `${projectName} · ${projectContext?.branch || "main"}`;
    workspace.dataset.inspector = "open";
    const layer = threadLayer;

    const renderThread = () => {
      document.querySelector(".thread-title strong").textContent = data.title;
      document.querySelector(".thread-title .eyebrow").textContent = data.eyebrow;
      activeThreadOutput = data.output;
      layer.classList.remove("leaving");
      switchThreadStage(data.stage, data.stage === "executing");
    };

    if (wasActive) {
      layer.classList.add("leaving");
      window.setTimeout(renderThread, root.dataset.motion === "reduced" ? 0 : 170);
      return;
    }

    renderThread();
    switchView("thread");
  }));
  document.querySelectorAll("[data-workspace-thread]").forEach((button) => button.addEventListener("click", () => {
    document.querySelector(`[data-thread="${button.dataset.workspaceThread}"]`)?.click();
  }));

  document.querySelectorAll("[data-file]").forEach((button) => button.addEventListener("click", () => {
    document.querySelectorAll("[data-file]").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelector("[data-file-title]").textContent = button.dataset.file;
    document.querySelector("[data-code-preview] code").innerHTML = filePreviews[button.dataset.file];
  }));

  document.querySelectorAll("[data-composer-menu-toggle]").forEach((button) => button.addEventListener("click", () => toggleComposerMenu(button.dataset.composerMenuToggle)));
  document.querySelector("[data-plan-toggle]").addEventListener("click", (event) => {
    const active = event.currentTarget.getAttribute("aria-pressed") !== "true";
    event.currentTarget.setAttribute("aria-pressed", String(active));
  });
  document.querySelectorAll("[data-approval-option]").forEach((button) => button.addEventListener("click", () => {
    document.querySelectorAll("[data-approval-option]").forEach((item) => item.classList.toggle("selected", item === button));
    document.querySelector("[data-approval-label]").textContent = button.querySelector("strong").textContent;
    document.querySelector("[data-approval-icon]").replaceChildren(button.querySelector(".menu-option-icon .ui-icon").cloneNode(true));
    closeComposerMenus();
  }));
  document.querySelectorAll("[data-model-option]").forEach((button) => button.addEventListener("click", () => {
    document.querySelectorAll("[data-model-option]").forEach((item) => item.classList.toggle("selected", item === button));
    document.querySelector("[data-model-label]").textContent = button.dataset.modelOption;
    const provider = document.querySelector("[data-selected-provider]");
    provider.src = `https://models.dev/logos/${button.dataset.providerLogo}.svg`;
    updateFastAvailability(button.dataset.providerLogo);
  }));
  fastToggle.addEventListener("click", () => setFastMode(fastToggle.getAttribute("aria-pressed") !== "true"));
  composerModelSearch.addEventListener("input", () => filterComposerModels(composerModelSearch.value));
  composerModelSearch.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      const firstVisible = [...document.querySelectorAll("[data-model-option]")].find((option) => !option.hidden);
      if (firstVisible) { event.preventDefault(); firstVisible.focus(); }
    }
  });

  document.querySelectorAll("[data-route-model-trigger]").forEach((trigger) => {
    trigger.addEventListener("click", () => openRouteModelPicker(trigger));
    trigger.addEventListener("keydown", (event) => {
      if (["ArrowDown", "Enter", " "].includes(event.key)) {
        event.preventDefault();
        openRouteModelPicker(trigger);
      }
    });
  });
  routeModelSearch.addEventListener("input", () => filterRouteModels(routeModelSearch.value));
  routeModelSearch.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      const firstVisible = routeModelOptions.find((option) => !option.hidden);
      if (firstVisible) { event.preventDefault(); firstVisible.focus(); }
    } else if (event.key === "Escape") {
      event.preventDefault();
      closeRouteModelPicker(true);
    }
  });
  routeModelOptions.forEach((option) => {
    option.addEventListener("click", () => selectRouteModel(option));
    option.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRouteModelPicker(true);
        return;
      }
      if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
      const visibleOptions = routeModelOptions.filter((item) => !item.hidden);
      const currentIndex = visibleOptions.indexOf(option);
      const nextIndex = event.key === "Home" ? 0 : event.key === "End" ? visibleOptions.length - 1 : Math.max(0, Math.min(visibleOptions.length - 1, currentIndex + (event.key === "ArrowDown" ? 1 : -1)));
      event.preventDefault();
      visibleOptions[nextIndex]?.focus();
    });
  });
  document.addEventListener("pointerdown", (event) => {
    if (event.target.closest("[data-route-model-popover], [data-route-model-trigger]")) return;
    closeRouteModelPicker();
  });
  document.addEventListener("pointerdown", (event) => {
    if (!event.target.closest(".home-project-picker")) toggleHomeProjectMenu(false);
  });
  document.addEventListener("pointerdown", (event) => {
    if (!event.target.closest(".project-switch-wrap")) toggleProjectSwitchPopover(false);
  });
  settingsScroll.addEventListener("scroll", () => closeRouteModelPicker());
  window.addEventListener("resize", () => closeRouteModelPicker());
  const effortNames = ["低", "中", "高", "极高"];
  const effortRange = document.querySelector("[data-effort-range]");
  const effortSlider = document.querySelector("[data-effort-slider]");
  const effortStops = [...effortSlider.querySelectorAll(".effort-stops i")];
  let effortSnapTimer = 0;
  function updateEffort(value, snap = false) {
    const rawValue = Math.max(0, Math.min(effortNames.length - 1, Number(value)));
    const renderedValue = snap ? Math.round(rawValue) : rawValue;
    const activeIndex = Math.round(renderedValue);
    effortRange.value = String(renderedValue);
    effortRange.setAttribute("aria-valuetext", effortNames[activeIndex]);
    effortSlider.style.setProperty("--effort-pct", `${(renderedValue / (effortNames.length - 1)) * 100}%`);
    document.querySelectorAll("[data-effort-index]").forEach((button) => button.classList.toggle("selected", Number(button.dataset.effortIndex) === activeIndex));
    effortStops.forEach((stop, index) => {
      stop.classList.toggle("is-passed", index < activeIndex);
      stop.classList.toggle("is-active", index === activeIndex);
    });
    document.querySelector("[data-effort-label]").textContent = effortNames[activeIndex];
  }
  effortRange.addEventListener("input", () => updateEffort(effortRange.value));
  effortRange.addEventListener("change", () => {
    updateEffort(effortRange.value, true);
    effortSlider.classList.add("is-snapping");
    window.clearTimeout(effortSnapTimer);
    effortSnapTimer = window.setTimeout(() => effortSlider.classList.remove("is-snapping"), 150);
  });
  effortRange.addEventListener("keydown", (event) => {
    const keys = ["ArrowLeft", "ArrowDown", "ArrowRight", "ArrowUp", "Home", "End"];
    if (!keys.includes(event.key)) return;
    event.preventDefault();
    const current = Math.round(Number(effortRange.value));
    if (event.key === "Home") updateEffort(0, true);
    else if (event.key === "End") updateEffort(effortNames.length - 1, true);
    else updateEffort(current + (["ArrowRight", "ArrowUp"].includes(event.key) ? 1 : -1), true);
  });
  document.querySelectorAll("[data-effort-index]").forEach((button) => button.addEventListener("click", () => {
    updateEffort(button.dataset.effortIndex, true);
    effortRange.focus({ preventScroll: true });
  }));
  document.addEventListener("pointerdown", (event) => {
    if (event.target.closest("[data-composer-menu-toggle], [data-composer-popover]")) return;
    closeComposerMenus();
  });

  document.querySelectorAll(".governance-options input").forEach((input) => input.addEventListener("change", () => {
    const group = input.closest(".governance-options");
    group.querySelectorAll("label").forEach((label) => label.classList.toggle("selected", label.contains(input)));
  }));

  document.querySelectorAll("[data-toast]").forEach((button) => button.addEventListener("click", () => showToast(button.dataset.toast)));
  document.querySelector("[data-replay]").addEventListener("click", () => {
    switchThreadStage("executing", false);
    playStream(activeThreadOutput);
  });
  document.querySelectorAll("[data-command-open]").forEach((button) => button.addEventListener("click", openCommand));
  document.querySelector("[data-motion-open]").addEventListener("click", openMotionSheet);
  document.querySelector("[data-motion-close]").addEventListener("click", closeMotionSheet);
  document.querySelector("[data-settings-open]").addEventListener("click", () => openSettings());
  document.querySelector("[data-settings-close]").addEventListener("click", closeSettings);
  commandBackdrop.addEventListener("click", (event) => { if (event.target === commandBackdrop) closeCommand(); });
  settingsBackdrop.addEventListener("click", (event) => { if (event.target === settingsBackdrop) closeSettings(); });
  document.querySelectorAll("[data-command]").forEach((button) => button.addEventListener("click", () => {
    const command = button.dataset.command;
    closeCommand();
    if (command === "motion") openMotionSheet();
    else switchView(command);
  }));

  document.querySelectorAll("[data-settings-section]").forEach((button) => button.addEventListener("click", () => activateSettingsSection(button.dataset.settingsSection)));
  document.querySelectorAll("[data-provider-tab]").forEach((button) => button.addEventListener("click", () => {
    document.querySelectorAll("[data-provider-tab]").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll("[data-provider-panel]").forEach((panel) => {
      const active = panel.dataset.providerPanel === button.dataset.providerTab;
      panel.hidden = !active;
      panel.classList.toggle("active", active);
    });
    document.querySelector("[data-settings-scroll]").scrollTop = 0;
  }));
  providerSearch.addEventListener("input", () => filterProviders(providerSearch.value));
  providerSearch.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !providerSearch.value) return;
    event.preventDefault();
    event.stopPropagation();
    providerSearch.value = "";
    filterProviders();
  });
  settingsSearch.addEventListener("input", () => {
    const query = settingsSearch.value.trim().toLowerCase();
    let firstMatch = null;
    document.querySelectorAll("[data-settings-section]").forEach((button) => {
      const page = document.querySelector(`[data-settings-page="${button.dataset.settingsSection}"]`);
      const matches = !query || `${button.textContent} ${page.dataset.settingsKeywords}`.toLowerCase().includes(query);
      button.hidden = !matches;
      if (matches && !firstMatch) firstMatch = button;
    });
    if (query && firstMatch) activateSettingsSection(firstMatch.dataset.settingsSection);
  });

  document.querySelectorAll("[data-settings-theme]").forEach((button) => button.addEventListener("click", () => {
    const value = button.dataset.settingsTheme;
    const resolved = value === "system" ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : value;
    root.dataset.theme = resolved;
    applyCustomSelectValue(document.querySelector('[data-custom-select="theme"]'), resolved, false);
    document.querySelectorAll("[data-settings-theme]").forEach((item) => item.classList.toggle("active", item === button));
  }));

  let interfaceFontSize = 14;
  document.querySelectorAll("[data-font-size]").forEach((button) => button.addEventListener("click", () => {
    interfaceFontSize = Math.max(11, Math.min(20, interfaceFontSize + (button.dataset.fontSize === "up" ? 1 : -1)));
    document.querySelector("[data-font-size-value]").textContent = `${interfaceFontSize} px`;
    showToast(`界面字号已调整为 ${interfaceFontSize} px`);
  }));

  document.querySelector("[data-reduce-motion]").addEventListener("change", (event) => {
    root.dataset.motion = event.target.checked ? "reduced" : "full";
    applyCustomSelectValue(document.querySelector('[data-custom-select="motion"]'), root.dataset.motion, false);
  });

  document.querySelectorAll(".segmented-control button").forEach((button) => button.addEventListener("click", () => {
    button.parentElement.querySelectorAll("button").forEach((item) => item.classList.toggle("active", item === button));
    showToast(`界面语言：${button.textContent}`);
  }));

  document.querySelectorAll("[data-stepper]").forEach((button) => button.addEventListener("click", () => {
    const value = document.querySelector("[data-stepper-value]");
    const next = Math.max(1, Math.min(12, Number(value.textContent) + (button.dataset.stepper === "up" ? 1 : -1)));
    value.textContent = String(next);
  }));

  customSelects.forEach((select) => {
    const trigger = select.querySelector("[data-custom-select-trigger]");
    trigger.addEventListener("click", () => select.classList.contains("open") ? closeCustomSelect(select) : openCustomSelect(select));
    trigger.addEventListener("keydown", (event) => {
      if (!["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) return;
      event.preventDefault();
      openCustomSelect(select, true);
    });
    select.querySelectorAll("[data-custom-select-option]").forEach((option) => {
      option.addEventListener("click", () => {
        applyCustomSelectValue(select, option.dataset.value);
        closeCustomSelect(select, true);
      });
      option.addEventListener("keydown", (event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          closeCustomSelect(select, true);
          return;
        }
        if (event.key === "Tab") { closeCustomSelect(select); return; }
        if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        moveCustomSelectFocus(option, event.key);
      });
    });
  });

  document.addEventListener("pointerdown", (event) => {
    if (!event.target.closest("[data-custom-select]")) closeCustomSelects();
  });

  commandInput.addEventListener("input", () => {
    const query = commandInput.value.trim().toLowerCase();
    document.querySelectorAll("[data-command]").forEach((button) => {
      button.hidden = Boolean(query) && !button.textContent.toLowerCase().includes(query);
    });
  });

  window.addEventListener("keydown", (event) => {
    if (event.defaultPrevented) return;
    const primary = event.metaKey || event.ctrlKey;
    if (primary && event.key.toLowerCase() === "k") { event.preventDefault(); openCommand(); }
    if (primary && event.key.toLowerCase() === "m") { event.preventDefault(); openMotionSheet(); }
    if (primary && event.key.toLowerCase() === "n") { event.preventDefault(); switchView("home"); }
    if (primary && event.key.toLowerCase() === "f" && !routeModelPopover.hidden) { event.preventDefault(); routeModelSearch.focus(); }
    else if (primary && event.key.toLowerCase() === "f" && !document.querySelector('[data-composer-popover="model"]').hidden) { event.preventDefault(); composerModelSearch.focus(); }
    else if (primary && event.key.toLowerCase() === "f" && !settingsBackdrop.hidden && document.querySelector('[data-settings-page="catalog"]').classList.contains("active")) { event.preventDefault(); providerSearch.focus(); }
    if (primary && event.key === ",") { event.preventDefault(); openSettings(); }
    if (primary && ["2", "3"].includes(event.key)) {
      event.preventDefault();
      switchView(event.key === "2" ? "files" : "changes");
    }
    if (event.key === "Escape") {
      if (!projectSwitchPopover.hidden) toggleProjectSwitchPopover(false);
      else if (!homeProjectMenu.hidden) toggleHomeProjectMenu(false);
      else if (!routeModelPopover.hidden) closeRouteModelPicker(true);
      else if (!projectCreateBackdrop.hidden) closeProjectCreate();
      else if (!settingsBackdrop.hidden) closeSettings();
      else if (!commandBackdrop.hidden) closeCommand();
      else if (motionSheet.classList.contains("open")) closeMotionSheet();
      else if (!projectAddPopover.hidden) toggleProjectMenu(false);
      else if ([...document.querySelectorAll("[data-composer-popover]")].some((popover) => !popover.hidden)) closeComposerMenus();
      else {
        const activeView = viewPanels.find((panel) => panel.classList.contains("active"))?.dataset.viewPanel;
        if (["files", "changes"].includes(activeView)) {
          event.preventDefault();
          switchView("workspace");
        }
      }
    }
  });

  window.setInterval(() => {
    elapsed += 0.1;
    document.querySelector("[data-elapsed]").textContent = `${elapsed.toFixed(1)}s`;
  }, 100);

  switchThreadStage("executing", false);
  playStream(activeThreadOutput);
})();
