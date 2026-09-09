use crate::localization::Locale;
use serde_json::json;
use uuid::Uuid;

#[test]
fn first_window_overlaps_runtime_start_with_native_window_creation() {
    let source = crate::MAIN_SOURCE;
    let main = source.split("fn main() {").nth(1).unwrap();
    let runtime_start = main
        .find("RuntimeConnection::start(options.clone())")
        .expect("runtime starts in main");
    let application_start = main
        .find("let application = application()")
        .expect("GPUI application starts");
    assert!(runtime_start < application_start);

    let open_window = source
        .split("fn open_main_window(")
        .nth(1)
        .unwrap()
        .split("fn runtime_options()")
        .next()
        .unwrap();
    let runtime_ready = open_window
        .find("runtime.wait_for_startup")
        .expect("first window waits for runtime");
    let window_open = open_window.find("cx.open_window").unwrap();
    let view_create = open_window.find("AzemWindow::new").unwrap();
    assert!(window_open < runtime_ready && runtime_ready < view_create);
}

#[test]
fn window_close_detaches_without_stopping_the_workspace_daemon() {
    let drop_impl = crate::MAIN_SOURCE
        .split("impl Drop for AzemWindow")
        .nth(1)
        .expect("window drop implementation")
        .split("fn composer_has_submission")
        .next()
        .unwrap();
    assert!(drop_impl.contains("self.runtime.detach()"));
    assert!(!drop_impl.contains("self.runtime.disconnect()"));
}

#[test]
fn project_session_navigation_reuses_the_existing_window() {
    let surfaces = crate::SURFACES_SOURCE;
    assert!(!surfaces.contains("launch_gpui_window"));
    assert!(!surfaces.contains("std::process::Command::new"));
    assert_eq!(surfaces.matches("this.switch_workspace(").count(), 3);

    let runtime_events = crate::MAIN_SOURCE
        .split("fn apply_runtime_message(")
        .nth(1)
        .unwrap()
        .split("fn apply_success(")
        .next()
        .unwrap();
    assert!(runtime_events.contains("self.switch_workspace("));
}

#[test]
fn project_removal_is_catalog_only_and_reuses_the_window() {
    let source = crate::MAIN_SOURCE;
    let action = source
        .split("fn run_project_menu_action(")
        .nth(1)
        .unwrap()
        .split("fn commit_session_rename")
        .next()
        .unwrap();
    for required in [
        "SidebarMenuAction::RemoveProject",
        "\"remove_project\"",
        "PendingRequest::RemoveProject",
        "sidebar.removeOnlyProject",
    ] {
        assert!(action.contains(required), "{required}");
    }
    assert!(!action.contains("remove_file"));
    let response = source
        .split("PendingRequest::RemoveProject {")
        .nth(1)
        .unwrap()
        .split("PendingRequest::Search")
        .next()
        .unwrap();
    assert!(response.contains("self.switch_workspace("));
}

#[test]
fn open_environment_panel_refreshes_metrics_after_terminal_runtime_events() {
    let source = crate::MAIN_SOURCE
        .split("fn apply_runtime_message(")
        .nth(1)
        .unwrap()
        .split("fn apply_success(")
        .next()
        .unwrap();
    for kind in [
        "tool_finished",
        "run_finished",
        "run_failed",
        "run_cancelled",
    ] {
        assert!(source.contains(kind));
    }
    assert!(source.contains("self.request_environment_metrics(false)"));
}

#[test]
fn environment_and_side_panel_have_independent_controls() {
    let source = crate::MAIN_SOURCE;
    assert!(source.contains(".id(\"environment-toggle\")"));
    assert!(source.contains("this.toggle_environment_panel(cx)"));
    assert!(source.contains(".id(\"side-panel-toggle\")"));
    assert!(source.contains("this.toggle_side_panel(window, cx)"));
    assert!(source.contains("environment_panel_fits("));
    assert!(source.contains("side_panel_layout_width"));
    assert!(source.contains("ENVIRONMENT_PANEL_RESERVED_WIDTH"));
    assert!(source.contains("let environment_returning"));
    assert!(source.contains("self.side_panel_visible_width"));
    assert!(source.contains(".max(ENVIRONMENT_PANEL_RESERVED_WIDTH)"));
    let environment_layer = source.find(".when_some(environment").unwrap();
    let side_panel_layer = source.find(".when_some(side_panel").unwrap();
    assert!(environment_layer < side_panel_layer);
    assert!(super::environment_panel_fits(1_200., 0.));
    assert!(!super::environment_panel_fits(1_200., 600.));
}

