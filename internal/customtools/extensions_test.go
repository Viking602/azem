package customtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestExtensionHostRegistersToolsCommandsProvidersAgentsAndThemes(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("Bun is unavailable")
	}
	root := t.TempDir()
	themePath := filepath.Join(root, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"name":"extension-dark","colors":{"accent":"#fff"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	brokerLog := filepath.Join(root, "broker.json")
	module := filepath.Join(root, "extension.ts")
	moduleSource := fmt.Sprintf(`
export default (pi) => {
  pi.registerTool({name:"extension_echo",description:"Extension echo",approval:"read",parameters:{type:"object",properties:{value:{type:"string"}}},execute(id,args){return "ext:"+args.value}});
  pi.registerCommand("extension-review", {description:"Review through extension", handler(args){return {prompt:"Review " + args}}});
  pi.registerProvider("extension-provider", {baseUrl:"https://example.test/v1",apiKey:"EXTENSION_API_KEY",api:"openai-completions",models:[{id:"extension-model",name:"Extension Model",reasoning:true,input:["text"],contextWindow:128000,maxTokens:4096}]});
  pi.registerAgent("extension-reviewer", {description:"Review code",systemPrompt:"Review the assigned code.",model:"extension-provider/extension-model",tools:["coding.read_file"]});
  pi.registerFileWriteFallback(() => { throw new Error("skip me"); });
  pi.registerFileWriteFallback(async (req, ctx) => { await Bun.write(%q, JSON.stringify({op:"write",req,sessionId:ctx.sessionId})); return req.content === "brokered"; });
  pi.registerFileDeleteFallback(async (req, ctx) => { await Bun.write(%q, JSON.stringify({op:"delete",req,sessionId:ctx.sessionId})); return req.confirmedFile; });
  pi.on("resources_discover", () => ({themePaths:[%q]}));
};
`, brokerLog, brokerLog, themePath)
	if err := os.WriteFile(module, []byte(moduleSource), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := NewWithExtensions(context.Background(), root, nil, []string{module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if len(host.Diagnostics()) != 0 || len(host.ExtensionCommands()) != 1 || len(host.ExtensionProviders()) != 1 || len(host.ExtensionAgents()) != 1 || len(host.ThemePaths()) != 1 {
		t.Fatalf("extension registrations commands=%#v providers=%#v agents=%#v themes=%#v diagnostics=%#v", host.ExtensionCommands(), host.ExtensionProviders(), host.ExtensionAgents(), host.ThemePaths(), host.Diagnostics())
	}
	if host.ExtensionProviders()[0].Name != "extension-provider" || !json.Valid(host.ExtensionProviders()[0].Config) {
		t.Fatalf("provider = %#v", host.ExtensionProviders()[0])
	}
	prompt, matched, err := host.ExecuteCommand(context.Background(), "extension-review", "src/main.go")
	if err != nil || !matched || prompt != "Review src/main.go" {
		t.Fatalf("extension command = %q, %v, %v", prompt, matched, err)
	}
	drivers, err := host.Drivers()
	if err != nil || len(drivers) != 1 {
		t.Fatalf("drivers = %#v, %v", drivers, err)
	}
	result, err := drivers[0].Execute(context.Background(), tool.Call{ID: "ext", Name: "extension_echo", Arguments: json.RawMessage(`{"value":"hello"}`)}, nil)
	if err != nil || result.Content != "ext:hello" {
		t.Fatalf("extension tool = %#v, %v", result, err)
	}
	handled, err := host.BrokerWrite(context.Background(), filepath.Join(root, "denied.txt"), []byte("brokered"), errors.New("permission denied"), "session-1")
	if err != nil || !handled {
		t.Fatalf("write fallback handled=%v error=%v", handled, err)
	}
	var brokerPayload map[string]any
	payload, readErr := os.ReadFile(brokerLog)
	if readErr != nil || json.Unmarshal(payload, &brokerPayload) != nil || brokerPayload["op"] != "write" || brokerPayload["sessionId"] != "session-1" {
		t.Fatalf("write fallback payload=%s error=%v", payload, readErr)
	}
	handled, err = host.BrokerDelete(context.Background(), filepath.Join(root, "denied.txt"), errors.New("read-only"), "session-2", true)
	if err != nil || !handled {
		t.Fatalf("delete fallback handled=%v error=%v", handled, err)
	}
	payload, readErr = os.ReadFile(brokerLog)
	if readErr != nil || json.Unmarshal(payload, &brokerPayload) != nil || brokerPayload["op"] != "delete" || brokerPayload["sessionId"] != "session-2" {
		t.Fatalf("delete fallback payload=%s error=%v", payload, readErr)
	}
}

func TestDiscoverExtensionsRequiresProjectTrustAndKeepsPluginModules(t *testing.T) {
	workspace, home, plugin := t.TempDir(), t.TempDir(), t.TempDir()
	writeModule(t, filepath.Join(workspace, ".omp", "extensions", "project.ts"))
	writeModule(t, filepath.Join(home, ".omp", "agent", "extensions", "user.ts"))
	pluginModule := filepath.Join(plugin, "plugin.ts")
	writeModule(t, pluginModule)
	modules, diagnostics, err := DiscoverExtensions(DiscoveryOptions{Workspace: workspace, HomeDir: home, AdditionalPaths: []string{pluginModule}})
	if err != nil || len(diagnostics) != 0 || len(modules) != 2 {
		t.Fatalf("untrusted extensions=%v diagnostics=%v err=%v", modules, diagnostics, err)
	}
	modules, _, err = DiscoverExtensions(DiscoveryOptions{Workspace: workspace, HomeDir: home, TrustProject: true, AdditionalPaths: []string{pluginModule}})
	if err != nil || len(modules) != 3 {
		t.Fatalf("trusted extensions=%v err=%v", modules, err)
	}
}
