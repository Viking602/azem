use super::*;

impl AzemWindow {
    pub(super) fn approval_request_pending(&self) -> bool {
        self.pending_requests
            .values()
            .any(|request| matches!(request, PendingRequest::ApprovalMode))
    }

    pub(super) fn toggle_approval_picker(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.state.connection.connected || runtime_busy(&self.state) {
            return;
        }
        self.approval_picker.open = !self.approval_picker.open;
        self.approval_picker.focus.focus(window, cx);
        if self.approval_picker.open {
            self.branch_picker.open = false;
            self.model_picker_open = false;
            self.context_popover_open = false;
            self.approval_picker.index = APPROVAL_MODES
                .iter()
                .position(|(mode, _, _)| *mode == self.state.settings.approval_mode.as_ref())
                .unwrap_or(0);
        }
        cx.notify();
    }

    pub(super) fn change_approval_mode(&mut self, target: &str, cx: &mut Context<Self>) {
        if let Some(action) =
            approval_mode_action(&self.state, target, self.approval_request_pending())
        {
            self.approval_picker.error.clear();
            self.state.settings.error = "".into();
            let id = self.runtime.request(Method::Execute, action);
            self.pending_requests
                .insert(id, PendingRequest::ApprovalMode);
        } else if target == self.state.settings.approval_mode.as_ref() {
            self.approval_picker.open = false;
        }
        cx.notify();
    }

