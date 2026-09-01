package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
	_ "modernc.org/sqlite"
)

type codexThread struct {
	ID, Path, Workspace, Title, FirstMessage string
	CreatedAt, UpdatedAt                     time.Time
}

func discoverCodex(ctx context.Context, root string) ([]Info, error) {
	if indexed := discoverCodexDatabase(ctx, root); len(indexed) > 0 {
		return indexed, nil
	}
	index := readCodexIndex(ctx, filepath.Join(root, "session_index.jsonl"))
	files := make([]string, 0)
	for _, directory := range []string{"sessions", ".sessions", "archived_sessions"} {
		base := filepath.Join(root, directory)
		_ = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return filepath.SkipDir
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				files = append(files, path)
			}
			return nil
		})
	}
	results := make([]Info, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		firstRecord, stat, err := readFirstJSONL(ctx, path)
		if err != nil {
			continue
		}
		first := firstRecord.Value
		if stringValue(first["type"]) != "session_meta" {
			continue
		}
		payload := objectValue(first["payload"])
		workspace := stringValue(payload["cwd"])
		if workspace == "" {
			continue
		}
		id := firstText(stringValue(payload["id"]), strings.TrimSuffix(filepath.Base(path), ".jsonl"))
		createdAt := timestampValue(firstValue(payload["timestamp"], first["timestamp"]), stat.ModTime())
		updatedAt := stat.ModTime().UTC()
		result := Info{Source: SourceCodex, ID: id, Path: path, Workspace: workspace, CreatedAt: createdAt, UpdatedAt: updatedAt}
		if metadata := index[id]; metadata != nil {
			result.Title = metadata.Title
			if !metadata.UpdatedAt.IsZero() {
				result.UpdatedAt = metadata.UpdatedAt
			}
		}
		results = append(results, result)
	}
	sortInfos(results)
	return results, nil
}

