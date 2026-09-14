use azem_ipc::{BinaryMetadata, Envelope};
use serde_json::json;

use super::{AppState, PullRequestModel, PullRequestTab, SessionSummary, unix_millis};

#[test]
fn pull_request_tabs_use_their_own_dashboard_rows() {
    let mut model = PullRequestModel {
        dashboard: json!({
            "current": {"number": 7},
            "createdByViewer": [{"number": 8}],
            "open": [{"number": 7}, {"number": 9}]
        }),
        ..Default::default()
    };
    assert_eq!(model.tab, PullRequestTab::Open);
    for (tab, expected) in [
        (PullRequestTab::Current, vec![7]),
        (PullRequestTab::Created, vec![8]),
        (PullRequestTab::Open, vec![7, 9]),
    ] {
        model.tab = tab;
        let numbers: Vec<_> = model
            .tab
            .rows(&model.dashboard)
            .iter()
            .map(|row| row["number"].as_i64().unwrap())
            .collect();
        assert_eq!(numbers, expected);
        assert!(tab.rows(&serde_json::Value::Null).is_empty());
        assert!(
            tab.rows(&json!({"current": null, "createdByViewer": [], "open": []}))
                .is_empty()
        );
    }
    model.dashboard = json!({"open": [{"number": 10}]});
    assert_eq!(model.tab, PullRequestTab::Open);
    assert_eq!(model.tab.rows(&model.dashboard)[0]["number"], 10);
}

#[test]
fn extension_snapshots_survive_activity_and_failed_refreshes() {
    let mut state = AppState::default();
    let servers = json!([{"name":"local-tools","enabled":true,"toolCount":3}]);
    state.apply_direct_event(json!({"kind":"mcp_state","state":"snapshot",
            "data":{"servers":servers.to_string()}}));
    assert_eq!(state.catalogs.mcp, servers);
    state.apply_direct_event(json!({"kind":"mcp_state","state":"prompt",
            "data":{"server":"local-tools","messages":"[]"}}));
    assert_eq!(state.catalogs.mcp, servers);
    state.apply_direct_event(json!({"kind":"mcp_state","state":"snapshot",
            "data":{"servers":"not json"}}));
    assert_eq!(state.catalogs.mcp, servers);
    assert!(!state.settings.error.is_empty());

    let hooks = json!({"enabled":true,"trustHooks":false,
            "sources":[{"name":"project"}],"commands":[{"id":"hook-1","enabled":true}]});
    state.apply_direct_event(json!({"kind":"hook_catalog","hookCatalog":hooks}));
    for kind in ["hook_started", "hook_finished", "hook_diagnostic"] {
        state.apply_direct_event(json!({"kind":kind,"runId":"run-1","text":"hook activity"}));
        assert_eq!(state.catalogs.hooks, hooks);
    }
    assert_eq!(state.runtime.hooks.len(), 2);
    assert_eq!(state.runtime.hooks[0]["runId"], "run-1");

    let market = json!({"marketplaces":[{"name":"local"}],"available":[],
            "installed":[{"id":"example@local","scope":"project"}]});
    state.apply_direct_event(json!({"kind":"marketplace_catalog","marketplaceCatalog":market}));
    state.apply_direct_event(json!({"kind":"marketplace_catalog","state":"update_error",
            "text":"marketplace unavailable"}));
    assert_eq!(state.catalogs.marketplace, market);
    assert_eq!(state.settings.error.as_ref(), "marketplace unavailable");
    state.apply_direct_event(json!({"kind":"mcp_state","state":"snapshot",
            "data":{"servers":"[]"}}));
    assert_eq!(state.catalogs.mcp, json!([]));
}

#[test]
fn optimistic_user_message_precedes_streamed_reply() {
    let mut state = AppState::default();
    state.append_optimistic_user(
        "request-1",
        "检查当前界面".to_string(),
        vec![json!({"id": "image-1"})],
    );
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].id.as_ref(), "user-request-1");
    assert_eq!(blocks[0].kind.as_ref(), "user");
    assert_eq!(blocks[0].content, "检查当前界面");
    assert_eq!(blocks[0].state.as_ref(), "submitted");
    assert_eq!(blocks[0].extra["attachments"][0]["id"], "image-1");
    assert!(!blocks[0].extra["createdAt"].as_str().unwrap().is_empty());
    assert!(state.runtime.running);
    assert_eq!(state.runtime.activity.as_ref(), "waiting_model");
}

#[test]
fn empty_project_snapshot_clears_the_previous_conversation() {
    for selected in ["", "startup-session"] {
        let mut state = AppState::default();
        state.workspace.root = "/old".into();
        state.navigation.current_session_id = "old-session".into();
        state.navigation.current_title = "Old conversation".into();
        state.append_optimistic_user("old", "Old message".into(), vec![]);
        state.runtime.agents.push(json!({"id":"old-agent"}));
        state.runtime.todo = json!({"items":[{"content":"old task"}]});
        state.apply_reconnect_snapshot(json!({
        "base":{"workspace":"/new","sessionId":"","language":"zh-CN","model":"new-model"},
        "selectedSessionId":selected, "session":null, "sessions":[], "projects":[{"path":"/new"}],
        "runs":[], "controls":[]
    }));
        assert_eq!(state.workspace.root.as_ref(), "/new");
        assert_eq!(state.navigation.current_session_id.as_ref(), selected);
        assert!(state.navigation.current_title.is_empty());
        assert!(state.transcript.blocks.borrow().is_empty());
        assert!(state.transcript.index_by_id.is_empty());
        assert!(state.runtime.agents.is_empty());
        assert!(state.runtime.todo.is_null());
        assert!(!state.runtime.running);
        assert_eq!(state.settings.model.as_ref(), "new-model");
    }
}

