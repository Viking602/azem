use super::*;
pub(super) fn environment_panel_return_spring(progress: f32) -> f32 {
    if progress >= 1. {
        1.
    } else {
        let phase = 5.5 * progress;
        1. - (-4. * progress).exp() * (phase.cos() + 0.73 * phase.sin())
    }
}

#[derive(Debug, Default)]
pub(super) struct EnvironmentSnapshot {
    pub(super) has_subagents: bool,
    pub(super) running_agents: usize,
    pub(super) completed_agents: usize,
    pub(super) running_terminals: usize,
    pub(super) current_pull_request: Option<String>,
}

pub(super) fn environment_snapshot(state: &AppState) -> EnvironmentSnapshot {
    let running_agents = state
        .runtime
        .agents
        .iter()
        .filter(|agent| {
            matches!(
                agent.get("state").and_then(serde_json::Value::as_str),
                Some("running" | "queued" | "pending")
            )
        })
        .count();
    let completed_agents = state
        .runtime
        .agents
        .iter()
        .filter(|agent| {
            matches!(
                agent.get("state").and_then(serde_json::Value::as_str),
                Some("completed" | "failed" | "cancelled")
            )
        })
        .count();
    EnvironmentSnapshot {
        has_subagents: !state.runtime.agents.is_empty(),
        running_agents,
        completed_agents,
        running_terminals: state
            .terminals
            .sessions
            .iter()
            .filter(|session| {
                session.get("state").and_then(serde_json::Value::as_str) == Some("running")
            })
            .count(),
        current_pull_request: state
            .pull_requests
            .dashboard
            .get("current")
            .filter(|pull_request| !pull_request.is_null())
            .and_then(|pull_request| {
                pull_request
                    .get("title")
                    .and_then(serde_json::Value::as_str)
                    .or_else(|| pull_request.get("head").and_then(serde_json::Value::as_str))
            })
            .map(str::to_string),
    }
}

pub(super) fn todo_status_mark(status: &str) -> &'static str {
    match status {
        "completed" => "✓",
        "cancelled" => "−",
        "in_progress" => "●",
        _ => "○",
    }
}

pub(super) fn plan_item_animates(status: &str, running: bool, reduced: bool) -> bool {
    status == "in_progress" && running && !reduced
}

pub(crate) fn plan_items(todo: &serde_json::Value) -> Vec<serde_json::Value> {
    todo.get("phases")
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|phase| phase.get("items").and_then(serde_json::Value::as_array))
        .flatten()
        .cloned()
        .collect()
}

fn plan_list(
    items: &[serde_json::Value],
    palette: ThemePalette,
    reduced: bool,
    running: bool,
    id: &'static str,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::List)
        .max_h(px(224.))
        .overflow_y_scroll()
        .px(px(10.))
        .pb(px(6.))
        .flex()
        .flex_col()
        .children(items.iter().enumerate().map(|(index, item)| {
            let status = item
                .get("status")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("pending");
            let done = matches!(status, "completed" | "cancelled");
            let color = if done {
                palette.faint
            } else {
                palette.ink_soft
            };
            let mark = if plan_item_animates(status, running, reduced) {
                icon("loader", 12., color)
                    .with_animation(
                        (id, index),
                        Animation::new(Duration::from_millis(800)).repeat(),
                        |icon, progress| {
                            icon.with_transformation(Transformation::rotate(percentage(progress)))
                        },
                    )
                    .into_any_element()
            } else if status == "pending" {
                div()
                    .size(px(14.))
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(
                        div()
                            .size(px(7.))
                            .rounded_full()
                            .border_1()
                            .border_color(palette.faint),
                    )
                    .into_any_element()
            } else if status == "cancelled" {
                div()
                    .w(px(14.))
                    .child(todo_status_mark(status))
                    .into_any_element()
            } else {
                icon(
                    if status == "completed" {
                        "check"
                    } else {
                        "loader"
                    },
                    12.,
                    color,
                )
                .into_any_element()
            };
            div()
                .id((id, index))
                .role(Role::ListItem)
                .flex_shrink_0()
                .py_1()
                .flex()
                .items_start()
                .gap_2()
                .text_size(px(13.))
                .line_height(px(20.))
                .text_color(color)
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(6.))
                        .mt(px(3.))
                        .flex_shrink_0()
                        .text_size(px(12.))
                        .child(mark)
                        .child(format!("{}.", index + 1)),
                )
                .child(
                    div()
                        .min_w_0()
                        .flex_1()
                        .whitespace_normal()
                        .when(done, |text| text.line_through())
                        .child(
                            item.get("content")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string(),
                        ),
                )
        }))
        .into_any_element()
}

