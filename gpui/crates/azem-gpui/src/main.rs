mod assets;
mod composer_completion;
mod fast_particles;
mod localization;
mod markdown;
mod model_selection;
mod runtime_connection;
mod selectable_text;
mod state;
mod surfaces;
mod terminal_emulator;
mod text_input;
mod theme;
mod window_actions;
mod window_composer;
mod window_controls;
mod window_model_picker;
mod window_render;
mod window_runtime;
mod window_settings;
mod window_terminal;

#[cfg(test)]
mod composer_tests;
#[cfg(test)]
const MAIN_SOURCE: &str = concat!(
    include_str!("window_runtime.rs"),
    include_str!("window_actions.rs"),
    include_str!("window_controls.rs"),
    include_str!("window_model_picker.rs"),
    include_str!("window_settings.rs"),
    include_str!("window_composer.rs"),
    include_str!("window_terminal.rs"),
    include_str!("window_render.rs"),
    include_str!("main.rs"),
);

#[cfg(test)]
const SURFACES_SOURCE: &str = concat!(
    include_str!("surfaces/navigation.rs"),
    include_str!("surfaces/environment.rs"),
    include_str!("surfaces/settings/mod.rs"),
    include_str!("surfaces/settings/routes.rs"),
    include_str!("surfaces/settings/subagents.rs"),
    include_str!("surfaces/settings/catalog.rs"),
    include_str!("surfaces/settings/governance.rs"),
    include_str!("surfaces/settings/appearance.rs"),
    include_str!("surfaces/settings/extensions.rs"),
    include_str!("surfaces/settings/security.rs"),
    include_str!("surfaces/settings/archive.rs"),
    include_str!("surfaces/settings/usage.rs"),
    include_str!("surfaces/projects.rs"),
    include_str!("surfaces/security.rs"),
    include_str!("surfaces/pull_requests.rs"),
    include_str!("surfaces/workspace.rs"),
    include_str!("surfaces/timeline.rs"),
    include_str!("surfaces/timeline/process.rs"),
    include_str!("surfaces.rs"),
);

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
    App, Bounds, BoxShadow, ClickEvent, ClipboardEntry, ClipboardItem, Context, Entity,
    ExternalPaths, FocusHandle, Focusable, FollowMode, ImageFormat, KeyBinding, ListAlignment,
    ListState, MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent, ObjectFit,
    PathPromptOptions, Pixels, PromptButton, PromptLevel, Role, ScrollHandle, Size, StyledImage,
    Task, UniformListScrollHandle, Window, WindowBounds, WindowHandle, WindowOptions, div, hsla,
    img, list, prelude::*, px, relative, rgb, rgba, size, svg,
};
use gpui_platform::application;
use localization::{Labels, Locale, labels};
use model_selection::{ModelModes, model_choices, model_modes, variant_selection};
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
        CloseOverlay,
        Quit
    ]
);

const TRANSCRIPT_COMPOSER_CLEARANCE: f32 = 64.;
const PROCESS_ACTIVITY_ROLL_DURATION: Duration = Duration::from_millis(220);
const SIDE_PANEL_DEFAULT_WIDTH: f32 = 420.;
const SIDE_PANEL_MIN_WIDTH: f32 = 260.;
const MAIN_TEXT_MIN_WIDTH: f32 = 640.;
const ENVIRONMENT_PANEL_RESERVED_WIDTH: f32 = 312.;
const CHAT_COLUMN_MAX_WIDTH: f32 = 736.;
const CHAT_COLUMN_GUTTER: f32 = 12.;
const CHAT_COLUMN_GUTTER_WIDE: f32 = 20.;

