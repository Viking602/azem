package responses

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAllowsMissingSSEContentType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := Open(&http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}, ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind == "" {
		t.Fatalf("event=%#v error=%v", event, err)
	}
}

func TestOpenRejectsExplicitNonSSEContentType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}
	if _, err := Open(response, ctx, cancel); err == nil {
		t.Fatal("explicit JSON response accepted as an SSE stream")
	}
}
