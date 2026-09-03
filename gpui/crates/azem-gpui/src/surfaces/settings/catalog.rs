use super::*;
pub(super) fn settings_catalog_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    selected_provider: &str,
    searches: (Entity<TextInput>, Entity<TextInput>),
    scrolls: (UniformListScrollHandle, UniformListScrollHandle),
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (provider_search, model_search) = searches;
    let (provider_scroll, model_scroll) = scrolls;
    let provider_query = provider_search.read(cx).text().trim().to_ascii_lowercase();
    let model_query = model_search.read(cx).text().trim().to_ascii_lowercase();
    let mut provider_inventory = state.catalogs.providers.clone();
    provider_inventory.sort_by_key(|provider| {
        let id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let name = provider_display_name(provider, id);
        (
            provider_preference_rank(id, &name),
            name.to_ascii_lowercase(),
        )
    });
    let selected = state
        .catalogs
        .providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(selected_provider)
        })
        .or_else(|| {
            state.catalogs.providers.iter().find(|provider| {
                provider
                    .get("enabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
                    || provider
                        .get("models")
                        .and_then(serde_json::Value::as_array)
                        .is_some_and(|models| !models.is_empty())
            })
        })
        .or_else(|| state.catalogs.providers.first())
        .cloned();
    let selected_provider_id = selected
        .as_ref()
        .and_then(|provider| provider.get("id"))
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let provider_rows = provider_inventory
        .into_iter()
        .enumerate()
        .filter(|(_, provider)| provider_matches_query(provider, &provider_query))
        .collect::<Vec<_>>();
    let detail = if let Some(provider) = selected {
        let provider_id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let detail_name = provider_detail_name(&provider, &provider_id);
        let logo_id = provider_logo_id(&provider, &provider_id);
        let account = provider
            .get("accountLabel")
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .or_else(|| {
                provider
                    .get("credentialSource")
                    .and_then(serde_json::Value::as_str)
            })
            .unwrap_or_default()
            .to_string();
        let plan = provider
            .get("accountPlan")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let enabled = provider
            .get("enabled")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let subscription = provider
            .get("subscription")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_available = provider
            .get("quotaAvailable")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_remaining = provider_quota_remaining(&provider).unwrap_or(0.);
        let quota_period = provider
            .get("quotaPeriod")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let quota_balance = provider
            .get("quotaBalance")
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .unwrap_or("0.00")
            .to_string();
        let models = provider
            .get("models")
            .and_then(serde_json::Value::as_array)
            .cloned()
            .unwrap_or_default();
        let enabled_count = models
            .iter()
            .filter(|model| {
                !model
                    .get("disabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
            })
            .count();
        let session_id = state.navigation.current_session_id.to_string();
        let provider_action = model_provider_action(&provider, &session_id);
        let model_rows = models
            .iter()
            .enumerate()
            .filter(|(_, model)| model_matches_query(model, &model_query))
            .map(|(index, model)| (index, model.clone()))
            .collect::<Vec<_>>();
        let model_list = if model_rows.is_empty() {
            div()
                .h(px(96.))
                .w_full()
                .text_color(palette.faint)
                .text_sm()
                .flex()
                .items_center()
                .justify_center()
                .child(if model_query.is_empty() {
                    locale.text("ui.thisProviderHasNoModels")
                } else {
                    locale.text("ui.noMatchingModels")
                })
                .into_any_element()
        } else {
            let model_row_count = model_rows.len().div_ceil(2);
            let model_provider_id = provider_id.clone();
            let model_logo_id = logo_id.clone();
            let model_session_id = session_id.clone();
            uniform_list(
                "provider-model-grid",
                model_row_count,
                cx.processor(move |_this, range: std::ops::Range<usize>, _window, cx| {
                    range
                        .map(|row_index| {
                            let mut row = div().w_full().h(px(144.)).flex().gap(px(12.));
                            for column in 0..2 {
                                let Some((index, model)) =
                                    model_rows.get(row_index * 2 + column)
                                else {
                                    row = row.child(div().min_w_0().flex_1());
                                    continue;
                                };
                                let model_id = model
                                    .get("id")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let model_name = catalog_model_name(model, &model_id);
                                let disabled = model
                                    .get("disabled")
                                    .and_then(serde_json::Value::as_bool)
                                    .unwrap_or(false);
                                let capabilities = model_capability_keys(model, subscription);
                                let provider_target = model_provider_id.clone();
                                let model_target = model_id.clone();
                                let session_target = model_session_id.clone();
                                let card = div()
                                    .id(("provider-model-card", *index))
                                    .w_full()
                                    .h(px(132.))
                                    .p(px(15.))
                                    .rounded(px(12.))
                                    .border_1()
                                    .border_color(palette.border)
                                    .bg(palette.paper)
                                    .flex()
                                    .flex_col()
                                    .gap_2()
                                    .child(
                                        div()
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .child(provider_logo(
                                                &model_logo_id,
                                                14.,
                                                palette.muted,
                                            ))
                                            .child(
                                                div()
                                                    .min_w_0()
                                                    .flex_1()
                                                    .truncate()
                                                    .text_color(if disabled {
                                                        palette.muted
                                                    } else {
                                                        palette.ink
                                                    })
                                                    .text_sm()
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(model_name),
                                            )
                                            .child(
                                                div()
                                                    .flex()
                                                    .items_center()
                                                    .gap_2()
                                                    .child(
                                                        div()
                                                            .text_color(palette.muted)
                                                            .text_size(px(10.))
                                                            .child(if disabled {
                                                                locale.text("ui.disabled3")
                                                            } else {
                                                                locale.text("ui.enabled")
                                                            }),
                                                    )
                                                    .child(
                                                        div()
                                                            .id(("model-enabled", *index))
                                                            .role(Role::Button)
                                                            .aria_label(if disabled {
                                                                locale.text("ui.enableModel")
                                                            } else {
                                                                locale.text("ui.disableModel")
                                                            })
                                                            .aria_selected(!disabled)
                                                            .tab_stop(true)
                                                            .w(px(38.))
                                                            .h(px(22.))
                                                            .rounded_full()
                                                            .bg(if disabled {
                                                                palette.border_strong
                                                            } else {
                                                                palette.positive
                                                            })
                                                            .p(px(2.))
                                                            .flex()
                                                            .justify_end()
                                                            .when(disabled, |toggle| {
                                                                toggle.justify_start()
                                                            })
                                                            .cursor_pointer()
                                                            .on_click(cx.listener(move |
                                                                this, _, _, cx,
                                                            | {
                                                                this.runtime.request(
                                                                    Method::Execute,
                                                                    json!({
                                                                        "kind": "set_model_enabled",
                                                                        "sessionId": session_target,
                                                                        "target": provider_target,
                                                                        "name": model_target,
                                                                        "decision": disabled.to_string(),
                                                                    }),
                                                                );
                                                                cx.notify();
                                                            }))
                                                            .child(
                                                                div()
                                                                    .size(px(18.))
                                                                    .rounded_full()
                                                                    .bg(palette.paper),
                                                            ),
                                                    ),
                                            ),
                                    )
                                    .child(div().flex_1())
                                    .when(!capabilities.is_empty(), |card| {
                                        card.child(settings_model_capabilities(
                                            &capabilities,
                                            *index,
                                            palette,
                                            locale,
                                        ))
                                    });
                                row = row.child(div().min_w_0().flex_1().child(card));
                            }
                            row
                        })
                        .collect::<Vec<_>>()
                }),
            )
            .track_scroll(&model_scroll)
            .h_full()
            .into_any_element()
        };
        let refresh_action = model_discovery_request(&provider, &session_id);
        div()
            .min_w_0()
            .flex_1()
            .h_full()
            .min_h_0()
            .flex()
            .flex_col()
            .gap(px(14.))
            .child(
                div()
                    .min_h(px(74.))
                    .px(px(18.))
                    .rounded(px(12.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .size(px(32.))
                            .rounded(px(8.))
                            .bg(palette.paper_muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(provider_logo(&logo_id, 19., palette.ink)),
                    )
                    .child(
                        div()
                            .min_w_0()
                            .flex_1()
                            .flex()
                            .flex_col()
                            .gap(px(2.))
                            .child(
                                div()
                                    .text_color(palette.ink)
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(detail_name),
                            )
                            .when(!account.is_empty(), |summary| {
                                summary.child(
                                    div()
                                        .truncate()
                                        .text_color(palette.faint)
                                        .text_xs()
                                        .child(account),
                                )
                            }),
                    )
                    .when(!plan.is_empty(), |summary| {
                        summary.child(
                            div()
                                .px_2()
                                .py_1()
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .text_color(palette.muted)
                                .text_size(px(10.))
                                .child(plan),
                        )
                    })
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(
                                div()
                                    .text_color(if enabled {
                                        palette.positive
                                    } else {
                                        palette.faint
                                    })
                                    .text_xs()
                                    .child(if enabled {
                                        locale.text("ui.available2")
                                    } else {
                                        locale.text("ui.disabled3")
                                    }),
                            )
                            .when(subscription, |actions| {
                                let action = provider_action.clone();
                                actions.child(
                                    div()
                                        .id(if enabled {
                                            "provider-logout"
                                        } else {
                                            "provider-login"
                                        })
                                        .role(Role::Button)
                                        .aria_label(locale.text(if enabled {
                                            "ui.signOut"
                                        } else {
                                            "ui.signIn"
                                        }))
                                        .tab_stop(true)
                                        .h(px(30.))
                                        .px_2()
                                        .rounded(px(8.))
                                        .border_1()
                                        .border_color(palette.border)
                                        .text_color(if enabled {
                                            palette.danger
                                        } else {
                                            palette.accent
                                        })
                                        .text_xs()
                                        .flex()
                                        .items_center()
                                        .cursor_pointer()
                                        .hover(move |style| style.bg(palette.hover))
                                        .active(|style| style.opacity(0.72))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.runtime.request(Method::Execute, action.clone());
                                            cx.notify();
                                        }))
                                        .child(locale.text(if enabled {
                                            "ui.signOut"
                                        } else {
                                            "ui.signIn"
                                        })),
                                )
                            })
                            .when(!subscription, |actions| {
                                let action = provider_action.clone();
                                actions.child(
                                    div()
                                        .id("provider-enabled")
                                        .role(Role::Button)
                                        .aria_label(if enabled {
                                            locale.text("ui.disableProvider")
                                        } else {
                                            locale.text("ui.enableProvider")
                                        })
                                        .aria_selected(enabled)
                                        .tab_stop(true)
                                        .w(px(38.))
                                        .h(px(22.))
                                        .rounded_full()
                                        .bg(if enabled {
                                            palette.positive
                                        } else {
                                            palette.border_strong
                                        })
                                        .p(px(2.))
                                        .flex()
                                        .justify_end()
                                        .when(!enabled, |toggle| toggle.justify_start())
                                        .cursor_pointer()
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.runtime.request(Method::Execute, action.clone());
                                            cx.notify();
                                        }))
                                        .child(
                                            div().size(px(18.)).rounded_full().bg(palette.paper),
                                        ),
                                )
                            }),
                    ),
            )
            .when(quota_available, |detail| {
                detail.child(
                    div()
                        .rounded(px(12.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .pt(px(17.))
                        .px(px(22.))
                        .pb(px(20.))
                        .flex()
                        .flex_col()
                        .gap_3()
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .flex_1()
                                        .text_color(palette.ink)
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(locale.format(
                                            "ui.weeklyAllowanceArg0Remaining",
                                            &[("arg0", format!("{:.0}", quota_remaining))],
                                        )),
                                )
                                .when(!quota_period.is_empty(), |row| {
                                    row.child(
                                        div()
                                            .text_color(palette.faint)
                                            .text_xs()
                                            .child(quota_period),
                                    )
                                }),
                        )
                        .child(
                            div()
                                .w_full()
                                .h(px(6.))
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .overflow_hidden()
                                .child(
                                    div()
                                        .w(relative((quota_remaining / 100.) as f32))
                                        .h_full()
                                        .rounded_full()
                                        .bg(palette.positive),
                                ),
                        )
                        .child(div().h(px(1.)).bg(palette.border))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .flex_1()
                                        .text_color(palette.muted)
                                        .text_xs()
                                        .child(locale.text("ui.extraCredits")),
                                )
                                .child(
                                    div()
                                        .text_color(palette.positive)
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(format!("US${quota_balance}")),
                                ),
                        ),
                )
            })
            .child(
                div()
                    .rounded(px(12.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .flex_col()
                    .overflow_hidden()
                    .child(
                        div()
                            .h(px(74.))
                            .px(px(18.))
                            .flex()
                            .items_center()
                            .child(
                                div()
                                    .flex_1()
                                    .flex()
                                    .flex_col()
                                    .gap(px(2.))
                                    .child(
                                        div()
                                            .text_color(palette.ink)
                                            .text_sm()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(locale.text("ui.models3")),
                                    )
                                    .child(div().text_color(palette.faint).text_xs().child(
                                        locale.text(
                                            "ui.enabledModelsCanBeAssignedInRoutesAndTheComposer",
                                        ),
                                    )),
                            )
                            .child(
                                div()
                                    .px_2()
                                    .py_1()
                                    .rounded_full()
                                    .bg(palette.paper_muted)
                                    .text_color(palette.muted)
                                    .text_xs()
                                    .child(format!("{enabled_count} / {}", models.len())),
                            )
                            .child(
                                div()
                                    .id("refresh-provider-models")
                                    .role(Role::Button)
                                    .aria_label(locale.text("ui.fetchModels"))
                                    .tab_stop(true)
                                    .h(px(32.))
                                    .px_2()
                                    .rounded(px(8.))
                                    .border_1()
                                    .border_color(palette.border)
                                    .text_color(palette.muted)
                                    .text_xs()
                                    .flex()
                                    .items_center()
                                    .gap_1()
                                    .cursor_pointer()
                                    .hover(move |style| style.bg(palette.hover))
                                    .active(|style| style.opacity(0.72))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime
                                            .request(Method::Execute, refresh_action.clone());
                                        cx.notify();
                                    }))
                                    .child(icon("rotate-ccw", 13., palette.muted))
                                    .child(locale.text("ui.fetchModels")),
                            ),
                    )
                    .child(
                        div()
                            .h(px(44.))
                            .px(px(14.))
                            .border_t_1()
                            .border_b_1()
                            .border_color(palette.border)
                            .text_color(palette.faint)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(icon("search", 13., palette.faint))
                            .child(model_search),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_h_0()
                            .overflow_hidden()
                            .p(px(14.))
                            .child(model_list),
                    ),
            )
            .into_any_element()
    } else {
        settings_empty_card(locale.text("ui.noModelProvidersAreConfigured"), palette)
    };
    let provider_list = if provider_rows.is_empty() {
        div()
            .h(px(100.))
            .text_color(palette.faint)
            .text_sm()
            .flex()
            .items_center()
            .justify_center()
            .child(locale.text("ui.noProviders"))
            .into_any_element()
    } else {
        let provider_count = provider_rows.len();
        uniform_list(
            "provider-list-scroll",
            provider_count,
            cx.processor(move |_this, range: std::ops::Range<usize>, _window, cx| {
                range
                    .filter_map(|row_index| {
                        let (index, provider) = provider_rows.get(row_index)?;
                        let id = provider
                            .get("id")
                            .and_then(serde_json::Value::as_str)?
                            .to_string();
                        let display_name = provider_display_name(provider, &id);
                        let logo_id = provider_logo_id(provider, &id);
                        let model_count = provider
                            .get("models")
                            .and_then(serde_json::Value::as_array)
                            .map(Vec::len)
                            .unwrap_or_default();
                        let backend = provider
                            .get("backend")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default();
                        let enabled = provider
                            .get("enabled")
                            .and_then(serde_json::Value::as_bool)
                            .unwrap_or(false);
                        let subtitle =
                            provider_list_subtitle(provider, &id, backend, model_count, locale);
                        let quota = provider_quota_remaining(provider)
                            .map(format_percentage)
                            .unwrap_or_default();
                        let active = selected_provider_id == id;
                        let selected_id = id.clone();
                        Some(
                            div().h(px(66.)).child(
                                div()
                                    .id(("settings-provider", *index))
                                    .role(Role::Button)
                                    .aria_label(display_name.clone())
                                    .aria_selected(active)
                                    .tab_stop(true)
                                    .h(px(62.))
                                    .px_3()
                                    .rounded(px(9.))
                                    .border_l_2()
                                    .border_color(if active {
                                        palette.accent
                                    } else {
                                        rgba(0x00000000)
                                    })
                                    .bg(if active {
                                        palette.accent_soft
                                    } else {
                                        palette.paper
                                    })
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .cursor_pointer()
                                    .hover(move |style| style.bg(palette.hover))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.settings_provider = Some(selected_id.clone());
                                        this.settings_model_search
                                            .update(cx, |search, cx| search.clear(cx));
                                        this.settings_model_scroll
                                            .scroll_to_item_strict(0, ScrollStrategy::Top);
                                        cx.notify();
                                    }))
                                    .child(
                                        div()
                                            .size(px(27.))
                                            .rounded(px(7.))
                                            .bg(palette.paper_muted)
                                            .flex()
                                            .items_center()
                                            .justify_center()
                                            .child(provider_logo(&logo_id, 16., palette.ink)),
                                    )
                                    .child(
                                        div()
                                            .min_w_0()
                                            .flex_1()
                                            .flex()
                                            .flex_col()
                                            .gap(px(2.))
                                            .child(
                                                div()
                                                    .truncate()
                                                    .text_color(palette.ink)
                                                    .text_sm()
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(display_name),
                                            )
                                            .child(
                                                div()
                                                    .truncate()
                                                    .text_color(palette.faint)
                                                    .text_size(px(10.))
                                                    .child(subtitle),
                                            ),
                                    )
                                    .child(
                                        div()
                                            .text_color(if enabled {
                                                palette.positive
                                            } else {
                                                palette.faint
                                            })
                                            .text_size(px(10.))
                                            .child(if quota.is_empty() {
                                                if enabled {
                                                    "•".to_string()
                                                } else {
                                                    locale.text("ui.off2").to_string()
                                                }
                                            } else {
                                                quota
                                            }),
                                    ),
                            ),
                        )
                    })
                    .collect::<Vec<_>>()
            }),
        )
        .track_scroll(&provider_scroll)
        .h_full()
        .into_any_element()
    };
    let remaining_provider_count = if provider_query.is_empty() {
        state.catalogs.providers.len().saturating_sub(31)
    } else {
        0
    };
    div()
        .w_full()
        .min_h_0()
        .flex_1()
        .flex()
        .items_start()
        .gap(px(20.))
        .child(
            div()
                .id("provider-list")
                .h_full()
                .w(px(268.))
                .min_h_0()
                .overflow_hidden()
                .flex_shrink_0()
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .p_2()
                .flex()
                .flex_col()
                .child(
                    div()
                        .h(px(42.))
                        .px_2()
                        .mb_1()
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper_muted)
                        .text_color(palette.faint)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 13., palette.faint))
                        .child(provider_search),
                )
                .child(
                    div()
                        .flex_1()
                        .min_h_0()
                        .overflow_hidden()
                        .child(provider_list),
                )
                .when(remaining_provider_count > 0, |list| {
                    list.child(
                        div()
                            .h(px(28.))
                            .border_t_1()
                            .border_color(palette.border)
                            .text_color(palette.faint)
                            .text_size(px(10.))
                            .flex()
                            .items_center()
                            .child(locale.format(
                                "ui.scrollForRemainingProviderCountMoreProviders",
                                &[(
                                    "remaining_provider_count",
                                    (remaining_provider_count).to_string(),
                                )],
                            )),
                    )
                }),
        )
        .child(detail)
        .into_any_element()
}

