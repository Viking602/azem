mod assets;
mod localization;
mod runtime_connection;
mod state;
mod surfaces;
mod terminal_emulator;
mod text_input;
mod theme;
use std::{
    cell::RefCell,
    collections::{HashMap, HashSet},
    path::PathBuf,
    rc::Rc,
    time::Instant,
};

use assets::Assets;
use azem_ipc::{ClientEvent, Method};
use gpui::{
    App, Bounds, BoxShadow, ClickEvent, Context, Entity, FocusHandle, Focusable, KeyBinding,
    ListAlignment, ListState, Role, ScrollHandle, Task, Window, WindowBounds, WindowHandle,
    WindowOptions, div, hsla, list, prelude::*, px, rgb, size,
};
use gpui_platform::application;
use localization::{Labels, labels};
use runtime_connection::{RuntimeConnection, RuntimeMessage, RuntimeOptions};
use serde_json::json;
use state::{AppState, Surface};
use surfaces::*;
use terminal_emulator::TerminalEmulator;
use text_input::TextInput;

use theme::ThemePalette;
gpui::actions!(azem, [Submit]);

#[derive(Clone, Copy)]
enum PendingRequest {
    ResumeSession,
    Search,
    Attachment,
    Entries,
    File,
    Changes,
    Change,
    PullRequests,
    PullRequestDetail,
    PullRequestMonitor,
    Usage,
    Terminals,
    CreateTerminal,
}

struct AzemWindow {
    focus: FocusHandle,
    state: AppState,
    runtime: RuntimeConnection,
    composer: Entity<TextInput>,
    search_input: Entity<TextInput>,
    model_search: Entity<TextInput>,
    model_picker_open: bool,
    settings_section: String,
    settings_open: bool,
    settings_provider: Option<String>,
    settings_provider_scroll: ScrollHandle,
    settings_model_scroll: ScrollHandle,
    environment_open: bool,
    environment_expanded: Option<String>,
    terminal_input: Entity<TextInput>,
    terminal_emulators: HashMap<String, TerminalEmulator>,
    process_expansion: Rc<RefCell<HashSet<String>>>,
    transcript_list: ListState,
    pending_requests: HashMap<String, PendingRequest>,
    open_projects: HashSet<String>,
    startup_started: Instant,
    snapshot_ready_logged: bool,
    provider_catalog_logged: bool,
    show_all_sessions: bool,
    _connection_task: Task<()>,
}

