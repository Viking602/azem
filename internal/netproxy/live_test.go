package netproxy

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveSystemProxyReachesChatGPT(t *testing.T) {
	if os.Getenv("AZEM_LIVE_PROXY_TEST") != "1" {
		t.Skip("set AZEM_LIVE_PROXY_TEST=1 to verify the current desktop proxy against ChatGPT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := Proxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil {
		t.Fatal("no proxy resolved for ChatGPT")
	}
	response, err := NewHTTPClient(10 * time.Second).Do(request)
	if err != nil {
		t.Fatalf("ChatGPT through %s: %v", proxyURL.Redacted(), err)
	}
	defer response.Body.Close()
	t.Logf("ChatGPT reached through %s with HTTP %d", proxyURL.Redacted(), response.StatusCode)
}