#[test]
fn side_panel_splits_the_current_workspace_and_clamps_its_draggable_width() {
    assert_eq!(super::workspace_width(1_686.), 1_440.);
    assert_eq!(super::side_panel_width_for_workspace(1_440.), Some(720.));
    assert_eq!(super::side_panel_width_for_workspace(1_200.), Some(560.));
    assert_eq!(super::side_panel_width_for_workspace(2_000.), Some(1_000.));
    assert_eq!(super::side_panel_width_for_workspace(899.), None);
    assert_eq!(super::side_panel_max_width(1_200.), Some(560.));
    assert_eq!(
        super::eased_side_panel_width(0., 420., std::time::Duration::ZERO),
        0.
    );
    assert_eq!(
        super::eased_side_panel_width(0., 420., super::SIDE_PANEL_TRANSITION),
        420.
    );
    assert!(super::eased_side_panel_width(0., 420., super::SIDE_PANEL_TRANSITION / 2) > 210.);
    let source = crate::MAIN_SOURCE;
    assert!(source.contains("window.request_animation_frame()"));
    assert!(source.contains("schedule_run_elapsed_tick"));
    assert!(source.contains("side_panel_resize_mouse_move"));
    assert!(source.contains("let side_panel_layout_width = if panel_visible"));
    let panel_flow = source
        .split("fn resize_side_panel(")
        .nth(1)
        .unwrap()
        .split("fn toggle_branch_picker(")
        .next()
        .unwrap();
    assert!(!panel_flow.contains("window.resize"));
}

#[test]
fn chat_column_and_composer_share_synara_width_and_gutters() {
    assert_eq!(super::CHAT_COLUMN_MAX_WIDTH, 736.);
    assert_eq!(super::chat_column_gutter(639.), 12.);
    assert_eq!(super::chat_column_gutter(640.), 20.);
    assert_eq!(super::chat_column_animation_offset(760., 0.), 380.);
    assert_eq!(super::chat_column_animation_offset(760., 760.), 0.);
    let main = crate::MAIN_SOURCE;
    assert!(main.contains(".max_w(px(CHAT_COLUMN_MAX_WIDTH))"));
    assert!(main.contains(".pl(px(column_gutter))"));
    assert!(main.contains(".pr(px(right_panel_width + column_gutter))"));
    let production = main;
    assert_eq!(
        production
            .matches(".left(px(column_animation_offset))")
            .count(),
        2
    );
    let surfaces = crate::SURFACES_SOURCE;
    assert!(surfaces.contains(".px(px(horizontal_gutter))"));
    assert!(surfaces.contains(".max_w(px(CHAT_COLUMN_MAX_WIDTH))"));
}

#[test]
fn security_form_validates_fields_and_only_sends_desktop_owned_keys() {
    let mut draft = json!({"enabled":true,"defaultMode":"standard","publicationTool":"protected","routes":{"auditor":{"model":"keep"}}});
    let values = ["4", "0", "5", "3", "25", "1.5"];
    let payload =
        super::security_settings_payload(&draft, values.into_iter(), Locale::resolve("en"))
            .unwrap();
    assert_eq!(
        payload,
        json!({"enabled":true,"defaultMode":"standard","workers":4,"subagents":0,"stopAfterNoNew":5,"stopAfterConsecutiveErrors":3,"maxDiscoveryRuns":25,"maxTimeHours":1.5})
    );
    draft["enabled"] = json!(false);
    draft["defaultMode"] = json!("deep");
    let changed =
        super::security_settings_payload(&draft, values.into_iter(), Locale::resolve("en"))
            .unwrap();
    assert_eq!(changed["enabled"], false);
    assert_eq!(changed["defaultMode"], "deep");
    for invalid in ["", "NaN", "inf", "0", "33", "1.5"] {
        let mut values = values;
        values[0] = invalid;
        assert!(
            super::security_settings_payload(&draft, values.into_iter(), Locale::resolve("en"))
                .is_err(),
            "accepted {invalid}"
        );
    }
    assert!(
        super::security_settings_payload(&draft, ["4"].into_iter(), Locale::resolve("en")).is_err()
    );
}

