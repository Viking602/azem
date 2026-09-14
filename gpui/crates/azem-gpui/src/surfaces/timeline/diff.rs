use super::*;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(super) enum SourceKind {
    #[default]
    Diff,
    Read,
}

pub(super) struct DiffFile {
    pub path: String,
    pub diff: String,
    pub first_line: Option<usize>,
    pub kind: SourceKind,
}

#[derive(Debug, PartialEq)]
struct DiffLine<T> {
    old: Option<usize>,
    new: Option<usize>,
    sign: char,
    text: T,
}

pub(super) fn source_from_read(path: String, content: &str) -> DiffFile {
    parse_read_source(&path, content)
}

fn parse_read_source(path: &str, content: &str) -> DiffFile {
    let trimmed = content.trim_end_matches('\n');
    let mut lines = trimmed.lines();
    let Some(first) = lines.next() else {
        return DiffFile {
            path: path.to_string(),
            diff: String::new(),
            first_line: None,
            kind: SourceKind::Read,
        };
    };
    if let Some(header_path) = hashline_header(first) {
        let path = if path.is_empty() {
            header_path
        } else {
            path.to_string()
        };
        let rest = lines.collect::<Vec<_>>();
        return numbered_read_file(path, &rest);
    }
    let all = std::iter::once(first).chain(lines).collect::<Vec<_>>();
    if all.len() > 1 && all.iter().all(|line| hashline_row(line).is_some()) {
        return numbered_read_file(path.to_string(), &all);
    }
    DiffFile {
        path: path.to_string(),
        diff: trimmed.to_string(),
        first_line: Some(1),
        kind: SourceKind::Read,
    }
}

fn numbered_read_file(path: String, lines: &[&str]) -> DiffFile {
    if lines.iter().all(|line| hashline_row(line).is_some()) {
        let first_line = lines
            .first()
            .and_then(|line| hashline_row(line).map(|(n, _)| n));
        let diff = lines
            .iter()
            .map(|line| hashline_row(line).map(|(_, text)| text).unwrap_or(*line))
            .collect::<Vec<_>>()
            .join("\n");
        return DiffFile {
            path,
            diff,
            first_line,
            kind: SourceKind::Read,
        };
    }
    DiffFile {
        path,
        diff: lines.join("\n"),
        first_line: None,
        kind: SourceKind::Read,
    }
}

fn hashline_header(line: &str) -> Option<String> {
    let line = line.trim();
    let rest = line.strip_prefix('[')?.strip_suffix(']')?;
    let (path, tag) = rest.rsplit_once('#')?;
    if path.is_empty() || tag.len() != 4 || !tag.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return None;
    }
    Some(path.to_string())
}

fn hashline_row(line: &str) -> Option<(usize, &str)> {
    let (number, text) = line.split_once(':')?;
    Some((number.parse().ok()?, text))
}

fn source_lines(file: &DiffFile) -> Vec<DiffLine<&str>> {
    if file.kind == SourceKind::Read {
        let mut number = file.first_line;
        return file
            .diff
            .lines()
            .map(|text| {
                let row = DiffLine {
                    old: None,
                    new: number,
                    sign: ' ',
                    text,
                };
                number = number.and_then(|line| line.checked_add(1));
                row
            })
            .collect();
    }
    diff_lines(file)
}

fn diff_lines(file: &DiffFile) -> Vec<DiffLine<&str>> {
    let (mut old, mut new) = (file.first_line, file.first_line);
    let unified = file.diff.lines().any(|line| line.starts_with("@@ "));
    file.diff
        .lines()
        .map(|line| {
            if line.starts_with("@@ ") {
                let mut spans = line.split_whitespace().skip(1);
                old = spans
                    .next()
                    .and_then(|span| span.strip_prefix('-'))
                    .and_then(|span| span.split(',').next())
                    .and_then(|line| line.parse().ok());
                new = spans
                    .next()
                    .and_then(|span| span.strip_prefix('+'))
                    .and_then(|span| span.split(',').next())
                    .and_then(|line| line.parse().ok());
                return DiffLine {
                    old: None,
                    new: None,
                    sign: '@',
                    text: line,
                };
            }
            if unified
                && (line.starts_with("--- ")
                    || line.starts_with("+++ ")
                    || line.starts_with("diff --git ")
                    || line.starts_with("index ")
                    || line.starts_with('\\'))
            {
                return DiffLine {
                    old: None,
                    new: None,
                    sign: '@',
                    text: line,
                };
            }
            let (sign, text) = match line.as_bytes().first() {
                Some(b'+' | b'-' | b' ') => (line.as_bytes()[0] as char, &line[1..]),
                _ => (' ', line),
            };
            let row = DiffLine {
                old: (sign != '+').then_some(old).flatten(),
                new: (sign != '-').then_some(new).flatten(),
                sign,
                text,
            };
            if sign != '+' {
                old = old.and_then(|line| line.checked_add(1));
            }
            if sign != '-' {
                new = new.and_then(|line| line.checked_add(1));
            }
            row
        })
        .collect()
}

