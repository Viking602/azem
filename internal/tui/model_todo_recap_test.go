package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

func TestTodoPaneFilteringStaleEventsAndBoundedRendering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Goal: "Ship", Revision: 3, Phases: []session.TodoPhase{{
		ID: "phase-1", Title: "Build", Items: []session.TodoItem{
			{ID: "done", Content: "Analyze", Status: session.TodoCompleted},
			{ID: "current", Content: "Implement", Status: session.TodoInProgress},
			{ID: "next", Content: "Verify", Status: session.TodoPending},
		},
	}}}
	model.todoExpanded = true
	model.focus = focusTodo
	model.width, model.height = 80, 24
	if model.visibleTodoItemCount() != 3 {
		t.Fatalf("todo item count=%d, want 3", model.visibleTodoItemCount())
	}
	updated, _ := model.updateTodoKey("h")
	model = updated.(AppModel)
	if !model.todoHideCompleted || model.visibleTodoItemCount() != 2 {
		t.Fatalf("hidden todo count=%d hide=%v", model.visibleTodoItemCount(), model.todoHideCompleted)
	}
	filtered := ansi.Strip(model.renderTodoPane(80, 3))
	if strings.Contains(filtered, "Analyze") || !strings.Contains(filtered, "Implement") || !strings.Contains(filtered, "Verify") {
		t.Fatalf("todo pane filtering is wrong:\n%s", filtered)
	}

	model.applyEvent(app.Event{Kind: app.EventTodoUpdated, Todo: &session.TodoList{Revision: 2}})
	if model.todo.Revision != 3 {
		t.Fatalf("stale todo event regressed revision to %d", model.todo.Revision)
	}
	model.todoHideCompleted = false
	for _, size := range [][2]int{{20, 8}, {40, 12}, {80, 24}, {120, 40}} {
		model.width, model.height = size[0], size[1]
		rendered := model.View().Content
		lines := strings.Split(rendered, "\n")
		if len(lines) != size[1] {
			t.Fatalf("todo view height=%d, want %d at %v", len(lines), size[1], size)
		}
		for lineNumber, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("todo view line %d width=%d, want <=%d", lineNumber+1, width, size[0])
			}
		}
	}
	if rail := model.renderContextRail(31, 16); !strings.Contains(rail, ";9m") {
		t.Fatalf("completed rail todo is not rendered with strikethrough: %q", rail)
	}
}

func TestInlineTodoPaneScrollsAndCloseControlCollapses(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	items := make([]session.TodoItem, 12)
	for index := range items {
		items[index] = session.TodoItem{
			ID:      fmt.Sprintf("todo-%02d", index),
			Content: fmt.Sprintf("Task %02d", index),
			Status:  session.TodoPending,
		}
	}
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: items}}}
	model.width, model.height = 100, 30
	model.todoExpanded = true
	model.focus = focusTodo
	model.composer.Blur()

	_, top, width, height := model.todoPaneBounds()
	// 30-row terminal × 30% cap → room for more of a long todo list.
	if height < 8 {
		t.Fatalf("todo pane height=%d, want at least 8", height)
	}
	initial := ansi.Strip(model.renderTodoPane(width, height))
	if !strings.Contains(initial, "Task 00") || !strings.ContainsAny(initial, "▁▂▃▄▅▆▇█") {
		t.Fatalf("scrollable todo pane omitted first item or thumb:\n%s", initial)
	}

	updated, _ := model.Update(tea.MouseWheelMsg{X: 10, Y: top + 1, Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.todoScroll != 1 {
		t.Fatalf("todo wheel offset=%d, want 1", model.todoScroll)
	}
	updated, _ = model.updateTodoKey("end")
	model = updated.(AppModel)
	if model.todoScroll != model.todoPaneScrollLimit(height) {
		t.Fatalf("todo end offset=%d, want %d", model.todoScroll, model.todoPaneScrollLimit(height))
	}

	updated, _ = model.Update(tea.MouseClickMsg{X: width - 1, Y: top, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.todoExpanded || model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("todo close = expanded:%t focus:%d composer:%t", model.todoExpanded, model.focus, model.composer.Focused())
	}
}

func TestTodoPaneHeightTracksContentUntilMaximum(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 100
	model.todoExpanded = true

	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: []session.TodoItem{
		{Content: "First", Status: session.TodoPending},
		{Content: "Second", Status: session.TodoPending},
	}}}}
	_, _, _, height := model.todoPaneBounds()
	if height != 2 {
		t.Fatalf("short todo pane height=%d, want content height 2", height)
	}

	items := make([]session.TodoItem, maxTodoPaneHeight+5)
	for index := range items {
		items[index] = session.TodoItem{Content: fmt.Sprintf("Task %02d", index), Status: session.TodoPending}
	}
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: items}}}
	_, _, width, height := model.todoPaneBounds()
	if height != maxTodoPaneHeight {
		t.Fatalf("long todo pane height=%d, want maximum %d", height, maxTodoPaneHeight)
	}
	if limit := model.todoPaneScrollLimit(height); limit <= 0 {
		t.Fatalf("long todo scroll limit=%d, want positive", limit)
	}
	if pane := ansi.Strip(model.renderTodoPane(width, height)); !strings.ContainsAny(pane, "▁▂▃▄▅▆▇█") {
		t.Fatalf("long todo pane omitted scrollbar:\n%s", pane)
	}
}

