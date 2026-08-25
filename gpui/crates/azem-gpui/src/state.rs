use std::{cell::RefCell, collections::HashMap, rc::Rc, sync::Arc};

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
    Work,
    Security,
    Usage,
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
    pub active_session_id: Arc<str>,
    pub activity: Arc<str>,
    pub plan_mode: bool,
    pub todo: Value,
    pub approvals: Vec<Value>,
    pub questions: Vec<Value>,
    pub plans: Vec<Value>,
    pub agents: Vec<Value>,
    pub agent_blocks: Vec<Value>,
    pub memories: Vec<Value>,
    pub background_logs: Value,
    pub background: Vec<Value>,
    pub context_profile: Value,
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
    pub agent_mode: Arc<str>,
    pub approval_mode: Arc<str>,
    pub queue_mode: Arc<str>,
    pub appearance: Value,
    pub usage: Value,
    pub recovery: Value,
    pub archive: Value,
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
    pub fn apply_reconnect_snapshot(&mut self, snapshot: Value) {
        if let Some(base) = snapshot.get("base") {
            self.workspace.root = value_str(base, "workspace");
            self.workspace.branch = value_str(base, "currentBranch");
            self.navigation.current_session_id = value_str(base, "sessionId");
            self.settings.language = value_str(base, "language");
            self.settings.provider = value_str(base, "provider");
            self.settings.model = value_str(base, "model");
            self.settings.reasoning = value_str(base, "reasoning");
            self.settings.agent_mode = value_str(base, "agentMode");
            self.settings.approval_mode = value_str(base, "approvalMode");
            self.settings.queue_mode = value_str(base, "queueMode");
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
        if let Ok(event) = decode_desktop_event(value) {
            self.apply_desktop_event(event);
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
                    set_session_running(&mut self.navigation.sessions, &event.session_id, true)
                }
                "run_finished" | "run_failed" | "run_cancelled" => {
                    set_session_running(&mut self.navigation.sessions, &event.session_id, false)
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
                    self.runtime.activity = "running".into();
                }
            }
            "run_finished" | "run_failed" | "run_cancelled" => {
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
                if is_current {
                    self.runtime.running = false;
                    self.runtime.run_id = "".into();
                    self.runtime.activity = event.state.into();
                    for block in self.transcript.blocks.borrow_mut().iter_mut() {
                        if block.state.as_ref() == "streaming" {
                            block.state = "complete".into();
                        }
                    }
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
                self.runtime.agents = event.agent_snapshots;
                self.runtime.agent_blocks = event.agent_blocks;
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
            "marketplace_catalog" => self.catalogs.marketplace = event.marketplace_catalog,
            "hook_catalog" => self.catalogs.hooks = event.hook_catalog,
            "hook_started" | "hook_finished" | "hook_diagnostic" => {
                self.catalogs.hooks = event_value(&event)
            }
            "context_profile" => self.runtime.context_profile = event.context_profile,
            "context_usage" => self.settings.usage = event_value(&event),
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
            "model_routes" => self.catalogs.routes = event.model_routes,
            "model_providers" => self.catalogs.providers = event.model_providers,
            "command_catalog" => {
                self.catalogs.commands = parse_string_json(event.data.get("commands"))
            }
            "theme_catalog" => self.catalogs.themes = parse_string_json(event.data.get("themes")),
            "mcp_state" => self.catalogs.mcp = event_value(&event),
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
                self.security.projection = event.security
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
        retain_session_values(&mut self.runtime.approvals, &loaded_session_id);
        retain_session_values(&mut self.runtime.questions, &loaded_session_id);
        retain_session_values(&mut self.runtime.plans, &loaded_session_id);
        self.navigation.current_session_id = event.session_id.into();
        for session in &mut self.navigation.sessions {
            if session.id == self.navigation.current_session_id {
                session.unread = false;
            }
        }
        self.navigation.current_title = event.data.get("title").cloned().unwrap_or_default().into();
        self.settings.provider = value_str_map(&event.data, "provider");
        self.settings.model = value_str_map(&event.data, "model");
        self.settings.reasoning = value_str_map(&event.data, "reasoning");
        self.settings.agent_mode = value_str_map(&event.data, "agentMode");
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
        if self.runtime.running {
            self.runtime.active_session_id = self.navigation.current_session_id.clone();
        }
        self.runtime.run_id = event
            .data
            .get("activeRunID")
            .cloned()
            .unwrap_or_default()
            .into();
        self.runtime.agents = event.agent_snapshots;
        self.runtime.agent_blocks = event.agent_blocks;
    }

    fn apply_agent_stream_event(&mut self, event: DesktopEvent) {
        let value = event_value(&event);
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
                let combined = format!(
                    "{}{}",
                    existing
                        .get("text")
                        .and_then(Value::as_str)
                        .unwrap_or_default(),
                    event.text
                );
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

    fn append_stream_text(&mut self, event: DesktopEvent, kind: &str) {
        let phase = event.text_phase.clone();
        let mut blocks = self.transcript.blocks.borrow_mut();
        if let Some(block) = blocks.last_mut()
            && block.kind.as_ref() == kind
            && block.run_id.as_ref() == event.run_id
            && block.text_phase.as_ref() == phase
            && block.state.as_ref() == "streaming"
        {
            block.content.push_str(&event.text);
            return;
        }
        let id: Arc<str> = format!("{}:{}:{}:{}", kind, event.run_id, phase, self.sequence).into();
        blocks.push(Block {
            id: id.clone(),
            kind: kind.into(),
            run_id: event.run_id.into(),
            content: event.text,
            state: "streaming".into(),
            text_phase: phase.into(),
            ..Default::default()
        });
        self.transcript.index_by_id.insert(id, blocks.len() - 1);
    }

    fn apply_tool_event(&mut self, event: DesktopEvent) {
        let id: Arc<str> = event.tool_call_id.clone().into();
        let mut blocks = self.transcript.blocks.borrow_mut();
        if let Some(index) = self.transcript.index_by_id.get(&id).copied()
            && let Some(block) = blocks.get_mut(index)
        {
            block.content = event.text;
            block.state = event.state.into();
            return;
        }
        let block = Block {
            id: id.clone(),
            kind: "tool".into(),
            run_id: event.run_id.into(),
            tool_call_id: id.clone(),
            title: event.data.get("name").cloned().unwrap_or_default().into(),
            content: event.text,
            state: event.state.into(),
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

fn value_str(value: &Value, key: &str) -> Arc<str> {
    value
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
        .into()
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
    let blocks: Vec<Block> = parse_string_json(data.get("blocks"));
    let sequences: Vec<i64> = parse_string_json(data.get("blockSequences"));
    let tools: Vec<Value> = parse_string_json(data.get("toolRecords"));
    if sequences.len() != blocks.len() {
        return append_restored_tools(blocks, tools);
    }
    let mut ordered = blocks
        .into_iter()
        .zip(sequences)
        .enumerate()
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

fn append_restored_tools(mut blocks: Vec<Block>, tools: Vec<Value>) -> Vec<Block> {
    for tool in tools {
        if let Some(block) = restored_tool_block(&tool)
            && !blocks
                .iter()
                .any(|existing| existing.tool_call_id == block.tool_call_id)
        {
            blocks.push(block);
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
        "approvalId": event.approval_id,
        "userInputId": event.user_input_id,
        "planId": event.plan_id,
        "toolCallId": event.tool_call_id,
        "text": event.text,
        "data": event.data,
    })
}

fn retain_unresolved(values: &mut Vec<Value>, key: &str, id: &str) {
    if !id.is_empty() {
        values.retain(|value| value.get(key).and_then(Value::as_str) != Some(id));
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
mod tests {
    use azem_ipc::{BinaryMetadata, Envelope};
    use serde_json::json;

    use super::AppState;

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
                "queueMode": "queue"
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
    fn session_and_project_lists_accept_rfc3339_timestamps() {
        let mut state = AppState::default();
        state.apply_direct_event(json!({
            "kind": "session_loaded",
            "state": "list",
            "data": {
                "sessions": "[{\"id\":\"session-1\",\"title\":\"One\",\"workspace\":\"/workspace\",\"updatedAt\":\"2026-08-25T00:00:00Z\",\"unread\":true}]",
                "projects": "[{\"workspace\":\"/workspace\",\"updatedAt\":\"2026-08-25T00:00:00Z\"}]"
            }
        }));
        assert_eq!(state.navigation.sessions.len(), 1);
        assert!(state.navigation.sessions[0].unread);
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
}
