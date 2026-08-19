package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePathsUsesDotAzemHome(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, ".azem")
	if want := filepath.Join(directory, "config.yaml"); paths.ConfigFile != want {
		t.Fatalf("config file = %q, want %q", paths.ConfigFile, want)
	}
	if paths.DataDir != directory || paths.StateDir != directory {
		t.Fatalf("data/state = %q %q, want %q", paths.DataDir, paths.StateDir, directory)
	}
	if want := filepath.Join(directory, "azem.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
}

func TestResolvePathsHonorsAzemHome(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	t.Setenv(HomeEnv, root)
	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "config.yaml"); paths.ConfigFile != want {
		t.Fatalf("config file = %q, want %q", paths.ConfigFile, want)
	}
	if want := filepath.Join(root, "azem.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
	if paths.DataDir != root || paths.StateDir != root {
		t.Fatalf("data/state = %q %q, want %q", paths.DataDir, paths.StateDir, root)
	}
}

func TestLegacyHomeRootsCoverPreviousPlatformLocations(t *testing.T) {
	if got := legacyHomeRoots("darwin", "/Users/user", "/Users/user/.azem"); !containsPath(got, filepath.Join("/Users/user", "Library", "Application Support", "azem")) {
		t.Fatalf("darwin roots = %#v", got)
	}
	if got := legacyHomeRoots("windows", `C:\Users\user`, `C:\Users\user\.azem`); !containsPath(got, filepath.Join(`C:\Users\user`, "AppData", "Roaming", "azem")) {
		t.Fatalf("windows roots = %#v", got)
	}
	if got := legacyHomeRoots("linux", "/home/user", "/home/user/.azem"); !containsPath(got, filepath.Join("/home/user", ".local", "share", "azem")) {
		t.Fatalf("linux roots = %#v", got)
	}
}

func TestMigrateLegacyHomesIntoDotAzem(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	dest := defaultHome(home)
	roots := legacyHomeRoots(runtime.GOOS, home, dest)
	if len(roots) < 2 {
		t.Fatalf("legacy roots = %#v", roots)
	}
	writeFile(t, filepath.Join(roots[0], "config.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(roots[0], "azem.db"), "db")
	writeFile(t, filepath.Join(roots[0], "azem.db-wal"), "wal")
	writeFile(t, filepath.Join(roots[0], "skills", "demo", "SKILL.md"), "skill")
	writeFile(t, filepath.Join(roots[1], "blobs", "aa", "hash"), "blob")
	writeFile(t, filepath.Join(roots[1], "plugin-packages", "local", "demo", "plugin.json"), "{}")
	if len(roots) > 2 {
		writeFile(t, filepath.Join(roots[2], "window.json"), "{}")
		writeFile(t, filepath.Join(roots[2], "credentials.json"), "{}")
	}
	writeFile(t, filepath.Join(dest, "skills", "keep", "SKILL.md"), "keep")

	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Database != filepath.Join(dest, "azem.db") {
		t.Fatalf("database = %q", paths.Database)
	}
	assertFile(t, filepath.Join(dest, "config.yaml"), "version: 1\n")
	assertFile(t, filepath.Join(dest, "azem.db"), "db")
	assertFile(t, filepath.Join(dest, "azem.db-wal"), "wal")
	assertFile(t, filepath.Join(dest, "blobs", "aa", "hash"), "blob")
	assertFile(t, filepath.Join(dest, "plugin-packages", "local", "demo", "plugin.json"), "{}")
	assertFile(t, filepath.Join(dest, "skills", "demo", "SKILL.md"), "skill")
	assertFile(t, filepath.Join(dest, "skills", "keep", "SKILL.md"), "keep")
	if len(roots) > 2 {
		assertFile(t, filepath.Join(dest, "window.json"), "{}")
		assertFile(t, filepath.Join(dest, "credentials.json"), "{}")
	}
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, "azem.db")); !os.IsNotExist(err) {
			t.Fatalf("legacy database still at %q: %v", root, err)
		}
		moved, err := os.ReadFile(filepath.Join(root, movedToName))
		if err != nil {
			t.Fatalf("MOVED_TO in %q: %v", root, err)
		}
		if string(moved) != "Moved to "+dest+"\n" {
			t.Fatalf("MOVED_TO in %q = %q", root, moved)
		}
	}
}

func TestMigrateSkipsWhenAzemHomeOverride(t *testing.T) {
	home := t.TempDir()
	override := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, override)
	legacy := filepath.Join(home, ".config", "azem")
	writeFile(t, filepath.Join(legacy, "azem.db"), "legacy")
	writeFile(t, filepath.Join(legacy, "config.yaml"), "version: 1\n")

	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Database != filepath.Join(override, "azem.db") {
		t.Fatalf("database = %q", paths.Database)
	}
	assertFile(t, filepath.Join(legacy, "azem.db"), "legacy")
	if _, err := os.Stat(paths.Database); !os.IsNotExist(err) {
		t.Fatalf("override home should not receive a migrated database: %v", err)
	}
}

