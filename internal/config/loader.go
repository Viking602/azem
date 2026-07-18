package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// UpdateDefault atomically updates one persisted UI default while preserving
// unrelated YAML fields and comments in the user's configuration file.
func UpdateDefault(path, key, value string) error {
	if key != "language" && key != "approval_mode" {
		return fmt.Errorf("unsupported default %q", key)
	}
	var document yaml.Node
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	} else if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode config for update: %w", err)
	}
	root := document.Content[0]
	defaults := mappingValue(root, "defaults")
	if defaults == nil {
		defaults = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "defaults"}, defaults)
	}
	setMappingScalar(defaults, key, value)

	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode config update: %w", err)
	}
	_ = encoder.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create config update: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(encoded.Bytes())
	}
	closeErr := temporary.Close()
	if err != nil {
		return fmt.Errorf("write config update: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close config update: %w", closeErr)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setMappingScalar(mapping *yaml.Node, key, value string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1].Kind = yaml.ScalarNode
			mapping.Content[index+1].Tag = "!!str"
			mapping.Content[index+1].Value = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func Load(path string, startupWorkspace string) (Config, error) {
	cfg := Default()
	if path == "" {
		paths, err := ResolvePaths(startupWorkspace)
		if err != nil {
			return Config{}, err
		}
		path = paths.ConfigFile
	}
	configDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve config directory: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
	} else {
		defer f.Close()
		decoder := yaml.NewDecoder(io.LimitReader(f, 1<<20))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode config %q: %w", path, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return Config{}, fmt.Errorf("decode config %q: multiple YAML documents are not allowed", path)
			}
			return Config{}, fmt.Errorf("decode config %q: %w", path, err)
		}
	}
	for i, directory := range cfg.Skills.AdditionalDirs {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(configDir, directory)
		}
		cfg.Skills.AdditionalDirs[i] = filepath.Clean(directory)
	}
	for i, hookPath := range cfg.Hooks.AdditionalPaths {
		if !filepath.IsAbs(hookPath) {
			hookPath = filepath.Join(configDir, hookPath)
		}
		cfg.Hooks.AdditionalPaths[i] = filepath.Clean(hookPath)
	}
	explicitRoles := make(map[string]bool)
	for name, role := range cfg.Agents.Subagents.Roles {
		if role.Source == "" {
			explicitRoles[name] = true
		}
	}
	explicitPersonas := make(map[string]bool)
	for name, persona := range cfg.Agents.Subagents.Personas {
		if persona.Source == "" {
			explicitPersonas[name] = true
		}
	}

	if cfg.Workspace.Root == "" {
		cfg.Workspace.Root = startupWorkspace
	} else if !filepath.IsAbs(cfg.Workspace.Root) {
		cfg.Workspace.Root = filepath.Join(filepath.Dir(path), cfg.Workspace.Root)
	}
	root, err := canonicalDirectory(cfg.Workspace.Root)
	if err != nil {
		return Config{}, fmt.Errorf("workspace root: %w", err)
	}
	cfg.Workspace.Root = root
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve user home for subagent profiles: %w", err)
	}
	if err := discoverSubagentProfiles(&cfg, root, homeDir, explicitRoles, explicitPersonas); err != nil {
		return Config{}, err
	}
	if err := resolveSubagentInstructionFiles(&cfg, filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	for name := range explicitRoles {
		role := cfg.Agents.Subagents.Roles[name]
		role.Source = "config:" + path
		cfg.Agents.Subagents.Roles[name] = role
	}
	for name := range explicitPersonas {
		persona := cfg.Agents.Subagents.Personas[name]
		persona.Source = "config:" + path
		cfg.Agents.Subagents.Personas[name] = persona
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolveSubagentInstructionFiles(cfg *Config, baseDir string) error {
	for name, role := range cfg.Agents.Subagents.Roles {
		if strings.TrimSpace(role.InstructionsFile) == "" {
			continue
		}
		if strings.TrimSpace(role.Instructions) != "" {
			if role.Source != "" && role.Source != "builtin" {
				continue
			}
			return fmt.Errorf("agents.subagents role %q sets both instructions and instructions_file", name)
		}
		instructions, path, err := readSubagentInstructionFile(baseDir, role.InstructionsFile)
		if err != nil {
			return fmt.Errorf("agents.subagents role %q: %w", name, err)
		}
		role.Instructions = instructions
		role.InstructionsFile = path
		cfg.Agents.Subagents.Roles[name] = role
	}
	for name, persona := range cfg.Agents.Subagents.Personas {
		if strings.TrimSpace(persona.InstructionsFile) == "" {
			continue
		}
		if strings.TrimSpace(persona.Instructions) != "" {
			if persona.Source != "" && persona.Source != "builtin" {
				continue
			}
			return fmt.Errorf("agents.subagents persona %q sets both instructions and instructions_file", name)
		}
		instructions, path, err := readSubagentInstructionFile(baseDir, persona.InstructionsFile)
		if err != nil {
			return fmt.Errorf("agents.subagents persona %q: %w", name, err)
		}
		persona.Instructions = instructions
		persona.InstructionsFile = path
		cfg.Agents.Subagents.Personas[name] = persona
	}
	return nil
}

func readSubagentInstructionFile(baseDir, configuredPath string) (string, string, error) {
	path := strings.TrimSpace(configuredPath)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("read instructions_file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("instructions_file %q is not a regular file", path)
	}
	if info.Size() > 1<<20 {
		return "", "", fmt.Errorf("instructions_file %q exceeds 1 MiB", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read instructions_file %q: %w", path, err)
	}
	instructions := strings.TrimSpace(string(encoded))
	if instructions == "" {
		return "", "", fmt.Errorf("instructions_file %q is empty", path)
	}
	return instructions, path, nil
}

func ResolveReference(value string, lookupEnv func(string) (string, bool), lookupKeyring func(string) (string, error)) (string, error) {
	kind, name, ok := strings.Cut(value, ":")
	if !ok || name == "" {
		return "", fmt.Errorf("secret reference must use env:NAME or keyring:NAME")
	}
	switch kind {
	case "env":
		resolved, found := lookupEnv(name)
		if !found || resolved == "" {
			return "", fmt.Errorf("environment secret %q is not set", name)
		}
		return resolved, nil
	case "keyring":
		if lookupKeyring == nil {
			return "", fmt.Errorf("keyring secret %q cannot be resolved", name)
		}
		resolved, err := lookupKeyring(name)
		if err != nil {
			return "", fmt.Errorf("keyring secret %q: %w", name, err)
		}
		if resolved == "" {
			return "", fmt.Errorf("keyring secret %q is empty", name)
		}
		return resolved, nil
	default:
		return "", fmt.Errorf("unsupported secret reference scheme %q", kind)
	}
}
