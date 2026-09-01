package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
)

func TestDurableBackendRenewReplaysExactResponseAcrossReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "renew.db")
	blobRoot := filepath.Join(root, "blobs")
	provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	backend := provider.DurableBackend()
	started := startDurableCodecTestExecution(t, ctx, backend, "renew-execution")
	leaseRef := durable.LeaseRef{OwnerID: started.Execution.Lease.OwnerID, Token: started.Execution.Lease.Token}
	request := durable.RenewExecutionRequest{
		ExecutionID: "renew-execution",
		Lease:       leaseRef,
		LeaseTTL:    time.Minute,
	}
	first, err := backend.RenewExecution(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := backend.RenewExecution(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("renew replay=%+v, want exact %+v", second, first)
	}
	stored, err := backend.LoadExecution(ctx, request.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Lease.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("replayed heartbeat did not extend stored lease: stored=%v replay=%v", stored.Lease.ExpiresAt, first.ExpiresAt)
	}
	conflicting := request
	conflicting.LeaseTTL = 2 * time.Minute
	if _, err := backend.RenewExecution(ctx, conflicting); !errors.Is(err, durable.ErrConflict) {
		t.Fatalf("renew fingerprint conflict error=%v", err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	reopened, err := provider.DurableBackend().RenewExecution(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if reopened != first {
		t.Fatalf("reopened renew replay=%+v, want exact %+v", reopened, first)
	}
}

func TestDurableBackendRejectsCorruptOrUnknownExecutionCodec(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, filepath.Join(t.TempDir(), "codec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	backend := provider.DurableBackend()

	for _, test := range []struct {
		name     string
		mutate   func(*durableExecutionEnvelope)
		wantText string
	}{
		{
			name: "spec hash mismatch",
			mutate: func(envelope *durableExecutionEnvelope) {
				envelope.Execution.Spec.Request.Prompt = "corrupt"
			},
			wantText: "spec hash mismatch",
		},
		{
			name: "unknown envelope version",
			mutate: func(envelope *durableExecutionEnvelope) {
				envelope.Version = durableStorageEnvelopeVersion + 1
			},
			wantText: "unsupported storage envelope version",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executionID := durable.ExecutionID(strings.ReplaceAll(test.name, " ", "-"))
			startDurableCodecTestExecution(t, ctx, backend, executionID)
			var inline []byte
			if err := provider.DB().QueryRowContext(ctx, `SELECT execution_inline FROM agent_executions WHERE execution_id=?`, executionID).Scan(&inline); err != nil {
				t.Fatal(err)
			}
			var envelope durableExecutionEnvelope
			if err := json.Unmarshal(inline, &envelope); err != nil {
				t.Fatal(err)
			}
			test.mutate(&envelope)
			corrupt, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.DB().ExecContext(ctx, `UPDATE agent_executions SET execution_inline=? WHERE execution_id=?`, corrupt, executionID); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.LoadExecution(ctx, executionID); err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("LoadExecution() error=%v, want %q", err, test.wantText)
			}
		})
	}
}

func startDurableCodecTestExecution(t *testing.T, ctx context.Context, backend durable.Backend, executionID durable.ExecutionID) durable.StartResult {
	t.Helper()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "codec test"}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: executionID,
		OwnerID:     "owner",
		ClaimID:     durable.ClaimID{15: byte(len(executionID))},
		LeaseTTL:    time.Minute,
		Spec:        spec,
		SpecHash:    specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return started
}
