package customtools

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const maxBridgeMessageBytes = 20 << 20

//go:embed bridge.mjs
var bridgeSource []byte

type Definition struct {
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
	Hidden      bool            `json:"hidden"`
	Approval    string          `json:"approval"`
	Concurrency string          `json:"concurrency"`
	ModulePath  string          `json:"modulePath"`
}
type ExtensionCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ModulePath  string `json:"modulePath"`
}

type ExtensionProvider struct {
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	ModulePath string          `json:"modulePath"`
}

type ExtensionAgent struct {
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	ModulePath string          `json:"modulePath"`
}

type Host struct {
	ctx             context.Context
	cancel          context.CancelFunc
	command         *exec.Cmd
	stdin           io.WriteCloser
	diagnostics     []string
	writeMu         sync.Mutex
	commands        []ExtensionCommand
	providers       []ExtensionProvider
	agents          []ExtensionAgent
	themePaths      []string
	writeFallbacks  int
	deleteFallbacks int
	mu              sync.Mutex
	pending         map[string]*pendingCall
	definitions     []Definition
	next            atomic.Uint64
	done            chan struct{}
	waitErr         error
	stderr          *limitedBuffer
	bridgePath      string
	closed          bool
}

type pendingCall struct {
	result chan bridgeMessage
	sink   tool.UpdateSink
}

