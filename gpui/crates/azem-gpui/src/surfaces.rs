use azem_ipc::Method;
use gpui::{Context, Role, div, prelude::*, px, rgb};
use serde_json::json;

use super::{AzemWindow, PendingRequest};
use crate::{
    runtime_connection::RuntimeConnection,
    state::{AppState, Block},
};

fn pretty_value(value: &serde_json::Value) -> String {
    serde_json::to_string_pretty(value).unwrap_or_default()
}

pub(super) fn settings_surface(state: &AppState, cx: &mut Context<AzemWindow>) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let mut models = Vec::new();
    for provider in &state.catalogs.providers {
        let provider_id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        if let Some(entries) = provider.get("models").and_then(serde_json::Value::as_array) {
            for model in entries {
                if model
                    .get("disabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
                {
                    continue;
                }
                if let Some(model_id) = model.get("id").and_then(serde_json::Value::as_str) {
                    models.push((provider_id.clone(), model_id.to_string()));
                }
            }
        }
    }
    div()
        .id("settings-surface")
        .role(Role::Region)
        .aria_label("Settings and extensions")
        .flex_1()
        .overflow_y_scroll()
        .p_5()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .text_xl()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child("Settings"),
        )
        .child(
            div()
                .flex()
                .flex_wrap()
                .gap_2()
                .children([
                    settings_action_button(
                        0,
                        "English",
                        state.connection.connected,
                        state.settings.language.as_ref() == "en",
                        cx,
                        json!({"kind": "set_language", "target": "en", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        1,
                        "简体中文",
                        state.connection.connected,
                        state.settings.language.as_ref() == "zh-CN",
                        cx,
                        json!({"kind": "set_language", "target": "zh-CN", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        2,
                        "Ask approval",
                        state.connection.connected,
                        state.settings.approval_mode.as_ref() == "prompt",
                        cx,
                        json!({"kind": "set_approval_mode", "target": "prompt", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        3,
                        "Auto review",
                        state.connection.connected,
                        state.settings.approval_mode.as_ref() == "auto_review",
                        cx,
                        json!({"kind": "set_approval_mode", "target": "auto_review", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        4,
                        "YOLO",
                        state.connection.connected,
                        state.settings.approval_mode.as_ref() == "yolo",
                        cx,
                        json!({"kind": "set_approval_mode", "target": "yolo", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        5,
                        "Queue follow-ups",
                        state.connection.connected,
                        state.settings.queue_mode.as_ref() == "queue",
                        cx,
                        json!({"kind": "set_queue_mode", "target": "queue", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        6,
                        "Guide immediately",
                        state.connection.connected,
                        state.settings.queue_mode.as_ref() == "guide",
                        cx,
                        json!({"kind": "set_queue_mode", "target": "guide", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        7,
                        "Refresh MCP",
                        state.connection.connected,
                        false,
                        cx,
                        json!({"kind": "refresh_mcp", "sessionId": session_id}),
                    ),
                    settings_action_button(
                        8,
                        "Refresh extensions",
                        state.connection.connected,
                        false,
                        cx,
                        json!({"kind": "list_plugins", "sessionId": session_id}),
                    ),
                ]),
        )
        .child(
            div()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(format!(
                    "Model · {} / {} / {}",
                    state.settings.provider, state.settings.model, state.settings.reasoning
                )),
        )
        .child(
            div()
                .flex()
                .flex_wrap()
                .gap_2()
                .children(models.into_iter().enumerate().map(
                    |(index, (provider, model))| {
                        let label = format!("{provider} · {model}");
                        let selected = state.settings.provider.as_ref() == provider
                            && state.settings.model.as_ref() == model;
                        div()
                            .id(("model-option", index))
                            .role(Role::RadioButton)
                            .aria_label(label.clone())
                            .aria_selected(selected)
                            .tab_stop(true)
                            .px_3()
                            .py_2()
                            .rounded_md()
                            .bg(if selected {
                                rgb(0xe5e4de)
                            } else {
                                rgb(0xf5f5f3)
                            })
                            .border_1()
                            .border_color(rgb(0xdeddd7))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.state.settings.provider = provider.clone().into();
                                this.state.settings.model = model.clone().into();
                                cx.notify();
                            }))
                            .child(label)
                    },
                )),
        )
        .child(work_data_card(
            "Routes",
            serde_json::to_string_pretty(&state.catalogs.routes).unwrap_or_default(),
        ))
        .child(work_data_card(
            "Skills and plugins",
            serde_json::to_string_pretty(&json!({
                "skills": state.catalogs.skills,
                "skillDiagnostics": state.catalogs.skill_diagnostics,
                "plugins": state.catalogs.plugins,
                "pluginDiagnostics": state.catalogs.plugin_diagnostics,
                "marketplace": state.catalogs.marketplace,
            }))
            .unwrap_or_default(),
        ))
        .child(work_data_card(
            "MCP and hooks",
            serde_json::to_string_pretty(&json!({
                "mcp": state.catalogs.mcp,
                "hooks": state.catalogs.hooks,
            }))
            .unwrap_or_default(),
        ))
        .child(work_data_card(
            "Appearance, auth, recovery",
            serde_json::to_string_pretty(&json!({
                "appearance": state.settings.appearance,
                "themes": state.catalogs.themes,
                "auth": state.catalogs.auth,
                "recovery": state.settings.recovery,
                "error": state.settings.error,
            }))
            .unwrap_or_default(),
        ))
        .into_any_element()
}

fn settings_action_button(
    id: usize,
    label: &'static str,
    enabled: bool,
    selected: bool,
    cx: &mut Context<AzemWindow>,
    payload: serde_json::Value,
) -> gpui::AnyElement {
    div()
        .id(("settings-action", id))
        .role(if id <= 6 {
            Role::RadioButton
        } else {
            Role::Button
        })
        .aria_label(label)
        .when(id <= 6, |button| button.aria_selected(selected))
        .tab_stop(enabled)
        .px_3()
        .py_2()
        .rounded_md()
        .bg(if selected {
            rgb(0xe5e4de)
        } else {
            rgb(0xf5f5f3)
        })
        .border_1()
        .border_color(rgb(0xdeddd7))
        .on_click(cx.listener(move |this, _, _, cx| {
            if enabled {
                this.runtime.request(Method::Execute, payload.clone());
                cx.notify();
            }
        }))
        .child(label)
        .into_any_element()
}

pub(super) fn usage_surface(state: &AppState, cx: &mut Context<AzemWindow>) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    div()
        .id("usage-surface")
        .role(Role::Region)
        .aria_label("Usage and context")
        .flex_1()
        .overflow_y_scroll()
        .p_5()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .flex()
                .gap_2()
                .child(
                    div()
                        .id("refresh-usage")
                        .role(Role::Button)
                        .aria_label("Refresh usage")
                        .tab_stop(true)
                        .px_3()
                        .py_2()
                        .rounded_md()
                        .border_1()
                        .border_color(rgb(0xdeddd7))
                        .on_click(cx.listener(|this, _, _, cx| {
                            let id = this
                                .runtime
                                .request(Method::UsageReport, json!({"scope": "all"}));
                            this.pending_requests.insert(id, PendingRequest::Usage);
                            cx.notify();
                        }))
                        .child("Refresh"),
                )
                .child(
                    div()
                        .id("compact-session")
                        .role(Role::Button)
                        .aria_label("Compact session context")
                        .tab_stop(true)
                        .px_3()
                        .py_2()
                        .rounded_md()
                        .border_1()
                        .border_color(rgb(0xdeddd7))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.runtime.request(
                                Method::Execute,
                                json!({"kind": "compact", "sessionId": session_id}),
                            );
                            cx.notify();
                        }))
                        .child("Compact"),
                ),
        )
        .child(work_data_card("Usage", pretty_value(&state.settings.usage)))
        .child(work_data_card(
            "Context profile",
            pretty_value(&state.runtime.context_profile),
        ))
        .child(work_data_card(
            "Recap and archive",
            serde_json::to_string_pretty(&json!({
                "recap": state.runtime.recap,
                "archive": state.settings.archive,
                "sessionTree": state.navigation.session_tree,
            }))
            .unwrap_or_default(),
        ))
        .child(work_data_card(
            "Background work",
            serde_json::to_string_pretty(&json!({
                "processes": state.runtime.background,
                "logs": state.runtime.background_logs,
                "agents": state.runtime.agents,
                "memories": state.runtime.memories,
            }))
            .unwrap_or_default(),
        ))
        .into_any_element()
}

pub(super) fn projects_surface(state: &AppState) -> gpui::AnyElement {
    div()
        .id("projects-surface")
        .role(Role::Region)
        .aria_label("Projects")
        .flex_1()
        .overflow_y_scroll()
        .p_5()
        .flex()
        .flex_col()
        .gap_3()
        .children(
            state
                .navigation
                .projects
                .iter()
                .enumerate()
                .map(|(index, project)| {
                    let path = project.path.to_string();
                    let display_path = path.clone();
                    let name = if project.name.is_empty() {
                        path.clone()
                    } else {
                        project.name.to_string()
                    };
                    div()
                        .id(("project", index))
                        .role(Role::Button)
                        .aria_label(format!("Open project {name}"))
                        .tab_stop(true)
                        .p_4()
                        .rounded_lg()
                        .border_1()
                        .border_color(rgb(0xdeddd7))
                        .on_click(move |_, _, _| {
                            let _ = launch_gpui_window(&path, "");
                        })
                        .child(div().font_weight(gpui::FontWeight::SEMIBOLD).child(name))
                        .child(
                            div()
                                .text_sm()
                                .text_color(rgb(0x6f7278))
                                .child(display_path),
                        )
                }),
        )
        .into_any_element()
}

pub(super) fn launch_gpui_window(workspace: &str, session_id: &str) -> std::io::Result<()> {
    let executable = std::env::current_exe()?;
    let mut command = std::process::Command::new(executable);
    command
        .arg("--workspace")
        .arg(workspace)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null());
    if !session_id.is_empty() {
        command.arg("--session").arg(session_id);
    }
    detach_gpui_window(&mut command);
    command.spawn().map(|_| ())
}

#[cfg(unix)]
fn detach_gpui_window(command: &mut std::process::Command) {
    use std::os::unix::process::CommandExt;
    command.process_group(0);
}

#[cfg(windows)]
fn detach_gpui_window(command: &mut std::process::Command) {
    use std::os::windows::process::CommandExt;
    const DETACHED_PROCESS: u32 = 0x0000_0008;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    command.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP);
}

#[cfg(not(any(unix, windows)))]
fn detach_gpui_window(_command: &mut std::process::Command) {}

pub(super) fn security_surface(state: &AppState, runtime: &RuntimeConnection) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let workspace = state.workspace.root.to_string();
    let route = json!({
        "provider": state.settings.provider,
        "model": state.settings.model,
        "reasoning": state.settings.reasoning,
    });
    let selected_id = state
        .security
        .projection
        .get("id")
        .or_else(|| state.security.projection.get("scanId"))
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let selected_status = state
        .security
        .projection
        .get("status")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    div()
        .id("security-surface")
        .role(Role::Region)
        .aria_label("Security scans")
        .flex_1()
        .overflow_y_scroll()
        .p_5()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .flex()
                .gap_2()
                .child(security_action_button(
                    0,
                    "Standard scan",
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
                ))
                .child(security_action_button(
                    1,
                    "Deep scan",
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
                ))
                .when(!selected_id.is_empty(), |actions| {
                    actions
                        .child(security_action_button(
                            2,
                            if matches!(selected_status.as_str(), "running" | "queued") {
                                "Cancel scan"
                            } else {
                                "Resume scan"
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
                        ))
                        .child(security_action_button(
                            3,
                            "Export SARIF",
                            runtime.clone(),
                            json!({
                                "kind": "export_security_scan",
                                "target": selected_id,
                                "decision": "sarif",
                                "sessionId": session_id,
                            }),
                        ))
                }),
        )
        .child(work_data_card(
            "Current scan",
            pretty_value(&state.security.projection),
        ))
        .child(work_data_card(
            "Scans",
            serde_json::to_string_pretty(&state.security.scans).unwrap_or_default(),
        ))
        .child(work_data_card(
            "Findings",
            serde_json::to_string_pretty(&state.security.findings).unwrap_or_default(),
        ))
        .child(work_data_card(
            "Finding detail",
            pretty_value(&state.security.selected_finding),
        ))
        .child(work_data_card(
            "Patch and publication",
            pretty_value(&state.security.patch),
        ))
        .into_any_element()
}

fn security_action_button(
    id: usize,
    label: &'static str,
    runtime: RuntimeConnection,
    payload: serde_json::Value,
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
        .border_color(rgb(0xc7c6c0))
        .on_click(move |_, _, _| {
            runtime.request(Method::Execute, payload.clone());
        })
        .child(label)
        .into_any_element()
}

pub(super) fn pull_requests_surface(
    state: &AppState,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mut pull_requests = Vec::new();
    if let Some(current) = state.pull_requests.dashboard.get("current")
        && !current.is_null()
    {
        pull_requests.push(current.clone());
    }
    for key in ["createdByViewer", "needsReview", "open"] {
        if let Some(values) = state
            .pull_requests
            .dashboard
            .get(key)
            .and_then(serde_json::Value::as_array)
        {
            for value in values {
                let number = value.get("number").and_then(serde_json::Value::as_i64);
                if !pull_requests.iter().any(|existing| {
                    existing.get("number").and_then(serde_json::Value::as_i64) == number
                }) {
                    pull_requests.push(value.clone());
                }
            }
        }
    }
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
    let mut capability_message = state
        .pull_requests
        .dashboard
        .pointer("/capability/message")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("GitHub pull requests")
        .to_string();
    if state.pull_requests.loading {
        capability_message.push_str(" · Loading");
    }
    if !state.pull_requests.error.is_empty() {
        capability_message.push_str(" · ");
        capability_message.push_str(&state.pull_requests.error);
    }
    div()
        .id("pull-requests")
        .role(Role::Region)
        .aria_label("Pull requests")
        .flex_1()
        .flex()
        .overflow_hidden()
        .child(
            div()
                .id("pull-request-list")
                .w(px(360.))
                .h_full()
                .overflow_y_scroll()
                .border_r_1()
                .border_color(rgb(0xdeddd7))
                .p_3()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .text_color(rgb(0x6f7278))
                        .child(capability_message),
                )
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
                                .unwrap_or("Untitled pull request")
                                .to_string();
                            div()
                                .id(("pull-request", index))
                                .role(Role::Button)
                                .aria_label(format!("Pull request {number}: {title}"))
                                .tab_stop(true)
                                .p_3()
                                .rounded_md()
                                .border_1()
                                .border_color(rgb(0xdeddd7))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    let id = this.runtime.request(
                                        Method::PullRequestDetail,
                                        json!({"number": number}),
                                    );
                                    this.pending_requests
                                        .insert(id, PendingRequest::PullRequestDetail);
                                    cx.notify();
                                }))
                                .child(format!("#{number}  {title}"))
                        }),
                ),
        )
        .child(
            div()
                .id("pull-request-detail")
                .role(Role::Document)
                .aria_label("Pull request detail")
                .flex_1()
                .h_full()
                .overflow_y_scroll()
                .p_5()
                .flex()
                .flex_col()
                .gap_3()
                .child(work_data_card(
                    "Pull request detail",
                    pretty_value(&state.pull_requests.selected),
                ))
                .when(number > 0, |detail| {
                    let draft = selected
                        .pointer("/pullRequest/draft")
                        .and_then(serde_json::Value::as_bool)
                        .unwrap_or(false);
                    let state_name = selected
                        .pointer("/pullRequest/state")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default();
                    detail.child(
                        div()
                            .flex()
                            .gap_2()
                            .child(pr_action_button(
                                0,
                                if draft {
                                    "Mark ready"
                                } else {
                                    "Convert to draft"
                                },
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
                                    "Close"
                                } else {
                                    "Reopen"
                                },
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
                                "Monitor",
                                cx,
                                Method::SetPullRequestMonitor,
                                json!({"number": number, "enabled": true}),
                            )),
                    )
                }),
        )
        .into_any_element()
}

