mod assets;
mod composer_completion;
mod fast_particles;
mod localization;
mod markdown;
mod model_selection;
mod runtime_connection;
mod state;
mod surfaces;
mod terminal_emulator;
mod text_input;
mod theme;
use std::{
    cell::RefCell,
    collections::{HashMap, HashSet},
    fs,
    path::PathBuf,
    rc::Rc,
    time::{Duration, Instant},
};

use assets::Assets;
use azem_ipc::{ClientEvent, Method};
use composer_completion::{ComposerCompletion, prepare_prompt, turn_payload};
use fast_particles::FastParticles;
use gpui::{
    App, Bounds, BoxShadow, ClickEvent, Context, Entity, FocusHandle, Focusable, FollowMode,
    KeyBinding, ListAlignment, ListState, MouseButton, MouseDownEvent, MouseMoveEvent,
    MouseUpEvent, ObjectFit, Pixels, PromptButton, PromptLevel, Role, ScrollHandle, Size,
    StyledImage, Task, UniformListScrollHandle, Window, WindowBounds, WindowHandle, WindowOptions,
    div, hsla, img, list, prelude::*, px, relative, rgb, rgba, size, svg,
};
use gpui_platform::application;
use localization::{Labels, Locale, labels};
use model_selection::{ModelModes, cursor_selection, model_choices, model_modes};
use runtime_connection::{
    RuntimeConnection, RuntimeMessage, RuntimeOptions, restore_desktop_workspace,
};
use serde_json::json;
use state::{AppState, Surface, unix_millis};
use surfaces::*;
use terminal_emulator::TerminalEmulator;
use text_input::TextInput;

use theme::{AppearancePreferences, ThemePalette};
gpui::actions!(
    azem,
    [
        Submit,
        ToggleSearch,
        ToggleSettings,
        FindSettings,
        ToggleTerminal,
        CloseOverlay
    ]
);

const TRANSCRIPT_COMPOSER_CLEARANCE: f32 = 64.;
const SIDE_PANEL_DEFAULT_WIDTH: f32 = 420.;
const SIDE_PANEL_MIN_WIDTH: f32 = 260.;
const MAIN_TEXT_MIN_WIDTH: f32 = 640.;
const ENVIRONMENT_PANEL_RESERVED_WIDTH: f32 = 312.;
const CHAT_COLUMN_MAX_WIDTH: f32 = 736.;
const CHAT_COLUMN_GUTTER: f32 = 12.;
const CHAT_COLUMN_GUTTER_WIDE: f32 = 20.;

fn eased_side_panel_width(from: f32, to: f32, elapsed: Duration) -> f32 {
    let progress = (elapsed.as_secs_f32() / SIDE_PANEL_TRANSITION.as_secs_f32()).clamp(0., 1.);
    let eased = 1. - (1. - progress).powi(5);
    from + (to - from) * eased
}

fn workspace_width(window_width: f32) -> f32 {
    (window_width - SIDEBAR_WIDTH).max(0.)
}

fn side_panel_max_width(workspace_width: f32) -> Option<f32> {
    let available = workspace_width - MAIN_TEXT_MIN_WIDTH;
    (available >= SIDE_PANEL_MIN_WIDTH).then_some(available)
}

fn side_panel_width_for_workspace(workspace_width: f32) -> Option<f32> {
    side_panel_max_width(workspace_width)
        .map(|maximum| (workspace_width / 2.).clamp(SIDE_PANEL_MIN_WIDTH, maximum))
}

fn environment_panel_fits(workspace_width: f32, side_panel_width: f32) -> bool {
    workspace_width - side_panel_width - ENVIRONMENT_PANEL_RESERVED_WIDTH >= MAIN_TEXT_MIN_WIDTH
}

fn chat_column_gutter(content_width: f32) -> f32 {
    if content_width >= MAIN_TEXT_MIN_WIDTH {
        CHAT_COLUMN_GUTTER_WIDE
    } else {
        CHAT_COLUMN_GUTTER
    }
}

fn chat_column_animation_offset(layout_width: f32, visible_width: f32) -> f32 {
    (layout_width - visible_width).max(0.) / 2.
}
const APPROVAL_MODES: [(&str, &str, &str); 3] = [
    ("prompt", "ui.askEveryTime", "approval.promptDescription"),
    (
        "auto_review",
        "ui.autoReview",
        "approval.autoReviewDescription",
    ),
    ("yolo", "approval.yolo", "approval.yoloDescription"),
];

enum PendingRequest {
    ResumeSession {
        sequence: Option<i64>,
    },
    ReplyForkTree {
        session_id: String,
        target_id: String,
        anchor: ReplyForkAnchor,
    },
    ReplyFork {
        target_id: String,
    },
    RemoveProject {
        workspace: String,
        next_workspace: Option<String>,
    },
    Search,
    CompletionFiles {
        generation: u64,
    },
    Attachment,
    Entries,
    File,
    Changes,
    Change,
    PullRequests,
    EnvironmentGit,
    PullRequestDetail,
    PullRequestMonitor,
    Usage,
    Terminals,
    CreateTerminal,
    GitBranches {
        target: Option<String>,
        confirmed: bool,
    },
    ApprovalMode,
    ModelSelection {
        target: Option<RoutePickerTarget>,
        session_id: String,
        provider: String,
        model: String,
        reasoning: String,
    },
    ChatGPTFastMode(bool),
    ExtensionAction {
        source_text: Option<String>,
    },
    SystemFonts {
        language: String,
    },
    LanguageChange {
        language: String,
    },
    SecuritySave {
        payload: serde_json::Value,
    },
    Turn {
        source_text: String,
        selected_skills: Vec<String>,
        attachments: Vec<serde_json::Value>,
        queued_id: Option<String>,
    },
    QueuedGuide {
        queued_id: String,
        prompt: String,
        attachments: Vec<serde_json::Value>,
    },
}

#[derive(Clone, Copy, Debug)]
struct ReplyForkAnchor {
    sequence: Option<i64>,
    assistant_ordinal: usize,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ReplyPopoverKind {
    Hooks,
    Memories,
}

#[derive(Clone, Debug)]
enum SidebarMenuTarget {
    Session {
        id: String,
        title: String,
        workspace: String,
        pinned: bool,
    },
    Project {
        workspace: String,
    },
}

#[derive(Clone, Copy, Debug)]
enum SidebarMenuAction {
    ToggleSessionPin,
    RenameSession,
    MarkSessionUnread,
    ArchiveSession,
    CopyWorkspace,
    CopySessionId,
    RevealProject,
    ArchiveProjectSessions,
    CopyProjectPath,
    RemoveProject,
}

#[derive(Clone, Debug)]
struct SidebarContextMenu {
    target: SidebarMenuTarget,
    position: gpui::Point<Pixels>,
}

#[derive(Clone)]
struct ReplyActionsSnapshot {
    hooks: Vec<serde_json::Value>,
    hook_catalog: serde_json::Value,
    popover: Option<(String, ReplyPopoverKind)>,
    feedback: HashMap<String, i8>,
    hovered_message: Option<String>,
}

#[derive(Clone, Debug)]
struct QueuedPrompt {
    id: String,
    session_id: String,
    prompt: String,
    selected_skills: Vec<String>,
    attachments: Vec<serde_json::Value>,
    failed: bool,
}

#[derive(Clone)]
struct QueuedPromptDrag {
    id: String,
    session_id: String,
    label: String,
}

impl Render for QueuedPromptDrag {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div()
            .max_w(px(420.))
            .px_3()
            .py_2()
            .rounded(px(9.))
            .bg(rgb(0xf5f5f5))
            .text_sm()
            .child(self.label.clone())
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct RoutePickerTarget {
    scope: String,
    role: String,
    label: String,
    kind: RoutePickerKind,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum RoutePickerKind {
    Model,
    Reasoning,
}

struct ReasoningDrag {
    progress: f32,
    selection: (String, String, String),
    session_id: String,
    target: Option<RoutePickerTarget>,
}

struct SidePanelResizeDrag {
    start_x: Pixels,
    start_width: f32,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SubagentSettingKind {
    Concurrency,
    Depth,
    ShellConcurrency,
    ShellWallClock,
    AwaitTimeout,
    IdleTimeout,
}

impl SubagentSettingKind {
    fn action(self) -> &'static str {
        match self {
            Self::Concurrency => "set_subagent_concurrency",
            Self::Depth => "set_subagent_depth",
            Self::ShellConcurrency => "set_shell_concurrency",
            Self::ShellWallClock => "set_shell_max_wall_clock",
            Self::AwaitTimeout => "set_subagent_await_timeout",
            Self::IdleTimeout => "set_subagent_idle_timeout",
        }
    }

    fn id(self) -> &'static str {
        match self {
            Self::Concurrency => "concurrency",
            Self::Depth => "depth",
            Self::ShellConcurrency => "shell-concurrency",
            Self::ShellWallClock => "shell-wall-clock",
            Self::AwaitTimeout => "await-timeout",
            Self::IdleTimeout => "idle-timeout",
        }
    }

    fn menu_values(self) -> &'static [i64] {
        match self {
            Self::Depth => &[-1, 0, 1, 2, 3],
            Self::ShellWallClock => &[60, 120, 300, 600, 900, 1800, 3600, 7200],
            Self::AwaitTimeout => &[0, 30, 60, 300, 600, 1800],
            Self::IdleTimeout => &[0, 60, 120, 300, 600, 900, 1800],
            Self::Concurrency | Self::ShellConcurrency => &[],
        }
    }
}

#[derive(Default)]
struct ProcessExpansion {
    open: HashSet<String>,
    closed: HashSet<String>,
}

impl ProcessExpansion {
    fn is_expanded(&self, key: &str, default_open: bool) -> bool {
        !self.closed.contains(key) && (default_open || self.open.contains(key))
    }

    fn toggle(&mut self, key: &str, default_open: bool) {
        if self.is_expanded(key, default_open) {
            self.open.remove(key);
            self.closed.insert(key.to_string());
        } else {
            self.closed.remove(key);
            self.open.insert(key.to_string());
        }
    }
}

struct ExtensionSettings {
    search: Entity<TextInput>,
    source: Entity<TextInput>,
    scope: &'static str,
    busy: bool,
}

struct BranchPicker {
    search: Entity<TextInput>,
    focus: FocusHandle,
    scroll: ScrollHandle,
    open: bool,
    index: usize,
    error: String,
    confirm_target: Option<String>,
    button_bounds: Option<Bounds<Pixels>>,
}

struct ApprovalPicker {
    open: bool,
    index: usize,
    focus: FocusHandle,
    button_bounds: Option<Bounds<Pixels>>,
    error: String,
}

const SECURITY_NUMBERS: [(&str, f64, f64); 6] = [
    ("workers", 1., 32.),
    ("subagents", 0., 32.),
    ("stopAfterNoNew", 1., 1000.),
    ("stopAfterConsecutiveErrors", 1., 1000.),
    ("maxDiscoveryRuns", 1., 1000.),
    ("maxTimeHours", 0.5, 96.),
];

struct NativeSettings {
    language_menu_open: bool,
    language_index: usize,
    language_saving: bool,
    language_focus: FocusHandle,
    language_scroll: gpui::ScrollHandle,
    search: Entity<TextInput>,
    font_search: Entity<TextInput>,
    font_menu_open: bool,
    fonts: Vec<serde_json::Value>,
    fonts_loading: bool,
    security_fields: Vec<Entity<TextInput>>,
    security_draft: serde_json::Value,
    security_baseline: serde_json::Value,
    security_busy: bool,
    security_saved: bool,
}

impl NativeSettings {
    fn security_payload(
        &self,
        cx: &gpui::App,
        locale: Locale,
    ) -> Result<serde_json::Value, String> {
        security_settings_payload(
            &self.security_draft,
            self.security_fields
                .iter()
                .map(|input| input.read(cx).text()),
            locale,
        )
    }

    fn security_dirty(&self, cx: &gpui::App) -> bool {
        !self.security_draft.is_null()
            && !self
                .security_payload(cx, Locale::resolve("en"))
                .is_ok_and(|value| value == self.security_baseline)
    }

    fn sync_security(&mut self, config: &serde_json::Value, cx: &mut gpui::App) {
        if !config.is_object() || self.security_busy || self.security_dirty(cx) {
            return;
        }
        self.security_draft = config.clone();
        for ((key, _, _), input) in SECURITY_NUMBERS.iter().zip(&self.security_fields) {
            let text = config[key].to_string();
            input.update(cx, |input, cx| input.set_text(&text, cx));
        }
        self.security_baseline = self
            .security_payload(cx, Locale::resolve("en"))
            .unwrap_or_default();
    }
}

fn security_settings_payload<'a>(
    draft: &serde_json::Value,
    numbers: impl Iterator<Item = &'a str>,
    locale: Locale,
) -> Result<serde_json::Value, String> {
    let mut payload = json!({
        "enabled": draft.get("enabled").and_then(serde_json::Value::as_bool).ok_or(locale.text("error.securityUnloaded"))?,
        "defaultMode": draft.get("defaultMode").and_then(serde_json::Value::as_str).ok_or(locale.text("error.securityUnloaded"))?,
    });
    if !["standard", "deep"].contains(&payload["defaultMode"].as_str().unwrap_or_default()) {
        return Err(locale.text("error.scanMode").into());
    }
    let numbers = numbers.collect::<Vec<_>>();
    if numbers.len() != SECURITY_NUMBERS.len() {
        return Err(locale.text("error.scanNumbers").into());
    }
    for ((key, min, max), input) in SECURITY_NUMBERS.iter().zip(numbers) {
        let number = input
            .trim()
            .parse::<f64>()
            .map_err(|_| locale.format("error.number", &[("key", key.to_string())]))?;
        if !number.is_finite()
            || number < *min
            || number > *max
            || (*key != "maxTimeHours" && number.fract() != 0.)
        {
            return Err(locale.format(
                "error.numberRange",
                &[
                    ("key", key.to_string()),
                    ("min", min.to_string()),
                    ("max", max.to_string()),
                    (
                        "type",
                        locale
                            .text(if *key == "maxTimeHours" {
                                "error.numeric"
                            } else {
                                "error.integer"
                            })
                            .to_string(),
                    ),
                ],
            ));
        }
        payload[key] = if *key == "maxTimeHours" {
            json!(number)
        } else {
            json!(number as i64)
        };
    }
    Ok(payload)
}

struct AzemWindow {
    focus: FocusHandle,
    state: AppState,
    runtime: RuntimeConnection,
    runtime_options: Rc<RefCell<RuntimeOptions>>,
    runtime_generation: u64,
    pending_workspace_sequence: Option<i64>,
    new_session_after_workspace_switch: bool,
    composer: Entity<TextInput>,
    completion: ComposerCompletion,
    search_input: Entity<TextInput>,
    session_rename_input: Entity<TextInput>,
    renaming_session_id: Option<String>,
    sidebar_context_menu: Option<SidebarContextMenu>,
    search_return_surface: Surface,
    model_search: Entity<TextInput>,
    branch_picker: BranchPicker,
    approval_picker: ApprovalPicker,
    settings_provider_search: Entity<TextInput>,
    settings_model_search: Entity<TextInput>,
    extension_settings: ExtensionSettings,
    native_settings: NativeSettings,
    model_picker_open: bool,
    model_picker_error: String,
    route_picker_target: Option<RoutePickerTarget>,
    subagent_setting_menu: Option<SubagentSettingKind>,
    context_popover_open: bool,
    reply_popover: Option<(String, ReplyPopoverKind)>,
    reply_feedback: HashMap<String, i8>,
    hovered_message: Option<String>,
    reasoning_slider_bounds: Option<Bounds<Pixels>>,
    reasoning_drag: Option<ReasoningDrag>,
    fast_particles: Entity<FastParticles>,
    settings_section: String,
    settings_open: bool,
    settings_provider: Option<String>,
    settings_provider_scroll: UniformListScrollHandle,
    settings_model_scroll: UniformListScrollHandle,
    archive_days: u32,
    archive_days_menu_open: bool,
    usage_hover: Option<(String, i64)>,
    environment_open: bool,
    environment_expanded: Option<String>,
    side_panel_open: bool,
    side_panel_agents_open: bool,
    side_panel_add_menu_open: bool,
    side_panel_closing: bool,
    side_panel_width: f32,
    side_panel_visible_width: f32,
    side_panel_animation_started: Option<Instant>,
    side_panel_animation_from: f32,
    side_panel_resize_drag: Option<SidePanelResizeDrag>,
    terminal_open: bool,
    terminal_input: Entity<TextInput>,
    terminal_scroll: ScrollHandle,
    terminal_emulators: HashMap<String, TerminalEmulator>,
    process_expansion: Rc<RefCell<ProcessExpansion>>,
    transcript_list: ListState,
    queued_prompts: Vec<QueuedPrompt>,
    editing_queued_id: Option<String>,
    pending_requests: HashMap<String, PendingRequest>,
    open_projects: HashSet<String>,
    startup_started: Instant,
    snapshot_ready_logged: bool,
    provider_catalog_logged: bool,
    show_all_sessions: bool,
    window_state_path: Option<PathBuf>,
    window_size: Size<Pixels>,
    _connection_task: Task<()>,
}

