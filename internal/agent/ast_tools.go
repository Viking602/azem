package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/tool"
)

const (
	ToolASTGrep          = "ast_grep"
	defaultASTGrepLimit  = 50
	maxASTPatternBytes   = 64 << 10
	maxASTMatchTextBytes = 16 << 10
)

type astGrepDriver struct {
	root         string
	bridge       *astBridge
	snapshotRead tool.Driver
	resources    *resource.Router
}

type astGrepInput struct {
	Pattern string `json:"pat"`
	Path    string `json:"path,omitempty"`
	Skip    int    `json:"skip,omitempty"`
}

type astGrepResult struct {
	Matches          []astNativeMatch `json:"matches"`
	TotalMatches     int              `json:"totalMatches"`
	FilesWithMatches int              `json:"filesWithMatches"`
	FilesSearched    int              `json:"filesSearched"`
	LimitReached     bool             `json:"limitReached"`
	ParseErrors      []string         `json:"parseErrors,omitempty"`
	Content          string           `json:"content"`
}

type astSearchTarget struct {
	baseAbsolute string
	baseRelative string
	glob         string
	isFile       bool
	internalURI  string
	internalData []byte
	cleanup      func()
}

func newASTGrepDriver(root string, bridge *astBridge, snapshotRead tool.Driver, resources *resource.Router) tool.Driver {
	if bridge == nil {
		bridge = newASTBridge()
	}
	return &astGrepDriver{root: root, bridge: bridge, snapshotRead: snapshotRead, resources: resources}
}

// MatchASTSnapshot evaluates bounded structural patterns against one in-memory
// source snapshot without exposing a provider or filesystem execution path.
func (s *Service) MatchASTSnapshot(ctx context.Context, source, language string, patterns []string) (bool, error) {
	if s == nil || s.ast == nil {
		return false, errors.New("AST bridge is unavailable")
	}
	result, err := s.ast.match(ctx, source, language, patterns)
	if err != nil {
		return false, err
	}
	return result.TotalMatches > 0 || len(result.Matches) > 0, nil
}

func (driver *astGrepDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolASTGrep,
		Description: "Structural code search via ast-grep. pat is one parseable AST pattern; $NAME captures one node, $_ is unbound, and $$$NAME captures zero or more nodes. Narrow path to one language and treat parse errors as query failures.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"pat"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"pat":  {Type: "string", Description: "One structural AST pattern."},
				"path": {Type: "string", Description: "File, directory, glob, internal URI, or semicolon-separated targets; default '.'."},
				"skip": {Type: "integer", Description: "Matches to skip."},
			},
		},
		Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *astGrepDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input astGrepInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return astToolError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Pattern = strings.TrimSpace(input.Pattern)
	if input.Pattern == "" {
		return astToolError(call, errors.New("pat must be a non-empty pattern")), nil
	}
	if err := validateASTPattern(input.Pattern); err != nil {
		return astToolError(call, err), nil
	}
	if len(input.Pattern) > maxASTPatternBytes {
		return astToolError(call, errors.New("pat exceeds 64 KiB")), nil
	}
	if input.Skip < 0 || input.Skip > 100_000 {
		return astToolError(call, errors.New("skip must be between 0 and 100000")), nil
	}
	caller, _ := InvocationFromContext(ctx)
	targets, err := resolveASTSearchTargets(ctx, driver.root, driver.resources, resource.Scope{
		SessionID: caller.SessionID, RunID: caller.TeamRunID, Workspace: driver.root,
	}, input.Path)
	if err != nil {
		return astToolError(call, err), nil
	}
	defer cleanupASTTargets(targets)
	retainedCapacity := input.Skip + defaultASTGrepLimit + 1
	nativeOffset, visibleSkip := 0, input.Skip
	if len(targets) == 1 {
		nativeOffset, visibleSkip = input.Skip, 0
		retainedCapacity = defaultASTGrepLimit + 1
	}
	var aggregate astGrepResult
	for _, target := range targets {
		var native astNativeGrepResult
		options := map[string]any{
			"patterns": []string{input.Pattern}, "path": target.baseAbsolute, "offset": nativeOffset,
			"limit": retainedCapacity, "includeMeta": true,
		}
		if target.glob != "" {
			options["glob"] = target.glob
		}
		if err := driver.bridge.invoke(ctx, "grep", options, &native); err != nil {
			return astToolError(call, err), nil
		}
		aggregate.TotalMatches += native.TotalMatches
		aggregate.FilesWithMatches += native.FilesWithMatches
		aggregate.FilesSearched += native.FilesSearched
		aggregate.LimitReached = aggregate.LimitReached || native.LimitReached
		aggregate.ParseErrors = append(aggregate.ParseErrors, native.ParseErrors...)
		for _, match := range native.Matches {
			rebased, err := rebaseASTResultPath(driver.root, target, match.Path)
			if err != nil {
				return astToolError(call, err), nil
			}
			match.Path = rebased
			aggregate.Matches = append(aggregate.Matches, match)
		}
	}
	sort.Slice(aggregate.Matches, func(left, right int) bool {
		return compareASTMatches(aggregate.Matches[left], aggregate.Matches[right]) < 0
	})
	if visibleSkip >= len(aggregate.Matches) {
		aggregate.Matches = nil
	} else {
		aggregate.Matches = aggregate.Matches[visibleSkip:]
	}
	if len(aggregate.Matches) > defaultASTGrepLimit {
		aggregate.Matches = aggregate.Matches[:defaultASTGrepLimit]
		aggregate.LimitReached = true
	}
	aggregate.Content = driver.renderASTMatches(ctx, call.ID, aggregate.Matches, targets)
	if aggregate.Content == "" {
		aggregate.Content = "No AST matches."
	}
	if len(aggregate.ParseErrors) > 0 {
		aggregate.Content += "\nParse errors:\n- " + strings.Join(aggregate.ParseErrors[:min(10, len(aggregate.ParseErrors))], "\n- ")
	}
	structured, _ := json.Marshal(aggregate)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: aggregate.Content, Structured: structured}, nil
}