// Synara changes only opacity when entering a conversation; keep transcript
// geometry and scroll anchoring stable throughout the transition.
#[derive(Default)]
struct SurfaceMotion {
    key: Option<(Surface, String)>,
    started: Option<Instant>,
}
impl SurfaceMotion {
    fn opacity(&mut self, surface: Surface, session: &str, reduced: bool, now: Instant) -> f32 {
        let changed = self
            .key
            .as_ref()
            .is_none_or(|key| key.0 != surface || key.1 != session);
        if changed {
            self.started = self.key.as_ref().map(|_| now);
            self.key = Some((surface, session.to_string()));
        }
        if reduced {
            self.started = None;
        }
        let Some(started) = self.started else {
            return 1.;
        };
        let progress = now.saturating_duration_since(started).as_secs_f32() / 0.140;
        if progress >= 1. {
            self.started = None;
            return 1.;
        }
        css_ease_out(progress)
    }
}

#[derive(Clone, Copy, Default)]
struct PopupMotion {
    opacity: f32,
    from: f32,
    open: bool,
    started: Option<Instant>,
}
impl PopupMotion {
    fn update(&mut self, open: bool, reduced: bool, now: Instant) -> bool {
        self.advance(now);
        if self.open != open {
            self.from = self.opacity;
            self.open = open;
            self.started = Some(now);
        }
        if reduced {
            self.opacity = if open { 1. } else { 0. };
            self.started = None;
        }
        self.started.is_some()
    }
    fn advance(&mut self, now: Instant) {
        let Some(started) = self.started else {
            return;
        };
        let progress = (now.saturating_duration_since(started).as_secs_f32() / 0.2).min(1.);
        let target = if self.open { 1. } else { 0. };
        self.opacity = self.from + (target - self.from) * css_bezier(progress, 0.42, 0., 0.58, 1.);
        if progress >= 1. {
            self.opacity = target;
            self.started = None;
        }
    }
    fn visible(self) -> bool {
        self.open || self.opacity > 0.
    }
}

fn css_ease_out(progress: f32) -> f32 {
    css_bezier(progress, 0., 0., 0.58, 1.)
}

fn css_bezier(progress: f32, x1: f32, y1: f32, x2: f32, y2: f32) -> f32 {
    if progress <= 0. {
        return 0.;
    }
    if progress >= 1. {
        return 1.;
    }
    let coordinate = |t: f32, a: f32, b: f32| {
        3. * (1. - t).powi(2) * t * a + 3. * (1. - t) * t * t * b + t.powi(3)
    };
    let (mut low, mut high) = (0., 1.);
    for _ in 0..16 {
        let t = (low + high) / 2.;
        if coordinate(t, x1, x2) < progress {
            low = t;
        } else {
            high = t;
        }
    }
    coordinate((low + high) / 2., y1, y2)
}

fn eased_side_panel_width(from: f32, to: f32, elapsed: Duration) -> f32 {
    let progress = (elapsed.as_secs_f32() / SIDE_PANEL_TRANSITION.as_secs_f32()).clamp(0., 1.);
    let eased = css_bezier(progress, 0.32, 0.72, 0., 1.);
    from + (to - from) * eased
}

fn workspace_width(window_width: f32) -> f32 {
    (window_width - SIDEBAR_WIDTH).max(0.)
}

fn side_panel_max_width(workspace_width: f32) -> Option<f32> {
    let available = workspace_width - MAIN_TEXT_MIN_WIDTH;
    (available >= SIDE_PANEL_MIN_WIDTH).then_some(available)
}

fn side_panel_width_for_workspace(workspace_width: f32, preferred: Option<f32>) -> Option<f32> {
    let desired = preferred
        .filter(|width| width.is_finite() && *width > 0.)
        .unwrap_or(workspace_width / 2.);
    side_panel_max_width(workspace_width)
        .map(|maximum| desired.clamp(SIDE_PANEL_MIN_WIDTH, maximum))
}

fn environment_panel_fits(workspace_width: f32, side_panel_width: f32) -> bool {
    workspace_width - side_panel_width - ENVIRONMENT_PANEL_RESERVED_WIDTH >= MAIN_TEXT_MIN_WIDTH
}

struct EnvironmentPanelLayout {
    right_inset: f32,
    tuck_progress: f32,
    opacity: f32,
    reserved_width: f32,
}

