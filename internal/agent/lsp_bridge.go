package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxLSPBridgeMessageBytes = 32 << 20

type lspBridgeRuntime struct {
	mu                   sync.Mutex
	writeMu              sync.Mutex
	mutationMu           sync.Mutex
	debugMu              sync.Mutex
	evalMu               sync.Mutex
	computerMu           sync.Mutex
	githubMu             sync.Mutex
	command              *exec.Cmd
	stdin                io.WriteCloser
	pending              map[uint64]chan lspBridgeResponse
	sequence             atomic.Uint64
	closed               bool
	runtime              string
	script               string
	toolModule           string
	clientModule         string
	debugModule          string
	dapModule            string
	evalModule           string
	evalJSModule         string
	evalPyModule         string
	browserModule        string
	browserTabsModule    string
	computerModule       string
	webSearchModule      string
	githubModule         string
	sshModule            string
	internalURLModule    string
	sshConnectionsModule string
	hubModule            string
	launchClientModule   string
	launchBrokerModule   string
	terminalWorkerModule string
	imageGenModule       string
	ttsModule            string
}

type lspBridgeRequest struct {
	ID           uint64         `json:"id"`
	Tool         string         `json:"tool,omitempty"`
	CWD          string         `json:"cwd"`
	ReadOnly     bool           `json:"readOnly,omitempty"`
	SessionID    string         `json:"sessionId,omitempty"`
	ArtifactsDir string         `json:"artifactsDir,omitempty"`
	Params       map[string]any `json:"params"`
}

