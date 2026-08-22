package cursor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"resty.dev/v3"

	"github.com/Viking602/azem/internal/netproxy"
)

const (
	DefaultLoginURL   = "https://cursor.com/loginDeepControl"
	DefaultPollURL    = "https://api2.cursor.sh/auth/poll"
	DefaultRefreshURL = "https://api2.cursor.sh/auth/exchange_user_api_key"

	pollMaxAttempts         = 150
	pollBaseDelay           = time.Second
	pollMaxDelay            = 10 * time.Second
	pollBackoffMultiplier   = 1.2
	tokenExpirySafetyWindow = 5 * time.Minute
)

type Client struct {
	HTTP          *resty.Client
	LoginURL      string
	PollURL       string
	RefreshURL    string
	AllowInsecure bool
	PollDelay     time.Duration
}

type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	AccountID    string
	Email        string
	DisplayName  string
}

type AuthParams struct {
	Verifier  string
	Challenge string
	UUID      string
	LoginURL  string
}

func NewClient() *Client {
	return &Client{HTTP: newHTTPClient(), LoginURL: DefaultLoginURL, PollURL: DefaultPollURL, RefreshURL: DefaultRefreshURL}
}

func (c *Client) Login(ctx context.Context, openURL func(string) error) (Tokens, error) {
	params, err := c.NewAuthParams()
	if err != nil {
		return Tokens{}, err
	}
	if openURL != nil {
		if err := openURL(params.LoginURL); err != nil {
			return Tokens{}, err
		}
	}
	return c.Poll(ctx, params.UUID, params.Verifier)
}

func (c *Client) NewAuthParams() (AuthParams, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return AuthParams{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	id := uuid.NewString()
	endpoint, err := url.Parse(firstNonEmpty(c.LoginURL, DefaultLoginURL))
	if err != nil {
		return AuthParams{}, err
	}
	query := endpoint.Query()
	query.Set("challenge", challenge)
	query.Set("uuid", id)
	query.Set("mode", "login")
	query.Set("redirectTarget", "cli")
	endpoint.RawQuery = query.Encode()
	return AuthParams{Verifier: verifier, Challenge: challenge, UUID: id, LoginURL: endpoint.String()}, nil
}

func (c *Client) Poll(ctx context.Context, id, verifier string) (Tokens, error) {
	delay := c.PollDelay
	if delay <= 0 {
		delay = pollBaseDelay
	}
	consecutiveErrors := 0
	for range pollMaxAttempts {
		if err := wait(ctx, delay); err != nil {
			return Tokens{}, err
		}
		tokens, pending, err := c.pollOnce(ctx, id, verifier)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				return Tokens{}, fmt.Errorf("cursor auth polling failed: %w", err)
			}
			continue
		}
		consecutiveErrors = 0
		if pending {
			next := time.Duration(float64(delay) * pollBackoffMultiplier)
			if next > pollMaxDelay {
				next = pollMaxDelay
			}
			delay = next
			continue
		}
		return tokens, nil
	}
	return Tokens{}, fmt.Errorf("cursor authentication polling timeout")
}

func (c *Client) pollOnce(ctx context.Context, id, verifier string) (Tokens, bool, error) {
	endpoint, err := url.Parse(firstNonEmpty(c.PollURL, DefaultPollURL))
	if err != nil {
		return Tokens{}, false, err
	}
	query := endpoint.Query()
	query.Set("uuid", id)
	query.Set("verifier", verifier)
	endpoint.RawQuery = query.Encode()
	response, err := c.httpClient().R().SetContext(ctx).Get(endpoint.String())
	if err != nil {
		return Tokens{}, false, err
	}
	if response.StatusCode() == http.StatusNotFound {
		return Tokens{}, true, nil
	}
	if response.StatusCode()/100 != 2 {
		return Tokens{}, false, fmt.Errorf("cursor poll returned HTTP %d: %s", response.StatusCode(), boundedError(response.Bytes()))
	}
	tokens, err := decodeTokens(response.Bytes())
	return tokens, false, err
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Tokens{}, fmt.Errorf("cursor refresh token is empty")
	}
	response, err := c.httpClient().R().SetContext(ctx).
		SetAuthToken(refreshToken).
		SetHeader("Content-Type", "application/json").
		SetBody("{}").
		Post(firstNonEmpty(c.RefreshURL, DefaultRefreshURL))
	if err != nil {
		return Tokens{}, err
	}
	if response.StatusCode()/100 != 2 {
		return Tokens{}, fmt.Errorf("cursor token refresh failed: HTTP %d: %s", response.StatusCode(), boundedError(response.Bytes()))
	}
	tokens, err := decodeTokens(response.Bytes())
	if err != nil {
		return Tokens{}, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokens, nil
}

func decodeTokens(data []byte) (Tokens, error) {
	var payload struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Tokens{}, fmt.Errorf("decode cursor tokens: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return Tokens{}, fmt.Errorf("cursor token response omitted accessToken")
	}
	tokens := Tokens{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken}
	applyIdentity(&tokens)
	return tokens, nil
}

func applyIdentity(tokens *Tokens) {
	identity := IdentityFromAccessToken(tokens.AccessToken)
	tokens.AccountID = firstNonEmpty(tokens.AccountID, identity.UserID)
	tokens.Email = firstNonEmpty(tokens.Email, identity.Email)
	tokens.DisplayName = firstNonEmpty(tokens.DisplayName, identity.DisplayName, tokens.Email, tokens.AccountID)
	if tokens.ExpiresAt.IsZero() {
		tokens.ExpiresAt = identity.ExpiresAt
	}
}

type Identity struct {
	UserID      string
	Email       string
	DisplayName string
	ExpiresAt   time.Time
}

func IdentityFromAccessToken(accessToken string) Identity {
	claims := tokenClaims(accessToken)
	sub := claimString(claims, "sub")
	userID := sub
	if parts := strings.Split(sub, "|"); len(parts) > 1 {
		userID = strings.TrimSpace(parts[len(parts)-1])
	}
	email := firstNonEmpty(claimString(claims, "email"), claimString(claims, "preferred_username"))
	expiresAt := time.Now().Add(time.Hour)
	if exp, ok := claims["exp"].(float64); ok && exp > 0 {
		expiresAt = time.Unix(int64(exp), 0).Add(-tokenExpirySafetyWindow)
	}
	return Identity{UserID: userID, Email: email, DisplayName: firstNonEmpty(email, userID), ExpiresAt: expiresAt}
}

func tokenClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func (c *Client) httpClient() *resty.Client {
	client := c.HTTP
	if client == nil {
		client = newHTTPClient()
		c.HTTP = client
	}
	if c.AllowInsecure {
		if transport, ok := client.Transport().(*http.Transport); ok && transport.TLSClientConfig != nil {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
	return client
}

func newHTTPClient() *resty.Client {
	client := resty.New().SetTimeout(30 * time.Second)
	netproxy.ConfigureTransport(client.Transport())
	return client
}

func (c *Client) Close() error {
	if c.HTTP == nil {
		return nil
	}
	return c.HTTP.Close()
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomURLSafe(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func boundedError(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 240 {
		return text[:240]
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
