package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/tool/kit"
	mcpclient "github.com/Viking602/go-hydaelyn/transport/mcp/client"
	"github.com/Viking602/go-hydaelyn/transport/mcpcontract"

	"github.com/Viking602/azem/internal/config"
)

type State string

const (
	StateDisabled   State = "disabled"
	StateConnecting State = "connecting"
	StateReady      State = "ready"
	StateDegraded   State = "degraded"
	StateStopped    State = "stopped"
)

var ErrManagerClosed = errors.New("mcp manager is closed")

// maxMCPModelOutputBytes bounds one MCP result before it enters provider
// context, durable model history, or UI events. Unlike shell output, MCP
// output cannot be silently truncated because doing so could turn incomplete
// structured data into an apparently successful result.
const maxMCPModelOutputBytes = 256 << 10

type Event struct {
	Server string
	State  State
	Error  string
	At     time.Time
}

type Diagnostic struct {
	Server string
	Tool   string
	Error  string
}

type ToolSnapshot struct {
	Name             string
	Description      string
	Effect           string
	RequiresApproval bool
}

type ServerSnapshot struct {
	Name        string
	State       State
	ToolCount   int
	Tools       []ToolSnapshot
	Diagnostics []Diagnostic
	LastError   string
}

type SecretResolver func(context.Context, string) (string, error)
type DialFunc func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error)
type SleepFunc func(context.Context, time.Duration) error

type Options struct {
	Dial        DialFunc
	Sleep       SleepFunc
	Sink        func(Event)
	Elicitation func(context.Context, string, mcpcontract.Elicitation) (mcpcontract.ElicitationResult, error)
}

type Manager struct {
	mu       sync.RWMutex
	config   map[string]config.MCPServerConfig
	version  string
	resolve  SecretResolver
	dial     DialFunc
	sleep    SleepFunc
	sink     func(Event)
	servers  map[string]*server
	attempts map[string]*connectionAttempt
	closing  []*connectionAttempt
	closed   bool
}

type connectionAttempt struct {
	client mcpcontract.Client
	cancel context.CancelFunc
	ready  chan struct{}
	once   sync.Once
	done   chan struct{}
	err    error
}

func newConnectionAttempt(client mcpcontract.Client) *connectionAttempt {
	ready := make(chan struct{})
	close(ready)
	return &connectionAttempt{client: client, ready: ready, done: make(chan struct{})}
}

