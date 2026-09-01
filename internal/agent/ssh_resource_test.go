package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/tool"
)

func TestSSHResourceListsConfiguredHostsThroughReadTool(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "ssh-index"})
	root := t.TempDir()
	router := resource.NewRouter(2 << 20)
	service := newASTWriteService(t, ctx, root, router)
	read := findWorkspaceTool(t, service, root, ToolReadFile)
	arguments := json.RawMessage(`{"path":"ssh://"}`)
	result, err := read.Execute(ctx, tool.Call{ID: "ssh", Name: ToolReadFile, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "SSH hosts") {
		t.Fatalf("SSH host index = %#v, %v", result, err)
	}
	if !strings.Contains(strings.ToLower(result.Content), "configured") {
		t.Fatalf("SSH host index lacks configuration state: %s", result.Content)
	}
}

func TestReliableSearchReadsExactInternalResource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	router := resource.NewRouter(2 << 20)
	if err := router.Register("fixture", &staticASTResource{data: []byte("alpha\nremote needle\nomega\n")}); err != nil {
		t.Fatal(err)
	}
	service := newASTWriteService(t, ctx, root, router)
	search := findWorkspaceTool(t, service, root, ToolSearch)
	arguments := json.RawMessage(`{"query":"needle","path":"fixture://sample.txt"}`)
	result, err := search.Execute(ctx, tool.Call{ID: "search", Name: ToolSearch, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "[fixture://sample.txt#") || !strings.Contains(result.Content, "2:remote needle") {
		t.Fatalf("internal resource search = %#v, %v", result, err)
	}
}

func TestSSHResourceHonorsNetworkPolicyAndBareURIParsing(t *testing.T) {
	parsed, err := resource.Parse("ssh://")
	if err != nil || parsed.Scheme != "ssh" || parsed.Opaque != "" {
		t.Fatalf("bare SSH URI = %#v, %v", parsed, err)
	}
	handler := newSSHResourceHandler(newLSPBridgeRuntime(), "deny")
	_, err = handler.Read(context.Background(), resource.Request{URI: resource.URI{Raw: "ssh://host/etc/hosts", Scheme: "ssh", Opaque: "host/etc/hosts"}, Scope: resource.Scope{Workspace: t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "network policy") {
		t.Fatalf("SSH network denial = %v", err)
	}
}

func TestSSHLiveRead(t *testing.T) {
	uri := os.Getenv("AZEM_LIVE_SSH_URI")
	if uri == "" {
		t.Skip("set AZEM_LIVE_SSH_URI=ssh://host/absolute/path for live SSH verification")
	}
	ctx := context.Background()
	handler := newSSHResourceHandler(newLSPBridgeRuntime(), "allow")
	parsed, err := resource.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.Read(ctx, resource.Request{URI: parsed, Scope: resource.Scope{Workspace: "."}})
	if err != nil || len(result.Data) == 0 {
		t.Fatalf("live SSH read = %#v, %v", result, err)
	}
}
