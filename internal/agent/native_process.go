package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/venat/tool"
)

const nativeOutputLimit = 4 << 20

func nativeSearchPath() string {
	paths := []string{os.Getenv("PATH"), "/opt/homebrew/bin", "/usr/local/bin"}
	if directory, err := os.UserHomeDir(); err == nil {
		for _, suffix := range []string{"go/bin", ".cargo/bin", ".local/bin"} {
			paths = append(paths, filepath.Join(directory, suffix))
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func nativeExecutable(program string) (string, error) {
	if strings.ContainsAny(program, "/\\") {
		return program, nil
	}
	if path, err := exec.LookPath(program); err == nil {
		return path, nil
	} else if !errors.Is(err, exec.ErrNotFound) {
		return "", err
	}
	for _, directory := range filepath.SplitList(nativeSearchPath()) {
		if !filepath.IsAbs(directory) {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(directory, program)); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("native executable %q is not installed or on PATH", program)
}

// Native tools share process ownership with shell commands, including descendant
// cleanup. Protocol sessions belong to one workspace, conversation and agent.
type nativeHost struct {
	ctx       context.Context
	mu        sync.Mutex
	processes map[string]*nativeProcess
	closed    bool
}

type nativeProcess struct {
	stdin       io.WriteCloser
	reader      io.ReadCloser
	stdout      *bufio.Reader
	supervisor  *shellSupervisor
	done        chan struct{}
	err         error
	stderr      *nativeBuffer
	logs        *nativeBuffer
	gate        chan struct{}
	stop        sync.Once
	seq         int
	initialized bool
	data        map[string]string
	events      []json.RawMessage
	started     time.Time
}

type nativeBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *nativeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := len(p)
	if count > nativeOutputLimit {
		p = p[count-nativeOutputLimit:]
		b.truncated = true
	}
	if len(b.data)+len(p) > nativeOutputLimit {
		b.data = b.data[len(b.data)+len(p)-nativeOutputLimit:]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return count, nil
}

func (b *nativeBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	prefix := ""
	if b.truncated {
		prefix = "[earlier output truncated]\n"
	}
	return prefix + string(b.data)
}

func newNativeHost(ctx context.Context) *nativeHost {
	if ctx == nil {
		ctx = context.Background()
	}
	return &nativeHost{ctx: ctx, processes: make(map[string]*nativeProcess)}
}

func nativeScope(ctx context.Context, root, kind, name string) string {
	caller, _ := InvocationFromContext(ctx)
	return strings.Join([]string{root, caller.SessionID, firstString(caller.AgentID, "Main"), kind, name}, "\x00")
}

func (h *nativeHost) start(key, root, program string, args []string, capture bool) (*nativeProcess, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.ctx.Err() != nil {
		return nil, errors.New("native tool host is closed")
	}
	if current := h.processes[key]; current != nil {
		select {
		case <-current.done:
			current.close()
			delete(h.processes, key)
		default:
			return current, nil
		}
	}
	// Retain completed hub logs until capacity is needed, then reclaim readers.
	if len(h.processes) >= 64 {
		for name, process := range h.processes {
			select {
			case <-process.done:
				process.close()
				delete(h.processes, name)
			default:
			}
		}
	}
	if len(h.processes) >= 64 {
		return nil, errors.New("native process limit reached; close unused sessions")
	}
	executable, err := nativeExecutable(program)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, args...)
	command.Dir, command.WaitDelay = root, 200*time.Millisecond
	command.Env = append(command.Environ(), "PATH="+nativeSearchPath())
	supervisor, err := newShellSupervisor(command)
	if err != nil {
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = supervisor.Close()
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = supervisor.Close()
		return nil, err
	}
	p := &nativeProcess{stdin: stdin, reader: reader, stdout: bufio.NewReaderSize(reader, 32<<10), supervisor: supervisor, done: make(chan struct{}), stderr: &nativeBuffer{}, logs: &nativeBuffer{}, gate: make(chan struct{}, 1), data: make(map[string]string), started: time.Now()}
	command.Stdout, command.Stderr = writer, p.stderr
	if capture {
		command.Stdout = p.logs
	}
	if err := supervisor.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = stdin.Close()
		_ = supervisor.Close()
		return nil, fmt.Errorf("start %s: %w", program, err)
	}
	_ = writer.Close()
	p.gate <- struct{}{}
	h.processes[key] = p
	stopContext := context.AfterFunc(h.ctx, p.close)
	go func() {
		p.err = supervisor.Wait()
		_ = supervisor.Terminate()
		_ = supervisor.Close()
		_ = stdin.Close()
		if capture {
			_ = reader.Close()
		}
		close(p.done)
		stopContext()
	}()
	return p, nil
}

func (p *nativeProcess) close() {
	p.stop.Do(func() { _ = p.stdin.Close(); _ = p.supervisor.Terminate(); _ = p.reader.Close() })
}

func (p *nativeProcess) lock(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.gate:
		if ctx.Err() != nil {
			p.gate <- struct{}{}
			return nil, ctx.Err()
		}
		stop := context.AfterFunc(ctx, p.close)
		return func() {
			stop()
			select {
			case <-p.done:
				_ = p.reader.Close()
			default:
			}
			p.gate <- struct{}{}
		}, nil
	}
}

func (h *nativeHost) close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	processes := make([]*nativeProcess, 0, len(h.processes))
	for _, process := range h.processes {
		processes = append(processes, process)
	}
	h.mu.Unlock()
	for _, process := range processes {
		process.close()
	}
	for _, process := range processes {
		select {
		case <-process.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func nativeRun(ctx context.Context, root, stdin, program string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	host := newNativeHost(ctx)
	p, err := host.start("command", root, program, args, true)
	if err != nil {
		return "", err
	}
	defer host.close(context.Background())
	if _, err = io.WriteString(p.stdin, stdin); err != nil {
		p.close()
		return "", err
	}
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-ctx.Done():
		p.close()
		<-p.done
		return p.logs.text(), ctx.Err()
	}
	if p.err != nil {
		return p.logs.text(), fmt.Errorf("%s failed: %w: %s", program, p.err, p.stderr.text())
	}
	return p.logs.text(), nil
}

func (p *nativeProcess) writeFrame(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) > nativeOutputLimit {
		return errors.New("native request exceeds 4 MiB")
	}
	_, err = fmt.Fprintf(p.stdin, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	return err
}

func (p *nativeProcess) readFrame() (json.RawMessage, error) {
	length, headerBytes := -1, 0
	for {
		lineBytes, err := p.stdout.ReadSlice('\n')
		if err != nil {
			return nil, fmt.Errorf("native protocol ended: %w: %s", err, p.stderr.text())
		}
		line := string(lineBytes)
		headerBytes += len(line)
		if headerBytes > 8192 {
			return nil, errors.New("native protocol header exceeds 8 KiB")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(key, "Content-Length") {
			if length != -1 {
				return nil, errors.New("duplicate native Content-Length")
			}
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 || length > nativeOutputLimit {
		return nil, errors.New("native protocol frame must be at most 4 MiB")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(p.stdout, payload); err != nil {
		return nil, err
	}
	if !json.Valid(payload) {
		return nil, errors.New("invalid native protocol JSON")
	}
	return payload, nil
}

func nativeJSONResult(call tool.Call, value any) (tool.Result, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return tool.Result{}, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: pretty.String(), Structured: encoded}, nil
}
