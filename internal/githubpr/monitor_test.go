package githubpr

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type monitorRunner struct {
	headOID            string
	branch             string
	localHeadOID       string
	upstreamRepository string
	repository         string
	dirty              bool
	conclusion         string
	detailStarted      chan<- struct{}
	detailRelease      <-chan struct{}
}

func (run *monitorRunner) Run(ctx context.Context, _ string, _ string, name string, args ...string) ([]byte, error) {
	command := name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(command, "gh repo view "):
		repository := run.repository
		if repository == "" {
			repository = "octo/demo"
		}
		return json.Marshal(map[string]any{"nameWithOwner": repository, "mergeCommitAllowed": true, "squashMergeAllowed": true})
	case command == "gh api user --jq .login":
		return []byte("octocat\n"), nil
	case strings.HasPrefix(command, "gh pr view "):
		if run.detailStarted != nil {
			run.detailStarted <- struct{}{}
		}
		if run.detailRelease != nil {
			select {
			case <-run.detailRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		payload := map[string]any{
			"number": 42, "title": "Repair me", "state": "OPEN",
			"headRefName": "feature/repair", "headRefOid": run.headOID,
			"headRepository": map[string]any{"name": "demo"}, "headRepositoryOwner": map[string]any{"login": "octo"},
			"baseRefName": "main",
			"mergeable":   "CONFLICTING", "mergeStateStatus": "DIRTY",
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
	case command == "git rev-parse HEAD":
		if run.localHeadOID != "" {
			return []byte(run.localHeadOID + "\n"), nil
		}
		return []byte(run.headOID + "\n"), nil
	case strings.HasPrefix(command, "git config --get branch.") && strings.HasSuffix(command, ".remote"):
		return []byte("origin\n"), nil
	case command == "git remote get-url origin":
		repository := run.upstreamRepository
		if repository == "" {
			repository = "octo/demo"
		}
		return []byte("https://github.com/" + repository + ".git\n"), nil
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

func TestMonitorRetainsTerminalEventBeforeRepairStarterReturns(t *testing.T) {
	runner := &monitorRunner{headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE"}
	var monitor *Monitor
	repair := func(context.Context, PullRequest, RepairIssue) (string, error) {
		monitor.ObserveSession("session-fast", "run_finished")
		return "session-fast", nil
	}
	monitor = NewMonitor(context.Background(), NewClientWithRunner(t.TempDir(), runner), "", repair, nil)
	defer monitor.Close()
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := monitor.check(42); err != nil {
		t.Fatalf("check error = %v", err)
	}
	if state := monitor.State(42); state.Status != MonitorCompleted || state.SessionID != "session-fast" {
		t.Fatalf("fast terminal state = %+v", state)
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
	runner.localHeadOID = "bbbbbbbbbbbbbbbb"
	if err := monitor.check(42); err != nil {
		t.Fatalf("head guard check error = %v", err)
	}
	if state := monitor.State(42); state.Status != MonitorPending || !strings.Contains(state.Message, "head commit") {
		t.Fatalf("head guard state = %+v", state)
	}
	runner.localHeadOID = ""
	runner.upstreamRepository = "other/demo"
	if err := monitor.check(42); err != nil {
		t.Fatalf("upstream guard check error = %v", err)
	}
	if state := monitor.State(42); state.Status != MonitorPending || !strings.Contains(state.Message, "head repository") {
		t.Fatalf("upstream guard state = %+v", state)
	}
	runner.upstreamRepository = ""
	if err := monitor.check(42); err != nil {
		t.Fatalf("clean workspace check error = %v", err)
	}
	if repairs != 1 || monitor.State(42).Status != MonitorRepairing {
		t.Fatalf("clean workspace repairs=%d state=%+v", repairs, monitor.State(42))
	}
}

func TestMonitorDisableWinsAgainstInFlightCheck(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	runner := &monitorRunner{
		headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE",
		detailStarted: started, detailRelease: release,
	}
	repairs := 0
	monitor := NewMonitor(context.Background(), NewClientWithRunner(t.TempDir(), runner), "", func(context.Context, PullRequest, RepairIssue) (string, error) {
		repairs++
		return "repair-session", nil
	}, nil)
	defer monitor.Close()
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set(true) error = %v", err)
	}

	checked := make(chan error, 1)
	go func() { checked <- monitor.check(42) }()
	<-started
	if _, err := monitor.Set(42, false); err != nil {
		t.Fatalf("Set(false) error = %v", err)
	}
	close(release)
	if err := <-checked; err != nil {
		t.Fatalf("in-flight check error = %v", err)
	}
	if state := monitor.State(42); state.Enabled || state.Status != MonitorDisabled {
		t.Fatalf("disabled state was overwritten: %+v", state)
	}
	if repairs != 0 {
		t.Fatalf("disabled monitor started %d repairs", repairs)
	}
}

func TestMonitorRetriesInterruptedRepairAfterRestart(t *testing.T) {
	runner := &monitorRunner{headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE"}
	client := NewClientWithRunner(t.TempDir(), runner)
	statePath := t.TempDir() + "/pr-monitors.json"
	repairs := 0
	repair := func(context.Context, PullRequest, RepairIssue) (string, error) {
		repairs++
		return "session-" + strconv.Itoa(repairs), nil
	}
	monitor := NewMonitor(context.Background(), client, statePath, repair, nil)
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := monitor.check(42); err != nil {
		t.Fatalf("initial check error = %v", err)
	}
	monitor.Close()

	restored := NewMonitor(context.Background(), client, statePath, repair, nil)
	defer restored.Close()
	if state := restored.State(42); state.Status != MonitorPending || state.SessionID != "" {
		t.Fatalf("interrupted repair state = %+v", state)
	}
	if err := restored.check(42); err != nil {
		t.Fatalf("retry check error = %v", err)
	}
	if state := restored.State(42); repairs != 2 || state.Status != MonitorRepairing || state.SessionID != "session-2" {
		t.Fatalf("retried repair count=%d state=%+v", repairs, state)
	}
}

func TestMonitorDisablesPersistedConsentWhenRepositoryChanges(t *testing.T) {
	statePath := t.TempDir() + "/pr-monitors.json"
	originalRunner := &monitorRunner{
		headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE",
		repository: "octo/demo",
	}
	monitor := NewMonitor(
		context.Background(),
		NewClientWithRunner(t.TempDir(), originalRunner),
		statePath,
		func(context.Context, PullRequest, RepairIssue) (string, error) { return "unexpected", nil },
		nil,
	)
	if _, err := monitor.Set(42, true); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	monitor.Close()

	repairs := 0
	changedRunner := &monitorRunner{
		headOID: "aaaaaaaaaaaaaaaa", branch: "feature/repair", conclusion: "FAILURE",
		repository: "other/repository",
	}
	restored := NewMonitor(
		context.Background(),
		NewClientWithRunner(t.TempDir(), changedRunner),
		statePath,
		func(context.Context, PullRequest, RepairIssue) (string, error) {
			repairs++
			return "repair-session", nil
		},
		nil,
	)
	defer restored.Close()
	if state := restored.State(42); !state.Enabled {
		t.Fatalf("persisted monitor was not restored before repository validation: %+v", state)
	}
	if err := restored.check(42); err != nil {
		t.Fatalf("repository validation check error = %v", err)
	}
	state := restored.State(42)
	if state.Enabled || state.Status != MonitorDisabled || !strings.Contains(state.Message, "repository changed") {
		t.Fatalf("repository mismatch state = %+v", state)
	}
	if repairs != 0 {
		t.Fatalf("repository mismatch started %d repairs", repairs)
	}
}
