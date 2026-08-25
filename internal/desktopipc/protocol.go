package desktopipc

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProtocolVersion         = 1
	MaxControlFrameBytes    = 16 << 20
	MaxBinaryChunkBytes     = 256 << 10
	MaxReassembledBinary    = 256 << 20
	DefaultReplayBytes      = 64 << 20
	DefaultClientQueueBytes = 8 << 20
)

type FrameKind string

const (
	FrameHello          FrameKind = "hello"
	FrameHelloAck       FrameKind = "hello_ack"
	FrameRequest        FrameKind = "request"
	FrameResponse       FrameKind = "response"
	FrameEventBatch     FrameKind = "event_batch"
	FrameReplayRequest  FrameKind = "replay_request"
	FrameReplayComplete FrameKind = "replay_complete"
	FrameResyncRequired FrameKind = "resync_required"
	FrameBinaryBegin    FrameKind = "binary_begin"
	FrameBinaryChunk    FrameKind = "binary_chunk"
	FrameBinaryEnd      FrameKind = "binary_end"
	FramePing           FrameKind = "ping"
	FramePong           FrameKind = "pong"
	FrameClientDetach   FrameKind = "client_detach"
	FrameDaemonStop     FrameKind = "daemon_stop"
)

type Method string

const (
	MethodInitialise              Method = "initialise"
	MethodReconnectSnapshot       Method = "reconnect_snapshot"
	MethodStartTurn               Method = "start_turn"
	MethodGuide                   Method = "guide"
	MethodFollowUp                Method = "follow_up"
	MethodCancelActive            Method = "cancel_active"
	MethodExecute                 Method = "execute"
	MethodImportAttachment        Method = "import_attachment"
	MethodImportClipboardImage    Method = "import_clipboard_image"
	MethodAttachmentDataURL       Method = "attachment_data_url"
	MethodSearchSessions          Method = "search_sessions"
	MethodResumeSession           Method = "resume_session"
	MethodSessionTree             Method = "session_tree"
	MethodNavigateSessionTree     Method = "navigate_session_tree"
	MethodCreateSessionFork       Method = "create_session_fork"
	MethodSetSessionEntryLabel    Method = "set_session_entry_label"
	MethodExportSession           Method = "export_session"
	MethodShareSession            Method = "share_session"
	MethodForkSession             Method = "fork_session"
	MethodPullRequestDashboard    Method = "pull_request_dashboard"
	MethodPullRequestDetail       Method = "pull_request_detail"
	MethodMutatePullRequest       Method = "mutate_pull_request"
	MethodSetPullRequestMonitor   Method = "set_pull_request_monitor"
	MethodSkillCatalog            Method = "skill_catalog"
	MethodHookCatalog             Method = "hook_catalog"
	MethodMarketplaceCatalog      Method = "marketplace_catalog"
	MethodUsageReport             Method = "usage_report"
	MethodWorkspaceChanges        Method = "workspace_changes"
	MethodWorkspaceChange         Method = "workspace_change"
	MethodWorkspaceEntries        Method = "workspace_entries"
	MethodWorkspaceFile           Method = "workspace_file"
	MethodCreateProject           Method = "create_project"
	MethodOpenProject             Method = "open_project"
	MethodOpenProjectSession      Method = "open_project_session"
	MethodSystemFonts             Method = "system_fonts"
	MethodListTerminals           Method = "list_terminals"
	MethodCreateTerminal          Method = "create_terminal"
	MethodWriteTerminal           Method = "write_terminal"
	MethodResizeTerminal          Method = "resize_terminal"
	MethodCloseTerminal           Method = "close_terminal"
	MethodOpenTerminal            Method = "open_terminal"
	MethodTerminalReplay          Method = "terminal_replay"
	MethodBeginAttachmentTransfer Method = "begin_attachment_transfer"
	MethodCommitAttachment        Method = "commit_attachment"
	MethodAbortAttachment         Method = "abort_attachment"
)

func AllMethods() []Method {
	return []Method{
		MethodInitialise, MethodReconnectSnapshot, MethodStartTurn, MethodGuide, MethodFollowUp,
		MethodCancelActive, MethodExecute, MethodImportAttachment, MethodImportClipboardImage,
		MethodAttachmentDataURL, MethodSearchSessions, MethodResumeSession, MethodSessionTree,
		MethodNavigateSessionTree, MethodCreateSessionFork, MethodSetSessionEntryLabel,
		MethodExportSession, MethodShareSession, MethodForkSession, MethodPullRequestDashboard,
		MethodPullRequestDetail, MethodMutatePullRequest, MethodSetPullRequestMonitor,
		MethodSkillCatalog, MethodHookCatalog, MethodMarketplaceCatalog, MethodUsageReport,
		MethodWorkspaceChanges, MethodWorkspaceChange, MethodWorkspaceEntries, MethodWorkspaceFile,
		MethodCreateProject, MethodOpenProject, MethodOpenProjectSession, MethodSystemFonts,
		MethodListTerminals, MethodCreateTerminal, MethodWriteTerminal, MethodResizeTerminal,
		MethodCloseTerminal, MethodOpenTerminal, MethodTerminalReplay, MethodBeginAttachmentTransfer,
		MethodCommitAttachment, MethodAbortAttachment,
	}
}

