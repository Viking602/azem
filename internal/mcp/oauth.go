package mcp

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
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/netproxy"
)

const maxOAuthResponseBytes = 1 << 20

type OAuthCredential struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ClientID     string
	ExpiresAt    time.Time
	TokenURL     string
}

type OAuthStore interface {
	Get(context.Context, string) (OAuthCredential, bool, error)
	Put(context.Context, string, OAuthCredential) error
	Delete(context.Context, string) error
}

type OAuthBroker struct {
	Store         OAuthStore
	ResolveSecret SecretResolver
	HTTPClient    *http.Client
}

type oauthEndpoints struct {
	AuthorizationURL string
	TokenURL         string
	RegistrationURL  string
	Scopes           []string
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (broker *OAuthBroker) AuthorizationHeader(ctx context.Context, serverName string, server config.MCPServerConfig) (string, error) {
	if broker == nil || broker.Store == nil || server.Transport != "streamable_http" {
		return "", nil
	}
	if server.Auth != nil && server.Auth.Type != "" && server.Auth.Type != "oauth" {
		return "", nil
	}
	credentialID := mcpOAuthCredentialID(serverName, server)
	credential, found, err := broker.Store.Get(ctx, credentialID)
	if err != nil {
		return "", err
	}
	if !found || credential.AccessToken == "" {
		return "", nil
	}
	if !credential.ExpiresAt.IsZero() && time.Until(credential.ExpiresAt) <= time.Minute {
		credential, err = broker.refresh(ctx, credentialID, server, credential)
		if err != nil {
			return "", err
		}
	}
	tokenType := strings.TrimSpace(credential.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	return tokenType + " " + credential.AccessToken, nil
}

func (broker *OAuthBroker) Authenticate(ctx context.Context, serverName string, server config.MCPServerConfig, openURL func(string) error) error {
	if broker == nil || broker.Store == nil {
		return errors.New("MCP OAuth credential store is unavailable")
	}
	if server.Transport != "streamable_http" || strings.TrimSpace(server.URL) == "" {
		return errors.New("MCP OAuth requires a streamable HTTP server")
	}
	listener, redirectURI, callbackPath, err := oauthCallbackListener(server)
	if err != nil {
		return err
	}
	defer listener.Close()
	endpoints, err := broker.discover(ctx, server)
	if err != nil {
		return err
	}
	clientID := oauthClientID(server)
	clientSecret := oauthClientSecret(server)
	if clientSecret != "" && broker.ResolveSecret != nil {
		clientSecret, err = broker.ResolveSecret(ctx, clientSecret)
		if err != nil {
			return fmt.Errorf("resolve MCP OAuth client secret: %w", err)
		}
	}
	if clientID == "" {
		clientID, err = broker.registerClient(ctx, endpoints.RegistrationURL, redirectURI)
		if err != nil {
			return err
		}
	}
	if clientID == "" {
		return errors.New("MCP OAuth discovery did not provide a client id or registration endpoint")
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		return err
	}
	verifier, err := randomOAuthValue(64)
	if err != nil {
		return err
	}
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])
	authorizationURL, err := url.Parse(endpoints.AuthorizationURL)
	if err != nil {
		return err
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	scopes := endpoints.Scopes
	if server.OAuth != nil && len(server.OAuth.Scopes) > 0 {
		scopes = server.OAuth.Scopes
	}
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	if server.OAuth != nil && server.OAuth.Prompt != "" {
		query.Set("prompt", server.OAuth.Prompt)
	}
	if server.Auth != nil && server.Auth.Resource != "" {
		query.Set("resource", server.Auth.Resource)
	} else {
		query.Set("resource", server.URL)
	}
	authorizationURL.RawQuery = query.Encode()

	type callbackResult struct{ code, err string }
	callback := make(chan callbackResult, 1)
	serverHTTP := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != callbackPath {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("state") != state {
			http.Error(writer, "OAuth state mismatch", http.StatusBadRequest)
			select {
			case callback <- callbackResult{err: "OAuth state mismatch"}:
			default:
			}
			return
		}
		result := callbackResult{code: request.URL.Query().Get("code"), err: request.URL.Query().Get("error")}
		if result.code == "" && result.err == "" {
			result.err = "OAuth callback did not contain a code"
		}
		select {
		case callback <- result:
		default:
		}
		_, _ = io.WriteString(writer, "Authorization received. You may close this window.")
	})}
	serverDone := make(chan error, 1)
	go func() { serverDone <- serverHTTP.Serve(listener) }()
	if openURL == nil {
		return errors.New("MCP OAuth browser opener is unavailable")
	}
	if err := openURL(authorizationURL.String()); err != nil {
		_ = serverHTTP.Close()
		return err
	}
	var result callbackResult
	select {
	case result = <-callback:
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return errors.New("MCP OAuth callback server stopped")
	case <-ctx.Done():
		_ = serverHTTP.Shutdown(context.Background())
		return ctx.Err()
	}
	_ = serverHTTP.Shutdown(context.Background())
	if result.err != "" {
		return errors.New(result.err)
	}
	credential, err := broker.exchange(ctx, endpoints.TokenURL, clientID, clientSecret, redirectURI, verifier, result.code, server)
	if err != nil {
		return err
	}
	credential.ClientID = clientID
	credential.TokenURL = endpoints.TokenURL
	return broker.Store.Put(ctx, mcpOAuthCredentialID(serverName, server), credential)
}

