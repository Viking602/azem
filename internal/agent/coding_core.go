package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/venat/tool"
)

const (
	ToolListFiles    = "coding.list_files"
	ToolReadFile     = "coding.read_file"
	ToolSearch       = "coding.search"
	ToolGitDiff      = "coding.git_diff"
	ToolEditHashline = "coding.edit_hashline"
	ToolWriteFile    = "coding.write_file"
	ToolGofmt        = "coding.gofmt"
	ToolGoTest       = "coding.go_test"
)

const (
	defaultWorkspaceListLimit = 1000
	maxWorkspaceFileBytes     = 16 << 20
	maxCommandOutputBytes     = 1 << 20
)

type ListFilesRequest struct {
	Glob          string   `json:"glob,omitempty"`
	Ignore        []string `json:"ignore,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	MaxFileBytes  int64    `json:"maxFileBytes,omitempty"`
	IncludeBinary bool     `json:"includeBinary,omitempty"`
}

type ListFilesResult struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

type ReadFileRequest struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	MaxBytes  int    `json:"maxBytes,omitempty"`
}

type ReadFileResult struct {
	Path      string `json:"path"`
	Tag       string `json:"tag"`
	Text      string `json:"text"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	SliceText string `json:"sliceText"`
	LineCount int    `json:"lineCount"`
	Truncated bool   `json:"truncated"`
}

type ReadFileToolResult struct {
	Path      string `json:"path"`
	Tag       string `json:"tag"`
	Header    string `json:"header"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	LineCount int    `json:"lineCount"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type SearchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

type SearchToolFile struct {
	Path    string        `json:"path"`
	Tag     string        `json:"tag"`
	Header  string        `json:"header"`
	Matches []SearchMatch `json:"matches"`
}

type SearchToolResult struct {
	Files     []SearchToolFile `json:"files"`
	Truncated bool             `json:"truncated"`
	Content   string           `json:"content"`
}

type EditSectionResult struct {
	Path             string   `json:"path"`
	Op               string   `json:"op"`
	OldTag           string   `json:"oldTag"`
	NewTag           string   `json:"newTag"`
	Header           string   `json:"header"`
	FirstChangedLine int      `json:"firstChangedLine"`
	Diff             string   `json:"diff"`
	Warnings         []string `json:"warnings,omitempty"`
	Recovered        bool     `json:"recovered,omitempty"`
}

type EditHashlineResult struct {
	DryRun            bool                `json:"dryRun"`
	Sections          []EditSectionResult `json:"sections"`
	Content           string              `json:"content"`
	OldTags           []string            `json:"oldTags"`
	NewTags           []string            `json:"newTags"`
	FirstChangedLines []int               `json:"firstChangedLines"`
	DiffHash          string              `json:"diffHash"`
	Recovered         bool                `json:"recovered,omitempty"`
}

type GofmtToolResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Tag     string `json:"tag"`
	Header  string `json:"header"`
	Diff    string `json:"diff"`
}