fn pr_action_button(
    id: usize,
    label: &'static str,
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
        .border_color(rgb(0xc7c6c0))
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

pub(super) fn workspace_files_surface(
    state: &AppState,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let directory = state
        .workspace
        .file_tree
        .get("path")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let entries = state
        .workspace
        .file_tree
        .get("entries")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let preview = state
        .workspace
        .selected_file
        .get("content")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .unwrap_or_else(|| pretty_value(&state.workspace.selected_file));
    div()
        .id("workspace-files")
        .role(Role::Region)
        .aria_label("Workspace files")
        .flex_1()
        .flex()
        .overflow_hidden()
        .child(
            div()
                .id("workspace-entry-list")
                .w(px(320.))
                .h_full()
                .overflow_y_scroll()
                .border_r_1()
                .border_color(rgb(0xdeddd7))
                .p_3()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .text_color(rgb(0x6f7278))
                        .child(if directory.is_empty() {
                            ".".to_string()
                        } else {
                            directory
                        }),
                )
                .children(entries.into_iter().enumerate().map(|(index, entry)| {
                    let path = entry
                        .get("path")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .to_string();
                    let directory = entry
                        .get("directory")
                        .and_then(serde_json::Value::as_bool)
                        .unwrap_or(false);
                    let label = format!(
                        "{}{}",
                        if directory { "▸ " } else { "" },
                        entry
                            .get("name")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                    );
                    div()
                        .id(("workspace-entry", index))
                        .role(Role::Button)
                        .aria_label(label.clone())
                        .tab_stop(true)
                        .px_3()
                        .py_2()
                        .rounded_md()
                        .on_click(cx.listener(move |this, _, _, cx| {
                            let (method, pending) = if directory {
                                (Method::WorkspaceEntries, PendingRequest::Entries)
                            } else {
                                (Method::WorkspaceFile, PendingRequest::File)
                            };
                            let id = this.runtime.request(method, json!({"path": path}));
                            this.pending_requests.insert(id, pending);
                            cx.notify();
                        }))
                        .child(label)
                })),
        )
        .child(
            div()
                .id("workspace-preview")
                .role(Role::Document)
                .aria_label("File preview")
                .flex_1()
                .h_full()
                .overflow_y_scroll()
                .p_5()
                .font_family("SF Mono")
                .text_sm()
                .whitespace_normal()
                .child(if preview.is_empty() {
                    "Select a file".to_string()
                } else {
                    preview
                }),
        )
        .into_any_element()
}

