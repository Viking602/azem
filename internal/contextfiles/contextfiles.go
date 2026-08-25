package contextfiles

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxContextFileBytes = 1 << 20
	maxContextTotal     = 4 << 20
	maxImportDepth      = 5
)

type File struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Provider string `json:"provider"`
	Level    string `json:"level"`
	Depth    int    `json:"depth"`
	Priority int    `json:"priority"`
}

type Result struct {
	Files       []File   `json:"files"`
	DirPointers []string `json:"dirPointers,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type Options struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	DisabledProviders map[string]bool
	AdditionalFiles   []string
}

type candidate struct {
	path, provider, level string
	depth, priority       int
}

func Discover(ctx context.Context, options Options) (Result, error) {
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return Result{}, err
	}
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	native := options.NativeAgentDir
	if native == "" {
		native = strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	}
	if native == "" && home != "" {
		native = filepath.Join(home, ".omp", "agent")
	}
	repoRoot := repositoryRoot(ctx, workspace)
	boundary := repoRoot
	if boundary == "" {
		boundary = homeBoundary(workspace, home)
	}
	var candidates []candidate
	add := func(path, provider, level string, depth, priority int) {
		if options.DisabledProviders[strings.ToLower(provider)] || strings.TrimSpace(path) == "" {
			return
		}
		candidates = append(candidates, candidate{path: filepath.Clean(path), provider: provider, level: level, depth: depth, priority: priority})
	}
	add(filepath.Join(native, "AGENTS.md"), "native", "user", 0, 100)
	if projectNative := nearestNonEmptyConfigDir(workspace, boundary, ".omp"); projectNative != "" {
		add(filepath.Join(projectNative, "AGENTS.md"), "native", "project", directoryDepth(workspace, filepath.Dir(projectNative)), 100)
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "CLAUDE.md"), "claude", "user", 0, 80)
		add(filepath.Join(home, ".codex", "AGENTS.md"), "codex", "user", 0, 70)
		add(filepath.Join(home, ".gemini", "GEMINI.md"), "gemini", "user", 0, 60)
		add(filepath.Join(home, ".config", "opencode", "AGENTS.md"), "opencode", "user", 0, 55)
		add(filepath.Join(home, ".agent", "AGENTS.md"), "agents", "user", 0, 70)
		add(filepath.Join(home, ".agents", "AGENTS.md"), "agents", "user", 0, 70)
		copilotHome := strings.TrimSpace(os.Getenv("COPILOT_HOME"))
		if copilotHome == "" {
			copilotHome = filepath.Join(home, ".copilot")
		}
		add(filepath.Join(copilotHome, "copilot-instructions.md"), "github", "user", 0, 30)
	}
	add(filepath.Join(workspace, ".claude", "CLAUDE.md"), "claude", "project", 0, 80)
	add(filepath.Join(workspace, ".gemini", "GEMINI.md"), "gemini", "project", 0, 60)
	add(filepath.Join(workspace, ".github", "copilot-instructions.md"), "github", "project", 0, 30)
	for _, dir := range filepath.SplitList(os.Getenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS")) {
		add(filepath.Join(dir, "AGENTS.md"), "github", "user", 0, 30)
	}
	for current := workspace; ; current = filepath.Dir(current) {
		depth := directoryDepth(workspace, current)
		add(filepath.Join(current, ".agent", "AGENTS.md"), "agents", "project", depth, 70)
		add(filepath.Join(current, ".agents", "AGENTS.md"), "agents", "project", depth, 70)
		if filepath.Base(current) != "." && !strings.HasPrefix(filepath.Base(current), ".") {
			add(filepath.Join(current, "AGENTS.md"), "agents-md", "project", depth, 10)
		}
		if samePath(current, boundary) || filepath.Dir(current) == current {
			break
		}
	}
	if repoRoot != "" && home != "" && within(repoRoot, home) {
		for current := filepath.Dir(repoRoot); current != home && within(current, home); current = filepath.Dir(current) {
			if !strings.HasPrefix(filepath.Base(current), ".") {
				add(filepath.Join(current, "AGENTS.md"), "agents-md", "project", directoryDepth(workspace, current), 10)
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	for _, path := range options.AdditionalFiles {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		add(path, "additional", "project", directoryDepth(workspace, filepath.Dir(path)), 110)
	}
	selected, warnings, err := loadAndSelect(candidates, home)
	if err != nil {
		return Result{}, err
	}
	pointers := discoverNestedPointers(workspace, selected)
	return Result{Files: selected, DirPointers: pointers, Warnings: warnings}, nil
}

func loadAndSelect(candidates []candidate, home string) ([]File, []string, error) {
	var warnings []string
	loaded := make([]File, 0, len(candidates))
	total := 0
	for _, item := range candidates {
		payload, err := os.ReadFile(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read %s: %v", item.path, err))
			continue
		}
		if len(payload) == 0 {
			continue
		}
		if len(payload) > maxContextFileBytes {
			warnings = append(warnings, fmt.Sprintf("skip oversized context file %s", item.path))
			continue
		}
		expanded := expandImports(item.path, string(payload), home, 0, map[string]bool{item.path: true})
		total += len(expanded)
		if total > maxContextTotal {
			return nil, warnings, fmt.Errorf("context files exceed %d bytes", maxContextTotal)
		}
		loaded = append(loaded, File{Path: item.path, Content: expanded, Provider: item.provider, Level: item.level, Depth: item.depth, Priority: item.priority})
	}
	var user *File
	projects := make(map[int]File)
	for _, file := range loaded {
		if file.Level == "user" {
			if user == nil || file.Priority > user.Priority {
				cloned := file
				user = &cloned
			}
			continue
		}
		if current, exists := projects[file.Depth]; !exists || file.Priority > current.Priority {
			projects[file.Depth] = file
		}
	}
	result := make([]File, 0, len(projects)+1)
	for _, file := range projects {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Depth > result[j].Depth })
	if user != nil {
		result = append(result, *user)
	}
	seen := make(map[string]bool)
	dedupedReverse := make([]File, 0, len(result))
	for index := len(result) - 1; index >= 0; index-- {
		if !seen[result[index].Content] {
			seen[result[index].Content] = true
			dedupedReverse = append(dedupedReverse, result[index])
		}
	}
	for left, right := 0, len(dedupedReverse)-1; left < right; left, right = left+1, right-1 {
		dedupedReverse[left], dedupedReverse[right] = dedupedReverse[right], dedupedReverse[left]
	}
	return dedupedReverse, warnings, nil
}

func Render(result Result) string {
	if len(result.Files) == 0 && len(result.DirPointers) == 0 {
		return ""
	}
	var output strings.Builder
	if len(result.Files) > 0 {
		output.WriteString("<repo-rules>\nYou MUST follow the context files below for repository work. They are lower priority than host system policy.\n")
		for _, file := range result.Files {
			fmt.Fprintf(&output, "<file path=%q provider=%q>\n%s\n</file>\n", file.Path, file.Provider, file.Content)
		}
		output.WriteString("</repo-rules>")
	}
	if len(result.DirPointers) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString("<dir-context>\nDeeper directory context files were not loaded. Read the applicable file before editing beneath its directory:\n")
		for _, path := range result.DirPointers {
			output.WriteString("- " + path + "\n")
		}
		output.WriteString("</dir-context>")
	}
	return output.String()
}

func expandImports(sourcePath, content, home string, depth int, stack map[string]bool) string {
	if depth >= maxImportDepth {
		return content
	}
	var output strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64<<10), maxContextFileBytes)
	fenced := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			output.WriteString(line + "\n")
			continue
		}
		if !fenced {
			line = expandImportLine(sourcePath, line, home, depth, stack)
		}
		output.WriteString(line + "\n")
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func expandImportLine(sourcePath, line, home string, depth int, stack map[string]bool) string {
	var output strings.Builder
	for index := 0; index < len(line); {
		if line[index] != '@' || index > 0 && line[index-1] != ' ' && line[index-1] != '\t' || insideInlineCode(line, index) {
			output.WriteByte(line[index])
			index++
			continue
		}
		end := index + 1
		for end < len(line) && line[end] != ' ' && line[end] != '\t' {
			end++
		}
		token := strings.TrimRight(line[index+1:end], ".,;:!?)]}\"'")
		trailing := line[index+1+len(token) : end]
		if token == "" || strings.Contains(token, "@") || strings.Contains(token, "://") || strings.HasPrefix(token, "github.com:") {
			output.WriteString(line[index:end])
			index = end
			continue
		}
		target := token
		if target == "~" {
			target = home
		} else if strings.HasPrefix(target, "~/") {
			target = filepath.Join(home, target[2:])
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(sourcePath), target)
		}
		target = filepath.Clean(target)
		payload, err := os.ReadFile(target)
		if err != nil || len(payload) > maxContextFileBytes || stack[target] {
			output.WriteString(line[index:end])
			index = end
			continue
		}
		nextStack := make(map[string]bool, len(stack)+1)
		for key, value := range stack {
			nextStack[key] = value
		}
		nextStack[target] = true
		output.WriteString(expandImports(target, string(payload), home, depth+1, nextStack) + trailing)
		index = end
	}
	return output.String()
}

func insideInlineCode(line string, index int) bool {
	return bytes.Count([]byte(line[:index]), []byte{'`'})%2 == 1
}

