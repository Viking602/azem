//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPTYStreamResizeCancelAndExit(t *testing.T) {
	if testing.Short() {
		t.Skip("PTY smoke test")
	}
	temp := t.TempDir()
	binary := filepath.Join(temp, "azem")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build PTY binary: %v\n%s", err, output)
	}

	command := exec.Command(binary)
	command.Dir = temp
	command.Env = append(os.Environ(),
		"HOME="+temp,
		"XDG_CONFIG_HOME="+filepath.Join(temp, "config"),
		"XDG_DATA_HOME="+filepath.Join(temp, "data"),
		"XDG_STATE_HOME="+filepath.Join(temp, "state"),
		"TERM=xterm-256color",
		"AZEM_FAKE_PROVIDER=1",
		"AZEM_REDUCED_MOTION=1",
		"NO_COLOR=1",
	)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("start PTY: %v", err)
	}
	defer terminal.Close()
	reads := make(chan ptyRead, 16)
	go readPTY(terminal, reads)

	output := &bytes.Buffer{}
	readUntil(t, reads, output, "READY FOR A TASK", 15*time.Second)
	if _, err := terminal.Write([]byte("\x1b[Z")); err != nil {
		t.Fatalf("enable yolo approval mode: %v", err)
	}
	readUntil(t, reads, output, "⚠ FULL ACCESS", 5*time.Second)
	output.Reset()
	if _, err := terminal.Write([]byte("\x1b[Z")); err != nil {
		t.Fatalf("restore prompt approval mode: %v", err)
	}
	readUntil(t, reads, output, "☝︎ ASK", 5*time.Second)
	output.Reset()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("resize PTY: %v", err)
	}
	if _, err := terminal.Write([]byte("hello from automated pty\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	readUntil(t, reads, output, "Deterministic ", 5*time.Second)
	readUntil(t, reads, output, "response: ", 5*time.Second)
	readUntil(t, reads, output, "automated ", 5*time.Second)
	readUntil(t, reads, output, "pty ", 5*time.Second)
	output.Reset()

	longPrompt := strings.Repeat("cancel-me ", 80)
	if _, err := terminal.Write([]byte(longPrompt + "\r")); err != nil {
		t.Fatalf("write long prompt: %v", err)
	}
	readUntil(t, reads, output, "◆ ", 10*time.Second)
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatalf("send cancellation: %v", err)
	}
	// Bubble Tea updates the status line in place, so "Cancelling" -> "Cancelled"
	// emits only the changed suffix rather than a second full status label.
	readUntil(t, reads, output, "ed · ☝︎ ASK", 5*time.Second)
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatalf("send exit: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PTY process exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("PTY process did not exit after idle Ctrl+C")
	}
}

func TestPTYRecoveryApprovalDoesNotReplayPendingEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("PTY smoke test")
	}
	temp := t.TempDir()
	notePath := filepath.Join(temp, "note.txt")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedPendingEditApproval(t, temp)
	binary := filepath.Join(temp, "azem")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build PTY binary: %v\n%s", err, output)
	}
	command := exec.Command(binary)
	command.Dir = temp
	command.Env = append(os.Environ(),
		"HOME="+temp,
		"XDG_CONFIG_HOME="+filepath.Join(temp, "config"),
		"XDG_DATA_HOME="+filepath.Join(temp, "data"),
		"XDG_STATE_HOME="+filepath.Join(temp, "state"),
		"TERM=xterm-256color",
		"AZEM_FAKE_PROVIDER=1",
		"NO_COLOR=1",
	)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("start recovery PTY: %v", err)
	}
	defer terminal.Close()
	reads := make(chan ptyRead, 16)
	go readPTY(terminal, reads)
	output := &bytes.Buffer{}
	readUntil(t, reads, output, "Pending approval", 15*time.Second)
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatalf("open recovered approval: %v", err)
	}
	readUntil(t, reads, output, "APPROVAL REQUIRED", 5*time.Second)
	output.Reset()
	if _, err := terminal.Write([]byte("d")); err != nil {
		t.Fatalf("deny recovered approval: %v", err)
	}
	readUntil(t, reads, output, "Ready", 5*time.Second)
	content, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "before\n" {
		t.Fatalf("pending edit replayed during PTY recovery: %q", content)
	}
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatalf("exit recovery PTY: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recovery PTY process exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("recovery PTY process did not exit")
	}
}