func newDialAttempt(cancel context.CancelFunc) *connectionAttempt {
	return &connectionAttempt{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
}

func (a *connectionAttempt) finishDial(client mcpcontract.Client) {
	a.client = client
	close(a.ready)
}

func (a *connectionAttempt) close() {
	a.once.Do(func() {
		go func() {
			if a.cancel != nil {
				a.cancel()
			}
			<-a.ready
			if a.client != nil {
				a.err = a.client.Close()
			}
			close(a.done)
		}()
	})
}

func (a *connectionAttempt) waitClosed(ctx context.Context) error {
	select {
	case <-a.ready:
		<-a.done
		return a.err
	default:
	}
	select {
	case <-a.ready:
		<-a.done
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type server struct {
	state       State
	client      mcpcontract.Client
	connection  *connectionAttempt
	tools       []tool.Driver
	diagnostics []Diagnostic
	lastError   string
}

func NewManager(servers map[string]config.MCPServerConfig, version string, resolve SecretResolver, options Options) *Manager {
	copied := make(map[string]config.MCPServerConfig, len(servers))
	states := make(map[string]*server, len(servers))
	for name, serverConfig := range servers {
		copied[name] = serverConfig
		states[name] = &server{state: StateDisabled}
	}
	if resolve == nil {
		resolve = resolveEnvironmentReference
	}
	if options.Dial == nil {
		options.Dial = func(ctx context.Context, name string, serverConfig config.MCPServerConfig, environment map[string]string, headers http.Header) (mcpcontract.Client, error) {
			return defaultDial(ctx, name, serverConfig, environment, headers, options.Elicitation)
		}
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	return &Manager{config: copied, version: version, resolve: resolve, dial: options.Dial, sleep: options.Sleep, sink: options.Sink, servers: states, attempts: make(map[string]*connectionAttempt)}
}

func (m *Manager) Start(ctx context.Context) error {
	if m.isClosed() {
		return ErrManagerClosed
	}
	names := m.names()
	var startErr error
	for _, name := range names {
		serverConfig := m.config[name]
		if !serverConfig.Enabled {
			m.transition(name, StateDisabled, nil)
			continue
		}
		if err := m.connectWithRetry(ctx, name); err != nil {
			startErr = errors.Join(startErr, fmt.Errorf("mcp %s: %w", name, err))
		}
	}
	return startErr
}

func (m *Manager) Reconnect(ctx context.Context, name string) error {
	if m.isClosed() {
		return ErrManagerClosed
	}
	serverConfig, ok := m.config[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	if !serverConfig.Enabled {
		m.transition(name, StateDisabled, nil)
		return fmt.Errorf("mcp server %q is disabled", name)
	}
	m.closeClient(name)
	return m.connectWithRetry(ctx, name)
}

func (m *Manager) Refresh(ctx context.Context, name string) error {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrManagerClosed
	}
	current := m.servers[name]
	var client mcpcontract.Client
	var connection *connectionAttempt
	if current != nil {
		client = current.client
		connection = current.connection
	}
	m.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("mcp server %q is not connected", name)
	}
	m.transition(name, StateConnecting, nil)
	serverConfig := m.config[name]
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	drivers, diagnostics, err := m.importTools(callCtx, name, serverConfig, client)
	if err != nil {
		m.transition(name, StateDegraded, err)
		return err
	}
	m.mu.Lock()
	current = m.servers[name]
	if m.closed || current == nil || current.connection != connection {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	current.tools = drivers
	current.diagnostics = diagnostics
	m.mu.Unlock()
	m.transition(name, StateReady, nil)
	return nil
}

// Snapshot returns a copy of the currently ready tool catalog. A caller keeps
// this slice for one agent turn; later refreshes never mutate it.
func (m *Manager) Snapshot() []tool.Driver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	drivers := make([]tool.Driver, 0)
	for _, current := range m.servers {
		if current.state == StateReady {
			drivers = append(drivers, current.tools...)
		}
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Definition().Name < drivers[j].Definition().Name })
	return append([]tool.Driver(nil), drivers...)
}

func (m *Manager) Servers() []ServerSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ServerSnapshot, 0, len(names))
	for _, name := range names {
		current := m.servers[name]
		tools := make([]ToolSnapshot, 0, len(current.tools))
		for _, driver := range current.tools {
			definition := driver.Definition()
			toolName := strings.TrimPrefix(definition.Name, "mcp__"+name+"__")
			tools = append(tools, ToolSnapshot{
				Name: toolName, Description: definition.Description, Effect: string(definition.EffectType),
				RequiresApproval: definition.RequiresApproval || definition.Security.RequiresApproval,
			})
		}
		result = append(result, ServerSnapshot{
			Name: name, State: current.state, ToolCount: len(tools), Tools: tools,
			Diagnostics: append([]Diagnostic(nil), current.diagnostics...), LastError: current.lastError,
		})
	}
	return result
}

func (m *Manager) Close() error {
	return m.CloseContext(context.Background())
}

func (m *Manager) CloseContext(ctx context.Context) error {
	m.mu.Lock()
	var stopped []string
	if !m.closed {
		m.closed = true
		for name, current := range m.servers {
			if current.connection != nil {
				m.closing = append(m.closing, current.connection)
				current.client = nil
				current.connection = nil
			}
			current.tools = nil
			current.state = StateStopped
			stopped = append(stopped, name)
		}
		for name, attempt := range m.attempts {
			m.closing = append(m.closing, attempt)
			delete(m.attempts, name)
		}
	}
	closing := append([]*connectionAttempt(nil), m.closing...)
	m.mu.Unlock()
	for _, name := range stopped {
		m.transition(name, StateStopped, nil)
	}
	for _, attempt := range closing {
		attempt.close()
	}
	var closeErr error
	for _, attempt := range closing {
		closeErr = errors.Join(closeErr, attempt.waitClosed(ctx))
	}
	return closeErr
}

