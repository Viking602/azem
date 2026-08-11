package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type acquiredFence struct {
	fence   RecoveryFence
	recover bool
	err     error
}

func requireRecoveryFence(t *testing.T, result acquiredFence, wantRecover bool) RecoveryFence {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.fence == nil || result.recover != wantRecover {
		t.Fatalf("recovery fence: recover=%v want=%v", result.recover, wantRecover)
	}
	return result.fence
}

func acquireFence(path string) acquiredFence {
	fence, recover, err := AcquireRecoveryFence(context.Background(), path)
	return acquiredFence{fence: fence, recover: recover, err: err}
}

func TestRecoveryFenceKeepsLiveProcessesOutOfCrashRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azem.db")
	first := requireRecoveryFence(t, acquireFence(path), true)
	t.Cleanup(func() { _ = first.Close() })

	secondResult := make(chan acquiredFence, 1)
	go func() {
		secondResult <- acquireFence(path)
	}()
	select {
	case result := <-secondResult:
		if result.fence != nil {
			_ = result.fence.Close()
		}
		t.Fatalf("second fence crossed active recovery: %+v", result)
	case <-time.After(75 * time.Millisecond):
	}
	if err := first.FinishRecovery(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-secondResult:
		second := requireRecoveryFence(t, result, false)
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second fence did not acquire shared runtime lock")
	}
}

func TestRecoveryFenceRetriesWhenOwnerExitsBeforeReady(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azem.db")
	first := requireRecoveryFence(t, acquireFence(path), true)

	result := make(chan bool, 1)
	go func() {
		fence, recover, acquireErr := AcquireRecoveryFence(context.Background(), path)
		if acquireErr == nil && fence != nil {
			_ = fence.FinishRecovery()
			_ = fence.Close()
		}
		result <- acquireErr == nil && recover
	}()
	time.Sleep(75 * time.Millisecond)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case recovered := <-result:
		if !recovered {
			t.Fatal("waiter did not take over incomplete crash recovery")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not retry crash recovery")
	}
}
