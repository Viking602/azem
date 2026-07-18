package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SQLiteStore persists OAuth credentials in Azem's protected application
// database. OAuth token bundles can exceed platform keychain item limits.
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Put(ctx context.Context, value Credential) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("credential database is unavailable")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode credential: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_credentials(provider_id,account_id,data,created_at,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(provider_id,account_id) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at`,
		value.Provider, value.AccountID, string(data), now, now)
	if err != nil {
		return "", fmt.Errorf("store credential in sqlite: %w", err)
	}
	return "sqlite:" + credentialKey(value.Provider, value.AccountID), nil
}

func (s *SQLiteStore) Get(ctx context.Context, provider string, accountID string) (Credential, error) {
	var value Credential
	if s == nil || s.db == nil {
		return value, fmt.Errorf("credential database is unavailable")
	}
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE provider_id=? AND account_id=?`, provider, accountID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrCredentialNotFound
	}
	if err != nil {
		return value, fmt.Errorf("load credential from sqlite: %w", err)
	}
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return Credential{}, fmt.Errorf("decode sqlite credential: %w", err)
	}
	if value.Provider != provider || value.AccountID != accountID {
		return Credential{}, fmt.Errorf("sqlite credential identity mismatch")
	}
	return value, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, provider string, accountID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credential database is unavailable")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_credentials WHERE provider_id=? AND account_id=?`, provider, accountID); err != nil {
		return fmt.Errorf("delete credential from sqlite: %w", err)
	}
	return nil
}

var _ CredentialStore = (*SQLiteStore)(nil)
