package resource

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type memoryHandler struct {
	value Result
}

func (handler *memoryHandler) Read(_ context.Context, request Request) (Result, error) {
	result := handler.value
	result.Metadata = map[string]string{"opaque": request.URI.Opaque, "selector": request.Selector}
	return result, nil
}

func (handler *memoryHandler) Write(_ context.Context, _ Request, value Result) (Result, error) {
	handler.value = value
	return value, nil
}

func TestRouterParsesRoutesAndClonesResources(t *testing.T) {
	router := NewRouter(32)
	handler := &memoryHandler{value: Result{Data: []byte("value")}}
	if err := router.Register("memory", handler); err != nil {
		t.Fatal(err)
	}
	result, err := router.Read(context.Background(), "memory://key/path", "1-2", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "value" || result.Metadata["opaque"] != "key/path" || result.Metadata["selector"] != "1-2" {
		t.Fatalf("read result = %#v", result)
	}
	result.Data[0] = 'X'
	if string(handler.value.Data) != "value" {
		t.Fatal("router returned handler-owned bytes")
	}
	if _, err := router.Write(context.Background(), "memory://key/path", "", Scope{}, Result{Data: []byte("next")}); err != nil {
		t.Fatal(err)
	}
	if got := router.Schemes(); !reflect.DeepEqual(got, []string{"memory"}) {
		t.Fatalf("schemes = %v", got)
	}
}

func TestRouterRejectsInvalidDuplicateUnsupportedAndOversized(t *testing.T) {
	router := NewRouter(2)
	handler := &memoryHandler{value: Result{Data: []byte("large")}}
	if err := router.Register("memory", handler); err != nil {
		t.Fatal(err)
	}
	if err := router.Register("memory", handler); !errors.Is(err, ErrSchemeRegistered) {
		t.Fatalf("duplicate scheme error = %v", err)
	}
	if _, err := router.Read(context.Background(), "missing://value", "", Scope{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported scheme error = %v", err)
	}
	if _, err := router.Read(context.Background(), "not-a-uri", "", Scope{}); !errors.Is(err, ErrInvalidURI) {
		t.Fatalf("invalid URI error = %v", err)
	}
	if _, err := router.Read(context.Background(), "memory://value", "", Scope{}); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("read limit error = %v", err)
	}
}
