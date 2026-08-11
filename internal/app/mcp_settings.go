package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Viking602/azem/internal/config"
)

type mcpServerMutation struct {
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Transport      string            `json:"transport"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	InheritEnv     bool              `json:"inheritEnv"`
	Env            map[string]string `json:"env,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ConnectTimeout string            `json:"connectTimeout,omitempty"`
	CallTimeout    string            `json:"callTimeout,omitempty"`
	MaxConcurrency int               `json:"maxConcurrency,omitempty"`
	Approval       string            `json:"approval,omitempty"`
}

func (s *Service) upsertMCPServer(ctx context.Context, payload json.RawMessage) error {
	var mutation mcpServerMutation
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mutation); err != nil {
		return fmt.Errorf("decode MCP server: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode MCP server: trailing data")
	}
	name := strings.TrimSpace(mutation.Name)
	server := config.MCPServerConfig{
		Enabled: mutation.Enabled, Transport: strings.TrimSpace(mutation.Transport),
		Command: strings.TrimSpace(mutation.Command), Args: append([]string(nil), mutation.Args...),
		CWD: strings.TrimSpace(mutation.CWD), InheritEnv: mutation.InheritEnv,
		Env: cloneMCPStringMap(mutation.Env), URL: strings.TrimSpace(mutation.URL), Headers: cloneMCPStringMap(mutation.Headers),
		ConnectTimeout: strings.TrimSpace(mutation.ConnectTimeout), CallTimeout: strings.TrimSpace(mutation.CallTimeout),
		MaxConcurrency: mutation.MaxConcurrency, Approval: strings.TrimSpace(mutation.Approval),
	}
	return s.applyMCPServer(ctx, name, server)
}

func (s *Service) setMCPServerEnabled(ctx context.Context, name string, enabled bool) error {
	name = strings.TrimSpace(name)
	server, ok := s.cfg.MCP.Servers[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	server.Enabled = enabled
	return s.applyMCPServer(ctx, name, server)
}

func (s *Service) applyMCPServer(ctx context.Context, name string, server config.MCPServerConfig) error {
	if s.mcp == nil {
		return fmt.Errorf("no MCP manager is attached")
	}
	normalized, err := config.NormalizeMCPServer(name, server)
	if err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			var updateErr error
			normalized, updateErr = config.UpdateMCPServer(s.configPath, name, normalized)
			return updateErr
		}); err != nil {
			return err
		}
	}
	normalized, err = s.mcp.Configure(name, normalized)
	if err != nil {
		return err
	}
	if s.cfg.MCP.Servers == nil {
		s.cfg.MCP.Servers = make(map[string]config.MCPServerConfig)
	}
	s.cfg.MCP.Servers[name] = normalized
	if err := s.emitMCPSnapshot(ctx); err != nil {
		return err
	}
	if normalized.Enabled {
		s.startMCPReconnect(name)
	}
	return nil
}

func (s *Service) startMCPReconnect(name string) {
	s.mu.Lock()
	if s.shuttingDown || s.mcp == nil {
		s.mu.Unlock()
		return
	}
	manager := s.mcp
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		_ = manager.Reconnect(s.ctx, name)
		_ = s.emitMCPSnapshot(s.ctx)
	}()
}

func cloneMCPStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return cloned
}
