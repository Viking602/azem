package blobstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryRoundTripAndDedup(t *testing.T) {
	store, err := NewDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("artifact"), 1024)
	first, err := store.Put(t.Context(), payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(t.Context(), payload)
	if err != nil || second != first {
		t.Fatalf("dedup digest = %s %s err=%v", first, second, err)
	}
	got, err := store.Get(t.Context(), first)
	if _, err := store.GetLimited(t.Context(), first, int64(len(payload)-1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("bounded read error = %v", err)
	}
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("round trip = %d %v", len(got), err)
	}
	if _, err := store.Get(t.Context(), "not-a-digest"); err == nil {
		t.Fatal("invalid digest was accepted")
	}
	if exists, err := store.Exists(t.Context(), first); err != nil || !exists {
		t.Fatalf("stored blob exists=%v err=%v", exists, err)
	}
	if err := store.Delete(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.Exists(t.Context(), first); err != nil || exists {
		t.Fatalf("deleted blob exists=%v err=%v", exists, err)
	}
}

func TestDirectoryInstallAtReportsOneCreator(t *testing.T) {
	store, err := NewDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("shared-payload"), 1_000)
	digest := Sum(payload)
	type installResult struct {
		created bool
		err     error
	}
	const writers = 16
	start := make(chan struct{})
	results := make(chan installResult, writers)
	for range writers {
		go func() {
			<-start
			created, err := store.InstallAt(t.Context(), digest, payload)
			results <- installResult{created: created, err: err}
		}()
	}
	close(start)
	creators := 0
	for range writers {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			creators++
		}
	}
	if creators != 1 {
		t.Fatalf("creators = %d, want 1", creators)
	}
}

func TestDirectoryRejectsCorruptBlobAndPutRepairsIt(t *testing.T) {
	store, err := NewDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("durable archive source")
	digest, err := store.Put(t.Context(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(digest), bytes.Repeat([]byte("x"), len(payload)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), digest); err == nil || !strings.Contains(err.Error(), "failed integrity check") {
		t.Fatalf("corrupt blob read error = %v", err)
	}
	if repaired, err := store.Put(t.Context(), payload); err != nil || repaired != digest {
		t.Fatalf("repair digest=%q err=%v", repaired, err)
	}
	if got, err := store.Get(t.Context(), digest); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("repaired blob=%q err=%v", got, err)
	}
}

func TestMemoryRejectsDigestPayloadMismatch(t *testing.T) {
	store := NewMemory()
	digest := Sum([]byte("expected"))
	if err := store.PutAt(t.Context(), digest, []byte("different")); err == nil || !strings.Contains(err.Error(), "failed integrity check") {
		t.Fatalf("mismatched PutAt error = %v", err)
	}
}

func TestMemoryIsolatesCopies(t *testing.T) {
	store := NewMemory()
	payload := []byte("live")
	digest, err := store.Put(t.Context(), payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	got, err := store.Get(t.Context(), digest)
	if err != nil || string(got) != "live" {
		t.Fatalf("memory store aliased caller buffer: %q %v", got, err)
	}
	if exists, err := store.Exists(t.Context(), digest); err != nil || !exists {
		t.Fatalf("stored memory blob exists=%v err=%v", exists, err)
	}
	if err := store.Delete(t.Context(), digest); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.Exists(t.Context(), digest); err != nil || exists {
		t.Fatalf("deleted memory blob exists=%v err=%v", exists, err)
	}
	_ = filepath.Separator
}