pub(super) fn file_cards(
    files: Vec<DiffFile>,
    identity: &str,
    index: usize,
    style: (ThemePalette, Locale, bool),
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, reduced) = style;
    div()
        .w_full()
        .min_w_0()
        .flex()
        .flex_col()
        .gap_2()
        .children(files.into_iter().enumerate().map(|(offset, file)| {
            let key = format!("diff-file:{identity}:{offset}");
            let expanded = expansion.borrow_mut().group_is_expanded(&key, true);
            let more_key = format!("diff-more:{identity}:{offset}");
            let show_all = expansion.borrow().is_expanded(&more_key);
            let is_read = file.kind == SourceKind::Read;
            let language = super::highlight::language_for_path(&file.path);
            let lines = source_lines(&file);
            let digits = lines
                .iter()
                .flat_map(|line| [line.old, line.new])
                .flatten()
                .max()
                .unwrap_or(1)
                .to_string()
                .len();
            let gutter_width = px((digits as f32 * palette.code_font_size * 0.7 + 12.).max(34.));
            let additions = lines.iter().filter(|line| line.sign == '+').count();
            let deletions = lines.iter().filter(|line| line.sign == '-').count();
            let start_line = lines.iter().find_map(|line| line.new);
            let end_line = lines.iter().rev().find_map(|line| line.new);
            let path = std::path::Path::new(&file.path);
            let name = path
                .file_name()
                .and_then(|name| name.to_str())
                .unwrap_or(if is_read { "file" } else { "diff" })
                .to_string();
            let directory = path
                .parent()
                .and_then(|path| path.to_str())
                .unwrap_or_default()
                .to_string();
            let click_key = key.clone();
            let click_expansion = expansion.clone();
            let click_owner = owner.clone();
            let more_expansion = expansion.clone();
            let more_owner = owner.clone();
            let body = div()
                .w_full()
                .min_w_0()
                .border_t_1()
                .border_color(palette.border)
                .bg(palette.paper_muted)
                .child(
                    div()
                        .id(format!("diff-code:{identity}:{offset}"))
                        .overflow_x_scroll()
                        .w_full()
                        .py_1()
                        .font_family("SF Mono")
                        .text_size(px(palette.code_font_size))
                        .line_height(px(palette.code_font_size * 1.7))
                        .children(
                            lines
                                .iter()
                                .take(if show_all { usize::MAX } else { 16 })
                                .map(|line| {
                                    source_row(line, language, gutter_width, palette, is_read)
                                }),
                        ),
                )
                .when(lines.len() > 16, |body| {
                    body.child(
                        div()
                            .id(more_key.clone())
                            .role(Role::Button)
                            .aria_label(locale.text(if show_all {
                                "ui.showLess"
                            } else {
                                "common.showMore"
                            }))
                            .tab_stop(true)
                            .h(px(28.))
                            .px_3()
                            .flex()
                            .items_center()
                            .text_size(px(11.))
                            .text_color(palette.muted)
                            .cursor_pointer()
                            .child(locale.text(if show_all {
                                "ui.showLess"
                            } else {
                                "common.showMore"
                            }))
                            .on_click(move |_, _, cx| {
                                more_expansion.borrow_mut().toggle(&more_key);
                                more_owner.update(cx, |this, cx| {
                                    this.refresh_transcript_layout(index);
                                    cx.notify();
                                });
                            }),
                    )
                });
            div()
                .id(format!("diff-card:{identity}:{offset}"))
                .w_full()
                .min_w_0()
                .rounded(px(8.))
                .border_1()
                .border_color(palette.border)
                .overflow_hidden()
                .bg(palette.paper)
                .child(
                    div()
                        .id(format!("diff-header:{identity}:{offset}"))
                        .role(Role::Button)
                        .aria_label(file.path.clone())
                        .aria_expanded(expanded)
                        .tab_stop(true)
                        .h(px(34.))
                        .px_3()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .child(icon("file-code", 13., palette.faint))
                        .child(
                            div()
                                .text_size(px(11.))
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .text_color(palette.ink)
                                .child(name),
                        )
                        .child(
                            div()
                                .min_w_0()
                                .flex_1()
                                .truncate()
                                .text_size(px(10.))
                                .text_color(palette.faint)
                                .child(directory),
                        )
                        .when(is_read, |header| {
                            header.when_some(start_line, |header, start| {
                                let label = match end_line {
                                    Some(end) if end != start => format!("L{start}–{end}"),
                                    _ => format!("L{start}"),
                                };
                                header.child(
                                    div()
                                        .text_size(px(10.))
                                        .text_color(palette.faint)
                                        .child(label),
                                )
                            })
                        })
                        .when(!is_read, |header| {
                            header
                                .child(
                                    div()
                                        .text_size(px(10.))
                                        .text_color(palette.positive)
                                        .child(format!("+{additions}")),
                                )
                                .child(
                                    div()
                                        .text_size(px(10.))
                                        .text_color(palette.danger)
                                        .child(format!("−{deletions}")),
                                )
                        })
                        .child(icon(
                            if expanded {
                                "chevron-down"
                            } else {
                                "chevron-right"
                            },
                            12.,
                            palette.faint,
                        ))
                        .on_click(move |_, _, cx| {
                            click_expansion.borrow_mut().toggle(&click_key);
                            click_owner.update(cx, |this, cx| {
                                this.refresh_transcript_layout(index);
                                cx.notify();
                            });
                        }),
                )
                .child(disclosure_body(
                    key,
                    index,
                    expanded,
                    reduced,
                    body,
                    expansion.clone(),
                    owner.clone(),
                ))
        }))
        .into_any_element()
}

