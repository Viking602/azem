package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/recap"
)

func TestScrollbarClickAndDragMovesTranscript(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	for index := range 40 {
		model.transcript = append(model.transcript, Block{Kind: BlockUser, Content: fmt.Sprintf("message %02d", index)})
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(AppModel)
	_, top, width, height := model.transcriptBounds()
	maxOffset := model.transcriptMaxOffset()
	if maxOffset == 0 {
		t.Fatal("test transcript does not overflow")
	}

	updated, _ = model.Update(tea.MouseClickMsg{X: width, Y: top, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptTop != maxOffset {
		t.Fatalf("top scrollbar click offset = %d, want %d", model.transcriptTop, maxOffset)
	}
	updated, _ = model.Update(tea.MouseMotionMsg{X: width, Y: top + height - 1, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseReleaseMsg{X: width, Y: top + height - 1, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptTop != 0 {
		t.Fatalf("bottom scrollbar drag offset = %d, want 0", model.transcriptTop)
	}
}

func TestTranscriptSupportsMouseAndKeyboardScrolling(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	for index := range 24 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant, Title: "Azem", Content: fmt.Sprintf("message %02d", index), State: "completed",
		})
	}
	viewportWidth, viewportHeight := model.transcriptViewportSize()
	latest := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight))
	if !strings.Contains(latest, "message 23") {
		t.Fatalf("latest transcript is not anchored to the bottom:\n%s", latest)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{X: 2, Y: 3, Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.transcriptTop == 0 {
		t.Fatal("mouse wheel did not move transcript history")
	}
	older := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight))
	if older == latest {
		t.Fatal("mouse wheel left the transcript viewport unchanged")
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if top := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight)); !strings.Contains(top, "message 00") {
		t.Fatalf("Ctrl+Home did not reach oldest transcript content:\n%s", top)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if model.transcriptTop != 0 {
		t.Fatalf("Ctrl+End transcript offset = %d", model.transcriptTop)
	}
}

func TestTranscriptScrollbarTracksHistoryPositionAtPaneRightEdge(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 80 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant, Title: "Azem", Content: fmt.Sprintf("message %02d", index), State: "completed",
		})
	}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()

	thumbRows := func() []int {
		body := strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n")
		rows := make([]int, 0)
		for row, line := range body {
			if strings.Contains("▁▂▃▄▅▆▇█", ansi.Cut(line, transcriptWidth, transcriptWidth+1)) {
				rows = append(rows, row)
			}
		}
		return rows
	}

	latest := thumbRows()
	if len(latest) == 0 || latest[len(latest)-1] != transcriptHeight-1 {
		t.Fatalf("latest scrollbar thumb rows = %v, want bottom row %d", latest, transcriptHeight-1)
	}
	model.transcriptTop = model.transcriptMaxOffset()
	oldest := thumbRows()
	if len(oldest) == 0 || oldest[0] != 0 {
		t.Fatalf("oldest scrollbar thumb rows = %v, want top row", oldest)
	}
	if fmt.Sprint(oldest) == fmt.Sprint(latest) {
		t.Fatalf("scrollbar thumb did not move: latest=%v oldest=%v", latest, oldest)
	}
}

func TestTranscriptScrollbarAppearsOnlyWhenHistoryOverflows(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "short history", State: "completed"}}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()

	rightEdge := func() string {
		var edge strings.Builder
		for _, line := range strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n") {
			edge.WriteString(ansi.Cut(line, transcriptWidth, transcriptWidth+1))
		}
		return edge.String()
	}
	if edge := rightEdge(); strings.TrimSpace(edge) != "" {
		t.Fatalf("non-overflowing transcript rendered a scrollbar: %q", edge)
	}

	model.transcript = nil
	for range 80 {
		model.transcript = append(model.transcript, Block{Kind: BlockAssistant, Content: "history line", State: "completed"})
	}
	if edge := rightEdge(); !strings.ContainsAny(edge, "▁▂▃▄▅▆▇█") {
		t.Fatalf("overflowing transcript lacks a scrollbar thumb: %q", edge)
	}
}

