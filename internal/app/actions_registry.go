package app

import (
	"context"
	"fmt"
)

// actionHandler executes one ActionKind. Handlers are registered in the
// domain-scoped maps below (sessions, runtime, models, memory, extensions,
// agents, workspace) and merged into a single registry at package init, which
// panics on duplicate registration so a kind can never be silently rebound.
type actionHandler func(*Service, context.Context, Action) error

var actionRegistry = mergeActionHandlers(
	sessionActionHandlers,
	runtimeActionHandlers,
	modelActionHandlers,
	memoryActionHandlers,
	extensionActionHandlers,
	agentActionHandlers,
	workspaceActionHandlers,
)

func mergeActionHandlers(groups ...map[ActionKind]actionHandler) map[ActionKind]actionHandler {
	merged := make(map[ActionKind]actionHandler)
	for _, group := range groups {
		for kind, handler := range group {
			if handler == nil {
				panic(fmt.Sprintf("action %q registered a nil handler", kind))
			}
			if _, exists := merged[kind]; exists {
				panic(fmt.Sprintf("action %q is registered twice", kind))
			}
			merged[kind] = handler
		}
	}
	return merged
}

func (s *Service) ExecuteAction(ctx context.Context, action Action) error {
	handler, ok := actionRegistry[action.Kind]
	if !ok {
		return fmt.Errorf("unsupported action %q", action.Kind)
	}
	return handler(s, ctx, action)
}
