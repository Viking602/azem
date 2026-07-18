package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const keyringService = "azem"

type KeyringStore struct{}

func NewKeyringStore() *KeyringStore { return &KeyringStore{} }

// LookupKeyringSecret resolves a generic config secret stored under the Azem
// service name and the supplied keyring account.
func LookupKeyringSecret(name string) (string, error) {
	value, err := keyring.Get(keyringService, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load keyring secret: %w", err)
	}
	return value, nil
}

func (s *KeyringStore) Put(_ context.Context, value Credential) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	key := credentialKey(value.Provider, value.AccountID)
	if err := keyring.Set(keyringService, key, string(data)); err != nil {
		return "", fmt.Errorf("store credential in keyring: %w", err)
	}
	return "keyring:" + key, nil
}

func (s *KeyringStore) Get(_ context.Context, provider string, accountID string) (Credential, error) {
	var value Credential
	secret, err := keyring.Get(keyringService, credentialKey(provider, accountID))
	if errors.Is(err, keyring.ErrNotFound) {
		return value, ErrCredentialNotFound
	}
	if err != nil {
		return value, fmt.Errorf("load credential from keyring: %w", err)
	}
	if err := json.Unmarshal([]byte(secret), &value); err != nil {
		return value, fmt.Errorf("decode keyring credential: %w", err)
	}
	return value, nil
}

func (s *KeyringStore) Delete(_ context.Context, provider string, accountID string) error {
	err := keyring.Delete(keyringService, credentialKey(provider, accountID))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete keyring credential: %w", err)
	}
	return nil
}

var _ CredentialStore = (*KeyringStore)(nil)
