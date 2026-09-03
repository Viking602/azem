use super::*;

impl AzemWindow {
    pub(super) fn new(
        window: &mut Window,
        cx: &mut Context<Self>,
        runtime: RuntimeConnection,
        runtime_options: Rc<RefCell<RuntimeOptions>>,
        window_state_path: Option<PathBuf>,
    ) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let mut state = AppState::default();
        let locale = Locale::resolve("en");
        let preferences = window_state_path
            .as_ref()
            .map(|path| AppearancePreferences::load(&path.with_file_name("gpui-appearance.json")))
            .transpose();
        let preferences = match preferences {
            Ok(value) => value.unwrap_or_default(),
            Err(error) => {
                state.settings.error = locale
                    .format("error.appearanceLoad", &[("error", error.to_string())])
                    .into();
                AppearancePreferences::default()
            }
        };
        state.settings.appearance = json!(preferences);
        cx.set_global(preferences);
        let composer = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.describeTheTaskReferenceFilesUseSkills"),
            )
            .multiline()
        });
        let search_input = cx
            .new(|cx| TextInput::new(cx, window, locale.text("ui.searchConversationsAndMessages")));
        let session_rename_input =
            cx.new(|cx| TextInput::new(cx, window, locale.text("sidebar.renameSession")).compact());
        let mut previous_composer = (String::new(), Some(0));
        cx.observe(&composer, move |this, input, cx| {
            let input = input.read(cx);
            if input.submission_text() != previous_composer.0
                || input.completion_cursor() != previous_composer.1
            {
                previous_composer = (input.submission_text(), input.completion_cursor());
                this.completion.submission_error.clear();
                this.sync_completions(cx);
                cx.notify();
            }
        })
        .detach();
        let terminal_input =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.terminal")).terminal());
        cx.observe(&terminal_input, |this, input, cx| {
            let data = {
                let input = input.read(cx);
                (!input.has_marked_text() && !input.text().is_empty())
                    .then(|| input.text().to_owned())
            };
            if let Some(data) = data {
                this.write_terminal_data(&data);
                input.update(cx, |input, cx| input.clear(cx));
            }
            cx.notify();
        })
        .detach();
        let model_search = cx.new(|cx| {
            TextInput::new(cx, window, locale.text("ui.searchModelsOrProviders")).compact()
        });
        let branch_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("branch.search")).compact());
        let mut previous_branch_query = String::new();
        cx.observe(&branch_search, move |this, input, cx| {
            let query = input.read(cx).text();
            if query != previous_branch_query {
                previous_branch_query = query.to_owned();
                this.branch_picker.index = 0;
                this.branch_picker.scroll.scroll_to_item(0);
                cx.notify();
            }
        })
        .detach();
        let settings_provider_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("ui.searchProviders")).compact());
        let settings_model_search = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.searchModelFamilyVersionOrRawId"),
            )
            .compact()
        });
        let extension_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("ui.searchExtensions")).compact());
        let marketplace_source = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.marketplaceOwnerRepoGitUrlOrLocalPath"),
            )
            .compact()
        });
        let settings_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.settingsSearch")).compact());
        let font_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.fontSearch")).compact());
        let security_fields = SECURITY_NUMBERS
            .iter()
            .map(|(key, _, _)| cx.new(|cx| TextInput::new(cx, window, *key).compact()))
            .collect::<Vec<_>>();
        for input in [
            &model_search,
            &settings_provider_search,
            &settings_model_search,
            &extension_search,
            &marketplace_source,
            &settings_search,
            &font_search,
        ]
        .into_iter()
        .chain(&security_fields)
        {
            let mut previous = input.read(cx).text().to_owned();
            cx.observe(input, move |_, input, cx| {
                let text = input.read(cx).text();
                if text != previous {
                    previous = text.to_owned();
                    cx.notify();
                }
            })
            .detach();
        }
        let transcript_list = ListState::new(0, ListAlignment::Top, px(800.));
        let messages = runtime.messages.clone();
        let mut this = Self {
            focus,
            state,
            runtime,
            runtime_options,
            runtime_generation: 0,
            pending_workspace_sequence: None,
            new_session_after_workspace_switch: false,
            composer,
            completion: ComposerCompletion::default(),
            search_input,
            session_rename_input,
            renaming_session_id: None,
            sidebar_context_menu: None,
            search_return_surface: Surface::Thread,
            model_search,
            branch_picker: BranchPicker {
                search: branch_search,
                focus: cx.focus_handle(),
                scroll: ScrollHandle::new(),
                open: false,
                index: 0,
                error: String::new(),
                confirm_target: None,
                button_bounds: None,
            },
            approval_picker: ApprovalPicker {
                open: false,
                index: 0,
                focus: cx.focus_handle(),
                button_bounds: None,
                error: String::new(),
            },
            settings_provider_search,
            settings_model_search,
            extension_settings: ExtensionSettings {
                search: extension_search,
                source: marketplace_source,
                scope: "user",
                busy: false,
            },
            native_settings: NativeSettings {
                language_menu_open: false,
                language_index: 0,
                language_saving: false,
                language_focus: cx.focus_handle(),
                language_scroll: gpui::ScrollHandle::new(),
                search: settings_search,
                font_search,
                font_menu_open: false,
                fonts: Vec::new(),
                fonts_loading: false,
                security_fields,
                security_draft: serde_json::Value::Null,
                security_baseline: serde_json::Value::Null,
                security_busy: false,
                security_saved: false,
            },
            model_picker_open: false,
            model_picker_error: String::new(),
            route_picker_target: None,
            subagent_setting_menu: None,
            context_popover_open: false,
            reply_popover: None,
            reply_feedback: HashMap::new(),
            hovered_message: None,
            reasoning_slider_bounds: None,
            reasoning_drag: None,
            fast_particles: cx.new(|_| FastParticles::new()),
            settings_section: "catalog".to_string(),
            settings_open: false,
            settings_provider: None,
            settings_provider_scroll: UniformListScrollHandle::new(),
            settings_model_scroll: UniformListScrollHandle::new(),
            archive_days: 30,
            archive_days_menu_open: false,
            usage_hover: None,
            environment_open: true,
            environment_expanded: None,
            side_panel_open: false,
            side_panel_agents_open: false,
            side_panel_add_menu_open: false,
            side_panel_closing: false,
            side_panel_width: SIDE_PANEL_DEFAULT_WIDTH,
            side_panel_visible_width: 0.,
            side_panel_animation_started: None,
            side_panel_animation_from: 0.,
            side_panel_resize_drag: None,
            terminal_open: false,
            terminal_input,
            terminal_scroll: ScrollHandle::new(),
            terminal_emulators: HashMap::new(),
            queued_prompts: Vec::new(),
            editing_queued_id: None,
            pending_requests: HashMap::new(),
            open_projects: HashSet::new(),
            process_expansion: Rc::new(RefCell::new(ProcessExpansion::default())),
            show_all_sessions: false,
            startup_started: Instant::now(),
            snapshot_ready_logged: false,
            provider_catalog_logged: false,
            window_state_path,
            window_size: window.window_bounds().get_bounds().size,
            _connection_task: Task::ready(()),
            transcript_list,
        };
        this.listen_to_runtime(messages, cx);
        this
    }

    pub(super) fn listen_to_runtime(
        &mut self,
        messages: async_channel::Receiver<RuntimeMessage>,
        cx: &mut Context<Self>,
    ) {
        let generation = self.runtime_generation;
        for _ in 0..messages.len() {
            if let Ok(message) = messages.try_recv() {
                self.apply_runtime_message(message, cx);
            }
        }
        self._connection_task = cx.spawn(async move |this, cx| {
            while let Ok(message) = messages.recv().await {
                if this
                    .update(cx, |this, cx| {
                        if this.runtime_generation != generation {
                            return;
                        }
                        this.apply_runtime_message(message, cx);
                        cx.notify();
                    })
                    .is_err()
                {
                    return;
                }
            }
        });
    }

    pub(super) fn switch_workspace(
        &mut self,
        workspace: &str,
        session_id: &str,
        sequence: Option<i64>,
        start_new_session: bool,
        cx: &mut Context<Self>,
    ) {
        self.runtime.detach();
        self.runtime_generation = self.runtime_generation.wrapping_add(1);
        let options = {
            let mut options = self.runtime_options.borrow_mut();
            options.workspace = PathBuf::from(workspace);
            options.session_id = (!session_id.is_empty()).then(|| session_id.to_string());
            options.clone()
        };
        self.runtime = RuntimeConnection::start(options);
        self.pending_workspace_sequence = sequence;
        self.new_session_after_workspace_switch = start_new_session;
        self.pending_requests.clear();
        self.queued_prompts.clear();
        self.terminal_emulators.clear();
        self.sidebar_context_menu = None;
        self.renaming_session_id = None;
        self.state.connection.connected = false;
        self.state.connection.reconnecting = true;
        self.state.connection.message = "Connecting to workspace runtime…".into();
        self.state.navigation.surface = Surface::Thread;
        self.listen_to_runtime(self.runtime.messages.clone(), cx);
        cx.notify();
    }

    pub(super) fn apply_runtime_message(
        &mut self,
        message: RuntimeMessage,
        cx: &mut Context<Self>,
    ) {
        let locale = Locale::resolve(&self.state.settings.language);
        let old_language = self.state.settings.language.clone();
        let old_block_count = self.state.transcript.blocks.borrow().len();
        let old_runtime_busy = runtime_busy(&self.state);
        let old_run_id = self.state.runtime.run_id.clone();
        let old_pending_process = needs_pending_process(
            &self.state.transcript.blocks.borrow(),
            self.state.runtime.running,
        );
        let old_session_id = self.state.navigation.current_session_id.clone();
        match message {
            RuntimeMessage::Connecting(message) => {
                self.state.connection.connected = false;
                self.state.connection.reconnecting = true;
                self.state.connection.message = message.into();
            }
            RuntimeMessage::Connected { pid, sequence } => {
                self.state.connection.connected = true;
                self.state.connection.reconnecting = false;
                self.state.connection.daemon_pid = pid;
                self.state.connection.message = "Connected".into();
                self.state.sequence = self.state.sequence.max(sequence);
            }
            RuntimeMessage::Snapshot(snapshot) => {
                self.state.apply_reconnect_snapshot(snapshot);
                if self.new_session_after_workspace_switch {
                    self.new_session_after_workspace_switch = false;
                    self.runtime
                        .request(Method::Execute, json!({"kind": "new_session"}));
                } else if let Some(sequence) = self.pending_workspace_sequence.take()
                    && let Some(index) =
                        self.state
                            .transcript
                            .blocks
                            .borrow()
                            .iter()
                            .position(|block| {
                                block
                                    .extra
                                    .get("sequence")
                                    .and_then(serde_json::Value::as_i64)
                                    == Some(sequence)
                            })
                {
                    self.transcript_list.scroll_to_reveal_item(index);
                }
                self.request_environment_metrics(true);
                if !self.snapshot_ready_logged {
                    tracing::info!(
                        interactive_ms = self.startup_started.elapsed().as_millis(),
                        "Azem GPUI state ready"
                    );
                    self.snapshot_ready_logged = true;
                }
                self.terminal_emulators.clear();
                for terminal in &self.state.terminals.sessions {
                    if let Some(id) = terminal.get("id").and_then(serde_json::Value::as_str) {
                        let mut emulator = TerminalEmulator::default();
                        emulator.resize(
                            terminal
                                .get("cols")
                                .and_then(serde_json::Value::as_u64)
                                .unwrap_or(120) as usize,
                            terminal
                                .get("rows")
                                .and_then(serde_json::Value::as_u64)
                                .unwrap_or(32) as usize,
                        );
                        self.terminal_emulators.insert(id.to_string(), emulator);
                        self.runtime
                            .request(Method::TerminalReplay, json!({"id": id}));
                    }
                }
                if self.state.terminals.active_id.is_empty()
                    && let Some(id) = self
                        .state
                        .terminals
                        .sessions
                        .first()
                        .and_then(|terminal| terminal.get("id"))
                        .and_then(serde_json::Value::as_str)
                {
                    self.state.terminals.active_id = id.to_string().into();
                }
            }
            RuntimeMessage::Event(ClientEvent::Envelope(envelope)) => {
                let environment_refresh = self.environment_open
                    && envelope.channel == "runtime"
                    && matches!(
                        envelope
                            .payload
                            .get("kind")
                            .and_then(serde_json::Value::as_str),
                        Some("tool_finished" | "run_finished" | "run_failed" | "run_cancelled")
                    );
                let listed_security_scans = envelope.channel == "runtime"
                    && envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        == Some("security_scan_list");
                if envelope.channel == "runtime" {
                    match envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                    {
                        "model_providers" if !self.provider_catalog_logged => {
                            tracing::info!(
                                providers = envelope
                                    .payload
                                    .get("modelProviders")
                                    .and_then(serde_json::Value::as_array)
                                    .map(Vec::len)
                                    .unwrap_or_default(),
                                "GPUI model provider catalog ready"
                            );
                            self.provider_catalog_logged = true;
                        }
                        "model_providers" => {}
                        "bridge_error" => tracing::warn!(
                            error = envelope
                                .payload
                                .get("text")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default(),
                            "GPUI bridge projection failed"
                        ),
                        _ => {}
                    }
                }
                if envelope.channel == "daemon"
                    && let Some(workspace) = envelope
                        .payload
                        .get("workspace")
                        .and_then(serde_json::Value::as_str)
                {
                    let session_id = envelope
                        .payload
                        .get("sessionId")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default();
                    let sequence = envelope
                        .payload
                        .get("sequence")
                        .and_then(serde_json::Value::as_i64);
                    self.switch_workspace(workspace, session_id, sequence, false, cx);
                    return;
                }
                if envelope.channel == "terminal"
                    && envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        == Some("terminal_exit")
                    && let Some(id) = envelope
                        .payload
                        .pointer("/session/id")
                        .and_then(serde_json::Value::as_str)
                {
                    self.terminal_emulators.remove(id);
                }
                self.state.apply_envelope(*envelope);
                if environment_refresh {
                    self.request_environment_metrics(false);
                }
                if listed_security_scans {
                    self.request_security_scan_projection();
                }
            }
            RuntimeMessage::Event(ClientEvent::Binary(metadata, data)) => {
                let is_terminal_output = metadata.purpose == "terminal_output";
                let follows_active_terminal = is_terminal_output
                    && (self.state.terminals.active_id.is_empty()
                        || self.state.terminals.active_id.as_ref() == metadata.transfer_id);
                if is_terminal_output {
                    self.terminal_emulators
                        .entry(metadata.transfer_id.clone())
                        .or_default()
                        .feed(&data);
                }
                if follows_active_terminal {
                    self.terminal_scroll.scroll_to_bottom();
                }
                self.state.apply_terminal_binary(metadata, &data)
            }
            RuntimeMessage::Event(ClientEvent::ResyncRequired { reason, .. })
            | RuntimeMessage::Event(ClientEvent::Disconnected(reason)) => {
                self.state.connection.connected = false;
                self.state.connection.reconnecting = true;
                self.state.connection.message = reason.into();
            }
            RuntimeMessage::Response { id, result } => match result {
                Ok(value) => {
                    if let Some(pending) = self.pending_requests.remove(&id) {
                        match pending {
                            PendingRequest::CompletionFiles { generation } => {
                                self.completion.receive_files(generation, Ok(value), locale);
                            }
                            PendingRequest::ApprovalMode => {
                                self.approval_picker.open = false;
                                self.approval_picker.error.clear();
                            }
                            PendingRequest::CancelActive { session_id, run_id } => {
                                let cancelled = value
                                    .get("cancelled")
                                    .and_then(serde_json::Value::as_bool)
                                    .unwrap_or(false);
                                let current_session =
                                    self.state.navigation.current_session_id.as_ref() == session_id;
                                let current_run = run_id.is_empty()
                                    || self.state.runtime.run_id.is_empty()
                                    || self.state.runtime.run_id.as_ref() == run_id;
                                if !cancelled && current_session && current_run {
                                    self.state.runtime.running = false;
                                    self.state.runtime.run_id = "".into();
                                    self.state.runtime.activity = "idle".into();
                                    for session in &mut self.state.navigation.sessions {
                                        if session.id.as_ref() == session_id {
                                            session.running = false;
                                        }
                                    }
                                    self.completion.submission_error =
                                        locale.text("error.noActiveRunToStop").to_string();
                                }
                            }
                            PendingRequest::ModelSelection {
                                target,
                                session_id,
                                provider,
                                model,
                                reasoning,
                            } => {
                                tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "ack", request = %id, %reasoning);
                                if let Some(target) = target {
                                    for entry in &mut self.state.catalogs.routes {
                                        if entry["scope"] == target.scope
                                            && entry["role"].as_str().unwrap_or_default()
                                                == target.role
                                        {
                                            entry["route"] = json!({"provider":provider,"model":model,"reasoning":reasoning});
                                            break;
                                        }
                                    }
                                } else if session_id
                                    == self.state.navigation.current_session_id.as_ref()
                                {
                                    self.state.settings.provider = provider.into();
                                    self.state.settings.model = model.into();
                                    self.state.settings.reasoning = reasoning.into();
                                }
                            }
                            PendingRequest::ChatGPTFastMode(enabled) => {
                                self.state.settings.chatgpt_fast_mode = enabled;
                            }
                            PendingRequest::GitBranches { target, .. } => {
                                self.branch_picker.error.clear();
                                self.branch_picker.confirm_target = None;
                                if target.is_some() {
                                    self.branch_picker.open = false;
                                }
                            }
                            PendingRequest::EnvironmentGit => {}
                            PendingRequest::LanguageChange { language } => {
                                self.native_settings.language_saving = false;
                                self.state.settings.language = language.into();
                                self.state.settings.error = "".into();
                            }
                            PendingRequest::SystemFonts { language } => {
                                if language != locale.id() {
                                    return;
                                }
                                self.native_settings.fonts_loading = false;
                                if let Some(fonts) = value.as_array() {
                                    self.native_settings.fonts = fonts.clone();
                                } else {
                                    self.state.settings.error = locale.text("error.fonts").into();
                                }
                            }
                            PendingRequest::SecuritySave { payload } => {
                                self.native_settings.security_busy = false;
                                if let Some(values) = payload.as_object() {
                                    for (key, value) in values {
                                        self.state.security.config[key] = value.clone();
                                    }
                                }
                                self.native_settings.security_baseline = payload;
                                self.native_settings.security_saved = true;
                            }
                            PendingRequest::ExtensionAction { source_text } => {
                                self.extension_settings.busy = false;
                                if source_text.as_deref()
                                    == Some(self.extension_settings.source.read(cx).text())
                                {
                                    self.extension_settings
                                        .source
                                        .update(cx, |input, cx| input.clear(cx));
                                }
                            }
                            PendingRequest::Turn {
                                source_text,
                                selected_skills,
                                attachments,
                                queued_id,
                            } => {
                                if queued_id.is_none()
                                    && self.composer.read(cx).submission_text() == source_text
                                {
                                    self.composer.update(cx, |composer, cx| composer.clear(cx));
                                }
                                if queued_id.is_none()
                                    && self.state.transcript.attachments == attachments
                                {
                                    self.state.transcript.attachments.clear();
                                }
                                if queued_id.is_none()
                                    && self.completion.selected_skills == selected_skills
                                {
                                    self.completion.selected_skills.clear();
                                }
                                if let Some(queued_id) = queued_id {
                                    self.queued_prompts.retain(|item| item.id != queued_id);
                                }
                            }
                            PendingRequest::QueuedGuide {
                                queued_id,
                                prompt,
                                attachments,
                            } => {
                                self.queued_prompts.retain(|item| item.id != queued_id);
                                self.state
                                    .append_optimistic_user(id.as_str(), prompt, attachments);
                            }
                            PendingRequest::ResumeSession { sequence } => {
                                self.state.apply_direct_event(value);
                                if let Some(sequence) = sequence
                                    && let Some(index) =
                                        self.state.transcript.blocks.borrow().iter().position(
                                            |block| {
                                                block
                                                    .extra
                                                    .get("sequence")
                                                    .and_then(serde_json::Value::as_i64)
                                                    == Some(sequence)
                                            },
                                        )
                                {
                                    self.transcript_list.scroll_to_reveal_item(index);
                                }
                            }
                            PendingRequest::ReplyForkTree {
                                session_id,
                                target_id,
                                anchor,
                            } => {
                                if let Some(entry_id) = session_entry_id(&value, anchor) {
                                    let request_id = self.runtime.request(
                                        Method::CreateSessionFork,
                                        json!({
                                            "sessionId": session_id,
                                            "targetId": target_id,
                                            "entryId": entry_id,
                                        }),
                                    );
                                    self.pending_requests.insert(
                                        request_id,
                                        PendingRequest::ReplyFork { target_id },
                                    );
                                } else {
                                    self.completion.submission_error =
                                        locale.text("reply.branchUnavailable").to_string();
                                }
                            }
                            PendingRequest::ReplyFork { target_id } => {
                                let request_id = self.runtime.request(
                                    Method::ResumeSession,
                                    json!({"sessionId": target_id}),
                                );
                                self.pending_requests.insert(
                                    request_id,
                                    PendingRequest::ResumeSession { sequence: None },
                                );
                                self.reply_popover = None;
                                self.state.navigation.surface = Surface::Thread;
                            }
                            PendingRequest::RemoveProject {
                                workspace,
                                next_workspace,
                            } => {
                                self.state
                                    .navigation
                                    .projects
                                    .retain(|project| project.path.as_ref() != workspace);
                                self.open_projects.remove(&workspace);
                                if let Some(next_workspace) = next_workspace {
                                    self.switch_workspace(&next_workspace, "", None, false, cx);
                                }
                            }
                            PendingRequest::Search => {
                                self.state.navigation.search_results =
                                    value.as_array().cloned().unwrap_or_default();
                                self.state.navigation.search_error = "".into();
                            }
                            PendingRequest::Attachment => {
                                self.state.transcript.attachments.push(value)
                            }
                            PendingRequest::Entries => self.state.workspace.file_tree = value,
                            PendingRequest::File => self.state.workspace.selected_file = value,
                            PendingRequest::Changes => self.state.workspace.changes = value,
                            PendingRequest::Change => self.state.workspace.selected_file = value,
                            PendingRequest::PullRequests => {
                                self.state.pull_requests.dashboard = value
                            }
                            PendingRequest::PullRequestDetail => {
                                self.state.pull_requests.selected = value
                            }
                            PendingRequest::Usage => self.state.settings.usage = value,
                            PendingRequest::PullRequestMonitor => {
                                if let Some(number) =
                                    value.get("number").and_then(serde_json::Value::as_i64)
                                {
                                    self.state.pull_requests.monitors.insert(number, value);
                                }
                            }
                            PendingRequest::CreateTerminal => {
                                if let Some(id) = value
                                    .get("id")
                                    .and_then(serde_json::Value::as_str)
                                    .map(str::to_string)
                                {
                                    let mut emulator = TerminalEmulator::default();
                                    emulator.resize(
                                        value
                                            .get("cols")
                                            .and_then(serde_json::Value::as_u64)
                                            .unwrap_or(120)
                                            as usize,
                                        value
                                            .get("rows")
                                            .and_then(serde_json::Value::as_u64)
                                            .unwrap_or(32)
                                            as usize,
                                    );
                                    self.state.terminals.active_id = id.clone().into();
                                    self.state.terminals.sessions.push(value);
                                    self.terminal_emulators.insert(id, emulator);
                                }
                            }
                            PendingRequest::Terminals => {
                                self.state.terminals.sessions =
                                    value.as_array().cloned().unwrap_or_default();
                                if self.state.terminals.active_id.is_empty()
                                    && let Some(id) = self
                                        .state
                                        .terminals
                                        .sessions
                                        .first()
                                        .and_then(|session| session.get("id"))
                                        .and_then(serde_json::Value::as_str)
                                {
                                    self.state.terminals.active_id = id.to_string().into();
                                }
                                if self.terminal_open && self.state.terminals.sessions.is_empty() {
                                    self.request_create_terminal();
                                }
                            }
                        }
                    }
                }
                Err(error) => {
                    let pending = self.pending_requests.remove(&id);
                    if let Some(PendingRequest::CompletionFiles { generation }) = pending {
                        self.completion
                            .receive_files(generation, Err(error), locale);
                        cx.notify();
                        return;
                    }
                    if matches!(&pending, Some(PendingRequest::ApprovalMode)) {
                        self.approval_picker.error = error.clone();
                    }
                    if matches!(&pending, Some(PendingRequest::CancelActive { .. })) {
                        self.state.runtime.activity = if self.state.runtime.running {
                            "running".into()
                        } else {
                            "idle".into()
                        };
                        self.completion.submission_error = error.clone();
                    }
                    if matches!(
                        &pending,
                        Some(
                            PendingRequest::ModelSelection { .. }
                                | PendingRequest::ChatGPTFastMode(_)
                        )
                    ) {
                        self.model_picker_error = error.clone();
                        self.completion.submission_error = error.clone();
                    }
                    if matches!(&pending, Some(PendingRequest::LanguageChange { .. })) {
                        self.native_settings.language_saving = false;
                    }
                    if let Some(PendingRequest::SystemFonts { language }) = &pending {
                        if language != locale.id() {
                            return;
                        }
                        self.native_settings.fonts_loading = false;
                    }
                    if matches!(&pending, Some(PendingRequest::SecuritySave { .. })) {
                        self.native_settings.security_busy = false;
                        self.native_settings.security_saved = false;
                    }
                    if matches!(&pending, Some(PendingRequest::ExtensionAction { .. })) {
                        self.extension_settings.busy = false;
                    }
                    if matches!(
                        &pending,
                        Some(PendingRequest::Turn { .. } | PendingRequest::QueuedGuide { .. })
                    ) {
                        self.state.append_request_error(&id, error.clone());
                    }
                    if let Some(queued_id) = match &pending {
                        Some(PendingRequest::Turn {
                            queued_id: Some(queued_id),
                            ..
                        })
                        | Some(PendingRequest::QueuedGuide { queued_id, .. }) => Some(queued_id),
                        _ => None,
                    } && let Some(item) = self
                        .queued_prompts
                        .iter_mut()
                        .find(|item| item.id == *queued_id)
                    {
                        item.failed = true;
                    }
                    if matches!(&pending, Some(PendingRequest::Turn { .. }))
                        && self.state.runtime.run_id.is_empty()
                    {
                        self.state.runtime.running = false;
                        self.state.runtime.activity = "failed".into();
                    }
                    if matches!(pending, Some(PendingRequest::ResumeSession { .. })) {
                        tracing::warn!(request_id = id, %error, "GPUI resume request failed");
                    }
                    if matches!(
                        &pending,
                        Some(
                            PendingRequest::ReplyForkTree { .. } | PendingRequest::ReplyFork { .. }
                        )
                    ) {
                        self.completion.submission_error = error.clone();
                    }
                    if matches!(pending, Some(PendingRequest::Search)) {
                        self.state.navigation.search_error = error.into();
                    } else if matches!(pending, Some(PendingRequest::EnvironmentGit)) {
                        tracing::warn!(request_id = id, %error, "refresh environment metrics");
                    } else if let Some(PendingRequest::GitBranches { target, confirmed }) = pending
                    {
                        self.branch_picker.open = true;
                        if !confirmed && error.contains("workspace has uncommitted changes") {
                            self.branch_picker.confirm_target = target;
                        } else {
                            self.branch_picker.error = error;
                        }
                    } else {
                        tracing::warn!(request_id = id, %error, "GPUI request failed");
                        self.state.settings.error =
                            if matches!(&pending, Some(PendingRequest::ExtensionAction { .. })) {
                                error.into()
                            } else {
                                format!("{id}: {error}").into()
                            };
                    }
                }
            },
        }
        if old_language != self.state.settings.language {
            self.refresh_language(cx);
        }
        self.native_settings
            .sync_security(&self.state.security.config, cx);
        let new_block_count = self.state.transcript.blocks.borrow().len();
        let new_pending_process = needs_pending_process(
            &self.state.transcript.blocks.borrow(),
            self.state.runtime.running,
        );
        let old_item_count = transcript_item_count(old_block_count, old_pending_process);
        let new_item_count = transcript_item_count(new_block_count, new_pending_process);
        if old_item_count != new_item_count {
            self.transcript_list.reset(new_item_count);
        } else if transcript_layout_changed(
            old_item_count,
            new_item_count,
            old_pending_process,
            new_pending_process,
        ) {
            self.transcript_list
                .splice(old_block_count..old_block_count + 1, 1);
        }
        if old_block_count != new_block_count
            && should_follow_transcript(
                old_session_id.as_ref(),
                self.state.navigation.current_session_id.as_ref(),
                old_block_count,
                new_block_count,
            )
        {
            self.follow_transcript_tail(cx);
        }
        let session_changed = old_session_id != self.state.navigation.current_session_id;
        if session_changed {
            self.completion.dismiss();
            if self.editing_queued_id.take().is_some() {
                self.composer.update(cx, |composer, cx| composer.clear(cx));
                self.state.transcript.attachments.clear();
                self.completion.selected_skills.clear();
            }
        }
        self.sync_completions(cx);
        if session_changed && !self.state.navigation.current_session_id.is_empty() {
            self.runtime
                .set_snapshot_session(self.state.navigation.current_session_id.to_string());
        }
        if self.state.runtime.running
            && !self.state.runtime.run_id.is_empty()
            && old_run_id != self.state.runtime.run_id
        {
            self.schedule_run_elapsed_tick(self.state.runtime.run_id.to_string(), cx);
        }
        if should_start_next_queued(old_runtime_busy, session_changed, &self.state) {
            self.start_next_queued(cx);
        }
    }

    pub(super) fn refresh_transcript_layout(&mut self, index: usize) {
        let block_count = self.state.transcript.blocks.borrow().len();
        let item_count = transcript_item_count(
            block_count,
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        );
        if item_count > 0 && index < item_count {
            self.transcript_list.remeasure_items(index..index + 1);
        }
    }

    pub(super) fn schedule_run_elapsed_tick(&mut self, run_id: String, cx: &mut Context<Self>) {
        let timer = cx.background_executor().timer(Duration::from_secs(1));
        cx.spawn(async move |this, cx| {
            timer.await;
            let _ = this.update(cx, move |this, cx| {
                if this.state.runtime.running && this.state.runtime.run_id.as_ref() == run_id {
                    cx.notify();
                    this.schedule_run_elapsed_tick(run_id, cx);
                }
            });
        })
        .detach();
    }
}
