use super::*;
pub(crate) fn projects_surface(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let project_name = std::path::Path::new(state.workspace.root.as_ref())
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("workspace")
        .to_string();
    let changed_files = state
        .workspace
        .changes
        .get("files")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let current_pr = state
        .pull_requests
        .dashboard
        .get("current")
        .filter(|value| !value.is_null())
        .cloned();
    let sessions = state
        .navigation
        .sessions
        .iter()
        .filter(|session| {
            !session.archived
                && (session.workspace == state.workspace.root || session.workspace.is_empty())
        })
        .take(5)
        .cloned()
        .collect::<Vec<_>>();
    let tabs = [
        (locale.text("ui.overview"), Surface::Projects),
        (labels.files, Surface::Files),
        (labels.changes, Surface::Changes),
        (labels.pull_requests, Surface::PullRequests),
    ];
    div()
        .id("projects-surface")
        .role(Role::Region)
        .aria_label(labels.workspace)
        .flex_1()
        .overflow_hidden()
        .bg(palette.paper)
        .flex()
        .flex_col()
        .child(
            div()
                .min_h(px(130.))
                .px(px(30.))
                .py(px(22.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_end()
                .child(
                    div()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .font_family("SF Mono")
                                .text_size(px(9.))
                                .text_color(palette.faint)
                                .child(locale.text("workspace.eyebrow")),
                        )
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(
                                    div()
                                        .text_size(px(26.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(project_name),
                                )
                                .child(div().text_color(palette.positive).child("●"))
                                .child(
                                    div()
                                        .font_family("SF Mono")
                                        .text_sm()
                                        .text_color(palette.muted)
                                        .child(state.workspace.branch.to_string()),
                                ),
                        )
                        .child(
                            div()
                                .text_color(palette.faint)
                                .text_sm()
                                .child(state.workspace.root.to_string()),
                        ),
                )
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(
                            div()
                                .id("workspace-open-terminal")
                                .role(Role::Button)
                                .aria_label(labels.terminal)
                                .tab_stop(true)
                                .h(px(34.))
                                .px_3()
                                .rounded(px(8.))
                                .border_1()
                                .border_color(palette.border)
                                .text_sm()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(AzemWindow::toggle_terminal_click))
                                .child(locale.text("ui.openTerminal")),
                        )
                        .child(
                            div()
                                .id("workspace-new-session")
                                .role(Role::Button)
                                .aria_label(labels.new_conversation)
                                .tab_stop(true)
                                .h(px(34.))
                                .px_3()
                                .rounded(px(8.))
                                .bg(palette.button)
                                .text_color(palette.button_text)
                                .text_sm()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(AzemWindow::new_session))
                                .child(locale.text("ui.newConversationHere")),
                        ),
                ),
        )
        .child(
            div()
                .h(px(42.))
                .px(px(30.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_end()
                .gap_5()
                .children(
                    tabs.into_iter()
                        .enumerate()
                        .map(|(index, (label, destination))| {
                            div()
                                .id(("workspace-tab", index))
                                .role(Role::Tab)
                                .aria_label(label)
                                .aria_selected(index == 0)
                                .tab_stop(true)
                                .h_full()
                                .px_1()
                                .border_b(px(if index == 0 { 2. } else { 0. }))
                                .border_color(palette.accent)
                                .text_sm()
                                .text_color(if index == 0 {
                                    palette.ink
                                } else {
                                    palette.muted
                                })
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.state.navigation.surface = destination;
                                    this.request_surface(destination);
                                    cx.notify();
                                }))
                                .child(label)
                        }),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .id("workspace-overview-changes")
                        .flex_1()
                        .min_w_0()
                        .overflow_y_scroll()
                        .px(px(30.))
                        .py(px(22.))
                        .flex()
                        .flex_col()
                        .gap_2()
                        .child(
                            div()
                                .pb_2()
                                .flex()
                                .items_end()
                                .child(
                                    div()
                                        .flex_1()
                                        .flex()
                                        .flex_col()
                                        .gap_1()
                                        .child(
                                            div()
                                                .text_size(px(17.))
                                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                                .child(locale.text("workspace.changes")),
                                        )
                                        .child(div().text_sm().text_color(palette.muted).child(
                                            locale.format(
                                                "workspace.changeSummary",
                                                &[
                                                    (
                                                        "count",
                                                        state.workspace.changed_files.to_string(),
                                                    ),
                                                    (
                                                        "added",
                                                        state.workspace.additions.to_string(),
                                                    ),
                                                    (
                                                        "deleted",
                                                        state.workspace.deletions.to_string(),
                                                    ),
                                                ],
                                            ),
                                        )),
                                )
                                .child(
                                    div()
                                        .text_sm()
                                        .text_color(palette.muted)
                                        .child(locale.text("workspace.allChanges")),
                                ),
                        )
                        .children(changed_files.into_iter().enumerate().map(|(index, file)| {
                            let path = file
                                .get("path")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string();
                            let name = std::path::Path::new(&path)
                                .file_name()
                                .and_then(|name| name.to_str())
                                .unwrap_or(&path)
                                .to_string();
                            let parent = std::path::Path::new(&path)
                                .parent()
                                .and_then(|path| path.to_str())
                                .unwrap_or_default()
                                .to_string();
                            let additions = file
                                .get("additions")
                                .and_then(serde_json::Value::as_i64)
                                .unwrap_or_default();
                            let deletions = file
                                .get("deletions")
                                .and_then(serde_json::Value::as_i64)
                                .unwrap_or_default();
                            div()
                                .id(("workspace-change", index))
                                .min_h(px(66.))
                                .px_1()
                                .border_b_1()
                                .border_color(palette.border)
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(div().w(px(3.)).h(px(38.)).bg(palette.accent))
                                .child(
                                    div()
                                        .flex_1()
                                        .flex()
                                        .flex_col()
                                        .gap_1()
                                        .child(
                                            div()
                                                .text_sm()
                                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                                .child(name),
                                        )
                                        .child(
                                            div().text_xs().text_color(palette.faint).child(parent),
                                        ),
                                )
                                .child(
                                    div()
                                        .text_sm()
                                        .text_color(palette.muted)
                                        .child(format!("+{additions} −{deletions}")),
                                )
                        }))
                        .when(state.workspace.changed_files == 0, |list| {
                            list.child(
                                div()
                                    .h(px(90.))
                                    .text_sm()
                                    .text_color(palette.faint)
                                    .flex()
                                    .items_center()
                                    .child(locale.text("workspace.clean")),
                            )
                        }),
                )
                .child(
                    div()
                        .w(px(370.))
                        .h_full()
                        .border_l_1()
                        .border_color(palette.border)
                        .px(px(24.))
                        .py(px(22.))
                        .flex()
                        .flex_col()
                        .gap_4()
                        .child(workspace_pr_summary(current_pr, palette, locale))
                        .child(
                            div()
                                .pt_3()
                                .border_t_1()
                                .border_color(palette.border)
                                .flex()
                                .flex_col()
                                .gap_2()
                                .child(
                                    div()
                                        .text_size(px(16.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(locale.text("workspace.activity")),
                                )
                                .children(sessions.into_iter().enumerate().map(
                                    |(index, session)| {
                                        div()
                                            .id(("workspace-session", index))
                                            .min_h(px(52.))
                                            .border_b_1()
                                            .border_color(palette.border)
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .child(
                                                div()
                                                    .text_color(if session.running {
                                                        rgb(0x1f7af0)
                                                    } else {
                                                        palette.faint
                                                    })
                                                    .child("●"),
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
                                                            .child(session.title.to_string()),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_xs()
                                                            .text_color(palette.faint)
                                                            .child(if session.running {
                                                                locale.text("common.running")
                                                            } else {
                                                                locale.text("common.completed")
                                                            }),
                                                    ),
                                            )
                                    },
                                )),
                        ),
                ),
        )
        .into_any_element()
}

fn workspace_pr_summary(
    pull_request: Option<serde_json::Value>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let missing = pull_request.is_none();
    div()
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .text_size(px(16.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(locale.text("pr.heading")),
        )
        .when_some(pull_request, |summary, pull_request| {
            let number = pull_request
                .get("number")
                .and_then(serde_json::Value::as_i64)
                .unwrap_or_default();
            let title = pull_request
                .get("title")
                .and_then(serde_json::Value::as_str)
                .unwrap_or(locale.text("pr.single"))
                .to_string();
            summary.child(
                div()
                    .py_3()
                    .border_t_1()
                    .border_b_1()
                    .border_color(palette.border)
                    .flex()
                    .flex_col()
                    .gap_2()
                    .child(
                        div()
                            .text_sm()
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .child(format!("#{number}  {title}")),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(palette.positive)
                            .child(locale.text("workspace.checksPassed")),
                    ),
            )
        })
        .when(missing, |summary| {
            summary.child(
                div()
                    .text_sm()
                    .text_color(palette.faint)
                    .child(locale.text("workspace.noPr")),
            )
        })
        .into_any_element()
}
