package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestRPCServerRecoversParseErrorsCorrelatesResponsesAndMutatesSession(t *testing.T) {
	ctx := context.Background()
	service, sessions, closeStore := rpcTestRuntime(t, ctx)
	defer closeStore()
	entryID := fmt.Sprintf("session:%020d", 0)
	input := strings.Join([]string{
		`not-json`,
		`{"id":"protocol","type":"negotiate_protocol","protocolVersion":2}`,
		`{"id":"state","type":"get_state"}`,
		`{"id":"page","type":"get_messages_page","limit":1}`,
		`{"id":"rename","type":"set_session_name","name":"Renamed"}`,
		`{"id":"branch","type":"branch","entryId":"` + entryID + `"}`,
		`{"id":"last","type":"get_last_assistant_text"}`,
		`{"id":"unknown","type":"does_not_exist"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Input: strings.NewReader(input), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	frames := decodeRPCObjects(t, output.Bytes())
	if len(frames) != 9 || frames[0]["type"] != "ready" {
		t.Fatalf("frames=%#v", frames)
	}
	responses := make(map[string]map[string]any)
	parseFailures, unknownWithoutID := 0, false
	for _, frame := range frames[1:] {
		id, _ := frame["id"].(string)
		if id != "" {
			responses[id] = frame
		}
		if frame["command"] == "parse" && frame["success"] == false {
			parseFailures++
		}
		if frame["command"] == "does_not_exist" {
			_, hasID := frame["id"]
			unknownWithoutID = !hasID && frame["success"] == false
		}
	}
	if parseFailures != 1 || !unknownWithoutID || responses["protocol"]["success"] != true || responses["state"]["success"] != true || responses["page"]["success"] != true || responses["branch"]["success"] != true {
		t.Fatalf("responses=%#v parse=%d unknown=%v", responses, parseFailures, unknownWithoutID)
	}
	loaded, err := sessions.LoadSession(ctx, "session")
	if err != nil || loaded.Title != "Renamed" {
		t.Fatalf("renamed session=%#v error=%v", loaded, err)
	}
}

func TestRPCMessageCursorRejectsChangedSnapshot(t *testing.T) {
	ctx := context.Background()
	service, sessions, closeStore := rpcTestRuntime(t, ctx)
	defer closeStore()
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Input: strings.NewReader(""), Output: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := server.messagesPage(ctx, "", 1)
	if err != nil || page.NextCursor == "" || len(page.Messages) != 1 {
		t.Fatalf("page=%#v error=%v", page, err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.messagesPage(ctx, page.NextCursor, 1); err != errStaleCursor {
		t.Fatalf("stale cursor error=%v", err)
	}
	server.mu.Lock()
	server.activeRun = "run-active"
	server.mu.Unlock()
	if _, err := server.messagesPage(ctx, "", 1); err != errSessionBusy {
		t.Fatalf("busy cursor error=%v", err)
	}
}

func TestRPCProtocolV2ChunksOversizedFramesLosslessly(t *testing.T) {
	ctx := context.Background()
	service, sessions, closeStore := rpcTestRuntime(t, ctx)
	defer closeStore()
	random := make([]byte, 2<<20)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: base64.RawStdEncoding.EncodeToString(random)}); err != nil {
		t.Fatal(err)
	}
	input := `{"id":"protocol","type":"negotiate_protocol","protocolVersion":2}` + "\n" + `{"id":"messages","type":"get_messages"}` + "\n"
	var output bytes.Buffer
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Input: strings.NewReader(input), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	lines := splitLines(output.Bytes())
	if len(lines) < 4 {
		t.Fatalf("chunk lines=%d", len(lines))
	}
	var chunks []chunkFrame
	for _, line := range lines {
		if len(line)+1 > MaxFrameBytes {
			t.Fatalf("physical frame bytes=%d", len(line)+1)
		}
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &header) == nil && header.Type == "rpc_chunk" {
			var chunk chunkFrame
			if err := json.Unmarshal(line, &chunk); err != nil {
				t.Fatal(err)
			}
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks=%#v", chunks)
	}
	var reassembled bytes.Buffer
	for index, chunk := range chunks {
		if chunk.Index != index || chunk.Count != len(chunks) || chunk.ChunkID != chunks[0].ChunkID {
			t.Fatalf("chunk sequence=%#v", chunks)
		}
		decoded, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		reassembled.Write(decoded)
	}
	if reassembled.Len() != chunks[0].ByteLength {
		t.Fatalf("reassembled=%d want=%d", reassembled.Len(), chunks[0].ByteLength)
	}
	var response Response
	if err := json.Unmarshal(reassembled.Bytes(), &response); err != nil || response.ID != "messages" || !response.Success {
		t.Fatalf("logical response=%#v error=%v", response, err)
	}
}

func TestRPCV1OversizeProducesBoundedTransportError(t *testing.T) {
	var output bytes.Buffer
	writer := newFrameWriter(&output)
	if err := writer.write(map[string]string{"payload": strings.Repeat("x", MaxFrameBytes+1)}); err != nil {
		t.Fatal(err)
	}
	lines := splitLines(output.Bytes())
	if len(lines) != 1 || len(lines[0])+1 > MaxFrameBytes {
		t.Fatalf("v1 lines=%d bytes=%d", len(lines), len(lines[0]))
	}
	var response Response
	if err := json.Unmarshal(lines[0], &response); err != nil || response.Code != "frame_too_large" || response.Success {
		t.Fatalf("fallback=%#v error=%v", response, err)
	}
}

func rpcTestRuntime(t *testing.T, ctx context.Context) (*app.Service, *session.Service, func()) {
	t.Helper()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "RPC", ProviderID: "provider", ModelID: "model", Reasoning: "low", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []session.Block{{Kind: "user", Content: "one"}, {Kind: "assistant", Content: "two"}} {
		if _, err := sessions.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	service := app.NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	return service, sessions, func() {
		shutdownCtx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = service.Shutdown(shutdownCtx)
		_ = store.Close(context.Background())
	}
}

func decodeRPCObjects(t *testing.T, payload []byte) []map[string]any {
	t.Helper()
	lines := splitLines(payload)
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		result = append(result, value)
	}
	return result
}

func splitLines(payload []byte) [][]byte {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 64<<10), MaxFrameBytes)
	lines := make([][]byte, 0)
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	return lines
}
