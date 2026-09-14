package devin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPersonalLoginPKCEAndState(t *testing.T) {
	var challenge string
	var exchanged bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid token request")
		}
		sum := sha256.Sum256([]byte(body["code_verifier"]))
		if body["code"] != "accepted-code" || len(body["code_verifier"]) < 43 || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			t.Error("PKCE exchange does not match authorization")
		}
		exchanged = true
		_, _ = io.WriteString(w, `{"token":"devin-session-token$header.eyJzdWIiOiJwZXJzb25hbC11c2VyIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUudGVzdCIsImV4cCI6NDEwMjQ0NDgwMH0.signature"}`)
	}))
	defer server.Close()
	client := NewClient()
	client.TokenURL = server.URL
	client.ListenCallback = func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
	tokens, err := client.Login(context.Background(), func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		if u.Host != "app.devin.ai" || q.Get("prompt") != "select_account" || q.Get("code_challenge_method") != "S256" || len(q.Get("state")) < 16 {
			t.Error("invalid authorization request")
		}
		callback, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			return err
		}
		if callback.Hostname() != "127.0.0.1" || callback.Path != "/callback" {
			t.Error("callback is not loopback")
		}
		for _, state := range []string{"wrong-state", q.Get("state")} {
			callback.RawQuery = url.Values{"state": {state}, "code": {"accepted-code"}}.Encode()
			res, err := http.Get(callback.String())
			if err != nil {
				return err
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			want := http.StatusOK
			if state == "wrong-state" {
				want = http.StatusBadRequest
			}
			if res.StatusCode != want || res.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), "accepted-code") {
				t.Error("invalid callback response")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exchanged || tokens.AccountID != "personal-user" || tokens.Email != "user@example.test" || tokens.ExpiresAt.Unix() != 4102444800 || !strings.HasPrefix(tokens.AccessToken, SessionTokenPrefix) {
		t.Fatal("personal identity or credential was lost")
	}
}

func TestLoginCancellationAndSafeExchangeErrors(t *testing.T) {
	client := NewClient()
	client.ListenCallback = func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
	ctx, cancel := context.WithCancel(context.Background())
	_, err := client.Login(ctx, func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, body := range []string{`{"token":"devin-session-token$"}`, `{"token":"two words"}`, strings.Repeat("s", (64<<10)+1), `sensitive-token-secret`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		client.TokenURL = server.URL
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		_, err := client.exchange(ctx, "code", "verifier")
		stop()
		server.Close()
		if err == nil || strings.Contains(err.Error(), "sensitive-token-secret") {
			t.Fatalf("unsafe/accepted token response: %v", err)
		}
	}
}