fn environment_panel_layout(
    workspace_width: f32,
    panel_width: f32,
    visible_width: f32,
    alongside_visibility: f32,
    environment_visibility: f32,
) -> Option<EnvironmentPanelLayout> {
    if environment_visibility <= 0. || !environment_panel_fits(workspace_width, 0.) {
        return None;
    }
    let visible_width = visible_width.clamp(0., panel_width);
    let visibility = environment_visibility.clamp(0., 1.)
        * if panel_width == 0. {
            1.
        } else {
            1. - (visible_width / panel_width) * (1. - alongside_visibility.clamp(0., 1.))
        };
    let tuck_progress = 1. - visibility;
    Some(EnvironmentPanelLayout {
        right_inset: visible_width,
        tuck_progress,
        // Keep the card readable as it slips under the foreground panel; fade at the end.
        opacity: 1. - tuck_progress.powi(3),
        // Use the same progress in both directions, including a reversed close.
        reserved_width: (visible_width + ENVIRONMENT_PANEL_RESERVED_WIDTH * visibility)
            .min(workspace_width - MAIN_TEXT_MIN_WIDTH),
    })
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
    OpenProject,
    WorkflowMode(String),
    ApprovalDecision {
        approval_id: String,
    },
    PromptQueueRefresh,
    PromptQueue {
        session_id: String,
        submission: Option<QueuedPrompt>,
        source_text: Option<String>,
    },
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
    CancelActive {
        session_id: String,
        run_id: String,
    },
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
    pending: bool,
    dispatching: bool,
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
    live_groups: HashMap<String, bool>,
    disclosures: HashMap<String, DisclosureMotion>,
    activity: HashMap<String, ProcessActivityRoll>,
}

struct DisclosureMotion {
    from: f32,
    target: f32,
    started: Instant,
    height: f32,
    index: usize,
}

impl DisclosureMotion {
    fn progress(&self, now: Instant) -> f32 {
        let elapsed = now.saturating_duration_since(self.started).as_secs_f32() / 0.220;
        self.from + (self.target - self.from) * disclosure_ease(elapsed)
    }
    fn active(&self, now: Instant) -> bool {
        self.from != self.target
            && now.saturating_duration_since(self.started) < Duration::from_millis(220)
    }
}

fn disclosure_ease(progress: f32) -> f32 {
    if progress <= 0. {
        return 0.;
    }
    if progress >= 1. {
        return 1.;
    }
    // CSS ease-out: solve x for cubic-bezier(0, 0, .58, 1).
    let (mut low, mut high) = (0., 1.);
    for _ in 0..16 {
        let t = (low + high) / 2.;
        let x = 1.74 * (1. - t) * t * t + t * t * t;
        if x < progress {
            low = t;
        } else {
            high = t;
        }
    }
    let t = (low + high) / 2.;
    3. * (1. - t) * t * t + t * t * t
}

#[derive(Default)]
struct ProcessActivityRoll {
    current: String,
    previous: Option<String>,
    revision: usize,
    changed_at: Option<Instant>,
}

impl ProcessExpansion {
    fn is_expanded(&self, key: &str) -> bool {
        self.open.contains(key)
    }

    fn toggle(&mut self, key: &str) {
        if !self.open.remove(key) {
            self.open.insert(key.to_string());
        }
    }

    fn group_is_expanded(&mut self, key: &str, running: bool) -> bool {
        if self.live_groups.insert(key.to_string(), running) != Some(running) {
            if running {
                self.open.insert(key.to_string());
            } else {
                self.open.remove(key);
            }
        }
        self.is_expanded(key)
    }

    fn disclosure(
        &mut self,
        key: &str,
        index: usize,
        open: bool,
        reduced: bool,
    ) -> (f32, f32, bool) {
        let now = Instant::now();
        let target = if open { 1. } else { 0. };
        let motion = self
            .disclosures
            .entry(key.to_string())
            .or_insert(DisclosureMotion {
                from: target,
                target,
                started: now,
                height: 0.,
                index,
            });
        if motion.target != target {
            motion.from = motion.progress(now);
            motion.target = target;
            motion.started = now;
        }
        if reduced {
            motion.from = target;
        }
        motion.index = index;
        (motion.progress(now), motion.height, motion.active(now))
    }

