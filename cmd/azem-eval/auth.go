package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

type authCredentialBlob struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func refreshAuth(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		return err
	}
	defer store.Close(ctx)
	credentials := authservice.NewSQLiteStore(store.DB())
	svc := authservice.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	var firstErr error
	refreshed := 0
	for _, provider := range []string{"grok", "chatgpt"} {
		accounts, err := svc.Accounts(ctx, provider)
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if account.Status != "active" {
				continue
			}
			credential, err := credentials.Get(ctx, provider, account.ID)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s/%s: %w", provider, account.ID, err)
				}
				fmt.Fprintf(os.Stderr, "azem-eval: refresh %s/%s: %v\n", provider, account.ID, err)
				continue
			}
			if credential.RefreshToken == "" {
				continue
			}
			if _, err := svc.Refresh(ctx, provider, account.ID); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s/%s: %w", provider, account.ID, err)
				}
				fmt.Fprintf(os.Stderr, "azem-eval: refresh %s/%s: %v\n", provider, account.ID, err)
				continue
			}
			refreshed++
			fmt.Fprintf(os.Stderr, "azem-eval: refreshed %s/%s\n", provider, account.ID)
		}
	}
	if refreshed == 0 {
		if firstErr != nil {
			return firstErr
		}
		return fmt.Errorf("no active Grok or ChatGPT refresh token could be renewed")
	}
	return nil
}

func syncAuth(from, to string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	src, err := openRawSQLite(from, true)
	if err != nil {
		return fmt.Errorf("open sync source: %w", err)
	}
	defer src.Close()
	dest, err := openRawSQLite(to, false)
	if err != nil {
		return fmt.Errorf("open sync dest: %w", err)
	}
	defer dest.Close()
	rows, err := src.QueryContext(ctx, `SELECT provider_id, account_id, data, created_at, updated_at FROM auth_credentials WHERE provider_id IN ('grok', 'chatgpt')`)
	if err != nil {
		return fmt.Errorf("read source credentials: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC().UnixNano()
	copied := 0
	for rows.Next() {
		var providerID, accountID string
		var data []byte
		var createdAt, updatedAt int64
		if err := rows.Scan(&providerID, &accountID, &data, &createdAt, &updatedAt); err != nil {
			return err
		}
		var destData []byte
		destErr := dest.QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE provider_id = ? AND account_id = ?`, providerID, accountID).Scan(&destData)
		if destErr != nil && destErr != sql.ErrNoRows {
			return destErr
		}
		srcAccount, err := loadSyncAccount(ctx, src, providerID, accountID)
		if err != nil {
			return err
		}
		destAccount, err := loadSyncAccount(ctx, dest, providerID, accountID)
		if err != nil {
			return err
		}
		if srcAccount.found && srcAccount.status != "active" {
			continue
		}
		if destErr == nil && !shouldReplaceAuth(data, destData) {
			continue
		}
		if _, err := dest.ExecContext(ctx, `INSERT OR REPLACE INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
			providerID, accountID, data, createdAt, now); err != nil {
			return fmt.Errorf("write %s/%s credential: %w", providerID, accountID, err)
		}
		if err := upsertSyncAccount(ctx, dest, providerID, accountID, srcAccount, destAccount, now); err != nil {
			return fmt.Errorf("upsert %s/%s account: %w", providerID, accountID, err)
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "azem-eval: synced %d credential(s) into %s\n", copied, to)
	return nil
}

type syncAccount struct {
	email, displayName, plan, status string
	found                            bool
}

func loadSyncAccount(ctx context.Context, db *sql.DB, providerID, accountID string) (syncAccount, error) {
	var account syncAccount
	err := db.QueryRowContext(ctx, `SELECT email, display_name, plan, status FROM accounts WHERE provider_id = ? AND id = ?`, providerID, accountID).
		Scan(&account.email, &account.displayName, &account.plan, &account.status)
	if err == sql.ErrNoRows {
		return account, nil
	}
	if err != nil {
		return account, err
	}
	account.found = true
	return account, nil
}

func upsertSyncAccount(ctx context.Context, dest *sql.DB, providerID, accountID string, src, existing syncAccount, now int64) error {
	if !src.found {
		if existing.found {
			return nil
		}
		_, err := dest.ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
			VALUES(?, ?, '', '', '', 'sqlite', 'active', ?, ?)`, accountID, providerID, now, now)
		return err
	}
	status := src.status
	if status == "" {
		status = "active"
	}
	_, err := dest.ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, 'sqlite', ?, ?, ?)
		ON CONFLICT(provider_id, id) DO UPDATE SET
			email = excluded.email,
			display_name = excluded.display_name,
			plan = excluded.plan,
			status = CASE WHEN excluded.status = 'active' THEN 'active' ELSE accounts.status END,
			updated_at = excluded.updated_at`,
		accountID, providerID, src.email, src.displayName, src.plan, status, now, now)
	return err
}

func shouldReplaceAuth(srcData, destData []byte) bool {
	src := decodeAuthBlob(srcData)
	if src.AccessToken == "" && src.RefreshToken == "" {
		return false
	}
	if len(destData) == 0 {
		return true
	}
	dest := decodeAuthBlob(destData)
	if !src.ExpiresAt.IsZero() && src.ExpiresAt.After(dest.ExpiresAt) {
		return true
	}
	if src.RefreshToken != "" && src.RefreshToken != dest.RefreshToken && !src.ExpiresAt.Before(dest.ExpiresAt) {
		return true
	}
	return false
}

func decodeAuthBlob(data []byte) authCredentialBlob {
	var blob authCredentialBlob
	_ = json.Unmarshal(data, &blob)
	return blob
}

func openRawSQLite(path string, readOnly bool) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path)
	if readOnly {
		dsn += "?mode=ro"
	} else {
		dsn += "?_pragma=busy_timeout(8000)"
	}
	return sql.Open("sqlite", dsn)
}
