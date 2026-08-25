package authgateway

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/authbroker"
	"github.com/Viking602/azem/internal/netproxy"
)

const maxGatewayRequestBytes = 64 << 20

type Route struct {
	Provider   string
	BaseURL    string
	Models     []string
	Protocols  []string
	AuthHeader string
	AuthPrefix string
	Headers    map[string]string
}

type Options struct {
	Broker     *authbroker.Client
	Token      string
	NoAuth     bool
	Routes     []Route
	HTTPClient *http.Client
	Version    string
}

type Server struct {
	broker  *authbroker.Client
	token   []byte
	noAuth  bool
	routes  []resolvedRoute
	http    *http.Client
	version string
}

type resolvedRoute struct {
	Route
	base *url.URL
}

func New(options Options) (*Server, error) {
	if options.Broker == nil {
		return nil, errors.New("auth gateway requires a broker client")
	}
	token := strings.TrimSpace(options.Token)
	if !options.NoAuth && len(token) < 32 {
		return nil, errors.New("auth gateway token must contain at least 32 characters")
	}
	routes := make([]resolvedRoute, 0, len(options.Routes))
	modelOwners := make(map[string]string)
	for _, route := range options.Routes {
		route.Provider = strings.TrimSpace(route.Provider)
		if route.Provider == "" || len(route.Models) == 0 || len(route.Protocols) == 0 {
			return nil, errors.New("gateway routes require provider, models, and protocols")
		}
		base, err := parseProviderURL(route.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("gateway route %s: %w", route.Provider, err)
		}
		for _, model := range route.Models {
			model = strings.TrimSpace(model)
			if model == "" {
				return nil, fmt.Errorf("gateway route %s has an empty model", route.Provider)
			}
			key := route.Provider + "\x00" + model
			if previous := modelOwners[key]; previous != "" {
				return nil, fmt.Errorf("gateway model %s/%s is duplicated", route.Provider, model)
			}
			modelOwners[key] = route.Provider
		}
		if route.AuthHeader == "" {
			route.AuthHeader = "Authorization"
		}
		if route.AuthPrefix == "" && strings.EqualFold(route.AuthHeader, "Authorization") {
			route.AuthPrefix = "Bearer "
		}
		routes = append(routes, resolvedRoute{Route: route, base: base})
	}
	if len(routes) == 0 {
		return nil, errors.New("auth gateway requires at least one provider route")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		netproxy.ConfigureTransport(transport)
		httpClient = &http.Client{Transport: transport, Timeout: 0}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if options.Version == "" {
		options.Version = "dev"
	}
	return &Server{broker: options.Broker, token: []byte(token), noAuth: options.NoAuth, routes: routes, http: &copyClient, version: options.Version}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.Handle("/v1/", server.requireBearer(http.HandlerFunc(server.route)))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(writer, request)
	})
}

func (server *Server) health(writer http.ResponseWriter, request *http.Request) {
	err := server.broker.Health(request.Context())
	status := http.StatusOK
	if err != nil {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, map[string]any{"ok": err == nil, "version": server.version, "broker": err == nil})
}