func TestTodoRailShowsProgressInsteadOfInternalRevision(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	emptyRail := ansi.Strip(model.renderContextRail(31, 16))
	if strings.Contains(emptyRail, "r0") || strings.TrimSpace(strings.Split(emptyRail, "\n")[2]) != "TODO" {
		t.Fatalf("empty todo header should hide revision: %q", emptyRail)
	}

	model.todo = session.TodoList{Revision: 9, Phases: []session.TodoPhase{{Items: []session.TodoItem{
		{Content: "done", Status: session.TodoCompleted},
		{Content: "working", Status: session.TodoInProgress},
		{Content: "cancelled", Status: session.TodoCancelled},
	}}}}
	rail := ansi.Strip(model.renderContextRail(31, 16))
	if strings.Contains(rail, "r9") || !strings.Contains(rail, "TODO  1/2") {
		t.Fatalf("todo header should show user-facing progress: %q", rail)
	}
}

func TestTodoPaneShowsPhaseBoundariesAndCompletedGroupRails(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Phases: []session.TodoPhase{
		{Title: "P1: 基础", Items: []session.TodoItem{
			{Content: "done-a", Status: session.TodoCompleted},
			{Content: "done-b", Status: session.TodoCompleted},
			{Content: "active", Status: session.TodoInProgress},
		}},
		{Title: "P2: 架构演进", Items: []session.TodoItem{
			{Content: "next", Status: session.TodoPending},
		}},
	}}
	model.todoExpanded = true
	model.focus = focusTodo
	pane := ansi.Strip(model.renderTodoPane(80, 10))
	for _, wanted := range []string{"▌ P1: 基础", "▌ P2: 架构演进", "┌ ✓", "└ ✓", "▶ active", "□ next"} {
		if !strings.Contains(pane, wanted) {
			t.Fatalf("todo pane missing phase boundary marker %q:\n%s", wanted, pane)
		}
	}
	// A blank gap separates the two phases so the list does not read as one block.
	lines := strings.Split(pane, "\n")
	p1, p2 := -1, -1
	for index, line := range lines {
		if strings.Contains(line, "P1: 基础") {
			p1 = index
		}
		if strings.Contains(line, "P2: 架构演进") {
			p2 = index
		}
	}
	if p1 < 0 || p2 <= p1+1 {
		t.Fatalf("phases are not separated:\n%s", pane)
	}
	foundGap := false
	for _, line := range lines[p1+1 : p2] {
		if strings.TrimSpace(ansi.Strip(line)) == "" || strings.Trim(strings.TrimSpace(line), "│┌┐└┘ ×┃") == "" {
			foundGap = true
			break
		}
	}
	if !foundGap {
		t.Fatalf("expected blank gap between phases:\n%s", pane)
	}
}

