package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateProjectDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Documents")
	project, err := createProjectDirectory(root, "我的项目")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		t.Fatalf("project directory was not created: %v", err)
	}
	if _, err := createProjectDirectory(root, "../escape"); err == nil {
		t.Fatal("path traversal project name must be rejected")
	}
	if _, err := createProjectDirectory(root, "我的项目"); err == nil {
		t.Fatal("existing project directory must not be overwritten")
	}
}
