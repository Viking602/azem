package app

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Viking602/venat/api"
)

const workspaceWriteClaimPrefix = "azem:workspace-write:"

func canonicalWorkspaceIdentity(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root absolute path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	// Windows and the default macOS filesystems are case-insensitive. Folding
	// their resolved path is conservative on case-sensitive volumes (it may
	// serialize unrelated writers) and prevents case aliases from bypassing the
	// workspace claim.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		resolved = strings.ToLower(resolved)
	}
	return resolved, nil
}

func workspaceWriteClaim(root string) (api.ResourceClaimSpec, error) {
	identity, err := canonicalWorkspaceIdentity(root)
	if err != nil {
		return api.ResourceClaimSpec{}, err
	}
	return api.ResourceClaimSpec{
		Key:  workspaceWriteClaimPrefix + identity,
		Mode: api.ResourceClaimExclusive,
	}, nil
}

func topLevelWorkspaceWriteClaims(allowWrite bool, shellPolicy, root string) ([]api.ResourceClaimSpec, error) {
	if !allowWrite && shellPolicy == "deny" {
		return nil, nil
	}
	claim, err := workspaceWriteClaim(root)
	if err != nil {
		return nil, err
	}
	return []api.ResourceClaimSpec{claim}, nil
}