pub(super) fn workspace_changes_surface(
    state: &AppState,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let files = state
        .workspace
        .changes
        .get("files")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let patch = state
        .workspace
        .selected_file
        .get("patch")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .unwrap_or_default();
    div()
        .id("workspace-changes")
        .role(Role::Region)
        .aria_label("Workspace changes")
        .flex_1()
        .flex()
        .overflow_hidden()
        .child(
            div()
                .id("workspace-change-list")
                .w(px(320.))
                .h_full()
                .overflow_y_scroll()
                .border_r_1()
                .border_color(rgb(0xdeddd7))
                .p_3()
                .flex()
                .flex_col()
                .gap_1()
                .children(files.into_iter().enumerate().map(|(index, file)| {
                    let path = file
                        .get("path")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .to_string();
                    let label = format!(
                        "{}  +{} −{}",
                        path,
                        file.get("additions")
                            .and_then(serde_json::Value::as_i64)
                            .unwrap_or_default(),
                        file.get("deletions")
                            .and_then(serde_json::Value::as_i64)
                            .unwrap_or_default()
                    );
                    div()
                        .id(("workspace-change", index))
                        .role(Role::Button)
                        .aria_label(label.clone())
                        .tab_stop(true)
                        .px_3()
                        .py_2()
                        .rounded_md()
                        .on_click(cx.listener(move |this, _, _, cx| {
                            let id = this
                                .runtime
                                .request(Method::WorkspaceChange, json!({"path": path}));
                            this.pending_requests.insert(id, PendingRequest::Change);
                            cx.notify();
                        }))
                        .child(label)
                })),
        )
        .child(
            div()
                .id("change-preview")
                .role(Role::Document)
                .aria_label("Selected change")
                .flex_1()
                .h_full()
                .overflow_y_scroll()
                .p_5()
                .font_family("SF Mono")
                .text_sm()
                .whitespace_normal()
                .child(if patch.is_empty() {
                    "Select a changed file".to_string()
                } else {
                    patch
                }),
        )
        .into_any_element()
}

