package sessionshare

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type fakeGistPublisher struct {
	content string
	url     string
	id      string
	err     error
}

func (publisher *fakeGistPublisher) Publish(_ context.Context, _, content string) (string, string, error) {
	publisher.content = content
	return publisher.url, publisher.id, publisher.err
}

func TestEncryptedShareKeepsKeyInFragmentAndRedactsBeforeUpload(t *testing.T) {
	ctx := context.Background()
	service, closeStore := shareTestSession(t, ctx)
	defer closeStore()
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Fragment != "" || request.URL.RawQuery != "" || strings.Contains(request.RequestURI, "#") {
			t.Errorf("share request=%s %s", request.Method, request.RequestURI)
		}
		if request.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("content type=%q", request.Header.Get("Content-Type"))
		}
		uploaded, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"share_123"}`))
	}))
	defer server.Close()
	client := New(service)
	client.Now = func() time.Time { return time.Unix(100, 0).UTC() }
	result, err := client.Share(ctx, "session", Options{ServerURL: server.URL + "/s", Secrets: []string{"custom-secret-value"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Method != "server" || result.SealedBytes != len(uploaded) || result.Truncated {
		t.Fatalf("share result=%#v", result)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/s/share_123" || parsed.Fragment == "" {
		t.Fatalf("share URL=%q", result.URL)
	}
	key, err := DecodeKey(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	document, err := Open(uploaded, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	body := string(encoded)
	for _, secret := range []string{"super-secret-value", "custom-secret-value", "ghp_1234567890abcdefghijkl", "/private/source.jsonl"} {
		if strings.Contains(body, secret) {
			t.Fatalf("redacted share leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") || !document.SharedAt.Equal(time.Unix(100, 0).UTC()) || document.Session.Tree.SourceRef != "" || document.Session.Tree.PromptCacheKey != "" {
		t.Fatalf("redacted document=%#v", document)
	}
	if bytesContain(uploaded, []byte("super-secret-value")) {
		t.Fatal("sealed upload contains plaintext secret")
	}
	tampered := append([]byte(nil), uploaded...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Open(tampered, key); err == nil {
		t.Fatal("tampered share authenticated")
	}
}

func TestEncryptedShareCanExplicitlyDisableRedaction(t *testing.T) {
	ctx := context.Background()
	service, closeStore := shareTestSession(t, ctx)
	defer closeStore()
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		uploaded, _ = io.ReadAll(request.Body)
		_, _ = writer.Write([]byte(`{"id":"raw_share"}`))
	}))
	defer server.Close()
	result, err := New(service).Share(ctx, "session", Options{ServerURL: server.URL, DisableRedaction: true})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(result.URL)
	key, _ := DecodeKey(parsed.Fragment)
	document, err := Open(uploaded, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	if !strings.Contains(string(encoded), "super-secret-value") || !strings.Contains(string(encoded), "/private/source.jsonl") {
		t.Fatalf("explicit raw share was redacted: %s", encoded)
	}
}

func TestGistShareAndFailureFallbackUseSameEncryptedEnvelope(t *testing.T) {
	ctx := context.Background()
	service, closeStore := shareTestSession(t, ctx)
	defer closeStore()
	var serverUploads int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serverUploads++
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = writer.Write([]byte(`{"id":"fallback"}`))
	}))
	defer server.Close()
	publisher := &fakeGistPublisher{url: "https://gist.github.com/user/abcdef1234567890abcdef", id: "abcdef1234567890abcdef"}
	client := New(service)
	client.Gists = publisher
	result, err := client.Share(ctx, "session", Options{ServerURL: server.URL, Store: StoreGist})
	if err != nil {
		t.Fatal(err)
	}
	if result.Method != "gist" || result.GistURL != publisher.url || serverUploads != 0 {
		t.Fatalf("gist result=%#v uploads=%d", result, serverUploads)
	}
	sealed, err := base64.StdEncoding.DecodeString(publisher.content)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(result.URL)
	key, _ := DecodeKey(parsed.Fragment)
	if _, err := Open(sealed, key); err != nil {
		t.Fatalf("open gist envelope: %v", err)
	}
	publisher.err = errors.New("gh unavailable")
	publisher.content = ""
	result, err = client.Share(ctx, "session", Options{ServerURL: server.URL, Store: StoreGist})
	if err != nil {
		t.Fatal(err)
	}
	if result.Method != "server" || serverUploads != 1 {
		t.Fatalf("fallback result=%#v uploads=%d", result, serverUploads)
	}
}

func TestShareTrimsIncompressibleContentToServerBudget(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := session.NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, session.Session{ID: "large", Title: "Large"}); err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 2<<20)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	content := base64.RawStdEncoding.EncodeToString(random)
	if _, err := service.AppendBlock(ctx, "large", session.Block{Kind: "user", Content: content}); err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		uploaded, _ = io.ReadAll(request.Body)
		_, _ = writer.Write([]byte(`{"id":"trimmed"}`))
	}))
	defer server.Close()
	result, err := New(service).Share(ctx, "large", Options{ServerURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(uploaded) > ServerMaxSealedBytes {
		t.Fatalf("trim result=%#v bytes=%d", result, len(uploaded))
	}
	parsed, _ := url.Parse(result.URL)
	key, _ := DecodeKey(parsed.Fragment)
	document, err := Open(uploaded, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Session.Blocks) != 1 || !strings.Contains(document.Session.Blocks[0].Content, "[truncated for share]") {
		t.Fatalf("trimmed document=%#v", document)
	}
}

func TestShareRejectsInsecureRemoteURLAndDoesNotFollowRedirects(t *testing.T) {
	ctx := context.Background()
	service, closeStore := shareTestSession(t, ctx)
	defer closeStore()
	if _, err := New(service).Share(ctx, "session", Options{ServerURL: "http://example.com/share"}); err == nil {
		t.Fatal("accepted insecure remote share URL")
	}
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	if _, err := New(service).Share(ctx, "session", Options{ServerURL: redirector.URL}); err == nil {
		t.Fatal("accepted share redirect")
	}
	if redirected {
		t.Fatal("followed share redirect")
	}
}

func shareTestSession(t *testing.T, ctx context.Context) (*session.Service, func()) {
	t.Helper()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := session.NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, session.Session{ID: "session", Title: "ghp_1234567890abcdefghijkl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "password=super-secret-value custom-secret-value"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", session.Block{Kind: "assistant", Content: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE session_graphs SET source_kind='claude',source_ref='/private/source.jsonl' WHERE session_id='session'`); err != nil {
		t.Fatal(err)
	}
	started, err := service.StartToolRecordAt(ctx, "session", session.ToolRecord{RunID: "run", ToolCallID: "call", Name: "read", Arguments: json.RawMessage(`{"api_key":"custom-secret-value"}`), StartedAt: time.Unix(1, 0)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	started.State = session.ToolCompleted
	started.Content = "authorization: bearer custom-secret-value"
	started.CompletedAt = time.Unix(2, 0)
	if _, err := service.FinishToolRecord(ctx, "session", started); err != nil {
		t.Fatal(err)
	}
	return service, func() { _ = store.Close(context.Background()) }
}

func bytesContain(value, fragment []byte) bool {
	return strings.Contains(string(value), string(fragment))
}