func discoverCodexDatabase(ctx context.Context, root string) []Info {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type candidate struct {
		version int
		path    string
	}
	candidates := make([]candidate, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, "state_") || !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		version, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "state_"), ".sqlite"))
		if err == nil {
			candidates = append(candidates, candidate{version: version, path: filepath.Join(root, name)})
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].version > candidates[right].version })
	for _, candidate := range candidates {
		dsn := (&url.URL{Scheme: "file", Path: candidate.path, RawQuery: "mode=ro&immutable=1"}).String()
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT id,rollout_path,created_at,updated_at,cwd,title,first_user_message FROM threads`)
		if err != nil {
			db.Close()
			continue
		}
		results := make([]Info, 0)
		for rows.Next() {
			var id, rolloutPath, workspace string
			var created, updated sql.NullFloat64
			var title, first sql.NullString
			if err := rows.Scan(&id, &rolloutPath, &created, &updated, &workspace, &title, &first); err != nil || id == "" || rolloutPath == "" || workspace == "" {
				continue
			}
			if !filepath.IsAbs(rolloutPath) {
				rolloutPath = filepath.Join(root, rolloutPath)
			}
			createdAt := epochTime(created.Float64)
			updatedAt := epochTime(updated.Float64)
			results = append(results, Info{Source: SourceCodex, ID: id, Path: rolloutPath, Workspace: workspace, Title: title.String, FirstMessage: first.String, CreatedAt: createdAt, UpdatedAt: updatedAt})
		}
		rows.Close()
		db.Close()
		if len(results) > 0 {
			sortInfos(results)
			return results
		}
	}
	return nil
}

func epochTime(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value < 10_000_000_000 {
		value *= 1000
	}
	return time.UnixMilli(int64(value)).UTC()
}

func readCodexIndex(ctx context.Context, path string) map[string]*codexThread {
	records, _, err := readJSONL(ctx, path)
	if err != nil {
		return map[string]*codexThread{}
	}
	result := make(map[string]*codexThread)
	for _, record := range records {
		id := stringValue(record.Value["id"])
		if id == "" {
			continue
		}
		result[id] = &codexThread{ID: id, Title: stringValue(record.Value["thread_name"]), UpdatedAt: timestampValue(record.Value["updated_at"], time.Time{})}
	}
	return result
}

func sortInfos(values []Info) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].UpdatedAt.Equal(values[right].UpdatedAt) {
			return values[left].ID < values[right].ID
		}
		return values[left].UpdatedAt.After(values[right].UpdatedAt)
	})
}

type codexConverted struct {
	entry    *session.ImportEntry
	title    string
	rollback int
}

func (importer *Importer) parseCodex(ctx context.Context, info Info, targetSessionID, fallbackWorkspace string) (session.SessionImport, error) {
	records, stat, err := readJSONL(ctx, info.Path)
	if err != nil {
		return session.SessionImport{}, fmt.Errorf("read Codex session %s: %w", info.ID, err)
	}
	if len(records) == 0 {
		return session.SessionImport{}, fmt.Errorf("Codex session %s is empty", info.ID)
	}
	workspace := info.Workspace
	for _, record := range records {
		if stringValue(record.Value["type"]) == "session_meta" {
			if candidate := stringValue(objectValue(record.Value["payload"])["cwd"]); candidate != "" {
				workspace = candidate
			}
			break
		}
	}
	workspace = importedWorkspace(workspace, fallbackWorkspace)
	canonicalUsers, canonicalAssistants, canonicalCalls := codexCanonicalSets(records)
	entries := make([]session.ImportEntry, 0, len(records))
	toolNames := make(map[string]string)
	used := make(map[string]int)
	model := "codex"
	title := info.Title
	firstMessage := info.FirstMessage
	fallbackTime := info.CreatedAt
	if fallbackTime.IsZero() {
		fallbackTime = stat.ModTime().UTC()
	}
	parentID := ""
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return session.SessionImport{}, err
		}
		value := record.Value
		payload := objectValue(value["payload"])
		if payload == nil {
			continue
		}
		createdAt := timestampValue(value["timestamp"], fallbackTime.Add(time.Duration(record.Line)*time.Nanosecond))
		var converted []codexConverted
		switch stringValue(value["type"]) {
		case "turn_context":
			if nextModel := stringValue(payload["model"]); nextModel != "" && nextModel != model {
				model = nextModel
				entry := session.ImportEntry{
					SourceID: uniqueSourceID(fmt.Sprintf("codex:%d:model", record.Line), used),
					Block: session.Block{
						Kind: "model_change", RunID: "import:codex:" + info.ID,
						Title: "Imported model", Content: "openai-codex/" + model,
						State: "completed", Data: map[string]string{"model": "openai-codex/" + model},
					},
					CreatedAt: createdAt,
				}
				converted = []codexConverted{{entry: &entry}}
			}
		case "response_item":
			converted, err = importer.convertCodexResponse(targetSessionID, info.ID, record.Line, payload, createdAt, model, toolNames, used)
		case "event_msg":
			converted, err = importer.convertCodexEvent(targetSessionID, info.ID, record.Line, payload, createdAt, model, toolNames, canonicalUsers, canonicalAssistants, canonicalCalls, used)
		case "compacted":
			summary := cleanText(stringValue(payload["message"]))
			if summary != "" {
				modelMessage := message.NewText(message.RoleUser, "[Imported Codex compaction]\n"+summary)
				modelMessage.Kind = message.KindCompactionSummary
				agentruntime.SetMessageCreatedAt(&modelMessage, createdAt)
				block, blockErr := importedBlock(modelMessage, nil, "import:codex:"+info.ID)
				if blockErr != nil {
					err = blockErr
				} else {
					entry := session.ImportEntry{SourceID: uniqueSourceID(fmt.Sprintf("codex:%d:compaction", record.Line), used), Block: block, CreatedAt: createdAt}
					converted = []codexConverted{{entry: &entry}}
				}
			}
		}
		if err != nil {
			return session.SessionImport{}, err
		}
		for _, item := range converted {
			if item.title != "" {
				title = item.title
			}
			if item.rollback > 0 {
				for turns := item.rollback; turns > 0; turns-- {
					index := lastUserEntry(entries)
					if index < 0 {
						break
					}
					entries = entries[:index]
				}
				if len(entries) == 0 {
					parentID = ""
				} else {
					parentID = entries[len(entries)-1].SourceID
				}
				continue
			}
			if item.entry == nil {
				continue
			}
			item.entry.ParentSourceID = parentID
			parentID = item.entry.SourceID
			if firstMessage == "" && item.entry.Block.Kind == "user" {
				firstMessage = item.entry.Block.Content
			}
			entries = append(entries, *item.entry)
		}
	}
	if len(entries) == 0 {
		return session.SessionImport{}, fmt.Errorf("Codex session %s contains no supported messages", info.ID)
	}
	createdAt := info.CreatedAt
	if createdAt.IsZero() {
		createdAt = entries[0].CreatedAt
	}
	updatedAt := info.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = entries[len(entries)-1].CreatedAt
	}
	return session.SessionImport{
		Session:    session.Session{ID: targetSessionID, Title: sourceTitle(title, firstMessage, SourceCodex), CreatedAt: createdAt, UpdatedAt: updatedAt, AgentMode: "single"},
		SourceKind: session.ImportSourceCodex, SourceRef: info.Path, Workspace: workspace, Entries: entries,
		ActiveSourceID: entries[len(entries)-1].SourceID,
	}, nil
}

func (importer *Importer) convertCodexResponse(targetID, sourceID string, line int, payload map[string]any, createdAt time.Time, model string, toolNames map[string]string, used map[string]int) ([]codexConverted, error) {
	typeName := stringValue(payload["type"])
	runID := "import:codex:" + sourceID
	makeEntry := func(modelMessage message.Message, attachments []session.Attachment, suffix string) ([]codexConverted, error) {
		agentruntime.SetMessageCreatedAt(&modelMessage, createdAt)
		block, err := importedBlock(modelMessage, attachments, runID)
		if err != nil {
			return nil, err
		}
		entry := session.ImportEntry{SourceID: uniqueSourceID(fmt.Sprintf("codex:%d:%s", line, suffix), used), Block: block, CreatedAt: createdAt}
		return []codexConverted{{entry: &entry}}, nil
	}
	switch typeName {
	case "message":
		role := stringValue(payload["role"])
		parts, images := textAndImageParts(payload["content"])
		if len(parts) == 0 && len(images) == 0 {
			return nil, nil
		}
		if role == "user" {
			attachments, inline, err := importer.importImages(targetID, fmt.Sprintf("codex-%d", line), images)
			if err != nil {
				return nil, err
			}
			parts = append(parts, inline...)
			return makeEntry(message.Message{Role: message.RoleUser, Content: parts}, attachments, "user")
		}
		if role == "assistant" {
			inline, err := inlineImageParts(images)
			if err != nil {
				return nil, err
			}
			parts = append(parts, inline...)
			value := message.Message{Role: message.RoleAssistant, Content: parts}
			value.Response.Model = model
			return makeEntry(value, nil, "assistant")
		}
	case "reasoning":
		parts := make([]message.ContentPart, 0)
		for _, key := range []string{"summary", "content"} {
			for _, raw := range arrayValue(payload[key]) {
				if text := stringValue(objectValue(raw)["text"]); text != "" {
					parts = append(parts, message.ReasoningPart(text, ""))
				}
			}
		}
		if len(parts) > 0 {
			return makeEntry(message.Message{Role: message.RoleAssistant, Content: parts}, nil, "reasoning")
		}
	case "function_call", "custom_tool_call":
		callID := firstText(stringValue(payload["call_id"]), stringValue(payload["id"]))
		name := stringValue(payload["name"])
		if callID == "" || name == "" {
			return nil, nil
		}
		toolNames[callID] = name
		arguments := codexArguments(firstValue(payload["arguments"], payload["input"]))
		return makeEntry(message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: callID, Name: name, Arguments: arguments}}}, nil, "call")
	case "function_call_output", "custom_tool_call_output":
		return makeEntry(codexToolResult(payload, toolNames, false, createdAt), nil, "result")
	case "web_search_call", "tool_search_call":
		callID := firstText(stringValue(payload["call_id"]), stringValue(payload["id"]))
		if callID == "" {
			return nil, nil
		}
		name := "web_search"
		if typeName == "tool_search_call" {
			name = "tool_search"
		}
		toolNames[callID] = name
		return makeEntry(message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: callID, Name: name, Arguments: codexArguments(firstValue(payload["action"], payload["arguments"]))}}}, nil, "call")
	case "tool_search_output":
		return makeEntry(codexToolResult(payload, toolNames, stringValue(payload["status"]) == "failed", createdAt), nil, "result")
	}
	return nil, nil
}

func (importer *Importer) convertCodexEvent(targetID, sourceID string, line int, payload map[string]any, createdAt time.Time, model string, toolNames map[string]string, canonicalUsers, canonicalAssistants, canonicalCalls map[string]struct{}, used map[string]int) ([]codexConverted, error) {
	typeName := stringValue(payload["type"])
	if typeName == "thread_name_updated" {
		return []codexConverted{{title: stringValue(payload["thread_name"])}}, nil
	}
	if typeName == "thread_rolled_back" {
		return []codexConverted{{rollback: int(numberValue(payload["num_turns"]))}}, nil
	}
	if typeName == "user_message" {
		text := stringValue(payload["message"])
		if text == "" {
			return nil, nil
		}
		if _, duplicate := canonicalUsers[text]; duplicate {
			return nil, nil
		}
		return importer.codexEventMessage(targetID, sourceID, line, message.NewText(message.RoleUser, text), createdAt, used)
	}
	if typeName == "agent_message" || typeName == "agent_reasoning" {
		text := firstText(stringValue(payload["message"]), stringValue(payload["text"]))
		if text == "" {
			return nil, nil
		}
		if _, duplicate := canonicalAssistants[text]; duplicate {
			return nil, nil
		}
		value := message.NewText(message.RoleAssistant, text)
		if typeName == "agent_reasoning" {
			value = message.Message{Role: message.RoleAssistant, Content: []message.ContentPart{message.ReasoningPart(text, "")}}
		}
		value.Response.Model = model
		return importer.codexEventMessage(targetID, sourceID, line, value, createdAt, used)
	}
	if typeName == "dynamic_tool_call_request" {
		callID := firstText(stringValue(payload["callId"]), stringValue(payload["call_id"]))
		name := stringValue(payload["tool"])
		if callID == "" || name == "" {
			return nil, nil
		}
		if _, duplicate := canonicalCalls[callID]; duplicate {
			return nil, nil
		}
		toolNames[callID] = name
		value := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: callID, Name: name, Arguments: codexArguments(payload["arguments"])}}}
		return importer.codexEventMessage(targetID, sourceID, line, value, createdAt, used)
	}
	if typeName == "dynamic_tool_call_response" || typeName == "web_search_end" {
		callID := firstText(stringValue(payload["call_id"]), stringValue(payload["callId"]))
		if callID == "" {
			return nil, nil
		}
		if _, duplicate := canonicalCalls[callID]; duplicate && typeName == "dynamic_tool_call_response" {
			return nil, nil
		}
		name := firstText(toolNames[callID], stringValue(payload["tool"]), "web_search")
		converted := make([]codexConverted, 0, 2)
		if typeName == "web_search_end" && toolNames[callID] == "" {
			toolNames[callID] = name
			call := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: callID, Name: name, Arguments: codexArguments(firstValue(payload["action"], payload["query"]))}}}
			callEntry, err := importer.codexEventMessage(targetID, sourceID, line, call, createdAt, used)
			if err != nil {
				return nil, err
			}
			converted = append(converted, callEntry...)
		}
		result := message.ToolResult{ToolCallID: callID, Name: name, Content: cleanText(firstText(stringValue(payload["error"]), stringifyValue(firstValue(payload["content_items"], payload["results"], payload["query"])))), IsError: stringValue(payload["error"]) != "" || payload["success"] == false}
		resultEntry, err := importer.codexEventMessage(targetID, sourceID, line, message.NewToolResult(result), createdAt, used)
		if err != nil {
			return nil, err
		}
		return append(converted, resultEntry...), nil
	}
	if typeName == "mcp_tool_call_end" {
		callID := stringValue(payload["call_id"])
		invocation := objectValue(payload["invocation"])
		server, toolName := stringValue(invocation["server"]), stringValue(invocation["tool"])
		if callID == "" || server == "" || toolName == "" {
			return nil, nil
		}
		if _, duplicate := canonicalCalls[callID]; duplicate {
			return nil, nil
		}
		name := server + "/" + toolName
		toolNames[callID] = name
		call := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: callID, Name: name, Arguments: codexArguments(invocation["arguments"])}}}
		callEntry, err := importer.codexEventMessage(targetID, sourceID, line, call, createdAt, used)
		if err != nil {
			return nil, err
		}
		resultPayload := objectValue(payload["result"])
		errorText := stringValue(resultPayload["Err"])
		content := firstValue(resultPayload["Ok"], payload["result"])
		result := message.ToolResult{ToolCallID: callID, Name: name, Content: stringifyValue(content), IsError: errorText != ""}
		if errorText != "" {
			result.Content = errorText
		}
		resultEntry, err := importer.codexEventMessage(targetID, sourceID, line, message.NewToolResult(result), createdAt, used)
		if err != nil {
			return nil, err
		}
		return append(callEntry, resultEntry...), nil
	}
	return nil, nil
}

func (importer *Importer) codexEventMessage(_ string, sourceID string, line int, value message.Message, createdAt time.Time, used map[string]int) ([]codexConverted, error) {
	agentruntime.SetMessageCreatedAt(&value, createdAt)
	block, err := importedBlock(value, nil, "import:codex:"+sourceID)
	if err != nil {
		return nil, err
	}
	entry := session.ImportEntry{SourceID: uniqueSourceID(fmt.Sprintf("codex:%d:event", line), used), Block: block, CreatedAt: createdAt}
	return []codexConverted{{entry: &entry}}, nil
}

func codexCanonicalSets(records []jsonRecord) (map[string]struct{}, map[string]struct{}, map[string]struct{}) {
	users, assistants, calls := make(map[string]struct{}), make(map[string]struct{}), make(map[string]struct{})
	for _, record := range records {
		if stringValue(record.Value["type"]) != "response_item" {
			continue
		}
		payload := objectValue(record.Value["payload"])
		typeName := stringValue(payload["type"])
		if strings.Contains(typeName, "call") {
			if id := firstText(stringValue(payload["call_id"]), stringValue(payload["id"])); id != "" {
				calls[id] = struct{}{}
			}
		}
		if typeName == "message" {
			parts, _ := textAndImageParts(payload["content"])
			text := contentText(parts)
			if stringValue(payload["role"]) == "user" {
				users[text] = struct{}{}
			} else {
				assistants[text] = struct{}{}
			}
		}
	}
	return users, assistants, calls
}

func codexArguments(value any) json.RawMessage {
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return json.RawMessage(text)
		}
		encoded, _ := json.Marshal(map[string]string{"input": text})
		return encoded
	}
	encoded, _ := json.Marshal(value)
	if string(encoded) == "null" {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func codexToolResult(payload map[string]any, toolNames map[string]string, failed bool, createdAt time.Time) message.Message {
	callID := firstText(stringValue(payload["call_id"]), stringValue(payload["id"]))
	result := message.ToolResult{ToolCallID: callID, Name: firstText(toolNames[callID], "unknown"), Content: stringifyValue(firstValue(payload["output"], payload["tools"])), IsError: failed}
	value := message.NewToolResult(result)
	agentruntime.SetMessageCreatedAt(&value, createdAt)
	return value
}

func stringifyValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func contentText(parts []message.ContentPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Kind == message.ContentText {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "")
}

func lastUserEntry(entries []session.ImportEntry) int {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Block.Kind == "user" {
			return index
		}
	}
	return -1
}