func TestMigrateSkipsWhenAzemHomeExplicitlyMatchesDefault(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, defaultHome(home))
	legacy := filepath.Join(home, ".config", "azem")
	writeFile(t, filepath.Join(legacy, "azem.db"), "legacy")

	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Database != filepath.Join(defaultHome(home), "azem.db") {
		t.Fatalf("database = %q", paths.Database)
	}
	assertFile(t, filepath.Join(legacy, "azem.db"), "legacy")
	if _, err := os.Stat(paths.Database); !os.IsNotExist(err) {
		t.Fatalf("explicit default home should not receive a migrated database: %v", err)
	}
}

func TestMigrateCustomXDGHomesAndRemapsExplicitConfig(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	configRoot := t.TempDir()
	dataRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_DATA_HOME", dataRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	legacyConfig := filepath.Join(configRoot, "azem", "config.yaml")
	writeFile(t, legacyConfig, "version: 1\n")
	writeFile(t, filepath.Join(dataRoot, "azem", "plugin-packages", "local", "demo", "plugin.json"), "{}")
	writeFile(t, filepath.Join(stateRoot, "azem", "credentials.json"), "credentials")

	paths, err := ResolvePathsWithConfig(workspace, legacyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigFile != filepath.Join(defaultHome(home), "config.yaml") {
		t.Fatalf("config file = %q", paths.ConfigFile)
	}
	assertFile(t, paths.ConfigFile, "version: 1\n")
	assertFile(t, filepath.Join(paths.DataDir, "plugin-packages", "local", "demo", "plugin.json"), "{}")
	assertFile(t, filepath.Join(paths.StateDir, "credentials.json"), "credentials")
}

func TestMigrateStandaloneAgentProfiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	legacy := filepath.Join(home, ".config", "azem")
	writeFile(t, filepath.Join(legacy, "agents", "reviewer.md"), "profile")

	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(paths.ConfigDir, "agents", "reviewer.md"), "profile")
}

func TestMigrateFailsOnExistingCollision(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	dest := defaultHome(home)
	writeFile(t, filepath.Join(dest, "config.yaml"), "new\n")
	writeFile(t, filepath.Join(home, ".config", "azem", "config.yaml"), "old\n")
	writeFile(t, filepath.Join(home, ".config", "azem", "azem.db"), "db")

	if _, err := ResolvePaths(workspace); err == nil {
		t.Fatal("colliding config.yaml was accepted")
	}
	assertFile(t, filepath.Join(home, ".config", "azem", "azem.db"), "db")
}

func TestMigrateKeepsExistingHookTranscriptsAndMovesDatabase(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	dest := defaultHome(home)
	writeFile(t, filepath.Join(dest, "hook-transcripts", "same.jsonl"), "new")
	writeFile(t, filepath.Join(home, ".config", "azem", "config.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(home, ".config", "azem", "azem.db"), "db")
	roots := legacyHomeRoots(runtime.GOOS, home, dest)
	cache := roots[len(roots)-1]
	writeFile(t, filepath.Join(cache, "hook-transcripts", "same.jsonl"), "old")
	writeFile(t, filepath.Join(cache, "hook-transcripts", "only-old.jsonl"), "cache")
	writeFile(t, filepath.Join(cache, "window.json"), "{}")

	paths, err := ResolvePaths(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Database != filepath.Join(dest, "azem.db") {
		t.Fatalf("database = %q", paths.Database)
	}
	assertFile(t, filepath.Join(dest, "azem.db"), "db")
	assertFile(t, filepath.Join(dest, "config.yaml"), "version: 1\n")
	assertFile(t, filepath.Join(dest, "hook-transcripts", "same.jsonl"), "new")
	assertFile(t, filepath.Join(dest, "hook-transcripts", "only-old.jsonl"), "cache")
	assertFile(t, filepath.Join(dest, "window.json"), "{}")
}

func TestMigrateAdoptsLeftoverBlobsWhenHomeAlreadyHasDatabase(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	dest := defaultHome(home)
	writeFile(t, filepath.Join(dest, "azem.db"), "current")
	writeFile(t, filepath.Join(dest, "config.yaml"), "version: 1\n")
	roots := legacyHomeRoots(runtime.GOOS, home, dest)
	data := roots[1]
	writeFile(t, filepath.Join(data, "azem.db"), "stale")
	writeFile(t, filepath.Join(data, "blobs", "aa", "hash"), "blob")
	writeFile(t, filepath.Join(data, "plugin-packages", "local", "demo", "plugin.json"), "{}")

	if _, err := ResolvePaths(workspace); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dest, "azem.db"), "current")
	assertFile(t, filepath.Join(dest, "blobs", "aa", "hash"), "blob")
	assertFile(t, filepath.Join(dest, "plugin-packages", "local", "demo", "plugin.json"), "{}")
	assertFile(t, filepath.Join(data, "azem.db"), "stale")
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func containsPath(paths []string, want string) bool {
	want = filepath.Clean(want)
	for _, path := range paths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}
