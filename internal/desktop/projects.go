package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (b *Bridge) CreateProject(name, location string, initialiseGit bool) (string, error) {
	root, err := expandProjectLocation(location)
	if err != nil {
		return "", err
	}
	project, err := createProjectDirectory(root, name)
	if err != nil {
		return "", err
	}
	if initialiseGit {
		command := exec.Command("git", "init", "-b", "main", project)
		if output, commandErr := command.CombinedOutput(); commandErr != nil {
			_ = os.Remove(project)
			return "", fmt.Errorf("initialize Git repository: %w: %s", commandErr, strings.TrimSpace(string(output)))
		}
	}
	return project, nil
}

func expandProjectLocation(location string) (string, error) {
	location = strings.TrimSpace(location)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	if location == "" {
		return filepath.Join(home, "Documents"), nil
	}
	if location == "~" {
		return home, nil
	}
	if strings.HasPrefix(location, "~/") {
		return filepath.Join(home, strings.TrimPrefix(location, "~/")), nil
	}
	absolute, err := filepath.Abs(location)
	if err != nil {
		return "", fmt.Errorf("resolve project location: %w", err)
	}
	return absolute, nil
}

func (b *Bridge) OpenProject(path string) error {
	return b.openProjectWindow(path, "", -1)
}

func (b *Bridge) OpenProjectSession(path, sessionID string, sequence int64) error {
	return b.openProjectWindow(path, strings.TrimSpace(sessionID), sequence)
}

func (b *Bridge) openProjectWindow(path, sessionID string, sequence int64) error {
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
	if sequence < -1 {
		return fmt.Errorf("search sequence is invalid")
	}
	if b.runtime != nil {
		if err := b.runtime.RememberProject(b.ctx, absolute); err != nil {
			return err
		}
	}
	return b.openProject(absolute, sessionID, sequence)
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
