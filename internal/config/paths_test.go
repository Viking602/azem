package config

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesDotConfigForDefaultConfig(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, ".config", "azem")
	if want := filepath.Join(directory, "config.yaml"); paths.ConfigFile != want {
		t.Fatalf("config file = %q, want %q", paths.ConfigFile, want)
	}
	if want := filepath.Join(directory, "azem.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
}

func TestResolvePathsHonorsXDGConfigHome(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "azem", "config.yaml"); paths.ConfigFile != want {
		t.Fatalf("config file = %q, want %q", paths.ConfigFile, want)
	}
	if want := filepath.Join(root, "azem", "azem.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
}
