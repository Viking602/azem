package devin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Viking602/azem/internal/provider/toolnames"
	"github.com/Viking602/venat/message"
)

// These native CLI names are mapped onto the existing governed Azem tools.
// Unsupported tools keep their original schema and a provider-safe name.
var nativeToolNames = map[string]string{
	"coding.read_file":  "read",
	"coding.write_file": "write",
	"coding.shell":      "exec",
	"replace":           "edit",
}

func nativeTools(definitions []message.ToolDefinition, history []message.Message) ([]message.ToolDefinition, *toolnames.Names) {
	names := toolnames.New(definitions)
	result := append([]message.ToolDefinition(nil), definitions...)
	for i, definition := range result {
		native := nativeToolNames[definition.Name]
		if native == "" || !nativeHistoryCompatible(definition.Name, history) || !names.Alias(definition.Name, native) {
			continue
		}
		if native == "edit" {
			additional := false
			result[i].InputSchema = message.JSONSchema{Type: "object", Required: []string{"path", "old_string", "new_string"}, AdditionalProperties: &additional, Properties: map[string]message.JSONSchema{
				"path": {Type: "string"}, "old_string": {Type: "string"}, "new_string": {Type: "string"},
			}}
			result[i].Description = "Replace one unique old_string with new_string in path. Read the file first. Ambiguous matches fail; use edit_hashline for batch edits."
		}
		if native == "exec" {
			additional := false
			result[i].InputSchema = message.JSONSchema{Type: "object", Required: []string{"command"}, AdditionalProperties: &additional, Properties: map[string]message.JSONSchema{
				"command": {Type: "string"}, "workdir": {Type: "string", Description: "Optional absolute working directory."},
			}}
			result[i].Description = "Run a supervised one-shot shell command under the current workspace permissions. Optional workdir is absolute. This adapter does not provide persistent shell IDs, interactive input, or background shells; use hub jobs for background work."
		}
	}
	return result, names
}

func nativeHistoryCompatible(local string, history []message.Message) bool {
	for _, item := range history {
		for _, call := range item.ToolCalls {
			if call.Name != local {
				continue
			}
			if local == "replace" {
				var input struct {
					Edits []json.RawMessage `json:"edits"`
				}
				if json.Unmarshal(call.Arguments, &input) != nil || len(input.Edits) != 1 {
					return false
				}
			}
			if local == "coding.shell" {
				var input map[string]json.RawMessage
				if json.Unmarshal(call.Arguments, &input) != nil || len(input) != 1 || input["command"] == nil {
					return false
				}
			}
		}
	}
	return true
}

// Only completed native calls are translated. Streaming argument fragments
// remain provider-native and never cause side effects.
func localToolArguments(local string, raw json.RawMessage, names *toolnames.Names) (json.RawMessage, error) {
	native := names.Wire(local)
	if nativeToolNames[local] != native || native != "edit" && native != "exec" {
		return raw, nil
	}
	decode := json.NewDecoder(bytes.NewReader(raw))
	decode.DisallowUnknownFields()
	if native == "edit" {
		return localEditArguments(decode)
	}
	return localExecArguments(decode)
}

func localEditArguments(decode *json.Decoder) (json.RawMessage, error) {
	var input struct {
		Path string  `json:"path"`
		Old  *string `json:"old_string"`
		New  *string `json:"new_string"`
	}
	if err := decode.Decode(&input); err != nil {
		return nil, fmt.Errorf("invalid Devin edit arguments: %w", err)
	}
	if strings.TrimSpace(input.Path) == "" || input.Old == nil || *input.Old == "" || input.New == nil {
		return nil, fmt.Errorf("Devin edit requires path, nonempty old_string and new_string")
	}
	return json.Marshal(map[string]any{"path": input.Path, "edits": []map[string]string{{"old_text": *input.Old, "new_text": *input.New}}})
}

func localExecArguments(decode *json.Decoder) (json.RawMessage, error) {
	var input struct {
		Command string `json:"command"`
		Workdir string `json:"workdir,omitempty"`
	}
	if err := decode.Decode(&input); err != nil {
		return nil, fmt.Errorf("invalid Devin exec arguments: %w", err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return nil, fmt.Errorf("Devin exec requires command")
	}
	if input.Workdir != "" {
		if !filepath.IsAbs(input.Workdir) || strings.ContainsRune(input.Workdir, 0) {
			return nil, fmt.Errorf("Devin exec workdir must be an absolute path")
		}
		if runtime.GOOS == "windows" {
			input.Command = "Set-Location -LiteralPath '" + strings.ReplaceAll(input.Workdir, "'", "''") + "' -ErrorAction Stop\n" + input.Command
		} else {
			input.Command = "cd '" + strings.ReplaceAll(input.Workdir, "'", "'\"'\"'") + "' && " + input.Command
		}
	}
	return json.Marshal(map[string]string{"command": input.Command})
}

func wireToolArguments(call message.ToolCall, names *toolnames.Names) json.RawMessage {
	// New Devin turns replay their exact native calls from provider state. This
	// fallback also supports canonical history produced by another provider.
	if call.Name == "replace" && names.Wire(call.Name) == "edit" {
		var input struct {
			Path  string `json:"path"`
			Edits []struct {
				Old string `json:"old_text"`
				New string `json:"new_text"`
			} `json:"edits"`
		}
		if json.Unmarshal(call.Arguments, &input) == nil && len(input.Edits) == 1 {
			raw, _ := json.Marshal(map[string]string{"path": input.Path, "old_string": input.Edits[0].Old, "new_string": input.Edits[0].New})
			return raw
		}
	}
	return call.Arguments
}
