package grok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	DefaultScope         = "openid profile email offline_access grok-cli:access api:access"
	CompatibilityNotice = "Grok sign-in uses the Grok CLI public-client compatibility surface and is experimental."
)

var (
	ErrExpiredToken = errors.New("device code expired")
	ErrAccessDenied = errors.New("device authorization denied")
)

type Client struct {
	HTTP          *http.Client
	DiscoveryURL  string
	ClientID      string
	Scope         string
	AllowInsecure bool
	Wait          func(context.Context, time.Duration) error
}

type Discovery struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
}

type DeviceAuthorization struct {
	DeviceCode              string    `json:"device_code"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete"`
	ExpiresIn               int       `json:"expires_in"`
	Interval                int       `json:"interval"`
	ExpiresAt               time.Time `json:"-"`
}

type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	TokenType    string
	ExpiresAt    time.Time
	AccountID    string
	Email        string
	Plan         string
	ClientID     string
	SourcePath   string
	SourceKey    string
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second}, DiscoveryURL: "https://auth.x.ai/.well-known/openid-configuration",
		ClientID: DefaultClientID, Scope: DefaultScope,
	}
}

func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	if err := c.validateEndpoint(c.DiscoveryURL); err != nil {
		return Discovery{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DiscoveryURL, nil)
	if err != nil {
		return Discovery{}, err
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return Discovery{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return Discovery{}, fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode)
	}
	var discovery Discovery
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&discovery); err != nil {
		return Discovery{}, err
	}
	if discovery.DeviceAuthorizationEndpoint == "" || discovery.TokenEndpoint == "" {
		return Discovery{}, fmt.Errorf("OIDC discovery missing device or token endpoint")
	}
	for _, endpoint := range []string{discovery.DeviceAuthorizationEndpoint, discovery.TokenEndpoint, discovery.RevocationEndpoint} {
		if endpoint != "" {
			if err := c.validateEndpoint(endpoint); err != nil {
				return Discovery{}, err
			}
		}
	}
	return discovery, nil
}

func (c *Client) BeginDevice(ctx context.Context, discovery Discovery) (DeviceAuthorization, error) {
	body := url.Values{"client_id": {c.ClientID}, "scope": {c.Scope}}
	response, err := c.postForm(ctx, discovery.DeviceAuthorizationEndpoint, body)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return DeviceAuthorization{}, err
	}
	if response.StatusCode/100 != 2 {
		return DeviceAuthorization{}, fmt.Errorf("device authorization returned HTTP %d", response.StatusCode)
	}
	var device DeviceAuthorization
	if err := json.Unmarshal(data, &device); err != nil {
		return DeviceAuthorization{}, err
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" {
		return DeviceAuthorization{}, fmt.Errorf("device authorization response is incomplete")
	}
	if device.Interval <= 0 {
		device.Interval = 5
	}
	device.ExpiresAt = time.Now().UTC().Add(time.Duration(device.ExpiresIn) * time.Second)
	return device, nil
}

func (c *Client) LoginDevice(ctx context.Context, notify func(DeviceAuthorization) error) (Tokens, error) {
	discovery, err := c.Discover(ctx)
	if err != nil {
		return Tokens{}, err
	}
	device, err := c.BeginDevice(ctx, discovery)
	if err != nil {
		return Tokens{}, err
	}
	if err := notify(device); err != nil {
		return Tokens{}, err
	}
	return c.PollDevice(ctx, discovery, device)
}

func (c *Client) PollDevice(ctx context.Context, discovery Discovery, device DeviceAuthorization) (Tokens, error) {
	interval := time.Duration(device.Interval) * time.Second
	for {
		if !device.ExpiresAt.IsZero() && time.Now().After(device.ExpiresAt) {
			return Tokens{}, ErrExpiredToken
		}
		if err := c.wait(ctx, interval); err != nil {
			return Tokens{}, err
		}
		response, err := c.postForm(ctx, discovery.TokenEndpoint, url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {c.ClientID}, "device_code": {device.DeviceCode},
		})
		if err != nil {
			return Tokens{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			return Tokens{}, readErr
		}
		if response.StatusCode/100 == 2 {
			return decodeTokens(data)
		}
		var failure struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &failure)
		switch failure.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return Tokens{}, ErrExpiredToken
		case "access_denied":
			return Tokens{}, ErrAccessDenied
		default:
			return Tokens{}, fmt.Errorf("device token request returned HTTP %d: %s", response.StatusCode, failure.Error)
		}
	}
}

func (c *Client) Refresh(ctx context.Context, discovery Discovery, refreshToken string) (Tokens, error) {
	response, err := c.postForm(ctx, discovery.TokenEndpoint, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {refreshToken}})
	if err != nil {
		return Tokens{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Tokens{}, err
	}
	if response.StatusCode/100 != 2 {
		return Tokens{}, fmt.Errorf("refresh returned HTTP %d", response.StatusCode)
	}
	return decodeTokens(data)
}

func (c *Client) Revoke(ctx context.Context, discovery Discovery, token string) error {
	if token == "" || discovery.RevocationEndpoint == "" {
		return nil
	}
	response, err := c.postForm(ctx, discovery.RevocationEndpoint, url.Values{"client_id": {c.ClientID}, "token": {token}})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("revoke returned HTTP %d", response.StatusCode)
	}
	return nil
}

