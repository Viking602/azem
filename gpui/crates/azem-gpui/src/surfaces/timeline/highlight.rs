use std::ops::Range;

use gpui::{HighlightStyle, Rgba, StyledText};

use crate::theme::ThemePalette;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Language {
    Rust,
    Go,
    JavaScript,
    Python,
    Json,
    Yaml,
    Toml,
    Css,
    Shell,
    Sql,
    Html,
    CLike,
    Plain,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum TokenKind {
    Default,
    Comment,
    String,
    Number,
    Keyword,
    Property,
}

pub(crate) fn language_for_path(path: &str) -> Language {
    let ext = std::path::Path::new(path)
        .extension()
        .and_then(|ext| ext.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    match ext.as_str() {
        "rs" => Language::Rust,
        "go" => Language::Go,
        "ts" | "tsx" | "js" | "jsx" | "mjs" | "cjs" => Language::JavaScript,
        "py" => Language::Python,
        "json" | "jsonc" => Language::Json,
        "yaml" | "yml" => Language::Yaml,
        "toml" => Language::Toml,
        "css" | "scss" => Language::Css,
        "sh" | "bash" | "zsh" => Language::Shell,
        "sql" => Language::Sql,
        "html" | "htm" | "xml" | "svg" => Language::Html,
        "c" | "h" | "cc" | "cpp" | "hpp" | "cxx" | "java" | "kt" | "kts" | "swift" | "cs" => {
            Language::CLike
        }
        _ => Language::Plain,
    }
}

pub(crate) fn highlighted_code(
    text: &str,
    language: Language,
    palette: ThemePalette,
    hunk: bool,
) -> StyledText {
    let display = if text.is_empty() { " " } else { text };
    if hunk {
        return StyledText::new(display.to_string()).with_highlights([(
            0..display.len(),
            HighlightStyle {
                color: Some(palette.faint.into()),
                ..Default::default()
            },
        )]);
    }
    StyledText::new(display.to_string()).with_highlights(
        highlight_spans(language, display)
            .into_iter()
            .filter(|(_, kind)| *kind != TokenKind::Default)
            .map(|(range, kind)| {
                (
                    range,
                    HighlightStyle {
                        color: Some(token_color(kind, palette).into()),
                        ..Default::default()
                    },
                )
            }),
    )
}

fn token_color(kind: TokenKind, palette: ThemePalette) -> Rgba {
    match kind {
        TokenKind::Default => palette.ink,
        TokenKind::Comment => palette.faint,
        TokenKind::String | TokenKind::Number => palette.warning,
        TokenKind::Keyword => palette.accent,
        TokenKind::Property => palette.ink_soft,
    }
}

pub(super) fn highlight_spans(language: Language, line: &str) -> Vec<(Range<usize>, TokenKind)> {
    let mut spans = Vec::new();
    let bytes = line.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i].is_ascii_whitespace() {
            i += 1;
            continue;
        }
        if let Some((end, kind)) = match_token(language, line, i) {
            if kind != TokenKind::Default {
                spans.push((i..end, kind));
            }
            i = end;
            continue;
        }
        i += char_len(line, i);
    }
    spans
}

fn match_token(language: Language, line: &str, start: usize) -> Option<(usize, TokenKind)> {
    if let Some(end) = match_comment(language, line, start) {
        return Some((end, TokenKind::Comment));
    }
    if language == Language::Html && line.as_bytes()[start] == b'<' {
        return match_html_tag(line, start);
    }
    if let Some((end, kind)) = match_string(language, line, start) {
        return Some((end, kind));
    }
    if let Some(end) = match_number(line, start) {
        return Some((end, TokenKind::Number));
    }
    if let Some((end, ident)) = match_ident(line, start) {
        if is_keyword(language, ident) {
            return Some((end, TokenKind::Keyword));
        }
        if language == Language::Css {
            let mut j = end;
            let bytes = line.as_bytes();
            while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                j += 1;
            }
            if bytes.get(j) == Some(&b':') {
                return Some((end, TokenKind::Property));
            }
        }
        return Some((end, TokenKind::Default));
    }
    None
}

