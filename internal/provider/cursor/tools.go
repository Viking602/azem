package cursor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/venat/message"
)

const (
	azemRead    = "coding.read_file"
	azemWrite   = "coding.write_file"
	azemShell   = "coding.shell"
	azemSearch  = "coding.search"
	azemList    = "coding.list_files"
	azemGlob    = "coding.glob"
	azemDelete  = "coding.delete_file"
	ReplaceTool = "coding.replace"

	DeleteCodeNotFound = "not_found"
	DeleteCodeNotFile  = "not_file"
	DeleteCodeDenied   = "permission_denied"
	DeleteCodeBusy     = "busy"
	DeleteCodeRejected = "rejected"
)

func decodeStartedTool(payload []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(payload)
	if err != nil {
		return message.ToolCall{}, false
	}
	id := fieldString(fields, fieldToolCallID)
	body := fieldBytes(fields, fieldToolCallBody)
	if len(body) == 0 {
		return message.ToolCall{}, false
	}
	return decodeToolBody(id, body)
}

func decodeExecTool(payload []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(payload)
	if err != nil {
		return message.ToolCall{}, false
	}
	fallbackID := firstNonEmpty(fieldString(fields, fieldExecID), uintString(fieldUint32(fields, fieldExecMessageID)))
	switch {
	case len(fieldBytes(fields, fieldExecReadArgs)) > 0:
		return mapReadArgs(fallbackID, fieldBytes(fields, fieldExecReadArgs), true)
	case len(fieldBytes(fields, fieldExecWriteArgs)) > 0:
		return mapWriteArgs(fallbackID, fieldBytes(fields, fieldExecWriteArgs))
	case len(fieldBytes(fields, fieldExecShellArgs)) > 0:
		return mapShellArgs(fallbackID, fieldBytes(fields, fieldExecShellArgs))
	case len(fieldBytes(fields, fieldExecShellStream)) > 0:
		return mapShellArgs(fallbackID, fieldBytes(fields, fieldExecShellStream))
	case len(fieldBytes(fields, fieldExecGrepArgs)) > 0:
		return mapGrepArgs(fallbackID, fieldBytes(fields, fieldExecGrepArgs))
	case len(fieldBytes(fields, fieldExecLsArgs)) > 0:
		return mapLsArgs(fallbackID, fieldBytes(fields, fieldExecLsArgs))
	case len(fieldBytes(fields, fieldExecDeleteArgs)) > 0:
		return mapDeleteArgs(fallbackID, fieldBytes(fields, fieldExecDeleteArgs))
	case len(fieldBytes(fields, fieldExecPiEditArgs)) > 0:
		return mapPiEditArgs(fallbackID, fieldBytes(fields, fieldExecPiEditArgs))
	case len(fieldBytes(fields, fieldExecPiFindArgs)) > 0:
		return mapPiFindArgs(fallbackID, fieldBytes(fields, fieldExecPiFindArgs))
	case len(fieldBytes(fields, fieldExecMCPArgs)) > 0:
		return mapMCPArgs(fallbackID, fieldBytes(fields, fieldExecMCPArgs))
	default:
		return message.ToolCall{}, false
	}
}

func mapDeleteArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	path := strings.TrimSpace(fieldString(fields, fieldDeletePath))
	id := firstNonEmpty(fieldString(fields, fieldDeleteToolCallID), fallbackID)
	if path == "" || id == "" {
		return message.ToolCall{}, false
	}
	return toolCall(id, azemDelete, map[string]any{"path": path}), true
}

func decodeToolBody(id string, body []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(body)
	if err != nil {
		return message.ToolCall{}, false
	}
	switch {
	case len(fieldBytes(fields, fieldToolReadCall)) > 0:
		return mapReadArgs(id, nestedArgs(fieldBytes(fields, fieldToolReadCall)), false)
	case len(fieldBytes(fields, fieldToolShellCall)) > 0:
		return mapShellArgs(id, nestedArgs(fieldBytes(fields, fieldToolShellCall)))
	case len(fieldBytes(fields, fieldToolGrepCall)) > 0:
		return mapGrepArgs(id, nestedArgs(fieldBytes(fields, fieldToolGrepCall)))
	case len(fieldBytes(fields, fieldToolLsCall)) > 0:
		return mapLsArgs(id, nestedArgs(fieldBytes(fields, fieldToolLsCall)))
	case len(fieldBytes(fields, fieldToolGlobCall)) > 0:
		return mapLsArgs(id, nestedArgs(fieldBytes(fields, fieldToolGlobCall)))
	case len(fieldBytes(fields, fieldMCPToolCall)) > 0:
		return mapMCPArgs(id, fieldBytes(fields, fieldMCPToolCall))
	default:
		return message.ToolCall{}, false
	}
}

