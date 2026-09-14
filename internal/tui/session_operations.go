package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/collab"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/usageview"
)

type sessionOperationResultMsg struct {
	Title   string
	Content string
	Refresh bool
	Err     error
}
type collabOperationResultMsg struct {
	Host    *collab.Host
	Guest   *collab.Guest
	Replica *collab.Replica
	Content string
	Err     error
}

type collabGuestEventMsg struct {
	Guest *collab.Guest
	Event collab.GuestEvent
	OK    bool
}

func runSessionOperation(runtime Runtime, sessionID string, command Command, _ string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		switch command.Name {
		case "tree":
			var tree session.SessionTree
			if err := runtime.Request(ctx, desktopipc.MethodSessionTree, map[string]string{"sessionId": sessionID}, &tree); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			encoded, _ := json.MarshalIndent(tree, "", "  ")
			return sessionOperationResultMsg{Title: "Session tree", Content: string(encoded)}
		case "branch":
			if len(command.Args) != 1 {
				return sessionOperationResultMsg{Err: errors.New("usage: /branch <entry-id>")}
			}
			var event desktop.Event
			if err := runtime.Request(ctx, desktopipc.MethodNavigateSessionTree, map[string]string{
				"sessionId": sessionID, "entryId": command.Args[0],
			}, &event); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session branch", Content: "Moved to " + command.Args[0], Refresh: true}
		case "fork":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /fork <target-session-id> [entry-id]")}
			}
			entryID := ""
			if len(command.Args) == 2 {
				entryID = command.Args[1]
			}
			var tree session.SessionTree
			if err := runtime.Request(ctx, desktopipc.MethodCreateSessionFork, map[string]string{
				"sessionId": sessionID, "targetId": command.Args[0], "entryId": entryID,
			}, &tree); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session fork", Content: "Created " + command.Args[0]}
		case "label":
			if len(command.Args) < 1 {
				return sessionOperationResultMsg{Err: errors.New("usage: /label <entry-id> [label]")}
			}
			label := strings.Join(command.Args[1:], " ")
			var tree session.SessionTree
			if err := runtime.Request(ctx, desktopipc.MethodSetSessionEntryLabel, map[string]string{
				"sessionId": sessionID, "entryId": command.Args[0], "label": label,
			}, &tree); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session label", Content: first(label, "Label cleared")}
		case "export":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /export <path> [html|text|json]")}
			}
			format := "html"
			if len(command.Args) == 2 {
				format = command.Args[1]
			}
			var outputPath string
			if err := runtime.Request(ctx, desktopipc.MethodExportSession, map[string]any{
				"sessionId": sessionID, "outputPath": command.Args[0], "format": format, "allBranches": false,
			}, &outputPath); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session export", Content: outputPath}
		case "share":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /share <server-url> [blob|gist]")}
			}
			store := "blob"
			if len(command.Args) == 2 {
				store = command.Args[1]
			}
			var result struct {
				URL string `json:"url"`
			}
			if err := runtime.Request(ctx, desktopipc.MethodShareSession, map[string]any{
				"sessionId": sessionID, "serverUrl": command.Args[0], "store": store, "allBranches": false,
			}, &result); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Encrypted share", Content: result.URL}
		case "usage":
			scope := string(session.UsageScopeProject)
			if len(command.Args) == 1 && command.Args[0] == "all" {
				scope = string(session.UsageScopeAll)
			} else if len(command.Args) > 0 {
				return sessionOperationResultMsg{Err: errors.New("usage: /usage [all]")}
			}
			var report session.UsageReport
			if err := runtime.Request(ctx, desktopipc.MethodUsageReport, map[string]string{"scope": scope}, &report); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Usage", Content: usageview.Text(report)}
		case "import":
			if len(command.Args) != 3 || (command.Args[0] != "claude" && command.Args[0] != "codex") {
				return sessionOperationResultMsg{Err: errors.New("usage: /import <claude|codex> <jsonl-path> <target-session-id>")}
			}
			var loaded session.Session
			if err := runtime.Request(ctx, desktopipc.MethodImportSession, map[string]string{
				"source": command.Args[0], "path": command.Args[1], "targetSessionId": command.Args[2],
			}, &loaded); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session import", Content: "Imported " + loaded.ID}
		default:
			return sessionOperationResultMsg{Err: errors.New("unknown session operation")}
		}
	}
}