func Import(path string) (Tokens, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Tokens{}, fmt.Errorf("read Grok credential: %w", err)
	}
	if len(data) > 1<<20 {
		return Tokens{}, fmt.Errorf("Grok credential exceeds 1 MiB")
	}
	var payload struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token"`
		IDToken      string    `json:"id_token"`
		AccountID    string    `json:"account_id"`
		Email        string    `json:"email"`
		Plan         string    `json:"plan"`
		ClientID     string    `json:"oidc_client_id"`
		ExpiresAt    time.Time `json:"expires_at"`
		Tokens       *struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Tokens{}, err
	}
	result := Tokens{
		AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, IDToken: payload.IDToken,
		AccountID: payload.AccountID, Email: payload.Email, Plan: payload.Plan,
		ClientID: payload.ClientID, ExpiresAt: payload.ExpiresAt,
	}
	if payload.Tokens != nil {
		if result.AccessToken == "" {
			result.AccessToken = payload.Tokens.AccessToken
		}
		if result.RefreshToken == "" {
			result.RefreshToken = payload.Tokens.RefreshToken
		}
		if result.IDToken == "" {
			result.IDToken = payload.Tokens.IDToken
		}
		if result.AccountID == "" {
			result.AccountID = payload.Tokens.AccountID
		}
	}
	if result.AccessToken == "" && result.RefreshToken == "" {
		var records map[string]struct {
			AuthMode     string    `json:"auth_mode"`
			AccessToken  string    `json:"access_token"`
			Key          string    `json:"key"`
			RefreshToken string    `json:"refresh_token"`
			IDToken      string    `json:"id_token"`
			ClientID     string    `json:"oidc_client_id"`
			Issuer       string    `json:"oidc_issuer"`
			PrincipalID  string    `json:"principal_id"`
			UserID       string    `json:"user_id"`
			TeamID       string    `json:"team_id"`
			Email        string    `json:"email"`
			ExpiresAt    time.Time `json:"expires_at"`
		}
		if err := json.Unmarshal(data, &records); err != nil {
			return Tokens{}, fmt.Errorf("decode Grok CLI credential: %w", err)
		}
		keys := make([]string, 0, len(records))
		for key := range records {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			record := records[key]
			accessToken := record.AccessToken
			if accessToken == "" {
				accessToken = record.Key
			}
			if record.AuthMode != "oidc" || record.Issuer != "https://auth.x.ai" || (accessToken == "" && record.RefreshToken == "") {
				continue
			}
			accountID := record.PrincipalID
			if accountID == "" {
				accountID = record.UserID
			}
			if accountID == "" {
				accountID = record.TeamID
			}
			result = Tokens{
				AccessToken: accessToken, RefreshToken: record.RefreshToken, IDToken: record.IDToken,
				ClientID: record.ClientID, AccountID: accountID, Email: record.Email, ExpiresAt: record.ExpiresAt,
				SourcePath: path, SourceKey: key,
			}
			break
		}
	}
	if result.AccessToken == "" && result.RefreshToken == "" {
		return Tokens{}, fmt.Errorf("Grok credential has no access or refresh token")
	}
	return result, nil
}

func SyncImported(tokens Tokens) error {
	if tokens.SourcePath == "" || tokens.SourceKey == "" {
		return nil
	}
	path, err := filepath.EvalSymlinks(tokens.SourcePath)
	if err != nil {
		return fmt.Errorf("resolve Grok CLI credential: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat Grok CLI credential: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Grok CLI credential is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Grok CLI credential: %w", err)
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("Grok CLI credential exceeds 1 MiB")
	}
	var records map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("decode Grok CLI credential: %w", err)
	}
	raw, found := records[tokens.SourceKey]
	if !found {
		return fmt.Errorf("Grok CLI credential record no longer exists")
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("decode Grok CLI credential record: %w", err)
	}
	set := func(name string, value any) error {
		encoded, err := json.Marshal(value)
		if err == nil {
			record[name] = encoded
		}
		return err
	}
	if err := set("key", tokens.AccessToken); err != nil {
		return err
	}
	if tokens.RefreshToken != "" {
		if err := set("refresh_token", tokens.RefreshToken); err != nil {
			return err
		}
	}
	if !tokens.ExpiresAt.IsZero() {
		if err := set("expires_at", tokens.ExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	records[tokens.SourceKey], err = json.Marshal(record)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".azem-grok-auth-*")
	if err != nil {
		return fmt.Errorf("create Grok CLI credential update: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace Grok CLI credential: %w", err)
	}
	return nil
}

func decodeTokens(data []byte) (Tokens, error) {
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		AccountID    string `json:"account_id"`
		Email        string `json:"email"`
		Plan         string `json:"plan"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Tokens{}, err
	}
	if payload.AccessToken == "" {
		return Tokens{}, fmt.Errorf("token response missing access_token")
	}
	result := Tokens{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, IDToken: payload.IDToken, TokenType: payload.TokenType, AccountID: payload.AccountID, Email: payload.Email, Plan: payload.Plan}
	if payload.ExpiresIn > 0 {
		result.ExpiresAt = time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	return result, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, values url.Values) (*http.Response, error) {
	if err := c.validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.httpClient().Do(request)
}

func (c *Client) ValidateResourceURL(raw string) error {
	return c.validateEndpoint(raw)
}

func (c *Client) validateEndpoint(raw string) error {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if c.AllowInsecure && (endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "localhost") {
		return nil
	}
	if endpoint.Scheme != "https" {
		return fmt.Errorf("Grok OAuth endpoint must use HTTPS")
	}
	host := strings.ToLower(endpoint.Hostname())
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return fmt.Errorf("refusing to send Grok bearer flow to %q", host)
	}
	return nil
}

func (c *Client) wait(ctx context.Context, duration time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
