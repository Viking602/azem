package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	eventDeltaCoalesceWindow = 20 * time.Millisecond
	maxQueuedEventBytes      = 8 << 20
	maxQueuedEventCount      = 4096
)

type eventPublishStatus uint8

const (
	eventPublishAccepted eventPublishStatus = iota
	eventPublishClosed
)

type eventRunKey struct {
	sessionID string
	runID     string
}

type eventStreamKey struct {
	kind       EventKind
	sessionID  string
	runID      string
	agentID    string
	toolCallID string
	textPhase  string
}

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
	window      time.Duration
	maxBytes    int
	maxEvents   int
	pending     map[eventStreamKey]int
	degraded    map[eventRunKey]bool
}

func newEventBroker(window time.Duration) *eventBroker {
	return &eventBroker{
		notify: make(chan struct{}), window: window,
		maxBytes: maxQueuedEventBytes, maxEvents: maxQueuedEventCount,
		pending: make(map[eventStreamKey]int), degraded: make(map[eventRunKey]bool),
	}
}

func (b *eventBroker) Publish(event Event) eventPublishStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return eventPublishClosed
	}
	terminal := isTerminalEvent(event.Kind)
	event = event.Clone()
	if isCoalescibleEvent(event.Kind) {
		b.appendCoalescible(event)
		b.compactOverLimit()
		b.signal()
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
	clear(b.pending)
	current := newQueuedEvent(event, time.Time{}, false)
	b.queue = append(b.queue, current)
	b.queuedBytes += current.size
	b.compactOverLimit()
	if terminal {
		key := runKey(event)
		if b.degraded[key] {
			b.appendResync(key, "final")
			delete(b.degraded, key)
		}
	}
	b.signal()
	return eventPublishAccepted
}

func (b *eventBroker) appendCoalescible(event Event) {
	key := streamKey(event)
	if index, ok := b.pending[key]; ok && index >= b.head && index < len(b.queue) {
		current := &b.queue[index]
		previousSize := current.size
		mergeCoalescibleEvent(current, event)
		b.queuedBytes += current.size - previousSize
		return
	}
	current := newQueuedEvent(event, time.Now().Add(b.window), true)
	b.queue = append(b.queue, current)
	b.pending[key] = len(b.queue) - 1
	b.queuedBytes += current.size
}

func eventDeliveryError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ioEOF{}
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
				if isCoalescibleEvent(current.event.Kind) {
					key := streamKey(current.event)
					if b.pending[key] < b.head {
						delete(b.pending, key)
					}
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
		b.rebuildPending()
	}
}

// compactOverLimit discards only replaceable stream projections. The durable
// session timeline remains authoritative, and a resync event asks consumers to
// reload it. UI speed must never become a provider execution failure.
func (b *eventBroker) compactOverLimit() {
	if b.queuedBytes <= b.maxBytes && len(b.queue)-b.head <= b.maxEvents {
		return
	}
	remaining := append([]queuedEvent(nil), b.queue[b.head:]...)
	clear(b.queue)
	b.queue = b.queue[:0]
	b.head = 0
	b.queuedBytes = 0
	clear(b.pending)
	affected := make(map[eventRunKey]struct{})
	for _, current := range remaining {
		if isCoalescibleEvent(current.event.Kind) {
			key := runKey(current.event)
			if key.sessionID != "" {
				if !b.degraded[key] {
					affected[key] = struct{}{}
				}
				b.degraded[key] = true
			}
			continue
		}
		b.queue = append(b.queue, current)
		b.queuedBytes += current.size
	}
	for key := range affected {
		b.appendResync(key, "degraded")
	}
}

func (b *eventBroker) appendResync(key eventRunKey, state string) {
	if key.sessionID == "" {
		return
	}
	current := newQueuedEvent(Event{
		Kind: EventProjectionResync, SessionID: key.sessionID, RunID: key.runID, State: state,
	}, time.Time{}, false)
	b.queue = append(b.queue, current)
	b.queuedBytes += current.size
}

func (b *eventBroker) rebuildPending() {
	clear(b.pending)
	for index := len(b.queue) - 1; index >= b.head; index-- {
		if !isCoalescibleEvent(b.queue[index].event.Kind) {
			break
		}
		key := streamKey(b.queue[index].event)
		if _, exists := b.pending[key]; !exists {
			b.pending[key] = index
		}
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

func runKey(event Event) eventRunKey {
	return eventRunKey{sessionID: event.SessionID, runID: event.RunID}
}

func streamKey(event Event) eventStreamKey {
	return eventStreamKey{
		kind: event.Kind, sessionID: event.SessionID, runID: event.RunID,
		agentID: event.AgentID, toolCallID: event.ToolCallID, textPhase: event.TextPhase,
	}
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