#[test]
fn archive_selection_uses_the_next_session_in_the_same_project() {
    let mut state = AppState::default();
    state.workspace.root = "/project".into();
    state.navigation.current_session_id = "current".into();
    state.navigation.sessions = serde_json::from_value(json!([
        {"id":"previous","workspace":"/project"},
        {"id":"current","workspace":"/project","archived":true},
        {"id":"archived","workspace":"/project","archived":true},
        {"id":"foreign","workspace":"/other"},
        {"id":"next","workspace":"/project"}
    ]))
    .unwrap();
    assert_eq!(
        state.next_session_after_archive().unwrap().id.as_ref(),
        "next"
    );
    state.navigation.sessions.pop();
    assert_eq!(
        state.next_session_after_archive().unwrap().id.as_ref(),
        "previous"
    );
    state.navigation.sessions[0].archived = true;
    assert!(state.next_session_after_archive().is_none());
}

#[test]
fn reconnect_snapshot_restores_active_session_and_catalogs() {
    let mut state = AppState::default();
    state.apply_reconnect_snapshot(json!({
        "base": {
            "workspace": "/workspace",
            "currentBranch": "main",
            "sessionId": "session-1",
            "language": "en",
            "provider": "chatgpt",
            "model": "model",
            "reasoning": "high",
            "agentMode": "single",
            "approvalMode": "prompt",
            "queueMode": "queue",
            "subagentConcurrency": 6,
            "subagentMaxDepth": 2,
            "shellConcurrency": 4,
            "shellMaxWallClockSeconds": 600,
            "subagentAwaitSeconds": 0,
            "subagentIdleSeconds": 300
        },
        "session": {
            "kind": "session_loaded",
            "sessionId": "session-1",
            "state": "refreshed",
            "data": {
                "title": "Session",
                "active": "true",
                "activeRunID": "run-1",
                "blocks": r#"[{"id":"block-1","kind":"user","content":"hello"}]"#
            }
        },
        "sessions": [{
            "id": "session-1",
            "title": "Session",
            "workspace": "/workspace",
            "updatedAt": "2026-08-25T00:00:00Z"
        }],
        "projects": [{
            "workspace": "/workspace",
            "updatedAt": "2026-08-25T00:00:00Z"
        }],
        "skills": {"entries": [{"name": "check"}]},
        "terminals": [{"id": "terminal-1"}],
        "pendingControls": [{
            "kind": "approval_requested",
            "sessionId": "session-1",
            "runId": "run-1",
            "approvalId": "approval-1",
            "state": "pending"
        }],
    }));
    assert_eq!(state.workspace.root.as_ref(), "/workspace");
    assert_eq!(state.navigation.current_title.as_ref(), "Session");
    assert!(state.runtime.running);
    assert_eq!(state.runtime.run_id.as_ref(), "run-1");
    assert_eq!(state.transcript.blocks.borrow().len(), 1);
    assert_eq!(state.catalogs.skills.len(), 1);
    assert_eq!(state.terminals.sessions.len(), 1);
    assert_eq!(state.navigation.sessions.len(), 1);
    assert_eq!(state.navigation.sessions[0].title.as_ref(), "Session");
    assert_eq!(state.navigation.projects.len(), 1);
    assert_eq!(state.navigation.projects[0].path.as_ref(), "/workspace");
    assert_eq!(state.runtime.approvals.len(), 1);
    assert_eq!(state.settings.subagent_concurrency, 6);
    assert_eq!(state.settings.subagent_max_depth, 2);
    assert_eq!(state.settings.shell_concurrency, 4);
    assert_eq!(state.settings.subagent_idle_seconds, 300);
}

#[test]
fn nullable_bridge_collections_do_not_drop_model_provider_events() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
        "kind": "model_providers",
        "agentSnapshots": null,
        "skillCatalog": null,
        "modelProviders": [{
            "id": "openrouter",
            "displayName": "OpenRouter",
            "enabled": true,
            "models": [{"id": "openai/gpt-test"}]
        }]
    }));
    assert_eq!(state.catalogs.providers.len(), 1);
    assert_eq!(
        state.catalogs.providers[0]
            .get("id")
            .and_then(serde_json::Value::as_str),
        Some("openrouter")
    );
}

#[test]
fn model_catalog_replaces_matching_provider_models() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
        "kind": "model_providers",
        "modelProviders": [{
            "id": "chatgpt",
            "displayName": "OpenAI / ChatGPT",
            "subscription": true,
            "enabled": true,
            "models": [{"id": "gpt-old", "name": "GPT Old"}]
        }]
    }));
    state.apply_direct_event(json!({
        "kind": "model_catalog",
        "data": {
            "provider": "chatgpt",
            "accountID": "acct",
            "models": "[{\"id\":\"gpt-new\",\"name\":\"GPT New\"}]"
        }
    }));
    assert_eq!(state.catalogs.models[0]["id"], "gpt-new");
    assert_eq!(state.catalogs.providers[0]["models"][0]["id"], "gpt-new");
    assert_eq!(state.catalogs.providers[0]["models"][0]["name"], "GPT New");
}

