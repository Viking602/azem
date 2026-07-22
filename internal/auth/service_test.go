package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestStreamingHTTPClientHasNoTotalBodyTimeout(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	if service.streamClient.Timeout != 0 {
		t.Fatalf("streaming client total timeout = %v, want none", service.streamClient.Timeout)
	}
	transport, ok := service.streamClient.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("streaming transport = %#v", service.streamClient.Transport)
	}
}

func TestHasAnyAccount(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	service := NewService(provider.DB(), NewSQLiteStore(provider.DB()), chatgpt.NewClient(), grok.NewClient())
	if exists, err := service.HasAnyAccount(ctx, "chatgpt"); err != nil || exists {
		t.Fatalf("empty account lookup = %v, %v", exists, err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := provider.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		"account", "chatgpt", "sqlite:chatgpt:account", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if exists, err := service.HasAnyAccount(ctx, "chatgpt"); err != nil || !exists {
		t.Fatalf("existing account lookup = %v, %v", exists, err)
	}
	if exists, err := service.HasAnyAccount(ctx, "grok"); err != nil || exists {
		t.Fatalf("provider-scoped account lookup = %v, %v", exists, err)
	}
}

func TestFileStorePermissionsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "credentials.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Credential{Provider: "chatgpt", AccountID: "acct", AccessToken: "secret"}
	if _, err := store.Put(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o", info.Mode().Perm())
	}
	got, err := store.Get(context.Background(), "chatgpt", "acct")
	if err != nil || got.AccessToken != want.AccessToken {
		t.Fatalf("credential=%+v err=%v", got, err)
	}
	if err := store.Delete(context.Background(), "chatgpt", "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "chatgpt", "acct"); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("after delete error = %v", err)
	}
}

func TestLoginChatGPTCallbackPersistsLargeCredentialInSQLite(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store := NewSQLiteStore(provider.DB())

	accessToken := strings.Repeat("access-token-", 1500)
	refreshToken := strings.Repeat("refresh-token-", 900)
	idToken := strings.Repeat("id-token-", 900)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("code") != "callback-code" {
			t.Errorf("token form = %v", request.Form)
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(map[string]any{
			"access_token": accessToken, "refresh_token": refreshToken, "id_token": idToken, "expires_in": 3600,
		}); err != nil {
			t.Error(err)
		}
	}))
	defer tokenServer.Close()

	client := chatgpt.NewClient()
	client.AuthorizeURL = tokenServer.URL + "/authorize"
	client.TokenURL = tokenServer.URL + "/token"
	client.ClientID = "test-client"
	client.ListenCallback = func() (net.Listener, int, error) {
		listener, listenErr := net.Listen("tcp4", "127.0.0.1:0")
		if listenErr != nil {
			return nil, 0, listenErr
		}
		return listener, listener.Addr().(*net.TCPAddr).Port, nil
	}
	callbackDone := make(chan error, 1)
	service := NewService(provider.DB(), store, client, grok.NewClient())
	account, err := service.LoginChatGPT(ctx, func(rawAuthorizationURL string) error {
		authorizationURL, parseErr := url.Parse(rawAuthorizationURL)
		if parseErr != nil {
			return parseErr
		}
		callbackURL, parseErr := url.Parse(authorizationURL.Query().Get("redirect_uri"))
		if parseErr != nil {
			return parseErr
		}
		query := callbackURL.Query()
		query.Set("state", authorizationURL.Query().Get("state"))
		query.Set("code", "callback-code")
		callbackURL.RawQuery = query.Encode()
		go func() {
			response, callbackErr := http.Get(callbackURL.String())
			if callbackErr == nil {
				callbackErr = response.Body.Close()
			}
			callbackDone <- callbackErr
		}()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(account.CredentialRef, "sqlite:chatgpt:") {
		t.Fatalf("credential reference = %q", account.CredentialRef)
	}
	credential, err := store.Get(ctx, "chatgpt", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != accessToken || credential.RefreshToken != refreshToken || credential.IDToken != idToken {
		t.Fatalf("persisted credential lengths = access:%d refresh:%d id:%d", len(credential.AccessToken), len(credential.RefreshToken), len(credential.IDToken))
	}
}

func TestRefreshRetryForbiddenAndBestEffortLogout(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int32
	var revokes atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			refreshes.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
		case "/revoke":
			revokes.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oauth.Close()
	chat := chatgpt.NewClient()
	chat.TokenURL = oauth.URL + "/token"
	chat.RevokeURL = oauth.URL + "/revoke"
	chat.ClientID = "test"
	service := NewService(provider.DB(), store, chat, grok.NewClient())
	var statusChanges []AccountStatusChange
	service.SetStatusChangeCallback(func(_ context.Context, change AccountStatusChange) {
		statusChanges = append(statusChanges, change)
	})
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"old-access","refresh_token":"old-refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	account, err := service.ImportChatGPT(ctx, importPath)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	resource := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path == "/forbidden" {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		if request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("account header = %q", request.Header.Get("ChatGPT-Account-ID"))
		}
		if request.Header.Get("Authorization") == "Bearer old-access" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	defer resource.Close()
	response, err := service.DoWithRefresh(ctx, "chatgpt", account.ID, func(Credential) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, resource.URL+"/ok", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 2 || refreshes.Load() != 1 {
		t.Fatalf("calls=%d refreshes=%d", calls.Load(), refreshes.Load())
	}
	_, err = service.DoWithRefresh(ctx, "chatgpt", account.ID, func(Credential) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, resource.URL+"/forbidden", nil)
	})
	var entitlement EntitlementError
	if !errors.As(err, &entitlement) {
		t.Fatalf("forbidden error = %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("403 triggered refresh; count=%d", refreshes.Load())
	}
	if err := service.Logout(ctx, "chatgpt", account.ID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if revokes.Load() != 1 {
		t.Fatalf("revokes=%d", revokes.Load())
	}
	if _, err := store.Get(ctx, "chatgpt", account.ID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("credential survived logout: %v", err)
	}
	metadata, err := service.Account(ctx, "chatgpt", account.ID)
	if err != nil || metadata.Status != "logged_out" {
		t.Fatalf("account=%+v err=%v", metadata, err)
	}
	if len(statusChanges) != 2 ||
		statusChanges[0] != (AccountStatusChange{Provider: "chatgpt", AccountID: account.ID, Status: "active"}) ||
		statusChanges[1] != (AccountStatusChange{Provider: "chatgpt", AccountID: account.ID, Status: "logged_out"}) {
		t.Fatalf("status callbacks=%+v", statusChanges)
	}
}

func TestImportGrokCLIRefreshesBeforePersisting(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oauth *httptest.Server
	oauth = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = writer.Write([]byte(`{"issuer":"` + oauth.URL + `","device_authorization_endpoint":"` + oauth.URL + `/device","token_endpoint":"` + oauth.URL + `/token"}`))
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("client_id") != "imported-client" || request.Form.Get("refresh_token") != "imported-refresh" {
				t.Errorf("refresh form=%v", request.Form)
			}
			_, _ = writer.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oauth.Close()
	grokClient := grok.NewClient()
	grokClient.DiscoveryURL = oauth.URL + "/.well-known/openid-configuration"
	grokClient.AllowInsecure = true
	service := NewService(provider.DB(), store, chatgpt.NewClient(), grokClient)
	importPath := filepath.Join(t.TempDir(), "auth.json")
	contents := `{"https://auth.x.ai::imported-client":{"auth_mode":"oidc","oidc_client_id":"imported-client","oidc_issuer":"https://auth.x.ai","principal_id":"principal","email":"person@example.com","refresh_token":"imported-refresh","expires_at":"2026-07-16T10:00:00Z"}}`
	if err := os.WriteFile(importPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	account, err := service.ImportGrok(ctx, importPath)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Get(ctx, "grok", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "principal" || credential.AccessToken != "fresh-access" ||
		credential.RefreshToken != "rotated-refresh" || credential.OAuthClientID != "imported-client" {
		t.Fatalf("account=%+v credential=%+v", account, credential)
	}
	synced, err := grok.Import(importPath)
	if err != nil {
		t.Fatal(err)
	}
	if synced.AccessToken != "fresh-access" || synced.RefreshToken != "rotated-refresh" {
		t.Fatalf("synced Grok CLI credential=%+v", synced)
	}
}
