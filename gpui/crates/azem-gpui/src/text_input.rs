use std::ops::Range;
use std::time::{Duration, Instant};

use gpui::{
    App, Bounds, ClipboardItem, ContentMask, Context, CursorStyle, ElementId, ElementInputHandler,
    Entity, EntityInputHandler, FocusHandle, Focusable, GlobalElementId, KeyBinding, LayoutId,
    MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent, PaintQuad, Pixels, Point, Role,
    ScrollWheelEvent, SharedString, Style, Task, TextRun, UTF16Selection, UnderlineStyle, Window,
    WrappedLine, actions, div, fill, point, prelude::*, px, relative, rgba, size,
};
use unicode_segmentation::*;

use crate::theme::{AppearancePreferences, ThemePalette};

actions!(
    text_input,
    [
        Backspace,
        Delete,
        Left,
        Right,
        SelectLeft,
        SelectRight,
        SelectAll,
        Home,
        End,
        ShowCharacterPalette,
        Paste,
        Cut,
        Copy,
        Newline,
    ]
);

pub fn init(cx: &mut App) {
    cx.bind_keys([
        KeyBinding::new("backspace", Backspace, Some("TextInput")),
        KeyBinding::new("delete", Delete, Some("TextInput")),
        KeyBinding::new("left", Left, Some("TextInput")),
        KeyBinding::new("right", Right, Some("TextInput")),
        KeyBinding::new("shift-left", SelectLeft, Some("TextInput")),
        KeyBinding::new("shift-right", SelectRight, Some("TextInput")),
        KeyBinding::new("cmd-a", SelectAll, Some("TextInput")),
        KeyBinding::new("cmd-v", Paste, Some("TextInput")),
        KeyBinding::new("cmd-c", Copy, Some("TextInput")),
        KeyBinding::new("cmd-x", Cut, Some("TextInput")),
        KeyBinding::new("home", Home, Some("TextInput")),
        KeyBinding::new("end", End, Some("TextInput")),
        KeyBinding::new("ctrl-cmd-space", ShowCharacterPalette, Some("TextInput")),
        KeyBinding::new("shift-enter", Newline, Some("TextInput")),
    ]);
}