fn source_row(
    line: &DiffLine<impl AsRef<str>>,
    language: super::highlight::Language,
    gutter_width: Pixels,
    palette: ThemePalette,
    is_read: bool,
) -> gpui::Div {
    let color = match line.sign {
        '+' => palette.positive,
        '-' => palette.danger,
        _ => palette.ink_soft,
    };
    let changed = matches!(line.sign, '+' | '-');
    let mut row = div()
        .w_full()
        .min_w_0()
        .flex()
        .border_l_2()
        .border_color(if changed { color } else { palette.paper_muted })
        .bg(if changed {
            Rgba { a: 0.12, ..color }
        } else {
            palette.paper_muted
        });
    if is_read {
        row = row.child(
            div()
                .w(gutter_width)
                .whitespace_nowrap()
                .flex_none()
                .text_right()
                .pr_2()
                .text_color(palette.faint)
                .child(line.new.map(|n| n.to_string()).unwrap_or_default()),
        );
    } else {
        row = row
            .child(
                div()
                    .w(gutter_width)
                    .whitespace_nowrap()
                    .flex_none()
                    .text_right()
                    .pr_2()
                    .text_color(palette.faint)
                    .child(line.old.map(|n| n.to_string()).unwrap_or_default()),
            )
            .child(
                div()
                    .w(gutter_width)
                    .whitespace_nowrap()
                    .flex_none()
                    .text_right()
                    .pr_2()
                    .text_color(palette.faint)
                    .child(line.new.map(|n| n.to_string()).unwrap_or_default()),
            )
            .child(
                div()
                    .w(px(20.))
                    .flex_none()
                    .text_center()
                    .text_color(color)
                    .child(if changed {
                        line.sign.to_string()
                    } else {
                        String::new()
                    }),
            );
    }
    row.child(div().flex_shrink_0().pr_3().whitespace_nowrap().child(
        super::highlight::highlighted_code(line.text.as_ref(), language, palette, line.sign == '@'),
    ))
}

