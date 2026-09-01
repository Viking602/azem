use std::{ops::Range, time::Duration};

use gpui::{
    Animation, AnimationExt as _, FontStyle, FontWeight, HighlightStyle, StrikethroughStyle,
    StyledText, UnderlineStyle, div, prelude::*, px,
};
use pulldown_cmark::{CodeBlockKind, Event, HeadingLevel, Options, Parser, Tag, TagEnd};

use crate::theme::ThemePalette;

const BOLD: u8 = 1;
const ITALIC: u8 = 1 << 1;
const STRIKE: u8 = 1 << 2;
const CODE: u8 = 1 << 3;
const LINK: u8 = 1 << 4;

#[derive(Clone)]
struct InlineRun {
    range: Range<usize>,
    marks: u8,
}

#[derive(Clone)]
enum BlockKind {
    Paragraph,
    Heading(u8),
    Code(String),
    Quote,
    Table(bool),
    Rule,
}

#[derive(Clone)]
struct MarkdownBlock {
    kind: BlockKind,
    text: String,
    runs: Vec<InlineRun>,
}

#[derive(Default)]
struct InlineState {
    bold: usize,
    italic: usize,
    strike: usize,
    link: usize,
}

impl InlineState {
    fn marks(&self) -> u8 {
        (if self.bold > 0 { BOLD } else { 0 })
            | (if self.italic > 0 { ITALIC } else { 0 })
            | (if self.strike > 0 { STRIKE } else { 0 })
            | (if self.link > 0 { LINK } else { 0 })
    }
}

struct ListState {
    next: Option<u64>,
}

struct MarkdownBuilder {
    blocks: Vec<MarkdownBlock>,
    kind: Option<BlockKind>,
    text: String,
    runs: Vec<InlineRun>,
    inline: InlineState,
    lists: Vec<ListState>,
    quote_depth: usize,
    table_head: bool,
}

impl MarkdownBuilder {
    fn new() -> Self {
        Self {
            blocks: Vec::new(),
            kind: None,
            text: String::new(),
            runs: Vec::new(),
            inline: InlineState::default(),
            lists: Vec::new(),
            quote_depth: 0,
            table_head: false,
        }
    }

    fn start(&mut self, kind: BlockKind) {
        if !self.text.trim().is_empty() {
            self.flush();
        }
        self.kind = Some(kind);
    }

    fn ensure_paragraph(&mut self) {
        if self.kind.is_none() {
            self.kind = Some(if self.quote_depth > 0 {
                BlockKind::Quote
            } else {
                BlockKind::Paragraph
            });
        }
    }

    fn append(&mut self, value: &str, extra_marks: u8) {
        if value.is_empty() {
            return;
        }
        self.ensure_paragraph();
        let start = self.text.len();
        self.text.push_str(value);
        let marks = self.inline.marks() | extra_marks;
        if marks != 0 {
            self.runs.push(InlineRun {
                range: start..self.text.len(),
                marks,
            });
        }
    }

    fn flush(&mut self) {
        let Some(kind) = self.kind.take() else {
            return;
        };
        if !self.text.trim().is_empty() || matches!(kind, BlockKind::Rule) {
            self.blocks.push(MarkdownBlock {
                kind,
                text: std::mem::take(&mut self.text),
                runs: std::mem::take(&mut self.runs),
            });
        } else {
            self.text.clear();
            self.runs.clear();
        }
    }

    fn list_item(&mut self) {
        self.flush();
        self.kind = Some(if self.quote_depth > 0 {
            BlockKind::Quote
        } else {
            BlockKind::Paragraph
        });
        let indent = "  ".repeat(self.lists.len().saturating_sub(1));
        let marker = self
            .lists
            .last_mut()
            .and_then(|list| list.next.as_mut())
            .map(|next| {
                let marker = format!("{next}. ");
                *next += 1;
                marker
            })
            .unwrap_or_else(|| "• ".to_string());
        self.append(&format!("{indent}{marker}"), 0);
    }
}

pub(crate) fn markdown_view(
    index: usize,
    source: &str,
    streaming: bool,
    reduced_motion: bool,
    palette: ThemePalette,
) -> gpui::AnyElement {
    let source = if streaming {
        stabilize_streaming_markdown(source)
    } else {
        source.to_string()
    };
    let blocks = parse_markdown(&source);
    let last_block_index = blocks.len().checked_sub(1);
    div()
        .id(("markdown", index))
        .role(gpui::Role::Document)
        .w_full()
        .flex()
        .flex_col()
        .gap(px(9.))
        .children(blocks.into_iter().enumerate().map(|(block_index, block)| {
            let rendered = render_block(index * 1000 + block_index, block, palette);
            if streaming && !reduced_motion && Some(block_index) == last_block_index {
                div()
                    .w_full()
                    .child(rendered)
                    .with_animation(
                        (
                            "markdown-stream-reveal",
                            index.wrapping_mul(1_000_003).wrapping_add(block_index),
                        ),
                        Animation::new(Duration::from_millis(180)),
                        |block, delta| block.opacity(stream_reveal_opacity(delta)),
                    )
                    .into_any_element()
            } else {
                rendered
            }
        }))
        .into_any_element()
}