func TestTodoRowsAreNumberedAndBoundRunningSubagentsAnimate(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Title: "Work", Items: []session.TodoItem{
		{ID: "main", Content: "Main task", Status: session.TodoInProgress},
		{ID: "delegated", Content: "Delegated task", Status: session.TodoPending, SubagentRunID: "child-1"},
		{ID: "next", Content: "Next task", Status: session.TodoPending},
	}}}}
	model.agents = []AgentView{{ID: "child-1", State: "running"}}

	before := ansi.Strip(model.renderContextRail(40, 16))
	for _, wanted := range []string{"◐  1. Main task", "◐  2. Delegated task", "○  3. Next task"} {
		if !strings.Contains(before, wanted) {
			t.Fatalf("numbered todo rail omitted %q:\n%s", wanted, before)
		}
	}
	model.animationFrame++
	after := ansi.Strip(model.renderContextRail(40, 16))
	for _, wanted := range []string{"◓  1. Main task", "◓  2. Delegated task"} {
		if !strings.Contains(after, wanted) {
			t.Fatalf("running todo did not animate as %q:\n%s", wanted, after)
		}
	}

	model.todoExpanded = true
	model.focus = focusTodo
	pane := ansi.Strip(model.renderTodoPane(80, 5))
	for _, wanted := range []string{"▌ Work", "▶ Main task", "▶ Delegated task", "□ Next task"} {
		if !strings.Contains(pane, wanted) {
			t.Fatalf("inline todo pane omitted %q:\n%s", wanted, pane)
		}
	}
}

func TestMemoryAndRecapCommandsAndOverlays(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	runCommand := func(command Command) {
		updated, cmd := model.executeCommand(command)
		model = updated.(AppModel)
		if cmd == nil {
			t.Fatalf("/%s did not start an action: %s", command.Name, model.errorBanner)
		}
		updated, _ = model.Update(cmd())
		model = updated.(AppModel)
	}

	runCommand(Command{Name: "memory", Args: []string{"cache", "policy"}})
	runCommand(Command{Name: "remember", Args: []string{"Use", "workspace", "scope"}})
	runCommand(Command{Name: "forget", Args: []string{"mem_123"}})
	runCommand(Command{Name: "recap"})
	wantKinds := []ActionKind{ActionListMemories, ActionRemember, ActionForgetMemory, ActionShowRecap}
	if len(runtime.actions) != len(wantKinds) {
		t.Fatalf("memory actions = %#v", runtime.actions)
	}
	for index, kind := range wantKinds {
		if runtime.actions[index].Kind != kind || runtime.actions[index].SessionID != "session-1" {
			t.Fatalf("action %d = %#v, want %s for session-1", index, runtime.actions[index], kind)
		}
	}
	if runtime.actions[0].Target != "cache policy" || runtime.actions[1].Target != "Use workspace scope" {
		t.Fatalf("command arguments were not preserved: %#v", runtime.actions)
	}

	now := time.Now()
	model.applyEvent(app.Event{Kind: app.EventMemoryState, State: "listed", Memories: []memory.Memory{{
		ID: "mem_123", Content: "Use workspace-scoped native memory", Provenance: "manual", Importance: 50, UpdatedAt: now,
	}}})
	if model.overlay != OverlayMemory || model.overlayOptionCount() != 1 {
		t.Fatalf("memory overlay = %q count=%d", model.overlay, model.overlayOptionCount())
	}
	plain := ansi.Strip(model.renderOverlay(64, 16))
	if !strings.Contains(plain, "mem_123") || !strings.Contains(plain, "workspace-scoped") {
		t.Fatalf("memory overlay omitted evidence identity/content:\n%s", plain)
	}

	model.applyEvent(app.Event{Kind: app.EventRecapState, State: "loaded", Recap: &recap.Recap{
		SessionID: "session-1", Goal: "Implement native memory", Summary: "Storage is complete", OpenItems: "Verify TUI", CoveredBoundary: "run-7", Revision: 2,
	}})
	if model.overlay != OverlayRecap || model.recap == nil {
		t.Fatalf("recap overlay = %q recap=%#v", model.overlay, model.recap)
	}
	plain = ansi.Strip(model.renderOverlay(64, 16))
	for _, wanted := range []string{"Implement native memory", "Storage is complete", "Verify TUI", "run-7"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("recap overlay omitted %q:\n%s", wanted, plain)
		}
	}
}

