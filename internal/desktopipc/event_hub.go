package desktopipc

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

type EventRecord struct {
	Sequence   uint64
	Channel    Channel
	Payload    json.RawMessage
	Binary     *BinaryMetadata
	Data       []byte
	ReplaceKey string
	Lossless   bool
	bytes      int
}

type ReplayResult struct {
	Events          []EventRecord
	CurrentSequence uint64
	ResyncRequired  bool
}

type Subscription struct {
	ID     uint64
	Events <-chan EventRecord
	hub    *EventHub
}

func (subscription *Subscription) Close() {
	if subscription == nil || subscription.hub == nil {
		return
	}
	subscription.hub.unsubscribe(subscription.ID)
}

type EventHub struct {
	mu             sync.Mutex
	sequence       uint64
	replayBytes    int
	maxReplayBytes int
	maxClientBytes int
	replay         []EventRecord
	replayHead     int
	nextClient     uint64
	clients        map[uint64]*clientEventQueue
}

type clientEventQueue struct {
	mu        sync.Mutex
	channel   chan EventRecord
	bytes     int
	maxBytes  int
	closed    bool
	resync    bool
	pending   []EventRecord
	replaceAt map[string]int
	notify    chan struct{}
	done      chan struct{}
}

func NewEventHub(maxReplayBytes, maxClientBytes int) *EventHub {
	if maxReplayBytes <= 0 {
		maxReplayBytes = DefaultReplayBytes
	}
	if maxClientBytes <= 0 {
		maxClientBytes = DefaultClientQueueBytes
	}
	return &EventHub{maxReplayBytes: maxReplayBytes, maxClientBytes: maxClientBytes, clients: make(map[uint64]*clientEventQueue)}
}

func (hub *EventHub) Publish(channel Channel, payload any, replaceKey string, lossless bool) (uint64, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return hub.PublishJSON(channel, encoded, replaceKey, lossless), nil
}

func (hub *EventHub) PublishJSON(channel Channel, payload json.RawMessage, replaceKey string, lossless bool) uint64 {
	return hub.publishRecord(EventRecord{
		Channel: channel, Payload: append(json.RawMessage(nil), payload...),
		ReplaceKey: replaceKey, Lossless: lossless, bytes: len(payload) + len(replaceKey) + 64,
	})
}

func (hub *EventHub) PublishBinary(channel Channel, metadata BinaryMetadata, data []byte, lossless bool) uint64 {
	metadata.Channel = channel
	return hub.publishRecord(EventRecord{
		Channel: channel, Binary: &metadata, Data: append([]byte(nil), data...),
		Lossless: lossless, bytes: len(data) + len(metadata.TransferID) + 128,
	})
}

func (hub *EventHub) publishRecord(record EventRecord) uint64 {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.sequence++
	record.Sequence = hub.sequence
	if record.Binary != nil {
		record.Binary.Sequence = record.Sequence
	}
	hub.replay = append(hub.replay, cloneEventRecord(record))
	hub.replayBytes += record.bytes
	for hub.replayBytes > hub.maxReplayBytes && hub.replayHead < len(hub.replay) {
		hub.replayBytes -= hub.replay[hub.replayHead].bytes
		hub.replay[hub.replayHead] = EventRecord{}
		hub.replayHead++
	}
	if hub.replayHead > 4096 && hub.replayHead*2 >= len(hub.replay) {
		hub.replay = append([]EventRecord(nil), hub.replay[hub.replayHead:]...)
		hub.replayHead = 0
	}
	for _, client := range hub.clients {
		client.enqueue(record)
	}
	return record.Sequence
}

func (hub *EventHub) Replay(after uint64) ReplayResult {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	result := ReplayResult{CurrentSequence: hub.sequence}
	if hub.replayUnavailable(after) {
		result.ResyncRequired = true
		return result
	}
	for index := hub.replayStart(after); index < len(hub.replay); index++ {
		result.Events = append(result.Events, cloneEventRecord(hub.replay[index]))
	}
	return result
}

func (hub *EventHub) Subscribe(buffer, lastSequence uint64) (*Subscription, ReplayResult) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextClient++
	queueSize := int(buffer)
	if queueSize <= 0 || queueSize > 4096 {
		queueSize = 256
	}
	client := &clientEventQueue{
		channel: make(chan EventRecord, queueSize), maxBytes: hub.maxClientBytes,
		replaceAt: make(map[string]int), notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
	hub.clients[hub.nextClient] = client
	go client.pump()
	replay := ReplayResult{CurrentSequence: hub.sequence}
	if hub.replayUnavailable(lastSequence) {
		replay.ResyncRequired = true
	} else {
		for index := hub.replayStart(lastSequence); index < len(hub.replay); index++ {
			replay.Events = append(replay.Events, cloneEventRecord(hub.replay[index]))
		}
	}
	return &Subscription{ID: hub.nextClient, Events: client.channel, hub: hub}, replay
}

