package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCurrentSchemaCreatesAndReopensSecurityCatalog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "azem.db")
	provider, err := Open(ctx, path, WithBlobRoot(filepath.Join(t.TempDir(), "blobs")))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := provider.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion || schemaVersion != len(migrations) {
		t.Fatalf("schema version=%d compiled=%d migrations=%d", version, schemaVersion, len(migrations))
	}
	for _, table := range []string{
		"security_scans", "security_scan_workers", "security_scan_progress", "security_scan_artifacts",
		"security_findings", "security_finding_occurrences", "security_finding_locations", "security_finding_triage",
		"security_remediation_attempts", "security_scan_matches", "security_publications",
	} {
		var count int
		if err := provider.DB().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d error=%v", table, count, err)
		}
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path, WithBlobRoot(filepath.Join(t.TempDir(), "blobs-reopen")))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	if err := reopened.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("reopened version=%d error=%v", version, err)
	}
}