#[test]
fn catalog_refresh_keeps_capabilities_until_they_actually_change() {
    let mut state = AppState::default();
    let capabilities = json!([
        "tools",
        "parallel-tools",
        "reasoning",
        "structured-output",
        "fast"
    ]);
    let displayed = json!({
        "id": "gpt-test", "name": "GPT Test", "disabled": true,
        "capabilities": capabilities, "inputModalities": ["text", "image"]
    });
    state.apply_direct_event(json!({
        "kind": "model_providers", "modelProviders": [{"id":"chatgpt", "models":[displayed]}]
    }));
    let mut raw = json!({
        "id": "gpt-test", "name": "GPT Test", "disabled": true,
        "supportsTools": true, "supportsParallel": true, "supportsReasoning": true,
        "supportsStructured": true, "serviceTiers":[{"id":"priority"}],
        "inputModalities":["text", "image"]
    });
    for speed_field in ["serviceTiers", "additionalSpeedTiers"] {
        if speed_field == "additionalSpeedTiers" {
            raw.as_object_mut().unwrap().remove("serviceTiers");
            raw["additionalSpeedTiers"] = json!(["fast"]);
        }
        state.apply_direct_event(json!({
            "kind":"model_catalog", "data":{"provider":"chatgpt", "models":json!([raw]).to_string()}
        }));
        let model = &state.catalogs.providers[0]["models"][0];
        for key in ["id", "name", "disabled", "capabilities", "inputModalities"] {
            assert_eq!(model[key], displayed[key], "refresh changed {key}");
        }
    }
    raw["supportsParallel"] = false.into();
    raw.as_object_mut().unwrap().remove("additionalSpeedTiers");
    state.apply_direct_event(json!({
        "kind":"model_catalog", "data":{"provider":"chatgpt", "models":json!([raw]).to_string()}
    }));
    assert_eq!(
        state.catalogs.providers[0]["models"][0]["capabilities"],
        json!(["tools", "reasoning", "structured-output"])
    );
}

#[test]
fn reconnect_without_active_run_clears_stale_runtime_state() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-1".into();
    state.runtime.running = true;
    state.runtime.run_id = "stale-run".into();
    state.runtime.activity = "running".into();
    state.apply_reconnect_snapshot(json!({
        "base": {"sessionId": "session-1"},
        "sessions": [{"id": "session-1", "title": "One"}],
        "projects": [],
        "activeSessionId": "",
        "activeRunId": "",
        "terminals": []
    }));
    assert!(!state.runtime.running);
    assert!(state.runtime.run_id.is_empty());
    assert!(state.runtime.activity.is_empty());
    assert!(!state.navigation.sessions[0].running);
}

#[test]
fn run_failure_is_visible_immediately() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-1".into();
    state.runtime.running = true;
    state.runtime.run_id = "run-1".into();
    state.apply_direct_event(json!({
        "sequence": 42,
        "kind": "run_failed",
        "sessionId": "session-1",
        "runId": "run-1",
        "state": "failed",
        "text": "provider unavailable"
    }));

    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].kind.as_ref(), "error");
    assert_eq!(blocks[0].run_id.as_ref(), "run-1");
    assert_eq!(blocks[0].content, "provider unavailable");
    assert_eq!(blocks[0].state.as_ref(), "failed");
    assert!(!state.runtime.running);
}

#[test]
fn turn_response_failure_is_visible_without_duplicate_terminal_event() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-1".into();
    state.runtime.running = true;
    state.append_request_error("request-1", "provider unavailable".to_string());
    state.apply_direct_event(json!({
        "sequence": 43,
        "kind": "run_failed",
        "sessionId": "session-1",
        "runId": "run-1",
        "state": "failed",
        "text": "provider unavailable"
    }));

    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].kind.as_ref(), "error");
    assert_eq!(blocks[0].content, "provider unavailable");
    assert!(!state.runtime.running);
}

#[test]
fn session_load_restores_preferences_and_durable_tools_in_order() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
            "kind": "session_loaded",
            "sessionId": "session-1",
            "state": "loaded",
            "data": {
                "title": "Restored",
                "provider": "grok",
                "model": "grok-4.20",
                "reasoning": "high",
                "agentMode": "team",

                "blocks": "[{\"id\":\"u1\",\"kind\":\"user\",\"content\":\"ask\"},{\"id\":\"a1\",\"kind\":\"assistant\",\"content\":\"done\"}]",
                "blockSequences": "[1,3]",
                "toolRecords": "[{\"runId\":\"run-1\",\"toolCallId\":\"tool-1\",\"anchorSequence\":1,\"name\":\"coding.read_file\",\"state\":\"completed\",\"content\":\"ok\"}]"
            }
        }));
    assert_eq!(state.settings.provider.as_ref(), "grok");
    assert_eq!(state.settings.model.as_ref(), "grok-4.20");
    assert_eq!(state.settings.reasoning.as_ref(), "high");
    assert_eq!(state.settings.agent_mode.as_ref(), "team");
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(
        blocks
            .iter()
            .map(|block| block.kind.as_ref())
            .collect::<Vec<_>>(),
        ["user", "tool", "assistant"]
    );
    assert_eq!(blocks[1].tool_call_id.as_ref(), "tool-1");
}