func TestPTYSkillsOverlayAndReload(t *testing.T) {
	if testing.Short() {
		t.Skip("PTY smoke test")
	}
	temp := t.TempDir()
	skillDir := filepath.Join(temp, ".azem", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: initial skill description\n---\nUse this skill for PTY verification.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(temp, "azem")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build PTY binary: %v\n%s", err, output)
	}
	command := exec.Command(binary)
	command.Dir = temp
	command.Env = append(os.Environ(),
		"HOME="+temp,
		"XDG_CONFIG_HOME="+filepath.Join(temp, "config"),
		"XDG_DATA_HOME="+filepath.Join(temp, "data"),
		"XDG_STATE_HOME="+filepath.Join(temp, "state"),
		"TERM=xterm-256color",
		"AZEM_FAKE_PROVIDER=1",
		"NO_COLOR=1",
	)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 110})
	if err != nil {
		t.Fatalf("start skills PTY: %v", err)
	}
	defer terminal.Close()
	reads := make(chan ptyRead, 16)
	go readPTY(terminal, reads)
	output := &bytes.Buffer{}
	readUntil(t, reads, output, "READY FOR A TASK", 15*time.Second)
	output.Reset()
	if _, err := terminal.Write([]byte("/skills\r")); err != nil {
		t.Fatalf("open skills overlay: %v", err)
	}
	readUntil(t, reads, output, "demo", 5*time.Second)
	readUntil(t, reads, output, "verify", 5*time.Second)
	readUntil(t, reads, output, "simplify", 5*time.Second)
	readUntil(t, reads, output, "skill-author", 5*time.Second)
	readUntil(t, reads, output, "initial skill description", 5*time.Second)
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: reloaded skill description\n---\nUse this updated skill for PTY verification.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if _, err := terminal.Write([]byte("r")); err != nil {
		t.Fatalf("reload skills: %v", err)
	}
	readUntil(t, reads, output, "reloaded skill description", 5*time.Second)
	output.Reset()
	if _, err := terminal.Write([]byte{27}); err != nil {
		t.Fatalf("close skills overlay: %v", err)
	}
	readUntil(t, reads, output, "READY FOR A TASK", 5*time.Second)
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatalf("exit skills PTY: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("skills PTY process exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("skills PTY process did not exit")
	}
}

func seedPendingEditApproval(t *testing.T, workspace string) {
	t.Helper()
	ctx := context.Background()
	database := filepath.Join(workspace, "config", "azem", "azem.db")
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentservice.NewService(store, workspace)
	if err != nil {
		_ = store.Close(ctx)
		t.Fatal(err)
	}
	run, err := service.StartRun(ctx, "edit note")
	if err != nil {
		t.Fatal(err)
	}
	readArguments, _ := json.Marshal(map[string]string{"path": "note.txt"})
	read, err := service.ExecuteTool(ctx, run, tool.Call{ID: "read-1", Name: coding.ToolReadFile, Arguments: readArguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var readResult coding.ReadFileToolResult
	if err := json.Unmarshal(read.Result.Structured, &readResult); err != nil {
		t.Fatal(err)
	}
	editArguments, _ := json.Marshal(map[string]string{"input": readResult.Header + "\nreplace 1:\n+after\n"})
	pending, err := service.ExecuteTool(ctx, run, tool.Call{ID: "edit-1", Name: coding.ToolEditHashline, Arguments: editArguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Approval == nil {
		t.Fatal("seeded edit did not require approval")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type ptyRead struct {
	data []byte
	err  error
}

func readPTY(terminal *os.File, reads chan<- ptyRead) {
	defer close(reads)
	buffer := make([]byte, 4096)
	for {
		count, err := terminal.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			reads <- ptyRead{data: data}
			if response := terminalQueryResponse(data); len(response) > 0 {
				if _, writeErr := terminal.Write(response); writeErr != nil {
					reads <- ptyRead{err: fmt.Errorf("answer terminal query: %w", writeErr)}
					return
				}
			}
		}
		if err != nil {
			reads <- ptyRead{err: err}
			return
		}
	}
}

func terminalQueryResponse(data []byte) []byte {
	var response []byte
	for range bytes.Count(data, []byte("\x1b]11;?\x07")) {
		response = append(response, "\x1b]11;rgb:0000/0000/0000\x1b\\"...)
	}
	for range bytes.Count(data, []byte("\x1b[c")) {
		response = append(response, "\x1b[?1;2c"...)
	}
	return response
}

func readUntil(t *testing.T, reads <-chan ptyRead, output *bytes.Buffer, needle string, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if strings.Contains(output.String(), needle) {
			return
		}
		select {
		case read, ok := <-reads:
			if !ok {
				t.Fatalf("PTY closed waiting for %q\n%s", needle, fmt.Sprintf("%q", output.String()))
			}
			if read.err != nil {
				t.Fatalf("read PTY waiting for %q: %v\n%s", needle, read.err, output.String())
			}
			output.Write(read.data)
		case <-timer.C:
			t.Fatalf("timeout waiting for %q\n%s", needle, fmt.Sprintf("%q", output.String()))
		}
	}
}
