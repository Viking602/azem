use super::*;
pub(super) fn settings_subagents_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    open_menu: Option<SubagentSettingKind>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let connected = state.connection.connected;
    let rows = vec![
        settings_control_detail_row(
            locale.text("ui.subagentConcurrency"),
            locale.text("ui.maximumConcurrentSubagentsZeroIsUnlimited"),
            settings_subagent_stepper(
                SubagentSettingKind::Concurrency,
                state.settings.subagent_concurrency,
                0,
                64,
                subagent_setting_label(
                    SubagentSettingKind::Concurrency,
                    state.settings.subagent_concurrency,
                    locale,
                ),
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
        settings_control_detail_row(
            locale.text("ui.recursionDepth"),
            locale.text("ui.howDeeplySubagentsMayDelegate"),
            settings_subagent_select(
                SubagentSettingKind::Depth,
                state.settings.subagent_max_depth,
                subagent_setting_label(
                    SubagentSettingKind::Depth,
                    state.settings.subagent_max_depth,
                    locale,
                ),
                open_menu,
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
        settings_control_detail_row(
            locale.text("ui.shellConcurrency"),
            locale.text("ui.independentCapacityForLocalCommands"),
            settings_subagent_stepper(
                SubagentSettingKind::ShellConcurrency,
                state.settings.shell_concurrency,
                1,
                16,
                subagent_setting_label(
                    SubagentSettingKind::ShellConcurrency,
                    state.settings.shell_concurrency,
                    locale,
                ),
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
        settings_control_detail_row(
            locale.text("ui.shellWallClock"),
            locale.text("ui.maximumDurationForOneCodingShellCommand"),
            settings_subagent_select(
                SubagentSettingKind::ShellWallClock,
                state.settings.shell_max_wall_clock_seconds,
                subagent_setting_label(
                    SubagentSettingKind::ShellWallClock,
                    state.settings.shell_max_wall_clock_seconds,
                    locale,
                ),
                open_menu,
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
        settings_control_detail_row(
            locale.text("ui.foregroundWait"),
            locale.text("ui.waitForForegroundCompletionByDefault"),
            settings_subagent_select(
                SubagentSettingKind::AwaitTimeout,
                state.settings.subagent_await_seconds,
                subagent_setting_label(
                    SubagentSettingKind::AwaitTimeout,
                    state.settings.subagent_await_seconds,
                    locale,
                ),
                open_menu,
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
        settings_control_detail_row(
            locale.text("ui.idleCancellation"),
            locale.text("ui.cancelOnlySilentSubagentsWithoutVisibleActivity"),
            settings_subagent_select(
                SubagentSettingKind::IdleTimeout,
                state.settings.subagent_idle_seconds,
                subagent_setting_label(
                    SubagentSettingKind::IdleTimeout,
                    state.settings.subagent_idle_seconds,
                    locale,
                ),
                open_menu,
                connected,
                palette,
                locale,
                cx,
            ),
            palette,
        ),
    ];
    div()
        .w_full()
        .flex()
        .justify_center()
        .child(
            div()
                .w_full()
                .max_w(px(760.))
                .flex()
                .flex_col()
                .gap_4()
                .child(settings_unclipped_detail_card(
                    locale.text("ui.capacityAndIsolation"),
                    locale.text("ui.subagentsDelegationAndShellCommandsHaveSeparateLimits"),
                    rows,
                    palette,
                ))
                .child(settings_detail_card(
                    locale.text("ui.scheduling"),
                    locale.text("ui.mainAndSubagentsAlwaysDispatchToolsInParallel"),
                    vec![settings_detail_row(
                        locale.text("ui.dispatchPolicy"),
                        locale.text("ui.parallelDispatchIsAProductInvariant"),
                        locale.text("ui.parallelReadOnly").to_string(),
                        palette,
                    )],
                    palette,
                )),
        )
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn settings_subagent_stepper(
    kind: SubagentSettingKind,
    value: i64,
    min: i64,
    max: i64,
    display_value: String,
    connected: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let decrease_enabled = connected && value > min;
    let increase_enabled = connected && value < max;
    let button = |id: String,
                  label: String,
                  symbol: &'static str,
                  enabled: bool,
                  next: i64,
                  cx: &mut Context<AzemWindow>| {
        div()
            .id(id)
            .role(Role::Button)
            .aria_label(label)
            .tab_stop(enabled)
            .w(px(32.))
            .h_full()
            .text_color(if enabled {
                palette.ink_soft
            } else {
                palette.faint
            })
            .text_sm()
            .flex()
            .items_center()
            .justify_center()
            .when(enabled, |button| {
                button
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
            })
            .on_click(cx.listener(move |this, _, _, cx| {
                if enabled {
                    this.set_subagent_setting(kind, next, cx);
                }
            }))
            .child(symbol)
    };
    div()
        .w(px(112.))
        .h(px(34.))
        .rounded(px(8.))
        .border_1()
        .border_color(palette.border_strong)
        .bg(palette.paper)
        .overflow_hidden()
        .flex()
        .items_center()
        .child(button(
            format!("subagent-{}-decrease", kind.id()),
            locale.text("ui.decreaseValue").to_string(),
            "−",
            decrease_enabled,
            (value - 1).clamp(min, max),
            cx,
        ))
        .child(
            div()
                .flex_1()
                .h_full()
                .border_l_1()
                .border_r_1()
                .border_color(palette.border)
                .text_color(palette.ink_soft)
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .flex()
                .items_center()
                .justify_center()
                .child(display_value),
        )
        .child(button(
            format!("subagent-{}-increase", kind.id()),
            locale.text("ui.increaseValue").to_string(),
            "+",
            increase_enabled,
            (value + 1).clamp(min, max),
            cx,
        ))
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn settings_subagent_select(
    kind: SubagentSettingKind,
    value: i64,
    label: String,
    open_menu: Option<SubagentSettingKind>,
    connected: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let open = open_menu == Some(kind);
    let options = kind
        .menu_values()
        .iter()
        .copied()
        .enumerate()
        .map(|(index, option)| {
            let selected = option == value;
            let option_label = subagent_setting_label(kind, option, locale);
            div()
                .id(format!("subagent-{}-option-{index}", kind.id()))
                .role(Role::RadioButton)
                .aria_label(option_label.clone())
                .aria_selected(selected)
                .tab_stop(connected)
                .h(px(32.))
                .px_2()
                .rounded(px(7.))
                .bg(if selected {
                    palette.paper_muted
                } else {
                    palette.paper
                })
                .text_color(palette.ink_soft)
                .text_sm()
                .flex()
                .items_center()
                .when(connected, |option| {
                    option
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                })
                .on_click(cx.listener(move |this, _, _, cx| {
                    if connected {
                        this.set_subagent_setting(kind, option, cx);
                    }
                }))
                .child(option_label)
                .child(div().flex_1())
                .when(selected, |option| {
                    option.child(icon("check", 13., palette.ink))
                })
                .into_any_element()
        })
        .collect::<Vec<_>>();
    div()
        .relative()
        .w(px(128.))
        .child(
            div()
                .id(format!("subagent-{}-select", kind.id()))
                .role(Role::Button)
                .aria_label(label.clone())
                .aria_expanded(open)
                .tab_stop(connected)
                .w_full()
                .h(px(34.))
                .px_3()
                .rounded(px(8.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .text_color(if connected {
                    palette.ink_soft
                } else {
                    palette.faint
                })
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .flex()
                .items_center()
                .gap_2()
                .when(connected, |select| {
                    select
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                })
                .on_click(cx.listener(move |this, _, _, cx| {
                    if connected {
                        this.subagent_setting_menu = if this.subagent_setting_menu == Some(kind) {
                            None
                        } else {
                            Some(kind)
                        };
                        this.model_picker_open = false;
                        this.route_picker_target = None;
                        this.archive_days_menu_open = false;
                        cx.notify();
                    }
                }))
                .child(div().min_w_0().flex_1().truncate().child(label))
                .child(icon(
                    if open { "chevron-up" } else { "chevron-down" },
                    12.,
                    palette.faint,
                )),
        )
        .when(open, |select| {
            select.child(
                deferred(
                    div()
                        .id(format!("subagent-{}-menu", kind.id()))
                        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                        .role(Role::RadioGroup)
                        .absolute()
                        .top(px(40.))
                        .right_0()
                        .w(px(160.))
                        .p(px(5.))
                        .rounded(px(10.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .bg(palette.paper)
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.15, 0.14))
                                .blur_radius(px(22.)),
                        ])
                        .children(options),
                )
                .with_priority(20),
            )
        })
        .into_any_element()
}

fn subagent_setting_label(kind: SubagentSettingKind, value: i64, locale: Locale) -> String {
    match kind {
        SubagentSettingKind::Concurrency | SubagentSettingKind::ShellConcurrency if value == 0 => {
            locale.text("ui.unlimited").to_string()
        }
        SubagentSettingKind::Concurrency
        | SubagentSettingKind::Depth
        | SubagentSettingKind::ShellConcurrency
            if value > 0 =>
        {
            value.to_string()
        }
        SubagentSettingKind::Depth if value == -1 => locale.text("ui.unlimited").to_string(),
        SubagentSettingKind::Depth => locale.text("ui.off").to_string(),
        SubagentSettingKind::AwaitTimeout => duration_label(value, locale, true),
        SubagentSettingKind::ShellWallClock | SubagentSettingKind::IdleTimeout => {
            duration_label(value, locale, false)
        }
        _ => value.to_string(),
    }
}

fn duration_label(seconds: i64, locale: Locale, unlimited: bool) -> String {
    if seconds == 0 {
        return if unlimited {
            locale.text("ui.untilComplete")
        } else {
            locale.text("ui.off")
        }
        .to_string();
    }
    if seconds % 60 == 0 {
        return format!("{} {}", seconds / 60, locale.text("ui.min"));
    }
    format!("{} {}", seconds, locale.text("ui.sec"))
}