    pub(super) fn approval_picker_key(
        &mut self,
        event: &gpui::KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        match event.keystroke.key.as_str() {
            "up" | "down" => {
                if !self.approval_picker.open {
                    self.toggle_approval_picker(window, cx);
                } else {
                    let count = APPROVAL_MODES.len();
                    let step = if event.keystroke.key == "down" {
                        1
                    } else {
                        count - 1
                    };
                    self.approval_picker.index = (self.approval_picker.index + step) % count;
                }
            }
            "enter" | "space" => {
                if self.approval_picker.open {
                    self.change_approval_mode(APPROVAL_MODES[self.approval_picker.index].0, cx);
                } else {
                    self.toggle_approval_picker(window, cx);
                }
            }
            "escape" => self.approval_picker.open = false,
            "tab" => {
                self.approval_picker.open = false;
                cx.notify();
                return;
            }
            _ => return,
        }
        window.prevent_default();
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn toggle_plan(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        if !self.state.runtime.running {
            self.state.runtime.plan_mode = !self.state.runtime.plan_mode;
            cx.notify();
        }
    }

    pub(super) fn model_picker_selection(&self) -> (String, String, String) {
        let Some(target) = &self.route_picker_target else {
            return (
                self.state.settings.provider.to_string(),
                self.state.settings.model.to_string(),
                self.state.settings.reasoning.to_string(),
            );
        };
        let route = self.state.catalogs.routes.iter().find(|route| {
            route.get("scope").and_then(serde_json::Value::as_str) == Some(target.scope.as_str())
                && route
                    .get("role")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    == target.role
        });
        let value = route.and_then(|route| route.get("route"));
        let selected = |key: &str, fallback: &str| {
            value
                .and_then(|route| route.get(key))
                .and_then(serde_json::Value::as_str)
                .filter(|value| !value.is_empty())
                .unwrap_or(fallback)
                .to_string()
        };
        (
            selected("provider", self.state.settings.provider.as_ref()),
            selected("model", self.state.settings.model.as_ref()),
            selected("reasoning", self.state.settings.reasoning.as_ref()),
        )
    }

    pub(super) fn apply_model_picker_selection(
        &mut self,
        provider: String,
        model: String,
        reasoning: String,
        close: bool,
        cx: &mut Context<Self>,
    ) {
        if !self.model_controls_enabled() {
            tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "blocked", %reasoning);
            return;
        }
        self.reasoning_drag = None;
        self.model_picker_error.clear();
        let target = self.route_picker_target.clone();
        let session_id = self.state.navigation.current_session_id.to_string();
        let id = if let Some(target) = &target {
            self.runtime.request(
                Method::Execute,
                json!({
                    "kind": "set_model_route",
                    "sessionId": self.state.navigation.current_session_id,
                    "route": {
                        "scope": target.scope,
                        "role": target.role,
                        "label": target.label,
                        "route": {
                            "provider": provider,
                            "model": model,
                            "reasoning": reasoning,
                        }
                    }
                }),
            )
        } else {
            self.runtime.request(
                Method::Execute,
                json!({
                    "kind": "set_session_preferences",
                    "sessionId": self.state.navigation.current_session_id,
                    "route": {
                        "scope": "session",
                        "role": "",
                        "label": "",
                        "route": {
                            "provider": provider,
                            "model": model,
                            "reasoning": reasoning,
                        }
                    }
                }),
            )
        };
        tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "submit", request = %id, %reasoning);
        self.pending_requests.insert(
            id,
            PendingRequest::ModelSelection {
                target,
                session_id,
                provider,
                model,
                reasoning,
            },
        );
        if close {
            self.model_picker_open = false;
            self.route_picker_target = None;
        }
        cx.notify();
    }

    pub(super) fn model_controls_enabled(&self) -> bool {
        self.state.connection.connected
            && !runtime_busy(&self.state)
            && !self.pending_requests.values().any(|request| {
                matches!(
                    request,
                    PendingRequest::ModelSelection { .. } | PendingRequest::ChatGPTFastMode(_)
                )
            })
    }

    pub(super) fn selected_model_modes(&self) -> ModelModes {
        let (provider, model, reasoning) = self.model_picker_selection();
        model_modes(
            &self.state.catalogs.providers,
            &provider,
            &model,
            &reasoning,
            self.state.settings.chatgpt_fast_mode,
        )
    }

    pub(super) fn set_reasoning(&mut self, next: String, cx: &mut Context<Self>) {
        let (provider, model, current) = self.model_picker_selection();
        if !self.selected_model_reasoning_levels().contains(&next) {
            return;
        }
        if provider == "cursor" {
            if let Some((target, tier)) =
                cursor_selection(&self.state.catalogs.providers, &model, Some(&next), None)
                && (target != model || tier != current)
            {
                self.apply_model_picker_selection(provider, target, tier, false, cx);
            }
        } else if current != next {
            self.apply_model_picker_selection(provider, model, next, false, cx);
        }
    }

    pub(super) fn toggle_fast_mode(&mut self, cx: &mut Context<Self>) {
        let modes = self.selected_model_modes();
        if !self.model_controls_enabled() || !modes.fast_available {
            return;
        }
        let (provider, model, _) = self.model_picker_selection();
        if provider == "cursor" {
            if let Some((target, tier)) = cursor_selection(
                &self.state.catalogs.providers,
                &model,
                None,
                Some(!modes.fast),
            ) {
                self.apply_model_picker_selection(provider, target, tier, false, cx);
            }
        } else if provider == "chatgpt" {
            self.model_picker_error.clear();
            let enabled = !modes.fast;
            let id = self.runtime.request(
                Method::Execute,
                json!({"kind":"set_chatgpt_fast_mode", "target":enabled.to_string()}),
            );
            self.pending_requests
                .insert(id, PendingRequest::ChatGPTFastMode(enabled));
            cx.notify();
        }
    }

    pub(super) fn update_reasoning_from_pointer(
        &mut self,
        position_x: Pixels,
        cx: &mut Context<Self>,
    ) {
        let (Some(bounds), Some(drag)) = (self.reasoning_slider_bounds, &mut self.reasoning_drag)
        else {
            return;
        };
        drag.progress = reasoning_progress_from_position(
            f32::from(position_x),
            f32::from(bounds.origin.x),
            f32::from(bounds.size.width),
        );
        tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "pointer", x = f32::from(position_x), progress = drag.progress);
        cx.notify();
    }

    pub(super) fn reasoning_drag_is_current(&self, drag: &ReasoningDrag) -> bool {
        self.model_picker_open
            && self.model_controls_enabled()
            && drag.session_id == self.state.navigation.current_session_id.as_ref()
            && drag.target == self.route_picker_target
            && drag.selection == self.model_picker_selection()
    }

    pub(super) fn reasoning_mouse_down(
        &mut self,
        event: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self.model_controls_enabled() || self.selected_model_reasoning_levels().len() < 2 {
            return;
        }
        tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "down", x = f32::from(event.position.x));
        self.reasoning_drag = Some(ReasoningDrag {
            progress: 0.,
            selection: self.model_picker_selection(),
            session_id: self.state.navigation.current_session_id.to_string(),
            target: self.route_picker_target.clone(),
        });
        self.update_reasoning_from_pointer(event.position.x, cx);
        cx.stop_propagation();
    }

    pub(super) fn reasoning_mouse_move(
        &mut self,
        event: &MouseMoveEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.reasoning_drag.is_none() {
            return;
        }
        if event.dragging() {
            tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "move", x = f32::from(event.position.x));
            self.update_reasoning_from_pointer(event.position.x, cx);
        } else {
            self.reasoning_drag = None;
            cx.notify();
        }
        cx.stop_propagation();
    }

    pub(super) fn reasoning_mouse_up(
        &mut self,
        event: &MouseUpEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if event.button != MouseButton::Left {
            return;
        }
        let Some(drag) = self.reasoning_drag.take() else {
            return;
        };
        tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "up", x = f32::from(event.position.x));
        cx.stop_propagation();
        if self.reasoning_drag_is_current(&drag)
            && let Some(bounds) = self.reasoning_slider_bounds
        {
            let levels = self.selected_model_reasoning_levels();
            let index = reasoning_index_from_position(
                f32::from(event.position.x),
                f32::from(bounds.origin.x),
                f32::from(bounds.size.width),
                levels.len(),
            );
            if levels.len() > 1
                && let Some(level) = levels.get(index)
            {
                self.set_reasoning(level.clone(), cx);
            }
        }
        cx.notify();
    }

    pub(super) fn selected_model_reasoning_levels(&self) -> Vec<String> {
        self.selected_model_modes().levels
    }

    pub(super) fn selected_model_context_window(&self) -> i64 {
        self.state
            .catalogs
            .providers
            .iter()
            .find(|provider| {
                provider.get("id").and_then(serde_json::Value::as_str)
                    == Some(self.state.settings.provider.as_ref())
            })
            .and_then(|provider| provider.get("models"))
            .and_then(serde_json::Value::as_array)
            .and_then(|models| {
                models.iter().find(|model| {
                    model.get("id").and_then(serde_json::Value::as_str)
                        == Some(self.state.settings.model.as_ref())
                })
            })
            .and_then(|model| model.get("contextWindow"))
            .and_then(serde_json::Value::as_i64)
            .unwrap_or_default()
    }

    pub(super) fn toggle_context_popover(
        &mut self,
        _: &ClickEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.branch_picker.open = false;
        self.context_popover_open = !self.context_popover_open;
        if self.context_popover_open {
            self.model_picker_open = false;
            self.route_picker_target = None;
        }
        cx.notify();
    }

    pub(super) fn branch_request_pending(&self) -> bool {
        self.pending_requests.values().any(|request| {
            matches!(
                request,
                PendingRequest::GitBranches { .. } | PendingRequest::EnvironmentGit
            )
        })
    }

    pub(super) fn request_environment_metrics(&mut self, include_pull_requests: bool) {
        if !self.state.connection.connected {
            return;
        }
        if !self.branch_request_pending() {
            let id = self.runtime.request(
                Method::Execute,
                json!({
                    "kind":"list_git_branches",
                    "sessionId":self.state.navigation.current_session_id,
                }),
            );
            self.pending_requests
                .insert(id, PendingRequest::EnvironmentGit);
        }
        if include_pull_requests
            && !self
                .pending_requests
                .values()
                .any(|request| matches!(request, PendingRequest::PullRequests))
        {
            let id = self
                .runtime
                .request(Method::PullRequestDashboard, json!({}));
            self.pending_requests
                .insert(id, PendingRequest::PullRequests);
        }
    }

    pub(super) fn toggle_environment_panel(&mut self, cx: &mut Context<Self>) {
        self.environment_open = !self.environment_open;
        if self.environment_open {
            self.request_environment_metrics(true);
        }
        cx.notify();
    }

    pub(super) fn resize_side_panel(
        &mut self,
        desired_width: f32,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let workspace_width = workspace_width(f32::from(window.bounds().size.width));
        let Some(maximum) = side_panel_max_width(workspace_width) else {
            self.hide_side_panel();
            cx.notify();
            return;
        };
        self.side_panel_width = desired_width.clamp(SIDE_PANEL_MIN_WIDTH, maximum);
        self.side_panel_visible_width = self.side_panel_width;
        self.side_panel_animation_started = None;
        cx.notify();
    }

    pub(super) fn hide_side_panel(&mut self) {
        self.side_panel_open = false;
        self.side_panel_closing = false;
        self.side_panel_visible_width = 0.;
        self.side_panel_animation_started = None;
        self.side_panel_resize_drag = None;
        self.side_panel_agents_open = false;
        self.side_panel_add_menu_open = false;
        self.state.runtime.selected_agent_id = "".into();
        self.state.runtime.agent_blocks.clear();
    }

    pub(super) fn reconcile_side_panel_layout(&mut self, window: &mut Window) {
        if !self.side_panel_open && !self.side_panel_closing {
            return;
        }
        let workspace_width = workspace_width(f32::from(window.bounds().size.width));
        let Some(maximum) = side_panel_max_width(workspace_width) else {
            self.hide_side_panel();
            return;
        };
        self.side_panel_width = self.side_panel_width.min(maximum);
        self.side_panel_visible_width = self.side_panel_visible_width.min(maximum);
        self.side_panel_animation_from = self.side_panel_animation_from.min(maximum);
    }

    pub(super) fn animate_side_panel(
        &mut self,
        closing: bool,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.side_panel_animation_from = self.side_panel_visible_width;
        self.side_panel_animation_started = Some(Instant::now());
        self.side_panel_closing = closing;
        cx.notify();
    }

    pub(super) fn advance_side_panel_animation(&mut self, window: &mut Window) {
        let Some(started) = self.side_panel_animation_started else {
            return;
        };
        let elapsed = started.elapsed();
        let target = if self.side_panel_closing {
            0.
        } else {
            self.side_panel_width
        };
        self.side_panel_visible_width =
            eased_side_panel_width(self.side_panel_animation_from, target, elapsed);
        if elapsed >= SIDE_PANEL_TRANSITION {
            self.side_panel_animation_started = None;
            self.side_panel_visible_width = target;
            if self.side_panel_closing {
                self.hide_side_panel();
            }
        } else {
            window.request_animation_frame();
        }
    }

    pub(super) fn toggle_side_panel(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let reduced_motion = self
            .state
            .settings
            .appearance
            .get("reducedMotion")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false);
        if self.side_panel_open && !self.side_panel_closing {
            self.side_panel_resize_drag = None;
            if reduced_motion {
                self.hide_side_panel();
                cx.notify();
                return;
            }
            self.animate_side_panel(true, window, cx);
        } else if self.side_panel_closing {
            self.animate_side_panel(false, window, cx);
        } else {
            let workspace_width = workspace_width(f32::from(window.bounds().size.width));
            let Some(width) = side_panel_width_for_workspace(workspace_width) else {
                self.hide_side_panel();
                cx.notify();
                return;
            };
            self.side_panel_width = width;
            self.side_panel_open = true;
            self.side_panel_closing = false;
            self.side_panel_visible_width = 0.;
            if reduced_motion {
                self.resize_side_panel(self.side_panel_width, window, cx);
            } else {
                self.animate_side_panel(false, window, cx);
            }
        }
        cx.notify();
    }

    pub(super) fn inspect_agent(
        &mut self,
        target: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self.side_panel_open || self.side_panel_closing {
            self.toggle_side_panel(window, cx);
        }
        if !self.side_panel_open || self.side_panel_closing {
            return;
        }
        self.side_panel_agents_open = true;
        if self.state.runtime.selected_agent_id.as_ref() != target {
            self.state.runtime.agent_blocks.clear();
        }
        self.state.runtime.selected_agent_id = target.clone().into();
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": "inspect_agent",
                "target": target,
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
        cx.notify();
    }

    pub(super) fn open_agent_roster(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.side_panel_open || self.side_panel_closing {
            self.toggle_side_panel(window, cx);
        }
        if !self.side_panel_open || self.side_panel_closing {
            return;
        }
        self.side_panel_agents_open = true;
        self.state.runtime.selected_agent_id = "".into();
        self.state.runtime.agent_blocks.clear();
        cx.notify();
    }

    pub(super) fn side_panel_resize_mouse_down(
        &mut self,
        event: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self.side_panel_open || self.side_panel_closing {
            return;
        }
        self.side_panel_resize_drag = Some(SidePanelResizeDrag {
            start_x: event.position.x,
            start_width: self.side_panel_width,
        });
        cx.stop_propagation();
    }

    pub(super) fn side_panel_resize_mouse_move(
        &mut self,
        event: &MouseMoveEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(drag) = self.side_panel_resize_drag.as_ref() else {
            return;
        };
        if !event.dragging() {
            self.side_panel_resize_drag = None;
            cx.notify();
            return;
        }
        let desired = drag.start_width + f32::from(drag.start_x - event.position.x);
        self.resize_side_panel(desired, window, cx);
        cx.stop_propagation();
    }

    pub(super) fn side_panel_resize_mouse_up(
        &mut self,
        event: &MouseUpEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if event.button == MouseButton::Left && self.side_panel_resize_drag.take().is_some() {
            cx.stop_propagation();
            cx.notify();
        }
    }

    pub(super) fn toggle_branch_picker(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.state.connection.connected || runtime_busy(&self.state) {
            return;
        }
        self.branch_picker.open = !self.branch_picker.open;
        if self.branch_picker.open {
            self.approval_picker.open = false;
            self.model_picker_open = false;
            self.context_popover_open = false;
            self.branch_picker
                .search
                .update(cx, |input, cx| input.clear(cx));
            self.branch_picker.search.focus_handle(cx).focus(window, cx);
            self.branch_picker.index = 0;
            self.branch_picker.scroll.scroll_to_item(0);
            if !self.branch_request_pending() {
                self.branch_picker.error.clear();
                self.branch_picker.confirm_target = None;
                let id = self.runtime.request(Method::Execute, json!({
                "kind":"list_git_branches", "sessionId":self.state.navigation.current_session_id,
            }));
                self.pending_requests.insert(
                    id,
                    PendingRequest::GitBranches {
                        target: None,
                        confirmed: false,
                    },
                );
            }
        }
        cx.notify();
    }

    pub(super) fn select_highlighted_branch(&mut self, cx: &mut Context<Self>) {
        if self.branch_picker.confirm_target.is_some() {
            return;
        }
        let names = visible_git_branches(
            &self.state.workspace.branches,
            &self.state.workspace.branch,
            self.branch_picker.search.read(cx).text(),
        );
        if let Some(name) = names.get(self.branch_picker.index.min(names.len().saturating_sub(1))) {
            self.switch_branch(name.clone(), false, cx);
        }
    }

    pub(super) fn switch_branch(
        &mut self,
        target: String,
        confirmed: bool,
        cx: &mut Context<Self>,
    ) {
        if !self.state.connection.connected
            || runtime_busy(&self.state)
            || self.branch_request_pending()
            || !self
                .state
                .workspace
                .branches
                .iter()
                .any(|branch| branch["name"].as_str() == Some(&target))
            || (confirmed && self.branch_picker.confirm_target.as_deref() != Some(&target))
        {
            return;
        }
        if target == self.state.workspace.branch.as_ref() {
            self.branch_picker.open = false;
        } else {
            self.branch_picker.error.clear();
            self.branch_picker.confirm_target = None;
            let id = self.runtime.request(
                Method::Execute,
                json!({
                    "kind":"switch_git_branch", "target":target,
                    "decision": if confirmed { "confirm_dirty" } else { "" },
                    "sessionId":self.state.navigation.current_session_id,
                }),
            );
            self.pending_requests.insert(
                id,
                PendingRequest::GitBranches {
                    target: Some(target),
                    confirmed,
                },
            );
        }
        cx.notify();
    }

    pub(super) fn branch_picker_key(
        &mut self,
        event: &gpui::KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        match event.keystroke.key.as_str() {
            "up" | "down" => {
                if !self.branch_picker.open {
                    self.toggle_branch_picker(window, cx);
                } else {
                    let count = visible_git_branches(
                        &self.state.workspace.branches,
                        &self.state.workspace.branch,
                        self.branch_picker.search.read(cx).text(),
                    )
                    .len();
                    if count > 0 {
                        let step = if event.keystroke.key == "down" {
                            1
                        } else {
                            count - 1
                        };
                        self.branch_picker.index = (self.branch_picker.index + step) % count;
                        self.branch_picker
                            .scroll
                            .scroll_to_item(self.branch_picker.index);
                    }
                }
            }
            "enter" | "space" if self.branch_picker.focus.is_focused(window) => {
                if self.branch_picker.open {
                    self.select_highlighted_branch(cx);
                } else {
                    self.toggle_branch_picker(window, cx);
                }
            }
            "escape" => {
                self.branch_picker.open = false;
                self.branch_picker.focus.focus(window, cx);
            }
            "tab" if self.branch_picker.confirm_target.is_none() => {
                self.branch_picker.open = false;
                cx.notify();
                return;
            }
            _ => return,
        }
        window.prevent_default();
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn toggle_model_picker(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.approval_picker.open = false;
        self.branch_picker.open = false;
        self.route_picker_target = None;
        self.model_picker_open = !self.model_picker_open;
        if self.model_picker_open {
            self.context_popover_open = false;
            if self.state.catalogs.providers.is_empty() {
                self.refresh_model_catalog();
            }
            let locale = Locale::resolve(&self.state.settings.language);
            self.model_search.update(cx, |search, cx| {
                search.set_placeholder(locale.text("ui.searchModelsOrProviders"), cx);
                search.clear(cx);
            });
            self.model_search.focus_handle(cx).focus(window, cx);
        }
        cx.notify();
    }

    pub(super) fn open_route_picker(
        &mut self,
        scope: String,
        role: String,
        label: String,
        kind: RoutePickerKind,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let target = RoutePickerTarget {
            scope,
            role,
            label,
            kind,
        };
        if self.model_picker_open && self.route_picker_target.as_ref() == Some(&target) {
            self.model_picker_open = false;
            self.route_picker_target = None;
            cx.notify();
            return;
        }
        self.route_picker_target = Some(target);
        self.model_picker_open = true;
        self.context_popover_open = false;
        if self.state.catalogs.providers.is_empty() {
            self.refresh_model_catalog();
        }
        let locale = Locale::resolve(&self.state.settings.language);
        self.model_search.update(cx, |search, cx| {
            search.set_placeholder(locale.text("ui.searchModelNamesOrAliases"), cx);
            search.clear(cx);
        });
        self.model_search.focus_handle(cx).focus(window, cx);
        cx.notify();
    }

    pub(super) fn refresh_model_catalog(&self) {
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": "list_model_providers",
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
    }

    pub(super) fn set_subagent_setting(
        &mut self,
        kind: SubagentSettingKind,
        value: i64,
        cx: &mut Context<Self>,
    ) {
        self.subagent_setting_menu = None;
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": kind.action(),
                "target": value.to_string(),
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
        cx.notify();
    }
}