pub(super) fn plan_banner_visible(items: &[serde_json::Value], running: bool) -> bool {
    running
        && items.iter().any(|item| {
            !matches!(
                item.get("status").and_then(serde_json::Value::as_str),
                Some("completed" | "cancelled")
            )
        })
}

pub(crate) fn composer_plan(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> Option<gpui::AnyElement> {
    let items = plan_items(&this.state.runtime.todo);
    if !plan_banner_visible(&items, this.state.runtime.running) {
        return None;
    }
    let completed = items
        .iter()
        .filter(|item| item.get("status").and_then(serde_json::Value::as_str) == Some("completed"))
        .count();
    let key = format!("plan-expanded:{}", this.state.navigation.current_session_id);
    let compact = !this.process_expansion.borrow().is_expanded(&key);
    let current_task = items
        .iter()
        .find(|item| item["status"] == "in_progress")
        .or_else(|| items.iter().find(|item| item["status"] == "pending"))
        .and_then(|item| item.get("content").and_then(serde_json::Value::as_str));
    let reduced = this
        .state
        .settings
        .appearance
        .get("reducedMotion")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let running = this.state.runtime.running;
    let header_icon = if compact
        && !reduced
        && running
        && items.iter().any(|item| {
            item.get("status").and_then(serde_json::Value::as_str) == Some("in_progress")
        }) {
        icon("loader", 14., palette.muted)
            .with_animation(
                "plan-header-running",
                Animation::new(Duration::from_millis(800)).repeat(),
                |icon, progress| {
                    icon.with_transformation(Transformation::rotate(percentage(progress)))
                },
            )
            .into_any_element()
    } else {
        icon("sliders-horizontal", 14., palette.muted).into_any_element()
    };
    let summary = locale.format(
        "plan.progress",
        &[
            ("completed", completed.to_string()),
            ("total", items.len().to_string()),
        ],
    );
    Some(
        div()
            .id("composer-plan")
            .role(Role::Group)
            .aria_label(locale.text("plan.title"))
            .w(relative(11. / 12.))
            .mx_auto()
            .bg(Rgba {
                a: 0.5,
                ..palette.paper
            })
            .border_1()
            .border_color(palette.border)
            .border_b_0()
            .rounded_t(px(12.))
            .mb(px(-1.))
            .overflow_hidden()
            .child(
                div()
                    .px(px(10.))
                    .py(px(6.))
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(header_icon)
                    .child(
                        div()
                            .flex_shrink_0()
                            .when(!compact, |summary| summary.flex_1())
                            .min_w_0()
                            .text_size(px(12.))
                            .text_color(palette.muted)
                            .child(summary),
                    )
                    .when(compact, |header| {
                        header.child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(div().text_color(palette.faint).child("·"))
                                .child(
                                    div()
                                        .min_w_0()
                                        .truncate()
                                        .text_size(px(12.))
                                        .text_color(palette.ink_soft)
                                        .child(current_task.unwrap_or_default().to_string()),
                                ),
                        )
                    })
                    .child(
                        div()
                            .id("plan-toggle-compact")
                            .role(Role::Button)
                            .aria_expanded(!compact)
                            .aria_label(locale.text(if compact {
                                "plan.expand"
                            } else {
                                "plan.collapse"
                            }))
                            .tab_stop(true)
                            .size(px(24.))
                            .rounded(px(6.))
                            .hover(move |style| style.bg(palette.hover))
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .child(icon(
                                if compact {
                                    "arrows-out-simple"
                                } else {
                                    "arrows-in-simple"
                                },
                                12.,
                                palette.muted,
                            ))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.process_expansion.borrow_mut().toggle(&key);
                                cx.notify();
                            })),
                    ),
            )
            .when(!compact, |panel| {
                panel.child(plan_list(
                    &items,
                    palette,
                    reduced,
                    running,
                    "composer-plan-items",
                ))
            })
            .into_any_element(),
    )
}

