package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
)

func TestDesktopPluginBootstrapDoesNotLaunchCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable test fixture uses a POSIX script")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "called")
	script := "#!/bin/sh\n/usr/bin/touch " + marker + "\nprintf '{\"installed\":[]}'\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("AZEM_FAKE_PROVIDER", "")
	assembly := bootstrapAssembly{
		ctx: context.Background(), cfg: config.Default(), homeDir: root,
		paths: config.Paths{DataDir: filepath.Join(root, "data"), Workspace: filepath.Join(root, "workspace")},
	}
	if err := assembly.loadPlugins(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("desktop startup launched the Codex plugin catalog subprocess")
	}
}

func TestDesktopBootstrapRestoresLastProjectWithoutChangingConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AZEM_HOME", root)
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
