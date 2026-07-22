package app

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
)

type phase4MeteringDriver struct{ calls int }

func (*phase4MeteringDriver) Metadata() hyprovider.Metadata { return hyprovider.Metadata{} }
func (d *phase4MeteringDriver) Stream(_ context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	d.calls++
	if reporter := responses.RequestUsageReporter(request); reporter != nil {
		reporter(responses.UsageDetails{ProviderRequestID: "upstream", InputTokens: 12, CachedTokens: 5, OutputTokens: 3, TotalTokens: 15, CacheReported: true})
	}
	return hyprovider.NewSliceStream([]hyprovider.Event{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 12, OutputTokens: 3, TotalTokens: 15}}}), nil
}

func TestProviderStreamSinkWithFactsDoesNotEmitLegacyAdditiveUsage(t *testing.T) {
	host := NewService(context.Background(), config.Default())
	host.emit(context.Background(), Event{Kind: EventContextUsage, SessionID: "s", RunID: "r", Data: map[string]string{"factSnapshot": "true"}})
	sink := host.providerStreamSinkWithFacts("s", "r", "p", "m", "high", "responses", true)
	if err := sink.Emit(context.Background(), stream.Frame{Kind: stream.FrameDone, Usage: hyprovider.Usage{InputTokens: 99, CachedInputTokens: 88, OutputTokens: 7}}); err != nil {
		t.Fatal(err)
	}
	event, err := host.NextEvent(context.Background())
	if err != nil || event.Data["factSnapshot"] != "true" {
		t.Fatalf("authoritative event=%#v err=%v", event, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if event, err = host.NextEvent(ctx); err == nil {
		t.Fatalf("legacy additive event emitted: %#v", event)
	}
}

func TestMeteredProviderDriverPersistsTerminalFactsAndUsesDistinctRequestIDs(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "meter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := session.NewService(store.DB())
	if _, err = svc.Ensure(ctx, session.Session{ID: "s", Title: "meter"}); err != nil {
		t.Fatal(err)
	}
	inner := &phase4MeteringDriver{}
	driver := &meteredProviderDriver{inner: inner, store: svc, sessionID: "s", runID: "r", kind: "main", provider: "p", model: "m"}
	for i := 0; i < 2; i++ {
		stream, streamErr := driver.Stream(ctx, hyprovider.Request{})
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		if event, recvErr := stream.Recv(); recvErr != nil || event.Kind != hyprovider.EventDone {
			t.Fatalf("event=%#v err=%v", event, recvErr)
		}
	}
	var rows, distinct, completed int
	if err = store.DB().QueryRow(`SELECT count(*),count(DISTINCT request_id),sum(status='completed') FROM provider_requests`).Scan(&rows, &distinct, &completed); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || distinct != 2 || completed != 2 {
		t.Fatalf("rows=%d distinct=%d completed=%d", rows, distinct, completed)
	}
	snap, err := svc.ProviderUsageSnapshot(ctx, "s", "r")
	if err != nil {
		t.Fatal(err)
	}
	if snap.CurrentTurnMainRequests != 2 || snap.CurrentTurnMainInput != 24 || snap.CurrentTurnMainCached != 10 {
		t.Fatalf("snapshot=%#v", snap)
	}
}

func TestMeteredProviderDriverMarksPrematureEOFFailed(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "eof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := session.NewService(store.DB())
	if _, err = svc.Ensure(ctx, session.Session{ID: "s", Title: "eof"}); err != nil {
		t.Fatal(err)
	}
	driver := &meteredProviderDriver{inner: eofMeteringDriver{}, store: svc, sessionID: "s", runID: "r", kind: "main"}
	stream, err := driver.Stream(ctx, hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err == nil || err != io.EOF {
		t.Fatalf("recv err=%v", err)
	}
	var status string
	if err = store.DB().QueryRow(`SELECT status FROM provider_requests`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

type eofMeteringDriver struct{}

func (eofMeteringDriver) Metadata() hyprovider.Metadata { return hyprovider.Metadata{} }
func (eofMeteringDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	return hyprovider.NewSliceStream(nil), nil
}

type aggregateOnlyMeteringDriver struct{}

func (aggregateOnlyMeteringDriver) Metadata() hyprovider.Metadata { return hyprovider.Metadata{} }
func (aggregateOnlyMeteringDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	return hyprovider.NewSliceStream([]hyprovider.Event{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 12, OutputTokens: 3}}}), nil
}

func TestMeteredProviderDriverDoesNotInferMissingCacheFieldAsZero(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "missing-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := session.NewService(store.DB())
	if _, err := svc.Ensure(ctx, session.Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	driver := &meteredProviderDriver{inner: aggregateOnlyMeteringDriver{}, store: svc, sessionID: "s", runID: "r", kind: "main"}
	stream, err := driver.Stream(ctx, hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.ProviderUsageSnapshot(ctx, "s", "r")
	if err != nil || snapshot.CurrentEpochMainReported || snapshot.CurrentEpochMainReportedRequests != 0 {
		t.Fatalf("missing cache field snapshot=%+v err=%v", snapshot, err)
	}
}

func TestMeteredProviderRetryPreservesFirstTerminalFact(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "terminal-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := session.NewService(store.DB())
	if _, err := svc.Ensure(ctx, session.Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `CREATE TRIGGER fail_usage BEFORE UPDATE OF usage ON session_projections BEGIN SELECT RAISE(FAIL,'once'); END;`); err != nil {
		t.Fatal(err)
	}
	driver := &meteredProviderDriver{inner: &phase4MeteringDriver{}, store: svc, sessionID: "s", runID: "r", kind: "main"}
	stream, err := driver.Stream(ctx, hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("terminal refresh failure was not returned")
	}
	if _, err := store.DB().ExecContext(ctx, `DROP TRIGGER fail_usage`); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("retry EOF=%v", err)
	}
	var status string
	var input, cached int
	if err := store.DB().QueryRowContext(ctx, `SELECT status,input_tokens,cached_tokens FROM provider_requests`).Scan(&status, &input, &cached); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || input != 12 || cached != 5 {
		t.Fatalf("terminal fact status=%s input=%d cached=%d", status, input, cached)
	}
}
