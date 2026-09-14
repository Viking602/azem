package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

// Each stage removes one source of work from the same long conversation.
func BenchmarkEvidenceProjectionAblation(b *testing.B) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(b.TempDir(), "evidence.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB(), store.Blobs())
	if _, err := svc.Ensure(ctx, Session{ID: "s", Title: "Evidence benchmark"}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		kind := "assistant"
		if i%10 == 0 {
			kind = "user"
		}
		if _, err := svc.AppendBlock(ctx, "s", Block{Kind: kind, Content: strings.Repeat("bounded evidence ", 4096)}); err != nil {
			b.Fatal(err)
		}
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		run := "old"
		if i >= 900 {
			run = "current"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO session_tool_records(session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at,content_sha256,structured_sha256) VALUES('s',?, ?,0,'read_file','{}','completed',?,'null','','[]',?,0,'','')`, run, fmt.Sprint(i), strings.Repeat("source line\n", 1024), i)
		if err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, stage := range []struct {
		name string
		opts loadProjectionOptions
	}{
		{"AllTranscript", loadProjectionOptions{SkipModelHistory: true}},
		{"UserBlocksOnly", loadProjectionOptions{SkipModelHistory: true, UserBlocksOnly: true}},
		{"CurrentRunOnly", loadProjectionOptions{SkipModelHistory: true, UserBlocksOnly: true, ToolRunIDs: []string{"current"}}},
	} {
		b.Run(stage.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p, err := svc.loadProjection(ctx, "s", stage.opts)
				if err != nil {
					b.Fatal(err)
				}
				if len(p.ToolRecords) == 0 {
					b.Fatal("missing tools")
				}
			}
		})
	}
}

func TestToolEvidenceRunFilterPreservesOrderAndScope(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "filter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB(), store.Blobs())
	for _, id := range []string{"s", "other"} {
		if _, err := svc.Ensure(ctx, Session{ID: id}); err != nil {
			t.Fatal(err)
		}
		for _, run := range []string{"old", "a", "b"} {
			if _, err := svc.StartToolRecord(ctx, id, ToolRecord{RunID: run, ToolCallID: run, Name: "read_file"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, test := range []struct {
		ids  []string
		want string
	}{
		{nil, "old,a,b"}, {[]string{}, ""}, {[]string{"b", "a", "a"}, "a,b"}, {[]string{"missing"}, ""}, {[]string{"a' OR 1=1 --"}, ""},
	} {
		records, err := svc.listToolRecordsForRuns(ctx, "s", test.ids)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range records {
			if r.SessionID != "s" {
				t.Fatal("cross-session record")
			}
			got = append(got, r.RunID)
		}
		if strings.Join(got, ",") != test.want {
			t.Fatalf("ids=%v got=%v want=%s", test.ids, got, test.want)
		}
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE session_tool_records SET observations='invalid' WHERE session_id='s' AND run_id='old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.listToolRecordsForRuns(ctx, "s", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.listToolRecordsForRuns(ctx, "s", []string{"old"}); err == nil {
		t.Fatal("selected corrupt evidence must fail")
	}
}
