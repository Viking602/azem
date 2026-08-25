package authgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/authbroker"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

const (
	brokerToken  = "broker-token-abcdefghijklmnopqrstuvwxyz-0123456789"
	gatewayToken = "gateway-token-abcdefghijklmnopqrstuvwxyz-0123456789"
)

func TestGatewayFiltersModelsAndStreamsWithBrokerCredential(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	authService := auth.NewService(store.DB(), auth.NewSQLiteStore(store.DB()), nil, nil)
	if _, err := authService.StoreCredential(ctx, auth.Credential{Provider: "openai", AccountID: "account", AccessToken: "provider-access-secret", ExpiresAt: time.Now().Add(time.Hour), Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	broker, err := authbroker.New(authbroker.Options{DB: store.DB(), Auth: authService, Tokens: []string{brokerToken}})
	if err != nil {
		t.Fatal(err)
	}
	brokerServer := httptest.NewServer(broker.Handler())
	defer brokerServer.Close()
	brokerClient, err := authbroker.NewClient(authbroker.ClientOptions{BaseURL: brokerServer.URL, Token: brokerToken})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var upstreamAuthorization, upstreamPath, upstreamBody, leakedGatewayHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		upstreamAuthorization = request.Header.Get("Authorization")
		upstreamPath = request.URL.Path
		upstreamBody = string(body)
		leakedGatewayHeader = request.Header.Get("X-Azem-Provider")
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("X-Request-ID", "upstream-request")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer upstream.Close()
	gateway, err := New(Options{
		Broker: brokerClient, Token: gatewayToken,
		Routes: []Route{{Provider: "openai", BaseURL: upstream.URL + "/v1", Models: []string{"gpt-test"}, Protocols: []string{"openai-chat"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gatewayServer := httptest.NewServer(gateway.Handler())
	defer gatewayServer.Close()
	response, err := http.Get(gatewayServer.URL + "/healthz")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%v error=%v", status(response), err)
	}
	response.Body.Close()
	response, _ = http.Get(gatewayServer.URL + "/v1/models")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized models status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = gatewayRequest(t, http.MethodGet, gatewayServer.URL+"/v1/models", nil)
	var models struct {
		Data []map[string]any `json:"data"`
	}
	decodeGatewayResponse(t, response, &models)
	if len(models.Data) != 1 || models.Data[0]["id"] != "gpt-test" {
		t.Fatalf("models=%#v", models)
	}
	response = gatewayRequest(t, http.MethodPost, gatewayServer.URL+"/v1/chat/completions", map[string]any{"model": "gpt-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	payload, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(payload) != "data: {\"choices\":[]}\n\n" || response.Header.Get("X-Request-ID") != "upstream-request" {
		t.Fatalf("proxy status=%d headers=%v body=%q", response.StatusCode, response.Header, payload)
	}
	mu.Lock()
	defer mu.Unlock()
	if upstreamAuthorization != "Bearer provider-access-secret" || upstreamPath != "/v1/chat/completions" || !strings.Contains(upstreamBody, `"model":"gpt-test"`) || leakedGatewayHeader != "" || strings.Contains(upstreamBody, gatewayToken) {
		t.Fatalf("upstream auth=%q path=%q body=%q leaked=%q", upstreamAuthorization, upstreamPath, upstreamBody, leakedGatewayHeader)
	}
}

func TestGatewayHonorsBrokerBlocksProtocolRoutesAndAccountSelection(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	authService := auth.NewService(store.DB(), auth.NewSQLiteStore(store.DB()), nil, nil)
	if _, err := authService.StoreCredential(ctx, auth.Credential{Provider: "anthropic", AccountID: "a", AccessToken: "token-a", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	broker, _ := authbroker.New(authbroker.Options{DB: store.DB(), Auth: authService, Tokens: []string{brokerToken}})
	brokerServer := httptest.NewServer(broker.Handler())
	defer brokerServer.Close()
	client, _ := authbroker.NewClient(authbroker.ClientOptions{BaseURL: brokerServer.URL, Token: brokerToken})
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	gateway, err := New(Options{Broker: client, Token: gatewayToken, Routes: []Route{{Provider: "anthropic", BaseURL: upstream.URL, Models: []string{"claude-test"}, Protocols: []string{"anthropic-messages"}, AuthHeader: "X-Api-Key"}}})
	if err != nil {
		t.Fatal(err)
	}
	gatewayServer := httptest.NewServer(gateway.Handler())
	defer gatewayServer.Close()
	response := gatewayRequest(t, http.MethodPost, gatewayServer.URL+"/v1/chat/completions", map[string]any{"model": "claude-test"})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("protocol mismatch status=%d", response.StatusCode)
	}
	response.Body.Close()
	snapshot, err := client.FetchSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockURL := brokerServer.URL + "/v1/credential/" + snapshot.Credentials[0].ID + "/block"
	brokerResponse := brokerHTTP(t, http.MethodPost, blockURL, map[string]any{"scope": "shared", "blockedUntil": time.Now().Add(time.Hour), "reason": "limit"})
	if brokerResponse.StatusCode != http.StatusOK {
		t.Fatalf("block status=%d", brokerResponse.StatusCode)
	}
	brokerResponse.Body.Close()
	response = gatewayRequest(t, http.MethodPost, gatewayServer.URL+"/v1/messages", map[string]any{"model": "claude-test", "messages": []any{}})
	if response.StatusCode != http.StatusUnauthorized || upstreamCalls != 0 {
		t.Fatalf("blocked status=%d upstream=%d", response.StatusCode, upstreamCalls)
	}
	response.Body.Close()
}

func gatewayRequest(t *testing.T, method, url string, value any) *http.Response {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, _ := json.Marshal(value)
		body = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, url, body)
	request.Header.Set("Authorization", "Bearer "+gatewayToken)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func brokerHTTP(t *testing.T, method, url string, value any) *http.Response {
	t.Helper()
	encoded, _ := json.Marshal(value)
	request, _ := http.NewRequest(method, url, bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+brokerToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeGatewayResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func status(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}
