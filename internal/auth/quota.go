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

	cursorauth "github.com/Viking602/azem/internal/auth/cursor"
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
	DefaultCursorUsageURL     = "https://api2.cursor.sh/auth/usage"
	DefaultCursorSummaryURL   = "https://cursor.com/api/usage-summary"
	DefaultCursorMeURL        = "https://cursor.com/api/auth/me"
	cursorQuotaTimeout        = 15 * time.Second
)

type SubscriptionQuotaBreakdown struct {
	ID          string
	UsedPercent float64
}

type SubscriptionQuota struct {
	Plan        string
	Period      string
	StartsAt    int64
	UsedPercent float64
	ResetsAt    int64
	Balance     string
	Unlimited   bool
	Breakdown   []SubscriptionQuotaBreakdown
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
	case "cursor":
		return s.cursorQuota(ctx, accountID)
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
	response, err := s.grokProxyGET(ctx, accountID, s.grokQuotaURL(), func(request *resty.Request) {
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
	response, err := s.grokProxyGET(ctx, accountID, s.grokUserURL(), func(request *resty.Request) {
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
	var response *resty.Response
	var err error
	for attempt := range 2 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		requestCtx, cancel := context.WithTimeout(ctx, grokQuotaTimeout)
		response, err = s.DoWithRefresh(requestCtx, "grok", accountID, resty.MethodGet, endpoint, configure)
		cancel()
		if err == nil || !IsRetryableSubscriptionQuotaError(err) || attempt == 1 {
			return response, err
		}
		s.closeIdleHTTP()
	}
	return response, err
}

func (s *Service) closeIdleHTTP() {
	if s == nil || s.httpClient == nil {
		return
	}
	if transport, err := s.httpClient.HTTPTransport(); err == nil {
		transport.CloseIdleConnections()
	}
}

// IsRetryableSubscriptionQuotaError reports transport failures that can recover
// without changing credentials or subscription state.
func IsRetryableSubscriptionQuotaError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset",
		"broken pipe",
		"use of closed network connection",
		"http2: stream closed",
		"http2: client conn not usable",
		"unexpected eof",
		"tls handshake timeout",
		"i/o timeout",
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

func (s *Service) cursorQuota(ctx context.Context, accountID string) (SubscriptionQuota, error) {
	requestCtx, cancel := context.WithTimeout(ctx, cursorQuotaTimeout)
	defer cancel()
	credential, err := s.Credential(requestCtx, "cursor", accountID)
	if err != nil {
		return SubscriptionQuota{}, err
	}
	identity := cursorauth.IdentityFromAccessToken(credential.AccessToken)
	if summary, summaryErr := s.cursorUsageSummary(requestCtx, credential.AccessToken, identity.UserID); summaryErr == nil {
		if identity.Email != "" {
			summary.Email = firstNonEmpty(summary.Email, identity.Email)
			summary.DisplayName = firstNonEmpty(summary.DisplayName, identity.DisplayName, identity.Email)
		}
		if me, meErr := s.cursorProfile(requestCtx, credential.AccessToken, identity.UserID); meErr == nil {
			summary.Email = firstNonEmpty(me, summary.Email)
			summary.DisplayName = firstNonEmpty(summary.DisplayName, me)
		}
		summary.UserID = firstNonEmpty(identity.UserID, accountID)
		return summary, nil
	}
	usage, err := s.cursorAuthUsage(requestCtx, accountID)
	if err != nil {
		return SubscriptionQuota{}, err
	}
	usage.Email = firstNonEmpty(usage.Email, identity.Email)
	usage.DisplayName = firstNonEmpty(usage.DisplayName, identity.DisplayName, identity.Email)
	usage.UserID = firstNonEmpty(identity.UserID, accountID)
	return usage, nil
}

func (s *Service) cursorUsageSummary(ctx context.Context, accessToken, userID string) (SubscriptionQuota, error) {
	if strings.TrimSpace(userID) == "" {
		return SubscriptionQuota{}, fmt.Errorf("cursor usage-summary requires a user id")
	}
	endpoint := firstNonEmpty(s.CursorSummaryURL, DefaultCursorSummaryURL)
	response, err := s.httpClient.R().SetContext(ctx).SetResponseBodyLimit(subscriptionQuotaBodySize).
		SetHeader("Accept", "application/json").
		SetHeader("Cookie", "WorkosCursorSessionToken="+url.QueryEscape(userID+"::"+accessToken)).
		Get(endpoint)
	if err != nil {
		return SubscriptionQuota{}, err
	}
	if response.StatusCode()/100 != 2 {
		return SubscriptionQuota{}, quotaHTTPError("cursor", response.StatusCode(), response.Bytes())
	}
	return decodeCursorUsageSummary(response.Bytes())
}

func (s *Service) cursorProfile(ctx context.Context, accessToken, userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", fmt.Errorf("cursor profile requires a user id")
	}
	endpoint := firstNonEmpty(s.CursorMeURL, DefaultCursorMeURL)
	response, err := s.httpClient.R().SetContext(ctx).SetResponseBodyLimit(grokUserBodySize).
		SetHeader("Accept", "application/json").
		SetHeader("Cookie", "WorkosCursorSessionToken="+url.QueryEscape(userID+"::"+accessToken)).
		Get(endpoint)
	if err != nil {
		return "", err
	}
	if response.StatusCode()/100 != 2 {
		return "", quotaHTTPError("cursor", response.StatusCode(), response.Bytes())
	}
	var payload struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(response.Bytes(), &payload); err != nil {
		return "", err
	}
	if payload.Sub != "" && payload.Sub != userID {
		return "", fmt.Errorf("cursor profile user mismatch")
	}
	return strings.TrimSpace(payload.Email), nil
}

func (s *Service) cursorAuthUsage(ctx context.Context, accountID string) (SubscriptionQuota, error) {
	endpoint := firstNonEmpty(s.CursorUsageURL, DefaultCursorUsageURL)
	response, err := s.DoWithRefresh(ctx, "cursor", accountID, resty.MethodGet, endpoint, func(request *resty.Request) {
		request.SetResponseBodyLimit(subscriptionQuotaBodySize).
			SetHeader("Accept", "application/json").
			SetHeader("User-Agent", "azem/1")
	})
	if err != nil {
		return SubscriptionQuota{}, err
	}
	if response.StatusCode()/100 != 2 {
		return SubscriptionQuota{}, quotaHTTPError("cursor", response.StatusCode(), response.Bytes())
	}
	return decodeCursorAuthUsage(response.Bytes())
}

type cursorUsageAmount struct {
	Enabled   *bool    `json:"enabled"`
	Limit     *float64 `json:"limit"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
}

type cursorPlanUsage struct {
	Enabled          *bool    `json:"enabled"`
	Limit            *float64 `json:"limit"`
	Used             *float64 `json:"used"`
	Remaining        *float64 `json:"remaining"`
	AutoPercentUsed  *float64 `json:"autoPercentUsed"`
	APIPercentUsed   *float64 `json:"apiPercentUsed"`
	TotalPercentUsed *float64 `json:"totalPercentUsed"`
}

type cursorIndividualUsage struct {
	Plan     *cursorPlanUsage   `json:"plan"`
	Overall  *cursorUsageAmount `json:"overall"`
	OnDemand *cursorUsageAmount `json:"onDemand"`
}

type cursorTeamUsage struct {
	Pooled   *cursorUsageAmount `json:"pooled"`
	OnDemand *cursorUsageAmount `json:"onDemand"`
}

type cursorUsageSummaryPayload struct {
	MembershipType    string                 `json:"membershipType"`
	BillingCycleStart string                 `json:"billingCycleStart"`
	BillingCycleEnd   string                 `json:"billingCycleEnd"`
	StartOfMonth      string                 `json:"startOfMonth"`
	IndividualUsage   *cursorIndividualUsage `json:"individualUsage"`
	TeamUsage         *cursorTeamUsage       `json:"teamUsage"`
}

func decodeCursorUsageSummary(data []byte) (SubscriptionQuota, error) {
	var payload cursorUsageSummaryPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return SubscriptionQuota{}, fmt.Errorf("decode cursor usage-summary: %w", err)
	}
	quota := SubscriptionQuota{Plan: strings.TrimSpace(payload.MembershipType), Period: "monthly"}
	quota.StartsAt = parseGrokQuotaTime(firstNonEmpty(payload.BillingCycleStart, payload.StartOfMonth))
	quota.ResetsAt = parseGrokQuotaTime(payload.BillingCycleEnd)
	if quota.ResetsAt == 0 && quota.StartsAt > 0 {
		quota.ResetsAt = time.Unix(quota.StartsAt, 0).UTC().AddDate(0, 1, 0).Unix()
	}
	used, ok := cursorSummaryUsedPercent(&payload)
	if !ok && quota.ResetsAt == 0 {
		return SubscriptionQuota{}, fmt.Errorf("cursor usage-summary contained no usage")
	}
	quota.UsedPercent = clampPercent(used)
	quota.Breakdown = cursorSummaryBreakdown(payload.IndividualUsage)
	if remaining := cursorOnDemandRemaining(payload.IndividualUsage, payload.TeamUsage); remaining != "" {
		quota.Balance = remaining
	}
	return quota, nil
}

func cursorSummaryUsedPercent(payload *cursorUsageSummaryPayload) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	var plan *cursorPlanUsage
	var overall, pooled *cursorUsageAmount
	if payload.IndividualUsage != nil {
		plan = payload.IndividualUsage.Plan
		overall = payload.IndividualUsage.Overall
	}
	if payload.TeamUsage != nil {
		pooled = payload.TeamUsage.Pooled
	}
	if plan != nil && cursorUsageEnabled(plan.Enabled) {
		switch {
		case plan.TotalPercentUsed != nil:
			return *plan.TotalPercentUsed, true
		case plan.AutoPercentUsed != nil && plan.APIPercentUsed != nil:
			return (*plan.AutoPercentUsed + *plan.APIPercentUsed) / 2, true
		case plan.APIPercentUsed != nil:
			return *plan.APIPercentUsed, true
		case plan.AutoPercentUsed != nil:
			return *plan.AutoPercentUsed, true
		case plan.Limit != nil && *plan.Limit > 0 && plan.Used != nil:
			return *plan.Used / *plan.Limit * 100, true
		}
	}
	for _, usage := range []*cursorUsageAmount{overall, pooled} {
		if usage != nil && cursorUsageEnabled(usage.Enabled) && usage.Limit != nil && *usage.Limit > 0 && usage.Used != nil {
			return *usage.Used / *usage.Limit * 100, true
		}
	}
	return 0, false
}

func cursorSummaryBreakdown(usage *cursorIndividualUsage) []SubscriptionQuotaBreakdown {
	if usage == nil || usage.Plan == nil || !cursorUsageEnabled(usage.Plan.Enabled) {
		return nil
	}
	breakdown := make([]SubscriptionQuotaBreakdown, 0, 2)
	if usage.Plan.AutoPercentUsed != nil {
		breakdown = append(breakdown, SubscriptionQuotaBreakdown{ID: "cursor", UsedPercent: clampPercent(*usage.Plan.AutoPercentUsed)})
	}
	if usage.Plan.APIPercentUsed != nil {
		breakdown = append(breakdown, SubscriptionQuotaBreakdown{ID: "third_party", UsedPercent: clampPercent(*usage.Plan.APIPercentUsed)})
	}
	return breakdown
}

func cursorOnDemandRemaining(individual *cursorIndividualUsage, team *cursorTeamUsage) string {
	if individual != nil {
		if remaining := cursorUsageRemaining(individual.OnDemand); remaining != "" {
			return remaining
		}
	}
	if team != nil {
		return cursorUsageRemaining(team.OnDemand)
	}
	return ""
}

func cursorUsageRemaining(usage *cursorUsageAmount) string {
	if usage == nil || !cursorUsageEnabled(usage.Enabled) {
		return ""
	}
	return cursorCentsRemaining(usage.Remaining, usage.Limit, usage.Used)
}

func cursorUsageEnabled(enabled *bool) bool {
	return enabled == nil || *enabled
}

func cursorCentsRemaining(remaining, limit, used *float64) string {
	var cents float64
	switch {
	case remaining != nil && *remaining >= 0:
		cents = *remaining
	case limit != nil && used != nil:
		cents = max(0, *limit-*used)
	default:
		return ""
	}
	return fmt.Sprintf("%.2f", cents/100)
}

func decodeCursorAuthUsage(data []byte) (SubscriptionQuota, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return SubscriptionQuota{}, fmt.Errorf("decode cursor auth usage: %w", err)
	}
	quota := SubscriptionQuota{
		Period:   "monthly",
		StartsAt: cursorTimeFromMap(payload, "billingCycleStart", "startOfMonth"),
		ResetsAt: cursorTimeFromMap(payload, "billingCycleEnd", "endOfMonth", "resetsAt", "nextReset"),
	}
	if quota.ResetsAt == 0 && quota.StartsAt > 0 {
		quota.ResetsAt = time.Unix(quota.StartsAt, 0).UTC().AddDate(0, 1, 0).Unix()
	}
	for _, value := range payload {
		bucket, ok := value.(map[string]any)
		if !ok {
			continue
		}
		used := firstNumber(bucket, "numRequests", "used", "amountUsed", "usdUsed")
		limit := firstNumber(bucket, "maxRequestUsage", "limit", "amountLimit", "usdLimit")
		if used == nil || limit == nil || *limit <= 0 {
			continue
		}
		quota.UsedPercent = clampPercent(*used / *limit * 100)
		return quota, nil
	}
	if quota.ResetsAt > 0 {
		return quota, nil
	}
	return SubscriptionQuota{}, fmt.Errorf("cursor auth usage contained no request or spend limit")
}

func cursorTimeFromMap(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if timestamp := parseGrokQuotaTime(fmt.Sprint(payload[key])); timestamp > 0 {
			return timestamp
		}
	}
	return 0
}

func firstNumber(values map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return &value
		case json.Number:
			if parsed, err := value.Float64(); err == nil {
				return &parsed
			}
		}
	}
	return nil
}