func decodeTodoCompletion(payload []byte) (string, TodoSnapshot, string, bool, error) {
	completed, err := decodeFields(payload)
	if err != nil {
		return "", TodoSnapshot{}, "", false, err
	}
	callID := fieldString(completed, fieldToolCallID)
	toolCall, err := decodeFields(fieldBytes(completed, fieldToolCallBody))
	if err != nil {
		return "", TodoSnapshot{}, "", false, err
	}
	rawTodo := fieldBytes(toolCall, fieldToolTodosCall)
	if len(rawTodo) == 0 {
		return "", TodoSnapshot{}, "", false, nil
	}
	todoCall, err := decodeFields(rawTodo)
	if err != nil {
		return "", TodoSnapshot{}, "", false, err
	}
	result, err := decodeFields(fieldBytes(todoCall, fieldTodoCallResult))
	if err != nil {
		return "", TodoSnapshot{}, "", false, err
	}
	if rawError := fieldBytes(result, fieldTodoResultError); len(rawError) > 0 {
		errorFields, err := decodeFields(rawError)
		if err != nil {
			return "", TodoSnapshot{}, "", false, err
		}
		return callID, TodoSnapshot{}, fieldString(errorFields, fieldTodoErrorText), true, nil
	}
	if !hasField(result, fieldTodoResultSuccess) {
		return callID, TodoSnapshot{}, "Cursor Todo completion omitted its result", true, nil
	}
	success, err := decodeFields(fieldBytes(result, fieldTodoResultSuccess))
	if err != nil {
		return "", TodoSnapshot{}, "", false, err
	}
	snapshot := TodoSnapshot{Merged: fieldUint32(success, fieldTodoSuccessMerged) != 0}
	for _, rawItem := range fieldAllBytes(success, fieldTodoSuccessItems) {
		item, err := decodeFields(rawItem)
		if err != nil {
			return "", TodoSnapshot{}, "", false, err
		}
		snapshot.Items = append(snapshot.Items, TodoSnapshotItem{
			ID: fieldString(item, fieldTodoItemID), Content: fieldString(item, fieldTodoItemContent),
			Status: cursorTodoStatus(fieldUint32(item, fieldTodoItemStatus)),
		})
	}
	return callID, snapshot, "", true, nil
}

func cursorTodoStatus(value uint32) string {
	switch value {
	case 2:
		return "in_progress"
	case 3:
		return "completed"
	case 4:
		return "cancelled"
	default:
		return "pending"
	}
}

func mapPiEditArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	path := strings.TrimSpace(fieldString(fields, fieldPiEditPath))
	if path == "" || fallbackID == "" {
		return message.ToolCall{}, false
	}
	var edits []map[string]string
	for _, item := range fieldAllBytes(fields, fieldPiEditEdits) {
		inner, innerErr := decodeFields(item)
		if innerErr != nil {
			continue
		}
		oldText := fieldString(inner, fieldPiEditOldText)
		if oldText == "" {
			continue
		}
		edits = append(edits, map[string]string{"old_text": oldText, "new_text": fieldString(inner, fieldPiEditNewText)})
	}
	if len(edits) == 0 {
		return message.ToolCall{}, false
	}
	return toolCall(fallbackID, ReplaceTool, map[string]any{"path": path, "edits": edits}), true
}

func mapPiFindArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	if fallbackID == "" {
		return message.ToolCall{}, false
	}
	pattern := strings.TrimSpace(fieldString(fields, fieldPiFindPattern))
	if pattern == "" {
		return message.ToolCall{}, false
	}
	args := map[string]any{"pattern": pattern}
	if path := strings.TrimSpace(fieldString(fields, fieldPiFindPath)); path != "" && path != "." {
		args["path"] = path
	}
	if hasField(fields, fieldPiFindLimit) {
		limit := fieldInt(fields, fieldPiFindLimit)
		if limit < 1 {
			limit = 1
		}
		args["limit"] = limit
	}
	return toolCall(fallbackID, azemGlob, args), true
}

func nestedArgs(payload []byte) []byte {
	fields, err := decodeFields(payload)
	if err != nil {
		return nil
	}
	if raw := fieldBytes(fields, fieldNestedArgs); len(raw) > 0 {
		return raw
	}
	return payload
}

func mapReadArgs(fallbackID string, raw []byte, exec bool) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	path := strings.TrimSpace(fieldString(fields, fieldReadPath))
	id := firstNonEmpty(fieldString(fields, fieldReadToolCallID), fallbackID)
	if path == "" || id == "" {
		return message.ToolCall{}, false
	}
	args := map[string]any{"path": path}
	offset := fieldInt(fields, fieldReadOffset)
	limit := fieldInt(fields, fieldReadLimit)
	if !exec && offset > 0 {
		args["startLine"] = offset
		if limit > 0 {
			args["endLine"] = offset + limit - 1
		}
	} else if !exec && limit > 0 {
		args["startLine"] = 1
		args["endLine"] = limit
	}
	return toolCall(id, azemRead, args), true
}

func mapWriteArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	path := strings.TrimSpace(fieldString(fields, fieldWritePath))
	id := firstNonEmpty(fieldString(fields, fieldWriteToolCallID), fallbackID)
	if path == "" || id == "" {
		return message.ToolCall{}, false
	}
	content := fieldString(fields, fieldWriteText)
	if data := fieldBytes(fields, fieldWriteBytes); len(data) > 0 {
		content = string(data)
	}
	return toolCall(id, azemWrite, map[string]any{"path": path, "content": content}), true
}

func mapShellArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	command := strings.TrimSpace(fieldString(fields, fieldShellCommand))
	id := firstNonEmpty(fieldString(fields, fieldShellToolCallID), fallbackID)
	if command == "" || id == "" {
		return message.ToolCall{}, false
	}
	args := map[string]any{"command": command}
	if timeout := fieldInt(fields, fieldShellTimeout); timeout > 0 {
		seconds := timeout
		if timeout >= 1000 {
			seconds = timeout / 1000
		}
		args["wall_clock_seconds"] = seconds
	}
	return toolCall(id, azemShell, args), true
}

func mapGrepArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	id := firstNonEmpty(fieldString(fields, fieldGrepToolCallID), fallbackID)
	pattern := fieldString(fields, fieldGrepPattern)
	glob := fieldString(fields, fieldGrepGlob)
	if id == "" {
		return message.ToolCall{}, false
	}
	if strings.TrimSpace(pattern) == "" && glob != "" {
		return toolCall(id, azemList, map[string]any{"glob": glob}), true
	}
	if strings.TrimSpace(pattern) == "" {
		return message.ToolCall{}, false
	}
	args := map[string]any{"query": pattern, "regexp": true}
	if glob != "" {
		args["glob"] = glob
	}
	if limit := fieldInt(fields, fieldGrepHeadLimit); limit > 0 {
		args["maxResults"] = limit
	}
	return toolCall(id, azemSearch, args), true
}

func mapLsArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	id := firstNonEmpty(fieldString(fields, fieldLsToolCallID), fallbackID)
	if id == "" {
		return message.ToolCall{}, false
	}
	path := strings.TrimSpace(fieldString(fields, fieldLsPath))
	args := map[string]any{}
	if path != "" && path != "." {
		args["glob"] = strings.TrimSuffix(path, "/") + "/**"
	}
	return toolCall(id, azemList, args), true
}

func mapMCPArgs(fallbackID string, raw []byte) (message.ToolCall, bool) {
	fields, err := decodeFields(raw)
	if err != nil {
		return message.ToolCall{}, false
	}
	if nested := fieldBytes(fields, fieldMCPArgs); len(nested) > 0 {
		if inner, innerErr := decodeFields(nested); innerErr == nil {
			fields = inner
		}
	}
	name := firstNonEmpty(fieldString(fields, fieldMCPArgName), fieldString(fields, fieldMCPArgToolName))
	id := firstNonEmpty(fieldString(fields, fieldMCPArgToolCallID), fallbackID)
	if name == "" || id == "" {
		return message.ToolCall{}, false
	}
	args, err := decodeMCPArgMap(fields)
	if err != nil {
		return message.ToolCall{}, false
	}
	return message.ToolCall{ID: id, Name: name, Arguments: args}, true
}

func decodeMCPArgMap(fields []protoField) (json.RawMessage, error) {
	entries := fieldRepeated(fields, fieldMCPArgMap)
	if len(entries) == 0 {
		return json.RawMessage(`{}`), nil
	}
	object := make(map[string]any, len(entries))
	for _, entry := range entries {
		item, err := decodeFields(entry)
		if err != nil {
			return nil, err
		}
		if !hasField(item, fieldMapKey) || !hasField(item, fieldMapValue) {
			return nil, fmt.Errorf("cursor MCP argument entry is incomplete")
		}
		value, err := decodeProtoValue(fieldBytes(item, fieldMapValue))
		if err != nil {
			return nil, err
		}
		object[fieldString(item, fieldMapKey)] = value
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func toolCall(id, name string, args map[string]any) message.ToolCall {
	raw, err := json.Marshal(args)
	if err != nil {
		raw = []byte(`{}`)
	}
	return message.ToolCall{ID: id, Name: name, Arguments: raw}
}

func fieldInt(fields []protoField, number int) int {
	return int(fieldUint32(fields, number))
}

func uintString(value uint32) string {
	if value == 0 {
		return ""
	}
	return strings.TrimLeft(jsonNumber(value), "")
}

func jsonNumber(value uint32) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
