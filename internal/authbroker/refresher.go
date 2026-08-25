package authbroker

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth"
)

type RefresherOptions struct {
	DB       *sql.DB
	Auth     *auth.Service
	Interval time.Duration
	Skew     time.Duration
	Refresh  func(context.Context, string, string) (auth.Credential, error)
	Now      func() time.Time
}

type Refresher struct {
	options RefresherOptions
}

func NewRefresher(options RefresherOptions) (*Refresher, error) {
	if options.DB == nil || options.Auth == nil {
		return nil, errors.New("auth broker refresher requires database and auth service")
	}
	if options.Interval <= 0 {
		options.Interval = time.Minute
	}
	if options.Skew <= 0 {
		options.Skew = 5 * time.Minute
	}
	if options.Refresh == nil {
		options.Refresh = options.Auth.Refresh
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Refresher{options: options}, nil
}

func (refresher *Refresher) Run(ctx context.Context) error {
	_ = refresher.Sweep(ctx)
	ticker := time.NewTicker(refresher.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = refresher.Sweep(ctx)
		}
	}
}

func (refresher *Refresher) Sweep(ctx context.Context) error {
	accounts, err := refresher.options.Auth.Accounts(ctx, "")
	if err != nil {
		return err
	}
	now := refresher.options.Now()
	for _, account := range accounts {
		if account.Status != "active" {
			continue
		}
		credential, err := refresher.options.Auth.StoredCredential(ctx, account.Provider, account.ID)
		if err != nil || credential.RefreshToken == "" || credential.ExpiresAt.IsZero() || credential.ExpiresAt.Sub(now) >= refresher.options.Skew {
			continue
		}
		if _, err := refresher.options.Refresh(ctx, account.Provider, account.ID); err != nil && definitiveRefreshFailure(err) {
			refresher.disable(ctx, account, err.Error(), now)
		}
	}
	return nil
}

func (refresher *Refresher) disable(ctx context.Context, account auth.Account, cause string, now time.Time) {
	_, _ = refresher.options.DB.ExecContext(ctx, `UPDATE accounts SET status='disabled',updated_at=? WHERE provider_id=? AND id=?`, now.UnixNano(), account.Provider, account.ID)
	_, _ = refresher.options.DB.ExecContext(ctx, `INSERT INTO auth_broker_disabled(credential_id,provider_id,account_id,cause,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(credential_id) DO UPDATE SET cause=excluded.cause,updated_at=excluded.updated_at`, credentialID(account.Provider, account.ID), account.Provider, account.ID, cause, now.UnixNano())
	_, _ = refresher.options.DB.ExecContext(ctx, `UPDATE auth_broker_state SET generation=generation+1,updated_at=? WHERE id=1`, now.UnixNano())
}

func definitiveRefreshFailure(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	for _, marker := range []string{"invalid_grant", "invalid token", "invalid_token", "revoked", "unauthorized", "status 401", "status 403", "http 401", "http 403"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
