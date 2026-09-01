package desktop

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// SearchWorkspaceFiles returns names, never contents. File reads still go
// through WorkspaceFile or the governed agent tools after an explicit choice.
func (b *Bridge) SearchWorkspaceFiles(query string, limit int) (WorkspaceDirectory, error) {
	if len(query) > 1024 || strings.ContainsRune(query, 0) || !utf8.ValidString(query) {
		return WorkspaceDirectory{}, errors.New("invalid workspace file query")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	parent := b.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	paths, truncated, err := workspaceSearchPaths(ctx, b.workspace)
	if err != nil {
		return WorkspaceDirectory{}, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	matched := paths[:0]
	for _, path := range paths {
		if strings.Contains(strings.ToLower(path), query) && utf8.ValidString(path) {
			matched = append(matched, path)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		left, right := workspaceMatchRank(matched[i], query), workspaceMatchRank(matched[j], query)
		if left != right {
			return left < right
		}
		return matched[i] < matched[j]
	})
	result := WorkspaceDirectory{Entries: make([]WorkspaceEntry, 0, limit), Truncated: truncated}
	seen := make(map[string]bool)
	for _, path := range matched {
		if ctx.Err() != nil {
			return WorkspaceDirectory{}, ctx.Err()
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		resolved, relative, err := resolveWorkspacePath(b.workspace, path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if len(result.Entries) == limit {
			result.Truncated = true
			break
		}
		result.Entries = append(result.Entries, WorkspaceEntry{
			Name: filepath.Base(relative), Path: filepath.ToSlash(relative), Size: info.Size(),
		})
	}
	return result, nil
}

func workspaceMatchRank(path, query string) int {
	name := strings.ToLower(filepath.Base(path))
	if name == query {
		return 0
	}
	if strings.HasPrefix(name, query) {
		return 1
	}
	return 2
}

func workspaceSearchPaths(ctx context.Context, root string) ([]string, bool, error) {
	// ponytail: one bounded scan per debounced query; add an index only if large
	// workspaces outgrow this 4 MiB / 50,000-entry interactive search budget.
	const maxBytes, maxEntries = 4 << 20, 50_000
	if isGitWorktree(ctx, root) {
		output, truncated, err := runGitBounded(ctx, root, maxBytes, "ls-files", "-co", "--exclude-standard", "-z", "--")
		if err != nil {
			return nil, false, err
		}
		// A bounded capture may end in the middle of a filename.
		output = output[:bytes.LastIndexByte(output, 0)+1]
		paths := strings.Split(string(output), "\x00")
		return paths[:len(paths)-1], truncated, nil
	}
	paths := make([]string, 0)
	count, total := 0, 0
	truncated := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		count++
		if count > maxEntries || total > maxBytes {
			truncated = true
			return fs.SkipAll
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		if entry.IsDir() || entry.Name() == ".DS_Store" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		total += len(relative)
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	return paths, truncated, err
}
