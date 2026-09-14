use std::{ops::Range, sync::Arc};

use gpui::{
    App, Bounds, ClipboardItem, ElementId, Entity, FocusHandle, GlobalElementId,
    InspectorElementId, KeyBinding, LayoutId, LineLayout, MouseButton, MouseMoveEvent,
    MouseUpEvent, Pixels, Point, SharedString, StyledText, TextAlign, Window, div, fill, point,
    prelude::*, px, rgba, size,
};
use unicode_segmentation::UnicodeSegmentation;

use crate::text_input::{Copy, SelectAll};

pub(crate) fn init(cx: &mut App) {
    cx.bind_keys([
        KeyBinding::new("cmd-c", Copy, Some("SelectableText")),
        KeyBinding::new("cmd-a", SelectAll, Some("SelectableText")),
    ]);
}

#[derive(Default)]
struct Selection {
    anchor: usize,
    head: usize,
    dragging: bool,
}

impl Selection {
    fn range(&self) -> Range<usize> {
        self.anchor.min(self.head)..self.anchor.max(self.head)
    }

    fn begin(&mut self, text: &str, index: usize, shift: bool, clicks: usize) {
        let index = index.min(text.len());
        self.dragging = true;
        if shift {
            self.head = index;
            return;
        }
        let range = match clicks {
            2 => text
                .split_word_bound_indices()
                .find(|(start, word)| *start <= index && index < start + word.len())
                .map(|(start, word)| start..start + word.len())
                .unwrap_or(index..index),
            3.. => {
                let start = text[..index].rfind('\n').map_or(0, |offset| offset + 1);
                let end = text[index..]
                    .find('\n')
                    .map_or(text.len(), |offset| index + offset);
                start..end
            }
            _ => index..index,
        };
        self.anchor = range.start;
        self.head = range.end;
    }
}

pub(crate) struct TextSelection {
    focus: FocusHandle,
    source: SharedString,
    text: String,
    layouts: Vec<TextRow>,
    selection: Selection,
}

struct TextRow {
    range: Range<usize>,
    line_start: usize,
    bounds: Bounds<Pixels>,
    layout: Arc<LineLayout>,
}

fn aligned_left(left: Pixels, available: Pixels, width: Pixels, align: TextAlign) -> Pixels {
    left + match align {
        TextAlign::Left => px(0.),
        TextAlign::Center => (available - width) / 2.,
        TextAlign::Right => available - width,
    }
}

impl TextSelection {
    fn index_at(&self, position: Point<Pixels>) -> usize {
        // Tables have siblings on the same row; choose the nearest text rectangle.
        self.layouts
            .iter()
            .min_by_key(|row| {
                let bounds = row.bounds;
                let dx = (bounds.left() - position.x)
                    .max(position.x - bounds.right())
                    .max(px(0.));
                let dy = (bounds.top() - position.y)
                    .max(position.y - bounds.bottom())
                    .max(px(0.));
                (dy, dx)
            })
            .map_or(0, |row| {
                let start = row.range.start - row.line_start;
                let x = position.x - row.bounds.left() + row.layout.x_for_index(start);
                let index = row.layout.closest_index_for_x(x);
                (row.line_start + index).clamp(row.range.start, row.range.end)
            })
    }

    fn copy(&self, cx: &mut App) {
        if let Some(text) = self
            .text
            .get(self.selection.range())
            .filter(|text| !text.is_empty())
        {
            cx.write_to_clipboard(ClipboardItem::new_string(text.to_string()));
        }
    }
}

#[derive(IntoElement)]
struct Selectable<F: FnOnce(Entity<TextSelection>) -> gpui::AnyElement + 'static> {
    id: ElementId,
    source: SharedString,
    build: F,
}

pub(crate) fn selectable(
    id: impl Into<ElementId>,
    source: impl Into<SharedString>,
    build: impl FnOnce(Entity<TextSelection>) -> gpui::AnyElement + 'static,
) -> impl IntoElement {
    Selectable {
        id: id.into(),
        source: source.into(),
        build,
    }
}

impl<F: FnOnce(Entity<TextSelection>) -> gpui::AnyElement + 'static> RenderOnce for Selectable<F> {
    fn render(self, window: &mut Window, cx: &mut App) -> impl IntoElement {
        let state = window.use_keyed_state(self.id.clone(), cx, |_, cx| TextSelection {
            focus: cx.focus_handle(),
            source: SharedString::default(),
            text: String::new(),
            layouts: Vec::new(),
            selection: Selection::default(),
        });
        state.update(cx, |state, _| {
            if !self.source.starts_with(state.source.as_ref()) {
                state.selection = Selection::default();
            }
            state.source = self.source;
            state.text.clear();
            state.layouts.clear();
        });
        let focus = state.read(cx).focus.clone();
        let down = state.clone();
        let copy = state.clone();
        let all = state.clone();
        let movement = state.clone();
        let release = state.clone();
        div()
            .id(self.id)
            .w_full()
            .min_w_0()
            .relative()
            .key_context("SelectableText")
            .track_focus(&focus)
            .cursor_text()
            .on_mouse_down(MouseButton::Left, move |event, window, cx| {
                down.update(cx, |state, cx| {
                    state.focus.focus(window, cx);
                    let index = state.index_at(event.position);
                    state.selection.begin(
                        &state.text,
                        index,
                        event.modifiers.shift,
                        event.click_count,
                    );
                    cx.notify();
                });
            })
            .on_action(move |_: &Copy, _, cx| copy.update(cx, |state, cx| state.copy(cx)))
            .on_action(move |_: &SelectAll, _, cx| {
                all.update(cx, |state, cx| {
                    state.selection.anchor = 0;
                    state.selection.head = state.text.len();
                    cx.notify();
                });
            })
            .child((self.build)(state))
            .child(
                gpui::canvas(
                    |_, _, _| (),
                    move |_, _, window, _| {
                        window.on_mouse_event(move |event: &MouseMoveEvent, phase, _, cx| {
                            if phase.capture() && movement.read(cx).selection.dragging {
                                movement.update(cx, |state, cx| {
                                    state.selection.dragging =
                                        event.pressed_button == Some(MouseButton::Left);
                                    if state.selection.dragging {
                                        let index = state.index_at(event.position);
                                        if state.selection.head != index {
                                            state.selection.head = index;
                                            cx.notify();
                                        }
                                    }
                                });
                            }
                        });
                        window.on_mouse_event(move |event: &MouseUpEvent, phase, _, cx| {
                            if phase.capture() && event.button == MouseButton::Left {
                                release.update(cx, |state, _| state.selection.dragging = false);
                            }
                        });
                    },
                )
                .absolute()
                .inset_0(),
            )
    }
}

