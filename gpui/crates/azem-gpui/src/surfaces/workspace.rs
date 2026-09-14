use super::*;
use gpui::Along;

#[derive(Default)]
pub(crate) struct WorkspaceSource {
    scroll: UniformListScrollHandle,
    content: Option<Rc<timeline::diff::SourcePreview>>,
}

impl WorkspaceSource {
    pub(crate) fn update(&mut self, file: &serde_json::Value) {
        let path = file
            .get("path")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let patch = file.get("patch").and_then(serde_json::Value::as_str);
        let text = patch.or_else(|| {
            (!is_markdown_file(path)
                && !matches!(
                    file.get("kind").and_then(serde_json::Value::as_str),
                    Some("image" | "binary")
                ))
            .then(|| file.get("content").and_then(serde_json::Value::as_str))
            .flatten()
        });
        self.content = text.map(|text| {
            Rc::new(timeline::diff::SourcePreview::new(
                path,
                text,
                patch.is_none(),
            ))
        });
        self.scroll
            .0
            .borrow()
            .base_handle
            .set_offset(gpui::point(px(0.), px(0.)));
    }
}

pub(crate) fn workspace_files_surface(
    state: &AppState,
    palette: ThemePalette,
    tree_scroll: &gpui::ScrollHandle,
    preview_scroll: &gpui::ScrollHandle,
    source: &WorkspaceSource,
    image: Option<Arc<gpui::Image>>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let directory = state
        .workspace
        .file_tree
        .get("path")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let entries = state
        .workspace
        .file_tree
        .get("entries")
        .and_then(serde_json::Value::as_array)
        .map(Vec::as_slice)
        .unwrap_or_default();
    let preview = selected_file_content(&state.workspace.selected_file);
    let kind = state
        .workspace
        .selected_file
        .get("kind")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let parent = parent_directory(&directory);
    div()
        .id("workspace-files")
        .min_h_0()
        .role(Role::Region)
        .aria_label(locale.text("files.title"))
        .flex_1()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(workspace_header(
            locale.text("ui.projectFiles"),
            palette,
            locale,
            cx,
        ))
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .relative()
                        .w(px(WORKSPACE_RAIL + 12.))
                        .flex_shrink_0()
                        .h_full()
                        .min_h_0()
                        .child(
                            div()
                                .id("workspace-entry-list")
                                .w(px(WORKSPACE_RAIL))
                                .flex_shrink_0()
                                .min_h_0()
                                .h_full()
                                .track_scroll(tree_scroll)
                                .overflow_y_scroll()
                                .border_r_1()
                                .border_color(palette.border)
                                .p_2()
                                .flex()
                                .flex_col()
                                .gap_0()
                                .child(
                                    div()
                                        .h(px(WORKSPACE_CONTROL))
                                        .flex_shrink_0()
                                        .px_2()
                                        .rounded(px(WORKSPACE_RADIUS))
                                        .border_1()
                                        .border_color(palette.border)
                                        .text_size(px(WORKSPACE_TEXT))
                                        .text_color(palette.faint)
                                        .flex()
                                        .items_center()
                                        .child(locale.text("ui.filterByName")),
                                )
                                .child(
                                    div()
                                        .pt_3()
                                        .px_2()
                                        .text_size(px(WORKSPACE_TEXT))
                                        .text_color(palette.muted)
                                        .child(if directory.is_empty() {
                                            ".".to_string()
                                        } else {
                                            directory.clone()
                                        }),
                                )
                                .when_some(parent, |list, parent| {
                                    list.child(
                                        div()
                                            .id("workspace-parent")
                                            .role(Role::Button)
                                            .aria_label("返回上一级")
                                            .tab_stop(true)
                                            .h(px(WORKSPACE_CONTROL))
                                            .flex_shrink_0()
                                            .px_3()
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .rounded(px(WORKSPACE_RADIUS))
                                            .hover(|row| row.bg(palette.hover))
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                let id = this.runtime.request(
                                                    Method::WorkspaceEntries,
                                                    json!({"path": parent}),
                                                );
                                                this.pending_requests
                                                    .insert(id, PendingRequest::Entries);
                                                cx.notify();
                                            }))
                                            .child(icon("arrow-up", 16., palette.muted))
                                            .child("返回上一级"),
                                    )
                                })
                                .children(entries.iter().enumerate().map(|(index, entry)| {
                                    let path = entry
                                        .get("path")
                                        .and_then(serde_json::Value::as_str)
                                        .unwrap_or_default()
                                        .to_string();
                                    let directory = entry
                                        .get("directory")
                                        .and_then(serde_json::Value::as_bool)
                                        .unwrap_or(false);
                                    let label = entry
                                        .get("name")
                                        .and_then(serde_json::Value::as_str)
                                        .unwrap_or_default()
                                        .to_string();
                                    let file_image = super::file_icons::image(&path, directory);
                                    div()
                                        .id(("workspace-entry", index))
                                        .role(Role::Button)
                                        .aria_label(label.clone())
                                        .tab_stop(true)
                                        .h(px(WORKSPACE_ROW))
                                        .flex_shrink_0()
                                        .gap_2()
                                        .px_3()
                                        .rounded(px(WORKSPACE_RADIUS))
                                        .text_size(px(WORKSPACE_TEXT))
                                        .flex()
                                        .items_center()
                                        .hover(|row| row.bg(palette.hover))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            let (method, pending) = if directory {
                                                (Method::WorkspaceEntries, PendingRequest::Entries)
                                            } else {
                                                (Method::WorkspaceFile, PendingRequest::File)
                                            };
                                            let id =
                                                this.runtime.request(method, json!({"path": path}));
                                            this.pending_requests.insert(id, pending);
                                            cx.notify();
                                        }))
                                        .child(
                                            img(file_image)
                                                .w(px(WORKSPACE_ICON))
                                                .h(px(WORKSPACE_ICON))
                                                .flex_shrink_0(),
                                        )
                                        .child(div().min_w_0().truncate().child(label))
                                })),
                        )
                        .child(workspace_scrollbar(
                            "file-tree-scrollbar",
                            tree_scroll,
                            palette,
                            cx,
                        )),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .h(px(WORKSPACE_ROW))
                                .px_4()
                                .border_b_1()
                                .border_color(palette.border)
                                .text_size(px(WORKSPACE_TEXT))
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .flex()
                                .items_center()
                                .child(locale.text("ui.filePreview")),
                        )
                        .child(
                            div()
                                .relative()
                                .flex_1()
                                .min_h_0()
                                .child(
                                    div()
                                        .id("workspace-preview")
                                        .role(Role::Document)
                                        .aria_label(locale.text("files.preview"))
                                        .size_full()
                                        .when(source.content.is_none(), |view| {
                                            view.track_scroll(preview_scroll)
                                        })
                                        .min_h_0()
                                        .when(kind != "image" && source.content.is_none(), |view| {
                                            view.overflow_y_scroll()
                                        })
                                        .when(kind == "image", |view| {
                                            view.flex().flex_col().overflow_hidden()
                                        })
                                        .p_5()
                                        .text_size(px(WORKSPACE_TEXT))
                                        .child(match preview {
                                            None => div()
                                                .text_color(palette.muted)
                                                .child(locale.text("ui.selectAFile"))
                                                .into_any_element(),
                                            Some(_) if kind == "image" => workspace_image_view(
                                                &state.workspace.selected_file,
                                                image,
                                                palette,
                                                locale,
                                            ),
                                            Some(_) if kind == "binary" => div()
                                                .text_color(palette.muted)
                                                .child(locale.text("files.unsupportedPreview"))
                                                .into_any_element(),
                                            Some(content) => {
                                                if let Some(content) = &source.content {
                                                    content.render(&source.scroll, palette)
                                                } else {
                                                    markdown_view(0, content, false, true, palette)
                                                }
                                            }
                                        }),
                                )
                                .when(kind != "image", |view| {
                                    view.child(workspace_scrollbar(
                                        "file-preview-scrollbar",
                                        &source.content.as_ref().map_or_else(
                                            || preview_scroll.clone(),
                                            |_| source.scroll.0.borrow().base_handle.clone(),
                                        ),
                                        palette,
                                        cx,
                                    ))
                                })
                                .when(source.content.is_some(), |view| {
                                    view.child(workspace_scrollbar_axis(
                                        "file-preview-horizontal-scrollbar",
                                        &source.scroll.0.borrow().base_handle,
                                        palette,
                                        cx,
                                        true,
                                    ))
                                }),
                        ),
                ),
        )
        .into_any_element()
}

