package eval

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Viking602/venat/message"
)

const BaselineIdentityVersionV1 = 1

type ToolIdentityV1 struct {
	Name             string `json:"name"`
	Origin           string `json:"origin,omitempty"`
	DefinitionSHA256 string `json:"definition_sha256"`
}

type DependencyIdentityV1 struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ValidatorIdentityV1 struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Status  string   `json:"status"` // available, missing, or failed
	Version string   `json:"version,omitempty"`
}

// BaselineIdentityV1 binds an evaluation outcome to executable prompt, tool,
// source, dependency, and validator identities without storing prompt text.
type BaselineIdentityV1 struct {
	Version                int                    `json:"version"`
	ID                     string                 `json:"id"`
	AzemVersion            string                 `json:"azem_version"`
	AzemCommit             string                 `json:"azem_commit"`
	BuildTime              string                 `json:"build_time,omitempty"`
	Provider               string                 `json:"provider"`
	Model                  string                 `json:"model"`
	Reasoning              string                 `json:"reasoning,omitempty"`
	InstructionFingerprint string                 `json:"instruction_fingerprint"`
	TaskPromptSHA256       string                 `json:"task_prompt_sha256"`
	ToolCatalogSHA256      string                 `json:"tool_catalog_sha256"`
	Tools                  []ToolIdentityV1       `json:"tools"`
	RepositoryCommit       string                 `json:"repository_commit"`
	RepositoryDirtySHA256  string                 `json:"repository_dirty_sha256"`
	DependencyLockSHA256   string                 `json:"dependency_lock_sha256"`
	Dependencies           []DependencyIdentityV1 `json:"dependencies"`
	Validators             []ValidatorIdentityV1  `json:"validators"`
}

type ValidatorCommand struct {
	Name    string
	Command []string
}

type BaselineOptions struct {
	Workspace              string
	AzemVersion            string
	AzemCommit             string
	BuildTime              string
	Provider               string
	Model                  string
	Reasoning              string
	InstructionFingerprint string
	TaskPrompt             string
	Tools                  []message.ToolDefinition
	DependencyFiles        []string
	Validators             []ValidatorCommand
}

func CaptureBaseline(ctx context.Context, options BaselineOptions) (BaselineIdentityV1, error) {
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" || options.Provider == "" || options.Model == "" || options.InstructionFingerprint == "" {
		return BaselineIdentityV1{}, fmt.Errorf("eval: baseline requires workspace, route, and instruction fingerprint")
	}
	root, err := commandOutput(ctx, workspace, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: locate repository: %w", err)
	}
	root = strings.TrimSpace(root)
	commit, err := commandOutput(ctx, root, "git", "rev-parse", "HEAD")
	if err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: read repository commit: %w", err)
	}
	dirty, err := commandBytes(ctx, root, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: read repository state: %w", err)
	}
	dirtyHash, err := dirtyWorktreeHash(root, dirty)
	if err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: hash repository state: %w", err)
	}
	tools, toolCatalogHash, err := toolIdentities(options.Tools)
	if err != nil {
		return BaselineIdentityV1{}, err
	}
	dependencies, dependencyHash, err := dependencyIdentities(root, options.DependencyFiles)
	if err != nil {
		return BaselineIdentityV1{}, err
	}
	validators := captureValidators(ctx, root, options.Validators)
	identity := BaselineIdentityV1{
		Version:                BaselineIdentityVersionV1,
		AzemVersion:            firstNonempty(options.AzemVersion, "unknown"),
		AzemCommit:             firstNonempty(options.AzemCommit, "unknown"),
		BuildTime:              options.BuildTime,
		Provider:               options.Provider,
		Model:                  options.Model,
		Reasoning:              options.Reasoning,
		InstructionFingerprint: options.InstructionFingerprint,
		TaskPromptSHA256:       sumHex([]byte(options.TaskPrompt)),
		ToolCatalogSHA256:      toolCatalogHash,
		RepositoryDirtySHA256:  dirtyHash,
		DependencyLockSHA256:   dependencyHash,
		Dependencies:           dependencies,
		Validators:             validators,
		Tools:                  tools,
		RepositoryCommit:       strings.TrimSpace(commit),
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: encode baseline identity: %w", err)
	}
	identity.ID = sumHex(encoded)
	return identity, nil
}

func toolIdentities(definitions []message.ToolDefinition) ([]ToolIdentityV1, string, error) {
	sorted := append([]message.ToolDefinition(nil), definitions...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name == sorted[j].Name {
			return sorted[i].Origin < sorted[j].Origin
		}
		return sorted[i].Name < sorted[j].Name
	})
	identities := make([]ToolIdentityV1, 0, len(sorted))
	var catalog strings.Builder
	for index, definition := range sorted {
		if definition.Name == "" {
			return nil, "", fmt.Errorf("eval: tool definition has empty name")
		}
		if index > 0 && definition.Name == sorted[index-1].Name {
			return nil, "", fmt.Errorf("eval: duplicate tool definition %q", definition.Name)
		}
		encoded, err := json.Marshal(definition)
		if err != nil {
			return nil, "", fmt.Errorf("eval: encode tool %s: %w", definition.Name, err)
		}
		digest := sumHex(encoded)
		identities = append(identities, ToolIdentityV1{Name: definition.Name, Origin: definition.Origin, DefinitionSHA256: digest})
		catalog.WriteString(definition.Name)
		catalog.WriteByte(0)
		catalog.WriteString(digest)
		catalog.WriteByte(0)
	}
	return identities, sumHex([]byte(catalog.String())), nil
}

