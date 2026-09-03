use super::*;

impl AzemWindow {
    pub(super) fn model_picker_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        if self
            .reasoning_drag
            .as_ref()
            .is_some_and(|drag| !self.reasoning_drag_is_current(drag))
        {
            self.reasoning_drag = None;
        }
        let query = self
            .model_search
            .read(cx)
            .text()
            .trim()
            .to_ascii_lowercase();
        let route_picker_kind = self.route_picker_target.as_ref().map(|target| target.kind);
        let (selected_provider, selected_model, current_reasoning) = self.model_picker_selection();
        let modes = self.selected_model_modes();
        let fast_supported = modes.fast_available || modes.fast;
        let (fast_icon_name, fast_icon_color) = if modes.fast {
            ("lightning-filled", palette.accent)
        } else {
            ("lightning", palette.ink)
        };
        // Keep the released thumb at its target while the existing request owns persistence.
        let selected_reasoning = self
            .pending_requests
            .values()
            .find_map(|request| match request {
                PendingRequest::ModelSelection {
                    target,
                    session_id,
                    reasoning,
                    ..
                } if target == &self.route_picker_target
                    && session_id == self.state.navigation.current_session_id.as_ref() =>
                {
                    Some(reasoning.clone())
                }
                _ => None,
            })
            .unwrap_or_else(|| modes.reasoning.clone());
        let editable = self.model_controls_enabled();
        let locale = Locale::resolve(&self.state.settings.language);
        let mut rows = Vec::new();
        for provider in self.state.catalogs.providers.clone() {
            if provider.get("enabled").and_then(serde_json::Value::as_bool) == Some(false) {
                continue;
            }
            let provider_id = provider
                .get("id")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_string();
            if provider_id.is_empty() {
                continue;
            }
            let provider_name = ["displayName", "name"]
                .into_iter()
                .find_map(|key| {
                    provider
                        .get(key)
                        .and_then(serde_json::Value::as_str)
                        .filter(|name| !name.trim().is_empty())
                })
                .unwrap_or(&provider_id)
                .to_string();
            for choice in model_choices(
                &provider,
                if provider_id == selected_provider {
                    &selected_model
                } else {
                    ""
                },
                &current_reasoning,
            ) {
                let model = choice.model;
                let model_id = model
                    .get("id")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                if model_id.is_empty() {
                    continue;
                }
                let model_name = choice
                    .family_name
                    .unwrap_or_else(|| catalog_model_name(model, &model_id));
                let aliases = choice.aliases;
                let searchable =
                    format!("{} {} {} {}", provider_id, provider_name, model_id, aliases)
                        .to_ascii_lowercase();
                if !query.is_empty()
                    && !searchable.contains(&query)
                    && !model_name.to_ascii_lowercase().contains(&query)
                {
                    continue;
                }
                let mut metadata = if route_picker_kind.is_some() {
                    provider_picker_label(&provider_id, &provider_name)
                } else {
                    format!(
                        "{} · {}",
                        provider_picker_label(&provider_id, &provider_name),
                        model_capability_hint(model, &model_id, &model_name, locale)
                    )
                };
                if choice.variant_count > 0 {
                    metadata.push_str(" · ");
                    metadata.push_str(&locale.format(
                        "model.variantCount",
                        &[("count", choice.variant_count.to_string())],
                    ));
                }
                let active = provider_id == selected_provider && choice.selected;
                let selected_provider_id = provider_id.clone();
                let selected_model_id = model_id.clone();
                let selected_reasoning = choice.reasoning;
                let row_id = format!("model-option-{provider_id}-{model_id}");
                let model_aria = model_name.clone();
                rows.push(
                    div()
                        .id(row_id)
                        .role(Role::ListItem)
                        .aria_label(model_aria)
                        .aria_selected(active)
                        .tab_stop(editable)
                        .min_h(px(47.))
                        .px_2()
                        .py(px(5.))
                        .rounded(px(9.))
                        .bg(if active {
                            if route_picker_kind.is_some() {
                                palette.paper_muted
                            } else {
                                palette.accent_soft
                            }
                        } else {
                            palette.paper
                        })
                        .flex()
                        .items_center()
                        .gap_2()
                        .when(editable, |row| {
                            row.cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.apply_model_picker_selection(
                                        selected_provider_id.clone(),
                                        selected_model_id.clone(),
                                        selected_reasoning.clone(),
                                        true,
                                        cx,
                                    );
                                }))
                        })
                        .child(
                            div()
                                .w(px(27.))
                                .flex()
                                .items_center()
                                .justify_center()
                                .child(provider_logo(&provider_id, 20., palette.ink)),
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
                                        .text_size(px(11.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(model_name),
                                )
                                .child(
                                    div()
                                        .truncate()
                                        .text_color(palette.faint)
                                        .text_size(px(9.))
                                        .child(metadata),
                                )
                                .when(choice.no_zdr, |detail| {
                                    detail.child(
                                        div()
                                            .text_size(px(9.))
                                            .text_color(palette.danger)
                                            .child(locale.text("model.dataRetention")),
                                    )
                                }),
                        )
                        .when(active, |row| {
                            row.child(icon(
                                "check",
                                15.,
                                if route_picker_kind.is_some() {
                                    palette.ink
                                } else {
                                    palette.accent
                                },
                            ))
                        })
                        .into_any_element(),
                );
            }
        }
        let no_results = rows.is_empty();
        let catalog_loading = self.state.catalogs.providers.is_empty();
        let no_results_label = if catalog_loading {
            locale.text("ui.loadingModelCatalog")
        } else {
            locale.text("ui.noAvailableModelsMatch")
        };
        if route_picker_kind == Some(RoutePickerKind::Model) {
            return div()
                .id("route-model-picker")
                .occlude()
                .on_mouse_down_out(cx.listener(Self::dismiss_picker))
                .role(Role::Region)
                .aria_label(locale.text("ui.selectModel"))
                .absolute()
                .top(px(44.))
                .right(px(94.))
                .w(px(292.))
                .max_h(px(310.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.15, 0.14))
                        .blur_radius(px(22.)),
                ])
                .overflow_hidden()
                .flex()
                .flex_col()
                .child(
                    div()
                        .h(px(34.))
                        .px(px(11.))
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 15., palette.faint))
                        .child(div().flex_1().h_full().child(self.model_search.clone())),
                )
                .child(
                    div()
                        .id("route-model-picker-list")
                        .role(Role::ListBox)
                        .aria_label(locale.text("ui.models3"))
                        .max_h(px(260.))
                        .p(px(5.))
                        .overflow_y_scroll()
                        .flex()
                        .flex_col()
                        .gap(px(2.))
                        .when(no_results, |list| {
                            list.child(
                                div()
                                    .h(px(112.))
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_color(palette.faint)
                                    .text_sm()
                                    .child(no_results_label),
                            )
                        })
                        .children(rows),
                )
                .into_any_element();
        }
        let reasoning_levels = &modes.levels;
        let reasoning_editable = editable && reasoning_levels.len() > 1;
        if route_picker_kind == Some(RoutePickerKind::Reasoning) {
            let mut options = Vec::with_capacity(reasoning_levels.len());
            for (index, level) in reasoning_levels.iter().enumerate() {
                let target = level.clone();
                let selected = target == selected_reasoning;
                options.push(
                    div()
                        .id(("route-reasoning-option", index))
                        .role(if reasoning_editable {
                            Role::RadioButton
                        } else {
                            Role::Label
                        })
                        .aria_label(reasoning_display_name(level, locale))
                        .aria_selected(selected)
                        .tab_stop(reasoning_editable)
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .bg(if selected {
                            palette.paper_muted
                        } else {
                            palette.paper
                        })
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .flex()
                        .items_center()
                        .when(reasoning_levels.len() == 1, |option| {
                            option.aria_description(locale.text("model.fixedReasoning"))
                        })
                        .when(reasoning_editable, |option| {
                            option
                                .cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.set_reasoning(target.clone(), cx);
                                    this.model_picker_open = false;
                                    this.route_picker_target = None;
                                    cx.notify();
                                }))
                        })
                        .child(reasoning_display_name(level, locale))
                        .child(div().flex_1())
                        .when(selected, |option| {
                            option.child(icon("check", 14., palette.ink))
                        })
                        .into_any_element(),
                );
            }
            return div()
                .id("route-reasoning-picker")
                .occlude()
                .on_mouse_down_out(cx.listener(Self::dismiss_picker))
                .role(Role::RadioGroup)
                .aria_label(locale.text("ui.reasoningEffort"))
                .absolute()
                .top(px(44.))
                .right_0()
                .w(px(148.))
                .p(px(5.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.15, 0.14))
                        .blur_radius(px(22.)),
                ])
                .flex()
                .flex_col()
                .gap(px(2.))
                .children(options)
                .into_any_element();
        }
        let mut selected_reasoning_index = reasoning_levels
            .iter()
            .position(|level| level == &selected_reasoning)
            .unwrap_or_default();
        let last_reasoning_index = reasoning_levels.len().saturating_sub(1);
        let reasoning_track_width = self
            .reasoning_slider_bounds
            .map(|bounds| f32::from(bounds.size.width))
            .filter(|width| *width > REASONING_THUMB_INSET * 2.)
            .unwrap_or(MODEL_PICKER_WIDTH - 32.);
        let selected_reasoning_offset = if let Some(drag) = &self.reasoning_drag {
            let offset = reasoning_offset_from_progress(reasoning_track_width, drag.progress);
            selected_reasoning_index = reasoning_index_from_position(
                offset,
                0.,
                reasoning_track_width,
                reasoning_levels.len(),
            );
            offset
        } else {
            reasoning_stop_offset(
                reasoning_track_width,
                selected_reasoning_index,
                reasoning_levels.len(),
            )
        };
        if self.model_picker_open {
            tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "render", offset = selected_reasoning_offset, index = selected_reasoning_index, editable = reasoning_editable, transcript_item = self.transcript_list.logical_scroll_top().item_ix, transcript_offset = f32::from(self.transcript_list.logical_scroll_top().offset_in_item));
        }
        let reasoning_fill_width = (selected_reasoning_offset - REASONING_RAIL_INSET)
            .clamp(0., reasoning_track_width - REASONING_RAIL_INSET * 2.);
        let reasoning_ticks = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, _)| {
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len())
                        - REASONING_RAIL_INSET;
                div()
                    .absolute()
                    .left(px(offset - REASONING_TICK_SIZE / 2.))
                    .top(px((REASONING_RAIL_HEIGHT - REASONING_TICK_SIZE) / 2.))
                    .size(px(REASONING_TICK_SIZE))
                    .rounded_full()
                    .bg(rgba(0xffffff66))
            })
            .collect::<Vec<_>>();
        let reasoning_controls = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, level)| {
                let target = level.clone();
                let selected = index == selected_reasoning_index;
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len());
                let previous = reasoning_stop_offset(
                    reasoning_track_width,
                    index.saturating_sub(1),
                    reasoning_levels.len(),
                );
                let next = reasoning_stop_offset(
                    reasoning_track_width,
                    (index + 1).min(last_reasoning_index),
                    reasoning_levels.len(),
                );
                let left = if index == 0 {
                    0.
                } else {
                    (previous + offset) / 2.
                };
                let right = if index == last_reasoning_index {
                    reasoning_track_width
                } else {
                    (offset + next) / 2.
                };
                div()
                    .id(("reasoning-level", index))
                    .role(if reasoning_editable {
                        Role::RadioButton
                    } else {
                        Role::Label
                    })
                    .aria_label(reasoning_display_name(level, locale))
                    .aria_selected(selected)
                    .tab_stop(reasoning_editable)
                    .absolute()
                    .left(px(left))
                    .top_0()
                    .w(px(right - left))
                    .h(px(REASONING_TRACK_HEIGHT))
                    .when(reasoning_levels.len() == 1, |control| {
                        control.aria_description(locale.text("model.fixedReasoning"))
                    })
                    .when(reasoning_editable, |control| {
                        control.cursor_pointer().on_click(cx.listener(
                            move |this, event: &ClickEvent, _, cx| {
                                // Mouse selection is committed once by the captured release.
                                if !matches!(event, ClickEvent::Mouse(_)) {
                                    this.set_reasoning(target.clone(), cx);
                                }
                            },
                        ))
                    })
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        let reasoning_labels = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, level)| {
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len());
                div()
                    .absolute()
                    .left(px(offset - 18.))
                    .top_0()
                    .w(px(36.))
                    .text_size(px(9.))
                    .text_color(if index == selected_reasoning_index {
                        rgb(0x319aff)
                    } else {
                        palette.faint
                    })
                    .font_weight(if index == selected_reasoning_index {
                        gpui::FontWeight::BOLD
                    } else {
                        gpui::FontWeight::NORMAL
                    })
                    .text_center()
                    .whitespace_nowrap()
                    .child(reasoning_display_name(level, locale))
            })
            .collect::<Vec<_>>();
        let slider_owner = cx.entity();
        let slider_mouse_move = cx.listener(Self::reasoning_mouse_move);
        let slider_mouse_up = cx.listener(Self::reasoning_mouse_up);
        div()
        .id("model-picker")
        .occlude()
        .on_mouse_down_out(cx.listener(Self::dismiss_picker))
        .role(Role::Region)
        .aria_label(locale.text("ui.modelAndReasoning"))
        .absolute()
        .right(px(44.))
        .bottom(px(48.))
        .w(px(MODEL_PICKER_WIDTH))
        .max_h(px(560.))
        .rounded(px(13.))
        .border_1()
        .border_color(palette.border_strong)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.15, 0.15, 0.15))
                .blur_radius(px(30.)),
        ])
        .p(px(6.))
        .flex()
        .flex_col()
        .child(
            div()
                .px_2()
                .pt(px(7.))
                .pb(px(6.))
                .flex()
                .items_center()
                .child(
                    div()
                        .text_size(px(11.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.modelAndReasoning")),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .text_size(px(9.))
                        .text_color(palette.faint)
                        .child(locale.text("ui.appliesToThisConversation")),
                ),
        )
        .child(
            div()
                .h(px(38.))
                .mx(px(7.))
                .mb(px(5.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_2()
                .child(icon("search", 15., palette.faint))
                .child(div().flex_1().h_full().child(self.model_search.clone()))
                .child(
                    div()
                        .text_size(px(8.))
                        .text_color(palette.faint)
                        .child("⌘F"),
                ),
        )
        .child(
            div()
                .id("model-picker-list")
                .role(Role::ListBox)
                .aria_label(locale.text("ui.models3"))
                .max_h(px(226.))
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .gap(px(2.))
                .when(no_results, |list| {
                    list.child(
                        div()
                            .h(px(112.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_color(palette.faint)
                            .text_sm()
                            .child(no_results_label),
                    )
                })
                .children(rows),
        )
        .when(
            !reasoning_levels.is_empty() || modes.fast_available || modes.fast,
            |picker| {
                picker.child(
                    div()
                        .mt(px(5.))
                        .px(px(9.))
                        .pt(px(11.))
                        .pb(px(9.))
                        .border_t_1()
                        .border_color(palette.border)
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .id("model-reasoning-heading")
                                .min_h(px(32.))
                                .pr(px(24.))
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(div().min_w_0().flex_1().flex().flex_col().when(
                                    !reasoning_levels.is_empty(),
                                    |heading| {
                                        heading
                                            .child(
                                                div()
                                                    .text_size(px(11.))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(locale.text("ui.reasoningEffort")),
                                            )
                                            .child(
                                                div()
                                                    .mt(px(1.))
                                                    .text_size(px(9.))
                                                    .text_color(palette.faint)
                                                    .child(locale.text(
                                                        "ui.higherIsDeeperAndTakesLonger",
                                                    )),
                                            )
                                    },
                                ))
                                .when(fast_supported, |heading| {
                                    let fast_editable = editable && modes.fast_available;
                                    let tooltip_label = locale
                                        .text(if modes.fast_available {
                                            "model.fastDescription"
                                        } else {
                                            "model.fastUnavailable"
                                        })
                                        .to_string();
                                    heading.child(
                                        div()
                                            .id("model-fast-toggle")
                                            .role(Role::Switch)
                                            .aria_label(locale.text("model.fastDescription"))
                                            .aria_description(tooltip_label.clone())
                                            .aria_toggled(modes.fast.into())
                                            .tab_stop(fast_editable)
                                            .size(px(32.))
                                            .flex_shrink_0()
                                            .rounded(px(7.))
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
                                            .when(fast_editable, |toggle| {
                                                toggle
                                                    .cursor_pointer()
                                                    .hover(move |style| style.bg(palette.hover))
                                                    .active(move |style| {
                                                        style.bg(palette.paper_muted)
                                                    })
                                                    .on_click(cx.listener(|this, _, _, cx| {
                                                        this.toggle_fast_mode(cx)
                                                    }))
                                            })
                                            .child(icon(fast_icon_name, 18., fast_icon_color)),
                                    )
                                }),
                        )
                        .when(!reasoning_levels.is_empty(), |section| {
                            section
                                .child(
                                    div()
                                        .mt(px(9.))
                                        .h(px(REASONING_TRACK_HEIGHT))
                                        .relative()
                                        .when(reasoning_editable, |slider| {
                                            slider
                                                .cursor_pointer()
                                                .on_mouse_down(
                                                    MouseButton::Left,
                                                    cx.listener(Self::reasoning_mouse_down),
                                                )
                                        })
                                        .on_children_prepainted(move |bounds, _, cx| {
                                            let Some(bounds) = bounds.first().copied() else {
                                                return;
                                            };
                                            slider_owner.update(cx, |this, cx| {
                                                if this.reasoning_slider_bounds != Some(bounds)
                                                {
                                                    this.reasoning_slider_bounds = Some(bounds);
                                                    cx.notify();
                                                }
                                            });
                                        })
                                        .child(
                                            gpui::canvas(
                                                |_, _, _| (),
                                                move |_, _, window, _| {
                                                    // Capture the gesture, including moves and release outside the rail.
                                                    window.on_mouse_event(move |event: &MouseMoveEvent, phase, window, cx| {
                                                        if phase == gpui::DispatchPhase::Capture {
                                                            slider_mouse_move(event, window, cx);
                                                        }
                                                    });
                                                    window.on_mouse_event(move |event: &MouseUpEvent, phase, window, cx| {
                                                        if phase == gpui::DispatchPhase::Capture {
                                                            slider_mouse_up(event, window, cx);
                                                        }
                                                    });
                                                },
                                            )
                                            .absolute()
                                            .inset_0(),
                                        )
                                        .child(
                                            div()
                                                .absolute()
                                                .left(px(REASONING_RAIL_INSET))
                                                .right(px(REASONING_RAIL_INSET))
                                                .top(px((REASONING_TRACK_HEIGHT - REASONING_RAIL_HEIGHT) / 2.))
                                                .h(px(REASONING_RAIL_HEIGHT))
                                                .rounded_full()
                                                .overflow_hidden()
                                                .bg(palette.paper_muted)
                                                .child(
                                                    div()
                                                        .relative()
                                                        .h_full()
                                                        .w(px(reasoning_fill_width))
                                                        .rounded_full()
                                                        .overflow_hidden()
                                                        .bg(rgb(0x319aff))
                                                        .child(
                                                            div()
                                                                .absolute()
                                                                .left_0()
                                                                .top_0()
                                                                .w(px(reasoning_track_width - REASONING_RAIL_INSET * 2.))
                                                                .h_full()
                                                                .children(reasoning_ticks)
                                                                .when(modes.fast, |track| {
                                                                    track.child(self.fast_particles.clone())
                                                                }),
                                                        ),
                                                ),
                                        )
                                        .child(
                                            div()
                                                .absolute()
                                                .inset_0()
                                                .top_0()
                                                .h(px(REASONING_TRACK_HEIGHT))
                                                .relative()
                                                .child(
                                                    div()
                                                        .absolute()
                                                        .top(px((REASONING_TRACK_HEIGHT - REASONING_THUMB_SIZE) / 2.))
                                                        .left(px(
                                                            selected_reasoning_offset - REASONING_THUMB_SIZE / 2.
                                                        ))
                                                        .size(px(REASONING_THUMB_SIZE))
                                                        .rounded_full()
                                                        .border_1()
                                                        .border_color(palette.border_strong)
                                                        .bg(palette.paper)
                                                        .shadow(vec![
                                                            BoxShadow::new(
                                                                px(0.),
                                                                px(0.),
                                                                hsla(0., 0., 0., 0.10),
                                                            )
                                                            .blur_radius(px(2.)),
                                                        ]),
                                                )
                                                .child(
                                                    div()
                                                        .absolute()
                                                        .inset_0()
                                                        .children(reasoning_controls),
                                                ),
                                        ),
                                )
                                .child(div().relative().h(px(13.)).children(reasoning_labels))
                        }),
                )
            },
        )
        .when(!self.model_picker_error.is_empty(), |picker| {
            picker.child(
                div()
                    .px_2()
                    .py_1()
                    .text_size(px(10.))
                    .text_color(palette.danger)
                    .child(self.model_picker_error.clone()),
            )
        })
        .into_any_element()
    }
}