type bridgeMessage struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result,omitempty"`
	Update bridgeResult    `json:"update,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type bridgeResult struct {
	Content string                `json:"content"`
	Details json.RawMessage       `json:"details,omitempty"`
	Parts   []message.ContentPart `json:"parts,omitempty"`
	IsError bool                  `json:"isError,omitempty"`
}

type initResult struct {
	Diagnostics     []string            `json:"diagnostics"`
	Definitions     []Definition        `json:"definitions"`
	Commands        []ExtensionCommand  `json:"commands"`
	Providers       []ExtensionProvider `json:"providers"`
	Agents          []ExtensionAgent    `json:"agents"`
	ThemePaths      []string            `json:"themePaths"`
	WriteFallbacks  int                 `json:"writeFallbacks"`
	DeleteFallbacks int                 `json:"deleteFallbacks"`
}

type limitedBuffer struct {
	mu    sync.Mutex
	value []byte
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := (64 << 10) - len(buffer.value)
	if remaining > 0 {
		buffer.value = append(buffer.value, payload[:min(len(payload), remaining)]...)
	}
	return len(payload), nil
}

func (buffer *limitedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.value)
}

func New(ctx context.Context, cwd string, modules []string) (*Host, error) {
	return NewWithExtensions(ctx, cwd, modules, nil)
}

func NewWithExtensions(ctx context.Context, cwd string, modules, extensions []string) (*Host, error) {
	if len(modules) == 0 && len(extensions) == 0 {
		return &Host{}, nil
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		return nil, errors.New("custom tools require Bun")
	}
	bridgeFile, err := os.CreateTemp("", "azem-custom-tools-*.mjs")
	if err != nil {
		return nil, err
	}
	bridgePath := bridgeFile.Name()
	if err := bridgeFile.Chmod(0o600); err != nil {
		bridgeFile.Close()
		os.Remove(bridgePath)
		return nil, err
	}
	if _, err := bridgeFile.Write(bridgeSource); err != nil {
		bridgeFile.Close()
		os.Remove(bridgePath)
		return nil, err
	}
	if err := bridgeFile.Close(); err != nil {
		os.Remove(bridgePath)
		return nil, err
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(runtimeCtx, bun, "run", bridgePath)
	command.Dir = cwd
	command.Env = os.Environ()
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		os.Remove(bridgePath)
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		os.Remove(bridgePath)
		return nil, err
	}
	stderr := &limitedBuffer{}
	command.Stderr = stderr
	host := &Host{ctx: runtimeCtx, cancel: cancel, command: command, stdin: stdin, pending: make(map[string]*pendingCall), done: make(chan struct{}), stderr: stderr, bridgePath: bridgePath}
	if err := command.Start(); err != nil {
		cancel()
		os.Remove(bridgePath)
		return nil, err
	}
	go host.readLoop(stdout)
	go func() {
		host.waitErr = command.Wait()
		host.failPending(fmt.Errorf("custom tool host stopped: %w: %s", host.waitErr, strings.TrimSpace(stderr.String())))
		close(host.done)
	}()
	var initialized initResult
	if err := host.request(ctx, "init", map[string]any{"cwd": cwd, "modules": modules, "extensions": extensions}, nil, &initialized); err != nil {
		_ = host.Close(context.Background())
		return nil, err
	}
	host.definitions = initialized.Definitions
	host.commands = append([]ExtensionCommand(nil), initialized.Commands...)
	host.providers = append([]ExtensionProvider(nil), initialized.Providers...)
	host.agents = append([]ExtensionAgent(nil), initialized.Agents...)
	host.themePaths = append([]string(nil), initialized.ThemePaths...)
	host.writeFallbacks = initialized.WriteFallbacks
	host.deleteFallbacks = initialized.DeleteFallbacks
	host.diagnostics = append([]string(nil), initialized.Diagnostics...)
	return host, nil
}

func (host *Host) Definitions() []Definition {
	if host == nil {
		return nil
	}
	return append([]Definition(nil), host.definitions...)
}

func (host *Host) Diagnostics() []string {
	if host == nil {
		return nil
	}
	return append([]string(nil), host.diagnostics...)
}

func (host *Host) ExtensionCommands() []ExtensionCommand {
	if host == nil {
		return nil
	}
	return append([]ExtensionCommand(nil), host.commands...)
}

func (host *Host) ExtensionProviders() []ExtensionProvider {
	if host == nil {
		return nil
	}
	return append([]ExtensionProvider(nil), host.providers...)
}

func (host *Host) ExtensionAgents() []ExtensionAgent {
	if host == nil {
		return nil
	}
	return append([]ExtensionAgent(nil), host.agents...)
}

func (host *Host) ThemePaths() []string {
	if host == nil {
		return nil
	}
	return append([]string(nil), host.themePaths...)
}

func (host *Host) ExecuteCommand(ctx context.Context, name, arguments string) (string, bool, error) {
	if host == nil {
		return "", false, nil
	}
	var found bool
	for _, command := range host.commands {
		if command.Name == name {
			found = true
			break
		}
	}
	if !found {
		return "", false, nil
	}
	var result struct {
		Prompt string `json:"prompt"`
		Output string `json:"output"`
	}
	if err := host.request(ctx, "command", map[string]any{"command": name, "args": arguments}, nil, &result); err != nil {
		return "", true, err
	}
	if result.Prompt != "" {
		return result.Prompt, true, nil
	}
	return result.Output, true, nil
}

func (host *Host) BrokerWrite(ctx context.Context, destination string, content []byte, cause error, sessionID string) (bool, error) {
	if host == nil || host.writeFallbacks == 0 {
		return false, nil
	}
	var result struct {
		Handled bool `json:"handled"`
	}
	causeText := ""
	if cause != nil {
		causeText = cause.Error()
	}
	err := host.request(ctx, "broker_write", map[string]any{
		"dst": destination, "content": string(content), "cause": causeText, "sessionId": sessionID,
	}, nil, &result)
	return result.Handled, err
}

func (host *Host) BrokerDelete(ctx context.Context, destination string, cause error, sessionID string, confirmedFile bool) (bool, error) {
	if host == nil || host.deleteFallbacks == 0 {
		return false, nil
	}
	var result struct {
		Handled bool `json:"handled"`
	}
	causeText := ""
	if cause != nil {
		causeText = cause.Error()
	}
	err := host.request(ctx, "broker_delete", map[string]any{
		"dst": destination, "cause": causeText, "sessionId": sessionID, "confirmedFile": confirmedFile,
	}, nil, &result)
	return result.Handled, err
}

func (host *Host) Drivers() ([]tool.Driver, error) {
	if host == nil {
		return nil, nil
	}
	drivers := make([]tool.Driver, 0, len(host.definitions))
	for _, definition := range host.definitions {
		var schema message.JSONSchema
		if len(definition.Parameters) == 0 {
			definition.Parameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
		}
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
			return nil, fmt.Errorf("custom tool %s schema: %w", definition.Name, err)
		}
		effect := agentruntime.ToolEffectExternalSideEffect
		switch definition.Approval {
		case "read", "read_only":
			effect = agentruntime.ToolEffectReadOnly
		case "write":
			effect = agentruntime.ToolEffectWrite
		}
		concurrency := tool.ConcurrencyParallel
		if definition.Concurrency == "sequential" {
			concurrency = tool.ConcurrencySequential
		} else if definition.Concurrency == "exclusive" {
			concurrency = tool.ConcurrencyExclusive
		}
		policy := agentruntime.ToolPolicy{
			Effect: effect, RiskLevel: "high", Origin: "custom",
			Metadata:    map[string]string{"module": definition.ModulePath, "strict": fmt.Sprint(definition.Strict)},
			Concurrency: concurrency,
		}
		drivers = append(drivers, &driver{host: host, definition: tool.Definition{
			Name: definition.Name, Description: definition.Description, InputSchema: schema,
			Concurrency: concurrency,
		}, policy: policy})
	}
	return drivers, nil
}

func (host *Host) request(ctx context.Context, operation string, body map[string]any, sink tool.UpdateSink, target any) error {
	if host == nil || host.command == nil {
		return errors.New("custom tool host is unavailable")
	}
	id := fmt.Sprintf("custom-%d", host.next.Add(1))
	request := make(map[string]any, len(body)+2)
	for key, value := range body {
		request[key] = value
	}
	request["id"], request["op"] = id, operation
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(encoded) > maxBridgeMessageBytes {
		return errors.New("custom tool request exceeds 8 MiB")
	}
	pending := &pendingCall{result: make(chan bridgeMessage, 1), sink: sink}
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return errors.New("custom tool host is closed")
	}
	host.pending[id] = pending
	host.mu.Unlock()
	defer func() {
		host.mu.Lock()
		delete(host.pending, id)
		host.mu.Unlock()
	}()
	host.writeMu.Lock()
	_, err = host.stdin.Write(append(encoded, '\n'))
	host.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case message := <-pending.result:
		if message.Type == "error" {
			return errors.New(message.Error)
		}
		if target != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, target); err != nil {
				return err
			}
		}
		return nil
	case <-ctx.Done():
		_ = host.send(map[string]any{"id": id, "op": "cancel"})
		return ctx.Err()
	case <-host.done:
		return fmt.Errorf("custom tool host stopped: %w", host.waitErr)
	}
}

func (host *Host) send(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	host.writeMu.Lock()
	defer host.writeMu.Unlock()
	_, err = host.stdin.Write(append(encoded, '\n'))
	return err
}

func (host *Host) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxBridgeMessageBytes)
	for scanner.Scan() {
		var response bridgeMessage
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.ID == "" {
			continue
		}
		host.mu.Lock()
		pending := host.pending[response.ID]
		host.mu.Unlock()
		if pending == nil {
			continue
		}
		if response.Type == "update" {
			if pending.sink != nil {
				data := map[string]string{}
				if len(response.Update.Details) > 0 {
					data["details"] = string(response.Update.Details)
				}
				_ = pending.sink(tool.Update{Kind: tool.UpdateProgress, Message: response.Update.Content, Data: data})
			}
			continue
		}
		select {
		case pending.result <- response:
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		host.failPending(err)
	}
}

func (host *Host) failPending(err error) {
	host.mu.Lock()
	pending := make([]*pendingCall, 0, len(host.pending))
	for _, call := range host.pending {
		pending = append(pending, call)
	}
	host.mu.Unlock()
	for _, call := range pending {
		select {
		case call.result <- bridgeMessage{Type: "error", Error: err.Error()}:
		default:
		}
	}
}

func (host *Host) Close(ctx context.Context) error {
	if host == nil || host.command == nil {
		return nil
	}
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		select {
		case <-host.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	host.closed = true
	host.mu.Unlock()
	_ = host.send(map[string]any{"id": "shutdown", "op": "shutdown"})
	select {
	case <-host.done:
		os.Remove(host.bridgePath)
		return nil
	case <-time.After(time.Second):
		host.cancel()
	case <-ctx.Done():
		host.cancel()
		return ctx.Err()
	}
	select {
	case <-host.done:
		os.Remove(host.bridgePath)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type driver struct {
	host       *Host
	definition tool.Definition
	policy     agentruntime.ToolPolicy
}

func (current *driver) Definition() tool.Definition         { return current.definition }
func (current *driver) ToolPolicy() agentruntime.ToolPolicy { return current.policy.Clone() }
func (current *driver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	var arguments map[string]any
	if len(call.Arguments) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&arguments); err != nil {
			return tool.Result{}, err
		}
	}
	var result bridgeResult
	if err := current.host.request(ctx, "execute", map[string]any{"tool": current.definition.Name, "callId": call.ID, "args": arguments}, sink, &result); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{ToolCallID: call.ID, Name: current.definition.Name, Content: result.Content, Parts: result.Parts, Structured: result.Details, IsError: result.IsError}, nil
}