#[test]
fn duplicate_resume_broadcast_skips_identical_transcript_reparse() {
    let mut state = AppState::default();
    let payload = json!({
        "kind": "session_loaded",
        "sessionId": "session-1",
        "state": "loaded",
        "data": {
            "title": "Restored",
            "lastRunID": "run-1",
            "blocks": "[{\"id\":\"u1\",\"kind\":\"user\",\"content\":\"ask\"}]",
            "blockSequences": "[1]"
        }
    });
    state.apply_direct_event(payload.clone());
    assert_eq!(state.navigation.current_session_id.as_ref(), "session-1");
    assert!(!state.runtime.resume_fingerprint.is_empty());
    state.transcript.blocks.borrow_mut()[0].content = "mutated-locally".into();
    state.apply_direct_event(payload);
    assert_eq!(
        state.transcript.blocks.borrow()[0].content,
        "mutated-locally"
    );
}

#[test]
fn session_load_replaces_stale_live_tool_with_durable_terminal_record() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
            "kind": "session_loaded",
            "sessionId": "session-1",
            "state": "loaded",
            "data": {
                "blocks": "[{\"id\":\"tool-1\",\"kind\":\"tool\",\"runId\":\"run-1\",\"toolCallId\":\"tool-1\",\"state\":\"running\"}]",
                "blockSequences": "[2]",
                "toolRecords": "[{\"runId\":\"run-1\",\"toolCallId\":\"tool-1\",\"anchorSequence\":2,\"name\":\"subagent.spawn\",\"state\":\"completed\"}]"
            }
        }));
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].state.as_ref(), "completed");
    assert!(!state.runtime.running);
}

#[test]
fn context_usage_is_session_scoped_and_does_not_replace_usage_history() {
    let mut state = AppState::default();
    state.settings.usage = json!({"rows": ["history"]});
    state.runtime.context_profile = json!({"contributions": [{"tokens": 1}]});
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-1",
        "data": {
            "blocks": "[]",
            "usage": "{\"inputTokens\":8500,\"outputTokens\":1500,\"contextLimit\":20000}"
        }
    }));
    assert_eq!(state.runtime.context_usage["inputTokens"], 8500);
    assert!(state.runtime.context_profile.is_null());
    state.apply_direct_event(json!({
        "kind": "context_usage",
        "sessionId": "session-1",
        "state": "reported",
        "data": {"inputTokens": "9000", "outputTokens": "1600", "contextLimit": "20000"}
    }));
    assert_eq!(state.runtime.context_usage["outputTokens"], 1600);
    assert_eq!(state.settings.usage, json!({"rows": ["history"]}));
}

#[test]
fn automatic_compaction_status_exists_only_while_compacting() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-1".into();
    state.apply_direct_event(json!({
        "kind": "context_usage",
        "sessionId": "session-1",
        "runId": "run-1",
        "state": "compacting",
        "data": {"requestKind": "compaction"}
    }));
    assert!(state.transcript.blocks.borrow().iter().any(|block| {
        block.kind.as_ref() == "context_compaction" && block.run_id.as_ref() == "run-1"
    }));

    state.apply_direct_event(json!({
        "kind": "context_usage",
        "sessionId": "session-1",
        "runId": "run-1",
        "state": "compacted",
        "data": {"requestKind": "compaction"}
    }));
    assert!(state.transcript.blocks.borrow().is_empty());
}

#[test]
fn session_load_keeps_catalog_title_when_projection_title_is_empty() {
    let mut state = AppState::default();
    state.navigation.sessions.push(SessionSummary {
        id: "session-1".into(),
        title: "Saved title".into(),
        ..SessionSummary::default()
    });
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-1",
        "state": "loaded",
        "data": {"title": "", "blocks": "[]"}
    }));
    assert_eq!(state.navigation.current_title.as_ref(), "Saved title");
}

#[test]
fn session_load_keeps_only_matching_pending_controls() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-a".into();
    state.apply_direct_event(json!({
        "kind": "approval_requested",
        "sessionId": "session-a",
        "approvalId": "approval-a"
    }));
    state.apply_direct_event(json!({
        "kind": "approval_requested",
        "sessionId": "session-b",
        "approvalId": "approval-b"
    }));
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-b",
        "state": "loaded",
        "data": {"blocks": "[]"}
    }));
    assert!(state.runtime.approvals.is_empty());

    state.navigation.current_session_id = "".into();
    state.apply_direct_event(json!({
        "kind": "approval_requested",
        "sessionId": "session-b",
        "approvalId": "approval-b"
    }));
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-b",
        "state": "loaded",
        "data": {"blocks": "[]"}
    }));
    assert_eq!(state.runtime.approvals.len(), 1);
    assert_eq!(
        state.runtime.approvals[0]
            .get("approvalId")
            .and_then(serde_json::Value::as_str),
        Some("approval-b")
    );
}