pub struct TextInput {
    focus_handle: FocusHandle,
    content: SharedString,
    references: Vec<InlineReference>,
    placeholder: SharedString,
    show_placeholder: bool,
    selected_range: Range<usize>,
    selection_reversed: bool,
    marked_range: Option<Range<usize>>,
    ime_committed_at: Option<Instant>,
    last_layout: Option<TextLayout>,
    last_bounds: Option<Bounds<Pixels>>,
    is_selecting: bool,
    reveal_cursor: bool,
    multiline: bool,
    compact: bool,
    terminal: bool,
    password: bool,
    cursor_visible: bool,
    blink_task: Option<Task<()>>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct InlineReference {
    // UTF-8 range in the displayed text, not in the serialized @path.
    range: Range<usize>,
    source: String,
    glyph: &'static str,
}

fn file_label(path: &str) -> String {
    let name = path.rsplit('/').next().unwrap_or(path);
    // Reserve one em for the SVG. The joiner keeps it attached to the filename.
    let name: String = name
        .chars()
        .map(|ch| if ch.is_control() { '�' } else { ch })
        .collect();
    format!("\u{2003}\u{2060}{name}")
}

pub(super) fn file_icon(path: &str) -> &'static str {
    let name = std::path::Path::new(path)
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    match name.as_str() {
        ".gitignore" | ".gitattributes" | ".gitmodules" => return "git-branch",
        "makefile" | "gnumakefile" | "justfile" | "dockerfile" | "containerfile" => {
            return "terminal";
        }
        "go.mod" | "go.sum" | "cargo.lock" | "bun.lock" | "yarn.lock" | "package-lock.json"
        | "pnpm-lock.yaml" => return "box",
        ".env" => return "settings",
        _ if name.starts_with(".env.") => return "settings",
        _ => {}
    }
    let extension = std::path::Path::new(path)
        .extension()
        .and_then(|ext| ext.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    match extension.as_str() {
        "rs" | "go" | "js" | "jsx" | "ts" | "tsx" | "py" | "rb" | "java" | "kt" | "swift" | "c"
        | "h" | "cpp" | "hpp" | "cs" | "php" | "html" | "css" | "scss" | "vue" | "svelte" => {
            "file-code"
        }
        "md" | "markdown" | "mdown" => "notebook-pen",
        "json" | "jsonc" | "yaml" | "yml" | "toml" | "xml" | "ini" => "braces",
        "png" | "jpg" | "jpeg" | "gif" | "webp" | "svg" | "ico" | "heic" => "image",
        "mp3" | "wav" | "flac" | "m4a" | "ogg" | "mp4" | "mov" | "webm" => "audio-lines",
        "sh" | "bash" | "zsh" | "fish" | "ps1" | "bat" | "cmd" => "terminal",
        "sql" | "db" | "sqlite" | "sqlite3" => "database",
        "zip" | "tar" | "gz" | "bz2" | "xz" | "7z" | "rar" => "archive",
        "ttf" | "otf" | "woff" | "woff2" => "type",
        _ => "file-text",
    }
}

pub(super) fn file_reference(path: &str) -> String {
    if path
        .chars()
        .any(|ch| ch.is_whitespace() || matches!(ch, '"' | '\\'))
    {
        format!("@{}", serde_json::json!(path))
    } else {
        format!("@{path}")
    }
}

pub(super) fn session_reference(id: &str, title: &str) -> String {
    let title = title
        .replace(['\n', '\r'], " ")
        .replace('\\', "\\\\")
        .replace(']', "\\]");
    format!("@[{title}](azem-session:{id})")
}

pub(super) fn session_reference_ranges(text: &str) -> Vec<(Range<usize>, String)> {
    let mut references = Vec::new();
    let mut offset = 0;
    while let Some(start) = text[offset..].find("@[").map(|start| start + offset) {
        let mut title = String::new();
        let mut escaped = false;
        let mut found = None;
        for (index, ch) in text[start + 2..].char_indices() {
            if ch == '\n' || ch == '\r' {
                break;
            }
            if escaped {
                title.push(ch);
                escaped = false;
            } else if ch == '\\' {
                escaped = true;
            } else if ch == ']' {
                let tail_start = start + 2 + index;
                if let Some(tail) = text[tail_start..].strip_prefix("](azem-session:")
                    && let Some(end) = tail.find(')')
                {
                    let id = &tail[..end];
                    if !id.is_empty()
                        && id.len() <= 128
                        && id
                            .bytes()
                            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
                    {
                        found = Some(tail_start + "](azem-session:".len() + end + 1);
                    }
                }
                break;
            } else {
                title.push(ch);
            }
        }
        offset = found.unwrap_or(start + 2);
        if let Some(end) = found {
            references.push((start..end, title));
        }
    }
    references
}

pub(super) fn display_session_references(text: &str) -> String {
    let mut result = text.to_owned();
    for (range, title) in session_reference_ranges(text).into_iter().rev() {
        result.replace_range(range, &format!("@{title}"));
    }
    result
}

fn expand_file_range(files: &[InlineReference], mut range: Range<usize>) -> Range<usize> {
    for file in files {
        if range.is_empty() {
            if file.range.start < range.start && range.start < file.range.end {
                return file.range.end..file.range.end;
            }
        } else if range.start < file.range.end && range.end > file.range.start {
            range.start = range.start.min(file.range.start);
            range.end = range.end.max(file.range.end);
        }
    }
    range
}

fn reference_source(text: &str, files: &[InlineReference], range: Range<usize>) -> String {
    let range = expand_file_range(files, clamp_byte_range(text, range));
    let mut source = String::new();
    let mut offset = range.start;
    for file in files
        .iter()
        .filter(|file| file.range.start >= range.start && file.range.end <= range.end)
    {
        source.push_str(&text[offset..file.range.start]);
        source.push_str(&file.source);
        offset = file.range.end;
    }
    source.push_str(&text[offset..range.end]);
    source
}

// All typing, paste, cut and IME edits go through the same atomic-span update.
fn replace_input_text(
    content: &mut SharedString,
    files: &mut Vec<InlineReference>,
    range: Range<usize>,
    text: &str,
) -> Range<usize> {
    let range = expand_file_range(files, clamp_byte_range(content, range));
    files.retain_mut(|file| {
        if file.range.end <= range.start {
            true
        } else if file.range.start >= range.end {
            file.range = file.range.start - range.len() + text.len()
                ..file.range.end - range.len() + text.len();
            true
        } else {
            false
        }
    });
    *content = (content[..range.start].to_owned() + text + &content[range.end..]).into();
    range
}

fn utf8_offset_from_utf16(text: &str, offset: usize) -> usize {
    let mut utf8_offset = 0;
    let mut utf16_count = 0;
    for character in text.chars() {
        if utf16_count >= offset {
            break;
        }
        utf16_count += character.len_utf16();
        utf8_offset += character.len_utf8();
    }
    utf8_offset
}

fn clamp_byte_range(text: &str, range: Range<usize>) -> Range<usize> {
    fn floor_boundary(text: &str, offset: usize) -> usize {
        let mut offset = offset.min(text.len());
        while offset > 0 && !text.is_char_boundary(offset) {
            offset -= 1;
        }
        offset
    }
    let start = floor_boundary(text, range.start);
    let end = floor_boundary(text, range.end).max(start);
    start..end
}

fn caret_visible(focused: bool, window_active: bool, blink_visible: bool) -> bool {
    focused && window_active && blink_visible
}

impl TextInput {
    pub fn new(
        cx: &mut Context<Self>,
        window: &mut Window,
        placeholder: impl Into<SharedString>,
    ) -> Self {
        let focus_handle = cx.focus_handle();
        cx.on_focus(&focus_handle, window, Self::sync_caret_focus)
            .detach();
        cx.on_blur(&focus_handle, window, Self::sync_caret_focus)
            .detach();
        cx.observe_window_activation(window, Self::sync_caret_focus)
            .detach();
        Self {
            focus_handle,
            content: "".into(),
            references: Vec::new(),
            placeholder: placeholder.into(),
            show_placeholder: true,
            selected_range: 0..0,
            selection_reversed: false,
            marked_range: None,
            ime_committed_at: None,
            last_layout: None,
            last_bounds: None,
            is_selecting: false,
            reveal_cursor: true,
            multiline: false,
            compact: false,
            terminal: false,
            password: false,
            cursor_visible: false,
            blink_task: None,
        }
    }

    fn sync_caret_focus(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.focus_handle.is_focused(window) && window.is_window_active() {
            self.restart_blink(cx);
        } else {
            self.blink_task = None;
            self.cursor_visible = false;
            self.is_selecting = false;
            cx.notify();
        }
    }

