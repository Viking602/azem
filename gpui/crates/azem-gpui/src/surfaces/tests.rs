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
        super::provider_quota_remaining(&json!({"quotaAvailable": true, "quotaUsedPercent": 42.5})),
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
    let environment = crate::SURFACES_SOURCE
        .split("fn environment_panel(")
        .nth(1)
        .unwrap()
        .split("fn side_panel(")
        .next()
        .unwrap();
    assert!(!environment.contains("locale.text(\"ui.sources\")"));
    assert!(!environment.contains("locale.text(\"ui.sessionHistory\")"));
}

#[test]
fn floating_environment_stays_separate_from_the_animated_side_panel() {
    let source = crate::SURFACES_SOURCE
        .split("fn side_panel(")
        .nth(1)
        .unwrap()
        .split("fn agent_panel_tab(")
        .next()
        .unwrap();
    assert!(source.contains("let visible_width = this.side_panel_visible_width"));
    assert!(source.contains(".right(px(-(panel_width - visible_width)))"));
    assert!(source.contains(".id(\"side-panel-resize-handle\")"));
    assert!(source.contains(".cursor_col_resize()"));
    let environment = crate::SURFACES_SOURCE
        .split("fn environment_panel(")
        .nth(1)
        .unwrap()
        .split("fn side_panel(")
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
    let files = json!({"filesCompleted":17,"filesTotal":100,"workersDone":4,"workersPlanned":4});
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
    let alert = include_str!("../../../../assets/icons/shield-alert.svg");
    assert!(alert.contains("M12 8v4"));
    assert!(alert.contains("M12 16h.01"));
}

#[test]
fn custom_picker_keys_capture_before_synthetic_clicks() {
    let source = crate::SURFACES_SOURCE;
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
    let source = crate::SURFACES_SOURCE;
    assert_eq!(source.matches(".position(picker_menu_position(").count(), 2);
}

#[test]
fn sidebar_rows_expose_real_context_menus() {
    let source = crate::SURFACES_SOURCE;
    let sidebar = source
        .split("fn sidebar(")
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
        .split("fn session_rename_modal(")
        .next()
        .unwrap();
    assert!(menu.contains(".h(px(30.))"));
    assert!(menu.contains(".w(px(172.))"));
    assert!(!menu.contains(".h(px(38.))"));
    assert!(!menu.contains(".w(px(210.))"));
    assert!(!menu.contains(".w(px(244.))"));
    let actions = crate::MAIN_SOURCE;
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
            crate::MAIN_SOURCE,
            &[
                "\"route-model-picker\"",
                "\"route-reasoning-picker\"",
                "\"model-picker\"",
                "\"context-composition-popover\"",
            ][..],
        ),
        (
            crate::SURFACES_SOURCE,
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
    ToolActivityKind, agent_belongs_to_run, agent_matches_group, animate_submitted_user,
    archived_session_groups, extension_confirmation, extension_items, extension_matches,
    format_usage_count, format_usage_exact, is_agent_block, is_core_settings_route, is_edit_tool,
    is_file_change, is_host_tool_announcement, is_open_agent_state, is_process_tool_block,
    is_thinking_text, marketplace_action, marketplace_entries, model_capability_label,
    model_discovery_request, model_matches_query, model_provider_action, needs_pending_process,
    plugin_import_action, process_step_indexes, process_step_label, processing_status,
    provider_matches_query, recap_copy, resolved_agent_state, run_process_summary,
    running_tool_summary, session_entry_id, settings_route_model_name, settings_route_title,
    settings_section_parts, thinking_belongs_to_tool_group, todo_status_mark, tool_action,
    tool_activity_kind, tool_group_key, tool_group_range, tool_step_detail, turn_process_range,
    usage_activity_level, usage_heatmap, visible_assistant_content,
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
    let available = json!({"name":"example", "marketplace":"local", "origin":"codex_available"});
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
    let block = Block {
        kind: Arc::from("agent"),
        extra: HashMap::from([("agentId".to_string(), json!("agent-1"))]),
        ..Default::default()
    };
    assert!(agent_matches_group(
        &json!({"id":"agent-1","parentRunId":"run-1"}),
        std::slice::from_ref(&block),
        "run-1"
    ));
    assert!(!agent_matches_group(
        &json!({"id":"agent-2","parentRunId":"run-1"}),
        std::slice::from_ref(&block),
        "run-1"
    ));
    let task_call = Block {
        kind: Arc::from("tool"),
        extra: HashMap::from([("toolCallId".to_string(), json!("call-1"))]),
        ..Default::default()
    };
    assert!(agent_matches_group(
        &json!({"id":"agent-1","parentRunId":"run-1","parentToolCallId":"call-1"}),
        std::slice::from_ref(&task_call),
        "run-1"
    ));
    assert!(!agent_matches_group(
        &json!({"id":"agent-2","parentRunId":"run-1","parentToolCallId":"call-2"}),
        std::slice::from_ref(&task_call),
        "run-1"
    ));
    let failed = Block {
        state: Arc::from("failed"),
        ..block.clone()
    };
    assert_eq!(resolved_agent_state(Some(&failed), "running"), "failed");
    assert_eq!(resolved_agent_state(Some(&block), "completed"), "completed");
}

