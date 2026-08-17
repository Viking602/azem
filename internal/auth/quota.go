package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"resty.dev/v3"

	"github.com/Viking602/azem/internal/auth/grok"
)

const (
	DefaultChatGPTQuotaURL    = "https://chatgpt.com/backend-api/wham/usage"
	DefaultGrokQuotaURL       = grok.DefaultQuotaURL
	DefaultGrokUserURL        = grok.DefaultUserURL
	subscriptionQuotaBodySize = 1 << 20
	grokUserBodySize          = 64 << 10
	chatgptQuotaTimeout       = 5 * time.Second
	grokQuotaTimeout          = 15 * time.Second
)

type SubscriptionQuota struct {
	Plan        string
	Period      string
	UsedPercent float64
	ResetsAt    int64
	Balance     string
	Unlimited   bool
	Email       string
	DisplayName string
	UserID      string
}

func (s *Service) SubscriptionQuota(ctx context.Context, provider, accountID string) (SubscriptionQuota, error) {
	switch provider {
	case "chatgpt":
		return s.chatGPTQuota(ctx, accountID)
	case "grok":
		return s.grokQuota(ctx, accountID)
	default:
		return SubscriptionQuota{}, fmt.Errorf("subscription quota is unsupported for %q", provider)
	}
}

func (s *Service) chatGPTQuota(ctx context.Context, accountID string) (SubscriptionQuota, error) {
	requestCtx, cancel := context.WithTimeout(ctx, chatgptQuotaTimeout)
	defer cancel()
	response, err := s.DoWithRefresh(requestCtx, "chatgpt", accountID, resty.MethodGet, DefaultChatGPTQuotaURL, func(request *resty.Request) {
		request.SetResponseBodyLimit(subscriptionQuotaBodySize).
			SetHeader("Accept", "application/json").
			SetHeader("ChatGPT-Account-ID", accountID).
			SetHeader("OpenAI-Beta", "codex-1").
			SetHeader("originator", "codex_cli_rs").
			SetHeader("User-Agent", "azem/1")
	})
	if err != nil {
		return SubscriptionQuota{}, err
	}
	if response.StatusCode()/100 != 2 {
		return SubscriptionQuota{}, quotaHTTPError("chatgpt", response.StatusCode(), response.Bytes())
	}
	return decodeChatGPTQuota(response.Bytes())
}

func (s *Service) grokQuota(ctx context.Context, accountID string) (SubscriptionQuota, error) {
	identity, err := s.grokIdentity(ctx, accountID)
	quota := SubscriptionQuota{Email: identity.Email, DisplayName: identity.DisplayName, Plan: identity.Plan, UserID: identity.UserID}
	_ = s.persistAccountProfile(ctx, "grok", accountID, identity.Email, identity.DisplayName, identity.Plan)
	if err != nil {
		return quota, err
	}
	if identity.UserID == "" {
		return quota, fmt.Errorf("Grok user lookup returned no user id")
	}
	requestCtx, cancel := context.WithTimeout(ctx, grokQuotaTimeout)
	defer cancel()
	response, err := s.grokProxyGET(requestCtx, accountID, s.grokQuotaURL(), func(request *resty.Request) {
		configureGrokProxyRequest(request, identity.UserID, subscriptionQuotaBodySize)
	})
	if err != nil {
		return quota, err
	}
	if response.StatusCode()/100 != 2 {
		return quota, quotaHTTPError("grok", response.StatusCode(), response.Bytes())
	}
	decoded, err := decodeGrokQuota(response.Bytes())
	if err != nil {
		return quota, err
	}
	decoded.Email = firstNonEmpty(decoded.Email, quota.Email)
	decoded.DisplayName = firstNonEmpty(decoded.DisplayName, quota.DisplayName)
	decoded.UserID = firstNonEmpty(decoded.UserID, quota.UserID)
	if decoded.Plan == "" {
		decoded.Plan = quota.Plan
	}
	return decoded, nil
}