func (broker *OAuthBroker) Unauthenticate(ctx context.Context, serverName string, server config.MCPServerConfig) error {
	if broker == nil || broker.Store == nil {
		return errors.New("MCP OAuth credential store is unavailable")
	}
	return broker.Store.Delete(ctx, mcpOAuthCredentialID(serverName, server))
}

func (broker *OAuthBroker) refresh(ctx context.Context, credentialID string, server config.MCPServerConfig, current OAuthCredential) (OAuthCredential, error) {
	if current.RefreshToken == "" {
		return OAuthCredential{}, errors.New("MCP OAuth credential expired without a refresh token")
	}
	tokenURL := current.TokenURL
	if server.Auth != nil && server.Auth.TokenURL != "" {
		tokenURL = server.Auth.TokenURL
	}
	if server.OAuth != nil && server.OAuth.TokenURL != "" {
		tokenURL = server.OAuth.TokenURL
	}
	if tokenURL == "" {
		return OAuthCredential{}, errors.New("MCP OAuth credential has no token endpoint")
	}
	clientID := current.ClientID
	if configured := oauthClientID(server); configured != "" {
		clientID = configured
	}
	secret := oauthClientSecret(server)
	var err error
	if secret != "" && broker.ResolveSecret != nil {
		secret, err = broker.ResolveSecret(ctx, secret)
		if err != nil {
			return OAuthCredential{}, err
		}
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken}, "client_id": {clientID}}
	if secret != "" {
		values.Set("client_secret", secret)
	}
	if server.Auth != nil && server.Auth.Resource != "" {
		values.Set("resource", server.Auth.Resource)
	}
	refreshed, err := broker.tokenRequest(ctx, tokenURL, values)
	if err != nil {
		return OAuthCredential{}, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	refreshed.ClientID, refreshed.TokenURL = clientID, tokenURL
	if err := broker.Store.Put(ctx, credentialID, refreshed); err != nil {
		return OAuthCredential{}, err
	}
	return refreshed, nil
}

func (broker *OAuthBroker) exchange(ctx context.Context, tokenURL, clientID, clientSecret, redirectURI, verifier, code string, server config.MCPServerConfig) (OAuthCredential, error) {
	values := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	}
	if clientSecret != "" {
		values.Set("client_secret", clientSecret)
	}
	if server.Auth != nil && server.Auth.Resource != "" {
		values.Set("resource", server.Auth.Resource)
	}
	return broker.tokenRequest(ctx, tokenURL, values)
}

