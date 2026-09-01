use std::{
    cell::RefCell,
    collections::{HashMap, HashSet},
    rc::Rc,
    sync::Arc,
    time::Duration,
};

use azem_ipc::Method;
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Local, TimeZone};
use gpui::{
    Animation, AnimationExt as _, BoxShadow, ClipboardItem, Context, Entity, Focusable,
    MouseButton, Pixels, Rgba, Role, ScrollStrategy, StyledImage, Svg, Transformation,
    UniformListScrollHandle, Window, deferred, div, hsla, img, percentage, prelude::*, px,
    relative, rgb, rgba, svg, uniform_list,
};
use serde_json::json;

use super::{
    AzemWindow, CHAT_COLUMN_GUTTER_WIDE, CHAT_COLUMN_MAX_WIDTH, ExtensionSettings, NativeSettings,
    PendingRequest, ProcessExpansion, ReplyActionsSnapshot, ReplyForkAnchor, ReplyPopoverKind,
    RoutePickerKind, RoutePickerTarget, SECURITY_NUMBERS, SidebarContextMenu, SidebarMenuAction,
    SidebarMenuTarget, SubagentSettingKind, catalog_model_name,
};
use crate::{
    localization::{Labels, Locale},
    markdown::markdown_view,
    runtime_connection::RuntimeConnection,
    state::{AppState, Block, SessionSummary, Surface},
    text_input::TextInput,
    theme::{AppearancePreferences, ThemePalette},
};

pub(super) const SIDEBAR_WIDTH: f32 = 246.;

fn pretty_value(value: &serde_json::Value) -> String {
    serde_json::to_string_pretty(value).unwrap_or_default()
}

fn picker_menu_position(bounds: gpui::Bounds<Pixels>, below: bool) -> gpui::Point<Pixels> {
    gpui::point(
        bounds.left(),
        if below {
            bounds.bottom() + px(5.)
        } else {
            bounds.top() - px(5.)
        },
    )
}

fn approval_mode_icon(mode: &str) -> &'static str {
    match mode {
        "auto_review" => "shield-check",
        "yolo" => "shield-alert",
        _ => "message-square-text",
    }
}

pub(super) fn approval_picker_control(
    this: &AzemWindow,
    labels: Labels,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let picker = &this.approval_picker;
    let enabled = this.state.connection.connected && !super::runtime_busy(&this.state);
    let busy = this.approval_request_pending();
    let current = this.state.settings.approval_mode.as_ref();
    let color = if current == "yolo" {
        palette.danger
    } else if enabled {
        palette.ink_soft
    } else {
        palette.faint
    };
    let label = if current == "auto_review" {
        labels.auto_review
    } else {
        locale.text(
            super::APPROVAL_MODES
                .iter()
                .find(|(mode, _, _)| *mode == current)
                .map_or("approval.select", |(_, label, _)| label),
        )
    };
    let owner = cx.entity();
    div()
        .relative()
        .on_children_prepainted(move |bounds, _, cx| {
            owner.update(cx, |this, cx| {
                let bounds = bounds.first().copied();
                if this.approval_picker.button_bounds != bounds {
                    this.approval_picker.button_bounds = bounds;
                    if this.approval_picker.open {
                        cx.notify();
                    }
                }
            });
        })
        .child(
            div()
                .id("approval-mode")
                .role(Role::ComboBox)
                .aria_label(locale.text("approval.select"))
                .aria_value(label)
                .aria_expanded(picker.open)
                .tab_stop(enabled)
                .track_focus(&picker.focus)
                // Consume keys before GPUI turns Enter/Space key-up into another click.
                .capture_key_down(cx.listener(AzemWindow::approval_picker_key))
                .h(px(32.))
                .px_2()
                .rounded_full()
                .text_color(color)
                .text_xs()
                .flex()
                .items_center()
                .gap_1()
                .when(enabled, |button| {
                    button
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.paper_muted))
                })
                .on_click(
                    cx.listener(|this, _, window, cx| this.toggle_approval_picker(window, cx)),
                )
                .child(icon(approval_mode_icon(current), 15., color))
                .child(label)
                .child(icon(
                    if busy { "loader" } else { "chevron-down" },
                    11.,
                    palette.faint,
                )),
        )
        .when(picker.open && picker.button_bounds.is_some(), |control| {
            let menu = div()
                .id("approval-mode-menu")
                .role(Role::ListBox)
                .aria_label(locale.text("approval.select"))
                .min_w(px(300.))
                .max_w(px(480.))
                .whitespace_nowrap()
                .p_1()
                .rounded(px(10.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .text_color(palette.ink)
                .text_xs()
                .shadow(vec![
                    BoxShadow::new(px(0.), px(6.), hsla(0., 0., 0., 0.15)).blur_radius(px(20.)),
                ])
                .occlude()
                .on_key_down(cx.listener(AzemWindow::approval_picker_key))
                .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                .children(super::APPROVAL_MODES.into_iter().enumerate().map(
                    |(index, (mode, label, description))| {
                        let selected = mode == current;
                        let color = if mode == "yolo" {
                            palette.danger
                        } else {
                            palette.ink
                        };
                        div()
                            .id(("approval-mode-option", index))
                            .role(Role::ListBoxOption)
                            .aria_label(locale.text(label))
                            .aria_description(locale.text(description))
                            .aria_selected(selected)
                            .text_color(color)
                            .when(index == picker.index, |row| row.aria_active_descendant())
                            .p_2()
                            .rounded(px(6.))
                            .flex()
                            .items_center()
                            .gap_2()
                            .bg(if index == picker.index {
                                palette.hover
                            } else {
                                palette.paper
                            })
                            .when(enabled && !busy, |row| {
                                row.cursor_pointer()
                                    .hover(move |style| style.bg(palette.hover))
                            })
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.approval_picker.focus.focus(window, cx);
                                this.change_approval_mode(mode, cx);
                            }))
                            .child(div().size(px(16.)).flex_shrink_0().child(icon(
                                approval_mode_icon(mode),
                                15.,
                                color,
                            )))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .child(locale.text(label))
                                    .child(
                                        div()
                                            .text_size(px(10.))
                                            .line_height(px(15.))
                                            .text_color(palette.muted)
                                            .truncate()
                                            .child(locale.text(description)),
                                    ),
                            )
                            .child(div().w(px(14.)).when(selected, |mark| {
                                mark.child(icon(
                                    "check",
                                    13.,
                                    if mode == "yolo" {
                                        palette.danger
                                    } else {
                                        palette.accent
                                    },
                                ))
                            }))
                    },
                ))
                .when(!picker.error.is_empty(), |menu| {
                    menu.child(
                        div()
                            .p_2()
                            .text_color(palette.danger)
                            .child(picker.error.clone()),
                    )
                });
            control.child(
                deferred(
                    gpui::anchored()
                        .anchor(gpui::Anchor::BottomLeft)
                        .position(picker_menu_position(picker.button_bounds.unwrap(), false))
                        .snap_to_window_with_margin(px(8.))
                        .child(menu),
                )
                .with_priority(25),
            )
        })
        .into_any_element()
}

pub(super) fn branch_picker_control(
    this: &AzemWindow,
    environment: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let picker = &this.branch_picker;
    let busy = this.branch_request_pending();
    let enabled = this.state.connection.connected && !super::runtime_busy(&this.state);
    let current = this.state.workspace.branch.to_string();
    let names = super::visible_git_branches(
        &this.state.workspace.branches,
        &current,
        picker.search.read(cx).text(),
    );
    let index = picker.index.min(names.len().saturating_sub(1));
    let owner = cx.entity();
    div()
        .relative()
        .when(environment, |control| control.w_full())
        .when(!environment, |control| control.min_w_0().max_w(px(260.)))
        .on_children_prepainted(move |bounds, _, cx| {
            owner.update(cx, |this, cx| {
                let bounds = bounds.first().copied();
                if this.branch_picker.button_bounds != bounds {
                    this.branch_picker.button_bounds = bounds;
                    if this.branch_picker.open {
                        cx.notify();
                    }
                }
            });
        })
        .child(
            div()
                .id("branch-picker")
                .role(Role::ComboBox)
                .aria_label(locale.text("branch.select"))
                .aria_value(current.clone())
                .aria_expanded(picker.open)
                .when(!enabled, |button| {
                    button.aria_description(locale.text(if this.state.connection.connected {
                        "branch.busy"
                    } else {
                        "branch.unavailable"
                    }))
                })
                .tab_stop(enabled)
                .track_focus(&picker.focus)
                .capture_key_down(cx.listener(AzemWindow::branch_picker_key))
                .px_2()
                .when(environment, |button| {
                    button.h(px(38.)).rounded(px(8.)).bg(palette.paper)
                })
                .when(!environment, |button| {
                    button.h(px(29.)).rounded_full().bg(palette.paper_muted)
                })
                .text_color(if enabled {
                    if environment {
                        palette.ink
                    } else {
                        palette.muted
                    }
                } else {
                    palette.faint
                })
                .text_xs()
                .flex()
                .items_center()
                .gap(px(if environment { 8. } else { 4. }))
                .when(enabled, |button| {
                    button
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                })
                .on_click(cx.listener(|this, _, window, cx| this.toggle_branch_picker(window, cx)))
                .child(div().w(px(if environment { 18. } else { 13. })).child(icon(
                    "git-branch",
                    if environment { 16. } else { 13. },
                    if environment {
                        palette.ink_soft
                    } else {
                        palette.muted
                    },
                )))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .child(if current.is_empty() {
                            locale.text("branch.select").to_string()
                        } else {
                            current.clone()
                        }),
                )
                .child(icon(
                    if busy { "loader" } else { "chevron-down" },
                    12.,
                    palette.faint,
                )),
        )
        .when(picker.open && picker.button_bounds.is_some(), |control| {
            let options_enabled = enabled && !busy && picker.confirm_target.is_none();
            let menu = div()
                .id("branch-menu")
                .w(px(280.))
                .rounded(px(10.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .text_color(palette.ink)
                .text_xs()
                .shadow(vec![
                    BoxShadow::new(px(0.), px(6.), hsla(0., 0., 0., 0.15)).blur_radius(px(20.)),
                ])
                .occlude()
                .flex()
                .flex_col()
                .on_key_down(cx.listener(AzemWindow::branch_picker_key))
                .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                .child(
                    div()
                        .h(px(36.))
                        .px_2()
                        .flex()
                        .items_center()
                        .gap_1()
                        .border_b_1()
                        .border_color(palette.border)
                        .child(icon("search", 14., palette.faint))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .h_full()
                                .child(picker.search.clone()),
                        ),
                )
                .child(
                    div()
                        .id("branch-options")
                        .role(Role::ListBox)
                        .aria_label(locale.text("branch.select"))
                        .max_h(px(224.))
                        .overflow_y_scroll()
                        .track_scroll(&picker.scroll)
                        .p_1()
                        .when(names.is_empty() || busy, |list| {
                            list.child(div().p_2().text_color(palette.muted).child(locale.text(
                                if busy {
                                    if this.pending_requests.values().any(|request| {
                                        matches!(
                                            request,
                                            PendingRequest::GitBranches {
                                                target: Some(_),
                                                ..
                                            }
                                        )
                                    }) {
                                        "branch.switching"
                                    } else {
                                        "branch.loading"
                                    }
                                } else {
                                    "branch.empty"
                                },
                            )))
                        })
                        .when(!busy, |list| {
                            list.children(names.into_iter().enumerate().map(|(row, name)| {
                                let selected = name == current;
                                let target = name.clone();
                                div()
                                    .id(("branch-option", row))
                                    .role(Role::ListBoxOption)
                                    .aria_label(name.clone())
                                    .aria_selected(selected)
                                    .when(row == index, |row| row.aria_active_descendant())
                                    .h(px(32.))
                                    .px_2()
                                    .rounded(px(6.))
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .bg(if row == index {
                                        palette.hover
                                    } else {
                                        palette.paper
                                    })
                                    .when(options_enabled, |row| {
                                        row.cursor_pointer()
                                            .hover(move |style| style.bg(palette.hover))
                                    })
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        if options_enabled {
                                            this.branch_picker.focus.focus(window, cx);
                                            this.switch_branch(target.clone(), false, cx);
                                        }
                                    }))
                                    .child(div().flex_1().min_w_0().truncate().child(name))
                                    .when(selected, |row| {
                                        row.child(icon("check", 13., palette.accent))
                                    })
                            }))
                        }),
                )
                .when(!enabled, |menu| {
                    menu.child(div().p_2().text_color(palette.muted).child(locale.text(
                        if this.state.connection.connected {
                            "branch.busy"
                        } else {
                            "branch.unavailable"
                        },
                    )))
                })
                .when(!picker.error.is_empty(), |menu| {
                    menu.child(
                        div()
                            .p_2()
                            .text_color(palette.danger)
                            .child(picker.error.clone()),
                    )
                })
                .when_some(picker.confirm_target.clone(), |menu, target| {
                    menu.child(
                        div()
                            .p_2()
                            .border_t_1()
                            .border_color(palette.border)
                            .flex()
                            .flex_col()
                            .gap_2()
                            .child(locale.format("branch.dirty", &[("branch", target.clone())]))
                            .child(
                                div()
                                    .flex()
                                    .justify_end()
                                    .gap_2()
                                    .child(
                                        div()
                                            .id("branch-cancel")
                                            .role(Role::Button)
                                            .aria_label(locale.text("ui.cancel"))
                                            .tab_stop(true)
                                            .px_2()
                                            .py_1()
                                            .rounded(px(6.))
                                            .cursor_pointer()
                                            .on_click(cx.listener(|this, _, window, cx| {
                                                this.branch_picker.confirm_target = None;
                                                this.branch_picker.open = false;
                                                this.branch_picker.focus.focus(window, cx);
                                                cx.notify();
                                            }))
                                            .on_key_down(cx.listener(
                                                |this, event: &gpui::KeyDownEvent, window, cx| {
                                                    if matches!(
                                                        event.keystroke.key.as_str(),
                                                        "enter" | "space"
                                                    ) {
                                                        this.branch_picker.confirm_target = None;
                                                        this.branch_picker.open = false;
                                                        this.branch_picker.focus.focus(window, cx);
                                                        cx.stop_propagation();
                                                        cx.notify();
                                                    }
                                                },
                                            ))
                                            .child(locale.text("ui.cancel")),
                                    )
                                    .child(
                                        div()
                                            .id("branch-confirm")
                                            .role(Role::Button)
                                            .aria_label(locale.text("branch.confirm"))
                                            .tab_stop(enabled && !busy)
                                            .px_2()
                                            .py_1()
                                            .rounded(px(6.))
                                            .bg(palette.button)
                                            .text_color(palette.button_text)
                                            .cursor_pointer()
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                this.switch_branch(target.clone(), true, cx)
                                            }))
                                            .on_key_down(cx.listener(
                                                |this, event: &gpui::KeyDownEvent, _, cx| {
                                                    if matches!(
                                                        event.keystroke.key.as_str(),
                                                        "enter" | "space"
                                                    ) {
                                                        if let Some(target) = this
                                                            .branch_picker
                                                            .confirm_target
                                                            .clone()
                                                        {
                                                            this.switch_branch(target, true, cx);
                                                        }
                                                        cx.stop_propagation();
                                                    }
                                                },
                                            ))
                                            .child(locale.text("branch.confirm")),
                                    ),
                            ),
                    )
                });
            control.child(
                deferred(
                    gpui::anchored()
                        .anchor(gpui::Anchor::TopLeft)
                        .position(picker_menu_position(picker.button_bounds.unwrap(), true))
                        .snap_to_window_with_margin(px(8.))
                        .child(menu),
                )
                .with_priority(25),
            )
        })
        .into_any_element()
}

const HOST_VERIFICATION_NOTICES: [&str; 2] = [
    "Verification evidence is missing or stale for the current workspace snapshot after one retry. The work remains uncertain and is not reported as complete.",
    "Verification failed for the current workspace snapshot. The attempted result is not reported as complete; inspect the failed checks and correct the work before retrying.",
];

const USER_MESSAGE_SUBMIT_TRANSITION: Duration = Duration::from_millis(180);
const USAGE_REVEAL_TRANSITION: Duration = Duration::from_millis(520);

fn visible_assistant_content(content: &str) -> &str {
    let without_notice = strip_host_notice(content);
    let (without_citation, _) = split_memory_citation(without_notice);
    strip_host_notice(without_citation)
}

fn strip_host_notice(content: &str) -> &str {
    let trimmed = content.trim_end();
    for notice in HOST_VERIFICATION_NOTICES {
        if let Some(value) = trimmed.strip_suffix(notice) {
            return value.trim_end();
        }
    }
    content
}

fn split_memory_citation(content: &str) -> (&str, Option<&str>) {
    const OPEN: &str = "<oai-mem-citation>";
    const CLOSE: &str = "</oai-mem-citation>";
    let trimmed = content.trim_end();
    let Some(start) = trimmed.rfind(OPEN) else {
        return (content, None);
    };
    if !trimmed.ends_with(CLOSE) {
        return (content, None);
    }
    let body_start = start + OPEN.len();
    let body_end = trimmed.len() - CLOSE.len();
    (
        trimmed[..start].trim_end(),
        Some(&trimmed[body_start..body_end]),
    )
}

fn assistant_memory_notes(content: &str) -> Vec<String> {
    let without_notice = strip_host_notice(content);
    let (_, citation) = split_memory_citation(without_notice);
    citation
        .into_iter()
        .flat_map(str::lines)
        .filter_map(|line| line.split_once("|note=[").map(|(_, note)| note))
        .filter_map(|note| note.trim().strip_suffix(']'))
        .filter(|note| !note.is_empty())
        .map(str::to_string)
        .collect()
}

pub(super) fn session_entry_id(
    tree: &serde_json::Value,
    anchor: ReplyForkAnchor,
) -> Option<String> {
    let roots = tree.get("roots")?.as_array()?;
    let active_leaf = tree
        .get("activeLeafEntryId")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let mut path = Vec::new();
    if active_leaf.is_empty() || !find_session_entry_path(roots, active_leaf, &mut path) {
        collect_session_entries(roots, &mut path);
        path.sort_by_key(|entry| {
            entry
                .get("sequence")
                .and_then(serde_json::Value::as_i64)
                .unwrap_or(i64::MAX)
        });
    }
    if let Some(sequence) = anchor.sequence
        && let Some(entry) = path.iter().find(|entry| {
            entry.get("sequence").and_then(serde_json::Value::as_i64) == Some(sequence)
        })
    {
        return entry
            .get("id")
            .and_then(serde_json::Value::as_str)
            .map(str::to_string);
    }
    path.into_iter()
        .filter(|entry| entry.get("kind").and_then(serde_json::Value::as_str) == Some("assistant"))
        .nth(anchor.assistant_ordinal)
        .and_then(|entry| entry.get("id"))
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
}

fn find_session_entry_path<'a>(
    nodes: &'a [serde_json::Value],
    target: &str,
    path: &mut Vec<&'a serde_json::Value>,
) -> bool {
    for node in nodes {
        let Some(entry) = node.get("entry") else {
            continue;
        };
        path.push(entry);
        if entry.get("id").and_then(serde_json::Value::as_str) == Some(target)
            || node
                .get("children")
                .and_then(serde_json::Value::as_array)
                .is_some_and(|children| find_session_entry_path(children, target, path))
        {
            return true;
        }
        path.pop();
    }
    false
}

fn collect_session_entries<'a>(
    nodes: &'a [serde_json::Value],
    entries: &mut Vec<&'a serde_json::Value>,
) {
    for node in nodes {
        if let Some(entry) = node.get("entry") {
            entries.push(entry);
        }
        if let Some(children) = node.get("children").and_then(serde_json::Value::as_array) {
            collect_session_entries(children, entries);
        }
    }
}

fn animate_submitted_user(state: &str, reduced_motion: bool) -> bool {
    state == "submitted" && !reduced_motion
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
    sidebar_context_target: Option<&SidebarMenuTarget>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
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
        .w(px(SIDEBAR_WIDTH))
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
                                        | Surface::Security
                                        | Surface::Terminal
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
                .aria_label(locale.text("navigation.primary"))
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
                            if this.state.navigation.surface != Surface::Search {
                                this.search_return_surface = this.state.navigation.surface;
                            }
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
                            this.request_surface(Surface::Security);
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
                            let context_selected = matches!(
                                sidebar_context_target,
                                Some(SidebarMenuTarget::Project { workspace })
                                    if workspace == &project
                            );
                            let expanded = active || open_projects.contains(&project);
                            let project_name = std::path::Path::new(&project)
                                .file_name()
                                .and_then(|name| name.to_str())
                                .unwrap_or("workspace")
                                .to_string();
                            let project_pull_request = if active {
                                current_pull_request.clone()
                            } else {
                                None
                            };
                            let pull_request_title = project_pull_request
                                .as_ref()
                                .and_then(|pull_request| pull_request.get("title"))
                                .and_then(serde_json::Value::as_str);
                            let sessions = state
                                .navigation
                                .sessions
                                .iter()
                                .filter(|session| {
                                    !session.archived
                                        && (session.workspace.as_ref() == project
                                            || (session.workspace.is_empty() && active))
                                        && pull_request_title != Some(session.title.as_ref())
                                })
                                .collect::<Vec<_>>();
                            let visible_count = if active && show_all_sessions {
                                sessions.len()
                            } else {
                                sessions.len().min(5)
                            };
                            let toggle_project = project.clone();
                            let new_project_session = project.clone();
                            let context_project = project.clone();
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
                                        } else if context_selected {
                                            palette.hover
                                        } else {
                                            palette.sidebar
                                        })
                                        .flex()
                                        .items_center()
                                        .on_mouse_down(
                                            MouseButton::Right,
                                            cx.listener(move |this, event: &gpui::MouseDownEvent, _, cx| {
                                                this.sidebar_context_menu =
                                                    Some(SidebarContextMenu {
                                                        target: SidebarMenuTarget::Project {
                                                            workspace: context_project.clone(),
                                                        },
                                                        position: event.position,
                                                    });
                                                cx.stop_propagation();
                                                cx.notify();
                                            }),
                                        )
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
                                                        this.switch_workspace(
                                                            &new_project_session,
                                                            "",
                                                            None,
                                                            true,
                                                            cx,
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
                                                .unwrap_or(locale.text("pr.single"))
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
                                                        let context_selected = matches!(
                                                            sidebar_context_target,
                                                            Some(SidebarMenuTarget::Session {
                                                                id,
                                                                ..
                                                            }) if id == session.id.as_ref()
                                                        );
                                                        let title = if session.title.is_empty() {
                                                            labels.new_conversation.to_string()
                                                        } else {
                                                            session.title.to_string()
                                                        };
                                                        let context_session_id = session_id.clone();
                                                        let context_workspace = workspace.clone();
                                                        let context_title = title.clone();
                                                        let context_pinned = session.pinned;
                                                        div()
                                                .id((
                                                    "project-session",
                                                    project_index * 1000 + session_index,
                                                ))
                                                .role(Role::Button)
                                                .aria_label(title.clone())
                                                .aria_selected(selected || context_selected)
                                                .tab_stop(true)
                                                .h(px(35.))
                                                .px(px(6.))
                                                .rounded(px(8.))
                                                .bg(if selected || context_selected {
                                                    palette.hover
                                                } else {
                                                    palette.sidebar
                                                })
                                                .text_color(if selected || context_selected {
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
                                                .on_mouse_down(
                                                    MouseButton::Right,
                                                    cx.listener(move |this, event: &gpui::MouseDownEvent, _, cx| {
                                                        this.sidebar_context_menu =
                                                            Some(SidebarContextMenu {
                                                                target: SidebarMenuTarget::Session {
                                                                    id: context_session_id.clone(),
                                                                    title: context_title.clone(),
                                                                    workspace: context_workspace.clone(),
                                                                    pinned: context_pinned,
                                                                },
                                                                position: event.position,
                                                            });
                                                        cx.stop_propagation();
                                                        cx.notify();
                                                    }),
                                                )
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if active {
                                                        let request_id = this.runtime.request(
                                                            Method::ResumeSession,
                                                            json!({"sessionId": session_id}),
                                                        );
                                                        this.pending_requests.insert(
                                                            request_id,
                                                            PendingRequest::ResumeSession {
                                                                sequence: None,
                                                            },
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                        cx.notify();
                                                    } else {
                                                        this.switch_workspace(
                                                            &workspace,
                                                            &session_id,
                                                            None,
                                                            false,
                                                            cx,
                                                        );
                                                    }
                                                }))
                                                .child(
                                                    div()
                                                        .flex_1()
                                                        .min_w_0()
                                                        .overflow_hidden()
                                                        .truncate()
                                                        .child(title),
                                                )
                                                .when(session.running, |row| {
                                                    row.child(
                                                        icon("loader", 13., palette.muted)
                                                            .with_animation(
                                                                (
                                                                    "sidebar-session-running",
                                                                    project_index * 1000
                                                                        + session_index,
                                                                ),
                                                                Animation::new(
                                                                    Duration::from_millis(800),
                                                                )
                                                                .repeat(),
                                                                |spinner, progress| {
                                                                    spinner.with_transformation(
                                                                        Transformation::rotate(
                                                                            percentage(progress),
                                                                        ),
                                                                    )
                                                                },
                                                            ),
                                                    )
                                                })
                                                .when(session.unread && !session.running, |row| {
                                                    row.child(
                                                        div()
                                                            .size(px(6.))
                                                            .flex_shrink_0()
                                                            .rounded_full()
                                                            .bg(palette.accent),
                                                    )
                                                })
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
                                                                locale.text("ui.showLess")
                                                            } else { locale.text("common.showMore") }),
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

fn sidebar_context_menu_item(
    id: &'static str,
    icon_name: &'static str,
    label: &'static str,
    action: SidebarMenuAction,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::MenuItem)
        .aria_label(label)
        .tab_stop(true)
        .h(px(30.))
        .px(px(8.))
        .rounded(px(6.))
        .text_size(px(13.))
        .text_color(palette.ink)
        .flex()
        .items_center()
        .gap(px(8.))
        .cursor_pointer()
        .hover(move |row| row.bg(palette.hover))
        .on_click(cx.listener(move |this, _, window, cx| {
            this.run_sidebar_menu_action(action, window, cx);
        }))
        .child(
            div()
                .w(px(16.))
                .child(icon(icon_name, 15., palette.ink_soft)),
        )
        .child(label)
        .into_any_element()
}

pub(super) fn sidebar_context_menu_view(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let menu = this
        .sidebar_context_menu
        .as_ref()
        .expect("sidebar context menu target exists");
    let (aria_label, items, separator_at) = match &menu.target {
        SidebarMenuTarget::Session { pinned, .. } => (
            locale.text("sidebar.sessionActions"),
            vec![
                (
                    "sidebar-menu-pin-session",
                    "anchor",
                    locale.text(if *pinned {
                        "sidebar.unpinSession"
                    } else {
                        "sidebar.pinSession"
                    }),
                    SidebarMenuAction::ToggleSessionPin,
                ),
                (
                    "sidebar-menu-rename-session",
                    "notebook-pen",
                    locale.text("sidebar.renameSession"),
                    SidebarMenuAction::RenameSession,
                ),
                (
                    "sidebar-menu-mark-unread",
                    "circle",
                    locale.text("sidebar.markUnread"),
                    SidebarMenuAction::MarkSessionUnread,
                ),
                (
                    "sidebar-menu-archive-session",
                    "archive",
                    locale.text("sidebar.archiveSession"),
                    SidebarMenuAction::ArchiveSession,
                ),
                (
                    "sidebar-menu-copy-workspace",
                    "folder",
                    locale.text("sidebar.copyWorkspace"),
                    SidebarMenuAction::CopyWorkspace,
                ),
                (
                    "sidebar-menu-copy-session-id",
                    "copy",
                    locale.text("sidebar.copySessionId"),
                    SidebarMenuAction::CopySessionId,
                ),
            ],
            Some(4),
        ),
        SidebarMenuTarget::Project { .. } => (
            locale.text("sidebar.projectActions"),
            vec![
                (
                    "sidebar-menu-reveal-project",
                    "folder",
                    locale.text("sidebar.showInFinder"),
                    SidebarMenuAction::RevealProject,
                ),
                (
                    "sidebar-menu-archive-project-sessions",
                    "archive",
                    locale.text("sidebar.archiveProjectSessions"),
                    SidebarMenuAction::ArchiveProjectSessions,
                ),
                (
                    "sidebar-menu-copy-project-path",
                    "copy",
                    locale.text("sidebar.copyProjectPath"),
                    SidebarMenuAction::CopyProjectPath,
                ),
                (
                    "sidebar-menu-remove-project",
                    "eye-off",
                    locale.text("sidebar.removeProject"),
                    SidebarMenuAction::RemoveProject,
                ),
            ],
            Some(3),
        ),
    };
    let mut content = div()
        .id("sidebar-context-menu")
        .role(Role::Menu)
        .aria_label(aria_label)
        .occlude()
        .w(px(172.))
        .p(px(5.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.12, 0.12, 0.18))
                .blur_radius(px(24.)),
        ])
        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_sidebar_context_menu));
    for (index, (id, icon_name, label, action)) in items.into_iter().enumerate() {
        if separator_at == Some(index) {
            content = content.child(div().h(px(1.)).mx(px(6.)).my(px(3.)).bg(palette.border));
        }
        content = content.child(sidebar_context_menu_item(
            id, icon_name, label, action, palette, cx,
        ));
    }
    deferred(
        gpui::anchored()
            .anchor(gpui::Anchor::TopLeft)
            .position(menu.position)
            .snap_to_window_with_margin(px(8.))
            .child(content),
    )
    .with_priority(50)
    .into_any_element()
}

