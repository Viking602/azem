package authbroker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth"
)

type RemoteStore struct {
	Client *Client
	DB     *sql.DB
}

func (store *RemoteStore) Sync(ctx context.Context) (Snapshot, error) {
	if store == nil || store.Client == nil || store.DB == nil {
		return Snapshot{}, errors.New("remote credential store is unavailable")
	}
	snapshot, err := store.Client.FetchSnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	visible := make(map[string]struct{}, len(snapshot.Credentials))
	now := time.Now().UTC().UnixNano()
	for _, credential := range snapshot.Credentials {
		key := credential.Provider + "\x00" + credential.AccountID
		visible[key] = struct{}{}
		createdAt := now
		_ = tx.QueryRowContext(ctx, `SELECT created_at FROM accounts WHERE provider_id=? AND id=?`, credential.Provider, credential.AccountID).Scan(&createdAt)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(provider_id,id) DO UPDATE SET email=excluded.email,display_name=excluded.display_name,
				plan=excluded.plan,credential_ref=excluded.credential_ref,status=excluded.status,updated_at=excluded.updated_at
		`, credential.AccountID, credential.Provider, credential.Email, credential.DisplayName, credential.Plan, "broker:"+credential.ID, credential.Status, createdAt, now)
		if err != nil {
			return Snapshot{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT provider_id,id FROM accounts WHERE credential_ref LIKE 'broker:%'`)
	if err != nil {
		return Snapshot{}, err
	}
	var stale [][2]string
	for rows.Next() {
		var provider, account string
		if rows.Scan(&provider, &account) == nil {
			if _, ok := visible[provider+"\x00"+account]; !ok {
				stale = append(stale, [2]string{provider, account})
			}
		}
	}
	rows.Close()
	for _, account := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE provider_id=? AND id=?`, account[0], account[1]); err != nil {
			return Snapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (store *RemoteStore) Put(context.Context, auth.Credential) (string, error) {
	return "", errors.New("broker-backed credentials are read-only; write credentials on the broker host")
}

func (store *RemoteStore) Get(ctx context.Context, provider, accountID string) (auth.Credential, error) {
	if store == nil || store.Client == nil {
		return auth.Credential{}, errors.New("remote credential store is unavailable")
	}
	credential, err := store.Client.Credential(ctx, provider, accountID)
	if err != nil {
		return auth.Credential{}, err
	}
	return auth.Credential{
		Provider: credential.Provider, AccountID: credential.AccountID, AccessToken: credential.AccessToken,
		TokenType: credential.TokenType, ExpiresAt: credential.ExpiresAt, Email: credential.Email,
		DisplayName: credential.DisplayName, Plan: credential.Plan,
	}, nil
}

func (store *RemoteStore) RunSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = store.Sync(ctx)
		}
	}
}

func (store *RemoteStore) Delete(context.Context, string, string) error {
	return errors.New("broker-backed credentials are read-only; remove credentials on the broker host")
}

func LoadAccountPool(path string) (map[string][]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pool map[string][]string
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pool); err != nil {
		return nil, fmt.Errorf("decode broker account pool: %w", err)
	}
	for provider, identities := range pool {
		if strings.TrimSpace(provider) == "" {
			return nil, errors.New("broker account pool contains an empty provider")
		}
		for _, identity := range identities {
			if strings.TrimSpace(identity) == "" {
				return nil, fmt.Errorf("broker account pool %s contains an empty identity", provider)
			}
		}
	}
	return pool, nil
}

var _ auth.CredentialStore = (*RemoteStore)(nil)
