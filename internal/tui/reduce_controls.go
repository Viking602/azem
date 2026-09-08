package tui

import (
	"github.com/Viking602/azem/internal/app"
)

func (m *AppModel) applyPendingControlProjection(controls []app.PendingControlProjection, recovery app.RecoveryProjection) {
	m.pendingControls = append([]app.PendingControlProjection(nil), controls...)
	m.approval = nil
	m.pendingApprovals = nil
	m.planningInput = nil
	m.planReview = nil
	m.recovery = make([]RecoveryView, 0, len(recovery.Items))
	for _, item := range recovery.Items {
		m.recovery = append(m.recovery, RecoveryView{
			Kind: item.Kind, ID: item.ID, RunID: item.RunID, Title: item.Title,
			Detail: item.Text, State: item.State, TokenID: item.Data["tokenId"], ToolName: item.Data["toolName"],
		})
	}
	for _, control := range controls {
		if control.SessionID != "" && control.SessionID != m.sessionID {
			continue
		}
		switch control.Kind {
		case "approval":
			approval := ApprovalView{
				ApprovalID: control.ID, AgentID: control.AgentID, ToolCallID: control.ToolCallID,
				Tool: control.Data["tool"], Target: control.Data["target"], Risk: control.Data["risk"],
				Effect: control.Data["effect"], Action: first(control.Data["action"], control.Text), Diff: control.Data["diff"],
			}
			if m.approval == nil {
				m.approval = &approval
			} else {
				m.pendingApprovals = append(m.pendingApprovals, approval)
			}
		case "input":
			input, err := planningInputFromEvent(control.ID, control.Data["questions"])
			if err == nil {
				m.planningInput = input
			}
		case "plan":
			m.planReview = &PlanReviewView{
				ID: control.ID, Title: control.Title, Body: control.Text,
				Version: first(control.Data["version"], "1"), State: control.State,
			}
			m.planMode = true
		}
	}
	if len(m.recovery) > 0 {
		m.status = "Recovery attention"
		m.openOverlay(OverlayRecovery)
	} else if m.approval != nil {
		m.status = "Awaiting approval"
		m.openOverlay(OverlayApproval)
	}
}
