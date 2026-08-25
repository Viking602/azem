package grok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultClientFollowsProxyResolver(t *testing.T) {
	client := NewClient()
	transport, ok := client.HTTP.Transport().(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("Grok transport = %#v", client.HTTP.Transport())
	}
}

func TestWithClientIDPreservesDependenciesWithoutMutatingDefault(t *testing.T) {
	client := NewClient()
	client.UserURL = "https://example.com/user"
	client.Wait = func(context.Context, time.Duration) error { return nil }

	override := client.WithClientID("imported-client")
	if override == client {
		t.Fatal("client ID override reused the mutable default client")
	}
	if override.ClientID != "imported-client" || client.ClientID != DefaultClientID {
		t.Fatalf("override=%q default=%q", override.ClientID, client.ClientID)
	}
	if override.HTTP != client.HTTP || override.DiscoveryURL != client.DiscoveryURL ||
		override.UserURL != client.UserURL || override.Scope != client.Scope || override.Wait == nil {
		t.Fatalf("override did not retain client dependencies: %+v", override)
	}
	if client.WithClientID("") != client {
		t.Fatal("empty client ID should reuse the default client")
	}
}

func TestRefreshSendsClientHeadersAndIncludesErrorBody(t *testing.T) {
	var sawVersion, sawSurface bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		sawVersion = request.Header.Get("x-grok-client-version") == DefaultClientVersion
		sawSurface = request.Header.Get("x-grok-client-surface") == deviceClientSurface
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token reused"}`))
	}))
	defer server.Close()
	client := NewClient()
	client.AllowInsecure = true
	_, err := client.Refresh(context.Background(), Discovery{TokenEndpoint: server.URL + "/token"}, "stale-refresh")
	if err == nil || err.Error() != "refresh returned HTTP 400: invalid_grant: refresh token reused" {
		t.Fatalf("error = %v", err)
	}
	if !sawVersion || !sawSurface {
		t.Fatalf("refresh headers version=%v surface=%v", sawVersion, sawSurface)
	}
}

func TestDiscoveryAndDevicePolling(t *testing.T) {
	var polls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/device" || request.URL.Path == "/token" {
			if got := request.Header.Get("x-grok-client-version"); got != "0.2.121" {
				t.Errorf("client version header = %q, want %q", got, "0.2.121")
			}
			if got := request.Header.Get("x-grok-client-surface"); got != "ui" {
				t.Errorf("client surface header = %q, want %q", got, "ui")
			}
		}
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(writer).Encode(Discovery{Issuer: server.URL, DeviceAuthorizationEndpoint: server.URL + "/device", TokenEndpoint: server.URL + "/token", RevocationEndpoint: server.URL + "/revoke"})
		case "/device":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if got := request.Form.Get("referrer"); got != "grok-build" {
				t.Errorf("device referrer = %q, want %q", got, "grok-build")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"device_code": "device", "user_code": "ABCD", "verification_uri": server.URL + "/verify", "expires_in": 600, "interval": 1})
		case "/token":
			if polls.Add(1) == 1 {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600, "account_id": "acct"})
		default:
			writer.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client := NewClient()
	client.DiscoveryURL = server.URL + "/.well-known/openid-configuration"
	client.AllowInsecure = true
	client.Wait = func(context.Context, time.Duration) error { return nil }
	discovery, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	device, err := client.BeginDevice(context.Background(), discovery)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.PollDevice(context.Background(), discovery, device)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access" || tokens.AccountID != "acct" || polls.Load() != 2 {
		t.Fatalf("tokens=%+v polls=%d", tokens, polls.Load())
	}
}

func TestDeviceDenialAndEndpointGuard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer server.Close()
	client := NewClient()
	client.AllowInsecure = true
	client.Wait = func(context.Context, time.Duration) error { return nil }
	_, err := client.PollDevice(context.Background(), Discovery{TokenEndpoint: server.URL}, DeviceAuthorization{DeviceCode: "code", Interval: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("error = %v", err)
	}
	client.DiscoveryURL = server.URL
	client.AllowInsecure = false
	if _, err := client.Discover(context.Background()); err == nil {
		t.Fatal("insecure discovery endpoint accepted")
	}
	if err := client.ValidateResourceURL(DefaultUserURL); err != nil {
		t.Fatalf("official Grok user endpoint rejected: %v", err)
	}
	if err := client.ValidateResourceURL(DefaultQuotaURL); err != nil {
		t.Fatalf("official Grok CLI proxy rejected: %v", err)
	}
	if err := client.ValidateResourceURL("https://cli-chat-proxy.grok.com.attacker.example/v1/billing"); err == nil {
		t.Fatal("lookalike Grok CLI proxy accepted")
	}
}

