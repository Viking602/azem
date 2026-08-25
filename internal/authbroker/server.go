package authbroker

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth"
)

const (
	RemoteRefreshSentinel = "__AZEM_REMOTE_REFRESH__"
	maxRequestBytes       = 1 << 20
	maxSnapshotWait       = 30 * time.Second
)

type Credential struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	AccountID    string    `json:"accountId"`
	IdentityKey  string    `json:"identityKey"`
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"displayName,omitempty"`
	Plan         string    `json:"plan,omitempty"`
	Status       string    `json:"status"`
}

type Block struct {
	CredentialID string    `json:"credentialId"`
	Provider     string    `json:"provider"`
	Scope        string    `json:"scope"`
	BlockedUntil time.Time `json:"blockedUntil"`
	Reason       string    `json:"reason,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Snapshot struct {
	Version     int          `json:"version"`
	Generation  int64        `json:"generation"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Credentials []Credential `json:"credentials"`
	Blocks      []Block      `json:"blocks"`
}

type Server struct {
	db      *sql.DB
	auth    *auth.Service
	tokens  [][]byte
	version string
	now     func() time.Time
}

type Options struct {
	DB      *sql.DB
	Auth    *auth.Service
	Tokens  []string
	Version string
}

func New(options Options) (*Server, error) {
	if options.DB == nil || options.Auth == nil {
		return nil, errors.New("auth broker requires database and auth service")
	}
	tokens := make([][]byte, 0, len(options.Tokens))
	for _, token := range options.Tokens {
		token = strings.TrimSpace(token)
		if len(token) < 32 || strings.ContainsAny(token, "\r\n\x00") {
			return nil, errors.New("auth broker tokens must contain at least 32 safe characters")
		}
		tokens = append(tokens, []byte(token))
	}
	if len(tokens) == 0 {
		return nil, errors.New("auth broker requires at least one bearer token")
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	return &Server{db: options.DB, auth: options.Auth, tokens: tokens, version: options.Version, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", server.health)
	mux.Handle("/v1/", server.requireBearer(http.HandlerFunc(server.route)))
	return securityHeaders(mux)
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "version": server.version})
}

func (server *Server) route(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/v1/snapshot":
		server.snapshot(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/snapshot/stream":
		server.snapshotStream(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/credential":
		server.putCredential(writer, request)
	case strings.HasPrefix(request.URL.Path, "/v1/credential/"):
		server.credentialAction(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/credentials/disabled":
		server.disabled(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/usage":
		server.usageHistory(writer, request, false)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/usage/history":
		server.usageHistory(writer, request, false)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/usage/clients":
		server.usageHistory(writer, request, true)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/usage/observed":
		server.observeUsage(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/usage/stale":
		server.staleUsage(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

func (server *Server) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := request.Header.Get("Authorization")
		prefix := "Bearer "
		if !strings.HasPrefix(value, prefix) || !server.validToken([]byte(strings.TrimSpace(strings.TrimPrefix(value, prefix)))) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="azem-auth-broker"`)
			writeError(writer, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) validToken(presented []byte) bool {
	valid := 0
	for _, expected := range server.tokens {
		equal := subtle.ConstantTimeEq(int32(len(presented)), int32(len(expected)))
		padded := make([]byte, len(expected))
		copy(padded, presented)
		valid |= equal & subtle.ConstantTimeCompare(padded, expected)
	}
	return valid == 1
}

func (server *Server) snapshot(writer http.ResponseWriter, request *http.Request) {
	generation, err := server.generation(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	wait := parseWait(request.URL.Query().Get("wait"))
	if matchGeneration(request.Header.Get("If-None-Match"), generation) && wait > 0 {
		deadline := time.NewTimer(wait)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for {
			select {
			case <-request.Context().Done():
				server.snapshotHeaders(writer, generation)
				writer.WriteHeader(499)
				return
			case <-deadline.C:
				server.snapshotHeaders(writer, generation)
				writer.WriteHeader(http.StatusNotModified)
				return
			case <-ticker.C:
				current, readErr := server.generation(request.Context())
				if readErr != nil {
					writeError(writer, http.StatusInternalServerError, readErr.Error())
					return
				}
				if current != generation {
					generation = current
					goto changed
				}
			}
		}
	}
changed:
	snapshot, err := server.buildSnapshot(request.Context(), generation)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	server.snapshotHeaders(writer, generation)
	writeJSON(writer, http.StatusOK, snapshot)
}

func (server *Server) snapshotHeaders(writer http.ResponseWriter, generation int64) {
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(generation, 10)))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Vary", "Azem-Auth-Broker-Capabilities")
}