// Prepared once per file response; scrolling only builds the requested visible rows.
pub(in crate::surfaces) struct SourcePreview {
    lines: Vec<DiffLine<String>>,
    language: super::highlight::Language,
    widest_line: usize,
    digits: usize,
    is_read: bool,
}

impl SourcePreview {
    pub(in crate::surfaces) fn new(path: &str, content: &str, is_read: bool) -> Self {
        let file = DiffFile {
            path: path.to_string(),
            diff: content.to_string(),
            first_line: None,
            kind: SourceKind::Diff,
        };
        let lines: Vec<_> = if is_read {
            // Keep the final empty source line and never interpret source as a patch.
            content
                .split('\n')
                .enumerate()
                .map(|(index, text)| DiffLine {
                    old: None,
                    new: Some(index + 1),
                    sign: ' ',
                    text,
                })
                .collect()
        } else {
            source_lines(&file)
        };
        let widest_line = lines
            .iter()
            .enumerate()
            .max_by_key(|(_, line)| line.text.len())
            .map_or(0, |(index, _)| index);
        let digits = lines
            .iter()
            .flat_map(|line| [line.old, line.new])
            .flatten()
            .max()
            .unwrap_or(1)
            .to_string()
            .len();
        Self {
            lines: lines
                .into_iter()
                .map(|line| DiffLine {
                    old: line.old,
                    new: line.new,
                    sign: line.sign,
                    text: line.text.to_string(),
                })
                .collect(),
            language: super::highlight::language_for_path(path),
            widest_line,
            digits,
            is_read,
        }
    }

    fn render_rows(
        &self,
        range: std::ops::Range<usize>,
        palette: ThemePalette,
    ) -> Vec<gpui::AnyElement> {
        let gutter = px((self.digits as f32 * palette.code_font_size * 0.7 + 12.).max(34.));
        self.lines[range]
            .iter()
            .map(|line| {
                let content = if self.is_read {
                    div()
                        .flex()
                        .child(
                            div()
                                .w(gutter)
                                .flex_shrink_0()
                                .text_right()
                                .pr_3()
                                .border_r_1()
                                .border_color(palette.border)
                                .text_color(palette.faint)
                                .child(line.new.unwrap_or(1).to_string()),
                        )
                        .child(div().pl_3().flex_shrink_0().whitespace_nowrap().child(
                            super::highlight::highlighted_code(
                                &line.text,
                                self.language,
                                palette,
                                false,
                            ),
                        ))
                        .into_any_element()
                } else {
                    source_row(line, self.language, gutter, palette, false)
                        .w_auto()
                        .into_any_element()
                };
                div()
                    .font_family("Menlo")
                    .text_size(px(palette.code_font_size))
                    .line_height(px(palette.code_font_size * 1.7))
                    .h(px(palette.code_font_size * 1.7))
                    .whitespace_nowrap()
                    .child(content)
                    .into_any_element()
            })
            .collect()
    }