func (hub *EventHub) replayStart(after uint64) int {
	count := len(hub.replay) - hub.replayHead
	return hub.replayHead + sort.Search(count, func(index int) bool {
		return hub.replay[hub.replayHead+index].Sequence > after
	})
}

func (hub *EventHub) replayUnavailable(after uint64) bool {
	if after >= hub.sequence {
		return false
	}
	if hub.replayHead >= len(hub.replay) {
		return true
	}
	return hub.replay[hub.replayHead].Sequence > after+1
}

func (hub *EventHub) CurrentSequence() uint64 {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.sequence
}

func (hub *EventHub) unsubscribe(id uint64) {
	hub.mu.Lock()
	client := hub.clients[id]
	delete(hub.clients, id)
	hub.mu.Unlock()
	if client != nil {
		client.close()
	}
}

func (client *clientEventQueue) enqueue(record EventRecord) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return
	}
	if record.ReplaceKey != "" && !record.Lossless {
		if index, exists := client.replaceAt[record.ReplaceKey]; exists && index < len(client.pending) {
			client.bytes -= client.pending[index].bytes
			client.pending[index] = cloneEventRecord(record)
			client.bytes += record.bytes
			client.signal()
			return
		}
	}
	if client.bytes+record.bytes > client.maxBytes {
		client.pending = client.pending[:0]
		clear(client.replaceAt)
		client.bytes = 0
		client.resync = true
		client.signal()
		return
	}
	client.pending = append(client.pending, cloneEventRecord(record))
	client.bytes += record.bytes
	if record.ReplaceKey != "" && !record.Lossless {
		client.replaceAt[record.ReplaceKey] = len(client.pending) - 1
	}
	client.signal()
}

func (client *clientEventQueue) pump() {
	for range client.notify {
		for {
			clientRecord, ok := client.dequeue()
			if !ok {
				break
			}
			select {
			case client.channel <- clientRecord:
			case <-client.done:
				close(client.channel)
				return
			}
		}
	}
	close(client.channel)
}

func (client *clientEventQueue) dequeue() (EventRecord, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return EventRecord{}, false
	}
	if client.resync {
		client.resync = false
		payload, _ := json.Marshal(map[string]any{"reason": "client_queue_overflow"})
		return EventRecord{Channel: ChannelDaemon, Payload: payload, Lossless: true, ReplaceKey: "", bytes: len(payload)}, true
	}
	if len(client.pending) == 0 {
		return EventRecord{}, false
	}
	record := client.pending[0]
	client.pending[0] = EventRecord{}
	client.pending = client.pending[1:]
	client.bytes -= record.bytes
	client.rebuildReplaceIndexes()
	return record, true
}

func (client *clientEventQueue) rebuildReplaceIndexes() {
	clear(client.replaceAt)
	for index, record := range client.pending {
		if record.ReplaceKey != "" && !record.Lossless {
			client.replaceAt[record.ReplaceKey] = index
		}
	}
}

func (client *clientEventQueue) signal() {
	select {
	case client.notify <- struct{}{}:
	default:
	}
}

func (client *clientEventQueue) close() {
	client.mu.Lock()
	if !client.closed {
		client.closed = true
		close(client.done)
		close(client.notify)
	}
	client.mu.Unlock()
}

func cloneEventRecord(record EventRecord) EventRecord {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.Data = append([]byte(nil), record.Data...)
	if record.Binary != nil {
		metadata := *record.Binary
		record.Binary = &metadata
	}
	return record
}

func eventEnvelope(record EventRecord) Envelope {
	envelope := NewEnvelope(FrameEventBatch)
	envelope.Channel = record.Channel
	envelope.Sequence = record.Sequence
	envelope.Payload = record.Payload
	return envelope
}

func validateEventChannel(channel Channel) error {
	switch channel {
	case ChannelRuntime, ChannelPullRequest, ChannelTerminal, ChannelDaemon:
		return nil
	default:
		return fmt.Errorf("unsupported IPC event channel %q", channel)
	}
}
