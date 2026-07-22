package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"
)

func TestShellUsesWorkspaceAndReturnsStructuredExit(t *testing.T) {
	root := t.TempDir()
	driver := newShellDriver(root, "prompt", "prompt")
	arguments, _ := json.Marshal(shellInput{Command: "pwd; printf shell-ok"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "shell-1", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content, root) || !strings.Contains(result.Content, "shell-ok") {
		t.Fatalf("shell result=%+v", result)
	}
	var output shellOutput
	if err := json.Unmarshal(result.Structured, &output); err != nil {
		t.Fatal(err)
	}
	if output.ExitCode != 0 || output.Truncated {
		t.Fatalf("structured shell output=%+v", output)
	}
}

func TestShellDefinitionPreservesApprovalAndNetworkPolicy(t *testing.T) {
	definition := newShellDriver(t.TempDir(), "allow", "prompt").Definition()
	if definition.Metadata["approval"] != "allow" || definition.Metadata["network"] != "prompt" {
		t.Fatalf("shell metadata=%#v", definition.Metadata)
	}
}

func TestShellCancellationTerminatesDescendantsAndReturnsPromptly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	root := t.TempDir()
	driver := newShellDriver(root, "prompt", "prompt")
	arguments, _ := json.Marshal(shellInput{Command: "sleep 10"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	result, err := driver.Execute(ctx, tool.Call{ID: "shell-cancel", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("cancelled shell returned after %s, want less than one second", elapsed)
	}
	if !result.IsError || !strings.Contains(result.Content, "context_cancelled") {
		t.Fatalf("cancelled shell result=%+v", result)
	}
	time.Sleep(450 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child-finished")); !os.IsNotExist(err) {
		t.Fatalf("shell descendant survived cancellation: %v", err)
	}
}

func TestShellDoesNotStartWhenContextIsAlreadyCancelled(t *testing.T) {
	root := t.TempDir()
	driver := newShellDriver(root, "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "printf started > marker"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := driver.Execute(ctx, tool.Call{ID: "already-cancelled", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("already-cancelled command started: %v", err)
	}
}

func TestShellReapsResidualGroupAfterNormalShellExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process groups")
	}
	command := exec.Command("/bin/sh", "-c", "sleep 10 &")
	owner, err := newShellProcessOwner(command)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	started := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Assign(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Terminate(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("residual cleanup took %s", elapsed)
	}
}

func TestShellDriverReapsResidualChildHoldingOutputPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process groups")
	}
	root := t.TempDir()
	driver := newShellDriver(root, "allow", "deny")
	pidPath := filepath.Join(root, "child.pid")
	command := fmt.Sprintf("AZEM_SHELL_RESIDUAL_HELPER=1 %q -test.run=TestShellResidualHelper -- %q", os.Args[0], pidPath)
	arguments, _ := json.Marshal(shellInput{Command: command})
	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "residual", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("driver waited for residual child for %s", elapsed)
	}
	rawPID, err := os.ReadFile(filepath.Join(root, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !shellProcessExists(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("residual child %d still exists", pid)
}

func TestShellResidualHelper(t *testing.T) {
	if os.Getenv("AZEM_SHELL_RESIDUAL_HELPER") != "1" {
		return
	}
	pidPath := os.Args[len(os.Args)-1]
	child := exec.Command("sleep", "10")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestShellOutputLimitKillsFloodPromptly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell utilities")
	}
	runtimeCtx := newShellRuntime(context.Background(), ShellOptions{MaxContextOutputBytes: 1024, MaxArtifactOutputBytes: 2048, StopOnOutputLimit: true, MaxConcurrency: 1})
	driver := newRuntimeShellDriver(t.TempDir(), "allow", "deny", runtimeCtx)
	arguments, _ := json.Marshal(shellInput{Command: "yes flood"})
	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "flood", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "output_limit") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("output flood returned after %s", elapsed)
	}
}

