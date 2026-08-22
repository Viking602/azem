package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultClientFollowsProxyResolver(t *testing.T) {
	client := NewClient()
	transport, ok := client.HTTP.Transport().(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("Cursor transport = %#v", client.HTTP.Transport())
	}
}

func TestPollReturnsTokensAfterPending(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/poll" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.URL.Query().Get("uuid") != "login-id" || request.URL.Query().Get("verifier") != "verifier" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if polls.Add(1) == 1 {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"accessToken": cursorTestJWT("user-1", "owner@example.com"), "refreshToken": "refresh"})
	}))
	defer server.Close()

	client := NewClient()
	client.PollURL = server.URL + "/poll"
	client.PollDelay = time.Millisecond
	tokens, err := client.Poll(context.Background(), "login-id", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccountID != "user-1" || tokens.Email != "owner@example.com" || tokens.RefreshToken != "refresh" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestRefreshKeepsPreviousRefreshTokenWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/refresh" || request.Header.Get("Authorization") != "Bearer old-refresh" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"accessToken": cursorTestJWT("user-2", "")})
	}))
	defer server.Close()

	client := NewClient()
	client.RefreshURL = server.URL + "/refresh"
	tokens, err := client.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccountID != "user-2" || tokens.RefreshToken != "old-refresh" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestNewAuthParamsUsesCLILoginQuery(t *testing.T) {
	client := NewClient()
	params, err := client.NewAuthParams()
	if err != nil {
		t.Fatal(err)
	}
	if params.Verifier == "" || params.Challenge == "" || params.UUID == "" {
		t.Fatalf("params = %+v", params)
	}
	if !containsAll(params.LoginURL, "challenge=", "uuid=", "mode=login", "redirectTarget=cli") {
		t.Fatalf("login URL = %q", params.LoginURL)
	}
}

func TestIdentityFromAccessTokenSplitsAuth0Subject(t *testing.T) {
	identity := IdentityFromAccessToken(cursorTestJWT("auth0|abc123", "person@example.com"))
	if identity.UserID != "abc123" || identity.Email != "person@example.com" {
		t.Fatalf("identity = %+v", identity)
	}
}

func cursorTestJWT(sub, email string) string {
	payload, _ := json.Marshal(map[string]any{"sub": sub, "email": email, "exp": time.Now().Add(time.Hour).Unix()})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !contains(value, part) {
			return false
		}
	}
	return true
}

func contains(value, part string) bool {
	return len(value) >= len(part) && (value == part || len(part) == 0 || stringIndex(value, part) >= 0)
}

func stringIndex(value, part string) int {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