    fn restart_blink(&mut self, cx: &mut Context<Self>) {
        self.cursor_visible = true;
        self.blink_task = Some(cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(500))
                    .await;
                if this
                    .update(cx, |input, cx| {
                        input.cursor_visible = !input.cursor_visible;
                        cx.notify();
                    })
                    .is_err()
                {
                    break;
                }
            }
        }));
        cx.notify();
    }

    fn notify_edit(&mut self, cx: &mut Context<Self>) {
        self.reveal_cursor = true;
        if self.blink_task.is_some() {
            self.restart_blink(cx);
        } else {
            cx.notify();
        }
    }

    pub fn multiline(mut self) -> Self {
        self.multiline = true;
        self
    }

    pub fn compact(mut self) -> Self {
        self.compact = true;
        self
    }

    pub fn password(mut self) -> Self {
        self.password = true;
        self
    }

    pub fn terminal(mut self) -> Self {
        self.terminal = true;
        self.show_placeholder = false;
        self
    }

    pub fn text(&self) -> &str {
        &self.content
    }

    pub fn submission_text(&self) -> String {
        reference_source(&self.content, &self.references, 0..self.content.len())
    }

    pub fn insert_file_reference(
        &mut self,
        range: Range<usize>,
        path: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.insert_reference(
            range,
            file_label(path),
            file_reference(path),
            file_icon(path),
            window,
            cx,
        );
    }

    pub fn insert_session_reference(
        &mut self,
        range: Range<usize>,
        id: &str,
        title: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let title = title.replace(['\n', '\r'], " ");
        self.insert_reference(
            range,
            format!("\u{2003}\u{2060}{title}"),
            session_reference(id, &title),
            "message-square-text",
            window,
            cx,
        );
    }

    fn insert_reference(
        &mut self,
        range: Range<usize>,
        label: String,
        source: String,
        glyph: &'static str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.replace_byte_range(range, &format!("{label} "), window, cx);
        let end = self.selected_range.end - 1;
        self.references.push(InlineReference {
            range: end - label.len()..end,
            source,
            glyph,
        });
        self.references
            .sort_by_key(|reference| reference.range.start);
    }

    pub fn completion_cursor(&self) -> Option<usize> {
        (self.selected_range.is_empty() && self.marked_range.is_none())
            .then_some(self.cursor_offset())
    }

    pub fn is_composing(&self) -> bool {
        self.marked_range.is_some()
            || self
                .ime_committed_at
                .is_some_and(|at| at.elapsed() < Duration::from_millis(100))
    }

    pub fn has_marked_text(&self) -> bool {
        self.marked_range.is_some()
    }

    pub fn cursor_visible(&self) -> bool {
        self.cursor_visible
    }

    pub fn bounds(&self) -> Option<Bounds<Pixels>> {
        self.last_bounds
    }

    pub fn replace_byte_range(
        &mut self,
        range: Range<usize>,
        text: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let range = clamp_byte_range(&self.content, range);
        self.replace_text_in_range(Some(self.range_to_utf16(&range)), text, window, cx);
    }

    pub fn set_placeholder(
        &mut self,
        placeholder: impl Into<SharedString>,
        cx: &mut Context<Self>,
    ) {
        self.placeholder = placeholder.into();
        cx.notify();
    }

    pub fn clear(&mut self, cx: &mut Context<Self>) {
        self.set_text("", cx);
    }

    pub fn show_placeholder(&mut self, show: bool, cx: &mut Context<Self>) {
        if self.show_placeholder != show {
            self.show_placeholder = show;
            cx.notify();
        }
    }

    pub fn set_text(&mut self, text: &str, cx: &mut Context<Self>) {
        self.content = normalize_input_text(text, self.multiline).into();
        self.references.clear();
        let source = self.content.to_string();
        let references = if self.multiline && !self.terminal && !self.password {
            session_reference_ranges(&source)
        } else {
            Vec::new()
        };
        for (range, title) in references.into_iter().rev() {
            let label = format!("\u{2003}\u{2060}{title}");
            let start = range.start;
            replace_input_text(
                &mut self.content,
                &mut self.references,
                range.clone(),
                &label,
            );
            self.references.push(InlineReference {
                range: start..start + label.len(),
                source: source[range].to_owned(),
                glyph: "message-square-text",
            });
        }
        self.references
            .sort_by_key(|reference| reference.range.start);
        self.selected_range = self.content.len()..self.content.len();
        self.selection_reversed = false;
        self.marked_range = None;
        self.ime_committed_at = None;
        self.last_layout = None;
        self.notify_edit(cx);
    }

    fn left(&mut self, _: &Left, _: &mut Window, cx: &mut Context<Self>) {
        if self.selected_range.is_empty() {
            self.move_to(self.previous_boundary(self.cursor_offset()), cx);
        } else {
            self.move_to(self.selected_range.start, cx)
        }
    }

    fn right(&mut self, _: &Right, _: &mut Window, cx: &mut Context<Self>) {
        if self.selected_range.is_empty() {
            self.move_to(self.next_boundary(self.selected_range.end), cx);
        } else {
            self.move_to(self.selected_range.end, cx)
        }
    }

    fn select_left(&mut self, _: &SelectLeft, _: &mut Window, cx: &mut Context<Self>) {
        self.select_to(self.previous_boundary(self.cursor_offset()), cx);
    }

    fn select_right(&mut self, _: &SelectRight, _: &mut Window, cx: &mut Context<Self>) {
        self.select_to(self.next_boundary(self.cursor_offset()), cx);
    }

    fn select_all(&mut self, _: &SelectAll, _: &mut Window, cx: &mut Context<Self>) {
        self.move_to(0, cx);
        self.select_to(self.content.len(), cx)
    }

    fn home(&mut self, _: &Home, _: &mut Window, cx: &mut Context<Self>) {
        self.move_to(0, cx);
    }

    fn end(&mut self, _: &End, _: &mut Window, cx: &mut Context<Self>) {
        self.move_to(self.content.len(), cx);
    }

    fn backspace(&mut self, _: &Backspace, window: &mut Window, cx: &mut Context<Self>) {
        if self.selected_range.is_empty() {
            let prev = self.previous_boundary(self.cursor_offset());
            if self.cursor_offset() == prev {
                window.play_system_bell();
                return;
            }
            self.select_to(prev, cx)
        }
        self.replace_text_in_range(None, "", window, cx)
    }

    fn delete(&mut self, _: &Delete, window: &mut Window, cx: &mut Context<Self>) {
        if self.selected_range.is_empty() {
            let next = self.next_boundary(self.cursor_offset());
            if self.cursor_offset() == next {
                window.play_system_bell();
                return;
            }
            self.select_to(next, cx)
        }
        self.replace_text_in_range(None, "", window, cx)
    }

    fn on_mouse_down(
        &mut self,
        event: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.focus_handle.focus(window, cx);
        self.is_selecting = true;

        if event.modifiers.shift {
            self.select_to(self.index_for_mouse_position(event.position), cx);
        } else {
            self.move_to(self.index_for_mouse_position(event.position), cx)
        }
    }

    fn on_mouse_up(&mut self, _: &MouseUpEvent, _window: &mut Window, _: &mut Context<Self>) {
        self.is_selecting = false;
    }

    fn on_mouse_move(&mut self, event: &MouseMoveEvent, _: &mut Window, cx: &mut Context<Self>) {
        if self.is_selecting {
            self.select_to(self.index_for_mouse_position(event.position), cx);
        }
    }

    fn on_scroll_wheel(
        &mut self,
        event: &ScrollWheelEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self.multiline {
            return;
        }
        let (Some(layout), Some(bounds)) = (&mut self.last_layout, self.last_bounds) else {
            return;
        };
        if layout.height <= bounds.size.height {
            return;
        }
        let offset = input_scroll_offset(
            layout.scroll_y - event.delta.pixel_delta(layout.line_height).y,
            layout.height,
            bounds.size.height,
            None,
        );
        self.reveal_cursor = false;
        if offset != layout.scroll_y {
            layout.scroll_y = offset;
            cx.notify();
        }
        // A scrollable editor owns the gesture, including at its top/bottom edge.
        cx.stop_propagation();
    }

    fn show_character_palette(
        &mut self,
        _: &ShowCharacterPalette,
        window: &mut Window,
        _: &mut Context<Self>,
    ) {
        window.show_character_palette();
    }

    fn paste(&mut self, _: &Paste, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(text) = cx.read_from_clipboard().and_then(|item| item.text()) {
            self.replace_text_in_range(
                None,
                &normalize_input_text(&text, self.multiline),
                window,
                cx,
            );
        }
    }

    fn newline(&mut self, _: &Newline, window: &mut Window, cx: &mut Context<Self>) {
        if self.multiline {
            self.replace_text_in_range(None, "\n", window, cx);
        }
    }

    fn copy(&mut self, _: &Copy, _: &mut Window, cx: &mut Context<Self>) {
        if !self.selected_range.is_empty() {
            cx.write_to_clipboard(ClipboardItem::new_string(reference_source(
                &self.content,
                &self.references,
                self.selected_range.clone(),
            )));
        }
    }
    fn cut(&mut self, _: &Cut, window: &mut Window, cx: &mut Context<Self>) {
        if !self.selected_range.is_empty() {
            cx.write_to_clipboard(ClipboardItem::new_string(reference_source(
                &self.content,
                &self.references,
                self.selected_range.clone(),
            )));
            self.replace_text_in_range(None, "", window, cx)
        }
    }

    fn move_to(&mut self, offset: usize, cx: &mut Context<Self>) {
        let offset = self
            .references
            .iter()
            .find(|file| file.range.start < offset && offset < file.range.end)
            .map_or(offset, |file| {
                if offset - file.range.start < file.range.end - offset {
                    file.range.start
                } else {
                    file.range.end
                }
            });
        self.selected_range = offset..offset;
        self.selection_reversed = false;
        self.notify_edit(cx)
    }

    fn cursor_offset(&self) -> usize {
        if self.selection_reversed {
            self.selected_range.start
        } else {
            self.selected_range.end
        }
    }

    fn index_for_mouse_position(&self, position: Point<Pixels>) -> usize {
        if self.content.is_empty() {
            return 0;
        }

        let (Some(bounds), Some(line)) = (self.last_bounds.as_ref(), self.last_layout.as_ref())
        else {
            return 0;
        };
        if position.y < bounds.top() {
            return 0;
        }
        if position.y > bounds.bottom() {
            return self.content.len();
        }
        line.index_for_position(
            point(position.x - bounds.left(), position.y - bounds.top()),
            self.content.len(),
        )
    }

    fn select_to(&mut self, offset: usize, cx: &mut Context<Self>) {
        let anchor = if self.selection_reversed {
            self.selected_range.end
        } else {
            self.selected_range.start
        };
        let offset = self.file_boundary(offset, offset >= anchor);
        if self.selection_reversed {
            self.selected_range.start = offset
        } else {
            self.selected_range.end = offset
        };
        if self.selected_range.end < self.selected_range.start {
            self.selection_reversed = !self.selection_reversed;
            self.selected_range = self.selected_range.end..self.selected_range.start;
        }
        self.notify_edit(cx)
    }

    fn offset_from_utf16(&self, offset: usize) -> usize {
        utf8_offset_from_utf16(&self.content, offset)
    }

    fn offset_to_utf16(&self, offset: usize) -> usize {
        let mut utf16_offset = 0;
        let mut utf8_count = 0;

        for ch in self.content.chars() {
            if utf8_count >= offset {
                break;
            }
            utf8_count += ch.len_utf8();
            utf16_offset += ch.len_utf16();
        }

        utf16_offset
    }

    fn range_to_utf16(&self, range: &Range<usize>) -> Range<usize> {
        self.offset_to_utf16(range.start)..self.offset_to_utf16(range.end)
    }

    fn range_from_utf16(&self, range_utf16: &Range<usize>) -> Range<usize> {
        clamp_byte_range(
            &self.content,
            self.offset_from_utf16(range_utf16.start)..self.offset_from_utf16(range_utf16.end),
        )
    }

    fn previous_boundary(&self, offset: usize) -> usize {
        let previous = self
            .content
            .grapheme_indices(true)
            .rev()
            .find_map(|(idx, _)| (idx < offset).then_some(idx))
            .unwrap_or(0);
        self.file_boundary(previous, false)
    }

    fn next_boundary(&self, offset: usize) -> usize {
        let next = self
            .content
            .grapheme_indices(true)
            .find_map(|(idx, _)| (idx > offset).then_some(idx))
            .unwrap_or(self.content.len());
        self.file_boundary(next, true)
    }

    fn file_boundary(&self, offset: usize, forward: bool) -> usize {
        self.references
            .iter()
            .find(|file| file.range.start < offset && offset < file.range.end)
            .map_or(offset, |file| {
                if forward {
                    file.range.end
                } else {
                    file.range.start
                }
            })
    }
}