type GoTestToolResult struct {
	Args      []string `json:"args"`
	ExitCode  int      `json:"exitCode"`
	Passed    bool     `json:"passed"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	Truncated bool     `json:"truncated"`
	TimedOut  bool     `json:"timedOut"`
	Duration  string   `json:"duration"`
}

type GitDiffToolResult struct {
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

type Workspace interface {
	Root() string
	ListFiles(context.Context, ListFilesRequest) (ListFilesResult, error)
	ReadFile(context.Context, ReadFileRequest) (ReadFileResult, error)
}

type localWorkspace struct{ root string }

func newLocalWorkspace(root string) Workspace {
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &localWorkspace{root: filepath.Clean(root)}
}

func NewLocalWorkspace(root string) Workspace {
	return newLocalWorkspace(root)
}

func NewToolSet(workspace Workspace) []tool.Driver {
	if workspace == nil {
		return nil
	}
	return []tool.Driver{
		listFilesDriver{workspace: workspace},
		snapshotReadDriver{workspace: workspace},
		gofmtDriver{workspace: workspace},
		goTestDriver{root: workspace.Root()},
		gitDiffDriver{root: workspace.Root()},
	}
}

func (workspace *localWorkspace) Root() string { return workspace.root }

func (workspace *localWorkspace) ListFiles(ctx context.Context, request ListFilesRequest) (ListFilesResult, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = defaultWorkspaceListLimit
	}
	maxBytes := request.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = maxWorkspaceFileBytes
	}
	result := ListFilesResult{Files: make([]string, 0, min(limit, 128))}
	err := filepath.WalkDir(workspace.root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(workspace.root, candidate)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if ignoredWorkspaceDirectory(entry.Name(), relative, request.Ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if ignoredWorkspacePath(relative, request.Ignore) || !workspaceGlobMatches(relative, request.Glob) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxBytes {
			return nil
		}
		if !request.IncludeBinary {
			payload, _, err := readFileLimited(candidate, 8<<10)
			if err != nil || bytes.IndexByte(payload, 0) >= 0 {
				return nil
			}
		}
		if len(result.Files) == limit {
			result.Truncated = true
			return fs.SkipAll
		}
		result.Files = append(result.Files, relative)
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return ListFilesResult{}, err
	}
	sort.Strings(result.Files)
	return result, nil
}

func (workspace *localWorkspace) ReadFile(ctx context.Context, request ReadFileRequest) (ReadFileResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadFileResult{}, err
	}
	absolute, relative, info, err := secureReadPath(workspace.root, request.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxWorkspaceFileBytes {
		return ReadFileResult{}, fmt.Errorf("coding: read %q: file is not a bounded regular file", relative)
	}
	payload, _, err := readFileLimited(absolute, maxWorkspaceFileBytes)
	if err != nil {
		return ReadFileResult{}, err
	}
	text := normalizeHashlineText(payload)
	lines := splitWorkspaceLines(text)
	start, end := request.StartLine, request.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if len(lines) == 0 {
		start, end = 0, 0
	}
	slice := ""
	if start > 0 && start <= end {
		slice = strings.Join(lines[start-1:end], "\n")
	}
	maxBytes := request.MaxBytes
	if maxBytes <= 0 || maxBytes > defaultOMPReadBytes {
		maxBytes = defaultOMPReadBytes
	}
	truncated := len(slice) > maxBytes
	if truncated {
		slice = slice[:maxBytes]
	}
	return ReadFileResult{Path: relative, Tag: computeHashlineTag(text), Text: text, StartLine: start, EndLine: end, SliceText: slice, LineCount: len(lines), Truncated: truncated}, nil
}

func ignoredWorkspaceDirectory(name, relative string, ignores []string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build":
		return true
	}
	return ignoredWorkspacePath(relative, ignores)
}

func ignoredWorkspacePath(relative string, ignores []string) bool {
	for _, pattern := range ignores {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		matched, _ := path.Match(pattern, relative)
		baseMatched, _ := path.Match(pattern, path.Base(relative))
		if matched || baseMatched || strings.HasPrefix(relative, strings.TrimSuffix(pattern, "/*")+"/") {
			return true
		}
	}
	return false
}

func workspaceGlobMatches(relative, pattern string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return true
	}
	matched, _ := path.Match(pattern, relative)
	if matched {
		return true
	}
	matched, _ = path.Match(pattern, path.Base(relative))
	return matched
}

func splitWorkspaceLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

type listFilesDriver struct{ workspace Workspace }

func (driver listFilesDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolListFiles, Description: "List workspace-relative files matching an optional glob.", InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Properties: map[string]tool.Schema{"glob": {Type: "string", Description: "Optional path glob."}, "limit": {Type: "integer", Description: "Cap on returned paths."}}}}
}

func (driver listFilesDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Glob  string `json:"glob,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{}, err
	}
	result, err := driver.workspace.ListFiles(ctx, ListFilesRequest{Glob: input.Glob, Limit: input.Limit})
	if err != nil {
		return toolError(call, "coding.list_files failed: "+err.Error()), nil
	}
	content := strings.Join(result.Files, "\n")
	if result.Truncated {
		content += fmt.Sprintf("\n[truncated; %d files shown]", len(result.Files))
	}
	return structuredToolResult(call, content, result)
}

