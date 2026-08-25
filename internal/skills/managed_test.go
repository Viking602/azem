package skills

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/venat/tool"
)

func TestManagedSkillCreateUpdateDeleteReloadsCatalog(t *testing.T) {
	home, managed := t.TempDir(), filepath.Join(t.TempDir(), "managed-skills")
	cfg := config.Default()
	catalog, err := Load(LoadOptions{HomeDir: home, ConfigDir: filepath.Join(home, ".azem"), ManagedDir: managed, Config: cfg.Skills, Discovery: cfg.Discovery})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagedSkillManager(managed, catalog)
	created, err := manager.Mutate(ManagedSkillMutation{Action: "create", Name: "debug-recipe", Description: "Use <when> `debugging`\nrepeated failures", Body: "# Steps\n\nRun the focused reproduction."})
	if err != nil || !strings.Contains(created, "Created") {
		t.Fatalf("create = %q, %v", created, err)
	}
	entry, ok := catalog.Snapshot().Registry.Get("debug-recipe")
	if !ok || !strings.Contains(entry.Body, "focused reproduction") {
		t.Fatalf("managed registry entry = %#v, %v", entry, ok)
	}
	var managedEntry bool
	for _, item := range catalog.Snapshot().Entries {
		if item.Name == "debug-recipe" {
			managedEntry = item.Managed
		}
	}
	if !managedEntry {
		t.Fatalf("managed catalog marker is absent: %#v", catalog.Snapshot().Entries)
	}
	payload, err := os.ReadFile(filepath.Join(managed, "debug-recipe", "SKILL.md"))
	if err != nil || strings.Contains(string(payload), "<when>") || strings.Contains(string(payload), "`debugging`") || !strings.Contains(string(payload), "Use when debugging repeated failures") {
		t.Fatalf("sanitized managed skill = %q, %v", payload, err)
	}
	if _, err := manager.Mutate(ManagedSkillMutation{Action: "update", Name: "debug-recipe", Description: "Updated recipe", Body: "Updated body."}); err != nil {
		t.Fatal(err)
	}
	if entry, _ := catalog.Snapshot().Registry.Get("debug-recipe"); !strings.Contains(entry.Body, "Updated body") {
		t.Fatalf("updated entry = %#v", entry)
	}
	if _, err := manager.Mutate(ManagedSkillMutation{Action: "delete", Name: "debug-recipe"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Snapshot().Registry.Get("debug-recipe"); ok {
		t.Fatal("deleted managed skill remained registered")
	}
}

func TestManagedSkillCannotShadowAuthoredOrFollowSymlinks(t *testing.T) {
	home, managed := t.TempDir(), filepath.Join(t.TempDir(), "managed")
	authoredRoot := filepath.Join(home, ".claude", "skills")
	writeTestSkill(t, authoredRoot, "authored", "Authored", "BODY")
	cfg := config.Default()
	catalog, err := Load(LoadOptions{HomeDir: home, ConfigDir: filepath.Join(home, ".azem"), ManagedDir: managed, Config: cfg.Skills, Discovery: cfg.Discovery})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagedSkillManager(managed, catalog)
	if _, err := manager.Mutate(ManagedSkillMutation{Action: "create", Name: "authored", Description: "Shadow", Body: "No"}); err == nil {
		t.Fatal("managed skill shadowed authored skill")
	}
	outside := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "managed-symlink")
	if err := os.Symlink(outside, symlinkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	symlinkManager := NewManagedSkillManager(symlinkRoot, nil)
	if _, err := symlinkManager.Mutate(ManagedSkillMutation{Action: "create", Name: "escape", Description: "Escape", Body: "No"}); err == nil {
		t.Fatal("managed skill followed a symlinked root")
	}
}

func TestManagedSkillSerializesSameNameAndRejectsHardlinkUpdate(t *testing.T) {
	managed := filepath.Join(t.TempDir(), "managed")
	manager := NewManagedSkillManager(managed, nil)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := manager.Mutate(ManagedSkillMutation{Action: "create", Name: "parallel", Description: "Parallel", Body: "Body"})
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	successes := 0
	for err := range errorsSeen {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("parallel create successes = %d", successes)
	}
	if runtime.GOOS != "windows" {
		file := filepath.Join(managed, "parallel", "SKILL.md")
		if err := os.Link(file, filepath.Join(t.TempDir(), "linked.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Mutate(ManagedSkillMutation{Action: "update", Name: "parallel", Description: "Unsafe", Body: "Unsafe"}); err == nil {
			t.Fatal("hard-linked managed skill was updated")
		}
	}
}

func TestManagedSkillToolValidatesCrossFieldContract(t *testing.T) {
	driver := NewManagedSkillManager(filepath.Join(t.TempDir(), "managed"), nil).Driver()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "managed", Name: "manage_skill", Arguments: []byte(`{"action":"create","name":"demo"}`)}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "description and body") {
		t.Fatalf("managed tool result = %#v, %v", result, err)
	}
}