impl AzemWindow {
    fn new(window: &mut Window, cx: &mut Context<Self>, options: RuntimeOptions) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let composer = cx.new(|cx| TextInput::new(cx, "Describe what you want Azem to do…"));
        let search_input = cx.new(|cx| TextInput::new(cx, "Search conversations…"));
        let terminal_input = cx.new(|cx| TextInput::new(cx, "Terminal input"));
        let model_search = cx.new(|cx| TextInput::new(cx, "Search models…"));
        let runtime = RuntimeConnection::start(options);
        let transcript_list = ListState::new(0, ListAlignment::Bottom, px(800.));
        let messages = runtime.messages.clone();
        let connection_task = cx.spawn(async move |this, cx| {
            while let Ok(message) = messages.recv().await {
                if this
                    .update(cx, |this, cx| {
                        this.apply_runtime_message(message, cx);
                        cx.notify();
                    })
                    .is_err()
                {
                    return;
                }
            }
        });
        Self {
            focus,
            state: AppState::default(),
            runtime,
            composer,
            search_input,
            model_search,
            model_picker_open: false,
            settings_section: "catalog".to_string(),
            settings_open: false,
            settings_provider: None,
            settings_provider_scroll: ScrollHandle::new(),
            settings_model_scroll: ScrollHandle::new(),
            environment_open: true,
            environment_expanded: None,
            terminal_input,
            terminal_emulators: HashMap::new(),
            pending_requests: HashMap::new(),
            open_projects: HashSet::new(),
            process_expansion: Rc::new(RefCell::new(HashSet::new())),
            show_all_sessions: false,
            startup_started: Instant::now(),
            snapshot_ready_logged: false,
            provider_catalog_logged: false,
            _connection_task: connection_task,
            transcript_list,
        }
    }

    fn apply_runtime_message(&mut self, message: RuntimeMessage, cx: &mut Context<Self>) {
        let old_block_count = self.state.transcript.blocks.borrow().len();
        match message {
            RuntimeMessage::Connecting(message) => {
                self.state.connection.connected = false;
                self.state.connection.reconnecting = true;
                self.state.connection.message = message.into();
            }
            RuntimeMessage::Connected { pid, sequence } => {
                self.state.connection.connected = true;
                self.state.connection.reconnecting = false;
                self.state.connection.daemon_pid = pid;
                self.state.connection.message = "Connected".into();
                self.state.sequence = self.state.sequence.max(sequence);
            }
            RuntimeMessage::Snapshot(snapshot) => {
                self.state.apply_reconnect_snapshot(snapshot);
                let zh = self.state.settings.language.as_ref() == "zh-CN";
                if !self.snapshot_ready_logged {
                    tracing::info!(
                        interactive_ms = self.startup_started.elapsed().as_millis(),
                        "Azem GPUI state ready"
                    );
                    self.snapshot_ready_logged = true;
                }
                self.composer.update(cx, |composer, cx| {
                    composer.set_placeholder(
                        if zh {
                            "描述要完成的任务，@ 引用文件，/ 使用技能…"
                        } else {
                            "Describe the task, @ reference files, / use skills…"
                        },
                        cx,
                    )
                });
                self.search_input.update(cx, |search, cx| {
                    search.set_placeholder(
                        if zh {
                            "搜索设置、会话标题或对话内容…"
                        } else {
                            "Search conversations and messages…"
                        },
                        cx,
                    )
                });
                self.model_search.update(cx, |search, cx| {
                    search.set_placeholder(
                        if zh {
                            "搜索模型或原始 ID…"
                        } else {
                            "Search models or raw IDs…"
                        },
                        cx,
                    )
                });
                self.terminal_emulators.clear();
                for terminal in &self.state.terminals.sessions {
                    if let Some(id) = terminal.get("id").and_then(serde_json::Value::as_str) {
                        let mut emulator = TerminalEmulator::default();
                        emulator.resize(
                            terminal
                                .get("cols")
                                .and_then(serde_json::Value::as_u64)
                                .unwrap_or(120) as usize,
                            terminal
                                .get("rows")
                                .and_then(serde_json::Value::as_u64)
                                .unwrap_or(32) as usize,
                        );
                        self.terminal_emulators.insert(id.to_string(), emulator);
                        self.runtime
                            .request(Method::TerminalReplay, json!({"id": id}));
                    }
                }
                if self.state.terminals.active_id.is_empty()
                    && let Some(id) = self
                        .state
                        .terminals
                        .sessions
                        .first()
                        .and_then(|terminal| terminal.get("id"))
                        .and_then(serde_json::Value::as_str)
                {
                    self.state.terminals.active_id = id.to_string().into();
                }
            }
            RuntimeMessage::Event(ClientEvent::Envelope(envelope)) => {
                if envelope.channel == "runtime" {
                    match envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                    {
                        "model_providers" if !self.provider_catalog_logged => {
                            tracing::info!(
                                providers = envelope
                                    .payload
                                    .get("modelProviders")
                                    .and_then(serde_json::Value::as_array)
                                    .map(Vec::len)
                                    .unwrap_or_default(),
                                "GPUI model provider catalog ready"
                            );
                            self.provider_catalog_logged = true;
                        }
                        "model_providers" => {}
                        "bridge_error" => tracing::warn!(
                            error = envelope
                                .payload
                                .get("text")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default(),
                            "GPUI bridge projection failed"
                        ),
                        _ => {}
                    }
                }
                if envelope.channel == "daemon"
                    && let Some(workspace) = envelope
                        .payload
                        .get("workspace")
                        .and_then(serde_json::Value::as_str)
                {
                    let session_id = envelope
                        .payload
                        .get("sessionId")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default();
                    let _ = launch_gpui_window(workspace, session_id);
                }
                self.state.apply_envelope(*envelope)
            }
            RuntimeMessage::Event(ClientEvent::Binary(metadata, data)) => {
                if metadata.purpose == "terminal_output" {
                    self.terminal_emulators
                        .entry(metadata.transfer_id.clone())
                        .or_default()
                        .feed(&data);
                }
                self.state.apply_terminal_binary(metadata, &data)
            }
            RuntimeMessage::Event(ClientEvent::ResyncRequired { reason, .. })
            | RuntimeMessage::Event(ClientEvent::Disconnected(reason)) => {
                self.state.connection.connected = false;
                self.state.connection.reconnecting = true;
                self.state.connection.message = reason.into();
            }
            RuntimeMessage::Response { id, result } => match result {
                Ok(value) => {
                    if let Some(pending) = self.pending_requests.remove(&id) {
                        match pending {
                            PendingRequest::ResumeSession => self.state.apply_direct_event(value),
                            PendingRequest::Search => {
                                self.state.navigation.search_results =
                                    value.as_array().cloned().unwrap_or_default();
                                self.state.navigation.search_error = "".into();
                            }
                            PendingRequest::Attachment => {
                                self.state.transcript.attachments.push(value)
                            }
                            PendingRequest::Entries => self.state.workspace.file_tree = value,
                            PendingRequest::File => self.state.workspace.selected_file = value,
                            PendingRequest::Changes => self.state.workspace.changes = value,
                            PendingRequest::Change => self.state.workspace.selected_file = value,
                            PendingRequest::PullRequests => {
                                self.state.pull_requests.dashboard = value
                            }
                            PendingRequest::PullRequestDetail => {
                                self.state.pull_requests.selected = value
                            }
                            PendingRequest::Usage => self.state.settings.usage = value,
                            PendingRequest::PullRequestMonitor => {
                                if let Some(number) =
                                    value.get("number").and_then(serde_json::Value::as_i64)
                                {
                                    self.state.pull_requests.monitors.insert(number, value);
                                }
                            }
                            PendingRequest::CreateTerminal => {
                                if let Some(id) = value
                                    .get("id")
                                    .and_then(serde_json::Value::as_str)
                                    .map(str::to_string)
                                {
                                    let mut emulator = TerminalEmulator::default();
                                    emulator.resize(
                                        value
                                            .get("cols")
                                            .and_then(serde_json::Value::as_u64)
                                            .unwrap_or(120)
                                            as usize,
                                        value
                                            .get("rows")
                                            .and_then(serde_json::Value::as_u64)
                                            .unwrap_or(32)
                                            as usize,
                                    );
                                    self.state.terminals.active_id = id.clone().into();
                                    self.state.terminals.sessions.push(value);
                                    self.terminal_emulators.insert(id, emulator);
                                }
                            }
                            PendingRequest::Terminals => {
                                self.state.terminals.sessions =
                                    value.as_array().cloned().unwrap_or_default();
                                if self.state.terminals.active_id.is_empty()
                                    && let Some(id) = self
                                        .state
                                        .terminals
                                        .sessions
                                        .first()
                                        .and_then(|session| session.get("id"))
                                        .and_then(serde_json::Value::as_str)
                                {
                                    self.state.terminals.active_id = id.to_string().into();
                                }
                            }
                        }
                    }
                }
                Err(error) => {
                    let pending = self.pending_requests.remove(&id);
                    if matches!(pending, Some(PendingRequest::Search)) {
                        self.state.navigation.search_error = error.into();
                    } else {
                        tracing::warn!(request_id = id, %error, "GPUI request failed");
                        self.state.settings.error = format!("{id}: {error}").into();
                    }
                }
            },
        }
        let new_block_count = self.state.transcript.blocks.borrow().len();
        if old_block_count != new_block_count {
            self.transcript_list.reset(new_block_count);
        }
    }

    fn send_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.send_current(cx);
    }

    fn submit_message(&mut self, _: &Submit, _: &mut Window, cx: &mut Context<Self>) {
        match self.state.navigation.surface {
            Surface::Terminal => self.write_terminal_current(cx),
            Surface::Search => self.search_current(cx),
            _ => self.send_current(cx),
        }
    }

    fn send_current(&mut self, cx: &mut Context<Self>) {
        let prompt = self.composer.read(cx).text().trim().to_string();
        if prompt.is_empty() || !self.state.connection.connected {
            return;
        }
        let session_id = self.state.navigation.current_session_id.to_string();
        if self.state.runtime.running {
            self.runtime.request(
                Method::FollowUp,
                json!({
                    "sessionId": session_id,
                    "runId": self.state.runtime.run_id,
                    "text": prompt,
                    "attachments": self.state.transcript.attachments,
                }),
            );
        } else {
            self.runtime.request(
                Method::StartTurn,
                json!({
                    "sessionId": session_id,
                    "prompt": prompt,
                    "provider": self.state.settings.provider,
                    "model": self.state.settings.model,
                    "reasoning": self.state.settings.reasoning,
                    "agentMode": self.state.settings.agent_mode,
                    "planMode": self.state.runtime.plan_mode,
                    "disableSubagents": false,
                    "activeSkills": [],
                    "images": self.state.transcript.attachments,
                }),
            );
        }
        self.composer.update(cx, |composer, cx| composer.clear(cx));
    }

    fn guide_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let text = self.composer.read(cx).text().trim().to_string();
        if text.is_empty() || !self.state.runtime.running {
            return;
        }
        self.runtime.request(
            Method::Guide,
            json!({
                "sessionId": self.state.navigation.current_session_id,
                "runId": self.state.runtime.run_id,
                "text": text,
                "attachments": self.state.transcript.attachments,
            }),
        );
        self.composer.update(cx, |composer, cx| composer.clear(cx));
    }

    fn attach_file(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let runtime = self.runtime.clone();
        let session_id = self.state.navigation.current_session_id.to_string();
        cx.spawn(async move |this, cx| {
            if let Some(file) = rfd::AsyncFileDialog::new()
                .add_filter("Images", &["png", "jpg", "jpeg", "gif", "webp"])
                .pick_file()
                .await
            {
                let path = file.path().to_path_buf();
                let mime_type = match path
                    .extension()
                    .and_then(|extension| extension.to_str())
                    .unwrap_or_default()
                    .to_ascii_lowercase()
                    .as_str()
                {
                    "png" => "image/png",
                    "jpg" | "jpeg" => "image/jpeg",
                    "gif" => "image/gif",
                    "webp" => "image/webp",
                    _ => return,
                };
                let id =
                    runtime.upload_attachment(session_id, path, file.file_name(), mime_type.into());
                let _ = this.update(cx, |this, cx| {
                    this.pending_requests.insert(id, PendingRequest::Attachment);
                    cx.notify();
                });
            }
        })
        .detach();
    }

    fn cancel_active(&mut self, _: &ClickEvent, _: &mut Window, _: &mut Context<Self>) {
        self.runtime
            .request(Method::CancelActive, json!({"includeChildren": true}));
    }

    fn new_session(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.runtime
            .request(Method::Execute, json!({"kind": "new_session"}));
        self.state.navigation.surface = Surface::Thread;
        cx.notify();
    }

    fn search_current(&mut self, cx: &mut Context<Self>) {
        let query = self.search_input.read(cx).text().trim().to_string();
        if query.is_empty() || !self.state.connection.connected {
            return;
        }
        self.state.navigation.search_error = "".into();
        let id = self
            .runtime
            .request(Method::SearchSessions, json!({"query": query, "limit": 30}));
        self.pending_requests.insert(id, PendingRequest::Search);
    }

    fn search_click(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.search_current(cx);
    }

    fn cycle_approval(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        if self.state.runtime.running {
            return;
        }
        let next = match self.state.settings.approval_mode.as_ref() {
            "prompt" => "auto_review",
            "auto_review" => "yolo",
            _ => "prompt",
        };
        self.state.settings.approval_mode = next.into();
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": "set_approval_mode",
                "target": next,
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
        cx.notify();
    }

    fn toggle_plan(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        if !self.state.runtime.running {
            self.state.runtime.plan_mode = !self.state.runtime.plan_mode;
            cx.notify();
        }
    }

    fn cycle_reasoning(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let levels = ["none", "low", "medium", "high", "xhigh"];
        let current = levels
            .iter()
            .position(|level| *level == self.state.settings.reasoning.as_ref())
            .unwrap_or(2);
        self.state.settings.reasoning = levels[(current + 1) % levels.len()].into();
        cx.notify();
    }

    fn toggle_model_picker(&mut self, _: &ClickEvent, window: &mut Window, cx: &mut Context<Self>) {
        self.model_picker_open = !self.model_picker_open;
        if self.model_picker_open {
            if self.state.catalogs.providers.is_empty() {
                self.refresh_model_catalog();
            }
            self.model_search.update(cx, |search, cx| search.clear(cx));
            self.model_search.focus_handle(cx).focus(window, cx);
        }
        cx.notify();
    }

    fn refresh_model_catalog(&self) {
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": "list_model_providers",
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
    }

    fn model_picker_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let query = self
            .model_search
            .read(cx)
            .text()
            .trim()
            .to_ascii_lowercase();
        let selected_provider = self.state.settings.provider.to_string();
        let selected_model = self.state.settings.model.to_string();
        let mut groups = Vec::new();
        for provider in self.state.catalogs.providers.clone() {
            if provider.get("enabled").and_then(serde_json::Value::as_bool) == Some(false) {
                continue;
            }
            let provider_id = provider
                .get("id")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_string();
            if provider_id.is_empty() {
                continue;
            }
            let provider_name = ["displayName", "name"]
                .into_iter()
                .find_map(|key| {
                    provider
                        .get(key)
                        .and_then(serde_json::Value::as_str)
                        .filter(|name| !name.trim().is_empty())
                })
                .unwrap_or(&provider_id)
                .to_string();
            let mut rows = Vec::new();
            if let Some(models) = provider.get("models").and_then(serde_json::Value::as_array) {
                for model in models {
                    if model
                        .get("disabled")
                        .and_then(serde_json::Value::as_bool)
                        .unwrap_or(false)
                    {
                        continue;
                    }
                    let model_id = model
                        .get("id")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .to_string();
                    if model_id.is_empty() {
                        continue;
                    }
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
                    let aliases = model
                        .get("aliases")
                        .and_then(serde_json::Value::as_array)
                        .map(|values| {
                            values
                                .iter()
                                .filter_map(serde_json::Value::as_str)
                                .collect::<Vec<_>>()
                                .join(" ")
                        })
                        .unwrap_or_default();
                    let searchable =
                        format!("{} {} {} {}", provider_id, provider_name, model_id, aliases)
                            .to_ascii_lowercase();
                    if !query.is_empty()
                        && !searchable.contains(&query)
                        && !model_name.to_ascii_lowercase().contains(&query)
                    {
                        continue;
                    }
                    let reasoning = model
                        .get("reasoningLevels")
                        .and_then(serde_json::Value::as_array)
                        .map(|levels| {
                            levels
                                .iter()
                                .filter_map(serde_json::Value::as_str)
                                .collect::<Vec<_>>()
                                .join(" · ")
                        })
                        .unwrap_or_default();
                    let context = model
                        .get("contextWindow")
                        .and_then(serde_json::Value::as_u64)
                        .map(|tokens| {
                            if tokens >= 1_000_000 {
                                format!("{}M", tokens / 1_000_000)
                            } else {
                                format!("{}k", tokens / 1_000)
                            }
                        })
                        .unwrap_or_default();
                    let metadata = [context, reasoning]
                        .into_iter()
                        .filter(|value| !value.is_empty())
                        .collect::<Vec<_>>()
                        .join(" · ");
                    let active = provider_id == selected_provider && model_id == selected_model;
                    let selected_provider_id = provider_id.clone();
                    let selected_model_id = model_id.clone();
                    let row_id = format!("model-option-{provider_id}-{model_id}");
                    rows.push(
                        div()
                            .id(row_id)
                            .role(Role::ListItem)
                            .aria_selected(active)
                            .tab_stop(true)
                            .min_h(px(48.))
                            .px_2()
                            .py_2()
                            .rounded(px(8.))
                            .bg(if active {
                                palette.accent_soft
                            } else {
                                palette.paper
                            })
                            .flex()
                            .items_center()
                            .gap_2()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.state.settings.provider = selected_provider_id.clone().into();
                                this.state.settings.model = selected_model_id.clone().into();
                                this.model_picker_open = false;
                                cx.notify();
                            }))
                            .child(provider_logo(&provider_id, 18., palette.ink))
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
                                            .child(model_name),
                                    )
                                    .when(!metadata.is_empty(), |row| {
                                        row.child(
                                            div()
                                                .truncate()
                                                .text_color(palette.faint)
                                                .text_xs()
                                                .child(metadata),
                                        )
                                    }),
                            )
                            .when(active, |row| row.child(icon("check", 15., palette.accent)))
                            .into_any_element(),
                    );
                }
            }
            if rows.is_empty() {
                continue;
            }
            let count = rows.len();
            groups.push(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(
                        div()
                            .h(px(28.))
                            .px_2()
                            .flex()
                            .items_center()
                            .gap_2()
                            .text_color(palette.faint)
                            .text_xs()
                            .child(provider_logo(&provider_id, 14., palette.faint))
                            .child(provider_name)
                            .child(div().flex_1())
                            .child(count.to_string()),
                    )
                    .children(rows)
                    .into_any_element(),
            );
        }
        let no_results = groups.is_empty();
        let catalog_loading = self.state.catalogs.providers.is_empty();
        let no_results_label = if catalog_loading {
            if self.state.settings.language.as_ref() == "zh-CN" {
                "正在加载模型目录…"
            } else {
                "Loading model catalog…"
            }
        } else if self.state.settings.language.as_ref() == "zh-CN" {
            "没有匹配的可用模型"
        } else {
            "No available models match"
        };
        div()
            .id("model-picker")
            .role(Role::ListBox)
            .aria_label(if self.state.settings.language.as_ref() == "zh-CN" {
                "模型选择器"
            } else {
                "Model picker"
            })
            .absolute()
            .right(px(40.))
            .bottom(px(45.))
            .w(px(420.))
            .max_h(px(470.))
            .rounded(px(14.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.15, 0.15, 0.15))
                    .blur_radius(px(30.)),
            ])
            .p_2()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .h(px(38.))
                    .rounded(px(9.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper_muted)
                    .flex()
                    .items_center()
                    .px_2()
                    .gap_2()
                    .child(icon("search", 15., palette.faint))
                    .child(div().flex_1().h_full().child(self.model_search.clone())),
            )
            .child(
                div()
                    .id("model-picker-list")
                    .max_h(px(352.))
                    .overflow_y_scroll()
                    .flex()
                    .flex_col()
                    .gap_2()
                    .when(no_results, |list| {
                        list.child(
                            div()
                                .h(px(112.))
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_color(palette.faint)
                                .text_sm()
                                .child(no_results_label),
                        )
                    })
                    .children(groups),
            )
            .child(
                div()
                    .id("configure-models")
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
                        this.model_picker_open = false;
                        this.settings_section = "catalog".to_string();
                        this.settings_provider = Some(this.state.settings.provider.to_string());
                        this.settings_open = true;
                        if this.state.catalogs.providers.is_empty() {
                            this.refresh_model_catalog();
                        }
                        cx.notify();
                    }))
                    .child(icon("settings", 14., palette.muted))
                    .child(if self.state.settings.language.as_ref() == "zh-CN" {
                        "管理模型与提供商"
                    } else {
                        "Manage models and providers"
                    }),
            )
            .into_any_element()
    }

    fn settings_modal_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let close_label = if self.state.settings.language.as_ref() == "zh-CN" {
            "关闭设置"
        } else {
            "Close settings"
        };
        let content = settings_surface(
            &self.state,
            palette,
            self.settings_section.as_str(),
            self.settings_provider.as_deref().unwrap_or_default(),
            self.settings_provider_scroll.clone(),
            self.settings_model_scroll.clone(),
            cx,
        );
        let dialog = self.settings_dialog(content, close_label, palette, cx);
        div()
            .id("settings-modal-backdrop")
            .role(Role::Region)
            .aria_label(close_label)
            .absolute()
            .size_full()
            .p(px(38.))
            .bg(hsla(220. / 360., 0.08, 0.18, 0.26))
            .flex()
            .items_center()
            .justify_center()
            .child(dialog)
            .into_any_element()
    }

    fn settings_dialog(
        &mut self,
        content: gpui::AnyElement,
        close_label: &'static str,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let title = if self.state.settings.language.as_ref() == "zh-CN" {
            "设置与扩展"
        } else {
            "Settings and extensions"
        };
        div()
            .id("settings-modal")
            .role(Role::Region)
            .aria_label(title)
            .relative()
            .w_full()
            .h_full()
            .max_w(px(1240.))
            .max_h(px(790.))
            .rounded(px(22.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(0.), px(18.), hsla(220. / 360., 0.15, 0.12, 0.22))
                    .blur_radius(px(48.)),
            ])
            .overflow_hidden()
            .child(content)
            .child(self.settings_close_button(close_label, palette, cx))
            .into_any_element()
    }

    fn settings_close_button(
        &mut self,
        close_label: &'static str,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        div()
            .id("close-settings")
            .role(Role::Button)
            .aria_label(close_label)
            .tab_stop(true)
            .absolute()
            .top(px(14.))
            .right(px(14.))
            .size(px(30.))
            .rounded(px(8.))
            .bg(palette.paper)
            .text_color(palette.muted)
            .flex()
            .items_center()
            .justify_center()
            .cursor_pointer()
            .hover(move |style| style.bg(palette.hover))
            .on_click(cx.listener(|this, _, _, cx| {
                this.settings_open = false;
                cx.notify();
            }))
            .child("×")
            .into_any_element()
    }

    fn composer_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        expanded: bool,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let prompt_empty = self.composer.read(cx).text().trim().is_empty();
        let attachment_count = self.state.transcript.attachments.len();
        let project_name = std::path::Path::new(self.state.workspace.root.as_ref())
            .file_name()
            .and_then(|name| name.to_str())
            .filter(|name| !name.is_empty())
            .unwrap_or("workspace")
            .to_string();
        let branch = if self.state.workspace.branch.is_empty() {
            "workspace".to_string()
        } else {
            self.state.workspace.branch.to_string()
        };
        let approval = match self.state.settings.approval_mode.as_ref() {
            "auto_review" => labels.auto_review,
            "yolo" => "YOLO",
            _ => {
                if self.state.settings.language.as_ref() == "zh-CN" {
                    "逐次确认"
                } else {
                    "Ask"
                }
            }
        };
        let model = if self.state.settings.model.is_empty() {
            if self.state.settings.language.as_ref() == "zh-CN" {
                "选择模型".to_string()
            } else {
                "Select model".to_string()
            }
        } else {
            self.state.settings.model.to_string()
        };
        let current_provider = self.state.settings.provider.to_string();
        let reasoning = if self.state.settings.reasoning.is_empty() {
            "default".to_string()
        } else {
            self.state.settings.reasoning.to_string()
        };
        let show_cancel = self.state.runtime.running && prompt_empty && attachment_count == 0;
        let send_control = if show_cancel {
            div()
                .id("cancel-active")
                .role(Role::Button)
                .aria_label(labels.stop)
                .tab_stop(true)
                .size(px(32.))
                .rounded_full()
                .bg(palette.danger)
                .text_color(rgb(0xffffff))
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .on_click(cx.listener(Self::cancel_active))
                .child(icon("square", 13., rgb(0xffffff)))
                .into_any_element()
        } else {
            let idle = prompt_empty && attachment_count == 0;
            div()
                .id("send-message")
                .role(Role::Button)
                .aria_label(if self.state.runtime.running {
                    labels.queue
                } else {
                    labels.send
                })
                .tab_stop(true)
                .size(px(32.))
                .rounded_full()
                .bg(if idle {
                    palette.paper_muted
                } else {
                    palette.ink
                })
                .text_color(if idle { palette.faint } else { palette.paper })
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .on_click(cx.listener(Self::send_message))
                .child(icon(
                    "arrow-up",
                    16.,
                    if idle { palette.faint } else { palette.paper },
                ))
                .into_any_element()
        };
        let picker = self.model_picker_view(palette, labels, cx);
        let composer_shell = div()
            .id("composer")
            .role(Role::Group)
            .aria_label(labels.composer)
            .w_full()
            .max_w(px(840.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .rounded(px(18.))
            .shadow(vec![
                BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.15, 0.15, 0.08))
                    .blur_radius(px(10.)),
            ])
            .overflow_hidden()
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(40.))
                    .px_3()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(
                        div()
                            .h(px(29.))
                            .px_2()
                            .rounded_full()
                            .bg(palette.paper_muted)
                            .text_color(palette.muted)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .child(icon("box", 13., palette.muted))
                            .child(project_name),
                    )
                    .child(
                        div()
                            .h(px(29.))
                            .px_2()
                            .rounded_full()
                            .bg(palette.paper_muted)
                            .text_color(palette.muted)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .child("⌘")
                            .child(branch)
                            .child(icon("chevron-down", 12., palette.faint)),
                    ),
            )
            .child(
                div()
                    .h(if expanded { px(82.) } else { px(60.) })
                    .px_1()
                    .child(self.composer.clone()),
            )
            .child(
                div()
                    .h(px(46.))
                    .px_2()
                    .pb_2()
                    .flex()
                    .items_center()
                    .gap_1()
                    .child(
                        div()
                            .id("attach-file")
                            .role(Role::Button)
                            .aria_label(labels.attach)
                            .tab_stop(true)
                            .size(px(32.))
                            .rounded_full()
                            .text_color(palette.ink_soft)
                            .text_lg()
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::attach_file))
                            .child(if attachment_count == 0 {
                                "+".to_string()
                            } else {
                                format!("+{attachment_count}")
                            }),
                    )
                    .child(
                        div()
                            .id("approval-mode")
                            .role(Role::Button)
                            .aria_label(approval)
                            .tab_stop(!self.state.runtime.running)
                            .h(px(32.))
                            .px_2()
                            .rounded_full()
                            .text_color(if self.state.runtime.running {
                                palette.faint
                            } else {
                                palette.ink_soft
                            })
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::cycle_approval))
                            .child(icon("shield-check", 15., palette.ink_soft))
                            .child(approval)
                            .child(icon("chevron-down", 11., palette.faint)),
                    )
                    .child(
                        div()
                            .id("plan-mode")
                            .role(Role::Button)
                            .aria_label(labels.plan)
                            .aria_selected(self.state.runtime.plan_mode)
                            .tab_stop(!self.state.runtime.running)
                            .h(px(32.))
                            .px_2()
                            .rounded_full()
                            .bg(if self.state.runtime.plan_mode {
                                palette.accent_soft
                            } else {
                                palette.paper
                            })
                            .text_color(if self.state.runtime.plan_mode {
                                palette.accent
                            } else {
                                palette.ink_soft
                            })
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::toggle_plan))
                            .child(icon(
                                "lightbulb",
                                15.,
                                if self.state.runtime.plan_mode {
                                    palette.accent
                                } else {
                                    palette.ink_soft
                                },
                            ))
                            .child(labels.plan),
                    )
                    .when(self.state.runtime.running, |toolbar| {
                        toolbar.child(
                            div()
                                .id("guide-message")
                                .role(Role::Button)
                                .aria_label(labels.guide)
                                .tab_stop(true)
                                .h(px(32.))
                                .px_2()
                                .rounded_full()
                                .text_color(palette.accent)
                                .text_xs()
                                .flex()
                                .items_center()
                                .gap_1()
                                .cursor_pointer()
                                .hover(move |style| style.bg(palette.accent_soft))
                                .on_click(cx.listener(Self::guide_message))
                                .child("↳")
                                .child(labels.guide),
                        )
                    })
                    .child(div().flex_1())
                    .child(
                        div()
                            .size(px(28.))
                            .text_color(palette.faint)
                            .text_sm()
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(icon("circle", 13., palette.faint)),
                    )
                    .child(
                        div()
                            .id("model-picker-toggle")
                            .role(Role::Button)
                            .aria_label(model.clone())
                            .aria_expanded(self.model_picker_open)
                            .tab_stop(true)
                            .h(px(32.))
                            .max_w(px(230.))
                            .px_2()
                            .rounded_full()
                            .bg(if self.model_picker_open {
                                palette.paper_muted
                            } else {
                                palette.paper
                            })
                            .text_color(palette.ink)
                            .text_xs()
                            .flex()
                            .items_center()
                            .gap_1()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::toggle_model_picker))
                            .child(provider_logo(&current_provider, 15., palette.ink))
                            .child(div().min_w_0().truncate().child(model))
                            .child(icon("chevron-down", 11., palette.faint)),
                    )
                    .child(
                        div()
                            .id("reasoning-cycle")
                            .role(Role::Button)
                            .aria_label(reasoning.clone())
                            .tab_stop(true)
                            .h(px(32.))
                            .px_1()
                            .rounded_full()
                            .text_color(palette.muted)
                            .text_xs()
                            .flex()
                            .items_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.paper_muted))
                            .on_click(cx.listener(Self::cycle_reasoning))
                            .child(reasoning),
                    )
                    .child(send_control),
            );
        div()
            .id("composer-shell")
            .relative()
            .w_full()
            .max_w(px(840.))
            .child(composer_shell)
            .when(self.model_picker_open, |shell| shell.child(picker))
            .into_any_element()
    }

    fn create_terminal(&mut self, _: &ClickEvent, _: &mut Window, _: &mut Context<Self>) {
        let id = self
            .runtime
            .request(Method::CreateTerminal, json!({"cols": 120, "rows": 32}));
        self.pending_requests
            .insert(id, PendingRequest::CreateTerminal);
    }

    fn close_terminal(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let id = self.state.terminals.active_id.to_string();
        if id.is_empty() {
            return;
        }
        self.runtime
            .request(Method::CloseTerminal, json!({"id": id}));
        self.terminal_emulators.remove(&id);
        self.state
            .terminals
            .sessions
            .retain(|session| session.get("id").and_then(serde_json::Value::as_str) != Some(&id));
        self.state.terminals.active_id = self
            .state
            .terminals
            .sessions
            .first()
            .and_then(|session| session.get("id"))
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default()
            .to_string()
            .into();
        cx.notify();
    }

    fn write_terminal_current(&mut self, cx: &mut Context<Self>) {
        let terminal_id = self.state.terminals.active_id.to_string();
        let data = self.terminal_input.read(cx).text().to_string();
        if terminal_id.is_empty() || data.is_empty() {
            return;
        }
        self.runtime.request(
            Method::WriteTerminal,
            json!({"id": terminal_id, "data": format!("{data}\n")}),
        );
        self.terminal_input
            .update(cx, |terminal_input, cx| terminal_input.clear(cx));
    }

    fn write_terminal(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.write_terminal_current(cx);
    }

    fn request_surface(&mut self, surface: Surface) {
        let (method, payload, pending) = match surface {
            Surface::Files => (
                Method::WorkspaceEntries,
                json!({"path": ""}),
                PendingRequest::Entries,
            ),
            Surface::Changes => (Method::WorkspaceChanges, json!({}), PendingRequest::Changes),
            Surface::PullRequests => (
                Method::PullRequestDashboard,
                json!({}),
                PendingRequest::PullRequests,
            ),
            Surface::Usage => (
                Method::UsageReport,
                json!({"scope": "all"}),
                PendingRequest::Usage,
            ),
            Surface::Terminal => (Method::ListTerminals, json!({}), PendingRequest::Terminals),
            _ => return,
        };
        let id = self.runtime.request(method, payload);
        self.pending_requests.insert(id, pending);
    }

    fn terminal_view(&mut self, palette: ThemePalette, cx: &mut Context<Self>) -> gpui::AnyElement {
        let visible = self
            .terminal_emulators
            .get(self.state.terminals.active_id.as_ref())
            .map(TerminalEmulator::visible_text)
            .unwrap_or_default();
        div()
            .id("terminal-surface")
            .role(Role::Region)
            .aria_label("Embedded terminal")
            .size_full()
            .bg(palette.paper)
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(44.))
                    .px_3()
                    .border_b_1()
                    .border_color(palette.border)
                    .flex()
                    .items_center()
                    .gap_1()
                    .children(self.state.terminals.sessions.iter().enumerate().map(
                        |(index, session)| {
                            let id = session
                                .get("id")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string();
                            let label = session
                                .get("title")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or("Terminal")
                                .to_string();
                            let selected = self.state.terminals.active_id.as_ref() == id.as_str();
                            div()
                                .id(("terminal-tab", index))
                                .role(Role::Tab)
                                .aria_label(label.clone())
                                .aria_selected(selected)
                                .tab_stop(true)
                                .h(px(30.))
                                .px_3()
                                .rounded(px(8.))
                                .bg(if selected {
                                    palette.hover
                                } else {
                                    palette.paper
                                })
                                .text_color(if selected { palette.ink } else { palette.muted })
                                .text_xs()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.state.terminals.active_id = id.clone().into();
                                    cx.notify();
                                }))
                                .child(label)
                        },
                    ))
                    .child(div().flex_1())
                    .child(
                        div()
                            .id("create-terminal")
                            .role(Role::Button)
                            .aria_label("Create terminal")
                            .tab_stop(true)
                            .size(px(30.))
                            .rounded(px(8.))
                            .text_color(palette.muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(Self::create_terminal))
                            .child("+"),
                    )
                    .child(
                        div()
                            .id("close-terminal")
                            .role(Role::Button)
                            .aria_label("Close terminal")
                            .tab_stop(true)
                            .size(px(30.))
                            .rounded(px(8.))
                            .text_color(palette.muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(Self::close_terminal))
                            .child("×"),
                    ),
            )
            .child(
                div()
                    .id("terminal-output")
                    .role(Role::Log)
                    .aria_label("Terminal output")
                    .flex_1()
                    .overflow_y_scroll()
                    .bg(rgb(0x171817))
                    .text_color(rgb(0xe8e8e5))
                    .font_family("SF Mono")
                    .text_sm()
                    .p_4()
                    .whitespace_normal()
                    .child(visible),
            )
            .child(
                div()
                    .h(px(48.))
                    .px_2()
                    .border_t_1()
                    .border_color(palette.border)
                    .bg(palette.paper)
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(self.terminal_input.clone())
                    .child(
                        div()
                            .id("terminal-send")
                            .role(Role::Button)
                            .aria_label("Send terminal input")
                            .tab_stop(true)
                            .size(px(32.))
                            .rounded_full()
                            .bg(palette.ink)
                            .text_color(palette.paper)
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .on_click(cx.listener(Self::write_terminal))
                            .child(icon("arrow-up", 16., palette.paper)),
                    ),
            )
            .into_any_element()
    }
}