pub(crate) fn environment_panel(
    this: &AzemWindow,
    palette: ThemePalette,
    labels: Labels,
    right_inset: f32,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let state = &this.state;
    let reduced_motion = state
        .settings
        .appearance
        .get("reducedMotion")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let snapshot = environment_snapshot(state);
    let locale = Locale::resolve(&state.settings.language);
    let panel = div()
        .id("environment-panel-card")
        .w(px(288.))
        .mr(px(12.))
        .flex_shrink_0()
        .max_h(px(520.))
        .rounded(px(16.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.12, 0.12, 0.05))
                .blur_radius(px(10.)),
        ])
        .overflow_y_scroll()
        .p(px(10.))
        .flex()
        .flex_col()
        .child(
            div()
                .h(px(32.))
                .px_1()
                .mb_1()
                .flex()
                .items_center()
                .child(
                    div()
                        .text_color(palette.faint)
                        .text_xs()
                        .child(locale.text("ui.environmentInfo")),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .id("environment-settings")
                        .role(Role::Button)
                        .aria_label(labels.settings)
                        .tab_stop(true)
                        .size(px(28.))
                        .rounded(px(7.))
                        .text_color(palette.muted)
                        .flex()
                        .items_center()
                        .justify_center()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.settings_open = true;
                            this.settings_provider = None;
                            if this.state.catalogs.providers.is_empty() {
                                this.refresh_model_catalog();
                            }
                            cx.notify();
                        }))
                        .child(icon("settings", 14., palette.muted)),
                ),
        )
        .child(environment_changes_row(state, labels, palette, cx))
        .child(branch_picker_control(this, true, palette, locale, cx))
        .child(environment_nav_row(
            "environment-services",
            "server",
            locale.text("ui.localServices").to_string(),
            format!("● {}  ⌄", snapshot.running_terminals),
            Some(Surface::Terminal),
            palette,
            cx,
        ))
        .when_some(snapshot.current_pull_request.clone(), |panel, title| {
            panel.child(environment_nav_row(
                "environment-pull-request",
                "git-pull-request",
                title,
                "⌄".to_string(),
                Some(Surface::PullRequests),
                palette,
                cx,
            ))
        })
        .when(snapshot.has_subagents, |panel| {
            panel
                .child(environment_divider(palette))
                .child(environment_label(locale.text("ui.subagents"), palette))
                .child(environment_subagent_summary(
                    this,
                    snapshot.running_agents,
                    snapshot.completed_agents,
                    palette,
                    locale,
                    cx,
                ))
        })
        .child(environment_divider(palette))
        .child(environment_label(locale.text("ui.editor"), palette))
        .child(environment_nav_row(
            "environment-local",
            "sidebar-simple",
            locale.text("ui.editorView").to_string(),
            "⌄".to_string(),
            Some(Surface::Files),
            palette,
            cx,
        ))
        .child(environment_divider(palette))
        .child(environment_label(locale.text("ui.recap"), palette))
        .child(recap_panel(&state.runtime.recap, palette, locale))
        .into_any_element();
    let reveal = div()
        .w(px(312.))
        .max_h(px(520.))
        .flex()
        .justify_end()
        .child(
            div()
                .w(px(312.))
                .flex_shrink_0()
                .flex()
                .justify_end()
                .child(panel),
        );
    let reveal = if reduced_motion {
        reveal.into_any_element()
    } else {
        reveal
            .with_animation(
                "environment-panel-return",
                Animation::new(ENVIRONMENT_PANEL_RETURN_TRANSITION)
                    .with_easing(environment_panel_return_spring),
                |panel, progress| {
                    panel
                        .w(px(312. * progress.max(0.)))
                        .opacity(progress.clamp(0., 1.))
                },
            )
            .into_any_element()
    };
    div()
        .id("environment-panel")
        .role(Role::Region)
        .aria_label(locale.text("ui.environment"))
        .absolute()
        .top(px(12.))
        .right(px(right_inset))
        .w(px(312.))
        .max_h(px(520.))
        .flex()
        .justify_end()
        .child(reveal)
        .into_any_element()
}

pub(crate) fn side_panel(
    this: &AzemWindow,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&this.state.settings.language);
    let panel_width = this.side_panel_width;
    let visible_width = this.side_panel_visible_width;
    let rail = div()
        .id("side-panel-actions")
        .absolute()
        .top(px(0.))
        .bottom(px(0.))
        .left(px((panel_width - 180.) / 2.))
        .w(px(180.))
        .px_2()
        .flex()
        .flex_col()
        .justify_center()
        .gap_1()
        .child(environment_nav_row(
            "side-panel-review",
            "shield-check",
            locale.text("ui.review").to_string(),
            String::new(),
            Some(Surface::Security),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "side-panel-terminal",
            "terminal",
            labels.terminal.to_string(),
            String::new(),
            Some(Surface::Terminal),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "side-panel-files",
            "folder",
            labels.files.to_string(),
            String::new(),
            Some(Surface::Files),
            palette,
            cx,
        ))
        .child(environment_subagent_action(this, palette, locale, cx));
    let panel = div()
        .id("side-panel")
        .role(Role::Region)
        .aria_label(locale.text("ui.sidePanel"))
        .absolute()
        .top(px(0.))
        .right(px(-(panel_width - visible_width)))
        .bottom(px(0.))
        .w(px(panel_width))
        .opacity((visible_width / panel_width).clamp(0., 1.))
        .bg(palette.paper)
        .overflow_hidden()
        .child(rail)
        .child(side_panel_resize_handle(panel_width, palette, locale, cx));
    panel.into_any_element()
}

