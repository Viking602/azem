package responses

import (
	"context"
	"fmt"
	"mime"

	"resty.dev/v3"
)

func Open(response *resty.Response, ctx context.Context, cancel context.CancelFunc, reporters ...UsageReporter) (*Stream, error) {
	if response.StatusCode()/100 != 2 {
		cancel()
		return nil, HTTPError(response)
	}
	contentType := response.Header().Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if contentType != "" && (err != nil || mediaType != "text/event-stream") {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		cancel()
		return nil, fmt.Errorf("provider returned non-SSE content type %q", contentType)
	}
	if response.Body == nil {
		cancel()
		return nil, fmt.Errorf("provider returned an empty streaming body")
	}
	return NewStream(ctx, cancel, response.Body, reporters...), nil
}