use super::{
    ProcessExpansion, SubagentSettingKind, TRANSCRIPT_COMPOSER_CLEARANCE, catalog_model_name,
    composer_has_submission, composer_input_height, composer_text_rows, context_composition,
    decode_window_size, format_context_tokens, humanize_model_id, load_window_size,
    model_capability_hint, reasoning_display_name, reasoning_index_from_position,
    reasoning_offset_from_progress, reasoning_progress_from_position, reasoning_stop_offset,
    runtime_busy, save_window_size, security_scan_target, should_follow_transcript,
    terminal_key_data, transcript_item_count, transcript_layout_changed,
};

#[test]
fn security_scan_list_loads_selected_or_latest_projection() {
    let mut state = crate::state::AppState::default();
    state.security.scans = vec![json!({"id":"latest"}), json!({"id":"older"})];
    assert_eq!(security_scan_target(&state).as_deref(), Some("latest"));

    state.security.projection = json!({"scan":{"id":"older"}});
    assert_eq!(security_scan_target(&state).as_deref(), Some("older"));

    state.security.projection = json!({"scan":{"id":"deleted"}});
    assert_eq!(security_scan_target(&state).as_deref(), Some("latest"));
}

#[test]
fn environment_plan_items_wrap_inside_the_sidebar() {
    let source = crate::SURFACES_SOURCE;
    let start = source.find("todo_items.into_iter().map").unwrap();
    let item = &source[start..source.len().min(start + 2_500)];
    assert!(item.contains(".min_w_0()"));
    assert!(item.contains(".whitespace_normal()"));
}

#[test]
fn empty_terminal_list_starts_the_first_shell() {
    let source = crate::MAIN_SOURCE;
    let start = source.find("PendingRequest::Terminals =>").unwrap();
    let response = &source[start..source.len().min(start + 1_500)];
    assert!(response.contains("request_create_terminal"));
}

#[test]
fn embedded_terminal_accepts_input_in_the_output_surface() {
    let source = crate::MAIN_SOURCE;
    let view = source
        .split_once("fn terminal_view(")
        .unwrap()
        .1
        .split_once("\n    }\n}")
        .unwrap()
        .0;
    assert!(view.contains("capture_key_down(cx.listener(Self::terminal_key))"));
    assert!(view.contains("Hack Nerd Font Mono"));
    assert!(view.contains("cursor_visible"));
    assert!(!view.contains("terminal-send"));
}

#[test]
fn terminal_output_follows_the_latest_line() {
    let source = crate::MAIN_SOURCE;
    assert!(source.contains("terminal_scroll: ScrollHandle"));
    assert!(source.contains(".track_scroll(&self.terminal_scroll)"));
    assert!(source.contains("self.terminal_scroll.scroll_to_bottom()"));
}

#[test]
fn terminal_keys_are_forwarded_as_pty_sequences() {
    for (key, expected) in [
        ("enter", "\r"),
        ("backspace", "\x7f"),
        ("left", "\x1b[D"),
        ("ctrl-c", "\x03"),
        ("shift-tab", "\x1b[Z"),
    ] {
        let keystroke = gpui::Keystroke::parse(key).unwrap();
        assert_eq!(terminal_key_data(&keystroke).as_deref(), Some(expected));
    }
    assert!(terminal_key_data(&gpui::Keystroke::parse("cmd-c").unwrap()).is_none());
}

