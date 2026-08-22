package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth/chatgpt"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestDecodeCursorUsageSummaryProjectsTotalAndBreakdown(t *testing.T) {
	quota, err := decodeCursorUsageSummary([]byte(`{
		"membershipType":"ultra",
		"billingCycleStart":"2026-08-01T00:00:00Z",
		"billingCycleEnd":"2026-09-01T00:00:00Z",
		"individualUsage":{
			"plan":{"autoPercentUsed":16,"apiPercentUsed":74,"totalPercentUsed":28,"limit":2000,"used":560},
			"onDemand":{"enabled":true,"limit":5000,"used":250,"remaining":4750}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.Plan != "ultra" || quota.Period != "monthly" || quota.UsedPercent != 28 || quota.Balance != "47.50" || quota.StartsAt == 0 || quota.ResetsAt <= quota.StartsAt {
		t.Fatalf("quota = %+v", quota)
	}
	if len(quota.Breakdown) != 2 ||
		quota.Breakdown[0] != (SubscriptionQuotaBreakdown{ID: "cursor", UsedPercent: 16}) ||
		quota.Breakdown[1] != (SubscriptionQuotaBreakdown{ID: "third_party", UsedPercent: 74}) {
		t.Fatalf("breakdown = %+v", quota.Breakdown)
	}
}

func TestDecodeCursorUsageSummaryFallsBackToOverallCents(t *testing.T) {
	quota, err := decodeCursorUsageSummary([]byte(`{
		"individualUsage":{"overall":{"used":250,"limit":1000,"remaining":750}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.UsedPercent != 25 || quota.Period != "monthly" {
		t.Fatalf("quota = %+v", quota)
	}
}

func TestDecodeCursorAuthUsageReadsRequestLimit(t *testing.T) {
	quota, err := decodeCursorAuthUsage([]byte(`{
		"startOfMonth":"2026-08-01T00:00:00Z",
		"gpt-4":{"numRequests":25,"maxRequestUsage":500}
	}`))
	if err != nil || quota.UsedPercent != 5 || quota.Period != "monthly" || quota.StartsAt == 0 || quota.ResetsAt <= quota.StartsAt {
		t.Fatalf("quota = %+v err=%v", quota, err)
	}
}

func TestCursorQuotaUsesDashboardSummaryCookie(t *testing.T) {
	ctx := context.Background()
	storeDB, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close(ctx)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	access := cursorQuotaJWT("auth0|user-1", "owner@example.com")
	var cookie string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/summary":
			cookie = request.Header.Get("Cookie")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"membershipType":    "ultra",
				"billingCycleStart": "2026-08-01T00:00:00Z",
				"billingCycleEnd":   "2026-09-01T00:00:00Z",
				"individualUsage": map[string]any{"plan": map[string]any{
					"autoPercentUsed": 18.25, "apiPercentUsed": 62.5, "totalPercentUsed": 40.375,
				}},
			})
		case "/me":
			_ = json.NewEncoder(writer).Encode(map[string]any{"sub": "user-1", "email": "owner@example.com"})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	service := NewService(storeDB.DB(), store, chatgpt.NewClient(), nil)
	service.CursorSummaryURL = server.URL + "/summary"
	service.CursorMeURL = server.URL + "/me"
	if _, err := store.Put(ctx, Credential{Provider: "cursor", AccountID: "user-1", AccessToken: access, RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := storeDB.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"user-1", "cursor", "", "", "", "file:cursor:user-1", "active", now, now); err != nil {
		t.Fatal(err)
	}
	quota, err := service.SubscriptionQuota(ctx, "cursor", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if quota.UsedPercent != 40.375 || quota.Period != "monthly" || quota.Email != "owner@example.com" || quota.StartsAt == 0 || len(quota.Breakdown) != 2 {
		t.Fatalf("quota = %+v", quota)
	}
	wantCookie := "WorkosCursorSessionToken=" + url.QueryEscape("user-1::"+access)
	if cookie != wantCookie {
		t.Fatalf("cookie = %q, want %q", cookie, wantCookie)
	}
}

func TestCursorQuotaFallsBackToAuthUsage(t *testing.T) {
	ctx := context.Background()
	storeDB, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close(ctx)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/usage" && request.Header.Get("Authorization") == "Bearer access" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"planUsage": map[string]any{"numRequests": 10, "maxRequestUsage": 40}})
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	service := NewService(storeDB.DB(), store, chatgpt.NewClient(), nil)
	service.CursorSummaryURL = server.URL + "/missing-summary"
	service.CursorUsageURL = server.URL + "/usage"
	if _, err := store.Put(ctx, Credential{Provider: "cursor", AccountID: "user-2", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := storeDB.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"user-2", "cursor", "", "", "", "file:cursor:user-2", "active", now, now); err != nil {
		t.Fatal(err)
	}
	quota, err := service.SubscriptionQuota(ctx, "cursor", "user-2")
	if err != nil || quota.UsedPercent != 25 {
		t.Fatalf("quota = %+v err=%v", quota, err)
	}
}

func cursorQuotaJWT(sub, email string) string {
	payload, _ := json.Marshal(map[string]any{"sub": sub, "email": email, "exp": time.Now().Add(time.Hour).Unix()})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
