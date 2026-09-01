package desktopipc

import (
	"testing"

	"github.com/Viking602/azem/internal/desktop"
)

func TestTerminalReplayBoundsEachTerminalAndPublishesBinary(t *testing.T) {
	hub := NewEventHub(1<<20, 1<<20)
	replay := NewTerminalReplay(hub, 5)
	event := desktop.TerminalEvent{Kind: "output", Session: desktop.TerminalSession{ID: "terminal-1"}}
	replay.Sink(event, []byte("abcd"))
	replay.Sink(event, []byte("efgh"))

	chunks := replay.Snapshot("terminal-1")
	if len(chunks) != 1 || string(chunks[0].data) != "efgh" {
		t.Fatalf("terminal replay = %#v", chunks)
	}
	if chunks[0].metadata.Index != 0 || chunks[0].metadata.Count != 1 || chunks[0].metadata.Purpose != "terminal_output" {
		t.Fatalf("terminal metadata = %#v", chunks[0].metadata)
	}
	events := hub.Replay(0)
	if events.ResyncRequired || len(events.Events) != 2 || events.Events[0].Binary == nil || events.Events[1].Binary == nil {
		t.Fatalf("hub replay = %#v", events)
	}

	replay.Sink(desktop.TerminalEvent{Kind: "terminal_exit", Session: event.Session}, nil)
	if chunks := replay.Snapshot("terminal-1"); len(chunks) != 0 {
		t.Fatalf("terminal exit retained %d chunks", len(chunks))
	}
}