impl EntityInputHandler for TextInput {
    fn text_for_range(
        &mut self,
        range_utf16: Range<usize>,
        actual_range: &mut Option<Range<usize>>,
        _window: &mut Window,
        _cx: &mut Context<Self>,
    ) -> Option<String> {
        let range = self.range_from_utf16(&range_utf16);
        actual_range.replace(self.range_to_utf16(&range));
        Some(self.content[range].to_string())
    }

    fn selected_text_range(
        &mut self,
        _ignore_disabled_input: bool,
        _window: &mut Window,
        _cx: &mut Context<Self>,
    ) -> Option<UTF16Selection> {
        Some(UTF16Selection {
            range: self.range_to_utf16(&self.selected_range),
            reversed: self.selection_reversed,
        })
    }

    fn marked_text_range(
        &self,
        _window: &mut Window,
        _cx: &mut Context<Self>,
    ) -> Option<Range<usize>> {
        self.marked_range
            .as_ref()
            .map(|range| self.range_to_utf16(range))
    }

    fn unmark_text(&mut self, _window: &mut Window, cx: &mut Context<Self>) {
        if self.marked_range.take().is_some() {
            self.ime_committed_at = Some(Instant::now());
        }
        self.notify_edit(cx);
    }

    fn replace_text_in_range(
        &mut self,
        range_utf16: Option<Range<usize>>,
        new_text: &str,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let range = range_utf16
            .as_ref()
            .map(|range_utf16| self.range_from_utf16(range_utf16))
            .or(self.marked_range.clone())
            .unwrap_or(self.selected_range.clone());
        let range = replace_input_text(&mut self.content, &mut self.references, range, new_text);
        self.selected_range = range.start + new_text.len()..range.start + new_text.len();
        self.selection_reversed = false;
        if self.marked_range.take().is_some() {
            // macOS can deliver the confirmation Enter just after unmarking.
            self.ime_committed_at = Some(Instant::now());
        }
        self.notify_edit(cx);
    }

