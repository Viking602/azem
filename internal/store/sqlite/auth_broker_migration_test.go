package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationV25AddsDurableAuthBrokerStateAndReopens(t *testing.T) {
	if len(migrations) != schemaVersion || schemaVersion < 25 {
		t.Fatalf("migration count=%d schema=%d", len(migrations), schemaVersion)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "broker.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 24; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply fixture migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=24`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"auth_broker_state", "auth_broker_disabled", "auth_broker_blocks", "auth_broker_usage_observations", "auth_broker_credentials_ai"} {
		var found string
		if err := provider.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name=?`, object).Scan(&found); err != nil {
			t.Fatalf("missing broker object %s: %v", object, err)
		}
	}
	if _, err := provider.db.ExecContext(ctx, `INSERT INTO auth_credentials(provider_id,account_id,data,created_at,updated_at) VALUES('chatgpt','account','{}',1,2)`); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := provider.db.QueryRowContext(ctx, `SELECT generation FROM auth_broker_state WHERE id=1`).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("generation=%d error=%v", generation, err)
	}
	if _, err := provider.db.ExecContext(ctx, `
		INSERT INTO auth_broker_blocks(credential_id,provider_id,scope,blocked_until,reason,updated_at) VALUES('credential','chatgpt','chat',10,'limit',3);
		INSERT INTO auth_broker_usage_observations(id,client_id,credential_id,provider_id,account_id,payload,observed_at) VALUES('usage','client','credential','chatgpt','account','{}',4);
	`); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var blocks, usage int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_broker_blocks`).Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_broker_usage_observations`).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if blocks != 1 || usage != 1 {
		t.Fatalf("retained broker state blocks=%d usage=%d", blocks, usage)
	}
}