impl Drop for AzemWindow {
    fn drop(&mut self) {
        self.runtime.disconnect();
    }
}

impl Render for AzemWindow {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let palette = ThemePalette::for_window(window);
        let labels = labels(&self.state.settings.language);
        let surface = self.state.navigation.surface;
        let block_count = self.state.transcript.blocks.borrow().len();
        let empty_thread =
            surface == Surface::Thread && block_count == 0 && !self.state.runtime.running;
        let surface_title = match surface {
            Surface::Thread if !self.state.navigation.current_title.is_empty() => {
                self.state.navigation.current_title.to_string()
            }
            Surface::Thread => labels.new_conversation.to_string(),
            Surface::Search => labels.search.to_string(),
            Surface::Projects => labels.workspace.to_string(),
            Surface::Files => labels.files.to_string(),
            Surface::Changes => labels.changes.to_string(),
            Surface::PullRequests => labels.pull_requests.to_string(),
            Surface::Work => labels.run_controls.to_string(),
            Surface::Security => labels.security.to_string(),
            Surface::Terminal => labels.terminal.to_string(),
            Surface::Usage => labels.usage.to_string(),
        };
        let header_project = std::path::Path::new(self.state.workspace.root.as_ref())
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("workspace")
            .to_string();
        let header_branch = self.state.workspace.branch.to_string();
        let content = match surface {
            Surface::Thread if empty_thread => {
                let composer = self.composer_view(palette, labels, true, cx);
                div()
                    .id("empty-thread")
                    .role(Role::Region)
                    .aria_label(labels.new_conversation)
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .bg(palette.paper)
                    .px(px(48.))
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(
                        div()
                            .w_full()
                            .max_w(px(840.))
                            .mt(px(-46.))
                            .flex()
                            .flex_col()
                            .child(
                                div()
                                    .mb(px(22.))
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(px(36.))
                                            .line_height(px(40.))
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .text_color(palette.ink)
                                            .child(labels.prompt_title),
                                    )
                                    .child(
                                        div()
                                            .max_w(px(540.))
                                            .text_size(px(13.))
                                            .line_height(px(21.))
                                            .text_color(palette.muted)
                                            .child(labels.prompt_subtitle),
                                    ),
                            )
                            .child(composer),
                    )
                    .into_any_element()
            }
            Surface::Thread => {
                let blocks = self.state.transcript.blocks.clone();
                let process_expansion = self.process_expansion.clone();
                let owner = cx.entity();
                let zh = self.state.settings.language.as_ref() == "zh-CN";
                let workspace_additions = self.state.workspace.additions;
                let workspace_deletions = self.state.workspace.deletions;
                let environment = if self.environment_open {
                    Some(environment_panel(
                        &self.state,
                        palette,
                        labels,
                        self.environment_expanded.as_deref(),
                        cx,
                    ))
                } else {
                    None
                };
                let composer = self.composer_view(palette, labels, false, cx);
                div()
                    .id("active-thread")
                    .role(Role::Region)
                    .aria_label("Conversation")
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .bg(palette.paper)
                    .flex()
                    .flex_col()
                    .child(
                        div()
                            .id("transcript")
                            .role(Role::Log)
                            .aria_label("Conversation transcript")
                            .flex_1()
                            .min_h_0()
                            .overflow_hidden()
                            .when(self.environment_open, |viewport| viewport.pr(px(312.)))
                            .child(
                                list(self.transcript_list.clone(), move |index, _, _| {
                                    timeline_entry(
                                        index,
                                        &blocks.borrow(),
                                        palette,
                                        zh,
                                        (workspace_additions, workspace_deletions),
                                        process_expansion.clone(),
                                        owner.clone(),
                                    )
                                })
                                .flex_1(),
                            ),
                    )
                    .child(
                        div()
                            .w_full()
                            .px(px(18.))
                            .pt(px(8.))
                            .pb(px(14.))
                            .when(self.environment_open, |dock| dock.pr(px(330.)))
                            .flex()
                            .justify_center()
                            .child(composer),
                    )
                    .when_some(environment, |thread, environment| thread.child(environment))
                    .into_any_element()
            }
            Surface::Search => {
                search_surface(&self.state, self.search_input.clone(), palette, labels, cx)
            }
            Surface::Projects => projects_surface(&self.state, palette, labels, cx),
            Surface::Files => workspace_files_surface(&self.state, cx),
            Surface::Changes => workspace_changes_surface(&self.state, cx),
            Surface::PullRequests => pull_requests_surface(&self.state, cx),
            Surface::Work => work_surface(&self.state, &self.runtime),
            Surface::Security => security_surface(&self.state, &self.runtime),
            Surface::Terminal => self.terminal_view(palette, cx),
            Surface::Usage => usage_surface(&self.state, cx),
        };
        let sidebar = sidebar(
            &self.state,
            palette,
            labels,
            &self.open_projects,
            self.show_all_sessions,
            cx,
        );
        let settings_modal = if self.settings_open {
            Some(self.settings_modal_view(palette, cx))
        } else {
            None
        };
        div()
            .id("azem-root")
            .relative()
            .role(Role::Application)
            .aria_label(labels.application)
            .track_focus(&self.focus)
            .size_full()
            .on_action(cx.listener(Self::submit_message))
            .bg(palette.canvas)
            .text_color(palette.ink)
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(37.))
                    .flex_shrink_0()
                    .border_b_1()
                    .border_color(palette.border)
                    .bg(palette.paper),
            )
            .child(
                div().flex_1().min_h_0().flex().child(sidebar).child(
                    div()
                        .id("workspace")
                        .role(Role::Main)
                        .aria_label(surface_title.clone())
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .bg(palette.paper)
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .h(px(50.))
                                .px(px(22.))
                                .border_b_1()
                                .border_color(palette.border)
                                .bg(palette.paper)
                                .flex()
                                .items_center()
                                .child(
                                    div()
                                        .text_color(palette.ink)
                                        .flex()
                                        .items_center()
                                        .gap_2()
                                        .when(
                                            surface == Surface::Thread && !empty_thread,
                                            |title| {
                                                title
                                                    .child(
                                                        div()
                                                            .font_family("SF Mono")
                                                            .text_size(px(9.))
                                                            .text_color(palette.faint)
                                                            .child("TASK"),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(13.))
                                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                                            .child(surface_title.clone()),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(11.))
                                                            .text_color(palette.muted)
                                                            .child(header_project),
                                                    )
                                                    .when(!header_branch.is_empty(), |title| {
                                                        title
                                                            .child(
                                                                div()
                                                                    .text_color(palette.faint)
                                                                    .child("·"),
                                                            )
                                                            .child(
                                                                div()
                                                                    .font_family("SF Mono")
                                                                    .text_size(px(10.))
                                                                    .text_color(palette.muted)
                                                                    .child(header_branch),
                                                            )
                                                            .child(icon(
                                                                "chevron-down",
                                                                11.,
                                                                palette.faint,
                                                            ))
                                                    })
                                            },
                                        )
                                        .when(surface != Surface::Thread, |title| {
                                            title.child(
                                                div()
                                                    .text_size(px(13.))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(surface_title),
                                            )
                                        }),
                                )
                                .child(div().flex_1())
                                .when(surface == Surface::Thread && !empty_thread, |header| {
                                    header
                                        .when(self.state.connection.connected, |header| {
                                            header.child(
                                                div()
                                                    .text_size(px(11.))
                                                    .font_weight(gpui::FontWeight::MEDIUM)
                                                    .text_color(palette.positive)
                                                    .child(
                                                        if self.state.settings.language.as_ref()
                                                            == "zh-CN"
                                                        {
                                                            "就绪"
                                                        } else {
                                                            "Ready"
                                                        },
                                                    ),
                                            )
                                        })
                                        .child(
                                            div()
                                                .id("environment-toggle")
                                                .role(Role::Button)
                                                .aria_label(
                                                    if self.state.settings.language.as_ref()
                                                        == "zh-CN"
                                                    {
                                                        "环境"
                                                    } else {
                                                        "Environment"
                                                    },
                                                )
                                                .aria_selected(self.environment_open)
                                                .tab_stop(true)
                                                .size(px(31.))
                                                .ml_2()
                                                .rounded(px(8.))
                                                .bg(if self.environment_open {
                                                    palette.paper_muted
                                                } else {
                                                    palette.paper
                                                })
                                                .text_color(palette.muted)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_click(cx.listener(|this, _, _, cx| {
                                                    this.environment_open = !this.environment_open;
                                                    cx.notify();
                                                }))
                                                .child(icon("panels", 15., palette.muted)),
                                        )
                                        .child(
                                            div()
                                                .id("header-terminal")
                                                .role(Role::Button)
                                                .aria_label(labels.terminal)
                                                .tab_stop(true)
                                                .size(px(31.))
                                                .ml_1()
                                                .rounded(px(8.))
                                                .border_1()
                                                .border_color(palette.border)
                                                .text_color(palette.muted)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_click(cx.listener(|this, _, _, cx| {
                                                    this.state.navigation.surface =
                                                        Surface::Terminal;
                                                    this.request_surface(Surface::Terminal);
                                                    cx.notify();
                                                }))
                                                .child(icon("terminal", 15., palette.muted)),
                                        )
                                })
                                .when(!self.state.connection.connected, |header| {
                                    header.child(
                                        div()
                                            .id("connection-status")
                                            .role(Role::Status)
                                            .text_size(px(11.))
                                            .text_color(palette.warning)
                                            .child(self.state.connection.message.to_string()),
                                    )
                                }),
                        )
                        .child(content),
                ),
            )
            .when_some(settings_modal, |root, modal| root.child(modal))
    }
}

