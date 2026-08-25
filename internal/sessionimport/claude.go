package sessionimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

type claudeHistory struct {
	created      time.Time
	updated      time.Time
	workspace    string
	firstMessage string
	count        int
}

func discoverClaude(ctx context.Context, root string) ([]Info, error) {
	history := readClaudeHistory(ctx, filepath.Join(root, "history.jsonl"))
	results := make([]Info, 0)
	for _, containerName := range []string{"projects", ".projects"} {
		container := filepath.Join(root, containerName)
		projects, err := os.ReadDir(container)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, project := range projects {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !project.IsDir() || project.Type()&os.ModeSymlink != 0 {
				continue
			}
			directory := filepath.Join(container, project.Name())
			files, err := os.ReadDir(directory)
			if err != nil {
				continue
			}
			for _, file := range files {
				if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".jsonl") {
					continue
				}
				path := filepath.Join(directory, file.Name())
				stat, err := file.Info()
				if err != nil || stat.Size() > maxSessionFileBytes {
					continue
				}
				id := strings.TrimSuffix(file.Name(), ".jsonl")
				indexed := history[id]
				createdAt := stat.ModTime().UTC()
				updatedAt := createdAt
				workspace := decodeClaudeWorkspace(project.Name())
				if indexed != nil {
					if !indexed.created.IsZero() {
						createdAt = indexed.created
					}
					if !indexed.updated.IsZero() {
						updatedAt = indexed.updated
					}
					if indexed.workspace != "" {
						workspace = indexed.workspace
					}
				}
				result := Info{Source: SourceClaude, ID: id, Path: path, Workspace: workspace, CreatedAt: createdAt, UpdatedAt: updatedAt}
				if indexed != nil {
					result.FirstMessage = indexed.firstMessage
					result.MessageCount = indexed.count
				}
				results = append(results, result)
			}
		}
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].UpdatedAt.Equal(results[right].UpdatedAt) {
			return results[left].Path < results[right].Path
		}
		return results[left].UpdatedAt.After(results[right].UpdatedAt)
	})
	return results, nil
}

func readClaudeHistory(ctx context.Context, path string) map[string]*claudeHistory {
	records, _, err := readJSONL(ctx, path)
	if err != nil {
		return map[string]*claudeHistory{}
	}
	result := make(map[string]*claudeHistory)
	for _, record := range records {
		value := record.Value
		id := firstText(stringValue(value["sessionId"]), stringValue(value["session_id"]))
		if id == "" {
			continue
		}
		timestamp := timestampValue(firstValue(value["timestamp"], value["ts"]), time.Time{})
		if timestamp.IsZero() {
			continue
		}
		item := result[id]
		if item == nil {
			item = &claudeHistory{created: timestamp, updated: timestamp}
			result[id] = item
		}
		if timestamp.Before(item.created) {
			item.created = timestamp
		}
		if timestamp.After(item.updated) {
			item.updated = timestamp
		}
		if item.firstMessage == "" {
			item.firstMessage = cleanText(firstText(stringValue(value["display"]), stringValue(value["text"])))
		}
		if item.workspace == "" {
			item.workspace = stringValue(value["project"])
		}
		item.count++
	}
	return result
}

func decodeClaudeWorkspace(encoded string) string {
	if !strings.HasPrefix(encoded, "-") {
		return encoded
	}
	return filepath.Clean(strings.ReplaceAll(encoded, "-", string(filepath.Separator)))
}

