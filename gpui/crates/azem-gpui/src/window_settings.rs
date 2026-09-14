use super::*;

impl AzemWindow {
    pub(super) fn load_catalog_provider_fields(
        &mut self,
        provider: &serde_json::Value,
        cx: &mut Context<Self>,
    ) {
        let id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        if id.is_empty() || self.native_settings.catalog_loaded_id.as_deref() == Some(id) {
            return;
        }
        self.native_settings.catalog_loaded_id = Some(id.to_string());
        self.native_settings.catalog_enabled = provider
            .get("enabled")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let locale = Locale::resolve(&self.state.settings.language);
        let default_url = provider
            .get("defaultBaseUrl")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .trim();
        let url = provider
            .get("baseUrl")
            .and_then(serde_json::Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .unwrap_or(default_url);
        let locked = !default_url.is_empty();
        self.native_settings
            .provider_base_url
            .update(cx, |input, cx| {
                if input.text() != url {
                    input.set_text(url, cx);
                }
                input.set_placeholder(
                    locale.text(if locked {
                        "ui.officialAPIAddressLocked"
                    } else {
                        "ui.customAPIBaseURL"
                    }),
                    cx,
                );
            });
        let configured = provider
            .get("credentialConfigured")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let env = provider
            .get("envKey")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let placeholder = if configured {
            locale.text("ui.keepCredential").to_string()
        } else if !env.is_empty() {
            env.to_string()
        } else {
            locale.text("ui.apiKey").to_string()
        };
        self.native_settings
            .provider_api_key
            .update(cx, |input, cx| {
                input.clear(cx);
                input.set_placeholder(placeholder, cx);
            });
    }

    fn sync_catalog_editor(&mut self, cx: &mut Context<Self>) {
        if !self.settings_open || self.settings_section != "catalog" {
            return;
        }
        let selected = self.settings_provider.clone().or_else(|| {
            self.state.catalogs.providers.iter().find_map(|provider| {
                provider
                    .get("id")
                    .and_then(serde_json::Value::as_str)
                    .map(str::to_string)
            })
        });
        let Some(id) = selected else {
            return;
        };
        if let Some(provider) = self
            .state
            .catalogs
            .providers
            .iter()
            .find(|provider| {
                provider.get("id").and_then(serde_json::Value::as_str) == Some(id.as_str())
            })
            .cloned()
        {
            self.load_catalog_provider_fields(&provider, cx);
        }
    }

    pub(super) fn execute_extension_action(
        &mut self,
        mut payload: serde_json::Value,
        cx: &mut Context<Self>,
    ) {
        if self.extension_settings.busy || !self.state.connection.connected {
            return;
        }
        payload["sessionId"] = json!(self.state.navigation.current_session_id.as_ref());
        let source_text = (payload["kind"] == "marketplace_add")
            .then(|| self.extension_settings.source.read(cx).text().to_owned());
        self.state.settings.error = "".into();
        self.extension_settings.busy = true;
        let id = self.runtime.request(Method::Execute, payload);
        self.pending_requests
            .insert(id, PendingRequest::ExtensionAction { source_text });
        cx.notify();
    }

    pub(super) fn confirm_extension_action(
        &mut self,
        payload: serde_json::Value,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let locale = Locale::resolve(&self.state.settings.language);
        if let Some(detail) = extension_confirmation(&payload, locale) {
            let answer = window.prompt(
                PromptLevel::Warning,
                locale.text("ui.confirmExtensionAction"),
                Some(&detail),
                &[
                    PromptButton::cancel(locale.text("ui.cancel")),
                    PromptButton::new(locale.text("ui.confirm")),
                ],
                cx,
            );
            cx.spawn(async move |this, cx| {
                if answer.await == Ok(1) {
                    let _ = this.update(cx, |this, cx| this.execute_extension_action(payload, cx));
                }
            })
            .detach();
        } else {
            self.execute_extension_action(payload, cx);
        }
    }

    pub(super) fn settings_modal_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        self.sync_catalog_editor(cx);
        let locale = Locale::resolve(&self.state.settings.language);
        let close_label = locale.text("ui.closeSettings");
        let picker = (self.popup_motion("model").visible()
            && self.model_picker_render_target.is_some())
        .then(|| self.model_picker_view(palette, cx));
        let content = settings_surface(
            &self.state,
            palette,
            self.settings_section.as_str(),
            self.settings_provider.as_deref().unwrap_or_default(),
            (
                self.settings_provider_search.clone(),
                self.settings_model_search.clone(),
            ),
            (
                self.settings_provider_scroll.clone(),
                self.settings_model_scroll.clone(),
            ),
            (self.model_picker_render_target.as_ref(), picker),
            self.subagent_setting_menu,
            (
                self.archive_days,
                self.archive_days_menu_open,
                &self.open_projects,
            ),
            self.usage_hover.as_ref(),
            &self.extension_settings,
            &self.native_settings,
            cx,
        );
        let dialog = self.settings_dialog(content, palette);
        let modal = div()
            .id("settings-modal-backdrop")
            .role(Role::Region)
            .aria_label(close_label)
            .absolute()
            .occlude()
            .size_full()
            .bg(palette.paper)
            .flex()
            .items_center()
            .justify_center()
            .child(dialog);
        popup_transition(modal, self.popup_motion("settings"))
    }

