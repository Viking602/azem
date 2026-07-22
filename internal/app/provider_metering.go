package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

// meteredProviderDriver creates one durable fact immediately before every
// inner Stream call. It deliberately sits inside engine-loop semantics so a
// retry or tool continuation receives a fresh request ID.
type meteredProviderDriver struct {
	inner                                              hyprovider.Driver
	store                                              *session.Service
	host                                               *Service
	sessionID, runID, kind, provider, model, transport string
}

func (d *meteredProviderDriver) Metadata() hyprovider.Metadata { return d.inner.Metadata() }

func (d *meteredProviderDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	id, err := randomID("preq")
	if err != nil {
		return nil, err
	}
	projection, err := d.store.LoadProjection(ctx, d.sessionID)
	if err != nil {
		return nil, err
	}
	fact := session.ProviderRequestFact{RequestID: id, SessionID: d.sessionID, RunID: d.runID, RequestKind: d.kind,
		Provider: d.provider, Model: d.model, Transport: d.transport, CacheEpoch: projection.CacheEpoch,
		CheckpointGeneration: projection.CheckpointGeneration, Status: "started", StartedAt: time.Now().UTC()}
	if err := d.store.UpsertProviderRequest(context.WithoutCancel(ctx), fact); err != nil {
		return nil, err
	}
	state := &meteredRequestState{driver: d, fact: fact}
	if request.ExtraBody == nil {
		request.ExtraBody = make(map[string]any)
	}
	// Fact metering owns all usage/detail accounting. Calling the old reporter
	// here would add the same request to the legacy projection a second time.
	request.ExtraBody[responses.UsageReporterExtraKey] = responses.UsageReporter(state.details)
	stream, err := d.inner.Stream(ctx, request)
	if err != nil {
		if persistErr := state.finish("failed", hyprovider.Usage{}); persistErr != nil && d.host != nil {
			d.host.emit(d.host.ctx, Event{Kind: EventContextUsage, SessionID: d.sessionID, RunID: d.runID, State: "failed",
				Data: map[string]string{"factPersistenceError": persistErr.Error(), "requestKind": d.kind}})
		}
		return nil, err
	}
	return &meteredProviderStream{Stream: stream, state: state}, nil
}

type meteredRequestState struct {
	mu        sync.Mutex
	driver    *meteredProviderDriver
	fact      session.ProviderRequestFact
	terminal  *session.ProviderRequestFact
	factSaved bool
	detail    responses.UsageDetails
	finished  bool
	finishErr error
}

func (s *meteredRequestState) details(d responses.UsageDetails) {
	s.mu.Lock()
	s.detail = d
	s.mu.Unlock()
}
func (s *meteredRequestState) finish(status string, usage hyprovider.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return s.finishErr
	}
	if s.terminal == nil {
		f := s.fact
		d := s.detail
		f.ProviderRequestID = d.ProviderRequestID
		f.Status = status
		f.CompletedAt = time.Now().UTC()
		f.InputTokens, f.CachedTokens, f.OutputTokens, f.TotalTokens = usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.TotalTokens
		if d.InputTokens != 0 || d.OutputTokens != 0 || d.TotalTokens != 0 {
			f.InputTokens, f.CachedTokens, f.OutputTokens, f.TotalTokens = d.InputTokens, d.CachedTokens, d.OutputTokens, d.TotalTokens
		}
		f.CacheWriteTokens, f.ReasoningTokens, f.CacheReported = d.CacheWriteTokens, d.ReasoningTokens, d.CacheReported
		s.terminal = &f
	}
	f := *s.terminal
	baseCtx := context.Background()
	if s.driver.host != nil && s.driver.host.ctx != nil {
		baseCtx = s.driver.host.ctx
	}
	ctx := context.WithoutCancel(baseCtx)
	if !s.factSaved {
		if err := s.driver.store.UpsertProviderRequest(ctx, f); err != nil {
			s.finishErr = err
			return err
		}
		s.factSaved = true
	}
	u, err := s.driver.store.ProviderUsageSnapshot(ctx, f.SessionID, f.RunID)
	if err != nil {
		s.finishErr = err
		return err
	}
	if err = s.driver.store.UpdateUsage(ctx, f.SessionID, u); err != nil {
		s.finishErr = err
		return err
	}
	encoded, err := json.Marshal(u)
	if err != nil {
		s.finishErr = err
		return err
	}
	eventState := status
	if status == "completed" {
		eventState = "reported"
	}
	if s.driver.host != nil {
		s.driver.host.emit(ctx, Event{Kind: EventContextUsage, SessionID: f.SessionID, RunID: f.RunID, State: eventState,
			Data: map[string]string{"factSnapshot": "true", "usageSnapshot": string(encoded), "requestKind": f.RequestKind,
				"inputTokens": fmt.Sprint(f.InputTokens), "cachedInputTokens": fmt.Sprint(f.CachedTokens), "outputTokens": fmt.Sprint(f.OutputTokens),
				"totalTokens": fmt.Sprint(f.TotalTokens), "cacheWriteTokens": fmt.Sprint(f.CacheWriteTokens), "reasoningTokens": fmt.Sprint(f.ReasoningTokens),
				"provider": f.Provider, "model": f.Model, "transport": f.Transport,
				"cacheStatus": map[bool]string{true: "reported", false: "unreported"}[f.CacheReported]}})
	}
	s.finished = true
	s.finishErr = nil
	return nil
}

type meteredProviderStream struct {
	hyprovider.Stream
	state *meteredRequestState
}

func (s *meteredProviderStream) Recv() (hyprovider.Event, error) {
	e, err := s.Stream.Recv()
	if err != nil {
		if finishErr := s.state.finish("failed", hyprovider.Usage{}); finishErr != nil {
			return e, finishErr
		}
		return e, err
	}
	if e.Kind == hyprovider.EventDone {
		status := "completed"
		if e.StopReason == hyprovider.StopReasonAborted || e.StopReason == hyprovider.StopReasonError {
			status = "failed"
		}
		if err := s.state.finish(status, e.Usage); err != nil {
			return hyprovider.Event{}, fmt.Errorf("persist provider request fact: %w", err)
		}
	}
	if e.Kind == hyprovider.EventError {
		if err := s.state.finish("failed", hyprovider.Usage{}); err != nil {
			return hyprovider.Event{}, fmt.Errorf("persist provider request fact: %w", err)
		}
	}
	return e, nil
}
