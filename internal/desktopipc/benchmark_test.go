package desktopipc

import (
	"bytes"
	"testing"
)

func BenchmarkEventHubPublishAndReplay(b *testing.B) {
	hub := NewEventHub(64<<20, 8<<20)
	payload := []byte(`{"kind":"text_delta","text":"streaming output"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		sequence := hub.PublishJSON(ChannelRuntime, payload, "", true)
		result := hub.Replay(sequence - 1)
		if len(result.Events) != 1 {
			b.Fatalf("replay event count = %d", len(result.Events))
		}
	}
}

func BenchmarkCodecBinaryRoundTrip256KiB(b *testing.B) {
	payload := bytes.Repeat([]byte{'x'}, MaxBinaryChunkBytes)
	metadata := BinaryMetadata{TransferID: "benchmark", Purpose: "terminal_output", Count: 1, ByteLength: int64(len(payload))}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		var stream bytes.Buffer
		if err := NewCodec(&stream).WriteBinary(metadata, payload); err != nil {
			b.Fatal(err)
		}
		frame, err := NewCodec(&stream).ReadFrame()
		if err != nil {
			b.Fatal(err)
		}
		if frame.Binary == nil || len(frame.Data) != len(payload) {
			b.Fatal("binary round trip was truncated")
		}
	}
}
