package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Viking602/venat/tool"
)

const ToolDeleteFile = "coding.delete_file"

type deleteFileDriver struct {
	workspace string
}

func newDeleteFileDriver(workspace string) tool.Driver {
	return deleteFileDriver{workspace: workspace}
}

func (d deleteFileDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolDeleteFile,
		Description: "Delete one regular workspace file. Directories, symlinks, and paths outside the workspace are rejected.",
		InputSchema: tool.Schema{
			Type:                 "object",
			Properties:           map[string]tool.Schema{"path": {Type: "string"}},
			Required:             []string{"path"},
			AdditionalProperties: &additional,
		},
		EffectType:         tool.EffectWrite,
		RequiresActionTask: true,
		RiskLevel:          "medium",
		PolicyTags:         []string{"coding", "delete", "workspace-write"},
	}
}

func (d deleteFileDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil || strings.TrimSpace(input.Path) == "" {
		return deleteFileError(call, "path is required"), nil
	}
	size, err := deleteRegularFile(d.workspace, input.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return deleteFileError(call, "File not found: "+input.Path), nil
		}
		if os.IsPermission(err) {
			return deleteFileError(call, "permission denied: "+input.Path), nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "busy") {
			return deleteFileError(call, "File busy: "+input.Path), nil
		}
		return deleteFileError(call, err.Error()), nil
	}
	payload, _ := json.Marshal(map[string]any{"path": input.Path, "size": size})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(payload)}, nil
}

func deleteFileError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.delete_file failed: " + message, IsError: true}
}

func cleanDeletePath(root, rel string) (string, []string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil, fmt.Errorf("workspace root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) {
		return "", nil, fmt.Errorf("path must be workspace-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", nil, fmt.Errorf("path escapes workspace")
	}
	parts := strings.Split(clean, string(os.PathSeparator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", nil, fmt.Errorf("invalid workspace path")
		}
	}
	if strings.EqualFold(parts[0], ".git") {
		return "", nil, fmt.Errorf("path targets denied .git tree")
	}
	return absRoot, parts, nil
}
