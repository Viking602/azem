use super::*;
use crate::state::PullRequestTab;
pub(crate) fn pull_requests_surface(
    state: &AppState,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let selected = &state.pull_requests.selected;
    let detail = selected.get("pullRequest").unwrap_or(selected);
    let number = detail
        .get("number")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let repository = state
        .pull_requests
        .dashboard
        .pointer("/repository/nameWithOwner")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let head_oid = detail
        .get("headRefOid")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let model = &state.pull_requests;
    let rows = model.tab.rows(&model.dashboard);
    let repository_label = if repository.is_empty() {
        "GitHub"
    } else {
        &repository
    };
    let dashboard = div()
        .id("pull-request-dashboard")
        .size_full()
        .min_h_0()
        .bg(palette.paper)
        .flex()
        .flex_col()
        .child(
            workspace::workspace_header(locale.text("pr.heading"), palette, locale, cx)
                .child(
                    div()
                        .min_w_0()
                        .flex_1()
                        .flex()
                        .justify_end()
                        .items_center()
                        .gap_2()
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.muted)
                        .child(icon("folder", WORKSPACE_ICON, palette.muted))
                        .child(div().truncate().child(repository_label.to_string())),
                )
                .child(
                    div()
                        .id("refresh-pull-requests")
                        .role(Role::Button)
                        .aria_label(locale.text("pr.refresh"))
                        .tab_stop(true)
                        .h(px(WORKSPACE_CONTROL))
                        .flex_shrink_0()
                        .px_2()
                        .rounded(px(WORKSPACE_RADIUS))
                        .border_1()
                        .border_color(palette.border)
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.muted)
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(|s| s.bg(palette.hover))
                        .focus_visible(|s| s.border_color(palette.accent))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.request_surface(Surface::PullRequests);
                            cx.notify();
                        }))
                        .child(icon("rotate-ccw", WORKSPACE_ICON, palette.muted))
                        .child(locale.text(if model.loading {
                            "common.refreshing"
                        } else {
                            "common.refresh"
                        })),
                ),
        )
        .child(
            div()
                .id("pull-request-tabs")
                .role(Role::TabList)
                .aria_label(locale.text("pr.title"))
                .h(px(WORKSPACE_TOOLBAR))
                .flex_shrink_0()
                .px(px(WORKSPACE_INSET))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_2()
                .children(
                    [
                        (PullRequestTab::Open, "pr.open"),
                        (PullRequestTab::Current, "pr.current"),
                        (PullRequestTab::Created, "pr.created"),
                    ]
                    .into_iter()
                    .enumerate()
                    .map(|(index, (tab, key))| {
                        let active = model.tab == tab;
                        div()
                            .id(("pull-request-tab", index))
                            .role(Role::Tab)
                            .aria_label(locale.text(key))
                            .aria_selected(active)
                            .tab_stop(true)
                            .h(px(WORKSPACE_CONTROL))
                            .px_3()
                            .rounded_full()
                            .border_1()
                            .border_color(rgba(0))
                            .bg(if active { palette.accent_soft } else { rgba(0) })
                            .text_size(px(WORKSPACE_TEXT))
                            .text_color(if active {
                                palette.accent
                            } else {
                                palette.muted
                            })
                            .font_weight(if active {
                                gpui::FontWeight::SEMIBOLD
                            } else {
                                gpui::FontWeight::NORMAL
                            })
                            .flex()
                            .items_center()
                            .gap_2()
                            .cursor_pointer()
                            .hover(move |s| {
                                s.bg(if active {
                                    palette.accent_soft
                                } else {
                                    palette.hover
                                })
                            })
                            .focus_visible(|s| s.border_color(palette.accent))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.state.pull_requests.tab = tab;
                                this.state.pull_requests.selected = serde_json::Value::Null;
                                cx.notify();
                            }))
                            .child(locale.text(key))
                            .child(
                                div()
                                    .min_w(px(18.))
                                    .h(px(18.))
                                    .px_1()
                                    .rounded_full()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .bg(if active {
                                        palette.paper
                                    } else {
                                        palette.paper_muted
                                    })
                                    .font_family("SF Mono")
                                    .text_size(px(WORKSPACE_META))
                                    .text_color(if active {
                                        palette.accent
                                    } else {
                                        palette.muted
                                    })
                                    .child(tab.rows(&model.dashboard).len().to_string()),
                            )
                    }),
                ),
        )
        .when(!model.error.is_empty(), |dashboard| {
            dashboard.child(
                div()
                    .px(px(WORKSPACE_INSET))
                    .py_2()
                    .text_size(px(WORKSPACE_TEXT))
                    .text_color(palette.danger)
                    .child(model.error.to_string()),
            )
        })
        .child(
            div()
                .id(("pull-request-list", model.tab as usize))
                .role(Role::TabPanel)
                .aria_label(locale.text(match model.tab {
                    PullRequestTab::Open => "pr.open",
                    PullRequestTab::Current => "pr.current",
                    PullRequestTab::Created => "pr.created",
                }))
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .px(px(WORKSPACE_INSET))
                .when(rows.is_empty() && model.error.is_empty(), |list| {
                    list.child(
                        div()
                            .h(px(200.))
                            .flex()
                            .flex_col()
                            .items_center()
                            .justify_center()
                            .gap_3()
                            .child(
                                div()
                                    .size(px(44.))
                                    .rounded(px(12.))
                                    .bg(palette.paper_muted)
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .child(icon("git-pull-request", 24., palette.muted)),
                            )
                            .child(
                                div()
                                    .text_size(px(WORKSPACE_TEXT))
                                    .text_color(palette.muted)
                                    .child(locale.text(if model.loading {
                                        "common.refreshing"
                                    } else {
                                        "pr.empty"
                                    })),
                            ),
                    )
                })
                .child(pull_request_list(rows, palette, cx, locale)),
        );
    let drawer = (number > 0).then(|| {
        let draft = selected
            .pointer("/pullRequest/draft")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let state_name = selected
            .pointer("/pullRequest/state")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        div()
            .id("pull-request-detail")
            .role(Role::Document)
            .aria_label(locale.text("pr.detail"))
            .absolute()
            .right_0()
            .top_0()
            .bottom_0()
            .w(px(648.))
            .border_l_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(-10.), px(0.), hsla(220. / 360., 0.12, 0.18, 0.08))
                    .blur_radius(px(24.)),
            ])
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(46.))
                    .px_4()
                    .border_b_1()
                    .border_color(palette.border)
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(icon("git-pull-request", 15., palette.muted))
                    .child(
                        div()
                            .text_size(px(WORKSPACE_TEXT))
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .child(format!("PR #{number}")),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .id("close-pr-detail")
                            .role(Role::Button)
                            .aria_label(locale.text("pr.closeDetail"))
                            .tab_stop(true)
                            .size(px(WORKSPACE_CONTROL))
                            .rounded(px(7.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.state.pull_requests.selected = serde_json::Value::Null;
                                cx.notify();
                            }))
                            .child("×"),
                    ),
            )
            .child(
                div()
                    .id("pull-request-detail-scroll")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(pull_request_detail_content(detail, palette, locale))
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(pr_action_button(
                                0,
                                if draft {
                                    locale.text("pr.markReady")
                                } else {
                                    locale.text("pr.draft")
                                },
                                palette,
                                cx,
                                Method::MutatePullRequest,
                                json!({
                                    "number": number,
                                    "kind": if draft { "ready" } else { "draft" },
                                    "expectedHeadOid": head_oid,
                                    "expectedRepository": repository,
                                }),
                            ))
                            .child(pr_action_button(
                                1,
                                if state_name == "OPEN" {
                                    locale.text("pr.close")
                                } else {
                                    locale.text("pr.reopen")
                                },
                                palette,
                                cx,
                                Method::MutatePullRequest,
                                json!({
                                    "number": number,
                                    "kind": if state_name == "OPEN" { "close" } else { "reopen" },
                                    "expectedHeadOid": head_oid,
                                    "expectedRepository": repository,
                                }),
                            ))
                            .child(pr_action_button(
                                2,
                                locale.text("pr.monitor"),
                                palette,
                                cx,
                                Method::SetPullRequestMonitor,
                                json!({"number": number, "enabled": true}),
                            )),
                    ),
            )
            .into_any_element()
    });
    div()
        .id("pull-requests")
        .role(Role::Region)
        .aria_label(locale.text("pr.title"))
        .relative()
        .flex_1()
        .overflow_hidden()
        .child(dashboard)
        .when_some(drawer, |surface, drawer| surface.child(drawer))
        .into_any_element()
}