pub(in crate::surfaces) fn model_matches_query(model: &serde_json::Value, query: &str) -> bool {
    query.is_empty()
        || ["id", "name"].into_iter().any(|key| {
            model
                .get(key)
                .and_then(serde_json::Value::as_str)
                .is_some_and(|value| value.to_ascii_lowercase().contains(query))
        })
        || model
            .get("aliases")
            .and_then(serde_json::Value::as_array)
            .is_some_and(|aliases| {
                aliases.iter().any(|alias| {
                    alias
                        .as_str()
                        .is_some_and(|value| value.to_ascii_lowercase().contains(query))
                })
            })
}

pub(in crate::surfaces) fn provider_matches_query(
    provider: &serde_json::Value,
    query: &str,
) -> bool {
    query.is_empty()
        || ["id", "displayName", "name", "backend"]
            .into_iter()
            .any(|key| {
                provider
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .is_some_and(|value| value.to_ascii_lowercase().contains(query))
            })
}

pub(in crate::surfaces) fn model_discovery_request(
    provider: &serde_json::Value,
    session_id: &str,
) -> serde_json::Value {
    if provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        json!({
            "kind": "discover_provider_models",
            "sessionId": session_id,
            "target": provider.get("id").and_then(serde_json::Value::as_str).unwrap_or_default(),
        })
    } else {
        json!({
            "kind": "discover_provider_models",
            "sessionId": session_id,
            "provider": provider,
        })
    }
}