fn side_panel_resize_handle(
    panel_width: f32,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let resize_mouse_move = cx.listener(AzemWindow::side_panel_resize_mouse_move);
    let resize_mouse_up = cx.listener(AzemWindow::side_panel_resize_mouse_up);
    div()
        .id("side-panel-resize-handle")
        .role(Role::Splitter)
        .aria_label(locale.text("ui.resizeSidePanel"))
        .aria_value(format!("{} px", panel_width.round() as i64))
        .tab_stop(false)
        .absolute()
        .top(px(0.))
        .left(px(0.))
        .bottom(px(0.))
        .w(px(7.))
        .cursor_col_resize()
        .on_mouse_down(
            gpui::MouseButton::Left,
            cx.listener(AzemWindow::side_panel_resize_mouse_down),
        )
        .child(
            gpui::canvas(
                |_, _, _| (),
                move |_, _, window, _| {
                    window.on_mouse_event(
                        move |event: &gpui::MouseMoveEvent, phase, window, cx| {
                            if phase == gpui::DispatchPhase::Capture {
                                resize_mouse_move(event, window, cx);
                            }
                        },
                    );
                    window.on_mouse_event(move |event: &gpui::MouseUpEvent, phase, window, cx| {
                        if phase == gpui::DispatchPhase::Capture {
                            resize_mouse_up(event, window, cx);
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
                .top(px(0.))
                .bottom(px(0.))
                .left(px(3.))
                .w(px(1.))
                .bg(palette.border),
        )
        .into_any_element()
}

fn environment_changes_row(
    state: &AppState,
    labels: Labels,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id("environment-changes")
        .role(Role::Button)
        .aria_label(labels.changes)
        .tab_stop(true)
        .h(px(38.))
        .px_2()
        .rounded(px(8.))
        .text_xs()
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |row| row.bg(palette.paper_muted))
        .on_click(cx.listener(|this, _, _, cx| {
            this.state.navigation.surface = Surface::Changes;
            this.request_surface(Surface::Changes);
            cx.notify();
        }))
        .child(
            div()
                .w(px(18.))
                .child(icon("file-diff", 16., palette.ink_soft)),
        )
        .child(div().flex_1().text_color(palette.ink).child(labels.changes))
        .child(
            div()
                .flex()
                .gap_1()
                .child(
                    div()
                        .text_color(palette.positive)
                        .child(format!("+{}", state.workspace.additions)),
                )
                .child(
                    div()
                        .text_color(palette.danger)
                        .child(format!("-{}", state.workspace.deletions)),
                ),
        )
        .into_any_element()
}

fn environment_subagent_summary(
    this: &AzemWindow,
    running: usize,
    completed: usize,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let status = if running == 0 {
        locale.text("ui.noRunningSubagents").to_string()
    } else {
        format!("{} {}", running, locale.text("ui.running"))
    };
    let trailing = if completed == 0 {
        String::new()
    } else {
        format!("{} {}", completed, locale.text("ui.completed"))
    };
    environment_agent_row(
        "environment-agent-summary",
        status,
        trailing,
        this,
        palette,
        locale,
        cx,
    )
}

fn environment_subagent_action(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    environment_agent_row(
        "side-panel-subagents",
        locale.text("ui.subagents").to_string(),
        String::new(),
        this,
        palette,
        locale,
        cx,
    )
}

fn environment_agent_row(
    id: &'static str,
    label: String,
    trailing: String,
    _this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(locale.text("ui.subagents"))
        .tab_stop(true)
        .h(px(38.))
        .px_2()
        .rounded(px(8.))
        .text_xs()
        .text_color(palette.ink)
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |row| row.bg(palette.paper_muted))
        .on_click(cx.listener(move |this, _, window, cx| {
            this.open_agent_roster(window, cx);
        }))
        .child(div().w(px(18.)).child(icon("bot", 16., palette.ink_soft)))
        .child(div().min_w_0().flex_1().truncate().child(label))
        .when(!trailing.is_empty(), |row| {
            row.child(div().text_color(palette.faint).child(trailing))
        })
        .into_any_element()
}

pub(crate) fn agent_panel_tab(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let panel_width = this.side_panel_width;
    let visible_width = this.side_panel_visible_width;
    let add_menu_open = this.side_panel_add_menu_open;
    div()
        .id("agent-panel-tabs")
        .role(Role::TabList)
        .aria_label(locale.text("ui.subagents"))
        .absolute()
        .top(px(0.))
        .right(px(-(panel_width - visible_width)))
        .h(px(46.))
        .w(px(panel_width))
        .pl(px(10.))
        .border_l_1()
        .border_color(palette.border)
        .opacity((visible_width / panel_width).clamp(0., 1.))
        .bg(palette.paper)
        .flex()
        .items_center()
        .gap_2()
        .child(
            div()
                .h(px(30.))
                .rounded(px(8.))
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .child(
                    div()
                        .id("agent-workspace-tab")
                        .role(Role::Tab)
                        .aria_label(locale.text("ui.subagents"))
                        .aria_selected(true)
                        .tab_stop(true)
                        .h_full()
                        .pl_2()
                        .pr_1()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .on_click(cx.listener(|this, _, window, cx| {
                            this.open_agent_roster(window, cx);
                        }))
                        .child(icon("bot", 14., palette.ink_soft))
                        .child(
                            div()
                                .text_xs()
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .text_color(palette.ink)
                                .child(locale.text("ui.subagents")),
                        ),
                )
                .child(
                    div()
                        .id("agent-panel-close")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.sidePanel"))
                        .tab_stop(true)
                        .size(px(24.))
                        .mr_1()
                        .rounded(px(6.))
                        .flex()
                        .items_center()
                        .justify_center()
                        .cursor_pointer()
                        .hover(move |button| button.bg(palette.hover))
                        .on_click(cx.listener(|this, _, window, cx| {
                            this.toggle_side_panel(window, cx);
                        }))
                        .text_size(px(15.))
                        .text_color(palette.muted)
                        .child("×"),
                ),
        )
        .child(
            div()
                .id("agent-panel-add")
                .role(Role::Button)
                .aria_label(locale.text("ui.addPanel"))
                .aria_expanded(add_menu_open)
                .tab_stop(true)
                .size(px(30.))
                .rounded(px(8.))
                .bg(if add_menu_open {
                    palette.hover
                } else {
                    palette.paper_muted
                })
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .hover(move |button| button.bg(palette.hover))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.side_panel_add_menu_open = !this.side_panel_add_menu_open;
                    cx.stop_propagation();
                    cx.notify();
                }))
                .child(icon("plus", 17., palette.ink_soft)),
        )
        .into_any_element()
}

