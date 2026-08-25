mod localization;
mod runtime_connection;
mod state;
mod surfaces;
mod terminal_emulator;
mod text_input;
mod theme;
use std::{cell::RefCell, collections::HashMap, path::PathBuf, rc::Rc};

use azem_ipc::{ClientEvent, Method};
use gpui::{
    App, Bounds, ClickEvent, Context, Entity, FocusHandle, KeyBinding, ListAlignment, ListState,
    Role, Task, Window, WindowBounds, WindowHandle, WindowOptions, div, list, prelude::*, px, rgb,
    size,
};
use gpui_platform::application;
use localization::labels;
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
    terminal_input: Entity<TextInput>,
    terminal_emulators: HashMap<String, TerminalEmulator>,
    transcript_list: ListState,
    pending_requests: HashMap<String, PendingRequest>,
    _connection_task: Task<()>,
}

impl AzemWindow {
    fn new(window: &mut Window, cx: &mut Context<Self>, options: RuntimeOptions) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let composer = cx.new(|cx| TextInput::new(cx, "Describe what you want Azem to do…"));
        let terminal_input = cx.new(|cx| TextInput::new(cx, "Terminal input"));
        let runtime = RuntimeConnection::start(options);
        let transcript_list = ListState::new(0, ListAlignment::Bottom, px(800.));
        let messages = runtime.messages.clone();
        let connection_task = cx.spawn(async move |this, cx| {
            while let Ok(message) = messages.recv().await {
                if this
                    .update(cx, |this, cx| {
                        this.apply_runtime_message(message);
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
            terminal_input,
            terminal_emulators: HashMap::new(),
            pending_requests: HashMap::new(),
            _connection_task: connection_task,
            transcript_list,
        }
    }

    fn apply_runtime_message(&mut self, message: RuntimeMessage) {
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
                    self.pending_requests.remove(&id);
                    self.state.settings.error = format!("{id}: {error}").into();
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
        if self.state.navigation.surface == Surface::Terminal {
            self.write_terminal_current(cx);
        } else {
            self.send_current(cx);
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
        let connection_message = if self.state.connection.connected {
            labels.connected.to_string()
        } else {
            self.state.connection.message.to_string()
        };
        let workspace = if self.state.workspace.root.is_empty() {
            "Workspace".to_string()
        } else {
            self.state.workspace.root.to_string()
        };
        let surface = self.state.navigation.surface;
        let surface_title = match surface {
            Surface::Thread if !self.state.navigation.current_title.is_empty() => {
                self.state.navigation.current_title.to_string()
            }
            Surface::Thread => labels.new_conversation.to_string(),
            Surface::Projects => labels.projects.to_string(),
            Surface::Files => labels.files.to_string(),
            Surface::Changes => labels.changes.to_string(),
            Surface::PullRequests => labels.pull_requests.to_string(),
            Surface::Work => labels.run_controls.to_string(),
            Surface::Security => labels.security.to_string(),
            Surface::Terminal => labels.terminal.to_string(),
            Surface::Settings => labels.settings.to_string(),
            Surface::Usage => labels.usage.to_string(),
        };
        let content = match surface {
            Surface::Thread => {
                let blocks = self.state.transcript.blocks.clone();
                div()
                    .id("transcript")
                    .role(Role::Log)
                    .aria_label("Conversation transcript")
                    .flex_1()
                    .overflow_hidden()
                    .child(
                        list(self.transcript_list.clone(), move |index, _, _| {
                            blocks
                                .borrow()
                                .get(index)
                                .cloned()
                                .map(|block| timeline_block(index, block))
                                .unwrap_or_else(|| div().into_any_element())
                        })
                        .flex_1(),
                    )
                    .into_any_element()
            }
            Surface::Projects => projects_surface(&self.state),
            Surface::Files => workspace_files_surface(&self.state, cx),
            Surface::Changes => workspace_changes_surface(&self.state, cx),
            Surface::PullRequests => pull_requests_surface(&self.state, cx),
            Surface::Work => work_surface(&self.state, &self.runtime),
            Surface::Security => security_surface(&self.state, &self.runtime),
            Surface::Terminal => {
                let visible = self
                    .terminal_emulators
                    .get(self.state.terminals.active_id.as_ref())
                    .map(TerminalEmulator::visible_text)
                    .unwrap_or_default();
                div()
                    .id("terminal-surface")
                    .role(Role::Region)
                    .aria_label("Embedded terminal")
                    .flex_1()
                    .p_4()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .child(
                        div()
                            .flex()
                            .gap_2()
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
                                    let selected =
                                        self.state.terminals.active_id.as_ref() == id.as_str();
                                    div()
                                        .id(("terminal-tab", index))
                                        .role(Role::Tab)
                                        .aria_label(label.clone())
                                        .aria_selected(selected)
                                        .tab_stop(true)
                                        .px_3()
                                        .py_2()
                                        .rounded_md()
                                        .bg(if selected {
                                            rgb(0xe5e4de)
                                        } else {
                                            rgb(0xf5f5f3)
                                        })
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.state.terminals.active_id = id.clone().into();
                                            cx.notify();
                                        }))
                                        .child(label)
                                },
                            ))
                            .child(
                                div()
                                    .id("create-terminal")
                                    .role(Role::Button)
                                    .aria_label("Create terminal")
                                    .tab_stop(true)
                                    .px_3()
                                    .py_2()
                                    .rounded_md()
                                    .on_click(cx.listener(Self::create_terminal))
                                    .child("New"),
                            )
                            .child(
                                div()
                                    .id("close-terminal")
                                    .role(Role::Button)
                                    .aria_label("Close terminal")
                                    .tab_stop(true)
                                    .px_3()
                                    .py_2()
                                    .rounded_md()
                                    .on_click(cx.listener(Self::close_terminal))
                                    .child("Close"),
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
                            .flex()
                            .gap_2()
                            .items_center()
                            .child(self.terminal_input.clone())
                            .child(
                                div()
                                    .id("terminal-send")
                                    .role(Role::Button)
                                    .aria_label("Send terminal input")
                                    .tab_stop(true)
                                    .px_3()
                                    .py_2()
                                    .rounded_md()
                                    .bg(rgb(0x1f2124))
                                    .text_color(rgb(0xffffff))
                                    .on_click(cx.listener(Self::write_terminal))
                                    .child("Send"),
                            ),
                    )
                    .into_any_element()
            }
            Surface::Settings => settings_surface(&self.state, cx),
            Surface::Usage => usage_surface(&self.state, cx),
        };

