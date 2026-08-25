package githubwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

const webhookSecret = "webhook-secret-abcdefghijklmnopqrstuvwxyz-0123456789"

type fakeTrigger struct {
	mu      sync.Mutex
	numbers []int
	err     error
}

func (trigger *fakeTrigger) Trigger(number int) error {
	trigger.mu.Lock()
	defer trigger.mu.Unlock()
	trigger.numbers = append(trigger.numbers, number)
	return trigger.err
}

func TestWebhookVerifiesSignatureAllowlistDeduplicatesAndQueuesFailure(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "webhook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	trigger := &fakeTrigger{}
	service, err := New(Options{DB: store.DB(), Secret: webhookSecret, Repositories: []string{"owner/repo"}, Trigger: trigger})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	payload := map[string]any{
		"action": "completed", "repository": map[string]any{"full_name": "owner/repo"},
		"check_run": map[string]any{"conclusion": "failure", "pull_requests": []map[string]int{{"number": 42}}},
	}
	response := webhookRequest(t, server.URL, "delivery-1", "check_run", payload, webhookSecret)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("accepted status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = webhookRequest(t, server.URL, "delivery-1", "check_run", payload, webhookSecret)
	var duplicate map[string]any
	decode(t, response, &duplicate)
	if duplicate["duplicate"] != true {
		t.Fatalf("duplicate response=%#v", duplicate)
	}
	trigger.mu.Lock()
	if len(trigger.numbers) != 1 || trigger.numbers[0] != 42 {
		t.Fatalf("triggered=%#v", trigger.numbers)
	}
	trigger.mu.Unlock()
	receipts, err := service.Receipts(ctx, 10)
	if err != nil || len(receipts) != 1 || receipts[0].Status != "queued" || receipts[0].Number != 42 {
		t.Fatalf("receipts=%#v error=%v", receipts, err)
	}
}

func TestWebhookRejectsInvalidSignatureRepositoryAndDisabledMonitor(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "webhook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	trigger := &fakeTrigger{err: errors.New("pull request is not monitored")}
	service, _ := New(Options{DB: store.DB(), Secret: webhookSecret, Repositories: []string{"owner/repo"}, Trigger: trigger})
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	payload := map[string]any{"action": "synchronize", "repository": map[string]any{"full_name": "owner/repo"}, "pull_request": map[string]int{"number": 7}}
	response := webhookRequest(t, server.URL, "bad-signature", "pull_request", payload, "wrong-secret-abcdefghijklmnopqrstuvwxyz")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signature status=%d", response.StatusCode)
	}
	response.Body.Close()
	foreign := map[string]any{"action": "synchronize", "repository": map[string]any{"full_name": "other/repo"}, "pull_request": map[string]int{"number": 7}}
	response = webhookRequest(t, server.URL, "foreign", "pull_request", foreign, webhookSecret)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("repository status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = webhookRequest(t, server.URL, "disabled", "pull_request", payload, webhookSecret)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("disabled monitor status=%d", response.StatusCode)
	}
	response.Body.Close()
	receipts, err := service.Receipts(ctx, 10)
	if err != nil || len(receipts) != 1 || receipts[0].Status != "rejected" {
		t.Fatalf("rejected receipts=%#v error=%v", receipts, err)
	}
}

func TestWebhookIgnoresSuccessfulChecksAndHealthNeedsNoSecret(t *testing.T) {
	ctx := context.Background()
	store, _ := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "webhook.db"))
	defer store.Close(ctx)
	trigger := &fakeTrigger{}
	service, _ := New(Options{DB: store.DB(), Secret: webhookSecret, Repositories: []string{"owner/repo"}, Trigger: trigger})
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/healthz")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%v error=%v", response.StatusCode, err)
	}
	response.Body.Close()
	payload := map[string]any{"action": "completed", "repository": map[string]any{"full_name": "owner/repo"}, "check_suite": map[string]any{"conclusion": "success", "pull_requests": []map[string]int{{"number": 4}}}}
	response = webhookRequest(t, server.URL, "success", "check_suite", payload, webhookSecret)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("success status=%d", response.StatusCode)
	}
	response.Body.Close()
	trigger.mu.Lock()
	defer trigger.mu.Unlock()
	if len(trigger.numbers) != 0 {
		t.Fatalf("successful check triggered=%#v", trigger.numbers)
	}
}

func webhookRequest(t *testing.T, serverURL, delivery, event string, value any, secret string) *http.Response {
	t.Helper()
	payload, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	request, _ := http.NewRequest(http.MethodPost, serverURL+"/webhook", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Delivery", delivery)
	request.Header.Set("X-GitHub-Event", event)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