fn pull_request_list(
    pull_requests: &[serde_json::Value],
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
    locale: Locale,
) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .children(
            pull_requests
                .iter()
                .enumerate()
                .map(|(index, pull_request)| {
                    let number = pull_request
                        .get("number")
                        .and_then(serde_json::Value::as_i64)
                        .unwrap_or_default();
                    let title = pull_request
                        .get("title")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or(locale.text("pr.untitled"))
                        .to_string();
                    let author = pull_request
                        .pointer("/author/login")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .to_string();
                    let head = pull_request
                        .get("headRefName")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or("main")
                        .to_string();
                    let base = pull_request
                        .get("baseRefName")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or("main")
                        .to_string();
                    let additions = pull_request
                        .get("additions")
                        .and_then(serde_json::Value::as_i64)
                        .unwrap_or_default();
                    let deletions = pull_request
                        .get("deletions")
                        .and_then(serde_json::Value::as_i64)
                        .unwrap_or_default();
                    div()
                        .id(("pull-request", index))
                        .role(Role::Button)
                        .aria_label(locale.format(
                            "pr.row",
                            &[("number", number.to_string()), ("title", title.clone())],
                        ))
                        .tab_stop(true)
                        .min_h(px(WORKSPACE_ROW + 16.))
                        .px_2()
                        .border_b_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .flex()
                        .items_center()
                        .gap_3()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            let id = this
                                .runtime
                                .request(Method::PullRequestDetail, json!({"number": number}));
                            this.pending_requests
                                .insert(id, PendingRequest::PullRequestDetail);
                            cx.notify();
                        }))
                        .child(icon("git-pull-request", WORKSPACE_ICON, palette.positive))
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
                                        .text_size(px(WORKSPACE_TEXT))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(title),
                                )
                                .child(
                                    div()
                                        .truncate()
                                        .text_size(px(WORKSPACE_TEXT))
                                        .text_color(palette.muted)
                                        .child(locale.format(
                                            "pr.openRow",
                                            &[("number", number.to_string()), ("author", author)],
                                        )),
                                ),
                        )
                        .child(
                            div()
                                .font_family("SF Mono")
                                .text_size(px(WORKSPACE_TEXT))
                                .text_color(palette.muted)
                                .max_w(px(240.))
                                .truncate()
                                .child(format!("{head} → {base}")),
                        )
                        .child(
                            div()
                                .text_size(px(WORKSPACE_TEXT))
                                .text_color(palette.positive)
                                .child(format!("+{additions}")),
                        )
                        .child(
                            div()
                                .text_size(px(WORKSPACE_TEXT))
                                .text_color(palette.danger)
                                .child(format!("−{deletions}")),
                        )
                }),
        )
        .into_any_element()
}

