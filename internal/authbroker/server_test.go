package authbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

const testToken = "test-token-abcdefghijklmnopqrstuvwxyz-0123456789"

func TestBrokerCredentialSnapshotBlocksDisableAndUsage(t *testing.T) {
	ctx := context.Background()
	broker, store, closeStore := brokerTestServer(t, ctx)
	defer closeStore()
	server := httptest.NewServer(broker.Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/healthz")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%v error=%v", responseStatus(response), err)
	}
	response.Body.Close()
	response, _ = http.Get(server.URL + "/v1/snapshot")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.StatusCode)
	}
	response.Body.Close()
	credential := auth.Credential{
		Provider: "chatgpt", AccountID: "account-1", AccessToken: "access-secret", RefreshToken: "refresh-must-never-leak",
		ExpiresAt: time.Now().Add(time.Hour).UTC(), Email: "alice@example.com", DisplayName: "Alice", Plan: "pro",
	}
	response = brokerRequest(t, http.MethodPost, server.URL+"/v1/credential", credential)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("put status=%d body=%s", response.StatusCode, readBody(response))
	}
	var stored Credential
	decodeResponse(t, response, &stored)
	if stored.ID == "" || stored.AccessToken != "access-secret" || stored.RefreshToken != RemoteRefreshSentinel || strings.Contains(mustJSON(stored), "refresh-must-never-leak") {
		t.Fatalf("stored credential=%#v", stored)
	}
	response = brokerRequest(t, http.MethodGet, server.URL+"/v1/snapshot", nil)
	var snapshot Snapshot
	decodeResponse(t, response, &snapshot)
	if snapshot.Generation <= 0 || len(snapshot.Credentials) != 1 || snapshot.Credentials[0].IdentityKey != "email:alice@example.com" || snapshot.Credentials[0].RefreshToken != RemoteRefreshSentinel {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	etag := response.Header.Get("ETag")
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/snapshot?wait=5", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("If-None-Match", etag)
	response, err = http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusNotModified || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("conditional snapshot status=%v error=%v", responseStatus(response), err)
	}
	response.Body.Close()
	block := map[string]any{"scope": "chat", "blockedUntil": time.Now().Add(time.Hour).UTC(), "reason": "rate limit"}
	response = brokerRequest(t, http.MethodPost, server.URL+"/v1/credential/"+stored.ID+"/block", block)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("block status=%d body=%s", response.StatusCode, readBody(response))
	}
	response.Body.Close()
	response = brokerRequest(t, http.MethodGet, server.URL+"/v1/snapshot", nil)
	decodeResponse(t, response, &snapshot)
	if len(snapshot.Blocks) != 1 || snapshot.Blocks[0].Scope != "chat" {
		t.Fatalf("blocked snapshot=%#v", snapshot)
	}
	response = brokerRequest(t, http.MethodPost, server.URL+"/v1/credential/"+stored.ID+"/disable", map[string]string{"cause": "revoked"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = brokerRequest(t, http.MethodGet, server.URL+"/v1/snapshot", nil)
	snapshot = Snapshot{}
	decodeResponse(t, response, &snapshot)
	if snapshot.Credentials[0].Status != "disabled" || snapshot.Credentials[0].AccessToken != "" {
		t.Fatalf("disabled snapshot=%#v", snapshot)
	}
	response = brokerRequest(t, http.MethodGet, server.URL+"/v1/credentials/disabled?provider=chatgpt", nil)
	var disabled []map[string]any
	decodeResponse(t, response, &disabled)
	if len(disabled) != 1 || disabled[0]["cause"] != "revoked" {
		t.Fatalf("disabled=%#v", disabled)
	}
	usage := map[string]any{"clientId": "client", "credentialId": stored.ID, "provider": "chatgpt", "accountId": "account-1", "payload": map[string]any{"inputTokens": 12}, "observedAt": time.Now().UTC()}
	response = brokerRequest(t, http.MethodPost, server.URL+"/v1/usage/observed", usage)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", response.StatusCode, readBody(response))
	}
	response.Body.Close()
	response = brokerRequest(t, http.MethodGet, server.URL+"/v1/usage/history?provider=chatgpt", nil)
	var history []map[string]any
	decodeResponse(t, response, &history)
	if len(history) != 1 || history[0]["provider"] != "chatgpt" {
		t.Fatalf("usage history=%#v", history)
	}
	var generation int64
	if err := store.DB().QueryRow(`SELECT generation FROM auth_broker_state WHERE id=1`).Scan(&generation); err != nil || generation < 3 {
		t.Fatalf("durable generation=%d error=%v", generation, err)
	}
}

