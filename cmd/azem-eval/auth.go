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
		err := dest.QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE provider_id = ? AND account_id = ?`, providerID, accountID).Scan(&destData)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && !shouldReplaceAuth(data, destData) {
			continue
		}
		if _, err := dest.ExecContext(ctx, `INSERT OR REPLACE INTO auth_credentials(provider_id, account_id, data, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
			providerID, accountID, data, createdAt, now); err != nil {
			return fmt.Errorf("write %s/%s credential: %w", providerID, accountID, err)
		}
		var email, displayName, plan, status string
		if err := src.QueryRowContext(ctx, `SELECT email, display_name, plan, status FROM accounts WHERE provider_id = ? AND id = ?`, providerID, accountID).Scan(&email, &displayName, &plan, &status); err != nil && err != sql.ErrNoRows {
			return err
		}
		if status != "active" {
			status = "active"
		}
		if _, err := dest.ExecContext(ctx, `UPDATE accounts SET email = ?, display_name = ?, plan = ?, status = ?, updated_at = ? WHERE provider_id = ? AND id = ?`,
			email, displayName, plan, status, now, providerID, accountID); err != nil {
			return fmt.Errorf("update %s/%s account: %w", providerID, accountID, err)
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "azem-eval: synced %d credential(s) into %s\n", copied, to)
	return nil
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
