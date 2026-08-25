package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	maxASTBridgeInputBytes  = 4 << 20
	maxASTBridgeOutputBytes = 32 << 20
	maxASTBridgeErrorBytes  = 1 << 20
)

type astBridge struct {
	runtimePath string
	mu          sync.Mutex
	scriptPath  string
	modulePath  string
}

type astBridgeRequest struct {
	Operation string         `json:"operation"`
	Options   map[string]any `json:"options"`
}

type astNativeMatch struct {
	Path          string         `json:"path"`
	Text          string         `json:"text"`
	ByteStart     int            `json:"byteStart"`
	ByteEnd       int            `json:"byteEnd"`
	StartLine     int            `json:"startLine"`
	StartColumn   int            `json:"startColumn"`
	EndLine       int            `json:"endLine"`
	EndColumn     int            `json:"endColumn"`
	MetaVariables map[string]any `json:"metaVariables,omitempty"`
}

type astNativeGrepResult struct {
	Matches          []astNativeMatch `json:"matches"`
	TotalMatches     int              `json:"totalMatches"`
	FilesWithMatches int              `json:"filesWithMatches"`
	FilesSearched    int              `json:"filesSearched"`
	LimitReached     bool             `json:"limitReached"`
	ParseErrors      []string         `json:"parseErrors,omitempty"`
}

type astNativeChange struct {
	Path        string `json:"path"`
	Before      string `json:"before"`
	After       string `json:"after"`
	ByteStart   int    `json:"byteStart"`
	ByteEnd     int    `json:"byteEnd"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
}

type astNativeFileChange struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type astNativeEditResult struct {
	Changes           []astNativeChange     `json:"changes"`
	FileChanges       []astNativeFileChange `json:"fileChanges"`
	TotalReplacements int                   `json:"totalReplacements"`
	FilesTouched      int                   `json:"filesTouched"`
	FilesSearched     int                   `json:"filesSearched"`
	Applied           bool                  `json:"applied"`
	LimitReached      bool                  `json:"limitReached"`
	ParseErrors       []string              `json:"parseErrors,omitempty"`
}

type astCappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *astCappedBuffer) Write(payload []byte) (int, error) {
	remaining := buffer.limit - buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return len(payload), nil
	}
	if len(payload) > remaining {
		buffer.truncated = true
		payload = payload[:remaining]
	}
	_, _ = buffer.Buffer.Write(payload)
	return len(payload), nil
}

func newASTBridge() *astBridge {
	return &astBridge{}
}

func (bridge *astBridge) invoke(ctx context.Context, operation string, options map[string]any, output any) error {
	if bridge == nil {
		return errors.New("AST bridge is unavailable")
	}
	if err := bridge.resolve(); err != nil {
		return err
	}
	request, err := json.Marshal(astBridgeRequest{Operation: operation, Options: options})
	if err != nil {
		return err
	}
	if len(request) > maxASTBridgeInputBytes {
		return errors.New("AST bridge request exceeds 4 MiB")
	}
	command := exec.CommandContext(ctx, bridge.runtimePath, bridge.scriptPath)
	command.Stdin = bytes.NewReader(request)
	command.Env = append(os.Environ(), "AZEM_PI_NATIVES="+bridge.modulePath)
	command.Dir = filepath.Dir(bridge.scriptPath)
	stdout := &astCappedBuffer{limit: maxASTBridgeOutputBytes}
	stderr := &astCappedBuffer{limit: maxASTBridgeErrorBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return fmt.Errorf("AST native bridge: %w: %s", err, detail)
		}
		return fmt.Errorf("AST native bridge: %w", err)
	}
	if stdout.truncated {
		return errors.New("AST native bridge output exceeds 32 MiB")
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("decode AST native bridge output: %w", err)
	}
	return nil
}

func (bridge *astBridge) match(ctx context.Context, source, language string, patterns []string) (astNativeGrepResult, error) {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(language) == "" || len(patterns) == 0 {
		return astNativeGrepResult{}, errors.New("AST source, language, and patterns are required")
	}
	var result astNativeGrepResult
	err := bridge.invoke(ctx, "match", map[string]any{
		"source": source, "lang": language, "patterns": patterns, "limit": 1, "timeoutMs": 250,
	}, &result)
	return result, err
}

func (bridge *astBridge) resolve() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.runtimePath != "" && bridge.scriptPath != "" && bridge.modulePath != "" {
		return nil
	}
	runtimePath := strings.TrimSpace(os.Getenv("AZEM_JS_RUNTIME"))
	if runtimePath == "" {
		for _, candidate := range []string{"bun", "node"} {
			if resolved, err := exec.LookPath(candidate); err == nil {
				runtimePath = resolved
				break
			}
		}
	}
	if runtimePath == "" {
		return errors.New("AST tools require the bundled JavaScript runtime or bun/node on PATH")
	}
	scriptPath := strings.TrimSpace(os.Getenv("AZEM_AST_BRIDGE"))
	modulePath := strings.TrimSpace(os.Getenv("AZEM_PI_NATIVES"))
	for _, root := range astBridgeRoots() {
		if scriptPath == "" {
			candidate := filepath.Join(root, "internal", "astbridge", "bridge.mjs")
			if regularFile(candidate) {
				scriptPath = candidate
			}
		}
		if modulePath == "" {
			candidate := filepath.Join(root, "frontend", "node_modules", "@oh-my-pi", "pi-natives", "native", "index.js")
			if regularFile(candidate) {
				modulePath = candidate
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		resources := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Resources", "astbridge"))
		if scriptPath == "" {
			candidate := filepath.Join(resources, "bridge.mjs")
			if regularFile(candidate) {
				scriptPath = candidate
			}
		}
		if modulePath == "" {
			candidate := filepath.Join(resources, "node_modules", "@oh-my-pi", "pi-natives", "native", "index.js")
			if regularFile(candidate) {
				modulePath = candidate
			}
		}
	}
	if !regularFile(scriptPath) {
		return errors.New("AST bridge script is missing")
	}
	if !regularFile(modulePath) {
		return errors.New("@oh-my-pi/pi-natives is missing; run frontend dependency installation")
	}
	bridge.runtimePath, bridge.scriptPath, bridge.modulePath = runtimePath, scriptPath, modulePath
	return nil
}

func astBridgeRoots() []string {
	var roots []string
	if _, source, _, ok := runtime.Caller(0); ok {
		roots = append(roots, filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..")))
	}
	if working, err := os.Getwd(); err == nil {
		for current := working; ; current = filepath.Dir(current) {
			roots = append(roots, current)
			next := filepath.Dir(current)
			if next == current {
				break
			}
		}
	}
	return roots
}

func regularFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