func TestMemoryRecallAppearsInTranscriptWithoutOpeningOverlay(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	model.applyEvent(app.Event{
		Kind: app.EventMemoryState, SessionID: "session-1", State: "recalled",
		Data: map[string]string{"count": "2"},
	})
	if model.overlay != OverlayNone {
		t.Fatalf("memory recall opened overlay %q", model.overlay)
	}
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockHook {
		t.Fatalf("memory recall transcript = %#v", model.transcript)
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 64), "\n"))
	if !strings.Contains(rendered, "Recalled 2 workspace memories") {
		t.Fatalf("memory recall is not visible: %q", rendered)
	}
}

func TestRecapOverlayCachesLongLayoutAndOnlySlicesVisibleWindow(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	var summary strings.Builder
	for index := 0; index < 500; index++ {
		fmt.Fprintf(&summary, "recap row %03d contains enough context to wrap predictably\n", index)
	}
	model.recap = &recap.Recap{Goal: "Keep scrolling smooth", Summary: summary.String(), OpenItems: "Verify cached rendering", Revision: 1}
	model.openOverlay(OverlayRecap)

	first := model.recapDescriptionLines(72)
	second := model.recapDescriptionLines(72)
	if len(first) < 500 || &first[0] != &second[0] {
		t.Fatal("stable recap layout was wrapped again")
	}
	top := ansi.Strip(model.renderOverlay(80, 24))
	model.overlayScroll = 300
	scrolled := ansi.Strip(model.renderOverlay(80, 24))
	third := model.recapDescriptionLines(72)
	if &first[0] != &third[0] {
		t.Fatal("scrolling invalidated the recap layout cache")
	}
	if top == scrolled || strings.Count(scrolled, "\n")+1 != 24 {
		t.Fatalf("recap viewport was not sliced to the visible window:\n%s", scrolled)
	}

	model.recap.Summary = "updated recap"
	updated := model.recapDescriptionLines(72)
	if len(updated) == 0 || &first[0] == &updated[0] || !strings.Contains(strings.Join(updated, "\n"), "updated recap") {
		t.Fatal("changed recap reused stale cached lines")
	}
}

func TestRecapUpdateIsVisibleWhenIdleAndResumesWithSession(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	value := recap.Recap{
		SessionID: "session-1", Goal: "Ship visible recap", Summary: "Recap now stays visible near the composer",
		OpenItems: "Verify the overlay", CoveredBoundary: "run-9", Revision: 3,
	}
	model.status = "Running"
	model.runID = "run-9"
	model.applyEvent(app.Event{Kind: app.EventRecapState, SessionID: "session-1", RunID: "run-9", State: "updated", Recap: &value})
	if model.overlay != OverlayNone || model.recap == nil {
		t.Fatalf("background recap update interrupted the UI: overlay=%q recap=%#v", model.overlay, model.recap)
	}
	if view := ansi.Strip(model.View().Content); strings.Contains(view, "※ recap:") {
		t.Fatalf("recap should not displace live run status:\n%s", view)
	}
	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "session-1", RunID: "run-9", State: "completed"})
	view := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"※ recap:", "Recap now stays visible near the composer", "/recap for details"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("visible recap status omitted %q:\n%s", wanted, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines != model.height {
		t.Fatalf("recap status escaped viewport: got %d lines, want %d", lines, model.height)
	}

	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "session-2", State: "loaded", Recap: &recap.Recap{
			SessionID: "session-2", Summary: "Resumed continuity", Revision: 1,
		}, Data: map[string]string{"blocks": "[]"},
	})
	if model.sessionID != "session-2" || model.recap == nil || model.recap.Summary != "Resumed continuity" {
		t.Fatalf("resumed recap was not restored: session=%q recap=%#v", model.sessionID, model.recap)
	}
}