fn scrollbar_geometry(viewport: f32, maximum: f32, offset: f32) -> (f32, f32) {
    let thumb = (viewport * viewport / (viewport + maximum.max(0.)))
        .max(24.)
        .min(viewport);
    let top = if maximum > 0. {
        (-offset / maximum).clamp(0., 1.) * (viewport - thumb)
    } else {
        0.
    };
    (top, thumb)
}

pub(super) fn workspace_scrollbar(
    id: &'static str,
    handle: &gpui::ScrollHandle,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    workspace_scrollbar_axis(id, handle, palette, cx, false)
}

fn workspace_scrollbar_axis(
    id: &'static str,
    handle: &gpui::ScrollHandle,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
    horizontal: bool,
) -> gpui::AnyElement {
    let paint_handle = handle.clone();
    let click_handle = handle.clone();
    let drag_handle = handle.clone();
    div()
        .id(id)
        .absolute()
        .right_0()
        .bottom_0()
        .when(horizontal, |bar| bar.left_0().right(px(12.)).h(px(12.)))
        .when(!horizontal, |bar| bar.top_0().w(px(12.)))
        .on_mouse_down(
            gpui::MouseButton::Left,
            cx.listener(move |_, event: &gpui::MouseDownEvent, _, cx| {
                scroll_from_pointer(&click_handle, event.position, horizontal);
                cx.notify();
            }),
        )
        .on_mouse_move(cx.listener(move |_, event: &gpui::MouseMoveEvent, _, cx| {
            if event.pressed_button == Some(gpui::MouseButton::Left) {
                scroll_from_pointer(&drag_handle, event.position, horizontal);
                cx.notify();
            }
        }))
        .child(
            gpui::canvas(
                |_, _, _| (),
                move |bounds, _, window, _| {
                    let axis = if horizontal {
                        gpui::Axis::Horizontal
                    } else {
                        gpui::Axis::Vertical
                    };
                    let max: f32 = paint_handle.max_offset().along(axis).into();
                    if max <= 0. {
                        return;
                    }
                    let (top, height) = scrollbar_geometry(
                        bounds.size.along(axis).into(),
                        max,
                        paint_handle.offset().along(axis).into(),
                    );
                    window.paint_quad(gpui::fill(
                        gpui::Bounds::new(
                            if horizontal {
                                gpui::point(bounds.left() + px(top), bounds.top() + px(3.))
                            } else {
                                gpui::point(bounds.left() + px(3.), bounds.top() + px(top))
                            },
                            if horizontal {
                                gpui::size(px(height), px(6.))
                            } else {
                                gpui::size(px(6.), px(height))
                            },
                        ),
                        palette.muted,
                    ));
                },
            )
            .size_full(),
        )
        .into_any_element()
}

