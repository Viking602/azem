use super::*;

impl AzemWindow {
    pub(super) fn composer_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        expanded: bool,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        self.composer.update(cx, |input, cx| {
            input.show_placeholder(self.completion.selected_skills.is_empty(), cx)
        });
        let prompt = self.composer.read(cx).text();
        let prompt_empty = prompt.trim().is_empty() && self.completion.selected_skills.is_empty();
        let composer_rows = composer_text_rows(prompt);
        let attachment_count = self.state.transcript.attachments.len();
        let input_height = px(composer_input_height(
            composer_rows,
            expanded,
            attachment_count,
        ));
        let project_name = std::path::Path::new(self.state.workspace.root.as_ref())
            .file_name()
            .and_then(|name| name.to_str())
            .filter(|name| !name.is_empty())
            .unwrap_or("workspace")
            .to_string();
        let model = if self.state.settings.model.is_empty() {
            locale.text("ui.selectModel").to_string()
        } else {
            self.state
                .catalogs
                .providers
                .iter()
                .find(|provider| {
                    provider.get("id").and_then(serde_json::Value::as_str)
                        == Some(self.state.settings.provider.as_ref())
                })
                .and_then(|provider| {
                    model_choices(
                        provider,
                        self.state.settings.model.as_ref(),
                        self.state.settings.reasoning.as_ref(),
                    )
                    .into_iter()
                    .find(|choice| choice.selected)
                })
                .map(|choice| {
                    choice.family_name.unwrap_or_else(|| {
                        catalog_model_name(choice.model, self.state.settings.model.as_ref())
                    })
                })
                .unwrap_or_else(|| humanize_model_id(self.state.settings.model.as_ref()))
        };
        let current_provider = self.state.settings.provider.to_string();
        let current_logo = catalog_provider_logo_id(
            self.state.catalogs.providers.iter().find(|provider| {
                provider.get("id").and_then(serde_json::Value::as_str)
                    == Some(current_provider.as_str())
            }),
            &current_provider,
        );
        let modes = model_modes(
            &self.state.catalogs.providers,
            &current_provider,
            self.state.settings.model.as_ref(),
            self.state.settings.reasoning.as_ref(),
            self.state.settings.chatgpt_fast_mode,
        );
        let mut reasoning = reasoning_display_name(
            if modes.reasoning.is_empty() {
                self.state.settings.reasoning.as_ref()
            } else {
                &modes.reasoning
            },
            locale,
        );
        if modes.fast {
            reasoning.push_str(" · ");
            reasoning.push_str(locale.text("model.fast"));
        }
        let show_cancel =
            stoppable_run(&self.state).is_some() && prompt_empty && attachment_count == 0;
        let send_control = if show_cancel {
            div()
                .id("cancel-active")
                .role(Role::Button)
                .aria_label(labels.stop)
                .tab_stop(true)
                .size(px(32.))
                .rounded_full()
                .bg(palette.button)
                .text_color(rgb(0xffffff))
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .on_click(cx.listener(Self::cancel_active))
                .child(icon("square", 13., rgb(0xffffff)))
                .into_any_element()
        } else {
            let idle = prompt_empty && attachment_count == 0;
            div()
                .id("send-message")
                .role(Role::Button)
                .aria_label(if self.state.runtime.running {
                    labels.queue
                } else {
                    labels.send
                })
                .tab_stop(true)
                .size(px(32.))
                .rounded_full()
                .bg(if idle {
                    palette.paper_muted
                } else {
                    palette.button
                })
                .text_color(if idle {
                    palette.faint
                } else {
                    palette.button_text
                })
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .on_click(cx.listener(Self::send_message))
                .child(icon(
                    "arrow-up",
                    16.,
                    if idle {
                        palette.faint
                    } else {
                        palette.button_text
                    },
                ))
                .into_any_element()
        };
        let picker = (self.popup_motion("model").visible()
            && self.model_picker_render_target.is_none())
        .then(|| self.model_picker_view(palette, cx));
        let locale = Locale::resolve(&self.state.settings.language);
        let context_control = self.composer_context_control(palette, locale, cx);
        let attachment_previews = self.composer_attachments_view(palette, locale, cx);
        let completion_menu = self.completion_menu(palette, cx);
        let selected_skills = self.selected_skills_view(palette, cx);
        let plan = surfaces::composer_plan(self, palette, locale, cx);
        let queue = self.queued_prompts_view(palette, labels, plan.is_some(), cx);
        let composer_shell = div()
            .id("composer")
            .role(Role::Group)
            .aria_label(labels.composer)
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .rounded(px(16.))
            .shadow(vec![
                BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.12, 0.12, 0.05))
                    .blur_radius(px(10.)),
            ])
            .overflow_hidden()
            .flex()
            .flex_col()
            .can_drop(|value, _, _| {
                value.downcast_ref::<ExternalPaths>().is_some_and(|paths| {
                    paths
                        .paths()
                        .iter()
                        .any(|path| image_mime_from_extension(path).is_some())
                })
            })
            .on_drop(cx.listener(|this, paths: &ExternalPaths, _, cx| {
                this.attach_image_paths(paths.paths(), cx);
            }))
            .when(expanded, |composer| {
                composer.child(
                    div()
                        .h(px(40.))
                        .px_3()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .h(px(29.))
                                .px_2()
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .text_color(palette.muted)
                                .text_xs()
                                .flex()
                                .items_center()
                                .gap_1()
                                .child(icon("box", 13., palette.muted))
                                .child(project_name),
                        )
                        .child(branch_picker_control(self, false, palette, locale, cx)),
                )
            })
            .when_some(attachment_previews, |composer, previews| {
                composer.child(previews)
            })
            .child(
                div()
                    .h(input_height)
                    .capture_key_down(cx.listener(Self::completion_key))
                    .capture_action(cx.listener(Self::submit_completion))
                    .capture_action(cx.listener(Self::backspace_completion))
                    .capture_action(cx.listener(Self::paste_composer_images))
                    .px_1()
                    .overflow_hidden()
                    .flex()
                    .items_start()
                    .when_some(selected_skills, |row, skills| row.child(skills))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .child(self.composer.clone()),
                    ),
            )
            .when(!self.completion.submission_error.is_empty(), |composer| {
                composer.child(
                    div()
                        .px_3()
                        .pb_2()
                        .text_size(px(11.))
                        .text_color(palette.danger)
                        .child(self.completion.submission_error.clone()),
                )
            })
            .child(
                div()
                    .h(px(46.))
                    .px_2()
                    .pb_2()
                    .flex()
                    .items_center()
                    .gap_1()
                    .child(
                        div()
                            .id("attach-file")
                            .role(Role::Button)
                            .aria_label(labels.attach)
                            .tab_stop(true)
                            .size(px(32.))
                            .rounded_full()
                            .text_color(palette.ink_soft)
                            .text_lg()
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::attach_file))
                            .child("+"),
                    )
                    .child(approval_picker_control(self, labels, palette, locale, cx))
                    .child(
                        div()
                            .id("plan-mode")
                            .role(Role::Button)
                            .aria_label(labels.plan)
                            .aria_selected(self.state.runtime.plan_mode)
                            .tab_stop(!self.state.runtime.running)
                            .h(px(32.))
                            .px_2()
                            .rounded_full()
                            .bg(if self.state.runtime.plan_mode {
                                palette.accent_soft
                            } else {
                                palette.paper
                            })
                            .text_color(if self.state.runtime.plan_mode {
                                palette.accent
                            } else {
                                palette.ink_soft
                            })
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::toggle_plan))
                            .child(icon(
                                "lightbulb",
                                15.,
                                if self.state.runtime.plan_mode {
                                    palette.accent
                                } else {
                                    palette.ink_soft
                                },
                            ))
                            .child(labels.plan),
                    )
                    .child(div().flex_1())
                    .child(context_control)
                    .child(
                        div()
                            .id("model-picker-toggle")
                            .role(Role::Button)
                            .aria_label(model.clone())
                            .aria_expanded(self.model_picker_open)
                            .tab_stop(true)
                            .h(px(32.))
                            .max_w(px(230.))
                            .px_2()
                            .rounded_full()
                            .bg(if self.model_picker_open {
                                palette.paper_muted
                            } else {
                                palette.paper
                            })
                            .text_color(palette.ink)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::toggle_model_picker))
                            .child(provider_logo(&current_logo, 15., palette.ink))
                            .child(div().min_w_0().truncate().child(model))
                            .child(
                                div()
                                    .text_color(palette.faint)
                                    .text_size(px(10.))
                                    .child(reasoning),
                            )
                            .child(icon("chevron-down", 11., palette.faint)),
                    )
                    .child(send_control),
            );
        let approval = self.pending_approval_view(palette, locale, cx);
        div()
            .id("composer-shell")
            .relative()
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .when_some(plan, |shell, plan| shell.child(plan))
            .when_some(queue, |shell, queue| shell.child(queue))
            .when_some(approval, |shell, panel| shell.child(panel))
            .child(composer_shell)
            .when_some(completion_menu, |shell, menu| shell.child(menu))
            .when(self.popup_motion("context").visible(), |shell| {
                shell.child(self.context_popover_view(palette, locale, cx))
            })
            .when_some(picker, |shell, picker| shell.child(picker))
            .into_any_element()
    }

    pub(super) fn composer_attachments_view(
        &mut self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        let attachments = self.state.transcript.attachments.clone();
        if attachments.is_empty() {
            return None;
        }
        let previews = attachments
            .into_iter()
            .enumerate()
            .map(|(index, attachment)| {
                let name = attachment
                    .get("name")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or(locale.text("ui.image"))
                    .to_string();
                let image =
                    attachment_image_preview(attachment_preview_path(&attachment), palette, 20.);
                div()
                    .id(("attachment-preview", index))
                    .role(Role::Group)
                    .aria_label(name.clone())
                    .relative()
                    .w(px(92.))
                    .h(px(68.))
                    .flex_none()
                    .rounded(px(10.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper_muted)
                    .overflow_hidden()
                    .child(image)
                    .child(
                        div()
                            .id(("remove-attachment", index))
                            .role(Role::Button)
                            .aria_label(
                                locale.format("ui.removeName", &[("name", (name).to_string())]),
                            )
                            .tab_stop(true)
                            .absolute()
                            .top(px(5.))
                            .right(px(5.))
                            .size(px(22.))
                            .rounded_full()
                            .bg(rgba(0x161616c4))
                            .text_color(rgb(0xffffff))
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(|style| style.bg(rgba(0x161616e8)))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.remove_attachment(index, cx);
                            }))
                            .child("×"),
                    )
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        Some(
            div()
                .id("composer-attachments")
                .role(Role::List)
                .aria_label(locale.text("ui.imageAttachments"))
                .h(px(82.))
                .px_3()
                .pt_2()
                .pb_1()
                .flex()
                .items_start()
                .gap_2()
                .overflow_x_scroll()
                .children(previews)
                .into_any_element(),
        )
    }

    pub(super) fn queued_prompts_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        has_plan: bool,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        let session_id = self.state.navigation.current_session_id.as_ref();
        let items = self
            .queued_prompts
            .iter()
            .filter(|item| item.session_id == session_id)
            .cloned()
            .collect::<Vec<_>>();
        if items.is_empty() {
            return None;
        }
        let locale = Locale::resolve(&self.state.settings.language);
        let paused = self
            .state
            .runtime
            .prompt_queues
            .get(session_id)
            .is_some_and(|q| q["state"] == "paused");
        let rows = items
            .into_iter()
            .enumerate()
            .map(|(index, item)| self.queued_prompt_row(index, item, palette, labels, locale, cx))
            .collect::<Vec<_>>();
        Some(
            div()
                .id("queued-prompts")
                .role(Role::List)
                .aria_label(locale.text("ui.queuedMessages"))
                .w(relative(11. / 12.))
                .mx_auto()
                .border_1()
                .border_color(palette.border)
                .border_b_0()
                .when(!has_plan, |queue| queue.rounded_t(px(12.)))
                .bg(palette.paper)
                .p_2()
                .mb(px(-1.))
                .max_h(px(144.))
                .flex_shrink_0()
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .px_1()
                        .pb_1()
                        .text_size(px(12.))
                        .text_color(palette.muted)
                        .child(locale.text("queue.title")),
                )
                .when(paused, |list| {
                    list.child(
                        div()
                            .id("resume-queue")
                            .role(Role::Button)
                            .aria_label(locale.text("ui.resumeQueue"))
                            .tab_stop(true)
                            .p_2()
                            .cursor_pointer()
                            .child(locale.text("ui.resumeQueue"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.mutate_prompt_queue(json!({"operation":"resume"}), None, cx)
                            })),
                    )
                })
                .children(rows)
                .into_any_element(),
        )
    }

    pub(super) fn queued_prompt_row(
        &mut self,
        index: usize,
        item: QueuedPrompt,
        palette: ThemePalette,
        labels: Labels,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let guide_id = item.id.clone();
        let can_guide = !item.pending
            && !item.dispatching
            && !self.queue_request_pending()
            && self.state.runtime.guidance_open
            && self.state.runtime.running
            && !self.state.runtime.run_id.is_empty()
            && item.selected_skills.is_empty()
            && self.can_guide_prompt(&item.prompt);
        let edit_id = item.id.clone();
        let delete_id = item.id.clone();
        let target_id = item.id.clone();
        let target_session = item.session_id.clone();
        let fallback = item
            .attachments
            .first()
            .and_then(|attachment| attachment.get("name"))
            .and_then(serde_json::Value::as_str)
            .unwrap_or(locale.text("ui.imageAttachment"));
        let text = if !item.selected_skills.is_empty() {
            format!(
                "{} {}",
                item.selected_skills
                    .iter()
                    .map(|name| composer_completion::skill_title(name))
                    .collect::<Vec<_>>()
                    .join(", "),
                item.prompt
            )
            .trim()
            .to_owned()
        } else if item.prompt.is_empty() {
            fallback.to_string()
        } else {
            item.prompt
        };
        let drag = QueuedPromptDrag {
            id: item.id.clone(),
            session_id: item.session_id,
            label: text.clone(),
        };
        let row_id = format!("queued-prompt-{}", item.id);
        div()
            .id(row_id)
            .role(Role::ListItem)
            .aria_label(text.clone())
            .min_h(px(32.))
            .flex_shrink_0()
            .px_2()
            .when(index > 0, |row| {
                row.border_t_1().border_color(palette.border)
            })
            .flex()
            .items_center()
            .gap_1()
            .text_size(px(12.))
            .text_color(if item.failed {
                palette.danger
            } else {
                palette.ink
            })
            .drag_over::<QueuedPromptDrag>(move |style, dragged, _, _| {
                if dragged.session_id == target_session && dragged.id != target_id {
                    style.bg(palette.hover)
                } else {
                    style
                }
            })
            .on_drop(cx.listener({
                let target_id = item.id.clone();
                move |this, dragged: &QueuedPromptDrag, _, cx| {
                    this.reorder_queued(dragged, &target_id, cx);
                }
            }))
            .child(
                div()
                    .id(format!("drag-queued-{}", item.id))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.reorderQueuedMessage"))
                    .tab_stop(true)
                    .w(px(20.))
                    .flex_none()
                    .text_color(palette.faint)
                    .cursor_move()
                    .on_drag(drag, |dragged: &QueuedPromptDrag, _, _, cx| {
                        cx.new(|_| dragged.clone())
                    })
                    .child(if item.attachments.is_empty() {
                        "⋮⋮"
                    } else {
                        "▧⋮"
                    }),
            )
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .overflow_hidden()
                    .whitespace_nowrap()
                    .text_ellipsis()
                    .child(text),
            )
            .when(item.pending, |row| {
                row.child(
                    div()
                        .id(("queue-pending", index))
                        .role(Role::Status)
                        .aria_label(locale.text("ui.sending"))
                        .text_color(palette.muted)
                        .child(locale.text("ui.sending")),
                )
            })
            .when(can_guide, |row| {
                row.child(
                    div()
                        .id(("guide-queued", index))
                        .role(Role::Button)
                        .aria_label(labels.guide)
                        .tab_stop(true)
                        .flex_shrink_0()
                        .whitespace_nowrap()
                        .rounded(px(6.))
                        .hover(move |style| style.bg(palette.hover))
                        .px_2()
                        .cursor_pointer()
                        .text_color(palette.accent)
                        .child(labels.guide)
                        .on_click(
                            cx.listener(move |this, _, _, cx| this.guide_queued(&guide_id, cx)),
                        ),
                )
            })
            .when(item.failed, |row| {
                let retry_id = item.id.clone();
                row.child(
                    div()
                        .id(("retry-queued", index))
                        .role(Role::Button)
                        .aria_label(locale.text("ui.retryQueuedMessage"))
                        .tab_stop(true)
                        .px_2()
                        .cursor_pointer()
                        .child(locale.text("ui.retryQueuedMessage"))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.mutate_prompt_queue(
                                json!({"operation":"retry","itemId":retry_id}),
                                None,
                                cx,
                            );
                        })),
                )
            })
            .child(
                div()
                    .id(("edit-queued", index))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.editQueuedMessage"))
                    .tab_stop(true)
                    .size(px(28.))
                    .rounded(px(8.))
                    .text_color(palette.faint)
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.edit_queued(&edit_id, window, cx);
                    }))
                    .child(icon("notebook-pen", 14., palette.faint)),
            )
            .child(
                div()
                    .id(("delete-queued", index))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.deleteQueuedMessage"))
                    .tab_stop(true)
                    .size(px(28.))
                    .rounded(px(8.))
                    .text_color(palette.faint)
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.delete_queued(&delete_id, cx);
                    }))
                    .child("×"),
            )
            .into_any_element()
    }
}
