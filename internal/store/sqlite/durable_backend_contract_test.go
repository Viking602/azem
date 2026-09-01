package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/durable/contract"
)

func TestDurableBackendContract(t *testing.T) {
	contract.RunBackendContractTests(t, func(t *testing.T) (durable.Backend, func(*testing.T) durable.Backend, func()) {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, "durable.db")
		blobRoot := filepath.Join(root, "blobs")
		ctx := context.Background()
		var (
			mu      sync.Mutex
			current *Provider
		)
		open := func(t *testing.T, closeCurrent bool) durable.Backend {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			if closeCurrent && current != nil {
				if err := current.Close(ctx); err != nil {
					t.Fatalf("Close() before reopen error = %v", err)
				}
				current = nil
			}
			provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			current = provider
			return provider.DurableBackend()
		}
		backend := open(t, false)
		return backend, func(t *testing.T) durable.Backend {
				return open(t, true)
			}, func() {
				mu.Lock()
				defer mu.Unlock()
				if current != nil {
					_ = current.Close(ctx)
				}
			}
	})
}
