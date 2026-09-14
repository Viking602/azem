// Package devin implements the personal-account Devin CLI browser login.
package devin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/netproxy"
)

const (
	DefaultAuthorizeURL = "https://app.devin.ai/auth/cli/continue"
	DefaultTokenURL     = "https://api.devin.ai/auth/cli/token"
	SessionTokenPrefix  = "devin-session-token$"
)

type Client struct {
	HTTP           *http.Client
	AuthorizeURL   string
	TokenURL       string
	ListenCallback func() (net.Listener, error)
}

type Tokens struct {
	AccessToken string
	AccountID   string
	Email       string
	ExpiresAt   time.Time
}

func NewClient() *Client {
	client := netproxy.NewHTTPClient(30 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{HTTP: client, AuthorizeURL: DefaultAuthorizeURL, TokenURL: DefaultTokenURL}
}

func (c *Client) Login(ctx context.Context, openURL func(string) error) (Tokens, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listen := c.ListenCallback
	if listen == nil {
		listen = func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:59653") }
	}
	listener, err := listen()
	if err != nil {
		return Tokens{}, fmt.Errorf("listen for Devin login callback: %w", err)
	}
	defer listener.Close()
	pkce, err := chatgpt.NewPKCE()
	if err != nil {
		return Tokens{}, err
	}
	endpoint, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return Tokens{}, fmt.Errorf("invalid Devin authorization URL")
	}
	query := endpoint.Query()
	query.Set("redirect_uri", "http://"+listener.Addr().String()+"/callback")
	query.Set("state", pkce.State)
	query.Set("prompt", "select_account")
	query.Set("code_challenge", pkce.Challenge)
	query.Set("code_challenge_method", "S256")
	endpoint.RawQuery = query.Encode()
	type callback struct {
		code string
		err  error
	}
	result := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet || r.URL.Query().Get("state") != pkce.State {
			http.Error(w, "Invalid sign-in callback", http.StatusBadRequest)
			return // Unrelated requests must not cancel the user's pending login.
		}
		answer := callback{code: r.URL.Query().Get("code")}
		if r.URL.Query().Get("error") != "" {
			answer.err = fmt.Errorf("Devin sign-in was declined")
		}
		if answer.code == "" && answer.err == nil {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		}
		select {
		case result <- answer:
			_, _ = io.WriteString(w, "Authorization received. Return to Azem to finish signing in.")
		default:
			http.Error(w, "Callback already received", http.StatusConflict)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case result <- callback{err: fmt.Errorf("Devin callback server stopped")}:
			default:
			}
		}
	}()
	if openURL == nil {
		return Tokens{}, fmt.Errorf("browser opener is unavailable")
	}
	if err := openURL(endpoint.String()); err != nil {
		return Tokens{}, fmt.Errorf("open Devin login: %w", err)
	}
	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case answer := <-result:
		if answer.err != nil {
			return Tokens{}, answer.err
		}
		return c.exchange(ctx, answer.code, pkce.Verifier)
	}
}

func (c *Client) exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	body, err := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	if err != nil {
		return Tokens{}, fmt.Errorf("invalid Devin token endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("Devin token exchange failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return Tokens{}, fmt.Errorf("Devin token exchange returned HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil {
		return Tokens{}, err
	}
	if len(raw) > 64<<10 {
		return Tokens{}, fmt.Errorf("Devin token response is too large")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &payload) != nil || strings.TrimSpace(payload.Token) == "" {
		return Tokens{}, fmt.Errorf("Devin token response omitted a valid token")
	}
	token := strings.TrimPrefix(strings.TrimSpace(payload.Token), SessionTokenPrefix)
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, " \t\r\n") {
		return Tokens{}, fmt.Errorf("Devin token response omitted a valid token")
	}
	tokens := Tokens{AccessToken: SessionTokenPrefix + token}
	// Claims are display/expiry hints from the authenticated exchange, never authorization.
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		data, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Sub    string `json:"sub"`
			UserID string `json:"user_id"`
			Email  string `json:"email"`
			Exp    int64  `json:"exp"`
		}
		if json.Unmarshal(data, &claims) == nil {
			tokens.AccountID, tokens.Email = claims.Sub, claims.Email
			if tokens.AccountID == "" {
				tokens.AccountID = claims.UserID
			}
			if claims.Exp > 0 {
				tokens.ExpiresAt = time.Unix(claims.Exp, 0)
			}
		}
	}
	return tokens, nil
}
