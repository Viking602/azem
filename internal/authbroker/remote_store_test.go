package authbroker

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestRemoteStoreSynchronizesAccountsAndNeverExposesRefreshTokens(t *testing.T) {
	ctx := context.Background()
	broker, _, closeBroker := brokerTestServer(t, ctx)
	defer closeBroker()
	if _, err := broker.auth.StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "remote", AccessToken: "access", RefreshToken: "refresh", Email: "remote@example.com", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker.Handler())
	defer server.Close()
	client, err := NewClient(ClientOptions{BaseURL: server.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	local, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close(ctx)
	remote := &RemoteStore{Client: client, DB: local.DB()}
	if _, err := remote.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(local.DB(), remote, nil, nil)
	accounts, err := authentication.Accounts(ctx, "chatgpt")
	if err != nil || len(accounts) != 1 || accounts[0].CredentialRef == "" {
		t.Fatalf("remote accounts=%#v error=%v", accounts, err)
	}
	credential, err := authentication.Credential(ctx, "chatgpt", "remote")
	if err != nil || credential.AccessToken != "access" || credential.RefreshToken != "" {
		t.Fatalf("remote credential=%#v error=%v", credential, err)
	}
	if _, err := remote.Put(ctx, auth.Credential{}); err == nil {
		t.Fatal("remote store accepted local write")
	}
	if err := remote.Delete(ctx, "chatgpt", "remote"); err == nil {
		t.Fatal("remote store accepted local delete")
	}
}
