package app

import (
	"context"
	"fmt"
)

var agentActionHandlers = map[ActionKind]actionHandler{
	ActionInspectAgent: func(s *Service, ctx context.Context, action Action) error {
		if s.providers == nil {
			return fmt.Errorf("subagent runtime is unavailable")
		}
		blocks, err := s.providers.DetailSubagent(ctx, action.SessionID, action.Target)
		if err != nil {
			return fmt.Errorf("inspect subagent %q: %w", action.Target, err)
		}
		s.emit(ctx, Event{
			Kind: EventAgentDetail, SessionID: action.SessionID, AgentID: action.Target,
			State: "detail", AgentBlocks: blocks,
		})
		return nil
	},
	ActionListAgentTypes: func(s *Service, ctx context.Context, action Action) error {
		s.emit(ctx, Event{Kind: EventAgentDetail, SessionID: action.SessionID, State: "agent_types", AgentCatalog: s.agentTypeCatalog()})
		return nil
	},
	ActionListPersonas: func(s *Service, ctx context.Context, action Action) error {
		s.emit(ctx, Event{Kind: EventAgentDetail, SessionID: action.SessionID, State: "personas", AgentCatalog: s.personaCatalog()})
		return nil
	},
	ActionCancelAgent: func(s *Service, ctx context.Context, action Action) error {
		if s.providers == nil {
			return fmt.Errorf("subagent runtime is unavailable")
		}
		outcome := s.providers.CancelSubagent(action.SessionID, action.Target)
		if outcome.Outcome == "not_found" {
			return fmt.Errorf("subagent %q was not found", action.Target)
		}
		s.emit(ctx, Event{
			Kind: EventAgentDetail, SessionID: action.SessionID, AgentID: action.Target, State: outcome.Outcome,
			Text: map[string]string{
				"cancel_requested": "Child cancellation requested.",
				"already_finished": "Child task already finished.",
			}[outcome.Outcome],
		})
		return nil
	},
}