func TestScrollbarThumbAdvancesByEighthCell(t *testing.T) {
	start, startSize := transcriptScrollbarThumb(20, 1000, 982, 0)
	next, nextSize := transcriptScrollbarThumb(20, 1000, 982, 6)
	if start-next != 1 {
		t.Fatalf("eighth-cell thumb movement = %d -> %d, want one eighth-cell", start, next)
	}
	if glyph, reverse := scrollbarThumbGlyph(19, start, startSize); glyph != "█" || reverse {
		t.Fatalf("aligned thumb cell = %q reverse:%v, want full block", glyph, reverse)
	}
	if glyph, reverse := scrollbarThumbGlyph(18, next, nextSize); glyph != "▁" || reverse {
		t.Fatalf("leading eighth-cell = %q reverse:%v, want lower one-eighth block", glyph, reverse)
	}
	if glyph, reverse := scrollbarThumbGlyph(19, next, nextSize); glyph != "▁" || !reverse {
		t.Fatalf("trailing seven-eighths cell = %q reverse:%v, want reversed lower one-eighth block", glyph, reverse)
	}
}

func TestTranscriptScrollbarReservesRightmostColumnWithoutContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	for range 80 {
		model.transcript = append(model.transcript, Block{Kind: BlockAssistant, Content: "history line", State: "completed"})
	}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()
	if transcriptWidth != model.width-1 {
		t.Fatalf("transcript width = %d, want scrollbar-reserved width %d", transcriptWidth, model.width-1)
	}
	for row, line := range strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n") {
		if ansi.StringWidth(line) != model.width {
			t.Fatalf("body row %d width = %d, want %d", row, ansi.StringWidth(line), model.width)
		}
		glyph := ansi.Cut(line, model.width-1, model.width)
		if glyph != "│" && !strings.Contains("▁▂▃▄▅▆▇█", glyph) {
			t.Fatalf("body row %d rightmost glyph = %q, want scrollbar", row, glyph)
		}
	}
}

func TestTranscriptDragSelectionClampsToConversationPaneAndCopies(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "dialogue only\nsecond dialogue line", State: "completed"}}
	if view := model.View(); view.MouseMode != tea.MouseModeAllMotion {
		t.Fatalf("default view mouse mode = %v", view.MouseMode)
	}
	if status := ansi.Strip(model.renderStatus(140)); !strings.Contains(status, "Drag copy") {
		t.Fatalf("drag selection gesture is not visible in status: %q", status)
	}
	_, top, transcriptWidth, transcriptHeight := model.transcriptBounds()
	lines := strings.Split(ansi.Strip(model.renderTranscript(transcriptWidth, transcriptHeight)), "\n")
	row := -1
	column := 0
	for index, line := range lines {
		if offset := strings.Index(line, "dialogue only"); offset >= 0 {
			row, column = index, offset
			break
		}
	}
	if row < 0 {
		t.Fatalf("dialogue fixture was not rendered:\n%s", strings.Join(lines, "\n"))
	}

	var copied string
	previousClipboard := writeClipboard
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { writeClipboard = previousClipboard })
	updated, _ = model.Update(tea.MouseClickMsg{X: column, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseMotionMsg{X: 119, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, command := model.Update(tea.MouseReleaseMsg{X: 119, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptSelection == nil || model.transcriptSelection.endX != transcriptWidth-1 {
		t.Fatalf("selection escaped transcript width %d: %#v", transcriptWidth, model.transcriptSelection)
	}
	if command == nil {
		t.Fatal("selection release did not copy")
	}
	command()
	if !strings.Contains(copied, "dialogue only") || strings.Contains(copied, "RUN CONTEXT") || strings.Contains(copied, "TODO") {
		t.Fatalf("copied selection crossed into context rail: %q", copied)
	}
}

func TestTranscriptSelectionBackgroundSurvivesNestedMarkdownStyles(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	styled := model.theme.Assistant.Render("plain ") + model.theme.DiffAdd.Render("inline code") + model.theme.Assistant.Render(" tail")
	width := ansi.StringWidth(styled)
	model.transcriptSelection = &transcriptSelection{startX: 0, startY: 0, endX: width - 1, endY: 0}

	highlighted := model.highlightTranscriptSelection([]string{styled}, width)[0]
	wanted := model.theme.Selected.Render(ansi.Strip(styled))
	if !strings.Contains(highlighted, wanted) {
		t.Fatalf("nested ANSI styles interrupted selection background:\nwant segment: %q\ngot:          %q", wanted, highlighted)
	}
}

func TestTranscriptScrollStopsFollowingStreamingTail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{Kind: BlockAssistant, RunID: "run-1", Title: "Azem", Content: strings.Repeat("older content ", 80), State: "running"}}
	model.scrollTranscript(4)
	width, height := model.transcriptViewportSize()
	before := ansi.Strip(model.renderTranscript(width, height))

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventTextDelta, RunID: "run-1", Text: strings.Repeat("new content ", 20)}})
	model = updated.(AppModel)
	after := ansi.Strip(model.renderTranscript(width, height))
	if after != before {
		t.Fatalf("streaming content moved a transcript scrolled into history:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestTranscriptOldestPositionStaysPinnedDuringStreaming(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{Kind: BlockAssistant, RunID: "run-1", Title: "Azem", Content: strings.Repeat("oldest content ", 80), State: "running"}}
	model.transcriptTop = model.transcriptMaxOffset()

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventTextDelta, RunID: "run-1", Text: strings.Repeat("new content ", 20)}})
	model = updated.(AppModel)
	if want := model.transcriptMaxOffset(); model.transcriptTop != want {
		t.Fatalf("oldest position offset = %d, want new maximum %d", model.transcriptTop, want)
	}
}