        div()
            .id("azem-root")
            .role(Role::Application)
            .aria_label(labels.application)
            .track_focus(&self.focus)
            .size_full()
            .on_action(cx.listener(Self::submit_message))
            .flex()
            .bg(palette.paper)
            .text_color(palette.ink)
            .child(
                div()
                    .id("sidebar")
                    .role(Role::Navigation)
                    .aria_label(labels.projects)
                    .w(px(280.))
                    .h_full()
                    .border_r_1()
                    .border_color(palette.border)
                    .p_4()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .text_xl()
                                    .font_weight(gpui::FontWeight::BOLD)
                                    .child("Azem"),
                            )
                            .child(
                                div()
                                    .id("new-session")
                                    .role(Role::Button)
                                    .aria_label(labels.new_conversation)
                                    .tab_stop(true)
                                    .px_3()
                                    .py_2()
                                    .rounded_md()
                                    .bg(palette.ink)
                                    .text_color(palette.paper)
                                    .on_click(cx.listener(Self::new_session))
                                    .child(labels.new_conversation),
                            ),
                    )
                    .child(div().text_sm().text_color(palette.muted).child(workspace))
                    .child(
                        div()
                            .id("primary-navigation")
                            .role(Role::Navigation)
                            .aria_label("Primary")
                            .flex()
                            .flex_col()
                            .gap_1()
                            .children(
                                [
                                    (labels.conversation, Surface::Thread),
                                    (labels.projects, Surface::Projects),
                                    (labels.files, Surface::Files),
                                    (labels.changes, Surface::Changes),
                                    (labels.pull_requests, Surface::PullRequests),
                                    (labels.run_controls, Surface::Work),
                                    (labels.security, Surface::Security),
                                    (labels.terminal, Surface::Terminal),
                                    (labels.settings, Surface::Settings),
                                    (labels.usage, Surface::Usage),
                                ]
                                .into_iter()
                                .enumerate()
                                .map(
                                    |(index, (label, destination))| {
                                        let selected = surface == destination;
                                        div()
                                            .id(("surface", index))
                                            .role(Role::Button)
                                            .aria_label(label)
                                            .aria_selected(selected)
                                            .tab_stop(true)
                                            .px_3()
                                            .py_2()
                                            .rounded_md()
                                            .bg(if selected {
                                                palette.selected
                                            } else {
                                                palette.paper
                                            })
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                this.state.navigation.surface = destination;
                                                this.request_surface(destination);
                                                cx.notify();
                                            }))
                                            .child(label)
                                    },
                                ),
                            ),
                    )
                    .child(
                        div()
                            .id("session-list")
                            .role(Role::List)
                            .aria_label("Conversations")
                            .flex_1()
                            .overflow_y_scroll()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .children(self.state.navigation.sessions.iter().enumerate().map(
                                |(index, session)| {
                                    let session_id = session.id.to_string();
                                    let unread = session.unread
                                        || self
                                            .state
                                            .navigation
                                            .unread
                                            .get(&session.id)
                                            .copied()
                                            .unwrap_or(false);
                                    let selected =
                                        self.state.navigation.current_session_id.as_ref()
                                            == session.id.as_ref();
                                    let title = session.title.to_string();
                                    let accessible_title = if session.running {
                                        format!("{title}, running")
                                    } else if unread {
                                        format!("{title}, unread")
                                    } else {
                                        title.clone()
                                    };
                                    div()
                                        .id(("session", index))
                                        .role(Role::Button)
                                        .aria_label(accessible_title)
                                        .aria_selected(selected)
                                        .focusable()
                                        .tab_stop(true)
                                        .px_3()
                                        .py_2()
                                        .rounded_md()
                                        .bg(if selected {
                                            palette.selected
                                        } else if session.running || unread {
                                            palette.unread
                                        } else {
                                            palette.paper
                                        })
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            let request_id = this.runtime.request(
                                                Method::ResumeSession,
                                                json!({"sessionId": session_id}),
                                            );
                                            this.pending_requests
                                                .insert(request_id, PendingRequest::ResumeSession);
                                            this.state.navigation.surface = Surface::Thread;
                                            cx.notify();
                                        }))
                                        .child(title)
                                },
                            )),
                    ),
            )
            .child(
                div()
                    .id("workspace")
                    .role(Role::Main)
                    .aria_label(surface_title.clone())
                    .flex_1()
                    .h_full()
                    .flex()
                    .flex_col()
                    .child(
                        div()
                            .h(px(52.))
                            .border_b_1()
                            .border_color(palette.border)
                            .px_5()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(surface_title),
                            )
                            .child(
                                div()
                                    .id("connection-status")
                                    .role(Role::Status)
                                    .aria_label(connection_message.clone())
                                    .text_sm()
                                    .text_color(if self.state.connection.connected {
                                        palette.positive
                                    } else {
                                        palette.warning
                                    })
                                    .child(connection_message),
                            ),
                    )
                    .child(content)
                    .when(surface == Surface::Thread, |workspace| {
                        workspace.child(
                            div()
                                .id("composer")
                                .role(Role::Group)
                                .aria_label(labels.composer)
                                .m_4()
                                .p_3()
                                .border_1()
                                .border_color(palette.border)
                                .bg(palette.raised)
                                .rounded_xl()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(
                                    div()
                                        .id("attach-file")
                                        .role(Role::Button)
                                        .aria_label(labels.attach)
                                        .tab_stop(true)
                                        .px_2()
                                        .py_2()
                                        .rounded_md()
                                        .on_click(cx.listener(Self::attach_file))
                                        .child(labels.attach),
                                )
                                .child(self.composer.clone())
                                .when(self.state.runtime.running, |composer| {
                                    composer.child(
                                        div()
                                            .id("guide-message")
                                            .role(Role::Button)
                                            .aria_label(labels.guide)
                                            .tab_stop(true)
                                            .px_3()
                                            .py_2()
                                            .rounded_md()
                                            .bg(palette.selected)
                                            .on_click(cx.listener(Self::guide_message))
                                            .child(labels.guide),
                                    )
                                })
                                .child(
                                    div()
                                        .id("send-message")
                                        .role(Role::Button)
                                        .aria_label(if self.state.runtime.running {
                                            labels.queue
                                        } else {
                                            labels.send
                                        })
                                        .tab_stop(true)
                                        .px_3()
                                        .py_2()
                                        .rounded_md()
                                        .bg(palette.ink)
                                        .text_color(palette.paper)
                                        .on_click(cx.listener(Self::send_message))
                                        .child(if self.state.runtime.running {
                                            labels.queue
                                        } else {
                                            labels.send
                                        }),
                                )
                                .when(self.state.runtime.running, |composer| {
                                    composer.child(
                                        div()
                                            .id("cancel-active")
                                            .role(Role::Button)
                                            .aria_label(labels.stop)
                                            .tab_stop(true)
                                            .px_3()
                                            .py_2()
                                            .rounded_md()
                                            .border_1()
                                            .border_color(palette.border)
                                            .on_click(cx.listener(Self::cancel_active))
                                            .child(labels.stop),
                                    )
                                }),
                        )
                    }),
            )
    }
}

fn open_main_window(cx: &mut App, options: RuntimeOptions) -> WindowHandle<AzemWindow> {
    let bounds = Bounds::centered(None, size(px(1180.), px(760.)), cx);
    cx.open_window(
        WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            titlebar: Some(gpui::TitlebarOptions {
                title: Some("Azem".into()),
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
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let options = runtime_options();
    let window: Rc<RefCell<Option<WindowHandle<AzemWindow>>>> = Rc::new(RefCell::new(None));
    let reopen_window = window.clone();
    let reopen_options = options.clone();
    let application = application().with_quit_mode(gpui::QuitMode::Explicit);
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
        text_input::init(cx);
        cx.bind_keys([KeyBinding::new("enter", Submit, Some("TextInput"))]);
        let handle = open_main_window(cx, options);
        let alive = handle
            .update(cx, |_, window, _| window.activate_window())
            .is_ok();
        *window.borrow_mut() = Some(handle);
        tracing::info!(alive, "Azem GPUI window ready");
        cx.activate(true);
    });
}