func (s *Service) grokIdentity(ctx context.Context, accountID string) (grok.Identity, error) {
	identity := s.storedGrokIdentity(ctx, accountID)
	requestCtx, cancel := context.WithTimeout(ctx, grokQuotaTimeout)
	defer cancel()
	response, err := s.grokProxyGET(requestCtx, accountID, s.grokUserURL(), func(request *resty.Request) {
		configureGrokProxyRequest(request, "", grokUserBodySize)
	})
	if err != nil {
		return identity, err
	}
	if response.StatusCode()/100 != 2 {
		return identity, quotaHTTPError("grok user lookup", response.StatusCode(), response.Bytes())
	}
	info, err := grok.DecodeUserInfo(response.Bytes())
	if err != nil {
		return identity, err
	}
	return grok.Identity{
		UserID:      info.UserID,
		Email:       firstNonEmpty(info.Email, identity.Email),
		DisplayName: firstNonEmpty(info.DisplayName(), identity.DisplayName, info.Email),
		Plan:        firstNonEmpty(info.SubscriptionTier, identity.Plan),
	}, nil
}

func (s *Service) storedGrokIdentity(ctx context.Context, accountID string) grok.Identity {
	identity := grok.Identity{}
	if account, err := s.Account(ctx, "grok", accountID); err == nil {
		identity.Email = account.Email
		identity.DisplayName = firstNonEmpty(account.DisplayName, account.Email)
		identity.Plan = account.Plan
	}
	credential, err := s.store.Get(ctx, "grok", accountID)
	if err != nil {
		return identity
	}
	fromToken := grok.IdentityFromTokens(credential.IDToken, credential.AccessToken)
	identity.UserID = firstNonEmpty(fromToken.UserID)
	identity.Email = firstNonEmpty(identity.Email, credential.Email, fromToken.Email)
	identity.DisplayName = firstNonEmpty(identity.DisplayName, credential.DisplayName, fromToken.DisplayName, identity.Email)
	identity.Plan = firstNonEmpty(identity.Plan, credential.Plan, fromToken.Plan)
	return identity
}

func (s *Service) grokUserURL() string {
	if s != nil && strings.TrimSpace(s.GrokUserURL) != "" {
		return s.GrokUserURL
	}
	return DefaultGrokUserURL
}

func (s *Service) grokQuotaURL() string {
	if s != nil && strings.TrimSpace(s.GrokQuotaURL) != "" {
		return s.GrokQuotaURL
	}
	return DefaultGrokQuotaURL
}

func (s *Service) grokProxyGET(ctx context.Context, accountID, endpoint string, configure func(*resty.Request)) (*resty.Response, error) {
	response, err := s.DoWithRefresh(ctx, "grok", accountID, resty.MethodGet, endpoint, configure)
	if err == nil || !isTransientTransportError(err) {
		return response, err
	}
	s.closeIdleHTTP()
	return s.DoWithRefresh(ctx, "grok", accountID, resty.MethodGet, endpoint, configure)
}

func (s *Service) closeIdleHTTP() {
	if s == nil || s.httpClient == nil {
		return
	}
	if transport, err := s.httpClient.HTTPTransport(); err == nil {
		transport.CloseIdleConnections()
	}
}

func isTransientTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isTransientTransportError(urlErr.Err)
	}
	message := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset",
		"broken pipe",
		"use of closed network connection",
		"http2: stream closed",
		"http2: client conn not usable",
		"unexpected eof",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func configureGrokProxyRequest(request *resty.Request, userID string, bodyLimit int64) {
	request.SetResponseBodyLimit(bodyLimit).
		SetHeader("Accept", "application/json").
		SetHeader("X-XAI-Token-Auth", "xai-grok-cli").
		SetHeader("x-grok-client-version", grok.DefaultClientVersion).
		SetHeader("x-grok-client-mode", grok.ClientModeHeadless).
		SetHeader("User-Agent", "azem/1")
	if userID != "" {
		request.SetHeader("x-userid", userID)
	}
}

func quotaHTTPError(provider string, status int, body []byte) error {
	if detail := boundedJSONError(body); detail != "" {
		return fmt.Errorf("%s returned HTTP %d: %s", provider, status, detail)
	}
	return fmt.Errorf("%s returned HTTP %d", provider, status)
}

