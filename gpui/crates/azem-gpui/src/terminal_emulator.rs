use alacritty_terminal::{Term, event::VoidListener, grid::Dimensions, term::Config, vte::ansi};

const DEFAULT_COLUMNS: usize = 120;
const DEFAULT_LINES: usize = 32;

#[derive(Clone, Copy)]
struct TerminalSize {
    columns: usize,
    lines: usize,
}

impl Dimensions for TerminalSize {
    fn total_lines(&self) -> usize {
        self.lines
    }

    fn screen_lines(&self) -> usize {
        self.lines
    }

    fn columns(&self) -> usize {
        self.columns
    }
}

pub struct TerminalEmulator {
    parser: ansi::Processor,
    terminal: Term<VoidListener>,
}

#[derive(Debug, PartialEq, Eq)]
pub struct TerminalLine {
    pub before_cursor: String,
    pub after_cursor: String,
    pub has_cursor: bool,
}

impl Default for TerminalEmulator {
    fn default() -> Self {
        let size = TerminalSize {
            columns: DEFAULT_COLUMNS,
            lines: DEFAULT_LINES,
        };
        Self {
            parser: ansi::Processor::new(),
            terminal: Term::new(Config::default(), &size, VoidListener),
        }
    }
}

impl TerminalEmulator {
    pub fn feed(&mut self, bytes: &[u8]) {
        self.parser.advance(&mut self.terminal, bytes);
    }

    pub fn resize(&mut self, columns: usize, lines: usize) {
        let size = TerminalSize {
            columns: columns.max(2),
            lines: lines.max(1),
        };
        self.terminal.resize(size);
    }

    pub fn visible_lines(&self) -> Vec<TerminalLine> {
        let content = self.terminal.renderable_content();
        let cursor = content.cursor;
        let cursor_visible = cursor.shape != ansi::CursorShape::Hidden;
        let mut lines = Vec::new();
        let mut current_row = None;
        let mut cells = Vec::new();

        for indexed in content.display_iter {
            let row = indexed.point.line.0;
            if current_row.is_some_and(|current| current != row) {
                lines.push(terminal_line(
                    current_row.unwrap_or_default(),
                    &cells,
                    cursor_visible,
                    cursor.point.line.0,
                    cursor.point.column.0,
                ));
                cells.clear();
            }
            current_row = Some(row);
            cells.push(indexed.cell.c);
        }
        if let Some(row) = current_row {
            lines.push(terminal_line(
                row,
                &cells,
                cursor_visible,
                cursor.point.line.0,
                cursor.point.column.0,
            ));
        }
        while lines.last().is_some_and(|line| {
            !line.has_cursor && line.before_cursor.is_empty() && line.after_cursor.is_empty()
        }) {
            lines.pop();
        }
        lines
    }
}

fn terminal_line(
    row: i32,
    cells: &[char],
    cursor_visible: bool,
    cursor_row: i32,
    cursor_column: usize,
) -> TerminalLine {
    let has_cursor = cursor_visible && row == cursor_row;
    let content_end = cells
        .iter()
        .rposition(|character| !character.is_whitespace())
        .map_or(0, |index| index + 1)
        .max(if has_cursor { cursor_column } else { 0 })
        .min(cells.len());
    let split = if has_cursor {
        cursor_column.min(content_end)
    } else {
        content_end
    };
    TerminalLine {
        before_cursor: cells[..split].iter().collect(),
        after_cursor: cells[split..content_end].iter().collect(),
        has_cursor,
    }
}

#[cfg(test)]
mod tests {
    use super::TerminalEmulator;

    #[test]
    fn strips_terminal_control_sequences_through_vte_state() {
        let mut terminal = TerminalEmulator::default();
        terminal.feed(b"hello\rworld\n\x1b[31mred\x1b[0m");
        let visible = terminal
            .visible_lines()
            .into_iter()
            .map(|line| line.before_cursor + &line.after_cursor)
            .collect::<Vec<_>>()
            .join("\n");
        assert!(visible.contains("world"));
        assert!(visible.contains("red"));
        assert!(!visible.contains("\x1b"));
    }

    #[test]
    fn exposes_the_terminal_cursor_without_changing_the_cell_text() {
        let mut terminal = TerminalEmulator::default();
        terminal.feed(b"$ abc");
        let line = terminal
            .visible_lines()
            .into_iter()
            .find(|line| line.has_cursor)
            .unwrap();
        assert_eq!(line.before_cursor, "$ abc");
        assert!(line.after_cursor.is_empty());
    }
}