func (server *Server) snapshotStream(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusNotImplemented, "streaming unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	last := int64(-1)
	ticker := time.NewTicker(15 * time.Second)
	poll := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	defer poll.Stop()
	for {
		generation, err := server.generation(request.Context())
		if err != nil {
			return
		}
		if generation != last {
			snapshot, buildErr := server.buildSnapshot(request.Context(), generation)
			if buildErr != nil {
				return
			}
			encoded, _ := json.Marshal(snapshot)
			_, _ = fmt.Fprintf(writer, "event: snapshot\ndata: %s\n\n", encoded)
			flusher.Flush()
			last = generation
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			_, _ = io.WriteString(writer, ": keepalive\n\n")
			flusher.Flush()
		case <-poll.C:
		}
	}
}

func (server *Server) buildSnapshot(ctx context.Context, generation int64) (Snapshot, error) {
	accounts, err := server.auth.Accounts(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	credentials := make([]Credential, 0, len(accounts))
	for _, account := range accounts {
		view := Credential{ID: credentialID(account.Provider, account.ID), Provider: account.Provider, AccountID: account.ID, IdentityKey: identityKey(account), Email: account.Email, DisplayName: account.DisplayName, Plan: account.Plan, Status: account.Status}
		if account.Status == "active" {
			stored, credentialErr := server.auth.StoredCredential(ctx, account.Provider, account.ID)
			if credentialErr != nil {
				return Snapshot{}, credentialErr
			}
			view.AccessToken, view.TokenType, view.ExpiresAt = stored.AccessToken, stored.TokenType, stored.ExpiresAt
			if stored.RefreshToken != "" {
				view.RefreshToken = RemoteRefreshSentinel
			}
		}
		credentials = append(credentials, view)
	}
	blocks, err := server.blocks(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Version: 1, Generation: generation, GeneratedAt: server.now(), Credentials: credentials, Blocks: blocks}, nil
}

func (server *Server) putCredential(writer http.ResponseWriter, request *http.Request) {
	var credential auth.Credential
	if err := decodeBody(request, &credential); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	account, err := server.auth.StoreCredential(request.Context(), credential)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	generation, _ := server.generation(request.Context())
	view := Credential{ID: credentialID(account.Provider, account.ID), Provider: account.Provider, AccountID: account.ID, IdentityKey: identityKey(account), AccessToken: credential.AccessToken, TokenType: credential.TokenType, ExpiresAt: credential.ExpiresAt, Email: account.Email, DisplayName: account.DisplayName, Plan: account.Plan, Status: account.Status}
	if credential.RefreshToken != "" {
		view.RefreshToken = RemoteRefreshSentinel
	}
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(generation, 10)))
	writeJSON(writer, http.StatusOK, view)
}

