package desktopipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	wireControl          byte = 1
	wireBinary           byte = 2
	maxBinaryHeaderBytes      = 64 << 10
)

type DecodedFrame struct {
	Envelope *Envelope
	Binary   *BinaryMetadata
	Data     []byte
}

type Codec struct {
	reader *bufio.Reader
	writer *bufio.Writer
	mu     sync.Mutex
}

func NewCodec(stream io.ReadWriter) *Codec {
	return &Codec{reader: bufio.NewReaderSize(stream, 64<<10), writer: bufio.NewWriterSize(stream, 64<<10)}
}

func (codec *Codec) ReadFrame() (DecodedFrame, error) {
	var sizeBuffer [4]byte
	if _, err := io.ReadFull(codec.reader, sizeBuffer[:]); err != nil {
		return DecodedFrame{}, err
	}
	size := int(binary.BigEndian.Uint32(sizeBuffer[:]))
	if size < 1 || size > MaxControlFrameBytes {
		return DecodedFrame{}, fmt.Errorf("IPC physical frame size %d is invalid", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(codec.reader, payload); err != nil {
		return DecodedFrame{}, err
	}
	switch payload[0] {
	case wireControl:
		var envelope Envelope
		if err := json.Unmarshal(payload[1:], &envelope); err != nil {
			return DecodedFrame{}, fmt.Errorf("decode IPC control frame: %w", err)
		}
		if err := envelope.Validate(); err != nil {
			return DecodedFrame{}, err
		}
		return DecodedFrame{Envelope: &envelope}, nil
	case wireBinary:
		if len(payload) < 3 {
			return DecodedFrame{}, errors.New("IPC binary frame is truncated")
		}
		headerLength := int(binary.BigEndian.Uint16(payload[1:3]))
		if headerLength <= 0 || headerLength > maxBinaryHeaderBytes || 3+headerLength > len(payload) {
			return DecodedFrame{}, errors.New("IPC binary frame header is invalid")
		}
		var metadata BinaryMetadata
		if err := json.Unmarshal(payload[3:3+headerLength], &metadata); err != nil {
			return DecodedFrame{}, fmt.Errorf("decode IPC binary metadata: %w", err)
		}
		data := payload[3+headerLength:]
		if len(data) > MaxBinaryChunkBytes {
			return DecodedFrame{}, fmt.Errorf("IPC binary chunk exceeds %d bytes", MaxBinaryChunkBytes)
		}
		if metadata.TransferID == "" || metadata.Index < 0 || metadata.Count < 0 {
			return DecodedFrame{}, errors.New("IPC binary metadata is incomplete")
		}
		return DecodedFrame{Binary: &metadata, Data: data}, nil
	default:
		return DecodedFrame{}, fmt.Errorf("unsupported IPC wire frame type %d", payload[0])
	}
}

func (codec *Codec) WriteEnvelope(envelope Envelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return codec.writeFrame(wireControl, nil, encoded)
}

func (codec *Codec) WriteBinary(metadata BinaryMetadata, data []byte) error {
	if metadata.TransferID == "" {
		return errors.New("IPC binary transfer id is required")
	}
	if len(data) > MaxBinaryChunkBytes {
		return fmt.Errorf("IPC binary chunk exceeds %d bytes", MaxBinaryChunkBytes)
	}
	header, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(header) > maxBinaryHeaderBytes {
		return errors.New("IPC binary metadata is too large")
	}
	var prefix [2]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(header)))
	return codec.writeFrame(wireBinary, append(prefix[:], header...), data)
}

func (codec *Codec) WriteTerminalReplay(response Envelope, chunks []terminalChunk) error {
	if err := response.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	headers := make([][]byte, len(chunks))
	for index, chunk := range chunks {
		if chunk.metadata.TransferID == "" || len(chunk.data) > MaxBinaryChunkBytes {
			return errors.New("IPC terminal replay chunk is invalid")
		}
		header, headerErr := json.Marshal(chunk.metadata)
		if headerErr != nil {
			return headerErr
		}
		if len(header) > maxBinaryHeaderBytes {
			return errors.New("IPC binary metadata is too large")
		}
		var prefix [2]byte
		binary.BigEndian.PutUint16(prefix[:], uint16(len(header)))
		headers[index] = append(prefix[:], header...)
	}
	codec.mu.Lock()
	defer codec.mu.Unlock()
	if err := codec.writeFrameLocked(wireControl, nil, encoded); err != nil {
		return err
	}
	for index, chunk := range chunks {
		if err := codec.writeFrameLocked(wireBinary, headers[index], chunk.data); err != nil {
			return err
		}
	}
	return nil
}

func (codec *Codec) writeFrame(kind byte, header, payload []byte) error {
	codec.mu.Lock()
	defer codec.mu.Unlock()
	return codec.writeFrameLocked(kind, header, payload)
}

func (codec *Codec) writeFrameLocked(kind byte, header, payload []byte) error {
	size := 1 + len(header) + len(payload)
	if size > MaxControlFrameBytes {
		return fmt.Errorf("IPC physical frame exceeds %d bytes", MaxControlFrameBytes)
	}
	var sizeBuffer [4]byte
	binary.BigEndian.PutUint32(sizeBuffer[:], uint32(size))
	if _, err := codec.writer.Write(sizeBuffer[:]); err != nil {
		return err
	}
	if err := codec.writer.WriteByte(kind); err != nil {
		return err
	}
	if len(header) > 0 {
		if _, err := codec.writer.Write(header); err != nil {
			return err
		}
	}
	if len(payload) > 0 {
		if _, err := codec.writer.Write(payload); err != nil {
			return err
		}
	}
	return codec.writer.Flush()
}
