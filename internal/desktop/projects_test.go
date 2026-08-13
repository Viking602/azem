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

func TestExpandProjectLocation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		location string
		want     string
	}{
		{name: "default", want: filepath.Join(home, "Documents")},
		{name: "home", location: "~", want: home},
		{name: "home child", location: "~/Projects", want: filepath.Join(home, "Projects")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := expandProjectLocation(test.location)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("expandProjectLocation(%q) = %q, want %q", test.location, got, test.want)
			}
		})
	}
}
