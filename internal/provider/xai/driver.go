package xai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

const (
	DefaultEndpoint  = "https://api.x.ai/v1/responses"
	CLIProxyEndpoint = "https://cli-chat-proxy.grok.com/v1/responses"
)

type Transport interface {
	Post(context.Context, []byte) (*http.Response, error)
	Name() string
}

type Driver struct {
	transport       Transport
	models          []string
	reasoningEffort string
}

func New(transport Transport, models []string, reasoningEffort string) (*Driver, error) {
	if transport == nil {
		return nil, fmt.Errorf("xAI driver transport is nil")
	}
	return &Driver{transport: transport, models: append([]string(nil), models...), reasoningEffort: reasoningEffort}, nil
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: d.transport.Name(), Models: append([]string(nil), d.models...), Version: "1"}
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	payload, err := responses.Build(request, responses.BuildOptions{DefaultParallelTools: true, DefaultReasoningEffort: d.reasoningEffort})
	if err != nil {
		return nil, err
	}
	streamContext, cancel := context.WithCancel(ctx)
	response, err := d.transport.Post(streamContext, payload)
	if err != nil {
		cancel()
		return nil, err
	}
	return responses.Open(response, streamContext, cancel)
}

type StandardTransport struct {
	Auth      *auth.Service
	AccountID string
	Endpoint  string
}

func (t *StandardTransport) Name() string { return "xai-responses" }

func (t *StandardTransport) Post(ctx context.Context, payload []byte) (*http.Response, error) {
	if t.Auth == nil {
		return nil, fmt.Errorf("xAI standard transport auth service is nil")
	}
	if t.AccountID == "" {
		return nil, fmt.Errorf("xAI standard transport account ID is empty")
	}
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return t.Auth.DoStreamWithRefresh(ctx, "grok", t.AccountID, func(auth.Credential) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("User-Agent", "azem/1")
		return request, nil
	})
}

type CLIProxyTransport struct {
	Endpoint string
	Token    func(context.Context) (string, error)
	Headers  map[string]string
	HTTP     *http.Client
}

func (t *CLIProxyTransport) Name() string { return "grok-cli-proxy-responses-experimental" }

func (t *CLIProxyTransport) Post(ctx context.Context, payload []byte) (*http.Response, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = CLIProxyEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com") {
		return nil, fmt.Errorf("Grok CLI proxy endpoint must be https://cli-chat-proxy.grok.com")
	}
	if t.Token == nil {
		return nil, fmt.Errorf("Grok CLI proxy token provider is nil")
	}
	token, err := t.Token(ctx)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("User-Agent", "azem/1")
	for name, value := range t.Headers {
		canonical := http.CanonicalHeaderKey(name)
		switch canonical {
		case "Authorization", "Host", "Content-Length":
			continue
		}
		request.Header.Set(canonical, value)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := t.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(request)
}

var _ hyprovider.Driver = (*Driver)(nil)
var _ Transport = (*StandardTransport)(nil)
var _ Transport = (*CLIProxyTransport)(nil)