fn agent_panel_add_menu(
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let items = [
        (
            "agent-panel-add-review",
            "shield-check",
            locale.text("ui.review"),
            "review",
        ),
        (
            "agent-panel-add-terminal",
            "terminal",
            locale.text("app.terminal"),
            "terminal",
        ),
        (
            "agent-panel-add-browser",
            "link",
            locale.text("ui.browser"),
            "browser",
        ),
        (
            "agent-panel-add-files",
            "folder",
            locale.text("app.files"),
            "files",
        ),
        (
            "agent-panel-add-side-chat",
            "message-square-text",
            locale.text("ui.sideChat"),
            "side-chat",
        ),
    ];
    div()
        .id("agent-panel-add-menu")
        .role(Role::Menu)
        .aria_label(locale.text("ui.addPanel"))
        .absolute()
        .top(px(6.))
        .left(px(112.))
        .w(px(280.))
        .p_2()
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.12, 0.12, 0.14))
                .blur_radius(px(28.)),
        ])
        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
        .children(items.into_iter().map(|(id, icon_name, label, action)| {
            div()
                .id(id)
                .role(Role::MenuItem)
                .tab_stop(true)
                .h(px(42.))
                .px_3()
                .rounded(px(8.))
                .text_sm()
                .text_color(palette.ink)
                .flex()
                .items_center()
                .gap_3()
                .cursor_pointer()
                .hover(move |row| row.bg(palette.hover))
                .on_click(cx.listener(move |this, _, window, cx| {
                    this.side_panel_add_menu_open = false;
                    match action {
                        "review" => {
                            this.hide_side_panel();
                            this.state.navigation.surface = Surface::Changes;
                            this.request_surface(Surface::Changes);
                        }
                        "terminal" => this.set_terminal_open(true, window, cx),
                        "browser" => cx.open_url("https://www.google.com"),
                        "files" => {
                            this.hide_side_panel();
                            this.state.navigation.surface = Surface::Files;
                            this.request_surface(Surface::Files);
                        }
                        "side-chat" => this.open_agent_roster(window, cx),
                        _ => {}
                    }
                    cx.notify();
                }))
                .child(div().w(px(20.)).child(icon(icon_name, 17., palette.muted)))
                .child(label)
        }))
        .into_any_element()
}