#[test]
fn foreign_session_events_do_not_contaminate_visible_transcript() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "visible".into();
    state.navigation.sessions = vec![
        super::SessionSummary {
            id: "visible".into(),
            ..Default::default()
        },
        super::SessionSummary {
            id: "background".into(),
            ..Default::default()
        },
    ];
    state.apply_direct_event(json!({
        "kind": "text_delta",
        "sessionId": "background",
        "runId": "run-bg",
        "text": "foreign"
    }));
    assert!(state.transcript.blocks.borrow().is_empty());
    state.apply_direct_event(json!({
        "kind": "text_delta",
        "sessionId": "visible",
        "runId": "run-child",
        "agentId": "agent-1",
        "text": "child"
    }));
    assert!(state.transcript.blocks.borrow().is_empty());
    assert_eq!(state.runtime.agent_blocks.len(), 1);
    state.apply_direct_event(json!({
        "kind": "run_started",

        "sessionId": "background",
        "runId": "run-bg"
    }));
    assert!(state.navigation.sessions[1].running);
    assert!(!state.runtime.running);
    assert_eq!(state.runtime.active_session_id.as_ref(), "background");
    state.apply_direct_event(json!({
        "kind": "run_finished",
        "sessionId": "background",
        "runId": "run-bg"
    }));
    assert!(!state.navigation.sessions[1].running);
    assert!(state.navigation.sessions[1].unread);
    assert!(state.runtime.active_session_id.is_empty());
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "background",
        "state": "loaded",
        "data": {"blocks": "[]"}
    }));
    assert!(!state.navigation.sessions[1].unread);
}

#[test]
fn terminal_run_interrupts_stale_live_tool_blocks() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session".into();
    state.apply_direct_event(json!({
        "kind": "run_started",
        "sessionId": "session",
        "runId": "run"
    }));
    state.runtime.run_started_at_ms = unix_millis() - 65_000;
    state.apply_direct_event(json!({
        "kind": "tool_started",
        "sessionId": "session",
        "runId": "run",
        "toolCallId": "call",
        "state": "running",
        "data": {"name": "subagent.spawn"}
    }));
    state.runtime.approvals = vec![json!({"approvalId":"approval","runId":"run"})];
    state.runtime.questions = vec![json!({"userInputId":"question","runId":"run"})];
    state.runtime.plans = vec![json!({"planId":"plan","runId":"run"})];
    state.apply_direct_event(json!({
        "kind": "run_cancelled",
        "sessionId": "session",
        "runId": "run",
        "state": "cancelled"
    }));

    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks[0].state.as_ref(), "interrupted");
    assert_eq!(blocks[1].kind.as_ref(), "status");
    assert_eq!(blocks[1].title.as_ref(), "run_cancelled");
    let elapsed = blocks[1].extra["runElapsedMs"]
        .as_str()
        .unwrap()
        .parse::<i64>()
        .unwrap();
    assert!((65_000..66_000).contains(&elapsed));
    assert!(state.runtime.approvals.is_empty());
    assert!(state.runtime.questions.is_empty());
    assert!(state.runtime.plans.is_empty());
}

#[test]
fn run_elapsed_survives_dispatch_projection_refresh_and_reconnect() {
    let mut state = AppState::default();
    let started = unix_millis() - 65_000;
    let started_at = chrono::DateTime::from_timestamp_millis(started)
        .unwrap()
        .to_rfc3339();
    let projection = json!({"session":{"id":"session"},"blocks":[],"toolRecords":[]});
    let snapshot = json!({
        "selectedSessionId":"session", "session":projection,
        "runs":[{"sessionId":"session", "runId":"run", "state":"running",
            "activity":"waiting_model", "startedAt":started_at}]
    });
    state.apply_reconnect_snapshot(snapshot.clone());
    assert_eq!(state.runtime.run_started_at_ms, started);
    for _ in 0..3 {
        state.apply_direct_event(json!({"kind":"session_projection", "sessionId":"session", "sessionProjection":projection}));
        assert_eq!(state.runtime.run_started_at_ms, started);
    }
    state.apply_reconnect_snapshot(snapshot);
    assert_eq!(state.runtime.run_started_at_ms, started);
    state.apply_direct_event(json!({
        "kind":"text_delta", "sessionId":"session", "runId":"run",
        "text":"Finished", "textPhase":"final_answer", "state":"streaming"
    }));
    state.apply_direct_event(json!({
        "kind":"run_state", "sessionId":"session", "runId":"run",
        "runProjection":{"sessionId":"session", "runId":"run", "state":"completed",
            "activity":"idle", "startedAt":started_at}
    }));
    state.apply_direct_event(json!({
        "kind":"run_finished", "sessionId":"session", "runId":"run"
    }));
    let blocks = state.transcript.blocks.borrow();
    let elapsed = blocks.last().unwrap().extra["elapsedMs"]
        .as_str()
        .unwrap()
        .parse::<i64>()
        .unwrap();
    assert!((65_000..66_000).contains(&elapsed), "elapsed: {elapsed}");
    assert!(!state.runtime.running);
    assert_eq!(state.runtime.run_started_at_ms, 0);
}

