package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
)

func TestExecutionBindingsUseStableIdentityAndImmutableStartedManifest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "bindings.db")
	blobRoot := filepath.Join(root, "blobs")
	provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}

	binding := testExecutionBinding(t, "main", "run-1", "run-1", 0)
	binding.Manifest.Prompt = strings.Repeat("durable prompt ", 500)
	saved, err := provider.SaveExecutionBinding(ctx, binding, 0)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved version=%d updatedAt=%v", saved.Version, saved.UpdatedAt)
	}
	var inline []byte
	var digest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT manifest_inline,manifest_digest FROM agent_execution_bindings WHERE execution_id=?`, saved.ExecutionID).Scan(&inline, &digest); err != nil {
		t.Fatal(err)
	}
	if string(inline) != "{}" || digest == "" {
		t.Fatalf("large manifest inline=%q digest=%q", inline, digest)
	}

	saved.Manifest.Metadata["mutable-return"] = "changed"
	loaded, err := provider.LoadExecutionBinding(ctx, binding.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.Manifest.Metadata["mutable-return"]; exists {
		t.Fatal("LoadExecutionBinding returned aliased manifest metadata")
	}

	loaded.Manifest.Metadata["provider"] = "sealed"
	loaded.Manifest.ProfileHash = "profile-sealed"
	loaded.ProfileHash = "profile-sealed"
	sealed, err := provider.SaveExecutionBinding(ctx, loaded, loaded.Version)
	if err != nil {
		t.Fatalf("seal pending manifest: %v", err)
	}
	if exists, err := provider.Blobs().Exists(ctx, digest); err != nil || exists {
		t.Fatalf("superseded manifest blob exists=%v error=%v", exists, err)
	}
	sealed.State = agentruntime.ExecutionBindingRunning
	running, err := provider.SaveExecutionBinding(ctx, sealed, sealed.Version)
	if err != nil {
		t.Fatalf("start binding: %v", err)
	}
	if running.Version != 3 {
		t.Fatalf("running version=%d, want 3", running.Version)
	}

	mutated := running
	mutated.Manifest.Prompt = "different prompt"
	mutated.Manifest.ProfileHash = "different-profile"
	mutated.ProfileHash = "different-profile"
	if _, err := provider.SaveExecutionBinding(ctx, mutated, running.Version); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("started manifest mutation error=%v, want conflict", err)
	}
	stale := running
	stale.State = agentruntime.ExecutionBindingCompleted
	if _, err := provider.SaveExecutionBinding(ctx, stale, running.Version-1); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("stale update error=%v, want conflict", err)
	}

	running.State = agentruntime.ExecutionBindingSuspended
	suspended, err := provider.SaveExecutionBinding(ctx, running, running.Version)
	if err != nil {
		t.Fatal(err)
	}
	segmentOne := testExecutionBinding(t, "main", "run-1", "run-1", 1)
	segmentOne.State = agentruntime.ExecutionBindingPending
	if _, err := provider.SaveExecutionBinding(ctx, segmentOne, 0); err != nil {
		t.Fatal(err)
	}
	latest, err := provider.LoadLatestExecutionBinding(ctx, "run-1", "agent-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ExecutionID != segmentOne.ExecutionID || latest.Segment != 1 {
		t.Fatalf("latest binding=%+v", latest)
	}
	listed, err := provider.ListExecutionBindings(ctx, []agentruntime.ExecutionBindingState{agentruntime.ExecutionBindingSuspended})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ExecutionID != suspended.ExecutionID {
		t.Fatalf("suspended bindings=%+v", listed)
	}

	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	reopened, err := provider.LoadExecutionBinding(ctx, suspended.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != agentruntime.ExecutionBindingSuspended || reopened.ProfileHash != "profile-sealed" || reopened.Manifest.Prompt != binding.Manifest.Prompt {
		t.Fatalf("reopened binding=%+v", reopened)
	}
}

func TestExecutionBindingRejectsUnversionedOrMismatchedIdentity(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, filepath.Join(t.TempDir(), "bindings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	binding := testExecutionBinding(t, "team", "task-1", "run-1", 0)
	binding.ExecutionID = "team-task-1"
	if _, err := provider.SaveExecutionBinding(ctx, binding, 0); err == nil {
		t.Fatal("unversioned execution id was accepted")
	}
	binding = testExecutionBinding(t, "team", "task-1", "run-1", 0)
	binding.Manifest.StableID = "task-2"
	if _, err := provider.SaveExecutionBinding(ctx, binding, 0); err == nil {
		t.Fatal("manifest identity mismatch was accepted")
	}
	binding = testExecutionBinding(t, "team", "task-1", "run-1", 0)
	binding.State = agentruntime.ExecutionBindingState("unknown")
	if _, err := provider.SaveExecutionBinding(ctx, binding, 0); err == nil {
		t.Fatal("unknown binding state was accepted")
	}
}

func testExecutionBinding(t *testing.T, kind, stableID, runID string, segment int) agentruntime.ExecutionBinding {
	t.Helper()
	executionID, err := agentruntime.ExecutionID(kind, stableID, segment)
	if err != nil {
		t.Fatal(err)
	}
	manifest := agentruntime.ExecutionManifest{
		Version:      agentruntime.ExecutionManifestVersion,
		SessionID:    "session-1",
		RunID:        runID,
		AgentID:      "agent-1",
		StableID:     stableID,
		AgentVersion: "v1",
		Kind:         kind,
		Segment:      segment,
		Prompt:       "prompt",
		Metadata:     map[string]string{"source": "test"},
		ProfileHash:  "profile-1",
		StartedAt:    time.Unix(1_700_000_000, 0).UTC(),
	}
	return agentruntime.ExecutionBinding{
		ExecutionID: executionID,
		SessionID:   manifest.SessionID,
		RunID:       runID,
		StableID:    stableID,
		AgentID:     manifest.AgentID,
		Kind:        kind,
		Segment:     segment,
		Manifest:    manifest,
		ProfileHash: manifest.ProfileHash,
		State:       agentruntime.ExecutionBindingPending,
	}
}