func (importer *Importer) parseClaude(ctx context.Context, info Info, targetSessionID, fallbackWorkspace string) (session.SessionImport, error) {
	records, stat, err := readJSONL(ctx, info.Path)
	if err != nil {
		return session.SessionImport{}, fmt.Errorf("read Claude session %s: %w", info.ID, err)
	}
	if len(records) == 0 && stat.Size() > 0 {
		return session.SessionImport{}, fmt.Errorf("Claude session %s contains no readable records", info.ID)
	}
	parents := make(map[string]string, len(records))
	workspace := info.Workspace
	customTitle, automaticTitle := "", ""
	for _, record := range records {
		value := record.Value
		if id := stringValue(value["uuid"]); id != "" {
			parents[id] = stringValue(value["parentUuid"])
		}
		if workspace == "" {
			workspace = stringValue(value["cwd"])
		}
		switch stringValue(value["type"]) {
		case "custom-title":
			customTitle = firstText(stringValue(value["customTitle"]), customTitle)
		case "ai-title":
			automaticTitle = firstText(stringValue(value["aiTitle"]), automaticTitle)
		}
	}
	workspace = importedWorkspace(workspace, fallbackWorkspace)
	fallbackTime := stat.ModTime().UTC()
	if !info.CreatedAt.IsZero() {
		fallbackTime = info.CreatedAt.UTC()
	}
	sourceTails := make(map[string]string, len(records))
	toolNames := make(map[string]string)
	usedIDs := make(map[string]int)
	entries := make([]session.ImportEntry, 0, len(records))
	firstMessage := info.FirstMessage
	lastModel := ""
	resolveParent := func(sourceID string) string {
		seen := make(map[string]struct{})
		for sourceID != "" {
			if _, duplicate := seen[sourceID]; duplicate {
				return ""
			}
			seen[sourceID] = struct{}{}
			if tail := sourceTails[sourceID]; tail != "" {
				return tail
			}
			sourceID = parents[sourceID]
		}
		return ""
	}
	for _, record := range records {
		value := record.Value
		typeName := stringValue(value["type"])
		if (typeName != "user" && typeName != "assistant") || boolValue(value["isSidechain"]) || boolValue(value["isMeta"]) {
			continue
		}
		wireMessage := objectValue(value["message"])
		if wireMessage == nil {
			continue
		}
		sourceUUID := stringValue(value["uuid"])
		if sourceUUID == "" {
			sourceUUID = fmt.Sprintf("line-%d", record.Line)
		}
		parentID := resolveParent(stringValue(value["parentUuid"]))
		createdAt := timestampValue(value["timestamp"], fallbackTime.Add(time.Duration(record.Line)*time.Nanosecond))
		if typeName == "assistant" {
			if model := stringValue(wireMessage["model"]); model != "" && model != lastModel {
				modelEntry := session.ImportEntry{
					SourceID:       uniqueSourceID("claude:"+sourceUUID+":model", usedIDs),
					ParentSourceID: parentID,
					Block: session.Block{
						Kind: "model_change", RunID: "import:claude:" + info.ID,
						Title: "Imported model", Content: "anthropic/" + model,
						State: "completed", Data: map[string]string{"model": "anthropic/" + model},
					},
					CreatedAt: createdAt,
				}
				entries = append(entries, modelEntry)
				parentID = modelEntry.SourceID
				lastModel = model
			}
		}
		converted, err := importer.convertClaudeRecord(targetSessionID, info.ID, sourceUUID, typeName, wireMessage, createdAt, toolNames, usedIDs)
		if err != nil {
			return session.SessionImport{}, err
		}
		for index := range converted {
			converted[index].ParentSourceID = parentID
			parentID = converted[index].SourceID
			if firstMessage == "" && converted[index].Block.Kind == "user" {
				firstMessage = converted[index].Block.Content
			}
			entries = append(entries, converted[index])
		}
		if parentID != "" {
			sourceTails[sourceUUID] = parentID
		}
	}
	if len(entries) == 0 {
		return session.SessionImport{}, fmt.Errorf("Claude session %s contains no supported messages", info.ID)
	}
	title := sourceTitle(firstText(customTitle, automaticTitle, info.Title), firstMessage, SourceClaude)
	createdAt := info.CreatedAt
	if createdAt.IsZero() {
		createdAt = entries[0].CreatedAt
	}
	updatedAt := info.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = entries[len(entries)-1].CreatedAt
	}
	return session.SessionImport{
		Session:    session.Session{ID: targetSessionID, Title: title, CreatedAt: createdAt, UpdatedAt: updatedAt, AgentMode: "single"},
		SourceKind: session.ImportSourceClaude, SourceRef: info.Path, Workspace: workspace, Entries: entries,
		ActiveSourceID: entries[len(entries)-1].SourceID,
	}, nil
}

