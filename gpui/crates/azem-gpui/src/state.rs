use std::{
    cell::RefCell,
    collections::HashMap,
    rc::Rc,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use azem_ipc::{BinaryMetadata, Envelope};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum Surface {
    #[default]
    Thread,
    Search,
    Projects,
    Files,
    Changes,
    PullRequests,
    Security,
    Terminal,
}

#[derive(Clone, Debug, Default)]
pub struct AppState {
    pub connection: ConnectionModel,
    pub navigation: NavigationModel,
    pub transcript: TranscriptModel,
    pub runtime: RuntimeModel,
    pub catalogs: CatalogModel,
    pub workspace: WorkspaceModel,
    pub pull_requests: PullRequestModel,
    pub security: SecurityModel,
    pub terminals: TerminalModel,
    pub settings: SettingsModel,
    pub sequence: u64,
}

#[derive(Clone, Debug, Default)]
pub struct ConnectionModel {
    pub connected: bool,
    pub reconnecting: bool,
    pub daemon_pid: u32,
    pub message: Arc<str>,
}

#[derive(Clone, Debug, Default)]
pub struct NavigationModel {
    pub surface: Surface,
    pub current_session_id: Arc<str>,
    pub current_title: Arc<str>,
    pub sessions: Vec<SessionSummary>,
    pub projects: Vec<ProjectSummary>,
    pub session_tree: Value,
    pub search_results: Vec<Value>,
    pub search_error: Arc<str>,
}

#[derive(Clone, Debug, Default)]
pub struct TranscriptModel {
    pub blocks: Rc<RefCell<Vec<Block>>>,
    pub index_by_id: HashMap<Arc<str>, usize>,
    pub attachments: Vec<Value>,
}

#[derive(Clone, Debug, Default)]
pub struct RuntimeModel {
    pub running: bool,
    pub run_id: Arc<str>,
    pub run_started_at_ms: i64,
    pub active_session_id: Arc<str>,
    pub activity: Arc<str>,
    pub plan_mode: bool,
    pub todo: Value,
    pub approvals: Vec<Value>,
    pub questions: Vec<Value>,
    pub plans: Vec<Value>,
    pub agents: Vec<Value>,
    pub selected_agent_id: Arc<str>,
    pub agent_blocks: Vec<Value>,
    pub hooks: Vec<Value>,
    pub memories: Vec<Value>,
    pub background_logs: Value,
    pub background: Vec<Value>,
    pub context_profile: Value,
    pub context_usage: Value,
    pub recap: Value,
}

#[derive(Clone, Debug, Default)]
pub struct CatalogModel {
    pub providers: Vec<Value>,
    pub models: Vec<Value>,
    pub skills: Vec<Value>,
    pub skill_diagnostics: Vec<Value>,
    pub routes: Vec<Value>,
    pub plugins: Vec<Value>,
    pub plugin_diagnostics: Vec<Value>,
    pub marketplace: Value,
    pub hooks: Value,
    pub mcp: Value,
    pub commands: Vec<Value>,
    pub themes: Vec<Value>,
    pub agent_types: Vec<Value>,
    pub auth: Value,
}

#[derive(Clone, Debug, Default)]
pub struct WorkspaceModel {
    pub root: Arc<str>,
    pub branch: Arc<str>,
    pub branches: Vec<Value>,
    pub dirty: bool,
    pub additions: i64,
    pub deletions: i64,
    pub changed_files: i64,
    pub file_tree: Value,
    pub selected_file: Value,
    pub changes: Value,
}

#[derive(Clone, Debug, Default)]
pub struct PullRequestModel {
    pub dashboard: Value,
    pub selected: Value,
    pub monitors: HashMap<i64, Value>,
    pub loading: bool,
    pub error: Arc<str>,
}

#[derive(Clone, Debug, Default)]
pub struct SecurityModel {
    pub config: Value,
    pub scans: Vec<Value>,
    pub projection: Value,
    pub findings: Vec<Value>,
    pub selected_finding: Value,
    pub patch: Value,
}

#[derive(Clone, Debug, Default)]
pub struct TerminalModel {
    pub sessions: Vec<Value>,
    pub active_id: Arc<str>,
}

#[derive(Clone, Debug, Default)]
pub struct SettingsModel {
    pub language: Arc<str>,
    pub provider: Arc<str>,
    pub model: Arc<str>,
    pub reasoning: Arc<str>,
    pub chatgpt_fast_mode: bool,
    pub agent_mode: Arc<str>,
    pub approval_mode: Arc<str>,
    pub queue_mode: Arc<str>,
    pub subagent_concurrency: i64,
    pub subagent_max_depth: i64,
    pub shell_concurrency: i64,
    pub shell_max_wall_clock_seconds: i64,
    pub subagent_await_seconds: i64,
    pub subagent_idle_seconds: i64,
    pub appearance: Value,
    pub usage: Value,
    pub recovery: Value,
    pub error: Arc<str>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionSummary {
    #[serde(default)]
    pub id: Arc<str>,
    #[serde(default)]
    pub title: Arc<str>,
    #[serde(default)]
    pub workspace: Arc<str>,
    #[serde(default)]
    pub updated_at: Value,
    #[serde(default)]
    pub running: bool,
    #[serde(default)]
    pub unread: bool,
    #[serde(default)]
    pub pinned: bool,
    #[serde(default)]
    pub archived: bool,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProjectSummary {
    #[serde(default)]
    pub id: Arc<str>,
    #[serde(default)]
    pub name: Arc<str>,
    #[serde(default, alias = "workspace")]
    pub path: Arc<str>,
    #[serde(default)]
    pub updated_at: Value,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Block {
    #[serde(default)]
    pub id: Arc<str>,
    #[serde(default)]
    pub kind: Arc<str>,
    #[serde(default)]
    pub run_id: Arc<str>,
    #[serde(default)]
    pub tool_call_id: Arc<str>,
    #[serde(default)]
    pub title: Arc<str>,
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub state: Arc<str>,
    #[serde(default)]
    pub text_phase: Arc<str>,
    #[serde(flatten)]
    pub extra: HashMap<String, Value>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct DesktopEvent {
    #[serde(default)]
    sequence: u64,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    session_id: String,
    #[serde(default)]
    run_id: String,
    #[serde(default)]
    agent_id: String,
    #[serde(default)]
    approval_id: String,
    #[serde(default)]
    user_input_id: String,
    #[serde(default)]
    plan_id: String,
    #[serde(default)]
    tool_call_id: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    text_phase: String,
    #[serde(default)]
    state: String,
    #[serde(default)]
    at: String,
    #[serde(default)]
    data: HashMap<String, String>,
    #[serde(default)]
    todo: Value,
    #[serde(default)]
    recap: Value,
    #[serde(default)]
    agent: Value,
    #[serde(default)]
    agent_snapshots: Vec<Value>,
    #[serde(default)]
    agent_blocks: Vec<Value>,
    #[serde(default)]
    agent_catalog: Vec<Value>,
    #[serde(default)]
    skill_catalog: Vec<Value>,
    #[serde(default)]
    skill_diagnostics: Vec<Value>,
    #[serde(default)]
    plugin_catalog: Vec<Value>,
    #[serde(default)]
    marketplace_catalog: Value,
    #[serde(default)]
    plugin_diagnostics: Vec<Value>,
    #[serde(default)]
    hook_catalog: Value,
    #[serde(default)]
    context_profile: Value,
    #[serde(default)]
    model_routes: Vec<Value>,
    #[serde(default)]
    model_providers: Vec<Value>,
    #[serde(default)]
    background: Vec<Value>,
    #[serde(default)]
    background_logs: Value,
    #[serde(default)]
    memories: Vec<Value>,
    #[serde(default)]
    git_branches: Vec<Value>,
    #[serde(default)]
    workspace_dirty: bool,
    #[serde(default)]
    usage_report: Value,
    #[serde(default)]
    security_config: Value,
    #[serde(default)]
    security: Value,
    #[serde(default)]
    security_scans: Vec<Value>,
    #[serde(default)]
    security_findings: Vec<Value>,
    #[serde(default)]
    security_finding: Value,
    #[serde(default)]
    security_patch: Value,
}

fn decode_desktop_event(mut value: Value) -> serde_json::Result<DesktopEvent> {
    if let Some(fields) = value.as_object_mut() {
        fields.retain(|_, value| !value.is_null());
    }
    serde_json::from_value(value)
}

impl AppState {
    pub(crate) fn append_optimistic_user(
        &mut self,
        request_id: &str,
        content: String,
        attachments: Vec<Value>,
    ) {
        let id: Arc<str> = format!("user-{request_id}").into();
        let mut extra = HashMap::new();
        if !attachments.is_empty() {
            extra.insert("attachments".to_string(), Value::Array(attachments));
        }
        if let Ok(elapsed) = SystemTime::now().duration_since(UNIX_EPOCH) {
            extra.insert(
                "createdAt".to_string(),
                Value::String(elapsed.as_millis().to_string()),
            );
        }
        let mut blocks = self.transcript.blocks.borrow_mut();
        blocks.push(Block {
            id: id.clone(),
            kind: "user".into(),
            content,
            state: "submitted".into(),
            extra,
            ..Default::default()
        });
        self.transcript.index_by_id.insert(id, blocks.len() - 1);
        self.runtime.running = true;
        self.runtime.activity = "waiting_model".into();
    }

    pub(crate) fn append_request_error(&mut self, request_id: &str, error: String) {
        self.apply_desktop_event(DesktopEvent {
            kind: "run_failed".to_string(),
            session_id: self.navigation.current_session_id.to_string(),
            run_id: if self.runtime.run_id.is_empty() {
                request_id.to_string()
            } else {
                self.runtime.run_id.to_string()
            },
            text: error,
            state: "failed".to_string(),
            ..Default::default()
        });
    }

    pub fn apply_reconnect_snapshot(&mut self, snapshot: Value) {
        if let Some(base) = snapshot.get("base") {
            self.workspace.root = value_str(base, "workspace");
            self.workspace.branch = value_str(base, "currentBranch");
            self.navigation.current_session_id = value_str(base, "sessionId");
            self.settings.language = value_str(base, "language");
            self.settings.provider = value_str(base, "provider");
            self.settings.model = value_str(base, "model");
            self.settings.reasoning = value_str(base, "reasoning");
            self.settings.chatgpt_fast_mode = base["chatgptFastMode"].as_bool().unwrap_or(false);
            self.settings.agent_mode = value_str(base, "agentMode");
            self.settings.approval_mode = value_str(base, "approvalMode");
            self.settings.queue_mode = value_str(base, "queueMode");
            self.settings.subagent_concurrency = value_i64(base, "subagentConcurrency");
            self.settings.subagent_max_depth = value_i64(base, "subagentMaxDepth");
            self.settings.shell_concurrency = value_i64(base, "shellConcurrency");
            self.settings.shell_max_wall_clock_seconds =
                value_i64(base, "shellMaxWallClockSeconds");
            self.settings.subagent_await_seconds = value_i64(base, "subagentAwaitSeconds");
            self.settings.subagent_idle_seconds = value_i64(base, "subagentIdleSeconds");
        }
        self.navigation.sessions = snapshot
            .get("sessions")
            .cloned()
            .and_then(|value| serde_json::from_value(value).ok())
            .unwrap_or_default();
        self.navigation.projects = snapshot
            .get("projects")
            .cloned()
            .and_then(|value| serde_json::from_value(value).ok())
            .unwrap_or_default();
        let active_session_id = value_str(&snapshot, "activeSessionId");
        let active_run_id = value_str(&snapshot, "activeRunId");
        self.runtime.active_session_id = active_session_id.clone();
        self.runtime.run_id = active_run_id.clone();
        self.runtime.running =
            !active_run_id.is_empty() && active_session_id == self.navigation.current_session_id;
        if active_run_id.is_empty() {
            self.runtime.activity = "".into();
        }
        for session in &mut self.navigation.sessions {
            session.running = !active_run_id.is_empty() && session.id == active_session_id;
        }
        if let Some(session) = snapshot.get("session")
            && let Ok(event) = decode_desktop_event(session.clone())
        {
            self.apply_desktop_event(event);
        }
        self.navigation.session_tree = snapshot.get("tree").cloned().unwrap_or(Value::Null);
        self.catalogs.skills = snapshot
            .pointer("/skills/entries")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        self.catalogs.hooks = snapshot.get("hooks").cloned().unwrap_or(Value::Null);
        self.catalogs.marketplace = snapshot.get("marketplace").cloned().unwrap_or(Value::Null);
        self.pull_requests.dashboard = snapshot.get("pullRequests").cloned().unwrap_or(Value::Null);
        self.terminals.sessions = snapshot
            .get("terminals")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        if let Some(controls) = snapshot.get("pendingControls").and_then(Value::as_array) {
            for control in controls {
                if let Ok(event) = decode_desktop_event(control.clone()) {
                    self.apply_desktop_event(event);
                }
            }
        }
        self.connection.connected = true;
        self.connection.reconnecting = false;
        self.connection.message = "Connected".into();
    }

    pub fn apply_envelope(&mut self, envelope: Envelope) {
        self.sequence = self.sequence.max(envelope.sequence);
        match envelope.channel.as_str() {
            "runtime" => match decode_desktop_event(envelope.payload) {
                Ok(event) => self.apply_desktop_event(event),
                Err(error) => tracing::warn!(%error, "GPUI runtime event decode failed"),
            },
            "pull_request" => self.apply_pull_request_event(envelope.payload),
            "terminal" => self.apply_terminal_event(envelope.payload),
            "daemon" => self.connection.message = value_str(&envelope.payload, "state"),
            _ => {}
        }
    }

    pub fn apply_direct_event(&mut self, value: Value) {
        match decode_desktop_event(value) {
            Ok(event) => self.apply_desktop_event(event),
            Err(error) => tracing::warn!(%error, "GPUI direct event decode failed"),
        }
    }
    pub fn apply_terminal_binary(&mut self, metadata: BinaryMetadata, _data: &[u8]) {
        if metadata.purpose != "terminal_output" || metadata.transfer_id.is_empty() {
            return;
        }
        if self.terminals.active_id.is_empty() {
            self.terminals.active_id = metadata.transfer_id.into();
        }
    }

    fn apply_desktop_event(&mut self, event: DesktopEvent) {
        self.sequence = self.sequence.max(event.sequence);
        let foreign_session = !event.session_id.is_empty()
            && !self.navigation.current_session_id.is_empty()
            && event.session_id != self.navigation.current_session_id.as_ref();
        if foreign_session && !matches!(event.kind.as_str(), "session_loaded" | "projection_resync")
        {
            match event.kind.as_str() {
                "run_started" => {
                    set_session_running(&mut self.navigation.sessions, &event.session_id, true);
                    self.runtime.active_session_id = event.session_id.clone().into();
                }
                "run_finished" | "run_failed" | "run_cancelled" => {
                    set_session_running(&mut self.navigation.sessions, &event.session_id, false);
                    if event.kind != "run_cancelled"
                        && let Some(session) = self
                            .navigation
                            .sessions
                            .iter_mut()
                            .find(|session| session.id.as_ref() == event.session_id)
                    {
                        session.unread = true;
                    }
                    if self.runtime.active_session_id.as_ref() == event.session_id {
                        self.runtime.active_session_id = "".into();
                    }
                }
                _ => {}
            }
            return;
        }
        if !event.agent_id.is_empty()
            && matches!(
                event.kind.as_str(),
                "text_delta"
                    | "thinking_delta"
                    | "tool_started"
                    | "tool_update"
                    | "tool_finished"
                    | "diff_ready"
                    | "todo_updated"
                    | "context_usage"
            )
        {
            self.apply_agent_stream_event(event);
            return;
        }
        match event.kind.as_str() {
            "session_loaded" | "projection_resync" => self.load_session(event),
            "run_started" => {
                let is_current = event.session_id == self.navigation.current_session_id.as_ref();
                self.runtime.active_session_id = event.session_id.clone().into();
                for session in &mut self.navigation.sessions {
                    if session.id.as_ref() == event.session_id {
                        session.running = true;
                    }
                }
                if is_current {
                    self.runtime.running = true;
                    self.runtime.run_id = event.run_id.into();
                    self.runtime.run_started_at_ms = unix_millis();
                    self.runtime.activity = "running".into();
                }
            }
            "run_finished" | "run_failed" | "run_cancelled" => {
                let completed_at_ms = unix_millis();
                let elapsed_ms = if self.runtime.run_started_at_ms > 0 {
                    (completed_at_ms - self.runtime.run_started_at_ms).max(0)
                } else {
                    0
                };
                let is_current = event.session_id == self.navigation.current_session_id.as_ref();
                for session in &mut self.navigation.sessions {
                    if session.id.as_ref() == event.session_id {
                        session.running = false;
                        if event.kind != "run_cancelled" {
                            session.unread = !is_current;
                        }
                    }
                }
                if self.runtime.active_session_id.as_ref() == event.session_id {
                    self.runtime.active_session_id = "".into();
                }
                remove_run_controls(&mut self.runtime.approvals, &event.run_id);
                remove_run_controls(&mut self.runtime.questions, &event.run_id);
                remove_run_controls(&mut self.runtime.plans, &event.run_id);
                if is_current {
                    self.remove_context_compaction(&event.run_id);
                    if event.kind == "run_failed" {
                        self.append_event_block(event.clone(), "error");
                    }
                    self.runtime.running = false;
                    self.runtime.run_id = "".into();
                    self.runtime.activity = event.state.into();
                    let mut inserted_status = false;
                    {
                        let mut blocks = self.transcript.blocks.borrow_mut();
                        for block in blocks.iter_mut() {
                            if block.run_id.as_ref() != event.run_id {
                                continue;
                            }
                            if matches!(block.kind.as_ref(), "tool" | "diff")
                                && matches!(
                                    block.state.as_ref(),
                                    "running"
                                        | "queued"
                                        | "pending"
                                        | "streaming"
                                        | "started"
                                        | "progress"
                                        | "awaiting_approval"
                                        | "reviewing_approval"
                                )
                            {
                                block.state = "interrupted".into();
                            } else if block.state.as_ref() == "streaming" {
                                block.state = "complete".into();
                            }
                            if is_final_output_block(block) {
                                if self.runtime.run_started_at_ms > 0 {
                                    block.extra.entry("startedAt".to_string()).or_insert_with(
                                        || {
                                            Value::String(
                                                self.runtime.run_started_at_ms.to_string(),
                                            )
                                        },
                                    );
                                }
                                block.extra.insert(
                                    "completedAt".to_string(),
                                    Value::String(completed_at_ms.to_string()),
                                );
                                block.extra.insert(
                                    "elapsedMs".to_string(),
                                    Value::String(elapsed_ms.to_string()),
                                );
                            }
                        }
                        if event.kind == "run_cancelled"
                            && !blocks.iter().any(|block| {
                                block.run_id.as_ref() == event.run_id
                                    && block.kind.as_ref() == "status"
                                    && block.title.as_ref() == "run_cancelled"
                            })
                        {
                            let status = Block {
                                id: format!("status-cancelled:{}:{}", event.run_id, self.sequence)
                                    .into(),
                                kind: "status".into(),
                                run_id: event.run_id.clone().into(),
                                title: "run_cancelled".into(),
                                state: "cancelled".into(),
                                extra: HashMap::from([(
                                    "runElapsedMs".to_string(),
                                    Value::String(elapsed_ms.to_string()),
                                )]),
                                ..Default::default()
                            };
                            let insert_at = blocks
                                .iter()
                                .rposition(|block| {
                                    block.run_id.as_ref() == event.run_id
                                        && is_final_output_block(block)
                                })
                                .unwrap_or(blocks.len());
                            blocks.insert(insert_at, status);
                            inserted_status = true;
                        }
                    }
                    if inserted_status {
                        self.transcript.rebuild_index();
                    }
                    self.runtime.run_started_at_ms = 0;
                }
            }
            "provider_retry" => self.runtime.activity = event.text.into(),
            "text_delta" => self.append_stream_text(event, "assistant"),
            "thinking_delta" => self.append_stream_text(event, "thinking"),
            "tool_started" | "tool_update" | "tool_finished" => self.apply_tool_event(event),
            "diff_ready" => self.append_event_block(event, "diff"),
            "todo_updated" => self.runtime.todo = event.todo,
            "approval_requested" => {
                let value = event_value(&event);
                upsert_pending(
                    &mut self.runtime.approvals,
                    "approvalId",
                    &event.approval_id,
                    value,
                );
            }
            "approval_resolved" => retain_unresolved(
                &mut self.runtime.approvals,
                "approvalId",
                &event.approval_id,
            ),
            "user_input_requested" => {
                let value = event_value(&event);
                upsert_pending(
                    &mut self.runtime.questions,
                    "userInputId",
                    &event.user_input_id,
                    value,
                );
            }
            "user_input_resolved" => retain_unresolved(
                &mut self.runtime.questions,
                "userInputId",
                &event.user_input_id,
            ),
            "plan_proposed" => {
                let value = event_value(&event);
                upsert_pending(&mut self.runtime.plans, "planId", &event.plan_id, value);
            }
            "plan_resolved" => retain_unresolved(&mut self.runtime.plans, "planId", &event.plan_id),
            "agent_state" => {
                if !event.agent.is_null() && !event.agent_id.is_empty() {
                    let mut agent = event.agent;
                    if let Some(fields) = agent.as_object_mut() {
                        fields.insert("id".to_string(), Value::String(event.agent_id.clone()));
                        fields.insert("agentId".to_string(), Value::String(event.agent_id.clone()));
                        fields.insert("state".to_string(), Value::String(event.state.clone()));
                        if !event.text.is_empty() {
                            fields.insert("summary".to_string(), Value::String(event.text.clone()));
                        }
                    }
                    if let Some(existing) = self.runtime.agents.iter_mut().find(|candidate| {
                        candidate.get("id").and_then(Value::as_str) == Some(event.agent_id.as_str())
                    }) {
                        *existing = agent;
                    } else {
                        self.runtime.agents.push(agent);
                    }
                    const MAX_AGENTS: usize = 64;
                    if self.runtime.agents.len() > MAX_AGENTS {
                        let excess = self.runtime.agents.len() - MAX_AGENTS;
                        self.runtime.agents.drain(..excess);
                    }
                }
            }
            "agent_detail" => {
                if !event.agent_snapshots.is_empty() {
                    self.runtime.agents = normalize_agent_snapshots(event.agent_snapshots);
                }
                if event.agent_id.is_empty()
                    || self.runtime.selected_agent_id.as_ref() == event.agent_id
                {
                    self.runtime.agent_blocks =
                        merge_agent_detail_blocks(&self.runtime.agent_blocks, event.agent_blocks);
                }
            }
            "agent_catalog" => self.catalogs.agent_types = event.agent_catalog,
            "skill_catalog" => {
                self.catalogs.skills = event.skill_catalog;
                self.catalogs.skill_diagnostics = event.skill_diagnostics;
            }
            "plugin_catalog" => {
                self.catalogs.plugins = event.plugin_catalog;
                self.catalogs.plugin_diagnostics = event.plugin_diagnostics;
            }
            "marketplace_catalog" => {
                if event.marketplace_catalog.is_object() {
                    self.catalogs.marketplace = event.marketplace_catalog;
                } else if !event.text.is_empty() {
                    self.settings.error = event.text.into();
                }
            }
            "hook_catalog" => {
                if event.hook_catalog.is_object() {
                    self.catalogs.hooks = event.hook_catalog;
                }
            }
            "hook_finished" | "hook_diagnostic" => {
                self.runtime.hooks.push(event_value(&event));
                if self.runtime.hooks.len() > 256 {
                    let excess = self.runtime.hooks.len() - 256;
                    self.runtime.hooks.drain(..excess);
                }
            }
            "context_profile" => self.runtime.context_profile = event.context_profile,
            "context_usage" => {
                if matches!(event.state.as_str(), "compacting" | "compacted" | "failed") {
                    self.apply_context_compaction_event(&event);
                }
                if let Some(usage) = project_context_usage(&self.runtime.context_usage, &event) {
                    self.runtime.context_usage = usage;
                }
            }
            "memory_state" => self.runtime.memories = event.memories,
            "recap_state" => self.runtime.recap = event.recap,
            "model_catalog" => {
                self.catalogs.models = parse_string_json(
                    event
                        .data
                        .get("models")
                        .or_else(|| event.data.get("catalog")),
                )
            }
            "model_routes" => {
                self.catalogs.routes = event.model_routes;
                if let Some(enabled) = event
                    .data
                    .get("chatgpt_fast_mode")
                    .and_then(|value| value.parse::<bool>().ok())
                {
                    self.settings.chatgpt_fast_mode = enabled;
                }
                update_i64(
                    &event.data,
                    "subagent_max_concurrency",
                    &mut self.settings.subagent_concurrency,
                );
                update_i64(
                    &event.data,
                    "subagent_max_depth",
                    &mut self.settings.subagent_max_depth,
                );
                update_i64(
                    &event.data,
                    "shell_max_concurrency",
                    &mut self.settings.shell_concurrency,
                );
                update_i64(
                    &event.data,
                    "shell_max_wall_clock_seconds",
                    &mut self.settings.shell_max_wall_clock_seconds,
                );
                update_i64(
                    &event.data,
                    "subagent_await_seconds",
                    &mut self.settings.subagent_await_seconds,
                );
                update_i64(
                    &event.data,
                    "subagent_idle_seconds",
                    &mut self.settings.subagent_idle_seconds,
                );
            }
            "model_providers" => self.catalogs.providers = event.model_providers,
            "command_catalog" => {
                self.catalogs.commands = parse_string_json(event.data.get("commands"))
            }
            "theme_catalog" => self.catalogs.themes = parse_string_json(event.data.get("themes")),
            "mcp_state" if event.state == "snapshot" => {
                match event
                    .data
                    .get("servers")
                    .and_then(|raw| serde_json::from_str::<Vec<Value>>(raw).ok())
                {
                    Some(servers) => self.catalogs.mcp = Value::Array(servers),
                    None => self.settings.error = "Invalid MCP server snapshot".into(),
                }
            }
            "auth_state" => self.catalogs.auth = event_value(&event),
            "approval_mode" => {
                self.settings.approval_mode = event
                    .data
                    .get("mode")
                    .cloned()
                    .unwrap_or(event.state)
                    .into()
            }
            "recovery_state" => self.settings.recovery = event_value(&event),
            "background_state" => self.runtime.background = event.background,
            "background_logs" => self.runtime.background_logs = event.background_logs,
            "git_branches" => self.apply_git_event(event),
            "usage_report" => self.settings.usage = event.usage_report,
            "security_config_state" => self.security.config = event.security_config,
            "security_scan_state" | "security_publication_state" => {
                let projection = event.security;
                if let Some(scan) = projection.get("scan") {
                    let id = scan.get("id").and_then(Value::as_str).unwrap_or_default();
                    upsert_pending(&mut self.security.scans, "id", id, scan.clone());
                }
                if let Some(findings) = projection.get("findings").and_then(Value::as_array) {
                    self.security.findings = findings.clone();
                }
                self.security.projection = projection;
            }
            "security_scan_list" => self.security.scans = event.security_scans,
            "security_finding_list" => self.security.findings = event.security_findings,
            "security_finding_detail" => self.security.selected_finding = event.security_finding,
            "security_patch_state" => self.security.patch = event.security_patch,
            "bootstrap_done" => {}
            _ => {}
        }
    }

    fn load_session(&mut self, event: DesktopEvent) {
        if event.state == "list" {
            self.navigation.sessions = parse_string_json(event.data.get("sessions"));
            self.navigation.projects = parse_string_json(event.data.get("projects"));
            for session in &mut self.navigation.sessions {
                session.running = session.id == self.runtime.active_session_id;
            }
            return;
        }
        let loaded_session_id = event.session_id.clone();
        if !self.navigation.current_session_id.is_empty()
            && self.navigation.current_session_id.as_ref() != loaded_session_id
        {
            self.runtime.hooks.clear();
        }
        let keep_agent_detail = event.state == "refreshed"
            && self.navigation.current_session_id.as_ref() == loaded_session_id;
        let fallback_title = self
            .navigation
            .sessions
            .iter()
            .find(|session| session.id.as_ref() == loaded_session_id)
            .map(|session| session.title.clone())
            .unwrap_or_default();
        retain_session_values(&mut self.runtime.approvals, &loaded_session_id);
        retain_session_values(&mut self.runtime.questions, &loaded_session_id);
        retain_session_values(&mut self.runtime.plans, &loaded_session_id);
        self.navigation.current_session_id = event.session_id.into();
        for session in &mut self.navigation.sessions {
            if session.id == self.navigation.current_session_id {
                session.unread = false;
            }
        }
        self.navigation.current_title = event
            .data
            .get("title")
            .filter(|title| !title.trim().is_empty())
            .cloned()
            .map(Arc::<str>::from)
            .unwrap_or(fallback_title);
        self.settings.provider = value_str_map(&event.data, "provider");
        self.settings.model = value_str_map(&event.data, "model");
        self.settings.reasoning = value_str_map(&event.data, "reasoning");
        self.settings.agent_mode = value_str_map(&event.data, "agentMode");
        self.runtime.context_profile = Value::Null;
        self.runtime.context_usage = event
            .data
            .get("usage")
            .and_then(|usage| serde_json::from_str(usage).ok())
            .unwrap_or(Value::Null);
        let blocks = restored_session_blocks(&event.data);
        restore_durable_controls(
            &blocks,
            self.navigation.current_session_id.as_ref(),
            &mut self.runtime.questions,
            &mut self.runtime.plans,
        );
        *self.transcript.blocks.borrow_mut() = blocks;
        self.transcript.rebuild_index();
        self.runtime.todo = event.todo;
        self.runtime.recap = event.recap;
        self.runtime.running = event
            .data
            .get("active")
            .is_some_and(|value| value == "true");
        self.runtime.run_started_at_ms = if self.runtime.running {
            unix_millis()
        } else {
            0
        };
        if self.runtime.running {
            self.runtime.active_session_id = self.navigation.current_session_id.clone();
        }
        self.runtime.run_id = event
            .data
            .get("activeRunID")
            .cloned()
            .unwrap_or_default()
            .into();
        self.runtime.agents = normalize_agent_snapshots(event.agent_snapshots);
        if !keep_agent_detail {
            self.runtime.selected_agent_id = "".into();
            self.runtime.agent_blocks = event.agent_blocks;
        }
    }

    fn apply_agent_stream_event(&mut self, event: DesktopEvent) {
        if !self.runtime.selected_agent_id.is_empty()
            && self.runtime.selected_agent_id.as_ref() != event.agent_id
        {
            return;
        }
        let mut value = event_value(&event);
        if event.kind == "thinking_delta" {
            value["text"] = Value::String(normalize_thinking_content(&event.text));
        }
        let replace = match event.kind.as_str() {
            "text_delta" | "thinking_delta" => {
                self.runtime
                    .agent_blocks
                    .iter_mut()
                    .rev()
                    .find(|candidate| {
                        candidate.get("agentId").and_then(Value::as_str)
                            == Some(event.agent_id.as_str())
                            && candidate.get("runId").and_then(Value::as_str)
                                == Some(event.run_id.as_str())
                            && candidate.get("kind").and_then(Value::as_str)
                                == Some(event.kind.as_str())
                    })
            }
            "tool_update" | "tool_finished" => {
                self.runtime
                    .agent_blocks
                    .iter_mut()
                    .rev()
                    .find(|candidate| {
                        candidate.get("toolCallId").and_then(Value::as_str)
                            == Some(event.tool_call_id.as_str())
                    })
            }
            "todo_updated" | "context_usage" => {
                self.runtime
                    .agent_blocks
                    .iter_mut()
                    .rev()
                    .find(|candidate| {
                        candidate.get("agentId").and_then(Value::as_str)
                            == Some(event.agent_id.as_str())
                            && candidate.get("kind").and_then(Value::as_str)
                                == Some(event.kind.as_str())
                    })
            }
            _ => None,
        };
        if let Some(existing) = replace {
            if matches!(event.kind.as_str(), "text_delta" | "thinking_delta") {
                let mut combined = existing
                    .get("text")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                if event.kind == "thinking_delta" {
                    append_thinking_content(&mut combined, &event.text);
                } else {
                    combined.push_str(&event.text);
                }
                *existing = value;
                existing["text"] = Value::String(combined);
            } else {
                *existing = value;
            }
        } else {
            self.runtime.agent_blocks.push(value);
        }
        const MAX_AGENT_BLOCKS: usize = 256;
        if self.runtime.agent_blocks.len() > MAX_AGENT_BLOCKS {
            let excess = self.runtime.agent_blocks.len() - MAX_AGENT_BLOCKS;
            self.runtime.agent_blocks.drain(..excess);
        }
    }

    fn append_event_block(&mut self, event: DesktopEvent, kind: &str) {
        if kind == "error"
            && self
                .transcript
                .blocks
                .borrow()
                .last()
                .is_some_and(|block| block.kind.as_ref() == "error" && block.content == event.text)
        {
            return;
        }
        let id: Arc<str> = if event.tool_call_id.is_empty() {
            format!("{}:{}:{}", kind, event.run_id, self.sequence).into()
        } else {
            event.tool_call_id.clone().into()
        };
        let mut blocks = self.transcript.blocks.borrow_mut();
        blocks.push(Block {
            id: id.clone(),
            kind: kind.into(),
            run_id: event.run_id.into(),
            tool_call_id: event.tool_call_id.into(),
            title: event.data.get("path").cloned().unwrap_or_default().into(),
            content: event.text,
            state: event.state.into(),
            ..Default::default()
        });
        self.transcript.index_by_id.insert(id, blocks.len() - 1);
    }

    fn apply_context_compaction_event(&mut self, event: &DesktopEvent) {
        if event.state != "compacting" {
            self.remove_context_compaction(&event.run_id);
            return;
        }
        let id: Arc<str> = format!("context-compaction:{}", event.run_id).into();
        if self.transcript.index_by_id.contains_key(&id) {
            return;
        }
        let mut blocks = self.transcript.blocks.borrow_mut();
        blocks.push(Block {
            id: id.clone(),
            kind: "context_compaction".into(),
            run_id: event.run_id.clone().into(),
            state: "running".into(),
            ..Default::default()
        });
        self.transcript.index_by_id.insert(id, blocks.len() - 1);
    }

    fn remove_context_compaction(&mut self, run_id: &str) {
        let removed = {
            let mut blocks = self.transcript.blocks.borrow_mut();
            let before = blocks.len();
            blocks.retain(|block| {
                block.kind.as_ref() != "context_compaction" || block.run_id.as_ref() != run_id
            });
            blocks.len() != before
        };
        if removed {
            self.transcript.rebuild_index();
        }
    }

    fn append_stream_text(&mut self, event: DesktopEvent, kind: &str) {
        let phase = event.text_phase.clone();
        let mut blocks = self.transcript.blocks.borrow_mut();
        if let Some(block) = blocks.last_mut()
            && block.kind.as_ref() == kind
            && block.run_id.as_ref() == event.run_id
            && block.text_phase.as_ref() == phase
            && block.state.as_ref() == "streaming"
        {
            if kind == "thinking" {
                append_thinking_content(&mut block.content, &event.text);
            } else {
                block.content.push_str(&event.text);
            }
            return;
        }
        settle_streaming_text_blocks(&mut blocks, &event.run_id);
        let id: Arc<str> = format!("{}:{}:{}:{}", kind, event.run_id, phase, self.sequence).into();
        blocks.push(Block {
            id: id.clone(),
            kind: kind.into(),
            run_id: event.run_id.into(),
            content: if kind == "thinking" {
                normalize_thinking_content(&event.text)
            } else {
                event.text
            },
            state: "streaming".into(),
            text_phase: phase.into(),
            ..Default::default()
        });
        self.transcript.index_by_id.insert(id, blocks.len() - 1);
    }

    fn apply_tool_event(&mut self, event: DesktopEvent) {
        let id: Arc<str> = event.tool_call_id.clone().into();
        let title = event.data.get("name").cloned().unwrap_or_default();
        let extra = event
            .data
            .into_iter()
            .map(|(key, value)| (key, Value::String(value)))
            .collect::<HashMap<_, _>>();
        let mut blocks = self.transcript.blocks.borrow_mut();
        settle_streaming_text_blocks(&mut blocks, &event.run_id);
        if let Some(index) = self.transcript.index_by_id.get(&id).copied()
            && let Some(block) = blocks.get_mut(index)
        {
            block.content = event.text;
            block.state = event.state.into();
            block.extra.extend(extra);
            return;
        }
        let block = Block {
            id: id.clone(),
            kind: "tool".into(),
            run_id: event.run_id.into(),
            tool_call_id: id.clone(),
            title: title.into(),
            content: event.text,
            state: event.state.into(),
            extra,
            ..Default::default()
        };
        self.transcript.index_by_id.insert(id, blocks.len());
        blocks.push(block);
    }

    fn apply_git_event(&mut self, event: DesktopEvent) {
        self.workspace.branches = event.git_branches;
        self.workspace.dirty = event.workspace_dirty;
        self.workspace.branch = event.text.into();
        self.workspace.additions = parse_i64(event.data.get("additions"));
        self.workspace.deletions = parse_i64(event.data.get("deletions"));
        self.workspace.changed_files = parse_i64(event.data.get("changed_files"));
    }

    fn apply_pull_request_event(&mut self, value: Value) {
        if let Some(number) = value.get("number").and_then(Value::as_i64) {
            self.pull_requests.monitors.insert(number, value);
        }
    }

    fn apply_terminal_event(&mut self, value: Value) {
        let kind = value
            .get("kind")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let session = value.get("session").cloned().unwrap_or(Value::Null);
        let id = session
            .get("id")
            .and_then(Value::as_str)
            .unwrap_or_default();
        if kind == "terminal_exit" {
            self.terminals
                .sessions
                .retain(|item| item.get("id").and_then(Value::as_str) != Some(id));
            if self.terminals.active_id.as_ref() == id {
                self.terminals.active_id = "".into();
            }
        } else if !id.is_empty() {
            if self.terminals.active_id.is_empty() {
                self.terminals.active_id = id.to_string().into();
            }
            if let Some(index) = self
                .terminals
                .sessions
                .iter()
                .position(|item| item.get("id").and_then(Value::as_str) == Some(id))
            {
                self.terminals.sessions[index] = session;
            } else {
                self.terminals.sessions.push(session);
            }
        }
    }
}

impl TranscriptModel {
    fn rebuild_index(&mut self) {
        self.index_by_id.clear();
        for (index, block) in self.blocks.borrow().iter().enumerate() {
            self.index_by_id.insert(block.id.clone(), index);
        }
    }
}

pub(crate) fn unix_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.as_millis() as i64)
}

fn is_final_output_block(block: &Block) -> bool {
    block.kind.as_ref() == "error"
        || (block.kind.as_ref() == "assistant" && block.text_phase.as_ref() != "commentary")
}

fn value_str(value: &Value, key: &str) -> Arc<str> {
    value
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
        .into()
}

fn value_i64(value: &Value, key: &str) -> i64 {
    value
        .get(key)
        .and_then(|value| {
            value
                .as_i64()
                .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
        })
        .unwrap_or_default()
}

fn update_i64(values: &HashMap<String, String>, key: &str, target: &mut i64) {
    if let Some(value) = values.get(key).and_then(|value| value.parse().ok()) {
        *target = value;
    }
}

fn parse_string_json<T: for<'de> Deserialize<'de>>(value: Option<&String>) -> Vec<T> {
    value
        .and_then(|encoded| serde_json::from_str(encoded).ok())
        .unwrap_or_default()
}

fn value_str_map(values: &HashMap<String, String>, key: &str) -> Arc<str> {
    values.get(key).cloned().unwrap_or_default().into()
}

fn restored_session_blocks(data: &HashMap<String, String>) -> Vec<Block> {
    let mut blocks: Vec<Block> = parse_string_json(data.get("blocks"));
    for block in &mut blocks {
        if block.kind.as_ref() == "thinking" {
            block.content = normalize_thinking_content(&block.content);
        }
    }
    let sequences: Vec<i64> = parse_string_json(data.get("blockSequences"));
    let tools: Vec<Value> = parse_string_json(data.get("toolRecords"));
    if sequences.len() != blocks.len() {
        return append_restored_tools(blocks, tools);
    }
    let durable_tool_ids = tools
        .iter()
        .filter_map(|tool| tool.get("toolCallId").and_then(Value::as_str))
        .collect::<Vec<_>>();
    let mut ordered = blocks
        .into_iter()
        .zip(sequences)
        .enumerate()
        .filter(|(_, (block, _))| {
            block.tool_call_id.is_empty()
                || !durable_tool_ids.contains(&block.tool_call_id.as_ref())
        })
        .map(|(index, (mut block, sequence))| {
            block
                .extra
                .insert("sequence".to_string(), Value::Number(sequence.into()));
            (sequence, 0_u8, index, block)
        })
        .collect::<Vec<_>>();
    for (index, tool) in tools.into_iter().enumerate() {
        if let Some(block) = restored_tool_block(&tool) {
            let anchor = tool
                .get("anchorSequence")
                .and_then(Value::as_i64)
                .unwrap_or(i64::MAX);
            ordered.push((anchor, 1, index, block));
        }
    }
    ordered.sort_by_key(|(sequence, priority, index, _)| (*sequence, *priority, *index));
    ordered.into_iter().map(|(_, _, _, block)| block).collect()
}

fn append_thinking_content(existing: &mut String, next: &str) {
    let normalized;
    let next = if next.contains("****") {
        normalized = normalize_thinking_content(next);
        normalized.as_str()
    } else {
        next
    };
    if existing.trim_end_matches([' ', '\t']).ends_with("**")
        && next.trim_start_matches([' ', '\t']).starts_with("**")
    {
        existing.push_str("\n\n");
    }
    existing.push_str(next);
}

pub(super) fn normalize_thinking_content(content: &str) -> String {
    content.replace("****", "**\n\n**")
}

fn append_restored_tools(mut blocks: Vec<Block>, tools: Vec<Value>) -> Vec<Block> {
    for tool in tools {
        if let Some(block) = restored_tool_block(&tool) {
            if let Some(existing) = blocks
                .iter_mut()
                .find(|existing| existing.tool_call_id == block.tool_call_id)
            {
                *existing = block;
            } else {
                blocks.push(block);
            }
        }
    }
    blocks
}

fn restored_tool_block(tool: &Value) -> Option<Block> {
    let tool_call_id = tool.get("toolCallId").and_then(Value::as_str)?.to_string();
    let mut extra = HashMap::new();
    if let Some(fields) = tool.as_object() {
        extra.extend(
            fields
                .iter()
                .map(|(key, value)| (key.clone(), value.clone())),
        );
    }
    Some(Block {
        id: tool_call_id.clone().into(),
        kind: "tool".into(),
        run_id: tool
            .get("runId")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string()
            .into(),
        tool_call_id: tool_call_id.into(),
        title: tool
            .get("name")
            .and_then(Value::as_str)
            .unwrap_or("Tool")
            .to_string()
            .into(),
        content: tool
            .get("content")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string(),
        state: tool
            .get("state")
            .and_then(Value::as_str)
            .unwrap_or("complete")
            .to_string()
            .into(),
        extra,
        ..Default::default()
    })
}

fn restore_durable_controls(
    blocks: &[Block],
    session_id: &str,
    questions: &mut Vec<Value>,
    plans: &mut Vec<Value>,
) {
    for block in blocks {
        let data = block.extra.get("data").and_then(Value::as_object);
        if block.kind.as_ref() == "question"
            && matches!(block.state.as_ref(), "pending" | "interrupted")
        {
            let id = data
                .and_then(|data| data.get("userInputId"))
                .and_then(Value::as_str)
                .unwrap_or_default();
            let encoded = data
                .and_then(|data| data.get("questions"))
                .and_then(Value::as_str)
                .unwrap_or("[]");
            upsert_pending(
                questions,
                "userInputId",
                id,
                serde_json::json!({
                    "kind": "user_input_requested",
                    "sessionId": session_id,
                    "runId": block.run_id,
                    "userInputId": id,
                    "state": block.state,
                    "data": {"questions": encoded},
                }),
            );
        }
        if block.kind.as_ref() == "plan" && block.state.as_ref() == "proposed" {
            let id = data
                .and_then(|data| data.get("planId"))
                .and_then(Value::as_str)
                .unwrap_or_default();
            upsert_pending(
                plans,
                "planId",
                id,
                serde_json::json!({
                    "kind": "plan_proposed",
                    "sessionId": session_id,
                    "runId": block.run_id,
                    "planId": id,
                    "state": block.state,
                    "text": block.content,
                    "data": {"title": block.title},
                }),
            );
        }
    }
}

fn set_session_running(sessions: &mut [SessionSummary], session_id: &str, running: bool) {
    if let Some(session) = sessions
        .iter_mut()
        .find(|session| session.id.as_ref() == session_id)
    {
        session.running = running;
    }
}

fn retain_session_values(values: &mut Vec<Value>, session_id: &str) {
    values.retain(|value| value.get("sessionId").and_then(Value::as_str) == Some(session_id));
}

fn parse_i64(value: Option<&String>) -> i64 {
    value
        .and_then(|value| value.parse().ok())
        .unwrap_or_default()
}

fn event_value(event: &DesktopEvent) -> Value {
    serde_json::json!({
        "kind": event.kind,
        "sessionId": event.session_id,
        "agentId": event.agent_id,
        "runId": event.run_id,
        "state": event.state,
        "at": event.at,
        "approvalId": event.approval_id,
        "userInputId": event.user_input_id,
        "planId": event.plan_id,
        "toolCallId": event.tool_call_id,
        "text": event.text,
        "data": event.data,
    })
}

fn normalize_agent_snapshots(values: Vec<Value>) -> Vec<Value> {
    values.into_iter().map(normalize_agent_snapshot).collect()
}

fn normalize_agent_snapshot(value: Value) -> Value {
    let Some(wrapper) = value.as_object() else {
        return value;
    };
    let mut agent = wrapper
        .get("agent")
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_else(|| wrapper.clone());
    for key in ["id", "state", "summary", "parentRunId", "parentRunID"] {
        if let Some(field) = wrapper.get(key).filter(|field| !field.is_null()) {
            agent.insert(key.to_string(), field.clone());
        }
    }
    if let Some(id) = agent.get("id").cloned() {
        agent.insert("agentId".to_string(), id);
    }
    Value::Object(agent)
}

fn merge_agent_detail_blocks(current: &[Value], incoming: Vec<Value>) -> Vec<Value> {
    if incoming.is_empty() {
        return current.to_vec();
    }
    let mut merged = incoming;
    for live in current {
        let id = live.get("id").and_then(Value::as_str).unwrap_or_default();
        if let Some(block) = merged
            .iter_mut()
            .find(|block| block.get("id").and_then(Value::as_str) == Some(id))
        {
            let live_len = live
                .get("content")
                .or_else(|| live.get("text"))
                .and_then(Value::as_str)
                .map(str::len)
                .unwrap_or_default();
            let incoming_len = block
                .get("content")
                .or_else(|| block.get("text"))
                .and_then(Value::as_str)
                .map(str::len)
                .unwrap_or_default();
            if live_len > incoming_len {
                *block = live.clone();
            }
        } else {
            merged.push(live.clone());
        }
    }
    merged
}

fn project_context_usage(current: &Value, event: &DesktopEvent) -> Option<Value> {
    if !is_main_context_usage(event) {
        return None;
    }
    event
        .data
        .get("factSnapshot")
        .filter(|value| *value == "true")
        .and_then(|_| event.data.get("usageSnapshot"))
        .and_then(|usage| serde_json::from_str(usage).ok())
        .or_else(|| Some(merge_context_usage(current, event)))
}

fn is_main_context_usage(event: &DesktopEvent) -> bool {
    event
        .data
        .get("requestKind")
        .is_none_or(|value| value == "main")
        && !event
            .data
            .get("aggregateOnly")
            .is_some_and(|value| value == "true")
}

fn merge_context_usage(current: &Value, event: &DesktopEvent) -> Value {
    let mut usage = current.as_object().cloned().unwrap_or_default();
    for key in ["inputTokens", "outputTokens", "contextLimit"] {
        if let Some(value) = event
            .data
            .get(key)
            .and_then(|value| value.parse::<i64>().ok())
        {
            usage.insert(key.to_string(), Value::from(value));
        }
    }
    usage.insert(
        "reported".to_string(),
        Value::Bool(
            event.state == "reported" || current.get("reported") == Some(&Value::Bool(true)),
        ),
    );
    Value::Object(usage)
}

fn retain_unresolved(values: &mut Vec<Value>, key: &str, id: &str) {
    if !id.is_empty() {
        values.retain(|value| value.get(key).and_then(Value::as_str) != Some(id));
    }
}

fn settle_streaming_text_blocks(blocks: &mut [Block], run_id: &str) {
    for block in blocks {
        if block.run_id.as_ref() == run_id
            && matches!(block.kind.as_ref(), "assistant" | "thinking")
            && block.state.as_ref() == "streaming"
        {
            block.state = "completed".into();
        }
    }
}

fn remove_run_controls(values: &mut Vec<Value>, run_id: &str) {
    if !run_id.is_empty() {
        values.retain(|value| value.get("runId").and_then(Value::as_str) != Some(run_id));
    }
}

fn upsert_pending(values: &mut Vec<Value>, key: &str, id: &str, value: Value) {
    if id.is_empty() {
        return;
    }
    values.retain(|candidate| candidate.get(key).and_then(Value::as_str) != Some(id));
    values.push(value);
}

#[cfg(test)]
mod tests;