fn scroll_from_pointer(
    handle: &gpui::ScrollHandle,
    position: gpui::Point<Pixels>,
    horizontal: bool,
) {
    let axis = if horizontal {
        gpui::Axis::Horizontal
    } else {
        gpui::Axis::Vertical
    };
    let bounds = handle.bounds();
    let viewport: f32 = bounds.size.along(axis).into();
    let max: f32 = handle.max_offset().along(axis).into();
    let (_, thumb) = scrollbar_geometry(viewport, max, handle.offset().along(axis).into());
    let position: f32 = (position.along(axis) - bounds.origin.along(axis)).into();
    let fraction = ((position - thumb / 2.) / (viewport - thumb).max(1.)).clamp(0., 1.);
    handle.set_offset(handle.offset().apply_along(axis, |_| px(-max * fraction)));
}

pub(crate) fn workspace_image(file: &serde_json::Value) -> Option<Arc<gpui::Image>> {
    if file.get("kind")?.as_str()? != "image" {
        return None;
    }
    let format = gpui::ImageFormat::from_mime_type(file.get("mediaType")?.as_str()?)?;
    if !matches!(
        format,
        gpui::ImageFormat::Png
            | gpui::ImageFormat::Jpeg
            | gpui::ImageFormat::Gif
            | gpui::ImageFormat::Webp
    ) {
        return None;
    }
    let encoded = file.get("content")?.as_str()?;
    const LIMIT: usize = 8 << 20;
    if encoded.len() > LIMIT.div_ceil(3) * 4 {
        return None;
    }
    let bytes = STANDARD.decode(encoded).ok()?;
    if bytes.is_empty() || bytes.len() > LIMIT {
        return None;
    }
    Some(Arc::new(gpui::Image::from_bytes(format, bytes)))
}

