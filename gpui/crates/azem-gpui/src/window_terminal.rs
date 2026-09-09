use super::*;

impl AzemWindow {
    pub(super) fn request_create_terminal(&mut self) {
        if self
            .pending_requests
            .values()
            .any(|pending| matches!(pending, PendingRequest::CreateTerminal))
        {
            return;
        }
        let id = self
            .runtime
            .request(Method::CreateTerminal, json!({"cols": 120, "rows": 32}));
        self.pending_requests
            .insert(id, PendingRequest::CreateTerminal);
    }

    pub(super) fn create_terminal(
        &mut self,
        _: &ClickEvent,
        _: &mut Window,
        _: &mut Context<Self>,
    ) {
        self.request_create_terminal();
    }

    pub(super) fn close_terminal(
        &mut self,
        _: &ClickEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        cx.stop_propagation();
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
        self.terminal_scroll.scroll_to_bottom();
        cx.notify();
    }

    pub(super) fn clear_terminal(
        &mut self,
        _: &ClickEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let id = self.state.terminals.active_id.to_string();
        if let Some(terminal) = self.terminal_emulators.get_mut(&id) {
            *terminal = TerminalEmulator::default();
            self.terminal_scroll.scroll_to_bottom();
            cx.notify();
        }
    }

    pub(super) fn write_terminal_data(&self, data: &str) {
        let terminal_id = self.state.terminals.active_id.to_string();
        if terminal_id.is_empty() || data.is_empty() {
            return;
        }
        self.runtime.request(
            Method::WriteTerminal,
            json!({"id": terminal_id, "data": data}),
        );
    }

    pub(super) fn terminal_key(
        &mut self,
        event: &gpui::KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self.terminal_input.focus_handle(cx).is_focused(window) {
            return;
        }
        let data = if event.keystroke.modifiers.platform && event.keystroke.key == "v" {
            cx.read_from_clipboard().and_then(|item| item.text())
        } else {
            terminal_key_data(&event.keystroke)
        };
        let Some(data) = data else {
            return;
        };
        self.write_terminal_data(&data);
        window.prevent_default();
        cx.stop_propagation();
    }

