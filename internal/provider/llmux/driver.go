package llmuxdriver

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/llmux/provider/anthropic"
	"github.com/Viking602/llmux/provider/cohere"
	"github.com/Viking602/llmux/provider/google"
	"github.com/Viking602/llmux/provider/mistral"
	"github.com/Viking602/llmux/provider/openai"
	"github.com/Viking602/llmux/provider/openai/compat"
	sdkxai "github.com/Viking602/llmux/provider/xai"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/provider/responses"
)

type Config struct {
	ProviderID      string
	APIKey          string
	BaseURL         string
	Models          []string
	ReasoningEffort string
}

type Driver struct {
	provider        sdk.Provider
	model           sdk.LanguageModel
	providerID      string
	models          []string
	reasoningEffort string
	retryDelay      func(int) time.Duration
	maxRetryDelay   time.Duration
	retryObserver   hyprovider.RetryObserver
}

func New(config Config) (*Driver, error) {
	config.ProviderID = strings.ToLower(strings.TrimSpace(config.ProviderID))
	if config.ProviderID == "" {
		return nil, fmt.Errorf("llmux provider ID is empty")
	}
	provider, err := newProvider(config)
	if err != nil {
		return nil, err
	}
	var model sdk.LanguageModel
	if len(config.Models) == 1 {
		model, err = provider.LanguageModel(config.Models[0])
		if err != nil {
			return nil, err
		}
	}
	return &Driver{
		provider: provider, model: model, providerID: config.ProviderID,
		models: append([]string(nil), config.Models...), reasoningEffort: config.ReasoningEffort,
	}, nil
}

func newProvider(config Config) (sdk.Provider, error) {
	retry := sdk.RetryPolicy{MaxAttempts: 1}
	if _, ok := compat.Lookup(config.ProviderID); ok {
		return compat.New(config.ProviderID, compat.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	}
	switch config.ProviderID {
	case "openai":
		return openai.New(openai.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	case "anthropic":
		return anthropic.New(anthropic.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	case "google":
		return google.New(google.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	case "mistral":
		return mistral.New(mistral.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	case "cohere":
		return cohere.New(cohere.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	case "xai":
		return sdkxai.New(sdkxai.Config{APIKey: config.APIKey, BaseURL: config.BaseURL, Retry: retry})
	default:
		return nil, fmt.Errorf("llmux provider %q is not a supported language provider", config.ProviderID)
	}
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "llmux:" + d.providerID, Models: append([]string(nil), d.models...), Version: "0.2"}
}

func (d *Driver) SetRetryObserver(observer hyprovider.RetryObserver) { d.retryObserver = observer }
func (d *Driver) SetMaxRetryDelay(delay time.Duration)               { d.maxRetryDelay = delay }

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	converted, err := convertRequest(request, d.reasoningEffort, d.providerID)
	if err != nil {
		return nil, err
	}
	model := d.model
	if model == nil || model.ModelID() != request.Model {
		model, err = d.provider.LanguageModel(request.Model)
		if err != nil {
			return nil, mapError(err)
		}
	}
	reporter := responses.WrapUsageReporter(responses.RequestUsageReporter(request), responses.CacheModelAutomatic)
	open := func() (hyprovider.Stream, error) {
		stream, err := model.Stream(ctx, converted)
		if err != nil {
			return nil, mapError(err)
		}
		return &streamAdapter{inner: stream, reporter: reporter}, nil
	}
	return hyprovider.OpenRetryingStream(ctx, open, hyprovider.StreamRetryOptions{
		Delay: d.retryDelay, MaxDelay: d.maxRetryDelay, Observer: d.retryObserver,
	})
}

var (
	_ hyprovider.Driver                 = (*Driver)(nil)
	_ hyprovider.RetryObservable        = (*Driver)(nil)
	_ hyprovider.RetryDelayConfigurable = (*Driver)(nil)
)
