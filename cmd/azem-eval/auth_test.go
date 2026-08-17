package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestShouldReplaceAuthPrefersNewerExpiryAndRotatedRefresh(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	older := mustAuthBlob(t, "old-access", "old-refresh", now.Add(-time.Hour))
	newer := mustAuthBlob(t, "new-access", "new-refresh", now.Add(time.Hour))
	if !shouldReplaceAuth(newer, older) {
		t.Fatal("expected newer expiry to replace")
	}
	if shouldReplaceAuth(older, newer) {
		t.Fatal("older expiry replaced a live credential")
	}
	rotated := mustAuthBlob(t, "new-access", "rotated", now.Add(time.Hour))
	if !shouldReplaceAuth(rotated, newer) {
		t.Fatal("expected rotated refresh token with equal expiry to replace")
	}
	same := mustAuthBlob(t, "new-access", "new-refresh", now.Add(time.Hour))
	if shouldReplaceAuth(same, newer) {
		t.Fatal("identical refresh token replaced dest")
	}
	if shouldReplaceAuth([]byte(`{}`), newer) {
		t.Fatal("empty source replaced dest")
	}
}

func TestSyncAuthCopiesNewerGrokCredentialWithoutOpeningDestAsRuntimeStore(t *testing.T) {
	ctx := t.Context()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	src, err := sqlitestore.Open(ctx, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'eval@example.com', 'Eval', 'SuperGrokPro', 'sqlite', 'active', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'eval@example.com', 'Eval', 'SuperGrokPro', 'sqlite', 'active', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	oldBlob := mustAuthBlob(t, "old", "old-refresh", now.Add(-time.Hour))
	newBlob := mustAuthBlob(t, "fresh", "rotated-refresh", now.Add(time.Hour))
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 1)`, oldBlob); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 3)`, newBlob); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syncAuth(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var data string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE account_id = 'acct'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "fresh") || !strings.Contains(data, "rotated-refresh") {
		t.Fatalf("dest credential = %s", data)
	}
}

func TestSyncAuthInsertsMissingDestinationAccount(t *testing.T) {
	ctx := t.Context()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	src, err := sqlitestore.Open(ctx, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'eval@example.com', 'Eval', 'SuperGrokPro', 'sqlite', 'active', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 3)`,
		mustAuthBlob(t, "fresh", "rotated-refresh", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syncAuth(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var email, status string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT email, status FROM accounts WHERE id = 'acct'`).Scan(&email, &status); err != nil {
		t.Fatal(err)
	}
	if email != "eval@example.com" || status != "active" {
		t.Fatalf("dest account email=%q status=%q", email, status)
	}
}

func TestSyncAuthKeepsDestinationAccountWhenSourceAccountIsMissing(t *testing.T) {
	ctx := t.Context()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	src, err := sqlitestore.Open(ctx, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'keep@example.com', 'Keep', 'SuperGrokPro', 'sqlite', 'active', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 1)`,
		mustAuthBlob(t, "old", "old-refresh", now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 3)`,
		mustAuthBlob(t, "fresh", "rotated-refresh", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syncAuth(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var email, status string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT email, status FROM accounts WHERE id = 'acct'`).Scan(&email, &status); err != nil {
		t.Fatal(err)
	}
	if email != "keep@example.com" || status != "active" {
		t.Fatalf("dest account overwritten email=%q status=%q", email, status)
	}
}

func TestSyncAuthSkipsReauthRequiredSource(t *testing.T) {
	ctx := t.Context()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	src, err := sqlitestore.Open(ctx, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'eval@example.com', 'Eval', 'SuperGrokPro', 'sqlite', 'reauth_required', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'grok', 'eval@example.com', 'Eval', 'SuperGrokPro', 'sqlite', 'active', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 3)`,
		mustAuthBlob(t, "stale", "revoked-refresh", now.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES('grok', 'acct', ?, 1, 1)`,
		mustAuthBlob(t, "live", "live-refresh", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syncAuth(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var data, status string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE account_id = 'acct'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DB().QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = 'acct'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "live-refresh") || strings.Contains(data, "revoked-refresh") || status != "active" {
		t.Fatalf("reauth source overwrote dest data=%s status=%s", data, status)
	}
}

func mustAuthBlob(t *testing.T, access, refresh string, expires time.Time) []byte {
	t.Helper()
	data, err := json.Marshal(authCredentialBlob{AccessToken: access, RefreshToken: refresh, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