#[test]
fn approval_button_opens_a_picker_before_changing_permissions() {
    let source = crate::MAIN_SOURCE;
    assert!(
        source.contains("approval_picker_control(self, labels, palette, locale, cx)"),
        "the approval button must open a choice, not cycle permissions on click"
    );
    let toggle = source
        .split_once("fn toggle_approval_picker(")
        .unwrap()
        .1
        .split_once("fn change_approval_mode(")
        .unwrap()
        .0;
    assert!(!toggle.contains("self.runtime.request("));
    let selection = source
        .split_once("fn change_approval_mode(")
        .unwrap()
        .1
        .split_once("fn approval_picker_key(")
        .unwrap()
        .0;
    assert!(selection.contains("self.runtime.request(Method::Execute, action)"));
}

#[test]
fn approval_selection_uses_the_existing_action_without_optimistic_permissions() {
    let mut state = crate::state::AppState::default();
    state.settings.approval_mode = "prompt".into();
    state.navigation.current_session_id = "session-approval".into();
    assert!(super::approval_mode_action(&state, "auto_review", false).is_none());
    state.connection.connected = true;
    for (target, label, description) in super::APPROVAL_MODES {
        state.settings.approval_mode = if target == "prompt" {
            "auto_review"
        } else {
            "prompt"
        }
        .into();
        let previous = state.settings.approval_mode.clone();
        assert_eq!(
            super::approval_mode_action(&state, target, false),
            Some(
                json!({"kind":"set_approval_mode", "target":target, "sessionId":"session-approval"})
            )
        );
        assert_eq!(state.settings.approval_mode, previous);
        for locale in [Locale::resolve("en"), Locale::resolve("zh-CN")] {
            assert_ne!(locale.text(label), label);
            assert_ne!(locale.text(description), description);
        }
    }
    state.settings.approval_mode = "prompt".into();
    assert!(super::approval_mode_action(&state, "prompt", false).is_none());
    assert!(super::approval_mode_action(&state, "invalid", false).is_none());
    assert!(super::approval_mode_action(&state, "auto_review", true).is_none());
    state.runtime.running = true;
    assert!(super::approval_mode_action(&state, "auto_review", false).is_none());
    state.runtime.running = false;
    state.runtime.active_session_id = "session-other".into();
    assert!(super::approval_mode_action(&state, "auto_review", false).is_none());
}

#[test]
fn branch_picker_filters_real_branches_and_has_one_environment_entry() {
    let branches = vec![
        json!({"name":"feature/UI"}),
        json!({"name":"main", "current":true}),
        json!({"name":"feature/api"}),
        json!({"name":""}),
        json!({"name":null}),
    ];
    assert_eq!(
        super::visible_git_branches(&branches, "main", ""),
        ["main", "feature/UI", "feature/api"]
    );
    assert_eq!(
        super::visible_git_branches(&branches, "main", "  ui "),
        ["feature/UI"]
    );
    assert!(super::visible_git_branches(&branches, "main", "missing").is_empty());
    let main_source = crate::MAIN_SOURCE;
    assert_eq!(
        main_source
            .matches("branch_picker_control(self, false, palette, locale, cx)")
            .count(),
        1,
        "new conversations still need a branch choice before the first turn"
    );
    assert!(!main_source.contains("header_project"));
    assert!(!main_source.contains("header_branch"));
    let surfaces_source = crate::SURFACES_SOURCE;
    assert_eq!(
        surfaces_source
            .matches(".child(branch_picker_control(this, true, palette, locale, cx))")
            .count(),
        1,
        "existing conversations must move their branch entry into the environment panel"
    );
    assert!(!surfaces_source.contains("\"environment-branch\",\n            \"git-branch\","));
}

#[test]
fn subagent_controls_use_the_existing_settings_contract() {
    use SubagentSettingKind::*;

    assert_eq!(Concurrency.action(), "set_subagent_concurrency");
    assert_eq!(ShellConcurrency.action(), "set_shell_concurrency");
    assert_eq!(Depth.menu_values(), &[-1, 0, 1, 2, 3]);
    assert_eq!(AwaitTimeout.menu_values(), &[0, 30, 60, 300, 600, 1800]);
    assert_eq!(
        IdleTimeout.menu_values(),
        &[0, 60, 120, 300, 600, 900, 1800]
    );
}

