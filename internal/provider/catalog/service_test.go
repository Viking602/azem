package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestCatalogCachingETagAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	secrets, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []string{"account-one", "account-two"} {
		if _, err := secrets.Put(ctx, auth.Credential{Provider: "chatgpt", AccountID: accountID, AccessToken: "token-" + accountID}); err != nil {
			t.Fatal(err)
		}
	}
	authentication := auth.NewService(provider.DB(), secrets, chatgpt.NewClient(), grok.NewClient())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		accountID := request.Header.Get("ChatGPT-Account-ID")
		if request.Header.Get("Authorization") != "Bearer token-"+accountID {
			t.Errorf("authorization/account mismatch")
		}
		if request.URL.Query().Get("client_version") != DefaultChatGPTClientVersion ||
			request.Header.Get("originator") != "codex_cli_rs" {
			t.Errorf("ChatGPT catalog compatibility metadata=%q %q", request.URL.RawQuery, request.Header.Get("originator"))
		}
		if request.Header.Get("If-None-Match") == `"v1"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("ETag", `"v1"`)
		if accountID == "account-one" {
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-one","title":"GPT One","supported_reasoning_levels":["high"],"supports_tools":true}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-two","title":"GPT Two","supports_tools":true}]}`))
		}
	}))
	catalog := NewService(provider.DB(), authentication)
	catalog.Endpoints["chatgpt"] = server.URL
	catalog.TTL["chatgpt"] = time.Hour
	one, err := catalog.List(ctx, "chatgpt", "account-one", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Models) != 1 || one.Models[0].ID != "gpt-one" || !one.Models[0].SupportsReasoning {
		t.Fatalf("account one catalog=%+v", one)
	}
	if _, err := catalog.List(ctx, "chatgpt", "account-one", false); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("fresh cache fetched again; calls=%d", calls.Load())
	}
	if _, err := catalog.List(ctx, "chatgpt", "account-one", true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("forced ETag refresh calls=%d", calls.Load())
	}
	two, err := catalog.List(ctx, "chatgpt", "account-two", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Models) != 1 || two.Models[0].ID != "gpt-two" {
		t.Fatalf("account two catalog=%+v", two)
	}
	if err := catalog.ValidateSelection(ctx, "chatgpt", "account-two", "gpt-one"); err == nil {
		t.Fatal("cross-account model selection accepted")
	}
	server.Close()
	stale, err := catalog.List(ctx, "chatgpt", "account-one", true)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.Warning == "" || stale.Models[0].ID != "gpt-one" {
		t.Fatalf("stale fallback=%+v", stale)
	}
}

func TestGrokCatalogDecode(t *testing.T) {
	models, more, after, err := decode("grok", []byte(`{"data":[{"id":"grok-code","capabilities":["tools","reasoning"]}],"has_more":true,"last_id":"cursor"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || !models[0].SupportsTools || !models[0].SupportsReasoning || !more || after != "cursor" {
		t.Fatalf("models=%+v more=%v after=%q", models, more, after)
	}
}

func TestPickerPreservesAccountScope(t *testing.T) {
	picker, err := NewPicker(Result{Provider: "chatgpt", AccountID: "acct", Models: []Model{{ID: "a"}, {ID: "b"}}}, "b")
	if err != nil {
		t.Fatal(err)
	}
	model, err := picker.Select("chatgpt", "acct")
	if err != nil || model.ID != "b" {
		t.Fatalf("model=%+v error=%v", model, err)
	}
	picker.Move(1)
	model, err = picker.Select("chatgpt", "acct")
	if err != nil || model.ID != "a" {
		t.Fatalf("wrapped model=%+v error=%v", model, err)
	}
	if _, err := picker.Select("chatgpt", "other"); err == nil {
		t.Fatal("cross-account picker selection accepted")
	}
}

func TestReasoningLevelsFollowCatalogAndProviderCapabilities(t *testing.T) {
	chatGPT := Model{
		ID:                "gpt-reasoning",
		SupportsReasoning: true,
		ReasoningLevels:   []string{"low", "high"},
		DefaultReasoning:  "low",
	}
	if got := strings.Join(AvailableReasoningLevels("chatgpt", chatGPT), ","); got != "low,high" {
		t.Fatalf("ChatGPT reasoning levels = %q", got)
	}
	if got := PreferredReasoningLevel("chatgpt", chatGPT); got != "low" {
		t.Fatalf("ChatGPT preferred reasoning = %q", got)
	}

	grok := Model{ID: "grok-4.5"}
	if got := strings.Join(AvailableReasoningLevels("grok", grok), ","); got != "low,medium,high" {
		t.Fatalf("Grok reasoning levels = %q", got)
	}
	if got := PreferredReasoningLevel("grok", grok); got != "high" {
		t.Fatalf("Grok preferred reasoning = %q", got)
	}
	if _, err := ResolveReasoningEffort("grok", grok, "minimal"); err == nil {
		t.Fatal("Grok accepted unsupported minimal reasoning")
	}

	multiAgent := Model{ID: "grok-4.20-multi-agent"}
	if got := strings.Join(AvailableReasoningLevels("grok", multiAgent), ","); got != "low,medium,high,xhigh" {
		t.Fatalf("Grok multi-agent reasoning levels = %q", got)
	}
	if got, err := ResolveReasoningEffort("grok", multiAgent, "xhigh"); err != nil || got != "xhigh" {
		t.Fatalf("Grok multi-agent xhigh = %q, %v", got, err)
	}

	if got, err := ResolveReasoningEffort("chatgpt", Model{ID: "plain"}, "high"); err != nil || got != "" {
		t.Fatalf("non-reasoning model effort = %q, %v", got, err)
	}
}
