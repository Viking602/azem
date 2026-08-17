package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEvalTLSUsesInstalledBundleWhenUnset(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "")
	dir := t.TempDir()
	bundle := filepath.Join(dir, "cacert.pem")
	if err := os.WriteFile(bundle, []byte(strings.Repeat("A", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := applyEvalTLSFrom([]string{bundle}); got != bundle {
		t.Fatalf("selected = %q", got)
	}
	if os.Getenv("SSL_CERT_FILE") != bundle {
		t.Fatalf("SSL_CERT_FILE = %q", os.Getenv("SSL_CERT_FILE"))
	}
}

func TestApplyEvalTLSKeepsExplicitBundle(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "/explicit/certs.pem")
	if got := applyEvalTLSFrom([]string{"/installed-agent/cacert.pem"}); got != "/explicit/certs.pem" {
		t.Fatalf("selected = %q", got)
	}
}
