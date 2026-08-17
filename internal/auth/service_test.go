package auth

import (
	"context"
	"encoding/base64"
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

	"resty.dev/v3"

	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestStreamingHTTPClientHasNoTotalBodyTimeout(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	if service.streamClient.Timeout() != 0 {
		t.Fatalf("streaming client total timeout = %v, want none", service.streamClient.Timeout())
	}
	transport, ok := service.streamClient.Transport().(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout != 30*time.Second || transport.Proxy == nil {
		t.Fatalf("streaming transport = %#v", service.streamClient.Transport())
	}
	httpTransport, ok := service.httpClient.Transport().(*http.Transport)
	if !ok || httpTransport.Proxy == nil {
		t.Fatalf("request transport = %#v", service.httpClient.Transport())
	}
}

func TestClassifyStreamOpenErrorRetriesTransportCancellationOnly(t *testing.T) {
	transportCancellation := classifyStreamOpenError(context.Background(), "chatgpt", context.Canceled)
	if !hyprovider.IsRetryableError(transportCancellation) {
		t.Fatalf("healthy caller transport cancellation is not retryable: %v", transportCancellation)
	}

	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()
	callerCancellation := classifyStreamOpenError(callerCtx, "chatgpt", context.Canceled)
	if !errors.Is(callerCancellation, context.Canceled) || hyprovider.IsRetryableError(callerCancellation) {
		t.Fatalf("caller cancellation classification=%v retryable=%v", callerCancellation, hyprovider.IsRetryableError(callerCancellation))
	}
}

func TestDecodeSubscriptionQuotas(t *testing.T) {
	chatgpt, err := decodeChatGPTQuota([]byte(`{
		"plan_type":"pro",
		"rate_limit":{"primary_window":{"used_percent":61.5,"limit_window_seconds":604800,"reset_at":1786500000}},
		"credits":{"unlimited":false,"balance":"12.50"}
	}`))
	if err != nil || chatgpt.Plan != "pro" || chatgpt.UsedPercent != 61.5 || chatgpt.ResetsAt != 1786500000 || chatgpt.Balance != "12.50" {
		t.Fatalf("ChatGPT quota=%+v error=%v", chatgpt, err)
	}

	grok, err := decodeGrokQuota([]byte(`{
		"subscription_tier":"SuperGrok Heavy",
		"config":{"creditUsagePercent":42.5,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-03T00:00:00Z","end":"2026-08-10T00:00:00Z"},"prepaidBalance":{"val":725}}
	}`))
	if err != nil || grok.Plan != "SuperGrok Heavy" || grok.Period != "weekly" || grok.UsedPercent != 42.5 || grok.ResetsAt == 0 || grok.Balance != "7.25" {
		t.Fatalf("Grok quota=%+v error=%v", grok, err)
	}

	live, err := decodeGrokQuota([]byte(`{
		"config":{
			"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-10T00:00:00Z","end":"2026-08-17T00:00:00Z"},
			"billingPeriodStart":"2026-08-01T00:00:00Z",
			"billingPeriodEnd":"2026-09-01T00:00:00Z",
			"isUnifiedBillingUser":true,
			"onDemandCap":{"val":100},
			"onDemandUsed":{"val":0},
			"prepaidBalance":{"val":0}
		}
	}`))
	if err != nil || live.Period != "weekly" || live.UsedPercent != 0 || live.ResetsAt == 0 || live.Balance != "" {
		t.Fatalf("live Grok credits shape=%+v error=%v", live, err)
	}

	periodOnly, err := decodeGrokQuota([]byte(`{
		"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_MONTHLY","start":"2026-08-01T00:00:00Z","end":"2026-09-01T00:00:00Z"}}
	}`))
	if err != nil || periodOnly.Period != "monthly" || periodOnly.UsedPercent != 0 || periodOnly.ResetsAt == 0 {
		t.Fatalf("period-only Grok quota=%+v error=%v", periodOnly, err)
	}

	if _, err := decodeGrokQuota([]byte(`{"config":{}}`)); err == nil || !strings.Contains(err.Error(), "no usage or billing period") {
		t.Fatalf("empty config error = %v", err)
	}
}

func TestGrokQuotaUsesLiveUserIDAndSurfacesHTTPReason(t *testing.T) {
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
	idToken := grokTestJWT(map[string]any{"sub": "jwt-user", "email": "owner@example.com"})
	var seenUserID []string
	var userHadUserID bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user":
			if request.Header.Get("x-userid") != "" {
				userHadUserID = true
			}
			if request.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || request.Header.Get("x-grok-client-mode") != grok.ClientModeHeadless {
				t.Errorf("user headers = %v", request.Header)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"userId": "usr_live", "email": "owner@example.com", "subscriptionTier": "SuperGrok"})
		case "/billing":
			seenUserID = append(seenUserID, request.Header.Get("x-userid"))
			if request.URL.Query().Get("format") != "credits" {
				t.Errorf("billing query = %s", request.URL.RawQuery)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"subscription_tier": "SuperGrok",
				"config": map[string]any{
					"creditUsagePercent": 18.5,
					"currentPeriod":      map[string]any{"type": "USAGE_PERIOD_TYPE_WEEKLY", "start": "2026-08-03T00:00:00Z", "end": "2026-08-10T00:00:00Z"},
					"prepaidBalance":     map[string]any{"val": 250},
				},
			})
		case "/fail-user":
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"invalid user"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	grokClient := grok.NewClient()
	grokClient.AllowInsecure = true
	service := NewService(provider.DB(), store, chatgpt.NewClient(), grokClient)
	service.GrokUserURL = server.URL + "/user"
	service.GrokQuotaURL = server.URL + "/billing?format=credits"
	if _, err := store.Put(ctx, Credential{Provider: "grok", AccountID: "anonymous-be73a171915548ed", AccessToken: "access", IDToken: idToken}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := provider.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"anonymous-be73a171915548ed", "grok", "", "", "", "file:grok:anonymous-be73a171915548ed", "active", now, now); err != nil {
		t.Fatal(err)
	}
	quota, err := service.SubscriptionQuota(ctx, "grok", "anonymous-be73a171915548ed")
	if err != nil {
		t.Fatal(err)
	}
	if userHadUserID {
		t.Fatal("user lookup included x-userid")
	}
	if len(seenUserID) != 1 || seenUserID[0] != "usr_live" {
		t.Fatalf("billing x-userid = %v", seenUserID)
	}
	if quota.UsedPercent != 18.5 || quota.Email != "owner@example.com" || quota.DisplayName != "owner@example.com" || quota.UserID != "usr_live" || quota.Balance != "2.50" {
		t.Fatalf("quota=%+v", quota)
	}
	account, err := service.Account(ctx, "grok", "anonymous-be73a171915548ed")
	if err != nil || account.ID != "anonymous-be73a171915548ed" || account.Email != "owner@example.com" {
		t.Fatalf("persisted account=%+v err=%v", account, err)
	}
	service.GrokUserURL = server.URL + "/fail-user"
	failed, failErr := service.SubscriptionQuota(ctx, "grok", "anonymous-be73a171915548ed")
	if failErr == nil || !strings.Contains(failErr.Error(), "HTTP 500") || !strings.Contains(failErr.Error(), "invalid user") {
		t.Fatalf("user failure = %v", failErr)
	}
	if failed.Email != "owner@example.com" {
		t.Fatalf("failed quota dropped identity: %+v", failed)
	}
}