func TestTranscriptBodyDoesNotJumpWhenLongFinalAnswerCompletes(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{
		Kind: BlockAssistant, RunID: "run-1", Title: "Azem",
		Content: strings.Repeat("final answer line\n", 40), State: "streaming",
	}}
	model.scrollTranscript(5)
	width, height := model.transcriptViewportSize()
	before := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventRunFinished, RunID: "run-1"}})
	model = updated.(AppModel)
	after := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	if got, want := strings.Join(after[:height-1], "\n"), strings.Join(before[:height-1], "\n"); got != want {
		t.Fatalf("finalization shifted the transcript body:\nbefore:\n%s\nafter:\n%s", want, got)
	}
}

func TestTranscriptNarrowViewportUsesRenderedVisualLineCount(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 8, Height: 8})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Title: "Azem", Content: "abcdefghijklmnopqrstuvwxyz", State: "completed"}}
	width, height := model.transcriptViewportSize()
	lineCount := len(model.transcriptLines(max(1, width-4)))
	want := model.transcriptOffsetLimit(lineCount, height)
	if got := model.transcriptMaxOffset(); got != want {
		t.Fatalf("narrow transcript max offset = %d, want rendered visual-line offset %d", got, want)
	}
}

func TestRecapMouseWheelMatchesTranscriptScrollStep(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.overlay = OverlayRecap
	model.recap = &recap.Recap{Summary: strings.Repeat("long recap line\n", 40)}
	model.transcriptTop = 7
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != 3 || model.transcriptTop != 7 {
		t.Fatalf("recap wheel state = overlay:%d transcript:%d", model.overlayScroll, model.transcriptTop)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.overlayScroll != 0 {
		t.Fatalf("recap wheel did not return to top: %d", model.overlayScroll)
	}
	for range 100 {
		updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		model = updated.(AppModel)
	}
	if model.overlayScroll != model.recapScrollLimit() {
		t.Fatalf("recap wheel escaped valid range: offset=%d limit=%d", model.overlayScroll, model.recapScrollLimit())
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.overlayScroll != max(0, model.recapScrollLimit()-3) {
		t.Fatalf("recap reverse wheel remained stuck after overscroll: %d", model.overlayScroll)
	}
}

func TestTranscriptLayoutCacheReusesStableRender(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.transcript = []Block{{
		Kind: BlockAssistant, Content: "## Cached\n\n- one\n- two", State: "completed",
	}}
	first := model.transcriptLines(72)
	second := model.transcriptLines(72)
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatal("unchanged transcript layout was rendered again")
	}
	model.transcript[0].Content = "## Updated\n\n- three"
	updated := model.transcriptLines(72)
	if len(updated) == 0 || &first[0] == &updated[0] {
		t.Fatal("changed transcript reused a stale layout")
	}
	if output := ansi.Strip(strings.Join(updated, "\n")); !strings.Contains(output, "Updated") || strings.Contains(output, "Cached") {
		t.Fatalf("updated transcript layout is stale:\n%s", output)
	}

	stable := model.transcriptLines(72)
	model.status = "Running"
	model.runID = "run-1"
	model.animationFrame++
	afterAnimation := model.transcriptLines(72)
	if &stable[0] != &afterAnimation[0] {
		t.Fatal("animation frame rerendered a transcript block without an animated indicator")
	}
}

func TestViewScrollReusesTranscriptLayout(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	model.transcript = []Block{{
		Kind:    BlockAssistant,
		Content: strings.Repeat("scroll performance content ", 200),
		State:   "completed",
	}}

	_ = model.View()
	contentWidth := max(1, bodyTranscriptWidth(model.width, model.paint.bodyHeight)-4)
	before := model.transcriptLines(contentWidth)
	model.scrollTranscript(3)
	_ = model.View()
	after := model.transcriptLines(contentWidth)

	if len(before) == 0 || len(after) == 0 || &before[0] != &after[0] {
		t.Fatal("pure scrolling invalidated the stable transcript layout")
	}
}

func BenchmarkLongTranscriptScroll(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 120 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant,
			Content: fmt.Sprintf(
				"## Finding %d\n\n**Summary:** terminal rendering must remain responsive.\n\n- first detail\n- second detail\n- third detail",
				index,
			),
			State: "completed",
		})
	}
	_ = model.View()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		model.scrollTranscript(3)
		_ = model.View()
	}
}

