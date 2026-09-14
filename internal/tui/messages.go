package tui

import (
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/session"
)

type (
	appEventMsg        struct{ Event app.Event }
	appStreamClosedMsg struct{ Err error }
	animationTickMsg   struct{}
	backgroundPollMsg  struct {
		Generation uint64
		ProcessID  string
	}
)

type backgroundPollResultMsg struct {
	Generation uint64
	ProcessID  string
	Err        error
}
type startTurnResultMsg struct {
	RunID       string
	Text        string
	Attachments []session.Attachment
	Err         error
}
type cancelResultMsg struct {
	Cancelled bool
	Err       error
}
type actionResultMsg struct {
	Action Action
	Err    error
}
type (
	shutdownResultMsg  struct{ Err error }
	sessionSelectedMsg struct {
		Snapshot desktop.ReconnectSnapshot
		Err      error
	}
)

type (
	BootstrapDoneMsg     = appEventMsg
	SessionLoadedMsg     = appEventMsg
	RunStartedMsg        = appEventMsg
	AgentStateMsg        = appEventMsg
	ThinkingDeltaMsg     = appEventMsg
	TextDeltaMsg         = appEventMsg
	ToolStartedMsg       = appEventMsg
	ToolUpdateMsg        = appEventMsg
	ToolFinishedMsg      = appEventMsg
	DiffReadyMsg         = appEventMsg
	ApprovalRequestedMsg = appEventMsg
	ApprovalResolvedMsg  = appEventMsg
	ModelCatalogMsg      = appEventMsg
	AuthStateMsg         = appEventMsg
	MCPStateMsg          = appEventMsg
	RunFinishedMsg       = appEventMsg
	RunFailedMsg         = appEventMsg
	RunCancelledMsg      = appEventMsg
)