pub(super) fn session_rename_modal(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let dialog = div()
        .id("session-rename-dialog")
        .role(Role::Region)
        .aria_label(locale.text("sidebar.renameTitle"))
        .occlude()
        .w(px(420.))
        .p(px(18.))
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(16.), hsla(220. / 360., 0.12, 0.12, 0.2))
                .blur_radius(px(38.)),
        ])
        .on_mouse_down_out(cx.listener(AzemWindow::cancel_session_rename))
        .child(
            div()
                .mb(px(12.))
                .text_size(px(15.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .text_color(palette.ink)
                .child(locale.text("sidebar.renameTitle")),
        )
        .child(
            div()
                .h(px(38.))
                .px_2()
                .rounded(px(8.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper_muted)
                .child(this.session_rename_input.clone()),
        )
        .child(
            div()
                .mt(px(16.))
                .flex()
                .justify_end()
                .gap_2()
                .child(
                    div()
                        .id("session-rename-cancel")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.cancel"))
                        .tab_stop(true)
                        .px_3()
                        .h(px(34.))
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_size(px(13.))
                        .text_color(palette.ink_soft)
                        .flex()
                        .items_center()
                        .cursor_pointer()
                        .hover(move |button| button.bg(palette.hover))
                        .on_click(cx.listener(AzemWindow::cancel_session_rename_click))
                        .child(locale.text("ui.cancel")),
                )
                .child(
                    div()
                        .id("session-rename-save")
                        .role(Role::Button)
                        .aria_label(locale.text("sidebar.saveRename"))
                        .tab_stop(true)
                        .px_3()
                        .h(px(34.))
                        .rounded(px(8.))
                        .bg(palette.button)
                        .text_size(px(13.))
                        .text_color(palette.button_text)
                        .flex()
                        .items_center()
                        .cursor_pointer()
                        .on_click(cx.listener(AzemWindow::commit_session_rename_click))
                        .child(locale.text("sidebar.saveRename")),
                ),
        );
    div()
        .id("session-rename-backdrop")
        .absolute()
        .occlude()
        .size_full()
        .bg(hsla(220. / 360., 0.08, 0.18, 0.24))
        .flex()
        .items_center()
        .justify_center()
        .child(dialog)
        .into_any_element()
}

pub(super) fn search_surface(
    state: &AppState,
    input: Entity<TextInput>,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let commands = [
        ("plus", locale.text("ui.newConversation"), "⌘N", "new"),
        (
            "file-code",
            locale.text("ui.viewProjectFiles"),
            "⌘2",
            "files",
        ),
        (
            "file-diff",
            locale.text("ui.viewCodeChanges"),
            "⌘3",
            "changes",
        ),
        (
            "shield-check",
            locale.text("ui.openSecurityScans"),
            locale.text("ui.workspace"),
            "security",
        ),
        (
            "sparkles",
            locale.text("ui.openMotionSettings"),
            "⌘,",
            "settings",
        ),
        (
            "terminal",
            locale.text("ui.openOrCloseTerminal"),
            "⌘`",
            "terminal",
        ),
    ];
    div()
        .id("search-surface")
        .role(Role::Search)
        .aria_label(labels.search)
        .absolute()
        .size_full()
        .bg(rgba(0x16161338))
        .px(px(22.))
        .pt(px(150.))
        .flex()
        .justify_center()
        .items_center()
        .child(
            div()
                .w_full()
                .max_w(px(590.))
                .max_h(px(560.))
                .rounded(px(15.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(24.), hsla(40. / 360., 0.1, 0.1, 0.22))
                        .blur_radius(px(70.)),
                ])
                .overflow_hidden()
                .flex()
                .flex_col()
                .child(
                    div()
                        .id("search-field")
                        .h(px(53.))
                        .px(px(14.))
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().w(px(18.)).child(icon("search", 17., palette.faint)))
                        .child(div().flex_1().min_w_0().child(input))
                        .child(
                            div()
                                .text_color(palette.faint)
                                .text_size(px(9.))
                                .child("esc"),
                        ),
                )
                .child(
                    div()
                        .id("search-results")
                        .max_h(px(470.))
                        .overflow_y_scroll()
                        .p(px(8.))
                        .flex()
                        .flex_col()
                        .gap(px(2.))
                        .when(state.navigation.search_results.is_empty(), |list| {
                            list.child(
                                div()
                                    .h(px(24.))
                                    .px_2()
                                    .text_color(palette.faint)
                                    .text_size(px(9.))
                                    .font_weight(gpui::FontWeight::BOLD)
                                    .flex()
                                    .items_center()
                                    .child(locale.text("ui.actions")),
                            )
                            .children(
                                commands.into_iter().enumerate().map(
                                    |(index, (icon_name, label, shortcut, action))| {
                                        div()
                                            .id(("search-command", index))
                                            .role(Role::Button)
                                            .aria_label(label)
                                            .tab_stop(true)
                                            .h(px(40.))
                                            .px_2()
                                            .rounded(px(8.))
                                            .bg(if index == 0 {
                                                palette.hover
                                            } else {
                                                palette.paper
                                            })
                                            .text_color(if index == 0 {
                                                palette.ink
                                            } else {
                                                palette.muted
                                            })
                                            .text_size(px(12.))
                                            .font_weight(gpui::FontWeight::MEDIUM)
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .cursor_pointer()
                                            .hover(move |style| {
                                                style.bg(palette.hover).text_color(palette.ink)
                                            })
                                            .on_click(cx.listener(move |this, _, window, cx| {
                                                match action {
                                                    "new" => {
                                                        this.runtime.request(
                                                            Method::Execute,
                                                            json!({"kind": "new_session"}),
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                    }
                                                    "files" => {
                                                        this.state.navigation.surface =
                                                            Surface::Files;
                                                        this.request_surface(Surface::Files);
                                                    }
                                                    "changes" => {
                                                        this.state.navigation.surface =
                                                            Surface::Changes;
                                                        this.request_surface(Surface::Changes);
                                                    }
                                                    "security" => {
                                                        this.state.navigation.surface =
                                                            Surface::Security;
                                                        this.request_surface(Surface::Security);
                                                    }
                                                    "settings" => {
                                                        this.state.navigation.surface =
                                                            this.search_return_surface;
                                                        this.settings_section =
                                                            "appearance".to_string();
                                                        this.settings_open = true;
                                                    }
                                                    "terminal" => {
                                                        this.state.navigation.surface =
                                                            this.search_return_surface;
                                                        this.set_terminal_open(true, window, cx);
                                                    }
                                                    _ => {}
                                                }
                                                cx.notify();
                                            }))
                                            .child(
                                                div()
                                                    .w(px(24.))
                                                    .flex()
                                                    .justify_center()
                                                    .child(icon(icon_name, 15., rgb(0x1f7af0))),
                                            )
                                            .child(div().flex_1().child(label))
                                            .child(
                                                div()
                                                    .text_color(palette.faint)
                                                    .text_size(px(9.))
                                                    .child(shortcut),
                                            )
                                    },
                                ),
                            )
                        })
                        .when(!state.navigation.search_error.is_empty(), |list| {
                            list.child(
                                div()
                                    .id("search-error")
                                    .role(Role::Alert)
                                    .px_3()
                                    .py_2()
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
                                    .unwrap_or(locale.text("ui.untitledConversation"))
                                    .to_string();
                                let preview = result
                                    .get("preview")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let sequence =
                                    result.get("sequence").and_then(serde_json::Value::as_i64);
                                let current_workspace = state.workspace.root.to_string();
                                div()
                                    .id(("search-result", index))
                                    .role(Role::Button)
                                    .aria_label(title.clone())
                                    .tab_stop(true)
                                    .min_h(px(44.))
                                    .px_3()
                                    .py_2()
                                    .rounded(px(8.))
                                    .text_color(palette.muted)
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .cursor_pointer()
                                    .hover(move |style| {
                                        style.bg(palette.hover).text_color(palette.ink)
                                    })
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        if workspace.is_empty() || workspace == current_workspace {
                                            let request_id = this.runtime.request(
                                                Method::ResumeSession,
                                                json!({"sessionId": session_id}),
                                            );
                                            this.pending_requests.insert(
                                                request_id,
                                                PendingRequest::ResumeSession { sequence },
                                            );
                                            this.state.navigation.surface = Surface::Thread;
                                        } else {
                                            this.switch_workspace(
                                                &workspace,
                                                &session_id,
                                                sequence,
                                                false,
                                                cx,
                                            );
                                        }
                                        cx.notify();
                                    }))
                                    .child(
                                        div()
                                            .text_sm()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(title),
                                    )
                                    .when(!preview.is_empty(), |row| {
                                        row.child(
                                            div()
                                                .truncate()
                                                .text_color(palette.faint)
                                                .text_xs()
                                                .child(preview),
                                        )
                                    })
                            },
                        )),
                )
                .child(
                    div()
                        .h(px(31.))
                        .px_3()
                        .border_t_1()
                        .border_color(palette.border)
                        .text_color(palette.faint)
                        .text_size(px(9.))
                        .flex()
                        .items_center()
                        .justify_end()
                        .gap_3()
                        .child(locale.text("ui.open"))
                        .child(locale.text("ui.navigate"))
                        .child(locale.text("ui.escClose")),
                ),
        )
        .into_any_element()
}
pub(super) const SIDE_PANEL_TRANSITION: Duration = Duration::from_millis(220);
const ENVIRONMENT_PANEL_RETURN_TRANSITION: Duration = Duration::from_millis(500);

fn environment_panel_return_spring(progress: f32) -> f32 {
    if progress >= 1. {
        1.
    } else {
        let phase = 5.5 * progress;
        1. - (-4. * progress).exp() * (phase.cos() + 0.73 * phase.sin())
    }
}

#[derive(Debug, Default)]
struct EnvironmentSnapshot {
    has_subagents: bool,
    running_agents: usize,
    completed_agents: usize,
    running_terminals: usize,
    current_pull_request: Option<String>,
}

fn environment_snapshot(state: &AppState) -> EnvironmentSnapshot {
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

pub(super) fn environment_panel(
    this: &AzemWindow,
    palette: ThemePalette,
    labels: Labels,
    expanded: Option<&str>,
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
    let recap_revision = state
        .runtime
        .recap
        .get("revision")
        .and_then(serde_json::Value::as_i64)
        .map(|revision| format!("r{revision}"))
        .unwrap_or_else(|| "—".to_string());
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
            BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.12, 0.12, 0.10))
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
        .child(environment_nav_row(
            "environment-local",
            "folder",
            locale.text("ui.local").to_string(),
            "⌄".to_string(),
            Some(Surface::Files),
            palette,
            cx,
        ))
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
        .child(environment_label(locale.text("ui.conversation"), palette))
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
                                .py_1()
                                .text_color(if status == "completed" {
                                    palette.muted
                                } else {
                                    palette.ink_soft
                                })
                                .text_xs()
                                .flex()
                                .items_start()
                                .gap_2()
                                .child(
                                    div()
                                        .w(px(12.))
                                        .flex_shrink_0()
                                        .line_height(px(18.))
                                        .child(if status == "completed" { "✓" } else { "○" }),
                                )
                                .child(
                                    div()
                                        .min_w_0()
                                        .flex_1()
                                        .line_height(px(18.))
                                        .whitespace_normal()
                                        .child(content),
                                )
                        }),
                    ))
                })
        })
        .child(environment_expand_row(
            "history",
            locale.text("ui.recap").to_string(),
            format!("{recap_revision}  ⌄"),
            "recap",
            expanded == Some("recap"),
            palette,
            cx,
        ))
        .when(expanded == Some("recap"), |panel| {
            panel.child(recap_panel(&state.runtime.recap, palette, locale))
        })
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

pub(super) fn side_panel(
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

pub(super) fn agent_panel_tab(
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

pub(super) fn agent_side_panel(
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

fn projected_agent_blocks(values: &[serde_json::Value]) -> Vec<Block> {
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
            process_step_row(index, 0, block, (palette, locale, reduced_motion))
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
    timeline_entry(index, blocks, style, agents, expansion, owner, None)
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

fn recap_copy(recap: &serde_json::Value) -> (String, String, String) {
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
        .px_2()
        .pb_1()
        .max_h(px(180.))
        .overflow_y_scroll()
        .text_xs()
        .line_height(px(17.))
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
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .font_family("SF Mono")
                .text_size(px(9.))
                .text_color(palette.faint)
                .child(label),
        )
        .child(div().whitespace_normal().child(content))
}

#[allow(clippy::too_many_arguments)]
pub(super) fn settings_surface(
    state: &AppState,
    palette: ThemePalette,
    section: &str,
    selected_provider: &str,
    catalog_searches: (Entity<TextInput>, Entity<TextInput>),
    scrolls: (UniformListScrollHandle, UniformListScrollHandle),
    route_picker: (Option<&RoutePickerTarget>, Option<gpui::AnyElement>),
    subagent_setting_menu: Option<SubagentSettingKind>,
    archive: (u32, bool, &HashSet<String>),
    usage_hover: Option<&(String, i64)>,
    extensions: &ExtensionSettings,
    native: &NativeSettings,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (section, extension_tab) = settings_section_parts(section);
    let (provider_scroll, model_scroll) = scrolls;
    let (route_picker_target, route_picker) = route_picker;
    let locale = Locale::resolve(&state.settings.language);
    let settings_query = native.search.read(cx).text().to_string();
    let nav_groups = [
        (
            locale.text("ui.system2"),
            vec![
                ("catalog", locale.text("ui.modelCatalog"), "database"),
                ("routes", locale.text("ui.modelRouting"), "bot"),
                ("agents", locale.text("ui.subagents"), "gauge"),
                ("security", locale.text("ui.securityScan"), "shield-check"),
            ],
        ),
        (
            locale.text("ui.preferences"),
            vec![
                (
                    "governance",
                    locale.text("ui.governance"),
                    "sliders-horizontal",
                ),
                ("appearance", locale.text("ui.appearance"), "palette"),
                ("extensions", locale.text("ui.extensions"), "puzzle"),
                ("archive", locale.text("ui.archive"), "archive"),
                ("usage", locale.text("ui.usage"), "chart"),
            ],
        ),
    ];
    let (title, description) = match section {
        "routes" => (
            locale.text("ui.modelRouting"),
            locale.text("ui.inspectProvidersAndModelsUsedByTheMainAgentTeam"),
        ),
        "agents" => (
            locale.text("ui.subagents"),
            locale.text("ui.configureConcurrencyAndIsolationThenInspectSchedulingAndMainSession"),
        ),
        "security" => (
            locale.text("ui.securityScans"),
            locale.text("ui.reviewSecurityPolicyScanStateAndFindings"),
        ),
        "governance" => (
            locale.text("ui.governance"),
            locale.text("ui.controlToolApprovalAndHowFollowUpMessagesAreDelivered"),
        ),
        "appearance" => (
            locale.text("ui.appearance"),
            locale.text("ui.chooseTheDesktopLanguageAndInspectAvailableThemes"),
        ),
        "extensions" => (
            locale.text("ui.extensions"),
            locale.text("ui.reviewSkillsPluginsMcpServersAndHooks"),
        ),
        "archive" => (
            locale.text("ui.archive"),
            locale.text("ui.inspectDeterministicContextArchivesRecapsAndRecoveryState"),
        ),
        "usage" => (
            locale.text("ui.usage"),
            locale.text("ui.reviewCurrentSessionAndProviderUsageSignals"),
        ),
        _ => (
            locale.text("ui.modelCatalog"),
            locale
                .text("ui.manageSubscriptionModelsAndOpenaiCompatibleProvidersCredentialsRemainIn"),
        ),
    };
    let body = match section {
        "routes" => settings_routes_body(
            state,
            palette,
            locale,
            route_picker_target,
            route_picker,
            cx,
        ),
        "agents" => settings_subagents_body(state, palette, locale, subagent_setting_menu, cx),
        "security" => settings_security_body(state, native, palette, locale, cx),
        "governance" => settings_governance_body(state, palette, locale, cx),
        "appearance" => settings_appearance_body(state, native, palette, locale, cx),
        "extensions" => {
            settings_extensions_body(state, palette, locale, extension_tab, extensions, cx)
        }
        "archive" => settings_archive_body(state, palette, locale, archive, cx),
        "usage" => settings_usage_body(state, palette, locale, usage_hover, cx),
        _ => settings_catalog_body(
            state,
            palette,
            locale,
            selected_provider,
            catalog_searches,
            (provider_scroll, model_scroll),
            cx,
        ),
    };
    div()
        .id("settings-surface")
        .relative()
        .role(Role::Region)
        .aria_label(locale.text("ui.settings"))
        .flex_1()
        .min_w_0()
        .min_h_0()
        .h_full()
        .rounded(px(17.))
        .overflow_hidden()
        .flex()
        .child(
            div()
                .id("settings-navigation")
                .role(Role::Navigation)
                .aria_label(locale.text("ui.settingsCategories"))
                .w(px(220.))
                .h_full()
                .min_h_0()
                .border_r_1()
                .border_color(palette.border)
                .bg(palette.sidebar)
                .rounded_tl(px(13.))
                .rounded_bl(px(13.))
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
                        .aria_label(locale.text("ui.backToWorkspace"))
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
                            this.archive_days_menu_open = false;
                            cx.notify();
                        }))
                        .child("←")
                        .child(locale.text("ui.backToWorkspace")),
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
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .h_full()
                                .child(native.search.clone()),
                        )
                        .child(div().text_size(px(9.)).child("⌘F")),
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
                        .children(
                            entries
                                .into_iter()
                                .filter(|(id, label, _)| {
                                    settings_search_matches(id, label, &settings_query, locale)
                                })
                                .map(|(id, label, icon_name)| {
                                    settings_navigation_item(
                                        id,
                                        label,
                                        icon_name,
                                        section == id,
                                        palette,
                                        cx,
                                    )
                                }),
                        )
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
                        .child(locale.text("ui.settingsStoredLocally"))
                        .child(div().flex_1())
                        .child(format!("Azem v{}", env!("CARGO_PKG_VERSION"))),
                ),
        )
        .child(
            div()
                .id("settings-content")
                .flex_1()
                .min_w_0()
                .min_h_0()
                .h_full()
                .bg(palette.paper)
                .rounded_tr(px(13.))
                .rounded_br(px(13.))
                .when(section == "catalog", |content| content.overflow_hidden())
                .when(section != "catalog", |content| content.overflow_y_scroll())
                .px(px(34.))
                .pt(px(28.))
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
                                        .text_size(px(26.))
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
                                    .aria_label(locale.text("ui.addProvider"))
                                    .tab_stop(true)
                                    .h(px(38.))
                                    .px_3()
                                    .rounded(px(9.))
                                    .bg(palette.button)
                                    .text_color(palette.button_text)
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
                                    .child(locale.text("ui.addProvider")),
                            )
                        }),
                )
                .child(body),
        )
        .when(!state.settings.error.is_empty(), |root| {
            root.child(
                div()
                    .id("settings-error")
                    .role(Role::Alert)
                    .aria_label(state.settings.error.to_string())
                    .absolute()
                    .bottom(px(10.))
                    .left(px(232.))
                    .right(px(20.))
                    .px_3()
                    .py_2()
                    .rounded(px(8.))
                    .border_1()
                    .border_color(palette.danger)
                    .bg(palette.paper)
                    .text_color(palette.danger)
                    .text_sm()
                    .occlude()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .child(state.settings.error.to_string()),
                    )
                    .child(
                        div()
                            .id("dismiss-settings-error")
                            .role(Role::Button)
                            .aria_label(locale.text("ui.dismissError"))
                            .tab_stop(true)
                            .cursor_pointer()
                            .p_1()
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.state.settings.error = "".into();
                                cx.notify();
                            }))
                            .child("×"),
                    ),
            )
        })
        .into_any_element()
}

fn settings_section_parts(section: &str) -> (&str, &str) {
    section
        .strip_prefix("extensions:")
        .map_or((section, "mcp"), |tab| ("extensions", tab))
}

fn settings_search_matches(section: &str, label: &str, query: &str, locale: Locale) -> bool {
    let key = match section {
        "catalog" => "search.catalog",
        "routes" => "search.routes",
        "agents" => "search.agents",
        "security" => "search.security",
        "governance" => "search.governance",
        "appearance" => "search.appearance",
        "extensions" => "search.extensions",
        "archive" => "search.archive",
        "usage" => "search.usage",
        _ => "",
    };
    let keywords = locale.text(key);
    let haystack = format!("{label} {keywords}").to_lowercase();
    query
        .split_whitespace()
        .all(|word| haystack.contains(&word.to_lowercase()))
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
        .h(px(38.))
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
            this.select_settings_section(id, cx);
        }))
        .child(icon(
            icon_name,
            15.,
            pick(selected, palette.accent, palette.faint),
        ))
        .child(label)
        .into_any_element()
}

fn settings_routes_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    route_picker_target: Option<&RoutePickerTarget>,
    route_picker: Option<gpui::AnyElement>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let core = state
        .catalogs
        .routes
        .iter()
        .filter(|route| {
            is_core_settings_route(
                route
                    .get("scope")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default(),
            )
        })
        .cloned()
        .collect::<Vec<_>>();
    let subagents = state
        .catalogs
        .routes
        .iter()
        .filter(|route| route.get("scope").and_then(serde_json::Value::as_str) == Some("subagent"))
        .cloned()
        .collect::<Vec<_>>();
    let (core_picker, subagent_picker) =
        if route_picker_target.is_some_and(|target| target.scope == "subagent") {
            (None, route_picker)
        } else {
            (route_picker, None)
        };
    div()
        .w_full()
        .flex()
        .items_start()
        .gap_4()
        .child(settings_route_card(
            (
                locale.text("ui.coreWorkflows"),
                locale.text("ui.mainConversationAndCriticalDecisions"),
            ),
            core,
            state,
            (route_picker_target, core_picker),
            palette,
            locale,
            cx,
        ))
        .child(settings_route_card(
            (
                locale.text("ui.subagentDefaults"),
                locale.text("ui.rolesCanStillOverrideThisSetting"),
            ),
            subagents,
            state,
            (route_picker_target, subagent_picker),
            palette,
            locale,
            cx,
        ))
        .into_any_element()
}

fn is_core_settings_route(scope: &str) -> bool {
    !matches!(scope, "main" | "subagent" | "security")
}

fn settings_route_card(
    copy: (&'static str, &'static str),
    routes: Vec<serde_json::Value>,
    state: &AppState,
    route_picker: (Option<&RoutePickerTarget>, Option<gpui::AnyElement>),
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (title, description) = copy;
    let (route_picker_target, mut route_picker) = route_picker;
    let empty = routes.is_empty();
    let has_picker = route_picker.is_some();
    let mut rows = Vec::with_capacity(routes.len());
    for route in routes {
        let scope = route
            .get("scope")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let role = route
            .get("role")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let expanded_kind = route_picker_target
            .filter(|target| target.scope == scope && target.role == role)
            .map(|target| target.kind);
        let row_picker = expanded_kind.and_then(|_| route_picker.take());
        rows.push(settings_route_row(
            route,
            state,
            expanded_kind,
            row_picker,
            palette,
            locale,
            cx,
        ));
    }
    div()
        .flex_1()
        .min_w(px(320.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .when(!has_picker, |card| card.overflow_hidden())
        .child(settings_card_header(title, description, palette))
        .when(empty, |card| {
            card.child(
                div()
                    .h(px(72.))
                    .border_t_1()
                    .border_color(palette.border)
                    .text_color(palette.faint)
                    .text_sm()
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(locale.text("ui.noModelRoutes")),
            )
        })
        .children(rows)
        .into_any_element()
}

fn settings_route_title(scope: &str, role: &str, label: &str, locale: Locale) -> String {
    match scope {
        "main" => locale.text("ui.main").to_string(),
        "title" => locale.text("ui.conversationTitle").into(),
        "plan" => locale.text("ui.planningModel").into(),
        "approval" => locale.text("ui.approvalModel").into(),
        "vision" => locale.text("ui.visionModel").into(),
        "recap" => locale.text("ui.recapModel").into(),
        _ if role == "research" => locale.text("ui.researchAndDocumentation").into(),
        _ if role == "review" => locale.text("ui.codingAndReview").into(),
        _ if !role.is_empty() => role.to_string(),
        _ => label.to_string(),
    }
}

fn settings_route_provider_name(providers: &[serde_json::Value], provider_id: &str) -> String {
    providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
        })
        .and_then(|provider| {
            ["displayName", "name"]
                .into_iter()
                .find_map(|key| provider.get(key).and_then(serde_json::Value::as_str))
        })
        .unwrap_or(provider_id)
        .to_string()
}

fn settings_route_model_name(
    providers: &[serde_json::Value],
    provider_id: &str,
    model_id: &str,
) -> String {
    providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
        })
        .and_then(|provider| provider.get("models"))
        .and_then(serde_json::Value::as_array)
        .and_then(|models| {
            models
                .iter()
                .find(|model| model.get("id").and_then(serde_json::Value::as_str) == Some(model_id))
        })
        .map(|model| catalog_model_name(model, model_id))
        .unwrap_or_else(|| model_id.to_string())
}

fn settings_route_row(
    route: serde_json::Value,
    state: &AppState,
    expanded_kind: Option<RoutePickerKind>,
    route_picker: Option<gpui::AnyElement>,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let providers = &state.catalogs.providers;
    let scope = route
        .get("scope")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let role = route
        .get("role")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let label = route
        .get("label")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let title = settings_route_title(scope, role, &label, locale);
    let description = match scope {
        "main" => locale.text("ui.mainConversationAndTools"),
        "title" => locale.text("ui.generateTheSidebarTitleFromTheFirstUserMessage"),
        "plan" => locale.text("ui.planningAndTaskDecomposition"),
        "approval" => locale.text("ui.structuredApprovalReview"),
        "vision" => locale.text("ui.imageAndVisualRouting"),
        "recap" => locale.text("ui.briefPostTurnRecap"),
        _ if role == "research" => locale.text("ui.researchAndDocumentation2"),
        _ if role == "review" => locale.text("ui.implementationAndReview"),
        _ => "",
    }
    .to_string();
    let description = if description.is_empty() && !label.is_empty() && label != role {
        label.clone()
    } else if description.is_empty() {
        locale.text("ui.defaultRoleRoute").to_string()
    } else {
        description
    };
    let route_value = route.get("route").unwrap_or(&route);
    let configured_provider = route_value
        .get("provider")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.provider.as_ref());
    let configured_model = route_value
        .get("model")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.model.as_ref());
    let provider = configured_provider.to_string();
    let model = settings_route_model_name(providers, configured_provider, configured_model);
    let provider_name = settings_route_provider_name(providers, configured_provider);
    let reasoning = route_value
        .get("reasoning")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.reasoning.as_ref());
    let reasoning = Some(reasoning)
        .map(|value| match value {
            "low" => locale.text("ui.low"),
            "medium" => locale.text("ui.medium"),
            "high" => locale.text("ui.high"),
            "xhigh" => locale.text("ui.xhigh"),
            "max" | "ultra" => locale.text("ui.max"),
            _ => value,
        })
        .unwrap_or("—")
        .to_string();
    let route_id = format!("{}-{}", scope, role);
    let model_aria = format!("{} {}", title, locale.text("ui.model"));
    let reasoning_aria = format!("{} {}", title, locale.text("ui.reasoning"));
    let model_scope = scope.to_string();
    let model_role = role.to_string();
    let model_label = label.clone();
    let reasoning_scope = scope.to_string();
    let reasoning_role = role.to_string();
    let reasoning_label = label;
    div()
        .id(format!("settings-route-{route_id}"))
        .min_h(px(62.))
        .px_3()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(provider_logo(&provider, 19., palette.ink))
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
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(
                    div()
                        .truncate()
                        .text_color(palette.faint)
                        .text_xs()
                        .child(description),
                ),
        )
        .child(
            div()
                .relative()
                .w(px(270.))
                .flex()
                .items_center()
                .gap(px(6.))
                .child(
                    settings_route_value(
                        model,
                        provider_name,
                        px(176.),
                        expanded_kind == Some(RoutePickerKind::Model),
                        palette,
                    )
                    .id(format!("settings-route-model-{route_id}"))
                    .role(Role::Button)
                    .aria_label(model_aria)
                    .tab_stop(true)
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.open_route_picker(
                            model_scope.clone(),
                            model_role.clone(),
                            model_label.clone(),
                            RoutePickerKind::Model,
                            window,
                            cx,
                        );
                    })),
                )
                .child(
                    settings_route_value(
                        reasoning,
                        String::new(),
                        px(88.),
                        expanded_kind == Some(RoutePickerKind::Reasoning),
                        palette,
                    )
                    .id(format!("settings-route-reasoning-{route_id}"))
                    .role(Role::Button)
                    .aria_label(reasoning_aria)
                    .tab_stop(true)
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.open_route_picker(
                            reasoning_scope.clone(),
                            reasoning_role.clone(),
                            reasoning_label.clone(),
                            RoutePickerKind::Reasoning,
                            window,
                            cx,
                        );
                    })),
                )
                .when_some(route_picker, |controls, picker| {
                    controls.child(deferred(picker).with_priority(10))
                }),
        )
        .into_any_element()
}

fn settings_route_value(
    primary: String,
    secondary: String,
    width: Pixels,
    expanded: bool,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .w(width)
        .h(px(40.))
        .px_2()
        .rounded(px(8.))
        .bg(palette.paper_muted)
        .text_color(palette.muted)
        .flex()
        .items_center()
        .gap_2()
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .child(
                    div()
                        .truncate()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(primary),
                )
                .when(!secondary.is_empty(), |value| {
                    value.child(div().truncate().text_xs().child(secondary))
                }),
        )
        .child(icon(
            if expanded {
                "chevron-up"
            } else {
                "chevron-down"
            },
            12.,
            palette.muted,
        ))
}

fn settings_subagents_body(
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

fn settings_detail_card(
    title: &'static str,
    description: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(title, description, palette))
        .children(rows)
        .into_any_element()
}

fn settings_unclipped_detail_card(
    title: &'static str,
    description: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .relative()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .child(settings_card_header(title, description, palette))
        .children(rows)
        .into_any_element()
}

