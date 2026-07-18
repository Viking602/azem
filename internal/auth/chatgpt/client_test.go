package chatgpt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestPKCEAuthorizationAndExchange(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		form = request.Form
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600})
	}))
	defer server.Close()
	client := NewClient()
	client.TokenURL = server.URL
	client.ClientID = "test-client"
	pkce, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkce.Verifier) < 43 || pkce.State == "" || pkce.Challenge == "" {
		t.Fatalf("invalid PKCE: %+v", pkce)
	}
	redirectURI := "http://localhost:1455/auth/callback"
	authorize, err := client.AuthorizationURL(redirectURI, pkce)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorize)
	query := parsed.Query()
	if query.Get("response_type") != "code" ||
		query.Get("client_id") != "test-client" ||
		query.Get("redirect_uri") != redirectURI ||
		query.Get("scope") != "openid profile email offline_access api.connectors.read api.connectors.invoke" ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") != pkce.Challenge ||
		query.Get("state") != pkce.State ||
		query.Get("id_token_add_organizations") != "true" ||
		query.Get("codex_cli_simplified_flow") != "true" ||
		query.Get("originator") != "codex_cli_rs" {
		t.Fatalf("authorization query = %s", parsed.RawQuery)
	}
	tokens, err := client.Exchange(context.Background(), "code", pkce.Verifier, redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access" || form.Get("code_verifier") != pkce.Verifier || form.Get("grant_type") != "authorization_code" {
		t.Fatalf("tokens=%+v form=%v", tokens, form)
	}
}

func TestOAuthCallbackUsesCodexAllowlistedPort(t *testing.T) {
	listener, port, err := listenOAuthCallback()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if port != defaultCallbackPort {
		t.Fatalf("OAuth callback port = %d, want %d", port, defaultCallbackPort)
	}
}

func TestImportCodexVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens, err := ImportCodex(path)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" || tokens.AccountID != "acct" {
		t.Fatalf("tokens = %+v", tokens)
	}
}