func TestGrokQuotaRetriesTransientUserEOF(t *testing.T) {
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
	var userHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user":
			if userHits.Add(1) == 1 {
				hijacker, ok := writer.(http.Hijacker)
				if !ok {
					t.Fatal("response writer cannot hijack")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"userId": "usr_live", "email": "owner@example.com"})
		case "/billing":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"subscription_tier": "SuperGrok",
				"config":            map[string]any{"creditUsagePercent": 12.0, "currentPeriod": map[string]any{"end": "2026-08-10T00:00:00Z"}},
			})
		case "/always-eof":
			hijacker, ok := writer.(http.Hijacker)
			if !ok {
				t.Fatal("response writer cannot hijack")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	grokClient := grok.NewClient()
	grokClient.AllowInsecure = true
	service := NewService(provider.DB(), store, chatgpt.NewClient(), grokClient)
	service.GrokUserURL = server.URL + "/user"
	service.GrokQuotaURL = server.URL + "/billing?format=credits"
	if _, err := store.Put(ctx, Credential{Provider: "grok", AccountID: "anonymous-be73a171915548ed", AccessToken: "access"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := provider.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"anonymous-be73a171915548ed", "grok", "", "", "", "file:grok:anonymous-be73a171915548ed", "active", now, now); err != nil {
		t.Fatal(err)
	}
	quota, err := service.SubscriptionQuota(ctx, "grok", "anonymous-be73a171915548ed")
	if err != nil {
		t.Fatal(err)
	}
	if userHits.Load() != 2 || quota.UserID != "usr_live" || quota.UsedPercent != 12 {
		t.Fatalf("hits=%d quota=%+v", userHits.Load(), quota)
	}

	service.GrokUserURL = server.URL + "/always-eof"
	failed, failErr := service.SubscriptionQuota(ctx, "grok", "anonymous-be73a171915548ed")
	if failErr == nil || !strings.Contains(failErr.Error(), "EOF") {
		t.Fatalf("persistent EOF = %v", failErr)
	}
	if failed.Email != "owner@example.com" {
		t.Fatalf("failed quota dropped identity: %+v", failed)
	}
}

func TestHydrateGrokAccountReadsJWTWithoutRenamingAccount(t *testing.T) {
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
	service := NewService(provider.DB(), store, chatgpt.NewClient(), grok.NewClient())
	idToken := grokTestJWT(map[string]any{"sub": "jwt-user", "email": "owner@example.com", "preferred_username": "owner"})
	if _, err := store.Put(ctx, Credential{Provider: "grok", AccountID: "anonymous-deadbeef", AccessToken: "access", IDToken: idToken}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := provider.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"anonymous-deadbeef", "grok", "", "", "", "file:grok:anonymous-deadbeef", "active", now, now); err != nil {
		t.Fatal(err)
	}
	account, err := service.Account(ctx, "grok", "anonymous-deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	hydrated := service.HydrateGrokAccount(ctx, account)
	if hydrated.ID != "anonymous-deadbeef" || hydrated.Email != "owner@example.com" || hydrated.DisplayName != "owner@example.com" {
		t.Fatalf("hydrated=%+v", hydrated)
	}
	stored, err := service.Account(ctx, "grok", "anonymous-deadbeef")
	if err != nil || stored.Email != "owner@example.com" || stored.ID != "anonymous-deadbeef" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func grokTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
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

func TestSetAPIKeyReplacesSingleProviderCredential(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store := NewSQLiteStore(provider.DB())
	service := NewService(provider.DB(), store, nil, nil)
	for _, secret := range []string{"first", "second"} {
		account, err := service.SetAPIKey(ctx, "openrouter", secret)
		if err != nil {
			t.Fatal(err)
		}
		if account.ID != "api-key" || account.Provider != "openrouter" || account.Status != "active" {
			t.Fatalf("account=%+v", account)
		}
	}
	credential, err := store.Get(ctx, "openrouter", "api-key")
	if err != nil || credential.AccessToken != "second" {
		t.Fatalf("credential=%+v err=%v", credential, err)
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
	response, err := service.DoWithRefresh(ctx, "chatgpt", account.ID, resty.MethodGet, resource.URL+"/ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.String() != "ok" {
		t.Fatalf("response body = %q", response.String())
	}
	if calls.Load() != 2 || refreshes.Load() != 1 {
		t.Fatalf("calls=%d refreshes=%d", calls.Load(), refreshes.Load())
	}
	_, err = service.DoWithRefresh(ctx, "chatgpt", account.ID, resty.MethodGet, resource.URL+"/forbidden", nil)
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
