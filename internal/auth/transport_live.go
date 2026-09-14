//go:build live

package auth

import "net/http"

// ObserveLiveStream installs test instrumentation before starting live requests.
// The observer must preserve request/response bodies and never log credentials.
// This hook is absent from ordinary builds.
func (s *Service) ObserveLiveStream(observer func(*http.Request, *http.Response, error)) {
	s.streamClient.SetTransport(liveObservedTransport{s.streamClient.Transport(), observer})
}

type liveObservedTransport struct {
	inner   http.RoundTripper
	observe func(*http.Request, *http.Response, error)
}

func (t liveObservedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.inner.RoundTrip(request)
	t.observe(request, response, err)
	return response, err
}
