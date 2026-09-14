package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/venat/tool"
)

func nativeTestCall(t *testing.T, ctx context.Context, driver tool.Driver, args string) tool.Result {
	t.Helper()
	result, err := driver.Execute(ctx, tool.Call{ID: "native-test", Name: driver.Definition().Name, Arguments: json.RawMessage(args)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeProtocolRejectsMalformedFrames(t *testing.T) {
	for _, payload := range []string{"Content-Length: 999999999\r\n\r\n", "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", "Content-Length: 2\r\n\r\nx!", strings.Repeat("x", 9000) + "\n\n", "Content-Length: 12\r\n\r\n{}"} {
		process := &nativeProcess{stdout: bufio.NewReader(strings.NewReader(payload)), stderr: &nativeBuffer{}}
		if _, err := process.readFrame(); err == nil {
			t.Fatal("malformed protocol accepted")
		}
	}
}

func TestNativeInventoryIsRegisteredAndPolicyScoped(t *testing.T) {
	service := &Service{allowWrite: true, shellPolicy: "allow", allowNetwork: "allow"}
	defer func() {
		if service.native != nil {
			_ = service.native.close(context.Background())
		}
	}()
	drivers, err := service.WorkspaceDrivers(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := tool.NewBus(drivers...)
	if err := bus.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ToolASTGrep, ToolASTEdit, ToolLSP, ToolEval, ToolWebSearch, ToolGitHub, ToolBrowser, ToolComputer, ToolDebug, ToolInspectImage, ToolGenerateImage, ToolTTS, ToolHub} {
		if _, ok := bus.Driver(name); !ok {
			t.Fatalf("missing native driver %s", name)
		}
	}
	service.allowWrite, service.shellPolicy = false, "deny"
	drivers, err = service.WorkspaceDrivers(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus = tool.NewBus(drivers...)
	for _, name := range []string{ToolASTEdit, ToolLSP, ToolEval, ToolBrowser, ToolComputer, ToolDebug, ToolGenerateImage, ToolTTS} {
		if _, ok := bus.Driver(name); ok {
			t.Fatalf("policy-disabled native driver %s advertised", name)
		}
	}
}

func TestNativeImageGenerationEditsAndSafeArtifacts(t *testing.T) {
	root := t.TempDir()
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.png"), pngData.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("missing authorization")
		}
		if r.URL.Path == "/images/edits" {
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			files := r.MultipartForm.File["image[]"]
			if len(files) != 1 || files[0].Header.Get("Content-Type") != "image/png" {
				t.Error("invalid image multipart")
			}
		} else if r.URL.Path != "/images/generations" {
			t.Error("wrong endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData.Bytes())}}})
	}))
	defer server.Close()
	driver := &mediaDriver{root: root, operation: ToolGenerateImage, networkPolicy: "allow", endpoint: server.URL, apiKey: "fixture-secret", client: server.Client()}
	for _, args := range []string{`{"prompt":"fixture","output_path":"new.png"}`, `{"prompt":"edit","output_path":"edited.png","input":["source.png"]}`} {
		result := nativeTestCall(t, context.Background(), driver, args)
		if result.IsError || len(result.Parts) != 1 || !bytes.Equal(result.Parts[0].Data, pngData.Bytes()) || strings.Contains(result.Content, "fixture-secret") {
			t.Fatal(result.Content)
		}
	}
	before := requests
	for _, path := range []string{"new.png", "../outside.png"} {
		args, _ := json.Marshal(map[string]string{"prompt": "fixture", "output_path": path})
		if result := nativeTestCall(t, context.Background(), driver, string(args)); !result.IsError {
			t.Fatal("unsafe output accepted", result.Content)
		}
	}
	if requests != before {
		t.Fatal("invalid output made a chargeable request")
	}
	inspect := &mediaDriver{root: root, operation: ToolInspectImage}
	result := nativeTestCall(t, context.Background(), inspect, `{"path":"source.png","crop":[1,1,3,2]}`)
	if result.IsError || len(result.Parts) != 1 {
		t.Fatal(result.Content)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(result.Parts[0].Data))
	if err != nil || config.Width != 3 || config.Height != 2 {
		t.Fatal(config, err)
	}
	if !nativeTestCall(t, context.Background(), inspect, `{"path":"source.png","crop":[7,5,3,2]}`).IsError {
		t.Fatal("invalid crop accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeArtifact(root, "escape/file.png", pngData.Bytes()); err == nil {
		t.Fatal("symlink escaped workspace")
	}
}

func TestNativePythonPersistsIsolatesResetsAndReportsFailures(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host := newNativeHost(ctx)
	defer host.close(context.Background())
	driver := &evalDriver{root: t.TempDir(), host: host, approval: "allow", networkPolicy: "allow"}
	if err := os.WriteFile(filepath.Join(driver.root, "local_fixture.py"), []byte("answer = 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := WithInvocation(ctx, Invocation{SessionID: "a", AgentID: "worker"})
	b := WithInvocation(ctx, Invocation{SessionID: "b", AgentID: "worker"})
	for _, step := range []struct {
		ctx            context.Context
		args, contains string
		failed         bool
	}{
		{a, `{"code":"import local_fixture\nlocal_fixture.answer"}`, "42", false},
		{a, `{"code":"value = 41\nprint('hello')\nvalue + 1"}`, "42", false},
		{a, `{"code":"value + 2"}`, "43", false},
		{b, `{"code":"value"}`, "NameError", true},
		{a, `{"code":"raise ValueError('reported')"}`, "ValueError", true},
		{a, `{"code":"value"}`, "41", false},
		{a, `{"reset":true,"code":"value"}`, "NameError", true},
		{a, `{"code":"while True: pass","timeout":1}`, "kernel stopped", true},
		{a, `{"code":"1+1"}`, "2", false},
	} {
		result := nativeTestCall(t, step.ctx, driver, step.args)
		if result.IsError != step.failed || !strings.Contains(result.Content, step.contains) {
			t.Fatalf("%s => %#v", step.args, result)
		}
	}
}

func TestNativeWebSearchDecodesLinksAndDoesNotHideProviderFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "blocked" {
			_, _ = w.Write([]byte(`<form id="challenge-form"></form>`))
			return
		}
		_, _ = w.Write([]byte(`<a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.org%2Fsource">Source &amp; docs</a><a class="result__snippet">Verified text</a>`))
	}))
	defer server.Close()
	driver := &webSearchDriver{networkPolicy: "allow", client: server.Client(), endpoint: server.URL}
	result := nativeTestCall(t, context.Background(), driver, `{"query":"hello & world"}`)
	if result.IsError || !strings.Contains(result.Content, "https://example.org/source") || !strings.Contains(result.Content, "Source & docs") {
		t.Fatal(result)
	}
	if !nativeTestCall(t, context.Background(), driver, `{"query":"blocked"}`).IsError {
		t.Fatal("challenge reported as success")
	}
	driver.networkPolicy = "deny"
	if !nativeTestCall(t, context.Background(), driver, `{"query":"hello"}`).IsError {
		t.Fatal("network denial bypassed")
	}
}

func TestNativePublicNetworkSmoke(t *testing.T) {
	if os.Getenv("AZEM_NATIVE_NETWORK_SMOKE") != "1" {
		t.Skip("opt-in public search and GitHub read smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := nativeTestCall(t, ctx, newWebSearchDriver("allow"), `{"query":"Language Server Protocol specification","limit":3}`)
	if result.IsError || !strings.Contains(result.Content, "https://") {
		t.Fatal(result.Content)
	}
	github := &githubDriver{root: t.TempDir(), networkPolicy: "allow"}
	result = nativeTestCall(t, ctx, github, `{"op":"repo_view","repo":"can1357/oh-my-pi"}`)
	if result.IsError || !strings.Contains(result.Content, `"full_name": "can1357/oh-my-pi"`) {
		t.Fatal(result.Content)
	}
}

func TestNativeLSPUsesGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module native.test\n\ngo 1.25.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc answer() int { return 42 }\nfunc main() { _ = answer() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host := newNativeHost(ctx)
	defer host.close(context.Background())
	driver := &lspDriver{root: root, host: host}
	result := nativeTestCall(t, ctx, driver, `{"action":"definition","file":"main.go","line":3,"symbol":"answer"}`)
	if result.IsError || !strings.Contains(result.Content, "main.go") || !strings.Contains(result.Content, `"line": 1`) {
		t.Fatal(result)
	}
}

func TestNativeLSPUnicodePositionsAndEdits(t *testing.T) {
	position, err := nativePosition("a😀b", 1, 3, "")
	if err != nil || position["character"] != 3 {
		t.Fatalf("%v %v", position, err)
	}
	if _, err := lspByteOffset("a😀b", lspPosition{0, 2}); err == nil {
		t.Fatal("split surrogate accepted")
	}
	var edits []lspTextEdit
	if err := json.Unmarshal([]byte(`[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"中"}]`), &edits); err != nil {
		t.Fatal(err)
	}
	result, err := applyLSPTextEdits("a😀b", edits)
	if err != nil || result != "a中b" {
		t.Fatalf("%q %v", result, err)
	}
}

func TestNativeASTSearchAndGuardedRewrite(t *testing.T) {
	if _, err := exec.LookPath("ast-grep"); err != nil {
		t.Skip("native ast-grep not installed")
	}
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(path, []byte("package fixture\nconst answer = 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	edit := newHashlineDriver(root, snapshotReadDriver{workspace: NewLocalWorkspace(root)}, newHashlineClipboard())
	driver := &astDriver{root: root, operation: ToolASTGrep}
	result := nativeTestCall(t, context.Background(), driver, `{"pat":"const $X = $Y","lang":"go","path":"fixture.go"}`)
	if result.IsError || !strings.Contains(result.Content, "answer") {
		t.Fatal(result)
	}
	driver.operation, driver.edit = ToolASTEdit, edit
	result = nativeTestCall(t, context.Background(), driver, `{"pat":"const $X = $Y","lang":"go","path":"fixture.go","rewrite":"const $X = 43","apply":true}`)
	if result.IsError {
		t.Fatal(result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "package fixture\nconst answer = 43\n" {
		t.Fatalf("%q %v", content, err)
	}
	result = nativeTestCall(t, context.Background(), driver, `{"pat":"const $X = $Y","lang":"go","path":"../escape","rewrite":"const $X = 44","apply":true}`)
	if !result.IsError {
		t.Fatal("workspace escape accepted")
	}
}

func TestNativeHubProcessLifecycleAndSessionIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX cat fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host := newNativeHost(ctx)
	defer host.close(context.Background())
	driver := newHubDriver(t.TempDir())
	driver.native, driver.shellPolicy, driver.networkPolicy = host, "allow", "allow"
	a := WithInvocation(ctx, Invocation{SessionID: "a", AgentID: "worker"})
	b := WithInvocation(ctx, Invocation{SessionID: "b", AgentID: "worker"})
	for _, args := range []string{`{"op":"start","name":"echo","application":"cat"}`, `{"op":"send","name":"echo","text":"literal $HOME; $(echo never)","enter":true}`} {
		if result := nativeTestCall(t, a, driver, args); result.IsError {
			t.Fatal(result)
		}
	}
	if len(host.activeProcesses()) != 1 {
		t.Fatal("running process is missing from daemon active work")
	}
	if !nativeTestCall(t, b, driver, `{"op":"send","name":"echo","text":"wrong session"}`).IsError {
		t.Fatal("cross-session send succeeded")
	}
	if result := nativeTestCall(t, a, driver, `{"op":"stop","name":"echo"}`); result.IsError {
		t.Fatal(result)
	}
	if len(host.activeProcesses()) != 0 {
		t.Fatal("stopped process remains active")
	}
}

func TestNativeBrowserSmoke(t *testing.T) {
	if os.Getenv("AZEM_NATIVE_SMOKE") != "1" {
		t.Skip("set AZEM_NATIVE_SMOKE=1 for isolated Chrome smoke")
	}
	if _, err := chromeProgram(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><button onclick="this.textContent='Clicked'">Native button</button><input aria-label="Name"></body></html>`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	host := newNativeHost(ctx)
	defer host.close(context.Background())
	driver := &browserDriver{root: t.TempDir(), host: host, networkPolicy: "allow"}
	args, _ := json.Marshal(map[string]any{"action": "open", "url": server.URL})
	if result := nativeTestCall(t, ctx, driver, string(args)); result.IsError {
		t.Fatal(result)
	}
	for attempt := 0; ; attempt++ {
		result := nativeTestCall(t, ctx, driver, `{"action":"snapshot"}`)
		if result.IsError {
			t.Fatal(result)
		}
		if strings.Contains(result.Content, "Native button") {
			break
		}
		if attempt == 20 {
			t.Fatal("page did not load", result.Content)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, args := range []string{`{"action":"click","selector":"button"}`, `{"action":"type","selector":"input","text":"原生 tools"}`} {
		if result := nativeTestCall(t, ctx, driver, args); result.IsError {
			t.Fatal(result)
		}
	}
	result := nativeTestCall(t, ctx, driver, `{"action":"snapshot"}`)
	if result.IsError || !strings.Contains(result.Content, "Clicked") || !strings.Contains(result.Content, "原生 tools") {
		t.Fatal(result)
	}
	result = nativeTestCall(t, ctx, driver, `{"action":"screenshot"}`)
	if result.IsError || len(result.Parts) != 1 || len(result.Parts[0].Data) == 0 {
		t.Fatal(result)
	}
	if result := nativeTestCall(t, ctx, driver, `{"action":"close"}`); result.IsError {
		t.Fatal(result)
	}
}

func TestNativeMacOSMediaAndPermissionSmoke(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("AZEM_NATIVE_SMOKE") != "1" {
		t.Skip("opt-in macOS native smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	result := nativeTestCall(t, ctx, &mediaDriver{root: root, operation: ToolTTS}, `{"text":"Native tool verification.","output_path":"speech.aiff"}`)
	if result.IsError {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(root, "speech.aiff"))
	if err != nil || len(data) < 100 || string(data[:4]) != "FORM" {
		t.Fatalf("invalid audio: %d %v", len(data), err)
	}
	result = nativeTestCall(t, ctx, &computerDriver{root: root}, `{"action":"status"}`)
	if result.IsError || !strings.Contains(result.Content, "accessibility") || !strings.Contains(result.Content, "screenRecording") {
		t.Fatal(result)
	}
	t.Log(result.Content)
}

func TestNativeDebuggerSmoke(t *testing.T) {
	if os.Getenv("AZEM_NATIVE_SMOKE") != "1" {
		t.Skip("opt-in native compiler/debugger smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.c"), []byte("int main(void) {\n  int answer = 42;\n  return answer;\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeRun(ctx, root, "", "cc", "-g", "-O0", "main.c", "-o", "fixture"); err != nil {
		t.Fatal(err)
	}
	host := newNativeHost(ctx)
	defer host.close(context.Background())
	driver := &debugDriver{root: root, host: host, networkPolicy: "allow"}
	result := nativeTestCall(t, ctx, driver, `{"action":"start","program":"fixture","file":"main.c","lines":[3]}`)
	if result.IsError {
		t.Fatal(result.Content)
	}
	result = nativeTestCall(t, ctx, driver, `{"action":"threads"}`)
	if result.IsError {
		t.Fatal(result.Content)
	}
	var threads struct {
		Result struct {
			Threads []struct {
				ID int `json:"id"`
			} `json:"threads"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result.Structured, &threads); err != nil || len(threads.Result.Threads) == 0 {
		t.Fatalf("threads: %s %v", result.Content, err)
	}
	threadID := threads.Result.Threads[0].ID
	breakpoints := nativeTestCall(t, ctx, driver, `{"action":"breakpoints","file":"main.c","lines":[3]}`)
	if breakpoints.IsError || !strings.Contains(breakpoints.Content, `"verified": true`) {
		t.Fatal(breakpoints.Content)
	}
	args, _ := json.Marshal(map[string]any{"action": "continue", "thread_id": threadID})
	if result := nativeTestCall(t, ctx, driver, string(args)); result.IsError {
		t.Fatal(result.Content)
	}
	for attempt := 0; attempt < 20; attempt++ {
		result = nativeTestCall(t, ctx, driver, `{"action":"wait"}`)
		if result.IsError {
			t.Fatal(result.Content)
		}
		if strings.Contains(result.Content, `"stopped"`) {
			break
		}
		if attempt == 19 {
			t.Fatal("breakpoint did not stop", result.Content)
		}
	}
	args, _ = json.Marshal(map[string]any{"action": "stack", "thread_id": threadID})
	result = nativeTestCall(t, ctx, driver, string(args))
	if result.IsError {
		t.Fatal(result.Content)
	}
	var stack struct {
		Result struct {
			Frames []struct {
				ID   int `json:"id"`
				Line int `json:"line"`
			} `json:"stackFrames"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result.Structured, &stack); err != nil || len(stack.Result.Frames) == 0 || stack.Result.Frames[0].Line != 3 {
		t.Fatalf("stack: %s %v", result.Content, err)
	}
	args, _ = json.Marshal(map[string]any{"action": "evaluate", "frame_id": stack.Result.Frames[0].ID, "expression": "answer"})
	result = nativeTestCall(t, ctx, driver, string(args))
	if result.IsError || !strings.Contains(result.Content, "42") {
		t.Fatal(result.Content)
	}
	if result := nativeTestCall(t, ctx, driver, `{"action":"stop"}`); result.IsError {
		t.Fatal(result.Content)
	}
}
