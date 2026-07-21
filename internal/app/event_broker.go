package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	eventDeltaCoalesceWindow = 20 * time.Millisecond
	maxQueuedEventBytes      = 8 << 20
	maxQueuedEventCount      = 4096
)

var errEventBrokerOverloaded = errors.New("UI event backlog exceeded the safe limit")

type eventPublishStatus uint8

const (
	eventPublishAccepted eventPublishStatus = iota
	eventPublishClosed
	eventPublishOverloaded
)

type queuedEvent struct {
	event   Event
	text    []byte
	readyAt time.Time
	size    int
}

// eventBroker separates event producers, including provider SSE readers, from
// the TUI consumer. Lifecycle events retain queue order, while adjacent work
// between lifecycle barriers can coalesce by stream identity.
type eventBroker struct {
	mu          sync.Mutex
	queue       []queuedEvent
	head        int
	queuedBytes int
	notify      chan struct{}
	closed      bool
	overloaded  bool
	window      time.Duration
	maxBytes    int
	maxEvents   int
}

func newEventBroker(window time.Duration) *eventBroker {
	return &eventBroker{
		notify: make(chan struct{}), window: window,
		maxBytes: maxQueuedEventBytes, maxEvents: maxQueuedEventCount,
	}
}

func (b *eventBroker) Publish(event Event) eventPublishStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return eventPublishClosed
	}
	terminal := isTerminalEvent(event.Kind)
	if b.overloaded && !terminal {
		return eventPublishOverloaded
	}
	event = event.Clone()
	if isCoalescibleEvent(event.Kind) {
		if len(b.queue) > b.head && sameEventStream(b.queue[len(b.queue)-1].event, event) {
			current := &b.queue[len(b.queue)-1]
			previousSize := current.size
			mergeCoalescibleEvent(current, event)
			b.queuedBytes += current.size - previousSize
		} else {
			current := newQueuedEvent(event, time.Now().Add(b.window), true)
			b.queue = append(b.queue, current)
			b.queuedBytes += current.size
		}
		b.signal()
		if b.queuedBytes > b.maxBytes || len(b.queue)-b.head > b.maxEvents {
			b.overloaded = true
			return eventPublishOverloaded
		}
		return eventPublishAccepted
	}
	// A lifecycle event is an ordering barrier. Any delta that precedes it must
	// be visible before the lifecycle transition, without waiting for the
	// coalescing window to expire.
	for index := len(b.queue) - 1; index >= b.head; index-- {
		if !isCoalescibleEvent(b.queue[index].event.Kind) {
			break
		}
		b.queue[index].readyAt = time.Time{}
	}
	current := newQueuedEvent(event, time.Time{}, false)
	b.queue = append(b.queue, current)
	b.queuedBytes += current.size
	if b.queuedBytes > b.maxBytes || len(b.queue)-b.head > b.maxEvents {
		b.overloaded = true
		if !terminal {
			b.signal()
			return eventPublishOverloaded
		}
	}
	b.signal()
	return eventPublishAccepted
}

func eventDeliveryError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errEventBrokerOverloaded
}

func (b *eventBroker) Next(ctx context.Context) (Event, error) {
	for {
		b.mu.Lock()
		if b.head < len(b.queue) {
			current := b.queue[b.head]
			wait := time.Until(current.readyAt)
			if current.readyAt.IsZero() || wait <= 0 {
				b.queue[b.head] = queuedEvent{}
				b.head++
				b.queuedBytes -= current.size
				if b.overloaded && b.queuedBytes <= b.maxBytes/2 && len(b.queue)-b.head <= b.maxEvents/2 {
					b.overloaded = false
				}
				b.compact()
				b.mu.Unlock()
				return materializeQueuedEvent(current), nil
			}
			notify := b.notify
			b.mu.Unlock()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return Event{}, ctx.Err()
			case <-notify:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
			continue
		}
		if b.closed {
			b.mu.Unlock()
			return Event{}, ioEOF{}
		}
		notify := b.notify
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-notify:
		}
	}
}

func (b *eventBroker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for index := b.head; index < len(b.queue); index++ {
		b.queue[index].readyAt = time.Time{}
	}
	b.signal()
	b.mu.Unlock()
}

func (b *eventBroker) signal() {
	close(b.notify)
	b.notify = make(chan struct{})
}

func (b *eventBroker) compact() {
	if b.head == len(b.queue) {
		clear(b.queue)
		b.queue = b.queue[:0]
		b.head = 0
		return
	}
	if b.head >= 64 && b.head*2 >= len(b.queue) {
		oldLength := len(b.queue)
		remaining := copy(b.queue, b.queue[b.head:])
		clear(b.queue[remaining:oldLength])
		b.queue = b.queue[:remaining]
		b.head = 0
	}
}

func isCoalescibleEvent(kind EventKind) bool {
	switch kind {
	case EventTextDelta, EventThinkingDelta, EventToolUpdate:
		return true
	default:
		return false
	}
}

func isTerminalEvent(kind EventKind) bool {
	switch kind {
	case EventRunFinished, EventRunFailed, EventRunCancelled:
		return true
	default:
		return false
	}
}

func sameEventStream(left, right Event) bool {
	return left.Kind == right.Kind &&
		left.SessionID == right.SessionID &&
		left.RunID == right.RunID &&
		left.AgentID == right.AgentID &&
		left.ToolCallID == right.ToolCallID
}

func mergeCoalescibleEvent(target *queuedEvent, next Event) {
	if next.Text != "" {
		previousCapacity := cap(target.text)
		if target.event.Kind == EventToolUpdate && len(target.text) > 0 && target.text[len(target.text)-1] != '\n' {
			target.text = append(target.text, '\n')
		}
		target.text = append(target.text, next.Text...)
		target.size += cap(target.text) - previousCapacity
	}
	target.size += len(next.State) - len(target.event.State)
	target.event.State = next.State
	target.event.At = next.At
	if next.Data != nil {
		if target.event.Data == nil {
			target.event.Data = make(map[string]string, len(next.Data))
		}
		for key, value := range next.Data {
			if previous, exists := target.event.Data[key]; exists {
				target.size += len(value) - len(previous)
			} else {
				target.size += len(key) + len(value)
			}
			target.event.Data[key] = value
		}
	}
}

func materializeQueuedEvent(current queuedEvent) Event {
	if current.text == nil {
		return current.event
	}
	current.event.Text = string(current.text)
	return current.event
}

func newQueuedEvent(event Event, readyAt time.Time, coalescible bool) queuedEvent {
	if !coalescible {
		encoded, err := json.Marshal(event)
		if err == nil {
			return queuedEvent{event: event, readyAt: readyAt, size: len(encoded)}
		}
	}
	size := len(event.SessionID) + len(event.RunID) + len(event.AgentID) + len(event.ToolCallID) +
		len(event.ApprovalID) + len(event.Text) + len(event.State)
	for key, value := range event.Data {
		size += len(key) + len(value)
	}
	current := queuedEvent{event: event, readyAt: readyAt, size: size}
	if coalescible {
		current.text = append([]byte(nil), event.Text...)
		current.event.Text = ""
		current.size += cap(current.text) - len(event.Text)
	}
	return current
}