pub(crate) fn agent_side_panel(
    this: &AzemWindow,
    palette: ThemePalette,
    selected_id: &str,
    reduced_motion: bool,
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let state = &this.state;
    let locale = Locale::resolve(&state.settings.language);
    let panel_width = this.side_panel_width;
    let visible_width = this.side_panel_visible_width;
    let detail_open = !selected_id.is_empty();
    let selected = state
        .runtime
        .agents
        .iter()
        .find(|agent| agent.get("id").and_then(serde_json::Value::as_str) == Some(selected_id));
    let field = |key| {
        selected
            .and_then(|agent| agent.get(key))
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .trim()
    };
    let role = [field("description"), field("type"), selected_id]
        .into_iter()
        .find(|value| !value.is_empty())
        .unwrap_or(locale.text("ui.subagent"))
        .to_string();
    let state_name = field("state");
    let active = is_active_agent_state(state_name);
    let elapsed_ms = selected
        .and_then(|agent| agent.get("elapsedMs"))
        .and_then(|value| {
            value
                .as_i64()
                .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
        })
        .unwrap_or_default();
    let elapsed = locale.format(
        "ui.subagentElapsed",
        &[("duration", format_usage_duration(elapsed_ms, locale))],
    );
    let transcript = projected_agent_blocks(&state.runtime.agent_blocks);
    let cancel_target = selected_id.to_string();
    let transcript_rows = if transcript.is_empty() {
        Vec::new()
    } else {
        (0..transcript.len())
            .map(|index| {
                agent_timeline_entry(
                    index,
                    &transcript,
                    (
                        palette,
                        locale,
                        reduced_motion,
                        CHAT_COLUMN_GUTTER_WIDE,
                        elapsed_ms,
                    ),
                    &state.runtime.agents,
                    expansion.clone(),
                    owner.clone(),
                )
            })
            .collect::<Vec<_>>()
    };
    let (open_agents, finished_agents): (Vec<_>, Vec<_>) =
        state.runtime.agents.iter().partition(|agent| {
            agent
                .get("state")
                .and_then(serde_json::Value::as_str)
                .is_some_and(is_open_agent_state)
        });
    let open_count = open_agents.len();
    let finished_count = finished_agents.len();
    let open_rows = open_agents
        .into_iter()
        .enumerate()
        .map(|(index, agent)| agent_roster_row(index, agent, palette, locale, cx))
        .collect::<Vec<_>>();
    let finished_rows = finished_agents
        .into_iter()
        .enumerate()
        .map(|(index, agent)| agent_roster_row(open_count + index, agent, palette, locale, cx))
        .collect::<Vec<_>>();

    div()
        .id("agent-side-panel")
        .role(Role::Region)
        .aria_label(locale.text("ui.subagentConversation"))
        .absolute()
        .top(px(0.))
        .right(px(-(panel_width - visible_width)))
        .bottom(px(0.))
        .w(px(panel_width))
        .opacity((visible_width / panel_width).clamp(0., 1.))
        .bg(palette.paper)
        .overflow_hidden()
        .flex()
        .flex_col()
        .child(side_panel_resize_handle(panel_width, palette, locale, cx))
        .when(!detail_open, |panel| {
            panel.child(
                div()
                    .id("agent-roster")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .px_5()
                    .py_5()
                    .child(
                        div()
                            .mb_3()
                            .text_xs()
                            .text_color(palette.faint)
                            .child(format!(
                                "{} · {}",
                                locale.text("ui.opened"),
                                open_count
                            )),
                    )
                    .when(open_count == 0, |roster| {
                        roster.child(
                            div()
                                .mb_6()
                                .text_sm()
                                .text_color(palette.faint)
                                .child(locale.text("ui.noOpenSubagents")),
                        )
                    })
                    .children(open_rows)
                    .child(
                        div()
                            .mt_5()
                            .mb_2()
                            .text_xs()
                            .text_color(palette.faint)
                            .child(format!(
                                "{} · {}",
                                locale.text("ui.finished"),
                                finished_count
                            )),
                    )
                    .children(finished_rows),
            )
        })
        .when(detail_open, |panel| {
            panel
                .child(
                    div()
                        .id("agent-detail-header")
                        .h(px(52.))
                        .px_3()
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .id("agent-detail-back")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.subagents"))
                                .tab_stop(true)
                                .size(px(28.))
                                .rounded(px(7.))
                                .flex()
                                .items_center()
                                .justify_center()
                                .cursor_pointer()
                                .hover(move |button| button.bg(palette.paper_muted))
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.state.runtime.selected_agent_id = "".into();
                                    this.state.runtime.agent_blocks.clear();
                                    cx.notify();
                                }))
                                .child(icon("arrow-left", 15., palette.muted)),
                        )
                        .child(icon("bot", 16., palette.muted))
                        .child(
                            div()
                                .min_w_0()
                                .flex_1()
                                .truncate()
                                .text_sm()
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .text_color(palette.ink)
                                .child(role),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(if active {
                                    palette.positive
                                } else {
                                    palette.faint
                                })
                                .child(subagent_status_label(state_name, locale)),
                        )
                        .when(active, |header| {
                            header.child(
                                div()
                                    .id("agent-cancel")
                                    .role(Role::Button)
                                    .aria_label(locale.text("ui.stopSubagent"))
                                    .tab_stop(true)
                                    .size(px(28.))
                                    .rounded(px(7.))
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .cursor_pointer()
                                    .hover(move |button| button.bg(palette.paper_muted))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime.request(
                                            Method::Execute,
                                            json!({
                                                "kind": "cancel_agent",
                                                "target": cancel_target,
                                                "sessionId": this.state.navigation.current_session_id,
                                            }),
                                        );
                                        cx.notify();
                                    }))
                                    .child(icon("square", 13., palette.muted)),
                            )
                        }),
                )
                .child(
                    div()
                        .id("agent-transcript")
                        .w_full()
                        .min_w_0()
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .pl(px(7.))
                        .py_2()
                        .child(
                            div()
                                .h(px(36.))
                                .mx_3()
                                .mb_2()
                                .border_b_1()
                                .border_color(palette.border)
                                .flex()
                                .items_center()
                                .text_xs()
                                .text_color(palette.faint)
                                .child(elapsed),
                        )
                        .when(transcript.is_empty(), |body| {
                            body.flex().items_center().justify_center().child(
                                div()
                                    .px_4()
                                    .text_sm()
                                    .text_color(palette.faint)
                                    .child(if active {
                                        locale.text("ui.waitingForSubagentOutput")
                                    } else {
                                        locale.text("ui.noTranscriptIsAvailableForThisSubagent")
                                    }),
                            )
                        })
                        .children(transcript_rows),
                )
        })
        .when(this.side_panel_add_menu_open, |panel| {
            panel.child(agent_panel_add_menu(palette, locale, cx))
        })
        .into_any_element()
}

