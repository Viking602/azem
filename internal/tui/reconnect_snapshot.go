package tui

import (
	"fmt"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopclient"
)

func (m *AppModel) ApplyReconnectSnapshot(snapshot desktop.ReconnectSnapshot) {
	if m == nil {
		return
	}
	m.workspace = snapshot.Base.Workspace
	m.provider = first(snapshot.Base.Provider, m.provider)
	m.model = first(snapshot.Base.Model, m.model)
	m.reasoning = first(snapshot.Base.Reasoning, m.reasoning)
	m.agentMode = first(snapshot.Base.AgentMode, m.agentMode)
	m.approvalMode = ApprovalMode(first(snapshot.Base.ApprovalMode, string(m.approvalMode)))
	m.autoReviewAvailable = snapshot.Base.AutoReviewAvailable
	m.deliveryMode = first(snapshot.Base.QueueMode, m.deliveryMode, "queue")
	m.connection = desktopclient.ConnectionProjection{
		State: desktopclient.ConnectionConnected, DaemonEpoch: snapshot.DaemonEpoch, WireSequence: snapshot.WireSequence,
	}
	m.lastWireCursor = snapshot.WireSequence
	if snapshot.SelectedSessionID != "" {
		m.sessionID = snapshot.SelectedSessionID
	}
	m.sessions = make([]SessionChoice, 0, len(snapshot.Sessions))
	for _, item := range snapshot.Sessions {
		m.sessions = append(m.sessions, SessionChoice{
			ID: item.ID, Title: item.Title, ProviderID: item.ProviderID, ModelID: item.ModelID,
			UpdatedAt: item.UpdatedAt.Local().Format("2006-01-02 15:04"),
		})
	}
	projection := app.RuntimeProjectionSnapshot{
		Session: snapshot.Session, Runs: snapshot.Runs, LiveBlocks: snapshot.LiveBlocks,
		PendingControls: snapshot.Controls, PromptQueues: snapshot.PromptQueues, Recovery: snapshot.RuntimeRecovery,
	}
	m.applyEvent(app.Event{
		Kind: app.EventProjectionResync, SessionID: snapshot.SelectedSessionID, State: "snapshot",
		RuntimeProjection: &projection, Data: map[string]string{"wireSequence": fmt.Sprint(snapshot.WireSequence)},
	})
	if snapshot.ContextProfile != nil {
		m.contextProfile = *snapshot.ContextProfile
		m.contextProfileError = ""
	}
	m.invalidateTranscriptLayout()
	m.transcriptTop = 0
}
