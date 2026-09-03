use super::*;

impl AzemWindow {
    pub(super) fn send_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.send_current(cx);
    }

    pub(super) fn submit_message(
        &mut self,
        _: &Submit,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.renaming_session_id.is_some() {
            if !self.session_rename_input.read(cx).is_composing() {
                self.commit_session_rename(window, cx);
            }
            return;
        }
        // Settings inputs must never submit a draft in the conversation behind the modal.
        if self.settings_open {
            return;
        }
        if self.composer.read(cx).is_composing() {
            return;
        }
        if self.completion.is_open() {
            return;
        }
        if self.approval_picker.open || self.approval_picker.focus.is_focused(window) {
            if self.approval_picker.open {
                self.change_approval_mode(APPROVAL_MODES[self.approval_picker.index].0, cx);
            } else {
                self.toggle_approval_picker(window, cx);
            }
            return;
        }
        if self.branch_picker.open {
            self.branch_picker.focus.focus(window, cx);
            self.select_highlighted_branch(cx);
            return;
        }
        if self.terminal_open && self.terminal_input.focus_handle(cx).is_focused(window) {
            self.write_terminal_data("\r");
            return;
        }
        match self.state.navigation.surface {
            Surface::Terminal => self.write_terminal_data("\r"),
            Surface::Search if self.search_input.read(cx).text().trim().is_empty() => {
                self.runtime
                    .request(Method::Execute, json!({"kind": "new_session"}));
                self.state.navigation.surface = Surface::Thread;
                cx.notify();
            }
            Surface::Search => self.search_current(cx),
            _ => self.send_current(cx),
        }
    }

    pub(super) fn toggle_search(
        &mut self,
        _: &ToggleSearch,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.settings_open {
            self.native_settings
                .search
                .focus_handle(cx)
                .focus(window, cx);
            return;
        }
        if self.state.navigation.surface == Surface::Search {
            self.state.navigation.surface = self.search_return_surface;
        } else {
            self.search_return_surface = self.state.navigation.surface;
            self.state.navigation.surface = Surface::Search;
            self.search_input.focus_handle(cx).focus(window, cx);
        }
        cx.notify();
    }

    pub(super) fn find_settings(
        &mut self,
        _: &FindSettings,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.settings_open {
            self.native_settings
                .search
                .focus_handle(cx)
                .focus(window, cx);
        }
    }

    pub(super) fn set_appearance(
        &mut self,
        key: &str,
        value: serde_json::Value,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let locale = Locale::resolve(&self.state.settings.language);
        let result = (|| -> anyhow::Result<AppearancePreferences> {
            let next = AppearancePreferences::current(cx).changed(key, value)?;
            let path = self
                .window_state_path
                .as_ref()
                .ok_or_else(|| anyhow::anyhow!(locale.text("error.appearanceDirectory")))?
                .with_file_name("gpui-appearance.json");
            next.save(&path)?;
            Ok(next)
        })();
        match result {
            Ok(preferences) => {
                self.state.settings.appearance = json!(preferences);
                cx.set_global(preferences);
                self.focus.focus(window, cx);
                self.native_settings.font_menu_open = false;
                self.state.settings.error = "".into();
                // Re-measure the virtual transcript when typography changes.
                self.transcript_list.reset(transcript_item_count(
                    self.state.transcript.blocks.borrow().len(),
                    needs_pending_process(
                        &self.state.transcript.blocks.borrow(),
                        self.state.runtime.running,
                    ),
                ));
                cx.refresh_windows();
            }
            Err(error) => {
                self.state.settings.error = locale
                    .format("error.appearanceSave", &[("error", error.to_string())])
                    .into()
            }
        }
        cx.notify();
    }

    pub(super) fn change_language(&mut self, language: &str, cx: &mut Context<Self>) {
        self.native_settings.language_menu_open = false;
        if self.native_settings.language_saving
            || !self.state.connection.connected
            || self.state.settings.language.as_ref() == language
            || !localization::available()
                .iter()
                .any(|pack| pack.id == language)
        {
            cx.notify();
            return;
        }
        self.native_settings.language_saving = true;
        self.state.settings.error = "".into();
        let id = self.runtime.request(Method::Execute, json!({
        "kind":"set_language", "target":language, "sessionId":self.state.navigation.current_session_id
    }));
        self.pending_requests.insert(
            id,
            PendingRequest::LanguageChange {
                language: language.to_string(),
            },
        );
        cx.notify();
    }

    pub(super) fn refresh_language(&mut self, cx: &mut Context<Self>) {
        let locale = Locale::resolve(&self.state.settings.language);
        for (input, key) in [
            (&self.composer, "ui.describeTheTaskReferenceFilesUseSkills"),
            (&self.search_input, "ui.searchConversationsAndMessages"),
            (&self.session_rename_input, "sidebar.renameSession"),
            (&self.model_search, "ui.searchModelsOrProviders"),
            (&self.branch_picker.search, "branch.search"),
            (&self.settings_provider_search, "ui.searchProviders"),
            (
                &self.settings_model_search,
                "ui.searchModelFamilyVersionOrRawId",
            ),
            (&self.extension_settings.search, "ui.searchExtensions"),
            (
                &self.extension_settings.source,
                "ui.marketplaceOwnerRepoGitUrlOrLocalPath",
            ),
            (&self.native_settings.search, "input.settingsSearch"),
            (&self.native_settings.font_search, "input.fontSearch"),
            (&self.terminal_input, "input.terminal"),
        ] {
            input.update(cx, |input, cx| input.set_placeholder(locale.text(key), cx));
        }
        self.native_settings.fonts.clear();
        if self.settings_open && self.settings_section == "appearance" {
            self.request_system_fonts();
        }
        self.transcript_list.reset(transcript_item_count(
            self.state.transcript.blocks.borrow().len(),
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        ));
        cx.notify();
    }

    pub(super) fn request_system_fonts(&mut self) {
        let language = Locale::resolve(&self.state.settings.language)
            .id()
            .to_string();
        self.native_settings.fonts_loading = true;
        let id = self
            .runtime
            .request(Method::SystemFonts, json!({"language":language}));
        self.pending_requests
            .insert(id, PendingRequest::SystemFonts { language });
    }

    pub(super) fn select_settings_section(&mut self, section: &str, cx: &mut Context<Self>) {
        self.native_settings.language_menu_open = false;
        self.settings_section = section.to_string();
        self.model_picker_open = false;
        self.route_picker_target = None;
        self.subagent_setting_menu = None;
        self.archive_days_menu_open = false;
        self.native_settings.font_menu_open = false;
        self.native_settings
            .search
            .update(cx, |input, cx| input.clear(cx));
        if section == "catalog" {
            self.settings_provider_search
                .update(cx, |input, cx| input.clear(cx));
            self.settings_model_search
                .update(cx, |input, cx| input.clear(cx));
        }
        if section == "appearance"
            && self.native_settings.fonts.is_empty()
            && !self.native_settings.fonts_loading
        {
            self.request_system_fonts();
        }
        if section == "security" {
            self.runtime.request(Method::Execute, json!({"kind":"get_security_config", "sessionId":self.state.navigation.current_session_id}));
        }
        if section == "usage" {
            let id = self
                .runtime
                .request(Method::UsageReport, json!({"scope":"project"}));
            self.pending_requests.insert(id, PendingRequest::Usage);
        }
        cx.notify();
    }

    pub(super) fn save_security_settings(&mut self, cx: &mut Context<Self>) {
        if self.native_settings.security_busy || !self.state.connection.connected {
            return;
        }
        let payload = match self
            .native_settings
            .security_payload(cx, Locale::resolve(&self.state.settings.language))
        {
            Ok(payload) => payload,
            Err(error) => {
                self.state.settings.error = error.into();
                cx.notify();
                return;
            }
        };
        self.state.settings.error = "".into();
        self.native_settings.security_busy = true;
        self.native_settings.security_saved = false;
        let id = self.runtime.request(Method::Execute, json!({
        "kind":"set_security_config", "sessionId":self.state.navigation.current_session_id, "payload":payload,
    }));
        self.pending_requests
            .insert(id, PendingRequest::SecuritySave { payload });
        cx.notify();
    }

    pub(super) fn toggle_settings(
        &mut self,
        _: &ToggleSettings,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.completion.dismiss();
        self.approval_picker.open = false;
        self.branch_picker.open = false;
        self.settings_open = !self.settings_open;
        if self.settings_open {
            self.settings_provider_search
                .update(cx, |search, cx| search.clear(cx));
            self.settings_model_search
                .update(cx, |search, cx| search.clear(cx));
        }
        if !self.settings_open {
            self.model_picker_open = false;
            self.route_picker_target = None;
            self.subagent_setting_menu = None;
            self.archive_days_menu_open = false;
        }
        if self.settings_open && self.state.catalogs.providers.is_empty() {
            self.refresh_model_catalog();
        }
        cx.notify();
    }

    pub(super) fn set_terminal_open(
        &mut self,
        open: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.terminal_open = open;
        if open {
            self.request_surface(Surface::Terminal);
            self.terminal_scroll.scroll_to_bottom();
            self.terminal_input.focus_handle(cx).focus(window, cx);
        } else if self.terminal_input.focus_handle(cx).is_focused(window) {
            self.focus.focus(window, cx);
        }
    }

    pub(super) fn toggle_terminal(
        &mut self,
        _: &ToggleTerminal,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.set_terminal_open(!self.terminal_open, window, cx);
        cx.notify();
    }

    pub(super) fn toggle_terminal_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.set_terminal_open(!self.terminal_open, window, cx);
        cx.notify();
    }

    pub(super) fn dismiss_picker(
        &mut self,
        _: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.reasoning_drag = None;
        self.completion.dismiss();
        self.approval_picker.open = false;
        self.branch_picker.open = false;
        self.model_picker_open = false;
        self.route_picker_target = None;
        self.context_popover_open = false;
        self.reply_popover = None;
        self.native_settings.language_menu_open = false;
        self.native_settings.font_menu_open = false;
        self.subagent_setting_menu = None;
        self.archive_days_menu_open = false;
        self.side_panel_add_menu_open = false;
        // Dismiss the popup without also activating a control underneath it.
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn dismiss_sidebar_context_menu(
        &mut self,
        _: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.sidebar_context_menu = None;
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn run_sidebar_menu_action(
        &mut self,
        action: SidebarMenuAction,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(menu) = self.sidebar_context_menu.take() else {
            return;
        };
        match menu.target {
            SidebarMenuTarget::Session {
                id,
                title,
                workspace,
                pinned,
            } => self.run_session_menu_action(action, (id, title, workspace, pinned), window, cx),
            SidebarMenuTarget::Project { workspace } => {
                self.run_project_menu_action(action, workspace, cx)
            }
        }
        cx.notify();
    }

    pub(super) fn run_session_menu_action(
        &mut self,
        action: SidebarMenuAction,
        target: (String, String, String, bool),
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (id, title, workspace, pinned) = target;
        match action {
            SidebarMenuAction::ToggleSessionPin => {
                self.runtime.request(
                Method::Execute,
                json!({"kind":"pin_session","target":id,"decision":(!pinned).to_string(),"sessionId":id}),
            );
            }
            SidebarMenuAction::RenameSession => {
                self.renaming_session_id = Some(id);
                self.session_rename_input
                    .update(cx, |input, cx| input.set_text(&title, cx));
                self.session_rename_input.focus_handle(cx).focus(window, cx);
            }
            SidebarMenuAction::MarkSessionUnread => {
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":"mark_session_unread","target":id,"sessionId":id}),
                );
            }
            SidebarMenuAction::ArchiveSession => {
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":"archive_session","target":id,"sessionId":id}),
                );
            }
            SidebarMenuAction::CopyWorkspace => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(workspace))
            }
            SidebarMenuAction::CopySessionId => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(id))
            }
            _ => {}
        }
    }

    pub(super) fn run_project_menu_action(
        &mut self,
        action: SidebarMenuAction,
        workspace: String,
        cx: &mut Context<Self>,
    ) {
        match action {
            SidebarMenuAction::RevealProject => {
                if let Err(error) = reveal_project_path(&workspace) {
                    self.state.settings.error = error.to_string().into();
                }
            }
            SidebarMenuAction::ArchiveProjectSessions => {
                let active = self.state.workspace.root.as_ref() == workspace;
                let session_ids = self
                    .state
                    .navigation
                    .sessions
                    .iter()
                    .filter(|session| {
                        !session.archived
                            && (session.workspace.as_ref() == workspace
                                || (session.workspace.is_empty() && active))
                    })
                    .map(|session| session.id.to_string())
                    .collect::<Vec<_>>();
                for id in session_ids {
                    self.runtime.request(
                        Method::Execute,
                        json!({"kind":"archive_session","target":id,"sessionId":id}),
                    );
                }
            }
            SidebarMenuAction::RemoveProject => {
                let active = self.state.workspace.root.as_ref() == workspace;
                let next_workspace = if active {
                    self.state
                        .navigation
                        .projects
                        .iter()
                        .map(|project| project.path.as_ref())
                        .find(|project| !project.is_empty() && *project != workspace)
                        .map(str::to_string)
                } else {
                    None
                };
                if active && next_workspace.is_none() {
                    self.state.settings.error = Locale::resolve(&self.state.settings.language)
                        .text("sidebar.removeOnlyProject")
                        .into();
                    return;
                }
                let request_id = self.runtime.request(
                    Method::Execute,
                    json!({"kind":"remove_project","target":workspace}),
                );
                self.pending_requests.insert(
                    request_id,
                    PendingRequest::RemoveProject {
                        workspace,
                        next_workspace,
                    },
                );
            }
            SidebarMenuAction::CopyProjectPath => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(workspace))
            }
            _ => {}
        }
    }

    pub(super) fn commit_session_rename(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(session_id) = self.renaming_session_id.clone() else {
            return;
        };
        let name = self.session_rename_input.read(cx).text().trim().to_string();
        if name.is_empty() {
            self.state.settings.error = Locale::resolve(&self.state.settings.language)
                .text("sidebar.renameEmpty")
                .into();
            cx.notify();
            return;
        }
        self.runtime.request(
            Method::Execute,
            json!({"kind":"rename_session","target":session_id,"name":name,"sessionId":session_id}),
        );
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.notify();
    }

    pub(super) fn cancel_session_rename(
        &mut self,
        _: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn cancel_session_rename_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.notify();
    }

    pub(super) fn commit_session_rename_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.commit_session_rename(window, cx);
    }

    pub(super) fn close_overlay(
        &mut self,
        _: &CloseOverlay,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.reasoning_drag = None;
        if self.renaming_session_id.is_some() {
            self.renaming_session_id = None;
            self.session_rename_input
                .update(cx, |input, cx| input.clear(cx));
        } else if self.sidebar_context_menu.is_some() {
            self.sidebar_context_menu = None;
        } else if self.completion.is_open() {
            self.completion.dismiss();
        } else if self.approval_picker.open {
            self.approval_picker.open = false;
        } else if self.branch_picker.open {
            self.branch_picker.open = false;
        } else if self.native_settings.language_menu_open {
            self.native_settings.language_menu_open = false;
        } else if self.native_settings.font_menu_open {
            self.native_settings.font_menu_open = false;
        } else if self.settings_open && !self.native_settings.search.read(cx).text().is_empty() {
            self.native_settings
                .search
                .update(cx, |input, cx| input.clear(cx));
        } else if self.subagent_setting_menu.is_some() {
            self.subagent_setting_menu = None;
        } else if self.archive_days_menu_open {
            self.archive_days_menu_open = false;
        } else if self.side_panel_add_menu_open {
            self.side_panel_add_menu_open = false;
        } else if self.model_picker_open && self.route_picker_target.is_some() {
            self.model_picker_open = false;
            self.route_picker_target = None;
        } else if self.settings_open {
            self.settings_open = false;
        } else if self.model_picker_open {
            self.model_picker_open = false;
        } else if self.context_popover_open {
            self.context_popover_open = false;
        } else if self.reply_popover.is_some() {
            self.reply_popover = None;
        } else if self.state.navigation.surface == Surface::Search {
            self.state.navigation.surface = self.search_return_surface;
        }
        cx.notify();
    }

    pub(super) fn fork_reply(&mut self, anchor: ReplyForkAnchor, cx: &mut Context<Self>) {
        let session_id = self.state.navigation.current_session_id.to_string();
        if session_id.is_empty()
            || self.pending_requests.values().any(|pending| {
                matches!(
                    pending,
                    PendingRequest::ReplyForkTree { .. } | PendingRequest::ReplyFork { .. }
                )
            })
        {
            return;
        }
        let target_id = format!("session-{}", uuid::Uuid::new_v4());
        let request_id = self
            .runtime
            .request(Method::SessionTree, json!({"sessionId": session_id}));
        self.pending_requests.insert(
            request_id,
            PendingRequest::ReplyForkTree {
                session_id,
                target_id,
                anchor,
            },
        );
        self.completion.submission_error.clear();
        self.reply_popover = None;
        cx.notify();
    }

    pub(super) fn send_current(&mut self, cx: &mut Context<Self>) {
        if self.branch_request_pending() || self.approval_request_pending() {
            return;
        }
        let source_text = self.composer.read(cx).submission_text();
        let prompt = source_text.trim().to_string();
        let selected_skills = self.completion.selected_skills.clone();
        let prepared_prompt = match prepare_prompt(
            &prompt,
            &selected_skills,
            &self.state.catalogs.skills,
            Locale::resolve(&self.state.settings.language),
        ) {
            Ok((prompt, _)) => prompt,
            Err(error) => {
                self.completion.submission_error = error;
                cx.notify();
                return;
            }
        };
        let attachments = self.state.transcript.attachments.clone();
        if !composer_has_submission(&prepared_prompt, &attachments) {
            return;
        }
        if let Some(id) = self.editing_queued_id.clone() {
            if let Some(item) = self.queued_prompts.iter_mut().find(|item| {
                item.id == id
                    && item.session_id == self.state.navigation.current_session_id.as_ref()
            }) {
                item.prompt = prompt;
                item.selected_skills = selected_skills;
                item.attachments = attachments;
                item.failed = false;
                self.editing_queued_id = None;
                self.composer.update(cx, |composer, cx| composer.clear(cx));
                self.state.transcript.attachments.clear();
                self.completion.selected_skills.clear();
                self.start_next_queued(cx);
                cx.notify();
                return;
            }
            self.editing_queued_id = None;
        }
        if !self.state.connection.connected {
            return;
        }
        if runtime_busy(&self.state) {
            self.queued_prompts.push(QueuedPrompt {
                id: uuid::Uuid::new_v4().to_string(),
                session_id: self.state.navigation.current_session_id.to_string(),
                prompt,
                selected_skills,
                attachments: attachments.clone(),
                failed: false,
            });
            self.composer.update(cx, |composer, cx| composer.clear(cx));
            self.state.transcript.attachments.clear();
            self.completion.selected_skills.clear();
            self.scroll_transcript_to_bottom(cx);
            cx.notify();
            return;
        }
        self.start_turn(prompt, selected_skills, attachments, None, cx);
    }

    pub(super) fn start_turn(
        &mut self,
        prompt: String,
        selected_skills: Vec<String>,
        attachments: Vec<serde_json::Value>,
        queued_id: Option<String>,
        cx: &mut Context<Self>,
    ) {
        let payload =
            match turn_payload(&self.state, &prompt, &selected_skills, attachments.clone()) {
                Ok(prepared) => prepared,
                Err(error) => {
                    self.completion.submission_error = error;
                    if let Some(item) = self
                        .queued_prompts
                        .iter_mut()
                        .find(|item| Some(&item.id) == queued_id.as_ref())
                    {
                        item.failed = true;
                    }
                    cx.notify();
                    return;
                }
            };
        let prompt = payload["prompt"].as_str().unwrap_or_default().to_owned();
        let request_id = self.runtime.request(Method::StartTurn, payload);
        self.state
            .append_optimistic_user(request_id.as_str(), prompt, attachments.clone());
        self.state.runtime.running = true;
        self.state.runtime.activity = "starting".into();
        self.scroll_transcript_to_bottom(cx);
        self.pending_requests.insert(
            request_id,
            PendingRequest::Turn {
                source_text: self.composer.read(cx).submission_text(),
                selected_skills,
                attachments,
                queued_id,
            },
        );
        cx.notify();
    }

    pub(super) fn scroll_transcript_to_bottom(&mut self, cx: &mut Context<Self>) {
        let block_count = self.state.transcript.blocks.borrow().len();
        let item_count = transcript_item_count(
            block_count,
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        );
        let old_item_count = self.transcript_list.item_count();
        let changed_from = old_item_count.saturating_sub(1);
        self.transcript_list.splice(
            changed_from..old_item_count,
            item_count.saturating_sub(changed_from),
        );
        self.follow_transcript_tail(cx);
    }

    pub(super) fn follow_transcript_tail(&mut self, cx: &mut Context<Self>) {
        self.transcript_list.set_follow_mode(FollowMode::Tail);
        let session_id = self.state.navigation.current_session_id.clone();
        let timer = cx.background_executor().timer(Duration::from_millis(50));
        cx.spawn(async move |this, cx| {
            timer.await;
            let _ = this.update(cx, |this, cx| {
                if this.state.navigation.current_session_id == session_id {
                    if let Some(last) = this.transcript_list.item_count().checked_sub(1) {
                        this.transcript_list.scroll_to_reveal_item(last);
                    }
                    this.transcript_list.set_follow_mode(FollowMode::Tail);
                    cx.notify();
                }
            });
        })
        .detach();
    }

    pub(super) fn start_next_queued(&mut self, cx: &mut Context<Self>) {
        if runtime_busy(&self.state) || self.editing_queued_id.is_some() {
            return;
        }
        let session_id = self.state.navigation.current_session_id.as_ref();
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| item.session_id == session_id)
            .cloned()
        else {
            return;
        };
        if item.failed {
            return;
        }
        self.start_turn(
            item.prompt,
            item.selected_skills,
            item.attachments,
            Some(item.id),
            cx,
        );
    }

    pub(super) fn delete_queued(&mut self, id: &str, cx: &mut Context<Self>) {
        if self.editing_queued_id.as_deref() == Some(id) {
            self.editing_queued_id = None;
            self.composer.update(cx, |composer, cx| composer.clear(cx));
            self.state.transcript.attachments.clear();
            self.completion.selected_skills.clear();
        }
        self.queued_prompts.retain(|item| item.id != id);
        self.start_next_queued(cx);
        cx.notify();
    }

    pub(super) fn edit_queued(&mut self, id: &str, window: &mut Window, cx: &mut Context<Self>) {
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| {
                item.id == id
                    && item.session_id == self.state.navigation.current_session_id.as_ref()
            })
            .cloned()
        else {
            return;
        };
        self.editing_queued_id = Some(item.id);
        self.composer
            .update(cx, |composer, cx| composer.set_text(&item.prompt, cx));
        self.completion.selected_skills = item.selected_skills;
        self.state.transcript.attachments = item.attachments;
        self.composer.focus_handle(cx).focus(window, cx);
        cx.notify();
    }

    pub(super) fn reorder_queued(
        &mut self,
        dragged: &QueuedPromptDrag,
        target_id: &str,
        cx: &mut Context<Self>,
    ) {
        if dragged.session_id == self.state.navigation.current_session_id.as_ref()
            && reorder_session_queue(
                &mut self.queued_prompts,
                &dragged.session_id,
                &dragged.id,
                target_id,
            )
        {
            cx.notify();
        }
    }

    pub(super) fn guide_queued(&mut self, id: &str, cx: &mut Context<Self>) {
        if !self.state.runtime.running {
            return;
        }
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| item.id == id)
            .cloned()
        else {
            return;
        };
        if !item.selected_skills.is_empty() || !self.can_guide_prompt(&item.prompt) {
            self.completion.submission_error = Locale::resolve(&self.state.settings.language)
                .text("completion.skillNextTurn")
                .into();
            cx.notify();
            return;
        }
        let request_id = self.runtime.request(
            Method::Guide,
            json!({
                "sessionId": item.session_id,
                "runId": self.state.runtime.run_id,
                "text": item.prompt,
                "attachments": item.attachments,
            }),
        );
        self.pending_requests.insert(
            request_id,
            PendingRequest::QueuedGuide {
                queued_id: item.id,
                prompt: item.prompt,
                attachments: item.attachments,
            },
        );
        cx.notify();
    }

    pub(super) fn guide_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let text = self.composer.read(cx).submission_text().trim().to_string();
        if !self.state.runtime.running {
            return;
        }
        if !self.completion.selected_skills.is_empty() || !self.can_guide_prompt(&text) {
            self.completion.submission_error = Locale::resolve(&self.state.settings.language)
                .text("completion.skillNextTurn")
                .into();
            cx.notify();
            return;
        }
        if text.is_empty() {
            return;
        }
        self.runtime.request(
            Method::Guide,
            json!({
                "sessionId": self.state.navigation.current_session_id,
                "runId": self.state.runtime.run_id,
                "text": text,
                "attachments": self.state.transcript.attachments,
            }),
        );
        self.composer.update(cx, |composer, cx| composer.clear(cx));
    }

    pub(super) fn can_guide_prompt(&self, text: &str) -> bool {
        prepare_prompt(
            text,
            &[],
            &self.state.catalogs.skills,
            Locale::resolve(&self.state.settings.language),
        )
        .is_ok_and(|(_, skills)| skills.is_empty())
    }

    pub(super) fn attach_file(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let locale = Locale::resolve(&self.state.settings.language);
        let runtime = self.runtime.clone();
        let session_id = self.state.navigation.current_session_id.to_string();
        cx.spawn(async move |this, cx| {
            if let Some(file) = rfd::AsyncFileDialog::new()
                .add_filter(
                    locale.text("files.images"),
                    &["png", "jpg", "jpeg", "gif", "webp"],
                )
                .pick_file()
                .await
            {
                let path = file.path().to_path_buf();
                let mime_type = match path
                    .extension()
                    .and_then(|extension| extension.to_str())
                    .unwrap_or_default()
                    .to_ascii_lowercase()
                    .as_str()
                {
                    "png" => "image/png",
                    "jpg" | "jpeg" => "image/jpeg",
                    "gif" => "image/gif",
                    "webp" => "image/webp",
                    _ => return,
                };
                let id =
                    runtime.upload_attachment(session_id, path, file.file_name(), mime_type.into());
                let _ = this.update(cx, |this, cx| {
                    this.pending_requests.insert(id, PendingRequest::Attachment);
                    cx.notify();
                });
            }
        })
        .detach();
    }

    pub(super) fn remove_attachment(&mut self, index: usize, cx: &mut Context<Self>) {
        if index < self.state.transcript.attachments.len() {
            self.state.transcript.attachments.remove(index);
            cx.notify();
        }
    }

    pub(super) fn cancel_active(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        if self
            .pending_requests
            .values()
            .any(|request| matches!(request, PendingRequest::CancelActive { .. }))
        {
            return;
        }
        let Some((session_id, run_id)) = stoppable_run(&self.state) else {
            return;
        };
        self.state.runtime.activity = "stopping".into();
        let id = self.runtime.request(
            Method::CancelActive,
            json!({
                "sessionId": session_id,
                "runId": run_id,
                "includeChildren": true,
            }),
        );
        self.pending_requests
            .insert(id, PendingRequest::CancelActive { session_id, run_id });
        cx.notify();
    }

    pub(super) fn new_session(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.runtime
            .request(Method::Execute, json!({"kind": "new_session"}));
        self.state.navigation.surface = Surface::Thread;
        cx.notify();
    }

    pub(super) fn search_current(&mut self, cx: &mut Context<Self>) {
        let query = self.search_input.read(cx).text().trim().to_string();
        if query.is_empty() || !self.state.connection.connected {
            return;
        }
        self.state.navigation.search_error = "".into();
        let id = self
            .runtime
            .request(Method::SearchSessions, json!({"query": query, "limit": 30}));
        self.pending_requests.insert(id, PendingRequest::Search);
    }
}