fn stream_reveal_opacity(delta: f32) -> f32 {
    let remaining = 1. - delta.clamp(0., 1.);
    1. - 0.28 * remaining * remaining
}

fn parse_markdown(source: &str) -> Vec<MarkdownBlock> {
    let mut builder = MarkdownBuilder::new();
    for event in Parser::new_ext(source, Options::all()) {
        match event {
            Event::Start(tag) => match tag {
                Tag::Paragraph => builder.ensure_paragraph(),
                Tag::Heading { level, .. } => {
                    builder.start(BlockKind::Heading(heading_level(level)))
                }
                Tag::BlockQuote(_) => builder.quote_depth += 1,
                Tag::CodeBlock(kind) => builder.start(BlockKind::Code(match kind {
                    CodeBlockKind::Indented => String::new(),
                    CodeBlockKind::Fenced(info) => {
                        info.split_whitespace().next().unwrap_or("").to_string()
                    }
                })),
                Tag::List(next) => builder.lists.push(ListState { next }),
                Tag::Item => builder.list_item(),
                Tag::Table(_) => builder.flush(),
                Tag::TableHead => builder.table_head = true,
                Tag::TableRow => builder.start(BlockKind::Table(builder.table_head)),
                Tag::TableCell => {}
                Tag::Emphasis => builder.inline.italic += 1,
                Tag::Strong => builder.inline.bold += 1,
                Tag::Strikethrough => builder.inline.strike += 1,
                Tag::Link { .. } | Tag::Image { .. } => builder.inline.link += 1,
                Tag::FootnoteDefinition(label) => {
                    builder.start(BlockKind::Paragraph);
                    builder.append(&format!("[{label}] "), BOLD);
                }
                Tag::DefinitionListTitle => builder.start(BlockKind::Heading(4)),
                Tag::DefinitionListDefinition => builder.start(BlockKind::Quote),
                Tag::HtmlBlock
                | Tag::DefinitionList
                | Tag::Superscript
                | Tag::Subscript
                | Tag::MetadataBlock(_) => {}
            },
            Event::End(tag) => match tag {
                TagEnd::Paragraph | TagEnd::Heading(_) | TagEnd::CodeBlock | TagEnd::TableRow => {
                    builder.flush()
                }
                TagEnd::BlockQuote(_) => {
                    builder.flush();
                    builder.quote_depth = builder.quote_depth.saturating_sub(1);
                }
                TagEnd::List(_) => {
                    builder.flush();
                    builder.lists.pop();
                }
                TagEnd::Item => builder.flush(),
                TagEnd::TableHead => builder.table_head = false,
                TagEnd::TableCell => builder.append("  |  ", 0),
                TagEnd::Emphasis => builder.inline.italic = builder.inline.italic.saturating_sub(1),
                TagEnd::Strong => builder.inline.bold = builder.inline.bold.saturating_sub(1),
                TagEnd::Strikethrough => {
                    builder.inline.strike = builder.inline.strike.saturating_sub(1)
                }
                TagEnd::Link | TagEnd::Image => {
                    builder.inline.link = builder.inline.link.saturating_sub(1)
                }
                TagEnd::FootnoteDefinition
                | TagEnd::DefinitionListDefinition
                | TagEnd::DefinitionListTitle => builder.flush(),
                TagEnd::HtmlBlock
                | TagEnd::DefinitionList
                | TagEnd::Table
                | TagEnd::Superscript
                | TagEnd::Subscript
                | TagEnd::MetadataBlock(_) => {}
            },
            Event::Text(value) => builder.append(&value, 0),
            Event::Code(value) | Event::InlineMath(value) => builder.append(&value, CODE),
            Event::DisplayMath(value) => {
                builder.start(BlockKind::Code("math".to_string()));
                builder.append(&value, CODE);
                builder.flush();
            }
            Event::SoftBreak => builder.append(" ", 0),
            Event::HardBreak => builder.append("\n", 0),
            Event::Rule => {
                builder.flush();
                builder.kind = Some(BlockKind::Rule);
                builder.flush();
            }
            Event::TaskListMarker(checked) => builder.append(if checked { "☑ " } else { "☐ " }, 0),
            Event::FootnoteReference(label) => builder.append(&format!("[{label}]"), LINK),
            Event::Html(value) | Event::InlineHtml(value) => builder.append(&value, 0),
        }
    }
    builder.flush();
    if builder.blocks.is_empty() && !source.is_empty() {
        builder.blocks.push(MarkdownBlock {
            kind: BlockKind::Paragraph,
            text: source.to_string(),
            runs: Vec::new(),
        });
    }
    builder.blocks
}

