use std::{cell::RefCell, collections::HashSet, rc::Rc};

use azem_ipc::Method;
use gpui::{
    BoxShadow, Context, Entity, Focusable, Rgba, Role, ScrollHandle, Svg, div, hsla, prelude::*,
    px, rgb, rgba, svg,
};
use serde_json::json;

use super::{AzemWindow, PendingRequest};
use crate::{
    localization::Labels,
    runtime_connection::RuntimeConnection,
    state::{AppState, Block, Surface},
    text_input::TextInput,
    theme::ThemePalette,
};

fn pretty_value(value: &serde_json::Value) -> String {
    serde_json::to_string_pretty(value).unwrap_or_default()
}

pub(super) fn icon(name: &'static str, size: f32, color: Rgba) -> Svg {
    svg()
        .path(format!("icons/{name}.svg"))
        .size(px(size))
        .text_color(color)
}

pub(super) fn provider_logo(provider: &str, size: f32, color: Rgba) -> Svg {
    let normalized = provider.to_ascii_lowercase().replace('_', "-");
    let logo = match normalized.as_str() {
        "chatgpt" | "azure-openai" => "openai".to_string(),
        "grok" => "xai".to_string(),
        "claude" => "anthropic".to_string(),
        "gemini" | "google-ai" => "google".to_string(),
        _ => normalized,
    };
    svg()
        .path(format!("logos/{logo}.svg"))
        .size(px(size))
        .text_color(color)
}

pub(super) fn sidebar(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    open_projects: &HashSet<String>,
    show_all_sessions: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mut projects = Vec::new();
    if !state.workspace.root.is_empty() {
        projects.push(state.workspace.root.to_string());
    }
    for project in &state.navigation.projects {
        let path = project.path.to_string();
        if !path.is_empty() && !projects.contains(&path) {
            projects.push(path);
        }
    }
    let current_workspace = state.workspace.root.to_string();
    let current_surface = state.navigation.surface;
    let current_pull_request = state.pull_requests.dashboard.get("current").cloned();
    div()
        .id("sidebar")
        .role(Role::Navigation)
        .aria_label(labels.projects)
        .w(px(246.))
        .h_full()
        .bg(palette.sidebar)
        .border_r_1()
        .border_color(palette.border)
        .px(px(10.))
        .pt(px(9.))
        .pb(px(12.))
        .flex()
        .flex_col()
        .child(
            div()
                .id("sidebar-switcher")
                .role(Role::TabList)
                .h(px(32.))
                .p(px(2.))
                .mb(px(12.))
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper_muted)
                .flex()
                .gap(px(2.))
                .children(
                    [
                        (labels.conversation, Surface::Thread),
                        (labels.workspace, Surface::Projects),
                    ]
                    .into_iter()
                    .enumerate()
                    .map(|(index, (label, destination))| {
                        let selected = matches!(
                            (destination, current_surface),
                            (Surface::Thread, Surface::Thread | Surface::Search)
                                | (
                                    Surface::Projects,
                                    Surface::Projects
                                        | Surface::Files
                                        | Surface::Changes
                                        | Surface::PullRequests
                                        | Surface::Work
                                        | Surface::Security
                                        | Surface::Terminal
                                        | Surface::Usage
                                )
                        );
                        div()
                            .id(("sidebar-tab", index))
                            .role(Role::Tab)
                            .aria_label(label)
                            .aria_selected(selected)
                            .tab_stop(true)
                            .flex_1()
                            .h_full()
                            .rounded(px(6.))
                            .bg(if selected {
                                palette.paper
                            } else {
                                palette.paper_muted
                            })
                            .text_color(if selected { palette.ink } else { palette.muted })
                            .text_xs()
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .when(selected, |tab| {
                                tab.shadow(vec![
                                    BoxShadow::new(
                                        px(0.),
                                        px(1.),
                                        hsla(220. / 360., 0.1, 0.15, 0.09),
                                    )
                                    .blur_radius(px(5.)),
                                ])
                            })
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
                .id("sidebar-primary")
                .role(Role::Navigation)
                .aria_label("Primary")
                .flex()
                .flex_col()
                .gap(px(2.))
                .mb(px(18.))
                .child(
                    div()
                        .id("sidebar-new")
                        .role(Role::Button)
                        .aria_label(labels.new_conversation)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink_soft)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(AzemWindow::new_session))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.accent)
                                .text_base()
                                .child(icon("plus", 15., palette.accent)),
                        )
                        .child(labels.new_conversation)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘N")),
                )
                .child(
                    div()
                        .id("sidebar-search")
                        .role(Role::Button)
                        .aria_label(labels.search)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink_soft)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, window, cx| {
                            this.state.navigation.surface = Surface::Search;
                            this.search_input.focus_handle(cx).focus(window, cx);
                            cx.notify();
                        }))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.muted)
                                .text_base()
                                .child(icon("search", 15., palette.muted)),
                        )
                        .child(labels.search)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘K")),
                )
                .child(
                    div()
                        .id("sidebar-security")
                        .role(Role::Button)
                        .aria_label(labels.security)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .bg(if current_surface == Surface::Security {
                            palette.hover
                        } else {
                            palette.sidebar
                        })
                        .text_color(if current_surface == Surface::Security {
                            palette.ink
                        } else {
                            palette.ink_soft
                        })
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.state.navigation.surface = Surface::Security;
                            cx.notify();
                        }))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.muted)
                                .text_sm()
                                .child(icon("shield-check", 15., palette.muted)),
                        )
                        .child(labels.security),
                ),
        )
        .child(
            div()
                .h(px(34.))
                .px(px(6.))
                .flex()
                .items_center()
                .child(
                    div()
                        .text_color(palette.faint)
                        .text_xs()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(labels.projects),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .id("project-add")
                        .role(Role::Button)
                        .aria_label(labels.projects)
                        .tab_stop(true)
                        .size(px(25.))
                        .rounded(px(7.))
                        .text_color(palette.faint)
                        .flex()
                        .items_center()
                        .justify_center()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.state.navigation.surface = Surface::Projects;
                            cx.notify();
                        }))
                        .child(icon("plus", 15., palette.faint)),
                ),
        )
        .child(
            div()
                .id("project-tree")
                .role(Role::List)
                .aria_label(labels.projects)
                .flex_1()
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .children(
                    projects
                        .into_iter()
                        .enumerate()
                        .map(|(project_index, project)| {
                            let active = project == current_workspace;
                            let expanded = active || open_projects.contains(&project);
                            let project_name = std::path::Path::new(&project)
                                .file_name()
                                .and_then(|name| name.to_str())
                                .unwrap_or("workspace")
                                .to_string();
                            let sessions = state
                                .navigation
                                .sessions
                                .iter()
                                .filter(|session| {
                                    session.workspace.as_ref() == project
                                        || (session.workspace.is_empty() && active)
                                })
                                .collect::<Vec<_>>();
                            let visible_count = if active && show_all_sessions {
                                sessions.len()
                            } else {
                                sessions.len().min(5)
                            };
                            let toggle_project = project.clone();
                            let new_project_session = project.clone();
                            let project_pull_request = if active {
                                current_pull_request.clone()
                            } else {
                                None
                            };
                            div()
                                .id(("project-node", project_index))
                                .flex()
                                .flex_col()
                                .mb(px(3.))
                                .child(
                                    div()
                                        .h(px(35.))
                                        .pl(px(3.))
                                        .pr(px(4.))
                                        .rounded(px(9.))
                                        .bg(if active {
                                            palette.accent_soft
                                        } else {
                                            palette.sidebar
                                        })
                                        .flex()
                                        .items_center()
                                        .child(
                                            div()
                                                .id(("project-toggle", project_index))
                                                .role(Role::Button)
                                                .aria_label(project_name.clone())
                                                .aria_expanded(expanded)
                                                .tab_stop(true)
                                                .flex_1()
                                                .h(px(31.))
                                                .flex()
                                                .items_center()
                                                .gap(px(6.))
                                                .cursor_pointer()
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if this.open_projects.contains(&toggle_project)
                                                    {
                                                        this.open_projects.remove(&toggle_project);
                                                    } else {
                                                        this.open_projects
                                                            .insert(toggle_project.clone());
                                                    }
                                                    cx.notify();
                                                }))
                                                .child(div().w(px(10.)).child(icon(
                                                    if expanded {
                                                        "chevron-down"
                                                    } else {
                                                        "chevron-right"
                                                    },
                                                    12.,
                                                    palette.faint,
                                                )))
                                                .child(
                                                    div()
                                                        .overflow_hidden()
                                                        .text_color(palette.ink)
                                                        .text_xs()
                                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                                        .child(project_name),
                                                ),
                                        )
                                        .child(
                                            div()
                                                .id(("project-new-session", project_index))
                                                .role(Role::Button)
                                                .aria_label(labels.new_conversation)
                                                .tab_stop(true)
                                                .size(px(24.))
                                                .rounded(px(7.))
                                                .text_color(palette.faint)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.paper))
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if active {
                                                        this.runtime.request(
                                                            Method::Execute,
                                                            json!({"kind": "new_session"}),
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                        cx.notify();
                                                    } else {
                                                        let _ = launch_gpui_window(
                                                            &new_project_session,
                                                            "",
                                                        );
                                                    }
                                                }))
                                                .child(icon("plus", 15., palette.faint)),
                                        ),
                                )
                                .when(expanded, |node| {
                                    let node = node.when_some(
                                        project_pull_request,
                                        |node, pull_request| {
                                            let number = pull_request
                                                .get("number")
                                                .and_then(serde_json::Value::as_i64)
                                                .unwrap_or_default();
                                            let title = pull_request
                                                .get("title")
                                                .and_then(serde_json::Value::as_str)
                                                .unwrap_or("Pull request")
                                                .to_string();
                                            let checks = pull_request
                                                .pointer("/checks/total")
                                                .and_then(serde_json::Value::as_i64)
                                                .unwrap_or_default();
                                            node.child(
                                                div()
                                                    .id(("project-pull-request", project_index))
                                                    .role(Role::Button)
                                                    .aria_label(title.clone())
                                                    .tab_stop(true)
                                                    .ml(px(27.))
                                                    .h(px(52.))
                                                    .px(px(6.))
                                                    .rounded(px(8.))
                                                    .text_color(palette.muted)
                                                    .flex()
                                                    .items_center()
                                                    .gap_2()
                                                    .cursor_pointer()
                                                    .hover(move |style| style.bg(palette.hover))
                                                    .on_click(cx.listener(move |this, _, _, cx| {
                                                        let request_id = this.runtime.request(
                                                            Method::PullRequestDetail,
                                                            json!({"number": number}),
                                                        );
                                                        this.pending_requests.insert(
                                                            request_id,
                                                            PendingRequest::PullRequestDetail,
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::PullRequests;
                                                        cx.notify();
                                                    }))
                                                    .child(icon(
                                                        "git-pull-request",
                                                        14.,
                                                        palette.accent,
                                                    ))
                                                    .child(
                                                        div()
                                                            .flex_1()
                                                            .min_w_0()
                                                            .overflow_hidden()
                                                            .flex()
                                                            .flex_col()
                                                            .child(
                                                                div()
                                                                    .w_full()
                                                                    .truncate()
                                                                    .text_xs()
                                                                    .font_weight(
                                                                        gpui::FontWeight::MEDIUM,
                                                                    )
                                                                    .child(title),
                                                            )
                                                            .child(
                                                                div()
                                                                    .text_size(px(10.))
                                                                    .text_color(palette.faint)
                                                                    .child(format!(
                                                                        "#{number} · {checks} checks"
                                                                    )),
                                                            ),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(9.))
                                                            .text_color(palette.accent)
                                                            .child("PR"),
                                                    ),
                                            )
                                        },
                                    );
                                    node.child(
                                        div()
                                            .ml(px(17.))
                                            .pl(px(10.))
                                            .border_l_1()
                                            .border_color(palette.border_strong)
                                            .flex()
                                            .flex_col()
                                            .children(
                                                sessions
                                                    .into_iter()
                                                    .take(visible_count)
                                                    .enumerate()
                                                    .map(|(session_index, session)| {
                                                        let session_id = session.id.to_string();
                                                        let workspace = project.clone();
                                                        let selected = active
                                                            && state.navigation.current_session_id
                                                                == session.id
                                                            && current_surface == Surface::Thread;
                                                        let title = if session.title.is_empty() {
                                                            labels.new_conversation.to_string()
                                                        } else {
                                                            session.title.to_string()
                                                        };
                                                        div()
                                                .id((
                                                    "project-session",
                                                    project_index * 1000 + session_index,
                                                ))
                                                .role(Role::Button)
                                                .aria_label(title.clone())
                                                .aria_selected(selected)
                                                .tab_stop(true)
                                                .h(px(35.))
                                                .px(px(6.))
                                                .rounded(px(8.))
                                                .bg(if selected {
                                                    palette.hover
                                                } else {
                                                    palette.sidebar
                                                })
                                                .text_color(if selected {
                                                    palette.ink
                                                } else {
                                                    palette.muted
                                                })
                                                .text_xs()
                                                .flex()
                                                .items_center()
                                                .gap(px(7.))
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if active {
                                                        let request_id = this.runtime.request(
                                                            Method::ResumeSession,
                                                            json!({"sessionId": session_id}),
                                                        );
                                                        this.pending_requests.insert(
                                                            request_id,
                                                            PendingRequest::ResumeSession,
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                        cx.notify();
                                                    } else {
                                                        let _ = launch_gpui_window(
                                                            &workspace,
                                                            &session_id,
                                                        );
                                                    }
                                                }))
                                                .child(
                                                    div()
                                                        .size(px(6.))
                                                        .rounded_full()
                                                        .border_1()
                                                        .border_color(if session.running {
                                                            palette.accent
                                                        } else {
                                                            palette.faint
                                                        })
                                                        .bg(if session.running {
                                                            palette.accent
                                                        } else {
                                                            palette.sidebar
                                                        }),
                                                )
                                                .child(
                                                    div()
                                                        .flex_1()
                                                        .overflow_hidden()
                                                        .child(title),
                                                )
                                                    }),
                                            )
                                            .when(
                                                active && state.navigation.sessions.len() > 5,
                                                |list| {
                                                    list.child(
                                                        div()
                                                            .id("show-more-sessions")
                                                            .role(Role::Button)
                                                            .tab_stop(true)
                                                            .h(px(30.))
                                                            .px(px(6.))
                                                            .text_color(palette.faint)
                                                            .text_xs()
                                                            .flex()
                                                            .items_center()
                                                            .cursor_pointer()
                                                            .on_click(cx.listener(
                                                                |this, _, _, cx| {
                                                                    this.show_all_sessions =
                                                                        !this.show_all_sessions;
                                                                    cx.notify();
                                                                },
                                                            ))
                                                            .child(if show_all_sessions {
                                                                if state.settings.language.as_ref()
                                                                    == "zh-CN"
                                                                {
                                                                    "收起"
                                                                } else {
                                                                    "Show less"
                                                                }
                                                            } else if state
                                                                .settings
                                                                .language
                                                                .as_ref()
                                                                == "zh-CN"
                                                            {
                                                                "展开显示"
                                                            } else {
                                                                "Show more"
                                                            }),
                                                    )
                                                },
                                            ),
                                    )
                                })
                        }),
                ),
        )
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .pt(px(8.))
                .child(
                    div()
                        .id("sidebar-settings")
                        .role(Role::Button)
                        .aria_label(labels.settings)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.muted)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
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
                        .child(div().w(px(16.)).child(icon("settings", 15., palette.muted)))
                        .child(labels.settings)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘,")),
                ),
        )
        .into_any_element()
}