func (m *Manager) connectWithRetry(ctx context.Context, name string) error {
	delays := []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.transition(name, StateConnecting, nil)
		if err := m.connectOnce(ctx, name); err == nil {
			return nil
		} else {
			lastErr = err
			if errors.Is(err, ErrManagerClosed) {
				return err
			}
			m.transition(name, StateDegraded, err)
		}
		if attempt < len(delays) {
			if err := m.sleep(ctx, delays[attempt]); err != nil {
				return err
			}
		}
	}
	return lastErr
}

func (m *Manager) connectOnce(ctx context.Context, name string) error {
	serverConfig := m.config[name]
	environment, err := m.resolveMap(ctx, serverConfig.Env)
	if err != nil {
		return fmt.Errorf("resolve environment: %w", err)
	}
	headerValues, err := m.resolveMap(ctx, serverConfig.Headers)
	if err != nil {
		return fmt.Errorf("resolve headers: %w", err)
	}
	headers := make(http.Header, len(headerValues))
	for key, value := range headerValues {
		headers.Set(key, value)
	}
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout(serverConfig))
	defer cancel()
	attempt := newDialAttempt(cancel)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		attempt.finishDial(nil)
		attempt.close()
		return ErrManagerClosed
	}
	previous := m.attempts[name]
	m.attempts[name] = attempt
	if previous != nil {
		m.closing = append(m.closing, previous)
	}
	m.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	published := false
	defer func() {
		if !published {
			m.mu.Lock()
			if m.attempts[name] == attempt {
				delete(m.attempts, name)
			}
			m.mu.Unlock()
			attempt.close()
		}
	}()
	client, err := m.dial(connectCtx, name, serverConfig, environment, headers)
	attempt.finishDial(client)
	if err != nil {
		return err
	}
	if _, err := client.Initialize(connectCtx, "azem", m.version); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	drivers, diagnostics, err := m.importTools(connectCtx, name, serverConfig, client)
	if err != nil {
		return fmt.Errorf("import tools: %w", err)
	}
	m.mu.Lock()
	current := m.servers[name]
	if m.closed || m.attempts[name] != attempt {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	old := current.connection
	current.client = client
	current.connection = attempt
	current.tools = drivers
	current.diagnostics = diagnostics
	current.lastError = ""
	delete(m.attempts, name)
	if old != nil {
		m.closing = append(m.closing, old)
	}
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	published = true
	m.transition(name, StateReady, nil)
	return nil
}

