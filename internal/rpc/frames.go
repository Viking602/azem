package rpc

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type chunkFrame struct {
	Type       string `json:"type"`
	ChunkID    string `json:"chunkId"`
	Index      int    `json:"index"`
	Count      int    `json:"count"`
	ByteLength int    `json:"byteLength"`
	Data       string `json:"data"`
}

type frameWriter struct {
	mu       sync.Mutex
	output   *bufio.Writer
	protocol int
	chunks   atomic.Uint64
	writeErr error
}

func newFrameWriter(output io.Writer) *frameWriter {
	return &frameWriter{output: bufio.NewWriter(output), protocol: ProtocolVersion}
}

func (writer *frameWriter) setProtocol(protocol int) {
	writer.mu.Lock()
	writer.protocol = protocol
	writer.mu.Unlock()
}

func (writer *frameWriter) err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writeErr
}

func (writer *frameWriter) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.writeErr != nil {
		return writer.writeErr
	}
	if len(encoded)+1 <= MaxFrameBytes {
		return writer.writeLine(encoded)
	}
	if writer.protocol < LosslessProtocolVersion {
		fallback, _ := json.Marshal(Response{Type: "response", Command: "transport", Success: false, Error: fmt.Sprintf("RPC frame exceeds %d bytes; negotiate protocol v2 or use pagination", MaxFrameBytes), Code: "frame_too_large"})
		return writer.writeLine(fallback)
	}
	if len(encoded) > MaxReassembledFrameBytes {
		fallback, _ := json.Marshal(Response{Type: "response", Command: "transport", Success: false, Error: fmt.Sprintf("RPC logical frame exceeds %d bytes", MaxReassembledFrameBytes), Code: "frame_too_large"})
		return writer.writeLine(fallback)
	}
	count := (len(encoded) + chunkPayloadBytes - 1) / chunkPayloadBytes
	chunkID := fmt.Sprintf("rpc-%d", writer.chunks.Add(1))
	for index := 0; index < count; index++ {
		start := index * chunkPayloadBytes
		end := min(start+chunkPayloadBytes, len(encoded))
		chunk, err := json.Marshal(chunkFrame{
			Type: "rpc_chunk", ChunkID: chunkID, Index: index, Count: count, ByteLength: len(encoded),
			Data: base64.StdEncoding.EncodeToString(encoded[start:end]),
		})
		if err != nil {
			return err
		}
		if len(chunk)+1 > MaxFrameBytes {
			return errors.New("RPC chunk exceeds physical frame limit")
		}
		if err := writer.writeLine(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (writer *frameWriter) writeLine(encoded []byte) error {
	if _, err := writer.output.Write(encoded); err != nil {
		writer.writeErr = err
		return err
	}
	if err := writer.output.WriteByte('\n'); err != nil {
		writer.writeErr = err
		return err
	}
	if err := writer.output.Flush(); err != nil {
		writer.writeErr = err
		return err
	}
	return nil
}
