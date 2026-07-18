package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	DefaultClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	CompatibilityNotice = "ChatGPT sign-in uses Codex-compatible OAuth endpoints; OpenAI does not publish this as a stable third-party Azem OAuth contract."
	defaultCallbackPort = 1455
	codexOriginator     = "codex_cli_rs"
)

type Client struct {
	HTTP           *http.Client
	ClientID       string
	AuthorizeURL   string
	TokenURL       string
	RevokeURL      string
	ListenCallback func() (net.Listener, int, error)
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
}

type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second}, ClientID: DefaultClientID,
		AuthorizeURL: "https://auth.openai.com/oauth/authorize", TokenURL: "https://auth.openai.com/oauth/token", RevokeURL: "https://auth.openai.com/oauth/revoke",
	}
}

func NewPKCE() (PKCE, error) {
	verifier, err := randomURLSafe(64)
	if err != nil {
		return PKCE{}, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return PKCE{}, err
	}
	digest := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), State: state}, nil
}

func (c *Client) AuthorizationURL(redirectURI string, pkce PKCE) (string, error) {
	endpoint, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", c.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", "openid profile email offline_access api.connectors.read api.connectors.invoke")
	query.Set("state", pkce.State)
	query.Set("code_challenge", pkce.Challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", codexOriginator)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func listenOAuthCallback() (net.Listener, int, error) {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", defaultCallbackPort))
	if err != nil {
		return nil, 0, fmt.Errorf("listen for OAuth callback on Codex allow-listed port %d: %w", defaultCallbackPort, err)
	}
	return listener, defaultCallbackPort, nil
}

func (c *Client) Login(ctx context.Context, openURL func(string) error) (Tokens, error) {
	listen := c.ListenCallback
	if listen == nil {
		listen = listenOAuthCallback
	}
	listener, callbackPort, err := listen()
	if err != nil {
		return Tokens{}, err
	}
	defer listener.Close()
	pkce, err := NewPKCE()
	if err != nil {
		return Tokens{}, err
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", callbackPort)
	authorizeURL, err := c.AuthorizationURL(redirectURI, pkce)
	if err != nil {
		return Tokens{}, err
	}
	type callback struct {
		code string
		err  error
	}
	result := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("state") != pkce.State {
			result <- callback{err: fmt.Errorf("OAuth state mismatch")}
			http.Error(writer, "Azem sign-in failed: state mismatch", http.StatusBadRequest)
			return
		}
		if oauthError := request.URL.Query().Get("error"); oauthError != "" {
			result <- callback{err: fmt.Errorf("OAuth callback: %s", oauthError)}
			http.Error(writer, "Azem sign-in was not completed", http.StatusBadRequest)
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			result <- callback{err: fmt.Errorf("OAuth callback missing code")}
			http.Error(writer, "Azem sign-in failed: missing code", http.StatusBadRequest)
			return
		}
		result <- callback{code: code}
		_, _ = io.WriteString(writer, "Azem sign-in complete. You can close this window.")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case result <- callback{err: serveErr}:
			default:
			}
		}
	}()
	defer server.Shutdown(context.Background())
	if err := openURL(authorizeURL); err != nil {
		return Tokens{}, fmt.Errorf("open authorization URL: %w", err)
	}
	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case callback := <-result:
		if callback.err != nil {
			return Tokens{}, callback.err
		}
		return c.Exchange(ctx, callback.code, pkce.Verifier, redirectURI)
	}
}

func (c *Client) Exchange(ctx context.Context, code string, verifier string, redirectURI string) (Tokens, error) {
	return c.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI},
	})
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {refreshToken}})
}

func (c *Client) Revoke(ctx context.Context, token string) error {
	if token == "" || c.RevokeURL == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RevokeURL, strings.NewReader(url.Values{"client_id": {c.ClientID}, "token": {token}}.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("revoke returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *Client) tokenRequest(ctx context.Context, values url.Values) (Tokens, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return Tokens{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Tokens{}, err
	}
	if response.StatusCode/100 != 2 {
		return Tokens{}, fmt.Errorf("token exchange returned HTTP %d: %s", response.StatusCode, boundedError(body))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Tokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return Tokens{}, fmt.Errorf("token response missing access_token")
	}
	result := Tokens{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, IDToken: payload.IDToken, TokenType: payload.TokenType}
	if payload.ExpiresIn > 0 {
		result.ExpiresAt = time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	claims := tokenClaims(payload.IDToken)
	if len(claims) == 0 {
		claims = tokenClaims(payload.AccessToken)
	}
	result.AccountID, result.Email, result.Plan = claimMetadata(claims)
	return result, nil
}

func ImportCodex(path string) (Tokens, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Tokens{}, fmt.Errorf("read Codex credential: %w", err)
	}
	return decodeCodexCredential(data)
}

func ImportCodexKeyring(codexHome string) (Tokens, error) {
	key, err := codexKeyringAccount(codexHome)
	if err != nil {
		return Tokens{}, err
	}
	secret, err := keyring.Get("Codex Auth", key)
	if errors.Is(err, keyring.ErrNotFound) {
		return Tokens{}, fmt.Errorf("Codex keyring credential not found")
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read Codex keyring credential: %w", err)
	}
	return decodeCodexCredential([]byte(secret))
}

func codexKeyringAccount(codexHome string) (string, error) {
	if codexHome == "" {
		return "", fmt.Errorf("Codex home is empty")
	}
	canonical := filepath.Clean(codexHome)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	digest := sha256.Sum256([]byte(canonical))
	return "cli|" + hex.EncodeToString(digest[:])[:16], nil
}

func decodeCodexCredential(data []byte) (Tokens, error) {
	if len(data) > 1<<20 {
		return Tokens{}, fmt.Errorf("Codex credential exceeds 1 MiB")
	}
	var payload struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Tokens{}, fmt.Errorf("decode Codex credential: %w", err)
	}
	result := Tokens{AccessToken: first(payload.Tokens.AccessToken, payload.AccessToken), RefreshToken: first(payload.Tokens.RefreshToken, payload.RefreshToken), IDToken: first(payload.Tokens.IDToken, payload.IDToken), AccountID: payload.Tokens.AccountID}
	if result.AccessToken == "" {
		return Tokens{}, fmt.Errorf("Codex credential has no access token")
	}
	account, email, plan := claimMetadata(tokenClaims(result.IDToken))
	if result.AccountID == "" {
		result.AccountID = account
	}
	result.Email, result.Plan = email, plan
	return result, nil
}

func tokenClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}

func claimMetadata(claims map[string]any) (accountID string, email string, plan string) {
	email, _ = claims["email"].(string)
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		accountID, _ = auth["chatgpt_account_id"].(string)
		plan, _ = auth["chatgpt_plan_type"].(string)
	}
	if accountID == "" {
		accountID, _ = claims["account_id"].(string)
	}
	return
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func randomURLSafe(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func boundedError(body []byte) string {
	var payload struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error != nil {
		encoded, _ := json.Marshal(payload.Error)
		if len(encoded) > 512 {
			encoded = encoded[:512]
		}
		return string(encoded)
	}
	return http.StatusText(http.StatusBadRequest)
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
