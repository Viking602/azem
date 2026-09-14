package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolASTGrep = "ast_grep"
const ToolASTEdit = "ast_edit"

type astDriver struct {
	root, operation string
	edit            tool.Driver
}

func (d *astDriver) Definition() tool.Definition {
	additional := false
	required := []string{"pat", "lang"}
	if d.operation == ToolASTEdit {
		required = append(required, "rewrite")
	}
	return tool.Definition{Name: d.operation, Description: "Structural AST search/rewrite with the native ast-grep executable (no Node/TS). pat uses $NAME / $$$NAMES captures. lang is the ast-grep language name. A path can be a file, directory or workspace glob. ast_edit previews by default; apply:true commits all bounded matches through Hashline with stale-source and rollback guards.", InputSchema: tool.Schema{Type: "object", Required: required, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"pat": {Type: "string"}, "lang": {Type: "string"}, "path": {Type: "string"}, "rewrite": {Type: "string"}, "apply": {Type: "boolean"}, "limit": {Type: "integer", Description: "Maximum matches, 1-500; default 50. Edits reject incomplete results."},
	}}, Concurrency: tool.ConcurrencyParallel}
}

func (d *astDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	var input struct {
		Apply bool `json:"apply"`
	}
	if d.operation == ToolASTEdit && (json.Unmarshal(call.Arguments, &input) != nil || input.Apply) {
		policy := workspaceWritePolicy("coding", "ast", "edit")
		policy.Concurrency, policy.ConcurrencyGroup = tool.ConcurrencyExclusive, "workspace-files"
		return policy
	}
	return readOnlyPolicy("coding", "ast", "read")
}

func (d *astDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Pattern  string  `json:"pat"`
		Language string  `json:"lang"`
		Path     string  `json:"path"`
		Rewrite  *string `json:"rewrite"`
		Apply    bool    `json:"apply"`
		Limit    int     `json:"limit"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if strings.TrimSpace(input.Pattern) == "" || len(input.Pattern) > 64<<10 || input.Language == "" || len(input.Language) > 48 || input.Limit < 0 || input.Limit > 500 {
		return toolError(call, "pat/lang are required; pattern limit 64 KiB, match limit 1-500"), nil
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	if d.operation == ToolASTEdit && (input.Rewrite == nil || len(*input.Rewrite) > 64<<10) {
		return toolError(call, "rewrite is required and must be at most 64 KiB"), nil
	}
	if d.operation == ToolASTGrep && (input.Apply || input.Rewrite != nil) {
		return toolError(call, "use ast_edit for rewriting"), nil
	}
	if input.Apply && d.edit == nil {
		return toolError(call, "workspace edits are disabled"), nil
	}
	program, err := nativeExecutable("ast-grep")
	if err != nil {
		return toolError(call, "native ast-grep executable is required (brew install ast-grep or cargo install ast-grep --locked)"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Do not load executable custom parsers from a workspace sgconfig.yml.
	parserRoot, err := os.MkdirTemp("", "azem-ast-")
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer os.RemoveAll(parserRoot)
	parserConfig := filepath.Join(parserRoot, "sgconfig.yml")
	if err := os.WriteFile(parserConfig, []byte("ruleDirs: []\n"), 0600); err != nil {
		return toolError(call, err.Error()), nil
	}
	pattern := input.Path
	if pattern == "" {
		pattern = "**/*"
	}
	if !strings.ContainsAny(pattern, "*?[") {
		_, relative, info, err := secureReadPath(d.root, pattern)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		pattern = relative
		if info.IsDir() {
			if relative == "." {
				pattern = "**/*"
			} else {
				pattern += "/**/*"
			}
		}
	} else if filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
		return toolError(call, "glob must remain inside workspace"), nil
	}
	listing, err := NewLocalWorkspace(d.root).ListFiles(ctx, ListFilesRequest{Glob: pattern, Limit: 1000, MaxFileBytes: 1 << 20})
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if listing.Truncated {
		return toolError(call, "AST scope exceeds 1000 files; narrow path"), nil
	}
	var matches []map[string]any
	var changes []nativeFileChange
	truncated := false
	for _, path := range listing.Files {
		_, relative, info, err := secureReadPath(d.root, path)
		if err != nil || !info.Mode().IsRegular() {
			return toolError(call, "AST path changed or escaped workspace"), nil
		}
		content, err := nativeWorkspaceFile(d.root, relative, 1<<20)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		if len(content) > 1<<20 || !utf8.Valid(content) {
			return toolError(call, "AST file must be UTF-8 and at most 1 MiB"), nil
		}
		arguments := []string{"run", "--config", parserConfig, "--lang", input.Language, "--pattern", input.Pattern, "--json=compact", "--stdin"}
		if input.Rewrite != nil {
			arguments = append(arguments, "--rewrite", *input.Rewrite)
		}
		output, runErr := nativeRun(ctx, parserRoot, string(content), program, arguments...)
		var exit *exec.ExitError
		if runErr != nil && (!errors.As(runErr, &exit) || exit.ExitCode() != 1) {
			return toolError(call, runErr.Error()), nil
		}
		var found []map[string]any
		if err := json.Unmarshal([]byte(output), &found); err != nil {
			return toolError(call, "invalid or oversized ast-grep output: "+err.Error()), nil
		}
		if len(matches)+len(found) > input.Limit {
			truncated = true
			if d.operation == ToolASTEdit {
				return toolError(call, "AST edit exceeds match limit; narrow path/pattern before applying"), nil
			}
			found = found[:input.Limit-len(matches)]
		}
		for _, match := range found {
			match["file"], match["tag"] = path, computeHashlineTag(string(content))
			matches = append(matches, match)
		}
		if input.Rewrite != nil && len(found) > 0 {
			after, err := applyASTReplacements(string(content), found)
			if err != nil {
				return toolError(call, err.Error()), nil
			}
			changes = append(changes, nativeFileChange{path, string(content), after})
		}
		if truncated {
			break
		}
	}
	if d.operation == ToolASTEdit {
		return applyNativeChanges(ctx, call, d.edit, changes, !input.Apply)
	}
	return nativeJSONResult(call, map[string]any{"matches": matches, "truncated": truncated})
}

func applyASTReplacements(content string, matches []map[string]any) (string, error) {
	type replacement struct {
		Start, End int
		Text       string
	}
	var replacements []replacement
	for _, match := range matches {
		encoded, err := json.Marshal(match)
		if err != nil {
			return "", err
		}
		var value struct {
			Replacement *string `json:"replacement"`
			Offsets     *struct {
				Start int `json:"start"`
				End   int `json:"end"`
			} `json:"replacementOffsets"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			return "", err
		}
		if value.Replacement == nil || value.Offsets == nil {
			return "", errors.New("ast-grep did not return a replacement")
		}
		replacements = append(replacements, replacement{value.Offsets.Start, value.Offsets.End, *value.Replacement})
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].Start < replacements[j].Start })
	var output strings.Builder
	offset := 0
	for _, edit := range replacements {
		if edit.Start < offset || edit.End < edit.Start || edit.End > len(content) || !utf8.ValidString(content[:edit.Start]) || !utf8.ValidString(content[:edit.End]) {
			return "", fmt.Errorf("overlapping or invalid AST replacement range %d:%d", edit.Start, edit.End)
		}
		output.WriteString(content[offset:edit.Start])
		output.WriteString(edit.Text)
		offset = edit.End
		if output.Len() > 4<<20 {
			return "", errors.New("AST replacement exceeds 4 MiB")
		}
	}
	output.WriteString(content[offset:])
	return output.String(), nil
}
