package resource

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
)

const DefaultMaxReadBytes = 64 << 20

type URI struct {
	Raw    string `json:"raw"`
	Scheme string `json:"scheme"`
	Opaque string `json:"opaque"`
}

type Scope struct {
	SessionID    string
	RunID        string
	Workspace    string
	ActiveSkills map[string]bool
}

type Request struct {
	URI      URI
	Selector string
	Scope    Scope
}

type Result struct {
	URI       string            `json:"uri"`
	MediaType string            `json:"mediaType,omitempty"`
	Data      []byte            `json:"data,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type Handler interface {
	Read(context.Context, Request) (Result, error)
}

type Writer interface {
	Write(context.Context, Request, Result) (Result, error)
}

type Lister interface {
	List(context.Context, Request) ([]Result, error)
}

var (
	ErrInvalidURI       = errors.New("invalid resource URI")
	ErrSchemeRegistered = errors.New("resource URI scheme is already registered")
	ErrUnsupported      = errors.New("resource operation is unsupported")
	ErrReadLimit        = errors.New("resource exceeds read limit")
)

type Router struct {
	mu           sync.RWMutex
	handlers     map[string]Handler
	maxReadBytes int
}

func NewRouter(maxReadBytes int) *Router {
	if maxReadBytes <= 0 {
		maxReadBytes = DefaultMaxReadBytes
	}
	return &Router{handlers: make(map[string]Handler), maxReadBytes: maxReadBytes}
}

func Parse(raw string) (URI, error) {
	raw = strings.TrimSpace(raw)
	scheme, opaque, found := strings.Cut(raw, "://")
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if !found || !validScheme(scheme) || opaque == "" && scheme != "ssh" || strings.ContainsAny(opaque, "\r\n") {
		return URI{}, fmt.Errorf("%w: %q", ErrInvalidURI, raw)
	}
	return URI{Raw: raw, Scheme: scheme, Opaque: opaque}, nil
}

func (router *Router) Register(scheme string, handler Handler) error {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if router == nil || !validScheme(scheme) || handler == nil {
		return errors.New("resource scheme and handler are required")
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if _, exists := router.handlers[scheme]; exists {
		return fmt.Errorf("%w: %s", ErrSchemeRegistered, scheme)
	}
	router.handlers[scheme] = handler
	return nil
}

func (router *Router) Schemes() []string {
	if router == nil {
		return nil
	}
	router.mu.RLock()
	result := make([]string, 0, len(router.handlers))
	for scheme := range router.handlers {
		result = append(result, scheme)
	}
	router.mu.RUnlock()
	sort.Strings(result)
	return result
}

func (router *Router) Handler(scheme string) Handler {
	if router == nil {
		return nil
	}
	router.mu.RLock()
	defer router.mu.RUnlock()
	return router.handlers[strings.ToLower(strings.TrimSpace(scheme))]
}

func (router *Router) Operations(scheme string) []string {
	if router == nil {
		return nil
	}
	router.mu.RLock()
	handler := router.handlers[strings.ToLower(strings.TrimSpace(scheme))]
	router.mu.RUnlock()
	if handler == nil {
		return nil
	}
	operations := []string{"read"}
	if _, ok := handler.(Writer); ok {
		operations = append(operations, "write")
	}
	if _, ok := handler.(Lister); ok {
		operations = append(operations, "list")
	}
	return operations
}

func (router *Router) Read(ctx context.Context, rawURI, selector string, scope Scope) (Result, error) {
	uri, handler, err := router.resolve(rawURI)
	if err != nil {
		return Result{}, err
	}
	result, err := handler.Read(ctx, Request{URI: uri, Selector: selector, Scope: cloneScope(scope)})
	if err != nil {
		return Result{}, err
	}
	if len(result.Data) > router.maxReadBytes {
		return Result{}, fmt.Errorf("%w: %d > %d", ErrReadLimit, len(result.Data), router.maxReadBytes)
	}
	if result.URI == "" {
		result.URI = uri.Raw
	}
	return cloneResult(result), nil
}

func (router *Router) Write(ctx context.Context, rawURI, selector string, scope Scope, value Result) (Result, error) {
	uri, handler, err := router.resolve(rawURI)
	if err != nil {
		return Result{}, err
	}
	writer, ok := handler.(Writer)
	if !ok {
		return Result{}, fmt.Errorf("%w: write %s", ErrUnsupported, uri.Scheme)
	}
	result, err := writer.Write(ctx, Request{URI: uri, Selector: selector, Scope: cloneScope(scope)}, cloneResult(value))
	if err != nil {
		return Result{}, err
	}
	return cloneResult(result), nil
}

func (router *Router) List(ctx context.Context, rawURI, selector string, scope Scope) ([]Result, error) {
	uri, handler, err := router.resolve(rawURI)
	if err != nil {
		return nil, err
	}
	lister, ok := handler.(Lister)
	if !ok {
		return nil, fmt.Errorf("%w: list %s", ErrUnsupported, uri.Scheme)
	}
	result, err := lister.List(ctx, Request{URI: uri, Selector: selector, Scope: cloneScope(scope)})
	if err != nil {
		return nil, err
	}
	cloned := make([]Result, len(result))
	for index := range result {
		cloned[index] = cloneResult(result[index])
	}
	return cloned, nil
}

func (router *Router) resolve(raw string) (URI, Handler, error) {
	if router == nil {
		return URI{}, nil, errors.New("resource router is nil")
	}
	uri, err := Parse(raw)
	if err != nil {
		return URI{}, nil, err
	}
	router.mu.RLock()
	handler := router.handlers[uri.Scheme]
	router.mu.RUnlock()
	if handler == nil {
		return URI{}, nil, fmt.Errorf("%w: scheme %s", ErrUnsupported, uri.Scheme)
	}
	return uri, handler, nil
}

func validScheme(scheme string) bool {
	if scheme == "" || scheme[0] < 'a' || scheme[0] > 'z' {
		return false
	}
	for _, current := range scheme[1:] {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || current == '+' || current == '-' || current == '.' {
			continue
		}
		return false
	}
	return true
}

func cloneScope(scope Scope) Scope {
	scope.ActiveSkills = maps.Clone(scope.ActiveSkills)
	return scope
}

func cloneResult(result Result) Result {
	result.Data = append([]byte(nil), result.Data...)
	result.Metadata = maps.Clone(result.Metadata)
	return result
}
