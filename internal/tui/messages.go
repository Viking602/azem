package tui

import "github.com/Viking602/azem/internal/app"

type appEventMsg struct{ Event app.Event }
type appStreamClosedMsg struct{ Err error }
type animationTickMsg struct{}
type startTurnResultMsg struct {
	RunID string
	Err   error
}
type cancelResultMsg struct{ Cancelled bool }
type actionResultMsg struct {
	Action Action
	Err    error
}
type shutdownResultMsg struct{ Err error }

type BootstrapDoneMsg = appEventMsg
type SessionLoadedMsg = appEventMsg
type RunStartedMsg = appEventMsg
type AgentStateMsg = appEventMsg
type ThinkingDeltaMsg = appEventMsg
type TextDeltaMsg = appEventMsg
type ToolStartedMsg = appEventMsg
type ToolUpdateMsg = appEventMsg
type ToolFinishedMsg = appEventMsg
type DiffReadyMsg = appEventMsg
type ApprovalRequestedMsg = appEventMsg
type ApprovalResolvedMsg = appEventMsg
type ModelCatalogMsg = appEventMsg
type AuthStateMsg = appEventMsg
type MCPStateMsg = appEventMsg
type RunFinishedMsg = appEventMsg
type RunFailedMsg = appEventMsg
type RunCancelledMsg = appEventMsg