#[test]
fn subagents_use_one_stable_click_disclosure() {
    let branch = crate::SURFACES_SOURCE
        .split("if is_agent_block(block)")
        .nth(1)
        .unwrap()
        .split("if kind == \"context_compaction\"")
        .next()
        .unwrap();
    assert!(branch.contains("return div().h(px(0.))"));
    let group = crate::SURFACES_SOURCE
        .split("fn tool_group_entry")
        .nth(1)
        .unwrap()
        .split("let summary = run_process_summary")
        .next()
        .unwrap();
    assert!(group.contains("subagent_run_card("));
    assert!(group.contains("return div()"));
    assert!(group.contains("index,\n            group,"));
    assert!(!group.contains("&blocks[turn.start..turn.end]"));
    let card = crate::SURFACES_SOURCE
        .split("fn subagent_run_card(")
        .nth(1)
        .unwrap()
        .split("fn is_active_agent_state")
        .next()
        .unwrap();
    assert!(card.contains("subagent-run-tag"));
    assert!(card.contains(".aria_expanded(expanded)"));
    assert!(card.contains(".on_click("));
    assert!(!card.contains(".on_hover("));
    assert!(card.contains("subagent-list"));
    assert!(card.contains(".children(rows)"));
    assert!(card.contains("refresh_transcript_layout(group_index)"));
    assert!(card.contains("subagents:{process_key}:{group_index}"));
    assert!(!card.contains(".overflow_y_scroll()"));
    assert!(!card.contains("window_handle()"));
}

