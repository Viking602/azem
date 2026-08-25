package authbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

type ClientOptions struct {
	BaseURL     string
	Token       string
	HTTPClient  *http.Client
	Cache       SnapshotCache
	AccountPool map[string][]string
}

type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
	cache   SnapshotCache
	pool    map[string]map[string]struct{}

	mu       sync.Mutex
	snapshot Snapshot
	loadedAt time.Time
	etag     string
}

func NewClient(options ClientOptions) (*Client, error) {
	baseURL, err := ParseBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(options.Token)
	if len(token) < 32 {
		return nil, errors.New("broker client token must contain at least 32 characters")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	pool := make(map[string]map[string]struct{}, len(options.AccountPool))
	for provider, identities := range options.AccountPool {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			return nil, errors.New("broker account pool provider is empty")
		}
		allowed := make(map[string]struct{}, len(identities))
		for _, identity := range identities {
			identity = strings.TrimSpace(identity)
			if identity == "" {
				return nil, fmt.Errorf("broker account pool %s contains an empty identity", provider)
			}
			allowed[identity] = struct{}{}
		}
		pool[provider] = allowed
	}
	return &Client{baseURL: baseURL, token: token, http: &copyClient, cache: options.Cache, pool: pool}, nil
}

func (client *Client) Health(ctx context.Context) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint("/v1/healthz"), nil)
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("broker health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (client *Client) FetchSnapshot(ctx context.Context) (Snapshot, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.fetchSnapshotLocked(ctx)
}

func (client *Client) fetchSnapshotLocked(ctx context.Context) (Snapshot, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint("/v1/snapshot"), nil)
	client.authorize(request)
	if client.etag != "" {
		request.Header.Set("If-None-Match", client.etag)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return client.cachedFallback(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Snapshot{}, fmt.Errorf("broker authentication failed with HTTP %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusNotModified && client.snapshot.Version != 0 {
		client.loadedAt = time.Now().UTC()
		return client.filtered(client.snapshot), nil
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return client.cachedFallback(fmt.Errorf("broker snapshot returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body))))
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<20))
	if err := decoder.Decode(&snapshot); err != nil || snapshot.Version != 1 {
		return client.cachedFallback(errors.New("broker returned an invalid snapshot"))
	}
	client.snapshot, client.loadedAt, client.etag = snapshot, time.Now().UTC(), response.Header.Get("ETag")
	if client.cache.Path != "" && client.cache.TTL > 0 {
		_ = client.cache.Write(client.baseURL.String(), client.token, snapshot)
	}
	return client.filtered(snapshot), nil
}

func (client *Client) cachedFallback(cause error) (Snapshot, error) {
	if client.snapshot.Version == 1 {
		return client.filtered(client.snapshot), nil
	}
	if client.cache.Path != "" && client.cache.TTL > 0 {
		snapshot, err := client.cache.Read(client.baseURL.String(), client.token)
		if err == nil {
			client.snapshot, client.loadedAt = snapshot, time.Now().UTC()
			return client.filtered(snapshot), nil
		}
	}
	return Snapshot{}, cause
}

func (client *Client) Credential(ctx context.Context, provider, accountID string) (Credential, error) {
	snapshot, err := client.FetchSnapshot(ctx)
	if err != nil {
		return Credential{}, err
	}
	for _, credential := range snapshot.Credentials {
		if credential.Provider == provider && credential.AccountID == accountID && credential.Status == "active" {
			if !credential.ExpiresAt.IsZero() && time.Until(credential.ExpiresAt) < time.Minute && credential.RefreshToken == RemoteRefreshSentinel {
				return client.Refresh(ctx, credential.ID)
			}
			return credential, nil
		}
	}
	return Credential{}, errors.New("broker credential not found")
}

func (client *Client) Refresh(ctx context.Context, credentialID string) (Credential, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint("/v1/credential/"+url.PathEscape(credentialID)+"/refresh"), bytes.NewReader([]byte(`{}`)))
	client.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Credential{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return Credential{}, fmt.Errorf("broker refresh returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var credential Credential
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&credential) != nil || credential.ID != credentialID {
		return Credential{}, errors.New("broker returned an invalid refreshed credential")
	}
	client.mu.Lock()
	client.snapshot = Snapshot{}
	client.etag = ""
	client.mu.Unlock()
	return credential, nil
}

func (client *Client) ObserveUsage(ctx context.Context, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint("/v1/usage/observed"), bytes.NewReader(encoded))
	client.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("broker usage observation returned HTTP %d", response.StatusCode)
	}

	return nil
}

func (client *Client) Get(ctx context.Context, endpointPath string) ([]byte, int, error) {
	if !strings.HasPrefix(endpointPath, "/v1/") || strings.Contains(endpointPath, "..") {
		return nil, 0, errors.New("invalid broker endpoint path")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint(endpointPath), nil)
	client.authorize(request)
	response, err := client.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return body, response.StatusCode, fmt.Errorf("broker endpoint returned HTTP %d", response.StatusCode)
	}
	return body, response.StatusCode, nil
}

func (client *Client) filtered(snapshot Snapshot) Snapshot {
	filtered := cloneSnapshot(snapshot)
	if len(client.pool) == 0 {
		return filtered
	}
	filtered.Credentials = make([]Credential, 0, len(snapshot.Credentials))
	visible := make(map[string]struct{})
	for _, credential := range snapshot.Credentials {
		allowed, restricted := client.pool[credential.Provider]
		if restricted && credential.RefreshToken == RemoteRefreshSentinel {
			if _, ok := allowed[credential.IdentityKey]; !ok {
				continue
			}
		}
		filtered.Credentials = append(filtered.Credentials, credential)
		visible[credential.ID] = struct{}{}
	}
	filtered.Blocks = make([]Block, 0, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		if _, ok := visible[block.CredentialID]; ok {
			filtered.Blocks = append(filtered.Blocks, block)
		}
	}
	return filtered
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Credentials = append([]Credential(nil), snapshot.Credentials...)
	snapshot.Blocks = append([]Block(nil), snapshot.Blocks...)
	return snapshot
}

func (client *Client) authorize(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
}

func (client *Client) endpoint(value string) string {
	endpoint := *client.baseURL
	endpoint.Path = path.Join(client.baseURL.Path, value)
	return endpoint.String()
}
