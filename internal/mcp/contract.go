package mcp

import (
	"context"

	"github.com/Viking602/venat/message"
)

// Client is the narrow MCP capability surface used by Azem. The official MCP
// SDK is adapted to this interface at the transport boundary so the rest of
// the application owns its snapshots and wire-independent DTOs.
type Client interface {
	Initialize(context.Context, string, string) (InitializeResult, error)
	ListTools(context.Context) ([]message.ToolDefinition, error)
	CallTool(context.Context, string, map[string]any) (CallToolResult, error)
	ListResources(context.Context) ([]Resource, error)
	ReadResource(context.Context, string) ([]ResourceContent, error)
	ListPrompts(context.Context) ([]Prompt, error)
	GetPrompt(context.Context, string, map[string]string) ([]PromptMessage, error)
	Close() error
}

type SubscriptionClient interface {
	SubscribeResource(context.Context, string) error
	UnsubscribeResource(context.Context, string) error
}

type ResourceTemplateClient interface {
	ListResourceTemplates(context.Context) ([]ResourceTemplate, error)
}

type ProtocolNotification struct {
	Kind          string  `json:"kind"`
	URI           string  `json:"uri,omitempty"`
	Level         string  `json:"level,omitempty"`
	Logger        string  `json:"logger,omitempty"`
	Message       string  `json:"message,omitempty"`
	ProgressToken string  `json:"progressToken,omitempty"`
	Progress      float64 `json:"progress,omitempty"`
	Total         float64 `json:"total,omitempty"`
	Data          any     `json:"data,omitempty"`
}

type NotificationHandler func(context.Context, ProtocolNotification)

type Elicitation struct {
	Mode            string
	Message         string
	URL             string
	ElicitationID   string
	RequestedSchema any
}

type ElicitationResult struct {
	Action  string
	Content map[string]any
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	ServerInfo      ServerInfo     `json:"serverInfo,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type CallToolResult struct {
	Content           []ContentBlock `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}
