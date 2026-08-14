package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var modelActionHandlers = map[ActionKind]actionHandler{
	ActionListModelRoutes: func(s *Service, ctx context.Context, action Action) error {
		s.emit(ctx, s.modelRoutesEvent("listed"))
		return nil
	},
	ActionListModels: func(s *Service, ctx context.Context, action Action) error {
		s.emitAuthCatalog(ctx)
		return nil
	},
	ActionListModelProviders: func(s *Service, ctx context.Context, action Action) error {
		return s.emitModelProviders(ctx, "listed")
	},
	ActionDiscoverProviderModels: func(s *Service, ctx context.Context, action Action) error {
		if target := strings.TrimSpace(action.Target); target == "chatgpt" || target == "grok" {
			return s.refreshSubscriptionCatalog(ctx, target)
		}
		return s.discoverModelProvider(ctx, action.Provider, action.Secret)
	},
	ActionSetModelProvider: func(s *Service, ctx context.Context, action Action) error {
		return s.updateModelProvider(ctx, action.Provider, action.Secret)
	},
	ActionSetModelEnabled: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(strings.TrimSpace(action.Decision))
		if err != nil {
			return fmt.Errorf("model enabled state must be true or false")
		}
		return s.setModelEnabled(ctx, action.Target, action.Name, enabled)
	},
	ActionSetModelRoute: func(s *Service, ctx context.Context, action Action) error {
		return s.updateModelRoute(ctx, action.Route, false)
	},
	ActionResetModelRoute: func(s *Service, ctx context.Context, action Action) error {
		return s.updateModelRoute(ctx, action.Route, true)
	},
	ActionSetSubagentConcurrency: func(s *Service, ctx context.Context, action Action) error {
		maxConcurrency, err := strconv.Atoi(strings.TrimSpace(action.Target))
		if err != nil || maxConcurrency < 0 {
			return fmt.Errorf("subagent max concurrency must be non-negative")
		}
		return s.updateSubagentMaxConcurrency(ctx, maxConcurrency)
	},
	ActionSetSubagentDepth: func(s *Service, ctx context.Context, action Action) error {
		maxDepth, err := strconv.Atoi(strings.TrimSpace(action.Target))
		if err != nil || maxDepth < -1 {
			return fmt.Errorf("subagent max depth must be -1 or non-negative")
		}
		return s.updateSubagentMaxDepth(ctx, maxDepth)
	},
	ActionSetShellConcurrency: func(s *Service, ctx context.Context, action Action) error {
		maxConcurrency, err := strconv.Atoi(strings.TrimSpace(action.Target))
		if err != nil || maxConcurrency < 1 {
			return fmt.Errorf("shell max concurrency must be positive")
		}
		return s.updateShellMaxConcurrency(ctx, maxConcurrency)
	},
	ActionSetSubagentAwait: func(s *Service, ctx context.Context, action Action) error {
		seconds, err := strconv.Atoi(strings.TrimSpace(action.Target))
		if err != nil || seconds < 5 || seconds > 3600 {
			return fmt.Errorf("subagent await timeout must be between 5 and 3600 seconds")
		}
		return s.updateSubagentAwaitTimeout(ctx, time.Duration(seconds)*time.Second)
	},
	ActionSetChatGPTFastMode: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(strings.TrimSpace(action.Target))
		if err != nil {
			return fmt.Errorf("ChatGPT fast mode must be true or false")
		}
		return s.updateChatGPTFastMode(ctx, enabled)
	},
	ActionSetSessionPreferences: func(s *Service, ctx context.Context, action Action) error {
		return s.updateSessionPreferences(ctx, action)
	},
}