fn open_main_window(cx: &mut App, options: RuntimeOptions) -> WindowHandle<AzemWindow> {
    let bounds = Bounds::centered(None, size(px(1360.), px(900.)), cx);
    cx.open_window(
        WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            window_min_size: Some(size(px(920.), px(640.))),
            titlebar: Some(gpui::TitlebarOptions {
                title: None,
                appears_transparent: true,
                ..Default::default()
            }),
            ..Default::default()
        },
        move |window, cx| cx.new(|cx| AzemWindow::new(window, cx, options)),
    )
    .expect("failed to open Azem window")
}

fn runtime_options() -> RuntimeOptions {
    let mut workspace = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
    let mut session_id = None;
    let mut config_file = None;
    let mut daemon_binary = None;
    let mut state_dir = None;
    let mut arguments = std::env::args_os().skip(1);
    while let Some(argument) = arguments.next() {
        match argument.to_string_lossy().as_ref() {
            "--workspace" => {
                if let Some(value) = arguments.next() {
                    workspace = value.into();
                }
            }
            "--session" => {
                session_id = arguments
                    .next()
                    .map(|value| value.to_string_lossy().into_owned())
            }
            "--config" => config_file = arguments.next().map(Into::into),
            "--daemon" => daemon_binary = arguments.next().map(Into::into),
            "--state-dir" => state_dir = arguments.next().map(Into::into),
            _ => {}
        }
    }
    RuntimeOptions {
        workspace,
        session_id,
        config_file,
        daemon_binary,
        state_dir,
    }
}

