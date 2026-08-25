package sessionshare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type Store string

const (
	StoreBlob Store = "blob"
	StoreGist Store = "gist"
)

type ShareSession struct {
	Metadata    session.Session      `json:"metadata"`
	Tree        session.SessionTree  `json:"tree"`
	Blocks      []session.Block      `json:"blocks"`
	ToolRecords []session.ToolRecord `json:"toolRecords,omitempty"`
}

type Options struct {
	ServerURL        string   `json:"serverUrl"`
	Store            Store    `json:"store,omitempty"`
	AllBranches      bool     `json:"allBranches,omitempty"`
	DisableRedaction bool     `json:"disableRedaction,omitempty"`
	Secrets          []string `json:"-"`
}

type Result struct {
	URL         string `json:"url"`
	Method      string `json:"method"`
	GistURL     string `json:"gistUrl,omitempty"`
	Truncated   bool   `json:"truncated"`
	SealedBytes int    `json:"sealedBytes"`
}

type GistPublisher interface {
	Publish(ctx context.Context, filename, content string) (gistURL string, gistID string, err error)
}

type Client struct {
	Sessions   *session.Service
	HTTPClient *http.Client
	Gists      GistPublisher
	Now        func() time.Time
}

func New(sessions *session.Service) *Client {
	return &Client{Sessions: sessions, HTTPClient: &http.Client{Timeout: 30 * time.Second}, Gists: GHGistPublisher{}, Now: func() time.Time { return time.Now().UTC() }}
}

func (client *Client) Share(ctx context.Context, sessionID string, options Options) (Result, error) {
	if client == nil || client.Sessions == nil {
		return Result{}, errors.New("session sharing is unavailable")
	}
	serverURL, err := validateServerURL(options.ServerURL)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := client.Sessions.LoadExportSnapshot(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return Result{}, err
	}
	blocks := snapshot.ActiveBlocks
	tools := snapshot.ToolRecords
	if options.AllBranches {
		blocks = snapshot.AllBlocks
	} else {
		active := make(map[int64]struct{}, len(snapshot.ActiveBlocks))
		for _, block := range snapshot.ActiveBlocks {
			active[block.Sequence] = struct{}{}
		}
		filtered := make([]session.ToolRecord, 0, len(tools))
		for _, record := range tools {
			if _, ok := active[record.AnchorSequence]; ok {
				filtered = append(filtered, record)
			}
		}
		tools = filtered
	}
	document := Document{
		Version:  shareDocumentVersion,
		SharedAt: time.Now().UTC(),
		Session:  ShareSession{Metadata: snapshot.Session, Tree: snapshot.Tree, Blocks: blocks, ToolRecords: tools},
	}
	if client.Now != nil {
		document.SharedAt = client.Now().UTC()
	}
	if !options.DisableRedaction {
		document = redactDocument(document, options.Secrets)
	}
	key := make([]byte, KeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return Result{}, err
	}
	store := options.Store
	if store == "" {
		store = StoreBlob
	}
	if store != StoreBlob && store != StoreGist {
		return Result{}, fmt.Errorf("unsupported share store %q", store)
	}
	limit := ServerMaxSealedBytes
	if store == StoreGist {
		limit = GistMaxSealedBytes
	}
	sealed, truncated, err := fitAndSeal(document, key, limit)
	if err != nil {
		if store != StoreGist {
			return Result{}, err
		}
		sealed, truncated, err = fitAndSeal(document, key, ServerMaxSealedBytes)
		if err != nil {
			return Result{}, err
		}
		return client.uploadBlob(ctx, serverURL, sealed, key, truncated)
	}
	if store == StoreGist && client.Gists != nil {
		gistURL, gistID, gistErr := client.Gists.Publish(ctx, "session.ompshare.txt", base64.StdEncoding.EncodeToString(sealed))
		if gistErr == nil {
			if !validGistID(gistID) {
				return Result{}, errors.New("gist publisher returned an invalid id")
			}
			return Result{URL: shareViewerURL(serverURL, gistID, key), Method: "gist", GistURL: gistURL, Truncated: truncated, SealedBytes: len(sealed)}, nil
		}
		sealed, truncated, err = fitAndSeal(document, key, ServerMaxSealedBytes)
		if err != nil {
			return Result{}, fmt.Errorf("gist share failed (%v), and server fallback cannot fit: %w", gistErr, err)
		}
	} else if store == StoreGist {
		sealed, truncated, err = fitAndSeal(document, key, ServerMaxSealedBytes)
		if err != nil {
			return Result{}, err
		}
	}
	return client.uploadBlob(ctx, serverURL, sealed, key, truncated)
}

func (client *Client) uploadBlob(ctx context.Context, serverURL *url.URL, sealed, key []byte, truncated bool) (Result, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL.String(), bytes.NewReader(sealed))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("upload encrypted share: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Result{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("share server returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !validServerShareID(payload.ID) {
		return Result{}, errors.New("share server returned an invalid id")
	}
	return Result{URL: shareViewerURL(serverURL, payload.ID, key), Method: "server", Truncated: truncated, SealedBytes: len(sealed)}, nil
}

func validateServerURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("share server URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("share server URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("share server URL must use HTTPS (HTTP is allowed only for loopback testing)")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

var (
	shareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	gistIDPattern  = regexp.MustCompile(`^[0-9a-f]{20,64}$`)
)

func validGistID(value string) bool { return gistIDPattern.MatchString(value) }

func validServerShareID(value string) bool {
	return shareIDPattern.MatchString(value) && !gistIDPattern.MatchString(value)
}

func shareViewerURL(serverURL *url.URL, id string, key []byte) string {
	viewer := *serverURL
	viewer.Path = path.Join(serverURL.Path, id)
	viewer.RawPath = ""
	viewer.Fragment = EncodeKey(key)
	return viewer.String()
}

type GHGistPublisher struct{}

func (GHGistPublisher) Publish(ctx context.Context, filename, content string) (string, string, error) {
	command := exec.CommandContext(ctx, "gh", "gist", "create", "--secret", "--filename", filename, "-")
	command.Stdin = strings.NewReader(content)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", "", fmt.Errorf("gh gist create: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	gistURL := strings.TrimSpace(stdout.String())
	parsed, err := url.Parse(gistURL)
	if err != nil || parsed.Host == "" {
		return "", "", errors.New("gh gist create returned an invalid URL")
	}
	gistID := path.Base(strings.TrimRight(parsed.Path, "/"))
	return gistURL, gistID, nil
}