fn settings_detail_row(
    title: &'static str,
    description: &'static str,
    value: String,
    palette: ThemePalette,
) -> gpui::AnyElement {
    settings_control_detail_row(
        title,
        description,
        div()
            .min_w(px(112.))
            .h(px(34.))
            .px_3()
            .rounded(px(8.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .text_color(palette.ink_soft)
            .text_sm()
            .font_weight(gpui::FontWeight::SEMIBOLD)
            .flex()
            .items_center()
            .justify_center()
            .child(value)
            .into_any_element(),
        palette,
    )
}

fn settings_control_detail_row(
    title: &'static str,
    description: &'static str,
    control: gpui::AnyElement,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .min_h(px(66.))
        .px_4()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_4()
        .child(
            div()
                .min_w_0()
                .flex_1()
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
        .child(control)
        .into_any_element()
}

fn settings_switch(on: bool, palette: ThemePalette) -> gpui::Div {
    div()
        .w(px(34.))
        .h(px(20.))
        .p(px(2.))
        .rounded_full()
        .bg(if on {
            palette.positive
        } else {
            palette.border_strong
        })
        .flex()
        .justify_end()
        .when(!on, |switch| switch.justify_start())
        .child(
            div()
                .size(px(16.))
                .rounded_full()
                .bg(rgb(0xffffff))
                .shadow(vec![
                    BoxShadow::new(px(0.), px(1.), hsla(0., 0., 0., 0.18)).blur_radius(px(2.)),
                ]),
        )
}

fn settings_catalog_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    selected_provider: &str,
    searches: (Entity<TextInput>, Entity<TextInput>),
    scrolls: (UniformListScrollHandle, UniformListScrollHandle),
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (provider_search, model_search) = searches;
    let (provider_scroll, model_scroll) = scrolls;
    let provider_query = provider_search.read(cx).text().trim().to_ascii_lowercase();
    let model_query = model_search.read(cx).text().trim().to_ascii_lowercase();
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
    let selected_provider_id = selected
        .as_ref()
        .and_then(|provider| provider.get("id"))
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let provider_rows = provider_inventory
        .into_iter()
        .enumerate()
        .filter(|(_, provider)| provider_matches_query(provider, &provider_query))
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
        let subscription = provider
            .get("subscription")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_available = provider
            .get("quotaAvailable")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        let quota_remaining = provider_quota_remaining(&provider).unwrap_or(0.);
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
        let provider_action = model_provider_action(&provider, &session_id);
        let model_rows = models
            .iter()
            .enumerate()
            .filter(|(_, model)| model_matches_query(model, &model_query))
            .map(|(index, model)| (index, model.clone()))
            .collect::<Vec<_>>();
        let model_list = if model_rows.is_empty() {
            div()
                .h(px(96.))
                .w_full()
                .text_color(palette.faint)
                .text_sm()
                .flex()
                .items_center()
                .justify_center()
                .child(if model_query.is_empty() {
                    locale.text("ui.thisProviderHasNoModels")
                } else {
                    locale.text("ui.noMatchingModels")
                })
                .into_any_element()
        } else {
            let model_row_count = model_rows.len().div_ceil(2);
            let model_provider_id = provider_id.clone();
            let model_logo_id = logo_id.clone();
            let model_session_id = session_id.clone();
            uniform_list(
                "provider-model-grid",
                model_row_count,
                cx.processor(move |_this, range: std::ops::Range<usize>, _window, cx| {
                    range
                        .map(|row_index| {
                            let mut row = div().w_full().h(px(144.)).flex().gap(px(12.));
                            for column in 0..2 {
                                let Some((index, model)) =
                                    model_rows.get(row_index * 2 + column)
                                else {
                                    row = row.child(div().min_w_0().flex_1());
                                    continue;
                                };
                                let model_id = model
                                    .get("id")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let model_name = catalog_model_name(model, &model_id);
                                let disabled = model
                                    .get("disabled")
                                    .and_then(serde_json::Value::as_bool)
                                    .unwrap_or(false);
                                let capabilities = model_capability_keys(model, subscription);
                                let provider_target = model_provider_id.clone();
                                let model_target = model_id.clone();
                                let session_target = model_session_id.clone();
                                let card = div()
                                    .id(("provider-model-card", *index))
                                    .w_full()
                                    .h(px(132.))
                                    .p(px(15.))
                                    .rounded(px(12.))
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
                                            .child(provider_logo(
                                                &model_logo_id,
                                                14.,
                                                palette.muted,
                                            ))
                                            .child(
                                                div()
                                                    .min_w_0()
                                                    .flex_1()
                                                    .truncate()
                                                    .text_color(if disabled {
                                                        palette.muted
                                                    } else {
                                                        palette.ink
                                                    })
                                                    .text_sm()
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(model_name),
                                            )
                                            .child(
                                                div()
                                                    .flex()
                                                    .items_center()
                                                    .gap_2()
                                                    .child(
                                                        div()
                                                            .text_color(palette.muted)
                                                            .text_size(px(10.))
                                                            .child(if disabled {
                                                                locale.text("ui.disabled3")
                                                            } else {
                                                                locale.text("ui.enabled")
                                                            }),
                                                    )
                                                    .child(
                                                        div()
                                                            .id(("model-enabled", *index))
                                                            .role(Role::Button)
                                                            .aria_label(if disabled {
                                                                locale.text("ui.enableModel")
                                                            } else {
                                                                locale.text("ui.disableModel")
                                                            })
                                                            .aria_selected(!disabled)
                                                            .tab_stop(true)
                                                            .w(px(38.))
                                                            .h(px(22.))
                                                            .rounded_full()
                                                            .bg(if disabled {
                                                                palette.border_strong
                                                            } else {
                                                                palette.positive
                                                            })
                                                            .p(px(2.))
                                                            .flex()
                                                            .justify_end()
                                                            .when(disabled, |toggle| {
                                                                toggle.justify_start()
                                                            })
                                                            .cursor_pointer()
                                                            .on_click(cx.listener(move |
                                                                this, _, _, cx,
                                                            | {
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
                                                            .child(
                                                                div()
                                                                    .size(px(18.))
                                                                    .rounded_full()
                                                                    .bg(palette.paper),
                                                            ),
                                                    ),
                                            ),
                                    )
                                    .child(div().flex_1())
                                    .when(!capabilities.is_empty(), |card| {
                                        card.child(settings_model_capabilities(
                                            &capabilities,
                                            *index,
                                            palette,
                                            locale,
                                        ))
                                    });
                                row = row.child(div().min_w_0().flex_1().child(card));
                            }
                            row
                        })
                        .collect::<Vec<_>>()
                }),
            )
            .track_scroll(&model_scroll)
            .h_full()
            .into_any_element()
        };
        let refresh_action = model_discovery_request(&provider, &session_id);
        div()
            .min_w_0()
            .flex_1()
            .h_full()
            .min_h_0()
            .flex()
            .flex_col()
            .gap(px(14.))
            .child(
                div()
                    .min_h(px(74.))
                    .px(px(18.))
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
                                        locale.text("ui.available2")
                                    } else {
                                        locale.text("ui.disabled3")
                                    }),
                            )
                            .when(subscription, |actions| {
                                let action = provider_action.clone();
                                actions.child(
                                    div()
                                        .id(if enabled {
                                            "provider-logout"
                                        } else {
                                            "provider-login"
                                        })
                                        .role(Role::Button)
                                        .aria_label(locale.text(if enabled {
                                            "ui.signOut"
                                        } else {
                                            "ui.signIn"
                                        }))
                                        .tab_stop(true)
                                        .h(px(30.))
                                        .px_2()
                                        .rounded(px(8.))
                                        .border_1()
                                        .border_color(palette.border)
                                        .text_color(if enabled {
                                            palette.danger
                                        } else {
                                            palette.accent
                                        })
                                        .text_xs()
                                        .flex()
                                        .items_center()
                                        .cursor_pointer()
                                        .hover(move |style| style.bg(palette.hover))
                                        .active(|style| style.opacity(0.72))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.runtime.request(Method::Execute, action.clone());
                                            cx.notify();
                                        }))
                                        .child(locale.text(if enabled {
                                            "ui.signOut"
                                        } else {
                                            "ui.signIn"
                                        })),
                                )
                            })
                            .when(!subscription, |actions| {
                                let action = provider_action.clone();
                                actions.child(
                                    div()
                                        .id("provider-enabled")
                                        .role(Role::Button)
                                        .aria_label(if enabled {
                                            locale.text("ui.disableProvider")
                                        } else {
                                            locale.text("ui.enableProvider")
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
                                            this.runtime.request(Method::Execute, action.clone());
                                            cx.notify();
                                        }))
                                        .child(
                                            div().size(px(18.)).rounded_full().bg(palette.paper),
                                        ),
                                )
                            }),
                    ),
            )
            .when(quota_available, |detail| {
                detail.child(
                    div()
                        .rounded(px(12.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .pt(px(17.))
                        .px(px(22.))
                        .pb(px(20.))
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
                                        .child(locale.format(
                                            "ui.weeklyAllowanceArg0Remaining",
                                            &[("arg0", format!("{:.0}", quota_remaining))],
                                        )),
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
                                        .w(relative((quota_remaining / 100.) as f32))
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
                                        .child(locale.text("ui.extraCredits")),
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
                            .h(px(74.))
                            .px(px(18.))
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
                                            .child(locale.text("ui.models3")),
                                    )
                                    .child(div().text_color(palette.faint).text_xs().child(
                                        locale.text(
                                            "ui.enabledModelsCanBeAssignedInRoutesAndTheComposer",
                                        ),
                                    )),
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
                                    .aria_label(locale.text("ui.fetchModels"))
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
                                    .active(|style| style.opacity(0.72))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.runtime
                                            .request(Method::Execute, refresh_action.clone());
                                        cx.notify();
                                    }))
                                    .child(icon("rotate-ccw", 13., palette.muted))
                                    .child(locale.text("ui.fetchModels")),
                            ),
                    )
                    .child(
                        div()
                            .h(px(44.))
                            .px(px(14.))
                            .border_t_1()
                            .border_b_1()
                            .border_color(palette.border)
                            .text_color(palette.faint)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(icon("search", 13., palette.faint))
                            .child(model_search),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_h_0()
                            .overflow_hidden()
                            .p(px(14.))
                            .child(model_list),
                    ),
            )
            .into_any_element()
    } else {
        settings_empty_card(locale.text("ui.noModelProvidersAreConfigured"), palette)
    };
    let provider_list = if provider_rows.is_empty() {
        div()
            .h(px(100.))
            .text_color(palette.faint)
            .text_sm()
            .flex()
            .items_center()
            .justify_center()
            .child(locale.text("ui.noProviders"))
            .into_any_element()
    } else {
        let provider_count = provider_rows.len();
        uniform_list(
            "provider-list-scroll",
            provider_count,
            cx.processor(move |_this, range: std::ops::Range<usize>, _window, cx| {
                range
                    .filter_map(|row_index| {
                        let (index, provider) = provider_rows.get(row_index)?;
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
                            .unwrap_or_default();
                        let enabled = provider
                            .get("enabled")
                            .and_then(serde_json::Value::as_bool)
                            .unwrap_or(false);
                        let subtitle =
                            provider_list_subtitle(provider, &id, backend, model_count, locale);
                        let quota = provider_quota_remaining(provider)
                            .map(format_percentage)
                            .unwrap_or_default();
                        let active = selected_provider_id == id;
                        let selected_id = id.clone();
                        Some(
                            div().h(px(66.)).child(
                                div()
                                    .id(("settings-provider", *index))
                                    .role(Role::Button)
                                    .aria_label(display_name.clone())
                                    .aria_selected(active)
                                    .tab_stop(true)
                                    .h(px(62.))
                                    .px_3()
                                    .rounded(px(9.))
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
                                        this.settings_model_search
                                            .update(cx, |search, cx| search.clear(cx));
                                        this.settings_model_scroll
                                            .scroll_to_item_strict(0, ScrollStrategy::Top);
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
                                                } else {
                                                    locale.text("ui.off2").to_string()
                                                }
                                            } else {
                                                quota
                                            }),
                                    ),
                            ),
                        )
                    })
                    .collect::<Vec<_>>()
            }),
        )
        .track_scroll(&provider_scroll)
        .h_full()
        .into_any_element()
    };
    let remaining_provider_count = if provider_query.is_empty() {
        state.catalogs.providers.len().saturating_sub(31)
    } else {
        0
    };
    div()
        .w_full()
        .min_h_0()
        .flex_1()
        .flex()
        .items_start()
        .gap(px(20.))
        .child(
            div()
                .id("provider-list")
                .h_full()
                .w(px(268.))
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
                        .h(px(42.))
                        .px_2()
                        .mb_1()
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper_muted)
                        .text_color(palette.faint)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 13., palette.faint))
                        .child(provider_search),
                )
                .child(
                    div()
                        .flex_1()
                        .min_h_0()
                        .overflow_hidden()
                        .child(provider_list),
                )
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
                            .child(locale.format(
                                "ui.scrollForRemainingProviderCountMoreProviders",
                                &[(
                                    "remaining_provider_count",
                                    (remaining_provider_count).to_string(),
                                )],
                            )),
                    )
                }),
        )
        .child(detail)
        .into_any_element()
}

fn model_matches_query(model: &serde_json::Value, query: &str) -> bool {
    query.is_empty()
        || ["id", "name"].into_iter().any(|key| {
            model
                .get(key)
                .and_then(serde_json::Value::as_str)
                .is_some_and(|value| value.to_ascii_lowercase().contains(query))
        })
        || model
            .get("aliases")
            .and_then(serde_json::Value::as_array)
            .is_some_and(|aliases| {
                aliases.iter().any(|alias| {
                    alias
                        .as_str()
                        .is_some_and(|value| value.to_ascii_lowercase().contains(query))
                })
            })
}

fn provider_matches_query(provider: &serde_json::Value, query: &str) -> bool {
    query.is_empty()
        || ["id", "displayName", "name", "backend"]
            .into_iter()
            .any(|key| {
                provider
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .is_some_and(|value| value.to_ascii_lowercase().contains(query))
            })
}

fn model_discovery_request(provider: &serde_json::Value, session_id: &str) -> serde_json::Value {
    if provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        json!({
            "kind": "discover_provider_models",
            "sessionId": session_id,
            "target": provider.get("id").and_then(serde_json::Value::as_str).unwrap_or_default(),
        })
    } else {
        json!({
            "kind": "discover_provider_models",
            "sessionId": session_id,
            "provider": provider,
        })
    }
}

fn model_provider_action(provider: &serde_json::Value, session_id: &str) -> serde_json::Value {
    if provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        let enabled = provider
            .get("enabled")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        return json!({
            "kind": if enabled { "logout" } else { "login" },
            "sessionId": session_id,
            "target": provider.get("id").and_then(serde_json::Value::as_str).unwrap_or_default(),
        });
    }
    let mut toggled = provider.clone();
    let enabled = provider
        .get("enabled")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    if let Some(fields) = toggled.as_object_mut() {
        fields.insert("enabled".to_string(), serde_json::Value::Bool(!enabled));
    }
    json!({"kind": "set_model_provider", "sessionId": session_id, "provider": toggled})
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
    locale: Locale,
) -> String {
    let subscription = provider
        .get("subscription")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    if !subscription {
        return format!("{} · {} {}", backend, model_count, locale.text("ui.models"));
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
        return format!("{plan} · {} {balance}", locale.text("ui.extra"));
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

fn provider_quota_remaining(provider: &serde_json::Value) -> Option<f64> {
    if !provider
        .get("quotaAvailable")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        return None;
    }
    let used = provider
        .get("quotaUsedPercent")
        .and_then(serde_json::Value::as_f64)
        .unwrap_or(0.);
    Some((100. - used).clamp(0., 100.))
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

pub(super) struct ModelCapabilityTooltip {
    pub(super) label: String,
    pub(super) palette: ThemePalette,
}

impl Render for ModelCapabilityTooltip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div().pl_2().pt_2().child(
            div()
                .px_2()
                .py_1()
                .rounded(px(7.))
                .border_1()
                .border_color(self.palette.border)
                .bg(self.palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.14))
                        .blur_radius(px(14.)),
                ])
                .text_color(self.palette.ink_soft)
                .text_xs()
                .child(self.label.clone()),
        )
    }
}

fn settings_model_capabilities(
    capabilities: &[String],
    model_index: usize,
    palette: ThemePalette,
    locale: Locale,
) -> impl IntoElement {
    div()
        .id(format!("model-capabilities-{model_index}"))
        .role(Role::List)
        .aria_label(locale.text("ui.modelCapabilities"))
        .flex()
        .items_center()
        .gap_1()
        .children(capabilities.iter().enumerate().map(|(index, capability)| {
            let icon_name = match capability.as_str() {
                "tools" | "tool_call" | "tool-call" => "wrench",
                "parallel-tools" => "layers",
                "reasoning" => "brain",
                "structured_output" | "structured-output" => "braces",
                "in:image" | "out:image" | "in:video" | "out:video" => "image",
                "in:audio" | "out:audio" => "audio-lines",
                "in:text" => "type",
                "out:text" => "message-square-text",
                _ => "wrench",
            };
            let label = model_capability_label(capability, locale);
            let tooltip_label = label.clone();
            div()
                .id(format!("model-capability-{model_index}-{index}"))
                .role(Role::ListItem)
                .aria_label(label)
                .size(px(28.))
                .rounded(px(8.))
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .justify_center()
                .tooltip(move |_, cx| {
                    cx.new(|_| ModelCapabilityTooltip {
                        label: tooltip_label.clone(),
                        palette,
                    })
                    .into()
                })
                .child(icon(icon_name, 13., palette.faint))
        }))
}

fn model_capability_label(capability: &str, locale: Locale) -> String {
    match capability {
        "tools" | "tool_call" | "tool-call" => locale.text("ui.tools").to_string(),
        "parallel-tools" => locale.text("ui.parallelTools").to_string(),
        "reasoning" => locale.text("ui.reasoning2").to_string(),
        "structured_output" | "structured-output" => locale.text("ui.structuredOutput").to_string(),
        value if value.starts_with("in:") => format!(
            "{}: {}",
            locale.text("ui.input"),
            value.trim_start_matches("in:")
        ),
        value if value.starts_with("out:") => format!(
            "{}: {}",
            locale.text("ui.output"),
            value.trim_start_matches("out:")
        ),
        value => value.to_string(),
    }
}

fn model_capability_keys(model: &serde_json::Value, subscription: bool) -> Vec<String> {
    let mut keys = Vec::new();
    if subscription {
        for key in [
            "tools",
            "reasoning",
            "structured-output",
            "in:text",
            "out:text",
        ] {
            push_unique(&mut keys, key);
        }
    }
    for key in ["capabilities", "inputModalities", "outputModalities"] {
        let prefix = match key {
            "inputModalities" => "in:",
            "outputModalities" => "out:",
            _ => "",
        };
        for value in model
            .get(key)
            .and_then(serde_json::Value::as_array)
            .into_iter()
            .flatten()
            .filter_map(serde_json::Value::as_str)
        {
            push_unique(&mut keys, &format!("{prefix}{value}"));
        }
    }
    keys
}

fn push_unique(values: &mut Vec<String>, value: &str) {
    if !values.iter().any(|existing| existing == value) {
        values.push(value.to_string());
    }
}

fn pick<T>(condition: bool, yes: T, no: T) -> T {
    if condition { yes } else { no }
}

fn settings_governance_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let approval_controls = [
        ("prompt", locale.text("ui.askEveryTime")),
        ("auto_review", locale.text("ui.autoReview")),
        ("yolo", locale.text("approval.yolo")),
    ]
    .into_iter()
    .enumerate()
    .map(|(id, (value, label))| {
        settings_action_button(
            id,
            label,
            state.connection.connected,
            state.settings.approval_mode.as_ref() == value,
            palette,
            cx,
            json!({"kind": "set_approval_mode", "target": value, "sessionId": session_id}),
        )
    })
    .collect::<Vec<_>>();
    let delivery_controls = [
        ("queue", locale.text("ui.queue")),
        ("guide", locale.text("ui.guideLive")),
    ]
    .into_iter()
    .enumerate()
    .map(|(index, (value, label))| {
        settings_action_button(
            index + 3,
            label,
            state.connection.connected,
            state.settings.queue_mode.as_ref() == value,
            palette,
            cx,
            json!({"kind": "set_queue_mode", "target": value, "sessionId": session_id}),
        )
    })
    .collect::<Vec<_>>();
    div()
        .w_full()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_control_row(
            locale.text("ui.defaultApprovalMode"),
            locale.text("ui.controlsToolExecutionBoundaries"),
            300.,
            approval_controls,
            palette,
        ))
        .child(settings_control_row(
            locale.text("ui.newMessagesWhileRunning"),
            locale.text("ui.whenFollowUpInputArrives"),
            200.,
            delivery_controls,
            palette,
        ))
        .child(
            div()
                .min_h(px(64.))
                .px_4()
                .border_t_1()
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
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("ui.approvalValidation")),
                        )
                        .child(div().text_xs().text_color(palette.muted).child(
                            locale.text("ui.onlyCompleteJsonIsAcceptedInvalidContentNeverExecutes"),
                        )),
                )
                .child(
                    div()
                        .text_sm()
                        .text_color(palette.positive)
                        .child(locale.text("ui.failClosed")),
                ),
        )
        .into_any_element()
}

fn settings_control_row(
    title: &'static str,
    description: &'static str,
    control_width: f32,
    controls: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(68.))
        .px_4()
        .py(px(11.))
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_5()
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(div().text_sm().text_color(palette.muted).child(description)),
        )
        .child(
            div()
                .w(px(control_width))
                .p(px(2.))
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .children(controls),
        )
}

fn settings_appearance_body(
    state: &AppState,
    native: &NativeSettings,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let preferences = AppearancePreferences::current(cx);
    let reduced_motion = preferences.reduced_motion;
    let theme_names = [
        locale.text("ui.warm"),
        locale.text("ui.dark"),
        locale.text("ui.system"),
    ];
    let themes = div()
        .flex()
        .items_start()
        .gap(px(14.))
        .children(theme_names.into_iter().enumerate().map(|(index, name)| {
            settings_theme_preview(index, name, &preferences.theme, palette, cx)
        }))
        .into_any_element();
    let primary = div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .child(settings_children_row(
            locale.text("ui.interfaceLanguage"),
            locale.text("ui.menusButtonsAndSystemMessages"),
            settings_language_control(state, native, locale, palette, cx),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.theme"),
            locale.text("ui.systemModeFollowsLightAndDarkAppearance"),
            themes,
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.interfaceFont"),
            locale.text("ui.chooseFromInstalledFontsCodeRemainsMonospaced"),
            settings_font_family_control(native, &preferences, locale, palette, cx),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.interfaceFontSize"),
            locale.text("ui.adjustSidebarSettingsAndOtherUiText"),
            settings_font_size_control(
                "uiFontSize",
                preferences.ui_font_size,
                11.,
                20.,
                locale,
                palette,
                cx,
            ),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.reduceMotion"),
            locale.text("ui.makeTransitionsAndStreamingUpdatesImmediate"),
            div()
                .id("appearance-reduced-motion")
                .role(Role::Switch)
                .aria_label(locale.text("ui.reduceMotion"))
                .aria_toggled(reduced_motion.into())
                .tab_stop(true)
                .cursor_pointer()
                .on_click(cx.listener(move |this, _, window, cx| {
                    this.set_appearance("reducedMotion", json!(!reduced_motion), window, cx)
                }))
                .child(settings_switch(reduced_motion, palette))
                .into_any_element(),
            palette,
        ));
    let chat = settings_detail_card(
        locale.text("ui.chatText"),
        locale.text("ui.onlyAffectsTranscriptAndCodeBlockSizes"),
        vec![
            settings_children_row(
                locale.text("ui.uiText"),
                locale.text("ui.messageBubblesAssistantTextLabelsAndComposer"),
                settings_font_size_control(
                    "chatFontSize",
                    preferences.chat_font_size,
                    12.,
                    20.,
                    locale,
                    palette,
                    cx,
                ),
                palette,
            )
            .into_any_element(),
            settings_children_row(
                locale.text("ui.codeFontSize"),
                locale.text("ui.monospacedSizeForFencedCodeBlocks"),
                settings_font_size_control(
                    "codeFontSize",
                    preferences.code_font_size,
                    11.,
                    18.,
                    locale,
                    palette,
                    cx,
                ),
                palette,
            )
            .into_any_element(),
        ],
        palette,
    );
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(primary)
        .child(chat)
        .into_any_element()
}

fn settings_theme_preview(
    index: usize,
    name: &'static str,
    theme: &str,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let value = ["light", "dark", "system"][index];
    let selected = theme == value;
    let (preview_color, border_color, sidebar_color, paper_color, line_color) = match index {
        1 => (
            rgb(0x151613),
            rgb(0x343530),
            rgb(0x2d2e28),
            rgb(0x20211d),
            rgb(0x44463f),
        ),
        _ => (
            rgb(0xf5f4ef),
            pick(selected, palette.accent, palette.border),
            rgb(0xe3e2dc),
            rgb(0xffffff),
            rgb(0xd4d3cd),
        ),
    };
    let preview = div()
        .w(px(54.))
        .h(px(42.))
        .relative()
        .overflow_hidden()
        .rounded(px(8.))
        .border_1()
        .border_color(border_color)
        .bg(preview_color)
        .when(index == 2, |preview| {
            preview.child(
                div()
                    .absolute()
                    .top_0()
                    .right_0()
                    .bottom_0()
                    .w(px(27.))
                    .bg(rgb(0x151613)),
            )
        })
        .child(
            div()
                .absolute()
                .top(px(7.))
                .bottom(px(7.))
                .left(px(8.))
                .w(px(14.))
                .rounded(px(3.))
                .bg(sidebar_color),
        )
        .child(
            div()
                .absolute()
                .top(px(7.))
                .right(px(8.))
                .bottom(px(7.))
                .left(px(22.))
                .rounded(px(3.))
                .bg(paper_color),
        )
        .child(
            div()
                .absolute()
                .top(px(13.))
                .right(px(10.))
                .left(px(28.))
                .h(px(4.))
                .rounded_full()
                .bg(line_color),
        )
        .child(
            div()
                .absolute()
                .top(px(22.))
                .right(px(10.))
                .left(px(28.))
                .h(px(4.))
                .rounded_full()
                .bg(line_color),
        );
    div()
        .id(("theme-preview", index))
        .w(px(64.))
        .p(px(3.))
        .rounded(px(9.))
        .border_1()
        .border_color(pick(selected, palette.accent, palette.paper))
        .bg(palette.paper)
        .role(Role::RadioButton)
        .aria_label(name)
        .aria_selected(selected)
        .tab_stop(true)
        .cursor_pointer()
        .on_click(cx.listener(move |this, _, window, cx| {
            this.set_appearance("theme", json!(value), window, cx)
        }))
        .flex()
        .flex_col()
        .items_center()
        .gap_1()
        .child(preview)
        .child(
            div()
                .text_size(px(10.))
                .text_color(palette.muted)
                .child(name),
        )
        .into_any_element()
}

fn settings_language_control(
    state: &AppState,
    native: &NativeSettings,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let languages = crate::localization::available();
    let selected = languages
        .iter()
        .position(|pack| pack.id == locale.id())
        .unwrap_or(0);
    let enabled = state.connection.connected && !native.language_saving;
    div()
        .id("language-control")
        .relative()
        .w(px(200.))
        .child(
            div()
                .id("language-picker")
                .role(Role::ComboBox)
                .aria_label(locale.text("ui.interfaceLanguage"))
                .aria_value(languages[selected].name.clone())
                .aria_expanded(native.language_menu_open)
                .tab_stop(enabled)
                .track_focus(&native.language_focus)
                .h(px(40.))
                .px_3()
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .items_center()
                .gap_2()
                .when(enabled, |button| {
                    button
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                })
                .on_click(cx.listener(move |this, _, window, cx| {
                    if enabled {
                        this.native_settings.language_focus.focus(window, cx);
                        this.native_settings.language_menu_open =
                            !this.native_settings.language_menu_open;
                        this.native_settings.language_index = selected;
                        this.native_settings
                            .language_scroll
                            .scroll_to_item(selected);
                        this.native_settings.font_menu_open = false;
                        cx.notify();
                    }
                }))
                .capture_key_down(cx.listener(
                    move |this, event: &gpui::KeyDownEvent, window, cx| {
                        if !enabled {
                            return;
                        }
                        match event.keystroke.key.as_str() {
                            "up" | "down" => {
                                let count = languages.len();
                                if !this.native_settings.language_menu_open {
                                    this.native_settings.language_menu_open = true;
                                    this.native_settings.language_index = selected;
                                } else {
                                    let step = if event.keystroke.key == "down" {
                                        1
                                    } else {
                                        count - 1
                                    };
                                    this.native_settings.language_index =
                                        (this.native_settings.language_index + step) % count;
                                }
                            }
                            "enter" | "space" => {
                                if this.native_settings.language_menu_open {
                                    let index = this
                                        .native_settings
                                        .language_index
                                        .min(languages.len() - 1);
                                    this.change_language(&languages[index].id, cx);
                                } else {
                                    this.native_settings.language_menu_open = true;
                                    this.native_settings.language_index = selected;
                                }
                            }
                            "escape" => this.native_settings.language_menu_open = false,
                            "tab" => {
                                this.native_settings.language_menu_open = false;
                                cx.notify();
                                return;
                            }
                            _ => return,
                        }
                        this.native_settings
                            .language_scroll
                            .scroll_to_item(this.native_settings.language_index);
                        this.native_settings.language_focus.focus(window, cx);
                        window.prevent_default();
                        cx.stop_propagation();
                        cx.notify();
                    },
                ))
                .child(
                    div()
                        .flex_1()
                        .truncate()
                        .child(languages[selected].name.clone()),
                )
                .child(icon(
                    if native.language_saving {
                        "loader"
                    } else {
                        "chevron-down"
                    },
                    14.,
                    palette.faint,
                )),
        )
        .when(native.language_menu_open, |control| {
            control.child(
                deferred(
                    div()
                        .id("language-options")
                        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                        .role(Role::ListBox)
                        .aria_label(locale.text("ui.interfaceLanguage"))
                        .absolute()
                        .top(px(44.))
                        .right_0()
                        .w(px(200.))
                        .max_h(px(260.))
                        .overflow_y_scroll()
                        .track_scroll(&native.language_scroll)
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .bg(palette.paper)
                        .occlude()
                        .p_1()
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(8.), hsla(0., 0., 0., 0.15))
                                .blur_radius(px(20.)),
                        ])
                        .children(languages.iter().enumerate().map(|(index, pack)| {
                            div()
                                .id(("language-option", index))
                                .role(Role::ListBoxOption)
                                .aria_label(pack.name.clone())
                                .aria_selected(index == selected)
                                .h(px(36.))
                                .px_2()
                                .rounded(px(6.))
                                .flex()
                                .items_center()
                                .gap_2()
                                .bg(if index == native.language_index {
                                    palette.hover
                                } else {
                                    palette.paper
                                })
                                .cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, window, cx| {
                                    this.change_language(&pack.id, cx);
                                    this.native_settings.language_focus.focus(window, cx);
                                }))
                                .child(div().flex_1().truncate().child(pack.name.clone()))
                                .when(index == selected, |row| {
                                    row.child(icon("check", 14., palette.accent))
                                })
                        })),
                )
                .with_priority(30),
            )
        })
        .into_any_element()
}

fn settings_font_family_control(
    native: &NativeSettings,
    preferences: &AppearancePreferences,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let family = preferences.font.clone();
    let selected_label = native
        .fonts
        .iter()
        .find(|font| font["family"].as_str() == Some(&family))
        .and_then(|font| font["label"].as_str())
        .unwrap_or(&family)
        .to_string();
    let query = native.font_search.read(cx).text().trim().to_lowercase();
    let mut fonts = vec![json!({"family":"system", "label":locale.text("ui.systemDefault")})];
    fonts.extend(
        native
            .fonts
            .iter()
            .filter(|font| {
                format!(
                    "{} {}",
                    font["family"].as_str().unwrap_or_default(),
                    font["label"].as_str().unwrap_or_default()
                )
                .to_lowercase()
                .contains(&query)
            })
            .cloned(),
    );
    div()
        .id("appearance-font-control")
        .relative()
        .w(px(200.))
        .child(
            div()
                .id("appearance-font-picker")
                .role(Role::Button)
                .aria_label(locale.text("ui.interfaceFont"))
                .aria_expanded(native.font_menu_open)
                .tab_stop(true)
                .min_h(px(42.))
                .px_3()
                .py_1()
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .on_click(cx.listener(|this, _, window, cx| {
                    this.native_settings.font_menu_open = !this.native_settings.font_menu_open;
                    this.native_settings.language_menu_open = false;
                    if this.native_settings.font_menu_open {
                        this.native_settings
                            .font_search
                            .focus_handle(cx)
                            .focus(window, cx);
                        if this.native_settings.fonts.is_empty()
                            && !this.native_settings.fonts_loading
                        {
                            this.request_system_fonts();
                        }
                    }
                    cx.notify();
                }))
                .child(div().text_color(palette.muted).child("Aa"))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .child(if family == "system" {
                            locale.text("ui.systemDefault").to_string()
                        } else {
                            selected_label
                        }),
                )
                .child(icon("chevron-down", 12., palette.faint)),
        )
        .when(native.font_menu_open, |control| {
            control.child(
                deferred(
                    div()
                        .id("appearance-font-menu")
                        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                        .role(Role::RadioGroup)
                        .absolute()
                        .top(px(46.))
                        .right_0()
                        .w(px(200.))
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .bg(palette.paper)
                        .occlude()
                        .p_2()
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(8.), hsla(0., 0., 0., 0.15))
                                .blur_radius(px(20.)),
                        ])
                        .child(
                            div()
                                .h(px(34.))
                                .border_b_1()
                                .border_color(palette.border)
                                .child(native.font_search.clone()),
                        )
                        .child(
                            div()
                                .id("appearance-font-list")
                                .h(px(210.))
                                .overflow_y_scroll()
                                .when(native.fonts_loading, |list| {
                                    list.child(locale.text("ui.loadingFonts"))
                                })
                                .children(fonts.into_iter().enumerate().map(|(index, font)| {
                                    let value =
                                        font["family"].as_str().unwrap_or_default().to_string();
                                    let label =
                                        font["label"].as_str().unwrap_or(&value).to_string();
                                    let selected = family == value;
                                    div()
                                        .id(("appearance-font-option", index))
                                        .role(Role::RadioButton)
                                        .aria_label(label.clone())
                                        .aria_selected(selected)
                                        .tab_stop(true)
                                        .h(px(32.))
                                        .px_2()
                                        .rounded(px(5.))
                                        .flex()
                                        .items_center()
                                        .bg(if selected {
                                            palette.accent_soft
                                        } else {
                                            palette.paper
                                        })
                                        .hover(move |s| s.bg(palette.hover))
                                        .cursor_pointer()
                                        .on_click(cx.listener(move |this, _, window, cx| {
                                            this.set_appearance("font", json!(value), window, cx)
                                        }))
                                        .child(div().flex_1().truncate().child(label))
                                        .when(selected, |row| {
                                            row.child(icon("check", 12., palette.accent))
                                        })
                                })),
                        ),
                )
                .with_priority(30),
            )
        })
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn settings_font_size_control(
    key: &'static str,
    value: f32,
    min: f32,
    max: f32,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let label = match key {
        "uiFontSize" => locale.text("ui.interfaceFontSize"),
        "chatFontSize" => locale.text("ui.chatFontSize"),
        _ => locale.text("ui.codeFontSize2"),
    };
    let button = |increase: bool, cx: &mut Context<AzemWindow>| {
        let enabled = if increase { value < max } else { value > min };
        div()
            .id(format!(
                "{key}-{}",
                if increase { "increase" } else { "decrease" }
            ))
            .role(Role::Button)
            .aria_label(format!("{} {label}", if increase { "+" } else { "−" }))
            .tab_stop(enabled)
            .w(px(44.))
            .h_full()
            .flex()
            .items_center()
            .justify_center()
            .text_color(if enabled {
                palette.ink_soft
            } else {
                palette.faint
            })
            .when(enabled, |button| {
                button.cursor_pointer().hover(move |s| s.bg(palette.hover))
            })
            .on_click(cx.listener(move |this, _, window, cx| {
                if enabled {
                    this.set_appearance(
                        key,
                        json!(value + if increase { 1. } else { -1. }),
                        window,
                        cx,
                    );
                }
            }))
            .child(if increase { "A+" } else { "A−" })
    };
    div()
        .w(px(160.))
        .h(px(34.))
        .rounded(px(8.))
        .border_1()
        .border_color(palette.border_strong)
        .bg(palette.paper)
        .overflow_hidden()
        .flex()
        .child(button(false, cx))
        .child(
            div()
                .flex_1()
                .h_full()
                .border_l_1()
                .border_r_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .justify_center()
                .text_sm()
                .child(format!("{value} px")),
        )
        .child(button(true, cx))
        .into_any_element()
}

fn settings_children_row(
    title: &'static str,
    description: &'static str,
    control: gpui::AnyElement,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(76.))
        .px_4()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_4()
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(div().text_sm().text_color(palette.muted).child(description)),
        )
        .child(control)
}