#[test]
fn process_expansion_toggles_explicit_rows() {
    let mut expansion = ProcessExpansion::default();
    expansion.toggle("run-1");
    assert!(expansion.is_expanded("run-1"));
    expansion.toggle("run-1");
    assert!(!expansion.is_expanded("run-1"));
}

#[test]
fn running_process_starts_collapsed_and_rolls_activity_labels() {
    let mut expansion = ProcessExpansion::default();
    assert!(!expansion.is_expanded("run"));
    assert_eq!(expansion.activity_transition("run", "first"), (None, 0));
    assert_eq!(
        expansion.activity_transition("run", "second"),
        (Some("first".to_string()), 1)
    );
    assert_eq!(
        expansion.activity_transition("run", "second"),
        (Some("first".to_string()), 1)
    );
}

#[test]
fn queued_messages_reorder_only_inside_their_session() {
    let queued = |id: &str, session: &str| super::QueuedPrompt {
        id: id.to_string(),
        session_id: session.to_string(),
        prompt: id.to_string(),
        selected_skills: Vec::new(),
        attachments: Vec::new(),
        failed: false,
    };
    let mut prompts = vec![
        queued("a", "one"),
        queued("x", "two"),
        queued("b", "one"),
        queued("c", "one"),
    ];

    assert!(super::reorder_session_queue(&mut prompts, "one", "c", "a"));
    assert_eq!(
        prompts
            .iter()
            .map(|item| item.id.as_str())
            .collect::<Vec<_>>(),
        vec!["c", "x", "a", "b"]
    );
    assert!(!super::reorder_session_queue(&mut prompts, "one", "x", "a"));
}

#[test]
fn composer_grows_for_hard_and_soft_wrapped_lines() {
    assert_eq!(composer_text_rows("short"), 1);
    assert_eq!(composer_text_rows("first\nsecond\nthird"), 3);
    assert_eq!(composer_text_rows(&"x".repeat(177)), 3);
    assert_eq!(composer_text_rows(&"界".repeat(400)), 7);
}

#[test]
fn image_attachments_expand_the_composer_input() {
    assert_eq!(composer_input_height(1, false, 0), 60.);
    assert_eq!(composer_input_height(1, false, 1), 72.);
    assert_eq!(composer_input_height(1, true, 1), 112.);
}

#[test]
fn transcript_reserves_codex_composer_clearance() {
    assert_eq!(transcript_item_count(4, false), 5);
    assert_eq!(transcript_item_count(4, true), 6);
    assert_eq!(TRANSCRIPT_COMPOSER_CLEARANCE, 64.);
}

#[test]
fn completed_process_replaces_pending_row_even_when_item_count_is_unchanged() {
    use gpui::{FollowMode, ListAlignment, ListState, px};

    let old_item_count = transcript_item_count(4, true);
    let new_item_count = transcript_item_count(5, false);

    assert_eq!(old_item_count, new_item_count);
    assert!(transcript_layout_changed(
        old_item_count,
        new_item_count,
        true,
        false,
    ));

    let list = ListState::new(old_item_count, ListAlignment::Top, px(800.));
    list.set_follow_mode(FollowMode::Tail);
    let changed_from = old_item_count - 1;
    list.splice(changed_from..old_item_count, new_item_count - changed_from);
    assert_eq!(list.item_count(), new_item_count);
    assert!(list.is_following_tail());
    assert_eq!(list.logical_scroll_top().item_ix, new_item_count);
}

#[test]
fn queue_waits_for_current_or_foreign_runtime() {
    let mut state = super::AppState::default();
    assert!(!runtime_busy(&state));
    state.runtime.running = true;
    assert!(runtime_busy(&state));
    state.runtime.running = false;
    state.runtime.active_session_id = "another-session".into();
    assert!(runtime_busy(&state));
}

#[test]
fn image_only_submission_is_allowed() {
    assert!(composer_has_submission("", &[json!({"id": "image-1"})]));
    assert!(!composer_has_submission("", &[]));
}