type snapshotReadDriver struct{ workspace Workspace }

func (driver snapshotReadDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolReadFile, Description: "Read a workspace file and return [PATH#TAG] numbered lines for hashline editing.", InputSchema: tool.Schema{Type: "object", Required: []string{"path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"path": {Type: "string"}, "startLine": {Type: "integer"}, "endLine": {Type: "integer"}, "maxBytes": {Type: "integer"}}}}
}

func (driver snapshotReadDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input ReadFileRequest
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{}, err
	}
	read, err := driver.workspace.ReadFile(ctx, input)
	if err != nil {
		return toolError(call, "coding.read_file failed: "+err.Error()), nil
	}
	header := "[" + read.Path + "#" + read.Tag + "]"
	content := header
	if read.LineCount > 0 {
		var body strings.Builder
		for offset, line := range strings.Split(read.SliceText, "\n") {
			if offset > 0 {
				body.WriteByte('\n')
			}
			fmt.Fprintf(&body, "%d:%s", read.StartLine+offset, line)
		}
		content += "\n" + body.String()
	}
	result := ReadFileToolResult{Path: read.Path, Tag: read.Tag, Header: header, StartLine: read.StartLine, EndLine: read.EndLine, LineCount: read.LineCount, Content: content, Truncated: read.Truncated}
	return structuredToolResult(call, content, result)
}

type gofmtDriver struct{ workspace Workspace }

func (driver gofmtDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolGofmt, Description: "Format one Go file in-process with go/format.", InputSchema: tool.Schema{Type: "object", Required: []string{"path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"path": {Type: "string"}}}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "workspace-files"}
}

func (driver gofmtDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{}, err
	}
	if !strings.HasSuffix(input.Path, ".go") {
		return toolError(call, "coding.gofmt rejected: only .go files can be formatted"), nil
	}
	read, err := driver.workspace.ReadFile(ctx, ReadFileRequest{Path: input.Path})
	if err != nil {
		return toolError(call, "coding.gofmt failed: "+err.Error()), nil
	}
	formatted, err := format.Source([]byte(read.Text))
	if err != nil {
		return toolError(call, "coding.gofmt rejected: the file does not parse as Go: "+err.Error()), nil
	}
	formattedText := string(formatted)
	changed := formattedText != read.Text
	if changed {
		destination, err := brokerDestination(driver.workspace.Root(), read.Path, true)
		if err != nil {
			return toolError(call, "coding.gofmt failed: "+err.Error()), nil
		}
		info, err := os.Stat(destination)
		if err != nil {
			return toolError(call, "coding.gofmt failed: "+err.Error()), nil
		}
		if err := os.WriteFile(destination, formatted, info.Mode().Perm()); err != nil {
			return toolError(call, "coding.gofmt failed: "+err.Error()), nil
		}
	}
	tag := computeHashlineTag(formattedText)
	header := "[" + read.Path + "#" + tag + "]"
	diff := compactHashlineDiff(read.Text, formattedText)
	content := header + "\n"
	if changed {
		content += "formatted " + read.Path
		if diff != "" {
			content += "\n\n--- compact diff ---\n" + diff
		}
	} else {
		content += "already formatted " + read.Path
	}
	return structuredToolResult(call, content, GofmtToolResult{Path: read.Path, Changed: changed, Tag: tag, Header: header, Diff: diff})
}

type goTestDriver struct{ root string }

func (driver goTestDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolGoTest, Description: "Run go test over a workspace package pattern without a shell.", InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Properties: map[string]tool.Schema{"package": {Type: "string"}, "run": {Type: "string"}}}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "workspace-processes"}
}