fn heading_level(level: HeadingLevel) -> u8 {
    match level {
        HeadingLevel::H1 => 1,
        HeadingLevel::H2 => 2,
        HeadingLevel::H3 => 3,
        HeadingLevel::H4 => 4,
        HeadingLevel::H5 => 5,
        HeadingLevel::H6 => 6,
    }
}

fn render_block(id: usize, block: MarkdownBlock, palette: ThemePalette) -> gpui::AnyElement {
    if matches!(block.kind, BlockKind::Rule) {
        return div()
            .id(("markdown-rule", id))
            .h(px(1.))
            .w_full()
            .bg(palette.border)
            .into_any_element();
    }
    if let BlockKind::Code(language) = &block.kind {
        return div()
            .id(("markdown-code", id))
            .w_full()
            .rounded(px(9.))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper_muted)
            .overflow_x_scroll()
            .p_3()
            .flex()
            .flex_col()
            .gap_2()
            .when(!language.is_empty(), |code| {
                code.child(
                    div()
                        .text_size(px(9.))
                        .text_color(palette.faint)
                        .child(language.clone()),
                )
            })
            .child(
                div()
                    .font_family("SF Mono")
                    .text_size(px(palette.code_font_size))
                    .line_height(px(palette.code_font_size * 1.6))
                    .text_color(palette.ink_soft)
                    .child(block.text),
            )
            .into_any_element();
    }
    let text = StyledText::new(block.text).with_highlights(block.runs.into_iter().map(|run| {
        let mut style = HighlightStyle::default();
        if run.marks & BOLD != 0 {
            style.font_weight = Some(FontWeight::BOLD);
        }
        if run.marks & ITALIC != 0 {
            style.font_style = Some(FontStyle::Italic);
        }
        if run.marks & STRIKE != 0 {
            style.strikethrough = Some(StrikethroughStyle {
                thickness: px(1.),
                color: Some(palette.muted.into()),
            });
        }
        if run.marks & CODE != 0 {
            style.background_color = Some(palette.paper_muted.into());
        }
        if run.marks & LINK != 0 {
            style.color = Some(palette.accent.into());
            style.underline = Some(UnderlineStyle {
                thickness: px(1.),
                color: Some(palette.accent.into()),
                wavy: false,
            });
        }
        (run.range, style)
    }));
    let base = div()
        .id(("markdown-block", id))
        .w_full()
        .whitespace_normal()
        .child(text);
    match block.kind {
        BlockKind::Heading(level) => base
            .text_size(px(palette.chat_font_size / 14.
                * match level {
                    1 => 22.,
                    2 => 19.,
                    3 => 16.,
                    _ => 14.,
                }))
            .line_height(px(palette.chat_font_size / 14.
                * match level {
                    1 => 30.,
                    2 => 27.,
                    3 => 24.,
                    _ => 22.,
                }))
            .font_weight(FontWeight::SEMIBOLD),
        BlockKind::Quote => base
            .border_l_2()
            .border_color(palette.border_strong)
            .pl_3()
            .text_color(palette.muted),
        BlockKind::Table(header) => base
            .px_2()
            .py_1()
            .border_b_1()
            .border_color(palette.border)
            .bg(if header {
                palette.paper_muted
            } else {
                palette.paper
            })
            .when(header, |row| row.font_weight(FontWeight::SEMIBOLD)),
        BlockKind::Paragraph => base,
        BlockKind::Code(_) | BlockKind::Rule => unreachable!(),
    }
    .into_any_element()
}

fn stabilize_streaming_markdown(source: &str) -> String {
    if source.is_empty() {
        return String::new();
    }
    let mut stable = source.to_string();
    let line_start = stable.rfind('\n').map_or(0, |index| index + 1);
    if incomplete_block_starter(stable[line_start..].trim_end()) {
        stable.push_str(" \u{200b}");
    }
    if has_open_fence(&stable) {
        if !stable.ends_with('\n') {
            stable.push('\n');
        }
        stable.push_str("```");
        return stable;
    }
    if let Some(index) = stable.rfind("](")
        && !stable[index..].contains(')')
    {
        stable.push(')');
    } else if let Some(index) = stable.rfind('[')
        && !stable[index..].contains(']')
    {
        stable.push(']');
    }
    for token in ["**", "~~", "__"] {
        if unescaped_count(&stable, token) % 2 == 1 {
            stable.push_str(token);
        }
    }
    for mark in ['`', '*'] {
        if single_mark_count(&stable, mark) % 2 == 1 {
            stable.push(mark);
        }
    }
    stable
}