pub(super) fn search_surface(
    state: &AppState,
    input: Entity<TextInput>,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let zh = state.settings.language.as_ref() == "zh-CN";
    div()
        .id("search-surface")
        .role(Role::Search)
        .aria_label(labels.search)
        .flex_1()
        .overflow_y_scroll()
        .bg(palette.paper)
        .px(px(48.))
        .pt(px(70.))
        .flex()
        .flex_col()
        .items_center()
        .child(
            div()
                .w_full()
                .max_w(px(760.))
                .flex()
                .flex_col()
                .gap_3()
                .child(
                    div()
                        .text_2xl()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(labels.search),
                )
                .child(
                    div()
                        .id("search-field")
                        .h(px(52.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .rounded(px(14.))
                        .bg(palette.paper)
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.12, 0.12, 0.07))
                                .blur_radius(px(8.)),
                        ])
                        .flex()
                        .items_center()
                        .pl(px(10.))
                        .child(
                            div()
                                .w(px(22.))
                                .text_color(palette.faint)
                                .text_lg()
                                .child(icon("search", 17., palette.faint)),
                        )
                        .child(input)
                        .child(
                            div()
                                .id("search-submit")
                                .role(Role::Button)
                                .aria_label(labels.search)
                                .tab_stop(true)
                                .h(px(34.))
                                .px_3()
                                .mr_2()
                                .rounded(px(9.))
                                .bg(palette.ink)
                                .text_color(palette.paper)
                                .text_xs()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(AzemWindow::search_click))
                                .child(labels.search),
                        ),
                )
                .when(!state.navigation.search_error.is_empty(), |surface| {
                    surface.child(
                        div()
                            .id("search-error")
                            .role(Role::Alert)
                            .text_color(palette.danger)
                            .text_xs()
                            .child(state.navigation.search_error.to_string()),
                    )
                })
                .children(state.navigation.search_results.iter().enumerate().map(
                    |(index, result)| {
                        let session_id = result
                            .get("sessionId")
                            .or_else(|| result.get("session_id"))
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let workspace = result
                            .get("workspace")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let title = result
                            .get("title")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or(if zh {
                                "未命名会话"
                            } else {
                                "Untitled conversation"
                            })
                            .to_string();
                        let preview = result
                            .get("preview")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let current_workspace = state.workspace.root.to_string();
                        div()
                            .id(("search-result", index))
                            .role(Role::Button)
                            .aria_label(title.clone())
                            .tab_stop(true)
                            .min_h(px(60.))
                            .px_3()
                            .py_2()
                            .rounded(px(10.))
                            .border_1()
                            .border_color(palette.border)
                            .text_color(palette.ink)
                            .flex()
                            .flex_col()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                if workspace.is_empty() || workspace == current_workspace {
                                    let request_id = this.runtime.request(
                                        Method::ResumeSession,
                                        json!({"sessionId": session_id}),
                                    );
                                    this.pending_requests
                                        .insert(request_id, PendingRequest::ResumeSession);
                                    this.state.navigation.surface = Surface::Thread;
                                    cx.notify();
                                } else {
                                    let _ = launch_gpui_window(&workspace, &session_id);
                                }
                            }))
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(title),
                            )
                            .when(!preview.is_empty(), |row| {
                                row.child(div().text_color(palette.muted).text_xs().child(preview))
                            })
                    },
                )),
        )
        .into_any_element()
}
pub(super) fn environment_panel(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    expanded: Option<&str>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mut todo_items = Vec::new();
    if let Some(phases) = state
        .runtime
        .todo
        .get("phases")
        .and_then(serde_json::Value::as_array)
    {
        for phase in phases {
            if let Some(items) = phase.get("items").and_then(serde_json::Value::as_array) {
                todo_items.extend(items.iter().cloned());
            }
        }
    }
    let completed = todo_items
        .iter()
        .filter(|item| {
            matches!(
                item.get("status").and_then(serde_json::Value::as_str),
                Some("completed" | "cancelled")
            )
        })
        .count();
    let branch = if state.workspace.branch.is_empty() {
        "—".to_string()
    } else {
        state.workspace.branch.to_string()
    };
    let history_count = state
        .navigation
        .session_tree
        .get("branches")
        .and_then(serde_json::Value::as_array)
        .map(Vec::len)
        .unwrap_or_default();
    let recap_revision = state
        .runtime
        .recap
        .get("revision")
        .and_then(serde_json::Value::as_i64)
        .map(|revision| format!("r{revision}"))
        .unwrap_or_else(|| "—".to_string());
    let zh = state.settings.language.as_ref() == "zh-CN";
    div()
        .id("environment-panel")
        .role(Role::Region)
        .aria_label(if zh { "环境" } else { "Environment" })
        .absolute()
        .top(px(12.))
        .right(px(12.))
        .bottom(px(12.))
        .w(px(288.))
        .rounded(px(16.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.12, 0.12, 0.12))
                .blur_radius(px(28.)),
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
                .child(div().text_color(palette.faint).text_xs().child(if zh {
                    "环境"
                } else {
                    "Environment"
                }))
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
        .child(environment_nav_row(
            "environment-changes",
            "file-diff",
            labels.changes.to_string(),
            format!(
                "+{}  −{}",
                state.workspace.additions, state.workspace.deletions
            ),
            Some(Surface::Changes),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "environment-local",
            "folder",
            if zh {
                "本地".to_string()
            } else {
                "Local".to_string()
            },
            "⌄".to_string(),
            Some(Surface::Files),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "environment-branch",
            "git-branch",
            branch,
            "⌄".to_string(),
            Some(Surface::Projects),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "environment-services",
            "server",
            if zh {
                "本地服务".to_string()
            } else {
                "Local services".to_string()
            },
            format!(
                "● {}  ⌄",
                state
                    .terminals
                    .sessions
                    .iter()
                    .filter(|session| {
                        session.get("state").and_then(serde_json::Value::as_str) == Some("running")
                    })
                    .count()
            ),
            Some(Surface::Terminal),
            palette,
            cx,
        ))
        .child(environment_divider(palette))
        .child(environment_label(
            if zh { "会话" } else { "Conversation" },
            palette,
        ))
        .when(!todo_items.is_empty(), |panel| {
            panel
                .child(environment_expand_row(
                    "list-todo",
                    labels.plan.to_string(),
                    format!("{completed} / {}  ⌄", todo_items.len()),
                    "plan",
                    expanded == Some("plan"),
                    palette,
                    cx,
                ))
                .when(expanded == Some("plan"), |panel| {
                    panel.child(div().px_2().pb_2().flex().flex_col().children(
                        todo_items.into_iter().map(|item| {
                            let status = item
                                .get("status")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or("pending");
                            let content = item
                                .get("content")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string();
                            div()
                                .min_h(px(28.))
                                .text_color(if status == "completed" {
                                    palette.muted
                                } else {
                                    palette.ink_soft
                                })
                                .text_xs()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(if status == "completed" { "✓" } else { "○" })
                                .child(content)
                        }),
                    ))
                })
        })
        .child(environment_expand_row(
            "git-branch",
            if zh {
                "会话历史".to_string()
            } else {
                "Session history".to_string()
            },
            format!(
                "{}  ⌄",
                if history_count == 0 {
                    "—".to_string()
                } else {
                    history_count.to_string()
                }
            ),
            "history",
            expanded == Some("history"),
            palette,
            cx,
        ))
        .when(expanded == Some("history"), |panel| {
            panel.child(environment_detail(
                pretty_value(&state.navigation.session_tree),
                palette,
            ))
        })
        .child(environment_expand_row(
            "history",
            if zh {
                "回顾".to_string()
            } else {
                "Recap".to_string()
            },
            format!("{recap_revision}  ⌄"),
            "recap",
            expanded == Some("recap"),
            palette,
            cx,
        ))
        .when(expanded == Some("recap"), |panel| {
            panel.child(environment_detail(
                pretty_value(&state.runtime.recap),
                palette,
            ))
        })
        .child(environment_expand_row(
            "link",
            if zh {
                "来源".to_string()
            } else {
                "Sources".to_string()
            },
            format!("{}  ⌄", state.transcript.attachments.len()),
            "sources",
            expanded == Some("sources"),
            palette,
            cx,
        ))
        .when(expanded == Some("sources"), |panel| {
            panel.child(environment_detail(
                serde_json::to_string_pretty(&state.transcript.attachments).unwrap_or_default(),
                palette,
            ))
        })
        .child(environment_divider(palette))
        .child(environment_label(
            if zh { "编辑器" } else { "Editor" },
            palette,
        ))
        .child(environment_nav_row(
            "environment-editor",
            "panels",
            if zh {
                "编辑器视图".to_string()
            } else {
                "Editor view".to_string()
            },
            String::new(),
            Some(Surface::Files),
            palette,
            cx,
        ))
        .child(environment_nav_row(
            "environment-terminal",
            "terminal",
            labels.terminal.to_string(),
            "⌄".to_string(),
            Some(Surface::Terminal),
            palette,
            cx,
        ))
        .into_any_element()
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
            row.on_click(cx.listener(move |this, _, _, cx| {
                this.state.navigation.surface = destination;
                this.request_surface(destination);
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

fn environment_expand_row(
    icon_name: &'static str,
    label: String,
    trailing: String,
    key: &'static str,
    selected: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(key)
        .role(Role::Button)
        .aria_label(label.clone())
        .aria_expanded(selected)
        .tab_stop(true)
        .h(px(38.))
        .px_2()
        .rounded(px(8.))
        .bg(if selected {
            palette.paper_muted
        } else {
            palette.paper
        })
        .text_color(palette.ink)
        .text_xs()
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.paper_muted))
        .on_click(cx.listener(move |this, _, _, cx| {
            this.environment_expanded = if this.environment_expanded.as_deref() == Some(key) {
                None
            } else {
                Some(key.to_string())
            };
            cx.notify();
        }))
        .child(
            div()
                .w(px(18.))
                .child(icon(icon_name, 16., palette.ink_soft)),
        )
        .child(div().flex_1().overflow_hidden().child(label))
        .child(div().text_color(palette.muted).text_xs().child(trailing))
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

fn environment_detail(content: String, palette: ThemePalette) -> gpui::Stateful<gpui::Div> {
    div()
        .id("environment-detail")
        .max_h(px(180.))
        .overflow_y_scroll()
        .mx_2()
        .mb_2()
        .p_2()
        .rounded(px(8.))
        .bg(palette.paper_muted)
        .font_family("SF Mono")
        .text_color(palette.muted)
        .text_size(px(10.))
        .line_height(px(16.))
        .whitespace_normal()
        .child(if content.is_empty() {
            "—".to_string()
        } else {
            content
        })
}

pub(super) fn settings_surface(
    state: &AppState,
    palette: ThemePalette,
    section: &str,
    selected_provider: &str,
    provider_scroll: ScrollHandle,
    model_scroll: ScrollHandle,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let zh = state.settings.language.as_ref() == "zh-CN";
    let nav_groups = [
        (
            if zh { "系统" } else { "SYSTEM" },
            vec![
                (
                    "catalog",
                    if zh { "模型目录" } else { "Model catalog" },
                    "bot",
                ),
                (
                    "routes",
                    if zh { "模型路由" } else { "Model routing" },
                    "git-branch",
                ),
                (
                    "agents",
                    if zh { "子智能体" } else { "Subagents" },
                    "sparkles",
                ),
                (
                    "security",
                    if zh { "安全扫描" } else { "Security scan" },
                    "shield-check",
                ),
            ],
        ),
        (
            if zh { "偏好设置" } else { "PREFERENCES" },
            vec![
                (
                    "governance",
                    if zh { "治理与审批" } else { "Governance" },
                    "sliders-horizontal",
                ),
                ("appearance", if zh { "外观" } else { "Appearance" }, "sun"),
                (
                    "extensions",
                    if zh { "扩展" } else { "Extensions" },
                    "blocks",
                ),
                ("archive", if zh { "归档" } else { "Archive" }, "archive"),
                ("usage", if zh { "用量" } else { "Usage" }, "activity"),
            ],
        ),
    ];
    let (title, description) = match section {
        "routes" => (
            if zh { "模型路由" } else { "Model routing" },
            if zh {
                "检查主代理、团队角色和辅助任务使用的提供商与模型。"
            } else {
                "Inspect providers and models used by the main agent, team roles, and helpers."
            },
        ),
        "agents" => (
            if zh {
                "代理与技能"
            } else {
                "Agents & skills"
            },
            if zh {
                "管理可用的代理角色、技能和运行时能力。"
            } else {
                "Review available agent roles, skills, and runtime capabilities."
            },
        ),
        "security" => (
            if zh { "安全" } else { "Security" },
            if zh {
                "查看安全策略、扫描状态和发现。"
            } else {
                "Review security policy, scan state, and findings."
            },
        ),
        "governance" => (
            if zh { "权限与交付" } else { "Governance" },
            if zh {
                "控制工具审批以及运行中消息的交付方式。"
            } else {
                "Control tool approval and how follow-up messages are delivered."
            },
        ),
        "appearance" => (
            if zh {
                "外观与语言"
            } else {
                "Appearance & language"
            },
            if zh {
                "选择桌面语言并检查可用主题。"
            } else {
                "Choose the desktop language and inspect available themes."
            },
        ),
        "extensions" => (
            if zh { "扩展" } else { "Extensions" },
            if zh {
                "查看技能、插件、MCP 服务和 Hooks。"
            } else {
                "Review skills, plugins, MCP servers, and hooks."
            },
        ),
        "archive" => (
            if zh {
                "上下文与归档"
            } else {
                "Context & archive"
            },
            if zh {
                "检查确定性上下文归档、会话回顾与恢复状态。"
            } else {
                "Inspect deterministic context archives, recaps, and recovery state."
            },
        ),
        "usage" => (
            if zh { "用量" } else { "Usage" },
            if zh {
                "查看当前会话和提供商的用量信号。"
            } else {
                "Review current session and provider usage signals."
            },
        ),
        _ => (
            if zh { "模型目录" } else { "Model catalog" },
            if zh {
                "管理订阅模型与 OpenAI 兼容提供方。凭据只存放在系统钥匙串中。"
            } else {
                "Manage subscription models and OpenAI-compatible providers. Credentials remain in the system keychain."
            },
        ),
    };
    let body = match section {
        "routes" => settings_inventory_body(
            state,
            palette,
            vec![settings_inventory_card(
                if zh { "路由" } else { "Routes" },
                if zh {
                    "运行时当前公开的模型路由。"
                } else {
                    "Model routes currently exposed by the runtime."
                },
                &state.catalogs.routes,
                if zh {
                    "还没有模型路由"
                } else {
                    "No model routes"
                },
                palette,
            )],
        ),
        "agents" => settings_inventory_body(
            state,
            palette,
            vec![
                settings_inventory_card(
                    if zh { "代理类型" } else { "Agent types" },
                    if zh {
                        "可供 Main、Team 和 Subagent 使用的角色。"
                    } else {
                        "Roles available to Main, Team, and Subagent runs."
                    },
                    &state.catalogs.agent_types,
                    if zh {
                        "还没有代理类型"
                    } else {
                        "No agent types"
                    },
                    palette,
                ),
                settings_inventory_card(
                    if zh { "技能" } else { "Skills" },
                    if zh {
                        "当前可激活的运行时技能。"
                    } else {
                        "Runtime skills available for activation."
                    },
                    &state.catalogs.skills,
                    if zh { "还没有技能" } else { "No skills" },
                    palette,
                ),
            ],
        ),
        "security" => settings_security_body(state, palette, zh, cx),
        "governance" => settings_governance_body(state, palette, zh, cx),
        "appearance" => settings_appearance_body(state, palette, zh, cx),
        "extensions" => settings_extensions_body(state, palette, zh, cx),
        "archive" => settings_archive_body(state, palette, zh, cx),
        "usage" => settings_usage_body(state, palette, zh, cx),
        _ => settings_catalog_body(
            state,
            palette,
            zh,
            selected_provider,
            provider_scroll,
            model_scroll,
            cx,
        ),
    };
    div()
        .id("settings-surface")
        .role(Role::Region)
        .aria_label(if zh { "设置" } else { "Settings" })
        .flex_1()
        .min_w_0()
        .bg(palette.paper)
        .rounded(px(20.))
        .overflow_hidden()
        .flex()
        .child(
            div()
                .id("settings-navigation")
                .role(Role::Navigation)
                .aria_label(if zh {
                    "设置分类"
                } else {
                    "Settings categories"
                })
                .w(px(216.))
                .h_full()
                .border_r_1()
                .border_color(palette.border)
                .bg(palette.sidebar)
                .px_3()
                .pt(px(16.))
                .pb(px(14.))
                .flex()
                .flex_col()
                .gap_3()
                .child(
                    div()
                        .id("settings-back")
                        .role(Role::Button)
                        .aria_label(if zh {
                            "返回工作台"
                        } else {
                            "Back to workspace"
                        })
                        .tab_stop(true)
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink)
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.settings_open = false;
                            cx.notify();
                        }))
                        .child("←")
                        .child(if zh {
                            "返回工作台"
                        } else {
                            "Back to workspace"
                        }),
                )
                .child(
                    div()
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .text_color(palette.faint)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 13., palette.faint))
                        .child(if zh {
                            "搜索设置…"
                        } else {
                            "Search settings…"
                        }),
                )
                .children(nav_groups.into_iter().map(|(group, entries)| {
                    div()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .h(px(24.))
                                .px_2()
                                .text_color(palette.faint)
                                .text_size(px(10.))
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .flex()
                                .items_center()
                                .child(group),
                        )
                        .children(entries.into_iter().map(|(id, label, icon_name)| {
                            settings_navigation_item(
                                id,
                                label,
                                icon_name,
                                section == id,
                                palette,
                                cx,
                            )
                        }))
                }))
                .child(div().flex_1())
                .child(
                    div()
                        .h(px(32.))
                        .px_2()
                        .border_t_1()
                        .border_color(palette.border)
                        .text_color(palette.faint)
                        .text_size(px(9.))
                        .flex()
                        .items_center()
                        .child(if zh {
                            "配置自动保存到本机"
                        } else {
                            "Settings save locally"
                        })
                        .child(div().flex_1())
                        .child(format!("Azem v{}", env!("CARGO_PKG_VERSION"))),
                ),
        )
        .child(
            div()
                .id("settings-content")
                .flex_1()
                .min_w_0()
                .h_full()
                .when(section == "catalog", |content| content.overflow_hidden())
                .when(section != "catalog", |content| content.overflow_y_scroll())
                .px(px(48.))
                .pt(px(42.))
                .pb(px(if section == "catalog" { 16. } else { 64. }))
                .flex()
                .flex_col()
                .gap_5()
                .child(
                    div()
                        .flex()
                        .items_start()
                        .gap_4()
                        .child(
                            div()
                                .flex_1()
                                .flex()
                                .flex_col()
                                .gap_2()
                                .child(
                                    div()
                                        .text_color(palette.ink)
                                        .text_size(px(22.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(title),
                                )
                                .child(
                                    div()
                                        .max_w(px(680.))
                                        .text_color(palette.muted)
                                        .text_sm()
                                        .line_height(px(21.))
                                        .child(description),
                                ),
                        )
                        .when(section == "catalog", |header| {
                            header.child(
                                div()
                                    .id("add-model-provider")
                                    .role(Role::Button)
                                    .aria_label(if zh {
                                        "添加提供方"
                                    } else {
                                        "Add provider"
                                    })
                                    .tab_stop(true)
                                    .h(px(38.))
                                    .px_3()
                                    .rounded(px(9.))
                                    .bg(palette.ink)
                                    .text_color(palette.paper)
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .cursor_pointer()
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.settings_provider = this
                                            .state
                                            .catalogs
                                            .providers
                                            .iter()
                                            .find(|provider| {
                                                !provider
                                                    .get("enabled")
                                                    .and_then(serde_json::Value::as_bool)
                                                    .unwrap_or(false)
                                            })
                                            .and_then(|provider| provider.get("id"))
                                            .and_then(serde_json::Value::as_str)
                                            .map(str::to_string);
                                        if this.settings_provider.is_none() {
                                            this.refresh_model_catalog();
                                        }
                                        cx.notify();
                                    }))
                                    .child("+")
                                    .child(if zh {
                                        "添加提供方"
                                    } else {
                                        "Add provider"
                                    }),
                            )
                        }),
                )
                .child(body),
        )
        .into_any_element()
}

