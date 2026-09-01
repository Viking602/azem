package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/Viking602/venat/message"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type sdkClient struct {
	mu        sync.RWMutex
	transport sdkmcp.Transport
	options   *sdkmcp.ClientOptions
	session   *sdkmcp.ClientSession
}

func newSDKClient(transport sdkmcp.Transport, options *sdkmcp.ClientOptions) *sdkClient {
	return &sdkClient{transport: transport, options: options}
}

func (c *sdkClient) Initialize(ctx context.Context, name, version string) (InitializeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return sdkInitializeResult(c.session.InitializeResult()), nil
	}
	if c.transport == nil {
		return InitializeResult{}, errors.New("MCP transport is nil")
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: name, Version: version}, c.options)
	session, err := client.Connect(ctx, c.transport, nil)
	if err != nil {
		return InitializeResult{}, err
	}
	c.session = session
	return sdkInitializeResult(session.InitializeResult()), nil
}

func sdkInitializeResult(result *sdkmcp.InitializeResult) InitializeResult {
	if result == nil {
		return InitializeResult{}
	}
	converted := InitializeResult{ProtocolVersion: result.ProtocolVersion}
	if result.ServerInfo != nil {
		converted.ServerInfo = ServerInfo{Name: result.ServerInfo.Name, Version: result.ServerInfo.Version}
	}
	if result.Capabilities != nil {
		if raw, err := json.Marshal(result.Capabilities); err == nil {
			_ = json.Unmarshal(raw, &converted.Capabilities)
		}
	}
	return converted
}

func (c *sdkClient) currentSession() (*sdkmcp.ClientSession, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.session == nil {
		return nil, errors.New("MCP client is not initialized")
	}
	return c.session, nil
}

func (c *sdkClient) ListTools(ctx context.Context) ([]message.ToolDefinition, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	var definitions []message.ToolDefinition
	for remote, iterErr := range session.Tools(ctx, nil) {
		if iterErr != nil {
			return nil, iterErr
		}
		if remote == nil {
			continue
		}
		schema, err := convertInputSchema(remote.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("MCP tool %q input schema: %w", remote.Name, err)
		}
		definitions = append(definitions, message.ToolDefinition{
			Name: remote.Name, Description: remote.Description, InputSchema: schema,
		})
	}
	return definitions, nil
}

func convertInputSchema(input any) (message.JSONSchema, error) {
	if input == nil {
		return message.JSONSchema{Type: "object"}, nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return message.JSONSchema{}, err
	}
	var schema message.JSONSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return message.JSONSchema{}, err
	}
	return schema, nil
}

func (c *sdkClient) CallTool(ctx context.Context, name string, arguments map[string]any) (CallToolResult, error) {
	session, err := c.currentSession()
	if err != nil {
		return CallToolResult{}, err
	}
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return CallToolResult{}, err
	}
	converted := CallToolResult{IsError: result.IsError, StructuredContent: result.StructuredContent}
	for _, content := range result.Content {
		if content == nil {
			continue
		}
		if text, ok := content.(*sdkmcp.TextContent); ok {
			converted.Content = append(converted.Content, ContentBlock{Type: "text", Text: text.Text})
			continue
		}
		raw, marshalErr := content.MarshalJSON()
		if marshalErr != nil {
			return CallToolResult{}, fmt.Errorf("encode MCP content: %w", marshalErr)
		}
		converted.Content = append(converted.Content, ContentBlock{Type: "json", Text: string(raw)})
	}
	return converted, nil
}

func (c *sdkClient) ListResources(ctx context.Context) ([]Resource, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	var resources []Resource
	for remote, iterErr := range session.Resources(ctx, nil) {
		if iterErr != nil {
			return nil, iterErr
		}
		if remote != nil {
			resources = append(resources, Resource{URI: remote.URI, Name: remote.Name, Description: remote.Description, MimeType: remote.MIMEType})
		}
	}
	return resources, nil
}

func (c *sdkClient) ReadResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	contents := make([]ResourceContent, 0, len(result.Contents))
	for _, content := range result.Contents {
		if content != nil {
			contents = append(contents, ResourceContent{URI: content.URI, MimeType: content.MIMEType, Text: content.Text})
		}
	}
	return contents, nil
}

func (c *sdkClient) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	var templates []ResourceTemplate
	for remote, iterErr := range session.ResourceTemplates(ctx, nil) {
		if iterErr != nil {
			return nil, iterErr
		}
		if remote != nil {
			templates = append(templates, ResourceTemplate{URITemplate: remote.URITemplate, Name: remote.Name, Description: remote.Description, MimeType: remote.MIMEType})
		}
	}
	return templates, nil
}

func (c *sdkClient) ListPrompts(ctx context.Context) ([]Prompt, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	var prompts []Prompt
	for remote, iterErr := range session.Prompts(ctx, nil) {
		if iterErr != nil {
			return nil, iterErr
		}
		if remote == nil {
			continue
		}
		prompt := Prompt{Name: remote.Name, Description: remote.Description, Arguments: make([]PromptArgument, 0, len(remote.Arguments))}
		for _, argument := range remote.Arguments {
			if argument != nil {
				prompt.Arguments = append(prompt.Arguments, PromptArgument{Name: argument.Name, Description: argument.Description, Required: argument.Required})
			}
		}
		prompts = append(prompts, prompt)
	}
	return prompts, nil
}

func (c *sdkClient) GetPrompt(ctx context.Context, name string, arguments map[string]string) ([]PromptMessage, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	result, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	messages := make([]PromptMessage, 0, len(result.Messages))
	for _, remote := range result.Messages {
		content := ContentBlock{Type: "text"}
		if text, ok := remote.Content.(*sdkmcp.TextContent); ok {
			content.Text = text.Text
		} else if remote.Content != nil {
			raw, marshalErr := remote.Content.MarshalJSON()
			if marshalErr != nil {
				return nil, fmt.Errorf("encode MCP prompt content: %w", marshalErr)
			}
			content.Type, content.Text = "json", string(raw)
		}
		messages = append(messages, PromptMessage{Role: string(remote.Role), Content: content})
	}
	return messages, nil
}

func (c *sdkClient) SubscribeResource(ctx context.Context, uri string) error {
	session, err := c.currentSession()
	if err != nil {
		return err
	}
	return session.Subscribe(ctx, &sdkmcp.SubscribeParams{URI: uri})
}

func (c *sdkClient) UnsubscribeResource(ctx context.Context, uri string) error {
	session, err := c.currentSession()
	if err != nil {
		return err
	}
	return session.Unsubscribe(ctx, &sdkmcp.UnsubscribeParams{URI: uri})
}

func (c *sdkClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for name, values := range t.headers {
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return t.base.RoundTrip(clone)
}

func sdkCommandTransport(command string, args []string, directory string, environment map[string]string, inherit bool) *sdkmcp.CommandTransport {
	process := exec.Command(command, args...)
	process.Dir = directory
	if inherit {
		merged := make(map[string]string, len(environment)+len(os.Environ()))
		for _, entry := range os.Environ() {
			key, value, found := strings.Cut(entry, "=")
			if found {
				merged[key] = value
			}
		}
		for key, value := range environment {
			merged[key] = value
		}
		process.Env = sortedEnvironment(merged)
	} else {
		process.Env = sortedEnvironment(environment)
	}
	return &sdkmcp.CommandTransport{Command: process}
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}