fn pull_request_detail_content(
    detail: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    if detail.is_null() || detail.get("number").is_none() {
        return div()
            .text_color(palette.faint)
            .text_size(px(WORKSPACE_TEXT))
            .child(locale.text("pr.select"))
            .into_any_element();
    }
    let number = detail
        .get("number")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let title = detail
        .get("title")
        .and_then(serde_json::Value::as_str)
        .unwrap_or(locale.text("pr.single"))
        .to_string();
    let state = detail
        .get("state")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let author = detail
        .pointer("/author/login")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let head = detail
        .get("headRefName")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let base = detail
        .get("baseRefName")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let body = detail
        .get("body")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let additions = detail
        .get("additions")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let deletions = detail
        .get("deletions")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let changed_files = detail
        .get("changedFiles")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let checks = detail
        .get("checksDetail")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let files = detail
        .get("files")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    div()
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
                        .text_size(px(WORKSPACE_PAGE_TITLE))
                        .line_height(px(WORKSPACE_CONTROL))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .text_color(palette.ink)
                        .child(title),
                )
                .child(
                    div()
                        .text_size(px(11.))
                        .text_color(palette.muted)
                        .child(format!(
                            "#{number} · {} · {author} · {head} → {base}",
                            locale.value("status", &state)
                        )),
                ),
        )
        .child(
            div()
                .flex()
                .gap_3()
                .text_size(px(11.))
                .child(
                    div()
                        .text_color(palette.positive)
                        .child(format!("+{additions}")),
                )
                .child(
                    div()
                        .text_color(palette.danger)
                        .child(format!("−{deletions}")),
                )
                .child(
                    div().text_color(palette.muted).child(
                        locale.format("pr.fileCount", &[("count", changed_files.to_string())]),
                    ),
                ),
        )
        .when_some(
            (!body.is_empty()).then(|| pull_request_body(body, palette)),
            |content, body| content.child(body),
        )
        .when_some(
            (!checks.is_empty()).then(|| pull_request_checks(checks, palette, locale)),
            |content, checks| content.child(checks),
        )
        .when_some(
            (!files.is_empty()).then(|| pull_request_files(files, palette, locale)),
            |content, files| content.child(files),
        )
        .into_any_element()
}

