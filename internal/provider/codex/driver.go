package codex

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"resty.dev/v3"

	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

const (
	DefaultEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	FastServiceTier = "priority"
)

type Driver struct {
	auth            *auth.Service
	accountID       string
	endpoint        string
	models          []string
	toolIDsMu       sync.RWMutex
	toolItemIDs     map[string]string
	reasoningEffort string
	serviceTier     string
}

type turnAffinityKey struct{}

type turnAffinityDriver struct {
	hyprovider.Driver
	routes sync.Map
}

// WithTurnAffinity owns server routing state for exactly one logical turn.
// The runtime creates a new wrapper for each main/Team/Sidekick execution;
// a reused transport driver must never retain the previous turn's token.
func WithTurnAffinity(driver hyprovider.Driver) hyprovider.Driver {
	return &turnAffinityDriver{Driver: driver}
}

func (d *turnAffinityDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	return d.Driver.Stream(context.WithValue(ctx, turnAffinityKey{}, &d.routes), request)
}

func New(authentication *auth.Service, accountID string, endpoint string, models []string, reasoningEffort string) (*Driver, error) {
	if authentication == nil {
		return nil, fmt.Errorf("codex driver auth service is nil")
	}
	if accountID == "" {
		return nil, fmt.Errorf("codex driver account ID is empty")
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Driver{
		auth: authentication, accountID: accountID, endpoint: endpoint,
		models: append([]string(nil), models...), toolItemIDs: make(map[string]string),
		reasoningEffort: reasoningEffort,
	}, nil
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "chatgpt-codex-responses", Models: append([]string(nil), d.models...), Version: "1"}
}

func (d *Driver) SetServiceTier(tier string) {
	d.serviceTier = strings.TrimSpace(tier)
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	cacheKey := promptCacheKey(request)
	request, reverseNames := mapToolNames(request)
	payload, err := responses.BuildContext(ctx, request, responses.BuildOptions{
		IncludeEncryptedReasoning: true, DefaultParallelTools: true, ToolCallItemID: d.toolItemID,
		DefaultReasoningEffort: d.reasoningEffort, ServiceTier: d.serviceTier,
	})
	if err != nil {
		return nil, err
	}
	return d.openStream(ctx, payload, reverseNames, request.Model, cacheKey, nil)
}

func (d *Driver) openStream(ctx context.Context, payload []byte, reverseNames map[string]string, model, cacheKey string, reporter responses.UsageReporter) (hyprovider.Stream, error) {
	routes, _ := ctx.Value(turnAffinityKey{}).(*sync.Map)
	identity := [4]string{d.accountID, d.endpoint, model, cacheKey}
	streamContext, cancel := context.WithCancel(ctx)
	response, err := d.auth.DoStreamWithRefresh(
		streamContext,
		"chatgpt",
		d.accountID,
		resty.MethodPost,
		d.endpoint,
		func(request *resty.Request) {
			request.SetBody(payload)
			request.SetHeader("Content-Type", "application/json")
			request.SetHeader("Accept", "text/event-stream")
			request.SetHeader("OpenAI-Beta", "responses=experimental")
			request.SetHeader("originator", "codex_cli_rs")
			request.SetHeader("User-Agent", "azem/1")
			if routes != nil {
				if state, ok := routes.Load(identity); ok {
					request.SetHeader("x-codex-turn-state", state.(string))
				}
			}
			if cacheKey != "" {
				request.SetHeader("session-id", cacheKey)
				request.SetHeader("thread-id", cacheKey)
				request.SetHeader("conversation_id", cacheKey)
				request.SetHeader("session_id", cacheKey)
			}
		},
	)
	if err != nil {
		cancel()
		return nil, err
	}
	if routes != nil && response.StatusCode()/100 == 2 {
		if state := response.Header().Get("x-codex-turn-state"); state != "" && len(state) <= 8192 {
			// Keep the first server-issued token unchanged, including on retry.
			routes.LoadOrStore(identity, state)
		}
	}
	stream, err := responses.Open(response, streamContext, cancel, reporter)
	if err != nil {
		return nil, err
	}
	return &toolNameStream{inner: stream, reverse: reverseNames, recordItemID: d.recordToolItemID}, nil
}

func promptCacheKey(request hyprovider.Request) string {
	return strings.TrimSpace(request.PromptCacheKey)
}

func (d *Driver) toolItemID(callID string) string {
	d.toolIDsMu.RLock()
	defer d.toolIDsMu.RUnlock()
	return d.toolItemIDs[callID]
}

func (d *Driver) recordToolItemID(callID string, itemID string) {
	d.toolIDsMu.Lock()
	defer d.toolIDsMu.Unlock()
	d.toolItemIDs[callID] = itemID
}

var _ hyprovider.Driver = (*Driver)(nil)
