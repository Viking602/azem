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
pub(crate) const SIDE_PANEL_TRANSITION: Duration = Duration::from_millis(220);
const ENVIRONMENT_PANEL_RETURN_TRANSITION: Duration = Duration::from_millis(500);
mod environment;
mod navigation;
mod projects;
mod pull_requests;
mod security;
mod settings;
mod timeline;
mod workspace;

pub(super) use environment::{agent_panel_tab, agent_side_panel, environment_panel, side_panel};
pub(super) use navigation::{
    search_surface, session_rename_modal, sidebar, sidebar_context_menu_view,
};
pub(super) use projects::projects_surface;
pub(super) use pull_requests::pull_requests_surface;
pub(super) use security::security_surface;
pub(super) use settings::{ModelCapabilityTooltip, extension_confirmation, settings_surface};
pub(super) use timeline::process::needs_pending_process;
pub(super) use timeline::timeline_entry;
pub(super) use workspace::{workspace_changes_surface, workspace_files_surface};

use settings::{format_usage_duration, pick, settings_card_header};
use timeline::process::{
    is_active_agent_state, is_open_agent_state, is_process_tool_block, is_thinking_text,
    process_detail_row, process_step_row, subagent_status_label,
};

#[cfg(test)]
use environment::{
    environment_panel_return_spring, environment_snapshot, projected_agent_blocks, recap_copy,
    todo_status_mark,
};
#[cfg(test)]
use security::{security_compact_text, security_progress_fraction};
#[cfg(test)]
use settings::{
    archived_session_groups, extension_items, extension_matches, extension_safe_target,
    format_usage_count, format_usage_exact, is_core_settings_route, marketplace_action,
    marketplace_entries, model_capability_label, model_discovery_request, model_matches_query,
    model_provider_action, plugin_import_action, plugin_logo, provider_matches_query,
    provider_quota_remaining, settings_route_model_name, settings_route_title,
    settings_search_matches, settings_section_parts, usage_activity_level, usage_heatmap,
};
#[cfg(test)]
use timeline::process::{
    ToolActivityKind, agent_belongs_to_run, agent_matches_group, completed_tool_group_summary,
    is_agent_block, is_edit_tool, is_file_change, is_hidden_process_block,
    is_host_tool_announcement, process_step_indexes, process_step_label, processing_status,
    resolved_agent_state, run_process_summary, running_tool_summary,
    thinking_belongs_to_tool_group, tool_action, tool_activity_kind, tool_group_key,
    tool_group_range, tool_step_detail, turn_process_range,
};
#[cfg(test)]
use timeline::{message_time, timeline_message_key};

#[cfg(test)]
mod tests;

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