type Channel string

const (
	ChannelRuntime     Channel = "runtime"
	ChannelPullRequest Channel = "pull_request"
	ChannelTerminal    Channel = "terminal"
	ChannelDaemon      Channel = "daemon"
)

type Envelope struct {
	Version     int             `json:"version"`
	Kind        FrameKind       `json:"kind"`
	ID          string          `json:"id,omitempty"`
	ClientID    string          `json:"clientId,omitempty"`
	WorkspaceID string          `json:"workspaceId,omitempty"`
	Method      Method          `json:"method,omitempty"`
	Channel     Channel         `json:"channel,omitempty"`
	Sequence    uint64          `json:"sequence,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Error       *ProtocolError  `json:"error,omitempty"`
	Binary      *BinaryMetadata `json:"binary,omitempty"`
}

type ProtocolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type BinaryMetadata struct {
	TransferID string  `json:"transferId"`
	Purpose    string  `json:"purpose,omitempty"`
	Channel    Channel `json:"channel,omitempty"`
	Sequence   uint64  `json:"sequence,omitempty"`
	Name       string  `json:"name,omitempty"`
	MediaType  string  `json:"mediaType,omitempty"`
	ByteLength int64   `json:"byteLength"`
	SHA256     string  `json:"sha256"`
	Index      int     `json:"index,omitempty"`
	Count      int     `json:"count,omitempty"`
}

type Challenge struct {
	Nonce       string `json:"nonce"`
	WorkspaceID string `json:"workspaceId"`
	Protocol    int    `json:"protocol"`
}

type Authenticate struct {
	ClientID     string `json:"clientId"`
	Proof        string `json:"proof"`
	Protocol     int    `json:"protocol"`
	LastSequence uint64 `json:"lastSequence,omitempty"`
}

type HelloAck struct {
	Protocol        int    `json:"protocol"`
	WorkspaceID     string `json:"workspaceId"`
	CurrentSequence uint64 `json:"currentSequence"`
	ReplayAvailable bool   `json:"replayAvailable"`
}

type DaemonStop struct {
	IncludeActive bool `json:"includeActive,omitempty"`
}

type EventBatch struct {
	FirstSequence uint64            `json:"firstSequence"`
	LastSequence  uint64            `json:"lastSequence"`
	Events        []json.RawMessage `json:"events"`
}

func NewEnvelope(kind FrameKind) Envelope {
	return Envelope{Version: ProtocolVersion, Kind: kind}
}

func (envelope Envelope) Validate() error {
	if envelope.Version != ProtocolVersion {
		return fmt.Errorf("unsupported IPC protocol version %d", envelope.Version)
	}
	switch envelope.Kind {
	case FrameHello, FrameHelloAck, FrameRequest, FrameResponse, FrameEventBatch,
		FrameReplayRequest, FrameReplayComplete, FrameResyncRequired, FrameBinaryBegin,
		FrameBinaryChunk, FrameBinaryEnd, FramePing, FramePong, FrameClientDetach, FrameDaemonStop:
	default:
		return fmt.Errorf("unsupported IPC frame kind %q", envelope.Kind)
	}
	if envelope.Kind == FrameRequest && (strings.TrimSpace(envelope.ID) == "" || strings.TrimSpace(string(envelope.Method)) == "") {
		return fmt.Errorf("IPC request requires id and method")
	}
	if envelope.Kind == FrameResponse && strings.TrimSpace(envelope.ID) == "" {
		return fmt.Errorf("IPC response requires id")
	}
	if envelope.Kind == FrameDaemonStop && strings.TrimSpace(envelope.ID) == "" {
		return fmt.Errorf("IPC daemon stop requires id")
	}
	if (envelope.Kind == FrameBinaryBegin || envelope.Kind == FrameBinaryChunk || envelope.Kind == FrameBinaryEnd) && (envelope.Binary == nil || strings.TrimSpace(envelope.Binary.TransferID) == "") {
		return fmt.Errorf("IPC binary frame requires transfer metadata")
	}
	return nil
}
