package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
	"gopkg.in/yaml.v3"
)

const MaxManagedSkillBytes = 64_000

var managedSkillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type ManagedSkillManager struct {
	root     string
	catalog  *Catalog
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	onReload func()
}

type ManagedSkillMutation struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
}

func NewManagedSkillManager(root string, catalog *Catalog) *ManagedSkillManager {
	return &ManagedSkillManager{root: filepath.Clean(root), catalog: catalog, locks: make(map[string]*sync.Mutex)}
}

func (manager *ManagedSkillManager) SetReloadCallback(callback func()) {
	if manager != nil {
		manager.onReload = callback
	}
}

func (manager *ManagedSkillManager) Driver() tool.Driver {
	if manager == nil || strings.TrimSpace(manager.root) == "" {
		return nil
	}
	return &managedSkillDriver{manager: manager}
}

func (manager *ManagedSkillManager) Mutate(input ManagedSkillMutation) (string, error) {
	name := strings.ToLower(strings.TrimSpace(input.Name))
	if !managedSkillNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid managed skill name %q; use 1-64 lowercase letters, digits, and hyphens", input.Name)
	}
	lock := manager.skillLock(name)
	lock.Lock()
	defer lock.Unlock()
	if err := manager.ensureRoot(); err != nil {
		return "", err
	}
	directory := filepath.Join(manager.root, name)
	file := filepath.Join(directory, "SKILL.md")
	switch input.Action {
	case "delete":
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("managed skill %q does not exist", name)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("managed skill %q has an unsafe directory", name)
		}
		if err := os.RemoveAll(directory); err != nil {
			return "", err
		}
		if err := manager.reload(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted managed skill %q.", name), nil
	case "create", "update":
	default:
		return "", fmt.Errorf("managed skill action must be create, update, or delete")
	}
	description := sanitizeManagedDescription(input.Description)
	body := strings.TrimSpace(input.Body)
	if description == "" || body == "" {
		return "", fmt.Errorf("%s requires non-empty description and body", input.Action)
	}
	if input.Action == "create" && manager.authoredNameClaimed(name) {
		return "", fmt.Errorf("cannot create managed skill %q: an authored skill already claims that name", name)
	}
	frontmatter, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{Name: name, Description: description})
	if err != nil {
		return "", err
	}
	content := append([]byte("---\n"), frontmatter...)
	content = append(content, []byte("---\n\n"+body+"\n")...)
	if len(content) > MaxManagedSkillBytes {
		return "", fmt.Errorf("managed skill is %d bytes; limit is %d", len(content), MaxManagedSkillBytes)
	}
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("managed skill %q has an unsafe directory", name)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if input.Action == "update" {
			return "", fmt.Errorf("managed skill %q does not exist; use create", name)
		}
		if err := os.Mkdir(directory, 0o700); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	if input.Action == "create" {
		handle, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return "", fmt.Errorf("managed skill %q already exists; use update", name)
			}
			return "", err
		}
		_, writeErr := handle.Write(content)
		closeErr := handle.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else {
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("managed skill %q does not exist; use create", name)
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("managed skill %q SKILL.md is not a regular file", name)
		}
		if err := writeManagedFileNoFollow(file, content, info); err != nil {
			return "", err
		}
	}
	if err := manager.reload(); err != nil {
		return "", err
	}
	verb := "Created"
	if input.Action == "update" {
		verb = "Updated"
	}
	return fmt.Sprintf("%s managed skill %q (managed-skills/%s/SKILL.md).", verb, name, name), nil
}

func (manager *ManagedSkillManager) ensureRoot() error {
	if info, err := os.Lstat(manager.root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("managed-skills root is not a safe directory")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(manager.root, 0o700)
}

func (manager *ManagedSkillManager) authoredNameClaimed(name string) bool {
	if manager.catalog == nil {
		return false
	}
	for _, entry := range manager.catalog.Snapshot().Entries {
		if entry.Name == name && !entry.Managed {
			return true
		}
	}
	return false
}

func (manager *ManagedSkillManager) reload() error {
	if manager.catalog == nil {
		return nil
	}
	if err := manager.catalog.Reload(); err != nil {
		return err
	}
	if manager.onReload != nil {
		manager.onReload()
	}
	return nil
}

func (manager *ManagedSkillManager) skillLock(name string) *sync.Mutex {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	lock := manager.locks[name]
	if lock == nil {
		lock = &sync.Mutex{}
		manager.locks[name] = lock
	}
	return lock
}

func sanitizeManagedDescription(value string) string {
	var output strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) || strings.ContainsRune("<>`", character) {
			output.WriteByte(' ')
			continue
		}
		output.WriteRune(character)
	}
	value = output.String()
	for strings.Contains(value, "~~") {
		value = strings.ReplaceAll(value, "~~", "~")
	}
	return strings.Join(strings.Fields(value), " ")
}

type managedSkillDriver struct{ manager *ManagedSkillManager }

func (*managedSkillDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        "manage_skill",
		Description: "Create, update, or delete an isolated managed SKILL.md under the host-managed directory. Use only for genuinely reusable procedures; authored skills are never modified.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"action", "name"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"action":      {Type: "string", Enum: []string{"create", "update", "delete"}},
			"name":        {Type: "string", Description: "kebab-case skill name"},
			"description": {Type: "string", Description: "one-line trigger description; required for create/update"},
			"body":        {Type: "string", Description: "Markdown body without frontmatter; required for create/update"},
		}},
		Concurrency: tool.ConcurrencyParallel,
	}
}

func (*managedSkillDriver) ToolPolicy() agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectWrite, RiskLevel: "medium", RequiresApproval: true,
		PolicyTags: []string{"autolearn", "managed-skill"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *managedSkillDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input ManagedSkillMutation
	decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return tool.Result{}, err
	}
	content, err := driver.manager.Mutate(input)
	if err != nil {
		return tool.Result{ToolCallID: call.ID, Name: "manage_skill", Content: err.Error(), IsError: true}, nil
	}
	details, _ := json.Marshal(map[string]string{"action": input.Action, "name": strings.ToLower(strings.TrimSpace(input.Name))})
	return tool.Result{ToolCallID: call.ID, Name: "manage_skill", Content: content, Structured: details}, nil
}