fn match_comment(language: Language, line: &str, start: usize) -> Option<usize> {
    let rest = &line[start..];
    let line_markers: &[&str] = match language {
        Language::Python | Language::Yaml | Language::Toml | Language::Shell => &["#"],
        Language::Sql => &["--", "#"],
        Language::Css | Language::Html | Language::Json => &[],
        Language::Plain => &["//", "#"],
        _ => &["//"],
    };
    if line_markers.iter().any(|marker| rest.starts_with(marker)) {
        return Some(line.len());
    }
    let block = !matches!(
        language,
        Language::Python | Language::Yaml | Language::Toml | Language::Shell | Language::Json
    );
    if block && rest.starts_with("/*") {
        return Some(
            rest.find("*/")
                .map(|offset| start + offset + 2)
                .unwrap_or(line.len()),
        );
    }
    None
}

fn match_string(language: Language, line: &str, start: usize) -> Option<(usize, TokenKind)> {
    let bytes = line.as_bytes();
    let quote = bytes[start] as char;
    let quotes = match language {
        Language::Json => "\"",
        Language::Html => "\"'",
        Language::Rust => "\"'",
        _ => "\"'`",
    };
    if !quotes.contains(quote) {
        return None;
    }
    if language == Language::Rust && quote == '\'' {
        let rest = &line[start + 1..];
        if rest
            .chars()
            .next()
            .is_some_and(|ch| ch.is_alphabetic() || ch == '_')
            && !rest.starts_with("\\")
            && rest.chars().nth(1) != Some('\'')
        {
            return None;
        }
    }
    let end = close_string(line, start, quote)?;
    if language == Language::Json {
        let mut j = end;
        while j < bytes.len() && bytes[j].is_ascii_whitespace() {
            j += 1;
        }
        if bytes.get(j) == Some(&b':') {
            return Some((end, TokenKind::Property));
        }
    }
    Some((end, TokenKind::String))
}

fn close_string(line: &str, start: usize, quote: char) -> Option<usize> {
    let mut escaped = false;
    for (offset, ch) in line[start..].char_indices().skip(1) {
        if escaped {
            escaped = false;
            continue;
        }
        if ch == '\\' {
            escaped = true;
            continue;
        }
        if ch == quote {
            return Some(start + offset + ch.len_utf8());
        }
    }
    Some(line.len())
}

fn match_number(line: &str, start: usize) -> Option<usize> {
    let bytes = line.as_bytes();
    if !bytes[start].is_ascii_digit() {
        return None;
    }
    let mut end = start + 1;
    while end < bytes.len() {
        let ch = bytes[end];
        if ch.is_ascii_alphanumeric() || ch == b'_' || ch == b'.' {
            end += 1;
        } else {
            break;
        }
    }
    Some(end)
}

fn match_ident(line: &str, start: usize) -> Option<(usize, &str)> {
    let mut chars = line[start..].char_indices();
    let (_, first) = chars.next()?;
    if !is_ident_start(first) {
        return None;
    }
    let mut end = start + first.len_utf8();
    for (offset, ch) in chars {
        if !is_ident_continue(ch) {
            break;
        }
        end = start + offset + ch.len_utf8();
    }
    Some((end, &line[start..end]))
}

fn match_html_tag(line: &str, start: usize) -> Option<(usize, TokenKind)> {
    let bytes = line.as_bytes();
    let mut i = start + 1;
    if bytes.get(i) == Some(&b'/') {
        i += 1;
    }
    let ident_start = i;
    while i < bytes.len() && (bytes[i].is_ascii_alphanumeric() || bytes[i] == b'-') {
        i += 1;
    }
    if i == ident_start {
        return None;
    }
    Some((i, TokenKind::Keyword))
}

fn is_ident_start(ch: char) -> bool {
    ch.is_alphabetic() || ch == '_' || ch == '$'
}

fn is_ident_continue(ch: char) -> bool {
    is_ident_start(ch) || ch.is_ascii_digit()
}

fn char_len(line: &str, start: usize) -> usize {
    line[start..]
        .chars()
        .next()
        .map(char::len_utf8)
        .unwrap_or(1)
}

