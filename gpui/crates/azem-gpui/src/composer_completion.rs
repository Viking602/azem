use std::{ops::Range, time::Duration};

use azem_ipc::Method;
use gpui::{
    Anchor, BoxShadow, Context, Focusable, KeyDownEvent, MouseButton, MouseDownEvent, Role,
    ScrollHandle, Task, Window, anchored, deferred, div, hsla, point, prelude::*, px,
};
use serde_json::{Value, json};

use crate::{
    AzemWindow, PendingRequest, localization::Locale, model_selection::model_modes, surfaces::icon,
    text_input::file_icon, theme::ThemePalette,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum CompletionKind {
    Skill,
    File,
    Command,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct CompletionQuery {
    kind: CompletionKind,
    range: Range<usize>,
    needle: String,
}

#[derive(Clone, Debug)]
struct CompletionItem {
    kind: CompletionKind,
    name: String,
    detail: String,
    value: String,
    glyph: &'static str,
    enabled: bool,
}

#[derive(Default)]
pub(super) struct ComposerCompletion {
    query: Option<CompletionQuery>,
    last_query: Option<CompletionQuery>,
    pub generation: u64,
    index: usize,
    scroll: ScrollHandle,
    files: Vec<CompletionItem>,
    loading: bool,
    truncated: bool,
    error: String,
    pub submission_error: String,
    pub selected_skills: Vec<String>,
    debounce: Option<Task<()>>,
}

pub(super) fn skill_title(name: &str) -> String {
    name.split(['-', '_'])
        .filter(|part| !part.is_empty())
        .map(|part| {
            let mut chars = part.chars();
            chars.next().unwrap().to_uppercase().collect::<String>() + chars.as_str()
        })
        .collect::<Vec<_>>()
        .join(" ")
}

fn command_items(state: &crate::state::AppState, needle: &str) -> Vec<CompletionItem> {
    let locale = Locale::resolve(&state.settings.language);
    let needle = needle.to_lowercase();
    let busy = crate::runtime_busy(state);
    [
        ("mcp", "slash.mcp", "slash.mcpDetail", "server", "plugins"),
        (
            "compact",
            "slash.compact",
            "slash.compactDetail",
            "layers",
            "compress",
        ),
        (
            "rebuild",
            "slash.rebuild",
            "slash.rebuildDetail",
            "rotate-ccw",
            "",
        ),
        (
            "usage",
            "slash.usage",
            "slash.usageDetail",
            "gauge",
            "billing",
        ),
        (
            "settings",
            "slash.settings",
            "slash.settingsDetail",
            "settings",
            "config",
        ),
        (
            "reload-skills",
            "slash.reloadSkills",
            "slash.reloadSkillsDetail",
            "rotate-ccw",
            "reload",
        ),
        ("plan", "slash.plan", "slash.planDetail", "lightbulb", ""),
        (
            "agents",
            "slash.agents",
            "slash.agentsDetail",
            "bot",
            "subagents",
        ),
        (
            "approval",
            "slash.approval",
            "slash.approvalDetail",
            "shield-check",
            "approve permissions yolo",
        ),
        (
            "reasoning",
            "slash.reasoning",
            "slash.reasoningDetail",
            "brain",
            "reason",
        ),
        ("fast", "slash.fast", "slash.fastDetail", "gauge", "speed"),
        (
            "archive",
            "slash.archive",
            "slash.archiveDetail",
            "archive",
            "",
        ),
        ("new", "slash.new", "slash.newDetail", "plus", "chat"),
    ]
    .into_iter()
    .filter(|(id, _, _, _, _)| *id != "fast" || fast_available(state))
    .filter(|(id, label, _, _, aliases)| {
        [*id, locale.text(label), *aliases]
            .iter()
            .any(|value| value.to_lowercase().contains(&needle))
    })
    .map(|(id, label, detail, glyph, _)| {
        let needs_idle = matches!(
            id,
            "compact"
                | "rebuild"
                | "archive"
                | "new"
                | "plan"
                | "approval"
                | "reasoning"
                | "fast"
                | "reload-skills"
        );
        let needs_session = matches!(id, "compact" | "rebuild" | "archive");
        let enabled = state.connection.connected
            && (!needs_idle || !busy)
            && (!needs_session || !state.navigation.current_session_id.is_empty());
        CompletionItem {
            kind: CompletionKind::Command,
            name: locale.text(label).to_owned(),
            detail: locale
                .text(if needs_idle && busy {
                    "slash.waitForTurn"
                } else {
                    detail
                })
                .to_owned(),
            value: id.to_owned(),
            glyph,
            enabled,
        }
    })
    .collect()
}

fn fast_available(state: &crate::state::AppState) -> bool {
    model_modes(
        &state.catalogs.providers,
        state.settings.provider.as_ref(),
        state.settings.model.as_ref(),
        state.settings.reasoning.as_ref(),
        state.settings.chatgpt_fast_mode,
    )
    .fast_available
}

fn prompt_tokens(text: &str) -> Vec<Range<usize>> {
    let mut ranges = Vec::new();
    let mut start = None;
    let mut quote = None;
    let mut escaped = false;
    for (index, ch) in text.char_indices() {
        if start.is_none() && !ch.is_whitespace() {
            start = Some(index);
        }
        if escaped {
            escaped = false;
        } else if quote.is_some() && ch == '\\' {
            escaped = true;
        } else if quote == Some(ch) {
            quote = None;
        } else if quote.is_none() && matches!(ch, '"' | '`') {
            quote = Some(ch);
        } else if quote.is_none()
            && ch.is_whitespace()
            && let Some(start) = start.take()
        {
            ranges.push(start..index);
        }
    }
    if let Some(start) = start {
        ranges.push(start..text.len());
    }
    ranges
}

fn completion_query(text: &str, cursor: Option<usize>) -> Option<CompletionQuery> {
    let cursor = cursor?;
    let range = prompt_tokens(text)
        .into_iter()
        .find(|range| range.start < cursor && cursor <= range.end)?;
    let token = text.get(range.start..cursor)?;
    let marker = token.chars().next()?;
    if !matches!(marker, '/' | '@') {
        return None;
    }
    let raw = &token[1..];
    let quoted = marker == '@' && raw.starts_with('"');
    let needle = if quoted { &raw[1..] } else { raw };
    if needle.contains(['\n', '\r', '"']) || (!quoted && needle.chars().any(char::is_whitespace)) {
        return None;
    }
    Some(CompletionQuery {
        kind: if marker == '/' {
            CompletionKind::Skill
        } else {
            CompletionKind::File
        },
        range,
        needle: needle.to_owned(),
    })
}

fn skill_items(skills: &[Value], needle: &str) -> Vec<CompletionItem> {
    let needle = needle
        .strip_prefix("skill:")
        .unwrap_or(needle)
        .to_lowercase();
    let mut items = skills
        .iter()
        .filter_map(|skill| {
            let name = skill.get("name")?.as_str()?;
            if name.is_empty()
                || name.chars().any(char::is_whitespace)
                || skill.get("disabled").and_then(Value::as_bool) == Some(true)
            {
                return None;
            }
            let detail = skill
                .get("description")
                .and_then(Value::as_str)
                .unwrap_or_default();
            if !name.to_lowercase().contains(&needle) && !detail.to_lowercase().contains(&needle) {
                return None;
            }
            Some(CompletionItem {
                kind: CompletionKind::Skill,
                name: skill_title(name),
                detail: detail.to_owned(),
                value: format!("/skill:{name} "),
                glyph: "cube",
                enabled: true,
            })
        })
        .collect::<Vec<_>>();
    items.sort_by_key(|item| {
        (
            !item.name.to_lowercase().starts_with(&needle),
            item.name.to_lowercase(),
        )
    });
    items
}

// Keep the established activeSkills turn contract. Selected skills and explicit
// /skill:name tokens activate skills; ordinary slashes and file mentions stay text.
pub(super) fn prepare_prompt(
    source: &str,
    selected_skills: &[String],
    skills: &[Value],
    locale: Locale,
) -> Result<(String, Vec<String>), String> {
    let mut prompt = String::new();
    let mut active = Vec::new();
    let mut select = |name: &str| -> Result<(), String> {
        if !skills.iter().any(|skill| {
            skill["name"].as_str() == Some(name) && skill["disabled"].as_bool() != Some(true)
        }) {
            return Err(locale.format("completion.skillUnavailable", &[("name", name.to_owned())]));
        }
        if !active.iter().any(|item| item == name) {
            active.push(name.to_owned());
        }
        Ok(())
    };
    for name in selected_skills {
        select(name)?;
    }
    let mut previous = 0;
    for range in prompt_tokens(source) {
        prompt.push_str(&source[previous..range.start]);
        let token = &source[range.clone()];
        previous = range.end;
        if let Some(name) = token.strip_prefix("/skill:") {
            select(name)?;
        } else {
            prompt.push_str(token);
        }
    }
    prompt.push_str(&source[previous..]);
    let prompt = prompt.trim().to_owned();
    let prompt = if prompt.is_empty() && !active.is_empty() {
        locale.format("completion.skillPrompt", &[("names", active.join(", "))])
    } else {
        prompt
    };
    Ok((prompt, active))
}

pub(super) fn turn_payload(
    state: &crate::state::AppState,
    source: &str,
    selected_skills: &[String],
    attachments: Vec<Value>,
) -> Result<Value, String> {
    let (prompt, active_skills) = prepare_prompt(
        source,
        selected_skills,
        &state.catalogs.skills,
        Locale::resolve(&state.settings.language),
    )?;
    Ok(json!({
        "sessionId": state.navigation.current_session_id,
        "prompt": prompt,
        "provider": state.settings.provider,
        "model": state.settings.model,
        "reasoning": state.settings.reasoning,
        "agentMode": state.settings.agent_mode,
        "planMode": state.runtime.plan_mode,
        "disableSubagents": false,
        "activeSkills": active_skills,
        "images": attachments,
    }))
}

impl ComposerCompletion {
    pub fn dismiss(&mut self) {
        self.query = None;
        self.generation += 1;
        self.debounce = None;
        self.loading = false;
    }

    pub fn is_open(&self) -> bool {
        self.query.is_some()
    }

    fn items(&self, state: &crate::state::AppState) -> Vec<CompletionItem> {
        match &self.query {
            Some(query) if query.kind == CompletionKind::Skill => {
                let mut items = if query.range.start == 0 {
                    command_items(state, &query.needle)
                } else {
                    Vec::new()
                };
                items.extend(skill_items(&state.catalogs.skills, &query.needle));
                items
            }
            Some(_) => self.files.clone(),
            None => Vec::new(),
        }
    }

    pub fn receive_files(
        &mut self,
        generation: u64,
        result: Result<Value, String>,
        locale: Locale,
    ) {
        if generation != self.generation
            || self
                .query
                .as_ref()
                .is_none_or(|query| query.kind != CompletionKind::File)
        {
            return;
        }
        self.loading = false;
        match result {
            Ok(value) => {
                self.truncated = value["truncated"].as_bool().unwrap_or(false);
                let Some(entries) = value["entries"].as_array() else {
                    self.error = locale.text("completion.invalidFiles").to_owned();
                    return;
                };
                self.files = entries
                    .iter()
                    .filter_map(|entry| {
                        let path = entry["path"].as_str()?;
                        Some(CompletionItem {
                            kind: CompletionKind::File,
                            name: entry["name"].as_str().unwrap_or(path).to_owned(),
                            detail: path.to_owned(),
                            value: path.to_owned(),
                            glyph: file_icon(path),
                            enabled: true,
                        })
                    })
                    .collect();
            }
            Err(error) => self.error = error,
        }
    }
}

impl AzemWindow {
    pub(super) fn sync_completions(&mut self, cx: &mut Context<Self>) {
        let input = self.composer.read(cx);
        let query = if self.settings_open || !self.state.connection.connected {
            None
        } else {
            completion_query(input.text(), input.completion_cursor())
        };
        if query == self.completion.last_query {
            return;
        }
        self.completion.dismiss();
        self.completion.last_query = query.clone();
        self.completion.query = query.clone();
        self.completion.index = 0;
        self.completion.scroll.scroll_to_item(0);
        self.completion.files.clear();
        self.completion.error.clear();
        self.completion.truncated = false;
        if let Some(query) = query {
            self.approval_picker.open = false;
            self.branch_picker.open = false;
            self.model_picker_open = false;
            self.context_popover_open = false;
            if query.kind == CompletionKind::File {
                self.completion.loading = true;
                let generation = self.completion.generation;
                self.completion.debounce = Some(cx.spawn(async move |this, cx| {
                    cx.background_executor()
                        .timer(Duration::from_millis(120))
                        .await;
                    let _ = this.update(cx, |this, _| {
                        if generation == this.completion.generation {
                            let id = this.runtime.request(
                                Method::SearchWorkspaceFiles,
                                json!({"query": query.needle, "limit": 50}),
                            );
                            this.pending_requests
                                .insert(id, PendingRequest::CompletionFiles { generation });
                        }
                    });
                }));
            }
        }
        cx.notify();
    }

    pub(super) fn choose_completion(
        &mut self,
        index: usize,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(query) = self.completion.query.clone() else {
            return;
        };
        let input = self.composer.read(cx);
        if completion_query(input.text(), input.completion_cursor()) != Some(query.clone()) {
            return;
        }
        let Some(item) = self.completion.items(&self.state).get(index).cloned() else {
            return;
        };
        if !item.enabled {
            return;
        }
        self.completion.dismiss();
        self.composer.update(cx, |input, cx| {
            if item.kind == CompletionKind::File {
                input.insert_file_reference(query.range, &item.value, window, cx);
            } else {
                input.replace_byte_range(query.range, "", window, cx);
            }
        });
        self.composer.focus_handle(cx).focus(window, cx);
        match item.kind {
            CompletionKind::Skill => {
                let name = item.value.trim().trim_start_matches("/skill:").to_owned();
                if !self.completion.selected_skills.contains(&name) {
                    self.completion.selected_skills.push(name);
                }
            }
            CompletionKind::Command => self.execute_composer_command(&item.value, window, cx),
            CompletionKind::File => {}
        }
        cx.notify();
    }

    fn execute_composer_command(
        &mut self,
        command: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let session_id = self.state.navigation.current_session_id.to_string();
        match command {
            "settings" | "usage" | "mcp" => {
                self.settings_open = true;
                self.select_settings_section(
                    match command {
                        "usage" => "usage",
                        "mcp" => "extensions:mcp",
                        _ => "appearance",
                    },
                    cx,
                );
                if command == "mcp" {
                    self.runtime.request(
                        Method::Execute,
                        json!({"kind":"refresh_mcp", "sessionId":session_id}),
                    );
                }
            }
            "compact" | "rebuild" | "reload-skills" | "archive" | "new" => {
                let kind = match command {
                    "compact" | "rebuild" => "compact",
                    "reload-skills" => "reload_skills",
                    "archive" => "archive_session",
                    _ => "new_session",
                };
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":kind, "target":session_id, "sessionId":session_id}),
                );
            }
            "plan" => self.state.runtime.plan_mode = !self.state.runtime.plan_mode,
            "agents" => {
                self.state.navigation.surface = crate::state::Surface::Thread;
                if let Some(id) = self
                    .state
                    .runtime
                    .agents
                    .first()
                    .and_then(|agent| agent["id"].as_str())
                {
                    self.state.runtime.selected_agent_id = id.to_owned().into();
                    self.runtime.request(
                        Method::Execute,
                        json!({"kind":"inspect_agent", "target":id, "sessionId":session_id}),
                    );
                } else {
                    self.settings_open = true;
                    self.select_settings_section("agents", cx);
                }
            }
            "approval" => self.toggle_approval_picker(window, cx),
            "reasoning" => {
                self.route_picker_target = None;
                let levels = self.selected_model_reasoning_levels();
                let current = self.selected_model_modes().reasoning;
                if !levels.is_empty() {
                    let index = levels
                        .iter()
                        .position(|level| level == &current)
                        .unwrap_or(0);
                    self.set_reasoning(levels[(index + 1) % levels.len()].clone(), cx);
                }
            }
            "fast" => {
                self.route_picker_target = None;
                self.toggle_fast_mode(cx);
            }
            _ => {}
        }
    }

    pub(super) fn selected_skills_view(
        &self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        if self.completion.selected_skills.is_empty() {
            return None;
        }
        let locale = Locale::resolve(&self.state.settings.language);
        let font_size = crate::theme::AppearancePreferences::current(cx).chat_font_size;
        Some(
            div()
                .id("composer-selected-skills")
                .max_w(gpui::relative(0.6))
                .flex_shrink_0()
                .ml(px(8.))
                .mt(px(10.))
                .flex()
                .items_center()
                .gap_2()
                .overflow_x_scroll()
                .text_size(px(font_size))
                .line_height(px(font_size * 1.6))
                .text_color(palette.accent)
                .children(self.completion.selected_skills.iter().enumerate().map(
                    |(index, name)| {
                        let title = skill_title(name);
                        div()
                            .id(("selected-skill", index))
                            .role(Role::Button)
                            .aria_label(
                                locale.format("completion.removeSkill", &[("name", title.clone())]),
                            )
                            .tab_stop(true)
                            .flex()
                            .items_center()
                            .gap_1()
                            .flex_shrink_0()
                            .whitespace_nowrap()
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.remove_selected_skill(index, window, cx);
                            }))
                            .on_key_down(cx.listener(
                                move |this, event: &KeyDownEvent, window, cx| {
                                    if matches!(
                                        event.keystroke.key.as_str(),
                                        "enter" | "space" | "backspace" | "delete"
                                    ) {
                                        this.remove_selected_skill(index, window, cx);
                                        window.prevent_default();
                                        cx.stop_propagation();
                                    }
                                },
                            ))
                            .child(icon("cube", font_size, palette.accent))
                            .child(title)
                    },
                ))
                .into_any_element(),
        )
    }

    fn remove_selected_skill(&mut self, index: usize, window: &mut Window, cx: &mut Context<Self>) {
        if index < self.completion.selected_skills.len() {
            self.completion.selected_skills.remove(index);
            self.completion.submission_error.clear();
            self.composer.focus_handle(cx).focus(window, cx);
            cx.notify();
        }
    }

    pub(super) fn completion_key(
        &mut self,
        event: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.composer.read(cx).is_composing() || event.keystroke.modifiers.modified() {
            return;
        }
        if !self.completion.is_open() {
            return;
        }
        let count = self.completion.items(&self.state).len();
        match event.keystroke.key.as_str() {
            "escape" => self.completion.dismiss(),
            "up" | "down" => {
                if count > 0 {
                    self.completion.index = (self.completion.index
                        + if event.keystroke.key == "up" {
                            count - 1
                        } else {
                            1
                        })
                        % count;
                    self.completion.scroll.scroll_to_item(self.completion.index);
                }
            }
            "enter" | "tab" => self.choose_completion(
                self.completion.index.min(count.saturating_sub(1)),
                window,
                cx,
            ),
            _ => return,
        }
        window.prevent_default();
        cx.stop_propagation();
        cx.notify();
    }

    // GPUI dispatches bound actions before raw key listeners. Capture these
    // before TextInput/root handlers consume them; Tab/arrows still use keys.
    pub(super) fn submit_completion(
        &mut self,
        _: &crate::Submit,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.completion.is_open() && !self.composer.read(cx).is_composing() {
            self.choose_completion(self.completion.index, window, cx);
            cx.stop_propagation();
        }
    }

    pub(super) fn backspace_completion(
        &mut self,
        _: &crate::text_input::Backspace,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let input = self.composer.read(cx);
        if input.focus_handle(cx).is_focused(window)
            && !input.is_composing()
            && input.completion_cursor() == Some(0)
            && !self.completion.selected_skills.is_empty()
        {
            self.remove_selected_skill(self.completion.selected_skills.len() - 1, window, cx);
            cx.stop_propagation();
        }
    }

    fn dismiss_completion_outside(
        &mut self,
        event: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if event.button == MouseButton::Left
            && self
                .composer
                .read(cx)
                .bounds()
                .is_none_or(|bounds| !bounds.contains(&event.position))
        {
            self.completion.dismiss();
            cx.stop_propagation();
            cx.notify();
        }
    }

    pub(super) fn completion_menu(
        &self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        let query = self.completion.query.as_ref()?;
        let bounds = self.composer.read(cx).bounds()?;
        let locale = Locale::resolve(&self.state.settings.language);
        let (heading, empty) = if query.kind == CompletionKind::Skill {
            ("completion.slash", "completion.noSkills")
        } else {
            ("completion.files", "completion.noFiles")
        };
        let items = self.completion.items(&self.state);
        let has_commands = items
            .iter()
            .any(|item| item.kind == CompletionKind::Command);
        let menu = div()
            .id("composer-completion-menu")
            .role(Role::ListBox)
            .aria_label(locale.text(heading))
            .w(px(if has_commands { 560. } else { 420. })
                .min((self.window_size.width - px(32.)).max(px(200.))))
            .rounded(px(10.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .text_color(palette.ink)
            .text_xs()
            .p_1()
            .shadow(vec![
                BoxShadow::new(px(0.), px(6.), hsla(0., 0., 0., 0.15)).blur_radius(px(20.)),
            ])
            .occlude()
            .on_mouse_down_out(cx.listener(Self::dismiss_completion_outside))
            .child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(palette.muted)
                    .text_size(px(10.))
                    .child(locale.text(heading)),
            )
            .child(
                div()
                    .id("composer-completion-list")
                    .max_h(px(if has_commands { 360. } else { 240. }))
                    .overflow_y_scroll()
                    .track_scroll(&self.completion.scroll)
                    .children(items.iter().enumerate().map(|(index, item)| {
                        div()
                            .id(("composer-completion-option", index))
                            .role(Role::ListBoxOption)
                            .aria_label(item.name.clone())
                            .aria_description(item.detail.clone())
                            .aria_selected(index == self.completion.index)
                            .when(index == self.completion.index, |row| {
                                row.aria_active_descendant()
                            })
                            .px_2()
                            .py_2()
                            .rounded(px(6.))
                            .flex()
                            .items_center()
                            .gap_2()
                            .bg(if index == self.completion.index {
                                palette.hover
                            } else {
                                palette.paper
                            })
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.choose_completion(index, window, cx)
                            }))
                            .child(div().size(px(16.)).flex_shrink_0().child(icon(
                                item.glyph,
                                15.,
                                if item.enabled {
                                    palette.muted
                                } else {
                                    palette.faint
                                },
                            )))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .flex()
                                    .when(item.kind == CompletionKind::Command, |text| {
                                        text.items_center().justify_between().gap_4()
                                    })
                                    .when(item.kind != CompletionKind::Command, |text| {
                                        text.flex_col().gap_1()
                                    })
                                    .text_color(if item.enabled {
                                        palette.ink
                                    } else {
                                        palette.faint
                                    })
                                    .child(
                                        div().flex_shrink_0().truncate().child(item.name.clone()),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(10.))
                                            .line_height(px(15.))
                                            .text_color(palette.muted)
                                            .when(item.kind == CompletionKind::Command, |detail| {
                                                detail.max_w(gpui::relative(0.68))
                                            })
                                            .truncate()
                                            .child(item.detail.clone()),
                                    ),
                            )
                    })),
            )
            .when(items.is_empty(), |menu| {
                menu.child(div().p_2().text_color(palette.muted).child(
                    if self.completion.loading {
                        locale.text("status.loading").to_owned()
                    } else if !self.completion.error.is_empty() {
                        self.completion.error.clone()
                    } else {
                        locale.text(empty).to_owned()
                    },
                ))
            })
            .when(self.completion.truncated, |menu| {
                menu.child(
                    div()
                        .p_2()
                        .text_size(px(10.))
                        .text_color(palette.muted)
                        .child(locale.text("completion.refine")),
                )
            });
        Some(
            deferred(
                anchored()
                    .anchor(Anchor::BottomLeft)
                    .position(point(bounds.left(), bounds.top() - px(5.)))
                    .snap_to_window_with_margin(px(8.))
                    .child(menu),
            )
            .with_priority(25)
            .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn composer_shortcuts_capture_bound_actions_before_the_text_input() {
        let source = crate::MAIN_SOURCE;
        let composer = source.split("fn composer_view(").nth(1).unwrap();
        assert!(composer.contains(".capture_key_down(cx.listener(Self::completion_key))"));
        for handler in ["submit_completion", "backspace_completion"] {
            assert!(
                composer.contains(&format!(".capture_action(cx.listener(Self::{handler}))")),
                "missing {handler} action capture"
            );
        }
    }

    #[test]
    fn slash_keeps_basic_commands_alongside_enabled_skills() {
        let picker = ComposerCompletion {
            query: completion_query("/", Some(1)),
            ..Default::default()
        };
        let mut state = crate::state::AppState::default();
        state.connection.connected = true;
        state.navigation.current_session_id = "session-1".into();
        state.catalogs.skills = vec![json!({"name":"kratos-skill"})];
        let items = picker.items(&state);
        assert!(
            items
                .iter()
                .any(|item| item.value == "/skill:kratos-skill ")
        );
        for command in [
            "mcp", "compact", "rebuild", "settings", "usage", "plan", "new",
        ] {
            assert!(
                items.iter().any(|item| item.value == command),
                "missing /{command}"
            );
        }
        assert!(
            items.iter().position(|item| item.value == "mcp").unwrap()
                < items
                    .iter()
                    .position(|item| item.kind == CompletionKind::Skill)
                    .unwrap()
        );
        assert!(!items.iter().any(|item| item.value == "fast"));
        state.runtime.running = true;
        let items = picker.items(&state);
        assert!(
            !items
                .iter()
                .find(|item| item.value == "compact")
                .unwrap()
                .enabled
        );
        assert!(
            items
                .iter()
                .find(|item| item.value == "usage")
                .unwrap()
                .enabled
        );
        assert_eq!(command_items(&state, "压缩").len(), 0);
        state.settings.language = "zh-CN".into();
        assert_eq!(command_items(&state, "压缩")[0].value, "compact");
    }

    #[test]
    fn trigger_is_caret_aware_and_does_not_treat_paths_or_emails_as_commands() {
        for text in [
            "hello@example.com",
            "https://example.com/path",
            "src/main.rs",
            "say /check done",
        ] {
            assert_eq!(completion_query(text, Some(text.len())), None, "{text}");
        }
        let text = "请🦀 /verify 保留";
        let cursor = "请🦀 /ver".len();
        let query = completion_query(text, Some(cursor)).unwrap();
        assert_eq!(query.needle, "ver");
        assert_eq!(&text[query.range], "/verify");
        assert!(completion_query(text, None).is_none());
        assert_eq!(
            completion_query("@\"中文 文件", Some("@\"中文 文件".len()))
                .unwrap()
                .needle,
            "中文 文件"
        );
        assert!(completion_query("@\"中文 文件\" ", Some("@\"中文 文件\" ".len())).is_none());
    }

    #[test]
    fn selection_activates_only_real_enabled_skills_and_keeps_file_mentions() {
        let skills = vec![
            json!({"name":"check","description":"Review changes"}),
            json!({"name":"disabled","disabled":true}),
        ];
        assert_eq!(skill_items(&skills, "review")[0].value, "/skill:check ");
        assert_eq!(skill_items(&skills, "").len(), 1);
        let (prompt, active) = prepare_prompt(
            "/skill:check inspect @src/main.rs /skill:check",
            &[],
            &skills,
            Locale::resolve("en"),
        )
        .unwrap();
        assert_eq!(prompt, "inspect @src/main.rs");
        assert_eq!(active, vec!["check"]);
        assert!(prepare_prompt("/skill:disabled", &[], &skills, Locale::resolve("en")).is_err());
        assert!(prepare_prompt("/skill:missing", &[], &skills, Locale::resolve("en")).is_err());
        assert!(
            !prepare_prompt("/skill:check", &[], &skills, Locale::resolve("en"))
                .unwrap()
                .0
                .is_empty()
        );
        let literal = "explain `/skill:check` and @\"folder /skill:check\"";
        assert_eq!(
            prepare_prompt(literal, &[], &skills, Locale::resolve("en")).unwrap(),
            (literal.to_owned(), vec![])
        );
        assert!(completion_query("`/check", Some(7)).is_none());
        let quoted = "@\"folder /skill:check";
        assert_eq!(
            completion_query(quoted, Some(quoted.len())).unwrap().kind,
            CompletionKind::File
        );
    }

    #[test]
    fn start_and_queued_turns_preserve_skills_files_and_image_attachments() {
        let mut state = crate::state::AppState::default();
        state.navigation.current_session_id = "session-1".into();
        state.catalogs.skills = vec![json!({"name":"check"})];
        let raw_draft = "/skill:check inspect @\"docs/中文 说明.md\"";
        let images = vec![json!({"id":"image-1"})];
        let payload = turn_payload(&state, raw_draft, &[], images.clone()).unwrap();
        assert_eq!(payload["activeSkills"], json!(["check"]));
        assert_eq!(payload["prompt"], "inspect @\"docs/中文 说明.md\"");
        assert_eq!(payload["sessionId"], "session-1");
        assert_eq!(payload["images"], json!(images));
        state.catalogs.skills[0]["disabled"] = json!(true);
        assert!(turn_payload(&state, raw_draft, &[], images).is_err());
    }

    #[test]
    fn blue_skill_labels_keep_canonical_ids_out_of_the_editable_prompt() {
        let mut state = crate::state::AppState::default();
        state.catalogs.skills = vec![json!({"name":"kratos-skill"})];
        let selected = vec!["kratos-skill".to_owned()];
        assert_eq!(skill_title(&selected[0]), "Kratos Skill");
        let payload = turn_payload(&state, "inspect @README.md", &selected, vec![]).unwrap();
        assert_eq!(payload["prompt"], "inspect @README.md");
        assert_eq!(payload["activeSkills"], json!(["kratos-skill"]));
        let payload = turn_payload(&state, "/skill:kratos-skill", &selected, vec![]).unwrap();
        assert_eq!(payload["activeSkills"], json!(["kratos-skill"]));
        assert!(!payload["prompt"].as_str().unwrap().is_empty());
        state.catalogs.skills[0]["disabled"] = json!(true);
        assert!(turn_payload(&state, "inspect", &selected, vec![]).is_err());
    }

    #[test]
    fn explicit_skill_prefix_never_runs_a_same_named_command_and_fast_uses_real_capabilities() {
        let mut state = crate::state::AppState::default();
        state.catalogs.skills = vec![json!({"name":"mcp"})];
        let picker = ComposerCompletion {
            query: completion_query("/skill:mcp", Some(10)),
            ..Default::default()
        };
        let items = picker.items(&state);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].kind, CompletionKind::Skill);
        state.settings.provider = "chatgpt".into();
        state.settings.model = "model-1".into();
        state.catalogs.providers =
            vec![json!({"id":"chatgpt", "models":[{"id":"model-1", "capabilities":["fast"]}]})];
        assert!(fast_available(&state));
        state.apply_direct_event(
            json!({"kind":"model_routes", "data":{"chatgpt_fast_mode":"true"}}),
        );
        assert!(state.settings.chatgpt_fast_mode);
        state.apply_direct_event(
            json!({"kind":"model_routes", "data":{"chatgpt_fast_mode":"false"}}),
        );
        assert!(!state.settings.chatgpt_fast_mode);
        state.settings.provider = "grok".into();
        assert!(!fast_available(&state));
    }

    #[test]
    fn fast_command_is_available_for_an_actual_cursor_speed_pair() {
        let mut state = crate::state::AppState::default();
        state.settings.provider = "cursor".into();
        state.settings.model = "gpt-5.6-sol-high".into();
        state.settings.reasoning = "high".into();
        state.catalogs.providers = vec![json!({"id":"cursor","models":[
            {"id":"gpt-5.6-sol-high"}, {"id":"gpt-5.6-sol-high-fast"}
        ]})];
        assert!(
            command_items(&state, "fast")
                .iter()
                .any(|item| item.value == "fast")
        );
        state.catalogs.providers[0]["models"][1]["disabled"] = json!(true);
        assert!(command_items(&state, "fast").is_empty());
    }

    #[test]
    fn late_file_results_cannot_replace_a_new_query_or_reopen_a_dismissed_menu() {
        let mut picker = ComposerCompletion {
            generation: 2,
            query: completion_query("@new", Some(4)),
            ..Default::default()
        };
        let result = json!({"entries":[{"name":"old","path":"old"}]});
        picker.receive_files(1, Ok(result.clone()), Locale::resolve("en"));
        assert!(picker.files.is_empty());
        picker.dismiss();
        picker.receive_files(2, Ok(result), Locale::resolve("en"));
        assert!(picker.files.is_empty());
        assert!(!picker.is_open());
    }
}
