package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestDevinAccountCatalogRoutesAndRefresh(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	authentication := auth.NewService(db.DB(), auth.NewSQLiteStore(db.DB()), nil, nil)
	account, err := authentication.SetAPIKey(ctx, "devin", "mock-personal-session")
	if err != nil {
		t.Fatal(err)
	}
	models := catalog.NewService(db.DB(), authentication)
	current := []catalog.Model{{ID: "model-high", Name: "Account model", ContextWindow: 200000, SupportsReasoning: true, SupportsTools: true, DevinRouter: true}}
	models.Fetchers["devin"] = func(ctx context.Context, accountID string) ([]catalog.Model, error) {
		if accountID != account.ID {
			t.Error("catalog requested another account")
		}
		if current == nil {
			return nil, fmt.Errorf("discovery failed")
		}
		return append([]catalog.Model(nil), current...), nil
	}
	runtime := &ProviderRuntime{cfg: config.Default(), auth: authentication, catalog: models}
	resolvedAccount, modelID, window, driver, err := runtime.resolveDriverForAccount(ctx, "devin", "model-high", "high", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedAccount.ID != account.ID || modelID != "model-high" || window != 200000 || driver.Metadata().Name != "devin-agent" || driver.Metadata().Version != "2" {
		t.Fatal("personal model routed through wrong account or driver")
	}
	cached, found, err := models.Cached(ctx, "devin", account.ID)
	if err != nil || !found || !cached.Models[0].DevinRouter {
		t.Fatal("model capabilities lost in persistence")
	}
	// No external metadata may invent structured-output support or effort controls.
	models.ModelsDevURL = ":invalid"
	enriched := models.EnrichWithModelsDev(ctx, cached)
	if enriched.Warning != "" || enriched.Models[0].Name != "Account model" || enriched.Models[0].SupportsStructured || len(catalog.AvailableReasoningLevels("devin", enriched.Models[0])) != 0 {
		t.Fatal("account catalog was changed by external metadata")
	}
	runtime.UpdateSubscriptionDisabledModels("devin", []string{"model-high"})
	if _, _, _, _, err := runtime.resolveDriver(ctx, "devin", "model-high", ""); err == nil {
		t.Fatal("disabled model remained routable")
	}
	if len(runtime.cfg.Providers.Cursor.DisabledModels) != 0 {
		t.Fatal("Devin switch modified Cursor")
	}
	runtime.UpdateSubscriptionDisabledModels("devin", nil)
	if _, _, _, _, err := runtime.resolveDriverForAccount(ctx, "devin", "model-high", "", "another-account"); err == nil {
		t.Fatal("account binding was ignored")
	}
	for i := 0; i < 5; i++ {
		current = append(current, catalog.Model{ID: fmt.Sprintf("new-%d", i), ContextWindow: 100000, SupportsTools: true})
	}
	if _, err := models.List(ctx, "devin", account.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := runtime.resolveDriver(ctx, "devin", "new-0", ""); err == nil {
		t.Fatal("discovery-disabled model remained routable")
	}
	if _, _, _, _, err := runtime.resolveDriver(ctx, "devin", "model-high", ""); err != nil {
		t.Fatalf("refresh disabled an existing selection: %v", err)
	}
	host := NewService(ctx, config.Default())
	host.AttachAuth(authentication, models)
	listed, _, _ := models.Cached(ctx, "devin", account.ID)
	if !host.catalogModelsWithAvailability("devin", listed.Models)[1].Disabled {
		t.Fatal("settings ignored discovery defaults")
	}
	if err := host.setSubscriptionModelsEnabled(ctx, "devin", []string{"new-0", "new-1"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := models.List(ctx, "devin", account.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := runtime.resolveDriver(ctx, "devin", "new-0", ""); err != nil {
		t.Fatalf("explicit enable did not survive refresh: %v", err)
	}
	current = []catalog.Model{{ID: "replacement", ContextWindow: 100000, SupportsTools: true}}
	if _, err := models.List(ctx, "devin", account.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := runtime.resolveDriver(ctx, "devin", "model-high", ""); err == nil {
		t.Fatal("model omitted by refresh remained routable")
	}
	current = nil
	stale, err := models.List(ctx, "devin", account.ID, true)
	if err != nil || !stale.Stale || stale.Warning == "" || len(stale.Models) != 1 || stale.Models[0].ID != "replacement" {
		t.Fatal("failed refresh did not preserve explicit last-successful cache")
	}
	if err := host.refreshOneSubscriptionCatalog(ctx, "devin", account.ID); err == nil {
		t.Fatal("login/manual refresh accepted stale models as fresh")
	}
}

func TestDevinQuotaUsesSharedAsyncProjection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	authentication := auth.NewService(db.DB(), auth.NewSQLiteStore(db.DB()), nil, nil)
	account, err := authentication.SetAPIKey(ctx, "devin", "mock-personal-session")
	if err != nil {
		t.Fatal(err)
	}
	host := NewService(ctx, config.Default())
	host.AttachAuth(authentication, nil)
	host.subscriptionQuotaLookup = func(context.Context, string, string) (auth.SubscriptionQuota, error) {
		return auth.SubscriptionQuota{Plan: "Pro", Period: "weekly", UsedPercent: 30, Breakdown: []auth.SubscriptionQuotaBreakdown{{ID: "daily", UsedPercent: 20, ResetsAt: 2200000000}}}, nil
	}
	entry := ModelProviderEntry{ID: "devin", Subscription: true, AccountID: account.ID}
	host.rememberModelProviderEntries([]ModelProviderEntry{entry})
	host.scheduleSubscriptionQuotaRefresh([]ModelProviderEntry{entry})
	deadline, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	for {
		event, err := host.NextEvent(deadline)
		if err != nil {
			t.Fatal(err)
		}
		if event.State != "quota_updated" {
			continue
		}
		p := event.ModelProviders[0]
		if !p.QuotaAvailable || p.AccountPlan != "Pro" || p.QuotaUsedPercent != 30 || len(p.QuotaBreakdown) != 1 || p.QuotaBreakdown[0].ResetsAt != 2200000000 {
			t.Fatalf("quota projection = %+v", p)
		}
		break
	}
}