pub(crate) fn selection_text(text: StyledText, selection: Entity<TextSelection>) -> SelectionText {
    SelectionText { text, selection }
}

pub(crate) struct SelectionText {
    text: StyledText,
    selection: Entity<TextSelection>,
}

impl IntoElement for SelectionText {
    type Element = Self;
    fn into_element(self) -> Self {
        self
    }
}

impl Element for SelectionText {
    type RequestLayoutState = ();
    type PrepaintState = Range<usize>;
    fn id(&self) -> Option<ElementId> {
        None
    }
    fn source_location(&self) -> Option<&'static core::panic::Location<'static>> {
        None
    }
    fn request_layout(
        &mut self,
        _: Option<&GlobalElementId>,
        inspector: Option<&InspectorElementId>,
        window: &mut Window,
        cx: &mut App,
    ) -> (LayoutId, ()) {
        self.text.request_layout(None, inspector, window, cx)
    }
    fn prepaint(
        &mut self,
        _: Option<&GlobalElementId>,
        inspector: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        state: &mut (),
        window: &mut Window,
        cx: &mut App,
    ) -> Range<usize> {
        self.text
            .prepaint(None, inspector, bounds, state, window, cx);
        let layout = self.text.layout();
        let align = window.text_style().text_align;
        self.selection.update(cx, |selection, _| {
            if !selection.layouts.is_empty() {
                selection.text.push('\n');
            }
            let mut offset = selection.text.len();
            selection.text.push_str(&layout.text());
            let first_row = selection.layouts.len();
            let height = layout.line_height();
            let mut top = bounds.top();
            for line in layout.line_layouts() {
                let mut start = 0;
                for end in line
                    .wrap_boundaries
                    .iter()
                    .map(|boundary| {
                        line.unwrapped_layout.runs[boundary.run_ix].glyphs[boundary.glyph_ix].index
                    })
                    .chain([line.len()])
                {
                    let width = line.unwrapped_layout.x_for_index(end)
                        - line.unwrapped_layout.x_for_index(start);
                    selection.layouts.push(TextRow {
                        range: offset + start..offset + end,
                        line_start: offset,
                        bounds: Bounds::new(
                            point(
                                aligned_left(bounds.left(), bounds.size.width, width, align),
                                top,
                            ),
                            size(width, height),
                        ),
                        layout: line.unwrapped_layout.clone(),
                    });
                    start = end;
                    top += height;
                }
                offset += line.len() + 1;
            }
            first_row..selection.layouts.len()
        })
    }

    fn paint(
        &mut self,
        _: Option<&GlobalElementId>,
        inspector: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        state: &mut (),
        range: &mut Range<usize>,
        window: &mut Window,
        cx: &mut App,
    ) {
        self.text
            .paint(None, inspector, bounds, state, &mut (), window, cx);
        let selection = self.selection.read(cx);
        if !selection.focus.is_focused(window) {
            return;
        }
        let selected = selection.selection.range();
        for row in &selection.layouts[range.clone()] {
            let start = selected.start.max(row.range.start);
            let end = selected.end.min(row.range.end);
            if start >= end {
                continue;
            }
            let origin =
                row.bounds.left() - row.layout.x_for_index(row.range.start - row.line_start);
            let left = origin + row.layout.x_for_index(start - row.line_start);
            let right = origin + row.layout.x_for_index(end - row.line_start);
            if right > left {
                window.paint_quad(fill(
                    Bounds::new(
                        point(left, row.bounds.top()),
                        size(right - left, row.bounds.size.height),
                    ),
                    rgba(0x3377ff45),
                ));
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn selection_copies_unicode_across_blocks_and_in_reverse() {
        assert_eq!(
            aligned_left(px(10.), px(100.), px(20.), TextAlign::Left),
            px(10.)
        );
        assert_eq!(
            aligned_left(px(10.), px(100.), px(20.), TextAlign::Center),
            px(50.)
        );
        assert_eq!(
            aligned_left(px(10.), px(100.), px(20.), TextAlign::Right),
            px(90.)
        );
        let text = "第一段🙂\nlet value = 42;\n最后一段";
        let mut selection = Selection::default();
        let start = text.find('🙂').unwrap();
        let end = text.find('最').unwrap();
        selection.begin(text, end, false, 1);
        selection.head = start;
        assert_eq!(&text[selection.range()], "🙂\nlet value = 42;\n");
        selection.begin(text, text.find("value").unwrap() + 1, false, 2);
        assert_eq!(&text[selection.range()], "value");
        selection.begin(text, start, false, 3);
        assert_eq!(&text[selection.range()], "第一段🙂");
        selection.begin(text, end, true, 1);
        assert_eq!(&text[selection.range()], "第一段🙂\nlet value = 42;\n");
    }
}
