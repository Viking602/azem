package netproxy

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const systemProxyRefreshInterval = 5 * time.Second

// Endpoint is one proxy endpoint from the operating-system network settings.
type Endpoint struct {
	Enabled bool
	Host    string
	Port    int
}

// Settings is the proxy subset shared by the supported desktop platforms.
type Settings struct {
	HTTP                   Endpoint
	HTTPS                  Endpoint
	SOCKS                  Endpoint
	Exceptions             []string
	ExcludeSimpleHostnames bool
}

type resolver struct {
	mu              sync.Mutex
	loadSystem      func() (Settings, error)
	environment     func(*http.Request) (*url.URL, error)
	hasEnvironment  func(*http.Request) bool
	now             func() time.Time
	refreshInterval time.Duration
	cached          Settings
	expiresAt       time.Time
}

var defaultResolver = newResolver(loadSystemProxy)

func newResolver(load func() (Settings, error)) *resolver {
	return &resolver{
		loadSystem: load, environment: http.ProxyFromEnvironment,
		hasEnvironment: environmentProxyConfigured, now: time.Now,
		refreshInterval: systemProxyRefreshInterval,
	}
}

// Proxy resolves environment proxies first, then the native desktop proxy.
// This matches conventional CLI overrides while making Finder-launched desktop
// builds follow the same macOS proxy configuration as Chromium/Electron apps.
func Proxy(request *http.Request) (*url.URL, error) {
	return defaultResolver.proxy(request)
}

// CurrentSystemSettings returns the native settings without environment
// overrides. It is intended for diagnostics and tests, not for serialization.
func CurrentSystemSettings() (Settings, error) {
	return loadSystemProxy()
}

// ConfigureTransport installs Azem's proxy resolver on a transport before the
// transport is used. It returns false for non-standard custom round trippers.
func ConfigureTransport(roundTripper http.RoundTripper) bool {
	transport, ok := roundTripper.(*http.Transport)
	if !ok || transport == nil {
		return false
	}
	transport.Proxy = Proxy
	return true
}

// NewHTTPClient creates an HTTP client that follows environment or native
// desktop proxy settings. A zero timeout preserves streaming semantics.
func NewHTTPClient(timeout time.Duration) *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = Proxy
	return &http.Client{Transport: transport, Timeout: timeout}
}

// InstallDefaultTransport covers libraries that use http.DefaultClient. Azem's
// explicit Resty transports are configured separately because Resty creates
// independent http.Transport instances.
func InstallDefaultTransport() {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return
	}
	transport := base.Clone()
	transport.Proxy = Proxy
	http.DefaultTransport = transport
}

func (r *resolver) proxy(request *http.Request) (*url.URL, error) {
	if r.hasEnvironment(request) {
		return r.environment(request)
	}
	return r.systemSettings().proxyFor(request.URL)
}

func (r *resolver) systemSettings() Settings {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Before(r.expiresAt) {
		return r.cached
	}
	settings, err := r.loadSystem()
	if err == nil {
		r.cached = settings
	}
	r.expiresAt = now.Add(r.refreshInterval)
	return r.cached
}

func (s Settings) proxyFor(target *url.URL) (*url.URL, error) {
	if target == nil || shouldBypass(target.Hostname(), s) {
		return nil, nil
	}
	endpoint := s.HTTP
	if strings.EqualFold(target.Scheme, "https") {
		endpoint = s.HTTPS
	}
	proxyScheme := "http"
	if !validEndpoint(endpoint) {
		endpoint = s.SOCKS
		proxyScheme = "socks5"
	}
	if !validEndpoint(endpoint) {
		return nil, nil
	}
	return &url.URL{
		Scheme: proxyScheme,
		Host:   net.JoinHostPort(strings.TrimSpace(endpoint.Host), strconv.Itoa(endpoint.Port)),
	}, nil
}

func validEndpoint(endpoint Endpoint) bool {
	return endpoint.Enabled && strings.TrimSpace(endpoint.Host) != "" && endpoint.Port > 0 && endpoint.Port <= 65535
}

func environmentProxyConfigured(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	names := []string{"HTTP_PROXY", "http_proxy"}
	if strings.EqualFold(request.URL.Scheme, "https") {
		names = []string{"HTTPS_PROXY", "https_proxy"}
	}
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func shouldBypass(host string, settings Settings) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	if settings.ExcludeSimpleHostnames && !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return true
	}
	for _, exception := range settings.Exceptions {
		if matchesException(host, exception) {
			return true
		}
	}
	return false
}

func matchesException(host, exception string) bool {
	exception = strings.ToLower(strings.TrimSpace(exception))
	if exception == "" {
		return false
	}
	if _, network, err := net.ParseCIDR(exception); err == nil {
		ip := net.ParseIP(host)
		return ip != nil && network.Contains(ip)
	}
	if parsed, err := url.Parse(exception); err == nil && parsed.Hostname() != "" {
		exception = parsed.Hostname()
	}
	if name, _, err := net.SplitHostPort(exception); err == nil {
		exception = name
	}
	if strings.HasPrefix(exception, "*.") {
		suffix := strings.TrimPrefix(exception, "*.")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	if strings.HasPrefix(exception, ".") {
		suffix := strings.TrimPrefix(exception, ".")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	if strings.Contains(exception, "*") {
		matched, _ := path.Match(exception, host)
		return matched
	}
	return host == exception
}