fn agent_roster_row(
    index: usize,
    agent: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let field = |key| {
        agent
            .get(key)
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .trim()
    };
    let id = [field("id"), field("agentId")]
        .into_iter()
        .find(|value| !value.is_empty())
        .unwrap_or_default()
        .to_string();
    let label = [field("description"), field("type"), id.as_str()]
        .into_iter()
        .find(|value| !value.is_empty())
        .unwrap_or(locale.text("ui.subagent"))
        .to_string();
    let state = field("state");
    let elapsed_ms = agent
        .get("elapsedMs")
        .and_then(|value| {
            value
                .as_i64()
                .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
        })
        .unwrap_or_default();
    let aria_label = format!("{}, {}", label, subagent_status_label(state, locale));
    div()
        .id(("agent-roster-row", index))
        .role(Role::Button)
        .aria_label(aria_label)
        .tab_stop(true)
        .h(px(48.))
        .px_2()
        .rounded(px(8.))
        .flex()
        .items_center()
        .gap_3()
        .cursor_pointer()
        .hover(move |row| row.bg(palette.paper_muted))
        .on_click(cx.listener(move |this, _, window, cx| {
            this.inspect_agent(id.clone(), window, cx);
        }))
        .child(icon(
            "bot",
            18.,
            if is_open_agent_state(state) {
                palette.positive
            } else {
                palette.muted
            },
        ))
        .child(
            div()
                .min_w_0()
                .flex_1()
                .truncate()
                .text_sm()
                .text_color(palette.ink)
                .child(label),
        )
        .child(
            div()
                .text_xs()
                .text_color(palette.faint)
                .child(format_usage_duration(elapsed_ms, locale)),
        )
        .into_any_element()
}

pub(super) fn projected_agent_blocks(values: &[serde_json::Value]) -> Vec<Block> {
    values
        .iter()
        .enumerate()
        .map(|(index, value)| {
            let raw_kind = value
                .get("kind")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default();
            let kind = match raw_kind {
                "text_delta" => "assistant",
                "thinking_delta" => "thinking",
                "tool_started" | "tool_update" | "tool_finished" => "tool",
                "diff_ready" => "diff",
                other => other,
            };
            let data = value.get("data");
            let data_string = |key| {
                data.and_then(|data| data.get(key))
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
            };
            let mut extra = HashMap::new();
            if let Some(fields) = value.as_object() {
                extra.extend(
                    fields
                        .iter()
                        .map(|(key, value)| (key.clone(), value.clone())),
                );
            }
            Block {
                id: value
                    .get("id")
                    .or_else(|| value.get("toolCallId"))
                    .and_then(serde_json::Value::as_str)
                    .map(str::to_string)
                    .unwrap_or_else(|| format!("agent-block-{index}"))
                    .into(),
                kind: kind.to_string().into(),
                run_id: value
                    .get("runId")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string()
                    .into(),
                tool_call_id: value
                    .get("toolCallId")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string()
                    .into(),
                title: value
                    .get("title")
                    .and_then(serde_json::Value::as_str)
                    .filter(|title| !title.is_empty())
                    .unwrap_or_else(|| data_string("name"))
                    .to_string()
                    .into(),
                content: {
                    let content = value
                        .get("content")
                        .or_else(|| value.get("text"))
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default();
                    if kind == "thinking" {
                        crate::state::normalize_thinking_content(content)
                    } else {
                        content.to_string()
                    }
                },
                state: value
                    .get("state")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string()
                    .into(),
                extra,
                ..Default::default()
            }
        })
        .collect()
}