func (m *Manager) importTools(ctx context.Context, name string, serverConfig config.MCPServerConfig, client mcpcontract.Client) ([]tool.Driver, []Diagnostic, error) {
	imported, err := kit.ImportMCPTools(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]bool, len(imported))
	drivers := make([]tool.Driver, 0, len(imported))
	diagnostics := make([]Diagnostic, 0)
	for _, remote := range imported {
		definition := remote.Definition()
		original := strings.TrimSpace(definition.Name)
		normalized := normalizeToolName(original)
		if original == "" || normalized == "" {
			diagnostics = append(diagnostics, Diagnostic{Server: name, Tool: original, Error: "tool name is empty after normalization"})
			continue
		}
		visible := "mcp__" + name + "__" + normalized
		if seen[visible] {
			diagnostics = append(diagnostics, Diagnostic{Server: name, Tool: original, Error: "normalized tool name conflicts with another tool"})
			continue
		}
		if err := validateSchema(definition.InputSchema, 0); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Server: name, Tool: original, Error: "invalid input schema: " + err.Error()})
			continue
		}
		seen[visible] = true
		override, overridden := serverConfig.ToolOverrides[original]
		definition.Name = visible
		definition.Origin = "mcp:" + name
		definition.EffectType = tool.EffectExternalSideEffect
		definition.RequiresApproval = true
		definition.RequiresActionTask = true
		definition.RiskLevel = "high"
		definition.Security.RequiresApproval = true
		definition.Security.RiskLevel = "high"
		definition.Idempotent = false
		definition.Timeout = callTimeout(serverConfig)
		if overridden {
			definition.EffectType = tool.EffectType(override.Effect)
			definition.RequiresApproval = override.Approval != "never"
			definition.Security.RequiresApproval = definition.RequiresApproval
			if definition.EffectType == tool.EffectReadOnly {
				definition.RequiresActionTask = false
				definition.RiskLevel = "low"
				definition.Security.RiskLevel = "low"
			}
		}
		drivers = append(drivers, &remoteDriver{
			manager: m, server: name, inner: remote, original: original, definition: definition,
			semaphore: make(chan struct{}, max(1, serverConfig.MaxConcurrency)), timeout: callTimeout(serverConfig),
		})
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Definition().Name < drivers[j].Definition().Name })
	return drivers, diagnostics, nil
}

func (m *Manager) resolveMap(ctx context.Context, references map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(references))
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := m.resolve(ctx, references[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		resolved[key] = value
	}
	return resolved, nil
}

func (m *Manager) transition(name string, state State, cause error) {
	m.mu.Lock()
	current := m.servers[name]
	if current == nil {
		m.mu.Unlock()
		return
	}
	if m.closed && state != StateStopped {
		m.mu.Unlock()
		return
	}
	current.state = state
	if cause != nil {
		current.lastError = cause.Error()
	} else if state == StateReady {
		current.lastError = ""
	}
	sink := m.sink
	event := Event{Server: name, State: state, At: time.Now().UTC()}
	if cause != nil {
		event.Error = cause.Error()
	}
	m.mu.Unlock()
	if sink != nil {
		sink(event)
	}
}

func (m *Manager) degrade(name string, cause error) {
	m.transition(name, StateDegraded, cause)
}

func (m *Manager) closeClient(name string) {
	m.mu.Lock()
	current := m.servers[name]
	var connection *connectionAttempt
	if current != nil {
		connection = current.connection
		current.client = nil
		current.connection = nil
		current.tools = nil
		if connection != nil {
			m.closing = append(m.closing, connection)
		}
	}
	m.mu.Unlock()
	if connection != nil {
		connection.close()
	}
}

func (m *Manager) names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.config))
	for name := range m.config {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) isClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

type remoteDriver struct {
	manager    *Manager
	server     string
	inner      tool.Driver
	original   string
	definition tool.Definition
	semaphore  chan struct{}
	timeout    time.Duration
}

func (d *remoteDriver) Definition() tool.Definition { return d.definition }