pub(in crate::surfaces) fn model_provider_action(
    provider: &serde_json::Value,
    session_id: &str,
) -> serde_json::Value {
    if provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        let enabled = provider
            .get("enabled")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        return json!({
            "kind": if enabled { "logout" } else { "login" },
            "sessionId": session_id,
            "target": provider.get("id").and_then(serde_json::Value::as_str).unwrap_or_default(),
        });
    }
    let mut toggled = provider.clone();
    let enabled = provider
        .get("enabled")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    if let Some(fields) = toggled.as_object_mut() {
        fields.insert("enabled".to_string(), serde_json::Value::Bool(!enabled));
    }
    json!({"kind": "set_model_provider", "sessionId": session_id, "provider": toggled})
}

pub(super) fn provider_display_name(provider: &serde_json::Value, fallback: &str) -> String {
    ["displayName", "name"]
        .into_iter()
        .find_map(|key| {
            provider
                .get(key)
                .and_then(serde_json::Value::as_str)
                .filter(|value| !value.trim().is_empty())
        })
        .unwrap_or(fallback)
        .to_string()
}

pub(super) fn provider_logo_id(provider: &serde_json::Value, fallback: &str) -> String {
    provider
        .get("modelsDevId")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(fallback)
        .to_string()
}

fn provider_detail_name(provider: &serde_json::Value, provider_id: &str) -> String {
    match provider_id {
        "chatgpt" => "ChatGPT".to_string(),
        "grok" => "Grok".to_string(),
        "cursor" => "Cursor".to_string(),
        _ => provider_display_name(provider, provider_id),
    }
}