func BenchmarkLongTranscriptHover(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 120 {
		model.transcript = append(model.transcript, Block{
			Kind:    BlockAssistant,
			Content: fmt.Sprintf("## Finding %d\n\n- first detail\n- second detail\n- third detail", index),
			State:   "completed",
		})
	}
	model.transcript = append(model.transcript,
		Block{Kind: BlockTool, Title: "coding.shell", Arguments: `{"command":"first"}`, State: "completed", Collapsed: true},
		Block{Kind: BlockTool, Title: "coding.shell", Arguments: `{"command":"second"}`, State: "completed", Collapsed: true},
	)
	_ = model.View()
	_, top, width, height := model.transcriptBounds()
	headerRows := make([]int, 0, 2)
	for row := range height {
		index, ok := model.transcriptBlockHeaderAt(row, width, height)
		if ok && index >= len(model.transcript)-2 {
			headerRows = append(headerRows, row)
		}
	}
	if len(headerRows) != 2 {
		b.Fatalf("visible tool headers=%v", headerRows)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		updated, _ = model.Update(tea.MouseMotionMsg{X: 4, Y: top + headerRows[index%2]})
		model = updated.(AppModel)
		_ = model.View()
	}
}

func BenchmarkAgentDetailToolChurn(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 200, 60
	blocks := make([]Block, 0, 49)
	output := strings.Repeat("tool output line with enough content to render\n", 40)
	for index := range 48 {
		blocks = append(blocks, Block{
			ID: fmt.Sprintf("completed-%d", index), Kind: BlockTool, Title: "coding.shell",
			Content: output, State: "completed", Collapsed: true,
		})
	}
	blocks = append(blocks, Block{ID: "running", Kind: BlockTool, Title: "coding.shell", State: "running"})
	model.detailAgentID = "child-1"
	model.agents = []AgentView{{ID: "child-1", State: "running", Blocks: blocks}}
	model.openOverlay(OverlayAgentDetail)
	_ = model.renderOverlay(model.width, model.height)
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		model.agents[0].Blocks[len(blocks)-1].Content = fmt.Sprintf("progress %d\n%s", index, output)
		delta := 3
		if index%2 == 1 {
			delta = -3
		}
		model.scrollAgentDetail(delta)
		_ = model.renderOverlay(model.width, model.height)
	}
}