    fn replace_and_mark_text_in_range(
        &mut self,
        range_utf16: Option<Range<usize>>,
        new_text: &str,
        new_selected_range_utf16: Option<Range<usize>>,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let range = range_utf16
            .as_ref()
            .map(|range_utf16| self.range_from_utf16(range_utf16))
            .or(self.marked_range.clone())
            .unwrap_or(self.selected_range.clone());
        let range = replace_input_text(&mut self.content, &mut self.references, range, new_text);
        if !new_text.is_empty() {
            self.marked_range = Some(range.start..range.start + new_text.len());
        } else {
            self.marked_range = None;
        }
        let selection = new_selected_range_utf16
            .as_ref()
            .map(|selected| {
                let relative = clamp_byte_range(
                    new_text,
                    utf8_offset_from_utf16(new_text, selected.start)
                        ..utf8_offset_from_utf16(new_text, selected.end),
                );
                range.start + relative.start..range.start + relative.end
            })
            .unwrap_or_else(|| range.start + new_text.len()..range.start + new_text.len());
        self.selected_range = clamp_byte_range(&self.content, selection);
        self.selection_reversed = false;

        self.notify_edit(cx);
    }

    fn bounds_for_range(
        &mut self,
        range_utf16: Range<usize>,
        bounds: Bounds<Pixels>,
        _window: &mut Window,
        _cx: &mut Context<Self>,
    ) -> Option<Bounds<Pixels>> {
        let range = self.range_from_utf16(&range_utf16);
        let last_layout = self.last_layout.as_ref()?;
        let start = last_layout.position_for_index(range.start);
        let end = last_layout.position_for_index(range.end);
        Some(Bounds::from_corners(
            point(bounds.left() + start.x, bounds.top() + start.y),
            point(
                bounds.left() + end.x.max(start.x + px(1.)),
                bounds.top() + end.y + last_layout.line_height,
            ),
        ))
    }

    fn character_index_for_point(
        &mut self,
        point: gpui::Point<Pixels>,
        _window: &mut Window,
        _cx: &mut Context<Self>,
    ) -> Option<usize> {
        if self.content.is_empty() {
            return Some(0);
        }
        let line_point = self.last_bounds?.localize(&point)?;
        let last_layout = self.last_layout.as_ref()?;
        let utf8_index = last_layout.index_for_position(line_point, self.content.len());
        Some(self.offset_to_utf16(utf8_index))
    }
}

fn normalize_input_text(text: &str, multiline: bool) -> String {
    let normalized = text.replace("\r\n", "\n").replace('\r', "\n");
    if multiline {
        normalized
    } else {
        normalized.replace('\n', " ")
    }
}

fn input_scroll_offset(
    previous: Pixels,
    height: Pixels,
    viewport: Pixels,
    cursor: Option<Range<Pixels>>,
) -> Pixels {
    let maximum = (height - viewport).max(px(0.));
    let mut offset = previous.clamp(px(0.), maximum);
    if let Some(cursor) = cursor {
        if cursor.start < offset {
            offset = cursor.start.max(px(0.));
        } else if cursor.end > offset + viewport {
            offset = (cursor.end - viewport).clamp(px(0.), maximum);
        }
    }
    offset
}

struct TextLayout {
    lines: Vec<WrappedLine>,
    starts: Vec<usize>,
    tops: Vec<Pixels>,
    line_height: Pixels,
    scroll_y: Pixels,
    height: Pixels,
}

impl TextLayout {
    fn new(lines: Vec<WrappedLine>, line_height: Pixels) -> Self {
        let mut starts = Vec::with_capacity(lines.len());
        let mut tops = Vec::with_capacity(lines.len());
        let mut start = 0;
        let mut top = px(0.);
        for (index, line) in lines.iter().enumerate() {
            starts.push(start);
            tops.push(top);
            start += line.len();
            if index + 1 < lines.len() {
                start += 1;
            }
            top += line.size(line_height).height;
        }
        Self {
            lines,
            starts,
            tops,
            line_height,
            scroll_y: px(0.),
            height: top,
        }
    }

    fn raw_position_for_index(&self, index: usize) -> Point<Pixels> {
        for (line_index, line) in self.lines.iter().enumerate() {
            let start = self.starts[line_index];
            if index <= start + line.len() || line_index + 1 == self.lines.len() {
                let local = index.saturating_sub(start).min(line.len());
                let position = line
                    .position_for_index(local, self.line_height)
                    .unwrap_or_else(|| point(px(0.), px(0.)));
                return point(position.x, self.tops[line_index] + position.y);
            }
        }
        point(px(0.), px(0.))
    }

    fn position_for_index(&self, index: usize) -> Point<Pixels> {
        let position = self.raw_position_for_index(index);
        point(position.x, position.y - self.scroll_y)
    }

    fn index_for_position(&self, position: Point<Pixels>, content_len: usize) -> usize {
        let position = point(position.x, position.y + self.scroll_y);
        if position.y < px(0.) {
            return 0;
        }
        for (line_index, line) in self.lines.iter().enumerate() {
            let top = self.tops[line_index];
            let bottom = top + line.size(self.line_height).height;
            if position.y < bottom || line_index + 1 == self.lines.len() {
                let local = line
                    .closest_index_for_position(
                        point(position.x, (position.y - top).max(px(0.))),
                        self.line_height,
                    )
                    .unwrap_or_else(|index| index);
                return (self.starts[line_index] + local).min(content_len);
            }
        }
        content_len
    }

