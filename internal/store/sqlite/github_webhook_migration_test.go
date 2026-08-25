package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationV26AddsWebhookReceiptsAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "webhooks.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 25; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply fixture migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=25`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.db.ExecContext(ctx, `INSERT INTO github_webhook_deliveries(delivery_id,event_name,repository,pull_request_number,action,payload_sha256,status,received_at) VALUES('delivery','check_run','owner/repo',7,'completed','digest','queued',1)`); err != nil {
		t.Fatal(err)
	}
	provider.Close(ctx)
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var status string
	if err := reopened.db.QueryRowContext(ctx, `SELECT status FROM github_webhook_deliveries WHERE delivery_id='delivery'`).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("receipt status=%q error=%v", status, err)
	}
}