type lspBridgeResponse struct {
	ID      uint64          `json:"id"`
	IsError bool            `json:"isError,omitempty"`
	OK      bool            `json:"ok"`
	Content string          `json:"content,omitempty"`
	Parts   json.RawMessage `json:"parts,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func newLSPBridgeRuntime() *lspBridgeRuntime {
	return &lspBridgeRuntime{pending: make(map[uint64]chan lspBridgeResponse)}
}

func (runtime *lspBridgeRuntime) request(ctx context.Context, cwd string, readOnly bool, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestTool(ctx, "lsp", cwd, readOnly, params)
}

func (runtime *lspBridgeRuntime) requestTool(ctx context.Context, toolName, cwd string, readOnly bool, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, toolName, cwd, readOnly, "", "", params)
}

func (runtime *lspBridgeRuntime) requestEval(ctx context.Context, cwd, sessionID, artifactsDir string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "eval", cwd, false, sessionID, artifactsDir, params)
}

func (runtime *lspBridgeRuntime) requestBrowser(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "browser", cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestComputer(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "computer", cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestWebSearch(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "web_search", cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestGitHub(ctx context.Context, cwd, sessionID, artifactsDir string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "github", cwd, false, sessionID, artifactsDir, params)
}

func (runtime *lspBridgeRuntime) requestSSH(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "ssh", cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestHub(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, "hub", cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestMedia(ctx context.Context, toolName, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
	return runtime.requestEnvelope(ctx, toolName, cwd, false, sessionID, "", params)
}

func (runtime *lspBridgeRuntime) requestEnvelope(ctx context.Context, toolName, cwd string, readOnly bool, sessionID, artifactsDir string, params map[string]any) (lspBridgeResponse, error) {
	if runtime == nil {
		return lspBridgeResponse{}, errors.New("LSP bridge is unavailable")
	}
	if err := runtime.ensureStarted(); err != nil {
		return lspBridgeResponse{}, err
	}
	id := runtime.sequence.Add(1)
	payload, err := json.Marshal(lspBridgeRequest{ID: id, Tool: toolName, CWD: cwd, ReadOnly: readOnly, SessionID: sessionID, ArtifactsDir: artifactsDir, Params: params})
	if err != nil {
		return lspBridgeResponse{}, err
	}
	if len(payload) > maxLSPBridgeMessageBytes {
		return lspBridgeResponse{}, errors.New("LSP request exceeds 32 MiB")
	}
	response := make(chan lspBridgeResponse, 1)
	runtime.mu.Lock()
	if runtime.closed || runtime.stdin == nil {
		runtime.mu.Unlock()
		return lspBridgeResponse{}, errors.New("LSP bridge is not running")
	}
	runtime.pending[id] = response
	stdin := runtime.stdin
	runtime.mu.Unlock()
	runtime.writeMu.Lock()
	_, writeErr := stdin.Write(append(payload, '\n'))
	runtime.writeMu.Unlock()
	if writeErr != nil {
		runtime.removePending(id)
		return lspBridgeResponse{}, fmt.Errorf("write LSP bridge request: %w", writeErr)
	}
	select {
	case <-ctx.Done():
		runtime.removePending(id)
		runtime.sendCancel(id)
		return lspBridgeResponse{}, ctx.Err()
	case result := <-response:
		if !result.OK {
			return result, errors.New(result.Error)
		}
		return result, nil
	}
}

func (runtime *lspBridgeRuntime) ensureStarted() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return errors.New("LSP bridge is closed")
	}
	if runtime.command != nil && runtime.stdin != nil {
		return nil
	}
	if err := runtime.resolveAssets(); err != nil {
		return err
	}
	command := exec.Command(runtime.runtime, runtime.script)
	command.Dir = filepath.Dir(runtime.script)
	command.Env = append(os.Environ(),
		"AZEM_CODING_AGENT_LSP="+runtime.toolModule,
		"AZEM_CODING_AGENT_LSP_CLIENT="+runtime.clientModule,
		"AZEM_CODING_AGENT_DEBUG="+runtime.debugModule,
		"AZEM_CODING_AGENT_DAP="+runtime.dapModule,
		"AZEM_CODING_AGENT_EVAL="+runtime.evalModule,
		"AZEM_CODING_AGENT_EVAL_JS="+runtime.evalJSModule,
		"AZEM_CODING_AGENT_EVAL_PY="+runtime.evalPyModule,
		"AZEM_CODING_AGENT_BROWSER="+runtime.browserModule,
		"AZEM_CODING_AGENT_BROWSER_TABS="+runtime.browserTabsModule,
		"AZEM_CODING_AGENT_COMPUTER="+runtime.computerModule,
		"AZEM_CODING_AGENT_WEB_SEARCH="+runtime.webSearchModule,
		"AZEM_CODING_AGENT_GITHUB="+runtime.githubModule,
		"AZEM_CODING_AGENT_SSH="+runtime.sshModule,
		"AZEM_CODING_AGENT_INTERNAL_URL="+runtime.internalURLModule,
		"AZEM_CODING_AGENT_SSH_CONNECTIONS="+runtime.sshConnectionsModule,
		"AZEM_CODING_AGENT_HUB="+runtime.hubModule,
		"AZEM_CODING_AGENT_LAUNCH_CLIENT="+runtime.launchClientModule,
		"AZEM_CODING_AGENT_LAUNCH_BROKER="+runtime.launchBrokerModule,
		"AZEM_CODING_AGENT_TERMINAL_WORKER="+runtime.terminalWorkerModule,
		"AZEM_CODING_AGENT_IMAGE_GEN="+runtime.imageGenModule,
		"AZEM_CODING_AGENT_TTS="+runtime.ttsModule,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start LSP bridge: %w", err)
	}
	runtime.command, runtime.stdin = command, stdin
	go runtime.readResponses(command, stdout, stderr)
	return nil
}

func (runtime *lspBridgeRuntime) readResponses(command *exec.Cmd, stdout, stderr io.Reader) {
	errorOutput := &astCappedBuffer{limit: maxASTBridgeErrorBytes}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(errorOutput, stderr)
		close(stderrDone)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLSPBridgeMessageBytes)
	for scanner.Scan() {
		var response lspBridgeResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}
		runtime.mu.Lock()
		pending := runtime.pending[response.ID]
		delete(runtime.pending, response.ID)
		runtime.mu.Unlock()
		if pending != nil {
			pending <- response
		}
	}
	waitErr := command.Wait()
	<-stderrDone
	message := "LSP bridge stopped"
	if scanner.Err() != nil {
		message += ": " + scanner.Err().Error()
	} else if waitErr != nil {
		message += ": " + waitErr.Error()
	}
	if detail := strings.TrimSpace(errorOutput.String()); detail != "" {
		message += ": " + detail
	}
	runtime.mu.Lock()
	if runtime.command == command {
		runtime.command, runtime.stdin = nil, nil
	}
	pending := runtime.pending
	runtime.pending = make(map[uint64]chan lspBridgeResponse)
	closed := runtime.closed
	runtime.mu.Unlock()
	for id, waiter := range pending {
		waiter <- lspBridgeResponse{ID: id, OK: false, Error: message}
	}
	if !closed {
		return
	}
}

func (runtime *lspBridgeRuntime) sendCancel(id uint64) {
	payload, _ := json.Marshal(map[string]any{"cancel": id})
	runtime.mu.Lock()
	stdin := runtime.stdin
	runtime.mu.Unlock()
	if stdin == nil {
		return
	}
	runtime.writeMu.Lock()
	_, _ = stdin.Write(append(payload, '\n'))
	runtime.writeMu.Unlock()
}

func (runtime *lspBridgeRuntime) removePending(id uint64) {
	runtime.mu.Lock()
	delete(runtime.pending, id)
	runtime.mu.Unlock()
}

func (runtime *lspBridgeRuntime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil
	}
	runtime.closed = true
	stdin, command := runtime.stdin, runtime.command
	runtime.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if command == nil || command.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		for {
			runtime.mu.Lock()
			running := runtime.command == command
			runtime.mu.Unlock()
			if !running {
				done <- nil
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-ctx.Done():
		_ = command.Process.Kill()
		return ctx.Err()
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		return errors.New("LSP bridge shutdown timed out")
	}
}

func (runtime *lspBridgeRuntime) resolveAssets() error {
	if runtime.runtime != "" && runtime.script != "" && runtime.toolModule != "" && runtime.clientModule != "" && runtime.debugModule != "" && runtime.dapModule != "" && runtime.evalModule != "" && runtime.evalJSModule != "" && runtime.evalPyModule != "" && runtime.browserModule != "" && runtime.browserTabsModule != "" && runtime.computerModule != "" && runtime.webSearchModule != "" && runtime.githubModule != "" && runtime.sshModule != "" && runtime.internalURLModule != "" && runtime.sshConnectionsModule != "" && runtime.hubModule != "" && runtime.launchClientModule != "" && runtime.launchBrokerModule != "" && runtime.terminalWorkerModule != "" && runtime.imageGenModule != "" && runtime.ttsModule != "" {
		return nil
	}
	runtimePath := strings.TrimSpace(os.Getenv("AZEM_JS_RUNTIME"))
	if runtimePath == "" {
		resolved, err := exec.LookPath("bun")
		if err != nil {
			return errors.New("LSP tool requires the bundled Bun runtime or bun on PATH")
		}
		runtimePath = resolved
	}
	script := strings.TrimSpace(os.Getenv("AZEM_LSP_BRIDGE"))
	toolModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_LSP"))
	clientModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_LSP_CLIENT"))
	debugModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_DEBUG"))
	dapModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_DAP"))
	evalModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_EVAL"))
	evalJSModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_EVAL_JS"))
	evalPyModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_EVAL_PY"))
	browserModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_BROWSER"))
	browserTabsModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_BROWSER_TABS"))
	computerModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_COMPUTER"))
	webSearchModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_WEB_SEARCH"))
	githubModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_GITHUB"))
	sshModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_SSH"))
	internalURLModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_INTERNAL_URL"))
	sshConnectionsModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_SSH_CONNECTIONS"))
	hubModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_HUB"))
	launchClientModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_LAUNCH_CLIENT"))
	launchBrokerModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_LAUNCH_BROKER"))
	terminalWorkerModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_TERMINAL_WORKER"))
	imageGenModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_IMAGE_GEN"))
	ttsModule := strings.TrimSpace(os.Getenv("AZEM_CODING_AGENT_TTS"))
	for _, root := range astBridgeRoots() {
		if script == "" {
			candidate := filepath.Join(root, "internal", "lspbridge", "bridge.mjs")
			if regularFile(candidate) {
				script = candidate
			}
		}
		packageRoot := filepath.Join(root, "runtime-js", "node_modules", "@oh-my-pi", "pi-coding-agent", "src", "lsp")
		if toolModule == "" {
			candidate := filepath.Join(packageRoot, "tool.ts")
			if regularFile(candidate) {
				toolModule = candidate
			}
		}
		if clientModule == "" {
			candidate := filepath.Join(packageRoot, "client.ts")
			if regularFile(candidate) {
				clientModule = candidate
			}
		}
		packageSource := filepath.Join(root, "runtime-js", "node_modules", "@oh-my-pi", "pi-coding-agent", "src")
		if debugModule == "" {
			candidate := filepath.Join(packageSource, "tools", "debug.ts")
			if regularFile(candidate) {
				debugModule = candidate
			}
		}
		if dapModule == "" {
			candidate := filepath.Join(packageSource, "dap", "session.ts")
			if regularFile(candidate) {
				dapModule = candidate
			}
		}
		if evalModule == "" {
			candidate := filepath.Join(packageSource, "tools", "eval.ts")
			if regularFile(candidate) {
				evalModule = candidate
			}
		}
		if evalJSModule == "" {
			candidate := filepath.Join(packageSource, "eval", "js", "context-manager.ts")
			if regularFile(candidate) {
				evalJSModule = candidate
			}
		}
		if evalPyModule == "" {
			candidate := filepath.Join(packageSource, "eval", "py", "executor.ts")
			if regularFile(candidate) {
				evalPyModule = candidate
			}
		}
		if browserModule == "" {
			candidate := filepath.Join(packageSource, "tools", "browser.ts")
			if regularFile(candidate) {
				browserModule = candidate
			}
		}
		if browserTabsModule == "" {
			candidate := filepath.Join(packageSource, "tools", "browser", "tab-supervisor.ts")
			if regularFile(candidate) {
				browserTabsModule = candidate
			}
		}
		if computerModule == "" {
			candidate := filepath.Join(packageSource, "tools", "computer.ts")
			if regularFile(candidate) {
				computerModule = candidate
			}
		}
		if webSearchModule == "" {
			candidate := filepath.Join(packageSource, "web", "search", "index.ts")
			if regularFile(candidate) {
				webSearchModule = candidate
			}
		}
		if githubModule == "" {
			candidate := filepath.Join(packageSource, "tools", "gh.ts")
			if regularFile(candidate) {
				githubModule = candidate
			}
		}
		if sshModule == "" {
			candidate := filepath.Join(packageSource, "internal-urls", "ssh-protocol.ts")
			if regularFile(candidate) {
				sshModule = candidate
			}
		}
		if internalURLModule == "" {
			candidate := filepath.Join(packageSource, "internal-urls", "parse.ts")
			if regularFile(candidate) {
				internalURLModule = candidate
			}
		}
		if sshConnectionsModule == "" {
			candidate := filepath.Join(packageSource, "ssh", "connection-manager.ts")
			if regularFile(candidate) {
				sshConnectionsModule = candidate
			}
		}
		if hubModule == "" {
			candidate := filepath.Join(packageSource, "tools", "hub", "index.ts")
			if regularFile(candidate) {
				hubModule = candidate
			}
		}
		if launchClientModule == "" {
			candidate := filepath.Join(packageSource, "launch", "client.ts")
			if regularFile(candidate) {
				launchClientModule = candidate
			}
		}
		if launchBrokerModule == "" {
			candidate := filepath.Join(packageSource, "launch", "broker.ts")
			if regularFile(candidate) {
				launchBrokerModule = candidate
			}
		}
		if terminalWorkerModule == "" {
			candidate := filepath.Join(packageSource, "launch", "terminal-output-worker.ts")
			if regularFile(candidate) {
				terminalWorkerModule = candidate
			}
		}
		if imageGenModule == "" {
			candidate := filepath.Join(packageSource, "tools", "image-gen.ts")
			if regularFile(candidate) {
				imageGenModule = candidate
			}
		}
		if ttsModule == "" {
			candidate := filepath.Join(packageSource, "tools", "tts.ts")
			if regularFile(candidate) {
				ttsModule = candidate
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		resources := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Resources", "lspbridge"))
		if script == "" && regularFile(filepath.Join(resources, "bridge.mjs")) {
			script = filepath.Join(resources, "bridge.mjs")
		}
		packageRoot := filepath.Join(resources, "node_modules", "@oh-my-pi", "pi-coding-agent", "src", "lsp")
		if toolModule == "" && regularFile(filepath.Join(packageRoot, "tool.ts")) {
			toolModule = filepath.Join(packageRoot, "tool.ts")
		}
		if clientModule == "" && regularFile(filepath.Join(packageRoot, "client.ts")) {
			clientModule = filepath.Join(packageRoot, "client.ts")
		}
		packageSource := filepath.Join(resources, "node_modules", "@oh-my-pi", "pi-coding-agent", "src")
		if debugModule == "" && regularFile(filepath.Join(packageSource, "tools", "debug.ts")) {
			debugModule = filepath.Join(packageSource, "tools", "debug.ts")
		}
		if dapModule == "" && regularFile(filepath.Join(packageSource, "dap", "session.ts")) {
			dapModule = filepath.Join(packageSource, "dap", "session.ts")
		}
		if evalModule == "" && regularFile(filepath.Join(packageSource, "tools", "eval.ts")) {
			evalModule = filepath.Join(packageSource, "tools", "eval.ts")
		}
		if evalJSModule == "" && regularFile(filepath.Join(packageSource, "eval", "js", "context-manager.ts")) {
			evalJSModule = filepath.Join(packageSource, "eval", "js", "context-manager.ts")
		}
		if evalPyModule == "" && regularFile(filepath.Join(packageSource, "eval", "py", "executor.ts")) {
			evalPyModule = filepath.Join(packageSource, "eval", "py", "executor.ts")
		}
		if browserModule == "" && regularFile(filepath.Join(packageSource, "tools", "browser.ts")) {
			browserModule = filepath.Join(packageSource, "tools", "browser.ts")
		}
		if browserTabsModule == "" && regularFile(filepath.Join(packageSource, "tools", "browser", "tab-supervisor.ts")) {
			browserTabsModule = filepath.Join(packageSource, "tools", "browser", "tab-supervisor.ts")
		}
		if computerModule == "" && regularFile(filepath.Join(packageSource, "tools", "computer.ts")) {
			computerModule = filepath.Join(packageSource, "tools", "computer.ts")
		}
		if webSearchModule == "" && regularFile(filepath.Join(packageSource, "web", "search", "index.ts")) {
			webSearchModule = filepath.Join(packageSource, "web", "search", "index.ts")
		}
		if githubModule == "" && regularFile(filepath.Join(packageSource, "tools", "gh.ts")) {
			githubModule = filepath.Join(packageSource, "tools", "gh.ts")
		}
		if sshModule == "" && regularFile(filepath.Join(packageSource, "internal-urls", "ssh-protocol.ts")) {
			sshModule = filepath.Join(packageSource, "internal-urls", "ssh-protocol.ts")
		}
		if internalURLModule == "" && regularFile(filepath.Join(packageSource, "internal-urls", "parse.ts")) {
			internalURLModule = filepath.Join(packageSource, "internal-urls", "parse.ts")
		}
		if sshConnectionsModule == "" && regularFile(filepath.Join(packageSource, "ssh", "connection-manager.ts")) {
			sshConnectionsModule = filepath.Join(packageSource, "ssh", "connection-manager.ts")
		}
		if hubModule == "" && regularFile(filepath.Join(packageSource, "tools", "hub", "index.ts")) {
			hubModule = filepath.Join(packageSource, "tools", "hub", "index.ts")
		}
		if launchClientModule == "" && regularFile(filepath.Join(packageSource, "launch", "client.ts")) {
			launchClientModule = filepath.Join(packageSource, "launch", "client.ts")
		}
		if launchBrokerModule == "" && regularFile(filepath.Join(packageSource, "launch", "broker.ts")) {
			launchBrokerModule = filepath.Join(packageSource, "launch", "broker.ts")
		}
		if terminalWorkerModule == "" && regularFile(filepath.Join(packageSource, "launch", "terminal-output-worker.ts")) {
			terminalWorkerModule = filepath.Join(packageSource, "launch", "terminal-output-worker.ts")
		}
		if imageGenModule == "" && regularFile(filepath.Join(packageSource, "tools", "image-gen.ts")) {
			imageGenModule = filepath.Join(packageSource, "tools", "image-gen.ts")
		}
		if ttsModule == "" && regularFile(filepath.Join(packageSource, "tools", "tts.ts")) {
			ttsModule = filepath.Join(packageSource, "tools", "tts.ts")
		}
	}
	if !regularFile(script) || !regularFile(toolModule) || !regularFile(clientModule) || !regularFile(debugModule) || !regularFile(dapModule) || !regularFile(evalModule) || !regularFile(evalJSModule) || !regularFile(evalPyModule) || !regularFile(browserModule) || !regularFile(browserTabsModule) || !regularFile(computerModule) || !regularFile(webSearchModule) || !regularFile(githubModule) || !regularFile(sshModule) || !regularFile(internalURLModule) || !regularFile(sshConnectionsModule) || !regularFile(hubModule) || !regularFile(launchClientModule) || !regularFile(launchBrokerModule) || !regularFile(terminalWorkerModule) || !regularFile(imageGenModule) || !regularFile(ttsModule) {
		return errors.New("OMP runtime assets are missing; run `bun install --cwd runtime-js`")
	}
	runtime.runtime, runtime.script, runtime.toolModule, runtime.clientModule = runtimePath, script, toolModule, clientModule
	runtime.debugModule, runtime.dapModule = debugModule, dapModule
	runtime.evalModule, runtime.evalJSModule, runtime.evalPyModule = evalModule, evalJSModule, evalPyModule
	runtime.browserModule, runtime.browserTabsModule = browserModule, browserTabsModule
	runtime.computerModule, runtime.webSearchModule, runtime.githubModule = computerModule, webSearchModule, githubModule
	runtime.sshModule, runtime.internalURLModule, runtime.sshConnectionsModule = sshModule, internalURLModule, sshConnectionsModule
	runtime.hubModule, runtime.launchClientModule, runtime.launchBrokerModule, runtime.terminalWorkerModule = hubModule, launchClientModule, launchBrokerModule, terminalWorkerModule
	runtime.imageGenModule, runtime.ttsModule = imageGenModule, ttsModule
	return nil
}
