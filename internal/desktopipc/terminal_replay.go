package desktopipc

import (
	"sync"

	"github.com/Viking602/azem/internal/desktop"
)

const DefaultTerminalReplayBytes = 4 << 20

type TerminalReplay struct {
	mu       sync.Mutex
	hub      *EventHub
	maxBytes int
	buffers  map[string]*terminalBuffer
}

type terminalBuffer struct {
	chunks []terminalChunk
	bytes  int
}

type terminalChunk struct {
	metadata BinaryMetadata
	data     []byte
}

func NewTerminalReplay(hub *EventHub, maxBytes int) *TerminalReplay {
	if maxBytes <= 0 {
		maxBytes = DefaultTerminalReplayBytes
	}
	return &TerminalReplay{hub: hub, maxBytes: maxBytes, buffers: make(map[string]*terminalBuffer)}
}

func (replay *TerminalReplay) Sink(event desktop.TerminalEvent, data []byte) {
	if replay == nil || event.Session.ID == "" {
		return
	}
	if event.Kind == "terminal_exit" {
		replay.Remove(event.Session.ID)
		return
	}
	if len(data) == 0 {
		return
	}
	for offset, index := 0, 0; offset < len(data); index++ {
		end := min(offset+MaxBinaryChunkBytes, len(data))
		chunkData := append([]byte(nil), data[offset:end]...)
		metadata := BinaryMetadata{
			TransferID: event.Session.ID, Purpose: "terminal_output", Channel: ChannelTerminal,
			Name: event.Session.ID, ByteLength: int64(len(chunkData)), Index: index, Count: 0,
		}
		replay.mu.Lock()
		buffer := replay.buffers[event.Session.ID]
		if buffer == nil {
			buffer = &terminalBuffer{}
			replay.buffers[event.Session.ID] = buffer
		}
		buffer.chunks = append(buffer.chunks, terminalChunk{metadata: metadata, data: chunkData})
		buffer.bytes += len(chunkData)
		for buffer.bytes > replay.maxBytes && len(buffer.chunks) > 0 {
			buffer.bytes -= len(buffer.chunks[0].data)
			buffer.chunks[0] = terminalChunk{}
			buffer.chunks = buffer.chunks[1:]
		}
		replay.mu.Unlock()
		if replay.hub != nil {
			replay.hub.PublishBinary(ChannelTerminal, metadata, chunkData, false)
		}
		offset = end
	}
}

func (replay *TerminalReplay) Snapshot(terminalID string) []terminalChunk {
	if replay == nil {
		return nil
	}
	replay.mu.Lock()
	defer replay.mu.Unlock()
	buffer := replay.buffers[terminalID]
	if buffer == nil {
		return nil
	}
	result := make([]terminalChunk, len(buffer.chunks))
	for index, chunk := range buffer.chunks {
		result[index] = terminalChunk{metadata: chunk.metadata, data: append([]byte(nil), chunk.data...)}
		result[index].metadata.Index = index
		result[index].metadata.Count = len(result)
	}
	return result
}

func (replay *TerminalReplay) Remove(terminalID string) {
	if replay == nil {
		return
	}
	replay.mu.Lock()
	delete(replay.buffers, terminalID)
	replay.mu.Unlock()
}
