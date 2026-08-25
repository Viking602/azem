package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
)

type memoryOAuthStore struct {
	mu     sync.Mutex
	values map[string]OAuthCredential
}

func (store *memoryOAuthStore) Get(_ context.Context, id string) (OAuthCredential, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.values[id]
	return value, ok, nil
}

func (store *memoryOAuthStore) Put(_ context.Context, id string, credential OAuthCredential) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.values == nil {
		store.values = make(map[string]OAuthCredential)
	}
	store.values[id] = credential
	return nil
}

func (store *memoryOAuthStore) Delete(_ context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.values, id)
	return nil
}

func TestOAuthBrokerRefreshesAndInjectsBearerCredential(t *testing.T) {
	var refreshForm url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		refreshForm = request.Form
		_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "new-access", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	server := config.MCPServerConfig{Transport: "streamable_http", URL: "https://mcp.example.test", Auth: &config.MCPAuthConfig{Type: "oauth", TokenURL: tokenServer.URL, ClientID: "client"}}
	id := mcpOAuthCredentialID("demo", server)
	if mcpOAuthCredentialID("demo", server) != mcpOAuthCredentialID("other-name", server) {
		t.Fatal("OAuth credentials are not URL-keyed across server aliases")
	}
	store := &memoryOAuthStore{values: map[string]OAuthCredential{id: {
		AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", ClientID: "client", ExpiresAt: time.Now().Add(-time.Minute), TokenURL: tokenServer.URL,
	}}}
	broker := &OAuthBroker{Store: store, HTTPClient: tokenServer.Client()}
	header, err := broker.AuthorizationHeader(context.Background(), "demo", server)
	if err != nil {
		t.Fatal(err)
	}
	if header != "Bearer new-access" || refreshForm.Get("grant_type") != "refresh_token" || refreshForm.Get("refresh_token") != "refresh" {
		t.Fatalf("header=%q form=%v", header, refreshForm)
	}
}

func TestOAuthBrokerCompletesAuthorizationCodePKCEFlow(t *testing.T) {
	var authorizationQuery url.Values
	var tokenForm url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		tokenForm, _ = url.ParseQuery(string(payload))
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	server := config.MCPServerConfig{
		Transport: "streamable_http", URL: "https://mcp.example.test",
		OAuth: &config.MCPOAuthConfig{AuthorizationURL: tokenServer.URL + "/authorize", TokenURL: tokenServer.URL, ClientID: "client", Scopes: []string{"read", "offline_access"}},
	}
	store := &memoryOAuthStore{values: make(map[string]OAuthCredential)}
	broker := &OAuthBroker{Store: store, HTTPClient: tokenServer.Client()}
	openURL := func(raw string) error {
		parsed, err := url.Parse(raw)
		if err != nil {
			return err
		}
		authorizationQuery = parsed.Query()
		callback := authorizationQuery.Get("redirect_uri") + "?code=code-value&state=" + url.QueryEscape(authorizationQuery.Get("state"))
		go func() {
			response, requestErr := http.Get(callback)
			if requestErr == nil {
				_ = response.Body.Close()
			}
		}()
		return nil
	}
	if err := broker.Authenticate(context.Background(), "demo", server, openURL); err != nil {
		t.Fatal(err)
	}
	if authorizationQuery.Get("code_challenge_method") != "S256" || authorizationQuery.Get("scope") != "read offline_access" || authorizationQuery.Get("resource") != server.URL {
		t.Fatalf("authorization query = %v", authorizationQuery)
	}
	verifier := tokenForm.Get("code_verifier")
	digest := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(digest[:]) != authorizationQuery.Get("code_challenge") || tokenForm.Get("code") != "code-value" {
		t.Fatalf("PKCE/token form = %v query=%v", tokenForm, authorizationQuery)
	}
	credential, ok, err := store.Get(context.Background(), mcpOAuthCredentialID("demo", server))
	if err != nil || !ok || credential.AccessToken != "access" || credential.RefreshToken != "refresh" {
		t.Fatalf("stored credential = %#v, %v, %v", credential, ok, err)
	}
	if err := broker.Unauthenticate(context.Background(), "demo", server); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(context.Background(), mcpOAuthCredentialID("demo", server)); ok {
		t.Fatal("OAuth credential survived unauthenticate")
	}
}

func TestOAuthBrokerRejectsNonLoopbackRedirect(t *testing.T) {
	server := config.MCPServerConfig{OAuth: &config.MCPOAuthConfig{RedirectURI: "https://example.com/callback"}}
	listener, _, _, err := oauthCallbackListener(server)
	if listener != nil {
		listener.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("redirect error = %v", err)
	}
}