impl AzemWindow {
    fn new(
        window: &mut Window,
        cx: &mut Context<Self>,
        runtime: RuntimeConnection,
        runtime_options: Rc<RefCell<RuntimeOptions>>,
        window_state_path: Option<PathBuf>,
    ) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let mut state = AppState::default();
        let locale = Locale::resolve("en");
        let preferences = window_state_path
            .as_ref()
            .map(|path| AppearancePreferences::load(&path.with_file_name("gpui-appearance.json")))
            .transpose();
        let preferences = match preferences {
            Ok(value) => value.unwrap_or_default(),
            Err(error) => {
                state.settings.error = locale
                    .format("error.appearanceLoad", &[("error", error.to_string())])
                    .into();
                AppearancePreferences::default()
            }
        };
        state.settings.appearance = json!(preferences);
        cx.set_global(preferences);
        let composer = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.describeTheTaskReferenceFilesUseSkills"),
            )
            .multiline()
        });
        let search_input = cx
            .new(|cx| TextInput::new(cx, window, locale.text("ui.searchConversationsAndMessages")));
        let session_rename_input =
            cx.new(|cx| TextInput::new(cx, window, locale.text("sidebar.renameSession")).compact());
        let mut previous_composer = (String::new(), Some(0));
        cx.observe(&composer, move |this, input, cx| {
            let input = input.read(cx);
            if input.submission_text() != previous_composer.0
                || input.completion_cursor() != previous_composer.1
            {
                previous_composer = (input.submission_text(), input.completion_cursor());
                this.completion.submission_error.clear();
                this.sync_completions(cx);
                cx.notify();
            }
        })
        .detach();
        let terminal_input =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.terminal")).terminal());
        cx.observe(&terminal_input, |this, input, cx| {
            let data = {
                let input = input.read(cx);
                (!input.has_marked_text() && !input.text().is_empty())
                    .then(|| input.text().to_owned())
            };
            if let Some(data) = data {
                this.write_terminal_data(&data);
                input.update(cx, |input, cx| input.clear(cx));
            }
            cx.notify();
        })
        .detach();
        let model_search = cx.new(|cx| {
            TextInput::new(cx, window, locale.text("ui.searchModelsOrProviders")).compact()
        });
        let branch_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("branch.search")).compact());
        let mut previous_branch_query = String::new();
        cx.observe(&branch_search, move |this, input, cx| {
            let query = input.read(cx).text();
            if query != previous_branch_query {
                previous_branch_query = query.to_owned();
                this.branch_picker.index = 0;
                this.branch_picker.scroll.scroll_to_item(0);
                cx.notify();
            }
        })
        .detach();
        let settings_provider_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("ui.searchProviders")).compact());
        let settings_model_search = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.searchModelFamilyVersionOrRawId"),
            )
            .compact()
        });
        let extension_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("ui.searchExtensions")).compact());
        let marketplace_source = cx.new(|cx| {
            TextInput::new(
                cx,
                window,
                locale.text("ui.marketplaceOwnerRepoGitUrlOrLocalPath"),
            )
            .compact()
        });
        let settings_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.settingsSearch")).compact());
        let font_search =
            cx.new(|cx| TextInput::new(cx, window, locale.text("input.fontSearch")).compact());
        let security_fields = SECURITY_NUMBERS
            .iter()
            .map(|(key, _, _)| cx.new(|cx| TextInput::new(cx, window, *key).compact()))
            .collect::<Vec<_>>();
        for input in [
            &model_search,
            &settings_provider_search,
            &settings_model_search,
            &extension_search,
            &marketplace_source,
            &settings_search,
            &font_search,
        ]
        .into_iter()
        .chain(&security_fields)
        {
            let mut previous = input.read(cx).text().to_owned();
            cx.observe(input, move |_, input, cx| {
                let text = input.read(cx).text();
                if text != previous {
                    previous = text.to_owned();
                    cx.notify();
                }
            })
            .detach();
        }
        let transcript_list = ListState::new(0, ListAlignment::Top, px(800.));
        let messages = runtime.messages.clone();
        let mut this = Self {
            focus,
            state,
            runtime,
            runtime_options,
            runtime_generation: 0,
            pending_workspace_sequence: None,
            new_session_after_workspace_switch: false,
            composer,
            completion: ComposerCompletion::default(),
            search_input,
            session_rename_input,
            renaming_session_id: None,
            sidebar_context_menu: None,
            search_return_surface: Surface::Thread,
            model_search,
            branch_picker: BranchPicker {
                search: branch_search,
                focus: cx.focus_handle(),
                scroll: ScrollHandle::new(),
                open: false,
                index: 0,
                error: String::new(),
                confirm_target: None,
                button_bounds: None,
            },
            approval_picker: ApprovalPicker {
                open: false,
                index: 0,
                focus: cx.focus_handle(),
                button_bounds: None,
                error: String::new(),
            },
            settings_provider_search,
            settings_model_search,
            extension_settings: ExtensionSettings {
                search: extension_search,
                source: marketplace_source,
                scope: "user",
                busy: false,
            },
            native_settings: NativeSettings {
                language_menu_open: false,
                language_index: 0,
                language_saving: false,
                language_focus: cx.focus_handle(),
                language_scroll: gpui::ScrollHandle::new(),
                search: settings_search,
                font_search,
                font_menu_open: false,
                fonts: Vec::new(),
                fonts_loading: false,
                security_fields,
                security_draft: serde_json::Value::Null,
                security_baseline: serde_json::Value::Null,
                security_busy: false,
                security_saved: false,
            },
            model_picker_open: false,
            model_picker_error: String::new(),
            route_picker_target: None,
            subagent_setting_menu: None,
            context_popover_open: false,
            reply_popover: None,
            reply_feedback: HashMap::new(),
            hovered_message: None,
            reasoning_slider_bounds: None,
            reasoning_drag: None,
            fast_particles: cx.new(|_| FastParticles::new()),
            settings_section: "catalog".to_string(),
            settings_open: false,
            settings_provider: None,
            settings_provider_scroll: UniformListScrollHandle::new(),
            settings_model_scroll: UniformListScrollHandle::new(),
            archive_days: 30,
            archive_days_menu_open: false,
            usage_hover: None,
            environment_open: true,
            environment_expanded: None,
            side_panel_open: false,
            side_panel_agents_open: false,
            side_panel_add_menu_open: false,
            side_panel_closing: false,
            side_panel_width: SIDE_PANEL_DEFAULT_WIDTH,
            side_panel_visible_width: 0.,
            side_panel_animation_started: None,
            side_panel_animation_from: 0.,
            side_panel_resize_drag: None,
            terminal_open: false,
            terminal_input,
            terminal_scroll: ScrollHandle::new(),
            terminal_emulators: HashMap::new(),
            queued_prompts: Vec::new(),
            editing_queued_id: None,
            pending_requests: HashMap::new(),
            open_projects: HashSet::new(),
            process_expansion: Rc::new(RefCell::new(ProcessExpansion::default())),
            show_all_sessions: false,
            startup_started: Instant::now(),
            snapshot_ready_logged: false,
            provider_catalog_logged: false,
            window_state_path,
            window_size: window.window_bounds().get_bounds().size,
            _connection_task: Task::ready(()),
            transcript_list,
        };
        this.listen_to_runtime(messages, cx);
        this
    }

    fn listen_to_runtime(
        &mut self,
        messages: async_channel::Receiver<RuntimeMessage>,
        cx: &mut Context<Self>,
    ) {
        let generation = self.runtime_generation;
        for _ in 0..messages.len() {
            if let Ok(message) = messages.try_recv() {
                self.apply_runtime_message(message, cx);
            }
        }
        self._connection_task = cx.spawn(async move |this, cx| {
            while let Ok(message) = messages.recv().await {
                if this
                    .update(cx, |this, cx| {
                        if this.runtime_generation != generation {
                            return;
                        }
                        this.apply_runtime_message(message, cx);
                        cx.notify();
                    })
                    .is_err()
                {
                    return;
                }
            }
        });
    }

    fn switch_workspace(
        &mut self,
        workspace: &str,
        session_id: &str,
        sequence: Option<i64>,
        start_new_session: bool,
        cx: &mut Context<Self>,
    ) {
        self.runtime.detach();
        self.runtime_generation = self.runtime_generation.wrapping_add(1);
        let options = {
            let mut options = self.runtime_options.borrow_mut();
            options.workspace = PathBuf::from(workspace);
            options.session_id = (!session_id.is_empty()).then(|| session_id.to_string());
            options.clone()
        };
        self.runtime = RuntimeConnection::start(options);
        self.pending_workspace_sequence = sequence;
        self.new_session_after_workspace_switch = start_new_session;
        self.pending_requests.clear();
        self.queued_prompts.clear();
        self.terminal_emulators.clear();
        self.sidebar_context_menu = None;
        self.renaming_session_id = None;
        self.state.connection.connected = false;
        self.state.connection.reconnecting = true;
        self.state.connection.message = "Connecting to workspace runtime…".into();
        self.state.navigation.surface = Surface::Thread;
        self.listen_to_runtime(self.runtime.messages.clone(), cx);
        cx.notify();
    }

    fn apply_runtime_message(&mut self, message: RuntimeMessage, cx: &mut Context<Self>) {
        let locale = Locale::resolve(&self.state.settings.language);
        let old_language = self.state.settings.language.clone();
        let old_block_count = self.state.transcript.blocks.borrow().len();
        let old_runtime_busy = runtime_busy(&self.state);
        let old_run_id = self.state.runtime.run_id.clone();
        let old_pending_process = needs_pending_process(
            &self.state.transcript.blocks.borrow(),
            self.state.runtime.running,
        );
        let old_session_id = self.state.navigation.current_session_id.clone();
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
                if self.new_session_after_workspace_switch {
                    self.new_session_after_workspace_switch = false;
                    self.runtime
                        .request(Method::Execute, json!({"kind": "new_session"}));
                } else if let Some(sequence) = self.pending_workspace_sequence.take()
                    && let Some(index) =
                        self.state
                            .transcript
                            .blocks
                            .borrow()
                            .iter()
                            .position(|block| {
                                block
                                    .extra
                                    .get("sequence")
                                    .and_then(serde_json::Value::as_i64)
                                    == Some(sequence)
                            })
                {
                    self.transcript_list.scroll_to_reveal_item(index);
                }
                self.request_environment_metrics(true);
                if !self.snapshot_ready_logged {
                    tracing::info!(
                        interactive_ms = self.startup_started.elapsed().as_millis(),
                        "Azem GPUI state ready"
                    );
                    self.snapshot_ready_logged = true;
                }
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
                let environment_refresh = self.environment_open
                    && envelope.channel == "runtime"
                    && matches!(
                        envelope
                            .payload
                            .get("kind")
                            .and_then(serde_json::Value::as_str),
                        Some("tool_finished" | "run_finished" | "run_failed" | "run_cancelled")
                    );
                let listed_security_scans = envelope.channel == "runtime"
                    && envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        == Some("security_scan_list");
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
                    let sequence = envelope
                        .payload
                        .get("sequence")
                        .and_then(serde_json::Value::as_i64);
                    self.switch_workspace(workspace, session_id, sequence, false, cx);
                    return;
                }
                if envelope.channel == "terminal"
                    && envelope
                        .payload
                        .get("kind")
                        .and_then(serde_json::Value::as_str)
                        == Some("terminal_exit")
                    && let Some(id) = envelope
                        .payload
                        .pointer("/session/id")
                        .and_then(serde_json::Value::as_str)
                {
                    self.terminal_emulators.remove(id);
                }
                self.state.apply_envelope(*envelope);
                if environment_refresh {
                    self.request_environment_metrics(false);
                }
                if listed_security_scans {
                    self.request_security_scan_projection();
                }
            }
            RuntimeMessage::Event(ClientEvent::Binary(metadata, data)) => {
                let is_terminal_output = metadata.purpose == "terminal_output";
                let follows_active_terminal = is_terminal_output
                    && (self.state.terminals.active_id.is_empty()
                        || self.state.terminals.active_id.as_ref() == metadata.transfer_id);
                if is_terminal_output {
                    self.terminal_emulators
                        .entry(metadata.transfer_id.clone())
                        .or_default()
                        .feed(&data);
                }
                if follows_active_terminal {
                    self.terminal_scroll.scroll_to_bottom();
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
                            PendingRequest::CompletionFiles { generation } => {
                                self.completion.receive_files(generation, Ok(value), locale);
                            }
                            PendingRequest::ApprovalMode => {
                                self.approval_picker.open = false;
                                self.approval_picker.error.clear();
                            }
                            PendingRequest::ModelSelection {
                                target,
                                session_id,
                                provider,
                                model,
                                reasoning,
                            } => {
                                tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "ack", request = %id, %reasoning);
                                if let Some(target) = target {
                                    for entry in &mut self.state.catalogs.routes {
                                        if entry["scope"] == target.scope
                                            && entry["role"].as_str().unwrap_or_default()
                                                == target.role
                                        {
                                            entry["route"] = json!({"provider":provider,"model":model,"reasoning":reasoning});
                                            break;
                                        }
                                    }
                                } else if session_id
                                    == self.state.navigation.current_session_id.as_ref()
                                {
                                    self.state.settings.provider = provider.into();
                                    self.state.settings.model = model.into();
                                    self.state.settings.reasoning = reasoning.into();
                                }
                            }
                            PendingRequest::ChatGPTFastMode(enabled) => {
                                self.state.settings.chatgpt_fast_mode = enabled;
                            }
                            PendingRequest::GitBranches { target, .. } => {
                                self.branch_picker.error.clear();
                                self.branch_picker.confirm_target = None;
                                if target.is_some() {
                                    self.branch_picker.open = false;
                                }
                            }
                            PendingRequest::EnvironmentGit => {}
                            PendingRequest::LanguageChange { language } => {
                                self.native_settings.language_saving = false;
                                self.state.settings.language = language.into();
                                self.state.settings.error = "".into();
                            }
                            PendingRequest::SystemFonts { language } => {
                                if language != locale.id() {
                                    return;
                                }
                                self.native_settings.fonts_loading = false;
                                if let Some(fonts) = value.as_array() {
                                    self.native_settings.fonts = fonts.clone();
                                } else {
                                    self.state.settings.error = locale.text("error.fonts").into();
                                }
                            }
                            PendingRequest::SecuritySave { payload } => {
                                self.native_settings.security_busy = false;
                                if let Some(values) = payload.as_object() {
                                    for (key, value) in values {
                                        self.state.security.config[key] = value.clone();
                                    }
                                }
                                self.native_settings.security_baseline = payload;
                                self.native_settings.security_saved = true;
                            }
                            PendingRequest::ExtensionAction { source_text } => {
                                self.extension_settings.busy = false;
                                if source_text.as_deref()
                                    == Some(self.extension_settings.source.read(cx).text())
                                {
                                    self.extension_settings
                                        .source
                                        .update(cx, |input, cx| input.clear(cx));
                                }
                            }
                            PendingRequest::Turn {
                                source_text,
                                selected_skills,
                                attachments,
                                queued_id,
                            } => {
                                if queued_id.is_none()
                                    && self.composer.read(cx).submission_text() == source_text
                                {
                                    self.composer.update(cx, |composer, cx| composer.clear(cx));
                                }
                                if queued_id.is_none()
                                    && self.state.transcript.attachments == attachments
                                {
                                    self.state.transcript.attachments.clear();
                                }
                                if queued_id.is_none()
                                    && self.completion.selected_skills == selected_skills
                                {
                                    self.completion.selected_skills.clear();
                                }
                                if let Some(queued_id) = queued_id {
                                    self.queued_prompts.retain(|item| item.id != queued_id);
                                }
                            }
                            PendingRequest::QueuedGuide {
                                queued_id,
                                prompt,
                                attachments,
                            } => {
                                self.queued_prompts.retain(|item| item.id != queued_id);
                                self.state
                                    .append_optimistic_user(id.as_str(), prompt, attachments);
                            }
                            PendingRequest::ResumeSession { sequence } => {
                                self.state.apply_direct_event(value);
                                if let Some(sequence) = sequence
                                    && let Some(index) =
                                        self.state.transcript.blocks.borrow().iter().position(
                                            |block| {
                                                block
                                                    .extra
                                                    .get("sequence")
                                                    .and_then(serde_json::Value::as_i64)
                                                    == Some(sequence)
                                            },
                                        )
                                {
                                    self.transcript_list.scroll_to_reveal_item(index);
                                }
                            }
                            PendingRequest::ReplyForkTree {
                                session_id,
                                target_id,
                                anchor,
                            } => {
                                if let Some(entry_id) = session_entry_id(&value, anchor) {
                                    let request_id = self.runtime.request(
                                        Method::CreateSessionFork,
                                        json!({
                                            "sessionId": session_id,
                                            "targetId": target_id,
                                            "entryId": entry_id,
                                        }),
                                    );
                                    self.pending_requests.insert(
                                        request_id,
                                        PendingRequest::ReplyFork { target_id },
                                    );
                                } else {
                                    self.completion.submission_error =
                                        locale.text("reply.branchUnavailable").to_string();
                                }
                            }
                            PendingRequest::ReplyFork { target_id } => {
                                let request_id = self.runtime.request(
                                    Method::ResumeSession,
                                    json!({"sessionId": target_id}),
                                );
                                self.pending_requests.insert(
                                    request_id,
                                    PendingRequest::ResumeSession { sequence: None },
                                );
                                self.reply_popover = None;
                                self.state.navigation.surface = Surface::Thread;
                            }
                            PendingRequest::RemoveProject {
                                workspace,
                                next_workspace,
                            } => {
                                self.state
                                    .navigation
                                    .projects
                                    .retain(|project| project.path.as_ref() != workspace);
                                self.open_projects.remove(&workspace);
                                if let Some(next_workspace) = next_workspace {
                                    self.switch_workspace(&next_workspace, "", None, false, cx);
                                }
                            }
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
                                if self.terminal_open && self.state.terminals.sessions.is_empty() {
                                    self.request_create_terminal();
                                }
                            }
                        }
                    }
                }
                Err(error) => {
                    let pending = self.pending_requests.remove(&id);
                    if let Some(PendingRequest::CompletionFiles { generation }) = pending {
                        self.completion
                            .receive_files(generation, Err(error), locale);
                        cx.notify();
                        return;
                    }
                    if matches!(&pending, Some(PendingRequest::ApprovalMode)) {
                        self.approval_picker.error = error.clone();
                    }
                    if matches!(
                        &pending,
                        Some(
                            PendingRequest::ModelSelection { .. }
                                | PendingRequest::ChatGPTFastMode(_)
                        )
                    ) {
                        self.model_picker_error = error.clone();
                        self.completion.submission_error = error.clone();
                    }
                    if matches!(&pending, Some(PendingRequest::LanguageChange { .. })) {
                        self.native_settings.language_saving = false;
                    }
                    if let Some(PendingRequest::SystemFonts { language }) = &pending {
                        if language != locale.id() {
                            return;
                        }
                        self.native_settings.fonts_loading = false;
                    }
                    if matches!(&pending, Some(PendingRequest::SecuritySave { .. })) {
                        self.native_settings.security_busy = false;
                        self.native_settings.security_saved = false;
                    }
                    if matches!(&pending, Some(PendingRequest::ExtensionAction { .. })) {
                        self.extension_settings.busy = false;
                    }
                    if matches!(
                        &pending,
                        Some(PendingRequest::Turn { .. } | PendingRequest::QueuedGuide { .. })
                    ) {
                        self.state.append_request_error(&id, error.clone());
                    }
                    if let Some(queued_id) = match &pending {
                        Some(PendingRequest::Turn {
                            queued_id: Some(queued_id),
                            ..
                        })
                        | Some(PendingRequest::QueuedGuide { queued_id, .. }) => Some(queued_id),
                        _ => None,
                    } && let Some(item) = self
                        .queued_prompts
                        .iter_mut()
                        .find(|item| item.id == *queued_id)
                    {
                        item.failed = true;
                    }
                    if matches!(&pending, Some(PendingRequest::Turn { .. }))
                        && self.state.runtime.run_id.is_empty()
                    {
                        self.state.runtime.running = false;
                        self.state.runtime.activity = "failed".into();
                    }
                    if matches!(pending, Some(PendingRequest::ResumeSession { .. })) {
                        tracing::warn!(request_id = id, %error, "GPUI resume request failed");
                    }
                    if matches!(
                        &pending,
                        Some(
                            PendingRequest::ReplyForkTree { .. } | PendingRequest::ReplyFork { .. }
                        )
                    ) {
                        self.completion.submission_error = error.clone();
                    }
                    if matches!(pending, Some(PendingRequest::Search)) {
                        self.state.navigation.search_error = error.into();
                    } else if matches!(pending, Some(PendingRequest::EnvironmentGit)) {
                        tracing::warn!(request_id = id, %error, "refresh environment metrics");
                    } else if let Some(PendingRequest::GitBranches { target, confirmed }) = pending
                    {
                        self.branch_picker.open = true;
                        if !confirmed && error.contains("workspace has uncommitted changes") {
                            self.branch_picker.confirm_target = target;
                        } else {
                            self.branch_picker.error = error;
                        }
                    } else {
                        tracing::warn!(request_id = id, %error, "GPUI request failed");
                        self.state.settings.error =
                            if matches!(&pending, Some(PendingRequest::ExtensionAction { .. })) {
                                error.into()
                            } else {
                                format!("{id}: {error}").into()
                            };
                    }
                }
            },
        }
        if old_language != self.state.settings.language {
            self.refresh_language(cx);
        }
        self.native_settings
            .sync_security(&self.state.security.config, cx);
        let new_block_count = self.state.transcript.blocks.borrow().len();
        let new_pending_process = needs_pending_process(
            &self.state.transcript.blocks.borrow(),
            self.state.runtime.running,
        );
        let old_item_count = transcript_item_count(old_block_count, old_pending_process);
        let new_item_count = transcript_item_count(new_block_count, new_pending_process);
        if old_item_count != new_item_count {
            self.transcript_list.reset(new_item_count);
        } else if transcript_layout_changed(
            old_item_count,
            new_item_count,
            old_pending_process,
            new_pending_process,
        ) {
            self.transcript_list
                .splice(old_block_count..old_block_count + 1, 1);
        }
        if old_block_count != new_block_count
            && should_follow_transcript(
                old_session_id.as_ref(),
                self.state.navigation.current_session_id.as_ref(),
                old_block_count,
                new_block_count,
            )
        {
            self.follow_transcript_tail(cx);
        }
        let session_changed = old_session_id != self.state.navigation.current_session_id;
        if session_changed {
            self.completion.dismiss();
            if self.editing_queued_id.take().is_some() {
                self.composer.update(cx, |composer, cx| composer.clear(cx));
                self.state.transcript.attachments.clear();
                self.completion.selected_skills.clear();
            }
        }
        self.sync_completions(cx);
        if session_changed && !self.state.navigation.current_session_id.is_empty() {
            self.runtime
                .set_snapshot_session(self.state.navigation.current_session_id.to_string());
        }
        if self.state.runtime.running
            && !self.state.runtime.run_id.is_empty()
            && old_run_id != self.state.runtime.run_id
        {
            self.schedule_run_elapsed_tick(self.state.runtime.run_id.to_string(), cx);
        }
        if should_start_next_queued(old_runtime_busy, session_changed, &self.state) {
            self.start_next_queued(cx);
        }
    }

    fn refresh_transcript_layout(&mut self, index: usize) {
        let block_count = self.state.transcript.blocks.borrow().len();
        let item_count = transcript_item_count(
            block_count,
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        );
        if item_count > 0 && index < item_count {
            self.transcript_list.remeasure_items(index..index + 1);
        }
    }

    fn schedule_run_elapsed_tick(&mut self, run_id: String, cx: &mut Context<Self>) {
        let timer = cx.background_executor().timer(Duration::from_secs(1));
        cx.spawn(async move |this, cx| {
            timer.await;
            let _ = this.update(cx, move |this, cx| {
                if this.state.runtime.running && this.state.runtime.run_id.as_ref() == run_id {
                    cx.notify();
                    this.schedule_run_elapsed_tick(run_id, cx);
                }
            });
        })
        .detach();
    }

    fn send_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.send_current(cx);
    }

    fn submit_message(&mut self, _: &Submit, window: &mut Window, cx: &mut Context<Self>) {
        if self.renaming_session_id.is_some() {
            if !self.session_rename_input.read(cx).is_composing() {
                self.commit_session_rename(window, cx);
            }
            return;
        }
        // Settings inputs must never submit a draft in the conversation behind the modal.
        if self.settings_open {
            return;
        }
        if self.composer.read(cx).is_composing() {
            return;
        }
        if self.completion.is_open() {
            return;
        }
        if self.approval_picker.open || self.approval_picker.focus.is_focused(window) {
            if self.approval_picker.open {
                self.change_approval_mode(APPROVAL_MODES[self.approval_picker.index].0, cx);
            } else {
                self.toggle_approval_picker(window, cx);
            }
            return;
        }
        if self.branch_picker.open {
            self.branch_picker.focus.focus(window, cx);
            self.select_highlighted_branch(cx);
            return;
        }
        if self.terminal_open && self.terminal_input.focus_handle(cx).is_focused(window) {
            self.write_terminal_data("\r");
            return;
        }
        match self.state.navigation.surface {
            Surface::Terminal => self.write_terminal_data("\r"),
            Surface::Search if self.search_input.read(cx).text().trim().is_empty() => {
                self.runtime
                    .request(Method::Execute, json!({"kind": "new_session"}));
                self.state.navigation.surface = Surface::Thread;
                cx.notify();
            }
            Surface::Search => self.search_current(cx),
            _ => self.send_current(cx),
        }
    }

    fn toggle_search(&mut self, _: &ToggleSearch, window: &mut Window, cx: &mut Context<Self>) {
        if self.settings_open {
            self.native_settings
                .search
                .focus_handle(cx)
                .focus(window, cx);
            return;
        }
        if self.state.navigation.surface == Surface::Search {
            self.state.navigation.surface = self.search_return_surface;
        } else {
            self.search_return_surface = self.state.navigation.surface;
            self.state.navigation.surface = Surface::Search;
            self.search_input.focus_handle(cx).focus(window, cx);
        }
        cx.notify();
    }

    fn find_settings(&mut self, _: &FindSettings, window: &mut Window, cx: &mut Context<Self>) {
        if self.settings_open {
            self.native_settings
                .search
                .focus_handle(cx)
                .focus(window, cx);
        }
    }

    fn set_appearance(
        &mut self,
        key: &str,
        value: serde_json::Value,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let locale = Locale::resolve(&self.state.settings.language);
        let result = (|| -> anyhow::Result<AppearancePreferences> {
            let next = AppearancePreferences::current(cx).changed(key, value)?;
            let path = self
                .window_state_path
                .as_ref()
                .ok_or_else(|| anyhow::anyhow!(locale.text("error.appearanceDirectory")))?
                .with_file_name("gpui-appearance.json");
            next.save(&path)?;
            Ok(next)
        })();
        match result {
            Ok(preferences) => {
                self.state.settings.appearance = json!(preferences);
                cx.set_global(preferences);
                self.focus.focus(window, cx);
                self.native_settings.font_menu_open = false;
                self.state.settings.error = "".into();
                // Re-measure the virtual transcript when typography changes.
                self.transcript_list.reset(transcript_item_count(
                    self.state.transcript.blocks.borrow().len(),
                    needs_pending_process(
                        &self.state.transcript.blocks.borrow(),
                        self.state.runtime.running,
                    ),
                ));
                cx.refresh_windows();
            }
            Err(error) => {
                self.state.settings.error = locale
                    .format("error.appearanceSave", &[("error", error.to_string())])
                    .into()
            }
        }
        cx.notify();
    }

    fn change_language(&mut self, language: &str, cx: &mut Context<Self>) {
        self.native_settings.language_menu_open = false;
        if self.native_settings.language_saving
            || !self.state.connection.connected
            || self.state.settings.language.as_ref() == language
            || !localization::available()
                .iter()
                .any(|pack| pack.id == language)
        {
            cx.notify();
            return;
        }
        self.native_settings.language_saving = true;
        self.state.settings.error = "".into();
        let id = self.runtime.request(Method::Execute, json!({
            "kind":"set_language", "target":language, "sessionId":self.state.navigation.current_session_id
        }));
        self.pending_requests.insert(
            id,
            PendingRequest::LanguageChange {
                language: language.to_string(),
            },
        );
        cx.notify();
    }

    fn refresh_language(&mut self, cx: &mut Context<Self>) {
        let locale = Locale::resolve(&self.state.settings.language);
        for (input, key) in [
            (&self.composer, "ui.describeTheTaskReferenceFilesUseSkills"),
            (&self.search_input, "ui.searchConversationsAndMessages"),
            (&self.session_rename_input, "sidebar.renameSession"),
            (&self.model_search, "ui.searchModelsOrProviders"),
            (&self.branch_picker.search, "branch.search"),
            (&self.settings_provider_search, "ui.searchProviders"),
            (
                &self.settings_model_search,
                "ui.searchModelFamilyVersionOrRawId",
            ),
            (&self.extension_settings.search, "ui.searchExtensions"),
            (
                &self.extension_settings.source,
                "ui.marketplaceOwnerRepoGitUrlOrLocalPath",
            ),
            (&self.native_settings.search, "input.settingsSearch"),
            (&self.native_settings.font_search, "input.fontSearch"),
            (&self.terminal_input, "input.terminal"),
        ] {
            input.update(cx, |input, cx| input.set_placeholder(locale.text(key), cx));
        }
        self.native_settings.fonts.clear();
        if self.settings_open && self.settings_section == "appearance" {
            self.request_system_fonts();
        }
        self.transcript_list.reset(transcript_item_count(
            self.state.transcript.blocks.borrow().len(),
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        ));
        cx.notify();
    }

    fn request_system_fonts(&mut self) {
        let language = Locale::resolve(&self.state.settings.language)
            .id()
            .to_string();
        self.native_settings.fonts_loading = true;
        let id = self
            .runtime
            .request(Method::SystemFonts, json!({"language":language}));
        self.pending_requests
            .insert(id, PendingRequest::SystemFonts { language });
    }

    fn select_settings_section(&mut self, section: &str, cx: &mut Context<Self>) {
        self.native_settings.language_menu_open = false;
        self.settings_section = section.to_string();
        self.model_picker_open = false;
        self.route_picker_target = None;
        self.subagent_setting_menu = None;
        self.archive_days_menu_open = false;
        self.native_settings.font_menu_open = false;
        self.native_settings
            .search
            .update(cx, |input, cx| input.clear(cx));
        if section == "catalog" {
            self.settings_provider_search
                .update(cx, |input, cx| input.clear(cx));
            self.settings_model_search
                .update(cx, |input, cx| input.clear(cx));
        }
        if section == "appearance"
            && self.native_settings.fonts.is_empty()
            && !self.native_settings.fonts_loading
        {
            self.request_system_fonts();
        }
        if section == "security" {
            self.runtime.request(Method::Execute, json!({"kind":"get_security_config", "sessionId":self.state.navigation.current_session_id}));
        }
        if section == "usage" {
            let id = self
                .runtime
                .request(Method::UsageReport, json!({"scope":"project"}));
            self.pending_requests.insert(id, PendingRequest::Usage);
        }
        cx.notify();
    }

    fn save_security_settings(&mut self, cx: &mut Context<Self>) {
        if self.native_settings.security_busy || !self.state.connection.connected {
            return;
        }
        let payload = match self
            .native_settings
            .security_payload(cx, Locale::resolve(&self.state.settings.language))
        {
            Ok(payload) => payload,
            Err(error) => {
                self.state.settings.error = error.into();
                cx.notify();
                return;
            }
        };
        self.state.settings.error = "".into();
        self.native_settings.security_busy = true;
        self.native_settings.security_saved = false;
        let id = self.runtime.request(Method::Execute, json!({
            "kind":"set_security_config", "sessionId":self.state.navigation.current_session_id, "payload":payload,
        }));
        self.pending_requests
            .insert(id, PendingRequest::SecuritySave { payload });
        cx.notify();
    }

    fn toggle_settings(&mut self, _: &ToggleSettings, _: &mut Window, cx: &mut Context<Self>) {
        self.completion.dismiss();
        self.approval_picker.open = false;
        self.branch_picker.open = false;
        self.settings_open = !self.settings_open;
        if self.settings_open {
            self.settings_provider_search
                .update(cx, |search, cx| search.clear(cx));
            self.settings_model_search
                .update(cx, |search, cx| search.clear(cx));
        }
        if !self.settings_open {
            self.model_picker_open = false;
            self.route_picker_target = None;
            self.subagent_setting_menu = None;
            self.archive_days_menu_open = false;
        }
        if self.settings_open && self.state.catalogs.providers.is_empty() {
            self.refresh_model_catalog();
        }
        cx.notify();
    }

    fn set_terminal_open(&mut self, open: bool, window: &mut Window, cx: &mut Context<Self>) {
        self.terminal_open = open;
        if open {
            self.request_surface(Surface::Terminal);
            self.terminal_scroll.scroll_to_bottom();
            self.terminal_input.focus_handle(cx).focus(window, cx);
        } else if self.terminal_input.focus_handle(cx).is_focused(window) {
            self.focus.focus(window, cx);
        }
    }

    fn toggle_terminal(&mut self, _: &ToggleTerminal, window: &mut Window, cx: &mut Context<Self>) {
        self.set_terminal_open(!self.terminal_open, window, cx);
        cx.notify();
    }

    fn toggle_terminal_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.set_terminal_open(!self.terminal_open, window, cx);
        cx.notify();
    }

    fn dismiss_picker(&mut self, _: &MouseDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.reasoning_drag = None;
        self.completion.dismiss();
        self.approval_picker.open = false;
        self.branch_picker.open = false;
        self.model_picker_open = false;
        self.route_picker_target = None;
        self.context_popover_open = false;
        self.reply_popover = None;
        self.native_settings.language_menu_open = false;
        self.native_settings.font_menu_open = false;
        self.subagent_setting_menu = None;
        self.archive_days_menu_open = false;
        self.side_panel_add_menu_open = false;
        // Dismiss the popup without also activating a control underneath it.
        cx.stop_propagation();
        cx.notify();
    }

    fn dismiss_sidebar_context_menu(
        &mut self,
        _: &MouseDownEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.sidebar_context_menu = None;
        cx.stop_propagation();
        cx.notify();
    }

    fn run_sidebar_menu_action(
        &mut self,
        action: SidebarMenuAction,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(menu) = self.sidebar_context_menu.take() else {
            return;
        };
        match menu.target {
            SidebarMenuTarget::Session {
                id,
                title,
                workspace,
                pinned,
            } => self.run_session_menu_action(action, (id, title, workspace, pinned), window, cx),
            SidebarMenuTarget::Project { workspace } => {
                self.run_project_menu_action(action, workspace, cx)
            }
        }
        cx.notify();
    }

    fn run_session_menu_action(
        &mut self,
        action: SidebarMenuAction,
        target: (String, String, String, bool),
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (id, title, workspace, pinned) = target;
        match action {
            SidebarMenuAction::ToggleSessionPin => {
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":"pin_session","target":id,"decision":(!pinned).to_string(),"sessionId":id}),
                );
            }
            SidebarMenuAction::RenameSession => {
                self.renaming_session_id = Some(id);
                self.session_rename_input
                    .update(cx, |input, cx| input.set_text(&title, cx));
                self.session_rename_input.focus_handle(cx).focus(window, cx);
            }
            SidebarMenuAction::MarkSessionUnread => {
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":"mark_session_unread","target":id,"sessionId":id}),
                );
            }
            SidebarMenuAction::ArchiveSession => {
                self.runtime.request(
                    Method::Execute,
                    json!({"kind":"archive_session","target":id,"sessionId":id}),
                );
            }
            SidebarMenuAction::CopyWorkspace => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(workspace))
            }
            SidebarMenuAction::CopySessionId => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(id))
            }
            _ => {}
        }
    }

    fn run_project_menu_action(
        &mut self,
        action: SidebarMenuAction,
        workspace: String,
        cx: &mut Context<Self>,
    ) {
        match action {
            SidebarMenuAction::RevealProject => {
                if let Err(error) = reveal_project_path(&workspace) {
                    self.state.settings.error = error.to_string().into();
                }
            }
            SidebarMenuAction::ArchiveProjectSessions => {
                let active = self.state.workspace.root.as_ref() == workspace;
                let session_ids = self
                    .state
                    .navigation
                    .sessions
                    .iter()
                    .filter(|session| {
                        !session.archived
                            && (session.workspace.as_ref() == workspace
                                || (session.workspace.is_empty() && active))
                    })
                    .map(|session| session.id.to_string())
                    .collect::<Vec<_>>();
                for id in session_ids {
                    self.runtime.request(
                        Method::Execute,
                        json!({"kind":"archive_session","target":id,"sessionId":id}),
                    );
                }
            }
            SidebarMenuAction::RemoveProject => {
                let active = self.state.workspace.root.as_ref() == workspace;
                let next_workspace = if active {
                    self.state
                        .navigation
                        .projects
                        .iter()
                        .map(|project| project.path.as_ref())
                        .find(|project| !project.is_empty() && *project != workspace)
                        .map(str::to_string)
                } else {
                    None
                };
                if active && next_workspace.is_none() {
                    self.state.settings.error = Locale::resolve(&self.state.settings.language)
                        .text("sidebar.removeOnlyProject")
                        .into();
                    return;
                }
                let request_id = self.runtime.request(
                    Method::Execute,
                    json!({"kind":"remove_project","target":workspace}),
                );
                self.pending_requests.insert(
                    request_id,
                    PendingRequest::RemoveProject {
                        workspace,
                        next_workspace,
                    },
                );
            }
            SidebarMenuAction::CopyProjectPath => {
                cx.write_to_clipboard(gpui::ClipboardItem::new_string(workspace))
            }
            _ => {}
        }
    }

    fn commit_session_rename(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(session_id) = self.renaming_session_id.clone() else {
            return;
        };
        let name = self.session_rename_input.read(cx).text().trim().to_string();
        if name.is_empty() {
            self.state.settings.error = Locale::resolve(&self.state.settings.language)
                .text("sidebar.renameEmpty")
                .into();
            cx.notify();
            return;
        }
        self.runtime.request(
            Method::Execute,
            json!({"kind":"rename_session","target":session_id,"name":name,"sessionId":session_id}),
        );
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.notify();
    }

    fn cancel_session_rename(
        &mut self,
        _: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.stop_propagation();
        cx.notify();
    }

    fn cancel_session_rename_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.renaming_session_id = None;
        self.session_rename_input
            .update(cx, |input, cx| input.clear(cx));
        self.focus.focus(window, cx);
        cx.notify();
    }

    fn commit_session_rename_click(
        &mut self,
        _: &ClickEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.commit_session_rename(window, cx);
    }

    fn close_overlay(&mut self, _: &CloseOverlay, _: &mut Window, cx: &mut Context<Self>) {
        self.reasoning_drag = None;
        if self.renaming_session_id.is_some() {
            self.renaming_session_id = None;
            self.session_rename_input
                .update(cx, |input, cx| input.clear(cx));
        } else if self.sidebar_context_menu.is_some() {
            self.sidebar_context_menu = None;
        } else if self.completion.is_open() {
            self.completion.dismiss();
        } else if self.approval_picker.open {
            self.approval_picker.open = false;
        } else if self.branch_picker.open {
            self.branch_picker.open = false;
        } else if self.native_settings.language_menu_open {
            self.native_settings.language_menu_open = false;
        } else if self.native_settings.font_menu_open {
            self.native_settings.font_menu_open = false;
        } else if self.settings_open && !self.native_settings.search.read(cx).text().is_empty() {
            self.native_settings
                .search
                .update(cx, |input, cx| input.clear(cx));
        } else if self.subagent_setting_menu.is_some() {
            self.subagent_setting_menu = None;
        } else if self.archive_days_menu_open {
            self.archive_days_menu_open = false;
        } else if self.side_panel_add_menu_open {
            self.side_panel_add_menu_open = false;
        } else if self.model_picker_open && self.route_picker_target.is_some() {
            self.model_picker_open = false;
            self.route_picker_target = None;
        } else if self.settings_open {
            self.settings_open = false;
        } else if self.model_picker_open {
            self.model_picker_open = false;
        } else if self.context_popover_open {
            self.context_popover_open = false;
        } else if self.reply_popover.is_some() {
            self.reply_popover = None;
        } else if self.state.navigation.surface == Surface::Search {
            self.state.navigation.surface = self.search_return_surface;
        }
        cx.notify();
    }

    fn fork_reply(&mut self, anchor: ReplyForkAnchor, cx: &mut Context<Self>) {
        let session_id = self.state.navigation.current_session_id.to_string();
        if session_id.is_empty()
            || self.pending_requests.values().any(|pending| {
                matches!(
                    pending,
                    PendingRequest::ReplyForkTree { .. } | PendingRequest::ReplyFork { .. }
                )
            })
        {
            return;
        }
        let target_id = format!("session-{}", uuid::Uuid::new_v4());
        let request_id = self
            .runtime
            .request(Method::SessionTree, json!({"sessionId": session_id}));
        self.pending_requests.insert(
            request_id,
            PendingRequest::ReplyForkTree {
                session_id,
                target_id,
                anchor,
            },
        );
        self.completion.submission_error.clear();
        self.reply_popover = None;
        cx.notify();
    }

    fn send_current(&mut self, cx: &mut Context<Self>) {
        if self.branch_request_pending() || self.approval_request_pending() {
            return;
        }
        let source_text = self.composer.read(cx).submission_text();
        let prompt = source_text.trim().to_string();
        let selected_skills = self.completion.selected_skills.clone();
        let prepared_prompt = match prepare_prompt(
            &prompt,
            &selected_skills,
            &self.state.catalogs.skills,
            Locale::resolve(&self.state.settings.language),
        ) {
            Ok((prompt, _)) => prompt,
            Err(error) => {
                self.completion.submission_error = error;
                cx.notify();
                return;
            }
        };
        let attachments = self.state.transcript.attachments.clone();
        if !composer_has_submission(&prepared_prompt, &attachments) {
            return;
        }
        if let Some(id) = self.editing_queued_id.clone() {
            if let Some(item) = self.queued_prompts.iter_mut().find(|item| {
                item.id == id
                    && item.session_id == self.state.navigation.current_session_id.as_ref()
            }) {
                item.prompt = prompt;
                item.selected_skills = selected_skills;
                item.attachments = attachments;
                item.failed = false;
                self.editing_queued_id = None;
                self.composer.update(cx, |composer, cx| composer.clear(cx));
                self.state.transcript.attachments.clear();
                self.completion.selected_skills.clear();
                self.start_next_queued(cx);
                cx.notify();
                return;
            }
            self.editing_queued_id = None;
        }
        if !self.state.connection.connected {
            return;
        }
        if runtime_busy(&self.state) {
            self.queued_prompts.push(QueuedPrompt {
                id: uuid::Uuid::new_v4().to_string(),
                session_id: self.state.navigation.current_session_id.to_string(),
                prompt,
                selected_skills,
                attachments: attachments.clone(),
                failed: false,
            });
            self.composer.update(cx, |composer, cx| composer.clear(cx));
            self.state.transcript.attachments.clear();
            self.completion.selected_skills.clear();
            self.scroll_transcript_to_bottom(cx);
            cx.notify();
            return;
        }
        self.start_turn(prompt, selected_skills, attachments, None, cx);
    }

    fn start_turn(
        &mut self,
        prompt: String,
        selected_skills: Vec<String>,
        attachments: Vec<serde_json::Value>,
        queued_id: Option<String>,
        cx: &mut Context<Self>,
    ) {
        let payload =
            match turn_payload(&self.state, &prompt, &selected_skills, attachments.clone()) {
                Ok(prepared) => prepared,
                Err(error) => {
                    self.completion.submission_error = error;
                    if let Some(item) = self
                        .queued_prompts
                        .iter_mut()
                        .find(|item| Some(&item.id) == queued_id.as_ref())
                    {
                        item.failed = true;
                    }
                    cx.notify();
                    return;
                }
            };
        let prompt = payload["prompt"].as_str().unwrap_or_default().to_owned();
        let request_id = self.runtime.request(Method::StartTurn, payload);
        self.state
            .append_optimistic_user(request_id.as_str(), prompt, attachments.clone());
        self.state.runtime.running = true;
        self.state.runtime.activity = "starting".into();
        self.scroll_transcript_to_bottom(cx);
        self.pending_requests.insert(
            request_id,
            PendingRequest::Turn {
                source_text: self.composer.read(cx).submission_text(),
                selected_skills,
                attachments,
                queued_id,
            },
        );
        cx.notify();
    }

    fn scroll_transcript_to_bottom(&mut self, cx: &mut Context<Self>) {
        let block_count = self.state.transcript.blocks.borrow().len();
        let item_count = transcript_item_count(
            block_count,
            needs_pending_process(
                &self.state.transcript.blocks.borrow(),
                self.state.runtime.running,
            ),
        );
        let old_item_count = self.transcript_list.item_count();
        let changed_from = old_item_count.saturating_sub(1);
        self.transcript_list.splice(
            changed_from..old_item_count,
            item_count.saturating_sub(changed_from),
        );
        self.follow_transcript_tail(cx);
    }

    fn follow_transcript_tail(&mut self, cx: &mut Context<Self>) {
        self.transcript_list.set_follow_mode(FollowMode::Tail);
        let session_id = self.state.navigation.current_session_id.clone();
        let timer = cx.background_executor().timer(Duration::from_millis(50));
        cx.spawn(async move |this, cx| {
            timer.await;
            let _ = this.update(cx, |this, cx| {
                if this.state.navigation.current_session_id == session_id {
                    if let Some(last) = this.transcript_list.item_count().checked_sub(1) {
                        this.transcript_list.scroll_to_reveal_item(last);
                    }
                    this.transcript_list.set_follow_mode(FollowMode::Tail);
                    cx.notify();
                }
            });
        })
        .detach();
    }

    fn start_next_queued(&mut self, cx: &mut Context<Self>) {
        if runtime_busy(&self.state) || self.editing_queued_id.is_some() {
            return;
        }
        let session_id = self.state.navigation.current_session_id.as_ref();
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| item.session_id == session_id)
            .cloned()
        else {
            return;
        };
        if item.failed {
            return;
        }
        self.start_turn(
            item.prompt,
            item.selected_skills,
            item.attachments,
            Some(item.id),
            cx,
        );
    }

    fn delete_queued(&mut self, id: &str, cx: &mut Context<Self>) {
        if self.editing_queued_id.as_deref() == Some(id) {
            self.editing_queued_id = None;
            self.composer.update(cx, |composer, cx| composer.clear(cx));
            self.state.transcript.attachments.clear();
            self.completion.selected_skills.clear();
        }
        self.queued_prompts.retain(|item| item.id != id);
        self.start_next_queued(cx);
        cx.notify();
    }

    fn edit_queued(&mut self, id: &str, window: &mut Window, cx: &mut Context<Self>) {
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| {
                item.id == id
                    && item.session_id == self.state.navigation.current_session_id.as_ref()
            })
            .cloned()
        else {
            return;
        };
        self.editing_queued_id = Some(item.id);
        self.composer
            .update(cx, |composer, cx| composer.set_text(&item.prompt, cx));
        self.completion.selected_skills = item.selected_skills;
        self.state.transcript.attachments = item.attachments;
        self.composer.focus_handle(cx).focus(window, cx);
        cx.notify();
    }

    fn reorder_queued(
        &mut self,
        dragged: &QueuedPromptDrag,
        target_id: &str,
        cx: &mut Context<Self>,
    ) {
        if dragged.session_id == self.state.navigation.current_session_id.as_ref()
            && reorder_session_queue(
                &mut self.queued_prompts,
                &dragged.session_id,
                &dragged.id,
                target_id,
            )
        {
            cx.notify();
        }
    }

    fn guide_queued(&mut self, id: &str, cx: &mut Context<Self>) {
        if !self.state.runtime.running {
            return;
        }
        let Some(item) = self
            .queued_prompts
            .iter()
            .find(|item| item.id == id)
            .cloned()
        else {
            return;
        };
        if !item.selected_skills.is_empty() || !self.can_guide_prompt(&item.prompt) {
            self.completion.submission_error = Locale::resolve(&self.state.settings.language)
                .text("completion.skillNextTurn")
                .into();
            cx.notify();
            return;
        }
        let request_id = self.runtime.request(
            Method::Guide,
            json!({
                "sessionId": item.session_id,
                "runId": self.state.runtime.run_id,
                "text": item.prompt,
                "attachments": item.attachments,
            }),
        );
        self.pending_requests.insert(
            request_id,
            PendingRequest::QueuedGuide {
                queued_id: item.id,
                prompt: item.prompt,
                attachments: item.attachments,
            },
        );
        cx.notify();
    }

    fn guide_message(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let text = self.composer.read(cx).submission_text().trim().to_string();
        if !self.state.runtime.running {
            return;
        }
        if !self.completion.selected_skills.is_empty() || !self.can_guide_prompt(&text) {
            self.completion.submission_error = Locale::resolve(&self.state.settings.language)
                .text("completion.skillNextTurn")
                .into();
            cx.notify();
            return;
        }
        if text.is_empty() {
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

    fn can_guide_prompt(&self, text: &str) -> bool {
        prepare_prompt(
            text,
            &[],
            &self.state.catalogs.skills,
            Locale::resolve(&self.state.settings.language),
        )
        .is_ok_and(|(_, skills)| skills.is_empty())
    }

    fn attach_file(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let locale = Locale::resolve(&self.state.settings.language);
        let runtime = self.runtime.clone();
        let session_id = self.state.navigation.current_session_id.to_string();
        cx.spawn(async move |this, cx| {
            if let Some(file) = rfd::AsyncFileDialog::new()
                .add_filter(
                    locale.text("files.images"),
                    &["png", "jpg", "jpeg", "gif", "webp"],
                )
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

    fn remove_attachment(&mut self, index: usize, cx: &mut Context<Self>) {
        if index < self.state.transcript.attachments.len() {
            self.state.transcript.attachments.remove(index);
            cx.notify();
        }
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

    fn approval_request_pending(&self) -> bool {
        self.pending_requests
            .values()
            .any(|request| matches!(request, PendingRequest::ApprovalMode))
    }

    fn toggle_approval_picker(&mut self, window: &mut Window, cx: &mut Context<Self>) {
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

    fn change_approval_mode(&mut self, target: &str, cx: &mut Context<Self>) {
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

    fn approval_picker_key(
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

    fn toggle_plan(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        if !self.state.runtime.running {
            self.state.runtime.plan_mode = !self.state.runtime.plan_mode;
            cx.notify();
        }
    }

    fn model_picker_selection(&self) -> (String, String, String) {
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

    fn apply_model_picker_selection(
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

    fn model_controls_enabled(&self) -> bool {
        self.state.connection.connected
            && !runtime_busy(&self.state)
            && !self.pending_requests.values().any(|request| {
                matches!(
                    request,
                    PendingRequest::ModelSelection { .. } | PendingRequest::ChatGPTFastMode(_)
                )
            })
    }

    fn selected_model_modes(&self) -> ModelModes {
        let (provider, model, reasoning) = self.model_picker_selection();
        model_modes(
            &self.state.catalogs.providers,
            &provider,
            &model,
            &reasoning,
            self.state.settings.chatgpt_fast_mode,
        )
    }

    fn set_reasoning(&mut self, next: String, cx: &mut Context<Self>) {
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

    fn toggle_fast_mode(&mut self, cx: &mut Context<Self>) {
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

    fn update_reasoning_from_pointer(&mut self, position_x: Pixels, cx: &mut Context<Self>) {
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

    fn reasoning_drag_is_current(&self, drag: &ReasoningDrag) -> bool {
        self.model_picker_open
            && self.model_controls_enabled()
            && drag.session_id == self.state.navigation.current_session_id.as_ref()
            && drag.target == self.route_picker_target
            && drag.selection == self.model_picker_selection()
    }

    fn reasoning_mouse_down(
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

    fn reasoning_mouse_move(
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

    fn reasoning_mouse_up(&mut self, event: &MouseUpEvent, _: &mut Window, cx: &mut Context<Self>) {
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

    fn selected_model_reasoning_levels(&self) -> Vec<String> {
        self.selected_model_modes().levels
    }

    fn selected_model_context_window(&self) -> i64 {
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

    fn toggle_context_popover(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        self.branch_picker.open = false;
        self.context_popover_open = !self.context_popover_open;
        if self.context_popover_open {
            self.model_picker_open = false;
            self.route_picker_target = None;
        }
        cx.notify();
    }

    fn branch_request_pending(&self) -> bool {
        self.pending_requests.values().any(|request| {
            matches!(
                request,
                PendingRequest::GitBranches { .. } | PendingRequest::EnvironmentGit
            )
        })
    }

    fn request_environment_metrics(&mut self, include_pull_requests: bool) {
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

    fn toggle_environment_panel(&mut self, cx: &mut Context<Self>) {
        self.environment_open = !self.environment_open;
        if self.environment_open {
            self.request_environment_metrics(true);
        }
        cx.notify();
    }

    fn resize_side_panel(
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

    fn hide_side_panel(&mut self) {
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

    fn reconcile_side_panel_layout(&mut self, window: &mut Window) {
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

    fn animate_side_panel(&mut self, closing: bool, _: &mut Window, cx: &mut Context<Self>) {
        self.side_panel_animation_from = self.side_panel_visible_width;
        self.side_panel_animation_started = Some(Instant::now());
        self.side_panel_closing = closing;
        cx.notify();
    }

    fn advance_side_panel_animation(&mut self, window: &mut Window) {
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

    fn toggle_side_panel(&mut self, window: &mut Window, cx: &mut Context<Self>) {
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

    fn inspect_agent(&mut self, target: String, window: &mut Window, cx: &mut Context<Self>) {
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

    fn open_agent_roster(&mut self, window: &mut Window, cx: &mut Context<Self>) {
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

    fn side_panel_resize_mouse_down(
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

    fn side_panel_resize_mouse_move(
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

    fn side_panel_resize_mouse_up(
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

    fn toggle_branch_picker(&mut self, window: &mut Window, cx: &mut Context<Self>) {
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

    fn select_highlighted_branch(&mut self, cx: &mut Context<Self>) {
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

    fn switch_branch(&mut self, target: String, confirmed: bool, cx: &mut Context<Self>) {
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

    fn branch_picker_key(
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

    fn toggle_model_picker(&mut self, _: &ClickEvent, window: &mut Window, cx: &mut Context<Self>) {
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

    fn open_route_picker(
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

    fn refresh_model_catalog(&self) {
        self.runtime.request(
            Method::Execute,
            json!({
                "kind": "list_model_providers",
                "sessionId": self.state.navigation.current_session_id,
            }),
        );
    }

    fn set_subagent_setting(
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

    fn model_picker_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        if self
            .reasoning_drag
            .as_ref()
            .is_some_and(|drag| !self.reasoning_drag_is_current(drag))
        {
            self.reasoning_drag = None;
        }
        let query = self
            .model_search
            .read(cx)
            .text()
            .trim()
            .to_ascii_lowercase();
        let route_picker_kind = self.route_picker_target.as_ref().map(|target| target.kind);
        let (selected_provider, selected_model, current_reasoning) = self.model_picker_selection();
        let modes = self.selected_model_modes();
        let fast_supported = modes.fast_available || modes.fast;
        let (fast_icon_name, fast_icon_color) = if modes.fast {
            ("lightning-filled", palette.accent)
        } else {
            ("lightning", palette.ink)
        };
        // Keep the released thumb at its target while the existing request owns persistence.
        let selected_reasoning = self
            .pending_requests
            .values()
            .find_map(|request| match request {
                PendingRequest::ModelSelection {
                    target,
                    session_id,
                    reasoning,
                    ..
                } if target == &self.route_picker_target
                    && session_id == self.state.navigation.current_session_id.as_ref() =>
                {
                    Some(reasoning.clone())
                }
                _ => None,
            })
            .unwrap_or_else(|| modes.reasoning.clone());
        let editable = self.model_controls_enabled();
        let locale = Locale::resolve(&self.state.settings.language);
        let mut rows = Vec::new();
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
            for choice in model_choices(
                &provider,
                if provider_id == selected_provider {
                    &selected_model
                } else {
                    ""
                },
                &current_reasoning,
            ) {
                let model = choice.model;
                let model_id = model
                    .get("id")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                if model_id.is_empty() {
                    continue;
                }
                let model_name = choice
                    .family_name
                    .unwrap_or_else(|| catalog_model_name(model, &model_id));
                let aliases = choice.aliases;
                let searchable =
                    format!("{} {} {} {}", provider_id, provider_name, model_id, aliases)
                        .to_ascii_lowercase();
                if !query.is_empty()
                    && !searchable.contains(&query)
                    && !model_name.to_ascii_lowercase().contains(&query)
                {
                    continue;
                }
                let mut metadata = if route_picker_kind.is_some() {
                    provider_picker_label(&provider_id, &provider_name)
                } else {
                    format!(
                        "{} · {}",
                        provider_picker_label(&provider_id, &provider_name),
                        model_capability_hint(model, &model_id, &model_name, locale)
                    )
                };
                if choice.variant_count > 0 {
                    metadata.push_str(" · ");
                    metadata.push_str(&locale.format(
                        "model.variantCount",
                        &[("count", choice.variant_count.to_string())],
                    ));
                }
                let active = provider_id == selected_provider && choice.selected;
                let selected_provider_id = provider_id.clone();
                let selected_model_id = model_id.clone();
                let selected_reasoning = choice.reasoning;
                let row_id = format!("model-option-{provider_id}-{model_id}");
                let model_aria = model_name.clone();
                rows.push(
                    div()
                        .id(row_id)
                        .role(Role::ListItem)
                        .aria_label(model_aria)
                        .aria_selected(active)
                        .tab_stop(editable)
                        .min_h(px(47.))
                        .px_2()
                        .py(px(5.))
                        .rounded(px(9.))
                        .bg(if active {
                            if route_picker_kind.is_some() {
                                palette.paper_muted
                            } else {
                                palette.accent_soft
                            }
                        } else {
                            palette.paper
                        })
                        .flex()
                        .items_center()
                        .gap_2()
                        .when(editable, |row| {
                            row.cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.apply_model_picker_selection(
                                        selected_provider_id.clone(),
                                        selected_model_id.clone(),
                                        selected_reasoning.clone(),
                                        true,
                                        cx,
                                    );
                                }))
                        })
                        .child(
                            div()
                                .w(px(27.))
                                .flex()
                                .items_center()
                                .justify_center()
                                .child(provider_logo(&provider_id, 20., palette.ink)),
                        )
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
                                        .text_size(px(11.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(model_name),
                                )
                                .child(
                                    div()
                                        .truncate()
                                        .text_color(palette.faint)
                                        .text_size(px(9.))
                                        .child(metadata),
                                )
                                .when(choice.no_zdr, |detail| {
                                    detail.child(
                                        div()
                                            .text_size(px(9.))
                                            .text_color(palette.danger)
                                            .child(locale.text("model.dataRetention")),
                                    )
                                }),
                        )
                        .when(active, |row| {
                            row.child(icon(
                                "check",
                                15.,
                                if route_picker_kind.is_some() {
                                    palette.ink
                                } else {
                                    palette.accent
                                },
                            ))
                        })
                        .into_any_element(),
                );
            }
        }
        let no_results = rows.is_empty();
        let catalog_loading = self.state.catalogs.providers.is_empty();
        let no_results_label = if catalog_loading {
            locale.text("ui.loadingModelCatalog")
        } else {
            locale.text("ui.noAvailableModelsMatch")
        };
        if route_picker_kind == Some(RoutePickerKind::Model) {
            return div()
                .id("route-model-picker")
                .occlude()
                .on_mouse_down_out(cx.listener(Self::dismiss_picker))
                .role(Role::Region)
                .aria_label(locale.text("ui.selectModel"))
                .absolute()
                .top(px(44.))
                .right(px(94.))
                .w(px(292.))
                .max_h(px(310.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.15, 0.14))
                        .blur_radius(px(22.)),
                ])
                .overflow_hidden()
                .flex()
                .flex_col()
                .child(
                    div()
                        .h(px(34.))
                        .px(px(11.))
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 15., palette.faint))
                        .child(div().flex_1().h_full().child(self.model_search.clone())),
                )
                .child(
                    div()
                        .id("route-model-picker-list")
                        .role(Role::ListBox)
                        .aria_label(locale.text("ui.models3"))
                        .max_h(px(260.))
                        .p(px(5.))
                        .overflow_y_scroll()
                        .flex()
                        .flex_col()
                        .gap(px(2.))
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
                        .children(rows),
                )
                .into_any_element();
        }
        let reasoning_levels = &modes.levels;
        let reasoning_editable = editable && reasoning_levels.len() > 1;
        if route_picker_kind == Some(RoutePickerKind::Reasoning) {
            let mut options = Vec::with_capacity(reasoning_levels.len());
            for (index, level) in reasoning_levels.iter().enumerate() {
                let target = level.clone();
                let selected = target == selected_reasoning;
                options.push(
                    div()
                        .id(("route-reasoning-option", index))
                        .role(if reasoning_editable {
                            Role::RadioButton
                        } else {
                            Role::Label
                        })
                        .aria_label(reasoning_display_name(level, locale))
                        .aria_selected(selected)
                        .tab_stop(reasoning_editable)
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .bg(if selected {
                            palette.paper_muted
                        } else {
                            palette.paper
                        })
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .flex()
                        .items_center()
                        .when(reasoning_levels.len() == 1, |option| {
                            option.aria_description(locale.text("model.fixedReasoning"))
                        })
                        .when(reasoning_editable, |option| {
                            option
                                .cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.set_reasoning(target.clone(), cx);
                                    this.model_picker_open = false;
                                    this.route_picker_target = None;
                                    cx.notify();
                                }))
                        })
                        .child(reasoning_display_name(level, locale))
                        .child(div().flex_1())
                        .when(selected, |option| {
                            option.child(icon("check", 14., palette.ink))
                        })
                        .into_any_element(),
                );
            }
            return div()
                .id("route-reasoning-picker")
                .occlude()
                .on_mouse_down_out(cx.listener(Self::dismiss_picker))
                .role(Role::RadioGroup)
                .aria_label(locale.text("ui.reasoningEffort"))
                .absolute()
                .top(px(44.))
                .right_0()
                .w(px(148.))
                .p(px(5.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.15, 0.14))
                        .blur_radius(px(22.)),
                ])
                .flex()
                .flex_col()
                .gap(px(2.))
                .children(options)
                .into_any_element();
        }
        let mut selected_reasoning_index = reasoning_levels
            .iter()
            .position(|level| level == &selected_reasoning)
            .unwrap_or_default();
        let last_reasoning_index = reasoning_levels.len().saturating_sub(1);
        let reasoning_track_width = self
            .reasoning_slider_bounds
            .map(|bounds| f32::from(bounds.size.width))
            .filter(|width| *width > REASONING_THUMB_INSET * 2.)
            .unwrap_or(MODEL_PICKER_WIDTH - 32.);
        let selected_reasoning_offset = if let Some(drag) = &self.reasoning_drag {
            let offset = reasoning_offset_from_progress(reasoning_track_width, drag.progress);
            selected_reasoning_index = reasoning_index_from_position(
                offset,
                0.,
                reasoning_track_width,
                reasoning_levels.len(),
            );
            offset
        } else {
            reasoning_stop_offset(
                reasoning_track_width,
                selected_reasoning_index,
                reasoning_levels.len(),
            )
        };
        if self.model_picker_open {
            tracing::trace!(target: "azem_gpui::reasoning_slider", phase = "render", offset = selected_reasoning_offset, index = selected_reasoning_index, editable = reasoning_editable, transcript_item = self.transcript_list.logical_scroll_top().item_ix, transcript_offset = f32::from(self.transcript_list.logical_scroll_top().offset_in_item));
        }
        let reasoning_fill_width = (selected_reasoning_offset - REASONING_RAIL_INSET)
            .clamp(0., reasoning_track_width - REASONING_RAIL_INSET * 2.);
        let reasoning_ticks = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, _)| {
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len())
                        - REASONING_RAIL_INSET;
                div()
                    .absolute()
                    .left(px(offset - REASONING_TICK_SIZE / 2.))
                    .top(px((REASONING_RAIL_HEIGHT - REASONING_TICK_SIZE) / 2.))
                    .size(px(REASONING_TICK_SIZE))
                    .rounded_full()
                    .bg(rgba(0xffffff66))
            })
            .collect::<Vec<_>>();
        let reasoning_controls = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, level)| {
                let target = level.clone();
                let selected = index == selected_reasoning_index;
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len());
                let previous = reasoning_stop_offset(
                    reasoning_track_width,
                    index.saturating_sub(1),
                    reasoning_levels.len(),
                );
                let next = reasoning_stop_offset(
                    reasoning_track_width,
                    (index + 1).min(last_reasoning_index),
                    reasoning_levels.len(),
                );
                let left = if index == 0 {
                    0.
                } else {
                    (previous + offset) / 2.
                };
                let right = if index == last_reasoning_index {
                    reasoning_track_width
                } else {
                    (offset + next) / 2.
                };
                div()
                    .id(("reasoning-level", index))
                    .role(if reasoning_editable {
                        Role::RadioButton
                    } else {
                        Role::Label
                    })
                    .aria_label(reasoning_display_name(level, locale))
                    .aria_selected(selected)
                    .tab_stop(reasoning_editable)
                    .absolute()
                    .left(px(left))
                    .top_0()
                    .w(px(right - left))
                    .h(px(REASONING_TRACK_HEIGHT))
                    .when(reasoning_levels.len() == 1, |control| {
                        control.aria_description(locale.text("model.fixedReasoning"))
                    })
                    .when(reasoning_editable, |control| {
                        control.cursor_pointer().on_click(cx.listener(
                            move |this, event: &ClickEvent, _, cx| {
                                // Mouse selection is committed once by the captured release.
                                if !matches!(event, ClickEvent::Mouse(_)) {
                                    this.set_reasoning(target.clone(), cx);
                                }
                            },
                        ))
                    })
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        let reasoning_labels = reasoning_levels
            .iter()
            .enumerate()
            .map(|(index, level)| {
                let offset =
                    reasoning_stop_offset(reasoning_track_width, index, reasoning_levels.len());
                div()
                    .absolute()
                    .left(px(offset - 18.))
                    .top_0()
                    .w(px(36.))
                    .text_size(px(9.))
                    .text_color(if index == selected_reasoning_index {
                        rgb(0x319aff)
                    } else {
                        palette.faint
                    })
                    .font_weight(if index == selected_reasoning_index {
                        gpui::FontWeight::BOLD
                    } else {
                        gpui::FontWeight::NORMAL
                    })
                    .text_center()
                    .whitespace_nowrap()
                    .child(reasoning_display_name(level, locale))
            })
            .collect::<Vec<_>>();
        let slider_owner = cx.entity();
        let slider_mouse_move = cx.listener(Self::reasoning_mouse_move);
        let slider_mouse_up = cx.listener(Self::reasoning_mouse_up);
        div()
            .id("model-picker")
            .occlude()
            .on_mouse_down_out(cx.listener(Self::dismiss_picker))
            .role(Role::Region)
            .aria_label(locale.text("ui.modelAndReasoning"))
            .absolute()
            .right(px(44.))
            .bottom(px(48.))
            .w(px(MODEL_PICKER_WIDTH))
            .max_h(px(560.))
            .rounded(px(13.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.15, 0.15, 0.15))
                    .blur_radius(px(30.)),
            ])
            .p(px(6.))
            .flex()
            .flex_col()
            .child(
                div()
                    .px_2()
                    .pt(px(7.))
                    .pb(px(6.))
                    .flex()
                    .items_center()
                    .child(
                        div()
                            .text_size(px(11.))
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .child(locale.text("ui.modelAndReasoning")),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_size(px(9.))
                            .text_color(palette.faint)
                            .child(locale.text("ui.appliesToThisConversation")),
                    ),
            )
            .child(
                div()
                    .h(px(38.))
                    .mx(px(7.))
                    .mb(px(5.))
                    .border_b_1()
                    .border_color(palette.border)
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(icon("search", 15., palette.faint))
                    .child(div().flex_1().h_full().child(self.model_search.clone()))
                    .child(
                        div()
                            .text_size(px(8.))
                            .text_color(palette.faint)
                            .child("⌘F"),
                    ),
            )
            .child(
                div()
                    .id("model-picker-list")
                    .role(Role::ListBox)
                    .aria_label(locale.text("ui.models3"))
                    .max_h(px(226.))
                    .overflow_y_scroll()
                    .flex()
                    .flex_col()
                    .gap(px(2.))
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
                    .children(rows),
            )
            .when(
                !reasoning_levels.is_empty() || modes.fast_available || modes.fast,
                |picker| {
                    picker.child(
                        div()
                            .mt(px(5.))
                            .px(px(9.))
                            .pt(px(11.))
                            .pb(px(9.))
                            .border_t_1()
                            .border_color(palette.border)
                            .flex()
                            .flex_col()
                            .child(
                                div()
                                    .id("model-reasoning-heading")
                                    .min_h(px(32.))
                                    .pr(px(24.))
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .child(div().min_w_0().flex_1().flex().flex_col().when(
                                        !reasoning_levels.is_empty(),
                                        |heading| {
                                            heading
                                                .child(
                                                    div()
                                                        .text_size(px(11.))
                                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                                        .child(locale.text("ui.reasoningEffort")),
                                                )
                                                .child(
                                                    div()
                                                        .mt(px(1.))
                                                        .text_size(px(9.))
                                                        .text_color(palette.faint)
                                                        .child(locale.text(
                                                            "ui.higherIsDeeperAndTakesLonger",
                                                        )),
                                                )
                                        },
                                    ))
                                    .when(fast_supported, |heading| {
                                        let fast_editable = editable && modes.fast_available;
                                        let tooltip_label = locale
                                            .text(if modes.fast_available {
                                                "model.fastDescription"
                                            } else {
                                                "model.fastUnavailable"
                                            })
                                            .to_string();
                                        heading.child(
                                            div()
                                                .id("model-fast-toggle")
                                                .role(Role::Switch)
                                                .aria_label(locale.text("model.fastDescription"))
                                                .aria_description(tooltip_label.clone())
                                                .aria_toggled(modes.fast.into())
                                                .tab_stop(fast_editable)
                                                .size(px(32.))
                                                .flex_shrink_0()
                                                .rounded(px(7.))
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .tooltip(move |_, cx| {
                                                    cx.new(|_| ModelCapabilityTooltip {
                                                        label: tooltip_label.clone(),
                                                        palette,
                                                    })
                                                    .into()
                                                })
                                                .when(fast_editable, |toggle| {
                                                    toggle
                                                        .cursor_pointer()
                                                        .hover(move |style| style.bg(palette.hover))
                                                        .active(move |style| {
                                                            style.bg(palette.paper_muted)
                                                        })
                                                        .on_click(cx.listener(|this, _, _, cx| {
                                                            this.toggle_fast_mode(cx)
                                                        }))
                                                })
                                                .child(icon(fast_icon_name, 18., fast_icon_color)),
                                        )
                                    }),
                            )
                            .when(!reasoning_levels.is_empty(), |section| {
                                section
                                    .child(
                                        div()
                                            .mt(px(9.))
                                            .h(px(REASONING_TRACK_HEIGHT))
                                            .relative()
                                            .when(reasoning_editable, |slider| {
                                                slider
                                                    .cursor_pointer()
                                                    .on_mouse_down(
                                                        MouseButton::Left,
                                                        cx.listener(Self::reasoning_mouse_down),
                                                    )
                                            })
                                            .on_children_prepainted(move |bounds, _, cx| {
                                                let Some(bounds) = bounds.first().copied() else {
                                                    return;
                                                };
                                                slider_owner.update(cx, |this, cx| {
                                                    if this.reasoning_slider_bounds != Some(bounds)
                                                    {
                                                        this.reasoning_slider_bounds = Some(bounds);
                                                        cx.notify();
                                                    }
                                                });
                                            })
                                            .child(
                                                gpui::canvas(
                                                    |_, _, _| (),
                                                    move |_, _, window, _| {
                                                        // Capture the gesture, including moves and release outside the rail.
                                                        window.on_mouse_event(move |event: &MouseMoveEvent, phase, window, cx| {
                                                            if phase == gpui::DispatchPhase::Capture {
                                                                slider_mouse_move(event, window, cx);
                                                            }
                                                        });
                                                        window.on_mouse_event(move |event: &MouseUpEvent, phase, window, cx| {
                                                            if phase == gpui::DispatchPhase::Capture {
                                                                slider_mouse_up(event, window, cx);
                                                            }
                                                        });
                                                    },
                                                )
                                                .absolute()
                                                .inset_0(),
                                            )
                                            .child(
                                                div()
                                                    .absolute()
                                                    .left(px(REASONING_RAIL_INSET))
                                                    .right(px(REASONING_RAIL_INSET))
                                                    .top(px((REASONING_TRACK_HEIGHT - REASONING_RAIL_HEIGHT) / 2.))
                                                    .h(px(REASONING_RAIL_HEIGHT))
                                                    .rounded_full()
                                                    .overflow_hidden()
                                                    .bg(palette.paper_muted)
                                                    .child(
                                                        div()
                                                            .relative()
                                                            .h_full()
                                                            .w(px(reasoning_fill_width))
                                                            .rounded_full()
                                                            .overflow_hidden()
                                                            .bg(rgb(0x319aff))
                                                            .child(
                                                                div()
                                                                    .absolute()
                                                                    .left_0()
                                                                    .top_0()
                                                                    .w(px(reasoning_track_width - REASONING_RAIL_INSET * 2.))
                                                                    .h_full()
                                                                    .children(reasoning_ticks)
                                                                    .when(modes.fast, |track| {
                                                                        track.child(self.fast_particles.clone())
                                                                    }),
                                                            ),
                                                    ),
                                            )
                                            .child(
                                                div()
                                                    .absolute()
                                                    .inset_0()
                                                    .top_0()
                                                    .h(px(REASONING_TRACK_HEIGHT))
                                                    .relative()
                                                    .child(
                                                        div()
                                                            .absolute()
                                                            .top(px((REASONING_TRACK_HEIGHT - REASONING_THUMB_SIZE) / 2.))
                                                            .left(px(
                                                                selected_reasoning_offset - REASONING_THUMB_SIZE / 2.
                                                            ))
                                                            .size(px(REASONING_THUMB_SIZE))
                                                            .rounded_full()
                                                            .border_1()
                                                            .border_color(palette.border_strong)
                                                            .bg(palette.paper)
                                                            .shadow(vec![
                                                                BoxShadow::new(
                                                                    px(0.),
                                                                    px(0.),
                                                                    hsla(0., 0., 0., 0.10),
                                                                )
                                                                .blur_radius(px(2.)),
                                                            ]),
                                                    )
                                                    .child(
                                                        div()
                                                            .absolute()
                                                            .inset_0()
                                                            .children(reasoning_controls),
                                                    ),
                                            ),
                                    )
                                    .child(div().relative().h(px(13.)).children(reasoning_labels))
                            }),
                    )
                },
            )
            .when(!self.model_picker_error.is_empty(), |picker| {
                picker.child(
                    div()
                        .px_2()
                        .py_1()
                        .text_size(px(10.))
                        .text_color(palette.danger)
                        .child(self.model_picker_error.clone()),
                )
            })
            .into_any_element()
    }

    fn execute_extension_action(&mut self, mut payload: serde_json::Value, cx: &mut Context<Self>) {
        if self.extension_settings.busy || !self.state.connection.connected {
            return;
        }
        payload["sessionId"] = json!(self.state.navigation.current_session_id.as_ref());
        let source_text = (payload["kind"] == "marketplace_add")
            .then(|| self.extension_settings.source.read(cx).text().to_owned());
        self.state.settings.error = "".into();
        self.extension_settings.busy = true;
        let id = self.runtime.request(Method::Execute, payload);
        self.pending_requests
            .insert(id, PendingRequest::ExtensionAction { source_text });
        cx.notify();
    }

    fn confirm_extension_action(
        &mut self,
        payload: serde_json::Value,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let locale = Locale::resolve(&self.state.settings.language);
        if let Some(detail) = extension_confirmation(&payload, locale) {
            let answer = window.prompt(
                PromptLevel::Warning,
                locale.text("ui.confirmExtensionAction"),
                Some(&detail),
                &[
                    PromptButton::cancel(locale.text("ui.cancel")),
                    PromptButton::new(locale.text("ui.confirm")),
                ],
                cx,
            );
            cx.spawn(async move |this, cx| {
                if answer.await == Ok(1) {
                    let _ = this.update(cx, |this, cx| this.execute_extension_action(payload, cx));
                }
            })
            .detach();
        } else {
            self.execute_extension_action(payload, cx);
        }
    }

    fn settings_modal_view(
        &mut self,
        palette: ThemePalette,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        let close_label = locale.text("ui.closeSettings");
        let picker = (self.model_picker_open && self.route_picker_target.is_some())
            .then(|| self.model_picker_view(palette, cx));
        let content = settings_surface(
            &self.state,
            palette,
            self.settings_section.as_str(),
            self.settings_provider.as_deref().unwrap_or_default(),
            (
                self.settings_provider_search.clone(),
                self.settings_model_search.clone(),
            ),
            (
                self.settings_provider_scroll.clone(),
                self.settings_model_scroll.clone(),
            ),
            (self.route_picker_target.as_ref(), picker),
            self.subagent_setting_menu,
            (
                self.archive_days,
                self.archive_days_menu_open,
                &self.open_projects,
            ),
            self.usage_hover.as_ref(),
            &self.extension_settings,
            &self.native_settings,
            cx,
        );
        let dialog = self.settings_dialog(content, palette);
        div()
            .id("settings-modal-backdrop")
            .role(Role::Region)
            .aria_label(close_label)
            .absolute()
            .occlude()
            .size_full()
            .p(px(42.))
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
        palette: ThemePalette,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        let title = locale.text("ui.settingsAndExtensions");
        div()
            .id("settings-modal")
            .role(Role::Region)
            .aria_label(title)
            .relative()
            .occlude()
            .w_full()
            .h_full()
            .max_w(px(1420.))
            .max_h(px(880.))
            .rounded(px(14.))
            .border_1()
            .border_color(palette.border_strong)
            .shadow(vec![
                BoxShadow::new(px(0.), px(18.), hsla(220. / 360., 0.15, 0.12, 0.22))
                    .blur_radius(px(48.)),
            ])
            .overflow_hidden()
            .child(
                div()
                    .id("settings-modal-content")
                    .size_full()
                    .rounded(px(13.))
                    .overflow_hidden()
                    .child(content),
            )
            .into_any_element()
    }

    fn composer_context_control(
        &mut self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let context = context_composition(
            &self.state.runtime.context_profile,
            &self.state.runtime.context_usage,
            self.selected_model_context_window(),
            locale,
        );
        let fraction = if context.limit > 0 {
            context.used as f32 / context.limit as f32
        } else {
            0.
        };
        let label = locale.text("ui.contextComposition");
        div()
            .id("context-composition")
            .relative()
            .on_hover(cx.listener(|this, hovered: &bool, _, cx| {
                this.context_popover_open = *hovered;
                if *hovered {
                    this.model_picker_open = false;
                }
                cx.notify();
            }))
            .child(
                div()
                    .id("context-composition-toggle")
                    .role(Role::Button)
                    .aria_label(label)
                    .aria_expanded(self.context_popover_open)
                    .tab_stop(true)
                    .size(px(32.))
                    .rounded_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.paper_muted))
                    .on_click(cx.listener(Self::toggle_context_popover))
                    .child(context_ring_svg(fraction, palette)),
            )
            .into_any_element()
    }

    fn context_popover_view(
        &self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let composition = context_composition(
            &self.state.runtime.context_profile,
            &self.state.runtime.context_usage,
            self.selected_model_context_window(),
            locale,
        );
        let used = format_context_tokens(composition.used);
        let total = format_context_tokens(composition.limit);
        let rail = composition.segments.iter().enumerate().fold(
            div()
                .w_full()
                .h(px(5.))
                .rounded_full()
                .overflow_hidden()
                .bg(palette.paper_muted)
                .flex()
                .gap(px(1.)),
            |rail, (index, segment)| {
                let width = if composition.limit > 0 {
                    segment.tokens as f32 / composition.limit as f32
                } else {
                    0.
                };
                rail.child(
                    div()
                        .h_full()
                        .w(relative(width.clamp(0., 1.)))
                        .bg(context_segment_color(index, &segment.category, palette)),
                )
            },
        );
        let rows = div().flex().flex_col().gap(px(8.)).children(
            composition
                .segments
                .iter()
                .enumerate()
                .map(|(index, segment)| {
                    let percentage = if composition.used > 0 {
                        (segment.tokens * 100 + composition.used / 2) / composition.used
                    } else {
                        0
                    };
                    div()
                        .h(px(19.))
                        .flex()
                        .items_center()
                        .gap(px(10.))
                        .text_size(px(13.))
                        .text_color(palette.muted)
                        .child(div().size(px(6.)).rounded_full().bg(context_segment_color(
                            index,
                            &segment.category,
                            palette,
                        )))
                        .child(div().flex_1().child(segment.label.clone()))
                        .child(
                            div()
                                .w(px(38.))
                                .flex_shrink_0()
                                .whitespace_nowrap()
                                .text_align(gpui::TextAlign::Right)
                                .text_color(palette.faint)
                                .child(format!("{percentage}%")),
                        )
                        .child(
                            div()
                                .min_w(px(40.))
                                .flex_shrink_0()
                                .whitespace_nowrap()
                                .text_align(gpui::TextAlign::Right)
                                .text_color(palette.muted)
                                .child(format_context_tokens(segment.tokens)),
                        )
                }),
        );
        div()
            .id("context-composition-popover")
            .on_mouse_down_out(cx.listener(Self::dismiss_picker))
            .role(Role::Region)
            .aria_label(locale.text("ui.contextComposition"))
            .absolute()
            .right(px(102.))
            .bottom(px(48.))
            .w(px(240.))
            .p(px(16.))
            .rounded(px(16.))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .shadow(vec![
                BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.12, 0.14))
                    .blur_radius(px(24.)),
            ])
            .flex()
            .flex_col()
            .gap(px(14.))
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .text_size(px(13.5))
                    .child(
                        div()
                            .font_weight(gpui::FontWeight::MEDIUM)
                            .text_color(palette.ink)
                            .child(locale.text("ui.contextComposition")),
                    )
                    .child(
                        div()
                            .whitespace_nowrap()
                            .text_color(palette.faint)
                            .child(used.clone()),
                    ),
            )
            .child(rail)
            .child(rows)
            .child(div().h(px(1.)).bg(palette.border))
            .when_some(composition.cache_hit_rate, |popover, cache_hit_rate| {
                popover.child(
                    div()
                        .h(px(19.))
                        .flex()
                        .items_center()
                        .justify_between()
                        .text_size(px(13.))
                        .text_color(palette.muted)
                        .child(locale.text("ui.cacheHitRate"))
                        .child(
                            div()
                                .whitespace_nowrap()
                                .text_color(palette.faint)
                                .child(format!("{cache_hit_rate}%")),
                        ),
                )
            })
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .text_size(px(13.))
                    .text_color(palette.muted)
                    .child(locale.text("ui.total"))
                    .child(
                        div()
                            .whitespace_nowrap()
                            .text_color(palette.faint)
                            .child(format!("{used} / {total}")),
                    ),
            )
            .into_any_element()
    }

    fn composer_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        expanded: bool,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let locale = Locale::resolve(&self.state.settings.language);
        self.composer.update(cx, |input, cx| {
            input.show_placeholder(self.completion.selected_skills.is_empty(), cx)
        });
        let prompt = self.composer.read(cx).text();
        let prompt_empty = prompt.trim().is_empty() && self.completion.selected_skills.is_empty();
        let composer_rows = composer_text_rows(prompt);
        let attachment_count = self.state.transcript.attachments.len();
        let input_height = px(composer_input_height(
            composer_rows,
            expanded,
            attachment_count,
        ));
        let project_name = std::path::Path::new(self.state.workspace.root.as_ref())
            .file_name()
            .and_then(|name| name.to_str())
            .filter(|name| !name.is_empty())
            .unwrap_or("workspace")
            .to_string();
        let model = if self.state.settings.model.is_empty() {
            locale.text("ui.selectModel").to_string()
        } else {
            self.state
                .catalogs
                .providers
                .iter()
                .find(|provider| {
                    provider.get("id").and_then(serde_json::Value::as_str)
                        == Some(self.state.settings.provider.as_ref())
                })
                .and_then(|provider| {
                    model_choices(
                        provider,
                        self.state.settings.model.as_ref(),
                        self.state.settings.reasoning.as_ref(),
                    )
                    .into_iter()
                    .find(|choice| choice.selected)
                })
                .map(|choice| {
                    choice.family_name.unwrap_or_else(|| {
                        catalog_model_name(choice.model, self.state.settings.model.as_ref())
                    })
                })
                .unwrap_or_else(|| humanize_model_id(self.state.settings.model.as_ref()))
        };
        let current_provider = self.state.settings.provider.to_string();
        let modes = model_modes(
            &self.state.catalogs.providers,
            &current_provider,
            self.state.settings.model.as_ref(),
            self.state.settings.reasoning.as_ref(),
            self.state.settings.chatgpt_fast_mode,
        );
        let mut reasoning = reasoning_display_name(
            if modes.reasoning.is_empty() {
                self.state.settings.reasoning.as_ref()
            } else {
                &modes.reasoning
            },
            locale,
        );
        if modes.fast {
            reasoning.push_str(" · ");
            reasoning.push_str(locale.text("model.fast"));
        }
        let show_cancel = self.state.runtime.running && prompt_empty && attachment_count == 0;
        let send_control = if show_cancel {
            div()
                .id("cancel-active")
                .role(Role::Button)
                .aria_label(labels.stop)
                .tab_stop(true)
                .size(px(32.))
                .rounded_full()
                .bg(palette.button)
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
                    palette.button
                })
                .text_color(if idle {
                    palette.faint
                } else {
                    palette.button_text
                })
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .on_click(cx.listener(Self::send_message))
                .child(icon(
                    "arrow-up",
                    16.,
                    if idle {
                        palette.faint
                    } else {
                        palette.button_text
                    },
                ))
                .into_any_element()
        };
        let picker = (self.model_picker_open && self.route_picker_target.is_none())
            .then(|| self.model_picker_view(palette, cx));
        let locale = Locale::resolve(&self.state.settings.language);
        let context_control = self.composer_context_control(palette, locale, cx);
        let attachment_previews = self.composer_attachments_view(palette, locale, cx);
        let completion_menu = self.completion_menu(palette, cx);
        let selected_skills = self.selected_skills_view(palette, cx);
        let composer_shell = div()
            .id("composer")
            .role(Role::Group)
            .aria_label(labels.composer)
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
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
            .when(expanded, |composer| {
                composer.child(
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
                        .child(branch_picker_control(self, false, palette, locale, cx)),
                )
            })
            .when_some(attachment_previews, |composer, previews| {
                composer.child(previews)
            })
            .child(
                div()
                    .h(input_height)
                    .capture_key_down(cx.listener(Self::completion_key))
                    .capture_action(cx.listener(Self::submit_completion))
                    .capture_action(cx.listener(Self::backspace_completion))
                    .px_1()
                    .overflow_hidden()
                    .flex()
                    .items_start()
                    .when_some(selected_skills, |row, skills| row.child(skills))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .child(self.composer.clone()),
                    ),
            )
            .when(!self.completion.submission_error.is_empty(), |composer| {
                composer.child(
                    div()
                        .px_3()
                        .pb_2()
                        .text_size(px(11.))
                        .text_color(palette.danger)
                        .child(self.completion.submission_error.clone()),
                )
            })
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
                            .child("+"),
                    )
                    .child(approval_picker_control(self, labels, palette, locale, cx))
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
                    .child(context_control)
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
                            .child(
                                div()
                                    .text_color(palette.faint)
                                    .text_size(px(10.))
                                    .child(reasoning),
                            )
                            .child(icon("chevron-down", 11., palette.faint)),
                    )
                    .child(send_control),
            );
        div()
            .id("composer-shell")
            .relative()
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .child(composer_shell)
            .when_some(completion_menu, |shell, menu| shell.child(menu))
            .when(self.context_popover_open, |shell| {
                shell.child(self.context_popover_view(palette, locale, cx))
            })
            .when_some(picker, |shell, picker| shell.child(picker))
            .into_any_element()
    }

    fn composer_attachments_view(
        &mut self,
        palette: ThemePalette,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        let attachments = self.state.transcript.attachments.clone();
        if attachments.is_empty() {
            return None;
        }
        let previews = attachments
            .into_iter()
            .enumerate()
            .map(|(index, attachment)| {
                let name = attachment
                    .get("name")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or(locale.text("ui.image"))
                    .to_string();
                let image = attachment
                    .get("path")
                    .and_then(serde_json::Value::as_str)
                    .filter(|path| !path.is_empty())
                    .map(|path| {
                        img(PathBuf::from(path))
                            .size_full()
                            .object_fit(ObjectFit::Contain)
                            .into_any_element()
                    })
                    .unwrap_or_else(|| {
                        div()
                            .size_full()
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(icon("image", 20., palette.faint))
                            .into_any_element()
                    });
                div()
                    .id(("attachment-preview", index))
                    .role(Role::Group)
                    .aria_label(name.clone())
                    .relative()
                    .w(px(92.))
                    .h(px(68.))
                    .flex_none()
                    .rounded(px(10.))
                    .border_1()
                    .border_color(palette.border)
                    .bg(palette.paper_muted)
                    .overflow_hidden()
                    .child(image)
                    .child(
                        div()
                            .id(("remove-attachment", index))
                            .role(Role::Button)
                            .aria_label(
                                locale.format("ui.removeName", &[("name", (name).to_string())]),
                            )
                            .tab_stop(true)
                            .absolute()
                            .top(px(5.))
                            .right(px(5.))
                            .size(px(22.))
                            .rounded_full()
                            .bg(rgba(0x161616c4))
                            .text_color(rgb(0xffffff))
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .hover(|style| style.bg(rgba(0x161616e8)))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.remove_attachment(index, cx);
                            }))
                            .child("×"),
                    )
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        Some(
            div()
                .id("composer-attachments")
                .role(Role::List)
                .aria_label(locale.text("ui.imageAttachments"))
                .h(px(82.))
                .px_3()
                .pt_2()
                .pb_1()
                .flex()
                .items_start()
                .gap_2()
                .overflow_x_scroll()
                .children(previews)
                .into_any_element(),
        )
    }

    fn queued_prompts_view(
        &mut self,
        palette: ThemePalette,
        labels: Labels,
        cx: &mut Context<Self>,
    ) -> Option<gpui::AnyElement> {
        let session_id = self.state.navigation.current_session_id.as_ref();
        let items = self
            .queued_prompts
            .iter()
            .filter(|item| item.session_id == session_id)
            .cloned()
            .collect::<Vec<_>>();
        if items.is_empty() {
            return None;
        }
        let locale = Locale::resolve(&self.state.settings.language);
        let rows = items
            .into_iter()
            .enumerate()
            .map(|(index, item)| self.queued_prompt_row(index, item, palette, labels, locale, cx))
            .collect::<Vec<_>>();
        Some(
            div()
                .id("queued-prompts")
                .role(Role::List)
                .aria_label(locale.text("ui.queuedMessages"))
                .mx(px(22.))
                .mb(px(-1.))
                .max_h(px(236.))
                .overflow_y_scroll()
                .border_1()
                .border_color(palette.border)
                .rounded_tl(px(15.))
                .rounded_tr(px(15.))
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.15, 0.12, 0.08))
                        .blur_radius(px(28.)),
                ])
                .children(rows)
                .into_any_element(),
        )
    }

    fn queued_prompt_row(
        &mut self,
        index: usize,
        item: QueuedPrompt,
        palette: ThemePalette,
        labels: Labels,
        locale: Locale,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        let can_guide = self.state.runtime.running
            && item.selected_skills.is_empty()
            && self.editing_queued_id.as_deref() != Some(item.id.as_str())
            && self.can_guide_prompt(&item.prompt);
        let guide_id = item.id.clone();
        let edit_id = item.id.clone();
        let delete_id = item.id.clone();
        let target_id = item.id.clone();
        let target_session = item.session_id.clone();
        let fallback = item
            .attachments
            .first()
            .and_then(|attachment| attachment.get("name"))
            .and_then(serde_json::Value::as_str)
            .unwrap_or(locale.text("ui.imageAttachment"));
        let text = if !item.selected_skills.is_empty() {
            format!(
                "{} {}",
                item.selected_skills
                    .iter()
                    .map(|name| composer_completion::skill_title(name))
                    .collect::<Vec<_>>()
                    .join(", "),
                item.prompt
            )
            .trim()
            .to_owned()
        } else if item.prompt.is_empty() {
            fallback.to_string()
        } else {
            item.prompt
        };
        let drag = QueuedPromptDrag {
            id: item.id.clone(),
            session_id: item.session_id,
            label: text.clone(),
        };
        let row_id = format!("queued-prompt-{}", item.id);
        div()
            .id(row_id)
            .role(Role::ListItem)
            .aria_label(text.clone())
            .min_h(px(48.))
            .px_2()
            .border_b_1()
            .border_color(palette.border)
            .flex()
            .items_center()
            .gap_1()
            .text_size(px(12.5))
            .text_color(if item.failed {
                palette.danger
            } else {
                palette.ink
            })
            .drag_over::<QueuedPromptDrag>(move |style, dragged, _, _| {
                if dragged.session_id == target_session && dragged.id != target_id {
                    style.bg(palette.hover)
                } else {
                    style
                }
            })
            .on_drop(cx.listener({
                let target_id = item.id.clone();
                move |this, dragged: &QueuedPromptDrag, _, cx| {
                    this.reorder_queued(dragged, &target_id, cx);
                }
            }))
            .child(
                div()
                    .id(format!("drag-queued-{}", item.id))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.reorderQueuedMessage"))
                    .tab_stop(true)
                    .w(px(20.))
                    .flex_none()
                    .text_color(palette.faint)
                    .cursor_move()
                    .on_drag(drag, |dragged: &QueuedPromptDrag, _, _, cx| {
                        cx.new(|_| dragged.clone())
                    })
                    .child(if item.attachments.is_empty() {
                        "⋮⋮"
                    } else {
                        "▧⋮"
                    }),
            )
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .overflow_hidden()
                    .whitespace_nowrap()
                    .text_ellipsis()
                    .child(text),
            )
            .child(
                div()
                    .id(("guide-queued", index))
                    .role(Role::Button)
                    .aria_label(labels.guide)
                    .tab_stop(can_guide)
                    .h(px(28.))
                    .px_2()
                    .rounded(px(8.))
                    .text_color(if can_guide {
                        palette.muted
                    } else {
                        palette.faint
                    })
                    .flex()
                    .items_center()
                    .gap_1()
                    .when(can_guide, |button| {
                        button
                            .cursor_pointer()
                            .hover(move |style| style.bg(palette.hover))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.guide_queued(&guide_id, cx);
                            }))
                    })
                    .child("↳")
                    .child(locale.text("ui.guide")),
            )
            .child(
                div()
                    .id(("edit-queued", index))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.editQueuedMessage"))
                    .tab_stop(true)
                    .size(px(28.))
                    .rounded(px(8.))
                    .text_color(palette.faint)
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.edit_queued(&edit_id, window, cx);
                    }))
                    .child(icon("notebook-pen", 14., palette.faint)),
            )
            .child(
                div()
                    .id(("delete-queued", index))
                    .role(Role::Button)
                    .aria_label(locale.text("ui.deleteQueuedMessage"))
                    .tab_stop(true)
                    .size(px(28.))
                    .rounded(px(8.))
                    .text_color(palette.faint)
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.delete_queued(&delete_id, cx);
                    }))
                    .child("×"),
            )
            .into_any_element()
    }

    fn request_create_terminal(&mut self) {
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

    fn create_terminal(&mut self, _: &ClickEvent, _: &mut Window, _: &mut Context<Self>) {
        self.request_create_terminal();
    }

    fn close_terminal(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
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

    fn clear_terminal(&mut self, _: &ClickEvent, _: &mut Window, cx: &mut Context<Self>) {
        let id = self.state.terminals.active_id.to_string();
        if let Some(terminal) = self.terminal_emulators.get_mut(&id) {
            *terminal = TerminalEmulator::default();
            self.terminal_scroll.scroll_to_bottom();
            cx.notify();
        }
    }

    fn write_terminal_data(&self, data: &str) {
        let terminal_id = self.state.terminals.active_id.to_string();
        if terminal_id.is_empty() || data.is_empty() {
            return;
        }
        self.runtime.request(
            Method::WriteTerminal,
            json!({"id": terminal_id, "data": data}),
        );
    }

    fn terminal_key(
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

    fn request_surface(&mut self, surface: Surface) {
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

    fn request_security_scan_projection(&self) {
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

    fn terminal_view(&mut self, palette: ThemePalette, cx: &mut Context<Self>) -> gpui::AnyElement {
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

fn terminal_key_data(keystroke: &gpui::Keystroke) -> Option<String> {
    if keystroke.modifiers.platform || keystroke.modifiers.function {
        return None;
    }
    let mut data = if keystroke.modifiers.control {
        String::from_utf8(vec![terminal_control_byte(&keystroke.key)?]).ok()?
    } else {
        terminal_special_key(&keystroke.key, keystroke.modifiers.shift)
            .or_else(|| {
                keystroke
                    .modifiers
                    .alt
                    .then_some(keystroke.key_char.as_deref())
                    .flatten()
            })?
            .to_string()
    };
    if keystroke.modifiers.alt && !data.starts_with('\x1b') {
        data.insert(0, '\x1b');
    }
    Some(data)
}

fn terminal_control_byte(key: &str) -> Option<u8> {
    if key == "space" {
        return Some(0);
    }
    let byte = *key.as_bytes().first()?;
    if key.len() == 1 && byte.is_ascii_alphabetic() {
        return Some(byte.to_ascii_uppercase() & 0x1f);
    }
    const INPUTS: &[u8] = b"@[\\]^_?";
    const OUTPUTS: &[u8] = &[0, 27, 28, 29, 30, 31, 127];
    INPUTS
        .iter()
        .position(|candidate| *candidate == byte)
        .map(|index| OUTPUTS[index])
}

fn terminal_special_key(key: &str, shift: bool) -> Option<&'static str> {
    if key == "tab" && shift {
        return Some("\x1b[Z");
    }
    const SEQUENCES: &[(&str, &str)] = &[
        ("enter", "\r"),
        ("tab", "\t"),
        ("backspace", "\x7f"),
        ("delete", "\x1b[3~"),
        ("up", "\x1b[A"),
        ("down", "\x1b[B"),
        ("right", "\x1b[C"),
        ("left", "\x1b[D"),
        ("home", "\x1b[H"),
        ("end", "\x1b[F"),
        ("pageup", "\x1b[5~"),
        ("pagedown", "\x1b[6~"),
        ("escape", "\x1b"),
    ];
    SEQUENCES
        .iter()
        .find(|(candidate, _)| *candidate == key)
        .map(|(_, sequence)| *sequence)
}

fn should_follow_transcript(
    old_session_id: &str,
    new_session_id: &str,
    old_count: usize,
    new_count: usize,
) -> bool {
    !new_session_id.is_empty()
        && new_count > 0
        && (old_session_id != new_session_id || new_count > old_count)
}

fn transcript_item_count(block_count: usize, pending_process: bool) -> usize {
    block_count + usize::from(pending_process) + 1
}

fn transcript_layout_changed(
    old_item_count: usize,
    new_item_count: usize,
    old_pending_process: bool,
    new_pending_process: bool,
) -> bool {
    old_item_count != new_item_count || old_pending_process != new_pending_process
}

impl Drop for AzemWindow {
    fn drop(&mut self) {
        let _ = self
            .window_state_path
            .as_deref()
            .map(|path| save_window_size(path, self.window_size))
            .transpose()
            .inspect_err(|error| tracing::warn!(%error, "persist GPUI window size"));
        self.runtime.disconnect();
    }
}

impl Render for AzemWindow {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.reconcile_side_panel_layout(window);
        self.advance_side_panel_animation(window);
        let locale = Locale::resolve(&self.state.settings.language);
        let preferences = AppearancePreferences::current(cx);
        window.set_rem_size(px(16. * preferences.ui_font_size / 14.));
        let palette = ThemePalette::for_window(window, cx);
        let labels = labels(&self.state.settings.language);
        let requested_surface = self.state.navigation.surface;
        let search_open = requested_surface == Surface::Search;
        let surface = if search_open {
            self.search_return_surface
        } else {
            requested_surface
        };
        let model_popup_visible = self.model_picker_open
            && !search_open
            && if self.settings_open {
                self.route_picker_target
                    .as_ref()
                    .is_some_and(|target| target.kind == RoutePickerKind::Model)
            } else {
                surface == Surface::Thread && self.route_picker_target.is_none()
            };
        if !model_popup_visible {
            self.reasoning_drag = None;
        }
        let animate_fast = model_popup_visible && {
            let modes = self.selected_model_modes();
            fast_particles::should_animate(
                !modes.levels.is_empty(),
                modes.fast,
                preferences.reduced_motion,
                window.is_window_active(),
            )
        };
        self.fast_particles
            .update(cx, |particles, cx| particles.set_active(animate_fast, cx));
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
            Surface::Security => labels.security.to_string(),
            Surface::Terminal => labels.terminal.to_string(),
        };
        let current_workspace_width = workspace_width(f32::from(window.bounds().size.width));
        let content = match surface {
            Surface::Thread if empty_thread => {
                let composer = self.composer_view(palette, labels, true, cx);
                let queue = self.queued_prompts_view(palette, labels, cx);
                div()
                    .id("empty-thread")
                    .role(Role::Region)
                    .aria_label(labels.new_conversation)
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .bg(palette.paper)
                    .px(px(chat_column_gutter(current_workspace_width)))
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(
                        div()
                            .w_full()
                            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
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
                            .child(
                                div()
                                    .w_full()
                                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                                    .flex()
                                    .flex_col()
                                    .when_some(queue, |stack, queue| stack.child(queue))
                                    .child(composer),
                            ),
                    )
                    .into_any_element()
            }
            Surface::Thread => {
                let blocks = self.state.transcript.blocks.clone();
                let transcript_content_item_count = block_count
                    + usize::from(needs_pending_process(
                        &self.state.transcript.blocks.borrow(),
                        self.state.runtime.running,
                    ));
                let agents = self.state.runtime.agents.clone();
                let live_elapsed_ms =
                    if self.state.runtime.running && self.state.runtime.run_started_at_ms > 0 {
                        (unix_millis() - self.state.runtime.run_started_at_ms).max(0)
                    } else {
                        0
                    };
                let reply_actions = ReplyActionsSnapshot {
                    hooks: self.state.runtime.hooks.clone(),
                    hook_catalog: self.state.catalogs.hooks.clone(),
                    popover: self.reply_popover.clone(),
                    feedback: self.reply_feedback.clone(),
                    hovered_message: self.hovered_message.clone(),
                };
                let process_expansion = self.process_expansion.clone();
                let owner = cx.entity();
                let locale = Locale::resolve(&self.state.settings.language);
                let reduced_motion = self
                    .state
                    .settings
                    .appearance
                    .get("reducedMotion")
                    .and_then(serde_json::Value::as_bool)
                    .unwrap_or(false);
                let selected_agent = self.state.runtime.selected_agent_id.to_string();
                let panel_visible = self.side_panel_open || self.side_panel_closing;
                let agent_panel_visible = self.side_panel_agents_open && panel_visible;
                let side_panel_visible = !self.side_panel_agents_open && panel_visible;
                let side_panel_layout_width = if panel_visible {
                    self.side_panel_width
                } else {
                    0.
                };
                let environment_returning = !self.side_panel_agents_open
                    && selected_agent.is_empty()
                    && self.environment_open
                    && self.side_panel_closing
                    && environment_panel_fits(current_workspace_width, 0.);
                let environment_visible = !self.side_panel_agents_open
                    && selected_agent.is_empty()
                    && self.environment_open
                    && (environment_returning
                        || environment_panel_fits(
                            current_workspace_width,
                            side_panel_layout_width,
                        ));
                let environment = if environment_visible {
                    Some(environment_panel(
                        self,
                        palette,
                        labels,
                        self.environment_expanded.as_deref(),
                        if environment_returning {
                            0.
                        } else {
                            side_panel_layout_width
                        },
                        cx,
                    ))
                } else {
                    None
                };
                let side_panel = if side_panel_visible {
                    Some(side_panel(self, palette, labels, cx))
                } else {
                    None
                };
                let agent_detail = if !agent_panel_visible {
                    None
                } else {
                    Some(agent_side_panel(
                        self,
                        palette,
                        &selected_agent,
                        reduced_motion,
                        process_expansion.clone(),
                        owner.clone(),
                        cx,
                    ))
                };
                let right_panel_width = if environment_returning {
                    self.side_panel_visible_width
                        .max(ENVIRONMENT_PANEL_RESERVED_WIDTH)
                } else {
                    side_panel_layout_width
                        + if environment.is_some() {
                            ENVIRONMENT_PANEL_RESERVED_WIDTH
                        } else {
                            0.
                        }
                };
                let column_gutter =
                    chat_column_gutter((current_workspace_width - right_panel_width).max(0.));
                let column_animation_offset = if environment_returning {
                    0.
                } else {
                    chat_column_animation_offset(
                        side_panel_layout_width,
                        self.side_panel_visible_width,
                    )
                };
                let composer = self.composer_view(palette, labels, false, cx);
                let queue = self.queued_prompts_view(palette, labels, cx);
                div()
                    .id("active-thread")
                    .role(Role::Region)
                    .aria_label(locale.text("conversation.transcript"))
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
                            .aria_label(locale.text("conversation.transcript"))
                            .flex_1()
                            .min_h_0()
                            .overflow_hidden()
                            .pr(px(right_panel_width))
                            .flex()
                            .flex_col()
                            .child(
                                list(self.transcript_list.clone(), move |index, _, _| {
                                    if index == transcript_content_item_count {
                                        div()
                                            .id("transcript-composer-clearance")
                                            .h(px(TRANSCRIPT_COMPOSER_CLEARANCE))
                                            .into_any_element()
                                    } else {
                                        timeline_entry(
                                            index,
                                            &blocks.borrow(),
                                            (
                                                palette,
                                                locale,
                                                reduced_motion,
                                                column_gutter,
                                                live_elapsed_ms,
                                            ),
                                            &agents,
                                            process_expansion.clone(),
                                            owner.clone(),
                                            Some(&reply_actions),
                                        )
                                    }
                                })
                                .relative()
                                .left(px(column_animation_offset))
                                .flex_1(),
                            ),
                    )
                    .child(
                        div()
                            .w_full()
                            .pl(px(column_gutter))
                            .pr(px(right_panel_width + column_gutter))
                            .pt(px(8.))
                            .pb(px(14.))
                            .flex()
                            .justify_center()
                            .child(
                                div()
                                    .w_full()
                                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                                    .relative()
                                    .left(px(column_animation_offset))
                                    .flex()
                                    .flex_col()
                                    .when_some(queue, |stack, queue| stack.child(queue))
                                    .child(composer),
                            ),
                    )
                    .when_some(environment, |thread, environment| thread.child(environment))
                    .when_some(side_panel, |thread, side_panel| thread.child(side_panel))
                    .when_some(agent_detail, |thread, detail| thread.child(detail))
                    .into_any_element()
            }
            Surface::Search => div().into_any_element(),
            Surface::Projects => projects_surface(&self.state, palette, labels, cx),
            Surface::Files => workspace_files_surface(&self.state, palette, cx),
            Surface::Changes => workspace_changes_surface(&self.state, palette, cx),
            Surface::PullRequests => pull_requests_surface(&self.state, palette, cx),
            Surface::Security => security_surface(&self.state, &self.runtime, palette),
            Surface::Terminal => self.terminal_view(palette, cx),
        };
        let sidebar = sidebar(
            &self.state,
            palette,
            labels,
            &self.open_projects,
            self.show_all_sessions,
            self.sidebar_context_menu.as_ref().map(|menu| &menu.target),
            cx,
        );
        let settings_modal = if self.settings_open {
            Some(self.settings_modal_view(palette, cx))
        } else {
            None
        };
        let search_modal = if search_open {
            Some(search_surface(
                &self.state,
                self.search_input.clone(),
                palette,
                labels,
                cx,
            ))
        } else {
            None
        };
        let sidebar_menu = self
            .sidebar_context_menu
            .as_ref()
            .map(|_| sidebar_context_menu_view(self, palette, locale, cx));
        let rename_modal = self
            .renaming_session_id
            .as_ref()
            .map(|_| session_rename_modal(self, palette, locale, cx));
        let agent_titlebar_visible = surface == Surface::Thread
            && self.side_panel_agents_open
            && (self.side_panel_open || self.side_panel_closing);
        let agent_titlebar_width = if agent_titlebar_visible {
            self.side_panel_width
        } else {
            0.
        };
        div()
            .id("azem-root")
            .relative()
            .role(Role::Application)
            .aria_label(labels.application)
            .track_focus(&self.focus)
            .size_full()
            .on_action(cx.listener(Self::submit_message))
            .on_action(cx.listener(Self::toggle_search))
            .on_action(cx.listener(Self::toggle_settings))
            .on_action(cx.listener(Self::find_settings))
            .on_action(cx.listener(Self::toggle_terminal))
            .on_action(cx.listener(Self::close_overlay))
            .bg(palette.canvas)
            .text_color(palette.ink)
            .font_family(if preferences.font == "system" {
                ".SystemUIFont".into()
            } else {
                preferences.font
            })
            .text_size(px(preferences.ui_font_size))
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
                                .relative()
                                .h(px(if surface == Surface::Thread { 46. } else { 0. }))
                                .pl(px(if surface == Surface::Thread { 22. } else { 0. }))
                                .pr(px(if surface == Surface::Thread {
                                    22. + agent_titlebar_width
                                } else {
                                    0.
                                }))
                                .when(surface == Surface::Thread && !empty_thread, |header| {
                                    header.border_b_1().border_color(palette.border)
                                })
                                .bg(palette.paper)
                                .overflow_hidden()
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
                                                            .child(
                                                                if surface_title.contains("UI") {
                                                                    locale.text(
                                                                        "conversation.designTask",
                                                                    )
                                                                } else {
                                                                    locale.text("conversation.task")
                                                                },
                                                            ),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(13.))
                                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                                            .child(surface_title.clone()),
                                                    )
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
                                                    .px_2()
                                                    .py(px(3.))
                                                    .rounded_full()
                                                    .bg(if self.state.runtime.running {
                                                        rgba(0x1f7af014)
                                                    } else {
                                                        palette.paper_muted
                                                    })
                                                    .text_size(px(10.))
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .text_color(if self.state.runtime.running {
                                                        rgb(0x1f7af0)
                                                    } else {
                                                        palette.faint
                                                    })
                                                    .child(if self.state.runtime.running {
                                                        locale.text("ui.running2")
                                                    } else {
                                                        locale.text("ui.ready")
                                                    }),
                                            )
                                        })
                                        .child(
                                            div()
                                                .id("environment-toggle")
                                                .role(Role::Button)
                                                .aria_label(locale.text("ui.toggleEnvironment"))
                                                .aria_selected(self.environment_open)
                                                .tab_stop(true)
                                                .h(px(29.))
                                                .px_2()
                                                .ml_2()
                                                .rounded(px(7.))
                                                .border_1()
                                                .border_color(palette.border)
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
                                                    this.toggle_environment_panel(cx);
                                                }))
                                                .child(icon(
                                                    "sliders-horizontal",
                                                    15.,
                                                    palette.muted,
                                                )),
                                        )
                                        .child(
                                            div()
                                                .id("side-panel-toggle")
                                                .role(Role::Button)
                                                .aria_label(locale.text("ui.toggleSidePanel"))
                                                .aria_selected(self.side_panel_open)
                                                .tab_stop(true)
                                                .h(px(29.))
                                                .px_2()
                                                .ml_1()
                                                .rounded(px(7.))
                                                .border_1()
                                                .border_color(palette.border)
                                                .bg(if self.side_panel_open {
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
                                                .on_click(cx.listener(|this, _, window, cx| {
                                                    this.toggle_side_panel(window, cx);
                                                }))
                                                .child(icon("panels", 15., palette.muted)),
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
                                })
                                .when(agent_titlebar_visible, |header| {
                                    header.child(agent_panel_tab(self, palette, locale, cx))
                                }),
                        )
                        .child(content)
                        .when(self.terminal_open, |workspace| {
                            workspace.child(
                                div()
                                    .id("terminal-dock")
                                    .h(px(260.))
                                    .min_h(px(140.))
                                    .flex_shrink_0()
                                    .border_t_1()
                                    .border_color(palette.border)
                                    .bg(palette.paper)
                                    .child(self.terminal_view(palette, cx)),
                            )
                        }),
                ),
            )
            .when_some(search_modal, |root, modal| root.child(modal))
            .when_some(settings_modal, |root, modal| root.child(modal))
            .when_some(sidebar_menu, |root, menu| root.child(menu))
            .when_some(rename_modal, |root, modal| root.child(modal))
    }
}