func runCollabOperation(runtime Runtime, sessionID string, command Command, currentHost *collab.Host, currentGuest *collab.Guest) tea.Cmd {
	return func() tea.Msg {
		action := "status"
		if len(command.Args) > 0 {
			action = command.Args[0]
		}
		if action == "status" && currentGuest != nil {
			replica := currentGuest.Snapshot()
			return collabOperationResultMsg{Guest: currentGuest, Replica: &replica, Content: fmt.Sprintf("Joined %s · read-only: %t", replica.Session.Title, replica.ReadOnly)}
		}
		if action == "join" {
			if len(command.Args) != 2 || currentHost != nil || currentGuest != nil {
				return collabOperationResultMsg{Err: errors.New("usage: /collab join <link>")}
			}
			guest := collab.NewGuest(collab.GuestOptions{Name: "Azem TUI"})
			if err := guest.Join(context.Background(), command.Args[1]); err != nil {
				return collabOperationResultMsg{Err: err}
			}
			replica := guest.Snapshot()
			return collabOperationResultMsg{Guest: guest, Replica: &replica, Content: fmt.Sprintf("Joined %s · read-only: %t", replica.Session.Title, replica.ReadOnly)}
		}
		if action != "status" && action != "host" && action != "stop" {
			return collabOperationResultMsg{Err: errors.New("usage: /collab [host [relay-url] | join <link> | stop | status]")}
		}
		relayURL := ""
		if action == "host" && len(command.Args) > 1 {
			relayURL = command.Args[1]
		}
		var state desktop.CollaborationState
		if err := runtime.Request(context.Background(), desktopipc.MethodCollaboration, desktop.CollaborationRequest{
			Action: action, SessionID: sessionID, RelayURL: relayURL,
		}, &state); err != nil {
			return collabOperationResultMsg{Err: err}
		}
		switch state.State {
		case "hosting":
			return collabOperationResultMsg{Content: fmt.Sprintf("Writable: %s\nView: %s\nParticipants: %d", state.Link, state.ViewLink, state.Participants)}
		case "starting":
			return collabOperationResultMsg{Content: "Collaboration is starting."}
		default:
			return collabOperationResultMsg{Content: "Collaboration is inactive."}
		}
	}
}

func waitCollabGuest(guest *collab.Guest) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-guest.Events()
		return collabGuestEventMsg{Guest: guest, Event: event, OK: ok}
	}
}

func (m *AppModel) applyCollabReplica(replica collab.Replica) {
	m.transcript = make([]Block, 0, len(replica.Blocks))
	for _, block := range replica.Blocks {
		m.transcript = append(m.transcript, replicatedBlock(block))
	}
	m.sessionID = replica.Session.ID
	m.status = map[bool]string{true: "Collab view", false: "Collab guest"}[replica.ReadOnly]
	m.invalidateTranscriptLayout()
	m.transcriptTop = 0
}

func replicatedBlock(block session.Block) Block {
	kind := BlockKind(block.Kind)
	if kind != BlockUser && kind != BlockAssistant && kind != BlockTool && kind != BlockPlan && kind != BlockQuestion {
		kind = BlockAssistant
	}
	return Block{
		Kind: kind, RunID: block.RunID, Title: first(block.Title, strings.ReplaceAll(block.Kind, "_", " ")),
		Content: block.Content, State: first(block.State, "completed"), Attachments: append([]session.Attachment(nil), block.Attachments...),
	}
}

func decodeCollabAppEvent(raw json.RawMessage) (app.Event, bool) {
	var event app.Event
	if len(raw) == 0 || json.Unmarshal(raw, &event) != nil {
		return app.Event{}, false
	}
	return event, true
}

func broadcastCollabEvent(host *collab.Host, event app.Event) {
	if host == nil {
		return
	}
	encoded, err := json.Marshal(event)
	if err == nil {
		_ = host.BroadcastEvent(context.Background(), encoded)
	}
}
