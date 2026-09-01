package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Viking602/venat/tool"
)

const ToolReplace = "replace"

type replaceDriver struct {
	read tool.Driver
	edit tool.Driver
}

type replaceEdit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type replaceInput struct {
	Path  string        `json:"path"`
	Edits []replaceEdit `json:"edits"`
}

func newReplaceDriver(read, edit tool.Driver) tool.Driver {
	return replaceDriver{read: read, edit: edit}
}

func (d replaceDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolReplace,
		Description: "Replace unique old_text with new_text in an existing file. Each old_text must occur exactly once. Prefer edit_hashline when you already have current [PATH#TAG] line anchors.",
		InputSchema: tool.Schema{
			Type: "object",
			Properties: map[string]tool.Schema{
				"path": {Type: "string"},
				"edits": {
					Type: "array",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]tool.Schema{
							"old_text": {Type: "string"},
							"new_text": {Type: "string"},
						},
						Required: []string{"old_text"},
					},
				},
			},
			Required:             []string{"path", "edits"},
			AdditionalProperties: &additional,
		},
	}
}

func (d replaceDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	if d.read == nil || d.edit == nil {
		return replaceError(call, "read or edit tool is unavailable"), nil
	}
	var in replaceInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil || strings.TrimSpace(in.Path) == "" || len(in.Edits) == 0 {
		return replaceError(call, "path and edits are required"), nil
	}
	readArgs, _ := json.Marshal(map[string]string{"path": in.Path})
	read, err := d.read.Execute(ctx, tool.Call{ID: call.ID + "-read", Name: ToolReadFile, Arguments: readArgs}, nil)
	if err != nil {
		return replaceError(call, err.Error()), nil
	}
	if read.IsError {
		return replaceError(call, read.Content), nil
	}
	var readResult ReadFileToolResult
	if len(read.Structured) == 0 || json.Unmarshal(read.Structured, &readResult) != nil {
		return replaceError(call, "read result metadata is unavailable"), nil
	}
	if readResult.Truncated {
		return replaceError(call, "file exceeds the safe read window; use search/read_file ranges with edit_hashline instead"), nil
	}
	header, count := hashlineHeaderAndCount(read.Content)
	if header == "" {
		return replaceError(call, "file not found: "+in.Path), nil
	}
	updated, applyErr := applyUniqueReplaces(hashlineSourceText(read.Content), in.Edits)
	if applyErr != nil {
		return replaceError(call, applyErr.Error()), nil
	}
	lines := count
	if lines == 0 {
		lines = 1
	}
	editArgs, _ := json.Marshal(map[string]string{"input": hashlineOverwritePatch(header, lines, updated)})
	result, err := d.edit.Execute(ctx, tool.Call{ID: call.ID, Name: ToolEditHashline, Arguments: editArgs}, sink)
	result.ToolCallID = call.ID
	result.Name = call.Name
	return result, err
}

func replaceError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "replace failed: " + message, IsError: true}
}

func hashlineHeaderAndCount(content string) (string, int) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "[") || !strings.HasSuffix(lines[0], "]") {
		return "", 0
	}
	count := 0
	for _, line := range lines[1:] {
		if separator := strings.IndexByte(line, ':'); separator > 0 {
			if _, err := strconv.Atoi(line[:separator]); err == nil {
				count++
			}
		}
	}
	return lines[0], count
}

func hashlineSourceText(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "[") || !strings.HasSuffix(lines[0], "]") {
		return ""
	}
	var body []string
	for _, line := range lines[1:] {
		if i := strings.IndexByte(line, ':'); i >= 0 {
			body = append(body, line[i+1:])
		}
	}
	return strings.Join(body, "\n")
}

func hashlineOverwritePatch(header string, lines int, content string) string {
	var body strings.Builder
	body.WriteString("*** Begin Patch\n")
	body.WriteString(header)
	body.WriteString("\nPUT 1.=")
	body.WriteString(strconv.Itoa(lines))
	body.WriteString(":\n")
	for _, line := range strings.Split(content, "\n") {
		body.WriteString("+")
		body.WriteString(line)
		body.WriteString("\n")
	}
	body.WriteString("*** End Patch\n")
	return body.String()
}

func applyUniqueReplaces(content string, edits []replaceEdit) (string, error) {
	updated := content
	for _, edit := range edits {
		if edit.OldText == "" {
			return "", fmt.Errorf("old_text is empty")
		}
		switch strings.Count(updated, edit.OldText) {
		case 0:
			return "", fmt.Errorf("old_text not found")
		case 1:
			updated = strings.Replace(updated, edit.OldText, edit.NewText, 1)
		default:
			return "", fmt.Errorf("old_text is not unique")
		}
	}
	return updated, nil
}
