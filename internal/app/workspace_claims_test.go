package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
)

func TestWorkspaceWriteClaimCanonicalizesRealAndSymlinkAliases(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	realClaim, err := workspaceWriteClaim(real)
	if err != nil {
		t.Fatal(err)
	}
	aliasClaim, err := workspaceWriteClaim(alias)
	if err != nil {
		t.Fatal(err)
	}
	if realClaim != aliasClaim {
		t.Fatalf("real claim=%#v, symlink claim=%#v", realClaim, aliasClaim)
	}
	if realClaim.Mode != agentruntime.ResourceClaimExclusive || !strings.HasPrefix(realClaim.Key, workspaceWriteClaimPrefix) {
		t.Fatalf("workspace claim=%#v", realClaim)
	}
}

func TestWorkspaceWriteClaimCanonicalizesCaseAliasesOnCaseInsensitivePlatforms(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("path case folding is only applied on case-insensitive target platforms")
	}
	real := t.TempDir()
	alias := strings.ToUpper(real)
	if _, err := os.Stat(alias); err != nil {
		t.Skip("test volume is case-sensitive")
	}
	realClaim, err := workspaceWriteClaim(real)
	if err != nil {
		t.Fatal(err)
	}
	aliasClaim, err := workspaceWriteClaim(alias)
	if err != nil {
		t.Fatal(err)
	}
	if realClaim != aliasClaim {
		t.Fatalf("real claim=%#v, case alias claim=%#v", realClaim, aliasClaim)
	}
}

func TestWorkspaceWriteClaimReturnsErrorWhenWorkspaceCannotBeResolved(t *testing.T) {
	_, err := workspaceWriteClaim(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("missing workspace unexpectedly produced a claim")
	}
}

func TestTopLevelWorkspaceWriteClaimsDoNotSerializeSessions(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name        string
		allowWrite  bool
		shellPolicy string
	}{
		{name: "read only", shellPolicy: "deny"},
		{name: "workspace writes", allowWrite: true, shellPolicy: "deny"},
		{name: "shell writes", shellPolicy: "prompt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims, err := topLevelWorkspaceWriteClaims(test.allowWrite, test.shellPolicy, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(claims) != 0 {
				t.Fatalf("top-level sessions must not take exclusive workspace claims: %#v", claims)
			}
		})
	}
}