fn composer_has_submission(prompt: &str, attachments: &[serde_json::Value]) -> bool {
    !prompt.is_empty() || !attachments.is_empty()
}

fn composer_input_height(rows: usize, expanded: bool, attachment_count: usize) -> f32 {
    let minimum = if expanded {
        112.
    } else if attachment_count > 0 {
        72.
    } else {
        60.
    };
    (20. + rows as f32 * 24.).max(minimum)
}

fn runtime_busy(state: &AppState) -> bool {
    state.runtime.running || !state.runtime.active_session_id.is_empty()
}

fn reveal_project_path(path: &str) -> std::io::Result<()> {
    #[cfg(target_os = "macos")]
    let mut command = {
        let mut command = std::process::Command::new("open");
        command.arg("-R").arg(path);
        command
    };
    #[cfg(target_os = "windows")]
    let mut command = {
        let mut command = std::process::Command::new("explorer.exe");
        command.arg(path);
        command
    };
    #[cfg(all(unix, not(target_os = "macos")))]
    let mut command = {
        let mut command = std::process::Command::new("xdg-open");
        command.arg(path);
        command
    };
    command.spawn().map(|_| ())
}

fn security_scan_target(state: &AppState) -> Option<String> {
    let selected = state
        .security
        .projection
        .get("scan")
        .unwrap_or(&state.security.projection)
        .get("id")
        .or_else(|| state.security.projection.get("scanId"))
        .and_then(serde_json::Value::as_str)
        .filter(|id| {
            state.security.scans.iter().any(|scan| {
                scan.get("id")
                    .or_else(|| scan.get("scanId"))
                    .and_then(serde_json::Value::as_str)
                    == Some(*id)
            })
        });
    selected
        .or_else(|| {
            state
                .security
                .scans
                .first()
                .and_then(|scan| scan.get("id").or_else(|| scan.get("scanId")))
                .and_then(serde_json::Value::as_str)
        })
        .map(str::to_string)
}