fn settings_extensions_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    selected_tab: &str,
    controls: &ExtensionSettings,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mcp = state
        .catalogs
        .mcp
        .as_array()
        .map(Vec::as_slice)
        .unwrap_or_default();
    let marketplaces = extension_items(&state.catalogs.marketplace, "marketplaces");
    let hooks = extension_items(&state.catalogs.hooks, "commands");
    let enabled = state.connection.connected && !controls.busy;
    let query = controls.search.read(cx).text().trim().to_lowercase();
    let refresh = match selected_tab {
        "skills" => "reload_skills",
        "plugins" => "list_plugins",
        "marketplace" => "marketplace_list",
        "hooks" => "list_hooks",
        _ => "refresh_mcp",
    };
    let tabs = [
        ("mcp", "server", locale.text("ui.mcpServers"), mcp.len()),
        (
            "skills",
            "sparkles",
            locale.text("extension.skills"),
            state.catalogs.skills.len(),
        ),
        (
            "plugins",
            "puzzle",
            locale.text("ui.plugins"),
            state.catalogs.plugins.len(),
        ),
        (
            "marketplace",
            "blocks",
            locale.text("ui.marketplace"),
            marketplaces.len(),
        ),
        (
            "hooks",
            "wrench",
            locale.text("extension.hooks"),
            hooks.len(),
        ),
    ];
    let values = match selected_tab {
        "skills" => state.catalogs.skills.as_slice(),
        "plugins" => state.catalogs.plugins.as_slice(),
        "hooks" => hooks,
        "marketplace" => &[],
        _ => mcp,
    };
    let mut rows = Vec::new();
    for value in values
        .iter()
        .filter(|value| extension_matches(value, &query))
    {
        let name = extension_text(value, "name");
        let detail;
        let mut actions = div().flex().items_center().gap_2().flex_shrink_0();
        let toggle = |kind: &str, target: &str, on: bool| json!({"kind":kind,"target":target,"decision":(!on).to_string()});
        match selected_tab {
            "skills" => {
                let on = !extension_flag(value, "disabled");
                detail = format!(
                    "{} · {}\n{}",
                    if on {
                        locale.text("ui.enabled")
                    } else {
                        locale.text("ui.disabled")
                    },
                    extension_text(value, "sourcePath"),
                    extension_text(value, "description")
                );
                actions = actions.child(extension_button(
                    locale.text("ui.enableSkill"),
                    toggle("set_skill_enabled", name, on),
                    enabled && !name.is_empty(),
                    palette,
                    cx,
                ));
            }
            "plugins" => {
                detail = [
                    "description",
                    "origin",
                    "scope",
                    "version",
                    "status",
                    "warning",
                ]
                .into_iter()
                .map(|key| {
                    let text = extension_text(value, key);
                    match key {
                        "origin" | "scope" => locale.value("extension", text),
                        "status" => locale.value("status", text),
                        _ => text,
                    }
                })
                .filter(|s| !s.is_empty())
                .collect::<Vec<_>>()
                .join(" · ");
                if let Some(payload) = plugin_import_action(value) {
                    let importing = payload["decision"] == "true";
                    actions = actions.child(extension_button(
                        if importing {
                            locale.text("ui.import")
                        } else {
                            locale.text("ui.remove")
                        },
                        payload,
                        enabled,
                        palette,
                        cx,
                    ));
                }
            }
            "hooks" => {
                let on = extension_flag(value, "enabled");
                let trusted = extension_text(value, "origin") != "plugin"
                    || extension_flag(&state.catalogs.hooks, "trustHooks");
                let status = if !on {
                    locale.text("ui.disabled")
                } else if !trusted {
                    locale.text("ui.awaitingTrust")
                } else {
                    locale.text("ui.enabled")
                };
                detail = format!(
                    "{} · {} · {}\n{}\n{}",
                    extension_text(value, "event"),
                    extension_text(value, "matcher"),
                    status,
                    extension_text(value, "command"),
                    extension_text(value, "source")
                );
                let id = extension_text(value, "id");
                actions = actions.child(extension_button(
                    locale.text("ui.enableHook"),
                    toggle("set_hook_enabled", id, on),
                    enabled && !id.is_empty(),
                    palette,
                    cx,
                ));
            }
            _ => {
                detail = format!(
                    "{} · {} · {} {}\n{}\n{}",
                    locale.value("status", extension_text(value, "state")),
                    extension_text(value, "transport"),
                    value["toolCount"].as_u64().unwrap_or(0),
                    locale.text("ui.tools2"),
                    extension_safe_target(value),
                    extension_text(value, "error")
                );
                actions = actions
                    .child(extension_button(
                        locale.text("ui.enableMcp"),
                        toggle("set_mcp_enabled", name, extension_flag(value, "enabled")),
                        enabled,
                        palette,
                        cx,
                    ))
                    .child(extension_button(
                        locale.text("ui.reconnect"),
                        json!({"kind":"reconnect_mcp","target":name}),
                        enabled && extension_flag(value, "enabled"),
                        palette,
                        cx,
                    ));
                if extension_flag(value, "removable") {
                    actions = actions.child(extension_button(
                        locale.text("ui.remove"),
                        json!({"kind":"delete_mcp_server","target":name}),
                        enabled,
                        palette,
                        cx,
                    ));
                }
            }
        }
        rows.push(
            extension_row(
                name,
                &detail,
                (selected_tab == "plugins").then(|| plugin_mark(value, palette)),
                palette,
            )
            .child(actions)
            .into_any_element(),
        );
    }
    let mut body = div().flex().flex_col().gap_4();
    if selected_tab == "marketplace" {
        body = body.child(settings_marketplace_body(
            state, controls, &query, enabled, palette, locale, cx,
        ));
    } else {
        if selected_tab == "hooks" {
            body = body.child(extension_row(locale.text("ui.trustPluginHooks"),
                locale.text("ui.pluginHooksRunLocalCommandsOffByDefaultProjectAnd"), None, palette)
                .child(extension_button(locale.text("ui.trustPluginHooks"),
                    json!({"kind":"set_plugin_hooks_trusted","decision":(!extension_flag(&state.catalogs.hooks,"trustHooks")).to_string()}), enabled, palette, cx)));
            let sources = extension_items(&state.catalogs.hooks, "sources")
                .iter()
                .map(|source| {
                    let detail = format!(
                        "{} · {} {} · {}\n{}\n{}",
                        locale.value("extension", extension_text(source, "origin")),
                        source["hookCount"].as_u64().unwrap_or(0),
                        locale.text("extension.hooks"),
                        if extension_flag(source, "trusted") {
                            locale.text("ui.trusted")
                        } else {
                            locale.text("ui.untrusted")
                        },
                        extension_text(source, "source"),
                        extension_text(source, "warning")
                    );
                    extension_row(
                        extension_text(source, "name"),
                        &detail,
                        Some(plugin_mark(source, palette)),
                        palette,
                    )
                    .into_any_element()
                })
                .collect();
            body = body.child(extension_list(
                locale.text("ui.hookSources"),
                sources,
                palette,
                locale,
            ));
        }
        let title = match selected_tab {
            "skills" => locale.text("extension.skills"),
            "plugins" => locale.text("ui.plugins"),
            "hooks" => locale.text("ui.hookCommands"),
            _ => locale.text("ui.mcpServers"),
        };
        body = body.child(extension_list(title, rows, palette, locale));
    }
    let diagnostics = match selected_tab {
        "skills" => state.catalogs.skill_diagnostics.as_slice(),
        "plugins" => state.catalogs.plugin_diagnostics.as_slice(),
        "hooks" => extension_items(&state.catalogs.hooks, "diagnostics"),
        _ => &[],
    };
    if !diagnostics.is_empty() {
        body = body.child(extension_list(
            locale.text("ui.diagnostics"),
            diagnostics
                .iter()
                .enumerate()
                .map(|(index, value)| {
                    extension_row(
                        &inventory_label(value, index, locale),
                        extension_text(value, "message"),
                        None,
                        palette,
                    )
                    .into_any_element()
                })
                .collect(),
            palette,
            locale,
        ));
    }
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .id("extensions-tab-list")
                .role(Role::TabList)
                .h(px(42.))
                .p(px(3.))
                .rounded(px(10.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .gap_1()
                .children(tabs.into_iter().map(|(tab, icon_name, label, count)| {
                    let selected = selected_tab == tab;
                    div()
                        .id(format!("extensions-tab-{tab}"))
                        .role(Role::Tab)
                        .aria_label(label)
                        .aria_selected(selected)
                        .tab_stop(true)
                        .h_full()
                        .px_3()
                        .rounded(px(8.))
                        .bg(if selected {
                            palette.paper
                        } else {
                            palette.paper_muted
                        })
                        .text_color(if selected { palette.ink } else { palette.muted })
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.settings_section = format!("extensions:{tab}");
                            this.extension_settings
                                .search
                                .update(cx, |input, cx| input.clear(cx));
                            cx.notify();
                        }))
                        .child(icon(icon_name, 14., palette.muted))
                        .child(label)
                        .child(
                            div()
                                .px_1()
                                .rounded_full()
                                .bg(palette.hover)
                                .text_size(px(9.))
                                .child(count.to_string()),
                        )
                })),
        )
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h(px(36.))
                        .relative()
                        .px_3()
                        .pr(px(36.))
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 14., palette.faint))
                        .child(controls.search.clone())
                        .when(controls.busy, |view| {
                            view.child(
                                div()
                                    .id("extension-busy")
                                    .absolute()
                                    .top_0()
                                    .right_2()
                                    .w(px(20.))
                                    .h_full()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .role(Role::Status)
                                    .aria_label(locale.text("ui.workingPleaseWait"))
                                    .child(icon("loader", 14., palette.muted)),
                            )
                        }),
                )
                .child(extension_button(
                    locale.text("ui.refresh"),
                    json!({"kind":refresh}),
                    enabled,
                    palette,
                    cx,
                )),
        )
        .child(body)
        .into_any_element()
}

fn extension_items<'a>(catalog: &'a serde_json::Value, key: &str) -> &'a [serde_json::Value] {
    catalog
        .get(key)
        .and_then(serde_json::Value::as_array)
        .map(Vec::as_slice)
        .unwrap_or_default()
}

fn extension_text<'a>(value: &'a serde_json::Value, key: &str) -> &'a str {
    value
        .get(key)
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
}

fn extension_flag(value: &serde_json::Value, key: &str) -> bool {
    value
        .get(key)
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
}

fn extension_safe_target(value: &serde_json::Value) -> String {
    let target = extension_text(value, "target");
    let Some((scheme, address)) = target.split_once("://") else {
        // Arguments may contain credentials; show only the executable.
        return extension_text(value, "command").to_owned();
    };
    let address = address.split(['?', '#']).next().unwrap_or_default();
    let (authority, path) = address.split_once('/').unwrap_or((address, ""));
    let host = authority.rsplit('@').next().unwrap_or_default();
    format!("{scheme}://{host}/{path}")
}

fn extension_matches(value: &serde_json::Value, query: &str) -> bool {
    query.is_empty()
        || [
            "name",
            "displayName",
            "id",
            "description",
            "sourcePath",
            "origin",
            "source",
            "command",
            "event",
            "marketplace",
            "target",
        ]
        .iter()
        .any(|key| extension_text(value, key).to_lowercase().contains(query))
}

fn plugin_import_action(value: &serde_json::Value) -> Option<serde_json::Value> {
    let imported = match extension_text(value, "origin") {
        "codex_available" => false,
        "codex" => true,
        _ => return None,
    };
    let mut id = extension_text(value, "id").trim().to_owned();
    if id.is_empty() {
        id = extension_text(value, "name").trim().to_owned();
        let market = extension_text(value, "marketplace").trim();
        if !id.is_empty() && !market.is_empty() {
            id = format!("{id}@{market}");
        }
    }
    (!id.is_empty()).then(
        || json!({"kind":"set_plugin_imported","target":id,"decision":(!imported).to_string()}),
    )
}

pub(super) fn extension_confirmation(
    payload: &serde_json::Value,
    locale: Locale,
) -> Option<String> {
    let detail = match extension_text(payload, "kind") {
        "set_plugin_hooks_trusted" if payload["decision"] == "true" => {
            locale.text("ui.trustedPluginHooksCanAutomaticallyRunLocalCommandsWithYour")
        }
        "set_plugin_imported" if payload["decision"] == "false" => {
            locale.text("ui.removeTheAzemCopyAndIntegrationsTheOriginalCodexPlugin")
        }
        "delete_mcp_server" => {
            locale.text("ui.deleteThisMcpConfigurationAndDisconnectItThisCannotBe")
        }
        "marketplace_remove" => {
            locale.text("ui.removeThisMarketplaceSourceAndCacheInstalledPluginsRemain")
        }
        "marketplace_uninstall" => locale.text("ui.uninstallThePluginAndItsLocalCopyInTheSelected"),
        _ => return None,
    };
    Some(format!("{}\n{}", extension_text(payload, "target"), detail))
}

fn extension_button(
    label: &'static str,
    payload: serde_json::Value,
    enabled: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let kind = extension_text(&payload, "kind");
    let target = extension_text(&payload, "target");
    let switch = matches!(
        kind,
        "set_skill_enabled" | "set_hook_enabled" | "set_mcp_enabled" | "set_plugin_hooks_trusted"
    );
    let checked = payload["decision"] == "false";
    div()
        .id(format!("extension-{kind}-{target}"))
        .role(if switch { Role::Switch } else { Role::Button })
        .aria_label(
            format!("{label} {}", target.replace('\u{1f}', " · "))
                .trim()
                .to_owned(),
        )
        .tab_stop(enabled)
        .when(switch, |button| {
            button.aria_toggled(if checked {
                gpui::Toggled::True
            } else {
                gpui::Toggled::False
            })
        })
        .h(px(32.))
        .px_2()
        .rounded(px(7.))
        .flex()
        .items_center()
        .justify_center()
        .flex_shrink_0()
        .when(!switch, |button| {
            button
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
        })
        .text_xs()
        .text_color(palette.ink)
        .when(enabled, |button| {
            button
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
        })
        .on_click(cx.listener(move |this, _, window, cx| {
            if enabled {
                this.confirm_extension_action(payload.clone(), window, cx);
            }
        }))
        .child(if switch {
            settings_switch(checked, palette).into_any_element()
        } else {
            div().child(label).into_any_element()
        })
        .into_any_element()
}

fn plugin_logo(source: &str) -> Option<Arc<gpui::Image>> {
    let (mime, encoded) = source.strip_prefix("data:")?.split_once(";base64,")?;
    let format = gpui::ImageFormat::from_mime_type(mime)?;
    if !matches!(
        format,
        gpui::ImageFormat::Png
            | gpui::ImageFormat::Jpeg
            | gpui::ImageFormat::Gif
            | gpui::ImageFormat::Webp
            | gpui::ImageFormat::Svg
    ) || encoded.len() > (1_usize << 20).div_ceil(3) * 4
    {
        return None;
    }
    let bytes = STANDARD.decode(encoded).ok()?;
    if bytes.is_empty() || bytes.len() > 1 << 20 {
        return None;
    }
    if format == gpui::ImageFormat::Svg {
        let document = roxmltree::Document::parse(std::str::from_utf8(&bytes).ok()?).ok()?;
        // GPUI's SVG loader can read local hrefs. Plugin images must be self-contained.
        if document.root_element().tag_name().name() != "svg"
            || document
                .descendants()
                .flat_map(|node| node.attributes())
                .any(|attribute| attribute.name() == "href" && !attribute.value().starts_with('#'))
        {
            return None;
        }
    }
    Some(Arc::new(gpui::Image::from_bytes(format, bytes)))
}

fn plugin_mark(value: &serde_json::Value, palette: ThemePalette) -> gpui::AnyElement {
    // Lucide Plug, under the ISC license in gpui/THIRD_PARTY_NOTICES.
    let fallback = move || {
        svg()
        .data(br#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22v-5M9 8V2M15 8V2M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/></svg>"#)
        .size(px(20.))
        .text_color(palette.muted)
        .into_any_element()
    };
    let mark = match plugin_logo(extension_text(value, "logoPath")) {
        Some(logo) => img(logo)
            .size(px(24.))
            .with_fallback(fallback)
            .into_any_element(),
        None => fallback(),
    };
    div()
        .size(px(36.))
        .flex_shrink_0()
        .rounded(px(8.))
        .bg(palette.paper_muted)
        .flex()
        .items_center()
        .justify_center()
        .child(mark)
        .into_any_element()
}

fn extension_row(
    title: &str,
    detail: &str,
    mark: Option<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .w_full()
        .px_4()
        .py_3()
        .border_b_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_4()
        .children(mark)
        .child(
            div()
                .flex_1()
                .min_w_0()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .text_color(palette.ink)
                        .child(title.to_owned()),
                )
                .child(
                    div()
                        .text_xs()
                        .line_height(px(18.))
                        .text_color(palette.muted)
                        .whitespace_normal()
                        .child(detail.trim().to_owned()),
                ),
        )
}

fn extension_list(
    title: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
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
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .text_color(palette.ink)
                .child(title),
        )
        .when(rows.is_empty(), |view| {
            view.child(
                div()
                    .px_4()
                    .py_5()
                    .text_sm()
                    .text_color(palette.faint)
                    .child(locale.text("ui.noItemsOrMatchingResults")),
            )
        })
        .children(rows)
        .into_any_element()
}

fn marketplace_action(kind: &str, id: &str, scope: &str) -> serde_json::Value {
    json!({"kind":kind,"target":id,"decision":scope,"payload":{"scope":scope}})
}

fn marketplace_entries(catalog: &serde_json::Value, scope: &str) -> Vec<serde_json::Value> {
    let mut entries = extension_items(catalog, "available").to_vec();
    for installed in extension_items(catalog, "installed")
        .iter()
        .filter(|v| extension_text(v, "scope") == scope)
    {
        if !entries.iter().any(|v| v["id"] == installed["id"]) {
            entries.push(installed.clone());
        }
    }
    entries
}

fn settings_marketplace_body(
    state: &AppState,
    controls: &ExtensionSettings,
    query: &str,
    enabled: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let catalog = &state.catalogs.marketplace;
    let source = controls.source.read(cx).text().trim().to_owned();
    let mut rows = Vec::new();
    for market in extension_items(catalog, "marketplaces") {
        let name = extension_text(market, "name");
        rows.push(
            extension_row(name, extension_text(market, "source"), None, palette)
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(extension_button(
                            locale.text("ui.update"),
                            json!({"kind":"marketplace_update","target":name}),
                            enabled,
                            palette,
                            cx,
                        ))
                        .child(extension_button(
                            locale.text("ui.remove2"),
                            json!({"kind":"marketplace_remove","target":name}),
                            enabled,
                            palette,
                            cx,
                        )),
                )
                .into_any_element(),
        );
    }
    let mut plugins = Vec::new();
    for plugin in marketplace_entries(catalog, controls.scope)
        .iter()
        .filter(|v| extension_matches(v, query))
    {
        let id = extension_text(plugin, "id");
        let installed = extension_items(catalog, "installed")
            .iter()
            .find(|v| v["id"] == plugin["id"] && extension_text(v, "scope") == controls.scope);
        let mut actions = div().flex().items_center().gap_2().flex_shrink_0();
        if let Some(installed) = installed {
            let on = extension_flag(installed, "enabled");
            actions = actions
                .child(extension_button(
                    if on {
                        locale.text("ui.disable")
                    } else {
                        locale.text("ui.enable")
                    },
                    marketplace_action(
                        if on {
                            "marketplace_disable"
                        } else {
                            "marketplace_enable"
                        },
                        id,
                        controls.scope,
                    ),
                    enabled,
                    palette,
                    cx,
                ))
                .child(extension_button(
                    locale.text("ui.uninstall"),
                    marketplace_action("marketplace_uninstall", id, controls.scope),
                    enabled,
                    palette,
                    cx,
                ));
            if extension_items(catalog, "upgrades").iter().any(|v| {
                v["plugin"]["id"] == plugin["id"] && v["plugin"]["scope"] == controls.scope
            }) {
                actions = actions.child(extension_button(
                    locale.text("ui.upgrade"),
                    marketplace_action("marketplace_upgrade", id, controls.scope),
                    enabled,
                    palette,
                    cx,
                ));
            }
        } else {
            actions = actions.child(extension_button(
                locale.text("ui.install"),
                marketplace_action("marketplace_install", id, controls.scope),
                enabled && !id.is_empty(),
                palette,
                cx,
            ));
        }
        let detail = format!(
            "{} · {} · {}\n{}",
            id,
            extension_text(plugin, "version"),
            if installed.is_some() {
                locale.text("ui.installed")
            } else {
                locale.text("ui.available")
            },
            extension_text(plugin, "description")
        );
        plugins.push(
            extension_row(
                extension_text(plugin, "name"),
                &detail,
                Some(plugin_mark(plugin, palette)),
                palette,
            )
            .child(actions)
            .into_any_element(),
        );
    }
    div()
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
                        .min_w_0()
                        .h(px(36.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .child(controls.source.clone()),
                )
                .child(extension_button(
                    locale.text("ui.addMarketplace"),
                    json!({"kind":"marketplace_add","target":source}),
                    enabled && !source.is_empty(),
                    palette,
                    cx,
                )),
        )
        .child(extension_list(
            locale.text("ui.marketplaceSources"),
            rows,
            palette,
            locale,
        ))
        .child(
            div()
                .id("marketplace-scope")
                .role(Role::RadioGroup)
                .aria_label(locale.text("ui.installationScope"))
                .flex()
                .items_center()
                .gap_2()
                .children(
                    [
                        ("user", locale.text("ui.userScope")),
                        ("project", locale.text("ui.currentProject")),
                    ]
                    .into_iter()
                    .map(|(scope, label)| {
                        div()
                            .id(format!("marketplace-scope-{scope}"))
                            .role(Role::RadioButton)
                            .aria_label(label)
                            .aria_selected(controls.scope == scope)
                            .tab_stop(true)
                            .px_3()
                            .py_2()
                            .rounded(px(7.))
                            .text_xs()
                            .bg(if controls.scope == scope {
                                palette.hover
                            } else {
                                palette.paper
                            })
                            .text_color(palette.ink)
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.extension_settings.scope = scope;
                                cx.notify();
                            }))
                            .child(label)
                    }),
                ),
        )
        .child(extension_list(
            locale.text("ui.marketplacePlugins"),
            plugins,
            palette,
            locale,
        ))
        .into_any_element()
}

fn settings_security_body(
    state: &AppState,
    native: &NativeSettings,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    if native.security_draft.is_null() {
        return div().flex().flex_col().gap_3()
            .child(locale.text("ui.loadingSecuritySettings"))
            .child(settings_action_button(30, locale.text("ui.reload"),
                state.connection.connected, false, palette, cx,
                json!({"kind":"get_security_config", "sessionId":state.navigation.current_session_id})))
            .into_any_element();
    }
    let editable = state.connection.connected && !native.security_busy;
    let config = &native.security_draft;
    let enabled = config["enabled"].as_bool().unwrap_or(false);
    let mode = config["defaultMode"].as_str().unwrap_or("standard");
    let dirty = native.security_dirty(cx);
    let field_labels = [
        (locale.text("ui.deepScanWorkers"), "1–32"),
        (locale.text("ui.investigatorsPerAudit"), "0–32"),
        (locale.text("ui.noNewThreshold"), "1–1000"),
        (locale.text("ui.consecutiveErrorThreshold"), "1–1000"),
        (locale.text("ui.discoveryRunCap"), "1–1000"),
        (locale.text("ui.deadlineHours"), "0.5–96"),
    ];
    let numbers = native
        .security_fields
        .iter()
        .enumerate()
        .map(|(index, input)| {
            let (key, _, _) = SECURITY_NUMBERS[index];
            let (label, hint) = field_labels[index];
            settings_children_row(
                label,
                hint,
                div()
                    .id(format!("security-field-{key}"))
                    .role(Role::Group)
                    .aria_label(label)
                    .w(px(130.))
                    .h(px(36.))
                    .rounded(px(8.))
                    .border_1()
                    .border_color(palette.border_strong)
                    .bg(palette.paper)
                    .child(input.clone())
                    .into_any_element(),
                palette,
            )
            .into_any_element()
        })
        .collect::<Vec<_>>();
    let modes = [
        ("standard", locale.text("ui.standard")),
        ("deep", locale.text("ui.deep")),
    ]
    .into_iter()
    .map(|(value, label)| {
        div()
            .id(format!("security-mode-{value}"))
            .role(Role::RadioButton)
            .aria_label(label)
            .aria_selected(mode == value)
            .tab_stop(editable)
            .h(px(32.))
            .px_3()
            .rounded(px(7.))
            .flex()
            .items_center()
            .bg(if mode == value {
                palette.accent_soft
            } else {
                palette.paper
            })
            .when(editable, |b| {
                b.cursor_pointer().hover(move |s| s.bg(palette.hover))
            })
            .on_click(cx.listener(move |this, _, _, cx| {
                if editable {
                    this.native_settings.security_draft["defaultMode"] = json!(value);
                    this.native_settings.security_saved = false;
                    cx.notify();
                }
            }))
            .child(label)
            .into_any_element()
    })
    .collect::<Vec<_>>();
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
                .child(settings_card_header(
                    locale.text("ui.executionPolicy"),
                    locale.text("ui.appliesToNewScansActiveScansRetainTheirCapturedSettings"),
                    palette,
                ))
                .child(settings_children_row(
                    locale.text("ui.enableSecurityScans"),
                    locale.text("ui.allowStandardAndDeepScansWithoutDeletingResults"),
                    div()
                        .id("security-enabled")
                        .role(Role::Switch)
                        .aria_label(locale.text("ui.enableSecurityScans"))
                        .aria_toggled(enabled.into())
                        .tab_stop(editable)
                        .when(editable, |b| b.cursor_pointer())
                        .on_click(cx.listener(move |this, _, _, cx| {
                            if editable {
                                this.native_settings.security_draft["enabled"] = json!(!enabled);
                                this.native_settings.security_saved = false;
                                cx.notify();
                            }
                        }))
                        .child(settings_switch(enabled, palette))
                        .into_any_element(),
                    palette,
                ))
                .child(settings_children_row(
                    locale.text("ui.defaultMode"),
                    locale.text("ui.defaultAuditDepthForNewScans"),
                    div().flex().gap_2().children(modes).into_any_element(),
                    palette,
                ))
                .children(numbers),
        )
        .child(settings_detail_card(
            locale.text("ui.publication"),
            locale.text("ui.readOnlyExternalPublicationToolsAreConfiguredByTheHost"),
            vec![settings_detail_row(
                locale.text("ui.publicationTool"),
                locale.text("ui.thisPageNeverModifiesToolArgumentsCredentialsOrModelRoutes"),
                state.security.config["publicationTool"]
                    .as_str()
                    .filter(|s| !s.is_empty())
                    .unwrap_or(locale.text("ui.disabled2"))
                    .to_string(),
                palette,
            )],
            palette,
        ))
        .child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(
                    div()
                        .id("security-save-status")
                        .role(Role::Status)
                        .text_sm()
                        .text_color(palette.muted)
                        .child(if dirty {
                            locale.text("ui.unsavedChanges")
                        } else if native.security_saved {
                            locale.text("ui.securitySettingsSaved")
                        } else {
                            ""
                        }),
                )
                .child(
                    div()
                        .id("security-save")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.saveSecuritySettings"))
                        .tab_stop(editable && dirty)
                        .h(px(36.))
                        .px_3()
                        .rounded(px(8.))
                        .bg(if editable && dirty {
                            palette.button
                        } else {
                            palette.paper_muted
                        })
                        .text_color(if editable && dirty {
                            palette.button_text
                        } else {
                            palette.muted
                        })
                        .flex()
                        .items_center()
                        .when(editable && dirty, |b| b.cursor_pointer())
                        .on_click(cx.listener(move |this, _, _, cx| {
                            if editable && dirty {
                                this.save_security_settings(cx);
                            }
                        }))
                        .child(if native.security_busy {
                            locale.text("ui.saving")
                        } else {
                            locale.text("ui.saveSecuritySettings")
                        }),
                ),
        )
        .into_any_element()
}

fn settings_archive_body(
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

fn archived_session_groups(
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

#[derive(Clone)]
struct UsageHeatCell {
    in_range: bool,
    tokens: i64,
    date: Option<String>,
}

fn usage_i64(value: &serde_json::Value, key: &str) -> i64 {
    value
        .get(key)
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default()
}

fn usage_compact(value: f64, digits: usize) -> String {
    let mut rendered = format!("{value:.digits$}");
    while rendered.contains('.') && rendered.ends_with('0') {
        rendered.pop();
    }
    if rendered.ends_with('.') {
        rendered.pop();
    }
    rendered
}

fn format_usage_count(value: i64, locale: Locale) -> String {
    let abs = value.unsigned_abs();
    if let Some(unit) = locale
        .compact_units()
        .iter()
        .find(|unit| abs >= unit.divisor)
    {
        return format!(
            "{}{}",
            usage_compact(value as f64 / unit.divisor as f64, unit.digits),
            unit.suffix
        );
    }
    format_usage_exact(value)
}

fn format_usage_exact(value: i64) -> String {
    let mut rendered = value.unsigned_abs().to_string();
    let mut index = rendered.len();
    while index > 3 {
        index -= 3;
        rendered.insert(index, ',');
    }
    format!("{}{rendered}", if value < 0 { "-" } else { "" })
}

fn format_usage_duration(milliseconds: i64, locale: Locale) -> String {
    if milliseconds <= 0 {
        return "—".to_string();
    }
    let total_minutes = ((milliseconds as f64 / 60_000.).round() as i64).max(0);
    let hours = total_minutes / 60;
    let minutes = total_minutes % 60;
    if hours > 0 && minutes > 0 {
        locale.format(
            "duration.hoursMinutes",
            &[
                ("hours", hours.to_string()),
                ("minutes", minutes.to_string()),
            ],
        )
    } else if hours > 0 {
        locale.format("duration.hours", &[("hours", hours.to_string())])
    } else if total_minutes > 0 {
        locale.format(
            "duration.minutes",
            &[("minutes", total_minutes.max(1).to_string())],
        )
    } else {
        locale.format(
            "duration.seconds",
            &[("seconds", (milliseconds / 1_000).max(1).to_string())],
        )
    }
}

fn usage_cache_hit(report: &serde_json::Value) -> String {
    if !report
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        return "—".to_string();
    }
    let input = usage_i64(report, "reportedInputTokens");
    if input <= 0 {
        return "—".to_string();
    }
    usage_compact(
        usage_i64(report, "cacheReadTokens") as f64 * 100. / input as f64,
        1,
    ) + "%"
}

fn usage_day_number(value: &str) -> Option<i64> {
    let mut parts = value.split('-');
    let year = parts.next()?.parse::<i64>().ok()?;
    let month = parts.next()?.parse::<u32>().ok()?;
    let day = parts.next()?.parse::<u32>().ok()?;
    if parts.next().is_some() {
        return None;
    }
    let days_in_month = usage_days_in_month(year, month)?;
    if !(1..=days_in_month).contains(&day) {
        return None;
    }
    let adjusted_year = year - i64::from(month <= 2);
    let era = adjusted_year.div_euclid(400);
    let year_of_era = adjusted_year - era * 400;
    let adjusted_month = month as i64 + 9 - 12 * i64::from(month > 2);
    let day_of_year = (153 * adjusted_month + 2) / 5 + day as i64 - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    Some(era * 146_097 + day_of_era - 719_468)
}

fn usage_days_in_month(year: i64, month: u32) -> Option<u32> {
    const DAYS: [u32; 12] = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    let days = *DAYS.get(month.checked_sub(1)? as usize)?;
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    Some(days + u32::from(month == 2 && leap))
}

fn fill_usage_heatmap(
    report: &serde_json::Value,
    cells: &mut [UsageHeatCell],
    leading: usize,
    from: i64,
    to: i64,
) -> i64 {
    let mut peak = 0;
    for day in report
        .get("days")
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
    {
        let Some(date) = day.get("date").and_then(serde_json::Value::as_str) else {
            continue;
        };
        let Some(index) = usage_day_number(date) else {
            continue;
        };
        if !(from..=to).contains(&index) {
            continue;
        }
        let tokens = usage_i64(day, "tokens").max(0);
        let cell = &mut cells[leading + (index - from) as usize];
        cell.tokens = tokens;
        cell.date = Some(date.to_string());
        peak = peak.max(tokens);
    }
    peak
}

fn usage_heatmap(report: &serde_json::Value) -> (Vec<Vec<UsageHeatCell>>, i64) {
    let Some(from) = report
        .get("from")
        .and_then(serde_json::Value::as_str)
        .and_then(usage_day_number)
    else {
        return (Vec::new(), 0);
    };
    let Some(to) = report
        .get("to")
        .and_then(serde_json::Value::as_str)
        .and_then(usage_day_number)
    else {
        return (Vec::new(), 0);
    };
    if to < from || to - from > 370 {
        return (Vec::new(), 0);
    }
    let leading = (from + 4).rem_euclid(7) as usize;
    let days = (to - from + 1) as usize;
    let slots = (leading + days).div_ceil(7) * 7;
    let mut cells = vec![
        UsageHeatCell {
            in_range: false,
            tokens: 0,
            date: None,
        };
        slots
    ];
    for cell in cells.iter_mut().skip(leading).take(days) {
        cell.in_range = true;
    }
    let peak = fill_usage_heatmap(report, &mut cells, leading, from, to);
    (cells.chunks(7).map(|week| week.to_vec()).collect(), peak)
}

fn usage_activity_level(tokens: i64, peak: i64) -> usize {
    if tokens <= 0 || peak <= 0 {
        0
    } else if tokens * 100 >= peak * 66 {
        3
    } else if tokens * 100 >= peak * 33 {
        2
    } else {
        1
    }
}

fn usage_heat_color(level: usize, palette: ThemePalette) -> Rgba {
    match level {
        1 => rgb(0xb8dcff),
        2 => rgb(0x62adf8),
        3 => palette.accent,
        _ => palette.paper_muted,
    }
}

fn usage_fact(label: &'static str, value: String, palette: ThemePalette) -> gpui::Div {
    div()
        .min_w(px(104.))
        .flex_1()
        .flex()
        .flex_col()
        .gap(px(3.))
        .child(div().text_xs().text_color(palette.faint).child(label))
        .child(div().text_sm().text_color(palette.ink).child(value))
}

fn usage_loading_view(palette: ThemePalette, locale: Locale) -> gpui::AnyElement {
    div()
        .min_h(px(120.))
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .flex()
        .items_center()
        .justify_center()
        .gap_2()
        .child(
            div()
                .size(px(9.))
                .rounded_full()
                .bg(palette.accent)
                .with_animation(
                    "usage-loading",
                    Animation::new(Duration::from_millis(900)).repeat(),
                    |dot, progress| {
                        dot.opacity(0.25 + (progress * std::f32::consts::PI).sin().abs() * 0.75)
                    },
                ),
        )
        .child(
            div()
                .text_sm()
                .text_color(palette.muted)
                .child(locale.text("ui.loadingUsage")),
        )
        .into_any_element()
}

fn usage_model_identity(
    model: &serde_json::Value,
    state: &AppState,
    locale: Locale,
) -> (String, String, String) {
    let provider_id = model
        .get("provider")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let model_id = model
        .get("model")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let provider = state.catalogs.providers.iter().find(|provider| {
        provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
    });
    let title = if model_id.trim().is_empty() {
        locale.text("ui.unlabeledModel").to_string()
    } else {
        settings_route_model_name(&state.catalogs.providers, provider_id, model_id)
    };
    let provider_name = provider
        .map(|provider| provider_display_name(provider, provider_id))
        .unwrap_or_else(|| provider_id.to_string());
    let logo = provider
        .map(|provider| provider_logo_id(provider, provider_id))
        .unwrap_or_else(|| provider_id.to_string());
    (title, provider_name, logo)
}

fn usage_kind_label(kind: &str, locale: Locale) -> String {
    match kind {
        "main" => locale.text("ui.main").into(),
        "subagent" => locale.text("ui.subagent").into(),
        "compaction" => locale.text("ui.compaction").into(),
        "team" => locale.text("ui.team").into(),
        "review" => locale.text("ui.review").into(),
        _ => kind.to_string(),
    }
}

fn usage_breakdown_header(title: &'static str, palette: ThemePalette) -> gpui::Div {
    div()
        .h(px(44.))
        .px_4()
        .flex()
        .items_center()
        .text_sm()
        .font_weight(gpui::FontWeight::SEMIBOLD)
        .text_color(palette.ink)
        .child(title)
}

fn usage_model_row(
    model: &serde_json::Value,
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::Div {
    let (title, provider_name, logo) = usage_model_identity(model, state, locale);
    let model_id = model
        .get("model")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let requests = usage_i64(model, "requests");
    let meta = [
        provider_name,
        if model_id != title {
            model_id.to_string()
        } else {
            String::new()
        },
        locale.format(
            "ui.requestsRequests",
            &[("requests", (requests).to_string())],
        ),
    ]
    .into_iter()
    .filter(|value| !value.is_empty())
    .collect::<Vec<_>>()
    .join(" · ");
    let cache = if model
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(model, "cacheReadTokens"), locale)
    } else {
        "—".to_string()
    };
    let token_detail = locale.format(
        "ui.inputArg0OutputArg1CacheArg2",
        &[
            (
                "arg0",
                (format_usage_count(usage_i64(model, "inputTokens"), locale)).to_string(),
            ),
            (
                "arg1",
                (format_usage_count(usage_i64(model, "outputTokens"), locale)).to_string(),
            ),
            ("arg2", (cache).to_string()),
        ],
    );
    div()
        .min_h(px(66.))
        .px_4()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(provider_logo(&logo, 18., palette.ink))
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
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .text_color(palette.ink)
                        .child(title),
                )
                .child(
                    div()
                        .truncate()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(meta),
                )
                .child(
                    div()
                        .truncate()
                        .text_size(px(10.))
                        .text_color(palette.muted)
                        .child(token_detail),
                ),
        )
        .child(
            div()
                .text_sm()
                .text_color(palette.ink_soft)
                .child(format_usage_count(usage_i64(model, "tokens"), locale)),
        )
}

