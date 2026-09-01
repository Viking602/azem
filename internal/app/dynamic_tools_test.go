package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Viking602/venat/tool"
)

type dynamicToolDriver struct {
	definition tool.Definition
}

func (driver dynamicToolDriver) Definition() tool.Definition { return driver.definition }
func (driver dynamicToolDriver) Execute(context.Context, tool.Call, tool.UpdateSink) (tool.Result, error) {
	return tool.Result{Name: driver.definition.Name, Content: "ok"}, nil
}

func TestDynamicToolCatalogRejectsConflictsAndFilters(t *testing.T) {
	catalog := newDynamicToolCatalog()
	read := dynamicToolDriver{definition: tool.Definition{Name: "read", InputSchema: tool.Schema{Type: "object"}}}
	write := dynamicToolDriver{definition: tool.Definition{Name: "write", InputSchema: tool.Schema{Type: "object"}}}
	if err := catalog.RegisterAll("builtin", []tool.Driver{write, read}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register("mcp", read); err == nil {
		t.Fatal("duplicate dynamic tool was accepted")
	}
	filtered := catalog.Filter(func(definition tool.Definition) bool {
		return definition.Name == "read"
	})
	if got := filtered.Names(); !reflect.DeepEqual(got, []string{"read"}) {
		t.Fatalf("filtered names = %v", got)
	}
	result, err := filtered.Bus().Execute(context.Background(), tool.Call{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}, tool.ExecuteOptions{})
	if err != nil || result.Content != "ok" {
		t.Fatalf("filtered execution = %#v, %v", result, err)
	}
}

func TestDynamicToolCatalogRejectsInvalidSchemaAtRegistration(t *testing.T) {
	catalog := newDynamicToolCatalog()
	err := catalog.Register("plugin", dynamicToolDriver{definition: tool.Definition{
		Name: "broken", InputSchema: tool.Schema{Type: "not-a-json-schema-type"},
	}})
	if err == nil {
		t.Fatal("invalid dynamic tool schema was accepted")
	}
}
