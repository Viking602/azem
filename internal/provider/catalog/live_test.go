//go:build live

package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestLiveSubscriptionCatalogs(t *testing.T) {
	if os.Getenv("AZEM_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set AZEM_LIVE_ACCEPTANCE=1 to use local subscription credentials")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(provider.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	service := NewService(provider.DB(), authentication)

	t.Run("chatgpt", func(t *testing.T) {
		account, err := authentication.ImportChatGPT(ctx, filepath.Join(home, ".codex", "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.List(ctx, "chatgpt", account.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Models) == 0 {
			t.Fatal("ChatGPT subscription catalog returned no models")
		}
		t.Logf("ChatGPT subscription catalog: %d models", len(result.Models))
	})

	t.Run("grok", func(t *testing.T) {
		account, err := authentication.ImportGrok(ctx, filepath.Join(home, ".grok", "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.List(ctx, "grok", account.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Models) == 0 {
			t.Fatal("Grok subscription catalog returned no models")
		}
		t.Logf("Grok subscription catalog: %d models", len(result.Models))
	})
}
