package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// FileMutationBroker is the trusted extension seam consulted only after a
// workspace-local ordinary file mutation fails with a permission error.
type FileMutationBroker interface {
	BrokerWrite(context.Context, string, []byte, error, string) (bool, error)
	BrokerDelete(context.Context, string, error, string, bool) (bool, error)
}

type fileMutationBrokerRef struct {
	mu     sync.RWMutex
	broker FileMutationBroker
}

func (ref *fileMutationBrokerRef) set(broker FileMutationBroker) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	ref.broker = broker
	ref.mu.Unlock()
}

func (ref *fileMutationBrokerRef) get() FileMutationBroker {
	if ref == nil {
		return nil
	}
	ref.mu.RLock()
	defer ref.mu.RUnlock()
	return ref.broker
}

func permissionMutationError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// brokerDestination resolves every existing path component before handing a
// target to privileged extension code. The broker never expands Azem's
// workspace boundary, even when an in-workspace path traverses a symlink.
func brokerDestination(rootPath, relative string, followLeaf bool) (string, error) {
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootPath, filepath.FromSlash(relative))
	parent := filepath.Dir(candidate)
	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr == nil {
			parent = resolved
			break
		}
		if !errors.Is(resolveErr, fs.ErrNotExist) {
			return "", resolveErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", resolveErr
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
	for index := len(missing) - 1; index >= 0; index-- {
		parent = filepath.Join(parent, missing[index])
	}
	candidate = filepath.Join(parent, filepath.Base(candidate))
	if followLeaf {
		if _, statErr := os.Lstat(candidate); statErr == nil {
			candidate, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", statErr
		}
	}
	relativeToRoot, err := filepath.Rel(resolvedRoot, candidate)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToRoot) {
		return "", fs.ErrPermission
	}
	return filepath.Clean(candidate), nil
}

func brokerCallerSession(ctx context.Context) string {
	caller, _ := InvocationFromContext(ctx)
	return caller.SessionID
}
