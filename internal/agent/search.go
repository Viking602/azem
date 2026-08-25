package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"
)

const (
	reliableSearchMaxResults    = 200
	reliableSearchFileListLimit = 100_000
	reliableSearchGitOutputMax  = 16 << 20
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
	ws        coding.Workspace
	read      tool.Driver
	resources *resource.Router
}

func newReliableSearchDriver(root string, ws coding.Workspace, read tool.Driver, resources *resource.Router) tool.Driver {
	return reliableSearchDriver{root: root, ws: ws, read: read, resources: resources}
}

func (d reliableSearchDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        coding.ToolSearch,
		Description: "Search Git-tracked/unignored workspace text or one exact internal resource URI (including ssh://) for a case-sensitive substring or Go regexp. Returns grouped [PATH#TAG] matches; maxResults caps matched lines, not files scanned.",
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
		EffectType:         tool.EffectReadOnly,
		RequiresActionTask: false,
		RiskLevel:          "low",
		PolicyTags:         []string{"coding", "search"},
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
	var expression *regexp.Regexp
	if input.Regexp {
		compiled, err := regexp.Compile(input.Query)
		if err != nil {
			return reliableSearchError(call, "invalid regexp: "+err.Error()), nil
		}
		expression = compiled
	}
	maxResults := input.MaxResults
	if maxResults <= 0 || maxResults > reliableSearchMaxResults {
		maxResults = reliableSearchMaxResults
	}
	if strings.Contains(input.Path, "://") {
		return d.searchInternalResource(ctx, call, input, expression, maxResults), nil
	}
	paths, listedTruncated, err := d.searchPaths(ctx, input.Glob)
	if err != nil {
		return reliableSearchError(call, err.Error()), nil
	}

	result := coding.SearchToolResult{Truncated: listedTruncated}
	total := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		candidate, err := d.ws.ReadFile(ctx, coding.ReadFileRequest{Path: path})
		if err != nil || strings.IndexByte(candidate.Text, 0) >= 0 || !searchTextMatches(candidate.Text, input.Query, expression) {
			continue
		}
		arguments, _ := json.Marshal(map[string]string{"path": path})
		readResult, err := d.read.Execute(ctx, tool.Call{ID: call.ID + "-read", Name: coding.ToolReadFile, Arguments: arguments}, nil)
		if err != nil {
			return tool.Result{}, err
		}
		if readResult.IsError {
			continue
		}
		var read coding.ReadFileToolResult
		if json.Unmarshal(readResult.Structured, &read) != nil {
			continue
		}
		matches := searchNumberedContent(read.Content, input.Query, expression, maxResults-total)
		if len(matches) == 0 {
			continue
		}
		result.Files = append(result.Files, coding.SearchToolFile{Path: read.Path, Tag: read.Tag, Header: "[" + read.Path + "#" + read.Tag + "]", Matches: matches})
		total += len(matches)
		if total >= maxResults {
			result.Truncated = true
			break
		}
	}
	result.Content = renderReliableSearch(result.Files)
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: result.Content, Structured: structured}, nil
}

func (d reliableSearchDriver) searchInternalResource(ctx context.Context, call tool.Call, input reliableSearchInput, expression *regexp.Regexp, maxResults int) tool.Result {
	if d.resources == nil {
		return reliableSearchError(call, "internal resources are unavailable")
	}
	caller, _ := tool.CallerFromContext(ctx)
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
	matches := make([]coding.SearchMatch, 0)
	for index, line := range lines {
		matched := expression != nil && expression.MatchString(line) || expression == nil && strings.Contains(line, input.Query)
		if matched {
			matches = append(matches, coding.SearchMatch{LineNumber: index + 1, Line: line})
			if len(matches) >= maxResults {
				break
			}
		}
	}
	tag := computeHashlineTag(normalizeHashlineText(result.Data))
	file := coding.SearchToolFile{Path: input.Path, Tag: tag, Header: "[" + input.Path + "#" + tag + "]", Matches: matches}
	output := coding.SearchToolResult{Files: []coding.SearchToolFile{file}, Truncated: len(matches) >= maxResults}
	if len(matches) == 0 {
		output.Files = nil
	}
	output.Content = renderReliableSearch(output.Files)
	structured, _ := json.Marshal(output)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: output.Content, Structured: structured}
}

func (d reliableSearchDriver) searchPaths(ctx context.Context, pattern string) ([]string, bool, error) {
	paths, err := gitSearchPaths(ctx, d.root)
	truncated := false
	if err != nil {
		listed, listErr := d.ws.ListFiles(ctx, coding.ListFilesRequest{
			Ignore: []string{"node_modules", "*/node_modules", "vendor", "*/vendor", "dist", "*/dist", "build", "*/build"},
			Limit:  reliableSearchFileListLimit,
		})
		if listErr != nil {
			return nil, false, listErr
		}
		paths, truncated = listed.Files, listed.Truncated
	}
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return paths, truncated, nil
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		matched, matchErr := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(path))
		if matchErr != nil {
			return nil, false, fmt.Errorf("invalid glob %q: %w", pattern, matchErr)
		}
		if matched {
			filtered = append(filtered, path)
		}
	}
	return filtered, truncated, nil
}

func gitSearchPaths(ctx context.Context, root string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z", "--")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	if len(output) > reliableSearchGitOutputMax {
		return nil, fmt.Errorf("git file list exceeds %d bytes", reliableSearchGitOutputMax)
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, raw := range strings.Split(string(output), "\x00") {
		path := filepath.ToSlash(filepath.Clean(raw))
		if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func searchTextMatches(text, query string, expression *regexp.Regexp) bool {
	if expression != nil {
		return expression.MatchString(text)
	}
	return strings.Contains(text, query)
}

func searchNumberedContent(content, query string, expression *regexp.Regexp, remaining int) []coding.SearchMatch {
	if remaining <= 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	matches := make([]coding.SearchMatch, 0)
	for _, line := range lines[1:] {
		separator := strings.IndexByte(line, ':')
		if separator <= 0 {
			continue
		}
		lineNumber, err := strconv.Atoi(line[:separator])
		if err != nil {
			continue
		}
		text := line[separator+1:]
		matched := (expression != nil && expression.MatchString(text)) || (expression == nil && strings.Contains(text, query))
		if !matched {
			continue
		}
		matches = append(matches, coding.SearchMatch{LineNumber: lineNumber, Line: text})
		if len(matches) >= remaining {
			break
		}
	}
	return matches
}

func renderReliableSearch(files []coding.SearchToolFile) string {
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
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.search failed: " + message, IsError: true}
}