func resolveASTSearchTargets(ctx context.Context, root string, resources *resource.Router, scope resource.Scope, raw string) ([]astSearchTarget, error) {
	values := splitASTTargetList(raw)
	if len(values) == 0 {
		values = []string{"."}
	}
	targets := make([]astSearchTarget, 0, len(values))
	for _, value := range values {
		if strings.Contains(value, "://") {
			if resources == nil {
				cleanupASTTargets(targets)
				return nil, errors.New("internal resources are unavailable")
			}
			result, err := resources.Read(ctx, value, "raw", scope)
			if err != nil {
				cleanupASTTargets(targets)
				return nil, err
			}
			if !utf8.Valid(result.Data) {
				cleanupASTTargets(targets)
				return nil, fmt.Errorf("AST target %s is not UTF-8 text", value)
			}
			extension := filepath.Ext(strings.SplitN(value, "?", 2)[0])
			file, err := os.CreateTemp("", "azem-ast-*"+extension)
			if err != nil {
				cleanupASTTargets(targets)
				return nil, err
			}
			path := file.Name()
			if _, err := file.Write(result.Data); err == nil {
				err = file.Close()
			} else {
				_ = file.Close()
			}
			if err != nil {
				_ = os.Remove(path)
				cleanupASTTargets(targets)
				return nil, err
			}
			targets = append(targets, astSearchTarget{baseAbsolute: path, baseRelative: value, isFile: true, internalURI: value, internalData: append([]byte(nil), result.Data...), cleanup: func() { _ = os.Remove(path) }})
			continue
		}
		base, glob := splitASTGlob(value)
		absolute, relative, info, err := secureReadPath(root, base)
		if err != nil {
			cleanupASTTargets(targets)
			return nil, fmt.Errorf("AST target %s: %w", value, err)
		}
		if glob != "" && !info.IsDir() {
			cleanupASTTargets(targets)
			return nil, fmt.Errorf("AST glob base %s is not a directory", base)
		}
		targets = append(targets, astSearchTarget{baseAbsolute: absolute, baseRelative: relative, glob: glob, isFile: !info.IsDir()})
	}
	return targets, nil
}

