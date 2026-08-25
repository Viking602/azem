package customtools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/venat/tool"
)

func TestCustomToolHostLoadsExecutesStreamsAndKeepsModuleFailuresIsolated(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("Bun is unavailable")
	}
	root := t.TempDir()
	valid := filepath.Join(root, "valid.ts")
	invalid := filepath.Join(root, "invalid.ts")
	if err := os.WriteFile(valid, []byte(`
export default (pi) => ({
  name: "repo_stats",
  description: "Run a persistent custom tool",
  approval: "read",
  parameters: pi.zod.object({ value: pi.zod.string(), optional: pi.zod.number().optional() }),
  async execute(id, params, onUpdate, ctx, signal) {
    onUpdate({ content: [{ type: "text", text: "working" }], details: { phase: "start" } });
    const result = await pi.exec("printf", [params.value], { signal });
    return { content: [{ type: "text", text: result.stdout }], details: { id, code: result.code } };
  },
  onSession(event) { if (event.reason === "shutdown") Bun.write(pi.cwd + "/host-closed.txt", event.reason); }
});
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(`export const nope = true;`), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := New(context.Background(), root, []string{invalid, valid})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	})
	if len(host.Diagnostics()) != 1 || !strings.Contains(host.Diagnostics()[0], "default export") {
		t.Fatalf("diagnostics = %#v", host.Diagnostics())
	}
	drivers, err := host.Drivers()
	if err != nil || len(drivers) != 1 {
		t.Fatalf("drivers = %#v, %v", drivers, err)
	}
	definition := drivers[0].Definition()
	if definition.Name != "repo_stats" || definition.EffectType != tool.EffectReadOnly || definition.InputSchema.Type != "object" || len(definition.InputSchema.Required) != 1 {
		t.Fatalf("definition = %#v", definition)
	}
	var updates []tool.Update
	result, err := drivers[0].Execute(context.Background(), tool.Call{ID: "call-1", Name: "repo_stats", Arguments: json.RawMessage(`{"value":"hello"}`)}, func(update tool.Update) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil || result.Content != "hello" || len(result.Parts) != 1 || result.Parts[0].Text != "hello" ||
		result.ToolCallID != "call-1" || !strings.Contains(string(result.Structured), `"code":0`) {
		t.Fatalf("result = %#v, %v", result, err)
	}
	if len(updates) != 1 || updates[0].Message != "working" || !strings.Contains(updates[0].Data["details"], "start") {
		t.Fatalf("updates = %#v", updates)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if payload, err := os.ReadFile(filepath.Join(root, "host-closed.txt")); err != nil || string(payload) != "shutdown" {
		t.Fatalf("custom tool shutdown event = %q, %v", payload, err)
	}
}

func TestDiscoverCustomToolsRequiresProjectTrustAndIncludesPluginPaths(t *testing.T) {
	workspace, home, plugin := t.TempDir(), t.TempDir(), t.TempDir()
	writeModule(t, filepath.Join(workspace, ".omp", "tools", "project.ts"))

	writeModule(t, filepath.Join(home, ".claude", "tools", "user.js"))
	writeModule(t, filepath.Join(plugin, "plugin.mjs"))
	modules, diagnostics, err := Discover(DiscoveryOptions{Workspace: workspace, HomeDir: home, AdditionalPaths: []string{plugin}})
	if err != nil || len(diagnostics) != 0 || len(modules) != 2 {
		t.Fatalf("untrusted discovery modules=%v diagnostics=%v err=%v", modules, diagnostics, err)
	}
	for _, module := range modules {
		if strings.Contains(module, "project.ts") {
			t.Fatalf("untrusted project tool loaded: %v", modules)
		}
	}
	modules, _, err = Discover(DiscoveryOptions{Workspace: workspace, HomeDir: home, TrustProject: true, AdditionalPaths: []string{plugin}})
	if err != nil || len(modules) != 3 {
		t.Fatalf("trusted discovery modules=%v err=%v", modules, err)
	}
}

func TestCustomToolHostCancelsInFlightExecution(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("Bun is unavailable")
	}
	root := t.TempDir()
	module := filepath.Join(root, "cancel.ts")
	if err := os.WriteFile(module, []byte(`
export default () => ({
  name: "wait_forever",
  parameters: {type:"object"},
  execute(id, params, update, ctx, signal) {
    return new Promise((resolve, reject) => signal.addEventListener("abort", () => reject(new Error("cancelled")), {once:true}));
  }
});
`), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := New(context.Background(), root, []string{module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	drivers, err := host.Drivers()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := drivers[0].Execute(ctx, tool.Call{ID: "cancel", Name: "wait_forever", Arguments: json.RawMessage(`{}`)}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel error = %v", err)
	}
}

func writeModule(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`export default () => ({name:"demo",description:"demo",parameters:{type:"object"},execute(){return "ok"}});`), 0o600); err != nil {
		t.Fatal(err)
	}
}
