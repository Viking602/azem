package sqlite

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// RecoveryFence keeps crash recovery exclusive while allowing every live Azem
// process to hold a shared runtime lock afterwards. A new window must not
// recover durable runs owned by another still-running process.
type RecoveryFence interface {
	io.Closer
	FinishRecovery() error
}

func AcquireRecoveryFence(ctx context.Context, databasePath string) (RecoveryFence, bool, error) {
	if databasePath == ":memory:" {
		return noopRecoveryFence{}, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, false, err
	}
	return acquireRecoveryFence(ctx, databasePath+".runtime.lock")
}

type noopRecoveryFence struct{}

func (noopRecoveryFence) FinishRecovery() error { return nil }
func (noopRecoveryFence) Close() error          { return nil }
