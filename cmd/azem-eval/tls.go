package main

import (
	"os"
	"strings"
)

const installedAgentCA = "/installed-agent/cacert.pem"

func applyEvalTLS() string {
	return applyEvalTLSFrom([]string{os.Getenv("SSL_CERT_FILE"), installedAgentCA})
}

func applyEvalTLSFrom(candidates []string) string {
	if existing := strings.TrimSpace(os.Getenv("SSL_CERT_FILE")); existing != "" {
		return existing
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1024 {
			continue
		}
		_ = os.Setenv("SSL_CERT_FILE", candidate)
		return candidate
	}
	return ""
}
