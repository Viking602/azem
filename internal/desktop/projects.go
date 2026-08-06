package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (b *Bridge) CreateProject(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return createProjectDirectory(filepath.Join(home, "Documents"), name)
}

func (b *Bridge) OpenProject(path string) error {
	return b.openProjectWindow(path, "")
}

func (b *Bridge) OpenProjectSession(path, sessionID string) error {
	return b.openProjectWindow(path, strings.TrimSpace(sessionID))
}

func (b *Bridge) openProjectWindow(path, sessionID string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("project path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("open project directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project path is not a directory")
	}
	if b.openProject == nil {
		return fmt.Errorf("project window is unavailable")
	}
	if b.runtime != nil {
		if err := b.runtime.RememberProject(b.ctx, absolute); err != nil {
			return err
		}
	}
	return b.openProject(absolute, sessionID)
}

func createProjectDirectory(root, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, `<>:"/\\|?*`) {
		return "", fmt.Errorf("invalid project name")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create Documents directory: %w", err)
	}
	target := filepath.Join(root, name)
	if err := os.Mkdir(target, 0o755); err != nil {
		return "", fmt.Errorf("create project directory: %w", err)
	}
	return target, nil
}
