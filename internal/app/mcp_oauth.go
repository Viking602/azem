package app

import (
	"context"
	"errors"

	authservice "github.com/Viking602/azem/internal/auth"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
)

type mcpOAuthStore struct {
	auth *authservice.Service
}

func (store mcpOAuthStore) Get(ctx context.Context, id string) (mcpruntime.OAuthCredential, bool, error) {
	if store.auth == nil {
		return mcpruntime.OAuthCredential{}, false, errors.New("credential service is unavailable")
	}
	credential, err := store.auth.StoredCredential(ctx, "mcp", id)
	if errors.Is(err, authservice.ErrCredentialNotFound) {
		return mcpruntime.OAuthCredential{}, false, nil
	}
	if err != nil {
		return mcpruntime.OAuthCredential{}, false, err
	}
	return mcpruntime.OAuthCredential{
		AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken,
		TokenType: credential.TokenType, ClientID: credential.OAuthClientID,
		ExpiresAt: credential.ExpiresAt, TokenURL: credential.SourcePath,
	}, true, nil
}

func (store mcpOAuthStore) Put(ctx context.Context, id string, credential mcpruntime.OAuthCredential) error {
	if store.auth == nil {
		return errors.New("credential service is unavailable")
	}
	_, err := store.auth.PutCredential(ctx, authservice.Credential{
		Provider: "mcp", AccountID: id, AccessToken: credential.AccessToken,
		RefreshToken: credential.RefreshToken, TokenType: credential.TokenType,
		OAuthClientID: credential.ClientID, ExpiresAt: credential.ExpiresAt,
		SourcePath: credential.TokenURL, DisplayName: "MCP OAuth",
	})
	return err
}

func (store mcpOAuthStore) Delete(ctx context.Context, id string) error {
	if store.auth == nil {
		return errors.New("credential service is unavailable")
	}
	return store.auth.DeleteCredential(ctx, "mcp", id)
}
