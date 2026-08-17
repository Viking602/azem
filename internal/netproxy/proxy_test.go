package netproxy

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestSettingsResolveHTTPSAndRespectExceptions(t *testing.T) {
	settings := Settings{
		HTTP:                   Endpoint{Enabled: true, Host: "127.0.0.1", Port: 8080},
		HTTPS:                  Endpoint{Enabled: true, Host: "127.0.0.1", Port: 8443},
		SOCKS:                  Endpoint{Enabled: true, Host: "127.0.0.1", Port: 1080},
		Exceptions:             []string{"*.local", "10.0.0.0/8", "example.internal"},
		ExcludeSimpleHostnames: true,
	}
	for target, want := range map[string]string{
		"https://chatgpt.com/backend-api/codex/responses": "http://127.0.0.1:8443",
		"http://models.dev/api.json":                      "http://127.0.0.1:8080",
		"https://service.local/api":                       "",
		"https://10.4.3.2/api":                            "",
		"https://printer/api":                             "",
		"https://example.internal/api":                    "",
	} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		proxyURL, err := settings.proxyFor(parsed)
		if err != nil {
			t.Fatalf("proxyFor(%q): %v", target, err)
		}
		got := ""
		if proxyURL != nil {
			got = proxyURL.String()
		}
		if got != want {
			t.Fatalf("proxyFor(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestSettingsFallBackToSOCKS(t *testing.T) {
	settings := Settings{SOCKS: Endpoint{Enabled: true, Host: "127.0.0.1", Port: 6153}}
	parsed, _ := url.Parse("https://chatgpt.com")
	proxyURL, err := settings.proxyFor(parsed)
	if err != nil || proxyURL == nil || proxyURL.String() != "socks5://127.0.0.1:6153" {
		t.Fatalf("proxy = %v, error = %v", proxyURL, err)
	}
}

func TestResolverPrefersEnvironmentAndRefreshesSystemSettings(t *testing.T) {
	now := time.Unix(100, 0)
	loads := 0
	resolver := newResolver(func() (Settings, error) {
		loads++
		return Settings{HTTPS: Endpoint{Enabled: true, Host: "127.0.0.1", Port: 6000 + loads}}, nil
	})
	resolver.hasEnvironment = func(*http.Request) bool { return false }
	resolver.now = func() time.Time { return now }
	resolver.refreshInterval = time.Second
	request, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)

	first, err := resolver.proxy(request)
	if err != nil || first.String() != "http://127.0.0.1:6001" {
		t.Fatalf("first proxy = %v, error = %v", first, err)
	}
	second, _ := resolver.proxy(request)
	if second.String() != first.String() || loads != 1 {
		t.Fatalf("cached proxy = %v, loads = %d", second, loads)
	}
	now = now.Add(2 * time.Second)
	refreshed, _ := resolver.proxy(request)
	if refreshed.String() != "http://127.0.0.1:6002" || loads != 2 {
		t.Fatalf("refreshed proxy = %v, loads = %d", refreshed, loads)
	}

	resolver.hasEnvironment = func(*http.Request) bool { return true }
	resolver.environment = func(*http.Request) (*url.URL, error) {
		return url.Parse("http://environment.proxy:9000")
	}
	environment, _ := resolver.proxy(request)
	if environment.String() != "http://environment.proxy:9000" || loads != 2 {
		t.Fatalf("environment proxy = %v, loads = %d", environment, loads)
	}
}

func TestEnvironmentProxyDetectionIsProtocolSpecific(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8080")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	httpRequest, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	httpsRequest, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if !environmentProxyConfigured(httpRequest) {
		t.Fatal("HTTP proxy should apply to HTTP requests")
	}
	if environmentProxyConfigured(httpsRequest) {
		t.Fatal("HTTP_PROXY alone must not suppress the native HTTPS proxy")
	}
}

func TestConfigureTransportPreservesTransportAndInstallsResolver(t *testing.T) {
	transport := &http.Transport{MaxIdleConns: 17}
	if !ConfigureTransport(transport) || transport.Proxy == nil || transport.MaxIdleConns != 17 {
		t.Fatalf("transport was not configured: %#v", transport)
	}
	if ConfigureTransport(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })) {
		t.Fatal("custom round tripper should not be mutated")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGrokUserURLUsesNativeHTTPSProxyWhenEnvironmentIsEmpty(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	request, err := http.NewRequest(http.MethodGet, "https://cli-chat-proxy.grok.com/v1/user?include=subscription", nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newResolver(func() (Settings, error) {
		return Settings{HTTPS: Endpoint{Enabled: true, Host: "127.0.0.1", Port: 6152}}, nil
	})
	resolver.hasEnvironment = func(*http.Request) bool { return false }
	proxyURL, err := resolver.proxy(request)
	if err != nil || proxyURL == nil || proxyURL.String() != "http://127.0.0.1:6152" {
		t.Fatalf("system proxy = %v, error = %v", proxyURL, err)
	}

	direct := newResolver(func() (Settings, error) { return Settings{}, nil })
	direct.hasEnvironment = func(*http.Request) bool { return false }
	if got, err := direct.proxy(request); err != nil || got != nil {
		t.Fatalf("empty system settings must not invent a proxy: %v %v", got, err)
	}
}

func TestCurrentSystemSettingsAreSelfConsistent(t *testing.T) {
	settings, err := CurrentSystemSettings()
	if err != nil {
		t.Fatal(err)
	}
	for name, endpoint := range map[string]Endpoint{"http": settings.HTTP, "https": settings.HTTPS, "socks": settings.SOCKS} {
		if endpoint.Enabled && !validEndpoint(endpoint) {
			t.Fatalf("enabled %s proxy is invalid: %#v", name, endpoint)
		}
		t.Logf("%s proxy: enabled=%t host=%s port=%d", name, endpoint.Enabled, endpoint.Host, endpoint.Port)
	}
}