fn pull_request_body(body: String, palette: ThemePalette) -> gpui::AnyElement {
    div()
        .max_w(px(760.))
        .text_size(px(12.))
        .line_height(px(19.))
        .text_color(palette.ink_soft)
        .whitespace_normal()
        .child(body)
        .into_any_element()
}

fn pull_request_checks(
    checks: Vec<serde_json::Value>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .text_size(px(12.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(locale.text("pr.checks")),
        )
        .children(
            checks
                .into_iter()
                .map(|check| pull_request_check_row(check, palette, locale)),
        )
        .into_any_element()
}

fn pull_request_check_row(
    check: serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::Div {
    let name = check
        .get("name")
        .and_then(serde_json::Value::as_str)
        .unwrap_or(locale.text("pr.check"))
        .to_string();
    let category = check
        .get("category")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let failing = category == "failing";
    div()
        .h(px(WORKSPACE_ROW))
        .border_b_1()
        .border_color(palette.border)
        .text_size(px(11.))
        .flex()
        .items_center()
        .gap_2()
        .child(icon(
            pick(failing, "square", "check"),
            13.,
            pick(failing, palette.danger, palette.positive),
        ))
        .child(name)
        .child(div().flex_1())
        .child(
            div()
                .text_color(palette.muted)
                .child(locale.value("status", &category).to_string()),
        )
}

fn pull_request_files(
    files: Vec<serde_json::Value>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .text_size(px(12.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(locale.text("pr.files")),
        )
        .children(
            files
                .into_iter()
                .map(|file| pull_request_file_row(file, palette)),
        )
        .into_any_element()
}

fn pull_request_file_row(file: serde_json::Value, palette: ThemePalette) -> gpui::Div {
    let path = file
        .get("path")
        .and_then(serde_json::Value::as_str)
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
        .h(px(WORKSPACE_CONTROL))
        .border_b_1()
        .border_color(palette.border)
        .font_family("SF Mono")
        .text_size(px(10.))
        .flex()
        .items_center()
        .child(path)
        .child(div().flex_1())
        .child(
            div()
                .text_color(palette.positive)
                .child(format!("+{additions}")),
        )
        .child(
            div()
                .ml_2()
                .text_color(palette.danger)
                .child(format!("−{deletions}")),
        )
}
fn pr_action_button(
    id: usize,
    label: &'static str,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
    method: Method,
    payload: serde_json::Value,
) -> gpui::AnyElement {
    div()
        .id(("pull-request-action", id))
        .role(Role::Button)
        .aria_label(label)
        .tab_stop(true)
        .px_3()
        .py_2()
        .rounded_md()
        .border_1()
        .border_color(palette.border)
        .on_click(cx.listener(move |this, _, _, cx| {
            let request_id = this.runtime.request(method, payload.clone());
            this.pending_requests.insert(
                request_id,
                if method == Method::SetPullRequestMonitor {
                    PendingRequest::PullRequestMonitor
                } else {
                    PendingRequest::PullRequestDetail
                },
            );
            cx.notify();
        }))
        .child(label)
        .into_any_element()
}