#[test]
fn subagent_panel_has_a_roster_instead_of_a_nested_team_switcher() {
    let source = crate::SURFACES_SOURCE
        .split("fn agent_side_panel(")
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
    let source = crate::SURFACES_SOURCE;
    let panel = source
        .split("fn agent_side_panel(")
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
    let tab = crate::SURFACES_SOURCE
        .split("fn agent_panel_tab(")
        .nth(1)
        .unwrap()
        .split("fn agent_side_panel")
        .next()
        .unwrap();
    let panel = crate::SURFACES_SOURCE
        .split("fn agent_side_panel(")
        .nth(1)
        .unwrap()
        .split("fn agent_roster_row")
        .next()
        .unwrap();
    let workspace = crate::MAIN_SOURCE
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
    let tab = crate::SURFACES_SOURCE
        .split("fn agent_panel_tab(")
        .nth(1)
        .unwrap()
        .split("fn agent_side_panel")
        .next()
        .unwrap();
    let menu = crate::SURFACES_SOURCE
        .split("fn agent_panel_add_menu(")
        .nth(1)
        .unwrap()
        .split("fn agent_side_panel")
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
fn completed_tool_group_summarizes_tools_without_a_thinking_label() {
    let blocks = [
        Block {
            kind: Arc::from("thinking"),
            content: "直接展示这段思考".to_string(),
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
        Some("读取了 1 个文件、编辑了 1 个文件、运行了 1 条命令".to_string())
    );
    assert_eq!(
        process_step_indexes(&blocks, 0..blocks.len()),
        vec![0, 1, 3, 4]
    );
    let late_thinking = [
        Block {
            kind: Arc::from("tool"),
            tool_call_id: Arc::from("read-1"),
            ..Default::default()
        },
        Block {
            kind: Arc::from("thinking"),
            content: "先说明为什么读取".to_string(),
            ..Default::default()
        },
        Block {
            kind: Arc::from("tool"),
            tool_call_id: Arc::from("read-2"),
            ..Default::default()
        },
        Block {
            kind: Arc::from("thinking"),
            content: "补充验证依据".to_string(),
            ..Default::default()
        },
    ];
    assert_eq!(
        process_step_indexes(&late_thinking, 0..late_thinking.len()),
        vec![1, 3, 0, 2]
    );
    let inline_script = Block {
        kind: Arc::from("tool"),
        title: Arc::from("coding.shell"),
        extra: HashMap::from([(
            "arguments".to_string(),
            json!({"command":"python3 - <<'PY'\nfrom pathlib import Path\nprint(Path.cwd())\nPY"}),
        )]),
        ..Default::default()
    };
    let script_label = process_step_label(&inline_script, Locale::resolve("zh-CN"));
    assert!(!script_label.contains('\n'), "{script_label:?}");
    assert!(script_label.contains("python3"), "{script_label:?}");
    assert!(!script_label.contains("pathlib"), "{script_label:?}");
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
    let source = crate::SURFACES_SOURCE;
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
    let source = crate::SURFACES_SOURCE;
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
    let image_url =
        |mime: &str, data: &[u8]| format!("data:{mime};base64,{}", super::STANDARD.encode(data));
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
    let source = crate::SURFACES_SOURCE;
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
    let source = crate::SURFACES_SOURCE;
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
    let catalog_source = crate::SURFACES_SOURCE;
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
        "正在处理 · 3分钟 32秒"
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
fn active_thinking_uses_one_processing_row() {
    let source = crate::SURFACES_SOURCE
        .split("fn thinking_process_entry")
        .nth(1)
        .unwrap()
        .split("struct TurnProcessRange")
        .next()
        .unwrap();
    assert!(source.contains("processing_status(elapsed_ms, locale)"));
    assert_eq!(source.matches("processing_status(").count(), 1);
    assert!(!source.contains(".when(active"));
}

#[test]
fn only_the_tool_group_header_owns_expansion() {
    let source = crate::SURFACES_SOURCE
        .split("fn timeline_entry")
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
    let group = crate::SURFACES_SOURCE
        .split("fn tool_group_entry")
        .nth(1)
        .unwrap()
        .split("fn needs_pending_process")
        .next()
        .unwrap();
    assert!(group.contains("process_step_indexes"));
    assert!(group.contains(".child(turn_status_header("));
    assert!(group.contains("process_step_row("));
    assert!(group.contains(".when(expanded"));
    assert!(group.contains(".left(px(8.))"));
    assert!(group.contains("is_expanded(&key)"));
    assert!(!group.contains(".when_some(processing"));
    assert!(!group.contains("\"git-branch\""));
    let pending = crate::SURFACES_SOURCE
        .split("fn pending_process_entry")
        .nth(1)
        .unwrap()
        .split("struct TurnProcessRange")
        .next()
        .unwrap();
    assert!(pending.contains("processing_status"));
    assert!(pending.contains("animated_activity_label"));
    assert!(!pending.contains(".h(px(1.))"));
    let header = crate::SURFACES_SOURCE
        .split("fn turn_status_header")
        .nth(1)
        .unwrap()
        .split("fn is_hidden_process_block")
        .next()
        .unwrap();
    assert!(header.contains(".aria_expanded(expanded)"));
    assert!(header.contains(".toggle(&toggle_key)"));
    assert!(header.contains("chevron-down"));
    assert!(header.contains("chevron-right"));
    let collapsed_header = header
        .split(".children(expanded.map")
        .next()
        .expect("status row before disclosure chevron");
    assert!(!collapsed_header.contains(".child(icon("));
    assert!(header.contains("rolling_activity_label"));
    assert!(crate::SURFACES_SOURCE.contains("activity-roll-out"));
    assert!(crate::SURFACES_SOURCE.contains("activity-roll-in"));
    assert!(crate::SURFACES_SOURCE.contains("row.top(px(-17. * progress))"));
    let step = crate::SURFACES_SOURCE
        .split("fn process_step_row")
        .nth(1)
        .unwrap()
        .split("fn process_step_presentation")
        .next()
        .unwrap();
    assert!(step.contains("if is_thinking_text(block)"));
    assert!(step.contains("process_detail_row("));
    let activity = crate::SURFACES_SOURCE
        .split("fn animated_activity_label")
        .nth(1)
        .unwrap()
        .split("fn rolling_activity_label")
        .next()
        .unwrap();
    assert_eq!(activity.matches(".with_animation(").count(), 1);
    assert!(!activity.contains(".chars()"));
    assert!(!activity.contains("character_index"));
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
fn subagent_tool_rows_render_structured_arguments_without_json() {
    let block = Block {
        kind: Arc::from("tool"),
        title: Arc::from("skill_resource"),
        content: r#"{"skill":"check","path":"references/persona-catalog.md"}"#.to_string(),
        ..Default::default()
    };
    let label = process_step_label(&block, Locale::resolve("zh-CN"));
    assert!(label.contains("check · references/persona-catalog.md"));
    assert!(!label.contains('{'));
    assert!(!label.contains("\"skill\""));
}

#[test]
fn active_plan_item_uses_a_distinct_filled_marker() {
    assert_eq!(todo_status_mark("completed"), "✓");
    assert_eq!(todo_status_mark("in_progress"), "●");
    assert_eq!(todo_status_mark("pending"), "○");
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
        "运行命令 git status"
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
fn tool_rows_show_status_and_failure_disclosure() {
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
        "读取文件 README.md"
    );
    let failed = Block {
        state: Arc::from("failed"),
        content: "permission denied while reading README.md".to_string(),
        ..tool.clone()
    };
    let failed_detail = tool_step_detail(&failed, std::slice::from_ref(&failed)).unwrap();
    assert_eq!(
        failed_detail.content,
        "permission denied while reading README.md"
    );
    assert!(!failed_detail.is_diff);
    let source = crate::SURFACES_SOURCE;
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
    let status = source
        .split("fn process_tool_state_mark(")
        .nth(1)
        .unwrap()
        .split("fn process_step_presentation")
        .next()
        .unwrap();
    assert!(status.contains("\"loader\""));
    assert!(status.contains("\"process-tool-running\""));
    assert!(status.contains("Animation::new(Duration::from_millis(800)).repeat()"));
    assert!(status.contains("\"check\""));
    assert!(status.contains("\"shield-alert\""));
    assert!(detail.contains("aria_expanded"));
    assert!(detail.contains("process-tool-detail"));
    assert!(detail.contains("chevron-right"));
    assert!(detail.contains("Role::Button"));
    assert!(!detail.contains("row.bg(palette.hover)"));
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
fn successful_tools_expose_results_and_edit_diffs() {
    let read = Block {
        kind: Arc::from("tool"),
        title: Arc::from("coding.read_file"),
        state: Arc::from("completed"),
        content: "README contents".to_string(),
        ..Default::default()
    };
    let read_detail = tool_step_detail(&read, std::slice::from_ref(&read)).unwrap();
    assert_eq!(read_detail.content, "README contents");
    assert!(!read_detail.is_diff);

    let edit = Block {
        kind: Arc::from("tool"),
        title: Arc::from("coding.edit_hashline"),
        tool_call_id: Arc::from("edit-1"),
        state: Arc::from("completed"),
        extra: HashMap::from([(
            "structured".to_string(),
            json!({
                "sections": [{
                    "path": "src/main.rs",
                    "diff": "-old line\n+new line"
                }]
            }),
        )]),
        ..Default::default()
    };
    let edit_detail = tool_step_detail(&edit, std::slice::from_ref(&edit)).unwrap();
    assert!(edit_detail.is_diff);
    assert!(edit_detail.content.contains("--- src/main.rs"));
    assert!(edit_detail.content.contains("-old line\n+new line"));

    let renderer = crate::SURFACES_SOURCE
        .split("fn process_step_row(")
        .nth(1)
        .unwrap()
        .split("fn process_tool_state_mark")
        .next()
        .unwrap();
    assert!(renderer.contains("let can_expand = detail.is_some()"));
    assert!(renderer.contains("format!(\"```diff"));
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
fn recap_projects_copy_without_exposing_raw_json() {
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
}