fn settings_navigation_item(
    id: &'static str,
    label: &'static str,
    icon_name: &'static str,
    selected: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(format!("settings-nav-{id}"))
        .role(Role::Button)
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(true)
        .h(px(34.))
        .px_2()
        .rounded(px(8.))
        .bg(pick(selected, palette.hover, palette.sidebar))
        .text_color(pick(selected, palette.ink, palette.muted))
        .text_sm()
        .font_weight(pick(
            selected,
            gpui::FontWeight::MEDIUM,
            gpui::FontWeight::NORMAL,
        ))
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.hover))
        .on_click(cx.listener(move |this, _, _, cx| {
            this.settings_section = id.to_string();
            cx.notify();
        }))
        .child(icon(
            icon_name,
            15.,
            pick(selected, palette.ink, palette.faint),
        ))
        .child(label)
        .into_any_element()
}

fn settings_inventory_body(
    _state: &AppState,
    _palette: ThemePalette,
    cards: Vec<gpui::AnyElement>,
) -> gpui::AnyElement {
    div()
        .w_full()
        .max_w(px(780.))
        .flex()
        .flex_col()
        .gap_4()
        .children(cards)
        .into_any_element()
}

fn settings_catalog_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    selected_provider: &str,
    provider_scroll: ScrollHandle,
    model_scroll: ScrollHandle,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mut provider_inventory = state.catalogs.providers.clone();
    provider_inventory.sort_by_key(|provider| {
        let id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let name = provider_display_name(provider, id);
        (
            provider_preference_rank(id, &name),
            name.to_ascii_lowercase(),
        )
    });
    let selected = state
        .catalogs
        .providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(selected_provider)
        })
        .or_else(|| {
            state.catalogs.providers.iter().find(|provider| {
                provider
                    .get("enabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
                    || provider
                        .get("models")
                        .and_then(serde_json::Value::as_array)
                        .is_some_and(|models| !models.is_empty())
            })
        })
        .or_else(|| state.catalogs.providers.first())
        .cloned();
    let provider_rows = provider_inventory
        .iter()
        .enumerate()
        .filter_map(|(index, provider)| {
            let id = provider
                .get("id")
                .and_then(serde_json::Value::as_str)?
                .to_string();
            let display_name = provider_display_name(provider, &id);
            let logo_id = provider_logo_id(provider, &id);
            let model_count = provider
                .get("models")
                .and_then(serde_json::Value::as_array)
                .map(Vec::len)
                .unwrap_or_default();
            let backend = provider
                .get("backend")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_string();
            let enabled = provider
                .get("enabled")
                .and_then(serde_json::Value::as_bool)
                .unwrap_or(false);
            let subtitle = provider_list_subtitle(provider, &id, &backend, model_count, zh);
            let quota = provider
                .get("quotaUsedPercent")
                .and_then(serde_json::Value::as_f64)
                .map(|used| format_percentage((100. - used).max(0.)))
                .unwrap_or_default();
            let active = selected
                .as_ref()
                .and_then(|provider| provider.get("id"))
                .and_then(serde_json::Value::as_str)
                == Some(id.as_str());
            let selected_id = id.clone();
            Some(
                div()
                    .id(("settings-provider", index))
                    .role(Role::Button)
                    .aria_label(display_name.clone())
                    .aria_selected(active)
                    .tab_stop(true)
                    .min_h(px(58.))
                    .px_3()
                    .rounded(px(8.))
                    .border_l_2()
                    .border_color(if active {
                        palette.accent
                    } else {
                        rgba(0x00000000)
                    })
                    .bg(if active {
                        palette.accent_soft
                    } else {
                        palette.paper
                    })
                    .flex()
                    .items_center()
                    .gap_3()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.settings_provider = Some(selected_id.clone());
                        this.settings_model_scroll.scroll_to_top_of_item(0);
                        cx.notify();
                    }))
                    .child(
                        div()
                            .size(px(27.))
                            .rounded(px(7.))
                            .bg(palette.paper_muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(provider_logo(&logo_id, 16., palette.ink)),
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
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(display_name),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .text_color(palette.faint)
                                    .text_size(px(10.))
                                    .child(subtitle),
                            ),
                    )
                    .child(
                        div()
                            .text_color(if enabled {
                                palette.positive
                            } else {
                                palette.faint
                            })
                            .text_size(px(10.))
                            .child(if quota.is_empty() {
                                if enabled {
                                    "•".to_string()
                                } else if zh {
                                    "未启用".to_string()
                                } else {
                                    "Off".to_string()
                                }
                            } else {
                                quota
                            }),
                    )
                    .into_any_element(),
            )
        })
        .collect::<Vec<_>>();
    let detail = if let Some(provider) = selected {
        let provider_id = provider
            .get("id")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let detail_name = provider_detail_name(&provider, &provider_id);
        let logo_id = provider_logo_id(&provider, &provider_id);
        let account = provider
            .get("accountLabel")
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .or_else(|| {
                provider
                    .get("credentialSource")
                    .and_then(serde_json::Value::as_str)
            })
            .unwrap_or_default()
            .to_string();
        let plan = provider
            .get("accountPlan")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let enabled = provider
            .get("enabled")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_available = provider
            .get("quotaAvailable")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_remaining = provider
            .get("quotaUsedPercent")
            .and_then(serde_json::Value::as_f64)
            .map(|used| (100. - used).clamp(0., 100.))
            .unwrap_or(0.);
        let quota_period = provider
            .get("quotaPeriod")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string();
        let quota_balance = provider
            .get("quotaBalance")
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .unwrap_or("0.00")
            .to_string();
        let models = provider
            .get("models")
            .and_then(serde_json::Value::as_array)
            .cloned()
            .unwrap_or_default();
        let enabled_count = models
            .iter()
            .filter(|model| {
                !model
                    .get("disabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false)
            })
            .count();
        let session_id = state.navigation.current_session_id.to_string();
        let toggle_session = session_id.clone();
        let mut toggle_provider = provider.clone();
        if let Some(fields) = toggle_provider.as_object_mut() {
            fields.insert("enabled".to_string(), serde_json::Value::Bool(!enabled));
        }
        let model_cards = models
            .iter()
            .enumerate()
            .map(|(index, model)| {
                let model_id = model
                    .get("id")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                let model_name = ["familyName", "name", "displayName"]
                    .into_iter()
                    .find_map(|key| {
                        model
                            .get(key)
                            .and_then(serde_json::Value::as_str)
                            .filter(|value| !value.trim().is_empty())
                    })
                    .unwrap_or(&model_id)
                    .to_string();
                let disabled = model
                    .get("disabled")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false);
                let capabilities = model
                    .get("capabilities")
                    .and_then(serde_json::Value::as_array)
                    .map(|values| {
                        values
                            .iter()
                            .filter_map(serde_json::Value::as_str)
                            .take(8)
                            .map(str::to_string)
                            .collect::<Vec<_>>()
                    })
                    .unwrap_or_default();
                let provider_target = provider_id.clone();
                let model_target = model_id.clone();
                let session_target = session_id.clone();
                div()
                    .id(("provider-model-card", index))
                    .w(px(302.))
                    .h(px(118.))
                    .p_3()
                    .rounded(px(11.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex()
                    .flex_col()
                    .gap_2()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(provider_logo(&logo_id, 14., palette.muted))
                            .child(
                                div()
                                    .min_w_0()
                                    .flex_1()
                                    .truncate()
                                    .text_color(if disabled { palette.muted } else { palette.ink })
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(model_name),
                            )
                            .child(
                                div()
                                    .id(("model-enabled", index))
                                    .role(Role::Button)
                                    .aria_label(if disabled {
                                        if zh { "启用模型" } else { "Enable model" }
                                    } else if zh {
                                        "停用模型"
                                    } else {
                                        "Disable model"
                                    })
                                    .aria_selected(!disabled)
                                    .tab_stop(true)
                                    .w(px(36.))
                                    .h(px(20.))
                                    .rounded_full()
                                    .bg(if disabled {
                                        palette.border_strong
                                    } else {
                                        palette.positive
                                    })
                                    .p(px(2.))
                                    .flex()
                                    .justify_end()
                                    .when(disabled, |toggle| toggle.justify_start())
                                    .cursor_pointer()
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime.request(
                                            Method::Execute,
                                            json!({
                                                "kind": "set_model_enabled",
                                                "sessionId": session_target,
                                                "target": provider_target,
                                                "name": model_target,
                                                "decision": disabled.to_string(),
                                            }),
                                        );
                                        cx.notify();
                                    }))
                                    .child(div().size(px(16.)).rounded_full().bg(palette.paper)),
                            ),
                    )
                    .child(div().flex_1())
                    .when(!capabilities.is_empty(), |card| {
                        card.child(settings_model_capabilities(&capabilities, palette))
                    })
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        let refresh_session = session_id.clone();
        div()
            .min_w_0()
            .flex_1()
            .h_full()
            .min_h_0()
            .flex()
            .flex_col()
            .gap_3()
            .child(
                div()
                    .min_h(px(68.))
                    .px_4()
                    .rounded(px(12.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .size(px(32.))
                            .rounded(px(8.))
                            .bg(palette.paper_muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(provider_logo(&logo_id, 19., palette.ink)),
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
                                    .text_color(palette.ink)
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(detail_name),
                            )
                            .when(!account.is_empty(), |summary| {
                                summary.child(
                                    div()
                                        .truncate()
                                        .text_color(palette.faint)
                                        .text_xs()
                                        .child(account),
                                )
                            }),
                    )
                    .when(!plan.is_empty(), |summary| {
                        summary.child(
                            div()
                                .px_2()
                                .py_1()
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .text_color(palette.muted)
                                .text_size(px(10.))
                                .child(plan),
                        )
                    })
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(
                                div()
                                    .text_color(if enabled {
                                        palette.positive
                                    } else {
                                        palette.faint
                                    })
                                    .text_xs()
                                    .child(if enabled {
                                        if zh { "可用" } else { "Available" }
                                    } else if zh {
                                        "未启用"
                                    } else {
                                        "Disabled"
                                    }),
                            )
                            .child(
                                div()
                                    .id("provider-enabled")
                                    .role(Role::Button)
                                    .aria_label(if enabled {
                                        if zh { "停用提供方" } else { "Disable provider" }
                                    } else if zh {
                                        "启用提供方"
                                    } else {
                                        "Enable provider"
                                    })
                                    .aria_selected(enabled)
                                    .tab_stop(true)
                                    .w(px(38.))
                                    .h(px(22.))
                                    .rounded_full()
                                    .bg(if enabled {
                                        palette.positive
                                    } else {
                                        palette.border_strong
                                    })
                                    .p(px(2.))
                                    .flex()
                                    .justify_end()
                                    .when(!enabled, |toggle| toggle.justify_start())
                                    .cursor_pointer()
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime.request(
                                            Method::Execute,
                                            json!({
                                                "kind": "set_model_provider",
                                                "sessionId": toggle_session,
                                                "provider": toggle_provider,
                                            }),
                                        );
                                        cx.notify();
                                    }))
                                    .child(
                                        div()
                                            .size(px(18.))
                                            .rounded_full()
                                            .bg(palette.paper),
                                    ),
                            ),
                    ),
            )
            .when(quota_available, |detail| {
                detail.child(
                    div()
                        .rounded(px(12.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .p_4()
                        .flex()
                        .flex_col()
                        .gap_3()
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .flex_1()
                                        .text_color(palette.ink)
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(if zh {
                                            format!("每周额度 {:.0}% 剩余", quota_remaining)
                                        } else {
                                            format!("Weekly allowance {:.0}% remaining", quota_remaining)
                                        }),
                                )
                                .when(!quota_period.is_empty(), |row| {
                                    row.child(
                                        div()
                                            .text_color(palette.faint)
                                            .text_xs()
                                            .child(quota_period),
                                    )
                                }),
                        )
                        .child(
                            div()
                                .w_full()
                                .h(px(6.))
                                .rounded_full()
                                .bg(palette.paper_muted)
                                .overflow_hidden()
                                .child(
                                    div()
                                        .w(px((quota_remaining as f32 / 100.) * 560.))
                                        .h_full()
                                        .rounded_full()
                                        .bg(palette.positive),
                                ),
                        )
                        .child(div().h(px(1.)).bg(palette.border))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .flex_1()
                                        .text_color(palette.muted)
                                        .text_xs()
                                        .child(if zh { "额外额度" } else { "Extra credits" }),
                                )
                                .child(
                                    div()
                                        .text_color(palette.positive)
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(format!("US${quota_balance}")),
                                ),
                        ),
                )
            })
            .child(
                div()
                    .rounded(px(12.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .flex_col()
                    .overflow_hidden()
                    .child(
                        div()
                            .h(px(54.))
                            .px_4()
                            .flex()
                            .items_center()
                            .child(
                                div()
                                    .flex_1()
                                    .flex()
                                    .flex_col()
                                    .gap(px(2.))
                                    .child(
                                        div()
                                            .text_color(palette.ink)
                                            .text_sm()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(if zh { "模型" } else { "Models" }),
                                    )
                                    .child(
                                        div()
                                            .text_color(palette.faint)
                                            .text_xs()
                                            .child(if zh {
                                                "启用后可在模型路由与输入框中使用。"
                                            } else {
                                                "Enabled models can be assigned in routes and the composer."
                                            }),
                                    ),
                            )
                            .child(
                                div()
                                    .px_2()
                                    .py_1()
                                    .rounded_full()
                                    .bg(palette.paper_muted)
                                    .text_color(palette.muted)
                                    .text_xs()
                                    .child(format!("{enabled_count} / {}", models.len())),
                            )
                            .child(
                                div()
                                    .id("refresh-provider-models")
                                    .role(Role::Button)
                                    .aria_label(if zh { "获取模型" } else { "Fetch models" })
                                    .tab_stop(true)
                                    .h(px(32.))
                                    .px_2()
                                    .rounded(px(8.))
                                    .border_1()
                                    .border_color(palette.border)
                                    .text_color(palette.muted)
                                    .text_xs()
                                    .flex()
                                    .items_center()
                                    .gap_1()
                                    .cursor_pointer()
                                    .hover(move |style| style.bg(palette.hover))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime.request(
                                            Method::Execute,
                                            json!({"kind": "list_models", "sessionId": refresh_session}),
                                        );
                                        this.refresh_model_catalog();
                                        cx.notify();
                                    }))
                                    .child(icon("rotate-ccw", 13., palette.muted))
                                    .child(if zh { "获取模型" } else { "Fetch models" }),
                            ),
                    )
                    .child(
                        div()
                            .h(px(38.))
                            .px_4()
                            .border_t_1()
                            .border_b_1()
                            .border_color(palette.border)
                            .text_color(palette.faint)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(icon("search", 13., palette.faint))
                            .child(if zh {
                                "搜索模型系列、版本或原始 ID…"
                            } else {
                                "Search model family, version, or raw ID…"
                            }),
                    )
                    .child(
                        div()
                            .id("provider-model-grid")
                            .track_scroll(&model_scroll)
                            .h(px(402.))
                            .overflow_y_scroll()
                            .p_3()
                            .flex()
                            .flex_wrap()
                            .gap_3()
                            .when(model_cards.is_empty(), |grid| {
                                grid.child(
                                    div()
                                        .h(px(96.))
                                        .w_full()
                                        .text_color(palette.faint)
                                        .text_sm()
                                        .flex()
                                        .items_center()
                                        .justify_center()
                                        .child(if zh {
                                            "此提供商还没有模型"
                                        } else {
                                            "This provider has no models"
                                        }),
                                )
                            })
                            .children(model_cards),
                    ),
            )
            .into_any_element()
    } else {
        settings_empty_card(
            if zh {
                "还没有配置模型提供商"
            } else {
                "No model providers are configured"
            },
            palette,
        )
    };
    let remaining_provider_count = state.catalogs.providers.len().saturating_sub(31);
    div()
        .w_full()
        .min_h_0()
        .flex_1()
        .flex()
        .items_start()
        .gap_4()
        .child(
            div()
                .id("provider-list")
                .h(px(700.))
                .w(px(252.))
                .min_h_0()
                .overflow_hidden()
                .flex_shrink_0()
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .p_2()
                .flex()
                .flex_col()
                .child(
                    div()
                        .h(px(36.))
                        .px_2()
                        .mb_1()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper_muted)
                        .text_color(palette.faint)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 13., palette.faint))
                        .child(if zh {
                            "搜索提供商"
                        } else {
                            "Search providers"
                        }),
                )
                .child(
                    div()
                        .id("provider-list-scroll")
                        .track_scroll(&provider_scroll)
                        .h(px(610.))
                        .overflow_y_scroll()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .when(provider_rows.is_empty(), |list| {
                            list.child(
                                div()
                                    .h(px(100.))
                                    .text_color(palette.faint)
                                    .text_sm()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .child(if zh {
                                        "还没有提供商"
                                    } else {
                                        "No providers"
                                    }),
                            )
                        })
                        .children(provider_rows),
                )
                .child(div().flex_1())
                .when(remaining_provider_count > 0, |list| {
                    list.child(
                        div()
                            .h(px(28.))
                            .border_t_1()
                            .border_color(palette.border)
                            .text_color(palette.faint)
                            .text_size(px(10.))
                            .flex()
                            .items_center()
                            .child(if zh {
                                format!("向下滚动加载剩余 {remaining_provider_count} 个提供商")
                            } else {
                                format!("Scroll for {remaining_provider_count} more providers")
                            }),
                    )
                }),
        )
        .child(detail)
        .into_any_element()
}