func dependencyIdentities(root string, requested []string) ([]DependencyIdentityV1, string, error) {
	if len(requested) == 0 {
		requested = []string{"go.mod", "go.sum", "frontend/package.json", "frontend/bun.lock", "frontend/bun.lockb"}
	}
	paths := append([]string(nil), requested...)
	sort.Strings(paths)
	identities := make([]DependencyIdentityV1, 0, len(paths))
	var combined strings.Builder
	for _, relative := range paths {
		relative = filepath.Clean(strings.TrimSpace(relative))
		if relative == "." || relative == "" || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, "", fmt.Errorf("eval: invalid dependency path %q", relative)
		}
		payload, err := os.ReadFile(filepath.Join(root, relative))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("eval: read dependency %s: %w", relative, err)
		}
		digest := sumHex(payload)
		slashPath := filepath.ToSlash(relative)
		identities = append(identities, DependencyIdentityV1{Path: slashPath, SHA256: digest})
		combined.WriteString(slashPath)
		combined.WriteByte(0)
		combined.WriteString(digest)
		combined.WriteByte(0)
	}
	return identities, sumHex([]byte(combined.String())), nil
}

func captureValidators(ctx context.Context, root string, requested []ValidatorCommand) []ValidatorIdentityV1 {
	if len(requested) == 0 {
		requested = []ValidatorCommand{
			{Name: "git", Command: []string{"git", "--version"}},
			{Name: "go", Command: []string{"go", "version"}},
			{Name: "bun", Command: []string{"bun", "--version"}},
		}
	}
	validators := make([]ValidatorIdentityV1, 0, len(requested))
	for _, validator := range requested {
		identity := ValidatorIdentityV1{Name: validator.Name, Command: append([]string(nil), validator.Command...)}
		if len(validator.Command) == 0 {
			identity.Status = "missing"
			validators = append(validators, identity)
			continue
		}
		if _, err := exec.LookPath(validator.Command[0]); err != nil {
			identity.Status = "missing"
			validators = append(validators, identity)
			continue
		}
		output, err := commandOutput(ctx, root, validator.Command[0], validator.Command[1:]...)
		if err != nil {
			identity.Status = "failed"
		} else {
			identity.Status = "available"
			identity.Version = strings.TrimSpace(output)
		}
		validators = append(validators, identity)
	}
	sort.Slice(validators, func(i, j int) bool { return validators[i].Name < validators[j].Name })
	return validators
}

func commandOutput(ctx context.Context, directory, name string, args ...string) (string, error) {
	payload, err := commandBytes(ctx, directory, name, args...)
	return string(payload), err
}
func commandBytes(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output := &limitedOutput{limit: fixtureOutputLimit}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if output.truncated {
		return nil, fmt.Errorf("%s: output exceeded %d bytes", strings.Join(command.Args, " "), fixtureOutputLimit)
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", strings.Join(command.Args, " "), strings.TrimSpace(string(output.Bytes())))
		}
		return nil, err
	}
	return output.Bytes(), nil
}

func WriteBaseline(w io.Writer, identity BaselineIdentityV1) error {
	if w == nil || identity.Version != BaselineIdentityVersionV1 || identity.ID == "" {
		return fmt.Errorf("eval: invalid baseline output")
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(identity); err != nil {
		return fmt.Errorf("eval: encode baseline: %w", err)
	}
	return nil
}

func ReadBaseline(r io.Reader) (BaselineIdentityV1, error) {
	if r == nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: baseline reader is nil")
	}
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var identity BaselineIdentityV1
	if err := decoder.Decode(&identity); err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: decode baseline: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return BaselineIdentityV1{}, fmt.Errorf("eval: decode baseline: %w", err)
	}
	claimed := identity.ID
	identity.ID = ""
	encoded, err := json.Marshal(identity)
	if err != nil || claimed == "" || claimed != sumHex(encoded) {
		return BaselineIdentityV1{}, fmt.Errorf("eval: baseline identity hash mismatch")
	}
	identity.ID = claimed
	return identity, nil
}

type dirtyWorktreeEntry struct {
	status string
	path   string
}

func dirtyWorktreeHash(root string, status []byte) (string, error) {
	parts := strings.Split(string(status), "\x00")
	entries := make([]dirtyWorktreeEntry, 0, len(parts))
	for index := 0; index < len(parts); index++ {
		part := parts[index]
		if part == "" {
			continue
		}
		if len(part) < 3 {
			return "", fmt.Errorf("malformed git status record")
		}
		entry := dirtyWorktreeEntry{status: part[:2], path: part[3:]}
		entries = append(entries, entry)
		if (entry.status[0] == 'R' || entry.status[0] == 'C' || entry.status[1] == 'R' || entry.status[1] == 'C') && index+1 < len(parts) {
			index++
			if parts[index] == "" {
				return "", fmt.Errorf("malformed rename status record")
			}
			entries = append(entries, dirtyWorktreeEntry{status: entry.status, path: parts[index]})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path == entries[j].path {
			return entries[i].status < entries[j].status
		}
		return entries[i].path < entries[j].path
	})
	hash := sha256.New()
	var size [8]byte
	for _, entry := range entries {
		writeHashField := func(payload []byte) {
			binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
			hash.Write(size[:])
			hash.Write(payload)
		}
		writeHashField([]byte(entry.status))
		writeHashField([]byte(entry.path))
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.path)))
		if err != nil {
			if os.IsNotExist(err) {
				writeHashField([]byte("missing"))
				continue
			}
			return "", fmt.Errorf("read dirty file %s: %w", entry.path, err)
		}
		writeHashField([]byte("present"))
		writeHashField(payload)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}
