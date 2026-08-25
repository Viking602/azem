package capability

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRegistrySnapshotsDynamicSourcesDeterministically(t *testing.T) {
	registry := NewRegistry()
	tools := []Descriptor{{ID: "write", Kind: KindTool, Enabled: true, InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if err := registry.RegisterSource("tools", func() ([]Descriptor, error) {
		return tools, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterSource("providers", Static(Descriptor{ID: "openai", Kind: KindProvider, Enabled: true})); err != nil {
		t.Fatal(err)
	}
	first, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{string(first[0].Kind) + ":" + first[0].ID, string(first[1].Kind) + ":" + first[1].ID}; !reflect.DeepEqual(got, []string{"provider:openai", "tool:write"}) {
		t.Fatalf("snapshot order = %v", got)
	}
	first[1].InputSchema[0] = 'x'
	tools = []Descriptor{{ID: "read", Kind: KindTool, Enabled: true}}
	second, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if second[1].ID != "read" {
		t.Fatalf("dynamic source snapshot = %#v", second)
	}
}

func TestRegistryRejectsDuplicateSourceAndCapability(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterSource("one", Static(Descriptor{ID: "read", Kind: KindTool})); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterSource("one", Static()); !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("duplicate source error = %v", err)
	}
	if err := registry.RegisterSource("two", Static(Descriptor{ID: "read", Kind: KindTool})); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Snapshot(); !errors.Is(err, ErrDuplicateCapability) {
		t.Fatalf("duplicate capability error = %v", err)
	}
}