fn approval_mode_action(
    state: &AppState,
    target: &str,
    pending: bool,
) -> Option<serde_json::Value> {
    if !state.connection.connected
        || runtime_busy(state)
        || pending
        || target == state.settings.approval_mode.as_ref()
        || !APPROVAL_MODES.iter().any(|(mode, _, _)| *mode == target)
    {
        return None;
    }
    Some(json!({
        "kind": "set_approval_mode",
        "target": target,
        "sessionId": state.navigation.current_session_id,
    }))
}

fn visible_git_branches(branches: &[serde_json::Value], current: &str, query: &str) -> Vec<String> {
    let query = query.trim().to_lowercase();
    let mut names = branches
        .iter()
        .filter_map(|branch| branch["name"].as_str())
        .filter(|name| !name.is_empty() && name.to_lowercase().contains(&query))
        .map(str::to_owned)
        .collect::<Vec<_>>();
    names.sort_by(|a, b| (a != current).cmp(&(b != current)).then_with(|| a.cmp(b)));
    names.dedup();
    names
}

fn reorder_session_queue(
    prompts: &mut [QueuedPrompt],
    session_id: &str,
    source_id: &str,
    target_id: &str,
) -> bool {
    let slots = prompts
        .iter()
        .enumerate()
        .filter_map(|(index, item)| (item.session_id == session_id).then_some(index))
        .collect::<Vec<_>>();
    let mut queued = slots
        .iter()
        .map(|index| prompts[*index].clone())
        .collect::<Vec<_>>();
    let Some(source) = queued.iter().position(|item| item.id == source_id) else {
        return false;
    };
    let Some(target) = queued.iter().position(|item| item.id == target_id) else {
        return false;
    };
    if source == target {
        return false;
    }
    let item = queued.remove(source);
    queued.insert(target, item);
    for (slot, item) in slots.into_iter().zip(queued) {
        prompts[slot] = item;
    }
    true
}