fn agent_timeline_entry(
    index: usize,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool, f32, i64),
    agents: &[serde_json::Value],
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let Some(block) = blocks.get(index) else {
        return div().h(px(0.)).into_any_element();
    };
    if is_thinking_text(block) || is_process_tool_block(block) {
        if is_thinking_text(block) && block.content.trim().is_empty() {
            return div().h(px(0.)).into_any_element();
        }
        let (palette, locale, reduced_motion, horizontal_gutter, _) = style;
        let body = if is_process_tool_block(block) {
            process_step_row(
                index,
                0,
                block,
                blocks,
                (palette, locale, reduced_motion),
                expansion,
                owner,
            )
        } else {
            process_detail_row(index, 0, block, palette, locale, reduced_motion)
        };
        return div()
            .id(("agent-timeline-block", index))
            .role(Role::Article)
            .aria_label(block.kind.to_string())
            .w_full()
            .min_w_0()
            .px(px(horizontal_gutter))
            .py(px(4.))
            .flex()
            .justify_center()
            .child(
                div()
                    .w_full()
                    .min_w_0()
                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                    .child(body),
            )
            .into_any_element();
    }
    timeline::timeline_entry_unfolded(index, blocks, style, agents, expansion, owner, None)
}

fn environment_nav_row(
    id: &'static str,
    icon_name: &'static str,
    label: String,
    trailing: String,
    destination: Option<Surface>,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label.clone())
        .tab_stop(destination.is_some())
        .h(px(38.))
        .px_2()
        .rounded(px(8.))
        .text_color(palette.ink)
        .text_xs()
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.paper_muted))
        .when_some(destination, |row, destination| {
            row.on_click(cx.listener(move |this, _, window, cx| {
                if destination == Surface::Terminal {
                    this.set_terminal_open(!this.terminal_open, window, cx);
                } else {
                    this.state.navigation.surface = destination;
                    this.request_surface(destination);
                }
                cx.notify();
            }))
        })
        .child(
            div()
                .w(px(18.))
                .child(icon(icon_name, 16., palette.ink_soft)),
        )
        .child(div().flex_1().overflow_hidden().child(label))
        .when(!trailing.is_empty(), |row| {
            row.child(div().text_color(palette.muted).text_xs().child(trailing))
        })
        .into_any_element()
}

fn environment_divider(palette: ThemePalette) -> gpui::Div {
    div().h(px(1.)).mx_2().my_2().bg(palette.border)
}

fn environment_label(label: &'static str, palette: ThemePalette) -> gpui::Div {
    div()
        .h(px(26.))
        .px_2()
        .text_color(palette.faint)
        .text_xs()
        .flex()
        .items_end()
        .child(label)
}

pub(super) fn recap_copy(recap: &serde_json::Value) -> (String, String, String) {
    let value = |key| {
        recap
            .get(key)
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_string()
    };
    (value("summary"), value("goal"), value("openItems"))
}

fn recap_panel(
    recap: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let (summary, goal, open_items) = recap_copy(recap);
    let empty = [&summary, &goal, &open_items]
        .into_iter()
        .all(|copy| copy.is_empty());
    div()
        .id("environment-recap-detail")
        .mx_2()
        .mb_2()
        .pb_1()
        .max_h(px(180.))
        .overflow_y_scroll()
        .text_xs()
        .line_height(px(19.))
        .text_color(palette.muted)
        .when(empty, |panel| {
            panel
                .py_1()
                .text_color(palette.faint)
                .child(locale.text("ui.noRecapHasBeenGeneratedForThisConversationYet"))
        })
        .when(!empty, |panel| {
            panel
                .when(!summary.is_empty(), |panel| {
                    panel.child(div().pb_2().whitespace_normal().child(summary))
                })
                .when(!goal.is_empty(), |panel| {
                    panel.child(recap_section(locale.text("ui.currentGoal"), goal, palette))
                })
                .when(!open_items.is_empty(), |panel| {
                    panel.child(recap_section(
                        locale.text("ui.openItems"),
                        open_items,
                        palette,
                    ))
                })
        })
        .into_any_element()
}

fn recap_section(label: &'static str, content: String, palette: ThemePalette) -> gpui::Div {
    div()
        .pt_2()
        .pb_1()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .text_size(px(11.))
                .text_color(palette.faint)
                .child(label),
        )
        .child(div().whitespace_normal().child(content))
}