fn is_keyword(language: Language, ident: &str) -> bool {
    keywords(language).contains(&ident)
}

fn keywords(language: Language) -> &'static [&'static str] {
    match language {
        Language::Rust => &[
            "as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum",
            "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod",
            "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super",
            "trait", "true", "type", "unsafe", "use", "where", "while",
        ],
        Language::Go => &[
            "break",
            "case",
            "chan",
            "const",
            "continue",
            "default",
            "defer",
            "else",
            "fallthrough",
            "false",
            "for",
            "func",
            "go",
            "goto",
            "if",
            "import",
            "interface",
            "iota",
            "map",
            "nil",
            "package",
            "range",
            "return",
            "select",
            "struct",
            "switch",
            "true",
            "type",
            "var",
        ],
        Language::JavaScript => &[
            "async",
            "await",
            "break",
            "case",
            "catch",
            "class",
            "const",
            "continue",
            "debugger",
            "default",
            "delete",
            "do",
            "else",
            "export",
            "extends",
            "false",
            "finally",
            "for",
            "from",
            "function",
            "if",
            "import",
            "in",
            "instanceof",
            "let",
            "new",
            "null",
            "of",
            "return",
            "static",
            "super",
            "switch",
            "this",
            "throw",
            "true",
            "try",
            "typeof",
            "undefined",
            "var",
            "void",
            "while",
            "with",
            "yield",
        ],
        Language::Python => &[
            "False", "None", "True", "and", "as", "assert", "async", "await", "break", "class",
            "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global",
            "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise",
            "return", "try", "while", "with", "yield",
        ],
        Language::Json => &["false", "null", "true"],
        Language::Toml => &["true", "false"],
        Language::Sql => &[
            "and", "as", "asc", "by", "case", "create", "desc", "else", "end", "from", "group",
            "having", "in", "insert", "into", "join", "limit", "not", "null", "on", "or", "order",
            "select", "set", "table", "then", "update", "values", "when", "where",
        ],
        Language::CLike => &[
            "bool",
            "break",
            "case",
            "catch",
            "char",
            "class",
            "const",
            "continue",
            "default",
            "else",
            "enum",
            "false",
            "float",
            "for",
            "if",
            "int",
            "namespace",
            "new",
            "nullptr",
            "override",
            "private",
            "protected",
            "public",
            "return",
            "static",
            "struct",
            "switch",
            "template",
            "this",
            "throw",
            "true",
            "try",
            "typename",
            "void",
            "while",
        ],
        Language::Css => &["and", "from", "important", "not", "only", "or", "to", "var"],
        Language::Yaml | Language::Shell | Language::Html | Language::Plain => {
            &["false", "true", "null"]
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rust_keywords_strings_and_comments_are_classified() {
        let line = "fn main() { let name = \"azem\"; // done";
        let spans = highlight_spans(Language::Rust, line);
        assert_eq!(&line[spans[0].0.clone()], "fn");
        assert_eq!(spans[0].1, TokenKind::Keyword);
        assert!(
            spans
                .iter()
                .any(|(range, kind)| *kind == TokenKind::String
                    && &line[range.clone()] == "\"azem\"")
        );
        assert!(
            spans.iter().any(|(range, kind)| *kind == TokenKind::Comment
                && line[range.clone()].starts_with("//"))
        );
    }

    #[test]
    fn json_keys_are_properties() {
        let line = "{\"path\": \"main.rs\", \"ok\": true}";
        let spans = highlight_spans(Language::Json, line);
        assert!(spans.iter().any(
            |(range, kind)| *kind == TokenKind::Property && &line[range.clone()] == "\"path\""
        ));
        assert!(
            spans
                .iter()
                .any(|(range, kind)| *kind == TokenKind::Keyword && &line[range.clone()] == "true")
        );
    }

    #[test]
    fn language_for_path_uses_extension() {
        assert_eq!(language_for_path("gpui/src/main.rs"), Language::Rust);
        assert_eq!(language_for_path("frontend/App.tsx"), Language::JavaScript);
        assert_eq!(language_for_path("README"), Language::Plain);
    }
}
