package responses

import (
	"context"
	"fmt"
	"mime"
	"net/http"
)

func Open(response *http.Response, ctx context.Context, cancel context.CancelFunc) (*Stream, error) {
	if response.StatusCode/100 != 2 {
		cancel()
		return nil, HTTPError(response)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if contentType != "" && (err != nil || mediaType != "text/event-stream") {
		response.Body.Close()
		cancel()
		return nil, fmt.Errorf("provider returned non-SSE content type %q", response.Header.Get("Content-Type"))
	}
	return NewStream(ctx, cancel, response.Body), nil
}
