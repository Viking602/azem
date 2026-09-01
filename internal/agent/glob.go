package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/venat/tool"
)

const ToolGlob = "glob"

type globDriver struct {
	ws Workspace
}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func newGlobDriver(ws Workspace) tool.Driver {
	return globDriver{ws: ws}
}

func (d globDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolGlob,
		Description: "Find workspace-relative file paths matching a glob pattern. Use this instead of listing everything when you know a filename or extension pattern.",
		InputSchema: tool.Schema{
			Type: "object",
			Properties: map[string]tool.Schema{
				"pattern": {Type: "string"},
				"path":    {Type: "string"},
				"limit":   {Type: "integer"},
			},
			Required:             []string{"pattern"},
			AdditionalProperties: &additional,
		},
	}
}

func (d globDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var in globInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return globError(call, "invalid arguments"), nil
	}
	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		return globError(call, "pattern is required"), nil
	}
	glob := joinWorkspaceGlob(in.Path, pattern)
	res, err := d.ws.ListFiles(ctx, ListFilesRequest{Glob: glob, Limit: in.Limit})
	if err != nil {
		return globError(call, err.Error()), nil
	}
	content := strings.Join(res.Files, "\n")
	if res.Truncated {
		content += fmt.Sprintf("\n[truncated; %d files shown]", len(res.Files))
	}
	structured, _ := json.Marshal(res)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}, nil
}

func globError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "glob failed: " + message, IsError: true}
}

func joinWorkspaceGlob(base, pattern string) string {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	base = strings.TrimSpace(strings.ReplaceAll(base, "\\", "/"))
	if pattern == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "/") || (len(pattern) > 1 && pattern[1] == ':') {
		return pattern
	}
	if base == "" || base == "." {
		return pattern
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(pattern, "/")
}