    pub(super) fn request_surface(&mut self, surface: Surface) {
        if surface == Surface::Security {
            self.runtime.request(
    				Method::Execute,
    				json!({"kind":"list_security_scans", "sessionId":self.state.navigation.current_session_id, "limit":100}),
    			);
            return;
        }
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
            Surface::Terminal => (Method::ListTerminals, json!({}), PendingRequest::Terminals),
            _ => return,
        };
        let id = self.runtime.request(method, payload);
        self.pending_requests.insert(id, pending);
    }

    pub(super) fn request_security_scan_projection(&self) {
        let Some(scan_id) = security_scan_target(&self.state) else {
            return;
        };
        for kind in ["get_security_scan", "list_security_findings"] {
            self.runtime.request(
                Method::Execute,
                json!({
                    "kind": kind,
                    "target": scan_id,
                    "sessionId": self.state.navigation.current_session_id,
                }),
            );
        }
    }

    pub(super) fn terminal_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        let lines = self
            .terminal_emulators
            .get(self.state.terminals.active_id.as_ref())
            .map(TerminalEmulator::visible_lines)
            .unwrap_or_default();
        let cursor_visible = self.terminal_input.read(cx).cursor_visible();
        let mut terminal_font = gpui::font("Hack Nerd Font Mono");
        terminal_font.fallbacks = Some(gpui::FontFallbacks::from_fonts(vec![
            "MesloLGS NF".to_string(),
            "SF Mono".to_string(),
            "Apple Symbols".to_string(),
        ]));
        div()
            .id("terminal-surface")
            .role(Role::Region)
            .aria_label(locale.text("terminal.embedded"))
            .size_full()
            .bg(palette.paper)
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(36.))
                    .px_2()
                    .bg(palette.paper_muted)
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
                                .unwrap_or(locale.text("terminal.title"))
                                .to_string();
                            let selected = self.state.terminals.active_id.as_ref() == id.as_str();
                            div()
                                .id(("terminal-tab", index))
                                .role(Role::Tab)
                                .aria_label(label.clone())
                                .aria_selected(selected)
                                .tab_stop(true)
                                .h(px(26.))
                                .px_2()
                                .rounded(px(7.))
                                .border_1()
                                .border_color(palette.border)
                                .bg(if selected {
                                    palette.hover
                                } else {
                                    palette.paper
                                })
                                .text_color(if selected { palette.ink } else { palette.muted })
                                .text_xs()
                                .flex()
                                .items_center()
                                .gap_2()
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.state.terminals.active_id = id.clone().into();
                                    this.terminal_scroll.scroll_to_bottom();
                                    cx.notify();
                                }))
                                .child(format!("›_  {label}"))
                                .when(selected, |tab| {
                                    tab.child(
                                        div()
                                            .id(("close-terminal-tab", index))
                                            .role(Role::Button)
                                            .aria_label(locale.text("terminal.close"))
                                            .tab_stop(true)
                                            .text_color(palette.faint)
                                            .cursor_pointer()
                                            .on_click(cx.listener(Self::close_terminal))
                                            .child("×"),
                                    )
                                })
                        },
                    ))
                    .child(div().flex_1())
                    .child(
                        div()
                            .id("create-terminal")
                            .role(Role::Button)
                            .aria_label(locale.text("terminal.create"))
                            .tab_stop(true)
                            .size(px(26.))
                            .rounded(px(7.))
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
                            .text_xs()
                            .text_color(palette.faint)
                            .mr_2()
                            .child(self.state.workspace.root.to_string()),
                    )
                    .child(
                        div()
                            .id("clear-terminal")
                            .role(Role::Button)
                            .aria_label(locale.text("terminal.clear"))
                            .tab_stop(true)
                            .size(px(26.))
                            .rounded(px(7.))
                            .text_color(palette.muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(Self::clear_terminal))
                            .child("⌫"),
                    )
                    .child(
                        div()
                            .id("collapse-terminal")
                            .role(Role::Button)
                            .aria_label(locale.text("terminal.collapse"))
                            .tab_stop(true)
                            .size(px(26.))
                            .rounded(px(7.))
                            .text_color(palette.muted)
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(Self::toggle_terminal_click))
                            .child("×"),
                    ),
            )
            .child(
                div()
                    .id("terminal-output")
                    .role(Role::Log)
                    .aria_label(locale.text("terminal.output"))
                    .relative()
                    .flex_1()
                    .overflow_y_scroll()
                    .track_scroll(&self.terminal_scroll)
                    .bg(palette.paper)
                    .text_color(palette.ink)
                    .font(terminal_font)
                    .text_size(px(12.))
                    .line_height(px(18.))
                    .px_2()
                    .py_2()
                    .whitespace_nowrap()
                    .cursor(gpui::CursorStyle::IBeam)
                    .capture_key_down(cx.listener(Self::terminal_key))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, _, window, cx| {
                            this.terminal_input.focus_handle(cx).focus(window, cx);
                            cx.notify();
                        }),
                    )
                    .children(lines.into_iter().enumerate().map(|(index, line)| {
                        let show_cursor = cursor_visible && line.has_cursor;
                        div()
                            .id(("terminal-line", index))
                            .h(px(18.))
                            .flex()
                            .items_center()
                            .flex_shrink_0()
                            .child(line.before_cursor)
                            .when(show_cursor, |row| {
                                row.child(
                                    div().w(px(1.)).h(px(14.)).flex_shrink_0().bg(palette.ink),
                                )
                            })
                            .child(line.after_cursor)
                    }))
                    .child(
                        div()
                            .absolute()
                            .top_0()
                            .left_0()
                            .size(px(1.))
                            .opacity(0.)
                            .child(self.terminal_input.clone()),
                    ),
            )
            .into_any_element()
    }
}
