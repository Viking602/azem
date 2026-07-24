package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
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
	err = dbgen.New(s.db).UpsertCredential(ctx, dbgen.UpsertCredentialParams{
		ProviderID: value.Provider, AccountID: value.AccountID, Data: string(data), CreatedAt: now, UpdatedAt: now,
	})
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
	data, err := dbgen.New(s.db).GetCredential(ctx, dbgen.GetCredentialParams{ProviderID: provider, AccountID: accountID})
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
	if err := dbgen.New(s.db).DeleteCredential(ctx, dbgen.DeleteCredentialParams{ProviderID: provider, AccountID: accountID}); err != nil {
		return fmt.Errorf("delete credential from sqlite: %w", err)
	}
	return nil
}

var _ CredentialStore = (*SQLiteStore)(nil)