fn should_start_next_queued(old_busy: bool, session_changed: bool, state: &AppState) -> bool {
    !runtime_busy(state) && (old_busy || session_changed)
}

#[derive(Debug, PartialEq)]
struct ContextSegment {
    category: String,
    label: String,
    tokens: i64,
}

#[derive(Debug, PartialEq)]
struct ContextComposition {
    segments: Vec<ContextSegment>,
    used: i64,
    limit: i64,
    cache_hit_rate: Option<i64>,
}

fn context_composition(
    profile: &serde_json::Value,
    usage: &serde_json::Value,
    fallback_limit: i64,
    locale: Locale,
) -> ContextComposition {
    let mut totals = context_profile_totals(profile);
    let input_tokens = usage
        .get("inputTokens")
        .and_then(json_i64)
        .unwrap_or_default()
        .max(0);
    let output_tokens = usage
        .get("outputTokens")
        .and_then(json_i64)
        .unwrap_or_default()
        .max(0);
    if totals.is_empty() && input_tokens > 0 {
        totals.insert("provider_input".to_string(), input_tokens);
    }
    if output_tokens > 0 {
        *totals.entry("current_output".to_string()).or_default() += output_tokens;
    }
    let mut segments = totals
        .into_iter()
        .map(|(category, tokens)| ContextSegment {
            label: context_category_label(&category, locale),
            category,
            tokens,
        })
        .collect::<Vec<_>>();
    segments.sort_by(|left, right| {
        right
            .tokens
            .cmp(&left.tokens)
            .then_with(|| left.category.cmp(&right.category))
    });
    let used = segments.iter().map(|segment| segment.tokens).sum();
    let limit = usage
        .get("contextLimit")
        .and_then(json_i64)
        .unwrap_or_default()
        .max(fallback_limit)
        .max(0);
    ContextComposition {
        segments,
        used,
        limit,
        cache_hit_rate: cache_hit_rate(usage),
    }
}