#[test]
fn tool_activity_settles_prior_streaming_thinking() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session".into();
    state.apply_direct_event(json!({
        "kind": "run_started",
        "sessionId": "session",
        "runId": "run"
    }));
    state.apply_direct_event(json!({
        "kind": "thinking_delta",
        "sessionId": "session",
        "runId": "run",
        "text": "first thought"
    }));
    state.apply_direct_event(json!({
        "kind": "tool_started",
        "sessionId": "session",
        "runId": "run",
        "toolCallId": "call",
        "state": "running",
        "data": {"name": "coding.read_file"}
    }));
    assert_eq!(
        state.transcript.blocks.borrow()[0].state.as_ref(),
        "completed"
    );

    state.apply_direct_event(json!({
        "kind": "thinking_delta",
        "sessionId": "session",
        "runId": "run",
        "text": "second thought"
    }));
    assert_eq!(
        state
            .transcript
            .blocks
            .borrow()
            .iter()
            .filter(|block| {
                matches!(block.kind.as_ref(), "assistant" | "thinking")
                    && block.state.as_ref() == "streaming"
            })
            .count(),
        1
    );
    state.apply_direct_event(json!({
        "kind": "tool_update",
        "sessionId": "session",
        "runId": "run",
        "toolCallId": "call",
        "state": "running",
        "data": {"name": "coding.read_file"}
    }));
    assert!(
        state
            .transcript
            .blocks
            .borrow()
            .iter()
            .filter(|block| matches!(block.kind.as_ref(), "assistant" | "thinking"))
            .all(|block| block.state.as_ref() == "completed")
    );
}

#[test]
fn late_tool_start_repairs_an_unnamed_progress_row() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session".into();
    state.apply_direct_event(json!({
        "kind": "tool_update",
        "sessionId": "session",
        "runId": "run",
        "toolCallId": "call",
        "state": "running",
        "text": "running"
    }));
    assert!(state.transcript.blocks.borrow()[0].title.is_empty());

    state.apply_direct_event(json!({
        "kind": "tool_started",
        "sessionId": "session",
        "runId": "run",
        "toolCallId": "call",
        "state": "running",
        "data": {
            "name": "coding.go_test",
            "arguments": "{\"package\":\"./internal/agent\"}"
        }
    }));
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].title.as_ref(), "coding.go_test");
    assert_eq!(
        blocks[0].extra["arguments"].as_str(),
        Some("{\"package\":\"./internal/agent\"}")
    );
}
#[test]
fn child_activity_coalesces_and_stays_bounded() {
    let mut state = AppState::default();
    state.navigation.current_session_id = "session-1".into();
    for _ in 0..300 {
        state.apply_direct_event(json!({
            "kind": "text_delta",
            "sessionId": "session-1",
            "runId": "run-child",
            "agentId": "agent-1",
            "text": "x"
        }));
    }
    assert_eq!(state.runtime.agent_blocks.len(), 1);
    assert_eq!(
        state.runtime.agent_blocks[0]
            .get("text")
            .and_then(serde_json::Value::as_str)
            .map(str::len),
        Some(300)
    );
    for index in 0..300 {
        state.apply_direct_event(json!({
            "kind": "diff_ready",
            "sessionId": "session-1",
            "runId": "run-child",
            "agentId": "agent-1",
            "toolCallId": format!("diff-{index}")
        }));
    }
    assert_eq!(state.runtime.agent_blocks.len(), 256);
    for index in 0..70 {
        state.apply_direct_event(json!({
            "kind": "agent_state",
            "sessionId": "session-1",
            "agentId": format!("agent-{index}"),
            "agent": {"state": "running"}
        }));
    }
    assert_eq!(state.runtime.agents.len(), 64);
    assert!(state.runtime.agents.iter().all(|agent| {
        agent
            .get("id")
            .and_then(serde_json::Value::as_str)
            .is_some_and(|id| !id.is_empty())
    }));
}

#[test]
fn child_thinking_titles_keep_a_markdown_block_break() {
    let mut state = AppState::default();
    for text in ["**Inspecting workspace**", "**Planning fix**"] {
        state.apply_direct_event(json!({
            "kind": "thinking_delta",
            "runId": "run-child",
            "agentId": "agent-1",
            "text": text
        }));
    }
    assert_eq!(
        state.runtime.agent_blocks[0]["text"],
        "**Inspecting workspace**\n\n**Planning fix**"
    );
}

#[test]
fn session_agent_snapshots_are_flattened_for_native_cards() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-1",
        "state": "loaded",
        "data": {"blocks": "[]"},
        "agentSnapshots": [{
            "id": "agent-1",
            "state": "completed",
            "summary": "审查完成",
            "parentRunId": "run-1",
            "agent": {"type": "review", "description": "审查界面"}
        }]
    }));
    assert_eq!(state.runtime.agents[0]["id"], "agent-1");
    assert_eq!(state.runtime.agents[0]["state"], "completed");
    assert_eq!(state.runtime.agents[0]["description"], "审查界面");
    assert_eq!(state.runtime.agents[0]["parentRunId"], "run-1");
}
#[test]
fn incremental_text_keeps_phase_boundaries_and_terminal_identity() {
    let mut state = AppState::default();
    for (sequence, text, phase) in [
        (1, "working ", "commentary"),
        (2, "now", "commentary"),
        (3, "done", "final_answer"),
    ] {
        state.apply_envelope(Envelope {
            version: 1,
            kind: "event_batch".into(),
            id: String::new(),
            client_id: String::new(),
            workspace_id: String::new(),
            method: None,
            channel: "runtime".into(),
            sequence,
            payload: json!({
                "sequence": sequence,
                "kind": "text_delta",
                "runId": "run-1",
                "text": text,
                "textPhase": phase
            }),
            error: None,
            binary: None,
        });
    }
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 2);
    assert_eq!(blocks[0].content, "working now");
    assert_eq!(blocks[1].content, "done");
    drop(blocks);
    state.apply_terminal_binary(
        BinaryMetadata {
            transfer_id: "terminal-1".into(),
            purpose: "terminal_output".into(),
            ..Default::default()
        },
        b"output",
    );
    assert_eq!(state.terminals.active_id.as_ref(), "terminal-1");
}