fn usage_model_rows(state: &AppState, palette: ThemePalette, locale: Locale) -> gpui::AnyElement {
    let models = state
        .settings
        .usage
        .get("models")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let rows = models
        .iter()
        .map(|model| usage_model_row(model, state, palette, locale))
        .collect::<Vec<_>>();
    div()
        .min_w_0()
        .flex_1()
        .border_l_1()
        .border_color(palette.border)
        .child(usage_breakdown_header(locale.text("ui.models2"), palette))
        .when(models.is_empty(), |section| {
            section.child(
                div()
                    .h(px(58.))
                    .px_4()
                    .flex()
                    .items_center()
                    .text_sm()
                    .text_color(palette.faint)
                    .child(locale.text("ui.noModelUsage")),
            )
        })
        .children(rows)
        .into_any_element()
}

fn usage_kind_rows(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let kinds = report
        .get("kinds")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let total = kinds
        .iter()
        .map(|row| usage_i64(row, "tokens"))
        .sum::<i64>();
    let rows = kinds
        .iter()
        .map(|row| {
            let tokens = usage_i64(row, "tokens");
            let fraction = if total > 0 {
                (tokens as f32 / total as f32).clamp(0.04, 1.)
            } else {
                0.
            };
            let requests = usage_i64(row, "requests");
            div()
                .min_h(px(58.))
                .px_4()
                .py_2()
                .border_t_1()
                .border_color(palette.border)
                .flex()
                .flex_col()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .flex()
                        .items_center()
                        .child(
                            div()
                                .min_w_0()
                                .flex_1()
                                .flex()
                                .flex_col()
                                .child(
                                    div()
                                        .truncate()
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(usage_kind_label(
                                            row.get("kind")
                                                .and_then(serde_json::Value::as_str)
                                                .unwrap_or_default(),
                                            locale,
                                        )),
                                )
                                .child(div().text_xs().text_color(palette.faint).child(
                                    locale.format(
                                        "ui.requestsRequests",
                                        &[("requests", (requests).to_string())],
                                    ),
                                )),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.ink_soft)
                                .child(format_usage_count(tokens, locale)),
                        ),
                )
                .child(
                    div()
                        .w_full()
                        .h(px(4.))
                        .rounded_full()
                        .bg(palette.paper_muted)
                        .overflow_hidden()
                        .child(
                            div()
                                .h_full()
                                .w(relative(fraction))
                                .rounded_full()
                                .bg(palette.ink_soft),
                        ),
                )
        })
        .collect::<Vec<_>>();
    div()
        .min_w_0()
        .flex_1()
        .child(usage_breakdown_header(
            locale.text("ui.requestKinds"),
            palette,
        ))
        .children(rows)
        .into_any_element()
}

fn usage_heat_tooltip(
    date: &str,
    tokens: i64,
    week_index: usize,
    day_index: usize,
    total_weeks: usize,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let middle = week_index >= 7 && week_index + 7 < total_weeks;
    div()
        .absolute()
        .top(px(day_index as f32 * 11. - 34.))
        .when(week_index < 7, |tooltip| tooltip.left_0())
        .when(week_index + 7 >= total_weeks, |tooltip| tooltip.right_0())
        .when(middle, |tooltip| {
            tooltip
                .left(relative((week_index as f32 + 0.5) / total_weeks as f32))
                .ml(px(-90.))
        })
        .h(px(28.))
        .px_2()
        .rounded(px(7.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.14))
                .blur_radius(px(14.)),
        ])
        .flex()
        .items_center()
        .text_xs()
        .text_color(palette.muted)
        .child(format!(
            "{date} · {} Token",
            format_usage_count(tokens, locale)
        ))
        .into_any_element()
}

fn usage_heatmap_view(
    weeks: &[Vec<UsageHeatCell>],
    peak: i64,
    usage_hover: Option<&(String, i64)>,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let hovered = usage_hover.and_then(|(hovered_date, tokens)| {
        weeks.iter().enumerate().find_map(|(week_index, week)| {
            week.iter().enumerate().find_map(|(day_index, cell)| {
                (cell.date.as_deref() == Some(hovered_date.as_str())).then(|| {
                    usage_heat_tooltip(
                        hovered_date,
                        *tokens,
                        week_index,
                        day_index,
                        weeks.len(),
                        palette,
                        locale,
                    )
                })
            })
        })
    });
    div()
        .relative()
        .w_full()
        .child(
            div()
                .w_full()
                .flex()
                .gap(px(3.))
                .children(weeks.iter().enumerate().map(|(week_index, week)| {
                    div()
                        .min_w_0()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap(px(3.))
                        .children(week.iter().enumerate().map(|(day_index, cell)| {
                            let date = cell.date.clone();
                            let tokens = cell.tokens;
                            div()
                                .id(("usage-day", week_index * 7 + day_index))
                                .w_full()
                                .h(px(8.))
                                .rounded(px(2.))
                                .bg(if cell.in_range {
                                    usage_heat_color(
                                        usage_activity_level(cell.tokens, peak),
                                        palette,
                                    )
                                } else {
                                    rgba(0x00000000)
                                })
                                .when_some(date, |day, date| {
                                    let clear_key = date.clone();
                                    day.aria_label(format!(
                                        "{date}: {} Token",
                                        format_usage_exact(tokens)
                                    ))
                                    .cursor_pointer()
                                    .hover(|style| style.opacity(0.72))
                                    .on_hover(cx.listener(
                                        move |this, hovered: &bool, _, cx| {
                                            if *hovered {
                                                this.usage_hover = Some((date.clone(), tokens));
                                            } else if this
                                                .usage_hover
                                                .as_ref()
                                                .is_some_and(|(current, _)| current == &clear_key)
                                            {
                                                this.usage_hover = None;
                                            }
                                            cx.notify();
                                        },
                                    ))
                                })
                        }))
                })),
        )
        .when_some(hovered, |heatmap, tooltip| heatmap.child(tooltip))
        .into_any_element()
}

fn usage_model_share_view(
    report: &serde_json::Value,
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
) -> Option<gpui::AnyElement> {
    let models = report.get("models")?.as_array()?;
    let total = models
        .iter()
        .map(|model| usage_i64(model, "tokens"))
        .sum::<i64>();
    let colors = [
        palette.accent,
        rgb(0x55a9ff),
        rgb(0x8bc5ff),
        palette.border_strong,
    ];
    let shares = models
        .iter()
        .take(4)
        .enumerate()
        .map(|(index, model)| {
            let fraction = if total > 0 {
                usage_i64(model, "tokens") as f32 / total as f32
            } else {
                0.
            };
            (
                index,
                usage_model_identity(model, state, locale).0,
                fraction,
            )
        })
        .collect::<Vec<_>>();
    (!shares.is_empty()).then(|| {
        div()
            .mt_1()
            .flex()
            .flex_col()
            .gap_3()
            .child(
                div()
                    .text_xs()
                    .text_color(palette.faint)
                    .child(locale.text("ui.byModel")),
            )
            .child(
                div()
                    .w_full()
                    .h(px(6.))
                    .rounded_full()
                    .overflow_hidden()
                    .bg(palette.paper_muted)
                    .flex()
                    .children(shares.iter().map(|(index, _, fraction)| {
                        div().h_full().w(relative(*fraction)).bg(colors[*index])
                    })),
            )
            .child(div().flex().flex_wrap().gap_3().children(shares.iter().map(
                |(index, title, fraction)| {
                    div()
                        .flex()
                        .items_center()
                        .gap(px(6.))
                        .text_xs()
                        .text_color(palette.muted)
                        .child(div().size(px(6.)).rounded_full().bg(colors[*index]))
                        .child(div().max_w(px(150.)).truncate().child(title.clone()))
                        .child(format!("{:.0}%", fraction * 100.))
                },
            )))
            .into_any_element()
    })
}

fn usage_activity_view(
    report: &serde_json::Value,
    state: &AppState,
    usage_hover: Option<&(String, i64)>,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (weeks, peak) = usage_heatmap(report);
    let heatmap = usage_heatmap_view(&weeks, peak, usage_hover, palette, locale, cx);
    let content = div()
        .w_full()
        .p_4()
        .pt_3()
        .flex()
        .flex_col()
        .gap_3()
        .child(heatmap)
        .child(
            div()
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.past366Days")),
                )
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(4.))
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.less"))
                        .children((0..4).map(|level| {
                            div()
                                .size(px(9.))
                                .rounded(px(2.))
                                .bg(usage_heat_color(level, palette))
                        }))
                        .child(locale.text("ui.more")),
                ),
        )
        .when_some(
            usage_model_share_view(report, state, palette, locale),
            |activity, share| activity.child(share),
        );
    let content = if reduced_motion {
        content.into_any_element()
    } else {
        content
            .with_animation(
                ("usage-activity", usage_i64(report, "totalTokens") as usize),
                Animation::new(USAGE_REVEAL_TRANSITION).with_easing(gpui::ease_out_quint()),
                |activity, progress| {
                    activity
                        .relative()
                        .top(px(5. * (1. - progress)))
                        .opacity(progress)
                },
            )
            .into_any_element()
    };
    div()
        .border_t_1()
        .border_color(palette.border)
        .child(
            div()
                .h(px(44.))
                .px_4()
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.tokenActivity")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.daily")),
                ),
        )
        .child(content)
        .into_any_element()
}

fn usage_total_node(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let total = usage_i64(report, "totalTokens");
    let total_node = div()
        .mt_2()
        .px_4()
        .text_size(px(34.))
        .line_height(px(39.))
        .font_weight(gpui::FontWeight::MEDIUM)
        .text_color(palette.ink);
    if reduced_motion {
        total_node
            .child(format_usage_count(total, locale))
            .into_any_element()
    } else {
        total_node
            .with_animation(
                ("usage-total", total as usize),
                Animation::new(USAGE_REVEAL_TRANSITION).with_easing(gpui::ease_out_quint()),
                move |node, progress| {
                    node.child(format_usage_count(
                        (total as f64 * progress as f64).round() as i64,
                        locale,
                    ))
                },
            )
            .into_any_element()
    }
}

fn usage_token_split(report: &serde_json::Value, locale: Locale) -> Vec<String> {
    let cache_read = if report
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(report, "cacheReadTokens"), locale)
    } else {
        "—".into()
    };
    let cache_write = if report
        .get("cacheWriteReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(report, "cacheWriteTokens"), locale)
    } else {
        "—".into()
    };
    [
        (
            "usage.input",
            format_usage_count(usage_i64(report, "inputTokens"), locale),
        ),
        (
            "usage.output",
            format_usage_count(usage_i64(report, "outputTokens"), locale),
        ),
        ("usage.cacheRead", cache_read),
        ("usage.cacheWrite", cache_write),
        ("usage.cacheHit", usage_cache_hit(report)),
    ]
    .into_iter()
    .map(|(key, value)| locale.format(key, &[("value", value)]))
    .collect()
}

fn usage_report_facts(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> Vec<gpui::Div> {
    vec![
        usage_fact(
            locale.text("ui.sessions"),
            format_usage_count(usage_i64(report, "sessions"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.runs"),
            format_usage_count(usage_i64(report, "runs"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.requests"),
            format_usage_count(usage_i64(report, "requests"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.peakDay"),
            if usage_i64(report, "peakDayTokens") > 0 {
                format_usage_count(usage_i64(report, "peakDayTokens"), locale)
            } else {
                "—".into()
            },
            palette,
        ),
        usage_fact(
            locale.text("ui.currentStreak"),
            locale.format(
                "ui.arg0Days",
                &[("arg0", (usage_i64(report, "currentStreak")).to_string())],
            ),
            palette,
        ),
        usage_fact(
            locale.text("ui.longestStreak"),
            locale.format(
                "ui.arg0Days",
                &[("arg0", (usage_i64(report, "longestStreak")).to_string())],
            ),
            palette,
        ),
        usage_fact(
            locale.text("ui.longestRun"),
            format_usage_duration(usage_i64(report, "longestRunMs"), locale),
            palette,
        ),
    ]
}

fn usage_report_view(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    usage_hover: Option<&(String, i64)>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let report = &state.settings.usage;
    let reduced_motion = state
        .settings
        .appearance
        .get("reducedMotion")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let from = report
        .get("from")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let to = report
        .get("to")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    div()
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(
            div()
                .px_4()
                .pt_4()
                .flex()
                .flex_col()
                .gap(px(3.))
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.usageLedger")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(palette.muted)
                        .child(locale.format(
                            "ui.usageFromFromToTo",
                            &[("from", (from).to_string()), ("to", (to).to_string())],
                        )),
                ),
        )
        .child(usage_total_node(report, palette, locale, reduced_motion))
        .child(
            div()
                .px_4()
                .text_xs()
                .text_color(palette.muted)
                .child(locale.text("ui.totalTokens")),
        )
        .child(
            div()
                .mt_3()
                .px_4()
                .pb_4()
                .flex()
                .flex_wrap()
                .gap_3()
                .children(
                    usage_token_split(report, locale)
                        .into_iter()
                        .map(|value| div().text_xs().text_color(palette.muted).child(value)),
                ),
        )
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .p_4()
                .flex()
                .flex_wrap()
                .gap_3()
                .children(usage_report_facts(report, palette, locale)),
        )
        .child(usage_activity_view(
            report,
            state,
            usage_hover,
            palette,
            locale,
            reduced_motion,
            cx,
        ))
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .flex()
                .items_start()
                .child(usage_kind_rows(report, palette, locale))
                .child(usage_model_rows(state, palette, locale)),
        )
        .into_any_element()
}

fn settings_usage_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    usage_hover: Option<&(String, i64)>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let scope = state
        .settings
        .usage
        .get("scope")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("project");
    let project_scope = scope != "all";
    let refresh_scope = if project_scope { "project" } else { "all" };
    let loading = state.settings.usage.is_null();
    let empty = state
        .settings
        .usage
        .get("empty")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let report =
        if loading {
            usage_loading_view(palette, locale)
        } else if empty {
            div()
                .min_h(px(116.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .px_4()
                .flex()
                .flex_col()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.noUsageYetCompletedSessionsWillAppearHere")),
                )
                .child(div().text_sm().text_color(palette.muted).child(locale.text(
                    "ui.onlyCompletedModelRequestsCountUnknownCacheTelemetryRemainsUnreported",
                )))
                .into_any_element()
        } else {
            usage_report_view(state, palette, locale, usage_hover, cx)
        };
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .h(px(40.))
                .flex()
                .items_center()
                .child(
                    div()
                        .h(px(38.))
                        .p(px(3.))
                        .rounded(px(9.))
                        .bg(palette.paper_muted)
                        .flex()
                        .child(
                            div()
                                .id("usage-scope-project")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.currentProject"))
                                .aria_selected(project_scope)
                                .tab_stop(state.connection.connected)
                                .h_full()
                                .px_3()
                                .rounded(px(7.))
                                .bg(if project_scope {
                                    palette.paper
                                } else {
                                    palette.paper_muted
                                })
                                .text_color(if project_scope {
                                    palette.ink
                                } else {
                                    palette.muted
                                })
                                .text_sm()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let id = this
                                        .runtime
                                        .request(Method::UsageReport, json!({"scope": "project"}));
                                    this.pending_requests.insert(id, PendingRequest::Usage);
                                    cx.notify();
                                }))
                                .child(locale.text("ui.currentProject")),
                        )
                        .child(
                            div()
                                .id("usage-scope-all")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.allProjects"))
                                .aria_selected(!project_scope)
                                .tab_stop(state.connection.connected)
                                .h_full()
                                .px_3()
                                .rounded(px(7.))
                                .bg(if project_scope {
                                    palette.paper_muted
                                } else {
                                    palette.paper
                                })
                                .text_sm()
                                .text_color(if project_scope {
                                    palette.muted
                                } else {
                                    palette.ink
                                })
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let id = this
                                        .runtime
                                        .request(Method::UsageReport, json!({"scope": "all"}));
                                    this.pending_requests.insert(id, PendingRequest::Usage);
                                    cx.notify();
                                }))
                                .child(locale.text("ui.allProjects")),
                        ),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .id("refresh-usage")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.refresh"))
                        .tab_stop(state.connection.connected)
                        .h(px(34.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .on_click(cx.listener(move |this, _, _, cx| {
                            let id = this
                                .runtime
                                .request(Method::UsageReport, json!({"scope": refresh_scope}));
                            this.pending_requests.insert(id, PendingRequest::Usage);
                            cx.notify();
                        }))
                        .child(icon("refresh-cw", 14., palette.muted))
                        .child(locale.text("ui.refresh")),
                ),
        )
        .child(report)
        .into_any_element()
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

fn inventory_label(value: &serde_json::Value, index: usize, locale: Locale) -> String {
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
    .unwrap_or_else(|| locale.format("inventory.item", &[("number", (index + 1).to_string())]))
}

fn settings_action_button(
    id: usize,
    label: &'static str,
    enabled: bool,
    selected: bool,
    palette: ThemePalette,
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
        .h(px(32.))
        .px(px(10.))
        .rounded(px(7.))
        .bg(pick(selected, palette.paper_muted, palette.paper))
        .border_1()
        .border_color(pick(selected, palette.border_strong, palette.paper))
        .when(selected, |button| {
            button.shadow(vec![
                BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.15, 0.15, 0.07))
                    .blur_radius(px(2.)),
            ])
        })
        .text_color(pick(enabled, palette.ink_soft, palette.faint))
        .text_xs()
        .font_weight(pick(
            selected,
            gpui::FontWeight::SEMIBOLD,
            gpui::FontWeight::NORMAL,
        ))
        .flex()
        .items_center()
        .justify_center()
        .when(selectable, |button| button.flex_1())
        .when(enabled, |button| {
            button
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
        })
        .on_click(cx.listener(move |this, _, _, cx| {
            if enabled {
                this.runtime.request(Method::Execute, payload.clone());
                cx.notify();
            }
        }))
        .child(label)
        .into_any_element()
}

