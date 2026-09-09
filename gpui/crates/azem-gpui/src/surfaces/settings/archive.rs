use super::*;
pub(super) fn settings_archive_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    archive: (u32, bool, &HashSet<String>),
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (archive_days, days_menu_open, open_projects) = archive;
    let session_id = state.navigation.current_session_id.to_string();
    let archive_session_id = session_id.clone();
    let connected = state.connection.connected;
    let groups = archived_session_groups(&state.navigation.sessions, locale);
    let archived_count = groups
        .iter()
        .map(|(_, sessions)| sessions.len())
        .sum::<usize>();
    let project_rows = groups
        .into_iter()
        .enumerate()
        .map(|(group_index, (workspace, sessions))| {
            archive_project_row(
                workspace,
                sessions,
                group_index,
                session_id.clone(),
                open_projects,
                connected,
                palette,
                locale,
                cx,
            )
        })
        .collect::<Vec<_>>();
    let list = div()
        .min_h(px(158.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(
            locale.text("ui.archivedSessions"),
            locale.text("ui.groupedByProjectRestoredSessionsReturnToTheirSidebar"),
            palette,
        ))
        .when(archived_count == 0, |card| {
            card.child(
                div()
                    .h(px(94.))
                    .text_color(palette.faint)
                    .text_sm()
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(locale.text("ui.noArchivedSessions")),
            )
        })
        .children(project_rows);
    let days_label = locale.format(
        "ui.inactiveForArchiveDaysDays",
        &[("archive_days", (archive_days).to_string())],
    );
    let day_options = archive_day_options(archive_days, palette, locale, cx);
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .p_4()
                .flex()
                .flex_col()
                .gap_4()
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("ui.archiveInactiveSessions")),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.muted)
                                .child(locale.text("ui.moveOldUnpinnedSessionsOutOfTheSidebarTheCurrent")),
                        ),
                )
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .relative()
                                .child(
                                    div()
                                        .id("archive-days-select")
                                        .role(Role::Button)
                                        .aria_label(locale.text("ui.archiveAge"))
                                        .aria_expanded(days_menu_open)
                                        .tab_stop(true)
                                        .w(px(174.))
                                        .h(px(30.))
                                        .px_2()
                                        .rounded(px(8.))
                                        .border_1()
                                        .border_color(palette.border)
                                        .text_sm()
                                        .flex()
                                        .items_center()
                                        .cursor_pointer()
                                        .on_click(cx.listener(|this, _, _, cx| {
                                            this.archive_days_menu_open =
                                                !this.archive_days_menu_open;
                                            this.model_picker_open = false;
                                            this.route_picker_target = None;
                                            cx.notify();
                                        }))
                                        .child(days_label)
                                        .child(div().flex_1())
                                        .child(icon(
                                            if days_menu_open {
                                                "chevron-up"
                                            } else {
                                                "chevron-down"
                                            },
                                            12.,
                                            palette.faint,
                                        )),
                                )
                                .when(days_menu_open, |select| {
                                    select.child(
                                        deferred(
                                            div()
                                                .id("archive-days-menu")
                                                .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                                                .role(Role::RadioGroup)
                                                .aria_label(locale.text("ui.archiveAge"))
                                                .absolute()
                                                .top(px(36.))
                                                .left_0()
                                                .w(px(174.))
                                                .p(px(5.))
                                                .rounded(px(10.))
                                                .border_1()
                                                .border_color(palette.border_strong)
                                                .bg(palette.paper)
                                                .shadow(vec![
                                                    BoxShadow::new(
                                                        px(0.),
                                                        px(8.),
                                                        hsla(220. / 360., 0.15, 0.15, 0.14),
                                                    )
                                                    .blur_radius(px(22.)),
                                                ])
                                                .children(day_options),
                                        )
                                        .with_priority(10),
                                    )
                                }),
                        )
                        .child(
                            div()
                                .id("archive-inactive-action")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.archiveInactiveSessions"))
                                .tab_stop(connected)
                                .h(px(29.))
                                .px_2()
                                .rounded(px(8.))
                                .bg(palette.paper_muted)
                                .text_color(if connected {
                                    palette.ink_soft
                                } else {
                                    palette.faint
                                })
                                .text_sm()
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if connected {
                                        this.runtime.request(
                                            Method::Execute,
                                            json!({"kind": "archive_inactive_sessions", "target": archive_days.to_string(), "sessionId": archive_session_id}),
                                        );
                                        cx.notify();
                                    }
                                }))
                                .flex()
                                .items_center()
                                .child(locale.text("ui.archiveInactiveSessions")),
                        ),
                ),
        )
        .child(list)
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn archive_project_row(
    workspace: String,
    sessions: Vec<SessionSummary>,
    group_index: usize,
    current_session_id: String,
    open_projects: &HashSet<String>,
    connected: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let key = archive_project_key(&workspace);
    let expanded = open_projects.contains(&key);
    let project_name = archive_project_name(&workspace, locale);
    let count = sessions.len();
    let session_rows = if expanded {
        archive_session_rows(
            sessions,
            group_index,
            current_session_id,
            connected,
            palette,
            locale,
            cx,
        )
    } else {
        Vec::new()
    };
    div()
        .id(("archive-project", group_index))
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .flex_col()
        .child(
            div()
                .id(("archive-project-toggle", group_index))
                .role(Role::Button)
                .aria_label(project_name.clone())
                .aria_expanded(expanded)
                .tab_stop(true)
                .min_h(px(56.))
                .px_4()
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
                .on_click(cx.listener(move |this, _, _, cx| {
                    if this.open_projects.contains(&key) {
                        this.open_projects.remove(&key);
                    } else {
                        this.open_projects.insert(key.clone());
                    }
                    cx.notify();
                }))
                .child(icon(
                    if expanded {
                        "chevron-down"
                    } else {
                        "chevron-right"
                    },
                    13.,
                    palette.faint,
                ))
                .child(
                    div()
                        .size(px(28.))
                        .rounded(px(8.))
                        .bg(palette.paper_muted)
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(icon("folder", 14., palette.muted)),
                )
                .child(
                    div()
                        .min_w_0()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .truncate()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(project_name),
                        )
                        .when(!workspace.is_empty(), |copy| {
                            copy.child(
                                div()
                                    .truncate()
                                    .text_xs()
                                    .text_color(palette.faint)
                                    .child(workspace),
                            )
                        }),
                )
                .child(
                    div().text_xs().text_color(palette.faint).child(
                        locale.format("ui.countSessions", &[("count", (count).to_string())]),
                    ),
                ),
        )
        .children(session_rows)
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn archive_session_rows(
    sessions: Vec<SessionSummary>,
    group_index: usize,
    current_session_id: String,
    connected: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> Vec<gpui::AnyElement> {
    sessions
        .into_iter()
        .enumerate()
        .map(|(session_index, session)| {
            let id = session.id.to_string();
            let session_id = current_session_id.clone();
            div()
                .id(("archived-session", group_index * 10_000 + session_index))
                .min_h(px(48.))
                .ml(px(52.))
                .mr_3()
                .px_2()
                .rounded(px(8.))
                .flex()
                .items_center()
                .gap_2()
                .hover(move |style| style.bg(palette.hover))
                .child(
                    div()
                        .min_w_0()
                        .flex_1()
                        .truncate()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(session.title.to_string()),
                )
                .child(
                    div()
                        .id(("restore-session", group_index * 10_000 + session_index))
                        .role(Role::Button)
                        .aria_label(locale.text("ui.restore"))
                        .tab_stop(connected)
                        .h(px(28.))
                        .px_2()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_xs()
                        .cursor_pointer()
                        .on_click(cx.listener(move |this, _, _, _| {
                            this.runtime.request(
                                Method::Execute,
                                json!({"kind": "archive_session", "target": id, "decision": "false", "sessionId": session_id}),
                            );
                        }))
                        .flex()
                        .items_center()
                        .child(locale.text("ui.restore")),
                )
                .into_any_element()
        })
        .collect()
}

fn archive_day_options(
    archive_days: u32,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> Vec<gpui::AnyElement> {
    [7_u32, 14, 30, 90]
        .into_iter()
        .enumerate()
        .map(|(index, days)| {
            let selected = days == archive_days;
            let label = locale.format("ui.inactiveForDaysDays", &[("days", (days).to_string())]);
            div()
                .id(("archive-days-option", index))
                .role(Role::RadioButton)
                .aria_label(label.clone())
                .aria_selected(selected)
                .tab_stop(true)
                .h(px(32.))
                .px_2()
                .rounded(px(7.))
                .bg(if selected {
                    palette.paper_muted
                } else {
                    palette.paper
                })
                .text_sm()
                .flex()
                .items_center()
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.archive_days = days;
                    this.archive_days_menu_open = false;
                    cx.notify();
                }))
                .child(label)
                .child(div().flex_1())
                .when(selected, |option| {
                    option.child(icon("check", 13., palette.ink))
                })
                .into_any_element()
        })
        .collect()
}

