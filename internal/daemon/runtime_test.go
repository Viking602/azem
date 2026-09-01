package daemon

import (
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/desktopipc"
)

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
