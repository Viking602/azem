package desktopipc

import (
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/desktop"
)

func TestEventHubReplayPreservesSequenceAndSignalsEviction(t *testing.T) {
	hub := NewEventHub(256, 1024)
	first, err := hub.Publish(ChannelRuntime, map[string]string{"text": "first"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Publish(ChannelRuntime, map[string]string{"text": "second"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("sequences = %d, %d", first, second)
	}
	replay := hub.Replay(first)
	if replay.ResyncRequired || len(replay.Events) != 1 || replay.Events[0].Sequence != second {
		t.Fatalf("replay = %#v", replay)
	}

	hub.PublishJSON(ChannelRuntime, []byte(`{"text":"`+strings.Repeat("x", 512)+`"}`), "", true)
	if replay := hub.Replay(0); !replay.ResyncRequired {
		t.Fatalf("evicted replay did not require resync: %#v", replay)
	}
}

func TestClientQueueCoalescesPendingReplaceableSnapshots(t *testing.T) {
	hub := NewEventHub(1<<20, 1<<20)
	subscription, _ := hub.Subscribe(1, 0)
	defer subscription.Close()
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "fill"}, "", true)
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "block"}, "", true)
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "old"}, "state", false)
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "new"}, "state", false)

	first := receiveRecord(t, subscription.Events)
	second := receiveRecord(t, subscription.Events)
	third := receiveRecord(t, subscription.Events)
	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 4 || !strings.Contains(string(third.Payload), "new") {
		t.Fatalf("records = %#v %#v %#v", first, second, third)
	}
}

func TestClientQueueOverflowRequestsDurableResync(t *testing.T) {
	hub := NewEventHub(1<<20, 128)
	subscription, _ := hub.Subscribe(1, 0)
	defer subscription.Close()
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "fill"}, "", true)
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": "block"}, "", true)
	_, _ = hub.Publish(ChannelRuntime, map[string]string{"value": strings.Repeat("x", 1024)}, "", true)

	for range 3 {
		record := receiveRecord(t, subscription.Events)
		if record.Sequence == 0 {
			return
		}
	}
	t.Fatal("overflow did not emit resync sentinel")
}

func TestRuntimeTextDeltasRemainLossless(t *testing.T) {
	for _, kind := range []string{"text_delta", "thinking_delta"} {
		replaceKey, lossless := runtimeEventPolicy(desktop.Event{Kind: kind, SessionID: "session", RunID: "run", Text: "delta"})
		if replaceKey != "" || !lossless {
			t.Fatalf("%s policy = key %q lossless %t", kind, replaceKey, lossless)
		}
	}
	replaceKey, lossless := runtimeEventPolicy(desktop.Event{Kind: "tool_update", SessionID: "session", RunID: "run", ToolCallID: "tool"})
	if replaceKey == "" || lossless {
		t.Fatalf("tool update policy = key %q lossless %t", replaceKey, lossless)
	}
}

func receiveRecord(t *testing.T, records <-chan EventRecord) EventRecord {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event record")
		return EventRecord{}
	}
}