    fn activity_transition(&mut self, key: &str, label: &str) -> (Option<String>, usize) {
        if !self.activity.contains_key(key) {
            self.activity
                .insert(key.to_string(), ProcessActivityRoll::default());
        }
        let activity = self.activity.get_mut(key).expect("activity was inserted");
        if activity.current.is_empty() {
            activity.current = label.to_string();
        } else if activity.current != label {
            activity.previous = Some(std::mem::replace(&mut activity.current, label.to_string()));
            activity.revision = activity.revision.wrapping_add(1);
            activity.changed_at = Some(Instant::now());
        } else if activity
            .changed_at
            .is_some_and(|changed_at| changed_at.elapsed() >= PROCESS_ACTIVITY_ROLL_DURATION)
        {
            activity.previous = None;
            activity.changed_at = None;
        }
        (activity.previous.clone(), activity.revision)
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
    provider_base_url: Entity<TextInput>,
    provider_api_key: Entity<TextInput>,
    catalog_loaded_id: Option<String>,
    catalog_enabled: bool,
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
    surface_motion: SurfaceMotion,
    popup_motions: HashMap<&'static str, PopupMotion>,
    sidebar_open: bool,
    sidebar_width: f32,
    sidebar_visible_width: f32,
    sidebar_animation: Option<(Instant, f32)>,
    sidebar_resize_drag: Option<SidePanelResizeDrag>,
    sidebar_resize_focus: FocusHandle,
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
    model_picker_render_target: Option<RoutePickerTarget>,
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
    side_panel_open: bool,
    side_panel_agents_open: bool,
    side_panel_add_menu_open: bool,
    side_panel_closing: bool,
    side_panel_width: f32,
    session_side_panel_widths: HashMap<String, f32>,
    side_panel_visible_width: f32,
    side_panel_animation_started: Option<Instant>,
    side_panel_animation_from: f32,
    side_panel_resize_drag: Option<SidePanelResizeDrag>,
    terminal_open: bool,
    terminal_input: Entity<TextInput>,
    terminal_scroll: ScrollHandle,
    workspace_changes_scroll: gpui::UniformListScrollHandle,
    workspace_source: surfaces::WorkspaceSource,
    workspace_overview_scroll: gpui::UniformListScrollHandle,
    workspace_tree_scroll: ScrollHandle,
    workspace_preview_scroll: ScrollHandle,
    workspace_image: Option<std::sync::Arc<gpui::Image>>,
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
    sidebar_tree: SidebarTreeState,
    window_state_path: Option<PathBuf>,
    window_size: Size<Pixels>,
    window_placement: Option<WindowPlacement>,
    _connection_task: Task<()>,
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
        self.persist_window_state();
        self.runtime.detach();
    }
}

