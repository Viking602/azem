package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestPinnedVenatModuleVersion(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path is unavailable")
	}
	goModPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "go.mod")
	content, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := modfile.Parse(goModPath, content, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range parsed.Replace {
		if replacement.Old.Path == "github.com/Viking602/venat" {
			t.Fatalf("Venat module has a local replacement: %s", replacement.New.Path)
		}
	}
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path != "github.com/Viking602/venat" {
			continue
		}
		if requirement.Indirect {
			t.Fatal("Venat module must remain a direct dependency")
		}
		if requirement.Mod.Version != "v0.16.1" {
			t.Fatalf("Venat module version = %q, want %q", requirement.Mod.Version, "v0.16.1")
		}
		return
	}
	t.Fatal("Venat module is absent from go.mod")
}
