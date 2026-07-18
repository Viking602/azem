package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
