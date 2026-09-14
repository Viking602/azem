package devin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Viking602/azem/internal/auth"
)

const quotaPath = "/exa.seat_management_pb.SeatManagementService/GetUserStatus"

func FetchQuota(ctx context.Context, token string) (auth.SubscriptionQuota, error) {
	return fetchQuota(ctx, newHTTPClient(), DefaultAPIURL, token)
}

func fetchQuota(ctx context.Context, client *http.Client, base, token string) (auth.SubscriptionQuota, error) {
	if strings.TrimSpace(token) == "" {
		return auth.SubscriptionQuota{}, fmt.Errorf("Devin login is required")
	}
	var request proto
	request.data(1, metadata(token, "", false))
	response, err := unary(ctx, client, base+quotaPath, request)
	if err != nil {
		return auth.SubscriptionQuota{}, err
	}
	return decodeQuota(response)
}

func decodeQuota(response *protoMessage) (auth.SubscriptionQuota, error) {
	user := response.child(1)
	status := user.child(13)
	plan := status.child(1)
	if len(status.data(1)) == 0 {
		plan = response.child(2)
	}
	quota := auth.SubscriptionQuota{Plan: plan.text(2), Email: user.text(7), DisplayName: user.text(3), UserID: user.text(36)}
	for _, window := range []struct {
		id                       string
		remaining, reset, hidden int
	}{{"daily", 14, 17, 36}, {"weekly", 15, 18, 37}} {
		reset := status.integer64(window.reset)
		remaining := status.number(window.remaining)
		// Proto3 omits zero remaining. A reset identifies a real, exhausted window.
		if reset <= 0 || plan.number(window.hidden) != 0 {
			continue
		}
		if remaining < 0 || remaining > 100 {
			return quota, fmt.Errorf("Devin returned an invalid quota percentage")
		}
		used := float64(100 - remaining)
		quota.Breakdown = append(quota.Breakdown, auth.SubscriptionQuotaBreakdown{ID: window.id, UsedPercent: used, ResetsAt: reset})
		quota.Period, quota.UsedPercent, quota.ResetsAt = window.id, used, reset
	}
	if len(status.values(16, 0)) > 0 {
		quota.Balance = fmt.Sprintf("%.2f", float64(status.integer64(16))/1e6)
	}
	for _, message := range []*protoMessage{response, user, status, plan} {
		if message.err != nil {
			return quota, message.err
		}
	}
	if len(quota.Breakdown) == 0 {
		return quota, fmt.Errorf("Devin account did not report quota windows")
	}
	return quota, nil
}