#[test]
fn discrete_thinking_titles_keep_a_markdown_block_break() {
    let mut state = AppState::default();
    for (sequence, text) in [(1, "**Inspecting workspace**"), (2, "**Planning fix**")] {
        state.apply_direct_event(json!({
            "sequence": sequence,
            "kind": "thinking_delta",
            "runId": "run-1",
            "text": text
        }));
    }
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(
        blocks[0].content,
        "**Inspecting workspace**\n\n**Planning fix**"
    );
    drop(blocks);

    state.apply_direct_event(json!({
        "kind": "session_loaded",
        "sessionId": "session-1",
        "state": "loaded",
        "data": {
            "blocks": json!([{
                "kind": "thinking",
                "content": "**Inspecting workspace****Planning fix**"
            }]).to_string()
        }
    }));
    let restored = state.transcript.blocks.borrow();
    assert_eq!(
        restored[0].content,
        "**Inspecting workspace**\n\n**Planning fix**"
    );
}

#[test]
fn resolved_approval_is_removed_by_durable_identifier() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
        "kind": "approval_requested",
        "approvalId": "approval-1",
        "runId": "run-1"
    }));
    assert_eq!(state.runtime.approvals.len(), 1);
    state.apply_direct_event(json!({
        "kind": "approval_resolved",
        "approvalId": "approval-1",
        "runId": "run-1"
    }));
    assert!(state.runtime.approvals.is_empty());
}

#[test]
fn security_projection_updates_live_scan_and_findings() {
    let mut state = AppState::default();
    state.security.scans = vec![json!({"id":"scan-1","status":"queued"})];
    state.apply_direct_event(json!({
        "kind": "security_scan_state",
        "security": {
            "scan": {"id":"scan-1","status":"running"},
            "findings": [{"occurrenceId":"finding-1"}]
        }
    }));

    assert_eq!(state.security.projection["scan"]["status"], "running");
    assert_eq!(state.security.scans.len(), 1);
    assert_eq!(state.security.scans[0]["status"], "running");
    assert_eq!(state.security.findings.len(), 1);
}

#[test]
fn session_and_project_lists_accept_rfc3339_timestamps() {
    let mut state = AppState::default();
    state.apply_direct_event(json!({
            "kind": "session_loaded",
            "state": "list",
            "data": {
                "sessions": "[{\"id\":\"session-1\",\"title\":\"One\",\"workspace\":\"/workspace\",\"updatedAt\":\"2026-08-25T00:00:00Z\",\"unread\":true,\"pinned\":true}]",
                "projects": "[{\"workspace\":\"/workspace\",\"updatedAt\":\"2026-08-25T00:00:00Z\"}]"
            }
        }));
    assert_eq!(state.navigation.sessions.len(), 1);
    assert!(state.navigation.sessions[0].unread);
    assert!(state.navigation.sessions[0].pinned);
    assert_eq!(state.navigation.projects.len(), 1);
    assert_eq!(state.navigation.projects[0].path.as_ref(), "/workspace");
}
#[test]
fn streaming_reducer_coalesces_ten_thousand_deltas_into_one_block() {
    let mut state = AppState::default();
    for sequence in 1..=10_000 {
        state.apply_envelope(Envelope {
            version: 1,
            kind: "event_batch".into(),
            id: String::new(),
            client_id: String::new(),
            workspace_id: String::new(),
            method: None,
            channel: "runtime".into(),
            sequence,
            payload: json!({
                "sequence": sequence,
                "kind": "text_delta",
                "runId": "run-1",
                "text": "x",
                "textPhase": "final_answer"
            }),
            error: None,
            binary: None,
        });
    }
    let blocks = state.transcript.blocks.borrow();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].content.len(), 10_000);
}

#[test]
fn current_backend_snapshot_restores_projection_and_controls() {
    let mut state = AppState::default();
    state.apply_reconnect_snapshot(json!({
        "selectedSessionId":"s", "wireSequence":42,
        "session":{"session":{"id":"s","title":"Current","providerId":"grok","modelId":"grok-4.6","reasoning":"xhigh"},
            "blocks":[{"id":"b","sequence":1,"kind":"user","content":"hello"}],
            "toolRecords":[],"todo":{"items":[{"id":"1","status":"completed"}]},"agentSnapshots":[]},
        "runs":[{"sessionId":"s","runId":"r","state":"running","activity":"awaiting_approval"}],
        "liveBlocks":[{"id":"live","sessionId":"s","runId":"r","kind":"text","textPhase":"commentary","content":"Working","state":"streaming"}],
        "controls":[{"kind":"approval","id":"a","sessionId":"s","runId":"r","state":"pending","data":{"tool":"coding.shell"}}]
    }));
    assert_eq!(state.navigation.current_session_id.as_ref(), "s");
    assert_eq!(state.navigation.current_title.as_ref(), "Current");
    assert_eq!(state.settings.model.as_ref(), "grok-4.6");
    assert_eq!(state.transcript.blocks.borrow().len(), 2);
    assert_eq!(state.runtime.todo["items"][0]["status"], "completed");
    assert!(state.runtime.running);
    assert_eq!(state.runtime.activity.as_ref(), "awaiting_approval");
    assert_eq!(state.runtime.approvals[0]["approvalId"], "a");
    assert_eq!(state.sequence, 42);
    state.apply_direct_event(json!({"kind":"run_state","sessionId":"s","runProjection":{"sessionId":"s","runId":"r","state":"completed","activity":"idle"}}));
    assert!(!state.runtime.running);
    state.apply_reconnect_snapshot(json!({"selectedSessionId":"s","runs":[],"controls":[]}));
    assert!(state.runtime.approvals.is_empty());
}

