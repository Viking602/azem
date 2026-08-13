//go:build darwin || windows

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestIndependentWindowDoesNotRegisterAnotherMacApplication(t *testing.T) {
	primary := desktopMacOptions(false)
	if primary.ActivationPolicy != application.ActivationPolicyRegular {
		t.Fatalf("primary activation policy = %d, want regular", primary.ActivationPolicy)
	}
	if !primary.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("primary app must terminate after its last window closes")
	}

	secondary := desktopMacOptions(true)
	if secondary.ActivationPolicy != application.ActivationPolicyAccessory {
		t.Fatalf("secondary activation policy = %d, want accessory", secondary.ActivationPolicy)
	}
	if !secondary.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("secondary app must terminate after its window closes")
	}
}

func TestSessionWindowURLCarriesAssetVersion(t *testing.T) {
	raw := sessionWindowURLWithVersion("session-1", "2026-08-09T11:04:09Z", 42)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("session"); got != "session-1" {
		t.Fatalf("session = %q, want session-1", got)
	}
	if got := parsed.Query().Get("assets"); got != "2026-08-09T11:04:09Z" {
		t.Fatalf("assets = %q, want build timestamp", got)
	}
	if got := parsed.Query().Get("searchSequence"); got != "42" {
		t.Fatalf("searchSequence = %q, want 42", got)
	}
	if got := sessionWindowURLWithVersion("", "unknown", -1); got != "/" {
		t.Fatalf("unknown-version root URL = %q, want /", got)
	}
	zero, err := url.Parse(sessionWindowURLWithVersion("session-0", "unknown", 0))
	if err != nil || zero.Query().Get("searchSequence") != "0" {
		t.Fatalf("zero search sequence was not preserved: %q, %v", zero, err)
	}
}

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