func TestRecapStatusUsesConcisePlainTextPreview(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width = 240
	original := "## Result\n\n**Recap display is complete.** The full session memory remains available. Implementation details should stay in the overlay."
	model.recap = &recap.Recap{Summary: original}
	status := strings.TrimSpace(ansi.Strip(model.renderRecapStatus(model.width)))
	for _, unwanted := range []string{"##", "**", "Implementation details"} {
		if strings.Contains(status, unwanted) {
			t.Fatalf("recap status retained %q: %q", unwanted, status)
		}
	}
	for _, wanted := range []string{"Recap display is complete.", "The full session memory remains available."} {
		if !strings.Contains(status, wanted) {
			t.Fatalf("recap status omitted %q: %q", wanted, status)
		}
	}
	if model.recap.Summary != original {
		t.Fatalf("status preview mutated persistent recap: %q", model.recap.Summary)
	}

	preview := recapStatusPreview(strings.Repeat("context ", 80))
	if len(strings.Fields(strings.TrimSuffix(preview, "…"))) > 40 || ansi.StringWidth(preview) > 120 || !strings.HasSuffix(preview, "…") {
		t.Fatalf("recap preview was not concise: words=%d width=%d value=%q", len(strings.Fields(preview)), ansi.StringWidth(preview), preview)
	}
}

func TestRecapStatusFallsBackWhenSummaryHasNoPlainText(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "```go\nfmt.Println(\"details\")\n```", Goal: "Keep continuity visible"}
	status := ansi.Strip(model.renderRecapStatus(100))
	if !strings.Contains(status, "Keep continuity visible") || strings.Contains(status, "fmt.Println") {
		t.Fatalf("recap fallback status = %q", status)
	}
}

func TestRecapStatusKeepsMutedDarkTreatment(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep continuity visible"}
	status := model.renderRecapStatus(80)
	emptyBackground := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	if background := fmt.Sprint(model.theme.Muted.GetBackground()); background != emptyBackground {
		t.Fatalf("muted recap background = %s, want unset (%s)", background, emptyBackground)
	}
	if !strings.Contains(ansi.Strip(status), "Keep continuity visible") {
		t.Fatalf("recap text missing: %q", ansi.Strip(status))
	}
}

func TestRecapStatusYieldsWhileUserIsTyping(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep the transcript usable"}
	model.composer.SetValue("draft request")
	if status := model.visibleRecapStatus(80, 24); status != "" {
		t.Fatalf("recap status competed with composer draft: %q", ansi.Strip(status))
	}
}

func TestRecapStatusYieldsToTinyViewport(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep the transcript usable"}
	model.width = 24
	model.height = 2
	view := ansi.Strip(model.View().Content)
	if strings.Contains(view, "recap") || strings.Count(view, "\n")+1 != model.height {
		t.Fatalf("tiny viewport should omit recap status:\n%s", view)
	}
}

func TestRecapCommandPaletteOpensRecap(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	model.openOverlay(OverlayCommand)
	for index, option := range commandPaletteOptions {
		if option == "recap" {
			model.overlayCursor = index
			break
		}
	}
	updated, cmd := model.activatePaletteOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("recap command-palette item did not start an action")
	}
	updated, _ = model.Update(cmd())
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionShowRecap || runtime.actions[0].SessionID != "session-1" {
		t.Fatalf("recap palette actions = %#v", runtime.actions)
	}
}