func (d *remoteDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	select {
	case d.semaphore <- struct{}{}:
		defer func() { <-d.semaphore }()
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
	callCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	remoteCall := call
	remoteCall.Name = d.original
	result, err := d.inner.Execute(callCtx, remoteCall, sink)
	result.Name = d.definition.Name
	if err != nil {
		d.manager.degrade(d.server, err)
		return result, err
	}
	return boundMCPModelOutput(result), nil
}

func boundMCPModelOutput(result tool.Result) tool.Result {
	contentBytes, structuredBytes := len(result.Content), len(result.Structured)
	if contentBytes > maxMCPModelOutputBytes ||
		structuredBytes > maxMCPModelOutputBytes ||
		contentBytes > maxMCPModelOutputBytes-structuredBytes {
		return mcpOutputLimitError(result, contentBytes+structuredBytes)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return mcpOutputEncodingError(result)
	}
	if len(encoded) > maxMCPModelOutputBytes {
		return mcpOutputLimitError(result, len(encoded))
	}
	return result
}

func mcpOutputLimitError(result tool.Result, received int) tool.Result {
	return tool.Result{
		ToolCallID: result.ToolCallID,
		Name:       result.Name,
		Content: fmt.Sprintf(
			"MCP result exceeded the model-context output limit (received %d bytes, limit %d bytes). Narrow the query or request a smaller range.",
			received,
			maxMCPModelOutputBytes,
		),
		IsError: true,
	}
}

func mcpOutputEncodingError(result tool.Result) tool.Result {
	return tool.Result{
		ToolCallID: result.ToolCallID,
		Name:       result.Name,
		Content:    "MCP result could not be encoded for model context. Narrow the query or request a different result format.",
		IsError:    true,
	}
}

func defaultDial(ctx context.Context, name string, serverConfig config.MCPServerConfig, environment map[string]string, headers http.Header, elicitation func(context.Context, string, mcpcontract.Elicitation) (mcpcontract.ElicitationResult, error)) (mcpcontract.Client, error) {
	clientOptions := mcpclient.Options{}
	if elicitation != nil {
		clientOptions.ElicitationHandler = func(handlerCtx context.Context, request mcpcontract.Elicitation) (mcpcontract.ElicitationResult, error) {
			return elicitation(handlerCtx, name, request)
		}
	}
	switch serverConfig.Transport {
	case "stdio":
		keys := make([]string, 0, len(environment))
		for key := range environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		env := make([]string, 0, len(keys))
		for _, key := range keys {
			env = append(env, key+"="+environment[key])
		}
		return mcpclient.DialStdioWithOptions(ctx, mcpclient.StdioConfig{
			Command: serverConfig.Command, Args: append([]string(nil), serverConfig.Args...),
			Dir: serverConfig.CWD, Env: env, InheritEnv: serverConfig.InheritEnv,
		}, clientOptions)
	case "streamable_http":
		return mcpclient.NewWithOptions(mcpclient.NewHTTPTransport(serverConfig.URL, headers), clientOptions), nil
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", serverConfig.Transport)
	}
}

func normalizeToolName(name string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(name) {
		valid := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		if valid && r <= unicode.MaxASCII {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func validateSchema(schema message.JSONSchema, depth int) error {
	if depth > 32 {
		return fmt.Errorf("schema nesting exceeds 32 levels")
	}
	switch schema.Type {
	case "", "object", "array", "string", "number", "integer", "boolean", "null":
	default:
		return fmt.Errorf("unsupported type %q", schema.Type)
	}
	if schema.Type == "array" && schema.Items == nil {
		return fmt.Errorf("array schema is missing items")
	}
	if schema.Items != nil {
		if err := validateSchema(*schema.Items, depth+1); err != nil {
			return err
		}
	}
	for property, child := range schema.Properties {
		if strings.TrimSpace(property) == "" {
			return fmt.Errorf("property name is empty")
		}
		if err := validateSchema(child, depth+1); err != nil {
			return fmt.Errorf("property %s: %w", property, err)
		}
	}
	for _, required := range schema.Required {
		if _, ok := schema.Properties[required]; !ok {
			return fmt.Errorf("required property %q is not defined", required)
		}
	}
	return nil
}

func connectTimeout(serverConfig config.MCPServerConfig) time.Duration {
	if serverConfig.ConnectDuration > 0 {
		return serverConfig.ConnectDuration
	}
	if parsed, err := time.ParseDuration(serverConfig.ConnectTimeout); err == nil && parsed > 0 {
		return parsed
	}
	return 30 * time.Second
}

func callTimeout(serverConfig config.MCPServerConfig) time.Duration {
	if serverConfig.CallDuration > 0 {
		return serverConfig.CallDuration
	}
	if parsed, err := time.ParseDuration(serverConfig.CallTimeout); err == nil && parsed > 0 {
		return parsed
	}
	return 60 * time.Second
}

func resolveEnvironmentReference(_ context.Context, reference string) (string, error) {
	return config.ResolveReference(reference, os.LookupEnv, nil)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
