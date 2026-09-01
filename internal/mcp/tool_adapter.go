package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Viking602/venat/tool"
)

var errInvalidMCPClient = errors.New("MCP client is invalid")

func importMCPTools(ctx context.Context, client Client) ([]tool.Driver, error) {
	if isNilMCPClient(client) {
		return nil, errInvalidMCPClient
	}
	definitions, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	drivers := make([]tool.Driver, 0, len(definitions))
	for _, definition := range definitions {
		drivers = append(drivers, importedRemoteTool{client: client, definition: tool.Definition(definition)})
	}
	return drivers, nil
}

type importedRemoteTool struct {
	client     Client
	definition tool.Definition
}

func (driver importedRemoteTool) Definition() tool.Definition { return driver.definition }

func (driver importedRemoteTool) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	arguments := map[string]any{}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return tool.Result{}, err
		}
	}
	result, err := driver.client.CallTool(ctx, call.Name, arguments)
	if err != nil {
		return tool.Result{}, err
	}
	texts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return tool.Result{}, err
	}
	if result.StructuredContent == nil {
		structured = nil
	}
	return tool.Result{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    strings.Join(texts, "\n"),
		Structured: structured,
		IsError:    result.IsError,
	}, nil
}