pub(super) fn work_surface(state: &AppState, runtime: &RuntimeConnection) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    div()
        .id("work-surface")
        .role(Role::Region)
        .aria_label("Run controls")
        .flex_1()
        .overflow_y_scroll()
        .p_6()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .text_xl()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child("Run controls"),
        )
        .child(work_data_card("Todo", pretty_value(&state.runtime.todo)))
        .children(
            state
                .runtime
                .approvals
                .iter()
                .enumerate()
                .map(|(index, approval)| {
                    let target = value_id(approval, "approvalId");
                    work_data_card("Approval", pretty_value(approval)).child(
                        div()
                            .flex()
                            .gap_2()
                            .child(ipc_action_button(
                                index * 10,
                                "Allow",
                                runtime.clone(),
                                json!({
                                    "kind": "resolve_approval",
                                    "target": target,
                                    "decision": "allow",
                                    "sessionId": session_id,
                                }),
                            ))
                            .child(ipc_action_button(
                                index * 10 + 1,
                                "Deny",
                                runtime.clone(),
                                json!({
                                    "kind": "resolve_approval",
                                    "target": value_id(approval, "approvalId"),
                                    "decision": "deny",
                                    "sessionId": session_id,
                                }),
                            )),
                    )
                }),
        )
        .children(
            state
                .runtime
                .questions
                .iter()
                .enumerate()
                .map(|(index, question)| {
                    work_data_card("Question", pretty_value(question)).when_some(
                        recommended_question_payload(question, &session_id),
                        |card, payload| {
                            card.child(ipc_action_button(
                                30_000 + index,
                                "Use recommended answers",
                                runtime.clone(),
                                payload,
                            ))
                        },
                    )
                }),
        )
        .children(state.runtime.plans.iter().enumerate().map(|(index, plan)| {
            let target = value_id(plan, "planId");
            work_data_card("Plan", pretty_value(plan)).child(
                div()
                    .flex()
                    .gap_2()
                    .child(ipc_action_button(
                        10_000 + index * 10,
                        "Execute",
                        runtime.clone(),
                        json!({
                            "kind": "resolve_plan",
                            "target": target,
                            "decision": "execute",
                            "sessionId": session_id,
                        }),
                    ))
                    .child(ipc_action_button(
                        10_001 + index * 10,
                        "Reject",
                        runtime.clone(),
                        json!({
                            "kind": "resolve_plan",
                            "target": value_id(plan, "planId"),
                            "decision": "reject",
                            "sessionId": session_id,
                        }),
                    )),
            )
        }))
        .children(
            state
                .runtime
                .agents
                .iter()
                .enumerate()
                .map(|(index, agent)| {
                    work_data_card("Agent", pretty_value(agent)).child(ipc_action_button(
                        20_000 + index,
                        "Cancel agent",
                        runtime.clone(),
                        json!({
                            "kind": "cancel_agent",
                            "target": value_id(agent, "id"),
                            "sessionId": session_id,
                        }),
                    ))
                }),
        )
        .into_any_element()
}