    fn selection(&self, range: Range<usize>, bounds: Bounds<Pixels>) -> Vec<PaintQuad> {
        let mut quads = Vec::new();
        for (line_index, line) in self.lines.iter().enumerate() {
            let line_start = self.starts[line_index];
            let line_end = line_start + line.len();
            if range.end < line_start || range.start > line_end {
                continue;
            }
            let start = range.start.saturating_sub(line_start).min(line.len());
            let end = range.end.saturating_sub(line_start).min(line.len());
            let Some(start_position) = line.position_for_index(start, self.line_height) else {
                continue;
            };
            let Some(end_position) = line.position_for_index(end, self.line_height) else {
                continue;
            };
            let first_row = (start_position.y / self.line_height) as usize;
            let last_row = (end_position.y / self.line_height) as usize;
            for row in first_row..=last_row {
                let left = if row == first_row {
                    start_position.x
                } else {
                    px(0.)
                };
                let right = if row == last_row {
                    end_position.x
                } else {
                    bounds.size.width
                };
                if right <= left {
                    continue;
                }
                quads.push(fill(
                    Bounds::new(
                        point(
                            bounds.left() + left,
                            bounds.top() + self.tops[line_index] - self.scroll_y
                                + self.line_height * row as f32,
                        ),
                        size(right - left, self.line_height),
                    ),
                    rgba(0x3311ff30),
                ));
            }
        }
        quads
    }
}

struct TextElement {
    input: Entity<TextInput>,
}

struct PrepaintState {
    layout: Option<TextLayout>,
    cursor: Option<PaintQuad>,
    selection: Vec<PaintQuad>,
}

impl IntoElement for TextElement {
    type Element = Self;

    fn into_element(self) -> Self::Element {
        self
    }
}

impl Element for TextElement {
    type RequestLayoutState = ();
    type PrepaintState = PrepaintState;

    fn id(&self) -> Option<ElementId> {
        None
    }

    fn source_location(&self) -> Option<&'static core::panic::Location<'static>> {
        None
    }

    fn request_layout(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&gpui::InspectorElementId>,
        window: &mut Window,
        cx: &mut App,
    ) -> (LayoutId, Self::RequestLayoutState) {
        let mut style = Style::default();
        style.size.width = relative(1.).into();
        style.size.height = if self.input.read(cx).multiline {
            relative(1.).into()
        } else {
            window.line_height().into()
        };
        (window.request_layout(style, [], cx), ())
    }

    fn prepaint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&gpui::InspectorElementId>,
        bounds: Bounds<Pixels>,
        _request_layout: &mut Self::RequestLayoutState,
        window: &mut Window,
        cx: &mut App,
    ) -> Self::PrepaintState {
        let input = self.input.read(cx);
        let content = input.content.clone();
        let selected_range = input.selected_range.clone();
        let cursor = input.cursor_offset();
        let style = window.text_style();

        let (display_text, text_color) = if content.is_empty() && input.show_placeholder {
            (
                input.placeholder.clone(),
                ThemePalette::for_window(window, cx).faint.into(),
            )
        } else if input.password {
            (password_mask(&content).into(), style.color)
        } else {
            (content, style.color)
        };

        let run = TextRun {
            len: display_text.len(),
            font: style.font(),
            color: text_color,
            background_color: None,
            underline: None,
            strikethrough: None,
        };
        let mut boundaries = vec![0, display_text.len()];
        for file in &input.references {
            boundaries.extend([file.range.start, file.range.end]);
        }
        if let Some(marked) = &input.marked_range {
            boundaries.extend([marked.start, marked.end]);
        }
        boundaries.sort_unstable();
        boundaries.dedup();
        let link_color = ThemePalette::for_window(window, cx).accent.into();
        let runs: Vec<TextRun> = boundaries
            .windows(2)
            .map(|range| {
                let color = if input
                    .references
                    .iter()
                    .any(|file| file.range.contains(&range[0]))
                {
                    link_color
                } else {
                    run.color
                };
                TextRun {
                    len: range[1] - range[0],
                    color,
                    underline: input
                        .marked_range
                        .as_ref()
                        .filter(|marked| marked.contains(&range[0]))
                        .map(|_| UnderlineStyle {
                            color: Some(color),
                            thickness: px(1.0),
                            wavy: false,
                        }),
                    ..run.clone()
                }
            })
            .collect();

        let font_size = style.font_size.to_pixels(window.rem_size());
        let line_height = window.line_height();
        let lines = window
            .text_system()
            .shape_text(
                display_text,
                font_size,
                &runs,
                input.multiline.then_some(bounds.size.width),
                None,
            )
            .unwrap_or_default()
            .into_iter()
            .collect();
        let mut layout = TextLayout::new(lines, line_height);
        let raw_cursor = layout.raw_position_for_index(cursor);
        layout.scroll_y = input_scroll_offset(
            input
                .last_layout
                .as_ref()
                .map_or(px(0.), |layout| layout.scroll_y),
            layout.height,
            bounds.size.height,
            input
                .reveal_cursor
                .then_some(raw_cursor.y..raw_cursor.y + line_height),
        );

        let cursor_pos = layout.position_for_index(cursor);
        let (selection, cursor) = if selected_range.is_empty() {
            (
                Vec::new(),
                Some(fill(
                    Bounds::new(
                        point(
                            bounds.left() + cursor_pos.x,
                            bounds.top() + cursor_pos.y + px(3.),
                        ),
                        size(px(1.), line_height - px(6.)),
                    ),
                    ThemePalette::for_window(window, cx).muted,
                )),
            )
        } else {
            (layout.selection(selected_range, bounds), None)
        };
        PrepaintState {
            layout: Some(layout),
            cursor,
            selection,
        }
    }

    fn paint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&gpui::InspectorElementId>,
        bounds: Bounds<Pixels>,
        _request_layout: &mut Self::RequestLayoutState,
        prepaint: &mut Self::PrepaintState,
        window: &mut Window,
        cx: &mut App,
    ) {
        let focus_handle = self.input.read(cx).focus_handle.clone();
        window.handle_input(
            &focus_handle,
            ElementInputHandler::new(bounds, self.input.clone()),
            cx,
        );
        window.with_content_mask(Some(ContentMask { bounds }), |window| {
            for selection in prepaint.selection.drain(..) {
                window.paint_quad(selection)
            }
            let layout = prepaint.layout.take().unwrap();
            for (index, line) in layout.lines.iter().enumerate() {
                let origin = point(
                    bounds.left(),
                    bounds.top() + layout.tops[index] - layout.scroll_y,
                );
                line.paint(
                    origin,
                    layout.line_height,
                    gpui::TextAlign::Left,
                    Some(Bounds::new(
                        origin,
                        size(bounds.size.width, line.size(layout.line_height).height),
                    )),
                    window,
                    cx,
                )
                .unwrap();
            }

            let icon_size = window.text_style().font_size.to_pixels(window.rem_size()) * 0.85;
            let color = ThemePalette::for_window(window, cx).accent.into();
            for file in &self.input.read(cx).references {
                let position = layout.position_for_index(file.range.start);
                let icon_bounds = Bounds::new(
                    point(
                        bounds.left() + position.x,
                        bounds.top() + position.y + (layout.line_height - icon_size) / 2.,
                    ),
                    size(icon_size, icon_size),
                );
                if let Err(error) = window.paint_svg(
                    icon_bounds,
                    format!("icons/{}.svg", file.glyph).into(),
                    None,
                    Default::default(),
                    color,
                    cx,
                ) {
                    tracing::warn!(%error, "failed to paint file reference icon");
                }
            }

            if caret_visible(
                focus_handle.is_focused(window),
                window.is_window_active(),
                self.input.read(cx).cursor_visible,
            ) && let Some(cursor) = prepaint.cursor.take()
            {
                window.paint_quad(cursor);
            }

            self.input.update(cx, |input, _cx| {
                input.last_layout = Some(layout);
                input.last_bounds = Some(bounds);
                input.reveal_cursor = false;
            });
        });
    }
}