fn provider_display_name(provider: &serde_json::Value, fallback: &str) -> String {
    ["displayName", "name"]
        .into_iter()
        .find_map(|key| {
            provider
                .get(key)
                .and_then(serde_json::Value::as_str)
                .filter(|value| !value.trim().is_empty())
        })
        .unwrap_or(fallback)
        .to_string()
}

fn provider_logo_id(provider: &serde_json::Value, fallback: &str) -> String {
    provider
        .get("modelsDevId")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(fallback)
        .to_string()
}

fn provider_detail_name(provider: &serde_json::Value, provider_id: &str) -> String {
    match provider_id {
        "chatgpt" => "ChatGPT".to_string(),
        "grok" => "Grok".to_string(),
        "cursor" => "Cursor".to_string(),
        _ => provider_display_name(provider, provider_id),
    }
}

fn provider_preference_rank(provider_id: &str, display_name: &str) -> usize {
    let identity = format!(
        "{} {}",
        provider_id.to_ascii_lowercase(),
        display_name.to_ascii_lowercase()
    );
    [
        "chatgpt",
        "grok",
        "cursor",
        "deepseek",
        "kimi for coding",
        "opencode go",
        "openrouter",
        "abacus",
        "abliteration ai",
        "ai-router",
        "ai21 labs",
    ]
    .iter()
    .position(|preferred| identity.contains(preferred))
    .unwrap_or(usize::MAX)
}

