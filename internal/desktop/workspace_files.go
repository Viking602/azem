package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxWorkspaceEntries  = 2000
	maxPreviewBytes      = 2 << 20
	maxImagePreviewBytes = 8 << 20
)

type WorkspaceEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Directory  bool   `json:"directory"`
	Symlink    bool   `json:"symlink,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
}

type WorkspaceDirectory struct {
	Path      string           `json:"path"`
	Entries   []WorkspaceEntry `json:"entries"`
	Truncated bool             `json:"truncated,omitempty"`
}

type WorkspaceFile struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Language   string `json:"language,omitempty"`
	MediaType  string `json:"mediaType,omitempty"`
	Content    string `json:"content,omitempty"`
	Size       int64  `json:"size"`
	LineCount  int    `json:"lineCount,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

func (b *Bridge) WorkspaceEntries(path string) (WorkspaceDirectory, error) {
	directory, relative, err := resolveWorkspacePath(b.workspace, path)
	if err != nil {
		return WorkspaceDirectory{}, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return WorkspaceDirectory{}, err
	}
	if !info.IsDir() {
		return WorkspaceDirectory{}, fmt.Errorf("workspace path %q is not a directory", relative)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return WorkspaceDirectory{}, err
	}
	result := WorkspaceDirectory{Path: filepath.ToSlash(relative), Entries: make([]WorkspaceEntry, 0, min(len(entries), maxWorkspaceEntries))}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".DS_Store" {
			continue
		}
		item, itemErr := workspaceEntry(b.workspace, relative, directory, entry)
		if itemErr != nil {
			continue
		}
		result.Entries = append(result.Entries, item)
	}
	result.Entries = excludeGitIgnoredEntries(b.workspace, result.Entries)
	sort.SliceStable(result.Entries, func(i, j int) bool {
		if result.Entries[i].Directory != result.Entries[j].Directory {
			return result.Entries[i].Directory
		}
		return strings.ToLower(result.Entries[i].Name) < strings.ToLower(result.Entries[j].Name)
	})
	if len(result.Entries) > maxWorkspaceEntries {
		result.Entries = result.Entries[:maxWorkspaceEntries]
		result.Truncated = true
	}
	return result, nil
}

// excludeGitIgnoredEntries keeps generated outputs and local tool state out of
// the source tree without weakening the read boundary. Git is optional: a
// missing executable, a non-repository workspace, or a timeout falls back to
// the complete directory listing.
func excludeGitIgnoredEntries(root string, entries []WorkspaceEntry) []WorkspaceEntry {
	if len(entries) == 0 {
		return entries
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--stdin", "-z")
	var input strings.Builder
	for _, entry := range entries {
		input.WriteString(filepath.FromSlash(entry.Path))
		input.WriteByte(0)
	}
	command.Stdin = strings.NewReader(input.String())
	output, err := command.Output()
	if err != nil && len(output) == 0 {
		return entries
	}
	ignored := make(map[string]struct{})
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) > 0 {
			ignored[filepath.ToSlash(string(path))] = struct{}{}
		}
	}
	if len(ignored) == 0 {
		return entries
	}
	visible := make([]WorkspaceEntry, 0, len(entries)-len(ignored))
	for _, entry := range entries {
		if _, ok := ignored[entry.Path]; !ok {
			visible = append(visible, entry)
		}
	}
	return visible
}

func (b *Bridge) WorkspaceFile(path string) (WorkspaceFile, error) {
	resolved, relative, err := resolveWorkspacePath(b.workspace, path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if !info.Mode().IsRegular() {
		return WorkspaceFile{}, fmt.Errorf("workspace path %q is not a regular file", relative)
	}
	result := WorkspaceFile{Path: filepath.ToSlash(relative), Name: filepath.Base(relative), Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(timeLayout)}
	if mediaType := previewImageMediaType(resolved); mediaType != "" {
		return readWorkspaceImage(resolved, mediaType, result)
	}
	return readWorkspaceText(resolved, result)
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func workspaceEntry(root, parent, directory string, entry os.DirEntry) (WorkspaceEntry, error) {
	relative := filepath.Join(parent, entry.Name())
	resolved, _, err := resolveWorkspacePath(root, relative)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	return WorkspaceEntry{
		Name: entry.Name(), Path: filepath.ToSlash(relative), Directory: info.IsDir(), Symlink: entry.Type()&os.ModeSymlink != 0,
		Hidden: strings.HasPrefix(entry.Name(), "."), Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(timeLayout),
	}, nil
}

func resolveWorkspacePath(root, requested string) (string, string, error) {
	if strings.ContainsRune(requested, 0) || filepath.IsAbs(requested) {
		return "", "", errors.New("workspace path must be relative")
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	relative := filepath.Clean(filepath.FromSlash(requested))
	if relative == "." {
		relative = ""
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("workspace path escapes the workspace root")
	}
	candidate := filepath.Join(rootResolved, relative)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", err
	}
	inside, err := filepath.Rel(rootResolved, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", "", errors.New("workspace path escapes the workspace root")
	}
	return resolved, relative, nil
}

func readWorkspaceText(path string, result WorkspaceFile) (WorkspaceFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxPreviewBytes+1))
	if err != nil {
		return WorkspaceFile{}, err
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		result.Kind = "binary"
		return result, nil
	}
	if len(content) > maxPreviewBytes {
		content = content[:maxPreviewBytes]
		result.Truncated = true
	}
	result.Kind = "text"
	result.Language = languageForPath(path)
	result.Content = strings.TrimPrefix(string(content), "\ufeff")
	result.LineCount = strings.Count(result.Content, "\n")
	if result.Content != "" && !strings.HasSuffix(result.Content, "\n") {
		result.LineCount++
	}
	return result, nil
}

func readWorkspaceImage(path, mediaType string, result WorkspaceFile) (WorkspaceFile, error) {
	if result.Size > maxImagePreviewBytes {
		result.Kind = "binary"
		return result, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	result.Kind, result.MediaType = "image", mediaType
	result.Content = base64.StdEncoding.EncodeToString(content)
	return result, nil
}

func previewImageMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func languageForPath(path string) string {
	name := strings.ToLower(filepath.Base(path))
	if language := map[string]string{
		"go.mod": "go-module", "go.sum": "go-module", "go.work": "go-module", "go.work.sum": "go-module",
		"makefile": "makefile", "gnumakefile": "makefile", "dockerfile": "dockerfile",
		".gitignore": "gitignore", ".gitattributes": "gitattributes",
	}[name]; language != "" {
		return language
	}
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if extension != "" {
		if mediaType := mime.TypeByExtension("." + extension); strings.HasPrefix(mediaType, "text/") {
			return extension
		}
	}
	return map[string]string{
		"go": "go", "rs": "rust", "py": "python", "js": "javascript", "jsx": "javascript", "ts": "typescript", "tsx": "typescript",
		"json": "json", "yaml": "yaml", "yml": "yaml", "toml": "toml", "md": "markdown", "css": "css", "scss": "scss",
		"html": "html", "xml": "xml", "sh": "shell", "bash": "shell", "zsh": "shell", "sql": "sql", "proto": "protobuf",
	}[extension]
}