#[test]
fn selection_snapshot_preserves_catalogs_and_queue_revisions() {
    let mut state = AppState::default();
    state.workspace.root = "/workspace".into();
    state.apply_direct_event(json!({"selectedSessionId":"s","session":{"session":{"id":"s","title":"New"},"blocks":[],"toolRecords":[]},"runs":[],"controls":[],"promptQueues":[{"sessionId":"s","revision":3,"items":[{"id":"q","text":"next"}]}]}));
    assert_eq!(state.workspace.root.as_ref(), "/workspace");
    state.apply_direct_event(json!({"kind":"prompt_queue_state","promptQueue":{"sessionId":"s","revision":2,"items":[]}}));
    assert_eq!(state.runtime.prompt_queues["s"]["items"][0]["id"], "q");
    state.apply_direct_event(json!({"kind":"session_projection","sessionId":"other","sessionProjection":{"session":{"id":"other"},"blocks":[]}}));
    assert_eq!(state.navigation.current_session_id.as_ref(), "s");
}

#[test]
fn native_session_navigation_preserves_project_pull_request() {
    // All native navigation entry points must use the session-only snapshot.
    // The legacy resume response is a reconnect snapshot without PR data.
    for source in [
        include_str!("../surfaces/navigation.rs"),
        include_str!("../window_runtime.rs"),
    ] {
        assert!(!source.contains("Method::ResumeSession"));
    }
    let mut state = AppState::default();
    state.workspace.root = "/workspace".into();
    let dashboard = json!({"current":{"number":33,"title":"Harden native desktop runtime"}});
    state.pull_requests.dashboard = dashboard.clone();
    state.pull_requests.selected = json!({"number":33});
    for session_id in ["second", "first"] {
        state.apply_direct_event(json!({"selectedSessionId":session_id,
            "session":{"session":{"id":session_id},"blocks":[],"toolRecords":[]},
            "runs":[],"controls":[],"promptQueues":[]}));
        assert_eq!(state.navigation.current_session_id.as_ref(), session_id);
        assert_eq!(state.pull_requests.dashboard, dashboard);
        assert_eq!(state.pull_requests.selected["number"], 33);
        assert_eq!(state.workspace.root.as_ref(), "/workspace");
    }
}

#[test]
fn fusion_prose_restores_in_order_and_streams_without_merging_the_lead() {
    let prose = |id: &str, kind: &str, text: &str, after: &str| {
        json!({
            "id":"block:7", "sequence":7, "runId":"lead", "kind":kind,
            "content":text, "state":"streaming", "textPhase":"commentary",
            "data":{"fusionRole":"sidekick", "sourceLabel":"Sidekick · actual-model", "fusionBlockId":id, "fusionAfterToolCallId":after}
        })
    };
    let mut state = AppState::default();
    state.apply_session_projection(&json!({
        "session":{"id":"session"},
        "blocks":[
            {"id":"user", "sequence":7, "kind":"user", "runId":"lead", "content":"request"},
            prose("thought", "thinking", "reason", "handoff"),
            prose("progress", "assistant", "progress", "handoff"),
            prose("report", "assistant", "report", "fusion:child:read")
        ],
        "toolRecords":[
            {"runId":"lead", "toolCallId":"handoff", "name":"sidekick", "anchorSequence":7},
            {"runId":"lead", "toolCallId":"fusion:child:read", "name":"coding.read_file", "anchorSequence":7}
        ]
    }));
    assert_eq!(
        state
            .transcript
            .blocks
            .borrow()
            .iter()
            .map(|block| block.id.to_string())
            .collect::<Vec<_>>(),
        [
            "user",
            "handoff",
            "thought",
            "progress",
            "fusion:child:read",
            "report"
        ]
    );
    let delta = |text: &str, data: serde_json::Value| {
        serde_json::from_value(json!({
        "kind":"text_delta", "sessionId":"session", "runId":"lead", "textPhase":"commentary", "text":text, "data":data
    })).unwrap()
    };
    state.append_stream_text(delta(" continued", json!({"fusionBlockId":"report", "fusionRole":"sidekick", "sourceLabel":"Sidekick · actual-model"})), "assistant");
    assert_eq!(state.transcript.blocks.borrow().len(), 6);
    assert_eq!(
        state.transcript.blocks.borrow().last().unwrap().content,
        "report continued"
    );
    state.append_stream_text(delta("Lead review", json!({})), "assistant");
    assert_eq!(state.transcript.blocks.borrow().len(), 7);
    assert_eq!(
        state.transcript.blocks.borrow()[5].state.as_ref(),
        "completed"
    );
    assert!(state.runtime.agents.is_empty());
}