fn provider_list_subtitle(
    provider: &serde_json::Value,
    provider_id: &str,
    backend: &str,
    model_count: usize,
    zh: bool,
) -> String {
    let subscription = provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    if !subscription {
        return format!(
            "{} · {} {}",
            backend,
            model_count,
            localized(zh, "个模型", "models")
        );
    }
    let plan = provider
        .get("accountPlan")
        .and_then(serde_json::Value::as_str)
        .map(|plan| format_subscription_plan(provider_id, plan))
        .filter(|plan| !plan.is_empty())
        .unwrap_or_else(|| {
            match provider_id {
                "chatgpt" => "Pro 20x",
                "grok" => "SuperGrokPro",
                "cursor" => "Cursor Ultra",
                _ => "",
            }
            .to_string()
        });
    let balance = provider
        .get("quotaBalance")
        .and_then(serde_json::Value::as_str)
        .map(format_credit_balance)
        .unwrap_or_default();
    if provider_id == "chatgpt" && !balance.is_empty() {
        return format!("{plan} · {} {balance}", localized(zh, "额外", "extra"));
    }
    plan
}

fn format_subscription_plan(provider_id: &str, plan: &str) -> String {
    match (provider_id, plan.trim().to_ascii_lowercase().as_str()) {
        ("chatgpt", "pro") => "Pro 20x".to_string(),
        ("cursor", "ultra") => "Cursor Ultra".to_string(),
        (_, _) => plan.to_string(),
    }
}

fn format_percentage(value: f64) -> String {
    if (value - value.round()).abs() < 0.05 {
        format!("{value:.0}%")
    } else {
        format!("{value:.1}%")
    }
}

