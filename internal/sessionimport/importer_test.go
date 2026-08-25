package sessionimport

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/message"
)

type capturedAttachments struct {
	values [][]byte
}

func (capture *capturedAttachments) ImportBytes(_ string, name, mimeType string, data []byte) (session.Attachment, error) {
	capture.values = append(capture.values, append([]byte(nil), data...))
	return session.Attachment{ID: name, Name: name, MIME: mimeType, Path: "/managed/" + name, Size: int64(len(data))}, nil
}

func TestClaudeDiscoveryAndImportPreserveTreeToolsAndImages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := t.TempDir()
	projectDir := filepath.Join(root, "projects", strings.ReplaceAll(workspace, string(filepath.Separator), "-"))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "claude-session.jsonl")
	image := base64.StdEncoding.EncodeToString([]byte("png-data"))
	claudeBody := fmt.Sprintf(
		"{\"type\":\"user\",\"uuid\":\"u1\",\"parentUuid\":null,\"cwd\":%q,\"timestamp\":\"2026-01-01T00:00:00Z\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"inspect this\"},{\"type\":\"image\",\"source\":{\"media_type\":\"image/png\",\"data\":%q}}]}}\n"+
			"{\"type\":\"assistant\",\"uuid\":\"a1\",\"parentUuid\":\"u1\",\"timestamp\":\"2026-01-01T00:00:01Z\",\"message\":{\"id\":\"msg-1\",\"model\":\"claude-opus\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"inspect\",\"signature\":\"sig\"},{\"type\":\"text\",\"text\":\"reading\"},{\"type\":\"tool_use\",\"id\":\"call-1\",\"name\":\"Read\",\"input\":{\"path\":\"a.txt\"}}]}}\n"+
			"{\"type\":\"user\",\"uuid\":\"r1\",\"parentUuid\":\"a1\",\"timestamp\":\"2026-01-01T00:00:02Z\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"call-1\",\"content\":\"contents\",\"is_error\":false}]}}\n"+
			"{\"type\":\"user\",\"uuid\":\"u2\",\"parentUuid\":\"u1\",\"timestamp\":\"2026-01-01T00:00:03Z\",\"message\":{\"content\":\"take another path\"}}\n"+
			"{\"type\":\"custom-title\",\"customTitle\":\"Imported Claude tree\"}\n"+
			"{\"type\":\"assistant\",\"uuid\":\"side\",\"parentUuid\":\"u1\",\"isSidechain\":true,\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"hidden\"}]}}\n",
		workspace, image,
	)
	if err := os.WriteFile(path, []byte(claudeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	historyPath := filepath.Join(root, "history.jsonl")
	historyBody := fmt.Sprintf("{\"sessionId\":\"claude-session\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"display\":\"inspect this\",\"project\":%q}\n", workspace)
	if err := os.WriteFile(historyPath, []byte(historyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err := discoverClaude(ctx, root)
	if err != nil || len(infos) != 1 || infos[0].ID != "claude-session" || infos[0].FirstMessage != "inspect this" {
		t.Fatalf("Claude discovery=%#v error=%v", infos, err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	capture := &capturedAttachments{}
	importer := New(session.NewService(store.DB(), store.Blobs()), capture)
	if _, err := importer.Import(ctx, infos[0], "imported-claude", workspace); err != nil {
		t.Fatal(err)
	}
	service := importer.Sessions
	tree, err := service.LoadSessionTree(ctx, "imported-claude")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Entry.SourceID != "claude:u1:message" || len(tree.Roots[0].Children) != 2 || tree.ActiveLeafEntryID != tree.Roots[0].Children[1].Entry.ID {
		t.Fatalf("Claude tree=%#v", tree)
	}
	if len(capture.values) != 1 || string(capture.values[0]) != "png-data" {
		t.Fatalf("captured images=%q", capture.values)
	}
	toolLeaf := tree.Roots[0].Children[0].Children[0].Children[0].Entry.ID
	if _, err := service.NavigateSessionTree(ctx, "imported-claude", toolLeaf); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "imported-claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 4 || len(projection.Blocks[0].Attachments) != 1 || projection.Blocks[1].Kind != "model_change" || len(projection.Blocks[2].ImportedMessage) == 0 || len(projection.Blocks[3].ImportedMessage) == 0 {
		t.Fatalf("Claude projection=%#v", projection.Blocks)
	}
	var assistant message.Message
	if err := json.Unmarshal(projection.Blocks[2].ImportedMessage, &assistant); err != nil || len(assistant.ToolCalls) != 1 || assistant.ThinkingSignature != "sig" {
		t.Fatalf("Claude assistant=%#v error=%v", assistant, err)
	}
	var toolResult message.Message
	if err := json.Unmarshal(projection.Blocks[3].ImportedMessage, &toolResult); err != nil || toolResult.ToolResult == nil || toolResult.ToolResult.Name != "Read" || toolResult.ToolResult.Content != "contents" {
		t.Fatalf("Claude tool result=%#v error=%v", toolResult, err)
	}
	loaded, err := service.LoadSession(ctx, "imported-claude")
	if err != nil || loaded.Title != "Imported Claude tree" {
		t.Fatalf("Claude title=%q error=%v", loaded.Title, err)
	}
}

func TestCodexDiscoveryAndImportDeduplicatesEventsAndAppliesRollback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := t.TempDir()
	path := filepath.Join(root, "sessions", "2026", "rollout-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	records := []map[string]any{
		{"type": "session_meta", "timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"id": "thread-1", "cwd": workspace, "timestamp": "2026-01-01T00:00:00Z"}},
		{"type": "turn_context", "timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"model": "gpt-5-codex"}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:01Z", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "fix it"}}}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:02Z", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "I will read"}}}},
		{"type": "event_msg", "timestamp": "2026-01-01T00:00:02Z", "payload": map[string]any{"type": "agent_message", "message": "I will read"}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:03Z", "payload": map[string]any{"type": "function_call", "call_id": "call-1", "name": "read", "arguments": `{"path":"a.txt"}`}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:04Z", "payload": map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "contents"}},
		{"type": "event_msg", "timestamp": "2026-01-01T00:00:05Z", "payload": map[string]any{"type": "thread_name_updated", "thread_name": "Imported Codex thread"}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:06Z", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "discard me"}}}},
		{"type": "response_item", "timestamp": "2026-01-01T00:00:07Z", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "discarded"}}}},
		{"type": "event_msg", "timestamp": "2026-01-01T00:00:08Z", "payload": map[string]any{"type": "thread_rolled_back", "num_turns": 1}},
	}
	writeJSONLines(t, path, records)
	infos, err := discoverCodex(ctx, root)
	if err != nil || len(infos) != 1 || infos[0].ID != "thread-1" {
		t.Fatalf("Codex discovery=%#v error=%v", infos, err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	importer := New(session.NewService(store.DB(), store.Blobs()), nil)
	loaded, err := importer.Import(ctx, infos[0], "imported-codex", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Imported Codex thread" {
		t.Fatalf("Codex title=%q", loaded.Title)
	}
	projection, err := importer.Sessions.LoadProjection(ctx, "imported-codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 5 || projection.Blocks[0].Kind != "model_change" || projection.Blocks[1].Content != "fix it" || projection.Blocks[2].Content != "I will read" || projection.Blocks[4].Content != "contents" {
		t.Fatalf("Codex blocks=%#v", projection.Blocks)
	}
	var call message.Message
	if err := json.Unmarshal(projection.Blocks[3].ImportedMessage, &call); err != nil || len(call.ToolCalls) != 1 || call.ToolCalls[0].Name != "read" {
		t.Fatalf("Codex call=%#v error=%v", call, err)
	}
	for _, block := range projection.Blocks {
		if strings.Contains(block.Content, "discard") {
			t.Fatalf("rolled-back Codex block retained: %#v", block)
		}
	}
}

func TestCodexDiscoveryUsesNewestStateDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "thread.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLines(t, rollout, []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": "thread-db", "cwd": t.TempDir()}}})
	db, err := sql.Open("sqlite", filepath.Join(root, "state_7.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads(id TEXT,rollout_path TEXT,created_at REAL,updated_at REAL,cwd TEXT,title TEXT,first_user_message TEXT); INSERT INTO threads VALUES('thread-db','sessions/thread.jsonl',100,200,'/tmp/project','From DB','hello')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	infos, err := discoverCodex(ctx, root)
	if err != nil || len(infos) != 1 || infos[0].Title != "From DB" || infos[0].Path != rollout {
		t.Fatalf("Codex DB discovery=%#v error=%v", infos, err)
	}
}

func TestForeignJSONLRejectsDuplicateKeysAndSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","type":"assistant"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readJSONL(context.Background(), path); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("duplicate JSON error=%v", err)
	}
	link := filepath.Join(t.TempDir(), "linked.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readJSONL(context.Background(), link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error=%v", err)
	}
}

func writeJSONLines(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	var body strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