impl Render for TextInput {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let prefs = AppearancePreferences::current(cx);
        let (font_size, line_height, padding_y) = if self.compact {
            (prefs.ui_font_size, prefs.ui_font_size * 1.5, 3.)
        } else {
            (prefs.chat_font_size, prefs.chat_font_size * 1.6, 10.)
        };
        div()
            .id("composer-text-input")
            .role(Role::TextInput)
            .aria_label(self.placeholder.clone())
            .when(self.multiline, |input| {
                input.aria_value(self.content.clone())
            })
            .flex_1()
            .key_context(if self.terminal {
                "TerminalInput"
            } else {
                "TextInput"
            })
            .track_focus(&self.focus_handle(cx))
            .cursor(CursorStyle::IBeam)
            .on_action(cx.listener(Self::backspace))
            .on_action(cx.listener(Self::delete))
            .on_action(cx.listener(Self::left))
            .on_action(cx.listener(Self::right))
            .on_action(cx.listener(Self::select_left))
            .on_action(cx.listener(Self::select_right))
            .on_action(cx.listener(Self::select_all))
            .on_action(cx.listener(Self::home))
            .on_action(cx.listener(Self::end))
            .on_action(cx.listener(Self::show_character_palette))
            .on_action(cx.listener(Self::paste))
            .on_action(cx.listener(Self::cut))
            .on_action(cx.listener(Self::copy))
            .on_action(cx.listener(Self::newline))
            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
            .on_mouse_down_out(cx.listener(|input, event: &MouseDownEvent, window, _| {
                if event.button == MouseButton::Left && input.focus_handle.is_focused(window) {
                    window.blur();
                }
            }))
            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_mouse_up_out(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_mouse_move(cx.listener(Self::on_mouse_move))
            .on_scroll_wheel(cx.listener(Self::on_scroll_wheel))
            .size_full()
            .bg(rgba(0x00000000))
            .line_height(px(line_height))
            .text_size(px(font_size))
            .child(
                div()
                    .size_full()
                    .px(px(8.))
                    .py(px(padding_y))
                    .when(!self.multiline, |field| field.flex().items_center())
                    .bg(rgba(0x00000000))
                    .child(TextElement { input: cx.entity() }),
            )
    }
}