func (server *Server) credentialAction(writer http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/v1/credential/")
	parts := strings.Split(remainder, "/")
	if len(parts) < 2 || !validCredentialID(parts[0]) {
		writeError(writer, http.StatusNotFound, "credential not found")
		return
	}
	provider, accountID, err := server.resolveCredential(request.Context(), parts[0])
	if err != nil {
		writeError(writer, http.StatusNotFound, err.Error())
		return
	}
	switch {
	case request.Method == http.MethodPost && parts[1] == "refresh":
		credential, err := server.auth.Refresh(request.Context(), provider, accountID)
		if err != nil {
			writeError(writer, http.StatusBadGateway, err.Error())
			return
		}
		account := auth.Account{ID: accountID, Provider: provider, Email: credential.Email, DisplayName: credential.DisplayName, Plan: credential.Plan, Status: "active"}
		view := Credential{ID: parts[0], Provider: provider, AccountID: accountID, IdentityKey: identityKey(account), AccessToken: credential.AccessToken, TokenType: credential.TokenType, ExpiresAt: credential.ExpiresAt, Email: credential.Email, DisplayName: credential.DisplayName, Plan: credential.Plan, Status: "active"}
		if credential.RefreshToken != "" {
			view.RefreshToken = RemoteRefreshSentinel
		}
		writeJSON(writer, http.StatusOK, view)
	case request.Method == http.MethodPost && parts[1] == "disable":
		var body struct {
			Cause string `json:"cause"`
		}
		if err := decodeBody(request, &body); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		now := server.now().UnixNano()
		if _, err := server.db.ExecContext(request.Context(), `UPDATE accounts SET status='disabled',updated_at=? WHERE provider_id=? AND id=?`, now, provider, accountID); err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		_, _ = server.db.ExecContext(request.Context(), `INSERT INTO auth_broker_disabled(credential_id,provider_id,account_id,cause,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(credential_id) DO UPDATE SET cause=excluded.cause,updated_at=excluded.updated_at`, parts[0], provider, accountID, strings.TrimSpace(body.Cause), now)
		server.bumpGeneration(request.Context())
		writeJSON(writer, http.StatusOK, map[string]bool{"disabled": true})
	case request.Method == http.MethodPost && parts[1] == "block":
		var body struct {
			Scope        string    `json:"scope"`
			BlockedUntil time.Time `json:"blockedUntil"`
			Reason       string    `json:"reason"`
		}
		if err := decodeBody(request, &body); err != nil || body.Scope == "" || body.BlockedUntil.IsZero() {
			writeError(writer, http.StatusBadRequest, "scope and blockedUntil are required")
			return
		}
		now := server.now().UnixNano()
		_, err := server.db.ExecContext(request.Context(), `INSERT INTO auth_broker_blocks(credential_id,provider_id,scope,blocked_until,reason,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(credential_id,scope) DO UPDATE SET provider_id=excluded.provider_id,blocked_until=excluded.blocked_until,reason=excluded.reason,updated_at=excluded.updated_at`, parts[0], provider, body.Scope, body.BlockedUntil.UnixNano(), body.Reason, now)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		server.bumpGeneration(request.Context())
		writeJSON(writer, http.StatusOK, map[string]bool{"blocked": true})
	case request.Method == http.MethodDelete && parts[1] == "blocks":
		_, err := server.db.ExecContext(request.Context(), `DELETE FROM auth_broker_blocks WHERE credential_id=?`, parts[0])
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		server.bumpGeneration(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

func (server *Server) disabled(writer http.ResponseWriter, request *http.Request) {
	provider := strings.TrimSpace(request.URL.Query().Get("provider"))
	query := `SELECT credential_id,provider_id,account_id,cause,updated_at FROM auth_broker_disabled`
	args := []any{}
	if provider != "" {
		query += ` WHERE provider_id=?`
		args = append(args, provider)
	}
	query += ` ORDER BY updated_at DESC,credential_id`
	rows, err := server.db.QueryContext(request.Context(), query, args...)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	values := make([]map[string]any, 0)
	for rows.Next() {
		var id, providerID, accountID, cause string
		var updated int64
		if rows.Scan(&id, &providerID, &accountID, &cause, &updated) == nil {
			values = append(values, map[string]any{"id": id, "provider": providerID, "accountId": accountID, "cause": cause, "updatedAt": time.Unix(0, updated).UTC()})
		}
	}
	writeJSON(writer, http.StatusOK, values)
}

func (server *Server) observeUsage(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ID           string          `json:"id"`
		ClientID     string          `json:"clientId"`
		CredentialID string          `json:"credentialId"`
		Provider     string          `json:"provider"`
		AccountID    string          `json:"accountId"`
		Payload      json.RawMessage `json:"payload"`
		ObservedAt   time.Time       `json:"observedAt"`
	}
	if err := decodeBody(request, &body); err != nil || body.Provider == "" || !json.Valid(body.Payload) {
		writeError(writer, http.StatusBadRequest, "provider and JSON payload are required")
		return
	}
	if body.ID == "" {
		body.ID = observationID(body.ClientID, body.Provider, body.AccountID, body.ObservedAt)
	}
	if body.ObservedAt.IsZero() {
		body.ObservedAt = server.now()
	}
	_, err := server.db.ExecContext(request.Context(), `INSERT INTO auth_broker_usage_observations(id,client_id,credential_id,provider_id,account_id,payload,observed_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, body.ID, body.ClientID, body.CredentialID, body.Provider, body.AccountID, []byte(body.Payload), body.ObservedAt.UnixNano())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"recorded": true})
}

func (server *Server) usageHistory(writer http.ResponseWriter, request *http.Request, clients bool) {
	since, _ := strconv.ParseInt(request.URL.Query().Get("sinceMs"), 10, 64)
	provider := strings.TrimSpace(request.URL.Query().Get("provider"))
	query := `SELECT id,client_id,credential_id,provider_id,account_id,payload,observed_at FROM auth_broker_usage_observations WHERE observed_at>=?`
	args := []any{since * int64(time.Millisecond)}
	if provider != "" {
		query += ` AND provider_id=?`
		args = append(args, provider)
	}
	if clients {
		query += ` AND client_id<>''`
	}
	query += ` ORDER BY observed_at DESC,id LIMIT 10000`
	rows, err := server.db.QueryContext(request.Context(), query, args...)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	values := make([]map[string]any, 0)
	for rows.Next() {
		var id, clientID, credentialID, providerID, accountID string
		var payload []byte
		var observed int64
		if rows.Scan(&id, &clientID, &credentialID, &providerID, &accountID, &payload, &observed) == nil {
			values = append(values, map[string]any{"id": id, "clientId": clientID, "credentialId": credentialID, "provider": providerID, "accountId": accountID, "payload": json.RawMessage(payload), "observedAt": time.Unix(0, observed).UTC()})
		}
	}
	writeJSON(writer, http.StatusOK, values)
}

func (server *Server) staleUsage(writer http.ResponseWriter, request *http.Request) {
	_, err := server.db.ExecContext(request.Context(), `DELETE FROM auth_broker_usage_observations`)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) blocks(ctx context.Context) ([]Block, error) {
	rows, err := server.db.QueryContext(ctx, `SELECT credential_id,provider_id,scope,blocked_until,reason,updated_at FROM auth_broker_blocks ORDER BY provider_id,credential_id,scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Block, 0)
	for rows.Next() {
		var value Block
		var blocked, updated int64
		if err := rows.Scan(&value.CredentialID, &value.Provider, &value.Scope, &blocked, &value.Reason, &updated); err != nil {
			return nil, err
		}
		value.BlockedUntil, value.UpdatedAt = time.Unix(0, blocked).UTC(), time.Unix(0, updated).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (server *Server) generation(ctx context.Context) (int64, error) {
	var generation int64
	err := server.db.QueryRowContext(ctx, `SELECT generation FROM auth_broker_state WHERE id=1`).Scan(&generation)
	return generation, err
}

func (server *Server) bumpGeneration(ctx context.Context) {
	_, _ = server.db.ExecContext(ctx, `UPDATE auth_broker_state SET generation=generation+1,updated_at=? WHERE id=1`, server.now().UnixNano())
}

func (server *Server) resolveCredential(ctx context.Context, id string) (string, string, error) {
	accounts, err := server.auth.Accounts(ctx, "")
	if err != nil {
		return "", "", err
	}
	for _, account := range accounts {
		if credentialID(account.Provider, account.ID) == id {
			return account.Provider, account.ID, nil
		}
	}
	return "", "", auth.ErrCredentialNotFound
}

func credentialID(provider, accountID string) string {
	digest := sha256.Sum256([]byte(provider + "\x00" + accountID))
	return hex.EncodeToString(digest[:16])
}

func validCredentialID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func identityKey(account auth.Account) string {
	if email := strings.ToLower(strings.TrimSpace(account.Email)); email != "" {
		return "email:" + email
	}
	return "account:" + account.ID
}

func observationID(client, provider, account string, observed time.Time) string {
	digest := sha256.Sum256([]byte(client + "\x00" + provider + "\x00" + account + "\x00" + observed.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(digest[:16])
}

func parseWait(raw string) time.Duration {
	milliseconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || milliseconds <= 0 {
		return 0
	}
	value := time.Duration(int64(milliseconds)) * time.Millisecond
	if value > maxSnapshotWait {
		return maxSnapshotWait
	}
	return value
}

func matchGeneration(raw string, generation int64) bool {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "W/"))
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = unquoted
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && value >= 0 && value == generation
}

func decodeBody(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxRequestBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func ParseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("broker URL must be absolute without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return nil, errors.New("broker URL must use HTTPS outside loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
