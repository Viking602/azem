package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"resty.dev/v3"
)

const (
	DefaultChatGPTQuotaURL    = "https://chatgpt.com/backend-api/wham/usage"
	DefaultGrokQuotaURL       = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	defaultGrokClientVersion  = "0.2.121"
	subscriptionQuotaBodySize = 1 << 20
)

type SubscriptionQuota struct {
	Plan        string
	UsedPercent float64
	ResetsAt    int64
	Balance     string
	Unlimited   bool
}

func (s *Service) SubscriptionQuota(ctx context.Context, provider, accountID string) (SubscriptionQuota, error) {
	var endpoint string
	configure := func(request *resty.Request) { request.SetResponseBodyLimit(subscriptionQuotaBodySize) }
	switch provider {
	case "chatgpt":
		endpoint = DefaultChatGPTQuotaURL
		configure = func(request *resty.Request) {
			request.SetResponseBodyLimit(subscriptionQuotaBodySize).
				SetHeader("Accept", "application/json").
				SetHeader("ChatGPT-Account-ID", accountID).
				SetHeader("OpenAI-Beta", "codex-1").
				SetHeader("originator", "codex_cli_rs").
				SetHeader("User-Agent", "azem/1")
		}
	case "grok":
		endpoint = DefaultGrokQuotaURL
		configure = func(request *resty.Request) {
			request.SetResponseBodyLimit(subscriptionQuotaBodySize).
				SetHeader("X-XAI-Token-Auth", "xai-grok-cli").
				SetHeader("x-userid", accountID).
				SetHeader("x-grok-client-version", defaultGrokClientVersion).
				SetHeader("User-Agent", "azem/1")
		}
	default:
		return SubscriptionQuota{}, fmt.Errorf("subscription quota is unsupported for %q", provider)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := s.DoWithRefresh(requestCtx, provider, accountID, resty.MethodGet, endpoint, configure)
	if err != nil {
		return SubscriptionQuota{}, err
	}
	if response.StatusCode()/100 != 2 {
		return SubscriptionQuota{}, fmt.Errorf("%s quota returned HTTP %d", provider, response.StatusCode())
	}
	if provider == "chatgpt" {
		return decodeChatGPTQuota(response.Bytes())
	}
	return decodeGrokQuota(response.Bytes())
}

func decodeChatGPTQuota(data []byte) (SubscriptionQuota, error) {
	var payload struct {
		Plan      string `json:"plan_type"`
		RateLimit *struct {
			Primary   *chatGPTQuotaWindow `json:"primary_window"`
			Secondary *chatGPTQuotaWindow `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			Unlimited bool    `json:"unlimited"`
			Balance   *string `json:"balance"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return SubscriptionQuota{}, fmt.Errorf("decode ChatGPT quota: %w", err)
	}
	quota := SubscriptionQuota{Plan: payload.Plan}
	var weekly *chatGPTQuotaWindow
	if payload.RateLimit != nil {
		weekly = payload.RateLimit.Secondary
		if weekly == nil {
			weekly = payload.RateLimit.Primary
		}
	}
	if weekly == nil {
		return SubscriptionQuota{}, fmt.Errorf("ChatGPT quota response contained no weekly quota")
	}
	quota.UsedPercent = clampPercent(weekly.UsedPercent)
	quota.ResetsAt = weekly.ResetAt
	if payload.Credits != nil {
		quota.Unlimited = payload.Credits.Unlimited
		if payload.Credits.Balance != nil {
			quota.Balance = *payload.Credits.Balance
		}
	}
	return quota, nil
}

type chatGPTQuotaWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

func decodeGrokQuota(data []byte) (SubscriptionQuota, error) {
	type cent struct {
		Value int64 `json:"val"`
	}
	var payload struct {
		SubscriptionTier string `json:"subscription_tier"`
		Config           *struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      *struct {
				Type  string `json:"type"`
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"currentPeriod"`
			MonthlyLimit     *cent  `json:"monthlyLimit"`
			Used             *cent  `json:"used"`
			PrepaidBalance   *cent  `json:"prepaidBalance"`
			BillingPeriodEnd string `json:"billingPeriodEnd"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return SubscriptionQuota{}, fmt.Errorf("decode Grok quota: %w", err)
	}
	if payload.Config == nil {
		return SubscriptionQuota{}, fmt.Errorf("Grok quota response contained no quota data")
	}
	config := payload.Config
	usedPercent := 0.0
	hasUsage := false
	if config.CreditUsagePercent != nil {
		usedPercent = *config.CreditUsagePercent
		hasUsage = true
	} else if config.MonthlyLimit != nil && config.MonthlyLimit.Value > 0 && config.Used != nil {
		usedPercent = float64(config.Used.Value) / float64(config.MonthlyLimit.Value) * 100
		hasUsage = true
	}
	if !hasUsage {
		return SubscriptionQuota{}, fmt.Errorf("Grok quota response contained no weekly quota")
	}
	quota := SubscriptionQuota{Plan: payload.SubscriptionTier, UsedPercent: clampPercent(usedPercent)}
	if config.CurrentPeriod != nil {
		if end, err := time.Parse(time.RFC3339, config.CurrentPeriod.End); err == nil {
			quota.ResetsAt = end.Unix()
		}
	} else if end, err := time.Parse(time.RFC3339, config.BillingPeriodEnd); err == nil {
		quota.ResetsAt = end.Unix()
	}
	if config.PrepaidBalance != nil {
		quota.Balance = fmt.Sprintf("%.2f", float64(config.PrepaidBalance.Value)/100)
	}
	return quota, nil
}

func clampPercent(value float64) float64 { return max(0, min(100, value)) }
