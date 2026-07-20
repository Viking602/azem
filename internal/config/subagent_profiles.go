package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type discoveredSubagentProfile struct {
	Kind             string                 `json:"kind,omitempty" toml:"kind" yaml:"kind"`
	Name             string                 `json:"name,omitempty" toml:"name" yaml:"name"`
	Description      string                 `json:"description,omitempty" toml:"description" yaml:"description"`
	Instructions     string                 `json:"instructions,omitempty" toml:"instructions" yaml:"instructions"`
	InstructionsFile string                 `json:"instructions_file,omitempty" toml:"instructions_file" yaml:"instructions_file"`
	Persona          string                 `json:"persona,omitempty" toml:"persona" yaml:"persona"`
	Provider         string                 `json:"provider,omitempty" toml:"provider" yaml:"provider"`
	Model            string                 `json:"model,omitempty" toml:"model" yaml:"model"`
	Reasoning        string                 `json:"reasoning,omitempty" toml:"reasoning" yaml:"reasoning"`
	CapabilityMode   string                 `json:"capability_mode,omitempty" toml:"capability_mode" yaml:"capability_mode"`
	Isolation        string                 `json:"isolation,omitempty" toml:"isolation" yaml:"isolation"`
	Tools            []string               `json:"tools,omitempty" toml:"tools" yaml:"tools,omitempty"`
	Inputs           []SubagentContractItem `json:"inputs,omitempty" toml:"inputs" yaml:"inputs,omitempty"`
	Outputs          []SubagentContractItem `json:"outputs,omitempty" toml:"outputs" yaml:"outputs,omitempty"`
}

// discoverSubagentProfiles applies compatibility, user, then project profiles.
// Callers provide explicit names so config.yaml remains the final authority.
func discoverSubagentProfiles(cfg *Config, workspaceRoot, homeDir string, explicitRoles, explicitPersonas map[string]bool) error {
	roots := []string{
		filepath.Join(homeDir, ".agents", "agents"),
		filepath.Join(homeDir, ".config", "azem", "agents"),
		filepath.Join(workspaceRoot, ".azem", "agents"),
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("scan subagent profiles %q: %w", root, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !supportedSubagentProfile(entry.Name()) {
				continue
			}
			path := filepath.Join(root, entry.Name())
			profile, err := loadDiscoveredSubagentProfile(path)
			if err != nil {
				return err
			}
			if profile.Name == "" {
				profile.Name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			}
			switch strings.ToLower(strings.TrimSpace(profile.Kind)) {
			case "", "role":
				role, err := profile.discoveredRole(path)
				if err != nil {
					return err
				}
				if !explicitRoles[profile.Name] {
					cfg.Agents.Subagents.Roles[profile.Name] = role
				}
			case "persona":
				persona, err := profile.discoveredPersona(path)
				if err != nil {
					return err
				}
				if !explicitPersonas[profile.Name] {
					cfg.Agents.Subagents.Personas[profile.Name] = persona
				}
			default:
				return fmt.Errorf("subagent profile %q has invalid kind %q", path, profile.Kind)
			}
		}
	}
	return nil
}

func supportedSubagentProfile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".toml", ".json", ".md", ".markdown":
		return true
	default:
		return false
	}
}

func loadDiscoveredSubagentProfile(path string) (discoveredSubagentProfile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return discoveredSubagentProfile{}, fmt.Errorf("read subagent profile %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return discoveredSubagentProfile{}, fmt.Errorf("subagent profile %q is not a regular file", path)
	}
	if info.Size() > 1<<20 {
		return discoveredSubagentProfile{}, fmt.Errorf("subagent profile %q exceeds 1 MiB", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return discoveredSubagentProfile{}, fmt.Errorf("read subagent profile %q: %w", path, err)
	}
	var profile discoveredSubagentProfile
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&profile); err != nil {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: %w", path, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: trailing JSON content", path)
		}
	case ".toml":
		metadata, err := toml.Decode(string(encoded), &profile)
		if err != nil {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: %w", path, err)
		}
		if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: unknown TOML keys %v", path, undecoded)
		}
	case ".md", ".markdown":
		frontmatter, body, err := splitSubagentMarkdown(encoded)
		if err != nil {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: %w", path, err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(frontmatter))
		decoder.KnownFields(true)
		if err := decoder.Decode(&profile); err != nil {
			return discoveredSubagentProfile{}, fmt.Errorf("decode subagent profile %q: %w", path, err)
		}
		if profile.Instructions == "" && profile.InstructionsFile == "" {
			profile.Instructions = strings.TrimSpace(string(body))
		}
	}
	return profile, nil
}

func splitSubagentMarkdown(encoded []byte) ([]byte, []byte, error) {
	normalized := bytes.ReplaceAll(encoded, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, nil, fmt.Errorf("markdown profile requires YAML front matter")
	}
	rest := normalized[len("---\n"):]
	index := bytes.Index(rest, []byte("\n---\n"))
	if index < 0 {
		return nil, nil, fmt.Errorf("markdown profile has unterminated YAML front matter")
	}
	return rest[:index], rest[index+len("\n---\n"):], nil
}

func (profile discoveredSubagentProfile) discoveredRole(source string) (SubagentRoleConfig, error) {
	instructions, instructionsFile, err := hydrateDiscoveredInstructions(source, profile.Instructions, profile.InstructionsFile)
	if err != nil {
		return SubagentRoleConfig{}, err
	}
	return SubagentRoleConfig{
		Description: profile.Description, Instructions: instructions, InstructionsFile: instructionsFile,
		Persona: profile.Persona, Provider: profile.Provider, Model: profile.Model, Reasoning: profile.Reasoning,
		CapabilityMode: profile.CapabilityMode, Isolation: profile.Isolation,
		Tools: append([]string(nil), profile.Tools...), Source: source,
	}, nil
}

func (profile discoveredSubagentProfile) discoveredPersona(source string) (SubagentPersonaConfig, error) {
	instructions, instructionsFile, err := hydrateDiscoveredInstructions(source, profile.Instructions, profile.InstructionsFile)
	if err != nil {
		return SubagentPersonaConfig{}, err
	}
	return SubagentPersonaConfig{
		Description: profile.Description, Instructions: instructions, InstructionsFile: instructionsFile,
		Provider: profile.Provider, Model: profile.Model, Reasoning: profile.Reasoning, Isolation: profile.Isolation,
		Inputs: append([]SubagentContractItem(nil), profile.Inputs...), Outputs: append([]SubagentContractItem(nil), profile.Outputs...),
		Source: source,
	}, nil
}

func hydrateDiscoveredInstructions(source, inline, configuredPath string) (string, string, error) {
	inline = strings.TrimSpace(inline)
	configuredPath = strings.TrimSpace(configuredPath)
	if inline != "" && configuredPath != "" {
		return "", "", fmt.Errorf("subagent profile %q sets both instructions and instructions_file", source)
	}
	if configuredPath == "" {
		return inline, "", nil
	}
	instructions, path, err := readSubagentInstructionFile(filepath.Dir(source), configuredPath)
	if err != nil {
		return "", "", fmt.Errorf("subagent profile %q: %w", source, err)
	}
	return instructions, path, nil
}
