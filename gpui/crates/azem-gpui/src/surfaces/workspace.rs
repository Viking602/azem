use super::*;
pub(crate) fn workspace_files_surface(
    state: &AppState,
    palette: ThemePalette,
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
        .cloned()
        .unwrap_or_default();
    let preview = state
        .workspace
        .selected_file
        .get("content")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .unwrap_or_else(|| pretty_value(&state.workspace.selected_file));
    div()
        .id("workspace-files")
        .role(Role::Region)
        .aria_label(locale.text("files.title"))
        .flex_1()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(
            div()
                .h(px(54.))
                .px(px(26.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .text_color(palette.muted)
                        .child(locale.text("ui.workspaceEsc")),
                )
                .child(
                    div()
                        .text_size(px(17.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.projectFiles")),
                )
                .child(
                    div().flex_1().text_sm().text_color(palette.muted).child(
                        locale.text("ui.keepHierarchyPreviewAndContextActionsInOneSightline"),
                    ),
                )
                .child(
                    div()
                        .h(px(32.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_sm()
                        .flex()
                        .items_center()
                        .child(locale.text("ui.searchFiles")),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .id("workspace-entry-list")
                        .w(px(360.))
                        .h_full()
                        .overflow_y_scroll()
                        .border_r_1()
                        .border_color(palette.border)
                        .p_3()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .h(px(34.))
                                .px_2()
                                .rounded(px(8.))
                                .border_1()
                                .border_color(palette.border)
                                .text_sm()
                                .text_color(palette.faint)
                                .flex()
                                .items_center()
                                .child(locale.text("ui.filterByName")),
                        )
                        .child(
                            div()
                                .pt_3()
                                .px_2()
                                .text_sm()
                                .text_color(palette.muted)
                                .child(if directory.is_empty() {
                                    ".".to_string()
                                } else {
                                    directory
                                }),
                        )
                        .children(entries.into_iter().enumerate().map(|(index, entry)| {
                            let path = entry
                                .get("path")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string();
                            let directory = entry
                                .get("directory")
                                .and_then(serde_json::Value::as_bool)
                                .unwrap_or(false);
                            let label = format!(
                                "{}{}",
                                if directory { "⌄  " } else { "    " },
                                entry
                                    .get("name")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                            );
                            div()
                                .id(("workspace-entry", index))
                                .role(Role::Button)
                                .aria_label(label.clone())
                                .tab_stop(true)
                                .h(px(38.))
                                .px_3()
                                .rounded(px(8.))
                                .text_sm()
                                .flex()
                                .items_center()
                                .hover(|row| row.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    let (method, pending) = if directory {
                                        (Method::WorkspaceEntries, PendingRequest::Entries)
                                    } else {
                                        (Method::WorkspaceFile, PendingRequest::File)
                                    };
                                    let id = this.runtime.request(method, json!({"path": path}));
                                    this.pending_requests.insert(id, pending);
                                    cx.notify();
                                }))
                                .child(label)
                        })),
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
                                .h(px(50.))
                                .px_4()
                                .border_b_1()
                                .border_color(palette.border)
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .flex()
                                .items_center()
                                .child(locale.text("ui.filePreview")),
                        )
                        .child(
                            div()
                                .id("workspace-preview")
                                .role(Role::Document)
                                .aria_label(locale.text("files.preview"))
                                .flex_1()
                                .overflow_y_scroll()
                                .p_5()
                                .font_family("SF Mono")
                                .text_sm()
                                .whitespace_normal()
                                .child(if preview.is_empty() {
                                    locale.text("ui.selectAFile").to_string()
                                } else {
                                    preview
                                }),
                        ),
                ),
        )
        .into_any_element()
}

pub(crate) fn workspace_changes_surface(
    state: &AppState,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let files = state
        .workspace
        .changes
        .get("files")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let patch = state
        .workspace
        .selected_file
        .get("patch")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .unwrap_or_default();
    div()
        .id("workspace-changes")
        .role(Role::Region)
        .aria_label(locale.text("changes.title"))
        .flex_1()
        .flex()
        .flex_col()
        .overflow_hidden()
        .child(
            div()
                .h(px(54.))
                .px(px(26.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .text_color(palette.muted)
                        .child(locale.text("ui.workspaceEsc")),
                )
                .child(
                    div()
                        .text_size(px(17.))
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.codeChanges")),
                )
                .child(
                    div()
                        .flex_1()
                        .text_sm()
                        .text_color(palette.muted)
                        .child(locale.text("ui.keepTheSummaryRiskAndDiffInOneReviewPath")),
                )
                .child(
                    div()
                        .h(px(32.))
                        .px_3()
                        .rounded(px(8.))
                        .bg(palette.button)
                        .text_color(palette.button_text)
                        .text_sm()
                        .flex()
                        .items_center()
                        .child(locale.text("ui.startReview")),
                ),
        )
        .child(
            div()
                .h(px(64.))
                .border_b_1()
                .border_color(palette.border)
                .flex()
                .children(
                    [
                        (
                            state.workspace.changed_files.to_string(),
                            locale.text("ui.files"),
                            palette.ink,
                        ),
                        (
                            format!("+{}", state.workspace.additions),
                            locale.text("ui.added"),
                            palette.positive,
                        ),
                        (
                            format!("−{}", state.workspace.deletions),
                            locale.text("ui.deleted"),
                            palette.danger,
                        ),
                    ]
                    .into_iter()
                    .map(|(value, label, color)| {
                        div()
                            .flex_1()
                            .px_5()
                            .border_r_1()
                            .border_color(palette.border)
                            .flex()
                            .flex_col()
                            .justify_center()
                            .gap_1()
                            .child(
                                div()
                                    .text_size(px(22.))
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .text_color(color)
                                    .child(value),
                            )
                            .child(div().text_xs().text_color(palette.faint).child(label))
                    }),
                ),
        )
        .child(
            div()
                .flex_1()
                .min_h_0()
                .flex()
                .child(
                    div()
                        .id("workspace-change-list")
                        .w(px(360.))
                        .h_full()
                        .overflow_y_scroll()
                        .border_r_1()
                        .border_color(palette.border)
                        .p_3()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .children(files.into_iter().enumerate().map(|(index, file)| {
                            let path = file
                                .get("path")
                                .and_then(serde_json::Value::as_str)
                                .unwrap_or_default()
                                .to_string();
                            let label = format!(
                                "{}  +{} −{}",
                                path,
                                file.get("additions")
                                    .and_then(serde_json::Value::as_i64)
                                    .unwrap_or_default(),
                                file.get("deletions")
                                    .and_then(serde_json::Value::as_i64)
                                    .unwrap_or_default()
                            );
                            div()
                                .id(("workspace-change", index))
                                .role(Role::Button)
                                .aria_label(label.clone())
                                .tab_stop(true)
                                .min_h(px(58.))
                                .px_3()
                                .rounded(px(8.))
                                .text_sm()
                                .flex()
                                .items_center()
                                .hover(|row| row.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    let id = this
                                        .runtime
                                        .request(Method::WorkspaceChange, json!({"path": path}));
                                    this.pending_requests.insert(id, PendingRequest::Change);
                                    cx.notify();
                                }))
                                .child(label)
                        })),
                )
                .child(
                    div()
                        .id("change-preview")
                        .role(Role::Document)
                        .aria_label(locale.text("changes.selected"))
                        .flex_1()
                        .h_full()
                        .overflow_y_scroll()
                        .p_5()
                        .font_family("SF Mono")
                        .text_sm()
                        .whitespace_normal()
                        .child(if patch.is_empty() {
                            locale.text("ui.selectAChangedFile").to_string()
                        } else {
                            patch
                        }),
                ),
        )
        .into_any_element()
}