fn cache_hit_rate(usage: &serde_json::Value) -> Option<i64> {
    let (input_key, cached_key) = if usage["currentTurnMainReported"].as_bool() == Some(true) {
        ("currentTurnMainReportedInput", "currentTurnMainCached")
    } else if usage["mainCacheReported"].as_bool() == Some(true) {
        ("mainCacheInput", "mainCachedInput")
    } else {
        return None;
    };
    let input = usage.get(input_key).and_then(json_i64).unwrap_or_default();
    let cached = usage.get(cached_key).and_then(json_i64).unwrap_or_default();
    (input > 0).then(|| ((cached.clamp(0, input) * 100 + input / 2) / input).clamp(0, 100))
}

fn context_profile_totals(profile: &serde_json::Value) -> HashMap<String, i64> {
    let mut totals = HashMap::new();
    for contribution in profile
        .get("contributions")
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
    {
        let tokens = contribution
            .get("tokens")
            .and_then(json_i64)
            .unwrap_or_default()
            .max(0);
        if tokens > 0 {
            let category = contribution
                .get("category")
                .and_then(serde_json::Value::as_str)
                .filter(|category| !category.is_empty())
                .unwrap_or("other");
            *totals.entry(category.to_string()).or_default() += tokens;
        }
    }
    totals
}

