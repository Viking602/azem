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

    pub fn visible_text(&self) -> String {
        let mut output = String::new();
        let mut current_line = None;
        let mut line = String::new();
        for indexed in self.terminal.renderable_content().display_iter {
            let row = indexed.point.line.0;
            if current_line.is_some_and(|current| current != row) {
                output.push_str(line.trim_end());
                output.push('\n');
                line.clear();
            }
            current_line = Some(row);
            line.push(indexed.cell.c);
        }
        output.push_str(line.trim_end());
        output.trim_end().to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::TerminalEmulator;

    #[test]
    fn strips_terminal_control_sequences_through_vte_state() {
        let mut terminal = TerminalEmulator::default();
        terminal.feed(b"hello\rworld\n\x1b[31mred\x1b[0m");
        let visible = terminal.visible_text();
        assert!(visible.contains("world"));
        assert!(visible.contains("red"));
        assert!(!visible.contains("\x1b"));
    }
}
