package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

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

var (
	ErrManagerClosed = errors.New("mcp manager is closed")
	ErrServerRemoved = errors.New("mcp server was removed")
)

// maxMCPModelOutputBytes bounds one MCP result before it enters provider
// context, durable model history, or UI events. Unlike shell output, MCP
// output cannot be silently truncated because doing so could turn incomplete
// structured data into an apparently successful result.
const maxMCPModelOutputBytes = 256 << 10

const mcpTransportRejectedCode = -32005

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
type Notification struct {
	Server        string
	Kind          string
	URI           string
	Level         string
	Logger        string
	Message       string
	ProgressToken string
	Progress      float64
	Total         float64
	Data          any
}

type ResourceSnapshot struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
}
type ResourceTemplateSnapshot struct {
	Server      string `json:"server"`
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
}

type PromptSnapshot struct {
	Server      string           `json:"server"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type ToolSnapshot struct {
	Name             string
	Description      string
	Effect           string
	RequiresApproval bool
}

type ServerSnapshot struct {
	Name              string
	State             State
	ToolCount         int
	Tools             []ToolSnapshot
	Diagnostics       []Diagnostic
	Resources         []ResourceSnapshot
	Prompts           []PromptSnapshot
	ResourceTemplates []ResourceTemplateSnapshot
	LastError         string
}

type (
	SecretResolver func(context.Context, string) (string, error)
	DialFunc       func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (Client, error)
	SleepFunc      func(context.Context, time.Duration) error
)

type Options struct {
	Dial         DialFunc
	Sleep        SleepFunc
	Sink         func(Event)
	Elicitation  func(context.Context, string, Elicitation) (ElicitationResult, error)
	Notification func(Notification)
	OAuth        *OAuthBroker
}

type Manager struct {
	mu           sync.RWMutex
	config       map[string]config.MCPServerConfig
	version      string
	resolve      SecretResolver
	dial         DialFunc
	sleep        SleepFunc
	sink         func(Event)
	notification func(Notification)
	servers      map[string]*server
	oauth        *OAuthBroker
	attempts     map[string]*connectionAttempt
	closing      []*connectionAttempt
	closed       bool
}

type connectionAttempt struct {
	client Client
	cancel context.CancelFunc
	ready  chan struct{}
	once   sync.Once
	done   chan struct{}
	err    error
}

func newConnectionAttempt(client Client) *connectionAttempt {
	ready := make(chan struct{})
	close(ready)
	return &connectionAttempt{client: client, ready: ready, done: make(chan struct{})}
}

func newDialAttempt(cancel context.CancelFunc) *connectionAttempt {
	return &connectionAttempt{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
}

func (a *connectionAttempt) finishDial(client Client) {
	if isNilMCPClient(client) {
		client = nil
	}
	a.client = client
	close(a.ready)
}

func (a *connectionAttempt) close() {
	a.once.Do(func() {
		go func() {
			select {
			case <-a.ready:
				if a.client != nil {
					a.err = a.client.Close()
				}
				if a.cancel != nil {
					a.cancel()
				}
			default:
				if a.cancel != nil {
					a.cancel()
				}
				<-a.ready
				if a.client != nil {
					a.err = a.client.Close()
				}
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
	client      Client
	connection  *connectionAttempt
	tools       []tool.Driver
	diagnostics []Diagnostic
	resources   []ResourceSnapshot
	prompts     []PromptSnapshot
	templates   []ResourceTemplateSnapshot
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
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	manager := &Manager{
		config: copied, version: version, resolve: resolve, sleep: options.Sleep, sink: options.Sink,
		notification: options.Notification, oauth: options.OAuth,
		servers: states, attempts: make(map[string]*connectionAttempt),
	}
	if options.Dial != nil {
		manager.dial = options.Dial
	} else {
		manager.dial = func(ctx context.Context, name string, serverConfig config.MCPServerConfig, environment map[string]string, headers http.Header) (Client, error) {
			return defaultDial(ctx, name, serverConfig, environment, headers, options.Elicitation, func(handlerCtx context.Context, notification ProtocolNotification) {
				manager.handleNotification(handlerCtx, name, notification)
			})
		}
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) error {
	if m.isClosed() {
		return ErrManagerClosed
	}
	names := m.names()
	var startErr error
	for _, name := range names {
		serverConfig, ok := m.serverConfig(name)
		if !ok {
			continue
		}
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
	serverConfig, ok := m.serverConfig(name)
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

// Authenticate completes OAuth for one remote server and reconnects it.
func (m *Manager) Authenticate(ctx context.Context, name string, openURL func(string) error) error {
	serverConfig, ok := m.serverConfig(strings.TrimSpace(name))
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	if m.oauth == nil {
		return errors.New("MCP OAuth broker is unavailable")
	}
	if err := m.oauth.Authenticate(ctx, name, serverConfig, openURL); err != nil {
		return err
	}
	return m.Reconnect(ctx, name)
}

func (m *Manager) Unauthenticate(ctx context.Context, name string) error {
	serverConfig, ok := m.serverConfig(strings.TrimSpace(name))
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	if m.oauth == nil {
		return errors.New("MCP OAuth broker is unavailable")
	}
	if err := m.oauth.Unauthenticate(ctx, name, serverConfig); err != nil {
		return err
	}
	m.closeClient(name)
	m.transition(name, StateDegraded, errors.New("MCP OAuth authorization removed"))
	return nil
}

// Remove drops one server from future tool snapshots and begins closing any active or in-flight connection.
func (m *Manager) Remove(name string) error {
	name = strings.TrimSpace(name)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	_, ok := m.config[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("mcp server %q not found", name)
	}
	current := m.servers[name]
	var connection *connectionAttempt
	if current != nil {
		connection = current.connection
	}
	attempt := m.attempts[name]
	delete(m.config, name)
	delete(m.servers, name)
	delete(m.attempts, name)
	if connection != nil {
		m.closing = append(m.closing, connection)
	}
	if attempt != nil && attempt != connection {
		m.closing = append(m.closing, attempt)
	}
	sink := m.sink
	m.mu.Unlock()

	if connection != nil {
		connection.close()
	}
	if attempt != nil && attempt != connection {
		attempt.close()
	}
	if sink != nil {
		sink(Event{Server: name, State: StateStopped, At: time.Now().UTC()})
	}
	return nil
}

// Configure installs or replaces one server definition without replacing the
// manager pointer held by active provider runtimes. Enabled servers enter the
// connecting state; the caller owns starting Reconnect so UI mutations remain
// responsive while network/process startup runs in the background.
func (m *Manager) Configure(name string, serverConfig config.MCPServerConfig) (config.MCPServerConfig, error) {
	name = strings.TrimSpace(name)
	normalized, err := config.NormalizeMCPServer(name, serverConfig)
	if err != nil {
		return config.MCPServerConfig{}, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return config.MCPServerConfig{}, ErrManagerClosed
	}
	current := m.servers[name]
	if current == nil {
		current = &server{state: StateDisabled}
		m.servers[name] = current
	}
	connection := current.connection
	current.client = nil
	current.connection = nil
	current.tools = nil
	current.diagnostics = nil
	if connection != nil {
		m.closing = append(m.closing, connection)
	}
	attempt := m.attempts[name]
	if attempt != nil {
		delete(m.attempts, name)
		m.closing = append(m.closing, attempt)
	}
	m.config[name] = normalized
	m.mu.Unlock()

	if connection != nil {
		connection.close()
	}
	if attempt != nil && attempt != connection {
		attempt.close()
	}
	if normalized.Enabled {
		m.transition(name, StateConnecting, nil)
	} else {
		m.transition(name, StateDisabled, nil)
	}
	return normalized, nil
}

func (m *Manager) Refresh(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		if m.isClosed() {
			return ErrManagerClosed
		}
		var refreshErr error
		for _, snapshot := range m.Servers() {
			if snapshot.State != StateReady {
				continue
			}
			if err := m.Refresh(ctx, snapshot.Name); err != nil {
				refreshErr = errors.Join(refreshErr, fmt.Errorf("mcp %s: %w", snapshot.Name, err))
			}
		}
		return refreshErr
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrManagerClosed
	}
	current := m.servers[name]
	var client Client
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
	serverConfig, ok := m.serverConfig(name)
	if !ok {
		return ErrServerRemoved
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	drivers, diagnostics, err := m.importTools(callCtx, name, serverConfig, client)
	if err != nil {
		m.transition(name, StateDegraded, err)
		return err
	}
	resources, templates, prompts, featureDiagnostics := m.importFeatures(callCtx, name, client)
	diagnostics = append(diagnostics, featureDiagnostics...)
	m.mu.Lock()
	current = m.servers[name]
	if m.closed || current == nil || current.connection != connection {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	current.tools = drivers
	current.diagnostics = diagnostics
	current.resources = resources
	current.templates = templates
	current.prompts = prompts
	m.mu.Unlock()
	m.transition(name, StateReady, nil)
	return nil
}

func (m *Manager) Resources(serverName string) []ResourceSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if serverName != "" {
		current := m.servers[serverName]
		if current == nil {
			return nil
		}
		return append([]ResourceSnapshot(nil), current.resources...)
	}
	var result []ResourceSnapshot
	for _, current := range m.servers {
		result = append(result, current.resources...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Server != result[j].Server {
			return result[i].Server < result[j].Server
		}
		return result[i].URI < result[j].URI
	})
	return result
}

func (m *Manager) ResourceTemplates(serverName string) []ResourceTemplateSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if serverName != "" {
		current := m.servers[serverName]
		if current == nil {
			return nil
		}
		return append([]ResourceTemplateSnapshot(nil), current.templates...)
	}
	var result []ResourceTemplateSnapshot
	for _, current := range m.servers {
		result = append(result, current.templates...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Server != result[j].Server {
			return result[i].Server < result[j].Server
		}
		return result[i].URITemplate < result[j].URITemplate
	})
	return result
}

func (m *Manager) Prompts(serverName string) []PromptSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if serverName != "" {
		current := m.servers[serverName]
		if current == nil {
			return nil
		}
		return append([]PromptSnapshot(nil), current.prompts...)
	}
	var result []PromptSnapshot
	for _, current := range m.servers {
		result = append(result, current.prompts...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Server != result[j].Server {
			return result[i].Server < result[j].Server
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) ([]ResourceContent, error) {
	client, serverConfig, err := m.featureClient(serverName)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	content, err := client.ReadResource(callCtx, uri)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, item := range content {
		total += len(item.Text)
	}
	if total > maxMCPModelOutputBytes {
		return nil, fmt.Errorf("MCP resource exceeds %d bytes", maxMCPModelOutputBytes)
	}
	return content, nil
}

func (m *Manager) GetPrompt(ctx context.Context, serverName, name string, arguments map[string]string) ([]PromptMessage, error) {
	client, serverConfig, err := m.featureClient(serverName)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	messages, err := client.GetPrompt(callCtx, name, arguments)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, item := range messages {
		total += len(item.Content.Text)
	}
	if total > maxMCPModelOutputBytes {
		return nil, fmt.Errorf("MCP prompt exceeds %d bytes", maxMCPModelOutputBytes)
	}
	return messages, nil
}

func (m *Manager) SubscribeResource(ctx context.Context, serverName, uri string) error {
	client, serverConfig, err := m.featureClient(serverName)
	if err != nil {
		return err
	}
	subscriber, ok := client.(SubscriptionClient)
	if !ok {
		return fmt.Errorf("MCP server %q client does not support resource subscriptions", serverName)
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	return subscriber.SubscribeResource(callCtx, uri)
}

func (m *Manager) UnsubscribeResource(ctx context.Context, serverName, uri string) error {
	client, serverConfig, err := m.featureClient(serverName)
	if err != nil {
		return err
	}
	subscriber, ok := client.(SubscriptionClient)
	if !ok {
		return fmt.Errorf("MCP server %q client does not support resource subscriptions", serverName)
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout(serverConfig))
	defer cancel()
	return subscriber.UnsubscribeResource(callCtx, uri)
}

func (m *Manager) featureClient(serverName string) (Client, config.MCPServerConfig, error) {
	serverName = strings.TrimSpace(serverName)
	m.mu.RLock()
	current := m.servers[serverName]
	client := Client(nil)
	if current != nil {
		client = current.client
	}
	serverConfig, configured := m.config[serverName]
	m.mu.RUnlock()
	if !configured {
		return nil, config.MCPServerConfig{}, fmt.Errorf("mcp server %q not found", serverName)
	}
	if client == nil {
		return nil, config.MCPServerConfig{}, fmt.Errorf("mcp server %q is not connected", serverName)
	}
	return client, serverConfig, nil
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
			policy := agentruntime.ToolPolicy{
				Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			}
			if provider, ok := driver.(interface {
				ToolPolicy() agentruntime.ToolPolicy
			}); ok {
				policy = provider.ToolPolicy()
			}
			toolName := strings.TrimPrefix(definition.Name, "mcp__"+name+"__")
			tools = append(tools, ToolSnapshot{
				Name: toolName, Description: definition.Description, Effect: string(policy.Effect),
				RequiresApproval: policy.RequiresApproval,
			})
		}
		result = append(result, ServerSnapshot{
			Name: name, State: current.state, ToolCount: len(tools), Tools: tools,
			Diagnostics:       append([]Diagnostic(nil), current.diagnostics...),
			Resources:         append([]ResourceSnapshot(nil), current.resources...),
			ResourceTemplates: append([]ResourceTemplateSnapshot(nil), current.templates...),
			Prompts:           append([]PromptSnapshot(nil), current.prompts...),
			LastError:         current.lastError,
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
			if errors.Is(err, ErrManagerClosed) || errors.Is(err, ErrServerRemoved) {
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
	serverConfig, ok := m.serverConfig(name)
	if !ok {
		return ErrServerRemoved
	}
	environment, err := m.resolveMap(ctx, serverConfig.Env)
	if err != nil {
		return fmt.Errorf("resolve environment: %w", err)
	}
	for key, value := range serverConfig.RuntimeEnv {
		environment[key] = value
	}
	headerValues, err := m.resolveMap(ctx, serverConfig.Headers)
	if err != nil {
		return fmt.Errorf("resolve headers: %w", err)
	}
	if headerValues["Authorization"] == "" && m.oauth != nil {
		authorization, authErr := m.oauth.AuthorizationHeader(ctx, name, serverConfig)
		if authErr != nil {
			return fmt.Errorf("resolve MCP OAuth credential: %w", authErr)
		}
		if authorization != "" {
			headerValues["Authorization"] = authorization
		}
	}
	for key, value := range serverConfig.RuntimeHeaders {
		headerValues[key] = value
	}
	headers := make(http.Header, len(headerValues))
	for key, value := range headerValues {
		headers.Set(key, value)
	}
	lifetimeCtx, lifetimeCancel := context.WithCancel(ctx)
	connectCtx, connectCancel := context.WithTimeout(lifetimeCtx, connectTimeout(serverConfig))
	defer connectCancel()
	attempt := newDialAttempt(lifetimeCancel)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		attempt.finishDial(nil)
		attempt.close()
		return ErrManagerClosed
	}
	if _, exists := m.config[name]; !exists {
		m.mu.Unlock()
		attempt.finishDial(nil)
		attempt.close()
		return ErrServerRemoved
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
	dialCtx := connectCtx
	if serverConfig.Transport == "stdio" {
		dialCtx = lifetimeCtx
	}
	client, err := m.dial(dialCtx, name, serverConfig, environment, headers)
	attempt.finishDial(client)
	if err != nil {
		return err
	}
	if isNilMCPClient(client) {
		return errors.New("MCP dial returned a nil client")
	}
	if _, err := client.Initialize(connectCtx, "azem", m.version); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	drivers, diagnostics, err := m.importTools(connectCtx, name, serverConfig, client)
	if err != nil {
		return fmt.Errorf("import tools: %w", err)
	}
	resources, templates, prompts, featureDiagnostics := m.importFeatures(connectCtx, name, client)
	diagnostics = append(diagnostics, featureDiagnostics...)
	m.mu.Lock()
	current := m.servers[name]
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	if current == nil || m.attempts[name] != attempt {
		m.mu.Unlock()
		return ErrServerRemoved
	}
	old := current.connection
	current.client = client
	current.connection = attempt
	current.tools = drivers
	current.diagnostics = diagnostics

	current.resources = resources
	current.templates = templates
	current.prompts = prompts
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

func isNilMCPClient(client Client) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (m *Manager) importTools(ctx context.Context, name string, serverConfig config.MCPServerConfig, client Client) ([]tool.Driver, []Diagnostic, error) {
	imported, err := importMCPTools(ctx, client)
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
		policy := agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high", Origin: "mcp:" + name,
			Concurrency: tool.ConcurrencyParallel, ConcurrencyGroup: "mcp:" + name,
			MaxConcurrency: max(1, serverConfig.MaxConcurrency),
		}
		definition.Name = visible
		definition.Timeout = callTimeout(serverConfig)
		definition.Concurrency = tool.ConcurrencyParallel
		definition.ConcurrencyGroup = policy.ConcurrencyGroup
		definition.MaxConcurrency = policy.MaxConcurrency
		if override, overridden := serverConfig.ToolOverrides[original]; overridden {
			policy.Effect = agentruntime.ToolEffectType(override.Effect)
			policy.RequiresApproval = override.Approval != "never"
			if policy.Effect == agentruntime.ToolEffectReadOnly {
				policy.RequiresActionTask = false
				policy.RiskLevel = "low"
			}
		}
		drivers = append(drivers, &remoteDriver{
			manager: m, server: name, inner: remote, original: original, definition: definition,
			policy: policy, semaphore: make(chan struct{}, policy.MaxConcurrency), timeout: callTimeout(serverConfig),
		})
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Definition().Name < drivers[j].Definition().Name })
	return drivers, diagnostics, nil
}

func (m *Manager) importFeatures(ctx context.Context, name string, client Client) ([]ResourceSnapshot, []ResourceTemplateSnapshot, []PromptSnapshot, []Diagnostic) {
	var diagnostics []Diagnostic
	var resources []ResourceSnapshot
	listedResources, err := client.ListResources(ctx)
	if err != nil {
		diagnostics = append(diagnostics, Diagnostic{Server: name, Error: "list resources: " + err.Error()})
	} else {
		for _, resource := range listedResources {
			resources = append(resources, ResourceSnapshot{
				Server: name, URI: resource.URI, Name: resource.Name, Description: resource.Description, MediaType: resource.MimeType,
			})
		}
		sort.Slice(resources, func(i, j int) bool { return resources[i].URI < resources[j].URI })
	}
	var templates []ResourceTemplateSnapshot
	if templateClient, ok := client.(ResourceTemplateClient); ok {
		listedTemplates, templateErr := templateClient.ListResourceTemplates(ctx)
		if templateErr != nil {
			diagnostics = append(diagnostics, Diagnostic{Server: name, Error: "list resource templates: " + templateErr.Error()})
		} else {
			for _, template := range listedTemplates {
				templates = append(templates, ResourceTemplateSnapshot{
					Server: name, URITemplate: template.URITemplate, Name: template.Name,
					Description: template.Description, MediaType: template.MimeType,
				})
			}
			sort.Slice(templates, func(i, j int) bool { return templates[i].URITemplate < templates[j].URITemplate })
		}
	}
	var prompts []PromptSnapshot
	listedPrompts, err := client.ListPrompts(ctx)
	if err != nil {
		diagnostics = append(diagnostics, Diagnostic{Server: name, Error: "list prompts: " + err.Error()})
	} else {
		for _, prompt := range listedPrompts {
			prompts = append(prompts, PromptSnapshot{
				Server: name, Name: prompt.Name, Description: prompt.Description,
				Arguments: append([]PromptArgument(nil), prompt.Arguments...),
			})
		}
		sort.Slice(prompts, func(i, j int) bool { return prompts[i].Name < prompts[j].Name })
	}
	return resources, templates, prompts, diagnostics
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
	} else if state == StateReady || state == StateDisabled {
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

func (m *Manager) handleNotification(ctx context.Context, serverName string, incoming ProtocolNotification) {
	notification := Notification{
		Server: serverName, Kind: incoming.Kind, URI: incoming.URI, Level: incoming.Level,
		Logger: incoming.Logger, Message: incoming.Message, ProgressToken: incoming.ProgressToken,
		Progress: incoming.Progress, Total: incoming.Total, Data: incoming.Data,
	}
	if sink := m.notification; sink != nil {
		sink(notification)
	}
	switch incoming.Kind {
	case "tools/list_changed", "prompts/list_changed", "resources/list_changed":
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = m.Refresh(refreshCtx, serverName)
		}()
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
		current.resources = nil
		current.prompts = nil
		current.templates = nil
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

func (m *Manager) serverConfig(name string) (config.MCPServerConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	serverConfig, ok := m.config[name]
	return serverConfig, ok
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
	policy     agentruntime.ToolPolicy
	semaphore  chan struct{}
	timeout    time.Duration
}

func (d *remoteDriver) Definition() tool.Definition { return d.definition }

func (d *remoteDriver) ToolPolicy() agentruntime.ToolPolicy { return d.policy.Clone() }

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
	if err == nil {
		return boundMCPModelOutput(result), nil
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == mcpTransportRejectedCode {
		return boundMCPModelOutput(tool.Result{
			ToolCallID: call.ID,
			Name:       d.definition.Name,
			Content:    fmt.Sprintf("jsonrpc error %d: %s. The MCP transport did not accept this request. The call was not replayed automatically.", rpcErr.Code, rpcErr.Message),
			IsError:    true,
		}), nil
	}
	d.manager.degrade(d.server, err)
	return result, err
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

func defaultDial(_ context.Context, name string, serverConfig config.MCPServerConfig, environment map[string]string, headers http.Header, elicitation func(context.Context, string, Elicitation) (ElicitationResult, error), notification NotificationHandler) (Client, error) {
	options := &sdkmcp.ClientOptions{}
	if elicitation != nil {
		options.ElicitationHandler = func(handlerCtx context.Context, request *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
			if request == nil || request.Params == nil {
				return nil, errors.New("MCP elicitation request is empty")
			}
			result, err := elicitation(handlerCtx, name, Elicitation{
				Mode: request.Params.Mode, Message: request.Params.Message, URL: request.Params.URL,
				ElicitationID: request.Params.ElicitationID, RequestedSchema: request.Params.RequestedSchema,
			})
			if err != nil {
				return nil, err
			}
			return &sdkmcp.ElicitResult{Action: result.Action, Content: result.Content}, nil
		}
	}
	if notification != nil {
		options.ToolListChangedHandler = func(ctx context.Context, _ *sdkmcp.ToolListChangedRequest) {
			notification(ctx, ProtocolNotification{Kind: "tools/list_changed"})
		}
		options.PromptListChangedHandler = func(ctx context.Context, _ *sdkmcp.PromptListChangedRequest) {
			notification(ctx, ProtocolNotification{Kind: "prompts/list_changed"})
		}
		options.ResourceListChangedHandler = func(ctx context.Context, _ *sdkmcp.ResourceListChangedRequest) {
			notification(ctx, ProtocolNotification{Kind: "resources/list_changed"})
		}
		options.ResourceUpdatedHandler = func(ctx context.Context, request *sdkmcp.ResourceUpdatedNotificationRequest) {
			if request != nil && request.Params != nil {
				notification(ctx, ProtocolNotification{Kind: "resources/updated", URI: request.Params.URI})
			}
		}
		options.LoggingMessageHandler = func(ctx context.Context, request *sdkmcp.LoggingMessageRequest) {
			if request == nil || request.Params == nil {
				return
			}
			message := fmt.Sprint(request.Params.Data)
			if raw, err := json.Marshal(request.Params.Data); err == nil {
				message = string(raw)
			}
			notification(ctx, ProtocolNotification{
				Kind: "logging/message", Level: string(request.Params.Level), Logger: request.Params.Logger,
				Message: message, Data: request.Params.Data,
			})
		}
		options.ProgressNotificationHandler = func(ctx context.Context, request *sdkmcp.ProgressNotificationClientRequest) {
			if request != nil && request.Params != nil {
				notification(ctx, ProtocolNotification{
					Kind: "progress", ProgressToken: fmt.Sprint(request.Params.ProgressToken),
					Message: request.Params.Message, Progress: request.Params.Progress, Total: request.Params.Total,
				})
			}
		}
	}
	switch serverConfig.Transport {
	case "stdio":
		return newSDKClient(sdkCommandTransport(
			serverConfig.Command, append([]string(nil), serverConfig.Args...), serverConfig.CWD,
			environment, serverConfig.InheritEnv,
		), options), nil
	case "streamable_http":
		base := http.DefaultTransport
		if base == nil {
			base = &http.Transport{}
		}
		client := &http.Client{Transport: headerTransport{base: base, headers: headers.Clone()}}
		return newSDKClient(&sdkmcp.StreamableClientTransport{
			Endpoint: serverConfig.URL, HTTPClient: client, MaxRetries: -1,
		}, options), nil
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
