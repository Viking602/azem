package background

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestManagerStartLogsStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command fixture is POSIX-specific")
	}
	root := t.TempDir()
	manager, err := NewManager(Options{
		Root: root, LogDir: filepath.Join(root, "logs"), MaxLogBytes: 4096, MaxLogLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	process, err := manager.Start(context.Background(), StartRequest{
		Name: "probe", Command: `printf 'ready\n'; while :; do sleep 1; done`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if process.State != "running" || process.PID <= 0 || process.Name != "probe" {
		t.Fatalf("started process = %+v", process)
	}

	var snapshot LogSnapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err = manager.Logs(process.ID, -1, 20)
		if err == nil && strings.Contains(strings.Join(snapshot.Lines, "\n"), "ready") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || !strings.Contains(strings.Join(snapshot.Lines, "\n"), "ready") {
		t.Fatalf("log snapshot = %+v, error = %v", snapshot, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Stop(ctx, process.ID); err != nil {
		t.Fatal(err)
	}
	items := manager.List()
	if len(items) != 1 || items[0].State != "stopped" || items[0].FinishedAt.IsZero() {
		t.Fatalf("stopped processes = %+v", items)
	}
}

func TestManagerCloseLeavesStartedProcessLogging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command fixture is POSIX-specific")
	}
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	manager, err := NewManager(Options{Root: root, LogDir: logDir, MaxLogBytes: 4096, MaxLogLines: 20})
	if err != nil {
		t.Fatal(err)
	}
	process, err := manager.Start(context.Background(), StartRequest{Command: `sleep 0.2; printf 'after-close\n'`})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminatePID(process.PID) })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		payload, readErr := os.ReadFile(process.LogPath)
		if readErr == nil && strings.Contains(string(payload), "after-close") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	payload, _ := os.ReadFile(process.LogPath)
	t.Fatalf("process stopped logging after manager close: %q", payload)
}

func TestLogWriterBoundsMemoryAndDiskWhileContinuingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "background.log")
	writer, err := newLogWriter(path, 96, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	for index := 0; index < 100; index++ {
		if _, err := writer.Write([]byte("0123456789-line\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("final-line\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 96 {
		t.Fatalf("retained log size = %d, want <= 96", info.Size())
	}
	lines, _, truncated := writer.Snapshot()
	if !truncated || len(lines) > 5 || len(lines) == 0 || lines[len(lines)-1] != "final-line" {
		t.Fatalf("bounded snapshot lines=%q truncated=%t", lines, truncated)
	}
}

func TestManagerRecoversMetadataAndRetainedTail(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "bg_recovered.log")
	if err := os.WriteFile(logPath, []byte("old\nlatest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := `{"id":"bg_recovered","name":"done","command":"probe","cwd":"` + root + `","pid":0,"state":"exited","exitCode":0,"startedAt":"2026-01-01T00:00:00Z","logPath":"` + logPath + `","generation":2}`
	if err := os.WriteFile(filepath.Join(logDir, "bg_recovered.json"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager(Options{Root: root, LogDir: logDir, MaxLogBytes: 1024, MaxLogLines: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	items := manager.List()
	if len(items) != 1 || items[0].ID != "bg_recovered" || items[0].State != "exited" {
		t.Fatalf("recovered processes = %+v", items)
	}
	snapshot, err := manager.Logs("bg_recovered", -1, 10)
	if err != nil || strings.Join(snapshot.Lines, ",") != "old,latest" {
		t.Fatalf("recovered logs = %+v, error = %v", snapshot, err)
	}
}
