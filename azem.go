// Package azem exposes the supported Go embedding boundary for the Azem agent
// runtime. Internal packages remain implementation details; embedders construct
// a Runtime, subscribe to typed events, start turns, and close the runtime.
package azem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

type (
	Event       = app.Event
	EventKind   = app.EventKind
	Session     = session.Session
	Projection  = session.Projection
	SessionTree = session.SessionTree
	Attachment  = session.Attachment
	TodoList    = session.TodoList
	Config      = config.Config
)

type (
	ApprovalHandler  func(ctx context.Context, event Event) (decision string, err error)
	UserInputHandler func(ctx context.Context, event Event) (payload []byte, err error)
	PlanHandler      func(ctx context.Context, event Event) (decision string, err error)
)

type Options struct {
	Workspace        string
	ConfigFile       string
	ApprovalHandler  ApprovalHandler
	UserInputHandler UserInputHandler
	PlanHandler      PlanHandler
}

type Runtime struct {
	service   *app.Service
	sessions  *session.Service
	config    config.Config
	workspace string
	sessionID string

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	nextID   uint64
	subs     map[uint64]*subscriptionState
	closed   bool
	close    sync.Once
	closeErr error

	approvalHandler  ApprovalHandler
	userInputHandler UserInputHandler
	planHandler      PlanHandler
}

func Open(parent context.Context, options Options) (*Runtime, error) {
	return openRuntime(parent, options, true)
}

func openRuntime(parent context.Context, options Options, startEventPump bool) (*Runtime, error) {
	if parent == nil {
		return nil, errors.New("azem: parent context is required")
	}
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		return nil, errors.New("azem: workspace is required")
	}
	ctx, cancel := context.WithCancel(parent)
	boot, err := app.Bootstrap(ctx, workspace, options.ConfigFile)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := boot.Validate(); err != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = boot.Service.Shutdown(shutdownCtx)
		shutdownCancel()
		cancel()
		return nil, err
	}
	boot.Service.SetDesktopSurface(false)
	runtime := &Runtime{
		service: boot.Service, sessions: boot.Service.Sessions(), config: boot.Config, workspace: boot.Paths.Workspace,
		sessionID: boot.SessionID, ctx: ctx, cancel: cancel, subs: make(map[uint64]*subscriptionState),
		approvalHandler: options.ApprovalHandler, userInputHandler: options.UserInputHandler, planHandler: options.PlanHandler,
	}
	if startEventPump {
		go runtime.pumpEvents()
	}
	return runtime, nil
}

func (runtime *Runtime) Workspace() string { return runtime.workspace }
func (runtime *Runtime) SessionID() string { return runtime.sessionID }
func (runtime *Runtime) Config() Config {
	payload, err := json.Marshal(runtime.config)
	if err != nil {
		return Config{}
	}
	var clone Config
	if json.Unmarshal(payload, &clone) != nil {
		return Config{}
	}
	return clone
}

func (runtime *Runtime) LoadSession(ctx context.Context, sessionID string) (Session, error) {
	if runtime == nil || runtime.sessions == nil {
		return Session{}, errors.New("azem: runtime is unavailable")
	}
	return runtime.sessions.LoadSession(ctx, sessionID)
}

func (runtime *Runtime) LoadProjection(ctx context.Context, sessionID string) (Projection, error) {
	if runtime == nil || runtime.sessions == nil {
		return Projection{}, errors.New("azem: runtime is unavailable")
	}
	return runtime.sessions.LoadProjection(ctx, sessionID)
}

func (runtime *Runtime) LoadSessionTree(ctx context.Context, sessionID string) (SessionTree, error) {
	if runtime == nil || runtime.sessions == nil {
		return SessionTree{}, errors.New("azem: runtime is unavailable")
	}
	return runtime.sessions.LoadSessionTree(ctx, sessionID)
}

func (runtime *Runtime) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if runtime == nil || runtime.sessions == nil {
		return nil, errors.New("azem: runtime is unavailable")
	}
	return runtime.sessions.List(ctx, limit)
}

func (runtime *Runtime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.close.Do(func() {
		runtime.closeErr = runtime.service.Shutdown(ctx)
		runtime.cancel()
		runtime.mu.Lock()
		runtime.closed = true
		for id, state := range runtime.subs {
			state.close(EventEnvelope{Err: errors.New("azem: runtime closed")})
			delete(runtime.subs, id)
		}
		runtime.mu.Unlock()
	})
	return runtime.closeErr
}

func (runtime *Runtime) ensureOpen() error {
	if runtime == nil || runtime.service == nil {
		return errors.New("azem: runtime is unavailable")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return errors.New("azem: runtime is closed")
	}
	return nil
}

func (runtime *Runtime) handleInteractiveEvent(event Event) {
	switch event.Kind {
	case app.EventApprovalRequested:
		if runtime.approvalHandler == nil {
			return
		}
		go func() {
			decision, err := runtime.approvalHandler(runtime.ctx, event)
			if err != nil {
				decision = "deny"
			}
			if decision != "once" && decision != "session" && decision != "deny" {
				decision = "deny"
			}
			_ = runtime.service.ExecuteAction(runtime.ctx, app.Action{Kind: app.ActionResolveApproval, Target: event.ApprovalID, Decision: decision, SessionID: event.SessionID})
		}()
	case app.EventUserInputRequested:
		if runtime.userInputHandler == nil {
			return
		}
		go func() {
			payload, err := runtime.userInputHandler(runtime.ctx, event)
			if err != nil {
				runtime.service.CancelActiveWithChildren(true)
				return
			}
			_ = runtime.service.ExecuteAction(runtime.ctx, app.Action{Kind: app.ActionResolveUserInput, Target: event.UserInputID, SessionID: event.SessionID, Payload: payload})
		}()
	case app.EventPlanProposed:
		if runtime.planHandler == nil {
			return
		}
		go func() {
			decision, err := runtime.planHandler(runtime.ctx, event)
			if err != nil || decision != "execute" {
				return
			}
			_ = runtime.service.ExecuteAction(runtime.ctx, app.Action{Kind: app.ActionResolvePlan, Target: event.PlanID, SessionID: event.SessionID, Decision: decision})
		}()
	}
}

func (runtime *Runtime) pumpEvents() {
	for {
		event, err := runtime.service.NextEvent(runtime.ctx)
		if err != nil {
			runtime.closeSubscriptions(err)
			return
		}
		runtime.handleInteractiveEvent(event)
		runtime.mu.Lock()
		for id, state := range runtime.subs {
			if !state.deliver(EventEnvelope{Event: event}) {
				state.close(EventEnvelope{Err: fmt.Errorf("azem: subscriber %d is too slow", id)})
				delete(runtime.subs, id)
			}
		}
		runtime.mu.Unlock()
	}
}

func (runtime *Runtime) closeSubscriptions(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.closed = true
	for id, state := range runtime.subs {
		state.close(EventEnvelope{Err: err})
		delete(runtime.subs, id)
	}
}