func boundedJSONError(body []byte) string {
	var payload struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error == nil {
		return ""
	}
	encoded, err := json.Marshal(payload.Error)
	if err != nil {
		return ""
	}
	if len(encoded) > 256 {
		encoded = encoded[:256]
	}
	return string(encoded)
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

type grokQuotaPeriodInfo struct {
	Type  string `json:"type"`
	Start string `json:"start"`
	End   string `json:"end"`
}

func decodeGrokQuota(data []byte) (SubscriptionQuota, error) {
	type cent struct {
		Value float64 `json:"val"`
	}
	var payload struct {
		SubscriptionTier string `json:"subscription_tier"`
		Config           *struct {
			CreditUsagePercent *float64             `json:"creditUsagePercent"`
			CurrentPeriod      *grokQuotaPeriodInfo `json:"currentPeriod"`
			MonthlyLimit       *cent                `json:"monthlyLimit"`
			Used               *cent                `json:"used"`
			OnDemandCap        *cent                `json:"onDemandCap"`
			OnDemandUsed       *cent                `json:"onDemandUsed"`
			PrepaidBalance     *cent                `json:"prepaidBalance"`
			BillingPeriodStart string               `json:"billingPeriodStart"`
			BillingPeriodEnd   string               `json:"billingPeriodEnd"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return SubscriptionQuota{}, fmt.Errorf("decode Grok quota: %w", err)
	}
	if payload.Config == nil {
		return SubscriptionQuota{}, fmt.Errorf("Grok quota response contained no quota data")
	}
	config := payload.Config
	resetsAt := parseGrokQuotaReset(config.CurrentPeriod, config.BillingPeriodEnd)
	usedPercent := 0.0
	hasUsage := false
	if config.CreditUsagePercent != nil {
		usedPercent = *config.CreditUsagePercent
		hasUsage = true
	} else if config.MonthlyLimit != nil && config.MonthlyLimit.Value > 0 && config.Used != nil {
		usedPercent = config.Used.Value / config.MonthlyLimit.Value * 100
		hasUsage = true
	} else if config.OnDemandCap != nil && config.OnDemandCap.Value > 0 && config.OnDemandUsed != nil {
		usedPercent = config.OnDemandUsed.Value / config.OnDemandCap.Value * 100
		hasUsage = true
	} else if resetsAt > 0 {
		// CodexBar and official proto3 omit creditUsagePercent when usage is zero.
		hasUsage = true
	}
	if !hasUsage {
		return SubscriptionQuota{}, fmt.Errorf("Grok quota response contained no usage or billing period")
	}
	quota := SubscriptionQuota{
		Plan:        payload.SubscriptionTier,
		Period:      grokQuotaPeriod(config.CurrentPeriod, config.BillingPeriodStart, config.BillingPeriodEnd, resetsAt),
		UsedPercent: clampPercent(usedPercent),
		ResetsAt:    resetsAt,
	}
	if config.PrepaidBalance != nil && config.PrepaidBalance.Value > 0 {
		quota.Balance = fmt.Sprintf("%.2f", config.PrepaidBalance.Value/100)
	}
	return quota, nil
}

func parseGrokQuotaReset(period *grokQuotaPeriodInfo, billingPeriodEnd string) int64 {
	if period != nil {
		if end := parseGrokQuotaTime(period.End); end > 0 {
			return end
		}
	}
	return parseGrokQuotaTime(billingPeriodEnd)
}

func grokQuotaPeriod(period *grokQuotaPeriodInfo, billingStart, billingEnd string, resetsAt int64) string {
	if period != nil {
		if label := grokPeriodLabel(period.Type, parseGrokQuotaTime(period.Start), parseGrokQuotaTime(period.End)); label != "" {
			return label
		}
	}
	if label := grokPeriodLabel("", parseGrokQuotaTime(billingStart), parseGrokQuotaTime(billingEnd)); label != "" {
		return label
	}
	if resetsAt > 0 {
		days := time.Until(time.Unix(resetsAt, 0).UTC()).Hours() / 24
		switch {
		case days >= 5 && days <= 9:
			return "weekly"
		case days >= 25 && days <= 35:
			return "monthly"
		}
	}
	return "credits"
}

func grokPeriodLabel(kind string, start, end int64) string {
	normalized := strings.ToUpper(kind)
	switch {
	case strings.Contains(normalized, "WEEK"):
		return "weekly"
	case strings.Contains(normalized, "MONTH"):
		return "monthly"
	case strings.Contains(normalized, "DAY"):
		return "credits"
	}
	if start > 0 && end > start {
		days := time.Unix(end, 0).UTC().Sub(time.Unix(start, 0).UTC()).Hours() / 24
		switch {
		case days >= 5 && days <= 9:
			return "weekly"
		case days >= 25 && days <= 35:
			return "monthly"
		}
	}
	return ""
}

func parseGrokQuotaTime(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.Unix()
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.Unix()
	}
	return 0
}

func clampPercent(value float64) float64 { return max(0, min(100, value)) }
