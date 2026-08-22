package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/toolview"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

type cursorExecHost struct {
	bus                   *tool.Bus
	timeline              *durableToolTimeline
	events                providerHost
	workspace             string
	sessionID, eventRunID string
	agentID               string
}

func newCursorExecHost(events providerHost, workspace, sessionID, timelineRunID, eventRunID, agentID string, bus *tool.Bus) *cursorExecHost {
	var timeline *durableToolTimeline
	if events != nil {
		timeline = newDurableToolTimeline(events.Sessions(), workspace, sessionID, timelineRunID)
	}
	return &cursorExecHost{
		bus: bus, timeline: timeline, events: events, workspace: workspace,
		sessionID: sessionID, eventRunID: eventRunID, agentID: agentID,
	}
}

func (h *cursorExecHost) Execute(ctx context.Context, call message.ToolCall) (cursordriver.HostResult, error) {
	if h == nil || h.bus == nil {
		return cursordriver.HostResult{Content: "Tool not available", IsError: true, Code: cursordriver.DeleteCodeRejected}, nil
	}
	toolCall := tool.Call{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
	if call.Name == coding.ToolWriteFile {
		if rewritten, ok := h.rewriteExistingWrite(ctx, toolCall); ok {
			toolCall = rewritten
		}
	}
	driver, ok := h.bus.Driver(toolCall.Name)
	if !ok {
		return cursordriver.HostResult{Content: fmt.Sprintf("Tool %q not available", toolCall.Name), IsError: true, Code: cursordriver.DeleteCodeRejected}, nil
	}
	started := false
	var startErr error
	start := func() error {
		if started {
			return startErr
		}
		started = true
		startErr = h.start(ctx, toolCall)
		return startErr
	}
	result, err := driver.Execute(ctx, toolCall, func(update tool.Update) error {
		if update.Kind == "running" {
			return start()
		}
		return nil
	})
	if !started {
		startErr = start()
	}
	if startErr != nil {
		return cursordriver.HostResult{Content: startErr.Error(), IsError: true, Code: classifyDeleteError(startErr.Error())}, nil
	}
	if err != nil {
		result.IsError = true
		if strings.TrimSpace(result.Content) == "" {
			result.Content = err.Error()
		}
	}
	if finishErr := h.finish(ctx, toolCall, result); finishErr != nil {
		return cursordriver.HostResult{Content: finishErr.Error(), IsError: true, Code: classifyDeleteError(finishErr.Error())}, nil
	}
	code := ""
	if result.IsError && toolCall.Name == agentservice.ToolDeleteFile {
		code = classifyDeleteError(result.Content)
	}
	if result.IsError && toolCall.Name == agentservice.ToolReplace && strings.Contains(strings.ToLower(result.Content), "denied by user") {
		code = cursordriver.DeleteCodeRejected
	}
	return cursordriver.HostResult{Content: result.Content, IsError: result.IsError, Code: code}, nil
}

func (h *cursorExecHost) start(ctx context.Context, call tool.Call) error {
	if h.timeline != nil {
		if err := h.timeline.start(ctx, message.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}); err != nil {
			return err
		}
	}
	if h.events != nil && !h.events.EmitEvent(ctx, Event{
		Kind: EventToolStarted, SessionID: h.sessionID, RunID: h.eventRunID, AgentID: h.agentID,
		ToolCallID: call.ID, State: "running", Data: map[string]string{"name": call.Name, "arguments": string(call.Arguments)},
	}) {
		return eventDeliveryError(ctx)
	}
	return nil
}

func (h *cursorExecHost) finish(ctx context.Context, call tool.Call, result tool.Result) error {
	callArguments := json.RawMessage(call.Arguments)
	resolvedName := result.Name
	if resolvedName == "" {
		resolvedName = call.Name
	}
	if h.timeline != nil {
		var err error
		callArguments, resolvedName, err = h.timeline.finish(ctx, message.ToolResult{
			ToolCallID: call.ID, Name: resolvedName, Content: result.Content,
			Structured: result.Structured, IsError: result.IsError,
		})
		if err != nil {
			return err
		}
	}
	if h.events == nil {
		return nil
	}
	content := boundedUTF8(result.Content, maxToolRecordPreviewBytes)
	structured := result.Structured
	if len(structured) > maxInlineToolRecordBytes {
		structured = nil
	}
	state := "completed"
	if result.IsError {
		state = "failed"
	}
	data := map[string]string{"name": resolvedName}
	if len(structured) > 0 {
		data["structured"] = string(structured)
	}
	if state == "completed" {
		if summary, ok := toolview.CompletedFileChanges(resolvedName, string(callArguments), string(structured), content); ok {
			data["fileChange"] = toolview.EncodeSummary(summary)
		}
	}
	if content != result.Content || len(structured) != len(result.Structured) {
		data["projection_truncated"] = "true"
	}
	if !h.events.EmitEvent(ctx, Event{
		Kind: EventToolFinished, SessionID: h.sessionID, RunID: h.eventRunID, AgentID: h.agentID,
		ToolCallID: call.ID, State: state, Text: content, Data: data,
	}) {
		return eventDeliveryError(ctx)
	}
	return nil
}

