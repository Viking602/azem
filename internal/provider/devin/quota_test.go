package devin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuotaUsesPersonalSessionAndReportedWindows(t *testing.T) {
	var plan, status, user, response proto
	plan.text(2, "Pro")
	status.data(1, plan)
	status.number(14, 75)
	// Zero weekly remaining is omitted by proto3, but its reset is reported.
	status.number(17, 2200000000)
	status.number(18, 2200500000)
	status.number(16, 4200500000)
	user.data(13, status)
	response.data(1, user)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != quotaPath || r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Error("wrong quota protocol")
		}
		raw, _ := io.ReadAll(r.Body)
		metadata := decodeProto(raw, nil).child(1)
		if metadata.text(3) != "devin-session-token$personal" || metadata.text(1) != "devin-cli" {
			t.Error("quota did not use personal CLI authentication")
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()
	quota, err := fetchQuota(context.Background(), server.Client(), server.URL, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if quota.Plan != "Pro" || quota.Period != "weekly" || quota.UsedPercent != 100 || quota.ResetsAt != 2200500000 || quota.Balance != "4200.50" || len(quota.Breakdown) != 2 || quota.Breakdown[0].UsedPercent != 25 {
		t.Fatalf("quota = %+v", quota)
	}
	if _, err := decodeQuota(decodeProto(nil, nil)); err == nil {
		t.Fatal("missing quota became zero usage")
	}
	status.number(14, 101)
	user = nil
	user.data(13, status)
	response = nil
	response.data(1, user)
	if _, err := decodeQuota(decodeProto(response, nil)); err == nil {
		t.Fatal("invalid percent accepted")
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("private-provider-error"))
	}))
	defer denied.Close()
	_, err = fetchQuota(context.Background(), denied.Client(), denied.URL, "personal")
	if err == nil || strings.Contains(err.Error(), "private-provider-error") {
		t.Fatalf("unsafe quota error = %v", err)
	}
}