func (server *Server) route(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/v1/models":
		server.models(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/credentials/check":
		server.checkCredentials(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/usage":
		server.usage(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/chat/completions":
		server.proxy(writer, request, "openai-chat")
	case request.Method == http.MethodPost && request.URL.Path == "/v1/responses":
		server.proxy(writer, request, "openai-responses")
	case request.Method == http.MethodPost && request.URL.Path == "/v1/messages":
		server.proxy(writer, request, "anthropic-messages")
	case request.Method == http.MethodPost && request.URL.Path == "/v1/pi/stream":
		server.proxy(writer, request, "pi-native")
	default:
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (server *Server) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if server.noAuth {
			next.ServeHTTP(writer, request)
			return
		}
		value := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
		presented := []byte(value)
		padded := make([]byte, len(server.token))
		copy(padded, presented)
		valid := subtle.ConstantTimeEq(int32(len(presented)), int32(len(server.token))) & subtle.ConstantTimeCompare(padded, server.token)
		if valid != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="azem-auth-gateway"`)
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) models(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := server.broker.FetchSnapshot(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	providers := activeProviders(snapshot)
	data := make([]map[string]any, 0)
	for _, route := range server.routes {
		if _, ok := providers[route.Provider]; !ok {
			continue
		}
		for _, model := range route.Models {
			data = append(data, map[string]any{"id": model, "object": "model", "owned_by": route.Provider})
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (server *Server) checkCredentials(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := server.broker.FetchSnapshot(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	values := make([]map[string]any, 0, len(snapshot.Credentials))
	for _, credential := range snapshot.Credentials {
		available := credential.Status == "active" && credential.AccessToken != "" && (credential.ExpiresAt.IsZero() || credential.ExpiresAt.After(now)) && !blocked(snapshot.Blocks, credential.ID, now)
		values = append(values, map[string]any{"id": credential.ID, "provider": credential.Provider, "accountId": credential.AccountID, "identityKey": credential.IdentityKey, "available": available, "expiresAt": credential.ExpiresAt})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"credentials": values})
}

func (server *Server) usage(writer http.ResponseWriter, request *http.Request) {
	payload, status, err := server.broker.Get(request.Context(), "/v1/usage")
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}

func (server *Server) proxy(writer http.ResponseWriter, request *http.Request, protocol string) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxGatewayRequestBytes+1))
	request.Body.Close()
	if err != nil || len(body) > maxGatewayRequestBytes {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body is oversized"})
		return
	}
	var header struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &header) != nil || strings.TrimSpace(header.Model) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "model is required"})
		return
	}
	providerHint := strings.TrimSpace(request.Header.Get("X-Azem-Provider"))
	route, err := server.selectRoute(header.Model, providerHint, protocol)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	snapshot, err := server.broker.FetchSnapshot(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	credential, err := selectCredential(snapshot, route.Provider, request.Header.Get("X-Azem-Account-ID"), time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	upstream := *route.base
	inboundPath := request.URL.Path
	if strings.HasSuffix(route.base.Path, "/v1") && strings.HasPrefix(inboundPath, "/v1/") {
		inboundPath = strings.TrimPrefix(inboundPath, "/v1")
	}
	upstream.Path = path.Join(route.base.Path, inboundPath)
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodPost, upstream.String(), bytes.NewReader(body))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	copySafeHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Del("Authorization")
	upstreamRequest.Header.Del("X-Api-Key")
	upstreamRequest.Header.Del("X-Azem-Provider")
	upstreamRequest.Header.Del("X-Azem-Account-ID")
	upstreamRequest.Header.Set(route.AuthHeader, route.AuthPrefix+credential.AccessToken)
	for key, value := range route.Headers {
		upstreamRequest.Header.Set(key, value)
	}
	response, err := server.http.Do(upstreamRequest)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer response.Body.Close()
	copyResponseHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	buffer := make([]byte, 32<<10)
	flusher, _ := writer.(http.Flusher)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, writeErr := writer.Write(buffer[:count]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (server *Server) selectRoute(model, provider, protocol string) (resolvedRoute, error) {
	var selected []resolvedRoute
	for _, route := range server.routes {
		if provider != "" && route.Provider != provider {
			continue
		}
		if slices.Contains(route.Models, model) && slices.Contains(route.Protocols, protocol) {
			selected = append(selected, route)
		}
	}
	if len(selected) == 0 {
		return resolvedRoute{}, fmt.Errorf("no %s route for model %s", protocol, model)
	}
	if len(selected) > 1 {
		return resolvedRoute{}, errors.New("model is ambiguous; send X-Azem-Provider")
	}
	return selected[0], nil
}

func activeProviders(snapshot authbroker.Snapshot) map[string]struct{} {
	result := make(map[string]struct{})
	for _, credential := range snapshot.Credentials {
		if credential.Status == "active" && credential.AccessToken != "" {
			result[credential.Provider] = struct{}{}
		}
	}
	return result
}

func selectCredential(snapshot authbroker.Snapshot, provider, accountID string, now time.Time) (authbroker.Credential, error) {
	for _, credential := range snapshot.Credentials {
		if credential.Provider != provider || credential.Status != "active" || credential.AccessToken == "" || (accountID != "" && credential.AccountID != accountID) || (!credential.ExpiresAt.IsZero() && !credential.ExpiresAt.After(now)) || blocked(snapshot.Blocks, credential.ID, now) {
			continue
		}
		return credential, nil
	}
	return authbroker.Credential{}, fmt.Errorf("no available credential for provider %s", provider)
}

func blocked(blocks []authbroker.Block, credentialID string, now time.Time) bool {
	for _, block := range blocks {
		if block.CredentialID == credentialID && block.BlockedUntil.After(now) {
			return true
		}
	}
	return false
}

func copySafeHeaders(target, source http.Header) {
	for _, key := range []string{"Content-Type", "Accept", "Accept-Encoding", "User-Agent", "OpenAI-Beta", "Anthropic-Version", "Anthropic-Beta"} {
		for _, value := range source.Values(key) {
			target.Add(key, value)
		}
	}
}

func copyResponseHeaders(target, source http.Header) {
	for _, key := range []string{"Content-Type", "Cache-Control", "OpenAI-Request-ID", "Request-ID", "Anthropic-Request-ID", "X-Request-ID"} {
		for _, value := range source.Values(key) {
			target.Add(key, value)
		}
	}
}

func parseProviderURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("provider URL must be absolute without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback(parsed.Hostname())) {
		return nil, errors.New("provider URL must use HTTPS outside loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	value := net.ParseIP(host)
	return value != nil && value.IsLoopback()
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
