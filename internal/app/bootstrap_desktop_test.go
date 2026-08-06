package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDesktopBootstrapRestoresLastProjectWithoutChangingConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("AZEM_FAKE_PROVIDER", "1")
	configFile := filepath.Join(root, "config.yaml")
	const contents = "version: 1\nauth:\n  store: file\n  import_codex: false\n  import_grok: false\nmcp:\n  servers: {}\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	first, second, fallback := t.TempDir(), t.TempDir(), t.TempDir()
	ctx := context.Background()

	for _, workspace := range []string{first, second} {
		boot, err := BootstrapDesktopAtWorkspace(ctx, workspace, configFile)
		if err != nil {
			t.Fatal(err)
		}
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := boot.Service.Shutdown(shutdownCtx); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
	}

	boot, err := BootstrapDesktop(ctx, fallback, configFile)
	if err != nil {
		t.Fatal(err)
	}
	defer boot.Service.Shutdown(ctx)
	second, _ = filepath.EvalSymlinks(second)
	if boot.Paths.Workspace != second {
		t.Fatalf("restored workspace = %q, want %q", boot.Paths.Workspace, second)
	}
	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("desktop bootstrap changed config:\n%s", got)
	}
}