func TestSlowDownAndExpiredDeviceCodes(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch polls.Add(1) {
		case 1:
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"slow_down"}`))
		case 2:
			_, _ = writer.Write([]byte(`{"access_token":"access"}`))
		default:
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"expired_token"}`))
		}
	}))
	defer server.Close()
	client := NewClient()
	client.AllowInsecure = true
	var waits []time.Duration
	client.Wait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	tokens, err := client.PollDevice(context.Background(), Discovery{TokenEndpoint: server.URL}, DeviceAuthorization{DeviceCode: "code", Interval: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil || tokens.AccessToken != "access" {
		t.Fatalf("tokens=%+v error=%v", tokens, err)
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != 6*time.Second {
		t.Fatalf("poll waits = %v", waits)
	}
	_, err = client.PollDevice(context.Background(), Discovery{TokenEndpoint: server.URL}, DeviceAuthorization{DeviceCode: "code", Interval: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestImportGrokCLIOIDCCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	contents := `{
		"https://auth.x.ai::client-id": {
			"auth_mode": "oidc",
			"oidc_client_id": "client-id",
			"oidc_issuer": "https://auth.x.ai",
			"principal_id": "principal",
			"email": "person@example.com",
			"key": "access",
			"refresh_token": "refresh",
			"expires_at": "2026-07-16T10:00:00.000000Z"
		}
	}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens, err := Import(path)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" || tokens.ClientID != "client-id" ||
		tokens.AccountID != "principal" || tokens.Email != "person@example.com" || tokens.ExpiresAt.IsZero() ||
		tokens.SourcePath != path || tokens.SourceKey != "https://auth.x.ai::client-id" {
		t.Fatalf("tokens=%+v", tokens)
	}
}

func TestDecodeTokensReadsIdentityFromIDToken(t *testing.T) {
	idToken := testJWT(map[string]any{
		"sub": "user-42", "email": "owner@example.com", "name": "Owner", "tier": "SuperGrok",
	})
	tokens, err := decodeTokens([]byte(`{"access_token":"access","refresh_token":"refresh","id_token":"` + idToken + `","expires_in":3600}`))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccountID != "user-42" || tokens.Email != "owner@example.com" || tokens.DisplayName != "owner@example.com" || tokens.Plan != "SuperGrok" {
		t.Fatalf("tokens=%+v", tokens)
	}
}

func TestIdentityFromTokensPrefersEmailAndIgnoresRawToken(t *testing.T) {
	identity := IdentityFromTokens(testJWT(map[string]any{
		"sub": "opaque-sub", "preferred_username": "handle", "email": "person@x.ai",
	}), "not-a-jwt")
	if identity.UserID != "opaque-sub" || identity.Email != "person@x.ai" || identity.DisplayName != "person@x.ai" {
		t.Fatalf("identity=%+v", identity)
	}
	if identity.DisplayName == "not-a-jwt" || identity.UserID == "not-a-jwt" {
		t.Fatal("raw access token leaked into identity")
	}
}

func TestUserLookupOmitsUserIDAndDecodesProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/user" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || request.Header.Get("x-grok-client-mode") != ClientModeHeadless {
			t.Errorf("headers = %v", request.Header)
		}
		if request.Header.Get("x-userid") != "" {
			t.Errorf("user lookup sent x-userid = %q", request.Header.Get("x-userid"))
		}
		if request.URL.Query().Get("include") != "subscription" {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"userId": "usr_live", "email": "live@example.com", "firstName": "Ada", "lastName": "Lovelace", "subscriptionTier": "SuperGrok Heavy",
		})
	}))
	defer server.Close()
	client := NewClient()
	client.AllowInsecure = true
	client.UserURL = server.URL + "/v1/user?include=subscription"
	info, err := client.User(context.Background(), "access")
	if err != nil {
		t.Fatal(err)
	}
	if info.UserID != "usr_live" || info.Email != "live@example.com" || info.DisplayName() != "live@example.com" || info.SubscriptionTier != "SuperGrok Heavy" {
		t.Fatalf("info=%+v", info)
	}
}

func testJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