impl AzemWindow {
    fn persist_window_state(&self) {
        let _ = self
            .window_state_path
            .as_deref()
            .map(|path| {
                save_window_size(
                    path,
                    self.window_size,
                    self.window_placement.as_ref(),
                    &self.session_side_panel_widths,
                )
            })
            .transpose()
            .inspect_err(|error| tracing::warn!(%error, "persist GPUI window placement"));
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

fn image_mime_from_extension(path: &std::path::Path) -> Option<&'static str> {
    match path
        .extension()
        .and_then(|extension| extension.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase()
        .as_str()
    {
        "png" => Some("image/png"),
        "jpg" | "jpeg" => Some("image/jpeg"),
        "gif" => Some("image/gif"),
        "webp" => Some("image/webp"),
        _ => None,
    }
}

fn image_mime_from_format(format: ImageFormat) -> Option<&'static str> {
    match format {
        ImageFormat::Png | ImageFormat::Jpeg | ImageFormat::Gif | ImageFormat::Webp => {
            Some(format.mime_type())
        }
        _ => None,
    }
}

pub(crate) fn block_attachments(block: &state::Block) -> &[serde_json::Value] {
    match block.extra.get("attachments") {
        Some(serde_json::Value::Array(items)) => items,
        _ => &[],
    }
}

pub(crate) fn attachment_preview_path(attachment: &serde_json::Value) -> Option<&str> {
    attachment
        .get("path")
        .and_then(serde_json::Value::as_str)
        .map(str::trim)
        .filter(|path| !path.is_empty())
}

pub(crate) fn attachment_image_preview(
    path: Option<&str>,
    palette: ThemePalette,
    icon_size: f32,
) -> gpui::AnyElement {
    match path {
        Some(path) if path.starts_with("data:image/") => img(path.to_string())
            .size_full()
            .object_fit(ObjectFit::Contain)
            .into_any_element(),
        Some(path) => img(PathBuf::from(path))
            .size_full()
            .object_fit(ObjectFit::Contain)
            .into_any_element(),
        None => div()
            .size_full()
            .flex()
            .items_center()
            .justify_center()
            .child(icon("image", icon_size, palette.faint))
            .into_any_element(),
    }
}

fn runtime_busy(state: &AppState) -> bool {
    state.runtime.running || !state.runtime.active_session_id.is_empty()
}

fn pending_question_run_id(state: &AppState) -> Option<&str> {
    let session_id = state.navigation.current_session_id.as_ref();
    state.runtime.questions.iter().find_map(|question| {
        let question_session = question
            .get("sessionId")
            .and_then(serde_json::Value::as_str)?;
        let question_state = question.get("state").and_then(serde_json::Value::as_str)?;
        (question_session == session_id && matches!(question_state, "pending" | "interrupted"))
            .then(|| question.get("runId").and_then(serde_json::Value::as_str))
            .flatten()
            .filter(|run_id| !run_id.is_empty())
    })
}

fn stoppable_run(state: &AppState) -> Option<(String, String)> {
    let session_id = state.navigation.current_session_id.as_ref();
    if session_id.is_empty() {
        return None;
    }
    if !state.runtime.running {
        let run_id = pending_question_run_id(state)?;
        return Some((session_id.to_string(), run_id.to_string()));
    }
    let run_id = if state.runtime.run_id.is_empty() {
        String::new()
    } else {
        state.runtime.run_id.to_string()
    };
    Some((session_id.to_string(), run_id))
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

#[cfg(test)]
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
    if tokens >= 1_000_000 {
        let millions = format!("{:.2}", tokens as f64 / 1_000_000.);
        return format!("{}M", millions.trim_end_matches('0').trim_end_matches('.'));
    }
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

fn selected_model_display_name(
    providers: &[serde_json::Value],
    provider_id: &str,
    model_id: &str,
) -> String {
    providers
        .iter()
        .find(|provider| provider["id"].as_str() == Some(provider_id))
        .and_then(|provider| {
            model_choices(provider, model_id, "")
                .into_iter()
                .find(|choice| choice.selected)
        })
        .map(|choice| {
            choice
                .family_name
                .unwrap_or_else(|| catalog_model_name(choice.model, model_id))
        })
        .unwrap_or_else(|| humanize_model_id(model_id))
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

fn model_reasoning_display_name(modes: &ModelModes, fallback: &str, locale: Locale) -> String {
    let mut label = reasoning_display_name(
        if modes.reasoning.is_empty() {
            fallback
        } else {
            &modes.reasoning
        },
        locale,
    );
    if modes.fast {
        label.push_str(" · ");
        label.push_str(locale.text("model.fast"));
    }
    label
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

#[derive(Clone, Debug, serde::Serialize, serde::Deserialize)]
struct WindowPlacement {
    uuid: String,
    x: f32,
    y: f32,
}

fn current_window_placement(window: &Window, cx: &App) -> Option<WindowPlacement> {
    let display = window.display(cx)?;
    let origin = window.window_bounds().get_bounds().origin - display.bounds().origin;
    Some(WindowPlacement {
        uuid: display.uuid().ok()?.to_string(),
        x: origin.x.into(),
        y: origin.y.into(),
    })
}

fn restored_window_bounds(
    screen: Bounds<Pixels>,
    window_size: Size<Pixels>,
    placement: Option<&WindowPlacement>,
) -> Bounds<Pixels> {
    let fitted = window_size.min(&screen.size);
    let max_x = f32::from(screen.size.width - fitted.width).max(0.);
    let max_y = f32::from(screen.size.height - fitted.height).max(0.);
    let (x, y) = placement
        .filter(|saved| saved.x.is_finite() && saved.y.is_finite())
        .map(|saved| (saved.x.clamp(0., max_x), saved.y.clamp(0., max_y)))
        .unwrap_or((max_x / 2., max_y / 2.));
    Bounds::new(screen.origin + gpui::point(px(x), px(y)), fitted)
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

fn load_session_side_panel_widths(path: &std::path::Path) -> HashMap<String, f32> {
    let widths = (|| -> anyhow::Result<HashMap<String, f32>> {
        let value: serde_json::Value = serde_json::from_slice(&fs::read(path)?)?;
        Ok(value["sessionSidePanelWidths"]
            .as_object()
            .into_iter()
            .flatten()
            .filter_map(|(session, value)| {
                let width = value.as_f64()? as f32;
                (!session.is_empty() && width.is_finite() && width > 0.)
                    .then(|| (session.clone(), width))
            })
            .collect())
    })();
    widths.unwrap_or_else(|error| {
        if path.exists() {
            tracing::warn!(%error, "load GPUI session panel widths");
        }
        HashMap::new()
    })
}

fn save_window_size(
    path: &std::path::Path,
    window_size: Size<Pixels>,
    placement: Option<&WindowPlacement>,
    session_side_panel_widths: &HashMap<String, f32>,
) -> std::io::Result<()> {
    fs::create_dir_all(path.parent().unwrap_or_else(|| std::path::Path::new(".")))?;
    let temporary = path.with_extension("json.tmp");
    fs::write(
        &temporary,
        serde_json::to_vec(&json!({
            "width": f32::from(window_size.width),
            "height": f32::from(window_size.height),
            "display": placement,
            "sessionSidePanelWidths": session_side_panel_widths,
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
    let placement = state_path
        .as_deref()
        .and_then(|path| fs::read_to_string(path).ok())
        .and_then(|text| serde_json::from_str::<serde_json::Value>(&text).ok())
        .and_then(|value| serde_json::from_value::<WindowPlacement>(value["display"].clone()).ok());
    let saved_display = placement.as_ref().and_then(|saved| {
        cx.displays().into_iter().find(|display| {
            display
                .uuid()
                .is_ok_and(|uuid| uuid.to_string() == saved.uuid)
        })
    });
    let restored_placement = saved_display.as_ref().and(placement.as_ref());
    let display = saved_display.or_else(|| cx.primary_display());
    let bounds = display.as_ref().map_or_else(
        || Bounds::centered(None, window_size, cx),
        |display| restored_window_bounds(display.bounds(), window_size, restored_placement),
    );
    cx.open_window(
        WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            display_id: display.as_ref().map(|display| display.id()),
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
                cx.observe_window_bounds(window, |this: &mut AzemWindow, window, cx| {
                    let size = window.window_bounds().get_bounds().size;
                    this.window_size = size;
                    this.window_placement = current_window_placement(window, cx);
                })
                .detach();
                cx.observe_window_activation(window, |this: &mut AzemWindow, window, cx| {
                    if !window.is_window_active() {
                        this.reasoning_drag = None;
                        this.finish_side_panel_resize();
                        this.finish_sidebar_resize(cx);
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
        .with_quit_mode(gpui::QuitMode::LastWindowClosed);
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
        selectable_text::init(cx);
        cx.on_action(|_: &Quit, cx| cx.quit());
        cx.bind_keys([
            KeyBinding::new("cmd-q", Quit, None),
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