func splitASTTargetList(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ";") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func splitASTGlob(value string) (string, string) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if strings.ContainsAny(part, "*?[") || strings.Contains(part, "{") {
			base := strings.Join(parts[:index], "/")
			if base == "" {
				base = "."
			}
			return base, strings.Join(parts[index:], "/")
		}
	}
	return value, ""
}

func cleanupASTTargets(targets []astSearchTarget) {
	for _, target := range targets {
		if target.cleanup != nil {
			target.cleanup()
		}
	}
}

func rebaseASTResultPath(root string, target astSearchTarget, nativePath string) (string, error) {
	if target.internalURI != "" {
		return target.internalURI, nil
	}
	if target.isFile {
		return filepath.ToSlash(target.baseRelative), nil
	}
	relative := filepath.ToSlash(filepath.Join(filepath.FromSlash(target.baseRelative), filepath.FromSlash(nativePath)))
	relative, err := workspaceRelativePath(root, relative)
	if err != nil {
		return "", fmt.Errorf("AST native result path %q escapes workspace", nativePath)
	}
	if _, _, _, err := secureReadPath(root, relative); err != nil {
		return "", fmt.Errorf("AST native result path %q is invalid: %w", nativePath, err)
	}
	return relative, nil
}

func compareASTMatches(left, right astNativeMatch) int {
	if left.Path != right.Path {
		return strings.Compare(left.Path, right.Path)
	}
	for _, pair := range [][2]int{{left.StartLine, right.StartLine}, {left.StartColumn, right.StartColumn}, {left.EndLine, right.EndLine}, {left.EndColumn, right.EndColumn}, {left.ByteStart, right.ByteStart}, {left.ByteEnd, right.ByteEnd}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func (driver *astGrepDriver) renderASTMatches(ctx context.Context, callID string, matches []astNativeMatch, targets []astSearchTarget) string {
	headers := make(map[string]string)
	internal := make(map[string][]byte)
	for _, target := range targets {
		if target.internalURI != "" {
			internal[target.internalURI] = target.internalData
		}
	}
	var output strings.Builder
	currentPath := ""
	for _, match := range matches {
		if match.Path != currentPath {
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			header := headers[match.Path]
			if header == "" {
				if payload, ok := internal[match.Path]; ok {
					header = "[" + match.Path + "#" + computeHashlineTag(normalizeHashlineText(payload)) + "]"
				} else {
					header = driver.astSnapshotHeader(ctx, callID, match.Path)
				}
				headers[match.Path] = header
			}
			output.WriteString(header)
			currentPath = match.Path
		}
		text := match.Text
		if len(text) > maxASTMatchTextBytes {
			text = text[:maxASTMatchTextBytes] + "…"
		}
		for index, line := range strings.Split(text, "\n") {
			fmt.Fprintf(&output, "\n%d:%s", match.StartLine+index, line)
		}
	}
	return output.String()
}

func (driver *astGrepDriver) astSnapshotHeader(ctx context.Context, callID, path string) string {
	if driver.snapshotRead == nil {
		return "[" + path + "#0000]"
	}
	arguments, _ := json.Marshal(map[string]any{"path": path, "startLine": 1, "endLine": 1})
	result, err := driver.snapshotRead.Execute(ctx, tool.Call{ID: callID + "-snapshot", Name: ToolReadFile, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		return "[" + path + "#0000]"
	}
	var observed ReadFileToolResult
	if json.Unmarshal(result.Structured, &observed) != nil || observed.Tag == "" {
		return "[" + path + "#0000]"
	}
	return "[" + path + "#" + observed.Tag + "]"
}

func validateASTPattern(pattern string) error {
	if regexp.MustCompile(`(^|[^$])\$\$[A-Z_][A-Z0-9_]*`).MatchString(pattern) {
		return errors.New("invalid metavariable: use $$$NAME, not $$NAME")
	}
	if regexp.MustCompile(`[A-Za-z0-9_]\$[A-Z_][A-Z0-9_]*`).MatchString(pattern) {
		return errors.New("metavariables must capture a whole AST node, not part of an identifier")
	}
	return nil
}

func astToolError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: call.Name + " failed: " + err.Error(), IsError: true}
}