func TestBrokerClientAccountPoolEncryptedCacheAndTransientFallback(t *testing.T) {
	ctx := context.Background()
	broker, _, closeStore := brokerTestServer(t, ctx)
	defer closeStore()
	if _, err := broker.auth.StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "alice", AccessToken: "alice-access", RefreshToken: "alice-refresh", Email: "alice@example.com", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.auth.StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "bob", AccessToken: "bob-access", RefreshToken: "bob-refresh", Email: "bob@example.com", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker.Handler())
	cachePath := filepath.Join(t.TempDir(), "cache", "snapshot.enc")
	client, err := NewClient(ClientOptions{BaseURL: server.URL, Token: testToken, Cache: SnapshotCache{Path: cachePath, TTL: time.Hour}, AccountPool: map[string][]string{"chatgpt": {"email:alice@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.FetchSnapshot(ctx)
	if err != nil || len(snapshot.Credentials) != 1 || snapshot.Credentials[0].AccountID != "alice" {
		t.Fatalf("pooled snapshot=%#v error=%v", snapshot, err)
	}
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("alice-access")) || bytes.Contains(payload, []byte("bob-access")) {
		t.Fatal("encrypted snapshot cache contains plaintext access token")
	}
	mutated := snapshot
	mutated.Credentials[0].AccessToken = "mutated"
	again, err := client.FetchSnapshot(ctx)
	if err != nil || again.Credentials[0].AccessToken != "alice-access" {
		t.Fatalf("snapshot caller mutated cache=%#v error=%v", again, err)
	}
	server.Close()
	fallbackClient, err := NewClient(ClientOptions{BaseURL: server.URL, Token: testToken, Cache: SnapshotCache{Path: cachePath, TTL: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := fallbackClient.FetchSnapshot(ctx)
	if err != nil || len(fallback.Credentials) != 2 {
		t.Fatalf("cache fallback=%#v error=%v", fallback, err)
	}
	wrongTokenClient, err := NewClient(ClientOptions{BaseURL: server.URL, Token: testToken + "-wrong", Cache: SnapshotCache{Path: cachePath, TTL: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongTokenClient.FetchSnapshot(ctx); err == nil {
		t.Fatal("wrong token decrypted snapshot cache")
	}
}

func TestBrokerTokenFilePermissionsRotationAndSymlinkDefense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "auth-broker.token")
	first, err := EnsureToken(path, false)
	if err != nil || len(first) < 32 {
		t.Fatalf("first token=%q error=%v", first, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%#o", info.Mode().Perm())
	}
	same, err := EnsureToken(path, false)
	if err != nil || same != first {
		t.Fatalf("same token=%q error=%v", same, err)
	}
	rotated, err := EnsureToken(path, true)

	if err != nil || rotated == first {
		t.Fatalf("rotated token=%q error=%v", rotated, err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "token-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureToken(link, true); err == nil {
		t.Fatal("rotated through symlink")
	}
}

func TestBrokerRefresherDisablesDefinitiveFailuresButKeepsTransientOnes(t *testing.T) {
	ctx := context.Background()
	broker, store, closeStore := brokerTestServer(t, ctx)
	defer closeStore()
	expiry := time.Now().Add(time.Minute)
	if _, err := broker.auth.StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "revoked", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.auth.StoreCredential(ctx, auth.Credential{Provider: "grok", AccountID: "transient", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	refresher, err := NewRefresher(RefresherOptions{
		DB: store.DB(), Auth: broker.auth, Skew: 5 * time.Minute,
		Refresh: func(_ context.Context, provider, _ string) (auth.Credential, error) {
			if provider == "chatgpt" {
				return auth.Credential{}, errors.New("invalid_grant: token revoked")
			}
			return auth.Credential{}, errors.New("connection reset")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := refresher.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, err := broker.auth.Accounts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	status := make(map[string]string)
	for _, account := range accounts {
		status[account.ID] = account.Status
	}
	if status["revoked"] != "disabled" || status["transient"] != "active" {
		t.Fatalf("refreshed statuses=%#v", status)
	}
}

func brokerTestServer(t *testing.T, ctx context.Context) (*Server, *sqlitestore.Provider, func()) {
	t.Helper()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(store.DB(), auth.NewSQLiteStore(store.DB()), nil, nil)
	broker, err := New(Options{DB: store.DB(), Auth: authService, Tokens: []string{testToken}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return broker, store, func() { _ = store.Close(context.Background()) }
}

func brokerRequest(t *testing.T, method, url string, value any) *http.Response {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, url, body)
	request.Header.Set("Authorization", "Bearer "+testToken)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(response *http.Response) string {
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	return string(payload)
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