#[test]
fn model_picker_uses_product_names_and_capability_copy() {
    let model = json!({
        "id": "stealth/ox-alpha",
        "name": "stealth/ox-alpha",
        "displayName": "Ox Alpha",
        "reasoningLevels": ["low", "max"],
        "capabilities": ["tools", "structured-output"],
        "inputModalities": ["image"]
    });
    assert_eq!(catalog_model_name(&model, "stealth/ox-alpha"), "Ox Alpha");
    assert_eq!(humanize_model_id("stealth/ox-alpha"), "Ox Alpha");
    assert_eq!(
        model_capability_hint(
            &model,
            "stealth/ox-alpha",
            "Ox Alpha",
            Locale::resolve("zh-CN")
        ),
        "推理 · 图像 · 工具 · 结构化"
    );
    assert_eq!(
        reasoning_display_name("max", Locale::resolve("zh-CN")),
        "最高"
    );
}

#[test]
fn closed_model_picker_does_not_build_the_catalog() {
    let composer = crate::MAIN_SOURCE
        .split("fn composer_view(")
        .nth(1)
        .unwrap()
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    assert!(composer.contains(
            "(self.model_picker_open && self.route_picker_target.is_none())\n            .then(|| self.model_picker_view(palette, cx))"
        ));
    assert!(composer.contains(".when_some(picker, |shell, picker| shell.child(picker))"));
}

#[test]
fn model_picker_blocks_scroll_from_reaching_the_background() {
    let picker = crate::MAIN_SOURCE
        .split("fn model_picker_view(")
        .nth(1)
        .unwrap()
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    for id in [
        "model-picker",
        "route-model-picker",
        "route-reasoning-picker",
    ] {
        let marker = format!(".id(\"{id}\")");
        let shell = picker
            .split(&marker)
            .nth(1)
            .unwrap()
            .split(".child(")
            .next()
            .unwrap();
        assert!(
            shell.contains(".occlude()"),
            "{id} must own its scroll hitbox"
        );
    }
    assert!(
        picker.contains(".overflow_y_scroll()"),
        "its model list must still scroll"
    );
}

#[test]
fn reasoning_drag_previews_without_saving_until_release() {
    let source = crate::MAIN_SOURCE;
    let pointer_update = source
        .split("fn update_reasoning_from_pointer(")
        .nth(1)
        .unwrap()
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    assert!(
        !pointer_update.contains("set_reasoning("),
        "pointer movement must not persist a discrete reasoning tier"
    );
    let release = source
        .split("fn reasoning_mouse_up(")
        .nth(1)
        .expect("drag must have an explicit release handler")
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    assert!(release.contains(".take()"), "a drag can only commit once");
    assert!(release.contains("set_reasoning("));
    assert!(source.contains("phase == gpui::DispatchPhase::Capture"));
}

#[test]
fn reasoning_slider_uses_shared_inset_geometry() {
    assert_eq!(reasoning_stop_offset(304., 0, 3), 18.);
    assert_eq!(reasoning_stop_offset(304., 1, 3), 152.);
    assert_eq!(reasoning_stop_offset(304., 2, 3), 286.);
    assert_eq!(reasoning_index_from_position(40., 40., 304., 3), 0);
    assert_eq!(reasoning_index_from_position(192., 40., 304., 3), 1);
    assert_eq!(reasoning_index_from_position(344., 40., 304., 3), 2);
    assert_eq!(reasoning_stop_offset(304., 0, 1), 286.);
    // The thumb follows every pixel even when the nearest discrete tier is unchanged.
    for offset in 18..=286 {
        let pointer = 40. + offset as f32;
        let progress = reasoning_progress_from_position(pointer, 40., 304.);
        let preview = reasoning_offset_from_progress(304., progress);
        assert!((preview - offset as f32).abs() < 0.001);
        for count in [4, 5, 8] {
            let index = reasoning_index_from_position(pointer, 40., 304., count);
            assert!(index < count);
            let snapped = reasoning_stop_offset(304., index, count);
            assert!((snapped - preview).abs() <= 268. / (count - 1) as f32 / 2. + 0.001);
        }
    }
    assert_eq!(reasoning_progress_from_position(-100., 40., 304.), 0.);
    assert_eq!(reasoning_progress_from_position(900., 40., 304.), 1.);
}

