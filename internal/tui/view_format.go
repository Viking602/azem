package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func optionWindowStart(cursor int, total int, visible int) int {
	if total <= visible || visible <= 0 {
		return 0
	}
	start := cursor - visible/2
	if start < 0 {
		return 0
	}
	if start+visible > total {
		return total - visible
	}
	return start
}

func joinSides(left string, right string, width int) string {
	if width <= 0 {
		return ""
	}
	right = truncateStyledFallback(right, width)
	rightWidth := lipgloss.Width(right)
	gap := 0
	if left != "" && right != "" && width-rightWidth >= 2 {
		gap = 2
	}
	left = truncateStyledFallback(left, max(0, width-rightWidth-gap))
	leftWidth := lipgloss.Width(left)
	return left + strings.Repeat(" ", max(gap, width-leftWidth-rightWidth)) + right
}

func fitViewport(value string, width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		lines[index] = padStyledLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func padStyledLine(value string, width int) string {
	if ansi.StringWidth(value) > width {
		return ansi.Truncate(value, width, "…")
	}
	return value + strings.Repeat(" ", width-ansi.StringWidth(value))
}

func truncateStyledFallback(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return ansi.Truncate(value, max(0, width), "")
	}
	return ansi.Truncate(value, width, "…")
}

func wrapText(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(text, max(1, width), " "), "\n")
}

func padOrTrim(value string, width int) string {
	if width <= 0 {
		return ""
	}
	displayWidth := ansi.StringWidth(value)
	if displayWidth > width {
		if width == 1 {
			return ansi.Truncate(value, 1, "")
		}
		return ansi.Truncate(value, width, "…")
	}
	return value + strings.Repeat(" ", width-displayWidth)
}

func shortenPath(path string, width int) string {
	if utf8.RuneCountInString(path) <= width {
		return path
	}
	base := filepath.Base(path)
	if utf8.RuneCountInString(base)+2 <= width {
		return "…/" + base
	}
	runes := []rune(base)
	if len(runes) >= width {
		return "…" + string(runes[len(runes)-width+1:])
	}
	return base
}

func formatTokens(tokens int) string {
	tokens = max(0, tokens)
	switch {
	case tokens >= 1_000_000:
		return formatTokenUnit(tokens, 1_000_000, "M")
	case tokens >= 1_000:
		return formatTokenUnit(tokens, 1_000, "K")
	default:
		return strconv.Itoa(tokens)
	}
}

func formatTokenUnit(tokens int, unit int, suffix string) string {
	whole := tokens / unit
	if whole >= 10 {
		return strconv.Itoa(whole) + suffix
	}
	tenths := (tokens % unit) * 10 / unit
	if tenths == 0 {
		return strconv.Itoa(whole) + suffix
	}
	return fmt.Sprintf("%d.%d%s", whole, tenths, suffix)
}
