package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func gitWorkspaceLineChanges(ctx context.Context, root string) (int, int, error) {
	diff, err := gitOutputLimited(ctx, root, 4*1024*1024, "diff", "--numstat", "HEAD", "--")
	if err != nil {
		unstaged, unstagedErr := gitOutputLimited(ctx, root, 4*1024*1024, "diff", "--numstat", "--")
		staged, stagedErr := gitOutputLimited(ctx, root, 4*1024*1024, "diff", "--cached", "--numstat", "--")
		if unstagedErr != nil || stagedErr != nil {
			return 0, 0, fmt.Errorf("read git line changes: %w", errors.Join(unstagedErr, stagedErr))
		}
		diff = append(unstaged, staged...)
	}
	additions, deletions := parseGitNumstat(diff)
	untracked, err := gitOutputLimited(ctx, root, 4*1024*1024, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return 0, 0, fmt.Errorf("list untracked files: %w", err)
	}
	for _, name := range bytes.Split(untracked, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		lines, err := gitTextLineCount(filepath.Join(root, string(name)))
		if err != nil {
			return 0, 0, fmt.Errorf("count untracked file %q: %w", name, err)
		}
		additions += lines
	}
	return additions, deletions, nil
}

func parseGitNumstat(output []byte) (int, int) {
	additions, deletions := 0, 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		added, addedErr := strconv.Atoi(fields[0])
		deleted, deletedErr := strconv.Atoi(fields[1])
		if addedErr == nil && deletedErr == nil {
			additions += added
			deletions += deleted
		}
	}
	return additions, deletions
}

func gitTextLineCount(path string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 1, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	buffer := make([]byte, 32*1024)
	lines, size, last := 0, 0, byte(0)
	for {
		read, readErr := file.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return 0, nil
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			size += read
			last = chunk[read-1]
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if size > 0 && last != '\n' {
		lines++
	}
	return lines, nil
}