    pub(in crate::surfaces) fn render(
        self: &Rc<Self>,
        scroll: &UniformListScrollHandle,
        palette: ThemePalette,
    ) -> gpui::AnyElement {
        let source = self.clone();
        uniform_list(
            "workspace-source-lines",
            self.lines.len(),
            move |range, _, _| source.render_rows(range, palette),
        )
        .size_full()
        .with_width_from_item(Some(self.widest_line))
        .with_horizontal_sizing_behavior(gpui::ListHorizontalSizingBehavior::Unconstrained)
        .track_scroll(scroll)
        .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn large_preview_keeps_late_hunks_and_literal_source_lines() {
        let mut patch = String::from("--- a/data.json\n+++ b/data.json\n@@ -0,0 +1,40000 @@\n");
        for index in 0..40_000 {
            patch.push_str(&format!("+{{\"row\":{index}}}\n"));
        }
        patch.push_str("@@ -90000,2 +90001,2 @@\n-old\n+中文 🦀\n unchanged\n");
        let preview = SourcePreview::new("data.json", &patch, false);
        assert_eq!(preview.lines.len(), 40_007);
        let last = &preview.lines[40_003..];
        assert_eq!(last[0].sign, '@');
        assert_eq!((last[1].old, last[1].new), (Some(90_000), None));
        assert_eq!((last[2].old, last[2].new), (None, Some(90_001)));
        assert_eq!(last[2].text, "中文 🦀");
        assert_eq!((last[3].old, last[3].new), (Some(90_001), Some(90_002)));
        assert_eq!(preview.digits, 5);
        let source = SourcePreview::new("source.txt", "+literal\n@@ -1 +2 @@\n", true);
        assert_eq!(source.lines.len(), 3);
        assert_eq!(source.lines[0].text, "+literal");
        assert_eq!(source.lines[1].sign, ' ');
        assert_eq!(source.lines[2].new, Some(3));
        assert_eq!(source.lines[2].text, "");
        assert_eq!(source.widest_line, 1);
        assert!(SourcePreview::new("empty.diff", "", false).lines.is_empty());
    }

    #[test]
    fn compact_diff_numbers_both_sides_without_counting_metadata() {
        let file = DiffFile {
            path: "a.rs".into(),
            diff: "-old\n+new\n unchanged".into(),
            first_line: Some(12),
            kind: SourceKind::Diff,
        };
        assert_eq!(
            source_lines(&file),
            vec![
                DiffLine {
                    old: Some(12),
                    new: None,
                    sign: '-',
                    text: "old"
                },
                DiffLine {
                    old: None,
                    new: Some(12),
                    sign: '+',
                    text: "new"
                },
                DiffLine {
                    old: Some(13),
                    new: Some(13),
                    sign: ' ',
                    text: "unchanged"
                }
            ]
        );
    }

    #[test]
    fn compact_code_that_looks_like_a_header_is_still_a_changed_line() {
        let file = DiffFile {
            path: "a.txt".into(),
            diff: "--- heading\n+++ heading".into(),
            first_line: Some(2),
            kind: SourceKind::Diff,
        };
        let lines = source_lines(&file);
        assert_eq!(lines[0].sign, '-');
        assert_eq!(lines[0].old, Some(2));
        assert_eq!(lines[1].sign, '+');
        assert_eq!(lines[1].new, Some(2));
    }

    #[test]
    fn unified_hunks_reset_positions_and_missing_positions_stay_unknown() {
        let file = DiffFile {
            path: "a.rs".into(),
            diff: "--- a.rs\n+++ a.rs\n@@ -4,1 +8,1 @@\n-old\n+new\n\\ No newline at end of file"
                .into(),
            first_line: None,
            kind: SourceKind::Diff,
        };
        let lines = source_lines(&file);
        assert_eq!(lines[3].old, Some(4));
        assert_eq!(lines[4].new, Some(8));
        assert_eq!(
            lines
                .iter()
                .filter(|line| matches!(line.sign, '+' | '-'))
                .count(),
            2
        );
        let unknown = DiffFile {
            path: "".into(),
            diff: "+new".into(),
            first_line: None,
            kind: SourceKind::Diff,
        };
        assert_eq!(source_lines(&unknown)[0].new, None);
    }

    #[test]
    fn hashline_read_strips_header_and_line_prefixes() {
        let file = source_from_read(
            "src/main.rs".into(),
            "[src/main.rs#ABCD]\n12:fn main() {\n13:    println!(\"hi\");\n14:}",
        );
        assert_eq!(file.kind, SourceKind::Read);
        assert_eq!(file.path, "src/main.rs");
        assert_eq!(file.first_line, Some(12));
        assert_eq!(file.diff, "fn main() {\n    println!(\"hi\");\n}");
        let lines = source_lines(&file);
        assert_eq!(lines[0].sign, ' ');
        assert_eq!(lines[0].new, Some(12));
        assert_eq!(lines[0].text, "fn main() {");
        assert_eq!(lines[2].new, Some(14));
    }

    #[test]
    fn read_lines_do_not_treat_plus_as_diff() {
        let file = source_from_read("notes.md".into(), "+ heading\n- still source");
        let lines = source_lines(&file);
        assert_eq!(lines[0].sign, ' ');
        assert_eq!(lines[0].text, "+ heading");
        assert_eq!(lines[1].text, "- still source");
        assert_eq!(lines[0].new, Some(1));
    }
}