func (broker *OAuthBroker) tokenRequest(ctx context.Context, endpoint string, values url.Values) (OAuthCredential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthCredential{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response oauthTokenResponse
	if err := broker.doJSON(request, &response); err != nil {
		return OAuthCredential{}, err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return OAuthCredential{}, errors.New("MCP OAuth token response omitted access_token")
	}
	credential := OAuthCredential{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, TokenType: response.TokenType}
	if response.ExpiresIn > 0 {
		credential.ExpiresAt = time.Now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	return credential, nil
}

func (broker *OAuthBroker) discover(ctx context.Context, server config.MCPServerConfig) (oauthEndpoints, error) {
	endpoints := oauthEndpoints{}
	if server.OAuth != nil {
		endpoints.AuthorizationURL = server.OAuth.AuthorizationURL
		endpoints.TokenURL = server.OAuth.TokenURL
		endpoints.RegistrationURL = server.OAuth.RegistrationURL
		endpoints.Scopes = append([]string(nil), server.OAuth.Scopes...)
	}
	if server.Auth != nil && server.Auth.TokenURL != "" && endpoints.TokenURL == "" {
		endpoints.TokenURL = server.Auth.TokenURL
	}
	if endpoints.AuthorizationURL != "" && endpoints.TokenURL != "" {
		return endpoints, nil
	}
	resourceURL, err := url.Parse(server.URL)
	if err != nil {
		return oauthEndpoints{}, err
	}
	metadataURL := resourceURL.Scheme + "://" + resourceURL.Host + "/.well-known/oauth-protected-resource"
	var protected struct {
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err := broker.doJSON(request, &protected); err != nil || len(protected.AuthorizationServers) == 0 {
		protected.AuthorizationServers = []string{resourceURL.Scheme + "://" + resourceURL.Host}
	}
	if len(endpoints.Scopes) == 0 {
		endpoints.Scopes = protected.Scopes
	}
	authorizationServer := strings.TrimRight(protected.AuthorizationServers[0], "/")
	var metadata struct {
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
	metadataRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, authorizationServer+"/.well-known/oauth-authorization-server", nil)
	if err := broker.doJSON(metadataRequest, &metadata); err != nil {
		metadataRequest, _ = http.NewRequestWithContext(ctx, http.MethodGet, authorizationServer+"/.well-known/openid-configuration", nil)
		if err := broker.doJSON(metadataRequest, &metadata); err != nil {
			return oauthEndpoints{}, fmt.Errorf("discover MCP OAuth metadata: %w", err)
		}
	}
	if endpoints.AuthorizationURL == "" {
		endpoints.AuthorizationURL = metadata.AuthorizationEndpoint
	}
	if endpoints.TokenURL == "" {
		endpoints.TokenURL = metadata.TokenEndpoint
	}
	if endpoints.RegistrationURL == "" {
		endpoints.RegistrationURL = metadata.RegistrationEndpoint
	}
	if len(endpoints.Scopes) == 0 {
		endpoints.Scopes = metadata.ScopesSupported
	}
	if endpoints.AuthorizationURL == "" || endpoints.TokenURL == "" {
		return oauthEndpoints{}, errors.New("MCP OAuth metadata omitted authorization or token endpoint")
	}
	return endpoints, nil
}

func (broker *OAuthBroker) registerClient(ctx context.Context, endpoint, redirectURI string) (string, error) {
	if endpoint == "" {
		return "", nil
	}
	payload, _ := json.Marshal(map[string]any{
		"client_name": "Azem", "redirect_uris": []string{redirectURI},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	var response struct {
		ClientID string `json:"client_id"`
	}
	if err := broker.doJSON(request, &response); err != nil {
		return "", fmt.Errorf("register MCP OAuth client: %w", err)
	}
	return response.ClientID, nil
}

func (broker *OAuthBroker) doJSON(request *http.Request, target any) error {
	client := broker.HTTPClient
	if client == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{}
		}
		transport := base.Clone()
		netproxy.ConfigureTransport(transport)
		client = &http.Client{Transport: transport, Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxOAuthResponseBytes {
		return errors.New("MCP OAuth response exceeds 1 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MCP OAuth HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode MCP OAuth response: %w", err)
	}
	return nil
}

func oauthCallbackListener(server config.MCPServerConfig) (net.Listener, string, string, error) {
	callbackPath := "/callback"
	port := 0
	redirectURI := ""
	if server.OAuth != nil {
		if server.OAuth.CallbackPath != "" {
			callbackPath = server.OAuth.CallbackPath
		}
		port = server.OAuth.CallbackPort
		redirectURI = server.OAuth.RedirectURI
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, "", "", err
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	if redirectURI == "" {
		redirectURI = "http://127.0.0.1:" + strconv.Itoa(actualPort) + callbackPath
	} else if parsed, err := url.Parse(redirectURI); err != nil {
		listener.Close()
		return nil, "", "", errors.New("MCP OAuth redirect_uri is invalid")
	} else if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		if server.OAuth == nil || server.OAuth.CallbackPort <= 0 || parsed.Scheme != "https" {
			listener.Close()
			return nil, "", "", errors.New("MCP OAuth redirect_uri must be loopback HTTP or use callback_port behind HTTPS")
		}
		if server.OAuth.CallbackPath == "" && parsed.Path != "" {
			callbackPath = parsed.Path
		}
	}
	return listener, redirectURI, callbackPath, nil
}

func oauthClientID(server config.MCPServerConfig) string {
	if server.OAuth != nil && server.OAuth.ClientID != "" {
		return server.OAuth.ClientID
	}
	if server.Auth != nil {
		return server.Auth.ClientID
	}
	return ""
}

func oauthClientSecret(server config.MCPServerConfig) string {
	if server.RuntimeOAuthClientSecret != "" {
		return server.RuntimeOAuthClientSecret
	}
	if server.RuntimeAuthClientSecret != "" {
		return server.RuntimeAuthClientSecret
	}
	if server.OAuth != nil && server.OAuth.ClientSecret != "" {
		return server.OAuth.ClientSecret
	}
	if server.Auth != nil {
		return server.Auth.ClientSecret
	}
	return ""
}

func mcpOAuthCredentialID(_ string, server config.MCPServerConfig) string {
	if server.Auth != nil && strings.TrimSpace(server.Auth.CredentialID) != "" {
		return strings.TrimSpace(server.Auth.CredentialID)
	}
	sum := sha256.Sum256([]byte(server.URL))
	return "mcp_oauth_" + hex.EncodeToString(sum[:12])
}

func randomOAuthValue(size int) (string, error) {
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