func TestStickyInstructionPinsScrolledPastUserPrompt(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 30
	model.transcript = []Block{
		{Kind: BlockUser, RunID: "run-1", Content: "optimize the sticky prompt dialog"},
		{Kind: BlockAssistant, RunID: "run-1", Content: strings.Repeat("tool output line\n", 40)},
	}
	model.status = "Running"
	model.runID = "run-1"
	// Follow the bottom so the user prompt has left the viewport.
	model.transcriptTop = model.transcriptMaxOffset()

	content, ok := model.stickyInstructionContent(76, 12)
	if !ok || !strings.Contains(content, "optimize the sticky prompt dialog") {
		t.Fatalf("sticky content = %q ok=%v, want current instruction", content, ok)
	}
	card := ansi.Strip(model.renderStickyInstruction(80, 12))
	if !strings.Contains(card, "╭") || !strings.Contains(card, "›") || !strings.Contains(card, "optimize the sticky") {
		t.Fatalf("sticky dialog card is wrong:\n%s", card)
	}

	// When the prompt is still on screen, do not pin a duplicate.
	model.transcriptTop = 0
	model.status = "Ready"
	model.runID = ""
	if content, ok = model.stickyInstructionContent(76, 40); ok {
		t.Fatalf("sticky should hide while prompt is visible, got %q", content)
	}
}

func TestTranscriptScrollDoesNotReverseAtStickyBoundary(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 30
	model.transcriptLayout = &transcriptLayoutCache{
		initialized: true,
		lines:       make([]string, 100),
		userMarks:   []stickyUserMark{{content: "keep this prompt pinned", endLine: 80}},
	}
	model.paint.width = model.width
	model.paint.height = model.height
	model.paint.preBodyHeight = 20
	model.paint.bodyHeight = 16
	model.paint.lineCount = 100
	model.transcriptTop = 5

	if !model.stickyVisibleWithCache(model.paint.preBodyHeight) {
		t.Fatal("test setup is below the sticky dismissal boundary")
	}
	model.scrollTranscript(3)
	if model.transcriptTop != 8 {
		t.Fatalf("wheel up reversed at sticky boundary: offset=%d, want 8", model.transcriptTop)
	}
	if model.stickyVisibleWithCache(model.paint.preBodyHeight) {
		t.Fatal("sticky prompt remained visible after crossing its dismissal boundary")
	}
}

func TestTranscriptScrollDownStillReturnsToLatest(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: strings.Repeat("history ", 400), State: "completed"}}
	_ = model.View()
	model.scrollTranscript(12)
	if model.transcriptTop == 0 {
		t.Fatal("test transcript did not scroll into history")
	}
	model.scrollTranscript(-1000)
	if model.transcriptTop != 0 {
		t.Fatalf("scroll down offset = %d, want latest", model.transcriptTop)
	}
}

func TestInterruptedEmptyTurnScrollIsMonotonicAcrossStickyBoundary(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{
		{Kind: BlockUser, RunID: "run-previous", Content: "previous interrupted prompt"},
		{Kind: BlockUser, RunID: "run-current", Content: "continue"},
		{Kind: BlockAssistant, RunID: "run-current", Content: strings.Repeat("completed answer line ", 400), State: "completed"},
	}
	model.status = "Ready"
	model.runID = ""
	_ = model.View()

	previous := model.transcriptTop
	stickySeen := false
	for range 40 {
		model.scrollTranscript(3)
		if model.transcriptTop < previous {
			t.Fatalf("wheel up reversed after interrupted empty turn: offset=%d, previous=%d", model.transcriptTop, previous)
		}
		_ = model.View()
		stickySeen = stickySeen || model.paint.stickyHeight > 0
		previous = model.transcriptTop
		if previous == model.transcriptMaxOffsetForTop(previous) {
			break
		}
	}
	if !stickySeen {
		t.Fatal("test did not show a sticky prompt")
	}
}
