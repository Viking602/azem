package desktopipc

import (
	"bytes"
	"testing"
)

func TestCodecRoundTripsControlAndBinaryFrames(t *testing.T) {
	var stream bytes.Buffer
	writer := NewCodec(&stream)
	request := Envelope{Version: ProtocolVersion, Kind: FrameRequest, ID: "request-1", Method: MethodInitialise}
	if err := writer.WriteEnvelope(request); err != nil {
		t.Fatal(err)
	}
	metadata := BinaryMetadata{TransferID: "transfer-1", Purpose: "attachment", Index: 0, Count: 1, ByteLength: 3}
	if err := writer.WriteBinary(metadata, []byte("abc")); err != nil {
		t.Fatal(err)
	}

	reader := NewCodec(&stream)
	control, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if control.Envelope == nil || control.Envelope.ID != request.ID || control.Envelope.Method != MethodInitialise {
		t.Fatalf("control frame = %#v", control)
	}
	binary, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if binary.Binary == nil || binary.Binary.TransferID != metadata.TransferID || string(binary.Data) != "abc" {
		t.Fatalf("binary frame = %#v data=%q", binary.Binary, binary.Data)
	}
}

func TestCodecRejectsOversizedPhysicalFrameBeforeAllocation(t *testing.T) {
	stream := bytes.NewBuffer([]byte{1, 0, 0, 1})
	_, err := NewCodec(stream).ReadFrame()
	if err == nil {
		t.Fatal("oversized frame was accepted")
	}
}