fn archive_project_key(workspace: &str) -> String {
    format!(
        "archive:{}",
        if workspace.is_empty() {
            "unassigned"
        } else {
            workspace
        }
    )
}

fn archive_project_name(workspace: &str, locale: Locale) -> String {
    std::path::Path::new(workspace)
        .file_name()
        .and_then(|name| name.to_str())
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| locale.text("ui.unassigned"))
        .to_string()
}

pub(in crate::surfaces) fn archived_session_groups(
    sessions: &[SessionSummary],
    locale: Locale,
) -> Vec<(String, Vec<SessionSummary>)> {
    let mut groups = HashMap::<String, Vec<SessionSummary>>::new();
    for session in sessions.iter().filter(|session| session.archived) {
        groups
            .entry(session.workspace.to_string())
            .or_default()
            .push(session.clone());
    }
    let mut groups = groups.into_iter().collect::<Vec<_>>();
    for (_, sessions) in &mut groups {
        sessions.sort_by(|left, right| {
            right
                .updated_at
                .as_str()
                .unwrap_or_default()
                .cmp(left.updated_at.as_str().unwrap_or_default())
        });
    }
    groups.sort_by(|(left, _), (right, _)| {
        archive_project_name(left, locale).cmp(&archive_project_name(right, locale))
    });
    groups
}
