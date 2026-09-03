use super::*;
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
    let repository_label = state
        .pull_requests
        .dashboard
        .pointer("/repository/nameWithOwner")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("GitHub")
        .to_string();
    let dashboard_status = if state.pull_requests.loading {
        locale.text("common.refreshing").to_string()
    } else {
        state.pull_requests.error.to_string()
    };
    let current = state
        .pull_requests
        .dashboard
        .get("current")
        .filter(|value| !value.is_null())
        .cloned()
        .into_iter()
        .collect::<Vec<_>>();
    let created = state
        .pull_requests
        .dashboard
        .get("createdByViewer")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let open = state
        .pull_requests
        .dashboard
        .get("open")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let dashboard = div()
        .id("pull-request-dashboard")
        .size_full()
        .overflow_y_scroll()
        .bg(palette.paper_muted)
        .px(px(28.))
        .py(px(24.))
        .flex()
        .flex_col()
        .gap_5()
        .child(
            div()
                .pb_4()
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
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
                                .child("GITHUB"),
                        )
                        .child(
                            div()
                                .text_size(px(21.))
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("pr.heading")),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.muted)
                                .child(repository_label),
                        )
                        .when(!dashboard_status.is_empty(), |header| {
                            header.child(
                                div()
                                    .text_xs()
                                    .text_color(if state.pull_requests.error.is_empty() {
                                        palette.faint
                                    } else {
                                        palette.danger
                                    })
                                    .child(dashboard_status),
                            )
                        }),
                )
                .child(
                    div()
                        .id("refresh-pull-requests")
                        .role(Role::Button)
                        .aria_label(locale.text("pr.refresh"))
                        .tab_stop(true)
                        .h(px(34.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .on_click(cx.listener(|this, _, _, _| {
                            this.request_surface(Surface::PullRequests);
                        }))
                        .child(icon("refresh-cw", 14., palette.muted))
                        .child(locale.text("common.refresh")),
                ),
        )
        .child(pull_request_group(
            0,
            locale.text("pr.current"),
            current,
            palette,
            cx,
            locale,
        ))
        .child(pull_request_group(
            1,
            locale.text("pr.created"),
            created,
            palette,
            cx,
            locale,
        ))
        .child(pull_request_group(
            2,
            locale.text("pr.open"),
            open,
            palette,
            cx,
            locale,
        ));
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
                            .text_sm()
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
                            .size(px(28.))
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

fn pull_request_group(
    group_id: usize,
    title: &'static str,
    pull_requests: Vec<serde_json::Value>,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
    locale: Locale,
) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_2()
        .child(
            div()
                .text_size(px(16.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
        .when(pull_requests.is_empty(), |group| {
            group.child(
                div()
                    .h(px(56.))
                    .rounded(px(12.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .text_sm()
                    .text_color(palette.faint)
                    .flex()
                    .items_center()
                    .justify_center()
                    .child("—"),
            )
        })
        .children(
            pull_requests
                .into_iter()
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
                        .id(("pull-request", group_id * 1000 + index))
                        .role(Role::Button)
                        .aria_label(locale.format(
                            "pr.row",
                            &[("number", number.to_string()), ("title", title.clone())],
                        ))
                        .tab_stop(true)
                        .min_h(px(64.))
                        .px_4()
                        .rounded(px(12.))
                        .border_1()
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
                        .child(div().text_color(palette.positive).child("○"))
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
                                        .child(title),
                                )
                                .child(div().truncate().text_sm().text_color(palette.muted).child(
                                    locale.format(
                                        "pr.openRow",
                                        &[("number", number.to_string()), ("author", author)],
                                    ),
                                )),
                        )
                        .child(
                            div()
                                .font_family("SF Mono")
                                .text_sm()
                                .text_color(palette.muted)
                                .child(format!("⌘ {head} → {base}")),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.positive)
                                .child(format!("+{additions}")),
                        )
                        .child(
                            div()
                                .text_sm()
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
            .text_sm()
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
                        .text_size(px(22.))
                        .line_height(px(28.))
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
        .h(px(32.))
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
        .h(px(34.))
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