func (h *cursorExecHost) syncTodos(ctx context.Context, snapshot cursordriver.TodoSnapshot, callID, providerError string) cursordriver.HostResult {
	if strings.TrimSpace(providerError) != "" {
		return cursordriver.HostResult{Content: providerError, IsError: true}
	}
	if h == nil || h.events == nil || h.events.Sessions() == nil {
		return cursordriver.HostResult{Content: "Cursor Todo persistence is unavailable", IsError: true}
	}
	current, err := h.events.Sessions().LoadTodo(ctx, h.sessionID)
	if err != nil {
		return cursordriver.HostResult{Content: err.Error(), IsError: true}
	}
	items := make([]session.TodoItem, 0, len(snapshot.Items))
	for index, item := range snapshot.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("cursor-%d", index+1)
		}
		items = append(items, session.TodoItem{
			ID: id, Content: item.Content, Status: session.TodoStatus(item.Status),
		})
	}
	updated, err := h.events.Sessions().UpdateTodo(ctx, h.sessionID, current.Revision, func(todo *session.TodoList) error {
		if strings.TrimSpace(todo.Goal) == "" && len(items) > 0 {
			todo.Goal = "Cursor task plan"
		}
		if len(items) == 0 {
			todo.Phases = nil
		} else {
			todo.Phases = []session.TodoPhase{{ID: "cursor", Title: "Cursor", Items: items}}
		}
		return nil
	})
	if err != nil {
		return cursordriver.HostResult{Content: err.Error(), IsError: true}
	}
	copy := updated.Clone()
	h.events.EmitTodoUpdated(h.sessionID, copy)
	encoded, err := json.Marshal(updated)
	if err != nil {
		return cursordriver.HostResult{Content: err.Error(), IsError: true}
	}
	return cursordriver.HostResult{Content: string(encoded)}
}

func (h *cursorExecHost) rewriteExistingWrite(ctx context.Context, call tool.Call) (tool.Call, bool) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil || strings.TrimSpace(input.Path) == "" {
		return call, false
	}
	abs := filepath.Join(h.workspace, filepath.FromSlash(input.Path))
	if _, err := os.Stat(abs); err != nil {
		return call, false
	}
	readDriver, ok := h.bus.Driver(coding.ToolReadFile)
	if !ok {
		return call, false
	}
	readArgs, _ := json.Marshal(map[string]string{"path": input.Path})
	read, err := readDriver.Execute(ctx, tool.Call{ID: call.ID + "-read", Name: coding.ToolReadFile, Arguments: readArgs}, nil)
	if err != nil || read.IsError {
		return call, false
	}
	header, lines := hashlineHeaderAndCount(read.Content)
	if header == "" || lines == 0 {
		return call, false
	}
	args, _ := json.Marshal(map[string]string{"input": hashlineOverwritePatch(header, lines, input.Content)})
	return tool.Call{ID: call.ID, Name: coding.ToolEditHashline, Arguments: args}, true
}

func hashlineOverwritePatch(header string, lines int, content string) string {
	var body strings.Builder
	body.WriteString(header)
	body.WriteString("\nreplace 1..")
	body.WriteString(strconv.Itoa(lines))
	body.WriteString(":\n")
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		body.WriteString("+")
		body.WriteString(line)
		body.WriteString("\n")
	}
	return body.String()
}

func hashlineHeaderAndCount(content string) (string, int) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "¶") {
		return "", 0
	}
	count := 0
	for _, line := range lines[1:] {
		if line != "" {
			count++
		}
	}
	return lines[0], count
}

func withCursorExecHost(extraBody map[string]any, host cursordriver.ExecHost) map[string]any {
	bound := make(map[string]any, len(extraBody)+1)
	for key, value := range extraBody {
		bound[key] = value
	}
	if concrete, ok := host.(*cursorExecHost); ok {
		bound[cursordriver.TodoSyncExtraKey] = cursordriver.TodoSync(concrete.syncTodos)
	}
	if host != nil {
		bound[cursordriver.ExecHostExtraKey] = host
	}
	return bound
}

func classifyDeleteError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no such file"):
		return cursordriver.DeleteCodeNotFound
	case strings.Contains(lower, "not a file") || strings.Contains(lower, "directory"):
		return cursordriver.DeleteCodeNotFile
	case strings.Contains(lower, "denied by user") || strings.Contains(lower, "requires approval") || strings.Contains(lower, "not available") || strings.Contains(lower, "blocked by user"):
		return cursordriver.DeleteCodeRejected
	case strings.Contains(lower, "permission denied") || strings.Contains(lower, "read-only"):
		return cursordriver.DeleteCodeDenied
	case strings.Contains(lower, "busy"):
		return cursordriver.DeleteCodeBusy
	default:
		return ""
	}
}