fn provider_preference_rank(provider_id: &str, display_name: &str) -> usize {
    let identity = format!(
        "{} {}",
        provider_id.to_ascii_lowercase(),
        display_name.to_ascii_lowercase()
    );
    [
        "chatgpt",
        "grok",
        "cursor",
        "deepseek",
        "kimi for coding",
        "opencode go",
        "openrouter",
        "abacus",
        "abliteration ai",
        "ai-router",
        "ai21 labs",
    ]
    .iter()
    .position(|preferred| identity.contains(preferred))
    .unwrap_or(usize::MAX)
}

fn provider_list_subtitle(
    provider: &serde_json::Value,
    provider_id: &str,
    backend: &str,
    model_count: usize,
    locale: Locale,
) -> String {
    let subscription = provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    if !subscription {
        return format!("{} · {} {}", backend, model_count, locale.text("ui.models"));
    }
    let plan = provider
        .get("accountPlan")
        .and_then(serde_json::Value::as_str)
        .map(|plan| format_subscription_plan(provider_id, plan))
        .filter(|plan| !plan.is_empty())
        .unwrap_or_else(|| {
            match provider_id {
                "chatgpt" => "Pro 20x",
                "grok" => "SuperGrokPro",
                "cursor" => "Cursor Ultra",
                _ => "",
            }
            .to_string()
        });
    let balance = provider
        .get("quotaBalance")
        .and_then(serde_json::Value::as_str)
        .map(format_credit_balance)
        .unwrap_or_default();
    if provider_id == "chatgpt" && !balance.is_empty() {
        return format!("{plan} · {} {balance}", locale.text("ui.extra"));
    }
    plan
}

