package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	venatskill "github.com/Viking602/venat/skill"
)

func buildResourceRouter(sessions *session.Service, skillCatalog *skills.Catalog, ruleCatalog *rules.Catalog) (*resource.Router, error) {
	router := resource.NewRouter(resource.DefaultMaxReadBytes)
	if err := router.Register("artifact", artifactResourceHandler{sessions: sessions}); err != nil {
		return nil, err
	}
	if err := router.Register("skill", skillResourceHandler{catalog: skillCatalog}); err != nil {
		return nil, err
	}
	if err := router.Register("rule", ruleResourceHandler{catalog: ruleCatalog}); err != nil {
		return nil, err
	}
	return router, nil
}

type artifactResourceHandler struct {
	sessions *session.Service
}

func (handler artifactResourceHandler) Read(ctx context.Context, request resource.Request) (resource.Result, error) {
	if handler.sessions == nil || strings.TrimSpace(request.Scope.SessionID) == "" {
		return resource.Result{}, errors.New("artifact resource requires a current session")
	}
	id := strings.Trim(strings.TrimSpace(request.URI.Opaque), "/")
	if id == "" || strings.Contains(id, "/") {
		return resource.Result{}, fmt.Errorf("invalid artifact resource id %q", id)
	}
	artifact, err := handler.sessions.LoadArtifact(ctx, request.Scope.SessionID, id)
	if err != nil {
		return resource.Result{}, err
	}
	return resource.Result{
		URI: request.URI.Raw, MediaType: detectedResourceMediaType("", artifact.Payload),
		Data: artifact.Payload,
		Metadata: map[string]string{
			"id": artifact.ID, "session": artifact.SessionID, "run": artifact.RunID,
			"kind": artifact.Kind, "sha256": artifact.SHA256,
		},
	}, nil
}

type skillResourceHandler struct {
	catalog *skills.Catalog
}

func (handler skillResourceHandler) Read(_ context.Context, request resource.Request) (resource.Result, error) {
	name, resourcePath, found := strings.Cut(strings.Trim(request.URI.Opaque, "/"), "/")
	if !found || name == "" || resourcePath == "" {
		return resource.Result{}, errors.New("skill resource URI requires skill name and resource path")
	}
	if !request.Scope.ActiveSkills[name] {
		return resource.Result{}, fmt.Errorf("skill %q is not active", name)
	}
	if handler.catalog == nil {
		return resource.Result{}, errors.New("skill resources are unavailable")
	}
	snapshot := handler.catalog.Snapshot()
	current, ok := snapshot.Registry.Get(name)
	if !ok {
		return resource.Result{}, fmt.Errorf("skill %q is unavailable", name)
	}
	payload, err := venatskill.ReadResource(current, resourcePath)
	if err != nil {
		return resource.Result{}, err
	}
	return resource.Result{
		URI: request.URI.Raw, MediaType: detectedResourceMediaType(resourcePath, payload), Data: payload,
		Metadata: map[string]string{"skill": name, "path": resourcePath},
	}, nil
}

type ruleResourceHandler struct {
	catalog *rules.Catalog
}

func (handler ruleResourceHandler) Read(_ context.Context, request resource.Request) (resource.Result, error) {
	name := strings.Trim(strings.TrimSpace(request.URI.Opaque), "/")
	if name == "" || strings.Contains(name, "/") {
		return resource.Result{}, errors.New("rule resource URI requires one rule name")
	}
	rule, ok := handler.catalog.Get(name)
	if !ok {
		available := strings.Join(handler.catalog.Names(), ", ")
		if available == "" {
			available = "none"
		}
		return resource.Result{}, fmt.Errorf("rule %q is unavailable; available rules: %s", name, available)
	}
	return resource.Result{
		URI: request.URI.Raw, MediaType: "text/markdown; charset=utf-8", Data: []byte(rule.Content),
		Metadata: map[string]string{"rule": rule.Name, "path": rule.Path, "provider": rule.Provider},
	}, nil
}

type mcpResourceHandler struct {
	manager *mcpruntime.Manager
}

func (handler mcpResourceHandler) Read(ctx context.Context, request resource.Request) (resource.Result, error) {
	if handler.manager == nil {
		return resource.Result{}, errors.New("MCP resources are unavailable")
	}
	serverName, target, found := strings.Cut(strings.Trim(request.URI.Opaque, "/"), "/")
	if serverName == "" {
		return resource.Result{}, errors.New("mcp resource URI requires a server name")
	}
	if !found || target == "" {
		payload, err := json.Marshal(struct {
			Resources []mcpruntime.ResourceSnapshot         `json:"resources"`
			Templates []mcpruntime.ResourceTemplateSnapshot `json:"resourceTemplates"`
			Prompts   []mcpruntime.PromptSnapshot           `json:"prompts"`
		}{
			Resources: handler.manager.Resources(serverName),
			Templates: handler.manager.ResourceTemplates(serverName),
			Prompts:   handler.manager.Prompts(serverName),
		})
		if err != nil {
			return resource.Result{}, err
		}
		return resource.Result{URI: request.URI.Raw, MediaType: "application/json", Data: payload, Metadata: map[string]string{"server": serverName}}, nil
	}
	contents, err := handler.manager.ReadResource(ctx, serverName, target)
	if err != nil {
		return resource.Result{}, err
	}
	var output strings.Builder
	mediaType := ""
	for index, content := range contents {
		if index > 0 {
			output.WriteString("\n\n")
		}
		if len(contents) > 1 {
			fmt.Fprintf(&output, "## %s\n\n", content.URI)
		}
		output.WriteString(content.Text)
		if index == 0 {
			mediaType = content.MimeType
		} else if mediaType != content.MimeType {
			mediaType = "text/plain; charset=utf-8"
		}
	}
	if mediaType == "" {
		mediaType = "text/plain; charset=utf-8"
	}
	return resource.Result{
		URI: request.URI.Raw, MediaType: mediaType, Data: []byte(output.String()),
		Metadata: map[string]string{"server": serverName, "resource": target},
	}, nil
}

func detectedResourceMediaType(name string, payload []byte) string {
	if extension := filepath.Ext(name); extension != "" {
		if mediaType := mime.TypeByExtension(extension); mediaType != "" {
			return mediaType
		}
	}
	if utf8.Valid(payload) {
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}