pub(super) fn projects_surface(
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

fn security_progress_fraction(progress: &serde_json::Value, status: &str) -> f32 {
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

fn security_compact_text(value: &str, limit: usize) -> String {
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

pub(super) fn security_surface(
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

pub(super) fn pull_requests_surface(
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

pub(super) fn workspace_files_surface(
    state: &AppState,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
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
        .aria_label(locale.text("files.title"))
        .flex_1()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(
            div()
                .h(px(54.))
                .px(px(26.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .text_color(palette.muted)
                        .child(locale.text("ui.workspaceEsc")),
                )
                .child(
                    div()
                        .text_size(px(17.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.projectFiles")),
                )
                .child(
                    div().flex_1().text_sm().text_color(palette.muted).child(
                        locale.text("ui.keepHierarchyPreviewAndContextActionsInOneSightline"),
                    ),
                )
                .child(
                    div()
                        .h(px(32.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_sm()
                        .flex()
                        .items_center()
                        .child(locale.text("ui.searchFiles")),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .id("workspace-entry-list")
                        .w(px(360.))
                        .h_full()
                        .overflow_y_scroll()
                        .border_r_1()
                        .border_color(palette.border)
                        .p_3()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .h(px(34.))
                                .px_2()
                                .rounded(px(8.))
                                .border_1()
                                .border_color(palette.border)
                                .text_sm()
                                .text_color(palette.faint)
                                .flex()
                                .items_center()
                                .child(locale.text("ui.filterByName")),
                        )
                        .child(
                            div()
                                .pt_3()
                                .px_2()
                                .text_sm()
                                .text_color(palette.muted)
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
                                if directory { "⌄  " } else { "    " },
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
                                .h(px(38.))
                                .px_3()
                                .rounded(px(8.))
                                .text_sm()
                                .flex()
                                .items_center()
                                .hover(|row| row.bg(palette.hover))
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
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .h(px(50.))
                                .px_4()
                                .border_b_1()
                                .border_color(palette.border)
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .flex()
                                .items_center()
                                .child(locale.text("ui.filePreview")),
                        )
                        .child(
                            div()
                                .id("workspace-preview")
                                .role(Role::Document)
                                .aria_label(locale.text("files.preview"))
                                .flex_1()
                                .overflow_y_scroll()
                                .p_5()
                                .font_family("SF Mono")
                                .text_sm()
                                .whitespace_normal()
                                .child(if preview.is_empty() {
                                    locale.text("ui.selectAFile").to_string()
                                } else {
                                    preview
                                }),
                        ),
                ),
        )
        .into_any_element()
}

pub(super) fn workspace_changes_surface(
    state: &AppState,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
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
        .aria_label(locale.text("changes.title"))
        .flex_1()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(
            div()
                .h(px(54.))
                .px(px(26.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .text_color(palette.muted)
                        .child(locale.text("ui.workspaceEsc")),
                )
                .child(
                    div()
                        .text_size(px(17.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.codeChanges")),
                )
                .child(
                    div()
                        .flex_1()
                        .text_sm()
                        .text_color(palette.muted)
                        .child(locale.text("ui.keepTheSummaryRiskAndDiffInOneReviewPath")),
                )
                .child(
                    div()
                        .h(px(32.))
                        .px_3()
                        .rounded(px(8.))
                        .bg(palette.button)
                        .text_color(palette.button_text)
                        .text_sm()
                        .flex()
                        .items_center()
                        .child(locale.text("ui.startReview")),
                ),
        )
        .child(
            div()
                .h(px(64.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .children(
                    [
                        (
                            state.workspace.changed_files.to_string(),
                            locale.text("ui.files"),
                            palette.ink,
                        ),
                        (
                            format!("+{}", state.workspace.additions),
                            locale.text("ui.added"),
                            palette.positive,
                        ),
                        (
                            format!("−{}", state.workspace.deletions),
                            locale.text("ui.deleted"),
                            palette.danger,
                        ),
                    ]
                    .into_iter()
                    .map(|(value, label, color)| {
                        div()
                            .flex_1()
                            .px_5()
                            .border_r_1()
                            .border_color(palette.border)
                            .flex()
                            .flex_col()
                            .justify_center()
                            .gap_1()
                            .child(
                                div()
                                    .text_size(px(22.))
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .text_color(color)
                                    .child(value),
                            )
                            .child(div().text_xs().text_color(palette.faint).child(label))
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
                        .id("workspace-change-list")
                        .w(px(360.))
                        .h_full()
                        .overflow_y_scroll()
                        .border_r_1()
                        .border_color(palette.border)
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
                                .min_h(px(58.))
                                .px_3()
                                .rounded(px(8.))
                                .text_sm()
                                .flex()
                                .items_center()
                                .hover(|row| row.bg(palette.hover))
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
                        .aria_label(locale.text("changes.selected"))
                        .flex_1()
                        .h_full()
                        .overflow_y_scroll()
                        .p_5()
                        .font_family("SF Mono")
                        .text_sm()
                        .whitespace_normal()
                        .child(if patch.is_empty() {
                            locale.text("ui.selectAChangedFile").to_string()
                        } else {
                            patch
                        }),
                ),
        )
        .into_any_element()
}

pub(super) fn timeline_entry(
    index: usize,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool, f32, i64),
    agents: &[serde_json::Value],
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
    reply_actions: Option<&ReplyActionsSnapshot>,
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion, horizontal_gutter, live_elapsed_ms) = style;
    let Some(block) = blocks.get(index) else {
        if index == blocks.len()
            && blocks
                .last()
                .is_some_and(|block| block.kind.as_ref() == "user")
        {
            return pending_process_entry(
                index,
                palette,
                locale,
                reduced_motion,
                horizontal_gutter,
                live_elapsed_ms,
            );
        }
        return div().h(px(0.)).into_any_element();
    };
    let kind = block.kind.as_ref();
    if is_thinking_text(block) && !thinking_belongs_to_tool_group(blocks, index) {
        return thinking_process_entry(
            index,
            block,
            palette,
            locale,
            reduced_motion,
            horizontal_gutter,
            live_elapsed_ms,
        );
    }
    if is_hidden_process_block(block) {
        return div().h(px(0.)).into_any_element();
    }
    if is_process_tool_block(block) {
        return tool_group_entry(
            index,
            blocks,
            (
                palette,
                locale,
                reduced_motion,
                horizontal_gutter,
                live_elapsed_ms,
            ),
            expansion,
            owner,
        );
    }
    if is_agent_block(block) {
        let start = blocks[..index]
            .iter()
            .rposition(|candidate| !is_agent_block(candidate))
            .map_or(0, |boundary| boundary + 1);
        if index != start {
            return div().h(px(0.)).into_any_element();
        }
        let end = blocks[index + 1..]
            .iter()
            .position(|candidate| !is_agent_block(candidate))
            .map_or(blocks.len(), |offset| index + 1 + offset);
        let run_id = if block.run_id.is_empty() {
            block.id.as_ref()
        } else {
            block.run_id.as_ref()
        };
        let body = div()
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .child(subagent_run_card(
                index,
                &blocks[start..end],
                run_id,
                agents,
                (palette, locale),
                expansion.clone(),
                owner.clone(),
            ));
        return div()
            .id(("timeline-block", index))
            .role(Role::Article)
            .aria_label(kind.to_string())
            .w_full()
            .min_w_0()
            .px(px(horizontal_gutter))
            .py(px(4.))
            .flex()
            .flex_col()
            .items_center()
            .child(body)
            .into_any_element();
    }
    if kind == "context_compaction" {
        let body = div()
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .child(process_detail_row(
                index,
                0,
                block,
                palette,
                locale,
                reduced_motion,
            ));
        return div()
            .id(("timeline-block", index))
            .role(Role::Article)
            .aria_label(kind.to_string())
            .w_full()
            .min_w_0()
            .px(px(horizontal_gutter))
            .py(px(4.))
            .flex()
            .flex_col()
            .items_center()
            .child(body)
            .into_any_element();
    }
    let hover_key = timeline_message_key(index, block);
    let message_hovered = reply_actions.and_then(|actions| actions.hovered_message.as_deref())
        == Some(hover_key.as_str());
    let row = if kind == "user" {
        let time = message_time(block);
        let bubble = div()
            .w_full()
            .min_w_0()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .flex()
            .justify_end()
            .child(
                div()
                    .min_w_0()
                    .max_w(px(680.))
                    .flex()
                    .flex_col()
                    .items_end()
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .rounded(px(16.))
                            .bg(palette.paper_muted)
                            .text_color(palette.ink)
                            .text_size(px(palette.chat_font_size))
                            .line_height(px(palette.chat_font_size * 1.6))
                            .whitespace_normal()
                            .child(block.content.clone()),
                    )
                    .when_some(time, |bubble, time| {
                        bubble.child(
                            div()
                                .h(px(22.))
                                .mt_1()
                                .pr_1()
                                .flex()
                                .items_center()
                                .text_size(px(11.))
                                .text_color(palette.faint)
                                .opacity(if message_hovered { 1. } else { 0. })
                                .child(time),
                        )
                    }),
            );
        if animate_submitted_user(block.state.as_ref(), reduced_motion) {
            bubble
                .with_animation(
                    ("user-message-submit", index),
                    Animation::new(USER_MESSAGE_SUBMIT_TRANSITION)
                        .with_easing(gpui::ease_out_quint()),
                    |bubble, progress| {
                        bubble
                            .relative()
                            .top(px(8. * (1. - progress)))
                            .opacity(progress)
                    },
                )
                .into_any_element()
        } else {
            bubble.into_any_element()
        }
    } else {
        let active = matches!(
            block.state.as_ref(),
            "streaming" | "running" | "started" | "progress"
        );
        let content = if kind == "assistant" {
            visible_assistant_content(block.content.as_ref())
        } else {
            block.content.as_ref()
        };
        if kind == "assistant" && content.is_empty() && !active {
            return div().h(px(0.)).into_any_element();
        }
        let message = div()
            .w_full()
            .min_w_0()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .when(kind == "error", |message| {
                message
                    .px_3()
                    .py_2()
                    .rounded(px(10.))
                    .bg(palette.paper_muted)
                    .text_color(palette.danger)
            })
            .when(is_thinking_text(block), |message| {
                message.text_color(palette.faint)
            })
            .when(kind != "error" && !is_thinking_text(block), |message| {
                message.text_color(palette.ink)
            })
            .text_size(px(palette.chat_font_size))
            .line_height(px(palette.chat_font_size * 1.6))
            .child(markdown_view(
                index,
                content,
                active,
                reduced_motion,
                palette,
            ));
        if kind == "assistant"
            && block.text_phase.as_ref() != "commentary"
            && !active
            && let Some(actions) = reply_actions
        {
            message
                .child(reply_footer(
                    index,
                    block,
                    blocks,
                    (palette, locale, reduced_motion),
                    actions,
                    message_hovered,
                    owner.clone(),
                ))
                .into_any_element()
        } else {
            message.into_any_element()
        }
    };
    let hover_owner = owner.clone();
    let hover_enabled = reply_actions.is_some();
    let hover_message_key = hover_key.clone();
    let message = div()
        .id(("timeline-message", index))
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .child(row);
    let message = if hover_enabled {
        message.on_hover(move |hovered: &bool, _, cx| {
            let key = hover_message_key.clone();
            hover_owner.update(cx, |this, cx| {
                if *hovered {
                    if this.hovered_message.as_deref() != Some(key.as_str()) {
                        this.hovered_message = Some(key);
                        cx.notify();
                    }
                } else if this.hovered_message.as_deref() == Some(key.as_str()) {
                    this.hovered_message = None;
                    cx.notify();
                }
            });
        })
    } else {
        message
    };
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(kind.to_string())
        .w_full()
        .min_w_0()
        .px(px(horizontal_gutter))
        .py(px(8.))
        .flex()
        .flex_col()
        .items_center()
        .child(message)
        .into_any_element()
}

struct ReplyActionTooltip {
    label: String,
    palette: ThemePalette,
}

impl Render for ReplyActionTooltip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div().p_1().child(
            div()
                .px_2()
                .py_1()
                .rounded(px(7.))
                .bg(self.palette.button)
                .text_color(self.palette.button_text)
                .text_xs()
                .shadow(vec![
                    BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.2))
                        .blur_radius(px(14.)),
                ])
                .child(self.label.clone()),
        )
    }
}

#[derive(Clone)]
struct ReplyHookItem {
    event: String,
    origin: String,
    detail: String,
}

fn reply_footer(
    index: usize,
    block: &Block,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool),
    actions: &ReplyActionsSnapshot,
    show_time: bool,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, _reduced_motion) = style;
    let key = reply_key(index, block);
    let memory_notes = assistant_memory_notes(&block.content);
    let hooks = reply_hooks(
        &actions.hooks,
        &actions.hook_catalog,
        block.run_id.as_ref(),
        locale,
    );
    let popover = actions
        .popover
        .as_ref()
        .filter(|(open_key, _)| open_key == &key)
        .map(|(_, kind)| *kind);
    let feedback = actions.feedback.get(&key).copied().unwrap_or_default();
    let copy_text = visible_assistant_content(&block.content).to_string();
    let sequence = block
        .extra
        .get("sequence")
        .and_then(serde_json::Value::as_i64);
    let assistant_ordinal = blocks[..=index]
        .iter()
        .filter(|candidate| {
            candidate.kind.as_ref() == "assistant" && candidate.text_phase.as_ref() != "commentary"
        })
        .count()
        .saturating_sub(1);
    let anchor = ReplyForkAnchor {
        sequence,
        assistant_ordinal,
    };

    let copy = reply_action_button(
        format!("reply-copy-{index}"),
        "copy",
        locale.text("reply.copy").to_string(),
        false,
        palette,
        owner.clone(),
        move |_, cx| cx.write_to_clipboard(ClipboardItem::new_string(copy_text.clone())),
    );
    let good_key = key.clone();
    let good = reply_action_button(
        format!("reply-good-{index}"),
        "thumbs-up",
        locale.text("reply.good").to_string(),
        feedback == 1,
        palette,
        owner.clone(),
        move |owner, cx| {
            let key = good_key.clone();
            owner.update(cx, |this, cx| {
                if this.reply_feedback.get(&key) == Some(&1) {
                    this.reply_feedback.remove(&key);
                } else {
                    this.reply_feedback.insert(key, 1);
                }
                cx.notify();
            });
        },
    );
    let bad_key = key.clone();
    let bad = reply_action_button(
        format!("reply-bad-{index}"),
        "thumbs-down",
        locale.text("reply.bad").to_string(),
        feedback == -1,
        palette,
        owner.clone(),
        move |owner, cx| {
            let key = bad_key.clone();
            owner.update(cx, |this, cx| {
                if this.reply_feedback.get(&key) == Some(&-1) {
                    this.reply_feedback.remove(&key);
                } else {
                    this.reply_feedback.insert(key, -1);
                }
                cx.notify();
            });
        },
    );
    let branch = reply_action_button(
        format!("reply-branch-{index}"),
        "git-branch",
        locale.text("reply.branch").to_string(),
        false,
        palette,
        owner.clone(),
        move |owner, cx| {
            owner.update(cx, |this, cx| this.fork_reply(anchor, cx));
        },
    );

    let mut footer = div()
        .id(("reply-actions", index))
        .role(Role::Toolbar)
        .aria_label(locale.text("reply.actions"))
        .relative()
        .mt(px(7.))
        .h(px(30.))
        .flex()
        .items_center()
        .gap(px(2.))
        .text_size(px(11.))
        .text_color(palette.faint)
        .child(copy)
        .child(good)
        .child(bad)
        .child(branch);
    if !hooks.is_empty() {
        let hook_key = key.clone();
        footer = footer.child(reply_action_button(
            format!("reply-hooks-{index}"),
            "anchor",
            locale.text("reply.hooks").to_string(),
            popover == Some(ReplyPopoverKind::Hooks),
            palette,
            owner.clone(),
            move |owner, cx| {
                let key = hook_key.clone();
                owner.update(cx, |this, cx| {
                    let next = (key, ReplyPopoverKind::Hooks);
                    this.reply_popover =
                        (this.reply_popover.as_ref() != Some(&next)).then_some(next);
                    cx.notify();
                });
            },
        ));
    }
    if !memory_notes.is_empty() {
        let memory_key = key.clone();
        footer = footer.child(reply_action_button(
            format!("reply-memories-{index}"),
            "notebook-pen",
            locale.text("reply.memories").to_string(),
            popover == Some(ReplyPopoverKind::Memories),
            palette,
            owner.clone(),
            move |owner, cx| {
                let key = memory_key.clone();
                owner.update(cx, |this, cx| {
                    let next = (key, ReplyPopoverKind::Memories);
                    this.reply_popover =
                        (this.reply_popover.as_ref() != Some(&next)).then_some(next);
                    cx.notify();
                });
            },
        ));
    }
    if show_time && let Some(time) = message_time(block) {
        footer = footer.child(div().ml_2().text_color(palette.faint).child(time));
    }
    if let Some(kind) = popover {
        footer = footer.child(reply_metadata_popover(
            index,
            kind,
            &hooks,
            &memory_notes,
            style,
        ));
        let dismiss_owner = owner;
        footer = footer.on_mouse_down_out(move |_, _, cx| {
            dismiss_owner.update(cx, |this, cx| {
                this.reply_popover = None;
                cx.notify();
            });
            cx.stop_propagation();
        });
    }
    footer.into_any_element()
}

fn reply_action_button(
    id: String,
    icon_name: &'static str,
    label: String,
    selected: bool,
    palette: ThemePalette,
    owner: Entity<AzemWindow>,
    on_click: impl Fn(&Entity<AzemWindow>, &mut gpui::App) + 'static,
) -> gpui::AnyElement {
    let tooltip_label = label.clone();
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(true)
        .size(px(28.))
        .rounded(px(8.))
        .bg(if selected {
            palette.paper_muted
        } else {
            palette.paper
        })
        .flex()
        .items_center()
        .justify_center()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.hover))
        .on_click(move |_, _, cx| on_click(&owner, cx))
        .tooltip(move |_, cx| {
            cx.new(|_| ReplyActionTooltip {
                label: tooltip_label.clone(),
                palette,
            })
            .into()
        })
        .child(icon(
            icon_name,
            16.,
            if selected {
                palette.ink_soft
            } else {
                palette.faint
            },
        ))
        .into_any_element()
}

fn reply_key(index: usize, block: &Block) -> String {
    if !block.run_id.is_empty() {
        format!("run:{}", block.run_id)
    } else if let Some(sequence) = block
        .extra
        .get("sequence")
        .and_then(serde_json::Value::as_i64)
    {
        format!("sequence:{sequence}")
    } else {
        format!("reply:{index}")
    }
}

fn timeline_message_key(index: usize, block: &Block) -> String {
    format!("{}:{}", block.kind, reply_key(index, block))
}

fn message_time(block: &Block) -> Option<String> {
    let value = ["completedAt", "createdAt", "submittedAt"]
        .into_iter()
        .find_map(|key| {
            block
                .extra
                .get(key)
                .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
        })?;
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
    Some(local.format("%H:%M").to_string())
}

fn reply_hooks(
    hooks: &[serde_json::Value],
    catalog: &serde_json::Value,
    run_id: &str,
    locale: Locale,
) -> Vec<ReplyHookItem> {
    hooks
        .iter()
        .filter(|hook| hook.get("runId").and_then(serde_json::Value::as_str) == Some(run_id))
        .map(|hook| {
            let data = hook.get("data").unwrap_or(&serde_json::Value::Null);
            let event = data
                .get("event")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("Hook")
                .to_string();
            let origin = hook_origin(data, catalog);
            let origin = locale
                .text(if origin == "plugin" {
                    "reply.originPlugin"
                } else {
                    "reply.originUser"
                })
                .to_string();
            let detail = ["statusMessage", "reason", "stdout", "stderr", "tool"]
                .into_iter()
                .find_map(|key| {
                    data.get(key)
                        .and_then(serde_json::Value::as_str)
                        .map(str::trim)
                        .filter(|value| !value.is_empty())
                })
                .map(compact_reply_detail)
                .unwrap_or_default();
            ReplyHookItem {
                event,
                origin,
                detail,
            }
        })
        .collect()
}

fn hook_origin(data: &serde_json::Value, catalog: &serde_json::Value) -> &'static str {
    let event = data.get("event").and_then(serde_json::Value::as_str);
    let name = data.get("name").and_then(serde_json::Value::as_str);
    if let Some(origin) = catalog
        .get("commands")
        .and_then(serde_json::Value::as_array)
        .and_then(|commands| {
            commands.iter().find(|command| {
                command.get("event").and_then(serde_json::Value::as_str) == event
                    && command.get("name").and_then(serde_json::Value::as_str) == name
            })
        })
        .and_then(|command| command.get("origin"))
        .and_then(serde_json::Value::as_str)
    {
        return if origin == "plugin" { "plugin" } else { "user" };
    }
    if data
        .get("source")
        .and_then(serde_json::Value::as_str)
        .is_some_and(|source| source.contains("plugin"))
    {
        "plugin"
    } else {
        "user"
    }
}

fn compact_reply_detail(value: &str) -> String {
    let value = value.lines().next().unwrap_or_default().trim();
    let mut characters = value.chars();
    let compact = characters.by_ref().take(72).collect::<String>();
    if characters.next().is_some() {
        compact + "…"
    } else {
        compact
    }
}

fn reply_metadata_popover(
    index: usize,
    kind: ReplyPopoverKind,
    hooks: &[ReplyHookItem],
    memories: &[String],
    style: (ThemePalette, Locale, bool),
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion) = style;
    let title = locale.text(match kind {
        ReplyPopoverKind::Hooks => "reply.hooksTitle",
        ReplyPopoverKind::Memories => "reply.memoriesTitle",
    });
    let rows = match kind {
        ReplyPopoverKind::Hooks => hooks
            .iter()
            .enumerate()
            .map(|(row, hook)| {
                div()
                    .id(("reply-hook-row", index * 256 + row))
                    .flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .w(px(180.))
                            .text_color(palette.button_text)
                            .child(hook.event.clone()),
                    )
                    .child(div().flex_1().min_w_0().text_color(rgba(0xc7c9cdff)).child(
                        if hook.detail.is_empty() {
                            hook.origin.clone()
                        } else {
                            format!("{} · {}", hook.origin, hook.detail)
                        },
                    ))
                    .into_any_element()
            })
            .collect::<Vec<_>>(),
        ReplyPopoverKind::Memories => memories
            .iter()
            .enumerate()
            .map(|(row, memory)| {
                div()
                    .id(("reply-memory-row", index * 256 + row))
                    .flex()
                    .items_start()
                    .gap_2()
                    .text_color(rgba(0xc7c9cdff))
                    .child("•")
                    .child(div().flex_1().min_w_0().child(memory.clone()))
                    .into_any_element()
            })
            .collect::<Vec<_>>(),
    };
    let popover = div()
        .id(("reply-metadata-popover", index))
        .role(Role::Region)
        .aria_label(title)
        .absolute()
        .bottom(px(34.))
        .left(px(92.))
        .w(px(520.))
        .max_h(px(520.))
        .rounded(px(12.))
        .bg(palette.button)
        .text_size(px(12.))
        .line_height(px(18.))
        .shadow(vec![
            BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.12, 0.12, 0.25))
                .blur_radius(px(28.)),
        ])
        .occlude()
        .overflow_hidden()
        .flex()
        .flex_col()
        .child(
            div()
                .px_3()
                .pt_3()
                .pb_2()
                .text_color(palette.button_text)
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
        .child(
            div()
                .id(("reply-metadata-list", index))
                .px_3()
                .pb_3()
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .gap_2()
                .children(rows),
        );
    if reduced_motion {
        popover.into_any_element()
    } else {
        popover
            .with_animation(
                (
                    "reply-popover-open",
                    index * 2 + usize::from(kind == ReplyPopoverKind::Memories),
                ),
                Animation::new(Duration::from_millis(150)).with_easing(gpui::ease_out_quint()),
                |popover, progress| {
                    popover
                        .relative()
                        .top(px(5. * (1. - progress)))
                        .opacity(progress)
                },
            )
            .into_any_element()
    }
}

fn tool_group_entry(
    index: usize,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool, f32, i64),
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion, horizontal_gutter, live_elapsed_ms) = style;
    let turn = turn_process_range(blocks, index).expect("tool belongs to a turn");
    let range = tool_group_range(blocks, &turn, index);
    let step_indexes = process_step_indexes(blocks, range.clone());
    if step_indexes
        .iter()
        .copied()
        .find(|candidate| is_process_tool_block(&blocks[*candidate]))
        != Some(index)
    {
        return div().h(px(0.)).into_any_element();
    }
    let key = tool_group_key(blocks, &range, index);
    let terminal = blocks
        .get(turn.end.saturating_sub(1))
        .filter(|block| is_turn_final_output(block));
    let group = &blocks[range.clone()];
    let running = terminal.is_none()
        && range.end == turn.process_end
        && turn.end == blocks.len()
        && (live_elapsed_ms > 0 || group.iter().any(is_active_process_block));
    let expanded = expansion.borrow().is_expanded(&key, running);
    let summary = run_process_summary(group, terminal, running, live_elapsed_ms, locale);
    let processing = running.then(|| processing_status(live_elapsed_ms, locale));
    let step_count = step_indexes.len();
    let body = div()
        .relative()
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .pl(px(18.))
        .when(step_count > 1, |body| {
            body.child(
                div()
                    .absolute()
                    .left(px(8.))
                    .top(px(16.))
                    .bottom(px(16.))
                    .w(px(1.))
                    .bg(palette.border),
            )
        })
        .children(
            step_indexes
                .iter()
                .enumerate()
                .map(|(row_index, step_index)| {
                    process_step_row(
                        index,
                        row_index,
                        &blocks[*step_index],
                        (palette, locale, reduced_motion),
                    )
                }),
        );
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label("tool")
        .w_full()
        .min_w_0()
        .px(px(horizontal_gutter))
        .py(px(4.))
        .flex()
        .flex_col()
        .items_center()
        .child(turn_status_header(
            index,
            summary,
            running,
            (palette, reduced_motion),
            Some((key, expanded)),
            expansion,
            owner,
        ))
        .when(expanded, |entry| entry.child(body))
        .when_some(processing, |entry, processing| {
            entry.child(
                div()
                    .w_full()
                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                    .h(px(32.))
                    .flex()
                    .items_center()
                    .text_size(px(12.))
                    .text_color(palette.faint)
                    .child(processing),
            )
        })
        .into_any_element()
}

pub(super) fn needs_pending_process(blocks: &[Block], running: bool) -> bool {
    running
        && blocks
            .last()
            .is_some_and(|block| block.kind.as_ref() == "user")
}

fn pending_process_entry(
    index: usize,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    horizontal_gutter: f32,
    live_elapsed_ms: i64,
) -> gpui::AnyElement {
    let summary = processing_status(live_elapsed_ms, locale);
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(summary.clone())
        .w_full()
        .px(px(horizontal_gutter))
        .py(px(8.))
        .flex()
        .justify_center()
        .child(
            div()
                .id(("process-group", index))
                .role(Role::Status)
                .aria_label(summary.clone())
                .w_full()
                .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                .h(px(36.))
                .flex()
                .items_center()
                .child(animated_activity_label(
                    index,
                    summary,
                    palette,
                    reduced_motion,
                )),
        )
        .into_any_element()
}

fn thinking_process_entry(
    index: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    horizontal_gutter: f32,
    live_elapsed_ms: i64,
) -> gpui::AnyElement {
    let active = is_active_process_block(block);
    let elapsed_ms = process_number(block, "elapsedMs").unwrap_or(live_elapsed_ms);
    let label = if active && elapsed_ms > 0 {
        format!(
            "{} · {}",
            locale.text("ui.thinking"),
            format_usage_duration(elapsed_ms, locale)
        )
    } else if elapsed_ms > 0 {
        locale.format(
            "process.thinkingDuration",
            &[("duration", format_usage_duration(elapsed_ms, locale))],
        )
    } else {
        locale.text("ui.thinking").to_string()
    };
    let activity = if active {
        animated_activity_label(index, label.clone(), palette, reduced_motion)
    } else {
        div()
            .min_w_0()
            .truncate()
            .text_size(px(12.))
            .font_weight(gpui::FontWeight::MEDIUM)
            .text_color(palette.faint)
            .child(label.clone())
            .into_any_element()
    };
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(label)
        .w_full()
        .px(px(horizontal_gutter))
        .py(px(4.))
        .flex()
        .justify_center()
        .child(
            div()
                .w_full()
                .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                .flex()
                .flex_col()
                .child(
                    div()
                        .h(px(36.))
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("lightbulb", 14., palette.faint))
                        .child(activity),
                )
                .when(active, |entry| {
                    entry.child(
                        div()
                            .h(px(32.))
                            .flex()
                            .items_center()
                            .text_size(px(12.))
                            .text_color(palette.faint)
                            .child(processing_status(live_elapsed_ms, locale)),
                    )
                }),
        )
        .into_any_element()
}

struct TurnProcessRange {
    start: usize,
    end: usize,
    process_end: usize,
}

fn turn_process_range(blocks: &[Block], index: usize) -> Option<TurnProcessRange> {
    blocks
        .get(index)
        .filter(|block| block.kind.as_ref() != "user")?;
    let start = blocks[..index]
        .iter()
        .rposition(|block| block.kind.as_ref() == "user")
        .map_or(0, |user| user + 1);
    let end = blocks[index..]
        .iter()
        .position(|block| block.kind.as_ref() == "user")
        .map_or(blocks.len(), |offset| index + offset);
    let terminal = blocks
        .get(end.saturating_sub(1))
        .filter(|block| is_turn_final_output(block));
    let process_end = end - usize::from(terminal.is_some());
    Some(TurnProcessRange {
        start,
        end,
        process_end,
    })
}

fn tool_group_range(
    blocks: &[Block],
    turn: &TurnProcessRange,
    index: usize,
) -> std::ops::Range<usize> {
    let start = (turn.start..index)
        .rev()
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .map_or(turn.start, |boundary| boundary + 1);
    let end = (index + 1..turn.process_end)
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .unwrap_or(turn.process_end);
    start..end
}

fn thinking_belongs_to_tool_group(blocks: &[Block], index: usize) -> bool {
    let Some(turn) = turn_process_range(blocks, index) else {
        return false;
    };
    let start = (turn.start..index)
        .rev()
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .map_or(turn.start, |boundary| boundary + 1);
    let end = (index + 1..turn.process_end)
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .unwrap_or(turn.process_end);
    blocks[start..end].iter().any(is_process_tool_block)
}

fn is_tool_group_boundary(block: &Block) -> bool {
    !is_process_tool_block(block) && !is_hidden_process_block(block)
}

fn tool_group_key(blocks: &[Block], range: &std::ops::Range<usize>, index: usize) -> String {
    blocks[range.clone()]
        .iter()
        .find_map(|block| (!block.run_id.is_empty()).then_some(block.run_id.as_ref()))
        .map_or_else(
            || format!("tool-group:{index}"),
            |run_id| format!("tool-group:{run_id}:{index}"),
        )
}

fn process_step_indexes(blocks: &[Block], range: std::ops::Range<usize>) -> Vec<usize> {
    let mut seen_tool_calls = HashSet::new();
    range
        .filter(|index| {
            let block = &blocks[*index];
            if is_thinking_text(block) {
                return true;
            }
            if !is_process_tool_block(block) {
                return false;
            }
            let tool_call_id = block.tool_call_id.as_ref();
            tool_call_id.is_empty() || seen_tool_calls.insert(tool_call_id)
        })
        .collect()
}

fn turn_status_header(
    index: usize,
    summary: String,
    running: bool,
    style: (ThemePalette, bool),
    toggle: Option<(String, bool)>,
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let header = div()
        .id(("turn-status", index))
        .aria_label(summary.clone())
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .child(turn_status_row(
            index,
            summary,
            running,
            toggle.as_ref().map(|(_, expanded)| *expanded),
            style,
        ));
    let Some((toggle_key, expanded)) = toggle else {
        return header.role(Role::Status).into_any_element();
    };
    header
        .role(Role::Button)
        .aria_expanded(expanded)
        .tab_stop(true)
        .cursor_pointer()
        .on_click(move |_, _, cx| {
            expansion.borrow_mut().toggle(&toggle_key, running);
            owner.update(cx, |this, cx| {
                this.refresh_transcript_layout(index);
                cx.notify();
            });
        })
        .into_any_element()
}

fn turn_status_row(
    index: usize,
    summary: String,
    running: bool,
    expanded: Option<bool>,
    style: (ThemePalette, bool),
) -> gpui::AnyElement {
    let (palette, reduced_motion) = style;
    let label = if running {
        animated_activity_label(index, summary, palette, reduced_motion)
    } else {
        div()
            .min_w_0()
            .truncate()
            .text_size(px(12.))
            .font_weight(gpui::FontWeight::MEDIUM)
            .text_color(palette.faint)
            .child(summary)
            .into_any_element()
    };
    div()
        .min_w_0()
        .h(px(36.))
        .flex()
        .items_center()
        .gap_1()
        .child(div().min_w_0().flex().items_center().child(label))
        .children(expanded.map(|expanded| {
            div().flex_shrink_0().child(icon(
                if expanded {
                    "chevron-down"
                } else {
                    "chevron-right"
                },
                13.,
                palette.faint,
            ))
        }))
        .into_any_element()
}

fn is_hidden_process_block(block: &Block) -> bool {
    is_thinking_text(block)
        || is_host_tool_announcement(block)
        || (block.kind.as_ref() == "status" && block.title.as_ref() == "run_cancelled")
}

fn is_turn_final_output(block: &Block) -> bool {
    block.kind.as_ref() == "error"
        || (block.kind.as_ref() == "assistant" && block.text_phase.as_ref() != "commentary")
}

fn is_thinking_text(block: &Block) -> bool {
    block.kind.as_ref() == "thinking"
}

fn thinking_wave_opacity(delta: f32, character_index: usize, character_count: usize) -> f32 {
    let center = delta.clamp(0., 1.) * (character_count as f32 + 2.) - 1.;
    let highlight = (1. - (character_index as f32 - center).abs()).clamp(0., 1.);
    0.42 + 0.58 * highlight
}

fn is_active_process_block(block: &Block) -> bool {
    matches!(
        block.state.as_ref(),
        "running"
            | "queued"
            | "pending"
            | "streaming"
            | "started"
            | "arguments"
            | "progress"
            | "awaiting_approval"
            | "reviewing_approval"
    )
}

fn running_tool_summary(block: &Block, locale: Locale) -> String {
    let (action, _) = tool_action(block.title.as_ref(), locale);
    let preview = truncate_label(&tool_preview(block), 42);
    let prefix = locale.text("ui.running");
    if preview.is_empty() {
        format!("{prefix} {action}")
    } else {
        format!("{prefix} {action} \"{preview}\"")
    }
}

fn animated_activity_label(
    animation_id: usize,
    label: String,
    palette: ThemePalette,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let character_count = label.chars().count();
    div()
        .min_w_0()
        .h(px(17.))
        .overflow_hidden()
        .flex()
        .items_center()
        .text_size(px(12.))
        .font_weight(gpui::FontWeight::MEDIUM)
        .text_color(palette.faint)
        .when(reduced_motion, |row| row.child(label.clone()))
        .when(!reduced_motion, |row| {
            row.children(
                label
                    .chars()
                    .enumerate()
                    .map(move |(character_index, character)| {
                        div().child(character.to_string()).with_animation(
                            ("activity-wave", animation_id * 64 + character_index),
                            Animation::new(Duration::from_millis(1_500)).repeat(),
                            move |character, delta| {
                                character.opacity(thinking_wave_opacity(
                                    delta,
                                    character_index,
                                    character_count,
                                ))
                            },
                        )
                    }),
            )
        })
        .into_any_element()
}

fn is_host_tool_announcement(block: &Block) -> bool {
    block_data_value(block, "synthetic").and_then(serde_json::Value::as_str)
        == Some("tool_announcement")
        || block.content.trim() == "正在调用所需工具，并根据实际结果继续。"
}

fn run_process_summary(
    group: &[Block],
    terminal: Option<&Block>,
    running: bool,
    _live_elapsed_ms: i64,
    locale: Locale,
) -> String {
    if running {
        return completed_tool_group_summary(group, locale)
            .or_else(|| latest_process_activity(group, locale))
            .unwrap_or_else(|| locale.text("ui.thinking").to_string());
    }
    let cancelled = group.iter().chain(terminal).any(|block| {
        block.kind.as_ref() == "status"
            && block.title.as_ref() == "run_cancelled"
            && block.state.as_ref() == "cancelled"
    });
    let run_elapsed_ms = group
        .iter()
        .chain(terminal)
        .find_map(|block| process_number(block, "runElapsedMs"));
    let started_at = group
        .iter()
        .chain(terminal)
        .filter_map(|block| process_number(block, "startedAt"))
        .min();
    let completed_at = group
        .iter()
        .chain(terminal)
        .filter_map(|block| process_number(block, "completedAt"))
        .max();
    let elapsed_ms = run_elapsed_ms.unwrap_or_else(|| {
        terminal
            .and_then(|block| process_number(block, "elapsedMs"))
            .or_else(|| {
                started_at
                    .zip(completed_at)
                    .and_then(|(started, completed)| {
                        (completed >= started).then_some(completed - started)
                    })
            })
            .filter(|elapsed| *elapsed > 0)
            .unwrap_or_else(|| {
                group
                    .iter()
                    .filter_map(|block| process_number(block, "elapsedMs"))
                    .sum()
            })
    });
    let seconds = (elapsed_ms / 1000).max(0);
    let minutes = seconds / 60;
    let remainder = seconds % 60;
    if cancelled {
        return locale.format(
            if minutes > 0 {
                "duration.stoppedMinutes"
            } else {
                "duration.stoppedSeconds"
            },
            &[
                ("minutes", minutes.to_string()),
                ("seconds", remainder.to_string()),
            ],
        );
    }
    if let Some(summary) = completed_tool_group_summary(group, locale) {
        return summary;
    }
    if seconds == 0 {
        return locale.text("ui.worked").to_string();
    }
    locale.format(
        if minutes > 0 {
            "duration.processedMinutes"
        } else {
            "duration.processedSeconds"
        },
        &[
            ("minutes", minutes.to_string()),
            ("seconds", remainder.to_string()),
        ],
    )
}

fn processing_status(elapsed_ms: i64, locale: Locale) -> String {
    let seconds = (elapsed_ms / 1000).max(0);
    let minutes = seconds / 60;
    let remainder = seconds % 60;
    locale.format(
        if minutes > 0 {
            "duration.processingMinutes"
        } else {
            "duration.processingSeconds"
        },
        &[
            ("minutes", minutes.to_string()),
            ("seconds", remainder.to_string()),
        ],
    )
}

#[derive(Default)]
struct ProcessActivityCounts {
    thinking: usize,
    read: usize,
    edit: usize,
    command: usize,
    search: usize,
    web_search: usize,
    fetch: usize,
    subagent: usize,
    browser: usize,
    plan: usize,
    other: usize,
}

fn process_activity_counts(group: &[Block]) -> ProcessActivityCounts {
    let mut counts = ProcessActivityCounts::default();
    let mut seen_tool_calls = HashSet::new();
    for block in group {
        if is_thinking_text(block) {
            counts.thinking += 1;
            continue;
        }
        if !is_process_tool_block(block) {
            continue;
        }
        let tool_call_id = block.tool_call_id.as_ref();
        if !tool_call_id.is_empty() && !seen_tool_calls.insert(tool_call_id) {
            continue;
        }
        match tool_activity_kind(block) {
            ToolActivityKind::Read => counts.read += 1,
            ToolActivityKind::Edit => counts.edit += 1,
            ToolActivityKind::Command => counts.command += 1,
            ToolActivityKind::Search => counts.search += 1,
            ToolActivityKind::WebSearch => counts.web_search += 1,
            ToolActivityKind::Fetch => counts.fetch += 1,
            ToolActivityKind::Subagent => counts.subagent += 1,
            ToolActivityKind::Browser => counts.browser += 1,
            ToolActivityKind::Plan => counts.plan += 1,
            ToolActivityKind::Other => counts.other += 1,
        }
    }
    counts
}

fn completed_tool_group_summary(group: &[Block], locale: Locale) -> Option<String> {
    let counts = process_activity_counts(group);
    let thinking = (counts.thinking > 0).then(|| {
        locale.format(
            "tools.thinkingCount",
            &[("count", counts.thinking.to_string())],
        )
    });
    let mut tools = Vec::with_capacity(10);
    for (count, key) in [
        (counts.read, "tools.readCount"),
        (counts.edit, "tools.editCount"),
        (counts.command, "tools.commandCount"),
        (counts.search, "tools.searchCount"),
        (counts.web_search, "tools.webSearchCount"),
        (counts.fetch, "tools.fetchCount"),
        (counts.subagent, "tools.subagentCount"),
        (counts.browser, "tools.browserCount"),
        (counts.plan, "tools.planCount"),
        (counts.other, "tools.otherCount"),
    ] {
        if count > 0 {
            tools.push(locale.format(key, &[("count", count.to_string())]));
        }
    }
    let separator = if locale.id().starts_with("zh") {
        "、"
    } else {
        ", "
    };
    match (thinking, tools.is_empty()) {
        (Some(thinking), false) => Some(format!("{thinking} · {}", tools.join(separator))),
        (_, false) => Some(tools.join(separator)),
        (_, true) => None,
    }
}

fn latest_process_activity(group: &[Block], locale: Locale) -> Option<String> {
    group.iter().rev().find_map(|block| {
        if is_host_tool_announcement(block) {
            return None;
        }
        if is_process_tool_block(block) {
            return Some(process_step_label(block, locale));
        }
        if matches!(block.kind.as_ref(), "thinking" | "commentary") {
            return process_text_preview(&block.content);
        }
        None
    })
}

fn process_text_preview(content: &str) -> Option<String> {
    content
        .lines()
        .rev()
        .map(str::trim)
        .find(|line| !line.is_empty())
        .map(|line| line.trim_matches(['*', '#', '`', ' ']))
        .filter(|line| !line.is_empty())
        .map(|line| truncate_label(line, 72))
}

fn is_agent_block(block: &Block) -> bool {
    block.kind.as_ref() == "agent"
}

#[derive(Clone)]
struct SubagentCardItem {
    id: String,
    role: String,
    detail: String,
    state: String,
    elapsed_ms: i64,
}

fn agent_belongs_to_run(agent: &serde_json::Value, run_id: &str) -> bool {
    ["parentRunId", "parentRunID", "parent_run_id"]
        .into_iter()
        .any(|key| agent.get(key).and_then(serde_json::Value::as_str) == Some(run_id))
}