func TestShellSinkFailuresAreSurfaced(t *testing.T) {
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "sleep 10"})
	t.Run("started", func(t *testing.T) {
		result, err := driver.Execute(context.Background(), tool.Call{ID: "sink-start", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error { return fmt.Errorf("started broke") })
		if err != nil || !result.IsError || !strings.Contains(result.Content, "started broke") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("running", func(t *testing.T) {
		result, err := driver.Execute(context.Background(), tool.Call{ID: "sink-running", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error {
			if update.Kind == "progress" {
				return fmt.Errorf("progress broke")
			}
			return nil
		})
		if err != nil || !result.IsError || !strings.Contains(result.Content, "update_sink_failure") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("finished", func(t *testing.T) {
		quick, _ := json.Marshal(shellInput{Command: "printf done"})
		result, err := driver.Execute(context.Background(), tool.Call{ID: "sink-finish", Name: ToolShell, Arguments: quick}, func(update tool.Update) error {
			if update.Kind == "finished" {
				return fmt.Errorf("finished broke")
			}
			return nil
		})
		if err != nil || !result.IsError || !strings.Contains(result.Content, "finished broke") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestShellRejectsDetachedForms(t *testing.T) {
	for _, command := range []string{"sleep 10 &", "nohup sleep 10", "setsid sleep 10", "disown", "daemonize app"} {
		if !rejectDetached(command) {
			t.Errorf("did not reject %q", command)
		}
	}
	for _, command := range []string{"printf one && printf two", `printf '&'`, `printf \&`} {
		if rejectDetached(command) {
			t.Errorf("rejected foreground command %q", command)
		}
	}
}

func TestServiceSharesShellConcurrencyAndCloseDrainsRegistry(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir(), WithWorkspacePolicy(true, "allow", "deny"), WithShellOptions(ShellOptions{MaxConcurrency: 1}))
	if err != nil {
		t.Fatal(err)
	}
	driversA, err := service.WorkspaceDrivers(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driversB, err := service.WorkspaceDrivers(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	findShell := func(drivers []tool.Driver) tool.Driver {
		for _, driver := range drivers {
			if driver.Definition().Name == ToolShell {
				return driver
			}
		}
		return nil
	}
	a, b := findShell(driversA), findShell(driversB)
	if a == nil || b == nil {
		t.Fatal("shell driver missing")
	}
	arguments, _ := json.Marshal(shellInput{Command: "sleep 10"})
	done := make(chan struct{}, 2)
	for id, driver := range map[string]tool.Driver{"shared-a": a, "shared-b": b} {
		go func(id string, driver tool.Driver) {
			_, _ = driver.Execute(ctx, tool.Call{ID: id, Name: ToolShell, Arguments: arguments}, nil)
			done <- struct{}{}
		}(id, driver)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(service.ActiveShellExecutions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(service.ActiveShellExecutions()); got != 1 {
		t.Fatalf("active=%d, shared concurrency limit not enforced", got)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	<-done
	<-done
	if got := service.ActiveShellExecutions(); len(got) != 0 {
		t.Fatalf("registry not drained: %+v", got)
	}
}

func TestWorkspacePolicyFiltersWritesAndShell(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir(), WithWorkspacePolicy(false, "deny", "deny"))
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]bool{}
	for _, definition := range service.ToolDefinitions() {
		definitions[definition.Name] = true
	}
	for _, forbidden := range []string{coding.ToolEditHashline, coding.ToolWriteFile, coding.ToolGofmt, ToolShell} {
		if definitions[forbidden] {
			t.Fatalf("workspace policy exposed %q", forbidden)
		}
	}
	if !definitions[coding.ToolReadFile] {
		t.Fatal("workspace policy removed read_file")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceExposesShellAccordingToPolicy(t *testing.T) {
	for _, policy := range []string{"allow", "prompt", "deny"} {
		t.Run(policy, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlitestore.Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewService(store, t.TempDir(), WithWorkspacePolicy(true, policy, "prompt"))
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close(ctx)
			exposed := false
			for _, definition := range service.ToolDefinitions() {
				if definition.Name == ToolShell {
					exposed = true
				}
			}
			if exposed != (policy != "deny") {
				t.Fatalf("shell policy %q exposed=%v", policy, exposed)
			}
		})
	}
}

func TestShellRequiresApprovalAndPersistsActionAttempt(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	service, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartRun(ctx, "create marker")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(shellInput{Command: "printf approved > marker.txt"})
	call := tool.Call{ID: "shell-approved", Name: ToolShell, Arguments: arguments}
	first, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Approval == nil || first.Executed {
		t.Fatalf("first shell execution=%+v", first)
	}
	if err := service.ResolveApproval(ctx, run, call.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	second, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Executed || second.Result.IsError {
		t.Fatalf("approved shell execution=%+v", second)
	}
	contents, err := os.ReadFile(filepath.Join(root, "marker.txt"))
	if err != nil || string(contents) != "approved" {
		t.Fatalf("marker=%q error=%v", contents, err)
	}
	if err := service.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='action_attempt'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("durable shell action attempts=%d", attempts)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