fn format_subscription_plan(provider_id: &str, plan: &str) -> String {
    match (provider_id, plan.trim().to_ascii_lowercase().as_str()) {
        ("chatgpt", "pro") => "Pro 20x".to_string(),
        ("cursor", "ultra") => "Cursor Ultra".to_string(),
        (_, _) => plan.to_string(),
    }
}

fn format_percentage(value: f64) -> String {
    if (value - value.round()).abs() < 0.05 {
        format!("{value:.0}%")
    } else {
        format!("{value:.1}%")
    }
}

pub(in crate::surfaces) fn provider_quota_remaining(provider: &serde_json::Value) -> Option<f64> {
    if !provider
        .get("quotaAvailable")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        return None;
    }
    let used = provider
        .get("quotaUsedPercent")
        .and_then(serde_json::Value::as_f64)
        .unwrap_or(0.);
    Some((100. - used).clamp(0., 100.))
}

fn format_credit_balance(balance: &str) -> String {
    balance
        .parse::<f64>()
        .map(|value| {
            if (value - value.round()).abs() < 0.005 {
                format!("{value:.0}")
            } else {
                format!("{value:.2}")
            }
        })
        .unwrap_or_else(|_| balance.to_string())
}

pub(crate) struct ModelCapabilityTooltip {
    pub(crate) label: String,
    pub(crate) palette: ThemePalette,
}

