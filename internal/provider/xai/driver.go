package xai

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"resty.dev/v3"

	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

const (
	DefaultEndpoint  = "https://api.x.ai/v1/responses"
	CLIProxyEndpoint = "https://cli-chat-proxy.grok.com/v1/responses"
)

type Transport interface {
	Post(context.Context, []byte) (*resty.Response, error)
	Name() string
}

type Driver struct {
	transport       Transport
	models          []string
	reasoningEffort string
	retryDelay      func(int) time.Duration
	maxRetryDelay   time.Duration
	retryObserver   hyprovider.RetryObserver
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

func (d *Driver) SetRetryObserver(observer hyprovider.RetryObserver) {
	d.retryObserver = observer
}

func (d *Driver) SetMaxRetryDelay(delay time.Duration) {
	d.maxRetryDelay = delay
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	payload, err := responses.Build(request, responses.BuildOptions{IncludeEncryptedReasoning: true, DefaultParallelTools: true, DefaultReasoningEffort: d.reasoningEffort})
	if err != nil {
		return nil, err
	}
	// xAI prompt caching is automatic: route with prompt_cache_key, report hits via
	// cached_tokens only. Drop cache_write_tokens so shared metering/UI do not
	// pretend Anthropic/Codex-style writes exist for Grok.
	reporter := responses.WrapUsageReporter(responses.RequestUsageReporter(request), responses.CacheModelAutomatic)
	open := func() (hyprovider.Stream, error) {
		streamContext, cancel := context.WithCancel(ctx)
		response, err := d.transport.Post(streamContext, payload)
		if err != nil {
			cancel()
			return nil, err
		}
		return responses.Open(response, streamContext, cancel, reporter)
	}
	return hyprovider.OpenRetryingStream(ctx, open, hyprovider.StreamRetryOptions{
		Delay: d.retryDelay, MaxDelay: d.maxRetryDelay, Observer: d.retryObserver,
	})
}

type StandardTransport struct {
	Auth      *auth.Service
	AccountID string
	Endpoint  string
}

func (t *StandardTransport) Name() string { return "xai-responses" }

func (t *StandardTransport) Post(ctx context.Context, payload []byte) (*resty.Response, error) {
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
	return t.Auth.DoStreamWithRefresh(
		ctx,
		"grok",
		t.AccountID,
		resty.MethodPost,
		endpoint,
		func(request *resty.Request) {
			request.SetBody(payload)
			request.SetHeader("Content-Type", "application/json")
			request.SetHeader("Accept", "text/event-stream")
			request.SetHeader("User-Agent", "azem/1")
		},
	)
}

type CLIProxyTransport struct {
	Endpoint string
	Token    func(context.Context) (string, error)
	Headers  map[string]string
	Client   *resty.Client

	clientMu sync.Mutex
}

func (t *CLIProxyTransport) Name() string { return "grok-cli-proxy-responses-experimental" }

func (t *CLIProxyTransport) Post(ctx context.Context, payload []byte) (*resty.Response, error) {
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
	request := t.restyClient().R().
		SetContext(ctx).
		SetResponseDoNotParse(true).
		SetBody(payload).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "text/event-stream").
		SetHeader("User-Agent", "azem/1")
	for name, value := range t.Headers {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "authorization", "host", "content-length":
			continue
		}
		request.SetHeader(name, value)
	}
	return request.SetAuthToken(token).Post(endpoint)
}

func (t *CLIProxyTransport) restyClient() *resty.Client {
	t.clientMu.Lock()
	defer t.clientMu.Unlock()
	if t.Client == nil {
		t.Client = resty.New()
	}
	return t.Client
}

var (
	_ hyprovider.Driver = (*Driver)(nil)
	_ Transport         = (*StandardTransport)(nil)
	_ Transport         = (*CLIProxyTransport)(nil)
)