fn incomplete_block_starter(line: &str) -> bool {
    if line.is_empty() {
        return false;
    }
    if line.len() <= 6 && line.bytes().all(|byte| byte == b'#') {
        return true;
    }
    if matches!(line, "-" | "*" | "+") {
        return true;
    }
    let Some(last) = line.as_bytes().last() else {
        return false;
    };
    matches!(last, b'.' | b')')
        && line[..line.len() - 1]
            .bytes()
            .all(|byte| byte.is_ascii_digit())
}

fn has_open_fence(source: &str) -> bool {
    source.lines().fold(false, |open, line| {
        let line = line.trim_start_matches(' ');
        let fenced = line.starts_with("```") || line.starts_with("~~~");
        if fenced { !open } else { open }
    })
}

fn unescaped_count(source: &str, token: &str) -> usize {
    let bytes = source.as_bytes();
    let token = token.as_bytes();
    let mut count = 0;
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'\\' {
            index += 2;
        } else if bytes[index..].starts_with(token) {
            count += 1;
            index += token.len();
        } else {
            index += 1;
        }
    }
    count
}

fn single_mark_count(source: &str, mark: char) -> usize {
    let bytes = source.as_bytes();
    let mark = mark as u8;
    let mut count = 0;
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'\\' {
            index += 2;
            continue;
        }
        if bytes[index] == mark
            && bytes.get(index.wrapping_sub(1)) != Some(&mark)
            && bytes.get(index + 1) != Some(&mark)
            && !(mark == b'*'
                && (index == 0 || bytes.get(index.wrapping_sub(1)) == Some(&b'\n'))
                && bytes.get(index + 1) == Some(&b' '))
        {
            count += 1;
        }
        index += 1;
    }
    count
}

#[cfg(test)]
mod tests {
    use super::{
        BOLD, BlockKind, CODE, parse_markdown, stabilize_streaming_markdown, stream_reveal_opacity,
    };

    #[test]
    fn streaming_markdown_keeps_incomplete_syntax_structurally_stable() {
        assert_eq!(stabilize_streaming_markdown("先看 **架构"), "先看 **架构**");
        assert_eq!(
            stabilize_streaming_markdown("```ts\nconst ready = true"),
            "```ts\nconst ready = true\n```"
        );
        assert_eq!(
            stabilize_streaming_markdown("结果\n##"),
            "结果\n## \u{200b}"
        );
        let heading = parse_markdown(&stabilize_streaming_markdown("结果\n##"));
        assert_eq!(heading.len(), 2);
        assert!(matches!(heading[1].kind, BlockKind::Heading(2)));

        for marker in ["-", "1."] {
            let blocks = parse_markdown(&stabilize_streaming_markdown(&format!("结果\n{marker}")));
            assert_eq!(blocks.len(), 2, "{marker} must reserve the next line");
            assert!(matches!(blocks[1].kind, BlockKind::Paragraph));
        }
        assert_eq!(
            stabilize_streaming_markdown("见 [Azem](https://github.com/Viking602/azem"),
            "见 [Azem](https://github.com/Viking602/azem)"
        );
    }

    #[test]
    fn streaming_reveal_settles_without_overshooting() {
        assert_eq!(stream_reveal_opacity(0.), 0.72);
        assert!(stream_reveal_opacity(0.5) > 0.9);
        assert_eq!(stream_reveal_opacity(1.), 1.);
    }

    #[test]
    fn parses_common_blocks_and_inline_styles() {
        let blocks = parse_markdown(
            "# Title\n\n- item\n\nUse `code` and **bold**.\n\n```rust\nfn main() {}\n```",
        );
        assert!(matches!(blocks[0].kind, BlockKind::Heading(1)));
        assert_eq!(blocks[1].text, "• item");
        assert!(blocks[2].runs.iter().any(|run| run.marks & CODE != 0));
        assert!(blocks[2].runs.iter().any(|run| run.marks & BOLD != 0));
        assert!(matches!(&blocks[3].kind, BlockKind::Code(language) if language == "rust"));
    }

    #[test]
    fn raw_html_is_shown_as_text_instead_of_disappearing() {
        let blocks = parse_markdown("SUBAGENT_GUI_OK: <div align=\"center\">");
        assert_eq!(blocks[0].text, "SUBAGENT_GUI_OK: <div align=\"center\">");
    }
}
