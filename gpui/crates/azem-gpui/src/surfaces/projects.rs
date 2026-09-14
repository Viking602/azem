use super::*;

pub(crate) fn projects_surface(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    scroll: &UniformListScrollHandle,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let project_name = std::path::Path::new(state.workspace.root.as_ref())
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("workspace")
        .to_string();
    let files = state
        .workspace
        .changes
        .get("files")
        .and_then(serde_json::Value::as_array);
    let sessions = state
        .navigation
        .sessions
        .iter()
        .filter(|s| !s.archived && s.workspace == state.workspace.root)
        .take(6)
        .collect::<Vec<_>>();
    let current_pr = state
        .pull_requests
        .dashboard
        .get("current")
        .filter(|v| !v.is_null());
    let count = files.map_or(state.workspace.changed_files as usize, Vec::len);
    let additions = state
        .workspace
        .changes
        .get("additions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or(state.workspace.additions as u64);
    let deletions = state
        .workspace
        .changes
        .get("deletions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or(state.workspace.deletions as u64);
    div()
        .id("projects-surface")
        .role(Role::Region)
        .aria_label(labels.workspace)
        .flex_1()
        .min_h_0()
        .bg(palette.paper)
        .overflow_hidden()
        .flex()
        .flex_col()
        .child(
            div().w_full().flex_1().min_h_0().flex().justify_center().px_5().py_3().child(
                div()
                    .w_full()
                    .h_full()
                    .min_h_0()
                    .min_w_0()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_4()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(px(WORKSPACE_PAGE_TITLE))
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(project_name),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(WORKSPACE_TEXT))
                                            .text_color(palette.faint)
                                            .truncate()
                                            .child(state.workspace.root.to_string()),
                                    ),
                            )
                            .child(
                                div()
                                    .id("workspace-new-session")
                                    .role(Role::Button)
                                    .aria_label(labels.new_conversation)
                                    .tab_stop(true)
                                    .h(px(WORKSPACE_CONTROL))
                                    .px_3()
                                    .rounded(px(WORKSPACE_RADIUS))
                                    .bg(palette.button)
                                    .text_color(palette.button_text)
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .text_size(px(WORKSPACE_TEXT))
                                    .cursor_pointer()
                                    .hover(|s| s.opacity(0.85))
                                    .on_click(cx.listener(AzemWindow::new_session))
                                    .child(icon("plus", 16., palette.button_text))
                                    .child(labels.new_conversation),
                            ),
                    )
                    .child(
                        div()
                            .flex()
                            .flex_wrap()
                            .items_center()
                            .gap_2()
                            .pb_2()
                            .border_b_1()
                            .border_color(palette.border)
                            .child(icon("git-branch", 14., palette.muted))
                            .child(
                                div()
                                    .min_w_0()
                                    .max_w(px(420.))
                                    .truncate()
                                    .text_size(px(WORKSPACE_META))
                                    .text_color(palette.muted)
                                    .child(if state.workspace.branch.is_empty() {
                                        locale.text("workspace.localProject").to_string()
                                    } else {
                                        state.workspace.branch.to_string()
                                    }),
                            )
                            .child(div().flex_1())
                            .child(workspace_link(
                                "workspace-files",
                                labels.files,
                                "folder",
                                Surface::Files,
                                palette,
                                cx,
                            ))
                            .child(workspace_link(
                                "workspace-changes",
                                labels.changes,
                                "file-diff",
                                Surface::Changes,
                                palette,
                                cx,
                            ))
                            .child(workspace_link(
                                "workspace-pr",
                                labels.pull_requests,
                                "git-pull-request",
                                Surface::PullRequests,
                                palette,
                                cx,
                            ))
                            .child(
                                div()
                                    .id("workspace-open-terminal")
                                    .role(Role::Button)
                                    .aria_label(labels.terminal)
                                    .tab_stop(true)
                                    .h(px(WORKSPACE_CONTROL))
                                    .px_2()
                                    .rounded(px(WORKSPACE_RADIUS))
                                    .flex()
                                    .items_center()
                                    .gap_1()
                                    .text_size(px(WORKSPACE_META))
                                    .cursor_pointer()
                                    .hover(|s| s.bg(palette.hover))
                                    .on_click(cx.listener(AzemWindow::toggle_terminal_click))
                                    .child(icon("terminal", 14., palette.muted))
                                    .child(labels.terminal),
                            ),
                    )
                    .child(
                        div()
                            .flex()
                            .flex_1()
                            .min_h_0()
                            .items_start()
                            .gap_5()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .h_full()
                                    .min_h_0()
                                    .flex()
                                    .flex_col()
                                    .child(
                                        div()
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .pb_2()
                                            .border_b_1()
                                            .border_color(palette.border)
                                            .child(
                                                div()
                                                    .flex_1()
                                                    .text_size(px(WORKSPACE_TITLE))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(locale.text("workspace.changes")),
                                            )
                                            .child(
                                                div()
                                                    .text_size(px(WORKSPACE_META))
                                                    .text_color(palette.muted)
                                                    .child(count.to_string()),
                                            )
                                            .child(
                                                div()
                                                    .font_family("Menlo")
                                                    .text_size(px(WORKSPACE_TEXT))
                                                    .text_color(palette.positive)
                                                    .child(format!("+{additions}")),
                                            )
                                            .child(
                                                div()
                                                    .font_family("Menlo")
                                                    .text_size(px(WORKSPACE_TEXT))
                                                    .text_color(palette.danger)
                                                    .child(format!("−{deletions}")),
                                            ),
                                    )
                                    .child(div().relative().flex_1().min_h_0().pr_3().child(
                                        uniform_list("workspace-overview-files", files.map_or(0, Vec::len), cx.processor(move |this, range: std::ops::Range<usize>, _, cx| {
                                            let files = this.state.workspace.changes.get("files").and_then(serde_json::Value::as_array);
                                            range.filter_map(|index| {
                                                let file = files?.get(index)?;
                                                Some(super::workspace::workspace_change_row(index, file, false, palette, cx))
                                            }).collect()
                                        })).w_full().h_full().track_scroll(scroll)
                                    ).child(super::workspace::workspace_scrollbar("workspace-overview-scrollbar", &scroll.0.borrow().base_handle, palette, cx)))
                                    .when(files.is_none_or(Vec::is_empty), |s| {
                                        s.child(
                                            div().py_8().text_size(px(WORKSPACE_TEXT)).text_color(palette.muted).child(
                                                if files.is_none() {
                                                    locale.text("workspace.loadingChanges")
                                                } else {
                                                    locale.text("workspace.clean")
                                                },
                                            ),
                                        )
                                    })
                                    .child(div().pt_2().flex().items_start().child(
                                        workspace_link(
                                            "workspace-all-changes",
                                            locale.text("workspace.allChanges"),
                                            "chevron-right",
                                            Surface::Changes,
                                            palette,
                                            cx,
                                        ),
                                    )),
                            )
                            .child(
                                div()
                                    .w(px(300.))
                                    .h_full()
                                    .id("workspace-overview-activity")
                                    .overflow_y_scroll()
                                    .flex_shrink_0()
                                    .flex()
                                    .flex_col()
                                    .gap_4()
                                    .child(
                                        div()
                                            .flex()
                                            .flex_col()
                                            .child(
                                                div()
                                                    .pb_2()
                                                    .text_size(px(WORKSPACE_TITLE))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(locale.text("workspace.activity")),
                                            )
                                            .when(sessions.is_empty(), |s| {
                                                s.child(
                                                    div()
                                                        .py_4()
                                                        .text_size(px(WORKSPACE_TEXT))
                                                        .text_color(palette.muted)
                                                        .child(locale.text("workspace.noActivity")),
                                                )
                                            })
                                            .children(sessions.into_iter().enumerate().map(
                                                |(index, session)| {
                                                    let id = session.id.to_string();
                                                    let title = session.title.to_string();
                                                    div()
                                                        .id(("workspace-session", index))
                                                        .role(Role::Button)
                                                        .aria_label(title.clone())
                                                        .tab_stop(true)
                                                        .h(px(WORKSPACE_ROW))
                                                        .px_2()
                                                        .border_b_1()
                                                        .border_color(palette.border)
                                                        .flex()
                                                        .items_center()
                                                        .gap_3()
                                                        .cursor_pointer()
                                                        .hover(|s| s.bg(palette.hover))
                                                        .child(icon(
                                                            "message-square-text",
                                                            16.,
                                                            if session.running {
                                                                palette.accent
                                                            } else {
                                                                palette.muted
                                                            },
                                                        ))
                                                        .child(
                                                            div()
                                                                .flex_1()
                                                                .min_w_0()
                                                                .truncate()
                                                                .text_size(px(WORKSPACE_TEXT))
                                                                .child(title.clone()),
                                                        )
                                                        .when(session.running, |row| {
                                                            row.child(
                                                                div()
                                                                    .text_size(px(WORKSPACE_META))
                                                                    .text_color(palette.accent)
                                                                    .child(
                                                                        locale
                                                                            .text("common.running"),
                                                                    ),
                                                            )
                                                        })
                                                        .child(icon(
                                                            "chevron-right",
                                                            14.,
                                                            palette.faint,
                                                        ))
                                                        .on_click(cx.listener(
                                                            move |this, _, _, cx| {
                                                                this.state
                                                                    .navigation
                                                                    .current_session_id =
                                                                    id.clone().into();
                                                                this.state
                                                                    .navigation
                                                                    .current_title =
                                                                    title.clone().into();
                                                                this.state.navigation.surface =
                                                                    Surface::Thread;
                                                                let request = this.runtime.request(
                                                                    Method::SelectSession,
                                                                    json!({"sessionId":id}),
                                                                );
                                                                this.pending_requests.insert(
                                                                    request,
                                                                    PendingRequest::ResumeSession {
                                                                        sequence: None,
                                                                    },
                                                                );
                                                                cx.notify();
                                                            },
                                                        ))
                                                },
                                            )),
                                    )
                                    .child(
                                        div()
                                            .pt_3()
                                            .border_t_1()
                                            .border_color(palette.border)
                                            .flex()
                                            .flex_col()
                                            .items_start()
                                            .gap_2()
                                            .child(
                                                div()
                                                    .flex()
                                                    .items_center()
                                                    .gap_2()
                                                    .child(icon(
                                                        "git-pull-request",
                                                        16.,
                                                        palette.muted,
                                                    ))
                                                    .child(
                                                        div()
                                                            .text_size(px(WORKSPACE_TEXT))
                                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                                            .child(labels.pull_requests),
                                                    ),
                                            )
                                            .child(
                                                div().text_size(px(WORKSPACE_TEXT)).text_color(palette.muted).child(
                                                    current_pr
                                                        .map(|pr| {
                                                            format!(
                                                                "#{}  {}",
                                                                pr.get("number")
                                                                    .and_then(
                                                                        serde_json::Value::as_u64
                                                                    )
                                                                    .unwrap_or_default(),
                                                                pr.get("title")
                                                                    .and_then(
                                                                        serde_json::Value::as_str
                                                                    )
                                                                    .unwrap_or_default()
                                                            )
                                                        })
                                                        .unwrap_or_else(|| {
                                                            locale
                                                                .text("workspace.noPr")
                                                                .to_string()
                                                        }),
                                                ),
                                            )
                                            .child(workspace_link(
                                                "workspace-pr-details",
                                                locale.text("workspace.viewPr"),
                                                "chevron-right",
                                                Surface::PullRequests,
                                                palette,
                                                cx,
                                            )),
                                    ),
                            ),
                    ),
            ),
        )
        .into_any_element()
}

fn workspace_link(
    id: &'static str,
    label: &'static str,
    glyph: &'static str,
    destination: Surface,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label)
        .tab_stop(true)
        .h(px(WORKSPACE_CONTROL))
        .px_2()
        .rounded(px(WORKSPACE_RADIUS))
        .flex()
        .items_center()
        .gap_1()
        .text_size(px(WORKSPACE_META))
        .cursor_pointer()
        .hover(|s| s.bg(palette.hover))
        .on_click(cx.listener(move |this, _, _, cx| {
            this.state.navigation.surface = destination;
            this.request_surface(destination);
            cx.notify();
        }))
        .child(icon(glyph, 14., palette.muted))
        .child(label)
        .into_any_element()
}