fn subagent_run_card(
    group_index: usize,
    group: &[Block],
    process_key: &str,
    agents: &[serde_json::Value],
    style: (ThemePalette, Locale),
    _expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale) = style;
    let snapshots = agents
        .iter()
        .filter(|agent| agent_belongs_to_run(agent, process_key))
        .collect::<Vec<_>>();
    let items = if snapshots.is_empty() {
        group
            .iter()
            .filter(|block| is_agent_block(block))
            .map(|block| {
                let id = block
                    .extra
                    .get("agentId")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                let agent = agents
                    .iter()
                    .find(|agent| agent.get("id").and_then(serde_json::Value::as_str) == Some(&id));
                let field = |key| {
                    agent
                        .and_then(|agent| agent.get(key))
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .trim()
                };
                let role = [field("type"), block.title.as_ref(), id.as_str()]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or(locale.text("ui.subagent"))
                    .to_string();
                let detail = [field("description"), field("summary"), block.content.trim()]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or_default()
                    .to_string();
                let state = [field("state"), block.state.as_ref()]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or("idle");
                let elapsed_ms = agent
                    .and_then(|agent| agent.get("elapsedMs"))
                    .and_then(json_integer)
                    .or_else(|| process_number(block, "elapsedMs"))
                    .unwrap_or_default();
                SubagentCardItem {
                    id,
                    role: truncate_label(&role, 28),
                    detail: truncate_label(&detail, 72),
                    state: state.to_string(),
                    elapsed_ms,
                }
            })
            .collect::<Vec<_>>()
    } else {
        snapshots
            .into_iter()
            .map(|agent| {
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
                    .unwrap_or_default();
                let role = [field("type"), id]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or(locale.text("ui.subagent"));
                let detail = [field("description"), field("summary"), field("activity")]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or_default();
                let state = field("state");
                let state = if state.is_empty() { "idle" } else { state };
                SubagentCardItem {
                    id: id.to_string(),
                    role: truncate_label(role, 28),
                    detail: truncate_label(detail, 72),
                    state: state.to_string(),
                    elapsed_ms: agent
                        .get("elapsedMs")
                        .and_then(json_integer)
                        .unwrap_or_default(),
                }
            })
            .collect::<Vec<_>>()
    };
    div()
        .id(("subagent-run-card", group_index))
        .role(Role::Group)
        .aria_label(locale.text("ui.subagents"))
        .w_full()
        .my_1()
        .flex()
        .flex_col()
        .children(items.into_iter().enumerate().map(|(index, item)| {
            let target = item.id.clone();
            let click_owner = owner.clone();
            let state_label = subagent_status_label(&item.state, locale).to_string();
            let detail = if item.detail.is_empty() {
                state_label.clone()
            } else {
                format!("{state_label}: {}", item.detail)
            };
            let aria_label = format!("{}, {detail}", item.role);
            let state_color = if item.state == "failed" {
                palette.danger
            } else if is_active_agent_state(&item.state) {
                palette.ink_soft
            } else {
                palette.muted
            };
            div()
                .id(("subagent-run-row", group_index * 100 + index))
                .role(Role::Button)
                .aria_label(aria_label)
                .tab_stop(true)
                .h(px(34.))
                .min_w_0()
                .rounded(px(6.))
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .hover(move |row| row.bg(palette.hover))
                .on_click(move |_, window, cx| {
                    let target = target.clone();
                    click_owner.update(cx, |this, cx| {
                        this.inspect_agent(target, window, cx);
                    });
                })
                .child(icon("bot", 14., palette.faint))
                .child(
                    div()
                        .flex_shrink_0()
                        .px_1()
                        .py(px(2.))
                        .rounded(px(5.))
                        .bg(palette.paper_muted)
                        .text_size(px(12.))
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .text_color(palette.ink_soft)
                        .child(item.role),
                )
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_size(px(12.))
                        .text_color(state_color)
                        .child(detail),
                )
                .when(item.elapsed_ms > 0, |row| {
                    row.child(
                        div()
                            .flex_shrink_0()
                            .text_size(px(12.))
                            .text_color(palette.faint)
                            .child(format!(
                                "· {}",
                                format_usage_duration(item.elapsed_ms, locale)
                            )),
                    )
                })
                .child(
                    div()
                        .flex_shrink_0()
                        .child(icon("chevron-right", 13., palette.faint)),
                )
        }))
        .into_any_element()
}

fn is_active_agent_state(state: &str) -> bool {
    matches!(state, "initializing" | "started" | "running" | "cancelling")
}

fn is_open_agent_state(state: &str) -> bool {
    is_active_agent_state(state) || matches!(state, "queued" | "pending")
}

fn subagent_status_label(state: &str, locale: Locale) -> &'static str {
    match state {
        "initializing" => locale.text("ui.starting"),
        "started" | "running" => locale.text("ui.running2"),
        "cancelling" => locale.text("ui.stopping"),
        "completed" => locale.text("ui.completed"),
        "failed" => locale.text("ui.failed2"),
        "cancelled" | "canceled" => locale.text("ui.cancelled"),
        "interrupted" => locale.text("ui.interrupted"),
        "queued" => locale.text("ui.queued"),
        _ => locale.text("ui.idle"),
    }
}

fn is_process_tool_block(block: &Block) -> bool {
    matches!(block.kind.as_ref(), "tool" | "diff") && !is_host_tool_announcement(block)
}

fn process_step_row(
    group_index: usize,
    row_index: usize,
    block: &Block,
    style: (ThemePalette, Locale, bool),
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion) = style;
    let row_id = group_index * 1000 + row_index;
    let active = is_active_process_block(block);
    let failed = block.state.as_ref() == "failed";
    let (row_icon, action, target) = process_step_presentation(block, locale);
    let label = if target.is_empty() {
        action.clone()
    } else {
        format!("{action} {target}")
    };
    let has_target = !target.is_empty();
    let color = if failed {
        palette.danger
    } else if active {
        palette.ink_soft
    } else {
        palette.muted
    };
    let row = div()
        .id(("process-step", row_id))
        .role(Role::Status)
        .aria_label(label)
        .h(px(32.))
        .min_w_0()
        .flex()
        .items_center()
        .gap_2()
        .text_size(px(12.))
        .child(icon(
            row_icon,
            14.,
            if failed {
                palette.danger
            } else {
                palette.faint
            },
        ))
        .child(
            div()
                .min_w_0()
                .flex()
                .items_center()
                .gap_1()
                .child(
                    div()
                        .flex_shrink_0()
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .text_color(color)
                        .child(action),
                )
                .when(has_target, |detail| {
                    detail.child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_color(if failed {
                                palette.danger
                            } else {
                                palette.faint
                            })
                            .child(target),
                    )
                }),
        );
    if reduced_motion {
        row.into_any_element()
    } else {
        row.with_animation(
            ("process-step-enter", row_id),
            Animation::new(Duration::from_millis(160)).with_easing(gpui::ease_out_quint()),
            |row, progress| {
                row.relative()
                    .top(px(4. * (1. - progress)))
                    .opacity(progress)
            },
        )
        .into_any_element()
    }
}

fn process_step_presentation(block: &Block, locale: Locale) -> (&'static str, String, String) {
    if is_thinking_text(block) {
        let action = process_number(block, "elapsedMs")
            .filter(|elapsed| *elapsed > 0)
            .map(|elapsed| {
                locale.format(
                    "process.thinkingDuration",
                    &[("duration", format_usage_duration(elapsed, locale))],
                )
            })
            .unwrap_or_else(|| locale.text("ui.thinking").to_string());
        return ("lightbulb", action, String::new());
    }
    let kind = tool_activity_kind(block);
    let mut target = truncate_label(&tool_preview(block), 52);
    if kind == ToolActivityKind::Edit
        && let Some((additions, deletions)) = tool_file_change_counts(block)
    {
        target.push_str(&format!("  +{additions} −{deletions}"));
    }
    (kind.icon(), kind.action(locale).to_string(), target)
}

fn process_step_label(block: &Block, locale: Locale) -> String {
    if is_active_process_block(block) {
        return running_tool_summary(block, locale);
    }
    let preview = truncate_label(&tool_preview(block), 52);
    let target = if preview.is_empty() {
        tool_action(block.title.as_ref(), locale).0.to_string()
    } else {
        preview
    };
    let name = block.title.as_ref();
    if is_edit_tool(block) {
        let mut label = locale.format("process.editedFile", &[("target", target)]);
        if let Some((additions, deletions)) = tool_file_change_counts(block) {
            label.push_str(&format!(" +{additions} -{deletions}"));
        }
        return label;
    }
    if is_read_tool(name) {
        return locale.format("process.readFile", &[("target", target)]);
    }
    if is_shell_tool(name) {
        if let Some(elapsed_ms) = process_number(block, "elapsedMs").filter(|value| *value > 0) {
            return locale.format(
                "process.ranCommandIn",
                &[
                    ("target", target),
                    ("duration", format_usage_duration(elapsed_ms, locale)),
                ],
            );
        }
        return locale.format("process.ranCommand", &[("target", target)]);
    }
    locale.format("process.usedTool", &[("target", target)])
}

fn tool_file_change_counts(block: &Block) -> Option<(i64, i64)> {
    let summary = block_data_value(block, "fileChange").and_then(|value| match value {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    });
    let file = summary
        .as_ref()
        .and_then(|value| value.get("files"))
        .and_then(serde_json::Value::as_array)
        .and_then(|files| files.first());
    let additions = file
        .and_then(|value| value.get("additions"))
        .or_else(|| summary.as_ref().and_then(|value| value.get("additions")))
        .and_then(serde_json::Value::as_i64)
        .or_else(|| process_number(block, "additions"))
        .unwrap_or_default();
    let deletions = file
        .and_then(|value| value.get("deletions"))
        .or_else(|| summary.as_ref().and_then(|value| value.get("deletions")))
        .and_then(serde_json::Value::as_i64)
        .or_else(|| process_number(block, "deletions"))
        .unwrap_or_default();
    (additions > 0 || deletions > 0).then_some((additions, deletions))
}

fn process_detail_row(
    group_index: usize,
    row_index: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let row_id = group_index * 1000 + row_index;
    let active = is_active_process_block(block);
    if block.kind.as_ref() == "context_compaction" {
        let label = locale.text("ui.compactingContextAutomatically");
        return div()
            .id(("process-compaction", row_id))
            .role(Role::Status)
            .aria_label(label)
            .min_h(px(34.))
            .flex()
            .items_center()
            .gap_2()
            .text_size(px(12.))
            .text_color(palette.faint)
            .child(icon("file-text", 15., palette.faint))
            .when(active, |row| {
                row.child(animated_activity_label(
                    row_id,
                    label.to_string(),
                    palette,
                    reduced_motion,
                ))
            })
            .when(!active, |row| row.child(label))
            .into_any_element();
    }
    if is_file_change(block) {
        return file_change_card(row_id, block, palette, locale);
    }
    div()
        .id(("process-commentary", row_id))
        .py_1()
        .text_color(if is_thinking_text(block) {
            palette.faint
        } else {
            palette.ink
        })
        .text_size(px(palette.chat_font_size))
        .line_height(px(palette.chat_font_size * 1.6))
        .child(markdown_view(
            row_id,
            block.content.as_ref(),
            active,
            reduced_motion,
            palette,
        ))
        .into_any_element()
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ToolActivityKind {
    Read,
    Edit,
    Command,
    Search,
    WebSearch,
    Fetch,
    Subagent,
    Browser,
    Plan,
    Other,
}

impl ToolActivityKind {
    fn action(self, locale: Locale) -> &'static str {
        locale.text(match self {
            Self::Read => "ui.readFile",
            Self::Edit => "ui.editFile",
            Self::Command => "ui.ranCommand",
            Self::Search => "ui.searchedCode",
            Self::WebSearch => "ui.search",
            Self::Fetch => "ui.fetchPage",
            Self::Subagent => "ui.subagent",
            Self::Browser => "ui.browser",
            Self::Plan => "ui.updatedPlan",
            Self::Other => "ui.toolCall",
        })
    }

    fn icon(self) -> &'static str {
        match self {
            Self::Read => "file-code",
            Self::Edit => "pencil",
            Self::Command => "terminal",
            Self::Search => "search",
            Self::WebSearch => "globe",
            Self::Fetch => "link",
            Self::Subagent => "bot",
            Self::Browser => "monitor",
            Self::Plan => "list-todo",
            Self::Other => "wrench",
        }
    }
}

fn tool_activity_kind(block: &Block) -> ToolActivityKind {
    if block.title.contains("gofmt") {
        return if is_edit_tool(block) {
            ToolActivityKind::Edit
        } else {
            ToolActivityKind::Other
        };
    }
    if is_edit_tool(block) {
        ToolActivityKind::Edit
    } else {
        tool_activity_kind_from_name(block.title.as_ref())
    }
}

fn tool_activity_kind_from_name(name: &str) -> ToolActivityKind {
    let normalized = name.to_ascii_lowercase();
    if normalized.contains("read_file") || normalized.contains("read-file") {
        ToolActivityKind::Read
    } else if [
        "edit",
        "write",
        "replace",
        "apply_patch",
        "apply-patch",
        "delete_file",
        "gofmt",
    ]
    .iter()
    .any(|part| normalized.contains(part))
    {
        ToolActivityKind::Edit
    } else if normalized.contains("shell")
        || normalized.contains("command")
        || normalized.ends_with(".exec")
    {
        ToolActivityKind::Command
    } else if normalized.contains("web_search")
        || normalized.contains("web.search")
        || normalized.contains("search_web")
    {
        ToolActivityKind::WebSearch
    } else if normalized.contains("fetch")
        || normalized.contains("read_url")
        || normalized.contains("read-url")
    {
        ToolActivityKind::Fetch
    } else if normalized.contains("browser")
        || normalized.contains("computer")
        || normalized.contains("chrome")
        || normalized.contains("screenshot")
    {
        ToolActivityKind::Browser
    } else if normalized.contains("subagent") || normalized.ends_with("agent") {
        ToolActivityKind::Subagent
    } else if normalized.contains("search")
        || normalized.contains("grep")
        || normalized.contains("glob")
        || normalized.ends_with(".rg")
    {
        ToolActivityKind::Search
    } else if normalized == "todo" || normalized.ends_with(".todo") || normalized.contains("plan") {
        ToolActivityKind::Plan
    } else {
        ToolActivityKind::Other
    }
}

fn tool_action(name: &str, locale: Locale) -> (&'static str, &'static str) {
    let kind = tool_activity_kind_from_name(name);
    (kind.action(locale), kind.icon())
}

fn is_read_tool(name: &str) -> bool {
    tool_activity_kind_from_name(name) == ToolActivityKind::Read
}

fn is_shell_tool(name: &str) -> bool {
    tool_activity_kind_from_name(name) == ToolActivityKind::Command
}

fn is_edit_tool(block: &Block) -> bool {
    let normalized = block.title.to_ascii_lowercase();
    if normalized.contains("gofmt") {
        return formatter_changed(block)
            .unwrap_or_else(|| block_data_value(block, "fileChange").is_some());
    }
    is_file_change(block)
        || tool_activity_kind_from_name(block.title.as_ref()) == ToolActivityKind::Edit
}

fn formatter_changed(block: &Block) -> Option<bool> {
    if let Some(changed) = block_data_value(block, "changed").and_then(json_boolean) {
        return Some(changed);
    }
    let structured = block_data_value(block, "structured")?;
    match structured {
        serde_json::Value::Object(fields) => fields.get("changed").and_then(json_boolean),
        serde_json::Value::String(value) => serde_json::from_str::<serde_json::Value>(value)
            .ok()
            .and_then(|value| value.get("changed").and_then(json_boolean)),
        _ => None,
    }
}

fn tool_preview(block: &Block) -> String {
    if let Some(path) = block_data_value(block, "fileChange")
        .and_then(|value| match value {
            serde_json::Value::String(value) => serde_json::from_str(value).ok(),
            value => Some(value.clone()),
        })
        .and_then(|value: serde_json::Value| value.pointer("/files/0/path").cloned())
        .and_then(|value| value.as_str().map(str::to_string))
    {
        return path;
    }
    if block.kind.as_ref() == "diff" && !block.title.trim().is_empty() {
        return block.title.to_string();
    }
    let value = block_data_value(block, "arguments");
    let decoded = value.and_then(|value| match value {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    });
    let preview = decoded
        .as_ref()
        .and_then(serde_json::Value::as_object)
        .and_then(|arguments| {
            [
                "query", "pattern", "path", "command", "cmd", "goal", "url", "task", "prompt",
            ]
            .iter()
            .find_map(|key| arguments.get(*key))
        })
        .and_then(|value| value.as_str().map(str::to_string))
        .or_else(|| {
            block
                .content
                .lines()
                .find(|line| !line.trim().is_empty())
                .map(str::trim)
                .map(str::to_string)
        })
        .unwrap_or_default();
    truncate_label(&preview, 96)
}

fn file_change_card(
    row_id: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let summary = block_data_value(block, "fileChange").and_then(|value| match value {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    });
    let files = summary
        .as_ref()
        .and_then(|value| value.get("files"))
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let additions = summary
        .as_ref()
        .and_then(|value| value.get("additions"))
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_else(|| process_number(block, "additions").unwrap_or_default());
    let deletions = summary
        .as_ref()
        .and_then(|value| value.get("deletions"))
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_else(|| process_number(block, "deletions").unwrap_or_default());
    let file_count = files.len().max(1);
    div()
        .id(("file-change-card", row_id))
        .w_full()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .overflow_hidden()
        .child(
            div()
                .min_h(px(58.))
                .px_3()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .size(px(38.))
                        .rounded(px(10.))
                        .bg(palette.paper_muted)
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(icon("file-diff", 18., palette.muted)),
                )
                .child(
                    div()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_size(px(13.))
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .text_color(palette.ink)
                                .child(locale.format(
                                    "ui.editedFileCountFiles",
                                    &[("file_count", (file_count).to_string())],
                                )),
                        )
                        .child(
                            div()
                                .text_size(px(11.))
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
                        ),
                ),
        )
        .when(!files.is_empty(), |card| {
            card.child(
                div()
                    .border_t_1()
                    .border_color(palette.border)
                    .flex()
                    .flex_col()
                    .children(files.into_iter().enumerate().map(|(index, file)| {
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
                            .id(("file-change-row", row_id * 100 + index))
                            .min_h(px(38.))
                            .px_3()
                            .flex()
                            .items_center()
                            .gap_2()
                            .text_size(px(12.))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(palette.muted)
                                    .child(path),
                            )
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
                    })),
            )
        })
        .into_any_element()
}

fn block_data_value<'a>(block: &'a Block, key: &str) -> Option<&'a serde_json::Value> {
    block
        .extra
        .get(key)
        .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
}