#[test]
fn every_reasoning_depth_has_a_distinct_localized_name() {
    let levels = [
        "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra",
    ];
    for (language, expected) in [
        (
            "en",
            [
                "Off",
                "Minimal",
                "Low",
                "Medium",
                "High",
                "Very high",
                "Max",
                "Ultra",
            ],
        ),
        (
            "zh-CN",
            ["关闭", "最低", "低", "中", "高", "极高", "最高", "极限"],
        ),
    ] {
        let labels = levels.map(|level| reasoning_display_name(level, Locale::resolve(language)));
        assert_eq!(labels, expected, "{language}");
    }
}

#[test]
fn fast_lightning_stays_in_the_reasoning_heading_without_a_separate_row() {
    let picker = crate::MAIN_SOURCE
        .split("fn model_picker_view(")
        .nth(1)
        .unwrap()
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    let heading = picker.find(".id(\"model-reasoning-heading\")").unwrap();
    let toggle = picker.find(".id(\"model-fast-toggle\")").unwrap();
    let slider = picker.find(".on_children_prepainted(").unwrap();
    assert!(heading < toggle && toggle < slider);
    assert_eq!(picker.matches(".id(\"model-fast-toggle\")").count(), 1);
    assert!(picker.contains(".tooltip("));
    assert!(picker.contains("model.fastDescription"));
    assert!(!picker.contains(".child(locale.text(\"model.fast\"))"));
}

#[test]
fn all_reasoning_models_share_one_slider_style() {
    let picker = crate::MAIN_SOURCE
        .split("fn model_picker_view(")
        .nth(1)
        .unwrap()
        .split("\n    pub(super) fn ")
        .next()
        .unwrap();
    assert!(picker.contains("let fast_supported = modes.fast_available || modes.fast;"));
    assert_eq!(picker.matches("if modes.fast {").count(), 1);
    assert_eq!(picker.matches("\"lightning-filled\"").count(), 1);
    assert_eq!(picker.matches("\"lightning\"").count(), 1);
    assert!(picker.contains("(\"lightning-filled\", palette.accent)"));
    assert_eq!(picker.matches(".bg(rgb(0x319aff))").count(), 1);
    let ticks = picker.find(".children(reasoning_ticks)").unwrap();
    let fast_particles = picker.find(".when(modes.fast, |track|").unwrap();
    assert!(ticks < fast_particles);
    assert!(picker.contains("track.child(self.fast_particles.clone())"));
    assert!(!picker.contains("rgb(0x7055d9)"));

    let particles = include_str!("fast_particles.rs")
        .split("#[cfg(test)]")
        .next()
        .unwrap();
    assert!(!particles.contains(".when_some(\n            self.started"));
    assert!(
        particles
            .contains("self.phase = (self.phase + particle_phase(started.elapsed())).fract();")
    );
    assert!(particles.contains(".unwrap_or(self.phase);"));
}