fn format_credit_balance(balance: &str) -> String {
    balance
        .parse::<f64>()
        .map(|value| {
            if (value - value.round()).abs() < 0.005 {
                format!("{value:.0}")
            } else {
                format!("{value:.2}")
            }
        })
        .unwrap_or_else(|_| balance.to_string())
}

fn settings_model_capabilities(capabilities: &[String], palette: ThemePalette) -> gpui::Div {
    div()
        .flex()
        .items_center()
        .gap_1()
        .children(capabilities.iter().map(|capability| {
            let icon_name = match capability.as_str() {
                "tools" | "tool_call" | "tool-call" => "wrench",
                "reasoning" => "brain",
                "structured_output" | "structured-output" => "braces",
                "temperature" => "type",
                "chat" | "messages" => "message-square-text",
                "image" | "vision" => "image",
                "audio" => "audio-lines",
                _ => "layers",
            };
            div()
                .size(px(22.))
                .rounded(px(6.))
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .justify_center()
                .child(icon(icon_name, 12., palette.faint))
        }))
}

fn localized(zh: bool, chinese: &'static str, english: &'static str) -> &'static str {
    if zh { chinese } else { english }
}

fn pick<T>(condition: bool, yes: T, no: T) -> T {
    if condition { yes } else { no }
}

fn settings_governance_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    settings_inventory_body(
        state,
        palette,
        vec![
            settings_approval_card(state, palette, zh, &session_id, cx),
            settings_delivery_card(state, palette, zh, &session_id, cx),
        ],
    )
}

fn settings_approval_card(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    session_id: &str,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let options = [
        ("prompt", localized(zh, "逐次确认", "Ask")),
        ("auto_review", localized(zh, "自动审核", "Auto review")),
        ("yolo", "YOLO"),
    ];
    let controls = options
        .into_iter()
        .enumerate()
        .map(|(id, (value, label))| {
            settings_action_button(
                id,
                label,
                state.connection.connected,
                state.settings.approval_mode.as_ref() == value,
                cx,
                json!({"kind": "set_approval_mode", "target": value, "sessionId": session_id}),
            )
        })
        .collect();
    settings_control_card(
        localized(zh, "工具审批", "Tool approval"),
        localized(
            zh,
            "决定每个工具调用执行前的审核方式。",
            "Choose how tool calls are reviewed before execution.",
        ),
        controls,
        palette,
    )
}

fn settings_delivery_card(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    session_id: &str,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let options = [
        ("queue", localized(zh, "排队跟进", "Queue follow-ups")),
        ("guide", localized(zh, "立即引导", "Guide immediately")),
    ];
    let controls = options
        .into_iter()
        .enumerate()
        .map(|(index, (value, label))| {
            settings_action_button(
                index + 3,
                label,
                state.connection.connected,
                state.settings.queue_mode.as_ref() == value,
                cx,
                json!({"kind": "set_queue_mode", "target": value, "sessionId": session_id}),
            )
        })
        .collect();
    settings_control_card(
        localized(zh, "消息交付", "Message delivery"),
        localized(
            zh,
            "运行进行中时，新的消息可以排队或立即引导当前任务。",
            "While a run is active, new messages can queue or guide the current task immediately.",
        ),
        controls,
        palette,
    )
}

fn settings_appearance_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    settings_inventory_body(
        state,
        palette,
        vec![
            settings_language_card(state, palette, zh, &session_id, cx),
            settings_inventory_card(
                localized(zh, "主题", "Themes"),
                localized(
                    zh,
                    "运行时公开的可用主题。系统外观仍决定浅色或深色调色板。",
                    "Themes exposed by the runtime. System appearance still selects light or dark palette.",
                ),
                &state.catalogs.themes,
                localized(zh, "使用系统主题", "Using system theme"),
                palette,
            ),
            settings_scalar_card(
                localized(zh, "当前外观", "Current appearance"),
                localized(
                    zh,
                    "持久化的外观首选项。",
                    "Persisted appearance preferences.",
                ),
                &state.settings.appearance,
                palette,
            ),
        ],
    )
}

fn settings_language_card(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    session_id: &str,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let options = [("en", "English"), ("zh-CN", "简体中文")];
    let controls = options
        .into_iter()
        .enumerate()
        .map(|(index, (value, label))| {
            settings_action_button(
                index + 10,
                label,
                state.connection.connected,
                state.settings.language.as_ref() == value,
                cx,
                json!({"kind": "set_language", "target": value, "sessionId": session_id}),
            )
        })
        .collect();
    settings_control_card(
        localized(zh, "语言", "Language"),
        localized(
            zh,
            "界面语言立即应用到所有原生桌面控件。",
            "The interface language applies immediately to native desktop controls.",
        ),
        controls,
        palette,
    )
}

fn settings_extensions_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let mcp = nested_inventory(&state.catalogs.mcp, &["servers", "entries", "items"]);
    let hooks = nested_inventory(&state.catalogs.hooks, &["entries", "hooks", "items"]);
    let actions = div()
        .flex()
        .items_center()
        .gap_2()
        .child(settings_action_button(
            20,
            localized(zh, "刷新扩展", "Refresh extensions"),
            state.connection.connected,
            false,
            cx,
            json!({"kind": "list_plugins", "sessionId": session_id}),
        ))
        .child(settings_action_button(
            21,
            localized(zh, "刷新 MCP", "Refresh MCP"),
            state.connection.connected,
            false,
            cx,
            json!({"kind": "refresh_mcp", "sessionId": session_id}),
        ));
    let cards = [
        settings_inventory_card(
            localized(zh, "插件", "Plugins"),
            localized(
                zh,
                "已导入到 Azem 运行时的插件包。",
                "Plugin packages imported into the Azem runtime.",
            ),
            &state.catalogs.plugins,
            localized(zh, "还没有插件", "No plugins"),
            palette,
        ),
        settings_inventory_card(
            localized(zh, "技能", "Skills"),
            localized(
                zh,
                "当前可激活的技能目录。",
                "Skills currently available for activation.",
            ),
            &state.catalogs.skills,
            localized(zh, "还没有技能", "No skills"),
            palette,
        ),
        settings_inventory_card(
            "MCP",
            localized(
                zh,
                "已连接或已配置的 MCP 服务。",
                "Connected or configured MCP services.",
            ),
            &mcp,
            localized(zh, "还没有 MCP 服务", "No MCP servers"),
            palette,
        ),
        settings_inventory_card(
            "Hooks",
            localized(
                zh,
                "插件 Hooks 默认不受信任；状态来自运行时目录。",
                "Plugin hooks remain untrusted by default; status comes from the runtime catalog.",
            ),
            &hooks,
            localized(zh, "还没有 Hooks", "No hooks"),
            palette,
        ),
    ];
    div()
        .w_full()
        .max_w(px(780.))
        .flex()
        .flex_col()
        .gap_4()
        .child(actions)
        .children(cards)
        .into_any_element()
}

fn settings_security_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let policy = settings_scalar_card(
        localized(zh, "安全策略", "Security policy"),
        localized(
            zh,
            "当前会话使用的宿主安全设置。",
            "Host security settings applied to the current session.",
        ),
        &state.security.config,
        palette,
    );
    let status = settings_rows_card(
        localized(zh, "扫描状态", "Scan status"),
        localized(
            zh,
            "原生安全扫描保留独立的扫描和发现记录。",
            "Native security scanning keeps independent scan and finding records.",
        ),
        vec![
            (
                localized(zh, "扫描", "Scans").to_string(),
                state.security.scans.len().to_string(),
            ),
            (
                localized(zh, "发现", "Findings").to_string(),
                state.security.findings.len().to_string(),
            ),
        ],
        palette,
    );
    settings_inventory_body(
        state,
        palette,
        vec![
            policy,
            status,
            settings_surface_button(
                "open-security-surface",
                localized(zh, "打开安全扫描", "Open security scanning"),
                "shield-check",
                Surface::Security,
                palette,
                cx,
            ),
        ],
    )
}

fn settings_archive_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let action = settings_action_button(
        30,
        localized(zh, "压缩当前会话", "Compact current session"),
        state.connection.connected && !session_id.is_empty(),
        false,
        cx,
        json!({"kind": "compact", "target": session_id, "sessionId": session_id}),
    );
    settings_inventory_body(
        state,
        palette,
        vec![
            div().flex().items_center().child(action).into_any_element(),
            settings_scalar_card(
                localized(zh, "上下文配置", "Context profile"),
                localized(
                    zh,
                    "主代理当前使用的上下文窗口与归档载体。",
                    "Context window and archive carrier currently used by the main agent.",
                ),
                &state.runtime.context_profile,
                palette,
            ),
            settings_scalar_card(
                localized(zh, "会话回顾", "Session recap"),
                localized(
                    zh,
                    "独立回顾写入器保存的连续性状态。",
                    "Continuity state maintained by the independent recap writer.",
                ),
                &state.runtime.recap,
                palette,
            ),
            settings_scalar_card(
                localized(zh, "归档", "Archive"),
                localized(
                    zh,
                    "确定性归档和恢复设置。",
                    "Deterministic archive and recovery settings.",
                ),
                &state.settings.archive,
                palette,
            ),
            settings_scalar_card(
                localized(zh, "恢复", "Recovery"),
                localized(
                    zh,
                    "当前持久化恢复状态。",
                    "Current durable recovery state.",
                ),
                &state.settings.recovery,
                palette,
            ),
        ],
    )
}

fn settings_usage_body(
    state: &AppState,
    palette: ThemePalette,
    zh: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    settings_inventory_body(
        state,
        palette,
        vec![
            settings_surface_button(
                "open-usage-surface",
                localized(zh, "打开完整用量报告", "Open full usage report"),
                "chart",
                Surface::Usage,
                palette,
                cx,
            ),
            settings_scalar_card(
                localized(zh, "当前用量", "Current usage"),
                localized(
                    zh,
                    "最近一次完成请求与会话的用量信号。",
                    "Usage signals from the latest completed request and session.",
                ),
                &state.settings.usage,
                palette,
            ),
        ],
    )
}

fn settings_surface_button(
    id: &'static str,
    label: &'static str,
    icon_name: &'static str,
    destination: Surface,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label)
        .tab_stop(true)
        .h(px(38.))
        .px_3()
        .rounded(px(8.))
        .border_1()
        .border_color(palette.border_strong)
        .text_color(palette.ink)
        .text_sm()
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.hover))
        .on_click(cx.listener(move |this, _, _, cx| {
            this.state.navigation.surface = destination;
            this.settings_open = false;
            cx.notify();
        }))
        .child(icon(icon_name, 15., palette.ink))
        .child(label)
        .into_any_element()
}

fn settings_control_card(
    title: &'static str,
    description: &'static str,
    controls: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .p_4()
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(
                    div()
                        .text_color(palette.muted)
                        .text_xs()
                        .line_height(px(18.))
                        .child(description),
                ),
        )
        .child(div().flex().flex_wrap().gap_2().children(controls))
        .into_any_element()
}

fn settings_inventory_card(
    title: &'static str,
    description: &'static str,
    values: &[serde_json::Value],
    empty: &'static str,
    palette: ThemePalette,
) -> gpui::AnyElement {
    let rows = values
        .iter()
        .enumerate()
        .map(|(index, value)| settings_inventory_row(value, index, palette))
        .collect::<Vec<_>>();
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(title, description, palette))
        .when(values.is_empty(), |card| {
            card.child(
                div()
                    .h(px(64.))
                    .border_t_1()
                    .border_color(palette.border)
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_color(palette.faint)
                    .text_sm()
                    .child(empty),
            )
        })
        .children(rows)
        .into_any_element()
}

