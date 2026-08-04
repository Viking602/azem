package githubpr

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type monitorRunner struct {
	headOID    string
	branch     string
	dirty      bool
	conclusion string
}

func (run *monitorRunner) Run(_ context.Context, _ string, _ string, name string, args ...string) ([]byte, error) {
	command := name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(command, "gh repo view "):
		return []byte(`{"nameWithOwner":"octo/demo","mergeCommitAllowed":true,"squashMergeAllowed":true}`), nil
	case command == "gh api user --jq .login":
		return []byte("octocat\n"), nil
	case strings.HasPrefix(command, "gh pr view "):
		payload := map[string]any{
			"number": 42, "title": "Repair me", "state": "OPEN",
			"headRefName": "feature/repair", "headRefOid": run.headOID, "baseRefName": "main",
			"mergeable": "CONFLICTING", "mergeStateStatus": "DIRTY",
			"statusCheckRollup": []map[string]any{{"name": "unit", "workflowName": "CI", "status": "COMPLETED", "conclusion": run.conclusion}},
		}
		return json.Marshal(payload)
	case command == "git branch --show-current":
		return []byte(run.branch + "\n"), nil
	case command == "git status --porcelain=v1 -z --untracked-files=all":
		if run.dirty {
			return []byte(" M local.go\x00"), nil
		}
		return nil, nil
	default:
		return nil, &commandError{Name: name, Args: append([]string(nil), args...), Stderr: "unexpected test command"}
	}
}

func TestMonitorDeduplicatesRepairsAndRestoresLifecycle(t *testing.T) {
	runner := &monitorRunner{headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE"}
	client := NewClientWithRunner(t.TempDir(), runner)
	statePath := t.TempDir() + "/pr-monitors.json"
	repairs := 0
	repair := func(_ context.Context, pullRequest PullRequest, issue RepairIssue) (string, error) {
		repairs++
		if pullRequest.Number != 42 || !issue.Conflict || len(issue.FailingChecks) != 1 || issue.FailingChecks[0] != "unit" {
			t.Fatalf("repair input pullRequest=%+v issue=%+v", pullRequest.PullRequestSummary, issue)
		}
		return "session-" + strconv.Itoa(repairs), nil
	}
	monitor := NewMonitor(context.Background(), client, statePath, repair, nil)
	defer monitor.Close()
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := monitor.check(42); err != nil {
		t.Fatalf("first check error = %v", err)
	}
	if err := monitor.check(42); err != nil {
		t.Fatalf("duplicate check error = %v", err)
	}
	if repairs != 1 {
		t.Fatalf("repair count = %d, want 1", repairs)
	}
	if state := monitor.State(42); state.Status != MonitorRepairing || state.SessionID != "session-1" || state.Fingerprint == "" {
		t.Fatalf("repairing state = %+v", state)
	}
	monitor.ObserveSession("session-1", "run_finished")
	if state := monitor.State(42); state.Status != MonitorCompleted {
		t.Fatalf("completed state = %+v", state)
	}

	restored := NewMonitor(context.Background(), client, statePath, repair, nil)
	defer restored.Close()
	if state := restored.State(42); state.Status != MonitorCompleted || state.SessionID != "session-1" || len(state.FailingChecks) != 1 {
		t.Fatalf("restored state = %+v", state)
	}
	if err := restored.check(42); err != nil {
		t.Fatalf("restored duplicate check error = %v", err)
	}
	if repairs != 1 {
		t.Fatalf("restored monitor duplicated repair: %d", repairs)
	}

	runner.headOID = "bbbbbbbbbbbbbbbb"
	if err := restored.check(42); err != nil {
		t.Fatalf("new failure check error = %v", err)
	}
	if repairs != 2 {
		t.Fatalf("new head repair count = %d, want 2", repairs)
	}
	if state := restored.State(42); state.Status != MonitorRepairing || state.SessionID != "session-2" {
		t.Fatalf("new repair state = %+v", state)
	}
}

func TestMonitorWaitsForMatchingCleanWorkspace(t *testing.T) {
	runner := &monitorRunner{headOID: "aaaaaaaaaaaaaaaa", branch: "main", conclusion: "FAILURE"}
	client := NewClientWithRunner(t.TempDir(), runner)
	repairs := 0
	monitor := NewMonitor(context.Background(), client, "", func(context.Context, PullRequest, RepairIssue) (string, error) {
		repairs++
		return "repair-session", nil
	}, nil)
	defer monitor.Close()
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := monitor.check(42); err != nil {
		t.Fatalf("branch guard check error = %v", err)
	}
	if state := monitor.State(42); state.Status != MonitorPending || !strings.Contains(state.Message, "switch to feature/repair") {
		t.Fatalf("branch guard state = %+v", state)
	}
	runner.branch = "feature/repair"
	runner.dirty = true
	if err := monitor.check(42); err != nil {
		t.Fatalf("dirty guard check error = %v", err)
	}
	if state := monitor.State(42); state.Status != MonitorPending || !strings.Contains(state.Message, "workspace changes") {
		t.Fatalf("dirty guard state = %+v", state)
	}
	if repairs != 0 {
		t.Fatalf("unsafe workspace started %d repairs", repairs)
	}

	runner.dirty = false
	if err := monitor.check(42); err != nil {
		t.Fatalf("clean workspace check error = %v", err)
	}
	if repairs != 1 || monitor.State(42).Status != MonitorRepairing {
		t.Fatalf("clean workspace repairs=%d state=%+v", repairs, monitor.State(42))
	}
}