impl Focusable for TextInput {
    fn focus_handle(&self, _: &App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

fn password_mask(content: &str) -> String {
    "*".repeat(content.len())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn conversation_spans_preserve_unicode_titles_and_identity_when_copied_or_deleted() {
        let title = "方案 ] \\ 中文 / API";
        let wire = session_reference("session-123", title);
        let draft = format!("继续 {wire} 和 @src/main.rs");
        assert_eq!(session_reference_ranges(&draft)[0].1, title);
        assert_eq!(
            display_session_references(&draft),
            format!("继续 @{title} 和 @src/main.rs")
        );
        let label = format!("\u{2003}\u{2060}{title}");
        let mut text: SharedString = format!("继续 {label} ").into();
        let start = "继续 ".len();
        let mut refs = vec![InlineReference {
            range: start..start + label.len(),
            source: wire.clone(),
            glyph: "message-square-text",
        }];
        assert_eq!(
            reference_source(&text, &refs, 0..text.len()),
            format!("继续 {wire} ")
        );
        replace_input_text(&mut text, &mut refs, start + 3..start + 6, "");
        assert!(refs.is_empty());
        assert_eq!(text.as_ref(), "继续  ");
        assert!(session_reference_ranges("@[fake](azem-session:../other)").is_empty());
    }

    #[test]
    fn manual_input_scroll_survives_redraw_and_edits_reveal_only_when_needed() {
        let scroll =
            |previous, cursor| input_scroll_offset(px(previous), px(1000.), px(200.), cursor);
        assert_eq!(scroll(0., Some(px(980.)..px(1000.))), px(800.));
        assert_eq!(scroll(400., None), px(400.)); // manual scroll, including blink redraws
        assert_eq!(scroll(400., Some(px(440.)..px(460.))), px(400.)); // click/edit in viewport
        assert_eq!(scroll(400., Some(px(980.)..px(1000.))), px(800.));
        assert_eq!(scroll(800., Some(px(0.)..px(20.))), px(0.));
        assert_eq!(scroll(-100., None), px(0.));
        assert_eq!(scroll(1200., None), px(800.));
        assert_eq!(
            input_scroll_offset(px(800.), px(100.), px(200.), None),
            px(0.)
        );
    }

    #[test]
    fn password_mask_keeps_ascii_length_and_hides_secret() {
        let secret = "sk-test";
        let masked = password_mask(secret);
        assert_eq!(masked.len(), secret.len());
        assert_eq!(masked, "*******");
        assert!(!masked.contains('s'));
    }

    #[test]
    fn file_labels_hide_paths_but_copy_and_submission_keep_distinct_targets() {
        let first = "docs/decisions/README.md";
        let second = "internal/README.md";
        let label = file_label(first);
        assert_eq!(label, "\u{2003}\u{2060}README.md");
        assert_eq!(label, file_label(second));
        let text = format!("inspect {label} and {label} please");
        let first_start = "inspect ".len();
        let second_start = first_start + label.len() + " and ".len();
        let files = vec![
            InlineReference {
                range: first_start..first_start + label.len(),
                source: file_reference(first),
                glyph: file_icon(first),
            },
            InlineReference {
                range: second_start..second_start + label.len(),
                source: file_reference(second),
                glyph: file_icon(second),
            },
        ];
        let source = reference_source(&text, &files, 0..text.len());
        assert_eq!(
            source,
            "inspect @docs/decisions/README.md and @internal/README.md please"
        );
        // Selecting part of a visible filename copies its complete reference.
        assert_eq!(
            reference_source(&text, &files, second_start + 6..second_start + 7),
            "@internal/README.md"
        );
        let payload = crate::composer_completion::turn_payload(
            &crate::state::AppState::default(),
            &source,
            &[],
            vec![],
        )
        .unwrap();
        assert_eq!(payload["prompt"], source);
        assert_eq!(
            file_reference("docs/中文 \"文件\\.md"),
            "@\"docs/中文 \\\"文件\\\\.md\""
        );
        for (path, glyph) in [
            (first, "notebook-pen"),
            ("a.rs", "file-code"),
            ("README.MD", "notebook-pen"),
            ("go.mod", "box"),
            (".gitignore", "git-branch"),
            ("Makefile", "terminal"),
            (".env.local", "settings"),
            ("LICENSE", "file-text"),
            ("a.JSON", "braces"),
            ("a.png", "image"),
            ("a.sql", "database"),
            ("a.sh", "terminal"),
            ("a.zip", "archive"),
        ] {
            assert_eq!(file_icon(path), glyph);
            assert!(
                std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
                    .join(format!("../../assets/icons/{glyph}.svg"))
                    .is_file()
            );
        }
    }

    #[test]
    fn file_spans_survive_unicode_ime_edits_and_delete_atomically() {
        let path = "docs/中文 文件.md";
        let prefix = "👩‍🚒 请看 ";
        let label = file_label(path);
        let mut text: SharedString = format!("{prefix}{label} 后文").into();
        let mut files = vec![InlineReference {
            range: prefix.len()..prefix.len() + label.len(),
            source: file_reference(path),
            glyph: file_icon(path),
        }];
        // The same replacement path is used for both IME preedit and commit.
        replace_input_text(&mut text, &mut files, 0..0, "zhong");
        replace_input_text(&mut text, &mut files, 0..5, "中文");
        assert_eq!(files[0].range.start, prefix.len() + "中文".len());
        assert_eq!(
            reference_source(&text, &files, 0..text.len()),
            format!("中文{prefix}@\"{path}\" 后文")
        );
        let inside = files[0].range.start + 6;
        // A stale platform caret inside a reference inserts after it, never into its label.
        let after = files[0].range.end;
        assert_eq!(
            replace_input_text(&mut text, &mut files, inside..inside, "!"),
            after..after
        );
        assert_eq!(files.len(), 1);
        assert!(reference_source(&text, &files, 0..text.len()).ends_with("文件.md\"! 后文"));
        let removed = files[0].range.clone();
        assert_eq!(
            replace_input_text(&mut text, &mut files, inside..inside + 3, ""),
            removed
        );
        assert!(files.is_empty());
        assert_eq!(text.as_ref(), format!("中文{prefix}! 后文"));
        let len = text.len();
        replace_input_text(&mut text, &mut files, 0..len, "");
        assert!(reference_source(&text, &files, 0..0).is_empty());
    }

    #[test]
    fn caret_requires_active_focus_and_visible_blink_phase() {
        assert!(!caret_visible(false, true, true));
        assert!(!caret_visible(true, false, true));
        assert!(caret_visible(true, true, true));
        assert!(!caret_visible(true, true, false));
        assert!(caret_visible(true, true, true));
    }

    #[test]
    fn paste_preserves_multiline_composer_text_only() {
        assert_eq!(
            normalize_input_text("first\r\nsecond\rthird", true),
            "first\nsecond\nthird"
        );
        assert_eq!(
            normalize_input_text("first\r\nsecond\rthird", false),
            "first second third"
        );
    }

    #[test]
    fn stale_platform_ranges_clamp_to_empty_input() {
        assert_eq!(clamp_byte_range("", 1..1), 0..0);
        assert_eq!(clamp_byte_range("", 4..9), 0..0);
    }

    #[test]
    fn text_editing_bindings_do_not_consume_terminal_keys() {
        let init = include_str!("text_input.rs")
            .split_once("pub fn init")
            .unwrap()
            .1
            .split_once("pub struct TextInput")
            .unwrap()
            .0;
        for binding in init.lines().filter(|line| line.contains("KeyBinding::new")) {
            assert!(binding.contains("Some(\"TextInput\")"), "{binding}");
        }
    }

    #[test]
    fn utf16_ranges_preserve_unicode_boundaries() {
        let text = "a🦀文";
        assert_eq!(utf8_offset_from_utf16(text, 0), 0);
        assert_eq!(utf8_offset_from_utf16(text, 1), 1);
        assert_eq!(utf8_offset_from_utf16(text, 3), 5);
        assert_eq!(clamp_byte_range(text, 2..6), 1..5);
    }
}