fn settings_inventory_row(
    value: &serde_json::Value,
    index: usize,
    palette: ThemePalette,
) -> gpui::Div {
    let detail = inventory_detail(value);
    div()
        .h(px(44.))
        .px_3()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(icon("circle", 10., palette.faint))
        .child(
            div()
                .min_w_0()
                .flex_1()
                .truncate()
                .text_color(palette.ink)
                .text_sm()
                .child(inventory_label(value, index)),
        )
        .when(!detail.is_empty(), |row| {
            row.child(
                div()
                    .max_w(px(320.))
                    .truncate()
                    .text_color(palette.faint)
                    .text_xs()
                    .child(detail),
            )
        })
}

fn settings_scalar_card(
    title: &'static str,
    description: &'static str,
    value: &serde_json::Value,
    palette: ThemePalette,
) -> gpui::AnyElement {
    settings_rows_card(title, description, scalar_rows(value), palette)
}

fn settings_rows_card(
    title: &'static str,
    description: &'static str,
    rows: Vec<(String, String)>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    let empty = rows.is_empty();
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(title, description, palette))
        .when(empty, |card| {
            card.child(
                div()
                    .h(px(58.))
                    .border_t_1()
                    .border_color(palette.border)
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_color(palette.faint)
                    .text_sm()
                    .child("—"),
            )
        })
        .children(
            rows.into_iter()
                .map(|(label, value)| settings_value_row(label, value, palette)),
        )
        .into_any_element()
}

fn settings_value_row(label: String, value: String, palette: ThemePalette) -> gpui::Div {
    div()
        .min_h(px(42.))
        .px_3()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(
            div()
                .flex_1()
                .text_color(palette.muted)
                .text_sm()
                .child(label),
        )
        .child(
            div()
                .max_w(px(420.))
                .text_right()
                .text_color(palette.ink)
                .text_sm()
                .child(value),
        )
}