    pub(super) fn settings_dialog(
        &mut self,
        content: gpui::AnyElement,
        palette: ThemePalette,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        let title = locale.text("ui.settingsAndExtensions");
        div()
            .id("settings-modal")
            .role(Role::Region)
            .aria_label(title)
            .relative()
            .occlude()
            .w_full()
            .h_full()
            .bg(palette.paper)
            .overflow_hidden()
            .child(
                div()
                    .id("settings-modal-content")
                    .size_full()
                    .overflow_hidden()
                    .child(content),
            )
            .into_any_element()
    }

    pub(super) fn composer_context_control(
        &mut self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let context = context_composition(
            &self.state.runtime.context_profile,
            &self.state.runtime.context_usage,
            self.selected_model_context_window(),
            locale,
        );
        let fraction = if context.limit > 0 {
            context.used as f32 / context.limit as f32
        } else {
            0.
        };
        let label = locale.text("ui.contextComposition");
        div()
            .id("context-composition")
            .relative()
            .on_hover(cx.listener(|this, hovered: &bool, _, cx| {
                this.context_popover_open = *hovered;
                if *hovered {
                    this.model_picker_open = false;
                }
                cx.notify();
            }))
            .child(
                div()
                    .id("context-composition-toggle")
                    .role(Role::Button)
                    .aria_label(label)
                    .aria_expanded(self.context_popover_open)
                    .tab_stop(true)
                    .size(px(32.))
                    .rounded_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.paper_muted))
                    .on_click(cx.listener(Self::toggle_context_popover))
                    .child(context_ring_svg(fraction, palette)),
            )
            .into_any_element()
    }

    pub(super) fn context_popover_view(
        &self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let composition = context_composition(
            &self.state.runtime.context_profile,
            &self.state.runtime.context_usage,
            self.selected_model_context_window(),
            locale,
        );
        let used = format_context_tokens(composition.used);
        let total = format_context_tokens(composition.limit);
        let rail = composition.segments.iter().enumerate().fold(
            div()
                .w_full()
                .h(px(5.))
                .rounded_full()
                .overflow_hidden()
                .bg(palette.paper_muted)
                .flex()
                .gap(px(1.)),
            |rail, (index, segment)| {
                let width = if composition.limit > 0 {
                    segment.tokens as f32 / composition.limit as f32
                } else {
                    0.
                };
                rail.child(
                    div()
                        .h_full()
                        .w(relative(width.clamp(0., 1.)))
                        .bg(context_segment_color(index, &segment.category, palette)),
                )
            },
        );
        let rows = div().flex().flex_col().gap(px(8.)).children(
            composition
                .segments
                .iter()
                .enumerate()
                .map(|(index, segment)| {
                    let percentage = if composition.used > 0 {
                        (segment.tokens * 100 + composition.used / 2) / composition.used
                    } else {
                        0
                    };
                    div()
                        .h(px(19.))
                        .flex()
                        .items_center()
                        .gap(px(10.))
                        .text_size(px(13.))
                        .text_color(palette.muted)
                        .child(div().size(px(6.)).rounded_full().bg(context_segment_color(
                            index,
                            &segment.category,
                            palette,
                        )))
                        .child(div().flex_1().child(segment.label.clone()))
                        .child(
                            div()
                                .w(px(38.))
                                .flex_shrink_0()
                                .whitespace_nowrap()
                                .text_align(gpui::TextAlign::Right)
                                .text_color(palette.faint)
                                .child(format!("{percentage}%")),
                        )
                        .child(
                            div()
                                .min_w(px(40.))
                                .flex_shrink_0()
                                .whitespace_nowrap()
                                .text_align(gpui::TextAlign::Right)
                                .text_color(palette.muted)
                                .child(format_context_tokens(segment.tokens)),
                        )
                }),
        );
        let popover = div()
            .id("context-composition-popover")
            .on_mouse_down_out(cx.listener(Self::dismiss_picker))
            .role(Role::Region)
            .aria_label(locale.text("ui.contextComposition"))
            .absolute()
            .right(px(102.))
            .bottom(px(48.))
            .w(px(240.))
            .p(px(16.))
            .rounded(px(16.))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.12, 0.14))
                    .blur_radius(px(24.)),
            ])
            .flex()
            .flex_col()
            .gap(px(14.))
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .text_size(px(13.5))
                    .child(
                        div()
                            .font_weight(gpui::FontWeight::MEDIUM)
                            .text_color(palette.ink)
                            .child(locale.text("ui.contextComposition")),
                    )
                    .child(
                        div()
                            .whitespace_nowrap()
                            .text_color(palette.faint)
                            .child(used.clone()),
                    ),
            )
            .child(rail)
            .child(rows)
            .child(div().h(px(1.)).bg(palette.border))
            .when_some(composition.cache_hit_rate, |popover, cache_hit_rate| {
                popover.child(
                    div()
                        .h(px(19.))
                        .flex()
                        .items_center()
                        .justify_between()
                        .text_size(px(13.))
                        .text_color(palette.muted)
                        .child(locale.text("ui.cacheHitRate"))
                        .child(
                            div()
                                .whitespace_nowrap()
                                .text_color(palette.faint)
                                .child(format!("{cache_hit_rate}%")),
                        ),
                )
            })
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .text_size(px(13.))
                    .text_color(palette.muted)
                    .child(locale.text("ui.total"))
                    .child(
                        div()
                            .whitespace_nowrap()
                            .text_color(palette.faint)
                            .child(format!("{used} / {total}")),
                    ),
            );
        popup_transition(popover, self.popup_motion("context"))
    }
}
