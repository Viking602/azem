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
	"github.com/Viking602/venat/tool"
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

func TestShellFDDuplicationRedirectionIsForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX descriptor redirection")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: `printf shell-ok 2>&1 | tail -20`})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "fd-redirection", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content, "shell-ok") {
		t.Fatalf("descriptor redirection result=%+v", result)
	}
}

func TestShellDescriptorPreservesApprovalAndNetworkPolicy(t *testing.T) {
	descriptor, err := DescribeTool(newShellDriver(t.TempDir(), "allow", "prompt"))
	if err != nil {
		t.Fatal(err)
	}
	policy := descriptor.PolicyForCall(tool.Call{Name: ToolShell, Arguments: json.RawMessage(`{}`)})
	if policy.Metadata["approval"] != "allow" || policy.Metadata["network"] != "prompt" || policy.Metadata["platform"] != runtime.GOOS {
		t.Fatalf("shell metadata=%#v", policy.Metadata)
	}
	definition := descriptor.WireDefinition
	for _, required := range []string{"supervised foreground command", "background operators and known detach primitives are rejected"} {
		if !strings.Contains(definition.Description, required) {
			t.Fatalf("shell definition omitted %q: %s", required, definition.Description)
		}
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

func TestSilentShellCommandEmitsProgressWithoutRepeatedMessages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "sleep 1"})
	var updates []tool.Update
	result, err := driver.Execute(context.Background(), tool.Call{ID: "silent", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil || result.IsError {
		t.Fatalf("silent shell result=%+v error=%v", result, err)
	}
	progress := 0
	for _, update := range updates {
		if update.Kind == "progress" {
			progress++
			if update.Message != "" || update.Data["output_bytes"] != "0" {
				t.Fatalf("silent shell emitted visible progress update=%+v", update)
			}
		}
	}
	if progress == 0 {
		t.Fatal("silent shell stopped emitting progress heartbeats")
	}
}

func TestShellOutputActivityExtendsTimeoutAndStreamsLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell timing and process probes")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{
		Command:        `i=0; while [ "$i" -lt 7 ]; do i=$((i+1)); printf 'tick-%s\n' "$i"; sleep 0.2; done`,
		TimeoutSeconds: 1,
	})
	startedAlive := false
	sawProgressLog := false
	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "streaming", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error {
		if update.Kind == tool.UpdateProgress && update.Data["phase"] == "started" {
			pid, parseErr := strconv.Atoi(update.Data["pid"])
			startedAlive = parseErr == nil && shellProcessExists(pid)
		}
		if update.Kind == tool.UpdateProgress && update.Data["phase"] == "progress" && strings.Contains(update.Data["output"], "tick-") {
			sawProgressLog = true
		}
		return nil
	})
	if err != nil || result.IsError {
		t.Fatalf("streaming shell result=%+v error=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed <= time.Second {
		t.Fatalf("streaming shell completed after %s, timeout was not extended by output", elapsed)
	}
	if !startedAlive {
		t.Fatal("started update was emitted before the process was alive")
	}
	if !sawProgressLog {
		t.Fatal("progress updates did not include cumulative command output")
	}
	if !strings.Contains(result.Content, "tick-7") {
		t.Fatalf("final output=%q", result.Content)
	}
}

func TestResolveShellTimeoutsLetsModelChooseWallClockWithinConfiguredMax(t *testing.T) {
	maxWall := 20 * time.Minute
	inactivity, wall, err := resolveShellTimeouts(shellInput{WallClockSeconds: 900}, maxWall)
	if err != nil || wall != 15*time.Minute || inactivity != 15*time.Minute {
		t.Fatalf("model wall clock = inactivity %s wall %s err %v", inactivity, wall, err)
	}
	inactivity, wall, err = resolveShellTimeouts(shellInput{TimeoutSeconds: 30}, maxWall)
	if err != nil || inactivity != 30*time.Second || wall != maxWall {
		t.Fatalf("legacy inactivity = inactivity %s wall %s err %v", inactivity, wall, err)
	}
	if _, _, err := resolveShellTimeouts(shellInput{WallClockSeconds: 21 * 60}, maxWall); err == nil {
		t.Fatal("wall clock above configured maximum was accepted")
	}
	if _, _, err := resolveShellTimeouts(shellInput{TimeoutSeconds: 21 * 60}, maxWall); err == nil {
		t.Fatal("inactivity above configured maximum was accepted")
	}
	inactivity, wall, err = resolveShellTimeouts(shellInput{WallClockSeconds: 20, TimeoutSeconds: 5}, 20*time.Second)
	if err != nil || wall != 20*time.Second || inactivity != 5*time.Second {
		t.Fatalf("explicit pair = inactivity %s wall %s err %v", inactivity, wall, err)
	}
}

func TestShellStdinFeedsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "cat", Stdin: "scripted-keystroke\n"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "stdin", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content, "scripted-keystroke") {
		t.Fatalf("stdin shell result=%+v", result)
	}
	if _, ok := driver.Definition().InputSchema.Properties["stdin"]; !ok {
		t.Fatal("shell definition omitted stdin")
	}
}

func TestUpdateShellMaxWallClockChangesDefinition(t *testing.T) {
	shellRuntime := newShellRuntime(context.Background(), ShellOptions{MaxWallClockDuration: 10 * time.Minute})
	driver := newRuntimeShellDriver(t.TempDir(), "allow", "deny", shellRuntime)
	descriptor, err := DescribeTool(driver)
	if err != nil {
		t.Fatal(err)
	}
	if policy := descriptor.PolicyForCall(tool.Call{Name: ToolShell, Arguments: json.RawMessage(`{}`)}); policy.Metadata["max_wall_clock_seconds"] != "600" {
		t.Fatalf("initial metadata = %#v", policy.Metadata)
	}
	shellRuntime.updateMaxWallClock(20 * time.Minute)
	descriptor, err = DescribeTool(driver)
	if err != nil {
		t.Fatal(err)
	}
	if policy := descriptor.PolicyForCall(tool.Call{Name: ToolShell, Arguments: json.RawMessage(`{}`)}); policy.Metadata["max_wall_clock_seconds"] != "1200" {
		t.Fatalf("updated metadata = %#v", policy.Metadata)
	}
}

func TestShellHonorsModelRequestedWallClock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	shellRuntime := newShellRuntime(context.Background(), ShellOptions{MaxWallClockDuration: 10 * time.Second})
	driver := newRuntimeShellDriver(t.TempDir(), "allow", "deny", shellRuntime)
	arguments, _ := json.Marshal(shellInput{
		Command:          `i=0; while [ "$i" -lt 40 ]; do i=$((i+1)); printf 'tick\n'; sleep 0.05; done`,
		WallClockSeconds: 1,
	})
	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "chosen-wall", Name: ToolShell, Arguments: arguments}, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "command stopped: wall_clock_timeout") {
		t.Fatalf("model wall-clock shell result=%+v", result)
	}
	if elapsed < 800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("model wall-clock shell returned after %s", elapsed)
	}
	definition := driver.Definition()
	if !strings.Contains(definition.Description, "1 to 10 seconds") {
		t.Fatalf("shell definition omitted configured wall-clock ceiling: %s", definition.Description)
	}
	descriptor, err := DescribeTool(driver)
	if err != nil {
		t.Fatal(err)
	}
	policy := descriptor.PolicyForCall(tool.Call{Name: ToolShell, Arguments: arguments})
	if policy.Metadata["max_wall_clock_seconds"] != "10" {
		t.Fatalf("shell metadata = %#v", policy.Metadata)
	}
}

func TestShellRejectsBackgroundOperator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX background operator")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "sleep 1 & wait"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "background-unbounded", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "background operators that can escape process supervision are not permitted") {
		t.Fatalf("background result=%+v", result)
	}
}

func TestShellRejectsBoundedBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX background operator")
	}
	root := t.TempDir()
	driver := newShellDriver(root, "allow", "deny")
	arguments, _ := json.Marshal(shellInput{
		Command:          "(sleep 1 && touch child-finished) & wait",
		WallClockSeconds: 2,
	})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "background-bounded", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "background operators that can escape process supervision are not permitted") {
		t.Fatalf("bounded background result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "child-finished")); !os.IsNotExist(err) {
		t.Fatalf("rejected background command changed workspace: %v", err)
	}
}

func TestShellWallClockLimitCannotBeExtendedByOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	shellRuntime := newShellRuntime(context.Background(), ShellOptions{MaxWallClockDuration: 350 * time.Millisecond})
	driver := newRuntimeShellDriver(t.TempDir(), "allow", "deny", shellRuntime)
	arguments, _ := json.Marshal(shellInput{
		Command:        `i=0; while [ "$i" -lt 40 ]; do i=$((i+1)); printf 'tick\n'; sleep 0.05; done`,
		TimeoutSeconds: 1,
	})

	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "wall-clock", Name: ToolShell, Arguments: arguments}, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "command stopped: wall_clock_timeout") {
		t.Fatalf("wall-clock shell result=%+v", result)
	}
	if elapsed < 250*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("wall-clock shell returned after %s", elapsed)
	}
}

func TestShellTerminatesAfterOutputInactivityTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, _ := json.Marshal(shellInput{Command: "sleep 10", TimeoutSeconds: 1})
	started := time.Now()
	result, err := driver.Execute(context.Background(), tool.Call{ID: "inactive", Name: ToolShell, Arguments: arguments}, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "command stopped: timeout") {
		t.Fatalf("inactive shell result=%+v", result)
	}
	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("inactive shell returned after %s, want prompt termination near the one-second timeout", elapsed)
	}
	var output shellOutput
	if err := json.Unmarshal(result.Structured, &output); err != nil {
		t.Fatal(err)
	}
	if output.Status != "stopped" || output.Reason != "timeout" {
		t.Fatalf("structured inactive shell output=%+v", output)
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
		result, err := driver.Execute(context.Background(), tool.Call{ID: "sink-start", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error {
			if update.Kind == tool.UpdateProgress && update.Data["phase"] == "started" {
				return fmt.Errorf("started broke")
			}
			return nil
		})
		if err != nil || !result.IsError || !strings.Contains(result.Content, "started broke") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("running", func(t *testing.T) {
		result, err := driver.Execute(context.Background(), tool.Call{ID: "sink-running", Name: ToolShell, Arguments: arguments}, func(update tool.Update) error {
			if update.Kind == tool.UpdateProgress && update.Data["phase"] == "progress" {
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
			if update.Kind == tool.UpdateProgress && update.Data["phase"] == "finished" {
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
	for _, command := range []string{"nohup sleep 10", "setsid sleep 10", "set''sid sleep 10", `set\\sid sleep 10`, `s\"et\"sid sleep 10`, "disown", "daemonize app"} {
		if !rejectDetached(command) {
			t.Errorf("did not reject %q", command)
		}
	}
	for _, command := range []string{
		"printf one && printf two",
		`printf '&'`,
		`printf \&`,
		`printf shell-ok 2>&1 | tail -20`,
		"sleep 10 & wait",
	} {
		if rejectDetached(command) {
			t.Errorf("rejected supervised command %q", command)
		}
	}
	for _, command := range []string{
		"printf one && printf two",
		`printf '&'`,
		`printf \&`,
		`printf shell-ok 2>&1 | tail -20`,
		`printf shell-ok 1<&0`,
		`printf shell-ok &>output`,
		`printf shell-ok |& cat`,
	} {
		if hasBackgroundOperatorForOS(command, "darwin") {
			t.Errorf("classified foreground syntax as background %q", command)
		}
	}
	for _, command := range []string{"sleep 10 &", "sleep 10 & wait"} {
		if !hasBackgroundOperatorForOS(command, "darwin") {
			t.Errorf("missed background operator in %q", command)
		}
	}
	if rejectDetachedForOS(`& "C:\Program Files\Git\bin\git.exe" status`, "windows") {
		t.Fatal("rejected the PowerShell foreground invocation operator")
	}
}

func TestShellRuntimeConcurrencyCanGrowWithoutInterruptingActiveCalls(t *testing.T) {
	runtime := newShellRuntime(context.Background(), ShellOptions{MaxConcurrency: 1})
	if err := runtime.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() { acquired <- runtime.acquire(context.Background()) }()
	select {
	case err := <-acquired:
		t.Fatalf("second slot acquired before resize: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	runtime.updateMaxConcurrency(2)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second slot did not acquire after resize")
	}
	runtime.release()
	runtime.release()
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
	for _, forbidden := range []string{ToolEditHashline, ToolWriteFile, ToolGofmt, ToolShell} {
		if definitions[forbidden] {
			t.Fatalf("workspace policy exposed %q", forbidden)
		}
	}
	if !definitions[ToolReadFile] {
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

func TestShellRequiresApprovalAndExecutesAfterDecision(t *testing.T) {
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
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShellUpdatesUseV016ProgressContract(t *testing.T) {
	driver := newShellDriver(t.TempDir(), "allow", "deny")
	arguments, err := json.Marshal(shellInput{Command: "printf done"})
	if err != nil {
		t.Fatal(err)
	}
	var updates []tool.Update
	results, err := tool.NewBus(driver).ExecuteBatch(context.Background(), []tool.Call{{
		ID: "update-contract", Name: ToolShell, Arguments: arguments,
	}}, tool.ModeSequential, tool.ExecuteOptions{Sink: func(update tool.Update) error {
		updates = append(updates, update)
		return nil
	}})
	if err != nil || len(results) != 1 || results[0].IsError {
		t.Fatalf("shell execution = %#v, %v", results, err)
	}
	if len(updates) < 2 || updates[0].Data["phase"] != "started" || updates[len(updates)-1].Data["phase"] != "finished" {
		t.Fatalf("shell update phases = %#v", updates)
	}
	for _, update := range updates {
		if update.Kind != tool.UpdateProgress || len(update.Parts) != 0 {
			t.Fatalf("invalid v0.16 update = %#v", update)
		}
	}
}