fn settings_card_header(
    title: &'static str,
    description: &'static str,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(64.))
        .px_4()
        .py_3()
        .flex()
        .flex_col()
        .justify_center()
        .gap_1()
        .child(
            div()
                .text_color(palette.ink)
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
        .child(
            div()
                .text_color(palette.muted)
                .text_xs()
                .line_height(px(18.))
                .child(description),
        )
}

fn settings_empty_card(label: &'static str, palette: ThemePalette) -> gpui::AnyElement {
    div()
        .h(px(148.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .text_color(palette.faint)
        .text_sm()
        .flex()
        .items_center()
        .justify_center()
        .child(label)
        .into_any_element()
}

fn nested_inventory(value: &serde_json::Value, keys: &[&str]) -> Vec<serde_json::Value> {
    if let Some(values) = value.as_array() {
        return values.clone();
    }
    for key in keys {
        if let Some(values) = value.get(*key).and_then(serde_json::Value::as_array) {
            return values.clone();
        }
    }
    Vec::new()
}

fn inventory_label(value: &serde_json::Value, index: usize) -> String {
    [
        "label",
        "name",
        "displayName",
        "title",
        "id",
        "role",
        "scope",
        "path",
        "command",
        "provider",
    ]
    .into_iter()
    .find_map(|key| {
        value
            .get(key)
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
    })
    .map(str::to_string)
    .unwrap_or_else(|| format!("Item {}", index + 1))
}

fn inventory_detail(value: &serde_json::Value) -> String {
    if let Some(route) = value.get("route") {
        let provider = route
            .get("provider")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let model = route
            .get("model")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let reasoning = route
            .get("reasoning")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let detail = [provider, model, reasoning]
            .into_iter()
            .filter(|value| !value.is_empty())
            .collect::<Vec<_>>()
            .join(" · ");
        if !detail.is_empty() {
            return detail;
        }
    }
    [
        "status",
        "state",
        "kind",
        "source",
        "provider",
        "model",
        "description",
    ]
    .into_iter()
    .find_map(|key| {
        value
            .get(key)
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
    })
    .unwrap_or_default()
    .to_string()
}

fn scalar_rows(value: &serde_json::Value) -> Vec<(String, String)> {
    match value {
        serde_json::Value::Object(fields) => fields
            .iter()
            .filter_map(|(key, value)| {
                let display = scalar_display(value);
                (!display.is_empty()).then(|| (humanize_key(key), display))
            })
            .take(16)
            .collect(),
        serde_json::Value::Null => Vec::new(),
        _ => vec![("Status".to_string(), scalar_display(value))],
    }
}

fn scalar_display(value: &serde_json::Value) -> String {
    match value {
        serde_json::Value::Null => String::new(),
        serde_json::Value::Bool(value) => {
            if *value {
                "On".to_string()
            } else {
                "Off".to_string()
            }
        }
        serde_json::Value::Number(value) => value.to_string(),
        serde_json::Value::String(value) => value.clone(),
        serde_json::Value::Array(values) => format!("{} items", values.len()),
        serde_json::Value::Object(fields) => {
            for key in ["name", "title", "id", "status", "state"] {
                if let Some(value) = fields.get(key).and_then(serde_json::Value::as_str)
                    && !value.trim().is_empty()
                {
                    return value.to_string();
                }
            }
            format!("{} fields", fields.len())
        }
    }
}

fn humanize_key(key: &str) -> String {
    let mut output = String::with_capacity(key.len() + 4);
    for (index, character) in key.chars().enumerate() {
        if index > 0 && character.is_ascii_uppercase() {
            output.push(' ');
        }
        if index == 0 {
            output.extend(character.to_uppercase());
        } else {
            output.push(character);
        }
    }
    output
}

fn settings_action_button(
    id: usize,
    label: &'static str,
    enabled: bool,
    selected: bool,
    cx: &mut Context<AzemWindow>,
    payload: serde_json::Value,
) -> gpui::AnyElement {
    let selectable = id < 20;
    div()
        .id(("settings-action", id))
        .role(pick(selectable, Role::RadioButton, Role::Button))
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(enabled)
        .px_3()
        .py_2()
        .rounded_md()
        .bg(pick(selected, rgb(0xe7e9eb), rgb(0xffffff)))
        .border_1()
        .border_color(rgb(0xecedef))
        .text_color(pick(enabled, rgb(0x2d3033), rgb(0xa3a7ac)))
        .cursor_pointer()
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
                        .border_color(rgb(0xecedef))
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
                        .border_color(rgb(0xecedef))
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

pub(super) fn projects_surface(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let project_name = std::path::Path::new(state.workspace.root.as_ref())
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("workspace")
        .to_string();
    let destinations = [
        ("folder", labels.files, Surface::Files),
        ("file-diff", labels.changes, Surface::Changes),
        (
            "git-pull-request",
            labels.pull_requests,
            Surface::PullRequests,
        ),
        ("list-todo", labels.run_controls, Surface::Work),
        ("shield-check", labels.security, Surface::Security),
        ("terminal", labels.terminal, Surface::Terminal),
        ("chart", labels.usage, Surface::Usage),
    ];
    div()
        .id("projects-surface")
        .role(Role::Region)
        .aria_label(labels.workspace)
        .flex_1()
        .overflow_y_scroll()
        .bg(palette.paper)
        .px(px(42.))
        .py(px(34.))
        .flex()
        .justify_center()
        .child(
            div()
                .w_full()
                .max_w(px(980.))
                .flex()
                .flex_col()
                .gap_4()
                .child(
                    div()
                        .pb_3()
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_end()
                        .child(
                            div()
                                .flex()
                                .flex_col()
                                .gap_1()
                                .child(
                                    div()
                                        .text_size(px(26.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(project_name),
                                )
                                .child(
                                    div()
                                        .text_color(palette.muted)
                                        .text_xs()
                                        .child(state.workspace.root.to_string()),
                                ),
                        )
                        .child(div().flex_1())
                        .child(
                            div()
                                .text_color(if state.workspace.dirty {
                                    palette.warning
                                } else {
                                    palette.positive
                                })
                                .text_xs()
                                .child(if state.workspace.dirty {
                                    format!(
                                        "{} · +{} −{}",
                                        state.workspace.branch,
                                        state.workspace.additions,
                                        state.workspace.deletions
                                    )
                                } else {
                                    state.workspace.branch.to_string()
                                }),
                        ),
                )
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .children(destinations.into_iter().enumerate().map(
                            |(index, (icon_name, label, destination))| {
                                div()
                                    .id(("workspace-destination", index))
                                    .role(Role::Button)
                                    .aria_label(label)
                                    .tab_stop(true)
                                    .min_h(px(54.))
                                    .px_3()
                                    .border_b_1()
                                    .border_color(palette.border)
                                    .text_color(palette.ink)
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .cursor_pointer()
                                    .hover(move |style| style.bg(palette.paper_muted))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.state.navigation.surface = destination;
                                        this.request_surface(destination);
                                        cx.notify();
                                    }))
                                    .child(div().w(px(24.)).child(icon(
                                        icon_name,
                                        16.,
                                        palette.faint,
                                    )))
                                    .child(
                                        div()
                                            .text_sm()
                                            .font_weight(gpui::FontWeight::MEDIUM)
                                            .child(label),
                                    )
                                    .child(div().flex_1())
                                    .child(icon("chevron-right", 13., palette.faint))
                            },
                        )),
                )
                .when(!state.navigation.projects.is_empty(), |surface| {
                    surface.child(
                        div()
                            .pt_3()
                            .text_color(palette.faint)
                            .text_xs()
                            .child(format!(
                                "{} {}",
                                state.navigation.projects.len(),
                                if state.settings.language.as_ref() == "zh-CN" {
                                    "个已保存项目"
                                } else {
                                    "saved projects"
                                }
                            )),
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
        .border_color(rgb(0xe0e2e5))
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
                .border_color(rgb(0xecedef))
                .p_3()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .text_color(rgb(0x62656b))
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
                                .border_color(rgb(0xecedef))
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
                .child(pull_request_detail_content(detail))
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

fn pull_request_detail_content(detail: &serde_json::Value) -> gpui::AnyElement {
    if detail.is_null() || detail.get("number").is_none() {
        return div()
            .text_color(rgb(0x9a9da3))
            .text_sm()
            .child("Select a pull request")
            .into_any_element();
    }
    let number = detail
        .get("number")
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default();
    let title = detail
        .get("title")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("Pull request")
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
                        .text_color(rgb(0x1f2124))
                        .child(title),
                )
                .child(
                    div()
                        .text_size(px(11.))
                        .text_color(rgb(0x62656b))
                        .child(format!("#{number} · {state} · {author} · {head} → {base}")),
                ),
        )
        .child(
            div()
                .flex()
                .gap_3()
                .text_size(px(11.))
                .child(
                    div()
                        .text_color(rgb(0x189a4d))
                        .child(format!("+{additions}")),
                )
                .child(
                    div()
                        .text_color(rgb(0xe3474c))
                        .child(format!("−{deletions}")),
                )
                .child(
                    div()
                        .text_color(rgb(0x62656b))
                        .child(format!("{changed_files} files")),
                ),
        )
        .when_some(
            (!body.is_empty()).then(|| pull_request_body(body)),
            |content, body| content.child(body),
        )
        .when_some(
            (!checks.is_empty()).then(|| pull_request_checks(checks)),
            |content, checks| content.child(checks),
        )
        .when_some(
            (!files.is_empty()).then(|| pull_request_files(files)),
            |content, files| content.child(files),
        )
        .into_any_element()
}

fn pull_request_body(body: String) -> gpui::AnyElement {
    div()
        .max_w(px(760.))
        .text_size(px(12.))
        .line_height(px(19.))
        .text_color(rgb(0x45484d))
        .whitespace_normal()
        .child(body)
        .into_any_element()
}

fn pull_request_checks(checks: Vec<serde_json::Value>) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .text_size(px(12.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child("Checks"),
        )
        .children(checks.into_iter().map(pull_request_check_row))
        .into_any_element()
}

fn pull_request_check_row(check: serde_json::Value) -> gpui::Div {
    let name = check
        .get("name")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("Check")
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
        .border_color(rgb(0xecedef))
        .text_size(px(11.))
        .flex()
        .items_center()
        .gap_2()
        .child(icon(
            pick(failing, "square", "check"),
            13.,
            pick(failing, rgb(0xe3474c), rgb(0x189a4d)),
        ))
        .child(name)
        .child(div().flex_1())
        .child(div().text_color(rgb(0x62656b)).child(category))
}

fn pull_request_files(files: Vec<serde_json::Value>) -> gpui::AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .text_size(px(12.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child("Files"),
        )
        .children(files.into_iter().map(pull_request_file_row))
        .into_any_element()
}

fn pull_request_file_row(file: serde_json::Value) -> gpui::Div {
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
        .border_color(rgb(0xecedef))
        .font_family("SF Mono")
        .text_size(px(10.))
        .flex()
        .items_center()
        .child(path)
        .child(div().flex_1())
        .child(
            div()
                .text_color(rgb(0x189a4d))
                .child(format!("+{additions}")),
        )
        .child(
            div()
                .ml_2()
                .text_color(rgb(0xe3474c))
                .child(format!("−{deletions}")),
        )
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
        .border_color(rgb(0xe0e2e5))
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
                .border_color(rgb(0xecedef))
                .p_3()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .text_color(rgb(0x62656b))
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
                .border_color(rgb(0xecedef))
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
        .border_color(rgb(0xecedef))
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
        .border_color(rgb(0xe0e2e5))
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
pub(super) fn timeline_entry(
    index: usize,
    blocks: &[Block],
    palette: ThemePalette,
    zh: bool,
    workspace_changes: (i64, i64),
    expansion: Rc<RefCell<HashSet<String>>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (workspace_additions, workspace_deletions) = workspace_changes;
    let Some(block) = blocks.get(index) else {
        return div().h(px(0.)).into_any_element();
    };
    let kind = block.kind.as_ref();
    let process = matches!(kind, "thinking" | "tool" | "diff");
    if process
        && index > 0
        && blocks.get(index - 1).is_some_and(|previous| {
            matches!(previous.kind.as_ref(), "thinking" | "tool" | "diff")
                && previous.run_id == block.run_id
        })
    {
        return div().h(px(0.)).into_any_element();
    }
    if process {
        let end = blocks[index..]
            .iter()
            .position(|candidate| {
                !matches!(candidate.kind.as_ref(), "thinking" | "tool" | "diff")
                    || candidate.run_id != block.run_id
            })
            .map(|offset| index + offset)
            .unwrap_or(blocks.len());
        let group = &blocks[index..end];
        let tool_count = group
            .iter()
            .filter(|candidate| candidate.kind.as_ref() == "tool")
            .count();
        let diff_count = group
            .iter()
            .filter(|candidate| is_file_change(candidate))
            .count();
        let running = group.iter().any(|candidate| {
            matches!(
                candidate.state.as_ref(),
                "running" | "queued" | "pending" | "streaming"
            )
        });
        let key = if block.run_id.is_empty() {
            block.id.to_string()
        } else {
            block.run_id.to_string()
        };
        let expanded = running || expansion.borrow().contains(&key);
        let summary = if running && tool_count > 0 {
            if zh {
                format!("正在运行 {tool_count} 个工具")
            } else {
                format!("Running {tool_count} tools")
            }
        } else if diff_count > 0 {
            if zh {
                format!("已编辑 {diff_count} 个文件")
            } else {
                format!("Edited {diff_count} files")
            }
        } else if tool_count > 0 {
            if zh {
                format!("{tool_count} 次工具调用")
            } else {
                format!("{tool_count} tool calls")
            }
        } else if running {
            if zh {
                "思考".to_string()
            } else {
                "Thinking".to_string()
            }
        } else if zh {
            "思考了".to_string()
        } else {
            "Thought".to_string()
        };
        let measured_additions = group
            .iter()
            .filter_map(|candidate| process_number(candidate, "additions"))
            .sum::<i64>();
        let measured_deletions = group
            .iter()
            .filter_map(|candidate| process_number(candidate, "deletions"))
            .sum::<i64>();
        let additions = if diff_count > 0 && measured_additions == 0 {
            workspace_additions
        } else {
            measured_additions
        };
        let deletions = if diff_count > 0 && measured_deletions == 0 {
            workspace_deletions
        } else {
            measured_deletions
        };
        let toggle_key = key.clone();
        let toggle_expansion = expansion.clone();
        let toggle_owner = owner.clone();
        let header_icon = if running {
            "loader"
        } else if diff_count > 0 {
            "file-diff"
        } else {
            "check"
        };
        let card = div()
            .id(("process-group", index))
            .role(Role::Group)
            .aria_label(summary.clone())
            .w_full()
            .max_w(px(840.))
            .rounded(px(14.))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .overflow_hidden()
            .child(
                div()
                    .id(("process-summary", index))
                    .role(Role::Button)
                    .aria_label(summary.clone())
                    .aria_expanded(expanded)
                    .tab_stop(true)
                    .min_h(px(46.))
                    .px_3()
                    .flex()
                    .items_center()
                    .gap_3()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.paper_muted))
                    .on_click(move |_, _, cx| {
                        let mut values = toggle_expansion.borrow_mut();
                        if values.contains(&toggle_key) {
                            values.remove(&toggle_key);
                        } else {
                            values.insert(toggle_key.clone());
                        }
                        drop(values);
                        toggle_owner.update(cx, |_, cx| cx.notify());
                    })
                    .child(
                        div()
                            .size(px(28.))
                            .rounded(px(8.))
                            .bg(palette.paper_muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(icon(
                                header_icon,
                                15.,
                                if running {
                                    palette.accent
                                } else {
                                    palette.muted
                                },
                            )),
                    )
                    .child(
                        div()
                            .flex_1()
                            .flex()
                            .flex_col()
                            .gap(px(1.))
                            .child(
                                div()
                                    .text_size(px(12.))
                                    .font_weight(gpui::FontWeight::MEDIUM)
                                    .text_color(palette.ink)
                                    .child(summary),
                            )
                            .when(additions > 0 || deletions > 0, |copy| {
                                copy.child(
                                    div()
                                        .text_size(px(10.))
                                        .flex()
                                        .gap_2()
                                        .child(
                                            div()
                                                .text_color(palette.positive)
                                                .child(format!("+{additions}")),
                                        )
                                        .child(
                                            div()
                                                .text_color(palette.danger)
                                                .child(format!("−{deletions}")),
                                        ),
                                )
                            }),
                    )
                    .child(icon(
                        if expanded {
                            "chevron-down"
                        } else {
                            "chevron-right"
                        },
                        14.,
                        palette.faint,
                    )),
            )
            .when(expanded, |card| {
                card.child(
                    div()
                        .border_t_1()
                        .border_color(palette.border)
                        .flex()
                        .flex_col()
                        .children(group.iter().enumerate().map(|(row_index, candidate)| {
                            let title = if !candidate.title.is_empty() {
                                candidate.title.to_string()
                            } else if candidate.kind.as_ref() == "thinking" {
                                if zh {
                                    "思考".to_string()
                                } else {
                                    "Thinking".to_string()
                                }
                            } else {
                                candidate.kind.to_string()
                            };
                            let failed = candidate.state.as_ref() == "failed";
                            let row_running = matches!(
                                candidate.state.as_ref(),
                                "running" | "queued" | "pending" | "streaming"
                            );
                            let row_icon = if failed {
                                "square"
                            } else if row_running {
                                "loader"
                            } else {
                                "check"
                            };
                            div()
                                .id(("process-row", index * 1000 + row_index))
                                .min_h(px(40.))
                                .px_3()
                                .py_2()
                                .flex()
                                .items_start()
                                .gap_3()
                                .child(div().w(px(18.)).child(icon(
                                    row_icon,
                                    14.,
                                    if failed {
                                        palette.danger
                                    } else if row_running {
                                        palette.accent
                                    } else {
                                        palette.muted
                                    },
                                )))
                                .child(
                                    div()
                                        .flex_1()
                                        .flex()
                                        .flex_col()
                                        .gap_1()
                                        .child(
                                            div()
                                                .text_color(palette.ink_soft)
                                                .text_size(px(11.))
                                                .font_weight(gpui::FontWeight::MEDIUM)
                                                .child(title),
                                        )
                                        .when(!candidate.content.is_empty(), |row| {
                                            row.child(
                                                div()
                                                    .text_color(palette.muted)
                                                    .text_size(px(11.))
                                                    .line_height(px(17.))
                                                    .whitespace_normal()
                                                    .child(candidate.content.clone()),
                                            )
                                        }),
                                )
                        })),
                )
            });
        return div()
            .id(("timeline-block", index))
            .role(Role::Article)
            .aria_label(key)
            .w_full()
            .px(px(34.))
            .py(px(8.))
            .flex()
            .justify_center()
            .child(card)
            .into_any_element();
    }
    let row = if kind == "user" {
        div()
            .w_full()
            .max_w(px(840.))
            .flex()
            .justify_end()
            .child(
                div()
                    .max_w(px(680.))
                    .px_3()
                    .py_2()
                    .rounded(px(16.))
                    .bg(palette.paper_muted)
                    .text_color(palette.ink)
                    .text_size(px(13.))
                    .line_height(px(21.))
                    .whitespace_normal()
                    .child(block.content.clone()),
            )
            .into_any_element()
    } else {
        div()
            .w_full()
            .max_w(px(840.))
            .when(kind == "error", |message| {
                message
                    .px_3()
                    .py_2()
                    .rounded(px(10.))
                    .bg(palette.paper_muted)
                    .text_color(palette.danger)
            })
            .when(
                kind == "assistant" && block.text_phase.as_ref() == "commentary",
                |message| message.text_color(palette.muted),
            )
            .when(
                kind != "error"
                    && !(kind == "assistant" && block.text_phase.as_ref() == "commentary"),
                |message| message.text_color(palette.ink),
            )
            .text_size(px(13.))
            .line_height(px(21.))
            .whitespace_normal()
            .child(block.content.clone())
            .into_any_element()
    };
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(kind.to_string())
        .w_full()
        .px(px(34.))
        .py(px(8.))
        .flex()
        .justify_center()
        .child(row)
        .into_any_element()
}

fn process_number(block: &Block, key: &str) -> Option<i64> {
    block
        .extra
        .get(key)
        .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
        .and_then(|value| {
            value
                .as_i64()
                .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
        })
}

fn is_file_change(block: &Block) -> bool {
    if block.kind.as_ref() == "diff" {
        return true;
    }
    matches!(
        block.title.as_ref(),
        "coding.write_file"
            | "coding.edit"
            | "coding.replace"
            | "coding.delete_file"
            | "coding.gofmt"
    )
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