fn json_integer(value: &serde_json::Value) -> Option<i64> {
    value
        .as_i64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn json_boolean(value: &serde_json::Value) -> Option<bool> {
    value
        .as_bool()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn truncate_label(value: &str, limit: usize) -> String {
    let mut chars = value.chars();
    let result = chars.by_ref().take(limit).collect::<String>();
    if chars.next().is_some() {
        result + "…"
    } else {
        result
    }
}

fn process_number(block: &Block, key: &str) -> Option<i64> {
    block
        .extra
        .get(key)
        .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
        .and_then(json_integer)
}

fn is_file_change(block: &Block) -> bool {
    if block.kind.as_ref() == "diff" || block_data_value(block, "fileChange").is_some() {
        return true;
    }
    matches!(
        block.title.as_ref(),
        "coding.write_file"
            | "coding.edit"
            | "coding.edit_hashline"
            | "coding.replace"
            | "coding.delete_file"
            | "coding.gofmt"
    )
}

#[cfg(test)]
mod timeline_tests {
    use crate::localization::Locale;
    use crate::state::AppState;
    use std::{collections::HashMap, sync::Arc};

    use serde_json::json;

    #[test]
    fn zero_subscription_usage_remains_visible_without_leaking_to_other_providers() {
        assert_eq!(
            super::provider_quota_remaining(&json!({"quotaAvailable": true})),
            Some(100.)
        );
        assert_eq!(
            super::provider_quota_remaining(
                &json!({"quotaAvailable": true, "quotaUsedPercent": 42.5})
            ),
            Some(57.5)
        );
        assert_eq!(
            super::provider_quota_remaining(&json!({"quotaUsedPercent": 0})),
            None
        );
    }

    #[test]
    fn environment_snapshot_counts_live_state() {
        assert!(!super::environment_snapshot(&AppState::default()).has_subagents);

        let mut state = AppState::default();
        state.runtime.agents = vec![
            json!({"id":"running","state":"running"}),
            json!({"id":"queued","state":"queued"}),
            json!({"id":"completed","state":"completed"}),
            json!({"id":"failed","state":"failed"}),
        ];
        state.terminals.sessions = vec![
            json!({"id":"terminal-1","state":"running"}),
            json!({"id":"terminal-2","state":"closed"}),
        ];
        state.pull_requests.dashboard = json!({"current":{"title":"Live pull request"}});

        let snapshot = super::environment_snapshot(&state);
        assert!(snapshot.has_subagents);
        assert_eq!(snapshot.running_agents, 2);
        assert_eq!(snapshot.completed_agents, 2);
        assert_eq!(snapshot.running_terminals, 1);
        assert_eq!(
            snapshot.current_pull_request.as_deref(),
            Some("Live pull request")
        );
    }

    #[test]
    fn environment_hides_session_history_and_sources() {
        let environment = include_str!("surfaces.rs")
            .split("pub(super) fn environment_panel(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn side_panel(")
            .next()
            .unwrap();
        assert!(!environment.contains("locale.text(\"ui.sources\")"));
        assert!(!environment.contains("locale.text(\"ui.sessionHistory\")"));
    }

    #[test]
    fn floating_environment_stays_separate_from_the_animated_side_panel() {
        let source = include_str!("surfaces.rs")
            .split("pub(super) fn side_panel(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn agent_panel_tab(")
            .next()
            .unwrap();
        assert!(source.contains("let visible_width = this.side_panel_visible_width"));
        assert!(source.contains(".right(px(-(panel_width - visible_width)))"));
        assert!(source.contains(".id(\"side-panel-resize-handle\")"));
        assert!(source.contains(".cursor_col_resize()"));
        let environment = include_str!("surfaces.rs")
            .split("pub(super) fn environment_panel(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn side_panel(")
            .next()
            .unwrap();
        assert!(environment.contains(".right(px(right_inset))"));
        assert!(environment.contains(".max_h(px(520.))"));
        assert!(environment.contains("\"environment-panel-return\""));
        assert!(!environment.contains(".overflow_hidden()"));
        assert!(environment.contains(".w(px(312. * progress.max(0.)))"));
        assert!(environment.contains(".opacity(progress.clamp(0., 1.))"));
        assert!(!environment.contains(".left(px(-48."));
        assert!(!environment.contains(".top(px(6."));
        assert!(!source.contains(".border_l_1()"));
        assert!(source.contains(".left(px(3.))"));
    }

    #[test]
    fn environment_return_spring_has_stable_endpoints() {
        assert_eq!(super::environment_panel_return_spring(0.), 0.);
        assert_eq!(super::environment_panel_return_spring(1.), 1.);
        assert!(super::environment_panel_return_spring(0.4) > 0.9);
    }

    #[test]
    fn security_progress_uses_durable_file_and_worker_counts() {
        let files =
            json!({"filesCompleted":17,"filesTotal":100,"workersDone":4,"workersPlanned":4});
        assert!((super::security_progress_fraction(&files, "running") - 0.17).abs() < 0.001);

        let workers = json!({"workersDone":3,"workersPlanned":4});
        assert!((super::security_progress_fraction(&workers, "running") - 0.75).abs() < 0.001);
        assert_eq!(
            super::security_progress_fraction(&json!({}), "complete"),
            1.
        );
        assert_eq!(super::security_progress_fraction(&json!({}), "failed"), 0.);
    }

    #[test]
    fn security_detail_text_truncates_on_character_boundaries() {
        let value = super::security_compact_text("安全扫描进度详情", 4);
        assert_eq!(value, "安全扫描…");
        assert_eq!(super::security_compact_text("short", 20), "short");
    }

    #[test]
    fn approval_modes_keep_distinct_icons_and_localized_full_access() {
        assert_eq!(super::approval_mode_icon("prompt"), "message-square-text");
        assert_eq!(super::approval_mode_icon("auto_review"), "shield-check");
        assert_eq!(super::approval_mode_icon("yolo"), "shield-alert");
        assert_eq!(Locale::resolve("en").text("approval.yolo"), "Full Access");
        assert_eq!(Locale::resolve("zh-CN").text("approval.yolo"), "完全访问");
        let alert = include_str!("../../../assets/icons/shield-alert.svg");
        assert!(alert.contains("M12 8v4"));
        assert!(alert.contains("M12 16h.01"));
    }

    #[test]
    fn custom_picker_keys_capture_before_synthetic_clicks() {
        let source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        for handler in ["approval_picker_key", "branch_picker_key"] {
            assert!(
                source.contains(&format!(
                    ".capture_key_down(cx.listener(AzemWindow::{handler}))"
                )),
                "{handler} must consume keys before GPUI synthesizes a second click"
            );
        }
        let language = source
            .split_once("fn settings_language_control(")
            .unwrap()
            .1;
        assert!(
            language
                .split_once("fn settings_")
                .unwrap()
                .0
                .contains(".capture_key_down(")
        );
    }

    #[test]
    fn branch_menu_attaches_to_the_button_edge_in_window_coordinates() {
        let bounds = gpui::Bounds::new(
            gpui::point(gpui::px(160.), gpui::px(300.)),
            gpui::size(gpui::px(100.), gpui::px(29.)),
        );
        assert_eq!(
            super::picker_menu_position(bounds, true),
            gpui::point(gpui::px(160.), gpui::px(334.))
        );
        assert_eq!(
            super::picker_menu_position(bounds, false),
            gpui::point(gpui::px(160.), gpui::px(295.))
        );
        let source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        assert_eq!(source.matches(".position(picker_menu_position(").count(), 2);
    }

    #[test]
    fn sidebar_rows_expose_real_context_menus() {
        let source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        let sidebar = source
            .split("pub(super) fn sidebar(")
            .nth(1)
            .unwrap()
            .split("fn sidebar_context_menu_item(")
            .next()
            .unwrap();
        assert_eq!(
            sidebar.matches("MouseButton::Right,").count(),
            2,
            "project and session rows must both open a context menu"
        );
        assert!(
            sidebar.contains(".aria_selected(selected || context_selected)"),
            "the right-clicked session must remain visibly selected while its menu is open"
        );
        assert!(
            sidebar.contains(".when(session.running, |row|"),
            "running sessions must show a trailing status spinner"
        );
        assert!(
            sidebar.contains("Transformation::rotate(") && sidebar.contains("percentage(progress)"),
            "the running-session status must rotate"
        );
        assert!(
            sidebar.contains(".when(session.unread && !session.running, |row|"),
            "unread sessions must keep a trailing static indicator"
        );
        assert!(
            !sidebar.contains(".border_color(if session.running"),
            "session rows must not reserve a leading status-circle column"
        );
        let menu = source
            .split("fn sidebar_context_menu_item(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn session_rename_modal(")
            .next()
            .unwrap();
        assert!(menu.contains(".h(px(30.))"));
        assert!(menu.contains(".w(px(172.))"));
        assert!(!menu.contains(".h(px(38.))"));
        assert!(!menu.contains(".w(px(210.))"));
        assert!(!menu.contains(".w(px(244.))"));
        let actions = include_str!("main.rs");
        for action in [
            "ToggleSessionPin",
            "RenameSession",
            "MarkSessionUnread",
            "ArchiveSession",
            "CopyWorkspace",
            "CopySessionId",
        ] {
            let action = format!("SidebarMenuAction::{action}");
            assert!(menu.contains(&action), "missing menu entry {action}");
            assert!(actions.contains(&action), "missing action handler {action}");
        }
        for action in [
            "pin_session",
            "rename_session",
            "mark_session_unread",
            "archive_session",
            "SidebarMenuAction::RevealProject",
            "SidebarMenuAction::ArchiveProjectSessions",
            "SidebarMenuAction::RemoveProject",
            "remove_project",
        ] {
            assert!(
                source.contains(action) || actions.contains(action),
                "missing sidebar action {action}"
            );
        }
    }

    #[test]
    fn every_selection_popup_handles_clicks_outside_its_own_bounds() {
        for (source, ids) in [
            (
                include_str!("main.rs"),
                &[
                    "\"route-model-picker\"",
                    "\"route-reasoning-picker\"",
                    "\"model-picker\"",
                    "\"context-composition-popover\"",
                ][..],
            ),
            (
                include_str!("surfaces.rs"),
                &[
                    "\"approval-mode-menu\"",
                    "\"branch-menu\"",
                    "format!(\"subagent-{}-menu\", kind.id())",
                    "\"language-options\"",
                    "\"appearance-font-menu\"",
                    "\"archive-days-menu\"",
                ][..],
            ),
        ] {
            for id in ids {
                let tail = source.split_once(&format!(".id({id})")).unwrap().1;
                assert!(
                    tail.chars()
                        .take(1600)
                        .collect::<String>()
                        .contains(".on_mouse_down_out(cx.listener("),
                    "missing click-away on {id}"
                );
            }
        }
    }

    #[test]
    fn settings_search_matches_names_and_control_keywords() {
        let locale = Locale::resolve("zh-CN");
        assert!(super::settings_search_matches(
            "appearance",
            "外观",
            "字体",
            locale
        ));
        assert!(super::settings_search_matches(
            "appearance",
            "外观",
            "FONT size",
            locale
        ));
        assert!(super::settings_search_matches(
            "security",
            "安全扫描",
            "worker",
            locale
        ));
        assert!(super::settings_search_matches(
            "extensions",
            "扩展",
            "mcp",
            locale
        ));
        assert!(super::settings_search_matches(
            "routes",
            "模型路由",
            " ",
            locale
        ));
        assert!(!super::settings_search_matches(
            "routes",
            "模型路由",
            "font",
            locale
        ));
        assert!(!super::settings_search_matches(
            "appearance",
            "外观",
            "font not-found",
            locale
        ));
    }

    use super::ReplyForkAnchor;
    use super::{
        ToolActivityKind, agent_belongs_to_run, animate_submitted_user, archived_session_groups,
        extension_confirmation, extension_items, extension_matches, format_usage_count,
        format_usage_exact, is_agent_block, is_core_settings_route, is_edit_tool, is_file_change,
        is_host_tool_announcement, is_open_agent_state, is_process_tool_block, is_thinking_text,
        marketplace_action, marketplace_entries, model_capability_label, model_discovery_request,
        model_matches_query, model_provider_action, needs_pending_process, plugin_import_action,
        process_step_indexes, process_step_label, processing_status, provider_matches_query,
        recap_copy, run_process_summary, running_tool_summary, session_entry_id,
        settings_route_model_name, settings_route_title, settings_section_parts,
        thinking_belongs_to_tool_group, thinking_wave_opacity, tool_action, tool_activity_kind,
        tool_group_key, tool_group_range, turn_process_range, usage_activity_level, usage_heatmap,
        visible_assistant_content,
    };
    use crate::state::{Block, SessionSummary};

    #[test]
    fn extension_actions_preserve_ownership_scope_and_confirmation() {
        assert_eq!(
            super::extension_safe_target(&json!({
                "target":"https://user:secret@example.test/mcp?apiKey=secret#secret"
            })),
            "https://example.test/mcp"
        );
        assert_eq!(
            super::extension_safe_target(&json!({
                "target":"runner --api-key secret", "command":"runner"
            })),
            "runner"
        );
        let available =
            json!({"name":"example", "marketplace":"local", "origin":"codex_available"});
        assert_eq!(
            plugin_import_action(&available),
            Some(json!({
                "kind":"set_plugin_imported", "target":"example@local", "decision":"true"
            }))
        );
        let remove = plugin_import_action(&json!({"id":"exact-id","origin":"codex"})).unwrap();
        assert_eq!(remove["target"], "exact-id");
        assert_eq!(remove["decision"], "false");
        assert!(
            extension_confirmation(&remove, Locale::resolve("zh-CN"))
                .unwrap()
                .contains("Codex")
        );
        assert!(plugin_import_action(&json!({"id":"local","origin":"project"})).is_none());
        assert!(plugin_import_action(&json!({"origin":"codex_available"})).is_none());
        assert!(
            extension_confirmation(
                &json!({"kind":"set_plugin_hooks_trusted","decision":"true"}),
                Locale::resolve("zh-CN")
            )
            .is_some()
        );
        assert!(
            extension_confirmation(
                &json!({"kind":"set_plugin_hooks_trusted","decision":"false"}),
                Locale::resolve("zh-CN")
            )
            .is_none()
        );
        assert!(
            extension_confirmation(
                &json!({"kind":"set_skill_enabled","decision":"false"}),
                Locale::resolve("zh-CN")
            )
            .is_none()
        );
        let uninstall = marketplace_action("marketplace_uninstall", "example@local", "project");
        assert_eq!(uninstall["decision"], "project");
        assert_eq!(uninstall["payload"]["scope"], "project");
        assert!(extension_confirmation(&uninstall, Locale::resolve("zh-CN")).is_some());
        assert!(extension_matches(
            &json!({"sourcePath":"/project/Skills/Review/SKILL.md"}),
            "review"
        ));
        assert!(!extension_matches(&available, "unmatched"));
    }

    #[test]
    fn marketplace_keeps_sources_and_installed_plugins_when_available_is_empty() {
        let catalog = json!({"marketplaces":[{"name":"local"}],"available":[],
            "installed":[{"id":"example@local","scope":"project"}, {"id":"other@local","scope":"user"}]});
        assert_eq!(extension_items(&catalog, "marketplaces").len(), 1);
        let project = marketplace_entries(&catalog, "project");
        assert_eq!(project.len(), 1);
        assert_eq!(project[0]["id"], "example@local");
        assert_eq!(
            marketplace_entries(&catalog, "user")[0]["id"],
            "other@local"
        );
        let catalog = json!({"available":[{"id":"example@local","description":"available metadata"}],
            "installed":[{"id":"example@local","scope":"project"}]});
        let merged = marketplace_entries(&catalog, "project");
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0]["description"], "available metadata");
    }

    #[test]
    fn only_new_submitted_user_messages_animate() {
        assert!(animate_submitted_user("submitted", false));
        assert!(!animate_submitted_user("submitted", true));
        assert!(!animate_submitted_user("complete", false));
    }

    #[test]
    fn pending_process_only_fills_the_gap_after_submit() {
        let mut blocks = vec![Block {
            kind: Arc::from("user"),
            ..Default::default()
        }];
        assert!(needs_pending_process(&blocks, true));
        assert!(!needs_pending_process(&blocks, false));
        blocks.push(Block {
            kind: Arc::from("thinking"),
            state: Arc::from("running"),
            ..Default::default()
        });
        assert!(!needs_pending_process(&blocks, true));
        assert!(!thinking_belongs_to_tool_group(&blocks, 1));
        blocks.push(Block {
            kind: Arc::from("tool"),
            title: Arc::from("coding.read_file"),
            ..Default::default()
        });
        assert!(thinking_belongs_to_tool_group(&blocks, 1));
    }

    #[test]
    fn archived_sessions_are_grouped_by_project_for_folded_rendering() {
        let groups = archived_session_groups(
            &[
                SessionSummary {
                    id: Arc::from("new"),
                    workspace: Arc::from("/workspace/azem"),
                    updated_at: json!("2026-08-26T02:00:00Z"),
                    archived: true,
                    ..Default::default()
                },
                SessionSummary {
                    id: Arc::from("old"),
                    workspace: Arc::from("/workspace/azem"),
                    updated_at: json!("2026-08-25T02:00:00Z"),
                    archived: true,
                    ..Default::default()
                },
                SessionSummary {
                    workspace: Arc::from("/workspace/venat"),
                    archived: true,
                    ..Default::default()
                },
                SessionSummary {
                    workspace: Arc::from("/workspace/azem"),
                    archived: false,
                    ..Default::default()
                },
            ],
            Locale::resolve("en"),
        );
        assert_eq!(groups.len(), 2);
        assert_eq!(groups[0].0, "/workspace/azem");
        assert_eq!(groups[0].1.len(), 2);
        assert_eq!(groups[0].1[0].id.as_ref(), "new");
    }

    #[test]
    fn usage_report_keeps_sparse_daily_activity_and_compact_totals() {
        let report = json!({
            "from": "2026-08-12",
            "to": "2026-08-14",
            "days": [
                {"date": "2026-08-13", "tokens": 45_000},
                {"date": "2026-08-14", "tokens": 80_000}
            ]
        });
        let (weeks, peak) = usage_heatmap(&report);
        assert_eq!(weeks.len(), 1);
        assert_eq!(weeks[0].iter().filter(|cell| cell.in_range).count(), 3);
        assert_eq!(peak, 80_000);
        assert_eq!(usage_activity_level(45_000, peak), 2);
        assert_eq!(
            format_usage_count(706_992_941, Locale::resolve("zh-CN")),
            "7.07亿"
        );
        assert_eq!(format_usage_count(1_400_000, Locale::resolve("en")), "1.4M");
        assert_eq!(
            format_usage_count(2_959_274, Locale::resolve("zh-CN")),
            "295.9万"
        );
        assert_eq!(format_usage_count(2_959_274, Locale::resolve("en")), "3M");
        assert_eq!(format_usage_exact(706_992_941), "706,992,941");
        let august_thirteenth = weeks[0]
            .iter()
            .find(|cell| cell.date.as_deref() == Some("2026-08-13"))
            .expect("usage day");
        assert_eq!(august_thirteenth.tokens, 45_000);
    }

    #[test]
    fn historical_subagent_snapshots_attach_to_their_parent_run() {
        assert!(agent_belongs_to_run(
            &json!({"parentRunId": "run-1"}),
            "run-1"
        ));
        assert!(!agent_belongs_to_run(
            &json!({"parentRunId": "run-2"}),
            "run-1"
        ));
    }

    #[test]
    fn timeline_subagent_rows_use_the_supplied_window_entity() {
        let source = include_str!("surfaces.rs")
            .split("fn subagent_run_card(")
            .nth(1)
            .unwrap()
            .split("fn is_active_agent_state")
            .next()
            .unwrap();
        assert!(source.contains("owner.update"));
        assert!(!source.contains("window_handle()"));
        assert!(source.contains(".flex_col()"));
        assert!(source.contains("chevron-right"));
        assert!(!source.contains(".rounded_full()"));
        assert!(!source.contains(".border_1()"));
    }

    #[test]
    fn subagent_panel_has_a_roster_instead_of_a_nested_team_switcher() {
        let source = include_str!("surfaces.rs")
            .split("pub(super) fn agent_side_panel(")
            .nth(1)
            .unwrap()
            .split("fn projected_agent_blocks")
            .next()
            .unwrap();
        assert!(source.contains(".id(\"agent-roster\")"));
        assert!(!source.contains(".id(\"agent-switcher\")"));
    }

    #[test]
    fn subagent_timeline_shows_process_blocks_without_the_main_fold() {
        let source = include_str!("surfaces.rs");
        let panel = source
            .split("pub(super) fn agent_side_panel(")
            .nth(1)
            .unwrap()
            .split("fn agent_roster_row")
            .next()
            .unwrap();
        assert!(panel.contains("agent_timeline_entry("));
        let renderer = source
            .split("fn agent_timeline_entry")
            .nth(1)
            .unwrap()
            .split("fn environment_nav_row")
            .next()
            .unwrap();
        assert!(renderer.contains("is_thinking_text(block)"));
        assert!(renderer.contains("process_detail_row("));
        assert!(renderer.contains("process_step_row("));
        assert!(!renderer.contains("turn_status_header("));
    }

    #[test]
    fn subagent_tab_lives_in_the_shared_thread_titlebar() {
        let tab = include_str!("surfaces.rs")
            .split("pub(super) fn agent_panel_tab(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn agent_side_panel")
            .next()
            .unwrap();
        let panel = include_str!("surfaces.rs")
            .split("pub(super) fn agent_side_panel(")
            .nth(1)
            .unwrap()
            .split("fn agent_roster_row")
            .next()
            .unwrap();
        let workspace = include_str!("main.rs")
            .split(".id(\"workspace\")")
            .nth(1)
            .unwrap();
        assert!(!panel.contains(".id(\"agent-workspace-tab\")"));
        assert!(tab.contains(".h(px(46.))"));
        assert!(tab.contains(".right(px(-(panel_width - visible_width)))"));
        assert!(workspace.contains("agent_panel_tab("));
        assert!(workspace.contains("22. + agent_titlebar_width"));
    }

    #[test]
    fn subagent_titlebar_add_menu_routes_every_shortcut_to_a_real_action() {
        let tab = include_str!("surfaces.rs")
            .split("pub(super) fn agent_panel_tab(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn agent_side_panel")
            .next()
            .unwrap();
        let menu = include_str!("surfaces.rs")
            .split("fn agent_panel_add_menu(")
            .nth(1)
            .unwrap()
            .split("pub(super) fn agent_side_panel")
            .next()
            .unwrap();
        assert!(tab.contains("agent-panel-add"));
        for action in ["review", "terminal", "browser", "files", "side-chat"] {
            assert!(
                menu.contains(&format!("agent-panel-add-{action}")),
                "{action}"
            );
        }
        assert!(menu.contains("Surface::Changes"));
        assert!(menu.contains("set_terminal_open(true"));
        assert!(menu.contains("cx.open_url("));
        assert!(menu.contains("Surface::Files"));
        assert!(menu.contains("open_agent_roster"));
    }

    #[test]
    fn subagent_roster_keeps_only_nonterminal_states_open() {
        for state in [
            "initializing",
            "started",
            "running",
            "cancelling",
            "queued",
            "pending",
        ] {
            assert!(is_open_agent_state(state), "{state}");
        }
        for state in ["completed", "failed", "cancelled", "interrupted"] {
            assert!(!is_open_agent_state(state), "{state}");
        }
    }

    #[test]
    fn completed_tool_group_summarizes_counts_and_thoughts() {
        let blocks = [
            Block {
                kind: Arc::from("thinking"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.apply_patch"),
                tool_call_id: Arc::from("edit-1"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("diff"),
                title: Arc::from("src/main.rs"),
                tool_call_id: Arc::from("edit-1"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.read_file"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.shell"),
                ..Default::default()
            },
        ];
        assert_eq!(
            super::completed_tool_group_summary(&blocks, Locale::resolve("zh-CN")),
            Some("思考 1 轮 · 读取了 1 个文件、编辑了 1 个文件、运行了 1 条命令".to_string())
        );
        assert_eq!(
            process_step_indexes(&blocks, 0..blocks.len()),
            vec![0, 1, 3, 4]
        );
        assert!(super::is_hidden_process_block(&blocks[0]));
    }

    #[test]
    fn tool_group_summary_classifies_every_reference_activity_family() {
        let blocks = [
            "coding.search",
            "web_search",
            "web.fetch",
            "subagent.spawn",
            "browser.snapshot",
            "todo",
            "mcp.unknown",
        ]
        .map(|title| Block {
            kind: Arc::from("tool"),
            title: Arc::from(title),
            ..Default::default()
        });
        assert_eq!(
            super::completed_tool_group_summary(&blocks, Locale::resolve("zh-CN")),
            Some(
                "搜索了 1 次、网络搜索 1 次、抓取 1 个网页、调用子智能体 1 次、浏览器操作 1 次、更新了 1 次计划、调用了 1 个工具"
                    .to_string()
            )
        );
        assert_eq!(
            tool_action("browser.snapshot", Locale::resolve("en")),
            ("Browser", "monitor")
        );
    }

    #[test]
    fn only_thinking_blocks_use_the_thinking_tone() {
        assert!(is_thinking_text(&Block {
            kind: Arc::from("thinking"),
            ..Default::default()
        }));
        assert!(!is_thinking_text(&Block {
            kind: Arc::from("assistant"),
            text_phase: Arc::from("commentary"),
            ..Default::default()
        }));
    }

    #[test]
    fn subagent_route_title_uses_role_name_not_description() {
        assert_eq!(
            settings_route_title(
                "subagent",
                "explorer",
                "Investigate the workspace with focused read-only exploration",
                Locale::resolve("zh-CN"),
            ),
            "explorer"
        );
    }

    #[test]
    fn core_route_list_covers_native_settings() {
        assert!(is_core_settings_route("title"));
        assert!(is_core_settings_route("advisor"));
        assert!(!is_core_settings_route("main"));
        assert!(!is_core_settings_route("subagent"));
        assert_eq!(
            settings_route_title("title", "", "Title", Locale::resolve("zh-CN")),
            "会话标题"
        );
    }

    #[test]
    fn extension_tabs_keep_the_settings_section_and_selected_panel() {
        assert_eq!(settings_section_parts("extensions"), ("extensions", "mcp"));
        assert_eq!(
            settings_section_parts("extensions:skills"),
            ("extensions", "skills")
        );
        assert_eq!(settings_section_parts("usage"), ("usage", "mcp"));
    }

    #[test]
    fn extension_pending_feedback_does_not_reflow_or_dim_controls() {
        let source = include_str!("surfaces.rs");
        let body = source
            .split("fn settings_extensions_body(")
            .nth(1)
            .unwrap()
            .split("fn extension_items")
            .next()
            .unwrap();
        let pending = body.split(".id(\"extension-busy\")").nth(1).unwrap();
        assert!(
            pending.contains(".absolute()"),
            "pending feedback must stay out of layout flow"
        );
        assert!(
            body.find(".child(controls.search.clone())").unwrap()
                < body.find(".id(\"extension-busy\")").unwrap()
        );
        let button = source
            .split("fn extension_button(")
            .nth(1)
            .unwrap()
            .split("fn extension_row(")
            .next()
            .unwrap();
        assert!(button.contains(".tab_stop(enabled)"));
        assert!(button.contains("if enabled"));
        assert!(
            !button.contains(".opacity("),
            "saving must not flash all controls"
        );
    }

    #[test]
    fn extension_plugin_rows_keep_icons_and_a_plain_heading() {
        let source = include_str!("surfaces.rs");
        let body = source
            .split("fn settings_extensions_body(")
            .nth(1)
            .unwrap()
            .split("fn extension_items")
            .next()
            .unwrap();
        assert!(body.contains("plugin_mark(value, palette)"));
        assert!(body.contains("\"plugins\" => locale.text(\"ui.plugins\")"));
    }

    #[test]
    fn extension_logos_only_decode_bounded_self_contained_image_data() {
        use base64::Engine as _;
        let image_url = |mime: &str, data: &[u8]| {
            format!("data:{mime};base64,{}", super::STANDARD.encode(data))
        };
        let svg = br##"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path id="shape" d="M0 0h24v24H0z"/><use href="#shape"/></svg>"##;
        let source = image_url("image/svg+xml", svg);
        let image = super::plugin_logo(&source).unwrap();
        assert_eq!(image.format, gpui::ImageFormat::Svg);
        assert_eq!(image.bytes(), svg);
        assert_eq!(image.id(), super::plugin_logo(&source).unwrap().id());
        let png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a6uoAAAAASUVORK5CYII=";
        assert_eq!(
            super::plugin_logo(png).unwrap().format,
            gpui::ImageFormat::Png
        );
        for source in [
            "",
            "/tmp/icon.png",
            "https://example.test/icon.png",
            "data:text/html;base64,PHN2Zy8+",
            "data:image/png;base64,",
            "data:image/png;base64,!",
        ] {
            assert!(super::plugin_logo(source).is_none(), "{source}");
        }
        for svg in [
            r#"<svg><image href="/tmp/private.png"/></svg>"#,
            r#"<svg xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="&#47;tmp/private.png"/></svg>"#,
            r#"<!DOCTYPE svg [<!ENTITY external SYSTEM "file:///tmp/private.svg">]><svg>&external;</svg>"#,
        ] {
            assert!(super::plugin_logo(&image_url("image/svg+xml", svg.as_bytes())).is_none());
        }
        assert!(super::plugin_logo(&image_url("image/png", &vec![0; (1 << 20) + 1])).is_none());
    }

    #[test]
    fn route_model_uses_catalog_display_name() {
        let providers = vec![json!({
            "id": "chatgpt",
            "models": [{"id": "gpt-5.6-luna", "name": "GPT-5.6 Luna"}],
        })];
        assert_eq!(
            settings_route_model_name(&providers, "chatgpt", "gpt-5.6-luna"),
            "GPT-5.6 Luna"
        );
    }

    #[test]
    fn model_catalog_virtualizes_provider_and_model_rows() {
        let source = include_str!("surfaces.rs");
        let catalog = source
            .split("fn settings_catalog_body")
            .nth(1)
            .expect("model catalog")
            .split("fn model_matches_query")
            .next()
            .expect("model catalog end");
        assert!(catalog.contains("model_discovery_request"));
        assert_eq!(catalog.matches("uniform_list(").count(), 2);
        assert!(!catalog.contains(".children(provider_rows)"));
        assert!(!catalog.contains(".children(model_cards)"));
    }

    #[test]
    fn model_catalog_search_and_generic_discovery_are_wired() {
        let source = include_str!("surfaces.rs");
        let catalog = source
            .split("fn settings_catalog_body")
            .nth(1)
            .expect("model catalog")
            .split("fn provider_detail_name")
            .next()
            .expect("model catalog end");
        assert!(catalog.contains(".child(model_search)"));
        assert!(catalog.contains("model_matches_query"));
        assert!(catalog.contains("model_discovery_request"));
    }

    #[test]
    fn model_catalog_filters_models_and_builds_the_right_discovery_action() {
        let model = json!({
            "id": "moonshotai/kimi-k3-256k",
            "name": "Kimi K3 256K",
            "aliases": ["kimi-latest"]
        });
        assert!(model_matches_query(&model, "256k"));
        assert!(model_matches_query(&model, "latest"));
        assert!(!model_matches_query(&model, "gpt"));

        let generic = model_discovery_request(
            &json!({"id": "opencode-go", "subscription": false, "baseUrl": "https://example.com"}),
            "session-1",
        );
        assert_eq!(generic["provider"]["id"], "opencode-go");
        assert!(generic.get("target").is_none());

        let subscription =
            model_discovery_request(&json!({"id": "cursor", "subscription": true}), "session-1");
        assert_eq!(subscription["target"], "cursor");
        assert!(subscription.get("provider").is_none());
    }

    #[test]
    fn model_catalog_filters_providers_and_handles_subscription_auth_state() {
        let subscription = json!({
            "id": "chatgpt",
            "displayName": "OpenAI / ChatGPT",
            "backend": "subscription",
            "subscription": true,
            "enabled": true,
        });
        assert!(provider_matches_query(&subscription, "openai"));
        assert!(provider_matches_query(&subscription, "subscription"));
        assert!(!provider_matches_query(&subscription, "cursor"));

        let logout = model_provider_action(&subscription, "session-1");
        assert_eq!(logout["kind"], "logout");
        assert_eq!(logout["target"], "chatgpt");
        assert!(logout.get("provider").is_none());

        let login = model_provider_action(
            &json!({"id": "chatgpt", "subscription": true, "enabled": false}),
            "session-1",
        );
        assert_eq!(login["kind"], "login");
        assert_eq!(login["target"], "chatgpt");
        assert!(login.get("provider").is_none());
        let catalog_source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        assert!(catalog_source.contains(".when(subscription, |actions|"));
        assert!(!catalog_source.contains(".when(subscription && enabled, |actions|"));

        let toggle = model_provider_action(
            &json!({"id": "openrouter", "subscription": false, "enabled": false}),
            "session-1",
        );
        assert_eq!(toggle["kind"], "set_model_provider");
        assert_eq!(toggle["provider"]["enabled"], true);
    }

    #[test]
    fn model_capabilities_have_localized_hover_labels() {
        assert_eq!(
            model_capability_label("tools", Locale::resolve("zh-CN")),
            "工具调用"
        );
        assert_eq!(
            model_capability_label("reasoning", Locale::resolve("en")),
            "Reasoning"
        );
        assert_eq!(
            model_capability_label("in:image", Locale::resolve("zh-CN")),
            "输入: image"
        );
        assert_eq!(
            model_capability_label("out:text", Locale::resolve("en")),
            "Output: text"
        );
    }

    #[test]
    fn completed_run_uses_durable_elapsed_time() {
        let process = Block {
            kind: Arc::from("thinking"),
            ..Default::default()
        };
        let terminal = Block {
            kind: Arc::from("assistant"),
            text_phase: Arc::from("final_answer"),
            extra: HashMap::from([(
                "data".to_string(),
                json!({"startedAt": "1000", "completedAt": "126000"}),
            )]),
            ..Default::default()
        };
        assert_eq!(
            run_process_summary(
                &[process],
                Some(&terminal),
                false,
                0,
                Locale::resolve("zh-CN")
            ),
            "已处理 2分钟 5秒"
        );
        let cancelled = Block {
            kind: Arc::from("status"),
            title: Arc::from("run_cancelled"),
            state: Arc::from("cancelled"),
            extra: HashMap::from([("runElapsedMs".to_string(), json!(65_000))]),
            ..Default::default()
        };
        assert_eq!(
            run_process_summary(&[cancelled], None, false, 0, Locale::resolve("zh-CN")),
            "你在 1分钟 5秒 后停止了"
        );
        assert_eq!(
            run_process_summary(&[], None, true, 212_000, Locale::resolve("zh-CN")),
            "正在思考"
        );
        assert_eq!(
            processing_status(212_000, Locale::resolve("zh-CN")),
            "正在处理 · 3分钟 32秒"
        );
        assert_eq!(
            tool_action("coding.read_file", Locale::resolve("zh-CN")).0,
            "读取文件"
        );
        assert_eq!(
            tool_action("coding.shell", Locale::resolve("zh-CN")).0,
            "运行命令"
        );
    }

    #[test]
    fn only_the_tool_group_header_owns_expansion() {
        let source = include_str!("surfaces.rs")
            .split("pub(super) fn timeline_entry")
            .nth(1)
            .unwrap()
            .split("struct TurnProcessRange")
            .next()
            .unwrap();
        let tool_branch = source
            .split("if is_process_tool_block(block)")
            .nth(1)
            .unwrap()
            .split("if is_agent_block(block)")
            .next()
            .unwrap();
        assert!(tool_branch.contains("tool_group_entry("));
        assert!(source.contains("thinking_process_entry("));
        let group = include_str!("surfaces.rs")
            .split("fn tool_group_entry")
            .nth(1)
            .unwrap()
            .split("pub(super) fn needs_pending_process")
            .next()
            .unwrap();
        assert!(group.contains("process_step_indexes"));
        assert!(group.contains(".child(turn_status_header("));
        assert!(group.contains("process_step_row("));
        assert!(group.contains(".when(expanded"));
        assert!(group.contains(".left(px(8.))"));
        assert!(group.contains("is_expanded(&key, running)"));
        assert!(group.contains(".when_some(processing"));
        assert!(!group.contains("\"git-branch\""));
        let pending = include_str!("surfaces.rs")
            .split("fn pending_process_entry")
            .nth(1)
            .unwrap()
            .split("struct TurnProcessRange")
            .next()
            .unwrap();
        assert!(pending.contains("processing_status"));
        assert!(pending.contains("animated_activity_label"));
        assert!(!pending.contains(".h(px(1.))"));
        let header = include_str!("surfaces.rs")
            .split("fn turn_status_header")
            .nth(1)
            .unwrap()
            .split("fn is_hidden_process_block")
            .next()
            .unwrap();
        assert!(header.contains(".aria_expanded(expanded)"));
        assert!(header.contains(".toggle(&toggle_key, running)"));
        assert!(header.contains("chevron-down"));
        assert!(header.contains("chevron-right"));
        let collapsed_header = header
            .split(".children(expanded.map")
            .next()
            .expect("status row before disclosure chevron");
        assert!(!collapsed_header.contains(".child(icon("));
    }

    #[test]
    fn commentary_splits_tools_into_independent_groups() {
        let blocks = [
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.read_file"),
                run_id: Arc::from("run-1"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("thinking"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.shell"),
                run_id: Arc::from("run-1"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("commentary"),
                content: "阶段说明".to_string(),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.apply_patch"),
                run_id: Arc::from("run-1"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("assistant"),
                ..Default::default()
            },
        ];
        let turn = turn_process_range(&blocks, 0).unwrap();
        let first = tool_group_range(&blocks, &turn, 0);
        let second = tool_group_range(&blocks, &turn, 4);
        assert_eq!(first, 0..3);
        assert_eq!(second, 4..5);
        assert_ne!(
            tool_group_key(&blocks, &first, 0),
            tool_group_key(&blocks, &second, 4)
        );
    }

    #[test]
    fn tool_rows_keep_their_action_and_preview() {
        let tools = [
            Block {
                kind: Arc::from("thinking"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.read_file"),
                extra: HashMap::from([(
                    "data".to_string(),
                    json!({"arguments": {"path": "README.md"}}),
                )]),
                ..Default::default()
            },
            Block {
                kind: Arc::from("commentary"),
                content: "继续处理".to_string(),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.read_file"),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.shell"),
                ..Default::default()
            },
        ];
        assert_eq!(
            process_step_label(&tools[1], Locale::resolve("zh-CN")),
            "已读取 README.md"
        );
        let edit = Block {
            kind: Arc::from("tool"),
            title: Arc::from("coding.apply_patch"),
            extra: HashMap::from([(
                "fileChange".to_string(),
                json!({"files": [{"path": ".envlocal", "additions": 1, "deletions": 1}]}),
            )]),
            ..Default::default()
        };
        assert_eq!(
            process_step_label(&edit, Locale::resolve("zh-CN")),
            "已编辑 .envlocal +1 -1"
        );
        let command = Block {
            kind: Arc::from("tool"),
            title: Arc::from("coding.shell"),
            extra: HashMap::from([(
                "data".to_string(),
                json!({"arguments": {"cmd": "go test ./..."}, "elapsedMs": 1000}),
            )]),
            ..Default::default()
        };
        assert_eq!(
            process_step_label(&command, Locale::resolve("zh-CN")),
            "已在 1 秒 内运行 go test ./..."
        );
        assert!(is_process_tool_block(&tools[1]));
        assert!(!is_process_tool_block(&tools[2]));
        assert!(is_agent_block(&Block {
            kind: Arc::from("agent"),
            ..Default::default()
        }));
    }

    #[test]
    fn unchanged_formatter_stays_a_visible_non_edit_tool() {
        let unchanged = Block {
            kind: Arc::from("tool"),
            title: Arc::from("coding.gofmt"),
            extra: HashMap::from([("structured".to_string(), json!({"changed": false}))]),
            ..Default::default()
        };
        let changed = Block {
            extra: HashMap::from([("structured".to_string(), json!("{\"changed\":true}"))]),
            ..unchanged.clone()
        };
        assert!(!is_edit_tool(&unchanged));
        assert_eq!(tool_activity_kind(&unchanged), ToolActivityKind::Other);
        assert!(is_edit_tool(&changed));
        assert_eq!(tool_activity_kind(&changed), ToolActivityKind::Edit);
    }

    #[test]
    fn running_process_header_summarizes_the_visible_activity_tree() {
        let progress = vec![
            Block {
                kind: Arc::from("thinking"),
                content: "正在检查仓库".to_string(),
                ..Default::default()
            },
            Block {
                kind: Arc::from("tool"),
                title: Arc::from("coding.shell"),
                state: Arc::from("running"),
                extra: HashMap::from([(
                    "data".to_string(),
                    json!({"arguments": {"cmd": "git status"}}),
                )]),
                ..Default::default()
            },
        ];
        assert_eq!(
            run_process_summary(&progress, None, true, 30_000, Locale::resolve("zh-CN")),
            "思考 1 轮 · 运行了 1 条命令"
        );
    }

    #[test]
    fn historical_agent_thinking_restores_markdown_block_breaks() {
        let blocks = super::projected_agent_blocks(&[json!({
            "kind": "thinking_delta",
            "text": "**Inspecting workspace****Planning fix**"
        })]);
        assert_eq!(
            blocks[0].content,
            "**Inspecting workspace**\n\n**Planning fix**"
        );
    }

    #[test]
    fn active_tool_keeps_a_flat_action_row_without_nested_disclosure() {
        let tool = Block {
            kind: Arc::from("tool"),
            title: Arc::from("coding.read_file"),
            state: Arc::from("reviewing_approval"),
            extra: HashMap::from([(
                "data".to_string(),
                json!({"arguments": {"path": "README.md"}}),
            )]),
            ..Default::default()
        };
        assert_eq!(
            running_tool_summary(&tool, Locale::resolve("zh-CN")),
            "正在运行 读取文件 \"README.md\""
        );
        let source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        let detail = source
            .split("fn process_step_row(")
            .nth(1)
            .unwrap()
            .split("fn process_step_label")
            .next()
            .unwrap();
        assert!(detail.contains("process_step_presentation"));
        assert!(detail.contains("process-step-enter"));
        assert!(detail.contains(".child(icon("));
        assert!(!detail.contains("aria_expanded"));
        assert!(!detail.contains("chevron-right"));
        let header = source
            .split("fn turn_status_header")
            .nth(1)
            .unwrap()
            .split("fn is_hidden_process_block")
            .next()
            .unwrap();
        assert!(!header.contains(".h(px(1.))"));
    }

    #[test]
    fn durable_file_change_projection_is_recognized() {
        let block = Block {
            kind: Arc::from("tool"),
            extra: HashMap::from([(
                "fileChange".to_string(),
                json!("{\"files\":[{\"path\":\"a.rs\"}]}"),
            )]),
            ..Default::default()
        };
        assert!(is_file_change(&block));
    }

    #[test]
    fn legacy_host_verification_notices_are_hidden() {
        let first = "Useful answer.\n\nVerification evidence is missing or stale for the current workspace snapshot after one retry. The work remains uncertain and is not reported as complete.";
        let second = "Verification failed for the current workspace snapshot. The attempted result is not reported as complete; inspect the failed checks and correct the work before retrying.";
        assert_eq!(visible_assistant_content(first), "Useful answer.");
        assert_eq!(visible_assistant_content(second), "");
        assert_eq!(
            visible_assistant_content("Useful answer."),
            "Useful answer."
        );
    }

    #[test]
    fn reply_metadata_is_hidden_and_resolves_the_active_branch_entry() {
        let content = "Useful answer.\n\n<oai-mem-citation>\n<citation_entries>\nMEMORY.md:1-2|note=[kept the native layout]\nrollout.md:3-4|note=[used the saved behavior]\n</citation_entries>\n</oai-mem-citation>";
        assert_eq!(visible_assistant_content(content), "Useful answer.");
        assert_eq!(
            super::assistant_memory_notes(content),
            vec!["kept the native layout", "used the saved behavior"]
        );

        let tree = json!({
            "activeLeafEntryId": "answer-2",
            "roots": [{
                "entry": {"id":"user-1","sequence":1,"kind":"user"},
                "children": [{
                    "entry": {"id":"answer-1","sequence":2,"kind":"assistant"},
                    "children": [{
                        "entry": {"id":"user-2","sequence":3,"kind":"user"},
                        "children": [{"entry": {"id":"answer-2","sequence":4,"kind":"assistant"}}]
                    }]
                }]
            }]
        });
        assert_eq!(
            session_entry_id(
                &tree,
                ReplyForkAnchor {
                    sequence: None,
                    assistant_ordinal: 1,
                }
            )
            .as_deref(),
            Some("answer-2")
        );
        assert_eq!(
            session_entry_id(
                &tree,
                ReplyForkAnchor {
                    sequence: Some(2),
                    assistant_ordinal: 1,
                }
            )
            .as_deref(),
            Some("answer-1")
        );
    }

    #[test]
    fn message_context_distinguishes_turn_roles_and_reads_user_time() {
        let user = Block {
            kind: Arc::from("user"),
            run_id: Arc::from("run-1"),
            extra: HashMap::from([("data".to_string(), json!({"createdAt": "1787875200000"}))]),
            ..Default::default()
        };
        let assistant = Block {
            kind: Arc::from("assistant"),
            run_id: Arc::from("run-1"),
            ..Default::default()
        };
        assert_ne!(
            super::timeline_message_key(0, &user),
            super::timeline_message_key(1, &assistant)
        );
        assert!(super::message_time(&user).is_some());
    }

    #[test]
    fn host_tool_announcements_are_hidden_from_process_details() {
        let synthetic = Block {
            content: "different localized copy".to_string(),
            extra: HashMap::from([(
                "data".to_string(),
                json!({"synthetic": "tool_announcement"}),
            )]),
            ..Default::default()
        };
        let legacy = Block {
            content: "正在调用所需工具，并根据实际结果继续。".to_string(),
            ..Default::default()
        };
        assert!(is_host_tool_announcement(&synthetic));
        assert!(is_host_tool_announcement(&legacy));
        assert!(!is_host_tool_announcement(&Block {
            content: "正在读取文件".to_string(),
            ..Default::default()
        }));
    }

    #[test]
    fn recap_projects_copy_without_exposing_raw_json_and_thinking_wave() {
        let (summary, goal, open_items) = recap_copy(&json!({
            "sessionId": "secret-internal-id",
            "summary": "Summary",
            "goal": "Goal",
            "openItems": "Open",
        }));
        assert_eq!(
            (summary.as_str(), goal.as_str(), open_items.as_str()),
            ("Summary", "Goal", "Open")
        );
        assert_eq!(thinking_wave_opacity(0., 0, 4), 0.42);
        assert_eq!(thinking_wave_opacity(0.5, 2, 4), 1.);
        assert_eq!(thinking_wave_opacity(1., 3, 4), 0.42);
    }
}
