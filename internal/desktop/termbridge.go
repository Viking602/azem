package desktop

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/desktop/termhost"
)

// TerminalSession is the secret-free session snapshot returned by Bridge
// methods and terminal events.
type TerminalSession struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	CWD   string `json:"cwd"`
	Shell string `json:"shell"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	State string `json:"state"`
}

// TerminalEvent is a desktop-local event family. It uses a dedicated Wails
// channel so PTY output cannot occupy the runtime event broker (UI-002).
type TerminalEvent struct {
	Sequence uint64          `json:"sequence"`
	Kind     string          `json:"kind"`
	Session  TerminalSession `json:"session"`
	Data     string          `json:"data,omitempty"`
	Encoding string          `json:"encoding,omitempty"`
	ExitCode *int            `json:"exitCode,omitempty"`
	At       time.Time       `json:"at"`
}

func (b *Bridge) ListTerminals() []TerminalSession {
	host, err := b.terminalHost()
	if err != nil {
		return nil
	}
	return terminalSessionDTOs(host.List())
}

func (b *Bridge) CreateTerminal(cols, rows int) (TerminalSession, error) {
	host, err := b.terminalHost()
	if err != nil {
		return TerminalSession{}, err
	}
	session, err := host.Create(cols, rows)
	if err != nil {
		return TerminalSession{}, err
	}
	return terminalSessionDTO(session), nil
}

func (b *Bridge) WriteTerminal(id, data string) error {
	host, err := b.terminalHost()
	if err != nil {
		return err
	}
	return host.Write(strings.TrimSpace(id), []byte(data))
}

func (b *Bridge) ResizeTerminal(id string, cols, rows int) error {
	host, err := b.terminalHost()
	if err != nil {
		return err
	}
	return host.Resize(strings.TrimSpace(id), cols, rows)
}

func (b *Bridge) CloseTerminal(id string) error {
	host, err := b.terminalHost()
	if err != nil {
		return err
	}
	return host.Close(strings.TrimSpace(id))
}

func (b *Bridge) terminalHost() (*termhost.Host, error) {
	if b == nil || b.terminals == nil {
		return nil, fmt.Errorf("embedded terminal is unavailable")
	}
	return b.terminals, nil
}

func (b *Bridge) emitTerminal(event termhost.Event) {
	if b.emit == nil {
		return
	}
	payload := TerminalEvent{
		Sequence: b.terminalSeq.Add(1),
		Kind:     event.Kind,
		Session:  terminalSessionDTO(event.Session),
		ExitCode: event.ExitCode,
		At:       time.Now().UTC(),
	}
	if len(event.Data) > 0 {
		payload.Data = base64.StdEncoding.EncodeToString(event.Data)
		payload.Encoding = "base64"
	}
	b.emit(TerminalEventName, payload)
}

func terminalSessionDTO(session termhost.Session) TerminalSession {
	return TerminalSession{
		ID: session.ID, Title: session.Title, CWD: session.CWD, Shell: session.Shell,
		Cols: session.Cols, Rows: session.Rows, State: session.State,
	}
}

func terminalSessionDTOs(sessions []termhost.Session) []TerminalSession {
	out := make([]TerminalSession, len(sessions))
	for index, session := range sessions {
		out[index] = terminalSessionDTO(session)
	}
	return out
}