func (driver goTestDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Package string `json:"package,omitempty"`
		Run     string `json:"run,omitempty"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{}, err
	}
	packagePattern := strings.TrimSpace(input.Package)
	if packagePattern == "" {
		packagePattern = "./..."
	}
	if !strings.HasPrefix(packagePattern, "./") || packagePatternHasParentTraversal(packagePattern) {
		return toolError(call, "coding.go_test rejected: package must be a ./-prefixed workspace pattern without parent traversal"), nil
	}
	args := []string{"test", packagePattern}
	if strings.TrimSpace(input.Run) != "" {
		args = append(args, "-run", input.Run)
	}
	started := time.Now()
	stdout, stderr, exitCode, timedOut, truncated := runBoundedCommand(ctx, driver.root, 2*time.Minute, "go", args...)
	result := GoTestToolResult{Args: append([]string{"go"}, args...), ExitCode: exitCode, Passed: exitCode == 0 && !timedOut, Stdout: stdout, Stderr: stderr, Truncated: truncated, TimedOut: timedOut, Duration: time.Since(started).String()}
	content := strings.TrimSpace(stdout + "\n" + stderr)
	if content == "" {
		content = fmt.Sprintf("go test exited with code %d", exitCode)
	}
	return structuredToolResult(call, content, result)
}

func packagePatternHasParentTraversal(pattern string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(pattern, "./"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

type gitDiffDriver struct{ root string }

func (driver gitDiffDriver) Definition() tool.Definition {
	additional := false
	items := tool.Schema{Type: "string"}
	return tool.Definition{Name: ToolGitDiff, Description: "Show the bounded git diff of the workspace or selected paths.", InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Properties: map[string]tool.Schema{"paths": {Type: "array", Items: &items}}}}
}

func (driver gitDiffDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Paths []string `json:"paths,omitempty"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{}, err
	}
	args := []string{"diff", "--"}
	for _, value := range input.Paths {
		value = filepath.ToSlash(strings.TrimSpace(value))
		if value == "" || filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") {
			return toolError(call, "coding.git_diff rejected: paths must stay inside the workspace"), nil
		}
		args = append(args, value)
	}
	stdout, stderr, exitCode, _, truncated := runBoundedCommand(ctx, driver.root, 30*time.Second, "git", args...)
	if exitCode != 0 {
		return toolError(call, "coding.git_diff failed: "+strings.TrimSpace(stderr)), nil
	}
	content := stdout
	if content == "" {
		content = "(no changes)"
	}
	return structuredToolResult(call, content, GitDiffToolResult{Diff: stdout, Truncated: truncated})
}

func runBoundedCommand(ctx context.Context, root string, timeout time.Duration, name string, args ...string) (stdout, stderr string, exitCode int, timedOut, truncated bool) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, name, args...)
	command.Dir = root
	var outBuffer, errBuffer bytes.Buffer
	command.Stdout = &outBuffer
	command.Stderr = &errBuffer
	err := command.Run()
	exitCode = 0
	if err != nil {
		exitCode = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
	}
	timedOut = errors.Is(commandCtx.Err(), context.DeadlineExceeded)
	stdout, stderr = outBuffer.String(), errBuffer.String()
	if len(stdout) > maxCommandOutputBytes {
		stdout = stdout[:maxCommandOutputBytes]
		truncated = true
	}
	if len(stderr) > maxCommandOutputBytes {
		stderr = stderr[:maxCommandOutputBytes]
		truncated = true
	}
	return stdout, stderr, exitCode, timedOut, truncated
}

func structuredToolResult(call tool.Call, content string, value any) (tool.Result, error) {
	structured, err := json.Marshal(value)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}, nil
}

func toolError(call tool.Call, content string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

var _ Workspace = (*localWorkspace)(nil)