impl Render for ModelCapabilityTooltip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div().pl_2().pt_2().child(
            div()
                .px_2()
                .py_1()
                .rounded(px(7.))
                .border_1()
                .border_color(self.palette.border)
                .bg(self.palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.14))
                        .blur_radius(px(14.)),
                ])
                .text_color(self.palette.ink_soft)
                .text_xs()
                .child(self.label.clone()),
        )
    }
}

fn settings_model_capabilities(
    capabilities: &[String],
    model_index: usize,
    palette: ThemePalette,
    locale: Locale,
) -> impl IntoElement {
    div()
        .id(format!("model-capabilities-{model_index}"))
        .role(Role::List)
        .aria_label(locale.text("ui.modelCapabilities"))
        .flex()
        .items_center()
        .gap_1()
        .children(capabilities.iter().enumerate().map(|(index, capability)| {
            let icon_name = match capability.as_str() {
                "tools" | "tool_call" | "tool-call" => "wrench",
                "parallel-tools" => "layers",
                "reasoning" => "brain",
                "structured_output" | "structured-output" => "braces",
                "in:image" | "out:image" | "in:video" | "out:video" => "image",
                "in:audio" | "out:audio" => "audio-lines",
                "in:text" => "type",
                "out:text" => "message-square-text",
                _ => "wrench",
            };
            let label = model_capability_label(capability, locale);
            let tooltip_label = label.clone();
            div()
                .id(format!("model-capability-{model_index}-{index}"))
                .role(Role::ListItem)
                .aria_label(label)
                .size(px(28.))
                .rounded(px(8.))
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .justify_center()
                .tooltip(move |_, cx| {
                    cx.new(|_| ModelCapabilityTooltip {
                        label: tooltip_label.clone(),
                        palette,
                    })
                    .into()
                })
                .child(icon(icon_name, 13., palette.faint))
        }))
}

