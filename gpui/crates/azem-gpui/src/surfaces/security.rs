use super::*;
pub(super) fn security_progress_fraction(progress: &serde_json::Value, status: &str) -> f32 {
    let files_completed = progress
        .get("filesCompleted")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let files_total = progress
        .get("filesTotal")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    if files_total > 0 {
        return (files_completed as f32 / files_total as f32).clamp(0., 1.);
    }
    let workers_done = progress
        .get("workersDone")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let workers_planned = progress
        .get("workersPlanned")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    if workers_planned > 0 {
        return (workers_done as f32 / workers_planned as f32).clamp(0., 1.);
    }
    if status == "complete" { 1. } else { 0. }
}

fn security_timestamp(value: &serde_json::Value, key: &str) -> Option<String> {
    let value = value.get(key)?;
    let local = if let Some(milliseconds) = value
        .as_i64()
        .or_else(|| value.as_str().and_then(|value| value.parse::<i64>().ok()))
    {
        Local.timestamp_millis_opt(milliseconds).single()?
    } else {
        DateTime::parse_from_rfc3339(value.as_str()?)
            .ok()?
            .with_timezone(&Local)
    };
    Some(local.format("%m-%d %H:%M").to_string())
}

pub(super) fn security_compact_text(value: &str, limit: usize) -> String {
    let mut chars = value.trim().chars();
    let text = chars.by_ref().take(limit).collect::<String>();
    if chars.next().is_some() {
        format!("{text}…")
    } else {
        text
    }
}

fn security_status_color(status: &str, palette: ThemePalette) -> Rgba {
    match status {
        "complete" | "completed" | "succeeded" => palette.positive,
        "failed" | "error" => palette.danger,
        "blocked" => palette.warning,
        "queued" | "running" => palette.accent,
        _ => palette.faint,
    }
}

fn security_status_badge(status: &str, label: String, palette: ThemePalette) -> gpui::Div {
    let color = security_status_color(status, palette);
    div()
        .h(px(24.))
        .px_2()
        .rounded_full()
        .bg(palette.paper_muted)
        .text_xs()
        .text_color(color)
        .flex()
        .items_center()
        .gap_1()
        .child(div().size(px(6.)).rounded_full().bg(color))
        .child(label)
}

fn security_progress_card(
    progress: &serde_json::Value,
    status: &str,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let files_completed = progress
        .get("filesCompleted")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let files_total = progress
        .get("filesTotal")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let workers_running = progress
        .get("workersRunning")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let workers_done = progress
        .get("workersDone")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let workers_planned = progress
        .get("workersPlanned")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let phase = progress
        .get("phase")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let message = progress
        .get("message")
        .and_then(serde_json::Value::as_str)
        .map(|message| security_compact_text(message, 420))
        .unwrap_or_default();
    let updated = security_timestamp(progress, "updatedAt").unwrap_or_else(|| "—".into());
    let fraction = security_progress_fraction(progress, status);
    let percent = (fraction * 100.).round() as u32;
    let metrics = [
        (
            locale.text("security.phase"),
            locale.value("phase", phase).to_string(),
        ),
        (
            locale.text("security.filesReviewed"),
            format!("{files_completed} / {files_total}"),
        ),
        (
            locale.text("security.workers"),
            format!(
                "{workers_done} / {workers_planned} · {workers_running} {}",
                locale.text("common.running")
            ),
        ),
        (locale.text("security.updated"), updated),
    ];
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(
            div()
                .px_4()
                .py_3()
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("security.progress")),
                        )
                        .child(
                            div()
                                .mt_1()
                                .text_xs()
                                .text_color(palette.muted)
                                .child(locale.text("security.progressHint")),
                        ),
                )
                .child(
                    div()
                        .font_family("SF Mono")
                        .text_size(px(18.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(format!("{percent}%")),
                ),
        )
        .child(
            div()
                .px_4()
                .pb_4()
                .child(
                    div()
                        .w_full()
                        .h(px(6.))
                        .rounded_full()
                        .overflow_hidden()
                        .bg(palette.paper_muted)
                        .child(
                            div()
                                .h_full()
                                .w(relative(fraction))
                                .rounded_full()
                                .bg(security_status_color(status, palette)),
                        ),
                )
                .child(
                    div()
                        .mt_3()
                        .flex()
                        .flex_wrap()
                        .gap_2()
                        .children(metrics.into_iter().map(|(label, value)| {
                            div()
                                .min_w(px(180.))
                                .flex_1()
                                .rounded(px(8.))
                                .bg(palette.paper_muted)
                                .px_3()
                                .py_2()
                                .child(div().text_xs().text_color(palette.muted).child(label))
                                .child(
                                    div()
                                        .mt_1()
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::MEDIUM)
                                        .child(value),
                                )
                        })),
                )
                .when(!message.is_empty(), |card| {
                    card.child(
                        div()
                            .mt_3()
                            .rounded(px(8.))
                            .bg(palette.accent_soft)
                            .px_3()
                            .py_2()
                            .text_sm()
                            .line_height(px(20.))
                            .text_color(palette.ink_soft)
                            .child(message),
                    )
                }),
        )
        .into_any_element()
}

