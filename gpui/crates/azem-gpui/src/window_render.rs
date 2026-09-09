use super::*;
pub(crate) fn thread_status(state: &AppState) -> (&'static str, bool) {
    if state.runtime.activity.as_ref() == "stopping" && stoppable_run(state).is_some() {
        ("ui.stopping", false)
    } else if pending_question_run_id(state).is_some() {
        ("ui.awaitingInput", false)
    } else if state.runtime.running {
        ("ui.running2", true)
    } else {
        ("ui.ready", false)
    }
}

impl Render for AzemWindow {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.reconcile_side_panel_layout(window);
        self.advance_side_panel_animation(window);
        let locale = Locale::resolve(&self.state.settings.language);
        let preferences = AppearancePreferences::current(cx);
        window.set_rem_size(px(16. * preferences.ui_font_size / 14.));
        let palette = ThemePalette::for_window(window, cx);
        let labels = labels(&self.state.settings.language);
        let requested_surface = self.state.navigation.surface;
        let search_open = requested_surface == Surface::Search;
        let surface = if search_open {
            self.search_return_surface
        } else {
            requested_surface
        };
        let model_popup_visible = self.model_picker_open
            && !search_open
            && if self.settings_open {
                self.route_picker_target
                    .as_ref()
                    .is_some_and(|target| target.kind == RoutePickerKind::Model)
            } else {
                surface == Surface::Thread && self.route_picker_target.is_none()
            };
        if !model_popup_visible {
            self.reasoning_drag = None;
        }
        let animate_fast = model_popup_visible && {
            let modes = self.selected_model_modes();
            fast_particles::should_animate(
                !modes.levels.is_empty(),
                modes.fast,
                preferences.reduced_motion,
                window.is_window_active(),
            )
        };
        self.fast_particles
            .update(cx, |particles, cx| particles.set_active(animate_fast, cx));
        let block_count = self.state.transcript.blocks.borrow().len();
        let empty_thread =
            surface == Surface::Thread && block_count == 0 && !self.state.runtime.running;
        let (thread_status_key, thread_status_active) = thread_status(&self.state);
        let surface_title = match surface {
            Surface::Thread if !self.state.navigation.current_title.is_empty() => {
                self.state.navigation.current_title.to_string()
            }
            Surface::Thread => labels.new_conversation.to_string(),
            Surface::Search => labels.search.to_string(),
            Surface::Projects => labels.workspace.to_string(),
            Surface::Files => labels.files.to_string(),
            Surface::Changes => labels.changes.to_string(),
            Surface::PullRequests => labels.pull_requests.to_string(),
            Surface::Security => labels.security.to_string(),
            Surface::Terminal => labels.terminal.to_string(),
        };
        let current_workspace_width = workspace_width(f32::from(window.bounds().size.width));
        let content = match surface {
            Surface::Thread if empty_thread => {
                let composer = self.composer_view(palette, labels, true, cx);
                let queue = self.queued_prompts_view(palette, labels, cx);
                div()
                    .id("empty-thread")
                    .role(Role::Region)
                    .aria_label(labels.new_conversation)
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .bg(palette.paper)
                    .px(px(chat_column_gutter(current_workspace_width)))
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(
                        div()
                            .w_full()
                            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                            .mt(px(-46.))
                            .flex()
                            .flex_col()
                            .child(
                                div()
                                    .mb(px(22.))
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(px(36.))
                                            .line_height(px(40.))
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .text_color(palette.ink)
                                            .child(labels.prompt_title),
                                    )
                                    .child(
                                        div()
                                            .max_w(px(540.))
                                            .text_size(px(13.))
                                            .line_height(px(21.))
                                            .text_color(palette.muted)
                                            .child(labels.prompt_subtitle),
                                    ),
                            )
                            .child(
                                div()
                                    .w_full()
                                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                                    .flex()
                                    .flex_col()
                                    .when_some(queue, |stack, queue| stack.child(queue))
                                    .child(composer),
                            ),
                    )
                    .into_any_element()
            }
            Surface::Thread => {
                let blocks = self.state.transcript.blocks.clone();
                let transcript_content_item_count = block_count
                    + usize::from(needs_pending_process(
                        &self.state.transcript.blocks.borrow(),
                        self.state.runtime.running,
                    ));
                let agents = self.state.runtime.agents.clone();
                let live_elapsed_ms =
                    if self.state.runtime.running && self.state.runtime.run_started_at_ms > 0 {
                        (unix_millis() - self.state.runtime.run_started_at_ms).max(0)
                    } else {
                        0
                    };
                let reply_actions = ReplyActionsSnapshot {
                    hooks: self.state.runtime.hooks.clone(),
                    hook_catalog: self.state.catalogs.hooks.clone(),
                    popover: self.reply_popover.clone(),
                    feedback: self.reply_feedback.clone(),
                    hovered_message: self.hovered_message.clone(),
                };
                let process_expansion = self.process_expansion.clone();
                let owner = cx.entity();
                let locale = Locale::resolve(&self.state.settings.language);
                let reduced_motion = self
                    .state
                    .settings
                    .appearance
                    .get("reducedMotion")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false);
                let selected_agent = self.state.runtime.selected_agent_id.to_string();
                let panel_visible = self.side_panel_open || self.side_panel_closing;
                let agent_panel_visible = self.side_panel_agents_open && panel_visible;
                let side_panel_visible = !self.side_panel_agents_open && panel_visible;
                let side_panel_layout_width = if panel_visible {
                    self.side_panel_width
                } else {
                    0.
                };
                let environment_returning = !self.side_panel_agents_open
                    && selected_agent.is_empty()
                    && self.environment_open
                    && self.side_panel_closing
                    && environment_panel_fits(current_workspace_width, 0.);
                let environment_visible = !self.side_panel_agents_open
                    && selected_agent.is_empty()
                    && self.environment_open
                    && (environment_returning
                        || environment_panel_fits(
                            current_workspace_width,
                            side_panel_layout_width,
                        ));
                let environment = if environment_visible {
                    Some(environment_panel(
                        self,
                        palette,
                        labels,
                        self.environment_expanded.as_deref(),
                        if environment_returning {
                            0.
                        } else {
                            side_panel_layout_width
                        },
                        cx,
                    ))
                } else {
                    None
                };
                let side_panel = if side_panel_visible {
                    Some(side_panel(self, palette, labels, cx))
                } else {
                    None
                };
                let agent_detail = if !agent_panel_visible {
                    None
                } else {
                    Some(agent_side_panel(
                        self,
                        palette,
                        &selected_agent,
                        reduced_motion,
                        process_expansion.clone(),
                        owner.clone(),
                        cx,
                    ))
                };
                let right_panel_width = if environment_returning {
                    self.side_panel_visible_width
                        .max(ENVIRONMENT_PANEL_RESERVED_WIDTH)
                } else {
                    side_panel_layout_width
                        + if environment.is_some() {
                            ENVIRONMENT_PANEL_RESERVED_WIDTH
                        } else {
                            0.
                        }
                };
                let column_gutter =
                    chat_column_gutter((current_workspace_width - right_panel_width).max(0.));
                let column_animation_offset = if environment_returning {
                    0.
                } else {
                    chat_column_animation_offset(
                        side_panel_layout_width,
                        self.side_panel_visible_width,
                    )
                };
                let composer = self.composer_view(palette, labels, false, cx);
                let queue = self.queued_prompts_view(palette, labels, cx);
                div()
                    .id("active-thread")
                    .role(Role::Region)
                    .aria_label(locale.text("conversation.transcript"))
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .bg(palette.paper)
                    .flex()
                    .flex_col()
                    .child(
                        div()
                            .id("transcript")
                            .role(Role::Log)
                            .aria_label(locale.text("conversation.transcript"))
                            .flex_1()
                            .min_h_0()
                            .overflow_hidden()
                            .pr(px(right_panel_width))
                            .flex()
                            .flex_col()
                            .child(
                                list(self.transcript_list.clone(), move |index, _, _| {
                                    if index == transcript_content_item_count {
                                        div()
                                            .id("transcript-composer-clearance")
                                            .h(px(TRANSCRIPT_COMPOSER_CLEARANCE))
                                            .into_any_element()
                                    } else {
                                        timeline_entry(
                                            index,
                                            &blocks.borrow(),
                                            (
                                                palette,
                                                locale,
                                                reduced_motion,
                                                column_gutter,
                                                live_elapsed_ms,
                                            ),
                                            &agents,
                                            process_expansion.clone(),
                                            owner.clone(),
                                            Some(&reply_actions),
                                        )
                                    }
                                })
                                .relative()
                                .left(px(column_animation_offset))
                                .flex_1(),
                            ),
                    )
                    .child(
                        div()
                            .w_full()
                            .pl(px(column_gutter))
                            .pr(px(right_panel_width + column_gutter))
                            .pt(px(8.))
                            .pb(px(14.))
                            .flex()
                            .justify_center()
                            .child(
                                div()
                                    .w_full()
                                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                                    .relative()
                                    .left(px(column_animation_offset))
                                    .flex()
                                    .flex_col()
                                    .when_some(queue, |stack, queue| stack.child(queue))
                                    .child(composer),
                            ),
                    )
                    .when_some(environment, |thread, environment| thread.child(environment))
                    .when_some(side_panel, |thread, side_panel| thread.child(side_panel))
                    .when_some(agent_detail, |thread, detail| thread.child(detail))
                    .into_any_element()
            }
            Surface::Search => div().into_any_element(),
            Surface::Projects => projects_surface(&self.state, palette, labels, cx),
            Surface::Files => workspace_files_surface(&self.state, palette, cx),
            Surface::Changes => workspace_changes_surface(&self.state, palette, cx),
            Surface::PullRequests => pull_requests_surface(&self.state, palette, cx),
            Surface::Security => security_surface(&self.state, &self.runtime, palette),
            Surface::Terminal => self.terminal_view(palette, cx),
        };
        let sidebar = sidebar(
            &self.state,
            palette,
            labels,
            &self.open_projects,
            self.show_all_sessions,
            self.sidebar_context_menu.as_ref().map(|menu| &menu.target),
            cx,
        );
        let settings_modal = if self.settings_open {
            Some(self.settings_modal_view(palette, cx))
        } else {
            None
        };
        let search_modal = if search_open {
            Some(search_surface(
                &self.state,
                self.search_input.clone(),
                palette,
                labels,
                cx,
            ))
        } else {
            None
        };
        let sidebar_menu = self
            .sidebar_context_menu
            .as_ref()
            .map(|_| sidebar_context_menu_view(self, palette, locale, cx));
        let rename_modal = self
            .renaming_session_id
            .as_ref()
            .map(|_| session_rename_modal(self, palette, locale, cx));
        let agent_titlebar_visible = surface == Surface::Thread
            && self.side_panel_agents_open
            && (self.side_panel_open || self.side_panel_closing);
        let agent_titlebar_width = if agent_titlebar_visible {
            self.side_panel_width
        } else {
            0.
        };
        div()
            .id("azem-root")
            .relative()
            .role(Role::Application)
            .aria_label(labels.application)
            .track_focus(&self.focus)
            .size_full()
            .on_action(cx.listener(Self::submit_message))
            .on_action(cx.listener(Self::toggle_search))
            .on_action(cx.listener(Self::toggle_settings))
            .on_action(cx.listener(Self::find_settings))
            .on_action(cx.listener(Self::toggle_terminal))
            .on_action(cx.listener(Self::close_overlay))
            .bg(palette.canvas)
            .text_color(palette.ink)
            .font_family(if preferences.font == "system" {
                ".SystemUIFont".into()
            } else {
                preferences.font
            })
            .text_size(px(preferences.ui_font_size))
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(37.))
                    .flex_shrink_0()
                    .border_b_1()
                    .border_color(palette.border)
                    .bg(palette.paper),
            )
            .child(
                div().flex_1().min_h_0().flex().child(sidebar).child(
                    div()
                        .id("workspace")
                        .role(Role::Main)
                        .aria_label(surface_title.clone())
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .bg(palette.paper)
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .relative()
                                .h(px(if surface == Surface::Thread { 46. } else { 0. }))
                                .pl(px(if surface == Surface::Thread { 22. } else { 0. }))
                                .pr(px(if surface == Surface::Thread {
                                    22. + agent_titlebar_width
                                } else {
                                    0.
                                }))
                                .when(surface == Surface::Thread && !empty_thread, |header| {
                                    header.border_b_1().border_color(palette.border)
                                })
                                .bg(palette.paper)
                                .overflow_hidden()
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .text_color(palette.ink)
                                        .flex()
                                        .items_center()
                                        .gap_2()
                                        .when(
                                            surface == Surface::Thread && !empty_thread,
                                            |title| {
                                                title
                                                    .child(
                                                        div()
                                                            .font_family("SF Mono")
                                                            .text_size(px(9.))
                                                            .text_color(palette.faint)
                                                            .child(
                                                                if surface_title.contains("UI") {
                                                                    locale.text(
                                                                        "conversation.designTask",
                                                                    )
                                                                } else {
                                                                    locale.text("conversation.task")
                                                                },
                                                            ),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(13.))
                                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                                            .child(surface_title.clone()),
                                                    )
                                            },
                                        )
                                        .when(surface != Surface::Thread, |title| {
                                            title.child(
                                                div()
                                                    .text_size(px(13.))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(surface_title),
                                            )
                                        }),
                                )
                                .child(div().flex_1())
                                .when(surface == Surface::Thread && !empty_thread, |header| {
                                    header
                                        .when(self.state.connection.connected, |header| {
                                            header.child(
                                                div()
                                                    .px_2()
                                                    .py(px(3.))
                                                    .rounded_full()
                                                    .bg(if thread_status_active {
                                                        rgba(0x1f7af014)
                                                    } else {
                                                        palette.paper_muted
                                                    })
                                                    .text_size(px(10.))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .text_color(if thread_status_active {
                                                        rgb(0x1f7af0)
                                                    } else {
                                                        palette.faint
                                                    })
                                                    .child(locale.text(thread_status_key)),
                                            )
                                        })
                                        .child(
                                            div()
                                                .id("environment-toggle")
                                                .role(Role::Button)
                                                .aria_label(locale.text("ui.toggleEnvironment"))
                                                .aria_selected(self.environment_open)
                                                .tab_stop(true)
                                                .h(px(29.))
                                                .px_2()
                                                .ml_2()
                                                .rounded(px(7.))
                                                .border_1()
                                                .border_color(palette.border)
                                                .bg(if self.environment_open {
                                                    palette.paper_muted
                                                } else {
                                                    palette.paper
                                                })
                                                .text_color(palette.muted)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_click(cx.listener(|this, _, _, cx| {
                                                    this.toggle_environment_panel(cx);
                                                }))
                                                .child(icon(
                                                    "sliders-horizontal",
                                                    15.,
                                                    palette.muted,
                                                )),
                                        )
                                        .child(
                                            div()
                                                .id("side-panel-toggle")
                                                .role(Role::Button)
                                                .aria_label(locale.text("ui.toggleSidePanel"))
                                                .aria_selected(self.side_panel_open)
                                                .tab_stop(true)
                                                .h(px(29.))
                                                .px_2()
                                                .ml_1()
                                                .rounded(px(7.))
                                                .border_1()
                                                .border_color(palette.border)
                                                .bg(if self.side_panel_open {
                                                    palette.paper_muted
                                                } else {
                                                    palette.paper
                                                })
                                                .text_color(palette.muted)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_click(cx.listener(|this, _, window, cx| {
                                                    this.toggle_side_panel(window, cx);
                                                }))
                                                .child(icon("panels", 15., palette.muted)),
                                        )
                                })
                                .when(!self.state.connection.connected, |header| {
                                    header.child(
                                        div()
                                            .id("connection-status")
                                            .role(Role::Status)
                                            .text_size(px(11.))
                                            .text_color(palette.warning)
                                            .child(self.state.connection.message.to_string()),
                                    )
                                })
                                .when(agent_titlebar_visible, |header| {
                                    header.child(agent_panel_tab(self, palette, locale, cx))
                                }),
                        )
                        .child(content)
                        .when(self.terminal_open, |workspace| {
                            workspace.child(
                                div()
                                    .id("terminal-dock")
                                    .h(px(260.))
                                    .min_h(px(140.))
                                    .flex_shrink_0()
                                    .border_t_1()
                                    .border_color(palette.border)
                                    .bg(palette.paper)
                                    .child(self.terminal_view(palette, cx)),
                            )
                        }),
                ),
            )
            .when_some(search_modal, |root, modal| root.child(modal))
            .when_some(settings_modal, |root, modal| root.child(modal))
            .when_some(sidebar_menu, |root, menu| root.child(menu))
            .when_some(rename_modal, |root, modal| root.child(modal))
    }
}
