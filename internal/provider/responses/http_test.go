package responses

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

type cancelSensitiveBody struct {
	io.ReadCloser
	context context.Context
}

func (body cancelSensitiveBody) Read(data []byte) (int, error) {
	if err := body.context.Err(); err != nil {
		return 0, err
	}
	return body.ReadCloser.Read(data)
}

func TestOpenReadsRejectionBeforeCancellingBody(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	response := testResponse(http.StatusForbidden, make(http.Header), `{"error":{"code":"permission_denied","message":"not permitted"}}`)
	response.Body = cancelSensitiveBody{ReadCloser: response.Body, context: ctx}
	_, err := Open(response, ctx, cancel)
	var rejected *APIError
	if !errors.As(err, &rejected) || rejected.StatusCode != 403 || rejected.Code != "permission_denied" || rejected.Message != "not permitted" || ctx.Err() == nil {
		t.Fatalf("rejection=%+v context=%v", err, ctx.Err())
	}
}

func TestOpenAllowsMissingSSEContentType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := Open(
		testResponse(http.StatusOK, make(http.Header), "data: [DONE]\n\n"),
		ctx,
		cancel,
	)
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
	response := testResponse(http.StatusOK, http.Header{"Content-Type": {"application/json"}}, `{}`)
	if _, err := Open(response, ctx, cancel); err == nil {
		t.Fatal("explicit JSON response accepted as an SSE stream")
	}
}
