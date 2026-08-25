package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/collab"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/sessionexport"
	"github.com/Viking602/azem/internal/sessionimport"
	"github.com/Viking602/azem/internal/sessionshare"
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

type sessionRuntime interface {
	Sessions() *session.Service
}

type attachmentImportRuntime interface {
	ImportImageBytes(sessionID, name, mimeType string, data []byte) (session.Attachment, error)
}

type runtimeAttachmentImporter struct{ runtime attachmentImportRuntime }

func (adapter runtimeAttachmentImporter) ImportBytes(sessionID, name, mimeType string, data []byte) (session.Attachment, error) {
	return adapter.runtime.ImportImageBytes(sessionID, name, mimeType, data)
}

func runSessionOperation(runtime Runtime, sessionID string, command Command, workspace string) tea.Cmd {
	return func() tea.Msg {
		owner, ok := runtime.(sessionRuntime)
		if !ok || owner.Sessions() == nil {
			return sessionOperationResultMsg{Err: errors.New("session operations are unavailable")}
		}
		sessions := owner.Sessions()
		ctx := context.Background()
		if _, err := sessions.LoadSession(ctx, sessionID); err != nil {
			if _, ensureErr := sessions.Ensure(ctx, session.Session{ID: sessionID, Title: "New session", AgentMode: "single"}); ensureErr != nil {
				return sessionOperationResultMsg{Err: ensureErr}
			}
		}
		switch command.Name {
		case "tree":
			tree, err := sessions.LoadSessionTree(ctx, sessionID)
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			encoded, _ := json.MarshalIndent(tree, "", "  ")
			return sessionOperationResultMsg{Title: "Session tree", Content: string(encoded)}
		case "branch":
			if len(command.Args) != 1 {
				return sessionOperationResultMsg{Err: errors.New("usage: /branch <entry-id>")}
			}
			navigation, err := sessions.NavigateSessionTree(ctx, sessionID, command.Args[0])
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session branch", Content: fmt.Sprintf("Moved %s → %s", navigation.OldLeaf, navigation.NewLeaf), Refresh: true}
		case "fork":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /fork <target-session-id> [entry-id]")}
			}
			var err error
			if len(command.Args) == 2 {
				err = sessions.ForkAt(ctx, sessionID, command.Args[0], command.Args[1])
			} else {
				err = sessions.Fork(ctx, sessionID, command.Args[0])
			}
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session fork", Content: "Created " + command.Args[0]}
		case "label":
			if len(command.Args) < 1 {
				return sessionOperationResultMsg{Err: errors.New("usage: /label <entry-id> [label]")}
			}
			label := strings.Join(command.Args[1:], " ")
			if err := sessions.SetSessionEntryLabel(ctx, sessionID, command.Args[0], label); err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session label", Content: first(label, "Label cleared")}
		case "export":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /export <path> [html|text|json]")}
			}
			format := sessionexport.FormatHTML
			if len(command.Args) == 2 {
				format = sessionexport.Format(command.Args[1])
			}
			path, err := sessionexport.New(sessions).ExportFile(ctx, command.Args[0], sessionID, format, sessionexport.Options{})
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session export", Content: path}
		case "share":
			if len(command.Args) < 1 || len(command.Args) > 2 {
				return sessionOperationResultMsg{Err: errors.New("usage: /share <server-url> [blob|gist]")}
			}
			store := sessionshare.StoreBlob
			if len(command.Args) == 2 {
				store = sessionshare.Store(command.Args[1])
			}
			result, err := sessionshare.New(sessions).Share(ctx, sessionID, sessionshare.Options{ServerURL: command.Args[0], Store: store})
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Encrypted share", Content: result.URL}
		case "usage":
			scope := session.UsageScopeProject
			if len(command.Args) == 1 && command.Args[0] == "all" {
				scope = session.UsageScopeAll
			} else if len(command.Args) > 0 {
				return sessionOperationResultMsg{Err: errors.New("usage: /usage [all]")}
			}
			report, err := sessions.UsageReport(ctx, session.UsageReportQuery{Scope: scope, Workspace: workspace})
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Usage", Content: usageview.Text(report)}
		case "import":
			if len(command.Args) != 3 || (command.Args[0] != "claude" && command.Args[0] != "codex") {
				return sessionOperationResultMsg{Err: errors.New("usage: /import <claude|codex> <jsonl-path> <target-session-id>")}
			}
			absolute, err := filepath.Abs(command.Args[1])
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			stat, err := os.Stat(absolute)
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			attachmentRuntime, ok := runtime.(attachmentImportRuntime)
			if !ok {
				return sessionOperationResultMsg{Err: errors.New("attachment import is unavailable")}
			}
			info := sessionimport.Info{Source: sessionimport.Source(command.Args[0]), ID: strings.TrimSuffix(filepath.Base(absolute), filepath.Ext(absolute)), Path: absolute, Workspace: workspace, CreatedAt: stat.ModTime(), UpdatedAt: stat.ModTime()}
			loaded, err := sessionimport.New(sessions, runtimeAttachmentImporter{runtime: attachmentRuntime}).Import(ctx, info, command.Args[2], workspace)
			if err != nil {
				return sessionOperationResultMsg{Err: err}
			}
			return sessionOperationResultMsg{Title: "Session import", Content: "Imported " + loaded.ID}
		}
		return sessionOperationResultMsg{Err: errors.New("unknown session operation")}
	}
}

