package sessionexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestHTMLTextAndJSONExportsRespectActiveBranchAndEscapeContent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := session.NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, session.Session{ID: "session", Title: "Export <demo>"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []session.Block{
		{Kind: "user", Content: `<script>alert("x")</script> question`},
		{Kind: "assistant", Content: "first answer"},
		{Kind: "user", Content: "old branch question"},
		{Kind: "assistant", Content: "old branch answer"},
	} {
		if _, err := service.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	boundary := entryID("session", 1)
	if err := service.SetSessionEntryLabel(ctx, "session", boundary, "Checkpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.NavigateSessionTree(ctx, "session", boundary); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "active branch question"}); err != nil {
		t.Fatal(err)
	}
	started, err := service.StartToolRecordAt(ctx, "session", session.ToolRecord{
		RunID: "run", ToolCallID: "tool-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`), StartedAt: time.Unix(10, 0),
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	started.State = session.ToolCompleted
	started.Content = "file contents"
	started.CompletedAt = time.Unix(11, 0)
	if _, err := service.FinishToolRecord(ctx, "session", started); err != nil {
		t.Fatal(err)
	}
	exporter := New(service)
	exporter.Now = func() time.Time { return time.Unix(20, 0).UTC() }
	var html bytes.Buffer
	if err := exporter.Write(ctx, &html, "session", FormatHTML, Options{}); err != nil {
		t.Fatal(err)
	}
	htmlText := html.String()
	for _, required := range []string{"<!doctype html>", "Content-Security-Policy", "active branch question", "Checkpoint", "file contents", "&lt;script&gt;"} {
		if !strings.Contains(htmlText, required) {
			t.Fatalf("HTML missing %q:\n%s", required, htmlText)
		}
	}
	if strings.Contains(htmlText, "old branch question") || strings.Contains(htmlText, `<script>alert`) {
		t.Fatalf("HTML leaked inactive or executable content:\n%s", htmlText)
	}
	var text bytes.Buffer
	if err := exporter.Write(ctx, &text, "session", FormatText, Options{ExcludeTools: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "active branch question") || !strings.Contains(text.String(), "Checkpoint") || strings.Contains(text.String(), "old branch question") || strings.Contains(text.String(), "file contents") {
		t.Fatalf("text export:\n%s", text.String())
	}
	var allHTML bytes.Buffer
	if err := exporter.Write(ctx, &allHTML, "session", FormatHTML, Options{AllBranches: true, ExcludeTools: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(allHTML.String(), "old branch question") {
		t.Fatalf("all-branch HTML omitted abandoned branch:\n%s", allHTML.String())
	}
	var jsonOutput bytes.Buffer
	if err := exporter.Write(ctx, &jsonOutput, "session", FormatJSON, Options{}); err != nil {
		t.Fatal(err)
	}
	var document Document
	if err := json.Unmarshal(jsonOutput.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || !document.ExportedAt.Equal(time.Unix(20, 0).UTC()) || len(document.Snapshot.ActiveBlocks) != 3 || len(document.Snapshot.AllBlocks) != 5 || len(document.Snapshot.ToolRecords) != 1 {
		t.Fatalf("JSON document=%#v", document)
	}
}

func TestExportFileIsAtomicPrivateAndRejectsSymlinkTargets(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := session.NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, session.Session{ID: "session", Title: "Export"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	exporter := New(service)
	output := filepath.Join(t.TempDir(), "session.txt")
	resolved, err := exporter.ExportFile(ctx, output, "session", FormatText, Options{})
	if err != nil || resolved != output {
		t.Fatalf("export path=%q error=%v", resolved, err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode=%#o", info.Mode().Perm())
	}
	if body, err := os.ReadFile(output); err != nil || !strings.Contains(string(body), "hello") {
		t.Fatalf("export body=%q error=%v", body, err)
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.ExportFile(ctx, link, "session", FormatText, Options{}); err == nil {
		t.Fatal("exported through symlink target")
	}
	if body, _ := os.ReadFile(target); string(body) != "keep" {
		t.Fatalf("symlink target changed=%q", body)
	}
}

func entryID(sessionID string, sequence int64) string {
	return fmt.Sprintf("%s:%020d", sessionID, sequence)
}