fn main() {
    let startup_started = Instant::now();
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let options = runtime_options();
    let window: Rc<RefCell<Option<WindowHandle<AzemWindow>>>> = Rc::new(RefCell::new(None));
    let reopen_window = window.clone();
    let reopen_options = options.clone();
    let application = application()
        .with_assets(Assets::discover())
        .with_quit_mode(gpui::QuitMode::Explicit);
    application.on_reopen(move |cx| {
        if let Some(handle) = *reopen_window.borrow()
            && handle
                .update(cx, |_, window, _| window.activate_window())
                .is_ok()
        {
            return;
        }
        *reopen_window.borrow_mut() = Some(open_main_window(cx, reopen_options.clone()));
        cx.activate(true);
    });
    application.run(move |cx| {
        cx.set_app_identity("dev.azem.gpui", "Azem GPUI");
        text_input::init(cx);
        cx.bind_keys([KeyBinding::new("enter", Submit, Some("TextInput"))]);
        let handle = open_main_window(cx, options);
        let alive = handle
            .update(cx, |_, window, _| window.activate_window())
            .is_ok();
        *window.borrow_mut() = Some(handle);
        tracing::info!(
            alive,
            startup_ms = startup_started.elapsed().as_millis(),
            "Azem GPUI window ready"
        );
        cx.activate(true);
    });
}