func (importer *Importer) convertClaudeRecord(targetSessionID, sourceSessionID, sourceUUID, typeName string, wireMessage map[string]any, createdAt time.Time, toolNames map[string]string, used map[string]int) ([]session.ImportEntry, error) {
	runID := "import:claude:" + sourceSessionID
	if typeName == "assistant" {
		parts := make([]message.ContentPart, 0)
		calls := make([]message.ToolCall, 0)
		for _, raw := range arrayValue(wireMessage["content"]) {
			item := objectValue(raw)
			switch stringValue(item["type"]) {
			case "text":
				if text := stringValue(item["text"]); text != "" {
					parts = append(parts, message.TextPart(text))
				}
			case "thinking":
				if thinking := stringValue(item["thinking"]); thinking != "" {
					parts = append(parts, message.ReasoningPart(thinking, stringValue(item["signature"])))
				}
			case "redacted_thinking":
				if data := stringValue(item["data"]); data != "" {
					parts = append(parts, message.ContentPart{Kind: message.ContentRedactedReasoning, Data: []byte(data)})
				}
			case "tool_use":
				callID, name := stringValue(item["id"]), stringValue(item["name"])
				if callID == "" || name == "" {
					continue
				}
				arguments, _ := json.Marshal(objectValue(item["input"]))
				calls = append(calls, message.ToolCall{ID: callID, Name: name, Arguments: arguments})
				toolNames[callID] = name
			}
		}
		if len(parts) == 0 && len(calls) == 0 {
			return nil, nil
		}
		modelMessage := message.Message{Role: message.RoleAssistant, Content: parts, ToolCalls: calls, Visibility: message.VisibilityShared, CreatedAt: createdAt}
		modelMessage.Response.Model = stringValue(wireMessage["model"])
		modelMessage.Response.ID = stringValue(wireMessage["id"])
		block, err := importedBlock(modelMessage, nil, runID)
		if err != nil {
			return nil, err
		}
		return []session.ImportEntry{{SourceID: uniqueSourceID("claude:"+sourceUUID+":message", used), Block: block, CreatedAt: createdAt}}, nil
	}
	rawContent := wireMessage["content"]
	toolResults := make([]session.ImportEntry, 0)
	for index, raw := range arrayValue(rawContent) {
		item := objectValue(raw)
		if stringValue(item["type"]) != "tool_result" {
			continue
		}
		callID := stringValue(item["tool_use_id"])
		if callID == "" {
			continue
		}
		parts, images := textAndImageParts(item["content"])
		imageParts, err := inlineImageParts(images)
		if err != nil {
			return nil, err
		}
		parts = append(parts, imageParts...)
		result := message.ToolResult{ToolCallID: callID, Name: firstText(toolNames[callID], "unknown"), Parts: parts, IsError: boolValue(item["is_error"])}
		modelMessage := message.NewToolResult(result)
		modelMessage.CreatedAt = createdAt
		block, err := importedBlock(modelMessage, nil, runID)
		if err != nil {
			return nil, err
		}
		toolResults = append(toolResults, session.ImportEntry{SourceID: uniqueSourceID(fmt.Sprintf("claude:%s:tool:%d", sourceUUID, index), used), Block: block, CreatedAt: createdAt})
	}
	if len(toolResults) > 0 {
		return toolResults, nil
	}
	parts, images := textAndImageParts(rawContent)
	attachments, inlineImages, err := importer.importImages(targetSessionID, "claude-"+sourceUUID, images)
	if err != nil {
		return nil, err
	}
	parts = append(parts, inlineImages...)
	if len(parts) == 0 && len(attachments) == 0 {
		return nil, nil
	}
	modelMessage := message.Message{Role: message.RoleUser, Content: parts, Visibility: message.VisibilityShared, CreatedAt: createdAt}
	block, err := importedBlock(modelMessage, attachments, runID)
	if err != nil {
		return nil, err
	}
	return []session.ImportEntry{{SourceID: uniqueSourceID("claude:"+sourceUUID+":message", used), Block: block, CreatedAt: createdAt}}, nil
}

func inlineImageParts(images []foreignImage) ([]message.ContentPart, error) {
	parts := make([]message.ContentPart, 0, len(images))
	for index, image := range images {
		if int64(base64.StdEncoding.DecodedLen(len(image.Encoded))) > maxSessionFileBytes {
			return nil, fmt.Errorf("foreign image %d exceeds import budget", index+1)
		}
		payload, err := base64.StdEncoding.DecodeString(image.Encoded)
		if err != nil {
			return nil, fmt.Errorf("decode foreign image %d: %w", index+1, err)
		}
		parts = append(parts, message.ContentPart{Kind: message.ContentImage, Data: payload, MediaType: image.MediaType})
	}
	return parts, nil
}

func uniqueSourceID(base string, used map[string]int) string {
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, used[base])
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
