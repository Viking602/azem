package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrCredentialNotFound = errors.New("credential not found")

type Credential struct {
	Provider      string    `json:"provider"`
	AccountID     string    `json:"accountId"`
	AccessToken   string    `json:"accessToken"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	IDToken       string    `json:"idToken,omitempty"`
	TokenType     string    `json:"tokenType,omitempty"`
	OAuthClientID string    `json:"oauthClientId,omitempty"`
	SourcePath    string    `json:"sourcePath,omitempty"`
	SourceKey     string    `json:"sourceKey,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`
	Email         string    `json:"email,omitempty"`
	DisplayName   string    `json:"displayName,omitempty"`
	Plan          string    `json:"plan,omitempty"`
}

type CredentialStore interface {
	Put(context.Context, Credential) (string, error)
	Get(ctx context.Context, provider string, accountID string) (Credential, error)
	Delete(ctx context.Context, provider string, accountID string) error
}

func credentialKey(provider string, accountID string) string { return provider + ":" + accountID }

// RoutedStore keeps each existing account bound to the credential store named
// by accounts.credential_ref. The configured default only applies to accounts
// that do not exist yet.
type RoutedStore struct {
	db          *sql.DB
	defaultName string
	stores      map[string]CredentialStore
}

func NewRoutedStore(db *sql.DB, defaultName string, stores map[string]CredentialStore) (*RoutedStore, error) {
	if db == nil {
		return nil, fmt.Errorf("credential routing database is unavailable")
	}
	if defaultName == "" {
		return nil, fmt.Errorf("default credential store is empty")
	}
	if stores[defaultName] == nil {
		return nil, fmt.Errorf("default credential store %q is unavailable", defaultName)
	}
	copied := make(map[string]CredentialStore, len(stores))
	for name, store := range stores {
		if name != "" && store != nil {
			copied[name] = store
		}
	}
	return &RoutedStore{db: db, defaultName: defaultName, stores: copied}, nil
}

func (s *RoutedStore) Put(ctx context.Context, value Credential) (string, error) {
	name, store, err := s.storeFor(ctx, value.Provider, value.AccountID)
	if err != nil {
		return "", err
	}
	reference, err := store.Put(ctx, value)
	if err != nil {
		return "", err
	}
	if referenceStoreName(reference) != name {
		return "", fmt.Errorf("credential store %q returned mismatched reference %q", name, reference)
	}
	return reference, nil
}

func (s *RoutedStore) Get(ctx context.Context, provider string, accountID string) (Credential, error) {
	_, store, err := s.storeFor(ctx, provider, accountID)
	if err != nil {
		return Credential{}, err
	}
	return store.Get(ctx, provider, accountID)
}

func (s *RoutedStore) Delete(ctx context.Context, provider string, accountID string) error {
	_, store, err := s.storeFor(ctx, provider, accountID)
	if err != nil {
		return err
	}
	return store.Delete(ctx, provider, accountID)
}

func (s *RoutedStore) storeFor(ctx context.Context, provider string, accountID string) (string, CredentialStore, error) {
	name := s.defaultName
	var reference string
	err := s.db.QueryRowContext(ctx, `SELECT credential_ref FROM accounts WHERE provider_id=? AND id=?`, provider, accountID).Scan(&reference)
	switch {
	case err == nil:
		name = referenceStoreName(reference)
		if name == "" {
			return "", nil, fmt.Errorf("account %s/%s has invalid credential reference %q", provider, accountID, reference)
		}
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", nil, fmt.Errorf("resolve credential reference for %s/%s: %w", provider, accountID, err)
	}
	store := s.stores[name]
	if store == nil {
		return "", nil, fmt.Errorf("credential store %q referenced by %s/%s is unavailable", name, provider, accountID)
	}
	return name, store, nil
}

func referenceStoreName(reference string) string {
	name, _, found := strings.Cut(reference, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(name)
}

var _ CredentialStore = (*RoutedStore)(nil)