fn security_workers_card(
    workers: &[serde_json::Value],
    scan_status: &str,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let rows = workers
        .iter()
        .enumerate()
        .map(|(index, worker)| {
            let kind = worker
                .get("kind")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("unknown");
            let status = worker
                .get("status")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("unknown");
            let sequence = worker
                .get("sequence")
                .and_then(serde_json::Value::as_u64)
                .unwrap_or((index + 1) as u64);
            let error = worker
                .get("error")
                .and_then(serde_json::Value::as_str)
                .map(|error| security_compact_text(error, 260))
                .unwrap_or_default();
            div()
                .min_h(px(48.))
                .px_4()
                .py_3()
                .border_t_1()
                .border_color(palette.border)
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .flex_1()
                                .text_sm()
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .child(format!(
                                    "{} {sequence}",
                                    locale.value("securityWorker", kind)
                                )),
                        )
                        .child(security_status_badge(
                            status,
                            locale.value("status", status).to_string(),
                            palette,
                        )),
                )
                .when(!error.is_empty(), |row| {
                    row.child(
                        div()
                            .mt_2()
                            .text_xs()
                            .line_height(px(18.))
                            .text_color(palette.danger)
                            .child(error),
                    )
                })
        })
        .collect::<Vec<_>>();
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(
            locale.text("security.workerActivity"),
            locale.text("security.workerActivityHint"),
            palette,
        ))
        .when(rows.is_empty(), |card| {
            card.child(
                div()
                    .min_h(px(52.))
                    .px_4()
                    .border_t_1()
                    .border_color(palette.border)
                    .text_sm()
                    .text_color(palette.muted)
                    .flex()
                    .items_center()
                    .child(locale.text(if matches!(scan_status, "queued" | "running") {
                        "security.waitingWorkers"
                    } else {
                        "security.noWorkers"
                    })),
            )
        })
        .children(rows)
        .into_any_element()
}

fn security_findings_card(
    findings: &[serde_json::Value],
    scan_status: &str,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let rows = findings
        .iter()
        .map(|finding| {
            let title = finding
                .get("title")
                .and_then(serde_json::Value::as_str)
                .unwrap_or(locale.text("security.untitledFinding"))
                .to_string();
            let severity = finding
                .pointer("/severity/level")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("unknown");
            let summary = finding
                .get("summary")
                .and_then(serde_json::Value::as_str)
                .map(|value| security_compact_text(value, 320))
                .unwrap_or_default();
            let location = finding
                .get("locations")
                .and_then(serde_json::Value::as_array)
                .and_then(|locations| locations.first())
                .map(|location| {
                    let path = location
                        .get("path")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default();
                    let line = location
                        .get("startLine")
                        .and_then(serde_json::Value::as_u64)
                        .unwrap_or_default();
                    if line > 0 {
                        format!("{path}:{line}")
                    } else {
                        path.to_string()
                    }
                })
                .unwrap_or_default();
            let severity_color = match severity {
                "critical" | "high" => palette.danger,
                "medium" => palette.warning,
                "low" => palette.accent,
                _ => palette.faint,
            };
            div()
                .px_4()
                .py_3()
                .border_t_1()
                .border_color(palette.border)
                .child(
                    div()
                        .flex()
                        .items_start()
                        .gap_3()
                        .child(
                            div()
                                .flex_1()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(title),
                        )
                        .child(
                            div()
                                .h(px(24.))
                                .px_2()
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .text_xs()
                                .text_color(severity_color)
                                .flex()
                                .items_center()
                                .child(locale.value("severity", severity).to_string()),
                        ),
                )
                .when(!location.is_empty(), |row| {
                    row.child(
                        div()
                            .mt_1()
                            .font_family("SF Mono")
                            .text_xs()
                            .text_color(palette.muted)
                            .child(location),
                    )
                })
                .when(!summary.is_empty(), |row| {
                    row.child(
                        div()
                            .mt_2()
                            .text_sm()
                            .line_height(px(20.))
                            .text_color(palette.ink_soft)
                            .child(summary),
                    )
                })
        })
        .collect::<Vec<_>>();
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(
            locale.text("security.results"),
            locale.text("security.resultsHint"),
            palette,
        ))
        .when(rows.is_empty(), |card| {
            card.child(
                div()
                    .min_h(px(52.))
                    .px_4()
                    .border_t_1()
                    .border_color(palette.border)
                    .text_sm()
                    .text_color(palette.muted)
                    .flex()
                    .items_center()
                    .child(locale.text(if matches!(scan_status, "queued" | "running") {
                        "security.noFindingsYet"
                    } else {
                        "security.noFindings"
                    })),
            )
        })
        .children(rows)
        .into_any_element()
}

