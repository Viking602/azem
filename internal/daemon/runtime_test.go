package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/desktopipc"
)

func TestIdleDaemonStopsAfterLastClientDisconnects(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "azem-idle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("AZEM_HOME", home)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	done := make(chan error, 1)
	go func() { done <- runtime.Run() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := desktopipc.Connect(ctx, runtime.Endpoint(), "idle-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("idle daemon survived its last client")
	}
	if _, err := os.Stat(runtime.Endpoint().TokenFile); !os.IsNotExist(err) {
		t.Fatalf("stopped daemon retained authentication token: %v", err)
	}
}

func TestEndpointPathIsWorkspaceScoped(t *testing.T) {
	stateDir := t.TempDir()
	firstWorkspace := filepath.Join(t.TempDir(), "first")
	secondWorkspace := filepath.Join(t.TempDir(), "second")
	first, err := EndpointPath(stateDir, firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := EndpointPath(stateDir, firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EndpointPath(stateDir, secondWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if first != firstAgain || first == second {
		t.Fatalf("workspace endpoints = %q, %q, %q", first, firstAgain, second)
	}
	workspaceID, err := desktopipc.WorkspaceID(firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(stateDir, "gpui-daemons", workspaceID, "endpoint.json")
	if first != want {
		t.Fatalf("endpoint = %q, want %q", first, want)
	}
}

func TestEndpointPathRequiresStateDirectory(t *testing.T) {
	if _, err := EndpointPath("", t.TempDir()); err == nil {
		t.Fatal("empty state directory was accepted")
	}
}
