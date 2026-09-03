use azem_ipc::{BinaryMetadata, Envelope};
use serde_json::json;

use super::{AppState, SessionSummary, unix_millis};

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
