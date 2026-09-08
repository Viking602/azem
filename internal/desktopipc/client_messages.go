package desktopipc

import (
	"context"
	"errors"
	"sync"
)

// ClientMessageKind identifies an unsolicited frame delivered by Client.Next.
type ClientMessageKind string

const (
	ClientMessageEvent          ClientMessageKind = "event"
	ClientMessageBinary         ClientMessageKind = "binary"
	ClientMessageReplayComplete ClientMessageKind = "replay_complete"
	ClientMessageResyncRequired ClientMessageKind = "resync_required"
	ClientMessageDisconnected   ClientMessageKind = "disconnected"
)

// ClientMessage is an unsolicited daemon message. Kind determines which fields
// are populated: Event for control messages, Binary and Data for binary data,
// and Err for a terminal disconnect.
type ClientMessage struct {
	Kind     ClientMessageKind
	Event    Envelope
	Binary   BinaryMetadata
	Data     []byte
	Sequence uint64
	Err      error
}

type queuedClientMessage struct {
	message ClientMessage
	bytes   int
}

type clientMessageQueue struct {
	mu           sync.Mutex
	pending      []queuedClientMessage
	bytes        int
	maxBytes     int
	resyncQueued bool
	closed       bool
	terminalErr  error
	notify       chan struct{}
}

func newClientMessageQueue(maxBytes int) *clientMessageQueue {
	if maxBytes <= 0 {
		maxBytes = DefaultClientQueueBytes
	}
	return &clientMessageQueue{maxBytes: maxBytes, notify: make(chan struct{}, 1)}
}

// push returns false only when a second overflow happens before the caller has
// consumed the first resync marker. At that point the connection must be
// rebuilt because even its recovery signal is not making forward progress.
func (queue *clientMessageQueue) push(message ClientMessage, size int) bool {
	if size < 1 {
		size = 1
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.closed {
		return false
	}
	if message.Kind == ClientMessageResyncRequired && queue.resyncQueued {
		for index := range queue.pending {
			if queue.pending[index].message.Kind == ClientMessageResyncRequired {
				queue.pending[index].message = message
				queue.signalLocked()
				return true
			}
		}
	}
	if queue.resyncQueued {
		if size > queue.maxBytes || queue.bytes+size > queue.maxBytes {
			return false
		}
		return true
	}
	if size > queue.maxBytes || queue.bytes+size > queue.maxBytes {
		queue.pending = nil
		queue.bytes = 0
		message = clientQueueOverflowMessage(message.Sequence)
		size = clientMessageSize(message)
		queue.resyncQueued = true
	} else if message.Kind == ClientMessageResyncRequired {
		queue.resyncQueued = true
	}
	queue.pending = append(queue.pending, queuedClientMessage{message: cloneClientMessage(message), bytes: size})
	queue.bytes += size
	queue.signalLocked()
	return true
}

func (queue *clientMessageQueue) next(ctx context.Context) (ClientMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		queue.mu.Lock()
		if len(queue.pending) > 0 {
			item := queue.pending[0]
			queue.pending[0] = queuedClientMessage{}
			queue.pending = queue.pending[1:]
			queue.bytes -= item.bytes
			if item.message.Kind == ClientMessageResyncRequired {
				queue.resyncQueued = false
			}
			queue.mu.Unlock()
			return item.message, nil
		}
		if queue.closed {
			err := queue.terminalErr
			queue.mu.Unlock()
			if err == nil {
				err = ErrClientClosed
			}
			return ClientMessage{}, err
		}
		notify := queue.notify
		queue.mu.Unlock()
		select {
		case <-ctx.Done():
			return ClientMessage{}, ctx.Err()
		case <-notify:
		}
	}
}

func (queue *clientMessageQueue) finish(err error) {
	if err == nil {
		err = ErrClientClosed
	}
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return
	}
	queue.closed = true
	queue.terminalErr = err
	message := ClientMessage{Kind: ClientMessageDisconnected, Err: err}
	size := clientMessageSize(message)
	queue.pending = append(queue.pending, queuedClientMessage{message: message, bytes: size})
	queue.bytes += size
	queue.signalLocked()
	queue.mu.Unlock()
}

func (queue *clientMessageQueue) signalLocked() {
	select {
	case queue.notify <- struct{}{}:
	default:
	}
}

func clientQueueOverflowMessage(sequence uint64) ClientMessage {
	envelope := resyncEnvelope("client_queue_overflow", sequence)
	return ClientMessage{Kind: ClientMessageResyncRequired, Event: envelope, Sequence: sequence}
}

func clientMessageSize(message ClientMessage) int {
	return len(message.Event.Payload) + len(message.Data) + len(message.Binary.TransferID) + len(message.Binary.Name) + len(message.Binary.MediaType) + 256
}

func cloneClientMessage(message ClientMessage) ClientMessage {
	message.Event.Payload = append([]byte(nil), message.Event.Payload...)
	message.Data = append([]byte(nil), message.Data...)
	return message
}

var ErrClientClosed = errors.New("IPC client is closed")