fn workspace_image_view(
    file: &serde_json::Value,
    image: Option<Arc<gpui::Image>>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let failed = move || {
        div()
            .text_color(palette.muted)
            .child(locale.text("files.imageLoadFailed"))
            .into_any_element()
    };
    let Some(image) = image else {
        return failed();
    };
    div()
        .id("workspace-image-viewer")
        .role(Role::Region)
        .aria_label(locale.text("ui.image"))
        .size_full()
        .min_h_0()
        .min_w_0()
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .text_size(px(WORKSPACE_TEXT))
                .text_color(palette.muted)
                .child(
                    file.get("path")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .to_string(),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .min_w_0()
                .bg(palette.paper_muted)
                .rounded(px(WORKSPACE_RADIUS))
                .overflow_hidden()
                .child(
                    img(image)
                        .size_full()
                        .object_fit(gpui::ObjectFit::Contain)
                        .with_fallback(failed),
                ),
        )
        .into_any_element()
}

fn selected_file_content(file: &serde_json::Value) -> Option<&str> {
    file.get("path")
        .and_then(serde_json::Value::as_str)
        .filter(|path| !path.is_empty())?;
    Some(
        file.get("content")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default(),
    )
}

fn parent_directory(directory: &str) -> Option<String> {
    if directory.is_empty() || directory == "." {
        return None;
    }
    let parent = std::path::Path::new(directory).parent()?.to_str()?;
    Some(if parent.is_empty() { "." } else { parent }.to_string())
}

fn is_markdown_file(path: &str) -> bool {
    std::path::Path::new(path)
        .extension()
        .and_then(|ext| ext.to_str())
        .is_some_and(|ext| {
            matches!(
                ext.to_ascii_lowercase().as_str(),
                "md" | "markdown" | "mdown"
            )
        })
}

