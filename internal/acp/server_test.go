package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestACPRequiresInitializationAndServesV2SessionMethods(t *testing.T) {
	ctx := context.Background()
	service, sessions, workspace, closeStore := acpTestRuntime(t, ctx)
	defer closeStore()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"session/list","params":{}}`,
		`not-json`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":2,"capabilities":{},"info":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/list","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":"session","cwd":"` + workspace + `"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"session/set_mode","params":{"sessionId":"session","modeId":"plan"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"unstable_session/fork","params":{"sessionId":"session"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"unknown","params":{}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Workspace: workspace, Input: strings.NewReader(input), Output: &output, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	frames := decodeACPFrames(t, output.Bytes())
	responses := make(map[string]map[string]any)
	parseErrors := 0
	for _, frame := range frames {
		id := fmt.Sprint(frame["id"])
		responses[id] = frame
		if errorValue, ok := frame["error"].(map[string]any); ok && errorValue["code"] == float64(-32700) {
			parseErrors++
		}
	}
	if parseErrors != 1 || errorCode(responses["1"]) != -32002 || errorCode(responses["7"]) != -32601 {
		t.Fatalf("ACP responses=%#v parse=%d", responses, parseErrors)
	}
	initialize := responses["2"]["result"].(map[string]any)
	if initialize["protocolVersion"] != float64(2) || initialize["info"].(map[string]any)["name"] != "azem" {
		t.Fatalf("initialize=%#v", initialize)
	}
	list := responses["3"]["result"].(map[string]any)
	if len(list["sessions"].([]any)) == 0 || responses["4"]["error"] != nil || responses["5"]["error"] != nil || responses["6"]["error"] != nil {
		t.Fatalf("session responses=%#v", responses)
	}
}

func TestACPPromptCompletionAndReplayNotifications(t *testing.T) {
	ctx := context.Background()
	service, sessions, workspace, closeStore := acpTestRuntime(t, ctx)
	defer closeStore()
	var output bytes.Buffer
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Workspace: workspace, Input: strings.NewReader(""), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	server.ctx = ctx
	server.cancel = func() {}
	server.mu.Lock()
	server.managed["session"].activeRun = "run-1"
	server.managed["session"].promptID = json.RawMessage(`11`)
	server.mu.Unlock()
	server.completeRun(app.Event{Kind: app.EventRunFinished, SessionID: "session", RunID: "run-1"})
	server.replaySession("session")
	frames := decodeACPFrames(t, output.Bytes())
	if len(frames) != 3 {
		t.Fatalf("completion/replay frames=%#v", frames)
	}
	if frames[0]["id"] != float64(11) || frames[0]["result"].(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("prompt response=%#v", frames[0])
	}
	for _, frame := range frames[1:] {
		if frame["method"] != "session/update" {
			t.Fatalf("replay notification=%#v", frame)
		}
	}
}

func TestACPPermissionAndElicitationRequestsUseClientRoundTrips(t *testing.T) {
	ctx := context.Background()
	service, sessions, workspace, closeStore := acpTestRuntime(t, ctx)
	defer closeStore()
	var output bytes.Buffer
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Workspace: workspace, Input: strings.NewReader(""), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	server.ctx = ctx
	server.cancel = func() {}
	server.requestPermission(app.Event{Kind: app.EventApprovalRequested, SessionID: "session", ApprovalID: "approval", ToolCallID: "tool", Text: "Approve?", Data: map[string]string{"name": "write"}})
	questions, _ := json.Marshal([]map[string]any{{"id": "strategy", "header": "Strategy", "question": "Choose", "options": []map[string]any{{"label": "safe", "description": "Safe"}}}})
	server.requestElicitation(app.Event{Kind: app.EventUserInputRequested, SessionID: "session", UserInputID: "input", ToolCallID: "ask", Data: map[string]string{"questions": string(questions)}})
	frames := decodeACPFrames(t, output.Bytes())
	if len(frames) != 2 || frames[0]["method"] != "session/request_permission" || frames[1]["method"] != "elicitation/create" {
		t.Fatalf("client requests=%#v", frames)
	}
	permissionID, elicitationID := frames[0]["id"].(string), frames[1]["id"].(string)
	server.handleClientResponse(request{JSONRPC: "2.0", ID: json.RawMessage(`"` + permissionID + `"`), Result: json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)})
	server.handleClientResponse(request{JSONRPC: "2.0", ID: json.RawMessage(`"` + elicitationID + `"`), Result: json.RawMessage(`{"action":"cancel"}`)})
	server.mu.Lock()
	remaining := len(server.permissions) + len(server.elicitations)
	server.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("pending client requests=%d", remaining)
	}
}

func TestACPRejectsCrossWorkspaceAndOversizedFrames(t *testing.T) {
	ctx := context.Background()
	service, sessions, workspace, closeStore := acpTestRuntime(t, ctx)
	defer closeStore()
	server, err := New(Options{Service: service, Sessions: sessions, SessionID: "session", Workspace: workspace, Input: strings.NewReader(""), Output: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	server.ctx = ctx
	if err := server.requireWorkspace(t.TempDir()); err == nil {
		t.Fatal("accepted another workspace")
	}
	var output bytes.Buffer
	writer := newWriter(&output)
	if err := writer.write(map[string]string{"payload": strings.Repeat("x", maxFrameBytes)}); err == nil {
		t.Fatal("accepted oversized ACP frame")
	}
}

func acpTestRuntime(t *testing.T, ctx context.Context) (*app.Service, *session.Service, string, func()) {
	t.Helper()
	workspace := t.TempDir()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "ACP"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "assistant", Content: "world"}); err != nil {
		t.Fatal(err)
	}
	service := app.NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	return service, sessions, workspace, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
		_ = store.Close(context.Background())
	}
}

func decodeACPFrames(t *testing.T, payload []byte) []map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 64<<10), maxFrameBytes)
	frames := make([]map[string]any, 0)
	for scanner.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatalf("decode frame %q: %v", scanner.Bytes(), err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func errorCode(frame map[string]any) int {
	if errorValue, ok := frame["error"].(map[string]any); ok {
		return int(errorValue["code"].(float64))
	}
	return 0
}