#[test]
fn context_composition_groups_real_profile_and_usage_tokens() {
    let profile = json!({"contributions": [
        {"category": "core", "name": "system", "tokens": 4000},
        {"category": "builtin_tools", "name": "shell", "tokens": 2000},
        {"category": "conversation", "name": "history", "tokens": 2500}
    ]});
    let usage = json!({
        "inputTokens": 8500,
        "outputTokens": 1500,
        "contextLimit": 20000,
        "currentTurnMainReported": true,
        "currentTurnMainReportedInput": 8000,
        "currentTurnMainCached": 6000
    });
    let composition = context_composition(&profile, &usage, 0, Locale::resolve("zh-CN"));
    assert_eq!(composition.used, 10_000);
    assert_eq!(composition.limit, 20_000);
    assert_eq!(composition.cache_hit_rate, Some(75));
    assert_eq!(composition.segments[0].label, "核心指令");
    assert_eq!(composition.segments[3].category, "current_output");
    assert_eq!(format_context_tokens(1_048_600), "1048.6k");

    let popover = crate::MAIN_SOURCE
        .split("fn context_popover_view(")
        .nth(1)
        .unwrap()
        .split("fn composer_view(")
        .next()
        .unwrap();
    assert_eq!(popover.matches(".whitespace_nowrap()").count(), 5);
    assert!(popover.contains(".min_w(px(40.))"));
    assert!(popover.contains(".flex_shrink_0()"));
    assert!(popover.contains("locale.text(\"ui.cacheHitRate\")"));
    assert!(!popover.contains("mainCachedInput"));
    let divider = popover.find(".child(div().h(px(1.))").unwrap();
    let cache = popover.find("locale.text(\"ui.cacheHitRate\")").unwrap();
    let total = popover.find("locale.text(\"ui.total\")").unwrap();
    assert!(divider < cache);
    assert!(cache < total);

    let unreported = context_composition(&profile, &json!({}), 20_000, Locale::resolve("zh-CN"));
    assert_eq!(unreported.cache_hit_rate, None);
}

#[test]
fn session_switch_and_live_messages_follow_transcript_bottom() {
    assert!(should_follow_transcript("", "session-1", 0, 3));
    assert!(should_follow_transcript("session-1", "session-2", 3, 5));
    assert!(should_follow_transcript("session-1", "session-1", 3, 4));
    assert!(!should_follow_transcript("session-1", "session-2", 3, 0));
    assert!(!should_follow_transcript("session-1", "session-1", 4, 4));
}

#[test]
fn persisted_window_size_rejects_invalid_or_too_small_values() {
    let restored = decode_window_size(r#"{"width":1600,"height":980}"#).unwrap();
    assert_eq!(f32::from(restored.width), 1600.);
    assert_eq!(f32::from(restored.height), 980.);
    assert!(decode_window_size(r#"{"width":700,"height":500}"#).is_none());
    assert!(decode_window_size("not json").is_none());
}

#[test]
fn window_size_persistence_round_trips_on_disk() {
    let path = std::env::temp_dir().join(format!("azem-window-{}.json", Uuid::new_v4()));
    save_window_size(&path, gpui::size(gpui::px(1536.), gpui::px(960.))).unwrap();
    let restored = load_window_size(&path).unwrap();
    assert_eq!(f32::from(restored.width), 1536.);
    assert_eq!(f32::from(restored.height), 960.);
    std::fs::remove_file(path).unwrap();
}

#[test]
fn pending_question_replaces_running_header_status() {
    let mut state = crate::state::AppState::default();
    state.runtime.running = true;
    state.navigation.current_session_id = "session-1".into();
    state.runtime.questions = vec![json!({
        "sessionId": "session-1",
        "runId": "run-1",
        "state": "interrupted",
    })];
    assert_eq!(
        crate::window_render::thread_status(&state),
        ("ui.awaitingInput", false)
    );
    state.runtime.running = false;
    assert_eq!(
        crate::window_render::thread_status(&state),
        ("ui.awaitingInput", false)
    );
    assert_eq!(
        crate::stoppable_run(&state),
        Some(("session-1".to_string(), "run-1".to_string()))
    );
    state.runtime.activity = "stopping".into();
    assert_eq!(
        crate::window_render::thread_status(&state),
        ("ui.stopping", false)
    );
    state.runtime.activity = "idle".into();
    state.runtime.running = true;

    state.runtime.questions[0]["sessionId"] = json!("other-session");
    assert_eq!(
        crate::window_render::thread_status(&state),
        ("ui.running2", true)
    );
    state.runtime.running = false;
    assert_eq!(
        crate::window_render::thread_status(&state),
        ("ui.ready", false)
    );
    let cancel = crate::MAIN_SOURCE
        .split("fn cancel_active(")
        .nth(1)
        .unwrap()
        .split("pub(super) fn new_session")
        .next()
        .unwrap();
    assert!(cancel.contains("\"sessionId\": session_id"));
    assert!(cancel.contains("\"runId\": run_id"));
    assert!(cancel.contains("PendingRequest::CancelActive"));
}