pub(super) fn workspace_header(
    title: &'static str,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::Div {
    div()
        .h(px(WORKSPACE_TOOLBAR))
        .flex_shrink_0()
        .px(px(WORKSPACE_INSET))
        .border_b_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(
            div()
                .id("workspace-back")
                .role(Role::Button)
                .aria_label(locale.text("ui.workspaceEsc"))
                .tab_stop(true)
                .h(px(WORKSPACE_CONTROL))
                .px_2()
                .rounded(px(WORKSPACE_RADIUS))
                .flex()
                .items_center()
                .text_size(px(WORKSPACE_META))
                .text_color(palette.muted)
                .cursor_pointer()
                .hover(|s| s.bg(palette.hover))
                .on_click(cx.listener(|this, _, _, cx| this.return_to_workspace(cx)))
                .child(locale.text("ui.workspaceEsc")),
        )
        .child(
            div()
                .text_size(px(WORKSPACE_TITLE))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
}

pub(super) fn workspace_change_row(
    index: usize,
    file: &serde_json::Value,
    selected: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::Stateful<gpui::Div> {
    let path = file
        .get("path")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let image = super::file_icons::image(&path, false);
    let added = file
        .get("additions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    let removed = file
        .get("deletions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or_default();
    div()
        .id(("workspace-change", index))
        .role(Role::Button)
        .aria_label(path.clone())
        .tab_stop(true)
        .h(px(WORKSPACE_ROW))
        .w_full()
        .px_2()
        .border_b_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_2()
        .when(selected, |s| s.bg(palette.accent_soft))
        .cursor_pointer()
        .hover(|s| s.bg(palette.hover))
        .child(
            img(image)
                .w(px(WORKSPACE_ICON))
                .h(px(WORKSPACE_ICON))
                .flex_shrink_0(),
        )
        .child(
            div()
                .flex_1()
                .min_w_0()
                .truncate()
                .text_size(px(WORKSPACE_TEXT))
                .child(path.clone()),
        )
        .child(
            div()
                .text_size(px(WORKSPACE_META))
                .text_color(palette.positive)
                .child(format!("+{added}")),
        )
        .child(
            div()
                .text_size(px(WORKSPACE_META))
                .text_color(palette.danger)
                .child(format!("−{removed}")),
        )
        .on_click(cx.listener(move |this, _, _, cx| {
            if this.state.navigation.surface != Surface::Changes {
                this.state.navigation.surface = Surface::Changes;
                this.request_surface(Surface::Changes);
            }
            let id = this
                .runtime
                .request(Method::WorkspaceChange, json!({"path":path}));
            this.pending_requests.insert(id, PendingRequest::Change);
            cx.notify();
        }))
}

pub(crate) fn workspace_changes_surface(
    state: &AppState,
    palette: ThemePalette,
    list_scroll: &UniformListScrollHandle,
    source: &WorkspaceSource,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let files = state
        .workspace
        .changes
        .get("files")
        .and_then(serde_json::Value::as_array);
    let selected_path = state
        .workspace
        .selected_file
        .get("path")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let patch = state
        .workspace
        .selected_file
        .get("patch")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let count = files.map_or(state.workspace.changed_files as usize, Vec::len);
    let added = state
        .workspace
        .changes
        .get("additions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or(state.workspace.additions as u64);
    let deleted = state
        .workspace
        .changes
        .get("deletions")
        .and_then(serde_json::Value::as_u64)
        .unwrap_or(state.workspace.deletions as u64);
    div()
        .id("workspace-changes")
        .role(Role::Region)
        .aria_label(locale.text("changes.title"))
        .flex_1()
        .min_h_0()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(workspace_header(
            locale.text("ui.codeChanges"),
            palette,
            locale,
            cx,
        ))
        .child(
            div()
                .h(px(WORKSPACE_ROW))
                .flex_shrink_0()
                .px(px(WORKSPACE_INSET))
                .flex()
                .items_center()
                .gap_3()
                .border_b_1()
                .border_color(palette.border)
                .child(
                    div()
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.muted)
                        .child(
                            locale.format("workspace.fileCount", &[("count", count.to_string())]),
                        ),
                )
                .child(
                    div()
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.positive)
                        .child(format!("+{added}")),
                )
                .child(
                    div()
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.danger)
                        .child(format!("−{deleted}")),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_size(px(WORKSPACE_META))
                        .text_color(palette.muted)
                        .child(selected_path.to_string()),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .relative()
                        .w(px(WORKSPACE_RAIL + 12.))
                        .flex_shrink_0()
                        .h_full()
                        .pr_3()
                        .border_r_1()
                        .border_color(palette.border)
                        .child(
                            uniform_list(
                                "workspace-change-list",
                                files.map_or(0, Vec::len),
                                cx.processor(move |this, range: std::ops::Range<usize>, _, cx| {
                                    let files = this
                                        .state
                                        .workspace
                                        .changes
                                        .get("files")
                                        .and_then(serde_json::Value::as_array);
                                    let selected = this.state.workspace.selected_file.get("path");
                                    range
                                        .filter_map(|index| {
                                            let file = files?.get(index)?;
                                            Some(workspace_change_row(
                                                index,
                                                file,
                                                file.get("path") == selected,
                                                palette,
                                                cx,
                                            ))
                                        })
                                        .collect()
                                }),
                            )
                            .w_full()
                            .h_full()
                            .track_scroll(list_scroll),
                        )
                        .child(workspace_scrollbar(
                            "workspace-changes-scrollbar",
                            &list_scroll.0.borrow().base_handle,
                            palette,
                            cx,
                        )),
                )
                .child(
                    div()
                        .relative()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .pr_3()
                        .child(
                            div()
                                .id("change-preview")
                                .role(Role::Document)
                                .aria_label(locale.text("changes.selected"))
                                .w_full()
                                .h_full()
                                .pb_3()
                                .child(if patch.is_empty() {
                                    div()
                                        .p_3()
                                        .text_size(px(WORKSPACE_TEXT))
                                        .text_color(palette.muted)
                                        .child(locale.text("ui.selectAChangedFile"))
                                        .into_any_element()
                                } else {
                                    source
                                        .content
                                        .as_ref()
                                        .map(|content| content.render(&source.scroll, palette))
                                        .unwrap_or_else(|| div().into_any_element())
                                }),
                        )
                        .child(workspace_scrollbar(
                            "workspace-diff-scrollbar",
                            &source.scroll.0.borrow().base_handle,
                            palette,
                            cx,
                        ))
                        .child(workspace_scrollbar_axis(
                            "workspace-diff-horizontal-scrollbar",
                            &source.scroll.0.borrow().base_handle,
                            palette,
                            cx,
                            true,
                        )),
                ),
        )
        .into_any_element()
}

#[cfg(test)]
mod preview_tests {
    use super::*;
    #[test]
    fn selecting_a_file_replaces_source_and_resets_both_scroll_axes() {
        let mut source = WorkspaceSource::default();
        source.update(&json!({"path":"data.json", "patch":"@@ -0,0 +1 @@\n+{}"}));
        let old = Rc::downgrade(source.content.as_ref().unwrap());
        source
            .scroll
            .0
            .borrow()
            .base_handle
            .set_offset(gpui::point(px(-300.), px(-4000.)));
        source.update(&json!({"path":"code.rs", "kind":"text", "content":"fn main() {}"}));
        assert!(old.upgrade().is_none());
        assert!(source.content.is_some());
        assert_eq!(
            source.scroll.0.borrow().base_handle.offset(),
            gpui::point(px(0.), px(0.))
        );
        for file in [
            json!({"path":"README.md", "content":"# Title"}),
            json!({"path":"image.png", "kind":"image", "content":"encoded"}),
            json!({"path":"data.bin", "kind":"binary", "content":""}),
            serde_json::Value::Null,
        ] {
            source.update(&file);
            assert!(source.content.is_none());
        }
    }

    #[test]
    fn scrollbar_thumb_tracks_scroll_extent() {
        assert_eq!(scrollbar_geometry(100., 100., 0.), (0., 50.));
        assert_eq!(scrollbar_geometry(100., 100., -100.), (50., 50.));
        assert_eq!(scrollbar_geometry(100., 0., 0.), (0., 100.));
        assert_eq!(scrollbar_geometry(10., 1000., -1000.), (0., 10.));
    }
    #[test]
    fn image_preview_decodes_supported_bounded_payloads() {
        let content = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a6uoAAAAASUVORK5CYII=";
        let file = json!({"kind":"image","mediaType":"image/png","content":content});
        assert!(workspace_image(&file).is_some());
        assert!(
            workspace_image(&json!({"kind":"text","mediaType":"image/png","content":content}))
                .is_none()
        );
        for (mime, content) in [
            ("image/svg+xml", "PHN2Zy8+"),
            ("image/png", "!"),
            ("image/png", ""),
        ] {
            assert!(
                workspace_image(&json!({"kind":"image","mediaType":mime,"content":content}))
                    .is_none()
            );
        }
        assert!(workspace_image(&json!({"kind":"image","mediaType":"image/png","content":"A".repeat(((8_usize << 20).div_ceil(3) * 4) + 4)})).is_none());
    }
    #[test]
    fn unselected_preview_is_not_serialized_as_null() {
        assert_eq!(selected_file_content(&serde_json::Value::Null), None);
        assert_eq!(selected_file_content(&json!({})), None);
        assert_eq!(
            selected_file_content(&json!({"path":"empty.txt"})),
            Some("")
        );
        assert_eq!(
            selected_file_content(&json!({"path":"data.json","content":"null"})),
            Some("null")
        );
    }
    #[test]
    fn preview_selects_markdown_by_extension() {
        assert_eq!(parent_directory("eval/harbor"), Some("eval".into()));
        assert_eq!(parent_directory("eval"), Some(".".into()));
        assert_eq!(parent_directory("."), None);
        for path in ["README.md", "docs/Guide.MARKDOWN", "notes.mdown"] {
            assert!(is_markdown_file(path));
        }
        for path in ["azem_test.go", "README.md.rs", "Makefile"] {
            assert!(!is_markdown_file(path));
        }
        assert_eq!(
            timeline::highlight::language_for_path("azem_test.go"),
            timeline::highlight::Language::Go
        );
    }
}