pub(in crate::surfaces) fn model_capability_label(capability: &str, locale: Locale) -> String {
    match capability {
        "tools" | "tool_call" | "tool-call" => locale.text("ui.tools").to_string(),
        "parallel-tools" => locale.text("ui.parallelTools").to_string(),
        "reasoning" => locale.text("ui.reasoning2").to_string(),
        "structured_output" | "structured-output" => locale.text("ui.structuredOutput").to_string(),
        value if value.starts_with("in:") => format!(
            "{}: {}",
            locale.text("ui.input"),
            value.trim_start_matches("in:")
        ),
        value if value.starts_with("out:") => format!(
            "{}: {}",
            locale.text("ui.output"),
            value.trim_start_matches("out:")
        ),
        value => value.to_string(),
    }
}

fn model_capability_keys(model: &serde_json::Value, subscription: bool) -> Vec<String> {
    let mut keys = Vec::new();
    if subscription {
        for key in [
            "tools",
            "reasoning",
            "structured-output",
            "in:text",
            "out:text",
        ] {
            push_unique(&mut keys, key);
        }
    }
    for key in ["capabilities", "inputModalities", "outputModalities"] {
        let prefix = match key {
            "inputModalities" => "in:",
            "outputModalities" => "out:",
            _ => "",
        };
        for value in model
            .get(key)
            .and_then(serde_json::Value::as_array)
            .into_iter()
            .flatten()
            .filter_map(serde_json::Value::as_str)
        {
            push_unique(&mut keys, &format!("{prefix}{value}"));
        }
    }
    keys
}

fn push_unique(values: &mut Vec<String>, value: &str) {
    if !values.iter().any(|existing| existing == value) {
        values.push(value.to_string());
    }
}

pub(in crate::surfaces) fn pick<T>(condition: bool, yes: T, no: T) -> T {
    if condition { yes } else { no }
}
