package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/tool"
)

const (
	reliableSearchMaxResults     = 200
	reliableSearchJSONLineMax    = 2 << 20
	reliableSearchMaxFileSizeArg = "1M"
)

type reliableSearchInput struct {
	Query      string `json:"query"`
	Regexp     bool   `json:"regexp,omitempty"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type reliableSearchDriver struct {
	root      string
	read      tool.Driver
	resources *resource.Router
}

func newReliableSearchDriver(root string, read tool.Driver, resources *resource.Router) tool.Driver {
	return reliableSearchDriver{root: root, read: read, resources: resources}
}

func (d reliableSearchDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolSearch,
		Description: "Search workspace text with ripgrep or one exact internal resource URI (including ssh://). Workspace search is case-sensitive, literal by default, supports ripgrep regex and glob syntax, respects ignore files, and returns grouped [PATH#TAG] matches. maxResults caps matched lines, not files scanned.",
		InputSchema: tool.Schema{
			Type: "object",
			Properties: map[string]tool.Schema{
				"query":      {Type: "string"},
				"path":       {Type: "string"},
				"regexp":     {Type: "boolean"},
				"glob":       {Type: "string"},
				"maxResults": {Type: "integer"},
			},
			Required:             []string{"query"},
			AdditionalProperties: &additional,
		},
	}
}

func (d reliableSearchDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input reliableSearchInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return reliableSearchError(call, "invalid arguments"), nil
	}
	if strings.TrimSpace(input.Query) == "" {
		return reliableSearchError(call, "query must not be empty"), nil
	}
	maxResults := input.MaxResults
	if maxResults <= 0 || maxResults > reliableSearchMaxResults {
		maxResults = reliableSearchMaxResults
	}
	if strings.Contains(input.Path, "://") {
		var expression *regexp.Regexp
		if input.Regexp {
			compiled, err := regexp.Compile(input.Query)
			if err != nil {
				return reliableSearchError(call, "invalid regexp: "+err.Error()), nil
			}
			expression = compiled
		}
		return d.searchInternalResource(ctx, call, input, expression, maxResults), nil
	}
	return d.searchWorkspace(ctx, call, input, maxResults)
}

type ripgrepJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

func (d reliableSearchDriver) searchWorkspace(ctx context.Context, call tool.Call, input reliableSearchInput, maxResults int) (tool.Result, error) {
	ripgrep, err := resolveRipgrepExecutable()
	if err != nil {
		return reliableSearchError(call, err.Error()), nil
	}
	target := "."
	if strings.TrimSpace(input.Path) != "" {
		_, relative, info, pathErr := secureReadPath(d.root, input.Path)
		if pathErr != nil {
			return reliableSearchError(call, pathErr.Error()), nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return reliableSearchError(call, "path must be a regular file or directory"), nil
		}
		target = filepath.ToSlash(relative)
		if target == "" {
			target = "."
		}
	}
	args := []string{
		"--json",
		"--line-number",
		"--color=never",
		"--no-config",
		"--no-messages",
		"--hidden",
		"--glob",
		"!.git/**",
		"--max-filesize",
		reliableSearchMaxFileSizeArg,
		"--case-sensitive",
	}
	if !input.Regexp {
		args = append(args, "--fixed-strings")
	}
	if glob := strings.TrimSpace(strings.ReplaceAll(input.Glob, "\\", "/")); glob != "" {
		args = append(args, "--glob", glob)
	}
	args = append(args, "--", input.Query, target)

	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(searchCtx, ripgrep, args...)
	command.Dir = d.root
	stdout, err := command.StdoutPipe()
	if err != nil {
		return reliableSearchError(call, "open ripgrep output: "+err.Error()), nil
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return reliableSearchError(call, "start ripgrep: "+err.Error()), nil
	}

	matchesByPath := make(map[string][]SearchMatch)
	total := 0
	truncated := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), reliableSearchJSONLineMax)
	for scanner.Scan() {
		var event ripgrepJSONEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			cancel()
			_ = command.Wait()
			return reliableSearchError(call, "decode ripgrep output: "+err.Error()), nil
		}
		if event.Type != "match" || event.Data.LineNumber < 1 || event.Data.Path.Text == "" {
			continue
		}
		path, ok := normalizeRipgrepPath(d.root, event.Data.Path.Text)
		if !ok {
			continue
		}
		line := strings.TrimSuffix(strings.TrimSuffix(event.Data.Lines.Text, "\n"), "\r")
		matchesByPath[path] = append(matchesByPath[path], SearchMatch{
			LineNumber: event.Data.LineNumber,
			Line:       line,
		})
		total++
		if total >= maxResults {
			truncated = true
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}
	if !truncated && scanErr != nil {
		return reliableSearchError(call, "read ripgrep output: "+scanErr.Error()), nil
	}
	if !truncated && waitErr != nil {
		if exit, ok := waitErr.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = waitErr.Error()
			}
			return reliableSearchError(call, message), nil
		}
	}

	paths := make([]string, 0, len(matchesByPath))
	for path := range matchesByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := SearchToolResult{Truncated: truncated}
	for _, path := range paths {
		arguments, _ := json.Marshal(map[string]string{"path": path})
		readResult, err := d.read.Execute(ctx, tool.Call{
			ID: call.ID + "-read", Name: ToolReadFile, Arguments: arguments,
		}, nil)
		if err != nil {
			return tool.Result{}, err
		}
		if readResult.IsError {
			return reliableSearchError(call, "snapshot "+path+": "+readResult.Content), nil
		}
		var read ReadFileToolResult
		if err := json.Unmarshal(readResult.Structured, &read); err != nil {
			return reliableSearchError(call, "decode snapshot "+path+": "+err.Error()), nil
		}
		result.Files = append(result.Files, SearchToolFile{
			Path: read.Path, Tag: read.Tag, Header: "[" + read.Path + "#" + read.Tag + "]",
			Matches: matchesByPath[path],
		})
	}
	result.Content = renderReliableSearch(result.Files)
	structured, _ := json.Marshal(result)
	return tool.Result{
		ToolCallID: call.ID, Name: call.Name, Content: result.Content, Structured: structured,
	}, nil
}

func resolveRipgrepExecutable() (string, error) {
	name := "rg"
	if runtime.GOOS == "windows" {
		name = "rg.exe"
	}
	if executable, err := os.Executable(); err == nil {
		if bundled, ok := bundledRipgrepPath(executable, name); ok {
			return bundled, nil
		}
		if packagedRuntimeRequiresBundledRipgrep(executable) {
			return "", fmt.Errorf("bundled ripgrep executable is unavailable")
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("bundled ripgrep executable is unavailable")
}

func bundledRipgrepPath(executable, name string) (string, bool) {
	candidate := filepath.Join(filepath.Dir(executable), name)
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}

func packagedRuntimeRequiresBundledRipgrep(executable string) bool {
	directory := filepath.Dir(executable)
	if filepath.Base(directory) == "MacOS" &&
		filepath.Base(filepath.Dir(directory)) == "Contents" {
		return true
	}
	for _, renderer := range []string{"azem-gpui", "azem-gpui.exe"} {
		if info, err := os.Stat(filepath.Join(directory, renderer)); err == nil &&
			info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func normalizeRipgrepPath(root, path string) (string, bool) {
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", false
		}
		path = relative
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "" || path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}
	return path, true
}

func (d reliableSearchDriver) searchInternalResource(ctx context.Context, call tool.Call, input reliableSearchInput, expression *regexp.Regexp, maxResults int) tool.Result {
	if d.resources == nil {
		return reliableSearchError(call, "internal resources are unavailable")
	}
	caller, _ := InvocationFromContext(ctx)
	result, err := d.resources.Read(ctx, input.Path, "raw", resource.Scope{SessionID: caller.SessionID, RunID: caller.TeamRunID, Workspace: d.root})
	if err != nil {
		return reliableSearchError(call, err.Error())
	}
	if result.Metadata["directory"] == "true" {
		return reliableSearchError(call, "cannot search an internal directory listing; read a concrete file")
	}
	if strings.IndexByte(string(result.Data), 0) >= 0 || !utf8.Valid(result.Data) {
		return reliableSearchError(call, "internal resource is not UTF-8 text")
	}
	text := string(result.Data)
	lines := strings.Split(text, "\n")
	matches := make([]SearchMatch, 0)
	for index, line := range lines {
		matched := expression != nil && expression.MatchString(line) || expression == nil && strings.Contains(line, input.Query)
		if matched {
			matches = append(matches, SearchMatch{LineNumber: index + 1, Line: line})
			if len(matches) >= maxResults {
				break
			}
		}
	}
	tag := computeHashlineTag(normalizeHashlineText(result.Data))
	file := SearchToolFile{Path: input.Path, Tag: tag, Header: "[" + input.Path + "#" + tag + "]", Matches: matches}
	output := SearchToolResult{Files: []SearchToolFile{file}, Truncated: len(matches) >= maxResults}
	if len(matches) == 0 {
		output.Files = nil
	}
	output.Content = renderReliableSearch(output.Files)
	structured, _ := json.Marshal(output)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: output.Content, Structured: structured}
}

func renderReliableSearch(files []SearchToolFile) string {
	if len(files) == 0 {
		return "No matches found."
	}
	var output strings.Builder
	for index, file := range files {
		if index > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(file.Header)
		for _, match := range file.Matches {
			fmt.Fprintf(&output, "\n%d:%s", match.LineNumber, match.Line)
		}
		output.WriteByte('\n')
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func reliableSearchError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "search failed: " + message, IsError: true}
}
