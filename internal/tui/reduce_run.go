package tui

import (
	"strings"

	"github.com/Viking602/azem/internal/app"
)

func (m *AppModel) reduceRunEvent(event app.Event) bool {
	if event.Kind != app.EventRunState || event.RunProjection == nil {
		return false
	}
	run := *event.RunProjection
	key := runProjectionKey(run)
	if m.runs == nil {
		m.runs = make(map[string]app.RunProjection)
	}
	m.runs[key] = run
	if run.SessionID != m.sessionID {
		return true
	}
	m.applyCurrentRunProjection(run)
	return true
}

func (m *AppModel) applyCurrentRunProjection(run app.RunProjection) {
	if terminalProjectedRun(run) {
		return
	}
	m.runID = run.RunID
	m.runStartedAt = run.StartedAt
	m.runActivityAt = run.LastActivityAt
	m.runActivity = string(run.Activity)
	m.runActivityDetail = first(run.Progress, activeOperationSummary(run.ActiveOperations))
	m.status = runStatusLabel(run)
	if run.Provider != "" {
		m.switchProvider(run.Provider)
	}
	if run.Model != "" {
		m.selectModel(run.Model)
	}
	if run.Reasoning != "" {
		m.reasoning = run.Reasoning
	}
}

func (m AppModel) currentRunProjection() (app.RunProjection, bool) {
	var selected app.RunProjection
	found := false
	for _, run := range m.runs {
		if run.SessionID != m.sessionID || terminalProjectedRun(run) {
			continue
		}
		if !found || run.LastActivityAt.After(selected.LastActivityAt) {
			selected, found = run, true
		}
	}
	return selected, found
}

func (m AppModel) globalRunProjection() (app.RunProjection, bool) {
	var selected app.RunProjection
	found := false
	for _, run := range m.runs {
		if terminalProjectedRun(run) {
			continue
		}
		if !found || run.LastActivityAt.After(selected.LastActivityAt) {
			selected, found = run, true
		}
	}
	return selected, found
}

func runProjectionKey(run app.RunProjection) string {
	if run.RunID != "" {
		return run.RunID
	}
	return run.SessionID + ":preparing"
}

func terminalProjectedRun(run app.RunProjection) bool {
	switch run.State {
	case "completed", "failed", "cancelled", "reconcile_required", "blocked":
		return true
	}
	switch run.BindingState {
	case "completed", "failed", "cancelled", "reconcile_required":
		return true
	default:
		return run.Activity == app.RunActivityIdle
	}
}

func runStatusLabel(run app.RunProjection) string {
	switch run.Activity {
	case app.RunActivityPreparing:
		return "Starting"
	case app.RunActivityAwaitingApproval:
		return "Awaiting approval"
	case app.RunActivityReviewing:
		return "Reviewing approval"
	case app.RunActivityCompacting:
		return "Compacting"
	case app.RunActivityStopping:
		return "Cancelling"
	case app.RunActivityRecovering:
		return "Recovering"
	default:
		return "Running"
	}
}

func activeOperationSummary(operations []app.ActiveOperation) string {
	if len(operations) == 0 {
		return ""
	}
	operation := operations[0]
	parts := []string{operation.Name, operation.Target, operation.Summary}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func runAllowsAction(run app.RunProjection, action string) bool {
	for _, allowed := range run.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}
