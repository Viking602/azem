package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Viking602/venat/tool"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type externalTestDriver struct{ name string }

func (driver externalTestDriver) Definition() tool.Definition {
	return tool.Definition{Name: driver.name, Description: "external", InputSchema: tool.Schema{Type: "object"}, EffectType: tool.EffectReadOnly}
}

func (driver externalTestDriver) Execute(context.Context, tool.Call, tool.UpdateSink) (tool.Result, error) {
	return tool.Result{Name: driver.name, Content: "ok"}, nil
}

func TestAttachExternalToolsRejectsConflictsAndProjectsSameWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	if err := service.AttachExternalTools([]tool.Driver{externalTestDriver{name: "coding.read_file"}}, nil); err == nil {
		t.Fatal("custom tool conflict with built-in succeeded")
	}
	if err := service.AttachExternalTools([]tool.Driver{externalTestDriver{name: "custom_external"}}, func(context.Context) error { closed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	drivers, err := service.WorkspaceDrivers(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, driver := range drivers {
		if driver.Definition().Name == "custom_external" {
			found = true
		}
	}
	if !found {
		t.Fatal("same-workspace custom tool is absent")
	}
	otherDrivers, err := service.WorkspaceDrivers(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, driver := range otherDrivers {
		if driver.Definition().Name == "custom_external" {
			t.Fatal("custom tool leaked into another workspace")
		}
	}
	if err := service.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("external tool host closer was not called")
	}
}