fn json_i64(value: &serde_json::Value) -> Option<i64> {
    value
        .as_i64()
        .or_else(|| value.as_u64().and_then(|value| i64::try_from(value).ok()))
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn context_category_label(category: &str, locale: Locale) -> String {
    const LABELS: [(&str, &str); 8] = [
        ("core", "context.core"),
        ("conversation", "context.conversation"),
        ("builtin_tools", "context.tools"),
        ("skills", "context.skills"),
        ("mcp", "context.mcp"),
        ("current_output", "context.output"),
        ("provider_input", "context.input"),
        ("other", "context.other"),
    ];
    LABELS
        .iter()
        .find(|(key, _)| key == &category)
        .map(|(_, key)| locale.text(key))
        .unwrap_or(category)
        .replace('_', " ")
}

fn context_segment_color(index: usize, category: &str, palette: ThemePalette) -> gpui::Rgba {
    if category == "current_output" {
        palette.warning
    } else if index == 0 {
        palette.accent
    } else {
        palette.faint
    }
}

fn format_context_tokens(tokens: i64) -> String {
    let tokens = tokens.max(0);
    if tokens < 1_000 {
        return tokens.to_string();
    }
    let thousands = tokens as f64 / 1_000.;
    if (thousands * 10.).round() as i64 % 10 == 0 {
        format!("{thousands:.0}k")
    } else {
        format!("{thousands:.1}k")
    }
}

fn context_ring_svg(fraction: f32, palette: ThemePalette) -> gpui::Svg {
    let fraction = fraction.clamp(0., 1.);
    let arc = if fraction >= 0.999 {
        r#"<circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2.5"/>"#
            .to_string()
    } else if fraction > 0. {
        let angle = fraction * std::f32::consts::TAU;
        let x = 8. + 6. * angle.sin();
        let y = 8. - 6. * angle.cos();
        let large = i32::from(fraction > 0.5);
        format!(
            r#"<path d="M 8 2 A 6 6 0 {large} 1 {x:.3} {y:.3}" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/>"#
        )
    } else {
        String::new()
    };
    let data = format!(
        r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-opacity=".1" stroke-width="2.5"/>{arc}</svg>"#
    );
    svg()
        .data(data.as_bytes())
        .size(px(16.))
        .text_color(if fraction > 0.85 {
            palette.danger
        } else {
            palette.ink
        })
}

fn catalog_model_name(model: &serde_json::Value, model_id: &str) -> String {
    let names = ["familyName", "displayName", "name"]
        .into_iter()
        .filter_map(|key| model.get(key).and_then(serde_json::Value::as_str))
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .collect::<Vec<_>>();
    names
        .iter()
        .find(|value| !value.eq_ignore_ascii_case(model_id))
        .or_else(|| names.first())
        .map(|value| (*value).to_string())
        .unwrap_or_else(|| humanize_model_id(model_id))
}

fn humanize_model_id(model_id: &str) -> String {
    model_id
        .rsplit('/')
        .next()
        .unwrap_or(model_id)
        .split(['-', '_'])
        .filter(|part| !part.is_empty())
        .map(humanize_model_word)
        .collect::<Vec<_>>()
        .join(" ")
}

fn humanize_model_word(value: &str) -> String {
    const ACRONYMS: [(&str, &str); 4] =
        [("ai", "AI"), ("api", "API"), ("gpt", "GPT"), ("llm", "LLM")];
    let normalized = value.to_ascii_lowercase();
    if let Some((_, acronym)) = ACRONYMS.iter().find(|(key, _)| key == &normalized) {
        return (*acronym).to_string();
    }
    let mut characters = normalized.chars();
    characters
        .next()
        .map(|first| first.to_uppercase().chain(characters).collect())
        .unwrap_or_default()
}

fn provider_picker_label(provider_id: &str, provider_name: &str) -> String {
    match provider_id.trim().to_ascii_lowercase().as_str() {
        "chatgpt" | "openai" => "ChatGPT".to_string(),
        "grok" | "xai" => "Grok".to_string(),
        "cursor" => "Cursor".to_string(),
        "openrouter" => "OpenRouter".to_string(),
        _ => provider_name.to_string(),
    }
}

fn model_capability_hint(
    model: &serde_json::Value,
    model_id: &str,
    model_name: &str,
    locale: Locale,
) -> String {
    let capabilities = model_string_set(model, "capabilities");
    let inputs = model_string_set(model, "inputModalities");
    let identity = format!("{model_id} {model_name}").to_ascii_lowercase();
    let reasoning = capabilities.contains("reasoning")
        || model
            .get("reasoningLevels")
            .and_then(serde_json::Value::as_array)
            .is_some_and(|levels| !levels.is_empty());
    let image = inputs.contains("image")
        || ["claude", "gemini", "gpt-", "codex"]
            .iter()
            .any(|value| identity.contains(value));
    let (reasoning_label, image_label, tools_label, structured_label) = capability_labels(locale);
    let hints = [
        reasoning.then_some(reasoning_label),
        image.then_some(image_label),
        capabilities.contains("tools").then_some(tools_label),
        capabilities
            .contains("structured-output")
            .then_some(structured_label),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>();
    let explicit = hints.join(" · ");
    if explicit.is_empty() {
        default_model_capability_hint(&identity, locale)
    } else {
        explicit
    }
}

fn capability_labels(locale: Locale) -> (&'static str, &'static str, &'static str, &'static str) {
    (
        locale.text("capability.reasoning"),
        locale.text("capability.image"),
        locale.text("capability.tools"),
        locale.text("capability.structured"),
    )
}

fn default_model_capability_hint(identity: &str, locale: Locale) -> String {
    if identity.contains("codex") || identity.contains("code") {
        return locale.text("ui.codeTools").to_string();
    }
    if identity.contains("spark") || identity.contains("fast") {
        return locale.text("ui.fastEfficient").to_string();
    }
    locale.text("ui.reasoningImageTools").to_string()
}

fn model_string_set(model: &serde_json::Value, key: &str) -> HashSet<String> {
    model
        .get(key)
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(serde_json::Value::as_str)
        .map(str::to_ascii_lowercase)
        .collect()
}

const MODEL_PICKER_WIDTH: f32 = 312.;
const REASONING_TRACK_HEIGHT: f32 = 32.;
const REASONING_RAIL_HEIGHT: f32 = 16.;
const REASONING_THUMB_SIZE: f32 = 26.;
const REASONING_THUMB_INSET: f32 = 18.;
const REASONING_RAIL_INSET: f32 = 11.;
const REASONING_TICK_SIZE: f32 = 4.;

fn reasoning_stop_offset(track_width: f32, index: usize, count: usize) -> f32 {
    let ratio = if count <= 1 {
        1.
    } else {
        index.min(count - 1) as f32 / (count - 1) as f32
    };
    reasoning_offset_from_progress(track_width, ratio)
}

fn reasoning_offset_from_progress(track_width: f32, progress: f32) -> f32 {
    REASONING_THUMB_INSET + (track_width - REASONING_THUMB_INSET * 2.).max(1.) * progress
}

fn reasoning_progress_from_position(position_x: f32, track_left: f32, track_width: f32) -> f32 {
    let usable = (track_width - REASONING_THUMB_INSET * 2.).max(1.);
    ((position_x - track_left - REASONING_THUMB_INSET) / usable).clamp(0., 1.)
}

fn reasoning_index_from_position(
    position_x: f32,
    track_left: f32,
    track_width: f32,
    count: usize,
) -> usize {
    if count <= 1 {
        return 0;
    }
    let ratio = reasoning_progress_from_position(position_x, track_left, track_width);
    (ratio * (count - 1) as f32).round() as usize
}

fn reasoning_display_name(reasoning: &str, locale: Locale) -> String {
    const LABELS: [(&str, &str); 10] = [
        ("none", "reasoning.off"),
        ("minimal", "reasoning.minimal"),
        ("low", "reasoning.low"),
        ("medium", "reasoning.medium"),
        ("high", "reasoning.high"),
        ("xhigh", "reasoning.veryHigh"),
        ("max", "reasoning.highest"),
        ("ultra", "reasoning.ultra"),
        ("", "reasoning.default"),
        ("default", "reasoning.default"),
    ];
    let normalized = reasoning.trim().to_ascii_lowercase();
    if let Some((_, key)) = LABELS.iter().find(|(value, _)| value == &normalized) {
        locale.text(key).to_string()
    } else {
        humanize_model_id(&normalized)
    }
}

fn composer_text_rows(text: &str) -> usize {
    // ponytail: seven visible rows match the Codex composer; the input scrolls beyond this ceiling.
    text.split('\n')
        .map(|line| {
            line.chars()
                .map(|character| if character.is_ascii() { 1 } else { 2 })
                .sum::<usize>()
                .div_ceil(88)
                .max(1)
        })
        .sum::<usize>()
        .clamp(1, 7)
}

fn window_state_path(options: &RuntimeOptions) -> Option<PathBuf> {
    options
        .state_dir
        .clone()
        .or_else(|| std::env::var_os("AZEM_HOME").map(PathBuf::from))
        .or_else(|| dirs::home_dir().map(|home| home.join(".azem")))
        .map(|directory| directory.join("gpui-window.json"))
}

fn decode_window_size(content: &str) -> Option<Size<Pixels>> {
    let value: serde_json::Value = serde_json::from_str(content).ok()?;
    let width = value.get("width")?.as_f64()? as f32;
    let height = value.get("height")?.as_f64()? as f32;
    [
        width.is_finite(),
        height.is_finite(),
        width >= 880.,
        height >= 640.,
    ]
    .into_iter()
    .all(|valid| valid)
    .then(|| size(px(width), px(height)))
}

fn load_window_size(path: &std::path::Path) -> Option<Size<Pixels>> {
    decode_window_size(&fs::read_to_string(path).ok()?)
}

fn save_window_size(path: &std::path::Path, window_size: Size<Pixels>) -> std::io::Result<()> {
    fs::create_dir_all(path.parent().unwrap_or_else(|| std::path::Path::new(".")))?;
    let temporary = path.with_extension("json.tmp");
    fs::write(
        &temporary,
        serde_json::to_vec(&json!({
            "width": f32::from(window_size.width),
            "height": f32::from(window_size.height),
        }))?,
    )?;
    fs::rename(temporary, path)
}

fn open_main_window(
    cx: &mut App,
    options: Rc<RefCell<RuntimeOptions>>,
    runtime: RuntimeConnection,
) -> WindowHandle<AzemWindow> {
    let state_path = window_state_path(&options.borrow());
    let runtime_options = options;
    let window_size = state_path
        .as_deref()
        .and_then(load_window_size)
        .unwrap_or_else(|| size(px(1440.), px(920.)));
    let bounds = Bounds::centered(None, window_size, cx);
    cx.open_window(
        WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            window_min_size: Some(size(px(880.), px(640.))),
            titlebar: Some(gpui::TitlebarOptions {
                title: None,
                appears_transparent: true,
                ..Default::default()
            }),
            ..Default::default()
        },
        move |window, cx| {
            runtime.wait_for_startup(Duration::from_secs(2));
            cx.new(|cx| {
                cx.observe_window_bounds(window, |this: &mut AzemWindow, window, _| {
                    let size = window.window_bounds().get_bounds().size;
                    this.window_size = size;
                })
                .detach();
                cx.observe_window_activation(window, |this: &mut AzemWindow, window, cx| {
                    if !window.is_window_active() {
                        this.reasoning_drag = None;
                        this.side_panel_resize_drag = None;
                    }
                    cx.notify();
                })
                .detach();
                AzemWindow::new(window, cx, runtime, runtime_options, state_path)
            })
        },
    )
    .expect("failed to open Azem window")
}

fn runtime_options() -> RuntimeOptions {
    let mut workspace = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
    let mut workspace_explicit = false;
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
                    workspace_explicit = true;
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
    workspace = if !workspace_explicit {
        restore_desktop_workspace(workspace.clone(), state_dir.as_deref())
    } else {
        workspace
    };
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
    let runtime = RuntimeConnection::start(options.clone());
    let options = Rc::new(RefCell::new(options));
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
        let runtime = RuntimeConnection::start(reopen_options.borrow().clone());
        *reopen_window.borrow_mut() = Some(open_main_window(cx, reopen_options.clone(), runtime));
        cx.activate(true);
    });
    application.run(move |cx| {
        cx.set_app_identity("dev.azem.gpui", "Azem GPUI");
        text_input::init(cx);
        cx.bind_keys([
            KeyBinding::new("enter", Submit, Some("TextInput")),
            KeyBinding::new("cmd-k", ToggleSearch, None),
            KeyBinding::new("cmd-f", FindSettings, None),
            KeyBinding::new("cmd-,", ToggleSettings, None),
            KeyBinding::new("cmd-`", ToggleTerminal, None),
            KeyBinding::new("escape", CloseOverlay, None),
        ]);
        let handle = open_main_window(cx, options, runtime);
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
#[cfg(test)]
mod composer_tests {
    use crate::localization::Locale;
    use serde_json::json;
    use uuid::Uuid;

    #[test]
    fn first_window_overlaps_runtime_start_with_native_window_creation() {
        let source = include_str!("main.rs");
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
    fn project_session_navigation_reuses_the_existing_window() {
        let surfaces = include_str!("surfaces.rs");
        assert!(!surfaces.contains("launch_gpui_window"));
        assert!(!surfaces.contains("std::process::Command::new"));
        assert_eq!(surfaces.matches("this.switch_workspace(").count(), 3);

        let runtime_events = include_str!("main.rs")
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
        let source = include_str!("main.rs");
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
        let source = include_str!("main.rs")
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
        let source = include_str!("main.rs");
        assert!(source.contains(".id(\"environment-toggle\")"));
        assert!(source.contains("this.toggle_environment_panel(cx)"));
        assert!(source.contains(".id(\"side-panel-toggle\")"));
        assert!(source.contains("this.toggle_side_panel(window, cx)"));
        assert!(
            source.contains("environment_panel_fits(workspace_width, side_panel_layout_width)")
        );
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
        let source = include_str!("main.rs");
        assert!(source.contains("window.request_animation_frame()"));
        assert!(source.contains("schedule_run_elapsed_tick"));
        assert!(source.contains("side_panel_resize_mouse_move"));
        assert!(source.contains("let side_panel_layout_width = if panel_visible"));
        let panel_flow = source
            .split("    fn resize_side_panel(")
            .nth(1)
            .unwrap()
            .split("    fn toggle_branch_picker(")
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
        let main = include_str!("main.rs");
        assert!(main.contains(".max_w(px(CHAT_COLUMN_MAX_WIDTH))"));
        assert!(main.contains(".pl(px(column_gutter))"));
        assert!(main.contains(".pr(px(right_panel_width + column_gutter))"));
        let production = main.split("#[cfg(test)]").next().unwrap();
        assert_eq!(
            production
                .matches(".left(px(column_animation_offset))")
                .count(),
            2
        );
        let surfaces = include_str!("surfaces.rs");
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
            super::security_settings_payload(&draft, ["4"].into_iter(), Locale::resolve("en"))
                .is_err()
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
        let source = include_str!("surfaces.rs");
        let start = source.find("todo_items.into_iter().map").unwrap();
        let item = &source[start..source.len().min(start + 2_500)];
        assert!(item.contains(".min_w_0()"));
        assert!(item.contains(".whitespace_normal()"));
    }

    #[test]
    fn empty_terminal_list_starts_the_first_shell() {
        let source = include_str!("main.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        let start = source.find("PendingRequest::Terminals =>").unwrap();
        let response = &source[start..source.len().min(start + 1_500)];
        assert!(response.contains("request_create_terminal"));
    }

    #[test]
    fn embedded_terminal_accepts_input_in_the_output_surface() {
        let source = include_str!("main.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
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
        let source = include_str!("main.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
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
        let source = include_str!("main.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
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
        let main_source = include_str!("main.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        assert_eq!(
            main_source
                .matches("branch_picker_control(self, false, palette, locale, cx)")
                .count(),
            1,
            "new conversations still need a branch choice before the first turn"
        );
        assert!(!main_source.contains("header_project"));
        assert!(!main_source.contains("header_branch"));
        let surfaces_source = include_str!("surfaces.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
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
        expansion.toggle("run-1", false);
        assert!(expansion.is_expanded("run-1", false));
        expansion.toggle("run-1", false);
        assert!(!expansion.is_expanded("run-1", false));
    }

    #[test]
    fn running_process_can_override_its_default_expansion() {
        let mut expansion = ProcessExpansion::default();
        assert!(expansion.is_expanded("run", true));

        expansion.toggle("run", true);
        assert!(
            !expansion.is_expanded("run", true),
            "one click must collapse a process that was opened by its running default"
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
        let composer = include_str!("main.rs")
            .split("    fn composer_view(")
            .nth(1)
            .unwrap()
            .split("    fn ")
            .next()
            .unwrap();
        assert!(composer.contains(
            "(self.model_picker_open && self.route_picker_target.is_none())\n            .then(|| self.model_picker_view(palette, cx))"
        ));
        assert!(composer.contains(".when_some(picker, |shell, picker| shell.child(picker))"));
    }

    #[test]
    fn model_picker_blocks_scroll_from_reaching_the_background() {
        let picker = include_str!("main.rs")
            .split("    fn model_picker_view(")
            .nth(1)
            .unwrap()
            .split("    fn ")
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
        let source = include_str!("main.rs");
        let pointer_update = source
            .split("    fn update_reasoning_from_pointer(")
            .nth(1)
            .unwrap()
            .split("    fn ")
            .next()
            .unwrap();
        assert!(
            !pointer_update.contains("set_reasoning("),
            "pointer movement must not persist a discrete reasoning tier"
        );
        let release = source
            .split("    fn reasoning_mouse_up(")
            .nth(1)
            .expect("drag must have an explicit release handler")
            .split("    fn ")
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
            let labels =
                levels.map(|level| reasoning_display_name(level, Locale::resolve(language)));
            assert_eq!(labels, expected, "{language}");
        }
    }

    #[test]
    fn fast_lightning_stays_in_the_reasoning_heading_without_a_separate_row() {
        let picker = include_str!("main.rs")
            .split("    fn model_picker_view(")
            .nth(1)
            .unwrap()
            .split("    fn ")
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
        let picker = include_str!("main.rs")
            .split("    fn model_picker_view(")
            .nth(1)
            .unwrap()
            .split("    fn ")
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

        let popover = include_str!("main.rs")
            .split("    fn context_popover_view(")
            .nth(1)
            .unwrap()
            .split("    fn composer_view(")
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

        let unreported =
            context_composition(&profile, &json!({}), 20_000, Locale::resolve("zh-CN"));
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
}