pub(crate) fn security_surface(
    state: &AppState,
    runtime: &RuntimeConnection,
    palette: ThemePalette,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let session_id = state.navigation.current_session_id.to_string();
    let workspace = state.workspace.root.to_string();
    let route = json!({
        "provider": state.settings.provider,
        "model": state.settings.model,
        "reasoning": state.settings.reasoning,
    });
    let selected_scan = state
        .security
        .projection
        .get("scan")
        .unwrap_or(&state.security.projection);
    let selected_id = selected_scan
        .get("id")
        .or_else(|| selected_scan.get("scanId"))
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let selected_status = selected_scan
        .get("status")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let empty = state.security.scans.is_empty();
    let selected_number = state
        .security
        .scans
        .iter()
        .position(|scan| {
            scan.get("id")
                .or_else(|| scan.get("scanId"))
                .and_then(serde_json::Value::as_str)
                == Some(selected_id.as_str())
        })
        .map(|index| index + 1);
    let selected_title = selected_scan
        .get("title")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .or_else(|| {
            selected_number.map(|number| {
                locale.format("security.scanNumber", &[("number", number.to_string())])
            })
        })
        .unwrap_or_else(|| locale.text("security.title").to_string());
    let actions = div()
        .flex()
        .gap_2()
        .child(security_action_button(
            0,
            locale.text("security.standard"),
            runtime.clone(),
            json!({
                "kind": "start_security_scan",
                "sessionId": session_id,
                "payload": {
                    "projectId": workspace,
                    "repository": workspace,
                    "targetKind": "repository",
                    "mode": "standard",
                    "route": route,
                },
            }),
            palette,
            true,
        ))
        .child(security_action_button(
            1,
            locale.text("security.deep"),
            runtime.clone(),
            json!({
                "kind": "start_security_scan",
                "sessionId": session_id,
                "payload": {
                    "projectId": workspace,
                    "repository": workspace,
                    "targetKind": "repository",
                    "mode": "deep",
                    "route": route,
                },
            }),
            palette,
            false,
        ));
    let detail = if empty {
        div()
            .flex_1()
            .p(px(42.))
            .flex()
            .flex_col()
            .gap_3()
            .child(icon("shield-check", 32., palette.faint))
            .child(
                div()
                    .text_size(px(16.))
                    .font_weight(gpui::FontWeight::SEMIBOLD)
                    .child(locale.text("security.empty")),
            )
            .child(
                div()
                    .max_w(px(520.))
                    .text_sm()
                    .line_height(px(22.))
                    .text_color(palette.muted)
                    .child(locale.text("security.hint")),
            )
            .into_any_element()
    } else if selected_id.is_empty() {
        div()
            .flex_1()
            .p(px(42.))
            .flex()
            .flex_col()
            .gap_3()
            .child(icon("refresh-cw", 26., palette.faint))
            .child(
                div()
                    .text_sm()
                    .text_color(palette.muted)
                    .child(locale.text("security.loadingDetail")),
            )
            .into_any_element()
    } else {
        let empty_progress = serde_json::Value::Null;
        let progress = state
            .security
            .projection
            .get("progress")
            .unwrap_or(&empty_progress);
        let workers = state
            .security
            .projection
            .get("workers")
            .and_then(serde_json::Value::as_array)
            .map(Vec::as_slice)
            .unwrap_or_default();
        let findings = state
            .security
            .projection
            .get("findings")
            .and_then(serde_json::Value::as_array)
            .map(Vec::as_slice)
            .unwrap_or(&state.security.findings);
        let mode = selected_scan
            .get("mode")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let created = security_timestamp(selected_scan, "createdAt").unwrap_or_else(|| "—".into());
        let notices = [
            (
                locale.text("security.failure"),
                selected_scan
                    .get("failureMessage")
                    .and_then(serde_json::Value::as_str),
                palette.danger,
            ),
            (
                locale.text("security.blockingReason"),
                selected_scan
                    .get("blockingReason")
                    .and_then(serde_json::Value::as_str),
                palette.warning,
            ),
            (
                locale.text("security.warning"),
                selected_scan
                    .get("warning")
                    .and_then(serde_json::Value::as_str),
                palette.warning,
            ),
        ]
        .into_iter()
        .filter_map(|(label, message, color)| {
            let message = security_compact_text(message?, 520);
            (!message.is_empty()).then(|| {
                div()
                    .rounded(px(10.))
                    .bg(palette.paper)
                    .border_1()
                    .border_color(color)
                    .px_4()
                    .py_3()
                    .child(
                        div()
                            .text_xs()
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .text_color(color)
                            .child(label),
                    )
                    .child(
                        div()
                            .mt_1()
                            .text_sm()
                            .line_height(px(20.))
                            .text_color(palette.ink_soft)
                            .child(message),
                    )
            })
        })
        .collect::<Vec<_>>();
        div()
            .flex_1()
            .p(px(34.))
            .flex()
            .flex_col()
            .gap_4()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .child(
                                div()
                                    .text_size(px(22.))
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(selected_title),
                            )
                            .child(
                                div()
                                    .mt_1()
                                    .text_xs()
                                    .text_color(palette.muted)
                                    .child(format!(
                                        "{} · {created}",
                                        locale.value("securityMode", mode)
                                    )),
                            ),
                    )
                    .child(security_status_badge(
                        &selected_status,
                        locale.value("status", &selected_status).to_string(),
                        palette,
                    )),
            )
            .children(notices)
            .child(security_progress_card(
                progress,
                &selected_status,
                palette,
                locale,
            ))
            .child(security_workers_card(
                workers,
                &selected_status,
                palette,
                locale,
            ))
            .child(security_findings_card(
                findings,
                &selected_status,
                palette,
                locale,
            ))
            .when(!selected_id.is_empty(), |detail| {
                detail.child(
                    div()
                        .flex()
                        .gap_2()
                        .when(
                            matches!(selected_status.as_str(), "running" | "queued" | "blocked"),
                            |actions| {
                                actions.child(security_action_button(
                                    2,
                                    if matches!(selected_status.as_str(), "running" | "queued") {
                                        locale.text("security.cancel")
                                    } else {
                                        locale.text("security.resume")
                                    },
                                    runtime.clone(),
                                    json!({
                                        "kind": if matches!(selected_status.as_str(), "running" | "queued") {
                                            "cancel_security_scan"
                                        } else {
                                            "resume_security_scan"
                                        },
                                        "target": selected_id,
                                        "sessionId": session_id,
                                    }),
                                    palette,
                                    false,
                                ))
                            },
                        )
                        .child(security_action_button(
                            3,
                            locale.text("security.export"),
                            runtime.clone(),
                            json!({
                                "kind": "export_security_scan",
                                "target": selected_id,
                                "decision": "sarif",
                                "sessionId": session_id,
                            }),
                            palette,
                            false,
                        )),
                )
            })
            .into_any_element()
    };
    div()
        .id("security-surface")
        .role(Role::Region)
        .aria_label(locale.text("security.title"))
        .flex_1()
        .overflow_hidden()
        .flex()
        .flex_col()
        .child(
            div()
                .min_h(px(130.))
                .px(px(42.))
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
                                .child(locale.text("security.eyebrow")),
                        )
                        .child(
                            div()
                                .text_size(px(32.))
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("security.title")),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.muted)
                                .child(locale.text("security.description")),
                        ),
                )
                .child(actions),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .id("security-scan-list")
                        .w(px(260.))
                        .h_full()
                        .p_5()
                        .border_r_1()
                        .border_color(palette.border)
                        .bg(palette.paper_muted)
                        .flex()
                        .flex_col()
                        .gap_3()
                        .overflow_y_scroll()
                        .when(empty, |list| {
                            list.child(icon("shield-check", 22., palette.faint))
                                .child(
                                    div()
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(locale.text("security.empty")),
                                )
                                .child(
                                    div()
                                        .text_sm()
                                        .line_height(px(22.))
                                        .text_color(palette.muted)
                                        .child(locale.text("security.hint")),
                                )
                        })
                        .children(
                            state
                                .security
                                .scans
                                .iter()
                                .enumerate()
                                .map(|(index, scan)| {
                                    let scan_id = scan
                                        .get("id")
                                        .or_else(|| scan.get("scanId"))
                                        .and_then(serde_json::Value::as_str)
                                        .unwrap_or_default()
                                        .to_string();
                                    let status = scan
                                        .get("status")
                                        .and_then(serde_json::Value::as_str)
                                        .unwrap_or("unknown");
                                    let mode = scan
                                        .get("mode")
                                        .and_then(serde_json::Value::as_str)
                                        .unwrap_or_default();
                                    let created = security_timestamp(scan, "createdAt")
                                        .unwrap_or_else(|| "—".into());
                                    let title = locale.format(
                                        "security.scanNumber",
                                        &[("number", (index + 1).to_string())],
                                    );
                                    let status_label = locale.value("status", status).to_string();
                                    let selected = selected_id == scan_id;
                                    let aria_label = format!(
                                        "{title}, {}, {status_label}, {created}",
                                        locale.value("securityMode", mode)
                                    );
                                    let runtime = runtime.clone();
                                    let row_session_id = session_id.clone();
                                    let row_scan_id = scan_id.clone();
                                    div()
                                        .id(("security-scan", index))
                                        .role(Role::Button)
                                        .aria_label(aria_label)
                                        .aria_selected(selected)
                                        .tab_stop(true)
                                        .min_h(px(64.))
                                        .px_3()
                                        .py_2()
                                        .rounded(px(9.))
                                        .border_1()
                                        .border_color(if selected {
                                            palette.accent
                                        } else {
                                            palette.border
                                        })
                                        .bg(if selected {
                                            palette.accent_soft
                                        } else {
                                            palette.paper
                                        })
                                        .cursor_pointer()
                                        .hover(move |style| style.bg(palette.hover))
                                        .active(|style| style.opacity(0.72))
                                        .on_click(move |_, _, _| {
                                            if row_scan_id.is_empty() {
                                                return;
                                            }
                                            for kind in
                                                ["get_security_scan", "list_security_findings"]
                                            {
                                                runtime.request(
                                                    Method::Execute,
                                                    json!({
                                                        "kind": kind,
                                                        "target": row_scan_id,
                                                        "sessionId": row_session_id,
                                                    }),
                                                );
                                            }
                                        })
                                        .flex()
                                        .items_center()
                                        .child(
                                            div()
                                                .flex_1()
                                                .min_w_0()
                                                .child(
                                                    div()
                                                        .text_sm()
                                                        .font_weight(gpui::FontWeight::MEDIUM)
                                                        .child(title),
                                                )
                                                .child(
                                                    div()
                                                        .mt_1()
                                                        .text_xs()
                                                        .text_color(palette.muted)
                                                        .child(format!(
                                                            "{} · {created}",
                                                            locale.value("securityMode", mode)
                                                        )),
                                                ),
                                        )
                                        .child(
                                            div()
                                                .flex()
                                                .items_center()
                                                .gap_1()
                                                .text_xs()
                                                .text_color(security_status_color(status, palette))
                                                .child(
                                                    div()
                                                        .size(px(6.))
                                                        .rounded_full()
                                                        .bg(security_status_color(status, palette)),
                                                )
                                                .child(status_label),
                                        )
                                }),
                        ),
                )
                .child(
                    div()
                        .id("security-detail-scroll")
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .overflow_y_scroll()
                        .child(detail),
                ),
        )
        .into_any_element()
}

fn security_action_button(
    id: usize,
    label: &'static str,
    runtime: RuntimeConnection,
    payload: serde_json::Value,
    palette: ThemePalette,
    primary: bool,
) -> gpui::AnyElement {
    div()
        .id(("security-action", id))
        .role(Role::Button)
        .aria_label(label)
        .tab_stop(true)
        .px_3()
        .py_2()
        .rounded_md()
        .border_1()
        .border_color(if primary {
            palette.button
        } else {
            palette.border
        })
        .bg(if primary {
            palette.button
        } else {
            palette.paper
        })
        .text_color(if primary {
            palette.button_text
        } else {
            palette.ink
        })
        .cursor_pointer()
        .hover(move |style| style.opacity(0.88))
        .active(|style| style.opacity(0.72))
        .on_click(move |_, _, _| {
            runtime.request(Method::Execute, payload.clone());
        })
        .child(label)
        .into_any_element()
}