func runCollabOperation(runtime Runtime, sessionID string, command Command, currentHost *collab.Host, currentGuest *collab.Guest) tea.Cmd {
	return func() tea.Msg {
		if len(command.Args) == 0 || command.Args[0] == "status" {
			switch {
			case currentHost != nil:
				return collabOperationResultMsg{Host: currentHost, Content: fmt.Sprintf("Writable: %s\nView: %s\nParticipants: %d", currentHost.Link(), currentHost.ViewLink(), len(currentHost.Participants()))}
			case currentGuest != nil:
				replica := currentGuest.Snapshot()
				return collabOperationResultMsg{Guest: currentGuest, Replica: &replica, Content: fmt.Sprintf("Joined %s · read-only: %t", replica.Session.Title, replica.ReadOnly)}
			default:
				return collabOperationResultMsg{Content: "Collaboration is inactive."}
			}
		}
		switch command.Args[0] {
		case "host":
			if currentHost != nil || currentGuest != nil {
				return collabOperationResultMsg{Err: errors.New("collaboration is already active")}
			}
			relayURL := collab.DefaultRelayURL
			if len(command.Args) > 1 {
				relayURL = command.Args[1]
			}
			owner, ok := runtime.(sessionRuntime)
			if !ok || owner.Sessions() == nil {
				return collabOperationResultMsg{Err: errors.New("session operations are unavailable")}
			}
			if _, err := owner.Sessions().LoadSession(context.Background(), sessionID); err != nil {
				if _, ensureErr := owner.Sessions().Ensure(context.Background(), session.Session{ID: sessionID, Title: "New session", AgentMode: "single"}); ensureErr != nil {
					return collabOperationResultMsg{Err: ensureErr}
				}
			}
			host, err := collab.NewHost(collab.HostOptions{
				RelayURL: relayURL, SessionID: sessionID, Sessions: owner.Sessions(),
				OnPrompt: func(_ context.Context, _ collab.Participant, text string) error {
					_, err := runtime.StartTurn(text)
					return err
				},
				OnAbort: func(context.Context, collab.Participant) error {
					runtime.CancelActive()
					return nil
				},
			})
			if err != nil {
				return collabOperationResultMsg{Err: err}
			}
			if err := host.Start(context.Background()); err != nil {
				return collabOperationResultMsg{Err: err}
			}
			return collabOperationResultMsg{Host: host, Content: "Writable: " + host.Link() + "\nView: " + host.ViewLink()}
		case "join":
			if len(command.Args) != 2 || currentHost != nil || currentGuest != nil {
				return collabOperationResultMsg{Err: errors.New("usage: /collab join <link>")}
			}
			guest := collab.NewGuest(collab.GuestOptions{Name: "Azem TUI"})
			if err := guest.Join(context.Background(), command.Args[1]); err != nil {
				return collabOperationResultMsg{Err: err}
			}
			replica := guest.Snapshot()
			return collabOperationResultMsg{Guest: guest, Replica: &replica, Content: fmt.Sprintf("Joined %s · read-only: %t", replica.Session.Title, replica.ReadOnly)}
		default:
			return collabOperationResultMsg{Err: errors.New("usage: /collab [host [relay-url] | join <link> | stop | status]")}
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
