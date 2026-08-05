//go:build darwin || windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidSessionID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "generated", id: "session_0123456789abcdefABCDEF01", want: true},
		{name: "legacy numeric", id: "session-1", want: true},
		{name: "legacy slug", id: "session-imported_2026", want: true},
		{name: "wrong generated length", id: "session_0123", want: false},
		{name: "non-hex generated suffix", id: "session_0123456789abcdefghij0000", want: false},
		{name: "nested path", id: "session-1/../../other", want: false},
		{name: "encoded path residue", id: "session-1%2Fother", want: false},
		{name: "missing prefix", id: "0123456789abcdefABCDEF01", want: false},
		{name: "empty legacy suffix", id: "session-", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validSessionID(test.id); got != test.want {
				t.Fatalf("validSessionID(%q) = %t, want %t", test.id, got, test.want)
			}
		})
	}
}

func TestSameWorkspaceRejectsDifferentDeepLinkWorkspace(t *testing.T) {
	trusted := t.TempDir()
	untrusted := t.TempDir()
	if sameWorkspace(untrusted, trusted) {
		t.Fatal("different workspace was accepted")
	}
	link := filepath.Join(t.TempDir(), "trusted-link")
	if err := os.Symlink(trusted, link); err != nil {
		t.Fatal(err)
	}
	if !sameWorkspace(link, trusted) {
		t.Fatal("canonical path to the same workspace was rejected")
	}
}
