package tui

import (
	"strconv"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktopclient"
)

const connectionStateEvent app.EventKind = "connection_state"

func (m *AppModel) reduceConnectionEvent(event app.Event) bool {
	if event.Kind != connectionStateEvent {
		return false
	}
	m.connection.State = desktopclient.ConnectionState(event.State)
	m.connection.Error = event.Text
	m.connection.DaemonEpoch = event.Data["daemonEpoch"]
	if sequence, err := strconv.ParseUint(event.Data["wireSequence"], 10, 64); err == nil {
		m.connection.WireSequence = sequence
		m.lastWireCursor = max(m.lastWireCursor, sequence)
	}
	if m.connection.State == desktopclient.ConnectionOffline && event.Text != "" {
		m.errorBanner = event.Text
	}
	return true
}

func (m AppModel) mutationsEnabled() bool {
	return m.connection.State == "" || m.connection.State == desktopclient.ConnectionConnected
}
