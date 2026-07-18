package grok

import (
	"context"
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

func TestDiscoveryAndDevicePolling(t *testing.T) {
	var polls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(writer).Encode(Discovery{Issuer: server.URL, DeviceAuthorizationEndpoint: server.URL + "/device", TokenEndpoint: server.URL + "/token", RevocationEndpoint: server.URL + "/revoke"})
		case "/device":
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