fn work_data_card(title: &'static str, data: String) -> gpui::Div {
    div()
        .border_1()
        .border_color(rgb(0xdeddd7))
        .rounded_lg()
        .p_4()
        .flex()
        .flex_col()
        .gap_2()
        .child(div().font_weight(gpui::FontWeight::SEMIBOLD).child(title))
        .child(
            div()
                .font_family("SF Mono")
                .text_sm()
                .whitespace_normal()
                .child(data),
        )
}

fn ipc_action_button(
    id: usize,
    label: &'static str,
    runtime: RuntimeConnection,
    payload: serde_json::Value,
) -> gpui::AnyElement {
    div()
        .id(("work-action", id))
        .role(Role::Button)
        .aria_label(label)
        .tab_stop(true)
        .px_3()
        .py_2()
        .rounded_md()
        .border_1()
        .border_color(rgb(0xc7c6c0))
        .on_click(move |_, _, _| {
            runtime.request(Method::Execute, payload.clone());
        })
        .child(label)
        .into_any_element()
}

fn value_id(value: &serde_json::Value, key: &str) -> String {
    value
        .get(key)
        .or_else(|| value.get("data").and_then(|data| data.get(key)))
        .or_else(|| value.get("agentId"))
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string()
}
fn recommended_question_payload(
    value: &serde_json::Value,
    session_id: &str,
) -> Option<serde_json::Value> {
    let encoded = value
        .pointer("/data/questions")
        .and_then(serde_json::Value::as_str)?;
    let questions = serde_json::from_str::<Vec<serde_json::Value>>(encoded).ok()?;
    let mut answers = Vec::with_capacity(questions.len());
    for question in questions {
        let question_id = question.get("id")?.as_str()?;
        let options = question.get("options")?.as_array()?;
        let selected = options
            .iter()
            .find(|option| {
                option
                    .get("recommended")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
            })
            .or_else(|| options.first())?
            .get("label")?
            .as_str()?;
        answers.push(json!({
            "question_id": question_id,
            "selected": [selected],
        }));
    }
    Some(json!({
        "kind": "resolve_user_input",
        "target": value_id(value, "userInputId"),
        "sessionId": session_id,
        "payload": {"answers": answers},
    }))
}
pub(super) fn timeline_block(index: usize, block: Block) -> gpui::AnyElement {
    let kind = block.kind.as_ref();
    let label = if !block.title.is_empty() {
        block.title.to_string()
    } else {
        match kind {
            "user" => "You".to_string(),
            "assistant" => {
                if block.text_phase.as_ref() == "commentary" {
                    "Progress".to_string()
                } else {
                    "Azem".to_string()
                }
            }
            "thinking" => "Thinking".to_string(),
            "tool" => format!("Tool · {}", block.state),
            "diff" => "File change".to_string(),
            "error" => "Error".to_string(),
            _ => kind.to_string(),
        }
    };
    let is_user = kind == "user";
    let is_process = matches!(kind, "thinking" | "tool" | "diff");
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(label.clone())
        .w_full()
        .px_8()
        .py_3()
        .child(
            div()
                .max_w(if is_process { px(920.) } else { px(780.) })
                .when(is_user, |row| row.ml_auto().bg(rgb(0xe8e7e2)))
                .when(is_process, |row| {
                    row.border_l_2().border_color(rgb(0xb8b7b1)).pl_3()
                })
                .when(kind == "error", |row| row.bg(rgb(0xf7e9e7)))
                .rounded_lg()
                .px_3()
                .py_2()
                .child(div().text_xs().text_color(rgb(0x6f7278)).child(label))
                .child(
                    div()
                        .text_sm()
                        .line_height(px(22.))
                        .whitespace_normal()
                        .child(block.content),
                ),
        )
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::recommended_question_payload;

    #[test]
    fn recommended_question_answers_are_structured_for_the_runtime() {
        let question = json!({
            "userInputId": "input-1",
            "data": {
                "questions": "[{\"id\":\"q1\",\"options\":[{\"label\":\"A\"},{\"label\":\"B\",\"recommended\":true}]}]"
            }
        });
        let payload = recommended_question_payload(&question, "session-1").unwrap();
        assert_eq!(payload["kind"], "resolve_user_input");
        assert_eq!(payload["target"], "input-1");
        assert_eq!(payload["payload"]["answers"][0]["selected"][0], "B");
    }
}