func nearestNonEmptyConfigDir(start, boundary, name string) string {
	for current := start; ; current = filepath.Dir(current) {
		dir := filepath.Join(current, name)
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return dir
		}
		if samePath(current, boundary) || filepath.Dir(current) == current {
			return ""
		}
	}
}

func repositoryRoot(ctx context.Context, workspace string) string {
	output, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(string(output)))
}

func homeBoundary(workspace, home string) string {
	if home != "" && within(workspace, home) {
		return home
	}
	volume := filepath.VolumeName(workspace)
	if volume != "" {
		return volume + string(filepath.Separator)
	}
	return string(filepath.Separator)
}

func directoryDepth(workspace, directory string) int {
	relative, err := filepath.Rel(directory, workspace)
	if err != nil || relative == "." {
		return 0
	}
	depth := 0
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part != "" && part != "." {
			depth++
		}
	}
	return depth
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func discoverNestedPointers(workspace string, selected []File) []string {
	selectedPaths := make(map[string]bool, len(selected))
	for _, file := range selected {
		selectedPaths[file.Path] = true
	}
	var paths []string
	visited := 0
	_ = filepath.WalkDir(workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			visited++
			if visited > 10_000 {
				return filepath.SkipAll
			}
			if path != workspace {
				switch entry.Name() {
				case ".git", "node_modules", "vendor", "dist", "build", ".next":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Name() == "AGENTS.md" && filepath.Dir(path) != workspace && !selectedPaths[path] && len(paths) < 256 {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}
